#!/usr/bin/env bash
# ============================================================
# build-tools-guard_test.sh: 기본 빌드가 컴파일 도구 레이어를 넣지 않는지 정적으로 고정
# 상세: 활성 설치 경로는 컴파일 도구를 nsenter 로 호스트에서 실행한다. 도구 설치가 BUILD_TOOLS=true
#       조건 안에 있고 기본값이 false 인지, 사전 컴파일 빌드가 binutils 와 PPA 를 직접 설치하는지
#       본다. docker 없이 돈다.
# 생성일: 2026-10-13 | 수정일: 2026-10-13
# ============================================================
set -uo pipefail
cd "$(dirname "$0")" || exit 1
fail=0
grep -qE '^ARG BUILD_TOOLS="?false"?$' Dockerfile.v17 || { echo "FAIL: ARG BUILD_TOOLS=false 가 없다"; fail=1; }
cond=$(grep -nE 'if \[ "\$\{BUILD_TOOLS\}" = "true" \]' Dockerfile.v17 | head -1 | cut -d: -f1)
# 주석 줄은 건너뛴다. 조건 앞 주석에 dkms 낱말이 있어도 설치 줄로 오인하지 않는다.
dkms=$(grep -nE '^[^#]*[[:space:]]dkms([[:space:]]|\\|$)' Dockerfile.v17 | head -1 | cut -d: -f1)
[[ -n "$cond" && -n "$dkms" && "$dkms" -gt "$cond" ]] || { echo "FAIL: dkms 설치가 BUILD_TOOLS 조건 밖에 있다(cond=$cond dkms=$dkms)"; fail=1; }
bu=$(grep -nE '^[^#]*apt-get install[^#]*binutils' Dockerfile.v17 | head -1 | cut -d: -f1)
build=$(grep -nE '^[^#]*sh \./BUILD' Dockerfile.v17 | head -1 | cut -d: -f1)
[[ -n "$bu" && -n "$build" && "$bu" -lt "$build" ]] || { echo "FAIL: 사전 컴파일 빌드가 binutils 를 먼저 설치하지 않는다(bu=$bu build=$build)"; fail=1; }
# 사전 컴파일 RUN 의 사용자 공간 내려받기는 PPA 에만 있는 버전을 고정한다. 기본 빌드가 PPA 를
# 넣지 않으므로 그 RUN 안에서 add-apt-repository 가 download 보다 먼저 와야 한다.
pcond=$(grep -nE 'if \[ -n "\$\{PREBUILD_KERNEL\}" \]' Dockerfile.v17 | head -1 | cut -d: -f1)
ppa=$(grep -nE '^[^#]*add-apt-repository[^#]*ppa:graphics-drivers/ppa' Dockerfile.v17 | tail -1 | cut -d: -f1)
dl=$(grep -nE '^[^#]*apt-get download' Dockerfile.v17 | head -1 | cut -d: -f1)
[[ -n "$pcond" && -n "$ppa" && -n "$dl" && "$pcond" -lt "$ppa" && "$ppa" -lt "$dl" ]] || { echo "FAIL: 사전 컴파일 RUN 이 download 전에 PPA 를 추가하지 않는다(pcond=$pcond ppa=$ppa download=$dl)"; fail=1; }
grep -qE '^BUILD_TOOLS +\?= +false$' Makefile || { echo "FAIL: Makefile 기본값 BUILD_TOOLS ?= false 가 없다"; fail=1; }
[[ "$(grep -c -- '--build-arg BUILD_TOOLS' Makefile)" -ge 2 ]] || { echo "FAIL: build·build-precompiled 가 BUILD_TOOLS 를 넘기지 않는다"; fail=1; }
[[ "$fail" -eq 0 ]] && echo "build-tools-guard: OK"
exit "$fail"
