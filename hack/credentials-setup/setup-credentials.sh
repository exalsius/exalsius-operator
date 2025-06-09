#!/bin/bash

set -euo pipefail

# This namespace is used to store the credentials
# currently only the kcm-system namespace is supported, see https://github.com/k0rdent/kcm/issues/990
NAMESPACE="kcm-system"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

create_namespace() {
    local namespace=$1
    if ! kubectl get namespace "$namespace" &>/dev/null; then
        echo "Creating namespace $namespace..."
        kubectl create namespace "$namespace"
    else
        echo "Namespace $namespace already exists"
    fi
}

usage() {
    echo "Usage: $0 [--config CONFIG_FILE] [--provider PROVIDER] [--description DESCRIPTION] [--name NAME]"
    echo
    echo "Options:"
    echo "  --config CONFIG_FILE    Path to config file"
    echo "  --provider PROVIDER     Cloud provider (aws, gcp, docker)"
    echo "  --description DESC      Description for the credentials (default: 'PROVIDER Credentials')"
    echo "  --name NAME             Name for the credential (default: 'PROVIDER-credentials')"
    echo
    echo "For AWS, set these environment variables:"
    echo "  ACCESS_KEY_ID"
    echo "  SECRET_ACCESS_KEY"
    echo
    echo "For GCP, set one of these environment variables:"
    echo "  GCP_B64ENCODED_CREDENTIALS    Base64 encoded GCP service account key"
    echo "  GCP_CREDENTIALS_FILE         Path to GCP service account JSON key file"
    echo
    echo "For Docker, no additional environment variables are required"
    exit 1
}

process_templates() {
    local provider=$1
    local template_dir="${SCRIPT_DIR}/templates/${provider}"

    if [[ ! -d "$template_dir" ]]; then
        echo "Error: No templates found for provider $provider"
        exit 1
    fi

    for template in "${template_dir}"/*.yaml; do
        if [[ -f "$template" ]]; then
            echo "Processing template: $(basename "$template")"
            envsubst < "$template" | kubectl apply -f -
        fi
    done
}

CONFIG_FILE=""
PROVIDER=""
DESCRIPTION=""
NAME=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --config)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --provider)
            PROVIDER="$2"
            shift 2
            ;;
        --description)
            DESCRIPTION="$2"
            shift 2
            ;;
        --name)
            NAME="$2"
            shift 2
            ;;
        *)
            usage
            ;;
    esac
done

if [[ -n "$CONFIG_FILE" ]]; then
    if [[ ! -f "$CONFIG_FILE" ]]; then
        echo "Error: Config file $CONFIG_FILE does not exist"
        exit 1
    fi
    source "$CONFIG_FILE"
fi

if [[ -z "$PROVIDER" ]]; then
    echo "Error: Provider is required"
    usage
fi

if [[ -z "$DESCRIPTION" ]]; then
    DESCRIPTION="${PROVIDER^} Credentials"
fi

if [[ -z "$NAME" ]]; then
    NAME="${PROVIDER}-credentials"
fi

export namespace="$NAMESPACE"
export provider="$PROVIDER"
export description="$DESCRIPTION"
export credential_name="$NAME"

create_namespace "$NAMESPACE"

case "$PROVIDER" in
    aws)
        if [[ -z "${ACCESS_KEY_ID:-}" ]] || [[ -z "${SECRET_ACCESS_KEY:-}" ]]; then
            echo "Error: AWS credentials not set"
            echo "Please set ACCESS_KEY_ID and SECRET_ACCESS_KEY environment variables"
            exit 1
        fi
        export ACCESS_KEY_ID
        export SECRET_ACCESS_KEY
        process_templates "aws"
        ;;
    gcp)
        if [[ -z "${GCP_B64ENCODED_CREDENTIALS:-}" ]]; then
            if [[ -z "${GCP_CREDENTIALS_FILE:-}" ]]; then
                echo "Error: GCP credentials not set"
                echo "Please either:"
                echo "1. Set GCP_B64ENCODED_CREDENTIALS environment variable, or"
                echo "2. Set GCP_CREDENTIALS_FILE environment variable pointing to your GCP JSON credentials file"
                exit 1
            fi
            if [[ ! -f "${GCP_CREDENTIALS_FILE}" ]]; then
                echo "Error: GCP credentials file ${GCP_CREDENTIALS_FILE} does not exist"
                exit 1
            fi
            export GCP_B64ENCODED_CREDENTIALS=$(cat "${GCP_CREDENTIALS_FILE}" | base64 -w 0)
        fi
        process_templates "gcp"
        ;;
    docker)
        process_templates "docker"
        ;;
    *)
        echo "Error: Unsupported provider $PROVIDER"
        echo "Supported providers: aws, gcp, docker"
        exit 1
        ;;
esac

echo "Credentials setup completed successfully"
