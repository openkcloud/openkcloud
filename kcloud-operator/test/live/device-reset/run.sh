#!/usr/bin/env bash
# ============================================================
# run.sh: 벤더 장치 리셋 명령을 실제 노드에서 실행해 판정하는 하네스
# 상세: nvidia(<nvidia-node>)·tenstorrent(<tenstorrent-node>) 전용. 안전 게이트 뒤 리셋 전/후 상태를
#       evidence.txt 에 남긴다. PHASE 목록은 아래 case 문, FORCE_FAIL_AT 은 force_fail_check()
#       주석 참고.
# 용법: VENDOR=<nvidia|tenstorrent> [NODE=] [TARGET=] [DRY_RUN=1] [PHASE=] run.sh
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
export LC_ALL=C LANG=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$#" -ne 0 ]; then
  echo "인자를 받지 않는다 — VENDOR/NODE/TARGET/PHASE/DRY_RUN 환경변수로 지정" >&2
  exit 1
fi

PHASE="${PHASE:-all}"

# verdict <run-dir> — 그 실행 디렉터리에 남은 기계 파일만 읽어 판정 한 줄을 낸다. 클러스터·
# 노드에 접속하지 않는다 — VENDOR 조차 몰라도 된다. rc 는 reset-exit.txt(실제 종료 코드) 또는
# reset-ssh-lost.txt(SSH 유실로 종료 코드를 못 받았을 때 do_reset 이 남긴다) — 후자를 rc=1 로
# 지어내면 실제 exit=1 과 구분이 안 된다. devices 는 pre/post-devices.txt, node 는
# pre/post-boot-id.txt 비교(precheck·postcheck 가 남긴다) — 파일이 없으면 지어내지 않고
# missing/unknown 으로 낸다. RESET_REPRO 줄은 같은 계산에 sel_new·last_kernel_line 을 더한다.
verdict() {
  local dir="$1" rc devices node verdict_str="FAIL"
  if [ -f "$dir/reset-exit.txt" ]; then
    rc="$(cat "$dir/reset-exit.txt")"
  elif [ -f "$dir/reset-ssh-lost.txt" ]; then
    rc="ssh-lost"
  else
    rc="missing"
  fi
  if [ ! -f "$dir/post-devices.txt" ]; then
    devices="missing"
  elif diff -q "$dir/pre-devices.txt" "$dir/post-devices.txt" >/dev/null 2>&1; then
    devices="same"
  else
    devices="changed"
  fi
  if [ ! -f "$dir/pre-boot-id.txt" ] || [ ! -f "$dir/post-boot-id.txt" ]; then
    node="unknown"
  elif [ "$(cat "$dir/pre-boot-id.txt")" = "$(cat "$dir/post-boot-id.txt")" ]; then
    node="ready"
  else
    node="rebooted"
  fi
  if [ "$rc" = "0" ] && [ "$devices" = "same" ] && [ "$node" = "ready" ]; then verdict_str="PASS"; fi
  printf 'RESET_VERDICT: %s rc=%s devices=%s node=%s\n' "$verdict_str" "$rc" "$devices" "$node"

  local sel_new last_kernel_line
  if [ -f "$dir/sel-delta.txt" ]; then
    # grep -c 는 매치가 0건이어도 "0"을 정상 출력하면서 exit=1 을 낸다 — `$(grep ... ||
    # echo 0)` 로 쓰면 이미 나온 "0" 뒤에 폴백의 "0"이 또 붙어 값이 "0\n0"이 된다(이번
    # 라운드 DRY_RUN 오프라인 시험에서 실제로 재현). grep 실행을 먼저 끝내고 그 exit
    # 로만 폴백 여부를 가른다.
    if sel_new="$(grep -c '^>' "$dir/sel-delta.txt" 2>/dev/null)"; then :; else sel_new=0; fi
  else
    sel_new="missing"
  fi
  if [ ! -f "$dir/dmesg-live.log" ] || [ ! -f "$dir/reset-start-utc.txt" ]; then
    last_kernel_line="missing"
  else
    local file_mtime reset_epoch
    file_mtime="$(stat -c %Y "$dir/dmesg-live.log" 2>/dev/null || echo 0)"
    reset_epoch="$(date -u -d "$(cat "$dir/reset-start-utc.txt")" +%s 2>/dev/null || echo 0)"
    if [ "$reset_epoch" -gt 0 ] && [ "$file_mtime" -ge "$reset_epoch" ]; then
      last_kernel_line="yes"
    else
      last_kernel_line="no"
    fi
  fi
  printf 'RESET_REPRO: rc=%s node=%s sel_new=%s last_kernel_line=%s\n' "$rc" "$node" "$sel_new" "$last_kernel_line"
}

if [ "$PHASE" = "verdict" ]; then
  verdict "${RUN_DIR:?RUN_DIR 필요(runs/<RUN_ID>-<vendor> 디렉터리)}"
  exit 0
fi

VENDOR="${VENDOR:?VENDOR 필요(nvidia|tenstorrent)}"
DRY_RUN="${DRY_RUN:-0}"
RECOVER_TIMEOUT="${RECOVER_TIMEOUT:-300}"   # 장치 재열거 상한
ADVERTISE_TIMEOUT="${ADVERTISE_TIMEOUT:-300}"  # kubectl npu health 상한 5분
RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"

# vendor_conf — 벤더별 상수. 이 하네스가 다루는 벤더는 nvidia·tenstorrent 둘뿐이다
# (벤더가 리셋 명령을 노출하는 전부) — 그 밖의 벤더는 지원하지 않는다.
case "$VENDOR" in
  nvidia)
    NODE="${NODE:-k8s-worker1}"
    RESOURCE="${RESOURCE:-nvidia.com/gpu}"
    TARGET="${TARGET:-GPU-202cc280-df66-fed6-78b4-6782422adf5a}"  # A30, override로 A2 등 가능
    DP_NS="kube-system"
    # /proc/*/fd 로 확인: nvidia-device-plugin-flat·dcgm-exporter 둘 다 /dev/nvidia0 를 연
    # 채로 들고 있다 — 이름이 "device-plugin"인 것만이 아니라 실제로 핸들을 쥔 것 둘 다
    # 좁혀서 적는다.
    DP_LABELS=("app.kubernetes.io/name=nvidia-device-plugin-flat" "app.kubernetes.io/name=dcgm-exporter")
    ;;
  tenstorrent)
    NODE="${NODE:-k8s-worker3}"
    RESOURCE="${RESOURCE:-tenstorrent.com/blackhole}"
    TARGET="${TARGET:-0}"
    DP_NS="kcloud"
    DP_LABELS=("app.kubernetes.io/name=kcloud-tt-device-plugin")
    ;;
  *)
    echo "지원하지 않는 VENDOR: $VENDOR (nvidia|tenstorrent 만 이 하네스가 다룬다)" >&2
    exit 1
    ;;
esac

OUT_DIR="${SCRIPT_DIR}/runs/${RUN_ID}-${VENDOR}"
mkdir -p "$OUT_DIR"
EVIDENCE="${OUT_DIR}/evidence.txt"

log() { printf '[%s] %s\n' "$(date -u +%T)" "$*" >&2; }

# force_fail_check <지점 이름> — FORCE_FAIL_AT 시험 훅. 일치하면 강제로 exit 1 해
# trap cleanup 을 라이브 없이(DRY_RUN=1 과 함께) 확인할 수 있게 한다.
force_fail_check() {
  if [ "${FORCE_FAIL_AT:-}" = "$1" ]; then
    echo "FORCE_FAIL_AT=$1 — 시험 훅으로 강제 실패" >&2
    exit 1
  fi
}

# DID_CORDON — 이 실행이 quiesce() 로 cordon 을 걸었는지. cleanup 이 "이 실행이 직접
# 바꾼 것만" 되돌리려면 남이 이미 cordon 한 노드를 이 실행이 되돌려선 안 된다.
DID_CORDON=0

# LIVE_DMESG_PID/LIVE_JOURNAL_PID — start_live_capture() 가 기동한 백그라운드 SSH 의
# PID. stop_live_capture() 가 별도 함수 호출로 참조해야 해서(함수 지역 변수는 다른
# 함수로 넘어가지 않는다) DID_CORDON 과 같은 방식으로 스크립트 전역에 둔다.
LIVE_DMESG_PID=""
LIVE_JOURNAL_PID=""

# cleanup <rc> — trap cleanup EXIT. 정상 종료(rc=0)면 아무것도 하지 않는다. 비정상
# 종료면 이 실행이 만든 것만 되돌린다: cordon 했으면 uncordon, workload_check 의 시험
# 파드가 남아 있으면 삭제(이름이 이 하네스 전용 규칙이라 다른 자원과 겹치지 않는다 —
# --ignore-not-found 라 애초에 없어도 안전하다). 한 단계 실패가 다른 단계를 막지 않게
# set +e 아래에서 단계별로 기록한다.
cleanup() {
  local rc="$1"
  [ "$rc" = "0" ] && return 0
  set +e
  # 실시간 캡처 ssh 가 남아 있으면(do_reset 안에서 set -e 로 중단된 드문 경로) 여기서 닫는다.
  if [ -n "${LIVE_DMESG_PID:-}" ] || [ -n "${LIVE_JOURNAL_PID:-}" ]; then
    stop_live_capture
  fi
  log "cleanup: 비정상 종료(rc=$rc) — 이 실행이 바꾼 것만 되돌린다"
  if [ "$DID_CORDON" = "1" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN(cleanup): kubectl uncordon $NODE" >&2
    else
      kubectl uncordon "$NODE" >&2
      log "cleanup: uncordon $NODE 완료"
    fi
  fi
  local pod="device-reset-check-${VENDOR}"
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN(cleanup): kubectl delete pod $pod -n default --ignore-not-found" >&2
  else
    kubectl delete pod "$pod" -n default --ignore-not-found --wait=false >&2
    log "cleanup: 시험 파드 $pod 정리 시도 완료(없었으면 no-op)"
  fi
}
trap 'cleanup $?' EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# evid <제목> — stdin 을 evidence.txt 에 구분선과 함께 append. 화면에도 그대로 보여준다
# (tee) — 라이브 실행 중에도 진행을 볼 수 있고, 파일은 사후 evidence.sh 입력이 된다.
evid() {
  { printf '\n=== %s (%s UTC) ===\n' "$1" "$(date -u +%FT%TZ)"; cat; } | tee -a "$EVIDENCE" >&2
}

# 노드 SSH 계정. 소스에 박지 않는다 — 공개 저장소에 내부 계정이 실리면 안 된다.
SSH_USER="${SSH_USER:-ubuntu}"
NODE_IP="$(kubectl get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
if [ -z "$NODE_IP" ]; then
  echo "노드 ${NODE} 의 InternalIP 를 찾지 못했다" >&2
  exit 1
fi

# remote <원격 명령> — ssh 로 실행하고 stdout+stderr 를 합쳐 돌려준다. set -e 로 죽지
# 않도록 호출부에서 var=$(remote ...) 형태로 받고 $? 를 직접 확인한다.
# ServerAliveInterval/CountMax 로 원격 호스트가 응답을 멈추면 약 6초 안에 255 로
# 끝난다(리셋 급정지 뒤 5분 대기 방지).
remote() {
  ssh -o BatchMode=yes -o ConnectTimeout=5 \
    -o ServerAliveInterval=2 -o ServerAliveCountMax=3 \
    "${SSH_USER}@${NODE_IP}" "$1" 2>&1
}

# device_list_cmd / device_count_cmd — 벤더별 장치 목록·개수 확인 원격 명령 문자열.
# 개수는 온도·메모리처럼 계속 바뀌는 필드가 아니라 "장치 노드가 몇 개 보이는가"만 세어
# 리셋 전후 비교를 안정적으로 만든다(같은 장치 목록·같은 PCI 주소).
device_list_cmd() {
  case "$VENDOR" in
    nvidia) echo "sudo /usr/bin/nvidia-smi -L; sudo /usr/bin/nvidia-smi --query-gpu=index,uuid,name,pci.bus_id,persistence_mode,temperature.gpu,memory.used,memory.total,driver_version --format=csv" ;;
    tenstorrent) echo "/home/kcloud/.local/bin/tt-smi -ls" ;;
  esac
}
device_count_cmd() {
  case "$VENDOR" in
    nvidia) echo "sudo /usr/bin/nvidia-smi -L | grep -c '^GPU'" ;;
    tenstorrent) echo "ls /dev/tenstorrent | grep -cE '^[0-9]+\$'" ;;
  esac
}
reset_cmd() {
  case "$VENDOR" in
    nvidia) echo "sudo /usr/bin/nvidia-smi -i ${TARGET} -r" ;;
    tenstorrent) echo "/home/kcloud/.local/bin/tt-smi -r ${TARGET}" ;;
  esac
}
# device_check_cmd — workload_check 시험 파드 안에서 실행할 명령. 파드가 Running 되는
# 것만으로는 "장치를 시험 워크로드로 쓸 수 있다"가 성립하지 않는다 — 로그에 장치 파일
# 자체가 보여야 한다.
device_check_cmd() {
  case "$VENDOR" in
    nvidia) echo "ls -la /dev/nvidia* 2>&1" ;;
    tenstorrent) echo "ls -la /dev/tenstorrent/* 2>&1" ;;
  esac
}

# pod_count_using_resource — 그 노드에서 그 벤더 확장 자원을 requests 나 limits 에 담은
# 파드 수(확장 자원은 k8s 가 요구, 이 둘은 항상 같다 — 어느 쪽으로 선언했든 잡는다).
pod_count_using_resource() {
  kubectl get pods -A --field-selector "spec.nodeName=${NODE}" -o json | \
    jq --arg r "$RESOURCE" '[.items[] | select(.spec.containers[]? |
      ((.resources.requests // {}) | has($r)) or ((.resources.limits // {}) | has($r)))] | length'
}

safety_gate() {
  local count
  count="$(pod_count_using_resource)"
  log "안전 게이트: node=$NODE resource=$RESOURCE 요구 파드 수=$count"
  if [ "$count" -ne 0 ]; then
    echo "안전 게이트 실패: ${NODE} 에 ${RESOURCE} 를 요구하는 파드 ${count}개 — 중단" >&2
    kubectl get pods -A --field-selector "spec.nodeName=${NODE}" -o json | \
      jq --arg r "$RESOURCE" -r '.items[] | select(.spec.containers[]? |
        ((.resources.requests // {}) | has($r)) or ((.resources.limits // {}) | has($r))) |
        .metadata.namespace + "/" + .metadata.name' >&2
    return 1
  fi
  printf 'node=%s resource=%s 요구 파드=%s(안전)\n' "$NODE" "$RESOURCE" "$count" | evid "SAFETY GATE"
}

ndr_count() {
  kubectl get nodedevicereport "$NODE" -o json 2>/dev/null | \
    jq --arg v "$VENDOR" '[.status.devices[]? | select(.vendor==$v)] | length'
}
health_state() {
  kubectl get acceleratorhealth -A -o json | \
    jq -r --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)][0].status.state // "미수집"'
}
dus_state() {
  kubectl get driverupgradestate "${NODE}-${VENDOR}" -o jsonpath='{.status.state}' 2>/dev/null || echo "미수집"
}
# device_plugin_ready — 이 벤더의 device-plugin(류) 파드(DP_LABELS)가 그 노드에서 전부
# Ready 인지. 리셋 전 상태로 돌아온다는 성공 기준에 device-plugin 파드
# 상태도 포함되므로 NDR·health·DUS 와 함께 본다.
device_plugin_ready() {
  local l
  for l in "${DP_LABELS[@]}"; do
    local ready total
    ready="$(kubectl get pod -n "$DP_NS" -l "$l" --field-selector "spec.nodeName=${NODE}" -o json | \
      jq '[.items[] | select(.status.phase=="Running") | select([.status.containerStatuses[]?.ready] | all)] | length')"
    total="$(kubectl get pod -n "$DP_NS" -l "$l" --field-selector "spec.nodeName=${NODE}" -o json | jq '.items | length')"
    if [ "$total" -eq 0 ] || [ "$ready" != "$total" ]; then
      echo "no($l:${ready:-0}/${total:-0})"
      return
    fi
  done
  echo "yes"
}

precheck() {
  safety_gate
  force_fail_check precheck
  local list dmesg_n count
  list="$(remote "$(device_list_cmd)")"
  printf '%s\n' "$list" | evid "PRE-RESET DEVICE LIST"
  echo "$list" > "${OUT_DIR}/pre-devices.txt"
  count="$(remote "$(device_count_cmd)" | tr -d '[:space:]')"
  echo "$count" > "${OUT_DIR}/pre-device-count.txt"
  ndr_count > "${OUT_DIR}/pre-ndr-count.txt"
  # boot_id: PHASE=verdict 의 node 판정 기준선 — postcheck 가 남기는
  # post-boot-id.txt 와 비교해 재부팅 여부를 기계적으로 가른다.
  remote 'cat /proc/sys/kernel/random/boot_id' > "${OUT_DIR}/pre-boot-id.txt"
  dmesg_n="$(remote "sudo dmesg | wc -l")"
  echo "$dmesg_n" > "${OUT_DIR}/pre-dmesg-lines.txt"
  log "precheck 완료: 장치 개수=${count} dmesg 기준선=${dmesg_n}줄"
}

# start_live_capture/stop_live_capture — do_reset() 가 실제 리셋 직전에 원격 dmesg -w -T·
# journalctl -kf 를 백그라운드 SSH 로 열어 로컬 파일에 흘려 둔다. 리셋 중 노드가 죽어
# SSH 가 끊기면 이 두 파일의
# 마지막 줄이 크래시 직전 커널 기록이다 — 원격 디스크 flush 에 기대지 않고 스트림
# 자체를 로컬로 받는다. ServerAliveInterval/CountMax(합 4초)를 짧게 잡아 연결이
# 끊겼을 때 로컬 ssh 클라이언트도 빨리 죽는다.
start_live_capture() {
  ssh -o BatchMode=yes -o ServerAliveInterval=2 -o ServerAliveCountMax=2 \
    "${SSH_USER}@${NODE_IP}" 'sudo dmesg -w -T' > "${OUT_DIR}/dmesg-live.log" 2>&1 &
  LIVE_DMESG_PID=$!
  ssh -o BatchMode=yes -o ServerAliveInterval=2 -o ServerAliveCountMax=2 \
    "${SSH_USER}@${NODE_IP}" 'sudo journalctl -kf -o short-iso --no-pager' > "${OUT_DIR}/journal-live.log" 2>&1 &
  LIVE_JOURNAL_PID=$!
  log "실시간 커널 로그 캡처 기동: dmesg pid=${LIVE_DMESG_PID} journal pid=${LIVE_JOURNAL_PID}"
  sleep 2   # 두 SSH 세션이 자리잡을 시간(리셋 명령 전 스트리밍이 확실히 시작하도록)
}

# stop_live_capture — do_reset() 의 모든 반환 경로(성공·비정상 rc·SSH 유실)에서 직접
# 호출한다. trap RETURN 은 쓰지 않는다 — 함수 지역이 아니라 스크립트 전역으로 걸려
# do_reset() 이후에 호출되는 다른 함수의 반환에도 잘못 다시 발동하기 때문이다.
stop_live_capture() {
  local pid
  for pid in "$LIVE_DMESG_PID" "$LIVE_JOURNAL_PID"; do
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  LIVE_DMESG_PID=""
  LIVE_JOURNAL_PID=""
  log "실시간 커널 로그 캡처 정리 완료"
}

do_reset() {
  safety_gate
  force_fail_check reset
  local cmd; cmd="$(reset_cmd)"
  log "리셋 명령: $cmd"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s "%s"\n' "$SSH_USER" "$NODE_IP" "$cmd" | evid "RESET COMMAND (DRY_RUN)"
    printf 'DRY_RUN: 실시간 커널 로그 캡처(dmesg -w/journalctl -kf) 기동 생략\n' | evid "LIVE-CAPTURE (DRY_RUN)"
    return 0
  fi
  start_live_capture
  # reset-start-utc.txt: PHASE=verdict 의 last_kernel_line 판정 기준시 — 리셋
  # 명령을 실제로 보내기 직전 시각.
  date -u +%FT%TZ > "${OUT_DIR}/reset-start-utc.txt"
  local out ssh_rc rc
  if out="$(remote "$cmd; echo __RESET_RC=\$?")"; then ssh_rc=0; else ssh_rc=$?; fi
  rc="$(printf '%s\n' "$out" | sed -n 's/^__RESET_RC=\([0-9][0-9]*\)$/\1/p' | tail -1)"
  out="$(printf '%s\n' "$out" | sed '/^__RESET_RC=[0-9][0-9]*$/d')"
  if [ -z "$rc" ]; then
    # 마커 줄이 없다 — SSH/채널이 끊겨 원격 명령의 실제 종료 코드를 못 받았다(ssh 자체
    # exit=$ssh_rc, 보통 255). rc=1 로 지어내지 않는다 — reset-exit.txt 는 실제로 받은
    # 종료 코드에만 쓰고, 이 경우는 별도 마커 파일로 남겨 PHASE=verdict 가 "0이 아닌
    # 코드로 끝났다"와 "연결이 끊겨 모른다"를 구분하게 한다(rc=missing/ssh-lost 대 rc=<n>).
    printf '$ %s\n%s\nssh 종료(exit=%s), 원격 명령의 실제 종료 코드는 받지 못함\n' "$cmd" "$out" "$ssh_rc" | evid "RESET COMMAND"
    echo "$ssh_rc" > "${OUT_DIR}/reset-ssh-lost.txt"
    stop_live_capture
    return 1
  fi
  printf '$ %s\n%s\nexit=%s\n' "$cmd" "$out" "$rc" | evid "RESET COMMAND"
  echo "$rc" > "${OUT_DIR}/reset-exit.txt"
  stop_live_capture
  if [ "$rc" != "0" ]; then
    echo "리셋 명령이 0 이 아닌 코드로 끝났다: $rc — 아래는 원문" >&2
    return 1
  fi
}

wait_recover() {
  force_fail_check wait-recover
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_recover 생략(리셋을 안 했으므로 기다릴 것이 없다)" | evid "WAIT-RECOVER (DRY_RUN)"
    return 0
  fi
  local want elapsed=0 interval=5 got
  # want: precheck 가 리셋 전에 남긴 장치 개수(pre-device-count.txt). 리셋 직후 값은
  # 재초기화 중이라 불안정하므로 목표를 그 시점에 다시 재는 것이 아니라 리셋 전 기준선을
  # 그대로 쓴다.
  want="$(cat "${OUT_DIR}/pre-device-count.txt" 2>/dev/null || echo "")"
  if [ -z "$want" ]; then
    echo "pre-device-count.txt 없음 — precheck 를 먼저 실행해야 한다" >&2
    return 1
  fi
  while [ "$elapsed" -lt "$RECOVER_TIMEOUT" ]; do
    got="$(remote "$(device_count_cmd)" 2>/dev/null | tr -d '[:space:]' || echo 0)"
    log "장치 재열거 대기(${elapsed}s): 목표=${want} 현재=${got}"
    if [ "$got" = "$want" ] && [ "$got" != "0" ]; then
      printf '장치 재열거 확인: %s개, 경과 %s초\n' "$got" "$elapsed" | evid "WAIT-RECOVER"
      echo "$elapsed" > "${OUT_DIR}/recover-elapsed.txt"
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  printf '장치 재열거 실패: 목표=%s 마지막 관측=%s, 상한 %s초 초과\n' "$want" "$got" "$RECOVER_TIMEOUT" | evid "WAIT-RECOVER"
  return 1
}

postcheck() {
  force_fail_check postcheck
  local list dmesg_n0 delta
  list="$(remote "$(device_list_cmd)")"
  printf '%s\n' "$list" | evid "POST-RESET DEVICE LIST"
  echo "$list" > "${OUT_DIR}/post-devices.txt"
  remote 'cat /proc/sys/kernel/random/boot_id' > "${OUT_DIR}/post-boot-id.txt"
  dmesg_n0="$(cat "${OUT_DIR}/pre-dmesg-lines.txt" 2>/dev/null || echo 0)"
  delta="$(remote "sudo dmesg | tail -n +$((dmesg_n0 + 1))")"
  printf '%s\n' "$delta" | evid "DMESG DELTA(리셋 이후 새 줄)"
}

wait_advertise() {
  force_fail_check wait-advertise
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_advertise 생략" | evid "WAIT-ADVERTISE (DRY_RUN)"
    return 0
  fi
  local want elapsed=0 interval=10 ndr health dus dus_bad="" dp
  if [ ! -f "${OUT_DIR}/pre-ndr-count.txt" ]; then
    echo "pre-ndr-count.txt 없음 — precheck(리셋 전)를 먼저 실행해야 한다" >&2
    return 1
  fi
  want="$(cat "${OUT_DIR}/pre-ndr-count.txt")"
  while [ "$elapsed" -lt "$ADVERTISE_TIMEOUT" ]; do
    ndr="$(ndr_count)"; health="$(health_state)"; dus="$(dus_state)"; dp="$(device_plugin_ready)"
    log "광고 복구 대기(${elapsed}s): NDR=${ndr}(목표 ${want}) health=${health} dus=${dus} device-plugin=${dp}"
    if [ "$dus" != "Idle" ]; then dus_bad="$dus"; fi
    if [ "$ndr" = "$want" ] && [ "$health" = "Healthy" ] && [ "$dp" = "yes" ]; then
      printf 'NDR=%s(목표 %s) health=%s dus=%s(경로상 이탈=%s) device-plugin=%s 경과 %s초\n' \
        "$ndr" "$want" "$health" "$dus" "${dus_bad:-없음}" "$dp" "$elapsed" | evid "WAIT-ADVERTISE"
      echo "$elapsed" > "${OUT_DIR}/advertise-elapsed.txt"
      if [ -n "$dus_bad" ]; then
        echo "DUS 가 대기 중 Idle 이 아닌 상태(${dus_bad})를 거쳤다 — 성공 기준 위반" >&2
        return 1
      fi
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  printf 'NDR=%s(목표 %s) health=%s device-plugin=%s, 상한 %s초 초과\n' "$ndr" "$want" "$health" "$dp" "$ADVERTISE_TIMEOUT" | evid "WAIT-ADVERTISE"
  return 1
}

# pick_test_image — 이 노드에 이미 떠 있는 kube-system 표준 파드 중 셸(sh)과 ls 가 둘 다
# 있는 이미지를 exec 로 확인해 고른다 — 새 이미지를 pull 하지 않는다. busybox 같은 고정
# 이미지를 쓰면 레지스트리 pull 실패 시 재실행성이 떨어지므로 피한다(test/live/dp-dra-residue/
# run.sh 의 같은 이름 함수와 같은 방식).
pick_test_image() {
  local name pod img
  for name in nginx-proxy calico-node kube-proxy nodelocaldns; do
    pod="$(kubectl get pods -n kube-system --field-selector "spec.nodeName=${NODE}" -o json | \
      jq -r --arg p "$name" '.items[] | select(.metadata.name | startswith($p)) | .metadata.name' | head -1)"
    [ -z "$pod" ] && continue
    if kubectl exec -n kube-system "$pod" -c "$name" -- sh -c 'command -v ls' >/dev/null 2>&1; then
      img="$(kubectl get pod "$pod" -n kube-system -o jsonpath='{.spec.containers[0].image}')"
      echo "$img"
      return 0
    fi
  done
  return 1
}

workload_check() {
  safety_gate
  force_fail_check workload-check
  local pod="device-reset-check-${VENDOR}" check_cmd; check_cmd="$(device_check_cmd)"
  local image
  if [ "$DRY_RUN" = "1" ]; then
    image="(라이브에서 pick_test_image 로 결정)"
  else
    image="$(pick_test_image)" || { echo "셸 있는 이미지를 못 찾아 workload-check 를 건너뛴다" >&2; return 1; }
    log "시험 이미지(workload-check): $image"
  fi
  local manifest
  manifest="$(cat <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: ${pod}
  namespace: default
spec:
  nodeName: ${NODE}
  restartPolicy: Never
  containers:
  - name: check
    image: ${image}
    command: ["sh", "-c", "${check_cmd}"]
    resources:
      requests: {"${RESOURCE}": "1"}
      limits: {"${RESOURCE}": "1"}
YAML
)"
  if [ "$DRY_RUN" = "1" ]; then
    printf '%s\n' "$manifest" | evid "WORKLOAD-CHECK (DRY_RUN, 적용 안 함)"
    return 0
  fi
  printf '%s\n' "$manifest" | kubectl apply -f - >&2
  # Pending 을 벗어남(5분 상한)을 관문으로 삼는다. 이
  # 명령(ls)은 거의 즉시 끝나 kubectl wait --for=...=Running 의 watch 가 그 찰나를
  # 놓치고 이미 Succeeded 로 넘어간 뒤일 수 있다(실제로 관측 — phase=Succeeded 인데 wait 는
  # timeout 으로 실패해 로그를 못 건졌다).
  # Running·Succeeded·Failed 어느 쪽이든 "시작은 했다"로 보고, 로그는 지우기 전에
  # 반드시 먼저 받는다.
  local elapsed=0 phase=""
  while [ "$elapsed" -lt 300 ]; do
    phase="$(kubectl get pod "${pod}" -n default -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    case "$phase" in Running|Succeeded|Failed) break ;; esac
    sleep 2
    elapsed=$((elapsed + 2))
  done
  case "$phase" in
    Running|Succeeded|Failed)
      local logs; logs="$(kubectl logs "${pod}" -n default 2>&1)"
      printf '파드 %s phase=%s(%s초 이내 Pending 벗어남). 로그(장치 파일 확인):\n%s\n' \
        "$pod" "$phase" "$elapsed" "$logs" | evid "WORKLOAD-CHECK"
      kubectl delete pod "${pod}" -n default --wait=true >&2
      return 0
      ;;
  esac
  local events; events="$(kubectl get events -n default --field-selector "involvedObject.name=${pod}" 2>&1)"
  printf '파드 %s 가 300초 안에 Pending 을 벗어나지 못함\n이벤트:\n%s\n' "$pod" "$events" | evid "WORKLOAD-CHECK"
  kubectl delete pod "${pod}" -n default --wait=false --ignore-not-found >&2 || true
  return 1
}

# bmc_sel — ipmitool 로 BMC SEL 을 읽는다. <tenstorrent-node>
# 만 허용(닫힌 목록) — 다른 노드(예: VENDOR=nvidia 로 잘못 호출)면 그 자리에서 거절한다.
# 실행하는 명령은 읽기 4종뿐이다: sel elist(sel-<stage>.txt 로
# 저장 — before/after diff 대상은 이 출력만), sel info·mc info·chassis status(evidence.txt
# 로만, 참고용). ipmitool power·chassis power·sel clear 같은 쓰기 명령은 없다.
# SEL_STAGE=after 이고 sel-before.txt 가 이미 있으면 diff 로 sel-delta.txt 를 만든다.
bmc_sel() {
  local stage="${SEL_STAGE:-before}"
  if [ "$NODE" != "k8s-worker3" ]; then
    echo "PHASE=bmc-sel 은 k8s-worker3 에서만 허용한다 — NODE=${NODE}" >&2
    return 1
  fi
  local installed
  installed="$(remote 'command -v ipmitool >/dev/null 2>&1 && echo yes || echo no' | tr -d '[:space:]')"
  if [ "$installed" = "yes" ]; then
    echo "ipmitool 이미 설치됨 — 설치 생략" | evid "BMC-SEL install"
  elif [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s "sudo apt-get install -y ipmitool"(실행 안 함)\nipmitool 미설치 상태라 SEL 조회는 생략한다(설치했다고 가정하지 않는다)\n' "$SSH_USER" "$NODE_IP" | evid "BMC-SEL (DRY_RUN)"
    return 0
  else
    remote 'sudo apt-get install -y ipmitool' | evid "BMC-SEL install"
  fi
  local elist info mcinfo chassis
  elist="$(remote 'sudo ipmitool sel elist')"
  printf '%s\n' "$elist" > "${OUT_DIR}/sel-${stage}.txt"
  printf '%s\n' "$elist" | evid "BMC-SEL sel elist (${stage})"
  info="$(remote 'sudo ipmitool sel info')"
  printf '%s\n' "$info" | evid "BMC-SEL sel info (${stage})"
  mcinfo="$(remote 'sudo ipmitool mc info')"
  printf '%s\n' "$mcinfo" | evid "BMC-SEL mc info (${stage})"
  chassis="$(remote 'sudo ipmitool chassis status')"
  printf '%s\n' "$chassis" | evid "BMC-SEL chassis status (${stage})"
  if [ "$stage" = "after" ] && [ -f "${OUT_DIR}/sel-before.txt" ]; then
    diff "${OUT_DIR}/sel-before.txt" "${OUT_DIR}/sel-after.txt" > "${OUT_DIR}/sel-delta.txt" || true
    evid "SEL DELTA(elist, before vs after, diff '>' = 신규)" < "${OUT_DIR}/sel-delta.txt"
  fi
}

# quiesce/unquiesce — 좁은 조치: cordon(신규 비-DaemonSet 스케줄 차단) +
# 그 벤더 device-plugin DS 파드 삭제(장치를 다시 열기 전까지의 짧은 창을 만든다). reset 이
# "장치 사용 중"으로 막힐 때만 판단해서 쓰는 조치라 all 에는 넣지 않는다. cordon 은 DaemonSet
# 기본 toleration(unschedulable) 때문에 재스케줄을 막지 못한다 — 실제 효과는 파드 삭제가
# 만드는 창뿐이다. **허용 조치는 이 cordon + 해당 DS 파드 삭제뿐이다** — kill -9·커스텀
# taint·라벨 재사용 등은 컨트롤러 승인 없이 하지 않는다(실제로 세 차례 시도돼 전부 되돌렸다).
quiesce() {
  safety_gate
  force_fail_check quiesce
  if [ "$DRY_RUN" = "1" ]; then
    local l; for l in "${DP_LABELS[@]}"; do
      printf 'DRY_RUN: kubectl cordon %s && kubectl -n %s delete pod -l %s\n' "$NODE" "$DP_NS" "$l"
    done | evid "QUIESCE (DRY_RUN)"
    DID_CORDON=1
    return 0
  fi
  kubectl cordon "$NODE" >&2
  DID_CORDON=1
  local l before=""
  for l in "${DP_LABELS[@]}"; do
    before="${before}$(kubectl get pod -n "$DP_NS" -l "$l" --field-selector "spec.nodeName=${NODE}" -o name)"$'\n'
    kubectl -n "$DP_NS" delete pod -l "$l" --field-selector "spec.nodeName=${NODE}" --ignore-not-found >&2
  done
  printf 'cordon %s + 삭제된 파드(%s):\n%s\n' "$NODE" "${DP_LABELS[*]}" "$before" | evid "QUIESCE"
}
# unquiesce 는 quiesce 를 되돌리는 동작이라 safety_gate 를 걸지 않는다 — 되돌리기를
# "장치를 쓰는 파드가 생겼다"는 이유로 막으면 cordon 이 풀리지 않고 남는다.
unquiesce() {
  force_fail_check unquiesce
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl uncordon %s\n' "$NODE" | evid "UNQUIESCE (DRY_RUN)"
    return 0
  fi
  kubectl uncordon "$NODE" >&2
  printf 'uncordon %s\n' "$NODE" | evid "UNQUIESCE"
}

reboot_recover() {
  safety_gate
  force_fail_check reboot-recover
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s sudo reboot\n' "$SSH_USER" "$NODE_IP" | evid "REBOOT-RECOVER (DRY_RUN)"
    return 0
  fi
  local bootid_before bootid_after elapsed=0
  bootid_before="$(remote 'cat /proc/sys/kernel/random/boot_id')"
  printf '재부팅 전 boot_id=%s\n' "$bootid_before" | evid "REBOOT-RECOVER"
  ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_USER}@${NODE_IP}" 'sudo reboot' >&2 || true
  sleep 15
  while [ "$elapsed" -lt 300 ]; do
    if bootid_after="$(remote 'cat /proc/sys/kernel/random/boot_id' 2>/dev/null)" && [ -n "$bootid_after" ] && [ "$bootid_after" != "$bootid_before" ]; then
      printf '재부팅 완료: boot_id %s -> %s, 경과 %s초(재부팅 뒤 최초 응답까지)\n' "$bootid_before" "$bootid_after" "$elapsed" | evid "REBOOT-RECOVER"
      return 0
    fi
    sleep 10
    elapsed=$((elapsed + 10))
  done
  echo "재부팅 뒤 300초 내 응답 없음" >&2
  return 1
}

main() {
  safety_gate
  precheck
  do_reset
  wait_recover
  postcheck
  wait_advertise
  workload_check
  log "요약: evidence=${EVIDENCE}"
}

case "$PHASE" in
  safety-gate) safety_gate ;;
  precheck) precheck ;;
  reset) do_reset ;;
  wait-recover) wait_recover ;;
  postcheck) postcheck ;;
  wait-advertise) wait_advertise ;;
  workload-check) workload_check ;;
  quiesce) quiesce ;;
  unquiesce) unquiesce ;;
  reboot-recover) reboot_recover ;;
  bmc-sel) bmc_sel ;;
  all) main ;;
  *) echo "알 수 없는 PHASE: $PHASE" >&2; exit 1 ;;
esac
