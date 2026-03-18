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

package common

import (
	"context"
	"fmt"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// LabelKOFRegionalClusterName is the label on a ColonyCluster that indicates
	// it is a child of a regional cluster. The value is the regional cluster's name.
	LabelKOFRegionalClusterName = "k0rdent.mirantis.com/kof-regional-cluster-name"

	// LabelKOFClusterName is the label on a ClusterDeployment that identifies
	// the cluster by its logical name (used to find the regional ClusterDeployment).
	LabelKOFClusterName = "k0rdent.mirantis.com/kof-cluster-name"
)

// IsRegionalChild checks if a ColonyCluster is a child of a regional cluster.
// Returns (true, regionalName) if the cluster has the regional cluster label,
// (false, "") otherwise. This is the sole detection mechanism — no label means
// legacy behavior, full backward compatibility.
func IsRegionalChild(colonyCluster *infrav1.ColonyCluster) (bool, string) {
	if colonyCluster.ClusterLabels == nil {
		return false, ""
	}
	regionalName, ok := colonyCluster.ClusterLabels[LabelKOFRegionalClusterName]
	if !ok || regionalName == "" {
		return false, ""
	}
	return true, regionalName
}

// FindRegionalClusterDeployment finds the ClusterDeployment for a regional cluster
// by matching the label k0rdent.mirantis.com/kof-cluster-name=<regionalClusterName>.
func FindRegionalClusterDeployment(ctx context.Context, c client.Client, namespace, regionalClusterName string) (*k0rdentv1beta1.ClusterDeployment, error) {
	log := log.FromContext(ctx)

	cdList := &k0rdentv1beta1.ClusterDeploymentList{}
	if err := c.List(ctx, cdList,
		client.InNamespace(namespace),
		client.MatchingLabels{LabelKOFClusterName: regionalClusterName},
	); err != nil {
		return nil, fmt.Errorf("failed to list ClusterDeployments for regional cluster %q: %w", regionalClusterName, err)
	}

	if len(cdList.Items) == 0 {
		return nil, fmt.Errorf("no ClusterDeployment found with label %s=%s in namespace %s",
			LabelKOFClusterName, regionalClusterName, namespace)
	}

	if len(cdList.Items) > 1 {
		log.Info("Multiple ClusterDeployments found for regional cluster, using first",
			"regionalCluster", regionalClusterName,
			"count", len(cdList.Items))
	}

	return &cdList.Items[0], nil
}

// GetRegionalClusterClient builds a controller-runtime client for a regional cluster.
// It fetches the regional cluster's kubeconfig secret from the management cluster
// and creates a client that can talk to the regional cluster's API server.
func GetRegionalClusterClient(ctx context.Context, managementClient client.Client, namespace, regionalCDName string, scheme *runtime.Scheme) (client.Client, error) {
	log := log.FromContext(ctx)

	kubeconfigSecretName := fmt.Sprintf("%s-kubeconfig", regionalCDName)

	var kubeconfigSecret corev1.Secret
	if err := managementClient.Get(ctx, client.ObjectKey{
		Name:      kubeconfigSecretName,
		Namespace: namespace,
	}, &kubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to get regional cluster kubeconfig secret %q: %w", kubeconfigSecretName, err)
	}

	kubeconfigBytes, ok := kubeconfigSecret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("regional cluster kubeconfig secret %q does not contain key 'value'", kubeconfigSecretName)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config for regional cluster %q: %w", regionalCDName, err)
	}

	regionalClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client for regional cluster %q: %w", regionalCDName, err)
	}

	log.V(1).Info("Created client for regional cluster",
		"regionalClusterDeployment", regionalCDName,
		"kubeconfigSecret", kubeconfigSecretName)

	return regionalClient, nil
}

// GetColonyClusterByName looks up a ColonyCluster from the Colony spec by clusterName.
func GetColonyClusterByName(colony *infrav1.Colony, clusterName string) *infrav1.ColonyCluster {
	for i := range colony.Spec.ColonyClusters {
		if colony.Spec.ColonyClusters[i].ClusterName == clusterName {
			return &colony.Spec.ColonyClusters[i]
		}
	}
	return nil
}

// ResolveRegionalClient checks if a cluster is a regional child and, if so,
// builds a client for the regional cluster. Returns (regionalClient, isRegional, error).
// If the cluster is not a regional child, returns (nil, false, nil).
func ResolveRegionalClient(
	ctx context.Context,
	managementClient client.Client,
	colony *infrav1.Colony,
	clusterName string,
	scheme *runtime.Scheme,
) (client.Client, bool, error) {
	log := log.FromContext(ctx)

	colonyCluster := GetColonyClusterByName(colony, clusterName)
	if colonyCluster == nil {
		return nil, false, nil
	}

	isRegional, regionalName := IsRegionalChild(colonyCluster)
	if !isRegional {
		return nil, false, nil
	}

	log.Info("Cluster is a regional child, resolving regional cluster client",
		"cluster", clusterName,
		"regionalCluster", regionalName)

	// Find the regional ClusterDeployment
	regionalCD, err := FindRegionalClusterDeployment(ctx, managementClient, colony.Namespace, regionalName)
	if err != nil {
		return nil, true, fmt.Errorf("failed to find regional ClusterDeployment for %q: %w", regionalName, err)
	}

	// Build client for the regional cluster
	regionalClient, err := GetRegionalClusterClient(ctx, managementClient, colony.Namespace, regionalCD.Name, scheme)
	if err != nil {
		return nil, true, fmt.Errorf("failed to get regional cluster client for %q: %w", regionalName, err)
	}

	return regionalClient, true, nil
}
