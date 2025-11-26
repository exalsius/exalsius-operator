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

package cilium

import (
	"context"
	"testing"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestParseEndpoint tests the parseEndpoint function with various input formats
func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		wantHost    string
		wantPort    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "Valid IPv4 address with standard port",
			endpoint: "192.168.1.1:6443",
			wantHost: "192.168.1.1",
			wantPort: "6443",
			wantErr:  false,
		},
		{
			name:     "Valid IPv4 address with custom port",
			endpoint: "10.0.0.1:30443",
			wantHost: "10.0.0.1",
			wantPort: "30443",
			wantErr:  false,
		},
		{
			name:     "Valid IPv6 address with port",
			endpoint: "[2001:db8::1]:6443",
			wantHost: "2001:db8::1",
			wantPort: "6443",
			wantErr:  false,
		},
		{
			name:     "Valid IPv6 loopback with port",
			endpoint: "[::1]:8443",
			wantHost: "::1",
			wantPort: "8443",
			wantErr:  false,
		},
		{
			name:     "Valid hostname with port",
			endpoint: "api.example.com:6443",
			wantHost: "api.example.com",
			wantPort: "6443",
			wantErr:  false,
		},
		{
			name:     "Valid NetBird router hostname",
			endpoint: "colony-router.netbird.cloud:30443",
			wantHost: "colony-router.netbird.cloud",
			wantPort: "30443",
			wantErr:  false,
		},
		{
			name:     "Valid FQDN with subdomain",
			endpoint: "kmc-test-cluster.default.svc.cluster.local:30443",
			wantHost: "kmc-test-cluster.default.svc.cluster.local",
			wantPort: "30443",
			wantErr:  false,
		},
		{
			name:        "Invalid format - missing port",
			endpoint:    "192.168.1.1",
			wantErr:     true,
			errContains: "invalid endpoint format",
		},
		{
			name:        "Invalid format - multiple colons without brackets",
			endpoint:    "host:port:extra",
			wantErr:     true,
			errContains: "invalid endpoint format",
		},
		{
			name:        "Invalid format - empty string",
			endpoint:    "",
			wantErr:     true,
			errContains: "invalid endpoint format",
		},
		{
			name:        "Invalid format - only colon",
			endpoint:    ":",
			wantErr:     true,
			errContains: "host or port is empty",
		},
		{
			name:        "Invalid format - empty host",
			endpoint:    ":6443",
			wantErr:     true,
			errContains: "host or port is empty",
		},
		{
			name:        "Invalid format - empty port",
			endpoint:    "192.168.1.1:",
			wantErr:     true,
			errContains: "host or port is empty",
		},
		{
			name:        "Invalid IPv6 - missing closing bracket",
			endpoint:    "[2001:db8::1:6443",
			wantErr:     true,
			errContains: "invalid IPv6 endpoint format",
		},
		{
			name:        "Invalid IPv6 - missing port after bracket",
			endpoint:    "[2001:db8::1]",
			wantErr:     true,
			errContains: "missing port in IPv6 endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseEndpoint(tt.endpoint)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parseEndpoint() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("parseEndpoint() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("parseEndpoint() unexpected error = %v", err)
				return
			}

			if host != tt.wantHost {
				t.Errorf("parseEndpoint() host = %v, want %v", host, tt.wantHost)
			}
			if port != tt.wantPort {
				t.Errorf("parseEndpoint() port = %v, want %v", port, tt.wantPort)
			}
		})
	}
}

// TestEnsureAPIEndpointConfigMap tests the ConfigMap creation and update logic
func TestEnsureAPIEndpointConfigMap(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	tests := []struct {
		name            string
		colony          *infrav1.Colony
		clusterName     string
		exposedEndpoint string
		setupFunc       func(*fake.ClientBuilder) *fake.ClientBuilder
		wantErr         bool
		errContains     string
	}{
		{
			name:            "Missing kubeconfig secret",
			colony:          createTestColony("test-colony", "default"),
			clusterName:     "cluster-a",
			exposedEndpoint: "192.168.1.1:6443",
			setupFunc: func(builder *fake.ClientBuilder) *fake.ClientBuilder {
				// Don't add the kubeconfig secret
				return builder
			},
			wantErr:     true,
			errContains: "failed to get kubeconfig secret",
		},
		{
			name:            "Kubeconfig secret exists but is empty",
			colony:          createTestColony("test-colony", "default"),
			clusterName:     "cluster-a",
			exposedEndpoint: "192.168.1.1:6443",
			setupFunc: func(builder *fake.ClientBuilder) *fake.ClientBuilder {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-colony-cluster-a-kubeconfig",
						Namespace: "default",
					},
					Data: map[string][]byte{},
				}
				return builder.WithObjects(secret)
			},
			wantErr:     true,
			errContains: "does not contain key 'value'",
		},
		{
			name:            "Invalid kubeconfig data",
			colony:          createTestColony("test-colony", "default"),
			clusterName:     "cluster-a",
			exposedEndpoint: "192.168.1.1:6443",
			setupFunc: func(builder *fake.ClientBuilder) *fake.ClientBuilder {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-colony-cluster-a-kubeconfig",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"value": []byte("invalid kubeconfig data"),
					},
				}
				return builder.WithObjects(secret)
			},
			wantErr:     true,
			errContains: "failed to create REST config",
		},
		{
			name:            "Invalid endpoint format",
			colony:          createTestColony("test-colony", "default"),
			clusterName:     "cluster-a",
			exposedEndpoint: "invalid-endpoint-no-port",
			setupFunc: func(builder *fake.ClientBuilder) *fake.ClientBuilder {
				return builder
			},
			wantErr:     true,
			errContains: "failed to parse endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.setupFunc != nil {
				builder = tt.setupFunc(builder)
			}
			client := builder.Build()

			ctx := context.Background()
			err := EnsureAPIEndpointConfigMap(ctx, client, tt.colony, tt.clusterName, tt.exposedEndpoint, scheme)

			if tt.wantErr {
				if err == nil {
					t.Errorf("EnsureAPIEndpointConfigMap() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("EnsureAPIEndpointConfigMap() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("EnsureAPIEndpointConfigMap() unexpected error = %v", err)
			}
		})
	}
}

// Helper functions

func createTestColony(name, namespace string) *infrav1.Colony {
	return &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled:      true,
				APIKeySecret: "netbird-api-key",
			},
		},
		Status: infrav1.ColonyStatus{
			ClusterDeploymentRefs: []*corev1.ObjectReference{
				{
					Name:      name + "-cluster-a",
					Namespace: namespace,
				},
			},
		},
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(s) > 0 && len(substr) > 0 && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

