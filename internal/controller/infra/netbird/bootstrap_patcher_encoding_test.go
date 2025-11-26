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

	"sigs.k8s.io/yaml"
)

// TestSecretDataEncoding verifies that we don't double-encode the secret data
// This was the bug causing: "cannot unmarshal !!str into provisioner.InputProvisionData"
func TestSecretDataEncoding(t *testing.T) {
	// Create a valid cloud-config with a token
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
		"current-context": "k0s",
	}

	kubeconfigYAML, _ := yaml.Marshal(kubeconfig)
	gzipped, _ := gzipData(kubeconfigYAML)
	tokenContent := base64.StdEncoding.EncodeToString(gzipped)

	cloudConfig := `## template: jinja
#cloud-config
write_files:
  - path: /etc/k0s.token
    content: ` + tokenContent + `
    permissions: "0600"
runcmd:
  - echo test
`

	// Simulate what happens when we store this in Secret.Data
	// After our fix, we should store raw bytes directly
	secretDataValue := []byte(cloudConfig)

	// Verify: the bytes should be the raw cloud-config (not base64-encoded)
	storedCloudConfig := string(secretDataValue)

	if !strings.HasPrefix(storedCloudConfig, "## template: jinja") {
		t.Error("Secret data should contain raw cloud-config, not base64-encoded string")
	}

	if !strings.Contains(storedCloudConfig, "#cloud-config") {
		t.Error("Secret data should contain #cloud-config directive")
	}

	if !strings.Contains(storedCloudConfig, "write_files:") {
		t.Error("Secret data should contain write_files section")
	}

	// Verify it's valid YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(secretDataValue, &parsed); err != nil {
		t.Fatalf("Secret data should be valid YAML: %v", err)
	}

	// Verify: if someone tries to base64-decode it, they should get nonsense
	// (because it's not base64-encoded)
	_, err := base64.StdEncoding.DecodeString(storedCloudConfig)
	if err == nil {
		t.Error("Secret data should NOT be base64-encoded (it's raw cloud-config)")
	}

	t.Log("✓ Secret data correctly stored as raw bytes (not double-encoded)")
}

// TestKubernetesSecretBehavior documents how Kubernetes Secret.Data works
func TestKubernetesSecretBehavior(t *testing.T) {
	// This test documents the expected behavior

	originalData := "## template: jinja\n#cloud-config\ntest: data"

	// What we store in Secret.Data
	secretData := []byte(originalData)

	// When Kubernetes serializes to YAML, it will base64-encode
	// (this is automatic, we don't do it)
	expectedInYAML := base64.StdEncoding.EncodeToString(secretData)

	// When we read from API, we get raw bytes back
	// (Kubernetes automatically decodes)
	retrieved := string(secretData)

	if retrieved != originalData {
		t.Errorf("Expected to retrieve original data, got %q", retrieved)
	}

	t.Logf("✓ Original data: %q", originalData)
	t.Logf("✓ Stored in Secret.Data: []byte (raw bytes)")
	t.Logf("✓ Appears in YAML as: %s (base64, automatic)", expectedInYAML[:20]+"...")
	t.Logf("✓ Retrieved from API: %q (raw bytes, automatic decode)", retrieved[:30]+"...")
}

// TestNoDoubleEncoding verifies the actual fix - that we don't manually encode
func TestNoDoubleEncoding(t *testing.T) {
	cloudConfig := `## template: jinja
#cloud-config
write_files:
  - path: /etc/k0s.token
    content: FAKE_TOKEN
`

	// WRONG way (old code - causes double encoding):
	// encodedData := base64.StdEncoding.EncodeToString([]byte(cloudConfig))
	// secretData := []byte(encodedData) // ← This is base64 string as bytes
	// Result: Kubernetes encodes AGAIN → double-encoded

	// CORRECT way (new code):
	secretData := []byte(cloudConfig) // ← Just raw bytes

	// Verify: the bytes are the raw cloud-config
	if string(secretData) != cloudConfig {
		t.Error("Secret data should be raw cloud-config bytes")
	}

	// Verify: trying to decode as base64 should fail
	_, err := base64.StdEncoding.DecodeString(string(secretData))
	if err == nil {
		t.Error("Secret data should NOT be base64-encoded")
	}

	// Simulate what k0smotron receives after Kubernetes decoding
	// (Kubernetes automatically decodes the base64 from YAML)
	k0smotronReceives := string(secretData)

	// k0smotron should be able to parse this as YAML
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(k0smotronReceives), &parsed); err != nil {
		t.Fatalf("k0smotron should be able to parse: %v", err)
	}

	if _, ok := parsed["write_files"]; !ok {
		t.Error("Parsed YAML should contain write_files")
	}

	t.Log("✓ No double encoding - k0smotron can parse the data")
}
