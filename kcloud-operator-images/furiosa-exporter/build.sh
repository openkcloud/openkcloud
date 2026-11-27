#!/usr/bin/env bash
# ============================================================
# build.sh: Furiosa exporter 이미지 빌드/푸시
# 상세: 벤더 apt 에서 furiosa-libsmi 를 빌드 단계에서만 받아 이미지에는 담지 않는다. 이미 있는
#       태그면 멈춘다(재사용 금지). REGISTRY 환경변수로 자체 미러를 쓸 수 있다.
# 생성일: 2026-08-07 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

REGISTRY="${REGISTRY:-ghcr.io/openkcloud}"
REPO="${REPO:-furiosa-exporter}"
TAG="${TAG:-0.1.0}"
IMAGE="${REGISTRY}/${REPO}:${TAG}"

cd "$(dirname "$0")"

# --insecure 가 없으면 실재하는 태그도 없는 것으로 보인다(HTTP 레지스트리).
if sudo docker manifest inspect --insecure "$IMAGE" >/dev/null 2>&1; then
	echo "!! ${IMAGE} 가 이미 존재한다. TAG 를 올려라." >&2
	exit 1
fi

echo ">> building ${IMAGE}"
sudo docker build -t "$IMAGE" .

if [ "${PUSH:-false}" = "true" ]; then
	echo ">> pushing ${IMAGE}"
	sudo docker push "$IMAGE"
fi
echo ">> done: ${IMAGE}"
