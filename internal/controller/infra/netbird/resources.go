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
	netbirdrest "github.com/netbirdio/netbird/management/client/rest"
	"github.com/netbirdio/netbird/management/server/http/api"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ensureNetwork ensures that a NetBird network exists for the Colony.
func ensureNetwork(ctx context.Context, nbClient *netbirdrest.Client, colony *infrav1.Colony) (string, error) {
	log := log.FromContext(ctx)

	// If network ID already stored, verify it still exists
	if colony.Status.NetBird != nil && colony.Status.NetBird.NetworkID != "" {
		// Try to get the network to verify it exists
		networks, err := nbClient.Networks.List(ctx)
		if err == nil {
			for _, n := range networks {
				if n.Id == colony.Status.NetBird.NetworkID {
					log.Info("Using existing NetBird network", "networkID", n.Id, "name", n.Name)
					return n.Id, nil
				}
			}
		}
		// Network not found, will create a new one
		log.Info("Stored network ID not found, will create new network")
	}

	// List existing networks to find one with matching name
	networkName := fmt.Sprintf("%s-colony", colony.Name)
	networks, err := nbClient.Networks.List(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list networks: %w", err)
	}

	// Try to find existing network
	for _, n := range networks {
		if n.Name == networkName {
			log.Info("Found existing NetBird network", "networkID", n.Id, "name", n.Name)
			return n.Id, nil
		}
	}

	// Create new network
	log.Info("Creating new NetBird network", "name", networkName)
	network, err := nbClient.Networks.Create(ctx, api.PostApiNetworksJSONRequestBody{
		Name:        networkName,
		Description: stringPtr(fmt.Sprintf("Network for Colony %s", colony.Name)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create network: %w", err)
	}

	log.Info("Created NetBird network", "networkID", network.Id, "name", network.Name)
	return network.Id, nil
}

// ensureColonyGroup is a generic helper that ensures a colony-specific group exists and returns its ID.
func ensureColonyGroup(ctx context.Context, nbClient *netbirdrest.Client, colonyName, groupSuffix string) (string, error) {
	log := log.FromContext(ctx)
	groupName := fmt.Sprintf("%s-%s", colonyName, groupSuffix)

	// List existing groups
	groups, err := nbClient.Groups.List(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list groups: %w", err)
	}

	// Check if group already exists
	for _, group := range groups {
		if group.Name == groupName {
			log.Info("Colony group already exists", "groupName", groupName, "groupID", group.Id, "type", groupSuffix)
			return group.Id, nil
		}
	}

	// Create new group
	log.Info("Creating colony group", "groupName", groupName, "type", groupSuffix)
	group, err := nbClient.Groups.Create(ctx, api.PostApiGroupsJSONRequestBody{
		Name: groupName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create colony %s group: %w", groupSuffix, err)
	}

	log.Info("Created colony group", "groupName", groupName, "groupID", group.Id, "type", groupSuffix)
	return group.Id, nil
}

// ensureColonyNodesGroup ensures that the colony-specific nodes group exists and returns its ID.
func ensureColonyNodesGroup(ctx context.Context, nbClient *netbirdrest.Client, colonyName string) (string, error) {
	return ensureColonyGroup(ctx, nbClient, colonyName, "nodes")
}

// ensureColonyRoutersGroup ensures that the colony-specific routers group exists and returns its ID.
// This group is used specifically for routing peers, not for all nodes.
func ensureColonyRoutersGroup(ctx context.Context, nbClient *netbirdrest.Client, colonyName string) (string, error) {
	return ensureColonyGroup(ctx, nbClient, colonyName, "routers")
}

// ensureColonyMeshPolicy ensures that the colony-specific mesh policy exists.
func ensureColonyMeshPolicy(ctx context.Context, nbClient *netbirdrest.Client, groupID, existingPolicyID, colonyName string) (string, error) {
	log := log.FromContext(ctx)

	policyName := fmt.Sprintf("%s-mesh-policy", colonyName)
	description := fmt.Sprintf("Allow all traffic between %s nodes", colonyName)

	// If we have an existing policy ID, try to update it
	if existingPolicyID != "" {
		_, err := nbClient.Policies.Update(ctx, existingPolicyID, api.PutApiPoliciesPolicyIdJSONRequestBody{
			Name:        policyName,
			Description: stringPtr(description),
			Enabled:     true,
			Rules: []api.PolicyRuleUpdate{
				{
					Name:          "allow-all-mesh",
					Description:   stringPtr(description),
					Enabled:       true,
					Action:        api.PolicyRuleUpdateActionAccept,
					Protocol:      api.PolicyRuleUpdateProtocolAll,
					Sources:       &[]string{groupID},
					Destinations:  &[]string{groupID},
					Bidirectional: true,
				},
			},
		})
		if err == nil {
			log.Info("Updated colony mesh policy", "policyID", existingPolicyID, "name", policyName)
			return existingPolicyID, nil
		}
		log.Info("Failed to update policy, will try to find/create", "policyID", existingPolicyID, "error", err.Error())
	}

	// Try to find existing policy by name
	policies, err := nbClient.Policies.List(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list policies: %w", err)
	}

	for _, policy := range policies {
		if policy.Name == policyName {
			log.Info("Found existing policy by name", "policyID", policy.Id, "name", policyName)
			if policy.Id == nil {
				continue
			}
			// Update it to ensure correct configuration
			_, err := nbClient.Policies.Update(ctx, *policy.Id, api.PutApiPoliciesPolicyIdJSONRequestBody{
				Name:        policyName,
				Description: stringPtr(description),
				Enabled:     true,
				Rules: []api.PolicyRuleUpdate{
					{
						Name:          "allow-all-mesh",
						Description:   stringPtr(description),
						Enabled:       true,
						Action:        api.PolicyRuleUpdateActionAccept,
						Protocol:      api.PolicyRuleUpdateProtocolAll,
						Sources:       &[]string{groupID},
						Destinations:  &[]string{groupID},
						Bidirectional: true,
					},
				},
			})
			if err != nil {
				return "", fmt.Errorf("failed to update existing policy: %w", err)
			}
			return *policy.Id, nil
		}
	}

	// Create new Policy
	log.Info("Creating colony mesh policy", "name", policyName, "groupID", groupID)
	policy, err := nbClient.Policies.Create(ctx, api.PostApiPoliciesJSONRequestBody{
		Name:        policyName,
		Description: stringPtr(description),
		Enabled:     true,
		Rules: []api.PolicyRuleUpdate{
			{
				Name:          "allow-all-mesh",
				Description:   stringPtr(description),
				Enabled:       true,
				Action:        api.PolicyRuleUpdateActionAccept,
				Protocol:      api.PolicyRuleUpdateProtocolAll,
				Sources:       &[]string{groupID},
				Destinations:  &[]string{groupID},
				Bidirectional: true,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create policy: %w", err)
	}

	if policy.Id == nil {
		return "", fmt.Errorf("created policy but got nil ID")
	}

	log.Info("Created colony mesh policy", "policyID", *policy.Id, "name", policyName)
	return *policy.Id, nil
}
