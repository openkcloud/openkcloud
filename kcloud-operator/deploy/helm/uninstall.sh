#!/usr/bin/env bash
# ============================================================
# uninstall.sh: NPU Operator Helm 제거 래퍼
# 상세: tester.md §3 순서대로 안전하게 제거합니다.
#       순서: CR 삭제(finalizer 처리) → orphan driver DS 정리
#             → helm uninstall → 고착 CR finalizer strip → (선택) CRD 삭제
#       레거시(npu-op-*) + 신규(kcloud-*) 양쪽 리소스를 모두 처리합니다.
# 사용법:
#   bash uninstall.sh              # 일반 제거 (CRD 유지)
#   bash uninstall.sh --purge-crds # CRD 까지 완전 삭제
# 생성일: 2026-06-02
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── --purge-crds 플래그 감지 ──────────────────────────────────
PURGE_CRDS=false
for _arg in "$@"; do
  [[ "${_arg}" == "--purge-crds" ]] && PURGE_CRDS=true
done

# ── deploy.env 로드 (선택적) ──────────────────────────────────
ENV_FILE="${SCRIPT_DIR}/deploy.env"
if [[ -f "${ENV_FILE}" ]]; then
  # shellcheck source=/dev/null
  source "${ENV_FILE}"
fi

NAMESPACE="${NAMESPACE:-npu-operator}"
RELEASE="${RELEASE:-npu-operator}"

echo "========================================================"
echo " NPU Operator 제거: RELEASE=${RELEASE}  NS=${NAMESPACE}"
echo "========================================================"

# ── [1/4] CR 먼저 삭제 ───────────────────────────────────────
# ⚠️ 순서 중요: operator 가 finalizer(npu.ai/cleanup)를 처리해야
#   device-plugin/detector DS 가 정리됩니다.
#   operator 보다 먼저 CR 을 삭제해야 합니다.
echo ""
echo "=== [1/4] CR 삭제 (operator finalizer 처리 대기) ==="
kubectl delete npuclusterpolicy \
  --all -n "${NAMESPACE}" \
  --ignore-not-found --timeout=120s || true
kubectl delete driverinstallpolicy \
  --all \
  --ignore-not-found --timeout=120s || true

# ── [2/4] orphan driver DS 정리 ──────────────────────────────
# driver DS 는 DIP 삭제 시 ownerReference 로 cascade 삭제되지만
# 구버전(pre-v0.5.22) 또는 ownerRef 미부여 환경에서는 orphan 으로 남을 수 있음.
# label 기반 + 이름 기반(레거시·신규) 양쪽 처리.
echo ""
echo "=== [2/4] orphan driver DS 정리 ==="

# label 기반: v0.5.23+ kcloud-* driver DS (app.kubernetes.io/component=driver)
kubectl delete ds -n kube-system \
  -l app.kubernetes.io/component=driver \
  --ignore-not-found || true

# 이름 기반 — 레거시(npu-op-*) v0.5.7~v0.5.22 driver DS
kubectl delete ds -n kube-system --ignore-not-found \
  npu-op-driver-nvidia-generic \
  npu-op-driver-furiosa-warboy \
  npu-op-driver-furiosa-rngd || true

# 이름 기반 — 신규(kcloud-*) v0.5.23+ driver DS
kubectl delete ds -n kube-system --ignore-not-found \
  kcloud-nvidia-driver \
  kcloud-furiosa-warboy-driver \
  kcloud-furiosa-rngd-driver || true

# ── [3/4] Helm uninstall ─────────────────────────────────────
echo ""
echo "=== [3/4] Helm uninstall ==="
if helm status "${RELEASE}" -n "${NAMESPACE}" &>/dev/null; then
  helm uninstall "${RELEASE}" -n "${NAMESPACE}"
else
  echo "WARN: 릴리스 '${RELEASE}' 를 찾을 수 없습니다 — skip"
fi

# ── [4/4] 고착 CR finalizer 제거 (fallback) ─────────────────
# operator 가 이미 없는 상태에서 CR 이 Terminating 고착 시 수동 strip.
echo ""
echo "=== [4/4] 고착 CR finalizer 제거 (fallback) ==="
while IFS= read -r _cr; do
  [[ -z "${_cr}" ]] && continue
  echo "  finalizer 제거: ${_cr}"
  kubectl patch "${_cr}" -n "${NAMESPACE}" \
    -p '{"metadata":{"finalizers":[]}}' --type=merge 2>/dev/null || true
done < <(kubectl get npuclusterpolicy -n "${NAMESPACE}" -o name 2>/dev/null || true)

# ── [선택] CRD 삭제 ──────────────────────────────────────────
# Helm 은 설계상 crds/ 를 자동 삭제하지 않습니다.
# --purge-crds 가 지정된 경우에만 삭제합니다.
if [[ "${PURGE_CRDS}" == "true" ]]; then
  echo ""
  echo "=== [--purge-crds] CRD 삭제 ==="
  kubectl delete crd \
    npuclusterpolicies.npu.ai \
    driverinstallpolicies.npu.ai \
    driverupgradestates.npu.ai \
    nodedevicereports.npu.ai \
    --ignore-not-found || true
fi

echo ""
echo "✅ uninstall 완료 (RELEASE=${RELEASE}, NS=${NAMESPACE})"
