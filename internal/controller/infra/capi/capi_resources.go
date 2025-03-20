package capi

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	helmv1alpha1 "sigs.k8s.io/cluster-api-addon-provider-helm/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// ensureCluster ensures that the cluster exists.
func EnsureCluster(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	labels := make(map[string]string)
	for _, workloadDependency := range colony.Spec.WorkloadDependencies {
		var hcp helmv1alpha1.HelmChartProxy
		err := c.Get(ctx, types.NamespacedName{
			Name:      workloadDependency.Name,
			Namespace: colony.Namespace,
		}, &hcp)
		if err == nil {
			// Merge the HelmChartProxy labels with existing labels
			for k, v := range hcp.Spec.ClusterSelector.MatchLabels {
				labels[k] = v
			}
		} else {
			log.Error(err, "Failed to get HelmChartProxy", "HelmChartProxy.Namespace", hcp.Namespace, "HelmChartProxy.Name", hcp.Name)
		}
	}

	cluster := &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName,
			Namespace: colony.Namespace,
			Labels:    labels,
		},
		Spec: clusterv1.ClusterSpec{
			ClusterNetwork: &clusterv1.ClusterNetwork{
				Pods: &clusterv1.NetworkRanges{
					CIDRBlocks: []string{"192.168.0.0/16"},
				},
				ServiceDomain: "cluster.local",
				Services: &clusterv1.NetworkRanges{
					CIDRBlocks: []string{"10.128.0.0/12"},
				},
			},
			ControlPlaneRef:   getControlPlaneRef(colony),
			InfrastructureRef: getInfrastructureRef(colony),
		},
	}

	existingCluster := &clusterv1.Cluster{}
	err := c.Get(ctx, client.ObjectKeyFromObject(cluster), existingCluster)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Cluster not found. Creating...")
			if err := c.Create(ctx, cluster); err != nil {
				log.Error(err, "failed to create Cluster", "Cluster.Namespace", cluster.Namespace, "Cluster.Name", cluster.Name)
				return err
			}
			log.Info("Created Cluster", "Cluster.Namespace", cluster.Namespace, "Cluster.Name", cluster.Name)
			existingCluster = cluster
		} else {
			log.Error(err, "failed to get Cluster", "Cluster.Namespace", cluster.Namespace, "Cluster.Name", cluster.Name)
			return err
		}
	}

	clusterRef := &corev1.ObjectReference{
		APIVersion: existingCluster.APIVersion,
		Kind:       existingCluster.Kind,
		Name:       existingCluster.Name,
		Namespace:  existingCluster.Namespace,
	}

	exists := false
	for _, ref := range colony.Status.ClusterRefs {
		if ref.Name == clusterRef.Name && ref.Namespace == clusterRef.Namespace {
			exists = true
			break
		}
	}

	if !exists {
		colony.Status.ClusterRefs = append(colony.Status.ClusterRefs, clusterRef)
		if err := c.Status().Update(ctx, colony); err != nil {
			log.Error(err, "failed to update Colony status")
			return err
		}
		log.Info("Updated Colony status", "Colony.Namespace", colony.Namespace, "Colony.Name", colony.Name)
	}

	return nil
}

func EnsureMachineDeployment(ctx context.Context, c client.Client, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)

	md := &clusterv1.MachineDeployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.x-k8s.io/v1beta1",
			Kind:       "MachineDeployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Spec.ClusterName + "-md",
			Namespace: colony.Namespace,
		},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: colony.Spec.ClusterName,
			Replicas:    ptr.To(colony.Spec.Docker.Replicas),
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster.x-k8s.io/cluster-name": colony.Spec.ClusterName,
					// TODO: make this dynamic
					"pool": "worker-pool-1",
				},
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": colony.Spec.ClusterName,
						// TODO: make this dynamic
						"pool": "worker-pool-1",
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: colony.Spec.ClusterName,
					Version:     ptr.To(colony.Spec.K8sVersion),
					Bootstrap: clusterv1.Bootstrap{
						ConfigRef: &corev1.ObjectReference{
							APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
							Kind:       "K0sWorkerConfigTemplate",
							Name:       colony.Spec.ClusterName + "-machine-config",
							Namespace:  colony.Namespace,
						},
					},
					InfrastructureRef: getMachineInfrastructureRef(colony),
				},
			},
		},
	}

	existing := &clusterv1.MachineDeployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: md.Namespace, Name: md.Name}, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := c.Create(ctx, md); err != nil {
				log.Error(err, "failed to create MachineDeployment", "Namespace", md.Namespace, "Name", md.Name)
				return err
			}
			log.Info("Created MachineDeployment", "Namespace", md.Namespace, "Name", md.Name)
		} else {
			return err
		}
	} else {
		log.Info("MachineDeployment already exists", "Namespace", md.Namespace, "Name", md.Name)
	}

	return nil
}

func getControlPlaneRef(colony *infrav1.Colony) *corev1.ObjectReference {
	if colony.Spec.HostedControlPlaneEnabled != nil && *colony.Spec.HostedControlPlaneEnabled {
		return &corev1.ObjectReference{
			APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
			Kind:       "K0smotronControlPlane",
			Name:       colony.Spec.ClusterName + "-cp",
		}
	}

	return &corev1.ObjectReference{
		APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
		Kind:       "K0sControlPlane",
		Name:       colony.Spec.ClusterName,
	}
}

func getInfrastructureRef(colony *infrav1.Colony) *corev1.ObjectReference {
	// TODO: check if infrastructureRef can contain multiple providers/clusters
	if colony.Spec.AWS != nil {
		return &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSCluster",
			Name:       colony.Spec.ClusterName,
		}
	}
	if colony.Spec.Docker != nil {
		return &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "DockerCluster",
			Name:       colony.Spec.ClusterName,
		}
	}
	// if colony.Spec.Azure != nil {
	//     return &corev1.ObjectReference{
	//         APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
	//         Kind:       "AzureCluster",
	//         Name:       colony.Spec.ClusterName,
	//     }
	// }

	return nil
}

func getMachineInfrastructureRef(colony *infrav1.Colony) corev1.ObjectReference {
	if colony.Spec.AWS != nil {
		return corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSMachineTemplate",
			Name:       colony.Spec.ClusterName + "-mt",
			Namespace:  colony.Namespace,
		}
	}
	// Default to Docker
	return corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
		Kind:       "DockerMachineTemplate",
		Name:       colony.Spec.ClusterName + "-mt",
		Namespace:  colony.Namespace,
	}
}
