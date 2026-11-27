#!/usr/bin/env bash
# ============================================================
# probe.sh: RNGD PE 장치노드 다중 프로세스 동시 open() 실측(호스트 레벨, 이미지 불필요)
# 상세: <rngd-node> 을 SSH 로 직접 접속해 두 프로세스가 같은 PE 캐릭터 디바이스를 동시에 open() 할
#       수 있는지 판정한다. open 성공은 추론 점유 증거가 아니다 — 컨트롤(다른 PE) 성공이 계측기 유효 신호다.
# 생성일: 2026-07-29 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

NODE_HOST="${NODE_HOST:?RNGD 노드 SSH 대상을 지정하세요 (예: user@host)}"
PE_DEV="${PE_DEV:-/dev/rngd/npu0pe0}"
CONTROL_PE_DEV="${CONTROL_PE_DEV:-/dev/rngd/npu0pe1}"
HOLD_SECONDS="${HOLD_SECONDS:-20}"
A_LOG="$(mktemp)"

ssh_run() { ssh -o BatchMode=yes -o ConnectTimeout=5 "$NODE_HOST" "$@"; }

# open_probe: 지정 장치를 한 번 열어보고 즉시 닫는다. bash exec 리다이렉션은 실제 errno 를
# 숨기므로(항상 "No such file or directory" 로만 보고), python3 os.open 으로 errno 를 그대로 받는다.
open_probe() {
  ssh_run "python3 -c \"
import os
try:
    fd = os.open('$1', os.O_RDWR)
    print('OPEN_OK')
    os.close(fd)
except OSError as e:
    print(f'OPEN_FAIL errno={e.errno} {e.strerror}')
\""
}

echo "== baseline: pe_occupancy / furiosa-smi ps =="
ssh_run 'cat /sys/class/rngd_mgmt/rngd\!npu0mgmt/pe_occupancy; furiosa-smi ps'

echo "== step A: 프로세스 A 가 $PE_DEV 를 ${HOLD_SECONDS}s 동안 보유 =="
ssh_run "python3 -c \"
import os, time
try:
    fd = os.open('$PE_DEV', os.O_RDWR)
    print('A_OPEN_OK fd=', fd)
    time.sleep($HOLD_SECONDS)
    os.close(fd)
    print('A_CLOSED')
except OSError as e:
    print(f'A_OPEN_FAIL errno={e.errno} {e.strerror}')
\"" > "$A_LOG" 2>&1 &
A_PID=$!
sleep 3

echo "== step B: A 보유 중 동일 장치($PE_DEV) 2차 open 시도 — 이게 판정 =="
open_probe "$PE_DEV"

echo "== step C(control): A 보유 중 다른 PE($CONTROL_PE_DEV) open 시도 — 성공해야 계측기가 유효 =="
open_probe "$CONTROL_PE_DEV"

wait "$A_PID" || true # A 가 open 실패해도(계측 무효 케이스) 스크립트는 끝까지 진행해 로그를 남긴다.
echo "== A 로그 =="
cat "$A_LOG"
rm -f "$A_LOG"

echo "== 복원 확인: pe_occupancy 가 baseline 으로 돌아왔는지 =="
ssh_run 'cat /sys/class/rngd_mgmt/rngd\!npu0mgmt/pe_occupancy; furiosa-smi ps'

echo "== 판정 안내 =="
echo "B(동일 PE)와 C(다른 PE) 결과를 비교: C 는 성공, B 만 실패해야 '동시 접근 배타적'을 뜻한다."
echo "B/C 가 둘 다 실패(또는 둘 다 성공)하면 이 계측기가 그 실행 시점엔 무효하다는 뜻 — README 참조."
