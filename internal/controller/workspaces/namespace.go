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

	"github.com/exalsius/exalsius-operator/internal/controller/infra/common"
	"github.com/exalsius/exalsius-operator/internal/controller/workspaces/routing"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// LabelWorkspace marks a namespace as a workspace namespace and identifies
// the workspace it belongs to. Istio mesh discovery (discoverySelectors)
// keys off this label so that ONLY workspace namespaces participate in
// cross-cluster endpoint discovery — prerequisites and system services
// stay cluster-local (ADR-0001). Shared with routing providers.
const LabelWorkspace = routing.LabelWorkspace

// workspaceNamespaceName returns the per-workspace namespace on the child
// cluster. One namespace per workspace — the unit of isolation, mesh
// visibility, and cleanup. Unique across a tenant's entire mesh by
// construction (workspace names are unique per org namespace, and a child
// cluster belongs to exactly one tenant).
func workspaceNamespaceName(wsd *workspacesv1.WorkspaceDeployment) string {
	return routing.WorkspaceNamespaceName(wsd)
}

// getChildClusterClient builds a client for the WSD's target child cluster
// from its `<cd-name>-kubeconfig` secret on the management cluster.
// common.GetRegionalClusterClient is, despite its name, a generic
// "client from a ClusterDeployment's kubeconfig secret" helper.
func getChildClusterClient(
	ctx context.Context,
	managementClient client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
	scheme *runtime.Scheme,
) (client.Client, error) {
	cdRef := wsd.Spec.ClusterDeploymentRef
	return common.GetRegionalClusterClient(ctx, managementClient, cdRef.Namespace, cdRef.Name, scheme)
}

// ensureWorkspaceNamespace pre-creates the labeled workspace namespace on the
// child cluster before the ServiceSet is applied. Pre-creation (rather than
// letting Sveltos create it on Helm install) guarantees the mesh-discovery
// label is present before any workspace Service exists — Sveltos creates
// namespaces unlabeled.
//
// Returns ready=false (without error) when the namespace is currently
// terminating: a same-name predecessor is still cleaning up, and installing
// into it would resurrect stale state (PVCs). The caller requeues until
// termination completes.
// meshLabels are the Istio mesh-enrollment labels (from --workspace-mesh-mode)
// stamped on the workspace namespace alongside LabelWorkspace, so ztunnel
// captures the workspace's pods (ambient) or they get sidecars (sidecar).
func ensureWorkspaceNamespace(
	ctx context.Context,
	childClient client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
	meshLabels map[string]string,
) (bool, error) {
	nsName := workspaceNamespaceName(wsd)
	desired := workspaceNamespaceLabels(wsd.Name, meshLabels)

	ns := &corev1.Namespace{}
	err := childClient.Get(ctx, client.ObjectKey{Name: nsName}, ns)
	if apierrors.IsNotFound(err) {
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   nsName,
				Labels: desired,
			},
		}
		if err := childClient.Create(ctx, ns); err != nil {
			// A concurrent reconcile may have won the race — treat as retryable.
			if apierrors.IsAlreadyExists(err) {
				return false, nil
			}
			return false, err
		}
		log.FromContext(ctx).Info("Created workspace namespace on child cluster", "namespace", nsName)
		return true, nil
	}
	if err != nil {
		return false, err
	}

	if !ns.DeletionTimestamp.IsZero() {
		// Predecessor namespace still terminating — wait for a clean slate.
		return false, nil
	}

	// Namespace exists — heal the workspace + mesh-enrollment labels
	// (manually created or label-stripped namespaces, or a mesh-mode change).
	if applyMissingLabels(ns, desired) {
		if err := childClient.Update(ctx, ns); err != nil {
			return false, err
		}
	}
	return true, nil
}

// workspaceNamespaceLabels builds the full desired label set for a workspace
// namespace: the workspace identity label plus the mesh-enrollment labels.
func workspaceNamespaceLabels(workspaceName string, meshLabels map[string]string) map[string]string {
	labels := map[string]string{LabelWorkspace: workspaceName}
	for k, v := range meshLabels {
		labels[k] = v
	}
	return labels
}

// applyMissingLabels sets any desired label that is absent or has a different
// value on the object, leaving other labels untouched. Returns true if it
// changed anything.
func applyMissingLabels(obj *corev1.Namespace, desired map[string]string) bool {
	changed := false
	if obj.Labels == nil && len(desired) > 0 {
		obj.Labels = map[string]string{}
	}
	for k, v := range desired {
		if obj.Labels[k] != v {
			obj.Labels[k] = v
			changed = true
		}
	}
	return changed
}

// deleteWorkspaceNamespace requests deletion of the workspace namespace on
// the child cluster. Returns done=true once deletion is underway (namespace
// gone or terminating) — kube GC owns the rest, and recreation safety is
// enforced at create time by ensureWorkspaceNamespace, which refuses to
// install into a terminating namespace.
func deleteWorkspaceNamespace(
	ctx context.Context,
	childClient client.Client,
	wsd *workspacesv1.WorkspaceDeployment,
) (bool, error) {
	nsName := workspaceNamespaceName(wsd)

	ns := &corev1.Namespace{}
	err := childClient.Get(ctx, client.ObjectKey{Name: nsName}, ns)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !ns.DeletionTimestamp.IsZero() {
		return true, nil
	}

	if err := childClient.Delete(ctx, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	log.FromContext(ctx).Info("Deleted workspace namespace on child cluster", "namespace", nsName)
	// A successful Delete means deletion is underway — done by our definition.
	return true, nil
}
