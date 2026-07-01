# Pre-release E2E runs through local-dev-env, not a bespoke in-repo kind job

Status: accepted (2026-06-30)

The operator's pre-release gate used to stand up its own single kind cluster, install the chart,
apply a `docker-colony.yaml`, and wait on CAPI/k0smotron to provision a real child cluster. On a
GitHub-hosted runner that bring-up never finished — the job hung indefinitely instead of failing,
so release-please PRs could never go green. We replace it with the real multi-cluster environment
from the sibling **local-dev-env** repo, driven by `make workspace-testing-operator`, which
exercises every WorkspaceClass through every capability (resource override, prerequisites, GPU
gating, HTTP/SSH routing, cross-cluster routing, deletion ordering, the infra-not-ready negative).

## The shape

- **The environment lives in local-dev-env; the test lives here.** local-dev-env owns provisioning
  (4 kind clusters — management + regional + two adopted children — plus Istio ambient and
  k0rdent). This repo's CI **checks out local-dev-env** with the App token and runs its shared CI
  scripts (`scripts/ci/render-components.sh`, `collect-diagnostics.sh`) plus the make targets.
  exalsius-api will run the analogous checkout-and-run job.
- **Not a reusable workflow.** This repo is **public** and local-dev-env is **private**; GitHub
  forbids a public repo from calling a reusable workflow in a private one (`workflow was not
  found`, regardless of the repo Access setting). `actions/checkout` of a private repo with a token
  has no such restriction, so checkout-and-run is the correct shape. See local-dev-env ADR-0001.
- **The operator under test is built from local source** (`components.ci.yaml`
  `source.local` + `build.enabled: true` pointed at the PR checkout), the exact path a developer
  uses — so CI also exercises the Dockerfile, no special-case injection.
- **The rest of the stack is pinned.** The release gate runs every *other* component (api,
  k0rdent, CAPI, Istio) at fixed released tags from a committed known-good baseline file in
  local-dev-env, so a green run means "this operator works against the last known-good stack" and
  another component's broken `main` can't red our release. Cross-component drift is caught by a
  separate **nightly-at-main** run that is non-blocking.
- **Runner:** ephemeral self-hosted ARC pods (one job per pod) with a privileged DinD sidecar, so
  the 4 kind clusters fit and tear down with the pod. A hard `timeout-minutes` guarantees a hung
  bring-up *fails* rather than hangs (the original bug). Because the pod is gone after the run, an
  always-on diagnostics step uploads pods/events/describe + operator/k0rdent logs + every
  WorkspaceDeployment status as a CI artifact.
- **Gating:** the workspace suite runs on release-please PRs (required, blocking) and opt-in via an
  `e2e` label on other PRs. A always-running sentinel `e2e-gate` job is the actual *required*
  status check, so non-release PRs (where the heavy job is skipped by design) are not blocked
  forever waiting on a status that never reports.

## Consequences

- **The operator release gate no longer covers ColonyReconciler / CAPI provisioning directly.**
  `make workspace-testing-operator` runs against *adopted* kind children; it never provisions a
  cluster from a Colony. This is deliberate: that path is covered by the API-driven user-flow in
  exalsius-api's gate and by the nightly-at-main run. **Do not "restore" a colony-provisioning
  step to this gate without revisiting this trade-off** — the old job was dropped on purpose.
- The gate depends on a **cross-repo checkout** of local-dev-env and a GitHub App token (scoped to
  the org, `contents:read` + `packages:read`). Env changes (new cluster, bumped k0rdent) are made
  once in local-dev-env and require no change here.
- The shared testing architecture (checkout + shared scripts, local-source injection, pinned
  baseline) is recorded in local-dev-env's own ADR.
