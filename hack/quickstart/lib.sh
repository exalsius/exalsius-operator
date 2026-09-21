#!/usr/bin/env bash
# Shared helpers for the quickstart scripts. Sourced, not executed.

set -euo pipefail

QUICKSTART_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export QUICKSTART_ROOT

# shellcheck source=versions.env
source "$QUICKSTART_ROOT/versions.env"

# --- logging -----------------------------------------------------------------
_c() { if [[ -t 2 ]]; then printf '\033[%sm' "$1" >&2; fi; }
log()  { _c '0;36'; printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; _c '0'; }
info() { _c '0;32'; printf '  ok  %s\n' "$*" >&2; _c '0'; }
warn() { _c '1;33'; printf '  !!  %s\n' "$*" >&2; _c '0'; }
err()  { _c '1;31'; printf '  xx  %s\n' "$*" >&2; _c '0'; }
die()  { err "$*"; exit 1; }

# Run a command, echoing it first. DRY_RUN=1 prints only.
run() {
  _c '0;90'; printf '    $ %s\n' "$*" >&2; _c '0'
  [[ "${DRY_RUN:-0}" == "1" ]] && return 0
  "$@"
}

require_cmd() {
  local c
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || die "required command not found: $c"
  done
}

# Poll until <cmd...> succeeds. wait_for <timeout-seconds> <description> <cmd...>
wait_for() {
  local timeout="$1" desc="$2"; shift 2
  local deadline=$(( $(date +%s) + timeout ))
  log "waiting (up to ${timeout}s) for: $desc"
  [[ "${DRY_RUN:-0}" == "1" ]] && { info "[dry-run] $desc"; return 0; }
  until "$@" >/dev/null 2>&1; do
    (( $(date +%s) < deadline )) || die "timed out waiting for: $desc"
    sleep 5
  done
  info "$desc"
}

# Read a value interactively when a variable is empty. prompt_var <VAR> <prompt>
prompt_var() {
  local var="$1" prompt="$2" ans
  [[ -n "${!var:-}" ]] && return 0
  [[ -t 0 ]] || die "$var not set and no TTY to prompt"
  read -r -p "$prompt: " ans
  printf -v "$var" '%s' "$ans"
}

# --- kubernetes --------------------------------------------------------------
# All kubectl/helm calls go through these so the management kubeconfig is
# explicit and never collides with the reader's own KUBECONFIG.
kc()  { KUBECONFIG="$KUBECONFIG_OUT" kubectl "$@"; }
hlm() { KUBECONFIG="$KUBECONFIG_OUT" helm "$@"; }

# True when the jsonpath of a resource equals a value.
# kc_field_is <ns> <kind/name> <jsonpath> <expected>
kc_field_is() {
  local ns="$1" res="$2" path="$3" want="$4" got
  got="$(kc ${ns:+-n "$ns"} get "$res" -o jsonpath="$path" 2>/dev/null)" || return 1
  [[ "$got" == "$want" ]]
}

# True when a resource's Ready condition is True and (if reported) its status
# generation has caught up with its spec. kc_ready <ns> <kind/name>
kc_ready() {
  local ns="$1" res="$2" out gen obs ready
  out="$(kc ${ns:+-n "$ns"} get "$res" \
    -o jsonpath='{.metadata.generation} {.status.observedGeneration} {.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)" || return 1
  read -r gen obs ready <<<"$out"
  [[ "$ready" == "True" ]] || return 1
  [[ -z "$obs" || "$obs" == "$gen" ]]
}
