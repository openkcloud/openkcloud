#!/usr/bin/env bash
# ============================================================
# run.sh: 라이브 재확인 — 사전 컴파일 이미지로 <nvidia-node> 빈 노드 전이
# 상세: 새 이미지(-v3)를 DIP kernelImages 에 등록하고 패키지 제거+재부팅으로 노드를 비운
#       뒤 operator 자기복구 설치 Job 을 관찰해 madison 미조회·dpkg 설치·모듈 복귀를
#       확인한다(PHASE 목록은 case 문 참고, rollback-dip 은 실패 판단 시에만 따로 부른다).
# 용법: [DRY_RUN=1] [PHASE=] [RUN_ID=] [KERNEL=] [DRIVER_VERSION=] [NEW_VARIANT=] run.sh
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
export LC_ALL=C LANG=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [ "$#" -ne 0 ]; then
  echo "인자를 받지 않는다 — PHASE/DRY_RUN/RUN_ID/KERNEL/... 환경변수로 지정" >&2
  exit 1
fi

PHASE="${PHASE:-all}"

# 안전 관련 상수 — 이 하네스가 다루는 노드·정책·자원은 고정이다.
NODE="k8s-worker1"
RESOURCE="nvidia.com/gpu"
DUS_NAME="${NODE}-nvidia"
DIP_NAME="nvidia-gpu-ds"
DRIVER_MAJOR="580"

KERNEL="${KERNEL:-5.15.0-171-generic}"
DRIVER_VERSION="${DRIVER_VERSION:-580.126.09}"
NEW_VARIANT="${NEW_VARIANT:-v3}"
REGISTRY_REPO="${REGISTRY_REPO:?REGISTRY_REPO 을 지정하십시오}"
NEW_TAG="${IMAGE_TAG:-${REGISTRY_REPO}:${DRIVER_VERSION}-k${KERNEL}-${NEW_VARIANT}}"

# PACKAGES — 9개, 순서 고정. entrypoint.sh 의 nvidia_prebuilt_cleanup_cmd 와 정확히 이
# 순서다: 모듈이 nvidia-kernel-common 을, 그것이 nvidia-firmware 를 의존하므로 모듈이 앞,
# firmware 가 맨 뒤. env 로 덮지 않는다 — 목록 자체가 이 절차의 근거다.
PACKAGES=(
  "linux-modules-nvidia-${DRIVER_MAJOR}-${KERNEL}"
  "linux-objects-nvidia-${DRIVER_MAJOR}-${KERNEL}"
  "linux-signatures-nvidia-${KERNEL}"
  "nvidia-utils-${DRIVER_MAJOR}"
  "nvidia-compute-utils-${DRIVER_MAJOR}"
  "libnvidia-cfg1-${DRIVER_MAJOR}"
  "libnvidia-compute-${DRIVER_MAJOR}"
  "nvidia-kernel-common-${DRIVER_MAJOR}"
  "nvidia-firmware-${DRIVER_MAJOR}-${DRIVER_VERSION}"
)

DRY_RUN="${DRY_RUN:-0}"
REBOOT_TIMEOUT="${REBOOT_TIMEOUT:-540}"
OBSERVE_TIMEOUT="${OBSERVE_TIMEOUT:-540}"
RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"

log() { printf '[%s] %s\n' "$(date -u +%T)" "$*" >&2; }

# verdict <run-dir> — 그 실행 디렉터리의 기계 파일만 읽어 판정 한 줄을
# 낸다. 클러스터·노드에 접속하지 않는다(형제 하네스들과 같은 원칙). 파일이 없으면
# missing 으로 낸다 — 지어내지 않는다.
verdict() {
  local dir="$1" image madison apt_install dpkg_i module gpu dus
  image="$(cat "$dir/image-tag.txt" 2>/dev/null || echo "missing")"
  if [ -f "$dir/install-job.log" ]; then
    if madison="$(grep -c 'apt-cache madison' "$dir/install-job.log" 2>/dev/null)"; then :; else madison=0; fi
    if apt_install="$(grep -cE 'apt-get install|apt install' "$dir/install-job.log" 2>/dev/null)"; then :; else apt_install=0; fi
    # entrypoint 는 dpkg -i 커맨드 문자열 자체를 로그로 echo 하지 않는다
    # (install_prebuilt_onto_host 가 dpkg -i 를 직접 실행할 뿐이다) — dpkg 가 각 .deb
    # 파일을 처리할 때 찍는 "Preparing to unpack .../<pkg>_<ver>_<arch>.deb ..." 줄
    # 수를 실제 .deb 설치 건수의 증거로 쓴다. "Unpacking "
    # 줄도 같은 개수를 내지만 그쪽은 패키지명만 찍혀 .deb 파일 자체를 가리키지 않는다.
    if dpkg_i="$(grep -cE 'Preparing to unpack .*\.deb' "$dir/install-job.log" 2>/dev/null)"; then :; else dpkg_i=0; fi
  else
    madison="missing"; apt_install="missing"; dpkg_i="missing"
  fi
  module="$(cat "$dir/final-module-version.txt" 2>/dev/null || echo "missing")"
  gpu="$(cat "$dir/final-allocatable.txt" 2>/dev/null || echo "missing")"
  dus="$(cat "$dir/final-dus.txt" 2>/dev/null || echo "missing")"
  printf 'RELIVE: image=%s madison=%s apt_install=%s dpkg_i=%s module=%s gpu=%s dus=%s\n' \
    "$image" "$madison" "$apt_install" "$dpkg_i" "$module" "$gpu" "$dus"
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
# 되돌리게 한다(형제 하네스들과 같은 원칙).
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
  log "cleanup: DIP kernelImages 는 되돌리지 않는다 — 실패 시 PHASE=rollback-dip 을 컨트롤러 판단 하에 따로 부른다"
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

dus_state() {
  kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.state}' 2>/dev/null || echo "미수집"
}
dus_current_version() {
  kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.currentVersion}' 2>/dev/null || echo "미수집"
}
allocatable_gpu() {
  kubectl get node "$NODE" -o json 2>/dev/null | jq -r --arg r "$RESOURCE" '.status.allocatable[$r] // "미수집"'
}
health_state() {
  kubectl get acceleratorhealth -A -o json 2>/dev/null | \
    jq -r --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)][0].status.state // "미수집"'
}
node_ready() {
  kubectl get node "$NODE" -o json 2>/dev/null | \
    jq -r '[.status.conditions[] | select(.type=="Ready")][0].status // "미수집"'
}

precheck() {
  safety_gate
  local dpkg_out modver grub dip_before dus dus_ver gpu health ready

  dpkg_out="$(remote "dpkg -l | grep -E 'linux-image-5.15.0'")"
  printf '%s\n' "$dpkg_out" | evid "PRE 설치된 5.15.0 계열 커널 목록"

  modver="$(remote 'cat /sys/module/nvidia/version' | tr -d '[:space:]')"
  printf '%s\n' "$modver" | evid "PRE 로드된 모듈 버전"
  printf '%s\n' "$modver" > "${OUT_DIR}/pre-module-version.txt"

  grub="$(remote "grep -E '^GRUB_DEFAULT' /etc/default/grub; sudo grub-editenv /boot/grub/grubenv list 2>/dev/null | grep saved_entry")"
  printf '%s\n' "$grub" | evid "PRE GRUB 기본값·saved_entry(재부팅 뒤 어느 커널로 돌아오는지)"
  case "$grub" in
    *"$KERNEL"*) : ;;
    *) echo "경고: GRUB 기본값/saved_entry 에 ${KERNEL} 이 안 보인다 — 재부팅 뒤 다른 커널로 뜰 수 있다" | evid "PRE GRUB 경고" ;;
  esac

  dip_before="$(kubectl get dip "$DIP_NAME" -o json | jq -r --arg k "$KERNEL" '.spec.driver.kernelImages[$k] // "MISSING"')"
  printf 'kernelImages[%s]=%s\n' "$KERNEL" "$dip_before" | evid "PRE DIP kernelImages"

  dus="$(dus_state)"; dus_ver="$(dus_current_version)"; gpu="$(allocatable_gpu)"; health="$(health_state)"; ready="$(node_ready)"
  printf 'dus=%s/%s allocatable=%s health=%s nodeReady=%s\n' "$dus" "$dus_ver" "$gpu" "$health" "$ready" | \
    evid "PRE kubectl 상태(DUS·allocatable·health·nodeReady)"
  printf '%s\n' "$gpu" > "${OUT_DIR}/pre-allocatable.txt"
  log "precheck 완료: 모듈=${modver} dus=${dus}/${dus_ver} allocatable=${gpu} health=${health}"
}

dip_patch() {
  local before after
  before="$(kubectl get dip "$DIP_NAME" -o json | jq -r --arg k "$KERNEL" '.spec.driver.kernelImages[$k] // "MISSING"')"
  printf '%s\n' "$before" > "${OUT_DIR}/dip-before.txt"
  printf 'kernelImages[%s] 현재값=%s\n' "$KERNEL" "$before" | evid "DIP-PATCH 이전"
  printf '%s\n' "$NEW_TAG" > "${OUT_DIR}/image-tag.txt"

  local patch; patch="$(jq -n --arg k "$KERNEL" --arg v "$NEW_TAG" '{spec:{driver:{kernelImages:{($k):$v}}}}')"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl patch dip %s --type=merge -p %s\n' "$DIP_NAME" "$patch" | evid "DIP-PATCH (DRY_RUN)"
    return 0
  fi
  kubectl patch dip "$DIP_NAME" --type=merge -p "$patch" >&2
  after="$(kubectl get dip "$DIP_NAME" -o json | jq -r --arg k "$KERNEL" '.spec.driver.kernelImages[$k] // "MISSING"')"
  printf '%s\n' "$after" > "${OUT_DIR}/dip-after.txt"
  printf 'kernelImages[%s] 새값=%s\n' "$KERNEL" "$after" | evid "DIP-PATCH 이후"
  if [ "$after" != "$NEW_TAG" ]; then
    echo "patch 뒤 값이 기대와 다르다: 기대=${NEW_TAG} 실제=${after}" >&2
    return 1
  fi
}

# rollback_dip — 실패 시에만 쓰는 별도 조치(all 에 없음). dip-before.txt(같은 RUN_DIR 의
# dip_patch 가 남긴 이전 값)로 되돌리고, 자기복구가 다시 돌 때까지는 기다리지 않는다
# (그건 PHASE=observe 를 다시 부르는 것으로 확인한다).
rollback_dip() {
  local before; before="$(cat "${OUT_DIR}/dip-before.txt" 2>/dev/null || echo "")"
  if [ -z "$before" ] || [ "$before" = "MISSING" ]; then
    echo "dip-before.txt 가 없거나 MISSING이다 — 같은 RUN_DIR 로 dip-patch 를 먼저 돌렸어야 한다" >&2
    return 1
  fi
  local patch; patch="$(jq -n --arg k "$KERNEL" --arg v "$before" '{spec:{driver:{kernelImages:{($k):$v}}}}')"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl patch dip %s --type=merge -p %s (되돌리기 → %s)\n' "$DIP_NAME" "$patch" "$before" | evid "ROLLBACK-DIP (DRY_RUN)"
    return 0
  fi
  kubectl patch dip "$DIP_NAME" --type=merge -p "$patch" >&2
  printf 'kernelImages[%s] 를 %s 로 되돌림\n' "$KERNEL" "$before" | evid "ROLLBACK-DIP"
}

quiesce() {
  safety_gate
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl cordon %s\n' "$NODE" | evid "QUIESCE (DRY_RUN)"
    DID_CORDON=1
    return 0
  fi
  kubectl cordon "$NODE" >&2
  DID_CORDON=1
  printf 'cordon %s\n' "$NODE" | evid "QUIESCE"
}
unquiesce() {
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl uncordon %s\n' "$NODE" | evid "UNQUIESCE (DRY_RUN)"
    return 0
  fi
  kubectl uncordon "$NODE" >&2
  printf 'uncordon %s\n' "$NODE" | evid "UNQUIESCE"
}

# empty_node — PACKAGES 배열에 고정된 9개 패키지(순서·이름 고정)를 apt-get remove 로
# 지운다. dcgm-exporter·device-plugin DaemonSet 파드는
# 변경하지 않는다 — 드라이버가 사라지면 CrashLoop 이 정상이고 설치 뒤 스스로 복귀한다.
empty_node() {
  safety_gate
  local cmd="sudo apt-get remove -y ${PACKAGES[*]}"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s "%s"\n' "$SSH_USER" "$NODE_IP" "$cmd" | evid "EMPTY-NODE REMOVE (DRY_RUN)"
    return 0
  fi
  local out ssh_rc rc
  if out="$(remote "$cmd; echo __REMOVE_RC=\$?")"; then ssh_rc=0; else ssh_rc=$?; fi
  rc="$(printf '%s\n' "$out" | sed -n 's/^__REMOVE_RC=\([0-9][0-9]*\)$/\1/p' | tail -1)"
  out="$(printf '%s\n' "$out" | sed '/^__REMOVE_RC=[0-9][0-9]*$/d')"
  if [ -z "$rc" ]; then
    printf '$ %s\n%s\nssh 종료(exit=%s), 원격 명령의 실제 종료 코드는 받지 못함\n' "$cmd" "$out" "$ssh_rc" | evid "EMPTY-NODE REMOVE"
    echo "$ssh_rc" > "${OUT_DIR}/remove-ssh-lost.txt"
    return 1
  fi
  printf '$ %s\n%s\nexit=%s\n' "$cmd" "$out" "$rc" | evid "EMPTY-NODE REMOVE"
  echo "$rc" > "${OUT_DIR}/remove-exit.txt"
  if [ "$rc" != "0" ]; then
    echo "제거 명령이 0 이 아닌 코드로 끝났다: $rc" >&2
    return 1
  fi
  local remaining pat=""
  local p; for p in "${PACKAGES[@]}"; do pat="${pat}${pat:+|}${p}"; done
  remaining="$(remote "dpkg -l | grep -E '^ii' | grep -cE \"${pat}\" || true")"
  printf '제거 확인: dpkg -l 에 남은 대상 패키지 수=%s(0 기대)\n' "$remaining" | evid "EMPTY-NODE 제거 확인"
  if [ "$remaining" != "0" ]; then
    echo "제거 대상 9개 중 ${remaining}개가 여전히 ii 로 남아 있다" >&2
    return 1
  fi
}

# reboot_recover — 재부팅을 보내고, SSH 로 확인한 uptime 이 600초 미만(=이번 재부팅의
# 새 boot)인 시점과 kubectl 이 보는 Ready=True 가 "같은 폴링 순간"에 동시에 성립할 때만
# 성공으로 본다. "재부팅 전 오래 떠 있던 Ready" 를 새 Ready 로 착각하는 함정이 있다
# (uptime 87897s 를 uptime<600 가드로 걸러낸다) — 이 동시 조건이 그 가드와 같은 효과를
# 낸다: uptime<600 이 참인 순간의 Ready 는 정의상 이번 새 부팅 이후의 관측일 수밖에 없다.
reboot_recover() {
  safety_gate
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: ssh %s@%s sudo reboot\n' "$SSH_USER" "$NODE_IP" | evid "REBOOT-RECOVER (DRY_RUN)"
    return 0
  fi
  date -u +%FT%TZ > "${OUT_DIR}/reboot-start-utc.txt"
  ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_USER}@${NODE_IP}" 'sudo reboot' >&2 || true
  sleep 20
  local elapsed=0 interval=10 uptime_s="" ready=""
  while [ "$elapsed" -lt "$REBOOT_TIMEOUT" ]; do
    uptime_s="$(remote "cut -d. -f1 /proc/uptime" 2>/dev/null | tr -d '[:space:]' || echo "")"
    # SSH 가 아직 안 올라왔으면 remote() 가 "ssh: connect to host ... refused" 같은
    # 오류 문자열을 그대로 돌려준다 — tr 로 공백만 지워도 숫자가 아니므로, 순수 숫자가
    # 아니면 빈 값으로 취급해 다음 [ -lt ] 비교가 "정수가 아니다" 오류를 내지 않게 한다.
    case "$uptime_s" in ''|*[!0-9]*) uptime_s="" ;; esac
    ready="$(node_ready)"
    log "재부팅 대기(${elapsed}s): uptime=${uptime_s:-응답없음} nodeReady=${ready}"
    if [ -n "$uptime_s" ] && [ "$uptime_s" -lt 600 ] && [ "$ready" = "True" ]; then
      printf '재부팅 완료: uptime=%s초(새 boot) nodeReady=True, 경과 %s초\n' "$uptime_s" "$elapsed" | evid "REBOOT-RECOVER"
      echo "$elapsed" > "${OUT_DIR}/reboot-elapsed.txt"
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  printf '재부팅 %s초 내 (uptime<600 및 nodeReady=True) 동시 충족 실패: 마지막 uptime=%s nodeReady=%s\n' \
    "$REBOOT_TIMEOUT" "${uptime_s:-없음}" "$ready" | evid "REBOOT-RECOVER 실패"
  return 1
}

# wait_empty — 재부팅 직후 모듈·사용자 공간이 실제로 비었는지 확인한다. 여기서 모듈이
# 이미 로드돼 있으면 이후 관찰이 성립하지 않으므로 실패로 멈춘다.
wait_empty() {
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_empty 생략(재부팅을 안 했다)" | evid "WAIT-EMPTY (DRY_RUN)"
    return 0
  fi
  local modver smi
  modver="$(remote 'cat /sys/module/nvidia/version 2>/dev/null || echo ABSENT' | tr -d '[:space:]')"
  smi="$(remote 'command -v nvidia-smi >/dev/null 2>&1 && echo present || echo absent' | tr -d '[:space:]')"
  printf '모듈 버전=%s nvidia-smi=%s\n' "$modver" "$smi" | evid "WAIT-EMPTY"
  if [ "$modver" != "ABSENT" ] || [ "$smi" != "absent" ]; then
    echo "재부팅 뒤에도 모듈/명령이 남아 있다 — 빈 노드 전제가 성립하지 않는다(module=${modver} smi=${smi})" >&2
    return 1
  fi
}

# observe — DUS 가 Idle 을 벗어나는지, kcloud 네임스페이스에 설치 Job 이 생기는지,
# 그 Job 이 끝나는지, DUS 가 다시 Idle/DRIVER_VERSION 으로 돌아오는지 한 폴링 순회로
# 확인한다. 설치 파드 로그 전문을 install-job.log 로 저장 — PHASE=verdict 는 이 파일만
# 읽는다.
observe() {
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: observe 생략(재부팅을 안 했다)" | evid "OBSERVE (DRY_RUN)"
    return 0
  fi
  local elapsed=0 interval=10 dus="" job_pod="" job_phase=""
  while [ "$elapsed" -lt "$OBSERVE_TIMEOUT" ]; do
    dus="$(dus_state)"
    if [ -z "$job_pod" ]; then
      job_pod="$(kubectl get pods -n kcloud -o json 2>/dev/null | \
        jq -r '[.items[] | select(.metadata.name | startswith("kcloud-nvidia-install-"))][0].metadata.name // empty')"
    fi
    if [ -n "$job_pod" ]; then
      job_phase="$(kubectl get pod -n kcloud "$job_pod" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")"
    fi
    log "관찰(${elapsed}s): dus=${dus} job_pod=${job_pod:-없음} job_phase=${job_phase:-없음}"
    if [ -n "$job_pod" ] && { [ "$job_phase" = "Succeeded" ] || [ "$job_phase" = "Failed" ]; } && [ "$dus" = "Idle" ]; then
      break
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done

  if [ -z "$job_pod" ]; then
    echo "설치 Job 파드가 ${OBSERVE_TIMEOUT}초 안에 생기지 않았다 — 모듈 부재가 operator 자기복구를 촉발하지 않았다" >&2
    return 1
  fi

  local log_text
  log_text="$(kubectl logs -n kcloud "$job_pod" 2>&1)"
  printf '%s\n' "$log_text" > "${OUT_DIR}/install-job.log"
  printf '파드=%s phase=%s, 로그 %s줄(전문은 install-job.log)\n' "$job_pod" "$job_phase" "$(printf '%s\n' "$log_text" | wc -l)" | \
    evid "OBSERVE 설치 Job"

  local skip_count
  skip_count="$(printf '%s\n' "$log_text" | grep -ciE 'already installed and healthy|skipping' || true)"
  printf '건너뜀(already installed and healthy|skipping) 횟수=%s(0 기대 — 건너뛰면 이번 전이를 증명 못한다)\n' "$skip_count" | \
    evid "OBSERVE 건너뜀 아님 확인(보강 증거, 판정 기계 줄에는 없음)"

  if [ "$job_phase" != "Succeeded" ]; then
    echo "설치 Job 이 Succeeded 로 끝나지 않았다(phase=${job_phase}) — 로그는 install-job.log 참고" >&2
    return 1
  fi
  if [ "$dus" != "Idle" ]; then
    echo "Job 은 Succeeded 인데 DUS 가 ${OBSERVE_TIMEOUT}초 안에 Idle 로 돌아오지 않았다(dus=${dus})" >&2
    return 1
  fi

  local modver gpu health dusver
  modver="$(remote 'cat /sys/module/nvidia/version' 2>/dev/null | tr -d '[:space:]' || echo "missing")"
  gpu="$(allocatable_gpu)"
  health="$(health_state)"
  dusver="$(dus_current_version)"
  printf '%s\n' "$modver" > "${OUT_DIR}/final-module-version.txt"
  printf '%s\n' "$gpu" > "${OUT_DIR}/final-allocatable.txt"
  printf '%s\n' "$dus" > "${OUT_DIR}/final-dus.txt"
  printf 'module=%s(정책버전=%s) allocatable=%s health=%s dus=%s/%s\n' \
    "$modver" "$DRIVER_VERSION" "$gpu" "$health" "$dus" "$dusver" | evid "OBSERVE 마감 상태"

  if [ "$modver" != "$DRIVER_VERSION" ] || [ "$gpu" != "2" ] || [ "$health" != "Healthy" ]; then
    echo "마감 상태가 기대와 다르다: module=${modver}(기대 ${DRIVER_VERSION}) gpu=${gpu}(기대 2) health=${health}(기대 Healthy)" >&2
    return 1
  fi
}

main() {
  precheck
  dip_patch
  quiesce
  empty_node
  reboot_recover
  wait_empty
  observe
  unquiesce
  log "요약: evidence=${EVIDENCE}"
}

case "$PHASE" in
  precheck) precheck ;;
  dip-patch) dip_patch ;;
  rollback-dip) rollback_dip ;;
  quiesce) quiesce ;;
  unquiesce) unquiesce ;;
  empty-node) empty_node ;;
  reboot-recover) reboot_recover ;;
  wait-empty) wait_empty ;;
  observe) observe ;;
  all) main ;;
  *) echo "알 수 없는 PHASE: $PHASE" >&2; exit 1 ;;
esac
