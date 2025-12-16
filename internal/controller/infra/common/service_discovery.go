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

// Package common provides shared utilities for infrastructure controllers.
package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DiscoverControlPlaneService discovers the k0smotron control plane Service for a cluster.
// Uses label selectors to find services, supporting name variations like -nodeport or -loadbalancer suffixes.
// Returns the service with validated ClusterIP and ports 30443, 30132.
func DiscoverControlPlaneService(ctx context.Context, c client.Client, namespace, colonyName, clusterName string) (*corev1.Service, error) {
	log := log.FromContext(ctx)

	// List services by label selector instead of exact name
	// This handles variations like: kmc-colony-cluster-a-nodeport, kmc-colony-cluster-a-loadbalancer, etc.
	serviceList := &corev1.ServiceList{}
	err := c.List(ctx, serviceList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			"app":       "k0smotron",
			"component": "cluster",
			"cluster":   fmt.Sprintf("%s-%s", colonyName, clusterName),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to list control plane services: %w", err)
	}

	if len(serviceList.Items) == 0 {
		expectedPrefix := fmt.Sprintf("kmc-%s-%s", colonyName, clusterName)
		log.Info("Control plane service not found yet, will retry on next reconciliation",
			"expectedPrefix", expectedPrefix,
			"cluster", clusterName,
			"namespace", namespace)
		return nil, fmt.Errorf("control plane service not found yet (cluster still provisioning)")
	}

	// Return the first matching service
	service := &serviceList.Items[0]

	// Validate ClusterIP exists
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		return nil, fmt.Errorf("control plane service %s has no ClusterIP (type: %s)", service.Name, service.Spec.Type)
	}

	// Validate required ports exist
	if err := ValidateControlPlanePorts(service); err != nil {
		return nil, fmt.Errorf("control plane service %s port validation failed: %w", service.Name, err)
	}

	log.Info("Discovered control plane service",
		"service", service.Name,
		"namespace", service.Namespace,
		"cluster", clusterName,
		"clusterIP", service.Spec.ClusterIP,
		"type", service.Spec.Type,
		"ports", len(service.Spec.Ports))

	return service, nil
}

// ValidateControlPlanePorts validates that required ports exist in the service.
// Checks by port name (more reliable) and falls back to common port numbers.
func ValidateControlPlanePorts(service *corev1.Service) error {
	hasAPIPort := false
	hasKonnectivityPort := false

	for _, port := range service.Spec.Ports {
		// Check by name (more reliable)
		if port.Name == "api" || port.Name == "kube-apiserver" {
			hasAPIPort = true
		}
		if port.Name == "konnectivity" {
			hasKonnectivityPort = true
		}

		// Fallback: check by common port numbers
		if port.Port == 30443 || port.Port == 6443 {
			hasAPIPort = true
		}
		if port.Port == 30132 || port.Port == 8132 {
			hasKonnectivityPort = true
		}
	}

	if !hasAPIPort {
		return fmt.Errorf("API server port not found (expected port named 'api' or ports 30443/6443)")
	}
	if !hasKonnectivityPort {
		return fmt.Errorf("konnectivity port not found (expected port named 'konnectivity' or ports 30132/8132)")
	}
	return nil
}

// GetAPIServerPort returns the API server port from the service.
// Checks by port name first, then falls back to common port numbers.
func GetAPIServerPort(service *corev1.Service) int32 {
	for _, port := range service.Spec.Ports {
		if port.Name == "api" || port.Name == "kube-apiserver" {
			return port.Port
		}
	}
	// Fallback to common port numbers
	for _, port := range service.Spec.Ports {
		if port.Port == 30443 || port.Port == 6443 {
			return port.Port
		}
	}
	return 30443 // default
}

// GetServiceExternalAddress extracts the external address from a LoadBalancer service.
// Returns the hostname or IP and a boolean indicating if an external address was found.
func GetServiceExternalAddress(service *corev1.Service) (string, bool) {
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return "", false
	}

	// Check for LoadBalancer ingress
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		ingress := service.Status.LoadBalancer.Ingress[0]
		if ingress.Hostname != "" {
			return ingress.Hostname, true
		}
		if ingress.IP != "" {
			return ingress.IP, true
		}
	}
	return "", false
}

// ExtractClusterNameFromDeploymentName extracts the cluster name from a ClusterDeployment name.
// ClusterDeployment names follow the pattern: <colony-name>-<cluster-name>
func ExtractClusterNameFromDeploymentName(deploymentName, colonyName string) string {
	prefix := colonyName + "-"
	if len(deploymentName) <= len(prefix) {
		return ""
	}
	if deploymentName[:len(prefix)] != prefix {
		return ""
	}
	return deploymentName[len(prefix):]
}

// DetermineAPIEndpoint determines the API endpoint for a control plane service.
// If the service is a LoadBalancer with an external address, it returns the external address.
// Otherwise, it returns the ClusterIP.
// Returns the endpoint in format "host:port" and an error if the endpoint cannot be determined.
func DetermineAPIEndpoint(service *corev1.Service) (string, error) {
	// Validate ClusterIP exists (as fallback)
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		return "", fmt.Errorf("service %s has no valid ClusterIP", service.Name)
	}

	// Get API server port
	apiPort := GetAPIServerPort(service)

	// Check if this is a LoadBalancer service with external address
	externalAddress, hasExternalAddress := GetServiceExternalAddress(service)

	if hasExternalAddress {
		// Use external LoadBalancer address
		return fmt.Sprintf("%s:%d", externalAddress, apiPort), nil
	}

	// Fall back to ClusterIP
	return fmt.Sprintf("%s:%d", service.Spec.ClusterIP, apiPort), nil
}


