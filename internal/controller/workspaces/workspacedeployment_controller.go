/*
Copyright 2025 Exalsius contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workspaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	"github.com/exalsius/exalsius-operator/internal/controller/infra/common"
	"github.com/exalsius/exalsius-operator/internal/gpu"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

const (
	requeueInterval = 15 * time.Second
)

// WorkspaceDeploymentReconciler reconciles a WorkspaceDeployment object.
type WorkspaceDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// RouteProvider materializes external access for workspace endpoints
	// (ADR-0001). Nil disables routing entirely — workspaces still deploy,
	// status.access[] stays empty.
	RouteProvider routing.RouteProvider
	// Recorder emits Kubernetes events on phase transitions; they feed
	// status.failureContext.recentEvents.
	Recorder events.EventRecorder
	// APIReader reads events uncached so the manager cache never has to
	// hold all cluster events. Nil disables event capture.
	APIReader client.Reader
	// MeshNamespaceLabels are the Istio mesh-enrollment labels stamped on the
	// child workspace namespace (from --workspace-mesh-mode). Nil/empty = no
	// enrollment label (mesh-mode none).
	MeshNamespaceLabels map[string]string
}

// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspaceclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=servicesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

func (r *WorkspaceDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the WorkspaceDeployment
	wsd := &workspacesv1.WorkspaceDeployment{}
	if err := r.Get(ctx, req.NamespacedName, wsd); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle suspend
	if wsd.Spec.Suspend {
		log.Info("WorkspaceDeployment is suspended, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Handle deletion
	if !wsd.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, wsd)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer) {
		controllerutil.AddFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer)
		if err := r.Update(ctx, wsd); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve WorkspaceClass
	wsc, err := r.resolveWorkspaceClass(ctx, wsd)
	if err != nil {
		return ctrl.Result{}, err
	}
	if wsc == nil {
		// Class not found — status already updated by resolveWorkspaceClass
		return ctrl.Result{}, nil
	}

	// Set ClassResolved=True
	setCondition(&wsd.Status, metav1.Condition{
		Type:               workspacesv1.ConditionClassResolved,
		Status:             metav1.ConditionTrue,
		Reason:             workspacesv1.ReasonClassResolved,
		Message:            fmt.Sprintf("WorkspaceClass %q resolved", wsd.Spec.WorkspaceClassRef),
		ObservedGeneration: wsd.Generation,
	})

	// Verify the referenced ClusterDeployment exists before anything that
	// depends on it (feasibility, child-cluster client, ServiceSet). A
	// missing CD is non-terminal: keep the workspace Pending and requeue so
	// it self-heals if the cluster is still provisioning or applied out of
	// order. Short-circuit here so the dependent steps don't emit misleading
	// "could not reach regional cluster" / "waiting for child cluster access"
	// errors. Skipped once Running (already validated; teardown handles a CD
	// that disappears later).
	if wsd.Status.Phase != workspacesv1.WorkspaceDeploymentPhaseRunning {
		if res, ok, err := r.resolveClusterDeployment(ctx, wsd); err != nil || !ok {
			return res, err
		}
	}

	// Refresh feasibility status while the workspace is still being set up.
	// This is observability only — no gating; the API gates user-facing
	// admission. Skipped once Running, terminally Failed, or held Waiting for
	// GPU capacity: in those states it would either be stale or fight the gate
	// over status fields. We swallow regional-cluster failures and requeue.
	switch wsd.Status.Phase {
	case workspacesv1.WorkspaceDeploymentPhaseRunning,
		workspacesv1.WorkspaceDeploymentPhaseFailed,
		workspacesv1.WorkspaceDeploymentPhaseWaiting:
		// skip
	default:
		if res, err := r.refreshFeasibility(ctx, wsd, wsc); err != nil {
			return res, err
		}
	}

	// Gate on GPU offering existence and capacity before deploying (ADR-0002):
	// fail fast if the requested GPU model is absent, or hold in Waiting if it
	// exists but none are free. Skipped once the workspace has progressed past
	// the decision to deploy (Deploying/Running) or terminally Failed — a
	// deployed workspace must never be re-gated against capacity it now holds.
	switch wsd.Status.Phase {
	case workspacesv1.WorkspaceDeploymentPhaseDeploying,
		workspacesv1.WorkspaceDeploymentPhaseRunning,
		workspacesv1.WorkspaceDeploymentPhaseFailed:
		// skip
	default:
		if res, ok, err := r.gateGPUOffering(ctx, wsd, wsc); err != nil || !ok {
			return res, err
		}
	}

	// Auto-install prerequisites declared on the WorkspaceClass. Skipped
	// entirely when no prereqs are declared — the common case.
	if len(wsc.Spec.Prerequisites) > 0 {
		if res, done, err := r.reconcilePrerequisites(ctx, wsd, wsc); err != nil || done {
			return res, err
		}
	}

	// If already deploying, check service readiness
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseDeploying {
		return r.checkServiceReadiness(ctx, wsd)
	}
	// If running, ensure access routes and requeue periodically for health checks
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseRunning {
		if res, retry, err := r.reconcileRoutes(ctx, wsd, wsc); err != nil || retry {
			return res, err
		}
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}
	// If failed, don't retry — user must fix the issue and recreate
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseFailed {
		return ctrl.Result{}, nil
	}

	// First-time setup: merge resources, apply service entry, set phase=Deploying

	// Merge resources: class defaults + user overrides
	resolved := mergeResources(wsc.Spec.DefaultResources, wsd.Spec.Resources)
	wsd.Status.ResolvedResources = &resolved

	// Compute service entry name
	entryName := serviceEntryName(wsd)
	wsd.Status.ServiceEntryName = entryName

	// Merge values: class defaultValues + user values, then inject resolved
	// resources at _exalsius.resources and any class.resourceInjection paths.
	mergedMap, err := mergeValuesMap(wsc.Spec.DefaultValues, wsd.Spec.Values)
	if err != nil {
		r.markFailed(ctx, wsd, workspacesv1.ReasonInternalError, fmt.Sprintf("Failed to merge values: %v", err))
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, err
	}
	warnings := injectResources(mergedMap, resolved, wsc.Spec.ResourceInjection)
	r.setResourcesInjectedCondition(wsd, warnings)
	// Inject the GPU node selector so the chart pins the pod to the requested
	// model (ADR-0002). The gate already validated this exact selector is
	// placeable on the target cluster.
	if resolved.PerReplica.GPUType != nil && *resolved.PerReplica.GPUType != "" {
		injectNodeSelector(mergedMap, gpu.NodeSelectorForModel(*resolved.PerReplica.GPUType, gpu.Options{}))
	}
	mergedValues, err := serializeValues(mergedMap)
	if err != nil {
		r.markFailed(ctx, wsd, workspacesv1.ReasonInternalError, fmt.Sprintf("Failed to serialize values: %v", err))
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, err
	}

	// Pre-create the labeled workspace namespace on the child cluster BEFORE
	// the ServiceSet exists: Sveltos creates namespaces unlabeled, and the
	// mesh-discovery label must be present before any workspace Service does
	// (ADR-0001). Child-cluster unreachability is treated as transient — the
	// kubeconfig secret appears once the cluster is ready.
	childClient, err := getChildClusterClient(ctx, r.Client, wsd, r.Scheme)
	if err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = fmt.Sprintf("Waiting for child cluster access: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	nsReady, err := ensureWorkspaceNamespace(ctx, childClient, wsd, r.MeshNamespaceLabels)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !nsReady {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = fmt.Sprintf(
			"Waiting for workspace namespace %q on the child cluster", workspaceNamespaceName(wsd))
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	if err := ensureWorkspaceServiceSet(ctx, r.Client, r.Scheme, wsd, wsc, mergedValues); err != nil {
		r.markFailed(ctx, wsd, workspacesv1.ReasonInternalError, fmt.Sprintf("Failed to create ServiceSet: %v", err))
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, err
	}

	// Set phase to Deploying
	wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseDeploying
	wsd.Status.FailureContext = nil
	wsd.Status.ObservedGeneration = wsd.Generation
	if err := r.Status().Update(ctx, wsd); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// resolveWorkspaceClass looks up the WorkspaceClass referenced by the deployment.
// If not found, it sets phase=Failed and ClassResolved=False, and returns (nil, nil).
func (r *WorkspaceDeploymentReconciler) resolveWorkspaceClass(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment) (*workspacesv1.WorkspaceClass, error) {
	wsc := &workspacesv1.WorkspaceClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: wsd.Spec.WorkspaceClassRef}, wsc); err != nil {
		if apierrors.IsNotFound(err) {
			r.markFailed(ctx, wsd, workspacesv1.ReasonClassNotFound,
				fmt.Sprintf("WorkspaceClass %q not found", wsd.Spec.WorkspaceClassRef))
			setCondition(&wsd.Status, metav1.Condition{
				Type:               workspacesv1.ConditionClassResolved,
				Status:             metav1.ConditionFalse,
				Reason:             workspacesv1.ReasonClassNotFound,
				Message:            wsd.Status.Message,
				ObservedGeneration: wsd.Generation,
			})
			if updateErr := r.Status().Update(ctx, wsd); updateErr != nil {
				return nil, updateErr
			}
			return nil, nil
		}
		return nil, err
	}
	return wsc, nil
}

// resolveClusterDeployment verifies the referenced ClusterDeployment exists.
// Returns (result, ok, err): ok=false means the caller should return the
// result without proceeding. A missing CD is non-terminal — it sets
// ClusterDeploymentResolved=False, keeps the workspace Pending, and requeues
// so it self-heals when the cluster appears.
func (r *WorkspaceDeploymentReconciler) resolveClusterDeployment(
	ctx context.Context, wsd *workspacesv1.WorkspaceDeployment,
) (ctrl.Result, bool, error) {
	cdRef := wsd.Spec.ClusterDeploymentRef
	cd := &k0rdentv1beta1.ClusterDeployment{}
	err := r.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd)
	if apierrors.IsNotFound(err) {
		msg := fmt.Sprintf("ClusterDeployment %q not found in namespace %q", cdRef.Name, cdRef.Namespace)
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = msg
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionClusterDeploymentResolved,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonClusterDeploymentNotFound,
			Message:            msg,
			ObservedGeneration: wsd.Generation,
		})
		if updateErr := r.Status().Update(ctx, wsd); updateErr != nil {
			return ctrl.Result{}, false, updateErr
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}
	if err != nil {
		return ctrl.Result{}, false, err
	}

	setCondition(&wsd.Status, metav1.Condition{
		Type:               workspacesv1.ConditionClusterDeploymentResolved,
		Status:             metav1.ConditionTrue,
		Reason:             workspacesv1.ReasonClusterDeploymentResolved,
		Message:            fmt.Sprintf("ClusterDeployment %q resolved", cdRef.Name),
		ObservedGeneration: wsd.Generation,
	})
	return ctrl.Result{}, true, nil
}

// refreshFeasibility computes feasibility against the live regional cluster
// and writes status.feasibility + Feasible condition. Pure observability —
// never changes phase. Regional-cluster transient errors result in a requeue.
func (r *WorkspaceDeploymentReconciler) refreshFeasibility(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
) (ctrl.Result, error) {
	demanded := mergeResources(wsc.Spec.DefaultResources, wsd.Spec.Resources)

	regionalClient, err := common.GetClusterClientForCD(
		ctx, r.Client, wsd.Spec.ClusterDeploymentRef.Namespace,
		wsd.Spec.ClusterDeploymentRef.Name, r.Scheme,
	)
	if err != nil {
		// Set a Feasible=Unknown condition so the API can surface "we don't
		// know yet" rather than a stale verdict — but keep deploying.
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionFeasible,
			Status:             metav1.ConditionUnknown,
			Reason:             workspacesv1.ReasonFeasibilityUnknown,
			Message:            fmt.Sprintf("Could not reach regional cluster: %v", err),
			ObservedGeneration: wsd.Generation,
		})
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	feas, err := computeFeasibility(ctx, regionalClient, demanded)
	if err != nil {
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionFeasible,
			Status:             metav1.ConditionUnknown,
			Reason:             workspacesv1.ReasonFeasibilityUnknown,
			Message:            fmt.Sprintf("Failed to compute feasibility: %v", err),
			ObservedGeneration: wsd.Generation,
		})
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	wsd.Status.Feasibility = feas
	if feas.Fits {
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionFeasible,
			Status:             metav1.ConditionTrue,
			Reason:             workspacesv1.ReasonResourcesAvailable,
			Message:            feas.Message,
			ObservedGeneration: wsd.Generation,
		})
	} else {
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionFeasible,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonInsufficientResources,
			Message:            feas.Message,
			ObservedGeneration: wsd.Generation,
		})
	}
	if err := r.Status().Update(ctx, wsd); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// gateGPUOffering enforces that the requested GPU model actually exists on the
// target child cluster before deploying (ADR-0002). It live-scans the child
// cluster's nodes and derives the GPU Offerings present (the same derivation
// the Colony inventory uses, so the vocabulary matches).
//
// Returns (result, ok, err): ok=false means the caller should return the
// result without proceeding. A model absent from the cluster is a terminal
// Failed (reason GpuOfferingUnavailable) — the user fixes the request and
// recreates. A child cluster that can't be reached yet is transient: keep
// Pending and requeue. A workspace requesting no specific GPU model passes
// straight through.
func (r *WorkspaceDeploymentReconciler) gateGPUOffering(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
) (ctrl.Result, bool, error) {
	resolved := mergeResources(wsc.Spec.DefaultResources, wsd.Spec.Resources)
	if resolved.PerReplica.GPUType == nil || *resolved.PerReplica.GPUType == "" {
		return ctrl.Result{}, true, nil
	}
	model := *resolved.PerReplica.GPUType

	childClient, err := getChildClusterClient(ctx, r.Client, wsd, r.Scheme)
	if err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = fmt.Sprintf("Waiting for child cluster access to verify GPU availability: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}

	nodes := &corev1.NodeList{}
	if err := childClient.List(ctx, nodes); err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = fmt.Sprintf("Could not list nodes to verify GPU availability: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}

	offerings := gpu.DeriveOfferings(nodes.Items, gpu.Options{})
	if _, ok := gpu.FindByModel(offerings, model); !ok {
		// Static infeasibility: the model isn't on this cluster at all. Fail
		// fast and terminally — the user fixes the request and recreates.
		// markFailed owns the terminal signal (phase + message +
		// failureContext.reason). We deliberately don't touch the Feasible
		// condition here — refreshFeasibility owns it and would otherwise
		// fight us over the reason.
		msg := fmt.Sprintf("GPU model %q is not available on cluster %q; choose a different cluster or model",
			model, wsd.Spec.ClusterDeploymentRef.Name)
		r.markFailed(ctx, wsd, workspacesv1.ReasonGpuOfferingUnavailable, msg)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, false, nil
	}

	// Offering present — hold for capacity (ADR-0002). Demand is
	// replicas × per-replica GPU count; with no GPU count there's nothing to
	// reserve, so existence alone suffices.
	demand := gpuDemand(resolved)
	if demand <= 0 {
		return ctrl.Result{}, true, nil
	}

	pods := &corev1.PodList{}
	if err := childClient.List(ctx, pods); err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhasePending
		wsd.Status.Message = fmt.Sprintf("Could not list pods to check GPU capacity: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}

	free, _ := gpu.AvailableForModel(nodes.Items, pods.Items, model, gpu.Options{})
	if free < demand {
		// Transient contention: hold in Waiting (no Helm release) and retry.
		msg := fmt.Sprintf("Waiting for %d %q GPU(s) on cluster %q; %d currently free",
			demand, model, wsd.Spec.ClusterDeploymentRef.Name, free)
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseWaiting
		wsd.Status.Message = msg
		if r.Recorder != nil {
			r.Recorder.Eventf(wsd, nil, corev1.EventTypeNormal,
				workspacesv1.ReasonWaitingForGpuCapacity, "Reconcile", "%s", msg)
		}
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}

	return ctrl.Result{}, true, nil
}

// gpuDemand returns the total GPU count a workspace requests: replicas ×
// per-replica GPU count (defaults: 1 replica, 0 GPUs).
func gpuDemand(r workspacesv1.WorkspaceResourceSpec) int64 {
	replicas := int64(1)
	if r.Replicas != nil {
		replicas = int64(*r.Replicas)
	}
	var perReplica int64
	if r.PerReplica.GPUCount != nil {
		perReplica = int64(*r.PerReplica.GPUCount)
	}
	return replicas * perReplica
}

// reconcilePrerequisites evaluates and (if needed) installs prerequisites for
// the WorkspaceDeployment. Returns (result, done, err) where `done=true` means
// the caller should return the result without proceeding to Deploying — either
// because we set a terminal Failed phase, or because we're still waiting on
// installs.
// setResourcesInjectedCondition records the result of resource injection into
// the merged Helm values. Status=True is the steady state; Status=False with
// reason=UserPathsOverwritten signals that the operator overwrote one or more
// spec.values paths to align with the structured spec.resources channel —
// usually a configuration mistake worth surfacing to the user.
func (r *WorkspaceDeploymentReconciler) setResourcesInjectedCondition(
	wsd *workspacesv1.WorkspaceDeployment,
	warnings []resourceInjectionWarning,
) {
	if len(warnings) == 0 {
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionResourcesInjected,
			Status:             metav1.ConditionTrue,
			Reason:             workspacesv1.ReasonResourcesInjected,
			Message:            "Resolved resources injected into Helm values",
			ObservedGeneration: wsd.Generation,
		})
		return
	}
	msgs := make([]string, 0, len(warnings))
	for _, w := range warnings {
		msgs = append(msgs, w.String())
	}
	setCondition(&wsd.Status, metav1.Condition{
		Type:               workspacesv1.ConditionResourcesInjected,
		Status:             metav1.ConditionFalse,
		Reason:             workspacesv1.ReasonUserPathsOverwritten,
		Message:            strings.Join(msgs, "; "),
		ObservedGeneration: wsd.Generation,
	})
}

func (r *WorkspaceDeploymentReconciler) reconcilePrerequisites(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
) (ctrl.Result, bool, error) {
	verdict, statuses, err := evaluatePrerequisites(ctx, r.Client, wsd, wsc)
	if err != nil {
		return ctrl.Result{}, true, err
	}
	wsd.Status.Prerequisites = statuses

	switch verdict {
	case PrerequisitesVerdictInvalid:
		var msg string
		if len(statuses) > 0 {
			msg = statuses[0].Message
		}
		r.markFailed(ctx, wsd, workspacesv1.ReasonInvalidPrerequisite, msg)
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonInvalidPrerequisite,
			Message:            msg,
			ObservedGeneration: wsd.Generation,
		})
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, true, nil

	case PrerequisitesVerdictFailed:
		failed := firstFailedPrerequisite(statuses)
		msg := fmt.Sprintf("Prerequisite %q failed: %s", failed.Name, failed.Message)
		r.markFailed(ctx, wsd, workspacesv1.ReasonPrerequisitesNotReady, msg)
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonPrerequisitesNotReady,
			Message:            msg,
			ObservedGeneration: wsd.Generation,
		})
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, true, nil

	case PrerequisitesVerdictMissing:
		// Create the missing wsprereq SSes. Subsequent reconciles (via the
		// SS watch mapper) will pick up state changes.
		for i, st := range statuses {
			if st.Phase != workspacesv1.PrerequisitePhasePending {
				continue
			}
			if err := ensurePrerequisiteServiceSet(ctx, r.Client, wsd, wsc, wsc.Spec.Prerequisites[i]); err != nil {
				return ctrl.Result{}, true, err
			}
			statuses[i].Phase = workspacesv1.PrerequisitePhaseInstalling
		}
		wsd.Status.Prerequisites = statuses
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseInstallingPrerequisites
		wsd.Status.Message = installingMessage(statuses)
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonInstallingPrerequisites,
			Message:            wsd.Status.Message,
			ObservedGeneration: wsd.Generation,
		})
		if err := r.Status().Update(ctx, wsd); err != nil {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, true, nil

	case PrerequisitesVerdictInstalling:
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseInstallingPrerequisites
		wsd.Status.Message = installingMessage(statuses)
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionFalse,
			Reason:             workspacesv1.ReasonInstallingPrerequisites,
			Message:            wsd.Status.Message,
			ObservedGeneration: wsd.Generation,
		})
		if err := r.Status().Update(ctx, wsd); err != nil {
			return ctrl.Result{}, true, err
		}
		return ctrl.Result{RequeueAfter: requeueInterval}, true, nil

	case PrerequisitesVerdictSatisfied:
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionTrue,
			Reason:             workspacesv1.ReasonPrerequisitesMet,
			Message:            "All prerequisites are installed",
			ObservedGeneration: wsd.Generation,
		})
		// Fall through to Deploying.
		return ctrl.Result{}, false, nil
	}
	return ctrl.Result{}, false, nil
}

// firstFailedPrerequisite returns the first prereq with phase=Failed, or a
// zero PrerequisiteStatus if none.
func firstFailedPrerequisite(statuses []workspacesv1.PrerequisiteStatus) workspacesv1.PrerequisiteStatus {
	for _, s := range statuses {
		if s.Phase == workspacesv1.PrerequisitePhaseFailed {
			return s
		}
	}
	return workspacesv1.PrerequisiteStatus{}
}

// installingMessage formats a status summary like "Installing 1/2 prerequisites: foo".
func installingMessage(statuses []workspacesv1.PrerequisiteStatus) string {
	var inFlight []string
	for _, s := range statuses {
		if s.Phase == workspacesv1.PrerequisitePhaseInstalling || s.Phase == workspacesv1.PrerequisitePhasePending {
			inFlight = append(inFlight, s.Name)
		}
	}
	return fmt.Sprintf("Installing %d/%d prerequisites: %s",
		len(inFlight), len(statuses), strings.Join(inFlight, ", "))
}

// checkServiceReadiness reads the ServiceSet status to determine if the
// workspace's service entry has been deployed or failed. K0rdent's CD-status
// aggregation only reflects services that the ClusterDeployment itself spawned
// from spec.serviceSpec.services[]; standalone ServiceSets are invisible
// there, so the ServiceSet's own status is the authoritative source.
func (r *WorkspaceDeploymentReconciler) checkServiceReadiness(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	cdRef := wsd.Spec.ClusterDeploymentRef
	ssName := serviceSetName(wsd)
	entryName := serviceEntryName(wsd)

	ss := &k0rdentv1beta1.ServiceSet{}
	if err := r.Get(ctx, client.ObjectKey{Name: ssName, Namespace: cdRef.Namespace}, ss); err != nil {
		if apierrors.IsNotFound(err) {
			// ServiceSet hasn't materialised yet — keep polling.
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ServiceSet: %w", err)
	}

	for _, svc := range ss.Status.Services {
		if svc.Name != entryName {
			continue
		}

		// Re-fetch the WorkspaceDeployment to avoid conflicts from prior reconcile steps
		if err := r.Get(ctx, client.ObjectKeyFromObject(wsd), wsd); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to re-fetch WorkspaceDeployment: %w", err)
		}

		switch svc.State {
		case k0rdentv1beta1.ServiceStateDeployed:
			log.Info("Service deployed successfully", "service", entryName)
			wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseRunning
			// The workspace recovered/succeeded — stale failure forensics
			// must not outlive the failure.
			wsd.Status.FailureContext = nil
			setCondition(&wsd.Status, metav1.Condition{
				Type:               workspacesv1.ConditionHelmReleaseReady,
				Status:             metav1.ConditionTrue,
				Reason:             workspacesv1.ReasonHelmReleaseDeployed,
				Message:            "Helm release deployed successfully",
				ObservedGeneration: wsd.Generation,
			})
			setCondition(&wsd.Status, metav1.Condition{
				Type:               workspacesv1.ConditionReady,
				Status:             metav1.ConditionTrue,
				Reason:             workspacesv1.ReasonDeploymentReady,
				Message:            "Workspace is running",
				ObservedGeneration: wsd.Generation,
			})
			if err := r.Status().Update(ctx, wsd); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 60 * time.Second}, nil

		case k0rdentv1beta1.ServiceStateFailed:
			log.Info("Service deployment failed", "service", entryName, "message", svc.FailureMessage)
			r.markFailed(ctx, wsd, workspacesv1.ReasonHelmReleaseFailed,
				fmt.Sprintf("Helm release failed: %s", svc.FailureMessage))
			setCondition(&wsd.Status, metav1.Condition{
				Type:               workspacesv1.ConditionHelmReleaseReady,
				Status:             metav1.ConditionFalse,
				Reason:             workspacesv1.ReasonHelmReleaseFailed,
				Message:            svc.FailureMessage,
				ObservedGeneration: wsd.Generation,
			})
			if err := r.Status().Update(ctx, wsd); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// Service not yet in ServiceSet status — still waiting for k0rdent to report
	log.Info("Waiting for ServiceSet status", "serviceSet", ssName, "service", entryName)
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// reconcileDelete handles finalizer cleanup during deletion, in order:
// routes (provider cleanup) → ServiceSet (Helm uninstall via k0rdent/
// Sveltos) → workspace namespace on the child cluster → finalizer removal.
// The namespace step is skipped when
// the target ClusterDeployment is gone or being torn down — the cluster's
// destruction is the cleanup (ADR-0001).
func (r *WorkspaceDeploymentReconciler) reconcileDelete(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Cleaning up WorkspaceDeployment", "name", wsd.Name)

	if wsd.Status.Phase != workspacesv1.WorkspaceDeploymentPhaseDeleting {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseDeleting
		// Best-effort: deletion proceeds even if the status write conflicts.
		_ = r.Status().Update(ctx, wsd)
	}

	// 1. Remove provider-materialized routes first — kill the front door
	// before the backend disappears, and free provider-held resources
	// (e.g. pool ports).
	if !r.cleanupRoutes(ctx, wsd) {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// 2. Delete the workspace's ServiceSet and wait until it is fully gone.
	// K0rdent garbage-collects the Sveltos Profile and Sveltos uninstalls the
	// Helm release; touching the namespace earlier would race the uninstall.
	ssGone, err := ensureWorkspaceServiceSetDeleted(ctx, r.Client, wsd)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ssGone {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	// 3. Delete the workspace namespace on the child cluster — Sveltos never
	// removes it, and stale PVCs would resurrect state into a recreated
	// same-name workspace.
	if res, done, err := r.cleanupWorkspaceNamespace(ctx, wsd); err != nil || !done {
		return res, err
	}

	// 4. Remove finalizer
	controllerutil.RemoveFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer)
	if err := r.Update(ctx, wsd); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// cleanupWorkspaceNamespace removes the per-workspace namespace from the
// child cluster. Returns done=true when cleanup is complete or rightly
// skipped. Unreachable child clusters are retried for as long as the
// ClusterDeployment exists — giving up early would silently leak the
// namespace; once the CD is gone or deleting, teardown owns the cleanup.
func (r *WorkspaceDeploymentReconciler) cleanupWorkspaceNamespace(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
) (ctrl.Result, bool, error) {
	log := log.FromContext(ctx)
	cdRef := wsd.Spec.ClusterDeploymentRef

	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Target ClusterDeployment gone, skipping workspace namespace cleanup",
				"clusterDeployment", cdRef.Name)
			return ctrl.Result{}, true, nil
		}
		return ctrl.Result{}, false, err
	}
	if !cd.DeletionTimestamp.IsZero() {
		log.Info("Target ClusterDeployment is being torn down, skipping workspace namespace cleanup",
			"clusterDeployment", cdRef.Name)
		return ctrl.Result{}, true, nil
	}

	childClient, err := getChildClusterClient(ctx, r.Client, wsd, r.Scheme)
	if err != nil {
		log.Info("Child cluster unreachable, retrying workspace namespace cleanup",
			"clusterDeployment", cdRef.Name, "error", err.Error())
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}

	done, err := deleteWorkspaceNamespace(ctx, childClient, wsd)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if !done {
		return ctrl.Result{RequeueAfter: requeueInterval}, false, nil
	}
	return ctrl.Result{}, true, nil
}

// setCondition adds or updates a condition on the WorkspaceDeployment status.
func setCondition(status *workspacesv1.WorkspaceDeploymentStatus, condition metav1.Condition) {
	condition.LastTransitionTime = metav1.Now()

	for i, existing := range status.Conditions {
		if existing.Type == condition.Type {
			if existing.Status != condition.Status {
				status.Conditions[i] = condition
			} else {
				// Keep the existing transition time if status hasn't changed
				condition.LastTransitionTime = existing.LastTransitionTime
				status.Conditions[i] = condition
			}
			return
		}
	}
	status.Conditions = append(status.Conditions, condition)
}

// workspaceDeploymentForServiceSet maps a ServiceSet event back to the
// WorkspaceDeployment(s) that depend on it. Two paths:
//
//  1. Workspace ServiceSet (one-to-one): the SS's name matches a WSD's
//     serviceSetName(). Single reconcile request.
//  2. Prerequisite ServiceSet (one-to-many, identified by labels): fan out
//     to every WSD whose WorkspaceClass declares the SS's template as a
//     prerequisite AND targets the same ClusterDeployment.
func (r *WorkspaceDeploymentReconciler) workspaceDeploymentForServiceSet(
	ctx context.Context, obj client.Object,
) []reconcile.Request {
	ss, ok := obj.(*k0rdentv1beta1.ServiceSet)
	if !ok {
		return nil
	}

	var wsds workspacesv1.WorkspaceDeploymentList
	if err := r.List(ctx, &wsds, client.InNamespace(ss.Namespace)); err != nil {
		log.FromContext(ctx).Error(err,
			"failed to list WorkspaceDeployments for ServiceSet watch",
			"serviceSet", client.ObjectKeyFromObject(ss))
		return nil
	}

	if isPrerequisiteServiceSet(ss) {
		cdName := ss.Labels[labelPrerequisiteCluster]
		templateName := ss.Labels[labelPrerequisiteTemplate]
		if cdName == "" || templateName == "" {
			return nil
		}
		var requests []reconcile.Request
		seen := map[string]bool{} // dedupe in case of duplicate prereqs
		for i := range wsds.Items {
			wsd := &wsds.Items[i]
			if wsd.Spec.ClusterDeploymentRef.Name != cdName {
				continue
			}
			wsc := &workspacesv1.WorkspaceClass{}
			if err := r.Get(ctx, client.ObjectKey{Name: wsd.Spec.WorkspaceClassRef}, wsc); err != nil {
				continue
			}
			for _, prereq := range wsc.Spec.Prerequisites {
				if sanitizeLabelValue(prereq.ServiceTemplate.Name) == templateName && !seen[wsd.Name] {
					requests = append(requests, reconcile.Request{
						NamespacedName: client.ObjectKeyFromObject(wsd),
					})
					seen[wsd.Name] = true
					break
				}
			}
		}
		return requests
	}

	// Workspace SS path.
	for i := range wsds.Items {
		if serviceSetName(&wsds.Items[i]) == ss.Name {
			return []reconcile.Request{{
				NamespacedName: client.ObjectKeyFromObject(&wsds.Items[i]),
			}}
		}
	}
	return nil
}

func (r *WorkspaceDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Providers that support it get a periodic orphan sweep — the
	// label-based backstop for cross-cluster cleanup that finalizers
	// cannot guarantee (e.g. workspaces deleted after their child
	// cluster was already torn down).
	if sweeper, ok := r.RouteProvider.(routing.OrphanSweeper); ok {
		if err := mgr.Add(&orphanRouteSweeper{
			client:  mgr.GetClient(),
			scheme:  mgr.GetScheme(),
			sweeper: sweeper,
		}); err != nil {
			return err
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&workspacesv1.WorkspaceDeployment{}).
		Watches(
			&k0rdentv1beta1.ServiceSet{},
			handler.EnqueueRequestsFromMapFunc(r.workspaceDeploymentForServiceSet),
		).
		Named("workspace-deployment").
		Complete(r)
}
