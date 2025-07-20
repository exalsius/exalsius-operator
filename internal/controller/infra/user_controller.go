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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	infrav1 "github.com/exalsius/exalsius-operator/api/infra/v1"
)

const (
	userFinalizerName = "user.infra.exalsius.ai/finalizer"
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=users,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=users/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.exalsius.ai,resources=users/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=accessmanagements,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling User", "name", req.Name, "namespace", req.Namespace)

	// Fetch the User instance
	user := &infrav1.User{}
	if err := r.Get(ctx, req.NamespacedName, user); err != nil {
		if errors.IsNotFound(err) {
			// User was deleted, nothing to do
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get User")
		return ctrl.Result{}, err
	}

	// Check if the User is being deleted
	if user.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, user)
	}

	// Add finalizer if not present
	if !containsString(user.ObjectMeta.Finalizers, userFinalizerName) {
		user.ObjectMeta.Finalizers = append(user.ObjectMeta.Finalizers, userFinalizerName)
		if err := r.Update(ctx, user); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Step 1: Check if the namespace exists and create it if not
	if err := r.ensureNamespace(ctx, user.Spec.UserNamespace); err != nil {
		logger.Error(err, "Failed to ensure namespace", "namespace", user.Spec.UserNamespace)
		return ctrl.Result{}, err
	}

	// Step 2: Check AccessManagement and add namespace to targetNamespaces if not present
	if err := r.ensureAccessManagement(ctx, user.Spec.UserNamespace); err != nil {
		logger.Error(err, "Failed to ensure AccessManagement", "namespace", user.Spec.UserNamespace)
		return ctrl.Result{}, err
	}

	// Update User status to indicate it's ready
	user.Status.Ready = true
	if err := r.Status().Update(ctx, user); err != nil {
		logger.Error(err, "Failed to update User status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled User", "name", user.Name, "namespace", user.Spec.UserNamespace)
	return ctrl.Result{}, nil
}

// handleDeletion handles the deletion of a User resource
func (r *UserReconciler) handleDeletion(ctx context.Context, user *infrav1.User) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling User deletion", "name", user.Name)

	// Remove namespace from AccessManagement
	if err := r.removeNamespaceFromAccessManagement(ctx, user.Spec.UserNamespace); err != nil {
		logger.Error(err, "Failed to remove namespace from AccessManagement", "namespace", user.Spec.UserNamespace)
		return ctrl.Result{}, err
	}

	// Remove finalizer
	user.ObjectMeta.Finalizers = removeString(user.ObjectMeta.Finalizers, userFinalizerName)
	if err := r.Update(ctx, user); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully handled User deletion", "name", user.Name)
	return ctrl.Result{}, nil
}

// ensureNamespace checks if the namespace exists and creates it if not
func (r *UserReconciler) ensureNamespace(ctx context.Context, namespaceName string) error {
	logger := log.FromContext(ctx)

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
		},
	}

	// Try to get the namespace
	err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			// Namespace doesn't exist, create it
			logger.Info("Creating namespace", "namespace", namespaceName)
			if err := r.Create(ctx, namespace); err != nil {
				return fmt.Errorf("failed to create namespace %s: %w", namespaceName, err)
			}
			logger.Info("Successfully created namespace", "namespace", namespaceName)
		} else {
			return fmt.Errorf("failed to get namespace %s: %w", namespaceName, err)
		}
	} else {
		logger.Info("Namespace already exists", "namespace", namespaceName)
	}

	return nil
}

// ensureAccessManagement checks the AccessManagement object and adds the namespace to targetNamespaces if not present
func (r *UserReconciler) ensureAccessManagement(ctx context.Context, namespaceName string) error {
	logger := log.FromContext(ctx)

	// Get the AccessManagement object named "kcm"
	accessManagement := &k0rdentv1beta1.AccessManagement{}
	err := r.Get(ctx, client.ObjectKey{Name: "kcm"}, accessManagement)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("AccessManagement 'kcm' not found, skipping namespace addition")
			return nil
		}
		return fmt.Errorf("failed to get AccessManagement 'kcm': %w", err)
	}

	// Check if the namespace is already in targetNamespaces
	namespaceExists := false
	if len(accessManagement.Spec.AccessRules) > 0 {
		for _, namespace := range accessManagement.Spec.AccessRules[0].TargetNamespaces.List {
			if namespace == namespaceName {
				namespaceExists = true
				break
			}
		}
	}

	if !namespaceExists {
		logger.Info("Adding namespace to AccessManagement targetNamespaces", "namespace", namespaceName)

		// Add the namespace to the first access rule's targetNamespaces
		if len(accessManagement.Spec.AccessRules) == 0 {
			// Create a new access rule if none exists
			accessManagement.Spec.AccessRules = []k0rdentv1beta1.AccessRule{
				{
					TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
						List: []string{namespaceName},
					},
				},
			}
		} else {
			// Add to existing access rule
			accessManagement.Spec.AccessRules[0].TargetNamespaces.List = append(
				accessManagement.Spec.AccessRules[0].TargetNamespaces.List,
				namespaceName,
			)
		}

		// Update the AccessManagement object
		if err := r.Update(ctx, accessManagement); err != nil {
			return fmt.Errorf("failed to update AccessManagement: %w", err)
		}

		logger.Info("Successfully added namespace to AccessManagement", "namespace", namespaceName)
	} else {
		logger.Info("Namespace already exists in AccessManagement targetNamespaces", "namespace", namespaceName)
	}

	return nil
}

// removeNamespaceFromAccessManagement removes the namespace from AccessManagement targetNamespaces
func (r *UserReconciler) removeNamespaceFromAccessManagement(ctx context.Context, namespaceName string) error {
	logger := log.FromContext(ctx)

	// Get the AccessManagement object named "kcm"
	accessManagement := &k0rdentv1beta1.AccessManagement{}
	err := r.Get(ctx, client.ObjectKey{Name: "kcm"}, accessManagement)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("AccessManagement 'kcm' not found, nothing to remove")
			return nil
		}
		return fmt.Errorf("failed to get AccessManagement 'kcm': %w", err)
	}

	// Check if the namespace is in targetNamespaces and remove it
	if len(accessManagement.Spec.AccessRules) > 0 {
		namespaces := accessManagement.Spec.AccessRules[0].TargetNamespaces.List
		for i, namespace := range namespaces {
			if namespace == namespaceName {
				// Remove the namespace from the list
				accessManagement.Spec.AccessRules[0].TargetNamespaces.List = append(namespaces[:i], namespaces[i+1:]...)

				// Update the AccessManagement object
				if err := r.Update(ctx, accessManagement); err != nil {
					return fmt.Errorf("failed to update AccessManagement: %w", err)
				}

				logger.Info("Successfully removed namespace from AccessManagement", "namespace", namespaceName)
				return nil
			}
		}
	}

	logger.Info("Namespace not found in AccessManagement targetNamespaces", "namespace", namespaceName)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.User{}).
		Named("infra-user").
		Complete(r)
}

// Helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := []string{}
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}
