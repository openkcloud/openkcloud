#!/usr/bin/env bash
# ============================================================
# measure-node.sh: <rebellions-node> 에서 vLLM 서비스를 멈추고 두 rebel 프로세스를 같은 장치(0)에 붙여
#                  동시 실행 여부를 실측(run.sh 가 ssh 로 이 스크립트를 <rebellions-node> 에서 실행)
# 상세: workload.py 를 solo 1회→co-run 2회로 돌리고 SOLO_RESULT/CORUN_A_RESULT/... 접두 줄로
#       낸다(run.sh 가 파싱). 트랩이 EXIT·시그널에서 SERVICE 를 복원해 ssh 세션이 끊겨도 되살아난다.
# 생성일: 2026-09-25 | 수정일: 2026-10-11
# ============================================================
set -u

SERVICE="${SERVICE:-atomplus-vllm.service}"
WL="${WL:-/tmp/rbln-workload.py}"
OUT="${OUT:-/tmp/rebellions-timeslice-node}"
DUR="${DUR:-30}"
DIM="${DIM:-2048}"
BAR="${BAR:-40}"
DEVICE_ID="${DEVICE_ID:-0}"
FREE_WAIT="${FREE_WAIT:-90}"

mkdir -p "$OUT"

restore() {
  echo "MEASURE_RESTORE starting $SERVICE"
  sudo systemctl start "$SERVICE" 2>/dev/null || true
}
trap restore EXIT HUP INT TERM

emit_lines() {
  # emit_lines PREFIX LOGFILE — 로그에서 RESULT/INIT 줄을 PREFIX 로 접두해 stdout 에 낸다.
  local prefix="$1" logf="$2"
  local r i
  r="$(grep '^RESULT ' "$logf" 2>/dev/null | tail -1 | sed 's/^RESULT //')"
  [ -n "$r" ] || r='{}'
  i="$(grep '^INIT ' "$logf" 2>/dev/null | tail -1)"
  [ -n "$i" ] || i='INIT (none)'
  printf '%s_RESULT %s\n' "$prefix" "$r"
  printf '%s_INIT %s\n' "$prefix" "$i"
}

echo "MEASURE_BEGIN service=$SERVICE dur=$DUR dim=$DIM barrier=$BAR device_id=$DEVICE_ID"

echo "MEASURE_STOP $SERVICE"
sudo systemctl stop "$SERVICE" 2>/dev/null || true

# 장치가 free 될 때까지 대기. 안 풀리면 BLOCKED 로 보고하고 종료(트랩이 서비스 복원).
free=0
for _ in $(seq 1 "$((FREE_WAIT / 2))"); do
  if ! sudo fuser -s /dev/rbln0 /dev/rbln1 2>/dev/null; then free=1; break; fi
  sleep 2
done
if [ "$free" != "1" ]; then
  echo "MEASURE_BLOCKED devices_still_held"
  sudo fuser -v /dev/rbln0 /dev/rbln1 2>&1 || true
  exit 2
fi
echo "MEASURE_STATE devices_free"

# ── solo: 장치를 혼자 쓸 때의 기준선 ────────────────────────────────────────
echo "MEASURE_PHASE solo"
PHASE=solo DURATION="$DUR" DEVICE_ID="$DEVICE_ID" MATMUL_DIM="$DIM" \
  python3 "$WL" >"$OUT/solo.log" 2>&1
emit_lines SOLO "$OUT/solo.log"

# ── co-run: 두 프로세스가 같은 DEVICE_ID 를 같은 START_AT 에 측정 시작 ───────
START_AT="$(python3 -c "import time;print(time.time()+$BAR)")"
echo "MEASURE_PHASE corun START_AT=$START_AT"
PHASE=corun-a DURATION="$DUR" DEVICE_ID="$DEVICE_ID" MATMUL_DIM="$DIM" START_AT="$START_AT" \
  python3 "$WL" >"$OUT/corun-a.log" 2>&1 &
PA=$!
PHASE=corun-b DURATION="$DUR" DEVICE_ID="$DEVICE_ID" MATMUL_DIM="$DIM" START_AT="$START_AT" \
  python3 "$WL" >"$OUT/corun-b.log" 2>&1 &
PB=$!
echo "MEASURE_PIDS a=$PA b=$PB"

# 측정 창(배리어 뒤) 한가운데에서 rbln-smi 를 찍어 두 PID 가 같은 NPU 에 있는지 남긴다.
( sleep "$((BAR + DUR / 2))"
  rbln-smi >"$OUT/corun-smi.txt" 2>&1 || true ) &
SMI=$!

wait "$PA"; echo "MEASURE_RC a=$?"
wait "$PB"; echo "MEASURE_RC b=$?"
wait "$SMI" 2>/dev/null || true

emit_lines CORUN_A "$OUT/corun-a.log"
emit_lines CORUN_B "$OUT/corun-b.log"

# rbln-smi 컨텍스트 표에서 DEVICE_ID 행에 올라온 서로 다른 PID 수를 센다(같은 장치 공유 증거).
npu0_pids="$(grep -E "^\| +${DEVICE_ID} +\|" "$OUT/corun-smi.txt" 2>/dev/null \
  | grep -oE '\| +[0-9]{3,} +\|' | grep -oE '[0-9]{3,}' | sort -u | wc -l | tr -d ' ')"
echo "CORUN_SMI_NPU${DEVICE_ID}_DISTINCT_PIDS ${npu0_pids:-0}"
echo "CORUN_SMI_DUMP_BEGIN"
sed 's/\x1b\[[0-9;]*m//g' "$OUT/corun-smi.txt" 2>/dev/null | grep -E "Process|VLLM|python|rbln|NPU|\| +[0-9] +\|" | head -20 || true
echo "CORUN_SMI_DUMP_END"

echo "MEASURE_RESTART $SERVICE"
restore
sleep 3
echo "SERVICE_STATE $(systemctl is-active "$SERVICE" 2>/dev/null || echo unknown)"
echo "MEASURE_END"
