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

package webhook

import (
	"context"
	"testing"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = infrastructurev1beta1.AddToScheme(scheme)
	_ = k0rdentv1beta1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)
	return scheme
}

func TestRemoteMachineDefaulter_Default_WithNetBirdEnabled(t *testing.T) {
	scheme := newScheme()

	// Create Colony with NetBird enabled
	colony := &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-colony",
			Namespace: "default",
		},
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: true,
			},
		},
	}

	// Create ClusterDeployment owned by Colony
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "infra.exalsius.ai/v1",
					Kind:       "Colony",
					Name:       "test-colony",
				},
			},
		},
	}

	// Create RemoteMachine with cluster label
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "test-cluster",
			},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(colony, clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.Contains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should be added when NetBird is enabled")
}

func TestRemoteMachineDefaulter_Default_WithNetBirdDisabled(t *testing.T) {
	scheme := newScheme()

	// Create Colony with NetBird disabled
	colony := &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-colony",
			Namespace: "default",
		},
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: false,
			},
		},
	}

	// Create ClusterDeployment owned by Colony
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "infra.exalsius.ai/v1",
					Kind:       "Colony",
					Name:       "test-colony",
				},
			},
		},
	}

	// Create RemoteMachine with cluster label
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "test-cluster",
			},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(colony, clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added when NetBird is disabled")
}

func TestRemoteMachineDefaulter_Default_WithNoNetBirdConfig(t *testing.T) {
	scheme := newScheme()

	// Create Colony without NetBird config
	colony := &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-colony",
			Namespace: "default",
		},
		Spec: infrav1.ColonySpec{
			// No NetBird config
		},
	}

	// Create ClusterDeployment owned by Colony
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "infra.exalsius.ai/v1",
					Kind:       "Colony",
					Name:       "test-colony",
				},
			},
		},
	}

	// Create RemoteMachine with cluster label
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "test-cluster",
			},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(colony, clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added when no NetBird config exists")
}

func TestRemoteMachineDefaulter_Default_WithNoColonyOwner(t *testing.T) {
	scheme := newScheme()

	// Create ClusterDeployment without Colony owner (standalone cluster)
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "standalone-cluster",
			Namespace: "default",
			// No owner references
		},
	}

	// Create RemoteMachine with cluster label
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "standalone-cluster",
			},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added for standalone clusters without Colony owner")
}

func TestRemoteMachineDefaulter_Default_WithNoClusterLabel(t *testing.T) {
	scheme := newScheme()

	// Create RemoteMachine without cluster label
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			// No cluster label
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added when cluster label is missing")
}

func TestRemoteMachineDefaulter_Default_WithMissingClusterDeployment(t *testing.T) {
	scheme := newScheme()

	// Create RemoteMachine pointing to non-existent ClusterDeployment
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "non-existent-cluster",
			},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added when ClusterDeployment doesn't exist")
}

func TestRemoteMachineDefaulter_Default_Idempotent(t *testing.T) {
	scheme := newScheme()

	// Create Colony with NetBird enabled
	colony := &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-colony",
			Namespace: "default",
		},
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: true,
			},
		},
	}

	// Create ClusterDeployment owned by Colony
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "infra.exalsius.ai/v1",
					Kind:       "Colony",
					Name:       "test-colony",
				},
			},
		},
	}

	// Create RemoteMachine that already has the finalizer
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				ClusterNameLabel: "test-cluster",
			},
			Finalizers: []string{NetBirdCleanupFinalizer},
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(colony, clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	// Count finalizers
	finalizerCount := 0
	for _, f := range remoteMachine.Finalizers {
		if f == NetBirdCleanupFinalizer {
			finalizerCount++
		}
	}
	assert.Equal(t, 1, finalizerCount, "Finalizer should appear exactly once (idempotent)")
}

func TestRemoteMachineDefaulter_Default_SkipsDeletingObjects(t *testing.T) {
	scheme := newScheme()

	// Create Colony with NetBird enabled
	colony := &infrav1.Colony{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-colony",
			Namespace: "default",
		},
		Spec: infrav1.ColonySpec{
			NetBird: &infrav1.NetBirdConfig{
				Enabled: true,
			},
		},
	}

	// Create ClusterDeployment owned by Colony
	clusterDeployment := &k0rdentv1beta1.ClusterDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "infra.exalsius.ai/v1",
					Kind:       "Colony",
					Name:       "test-colony",
				},
			},
		},
	}

	// Create RemoteMachine that is being deleted
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-machine",
			Namespace:         "default",
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
			Labels: map[string]string{
				ClusterNameLabel: "test-cluster",
			},
			Finalizers: []string{"other-finalizer"}, // Must have at least one for valid deletion
		},
		Spec: infrastructurev1beta1.RemoteMachineSpec{
			Address: "192.168.1.1",
			Port:    22,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(colony, clusterDeployment).
		Build()

	webhook := &RemoteMachineDefaulter{Client: fakeClient}

	err := webhook.Default(context.Background(), remoteMachine)
	require.NoError(t, err)

	assert.NotContains(t, remoteMachine.Finalizers, NetBirdCleanupFinalizer,
		"NetBird cleanup finalizer should NOT be added to objects being deleted")
}

func TestRemoteMachineDefaulter_FinalizerValue(t *testing.T) {
	// Verify the finalizer constant is correct
	assert.Equal(t, "netbird.exalsius.ai/cleanup", NetBirdCleanupFinalizer,
		"Finalizer value should match expected format")
}
