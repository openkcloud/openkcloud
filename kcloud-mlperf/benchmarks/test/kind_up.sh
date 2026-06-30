#!/usr/bin/env bash
# test/kind_up.sh — create an ephemeral kind cluster for kcloud-tool E2E tests.
#
# Creates 1 control-plane + 2 workers (mirrors the 3-node pilot cluster layout).
# Uses rootless nerdctl via KIND_EXPERIMENTAL_PROVIDER=nerdctl (no docker required).
#
# Usage:
#   ./test/kind_up.sh [--name <cluster-name>]
#
# Exit codes:
#   0  — cluster created (or already exists with the same name)
#   3  — kind binary or a working container runtime is absent (actionable skip)
#   1  — unexpected error

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
      echo "Usage: kind_up.sh [--name <cluster-name>]"
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
# Prerequisite checks — exit 3 (SKIP) with actionable message if absent
# ---------------------------------------------------------------------------
_skip() {
  log_warn "=== SKIP: kind E2E tests cannot run on this host ==="
  log_warn "$*"
  log_warn ""
  log_warn "To enable kind testing, install the missing tools:"
  log_warn "  kind:     https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
  log_warn "            curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64"
  log_warn "            chmod +x /usr/local/bin/kind"
  log_warn "  nerdctl:  https://github.com/containerd/nerdctl/releases"
  log_warn "            (requires rootless containerd; see 'containerd-rootless-setuptool.sh install')"
  log_warn "  docker:   https://docs.docker.com/engine/install/"
  log_warn ""
  log_warn "NOTE: GPU/NPU operators, kubespray provisioning, and RWX-NFS storage"
  log_warn "cannot be tested with kind. Only orchestration, helm idempotency,"
  log_warn "webapp reachability, and verify logic are exercised here."
  exit 3
}

# Check kind
if ! command -v kind &>/dev/null; then
  _skip "kind binary not found in PATH."
fi

# Check container runtime: prefer nerdctl (rootless), fall back to docker
RUNTIME=""
if command -v nerdctl &>/dev/null; then
  # Verify nerdctl is actually functional (rootless or with containerd socket)
  if nerdctl info &>/dev/null 2>&1; then
    RUNTIME="nerdctl"
  else
    log_warn "nerdctl found but 'nerdctl info' failed — trying docker fallback"
  fi
fi

if [[ -z "$RUNTIME" ]] && command -v docker &>/dev/null; then
  if docker info &>/dev/null 2>&1; then
    RUNTIME="docker"
  else
    log_warn "docker found but 'docker info' failed (daemon not running?)"
  fi
fi

if [[ -z "$RUNTIME" ]]; then
  _skip "No working container runtime found. nerdctl info and docker info both failed."
fi

log_info "Container runtime: ${RUNTIME}"

# Export nerdctl provider for kind when using nerdctl
if [[ "$RUNTIME" == "nerdctl" ]]; then
  export KIND_EXPERIMENTAL_PROVIDER=nerdctl
  log_info "KIND_EXPERIMENTAL_PROVIDER=nerdctl"
fi

# ---------------------------------------------------------------------------
# Check if cluster already exists (idempotent)
# ---------------------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  log_warn "Kind cluster '${CLUSTER_NAME}' already exists — skipping create (idempotent)"
  log_info "To recreate: run kind_down.sh --name ${CLUSTER_NAME} first"
  # Ensure kubeconfig is exported
  kind export kubeconfig --name "${CLUSTER_NAME}" 2>/dev/null || true
  exit 0
fi

# ---------------------------------------------------------------------------
# Kind cluster config: 1 control-plane + 2 workers
# ---------------------------------------------------------------------------
KIND_CONFIG=$(mktemp /tmp/kcloud-kind-config-XXXXXX.yaml)
trap 'rm -f "${KIND_CONFIG}"' EXIT

cat > "${KIND_CONFIG}" <<EOF
# kind cluster config for kcloud-tool E2E tests
# Mirrors a 3-node pilot cluster: 1 control-plane + 2 workers
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
  - role: worker
# NOTE: kind does NOT provide:
#   - GPU/NPU hardware resources
#   - NFS / RWX storage (local-path only, RWO)
#   - kubespray provisioning paths
#   - Real NodePort reachability outside the host network
# These are validated on the real 3-node test cluster only.
EOF

log_step "Creating kind cluster '${CLUSTER_NAME}' (1 control-plane + 2 workers)..."
log_info "This may take 1-3 minutes on first run (image pull)."

kind create cluster \
  --name "${CLUSTER_NAME}" \
  --config "${KIND_CONFIG}" \
  --wait 120s

log_step "Kind cluster '${CLUSTER_NAME}' is ready."
log_info "KUBECONFIG updated; active context: $(kubectl config current-context)"
log_info ""
log_info "Cluster nodes:"
kubectl get nodes -o wide

log_info ""
log_warn "=== kind limitations (honest accounting) ==="
log_warn "  - No GPU/NPU resources: --skip-operators required"
log_warn "  - No NFS/RWX storage: local-path StorageClass only (RWO)"
log_warn "  - NodePort not reachable from external browser (host-local only)"
log_warn "  - kubespray --provision path not exercised"
log_warn "  - Observability (loki/prometheus) may need --skip-observability for resource limits"
log_warn "Final validation on the real 3-node cluster covers GPU/NPU/NFS/kubespray paths."
