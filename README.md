<p align="middle"><img src="./docs/assets/logo_banner.png" alt="exalsius banner" width="250"></p>

<h1 align="center">exalsius-operator</h1>

<div align="center">

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0) ![CI](https://img.shields.io/github/actions/workflow/status/exalsius/exalsius-operator/ci.yml?label=CI) [![Docker Image](https://img.shields.io/badge/docker-ghcr.io%2Fexalsius%2Fexalsius--operator-blue)](https://github.com/exalsius/exalsius-operator/pkgs/container/exalsius-operator)

</div>


The **exalsius-operator** is a **Kubernetes operator** that extends Kubernetes with **Custom Resource Definitions (CRDs)** for dynamic, multi-cloud orchestration of **ephemeral AI clusters** and **distributed training workloads**.

As one of the **core backbone components** of the [**exalsius stack**](https://github.com/exalsius), it provides the control-plane intelligence required to **automate infrastructure provisioning**, **dependency installation**, and **lifecycle management** of training environments across **public cloud** and **on-premise** resources.

With **exalsius**, AI practitioners and engineers can:

* Dynamically provision complete, ready-to-train Kubernetes clusters
* Automatically install all required GPU and ML framework dependencies
* Execute and monitor distributed workloads across multiple providers

Together with [**exalsius-cli**](https://github.com/exalsius/exalsius-cli) and [**exalsius-api**](https://api.exalsius.ai/docs), the operator forms the foundation of a **modular, decentralized, and cost-efficient AI training platform**.

> **Quick start:** [docs/quickstart.md](docs/quickstart.md) takes two SSH-reachable Linux nodes to a management cluster, a Colony-provisioned child cluster, and a running Jupyter workspace in about 20 minutes.


## Key Features

* **Colony-Based Resource Management**
  Define and manage **Colony** resources, logical groups of Kubernetes clusters that may span multiple cloud providers or on-premise hosts.

* **Ephemeral AI Clusters**
  Automatically launch and tear down **short-lived clusters** for AI workloads, reducing idle infrastructure and optimizing cost.

* **Automated Dependency Installation**
  Deploy key components required for distributed AI training, including:

  * **NVIDIA CUDA drivers**
  * **AMD ROcm drivers**
  * **Observability stacks** (Prometheus, Grafana, etc.)
  * **Distributed ML frameworks** (Ray, Kubeflow, etc.)

* **Multi-Cloud GPU Cost Optimization**
  Deploy workloads on the most cost-efficient GPUs across **AWS**, **Azure**, **GCP**, and on-premise environments using [**exalsius-cli**](https://github.com/exalsius/exalsius-cli).

* **Workspace Catalog & Deployments**
  Offer ready-to-use application environments (Jupyter notebooks, training stacks, inference servers) as an admin-curated catalog of **WorkspaceClass** entries that users instantiate on any provisioned cluster via **WorkspaceDeployment** resources — see [Workspaces](#workspaces).

## Declarative Workflow
The **exalsius-operator** follows a **declarative approach** to multi-cluster infrastructure management using Kubernetes **Custom Resource Definitions (CRDs)**.

1. **User initiates cluster creation via the CLI**
   The process starts with the [**exls CLI application**](https://github.com/exalsius/exalsius-cli), where the user defines requirements such as on-premise machines, cloud provider, region, instance type, and Kubernetes version.

2. **exalsius-api generates and applies CRDs**
   The **exalsius-api** receives the request, translates it into Kubernetes CRDs, and submits them to the cluster where the operator is running.

3. **exalsius-operator reconciles CRDs and provisions infrastructure**
   The operator uses **Cluster API (CAPI)** and higher-level abstractions such as the **K0rdent `ClusterDeployment` CRD** to create and configure the target clusters on the selected cloud or on-premise environment.

4. **Colony resources logically group clusters**
   A `Colony` CRD provides a high-level abstraction that **bundles multiple clusters** across different geographical locations or cloud providers into a unified logical group for management and coordination.

5. **Workspaces are deployed onto the provisioned clusters**
   Users pick a **WorkspaceClass** from the catalog and create **WorkspaceDeployment** resources; the operator installs the corresponding application (e.g. a Jupyter environment) onto the target cluster and publishes its access endpoints (see [Workspaces](#workspaces)).

6. **Lifecycle management and teardown**
   The operator continuously reconciles the desired and actual states of all managed clusters, and can **automatically delete** clusters when they are no longer needed, minimizing operational and cloud costs.

### Integration within the exalsius Stack

The **exalsius-operator** forms a central backbone component of the **exalsius stack**, working in close coordination with the **exalsius-api** and **exalsius-cli**:

* **[exalsius-cli](https://github.com/exalsius/exalsius-cli)**:
  A user-facing command-line interface for defining requirements and initiating cluster creation. It communicates with the **exalsius-api** to manage lifecycle operations declaratively.

* **[exalsius-api](https://api.exalsius.ai/docs)**: 
  A backend service that exposes APIs for cluster orchestration and management. It schedules and translates user requests from the CLI into Kubernetes CRDs and communicates with the exalsius-operator to reconcile and manage resources.


* **exalsius-operator**
  The control-plane component responsible for reconciling CRDs and turning declarative specifications into running Kubernetes clusters. It leverages **Cluster API** and **K0rdent abstractions** to provision and manage multi-cloud and on-premise clusters.

Together, these components enable **declarative, automated, and cost-efficient multi-cloud cluster orchestration** for AI and data-intensive workloads.

## Workspaces

A **workspace** is a ready-to-use application environment — a Jupyter notebook server, a distributed training stack, an inference server — installed as a Helm release onto one of the clusters the operator manages. Two CRDs in the `workspaces.exalsius.ai/v1` API group split the responsibility between platform admins and end users:

* **WorkspaceClass** (cluster-scoped, `wsc`) is an admin-authored **catalog entry** describing a workspace *type*. It pins the k0rdent **ServiceTemplate** (i.e. the Helm chart) to deploy, declares the default resource shape (replicas × CPU/memory/GPU per replica), lists **prerequisites** that must be healthy on the target cluster first (e.g. a GPU operator), and defines the **access endpoints** the workspace exposes as well as the config options users may set. Admins apply classes to the management cluster; users browse them as a catalog.

* **WorkspaceDeployment** (namespaced, `wsd`) captures a **user's intent** to run one instance of a class on a specific target cluster. It references a WorkspaceClass and a k0rdent `ClusterDeployment`, optionally overriding resources and Helm values. End users can create deployments with `kubectl` or through the exalsius API/CLI.

A deployment moves through the phases `Pending → InstallingPrerequisites → Deploying → Running` (with `Waiting` when GPU capacity is temporarily exhausted, `Failed` on errors, and `Deleting` on teardown). The operator publishes the phase, detailed conditions, and the resolved access URLs on the WorkspaceDeployment's `status`, so clients only ever need to watch the CR.

Under the hood, each WorkspaceDeployment becomes its own k0rdent **ServiceSet** on the management cluster; k0rdent drives a **Sveltos** profile that installs the class's Helm chart onto the target child cluster and reports readiness back via the `ClusterDeployment` status. Before deploying, the operator validates the class's prerequisites against the target cluster and installs any that are missing. One ServiceSet per workspace keeps independent workspaces isolated from each other.

A minimal WorkspaceDeployment:

```yaml
apiVersion: workspaces.exalsius.ai/v1
kind: WorkspaceDeployment
metadata:
  name: my-notebook
  namespace: default
spec:
  workspaceClassRef: jupyter
  clusterDeploymentRef:
    name: my-colony-cluster-1
    namespace: kcm-system
```

See [docs/quickstart.md](docs/quickstart.md) for an end-to-end walkthrough from bare SSH nodes to a running workspace.

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
