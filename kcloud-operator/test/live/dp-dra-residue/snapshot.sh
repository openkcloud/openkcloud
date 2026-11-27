#!/usr/bin/env bash
# ============================================================
# snapshot.sh: 노드 하나의 device-plugin/DRA 관련 자원을 파일로 남기는 읽기 전용 도구
# 상세: kubectl get·ssh cat/ls 외의 명령을 쓰지 않는 읽기 전용 도구다. device-plugin↔DRA
#       전환 흔적(0 등록 잔재·옛 광고·고아 ResourceSlice/CDI)을 노드별 파일 12개로 남긴다.
#       "종류 자체가 없음"과 "있으나 0개"를 collected:false/true 로 구분한다(앞을 오류로
#       죽이면 지금 상태로 못 돈다). 벤더는 기록용일 뿐 조회를 좁히지 않는다(옛 이름 잔재 보존).
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
# 이 프로세스의 LC_ALL/LANG 을 SSH 가 원격으로 전달(SendEnv)하면서 원격 sshd 가 그 로케일을
# 못 만들면 "setlocale: LC_ALL: cannot change locale" 경고가 캡처 파일 첫 줄에 섞인다
# (reference_locale_commit_hook 과 같은 원인). 여기서 C 로 고정해 전달값 자체를 바꾼다.
export LC_ALL=C LANG=C

if [ "$#" -ne 3 ]; then
  echo "usage: snapshot.sh <output-dir> <node> <vendor>" >&2
  exit 1
fi

OUT="$1"
NODE="$2"
VENDOR="$3"
mkdir -p "$OUT"

# _kubectl_raw <kubectl get 인자...> — _RAW/_RC 에 결과를 담는다. if 조건 안에서 실행해
# kind 부재(비정상 종료)가 set -e 로 스크립트를 죽이지 않게 한다.
_RAW=""; _RC=0
_kubectl_raw() {
  if _RAW="$(kubectl get "$@" -o json 2>&1)"; then
    _RC=0
  else
    _RC=$?
  fi
}

# kget <출력파일> <kubectl get 인자...> — 목록 형태 자원을 남긴다.
kget() {
  local outfile="$1"; shift
  _kubectl_raw "$@"
  if [ "$_RC" -eq 0 ]; then
    jq '{collected:true, items:(.items // [.])}' <<<"$_RAW" > "$outfile"
  else
    jq -n --arg reason "$_RAW" '{collected:false, reason:$reason}' > "$outfile"
  fi
}

# kget_filtered <출력파일> <jq 필터> <kubectl get 인자...> — 단일 오브젝트를 필터로 좁혀 남긴다.
kget_filtered() {
  local outfile="$1" filter="$2"; shift 2
  _kubectl_raw "$@"
  if [ "$_RC" -eq 0 ]; then
    jq "$filter" <<<"$_RAW" > "$outfile"
  else
    jq -n --arg reason "$_RAW" '{collected:false, reason:$reason}' > "$outfile"
  fi
}

# manifest_status <jsonfile> — manifest 한 줄에 쓸 상태 문자열.
manifest_status() {
  local f="$1"
  if jq -e '.collected == false' "$f" >/dev/null 2>&1; then
    printf 'absent(%s)' "$(jq -r '.reason' "$f" | head -1 | cut -c1-60)"
  elif jq -e '.items' "$f" >/dev/null 2>&1; then
    printf 'ok(%d)' "$(jq '.items | length' "$f")"
  else
    echo ok
  fi
}

# ssh_capture <출력파일> <원격 명령> — SSH 로만 읽는다. 실패해도 그 사실을 파일에 남기고
# 계속한다(exit 하지 않는다). 원격 명령 뒤에 종료 코드 마커(echo)를 붙여 SSH 접속 자체의
# 성공 여부와 원격 명령 자체의 성공 여부를 분리해서 받는다 — 접속은 됐지만 원격 명령이
# "Permission denied" 등으로 실패하면 그 결과는 관측 불가(partial)이지 "관측 성공"(ok)이
# 아니다. 마커 줄은 파일에 남기기 전에 지운다.
SSH_STATUS=""
ssh_capture() {
  local outfile="$1" remote_cmd="$2"
  if [ -z "$NODE_IP" ]; then
    printf '노드 %s 의 InternalIP 를 못 찾아 SSH 를 생략한다\n' "$NODE" > "$outfile"
    SSH_STATUS="skipped(no-ip)"
    return 0
  fi
  local marker="__SNAPSHOT_RC" raw ssh_rc remote_rc
  if raw="$(ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_USER}@${NODE_IP}" \
      "${remote_cmd}; echo \"${marker}=\$?\"" 2>&1)"; then
    ssh_rc=0
  else
    ssh_rc=$?
  fi
  if [ "$ssh_rc" -ne 0 ]; then
    printf 'ssh 실패(rc=%d): %s\n' "$ssh_rc" "$raw" > "$outfile"
    SSH_STATUS="ssh-failed"
    return 0
  fi
  remote_rc="$(printf '%s\n' "$raw" | sed -n "s/^${marker}=\([0-9][0-9]*\)\$/\1/p" | tail -1)"
  raw="$(printf '%s\n' "$raw" | sed "/^${marker}=[0-9][0-9]*\$/d")"
  printf '%s\n' "$raw" > "$outfile"
  if [ -z "$remote_rc" ]; then
    SSH_STATUS="partial(no-exit-marker)"
  elif printf '%s' "$raw" | grep -qi 'Permission denied'; then
    SSH_STATUS="partial(permission-denied)"
  elif [ "$remote_rc" -ne 0 ]; then
    SSH_STATUS="partial(remote-rc=$remote_rc)"
  else
    SSH_STATUS=ok
  fi
}

COLLECTED_AT="$(date -u +%FT%TZ)"
KCTX="$(kubectl config current-context 2>/dev/null || echo unknown)"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMIT="$(git -C "$REPO_DIR" rev-parse HEAD 2>/dev/null || echo unknown)"
# 노드 SSH 계정. 소스에 박지 않는다 — 공개 저장소에 내부 계정이 실리면 안 된다.
SSH_USER="${SSH_USER:-ubuntu}"
NODE_IP="$(kubectl get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true)"

MANIFEST="$OUT/manifest.txt"
{
  echo "# dp-dra-residue snapshot manifest"
  echo "collected_at: $COLLECTED_AT"
  echo "node: $NODE"
  echo "vendor: $VENDOR (기록용 — 아래 조회는 벤더로 좁히지 않는다)"
  echo "kubectl_context: $KCTX"
  echo "operator_repo_commit: $COMMIT"
  echo
  printf '%-28s %-24s %s\n' "file" "status" "description"
} > "$MANIFEST"

add_manifest_row() {
  printf '%-28s %-24s %s\n' "$1" "$2" "$3" >> "$MANIFEST"
}

# ── K8s 자원 (kubectl) ──────────────────────────────────────────────
kget_filtered "$OUT/node-allocatable.json" \
  '{name:.metadata.name, allocatable:.status.allocatable, capacity:.status.capacity}' \
  node "$NODE"
add_manifest_row node-allocatable.json "$(manifest_status "$OUT/node-allocatable.json")" \
  "노드 status.allocatable/capacity 전부(도메인 필터 없음)"

kget_filtered "$OUT/node-labels.json" \
  '{name:.metadata.name, labels:.metadata.labels}' \
  node "$NODE"
add_manifest_row node-labels.json "$(manifest_status "$OUT/node-labels.json")" \
  "노드 라벨 전부(kcloud.ai/* present·dra-owned·driver-* 포함)"

kget "$OUT/resourceslices.json" resourceslices.resource.k8s.io
add_manifest_row resourceslices.json "$(manifest_status "$OUT/resourceslices.json")" \
  "ResourceSlice 전체(cluster-scoped, 노드 필터 없음)"

kget "$OUT/resourceclaims.json" resourceclaims.resource.k8s.io -A
add_manifest_row resourceclaims.json "$(manifest_status "$OUT/resourceclaims.json")" \
  "ResourceClaim 전체(전 네임스페이스)"

kget "$OUT/resourceclaimtemplates.json" resourceclaimtemplates.resource.k8s.io -A
add_manifest_row resourceclaimtemplates.json "$(manifest_status "$OUT/resourceclaimtemplates.json")" \
  "ResourceClaimTemplate 전체(전 네임스페이스)"

kget "$OUT/deviceclasses.json" deviceclasses.resource.k8s.io
add_manifest_row deviceclasses.json "$(manifest_status "$OUT/deviceclasses.json")" \
  "DeviceClass 전체(cluster-scoped)"

kget "$OUT/daemonsets.json" daemonsets.apps -A
add_manifest_row daemonsets.json "$(manifest_status "$OUT/daemonsets.json")" \
  "DaemonSet 전체(전 네임스페이스 — device-plugin·DRA driver DS 포함)"

kget "$OUT/pods.json" pods -A --field-selector "spec.nodeName=$NODE"
add_manifest_row pods.json "$(manifest_status "$OUT/pods.json")" \
  "이 노드의 파드 전체(이름·상태·이미지 포함)"

kget "$OUT/npuclusterpolicy.json" npuclusterpolicies.npu.ai -A
add_manifest_row npuclusterpolicy.json "$(manifest_status "$OUT/npuclusterpolicy.json")" \
  "NPUClusterPolicy 전체(전 벤더 블록 + status.advertiseSwitch)"

# ── 노드 위 파일 (SSH 읽기 전용) ─────────────────────────────────────
ssh_capture "$OUT/cdi-files.txt" \
  'ls -la /var/run/cdi /etc/cdi 2>&1'
add_manifest_row cdi-files.txt "$SSH_STATUS" "노드의 /var/run/cdi, /etc/cdi 목록(ssh)"

ssh_capture "$OUT/dev-nodes.txt" \
  "ls -la /dev | grep -iE 'nvidia|furiosa|npu|rngd|tenstorrent|rebellions|rbln|atom|accel' || echo '(no matching device nodes)'"
add_manifest_row dev-nodes.txt "$SSH_STATUS" "노드의 /dev 벤더 장치 노드 목록(ssh)"

ssh_capture "$OUT/kubelet-sockets.txt" \
  'ls -la /var/lib/kubelet/device-plugins/ 2>&1'
add_manifest_row kubelet-sockets.txt "$SSH_STATUS" "kubelet device-plugin 소켓 목록(ssh)"

echo "스냅샷을 $OUT 에 남겼다 — manifest: $MANIFEST" >&2
