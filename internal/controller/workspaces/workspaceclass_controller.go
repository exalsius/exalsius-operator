/*
Copyright 2025 Exalsius contributors.

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

package workspaces

import (
	"context"
	"fmt"

	k0rdentv1beta1 "github.com/K0rdent/kcm/api/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

const (
	// systemNamespace is the default namespace used to look up ServiceTemplates
	// referenced by a WorkspaceClass when spec.serviceTemplate.namespace is empty.
	systemNamespace = "kcm-system"

	// sourceNotValidMessage is set on ValidationError when the referenced
	// ServiceTemplate exists but reports valid=false with no message of its own.
	sourceNotValidMessage = "ServiceTemplate is not valid"
)

// WorkspaceClassReconciler reconciles a WorkspaceClass by reflecting the
// referenced ServiceTemplate's availability into status.valid /
// status.validationError.
type WorkspaceClassReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspaceclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=workspaces.exalsius.ai,resources=workspaceclasses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=k0rdent.mirantis.com,resources=servicetemplates,verbs=get;list;watch

func (r *WorkspaceClassReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	wsc := &workspacesv1.WorkspaceClass{}
	if err := r.Get(ctx, req.NamespacedName, wsc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	valid, validationError := r.evaluate(ctx, wsc)

	if wsc.Status.Valid == valid &&
		wsc.Status.ValidationError == validationError &&
		wsc.Status.ObservedGeneration == wsc.Generation {
		return ctrl.Result{}, nil
	}

	wsc.Status.Valid = valid
	wsc.Status.ValidationError = validationError
	wsc.Status.ObservedGeneration = wsc.Generation

	if err := r.Status().Update(ctx, wsc); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update WorkspaceClass status: %w", err)
	}

	log.Info("Updated WorkspaceClass validity",
		"valid", valid,
		"validationError", validationError)

	return ctrl.Result{}, nil
}

// evaluate resolves the referenced ServiceTemplate and returns the validity
// state to record on the WorkspaceClass status.
func (r *WorkspaceClassReconciler) evaluate(
	ctx context.Context, wsc *workspacesv1.WorkspaceClass,
) (bool, string) {
	stRef := wsc.Spec.ServiceTemplate
	stNs := stRef.Namespace
	if stNs == "" {
		stNs = systemNamespace
	}

	st := &k0rdentv1beta1.ServiceTemplate{}
	err := r.Get(ctx, types.NamespacedName{Name: stRef.Name, Namespace: stNs}, st)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, fmt.Sprintf("ServiceTemplate %s/%s not found", stNs, stRef.Name)
		}
		return false, fmt.Sprintf("failed to get ServiceTemplate %s/%s: %v", stNs, stRef.Name, err)
	}

	if !st.Status.Valid {
		msg := st.Status.ValidationError
		if msg == "" {
			msg = sourceNotValidMessage
		}
		return false, msg
	}

	// Validate resourceInjection paths if set. Surfaces parse errors as
	// catalog-level invalidity so the API can filter out unusable classes
	// before users try to deploy.
	if err := validateInjectionPaths(wsc.Spec.ResourceInjection); err != nil {
		return false, err.Error()
	}

	return true, ""
}

// workspaceClassesForServiceTemplate maps a ServiceTemplate change to the
// WorkspaceClasses that reference it, so a flip of the ST's validity wakes
// the corresponding WC reconciles.
func (r *WorkspaceClassReconciler) workspaceClassesForServiceTemplate(
	ctx context.Context, obj client.Object,
) []reconcile.Request {
	st, ok := obj.(*k0rdentv1beta1.ServiceTemplate)
	if !ok {
		return nil
	}

	var classes workspacesv1.WorkspaceClassList
	if err := r.List(ctx, &classes); err != nil {
		log.FromContext(ctx).Error(err,
			"failed to list WorkspaceClasses for ServiceTemplate watch",
			"serviceTemplate", client.ObjectKeyFromObject(st))
		return nil
	}

	var requests []reconcile.Request
	for _, wc := range classes.Items {
		ns := wc.Spec.ServiceTemplate.Namespace
		if ns == "" {
			ns = systemNamespace
		}
		if wc.Spec.ServiceTemplate.Name == st.Name && ns == st.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: wc.Name},
			})
		}
	}
	return requests
}

func (r *WorkspaceClassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&workspacesv1.WorkspaceClass{}).
		Watches(
			&k0rdentv1beta1.ServiceTemplate{},
			handler.EnqueueRequestsFromMapFunc(r.workspaceClassesForServiceTemplate),
		).
		Named("workspace-class").
		Complete(r)
}
