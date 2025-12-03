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

	// K0smotronFinalizer is k0smotron's finalizer on RemoteMachines
	// We must wait for k0smotron to remove its finalizer before removing ours
	// to avoid a race condition with k0smotron's patch helper
	K0smotronFinalizer = "remotemachine.k0smotron.io/finalizer"
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

	// IMPORTANT: Wait for k0smotron to finish BEFORE running our cleanup.
	// This ensures:
	// 1. k0s is fully stopped before we remove netbird (k0s wrapper uses netbird for IP)
	// 2. No race condition with k0smotron's patch helper (which takes a snapshot at start)
	// If we remove our finalizer while k0smotron is still processing, k0smotron's
	// patch will try to "restore" our finalizer (because it was in the snapshot),
	// which fails with "no new finalizers can be added if the object is being deleted".
	if controllerutil.ContainsFinalizer(rm, K0smotronFinalizer) {
		log.Info("Waiting for k0smotron to finish cleanup first",
			"machine", rm.Name,
			"k0smotronFinalizer", K0smotronFinalizer)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// k0smotron is done (k0s stopped/uninstalled), now safe to cleanup NetBird
	// Check if we already did cleanup by looking for an annotation to avoid repeated cleanups
	const cleanupDoneAnnotation = "netbird.exalsius.ai/cleanup-done"
	cleanupAlreadyDone := rm.Annotations != nil && rm.Annotations[cleanupDoneAnnotation] == "true"

	if !cleanupAlreadyDone {
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

			// Mark cleanup as done with annotation
			if rm.Annotations == nil {
				rm.Annotations = make(map[string]string)
			}
			rm.Annotations[cleanupDoneAnnotation] = "true"
		} else {
			log.V(1).Info("NetBird not enabled for this RemoteMachine, skipping cleanup", "machine", rm.Name)
		}
	} else {
		log.V(1).Info("NetBird cleanup already completed", "machine", rm.Name)
	}

	// Remove our finalizer and update (single API call for both annotation and finalizer)
	controllerutil.RemoveFinalizer(rm, NetBirdCleanupFinalizer)
	if err := r.Update(ctx, rm); err != nil {
		// If update fails, the object might already be gone - log but don't fail
		log.V(1).Info("Failed to update RemoteMachine (object may be deleted)", "machine", rm.Name, "error", err)
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
