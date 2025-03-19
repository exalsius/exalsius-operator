<h1 align="center">
  <br>
  <img height="300" src="docs/assets/logo.png"> <br>
    exalsius-operator
<br>
</h1>

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

## Installation
### Prerequisites
- Kubernetes Cluster (either a local Kind cluster or a remote cluster)
- Kubectl
- Helm
- Cloud provider credentials (if provisioning cloud-based resources)

### Local Kind Cluster Installation

The operator can either be installed in a local Kind cluster or in any other available Kubernetes cluster.

To create a local test environment using Kind, the following script can be used:
```sh
cd hack
./start-dev-env.sh
```

### Install via Helm
The **exalsius-operator**, together with its needed dependencies, can be installed using Helm with the provided `install-via-helm.sh` script. A `values.yaml` file with custom configuration can be used, e.g.,

```yaml
# Values for the actual operator chart.
# See charts/exalsius-operator/values.yaml for more information.
operator:
  enabled: true

# Credentials for the AWS provider.
aws:
  enabled: true
  awsCredentials:
    accessKeyId: XXXX
    secretAccessKey: XXXX
    region: eu-central-1

skypilot-nightly:
  enabled: true
  awsCredentials:
    enabled: true
  ingress:
    #  Credentials for Skypilot API service, create with
    # export WEB_USERNAME=skypilot
    # export WEB_PASSWORD=yourpassword
    # AUTH_STRING=$(htpasswd -nb $WEB_USERNAME $WEB_PASSWORD)
    authCredentials: skypilot:$apr1$zV6AUMNr$wZvIpvvDbPQmZuOn7mk5Q0

# if the nvidia operator is not needed on the management cluster, it can be disabled
nvidia-operator:
  enabled: false

```

```sh
cd hack
./install-via-helm.sh ../values.yaml
```


After the installation is done, the following CRDs can be used:

## Usage
### Deploy a Colony
A colony can be defined as follows:

```yaml
#
# This example creates a colony with 2 nodes in us-east-1 using a g5.xlarge instance type.
#
apiVersion: infra.exalsius.ai/v1
kind: Colony
metadata:
  name: test-colony
spec:
  clusterName: test-cluster
  k8sVersion: v1.30.3+k0s.0
  aws:
    replicas: 2
    ami: ami-084568db4383264d4 # us-east-1
    region: us-east-1
    instanceType: g5.xlarge
    sshKeyName: exalsius
    iamInstanceProfile: nodes.cluster-api-provider-aws.sigs.k8s.io
```

It can be deployed using `kubectl`:

```sh
kubectl apply -f colony.yaml
```

The operator will automatically provision the resources and install the necessary dependencies.

### Check status of the Colony
The status of the colony can be checked using `kubectl`:

```sh
# show all available colonies
kubectl get colonies

# show the status of a specific colony
kubectl get colonies <colony-name>
```

### Delete a Colony
A colony can be deleted using `kubectl`:

```sh
kubectl delete colony <colony-name>
```


### Submit a Job
Currently we support Pytorch Distributed Data Parallel (DDP) jobs. A job can be submitted as
follows:

```yaml
apiVersion: training.exalsius.ai/v1
kind: DDPJob
metadata:
  name: test-ddp-job
spec:
  # The colony to run the job on
  targetColony: test-colony
  # We want to run the job on 2 nodes
  parallelism: 2
  # each node should have 1 GPU
  nprocPerNode: 1  # Use 1 GPUs per node (pod)
  # Use the following container image
  image: <docker-image>
  # The path to the script to run
  scriptPath: "/app/training/trainer.py"
  # The API key for Weights and Biases
  wandbApiKey: "XXXXXXXXX"
  # The arguments to pass to the script
  args:
    - "--per_device_train_batch_size"
    - "16"
    - "--batch_size"
    - "256"
    - "--lr"
    - "1e-3"
```

It can be submitted using `kubectl`:

```sh
kubectl apply -f job.yaml
```

Using exalsius-cli, the job description can be further extended to include more information, e.g., with GPU types the user would like to use for the job. Based on this extended job description, the operator will automatically provision the infrastructure and submit the job, the user does not need to specify the target colony in that case. See [exalsius-cli](https://github.com/exalsius/exalsius-cli) for more information.

### Check status of the Job
The status of the job can be checked using `kubectl`:

```sh
# show all available jobs
kubectl get ddpjobs

# show the status of a specific job
kubectl get ddpjobs <job-name>
```

### Delete a Job
A job can be deleted using `kubectl`:

```sh
kubectl delete ddpjob <job-name>
```

## exalsius CLI

**exalsius-operator** works seamlessly with the **exalsius-cli** to simplify cluster creation and GPU cost optimization:

```sh
exalsius jobs submit job.yaml
```

For more information on the exalsius-cli, please refer to the [exalsius-cli README](https://github.com/exalsius/exalsius-cli/blob/main/README.md).


## Roadmap

- [ ] Support for additional cloud providers
- [ ] Integration and support for furter distributed AI training frameworks and jobs
- [ ] Improved auto-shutdown policies
- [ ] Advanced scheduling policies for distributed workloads



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

