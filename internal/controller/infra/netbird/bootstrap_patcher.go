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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const (
	// BootstrapSecretType is the type for CAPI bootstrap secrets
	BootstrapSecretType = "cluster.x-k8s.io/secret"

	// PatchedAnnotation marks secrets that have been patched
	PatchedAnnotation = "netbird.exalsius.ai/join-url-patched"

	// ClusterNameLabel is the label for cluster name
	ClusterNameLabel = "cluster.x-k8s.io/cluster-name"
)

// PatchBootstrapSecretsForCluster patches all bootstrap secrets for a specific cluster
// to replace URLs with the appropriate endpoint (LoadBalancer external or internal DNS).
func PatchBootstrapSecretsForCluster(
	ctx context.Context,
	c client.Client,
	colony *infrav1.Colony,
	clusterName string,
) error {
	log := log.FromContext(ctx)

	// Get the cluster status to find the endpoint configuration
	clusterStatus, ok := colony.Status.NetBird.ClusterResources[clusterName]
	if !ok || clusterStatus.ServiceName == "" {
		return fmt.Errorf("cluster %s not found in NetBird status or service name not set", clusterName)
	}

	// Determine the target endpoint based on service type
	var targetEndpoint string
	if clusterStatus.UseDirectConnection && clusterStatus.ExternalAddress != "" {
		// LoadBalancer: use external address directly
		targetEndpoint = clusterStatus.ExposedEndpoint
		log.Info("Using LoadBalancer external endpoint for bootstrap patching",
			"cluster", clusterName,
			"endpoint", targetEndpoint,
			"externalAddress", clusterStatus.ExternalAddress)
	} else {
		// NodePort or LoadBalancer without external address: use internal DNS
		targetEndpoint = getInternalServiceDNS(clusterStatus.ServiceName, clusterStatus.ServiceNamespace)
		log.Info("Using internal service DNS for bootstrap patching",
			"cluster", clusterName,
			"endpoint", targetEndpoint)
	}

	// List all secrets in the colony namespace that match our criteria
	secretList := &corev1.SecretList{}
	fullClusterName := fmt.Sprintf("%s-%s", colony.Name, clusterName)

	listOpts := []client.ListOption{
		client.InNamespace(colony.Namespace),
		client.MatchingLabels{ClusterNameLabel: fullClusterName},
	}

	if err := c.List(ctx, secretList, listOpts...); err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	log.Info("Found secrets for cluster",
		"cluster", clusterName,
		"count", len(secretList.Items),
		"fullClusterName", fullClusterName)

	// Process each secret
	patchedCount := 0
	for i := range secretList.Items {
		secret := &secretList.Items[i]

		// Check if this is a bootstrap secret
		if !isBootstrapSecret(secret, log) {
			continue
		}

		// Check if already patched
		if secret.Annotations != nil && secret.Annotations[PatchedAnnotation] == "true" {
			log.V(1).Info("Secret already patched, skipping", "secret", secret.Name)
			continue
		}

		// Patch the secret
		if err := patchBootstrapSecret(ctx, c, secret, targetEndpoint, colony); err != nil {
			log.Error(err, "Failed to patch bootstrap secret", "secret", secret.Name)
			// Continue with other secrets
			continue
		}

		patchedCount++
		log.Info("Successfully patched bootstrap secret",
			"secret", secret.Name,
			"targetEndpoint", targetEndpoint)
	}

	log.Info("Completed bootstrap secret patching",
		"cluster", clusterName,
		"patchedCount", patchedCount)

	return nil
}

// isBootstrapSecret checks if a secret is a CAPI bootstrap secret
// by validating it contains cloud-init data in the format we can patch
func isBootstrapSecret(secret *corev1.Secret, log logr.Logger) bool {
	// Check type
	if secret.Type != BootstrapSecretType {
		return false
	}

	// Check for required data keys
	if secret.Data == nil {
		return false
	}

	// Must have 'value' key
	valueData, hasValue := secret.Data["value"]
	if !hasValue {
		return false
	}

	// Try to parse as cloud-init YAML
	var cloudConfig map[string]interface{}
	if err := yaml.Unmarshal(valueData, &cloudConfig); err != nil {
		// Not valid cloud-init YAML, not a bootstrap secret we can patch
		log.V(1).Info("Secret is not valid cloud-init YAML, skipping",
			"secret", secret.Name,
			"error", err.Error())
		return false
	}

	// k0s bootstrap secrets have a write_files entry with /etc/k0s.token
	writeFiles, ok := cloudConfig["write_files"].([]interface{})
	if !ok {
		log.V(1).Info("Secret has no write_files section, skipping",
			"secret", secret.Name)
		return false
	}

	// Look for the k0s.token file which is characteristic of k0s bootstrap
	for _, fileInterface := range writeFiles {
		file, ok := fileInterface.(map[string]interface{})
		if !ok {
			continue
		}

		if path, _ := file["path"].(string); path == "/etc/k0s.token" {
			// This is definitely a k0s bootstrap secret
			log.V(1).Info("Identified k0s bootstrap secret",
				"secret", secret.Name)
			return true
		}
	}

	log.V(1).Info("Secret has write_files but no k0s.token, skipping",
		"secret", secret.Name)
	return false
}

// patchBootstrapSecret patches a single bootstrap secret
func patchBootstrapSecret(
	ctx context.Context,
	c client.Client,
	secret *corev1.Secret,
	targetEndpoint string,
	colony *infrav1.Colony,
) error {
	log := log.FromContext(ctx)

	// Get the bootstrap data
	valueData, ok := secret.Data["value"]
	if !ok {
		return fmt.Errorf("secret missing 'value' key")
	}

	cloudInitData := string(valueData)

	// Get setup key if NetBird is enabled
	var setupKey string
	if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
		key, err := getSetupKey(ctx, c, colony)
		if err != nil {
			log.Error(err, "Failed to get NetBird setup key, skipping NetBird injection")
		} else {
			setupKey = key
		}
	}

	// Modify cloud-init (both URL replacement and NetBird injection)
	replaced, err := modifyCloudInit(cloudInitData, targetEndpoint, setupKey, colony, log)
	if err != nil {
		return fmt.Errorf("failed to modify cloud-init: %w", err)
	}

	if replaced == cloudInitData {
		log.Info("No modifications needed for secret", "secret", secret.Name)
	}

	// Store the raw cloud-config (Kubernetes will base64-encode it automatically)
	secret.Data["value"] = []byte(replaced)

	// Add annotation to mark as patched
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[PatchedAnnotation] = "true"

	// Update the secret
	if err := c.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	return nil
}

// modifyCloudInit modifies cloud-init data to:
// 1. Replace service URLs with target endpoint (LoadBalancer external or internal DNS)
// 2. Inject NetBird installation commands (if enabled)
func modifyCloudInit(cloudInitData, targetEndpoint, setupKey string, colony *infrav1.Colony, log logr.Logger) (string, error) {
	// Parse cloud-config YAML
	var cloudConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(cloudInitData), &cloudConfig); err != nil {
		return "", fmt.Errorf("failed to parse cloud-config: %w", err)
	}

	modified := false

	// === PART 1: Inject NetBird commands (if enabled and setup key provided) ===
	if setupKey != "" && colony != nil && colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
		if injected := injectNetBirdCommands(cloudConfig, setupKey, log); injected {
			modified = true
		}
	}

	// === PART 2: Replace join URL in token ===
	// Extract write_files to find old token
	var oldTokenContent string
	writeFiles, ok := cloudConfig["write_files"].([]interface{})
	if ok && len(writeFiles) > 0 {
		for _, fileInterface := range writeFiles {
			file, ok := fileInterface.(map[string]interface{})
			if !ok {
				continue
			}

			path, _ := file["path"].(string)
			if path != "/etc/k0s.token" {
				continue
			}

			oldTokenContent, _ = file["content"].(string)
			if oldTokenContent != "" {
				break
			}
		}
	}

	// If we have a token to replace
	if oldTokenContent != "" {
		// Decode, modify, re-encode token
		newTokenContent, err := modifyToken(oldTokenContent, targetEndpoint, log)
		if err != nil {
			log.Error(err, "Failed to modify token")
		} else if newTokenContent != oldTokenContent {
			// Update in the parsed structure
			for _, fileInterface := range writeFiles {
				file := fileInterface.(map[string]interface{})
				if path, _ := file["path"].(string); path == "/etc/k0s.token" {
					file["content"] = newTokenContent
					modified = true
					log.Info("Replaced k0s.token server URL", "targetEndpoint", targetEndpoint)
					break
				}
			}
		}
	}

	if !modified {
		return cloudInitData, nil
	}

	modifiedYAML, err := yaml.Marshal(cloudConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cloud-config: %w", err)
	}

	log.Info("Successfully modified cloud-init",
		"hasNetBird", setupKey != "",
		"hasURLReplacement", oldTokenContent != "")

	return string(modifiedYAML), nil
}

// gunzipData decompresses gzipped data
func gunzipData(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// gzipData compresses data with gzip (best compression like k0smotron)
func gzipData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}

	if _, err := writer.Write(data); err != nil {
		return nil, err
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// replaceServerURL replaces the server URL in kubeconfig clusters section
func replaceServerURL(kubeconfig map[string]interface{}, newServerURL string, log logr.Logger) bool {
	clusters, ok := kubeconfig["clusters"].([]interface{})
	if !ok || len(clusters) == 0 {
		return false
	}

	modified := false
	for _, clusterInterface := range clusters {
		clusterEntry, ok := clusterInterface.(map[string]interface{})
		if !ok {
			continue
		}

		cluster, ok := clusterEntry["cluster"].(map[string]interface{})
		if !ok {
			continue
		}

		if server, ok := cluster["server"].(string); ok {
			// Only replace if it contains our target ports
			if strings.Contains(server, ":30443") || strings.Contains(server, ":30132") {
				oldServer := server
				cluster["server"] = "https://" + newServerURL
				modified = true
				log.Info("Replaced server URL in kubeconfig",
					"old", oldServer,
					"new", cluster["server"])
			}
		}
	}

	return modified
}

// getInternalServiceDNS constructs the internal Kubernetes DNS name for a service with port
func getInternalServiceDNS(serviceName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:30443", serviceName, namespace)
}

// getSetupKey fetches the NetBird setup key from the colony's secret
func getSetupKey(ctx context.Context, c client.Client, colony *infrav1.Colony) (string, error) {
	secretName := colony.Name + "-netbird-setup-key"
	secret := &corev1.Secret{}

	if err := c.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: colony.Namespace,
	}, secret); err != nil {
		return "", fmt.Errorf("failed to get setup key secret: %w", err)
	}

	setupKeyBytes, ok := secret.Data["setupKey"]
	if !ok {
		return "", fmt.Errorf("setupKey not found in secret %s", secretName)
	}

	return string(setupKeyBytes), nil
}

// injectNetBirdCommands injects NetBird installation into the runcmd section
func injectNetBirdCommands(cloudConfig map[string]interface{}, setupKey string, log logr.Logger) bool {
	// Get or create runcmd array
	var runcmd []interface{}
	if existing, ok := cloudConfig["runcmd"].([]interface{}); ok {
		runcmd = existing
	} else {
		runcmd = []interface{}{}
	}

	// Check if NetBird commands already present (idempotency)
	hasNetBird := false
	for _, cmd := range runcmd {
		if cmdStr, ok := cmd.(string); ok {
			if strings.Contains(cmdStr, "netbird up") {
				hasNetBird = true
				break
			}
		}
	}

	if hasNetBird {
		log.V(1).Info("NetBird commands already present in runcmd")
		return false
	}

	// Prepend NetBird commands
	netbirdCommands := []interface{}{
		"curl -fsSL https://pkgs.netbird.io/install.sh | sh",
		fmt.Sprintf("netbird up --setup-key %s", setupKey),
		"timeout 30 bash -c 'until netbird status | grep -q Connected; do sleep 1; done'",
	}

	// Prepend to existing commands
	runcmd = append(netbirdCommands, runcmd...)

	// Now find the systemctl start k0sworker command and insert systemd modifications before it
	systemctlIndex := -1
	for i, cmd := range runcmd {
		if cmdStr, ok := cmd.(string); ok {
			if strings.Contains(cmdStr, "systemctl start k0sworker") {
				systemctlIndex = i
				break
			}
		}
	}

	// If we found the systemctl command, inject systemd overwrite commands before it
	if systemctlIndex != -1 {
		systemdOverwriteCommands := createSystemdOverwriteCommands(log)

		// Insert systemd overwrite commands before systemctl start
		// Split runcmd into before and after
		before := runcmd[:systemctlIndex]
		after := runcmd[systemctlIndex:]

		// Concatenate: before + systemd overwrite + after
		// Use explicit capacity to avoid slice aliasing issues
		newRuncmd := make([]interface{}, 0, len(before)+len(systemdOverwriteCommands)+len(after))
		newRuncmd = append(newRuncmd, before...)
		newRuncmd = append(newRuncmd, systemdOverwriteCommands...)
		newRuncmd = append(newRuncmd, after...)
		runcmd = newRuncmd

		log.Info("Injected systemd overwrite commands before systemctl start",
			"systemdCommandCount", len(systemdOverwriteCommands))
	} else {
		log.V(1).Info("systemctl start k0sworker command not found, skipping systemd modifications")
	}

	cloudConfig["runcmd"] = runcmd

	log.Info("Injected NetBird commands into runcmd", "totalCommands", len(runcmd))
	return true
}

// createSystemdOverwriteCommands creates commands to set up a wrapper script and systemd service
func createSystemdOverwriteCommands(log logr.Logger) []interface{} {
	// Command 1: Create the wrapper script that extracts args and injects NetBird IP
	createWrapperScript := `/bin/bash -c '
# Extract k0s worker arguments from the existing service file
ARGS=$(grep "^ExecStart=" /etc/systemd/system/k0sworker.service | sed "s|.*/k0s worker ||")

# Add --node-ip to kubelet-extra-args if it exists, otherwise append it
if echo "$ARGS" | grep -q "kubelet-extra-args"; then
  ARGS=$(echo "$ARGS" | sed "s|--kubelet-extra-args=|--kubelet-extra-args=--node-ip=\${NODE_IP},|")
else
  ARGS="$ARGS --kubelet-extra-args=--node-ip=\${NODE_IP}"
fi

# Create the wrapper script
cat > /usr/local/bin/k0s-worker-wrapper.sh <<EOF
#!/usr/bin/env bash

set -euo pipefail

# Log to journal for debugging
echo "[k0sworker] querying netbird IP" | systemd-cat -t k0sworker

# Get NetBird IP
NODE_IP="\$(netbird status --ipv4)"
if [[ -z "\${NODE_IP}" ]]; then
  echo "[k0sworker] ERROR: netbird returned empty IP" | systemd-cat -t k0sworker
  exit 1
fi

echo "[k0sworker] using NODE_IP=\${NODE_IP}" | systemd-cat -t k0sworker

# Execute k0s with extracted args
exec /usr/local/bin/k0s worker $ARGS
EOF

# Make the wrapper script executable
chmod +x /usr/local/bin/k0s-worker-wrapper.sh
'`

	// Command 2: Overwrite the systemd service file to use the wrapper
	overwriteSystemdService := `/bin/bash -c '
cat > /etc/systemd/system/k0sworker.service <<EOF
[Unit]
Description=k0s - Zero Friction Kubernetes
Documentation=https://docs.k0sproject.io
ConditionFileIsExecutable=/usr/local/bin/k0s-worker-wrapper.sh
After=network-online.target
Wants=network-online.target

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart=/usr/local/bin/k0s-worker-wrapper.sh
RestartSec=10
Delegate=yes
KillMode=process
LimitCORE=infinity
TasksMax=infinity
TimeoutStartSec=0
LimitNOFILE=999999
Restart=always

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
'`

	commands := []interface{}{createWrapperScript, overwriteSystemdService}

	log.V(1).Info("Created systemd overwrite commands", "commandCount", len(commands))
	return commands
}

// modifyToken decodes a k0s token, replaces the server URL, and re-encodes it
func modifyToken(tokenContent, targetEndpoint string, log logr.Logger) (string, error) {
	// Decode token: base64 → gunzip → kubeconfig YAML
	decodedToken, err := base64.StdEncoding.DecodeString(tokenContent)
	if err != nil {
		return tokenContent, err
	}

	kubeconfigBytes, err := gunzipData(decodedToken)
	if err != nil {
		return tokenContent, err
	}

	// Parse kubeconfig
	var kubeconfig map[string]interface{}
	if err := yaml.Unmarshal(kubeconfigBytes, &kubeconfig); err != nil {
		return tokenContent, err
	}

	// Replace server URL
	if !replaceServerURL(kubeconfig, targetEndpoint, log) {
		return tokenContent, nil // No replacement needed
	}

	// Re-encode: YAML → gzip → base64
	modifiedKubeconfig, err := yaml.Marshal(kubeconfig)
	if err != nil {
		return tokenContent, err
	}

	gzipped, err := gzipData(modifiedKubeconfig)
	if err != nil {
		return tokenContent, err
	}

	newTokenContent := base64.StdEncoding.EncodeToString(gzipped)
	return newTokenContent, nil
}

// WatchAndPatchBootstrapSecrets watches for new bootstrap secrets and patches them
// This can be called from the Colony reconciler after control plane exposure
func WatchAndPatchBootstrapSecrets(
	ctx context.Context,
	c client.Client,
	colony *infrav1.Colony,
) error {
	log := log.FromContext(ctx)

	if colony.Status.NetBird == nil || !colony.Spec.NetBird.Enabled {
		return nil
	}

	// Patch secrets for each cluster
	for clusterName := range colony.Status.NetBird.ClusterResources {
		if err := PatchBootstrapSecretsForCluster(ctx, c, colony, clusterName); err != nil {
			// Check if it's a "not ready" error
			if errors.IsNotFound(err) || strings.Contains(err.Error(), "not found") {
				log.Info("Cluster not ready for bootstrap secret patching",
					"cluster", clusterName,
					"error", err.Error())
				continue
			}
			log.Error(err, "Failed to patch bootstrap secrets for cluster", "cluster", clusterName)
			// Continue with other clusters
		}
	}

	return nil
}

// SecretToColonyMapper maps a Secret to Colony reconcile requests
// This is used in the controller's Watch configuration
func SecretToColonyMapper(c client.Client) func(context.Context, client.Object) []ctrl.Request {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		log := log.FromContext(ctx)
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return nil
		}

		// Only handle bootstrap secrets
		if !isBootstrapSecret(secret, log) {
			return nil
		}

		// Check if already patched
		if secret.Annotations != nil && secret.Annotations[PatchedAnnotation] == "true" {
			return nil
		}

		// Get cluster name from label
		clusterName, ok := secret.Labels[ClusterNameLabel]
		if !ok {
			return nil
		}

		// Query the ClusterDeployment to find the owning Colony
		// ClusterDeployment has the same name as the cluster label
		clusterDeployment := &k0rdentv1beta1.ClusterDeployment{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      clusterName,
			Namespace: secret.Namespace,
		}, clusterDeployment); err != nil {
			if errors.IsNotFound(err) {
				log.V(1).Info("ClusterDeployment not found for secret, might not be created yet",
					"clusterName", clusterName,
					"secret", secret.Name)
			} else {
				log.Error(err, "Failed to get ClusterDeployment for secret",
					"clusterName", clusterName,
					"secret", secret.Name)
			}
			return nil
		}

		// Extract the Colony owner from the ClusterDeployment's owner references
		colonyName, err := getColonyOwner(clusterDeployment)
		if err != nil {
			log.Error(err, "Failed to extract Colony owner from ClusterDeployment",
				"clusterDeployment", clusterDeployment.Name,
				"secret", secret.Name)
			return nil
		}

		log.V(1).Info("Mapped secret to Colony via ClusterDeployment owner reference",
			"secret", secret.Name,
			"clusterDeployment", clusterDeployment.Name,
			"colony", colonyName)

		return []ctrl.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      colonyName,
					Namespace: secret.Namespace,
				},
			},
		}
	}
}

// getColonyOwner extracts the Colony owner name from a ClusterDeployment's owner references
func getColonyOwner(clusterDeployment *k0rdentv1beta1.ClusterDeployment) (string, error) {
	for _, ownerRef := range clusterDeployment.OwnerReferences {
		// Look for the controller owner reference that is a Colony
		if ownerRef.Controller != nil && *ownerRef.Controller &&
			ownerRef.Kind == "Colony" &&
			strings.HasPrefix(ownerRef.APIVersion, "infra.exalsius.ai/") {
			return ownerRef.Name, nil
		}
	}
	return "", fmt.Errorf("no Colony controller owner found in ClusterDeployment owner references")
}
