#!/usr/bin/env bash
# ============================================================
# prebake-guard_test.sh: 기본 빌드가 NVIDIA 드라이버 패키지를 이미지에 넣지 않는지 정적으로 고정
# 상세: Dockerfile.v17 의 prebake RUN 이 PREBAKE_DRIVER=true 조건 안에 있고 기본값이 false 인지,
#       Makefile 이 그 인자를 넘기는지 본다. docker 없이 돈다.
# 생성일: 2026-10-12 | 수정일: 2026-10-12
# ============================================================
set -uo pipefail
cd "$(dirname "$0")"
fail=0
grep -qE '^ARG PREBAKE_DRIVER="?false"?$' Dockerfile.v17 || { echo "FAIL: ARG PREBAKE_DRIVER=false 가 없다"; fail=1; }
# prebake 설치 줄은 조건문 뒤에만 나와야 한다.
cond=$(grep -nE 'if \[ "\$\{PREBAKE_DRIVER\}" = "true" \]' Dockerfile.v17 | head -1 | cut -d: -f1)
inst=$(grep -nE 'nvidia-driver-\$\{NVIDIA_DRIVER_MAJOR\}-server-open' Dockerfile.v17 | head -1 | cut -d: -f1)
[ -n "$cond" ] && [ -n "$inst" ] && [ "$cond" -lt "$inst" ] || { echo "FAIL: prebake 설치 줄($inst)이 PREBAKE_DRIVER 조건($cond) 밖이다"; fail=1; }
grep -qE '^PREBAKE_DRIVER +\?= +false$' Makefile || { echo "FAIL: Makefile 기본 PREBAKE_DRIVER ?= false 가 없다"; fail=1; }
[ "$(grep -c -- '--build-arg PREBAKE_DRIVER=\$(PREBAKE_DRIVER)' Makefile)" -ge 2 ] || { echo "FAIL: build·build-precompiled 가 PREBAKE_DRIVER 인자를 넘기지 않는다"; fail=1; }
[ "$fail" -eq 0 ] && echo "PASS: prebake-guard" || exit 1
