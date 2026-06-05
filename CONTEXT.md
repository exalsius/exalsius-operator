# Domain Context: exalsius-operator

## Ubiquitous Language

### Workspace
The user-facing concept for a running application instance on a child cluster (e.g., "my Jupyter workspace"). Represented in Kubernetes as a **WorkspaceDeployment** CR. The CLI and user-facing documentation use "workspace"; the operator and API use "WorkspaceDeployment." They refer to the same thing at different abstraction levels.

### WorkspaceClass
A cluster-scoped catalog entry defining a type of workspace — its Helm chart, resource defaults, prerequisites, access endpoints, and user-facing configuration prompts. Created by platform engineers. Users reference a WorkspaceClass by name when creating a workspace. Analogous to `StorageClass` or `IngressClass`.

### Tenant
An organization using the exalsius platform. Synonymous with "org" (the API/auth layer says "org"; infrastructure discussions say "tenant"). Each tenant has exactly one Regional Cluster and one dedicated Tenant Domain (e.g. `dos-lab.ex.ls`).

### Regional Cluster
The per-tenant hub cluster. Hosts shared tenant infrastructure — most importantly the gateway that terminates TLS for the Tenant Domain — and is connected to the tenant's Child Clusters. A Child Cluster belongs to exactly one Regional Cluster.

### Child Cluster
A cluster provisioned to run workloads (workspaces). Attached to a Regional Cluster; not directly reachable from outside. Represented by a ClusterDeployment.

### Tenant Domain
The DNS domain dedicated to a tenant (e.g. `dos-lab.ex.ls`). A workspace's primary endpoint (its first declared HTTP endpoint) is reachable at `https://<workspace-name>.<tenant-domain>`; additional endpoints at `https://<workspace-name>-<endpoint-name>.<tenant-domain>`. Hostnames stay one label deep so a single per-tenant wildcard certificate covers them all. Non-HTTP endpoints (SSH, raw TCP) are reachable on dedicated high ports of the tenant domain itself (e.g. `ssh://dos-lab.ex.ls:2207`), drawn from a per-tenant Port Pool. Because tenant, regional cluster, and domain are 1:1:1, the hostname identifies both the workspace and where its traffic enters.

### Port Pool
A fixed range of TCP ports on a tenant's gateway, reserved for non-HTTP workspace endpoints. Provisioned by admins as tenant infrastructure; workspaces are assigned a free port for the lifetime of the workspace and release it on deletion. Pool exhaustion is a visible, per-tenant capacity limit.

### Workspace Namespace
The dedicated namespace a workspace's workload runs in on its Child Cluster (`ws-<workspace-name>`). One per workspace — the unit of isolation, mesh visibility, and cleanup. Unique across a tenant's entire mesh by construction. Prerequisites do not live in Workspace Namespaces; they are cluster-local shared infrastructure.

### Colony
A logical grouping of Kubernetes clusters managed together, potentially spanning cloud providers. Owns one or more ClusterDeployments via k0rdent. May include NetBird VPN networking for cross-cloud connectivity.

### ClusterDeployment
A k0rdent resource representing a provisioned child cluster. Owned by a Colony (via ownerReference). The workspace controller references it to know which cluster to deploy to, but does not write to it — workspace services are deployed via standalone ServiceSets.

### ServiceSet
A k0rdent resource that triggers deployment of Helm charts onto a child cluster via Sveltos. Each workspace gets its own ServiceSet (one per WorkspaceDeployment), avoiding shared writes to the ClusterDeployment. The ServiceSet references the ClusterDeployment by name for credential resolution.

### ServiceTemplate
A k0rdent resource wrapping a Helm chart reference. WorkspaceClasses reference ServiceTemplates to define which chart to deploy.
