# Helm chart distribution and versioning

Status: accepted (2026-06-18)

The operator ships as a Helm chart, but the chart was an **umbrella** (`charts/exalsius`)
wrapping an operator subchart plus `skypilot-nightly` and `gpu-operator` dependencies, frozen
at version `0.1.0` while the operator marched to `0.9.0`. It was never packaged or published
anywhere — only the container image reached GHCR. release-please bumped the Go module, tag, and
GitHub release, but the chart drifted independently and by hand. We collapse the chart to a
single artifact, tie its version to the operator, and publish it on every release.

## The shape

- **One chart, not an umbrella.** The umbrella existed to bundle three things: a dead
  `skypilot-nightly` dependency (zero references anywhere in the operator), a default-off
  `gpu-operator` dependency (which belongs on **Child Clusters** where workloads run, not the
  management cluster this chart targets), and a single `aws-credentials` Secret. None justify a
  second Chart.yaml. We flatten the umbrella and the operator subchart into **one chart at
  `charts/exalsius-operator/`** (`name: exalsius-operator`): Deployment, RBAC, ServiceAccount,
  HPA, and CRDs. skypilot, gpu-operator, the dead `capi:` provider-version values, and the
  `aws-credentials` Secret are all dropped — AWS credentials are managed out-of-band. GPU
  tooling on Child Clusters is documented as a prerequisite, not bundled here.

- **One version line.** The chart `version`, chart `appVersion`, and the operator/image version
  move together as a single number, owned by release-please. A `release-please-config.json`
  (single `.` component, `release-type: go`) carries `extra-files` entries that bump
  `$.version` and `$.appVersion` in `charts/exalsius-operator/Chart.yaml` in the same release PR
  as the tag and CHANGELOG. The image tag is **not** patched: `values.yaml` sets `image.tag: ""`
  and the Deployment template falls back to `{{ .Values.image.tag | default .Chart.AppVersion }}`,
  so the pinned `appVersion` flows through to the image automatically. The chart version always
  identifies which operator it ships.

- **OCI to GHCR, on release only.** On `release published`, CI packages the chart and
  `helm push`es it to `oci://ghcr.io/exalsius/charts/exalsius-operator:<version>` — the same
  registry as the image, under a `/charts/` namespace to keep charts and images separate. The
  packaged `.tgz` is also attached to the GitHub Release as a downloadable artifact. No GH Pages
  branch, no `index.yaml` to maintain. No dev/prerelease charts on `main` pushes: every
  published chart version is a real SemVer that matches a tag.

## Why not the alternatives

- **GH Pages classic repo (chart-releaser).** Most familiar `helm repo add` ergonomics, but it
  means a `gh-pages` branch, an `index.yaml` to keep consistent, and a second publish mechanism
  alongside the image. OCI is the Helm 3.8+ default, needs no extra hosting, and reuses the GHCR
  auth we already have for images.

- **Keep the umbrella, just clean it.** Once skypilot and gpu-operator are gone, an umbrella
  wraps a single subchart for no benefit — two Chart.yaml files and subchart packaging to
  maintain. Flattening removes that overhead.

- **Independent chart version (own SemVer, appVersion tracks operator).** Correct when a chart
  ships software it doesn't own. Here the chart and the operator are released from one repo in
  lockstep, so two version lines is needless ceremony. One number is simpler to reason about and
  to wire into release-please.

## Consequences

- Install changes from a local `helm install ./charts/exalsius` to
  `helm install exalsius-operator oci://ghcr.io/exalsius/charts/exalsius-operator --version <v>`.
- The CRD lockstep (`make sync-chart-crds`) retargets to `charts/exalsius-operator/crds`; the
  Helm-only-installs-crds-on-first-release caveat still applies.
- CI references to `charts/exalsius` (pre-release lint, `hack/install-ci.sh`,
  `hack/install-via-helm.sh`, `hack/ci-values.yaml` with its now-removed `operator:` nesting and
  skypilot/aws blocks) must be updated to the flattened chart.
- AWS-credential provisioning that relied on the chart's `aws-credentials` Secret must now be
  supplied externally.
- release-please bumps by **commit message, not changed files**, so the version applies to the
  whole repo. Operator-only changes republish the chart (correct — a new binary is a new
  deployable); a chart-only change still advances `appVersion` and retags a functionally
  identical image. We accept this: `appVersion` means "the release this chart was cut in," not
  strictly "the operator binary version," and chart-only changes are expected to be rare (chart
  edits usually ride along with the operator change that motivates them). The cost of the
  alternative — two release-please components and two version lines maintained forever — is not
  worth it at this cadence.
