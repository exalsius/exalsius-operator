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
	"fmt"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
// The webhook adds the finalizer to ALL RemoteMachines, but cleanup only runs if NetBird is actually
// enabled for the Colony that owns this RemoteMachine.
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
		log.V(1).Info("NetBird cleanup finalizer not present, skipping", "machine", rm.Name)
		return ctrl.Result{}, nil
	}

	log.Info("Processing RemoteMachine deletion with NetBird cleanup finalizer", "machine", rm.Name)

	// Check if this RemoteMachine actually needs NetBird cleanup
	needsCleanup, err := r.remoteMachineNeedsNetBirdCleanup(ctx, rm)
	if err != nil {
		log.Error(err, "Failed to determine if NetBird cleanup is needed, assuming yes", "machine", rm.Name)
		// If we can't determine, assume cleanup is needed to be safe
		needsCleanup = true
	}

	if needsCleanup {
		// Run NetBird cleanup
		log.Info("Running NetBird cleanup on RemoteMachine", "machine", rm.Name)
		if err := CleanupRemoteMachineNetBird(ctx, r.Client, rm); err != nil {
			log.Error(err, "NetBird cleanup failed, will retry")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		log.Info("NetBird cleanup completed successfully", "machine", rm.Name)
	} else {
		log.Info("NetBird not enabled for this RemoteMachine, skipping cleanup", "machine", rm.Name)
	}

	// Always remove finalizer (whether cleanup was needed or not)
	// This ensures deletion is never blocked unnecessarily
	controllerutil.RemoveFinalizer(rm, NetBirdCleanupFinalizer)
	if err := r.Update(ctx, rm); err != nil {
		// If update fails, the object might already be gone - log but don't fail
		log.V(1).Info("Failed to remove finalizer (object may be deleted)", "machine", rm.Name, "error", err)
		// Return success anyway since cleanup is done
		return ctrl.Result{}, nil
	}

	log.Info("Removed NetBird cleanup finalizer from RemoteMachine", "machine", rm.Name)
	return ctrl.Result{}, nil
}

// remoteMachineNeedsNetBirdCleanup determines if a RemoteMachine needs NetBird cleanup
// by checking if it belongs to a Colony with NetBird enabled.
func (r *RemoteMachineCleanupReconciler) remoteMachineNeedsNetBirdCleanup(ctx context.Context, rm *infrastructurev1beta1.RemoteMachine) (bool, error) {
	log := log.FromContext(ctx)

	// Get the cluster name from labels
	clusterName, ok := rm.Labels["cluster.x-k8s.io/cluster-name"]
	if !ok {
		log.V(1).Info("RemoteMachine has no cluster label, cannot determine if NetBird cleanup is needed", "machine", rm.Name)
		return false, nil
	}

	// Get the ClusterDeployment
	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: rm.Namespace,
	}, cd); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("failed to get ClusterDeployment %s/%s: %w", rm.Namespace, clusterName, err)
		}
		log.V(1).Info("ClusterDeployment not found, cannot determine if NetBird cleanup is needed",
			"machine", rm.Name,
			"cluster", clusterName)
		return false, nil
	}

	// Get the Colony from ClusterDeployment owner references
	for _, owner := range cd.OwnerReferences {
		if owner.Kind == "Colony" {
			colony := &infrav1.Colony{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      owner.Name,
				Namespace: rm.Namespace,
			}, colony); err != nil {
				if client.IgnoreNotFound(err) != nil {
					return false, fmt.Errorf("failed to get Colony %s/%s: %w", rm.Namespace, owner.Name, err)
				}
				log.V(1).Info("Colony not found, assuming NetBird is not enabled",
					"machine", rm.Name,
					"colony", owner.Name)
				return false, nil
			}

			// Check if NetBird is enabled in the Colony
			if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
				log.V(1).Info("Colony has NetBird enabled, cleanup is needed",
					"machine", rm.Name,
					"colony", colony.Name)
				return true, nil
			}

			log.V(1).Info("Colony does not have NetBird enabled, cleanup not needed",
				"machine", rm.Name,
				"colony", colony.Name)
			return false, nil
		}
	}

	// No Colony owner found
	log.V(1).Info("No Colony owner found for ClusterDeployment, assuming NetBird is not enabled",
		"machine", rm.Name,
		"cluster", clusterName)
	return false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteMachineCleanupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrastructurev1beta1.RemoteMachine{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// Don't care about creates - finalizer is added by webhook
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
