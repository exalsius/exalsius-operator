#!/bin/bash

set -e

NAMESPACE=exalsius-system
KORDENT_VERSION=1.0.0
POLL_INTERVAL=10
KOF_NAMESPACE=kof

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

VALUES_FILE=${1:-""}
INSTALL_KOF=${2:-""}
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

# print kof installation setting
if [ -n "$INSTALL_KOF" ]; then
    echo "Used kof installation flag: '$INSTALL_KOF' (to enable kof installation, set 'install_kof=true')"
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

# install kcm
echo "installing kcm"
helm install kcm oci://ghcr.io/k0rdent/kcm/charts/kcm --version $KORDENT_VERSION -n kcm-system --create-namespace --wait

# install exalsius umbrella chart
echo "installing exalsius-operator umbrella chart"
helm dependency update "${SCRIPT_DIR}/../charts/exalsius"
helm upgrade --install exalsius "${SCRIPT_DIR}/../charts/exalsius" \
  --namespace $NAMESPACE \
  --create-namespace \
  $VALUES_ARG \
  --timeout 30m \
  --wait

# Only install kof if INSTALL_KOF is set to "true"
if [ "$INSTALL_KOF" = "install_kof=true" ]; then
    # We have to wait for CRDs to be available before installing kof
    CRD_NAME="clusterprofiles.config.projectsveltos.io"
    echo "Waiting until CRD '$CRD_NAME' is installed..."
    while true; do
      if kubectl get crd "$CRD_NAME" > /dev/null 2>&1; then
        echo "CRD '$CRD_NAME' is installed."
        break
      else
        echo "CRD '$CRD_NAME' not found yet. Retrying in $POLL_INTERVAL seconds..."
        sleep $POLL_INTERVAL
      fi
    done

    # install kof
    echo "installing kof"
    kubectl create namespace $KOF_NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
    kubectl label namespace $KOF_NAMESPACE istio-injection=enabled
    helm upgrade -i --reset-values --wait \
      --create-namespace -n istio-system kof-istio \
      oci://ghcr.io/k0rdent/kof/charts/kof-istio --version $KORDENT_VERSION

    helm upgrade -i --reset-values --wait \
      --create-namespace -n $KOF_NAMESPACE kof-operators \
      oci://ghcr.io/k0rdent/kof/charts/kof-operators --version $KORDENT_VERSION

    helm upgrade -i --reset-values --wait -n $KOF_NAMESPACE kof-mothership \
      --set kcm.installTemplates=true \
      oci://ghcr.io/k0rdent/kof/charts/kof-mothership --version $KORDENT_VERSION

    if [ "$(printf '%s\n' "$KORDENT_VERSION" "1.1.0" | sort -V | head -n1)" = "$KORDENT_VERSION" ] && [ "$KORDENT_VERSION" != "1.1.0" ]; then
      echo "Since we use KOF version ("$KORDENT_VERSION") less than "1.1.0", we need to apply some modifications..."
      # Since we use KOF version less than 1.1.0, we need to additionally do:
      kubectl apply --server-side --force-conflicts \
        -f https://github.com/grafana/grafana-operator/releases/download/v5.18.0/crds.yaml
    fi

    echo "Waiting until valid=true for all ServiceTemplate objects..."
    while true; do
      # Fetch current statuses
      statuses=$(kubectl get svctmpl -A -o jsonpath='{range .items[*]}{.metadata.namespace}{"|"}{.metadata.name}{"|"}{.status.valid}{"\n"}{end}')
      # Assume success initially
      all_valid=true
      while IFS='|' read -r ns name valid; do
        if [[ "$valid" != "true" ]]; then
          echo "Not ready: $ns/$name valid=$valid"
          all_valid=false
        fi
      done <<< "$statuses"

      if [[ "$all_valid" == "true" ]]; then
        echo "All ServiceTemplate objects have valid=true."
        break
      fi

      echo "Retrying in $POLL_INTERVAL seconds..."
      sleep $POLL_INTERVAL
    done

    echo "Waiting until all pods in namespace '$KOF_NAMESPACE' are Running..."
    while true; do
      # Get pods and their phases
      statuses=$(kubectl get pods -n "$KOF_NAMESPACE" -o jsonpath='{range .items[*]}{.metadata.name}{"|"}{.status.phase}{"\n"}{end}')
      
      # Assume all are running
      all_running=true

      while IFS='|' read -r name phase; do
        if [[ "$phase" != "Running" ]]; then
          echo "Pod $name is in phase $phase"
          all_running=false
        fi
      done <<< "$statuses"

      if [[ "$all_running" == "true" ]]; then
        echo "All pods in namespace '$KOF_NAMESPACE' are Running."
        break
      fi

      echo "Retrying in $POLL_INTERVAL seconds..."
      sleep $POLL_INTERVAL
    done

fi
