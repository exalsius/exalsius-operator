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

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// NetBirdCleanupFinalizer is added to RemoteMachines that belong to
	// NetBird-enabled Colonies to ensure NetBird cleanup runs before k0smotron's cleanup.
	NetBirdCleanupFinalizer = "netbird.exalsius.ai/cleanup"

	// ClusterNameLabel is the label used to identify which cluster a RemoteMachine belongs to
	ClusterNameLabel = "cluster.x-k8s.io/cluster-name"
)

// RemoteMachineDefaulter adds the NetBird cleanup finalizer to RemoteMachine objects
// at creation time, but only if the RemoteMachine belongs to a Colony with NetBird enabled.
// This ensures the finalizer is present before k0smotron's controller creates its patch helper,
// preventing race conditions during deletion.
// +kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-remotemachine,mutating=true,failurePolicy=fail,sideEffects=None,groups=infrastructure.cluster.x-k8s.io,resources=remotemachines,verbs=create,versions=v1beta1,name=mremotemachine.kb.io,admissionReviewVersions=v1

type RemoteMachineDefaulter struct {
	client.Client
}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *RemoteMachineDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	rm, ok := obj.(*infrastructurev1beta1.RemoteMachine)
	if !ok {
		return nil
	}

	log := log.FromContext(ctx)

	// Don't add finalizers to objects being deleted
	if !rm.DeletionTimestamp.IsZero() {
		log.V(1).Info("RemoteMachine is being deleted, skipping finalizer addition",
			"remotemachine", rm.Name,
			"namespace", rm.Namespace)
		return nil
	}

	// Only add finalizer if it doesn't already exist
	if controllerutil.ContainsFinalizer(rm, NetBirdCleanupFinalizer) {
		return nil
	}

	// Check if NetBird is enabled for this RemoteMachine's Colony
	netbirdEnabled, err := r.isNetBirdEnabledForRemoteMachine(ctx, rm)
	if err != nil {
		// Log but don't fail the webhook - better to allow creation than block it
		log.V(1).Info("Could not determine if NetBird is enabled, skipping finalizer",
			"remotemachine", rm.Name,
			"namespace", rm.Namespace,
			"error", err.Error())
		return nil
	}

	if !netbirdEnabled {
		log.V(1).Info("NetBird not enabled for this RemoteMachine's Colony, skipping finalizer",
			"remotemachine", rm.Name,
			"namespace", rm.Namespace)
		return nil
	}

	log.V(1).Info("Adding NetBird cleanup finalizer to RemoteMachine",
		"remotemachine", rm.Name,
		"namespace", rm.Namespace)
	controllerutil.AddFinalizer(rm, NetBirdCleanupFinalizer)

	return nil
}

// isNetBirdEnabledForRemoteMachine checks if the RemoteMachine belongs to a Colony
// with NetBird enabled by following the chain:
// RemoteMachine -> ClusterDeployment (via cluster label) -> Colony (via owner reference)
func (r *RemoteMachineDefaulter) isNetBirdEnabledForRemoteMachine(ctx context.Context, rm *infrastructurev1beta1.RemoteMachine) (bool, error) {
	log := log.FromContext(ctx)

	// Get the cluster name from labels
	clusterName, ok := rm.Labels[ClusterNameLabel]
	if !ok {
		log.V(1).Info("RemoteMachine has no cluster label",
			"remotemachine", rm.Name)
		return false, nil
	}

	// Get the ClusterDeployment
	cd := &k0rdentv1beta1.ClusterDeployment{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      clusterName,
		Namespace: rm.Namespace,
	}, cd); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return false, err
		}
		log.V(1).Info("ClusterDeployment not found",
			"remotemachine", rm.Name,
			"cluster", clusterName)
		return false, nil
	}

	// Get the Colony from ClusterDeployment owner references
	for _, owner := range cd.OwnerReferences {
		if owner.Kind == "Colony" {
			colony := &infrav1.Colony{}
			if err := r.Get(ctx, types.NamespacedName{
				Name:      owner.Name,
				Namespace: rm.Namespace,
			}, colony); err != nil {
				if client.IgnoreNotFound(err) != nil {
					return false, err
				}
				log.V(1).Info("Colony not found",
					"remotemachine", rm.Name,
					"colony", owner.Name)
				return false, nil
			}

			// Check if NetBird is enabled in the Colony
			if colony.Spec.NetBird != nil && colony.Spec.NetBird.Enabled {
				log.V(1).Info("Colony has NetBird enabled",
					"remotemachine", rm.Name,
					"colony", colony.Name)
				return true, nil
			}

			log.V(1).Info("Colony does not have NetBird enabled",
				"remotemachine", rm.Name,
				"colony", colony.Name)
			return false, nil
		}
	}

	// No Colony owner found - this RemoteMachine is not managed by a Colony
	log.V(1).Info("No Colony owner found for ClusterDeployment",
		"remotemachine", rm.Name,
		"cluster", clusterName)
	return false, nil
}

// SetupWebhookWithManager registers the webhook with the manager
func (r *RemoteMachineDefaulter) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&infrastructurev1beta1.RemoteMachine{}).
		WithDefaulter(r).
		Complete()
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=remotemachines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=clusterdeployments,verbs=get;list
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=colonies,verbs=get;list
