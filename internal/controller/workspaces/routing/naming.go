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

package routing

import (
	"errors"
	"fmt"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

const (
	// LabelWorkspace marks an object (namespace, Service, route) as belonging
	// to a workspace and identifies which one. On child clusters, Istio mesh
	// discovery (discoverySelectors) keys off this label; on regional
	// clusters it identifies provider-created route objects for cleanup.
	LabelWorkspace = "workspaces.exalsius.ai/workspace"

	// workspaceNamespacePrefix prefixes the per-workspace namespace.
	// Workspace names are capped at 60 chars via CEL so the prefixed name
	// stays a valid DNS-1123 namespace name.
	workspaceNamespacePrefix = "ws-"
)

// MeshMode selects how workspace namespaces are enrolled into the Istio mesh.
// The operator creates the workspace namespaces (child) and mirror namespaces
// (regional), so it stamps the enrollment label; the value depends on which
// data-plane mode the mesh runs (ADR-0001).
type MeshMode string

const (
	// MeshModeAmbient enrolls namespaces into the Istio ambient data plane
	// (ztunnel captures their pods — no sidecar). The default.
	MeshModeAmbient MeshMode = "ambient"
	// MeshModeSidecar enrolls namespaces for sidecar injection.
	MeshModeSidecar MeshMode = "sidecar"
	// MeshModeNone stamps no enrollment label — the mesh enrolls namespaces
	// some other way (or routing is being tested without a mesh).
	MeshModeNone MeshMode = "none"

	labelIstioDataplaneMode = "istio.io/dataplane-mode"
	labelIstioInjection     = "istio-injection"
)

// MeshNamespaceLabels returns the mesh-enrollment labels to stamp on
// workspace namespaces for the given mode. Empty for MeshModeNone (or an
// unknown mode, treated as none).
func MeshNamespaceLabels(mode MeshMode) map[string]string {
	switch mode {
	case MeshModeAmbient:
		return map[string]string{labelIstioDataplaneMode: "ambient"}
	case MeshModeSidecar:
		return map[string]string{labelIstioInjection: "enabled"}
	default:
		return nil
	}
}

// WorkspaceNamespaceName returns the per-workspace namespace. It is the same
// name on the child cluster (where the workload runs) and on the regional
// cluster (where the mirror Service lives) — Istio multi-cluster endpoint
// discovery requires namespace sameness (ADR-0001).
func WorkspaceNamespaceName(wsd *workspacesv1.WorkspaceDeployment) string {
	return workspaceNamespacePrefix + wsd.Name
}

// HelmReleaseName returns the workspace's Helm release name (the ServiceSet
// service entry name). Chart Services follow the convention
// `<release-name>-<endpoint-name>`.
func HelmReleaseName(wsd *workspacesv1.WorkspaceDeployment) string {
	return fmt.Sprintf("wsd-%s-%s", wsd.Spec.ClusterDeploymentRef.Name, wsd.Name)
}

// EndpointServiceName returns the child-cluster Service name for an access
// endpoint: the explicit per-endpoint serviceName when the class declares
// one (fixed-name/umbrella charts), otherwise the
// `<release-name>-<endpoint-name>` convention.
func EndpointServiceName(wsd *workspacesv1.WorkspaceDeployment, ep workspacesv1.AccessEndpoint) string {
	if ep.ServiceName != "" {
		return ep.ServiceName
	}
	return HelmReleaseName(wsd) + "-" + ep.Name
}

// InfraNotReadyError signals that the tenant's routing infrastructure
// (regional cluster topology, gateway, listeners) is missing or not ready —
// an admin-fixable condition, not a transient provider error. The reconciler
// surfaces it as a RoutesReady=False condition with reason
// RoutingInfraNotReady and retries.
type InfraNotReadyError struct {
	Reason string
}

func (e *InfraNotReadyError) Error() string {
	return e.Reason
}

// IsInfraNotReady reports whether err is (or wraps) an InfraNotReadyError.
func IsInfraNotReady(err error) bool {
	var infraErr *InfraNotReadyError
	return errors.As(err, &infraErr)
}
