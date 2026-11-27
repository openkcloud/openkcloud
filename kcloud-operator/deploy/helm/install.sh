#!/usr/bin/env bash
# ============================================================
# install.sh: NPU Operator Helm 설치/업그레이드 래퍼
# 상세: deploy.env 를 로드해 global.registry/global.vendorRegistry 를 주입한 뒤 helm
#       upgrade --install 을 실행한다. --dry-run 등 CLI 인수는 helm 에 그대로 전달한다.
# 생성일: 2026-06-02 | 수정일: 2026-10-11
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
# VENDOR_REGISTRY: 벤더(3rd-party) 이미지를 당길 미러. 미지정이면 REGISTRY 와 같은
# 곳으로 본다 — 사설 미러 하나에 전부 올려 두는 것이 기존 배포 방식이기 때문이다.
# 벤더 이미지만 인터넷에서 직접 받으려면 deploy.env 에 VENDOR_REGISTRY="" 를 명시한다.
VENDOR_REGISTRY="${VENDOR_REGISTRY-${REGISTRY}}"
NAMESPACE="${NAMESPACE:-kcloud}"
RELEASE="${RELEASE:-kcloud-operator}"
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
  --set "global.vendorRegistry=${VENDOR_REGISTRY}"
)

# 개발 클러스터 오버라이드 자동 적용: 로컬 values-dev.yaml 이 있으면 -f 로 병합한다(저장소에는 values-dev.example.yaml 만 있다).
# (dev 배포 경로 한정 — 차트 기본값 values.yaml 은 외부 소비자용으로 유지). nvidia toolkit
# 등 이 클러스터에만 필요한 설정을 --set 대신 커밋된 values 로 영속시켜 upgrade 승계를 보장.
if [[ -f "${SCRIPT_DIR}/values-dev.yaml" ]]; then
  HELM_CMD+=(-f "${SCRIPT_DIR}/values-dev.yaml")
fi

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
