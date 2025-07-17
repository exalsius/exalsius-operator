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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func EnsureClusterDeployment(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	var k0rdentSpec k0rdentv1beta1.ClusterDeploymentSpec
	if err := json.Unmarshal(colonyCluster.ClusterDeploymentSpec.Raw, &k0rdentSpec); err != nil {
		log.Error(err, "failed to unmarshal ClusterDeploymentSpec")
		return err
	}

	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
		},
		Spec: k0rdentSpec,
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

		// Check if the spec has changed
		if !reflect.DeepEqual(existing.Spec, clusterDeployment.Spec) {
			log.Info("ClusterDeployment spec has changed, updating", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)

			// Update the existing ClusterDeployment with the new spec
			existing.Spec = clusterDeployment.Spec
			if err := c.Update(ctx, existing); err != nil {
				log.Error(err, "Failed to update cluster deployment")
				return err
			}
			log.Info("Updated cluster deployment", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
		} else {
			log.Info("ClusterDeployment spec unchanged", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
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
		if err := c.Status().Update(ctx, colony); err != nil {
			log.Error(err, "Failed to update colony status")
			return err
		}
		log.Info("Updated colony ClusterDeploymentRefs", "name", colony.Name, "namespace", colony.Namespace)
	}

	return nil
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
			if err := waitForClusterDeletion(ctx, c, clusterDeployment, 10*time.Minute, 10*time.Second); err != nil {
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

// waitForClusterDeletion polls until the given ClusterDeployment is no longer found.
func waitForClusterDeletion(ctx context.Context, c client.Client, clusterDeployment *k0rdentv1beta1.ClusterDeployment, timeout, interval time.Duration) error {
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
				return nil
			}
			log.Info("Waiting for cluster deletion", "ClusterDeployment.Namespace", clusterDeployment.Namespace, "ClusterDeployment.Name", clusterDeployment.Name)
		}
	}
}
