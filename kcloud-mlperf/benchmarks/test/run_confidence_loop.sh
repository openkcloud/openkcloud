#!/usr/bin/env bash
# test/run_confidence_loop.sh — repeatability proof for the kcloud-tool installer.
#
# Runs kind_up -> e2e_install -> kind_down N times, logs PASS/FAIL per iteration
# to test/.results/, prints a summary table, exits non-zero if any iteration failed.
#
# Usage:
#   ./test/run_confidence_loop.sh <N> [OPTIONS]
#
# Arguments:
#   N                     Number of iterations (required, integer >= 1)
#
# Options:
#   --name <cluster>      Kind cluster name (default: kcloud-e2e-loop)
#   --keep-on-fail        Do not run kind_down on a failed iteration (aids debugging)
#   --e2e-args <args>     Extra args forwarded to e2e_install.sh (quoted string)
#   --results-dir <path>  Directory for per-iteration logs (default: test/.results/)
#   -h|--help             Show this help and exit 0
#
# Exit codes:
#   0  — all N iterations PASSED
#   1  — one or more iterations FAILED
#   3  — prerequisites missing (kind absent) — propagated from kind_up.sh

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
LIB_DIR="${REPO_ROOT}/scripts/lib"

# Source log helpers
# shellcheck source=scripts/lib/log.sh
source "${LIB_DIR}/log.sh"

# ERR trap — do not use set -e in the main loop; we track failures manually.
# Unexpected errors outside the per-iteration block should still surface.
trap '_on_err $? "$BASH_COMMAND" ${BASH_LINENO[0]}' ERR
_on_err() {
  local ec="$1" cmd="$2" line="$3"
  # Only fatal for errors outside the per-iteration try/catch block
  log_error "Unexpected error at line ${line}: ${cmd} (exit ${ec})"
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
N=""
CLUSTER_NAME="kcloud-e2e-loop"
KEEP_ON_FAIL=false
E2E_EXTRA_ARGS=""
RESULTS_DIR="${SCRIPT_DIR}/.results"

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------
if [[ $# -eq 0 ]]; then
  log_error "Usage: run_confidence_loop.sh <N> [OPTIONS]"
  log_error "  N = number of iterations (integer >= 1)"
  exit 1
fi

# First positional arg is N if it looks like a number
if [[ "$1" =~ ^[0-9]+$ ]]; then
  N="$1"
  shift
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name)           CLUSTER_NAME="$2";     shift 2 ;;
    --keep-on-fail)   KEEP_ON_FAIL=true;     shift ;;
    --e2e-args)       E2E_EXTRA_ARGS="$2";   shift 2 ;;
    --results-dir)    RESULTS_DIR="$2";      shift 2 ;;
    -h|--help)
      cat <<'USAGE'
run_confidence_loop.sh <N> [OPTIONS] — repeatability proof for kcloud-tool

Runs kind_up -> e2e_install -> kind_down N times.
Each iteration is independent; results are logged to test/.results/.

Arguments:
  N                    Number of iterations (integer >= 1)

Options:
  --name <cluster>     Kind cluster name (default: kcloud-e2e-loop)
  --keep-on-fail       Skip kind_down on failure (aids debugging)
  --e2e-args <args>    Extra args for e2e_install.sh (quoted string)
  --results-dir <dir>  Log output directory (default: test/.results/)

What kind CANNOT test (only exercised on the real 3-node cluster):
  - GPU/NPU operators and device scheduling
  - NFS/RWX persistent storage
  - kubespray bare-node provisioning
  - NodePort accessibility from external browsers
USAGE
      exit 0
      ;;
    *)
      log_error "Unknown argument: $1"
      exit 1
      ;;
  esac
done

if [[ -z "$N" ]]; then
  log_error "N (number of iterations) is required and must be a positive integer"
  log_error "Usage: run_confidence_loop.sh <N> [OPTIONS]"
  exit 1
fi

if ! [[ "$N" =~ ^[0-9]+$ ]] || [[ "$N" -lt 1 ]]; then
  log_error "N must be a positive integer (got: ${N})"
  exit 1
fi

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------
if ! command -v kind &>/dev/null; then
  log_warn "=== SKIP: kind binary not found ==="
  log_warn "Install kind to run the confidence loop: https://kind.sigs.k8s.io"
  log_warn "  curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64"
  log_warn "  chmod +x /usr/local/bin/kind"
  exit 3
fi

for script in kind_up.sh kind_down.sh e2e_install.sh; do
  if [[ ! -f "${SCRIPT_DIR}/${script}" ]]; then
    log_error "Required test script not found: ${SCRIPT_DIR}/${script}"
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# Setup results directory
# ---------------------------------------------------------------------------
mkdir -p "${RESULTS_DIR}"
RUN_TS="$(date '+%Y%m%dT%H%M%S')"
SUMMARY_FILE="${RESULTS_DIR}/summary_${RUN_TS}.txt"

log_step "=== kcloud-tool confidence loop: N=${N} iterations ==="
log_info "Cluster name:  ${CLUSTER_NAME}"
log_info "Results dir:   ${RESULTS_DIR}"
log_info "Summary file:  ${SUMMARY_FILE}"
log_info ""
log_warn "=== kind limitations (none of these paths exercised here) ==="
log_warn "  GPU/NPU operators, NFS/RWX storage, kubespray, external NodePort"
log_warn "Final acceptance requires running e2e_install.sh on the real 3-node cluster."
log_info ""

# ---------------------------------------------------------------------------
# Per-iteration tracking
# ---------------------------------------------------------------------------
PASS_COUNT=0
FAIL_COUNT=0
declare -a ITER_RESULTS=()

# ---------------------------------------------------------------------------
# Main loop
# ---------------------------------------------------------------------------
for (( i=1; i<=N; i++ )); do
  ITER_LOG="${RESULTS_DIR}/iter_${i}_${RUN_TS}.log"
  ITER_LABEL="iteration ${i}/${N}"

  log_step "--- ${ITER_LABEL} ---"
  log_info "Log: ${ITER_LOG}"

  ITER_STATUS="PASS"
  ITER_FAIL_STAGE=""

  # ---- kind_up ----
  log_info "[${ITER_LABEL}] kind_up --name ${CLUSTER_NAME}"
  if ! "${SCRIPT_DIR}/kind_up.sh" --name "${CLUSTER_NAME}" \
      >> "${ITER_LOG}" 2>&1; then
    UP_EXIT=$?
    if [[ "$UP_EXIT" -eq 3 ]]; then
      log_warn "kind_up exited with SKIP (exit 3) — no runtime available"
      ITER_RESULTS+=("iter ${i}: SKIP (no kind runtime)")
      log_warn "Cannot continue confidence loop without kind. Exiting."
      exit 3
    fi
    ITER_STATUS="FAIL"
    ITER_FAIL_STAGE="kind_up"
    log_error "[${ITER_LABEL}] kind_up FAILED (see ${ITER_LOG})"
  fi

  # ---- e2e_install (only if kind_up succeeded) ----
  if [[ "$ITER_STATUS" == "PASS" ]]; then
    log_info "[${ITER_LABEL}] e2e_install.sh --cluster-name ${CLUSTER_NAME}"

    # Build e2e args array
    E2E_ARGS=("--cluster-name" "${CLUSTER_NAME}" "--kind-mode")
    if [[ -n "$E2E_EXTRA_ARGS" ]]; then
      # Word-split intentionally: user passes a quoted string of flags
      # shellcheck disable=SC2206
      E2E_ARGS+=($E2E_EXTRA_ARGS)
    fi

    if ! "${SCRIPT_DIR}/e2e_install.sh" "${E2E_ARGS[@]}" \
        >> "${ITER_LOG}" 2>&1; then
      ITER_STATUS="FAIL"
      ITER_FAIL_STAGE="e2e_install"
      log_error "[${ITER_LABEL}] e2e_install FAILED (see ${ITER_LOG})"
    fi
  fi

  # ---- kind_down (always, unless --keep-on-fail and we failed) ----
  if [[ "$ITER_STATUS" == "FAIL" && "$KEEP_ON_FAIL" == "true" ]]; then
    log_warn "[${ITER_LABEL}] --keep-on-fail: skipping kind_down for debugging"
    log_warn "  Cluster '${CLUSTER_NAME}' is still running."
    log_warn "  Manually tear down with: test/kind_down.sh --name ${CLUSTER_NAME}"
  else
    log_info "[${ITER_LABEL}] kind_down --name ${CLUSTER_NAME}"
    if ! "${SCRIPT_DIR}/kind_down.sh" --name "${CLUSTER_NAME}" \
        >> "${ITER_LOG}" 2>&1; then
      # kind_down failure is logged but does not override a passing iteration status
      log_warn "[${ITER_LABEL}] kind_down failed (cluster may still exist)"
      if [[ "$ITER_STATUS" == "PASS" ]]; then
        ITER_STATUS="FAIL"
        ITER_FAIL_STAGE="kind_down"
      fi
    fi
  fi

  # ---- Record result ----
  if [[ "$ITER_STATUS" == "PASS" ]]; then
    ITER_RESULTS+=("iter ${i}: PASS")
    PASS_COUNT=$((PASS_COUNT + 1))
    log_step "[${ITER_LABEL}] PASS"
  else
    ITER_RESULTS+=("iter ${i}: FAIL (stage: ${ITER_FAIL_STAGE}) — log: ${ITER_LOG}")
    FAIL_COUNT=$((FAIL_COUNT + 1))
    log_error "[${ITER_LABEL}] FAIL (stage: ${ITER_FAIL_STAGE})"
    log_error "  Tail of log (${ITER_LOG}):"
    tail -20 "${ITER_LOG}" >&2 || true
  fi

  log_info ""
done

# ---------------------------------------------------------------------------
# Summary table
# ---------------------------------------------------------------------------
{
  echo "===== kcloud-tool confidence loop summary ====="
  echo "Run timestamp: ${RUN_TS}"
  echo "Iterations:    ${N}"
  echo "PASS:          ${PASS_COUNT}"
  echo "FAIL:          ${FAIL_COUNT}"
  echo ""
  echo "Per-iteration results:"
  for r in "${ITER_RESULTS[@]}"; do
    echo "  ${r}"
  done
  echo ""
  echo "kind limitations (NOT exercised in this loop):"
  echo "  - GPU/NPU operators and device scheduling"
  echo "  - NFS/RWX storage (local-path RWO used)"
  echo "  - kubespray bare-node provisioning"
  echo "  - External NodePort accessibility"
  echo "Covered by this loop:"
  echo "  - Install orchestration + stage ordering"
  echo "  - Namespace + helm idempotency"
  echo "  - Webapp helm release deploy status"
  echo "  - NodePort reachability (host-local)"
  echo "  - Backend /api/devices JSON response"
  echo "  - Second-run convergence (no diff/errors)"
  echo "  - Clean teardown"
  echo "================================================"
} | tee "${SUMMARY_FILE}"

log_step "Summary written to: ${SUMMARY_FILE}"
log_info ""

if [[ "$FAIL_COUNT" -gt 0 ]]; then
  log_error "${FAIL_COUNT}/${N} iteration(s) FAILED"
  exit 1
fi

log_step "All ${N}/${N} iterations PASSED"
exit 0
