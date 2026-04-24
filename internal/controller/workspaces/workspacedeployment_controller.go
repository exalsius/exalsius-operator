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
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

const (
	requeueInterval = 15 * time.Second
)

// WorkspaceDeploymentReconciler reconciles a WorkspaceDeployment object.
type WorkspaceDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspacedeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspaceclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=servicesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments/status,verbs=get

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

	// Check prerequisites (validation only — no auto-install)
	if len(wsc.Spec.Prerequisites) > 0 {
		met, missing, err := r.checkPrerequisites(ctx, wsd, wsc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !met {
			wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
			wsd.Status.Message = fmt.Sprintf("Missing prerequisites on cluster %q: %s", wsd.Spec.ClusterDeploymentRef.Name, missing)
			setCondition(&wsd.Status, metav1.Condition{
				Type:               workspacesv1.ConditionPrerequisitesMet,
				Status:             metav1.ConditionFalse,
				Reason:             workspacesv1.ReasonPrerequisitesNotReady,
				Message:            wsd.Status.Message,
				ObservedGeneration: wsd.Generation,
			})
			_ = r.Status().Update(ctx, wsd)
			return ctrl.Result{}, nil
		}
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionPrerequisitesMet,
			Status:             metav1.ConditionTrue,
			Reason:             workspacesv1.ReasonPrerequisitesMet,
			Message:            "All prerequisites are installed",
			ObservedGeneration: wsd.Generation,
		})
	}

	// If already deploying, check service readiness
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseDeploying {
		return r.checkServiceReadiness(ctx, wsd)
	}
	// If running, requeue periodically for health checks
	if wsd.Status.Phase == workspacesv1.WorkspaceDeploymentPhaseRunning {
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

	// Merge values: class defaultValues + user values
	mergedValues, err := mergeValues(wsc.Spec.DefaultValues, wsd.Spec.Values)
	if err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
		wsd.Status.Message = fmt.Sprintf("Failed to merge values: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, err
	}
	if err := ensureWorkspaceServiceSet(ctx, r.Client, r.Scheme, wsd, wsc, mergedValues); err != nil {
		wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
		wsd.Status.Message = fmt.Sprintf("Failed to create ServiceSet: %v", err)
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{}, err
	}

	// Set phase to Deploying
	wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseDeploying
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
			wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
			wsd.Status.Message = fmt.Sprintf("WorkspaceClass %q not found", wsd.Spec.WorkspaceClassRef)
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

// checkPrerequisites checks if all prerequisite services are deployed on the
// target ClusterDeployment. Returns (met, missingNames, error).
func (r *WorkspaceDeploymentReconciler) checkPrerequisites(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment, wsc *workspacesv1.WorkspaceClass) (bool, string, error) {
	cdRef := wsd.Spec.ClusterDeploymentRef
	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd); err != nil {
		if apierrors.IsNotFound(err) {
			return false, fmt.Sprintf("ClusterDeployment %q not found", cdRef.Name), nil
		}
		return false, "", err
	}

	// Build a set of deployed service template names from status
	deployed := make(map[string]bool)
	for _, svc := range cd.Status.Services {
		if svc.State == k0rdentv1beta1.ServiceStateDeployed {
			deployed[svc.Template] = true
		}
	}

	// Also check spec.serviceSpec.services for entries that may not have status yet
	for _, svc := range cd.Spec.ServiceSpec.Services {
		deployed[svc.Template] = true
	}

	var missing []string
	for _, prereq := range wsc.Spec.Prerequisites {
		if !deployed[prereq.Name] {
			missing = append(missing, prereq.Name)
		}
	}

	if len(missing) > 0 {
		return false, fmt.Sprintf("%v", missing), nil
	}
	return true, "", nil
}

// checkServiceReadiness reads the ClusterDeployment status to determine if the
// workspace's service entry has been deployed or failed.
func (r *WorkspaceDeploymentReconciler) checkServiceReadiness(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	cdRef := wsd.Spec.ClusterDeploymentRef
	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: cdRef.Name, Namespace: cdRef.Namespace}, cd); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ClusterDeployment: %w", err)
	}

	entryName := serviceEntryName(wsd)

	for _, svc := range cd.Status.Services {
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
			wsd.Status.Phase = workspacesv1.WorkspaceDeploymentPhaseFailed
			wsd.Status.Message = fmt.Sprintf("Helm release failed: %s", svc.FailureMessage)
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

	// Service not yet in status — still waiting for k0rdent to report
	log.Info("Waiting for service status", "service", entryName)
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// reconcileDelete handles finalizer cleanup during deletion.
func (r *WorkspaceDeploymentReconciler) reconcileDelete(ctx context.Context, wsd *workspacesv1.WorkspaceDeployment) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Cleaning up WorkspaceDeployment", "name", wsd.Name)

	// Delete the workspace's ServiceSet. K0rdent will garbage-collect the
	// Sveltos Profile, and Sveltos will uninstall the Helm release.
	if err := deleteWorkspaceServiceSet(ctx, r.Client, wsd); err != nil {
		log.Error(err, "Failed to delete workspace ServiceSet")
		// Continue with finalizer removal — the ServiceSet may already be gone
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(wsd, workspacesv1.WorkspaceDeploymentFinalizer)
	if err := r.Update(ctx, wsd); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
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

func (r *WorkspaceDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&workspacesv1.WorkspaceDeployment{}).
		Named("workspace-deployment").
		Complete(r)
}
