#!/usr/bin/env bash
# Print the kubeconfig of a child cluster provisioned by a Colony.
#
#   hack/quickstart/child-kubeconfig.sh <clusterdeployment> [namespace] > child.kubeconfig
#
# k0rdent stores every child's admin kubeconfig in Secret <clusterdeployment>-kubeconfig
# (key `value`) next to the ClusterDeployment. Uses your current KUBECONFIG
# (the management cluster).

set -euo pipefail

cd_name="${1:-}"; ns="${2:-kcm-system}"
if [[ -z "$cd_name" || "$cd_name" == "-h" || "$cd_name" == "--help" ]]; then
  sed -n '2,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit "$([[ -z "$cd_name" ]] && echo 1 || echo 0)"
fi

data="$(kubectl -n "$ns" get secret "${cd_name}-kubeconfig" -o jsonpath='{.data.value}' 2>/dev/null)" || true
if [[ -z "$data" ]]; then
  echo "no Secret ${cd_name}-kubeconfig in namespace $ns yet." >&2
  echo "It appears once the ClusterDeployment's control plane is up: kubectl -n $ns get clusterdeployment $cd_name" >&2
  exit 1
fi
printf '%s' "$data" | base64 -d
