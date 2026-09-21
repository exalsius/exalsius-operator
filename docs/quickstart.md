# Quick start: from two SSH nodes to a running workspace

In about 20 minutes you will turn two Linux machines into an exalsius setup:
a **management cluster** running k0rdent and the exalsius-operator, a **child
cluster** provisioned from it with a `Colony`, and a **Jupyter Notebook
workspace** deployed onto that child with a `WorkspaceDeployment`.

```
 your workstation                node 1 (management)              node 2 (worker)
 ───────────────                 ────────────────────             ────────────────
 kubectl / helm / k0sctl  ──►    k0s + k0rdent + operator         joins the child
                                 hosted child control plane  ◄──  cluster over SSH
                                 (NodePorts 30443 / 30132)        and runs Jupyter
```

**What this guide covers:** one management node, one child cluster with one
worker, one CPU-only notebook reached through `kubectl port-forward`, and a
teardown that frees both nodes. Everything is done with `kubectl` and YAML.

**What it leaves out:** GPUs and GPU scheduling, published workspace URLs and
the routing infrastructure behind them, distributed inference, acquiring cloud
VMs, and the exalsius CLI and API. [Going further](#going-further) points to
each of these.

**Tested with:** the pins in
[`hack/quickstart/versions.env`](../hack/quickstart/versions.env) (operator
0.12.0, <!-- x-release-please-version -->
KCM 1.10.0, k0s v1.33.8, k0sctl v0.32.2) on Ubuntu 24.04 and 26.04 nodes with
4 vCPU and 8 GB RAM.

## Who does what

| Installed by the setup script | Applied by you | Managed by the operator |
|---|---|---|
| k0s on node 1, OpenEBS as default StorageClass, k0rdent (KCM) with the k0smotron and Sveltos providers, the exalsius-operator, the `exalsius-remote-cluster` ClusterTemplate, a k0rdent `Credential` with your SSH key, the Jupyter Notebook `ServiceTemplate` and `WorkspaceClass` | one `Colony` (step 2), one `WorkspaceDeployment` (step 3) | the k0rdent `ClusterDeployment` for the child, the child's kubeconfig Secrets, the k0rdent `ServiceSet` that installs the notebook chart, the `ws-my-notebook` namespace on the child |

You end up with two clusters and two kubeconfigs. **The kubeconfig selects the
cluster, not the namespace:** the `Colony` and `WorkspaceDeployment` live in
namespace `kcm-system` on the management cluster, and the notebook lives in
namespace `ws-my-notebook` on the child. Every command block below starts with
a comment naming its target cluster.

## Prerequisites

**Two Linux nodes**, each:

- Ubuntu 24.04 or 26.04, 4 vCPU, 8 GB RAM and at least 20 GB of disk.
- Reachable as `root` over SSH with the **same** key from your workstation.
  The key must work non-interactively: no passphrase, or loaded into
  `ssh-agent`.
- Fresh: no Kubernetes, no container runtime, no firewall rules you did not
  set yourself.

> **Your SSH private key is copied into the management cluster.** Step 1
> stores it in Secret `remote-ssh-key` in namespace `kcm-system`, because
> k0rdent uses it to provision the worker from a pod on node 1. Create a key
> just for this guide rather than reusing your personal one:
>
> ```bash
> ssh-keygen -t ed25519 -N "" -f ~/.ssh/exalsius-quickstart
> ```
>
> and install `~/.ssh/exalsius-quickstart.pub` in `root`'s
> `authorized_keys` on both nodes.

> **Ubuntu 26.04 worker only.** Before step 2, put GNU `stat` first on the
> `PATH` of **node 2**; the k0smotron release bundled with KCM 1.10 cannot
> copy the join token past the Rust coreutils `stat` that 26.04 ships.
> Ubuntu 24.04 and node 1 need nothing. Why, and what it looks like when
> missed, is in [Troubleshooting](#troubleshooting).
>
> ```bash
> ssh -i "$SSH_KEY" root@$WORKER_NODE_IP 'ln -s /usr/bin/gnustat /usr/local/bin/stat'
> ```

**Network** between the three machines:

| From | To | Ports |
|---|---|---|
| your workstation | node 1 | 22 (SSH), 6443 (management cluster API), 30443 (child cluster API, used by every `child.kubeconfig` command from step 2 on) |
| node 1 | node 2 | 22 (SSH, the child node is provisioned from a pod on node 1) |
| node 2 | node 1 | 30443, 30132 (the child cluster's API server and konnectivity tunnel) |

Cloud VMs with public IPs and the ports above open work fine. Put the two
addresses and the key path in your shell; every command below reads them:

```bash
export MGMT_NODE_IP=203.0.113.10        # node 1, as both your workstation and node 2 reach it
export WORKER_NODE_IP=203.0.113.11      # node 2, as node 1 reaches it
export SSH_KEY=~/.ssh/exalsius-quickstart   # the key that logs in as root on both nodes
```

**On your workstation**: `bash`, `ssh`, `kubectl`, `helm`, and
[`k0sctl`](https://github.com/k0sproject/k0sctl/releases) **v0.32.2** (the
version every other pin in this guide was verified with), plus a clone of this
repository:

```bash
git clone https://github.com/exalsius/exalsius-operator.git
cd exalsius-operator
```

> Already running a k0rdent management cluster? This guide is for fresh
> machines only. Install the operator with the
> [Helm chart](../charts/exalsius-operator/README.md) and follow its
> "After installing" section instead.

## Step 1: set up the management cluster

One command turns node 1 into the management cluster. It bootstraps a single-node
k0s, installs a default StorageClass (OpenEBS), k0rdent (KCM 1.10) with only the
k0smotron and Sveltos providers, the exalsius-operator, the
`exalsius-remote-cluster` ClusterTemplate, a k0rdent `Credential` holding your
SSH key for the worker node, and the public workspace catalog with the Jupyter
Notebook class.

```bash
# workstation
hack/quickstart/setup-management.sh --node "$MGMT_NODE_IP" --ssh-key "$SSH_KEY"
```

Expect roughly 5 to 10 minutes; each step prints what it is waiting for. The
script is idempotent, so if it fails on a transient error (a slow image pull,
an SSH hiccup) just run it again. It ends with:

<!-- x-release-please-start-version -->
```
==> management cluster ready

  Management cluster:   operator 0.12.0, KCM 1.10.0, k0s v1.33.8+k0s.1  (root@203.0.113.10)
  Kubeconfig:           /path/to/exalsius-operator/quickstart-out/mgmt.kubeconfig
  Cluster template:     exalsius-remote-cluster-0-1-5   (credential: remote-cred)
  Workspace class:      jupyter-notebook-0-3-0

Next:
  export KUBECONFIG=/path/to/exalsius-operator/quickstart-out/mgmt.kubeconfig
  ...
```
<!-- x-release-please-end -->

Point `kubectl` at the new cluster for the rest of the guide:

```bash
export KUBECONFIG=$PWD/quickstart-out/mgmt.kubeconfig
```

**Checkpoint.** One Ready node, a valid ClusterTemplate, and the Jupyter
WorkspaceClass:

```bash
# management cluster
kubectl get nodes
kubectl -n kcm-system get clustertemplate exalsius-remote-cluster-0-1-5
kubectl get workspaceclass
```

```
NAME                STATUS   ROLES           AGE   VERSION
<node 1 hostname>   Ready    control-plane   6m    v1.33.8+k0s

NAME                            VALID
exalsius-remote-cluster-0-1-5   true

NAME                     DISPLAY NAME       REPLICAS   SERVICETEMPLATE          VALID   AGE
jupyter-notebook-0-3-0   Jupyter Notebook   1          jupyter-notebook-0-3-0   true    1m
```

## Step 2: provision a child cluster with a Colony

A `Colony` groups one or more clusters. The quickstart Colony has one cluster,
`child-1`, whose control plane runs as pods on the management cluster (hosted by
k0smotron) and whose only node is your worker, joined over SSH.

[`examples/quickstart/colony.yaml`](../examples/quickstart/colony.yaml) has two
`CHANGE ME` placeholders, `MGMT_NODE_IP` and `WORKER_NODE_IP`. Fill them in from
the variables you exported:

```bash
# workstation
sed -i "s/MGMT_NODE_IP/$MGMT_NODE_IP/; s/WORKER_NODE_IP/$WORKER_NODE_IP/" examples/quickstart/colony.yaml
```

The file you are about to apply:

```yaml
apiVersion: infra.exalsius.ai/v1
kind: Colony
metadata:
  name: quickstart
  namespace: kcm-system
spec:
  colonyClusters:
    - clusterName: child-1               # ClusterDeployment: quickstart-child-1
      clusterDeploymentSpec:
        template: exalsius-remote-cluster-0-1-5
        credential: remote-cred
        config:
          controlPlaneNumber: 1
          k0sCloudProvider:
            enabled: false               # bare SSH nodes have no cloud provider
          k0smotron:
            externalAddress: "203.0.113.10"   # MGMT_NODE_IP: how the worker reaches the control plane
            service:
              type: NodePort
              apiPort: 30443
              konnectivityPort: 30132
          machines:
            - address: "203.0.113.11"     # WORKER_NODE_IP
              user: root
              port: 22
```

Apply it and watch the Colony and the ClusterDeployment it creates:

```bash
# management cluster
kubectl apply -f examples/quickstart/colony.yaml
kubectl get colony -n kcm-system -w
kubectl get clusterdeployment -n kcm-system -w
```

Within 2 to 4 minutes the ClusterDeployment becomes Ready. Press `Ctrl-C` once
you see:

```
NAME                                PHASE   READY CLUSTERS   TOTAL CLUSTERS
colony.infra.exalsius.ai/quickstart Ready   1                1

NAME                                                          READY   SERVICES   TEMPLATE                        MESSAGES   AGE
clusterdeployment.k0rdent.mirantis.com/quickstart-child-1     True    0/0        exalsius-remote-cluster-0-1-5              2m
```

**Checkpoint.** Wait for `READY` = `True` on the **ClusterDeployment**; the
Colony's `PHASE` can read `Ready` a moment earlier and is not the signal. Then
save the child's kubeconfig, which k0rdent keeps in a Secret next to the
ClusterDeployment, and look at the new cluster:

```bash
# management cluster
hack/quickstart/child-kubeconfig.sh quickstart-child-1 > child.kubeconfig

# child cluster
kubectl --kubeconfig child.kubeconfig get nodes -o wide
```

```
NAME                STATUS   ROLES    AGE   VERSION       INTERNAL-IP     ...
<node 2 hostname>   Ready    <none>   90s   v1.36.2+k0s   203.0.113.11    ...
```

Notice that the child already runs Cilium as its network layer and OpenEBS as
its default StorageClass; both come from the cluster template. To follow the
provisioning while you wait, `kubectl -n kcm-system get pods` on the management
cluster shows the hosted control plane (`kmc-quickstart-child-1-*`) and the
k0smotron infrastructure controller logs the SSH join of the worker:

```bash
# management cluster
kubectl -n kcm-system logs deploy/k0smotron-controller-manager-infrastructure -f
```

## Step 3: deploy a workspace

Workspaces come in two parts: a cluster-scoped **WorkspaceClass** that an
administrator publishes as a catalog entry, and a namespaced
**WorkspaceDeployment** that a user creates to run one instance of a class on
one cluster. How the two map onto k0rdent and Sveltos is in
[Concepts](concepts.md).

The setup script installed the Jupyter Notebook class from the public
[exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub)
catalog. Its essentials (abridged; see the full object with
`kubectl get workspaceclass jupyter-notebook-0-3-0 -o yaml`):

```yaml
apiVersion: workspaces.exalsius.ai/v1
kind: WorkspaceClass
metadata:
  name: jupyter-notebook-0-3-0
spec:
  displayName: "Jupyter Notebook"
  serviceTemplate:
    name: jupyter-notebook-0-3-0
    namespace: kcm-system
  defaultResources:
    replicas: 1
    perReplica: { cpu: "4", memory: "8Gi", storage: "20Gi", gpuCount: 0 }
  accessEndpoints:
    - { name: http, protocol: HTTP, port: 80 }
  userFacingConfig:
    - helmValuePath: notebookPassword
      displayName: "Notebook Password"
      type: secret
      required: true
  deployTimeout: 15m
```

Notice that the class declares no `prerequisites`, so the deployment below
skips the `InstallingPrerequisites` phase.

Now the user side. Open
[`examples/quickstart/workspacedeployment.yaml`](../examples/quickstart/workspacedeployment.yaml)
and set your own notebook password at the `CHANGE ME` line. The example also
sizes the workspace down from the class defaults so it fits the 4 vCPU / 8 GB
worker:

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

Apply it and watch the phases:

```bash
# management cluster
kubectl apply -f examples/quickstart/workspacedeployment.yaml
kubectl get wsd -n kcm-system -w
```

The operator resolves the class, checks the target cluster, creates a k0rdent
`ServiceSet`, and k0rdent (through Sveltos) installs the chart on the child.
Expect `Pending` → `Deploying` → `Running` in about a minute:

```
NAME          CLASS                    CLUSTER              PHASE       READY   URL   AGE
my-notebook   jupyter-notebook-0-3-0   quickstart-child-1   Pending             5s
my-notebook   jupyter-notebook-0-3-0   quickstart-child-1   Deploying   False         12s
my-notebook   jupyter-notebook-0-3-0   quickstart-child-1   Running     True          65s
```

**Checkpoint.** `PHASE` = `Running` and `READY` = `True`; the empty `URL`
column is expected, because this setup has no Gateway to publish one through
(see [Going further](#going-further)). On the child, the workspace lives in
namespace `ws-<workspace name>`:

```bash
# child cluster
kubectl --kubeconfig child.kubeconfig -n ws-my-notebook get pods,pvc,svc
```

```
NAME                                                   READY   STATUS    RESTARTS   AGE
pod/wsd-quickstart-child-1-my-notebook-<hash>          1/1     Running   0          60s

NAME                                                      STATUS   VOLUME   CAPACITY   ACCESS MODES   STORAGECLASS
persistentvolumeclaim/wsd-quickstart-child-1-my-notebook  Bound    pvc-…    10Gi       RWO            openebs-hostpath

NAME                                             TYPE        CLUSTER-IP     PORT(S)
service/wsd-quickstart-child-1-my-notebook-http  ClusterIP   10.x.x.x       80/TCP
```

## Step 4: open the notebook

Forward the workspace's Service to your workstation. The Service name is
`wsd-<clusterdeployment>-<workspace>-http`; if you renamed the workspace, find
it with `kubectl --kubeconfig child.kubeconfig -n ws-<name> get svc`.

```bash
# child cluster
kubectl --kubeconfig child.kubeconfig -n ws-my-notebook \
  port-forward svc/wsd-quickstart-child-1-my-notebook-http 8888:80
```

Leave that running and, from a second terminal, ask Jupyter for its version:

```bash
# workstation
curl -s http://localhost:8888/api
```

```json
{"version": "2.20.0"}
```

Open <http://localhost:8888> in a browser and log in with the
`notebookPassword` you set in the WorkspaceDeployment.

**Checkpoint.** Create a notebook with *File → New → Notebook*, accept the
default Python kernel, and run one cell:

```python
import platform, socket
print(socket.gethostname(), platform.python_version())
```

The output names the workspace pod (`wsd-quickstart-child-1-my-notebook-…`)
and a Python version.
That cell ran on a cluster that did not exist a few minutes ago.

## Teardown

> **Before you delete anything.** Deleting the WorkspaceDeployment deletes the
> notebook's volume with it; download any notebook you want to keep first.
> Deleting the Colony removes the child cluster and resets k0s on node 2. The
> last helper resets k0s on node 1. None of this deletes or stops the machines
> themselves: the two VMs stay running and billed until you remove them.

Delete in the reverse order of creation. Each delete blocks until the operator
has finished cleaning up; then a helper removes k0s from the management node.

```bash
# management cluster
kubectl delete wsd my-notebook -n kcm-system                      # ~45 s: uninstalls the chart, removes ws-my-notebook
kubectl delete colony quickstart -n kcm-system --timeout=10m      # ~60 s: removes the child cluster, k0s reset on the worker

# workstation
hack/quickstart/reset-management.sh                               # refuses while a Colony or workspace remains, then k0sctl reset
```

Afterwards:

- **Gone:** the workspace, its namespace and volume on the child; the child
  cluster, its hosted control plane and kubeconfig Secrets; k0s, k0rdent and
  the operator on node 1.
- **Left on the nodes, harmless:** on node 2 the k0s binary, `/etc/k0s`, a
  stale Cilium CNI config and network links (a reboot clears the links), and
  the OpenEBS data directory `/var/openebs/local`, empty once the volume was deleted. A
  new Colony provisions straight over all of it.
- **You remove yourself:** the two VMs, `quickstart-out/` and
  `child.kubeconfig` on your workstation, and the quickstart SSH key from
  `authorized_keys` on both nodes if you created one for this guide.

Both nodes are then free for a second run; you do not need fresh machines to
try again. To keep the management cluster and only clear what a deleted Colony
leaves behind, run the helper with `--keep-cluster` instead; it also honors
`--out-dir` if you changed it for the setup script.

## Troubleshooting

**`kubectl --kubeconfig child.kubeconfig` times out or says `connection
refused`.** The child's API server is the NodePort `30443` on node 1, and your
workstation must reach it directly. If you cannot open the port, tunnel it
instead and point the kubeconfig at the tunnel:

```bash
# workstation
ssh -i "$SSH_KEY" -N -L 30443:localhost:30443 root@$MGMT_NODE_IP &
sed -i "s#https://$MGMT_NODE_IP:30443#https://localhost:30443#" child.kubeconfig
```

**The ClusterDeployment never becomes Ready and the child node is `NotReady`.**
Check the Cilium agent on the child: `kubectl --kubeconfig child.kubeconfig -n
kube-system get pods`. If it sits in `Init:0/6`, the `cp-api-endpoint`
ConfigMap in the child's `kube-system` namespace does not carry an address the
worker can reach. Operator releases before the fix for NodePort-exposed control
planes published the control plane's cluster-internal IP; make sure
`EXALSIUS_OPERATOR_VERSION` in `versions.env` is the pinned release and re-run
the setup script.

**The node has `providerID: k0s-cloud-provider://…` and no `InternalIP`**, the
Machine stays `Provisioned`, and pods fail with `host IP unknown`. The Colony
was applied with `k0sCloudProvider.enabled` left at the chart default (`true`).
The provider ID is immutable, so delete the Colony, keep `enabled: false` as in
the example, and apply again.

**`kubectl logs` or `port-forward` against the child says `No agent
available`.** The konnectivity agent on the worker is not up yet. It starts a
minute or so after the node's network is ready; retry.

**`kubectl describe wsd` shows `Feasible=False` with
`InsufficientResources`.** The operator's capacity estimate is more
conservative than the scheduler's. On a 4 vCPU / 8 GB worker the 2 CPU / 4Gi
example still schedules and reaches `Running`; the condition is advisory. If
the pod actually stays `Pending`, lower `resources.perReplica`.

**`kubectl describe wsd` shows `RoutesReady=False` /
`RoutingInfraNotReady`.** Expected here. URL publishing needs a regional
cluster with a tenant Gateway, which this guide does not set up; the workspace
itself is fine. See [Workspace access](concepts.md#workspace-access).

**The Colony shows `Ready` before the ClusterDeployment does.** The Colony's
phase can briefly read `Ready` right after apply and then flip back to
`Provisioning`. Wait for the ClusterDeployment's `READY` column.

**The setup script fails on the SSH probe.** The key must log in as `root` on
node 1 without a passphrase prompt. Test with `ssh -i "$SSH_KEY" root@$MGMT_NODE_IP
hostname`. The same key is uploaded for the worker, so it must work on node 2
as well. If the probe reports `REMOTE HOST IDENTIFICATION HAS CHANGED`, the
address was re-used by a re-provisioned VM; run `ssh-keygen -R $MGMT_NODE_IP`
(and the same for the worker) and start again.

**The worker never joins and the RemoteMachine reports `ProvisionFailed` with
`failed to upload file: ... /etc/k0s.token: invalid argument: failed to stat
parent directory`.** The worker runs Ubuntu 26.04 without the `gnustat` symlink
from the [prerequisites](#prerequisites). 26.04 ships the Rust `uutils`
coreutils as `/usr/bin/stat`, and the k0smotron release bundled with KCM 1.10
misdetects it as BSD `stat` while copying the join token; GNU stat is still
installed as `gnustat`. Cluster API does not retry a failed RemoteMachine: add
the symlink, then delete the Colony and apply it again. Check with
`kubectl -n kcm-system get remotemachine -o
jsonpath='{.items[*].status.failureMessage}'`.

**The worker never joins (Machine stuck, no `RemoteMachine` progress).** Node 1
must reach node 2 on port 22, and node 2 must reach node 1 on 30443 and 30132.
Follow along with `kubectl -n kcm-system logs
deploy/k0smotron-controller-manager-infrastructure -f`.

## Going further

- **Published URLs instead of port-forward.** In a full deployment each child
  is attached to a **regional cluster**, a per-tenant hub that runs a Gateway
  API `Gateway`. Attach the child to one and the operator fills in the `URL`
  column automatically. How publishing works is in
  [Workspace access](concepts.md#workspace-access).
- **More nodes.** Add entries to `config.machines[]` in the Colony to give the
  child more workers, or add a second `colonyClusters` entry for a second
  cluster.
- **GPU workspaces.** The same catalog serves GPU-backed classes (LLM
  inference, training stacks). Point a WorkspaceDeployment at a GPU node with
  `gpuCount` and `gpuNodeSelector`; the GPU gate and the `Waiting` phase are
  explained in [Concepts](concepts.md#the-deployment-merges-gates-and-hands-off).
- **A reproducible GPU evaluation.** The
  [exalsius-provisioning-benchmark](https://github.com/exalsius/exalsius-provisioning-benchmark)
  provisions an operator-managed cluster directly on your own GPU nodes,
  deploys LLM inference workspaces in single-node, replicated and sharded
  scenarios, and collects throughput and telemetry. It needs Docker on your
  workstation and NVIDIA nodes, runs everything in one cluster rather than a
  Colony with a child, and pins its own component versions.
- **The full picture.** [Concepts](concepts.md) explains how classes,
  deployments, ServiceSets and Sveltos fit together and shows the complete
  topology with a regional cluster, which this guide leaves out.
- **The CLI and hosted platform.** The exalsius CLI and API sit on top of these
  resources; their journey starts at <https://docs.exalsius.ai>.
