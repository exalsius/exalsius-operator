#!/usr/bin/env bash
# Quickstart step 1: turn one SSH node into the exalsius management cluster.
#
#   k0s (single node, untainted)  ->  OpenEBS default StorageClass
#   -> k0rdent/KCM (k0smotron + sveltos providers)  ->  exalsius-operator
#   -> child cluster template + SSH Credential  ->  demo workspace catalog
#
# Inputs (flags, environment, or interactive prompt):
#   --node       <ip[:port]>   management node SSH endpoint      (MGMT_NODE)
#   --ssh-user   <user>        SSH user on the node, default root (SSH_USER)
#   --ssh-key    <path>        private key for the node; the same key is
#                              handed to k0rdent for the worker nodes (SSH_KEY_PATH)
#   --out-dir    <dir>         where k0sctl.yaml and the management kubeconfig
#                              are written, default ./quickstart-out (OUT_DIR)
#
# Safe to re-run: every step is an upgrade-or-install / apply.
# Versions are pinned in versions.env next to this script.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

usage() { sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit "${1:-0}"; }

MGMT_NODE="${MGMT_NODE:-}"
SSH_USER="${SSH_USER:-root}"
SSH_KEY_PATH="${SSH_KEY_PATH:-}"
OUT_DIR="${OUT_DIR:-$PWD/quickstart-out}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --node)     MGMT_NODE="$2"; shift 2 ;;
    --ssh-user) SSH_USER="$2"; shift 2 ;;
    --ssh-key)  SSH_KEY_PATH="$2"; shift 2 ;;
    --out-dir)  OUT_DIR="$2"; shift 2 ;;
    -h|--help)  usage 0 ;;
    *) err "unknown argument: $1"; usage 1 ;;
  esac
done

prompt_var MGMT_NODE "Management node SSH endpoint (ip or ip:port)"
prompt_var SSH_KEY_PATH "Path to the SSH private key for $SSH_USER@$MGMT_NODE"
SSH_KEY_PATH="${SSH_KEY_PATH/#\~/$HOME}"
MGMT_HOST="${MGMT_NODE%%:*}"; MGMT_PORT="${MGMT_NODE##*:}"; [[ "$MGMT_PORT" == "$MGMT_HOST" ]] && MGMT_PORT=22
[[ -r "$SSH_KEY_PATH" ]] || die "SSH key not readable: $SSH_KEY_PATH"

mkdir -p "$OUT_DIR"
K0SCTL_CONFIG="$OUT_DIR/k0sctl.yaml"
KUBECONFIG_OUT="$OUT_DIR/mgmt.kubeconfig"
export KUBECONFIG_OUT

# --- 0. preflight --------------------------------------------------------------
log "preflight"
require_cmd k0sctl kubectl helm ssh
have="$(k0sctl version 2>/dev/null | sed -n 's/^version: //p')"
[[ "$have" == "$K0SCTL_VERSION" ]] || warn "k0sctl $have found; the quickstart was verified with $K0SCTL_VERSION" \
  "(https://github.com/k0sproject/k0sctl/releases/tag/$K0SCTL_VERSION)"
run ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 \
  -i "$SSH_KEY_PATH" -p "$MGMT_PORT" "$SSH_USER@$MGMT_HOST" 'hostname && nproc && free -g | sed -n 2p' \
  || die "cannot SSH to $SSH_USER@$MGMT_HOST:$MGMT_PORT with $SSH_KEY_PATH (key must have no passphrase, or be loaded in ssh-agent)"
info "node reachable"

# --- 1. k0s management cluster --------------------------------------------------
log "bootstrapping single-node k0s $K0S_VERSION on $MGMT_HOST (k0sctl)"
cat > "$K0SCTL_CONFIG" <<YAML
apiVersion: k0sctl.k0sproject.io/v1beta1
kind: Cluster
metadata:
  name: quickstart-mgmt
spec:
  hosts:
    # controller+worker with --no-taints: the one node runs the control plane
    # and every workload (KCM, the operator, hosted child control planes).
    - role: controller+worker
      installFlags:
        - --no-taints
      ssh:
        address: ${MGMT_HOST}
        user: ${SSH_USER}
        port: ${MGMT_PORT}
        keyPath: ${SSH_KEY_PATH}
  k0s:
    version: ${K0S_VERSION}
YAML
info "wrote $K0SCTL_CONFIG"
run k0sctl apply --config "$K0SCTL_CONFIG" --kubeconfig-out "$KUBECONFIG_OUT"
[[ "${DRY_RUN:-0}" == "1" ]] || chmod 600 "$KUBECONFIG_OUT"
node_ready() { kc get nodes --no-headers | grep -q ' Ready '; }
wait_for 300 "management node Ready" node_ready

# --- 2. default StorageClass ----------------------------------------------------
log "installing OpenEBS $OPENEBS_VERSION (default StorageClass for hosted control planes)"
run hlm repo add openebs "$OPENEBS_REPO_URL" --force-update >/dev/null
run hlm repo update openebs >/dev/null
run hlm upgrade --install openebs openebs/openebs --version "$OPENEBS_VERSION" \
  -n openebs --create-namespace -f "$QUICKSTART_ROOT/manifests/openebs-values.yaml" --timeout 10m
wait_for 300 "StorageClass openebs-hostpath is the default" \
  kc_field_is "" storageclass/openebs-hostpath '{.metadata.annotations.storageclass\.kubernetes\.io/is-default-class}' true

# --- 3. k0rdent / KCM -------------------------------------------------------------
log "installing k0rdent KCM $KCM_VERSION"
run hlm upgrade --install kcm "$KCM_CHART" --version "$KCM_VERSION" \
  -n "$KCM_NAMESPACE" --create-namespace --wait --timeout 15m
wait_for 900 "Management/kcm Ready" kc_ready "$KCM_NAMESPACE" management.k0rdent.mirantis.com/kcm
log "trimming KCM providers to k0smotron + projectsveltos"
run kc patch management.k0rdent.mirantis.com kcm --type=merge \
  -p '{"spec":{"providers":[{"name":"cluster-api-provider-k0sproject-k0smotron"},{"name":"projectsveltos"}]}}'
wait_for 900 "Management/kcm Ready with trimmed providers" kc_ready "$KCM_NAMESPACE" management.k0rdent.mirantis.com/kcm

# --- 4. exalsius-operator ----------------------------------------------------------
log "installing exalsius-operator $EXALSIUS_OPERATOR_VERSION"
run hlm upgrade --install exalsius-operator "$EXALSIUS_OPERATOR_CHART" \
  --version "$EXALSIUS_OPERATOR_VERSION" -n "$KCM_NAMESPACE" --wait --timeout 10m
wait_for 120 "exalsius CRDs registered" \
  kc get crd colonies.infra.exalsius.ai workspacedeployments.workspaces.exalsius.ai workspaceclasses.workspaces.exalsius.ai

# --- 5. child cluster template + SSH credential ----------------------------------------
log "registering ClusterTemplate $REMOTE_CLUSTER_TEMPLATE_NAME"
run kc apply -f "$QUICKSTART_ROOT/manifests/cluster-templates-helm-repository.yaml"
[[ "${DRY_RUN:-0}" == "1" ]] || kc apply -f - <<YAML
apiVersion: k0rdent.mirantis.com/v1beta1
kind: ClusterTemplate
metadata:
  name: ${REMOTE_CLUSTER_TEMPLATE_NAME}
  namespace: ${KCM_NAMESPACE}
spec:
  helm:
    chartSpec:
      chart: ${REMOTE_CLUSTER_TEMPLATE_CHART}
      version: ${REMOTE_CLUSTER_TEMPLATE_VERSION}
      interval: 10m
      reconcileStrategy: ChartVersion
      sourceRef:
        kind: HelmRepository
        name: exalsius-cluster-templates
YAML
wait_for 300 "ClusterTemplate $REMOTE_CLUSTER_TEMPLATE_NAME valid" \
  kc_field_is "$KCM_NAMESPACE" "clustertemplate/$REMOTE_CLUSTER_TEMPLATE_NAME" '{.status.valid}' true

log "storing the SSH key as k0rdent Credential remote-cred (for the worker nodes)"
# The Secret's data key must be `value`: that is what k0smotron reads.
[[ "${DRY_RUN:-0}" == "1" ]] || kc -n "$KCM_NAMESPACE" create secret generic remote-ssh-key \
  --from-file=value="$SSH_KEY_PATH" --dry-run=client -o yaml \
  | kc label --local -f - k0rdent.mirantis.com/component=kcm -o yaml \
  | kc apply -f - >/dev/null
run kc apply -f "$QUICKSTART_ROOT/manifests/ssh-credential.yaml"
wait_for 120 "Credential remote-cred ready" \
  kc_field_is "$KCM_NAMESPACE" credential/remote-cred '{.status.ready}' true

# --- 6. demo workspace catalog -------------------------------------------------------------
log "installing the demo workspace catalog ($JUPYTER_TEMPLATE_NAME from exalsius-workspace-hub)"
run kc apply -f "$QUICKSTART_ROOT/manifests/workspace-hub-helm-repository.yaml"
run kc apply -f "$JUPYTER_SERVICETEMPLATE_URL"
wait_for 300 "ServiceTemplate $JUPYTER_TEMPLATE_NAME valid" \
  kc_field_is "$KCM_NAMESPACE" "servicetemplate/$JUPYTER_TEMPLATE_NAME" '{.status.valid}' true
run kc apply -f "$JUPYTER_WORKSPACECLASS_URL"

# --- done ----------------------------------------------------------------------------
log "management cluster ready"
cat >&2 <<MSG

  Management cluster:   $SSH_USER@$MGMT_HOST  (k0s $K0S_VERSION, KCM $KCM_VERSION, operator $EXALSIUS_OPERATOR_VERSION)
  Kubeconfig:           $KUBECONFIG_OUT
  Cluster template:     $REMOTE_CLUSTER_TEMPLATE_NAME   (credential: remote-cred)
  Workspace class:      $JUPYTER_TEMPLATE_NAME

Next:
  export KUBECONFIG=$KUBECONFIG_OUT
  # edit MGMT_NODE_IP / WORKER_NODE_IP in examples/quickstart/colony.yaml, then
  kubectl apply -f examples/quickstart/colony.yaml
  kubectl get colony,clusterdeployment -n kcm-system -w
MSG
