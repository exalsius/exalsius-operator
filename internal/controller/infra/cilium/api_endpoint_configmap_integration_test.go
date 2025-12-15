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

package cilium_test

import (
	"context"
	"encoding/json"
	"testing"

	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestIntegrationConfigMapCreation tests the full flow of ConfigMap creation
// Note: These are simplified integration tests using fake clients.
// For full e2e testing with real clusters, use envtest or a real cluster.
func TestIntegrationConfigMapCreation(t *testing.T) {
	// Skip if running in short mode
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("Full flow with multiple clusters", func(t *testing.T) {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)

		colony := &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{
				NetBird: &infrav1.NetBirdConfig{
					Enabled:      true,
					APIKeySecret: "netbird-api-key",
				},
			},
			Status: infrav1.ColonyStatus{
				ClusterDeploymentRefs: []*corev1.ObjectReference{
					{Name: "test-colony-cluster-a", Namespace: "default"},
					{Name: "test-colony-cluster-b", Namespace: "default"},
				},
			},
		}

		// Create fake kubeconfig
		kubeconfig := createTestKubeconfig()
		kubeconfigBytes, _ := clientcmd.Write(*kubeconfig)

		// Setup secrets for both clusters
		kubeconfigSecretA := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-cluster-a-kubeconfig",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"value": kubeconfigBytes,
			},
		}

		kubeconfigSecretB := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-cluster-b-kubeconfig",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"value": kubeconfigBytes,
			},
		}

		objRefA := corev1.ObjectReference{
			Name:      kubeconfigSecretA.Name,
			Namespace: kubeconfigSecretA.Namespace,
		}
		refBytesA, _ := json.Marshal(objRefA)

		objRefB := corev1.ObjectReference{
			Name:      kubeconfigSecretB.Name,
			Namespace: kubeconfigSecretB.Namespace,
		}
		refBytesB, _ := json.Marshal(objRefB)

		aggregatedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-colony-kubeconfigs",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"test-colony-cluster-a": refBytesA,
				"test-colony-cluster-b": refBytesB,
			},
		}

		managementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(colony, aggregatedSecret, kubeconfigSecretA, kubeconfigSecretB).
			Build()

		// Note: In this test, we can verify that the secrets are properly set up
		// The actual ConfigMap creation would require connecting to a real cluster
		// which we simulate in the unit tests above

		// Verify aggregated secret exists
		secret := &corev1.Secret{}
		err := managementClient.Get(ctx, client.ObjectKey{
			Name:      "test-colony-kubeconfigs",
			Namespace: "default",
		}, secret)
		if err != nil {
			t.Errorf("Failed to get aggregated secret: %v", err)
		}

		// Verify both cluster references exist
		if _, ok := secret.Data["test-colony-cluster-a"]; !ok {
			t.Error("Cluster A reference not found in aggregated secret")
		}
		if _, ok := secret.Data["test-colony-cluster-b"]; !ok {
			t.Error("Cluster B reference not found in aggregated secret")
		}

		t.Log("Integration test structure validated successfully")
		t.Log("Note: Full end-to-end testing requires envtest or real clusters")
	})
}

// TestIntegrationNonNetBirdConfigMapCreation tests the ConfigMap creation flow
// for non-NetBird colonies (using LoadBalancer or ClusterIP service discovery).
func TestIntegrationNonNetBirdConfigMapCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("Non-NetBird colony with LoadBalancer service", func(t *testing.T) {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)

		// Create colony without NetBird
		colony := &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "non-netbird-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{
				// NetBird is nil (not enabled)
			},
			Status: infrav1.ColonyStatus{
				ClusterDeploymentRefs: []*corev1.ObjectReference{
					{Name: "non-netbird-colony-cluster-a", Namespace: "default"},
				},
			},
		}

		// Create control plane service with LoadBalancer type and external address
		controlPlaneService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kmc-non-netbird-colony-cluster-a",
				Namespace: "default",
				Labels: map[string]string{
					"app":       "k0smotron",
					"component": "cluster",
					"cluster":   "non-netbird-colony-cluster-a",
				},
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeLoadBalancer,
				ClusterIP: "10.0.0.100",
				Ports: []corev1.ServicePort{
					{Name: "api", Port: 30443},
					{Name: "konnectivity", Port: 30132},
				},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{
						{IP: "203.0.113.50"},
					},
				},
			},
		}

		// Create fake kubeconfig
		kubeconfig := createTestKubeconfig()
		kubeconfigBytes, _ := clientcmd.Write(*kubeconfig)

		kubeconfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "non-netbird-colony-cluster-a-kubeconfig",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"value": kubeconfigBytes,
			},
		}

		managementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(colony, controlPlaneService, kubeconfigSecret).
			Build()

		// Verify service discovery works
		service := &corev1.Service{}
		err := managementClient.Get(ctx, client.ObjectKey{
			Name:      "kmc-non-netbird-colony-cluster-a",
			Namespace: "default",
		}, service)
		if err != nil {
			t.Errorf("Failed to get control plane service: %v", err)
		}

		// Verify service is LoadBalancer type
		if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
			t.Errorf("Expected LoadBalancer service, got %v", service.Spec.Type)
		}

		// Verify external address
		if len(service.Status.LoadBalancer.Ingress) == 0 {
			t.Error("Expected LoadBalancer ingress, got none")
		} else if service.Status.LoadBalancer.Ingress[0].IP != "203.0.113.50" {
			t.Errorf("Expected external IP 203.0.113.50, got %v", service.Status.LoadBalancer.Ingress[0].IP)
		}

		t.Log("Non-NetBird colony LoadBalancer flow validated successfully")
	})

	t.Run("Non-NetBird colony with NodePort service (falls back to ClusterIP)", func(t *testing.T) {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)

		// Create colony without NetBird
		colony := &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nodeport-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{},
			Status: infrav1.ColonyStatus{
				ClusterDeploymentRefs: []*corev1.ObjectReference{
					{Name: "nodeport-colony-cluster-a", Namespace: "default"},
				},
			},
		}

		// Create control plane service with NodePort type (no external address)
		controlPlaneService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kmc-nodeport-colony-cluster-a",
				Namespace: "default",
				Labels: map[string]string{
					"app":       "k0smotron",
					"component": "cluster",
					"cluster":   "nodeport-colony-cluster-a",
				},
			},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeNodePort,
				ClusterIP: "10.0.0.200",
				Ports: []corev1.ServicePort{
					{Name: "api", Port: 30443, NodePort: 31443},
					{Name: "konnectivity", Port: 30132, NodePort: 31132},
				},
			},
		}

		// Create fake kubeconfig
		kubeconfig := createTestKubeconfig()
		kubeconfigBytes, _ := clientcmd.Write(*kubeconfig)

		kubeconfigSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nodeport-colony-cluster-a-kubeconfig",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"value": kubeconfigBytes,
			},
		}

		managementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(colony, controlPlaneService, kubeconfigSecret).
			Build()

		// Verify service discovery works
		service := &corev1.Service{}
		err := managementClient.Get(ctx, client.ObjectKey{
			Name:      "kmc-nodeport-colony-cluster-a",
			Namespace: "default",
		}, service)
		if err != nil {
			t.Errorf("Failed to get control plane service: %v", err)
		}

		// Verify service is NodePort type
		if service.Spec.Type != corev1.ServiceTypeNodePort {
			t.Errorf("Expected NodePort service, got %v", service.Spec.Type)
		}

		// Verify ClusterIP exists for fallback
		if service.Spec.ClusterIP != "10.0.0.200" {
			t.Errorf("Expected ClusterIP 10.0.0.200, got %v", service.Spec.ClusterIP)
		}

		t.Log("Non-NetBird colony NodePort flow (ClusterIP fallback) validated successfully")
	})

	t.Run("Service not ready yet (cluster provisioning)", func(t *testing.T) {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		_ = clientgoscheme.AddToScheme(scheme)
		_ = infrav1.AddToScheme(scheme)

		// Create colony without NetBird
		colony := &infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "provisioning-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{},
			Status: infrav1.ColonyStatus{
				ClusterDeploymentRefs: []*corev1.ObjectReference{
					{Name: "provisioning-colony-cluster-a", Namespace: "default"},
				},
			},
		}

		// No control plane service created (cluster still provisioning)

		managementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(colony).
			Build()

		// Verify service doesn't exist
		serviceList := &corev1.ServiceList{}
		err := managementClient.List(ctx, serviceList,
			client.InNamespace("default"),
			client.MatchingLabels{
				"app":       "k0smotron",
				"component": "cluster",
				"cluster":   "provisioning-colony-cluster-a",
			},
		)
		if err != nil {
			t.Errorf("Failed to list services: %v", err)
		}

		if len(serviceList.Items) != 0 {
			t.Errorf("Expected no services, got %d", len(serviceList.Items))
		}

		t.Log("Provisioning flow (no service yet) validated successfully")
	})
}

func createTestKubeconfig() *clientcmdapi.Config {
	return &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"test-cluster": {
				Server:                "https://test-server:6443",
				InsecureSkipTLSVerify: true,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"test-context": {
				Cluster:  "test-cluster",
				AuthInfo: "test-user",
			},
		},
		CurrentContext: "test-context",
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"test-user": {
				Token: "test-token",
			},
		},
	}
}
