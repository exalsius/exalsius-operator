<p align="middle"><img src="./docs/assets/logo_banner.png" alt="exalsius banner" width="250"></p>

<h1 align="center">exalsius-operator</h1>

<div align="center">

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0) ![CI](https://img.shields.io/github/actions/workflow/status/exalsius/exalsius-operator/ci.yml?label=CI) [![Docker Image](https://img.shields.io/badge/docker-ghcr.io%2Fexalsius%2Fexalsius--operator-blue)](https://github.com/exalsius/exalsius-operator/pkgs/container/exalsius-operator) [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/exalsius-operator)](https://artifacthub.io/packages/search?repo=exalsius-operator)

</div>

The **exalsius-operator** is a Kubernetes operator that lets infrastructure
teams describe managed Kubernetes clusters and the application workspaces
running on them as Kubernetes resources. A **Colony** groups the clusters you
want provisioned; a single cluster can span nodes in several datacenters or
providers, with pod traffic between them encrypted by Cilium's WireGuard
integration. A **WorkspaceClass** is a catalog entry for a workspace type
such as a Jupyter notebook or an inference server, and a
**WorkspaceDeployment** asks for one instance of a class on one of those
clusters. The operator provisions the clusters through
[k0rdent](https://k0rdent.io) and installs the workspaces through k0rdent's
Sveltos integration. Everything works with plain `kubectl`; the exalsius CLI
and API build on the same resources but are not required.

**New to exalsius?** Follow the [quick start](docs/quickstart.md): two
SSH-reachable Linux nodes become a management cluster, a Colony-provisioned
child cluster, and a running Jupyter workspace in about 20 minutes.

**Already running k0rdent?** Install the operator with the
[Helm chart](charts/exalsius-operator/README.md) and follow its
"After installing" section.

## Resource model

Three custom resources map onto k0rdent objects, which in turn drive the
provisioning and deployment machinery:

```
 Colony ──────────────► ClusterDeployment (k0rdent) ──► Cluster API + k0smotron ──► child cluster
   one entry per cluster                                                            (Cilium CNI)

 WorkspaceClass ──────► ServiceTemplate (k0rdent) = the workspace's Helm chart

 WorkspaceDeployment ─► ServiceSet (k0rdent) ──► Sveltos Profile ──► Helm release on the child,
   pins one class,                                                    in namespace ws-<name>
   targets one ClusterDeployment
```

All three resources, and everything the operator creates from them, live on
the **management cluster**: the cluster where k0rdent and the operator are
installed. Colonies and WorkspaceDeployments are namespaced; WorkspaceClasses
are cluster-scoped, like a `StorageClass`. The workspace itself, a Helm release
in its own `ws-<name>` namespace, is the only thing that lands on the **child
cluster**. The operator reaches the child through the kubeconfig Secret k0rdent
stores next to each ClusterDeployment.

[Concepts](docs/concepts.md) walks through the full model with diagrams:
how a Colony becomes clusters, how a deployment becomes a running workspace,
the phases and the GPU gate, and how workspace URLs are published through a
per-tenant regional cluster.

## What the operator does

- **Provisions and groups clusters.** Each `Colony` entry becomes a k0rdent
  `ClusterDeployment` built from an exalsius cluster template: a hosted
  control plane with Cilium as the pod network and WireGuard encryption
  between all nodes, so one cluster can spread across datacenters and cloud
  providers. The operator aggregates the child kubeconfigs into one Secret
  per Colony and publishes each child's GPU inventory on the Colony status.
- **Publishes a workspace catalog.** `WorkspaceClass` pins a k0rdent
  `ServiceTemplate`, declares default resources, prerequisites the target
  cluster needs first, access endpoints, and the configuration prompts users
  see.
- **Deploys workspaces.** For each `WorkspaceDeployment` the operator resolves
  the class, installs the prerequisites the class declares if the target
  cluster lacks them, gates on GPU capacity, creates one k0rdent `ServiceSet`
  per workspace, and reports phase, conditions and access URLs on the
  resource's status.
- **Tears down.** Deleting a WorkspaceDeployment uninstalls the release;
  deleting a Colony removes its clusters.

## What the wider exalsius stack adds

The operator is one component of the [exalsius stack](https://github.com/exalsius).
On top of it, the [exalsius-api](https://api.exalsius.ai/docs) turns requests
from the [exalsius CLI](https://github.com/exalsius/exalsius-cli) and the
hosted platform into these resources and adds multi-cloud node acquisition,
cost-aware placement and tenant management. The CLI and platform journey is
documented at <https://docs.exalsius.ai>; this repository documents the
operator and its resources.

## Workspaces

A **workspace** is a ready-to-use application environment, such as a Jupyter
notebook server, a training stack or an inference server, installed as a Helm
release onto one of the clusters the operator manages. Two CRDs in the
`workspaces.exalsius.ai/v1` API group split the responsibility between platform
admins and end users:

* **WorkspaceClass** (cluster-scoped, `wsc`) is an admin-authored catalog
  entry describing a workspace *type*. It pins the k0rdent ServiceTemplate to
  deploy, declares the default resource shape (replicas × CPU/memory/GPU per
  replica), lists prerequisites that must be healthy on the target cluster
  first, and defines the access endpoints the workspace exposes and the config
  options users may set. Classes are versioned and immutable: the name carries
  the version, so a running workspace's definition never changes underneath
  it. Ready-made classes and their charts live in the
  [exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub).

* **WorkspaceDeployment** (namespaced, `wsd`) captures a user's intent to run
  one instance of a class on a specific target cluster. It references a
  WorkspaceClass and a k0rdent ClusterDeployment, optionally overriding
  resources and Helm values.

A deployment moves through `Pending → InstallingPrerequisites → Deploying →
Running`, with `Waiting` when the requested GPU capacity is temporarily
exhausted, `Failed` on errors, and `Deleting` on teardown. Each workspace is
deployed and tracked through its own k0rdent ServiceSet, so independent
workspaces never write to shared fields of the ClusterDeployment.

The WorkspaceDeployment from the quick start, which targets the child cluster
`quickstart-child-1` provisioned there:

```yaml
apiVersion: workspaces.exalsius.ai/v1
kind: WorkspaceDeployment
metadata:
  name: my-notebook
  namespace: kcm-system
spec:
  workspaceClassRef: jupyter-notebook-0-3-0
  clusterDeploymentRef:
    name: quickstart-child-1
    namespace: kcm-system
  resources:
    perReplica:
      cpu: "2"
      memory: "4Gi"
      storage: "10Gi"
  values:
    notebookPassword: "change-me-please"
```

The runnable copy lives in
[`examples/quickstart/workspacedeployment.yaml`](examples/quickstart/workspacedeployment.yaml).

## Documentation

- [Quick start](docs/quickstart.md): two SSH nodes to a running workspace.
- [Concepts](docs/concepts.md): the resource model, the reconcilers and the
  cluster topology, with diagrams.
- [Helm chart](charts/exalsius-operator/README.md): install, upgrade,
  values and RBAC for an existing k0rdent cluster.
- [docs.exalsius.ai](https://docs.exalsius.ai): the CLI and hosted platform.

## Contributing
We welcome contributions! Please check the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
