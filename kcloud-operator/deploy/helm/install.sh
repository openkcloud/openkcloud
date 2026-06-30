#!/usr/bin/env bash
# ============================================================
# install.sh: NPU Operator Helm 설치/업그레이드 래퍼
# 상세: deploy.env 를 로드하여 global.registry 를 주입한 뒤
#       helm upgrade --install 을 실행합니다.
#       --dry-run 등 CLI 인수는 helm 에 그대로 전달됩니다.
# 사용법:
#   bash install.sh            # 실제 설치
#   bash install.sh --dry-run  # helm dry-run (명령 미리보기 후 helm 이 렌더링만 수행)
# 생성일: 2026-06-02
# ============================================================
set -euo pipefail

# 스크립트 위치 기준 절대경로 확정
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/deploy.env"

# ── deploy.env 로드 ───────────────────────────────────────────
if [[ ! -f "${ENV_FILE}" ]]; then
  echo "ERROR: ${ENV_FILE} 없음" >&2
  echo "  → cp \"${SCRIPT_DIR}/deploy.env.example\" \"${ENV_FILE}\" 후 REGISTRY 를 설정하세요" >&2
  exit 1
fi

# shellcheck source=/dev/null
source "${ENV_FILE}"

if [[ -z "${REGISTRY:-}" ]]; then
  echo "ERROR: REGISTRY 가 비어 있습니다 — deploy.env 에 레지스트리 주소를 입력하세요" >&2
  exit 1
fi

# ── 변수 기본값 ───────────────────────────────────────────────
NAMESPACE="${NAMESPACE:-npu-operator}"
RELEASE="${RELEASE:-npu-operator}"
CHART="${CHART:-${SCRIPT_DIR}}"

# 상대경로이면 SCRIPT_DIR 기준 절대경로로 변환 (OCI ref 제외)
if [[ "${CHART}" != /* ]] && [[ "${CHART}" != *"://"* ]]; then
  CHART="${SCRIPT_DIR}/${CHART}"
fi

# ── helm 명령 구성 ────────────────────────────────────────────
HELM_CMD=(
  helm upgrade --install "${RELEASE}" "${CHART}"
  -n "${NAMESPACE}" --create-namespace
  --set "global.registry=${REGISTRY}"
)

# EXTRA_ARGS: 공백 구분 추가 인수를 배열로 분리 (word-split 의도적)
if [[ -n "${EXTRA_ARGS:-}" ]]; then
  read -ra _extra <<< "${EXTRA_ARGS}"
  HELM_CMD+=("${_extra[@]}")
fi

# CLI 인수 그대로 추가 (--dry-run 등)
HELM_CMD+=("$@")

# ── 실행 ─────────────────────────────────────────────────────
echo "▶ ${HELM_CMD[*]}"
exec "${HELM_CMD[@]}"
