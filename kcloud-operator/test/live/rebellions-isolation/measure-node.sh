#!/usr/bin/env bash
# ============================================================
# measure-node.sh: <rebellions-node> 에서 vLLM 서비스를 멈추고 ATOM 두 장치(0·1)에 각각 프로세스를 붙여
#                  연산·용량·대역폭·장애 네 축의 격리를 실측(run.sh 가 ssh 로 이 스크립트를 실행)
# 상세: 각 축의 RESULT/INIT 줄을 축별 접두사로 stdout 에 내고(run.sh 가 grep 으로 파싱), 트랩이
#       EXIT·시그널에서 SERVICE 복원과 OUT 삭제를 함께 해 ssh 세션이 끊겨도 정리된다.
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -u

SERVICE="${SERVICE:-atomplus-vllm.service}"
WL="${WL:-/tmp/rbln-workload-isolation.py}"
OUT="${OUT:-/home/kcloud/rebellions-isolation-run}"
DEV0="${DEV0:-0}"
DEV1="${DEV1:-1}"
DUR="${DUR:-20}"
BARRIER="${BARRIER:-40}"
CAP_DUR="${CAP_DUR:-15}"
CAP_BARRIER="${CAP_BARRIER:-60}"
CAP_K="${CAP_K:-32}"
CAP_DIM="${CAP_DIM:-2048}"
# 워크로드 자식 프로세스에는 MATMUL_DIM/BW_DIM 이름으로 넘긴다(workload.py 계약) — 이 스크립트
# 안에서는 SC2030/31(서브셸 export 가 상위 스코프를 오염시킨다는 오탐) 을 피하려 다른 이름을 쓴다.
SCALE_DIM="${BW_DIM:-4096}"
MM_DIM="${MATMUL_DIM:-2048}"
FAULT_DUR="${FAULT_DUR:-30}"
FREE_WAIT="${FREE_WAIT:-90}"
READY_TIMEOUT="${READY_TIMEOUT:-150}"

mkdir -p "$OUT"

restore() {
  echo "MEASURE_RESTORE starting $SERVICE"
  sudo systemctl start "$SERVICE" 2>/dev/null || true
  rm -rf "$OUT" 2>/dev/null || true
  echo "MEASURE_CLEANUP removed $OUT"
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

# wait_ready LOGFILE TIMEOUT — 워크로드가 PHASE_READY 를 찍을 때까지 기다린다(컴파일 소요를
# 고정 sleep 으로 추정하지 않기 위함 — 용량 축처럼 컴파일 시간이 불확실한 경우에 쓴다).
wait_ready() {
  local logf="$1" to="${2:-$READY_TIMEOUT}" waited=0
  while [ "$waited" -lt "$to" ]; do
    grep -q '^PHASE_READY' "$logf" 2>/dev/null && return 0
    sleep 1
    waited=$((waited + 1))
  done
  return 1
}

# run_bg LOGFILE PHASE DEVICE_ID OP DIM DURATION [START_AT] [BAD_SHAPE] [PIDFILE]
# PIDFILE 을 주면 실제 python3 워커 PID 를 그 파일에 적는다 — kill -9 대상을 래퍼 서브셸이
# 아니라 워커 자신으로 정확히 지정하기 위함(장애 축).
# shellcheck disable=SC2030,SC2031  # 아래 export 는 이 함수의 서브셸 안에서만 유효하도록
# 의도한 것이다 — 동시에 도는 여러 run_bg 호출이 서로의 MATMUL_DIM/BW_DIM 을 밟지 않게 한다.
run_bg() {
  local logf="$1" ph="$2" dev="$3" op="$4" d="$5" dur="$6" start_at="${7:-}" bad="${8:-}" pidfile="${9:-}"
  (
    export PHASE="$ph" DEVICE_ID="$dev" OP="$op" DURATION="$dur"
    export MATMUL_DIM="$d" BW_DIM="$d" CHAIN_K="$CAP_K"
    [ -n "$start_at" ] && export START_AT="$start_at"
    [ -n "$bad" ] && export BAD_SHAPE=1
    python3 "$WL" &
    local wpid=$!
    [ -n "$pidfile" ] && echo "$wpid" >"$pidfile"
    wait "$wpid"
  ) >"$logf" 2>&1
}

echo "MEASURE_BEGIN service=$SERVICE dev0=$DEV0 dev1=$DEV1 dur=$DUR barrier=$BARRIER cap_dur=$CAP_DUR cap_barrier=$CAP_BARRIER bw_dim=$SCALE_DIM fault_dur=$FAULT_DUR"

echo "MEASURE_STOP $SERVICE"
sudo systemctl stop "$SERVICE" 2>/dev/null || true

# 장치가 free 될 때까지 대기. 안 풀리면 BLOCKED 로 보고하고 종료(트랩이 서비스 복원+정리).
free=0
for _ in $(seq 1 "$((FREE_WAIT / 2))"); do
  if ! sudo fuser -s "/dev/rbln${DEV0}" "/dev/rbln${DEV1}" 2>/dev/null; then
    free=1
    break
  fi
  sleep 2
done
if [ "$free" != "1" ]; then
  echo "MEASURE_BLOCKED devices_still_held"
  sudo fuser -v "/dev/rbln${DEV0}" "/dev/rbln${DEV1}" 2>&1 || true
  exit 2
fi
echo "MEASURE_STATE devices_free"

# ── axis 1: compute — device 0 solo 대 device 1 최대 부하 중 device 0 ───────────────────────
echo "MEASURE_AXIS compute"
run_bg "$OUT/compute-solo.log" compute-solo "$DEV0" matmul "$MM_DIM" "$DUR"
emit_lines COMPUTE_SOLO "$OUT/compute-solo.log"

START_AT="$(python3 -c "import time;print(time.time()+$BARRIER)")"
run_bg "$OUT/compute-d0.log" compute-d0 "$DEV0" matmul "$MM_DIM" "$DUR" "$START_AT" &
PD0=$!
run_bg "$OUT/compute-d1.log" compute-d1 "$DEV1" matmul "$MM_DIM" "$DUR" "$START_AT" &
PD1=$!
wait "$PD0"; echo "MEASURE_RC compute_d0=$?"
wait "$PD1"; echo "MEASURE_RC compute_d1=$?"
emit_lines COMPUTE_D0 "$OUT/compute-d0.log"
emit_lines COMPUTE_D1 "$OUT/compute-d1.log"

# ── axis 2: capacity — device 1 이 K-chain 으로 큰 텐서를 쥔 채 device 0 도 같은 크기 시도 ───
# ponytail: 최대 할당 가능량을 찾는 이분 탐색 대신 고정 크기(CHAIN_K·CAP_DIM) 한 점만 잰다 —
# 두 NPU 는 rbln-smi 에 별도 Memory(used/total) 행으로 잡혀 공유 풀일 가능성이 낮고, 이 측정의
# 물음은 "같은 크기가 co-run 에서도 성공하는가" 이지 최대치를 정밀히 재는 것이 아니다. 상한을
# 더 정밀히 재려면 CAP_K/CAP_DIM 을 올려 재실행.
echo "MEASURE_AXIS capacity"
run_bg "$OUT/capacity-solo.log" capacity-solo "$DEV0" chain "$CAP_DIM" "$CAP_DUR" &
PCS=$!
if wait_ready "$OUT/capacity-solo.log"; then
  sleep 2
  rbln-smi >"$OUT/capacity-solo-smi.txt" 2>&1 || true
else
  echo "MEASURE_CAPACITY_SOLO_NOT_READY timeout=${READY_TIMEOUT}s"
fi
wait "$PCS"; echo "MEASURE_RC capacity_solo=$?"
emit_lines CAPACITY_SOLO "$OUT/capacity-solo.log"
echo "CAPACITY_SOLO_SMI_BEGIN"
sed 's/\x1b\[[0-9;]*m//g' "$OUT/capacity-solo-smi.txt" 2>/dev/null | grep -E "Process|python|rbln|NPU|\| +[0-9] +\|" | head -20 || true
echo "CAPACITY_SOLO_SMI_END"

CAP_START_AT="$(python3 -c "import time;print(time.time()+$CAP_BARRIER)")"
run_bg "$OUT/capacity-d0.log" capacity-d0 "$DEV0" chain "$CAP_DIM" "$CAP_DUR" "$CAP_START_AT" &
PCD0=$!
run_bg "$OUT/capacity-d1.log" capacity-d1 "$DEV1" chain "$CAP_DIM" "$CAP_DUR" "$CAP_START_AT" &
PCD1=$!
if wait_ready "$OUT/capacity-d0.log" "$((CAP_BARRIER + READY_TIMEOUT))"; then
  sleep 2
  rbln-smi >"$OUT/capacity-corun-smi.txt" 2>&1 || true
else
  echo "MEASURE_CAPACITY_CORUN_NOT_READY timeout=$((CAP_BARRIER + READY_TIMEOUT))s"
fi
wait "$PCD0"; echo "MEASURE_RC capacity_d0=$?"
wait "$PCD1"; echo "MEASURE_RC capacity_d1=$?"
emit_lines CAPACITY_D0 "$OUT/capacity-d0.log"
emit_lines CAPACITY_D1 "$OUT/capacity-d1.log"
echo "CAPACITY_CORUN_SMI_BEGIN"
sed 's/\x1b\[[0-9;]*m//g' "$OUT/capacity-corun-smi.txt" 2>/dev/null | grep -E "Process|python|rbln|NPU|\| +[0-9] +\|" | head -20 || true
echo "CAPACITY_CORUN_SMI_END"

# ── axis 3: bandwidth — OP=scale(계산 무시 가능) 로 처리시간을 전송이 지배하게 함 ────────────
echo "MEASURE_AXIS bandwidth"
run_bg "$OUT/bandwidth-solo.log" bandwidth-solo "$DEV0" scale "$SCALE_DIM" "$DUR"
emit_lines BANDWIDTH_SOLO "$OUT/bandwidth-solo.log"

BW_START_AT="$(python3 -c "import time;print(time.time()+$BARRIER)")"
run_bg "$OUT/bandwidth-d0.log" bandwidth-d0 "$DEV0" scale "$SCALE_DIM" "$DUR" "$BW_START_AT" &
PBD0=$!
run_bg "$OUT/bandwidth-d1.log" bandwidth-d1 "$DEV1" scale "$SCALE_DIM" "$DUR" "$BW_START_AT" &
PBD1=$!
wait "$PBD0"; echo "MEASURE_RC bandwidth_d0=$?"
wait "$PBD1"; echo "MEASURE_RC bandwidth_d1=$?"
emit_lines BANDWIDTH_D0 "$OUT/bandwidth-d0.log"
emit_lines BANDWIDTH_D1 "$OUT/bandwidth-d1.log"

# ── axis 4: fault — device 1 을 kill -9 하고 이어 형상 불일치로 실패시키는 동안 device 0 은
#    끊김 없이 완주해야 한다("격리"는 device 1 의 장애가 device 0 으로 새지 않는 것) ──────────
echo "MEASURE_AXIS fault"
run_bg "$OUT/fault-d0.log" fault-d0 "$DEV0" matmul "$MM_DIM" "$FAULT_DUR" &
PFD0=$!
sleep 5
D1_PIDFILE="$OUT/fault-d1-normal.pid"
run_bg "$OUT/fault-d1-normal.log" fault-d1-normal "$DEV1" matmul "$MM_DIM" 60 "" "" "$D1_PIDFILE" &
PFD1_WRAP=$!
sleep 8
for _ in $(seq 1 20); do [ -s "$D1_PIDFILE" ] && break; sleep 0.2; done
D1_WORKER_PID="$(cat "$D1_PIDFILE" 2>/dev/null || echo "")"
if [ -n "$D1_WORKER_PID" ]; then
  kill -9 "$D1_WORKER_PID" 2>/dev/null
  echo "MEASURE_FAULT_KILL_TARGET worker_pid=$D1_WORKER_PID"
else
  kill -9 "$PFD1_WRAP" 2>/dev/null
  echo "MEASURE_FAULT_KILL_TARGET wrapper_pid=$PFD1_WRAP (pidfile 못 읽음 — 폴백)"
fi
wait "$PFD1_WRAP" 2>/dev/null
echo "MEASURE_RC fault_d1_killed=$?"
sleep 2
run_bg "$OUT/fault-d1-badshape.log" fault-d1-badshape "$DEV1" matmul "$MM_DIM" 5 "" bad
echo "MEASURE_RC fault_d1_badshape=$?"
emit_lines FAULT_D1_BADSHAPE "$OUT/fault-d1-badshape.log"
wait "$PFD0"; echo "MEASURE_RC fault_d0=$?"
emit_lines FAULT_D0 "$OUT/fault-d0.log"
rbln-smi >"$OUT/fault-post-smi.txt" 2>&1
echo "MEASURE_FAULT_POST_SMI_RC $?"
echo "FAULT_POST_SMI_BEGIN"
sed 's/\x1b\[[0-9;]*m//g' "$OUT/fault-post-smi.txt" 2>/dev/null | head -10 || true
echo "FAULT_POST_SMI_END"

echo "MEASURE_RESTART $SERVICE"
sudo systemctl start "$SERVICE" 2>/dev/null || true
sleep 3
# is-active 는 inactive/failed 에서도 stdout 에 상태를 찍고 exit 은 0 이 아니다 — `||` 로
# 묶으면 run.sh 파싱이 두 줄을 같이 삼킨다. 빈 문자열일 때만 unknown 으로 폴백.
SVC_NOW="$(systemctl is-active "$SERVICE" 2>/dev/null)"
[ -n "$SVC_NOW" ] || SVC_NOW="unknown"
echo "SERVICE_STATE $SVC_NOW"
echo "MEASURE_END"
