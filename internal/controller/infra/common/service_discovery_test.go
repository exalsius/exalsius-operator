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

package common

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestValidateControlPlanePorts tests port validation logic
func TestValidateControlPlanePorts(t *testing.T) {
	tests := []struct {
		name        string
		service     *corev1.Service
		wantErr     bool
		errContains string
	}{
		{
			name: "Valid service with named ports (api, konnectivity)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 30443},
						{Name: "konnectivity", Port: 30132},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid service with named ports (kube-apiserver, konnectivity)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "kube-apiserver", Port: 6443},
						{Name: "konnectivity", Port: 8132},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid service with numbered ports (30443, 30132)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "custom-api", Port: 30443},
						{Name: "custom-konnectivity", Port: 30132},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid service with numbered ports (6443, 8132)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "custom", Port: 6443},
						{Name: "custom2", Port: 8132},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid service with mixed named and numbered ports",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 9999},     // Name matches
						{Name: "custom", Port: 30132}, // Port number matches
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Missing API port (by name and number)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "custom", Port: 12345},
						{Name: "konnectivity", Port: 30132},
					},
				},
			},
			wantErr:     true,
			errContains: "API server port not found",
		},
		{
			name: "Missing konnectivity port (by name and number)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 30443},
						{Name: "custom", Port: 12345},
					},
				},
			},
			wantErr:     true,
			errContains: "konnectivity port not found",
		},
		{
			name: "Service with no ports",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{},
				},
			},
			wantErr:     true,
			errContains: "API server port not found",
		},
		{
			name: "Service with only unrelated ports",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
			},
			wantErr:     true,
			errContains: "API server port not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateControlPlanePorts(tt.service)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateControlPlanePorts() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateControlPlanePorts() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("ValidateControlPlanePorts() unexpected error = %v", err)
			}
		})
	}
}

// TestGetAPIServerPort tests API server port extraction
func TestGetAPIServerPort(t *testing.T) {
	tests := []struct {
		name     string
		service  *corev1.Service
		wantPort int32
	}{
		{
			name: "Port found by name 'api'",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 12345},
						{Name: "other", Port: 30443},
					},
				},
			},
			wantPort: 12345,
		},
		{
			name: "Port found by name 'kube-apiserver'",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "kube-apiserver", Port: 54321},
						{Name: "other", Port: 30443},
					},
				},
			},
			wantPort: 54321,
		},
		{
			name: "Port found by number 30443 (fallback)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "custom", Port: 30443},
						{Name: "other", Port: 9999},
					},
				},
			},
			wantPort: 30443,
		},
		{
			name: "Port found by number 6443 (fallback)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "custom", Port: 6443},
						{Name: "other", Port: 9999},
					},
				},
			},
			wantPort: 6443,
		},
		{
			name: "Multiple matching ports - returns first by name",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 11111},
						{Name: "kube-apiserver", Port: 22222},
					},
				},
			},
			wantPort: 11111,
		},
		{
			name: "No matching port - returns default 30443",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
			},
			wantPort: 30443,
		},
		{
			name: "Empty ports - returns default 30443",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{},
				},
			},
			wantPort: 30443,
		},
		{
			name: "Port with custom number",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 9876},
					},
				},
			},
			wantPort: 9876,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := GetAPIServerPort(tt.service)
			if port != tt.wantPort {
				t.Errorf("GetAPIServerPort() = %d, want %d", port, tt.wantPort)
			}
		})
	}
}

// TestGetServiceExternalAddress tests external address extraction
func TestGetServiceExternalAddress(t *testing.T) {
	tests := []struct {
		name        string
		service     *corev1.Service
		wantAddress string
		wantFound   bool
	}{
		{
			name: "LoadBalancer with hostname in ingress",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "lb.example.com"},
						},
					},
				},
			},
			wantAddress: "lb.example.com",
			wantFound:   true,
		},
		{
			name: "LoadBalancer with IP in ingress",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.0.0.100"},
						},
					},
				},
			},
			wantAddress: "10.0.0.100",
			wantFound:   true,
		},
		{
			name: "LoadBalancer with both hostname and IP - prefers hostname",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "lb.example.com", IP: "10.0.0.100"},
						},
					},
				},
			},
			wantAddress: "lb.example.com",
			wantFound:   true,
		},
		{
			name: "LoadBalancer with no ingress (not ready yet)",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			wantAddress: "",
			wantFound:   false,
		},
		{
			name: "LoadBalancer with empty ingress entry",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{}, // Empty entry
						},
					},
				},
			},
			wantAddress: "",
			wantFound:   false,
		},
		{
			name: "NodePort service",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeNodePort,
				},
			},
			wantAddress: "",
			wantFound:   false,
		},
		{
			name: "ClusterIP service",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
				},
			},
			wantAddress: "",
			wantFound:   false,
		},
		{
			name: "ExternalName service",
			service: &corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeExternalName,
				},
			},
			wantAddress: "",
			wantFound:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			address, found := GetServiceExternalAddress(tt.service)
			if address != tt.wantAddress {
				t.Errorf("GetServiceExternalAddress() address = %q, want %q", address, tt.wantAddress)
			}
			if found != tt.wantFound {
				t.Errorf("GetServiceExternalAddress() found = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

// TestExtractClusterNameFromDeploymentName tests cluster name extraction
func TestExtractClusterNameFromDeploymentName(t *testing.T) {
	tests := []struct {
		name           string
		deploymentName string
		colonyName     string
		wantCluster    string
	}{
		{
			name:           "Valid extraction",
			deploymentName: "test-colony-cluster-a",
			colonyName:     "test-colony",
			wantCluster:    "cluster-a",
		},
		{
			name:           "Valid extraction with longer cluster name",
			deploymentName: "my-colony-my-long-cluster-name",
			colonyName:     "my-colony",
			wantCluster:    "my-long-cluster-name",
		},
		{
			name:           "Valid extraction with hyphens",
			deploymentName: "prod-us-east-cluster-1",
			colonyName:     "prod-us-east",
			wantCluster:    "cluster-1",
		},
		{
			name:           "Deployment name equals colony name with hyphen",
			deploymentName: "test-colony-",
			colonyName:     "test-colony",
			wantCluster:    "",
		},
		{
			name:           "Deployment name too short",
			deploymentName: "test-colony",
			colonyName:     "test-colony",
			wantCluster:    "",
		},
		{
			name:           "Deployment name doesn't match prefix",
			deploymentName: "other-colony-cluster-a",
			colonyName:     "test-colony",
			wantCluster:    "",
		},
		{
			name:           "Empty deployment name",
			deploymentName: "",
			colonyName:     "test-colony",
			wantCluster:    "",
		},
		{
			name:           "Empty colony name",
			deploymentName: "test-colony-cluster-a",
			colonyName:     "",
			wantCluster:    "", // Empty colony name creates prefix "-", which doesn't match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractClusterNameFromDeploymentName(tt.deploymentName, tt.colonyName)
			if result != tt.wantCluster {
				t.Errorf("ExtractClusterNameFromDeploymentName() = %q, want %q", result, tt.wantCluster)
			}
		})
	}
}

// TestDetermineAPIEndpoint tests endpoint determination logic
func TestDetermineAPIEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		service      *corev1.Service
		wantEndpoint string
		wantErr      bool
		errContains  string
	}{
		{
			name: "LoadBalancer with external hostname",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeLoadBalancer,
					ClusterIP: "10.96.0.1",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "lb.example.com"},
						},
					},
				},
			},
			wantEndpoint: "lb.example.com:6443",
			wantErr:      false,
		},
		{
			name: "LoadBalancer with external IP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeLoadBalancer,
					ClusterIP: "10.96.0.1",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 30443},
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "203.0.113.50"},
						},
					},
				},
			},
			wantEndpoint: "203.0.113.50:30443",
			wantErr:      false,
		},
		{
			name: "LoadBalancer without external address - falls back to ClusterIP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeLoadBalancer,
					ClusterIP: "10.96.0.1",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{},
					},
				},
			},
			wantEndpoint: "10.96.0.1:6443",
			wantErr:      false,
		},
		{
			name: "NodePort service - uses ClusterIP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeNodePort,
					ClusterIP: "10.96.0.2",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 30443},
					},
				},
			},
			wantEndpoint: "10.96.0.2:30443",
			wantErr:      false,
		},
		{
			name: "ClusterIP service - uses ClusterIP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "10.96.0.3",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
			},
			wantEndpoint: "10.96.0.3:6443",
			wantErr:      false,
		},
		{
			name: "Service with ClusterIP 'None' (headless)",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "None",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
			},
			wantErr:     true,
			errContains: "no valid ClusterIP",
		},
		{
			name: "Service with empty ClusterIP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
			},
			wantErr:     true,
			errContains: "no valid ClusterIP",
		},
		{
			name: "Service with IPv6 ClusterIP",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "fd00::1",
					Ports: []corev1.ServicePort{
						{Name: "api", Port: 6443},
					},
				},
			},
			wantEndpoint: "fd00::1:6443",
			wantErr:      false,
		},
		{
			name: "Port extraction from port number fallback",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "10.96.0.5",
					Ports: []corev1.ServicePort{
						{Name: "custom", Port: 30443}, // Should be detected by port number
					},
				},
			},
			wantEndpoint: "10.96.0.5:30443",
			wantErr:      false,
		},
		{
			name: "Default port when no matching port found",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "test-svc"},
				Spec: corev1.ServiceSpec{
					Type:      corev1.ServiceTypeClusterIP,
					ClusterIP: "10.96.0.6",
					Ports: []corev1.ServicePort{
						{Name: "http", Port: 80},
					},
				},
			},
			wantEndpoint: "10.96.0.6:30443", // Default port
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := DetermineAPIEndpoint(tt.service)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DetermineAPIEndpoint() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("DetermineAPIEndpoint() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("DetermineAPIEndpoint() unexpected error = %v", err)
				return
			}

			if endpoint != tt.wantEndpoint {
				t.Errorf("DetermineAPIEndpoint() = %q, want %q", endpoint, tt.wantEndpoint)
			}
		})
	}
}

// TestDiscoverControlPlaneService tests the service discovery logic
func TestDiscoverControlPlaneService(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	tests := []struct {
		name        string
		namespace   string
		colonyName  string
		clusterName string
		services    []corev1.Service
		wantErr     bool
		errContains string
		wantService string
	}{
		{
			name:        "Service found with correct labels",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     false,
			wantService: "kmc-test-colony-cluster-a",
		},
		{
			name:        "No services found (cluster still provisioning)",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services:    []corev1.Service{},
			wantErr:     true,
			errContains: "not found yet",
		},
		{
			name:        "Service found but missing ClusterIP",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "",
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "no ClusterIP",
		},
		{
			name:        "Service found but ClusterIP is 'None'",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "None",
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "no ClusterIP",
		},
		{
			name:        "Service found but missing API port",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Ports: []corev1.ServicePort{
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "API server port not found",
		},
		{
			name:        "Service found but missing konnectivity port",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "konnectivity port not found",
		},
		{
			name:        "Service in different namespace (should not match)",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "other-namespace",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     true,
			errContains: "not found yet",
		},
		{
			name:        "Multiple services match (returns first)",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a-nodeport",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Type:      corev1.ServiceTypeNodePort,
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a-loadbalancer",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.2",
						Type:      corev1.ServiceTypeLoadBalancer,
						Ports: []corev1.ServicePort{
							{Name: "api", Port: 30443},
							{Name: "konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr: false,
			// Returns first service found
		},
		{
			name:        "Service with port by number (30443, 30132)",
			namespace:   "default",
			colonyName:  "test-colony",
			clusterName: "cluster-a",
			services: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kmc-test-colony-cluster-a",
						Namespace: "default",
						Labels: map[string]string{
							"app":       "k0smotron",
							"component": "cluster",
							"cluster":   "test-colony-cluster-a",
						},
					},
					Spec: corev1.ServiceSpec{
						ClusterIP: "10.96.0.1",
						Ports: []corev1.ServicePort{
							{Name: "custom-api", Port: 30443},
							{Name: "custom-konnectivity", Port: 30132},
						},
					},
				},
			},
			wantErr:     false,
			wantService: "kmc-test-colony-cluster-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build fake client with services
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range tt.services {
				builder = builder.WithObjects(&tt.services[i])
			}
			client := builder.Build()

			ctx := context.Background()
			service, err := DiscoverControlPlaneService(ctx, client, tt.namespace, tt.colonyName, tt.clusterName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("DiscoverControlPlaneService() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("DiscoverControlPlaneService() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("DiscoverControlPlaneService() unexpected error = %v", err)
				return
			}

			if tt.wantService != "" && service.Name != tt.wantService {
				t.Errorf("DiscoverControlPlaneService() service name = %q, want %q", service.Name, tt.wantService)
			}
		})
	}
}

// Helper function
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
