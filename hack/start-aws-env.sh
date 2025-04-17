#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AWS_DIR="$SCRIPT_DIR/aws-bootstrap"
cd "$AWS_DIR"

ensure_virtualenv() {
  if ! command -v virtualenv >/dev/null 2>&1; then
    echo "Installing virtualenv..."
    if command -v pip3 >/dev/null 2>&1; then
      pip3 install --user virtualenv
    else
      sudo apt-get update
      sudo apt-get install -y python3-pip
      pip3 install --user virtualenv
    fi
    # ensure ~/.local/bin is on PATH
    export PATH="$HOME/.local/bin:$PATH"
  fi
}

create_and_activate_venv() {
  virtualenv .venv
  # shellcheck disable=SC1091
  source .venv/bin/activate
}

install_python_requirements() {
  pip install --upgrade pip
  pip install -r requirements.txt
}

install_ansible_dependencies() {
  ansible-galaxy install -r requirements.yml
}

run_bootstrap_playbook() {
  ansible-playbook bootstrap.yml
}

install_via_helm() {
  export KUBECONFIG="$SCRIPT_DIR/aws-bootstrap/fetched/k3s.kubeconfig"
  cd "$SCRIPT_DIR"
  bash install-via-helm.sh
}

main() {
  ensure_virtualenv
  create_and_activate_venv
  install_python_requirements
  install_ansible_dependencies
  run_bootstrap_playbook
  install_via_helm
}

main "$@"
