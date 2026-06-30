#!/usr/bin/env bash
# test/e2e_install.sh — E2E install test against an ACTIVE cluster kubeconfig.
#
# Runs install_kcloud_stack.sh with kind-safe flags, then asserts:
#   1. Helm releases are in "deployed" state
#   2. Web UI NodePort (30001) is reachable (HTTP 200)
#   3. Backend /api/devices responds with a JSON array
#   4. A second run is idempotent (converges, no errors)
#
# Works against both:
#   - A kind cluster (created by kind_up.sh) — requires --skip-operators,
#     storage falls back to local-path (no NFS).
#   - A REAL cluster kubeconfig (3-node test cluster) — full stack,
#     pass --kubeconfig <path> and omit --kind-mode if real hardware present.
#
# Usage:
#   ./test/e2e_install.sh [OPTIONS]
#
# Options:
#   --kubeconfig <path>   Path to kubeconfig (default: $KUBECONFIG or ~/.kube/config)
#   --cluster-name <n>    Kind cluster name to derive --access-ip from (default: kcloud-e2e)
#   --kind-mode           Force kind-safe flags: --skip-operators, local-path storage
#                         (auto-detected when cluster has no real nodes or is named kcloud-e2e)
#   --app-namespace <ns>  App namespace to install into (default: llm-evaluation-e2e)
#   --keep                Do not delete helm releases after test (default: cleanup on exit)
#   --timeout <secs>      Per-rollout wait timeout (default: 300)
#   -h|--help             Show this help and exit 0
#
# Exit codes:
#   0  — all assertions passed
#   1  — assertion or install failure
#   3  — prerequisites missing (kind/kubectl absent)

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
LIB_DIR="${REPO_ROOT}/scripts/lib"

# Source log helpers
# shellcheck source=scripts/lib/log.sh
source "${LIB_DIR}/log.sh"

# ERR trap
trap '_on_err $? "$BASH_COMMAND" ${BASH_LINENO[0]}' ERR
_on_err() {
  local ec="$1" cmd="$2" line="$3"
  log_error "Command failed at line ${line}: ${cmd} (exit ${ec})"
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
KUBECONFIG_PATH="${KUBECONFIG:-${HOME}/.kube/config}"
CLUSTER_NAME="kcloud-e2e"
KIND_MODE=""          # "true" | "false" | "" (auto-detect)
APP_NAMESPACE="llm-evaluation-e2e"
KEEP=false
TIMEOUT=300
ACCESS_IP=""

INSTALLER="${REPO_ROOT}/scripts/install_kcloud_stack.sh"

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kubeconfig)    KUBECONFIG_PATH="$2"; shift 2 ;;
    --cluster-name)  CLUSTER_NAME="$2";    shift 2 ;;
    --kind-mode)     KIND_MODE="true";     shift ;;
    --app-namespace) APP_NAMESPACE="$2";   shift 2 ;;
    --keep)          KEEP=true;            shift ;;
    --timeout)       TIMEOUT="$2";         shift 2 ;;
    -h|--help)
      cat <<'USAGE'
e2e_install.sh — kcloud-tool E2E install test

Runs install_kcloud_stack.sh against the active kubeconfig, then asserts:
  1. Helm releases are "deployed"
  2. Web UI NodePort 30001 returns HTTP 200
  3. Backend /api/devices returns a JSON array
  4. Second run is idempotent

Usage:
  e2e_install.sh [--kubeconfig <path>] [--kind-mode] [--keep] [--timeout <secs>]

Options:
  --kubeconfig <path>   Kubeconfig path (default: $KUBECONFIG or ~/.kube/config)
  --cluster-name <n>    Kind cluster name for IP derivation (default: kcloud-e2e)
  --kind-mode           Force kind-safe flags (--skip-operators, local-path storage)
  --app-namespace <ns>  Install into this namespace (default: llm-evaluation-e2e)
  --keep                Keep installed releases after test
  --timeout <secs>      Per-rollout timeout (default: 300)

What kind CANNOT test (only validated on the real cluster):
  - GPU/NPU operators and device scheduling
  - NFS/RWX storage (local-path used instead)
  - kubespray provisioning
  - Real NodePort accessibility from outside the host
USAGE
      exit 0
      ;;
    *)
      log_error "Unknown argument: $1"
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------
if ! command -v kubectl &>/dev/null; then
  log_error "kubectl not found in PATH — cannot run E2E tests"
  exit 3
fi

if ! command -v helm &>/dev/null; then
  log_error "helm not found in PATH — cannot run E2E tests"
  exit 3
fi

if ! command -v curl &>/dev/null; then
  log_error "curl not found in PATH — cannot check web UI / API endpoints"
  exit 3
fi

if [[ ! -f "$INSTALLER" ]]; then
  log_error "Installer not found: ${INSTALLER}"
  log_error "Expected scripts/install_kcloud_stack.sh to exist. Run from the repo root."
  exit 1
fi

# ---------------------------------------------------------------------------
# Export kubeconfig
# ---------------------------------------------------------------------------
if [[ -f "$KUBECONFIG_PATH" ]]; then
  export KUBECONFIG="$KUBECONFIG_PATH"
  log_info "Using kubeconfig: ${KUBECONFIG_PATH}"
else
  log_warn "Kubeconfig not found at ${KUBECONFIG_PATH} — relying on in-cluster / default"
fi

# ---------------------------------------------------------------------------
# Verify cluster is reachable
# ---------------------------------------------------------------------------
if ! kubectl cluster-info &>/dev/null; then
  log_error "Cluster not reachable with kubeconfig: ${KUBECONFIG_PATH}"
  log_error "Check that the cluster is running and KUBECONFIG is set correctly."
  exit 1
fi
log_info "Cluster is reachable"

# ---------------------------------------------------------------------------
# Auto-detect kind mode
# ---------------------------------------------------------------------------
if [[ -z "$KIND_MODE" ]]; then
  # Detect kind cluster by context name or node provider label
  local_ctx="$(kubectl config current-context 2>/dev/null || echo "")"
  if [[ "$local_ctx" == *"kind"* ]] || [[ "$local_ctx" == *"${CLUSTER_NAME}"* ]]; then
    KIND_MODE="true"
    log_info "Auto-detected kind cluster (context: ${local_ctx}) — enabling kind-safe flags"
  else
    KIND_MODE="false"
    log_info "Non-kind cluster detected (context: ${local_ctx})"
  fi
fi

# ---------------------------------------------------------------------------
# Derive node IPs and access IP
# ---------------------------------------------------------------------------
log_step "Resolving node IPs from cluster..."
NODE_IPS_RAW="$(kubectl get nodes \
  -o jsonpath='{range .items[*]}{.status.addresses[?(@.type=="InternalIP")].address}{","}{end}' \
  2>/dev/null | sed 's/,$//')"

if [[ -z "$NODE_IPS_RAW" ]]; then
  log_error "Could not retrieve node InternalIPs from cluster"
  exit 1
fi
log_info "Node IPs: ${NODE_IPS_RAW}"

# Derive ACCESS_IP: first node IP (control-plane equivalent)
FIRST_IP="${NODE_IPS_RAW%%,*}"
ACCESS_IP="${FIRST_IP}"
log_info "Access IP (for NodePort reachability check): ${ACCESS_IP}"

# ---------------------------------------------------------------------------
# Build installer flags
# ---------------------------------------------------------------------------
INSTALLER_FLAGS=(
  "--node-ips" "${NODE_IPS_RAW}"
  "--access-ip" "${ACCESS_IP}"
  "--app-namespace" "${APP_NAMESPACE}"
  "--timeout" "${TIMEOUT}"
  "--skip-benchmarks"
)

if [[ "$KIND_MODE" == "true" ]]; then
  log_info "Kind-safe flags: --skip-operators (no GPU/NPU) + --skip-observability (resource limits)"
  log_warn "kind CANNOT test: GPU/NPU operators, NFS/RWX storage, kubespray, external NodePort"
  INSTALLER_FLAGS+=(
    "--skip-operators"
    "--skip-observability"
    "--storage-class" "standard"
  )
fi

# ---------------------------------------------------------------------------
# Cleanup on exit (unless --keep)
# ---------------------------------------------------------------------------
_cleanup_releases() {
  if [[ "$KEEP" == "true" ]]; then
    log_info "--keep specified; leaving releases installed"
    return 0
  fi
  log_step "Cleaning up installed releases (post-test teardown)..."
  # Remove the app-chart release if installed
  if helm status app-chart -n "${APP_NAMESPACE}" &>/dev/null 2>&1; then
    helm uninstall app-chart -n "${APP_NAMESPACE}" --wait --timeout 120s 2>/dev/null || \
      log_warn "helm uninstall app-chart failed (may already be gone)"
  fi
  # Remove storage release if installed by this test
  if helm status nfs-subdir -n nfs-provisioner &>/dev/null 2>&1; then
    helm uninstall nfs-subdir -n nfs-provisioner --wait --timeout 60s 2>/dev/null || \
      log_warn "helm uninstall nfs-subdir failed"
  fi
  log_info "Cleanup complete"
}
trap '_cleanup_releases' EXIT

# ---------------------------------------------------------------------------
# Assertion helpers
# ---------------------------------------------------------------------------
ASSERT_PASS=0
ASSERT_FAIL=0

_assert_pass() { log_info "[PASS] $*"; ASSERT_PASS=$((ASSERT_PASS + 1)); }
_assert_fail() { log_error "[FAIL] $*"; ASSERT_FAIL=$((ASSERT_FAIL + 1)); }

# Assert a helm release is in "deployed" state
_assert_helm_deployed() {
  local release="$1" ns="$2"
  local status
  status=$(helm status "${release}" -n "${ns}" -o json 2>/dev/null | \
    python3 -c "import json,sys; print(json.load(sys.stdin).get('info',{}).get('status',''))" \
    2>/dev/null || echo "")
  if [[ "$status" == "deployed" ]]; then
    _assert_pass "helm release '${release}' in namespace '${ns}' is deployed"
  else
    _assert_fail "helm release '${release}' in namespace '${ns}' status='${status}' (expected 'deployed')"
  fi
}

# Assert HTTP endpoint returns expected status code
_assert_http() {
  local url="$1" expected_code="$2" label="$3"
  local actual_code
  actual_code=$(curl -s -o /dev/null -w '%{http_code}' \
    --connect-timeout 10 --max-time 20 "${url}" 2>/dev/null || echo "000")
  if [[ "$actual_code" == "$expected_code" ]]; then
    _assert_pass "${label} → HTTP ${actual_code}"
  else
    _assert_fail "${label} → HTTP ${actual_code} (expected ${expected_code}) — URL: ${url}"
  fi
}

# Assert curl output contains valid JSON array
_assert_json_array() {
  local url="$1" label="$2"
  local body
  body=$(curl -s --connect-timeout 10 --max-time 20 "${url}" 2>/dev/null || echo "")
  if echo "$body" | python3 -c "import json,sys; v=json.load(sys.stdin); assert isinstance(v,list)" &>/dev/null 2>&1; then
    _assert_pass "${label} → valid JSON array"
  else
    _assert_fail "${label} → not a JSON array. Body: ${body:0:200}"
  fi
}

# ---------------------------------------------------------------------------
# STEP 1: First install run
# ---------------------------------------------------------------------------
log_step "=== STEP 1: First install run ==="
log_info "Running: ${INSTALLER} ${INSTALLER_FLAGS[*]}"

if ! "${INSTALLER}" "${INSTALLER_FLAGS[@]}"; then
  log_error "First install run FAILED"
  exit 1
fi
log_step "First install run completed."

# ---------------------------------------------------------------------------
# STEP 2: Assert helm releases are deployed
# ---------------------------------------------------------------------------
log_step "=== STEP 2: Assert helm release state ==="

# app-chart is the primary release from the webapp stage
_assert_helm_deployed "app-chart" "${APP_NAMESPACE}"

# NFS provisioner (or local-path fallback in kind mode — skip if not installed by us)
if helm status nfs-subdir -n nfs-provisioner &>/dev/null 2>&1; then
  _assert_helm_deployed "nfs-subdir" "nfs-provisioner"
fi

# ---------------------------------------------------------------------------
# STEP 3: Wait for deployments to be ready
# ---------------------------------------------------------------------------
log_step "=== STEP 3: Wait for deployments in ${APP_NAMESPACE} ==="
log_info "Waiting up to ${TIMEOUT}s for all deployments to be available..."

DEPLOY_WAIT_DEADLINE=$(( $(date +%s) + TIMEOUT ))
ALL_READY=false

while [[ $(date +%s) -lt $DEPLOY_WAIT_DEADLINE ]]; do
  total=$(kubectl get deployments -n "${APP_NAMESPACE}" \
    --no-headers 2>/dev/null | wc -l || echo 0)
  ready=$(kubectl get deployments -n "${APP_NAMESPACE}" \
    --no-headers 2>/dev/null | \
    awk '$2 == $3 && $2 != "0/0" && $2 != "0"' | wc -l || echo 0)
  if [[ "$total" -gt 0 && "$ready" -ge "$total" ]]; then
    ALL_READY=true
    break
  fi
  log_info "Deployments ready: ${ready}/${total} — waiting..."
  sleep 10
done

if [[ "$ALL_READY" == "true" ]]; then
  _assert_pass "All deployments in ${APP_NAMESPACE} are available"
else
  remaining=$(( DEPLOY_WAIT_DEADLINE - $(date +%s) ))
  _assert_fail "Not all deployments ready after ${TIMEOUT}s (${remaining}s remaining)"
  log_warn "Deployment status:"
  kubectl get deployments -n "${APP_NAMESPACE}" 2>/dev/null || true
fi

# ---------------------------------------------------------------------------
# STEP 4: Assert web UI reachable (NodePort 30001)
# ---------------------------------------------------------------------------
log_step "=== STEP 4: Assert web UI reachable ==="

FRONTEND_URL="http://${ACCESS_IP}:30001"
BACKEND_API_URL="http://${ACCESS_IP}:30980/api/devices"

if [[ "$KIND_MODE" == "true" ]]; then
  # In kind, NodePort is only reachable on the node's container IP, not necessarily
  # the reported InternalIP. Use the kind node's container IP if available.
  KIND_NODE_IP=$(kubectl get nodes \
    -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' \
    2>/dev/null || echo "${ACCESS_IP}")
  FRONTEND_URL="http://${KIND_NODE_IP}:30001"
  BACKEND_API_URL="http://${KIND_NODE_IP}:30980/api/devices"
  log_info "Kind mode: NodePort URLs use node IP ${KIND_NODE_IP}"
  log_warn "kind NodePort reachability is host-local only (not accessible from external browsers)"
fi

# Allow some time for pods to settle before probing
log_info "Waiting 15s for services to settle before endpoint checks..."
sleep 15

_assert_http "${FRONTEND_URL}" "200" "Web UI (${FRONTEND_URL})"
_assert_http "${BACKEND_API_URL}" "200" "Backend API (${BACKEND_API_URL})"
_assert_json_array "${BACKEND_API_URL}" "Backend /api/devices JSON array"

# ---------------------------------------------------------------------------
# STEP 5: Second run — assert idempotency
# ---------------------------------------------------------------------------
log_step "=== STEP 5: Second run — idempotency check ==="
log_info "Re-running installer; expecting zero errors and converged state."

SECOND_RUN_OUTPUT=$(mktemp /tmp/kcloud-e2e-run2-XXXXXX.log)
trap 'rm -f "${SECOND_RUN_OUTPUT}"; _cleanup_releases' EXIT

if "${INSTALLER}" "${INSTALLER_FLAGS[@]}" >"${SECOND_RUN_OUTPUT}" 2>&1; then
  _assert_pass "Second install run succeeded (idempotent)"
else
  _assert_fail "Second install run FAILED — see ${SECOND_RUN_OUTPUT}"
  log_error "Second run output (last 40 lines):"
  tail -40 "${SECOND_RUN_OUTPUT}" >&2
fi

# Ensure releases are still deployed after second run
_assert_helm_deployed "app-chart" "${APP_NAMESPACE}"

rm -f "${SECOND_RUN_OUTPUT}"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
log_step "=== E2E Test Summary ==="
log_info "PASS: ${ASSERT_PASS}"
log_info "FAIL: ${ASSERT_FAIL}"
log_info ""
if [[ "$KIND_MODE" == "true" ]]; then
  log_warn "=== kind limitations (not tested here — real cluster required) ==="
  log_warn "  GPU/NPU device operators and scheduling"
  log_warn "  NFS/RWX persistent storage (local-path RWO used in this run)"
  log_warn "  kubespray bare-node provisioning"
  log_warn "  External NodePort accessibility from a browser"
fi

if [[ "$ASSERT_FAIL" -gt 0 ]]; then
  log_error "${ASSERT_FAIL} assertion(s) FAILED"
  exit 1
fi

log_step "All ${ASSERT_PASS} assertions PASSED"
exit 0
