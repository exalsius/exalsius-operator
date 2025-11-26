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

package cilium

import (
	"context"
	"fmt"
	"strings"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// ConfigMapName is the name of the ConfigMap containing the API endpoint
	ConfigMapName = "cp-api-endpoint"
	// ConfigMapNamespace is the namespace where the ConfigMap is created
	ConfigMapNamespace = "kube-system"
)

// EnsureAPIEndpointConfigMap creates or updates the cp-api-endpoint ConfigMap in the child cluster
// with the control plane API server endpoint information.
func EnsureAPIEndpointConfigMap(
	ctx context.Context,
	managementClient client.Client,
	colony *infrav1.Colony,
	clusterName string,
	exposedEndpoint string,
	scheme *runtime.Scheme,
) error {
	log := log.FromContext(ctx)

	// Parse the endpoint into host and port
	host, port, err := parseEndpoint(exposedEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint %q: %w", exposedEndpoint, err)
	}

	log.Info("Parsed API endpoint",
		"cluster", clusterName,
		"endpoint", exposedEndpoint,
		"host", host,
		"port", port)

	// Get client for the child cluster
	childClient, err := getClientForChildCluster(ctx, managementClient, colony, clusterName, scheme)
	if err != nil {
		return fmt.Errorf("failed to get client for child cluster: %w", err)
	}

	// Check if ConfigMap already exists with correct values
	existingCM := &corev1.ConfigMap{}
	err = childClient.Get(ctx, client.ObjectKey{
		Name:      ConfigMapName,
		Namespace: ConfigMapNamespace,
	}, existingCM)

	if err == nil {
		// ConfigMap exists, check if it has the correct values
		if existingCM.Data["host"] == host && existingCM.Data["port"] == port {
			log.Info("ConfigMap already exists with correct values, skipping update",
				"cluster", clusterName,
				"configMap", ConfigMapName)
			return nil
		}

		// Update existing ConfigMap
		existingCM.Data["host"] = host
		existingCM.Data["port"] = port
		if err := childClient.Update(ctx, existingCM); err != nil {
			return fmt.Errorf("failed to update ConfigMap: %w", err)
		}

		log.Info("Updated API endpoint ConfigMap in child cluster",
			"cluster", clusterName,
			"configMap", ConfigMapName,
			"host", host,
			"port", port)

		return nil
	}

	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Create new ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: ConfigMapNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "exalsius-operator",
				"exalsius.ai/colony":           colony.Name,
				"exalsius.ai/cluster":          clusterName,
			},
		},
		Data: map[string]string{
			"host": host,
			"port": port,
		},
	}

	if err := childClient.Create(ctx, cm); err != nil {
		return fmt.Errorf("failed to create ConfigMap: %w", err)
	}

	log.Info("Created API endpoint ConfigMap in child cluster",
		"cluster", clusterName,
		"configMap", ConfigMapName,
		"host", host,
		"port", port)

	return nil
}

// getClientForChildCluster creates a Kubernetes client for accessing a child cluster
// using its kubeconfig secret directly.
// Kubeconfig secret name format: {colony-name}-{cluster-name}-kubeconfig
func getClientForChildCluster(
	ctx context.Context,
	managementClient client.Client,
	colony *infrav1.Colony,
	clusterName string,
	scheme *runtime.Scheme,
) (client.Client, error) {
	log := log.FromContext(ctx)

	// Build kubeconfig secret name directly
	// Format: {colony-name}-{cluster-name}-kubeconfig
	kubeconfigSecretName := fmt.Sprintf("%s-%s-kubeconfig", colony.Name, clusterName)

	// Get the kubeconfig secret
	var kubeconfigSecret corev1.Secret
	if err := managementClient.Get(ctx, client.ObjectKey{
		Name:      kubeconfigSecretName,
		Namespace: colony.Namespace,
	}, &kubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %q: %w", kubeconfigSecretName, err)
	}

	// Extract the kubeconfig data
	kubeconfigBytes, ok := kubeconfigSecret.Data["value"]
	if !ok {
		return nil, fmt.Errorf("kubeconfig secret %q does not contain key 'value'", kubeconfigSecretName)
	}

	// Create REST config from kubeconfig
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config for cluster %q: %w", clusterName, err)
	}

	// Create client for the child cluster
	childClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client for cluster %q: %w", clusterName, err)
	}

	log.V(1).Info("Created client for child cluster", "cluster", clusterName, "kubeconfigSecret", kubeconfigSecretName)

	return childClient, nil
}

// parseEndpoint parses an endpoint string in the format "host:port" and returns
// the host and port components separately.
func parseEndpoint(endpoint string) (host string, port string, err error) {
	// Handle IPv6 addresses in brackets [::1]:port
	if strings.HasPrefix(endpoint, "[") {
		closeBracket := strings.Index(endpoint, "]")
		if closeBracket == -1 {
			return "", "", fmt.Errorf("invalid IPv6 endpoint format: %s", endpoint)
		}
		host = endpoint[1:closeBracket]
		if len(endpoint) > closeBracket+1 && endpoint[closeBracket+1] == ':' {
			port = endpoint[closeBracket+2:]
		} else {
			return "", "", fmt.Errorf("missing port in IPv6 endpoint: %s", endpoint)
		}
		return host, port, nil
	}

	// Handle regular IPv4 or hostname:port
	parts := strings.Split(endpoint, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid endpoint format, expected host:port, got: %s", endpoint)
	}

	host = parts[0]
	port = parts[1]

	if host == "" || port == "" {
		return "", "", fmt.Errorf("host or port is empty in endpoint: %s", endpoint)
	}

	return host, port, nil
}
