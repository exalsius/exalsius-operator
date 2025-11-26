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

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	netbirdrest "github.com/netbirdio/netbird/management/client/rest"
	"github.com/netbirdio/netbird/management/server/http/api"
)

const (
	netbirdImage = "netbirdio/netbird:0.60.2"
)

// ensureRoutingPeerDeployment ensures that a NetBird routing peer Deployment exists for the Colony.
func ensureRoutingPeerDeployment(ctx context.Context, c client.Client, colony *infrav1.Colony, routersGroupID string) error {
	log := log.FromContext(ctx)

	deploymentName := fmt.Sprintf("%s-netbird-router", colony.Name)

	// Use router-specific setup key that auto-assigns to routers group
	setupKeySecretName := colony.Status.NetBird.RouterSetupKeySecretName
	if setupKeySecretName == "" {
		// Fallback to expected name if not in status yet
		setupKeySecretName = fmt.Sprintf("%s-netbird-router-setup-key", colony.Name)
		log.Info("Router setup key secret name not in status, using default",
			"secretName", setupKeySecretName,
			"routersGroupID", routersGroupID)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: colony.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":       "netbird",
					"colony":    colony.Name,
					"component": "router",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":       "netbird",
						"colony":    colony.Name,
						"component": "router",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "netbird",
							Image: netbirdImage,
							Env: []corev1.EnvVar{
								{
									Name: "NB_SETUP_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: setupKeySecretName,
											},
											Key: "setupKey",
										},
									},
								},
								{
									Name:  "NB_MANAGEMENT_URL",
									Value: getManagementURL(colony),
								},
								{
									Name:  "NB_HOSTNAME",
									Value: fmt.Sprintf("%s-netbird-router", colony.Name),
								},
								{
									Name:  "NB_LOG_LEVEL",
									Value: "info",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_ADMIN", "SYS_RESOURCE", "SYS_ADMIN"},
								},
							},
						},
					},
				},
			},
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(colony, deployment, c.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	existing := &appsv1.Deployment{}
	err := c.Get(ctx, client.ObjectKeyFromObject(deployment), existing)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Creating NetBird routing peer Deployment", "name", deploymentName, "namespace", colony.Namespace)
			if err := c.Create(ctx, deployment); err != nil {
				return fmt.Errorf("failed to create routing peer Deployment: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get routing peer Deployment: %w", err)
	}

	// Update existing deployment if needed
	needsUpdate := false
	if existing.Spec.Replicas == nil || *existing.Spec.Replicas != 1 {
		existing.Spec.Replicas = int32Ptr(1)
		needsUpdate = true
	}

	// Check if setup key changed
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		container := &existing.Spec.Template.Spec.Containers[0]
		for i, env := range container.Env {
			if env.Name == "NB_SETUP_KEY" {
				// Check if using SecretKeyRef with correct secret name
				if env.ValueFrom == nil ||
					env.ValueFrom.SecretKeyRef == nil ||
					env.ValueFrom.SecretKeyRef.Name != setupKeySecretName {
					container.Env[i].ValueFrom = &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: setupKeySecretName,
							},
							Key: "setupKey",
						},
					}
					container.Env[i].Value = "" // Clear any direct value
					needsUpdate = true
				}
			}
		}
	}

	if needsUpdate {
		log.Info("Updating NetBird routing peer Deployment", "name", deploymentName, "namespace", colony.Namespace)
		if err := c.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update routing peer Deployment: %w", err)
		}
	}

	// Check if deployment is ready
	if existing.Status.ReadyReplicas < 1 {
		log.Info("NetBird routing peer Deployment not ready yet", "readyReplicas", existing.Status.ReadyReplicas)
		return fmt.Errorf("routing peer Deployment not ready")
	}

	return nil
}

func getManagementURL(colony *infrav1.Colony) string {
	if colony.Spec.NetBird != nil && colony.Spec.NetBird.ManagementURL != "" {
		return colony.Spec.NetBird.ManagementURL
	}
	return "https://api.netbird.io"
}

func int32Ptr(i int32) *int32 {
	return &i
}

// ensureRoutingPeerInRoutersGroup ensures that the routing peer is added to the routers group.
// This function finds the routing peer by hostname and adds it to the group if not already present.
// It is idempotent and safe to call on every reconciliation.
func ensureRoutingPeerInRoutersGroup(ctx context.Context, nbClient *netbirdrest.Client, colony *infrav1.Colony, routersGroupID string) error {
	log := log.FromContext(ctx)

	// Expected hostname for the routing peer
	routerHostname := fmt.Sprintf("%s-netbird-router", colony.Name)

	// List all peers to find the routing peer by hostname
	allPeers, err := nbClient.Peers.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list peers: %w", err)
	}

	// Find routing peer by hostname
	var routingPeerID string
	for _, peer := range allPeers {
		if peer.Hostname == routerHostname {
			routingPeerID = peer.Id
			break
		}
	}

	if routingPeerID == "" {
		// Peer not connected yet, will retry on next reconciliation
		log.V(1).Info("Routing peer not connected yet", "hostname", routerHostname)
		return nil
	}

	// Get current routers group
	routersGroup, err := nbClient.Groups.Get(ctx, routersGroupID)
	if err != nil {
		return fmt.Errorf("failed to get routers group: %w", err)
	}

	// Check if routing peer is already in the group
	for _, peer := range routersGroup.Peers {
		if peer.Id == routingPeerID {
			log.V(1).Info("Routing peer already in routers group", "peerID", routingPeerID)
			return nil
		}
	}

	// Build updated peer list with routing peer added
	updatedPeerIDs := make([]string, 0, len(routersGroup.Peers)+1)
	for _, peer := range routersGroup.Peers {
		updatedPeerIDs = append(updatedPeerIDs, peer.Id)
	}
	updatedPeerIDs = append(updatedPeerIDs, routingPeerID)

	// Update the group with the new peer list
	log.Info("Adding routing peer to routers group", "peerID", routingPeerID, "groupID", routersGroupID)
	_, err = nbClient.Groups.Update(ctx, routersGroupID, api.GroupRequest{
		Name:  routersGroup.Name,
		Peers: &updatedPeerIDs,
	})
	if err != nil {
		return fmt.Errorf("failed to add routing peer to routers group: %w", err)
	}

	log.Info("Successfully added routing peer to routers group", "peerID", routingPeerID, "groupID", routersGroupID)
	return nil
}

// ensureNetworkRouters ensures that all peers in the routers group are configured as Network Routers
// with masquerade enabled. This enables L3 reachability to ClusterIPs from remote peers.
// Uses PeerGroups instead of individual peer IDs for automatic inclusion of all group members.
func ensureNetworkRouters(ctx context.Context, nbClient *netbirdrest.Client, networkID string, groupID string, colonyName string) error {
	log := log.FromContext(ctx)

	// List existing routers for the network
	routers, err := nbClient.Networks.Routers(networkID).List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list network routers: %w", err)
	}

	// Check if a router already exists for this peer group
	for _, router := range routers {
		// Check if this router is configured with our peer group
		if router.PeerGroups != nil && len(*router.PeerGroups) > 0 {
			for _, routerGroupID := range *router.PeerGroups {
				if routerGroupID == groupID {
					// Router exists with correct peer group
					// Verify masquerade is enabled
					if !router.Enabled || !router.Masquerade || router.Metric != 9999 {
						log.Info("Updating Network Router configuration",
							"routerID", router.Id,
							"groupID", groupID,
							"colony", colonyName)
						_, err := nbClient.Networks.Routers(networkID).Update(ctx, router.Id, api.NetworkRouterRequest{
							Enabled:    true,
							Masquerade: true,
							Metric:     9999,
							PeerGroups: &[]string{groupID},
						})
						if err != nil {
							return fmt.Errorf("failed to update network router: %w", err)
						}
					} else {
						log.Info("Network Router already correctly configured",
							"routerID", router.Id,
							"groupID", groupID,
							"colony", colonyName)
					}
					return nil
				}
			}
		}
	}

	// No router found for this peer group, create one
	log.Info("Creating Network Router with peer group",
		"groupID", groupID,
		"colony", colonyName,
		"masquerade", true,
		"metric", 9999)

	_, err = nbClient.Networks.Routers(networkID).Create(ctx, api.NetworkRouterRequest{
		Enabled:    true,
		Masquerade: true,
		Metric:     9999,
		PeerGroups: &[]string{groupID},
	})
	if err != nil {
		return fmt.Errorf("failed to create network router: %w", err)
	}

	log.Info("Created Network Router with peer group", "groupID", groupID, "colony", colonyName)
	return nil
}
