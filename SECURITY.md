# Security Policy

This policy covers the exalsius-operator: the operator binary, its Custom Resource
Definitions, the Helm chart at `charts/exalsius-operator`, the container image
`ghcr.io/exalsius/exalsius-operator`, and the scripts under `hack/`.

## Reporting a vulnerability

Please do not open a public GitHub issue for a suspected vulnerability.

Report it through one of these channels:

1. **GitHub private vulnerability reporting** (preferred):
   <https://github.com/exalsius/exalsius-operator/security/advisories/new>.
   The report is visible only to the maintainers, and the fix can be coordinated in
   the same draft advisory.
2. **E-mail**: <run.it@exalsius.ai> with the subject prefix `[SECURITY]`. If you need
   to send sensitive details, ask for an encrypted channel in a first message and we
   will arrange one.

Include what you can of the following. It shortens triage considerably:

- Operator version (Helm chart version, image tag or digest) and how it was installed.
- k0rdent (KCM) version and the Kubernetes version of the management cluster.
- The affected component: Colony reconciliation, WorkspaceClass or WorkspaceDeployment
  reconciliation, Helm chart RBAC or manifests, container image, quickstart scripts.
- Steps to reproduce, ideally with redacted custom resource YAML and operator logs.
- Your assessment of the impact, for example credential exposure, privilege escalation
  on the management cluster, or cross-cluster access.
- Whether the issue is already public or being exploited.

Do not include real cloud credentials, kubeconfigs or SSH keys in a report.

## What to expect

| Step                                  | Target                                            |
| ------------------------------------- | ------------------------------------------------- |
| Acknowledgement of your report        | within 3 business days                            |
| Triage and severity assessment        | within 10 business days                           |
| Fix released                          | in the next patch of the supported minor version  |
| Public disclosure                     | when the fix is released, or 90 days after the report, whichever comes first |

For issues that are being actively exploited we shorten the disclosure window and
publish a fix as soon as it exists. We keep you informed at each step and ask you to
keep the report confidential until disclosure. We credit reporters in the GitHub
security advisory and the release notes unless you ask us not to.

Fixes ship as a normal release: a `fix:` commit picked up by release-please, a new
`vX.Y.Z` tag, a new image tag and chart version on GHCR, and a GitHub security
advisory that names the affected versions.

## Supported versions

The operator is pre-1.0. Security fixes are made on the latest minor release only.

| Version          | Supported                                   |
| ---------------- | ------------------------------------------- |
| 0.12.x (latest)  | yes                                         |
| < 0.12           | no, upgrade to the latest release           |
| `dev-*`, `main`  | no, development builds without support      |

Upgrade with the Helm chart (`helm upgrade --install exalsius-operator
oci://ghcr.io/exalsius/charts/exalsius-operator --version <version>`); see the
[chart README](charts/exalsius-operator/README.md) for upgrade notes. This table is
updated on every release.

## Scope

**In scope**

- The operator's reconciliation logic in `internal/` and the API types in `api/`.
- RBAC, ServiceAccount and Deployment manifests in the Helm chart and `config/`.
- The container image build (`Dockerfile`) and the release workflows under
  `.github/workflows/`.
- Quickstart and bootstrap scripts under `hack/`.

**Out of scope, report upstream**

| Component                                   | Where to report                                                     |
| ------------------------------------------- | ------------------------------------------------------------------- |
| k0rdent / KCM (ClusterDeployment, ServiceSet) | <https://github.com/k0rdent/kcm/security>                          |
| k0smotron and k0s                           | <https://github.com/k0sproject/k0smotron/security>, <https://github.com/k0sproject/k0s/security> |
| Sveltos                                     | <https://github.com/projectsveltos/addon-controller/security>       |
| Cilium                                      | <https://github.com/cilium/cilium/security>                         |
| Cluster API and its providers               | <https://github.com/kubernetes-sigs/cluster-api/security>           |
| Workspace Helm charts and WorkspaceClass catalog | <https://github.com/exalsius/exalsius-workspace-hub>           |
| exalsius API and CLI                        | <https://github.com/exalsius/exalsius-api>, <https://github.com/exalsius/exalsius-cli> |

If you are unsure where an issue belongs, report it here and we will route it.

## Artifact integrity

Container images and Helm charts are **not signed** at the moment, and no SBOM or
build provenance attestation is published. Until that changes:

- Pin the operator image by digest rather than tag. Resolve the digest for a release
  with `docker buildx imagetools inspect ghcr.io/exalsius/exalsius-operator:<version>`
  and set `image.tag` to `<version>@sha256:<digest>` in your Helm values; the chart
  renders `repository:tag`, and a tag with an appended digest is resolved by the
  digest.
- Verify a pulled chart against the `.tgz` attached to the matching
  [GitHub Release](https://github.com/exalsius/exalsius-operator/releases); both are
  produced by the same workflow run and must have the same SHA-256.
- Images tagged `dev-<sha>`, `dev`, `main` and `latest` are moving targets; use a
  release version in production.

Signing the image and chart and publishing provenance is planned; this section will be
updated when it lands.

## Deployment hardening

The operator is a privileged component. Keep these properties in mind when deploying:

- **Cluster-admin-equivalent RBAC.** The bundled ClusterRole lets the operator manage
  k0rdent ClusterDeployments, ServiceSets, Secrets and its own resources on the
  management cluster. Run the operator in its own namespace, restrict who can create
  or edit `Colony`, `WorkspaceClass` and `WorkspaceDeployment` resources, and set
  `rbac.create=false` to supply a narrower role if your setup allows it.
- **Secrets on the management cluster.** Child-cluster kubeconfigs, cloud credentials
  consumed by k0rdent, and (on the quickstart path) the SSH private key used to reach
  worker nodes (Secret `remote-ssh-key` in `kcm-system`) live as Secrets on the
  management cluster. Enable encryption at rest
  for Secrets, restrict `get`/`list` on Secrets in the operator and k0rdent
  namespaces, and rotate credentials that were used in a demo or quickstart.
- **Anyone who can create a WorkspaceDeployment can deploy the Helm chart pinned by
  the chosen WorkspaceClass onto the target child cluster.** Treat WorkspaceClass
  authoring as an administrator action and review the charts it pins.
- **Pod security.** The chart ships restricted Pod Security Standards defaults
  (`runAsNonRoot`, seccomp `RuntimeDefault`, dropped capabilities, read-only root
  filesystem) and the image is built on `gcr.io/distroless/static:nonroot`. Keep
  these defaults.
- **Audit.** Enable Kubernetes audit logging for the management cluster so changes to
  the exalsius CRDs and to Secrets are traceable.

## Dependencies

Go module and GitHub Actions dependencies are tracked with Dependabot. Dependency
updates that fix a published vulnerability are released as a `fix:` in the next patch
release of the supported minor version. Vulnerabilities in the base image are
addressed by rebuilding on the next release.
