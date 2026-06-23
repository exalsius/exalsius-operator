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
	"crypto/sha256"
	"encoding/hex"
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

	// Waypoint-routing labels (ambient). They route workspace traffic through
	// a shared waypoint so the north-south ingress gateway can reach ambient
	// "global" (cross-cluster) services — without them the gateway resolves
	// zero endpoints for a global service and returns 503.
	labelIstioUseWaypoint          = "istio.io/use-waypoint"
	labelIstioUseWaypointNamespace = "istio.io/use-waypoint-namespace"
	labelIstioIngressUseWaypoint   = "istio.io/ingress-use-waypoint"
)

// WaypointConfig points workspace namespaces at a shared Istio ambient
// waypoint. An empty Name disables waypoint enrollment (the labels are
// omitted).
type WaypointConfig struct {
	Name      string
	Namespace string
}

// waypointNameSuffix is appended to a ClusterDeployment name to form its
// per-child waypoint name (ADR-0005).
const waypointNameSuffix = "-waypoint"

// maxDNS1123Label is the Kubernetes object-name / DNS-1123 label limit.
const maxDNS1123Label = 63

// WaypointNameForClusterDeployment returns the name of the per-child waypoint
// for the given ClusterDeployment (ADR-0005). Each child cluster has a
// dedicated waypoint named `<cd-name>-waypoint`, present on both the regional
// cluster and that child; the waypoint name scopes the global service's
// cross-cluster endpoints to the one hosting child (no fan-out, no relay).
//
// The operator and onboarding must agree on this name without coordination, so
// it is a pure function of the CD name. If `<cd-name>-waypoint` would exceed
// the 63-char object-name limit, the CD name is truncated and a short,
// deterministic hash of the full name is inserted to keep it unique.
func WaypointNameForClusterDeployment(cdName string) string {
	name := cdName + waypointNameSuffix
	if len(name) <= maxDNS1123Label {
		return name
	}
	sum := sha256.Sum256([]byte(cdName))
	h := hex.EncodeToString(sum[:])[:8]
	// budget: maxDNS1123Label - len("-"+hash) - len(suffix)
	keep := maxDNS1123Label - (1 + len(h)) - len(waypointNameSuffix)
	return cdName[:keep] + "-" + h + waypointNameSuffix
}

// MeshConfig captures how the operator enrolls workspace namespaces into the
// Istio mesh, resolved from manager flags. Unlike a fixed label set, the
// waypoint label is per-hosting-child (ADR-0005): NamespaceLabels derives the
// `<cd-name>-waypoint` value for a specific ClusterDeployment.
type MeshConfig struct {
	// Mode is the data-plane enrollment mode (ambient/sidecar/none).
	Mode MeshMode
	// WaypointEnabled turns on per-child waypoint routing (ambient only). When
	// false, namespaces are enrolled without a waypoint label.
	WaypointEnabled bool
	// WaypointNamespace is where the per-child waypoint Gateways live (e.g.
	// istio-system) on both the regional and child clusters.
	WaypointNamespace string
}

// NamespaceLabels returns the mesh-enrollment labels to stamp on a workspace's
// namespaces for the workspace hosted on the given ClusterDeployment. In
// ambient mode with waypoints enabled, the waypoint label points at that
// child's dedicated `<cd-name>-waypoint`.
func (c MeshConfig) NamespaceLabels(cdName string) map[string]string {
	wp := WaypointConfig{}
	if c.WaypointEnabled {
		wp = WaypointConfig{
			Name:      WaypointNameForClusterDeployment(cdName),
			Namespace: c.WaypointNamespace,
		}
	}
	return MeshNamespaceLabels(c.Mode, wp)
}

// MeshNamespaceLabels returns the mesh-enrollment labels to stamp on
// workspace namespaces for the given mode. Empty for MeshModeNone (or an
// unknown mode, treated as none). In ambient mode, when a waypoint is
// configured, the waypoint-routing labels are included so the ingress gateway
// routes through the waypoint to ambient/global services.
func MeshNamespaceLabels(mode MeshMode, wp WaypointConfig) map[string]string {
	switch mode {
	case MeshModeAmbient:
		labels := map[string]string{labelIstioDataplaneMode: "ambient"}
		if wp.Name != "" {
			labels[labelIstioUseWaypoint] = wp.Name
			labels[labelIstioUseWaypointNamespace] = wp.Namespace
			labels[labelIstioIngressUseWaypoint] = "true"
		}
		return labels
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
