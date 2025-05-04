package clusterdeployment

import (
	"context"
	"fmt"

	k0rdentv1alpha1 "github.com/K0rdent/kcm/api/v1alpha1"
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

	clusterDeployment := &k0rdentv1alpha1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
			Namespace: colony.Namespace,
		},
		Spec: *colonyCluster.ClusterDeploymentSpec,
	}

	if err := controllerutil.SetControllerReference(colony, clusterDeployment, scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on ClusterDeployment: %w", err)
	}

	var actual *k0rdentv1alpha1.ClusterDeployment

	existing := &k0rdentv1alpha1.ClusterDeployment{}
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
		log.Info("Cluster deployment already exists", "name", clusterDeployment.Name, "namespace", clusterDeployment.Namespace)
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
