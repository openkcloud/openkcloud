#!/usr/bin/env bash
# ============================================================
# run.sh: <nvidia-node> NVIDIA 잔재 패키지 셋 제거 실측 하네스
# 상세: 실행 중 드라이버(580.126.09)와 무관한 세 패키지를 안전 게이트·시뮬레이션 검증 뒤
#       제거하고, 모듈·헬스·allocatable 이 그대로인지 폴링으로 확인한다(PHASE 목록은 case
#       문 참고).
# 용법: [DRY_RUN=1] [PHASE=] [RUN_ID=] [POSTCHECK_TIMEOUT=] run.sh
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
export LC_ALL=C LANG=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$#" -ne 0 ]; then
  echo "인자를 받지 않는다 — PHASE/DRY_RUN/RUN_ID/POSTCHECK_TIMEOUT 환경변수로 지정" >&2
  exit 1
fi

PHASE="${PHASE:-all}"

# 안전 관련 상수 — 정확히 이 세 이름만 제거한다. env 로 덮지 않는다:
# 이 목록 자체가 게이트의 기준이라 실행 시점에 바꿀 수 있으면 게이트의 의미가 없다.
PACKAGES=(nvidia-firmware-580-580.178.04 nvidia-firmware-595-595.91.07 nvidia-kernel-source-580)
# 실행 중 버전과 짝이 맞는 패키지 — 시뮬레이션 결과에 이 중 하나라도 섞이면 중단(2차 방어).
RUNNING_SET=(libnvidia-cfg1-580 libnvidia-compute-580 nvidia-firmware-580-580.126.09 nvidia-kernel-common-580)

NODE="${NODE:-k8s-worker1}"
RESOURCE="nvidia.com/gpu"
DUS_NAME="${NODE}-nvidia"
EXPECTED_VERSION="${EXPECTED_VERSION:-580.126.09}"
DRY_RUN="${DRY_RUN:-0}"
POSTCHECK_TIMEOUT="${POSTCHECK_TIMEOUT:-300}"
RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"

log() { printf '[%s] %s\n' "$(date -u +%T)" "$*" >&2; }

# verdict <run-dir> — 그 실행 디렉터리의 기계 파일만 읽어 판정 한 줄을 낸다.
# 클러스터·노드에 접속하지 않는다(device-reset/run.sh 의 PHASE=verdict 와 같은 원칙).
# 파일이 없으면 missing 으로 낸다 — 지어내지 않는다. nvidia_smi 는 ok|fail 두 값만
# 쓰는 닫힌 표현이라 증거 없음도 fail 로 낸다(ok 를 증명 못하면 ok 가 아니다).
verdict() {
  local dir="$1" removed=0 p running nvidia_smi smi_count gpu dus
  if [ -f "$dir/post-dpkg.txt" ]; then
    for p in "${PACKAGES[@]}"; do
      grep -qE "^ii[[:space:]]+${p}[[:space:]:]" "$dir/post-dpkg.txt" || removed=$((removed + 1))
    done
  else
    removed="missing"
  fi
  if [ -f "$dir/final-module-version.txt" ]; then
    running="$(cat "$dir/final-module-version.txt")"
  else
    running="missing"
  fi
  if [ -f "$dir/final-nvidia-smi.txt" ]; then
    smi_count="$(grep -c '^GPU' "$dir/final-nvidia-smi.txt" || true)"
    if [ "$smi_count" = "2" ]; then nvidia_smi="ok"; else nvidia_smi="fail"; fi
  else
    nvidia_smi="fail"
  fi
  if [ -f "$dir/final-allocatable.txt" ]; then gpu="$(cat "$dir/final-allocatable.txt")"; else gpu="missing"; fi
  if [ -f "$dir/final-dus.txt" ]; then dus="$(cat "$dir/final-dus.txt")"; else dus="missing"; fi
  printf 'RESIDUE_REMOVED: removed=%s running=%s nvidia_smi=%s gpu=%s dus=%s\n' \
    "$removed" "$running" "$nvidia_smi" "$gpu" "$dus"
}

if [ "$PHASE" = "verdict" ]; then
  verdict "${RUN_DIR:?RUN_DIR 필요(runs/<RUN_ID>-nvidia 디렉터리)}"
  exit 0
fi

OUT_DIR="${SCRIPT_DIR}/runs/${RUN_ID}-nvidia"
mkdir -p "$OUT_DIR"
EVIDENCE="${OUT_DIR}/evidence.txt"

# evid <제목> — stdin 을 evidence.txt 에 구분선과 함께 append, 화면(stderr)에도 보여준다.
evid() {
  { printf '\n=== %s (%s UTC) ===\n' "$1" "$(date -u +%FT%TZ)"; cat; } | tee -a "$EVIDENCE" >&2
}

# DID_CORDON — 이 실행이 quiesce() 로 cordon 했는지. cleanup 이 이 실행이 만든 것만
# 되돌리게 한다(device-reset/run.sh 와 같은 원칙). 기본 흐름(all)은 quiesce 를 부르지
# 않으므로 정상적으로는 0 인 채로 끝난다 — 패키지 제거는 장치 상태를 바꾸지 않는다.
DID_CORDON=0

cleanup() {
  local rc="$1"
  [ "$rc" = "0" ] && return 0
  set +e
  log "cleanup: 비정상 종료(rc=$rc) — 이 실행이 cordon 했으면 되돌린다"
  if [ "$DID_CORDON" = "1" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN(cleanup): kubectl uncordon $NODE" >&2
    else
      kubectl uncordon "$NODE" >&2
      log "cleanup: uncordon $NODE 완료"
    fi
  fi
}
trap 'cleanup $?' EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# 노드 SSH 계정. 소스에 박지 않는다 — 공개 저장소에 내부 계정이 실리면 안 된다.
SSH_USER="${SSH_USER:-ubuntu}"
NODE_IP="$(kubectl get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
if [ -z "$NODE_IP" ]; then
  echo "노드 ${NODE} 의 InternalIP 를 찾지 못했다" >&2
  exit 1
fi

# remote <원격 명령> — ssh 로 실행하고 stdout+stderr 를 합쳐 돌려준다.
remote() {
  ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_USER}@${NODE_IP}" "$1" 2>&1
}

# pod_count_using_resource — 이 노드에서 nvidia.com/gpu 를 requests 나 limits 에 담은
# 파드 수(uid 중복 제거 — 다중 컨테이너가 같은 자원을 각각 요청해도 파드 하나로 센다).
pod_count_using_resource() {
  kubectl get pods -A --field-selector "spec.nodeName=${NODE}" -o json | \
    jq --arg r "$RESOURCE" '[.items[] | select(.spec.containers[]? |
      ((.resources.requests // {}) | has($r)) or ((.resources.limits // {}) | has($r)))] | unique_by(.metadata.uid) | length'
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

ndr_driver_loaded_all() {
  kubectl get nodedevicereport "$NODE" -o json 2>/dev/null | \
    jq -r '[.status.devices[]? | select(.vendor=="nvidia")] |
      if length==0 then "no-entries" elif all(.driverLoaded==true) then "true" else "false" end'
}
health_state() {
  kubectl get acceleratorhealth -A -o json 2>/dev/null | \
    jq -r --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)][0].status.state // "미수집"'
}
dus_state() {
  kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.state}' 2>/dev/null || echo "미수집"
}
allocatable_gpu() {
  kubectl get node "$NODE" -o json 2>/dev/null | jq -r --arg r "$RESOURCE" '.status.allocatable[$r] // "미수집"'
}
# device_plugin_ready — 보조 증거용(5분 게이트 여섯 조건에는 없다) — 판정에는 쓰지
# 않고 evidence 에만 남긴다.
device_plugin_ready() {
  local ready total
  ready="$(kubectl get pod -n kube-system -l app.kubernetes.io/name=nvidia-device-plugin-flat \
    --field-selector "spec.nodeName=${NODE}" -o json 2>/dev/null | \
    jq '[.items[] | select(.status.phase=="Running") | select([.status.containerStatuses[]?.ready] | all)] | length')"
  total="$(kubectl get pod -n kube-system -l app.kubernetes.io/name=nvidia-device-plugin-flat \
    --field-selector "spec.nodeName=${NODE}" -o json 2>/dev/null | jq '.items | length')"
  if [ "${total:-0}" -eq 0 ] || [ "$ready" != "$total" ]; then echo "no(${ready:-0}/${total:-0})"; else echo "yes"; fi
}

precheck() {
  safety_gate
  local dpkg_out modver smi dkms_out ndr health dus alloc dp
  dpkg_out="$(remote "dpkg -l | grep -E 'nvidia-firmware|nvidia-kernel-source|nvidia-kernel-common|nvidia-driver|libnvidia'")"
  printf '%s\n' "$dpkg_out" | evid "PRE dpkg -l(nvidia 관련)"
  printf '%s\n' "$dpkg_out" > "${OUT_DIR}/pre-dpkg.txt"

  modver="$(remote 'cat /sys/module/nvidia/version' | tr -d '[:space:]')"
  printf '%s\n' "$modver" | evid "PRE 로드된 모듈 버전"
  printf '%s\n' "$modver" > "${OUT_DIR}/pre-module-version.txt"

  smi="$(remote 'sudo /usr/bin/nvidia-smi -L')"
  printf '%s\n' "$smi" | evid "PRE nvidia-smi -L"
  printf '%s\n' "$smi" > "${OUT_DIR}/pre-nvidia-smi.txt"

  dkms_out="$(remote 'dkms status')"
  printf '%s\n' "$dkms_out" | evid "PRE dkms status(nvidia DKMS 등록 없음을 기대)"

  ndr="$(kubectl get nodedevicereport "$NODE" -o json 2>/dev/null | jq -c '[.status.devices[]? | select(.vendor=="nvidia")]')"
  health="$(health_state)"; dus="$(dus_state)"; alloc="$(allocatable_gpu)"; dp="$(device_plugin_ready)"
  printf 'NDR(nvidia)=%s\nhealth=%s dus=%s allocatable=%s device-plugin=%s\n' "$ndr" "$health" "$dus" "$alloc" "$dp" | \
    evid "PRE kubectl 상태(NDR·health·DUS·allocatable·device-plugin)"
  printf '%s\n' "$dus" > "${OUT_DIR}/pre-dus.txt"
  printf '%s\n' "$alloc" > "${OUT_DIR}/pre-allocatable.txt"
  log "precheck 완료: 모듈=${modver} dus=${dus} allocatable=${alloc}"
}

simulate() {
  local out names count r
  out="$(remote "sudo apt-get -s remove ${PACKAGES[*]}")"
  printf '%s\n' "$out" | evid "SIMULATE(apt-get -s remove)"
  printf '%s\n' "$out" > "${OUT_DIR}/simulate-output.txt"

  names="$(printf '%s\n' "$out" | sed -n 's/^Remv \([^ ]*\).*/\1/p')"
  count="$(printf '%s\n' "$names" | grep -c . || true)"
  log "시뮬레이션 게이트: Remv 줄 수=${count}"
  if [ "$count" -ne 3 ]; then
    printf 'Remv 줄이 3개가 아니라 %s개 — 중단\n%s\n' "$count" "$names" | evid "SIMULATE GATE 실패"
    return 1
  fi

  local want_sorted got_sorted
  want_sorted="$(printf '%s\n' "${PACKAGES[@]}" | sort)"
  got_sorted="$(printf '%s\n' "$names" | sort)"
  if [ "$got_sorted" != "$want_sorted" ]; then
    printf '제거 대상 이름이 예상과 다름 — 중단\n기대:\n%s\n실제:\n%s\n' "$want_sorted" "$got_sorted" | evid "SIMULATE GATE 실패"
    return 1
  fi

  for r in "${RUNNING_SET[@]}"; do
    if printf '%s\n' "$names" | grep -qxF "$r"; then
      printf '실행 중 버전 세트의 패키지(%s)가 제거 목록에 있음 — 중단\n' "$r" | evid "SIMULATE GATE 실패"
      return 1
    fi
  done

  printf '게이트 통과: 정확히 3개, 실행 중 버전 세트와 겹치지 않음 — %s\n' "$(printf '%s' "$got_sorted" | tr '\n' ' ')" | \
    evid "SIMULATE GATE 통과"
}

do_remove() {
  safety_gate
  local cmd="sudo apt-get remove -y ${PACKAGES[*]}"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s "%s"\n' "$SSH_USER" "$NODE_IP" "$cmd" | evid "REMOVE COMMAND(DRY_RUN)"
    return 0
  fi
  local out ssh_rc rc
  if out="$(remote "$cmd; echo __REMOVE_RC=\$?")"; then ssh_rc=0; else ssh_rc=$?; fi
  rc="$(printf '%s\n' "$out" | sed -n 's/^__REMOVE_RC=\([0-9][0-9]*\)$/\1/p' | tail -1)"
  out="$(printf '%s\n' "$out" | sed '/^__REMOVE_RC=[0-9][0-9]*$/d')"
  if [ -z "$rc" ]; then
    printf '$ %s\n%s\nssh 종료(exit=%s), 원격 명령의 실제 종료 코드는 받지 못함\n' "$cmd" "$out" "$ssh_rc" | evid "REMOVE COMMAND"
    echo "$ssh_rc" > "${OUT_DIR}/remove-ssh-lost.txt"
    return 1
  fi
  printf '$ %s\n%s\nexit=%s\n' "$cmd" "$out" "$rc" | evid "REMOVE COMMAND"
  echo "$rc" > "${OUT_DIR}/remove-exit.txt"
  if [ "$rc" != "0" ]; then
    echo "제거 명령이 0 이 아닌 코드로 끝났다: $rc" >&2
    return 1
  fi
}

postcheck() {
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: postcheck 생략(제거를 안 했으므로 비교할 것이 없다)" | evid "POSTCHECK(DRY_RUN)"
    return 0
  fi
  local dpkg_out
  dpkg_out="$(remote "dpkg -l | grep -E 'nvidia-firmware|nvidia-kernel-source|nvidia-kernel-common|nvidia-driver|libnvidia'")"
  printf '%s\n' "$dpkg_out" | evid "POST dpkg -l(nvidia 관련)"
  printf '%s\n' "$dpkg_out" > "${OUT_DIR}/post-dpkg.txt"
  log "postcheck 완료 — PRE/POST 비교는 evidence.txt 참고"
}

# wait_postchecks — 최대 POSTCHECK_TIMEOUT초(기본 300, 5분 상한) 10초 간격
# 폴링. 여섯 조건(모듈 버전·nvidia-smi 장치 수·NDR driverLoaded·health·DUS·allocatable)이
# 전부 맞을 때까지 기다리고, 통과 여부와 무관하게 마지막 관측값을 final-*.txt 에 남긴다
# — verdict 가 그 파일만 읽는다. 실패해도 재부팅·재설치를 스스로 하지 않는다.
wait_postchecks() {
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_postchecks 생략(제거를 안 했으므로 기다릴 것이 없다)" | evid "WAIT-POSTCHECKS(DRY_RUN)"
    return 0
  fi
  local elapsed=0 interval=10 modver="" smi_out="" smi_count=0 ndr="" health="" dus="" alloc="" ok=0
  while [ "$elapsed" -lt "$POSTCHECK_TIMEOUT" ]; do
    modver="$(remote 'cat /sys/module/nvidia/version' 2>/dev/null | tr -d '[:space:]' || echo "")"
    smi_out="$(remote 'sudo /usr/bin/nvidia-smi -L' 2>/dev/null || echo "")"
    smi_count="$(printf '%s\n' "$smi_out" | grep -c '^GPU' || true)"
    ndr="$(ndr_driver_loaded_all)"
    health="$(health_state)"
    dus="$(dus_state)"
    alloc="$(allocatable_gpu)"
    log "사후 게이트 폴링(${elapsed}s): module=${modver} smi=${smi_count}개 ndr=${ndr} health=${health} dus=${dus} allocatable=${alloc}"
    if [ "$modver" = "$EXPECTED_VERSION" ] && [ "$smi_count" = "2" ] && [ "$ndr" = "true" ] && \
       [ "$health" = "Healthy" ] && [ "$dus" = "Idle" ] && [ "$alloc" = "2" ]; then
      ok=1
      break
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  printf '%s\n' "$modver" > "${OUT_DIR}/final-module-version.txt"
  printf '%s\n' "$smi_out" > "${OUT_DIR}/final-nvidia-smi.txt"
  printf '%s\n' "$alloc" > "${OUT_DIR}/final-allocatable.txt"
  printf '%s\n' "$dus" > "${OUT_DIR}/final-dus.txt"
  if [ "$ok" -eq 1 ]; then
    printf '사후 게이트 통과(%s초): module=%s smi=%s개 ndr=%s health=%s dus=%s allocatable=%s\n' \
      "$elapsed" "$modver" "$smi_count" "$ndr" "$health" "$dus" "$alloc" | evid "WAIT-POSTCHECKS 통과"
    return 0
  fi
  printf '%s초 안에 통과 못함(마지막 관측): module=%s smi=%s개 ndr=%s health=%s dus=%s allocatable=%s\n' \
    "$POSTCHECK_TIMEOUT" "$modver" "$smi_count" "$ndr" "$health" "$dus" "$alloc" | evid "WAIT-POSTCHECKS 실패"
  return 1
}

# quiesce/unquiesce — 선택 조치. cordon 만 하고 파드는 지우지 않는다 — 패키지
# 제거가 device-plugin 의 NVML 핸들과 무관하기 때문이다. all 에는 없다.
quiesce() {
  safety_gate
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl cordon %s\n' "$NODE" | evid "QUIESCE(DRY_RUN)"
    DID_CORDON=1
    return 0
  fi
  kubectl cordon "$NODE" >&2
  DID_CORDON=1
  printf 'cordon %s\n' "$NODE" | evid "QUIESCE"
}
unquiesce() {
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl uncordon %s\n' "$NODE" | evid "UNQUIESCE(DRY_RUN)"
    return 0
  fi
  kubectl uncordon "$NODE" >&2
  printf 'uncordon %s\n' "$NODE" | evid "UNQUIESCE"
}

main() {
  safety_gate
  precheck
  simulate
  do_remove
  postcheck
  wait_postchecks
  log "요약: evidence=${EVIDENCE}"
}

case "$PHASE" in
  safety-gate) safety_gate ;;
  precheck) precheck ;;
  simulate) simulate ;;
  remove) do_remove ;;
  postcheck) postcheck ;;
  wait-postchecks) wait_postchecks ;;
  quiesce) quiesce ;;
  unquiesce) unquiesce ;;
  all) main ;;
  *) echo "알 수 없는 PHASE: $PHASE" >&2; exit 1 ;;
esac
