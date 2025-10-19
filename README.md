<p align="left"><img src="./docs/assets/logo_banner.png" alt="exalsius banner" width="250"></p>

# exalsius-operator

## Features
The **exalsius-operator** is a Kubernetes operator that extends Kubernetes with **Custom Resource Definitions (CRDs)** to facilitate the **creation of further ephemeral Kubernetes clusters** and **installation of dependencies** necessary for distributed AI training workloads across **public cloud providers** and **on-premise machines**.

With **exalsius**, AI practitioners and engineers can dynamically provision infrastructure, execute machine learning workloads, and efficiently manage resources while minimizing cloud costs through **automatic resource termination** after job completion.

## Features

- **Colony-Based Resource Management**: Define and manage **Colony** resources, consisting of Kubernetes clusters spanning multiple cloud providers or on-premise hosts.
- **Ephemeral AI Clusters**: Automatically launch and tear down **short-lived** Kubernetes clusters for AI workloads, optimizing cloud cost efficiency.
- **Automated Dependency Installation**: Deploy key components required for **distributed AI training**, such as:
  - **NVIDIA CUDA drivers**
  - **Volcano for gang scheduling**
  - **Observability stack (Prometheus, Grafana, etc.)**
  - **Distributed ML frameworks (Ray, Kubeflow, etc.)**
- **Multi-Cloud GPU Cost Optimization**: Deploy AI workloads on the cheapest available GPUs across **AWS, Azure, GCP, and other providers**, using [exalsius-cli](https://github.com/exalsius/exalsius-cli).

## Architecture

**exalsius-operator** follows a **declarative approach** to infrastructure management using Kubernetes CRDs:

1. **User defines a Colony CRD**, specifying compute resources and dependencies.
2. **Exalsius Operator provisions cloud/on-premise resources** and sets up AI infrastructure.
3. **User specifies a training job (e.g. a DDPJob CRD)**, specifying the AI workload to execute and the colony infrastructure to use.
3. **AI workload is executed** on the provisioned infrastructure.
4. **Cloud resources are automatically terminated** after job completion to save costs.

In addition, [exalsius-cli](https://github.com/exalsius/exalsius-cli) can be used to submit a job to the operator which automatically provisions the infrastructure - based on requirements provided by the user - and executes the job

TBA

## Contributing
We welcome contributions! Please check the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines.

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

# Trigger release-please for label fix
