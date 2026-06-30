#!/usr/bin/env bash
# scripts/validate_pilot_installer.sh
# Static + safe behavioral validation harness for the kcloud-mlperf pilot installer.
# NEVER mutates a live cluster. All checks are dry-run / read-only.
#
# Usage: validate_pilot_installer.sh [--help]
# Exit:  0 if all checks PASS or SKIP; non-zero if any FAIL.
set -Eeuo pipefail

# ── paths ──────────────────────────────────────────────────────────────────────
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${REPO_ROOT}/scripts/install_pilot_k8s.sh"
DETECT_LIB="${REPO_ROOT}/scripts/lib/detect.sh"
LOG_LIB="${REPO_ROOT}/scripts/lib/log.sh"
TEMPLATES_DIR="${REPO_ROOT}/deploy/templates"
SELF="${BASH_SOURCE[0]}"

# Sample node IPs matching the design contract (used for dry-run checks).
SAMPLE_IPS="192.0.2.11,192.0.2.12,192.0.2.13"

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
Usage: validate_pilot_installer.sh [--help|-h]

Static + safe behavioral checks for the kcloud-mlperf pilot installer.
Never mutates a live cluster. Safe to run at any time.

Checks performed:
  1  bash syntax (bash -n) on installer + libs + self
  2  shellcheck (SKIP if not on PATH; warning only)
  3  --help flag: exits 0, prints usage with --node-ips, runs with KUBECONFIG=/dev/null
  4  --dry-run: exits 0, prints plan, no mutating kubectl verb executed
  5  Template YAML render + validity (envsubst + python3 yaml.safe_load_all)
  6  Secret safety: canary HF_TOKEN value never appears in stdout/stderr
  7  git diff --check (whitespace/conflict markers; SKIP if tree clean or git absent)

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
# CHECK 1 — bash -n syntax on installer, libs, and self
# ══════════════════════════════════════════════════════════════════════════════
_check_bash_syntax() {
  local label="$1" path="$2"
  if [[ ! -f "$path" ]]; then
    _fail "$label" "not found: $path — teammate artifact missing; will PASS once created"
    return
  fi
  local out
  if out=$(bash -n "$path" 2>&1); then
    _pass "$label"
  else
    _fail "$label" "bash -n $path failed: ${out}"$'\n'"  Remediation: fix the syntax error reported above."
  fi
}

_check_bash_syntax "1a.syntax:install_pilot_k8s.sh"          "$INSTALLER"
_check_bash_syntax "1b.syntax:lib/detect.sh"                  "$DETECT_LIB"
_check_bash_syntax "1c.syntax:lib/log.sh"                     "$LOG_LIB"
_check_bash_syntax "1d.syntax:validate_pilot_installer.sh"    "$SELF"

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 2 — shellcheck (SKIP if absent; non-blocking warning)
# ══════════════════════════════════════════════════════════════════════════════
if ! command -v shellcheck &>/dev/null; then
  _skip "2.shellcheck" "shellcheck not on PATH — install: sudo apt-get install shellcheck"
else
  _sc_targets=()
  for _f in "$INSTALLER" "$DETECT_LIB" "$LOG_LIB" "$SELF"; do
    [[ -f "$_f" ]] && _sc_targets+=("$_f")
  done
  if [[ ${#_sc_targets[@]} -eq 0 ]]; then
    _skip "2.shellcheck" "no target files exist yet (teammates still writing)"
  else
    _sc_out=$(shellcheck --severity=warning "${_sc_targets[@]}" 2>&1) && _sc_rc=0 || _sc_rc=$?
    if [[ $_sc_rc -eq 0 ]]; then
      _pass "2.shellcheck"
    else
      _fail "2.shellcheck" "${_sc_out:0:600}"$'\n'"  Remediation: fix shellcheck warnings listed above."
    fi
  fi
  unset _sc_targets _sc_out _sc_rc _f
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 3 — --help exits 0, output contains --node-ips, no cluster call
# ══════════════════════════════════════════════════════════════════════════════
if [[ ! -f "$INSTALLER" ]]; then
  _fail "3.help-flag" "not found: $INSTALLER — will PASS once worker-installer creates it"
else
  _help_out=$(KUBECONFIG=/dev/null bash "$INSTALLER" --help 2>&1) && _help_rc=0 || _help_rc=$?
  if [[ $_help_rc -ne 0 ]]; then
    _fail "3.help-flag" \
      "KUBECONFIG=/dev/null bash install_pilot_k8s.sh --help  → exited $_help_rc (expected 0)"$'\n'"  Output snippet: ${_help_out:0:300}"$'\n'"  Remediation: ensure --help is handled before any cluster access, before set -e triggers."
  elif ! echo "$_help_out" | grep -q -- '--node-ips'; then
    _fail "3.help-flag" \
      "--help output does not contain '--node-ips'."$'\n'"  Output snippet: ${_help_out:0:400}"$'\n'"  Remediation: add --node-ips to the usage/help text."
  else
    _pass "3.help-flag"
  fi
  unset _help_out _help_rc
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 4 — --dry-run: exits 0, prints plan, NO mutating kubectl verb executed
#
# Run with KUBECONFIG=/dev/null so no real cluster is needed.
# A connection/kubeconfig error from kubectl calls is acceptable for this check
# (it proves the cluster was attempted in read-only fashion); we distinguish it
# from a real script error.  The core assertion is that no mutating verb was
# executed — grep the combined output for unguarded kubectl apply/create/delete/
# patch/replace lines.
# ══════════════════════════════════════════════════════════════════════════════
if [[ ! -f "$INSTALLER" ]]; then
  _fail "4.dry-run-no-mutation" "not found: $INSTALLER — will PASS once worker-installer creates it"
else
  _dr_tmp=$(mktemp)
  # shellcheck disable=SC2064
  trap "rm -f '$_dr_tmp'" EXIT

  KUBECONFIG=/dev/null bash "$INSTALLER" \
    --dry-run --node-ips "$SAMPLE_IPS" \
    >"$_dr_tmp" 2>&1 && _dr_rc=0 || _dr_rc=$?

  _dr_out=$(cat "$_dr_tmp")

  # Classify failures: cluster-connection errors are acceptable (no mutation happened).
  _cluster_err=false
  if echo "$_dr_out" | grep -qiE 'connection refused|connection to the server|unable to connect|dial tcp|the server could not find|no configuration has been provided|invalid configuration|kubeconfig.*invalid|cluster not reachable|not reachable'; then
    _cluster_err=true
  fi

  if [[ $_dr_rc -ne 0 ]] && [[ "$_cluster_err" == "false" ]]; then
    _fail "4.dry-run-no-mutation" \
      "KUBECONFIG=/dev/null bash install_pilot_k8s.sh --dry-run --node-ips '$SAMPLE_IPS'  → exited $_dr_rc"$'\n'"  Output snippet: ${_dr_out:0:400}"$'\n'"  Remediation: ensure --dry-run path does not call exit-on-error operations that require a live cluster."
  else
    # Assert no mutating kubectl verb was executed without --dry-run=client guard.
    _mutation_found=false
    _mutation_detail=""
    while IFS= read -r _line; do
      # kubectl create / delete / patch / replace are never OK in dry-run mode.
      if echo "$_line" | grep -qE 'kubectl (create|delete|patch|replace)\b'; then
        _mutation_found=true
        _mutation_detail="mutating kubectl verb detected: $_line"
        break
      fi
      # kubectl apply without --dry-run=client is a mutation.
      if echo "$_line" | grep -qE 'kubectl apply\b' && ! echo "$_line" | grep -q -- '--dry-run'; then
        _mutation_found=true
        _mutation_detail="kubectl apply without --dry-run=client detected: $_line"
        break
      fi
    done < "$_dr_tmp"

    if [[ "$_mutation_found" == "true" ]]; then
      _fail "4.dry-run-no-mutation" \
        "$_mutation_detail"$'\n'"  Remediation: in --dry-run path always pass --dry-run=client to kubectl apply; never call create/delete/patch/replace."
    else
      _pass "4.dry-run-no-mutation"
    fi
    unset _mutation_found _mutation_detail _line
  fi

  rm -f "$_dr_tmp"
  trap - EXIT
  unset _dr_tmp _dr_out _dr_rc _cluster_err
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 5 — template YAML render + validity via envsubst + python3
#
# Export dummy values for ALL frozen variables so envsubst has no unresolved
# placeholders.  For each *.yaml in deploy/templates/: envsubst → yaml.safe_load_all.
# Missing templates directory or empty templates → FAIL (not just skip).
# ══════════════════════════════════════════════════════════════════════════════

# Export dummy values for every frozen variable (names are FROZEN per contract).
export KCLOUD_NAMESPACE="kcloud-mlperf-test"
export RELEASE="kcloud-mlperf"
export SA_NAME="kcloud-mlperf-sa"
export HF_SECRET_NAME="huggingface-token"
export HF_TOKEN_B64="dGVzdC10b2tlbg=="          # base64("test-token") — not a real secret
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
export MANAGED_BY="kcloud-tool"
export PART_OF="kcloud-mlperf"

if [[ ! -d "$TEMPLATES_DIR" ]]; then
  _fail "5.template-yaml-render" \
    "not found: $TEMPLATES_DIR — worker-manifests has not created the templates directory yet"$'\n'"  Remediation: wait for worker-manifests to create deploy/templates/."
else
  # Collect templates (nullglob so empty dir gives empty array)
  _tpls=()
  while IFS= read -r -d '' _t; do
    _tpls+=("$_t")
  done < <(find "$TEMPLATES_DIR" -maxdepth 1 -name '*.yaml' -print0 | sort -z)

  if [[ ${#_tpls[@]} -eq 0 ]]; then
    _fail "5.template-yaml-render" \
      "no *.yaml files found in $TEMPLATES_DIR — worker-manifests has not created templates yet"$'\n'"  Remediation: wait for worker-manifests to populate deploy/templates/."
  else
    for _tpl in "${_tpls[@]}"; do
      _tname="$(basename "$_tpl")"
      _rendered=$(envsubst < "$_tpl" 2>&1) && _subst_rc=0 || _subst_rc=$?
      if [[ $_subst_rc -ne 0 ]]; then
        _fail "5.template-yaml-render:${_tname}" \
          "envsubst failed on $_tpl: $_rendered"$'\n'"  Remediation: check template file for syntax errors."
        continue
      fi
      _yaml_err=$(echo "$_rendered" | python3 -c \
        'import sys,yaml;list(yaml.safe_load_all(sys.stdin))' 2>&1) && _yaml_rc=0 || _yaml_rc=$?
      if [[ $_yaml_rc -ne 0 ]]; then
        _fail "5.template-yaml-render:${_tname}" \
          "invalid YAML after envsubst: $_yaml_err"$'\n'"  Remediation: fix YAML syntax in $_tpl."
      else
        _pass "5.template-yaml-render:${_tname}"
      fi
    done
    unset _tpl _tname _rendered _subst_rc _yaml_err _yaml_rc
  fi
  unset _tpls _t
fi

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 6 — secret safety: canary token must never appear in stdout/stderr
#
# Plant HF_TOKEN=CANARY-DO-NOT-LOG through the dry-run path and assert the
# literal string never leaks into any output.
# ══════════════════════════════════════════════════════════════════════════════
_CANARY="CANARY-DO-NOT-LOG"
if [[ ! -f "$INSTALLER" ]]; then
  _fail "6.secret-safety" "not found: $INSTALLER — will PASS once worker-installer creates it"
else
  _canary_out=$(HF_TOKEN="$_CANARY" KUBECONFIG=/dev/null bash "$INSTALLER" \
    --dry-run --node-ips "$SAMPLE_IPS" 2>&1 || true)

  if echo "$_canary_out" | grep -qF "$_CANARY"; then
    _leaking=$(echo "$_canary_out" | grep -nF "$_CANARY" | head -5)
    _fail "6.secret-safety" \
      "Canary token '${_CANARY}' found in output:"$'\n'"  $_leaking"$'\n'"  Remediation: replace HF_TOKEN value with '***' in all log/echo/printf calls in the installer."
  else
    _pass "6.secret-safety"
  fi
  unset _canary_out _leaking
fi
unset _CANARY

# ══════════════════════════════════════════════════════════════════════════════
# CHECK 7 — git diff --check (whitespace errors / conflict markers)
# Only runs when git is available AND the working tree is dirty.
# ══════════════════════════════════════════════════════════════════════════════
if ! command -v git &>/dev/null; then
  _skip "7.git-diff-check" "git not on PATH"
elif ! git -C "$REPO_ROOT" rev-parse --git-dir &>/dev/null 2>&1; then
  _skip "7.git-diff-check" "not inside a git repository"
else
  _git_status=$(git -C "$REPO_ROOT" status --porcelain 2>/dev/null || true)
  if [[ -z "$_git_status" ]]; then
    _skip "7.git-diff-check" "working tree is clean"
  else
    _gc_out=$(git -C "$REPO_ROOT" diff --check 2>&1) && _gc_rc=0 || _gc_rc=$?
    if [[ $_gc_rc -eq 0 ]]; then
      _pass "7.git-diff-check"
    else
      _fail "7.git-diff-check" \
        "whitespace errors or conflict markers found:"$'\n'"  ${_gc_out:0:600}"$'\n'"  Remediation: remove trailing whitespace / resolve conflict markers, then re-stage."
    fi
    unset _gc_out _gc_rc
  fi
  unset _git_status
fi

# ══════════════════════════════════════════════════════════════════════════════
# SUMMARY TABLE
# ══════════════════════════════════════════════════════════════════════════════
_OVERALL=0

echo ""
printf "╔%s╗\n" "$(printf '═%.0s' {1..76})"
printf "║  %-72s  ║\n" "kcloud-mlperf pilot installer — validation summary"
printf "╠%s╣\n" "$(printf '═%.0s' {1..76})"
printf "║  %-56s  %-6s  ║\n" "CHECK" "RESULT"
printf "╠%s╣\n" "$(printf '═%.0s' {1..76})"

for _i in "${!CHECK_NAMES[@]}"; do
  _name="${CHECK_NAMES[$_i]}"
  _result="${CHECK_RESULTS[$_i]}"
  case "$_result" in
    PASS) _marker="✓" ;;
    FAIL) _marker="✗"; _OVERALL=1 ;;
    SKIP) _marker="─" ;;
    *)    _marker="?" ;;
  esac
  printf "║  %-56s  %s %-5s ║\n" "$_name" "$_marker" "$_result"
done

printf "╚%s╝\n" "$(printf '═%.0s' {1..76})"

# Print failure details beneath the table.
for _i in "${!CHECK_RESULTS[@]}"; do
  if [[ "${CHECK_RESULTS[$_i]}" == "FAIL" ]]; then
    echo ""
    echo "✗ FAIL: ${CHECK_NAMES[$_i]}"
    # Print each detail line indented.
    while IFS= read -r _detail_line; do
      echo "  ${_detail_line}"
    done <<< "${CHECK_DETAILS[$_i]}"
  fi
done

echo ""
if [[ "$_OVERALL" -eq 0 ]]; then
  echo "All checks PASS or SKIP — installer validation clean."
  exit 0
else
  echo "One or more checks FAILED — fix the issues above and re-run."
  exit 1
fi
