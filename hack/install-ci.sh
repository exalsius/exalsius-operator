#!/bin/bash

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

# install volcano
echo "installing volcano"
helm repo add volcano-sh https://volcano-sh.github.io/helm-charts
helm upgrade --install volcano volcano-sh/volcano \
  --namespace volcano-system \
  --create-namespace \
  --wait

echo "installing kcm"
helm install kcm oci://ghcr.io/k0rdent/kcm/charts/kcm --version 1.0.0 -n kcm-system --create-namespace

echo "installing exalsius-operator umbrella chart"
helm dependency update "${SCRIPT_DIR}/../charts/exalsius-operator"
helm upgrade --install exalsius "${SCRIPT_DIR}/../charts/exalsius-operator" \
  --namespace $NAMESPACE \
  --create-namespace \
  $VALUES_ARG \
  --wait \
  --timeout 30m \
  --debug
