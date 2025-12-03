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

	infrastructurev1beta1 "github.com/k0sproject/k0smotron/api/infrastructure/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// NetBirdCleanupFinalizer is added to all RemoteMachines at creation time
	// to ensure NetBird cleanup can run before k0smotron's cleanup.
	// The cleanup controller will determine if cleanup is actually needed.
	NetBirdCleanupFinalizer = "netbird.exalsius.ai/cleanup"
)

// RemoteMachineDefaulter adds the NetBird cleanup finalizer to RemoteMachine objects
// at creation time. This ensures the finalizer is present before k0smotron's controller
// creates its patch helper, preventing race conditions during deletion.
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
	// This prevents the race condition: "no new finalizers can be added if the object is being deleted"
	if !rm.DeletionTimestamp.IsZero() {
		log.V(1).Info("RemoteMachine is being deleted, skipping finalizer addition",
			"remotemachine", rm.Name,
			"namespace", rm.Namespace)
		return nil
	}

	// Only add finalizer if it doesn't already exist
	if !controllerutil.ContainsFinalizer(rm, NetBirdCleanupFinalizer) {
		log.V(1).Info("Adding NetBird cleanup finalizer to RemoteMachine",
			"remotemachine", rm.Name,
			"namespace", rm.Namespace)
		controllerutil.AddFinalizer(rm, NetBirdCleanupFinalizer)
	}

	return nil
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
