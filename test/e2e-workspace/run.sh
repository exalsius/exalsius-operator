#!/usr/bin/env bash
#
# Workspace operator E2E assertion suite (ADR-0001).
#
# Drives every WorkspaceClass through every capability on the live multi-cluster
# Istio-ambient mesh and asserts the runbook PASS gates: per-class behaviour
# (resource override, prerequisite install + sharing, resource injection),
# routing (hostname scheme, RoutesReady, live HTTP curl over the mesh, SSH/TCP
# port pool), serviceName override, deletion ordering, cross-cluster routing,
# orphan sweep, the infra-not-ready negative, and failureContext.
#
# Runs every phase, records pass/fail, prints a summary and exits non-zero if
# anything failed. Pre-cleans its own resources at start; on full success it
# tears them down (fixtures/classes stay), on any failure it leaves them up for
# inspection. Idempotent — safe to re-run.
#
# This harness is ENVIRONMENT-AGNOSTIC: it carries no topology defaults. The
# caller supplies the kube-contexts, ClusterDeployment names, namespace, tenant
# domain and Gateway as environment variables (see the required list below).
# local-dev-env's `make workspace-testing-operator` provisions the multi-cluster
# Istio-ambient mesh, applies the env-side glue + the operator's WorkspaceClasses,
# then invokes this via `make test-e2e-workspace` with those vars exported.
#
# To run from the operator repo directly, point the vars at an already-running
# environment and: make test-e2e-workspace
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Minimal coloured output — self-contained (no external dependency).
print_status()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
print_success() { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
print_warning() { printf '\033[1;33m!\033[0m %s\n' "$*"; }
print_error()   { printf '\033[1;31m✗\033[0m %s\n' "$*"; }

# Topology — supplied entirely by the caller; no defaults live in the operator
# repo. Fail fast with a clear message if any is missing.
: "${MGMT:?set MGMT to the management kube-context}"
: "${REG:?set REG to the regional kube-context}"
: "${CHILD1:?set CHILD1 to the first child kube-context}"
: "${CHILD2:?set CHILD2 to the second child kube-context}"
: "${NS:?set NS to the workspace namespace}"
: "${CD1:?set CD1 to the first child ClusterDeployment name}"
: "${CD2:?set CD2 to the second child ClusterDeployment name}"
: "${REG_CLUSTER:?set REG_CLUSTER to the regional cluster logical name used by the KOF label and kubeconfig secret prefix}"
: "${GW:?set GW to the tenant Gateway name}"
: "${GW_NS:?set GW_NS to the tenant Gateway namespace}"
: "${DOMAIN:?set DOMAIN to the tenant domain}"
: "${OPERATOR_NS:?set OPERATOR_NS to the namespace the operator is deployed in}"
: "${GW_MANIFEST:?set GW_MANIFEST to the tenant Gateway manifest path used to restore the gateway in the infra-not-ready negative phase}"

DEP="${SCRIPT_DIR}/workspaces"

# apply_ws <file> — render an env-parameterized workspace fixture (${CD1}/${CD2}/
# ${NS}) and apply it on the management cluster.
apply_ws() {
  envsubst '${CD1} ${CD2} ${NS}' < "$DEP/$1" | kubectl --context "$MGMT" apply -f -
}

# GPU e2e (ADR-0002) — faked GPU nodes (patched capacity + label; nginx never
# touches the device). Test-defined constants, not topology.
GPU_MODEL="TESTGPU-A100"
GPU_MODEL_LABEL="exalsius.ai/gpu-model"
GPU_RAW_KEY="nvidia.com/gpu.product"
GPU_RAW_VAL="TESTGPU-RAW"

# Timeouts (seconds).
RUN_TIMEOUT=300   # reach a terminal phase (Running/Failed)
SETTLE=150        # status / route objects settle
SHORT=60          # quick data-path / object checks

PASS=0
FAIL=0
declare -a FAILED=()
LB_IP=""

# check <timeout> <desc> <snippet> — poll the snippet (eval'd in a subshell, so
# it sees the globals above) until it exits 0 or the timeout elapses.
check() {
  local timeout="$1" desc="$2" snippet="$3"
  local end=$(( SECONDS + timeout )) out rc
  while :; do
    out="$(eval "$snippet" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ]; then
      print_success "PASS  ${desc}"
      PASS=$(( PASS + 1 ))
      return 0
    fi
    [ "$SECONDS" -ge "$end" ] && break
    sleep 4
  done
  print_error "FAIL  ${desc}"
  [ -n "${out:-}" ] && printf '        %s\n' "$(printf '%s' "$out" | head -n2 | tr '\n' ' ')"
  FAILED+=("${desc}")
  FAIL=$(( FAIL + 1 ))
  return 1
}

discover_lb() {
  LB_IP="$(kubectl --context "$REG" -n "$GW_NS" get svc "${GW}-istio" \
    -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null)"
  if [ -z "$LB_IP" ]; then
    print_error "gateway Service ${GW}-istio has no LoadBalancer external IP"
    return 1
  fi
  return 0
}

preflight() {
  print_status "Preflight checks"
  local ok=1 c n
  for c in "$MGMT" "$REG" "$CHILD1" "$CHILD2"; do
    if ! kubectl config get-contexts -o name | grep -qx "$c"; then
      print_error "kube-context '$c' missing — run 'make up' / 'make setup-kcm-regional-child'"
      ok=0
    fi
  done
  command -v jq   >/dev/null 2>&1 || { print_error "jq not found on PATH"; ok=0; }
  command -v curl >/dev/null 2>&1 || { print_error "curl not found on PATH"; ok=0; }

  if ! kubectl --context "$MGMT" -n "$NS" get clusterdeployment "$CD1" >/dev/null 2>&1; then
    print_error "ClusterDeployment '$CD1' not found — run 'make setup-kcm-regional-child'"
    ok=0
  elif ! kubectl --context "$MGMT" -n "$NS" get clusterdeployment "$CD1" -o json 2>/dev/null \
       | jq -e ".metadata.labels[\"k0rdent.mirantis.com/kof-regional-cluster-name\"]==\"$REG_CLUSTER\"" >/dev/null; then
    print_error "ClusterDeployment '$CD1' missing KOF regional label — child not mesh-joined"
    ok=0
  fi
  kubectl --context "$MGMT" -n "$NS" get secret "${REG_CLUSTER}-kubeconfig" >/dev/null 2>&1 \
    || { print_error "secret ${REG_CLUSTER}-kubeconfig missing on mgmt"; ok=0; }

  if ! kubectl --context "$REG" -n "$GW_NS" get gateway "$GW" -o json 2>/dev/null \
       | jq -e '.status.conditions[]|select(.type=="Programmed")|.status=="True"' >/dev/null; then
    print_error "tenant Gateway '$GW' not Programmed — run 'make workspace-testing-operator' (setup)"
    ok=0
  fi

  n="$(kubectl --context "$MGMT" -n "$OPERATOR_NS" get deploy exalsius-operator \
        -o jsonpath='{.status.availableReplicas}' 2>/dev/null)"
  [ "${n:-0}" -ge 1 ] 2>/dev/null \
    || { print_error "exalsius-operator not available — run 'make only-update-exalsius'"; ok=0; }

  [ "$(kubectl --context "$MGMT" get crd workspacedeployments.workspaces.exalsius.ai \
        -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.status.properties.access.type}' 2>/dev/null)" = "array" ] \
    || { print_error "WSD CRD lacks status.access (stale CRDs) — apply config/crd/bases from the operator repo"; ok=0; }

  # GPU selection (ADR-0002) needs the Colony status.gpuInventory field — guards
  # against testing the GPU phase against pre-ADR-0002 CRDs.
  kubectl --context "$MGMT" get crd colonies.infra.exalsius.ai -o json 2>/dev/null \
    | jq -e '.spec.versions[0].schema.openAPIV3Schema.properties.status.properties.gpuInventory!=null' >/dev/null \
    || { print_error "Colony CRD lacks status.gpuInventory (stale CRDs) — apply config/crd/bases from the operator repo"; ok=0; }

  discover_lb || ok=0

  if [ "$ok" -ne 1 ]; then
    print_error "Preflight failed — fix the above and re-run."
    exit 1
  fi
  print_success "Preflight OK (gateway LB ${LB_IP})"
}

# Delete anything a prior run may have left so this run starts clean.
pre_clean() {
  print_status "Pre-cleaning leftover test resources"
  kubectl --context "$MGMT" -n "$NS" delete wsd \
    routed-a routed-b fx1 bad1 neg1 \
    test-single test-with-prereq-a test-with-prereq-b test-injection \
    gpu-ok gpu-wait gpu-absent gpu-raw \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl --context "$REG" delete ns ws-ghost --ignore-not-found >/dev/null 2>&1 || true
  unfake_gpu "$CHILD1" "$GPU_MODEL_LABEL"
  unfake_gpu "$CHILD2" "$GPU_RAW_KEY"
  sleep 3
}

ssh_port() { # ssh_port <wsd-name>
  kubectl --context "$MGMT" -n "$NS" get wsd "$1" -o json 2>/dev/null \
    | jq -r '.status.access[]|select(.protocol=="SSH")|.url' | sed 's#.*:##'
}

gpu_node() { # gpu_node <ctx> — first node name on the cluster
  kubectl --context "$1" get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}

# fake_gpu <ctx> <labelKey> <labelVal> <count> — advertise a fake GPU on a node:
# a model/GFD label plus nvidia.com/gpu capacity+allocatable (the kubelet
# preserves manually-advertised extended resources). Lets the scheduler place
# GPU pods on kind without a real device.
fake_gpu() {
  local ctx="$1" key="$2" val="$3" count="$4" node
  node="$(gpu_node "$ctx")"
  kubectl --context "$ctx" label node "$node" "$key=$val" --overwrite >/dev/null 2>&1
  kubectl --context "$ctx" patch node "$node" --subresource=status --type=json -p \
    "[{\"op\":\"add\",\"path\":\"/status/capacity/nvidia.com~1gpu\",\"value\":\"$count\"},{\"op\":\"add\",\"path\":\"/status/allocatable/nvidia.com~1gpu\",\"value\":\"$count\"}]" \
    >/dev/null 2>&1
}

# unfake_gpu <ctx> <labelKey> — remove the fake GPU label + capacity.
unfake_gpu() {
  local ctx="$1" key="$2" node
  node="$(gpu_node "$ctx")"
  kubectl --context "$ctx" label node "$node" "${key}-" >/dev/null 2>&1 || true
  kubectl --context "$ctx" patch node "$node" --subresource=status --type=json -p \
    '[{"op":"remove","path":"/status/capacity/nvidia.com~1gpu"},{"op":"remove","path":"/status/allocatable/nvidia.com~1gpu"}]' \
    >/dev/null 2>&1 || true
}

# ---- Per-class behaviour (baseline override, prereq, shared-prereq, injection)

phase_per_class() {
  print_status "Phase A — per-class behaviour"

  apply_ws baseline.yaml >/dev/null
  check "$RUN_TIMEOUT" "per-class baseline: test-single Running" \
    'kubectl --context $MGMT -n $NS get wsd test-single -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "per-class baseline: resource override (150m,192Mi,_exalsius) in ServiceSet values" \
    'v=$(kubectl --context $MGMT -n $NS get serviceset wsd-${CD1}-test-single -o jsonpath="{.spec.services[0].values}" 2>/dev/null);
     printf "%s" "$v" | grep -q "150m" && printf "%s" "$v" | grep -q "192Mi" && printf "%s" "$v" | grep -q "_exalsius"'

  apply_ws with-prereq.yaml >/dev/null
  check "$RUN_TIMEOUT" "per-class prereq: test-with-prereq-a Running" \
    'kubectl --context $MGMT -n $NS get wsd test-with-prereq-a -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "per-class prereq: prerequisite ServiceSet created" \
    '[ "$(kubectl --context $MGMT -n $NS get serviceset -l workspaces.exalsius.ai/role=prerequisite -o name 2>/dev/null | wc -l)" -ge 1 ]'

  apply_ws shared-prereq.yaml >/dev/null
  check "$RUN_TIMEOUT" "per-class shared-prereq: test-with-prereq-b Running" \
    'kubectl --context $MGMT -n $NS get wsd test-with-prereq-b -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "per-class shared-prereq: exactly ONE prerequisite ServiceSet (shared, not duplicated)" \
    '[ "$(kubectl --context $MGMT -n $NS get serviceset -l workspaces.exalsius.ai/role=prerequisite -o name 2>/dev/null | wc -l)" -eq 1 ]'

  apply_ws with-injection.yaml >/dev/null
  check "$RUN_TIMEOUT" "per-class injection: test-injection Running" \
    'kubectl --context $MGMT -n $NS get wsd test-injection -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "per-class injection: _exalsius.resources + custom demo.subchart paths (250m) in values" \
    'v=$(kubectl --context $MGMT -n $NS get serviceset wsd-${CD1}-test-injection -o jsonpath="{.spec.services[0].values}" 2>/dev/null);
     printf "%s" "$v" | grep -q "_exalsius" && printf "%s" "$v" | grep -q "demo" && printf "%s" "$v" | grep -q "250m"'
}

# ---- Infra-not-ready negative (gateway deliberately absent, then restored)

phase_negative() {
  print_status "Phase 4 — infra-not-ready negative (delete gateway, observe, restore)"
  kubectl --context "$REG" -n "$GW_NS" delete gateway "$GW" --ignore-not-found >/dev/null 2>&1 || true
  apply_ws negative.yaml >/dev/null

  check "$RUN_TIMEOUT" "negative: neg1 reaches Running (not Failed) with gateway absent" \
    'kubectl --context $MGMT -n $NS get wsd neg1 -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "negative: RoutesReady reason = RoutingInfraNotReady" \
    'kubectl --context $MGMT -n $NS get wsd neg1 -o json | jq -e ".status.conditions[]|select(.type==\"RoutesReady\")|.reason==\"RoutingInfraNotReady\"" >/dev/null'

  kubectl --context "$REG" apply -f "$GW_MANIFEST" >/dev/null
  kubectl --context "$REG" -n "$GW_NS" wait --for=condition=Programmed "gateway/$GW" --timeout=180s >/dev/null 2>&1 || true
  check "$SETTLE" "negative: neg1 recovers to RoutesReady=True after gateway returns" \
    'kubectl --context $MGMT -n $NS get wsd neg1 -o json | jq -e ".status.conditions[]|select(.type==\"RoutesReady\")|.status==\"True\"" >/dev/null'

  kubectl --context "$MGMT" -n "$NS" delete wsd neg1 --ignore-not-found --wait=false >/dev/null 2>&1 || true
}

# ---- Routing happy path + hostname scheme + SSH/TCP pool (child 1)

phase_routing() {
  print_status "Phase 5/6/7 — routing happy path, hostname scheme, SSH/TCP pool"
  discover_lb || true   # refresh: the negative phase recreated the gateway Service
  apply_ws routed.yaml >/dev/null

  check "$RUN_TIMEOUT" "routing: routed-a Running" \
    'kubectl --context $MGMT -n $NS get wsd routed-a -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$RUN_TIMEOUT" "routing: routed-b Running" \
    'kubectl --context $MGMT -n $NS get wsd routed-b -o json | jq -e ".status.phase==\"Running\"" >/dev/null'

  check "$SETTLE" "routing: ws-routed-a child ns has workspace + ambient + waypoint labels" \
    'kubectl --context $CHILD1 get ns ws-routed-a -o json | jq -e ".metadata.labels[\"workspaces.exalsius.ai/workspace\"]==\"routed-a\" and .metadata.labels[\"istio.io/dataplane-mode\"]==\"ambient\" and .metadata.labels[\"istio.io/use-waypoint\"]==\"istio-waypoint\"" >/dev/null'

  check "$SETTLE" "routing: access[http] = bare hostname routed-a.$DOMAIN" \
    'kubectl --context $MGMT -n $NS get wsd routed-a -o json | jq -e ".status.access[]|select(.name==\"http\")|.url==\"http://routed-a.$DOMAIN\"" >/dev/null'
  check "$SETTLE" "routing: access[api] = suffix hostname routed-a-api.$DOMAIN" \
    'kubectl --context $MGMT -n $NS get wsd routed-a -o json | jq -e ".status.access[]|select(.name==\"api\")|.url==\"http://routed-a-api.$DOMAIN\"" >/dev/null'
  check "$SETTLE" "routing: routed-a RoutesReady=True" \
    'kubectl --context $MGMT -n $NS get wsd routed-a -o json | jq -e ".status.conditions[]|select(.type==\"RoutesReady\")|.status==\"True\"" >/dev/null'

  check "$SETTLE" "routing: HTTPRoute http Accepted on regional" \
    'kubectl --context $REG -n ws-routed-a get httproute http -o json | jq -e "[.status.parents[].conditions[]|select(.type==\"Accepted\")|.status==\"True\"]|any" >/dev/null'

  check "$SHORT" "routing: curl routed-a returns 200 over the mesh" \
    '[ "$(curl -s -m 5 -o /dev/null -w "%{http_code}" -H "Host: routed-a.$DOMAIN" http://$LB_IP/)" = "200" ]'

  check "$SETTLE" "routing: routed-a and routed-b have distinct SSH pool ports" \
    'pa=$(ssh_port routed-a); pb=$(ssh_port routed-b); [ -n "$pa" ] && [ -n "$pb" ] && [ "$pa" != "$pb" ]'
  check "$SHORT" "routing: routed-a SSH port open at gateway LB" \
    'p=$(ssh_port routed-a); [ -n "$p" ] && timeout 3 bash -c "echo > /dev/tcp/$LB_IP/$p"'
}

# ---- serviceName override

phase_servicename() {
  print_status "Phase 8 — serviceName override"
  apply_ws serviceName.yaml >/dev/null
  check "$RUN_TIMEOUT" "serviceName: fx1 Running" \
    'kubectl --context $MGMT -n $NS get wsd fx1 -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "serviceName: mirror Service proxy-public exists on regional" \
    'kubectl --context $REG -n ws-fx1 get svc proxy-public >/dev/null 2>&1'
  check "$SETTLE" "serviceName: HTTPRoute ui backend is proxy-public" \
    'kubectl --context $REG -n ws-fx1 get httproute ui -o json | jq -e ".spec.rules[0].backendRefs[0].name==\"proxy-public\"" >/dev/null'
}

# ---- Deletion ordering + port release (delete child-1 routed-b)

phase_deletion() {
  print_status "Phase 9 — deletion ordering + port release"
  kubectl --context "$MGMT" -n "$NS" delete wsd routed-b --wait=false --ignore-not-found >/dev/null 2>&1 || true
  check "$SETTLE" "deletion: regional routes for routed-b drained" \
    '[ "$(kubectl --context $REG -n ws-routed-b get httproute,tcproute --no-headers 2>/dev/null | wc -l)" -eq 0 ]'
  check "$SETTLE" "deletion: ServiceSet for routed-b removed" \
    '! kubectl --context $MGMT -n $NS get serviceset wsd-${CD1}-routed-b >/dev/null 2>&1'
  check "$SETTLE" "deletion: routed-b WSD fully removed (finalizer cleared)" \
    '! kubectl --context $MGMT -n $NS get wsd routed-b >/dev/null 2>&1'
}

# ---- Cross-cluster: routed-b on child 2, through the one regional gateway

phase_crosscluster() {
  print_status "Phase 13 — cross-cluster routing (routed-b on child 2)"
  apply_ws routed-cross.yaml >/dev/null
  check "$RUN_TIMEOUT" "cross-cluster: routed-b (child 2) Running" \
    'kubectl --context $MGMT -n $NS get wsd routed-b -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "cross-cluster: workload actually lives on child 2" \
    'kubectl --context $CHILD2 -n ws-routed-b get deploy --no-headers 2>/dev/null | grep -q .'
  check "$SETTLE" "cross-cluster: routed-a and routed-b distinct SSH ports (regional-wide pool)" \
    'pa=$(ssh_port routed-a); pb=$(ssh_port routed-b); [ -n "$pa" ] && [ -n "$pb" ] && [ "$pa" != "$pb" ]'
  check "$SETTLE" "cross-cluster: both mirror namespaces coexist on regional" \
    'kubectl --context $REG get ns ws-routed-a ws-routed-b >/dev/null 2>&1'
  check "$SHORT" "cross-cluster: curl routed-a (child 1) = 200" \
    '[ "$(curl -s -m 5 -o /dev/null -w "%{http_code}" -H "Host: routed-a.$DOMAIN" http://$LB_IP/)" = "200" ]'
  check "$SHORT" "cross-cluster: curl routed-b (child 2) = 200" \
    '[ "$(curl -s -m 5 -o /dev/null -w "%{http_code}" -H "Host: routed-b.$DOMAIN" http://$LB_IP/)" = "200" ]'
}

# ---- Orphan sweep (residual mirror namespace with no live WSD)

phase_orphan() {
  print_status "Phase 10 — orphan sweep"
  kubectl --context "$REG" create ns ws-ghost --dry-run=client -o yaml | kubectl --context "$REG" apply -f - >/dev/null 2>&1
  kubectl --context "$REG" label ns ws-ghost workspaces.exalsius.ai/workspace=ghost --overwrite >/dev/null 2>&1
  kubectl --context "$REG" -n ws-ghost apply -f - >/dev/null 2>&1 <<EOF
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: ssh
  labels:
    workspaces.exalsius.ai/workspace: ghost
spec:
  parentRefs:
    - name: ${GW}
      namespace: ${GW_NS}
      sectionName: ssh-2202
  rules:
    - backendRefs:
        - name: svc
          port: 22
EOF
  kubectl --context "$MGMT" -n "$OPERATOR_NS" rollout restart deploy/exalsius-operator >/dev/null 2>&1
  kubectl --context "$MGMT" -n "$OPERATOR_NS" rollout status  deploy/exalsius-operator --timeout=120s >/dev/null 2>&1 || true
  check 120 "orphan sweep: ws-ghost reclaimed (gone or Terminating)" \
    '! kubectl --context $REG get ns ws-ghost >/dev/null 2>&1 || kubectl --context $REG get ns ws-ghost -o json | jq -e ".status.phase==\"Terminating\"" >/dev/null'
  check "$SHORT" "orphan sweep: live ws-routed-a namespace untouched" \
    'kubectl --context $REG get ns ws-routed-a >/dev/null 2>&1'
}

# ---- failureContext (forced Helm failure)

phase_failurecontext() {
  print_status "Phase 11 — failureContext"
  apply_ws broken.yaml >/dev/null
  check "$RUN_TIMEOUT" "failureContext: bad1 reaches Failed" \
    'kubectl --context $MGMT -n $NS get wsd bad1 -o json | jq -e ".status.phase==\"Failed\"" >/dev/null'
  check "$SETTLE" "failureContext: reason HelmReleaseFailed + ServiceSet snapshot present" \
    'kubectl --context $MGMT -n $NS get wsd bad1 -o json | jq -e ".status.failureContext.reason==\"HelmReleaseFailed\" and (.status.failureContext.serviceSetStatus!=null)" >/dev/null'
  check "$SETTLE" "failureContext: recentEvents[0] is the HelmReleaseFailed event" \
    'kubectl --context $MGMT -n $NS get wsd bad1 -o json | jq -e ".status.failureContext.recentEvents[0].reason==\"HelmReleaseFailed\"" >/dev/null'
}

# ---- GPU selection / capacity / FCFS / raw selector / inventory (ADR-0002)

phase_gpu() {
  print_status "Phase 12 — GPU selection, capacity gating, FCFS, raw selector, inventory"

  # Fake one GPU on a child-1 node (canonical model label) and one on a child-2
  # node (raw GFD label only — no model label, proving the raw-selector path).
  fake_gpu "$CHILD1" "$GPU_MODEL_LABEL" "$GPU_MODEL" 1
  fake_gpu "$CHILD2" "$GPU_RAW_KEY" "$GPU_RAW_VAL" 1

  # Absent model -> terminal Failed with the right reason.
  apply_ws gpu-absent.yaml >/dev/null
  check "$RUN_TIMEOUT" "gpu: absent model -> Failed (GpuOfferingUnavailable)" \
    'kubectl --context $MGMT -n $NS get wsd gpu-absent -o json | jq -e ".status.phase==\"Failed\" and .status.failureContext.reason==\"GpuOfferingUnavailable\"" >/dev/null'

  # Present model -> Running, placed on the labelled node with the injected
  # nodeSelector + nvidia.com/gpu request.
  apply_ws gpu-ok.yaml >/dev/null
  check "$RUN_TIMEOUT" "gpu: present model -> gpu-ok Running" \
    'kubectl --context $MGMT -n $NS get wsd gpu-ok -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "gpu: gpu-ok pod has injected nodeSelector ($GPU_MODEL_LABEL) + nvidia.com/gpu request" \
    'p=$(kubectl --context $CHILD1 -n ws-gpu-ok get pods -o json 2>/dev/null);
     printf "%s" "$p" | jq -e ".items[0].spec.nodeSelector[\"'$GPU_MODEL_LABEL'\"]==\"'$GPU_MODEL'\"" >/dev/null &&
     printf "%s" "$p" | jq -e ".items[0].spec.containers[]|select(.name==\"nginx\")|.resources.requests[\"nvidia.com/gpu\"]==\"1\"" >/dev/null'

  # Contended request for the (now full) single-GPU node -> Waiting, no release.
  apply_ws gpu-wait.yaml >/dev/null
  check "$RUN_TIMEOUT" "gpu: contended request -> gpu-wait Waiting" \
    'kubectl --context $MGMT -n $NS get wsd gpu-wait -o json | jq -e ".status.phase==\"Waiting\"" >/dev/null'
  check "$SHORT" "gpu: gpu-wait creates no ServiceSet while held" \
    '! kubectl --context $MGMT -n $NS get serviceset wsd-${CD1}-gpu-wait >/dev/null 2>&1'

  # Free the GPU -> the waiter proceeds.
  kubectl --context "$MGMT" -n "$NS" delete wsd gpu-ok --wait=false --ignore-not-found >/dev/null 2>&1 || true
  check "$RUN_TIMEOUT" "gpu: gpu-wait proceeds to Running once capacity frees" \
    'kubectl --context $MGMT -n $NS get wsd gpu-wait -o json | jq -e ".status.phase==\"Running\"" >/dev/null'

  # Raw selector on a node lacking the model label -> Running on child 2.
  apply_ws gpu-raw.yaml >/dev/null
  check "$RUN_TIMEOUT" "gpu: raw selector -> gpu-raw Running on child 2" \
    'kubectl --context $MGMT -n $NS get wsd gpu-raw -o json | jq -e ".status.phase==\"Running\"" >/dev/null'
  check "$SETTLE" "gpu: gpu-raw pod nodeSelector is the exact raw GFD label" \
    'kubectl --context $CHILD2 -n ws-gpu-raw get pods -o json 2>/dev/null | jq -e ".items[0].spec.nodeSelector[\"'$GPU_RAW_KEY'\"]==\"'$GPU_RAW_VAL'\"" >/dev/null'

  # Colony inventory: nudge a reconcile, then assert the model is advertised.
  for c in $(kubectl --context "$MGMT" get colony -A -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name} {end}' 2>/dev/null); do
    kubectl --context "$MGMT" -n "${c%/*}" annotate colony "${c#*/}" exalsius.ai/gpu-test="probe" --overwrite >/dev/null 2>&1 || true
  done
  check "$RUN_TIMEOUT" "gpu: Colony status.gpuInventory advertises $GPU_MODEL" \
    'kubectl --context $MGMT get colony -A -o json | jq -e "[.items[].status.gpuInventory[]?.offerings[]?|select(.model==\"'$GPU_MODEL'\")]|any" >/dev/null'

  # Cleanup workspaces + node fakes.
  kubectl --context "$MGMT" -n "$NS" delete wsd gpu-wait gpu-absent gpu-raw --ignore-not-found --wait=false >/dev/null 2>&1 || true
  unfake_gpu "$CHILD1" "$GPU_MODEL_LABEL"
  unfake_gpu "$CHILD2" "$GPU_RAW_KEY"
}

teardown_pass() {
  print_status "All passed — tearing down test workspaces (classes stay)"
  # Delete by name (the fixtures are envsubst templates, so `delete -f` on the
  # raw dir wouldn't resolve ${CD1}/${NS}).
  kubectl --context "$MGMT" -n "$NS" delete wsd \
    routed-a routed-b fx1 bad1 neg1 \
    test-single test-with-prereq-a test-with-prereq-b test-injection \
    gpu-ok gpu-wait gpu-absent gpu-raw \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl --context "$REG" delete ns ws-ghost --ignore-not-found >/dev/null 2>&1 || true
  unfake_gpu "$CHILD1" "$GPU_MODEL_LABEL"
  unfake_gpu "$CHILD2" "$GPU_RAW_KEY"
}

leave_for_debug() {
  print_warning "Failures present — leaving resources up for inspection:"
  print_warning "  kubectl --context $MGMT -n $NS get wsd"
  print_warning "  reset with:  make workspace-testing-operator-clean"
}

summary() {
  echo
  print_status "==== SUMMARY ===="
  echo "  ${PASS} passed, ${FAIL} failed"
  if [ "$FAIL" -gt 0 ]; then
    local f
    for f in "${FAILED[@]}"; do
      print_error "  FAILED: ${f}"
    done
  fi
}

main() {
  preflight
  pre_clean
  phase_per_class
  phase_negative
  phase_routing
  phase_servicename
  phase_deletion
  phase_crosscluster
  phase_orphan
  phase_failurecontext
  phase_gpu
  summary
  if [ "$FAIL" -eq 0 ]; then
    teardown_pass
    print_success "Workspace operator E2E suite passed (${PASS} checks)"
    exit 0
  fi
  leave_for_debug
  exit 1
}

main "$@"
