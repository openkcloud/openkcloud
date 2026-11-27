#!/usr/bin/env bash
# ============================================================
# install-decision.sh: 드라이버 설치 판단 (순수 함수, entrypoint.sh 가 source 한다)
# 상세: 설치된 버전과 dpkg 설정 상태를 함께 보고 설치 여부를 판단하는 순수 함수다. rngd·warboy
#       이미지에 같은 사본을 두며, 일치 여부는 install-decision_test.sh 가 지킨다.
# 생성일: 2026-08-10
# ============================================================

# ver_gt 는 $1 > $2 이면 0(true) 다. dpkg 로 정상 비교되면 그 결과를 쓰고, 형식오류 등으로
# dpkg 가 판정 불가하면 major 정수 비교로 fallback 한다.
# 여기 두는 이유: entrypoint 마다 사본을 두면 시험이 보는 것과 이미지가 쓰는 것이 갈라진다 —
# 이 파일은 install-decision_test.sh 가 두 사본의 동일성을 지키므로 여기가 맞다.
# 호출부에서 `ns` 를 제공해야 한다.
ver_gt() {
  local a="$1" b="$2" am bm
  if ns "dpkg --compare-versions '${a}' gt '${b}'" >/dev/null 2>&1; then
    return 0
  elif ns "dpkg --compare-versions '${a}' le '${b}'" >/dev/null 2>&1; then
    return 1
  fi
  am="${a%%.*}"; bm="${b%%.*}"
  if [[ "$am" =~ ^[0-9]+$ && "$bm" =~ ^[0-9]+$ ]] && [[ "$am" -gt "$bm" ]]; then
    return 0
  fi
  return 1
}

# decide_install_action 은 무엇을 할지 하나로 답한다.
# 인자: <설치된 버전(dpkg-query Version, 없으면 빈 문자열)> <dpkg 상태(Status-Status: installed/
#   half-configured/unpacked/half-installed/config-files, 없으면 빈 문자열)> <목표 버전> <다운그레이드 허용>
# 출력(한 줄): install=전량 설치, repair=설정 마무리(dpkg --configure, 실패 시 호출부가 install 로
#   승격), skip=할 일 없음, skip-downgrade=상위 버전 있고 다운그레이드 금지.
# 호출부에서 `ns` 를 제공해야 한다(`ver_gt` 는 이 파일이 정의한다).
decide_install_action() {
  local existing_ver="${1:-}" existing_state="${2:-}" desired_ver="${3:-}" allow_downgrade="${4:-false}"

  # 아무것도 없다 — 그냥 설치한다.
  if [[ -z "${existing_ver}" ]]; then
    echo "install"
    return
  fi

  # 설정이 끝나지 않은 패키지는 **무엇보다 먼저** 복구한다. 버전 비교나 다운그레이드 가드보다
  # 앞이다: 이 상태에서는 dpkg 가 버전을 정상 보고하지 못해 업그레이드 사이클이 통째로 멈춘다.
  # 다운그레이드 가드를 앞세우면 "깨진 상위 버전" 이 영원히 손대지지 않는 칸이 생긴다.
  if [[ -n "${existing_state}" && "${existing_state}" != "installed" ]]; then
    echo "repair"
    return
  fi

  # 목표가 없거나 이미 같다 — 할 일 없음.
  if [[ -z "${desired_ver}" || "${existing_ver}" == "${desired_ver}" ]]; then
    echo "skip"
    return
  fi

  # 상위 버전이 이미 깔려 있으면 허용했을 때만 내린다.
  if ver_gt "${existing_ver}" "${desired_ver}" && [[ "${allow_downgrade}" != "true" ]]; then
    echo "skip-downgrade"
    return
  fi

  echo "install"
}

# refusal_is_fatal 은 "설치를 거부했다" 를 실패로 보고할지 답한다(0=실패로 보고).
# job 모드에서만 실패다 — operator 가 Job 종료코드로 설치 성공을 읽어, 거부해 놓고 exit 0 을
# 주면 거짓 성공이 되고 검증 타임아웃(기본 10분)을 다 쓴 뒤에야 어긋남이 드러난다. daemonset
# 모드는 실패로 보고하지 않는다 — 상주 pod 이 exit 1 을 내면 CrashLoopBackOff 로 들어가 드라이버
# 감시 자체가 멈춘다. 거부는 정상 상태일 수 있다(호스트가 더 최신). 호출부는 skip-downgrade
# 분기에서만 부른다.
refusal_is_fatal() {
  [ "${1:-daemonset}" = "job" ]
}
