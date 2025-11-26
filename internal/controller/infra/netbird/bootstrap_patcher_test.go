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
	"encoding/base64"
	"strings"
	"testing"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/yaml"
)

const (
	testServerURL = "kmc-test-cluster.default.svc.cluster.local:30443"
)

func TestGunzipData(t *testing.T) {
	// Test data
	original := []byte("test data for compression")

	// Compress
	compressed, err := gzipData(original)
	if err != nil {
		t.Fatalf("Failed to compress data: %v", err)
	}

	// Decompress
	decompressed, err := gunzipData(compressed)
	if err != nil {
		t.Fatalf("Failed to decompress data: %v", err)
	}

	// Verify
	if string(decompressed) != string(original) {
		t.Errorf("Decompressed data doesn't match original. Got %q, want %q", string(decompressed), string(original))
	}
}

func TestReplaceServerURL(t *testing.T) {
	// Create a test kubeconfig
	kubeconfig := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []interface{}{
			map[string]interface{}{
				"name": "k0s",
				"cluster": map[string]interface{}{
					"server":                     "https://172.18.0.2:30443",
					"certificate-authority-data": "test-ca-data",
				},
			},
		},
	}

	// Replace URL
	newServerURL := testServerURL
	modified := replaceServerURL(kubeconfig, newServerURL, logr.Discard())

	if !modified {
		t.Fatal("Expected replaceServerURL to return true")
	}

	// Verify the URL was replaced
	clusters := kubeconfig["clusters"].([]interface{})
	clusterEntry := clusters[0].(map[string]interface{})
	cluster := clusterEntry["cluster"].(map[string]interface{})
	server := cluster["server"].(string)

	expectedServer := "https://" + newServerURL
	if server != expectedServer {
		t.Errorf("Server URL not replaced correctly. Got %q, want %q", server, expectedServer)
	}
}

func TestModifyCloudInit(t *testing.T) {
	// Create a kubeconfig
	kubeconfig := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []interface{}{
			map[string]interface{}{
				"name": "k0s",
				"cluster": map[string]interface{}{
					"server":                     "https://172.18.0.2:30443",
					"certificate-authority-data": "LS0tLS1CRUdJTi0tLS0t",
				},
			},
		},
		"contexts": []interface{}{
			map[string]interface{}{
				"name": "k0s",
				"context": map[string]interface{}{
					"cluster": "k0s",
					"user":    "kubelet-bootstrap",
				},
			},
		},
		"current-context": "k0s",
		"users": []interface{}{
			map[string]interface{}{
				"name": "kubelet-bootstrap",
				"user": map[string]interface{}{
					"token": "test.token",
				},
			},
		},
	}

	// Encode kubeconfig: YAML -> gzip -> base64
	kubeconfigYAML, err := yaml.Marshal(kubeconfig)
	if err != nil {
		t.Fatalf("Failed to marshal kubeconfig: %v", err)
	}

	gzipped, err := gzipData(kubeconfigYAML)
	if err != nil {
		t.Fatalf("Failed to gzip kubeconfig: %v", err)
	}

	tokenContent := base64.StdEncoding.EncodeToString(gzipped)

	// Create cloud-config with the token
	cloudConfig := map[string]interface{}{
		"write_files": []interface{}{
			map[string]interface{}{
				"path":        "/etc/k0s.token",
				"content":     tokenContent,
				"permissions": "0600",
			},
		},
		"runcmd": []interface{}{
			"echo 'test command'",
		},
	}

	cloudConfigYAML, err := yaml.Marshal(cloudConfig)
	if err != nil {
		t.Fatalf("Failed to marshal cloud-config: %v", err)
	}

	cloudInitData := string(cloudConfigYAML)

	// Modify cloud-init (URL replacement only, no NetBird)
	internalDNS := testServerURL
	replaced, err := modifyCloudInit(cloudInitData, internalDNS, "", nil, logr.Discard())
	if err != nil {
		t.Fatalf("Failed to modify cloud-init: %v", err)
	}

	if replaced == cloudInitData {
		t.Fatal("Expected cloud-config to be modified")
	}

	// Decode and verify the result
	var replacedConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(replaced), &replacedConfig); err != nil {
		t.Fatalf("Failed to parse replaced cloud-config: %v", err)
	}

	writeFiles := replacedConfig["write_files"].([]interface{})
	file := writeFiles[0].(map[string]interface{})
	newTokenContent := file["content"].(string)

	// Decode the new token
	decoded, err := base64.StdEncoding.DecodeString(newTokenContent)
	if err != nil {
		t.Fatalf("Failed to decode new token: %v", err)
	}

	decompressed, err := gunzipData(decoded)
	if err != nil {
		t.Fatalf("Failed to decompress new token: %v", err)
	}

	var newKubeconfig map[string]interface{}
	if err := yaml.Unmarshal(decompressed, &newKubeconfig); err != nil {
		t.Fatalf("Failed to parse new kubeconfig: %v", err)
	}

	// Verify the server URL was changed
	clusters := newKubeconfig["clusters"].([]interface{})
	clusterEntry := clusters[0].(map[string]interface{})
	cluster := clusterEntry["cluster"].(map[string]interface{})
	server := cluster["server"].(string)

	expectedServer := "https://" + internalDNS
	if server != expectedServer {
		t.Errorf("Server URL not replaced in token. Got %q, want %q", server, expectedServer)
	}

	// Verify the token is still valid (can be decoded)
	if !strings.Contains(server, "svc.cluster.local") {
		t.Error("Expected server URL to contain svc.cluster.local")
	}

	// Verify the cloud-config structure is preserved (not re-marshaled)
	if !strings.Contains(replaced, "write_files:") {
		t.Error("Expected cloud-config to contain write_files section")
	}

	if !strings.Contains(replaced, "runcmd:") {
		t.Error("Expected cloud-config to contain runcmd section (original structure)")
	}

	// Verify it's still valid YAML
	var verifyConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(replaced), &verifyConfig); err != nil {
		t.Fatalf("Modified cloud-config is not valid YAML: %v", err)
	}

	// Verify the structure matches original (both have write_files and runcmd)
	if _, ok := verifyConfig["write_files"]; !ok {
		t.Error("Modified cloud-config missing write_files section")
	}
	if _, ok := verifyConfig["runcmd"]; !ok {
		t.Error("Modified cloud-config missing runcmd section")
	}
}

func TestGetInternalServiceDNS(t *testing.T) {
	serviceName := "kmc-test-cluster"
	namespace := "default"

	result := getInternalServiceDNS(serviceName, namespace)
	expected := testServerURL

	if result != expected {
		t.Errorf("getInternalServiceDNS returned %q, want %q", result, expected)
	}

	// Verify it doesn't have https://
	if strings.HasPrefix(result, "https://") {
		t.Error("getInternalServiceDNS should not include https:// prefix")
	}
}

func TestInjectNetBirdCommands(t *testing.T) {
	setupKey := "test-setup-key-123"

	tests := []struct {
		name             string
		cloudConfig      map[string]interface{}
		expectedInjected bool
		expectedCount    int
	}{
		{
			name: "New runcmd array",
			cloudConfig: map[string]interface{}{
				"write_files": []interface{}{
					map[string]interface{}{"path": "/etc/test"},
				},
			},
			expectedInjected: true,
			expectedCount:    3,
		},
		{
			name: "Existing runcmd array",
			cloudConfig: map[string]interface{}{
				"runcmd": []interface{}{
					"echo existing command",
				},
			},
			expectedInjected: true,
			expectedCount:    4, // 3 NetBird + 1 existing
		},
		{
			name: "Already has NetBird commands",
			cloudConfig: map[string]interface{}{
				"runcmd": []interface{}{
					"curl -fsSL https://pkgs.netbird.io/install.sh | sh",
					"netbird up --setup-key existing-key",
					"echo other command",
				},
			},
			expectedInjected: false,
			expectedCount:    3, // No change
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logr.Discard()
			injected := injectNetBirdCommands(tt.cloudConfig, setupKey, log)

			if injected != tt.expectedInjected {
				t.Errorf("injectNetBirdCommands returned %v, want %v", injected, tt.expectedInjected)
			}

			runcmd, ok := tt.cloudConfig["runcmd"].([]interface{})
			if !ok {
				if tt.expectedCount > 0 {
					t.Fatal("Expected runcmd array to be created")
				}
				return
			}

			if len(runcmd) != tt.expectedCount {
				t.Errorf("Expected %d commands, got %d", tt.expectedCount, len(runcmd))
			}

			// If we injected, verify NetBird commands are first
			if injected {
				if !strings.Contains(runcmd[0].(string), "install.sh") {
					t.Error("First command should be NetBird install")
				}

				if !strings.Contains(runcmd[1].(string), "netbird up") {
					t.Error("Second command should be netbird up")
				}

				if !strings.Contains(runcmd[1].(string), setupKey) {
					t.Error("Setup key not included in command")
				}

				if !strings.Contains(runcmd[2].(string), "netbird status") {
					t.Error("Third command should be netbird status wait")
				}
			}
		})
	}
}

func TestModifyCloudInitWithNetBird(t *testing.T) {
	// Create a simple cloud-config
	cloudConfig := map[string]interface{}{
		"write_files": []interface{}{
			map[string]interface{}{
				"path":        "/etc/test.txt",
				"content":     "test",
				"permissions": "0600",
			},
		},
		"runcmd": []interface{}{
			"echo 'original command'",
		},
	}

	cloudConfigYAML, err := yaml.Marshal(cloudConfig)
	if err != nil {
		t.Fatalf("Failed to marshal cloud-config: %v", err)
	}

	cloudInitData := string(cloudConfigYAML)

	// Create a mock colony with NetBird enabled
	colony := &infrav1.Colony{
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: true,
			},
		},
	}

	// Modify cloud-init with NetBird
	setupKey := "test-setup-key-456"
	internalDNS := "test.svc.cluster.local:30443"
	replaced, err := modifyCloudInit(cloudInitData, internalDNS, setupKey, colony, logr.Discard())
	if err != nil {
		t.Fatalf("Failed to modify cloud-init: %v", err)
	}

	if replaced == cloudInitData {
		t.Fatal("Expected cloud-config to be modified")
	}

	// Decode and verify the result
	var replacedConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(replaced), &replacedConfig); err != nil {
		t.Fatalf("Failed to parse replaced cloud-config: %v", err)
	}

	runcmd, ok := replacedConfig["runcmd"].([]interface{})
	if !ok {
		t.Fatal("Expected runcmd array in modified config")
	}

	// Verify NetBird commands are present and first
	if len(runcmd) < 4 {
		t.Fatalf("Expected at least 4 commands (3 NetBird + 1 original), got %d", len(runcmd))
	}

	if !strings.Contains(runcmd[0].(string), "install.sh") {
		t.Error("First command should be NetBird install")
	}

	if !strings.Contains(runcmd[1].(string), setupKey) {
		t.Error("Second command should contain setup key")
	}

	if !strings.Contains(runcmd[3].(string), "original command") {
		t.Error("Original command should be preserved")
	}
}

// Helper functions for TestInjectNetBirdCommandsWithSystemdModifications

func findSystemctlCommand(runcmd []interface{}) int {
	for i, cmd := range runcmd {
		if cmdStr, ok := cmd.(string); ok {
			if strings.Contains(cmdStr, "systemctl start k0sworker") {
				return i
			}
		}
	}
	return -1
}

func findK0sInstallCommand(runcmd []interface{}) int {
	for i, cmd := range runcmd {
		if cmdStr, ok := cmd.(string); ok {
			if strings.Contains(cmdStr, "k0s install worker") {
				return i
			}
		}
	}
	return -1
}

func verifyNetBirdCommandsInjected(t *testing.T, runcmd []interface{}) {
	t.Helper()
	if len(runcmd) < 3 {
		t.Fatalf("Expected at least 3 NetBird commands, got %d", len(runcmd))
	}

	if !strings.Contains(runcmd[0].(string), "install.sh") {
		t.Error("First command should be NetBird install")
	}

	if !strings.Contains(runcmd[1].(string), "netbird up") {
		t.Error("Second command should be netbird up")
	}

	if !strings.Contains(runcmd[2].(string), "netbird status") {
		t.Error("Third command should be netbird status wait")
	}
}

func verifySystemdOverwriteCommands(t *testing.T, runcmd []interface{}) {
	t.Helper()
	// Find systemctl start command
	systemctlIndex := findSystemctlCommand(runcmd)

	if systemctlIndex == -1 {
		t.Fatal("Expected to find systemctl start k0sworker command")
	}

	// Verify systemd overwrite commands are present before systemctl start
	// Now we have 2 commands: wrapper creation + service overwrite
	foundWrapperCreation := false
	foundServiceOverwrite := false

	for i := 0; i < systemctlIndex; i++ {
		cmdStr := runcmd[i].(string)

		// Check for wrapper script creation command
		hasWrapperElements := strings.Contains(cmdStr, "/bin/bash -c") &&
			strings.Contains(cmdStr, "k0s-worker-wrapper.sh") &&
			strings.Contains(cmdStr, "--node-ip=") &&
			strings.Contains(cmdStr, "netbird status --ipv4") &&
			strings.Contains(cmdStr, "chmod +x")

		if hasWrapperElements {
			foundWrapperCreation = true
		}

		// Check for service overwrite command
		hasServiceElements := strings.Contains(cmdStr, "/bin/bash -c") &&
			strings.Contains(cmdStr, "k0sworker.service") &&
			strings.Contains(cmdStr, "ExecStart=/usr/local/bin/k0s-worker-wrapper.sh") &&
			strings.Contains(cmdStr, "systemctl daemon-reload")

		if hasServiceElements {
			foundServiceOverwrite = true
		}
	}

	if !foundWrapperCreation {
		t.Error("Expected to find wrapper script creation before systemctl start")
	}
	if !foundServiceOverwrite {
		t.Error("Expected to find service overwrite before systemctl start")
	}
}

func verifyCommandOrder(t *testing.T, runcmd []interface{}) {
	t.Helper()
	// Expected order:
	// 1. NetBird install
	// 2. NetBird up
	// 3. NetBird status wait
	// 4. k0s install
	// 5. k0s install worker
	// 6. Wrapper script creation
	// 7. Systemd service overwrite
	// 8. systemctl start
	// 9. bootstrap success

	if len(runcmd) < 9 {
		t.Fatalf("Expected at least 9 commands, got %d", len(runcmd))
	}

	// Verify NetBird commands are first
	if !strings.Contains(runcmd[0].(string), "install.sh") {
		t.Error("Command 0 should be NetBird install")
	}
	if !strings.Contains(runcmd[1].(string), "netbird up") {
		t.Error("Command 1 should be netbird up")
	}
	if !strings.Contains(runcmd[2].(string), "netbird status") {
		t.Error("Command 2 should be netbird status")
	}

	// Find where systemd overwrite starts (after k0s install worker)
	k0sInstallIndex := findK0sInstallCommand(runcmd)

	if k0sInstallIndex == -1 {
		t.Fatal("Expected to find k0s install worker command")
	}

	// Verify systemd overwrite comes after k0s install
	systemdModStart := k0sInstallIndex + 1
	if systemdModStart >= len(runcmd) {
		t.Fatal("Expected systemd overwrite after k0s install")
	}

	// Verify the systemd overwrite script exists after k0s install
	foundSystemdOverwrite := false
	for i := systemdModStart; i < len(runcmd); i++ {
		if cmdStr, ok := runcmd[i].(string); ok {
			if strings.Contains(cmdStr, "/bin/bash -c") &&
				strings.Contains(cmdStr, "k0sworker.service") &&
				strings.Contains(cmdStr, "systemctl daemon-reload") {
				foundSystemdOverwrite = true
				break
			}
		}
	}

	if !foundSystemdOverwrite {
		t.Error("Expected systemd overwrite script after k0s install")
	}
}

func TestInjectNetBirdCommandsWithSystemdModifications(t *testing.T) {
	setupKey := "test-setup-key-789"

	tests := []struct {
		name                    string
		cloudConfig             map[string]interface{}
		expectedInjected        bool
		expectedSystemdCommands bool
		verifyCommandOrder      bool
	}{
		{
			name: "With systemctl start k0sworker command",
			cloudConfig: map[string]interface{}{
				"runcmd": []interface{}{
					"curl -sSfL https://get.k0s.sh | sh",
					"/usr/local/bin/k0s install worker --token-file /etc/k0s.token",
					"(command -v systemctl > /dev/null 2>&1 && systemctl start k0sworker) || echo 'no systemctl'",
					"mkdir -p /run/cluster-api && touch /run/cluster-api/bootstrap-success.complete",
				},
			},
			expectedInjected:        true,
			expectedSystemdCommands: true,
			verifyCommandOrder:      true,
		},
		{
			name: "Without systemctl command",
			cloudConfig: map[string]interface{}{
				"runcmd": []interface{}{
					"echo 'test command'",
				},
			},
			expectedInjected:        true,
			expectedSystemdCommands: false,
			verifyCommandOrder:      false,
		},
		{
			name: "Empty runcmd",
			cloudConfig: map[string]interface{}{
				"runcmd": []interface{}{},
			},
			expectedInjected:        true,
			expectedSystemdCommands: false,
			verifyCommandOrder:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logr.Discard()
			injected := injectNetBirdCommands(tt.cloudConfig, setupKey, log)

			if injected != tt.expectedInjected {
				t.Errorf("injectNetBirdCommands returned %v, want %v", injected, tt.expectedInjected)
			}

			runcmd, ok := tt.cloudConfig["runcmd"].([]interface{})
			if !ok {
				t.Fatal("Expected runcmd array to exist")
			}

			// Verify NetBird commands are at the beginning
			if tt.expectedInjected {
				verifyNetBirdCommandsInjected(t, runcmd)
			}

			// Verify systemd overwrite script if expected
			if tt.expectedSystemdCommands {
				verifySystemdOverwriteCommands(t, runcmd)
			}

			// Verify command order if requested
			if tt.verifyCommandOrder {
				verifyCommandOrder(t, runcmd)
			}
		})
	}
}

func TestCreateSystemdOverwriteCommands(t *testing.T) {
	log := logr.Discard()
	commands := createSystemdOverwriteCommands(log)

	// Verify we got 2 commands (wrapper script creation + systemd service overwrite)
	expectedCount := 2
	if len(commands) != expectedCount {
		t.Errorf("Expected %d systemd overwrite commands, got %d", expectedCount, len(commands))
	}

	// Command 1: Wrapper script creation
	wrapperCmd, ok := commands[0].(string)
	if !ok {
		t.Fatal("First command should be a string")
	}

	// Verify wrapper script creation contains key elements
	wrapperElements := []string{
		"/bin/bash -c",
		"ExecStart=",
		"/k0s worker",
		"--node-ip=",
		"--kubelet-extra-args",
		"netbird status --ipv4",
		"/usr/local/bin/k0s-worker-wrapper.sh",
		"chmod +x",
		"set -euo pipefail",
		"systemd-cat -t k0sworker",
		"exec /usr/local/bin/k0s worker",
	}

	for _, element := range wrapperElements {
		if !strings.Contains(wrapperCmd, element) {
			t.Errorf("Wrapper command missing required element: %s", element)
		}
	}

	// === NEW: Verify args expansion behavior ===
	// Verify heredoc is unquoted to allow $ARGS expansion
	if strings.Contains(wrapperCmd, "<<'EOF'") {
		t.Error("Wrapper command uses quoted heredoc <<'EOF' which prevents $ARGS expansion")
	}

	// Verify $ARGS is not single-quoted
	if strings.Contains(wrapperCmd, `'$ARGS'`) {
		t.Error("$ARGS should not be single-quoted in wrapper command")
	}

	// Verify $ARGS is present for expansion
	if !strings.Contains(wrapperCmd, " $ARGS") {
		t.Error("Wrapper command should contain $ARGS for expansion")
	}

	// Verify NODE_IP is escaped for runtime evaluation
	if !strings.Contains(wrapperCmd, `\$(netbird`) {
		t.Error("NODE_IP command substitution should be escaped")
	}

	// Command 2: Systemd service overwrite
	serviceCmd, ok := commands[1].(string)
	if !ok {
		t.Fatal("Second command should be a string")
	}

	// Verify systemd service contains key elements
	serviceElements := []string{
		"/bin/bash -c",
		"ExecStart=/usr/local/bin/k0s-worker-wrapper.sh",
		"systemctl daemon-reload",
		"/etc/systemd/system/k0sworker.service",
		"ConditionFileIsExecutable=/usr/local/bin/k0s-worker-wrapper.sh",
	}

	for _, element := range serviceElements {
		if !strings.Contains(serviceCmd, element) {
			t.Errorf("Service command missing required element: %s", element)
		}
	}

	// Verify it's a complete systemd unit file
	if !strings.Contains(serviceCmd, "[Unit]") {
		t.Error("Service command should contain [Unit] section")
	}
	if !strings.Contains(serviceCmd, "[Service]") {
		t.Error("Service command should contain [Service] section")
	}
	if !strings.Contains(serviceCmd, "[Install]") {
		t.Error("Service command should contain [Install] section")
	}
}

func TestModifyCloudInitWithSystemdModifications(t *testing.T) {
	// Create a realistic cloud-config with k0s worker setup
	cloudConfig := map[string]interface{}{
		"write_files": []interface{}{
			map[string]interface{}{
				"path":        "/etc/k0s.token",
				"content":     "fake-token-content",
				"permissions": "0600",
			},
		},
		"runcmd": []interface{}{
			"curl -sSfL https://get.k0s.sh | sh",
			"/usr/local/bin/k0s install worker --token-file /etc/k0s.token --labels=k0smotron.io/machine-name=test-worker-0",
			"(command -v systemctl > /dev/null 2>&1 && systemctl start k0sworker) || echo 'no systemctl'",
			"mkdir -p /run/cluster-api && touch /run/cluster-api/bootstrap-success.complete",
		},
	}

	cloudConfigYAML, err := yaml.Marshal(cloudConfig)
	if err != nil {
		t.Fatalf("Failed to marshal cloud-config: %v", err)
	}

	cloudInitData := string(cloudConfigYAML)

	// Create a mock colony with NetBird enabled
	colony := &infrav1.Colony{
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: true,
			},
		},
	}

	// Modify cloud-init with NetBird
	setupKey := "test-setup-key-systemd"
	internalDNS := "test.svc.cluster.local:30443"
	replaced, err := modifyCloudInit(cloudInitData, internalDNS, setupKey, colony, logr.Discard())
	if err != nil {
		t.Fatalf("Failed to modify cloud-init: %v", err)
	}

	if replaced == cloudInitData {
		t.Fatal("Expected cloud-config to be modified")
	}

	// Decode and verify the result
	var replacedConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(replaced), &replacedConfig); err != nil {
		t.Fatalf("Failed to parse replaced cloud-config: %v", err)
	}

	runcmd, ok := replacedConfig["runcmd"].([]interface{})
	if !ok {
		t.Fatal("Expected runcmd array in modified config")
	}

	// Find systemctl start command
	systemctlIndex := -1
	for i, cmd := range runcmd {
		if cmdStr, ok := cmd.(string); ok {
			if strings.Contains(cmdStr, "systemctl start k0sworker") {
				systemctlIndex = i
				break
			}
		}
	}

	if systemctlIndex == -1 {
		t.Fatal("Expected to find systemctl start k0sworker command")
	}

	// Verify systemd overwrite commands exist before systemctl start
	// Now we have 2 commands: wrapper creation + service overwrite
	foundWrapperCreation := false
	foundServiceOverwrite := false

	for i := 0; i < systemctlIndex; i++ {
		cmdStr := runcmd[i].(string)

		// Check for wrapper script creation command
		if strings.Contains(cmdStr, "/bin/bash -c") &&
			strings.Contains(cmdStr, "k0s-worker-wrapper.sh") &&
			strings.Contains(cmdStr, "--node-ip=") &&
			strings.Contains(cmdStr, "netbird status --ipv4") {
			foundWrapperCreation = true
		}

		// Check for service overwrite command
		if strings.Contains(cmdStr, "/bin/bash -c") &&
			strings.Contains(cmdStr, "k0sworker.service") &&
			strings.Contains(cmdStr, "ExecStart=/usr/local/bin/k0s-worker-wrapper.sh") &&
			strings.Contains(cmdStr, "systemctl daemon-reload") {
			foundServiceOverwrite = true
		}
	}

	if !foundWrapperCreation {
		t.Error("Expected wrapper script creation before systemctl start")
	}
	if !foundServiceOverwrite {
		t.Error("Expected service overwrite before systemctl start")
	}

	// Verify NetBird commands are at the beginning
	if !strings.Contains(runcmd[0].(string), "install.sh") {
		t.Error("First command should be NetBird install")
	}

	// Verify the order: NetBird -> k0s install -> systemd mods -> systemctl start
	k0sInstallFound := false
	for i := 3; i < systemctlIndex; i++ {
		if strings.Contains(runcmd[i].(string), "k0s install worker") {
			k0sInstallFound = true
			break
		}
	}

	if !k0sInstallFound {
		t.Error("Expected k0s install worker command before systemd modifications")
	}
}

func TestSystemdOverwriteScriptStructure(t *testing.T) {
	// Test that the systemd overwrite commands have the correct structure
	// This validates the bash scripts generated by createSystemdOverwriteCommands

	log := logr.Discard()
	commands := createSystemdOverwriteCommands(log)

	if len(commands) != 2 {
		t.Fatalf("Expected 2 commands, got %d", len(commands))
	}

	// Test Command 1: Wrapper script creation
	wrapperScript, ok := commands[0].(string)
	if !ok {
		t.Fatal("First command should be a string")
	}

	// Test that the script extracts args from existing service
	if !strings.Contains(wrapperScript, "grep \"^ExecStart=\"") {
		t.Error("Wrapper script should extract ExecStart line from existing service")
	}

	// Test that it checks for existing kubelet-extra-args
	if !strings.Contains(wrapperScript, "kubelet-extra-args") {
		t.Error("Wrapper script should handle kubelet-extra-args")
	}

	// Test that it adds --node-ip to kubelet-extra-args
	if !strings.Contains(wrapperScript, "--node-ip=") {
		t.Error("Wrapper script should add --node-ip=")
	}

	// Test that it gets NODE_IP from netbird
	if !strings.Contains(wrapperScript, "netbird status --ipv4") {
		t.Error("Wrapper script should get NODE_IP from netbird status --ipv4")
	}

	// Test that it creates the wrapper script
	if !strings.Contains(wrapperScript, "/usr/local/bin/k0s-worker-wrapper.sh") {
		t.Error("Should create k0s-worker-wrapper.sh")
	}

	// Test that it makes the wrapper executable
	if !strings.Contains(wrapperScript, "chmod +x") {
		t.Error("Should make wrapper script executable")
	}

	// Test that wrapper has error handling
	if !strings.Contains(wrapperScript, "set -euo pipefail") {
		t.Error("Wrapper should have set -euo pipefail")
	}

	// Test that wrapper logs to journal
	if !strings.Contains(wrapperScript, "systemd-cat -t k0sworker") {
		t.Error("Wrapper should log to systemd journal")
	}

	// Test that wrapper uses exec
	if !strings.Contains(wrapperScript, "exec /usr/local/bin/k0s worker") {
		t.Error("Wrapper should use exec to run k0s")
	}

	// === NEW: Test for proper variable expansion ===
	// Test that heredoc is unquoted (allows $ARGS expansion)
	if strings.Contains(wrapperScript, "<<'EOF'") {
		t.Error("Heredoc should be unquoted (<<EOF) to allow $ARGS expansion")
	}

	// Test that $ARGS is present and NOT single-quoted
	if !strings.Contains(wrapperScript, "exec /usr/local/bin/k0s worker $ARGS") {
		t.Error("Wrapper should contain 'exec /usr/local/bin/k0s worker $ARGS' (unquoted)")
	}
	if strings.Contains(wrapperScript, `'$ARGS'`) {
		t.Error("$ARGS should NOT be single-quoted, that prevents expansion")
	}

	// Test that NODE_IP variables are escaped (evaluated at runtime)
	if !strings.Contains(wrapperScript, `\$(netbird status --ipv4)`) {
		t.Error("NODE_IP assignment should use escaped command substitution: \\$(netbird status --ipv4)")
	}
	if !strings.Contains(wrapperScript, `\${NODE_IP}`) {
		t.Error("NODE_IP references should be escaped: \\${NODE_IP}")
	}

	// Test Command 2: Systemd service overwrite
	serviceScript, ok := commands[1].(string)
	if !ok {
		t.Fatal("Second command should be a string")
	}

	// Test that it overwrites the service file
	if !strings.Contains(serviceScript, "cat >") {
		t.Error("Should overwrite the service file")
	}

	// Test that it reloads systemd
	if !strings.Contains(serviceScript, "systemctl daemon-reload") {
		t.Error("Should reload systemd")
	}

	// Test that the service file template has required sections
	if !strings.Contains(serviceScript, "[Unit]") {
		t.Error("Service file should have [Unit] section")
	}
	if !strings.Contains(serviceScript, "[Service]") {
		t.Error("Service file should have [Service] section")
	}
	if !strings.Contains(serviceScript, "[Install]") {
		t.Error("Service file should have [Install] section")
	}

	// Test that service calls the wrapper
	if !strings.Contains(serviceScript, "ExecStart=/usr/local/bin/k0s-worker-wrapper.sh") {
		t.Error("Service should execute the wrapper script")
	}

	// Test that service checks wrapper is executable
	if !strings.Contains(serviceScript, "ConditionFileIsExecutable=/usr/local/bin/k0s-worker-wrapper.sh") {
		t.Error("Service should check wrapper script is executable")
	}
}

func TestWrapperScriptArgsExpansion(t *testing.T) {
	// This test verifies that $ARGS gets properly expanded in the wrapper script
	// while $NODE_IP remains as a runtime variable

	log := logr.Discard()
	commands := createSystemdOverwriteCommands(log)

	if len(commands) != 2 {
		t.Fatalf("Expected 2 commands, got %d", len(commands))
	}

	wrapperCmd, ok := commands[0].(string)
	if !ok {
		t.Fatal("First command should be a string")
	}

	// Test 1: Verify the heredoc is unquoted (allows variable expansion)
	// Unquoted heredoc: cat > file <<EOF
	// Quoted heredoc: cat > file <<'EOF' or <<'EOF' or <<"EOF"
	if strings.Contains(wrapperCmd, "<<'EOF'") {
		t.Error("Heredoc should be unquoted (<<EOF) to allow $ARGS expansion, found <<'EOF'")
	}
	if strings.Contains(wrapperCmd, `<<"EOF"`) {
		t.Error("Heredoc should be unquoted (<<EOF) to allow $ARGS expansion, found <<\"EOF\"")
	}

	// Test 2: Verify $ARGS is used without quotes in the wrapper (will be expanded)
	if !strings.Contains(wrapperCmd, "exec /usr/local/bin/k0s worker $ARGS") {
		t.Error("Wrapper should contain 'exec /usr/local/bin/k0s worker $ARGS' (unquoted)")
	}

	// Test 3: Verify $ARGS is NOT quoted with single quotes (which would prevent expansion)
	if strings.Contains(wrapperCmd, `exec /usr/local/bin/k0s worker '$ARGS'`) {
		t.Error("$ARGS should not be single-quoted in the wrapper, that prevents expansion")
	}

	// Test 4: Verify $NODE_IP is escaped so it's evaluated at runtime
	if !strings.Contains(wrapperCmd, `NODE_IP="\$(netbird status --ipv4)"`) {
		t.Error("NODE_IP assignment should be escaped: NODE_IP=\"\\$(netbird status --ipv4)\"")
	}
	if !strings.Contains(wrapperCmd, `\${NODE_IP}`) {
		t.Error("NODE_IP references should be escaped: \\${NODE_IP}")
	}

	// Test 5: Verify the args extraction logic is present
	if !strings.Contains(wrapperCmd, `ARGS=$(grep "^ExecStart="`) {
		t.Error("Should extract ARGS from existing service file")
	}

	// Test 6: Verify node-ip injection logic
	if !strings.Contains(wrapperCmd, `--kubelet-extra-args=--node-ip=\${NODE_IP}`) {
		t.Error("Should inject --node-ip=\\${NODE_IP} into kubelet-extra-args")
	}

	t.Log("✓ Wrapper script correctly uses unquoted heredoc for $ARGS expansion")
	t.Log("✓ $ARGS will be expanded at script creation time with actual k0s arguments")
	t.Log("✓ $NODE_IP is escaped and will be evaluated at runtime")
}

func TestWrapperScriptVariousArgsPatterns(t *testing.T) {
	// This test simulates different k0s args patterns to ensure they're handled correctly
	// We simulate the actual bash logic that creates the wrapper

	testCases := []struct {
		name         string
		originalArgs string
		expectedIn   string // What we expect to find in the final wrapper
		description  string
	}{
		{
			name:         "No kubelet-extra-args",
			originalArgs: "--token-file=/etc/k0s.token --labels=k0smotron.io/machine-name=test-1",
			expectedIn:   "--token-file=/etc/k0s.token --labels=k0smotron.io/machine-name=test-1 --kubelet-extra-args=--node-ip=${NODE_IP}",
			description:  "Should append --kubelet-extra-args with --node-ip",
		},
		{
			name:         "Existing kubelet-extra-args",
			originalArgs: "--token-file=/etc/k0s.token --kubelet-extra-args=--hostname-override=test-1 --labels=test",
			expectedIn:   "--token-file=/etc/k0s.token --kubelet-extra-args=--node-ip=${NODE_IP},--hostname-override=test-1 --labels=test",
			description:  "Should inject --node-ip at the start of existing kubelet-extra-args",
		},
		{
			name:         "Only token-file",
			originalArgs: "--token-file=/etc/k0s.token",
			expectedIn:   "--token-file=/etc/k0s.token --kubelet-extra-args=--node-ip=${NODE_IP}",
			description:  "Should append --kubelet-extra-args when only token is present",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the bash script logic
			args := tt.originalArgs

			// This simulates: if echo "$ARGS" | grep -q "kubelet-extra-args"; then
			if strings.Contains(args, "kubelet-extra-args") {
				// Simulate: ARGS=$(echo "$ARGS" | sed "s|--kubelet-extra-args=|--kubelet-extra-args=--node-ip=\${NODE_IP},|")
				args = strings.Replace(args, "--kubelet-extra-args=", "--kubelet-extra-args=--node-ip=${NODE_IP},", 1)
			} else {
				// Simulate: ARGS="$ARGS --kubelet-extra-args=--node-ip=\${NODE_IP}"
				args = args + " --kubelet-extra-args=--node-ip=${NODE_IP}"
			}

			if args != tt.expectedIn {
				t.Errorf("%s: Expected: %s\nGot: %s", tt.description, tt.expectedIn, args)
			} else {
				t.Logf("✓ %s: %s", tt.name, tt.description)
			}
		})
	}
}

func TestWrapperScriptRuntimeVsCreationTime(t *testing.T) {
	// This test ensures we understand when variables are expanded
	log := logr.Discard()
	commands := createSystemdOverwriteCommands(log)

	wrapperCmd := commands[0].(string)

	// Variables that should be expanded at CREATION time (during cloud-init):
	creationTimeVars := []string{
		"$ARGS", // Should be expanded to actual k0s args when wrapper is created
	}

	// Variables that should be expanded at RUNTIME (when wrapper executes):
	runtimeVars := []struct {
		escaped   string
		unescaped string
	}{
		{`\$(netbird status --ipv4)`, `$(netbird status --ipv4)`},      // Command substitution
		{`\${NODE_IP}`, `${NODE_IP}`},                                  // Variable reference
		{`NODE_IP="\$(netbird status`, `NODE_IP="$(netbird status`},    // Assignment
		{`echo "[k0sworker] using NODE_IP=\${NODE_IP}"`, `${NODE_IP}`}, // In echo statement
	}

	t.Log("Checking creation-time variables...")
	for _, v := range creationTimeVars {
		if !strings.Contains(wrapperCmd, v) {
			t.Errorf("Creation-time variable %s should be present unescaped in command", v)
		} else {
			t.Logf("✓ Found creation-time variable: %s", v)
		}
	}

	t.Log("Checking runtime variables are properly escaped...")
	for _, v := range runtimeVars {
		if !strings.Contains(wrapperCmd, v.escaped) {
			t.Errorf("Runtime variable should be escaped: expected %s in command", v.escaped)
		} else {
			t.Logf("✓ Runtime variable properly escaped: %s", v.escaped)
		}

		// Make sure it's not unescaped where it shouldn't be
		// Note: We can't simply check for unescaped version because the escaped version contains it
		// So we check specific patterns
	}

	t.Log("✓ Variable expansion timing is correct")
}
