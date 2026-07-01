# Cross-cluster workspace routing scoped via per-child waypoints

Status: accepted (2026-06-22)

ADR-0001 routes workspace traffic through the tenant's regional Gateway to the workspace pod on
a child cluster via an Istio ambient **global service** (`istio.io/global` on the child Service +
a regional mirror Service). In practice this **fans out**: istiod advertises *every* mesh
cluster's east-west gateway as a healthy endpoint for the global service, regardless of which
cluster actually hosts the pod. With one child it happens to work; with more than one it breaks.

We validated this on the live mesh: with the workspace on child-1 and child-2 present, the
regional gateway round-robined across all clusters' east-west gateways. Requests landing on a
cluster **without** the service dead-ended (`503`); a cluster **with** a pod-less copy of the
service **relayed** them on to the hosting child — a second cross-cluster hop. For
geo-distributed clusters that relay is pathological (e.g. `regional → eu-west → us-east → pod`).

## Alternatives ruled out (all tested on the live mesh)

- **Drop `istio.io/global`** → no cross-cluster reachability at all (traffic dead-ends at the
  local waypoint; 100% `503`). Ambient requires the global opt-in.
- **Hand-pin an endpoint** (`WorkloadEntry`/`ServiceEntry` with `network: <child>`) → unreachable
  (`000`), while the global control path worked — istiod only synthesizes cross-network endpoints
  through the global-service mechanism, which is all-or-nothing.
- **Outlier detection** → ejects the dead clusters; lifted success from ~47% to ~97.5% but never
  100% (Envoy re-probes ejected endpoints) and is reactive.
- **`trafficDistribution: PreferClose`** → no effect (no node locality labels; and it's
  proximity/failover, not "route to the endpoint-owning cluster").
- **Pseudo-service-everywhere (relay)** → functionally 100%, but every non-hosting cluster becomes
  a relay hop — geo-pathological.
- **Two-tier north-south gateway** → correct and geo-friendly, but requires a *new* externally
  exposed gateway (LoadBalancer) on every child and makes the operator own child-gateway address
  discovery/sync.

## Decision / the shape

Scope the global service **per hosting child** using a dedicated, **internal** Istio **waypoint
pair**, instead of the shared default waypoint.

- **A waypoint name is a routing scope.** Cross-cluster traffic for a global service through a
  named waypoint resolves only to clusters whose matching service *also* uses that waypoint. So a
  per-child waypoint, used by only that child's workspace namespaces, scopes the endpoint set to
  that one child. Validated: traffic pinned to child-1, child-2 **excluded even with a pod-less
  copy present** (its relay counter stayed flat), 100% success, no fan-out.

- **Per-child waypoint pair, ClusterIP, admin-provisioned.** Each child gets a waypoint named
  `<clusterDeployment-name>-waypoint` (in `istio-system`, namespace configurable; truncate+hash
  over 63 chars) on **both** the regional cluster and that child. They are **ClusterIP / internal**
  — **no new externally exposed LoadBalancer**. They are tenant/onboarding infrastructure, the
  same category as the east-west gateway and the tenant Gateway (ADR-0001); the operator does not
  create them.

- **The operator only relabels.** It keeps `istio.io/global` + the regional mirror Service exactly
  as today, and changes one thing: the `istio.io/use-waypoint` value stamped on the workspace's
  **child** namespace becomes the per-child `<cd>-waypoint` (derived from
  `spec.clusterDeploymentRef.Name`) instead of a single shared waypoint. The waypoint-routing
  labels are stamped **only on the child namespace** — the side that owns the real endpoints; the
  regional mirror namespace gets mesh enrollment (`istio.io/dataplane-mode`) but no waypoint
  reference. Everything else in the routing path is unchanged.

- **Both sides required; hold otherwise.** If the regional or child `<cd>-waypoint` is absent or
  not `Programmed`, the operator surfaces `RoutesReady=False` / `InfraNotReadyError` and retries —
  same pattern as the missing tenant Gateway — rather than half-configuring a route.

## Consequences

- **No new exposed surface.** Workspaces route directly to the hosting child with no fan-out, no
  relay, and **no new LoadBalancer** — the existing (already-exposed) east-west gateways carry the
  traffic; the waypoints are internal.
- **Zero-trust retained.** Traffic stays on the ambient mesh (nested HBONE/mTLS) end to end.
- **No operator-owned address sync.** istiod continues to manage east-west gateway addresses
  (unlike the two-tier alternative, where the operator would track child gateway IPs).
- **Onboarding gains an obligation:** provision the `<cd>-waypoint` pair when a child cluster is
  onboarded (operator + local-dev-env must agree on the name).
- **The operator change is minimal:** make the static `MeshNamespaceLabels` (computed once in
  `cmd/main.go`) a per-workspace value (the waypoint name derived from the CD), stamp it on the
  child-namespace step (`MeshConfig.NamespaceLabels`), enrol the regional mirror namespace without
  the waypoint labels (`MeshConfig.MirrorNamespaceLabels`), and add the waypoint existence check.
  No new CRDs, no cross-cluster writes beyond what already exists.

## Risks / open verification (validate before relying on it in production)

- **Why istiod scopes this way is not yet fully explained** — it is experimental ambient
  multicluster behavior and may be version-sensitive (validated on Istio 1.29). Pin/track the
  Istio version; add a guard test.
- **Is the child-side waypoint strictly required, or does regional-only suffice?** We deployed the
  pair; regional-only was not isolated. If regional-only suffices, drop the child-side obligation.
- **Robustness unverified:** waypoint pod restart; multiple workspaces on one child; a workspace
  *moving* between children (relabel → re-pin); negative control (a second child with its own
  `<cd>-waypoint` stays independent).
