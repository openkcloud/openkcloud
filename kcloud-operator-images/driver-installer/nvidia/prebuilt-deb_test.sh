#!/usr/bin/env bash
# ============================================================
# prebuilt-deb_test.sh: entrypoint.sh 의 select_prebuilt_deb 가 커널 일치 .deb 만 고르는지 시험
# 상세: 가짜 /opt/nvidia-deb/<kernel>/ 트리를 만들고 함수 정의만 source(ENTRYPOINT_LIB_ONLY=1)해
#       호출한다. 일치 커널이면 .deb 경로 출력 + exit 0, 없는 커널이면 exit 비0 을 확인한다.
#       실제 실행 경로인 host-install entrypoint.sh(v16)를 대상으로 한다. 컨테이너 실행 없이
#       도는 순수 bash 시험이다.
# 생성일: 2026-09-23 | 수정일: 2026-09-27
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT

# 가짜 prebuilt 트리: <nvidia-node> 커널 하나만 동봉된 상태를 흉내낸다.
mkdir -p "$tmp/opt/nvidia-deb/5.15.0-171-generic"
: > "$tmp/opt/nvidia-deb/5.15.0-171-generic/linux-modules-nvidia-580-5.15.0-171-generic_5.15.0-171.181_amd64.deb"

# 함수 정의만 로드(부작용 없음). PREBUILT_ROOT 로 가짜 트리를 가리킨다.
# source 는 현재 셸에서 돌므로 일반 변수 할당으로 넘긴다(명령 prefix 는 특수 빌트인에서
# 유지가 셸마다 달라 set -u 에서 unbound 가 날 수 있다).
# shellcheck disable=SC2034  # 두 변수는 source 된 entrypoint.sh 가 읽는다(이 파일에선 미참조)
PREBUILT_ROOT="$tmp/opt/nvidia-deb"
# shellcheck disable=SC2034
ENTRYPOINT_LIB_ONLY=1
# shellcheck source=/dev/null
source "$here/entrypoint.sh"

# 1) 일치 커널: .deb 경로를 출력하고 exit 0.
p="$(select_prebuilt_deb 5.15.0-171-generic)"
[[ "$p" == *linux-modules-nvidia-580-5.15.0-171-generic_* ]] || { echo "FAIL match: $p"; exit 1; }

# 2) 동봉되지 않은 커널: 아무것도 출력하지 않고 exit 비0.
if select_prebuilt_deb 5.15.0-190-generic >/dev/null 2>&1; then echo "FAIL mismatch"; exit 1; fi

# nvidia_prebuilt_coherent 시험은 prebuilt-coherent_test.sh 로 옮겼다. 함수가 두 인자
# (<동봉버전> <정책버전>)만 받도록 바뀌어 아카이브 candidate 케이스가 없다(ADR-002).

# --- 실행 경로에서 사용자 공간 apt 설치가 사라졌는지 ---
# 성공은 조용히 넘어가고 실패만 출력한다. 위 케이스와 같은 방식이다.
pass() { :; }
fail() { echo "FAIL: $*"; exit 1; }

# 실행 중 apt 로 사용자 공간을 고르면 이미지와 독립적인 두 번째 버전 출처가 생긴다. 그 통로가
# 소스에 남아 있지 않은지 고정한다. 저장소 버전을 명시한 apt 설치 줄에 사용자 공간 패키지
# 이름이 함께 남으면 이 검사가 실패한다.
if grep -qE 'apt-get install .*(libnvidia-compute|nvidia-utils|nvidia-kernel-common)' "$here/entrypoint.sh"; then
  fail "사전 컴파일 경로에 사용자 공간 apt 설치가 남아 있다"
else
  pass "사용자 공간 apt 설치가 없다"
fi
# 버전 고정 apt 함수가 지워졌는지 — 이 함수가 남아 있으면 apt 설치 통로도 함께 남은 것이다.
if grep -q 'nvidia_pinned_userspace_pkgs' "$here/entrypoint.sh"; then
  fail "버전 고정 apt 함수가 남아 있다"
else
  pass "버전 고정 apt 함수가 제거됐다"
fi
# 구운 사용자 공간을 dpkg -i 로 설치하는지 — 사용자 공간 .deb 를 스테이징으로 복사하고
# 스테이징 전체를 한 번의 dpkg -i 로 설치해야 사용자 공간과 모듈이 같은 트랜잭션에서 풀린다.
if grep -F 'cp -f' "$here/entrypoint.sh" | grep -q 'usrc' \
   && grep -F 'dpkg -i' "$here/entrypoint.sh" | grep -q 'stage'; then
  pass "구운 사용자 공간을 스테이징으로 복사해 dpkg -i 로 설치한다"
else
  fail "구운 사용자 공간 dpkg -i 설치 경로가 없다"
fi

# nvidia_prebuilt_cleanup_cmd: 설치 실패 뒤 잔재를 치우는 명령을 낸다. 미설정 상태로 남은
# 모듈·사용자 공간 패키지를 지우고 깨진 의존을 고쳐야 다음 설치가 가능하다. firmware 는
# 이름에 버전이 박혀 세 번째 인자로 버전을 받는다.
cmd="$(nvidia_prebuilt_cleanup_cmd 580 5.15.0-171-generic 580.126.09)"
case "$cmd" in
  *"linux-modules-nvidia-580-5.15.0-171-generic"*) pass "모듈 패키지를 지운다" ;;
  *) fail "모듈 패키지 제거 없음: $cmd" ;;
esac
case "$cmd" in
  *"linux-objects-nvidia-580-5.15.0-171-generic"*) pass "objects 패키지를 지운다" ;;
  *) fail "objects 패키지 제거 없음: $cmd" ;;
esac
# 서명 패키지 이름에는 계열 번호가 없고 커널만 들어간다. 동봉 .deb 세 종류가 모두
# host 에 설치되므로 서명 패키지도 제거 목록에 있어야 한다.
case "$cmd" in
  *"linux-signatures-nvidia-5.15.0-171-generic"*) pass "서명 패키지를 지운다" ;;
  *) fail "서명 패키지 제거 없음: $cmd" ;;
esac
# 굽힌 사용자 공간 여섯 종 중 firmware 도 지워야 한다. 실패 뒤 낡은 firmware 가 남으면
# 다음 시도의 상태가 흐려진다. 이름에 버전이 박혀 nvidia-firmware-<major>-<version> 이다.
case "$cmd" in
  *"nvidia-firmware-580-580.126.09"*) pass "펌웨어 패키지를 지운다" ;;
  *) fail "펌웨어 제거 없음: $cmd" ;;
esac
# nvidia-kernel-common 이 firmware 를 의존하므로 firmware 가 맨 뒤에 온다.
case "$cmd" in
  *nvidia-kernel-common-580*nvidia-firmware-580-580.126.09*) pass "펌웨어가 kernel-common 뒤에 온다" ;;
  *) fail "펌웨어 제거 순서가 의존 방향과 어긋난다: $cmd" ;;
esac
case "$cmd" in
  *"--fix-broken"*) pass "깨진 의존을 고친다" ;;
  *) fail "의존 복구 없음: $cmd" ;;
esac
case "$cmd" in
  *"dpkg --remove"*|*"apt-get remove"*) pass "제거 명령을 쓴다" ;;
  *) fail "제거 명령 없음: $cmd" ;;
esac

# --- 스테이징을 복사 전에 비우고 구운 사용자 공간을 함께 스테이징한다 ---
# host 에 남는 디렉터리라 앞선 시도의 다른 계열 .deb 가 남아 있으면 dpkg -i 가 와일드카드로
# 함께 집어 설치가 깨진다. install_prebuilt_onto_host 를 실제로 돌려 그 잔재가 사라지는지 본다.
# host 의존(ns·apt_wait·userspace_missing_host_deps)만 스텁으로 막고 나머지(select_prebuilt_deb·
# cp·함수)는 실제로 돈다. 사용자 공간 디렉터리와 .deb 를 만들어 두 인자 호출이 성립하게 한다.
(
  MARKER_DIR="$tmp/marker"
  ns() { return 0; }
  apt_wait() { return 0; }
  userspace_missing_host_deps() { :; }
  mkdir -p "$tmp/opt/nvidia-deb/5.15.0-171-generic/userspace"
  : > "$tmp/opt/nvidia-deb/5.15.0-171-generic/userspace/nvidia-kernel-common-580_580.126.09-0ubuntu0.22.04.1_amd64.deb"
  s="$MARKER_DIR/prebuilt-deb/5.15.0-171-generic"
  mkdir -p "$s"
  : > "$s/linux-modules-nvidia-590-5.15.0-171-generic_5.15.0-171.181_amd64.deb"
  install_prebuilt_onto_host 5.15.0-171-generic 580 >/dev/null 2>&1 \
    || fail "install_prebuilt_onto_host 가 0 을 반환하지 않음"
  if [ -e "$s/linux-modules-nvidia-590-5.15.0-171-generic_5.15.0-171.181_amd64.deb" ]; then
    fail "앞선 시도의 590 잔재가 스테이징에 남았다"
  fi
  pass "복사 전 스테이징 디렉터리를 비운다"
)

# --- 결함 수정: TARGET_PKG_OK 의 grep -c 뒤 || echo 0 제거 ---
# grep -c 는 무매치에도 0 을 내고 종료 상태만 1 이라 || echo 0 이 0 을 한 번 더 붙여 값을
# 두 줄로 만든다. 그 값으로 하는 산술 비교가 깨진다. 그 줄에 || echo 0 이 없어야 한다.
# ns 를 거치는 줄이라 소스로 확인한다.
pkgline="$(grep -F 'TARGET_PKG_OK=' "$here/entrypoint.sh" || true)"
case "$pkgline" in
  *"|| echo 0"*) fail "TARGET_PKG_OK 에 || echo 0 이 남아 값이 두 줄이 된다: $pkgline" ;;
  *"grep -c"*) pass "TARGET_PKG_OK 가 grep -c 결과를 그대로 쓴다(|| echo 0 없음)" ;;
  *) fail "TARGET_PKG_OK 줄을 찾지 못함: $pkgline" ;;
esac

# --- 결함 수정: NVRM version 파서를 실제 형식에 맞춤 ---
# 실제 /proc/driver/nvidia/version 은 라벨과 버전 사이에 문구가 더 있어 앞자리만 보는
# 정규식은 빈 값을 낸다. parse_nvrm_version 이 실제 형식·느슨한 형식·자리 개수 변화를 모두
# 받는지, 옛 정규식이 코드에 남지 않았는지, 네 호출부가 도우미를 쓰는지 본다.
real='NVRM version: NVIDIA UNIX x86_64 Kernel Module  580.126.09  Wed Jan  7 22:59:56 UTC 2026'
case "$(printf '%s\n' "$real" | parse_nvrm_version)" in
  580.126.09) pass "실제 형식에서 버전을 뽑는다" ;;
  *) fail "실제 형식 파싱 실패: [$(printf '%s\n' "$real" | parse_nvrm_version)]" ;;
esac
case "$(printf 'NVRM version: 580.126.09\n' | parse_nvrm_version)" in
  580.126.09) pass "느슨한 형식도 받는다" ;;
  *) fail "느슨한 형식 파싱 실패" ;;
esac
case "$(printf 'NVRM version: 580.126\n' | parse_nvrm_version)" in
  580.126) pass "X.Y 자리도 받는다" ;;
  *) fail "X.Y 파싱 실패" ;;
esac
case "$(printf 'NVRM version: 580.126.09.1\n' | parse_nvrm_version)" in
  580.126.09.1) pass "X.Y.Z.W 자리도 받는다" ;;
  *) fail "X.Y.Z.W 파싱 실패" ;;
esac
if grep -F '\K[0-9]+\.[0-9]+\.[0-9]+' "$here/entrypoint.sh" >/dev/null; then
  fail "옛 NVRM 3-그룹 정규식이 코드에 아직 남아 있다"
fi
pass "옛 NVRM 3-그룹 정규식이 없다"
nvrm_calls="$(grep -c '| parse_nvrm_version' "$here/entrypoint.sh" || true)"
case "$nvrm_calls" in
  4) pass "네 호출부가 모두 parse_nvrm_version 을 쓴다" ;;
  *) fail "parse_nvrm_version 호출부 수가 4 가 아님: $nvrm_calls" ;;
esac

echo "PASS: all cases"
