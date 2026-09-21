# exalsius-operator

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/exalsius-operator)](https://artifacthub.io/packages/search?repo=exalsius-operator)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

The **exalsius-operator** lets infrastructure teams describe managed
Kubernetes clusters and the application workspaces running on them as
Kubernetes resources, provisioned through [k0rdent](https://k0rdent.io). This
chart installs the operator and its CRDs on a k0rdent **management cluster**.

**New to exalsius?** Start with the
[quick start](https://github.com/exalsius/exalsius-operator/blob/main/docs/quickstart.md),
which bootstraps a management cluster on a fresh machine, installs this chart
and walks to a running workspace.

**Already running k0rdent?** This page is for you: install below, then follow
[After installing](#after-installing).

## What the operator does

| CRD | Scope | Purpose |
| --- | --- | --- |
| `Colony` (`infra.exalsius.ai/v1`) | Namespaced | Groups one or more child clusters, possibly across cloud providers. Each entry becomes a k0rdent `ClusterDeployment`; the operator aggregates kubeconfigs and publishes the GPU inventory of every child. |
| `WorkspaceClass` (`workspaces.exalsius.ai/v1`) | Cluster | Catalog entry for a workspace type (Jupyter, LLM endpoint, Slurm, ...). Pins a k0rdent `ServiceTemplate`, resource defaults, prerequisites, access endpoints and user-facing configuration prompts. |
| `WorkspaceDeployment` (`workspaces.exalsius.ai/v1`) | Namespaced | A request to run a workspace of a class on a target child cluster. The operator installs the prerequisites the class declares, gates on GPU capacity, creates one k0rdent `ServiceSet` per workspace, and reports readiness and access URLs. |

Ready-made WorkspaceClasses and their Helm charts live in the
[exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub).
How the resources map onto k0rdent, Cluster API and Sveltos is explained in
[Concepts](https://github.com/exalsius/exalsius-operator/blob/main/docs/concepts.md).

## Prerequisites

### To install the operator

- Kubernetes >= 1.28 and Helm >= 3.8 (OCI support).
- [k0rdent](https://docs.k0rdent.io) (KCM) installed on the management
  cluster, including its `ksm-projectsveltos` state management provider. The
  operator creates `ClusterDeployment` and `ServiceSet` resources and reads
  `ServiceTemplate`s from it.

### To deploy workspaces

- A k0rdent `ClusterTemplate` and `Credential` for each place you want
  Colonies to provision clusters: a cloud provider's template and credentials
  registered per the k0rdent documentation, or the exalsius
  `exalsius-remote-cluster` template with an SSH key for bare machines.
- A `ServiceTemplate` and `WorkspaceClass` for each workspace type, for
  example from the workspace hub.

[After installing](#after-installing) shows both for the bare-machine case.

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

The chart installs only the operator and its CRDs. It does not install
k0rdent, cluster templates, credentials or workspace catalogs; on an existing
management cluster those are yours to register, as shown next. The
quick start's bootstrap script is for fresh machines only and must not be run
against an existing cluster.

## After installing

Register what a first Colony and workspace need, then apply both. The
commands below assume k0rdent's namespace `kcm-system`; the `ClusterTemplate`,
`Credential` and `ServiceTemplate` must live in the same namespace as the
`Colony` and `WorkspaceDeployment` that reference them.

### 1. Register a cluster template

For bare SSH-reachable machines, the `exalsius-remote-cluster` template
provisions a k0smotron-hosted control plane with Cilium as the network layer.
Its versions are listed at
<https://github.com/exalsius/cluster-templates>; the template name is the
chart name with the version's dots replaced by dashes.

```bash
kubectl apply -f - <<'YAML'
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: exalsius-cluster-templates
  namespace: kcm-system
  labels:
    k0rdent.mirantis.com/managed: "true"
spec:
  type: oci
  interval: 10m
  url: oci://ghcr.io/exalsius/cluster-templates/charts
---
apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterTemplate
metadata:
  name: exalsius-remote-cluster-0-1-5
  namespace: kcm-system
spec:
  helm:
    chartSpec:
      chart: exalsius-remote-cluster
      version: 0.1.5
      interval: 10m
      reconcileStrategy: ChartVersion
      sourceRef:
        kind: HelmRepository
        name: exalsius-cluster-templates
YAML
kubectl -n kcm-system wait clustertemplate/exalsius-remote-cluster-0-1-5 \
  --for=jsonpath='{.status.valid}'=true --timeout=5m
```

For cloud providers, register the provider's `ClusterTemplate` and
`Credential` as described in the
[k0rdent documentation](https://docs.k0rdent.io) and skip step 2.

### 2. Create a credential

The remote-cluster template provisions workers over SSH. Store the private
key in a Secret whose data key is `value` and wrap it in a k0rdent
`Credential`. The key is readable by anyone who can read Secrets in that
namespace, so use a key dedicated to cluster provisioning.

```bash
kubectl -n kcm-system create secret generic remote-ssh-key \
  --from-file=value=<path to private key> --dry-run=client -o yaml \
  | kubectl label --local -f - k0rdent.mirantis.com/component=kcm -o yaml \
  | kubectl apply -f -

kubectl apply -f - <<'YAML'
apiVersion: k0rdent.mirantis.com/v1beta1
kind: Credential
metadata:
  name: remote-cred
  namespace: kcm-system
spec:
  description: SSH key for remote worker nodes
  identityRef:
    apiVersion: v1
    kind: Secret
    name: remote-ssh-key
    namespace: kcm-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: remote-ssh-key-resource-template
  namespace: kcm-system
  labels:
    k0rdent.mirantis.com/component: "kcm"
  annotations:
    projectsveltos.io/template: "true"
YAML
```

The empty ConfigMap is a k0rdent convention: every Credential is paired with a
resource-template ConfigMap named `<secret>-resource-template`.

### 3. Install a workspace catalog entry

The workspace hub publishes, per chart version, a rendered `ServiceTemplate`
and `WorkspaceClass`. Both expect a `HelmRepository` named
`exalsius-workspace-hub` in their namespace. For the Jupyter Notebook class:

```bash
kubectl apply -f - <<'YAML'
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: exalsius-workspace-hub
  namespace: kcm-system
  labels:
    k0rdent.mirantis.com/managed: "true"
spec:
  type: oci
  interval: 10m
  url: oci://ghcr.io/exalsius/exalsius-workspace-hub/charts
YAML

HUB=https://raw.githubusercontent.com/exalsius/exalsius-workspace-hub/main/manifests
kubectl apply -f $HUB/jupyter-notebook/0.3.0/servicetemplate.yaml
kubectl apply -f $HUB/jupyter-notebook/0.3.0/workspaceclass.yaml
kubectl get workspaceclass jupyter-notebook-0-3-0
```

The class is valid once the `VALID` column reads `true`. Other classes are
listed in the hub's
[`manifests/`](https://github.com/exalsius/exalsius-workspace-hub/tree/main/manifests)
directory.

### 4. Apply a Colony and a WorkspaceDeployment

Use the quick start's
[`examples/quickstart/`](https://github.com/exalsius/exalsius-operator/tree/main/examples/quickstart)
as the starting point: its `colony.yaml` provisions one child cluster onto one
SSH-reachable machine with the template and credential above, and its
`workspacedeployment.yaml` runs the Jupyter Notebook class on that child.
Adjust the addresses, machine list and resources, then:

```bash
kubectl apply -f colony.yaml
kubectl get clusterdeployment -n kcm-system -w      # wait for READY = True

kubectl apply -f workspacedeployment.yaml
kubectl get wsd -n kcm-system -w                    # wait for PHASE = Running
```

Steps 2 to 4 of the
[quick start](https://github.com/exalsius/exalsius-operator/blob/main/docs/quickstart.md#step-2-provision-a-child-cluster-with-a-colony)
walk through the same two resources with expected output, checkpoints and
troubleshooting.

## Workspace access

A workspace's chart exposes a `ClusterIP` Service on the child cluster. What
you need beyond this chart depends on how you want to reach it:

- **Port-forward** needs nothing extra. `kubectl port-forward` against the
  child cluster reaches the Service; the WorkspaceDeployment reports
  `Running` with an empty URL and a routing condition of
  `RoutingInfraNotReady`, which is expected in this setup.
- **Published URLs** need a **regional cluster**: a per-tenant hub cluster
  that the child is attached to and that runs a Gateway API `Gateway` (Istio
  by default) for the tenant's domain, plus an Istio mesh spanning the
  tenant's clusters. The operator only attaches routes to that Gateway; the
  chart does not install any of it. The `operator.workspaces.*` values name
  the Gateway and mesh mode the operator expects. The model is described in
  [Concepts](https://github.com/exalsius/exalsius-operator/blob/main/docs/concepts.md#workspace-access).

## Chart compatibility and tested stack

The chart declares Kubernetes >= 1.28 and is expected to work on any k0rdent
management cluster that meets the prerequisites above. The combination the
project actually verifies end to end is the quick start's pinned stack in
[`hack/quickstart/versions.env`](https://github.com/exalsius/exalsius-operator/blob/main/hack/quickstart/versions.env):
one k0s, KCM, cluster-template and workspace-hub version per operator release,
re-run on fresh machines whenever a pin moves.

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

- Quick start: <https://github.com/exalsius/exalsius-operator/blob/main/docs/quickstart.md>
- Concepts: <https://github.com/exalsius/exalsius-operator/blob/main/docs/concepts.md>
- Source and issues: <https://github.com/exalsius/exalsius-operator>
- Changelog: <https://github.com/exalsius/exalsius-operator/blob/main/CHANGELOG.md>
- Workspace hub: <https://github.com/exalsius/exalsius-workspace-hub>
- exalsius CLI: <https://github.com/exalsius/exalsius-cli>

## License

Apache-2.0, see [LICENSE](./LICENSE).
