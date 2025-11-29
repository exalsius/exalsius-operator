/*
Copyright 2025.

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

package infra

import (
	"context"
	"time"

	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// NetBirdCleanupFinalizer is added to RemoteMachines that need NetBird cleanup
	NetBirdCleanupFinalizer = "netbird.exalsius.ai/cleanup"
)

// RemoteMachineCleanupReconciler watches RemoteMachine deletions and runs NetBird cleanup
// before k0smotron's finalizer logic executes. This ensures NetBird is properly stopped
// and cleaned up on remote machines before k0s cleanup runs.
type RemoteMachineCleanupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=remotemachines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=remotemachines/status,verbs=get
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments,verbs=get;list
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies,verbs=get;list
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

// Reconcile watches for RemoteMachine deletions and runs NetBird cleanup if the finalizer is present.
func (r *RemoteMachineCleanupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	rm := &infrastructurev1beta1.RemoteMachine{}
	if err := r.Get(ctx, req.NamespacedName, rm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Only act on deletions
	if rm.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	// Check if our finalizer is present
	if !controllerutil.ContainsFinalizer(rm, NetBirdCleanupFinalizer) {
		// Finalizer not present, nothing to do
		return ctrl.Result{}, nil
	}

	// Run NetBird cleanup
	log.Info("Running NetBird cleanup on RemoteMachine", "machine", rm.Name)
	if err := CleanupRemoteMachineNetBird(ctx, r.Client, rm); err != nil {
		log.Error(err, "NetBird cleanup failed, will retry")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// Remove finalizer to allow deletion to proceed
	controllerutil.RemoveFinalizer(rm, NetBirdCleanupFinalizer)
	if err := r.Update(ctx, rm); err != nil {
		// If update fails, the object might already be gone - log but don't fail
		log.V(1).Info("Failed to remove finalizer (object may be deleted)", "machine", rm.Name, "error", err)
		// Return success anyway since cleanup is done
		return ctrl.Result{}, nil
	}

	log.Info("NetBird cleanup completed successfully", "machine", rm.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteMachineCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.RemoteMachine{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// Don't care about creates - finalizer is added by bootstrap patcher
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Only trigger when DeletionTimestamp is set
				return e.ObjectNew.GetDeletionTimestamp() != nil
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false // Too late at this point
			},
		}).
		Complete(r)
}
