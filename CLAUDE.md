# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

exalsius-operator is a Kubernetes operator that orchestrates ephemeral AI clusters and distributed training workloads across multi-cloud environments. It provides declarative infrastructure provisioning via Custom Resource Definitions (CRDs) for the exalsius stack.

## Build and Development Commands

```bash
# Build manager binary
make build

# Run operator locally (against ~/.kube/config cluster)
make run

# Generate CRDs and RBAC manifests (run after modifying *_types.go)
make manifests

# Generate DeepCopy methods (run after modifying *_types.go)
make generate

# Format and vet code
make fmt && make vet

# Lint code
make lint
make lint-fix    # with auto-fixes

# Run unit tests
make test

# Run e2e tests (requires Kind cluster running)
make test-e2e

# Build Docker image
make docker-build IMG=<registry/image:tag>

# Deploy to cluster
make deploy IMG=<registry/image:tag>

# Generate consolidated install.yaml
make build-installer
```

## Running Individual Tests

```bash
# Run specific test file
go test ./internal/controller/infra/... -v

# Run specific test by name
go test ./internal/controller/infra/... -v -run TestColonyReconciler

# Run with Ginkgo (e2e style)
go test ./test/e2e/ -v -ginkgo.v -ginkgo.focus="pattern"
```

## Architecture

### CRDs (api/)

- **Colony** (`infra.exalsius.ai/v1`): Logical grouping of clusters across cloud providers. Manages ClusterDeployments via K0rdent, NetBird VPN integration, and multi-cluster kubeconfig aggregation.

- **WorkspaceClass** (`workspaces.exalsius.ai/v1`, cluster-scoped): Catalog entry for a workspace type. Pins a k0rdent ServiceTemplate, declares resource shape (SingleNode/MultiNode), default resources, prerequisites, access endpoints, user-facing config prompts, and deploy timeout.

- **WorkspaceDeployment** (`workspaces.exalsius.ai/v1`, namespaced): User intent to deploy a workspace instance onto a target ClusterDeployment. Lifecycle phases: Pending → Deploying → Running → Failed → Deleting.

### Controllers (internal/controller/)

- **ColonyReconciler** (`infra/colony_controller.go`): Main controller for cluster provisioning. Creates/deletes ClusterDeployment resources, manages NetBird setup (keys, policies, groups), configures Cilium CNI, and aggregates kubeconfigs for multi-cluster access.

- **WorkspaceDeploymentReconciler** (`workspaces/workspacedeployment_controller.go`): Resolves WorkspaceClass, validates prerequisites against `ClusterDeployment.status.services[]`, merges class defaults with user overrides, and creates a **ServiceSet** per workspace on the management cluster. Polls readiness every 15s; deletes the ServiceSet on teardown so k0rdent garbage-collects the Sveltos Profile.

- **RemoteMachineCleanupReconciler** (`infra/remotemachine_controller.go`): Watches K0smotron RemoteMachine resources for NetBird cleanup on deletion. Coordinates with k0smotron finalizer ordering.

### Key Integration Packages (internal/controller/infra/)

- `netbird/`: NetBird API client for VPN overlay networking
- `cilium/`: Cilium CNI configuration for child clusters
- `clusterdeployment/`: K0rdent ClusterDeployment helpers

### Webhook (internal/webhook/)

- **RemoteMachineDefaulter**: Mutating webhook that pre-initializes NetBird cleanup finalizer on RemoteMachine resources to resolve race conditions with k0smotron.

### External Dependencies

- **K0rdent/KCM**: ClusterDeployment and ServiceSet provider (wraps Cluster API)
- **K0smotron**: Lightweight Kubernetes distribution and infrastructure
- **Sveltos** (via k0rdent StateManagementProvider `ksm-projectsveltos`): Deploys Helm charts onto child clusters based on ServiceSets
- **NetBird**: VPN overlay for cross-cloud cluster connectivity
- **Cilium**: CNI for networking

### Multi-Cluster Pattern

The operator runs on a management cluster and provisions child clusters via K0rdent. Cross-cluster operations use kubeconfig secrets stored in the management cluster. NetBird provides the network overlay for inter-cluster communication.

### Workspace Deployment Pattern

Each `WorkspaceDeployment` creates its own k0rdent `ServiceSet` named `wsd-<clusterdeployment-name>-<workspace-name>` on the management cluster. K0rdent picks it up (matched via label `ksm.k0rdent.mirantis.com/adapter: kcm-controller-manager`) and drives a Sveltos Profile that installs the Helm chart on the child cluster. Status flows back into `ClusterDeployment.status.services[]` via k0rdent's field index. One ServiceSet per workspace keeps independent workspaces isolated and avoids multi-writer races on shared ClusterDeployment fields.

### Finalizer Strategy

- Colony: `colony.infra.exalsius.ai/finalizer`
- WorkspaceDeployment: `workspaces.exalsius.ai/deployment`
- RemoteMachine cleanup: `netbird.exalsius.ai/cleanup`

## Deployment

Helm chart at `charts/exalsius/` with operator subchart at `charts/exalsius/charts/operator/`.

## Agent skills

### Issue tracker

Issues live as local markdown files under `.scratch/<feature>/` in this repo. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
