#!/bin/bash

# The namespace where the user will be able to deploy clusters/colonies
NAMESPACE="default"
# The credentials that the user will be able to use to deploy clusters/colonies
# the names should map to the names used for the credentials in the credentials-setup directory
CREDENTIALS=("aws-credential" "gcp-credential" "docker-stub-credential")
# The cluster template chains that the user will be able to use to deploy clusters/colonies
# the names should map to the names used for the cluster template chains in the templates directory
CLUSTER_TEMPLATE_CHAINS=("aws" "gcp" "docker" "remote-cluster")
# The service template chains that the user will be able to use to deploy services
# the names should map to the names used for the service template chains in the templates directory
SERVICE_TEMPLATE_CHAINS=("exalsius-services")

while [[ $# -gt 0 ]]; do
  case $1 in
    -n|--namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    -c|--credentials)
      shift
      while [[ $# -gt 0 && ! $1 =~ ^- ]]; do
        CREDENTIALS+=("$1")
        shift
      done
      ;;
    -t|--templates)
      shift
      while [[ $# -gt 0 && ! $1 =~ ^- ]]; do
        CLUSTER_TEMPLATE_CHAINS+=("$1")
        shift
      done
      ;;
    -s|--service-templates)
      shift
      while [[ $# -gt 0 && ! $1 =~ ^- ]]; do
        SERVICE_TEMPLATE_CHAINS+=("$1")
        shift
      done
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

for template in templates/cluster-templates/*.yaml; do
  if [ -f "$template" ]; then
    echo "Applying cluster template: $template"
    kubectl apply -f "$template"
  fi
done

for template in templates/service-templates/*.yaml; do
  if [ -f "$template" ]; then
    echo "Applying service template: $template"
    kubectl apply -f "$template"
  fi
done

if kubectl get accessmanagement kcm &>/dev/null; then
  # If it exists, patch it
  PATCH=$(cat <<EOF
{
  "spec": {
    "accessRules": [
      {
        "credentials": $(printf '%s\n' "${CREDENTIALS[@]}" | jq -R . | jq -s .),
        "clusterTemplateChains": $(printf '%s\n' "${CLUSTER_TEMPLATE_CHAINS[@]}" | jq -R . | jq -s .),
        "serviceTemplateChains": $(printf '%s\n' "${SERVICE_TEMPLATE_CHAINS[@]}" | jq -R . | jq -s .),
        "targetNamespaces": {
          "list": ["$NAMESPACE"]
        }
      }
    ]
  }
}
EOF
)
  kubectl patch accessmanagement kcm --type=merge -p "$PATCH"
else
  # If it doesn't exist, create it
  cat <<EOF | kubectl apply -f -
apiVersion: k0rdent.mirantis.com/v1alpha1
kind: AccessManagement
metadata:
  name: kcm
spec:
  accessRules:
  - credentials: $(printf '%s\n' "${CREDENTIALS[@]}" | jq -R . | jq -s .)
    clusterTemplateChains: $(printf '%s\n' "${CLUSTER_TEMPLATE_CHAINS[@]}" | jq -R . | jq -s .)
    serviceTemplateChains: $(printf '%s\n' "${SERVICE_TEMPLATE_CHAINS[@]}" | jq -R . | jq -s .)
    targetNamespaces:
      list:
        - $NAMESPACE
EOF
fi

