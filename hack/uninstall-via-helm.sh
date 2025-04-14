#!/bin/bash

NAMESPACE=exalsius-system


safe_helm_uninstall() {
  if helm status "$1" --namespace $2 >/dev/null 2>&1; then
    helm uninstall "$1" --namespace $2 --wait
  else
    echo "Helm release '$1' not found. Skipping uninstall."
  fi
}

echo "uninstalling exalsius-operator"
safe_helm_uninstall exalsius $NAMESPACE

# clean up namespace
if kubectl get pods -n "$NAMESPACE" --no-headers --ignore-not-found | grep -q .; then
  echo "Cleaning up exalsius pods"
  kubectl delete pods --all -n "$NAMESPACE"
fi
if kubectl get jobs -n "$NAMESPACE" --no-headers --ignore-not-found | grep -q .; then
  echo "Cleaning up exalsius pods"
  kubectl delete jobs --all -n "$NAMESPACE"
fi

echo "uninstalling cluster-api"
safe_helm_uninstall capi-operator capi-system

echo "uninstalling volcano"
safe_helm_uninstall volcano volcano-system

echo "uninstalling cert-manager"
safe_helm_uninstall cert-manager cert-manager
