#!/usr/bin/env bash
# scripts/install_pilot_k8s.sh — kcloud-mlperf LLM benchmark suite Kubernetes installer
#
# One-command happy path (only required input is node IPs):
#   ./scripts/install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
#
# See --help for full flag reference.

set -Eeuo pipefail

# ---------------------------------------------------------------------------
# ERR trap — prints failing line + command + exit code
# ---------------------------------------------------------------------------
trap '_on_err $? "$BASH_COMMAND" ${BASH_LINENO[0]}' ERR
_on_err() {
  local ec="$1" cmd="$2" line="$3"
  # log_error may not be defined yet if we fail during source; fall back to echo
  if type log_error &>/dev/null 2>&1; then
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

# Source logging helpers first — needed by detect.sh
# shellcheck source=scripts/lib/log.sh
source "${LIB_DIR}/log.sh"

# Source auto-detection functions
# shellcheck source=scripts/lib/detect.sh
source "${LIB_DIR}/detect.sh"

# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
usage() {
  cat <<'USAGE'
kcloud-mlperf Kubernetes Installer
===================================
Installs the MLPerf/MMLU LLM benchmark suite on Kubernetes.
Only required input: --node-ips.  Everything else is auto-detected or defaulted.

Usage:
  install_pilot_k8s.sh --node-ips "<ip1,ip2,...>" [OPTIONS]

Required (except --help):
  --node-ips <csv>           Comma-separated node InternalIPs
                             (validated against cluster; warn on mismatch unless --force)

Cluster / naming:
  --namespace <ns>           Target namespace                  (default: kcloud-mlperf)
  --release <name>           Release label for all resources   (default: kcloud-mlperf)

Storage:
  --results-pvc-size <size>  PVC capacity for results          (default: 50Gi)
  --storage-class <sc>       StorageClass name                 (default: auto — prefer NFS/RWX)

Device / model:
  --device <mode>            auto|gpu|npu-rngd|npu-atom|cpu    (default: auto)
                             Priority when auto: gpu > npu-rngd > npu-atom > cpu
  --model <id>               HuggingFace model repo            (default: meta-llama/Llama-3.1-8B-Instruct)
                             FP8 variant auto-selected for gpu/npu-rngd when using repo default.

HF token:
  --hf-token-source <s>      auto|env|file:<path>|secret:<ns>/<name>  (default: auto)
                             auto order: HF_TOKEN env → ~/.cache/huggingface/token → in-cluster secret

Images:
  --registry <reg>           Registry prefix                   (default: auto-detect; empty = public)

Benchmark:
  --bench <mode>             smoke|full                        (default: smoke)
  --timeout <secs>           Rollout/job wait timeout          (default: 600)

Execution modes (mutually exclusive with normal install):
  --dry-run                  Render templates + kubectl apply --dry-run=client; NO cluster mutation
  --validate-only            Read-only preflight checks only; no rendering/apply
                             Exit 0 = cluster ready; non-zero = blockers found
  --cleanup                  Delete resources labeled managed-by=kcloud-tool, part-of=<release>
                             Requires --force to delete a namespace not labeled as ours

Post-install:
  --smoke-test               Explicitly run smoke Job after install and wait for completion
  --skip-smoke-test          Install without running the smoke Job (overrides default)

Safety:
  --force                    Allow non-idempotent / potentially overwriting actions
  -h, --help                 Show this help and exit 0 (NEVER touches the cluster)

Examples:
  # Minimal one-command install
  ./install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"

  # Dry run — render + plan, no cluster mutations
  ./install_pilot_k8s.sh --node-ips "192.0.2.11" --dry-run

  # Preflight only — check cluster readiness
  ./install_pilot_k8s.sh --node-ips "192.0.2.11" --validate-only

  # GPU, full benchmark, explicit HF token file
  ./install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12" \
      --device gpu --bench full --hf-token-source file:/root/.hf_token

  # Cleanup a previously installed release
  ./install_pilot_k8s.sh --node-ips "192.0.2.11" --cleanup --force

USAGE
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
NODE_IPS=""
KCLOUD_NAMESPACE="kcloud-mlperf"
RELEASE="kcloud-mlperf"
RESULTS_PVC_SIZE="50Gi"
STORAGE_CLASS=""
DEVICE_ARG="auto"
MODEL_ARG="meta-llama/Llama-3.1-8B-Instruct"
HF_TOKEN_SOURCE="auto"
REGISTRY_ARG=""
BENCH_MODE="smoke"
TIMEOUT=600
OPT_DRY_RUN=false
OPT_VALIDATE_ONLY=false
OPT_SMOKE_TEST=false
OPT_SKIP_SMOKE=false
OPT_CLEANUP=false
OPT_FORCE=false

# State populated during run (not user inputs)
DEVICE_MODE=""
DEVICE_RESOURCE=""
NODE_SELECTOR_KEY=""
NODE_SELECTOR_VALUE=""
BENCH_IMAGE=""
MODEL_ID=""
N_SAMPLES=""
REGISTRY=""
PVC_ACCESS_MODE=""
HF_TOKEN_B64=""
HF_SECRET_EXISTING=""   # "ns/name" if reusing existing secret; empty = create new
RENDER_DIR=""

# Dry-run guard — exported so kubectl_apply wrapper can read it
KCLOUD_DRYRUN=false
export KCLOUD_DRYRUN

# Set true when the cluster is unreachable during a --dry-run, so the apply step
# previews rendered manifests instead of calling kubectl (which needs a server
# even for --dry-run=client).
CLUSTER_OFFLINE=false

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --node-ips)             NODE_IPS="$2";          shift 2 ;;
      --namespace)            KCLOUD_NAMESPACE="$2";  shift 2 ;;
      --release)              RELEASE="$2";           shift 2 ;;
      --results-pvc-size)     RESULTS_PVC_SIZE="$2";  shift 2 ;;
      --storage-class)        STORAGE_CLASS="$2";     shift 2 ;;
      --device)               DEVICE_ARG="$2";        shift 2 ;;
      --model)                MODEL_ARG="$2";         shift 2 ;;
      --hf-token-source)      HF_TOKEN_SOURCE="$2";   shift 2 ;;
      --registry)             REGISTRY_ARG="$2";      shift 2 ;;
      --bench)                BENCH_MODE="$2";        shift 2 ;;
      --timeout)              TIMEOUT="$2";           shift 2 ;;
      --dry-run)              OPT_DRY_RUN=true;       shift ;;
      --validate-only)        OPT_VALIDATE_ONLY=true; shift ;;
      --smoke-test)           OPT_SMOKE_TEST=true;    shift ;;
      --skip-smoke-test)      OPT_SKIP_SMOKE=true;    shift ;;
      --cleanup)              OPT_CLEANUP=true;       shift ;;
      --force)                OPT_FORCE=true;         shift ;;
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
# Argument validation
# ---------------------------------------------------------------------------
validate_args() {
  if [[ -z "$NODE_IPS" ]]; then
    log_error "--node-ips is required (comma-separated node InternalIPs)"
    echo "" >&2
    usage >&2
    exit 1
  fi

  case "$DEVICE_ARG" in
    auto|gpu|npu-rngd|npu-atom|cpu) ;;
    *)
      log_error "--device must be one of: auto|gpu|npu-rngd|npu-atom|cpu (got: ${DEVICE_ARG})"
      exit 1 ;;
  esac

  case "$BENCH_MODE" in
    smoke|full) ;;
    *)
      log_error "--bench must be one of: smoke|full (got: ${BENCH_MODE})"
      exit 1 ;;
  esac

  case "$HF_TOKEN_SOURCE" in
    auto|env|file:*|secret:*/*) ;;
    *)
      log_error "--hf-token-source must be: auto|env|file:<path>|secret:<ns>/<name> (got: ${HF_TOKEN_SOURCE})"
      exit 1 ;;
  esac

  if ! [[ "$TIMEOUT" =~ ^[0-9]+$ ]] || [[ "$TIMEOUT" -lt 1 ]]; then
    log_error "--timeout must be a positive integer (got: ${TIMEOUT})"
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# kubectl_apply wrapper
# All mutating kubectl calls MUST go through this function.
# Under KCLOUD_DRYRUN=true it prints a plan and uses --dry-run=client; never mutates.
# ---------------------------------------------------------------------------
kubectl_apply() {
  local manifest="$1"
  if [[ "$KCLOUD_DRYRUN" == "true" ]]; then
    if [[ "${CLUSTER_OFFLINE:-false}" == "true" ]]; then
      # No cluster: kubectl --dry-run=client still needs a server, so just preview.
      if [[ -f "$manifest" ]]; then
        log_info "[dry-run/offline] would apply: $(basename "$manifest") (client validation skipped — no cluster)"
      else
        log_info "[dry-run/offline] would apply: (stdin) (client validation skipped — no cluster)"
      fi
    elif [[ -f "$manifest" ]]; then
      log_info "[dry-run] apply: $(basename "$manifest")"
      kubectl apply --dry-run=client -f "$manifest" 2>&1 | sed 's/^/  /' >&2
    else
      log_info "[dry-run] apply: (stdin)"
      kubectl apply --dry-run=client -f - <<< "$manifest" 2>&1 | sed 's/^/  /' >&2
    fi
  else
    if [[ -f "$manifest" ]]; then
      kubectl apply -f "$manifest"
    else
      kubectl apply -f - <<< "$manifest"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Preflight checks
# ---------------------------------------------------------------------------
run_preflight() {
  log_step "Preflight checks..."

  # 1. Cluster reachable
  if ! kubectl cluster-info &>/dev/null; then
    if [[ "$OPT_DRY_RUN" == "true" ]]; then
      # Offline dry-run: render the plan from defaults; no live detection or apply.
      CLUSTER_OFFLINE=true
      log_warn "Cluster not reachable — DRY-RUN will preview the plan using defaults (no live detection)."
      if [[ "$DEVICE_ARG" == "auto" ]]; then
        DEVICE_MODE="cpu"
        log_warn "Device auto-detect needs a cluster; plan defaults to device mode 'cpu' (override with --device)."
      else
        DEVICE_MODE="$DEVICE_ARG"
      fi
      [[ -z "$STORAGE_CLASS" ]] && STORAGE_CLASS="auto-detect-at-apply"
      PVC_ACCESS_MODE="${PVC_ACCESS_MODE:-ReadWriteOnce}"
      log_info "Preflight (offline dry-run) complete"
      return 0
    fi
    log_error "Cluster not reachable. Check KUBECONFIG / cluster state."
    exit 1
  fi
  log_info "Cluster is reachable"

  # 2. Node IP validation
  local node_result
  node_result=$(detect_nodes_match "$NODE_IPS")
  case "$node_result" in
    ok)
      log_info "All node IPs matched in cluster"
      ;;
    unknown)
      log_warn "Could not validate node IPs (kubectl error) — proceeding with caution"
      ;;
    mismatch:*)
      local bad="${node_result#mismatch:}"
      local is_mutating=true
      [[ "$OPT_DRY_RUN" == "true" || "$OPT_VALIDATE_ONLY" == "true" ]] && is_mutating=false
      if [[ "$is_mutating" == "true" && "$OPT_FORCE" != "true" ]]; then
        log_error "Node IP mismatch — IPs not found in cluster: ${bad}"
        log_error "Use --force to proceed anyway, or verify your --node-ips value."
        exit 1
      else
        log_warn "Node IP mismatch (not found: ${bad}) — continuing"
      fi
      ;;
  esac

  # 3. Device mode
  if [[ "$DEVICE_ARG" == "auto" ]]; then
    log_info "Auto-detecting device mode..."
    DEVICE_MODE=$(detect_device_mode)
    log_info "Detected device mode: ${DEVICE_MODE}"
  else
    DEVICE_MODE="$DEVICE_ARG"
    log_info "Using specified device mode: ${DEVICE_MODE}"
  fi

  # 4. Device operator/plugin check (non-CPU only)
  if [[ "$DEVICE_MODE" != "cpu" ]]; then
    if detect_operator_present "$DEVICE_MODE"; then
      log_info "Device operator/plugin confirmed for mode '${DEVICE_MODE}'"
    else
      if [[ "$OPT_FORCE" == "true" ]]; then
        log_warn "Device operator not confirmed for '${DEVICE_MODE}' — continuing with --force"
      else
        log_error "No device operator or plugin found for mode '${DEVICE_MODE}'."
        log_error "Use --force to override, or choose a different --device."
        exit 1
      fi
    fi
  fi

  # 5. Storage class
  if [[ -z "$STORAGE_CLASS" ]]; then
    log_info "Auto-detecting storage class (prefer NFS/RWX)..."
    STORAGE_CLASS=$(detect_storage_class)
    if [[ -z "$STORAGE_CLASS" ]]; then
      log_error "No usable StorageClass found. Specify one with --storage-class."
      exit 1
    fi
    log_info "Detected storage class: ${STORAGE_CLASS}"
  else
    log_info "Using specified storage class: ${STORAGE_CLASS}"
  fi

  # 6. PVC access mode
  PVC_ACCESS_MODE=$(detect_pvc_access_mode "$STORAGE_CLASS")
  log_info "PVC access mode: ${PVC_ACCESS_MODE}"

  # 7. HF token
  resolve_hf_token

  log_info "Preflight complete"
}

# ---------------------------------------------------------------------------
# HF token resolution
# Sets: HF_TOKEN_B64, HF_SECRET_EXISTING
# NEVER logs the token value.
# ---------------------------------------------------------------------------
resolve_hf_token() {
  local source="$HF_TOKEN_SOURCE"

  if [[ "$source" == "auto" ]]; then
    source=$(detect_hf_secret "$KCLOUD_NAMESPACE")
    if [[ -z "$source" ]]; then
      log_warn "No HF token source found via auto-detection."
      log_warn "Set HF_TOKEN env var, use --hf-token-source, or create the 'huggingface-token' secret."
      return 0   # non-fatal; bench may not need it for all modes
    fi
    # Redact file path details from log
    local log_src="${source//file:*/file:[path]}"
    log_info "Auto-detected HF token source: ${log_src}"
  fi

  case "$source" in
    env)
      local token="${HF_TOKEN:-${HUGGING_FACE_HUB_TOKEN:-}}"
      if [[ -z "$token" ]]; then
        log_error "HF token source 'env' specified but HF_TOKEN / HUGGING_FACE_HUB_TOKEN are not set"
        exit 1
      fi
      HF_TOKEN_B64=$(printf '%s' "$token" | base64)
      log_info "HF token source: environment variable ($(redact token))"
      ;;

    file:*)
      local fpath="${source#file:}"
      if [[ ! -f "$fpath" ]]; then
        log_error "HF token file not found: ${fpath}"
        exit 1
      fi
      local token
      token=$(cat "$fpath")
      if [[ -z "$token" ]]; then
        log_error "HF token file is empty: ${fpath}"
        exit 1
      fi
      HF_TOKEN_B64=$(printf '%s' "$token" | base64)
      log_info "HF token source: file ${fpath} ($(redact token))"
      unset token
      ;;

    secret:*/*)
      local ns_name="${source#secret:}"
      local sec_ns="${ns_name%%/*}"
      local sec_name="${ns_name#*/}"
      if ! kubectl get secret "$sec_name" -n "$sec_ns" &>/dev/null; then
        log_error "Specified HF secret '${sec_name}' not found in namespace '${sec_ns}'"
        exit 1
      fi
      HF_SECRET_EXISTING="${sec_ns}/${sec_name}"
      HF_TOKEN_B64=""   # will reference existing secret; no new secret needed
      log_info "HF token source: existing secret ${sec_ns}/${sec_name}"
      ;;

    *)
      log_error "Unrecognised HF token source: ${source}"
      exit 1
      ;;
  esac
}

# ---------------------------------------------------------------------------
# Registry detection
# Sets: REGISTRY
# ---------------------------------------------------------------------------
resolve_registry() {
  if [[ -n "$REGISTRY_ARG" ]]; then
    REGISTRY="$REGISTRY_ARG"
    log_info "Using specified registry: ${REGISTRY}"
  else
    # Auto-detection is INFORMATIONAL ONLY. The bench images (vllm/vllm-openai,
    # python:3.11-slim) are public upstream; prefixing them with an unrelated
    # in-cluster registry would break image pulls. To mirror images through a
    # private/air-gapped registry, pass --registry explicitly.
    local detected
    detected=$(detect_registry || true)
    if [[ -n "$detected" ]]; then
      log_info "Detected an in-cluster registry (${detected}); not auto-applied to public images. Use --registry to mirror."
    else
      log_info "No registry specified — using public images"
    fi
    REGISTRY=""
  fi
}

# ---------------------------------------------------------------------------
# Device-specific variable derivation
# Sets: DEVICE_RESOURCE, NODE_SELECTOR_KEY, NODE_SELECTOR_VALUE,
#       BENCH_IMAGE, MODEL_ID, N_SAMPLES
# ---------------------------------------------------------------------------
set_device_vars() {
  DEVICE_RESOURCE=$(detect_device_resource "$DEVICE_MODE")

  # Prefix helper: prepend registry if set
  _img() { printf '%s%s' "${REGISTRY:+${REGISTRY}/}" "$1"; }

  case "$DEVICE_MODE" in
    gpu)
      NODE_SELECTOR_KEY="nvidia.com/gpu.product"
      NODE_SELECTOR_VALUE="NVIDIA-L40"
      BENCH_IMAGE=$(_img "vllm/vllm-openai:v0.8.4")
      # Auto-select FP8 variant for the repo default model
      if [[ "$MODEL_ARG" == "meta-llama/Llama-3.1-8B-Instruct" ]]; then
        MODEL_ID="RedHatAI/Meta-Llama-3.1-8B-Instruct-FP8"
      else
        MODEL_ID="$MODEL_ARG"
      fi
      ;;

    npu-rngd)
      # RNGD path: thin client on a CPU node hitting external furiosa-llm server
      NODE_SELECTOR_KEY="kubernetes.io/os"
      NODE_SELECTOR_VALUE="linux"
      BENCH_IMAGE=$(_img "python:3.11-slim")
      if [[ "$MODEL_ARG" == "meta-llama/Llama-3.1-8B-Instruct" ]]; then
        MODEL_ID="furiosa-ai/Llama-3.1-8B-Instruct-FP8"
      else
        MODEL_ID="$MODEL_ARG"
      fi
      ;;

    npu-atom)
      NODE_SELECTOR_KEY=""
      NODE_SELECTOR_VALUE=""
      BENCH_IMAGE=$(_img "python:3.11-slim")
      MODEL_ID="$MODEL_ARG"
      ;;

    cpu)
      NODE_SELECTOR_KEY=""
      NODE_SELECTOR_VALUE=""
      BENCH_IMAGE=$(_img "python:3.11-slim")
      MODEL_ID="$MODEL_ARG"
      ;;
  esac

  case "$BENCH_MODE" in
    smoke) N_SAMPLES="1" ;;
    full)  N_SAMPLES="100" ;;
  esac

  log_info "Device resource:    ${DEVICE_RESOURCE:-none}"
  log_info "Node selector:      ${NODE_SELECTOR_KEY:-<none>}=${NODE_SELECTOR_VALUE:-<none>}"
  log_info "Bench image:        ${BENCH_IMAGE}"
  log_info "Model ID:           ${MODEL_ID}"
  log_info "N samples:          ${N_SAMPLES}"
}

# ---------------------------------------------------------------------------
# Export frozen template variable set (contract names — do not rename)
# ---------------------------------------------------------------------------
export_template_vars() {
  export KCLOUD_NAMESPACE
  export RELEASE
  export SA_NAME="${RELEASE}-sa"
  export HF_SECRET_NAME="huggingface-token"
  export HF_TOKEN_B64="${HF_TOKEN_B64:-}"
  export BENCH_SCRIPTS_CM="${RELEASE}-bench-scripts"
  export RESULTS_PVC_NAME="${RELEASE}-results"
  export RESULTS_PVC_SIZE
  export STORAGE_CLASS
  export PVC_ACCESS_MODE
  export DEVICE_MODE
  export DEVICE_RESOURCE="${DEVICE_RESOURCE:-}"
  export DEVICE_RESOURCE_COUNT="1"
  export NODE_SELECTOR_KEY="${NODE_SELECTOR_KEY:-}"
  export NODE_SELECTOR_VALUE="${NODE_SELECTOR_VALUE:-}"
  export BENCH_IMAGE
  export MODEL_ID
  export BENCH_MODE
  export N_SAMPLES
  export MAX_TOKENS="128"
  export MANAGED_BY="kcloud-tool"
  export PART_OF="${RELEASE}"
}

# envsubst variable whitelist (only frozen contract vars substituted)
_ENVSUBST_VARS='${KCLOUD_NAMESPACE}${RELEASE}${SA_NAME}${HF_SECRET_NAME}${HF_TOKEN_B64}'\
'${BENCH_SCRIPTS_CM}${RESULTS_PVC_NAME}${RESULTS_PVC_SIZE}${STORAGE_CLASS}${PVC_ACCESS_MODE}'\
'${DEVICE_MODE}${DEVICE_RESOURCE}${DEVICE_RESOURCE_COUNT}${NODE_SELECTOR_KEY}'\
'${NODE_SELECTOR_VALUE}${BENCH_IMAGE}${MODEL_ID}${BENCH_MODE}${N_SAMPLES}'\
'${MAX_TOKENS}${MANAGED_BY}${PART_OF}'

# ---------------------------------------------------------------------------
# Generate the bench-scripts ConfigMap YAML programmatically.
# This avoids passing multi-line Python through envsubst (unsafe).
# The Python script is embedded as a YAML literal block scalar (4-space indent).
# ---------------------------------------------------------------------------
_BENCH_SCRIPT_SRC="${REPO_ROOT}/benchmarks/mlperf_cnndm100_fp8.py"

_generate_bench_cm_yaml() {
  local script_content
  if [[ -f "$_BENCH_SCRIPT_SRC" ]]; then
    script_content=$(cat "$_BENCH_SCRIPT_SRC")
  else
    log_warn "Bench script not found at ${_BENCH_SCRIPT_SRC} — ConfigMap data will be empty placeholder"
    script_content="# bench script not found at ${_BENCH_SCRIPT_SRC}"
  fi

  # Emit valid YAML; the literal block scalar (|) uses 4-space indent for content
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n'
  printf '  name: %s\n  namespace: %s\n' "$BENCH_SCRIPTS_CM" "$KCLOUD_NAMESPACE"
  printf '  labels:\n'
  printf '    app.kubernetes.io/managed-by: %s\n' "$MANAGED_BY"
  printf '    app.kubernetes.io/part-of: %s\n' "$PART_OF"
  printf 'data:\n'
  printf '  mlperf_cnndm100_fp8.py: |\n'
  # Indent every line of the script by 4 spaces
  while IFS= read -r line; do
    printf '    %s\n' "$line"
  done <<< "$script_content"
}

# ---------------------------------------------------------------------------
# Render all templates → RENDER_DIR
# ---------------------------------------------------------------------------
render_templates() {
  local templates_dir="${REPO_ROOT}/deploy/templates"
  RENDER_DIR=$(mktemp -d /tmp/kcloud-render-XXXXXX)
  log_step "Rendering templates → ${RENDER_DIR}"

  # Bench scripts ConfigMap is always generated programmatically
  _generate_bench_cm_yaml > "${RENDER_DIR}/30-configmap-bench-scripts.yaml"
  log_info "Generated (programmatic): 30-configmap-bench-scripts.yaml"

  if [[ ! -d "$templates_dir" ]]; then
    log_warn "Templates directory not found: ${templates_dir} — only bench-scripts CM rendered"
    return 0
  fi

  local fname out
  while IFS= read -r -d '' tmpl; do
    fname=$(basename "$tmpl")
    out="${RENDER_DIR}/${fname}"

    # Skip HF secret template when reusing an existing secret
    if [[ "$fname" == "20-secret-hf-token.yaml" && -n "${HF_SECRET_EXISTING}" ]]; then
      log_info "Skipping ${fname} (reusing existing secret: ${HF_SECRET_EXISTING})"
      continue
    fi

    # Bench scripts CM is already generated above — skip template version
    if [[ "$fname" == "30-configmap-bench-scripts.yaml" ]]; then
      log_info "Skipping template ${fname} (already generated programmatically)"
      continue
    fi

    log_info "Rendering: ${fname}"
    # OPTIONAL-BLOCK STRATEGY: strip sentinel-wrapped blocks whose driving variable
    # is empty BEFORE envsubst, so the CPU/no-device path (empty DEVICE_RESOURCE) and
    # the no-nodeSelector path (empty NODE_SELECTOR_KEY) stay valid YAML. When the var
    # is non-empty the block is kept and the sentinel lines remain as YAML comments.
    local _sed_prog=''
    if [[ -z "${DEVICE_RESOURCE}" ]]; then
      _sed_prog+='/# KCLOUD_BEGIN_DEVICE_REQUEST/,/# KCLOUD_END_DEVICE_REQUEST/d;'
    fi
    if [[ -z "${NODE_SELECTOR_KEY}" ]]; then
      _sed_prog+='/# KCLOUD_BEGIN_NODESELECTOR/,/# KCLOUD_END_NODESELECTOR/d;'
    fi
    if [[ -n "$_sed_prog" ]]; then
      sed "$_sed_prog" "$tmpl" | envsubst "$_ENVSUBST_VARS" > "$out"
    else
      envsubst "$_ENVSUBST_VARS" < "$tmpl" > "$out"
    fi
  done < <(find "$templates_dir" -name "*.yaml" -print0 | sort -z)

  local count
  count=$(find "$RENDER_DIR" -name "*.yaml" | wc -l)
  log_info "Total rendered manifests: ${count}"
}

# ---------------------------------------------------------------------------
# Apply all rendered manifests (in numeric sort order)
# ---------------------------------------------------------------------------
apply_manifests() {
  log_step "Applying manifests..."
  local applied=0
  local fname
  while IFS= read -r -d '' manifest; do
    fname=$(basename "$manifest")
    log_info "Applying: ${fname}"
    kubectl_apply "$manifest"
    applied=$((applied + 1))
  done < <(find "$RENDER_DIR" -name "*.yaml" -print0 | sort -z)
  log_info "Applied ${applied} manifest(s)"
}

# ---------------------------------------------------------------------------
# Copy HF secret from source namespace into target namespace (if needed)
# Only called when HF_SECRET_EXISTING is set and we are NOT in dry-run.
# ---------------------------------------------------------------------------
copy_hf_secret_if_needed() {
  [[ -n "${HF_SECRET_EXISTING}" ]] || return 0

  local src_ns="${HF_SECRET_EXISTING%%/*}"
  local sec_name="${HF_SECRET_EXISTING#*/}"

  # Already present in target namespace?
  if kubectl get secret "$sec_name" -n "$KCLOUD_NAMESPACE" &>/dev/null 2>&1; then
    log_info "HF secret '${sec_name}' already exists in namespace '${KCLOUD_NAMESPACE}' — skipping copy"
    return 0
  fi

  log_step "Copying HF secret '${sec_name}' from '${src_ns}' → '${KCLOUD_NAMESPACE}'..."

  if [[ "$KCLOUD_DRYRUN" == "true" ]]; then
    log_info "[dry-run] Would copy secret ${src_ns}/${sec_name} → ${KCLOUD_NAMESPACE}/${sec_name}"
    return 0
  fi

  # Re-serialize the secret into the target namespace with a clean metadata
  # block (strip uid/resourceVersion/etc), renamed to the contract secret name,
  # using jq.  NEVER log the secret data field — the JSON is piped straight to
  # kubectl apply and is never echoed.
  local secret_json
  secret_json=$(kubectl get secret "$sec_name" -n "$src_ns" -o json 2>/dev/null)

  # Fail loudly if the source secret could not be read (empty/missing) so we
  # never feed empty input to jq/kubectl.
  if [[ -z "${secret_json//[[:space:]]/}" ]]; then
    log_error "Failed to read secret '${sec_name}' from namespace '${src_ns}' (empty or missing)."
    log_error "Verify it exists and is readable: kubectl get secret ${sec_name} -n ${src_ns}"
    return 1
  fi

  if ! printf '%s' "$secret_json" \
    | jq --arg dst "$KCLOUD_NAMESPACE" --arg name "$HF_SECRET_NAME" '
        del(
          .metadata.namespace,
          .metadata.resourceVersion,
          .metadata.uid,
          .metadata.creationTimestamp,
          .metadata.ownerReferences,
          .metadata.managedFields,
          .metadata.annotations
        )
        | .metadata.namespace = $dst
        | .metadata.name = $name
      ' \
    | kubectl apply -f -; then
    log_error "Failed to copy HF secret into namespace '${KCLOUD_NAMESPACE}'."
    return 1
  fi
  log_info "HF secret copied to namespace '${KCLOUD_NAMESPACE}' as '${HF_SECRET_NAME}' (data redacted)"
}

# ---------------------------------------------------------------------------
# Print dry-run plan summary
# ---------------------------------------------------------------------------
print_plan() {
  log_step "===== INSTALL PLAN ====="
  log_info "Namespace:          ${KCLOUD_NAMESPACE}"
  log_info "Release:            ${RELEASE}"
  log_info "Device mode:        ${DEVICE_MODE}"
  log_info "Device resource:    ${DEVICE_RESOURCE:-none}"
  log_info "Node selector:      ${NODE_SELECTOR_KEY:-<none>}=${NODE_SELECTOR_VALUE:-<none>}"
  log_info "Storage class:      ${STORAGE_CLASS}"
  log_info "PVC access mode:    ${PVC_ACCESS_MODE}"
  log_info "Results PVC size:   ${RESULTS_PVC_SIZE}"
  log_info "Bench image:        ${BENCH_IMAGE}"
  log_info "Model ID:           ${MODEL_ID}"
  log_info "Bench mode:         ${BENCH_MODE} (N_SAMPLES=${N_SAMPLES}, MAX_TOKENS=128)"
  log_info "HF token:           ${HF_SECRET_EXISTING:-${HF_TOKEN_SOURCE}}"
  log_info "Registry:           ${REGISTRY:-<public>}"
  log_info "Manifests to apply:"
  if [[ -d "${RENDER_DIR}" ]]; then
    while IFS= read -r -d '' f; do
      log_info "  → $(basename "$f")"
    done < <(find "$RENDER_DIR" -name "*.yaml" -print0 | sort -z)
  fi
  log_step "========================"
}

# ---------------------------------------------------------------------------
# Smoke test runner
# ---------------------------------------------------------------------------
run_smoke_test() {
  local job_name="${RELEASE}-smoke"
  log_step "Running smoke test: ${job_name} in namespace ${KCLOUD_NAMESPACE}..."

  if [[ "$KCLOUD_DRYRUN" == "true" ]]; then
    log_info "[dry-run] Would run smoke test job '${job_name}'"
    return 0
  fi

  if ! kubectl get job "$job_name" -n "$KCLOUD_NAMESPACE" &>/dev/null 2>&1; then
    log_warn "Smoke job '${job_name}' not found in namespace '${KCLOUD_NAMESPACE}' — skipping"
    return 0
  fi

  log_info "Waiting up to ${TIMEOUT}s for smoke job to complete..."
  if kubectl wait "job/${job_name}" \
      --for=condition=complete \
      --timeout="${TIMEOUT}s" \
      -n "$KCLOUD_NAMESPACE"; then
    log_info "Smoke test PASSED"
  else
    log_error "Smoke test FAILED or timed out (timeout=${TIMEOUT}s)"
    log_error "Check pod logs:  kubectl logs -l job-name=${job_name} -n ${KCLOUD_NAMESPACE}"
    log_error "Check events:    kubectl get events -n ${KCLOUD_NAMESPACE} --sort-by=.metadata.creationTimestamp"
    exit 1
  fi
}

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
run_cleanup() {
  log_step "Cleanup: namespace=${KCLOUD_NAMESPACE} release=${RELEASE}"

  if ! kubectl get namespace "$KCLOUD_NAMESPACE" &>/dev/null 2>&1; then
    log_info "Namespace '${KCLOUD_NAMESPACE}' does not exist — nothing to clean up"
    return 0
  fi

  # Guard: only delete from namespaces labeled as managed by us
  local ns_managed
  ns_managed=$(kubectl get namespace "$KCLOUD_NAMESPACE" \
    -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' 2>/dev/null || echo "")

  if [[ "$ns_managed" != "kcloud-tool" && "$OPT_FORCE" != "true" ]]; then
    log_error "Namespace '${KCLOUD_NAMESPACE}' is not labeled app.kubernetes.io/managed-by=kcloud-tool"
    log_error "Use --force to delete resources from this namespace anyway."
    exit 1
  fi

  local label_sel="app.kubernetes.io/managed-by=kcloud-tool,app.kubernetes.io/part-of=${RELEASE}"
  log_info "Deleting resources with labels: ${label_sel}"

  if [[ "$KCLOUD_DRYRUN" == "true" ]]; then
    log_info "[dry-run] Would delete:"
    kubectl delete all,configmap,secret,pvc,serviceaccount,role,rolebinding \
      -l "$label_sel" -n "$KCLOUD_NAMESPACE" \
      --dry-run=client 2>/dev/null || true
    return 0
  fi

  kubectl delete all,configmap,secret,pvc,serviceaccount,role,rolebinding \
    -l "$label_sel" -n "$KCLOUD_NAMESPACE" \
    --ignore-not-found=true || true

  log_info "Cleanup complete"
}

# ---------------------------------------------------------------------------
# Cleanup temp dir on exit
# ---------------------------------------------------------------------------
_cleanup_tempdir() {
  if [[ -n "${RENDER_DIR:-}" && -d "${RENDER_DIR}" ]]; then
    rm -rf "$RENDER_DIR"
  fi
}
trap _cleanup_tempdir EXIT

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
  parse_args "$@"

  # --help already handled in parse_args (exits 0, never reaches here)

  validate_args

  # Propagate dry-run flag
  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    KCLOUD_DRYRUN=true
    export KCLOUD_DRYRUN
  fi

  # ------ VALIDATE-ONLY mode -----------------------------------------------
  if [[ "$OPT_VALIDATE_ONLY" == "true" ]]; then
    log_step "=== VALIDATE-ONLY MODE ==="
    run_preflight
    resolve_registry
    set_device_vars
    log_step "=== PREFLIGHT PASSED — cluster is ready for installation ==="
    exit 0
  fi

  # ------ CLEANUP mode -----------------------------------------------------
  if [[ "$OPT_CLEANUP" == "true" ]]; then
    if [[ "$OPT_DRY_RUN" == "true" ]]; then
      log_step "=== DRY-RUN CLEANUP MODE ==="
    else
      log_step "=== CLEANUP MODE ==="
    fi
    run_cleanup
    exit 0
  fi

  # ------ INSTALL (or dry-run install) mode --------------------------------
  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    log_step "=== DRY-RUN INSTALL — no cluster mutations will be made ==="
  else
    log_step "=== kcloud-mlperf INSTALLER ==="
  fi

  run_preflight
  resolve_registry
  set_device_vars
  export_template_vars
  render_templates
  print_plan

  if [[ "$OPT_DRY_RUN" == "true" ]]; then
    log_step "Applying manifests (dry-run)..."
    apply_manifests
    log_step "=== DRY-RUN COMPLETE — no cluster mutations made ==="
    exit 0
  fi

  # Real apply
  apply_manifests
  copy_hf_secret_if_needed

  log_step "Install complete"

  # Smoke test decision
  if [[ "$OPT_SKIP_SMOKE" == "true" ]]; then
    log_info "Smoke test skipped (--skip-smoke-test)"
  elif [[ "$OPT_SMOKE_TEST" == "true" || "$BENCH_MODE" == "smoke" ]]; then
    run_smoke_test
  fi

  log_step "=== Done ==="
}

main "$@"
