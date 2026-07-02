# Configurable prerequisite namespace with singleton conflict policy

Status: accepted (2026-07-02)

A WorkspaceClass declares Prerequisites — cluster-local shared infrastructure (operators/stacks
like `slurm-operator`, `llm-d-stack`) that must be healthy on a Child Cluster before a workspace
of that class deploys. The workspace controller installs each via a **singleton** ServiceSet named
`wsprereq-<cd>-<template>`, shared by every WorkspaceDeployment that needs it, and never mutated
after creation. Until now the install target namespace was hardcoded to `default`. Operators that
expect their own namespace (`slurm-system`, `cert-manager`, …) couldn't be expressed. We add an
optional per-prerequisite `namespace`, and — because the install is a per-cluster singleton but the
namespace is authored per-class — a policy for when two classes disagree.

## The shape

- **`PrerequisiteSpec.namespace`**, optional, DNS-1123-validated at admission, **defaulted to
  `default` in code, not via a CRD default** — so "unset" stays distinguishable from an explicit
  `"default"`. That distinction is load-bearing (see opt-in strictness below).
- **The install creates the namespace.** The prereq ServiceSet sets `createNamespace: true`; prereqs
  are not mesh participants and need no labels, so they skip the manual child-cluster pre-creation
  that Workspace Namespaces require (`namespace.go`). No-op when the namespace already exists.
- **Prerequisite is a per-cluster singleton** (see CONTEXT.md). The namespace is therefore a
  *cluster-global* property even though each class declares it. The singleton ServiceSet is the lock:
  whoever creates `wsprereq-<cd>-<template>` first bakes in the namespace and wins; we never mutate it.
- **Opt-in strictness (Option Y).** A namespace match is enforced **only when a class explicitly sets
  `namespace`**. An unset class accepts whatever namespace any provider already placed the prereq in
  (and installs to `default` only when nothing exists yet). This keeps every pre-existing class and
  every Colony-provisioned prereq working untouched on upgrade — conflict enforcement activates
  exactly when authors start caring about namespaces.
- **Conflict → terminal Failure.** When a class explicitly requests namespace X but the shared
  singleton already lives in Y — whether installed by another workspace ServiceSet **or** provided by
  the Colony — the WorkspaceDeployment goes `Failed` with reason `PrerequisiteNamespaceConflict` and a
  message naming both namespaces and the incumbent. The winning class's workspaces keep running; only
  the conflicting newcomer fails. This is uniform across providers — the `Source` field
  (`workspace` | `colony`) tells the user where to look. `PrerequisiteStatus` gains a structured
  `namespace` field reporting where the singleton actually resides.
- **Migration is manual and deliberate.** To move a prereq to a new namespace, an admin deletes the
  shared `wsprereq-<cd>-<template>` ServiceSet; the next reconcile reinstalls it in the new namespace.

## Alternatives ruled out

- **Multi-namespace prerequisites** (namespace part of identity; `wsprereq-<cd>-<template>-<ns>` →
  separate installs). Rejected: contradicts "cluster-local shared infrastructure" and would install
  cluster-scoped operators twice, colliding on CRDs/webhooks.
- **Always-strict matching (Option X)** (effective namespace, unset→`default`, must always match).
  Rejected: a Colony-provisioned prereq in a non-`default` namespace would suddenly conflict-Fail
  every existing class on upgrade — a silent break.
- **Colony silently wins** on class-vs-colony namespace disagreement. Rejected: silently ignoring an
  explicit request is the same "silent surprise" we reject for the workspace-vs-workspace case.
- **Auto-migrate** on class edit (recreate the SS automatically). Rejected: would tear down shared
  cluster infrastructure out from under running workspaces on a mere catalog edit, and "never mutate
  the singleton" is what keeps concurrent reconciles safe.

## Consequences

- Editing a WorkspaceClass's prerequisite `namespace` does **not** relocate an existing install; it
  only produces a conflict-Fail until an admin deletes the shared ServiceSet. Relocation disrupts all
  current dependents — an explicit, admin-initiated act by design.
- An *unset* class that reconciles first can "claim" `default` for a template and thereby
  conflict-Fail a later *explicit* class that wanted a different namespace. Consistent with
  "whoever's SS exists wins," but worth knowing.
- Colony-managed detection must now read `ServiceState.Namespace` (available on both
  `cd.status.services[]` and the embedded `cd.spec.serviceSpec.services[]`), not just the template name.
