# exalsius-operator

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/exalsius-operator)](https://artifacthub.io/packages/search?repo=exalsius-operator)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

The **exalsius-operator** is a Kubernetes operator that provisions ephemeral,
multi-cloud AI clusters and deploys GPU workspaces onto them. It is the
control-plane component of the [exalsius stack](https://github.com/exalsius):
declarative infrastructure through Custom Resource Definitions, backed by
[k0rdent](https://k0rdent.io) (Cluster API), [k0smotron](https://k0smotron.io)
and [Sveltos](https://projectsveltos.github.io/sveltos/).

This chart installs the operator and its CRDs on a k0rdent **management
cluster**.

## What the operator does

| CRD | Scope | Purpose |
| --- | --- | --- |
| `Colony` (`infra.exalsius.ai/v1`) | Namespaced | Groups one or more child clusters, possibly across cloud providers. Each entry becomes a k0rdent `ClusterDeployment`; the operator configures Cilium, aggregates kubeconfigs and publishes the GPU inventory of every child. |
| `WorkspaceClass` (`workspaces.exalsius.ai/v1`) | Cluster | Catalog entry for a workspace type (Jupyter, LLM endpoint, Slurm, ...). Pins a k0rdent `ServiceTemplate`, resource defaults, prerequisites, access endpoints and user-facing configuration prompts. |
| `WorkspaceDeployment` (`workspaces.exalsius.ai/v1`) | Namespaced | A request to run a workspace of a class on a target child cluster. The operator gates on GPU capacity, creates one k0rdent `ServiceSet` per workspace, and reports readiness and access URLs. |

Ready-made WorkspaceClasses and their Helm charts live in the
[exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub).

## Prerequisites

- Kubernetes >= 1.28 and Helm >= 3.8 (OCI support).
- [k0rdent](https://docs.k0rdent.io) (KCM) installed on the management cluster,
  including its `ksm-projectsveltos` state management provider. The operator
  creates `ClusterDeployment` and `ServiceSet` resources and reads
  `ServiceTemplate`s from it.
- Cloud credentials and `ClusterTemplate`s registered in k0rdent for the
  providers you want Colonies to provision on.
- For workspace access over HTTP or TCP: a Gateway API `Gateway` (Istio by
  default) on the regional cluster. See the `operator.workspaces.*` values.

## Installing

```bash
helm install exalsius-operator \
  oci://ghcr.io/exalsius/charts/exalsius-operator \
  --version <version> \
  --namespace exalsius-system --create-namespace
```

Chart `version` and `appVersion` move together with the operator release; the
chart always installs the operator image of the same version unless
`image.tag` is set.

Then create a `Colony`, a `WorkspaceClass` and a `WorkspaceDeployment`. See the
[examples](https://github.com/exalsius/exalsius-operator/tree/main/examples).

## Upgrading

```bash
helm upgrade exalsius-operator \
  oci://ghcr.io/exalsius/charts/exalsius-operator \
  --version <version> \
  --namespace exalsius-system
```

Helm installs CRDs from the chart's `crds/` directory on first install only
and never upgrades or deletes them. When a release ships CRD changes, apply
them before upgrading:

```bash
helm pull oci://ghcr.io/exalsius/charts/exalsius-operator --version <version> --untar
kubectl apply --server-side -f exalsius-operator/crds/
```

## Uninstalling

```bash
helm uninstall exalsius-operator --namespace exalsius-system
```

CRDs and the custom resources created from them are left in place. Delete all
`WorkspaceDeployment`s and `Colony`s first if you want the operator to tear
down the workspaces and child clusters it manages; deleting the CRDs
afterwards removes the remaining objects without cleanup.

## Values

### Image and scheduling

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Operator replicas. Leader election is on, extra replicas are hot standbys. |
| `image.repository` | `ghcr.io/exalsius/exalsius-operator` | Operator image repository. |
| `image.tag` | `""` | Image tag; defaults to the chart `appVersion`. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Pull secrets for a private registry. |
| `nameOverride` | `""` | Override the chart name in resource names. |
| `fullnameOverride` | `exalsius-operator` | Override the fully qualified resource name. |
| `podAnnotations` | `{}` | Extra pod annotations. |
| `podLabels` | `{}` | Extra pod labels. |
| `resources` | `{}` | Container resource requests and limits. |
| `nodeSelector` | `{}` | Node selector for the operator pod. |
| `tolerations` | `[]` | Tolerations for the operator pod. |
| `affinity` | `{}` | Affinity rules for the operator pod. |
| `priorityClassName` | `""` | PriorityClass for the operator pod. |
| `terminationGracePeriodSeconds` | `10` | Graceful shutdown period. |
| `volumes` | `[]` | Extra volumes for the pod. |
| `volumeMounts` | `[]` | Extra volume mounts for the operator container. |

### Operator configuration

| Key | Default | Description |
| --- | --- | --- |
| `operator.leaderElection` | `true` | Enable leader election. |
| `operator.logging.development` | `true` | zap development mode (console encoding, debug level). |
| `operator.metrics.enabled` | `false` | Expose the controller-runtime metrics endpoint and create a Service for it. |
| `operator.metrics.port` | `8443` | Metrics port. |
| `operator.metrics.secure` | `true` | Serve metrics over HTTPS with authn/authz. |
| `operator.workspaces.gateway.name` | `istio-ingressgateway` | Gateway API `Gateway` on regional clusters that workspace routes attach to. |
| `operator.workspaces.gateway.namespace` | `istio-system` | Namespace of that Gateway. |
| `operator.workspaces.meshMode` | `ambient` | How workspace namespaces join the Istio mesh: `ambient`, `sidecar` or `none`. |
| `operator.workspaces.waypoint.enabled` | `true` | Route workspace namespaces through a per-child Istio ambient waypoint. |
| `operator.workspaces.waypoint.namespace` | `istio-system` | Namespace of the per-child waypoint Gateways. |
| `operator.extraArgs` | `[]` | Extra command-line arguments for the operator. |
| `operator.extraEnv` | `[]` | Extra environment variables for the operator container. |

### Security and RBAC

| Key | Default | Description |
| --- | --- | --- |
| `serviceAccount.create` | `true` | Create the ServiceAccount. |
| `serviceAccount.name` | `""` | ServiceAccount name; defaults to the fullname. Required when `create` is false. |
| `serviceAccount.automount` | `true` | Mount the ServiceAccount token. |
| `serviceAccount.annotations` | `{}` | ServiceAccount annotations. |
| `rbac.create` | `true` | Create the ClusterRole and ClusterRoleBinding. |
| `podSecurityContext` | restricted PSS | Pod security context (`runAsNonRoot`, `RuntimeDefault` seccomp). |
| `securityContext` | restricted PSS | Container security context (no privilege escalation, drop all capabilities, read-only root filesystem). |
| `livenessProbe` | `/healthz` on port 8081 | Liveness probe. |
| `readinessProbe` | `/readyz` on port 8081 | Readiness probe. |

The operator manages k0rdent `ClusterDeployment`s, `ServiceSet`s, kubeconfig
Secrets and its own resources on the management cluster, and reaches into
child clusters through their kubeconfigs. The bundled ClusterRole therefore
grants cluster-admin-equivalent permissions. Set `rbac.create=false` to supply
your own, narrower role.

## Links

- Source and issues: <https://github.com/exalsius/exalsius-operator>
- Changelog: <https://github.com/exalsius/exalsius-operator/blob/main/CHANGELOG.md>
- Workspace hub: <https://github.com/exalsius/exalsius-workspace-hub>
- exalsius CLI: <https://github.com/exalsius/exalsius-cli>

## License

Apache-2.0, see [LICENSE](./LICENSE).
