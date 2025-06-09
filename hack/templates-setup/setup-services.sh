#!/bin/bash

set -euo pipefail

install_service_template() {
    local name=$1
    local chart=$2
    local version=$3
    
    echo "Installing $name..."
    helm install "$name" "oci://ghcr.io/k0rdent/catalog/charts/$chart" \
        --version "$version" \
        -n kcm-system \
        --wait
}

# Install all service templates
install_service_template "gpu-operator" "gpu-operator-service-template" "24.9.2"
install_service_template "amd-gpu-operator" "amd-gpu-operator-service-template" "v1.2.2"
install_service_template "cert-manager" "cert-manager-service-template" "1.17.2"
install_service_template "kuberay-operator" "kuberay-operator-service-template" "1.3.2"
install_service_template "ray-cluster" "ray-cluster-service-template" "1.3.2"

echo "All service templates have been installed successfully!"