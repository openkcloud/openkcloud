#!/usr/bin/env bash
# ============================================================
# userspace-deps-check_test.sh: 굽힌 사용자 공간 .deb 의 의존 추출·host 조회 방향 시험
# 상세: 방향 시험은 docker 없이 돈다. 의존 추출 시험은 로컬 이미지(sudo docker 필요)에서
#       fixture 를 직접 뽑고, 이미지가 없으면 SKIP(비0 종료)한다.
# 생성일: 2026-09-26 | 수정일: 2026-10-11
# ============================================================
set -uo pipefail

IMG="${IMG:-ghcr.io/openkcloud/nvidia-driver-ds:580.126.09-k5.15.0-171-generic-v2}"
KERNEL="${KERNEL:-5.15.0-171-generic}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

. ./userspace-deps-check.sh

# --- 소스 고정: host 설치 상태 조회가 ns 를 거치는지 ---
# userspace_missing_host_deps 는 host 로 dpkg -i 될 .deb 의 의존이 host 에 있는지 봐야 한다.
# 맨 dpkg-query 는 컨테이너를 조회해 host 에 없는 의존을 있다고 오판한다. 그래서 상태 조회는
# ns 를 거친다(dpkg-deb 는 컨테이너 로컬 .deb 를 읽으므로 대상이 아니다).
if grep -qE '^[[:space:]]*dpkg-query ' userspace-deps-check.sh; then
  echo "FAIL: ns 를 거치지 않는 dpkg-query 호출이 있다 — host 가 아니라 컨테이너를 조회한다"; exit 1
fi
if grep -q 'ns "dpkg-query' userspace-deps-check.sh; then
  echo "PASS: host 설치 상태 조회가 ns 를 거친다"
else
  echo 'FAIL: host 설치 상태 조회(ns "dpkg-query)가 없다'; exit 1
fi

# --- 방향 고정: host 에 있는 의존은 빠지고 없는 의존은 남는지 ---
# 시험은 컨테이너 밖에서 돈다. ns 를 로컬 조회로 명시해 "이 시험은 로컬을 host 로 본다"를
# 코드로 드러낸다 — 소스는 ns 부재 시 조용히 로컬로 되돌아가지 않는다. userspace_deb_depends
# 를 고정 목록으로 덮어 방향만 본다. 하위 도커 시험이 실제 함수를 쓰도록 subshell 로 가둔다.
(
  ns() { bash -lc "$*"; }
  userspace_deb_depends() { printf '%s\n' libc6 nvidia-nonexistent-dep-xyz; }
  miss="$(userspace_missing_host_deps /ignored)"
  case "$miss" in
    *libc6*) echo "FAIL: host 에 설치된 libc6 을 missing 으로 냈다: $miss"; exit 1 ;;
    *) echo "PASS: host 에 설치된 의존은 missing 에서 빠진다" ;;
  esac
  case "$miss" in
    *nvidia-nonexistent-dep-xyz*) echo "PASS: host 에 없는 의존은 missing 으로 낸다" ;;
    *) echo "FAIL: host 에 없는 의존을 못 잡았다: $miss"; exit 1 ;;
  esac
) || exit 1

if ! sudo docker image inspect "$IMG" >/dev/null 2>&1; then
  echo "SKIP: 로컬 이미지 ${IMG} 없음 — fixture 를 못 만들어 시험을 못 돌린다"
  exit 1
fi

# --- 의존 추출: 굽힌 .deb 에서 외부 의존(libc6 등)은 남고 nvidia 끼리의 의존은 빠지는지 ---
mkdir -p "$tmp/userspace"
if ! sudo docker run --rm -v "$tmp:/out" --entrypoint /bin/bash "$IMG" \
    -c "cp /opt/nvidia-deb/${KERNEL}/userspace/*.deb /out/userspace/" >/dev/null; then
  echo "SKIP: 이미지에서 .deb 추출 실패 — fixture 를 못 만들어 시험을 못 돌린다"
  exit 1
fi

out="$(userspace_deb_depends "$tmp/userspace")"
case "$out" in
  *libc6*) echo "PASS: 의존 목록에 libc6 이 있다" ;;
  *) echo "FAIL: 의존을 못 읽었다: $out"; exit 1 ;;
esac
case "$out" in
  *nvidia*) echo "FAIL: nvidia 패키지가 목록에 남았다(굽힌 것끼리의 의존은 제외해야 한다)"; exit 1 ;;
  *) echo "PASS: nvidia 자체 의존은 제외됐다" ;;
esac
