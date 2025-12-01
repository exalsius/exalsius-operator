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

package infra

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

func TestRemoteMachineCleanupReconciler_remoteMachineNeedsNetBirdCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrastructurev1beta1.AddToScheme(scheme)
	_ = k0rdentv1beta1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	tests := []struct {
		name          string
		remoteMachine *infrastructurev1beta1.RemoteMachine
		objects       []runtime.Object
		expectCleanup bool
		expectError   bool
	}{
		{
			name: "cleanup needed when Colony has NetBird enabled",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects: []runtime.Object{
				&k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "infra.exalsius.ai/v1",
								Kind:       "Colony",
								Name:       "test-colony",
								UID:        "test-uid",
							},
						},
					},
				},
				&infrav1.Colony{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-colony",
						Namespace: "default",
					},
					Spec: infrav1.ColonySpec{
						NetBird: &infrav1.NetBirdConfig{
							Enabled: true,
						},
					},
				},
			},
			expectCleanup: true,
			expectError:   false,
		},
		{
			name: "cleanup skipped when Colony has NetBird disabled",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects: []runtime.Object{
				&k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "infra.exalsius.ai/v1",
								Kind:       "Colony",
								Name:       "test-colony",
								UID:        "test-uid",
							},
						},
					},
				},
				&infrav1.Colony{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-colony",
						Namespace: "default",
					},
					Spec: infrav1.ColonySpec{
						NetBird: &infrav1.NetBirdConfig{
							Enabled: false,
						},
					},
				},
			},
			expectCleanup: false,
			expectError:   false,
		},
		{
			name: "cleanup skipped when Colony has nil NetBird config",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects: []runtime.Object{
				&k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "infra.exalsius.ai/v1",
								Kind:       "Colony",
								Name:       "test-colony",
								UID:        "test-uid",
							},
						},
					},
				},
				&infrav1.Colony{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-colony",
						Namespace: "default",
					},
					Spec: infrav1.ColonySpec{
						NetBird: nil,
					},
				},
			},
			expectCleanup: false,
			expectError:   false,
		},
		{
			name: "cleanup skipped when ClusterDeployment has no Colony owner",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects: []runtime.Object{
				&k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "default",
						// No owner references
					},
				},
			},
			expectCleanup: false,
			expectError:   false,
		},
		{
			name: "cleanup skipped when ClusterDeployment not found",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects:       []runtime.Object{},
			expectCleanup: false,
			expectError:   false,
		},
		{
			name: "cleanup skipped when RemoteMachine has no cluster label",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					// No labels
				},
			},
			objects:       []runtime.Object{},
			expectCleanup: false,
			expectError:   false,
		},
		{
			name: "cleanup skipped when Colony not found",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
			},
			objects: []runtime.Object{
				&k0rdentv1beta1.ClusterDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "infra.exalsius.ai/v1",
								Kind:       "Colony",
								Name:       "test-colony",
								UID:        "test-uid",
							},
						},
					},
				},
				// Colony doesn't exist
			},
			expectCleanup: false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tt.objects...).
				Build()

			reconciler := &RemoteMachineCleanupReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			needsCleanup, err := reconciler.remoteMachineNeedsNetBirdCleanup(context.Background(), tt.remoteMachine)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectCleanup, needsCleanup,
				"cleanup expectation mismatch")
		})
	}
}

func TestRemoteMachineCleanupReconciler_remoteMachineNeedsNetBirdCleanup_MultipleColonies(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrastructurev1beta1.AddToScheme(scheme)
	_ = k0rdentv1beta1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	// Test that it uses the first Colony owner reference
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
	}

	objects := []runtime.Object{
		&k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "infra.exalsius.ai/v1",
						Kind:       "Colony",
						Name:       "first-colony",
						UID:        "first-uid",
					},
					{
						APIVersion: "infra.exalsius.ai/v1",
						Kind:       "Colony",
						Name:       "second-colony",
						UID:        "second-uid",
					},
				},
			},
		},
		&infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "first-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{
				NetBird: &infrav1.NetBirdConfig{
					Enabled: true,
				},
			},
		},
		&infrav1.Colony{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "second-colony",
				Namespace: "default",
			},
			Spec: infrav1.ColonySpec{
				NetBird: &infrav1.NetBirdConfig{
					Enabled: false,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		Build()

	reconciler := &RemoteMachineCleanupReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	needsCleanup, err := reconciler.remoteMachineNeedsNetBirdCleanup(context.Background(), remoteMachine)

	require.NoError(t, err)
	// Should use first Colony owner, which has NetBird enabled
	assert.True(t, needsCleanup)
}

func TestRemoteMachineCleanupReconciler_remoteMachineNeedsNetBirdCleanup_NonColonyOwner(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = infrastructurev1beta1.AddToScheme(scheme)
	_ = k0rdentv1beta1.AddToScheme(scheme)
	_ = infrav1.AddToScheme(scheme)

	// ClusterDeployment with non-Colony owner should skip cleanup
	remoteMachine := &infrastructurev1beta1.RemoteMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
	}

	objects := []runtime.Object{
		&k0rdentv1beta1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "v1",
						Kind:       "Namespace", // Not a Colony
						Name:       "some-namespace",
						UID:        "some-uid",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		Build()

	reconciler := &RemoteMachineCleanupReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	needsCleanup, err := reconciler.remoteMachineNeedsNetBirdCleanup(context.Background(), remoteMachine)

	require.NoError(t, err)
	assert.False(t, needsCleanup, "should skip cleanup when owner is not a Colony")
}

func TestNetBirdCleanupFinalizerConstant(t *testing.T) {
	// Verify the finalizer constant matches expected value
	assert.Equal(t, "netbird.exalsius.ai/cleanup", NetBirdCleanupFinalizer,
		"Finalizer constant should match expected value")
}
