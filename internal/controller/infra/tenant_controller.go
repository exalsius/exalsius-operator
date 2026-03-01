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

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	capsulev1beta2 "github.com/projectcapsule/capsule/api/v1beta2"
)

const (
	tenantFinalizerName = "tenant.infra.exalsius.ai/finalizer"
	capsuleTenantLabel  = "capsule.clastix.io/tenant"
)

// TenantReconciler reconciles Capsule Tenant objects and bridges them to K0rdent AccessManagement.
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=capsule.clastix.io,resources=tenants,verbs=get;list;watch
// +kubebuilder:rbac:groups=capsule.clastix.io,resources=tenants/status,verbs=get
// +kubebuilder:rbac:groups=capsule.clastix.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=accessmanagements,verbs=get;list;watch;update;patch

func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Tenant", "name", req.Name)

	tenant := &capsulev1beta2.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if tenant.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, tenant)
	}

	if !controllerutil.ContainsFinalizer(tenant, tenantFinalizerName) {
		controllerutil.AddFinalizer(tenant, tenantFinalizerName)
		if err := r.Update(ctx, tenant); err != nil {
			logger.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.ensureAccessRule(ctx, tenant.Name); err != nil {
		logger.Error(err, "Failed to ensure AccessRule for tenant", "tenant", tenant.Name)
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled Tenant", "name", tenant.Name)
	return ctrl.Result{}, nil
}

// handleDeletion removes the tenant's AccessRule from AccessManagement and removes the finalizer.
func (r *TenantReconciler) handleDeletion(ctx context.Context, tenant *capsulev1beta2.Tenant) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling Tenant deletion", "name", tenant.Name)

	if err := r.removeAccessRule(ctx, tenant.Name); err != nil {
		logger.Error(err, "Failed to remove AccessRule for tenant", "tenant", tenant.Name)
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(tenant, tenantFinalizerName)
	if err := r.Update(ctx, tenant); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully handled Tenant deletion", "name", tenant.Name)
	return ctrl.Result{}, nil
}

// ensureAccessRule ensures that the AccessManagement "kcm" has an AccessRule for this tenant
// with a label selector targeting the tenant's namespaces.
func (r *TenantReconciler) ensureAccessRule(ctx context.Context, tenantName string) error {
	logger := log.FromContext(ctx)

	accessManagement := &k0rdentv1beta1.AccessManagement{}
	if err := r.Get(ctx, client.ObjectKey{Name: "kcm"}, accessManagement); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("AccessManagement 'kcm' not found, skipping AccessRule creation")
			return nil
		}
		return fmt.Errorf("failed to get AccessManagement 'kcm': %w", err)
	}

	// Check if an AccessRule for this tenant already exists
	if r.findAccessRuleIndex(accessManagement, tenantName) >= 0 {
		logger.Info("AccessRule already exists for tenant", "tenant", tenantName)
		return nil
	}

	// Build the new AccessRule with label selector
	newRule := k0rdentv1beta1.AccessRule{
		TargetNamespaces: k0rdentv1beta1.TargetNamespaces{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					capsuleTenantLabel: tenantName,
				},
			},
		},
	}

	// Copy templates and credentials from the first existing rule (the "default" rule)
	if len(accessManagement.Spec.AccessRules) > 0 {
		defaultRule := accessManagement.Spec.AccessRules[0]
		newRule.ClusterTemplateChains = defaultRule.ClusterTemplateChains
		newRule.ServiceTemplateChains = defaultRule.ServiceTemplateChains
		newRule.Credentials = defaultRule.Credentials
	}

	accessManagement.Spec.AccessRules = append(accessManagement.Spec.AccessRules, newRule)

	if err := r.Update(ctx, accessManagement); err != nil {
		return fmt.Errorf("failed to update AccessManagement with new AccessRule: %w", err)
	}

	logger.Info("Successfully added AccessRule for tenant", "tenant", tenantName)
	return nil
}

// removeAccessRule removes the AccessRule for the given tenant from AccessManagement.
func (r *TenantReconciler) removeAccessRule(ctx context.Context, tenantName string) error {
	logger := log.FromContext(ctx)

	accessManagement := &k0rdentv1beta1.AccessManagement{}
	if err := r.Get(ctx, client.ObjectKey{Name: "kcm"}, accessManagement); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("AccessManagement 'kcm' not found, nothing to remove")
			return nil
		}
		return fmt.Errorf("failed to get AccessManagement 'kcm': %w", err)
	}

	idx := r.findAccessRuleIndex(accessManagement, tenantName)
	if idx < 0 {
		logger.Info("AccessRule not found for tenant, nothing to remove", "tenant", tenantName)
		return nil
	}

	// Remove the rule at index
	accessManagement.Spec.AccessRules = append(
		accessManagement.Spec.AccessRules[:idx],
		accessManagement.Spec.AccessRules[idx+1:]...,
	)

	if err := r.Update(ctx, accessManagement); err != nil {
		return fmt.Errorf("failed to update AccessManagement: %w", err)
	}

	logger.Info("Successfully removed AccessRule for tenant", "tenant", tenantName)
	return nil
}

// findAccessRuleIndex finds the index of the AccessRule for the given tenant by scanning
// for a matching label selector. Returns -1 if not found.
func (r *TenantReconciler) findAccessRuleIndex(am *k0rdentv1beta1.AccessManagement, tenantName string) int {
	for i, rule := range am.Spec.AccessRules {
		if rule.TargetNamespaces.Selector != nil {
			if val, ok := rule.TargetNamespaces.Selector.MatchLabels[capsuleTenantLabel]; ok && val == tenantName {
				return i
			}
		}
	}
	return -1
}

// SetupWithManager sets up the controller with the Manager.
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&capsulev1beta2.Tenant{}).
		Named("infra-tenant").
		Complete(r)
}
