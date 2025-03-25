package capi

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	helmv1alpha1 "sigs.k8s.io/cluster-api-addon-provider-helm/api/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// ensureCluster ensures that the cluster exists.
func EnsureCluster(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	labels := make(map[string]string)
	if colony.Spec.WorkloadDependencies != nil {
		for _, workloadDependency := range *colony.Spec.WorkloadDependencies {
			var hcp helmv1alpha1.HelmChartProxy
			err := c.Get(ctx, types.NamespacedName{
				Name:      workloadDependency.Name,
				Namespace: "default", // TODO: at some point we need to discuss what resources should have their own namespace
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
	}

	if colony.Spec.AdditionalDependencies != nil {
		newLabels, err := ensureAdditionalDependencies(ctx, c, colony, scheme)
		if err != nil {
			log.Error(err, "Failed to ensure additional dependencies")
			return err
		}

		for k, v := range newLabels {
			labels[k] = v
		}
	}

	log.Info("Ensuring cluster", "Cluster.Name", colonyCluster.ClusterName)
	cluster := &clusterv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName,
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
			ControlPlaneRef:   getControlPlaneRef(colony, colonyCluster),
			InfrastructureRef: getInfrastructureRef(colony, colonyCluster),
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

func EnsureMachineDeployment(ctx context.Context, c client.Client, colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) error {
	log := log.FromContext(ctx)

	md := &clusterv1.MachineDeployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.x-k8s.io/v1beta1",
			Kind:       "MachineDeployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      colony.Name + "-" + colonyCluster.ClusterName + "-md",
			Namespace: colony.Namespace,
		},
		Spec: clusterv1.MachineDeploymentSpec{
			ClusterName: colony.Name + "-" + colonyCluster.ClusterName,
			Replicas:    ptr.To(colonyCluster.Docker.Replicas),
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cluster.x-k8s.io/cluster-name": colony.Name + "-" + colonyCluster.ClusterName,
					// TODO: make this dynamic
					"pool": colony.Name + "-" + colonyCluster.ClusterName + "-pool",
				},
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": colony.Name + "-" + colonyCluster.ClusterName,
						// TODO: make this dynamic
						"pool": colony.Name + "-" + colonyCluster.ClusterName + "-pool",
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: colony.Name + "-" + colonyCluster.ClusterName,
					Version:     ptr.To(colony.Spec.K8sVersion),
					Bootstrap: clusterv1.Bootstrap{
						ConfigRef: &corev1.ObjectReference{
							APIVersion: "bootstrap.cluster.x-k8s.io/v1beta1",
							Kind:       "K0sWorkerConfigTemplate",
							Name:       colony.Name + "-" + colonyCluster.ClusterName + "-machine-config",
						},
					},
					InfrastructureRef: getMachineInfrastructureRef(colony, colonyCluster),
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

func getControlPlaneRef(colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) *corev1.ObjectReference {
	if colony.Spec.HostedControlPlaneEnabled != nil && *colony.Spec.HostedControlPlaneEnabled {
		return &corev1.ObjectReference{
			APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
			Kind:       "K0smotronControlPlane",
			Name:       colony.Name + "-" + colonyCluster.ClusterName + "-cp",
		}
	}

	return &corev1.ObjectReference{
		APIVersion: "controlplane.cluster.x-k8s.io/v1beta1",
		Kind:       "K0sControlPlane",
		Name:       colony.Name + "-" + colonyCluster.ClusterName + "-cp",
	}
}

func getInfrastructureRef(colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) *corev1.ObjectReference {
	// TODO: check if infrastructureRef can contain multiple providers/clusters
	if colonyCluster.AWS != nil {
		return &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSCluster",
			Name:       colony.Name + "-" + colonyCluster.ClusterName,
		}
	}
	if colonyCluster.Docker != nil {
		return &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "DockerCluster",
			Name:       colony.Name + "-" + colonyCluster.ClusterName,
		}
	}
	if colonyCluster.RemoteClusterEnabled != nil && *colonyCluster.RemoteClusterEnabled {
		return &corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
			Kind:       "RemoteCluster",
			Name:       colony.Name + "-" + colonyCluster.ClusterName,
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

func getMachineInfrastructureRef(colony *infrav1.Colony, colonyCluster *infrav1.ColonyCluster) corev1.ObjectReference {
	if colonyCluster.AWS != nil {
		return corev1.ObjectReference{
			APIVersion: "infrastructure.cluster.x-k8s.io/v1beta2",
			Kind:       "AWSMachineTemplate",
			Name:       colony.Name + "-" + colonyCluster.ClusterName + "-mt",
			Namespace:  colony.Namespace,
		}
	}
	// Default to Docker
	return corev1.ObjectReference{
		APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
		Kind:       "DockerMachineTemplate",
		Name:       colony.Name + "-" + colonyCluster.ClusterName + "-mt",
		Namespace:  colony.Namespace,
	}
}

func ensureAdditionalDependencies(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) (map[string]string, error) {
	log := log.FromContext(ctx)

	// for now we only support one namespace for additional dependencies
	namespace := "default"
	newLabels := make(map[string]string)

	if colony.Spec.AdditionalDependencies == nil {
		return newLabels, nil
	}

	for _, chart := range *colony.Spec.AdditionalDependencies {
		existingChart := &helmv1alpha1.HelmChartProxy{}
		err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: chart.Name}, existingChart)

		if err == nil {
			log.Info("HelmChartProxy already exists", "HelmChartProxy.Namespace", namespace, "HelmChartProxy.Name", chart.Name)
			for k, v := range existingChart.Spec.ClusterSelector.MatchLabels {
				newLabels[k] = v
			}
			continue
		}

		if !errors.IsNotFound(err) {
			log.Error(err, "failed to get HelmChartProxy", "HelmChartProxy.Namespace", namespace, "HelmChartProxy.Name", chart.Name)
			return nil, err
		}

		// HelmChartProxy does not exist, create it
		newChart := &helmv1alpha1.HelmChartProxy{
			TypeMeta: metav1.TypeMeta{
				APIVersion: helmv1alpha1.GroupVersion.String(),
				Kind:       "HelmChartProxy",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      chart.Name,
				Namespace: namespace,
			},
			Spec: chart.Spec,
		}

		// Set the owner reference for garbage collection
		if err := controllerutil.SetControllerReference(colony, newChart, scheme); err != nil {
			log.Error(err, "failed to set owner reference for HelmChartProxy", "HelmChartProxy.Namespace", newChart.Namespace, "HelmChartProxy.Name", newChart.Name)
			return nil, err
		}

		if err := c.Create(ctx, newChart); err != nil {
			log.Error(err, "failed to create HelmChartProxy", "HelmChartProxy.Namespace", newChart.Namespace, "HelmChartProxy.Name", newChart.Name)
			return nil, err
		}

		log.Info("Created HelmChartProxy", "HelmChartProxy.Namespace", newChart.Namespace, "HelmChartProxy.Name", newChart.Name)

		for k, v := range newChart.Spec.ClusterSelector.MatchLabels {
			newLabels[k] = v
		}
	}

	return newLabels, nil
}
