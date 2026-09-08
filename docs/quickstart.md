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

The incidental plumbing (k0s, k0rdent, the operator, templates, credentials) is
installed by one script. The two resources this project is about, the `Colony`
and the `WorkspaceDeployment`, you apply yourself as YAML.

## Prerequisites

**Two Linux nodes**, each:

- Ubuntu 24.04 (other systemd-based distributions that k0s supports should work
  but are untested here), 4 vCPU and 8 GB RAM.
- Reachable as `root` over SSH with the **same** key from your workstation.
  The key must work non-interactively: no passphrase, or loaded into
  `ssh-agent`.
- Fresh: no Kubernetes, no container runtime, no firewall rules you did not
  set yourself.

**Network** between them:

| From | To | Ports |
|---|---|---|
| your workstation | node 1 | 22 (SSH), 6443 (Kubernetes API) |
| node 1 | node 2 | 22 (SSH, the child node is provisioned from a pod on node 1) |
| node 2 | node 1 | 30443, 30132 (the child cluster's API server and konnectivity tunnel) |

Cloud VMs with public IPs and the ports above open work fine. Put the two
addresses and the key path in your shell; every command below reads them:

```bash
export MGMT_NODE_IP=203.0.113.10        # node 1, as both your workstation and node 2 reach it
export WORKER_NODE_IP=203.0.113.11      # node 2, as node 1 reaches it
export SSH_KEY=~/.ssh/id_ed25519        # the key that logs in as root on both nodes
```

**On your workstation**: `bash`, `ssh`, `kubectl`, `helm`, and
[`k0sctl`](https://github.com/k0sproject/k0sctl/releases) **v0.32.2** (the
version every other pin in this guide was verified with), plus a clone of this
repository:

```bash
git clone https://github.com/exalsius/exalsius-operator.git
cd exalsius-operator
```

All version pins live in [`hack/quickstart/versions.env`](../hack/quickstart/versions.env).

> Already running a k0rdent management cluster? Skip the k0s bootstrap, but the
> rest of step 1 still applies: read
> [`hack/quickstart/setup-management.sh`](../hack/quickstart/setup-management.sh)
> and run its sections 2 to 6 against your own kubeconfig.

## Step 1: set up the management cluster

One command turns node 1 into the management cluster. It bootstraps a single-node
k0s, installs a default StorageClass (OpenEBS), k0rdent (KCM 1.8) trimmed to the
k0smotron and Sveltos providers, the exalsius-operator, the
`exalsius-remote-cluster` ClusterTemplate, a k0rdent `Credential` holding your
SSH key for the worker node, and the public workspace catalog with the Jupyter
Notebook class.

```bash
hack/quickstart/setup-management.sh --node "$MGMT_NODE_IP" --ssh-key "$SSH_KEY"
```

Expect roughly 8 to 10 minutes; each step prints what it is waiting for. The
script is idempotent, so if it fails on a transient error (a slow image pull,
an SSH hiccup) just run it again. It ends with:

```
==> management cluster ready

  Management cluster:   root@203.0.113.10  (k0s v1.33.8+k0s.1, KCM 1.8.0, operator 0.11.1)
  Kubeconfig:           /path/to/exalsius-operator/quickstart-out/mgmt.kubeconfig
  Cluster template:     exalsius-remote-cluster-0-1-5   (credential: remote-cred)
  Workspace class:      jupyter-notebook-0-3-0

Next:
  export KUBECONFIG=/path/to/exalsius-operator/quickstart-out/mgmt.kubeconfig
  ...
```

Point `kubectl` at the new cluster for the rest of the guide:

```bash
export KUBECONFIG=$PWD/quickstart-out/mgmt.kubeconfig
```

**Checkpoint.** One Ready node, a valid ClusterTemplate, and the Jupyter
WorkspaceClass:

```bash
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
kubectl apply -f examples/quickstart/colony.yaml
kubectl get colony,clusterdeployment -n kcm-system -w
```

Within about 2 minutes the ClusterDeployment becomes Ready. Press `Ctrl-C` once
you see:

```
NAME                                PHASE   READY CLUSTERS   TOTAL CLUSTERS
colony.infra.exalsius.ai/quickstart Ready   1                1

NAME                                                          READY   SERVICES   TEMPLATE                        MESSAGES   AGE
clusterdeployment.k0rdent.mirantis.com/quickstart-child-1     True    0/0        exalsius-remote-cluster-0-1-5              2m
```

The Colony may report `Ready` for a moment before the ClusterDeployment does;
the ClusterDeployment's `READY` column is the one to wait for. To follow what is
happening underneath, `kubectl -n kcm-system get pods` shows the hosted control
plane (`kmc-quickstart-child-1-*`), and the k0smotron infrastructure controller
logs the SSH provisioning of the worker:

```bash
kubectl -n kcm-system logs deploy/k0smotron-controller-manager-infrastructure -f
```

**Checkpoint.** k0rdent stores every child cluster's kubeconfig in a Secret
next to the ClusterDeployment. Save it to a file and look at the new cluster:

```bash
hack/quickstart/child-kubeconfig.sh quickstart-child-1 > child.kubeconfig
kubectl --kubeconfig child.kubeconfig get nodes -o wide
```

```
NAME                STATUS   ROLES    AGE   VERSION       INTERNAL-IP     ...
<node 2 hostname>   Ready    <none>   90s   v1.36.2+k0s   203.0.113.11    ...
```

The child runs Cilium as its network layer and OpenEBS as its default
StorageClass; both came from the cluster template. (The operator also keeps an
aggregated kubeconfig for the whole Colony in Secret
`quickstart-kubeconfigs`, which is what multi-cluster consumers use.)

## Step 3: deploy a workspace

Workspaces come in two parts. A cluster-scoped **WorkspaceClass** is the
catalog entry an administrator publishes; it pins a Helm chart (via a k0rdent
ServiceTemplate), declares default resources, and lists what a user may
configure. A namespaced **WorkspaceDeployment** is a user's request for one
instance of a class on one cluster.

The setup script installed the Jupyter Notebook class from the public
[exalsius-workspace-hub](https://github.com/exalsius/exalsius-workspace-hub)
catalog. Its essentials:

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

The `URL` column stays empty in this setup: the operator publishes URLs only
when the child is attached to a regional cluster with a tenant Gateway (see
[Going further](#going-further)), which the quickstart deliberately leaves out.

**Checkpoint.** On the child, the workspace lives in namespace
`ws-<workspace name>`:

```bash
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
kubectl --kubeconfig child.kubeconfig -n ws-my-notebook \
  port-forward svc/wsd-quickstart-child-1-my-notebook-http 8888:80
```

Leave that running and, from a second terminal, ask Jupyter for its version:

```bash
curl -s http://localhost:8888/api
```

```json
{"version": "2.20.0"}
```

Open <http://localhost:8888> in a browser and log in with the
`notebookPassword` you set in the WorkspaceDeployment. You have a notebook
running on a cluster that did not exist a few minutes ago.

## Teardown

Delete in the reverse order of creation. Each delete blocks until the operator
has finished cleaning up.

```bash
kubectl delete wsd my-notebook -n kcm-system                      # ~45 s: uninstalls the chart, removes ws-my-notebook
kubectl delete colony quickstart -n kcm-system --timeout=10m      # ~60 s: removes the child cluster, k0s reset on the worker
k0sctl reset --config quickstart-out/k0sctl.yaml                  # removes k0s from the management node (asks for confirmation)
```

Both nodes are then free for a second run; you do not need fresh machines to
try again.

## Troubleshooting

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
itself is fine. See
[ADR-0001](adr/0001-workspace-access-via-gateway-api.md).

**The Colony shows `Ready` before the ClusterDeployment does.** The Colony's
phase can briefly read `Ready` right after apply and then flip back to
`Provisioning`. Wait for the ClusterDeployment's `READY` column.

**The setup script fails on the SSH probe.** The key must log in as `root` on
node 1 without a passphrase prompt. Test with `ssh -i "$SSH_KEY" root@$MGMT_NODE_IP
hostname`. The same key is uploaded for the worker, so it must work on node 2
as well.

**The worker never joins (Machine stuck, no `RemoteMachine` progress).** Node 1
must reach node 2 on port 22, and node 2 must reach node 1 on 30443 and 30132.
Follow along with `kubectl -n kcm-system logs
deploy/k0smotron-controller-manager-infrastructure -f`.

## Going further

- **Published URLs instead of port-forward.** Attach the child to a regional
  cluster with a tenant Gateway and the operator fills in the `URL` column
  automatically. The design is in
  [ADR-0001](adr/0001-workspace-access-via-gateway-api.md).
- **More nodes.** Add entries to `config.machines[]` in the Colony to give the
  child more workers, or add a second `colonyClusters` entry for a second
  cluster.
- **GPU workspaces.** The same catalog serves GPU-backed classes (LLM
  inference, training stacks). Point a WorkspaceDeployment at a GPU node with
  `gpuCount` and `gpuNodeSelector`; see
  [ADR-0002](adr/0002-gpu-selection-inventory-and-capacity-gating.md).
- **The workspace model.** The
  [Workspaces](../README.md#workspaces) section of the README and the
  [ADRs](adr/) explain how classes, deployments, ServiceSets and Sveltos fit
  together.
