#!/usr/bin/env bash
# shellcheck disable=SC2086,SC2029
# SC2086: SSH_OPTS 는 공백분리 옵션 여러 개가 와야 해 의도적으로 unquoted. SC2029: $PY 는 로컬(USE_SUDO)에서 정해 client-side 확장이 의도다.
# ============================================================
# probe.sh: 같은 장치 파일을 두 프로세스가 동시에 열 수 있는지(SW 시분할 안전 조건) 비파괴로 실측
# 상세: MODE=hold(기본)·single 두 모드로 os.open(O_RDWR) 성공/실패를 보고 JSON 한 줄을 낸다.
#       verdict 판정 기준과 환경변수는 README.md 참고.
# 생성일: 2026-09-19 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
VENDOR="${1:?vendor}"; HOST="${2:?ssh host}"; DEV="${3:?device path e.g. /dev/tenstorrent/0}"
HOLD="${HOLD:-3}"
# 정수가 아니면(빈 값·문자·소수 등) (( )) 산술 컨텍스트가 set -u 로 죽거나 조용히 오동작하므로
# clamp 전에 반드시 검증 — 정수가 아니면 기본값 3 으로.
[[ "$HOLD" =~ ^[0-9]+$ ]] || HOLD=3
# 정수여도 "089" 처럼 선행 0 이 붙으면 (( )) 가 8진수로 읽어 "value too great for base"
# 로 죽는다(set -e). 10# 접두로 항상 10진수로 강제한 뒤 clamp 한다.
HOLD=$((10#$HOLD))
(( HOLD > 5 )) && HOLD=5
MODE="${MODE:-hold}"
CONFOUND="${CONFOUND:-}"
SSH_OPTS="${SSH_OPTS:-}"
if [ "$MODE" = "single" ] && [ -z "$CONFOUND" ]; then
  echo "probe.sh: MODE=single 은 CONFOUND(사유)가 필수다 — 외부 holder 가 이미 A 역할이라" \
       "표준 2-프로세스 프로토콜이 아님을 기록해야 한다" >&2
  exit 1
fi
PY=python3
[ "${USE_SUDO:-0}" = "1" ] && PY="sudo -n python3"

# 첫 인자는 역할 태그(A=보유, B=2차 open)다. 두 프로세스의 출력이 한 stdout 으로 섞여
# 나오므로 줄 위치(tail -1)가 아니라 태그로 가른다 — 원격 셸 배너가 섞여도 안전하다.
py='import os,sys,time,errno
tag=sys.argv[1]; p=sys.argv[2]; hold=float(sys.argv[3])
try:
    fd=os.open(p, os.O_RDWR)
except OSError as e:
    print(tag, "OPEN_FAIL", errno.errorcode.get(e.errno, e.errno)); sys.exit(0)
print(tag, "OPEN_OK"); sys.stdout.flush(); time.sleep(hold); os.close(fd)'

# ssh 실패(unreachable/auth/timeout, exit 255 등)도 abort 없이 raw 에 담는다 — set -e 라도
# if 조건 안의 실패는 스크립트를 죽이지 않는다(표준 관용구). ssh_failed 는 실패 여부만 기억하고,
# "SSH_FAILED:" 표식은 tail -1(아래) 로 마지막 줄을 뽑은 뒤에 붙인다 — 먼저 붙이면 host-key
# 경고처럼 여러 줄짜리 stderr 일 때 tail -1 이 표식만 잘라내고 정작 메시지를 버릴 수 있다.
ssh_failed=0
if [ "$MODE" = "single" ]; then
  # 외부 holder 가 이미 A 역할 — 이 프로세스가 곧 B(두 번째 opener).
  raw=$(ssh $SSH_OPTS "$HOST" "$PY -c '$py' B '$DEV' 0" 2>&1) || ssh_failed=1
else
  raw=$(ssh $SSH_OPTS "$HOST" "$PY -c '$py' A '$DEV' '$HOLD' & sleep 0.5; $PY -c '$py' B '$DEV' 0" 2>&1) || ssh_failed=1
fi
# 역할 태그로 가른다. SSH 로 전달되는 로케일 등 원격 셸의 부수 stderr 배너가 섞여도
# (예: "bash: warning: setlocale: LC_ALL: cannot change locale") 태그가 없으면 걸러진다.
# 태그 줄이 하나도 없으면(ssh 자체 실패 등) 예전처럼 마지막 줄을 그대로 싣는다 —
# 조용히 빈 값을 내는 대신 실패 메시지를 evidence 에 남기기 위함이다.
second=$(printf '%s\n' "$raw" | sed -n 's/^B //p' | tail -1)
[ -z "$second" ] && second=$(printf '%s\n' "$raw" | tail -1)
[ "$ssh_failed" = "1" ] && second="SSH_FAILED: $second"
# single 모드에는 A 가 없다(외부 holder 가 A 역할) — n/a 로 그 사실을 남긴다.
first="n/a"
if [ "$MODE" != "single" ]; then
  first=$(printf '%s\n' "$raw" | sed -n 's/^A //p' | tail -1)
  [ -z "$first" ] && first="MISSING"
fi

case "$second" in
  OPEN_OK*) verdict=ok ;;
  *EBUSY*)  verdict=exclusive ;;
  *)        verdict=error ;;
esac
# A 가 장치를 못 잡았으면 B 의 성공은 "동시 open" 근거가 아니다(보유자 없는 단발 open).
case "$first" in
  n/a|OPEN_OK*) ;;
  *)            verdict=error ;;
esac
printf '{"vendor":"%s","host":"%s","device":"%s","mode":"%s","firstOpen":"%s","secondOpen":"%s","verdict":"%s","confound":"%s","date":"%s"}\n' \
  "$VENDOR" "$HOST" "$DEV" "$MODE" "$first" "$second" "$verdict" "$CONFOUND" "$(date -u +%FT%TZ)"
