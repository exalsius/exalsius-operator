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

// Package routing defines the seam between the workspace lifecycle and how
// workspace access routes are materialized (ADR-0001). The reconciler knows
// nothing about HTTPRoutes, gateways, or VPNs — it asks a RouteProvider for
// access entries and cleans them up on deletion. New access technologies
// (Tailscale, NetBird-as-access) are added by implementing RouteProvider;
// provider selection, when more than one exists, will be a per-tenant
// concern.
package routing

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workspacesv1 "github.com/exalsius/exalsius-operator/api/workspaces/v1"
)

// RouteRequest carries the resolved context a provider needs to materialize
// or remove access for one workspace. It deliberately includes the
// management-cluster client rather than pre-resolved regional/child clients:
// each provider decides which clusters it needs to touch and resolves them
// via the cluster topology (KOF labels, kubeconfig secrets).
type RouteRequest struct {
	// Workspace is the deployment being routed.
	Workspace *workspacesv1.WorkspaceDeployment

	// Endpoints are the access endpoints declared by the WorkspaceClass.
	// May be nil during cleanup when the class is no longer resolvable —
	// providers must be able to clean up from workspace identity alone
	// (e.g. via labels on the objects they created).
	Endpoints []workspacesv1.AccessEndpoint

	// ManagementClient reads cluster topology and kubeconfig secrets on the
	// management cluster.
	ManagementClient client.Client

	// Scheme is the management client's scheme, for building per-cluster
	// clients.
	Scheme *runtime.Scheme
}

// RouteProvider materializes externally reachable access for workspace
// endpoints and reports the result as AccessEntry values for
// WorkspaceDeployment.status.access[].
type RouteProvider interface {
	// EnsureRoutes materializes access for all endpoints of the workspace
	// and returns what is reachable. Idempotent: repeat calls with unchanged
	// input converge on the same entries. An entry with Ready=false and a
	// Message is the per-endpoint failure surface (e.g. pool exhausted);
	// returning an error signals a provider-wide transient problem the
	// caller should retry.
	EnsureRoutes(ctx context.Context, req RouteRequest) ([]workspacesv1.AccessEntry, error)

	// CleanupRoutes removes everything EnsureRoutes created for the
	// workspace. Idempotent; must tolerate nil req.Endpoints and routes
	// that never existed.
	CleanupRoutes(ctx context.Context, req RouteRequest) error
}
