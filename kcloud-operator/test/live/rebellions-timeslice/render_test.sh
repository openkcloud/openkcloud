#!/usr/bin/env bash
# ============================================================
# render_test.sh: run.sh 의 render_pod 가 세 파드 매니페스트의 env 를 변수 값으로 채우는지 확인
# 상세: 클러스터 미접속. run.sh 에서 render_pod 함수만 추출해 실행하고 렌더 결과를 grep 한다.
# 생성일: 2026-09-30
# ============================================================
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MATMUL_DIM=1024 DURATION=7 DEVICE_ID=1 RBLN_NPU_NAME=RBLN-TEST CACHE_FILE=matmul-1024-RBLN-TEST.rbln
# shellcheck disable=SC1090
source <(sed -n '/^render_pod()/,/^}/p' "$HERE/run.sh")
declare -F render_pod >/dev/null || { echo "FAIL: render_pod 가 run.sh 에 없다"; exit 1; }
fail=0
value_of() { grep -A1 "name: $2\$" "$1" | sed -n 's/.*value: *"\{0,1\}\([^"#]*\)"\{0,1\}.*/\1/p' | head -1 | sed 's/ *$//'; }
for f in pod-solo.yaml pod-a.yaml pod-b.yaml; do
  if [ "$f" = pod-solo.yaml ]; then out="$(render_pod "$HERE/$f")"; else out="$(render_pod "$HERE/$f" 1700000000)"; fi
  for pair in "MATMUL_DIM=1024" "DURATION=7" "DEVICE_ID=1" "RBLN_NPU_NAME=RBLN-TEST" "RBLN_COMPILED_PATH=/models/matmul-1024-RBLN-TEST.rbln"; do
    name="${pair%%=*}"; want="${pair#*=}"; got="$(value_of "$out" "$name")"
    [ "$got" = "$want" ] || { echo "FAIL $f $name: got '$got' want '$want'"; fail=1; }
  done
  if [ "$f" != pod-solo.yaml ]; then
    got="$(value_of "$out" START_AT)"; [ "$got" = "1700000000" ] || { echo "FAIL $f START_AT: got '$got'"; fail=1; }
  fi
  rm -f "$out"
done
[ "$fail" = 0 ] && echo "render_test: PASS"
exit "$fail"
