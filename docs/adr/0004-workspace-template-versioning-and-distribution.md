# Workspace template versioning, distribution, and rollback

Status: accepted (2026-06-19)

A workspace type (jupyter-notebook, vscode, …) is more than a Helm chart: it is the chart
(deployed to a **Child Cluster**) **plus** the two k0rdent/operator CRs that make it selectable
on the **management cluster** — a `ServiceTemplate` (wraps the chart) and a `WorkspaceClass`
(the catalog entry a `WorkspaceDeployment` references). Today these drift apart: the chart CI
re-pushes the *same* `Chart.yaml` version to OCI on every change (mutable — overwrites the
artifact), ServiceTemplate sourcing is inconsistent (`amd-operator`/`slinky` source the chart
by **git path**, the jupyter ServiceTemplate uses a **HelmRepository**), the CRs are
hand-written and unversioned, and a `WorkspaceDeployment` referencing a `WorkspaceClass` by a
bare name silently changes behaviour when that class is edited. There is no way to roll back.
We make the **Workspace Template** the versioned unit, publish it immutably, and give every
`WorkspaceDeployment` an immutable lineage.

## The shape

- **Workspace Template = the versioned unit.** A chart directory owns, alongside `Chart.yaml`,
  an `exalsius/` folder holding templated `servicetemplate.yaml`, `workspaceclass.yaml`, and an
  `example-workspacedeployment.yaml`. The chart and its CRs version and ship together; they are
  never edited independently of a release.

- **Per-chart release-please, immutable OCI.** Each chart owns its SemVer in `Chart.yaml`,
  bumped by release-please from conventional commits (`feat(jupyter)!` → major, `fix(jupyter)`
  → patch), with per-chart tags (`jupyter-notebook-v1.0.1`) and CHANGELOGs. The chart is pushed
  to `oci://ghcr.io/<repo>/charts` and a **published version is never overwritten** — CI fails a
  push to an existing version, so every change must bump. Immutability is the precondition for
  rollback: an old version must still be there to roll back to.

- **One chart version per ServiceTemplate, version-named.** k0rdent's `ServiceTemplate`
  `chartSpec.version` is a single value, so a ServiceTemplate wraps **exactly one** chart
  version. Each release therefore emits an immutable ServiceTemplate named with the version
  (`jupyter-notebook-1-0-1`, dots → dashes for DNS-1123), sourcing the chart from the **OCI
  HelmRepository** at the matching version. Old ServiceTemplates persist.

- **Version-named WorkspaceClass; the WorkspaceDeployment pins an exact version.** The
  `WorkspaceClass` carries the version in its name too (`jupyter-notebook-1-0-1`) and pins its
  ServiceTemplate. A `WorkspaceDeployment` references that exact class. The result is an
  **immutable lineage**: what a running workspace deploys never changes underneath it, and an
  upgrade or rollback is an explicit, auditable edit of the `workspaceClassRef`. We deliberately
  reject a stable, mutable `WorkspaceClass` name (see alternatives).

- **Delivery applies the rendered CRs to the management cluster; rollback is by version.**
  On release, CI renders the `exalsius/` templates to version-named CRs under
  `manifests/<chart>/<version>/` in the templates repo; that tree is then applied to the
  management cluster. The exact transport (GitOps reconciler, a pipeline `kubectl apply`, etc.)
  is a separate, swappable decision and is intentionally not fixed here. Whatever the transport,
  rollback of a bad release is reverting the change that introduced the version and/or pointing a
  `WorkspaceDeployment` back at the prior version; the prior CRs and OCI chart are still there.

- **Version discovery moves to the CLI/API.** Because names are no longer stable, "deploy
  jupyter" resolves to the latest `jupyter-notebook-*` class at request time; the CLI lists
  available versions and defaults to latest. The operator stays dumb: it only ever sees a
  concrete, pinned `workspaceClassRef`.

## Why not the alternatives

- **Stable, mutable `WorkspaceClass` name** (e.g. always `jupyter-notebook`, repinned on
  release). Cleaner upgrades (WSDs never change), but a running workspace's definition changes
  underneath it on every release, and rollback is a cluster-state edit with no immutable record
  of what a given WSD was actually built from. The immutable-lineage guarantee is worth the
  cost of explicit version references. (A stable "→ latest" alias can be added later as a
  convenience without giving up immutable pinning.)

- **Git-path chart sourcing** (`sourceRef: GitRepository`, `chart: workspace-templates/…`, as
  `amd-operator`/`slinky` do). The chart version is whatever sits at a git ref — mutable, no
  SemVer artifact, no immutable OCI digest to roll back to. OCI gives content-addressable,
  immutable releases.

- **One repo-wide version for all charts.** Couples unrelated charts and produces noisy no-op
  bumps; a jupyter fix should not version vscode.

- **CI `kubectl apply` of the CRs.** Imperative, no on-cluster audit trail, and rollback means
  re-running a pipeline at an old tag rather than reverting declared state.

## Consequences

- **WorkspaceClasses and ServiceTemplates accumulate** (cluster-scoped, one pair per release).
  A retention/pruning policy (keep last N, or GC versions with no referencing WSDs) is needed —
  deferred, but called out so it is not a surprise.

- **The CLI/API owns version discovery and the "latest" default**; a bare "jupyter" is no
  longer a valid on-cluster reference.

- **A transport that applies `manifests/`** to the management cluster is a one-time
  prerequisite to set up (GitOps reconciler or a release pipeline step — left open here).

- **Chart authors must keep `exalsius/` templates in lockstep** with the chart's value contract
  — the same lockstep discipline ADR-0003 applies to the operator chart's CRDs.

- **Qualifying as a Workspace Template requires conforming to the chart contract**: consume the
  `_exalsius` resource/scheduling injection (ADR-0002) and expose a ClusterIP Service for
  operator-owned routing (ADR-0001) rather than NodePort/Ingress. Charts that don't (the
  finite-Job training charts, the operator/prerequisite charts) are out of scope for this
  scheme.
