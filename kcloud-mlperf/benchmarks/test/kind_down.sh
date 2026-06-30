#!/usr/bin/env bash
# test/kind_down.sh — delete the ephemeral kind cluster created by kind_up.sh.
#
# Trap-guarded and idempotent: succeeds silently when the cluster does not exist.
#
# Usage:
#   ./test/kind_down.sh [--name <cluster-name>]

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_DIR="${SCRIPT_DIR}/../scripts/lib"

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
CLUSTER_NAME="kcloud-e2e"

# ---------------------------------------------------------------------------
# Parse args
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --name) CLUSTER_NAME="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: kind_down.sh [--name <cluster-name>]"
      echo "  --name   Kind cluster name (default: kcloud-e2e)"
      exit 0
      ;;
    *)
      log_error "Unknown argument: $1"
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# kind must be present; if not, nothing to tear down — exit clean
# ---------------------------------------------------------------------------
if ! command -v kind &>/dev/null; then
  log_warn "kind binary not found — no cluster to delete"
  exit 0
fi

# Set nerdctl provider if nerdctl is the available runtime
if command -v nerdctl &>/dev/null && nerdctl info &>/dev/null 2>&1; then
  export KIND_EXPERIMENTAL_PROVIDER=nerdctl
  log_info "KIND_EXPERIMENTAL_PROVIDER=nerdctl"
elif command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
  : # docker is the default; no env var needed
fi

# ---------------------------------------------------------------------------
# Idempotent delete — succeed silently if cluster absent
# ---------------------------------------------------------------------------
if ! kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  log_info "Kind cluster '${CLUSTER_NAME}' does not exist — nothing to do (idempotent)"
  exit 0
fi

log_step "Deleting kind cluster '${CLUSTER_NAME}'..."
kind delete cluster --name "${CLUSTER_NAME}"
log_step "Kind cluster '${CLUSTER_NAME}' deleted."
