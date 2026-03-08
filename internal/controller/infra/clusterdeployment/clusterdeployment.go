package clusterdeployment

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// jsonConfigsEqual compares two JSON configs for semantic equality.
// It unmarshals both sides so that key ordering differences don't cause false positives.
func jsonConfigsEqual(a, b *apiextv1.JSON) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	var aVal, bVal interface{}
	if err := json.Unmarshal(a.Raw, &aVal); err != nil {
		return false
	}
	if err := json.Unmarshal(b.Raw, &bVal); err != nil {
		return false
	}
	return reflect.DeepEqual(aVal, bVal)
}

func EnsureClusterDeployment(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	// Get the typed ClusterDeploymentSpec
	spec, err := colonyCluster.GetClusterDeploymentSpec()
	if err != nil {
		log.Error(err, "failed to get ClusterDeploymentSpec")
		return err
	}
	if spec == nil {
		log.Info("ClusterDeploymentSpec is nil, skipping")
		return nil
	}

	// Extract only the fields we manage (not KCM-managed fields like ipamClaim, serviceSpec, propagateCredentials)
	managedConfig := spec.Config
	managedCredential := spec.Credential
	managedTemplate := spec.Template

	// Full spec is used for initial creation only. Updates use field-specific logic
	// in updateManagedFields to avoid overwriting KCM-managed fields.
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        colony.Name + "-" + colonyCluster.ClusterName,
			Namespace:   colony.Namespace,
			Labels:      colonyCluster.ClusterLabels,
			Annotations: colonyCluster.ClusterAnnotations,
		},
		Spec: *spec,
	}

	if err := controllerutil.SetControllerReference(colony, clusterDeployment, scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on ClusterDeployment: %w", err)
	}

	var actual *k0rdentv1beta1.ClusterDeployment

	existing := &k0rdentv1beta1.ClusterDeployment{}
	key := client.ObjectKey{Name: clusterDeployment.Name, Namespace: clusterDeployment.Namespace}
	if err := c.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, clusterDeployment); err != nil {
				log.Error(err, "Failed to create cluster deployment")
				return err
			}
			log.Info("Created cluster deployment", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
			actual = clusterDeployment
		} else {
			log.Error(err, "Failed to get cluster deployment")
			return err
		}
	} else {
		log.Info("Cluster deployment already exists, checking for updates", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)

		// Update only managed fields to avoid overwriting KCM-managed fields.
		// updateManagedFields re-fetches the latest version internally and returns
		// early (nil, nil) if nothing changed, so no redundant API calls are made.
		updated, err := updateManagedFields(ctx, c, existing,
			managedConfig,
			managedCredential,
			managedTemplate,
			colonyCluster.ClusterLabels,
			colonyCluster.ClusterAnnotations)
		if err != nil {
			log.Error(err, "Failed to update cluster deployment after retries")
			return err
		}
		if updated != nil {
			actual = updated
			log.Info("Updated cluster deployment", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
		} else {
			actual = existing
			log.Info("ClusterDeployment spec, labels, and annotations unchanged", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
		}
	}

	clusterDeploymentRef := &corev1.ObjectReference{
		APIVersion: actual.APIVersion,
		Kind:       actual.Kind,
		Name:       actual.Name,
		Namespace:  actual.Namespace,
	}

	exists := false
	for _, ref := range colony.Status.ClusterDeploymentRefs {
		if ref.Name == clusterDeploymentRef.Name && ref.Namespace == clusterDeploymentRef.Namespace {
			exists = true
			break
		}
	}
	if !exists {
		colony.Status.ClusterDeploymentRefs = append(colony.Status.ClusterDeploymentRefs, clusterDeploymentRef)
		if err := updateColonyStatusWithRetry(ctx, c, colony); err != nil {
			log.Error(err, "Failed to update colony status")
			return err
		}
		log.Info("Updated colony ClusterDeploymentRefs", "name", colony.Name, "namespace", colony.Namespace)
	}

	return nil
}

// updateManagedFields updates only the fields that the operator manages.
// This prevents overwriting KCM-managed fields.
// Returns the updated object on success, or (nil, nil) if no changes were needed.
func updateManagedFields(ctx context.Context, c client.Client,
	existing *k0rdentv1beta1.ClusterDeployment,
	newConfig *apiextv1.JSON,
	newCredential string,
	newTemplate string,
	colonyLabels map[string]string,
	colonyAnnotations map[string]string) (*k0rdentv1beta1.ClusterDeployment, error) {

	log := log.FromContext(ctx)
	maxRetries := 5
	backoff := time.Millisecond * 100

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get the latest version
		latest := &k0rdentv1beta1.ClusterDeployment{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(existing), latest); err != nil {
			return nil, fmt.Errorf("failed to get latest ClusterDeployment: %w", err)
		}

		changed := false

		// Update Config if different
		if !jsonConfigsEqual(latest.Spec.Config, newConfig) {
			latest.Spec.Config = newConfig
			changed = true
		}

		// Update Credential if different
		if latest.Spec.Credential != newCredential {
			latest.Spec.Credential = newCredential
			changed = true
		}

		// Update Template if different
		if latest.Spec.Template != newTemplate {
			latest.Spec.Template = newTemplate
			changed = true
		}

		// Update Labels (merge colony labels with existing to preserve KCM-added labels)
		mergedLabels := mergeLabels(latest.Labels, colonyLabels)
		if !reflect.DeepEqual(latest.Labels, mergedLabels) {
			latest.Labels = mergedLabels
			changed = true
		}

		// Update Annotations (merge colony annotations with existing)
		mergedAnnotations := mergeAnnotations(latest.Annotations, colonyAnnotations)
		if !reflect.DeepEqual(latest.Annotations, mergedAnnotations) {
			latest.Annotations = mergedAnnotations
			changed = true
		}

		// No changes needed
		if !changed {
			return nil, nil
		}

		// Attempt update
		if err := c.Update(ctx, latest); err != nil {
			if errors.IsConflict(err) {
				log.Info("Conflict detected, retrying", "attempt", attempt+1, "name", latest.Name)
				time.Sleep(backoff)
				backoff *= 2
				continue
			}
			return nil, fmt.Errorf("failed to update ClusterDeployment: %w", err)
		}

		return latest, nil
	}

	return nil, fmt.Errorf("failed to update ClusterDeployment after %d attempts", maxRetries)
}

// updateColonyStatusWithRetry handles Colony status updates with conflict resolution
func updateColonyStatusWithRetry(ctx context.Context, c client.Client, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)
	maxRetries := 5
	backoff := time.Millisecond * 100

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get the latest version of the object
		latest := &infrav1.Colony{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(colony), latest); err != nil {
			if errors.IsNotFound(err) {
				// Object no longer exists, nothing to update
				return nil
			}
			return fmt.Errorf("failed to get latest version of Colony: %w", err)
		}

		// Update the status with the new ClusterDeploymentRefs
		latest.Status.ClusterDeploymentRefs = colony.Status.ClusterDeploymentRefs

		// Try to update
		if err := c.Status().Update(ctx, latest); err != nil {
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

// CleanupOrphanedClusterDeployments removes ClusterDeployment objects that are no longer
// referenced in the Colony spec but still exist in the status.
// Returns (hasPendingDeletions, error) - hasPendingDeletions is true if there are still
// ClusterDeployments being deleted that require a requeue.
func CleanupOrphanedClusterDeployments(ctx context.Context, c client.Client, colony *infrav1.Colony) (bool, error) {
	log := log.FromContext(ctx)

	// Create a set of expected cluster names from the spec
	expectedClusterNames := make(map[string]bool)
	for _, colonyCluster := range colony.Spec.ColonyClusters {
		expectedClusterName := colony.Name + "-" + colonyCluster.ClusterName
		expectedClusterNames[expectedClusterName] = true
	}

	// Find orphaned ClusterDeployments
	var deletedRefs []*corev1.ObjectReference
	var pendingDeletions []*corev1.ObjectReference

	for _, ref := range colony.Status.ClusterDeploymentRefs {
		if !expectedClusterNames[ref.Name] {
			// Check if the ClusterDeployment still exists
			clusterDeployment := &k0rdentv1beta1.ClusterDeployment{}
			err := c.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace}, clusterDeployment)

			if errors.IsNotFound(err) {
				// ClusterDeployment is already deleted, we can remove from status
				log.Info("Orphaned ClusterDeployment already deleted", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)

				// Clean up associated PVC
				pvcClusterDeployment := &k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      ref.Name,
						Namespace: ref.Namespace,
					},
				}
				if err := DeletePVCForClusterDeployment(ctx, c, pvcClusterDeployment); err != nil {
					log.Error(err, "Failed to delete PVC for ClusterDeployment", "ClusterDeployment.Name", ref.Name)
					// Don't return error here as the main deletion was successful
				}

				deletedRefs = append(deletedRefs, ref)
				continue
			}

			if err != nil {
				log.Error(err, "Failed to get orphaned ClusterDeployment", "ClusterDeployment.Name", ref.Name)
				return false, err
			}

			// ClusterDeployment still exists - trigger deletion if not already being deleted
			if clusterDeployment.DeletionTimestamp.IsZero() {
				log.Info("Found orphaned ClusterDeployment, triggering deletion", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)
				if err := c.Delete(ctx, clusterDeployment); err != nil && !errors.IsNotFound(err) {
					log.Error(err, "Failed to delete orphaned ClusterDeployment", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)
					return false, err
				}
			} else {
				log.Info("Orphaned ClusterDeployment is being deleted, waiting", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)
			}

			// Track as pending deletion - will be removed from status on next reconciliation
			pendingDeletions = append(pendingDeletions, ref)
		}
	}

	// Remove deleted references from status
	if len(deletedRefs) > 0 {
		orig := colony.DeepCopy()
		var updatedRefs []*corev1.ObjectReference
		for _, ref := range colony.Status.ClusterDeploymentRefs {
			isDeleted := false
			for _, deletedRef := range deletedRefs {
				if ref.Name == deletedRef.Name && ref.Namespace == deletedRef.Namespace {
					isDeleted = true
					break
				}
			}
			if !isDeleted {
				updatedRefs = append(updatedRefs, ref)
			}
		}
		colony.Status.ClusterDeploymentRefs = updatedRefs

		if err := c.Status().Patch(ctx, colony, client.MergeFrom(orig)); err != nil {
			return false, fmt.Errorf("failed to update Colony status after cleanup: %w", err)
		}

		log.Info("Cleaned up orphaned ClusterDeployment references", "count", len(deletedRefs))
	}

	// Return true if there are still pending deletions that need requeue
	return len(pendingDeletions) > 0, nil
}

// DeletePVCForClusterDeployment deletes the PVC associated with a ClusterDeployment
func DeletePVCForClusterDeployment(ctx context.Context, c client.Client, clusterDeployment *k0rdentv1beta1.ClusterDeployment) error {
	log := log.FromContext(ctx)

	// Construct PVC name following the pattern: etcd-data-kmc-<cluster-deployment-name>-etcd-0
	pvcName := fmt.Sprintf("etcd-data-kmc-%s-etcd-0", clusterDeployment.Name)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: clusterDeployment.Namespace,
		},
	}

	if err := c.Delete(ctx, pvc); err != nil {
		if errors.IsNotFound(err) {
			log.Info("PVC not found, already deleted or never existed", "PVC.Name", pvcName, "PVC.Namespace", clusterDeployment.Namespace)
			return nil
		}
		log.Error(err, "Failed to delete PVC", "PVC.Name", pvcName, "PVC.Namespace", clusterDeployment.Namespace)
		return err
	}

	log.Info("PVC deleted successfully", "PVC.Name", pvcName, "PVC.Namespace", clusterDeployment.Namespace)
	return nil
}

// WaitForClusterDeletion polls until the given ClusterDeployment is no longer found.
func WaitForClusterDeletion(ctx context.Context, c client.Client, clusterDeployment *k0rdentv1beta1.ClusterDeployment, timeout, interval time.Duration) error {
	log := log.FromContext(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	timeoutCh := time.After(timeout)
	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for cluster %s deletion", clusterDeployment.Name)
		case <-ticker.C:
			temp := &k0rdentv1beta1.ClusterDeployment{}
			err := c.Get(ctx, client.ObjectKeyFromObject(clusterDeployment), temp)
			if errors.IsNotFound(err) {
				log.Info("ClusterDeployment deleted", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)

				// Delete the associated PVC after ClusterDeployment is confirmed deleted
				if err := DeletePVCForClusterDeployment(ctx, c, clusterDeployment); err != nil {
					log.Error(err, "Failed to delete PVC for ClusterDeployment", "ClusterDeployment.Name", clusterDeployment.Name)
					// Don't return error here as the main deletion was successful
				}

				return nil
			}
			log.Info("Waiting for cluster deletion", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
		}
	}
}

// mergeLabels merges existing labels with new labels from ColonyCluster
// It preserves existing labels and adds/updates only the labels specified in the ColonyCluster
func mergeLabels(existingLabels, newLabels map[string]string) map[string]string {
	merged := make(map[string]string)

	for k, v := range existingLabels {
		merged[k] = v
	}

	for k, v := range newLabels {
		merged[k] = v
	}

	return merged
}

// mergeAnnotations merges existing annotations with new annotations from ColonyCluster
// It preserves existing annotations and adds/updates only the annotations specified in the ColonyCluster
func mergeAnnotations(existingAnnotations, newAnnotations map[string]string) map[string]string {
	merged := make(map[string]string)

	for k, v := range existingAnnotations {
		merged[k] = v
	}

	for k, v := range newAnnotations {
		merged[k] = v
	}

	return merged
}
