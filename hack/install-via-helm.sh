#!/bin/bash

set -e

NAMESPACE=exalsius-system

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

VALUES_FILE=${1:-""}
VALUES_ARG=""

if [ -n "$VALUES_FILE" ]; then
    if [ -f "$VALUES_FILE" ]; then
        VALUES_ARG="--values $VALUES_FILE"
    else
        echo "Error: Values file '$VALUES_FILE' not found"
        exit 1
    fi
fi

# print path to values file if set
if [ -n "$VALUES_FILE" ]; then
    echo "Using values file for exalsius-operator: $VALUES_FILE"
fi

# set ulimit and sysctls
ulimit -n 65535
if command -v sudo >/dev/null && sudo -n true 2>/dev/null; then
  echo "Running sysctl updates with sudo"
  sudo sysctl -w fs.inotify.max_user_watches=524288
  sudo sysctl -w fs.inotify.max_user_instances=8192
else
  echo "[WARNING] Skipping sysctl updates (no sudo permissions)"
  echo "Consider setting the following sysctls manually:"
  echo "sysctl -w fs.inotify.max_user_watches=524288"
  echo "sysctl -w fs.inotify.max_user_instances=8192"
  echo "ulimit -n 65535"
fi

# install cert manager
echo "installing cert-manager"

helm repo add jetstack https://charts.jetstack.io --force-update
helm upgrade --install cert-manager \
  jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true \
  --wait


# install volcano
echo "installing volcano"
helm repo add volcano-sh https://volcano-sh.github.io/helm-charts
helm upgrade --install volcano volcano-sh/volcano \
  --namespace volcano-system \
  --create-namespace \
  --wait

echo "installing cluster-api operator"
# install cluster-api-provider
helm repo add capi-operator https://kubernetes-sigs.github.io/cluster-api-operator
helm install capi-operator capi-operator/cluster-api-operator \
  --create-namespace \
  --namespace capi-system \
  --wait

echo "installing exalsius-operator umbrella chart"
helm dependency update "${SCRIPT_DIR}/../charts/exalsius-operator"
helm upgrade --install exalsius "${SCRIPT_DIR}/../charts/exalsius-operator" \
  --namespace $NAMESPACE \
  --create-namespace \
  $VALUES_ARG \
  --timeout 30m \
  --wait
