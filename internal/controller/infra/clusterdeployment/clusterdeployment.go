package clusterdeployment

import (
	"context"
	"fmt"
	"reflect"
	"time"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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

	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
			Labels:    colonyCluster.ClusterLabels,
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

		// Merge labels: preserve existing labels and add/update our labels
		mergedLabels := mergeLabels(existing.Labels, colonyCluster.ClusterLabels)

		// Check if the spec or our labels have changed
		specChanged := !reflect.DeepEqual(existing.Spec, clusterDeployment.Spec)
		labelsChanged := !reflect.DeepEqual(existing.Labels, mergedLabels)

		if specChanged || labelsChanged {
			log.Info("ClusterDeployment spec or labels have changed, updating",
				"name", clusterDeployment.Name,
				"namespace", clusterDeployment.Namespace,
				"specChanged", specChanged,
				"labelsChanged", labelsChanged)

			// Update the existing object with new spec and merged labels
			existing.Spec = clusterDeployment.Spec
			existing.Labels = mergedLabels

			// Retry update with conflict resolution
			if err := updateClusterDeploymentWithRetry(ctx, c, existing, clusterDeployment.Spec, colonyCluster); err != nil {
				log.Error(err, "Failed to update cluster deployment after retries")
				return err
			}
			log.Info("Updated cluster deployment", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
		} else {
			log.Info("ClusterDeployment spec and labels unchanged", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
		}
		actual = existing
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

// updateClusterDeploymentWithRetry handles the update with conflict resolution
func updateClusterDeploymentWithRetry(ctx context.Context, c client.Client, existing *k0rdentv1beta1.ClusterDeployment, newSpec k0rdentv1beta1.ClusterDeploymentSpec, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)
	maxRetries := 5
	backoff := time.Millisecond * 100

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get the latest version of the object
		latest := &k0rdentv1beta1.ClusterDeployment{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(existing), latest); err != nil {
			return fmt.Errorf("failed to get latest version of ClusterDeployment: %w", err)
		}

		// Update the spec and merge labels (preserving existing labels from other components)
		latest.Spec = newSpec
		latest.Labels = mergeLabels(latest.Labels, colonyCluster.ClusterLabels)

		// Try to update
		if err := c.Update(ctx, latest); err != nil {
			if errors.IsConflict(err) {
				log.Info("Conflict detected, retrying update", "attempt", attempt+1, "name", latest.Name)
				time.Sleep(backoff)
				backoff *= 2 // Exponential backoff
				continue
			}
			return fmt.Errorf("failed to update ClusterDeployment: %w", err)
		}

		// Update successful, copy the updated object back to existing
		*existing = *latest
		return nil
	}

	return fmt.Errorf("failed to update ClusterDeployment after %d attempts due to conflicts", maxRetries)
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
// referenced in the Colony spec but still exist in the status
func CleanupOrphanedClusterDeployments(ctx context.Context, c client.Client, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)

	// Create a set of expected cluster names from the spec
	expectedClusterNames := make(map[string]bool)
	for _, colonyCluster := range colony.Spec.ColonyClusters {
		expectedClusterName := colony.Name + "-" + colonyCluster.ClusterName
		expectedClusterNames[expectedClusterName] = true
	}

	// Find and delete orphaned ClusterDeployments
	var orphanedRefs []*corev1.ObjectReference
	for _, ref := range colony.Status.ClusterDeploymentRefs {
		if !expectedClusterNames[ref.Name] {
			log.Info("Found orphaned ClusterDeployment, deleting", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)

			clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ref.Name,
					Namespace: ref.Namespace,
				},
			}

			if err := c.Delete(ctx, clusterDeployment); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete orphaned ClusterDeployment", "ClusterDeployment.Name", ref.Name, "ClusterDeployment.Namespace", ref.Namespace)
				return err
			}

			// Wait for the cluster to be deleted
			if err := WaitForClusterDeletion(ctx, c, clusterDeployment, 10*time.Minute, 10*time.Second); err != nil {
				log.Error(err, "Failed to wait for orphaned ClusterDeployment deletion")
				return err
			}

			orphanedRefs = append(orphanedRefs, ref)
		}
	}

	// Remove orphaned references from status
	if len(orphanedRefs) > 0 {
		orig := colony.DeepCopy()
		var updatedRefs []*corev1.ObjectReference
		for _, ref := range colony.Status.ClusterDeploymentRefs {
			isOrphaned := false
			for _, orphanedRef := range orphanedRefs {
				if ref.Name == orphanedRef.Name && ref.Namespace == orphanedRef.Namespace {
					isOrphaned = true
					break
				}
			}
			if !isOrphaned {
				updatedRefs = append(updatedRefs, ref)
			}
		}
		colony.Status.ClusterDeploymentRefs = updatedRefs

		if err := c.Status().Patch(ctx, colony, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("failed to update Colony status after cleanup: %w", err)
		}

		log.Info("Cleaned up orphaned ClusterDeployment references", "count", len(orphanedRefs))
	}

	return nil
}

// deletePVCForClusterDeployment deletes the PVC associated with a ClusterDeployment
func deletePVCForClusterDeployment(ctx context.Context, c client.Client, clusterDeployment *k0rdentv1beta1.ClusterDeployment) error {
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
				if err := deletePVCForClusterDeployment(ctx, c, clusterDeployment); err != nil {
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
