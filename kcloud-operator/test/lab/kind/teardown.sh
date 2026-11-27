#!/usr/bin/env bash
# ============================================================
# teardown.sh: kind 실험 클러스터(kcloud-lab) 정리 — operator 프로세스 + 클러스터 삭제
# 상세: setup.sh 가 만든 kind 클러스터와 백그라운드 operator(make run) 프로세스를 지운다.
#       operator 는 setsid 로 띄워 PID==PGID 라 프로세스 그룹째 신호를 보내 한 번에
#       정리된다. 클러스터·operator 가 이미 없어도 성공(exit 0)하고, operator.log 는
#       사후 확인용으로 지우지 않는다. DRY_RUN=1 지원.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-kcloud-lab}"
LAB_DIR="${LAB_DIR:-/tmp/kcloud-lab}"
OPERATOR_PID_FILE="$LAB_DIR/operator.pid"
KUBECONFIG_PATH="$LAB_DIR/kubeconfig"

DRY_RUN="${DRY_RUN:-0}"

log() { printf '%s\n' "$*" >&2; }

if [ "$DRY_RUN" = "1" ]; then
  cat >&2 <<EOF
+ kill -TERM -- -\$(cat $OPERATOR_PID_FILE)   # PID 파일 있을 때. 없으면 이 LAB_DIR 로 띄운 프로세스만 pgrep -f 로 찾아 보조 정리
+ rm -f $OPERATOR_PID_FILE
+ sudo kind delete cluster --name $CLUSTER_NAME
EOF
  exit 0
fi

# --- 1. operator 프로세스 그룹 정리 ------------------------------------------
if [ -f "$OPERATOR_PID_FILE" ]; then
  pid="$(cat "$OPERATOR_PID_FILE")"
  if kill -0 "$pid" 2>/dev/null; then
    log "operator 프로세스 그룹(PGID $pid) 종료(SIGTERM)"
    kill -TERM -- "-$pid" 2>/dev/null || true
    tries=0
    while kill -0 "$pid" 2>/dev/null && [ "$tries" -lt 10 ]; do
      sleep 1
      tries=$((tries + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
      log "SIGTERM 후에도 살아있음 — SIGKILL"
      kill -KILL -- "-$pid" 2>/dev/null || true
    fi
  else
    log "operator PID $pid 이미 종료됨"
  fi
  rm -f "$OPERATOR_PID_FILE"
else
  log "PID 파일 없음 — 이 LAB_DIR($LAB_DIR)로 띄운 프로세스만 찾아 보조 정리 시도"
  # go run 의 명령줄 자체에는 LAB_DIR 정보가 없다 — env KUBECONFIG=... 가 make 를 exec
  # 하는 순간 그 값은 자식 명령줄(argv)이 아니라 환경변수로만 넘어간다(exec 는 argv 를
  # 통째로 바꾸지만 환경은 물려준다). 그래서 명령줄로는 "go run ./cmd/main.go" 후보만
  # 추리고, 후보 각각의 /proc/PID/environ 을 봐서 이 LAB_DIR 의 KUBECONFIG 값을 가진
  # 것만 그 프로세스 그룹째 종료한다 — 다른 LAB_DIR 나 사람이 띄운 make run 은 값이
  # 다르므로 걸리지 않는다.
  fallback_pid=""
  for cand in $(pgrep -f "go run ./cmd/main.go" 2>/dev/null); do
    if tr '\0' '\n' <"/proc/$cand/environ" 2>/dev/null | grep -qxF "KUBECONFIG=$KUBECONFIG_PATH"; then
      fallback_pid="$cand"
      break
    fi
  done
  if [ -n "$fallback_pid" ]; then
    fallback_pgid="$(ps -o pgid= -p "$fallback_pid" 2>/dev/null | tr -d ' ')"
    if [ -n "$fallback_pgid" ]; then
      log "이 LAB_DIR 의 operator 프로세스 그룹(PGID $fallback_pgid) 발견 — 종료(SIGTERM)"
      kill -TERM -- "-$fallback_pgid" 2>/dev/null || true
    fi
  else
    log "이 LAB_DIR 로 띄운 operator 프로세스를 찾지 못함"
  fi
fi

# --- 2. kind 클러스터 삭제 ----------------------------------------------------
if sudo kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "kind 클러스터 $CLUSTER_NAME 삭제"
  sudo kind delete cluster --name "$CLUSTER_NAME"
else
  log "kind 클러스터 $CLUSTER_NAME 이미 없음"
fi

log "완료"
