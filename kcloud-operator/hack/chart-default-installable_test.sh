#!/usr/bin/env bash
# ============================================================
# chart-default-installable_test.sh: chart-default-installable.sh 의 판정을 시험한다
# 상세: 기본 values.yaml 로 돌리면 OK 를 보고하는지, 기본 렌더에는 없어야 할
#       사이트별 kernelImages 가 섞인 values 로 돌리면 실패로 잡는지 확인한다.
# 생성일: 2026-09-26
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."
rc=0
out="$(bash hack/chart-default-installable.sh 2>&1)" || rc=$?
if [ "${rc}" -eq 0 ] && [[ "${out}" == *"chart-default-installable: OK"* ]]; then
  echo "PASS: 기본값 검사가 통과를 보고한다"
else
  echo "FAIL: 기본값에서 통과를 보고하지 않았다(rc=${rc}): ${out}"; exit 1
fi
# 기본 렌더에 커널 이미지가 있으면 실패해야 한다
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
sed 's/^      kernelImages: {}$/      kernelImages: {"x":"y"}/' deploy/helm/values.yaml > "$tmp"
if VALUES="$tmp" bash hack/chart-default-installable.sh >/dev/null 2>&1; then
  echo "FAIL: 기본 렌더에 커널 이미지가 있는데 통과했다"; exit 1
else
  echo "PASS: 기본 렌더의 커널 이미지를 잡는다"
fi
