# Workspace access via per-tenant Gateway API on regional clusters

Status: accepted (2026-06-05)

Workspaces need TLS-secured external access (`https://<workspace>.<tenant-domain>`, e.g.
`https://thorsten-nb.dos-lab.ex.ls`). The agent-interface design originally chose NodePort +
cluster public IP for v1, deferring Istio. **We reverse that**: workspace access goes through a
Gateway API instance (Istio) on the tenant's regional cluster from day one. Each tenant (= org)
has exactly one regional cluster and one dedicated tenant domain; the regional cluster forms an
Istio multi-cluster mesh with its child clusters, and the operator attaches per-workspace routes
to the tenant's gateway. NodePort was rejected because it exposes child clusters directly,
provides no TLS, leaks node IP/port churn into the API contract, and would have been thrown away
the moment the gateway landed — the API contract (`status.access[]`) is identical either way, so
paying for the real thing immediately was cheaper than building the placeholder.

## The shape

- **Topology**: tenant : org : regional cluster : domain are 1:1:1:1. Hostnames stay one DNS
  label deep (`<ws>.<domain>`, `<ws>-<endpoint>.<domain>`) so a single per-tenant wildcard
  certificate covers everything. The first declared HTTP endpoint of a class is the *primary*
  and gets the bare hostname.
- **Data path**: regional Gateway terminates TLS → HTTPRoute (hostname) → mirror Service on the
  regional cluster (same name + namespace as the child-cluster Service, no local endpoints) →
  Istio multi-cluster endpoint discovery routes through the east-west gateway to the workspace
  pods on the child cluster.
- **Non-HTTP (SSH/TCP)**: raw TCP carries no hostname, so these endpoints get a dedicated high
  port on the tenant domain (e.g. `ssh://dos-lab.ex.ls:2207`) from a fixed, admin-provisioned
  listener port pool on the gateway; the operator binds a TCPRoute per endpoint. The set of
  existing TCPRoutes *is* the port-allocation table (no separate state). Pool exhaustion is a
  visible per-tenant capacity limit. TLS-wrapping SSH (SNI routing) was rejected for UX; a
  bastion/jump-host was rejected as a new auth surface.
- **Isolation**: every workspace deploys into its own namespace on the child cluster
  (`ws-<workspace-name>`) instead of the previous hardcoded `default`. This is load-bearing, not
  cosmetic: Istio multi-cluster discovery merges endpoints of same-name/namespace Services
  across the mesh, so a shared namespace would let same-named chart Services from different
  child clusters (or fixed-name charts) bleed traffic into each other. Mesh discovery is scoped
  (discoverySelectors) to labeled workspace namespaces only; prerequisites stay cluster-local in
  `default` and never participate in cross-cluster discovery.
- **Infra ownership**: Istio, the mesh (east-west gateways, shared trust), the Gateway, the
  wildcard cert (cert-manager), and DNS are *admin-installed tenant infrastructure* — a
  prerequisite, not operator work. The operator only attaches routes and fails loudly
  (`RoutingInfraNotReady`) when the infra is missing. Colony-controller provisioning of this
  stack is the documented follow-up. The operator never mutates the Gateway (which is also why
  the SSH port pool is pre-provisioned rather than dynamic; `ListenerSet` may relax this later).
- **Single source of truth for the domain**: the operator derives the tenant domain from the
  Gateway's HTTPS listener hostname (`*.dos-lab.ex.ls`), locating the Gateway by well-known
  name/namespace (operator-level default, overridable). No config field can drift from what the
  cert and DNS actually serve.
- **Chart contract**: an endpoint's child-cluster Service is found by convention
  (`<release-name>-<endpoint-name>`) or by an explicit `serviceName` on the `AccessEndpoint`
  (for fixed-name/umbrella charts) — same convention-plus-override pattern as resource
  injection. `AccessEndpoint.port` is the *Service* port.
- **No new CRDs**: the WSD reconciler writes routes directly through a `RouteProvider` Go
  interface (Gateway API is the only v1 implementation). The interface is the deliberate seam
  for future providers (Tailscale, NetBird-as-access — distinct from the NetBird *CNI* removal,
  which is unrelated); provider selection, when it exists, will be per-tenant. The old
  `WorkspaceRoute`/`RoutingProvider` CRDs were rejected as a strategy layer with one strategy;
  extracting a CRD later is mechanical because the boundary already exists.
- **Readiness**: `status.access[].ready` means the route is `Accepted` + `Programmed` —
  platform-side done. App-level health stays the chart's readiness probes (already enforced via
  Helm `wait`).
- **Cleanup**: owner references don't cross clusters, so deletion is finalizer-driven, in order:
  routes on regional (frees the pool port) → ServiceSet (uninstalls the release) → `ws-<name>`
  namespace on the child (Sveltos doesn't remove it; stale PVCs would otherwise resurrect state
  into a recreated same-name workspace). Retry while the target ClusterDeployment exists; if the
  CD is gone or deleting, skip — cluster teardown is the cleanup.

## Consequences

- Tenant onboarding requires the full infra stack before the first workspace routes (accepted;
  admin-manual now, Colony-automated later).
- Gateway API experimental channel (TCPRoute) becomes part of the prerequisite CRD set.
- Workspace names are effectively capped below 63 chars (hostname label + `ws-` namespace
  prefix + `-<endpoint>` suffix math); enforce at API/CEL level.
- Existing deployments installed into `default` are orphaned by the namespace change (accepted:
  pre-GA, ephemeral clusters).
