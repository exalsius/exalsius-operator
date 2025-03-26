#!/bin/bash

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# This creates a kind cluster with the config in kind-config.yaml
# The docker socket is mounted to enable Docker-based cluster-api providers and a NodePort Service for Skypilot API
# is exposed on port 30050
kind create cluster --config "${SCRIPT_DIR}/kind-config.yaml"
