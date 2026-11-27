#!/usr/bin/env bash
# openkcloud 로컬 전체 기동 + 상시 워치독
#   사용법:  ./dev-all.sh
#   종료:    Ctrl+C  (전체 프로세스 트리 정리 후 종료)
#   로그:    .dev-logs/<서비스>.log  (예: tail -f .dev-logs/CORE.log)
#
# 동작: 백엔드 5개(.venv uvicorn --reload) + MNT-APP(npm run dev) + Temporal(설치돼 있으면)을
#       띄우고, 포그라운드에서 주기적으로 각 서비스를 헬스체크한다. 프로세스가 죽었거나
#       연속으로 응답이 없으면 해당 서비스만 트리째 kill 후 재기동한다.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="$ROOT/.dev-logs"
mkdir -p "$LOG_DIR"

INTERVAL=5            # 헬스체크 주기(초)
GRACE=25             # (재)기동 후 유예(초) — 이 시간 안에는 헬스 실패로 재시작하지 않음
FAILS_TO_RESTART=2   # 연속 헬스 실패가 이 횟수면 재기동

# ── 서비스 레지스트리 (index 공유 배열) ───────────────────────────────
NAMES=(); DIRS=(); PORTS=(); HPATHS=()   # HPATHS: 헬스 경로, "TCP"면 포트 연결만 확인
PIDS=(); STARTED=(); FAILC=(); ENABLED=()

add() {
  NAMES+=("$1"); DIRS+=("$2"); PORTS+=("$3"); HPATHS+=("$4")
  PIDS+=(0); STARTED+=(0); FAILC+=(0); ENABLED+=(1)
}

# Temporal 은 설치돼 있을 때만, 맨 앞에(다른 서비스가 의존)
if command -v temporal >/dev/null 2>&1; then
  add TEMPORAL "$ROOT/MNT-WORKFLOW" 7233 TCP
else
  echo "⚠️  temporal CLI 없음 → MNT-WORKFLOW(Temporal) 건너뜀 (필요하면 'brew install temporal' 후 재실행)"
fi

add CORE "$ROOT/MNT-CORE" 8000 /healthz
add IAAS "$ROOT/MNT-IAAS" 8001 /healthz
add PAAS "$ROOT/MNT-PAAS" 8002 /healthz
add OBS  "$ROOT/MNT-OBS"  8003 /healthz
add AINB "$ROOT/MNT-AINB" 8300 TCP
add APP  "$ROOT/MNT-APP"  3000 /

# ── 유틸 ──────────────────────────────────────────────────────────────
port_busy() { nc -z -w 1 127.0.0.1 "$1" >/dev/null 2>&1; }

health_ok() {  # $1=index
  local i=$1 port=${PORTS[$i]} hp=${HPATHS[$i]}
  if [ "$hp" = "TCP" ]; then
    nc -z -w 2 127.0.0.1 "$port" >/dev/null 2>&1
  else
    local code
    code=$(curl -s -o /dev/null -m 3 -w "%{http_code}" "http://127.0.0.1:$port$hp" 2>/dev/null)
    [ -n "$code" ] && [ "$code" != "000" ]
  fi
}

# 프로세스 트리 전체 종료(자식 먼저)
kill_tree() {
  local pid=$1 c
  { [ -n "$pid" ] && [ "$pid" -gt 0 ]; } 2>/dev/null || return 0
  for c in $(pgrep -P "$pid" 2>/dev/null); do kill_tree "$c"; done
  kill "$pid" 2>/dev/null || true
}

# 서비스 index 의 실행 커맨드 문자열
build_cmd() {  # $1=index
  local i=$1 name=${NAMES[$i]} dir=${DIRS[$i]} port=${PORTS[$i]}
  case "$name" in
    APP)      echo "cd '$dir' && exec npm run dev" ;;
    TEMPORAL) echo "cd '$dir' && exec ./run-local.sh" ;;
    *)
      local envarg=""
      if   [ -f "$dir/config.env" ];         then envarg="--env-file config.env"
      elif [ -f "$dir/config.env.example" ]; then envarg="--env-file config.env.example"; fi
      echo "cd '$dir' && exec .venv/bin/uvicorn app.main:app --reload --port $port $envarg"
      ;;
  esac
}

# 기동 전 사전점검 — 안 되면 ENABLED=0 (워치독에서 제외)
precheck() {  # $1=index
  local i=$1 name=${NAMES[$i]} dir=${DIRS[$i]} port=${PORTS[$i]}
  if [ ! -d "$dir" ]; then
    echo "⚠️  $name: 디렉토리 없음($dir) → 제외"; ENABLED[$i]=0; return
  fi
  case "$name" in
    APP)
      [ -x "$dir/node_modules/.bin/next" ] || [ -d "$dir/node_modules" ] || {
        echo "⚠️  $name: node_modules 없음 → 'npm install' 필요, 제외"; ENABLED[$i]=0; return; }
      ;;
    TEMPORAL) : ;;  # command -v 로 이미 확인됨
    *)
      [ -x "$dir/.venv/bin/uvicorn" ] || {
        echo "⚠️  $name: .venv/bin/uvicorn 없음 → venv 생성/설치 필요, 제외"; ENABLED[$i]=0; return; }
      ;;
  esac
  if port_busy "$port"; then
    echo "⚠️  $name: 포트 $port 가 이미 사용 중 → 외부 프로세스로 보고 관리 제외(중복 기동 방지)"
    ENABLED[$i]=0
  fi
}

start_one() {  # $1=index
  local i=$1 name=${NAMES[$i]} log="$LOG_DIR/${NAMES[$1]}.log"
  local cmd; cmd=$(build_cmd "$i")
  echo "▶️  $name 기동 (port ${PORTS[$i]}, 로그 $log)"
  bash -c "$cmd" >>"$log" 2>&1 &
  PIDS[$i]=$!
  STARTED[$i]=$SECONDS
  FAILC[$i]=0
}

cleanup() {
  echo ""
  echo "🛑 전체 종료 중…"
  local i
  for i in "${!NAMES[@]}"; do
    [ "${ENABLED[$i]}" -eq 1 ] && kill_tree "${PIDS[$i]}"
  done
  wait 2>/dev/null || true
  echo "완료."
  exit 0
}
trap cleanup INT TERM

# ── 기동 ──────────────────────────────────────────────────────────────
echo "== openkcloud 로컬 전체 기동 =="
rm -f "$LOG_DIR"/*.log 2>/dev/null   # 이전 실행 로그 전부 삭제(재실행마다 새로 쌓기)
for i in "${!NAMES[@]}"; do precheck "$i"; done

for i in "${!NAMES[@]}"; do
  [ "${ENABLED[$i]}" -eq 1 ] || continue
  start_one "$i"
  sleep 1                           # 로그·부하 분산을 위한 약간의 간격
done

echo ""
echo "모두 기동 요청 완료. 상태 요약:"
printf "  %-9s %-6s %s\n" "서비스" "포트" "비고"
for i in "${!NAMES[@]}"; do
  if [ "${ENABLED[$i]}" -eq 1 ]; then
    printf "  %-9s %-6s pid=%s\n" "${NAMES[$i]}" "${PORTS[$i]}" "${PIDS[$i]}"
  else
    printf "  %-9s %-6s (제외)\n" "${NAMES[$i]}" "${PORTS[$i]}"
  fi
done
echo ""
echo "프론트: http://localhost:3000   | 종료: Ctrl+C   | 로그: tail -f $LOG_DIR/<서비스>.log"
echo "워치독 시작 (${INTERVAL}s 주기)…"

# ── 워치독 ────────────────────────────────────────────────────────────
while true; do
  sleep "$INTERVAL"
  for i in "${!NAMES[@]}"; do
    [ "${ENABLED[$i]}" -eq 1 ] || continue
    pid=${PIDS[$i]}

    if ! kill -0 "$pid" 2>/dev/null; then
      echo "⚠️  $(date '+%H:%M:%S') ${NAMES[$i]} 프로세스 종료 감지 → 재기동"
      start_one "$i"
      continue
    fi

    # 기동 유예 중엔 헬스 판정 생략
    [ $((SECONDS - ${STARTED[$i]})) -lt "$GRACE" ] && continue

    if health_ok "$i"; then
      FAILC[$i]=0
    else
      FAILC[$i]=$(( ${FAILC[$i]} + 1 ))
      echo "… $(date '+%H:%M:%S') ${NAMES[$i]} 헬스체크 실패 (${FAILC[$i]}/${FAILS_TO_RESTART})"
      if [ "${FAILC[$i]}" -ge "$FAILS_TO_RESTART" ]; then
        echo "⚠️  $(date '+%H:%M:%S') ${NAMES[$i]} 응답 없음 → 트리 종료 후 재기동"
        kill_tree "$pid"
        start_one "$i"
      fi
    fi
  done
done
