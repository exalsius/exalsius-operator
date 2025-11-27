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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// generateSetupKey creates a new NetBird setup key via the API.
// Returns the setup key value and ID.
func generateSetupKey(ctx context.Context, nbClient *NetBirdClient, colonyName, groupID string) (keyValue, keyID string, err error) {
	log := log.FromContext(ctx)

	// Create setup key with auto-assignment to colony group
	ephemeral := true
	setupKeyReq := SetupKeyRequest{
		AutoGroups: []string{groupID},
		Ephemeral:  &ephemeral,
		Name:       colonyName,
		Type:       "reusable",
		ExpiresIn:  0, // No expiration
		UsageLimit: 0, // Unlimited usage
	}

	log.Info("Creating NetBird setup key", "colonyName", colonyName, "groupID", groupID)

	setupKey, err := nbClient.CreateSetupKey(ctx, setupKeyReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to create setup key: %w", err)
	}

	log.Info("Successfully created NetBird setup key",
		"colonyName", colonyName,
		"setupKeyID", setupKey.ID,
		"setupKeyName", setupKey.Name)

	return setupKey.Key, setupKey.ID, nil
}

// ensureSetupKeySecret creates or updates the Secret containing the setup key.
// The Secret will have an owner reference to the Colony for automatic cleanup.
func ensureSetupKeySecret(ctx context.Context, c client.Client, colony *infrav1.Colony, setupKeyValue string) error {
	log := log.FromContext(ctx)

	secretName := fmt.Sprintf("%s-netbird-setup-key", colony.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: colony.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         infrav1.GroupVersion.String(),
					Kind:               "Colony",
					Name:               colony.Name,
					UID:                colony.UID,
					BlockOwnerDeletion: boolPtr(true),
					Controller:         boolPtr(true),
				},
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"setupKey": setupKeyValue,
		},
	}

	// Try to get existing secret
	existingSecret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: colony.Namespace}, existingSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			log.Info("Creating setup key Secret", "secret", secretName, "namespace", colony.Namespace)
			if err := c.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create setup key secret: %w", err)
			}
			log.Info("Successfully created setup key Secret", "secret", secretName)
			return nil
		}
		return fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Update existing secret
	log.Info("Updating existing setup key Secret", "secret", secretName, "namespace", colony.Namespace)
	existingSecret.StringData = secret.StringData
	if err := c.Update(ctx, existingSecret); err != nil {
		return fmt.Errorf("failed to update setup key secret: %w", err)
	}

	log.Info("Successfully updated setup key Secret", "secret", secretName)
	return nil
}

// findSetupKeyIDByName looks up a setup key ID by its name from NetBird API.
// Returns the setup key ID if found, or empty string if not found.
func findSetupKeyIDByName(ctx context.Context, nbClient *NetBirdClient, colonyName string) (string, error) {
	log := log.FromContext(ctx)

	// List all setup keys
	setupKeys, err := nbClient.ListSetupKeys(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list setup keys: %w", err)
	}

	// Find the setup key with matching name
	for _, key := range setupKeys {
		if key.Name == colonyName {
			log.Info("Found existing setup key by name",
				"colonyName", colonyName,
				"setupKeyID", key.ID)
			return key.ID, nil
		}
	}

	return "", fmt.Errorf("setup key with name %s not found", colonyName)
}

// setupKeySecretExists checks if the setup key secret already exists.
func setupKeySecretExists(ctx context.Context, c client.Client, colony *infrav1.Colony) bool {
	secretName := fmt.Sprintf("%s-netbird-setup-key", colony.Name)
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: colony.Namespace,
	}, secret)
	return err == nil
}

// generateRouterSetupKey creates a new NetBird setup key specifically for routing peers.
// This key automatically assigns peers to BOTH the nodes group (for mesh connectivity)
// and the routers group (for network router configuration).
// Returns the setup key value and ID.
func generateRouterSetupKey(ctx context.Context, nbClient *NetBirdClient, colonyName, nodesGroupID, routersGroupID string) (keyValue, keyID string, err error) {
	log := log.FromContext(ctx)

	// Create setup key with auto-assignment to BOTH groups
	ephemeral := true
	setupKeyReq := SetupKeyRequest{
		AutoGroups: []string{nodesGroupID, routersGroupID}, // Assign to BOTH groups
		Ephemeral:  &ephemeral,
		Name:       fmt.Sprintf("%s-router", colonyName),
		Type:       "reusable",
		ExpiresIn:  0, // No expiration
		UsageLimit: 0, // Unlimited usage
	}

	log.Info("Creating NetBird router setup key", "colonyName", colonyName, "nodesGroupID", nodesGroupID, "routersGroupID", routersGroupID)

	setupKey, err := nbClient.CreateSetupKey(ctx, setupKeyReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to create router setup key: %w", err)
	}

	log.Info("Successfully created NetBird router setup key",
		"colonyName", colonyName,
		"setupKeyID", setupKey.ID,
		"setupKeyName", setupKey.Name)

	return setupKey.Key, setupKey.ID, nil
}

// ensureRouterSetupKeySecret creates or updates the Secret containing the router setup key.
// The Secret will have an owner reference to the Colony for automatic cleanup.
func ensureRouterSetupKeySecret(ctx context.Context, c client.Client, colony *infrav1.Colony, setupKeyValue string) error {
	log := log.FromContext(ctx)

	secretName := fmt.Sprintf("%s-netbird-router-setup-key", colony.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: colony.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         infrav1.GroupVersion.String(),
					Kind:               "Colony",
					Name:               colony.Name,
					UID:                colony.UID,
					BlockOwnerDeletion: boolPtr(true),
					Controller:         boolPtr(true),
				},
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"setupKey": setupKeyValue,
		},
	}

	// Try to get existing secret
	existingSecret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: colony.Namespace}, existingSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new secret
			log.Info("Creating router setup key Secret", "secret", secretName, "namespace", colony.Namespace)
			if err := c.Create(ctx, secret); err != nil {
				return fmt.Errorf("failed to create router setup key secret: %w", err)
			}
			log.Info("Successfully created router setup key Secret", "secret", secretName)
			return nil
		}
		return fmt.Errorf("failed to get existing router secret: %w", err)
	}

	// Update existing secret
	log.Info("Updating existing router setup key Secret", "secret", secretName, "namespace", colony.Namespace)
	existingSecret.StringData = secret.StringData
	if err := c.Update(ctx, existingSecret); err != nil {
		return fmt.Errorf("failed to update router setup key secret: %w", err)
	}

	log.Info("Successfully updated router setup key Secret", "secret", secretName)
	return nil
}
