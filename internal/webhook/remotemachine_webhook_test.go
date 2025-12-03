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

	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRemoteMachineDefaulter_Default(t *testing.T) {
	tests := []struct {
		name            string
		remoteMachine   *infrastructurev1beta1.RemoteMachine
		expectFinalizer bool
		expectError     bool
	}{
		{
			name: "adds finalizer to new RemoteMachine without finalizers",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: "default",
				},
				Spec: infrastructurev1beta1.RemoteMachineSpec{
					Address: "192.168.1.1",
					Port:    22,
				},
			},
			expectFinalizer: true,
			expectError:     false,
		},
		{
			name: "idempotent - doesn't duplicate finalizer if already present",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{NetBirdCleanupFinalizer},
				},
				Spec: infrastructurev1beta1.RemoteMachineSpec{
					Address: "192.168.1.1",
					Port:    22,
				},
			},
			expectFinalizer: true,
			expectError:     false,
		},
		{
			name: "adds finalizer even when other finalizers exist",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"remotemachine.k0smotron.io/finalizer"},
				},
				Spec: infrastructurev1beta1.RemoteMachineSpec{
					Address: "192.168.1.1",
					Port:    22,
				},
			},
			expectFinalizer: true,
			expectError:     false,
		},
		{
			name: "preserves existing finalizers when adding",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  "default",
					Finalizers: []string{"other-finalizer", "another-finalizer"},
				},
				Spec: infrastructurev1beta1.RemoteMachineSpec{
					Address: "192.168.1.1",
					Port:    22,
				},
			},
			expectFinalizer: true,
			expectError:     false,
		},
		{
			name: "does not add finalizer to object being deleted",
			remoteMachine: &infrastructurev1beta1.RemoteMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-machine",
					Namespace:         "default",
					DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
					Finalizers:        []string{"other-finalizer"}, // Must have at least one to be valid for deletion
				},
				Spec: infrastructurev1beta1.RemoteMachineSpec{
					Address: "192.168.1.1",
					Port:    22,
				},
			},
			expectFinalizer: false,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client
			fakeClient := fake.NewClientBuilder().Build()

			webhook := &RemoteMachineDefaulter{
				Client: fakeClient,
			}

			// Store original finalizers for comparison
			originalFinalizers := make([]string, len(tt.remoteMachine.Finalizers))
			copy(originalFinalizers, tt.remoteMachine.Finalizers)

			// Call the webhook
			err := webhook.Default(context.Background(), tt.remoteMachine)

			// Verify error expectation
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify finalizer expectation
			if tt.expectFinalizer {
				assert.Contains(t, tt.remoteMachine.Finalizers, NetBirdCleanupFinalizer,
					"NetBird cleanup finalizer should be present")

				// Verify we didn't duplicate the finalizer
				finalizerCount := 0
				for _, f := range tt.remoteMachine.Finalizers {
					if f == NetBirdCleanupFinalizer {
						finalizerCount++
					}
				}
				assert.Equal(t, 1, finalizerCount,
					"NetBird cleanup finalizer should appear exactly once")

				// Verify other finalizers are preserved
				for _, originalFinalizer := range originalFinalizers {
					if originalFinalizer != NetBirdCleanupFinalizer {
						assert.Contains(t, tt.remoteMachine.Finalizers, originalFinalizer,
							"Original finalizer %s should be preserved", originalFinalizer)
					}
				}
			} else {
				assert.NotContains(t, tt.remoteMachine.Finalizers, NetBirdCleanupFinalizer,
					"NetBird cleanup finalizer should not be present")
			}
		})
	}
}

func TestRemoteMachineDefaulter_FinalizerValue(t *testing.T) {
	// Verify the finalizer constant is correct
	assert.Equal(t, "netbird.exalsius.ai/cleanup", NetBirdCleanupFinalizer,
		"Finalizer value should match expected format")
}
