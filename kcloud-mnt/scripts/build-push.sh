#!/usr/bin/env bash
# openKcloud 이미지 빌드·푸시 (master3 등 빌드 머신에서 실행)
#
# 사용:
#   ./scripts/build-push.sh                 # core iaas paas obs ainb app 전부
#   ./scripts/build-push.sh core app        # 일부만
#   PUSH=0 ./scripts/build-push.sh core     # 빌드만(푸시 안 함, 로컬 이미지 남김)
#   TAG=mytag ./scripts/build-push.sh       # 태그 지정(기본: git short sha)
#   HARBOR_USER=admin HARBOR_PASS=... ./scripts/build-push.sh   # 먼저 로그인까지
#   * 푸시가 끝난 이미지는 로컬에서 지운다(rmi) — 빌드 캐시는 남아 다음 빌드 속도는 그대로
#
# 환경변수(기본값):
#   REGISTRY=harbor.io:7443/mnt   TAG=<git short sha>   PUSH=1
#   ENGINE=<자동: docker > nerdctl>   PLATFORM=linux/amd64
set -euo pipefail
cd "$(dirname "$0")/.."   # 레포 루트

REGISTRY="${REGISTRY:-harbor.io:7443/mnt}"
TAG="${TAG:-$(git rev-parse --short HEAD)}"
PUSH="${PUSH:-1}"
PLATFORM="${PLATFORM:-linux/amd64}"

# 빌드 엔진 자동탐지
if [ -z "${ENGINE:-}" ]; then
  if command -v docker  >/dev/null 2>&1; then ENGINE="docker"
  elif command -v nerdctl >/dev/null 2>&1; then ENGINE="nerdctl"
  else echo "ERROR: docker/nerdctl 둘 다 없음. ENGINE=... 로 지정하세요." >&2; exit 1
  fi
fi

# 모듈 → 빌드 컨텍스트 디렉터리
declare -A DIR=( [core]=MNT-CORE [iaas]=MNT-IAAS [paas]=MNT-PAAS [obs]=MNT-OBS [ainb]=MNT-AINB [app]=MNT-APP )

MODULES=("$@")
if [ "${#MODULES[@]}" -eq 0 ]; then MODULES=(core iaas paas obs ainb app); fi

# 선택적 로그인 (HARBOR_USER/HARBOR_PASS 주어지면)
if [ -n "${HARBOR_USER:-}" ]; then
  echo ">> login ${REGISTRY%%/*}"
  echo "${HARBOR_PASS:-}" | "$ENGINE" login "${REGISTRY%%/*}" -u "$HARBOR_USER" --password-stdin
fi

echo ">> ENGINE=$ENGINE REGISTRY=$REGISTRY TAG=$TAG PUSH=$PUSH PLATFORM=$PLATFORM"
echo ">> 모듈: ${MODULES[*]}"

for m in "${MODULES[@]}"; do
  d="${DIR[$m]:-}"
  if [ -z "$d" ]; then echo "ERROR: 알 수 없는 모듈 '$m' (core|iaas|paas|obs|ainb|app)" >&2; exit 1; fi
  img="$REGISTRY/mnt-$m:$TAG"
  echo "==> build  $img   (context: $d)"
  "$ENGINE" build --platform "$PLATFORM" -t "$img" "$d"
  if [ "$PUSH" = "1" ]; then
    echo "==> push   $img"
    "$ENGINE" push "$img"
    echo "==> rmi    $img"
    "$ENGINE" rmi "$img" >/dev/null
  fi
done

echo ""
echo "완료. TAG=$TAG"
echo "배포:"
echo "  helm upgrade --install openkcloud-portal charts/openkcloud-portal \\"
echo "    -n openkcloud-mnt --set global.image.tag=$TAG"
