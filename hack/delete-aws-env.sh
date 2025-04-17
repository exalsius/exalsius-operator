#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AWS_DIR="$SCRIPT_DIR/aws-bootstrap"
cd "$AWS_DIR"

if [[ ! -f .venv/bin/activate ]]; then
  echo "ERROR: virtualenv not found. Please run your bootstrap script first to create .venv." >&2
  exit 1
fi

# activate the existing venv
# shellcheck disable=SC1091
source .venv/bin/activate

# run the teardown playbook
ansible-playbook teardown.yml