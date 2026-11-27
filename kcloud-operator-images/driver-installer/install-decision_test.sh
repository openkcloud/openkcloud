#!/usr/bin/env bash
# ============================================================
# install-decision_test.sh: 드라이버 설치 판단 함수 시험 (Furiosa RNGD·Warboy 공용)
# 상세: dpkg 상태가 half-configured·unpacked 등으로 남으면 버전이 같아도 복구를
#       우선하는지, 다운그레이드 가드와 거부 보고 분기가 맞는지를 고정한다.
# 생성일: 2026-08-10
# ============================================================
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAILED=0

# ns 는 원래 nsenter 로 호스트에서 도는 래퍼다. 시험에서는 그냥 로컬에서 실행한다 —
# 판단 함수가 쓰는 것은 dpkg --compare-versions 뿐이고 그건 로컬에도 있다.
ns() { bash -c "$1"; }


check() {
  local label="$1" want="$2" got="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "  [FAIL] ${label}: got '${got}', want '${want}'"
    FAILED=1
  else
    echo "  [ok]   ${label}"
  fi
}

run_suite_for() {
  local dir="$1" lib="${1}/install-decision.sh"
  echo "== ${dir##*/} =="
  if [[ ! -f "${lib}" ]]; then
    echo "  [FAIL] ${lib} 이 없다"
    FAILED=1
    return
  fi
  # shellcheck source=/dev/null
  source "${lib}"   # ver_gt 도 이 파일이 준다 — 시험이 사본을 들지 않는다

  if ! declare -F decide_install_action >/dev/null; then
    echo "  [FAIL] decide_install_action 가 정의되지 않았다"
    FAILED=1
    return
  fi

  # 인자: <설치된 버전> <dpkg 상태> <목표 버전> <다운그레이드 허용>

  # 버전은 목표와 같지만 설정이 미완이라 손대지 않으면 영원히 그 상태로 남는다.
  check "설정 미완이면 복구한다" repair \
    "$(decide_install_action 2026.2.0 half-configured 2026.2.0 false)"
  check "풀린 상태여도 복구한다" repair \
    "$(decide_install_action 2026.2.0 unpacked 2026.2.0 false)"
  check "설치 반쪽이어도 복구한다" repair \
    "$(decide_install_action 2026.2.0 half-installed 2026.2.0 false)"

  # 정상 경로는 종전과 같아야 한다.
  check "정상 설치 + 같은 버전이면 건너뛴다" skip \
    "$(decide_install_action 2026.2.0 installed 2026.2.0 false)"
  check "목표 버전이 없으면 건너뛴다" skip \
    "$(decide_install_action 2026.2.0 installed '' false)"
  check "설치된 것이 없으면 설치한다" install \
    "$(decide_install_action '' '' 2026.2.0 false)"
  check "버전이 다르면 설치한다" install \
    "$(decide_install_action 2026.1.0 installed 2026.2.0 false)"

  # 다운그레이드 가드는 유지한다.
  check "상위 버전이 깔려 있고 다운그레이드 금지면 거부한다" skip-downgrade \
    "$(decide_install_action 2026.2.0 installed 2026.1.0 false)"
  check "다운그레이드를 허용하면 설치한다" install \
    "$(decide_install_action 2026.2.0 installed 2026.1.0 true)"

  # 설정이 미완이면 다운그레이드 가드보다 복구가 앞선다 — 깨진 패키지를 그대로 두면
  # 버전 보고가 막혀 업그레이드 사이클 전체가 멈춘다.
  check "설정 미완은 다운그레이드 가드보다 앞선다" repair \
    "$(decide_install_action 2026.2.0 half-configured 2026.1.0 false)"

  # 거부를 실패로 보고할지. job 모드는 operator 가 종료코드로 설치 성공을 읽으므로
  # 거부해 놓고 exit 0 을 주면 거짓말이 된다. 상주 daemonset 은 exit 1 이면 CrashLoop 이라 안 된다.
  if ! declare -F refusal_is_fatal >/dev/null; then
    echo "  [FAIL] refusal_is_fatal 가 정의되지 않았다"
    FAILED=1
  else
    refusal_is_fatal job && r=fatal || r=ok
    check "job 모드의 거부는 실패로 보고한다" fatal "${r}"
    refusal_is_fatal daemonset && r=fatal || r=ok
    check "daemonset 모드의 거부는 실패로 보고하지 않는다" ok "${r}"
    refusal_is_fatal "" && r=fatal || r=ok
    check "RUN_MODE 미지정은 daemonset 취급" ok "${r}"
  fi

  unset -f decide_install_action refusal_is_fatal ver_gt
}

run_suite_for "${HERE}/furiosa"

# install-decision.sh 는 furiosa/ 사본 하나뿐이다(rngd·warboy 설치기가 이 파일을 공유한다).

if [[ "${FAILED}" -ne 0 ]]; then
  echo "FAIL"
  exit 1
fi
echo "PASS"
