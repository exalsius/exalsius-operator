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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GetNetBirdClientFromSecrets retrieves the NetBird API key from a Secret and creates a NetBird client.
func GetNetBirdClientFromSecrets(ctx context.Context, c client.Client, colony *infrav1.Colony) (*NetBirdClient, error) {
	log := log.FromContext(ctx)

	if colony.Spec.NetBird == nil {
		return nil, fmt.Errorf("NetBird config is nil")
	}

	// Get API key from secret (always in exalsius-system namespace)
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      colony.Spec.NetBird.APIKeySecret,
		Namespace: "exalsius-system",
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key secret %s: %w", colony.Spec.NetBird.APIKeySecret, err)
	}

	apiKeyBytes, ok := secret.Data["apiKey"]
	if !ok {
		// Try alternative key name
		apiKeyBytes, ok = secret.Data["apikey"]
		if !ok {
			return nil, fmt.Errorf("secret %s does not contain 'apiKey' or 'apikey' key", colony.Spec.NetBird.APIKeySecret)
		}
	}

	apiKey := string(apiKeyBytes)
	managementURL := colony.Spec.NetBird.ManagementURL
	if managementURL == "" {
		managementURL = "https://api.netbird.io"
	}

	log.Info("Creating NetBird client", "managementURL", managementURL)
	return NewNetBirdClient(managementURL, apiKey), nil
}

// GetSetupKeyFromSecret retrieves the NetBird setup key from a Secret.
// It uses the auto-generated secret name from status, or constructs the expected name.
func GetSetupKeyFromSecret(ctx context.Context, c client.Client, colony *infrav1.Colony) (string, error) {
	if colony.Spec.NetBird == nil {
		return "", fmt.Errorf("NetBird config is nil")
	}

	// Determine which secret to use
	var secretName string

	// First priority: auto-generated secret from status
	if colony.Status.NetBird != nil && colony.Status.NetBird.SetupKeySecretName != "" {
		secretName = colony.Status.NetBird.SetupKeySecretName
	} else {
		// Fallback: construct expected auto-generated secret name
		secretName = fmt.Sprintf("%s-netbird-setup-key", colony.Name)
	}

	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: colony.Namespace,
	}, secret)
	if err != nil {
		return "", fmt.Errorf("failed to get setup key secret %s: %w", secretName, err)
	}

	setupKeyBytes, ok := secret.Data["setupKey"]
	if !ok {
		// Try alternative key names
		setupKeyBytes, ok = secret.Data["setupkey"]
		if !ok {
			setupKeyBytes, ok = secret.Data["key"]
			if !ok {
				return "", fmt.Errorf("secret %s does not contain 'setupKey', 'setupkey', or 'key'", secretName)
			}
		}
	}

	return string(setupKeyBytes), nil
}
