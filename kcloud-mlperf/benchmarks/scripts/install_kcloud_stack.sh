#!/usr/bin/env bash
# scripts/install_kcloud_stack.sh — kcloud-tool FULL-STACK one-input K8s installer
#
# A SINGLE command whose ONLY required input is the node IP list, bringing up the
# entire ETRI LLM evaluation platform on a fresh cluster and verifying it serves:
#   preflight → provision → storage → operators → observability → webapp → benchmarks → verify
#
# One-command happy path (only manual input = node IPs):
#   ./scripts/install_kcloud_stack.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
#
# See --help for the full flag reference. This installer REUSES install_pilot_k8s.sh
# as the benchmark stage and drives the vendored Helm charts under --platform-dir
# (working on COPIES under deploy/render/<run>/ — it NEVER edits the upstream tree).

set -Eeuo pipefail

# ---------------------------------------------------------------------------
# ERR trap — prints failing line + command + exit code (reuses log.sh once sourced)
# ---------------------------------------------------------------------------
trap '_on_err $? "$BASH_COMMAND" ${BASH_LINENO[0]}' ERR
_on_err() {
  local ec="$1" cmd="$2" line="$3"
  if type log_error &>/dev/null; then
    log_error "Command failed at line ${line}: ${cmd} (exit ${ec})"
  else
    echo "[ERROR] Command failed at line ${line}: ${cmd} (exit ${ec})" >&2
  fi
}

# ---------------------------------------------------------------------------
# Resolve paths
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_DIR="${SCRIPT_DIR}/lib"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEPLOY_PLATFORM_DIR="${REPO_ROOT}/deploy/platform"

# Source logging helpers first — needed by detect.sh + stages.sh
# shellcheck source=scripts/lib/log.sh
source "${LIB_DIR}/log.sh"
# shellcheck source=scripts/lib/detect.sh
source "${LIB_DIR}/detect.sh"
# shellcheck source=scripts/lib/stages.sh
source "${LIB_DIR}/stages.sh"

# ---------------------------------------------------------------------------
# Defaults — FROZEN render variables + CLI options
# ---------------------------------------------------------------------------
NODE_IPS=""
ACCESS_IP=""
NFS_SERVER=""
CONTROL_PLANE_IP=""
APP_NAMESPACE="llm-evaluation"
BENCH_NAMESPACE="kcloud-mlperf"
PLATFORM_DIR="/home/kcloud/etri-llm-deployments/app/kubernetes"
DEVICE_ARG="auto"
SSH_PORT_CP="122"
SSH_PORT_NPU="22"
# Provision SSH port (kubespray ansible_port for ALL target nodes). Defaults to
# whatever --ssh-port resolved to (SSH_PORT_CP); set explicitly by --ssh-port.
SSH_PORT_PROVISION="22"
NFS_PATH="/nfs-storage"
FRONTEND_NODEPORT="30001"
BACKEND_NODEPORT="30980"
MANAGED_BY="kcloud-tool"
PART_OF="kcloud-stack"
TIMEOUT="600"

ONLY_STAGE=""
OPT_PROVISION=false
OPT_SKIP_OBSERVABILITY=false
OPT_SKIP_WEBAPP=false
OPT_SKIP_BENCHMARKS=false
OPT_SKIP_OPERATORS=false
OPT_DRY_RUN=false
OPT_VALIDATE_ONLY=false
OPT_CLEANUP=false
OPT_FORCE=false

# State populated during run
DEVICE_MODE=""
RENDER_DIR=""
CLUSTER_OFFLINE=false

# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
usage() {
  cat <<'USAGE'
kcloud-tool Full-Stack Kubernetes Installer
===========================================
Brings up the ENTIRE ETRI LLM evaluation platform from a single command and
verifies the web UI is serving:
  storage -> device operators -> observability -> web app -> benchmarks -> verify
Only required input: --node-ips.  Everything else is auto-detected or defaulted.

Usage:
  install_kcloud_stack.sh --node-ips "<ip1,ip2,...>" [OPTIONS]

Required (except --help):
  --node-ips <csv>           Comma-separated node InternalIPs. First = control-plane.

Addressing / naming:
  --access-ip <ip>           Browser-facing IP for the frontend->backend URL  (default: control-plane)
  --nfs-server <ip>          NFS server for RWX storage                       (default: control-plane)
  --app-namespace <ns>       Web app namespace                                (default: llm-evaluation)
  --bench-namespace <ns>     Benchmark namespace                              (default: kcloud-mlperf)
  --platform-dir <path>      Source of vendored charts / stage scripts
                             (default: /home/kcloud/etri-llm-deployments/app/kubernetes)

Provisioning (bare nodes):
  --provision                Run kubespray first. Auto-skipped if cluster already healthy.
                             Needs SSH (port from --ssh-port) + sudo (SSHPASS/prompt).
  --ssh-port <n>             Control-plane/GPU SSH port      (default: 122; NPU nodes use 22)

Device / model:
  --device <mode>            auto|gpu|npu-rngd|npu-atom|cpu  (default: auto; drives which operators install)

Stage selection / skips:
  --only <stage>             Run exactly one stage:
                             preflight|provision|storage|operators|observability|webapp|benchmarks|verify
  --skip-observability       Skip loki/prometheus/alloy
  --skip-webapp              Skip the app-chart (platform infra + benchmarks only)
  --skip-benchmarks          Skip the benchmark layer
  --skip-operators           Skip device operators (CPU-only / kind testing)
  --timeout <secs>           Per-rollout wait                                 (default: 600)

Execution modes (never mutate the cluster):
  --dry-run                  Render + 'helm template' + 'kubectl apply --dry-run=client'; mutate NOTHING.
                             Works offline (degrades to a printed plan).
  --validate-only            Read-only preflight: cluster reachable, nodes match, tooling, charts,
                             node-IP substitution resolves. Exit 0 = ready.
  --cleanup                  Remove everything THIS installer created (helm uninstall + delete
                             labeled namespaces). Requires --force for non-empty/unlabeled.

Safety:
  --force                    Allow potentially-overwriting / destructive-by-design actions.
  -h, --help                 Show this help and exit 0 (NEVER touches the cluster).

Examples:
  # Minimal one-command full-stack install
  ./install_kcloud_stack.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"

  # Dry run — render + plan, no cluster mutations (works offline)
  ./install_kcloud_stack.sh --node-ips "192.0.2.11" --dry-run

  # Read-only readiness check
  ./install_kcloud_stack.sh --node-ips "192.0.2.11" --validate-only

  # Only re-run verification
  ./install_kcloud_stack.sh --node-ips "192.0.2.11" --only verify

  # CPU-only / kind: skip device operators
  ./install_kcloud_stack.sh --node-ips "192.0.2.11" --skip-operators

  # Tear down everything this installer created
  ./install_kcloud_stack.sh --node-ips "192.0.2.11" --cleanup --force
USAGE
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --node-ips)            NODE_IPS="$2";                shift 2 ;;
      --access-ip)           ACCESS_IP="$2";               shift 2 ;;
      --nfs-server)          NFS_SERVER="$2";              shift 2 ;;
      --app-namespace)       APP_NAMESPACE="$2";           shift 2 ;;
      --bench-namespace)     BENCH_NAMESPACE="$2";         shift 2 ;;
      --platform-dir)        PLATFORM_DIR="$2";            shift 2 ;;
      --provision)           OPT_PROVISION=true;           shift ;;
      --ssh-port)            SSH_PORT_CP="$2"; SSH_PORT_PROVISION="$2"; shift 2 ;;
      --device)              DEVICE_ARG="$2";              shift 2 ;;
      --only)                ONLY_STAGE="$2";              shift 2 ;;
      --skip-observability)  OPT_SKIP_OBSERVABILITY=true;  shift ;;
      --skip-webapp)         OPT_SKIP_WEBAPP=true;         shift ;;
      --skip-benchmarks)     OPT_SKIP_BENCHMARKS=true;     shift ;;
      --skip-operators)      OPT_SKIP_OPERATORS=true;      shift ;;
      --timeout)             TIMEOUT="$2";                 shift 2 ;;
      --dry-run)             OPT_DRY_RUN=true;             shift ;;
      --validate-only)       OPT_VALIDATE_ONLY=true;       shift ;;
      --cleanup)             OPT_CLEANUP=true;             shift ;;
      --force)               OPT_FORCE=true;               shift ;;
      -h|--help)
        usage
        exit 0
        ;;
      -*)
        log_error "Unknown flag: $1"
        echo "" >&2
        usage >&2
        exit 1
        ;;
      *)
        log_error "Unexpected argument: $1"
        exit 1
        ;;
    esac
  done
}

# ---------------------------------------------------------------------------
# _is_ipv4 <token> — strict pure-bash IPv4 validator (4 octets, each 0-255,
# no leading-zero ambiguity beyond a single "0"). Returns 0 if valid.
# This closes the YAML/INI/curl injection surface at the single entry point.
# ---------------------------------------------------------------------------
_is_ipv4() {
  local ip="${1:-}"
  # Reject anything with characters outside digits and dots up front
  # (kills newlines, spaces, $(...), YAML/INI metacharacters, etc.).
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  local -a o
  IFS=. read -r -a o <<< "$ip"
  [[ ${#o[@]} -eq 4 ]] || return 1
  local x
  for x in "${o[@]}"; do
    # Numeric already guaranteed by the regex; bound-check 0..255.
    (( 10#$x >= 0 && 10#$x <= 255 )) || return 1
  done
  return 0
}

# ---------------------------------------------------------------------------
# Argument validation + role/IP resolution from --node-ips
# ---------------------------------------------------------------------------
validate_args() {
  if [[ -z "$NODE_IPS" ]]; then
    log_error "--node-ips is required (comma-separated node InternalIPs; first = control-plane)"
    echo "" >&2
    usage >&2
    exit 1
  fi

  # STRICT IPv4 validation of every --node-ips token BEFORE any rendering.
  local -a _ip_tokens=()
  IFS=',' read -ra _ip_tokens <<< "$NODE_IPS"
  local _tok
  for _tok in "${_ip_tokens[@]}"; do
    _tok="${_tok// /}"
    [[ -z "$_tok" ]] && continue   # tolerate a trailing comma / empty token
    if ! _is_ipv4 "$_tok"; then
      log_error "--node-ips contains a non-IPv4 token: '${_tok}' (expected dotted IPv4, octets 0-255)"
      exit 1
    fi
  done

  # First IP = control-plane = default ACCESS_IP and default NFS_SERVER.
  local first_ip="${NODE_IPS%%,*}"
  first_ip="${first_ip// /}"
  if [[ -z "$first_ip" ]]; then
    log_error "Could not parse a control-plane IP from --node-ips '${NODE_IPS}'"
    exit 1
  fi
  CONTROL_PLANE_IP="$first_ip"
  [[ -z "$ACCESS_IP"  ]] && ACCESS_IP="$CONTROL_PLANE_IP"
  [[ -z "$NFS_SERVER" ]] && NFS_SERVER="$CONTROL_PLANE_IP"

  # Validate any operator-supplied --access-ip / --nfs-server too.
  if ! _is_ipv4 "$ACCESS_IP"; then
    log_error "--access-ip is not a valid IPv4 address: '${ACCESS_IP}'"
    exit 1
  fi
  if ! _is_ipv4 "$NFS_SERVER"; then
    log_error "--nfs-server is not a valid IPv4 address: '${NFS_SERVER}'"
    exit 1
  fi

  case "$DEVICE_ARG" in
    auto|gpu|npu-rngd|npu-atom|cpu) ;;
    *)
      log_error "--device must be one of: auto|gpu|npu-rngd|npu-atom|cpu (got: ${DEVICE_ARG})"
      exit 1 ;;
  esac

  # --provision unattended requires the sudo/SSH password in the environment.
  # Read it from SUDO_PASS (preferred) or SSHPASS — NEVER a hardcoded value, and
  # the value is never echoed/logged. We FAIL FAST (never prompt) so an
  # unattended run does not hang on kubespray's become prompt. The kubespray
  # inventory references it via {{ lookup('env','SUDO_PASS') }}, so we normalize
  # SSHPASS into SUDO_PASS when only the latter is provided.
  # Skipped under --dry-run / --validate-only (no real provisioning happens).
  if [[ "$OPT_PROVISION" == "true" \
        && "$OPT_DRY_RUN" != "true" \
        && "$OPT_VALIDATE_ONLY" != "true" ]]; then
    if [[ -z "${SUDO_PASS:-}" && -n "${SSHPASS:-}" ]]; then
      export SUDO_PASS="$SSHPASS"
    fi
    if [[ -z "${SUDO_PASS:-}" ]]; then
      log_error "--provision requires the sudo/SSH password in the environment."
      log_error "Set SUDO_PASS (or SSHPASS) and re-run, e.g.: SUDO_PASS=<pw> ... --provision"
      log_error "The password is never echoed, logged, or written as a literal — the kubespray inventory reads it via lookup('env','SUDO_PASS')."
      exit 1
    fi
  fi

  if [[ -n "$ONLY_STAGE" ]]; then
    case "$ONLY_STAGE" in
      preflight|provision|storage|operators|observability|webapp|benchmarks|verify) ;;
      *)
        log_error "--only must be one of: preflight|provision|storage|operators|observability|webapp|benchmarks|verify (got: ${ONLY_STAGE})"
        exit 1 ;;
    esac
  fi

  if ! [[ "$TIMEOUT" =~ ^[0-9]+$ ]] || [[ "$TIMEOUT" -lt 1 ]]; then
    log_error "--timeout must be a positive integer (got: ${TIMEOUT})"
    exit 1
  fi
  if ! [[ "$SSH_PORT_CP" =~ ^[0-9]+$ ]]; then
    log_error "--ssh-port must be a positive integer (got: ${SSH_PORT_CP})"
    exit 1
  fi

  if [[ ! -d "$PLATFORM_DIR" ]]; then
    if [[ "$OPT_DRY_RUN" == "true" || "$OPT_VALIDATE_ONLY" == "true" ]]; then
      log_warn "Platform dir not found: ${PLATFORM_DIR} (continuing — dry-run/validate may have limited coverage)"
    else
      log_error "Platform dir not found: ${PLATFORM_DIR}. Use --platform-dir to point at the vendored charts."
      exit 1
    fi
  fi
}

# ---------------------------------------------------------------------------
# Export the FROZEN render variables (consumed by stages.sh + envsubst).
# ---------------------------------------------------------------------------
export_render_vars() {
  export NODE_IPS CONTROL_PLANE_IP ACCESS_IP NFS_SERVER NFS_PATH
  export APP_NAMESPACE BENCH_NAMESPACE SSH_PORT_CP SSH_PORT_NPU SSH_PORT_PROVISION
  export FRONTEND_NODEPORT BACKEND_NODEPORT MANAGED_BY PART_OF
  # Resolved/runtime globals the stages read:
  export DEVICE_ARG DEVICE_MODE PLATFORM_DIR DEPLOY_PLATFORM_DIR SCRIPT_DIR REPO_ROOT
  export TIMEOUT RENDER_DIR OPT_DRY_RUN OPT_VALIDATE_ONLY OPT_FORCE CLUSTER_OFFLINE
  export OPT_PROVISION OPT_SKIP_OBSERVABILITY OPT_SKIP_WEBAPP OPT_SKIP_BENCHMARKS OPT_SKIP_OPERATORS
}

# ---------------------------------------------------------------------------
# Render dir — fixed name under --dry-run (reproducible diffs), timestamped otherwise.
# ---------------------------------------------------------------------------
setup_render_dir() {
  local base="${REPO_ROOT}/deploy/render"
  mkdir -p "$base"
  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    RENDER_DIR="${base}/dry-run"
    # Path-sanity guard before rm -rf (defends against a bad REPO_ROOT).
    if [[ -n "$REPO_ROOT" && "$RENDER_DIR" == "${REPO_ROOT}/deploy/render/"* ]]; then
      rm -rf "$RENDER_DIR"
    else
      log_error "Refusing rm -rf on an unexpected render dir: '${RENDER_DIR}'"
      exit 1
    fi
  else
    RENDER_DIR="${base}/$(date '+%Y%m%d-%H%M%S')"
  fi
  mkdir -p "$RENDER_DIR"
  # Render artifacts can carry secret-adjacent material — owner-only.
  chmod 700 "$RENDER_DIR" 2>/dev/null || true
  log_info "Render dir: ${RENDER_DIR}"
}

# ---------------------------------------------------------------------------
# Cleanup — helm uninstall releases we created + delete labeled namespaces.
# Guarded; require --force for non-empty/unlabeled namespaces.
# ---------------------------------------------------------------------------
# Namespaces that must NEVER be deleted by cleanup, even if mislabeled (hard
# allowlist-exclusion).
CLEANUP_PROTECTED_NS=( kube-system kube-public kube-node-lease default )

_cleanup_is_protected_ns() {
  local ns="$1" p
  for p in "${CLEANUP_PROTECTED_NS[@]}"; do
    [[ "$ns" == "$p" ]] && return 0
  done
  return 1
}

run_cleanup() {
  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    log_step "=== DRY-RUN CLEANUP — nothing will be deleted ==="
  else
    log_step "=== CLEANUP — removing resources created by kcloud-tool ==="
  fi

  # Note the label selector explicitly so cleanup scope is auditable (and the
  # validator can confirm cleanup is label-scoped, never a blanket delete).
  log_info "Cleanup scope: helm releases we install + namespaces annotated kcloud-tool/created=true"
  log_info "Resource ownership label selector: app.kubernetes.io/managed-by=${MANAGED_BY}"

  local _reachable=true
  if ! kubectl cluster-info &>/dev/null; then
    _reachable=false
    if [[ "$OPT_DRY_RUN" == "true" ]]; then
      log_warn "Cluster not reachable — dry-run cleanup will only describe intended actions."
    else
      log_error "Cluster not reachable. Cannot perform cleanup."
      exit 1
    fi
  fi

  # ---- 1. helm uninstall the releases THIS tool installs (shared-namespace safe;
  #         removes our resources WITHOUT deleting the namespace). -------------
  local -a rel_ns=(
    "etri-llm-app:${APP_NAMESPACE}"
    "nfs-subdir-external-provisioner:nfs-provisioner"
    "gpu-operator:gpu-operator"
    "kube-prometheus-stack:monitoring"
    "alloy:monitoring"
    "loki:loki"
    "furiosa-device-plugin:furiosa-system"
    "furiosa-feature-discovery:furiosa-system"
  )

  local entry rel ns
  for entry in "${rel_ns[@]}"; do
    rel="${entry%%:*}"
    ns="${entry##*:}"
    if [[ "$OPT_DRY_RUN" == "true" ]]; then
      log_info "[dry-run] would: helm uninstall ${rel} -n ${ns}"
      continue
    fi
    [[ "$_reachable" == "true" ]] || continue
    if helm status "$rel" -n "$ns" &>/dev/null; then
      log_info "helm uninstall ${rel} -n ${ns}"
      helm uninstall "$rel" -n "$ns" >/dev/null 2>&1 || log_warn "helm uninstall ${rel} -n ${ns} failed (continuing)"
    fi
  done

  # ---- 2. Delegate benchmark-layer cleanup to the pilot installer. ----------
  local pilot="${SCRIPT_DIR}/install_pilot_k8s.sh"
  if [[ -f "$pilot" ]]; then
    local pilot_help=""
    pilot_help="$(bash "$pilot" --help 2>&1 || true)"
    local -a pa=( --node-ips "$NODE_IPS" )
    printf '%s' "$pilot_help" | grep -qE -- '(^|[^[:alnum:]_])--namespace([^[:alnum:]_-]|$)' && pa+=( --namespace "$BENCH_NAMESPACE" )
    printf '%s' "$pilot_help" | grep -qE -- '(^|[^[:alnum:]_])--cleanup([^[:alnum:]_-]|$)'   && pa+=( --cleanup )
    [[ "$OPT_DRY_RUN" == "true" ]] && printf '%s' "$pilot_help" | grep -qE -- '(^|[^[:alnum:]_])--dry-run([^[:alnum:]_-]|$)' && pa+=( --dry-run )
    [[ "$OPT_FORCE"   == "true" ]] && printf '%s' "$pilot_help" | grep -qE -- '(^|[^[:alnum:]_])--force([^[:alnum:]_-]|$)'   && pa+=( --force )
    log_info "Delegating benchmark cleanup: install_pilot_k8s.sh ${pa[*]}"
    bash "$pilot" "${pa[@]}" || log_warn "Benchmark cleanup reported issues (continuing)."
  fi

  # ---- 3. Delete ONLY namespaces WE created (annotation kcloud-tool/created=true).
  #         Never a protected system namespace; refuse non-empty unless --force. -
  if [[ "$OPT_DRY_RUN" == "true" && "$_reachable" == "false" ]]; then
    log_info "[dry-run/offline] would: delete only namespaces annotated kcloud-tool/created=true (managed-by=${MANAGED_BY}); cannot enumerate — cluster unreachable."
    log_info "Cleanup complete"
    exit 0
  fi

  local -a created_ns=()
  if [[ "$_reachable" == "true" ]]; then
    while IFS= read -r line; do
      [[ -n "$line" ]] && created_ns+=("$line")
    done < <(kubectl get namespace \
      -o jsonpath='{range .items[?(@.metadata.annotations.kcloud-tool/created=="true")]}{.metadata.name}{"\n"}{end}' \
      2>/dev/null || true)
  fi

  if [[ ${#created_ns[@]} -eq 0 ]]; then
    log_info "No namespaces annotated kcloud-tool/created=true found (nothing this tool created to delete)."
  fi

  for ns in "${created_ns[@]}"; do
    # Hard allowlist-exclusion of system namespaces, even if mislabeled.
    if _cleanup_is_protected_ns "$ns"; then
      log_warn "Refusing to delete protected namespace '${ns}' (system namespace; skipping)."
      continue
    fi

    if [[ "$OPT_DRY_RUN" == "true" ]]; then
      log_info "[dry-run] would: kubectl delete namespace ${ns} (annotation kcloud-tool/created=true, managed-by=${MANAGED_BY})"
      continue
    fi

    # Emptiness guard: compare total resources vs. our managed-by subset.
    local total owned
    total=$(kubectl get all -n "$ns" -o name 2>/dev/null | wc -l | tr -d ' ')
    owned=$(kubectl get all -n "$ns" -l "app.kubernetes.io/managed-by=${MANAGED_BY}" -o name 2>/dev/null | wc -l | tr -d ' ')
    if [[ "${total:-0}" -ne "${owned:-0}" && "$OPT_FORCE" != "true" ]]; then
      log_warn "Namespace '${ns}' still contains non-kcloud-tool resources (${owned}/${total} owned) — skipping (use --force to delete anyway)."
      continue
    fi

    log_info "kubectl delete namespace ${ns} (we created it; annotation kcloud-tool/created=true)"
    kubectl delete namespace "$ns" --ignore-not-found=true >/dev/null 2>&1 || \
      log_warn "Failed to delete namespace ${ns} (continuing)."
  done

  log_info "Cleanup complete"
  exit 0
}

# ---------------------------------------------------------------------------
# Run a single stage by name (used by both --only and the default sequence).
# ---------------------------------------------------------------------------
run_stage() {
  case "$1" in
    preflight)     stage_preflight ;;
    provision)     stage_provision ;;
    storage)       stage_storage ;;
    operators)     stage_operators ;;
    observability) stage_observability ;;
    webapp)        stage_webapp ;;
    benchmarks)    stage_benchmarks ;;
    verify)        stage_verify ;;
    *)
      log_error "Unknown stage: $1"
      return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# Clean up the dry-run render dir contents are kept for inspection; timestamped
# dirs are kept too (they are gitignored artifacts). No temp removal needed.
# ---------------------------------------------------------------------------

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  parse_args "$@"
  # --help handled in parse_args (exits 0, never reaches here)
  validate_args

  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    log_step "=== DRY-RUN — no cluster mutations will be made ==="
  elif [[ "$OPT_VALIDATE_ONLY" == "true" ]]; then
    log_step "=== VALIDATE-ONLY — read-only readiness check ==="
  elif [[ "$OPT_CLEANUP" == "true" ]]; then
    : # banner printed in run_cleanup
  else
    log_step "=== kcloud-tool FULL-STACK INSTALLER ==="
  fi

  setup_render_dir
  export_render_vars

  # ------ CLEANUP mode -----------------------------------------------------
  if [[ "$OPT_CLEANUP" == "true" ]]; then
    run_cleanup   # exits
  fi

  # ------ VALIDATE-ONLY mode (read-only) -----------------------------------
  if [[ "$OPT_VALIDATE_ONLY" == "true" ]]; then
    if stage_preflight; then
      # Re-export DEVICE_MODE resolved by preflight.
      export DEVICE_MODE
      log_step "=== VALIDATE-ONLY PASSED — cluster is ready for full-stack install ==="
      exit 0
    fi
    log_error "=== VALIDATE-ONLY FAILED — blockers found (see above) ==="
    exit 1
  fi

  # ------ Single-stage mode (--only) ---------------------------------------
  if [[ -n "$ONLY_STAGE" ]]; then
    # All stages depend on preflight resolution; run it first (unless asked for it).
    if [[ "$ONLY_STAGE" != "preflight" ]]; then
      stage_preflight || { log_error "Preflight failed — cannot run --only ${ONLY_STAGE}"; exit 1; }
      export DEVICE_MODE
    fi
    if run_stage "$ONLY_STAGE"; then
      log_step "=== Stage '${ONLY_STAGE}' complete ==="
      exit 0
    fi
    log_error "=== Stage '${ONLY_STAGE}' FAILED ==="
    exit 1
  fi

  # ------ Full sequence ----------------------------------------------------
  local -a sequence=( preflight provision storage operators observability webapp benchmarks verify )
  local stage
  for stage in "${sequence[@]}"; do
    if ! run_stage "$stage"; then
      log_error "Stage '${stage}' failed — aborting full-stack install."
      exit 1
    fi
    # Re-export device mode after preflight so later stages see it.
    [[ "$stage" == "preflight" ]] && export DEVICE_MODE
  done

  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    log_step "=== DRY-RUN COMPLETE — no cluster mutations made (rendered to ${RENDER_DIR}) ==="
  else
    log_step "=== FULL-STACK INSTALL COMPLETE ==="
  fi
}

main "$@"
