#!/bin/bash

NAMESPACE=exalsius-system

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
helm install capi-operator capi-operator/cluster-api-operator \
  --create-namespace \
  --namespace capi-system \
  --wait

echo "installing exalsius-operator umbrella chart"
helm upgrade --install exalsius ../charts/exalsius-operator \
  --namespace $NAMESPACE \
  --create-namespace \
  $VALUES_ARG \
  --wait

