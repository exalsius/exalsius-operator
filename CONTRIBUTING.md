# Contributing to exalsius-operator

Thank you for helping improve the operator. This guide covers how the repository is
set up, what the tooling expects of a change, and how a change gets reviewed and
released. Everyone participating is expected to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- **Report a bug or request a feature** with the
  [issue forms](https://github.com/exalsius/exalsius-operator/issues/new/choose). The
  version fields and resource status in the bug form are what maintainers need first.
- **Fix documentation.** README, chart README, examples and ADRs live in this repo and
  are reviewed like code. The end-user site at <https://docs.exalsius.ai> has its own
  repository, `exalsius-docs`.
- **Send a pull request.** Small, focused PRs get reviewed faster. For anything that
  changes a CRD, the status contract or chart values, open an issue first so the
  design can be discussed before you invest in the implementation.
- **Security issues** go through [SECURITY.md](SECURITY.md), never through a public
  issue.

Some things belong elsewhere in the exalsius organisation:

| You want to change...                                          | Go to                                                                  |
| -------------------------------------------------------------- | ---------------------------------------------------------------------- |
| A workspace chart (Jupyter, Slurm, llm-d, ...) or a WorkspaceClass catalog entry | [exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub) |
| The `exls` CLI or the REST API                                 | [exalsius-cli](https://github.com/exalsius/exalsius-cli), [exalsius-api](https://github.com/exalsius/exalsius-api) |
| The multi-cluster local development environment                | [local-dev-env](https://github.com/exalsius/local-dev-env)             |

## Development setup

You need:

- Go as pinned in `go.mod` (currently 1.26)
- Docker (for building the image and for Kind)
- `kubectl`, [Kind](https://kind.sigs.k8s.io/) and Helm 3
- Access to a Kubernetes cluster with k0rdent installed if you want to run the operator
  against real ClusterDeployments; the [chart README](charts/exalsius-operator/README.md)
  lists the prerequisites

Everything else (`controller-gen`, `kustomize`, `setup-envtest`, `golangci-lint`) is
downloaded into `bin/` on first use by the Makefile.

A [devcontainer](.devcontainer/devcontainer.json) with Go, Docker-in-Docker, Kind,
kubebuilder and kubectl is provided if you prefer not to install the toolchain locally.

Common commands:

```bash
make build                 # build bin/manager
make run                   # run the operator on your host against the current kubeconfig context
make test                  # unit and envtest-based controller tests
make lint                  # golangci-lint with the config in .golangci.yml
make docker-build IMG=ghcr.io/exalsius/exalsius-operator:dev
make deploy IMG=...        # kustomize-based deploy to the current context
make build-installer       # consolidated install.yaml in dist/
```

For a throwaway single-node cluster, `hack/start-dev-env.sh` creates a Kind cluster from
`hack/kind-config.yaml`; `hack/install-via-helm.sh [values.yaml]` installs the chart
into `exalsius-system`; `hack/uninstall-dev-env.sh` tears it down. For a realistic
two-node setup with k0rdent and a child cluster, follow the
[quickstart](docs/quickstart.md); its scripts and version pins live under
`hack/quickstart/`.

## Making a change

### Branches

Branch from `main` using a prefix that matches the kind of change, the same convention
as the other exalsius repositories:

```
feat/<short-topic>    fix/<short-topic>    docs/<short-topic>    chore/<short-topic>
```

`main` is protected: changes land only through a reviewed pull request.

### Generated code and manifests

The CRDs, RBAC rules and DeepCopy methods are generated. After editing anything under
`api/**/*_types.go` or a `+kubebuilder` marker in `internal/`, run

```bash
make manifests generate sync-chart-crds
```

and commit the output. `make manifests` regenerates `config/crd/bases` and
`config/rbac`; `make generate` regenerates `zz_generated.deepcopy.go`;
`make sync-chart-crds` copies the CRDs into the Helm chart so the chart never drifts
from the API. `make test` runs the first two automatically, so a stale checkout shows
up as a diff in CI.

### Helm chart

The chart under `charts/exalsius-operator` is the supported install path and is
published to Artifact Hub. When you change `values.yaml`:

- update the values tables in `charts/exalsius-operator/README.md`,
- update `charts/exalsius-operator/values.schema.json`,
- make sure `helm lint --strict charts/exalsius-operator` and a render with
  `hack/ci-values.yaml` still pass (CI runs both).

Never change `version` or `appVersion` in `Chart.yaml` by hand; release-please bumps
them together with the operator version.

### Architecture decisions

Decisions that shape the operator (new CRD fields, status contracts, how the operator
talks to k0rdent or Sveltos, access models) are recorded as Architecture Decision
Records in [`docs/adr/`](docs/adr/). Add a new numbered file
(`NNNN-short-title.md`) with a `Status:` line and a short problem statement, decision,
and consequences; update an existing ADR when you change a decision. Use the terms
defined in [`CONTEXT.md`](CONTEXT.md) so names stay consistent across code, docs and
ADRs.

### Documentation

- Each document answers one kind of question (a tutorial, a how-to, a reference, or an
  explanation). Do not mix an install procedure into a concept page or vice versa.
- The user-facing entry points are [`docs/quickstart.md`](docs/quickstart.md) (tutorial)
  and [`docs/concepts.md`](docs/concepts.md) (explanation, with the diagrams). Diagram
  sources live under `docs/architecture/` as draw.io files with an exported PNG next to
  them; new diagrams go into `docs/concepts.md`, not into other pages.
- User-facing documents do not link to ADRs or paste test logs; ADRs are for
  contributors.
- Keep `examples/` runnable: if you rename a WorkspaceClass or a field, update the
  examples in the same PR.

## Testing

```bash
make fmt vet lint test
```

is the minimum before opening a PR and is what CI runs on every pull request, together
with a Helm chart lint and a container image build.

- **Unit and controller tests** use envtest (a real kube-apiserver and etcd downloaded
  by `setup-envtest`, no Docker needed). Add tests next to the code under `internal/`.
  Run a single package or test with `go test ./internal/controller/infra/... -run
  TestColonyReconciler -v`.
- **Kind-based e2e** (`make test-e2e`) expects a running Kind cluster and builds and
  loads the manager image into it.
- **Multi-cluster workspace e2e** (`make test-e2e-workspace`) runs the live suite under
  `test/e2e-workspace/` against a provisioned four-cluster environment from
  `local-dev-env`. In CI it runs automatically on release PRs and on any PR that
  carries the **`e2e` label**. Add that label yourself when your change touches
  WorkspaceDeployment reconciliation, prerequisites, routing or the chart RBAC. The
  required status check is `e2e-gate`, which passes trivially when the suite is
  skipped.

## Commits and pull requests

### Conventional Commits

PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
A check on the PR title enforces the allowed types: `feat`, `fix`, `docs`, `refactor`,
`chore`, `test`, `ci`. Scopes are optional; the ones in use are `colony`, `workspace`,
`chart`, `ci` and `docs`.

```
feat(workspace): add per-prerequisite namespace
fix(colony): publish a worker-reachable cp-api-endpoint on the NodePort path
docs(chart): document the external access prerequisites
feat(workspace)!: rename status.accessURL to status.endpoints
```

The title matters because PRs are usually squash-merged and the title becomes the commit
message that release-please reads: `feat` bumps the minor version, `fix` bumps the
patch version, `!` or a `BREAKING CHANGE:` footer marks a breaking change in the
changelog (while pre-1.0 it still bumps the minor version). `docs`, `chore`, `refactor`,
`test` and `ci` do not trigger a release.

Individual commits inside a PR can be informal if you squash; if you want them kept
(rebase merge), make each one a Conventional Commit. We do not require a DCO
`Signed-off-by` line. If a squash combines commits from several people, add
`Co-authored-by:` trailers so everyone is credited.

### What a good PR looks like

- One concern per PR. A refactor and a behaviour change are two PRs.
- The description says *why*, links the issue, and tells the reviewer where to look
  first and how you tested it. The PR template has a checklist for the mechanical parts.
- Generated files, chart README tables and examples are updated in the same PR as the
  change that needs them.
- CI is green. If the `e2e` suite is relevant, the label is set and `e2e-gate` is green.

### Review

A maintainer reviews every PR; one approving review is required to merge. Reviews focus
on the reconciliation logic being idempotent and safe under repeated or partial runs,
on RBAC not widening more than needed, on status and condition semantics staying
backwards compatible, and on the documentation matching the behaviour. Expect a first
response within a few business days; ping the PR if it goes quiet for longer.

## Releases

Releases are cut by [release-please](https://github.com/googleapis/release-please):

1. Merged `feat:` and `fix:` PRs accumulate in an automatically maintained release PR
   titled `chore(main): release X.Y.Z`.
2. Merging that PR tags `vX.Y.Z`, updates `CHANGELOG.md`, and bumps `version` and
   `appVersion` in the Helm chart in one commit.
3. The post-release workflow builds and pushes `ghcr.io/exalsius/exalsius-operator:X.Y.Z`,
   pushes the chart to `oci://ghcr.io/exalsius/charts/exalsius-operator`, refreshes the
   Artifact Hub metadata, and attaches the chart `.tgz` to the GitHub Release.

Contributors therefore never edit `CHANGELOG.md`, `.release-please-manifest.json` or
the chart versions by hand. Every push to `main` also publishes a `dev-<sha>` image for
testing unreleased changes.

## Getting help

- Questions about using or developing the operator: <run.it@exalsius.ai>
- End-user documentation for the whole stack: <https://docs.exalsius.ai>
- Security reports: [SECURITY.md](SECURITY.md)
