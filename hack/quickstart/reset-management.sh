#!/usr/bin/env bash
# Quickstart teardown, last step: remove k0s from the management node.
#
#   hack/quickstart/reset-management.sh [--out-dir <dir>] [--keep-cluster]
#
# Runs after `kubectl delete wsd ...` and `kubectl delete colony ...` (see the
# Teardown section of docs/quickstart.md). It refuses to run while any
# WorkspaceDeployment, Colony, or ClusterDeployment still exists: resetting the
# management node under a live child cluster leaves the worker node with a
# k0s worker that nothing resets.
#
#   --out-dir <dir>   where setup-management.sh wrote k0sctl.yaml and
#                     mgmt.kubeconfig, default ./quickstart-out (OUT_DIR)
#   --keep-cluster    only remove leftovers on the management cluster (the
#                     aggregated <colony>-kubeconfigs Secrets a deleted Colony
#                     leaves behind); do not reset the node
#
# Environment: FORCE=1 skips the guard when the management cluster no longer
# answers; DRY_RUN=1 prints the commands without running them.
#
# quickstart-out/ is kept so the reset can be re-run; the next
# setup-management.sh overwrites it.

set -euo pipefail
# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

usage() { sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit "${1:-0}"; }

OUT_DIR="${OUT_DIR:-$PWD/quickstart-out}"
KEEP_CLUSTER=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)      OUT_DIR="$2"; shift 2 ;;
    --keep-cluster) KEEP_CLUSTER=1; shift ;;
    -h|--help)      usage 0 ;;
    *) err "unknown argument: $1"; usage 1 ;;
  esac
done

K0SCTL_CONFIG="$OUT_DIR/k0sctl.yaml"
KUBECONFIG_OUT="$OUT_DIR/mgmt.kubeconfig"
export KUBECONFIG_OUT

# --- 0. preflight --------------------------------------------------------------
log "preflight"
require_cmd kubectl
[[ "$KEEP_CLUSTER" == "1" ]] || require_cmd k0sctl
[[ "$KEEP_CLUSTER" == "1" || -r "$K0SCTL_CONFIG" ]] \
  || die "no $K0SCTL_CONFIG: pass --out-dir <dir> given to setup-management.sh"
[[ -r "$KUBECONFIG_OUT" ]] || die "no $KUBECONFIG_OUT: pass --out-dir <dir> given to setup-management.sh"

# --- 1. guard: nothing may still be running on top of the management cluster ---
log "checking that no workspaces or clusters remain"
if [[ "${FORCE:-0}" == "1" ]]; then
  warn "FORCE=1: skipping the guard and the leftover cleanup"
elif [[ "${DRY_RUN:-0}" == "1" ]]; then
  info "[dry-run] would refuse if any WorkspaceDeployment, Colony, or ClusterDeployment remains"
  info "[dry-run] would delete leftover <colony>-kubeconfigs Secrets"
elif ! kc get --raw /readyz >/dev/null 2>&1; then
  die "management cluster at $KUBECONFIG_OUT does not answer; if it is already gone, re-run with FORCE=1"
else
  leftovers="$(kc get workspacedeployments,colonies,clusterdeployments -A --no-headers 2>/dev/null || true)"
  if [[ -n "$leftovers" ]]; then
    err "these must be deleted first, in this order (each delete blocks until cleanup is done):"
    printf '%s\n' "$leftovers" | sed 's/^/      /' >&2
    printf '\n    kubectl delete wsd <name> -n <namespace>\n    kubectl delete colony <name> -n <namespace> --timeout=10m\n\n' >&2
    exit 1
  fi
  info "no WorkspaceDeployments, Colonies, or ClusterDeployments left"

  # --- 2. leftovers: a deleted Colony leaves its aggregated kubeconfig Secret ---
  # (operator bug; the finalizer removes the ClusterDeployments only)
  while read -r ns name; do
    [[ -n "$name" ]] || continue
    run kc -n "$ns" delete secret "$name"
  done < <(kc get secrets -A -o jsonpath='{range .items[?(@.type=="Opaque")]}{.metadata.namespace} {.metadata.name}{"\n"}{end}' \
             | awk '$2 ~ /-kubeconfigs$/')
  info "leftover kubeconfig secrets removed"
fi

if [[ "$KEEP_CLUSTER" == "1" ]]; then
  log "management cluster kept (--keep-cluster); ready for the next Colony"
  exit 0
fi

# --- 3. reset the management node -----------------------------------------------
log "removing k0s from the management node (k0sctl reset, no confirmation prompt)"
run k0sctl reset --config "$K0SCTL_CONFIG" --force

log "management node reset"
cat >&2 <<MSG

  $OUT_DIR/ is kept (k0sctl.yaml, the old kubeconfig) so this reset can be
  re-run. Both nodes are free again; hack/quickstart/setup-management.sh
  starts over and overwrites those files.
MSG
