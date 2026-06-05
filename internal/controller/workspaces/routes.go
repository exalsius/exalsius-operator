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

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
)

// reconcileRoutes asks the routing provider to materialize access for the
// workspace's declared endpoints and mirrors the result into status.access[]
// + the RoutesReady condition. Called only once the workspace is Running —
// route targets don't exist before the Helm release is deployed.
//
// Returns retry=true when the caller should return the result immediately
// (transient provider error or not-yet-ready routes being polled).
// Provider errors never change the phase: a running workspace with broken
// routing is degraded, not failed.
func (r *WorkspaceDeploymentReconciler) reconcileRoutes(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
	wsc *workspacesv1.WorkspaceClass,
) (ctrl.Result, bool, error) {
	// No provider registered or no endpoints declared — routing is not in play.
	if r.RouteProvider == nil || len(wsc.Spec.AccessEndpoints) == 0 {
		return ctrl.Result{}, false, nil
	}

	entries, err := r.RouteProvider.EnsureRoutes(ctx, routing.RouteRequest{
		Workspace:        wsd,
		Endpoints:        wsc.Spec.AccessEndpoints,
		ManagementClient: r.Client,
		Scheme:           r.Scheme,
	})
	if err != nil {
		// Infra gaps (no gateway, no regional parent) are admin-fixable and
		// get their own reason so the API can map them to contact_admin
		// remediation; everything else is a transient provider error.
		reason := workspacesv1.ReasonRoutingError
		message := fmt.Sprintf("Failed to ensure routes: %v", err)
		if routing.IsInfraNotReady(err) {
			reason = workspacesv1.ReasonRoutingInfraNotReady
			message = fmt.Sprintf("Routing infrastructure is not ready: %v", err)
		}
		setCondition(&wsd.Status, metav1.Condition{
			Type:               workspacesv1.ConditionRoutesReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: wsd.Generation,
		})
		_ = r.Status().Update(ctx, wsd)
		return ctrl.Result{RequeueAfter: requeueInterval}, true, nil
	}

	allReady := true
	notReadyMsg := ""
	for _, e := range entries {
		if !e.Ready {
			allReady = false
			if notReadyMsg == "" {
				notReadyMsg = fmt.Sprintf("Endpoint %q is not routed yet", e.Name)
				if e.Message != "" {
					notReadyMsg = fmt.Sprintf("Endpoint %q: %s", e.Name, e.Message)
				}
			}
		}
	}

	condition := metav1.Condition{
		Type:               workspacesv1.ConditionRoutesReady,
		Status:             metav1.ConditionTrue,
		Reason:             workspacesv1.ReasonRoutesReady,
		Message:            fmt.Sprintf("All %d access endpoint(s) are routed", len(entries)),
		ObservedGeneration: wsd.Generation,
	}
	if !allReady {
		condition.Status = metav1.ConditionFalse
		condition.Reason = workspacesv1.ReasonRoutingInProgress
		condition.Message = notReadyMsg
	}

	// Skip the status write when nothing changed — this path runs on every
	// periodic Running-phase reconcile.
	prev := apimeta.FindStatusCondition(wsd.Status.Conditions, workspacesv1.ConditionRoutesReady)
	unchanged := apiequality.Semantic.DeepEqual(wsd.Status.Access, entries) &&
		prev != nil && prev.Status == condition.Status &&
		prev.Reason == condition.Reason && prev.Message == condition.Message
	if !unchanged {
		wsd.Status.Access = entries
		setCondition(&wsd.Status, condition)
		if err := r.Status().Update(ctx, wsd); err != nil {
			return ctrl.Result{}, true, err
		}
	}

	if !allReady {
		// Poll until the provider reports everything programmed.
		return ctrl.Result{RequeueAfter: requeueInterval}, true, nil
	}
	return ctrl.Result{}, false, nil
}

// cleanupRoutes removes provider-materialized access during deletion. Runs
// BEFORE the ServiceSet is deleted: kill the front door before the backend
// disappears, and free provider-held resources (e.g. pool ports) first.
// Returns done=false when cleanup must be retried.
func (r *WorkspaceDeploymentReconciler) cleanupRoutes(
	ctx context.Context,
	wsd *workspacesv1.WorkspaceDeployment,
) bool {
	if r.RouteProvider == nil {
		return true
	}

	// Best-effort class resolution: during deletion the class may already be
	// gone — providers clean up from workspace identity alone in that case.
	var endpoints []workspacesv1.AccessEndpoint
	wsc := &workspacesv1.WorkspaceClass{}
	if err := r.Get(ctx, client.ObjectKey{Name: wsd.Spec.WorkspaceClassRef}, wsc); err == nil {
		endpoints = wsc.Spec.AccessEndpoints
	}

	if err := r.RouteProvider.CleanupRoutes(ctx, routing.RouteRequest{
		Workspace:        wsd,
		Endpoints:        endpoints,
		ManagementClient: r.Client,
		Scheme:           r.Scheme,
	}); err != nil {
		log.FromContext(ctx).Info("Failed to clean up workspace routes, retrying", "error", err.Error())
		return false
	}
	return true
}
