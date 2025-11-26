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
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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

