#!/usr/bin/env bash
# scripts/validate_full_stack.sh
# Static + safe behavioral validation harness for the kcloud full-stack installer.
# NEVER mutates a live cluster. All checks are dry-run / read-only.
#
# Usage: validate_full_stack.sh [--help|-h]
# Exit:  0 if all checks PASS or SKIP; non-zero if any FAIL.
set -Eeuo pipefail

# ── paths ──────────────────────────────────────────────────────────────────────
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${REPO_ROOT}/scripts/install_kcloud_stack.sh"
STAGES_LIB="${REPO_ROOT}/scripts/lib/stages.sh"
DETECT_LIB="${REPO_ROOT}/scripts/lib/detect.sh"
LOG_LIB="${REPO_ROOT}/scripts/lib/log.sh"
PLATFORM_TMPL_DIR="${REPO_ROOT}/deploy/platform"
SELF="${BASH_SOURCE[0]}"

# Sample node IPs for the pilot cluster (NOT the dev cluster).
# These must appear in rendered output; dev-cluster IPs must NOT appear.
SAMPLE_IPS="192.0.2.11,192.0.2.12,192.0.2.13"
SAMPLE_CP="192.0.2.11"
SAMPLE_NFS="192.0.2.11"

# Dev-cluster IPs that must NEVER leak into rendered artifacts.
DEV_IPS=("192.0.2.41" "192.0.2.195" "192.0.2.196")

# ── on_err trap ────────────────────────────────────────────────────────────────
_on_err() {
  local _rc=$? _cmd="${BASH_COMMAND}" _line="${BASH_LINENO[0]}"
  printf '[ERROR] validate_full_stack.sh: unexpected error at line %s (rc=%s): %s\n' \
    "$_line" "$_rc" "$_cmd" >&2
}
trap '_on_err' ERR

# ── result tracking ────────────────────────────────────────────────────────────
declare -a CHECK_NAMES=()
declare -a CHECK_RESULTS=()   # PASS / FAIL / SKIP
declare -a CHECK_DETAILS=()   # failure detail or empty

_pass() { CHECK_NAMES+=("$1"); CHECK_RESULTS+=("PASS"); CHECK_DETAILS+=(""); }
_fail() { CHECK_NAMES+=("$1"); CHECK_RESULTS+=("FAIL"); CHECK_DETAILS+=("${2:-}"); }
_skip() { CHECK_NAMES+=("$1"); CHECK_RESULTS+=("SKIP"); CHECK_DETAILS+=("${2:-}"); }

# ── usage ──────────────────────────────────────────────────────────────────────
_usage() {
  cat <<'EOF'
Usage: validate_full_stack.sh [--help|-h]

Static + safe behavioral checks for the kcloud full-stack installer.
Never mutates a live cluster. Safe to run at any time.

Checks performed:
  1  bash syntax (bash -n) on installer, libs, and self + shellcheck (warn-only)
  2  --help: exits 0, output contains --node-ips, runs with KUBECONFIG=/dev/null
  3  --dry-run --node-ips <sample>: exits 0, prints plan, NO mutating verb executed
     (no helm install/upgrade w/o --dry-run, no kubectl apply w/o --dry-run=client,
      no kubectl create/delete/patch/replace)
  4  Node-IP substitution: sample IPs appear in rendered templates; no dev-cluster
     IP (192.0.2.41 / 192.0.2.195 / 192.0.2.196) leaks into rendered output
  5  helm template each vendored chart with rendered overrides → valid YAML
     (SKIP if helm not on PATH)
  6  Secret safety: planted canary HF_TOKEN and SSHPASS never appear in any log output
  7  --cleanup --dry-run selects only managed-by=kcloud-tool resources
     (never a blanket delete)

On any FAIL: prints the exact failing command and a one-line remediation hint.
Final summary table: CHECK -> PASS / FAIL / SKIP.

Exit: 0 = all PASS/SKIP.  Non-zero = at least one FAIL.
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  _usage
  exit 0
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 1 — bash -n syntax + shellcheck (warn-only if absent)
# ══════════════════════════════════════════════════════════════════════════════
_check_bash_syntax() {
  local label="$1" path="$2"
  if [[ ! -f "$path" ]]; then
    _fail "$label" "not found: $path — agent artifact missing; will PASS once created"
    return
  fi
  local out
  if out=$(bash -n "$path" 2>&1); then
    _pass "$label"
  else
    _fail "$label" \
      "bash -n $path failed: ${out}"$'\n'"  Remediation: fix the syntax error reported above."
  fi
}

_check_bash_syntax "1a.syntax:install_kcloud_stack.sh"  "$INSTALLER"
_check_bash_syntax "1b.syntax:lib/stages.sh"            "$STAGES_LIB"
_check_bash_syntax "1c.syntax:lib/detect.sh"            "$DETECT_LIB"
_check_bash_syntax "1d.syntax:lib/log.sh"               "$LOG_LIB"
_check_bash_syntax "1e.syntax:validate_full_stack.sh"   "$SELF"

# shellcheck — warn-only (SKIP if absent; FAIL on actual warnings so devs notice)
if ! command -v shellcheck &>/dev/null; then
  _skip "1f.shellcheck" "shellcheck not on PATH — install: sudo apt-get install shellcheck"
else
  _sc_targets=()
  for _f in "$INSTALLER" "$STAGES_LIB" "$DETECT_LIB" "$LOG_LIB" "$SELF"; do
    [[ -f "$_f" ]] && _sc_targets+=("$_f")
  done
  if [[ ${#_sc_targets[@]} -eq 0 ]]; then
    _skip "1f.shellcheck" "no target files exist yet (agents still writing)"
  else
    _sc_out=$(shellcheck --severity=warning "${_sc_targets[@]}" 2>&1) && _sc_rc=0 || _sc_rc=$?
    if [[ $_sc_rc -eq 0 ]]; then
      _pass "1f.shellcheck"
    else
      _fail "1f.shellcheck" \
        "${_sc_out:0:600}"$'\n'"  Remediation: fix shellcheck warnings listed above."
    fi
  fi
  unset _sc_targets _sc_out _sc_rc _f
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 2 — --help: exits 0, contains --node-ips, no cluster access
# ══════════════════════════════════════════════════════════════════════════════
if [[ ! -f "$INSTALLER" ]]; then
  _fail "2.help-flag" \
    "not found: $INSTALLER — will PASS once agent-A creates it"
else
  _help_out=$(KUBECONFIG=/dev/null bash "$INSTALLER" --help 2>&1) && _help_rc=0 || _help_rc=$?
  if [[ $_help_rc -ne 0 ]]; then
    _fail "2.help-flag" \
      "KUBECONFIG=/dev/null bash install_kcloud_stack.sh --help  → exited $_help_rc (expected 0)"$'\n'"  Output snippet: ${_help_out:0:300}"$'\n'"  Remediation: handle --help before any cluster access and before set -e triggers."
  elif ! echo "$_help_out" | grep -q -- '--node-ips'; then
    _fail "2.help-flag" \
      "--help output does not contain '--node-ips'."$'\n'"  Output snippet: ${_help_out:0:400}"$'\n'"  Remediation: add --node-ips to the usage/help text."
  else
    _pass "2.help-flag"
  fi
  unset _help_out _help_rc
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 3 — --dry-run: exits 0, prints plan, NO mutating verb executed
#
# Mutating verbs checked:
#   helm install / helm upgrade (without --dry-run flag)
#   kubectl apply (without --dry-run=client)
#   kubectl create / delete / patch / replace
#
# KUBECONFIG=/dev/null so no real cluster is needed. A connection error from
# kubectl read-only calls is acceptable (proves read-only was attempted). The
# hard assertion is that NO mutating verb appears unguarded in the output trace.
# ══════════════════════════════════════════════════════════════════════════════
if [[ ! -f "$INSTALLER" ]]; then
  _fail "3.dry-run-no-mutation" \
    "not found: $INSTALLER — will PASS once agent-A creates it"
else
  _dr_tmp=$(mktemp)
  # shellcheck disable=SC2064
  trap "rm -f '$_dr_tmp'" EXIT

  KUBECONFIG=/dev/null bash "$INSTALLER" \
    --dry-run --node-ips "$SAMPLE_IPS" \
    >"$_dr_tmp" 2>&1 && _dr_rc=0 || _dr_rc=$?

  _dr_out=$(cat "$_dr_tmp")

  # Cluster-connection errors are expected (no real cluster); treat as non-fatal.
  _cluster_err=false
  if echo "$_dr_out" | grep -qiE \
    'connection refused|connection to the server|unable to connect|dial tcp|the server could not find|no configuration has been provided|invalid configuration|kubeconfig.*invalid|cluster not reachable|not reachable|CLUSTER_OFFLINE'; then
    _cluster_err=true
  fi

  if [[ $_dr_rc -ne 0 ]] && [[ "$_cluster_err" == "false" ]]; then
    _fail "3.dry-run-no-mutation" \
      "KUBECONFIG=/dev/null bash install_kcloud_stack.sh --dry-run --node-ips '$SAMPLE_IPS'  → exited $_dr_rc"$'\n'"  Output snippet: ${_dr_out:0:400}"$'\n'"  Remediation: ensure --dry-run path does not call exit-on-error operations requiring a live cluster."
  else
    _mutation_found=false
    _mutation_detail=""
    while IFS= read -r _line; do
      # Skip dry-run / offline PREVIEW log lines. These describe INTENTIONS
      # ("[dry-run/offline] would 'helm upgrade --install ...'", "[dry-run] would
      # ensure namespace ...", "template skipped"), not executed commands. The
      # real mutation signal is an UNGUARDED, actually-invoked command line.
      if echo "$_line" | grep -qiE '\[dry-run|\[offline\]|\bwould\b|template skipped'; then
        continue
      fi
      # helm install or helm upgrade without --dry-run flag is a mutation.
      if echo "$_line" | grep -qE '\bhelm (install|upgrade)\b' && \
         ! echo "$_line" | grep -q -- '--dry-run'; then
        _mutation_found=true
        _mutation_detail="helm install/upgrade without --dry-run detected: ${_line}"
        break
      fi
      # kubectl create / delete / patch / replace are never OK.
      if echo "$_line" | grep -qE '\bkubectl (create|delete|patch|replace)\b'; then
        _mutation_found=true
        _mutation_detail="mutating kubectl verb detected: ${_line}"
        break
      fi
      # kubectl apply without --dry-run=client is a mutation.
      if echo "$_line" | grep -qE '\bkubectl apply\b' && \
         ! echo "$_line" | grep -q -- '--dry-run'; then
        _mutation_found=true
        _mutation_detail="kubectl apply without --dry-run=client detected: ${_line}"
        break
      fi
    done < "$_dr_tmp"

    if [[ "$_mutation_found" == "true" ]]; then
      _fail "3.dry-run-no-mutation" \
        "$_mutation_detail"$'\n'"  Remediation: in --dry-run path always pass --dry-run to helm and --dry-run=client to kubectl apply; never call create/delete/patch/replace."
    else
      _pass "3.dry-run-no-mutation"
    fi
    unset _mutation_found _mutation_detail _line
  fi

  rm -f "$_dr_tmp"
  trap - EXIT
  unset _dr_tmp _dr_out _dr_rc _cluster_err
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 4 — Node-IP substitution correctness
#
# Render each *.tmpl in deploy/platform/ with sample IPs exported as the
# FROZEN variable set. Assert:
#   a) at least one sample IP (192.0.2.11/12/13) appears in every rendered file
#   b) NO dev-cluster IP (192.0.2.41 / 192.0.2.195 / 192.0.2.196) appears
#      in any rendered file
#
# Uses envsubst (required; if absent the check fails — envsubst is a hard dep
# of the installer per the spec).
# ══════════════════════════════════════════════════════════════════════════════

# Export frozen variables with sample values (names FROZEN per plan section 3).
export NODE_IPS="192.0.2.11,192.0.2.12,192.0.2.13"
export CONTROL_PLANE_IP="192.0.2.11"
export ACCESS_IP="192.0.2.11"
export NFS_SERVER="192.0.2.11"
export NFS_PATH="/nfs-storage"
export APP_NAMESPACE="llm-evaluation"
export BENCH_NAMESPACE="kcloud-mlperf"
export SSH_PORT_CP="122"
export SSH_PORT_NPU="22"
export FRONTEND_NODEPORT="30001"
export BACKEND_NODEPORT="30980"
export MANAGED_BY="kcloud-tool"
export PART_OF="kcloud-stack"

# Also export pilot-installer frozen vars so templates that combine both sets render cleanly.
export KCLOUD_NAMESPACE="kcloud-mlperf-test"
export RELEASE="kcloud-mlperf"
export SA_NAME="kcloud-mlperf-sa"
export HF_SECRET_NAME="huggingface-token"
export HF_TOKEN_B64="dGVzdC10b2tlbg=="
export BENCH_SCRIPTS_CM="kcloud-mlperf-bench-scripts"
export RESULTS_PVC_NAME="kcloud-mlperf-results-pvc"
export RESULTS_PVC_SIZE="50Gi"
export STORAGE_CLASS="nfs-client"
export PVC_ACCESS_MODE="ReadWriteMany"
export DEVICE_MODE="gpu"
export DEVICE_RESOURCE="nvidia.com/gpu"
export DEVICE_RESOURCE_COUNT="1"
export NODE_SELECTOR_KEY="nvidia.com/gpu.product"
export NODE_SELECTOR_VALUE="NVIDIA-L40"
export BENCH_IMAGE="vllm/vllm-openai:v0.8.4"
export MODEL_ID="meta-llama/Llama-3.1-8B-Instruct"
export BENCH_MODE="smoke"
export N_SAMPLES="1"
export MAX_TOKENS="128"

# Composite kubespray inventory vars — assembled by stages.sh at runtime from node IPs.
# Exported here with sample IP content so envsubst resolves them in kubespray-inventory.ini.tmpl.
export NODE_ENTRIES_ALL="node1 ansible_host=192.0.2.11 ansible_port=122 ip=192.0.2.11 etcd_member_name=etcd1
node2 ansible_host=192.0.2.12 ansible_port=122 ip=192.0.2.12
node3 ansible_host=192.0.2.13 ansible_port=122 ip=192.0.2.13"
export KUBE_CONTROL_PLANE_HOSTS="node1"
export ETCD_HOSTS="node1"
export KUBE_NODE_HOSTS="node2
node3"

if [[ ! -d "$PLATFORM_TMPL_DIR" ]]; then
  _fail "4.node-ip-substitution" \
    "not found: $PLATFORM_TMPL_DIR — agent-B has not created deploy/platform/ yet"$'\n'"  Remediation: wait for agent-B to create deploy/platform/*.tmpl files."
elif ! command -v envsubst &>/dev/null; then
  _fail "4.node-ip-substitution" \
    "envsubst not on PATH — required hard dependency"$'\n'"  Remediation: apt-get install gettext-base"
else
  _tmpls=()
  while IFS= read -r -d '' _t; do
    _tmpls+=("$_t")
  done < <(find "$PLATFORM_TMPL_DIR" -maxdepth 1 -name '*.tmpl' -print0 | sort -z)

  if [[ ${#_tmpls[@]} -eq 0 ]]; then
    _fail "4.node-ip-substitution" \
      "no *.tmpl files found in $PLATFORM_TMPL_DIR — agent-B has not created templates yet"$'\n'"  Remediation: wait for agent-B to populate deploy/platform/*.tmpl."
  else
    _subst_fail=false
    for _tmpl in "${_tmpls[@]}"; do
      _tname="$(basename "$_tmpl")"

      # Render the template.
      _rendered=$(envsubst < "$_tmpl" 2>&1) && _subst_rc=0 || _subst_rc=$?
      if [[ $_subst_rc -ne 0 ]]; then
        _fail "4.node-ip-substitution:${_tname}" \
          "envsubst failed on $_tmpl: $_rendered"$'\n'"  Remediation: check template for envsubst syntax errors."
        _subst_fail=true
        continue
      fi

      # 4a: assert at least one sample IP appears.
      _found_sample=false
      for _sip in "192.0.2.11" "192.0.2.12" "192.0.2.13"; do
        if echo "$_rendered" | grep -qF "$_sip"; then
          _found_sample=true
          break
        fi
      done
      if [[ "$_found_sample" == "false" ]]; then
        _fail "4.node-ip-substitution:${_tname}" \
          "no sample IP (192.0.2.11/12/13) found in rendered output of $_tname"$'\n'"  Remediation: ensure the template uses \${NFS_SERVER}, \${CONTROL_PLANE_IP}, or \${ACCESS_IP} (frozen variable names from plan section 3)."
        _subst_fail=true
        continue
      fi

      # 4b: assert no dev-cluster IP leaks into rendered output.
      _dev_leak=""
      for _dev_ip in "${DEV_IPS[@]}"; do
        if echo "$_rendered" | grep -qF "$_dev_ip"; then
          _dev_leak="$_dev_ip"
          break
        fi
      done
      if [[ -n "$_dev_leak" ]]; then
        _fail "4.node-ip-substitution:${_tname}" \
          "dev-cluster IP '$_dev_leak' leaked into rendered output of $_tname"$'\n'"  Remediation: replace the hardcoded dev IP with the appropriate \${VAR} placeholder."
        _subst_fail=true
        continue
      fi

      _pass "4.node-ip-substitution:${_tname}"
    done

    if [[ "$_subst_fail" == "false" ]]; then
      : # individual PASSes already recorded above
    fi
    unset _subst_fail _tmpl _tname _rendered _subst_rc _found_sample _sip _dev_leak _dev_ip
  fi
  unset _tmpls _t
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 5 — helm template each vendored chart with rendered overrides → valid YAML
#
# Discover vendored charts: look in the PLATFORM_DIR (etri-llm-deployments) for
# known chart directories under kubernetes/. Then render any override template
# from deploy/platform/ that matches by name pattern, and run helm template.
# SKIP gracefully if helm is absent.
# ══════════════════════════════════════════════════════════════════════════════
_PLATFORM_DIR_DEFAULT="/home/kcloud/etri-llm-deployments/app/kubernetes"
_PLATFORM_DIR="${PLATFORM_DIR_OVERRIDE:-${_PLATFORM_DIR_DEFAULT}}"

if ! command -v helm &>/dev/null; then
  _skip "5.helm-template" "helm not on PATH — install helm to enable this check"
elif [[ ! -d "$_PLATFORM_DIR" ]]; then
  _skip "5.helm-template" "PLATFORM_DIR not found: $_PLATFORM_DIR — skipping helm template checks"
else
  # Map of helm release name → chart subdirectory path (relative to PLATFORM_DIR)
  # Derived from plan section 0 stage table.  Paths discovered read-only.
  declare -A _CHART_MAP
  # Only include charts that are local dirs (air-gap friendly).
  # nfs-subdir provisioner chart
  _nfs_chart=""
  if [[ -d "${_PLATFORM_DIR}/nfs-subdir-external-provisioner-4.0.18" ]]; then
    _nfs_chart="${_PLATFORM_DIR}/nfs-subdir-external-provisioner-4.0.18"
  fi
  # gpu-operator chart
  _gpu_chart=""
  if [[ -d "${_PLATFORM_DIR}/gpu-operator-25.10.0" ]]; then
    _gpu_chart="${_PLATFORM_DIR}/gpu-operator-25.10.0"
  fi
  # loki chart — the chart root is the loki/ subdirectory inside loki-2.2.1/
  _loki_chart=""
  if [[ -d "${_PLATFORM_DIR}/loki-2.2.1/loki" ]]; then
    _loki_chart="${_PLATFORM_DIR}/loki-2.2.1/loki"
  elif [[ -d "${_PLATFORM_DIR}/loki-2.2.1" ]]; then
    _loki_chart="${_PLATFORM_DIR}/loki-2.2.1"
  fi
  # kube-prometheus-stack — the chart root is charts/prometheus/ inside the version dir
  _prom_chart=""
  if [[ -d "${_PLATFORM_DIR}/kube-prometheus-stack-79.1.1/charts/prometheus" ]]; then
    _prom_chart="${_PLATFORM_DIR}/kube-prometheus-stack-79.1.1/charts/prometheus"
  elif [[ -d "${_PLATFORM_DIR}/kube-prometheus-stack-79.1.1" ]]; then
    _prom_chart="${_PLATFORM_DIR}/kube-prometheus-stack-79.1.1"
  fi
  # alloy
  _alloy_chart=""
  if [[ -d "${_PLATFORM_DIR}/alloy-1.4.0" ]]; then
    _alloy_chart="${_PLATFORM_DIR}/alloy-1.4.0"
  fi
  # app-chart
  _app_chart=""
  if [[ -d "${_PLATFORM_DIR}/app-chart" ]]; then
    _app_chart="${_PLATFORM_DIR}/app-chart"
  fi

  _helm_any_found=false

  # Helper: run helm template for a chart, with optional upstream values file(s) and
  # an optional platform override values file.  Extra -f args are passed in order.
  # Validates that the output is parseable YAML via python3.
  _helm_check() {
    local check_label="$1" chart_dir="$2"
    # Remaining args are values files to pass as -f (in order).
    shift 2
    if [[ ! -d "$chart_dir" ]]; then
      _skip "$check_label" "chart dir not found: $chart_dir"
      return
    fi
    _helm_any_found=true
    local _helm_args=("template" "kcloud-validate" "$chart_dir" "--dry-run")
    local _vf
    for _vf in "$@"; do
      [[ -n "$_vf" && -f "$_vf" ]] && _helm_args+=("-f" "$_vf")
    done
    unset _vf
    local _h_out _h_rc
    _h_out=$(helm "${_helm_args[@]}" 2>&1) && _h_rc=0 || _h_rc=$?
    if [[ $_h_rc -ne 0 ]]; then
      _fail "$check_label" \
        "helm template failed (rc=$_h_rc): ${_h_out:0:500}"$'\n'"  Remediation: fix chart values or override file at $chart_dir."
      return
    fi
    # Validate YAML via python3.
    local _yaml_err _yaml_rc
    _yaml_err=$(echo "$_h_out" | python3 -c \
      'import sys,yaml;list(yaml.safe_load_all(sys.stdin))' 2>&1) && _yaml_rc=0 || _yaml_rc=$?
    if [[ $_yaml_rc -ne 0 ]]; then
      _fail "$check_label" \
        "helm template output is not valid YAML: $_yaml_err"$'\n'"  Remediation: ensure chart + override produce parseable YAML."
    else
      _pass "$check_label"
    fi
    unset _h_out _h_rc _yaml_err _yaml_rc
  }

  # Render the NFS override template (if present) to a temp file for helm.
  _nfs_override=""
  if [[ -d "$PLATFORM_TMPL_DIR" ]]; then
    _nfs_tmpl="${PLATFORM_TMPL_DIR}/nfs-values-override.yaml.tmpl"
    if [[ -f "$_nfs_tmpl" ]] && command -v envsubst &>/dev/null; then
      _nfs_override=$(mktemp --suffix=.yaml)
      envsubst < "$_nfs_tmpl" > "$_nfs_override" 2>/dev/null || _nfs_override=""
    fi
  fi

  # Render the app-chart override template (if present).
  _app_override=""
  if [[ -d "$PLATFORM_TMPL_DIR" ]]; then
    _app_tmpl="${PLATFORM_TMPL_DIR}/app-chart-values-override.yaml.tmpl"
    if [[ -f "$_app_tmpl" ]] && command -v envsubst &>/dev/null; then
      _app_override=$(mktemp --suffix=.yaml)
      envsubst < "$_app_tmpl" > "$_app_override" 2>/dev/null || _app_override=""
    fi
  fi

  # Upstream values-override.yaml for kube-prometheus-stack (supplies required alertmanager values).
  _prom_upstream_override=""
  if [[ -n "${_prom_chart}" && -f "${_prom_chart}/values-override.yaml" ]]; then
    _prom_upstream_override="${_prom_chart}/values-override.yaml"
  fi

  _helm_check "5a.helm-template:nfs-subdir"            "${_nfs_chart}"   "${_nfs_override}"
  _helm_check "5b.helm-template:gpu-operator"          "${_gpu_chart}"
  _helm_check "5c.helm-template:loki"                  "${_loki_chart}"
  _helm_check "5d.helm-template:kube-prometheus-stack" "${_prom_chart}"  "${_prom_upstream_override}"
  _helm_check "5e.helm-template:alloy"                 "${_alloy_chart}"
  _helm_check "5f.helm-template:app-chart"             "${_app_chart}"   "${_app_override}"

  unset _prom_upstream_override

  # Clean up temp override files.
  [[ -n "${_nfs_override:-}" && -f "${_nfs_override:-}" ]] && rm -f "$_nfs_override"
  [[ -n "${_app_override:-}" && -f "${_app_override:-}" ]] && rm -f "$_app_override"

  if [[ "$_helm_any_found" == "false" ]]; then
    _skip "5.helm-template" \
      "no vendored chart directories found under $_PLATFORM_DIR — skipping helm template checks"
  fi

  unset _nfs_chart _gpu_chart _loki_chart _prom_chart _alloy_chart _app_chart
  unset _nfs_override _app_override _nfs_tmpl _app_tmpl _helm_any_found
  unset -f _helm_check
fi
unset _PLATFORM_DIR _PLATFORM_DIR_DEFAULT

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 6 — Secret safety: canary HF_TOKEN + SSHPASS never appear in any log
#
# Plant two canary secrets and run the installer through its --dry-run path.
# Assert neither canary string appears anywhere in combined stdout+stderr.
# ══════════════════════════════════════════════════════════════════════════════
_CANARY_TOKEN="CANARY-HF-TOKEN-DO-NOT-LOG"
_CANARY_PASS="CANARY-SSH-PASS-DO-NOT-LOG"

if [[ ! -f "$INSTALLER" ]]; then
  _fail "6.secret-safety" \
    "not found: $INSTALLER — will PASS once agent-A creates it"
else
  _canary_out=$(
    HF_TOKEN="$_CANARY_TOKEN" \
    SSHPASS="$_CANARY_PASS" \
    KUBECONFIG=/dev/null \
    bash "$INSTALLER" --dry-run --node-ips "$SAMPLE_IPS" 2>&1 || true
  )

  _leaked=()
  if echo "$_canary_out" | grep -qF "$_CANARY_TOKEN"; then
    _leaked+=("HF_TOKEN canary")
  fi
  if echo "$_canary_out" | grep -qF "$_CANARY_PASS"; then
    _leaked+=("SSHPASS canary")
  fi

  if [[ ${#_leaked[@]} -gt 0 ]]; then
    _leak_names="${_leaked[*]}"
    _leak_lines=$(echo "$_canary_out" | \
      grep -nF -e "$_CANARY_TOKEN" -e "$_CANARY_PASS" 2>/dev/null | head -5 || true)
    _fail "6.secret-safety" \
      "Secret(s) leaked: ${_leak_names}"$'\n'"  Lines: ${_leak_lines:0:400}"$'\n'"  Remediation: replace secret values with \$(redact ...) or '***' in all log/echo/printf calls."
  else
    _pass "6.secret-safety"
  fi
  unset _canary_out _leaked _leak_names _leak_lines
fi
unset _CANARY_TOKEN _CANARY_PASS

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 7 — --cleanup --dry-run uses label selector managed-by=kcloud-tool
#
# Run: KUBECONFIG=/dev/null bash install_kcloud_stack.sh --cleanup --dry-run
#      --node-ips <sample>
# Then assert:
#   a) the command exited 0 (or a cluster-connection-only error)
#   b) the output contains the label selector 'managed-by=kcloud-tool' or
#      'app.kubernetes.io/managed-by=kcloud-tool', proving scoped selection
#   c) the output does NOT contain a blanket 'kubectl delete' without a label
#      selector or namespace qualifier
# ══════════════════════════════════════════════════════════════════════════════
if [[ ! -f "$INSTALLER" ]]; then
  _fail "7.cleanup-label-scoped" \
    "not found: $INSTALLER — will PASS once agent-A creates it"
else
  _cu_tmp=$(mktemp)
  # shellcheck disable=SC2064
  trap "rm -f '$_cu_tmp'" EXIT

  KUBECONFIG=/dev/null bash "$INSTALLER" \
    --cleanup --dry-run --node-ips "$SAMPLE_IPS" \
    >"$_cu_tmp" 2>&1 && _cu_rc=0 || _cu_rc=$?

  _cu_out=$(cat "$_cu_tmp")

  # Classify cluster-connection errors (non-fatal for this check).
  _cu_cluster_err=false
  if echo "$_cu_out" | grep -qiE \
    'connection refused|connection to the server|unable to connect|dial tcp|the server could not find|no configuration has been provided|invalid configuration|kubeconfig.*invalid|cluster not reachable|not reachable'; then
    _cu_cluster_err=true
  fi

  if [[ $_cu_rc -ne 0 ]] && [[ "$_cu_cluster_err" == "false" ]]; then
    _fail "7.cleanup-label-scoped" \
      "bash install_kcloud_stack.sh --cleanup --dry-run --node-ips '$SAMPLE_IPS'  → exited $_cu_rc"$'\n'"  Output snippet: ${_cu_out:0:400}"$'\n'"  Remediation: ensure --cleanup --dry-run does not error on a missing cluster; it should print the plan and exit 0."
  else
    # 7a: output must reference the managed-by label selector.
    if ! echo "$_cu_out" | grep -qE \
        '(managed-by=kcloud-tool|app\.kubernetes\.io/managed-by=kcloud-tool)'; then
      _fail "7.cleanup-label-scoped" \
        "--cleanup --dry-run output does not reference 'managed-by=kcloud-tool' label selector"$'\n'"  Output snippet: ${_cu_out:0:400}"$'\n'"  Remediation: --cleanup must use '-l app.kubernetes.io/managed-by=kcloud-tool' on all kubectl delete/helm uninstall calls."
    else
      # 7b: assert no blanket delete (kubectl delete without -l / --selector or -n).
      _blanket=false
      _blanket_line=""
      while IFS= read -r _cline; do
        # A kubectl delete line is blanket if it lacks any of: -l, --selector, -n, --namespace.
        if echo "$_cline" | grep -qE '\bkubectl delete\b'; then
          if ! echo "$_cline" | grep -qE '(-l |--selector|--label-selector|-n |--namespace|managed-by)'; then
            _blanket=true
            _blanket_line="$_cline"
            break
          fi
        fi
      done < "$_cu_tmp"

      if [[ "$_blanket" == "true" ]]; then
        _fail "7.cleanup-label-scoped" \
          "blanket kubectl delete (no label/namespace scope) detected: ${_blanket_line}"$'\n'"  Remediation: always add '-l app.kubernetes.io/managed-by=kcloud-tool' to cleanup kubectl delete commands."
      else
        _pass "7.cleanup-label-scoped"
      fi
      unset _blanket _blanket_line _cline
    fi
  fi

  rm -f "$_cu_tmp"
  trap - EXIT
  unset _cu_tmp _cu_out _cu_rc _cu_cluster_err
fi

# ══════════════════════════════════════════════════════════════════════════════
# SUMMARY TABLE
# ══════════════════════════════════════════════════════════════════════════════
_OVERALL=0

echo ""
printf "╔%s╗\n" "$(printf '═%.0s' {1..78})"
printf "║  %-74s  ║\n" "kcloud full-stack installer — validation summary"
printf "╠%s╣\n" "$(printf '═%.0s' {1..78})"
printf "║  %-58s  %-6s  ║\n" "CHECK" "RESULT"
printf "╠%s╣\n" "$(printf '═%.0s' {1..78})"

for _i in "${!CHECK_NAMES[@]}"; do
  _name="${CHECK_NAMES[$_i]}"
  _result="${CHECK_RESULTS[$_i]}"
  case "$_result" in
    PASS) _marker="✓" ;;
    FAIL) _marker="✗"; _OVERALL=1 ;;
    SKIP) _marker="─" ;;
    *)    _marker="?" ;;
  esac
  printf "║  %-58s  %s %-5s ║\n" "$_name" "$_marker" "$_result"
done

printf "╚%s╝\n" "$(printf '═%.0s' {1..78})"

# Print failure details beneath the table.
for _i in "${!CHECK_RESULTS[@]}"; do
  if [[ "${CHECK_RESULTS[$_i]}" == "FAIL" ]]; then
    echo ""
    echo "✗ FAIL: ${CHECK_NAMES[$_i]}"
    while IFS= read -r _detail_line; do
      echo "  ${_detail_line}"
    done <<< "${CHECK_DETAILS[$_i]}"
  fi
done

echo ""
if [[ "$_OVERALL" -eq 0 ]]; then
  echo "All checks PASS or SKIP — full-stack installer validation clean."
  exit 0
else
  echo "One or more checks FAILED — fix the issues above and re-run."
  exit 1
fi
