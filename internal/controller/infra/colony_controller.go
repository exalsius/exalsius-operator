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
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	clusterdeployment "github.com/exalsius/exalsius-operator/internal/controller/infra/clusterdeployment"
	netbirdpkg "github.com/exalsius/exalsius-operator/internal/controller/infra/netbird"
)

const (
	colonyFinalizer = "colony.infra.exalsius.ai/finalizer"
)

// ColonyReconciler reconciles a Colony object
type ColonyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// Note: Child cluster access (for Cilium ConfigMap creation) is done via kubeconfig secrets,
// which requires the secrets RBAC permission above. No additional RBAC is needed for child cluster resources.
func (r *ColonyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	colony := &infrav1.Colony{}
	if err := r.Get(ctx, req.NamespacedName, colony); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if colony.GetDeletionTimestamp() != nil {
		return r.handleColonyDeletion(ctx, req, colony)
	}

	// Ensure finalizer is present
	if !controllerutil.ContainsFinalizer(colony, colonyFinalizer) {
		return r.addFinalizer(ctx, colony)
	}

	// Handle normal reconciliation
	return r.reconcileColony(ctx, colony)
}

// addFinalizer adds the colony finalizer to the Colony resource
func (r *ColonyReconciler) addFinalizer(ctx context.Context, colony *infrav1.Colony) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Adding finalizer to Colony", "Colony.Namespace", colony.Namespace, "Colony.Name", colony.Name)
	controllerutil.AddFinalizer(colony, colonyFinalizer)
	if err := r.Update(ctx, colony); err != nil {
		log.Error(err, "Failed to add finalizer to Colony", "Colony.Namespace", colony.Namespace, "Colony.Name", colony.Name)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileColony handles the main reconciliation logic for a Colony resource
func (r *ColonyReconciler) reconcileColony(ctx context.Context, colony *infrav1.Colony) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Clean up ClusterDeployments that are no longer in the spec (non-blocking)
	hasPendingDeletions, err := clusterdeployment.CleanupOrphanedClusterDeployments(ctx, r.Client, colony)
	if err != nil {
		log.Error(err, "Failed to cleanup orphaned cluster deployments")
		return ctrl.Result{}, err
	}
	if hasPendingDeletions {
		log.Info("Orphaned ClusterDeployments still being deleted, will requeue")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Create a copy before making any status changes for the final patch
	origForStatusPatch := colony.DeepCopy()

	// Reconcile NetBird integration FIRST if enabled
	if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
		if err := netbirdpkg.ReconcileNetBird(ctx, r.Client, colony, r.Scheme); err != nil {
			log.Error(err, "Failed to reconcile NetBird integration")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
	}

	// Ensure all ClusterDeployments exist
	for _, colonyCluster := range colony.Spec.ColonyClusters {
		if colonyCluster.ClusterDeploymentSpec != nil {
			if err := clusterdeployment.EnsureClusterDeployment(ctx, r.Client, colony, &colonyCluster, r.Scheme); err != nil {
				log.Error(err, "Failed to ensure cluster deployment")
				return ctrl.Result{}, err
			}
		}
	}

	// Update status from cluster states
	if err := r.updateColonyStatusFromClusters(ctx, colony); err != nil {
		log.Error(err, "Failed to update Colony status from clusters")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	// Persist status
	if err := r.Client.Status().Patch(ctx, colony, client.MergeFrom(origForStatusPatch)); err != nil {
		log.Error(err, "Failed to persist final Colony status")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}
	log.Info("Successfully persisted Colony status")

	// Requeue sooner if not all clusters are ready
	if colony.Status.ReadyClusters != colony.Status.TotalClusters {
		log.Info("Not all clusters are ready, requeueing",
			"ready", colony.Status.ReadyClusters,
			"total", colony.Status.TotalClusters,
		)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Ensure aggregated kubeconfig secret exists
	if result, err := r.ensureAggregatedKubeconfigSecretExists(ctx, colony); err != nil {
		log.Error(err, "Failed to ensure aggregated kubeconfig secret")
		return result, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	log.Info("Colony reconciled successfully")
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *ColonyReconciler) ensureAggregatedKubeconfigSecretExists(ctx context.Context, colony *infrav1.Colony) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	references := make(map[string][]byte)

	// iterate over all clusters in the colony
	for _, clusterDeploymentRef := range colony.Status.ClusterDeploymentRefs {
		kubeconfigSecretName := fmt.Sprintf("%s-kubeconfig", clusterDeploymentRef.Name)

		var kubeconfigSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{Namespace: clusterDeploymentRef.Namespace, Name: kubeconfigSecretName}, &kubeconfigSecret); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Kubeconfig secret not found for cluster, will retry", "cluster", fmt.Sprintf("%s/%s", clusterDeploymentRef.Namespace, clusterDeploymentRef.Name))
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			} else {
				log.Error(err, "Failed to get Kubeconfig secret for cluster", "cluster", fmt.Sprintf("%s/%s", clusterDeploymentRef.Namespace, clusterDeploymentRef.Name))
				return ctrl.Result{}, err
			}
		}

		// Create an ObjectReference for the kubeconfig secret.
		objRef := corev1.ObjectReference{
			APIVersion: kubeconfigSecret.APIVersion,
			Kind:       kubeconfigSecret.Kind,
			Namespace:  kubeconfigSecret.Namespace,
			Name:       kubeconfigSecret.Name,
		}

		// JSON-encode the ObjectReference.
		refBytes, err := json.Marshal(objRef)
		if err != nil {
			log.Error(err, "Failed to marshal object reference", "cluster", clusterDeploymentRef.Name)
			return ctrl.Result{}, err
		}
		references[clusterDeploymentRef.Name] = refBytes

	}

	aggregatedSecretName := colony.Name + "-kubeconfigs"
	aggregatedSecretNamespace := colony.Namespace

	var aggregatedKubeconfigSecret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: aggregatedSecretNamespace, Name: aggregatedSecretName}, &aggregatedKubeconfigSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Aggregated kubeconfig secret not found. Creating...")
			aggregatedKubeconfigSecret = corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      aggregatedSecretName,
					Namespace: aggregatedSecretNamespace,
				},
				Data: references,
			}
			if err := r.Create(ctx, &aggregatedKubeconfigSecret); err != nil {
				log.Error(err, "Failed to create aggregated kubeconfig secret")
				return ctrl.Result{}, err
			}
			log.Info("Aggregated kubeconfig secret created",
				"namespace", aggregatedSecretNamespace,
				"name", aggregatedSecretName,
			)
			return ctrl.Result{}, nil
		} else {
			log.Error(err, "Failed to get aggregated kubeconfig secret")
			return ctrl.Result{}, err
		}
	} else {
		// The secret exists, update its Data field.
		aggregatedKubeconfigSecret.Data = references
		if err := r.Update(ctx, &aggregatedKubeconfigSecret); err != nil {
			log.Error(err, "Failed to update aggregated kubeconfig secret")
			return ctrl.Result{}, err
		}
		log.Info("Aggregated kubeconfig secret updated",
			"namespace", aggregatedKubeconfigSecret.Namespace,
			"name", aggregatedKubeconfigSecret.Name)
	}

	// set owner reference to the colony
	if err := ctrl.SetControllerReference(colony, &aggregatedKubeconfigSecret, r.Scheme); err != nil {
		log.Error(err, "Failed to set owner reference to the aggregated kubeconfig secret")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleColonyDeletion handles the deletion flow for a Colony resource.
// It cleans up associated resources and removes the finalizer when cleanup is complete.
func (r *ColonyReconciler) handleColonyDeletion(ctx context.Context, req ctrl.Request, colony *infrav1.Colony) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("Colony marked for deletion. Shutting down cluster resources and removing finalizer.")

	// Update status with retry mechanism to handle conflicts
	if err := r.updateColonyStatusWithRetry(ctx, colony, "Deleting"); err != nil {
		log.Error(err, "Failed to update Colony status to deleting")
		return ctrl.Result{}, err
	}

	// Cleanup NetBird resources if enabled
	if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
		nbClient, err := netbirdpkg.GetNetBirdClientFromSecrets(ctx, r.Client, colony)
		if err != nil {
			log.Error(err, "Failed to get NetBird client for cleanup, continuing with other cleanup")
		} else {
			if err := netbirdpkg.CleanupNetBirdResources(ctx, r.Client, nbClient, colony); err != nil {
				log.Error(err, "Failed to cleanup NetBird resources, continuing with other cleanup")
			}
		}
	}

	// Cleanup all ClusterDeployments - track if any are still being deleted
	anyStillDeleting := false
	for _, colonyCluster := range colony.Spec.ColonyClusters {
		stillExists, err := r.cleanupAssociatedResources(ctx, colony, &colonyCluster)
		if err != nil {
			log.Error(err, "Failed to cleanup associated cluster resources for ColonyCluster", "ColonyCluster.Name", colonyCluster.ClusterName)
			return ctrl.Result{}, err
		}
		if stillExists {
			anyStillDeleting = true
		}
	}

	// If any ClusterDeployments are still being deleted, requeue to check again
	if anyStillDeleting {
		log.Info("ClusterDeployments still being deleted, will requeue")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Refresh the colony object before removing finalizer
	if err := r.Get(ctx, req.NamespacedName, colony); err != nil {
		if errors.IsNotFound(err) {
			// Colony already deleted, nothing more to do
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to refetch Colony before removing finalizer")
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(colony, colonyFinalizer)
	if err := r.Update(ctx, colony); err != nil {
		log.Error(err, "Failed to remove finalizer from Colony")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// cleanupAssociatedResources triggers deletion of ClusterDeployment and associated resources.
// Returns (stillExists, error) - stillExists is true if the ClusterDeployment is still being deleted.
func (r *ColonyReconciler) cleanupAssociatedResources(ctx context.Context, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) (bool, error) {
	log := log.FromContext(ctx)

	clusterDeploymentName := colony.Name + "-" + colonyCluster.ClusterName

	// First, delete patched bootstrap secrets to avoid garbage collection conflicts
	// This prevents the ClusterDeployment deletion from hanging due to field ownership issues
	if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
		log.Info("Cleaning up patched bootstrap secrets for cluster", "cluster", colonyCluster.ClusterName)
		if err := netbirdpkg.DeletePatchedBootstrapSecretsForCluster(ctx, r.Client, colony, colonyCluster.ClusterName); err != nil {
			log.Error(err, "Failed to delete patched bootstrap secrets, continuing with ClusterDeployment deletion", "cluster", colonyCluster.ClusterName)
			// Continue with deletion even if secret cleanup fails - ClusterDeployment deletion should still proceed
		}
	}

	// Check if the ClusterDeployment still exists
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{}
	err := r.Get(ctx, client.ObjectKey{Name: clusterDeploymentName, Namespace: colony.Namespace}, clusterDeployment)

	if errors.IsNotFound(err) {
		// ClusterDeployment is already deleted
		log.Info("ClusterDeployment already deleted", "ClusterDeployment.Name", clusterDeploymentName)

		// Clean up associated PVC
		pvcClusterDeployment := &k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterDeploymentName,
				Namespace: colony.Namespace,
			},
		}
		if err := clusterdeployment.DeletePVCForClusterDeployment(ctx, r.Client, pvcClusterDeployment); err != nil {
			log.Error(err, "Failed to delete PVC for ClusterDeployment", "ClusterDeployment.Name", clusterDeploymentName)
			// Don't return error here as the main deletion was successful
		}

		return false, nil
	}

	if err != nil {
		log.Error(err, "Failed to get ClusterDeployment", "ClusterDeployment.Name", clusterDeploymentName)
		return false, err
	}

	// ClusterDeployment still exists - trigger deletion if not already being deleted
	if clusterDeployment.DeletionTimestamp.IsZero() {
		log.Info("Deleting ClusterDeployment", "ClusterDeployment.Name", clusterDeploymentName, "ClusterDeployment.Namespace", colony.Namespace)
		if err := r.Delete(ctx, clusterDeployment); err != nil && !errors.IsNotFound(err) {
			log.Error(err, "Failed to delete ClusterDeployment", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
			return false, err
		}
	} else {
		log.Info("ClusterDeployment is being deleted, waiting", "ClusterDeployment.Name", clusterDeploymentName)
	}

	// Return true to indicate deletion is still in progress
	return true, nil
}

// updateColonyStatusFromClusters checks all clusters referenced in the Colony status
// and updates the Colony status with a ClusterReady condition that is true only if
// all referenced Clusters have a ready condition.
func (r *ColonyReconciler) updateColonyStatusFromClusters(ctx context.Context, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)
	allReady := true
	var notReadyClusters []string

	// Log to verify NetBird status is preserved
	if colony.Status.NetBird != nil {
		log.V(1).Info("Preserving NetBird status in cluster status update",
			"setupKeyID", colony.Status.NetBird.SetupKeyID)
	}

	// initialize status fields
	colony.Status.TotalClusters = 0
	colony.Status.ReadyClusters = 0

	for _, ref := range colony.Status.ClusterDeploymentRefs {
		clusterDeployment := &k0rdentv1beta1.ClusterDeployment{}
		key := client.ObjectKey{
			Namespace: ref.Namespace,
			Name:      ref.Name,
		}
		if err := r.Get(ctx, key, clusterDeployment); err != nil {
			if errors.IsNotFound(err) {
				log.Info("ClusterDeployment not found", "clusterDeployment", fmt.Sprintf("%s/%s", ref.Namespace, ref.Name))
				notReadyClusters = append(notReadyClusters, fmt.Sprintf("%s/%s (not found)", ref.Namespace, ref.Name))
				allReady = false
				continue
			}
			return err
		}

		clusterDeploymentConditions := clusterDeployment.GetConditions()
		for _, condition := range *clusterDeploymentConditions {
			if condition.Type == k0rdentv1beta1.ReadyCondition && condition.Status == metav1.ConditionFalse {
				allReady = false
				notReadyClusters = append(notReadyClusters, fmt.Sprintf("%s/%s", ref.Namespace, ref.Name))
			}
		}
	}

	colony.Status.TotalClusters = int32(len(colony.Status.ClusterDeploymentRefs))
	colony.Status.ReadyClusters = int32(len(colony.Status.ClusterDeploymentRefs) - len(notReadyClusters))

	var condition metav1.Condition
	if allReady {
		colony.Status.Phase = "Ready"
		condition = metav1.Condition{
			Type:    "ClustersReady",
			Status:  metav1.ConditionTrue,
			Reason:  "AllClustersReady",
			Message: fmt.Sprintf("All %d clusters of the colony are ready", len(colony.Status.ClusterDeploymentRefs)),
		}
	} else {
		colony.Status.Phase = "Provisioning"
		condition = metav1.Condition{
			Type:    "ClustersReady",
			Status:  metav1.ConditionFalse,
			Reason:  "NotAllClustersReady",
			Message: fmt.Sprintf("%d out of %d clusters are ready. Not ready clusters: %v", colony.Status.ReadyClusters, colony.Status.TotalClusters, notReadyClusters),
		}
	}

	meta.SetStatusCondition(&colony.Status.Conditions, condition)

	// Note: Status is now persisted at the end of Reconcile, not here
	// This avoids multiple patches that can conflict and lose NetBird status

	return nil
}

// updateColonyStatusWithRetry handles status updates with conflict resolution
func (r *ColonyReconciler) updateColonyStatusWithRetry(ctx context.Context, colony *infrav1.Colony, phase string) error {
	log := log.FromContext(ctx)
	maxRetries := 5
	backoff := time.Millisecond * 100

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get the latest version of the object
		latest := &infrav1.Colony{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(colony), latest); err != nil {
			if errors.IsNotFound(err) {
				// Object no longer exists, nothing to update
				return nil
			}
			return fmt.Errorf("failed to get latest version of Colony: %w", err)
		}

		// Update the status
		latest.Status.Phase = phase

		// Try to update
		if err := r.Status().Update(ctx, latest); err != nil {
			if errors.IsConflict(err) {
				log.Info("Conflict detected, retrying status update", "attempt", attempt+1, "name", latest.Name)
				time.Sleep(backoff)
				backoff *= 2 // Exponential backoff
				continue
			}
			return fmt.Errorf("failed to update Colony status: %w", err)
		}

		// Update successful, copy the updated object back to colony
		*colony = *latest
		return nil
	}

	return fmt.Errorf("failed to update Colony status after %d attempts due to conflicts", maxRetries)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ColonyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.Colony{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(netbirdpkg.SecretToColonyMapper(mgr.GetClient())),
		).
		Named("infra-colony").
		Complete(r)
}
