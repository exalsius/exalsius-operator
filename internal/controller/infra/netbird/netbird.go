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

package netbird

import (
	"context"
	"fmt"
	"strings"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	"github.com/exalsius/exalsius-operator/internal/controller/infra/cilium"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ReconcileNetBird reconciles NetBird integration for a Colony.
func ReconcileNetBird(ctx context.Context, c client.Client, colony *infrav1.Colony, scheme *runtime.Scheme) error {
	log := log.FromContext(ctx)

	if colony.Spec.NetBird == nil || !colony.Spec.NetBird.Enabled {
		log.Info("NetBird integration is disabled or not configured")
		return nil
	}

	// Initialize NetBird status if needed
	if colony.Status.NetBird == nil {
		colony.Status.NetBird = &infrav1.NetBirdStatus{
			ClusterResources: make(map[string]infrav1.ClusterNetBirdStatus),
		}
	}
	if colony.Status.NetBird.ClusterResources == nil {
		colony.Status.NetBird.ClusterResources = make(map[string]infrav1.ClusterNetBirdStatus)
	}

	// Get NetBird client first
	nbClient, err := GetNetBirdClientFromSecrets(ctx, c, colony)
	if err != nil {
		return fmt.Errorf("failed to get NetBird client: %w", err)
	}

	// Ensure network exists
	networkID, err := ensureNetwork(ctx, nbClient, colony)
	if err != nil {
		return fmt.Errorf("failed to ensure network: %w", err)
	}
	colony.Status.NetBird.NetworkID = networkID

	// Ensure colony-scoped group exists and get its ID
	groupID, err := ensureColonyNodesGroup(ctx, nbClient, colony.Name)
	if err != nil {
		return fmt.Errorf("failed to ensure colony nodes group: %w", err)
	}
	colony.Status.NetBird.ColonyNodesGroupID = groupID

	// Ensure colony-scoped routers group exists (for Network Routers config)
	routersGroupID, err := ensureColonyRoutersGroup(ctx, nbClient, colony.Name)
	if err != nil {
		return fmt.Errorf("failed to ensure colony routers group: %w", err)
	}
	colony.Status.NetBird.ColonyRoutersGroupID = routersGroupID

	// Ensure setup key exists - check secret first to avoid regenerating
	if err := ensureSetupKeysReconciled(ctx, c, nbClient, colony, groupID); err != nil {
		return err
	}

	// Ensure router-specific setup key exists
	ensureRouterSetupKeyReconciled(ctx, c, nbClient, colony, groupID, routersGroupID)

	// Ensure routing peer Deployment exists and is properly configured
	if err := reconcileRoutingPeer(ctx, c, nbClient, colony, routersGroupID, networkID); err != nil {
		return err
	}

	// Ensure colony-scoped mesh policy exists
	policyID, err := ensureColonyMeshPolicy(ctx, nbClient, groupID, colony.Status.NetBird.ColonyMeshPolicyID, colony.Name)
	if err != nil {
		return fmt.Errorf("failed to ensure colony mesh policy: %w", err)
	}
	colony.Status.NetBird.ColonyMeshPolicyID = policyID

	// Reconcile control plane exposure for each cluster
	reconcileClusters(ctx, c, nbClient, colony, networkID, scheme)

	// After exposing control planes, patch bootstrap secrets for workers to use internal DNS
	if err := WatchAndPatchBootstrapSecrets(ctx, c, colony); err != nil {
		log.Error(err, "Failed to patch bootstrap secrets, workers may not join correctly")
		// Don't fail the reconciliation - this can be retried
	}

	return nil
}

// ensureSetupKeysReconciled ensures that the main setup key exists for worker nodes.
// It handles setup key recovery from existing secrets and generation of new keys if needed.
func ensureSetupKeysReconciled(ctx context.Context, c client.Client, nbClient *NetBirdClient,
	colony *infrav1.Colony, groupID string) error {
	log := log.FromContext(ctx)
	secretName := fmt.Sprintf("%s-netbird-setup-key", colony.Name)

	if colony.Status.NetBird.SetupKeyID == "" {
		// Status is empty, but secret might exist (status might have been lost)
		if setupKeySecretExists(ctx, c, colony) {
			// Secret exists! Try to recover the setup key ID from NetBird API
			log.Info("Setup key secret exists but status is empty, attempting to recover setup key ID from NetBird API")
			setupKeyID, err := findSetupKeyIDByName(ctx, nbClient, colony.Name)
			if err != nil {
				log.Info("Could not find setup key by name in NetBird, will generate a new one", "error", err.Error())
				// Fall through to generate new key
			} else {
				// Successfully recovered!
				colony.Status.NetBird.SetupKeyID = setupKeyID
				colony.Status.NetBird.SetupKeySecretName = secretName
				log.Info("Successfully recovered setup key ID from NetBird API",
					"setupKeyID", setupKeyID,
					"secretName", secretName)
				// Don't generate a new key - return early
				return nil
			}
		}

		// Either secret doesn't exist, or we couldn't recover the ID - generate new key
		setupKeyValue, setupKeyID, err := generateSetupKey(ctx, nbClient, colony.Name, groupID)
		if err != nil {
			return fmt.Errorf("failed to generate setup key: %w", err)
		}

		// Store in Secret
		if err := ensureSetupKeySecret(ctx, c, colony, setupKeyValue); err != nil {
			return fmt.Errorf("failed to create setup key secret: %w", err)
		}

		colony.Status.NetBird.SetupKeyID = setupKeyID
		colony.Status.NetBird.SetupKeySecretName = secretName

		log.Info("Successfully generated and stored setup key",
			"setupKeyID", setupKeyID,
			"secretName", secretName)
	}

	return nil
}

// ensureRouterSetupKeyReconciled ensures that the router-specific setup key exists.
// This setup key automatically assigns peers to BOTH the nodes group (for mesh connectivity)
// and the routers group (for network router configuration).
// This function logs errors but does not fail reconciliation, as it can be retried.
func ensureRouterSetupKeyReconciled(ctx context.Context, c client.Client, nbClient *NetBirdClient,
	colony *infrav1.Colony, nodesGroupID, routersGroupID string) {
	log := log.FromContext(ctx)
	routerSetupKeySecretName := fmt.Sprintf("%s-netbird-router-setup-key", colony.Name)

	if colony.Status.NetBird.RouterSetupKeyID == "" {
		// Generate new router setup key
		routerSetupKeyValue, routerSetupKeyID, err := generateRouterSetupKey(ctx, nbClient, colony.Name, nodesGroupID, routersGroupID)
		if err != nil {
			log.Error(err, "Failed to generate router setup key, will retry")
			// Don't fail - we can retry on next reconciliation
		} else {
			// Store in Secret
			if err := ensureRouterSetupKeySecret(ctx, c, colony, routerSetupKeyValue); err != nil {
				log.Error(err, "Failed to create router setup key secret")
			} else {
				colony.Status.NetBird.RouterSetupKeyID = routerSetupKeyID
				colony.Status.NetBird.RouterSetupKeySecretName = routerSetupKeySecretName
				log.Info("Successfully generated and stored router setup key",
					"setupKeyID", routerSetupKeyID,
					"secretName", routerSetupKeySecretName)
			}
		}
	}
}

// reconcileRoutingPeer ensures the routing peer deployment exists and is properly configured.
// It also ensures network routers are configured for NodePort clusters.
func reconcileRoutingPeer(ctx context.Context, c client.Client, nbClient *NetBirdClient,
	colony *infrav1.Colony, routersGroupID, networkID string) error {
	log := log.FromContext(ctx)

	// Ensure routing peer Deployment exists
	// Note: We don't fail if it's not ready yet, we just create it and let it start
	if err := ensureRoutingPeerDeployment(ctx, c, colony, routersGroupID); err != nil {
		// Check if it's just a "not ready" error
		if err.Error() == "routing peer Deployment not ready" {
			log.Info("Routing peer Deployment is starting, will check again on next reconciliation")
			colony.Status.NetBird.RouterReady = false
			// Don't return error, continue with the rest of the reconciliation
		} else {
			log.Error(err, "Failed to ensure routing peer Deployment")
			colony.Status.NetBird.RouterReady = false
			return err
		}
	} else {
		colony.Status.NetBird.RouterReady = true

		// No longer needed - router setup key assigns to both groups automatically
		// The router-specific setup key has AutoGroups set to both nodes and routers groups
	}

	// Ensure Network Routers are configured (requires routers group ID)
	// Only routers in the routers group should be configured as network routers, not all nodes
	if err := ensureNetworkRouters(ctx, nbClient, networkID, routersGroupID, colony.Name); err != nil {
		log.Error(err, "Failed to ensure Network Routers")
		// Don't fail reconciliation, but log the error
	}

	return nil
}

// reconcileClusters reconciles control plane exposure for all clusters in the colony.
func reconcileClusters(ctx context.Context, c client.Client, nbClient *NetBirdClient,
	colony *infrav1.Colony, networkID string, scheme *runtime.Scheme) {
	log := log.FromContext(ctx)

	// Reconcile control plane exposure for each cluster
	for _, clusterRef := range colony.Status.ClusterDeploymentRefs {
		clusterName := extractClusterNameFromDeploymentName(clusterRef.Name, colony.Name)
		if clusterName == "" {
			log.Info("Could not extract cluster name from ClusterDeployment", "name", clusterRef.Name)
			continue
		}

		clusterDeployment := &k0rdentv1beta1.ClusterDeployment{}
		if err := c.Get(ctx, client.ObjectKey{Name: clusterRef.Name, Namespace: clusterRef.Namespace}, clusterDeployment); err != nil {
			if errors.IsNotFound(err) {
				log.Info("ClusterDeployment not found, skipping", "name", clusterRef.Name)
				continue
			}
			log.Error(err, "Failed to get ClusterDeployment", "name", clusterRef.Name)
			// Continue with other clusters
			continue
		}

		if err := reconcileControlPlaneExposure(ctx, c, nbClient, colony, clusterName, networkID, scheme); err != nil {
			// Check if it's a "service not found" error (expected during provisioning)
			if errors.IsNotFound(err) ||
				(err.Error() != "" && (contains(err.Error(), "not found yet") ||
					contains(err.Error(), "still provisioning"))) {
				log.Info("Control plane service not available yet, will retry", "cluster", clusterName)
			} else {
				log.Error(err, "Failed to reconcile control plane exposure", "cluster", clusterName)
			}
			// Continue with other clusters
			continue
		}
	}
}

// reconcileControlPlaneExposure exposes the control plane service for a cluster.
// Behavior differs based on service type:
// - LoadBalancer: Workers connect directly to external LB address
// - NodePort: Workers connect via NetBird routing peer (creates Network Resource)
// Access control is handled by the shared colony mesh policy.
func reconcileControlPlaneExposure(
	ctx context.Context,
	c client.Client,
	nbClient *NetBirdClient,
	colony *infrav1.Colony,
	clusterName string,
	networkID string,
	scheme *runtime.Scheme,
) error {
	log := log.FromContext(ctx)

	// Discover control plane service
	service, err := discoverControlPlaneService(ctx, c, colony, clusterName)
	if err != nil {
		return fmt.Errorf("failed to discover control plane service: %w", err)
	}

	// Get or initialize cluster status
	clusterStatus := colony.Status.NetBird.ClusterResources[clusterName]

	// Get API server port
	apiPort := getAPIServerPort(service)

	// Check if this is a LoadBalancer service with external address
	externalAddress, hasExternalAddress := getServiceExternalAddress(service)
	isLoadBalancer := service.Spec.Type == corev1.ServiceTypeLoadBalancer

	log.Info("Detected control plane service configuration",
		"cluster", clusterName,
		"serviceType", service.Spec.Type,
		"apiPort", apiPort,
		"hasExternalAddress", hasExternalAddress,
		"externalAddress", externalAddress)

	var workerEndpoint string
	var ciliumEndpoint string // Endpoint for Cilium ConfigMap (may differ from worker endpoint)
	var resourceID string

	if isLoadBalancer && hasExternalAddress {
		// LoadBalancer with external address: Workers connect directly to LB
		workerEndpoint = fmt.Sprintf("%s:%d", externalAddress, apiPort)
		ciliumEndpoint = workerEndpoint // Cilium uses same external LB address
		clusterStatus.ExternalAddress = externalAddress
		clusterStatus.UseDirectConnection = true

		// No NetBird Network Resource needed for LoadBalancer
		// Workers will connect directly to the external LB address
		log.Info("Using LoadBalancer direct connection mode",
			"cluster", clusterName,
			"workerEndpoint", workerEndpoint,
			"externalAddress", externalAddress)
	} else {
		// NodePort or LoadBalancer without external address yet:
		// Use NetBird routing peer for internal service routing

		// Get group ID from status
		destinationGroup := colony.Status.NetBird.ColonyNodesGroupID
		if destinationGroup == "" {
			return fmt.Errorf("colony nodes group ID not set in status")
		}

		// Use internal Kubernetes DNS name as resource address
		// Format: service-name.namespace.svc.cluster.local:port
		resourceAddress := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)

		// Cilium uses internal DNS (runs inside cluster)
		ciliumEndpoint = fmt.Sprintf("%s:%d", resourceAddress, apiPort)

		// Create/update Network Resource with internal DNS name
		var err error
		resourceID, err = ensureNetworkResource(ctx, nbClient, colony, clusterName, networkID, resourceAddress, destinationGroup)
		if err != nil {
			return fmt.Errorf("failed to ensure network resource: %w", err)
		}

		// Build router peer DNS name for workers to use
		// NetBird automatically assigns DNS names to peers: {hostname}.netbird.cloud
		routerPeerName := fmt.Sprintf("%s-netbird-router.netbird.cloud", colony.Name)

		// Workers connect to router peer
		// The router peer routes traffic to the internal service via masquerade
		workerEndpoint = fmt.Sprintf("%s:%d", routerPeerName, apiPort)
		clusterStatus.UseDirectConnection = false

		log.Info("Using NetBird routing peer connection mode",
			"cluster", clusterName,
			"workerEndpoint", workerEndpoint,
			"ciliumEndpoint", ciliumEndpoint,
			"resourceAddress", resourceAddress,
			"resourceID", resourceID)
	}

	// Update status
	clusterStatus.ControlPlaneResourceID = resourceID
	clusterStatus.ExposedEndpoint = workerEndpoint
	clusterStatus.ServiceName = service.Name
	clusterStatus.ServiceNamespace = service.Namespace
	clusterStatus.ServiceType = string(service.Spec.Type)
	clusterStatus.Ready = true
	colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus

	log.Info("Control plane exposed successfully",
		"cluster", clusterName,
		"serviceType", service.Spec.Type,
		"workerEndpoint", workerEndpoint,
		"useDirectConnection", clusterStatus.UseDirectConnection)

	// Create/update the Cilium API endpoint ConfigMap in the child cluster
	// This must happen as soon as the endpoint is known, so Cilium can start properly
	// Note: Use ciliumEndpoint (internal DNS for NodePort, external for LoadBalancer)
	if err := cilium.EnsureAPIEndpointConfigMap(ctx, c, colony, clusterName, ciliumEndpoint, scheme); err != nil {
		// Distinguish between "not ready yet" (expected) and actual errors
		if errors.IsNotFound(err) || isConnectionError(err) {
			log.Info("Child cluster not ready yet, will retry Cilium ConfigMap creation on next reconcile",
				"cluster", clusterName,
				"reason", getNotReadyReason(err))
		} else {
			log.Error(err, "Failed to create Cilium API endpoint ConfigMap, will retry",
				"cluster", clusterName)
		}
		// Don't fail the reconciliation - this will be retried on the next reconcile
	}

	return nil
}

// discoverControlPlaneService discovers the k0smotron control plane Service for a cluster.
// Uses label selectors to find services, supporting name variations like -nodeport or -loadbalancer suffixes.
// Returns the service with validated ClusterIP and ports 30443, 30132.
func discoverControlPlaneService(ctx context.Context, c client.Client, colony *infrav1.Colony, clusterName string) (*corev1.Service, error) {
	log := log.FromContext(ctx)

	// List services by label selector instead of exact name
	// This handles variations like: kmc-colony-cluster-a-nodeport, kmc-colony-cluster-a-loadbalancer, etc.
	serviceList := &corev1.ServiceList{}
	err := c.List(ctx, serviceList,
		client.InNamespace(colony.Namespace),
		client.MatchingLabels{
			"app":       "k0smotron",
			"component": "cluster",
			"cluster":   fmt.Sprintf("%s-%s", colony.Name, clusterName),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to list control plane services: %w", err)
	}

	if len(serviceList.Items) == 0 {
		expectedPrefix := fmt.Sprintf("kmc-%s-%s", colony.Name, clusterName)
		log.Info("Control plane service not found yet, will retry on next reconciliation",
			"expectedPrefix", expectedPrefix,
			"cluster", clusterName,
			"namespace", colony.Namespace)
		return nil, fmt.Errorf("control plane service not found yet (cluster still provisioning)")
	}

	// Return the first matching service
	service := &serviceList.Items[0]

	// Validate ClusterIP exists
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == "None" {
		return nil, fmt.Errorf("control plane service %s has no ClusterIP (type: %s)", service.Name, service.Spec.Type)
	}

	// Validate required ports exist
	if err := validateControlPlanePorts(service); err != nil {
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

// validateControlPlanePorts validates that required ports exist in the service.
// Checks by port name (more reliable) and falls back to common port numbers.
func validateControlPlanePorts(service *corev1.Service) error {
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

// getAPIServerPort returns the API server port from the service.
// Checks by port name first, then falls back to common port numbers.
func getAPIServerPort(service *corev1.Service) int32 {
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

// getServiceExternalAddress extracts the external address from a LoadBalancer service.
// Returns the hostname or IP and a boolean indicating if an external address was found.
func getServiceExternalAddress(service *corev1.Service) (string, bool) {
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

// extractClusterNameFromDeploymentName extracts the cluster name from a ClusterDeployment name.
// ClusterDeployment names follow the pattern: <colony-name>-<cluster-name>
func extractClusterNameFromDeploymentName(deploymentName, colonyName string) string {
	prefix := colonyName + "-"
	if len(deploymentName) <= len(prefix) {
		return ""
	}
	if deploymentName[:len(prefix)] != prefix {
		return ""
	}
	return deploymentName[len(prefix):]
}

// deleteSecretIfExists deletes a secret if it exists, logging errors but not failing.
func deleteSecretIfExists(ctx context.Context, c client.Client, secretName, namespace string) {
	log := log.FromContext(ctx)

	if secretName == "" {
		return
	}

	log.Info("Deleting secret", "secretName", secretName)
	secret := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, secret)
	if err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get secret for deletion", "secretName", secretName)
		}
		return
	}

	if err := c.Delete(ctx, secret); err != nil {
		log.Error(err, "Failed to delete secret", "secretName", secretName)
	}
}

// CleanupNetBirdResources cleans up NetBird resources when a Colony is deleted.
func CleanupNetBirdResources(ctx context.Context, c client.Client, nbClient *NetBirdClient, colony *infrav1.Colony) error {
	log := log.FromContext(ctx)

	if colony.Status.NetBird == nil {
		return nil
	}

	// Step 1: Delete the network
	if colony.Status.NetBird.NetworkID != "" {
		log.Info("Deleting NetBird network", "networkID", colony.Status.NetBird.NetworkID)
		if err := nbClient.DeleteNetwork(ctx, colony.Status.NetBird.NetworkID); err != nil {
			log.Error(err, "Failed to delete network", "networkID", colony.Status.NetBird.NetworkID)
			// Continue with cleanup
		}
	}

	// Step 2: Delete setup keys
	if colony.Status.NetBird.SetupKeyID != "" {
		log.Info("Deleting NetBird setup key", "setupKeyID", colony.Status.NetBird.SetupKeyID)
		if err := nbClient.DeleteSetupKey(ctx, colony.Status.NetBird.SetupKeyID); err != nil {
			log.Error(err, "Failed to delete setup key")
		}
	}
	if colony.Status.NetBird.RouterSetupKeyID != "" {
		log.Info("Deleting NetBird router setup key", "setupKeyID", colony.Status.NetBird.RouterSetupKeyID)
		if err := nbClient.DeleteSetupKey(ctx, colony.Status.NetBird.RouterSetupKeyID); err != nil {
			log.Error(err, "Failed to delete router setup key")
		}
	}

	// Step 3: Delete tracked peers
	for _, peerID := range colony.Status.NetBird.TrackedPeerIDs {
		log.Info("Deleting NetBird peer", "peerID", peerID)
		if err := nbClient.DeletePeer(ctx, peerID); err != nil {
			log.Error(err, "Failed to delete peer", "peerID", peerID)
		}
	}

	// Step 4: Delete groups
	if colony.Status.NetBird.ColonyNodesGroupID != "" {
		log.Info("Deleting colony nodes group", "groupID", colony.Status.NetBird.ColonyNodesGroupID)
		if err := nbClient.DeleteGroup(ctx, colony.Status.NetBird.ColonyNodesGroupID); err != nil {
			log.Error(err, "Failed to delete colony nodes group")
		}
	}
	if colony.Status.NetBird.ColonyRoutersGroupID != "" {
		log.Info("Deleting colony routers group", "groupID", colony.Status.NetBird.ColonyRoutersGroupID)
		if err := nbClient.DeleteGroup(ctx, colony.Status.NetBird.ColonyRoutersGroupID); err != nil {
			log.Error(err, "Failed to delete colony routers group")
		}
	}

	// Step 5: Delete policies
	if colony.Status.NetBird.ColonyMeshPolicyID != "" {
		log.Info("Deleting colony mesh policy", "policyID", colony.Status.NetBird.ColonyMeshPolicyID)
		if err := nbClient.DeletePolicy(ctx, colony.Status.NetBird.ColonyMeshPolicyID); err != nil {
			log.Error(err, "Failed to delete policy")
		}
	}

	// Step 6: Delete Kubernetes resources (secrets and deployment)
	deleteSecretIfExists(ctx, c, colony.Status.NetBird.SetupKeySecretName, colony.Namespace)
	deleteSecretIfExists(ctx, c, colony.Status.NetBird.RouterSetupKeySecretName, colony.Namespace)

	deploymentName := fmt.Sprintf("%s-netbird-router", colony.Name)
	deployment := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: colony.Namespace}, deployment); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get routing peer Deployment: %w", err)
		}
	} else {
		log.Info("Deleting NetBird routing peer Deployment", "name", deploymentName)
		if err := c.Delete(ctx, deployment); err != nil {
			return fmt.Errorf("failed to delete routing peer Deployment: %w", err)
		}
	}

	return nil
}

// ensureNetworkResource ensures a Network Resource exists with the given address and destination group.
func ensureNetworkResource(
	ctx context.Context,
	nbClient *NetBirdClient,
	colony *infrav1.Colony,
	clusterName, networkID, address, destinationGroup string,
) (string, error) {
	log := log.FromContext(ctx)

	resourceName := fmt.Sprintf("ctlplane-%s", clusterName)
	expectedDescription := fmt.Sprintf("Control plane for cluster %s in Colony %s", clusterName, colony.Name)

	// Get existing status
	clusterStatus := colony.Status.NetBird.ClusterResources[clusterName]

	resourceReq := NetworkResourceRequest{
		Name:        resourceName,
		Address:     address,
		Enabled:     true,
		Groups:      []string{destinationGroup},
		Description: stringPtr(expectedDescription),
	}

	// 1. Check if we have a stored resource ID - try to get it directly
	if clusterStatus.ControlPlaneResourceID != "" {
		resource, err := nbClient.GetNetworkResource(ctx, networkID, clusterStatus.ControlPlaneResourceID)
		if err == nil {
			log.Info("Found resource by stored ID", "resourceID", resource.ID, "name", resource.Name)
			// Resource exists, update if needed
			if resource.Address != address || resource.Name != resourceName {
				log.Info("Updating resource address or name", "resourceID", resource.ID, "oldAddress", resource.Address, "newAddress", address)
				_, err := nbClient.UpdateNetworkResource(ctx, networkID, resource.ID, resourceReq)
				if err != nil {
					return "", fmt.Errorf("failed to update network resource: %w", err)
				}
			}
			// Update status with current address
			clusterStatus.ControlPlaneResourceAddress = address
			colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
			return resource.ID, nil
		}
		// Resource ID invalid, clear it and proceed to search
		log.Info("Stored resource ID not found, will search by name/address", "resourceID", clusterStatus.ControlPlaneResourceID)
	}

	// 2. List all resources and search by name first, then address
	resources, err := nbClient.ListNetworkResources(ctx, networkID)
	if err != nil {
		return "", fmt.Errorf("failed to list network resources: %w", err)
	}

	// Search by name (exact match)
	for _, resource := range resources {
		if resource.Name == resourceName {
			log.Info("Found existing resource by name", "resourceID", resource.ID, "name", resource.Name)
			// Update if address changed
			if resource.Address != address {
				_, err := nbClient.UpdateNetworkResource(ctx, networkID, resource.ID, resourceReq)
				if err != nil {
					return "", fmt.Errorf("failed to update network resource: %w", err)
				}
			}
			// Update status
			clusterStatus.ControlPlaneResourceAddress = address
			colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
			return resource.ID, nil
		}
	}

	// Fuzzy match by address (contains substring)
	for _, resource := range resources {
		if resource.Address == address || strings.Contains(resource.Address, address) || strings.Contains(address, resource.Address) {
			log.Info("Found existing resource by address match", "resourceID", resource.ID, "address", resource.Address)
			// Update to correct name
			_, err := nbClient.UpdateNetworkResource(ctx, networkID, resource.ID, resourceReq)
			if err != nil {
				return "", fmt.Errorf("failed to update resource during migration: %w", err)
			}
			// Update status
			clusterStatus.ControlPlaneResourceAddress = address
			colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
			return resource.ID, nil
		}
	}

	// 3. Create new resource
	log.Info("Creating new Network Resource", "name", resourceName, "address", address, "group", destinationGroup)
	resource, err := nbClient.CreateNetworkResource(ctx, networkID, resourceReq)
	if err != nil {
		// Check if it's an "already exists" error (name or address conflict)
		if contains(err.Error(), "already exists") {
			log.Info("Resource creation failed with 'already exists', searching for existing resource")
			// Re-list resources to find the conflicting one
			refreshedResources, listErr := nbClient.ListNetworkResources(ctx, networkID)
			if listErr != nil {
				return "", fmt.Errorf("resource already exists but failed to list resources: %w", listErr)
			}
			// Try to find by name first
			for _, r := range refreshedResources {
				if r.Name == resourceName {
					log.Info("Found existing resource with same name", "resourceID", r.ID)
					// Update status
					clusterStatus.ControlPlaneResourceAddress = address
					colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
					return r.ID, nil
				}
			}
			// Try to find by address (fuzzy match)
			for _, r := range refreshedResources {
				if r.Address == address || strings.Contains(r.Address, address) || strings.Contains(address, r.Address) {
					log.Info("Found existing resource with same address", "resourceID", r.ID)
					// Update status
					clusterStatus.ControlPlaneResourceAddress = address
					colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
					return r.ID, nil
				}
			}
			return "", fmt.Errorf("resource already exists but could not find it by name or address")
		}
		return "", fmt.Errorf("failed to create network resource: %w", err)
	}

	log.Info("Created Network Resource", "resourceID", resource.ID, "name", resourceName, "address", address, "group", destinationGroup)
	// Update status
	clusterStatus.ControlPlaneResourceAddress = address
	colony.Status.NetBird.ClusterResources[clusterName] = clusterStatus
	return resource.ID, nil
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func stringPtr(s string) *string {
	return &s
}

// isConnectionError checks if the error is a connection/network error
// indicating the API server is not ready yet.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connect: connection timed out")
}

// getNotReadyReason returns a human-readable reason for why the cluster is not ready.
func getNotReadyReason(err error) string {
	if errors.IsNotFound(err) {
		return "kubeconfig not found"
	}
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") {
		return "API server not ready"
	}
	if strings.Contains(errStr, "i/o timeout") {
		return "API server timeout"
	}
	if strings.Contains(errStr, "no such host") {
		return "DNS not ready"
	}
	return "cluster starting up"
}

func boolPtr(b bool) *bool {
	return &b
}
