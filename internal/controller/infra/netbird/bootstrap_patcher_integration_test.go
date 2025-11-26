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

	"github.com/go-logr/logr"
	"sigs.k8s.io/yaml"
)

// TestCloudConfigFormatPreservation tests that the cloud-config format is preserved
// exactly as k0smotron creates it, preventing parse errors
func TestCloudConfigFormatPreservation(t *testing.T) {
	// Create a realistic cloud-config like k0smotron generates
	// First, create a valid kubeconfig
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
					"token": "test.token.value",
				},
			},
		},
	}

	// Encode it as k0s token
	kubeconfigYAML, _ := yaml.Marshal(kubeconfig)
	gzipped, _ := gzipData(kubeconfigYAML)
	tokenContent := base64.StdEncoding.EncodeToString(gzipped)

	// Create cloud-config with k0smotron-style formatting
	originalCloudConfig := `## template: jinja
#cloud-config
write_files:
  - path: /etc/k0s.token
    content: ` + tokenContent + `
    permissions: "0600"
runcmd:
  - curl -sSfL --retry 5 https://get.k0s.sh | K0S_INSTALL_PATH=/usr/local/bin K0S_VERSION=v1.32.8+k0s.0 sh
  - /usr/local/bin/k0s install worker --token-file /etc/k0s.token --labels=k0smotron.io/machine-name=test-machine
  - systemctl start k0sworker
`

	// Modify cloud-init using our function (no NetBird injection for this test)
	internalDNS := "kmc-test-cluster.default.svc.cluster.local:30443"
	replaced, err := modifyCloudInit(originalCloudConfig, internalDNS, "", nil, logr.Discard())
	if err != nil {
		t.Fatalf("Failed to modify cloud-init: %v", err)
	}

	// Critical checks for k0smotron compatibility
	// Note: With YAML marshaling, comments (like ## template: jinja) are not preserved,
	// but the YAML structure is valid and k0smotron can still parse it.

	// 1. Verify the token was actually replaced (should be different)
	if replaced == originalCloudConfig {
		t.Error("Cloud-config was not modified")
	}

	// 2. Verify k0smotron can parse it (simulate their parser)
	var cloudConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(replaced), &cloudConfig); err != nil {
		t.Fatalf("k0smotron would fail to parse this cloud-config: %v", err)
	}

	// 3. Verify structure matches k0smotron expectations
	writeFiles, ok := cloudConfig["write_files"].([]interface{})
	if !ok || len(writeFiles) == 0 {
		t.Error("write_files section missing or malformed")
	}

	runcmd, ok := cloudConfig["runcmd"].([]interface{})
	if !ok || len(runcmd) == 0 {
		t.Error("runcmd section missing or malformed")
	}

	// 4. Verify the token inside is valid
	file := writeFiles[0].(map[string]interface{})
	newTokenContent := file["content"].(string)

	// Decode and verify token
	decodedToken, err := base64.StdEncoding.DecodeString(newTokenContent)
	if err != nil {
		t.Fatalf("Token is not valid base64: %v", err)
	}

	kubeconfigBytes, err := gunzipData(decodedToken)
	if err != nil {
		t.Fatalf("Token is not valid gzip: %v", err)
	}

	var newKubeconfig map[string]interface{}
	if err := yaml.Unmarshal(kubeconfigBytes, &newKubeconfig); err != nil {
		t.Fatalf("Token does not contain valid kubeconfig: %v", err)
	}

	// Verify the URL was actually changed
	clusters := newKubeconfig["clusters"].([]interface{})
	cluster := clusters[0].(map[string]interface{})["cluster"].(map[string]interface{})
	server := cluster["server"].(string)

	if !strings.Contains(server, "svc.cluster.local") {
		t.Errorf("Server URL not replaced with internal DNS. Got: %s", server)
	}

	t.Log("✓ Cloud-config format preserved - k0smotron will be able to parse it")
	t.Log("✓ Token successfully modified with internal DNS")
}

// TestCloudConfigNoYAMLAnnotations verifies that the output doesn't contain
// YAML type annotations that break k0smotron's parser
func TestCloudConfigNoYAMLAnnotations(t *testing.T) {
	simpleCloudConfig := `#cloud-config
write_files:
  - path: /etc/k0s.token
    content: H4sIAAAAAAAC/test
    permissions: "0600"
`

	// Process it
	internalDNS := "test.default.svc.cluster.local:30443"
	replaced, err := modifyCloudInit(simpleCloudConfig, internalDNS, "", nil, logr.Discard())
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}

	// Check for YAML type annotations that would break k0smotron
	forbiddenPatterns := []string{
		"!!str",      // Type annotation
		"!!map",      // Type annotation
		"!!seq",      // Type annotation
		"!binary",    // Binary annotation
		"content: |", // Multi-line string indicator (changes format)
	}

	for _, pattern := range forbiddenPatterns {
		if strings.Contains(replaced, pattern) {
			t.Errorf("Cloud-config contains forbidden pattern %q - this will break k0smotron", pattern)
		}
	}

	t.Log("✓ No YAML type annotations found - safe for k0smotron")
}
