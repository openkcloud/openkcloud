#!/usr/bin/env bash
# ============================================================
# third-party-notices-images.sh: 이 저장소의 THIRD_PARTY_NOTICES.txt 를 만들거나 검사한다
# 상세: 모듈별 인자는 각 Dockerfile 의 go build 대상(furiosa-exporter 는 smi 태그)과 맞춘다.
#       인자 없이 실행하면 다시 만들고, --check 는 파일이 현재 의존성과 같은지만 본다.
# 생성일: 2026-10-13 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."

specs=(
  furiosa-exporter:.:smi
  generic-device-plugin:.
  generic-dra-driver:.
  node-device-detector:.
  tenstorrent-device-plugin:.
)

case "${1:-}" in
  "") bash hack/third-party-notices.sh --write THIRD_PARTY_NOTICES.txt "${specs[@]}" ;;
  --check) bash hack/third-party-notices.sh --check THIRD_PARTY_NOTICES.txt "${specs[@]}" ;;
  *) echo "용법: $0 [--check]" >&2; exit 2 ;;
esac
