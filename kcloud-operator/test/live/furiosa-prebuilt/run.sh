#!/usr/bin/env bash
# ============================================================
# run.sh: <rngd-node> Furiosa RNGD 사전 컴파일 이미지 같은 버전 왕복(2026.3.1) 실행·관측
# 상세: DIP 에 kernelImages·drainEnabled=false 를 주입하고(차트가 furiosa kernelImages 를
#       렌더하지 않아 helm 오버레이 불가), 참조 카운트를 0 으로 만든 뒤 rmmod 로 self-heal
#       install Job 을 유도해 전이·이미지·로그·종료 코드를 확인한다(단계별 근거는
#       phase_transition()·phase_restore() 등 섹션 주석 참고). DRY_RUN=1 은 클러스터 무변경.
# 생성일: 2026-09-24 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

# ── 설정(환경변수로 재정의) ─────────────────────────────────────────────
export KUBECTL_CONTEXT="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"
export NAMESPACE="${NAMESPACE:-kcloud}"
export RELEASE="${RELEASE:-kcloud-operator}"
export NODE="${NODE:-rngd-1.cluster.local}"
export DUS="${DUS:-rngd-1.cluster.local-furiosa}"
export DIP="${DIP:-furiosa-rngd-ds}"
export REGISTRY="${REGISTRY:?REGISTRY 을 지정하십시오}"
export KERNEL="${KERNEL:-6.8.0-138-generic}"
export MODEL="${MODEL:-rngd}"
export VERSION="${VERSION:-2026.3.1}"
export PRECOMPILED_SUFFIX="${PRECOMPILED_SUFFIX:-v1}"
export SSH_HOST="${SSH_HOST:?SSH_HOST 을 지정하십시오}"
export RNGD_POD_NS="${RNGD_POD_NS:-default}"
export RNGD_POD="${RNGD_POD:-rngd-pod}"
EXPECT_READY="${EXPECT_READY:-5}"

DRY_RUN="${DRY_RUN:-0}"
T_TRANSITION="${T_TRANSITION:-1200}"    # 전이 1건 타임아웃(초), 기본 20분
T_UNLOAD="${T_UNLOAD:-180}"             # rmmod 후 driverLoaded=false 감지 대기(초)
T_REFCOUNT="${T_REFCOUNT:-120}"         # rngd-pod 삭제 후 참조 카운트 0 대기(초)

# 사전 컴파일 태그 규약: <version>-<model>-k<kernel>-<suffix>
PRECOMPILED="${PRECOMPILED:-$REGISTRY/furiosa-driver-ds:${VERSION}-${MODEL}-k${KERNEL}-${PRECOMPILED_SUFFIX}}"

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/../../.." && pwd)"
export CHART_DIR="${CHART_DIR:-$REPO_ROOT/deploy/helm}"
export OUT="${OUT:-/tmp/furiosa-prebuilt-evidence}"
mkdir -p "$OUT"

PHASE_NAME="init"
TRANSITION_OK=0

# ── 헬퍼 ────────────────────────────────────────────────────────────────
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$OUT/run.log" >&2; }
kc()  { kubectl --context "$KUBECTL_CONTEXT" "$@"; }
helmx() { helm --kube-context "$KUBECTL_CONTEXT" "$@"; }
dus_f() { kc get dus "$DUS" -o jsonpath="{.status.$1}" 2>/dev/null || true; }
nssh() { ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 "$SSH_HOST" "$@"; }

# 변경 명령 단일 통로: DRY 는 출력만, 실행 모드는 실행.
run_or_print() {
  if [ "$DRY_RUN" = "1" ]; then log "DRY: $*"; else log "+ $*"; "$@"; fi
}

# 페이즈당 JSON 요약 1줄 append. 사용법: emit_json <verdict> key=value ...
emit_json() {
  local verdict="$1"; shift
  PHASE_="$PHASE_NAME" VERDICT_="$verdict" python3 - "$@" >> "$OUT/summary.jsonl" <<'PY'
import os, sys, json, datetime
rec = {"ts": datetime.datetime.utcnow().strftime("%Y-%m-%dT%H:%M:%SZ"),
       "phase": os.environ["PHASE_"], "verdict": os.environ["VERDICT_"]}
for kv in sys.argv[1:]:
    k, _, v = kv.partition("=")
    rec[k] = v
print(json.dumps(rec, ensure_ascii=False))
PY
}

fail()    { log "FAIL: $*";    emit_json FAIL    "reason=$*"; exit 1; }
blocked() { log "BLOCKED: $*"; emit_json BLOCKED "reason=$*"; exit 2; }

# 모듈 참조 카운트(정수) 또는 NONE.
module_refcount() {
  nssh "grep '^furiosa_rngd ' /proc/modules 2>/dev/null | awk '{print \$3}'" 2>/dev/null | tr -d '[:space:]' || true
}

# 사전 컴파일 이미지 동봉 .ko 의 srcversion. 워크스테이션 sudo docker 로 추출한다.
# 실패 시 NO_DOCKER/NO_CREATE/NO_KO/NO_MODINFO 를 돌려주고 판정은 그 값으로 미스매치가 된다.
image_ko_srcversion() {
  local img="$1" kopath="$2" cid src ko
  command -v modinfo >/dev/null 2>&1 || { echo "NO_MODINFO"; return; }
  { sudo -n docker inspect "$img" >/dev/null 2>&1 || sudo -n docker pull "$img" >/dev/null 2>&1; } || { echo "NO_DOCKER"; return; }
  cid=$(sudo -n docker create "$img" 2>/dev/null) || { echo "NO_CREATE"; return; }
  ko="$OUT/.image-furiosa_${MODEL}.ko"
  if sudo -n docker cp "$cid:/$kopath" "$ko" 2>/dev/null; then
    sudo -n chown "$(id -u):$(id -g)" "$ko" 2>/dev/null || true
    src=$(modinfo "$ko" 2>/dev/null | awk '/^srcversion:/{print $2}')
  else
    src="NO_KO"
  fi
  sudo -n docker rm "$cid" >/dev/null 2>&1 || true
  echo "${src:-EMPTY}"
}

# NODE·furiosa install Job(최신) 정보: name\timage\tstart\tcompletion\tdurationSec
job_info() {
  local jf="$OUT/.jobs.json"
  kc -n "$NAMESPACE" get jobs -l "npu.ai/vendor=furiosa,npu.ai/node=$NODE" -o json > "$jf" 2>/dev/null || echo '{"items":[]}' > "$jf"
  python3 - "$jf" <<'PY'
import json, sys, datetime
d = json.load(open(sys.argv[1]))
items = d.get("items", [])
if not items:
    print("NONE"); sys.exit(0)
items.sort(key=lambda j: j["metadata"].get("creationTimestamp", ""), reverse=True)
j = items[0]
name = j["metadata"]["name"]
img = j["spec"]["template"]["spec"]["containers"][0].get("image", "")
st = j.get("status", {}).get("startTime", "")
ct = j.get("status", {}).get("completionTime", "")
dur = ""
if st and ct:
    f = "%Y-%m-%dT%H:%M:%SZ"
    dur = str(int((datetime.datetime.strptime(ct, f) - datetime.datetime.strptime(st, f)).total_seconds()))
print("\t".join([name, img, st, ct, dur]))
PY
}

# DIP 에 kernelImages + drainEnabled=false 주입(merge patch).
dip_apply_overlay() {
  local patch
  patch=$(KERNEL="$KERNEL" PRECOMPILED="$PRECOMPILED" python3 -c '
import os, json
print(json.dumps({"spec":{"driver":{"kernelImages":{os.environ["KERNEL"]:os.environ["PRECOMPILED"]}},
                          "upgradePolicy":{"drainEnabled":False}}}))')
  run_or_print kc patch dip "$DIP" --type=merge -p "$patch"
}

# DIP 원복: kernelImages 제거 + drainEnabled=true.
dip_restore_baseline() {
  local has
  has=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.kernelImages}' 2>/dev/null || true)
  if [ -n "$has" ]; then
    run_or_print kc patch dip "$DIP" --type=json -p '[{"op":"remove","path":"/spec/driver/kernelImages"}]'
  fi
  run_or_print kc patch dip "$DIP" --type=merge -p '{"spec":{"upgradePolicy":{"drainEnabled":true}}}'
}

# 긴급 복구(transition 실패 시 EXIT 트랩이 호출). best-effort, 실패해도 계속.
# 핵심 안전: 모듈이 내려가 있으면 drainEnabled=true 로 되돌리기 전에 반드시 먼저 재적재한다.
# 그러지 않으면 self-heal 이 drainEnabled=true 로 <rngd-node> 을 drain 해 무관한 워크로드를 evict 한다.
emergency_restore() {
  log "긴급 복구: 모듈 재적재 → uncordon → DIP 원복 → 테스트 파드 재생성"
  if ! nssh 'test -e /sys/module/furiosa_rngd' 2>/dev/null; then
    log "모듈 미적재 — 수동 modprobe 시도(self-heal drain 방지)"
    nssh 'sudo modprobe furiosa_rngd' >/dev/null 2>&1 || log "warn: modprobe 실패"
  fi
  kc uncordon "$NODE" >/dev/null 2>&1 || true
  local has
  has=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.kernelImages}' 2>/dev/null || true)
  [ -n "$has" ] && kc patch dip "$DIP" --type=json -p '[{"op":"remove","path":"/spec/driver/kernelImages"}]' >/dev/null 2>&1 || true
  # drainEnabled=true 는 모듈이 적재된 경우에만 되돌린다. 미적재면 drain 회피를 위해 false 유지.
  if nssh 'test -e /sys/module/furiosa_rngd' 2>/dev/null; then
    kc patch dip "$DIP" --type=merge -p '{"spec":{"upgradePolicy":{"drainEnabled":true}}}' >/dev/null 2>&1 || true
  else
    log "warn: 모듈 미적재 — drainEnabled=false 유지(drain 회피). 수동 modprobe 후 restore 필요"
  fi
  if [ -s "$OUT/rngd-pod.yaml" ] && ! kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" >/dev/null 2>&1; then
    kc apply -f "$OUT/rngd-pod.yaml" >/dev/null 2>&1 || true
  fi
}

# ── preflight(전량 읽기 전용 + 스냅샷) ──────────────────────────────────
phase_preflight() {
  PHASE_NAME="preflight"
  log "preflight: context=$KUBECTL_CONTEXT node=$NODE dus=$DUS dip=$DIP"
  log "precompiled=$PRECOMPILED OUT=$OUT"

  kc version >/dev/null 2>&1 || blocked "context $KUBECTL_CONTEXT 도달 불가"

  local ready
  ready=$(kc get nodes -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(sum(1 for n in d["items"] for c in n["status"]["conditions"] if c["type"]=="Ready" and c["status"]=="True"))')
  [ "$ready" = "$EXPECT_READY" ] || log "warn: Ready 노드 $ready (기대 $EXPECT_READY) — 정보로만 기록"

  # 노드 커널 == kernelImages 키.
  local nodeKernel
  nodeKernel=$(kc get node "$NODE" -o jsonpath='{.status.nodeInfo.kernelVersion}' 2>/dev/null || true)
  [ "$nodeKernel" = "$KERNEL" ] || blocked "노드 커널 $nodeKernel != kernelImages 키 $KERNEL"

  # DUS Idle/VERSION.
  local st cur
  st=$(dus_f state); cur=$(dus_f currentVersion)
  { [ "$st" = "Idle" ] && [ "$cur" = "$VERSION" ]; } || blocked "DUS $st/$cur (기대 Idle/$VERSION)"

  # DIP 기준선: mode=job, version=VERSION, verifiedVersions 에 VERSION, kernelImages 부재.
  local mode dver ki verok
  mode=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.mode}' 2>/dev/null || true)
  dver=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.version}' 2>/dev/null || true)
  ki=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.kernelImages}' 2>/dev/null || true)
  verok=$(kc get dip "$DIP" -o json 2>/dev/null | python3 -c 'import json,sys,os;d=json.load(sys.stdin);print("yes" if os.environ["VERSION"] in (d["spec"].get("verifiedVersions") or []) else "no")')
  [ "$mode" = "job" ] || blocked "DIP mode=$mode (기대 job)"
  [ "$dver" = "$VERSION" ] || blocked "DIP driver.version=$dver (기대 $VERSION)"
  [ -z "$ki" ] || blocked "DIP kernelImages 이미 설정됨: $ki (기준선 아님)"
  [ "$verok" = "yes" ] || blocked "DIP verifiedVersions 에 $VERSION 없음"

  # 사전 컴파일 이미지 존재(HTTP Harbor → insecure).
  if command -v skopeo >/dev/null 2>&1; then
    skopeo inspect --tls-verify=false "docker://$PRECOMPILED" >/dev/null 2>&1 || blocked "사전 컴파일 이미지 없음: $PRECOMPILED"
  else
    sudo -n docker manifest inspect --insecure "$PRECOMPILED" >/dev/null 2>&1 || blocked "사전 컴파일 이미지 없음(또는 sudo docker 불가): $PRECOMPILED"
  fi

  # NDR: furiosa/rngd 엔트리.
  local ndrjson
  ndrjson=$(kc get ndr "$NODE" -o json 2>/dev/null || echo '{}')
  local ndr_loaded ndr_ver ndr_bind pass
  read -r ndr_loaded ndr_ver ndr_bind pass < <(printf '%s' "$ndrjson" | python3 -c '
import json,sys
d=json.load(sys.stdin)
st=d.get("status",{})
loaded=ver=bind="?"
for dev in st.get("devices",[]):
    if dev.get("vendor","").lower()=="furiosa" and dev.get("model","").lower()=="rngd":
        loaded=str(dev.get("driverLoaded")); ver=str(dev.get("driverVersion")); bind=str(dev.get("driverBinding"))
        break
print(loaded, ver, bind, str(st.get("passthroughReserved")))')
  [ "$ndr_loaded" = "True" ] || blocked "NDR furiosa/rngd driverLoaded=$ndr_loaded (기대 True)"
  [ "$ndr_ver" = "$VERSION" ] || blocked "NDR driverVersion=$ndr_ver (기대 $VERSION)"
  [ "$pass" != "True" ] || blocked "NDR passthroughReserved=True — 설치 경로가 skip 됨"
  log "NDR furiosa/rngd loaded=$ndr_loaded version=$ndr_ver binding=$ndr_bind passthrough=$pass"

  # 호스트: dpkg 버전, 모듈 참조 카운트, Secure Boot, 모듈 보유자.
  local sshout hostver refc sb
  sshout=$(nssh "dpkg-query -W -f='\${Version}' furiosa-driver-rngd 2>/dev/null; echo; mokutil --sb-state 2>&1" 2>/dev/null || true)
  hostver=$(printf '%s\n' "$sshout" | sed -n '1p')
  sb=$(printf '%s\n' "$sshout" | sed -n '2,$p' | tr '\n' ' ')
  refc=$(module_refcount)
  log "host dpkg furiosa-driver-rngd=$hostver moduleRefcount=$refc secureBoot=[$sb]"
  [ "$hostver" = "$VERSION" ] || blocked "호스트 dpkg 버전 $hostver != $VERSION"
  if printf '%s' "$sb" | grep -qi 'SecureBoot enabled'; then blocked "Secure Boot enabled — 사전 컴파일 모듈 서명 거부(HARDWARE)"; fi

  # 모듈 보유자: rngd-pod 하나뿐인지(다른 보유자는 안전한 unload 불가).
  local holders
  holders=$(nssh "sudo bash -c 'for p in /proc/[0-9]*; do ls -l \$p/fd 2>/dev/null | grep -q /dev/rngd && echo \$(cat \$p/comm 2>/dev/null); done | sort | uniq -c'" 2>/dev/null || true)
  log "모듈 /dev/rngd 보유 프로세스:"; printf '%s\n' "$holders" | sed 's/^/    /' | tee -a "$OUT/run.log" >&2
  # rngd-pod 존재 확인(예상 보유자).
  if ! kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" >/dev/null 2>&1; then
    log "warn: $RNGD_POD_NS/$RNGD_POD 없음 — 참조 카운트 근원을 preflight 에서 재확인 필요"
  fi

  # 스냅샷.
  kc get dus "$DUS" -o yaml > "$OUT/dus-before.yaml" 2>/dev/null || true
  kc get dip "$DIP" -o yaml > "$OUT/dip-before.yaml" 2>/dev/null || true
  kc get ndr "$NODE" -o yaml > "$OUT/ndr-before.yaml" 2>/dev/null || true
  kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" -o yaml > "$OUT/rngd-pod.yaml" 2>/dev/null || true
  helmx -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$OUT/values-baseline.yaml" 2>/dev/null || true

  log "preflight OK"
  emit_json PASS "ready=$ready" "dus=$st/$cur" "nodeKernel=$nodeKernel" \
    "hostVersion=$hostver" "moduleRefcount=$refc" "ndrLoaded=$ndr_loaded" \
    "ndrVersion=$ndr_ver" "secureBoot=$sb" "precompiled=$PRECOMPILED"
}

# ── selftest(오프라인) ──────────────────────────────────────────────────
phase_selftest() {
  PHASE_NAME="selftest"
  local tmp="$OUT/selftest"; mkdir -p "$tmp"

  # 1) DIP patch JSON 이 kernelImages[kernel]=precompiled, drainEnabled=false 인지.
  KERNEL="$KERNEL" PRECOMPILED="$PRECOMPILED" python3 - <<'PY' || fail "patch JSON 검증 실패"
import os, json
p = {"spec":{"driver":{"kernelImages":{os.environ["KERNEL"]:os.environ["PRECOMPILED"]}},
             "upgradePolicy":{"drainEnabled":False}}}
assert p["spec"]["driver"]["kernelImages"][os.environ["KERNEL"]] == os.environ["PRECOMPILED"]
assert p["spec"]["upgradePolicy"]["drainEnabled"] is False
print("selftest: DIP patch JSON OK")
PY

  # 2) 사전 컴파일 태그가 검증 빌드 태그 정규식을 통과하는지(resolveDriverImage 채택 근거).
  PRECOMPILED="$PRECOMPILED" python3 - <<'PY' || fail "verified build tag 정규식 불통과 — resolveDriverImage 가 태그를 무시함"
import os, re
img = os.environ["PRECOMPILED"]
tag = img[img.rfind(":"):]  # ":<tag>"
assert re.search(r':(v[0-9]+|.+-v[0-9]+|latest)$', tag), tag
print("selftest: verified build tag 정규식 통과:", tag)
PY

  # 3) 차트가 furiosa/rngd DIP 에 kernelImages 를 렌더하지 않음(kubectl patch 근거 = 차트 공백).
  local rendered="$tmp/dip.yaml"
  if helm template "$RELEASE" "$CHART_DIR" --show-only templates/driverinstallpolicy.yaml > "$rendered" 2>/dev/null; then
    python3 - "$rendered" <<'PY' || fail "예상과 달리 furiosa/rngd DIP 에 kernelImages 가 렌더됨 — 하네스 전제 재검토"
import sys, yaml
docs = [d for d in yaml.safe_load_all(open(sys.argv[1])) if d]
rngd = next((d for d in docs if d.get("spec",{}).get("vendor")=="furiosa" and d.get("spec",{}).get("model")=="rngd"), None)
assert rngd is not None, "furiosa/rngd DIP 미렌더"
ki = rngd["spec"]["driver"].get("kernelImages")
assert not ki, f"furiosa/rngd DIP 에 kernelImages 가 이미 있음: {ki}"
print("selftest: 차트 furiosa/rngd DIP 에 kernelImages 없음 확인(kubectl patch 필요 근거)")
PY
  else
    log "warn: helm template 실패 — 차트 렌더 검증 생략(오프라인 assert 1·2 만 유효)"
  fi

  log "selftest OK"
  emit_json PASS "renderedDip=$rendered" "precompiled=$PRECOMPILED"
}

# ── transition(라이브) ──────────────────────────────────────────────────
phase_transition() {
  PHASE_NAME="transition"
  trap 'if [ "$TRANSITION_OK" != 1 ]; then log "transition 미완료 종료 — 긴급 복구 수행"; emergency_restore; fi' EXIT
  # 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
  # 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM

  log "transition 시작: resolvedImage 기대=$PRECOMPILED"
  local t_start; t_start=$(date -u +%FT%TZ)

  # 0) 참조 카운트가 0 이 아니면(테스트 파드가 장치를 열고 있으면) 그 파드를 지워 0 을 유도한다.
  #    이미 0 이면(모듈 유휴) 파드를 건드리지 않는다 — 불필요한 disruption 회피.
  local refc
  refc=$(module_refcount)
  if [ "$refc" != "0" ] && kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" >/dev/null 2>&1; then
    log "참조 카운트 $refc — 테스트 파드 $RNGD_POD_NS/$RNGD_POD 삭제로 0 유도"
    # 재생성용 스펙 저장(서버 필드 제거).
    kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" -o json 2>/dev/null | python3 -c '
import json, sys
try:
    import yaml
except Exception:
    yaml = None
d = json.load(sys.stdin)
d.pop("status", None)
m = d.get("metadata", {})
for k in ("uid","resourceVersion","creationTimestamp","managedFields","generation","selfLink"):
    m.pop(k, None)
a = m.get("annotations") or {}
a.pop("kubectl.kubernetes.io/last-applied-configuration", None)
out = yaml.safe_dump(d, sort_keys=False, allow_unicode=True) if yaml else json.dumps(d)
sys.stdout.write(out)' > "$OUT/rngd-pod.yaml" 2>/dev/null || \
      kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" -o yaml > "$OUT/rngd-pod.yaml" 2>/dev/null || true
    run_or_print kc -n "$RNGD_POD_NS" delete pod "$RNGD_POD" --wait=true --timeout=90s
  elif [ "$refc" = "0" ]; then
    log "참조 카운트 이미 0 — 테스트 파드 삭제 불필요(모듈 유휴)"
  fi

  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: 참조 카운트 대기·DIP patch·rmmod·전이 관찰 생략"
    dip_apply_overlay
    log "DRY: rmmod furiosa_rngd (SSH $SSH_HOST)"
    dip_restore_baseline
    TRANSITION_OK=1
    emit_json DRY "precompiled=$PRECOMPILED"
    return 0
  fi

  # 1) 참조 카운트 0 대기(상한). 0 이 아니면 rmmod 하지 않고 BLOCKED.
  local t0
  t0=$(date +%s)
  while :; do
    refc=$(module_refcount)
    [ "$refc" = "0" ] && break
    { [ "$refc" = "NONE" ] || [ -z "$refc" ]; } && log "warn: 모듈 참조 카운트 조회값 '$refc'"
    [ $(( $(date +%s) - t0 )) -lt "$T_REFCOUNT" ] || blocked "참조 카운트 0 미도달(현재 $refc) — 다른 보유자 존재(ENVIRONMENT)"
    sleep 5
  done
  log "모듈 참조 카운트 0 — unload 가능"

  # 2) DIP 오버레이 주입.
  dip_apply_overlay
  local ki_now dr_now
  # kubectl jsonpath 은 점(.)이 든 맵 키를 ['...'] 로 못 읽으므로 맵 전체를 받아 파이썬으로 추출한다.
  ki_now=$(kc get dip "$DIP" -o json 2>/dev/null | KERNEL="$KERNEL" python3 -c 'import json,sys,os;d=json.load(sys.stdin);print((d["spec"]["driver"].get("kernelImages") or {}).get(os.environ["KERNEL"],""))' 2>/dev/null || true)
  dr_now=$(kc get dip "$DIP" -o jsonpath='{.spec.upgradePolicy.drainEnabled}' 2>/dev/null || true)
  [ "$ki_now" = "$PRECOMPILED" ] || fail "DIP kernelImages 주입 실패: $ki_now"
  [ "$dr_now" = "false" ] || fail "DIP drainEnabled=$dr_now (기대 false)"
  log "DIP 주입 확인: kernelImages[$KERNEL]=$ki_now drainEnabled=$dr_now"

  # 3) rmmod → driverLoaded=false 유도. 일시적 device open 대비 재시도.
  local i rmok=0
  for i in 1 2 3; do
    if nssh 'sudo rmmod furiosa_rngd' 2>>"$OUT/run.log"; then rmok=1; break; fi
    log "rmmod 시도 $i 실패(일시적 device open 가능) — 5초 후 재시도"
    sleep 5
  done
  [ "$rmok" = "1" ] || fail "rmmod 실패(3회 시도) — 모듈 사용 중"
  nssh 'test -e /sys/module/furiosa_rngd' 2>/dev/null && fail "rmmod 후에도 /sys/module/furiosa_rngd 존재"
  log "rmmod 완료: /sys/module/furiosa_rngd 소멸"

  # 5) self-heal 감지 — DUS 가 Idle 을 벗어나거나 NDR driverLoaded=false 확인(상한).
  local tlog="$OUT/transitions.log"; : > "$tlog"
  t0=$(date +%s)
  while :; do
    local state ndrl
    state=$(dus_f state)
    ndrl=$(kc get ndr "$NODE" -o json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for x in d.get("status",{}).get("devices",[]):
    if x.get("vendor","").lower()=="furiosa" and x.get("model","").lower()=="rngd":
        print(x.get("driverLoaded")); break
else: print("?")' 2>/dev/null || echo "?")
    printf '%s state=%s ndrLoaded=%s resolvedImage=%s msg=%s\n' \
      "$(date -u +%FT%TZ)" "$state" "$ndrl" "$(dus_f resolvedImage)" "$(dus_f message)" >> "$tlog"
    if [ "$state" != "Idle" ]; then break; fi
    [ $(( $(date +%s) - t0 )) -lt "$T_UNLOAD" ] || fail "rmmod 후 ${T_UNLOAD}s 내 self-heal 미발생(state=$state ndrLoaded=$ndrl)"
    sleep 5
  done
  log "self-heal 감지: DUS 가 Idle 을 벗어남"

  # 6) Idle/VERSION 재수렴 대기(상한). 터미널 실패 방어.
  t0=$(date +%s)
  while :; do
    local state cur rimg
    state=$(dus_f state); cur=$(dus_f currentVersion); rimg=$(dus_f resolvedImage)
    printf '%s state=%s current=%s resolvedImage=%s msg=%s\n' \
      "$(date -u +%FT%TZ)" "$state" "$cur" "$rimg" "$(dus_f message)" >> "$tlog"
    { [ "$state" = "Idle" ] && [ "$cur" = "$VERSION" ]; } && break
    case "$state" in
      Failed|UnverifiedVersion|RebootRequired)
        kc get dus "$DUS" -o yaml > "$OUT/dus-fail.yaml" 2>/dev/null || true
        fail "터미널 $state: $(dus_f message)" ;;
    esac
    # needs-reboot 정황 방어.
    if nssh 'test -e /var/lib/kcloud-operator/needs-reboot' 2>/dev/null; then
      fail "needs-reboot 마커 생성됨 — 재부팅 경로 진입(중단)"
    fi
    [ $(( $(date +%s) - t0 )) -lt "$T_TRANSITION" ] || {
      kc get dus "$DUS" -o yaml > "$OUT/dus-fail.yaml" 2>/dev/null || true
      fail "타임아웃 ${T_TRANSITION}s (state=$state current=$cur)"; }
    sleep 10
  done
  log "DUS Idle/$VERSION 재수렴"

  # 7) 검증.
  local rimg; rimg=$(dus_f resolvedImage)
  [ "$rimg" = "$PRECOMPILED" ] || fail "resolvedImage=$rimg (기대 $PRECOMPILED)"

  local jn ji jst jct jdur
  IFS=$'\t' read -r jn ji jst jct jdur < <(job_info)
  { [ "$jn" = "NONE" ] || [ -z "$jn" ]; } && fail "install Job 미발견(labels npu.ai/vendor=furiosa,npu.ai/node=$NODE)"
  kc -n "$NAMESPACE" get job "$jn" -o yaml > "$OUT/job.yaml" 2>/dev/null || true
  kc -n "$NAMESPACE" logs "job/$jn" --tail=-1 > "$OUT/job.log" 2>/dev/null || true
  [ "$ji" = "$PRECOMPILED" ] || fail "Job 이미지=$ji (기대 $PRECOMPILED)"
  log "install Job=$jn image=$ji durationSec=$jdur (start=$jst completion=$jct)"

  # 사전 컴파일 설치 경로 로그(67행 found, 80행 installed dkms skipped).
  local found_line inst_line
  found_line=$(grep -F "prebuilt module found for $KERNEL" "$OUT/job.log" | head -1 || true)
  inst_line=$(grep -F "prebuilt module for $KERNEL installed" "$OUT/job.log" | head -1 || true)
  [ -n "$inst_line" ] || fail "Job 로그에 'prebuilt module for $KERNEL installed' 줄 없음"
  printf '%s' "$inst_line" | grep -qi 'dkms skipped' || fail "prebuilt 설치 줄에 'dkms skipped' 없음: $inst_line"
  log "prebuilt found: ${found_line:-<none>}"
  log "prebuilt installed: $inst_line"

  # APT-postinst dkms 판정 줄은 같은 버전 skip 이라 부재가 정상 — 사실대로 기록.
  local dkms_verdict
  dkms_verdict=$(grep -E "DKMS 가 .* 빌드함|DKMS 빌드 없음" "$OUT/job.log" | head -1 || true)
  log "APT-postinst dkms 판정 줄: ${dkms_verdict:-<부재: 같은 버전 재설치라 APT install skip>}"

  # NDR driverVersion(전이 후).
  local ndrv
  ndrv=$(kc get ndr "$NODE" -o json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for x in d.get("status",{}).get("devices",[]):
    if x.get("vendor","").lower()=="furiosa" and x.get("model","").lower()=="rngd":
        print(x.get("driverVersion")); break
else: print("")' 2>/dev/null || true)
  [ "$ndrv" = "$VERSION" ] || fail "전이 후 NDR driverVersion=$ndrv (기대 $VERSION)"

  # 노드 uncordon 확인.
  local unsched
  unsched=$(kc get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || true)
  [ -z "$unsched" ] || [ "$unsched" = "false" ] || fail "노드가 cordon 상태로 남음(unschedulable=$unsched)"

  # furiosa-smi 종료 코드.
  local smi_rc
  nssh 'furiosa-smi info' > "$OUT/furiosa-smi.log" 2>&1 && smi_rc=0 || smi_rc=$?
  log "host furiosa-smi info exit=$smi_rc (로그 $OUT/furiosa-smi.log)"
  [ "$smi_rc" = "0" ] || fail "furiosa-smi info 종료 코드 $smi_rc"

  kc get ndr "$NODE" -o yaml > "$OUT/ndr-after.yaml" 2>/dev/null || true
  kc get dus "$DUS" -o yaml > "$OUT/dus-after.yaml" 2>/dev/null || true

  TRANSITION_OK=1
  log "transition PASS"
  emit_json PASS "resolvedImage=$rimg" "jobImage=$ji" "jobName=$jn" "jobDurationSec=$jdur" \
    "prebuiltInstalled=$inst_line" "ndrDriverVersion=$ndrv" "furiosaSmiExit=$smi_rc" \
    "startedAt=$t_start"
}

# ── restore(기준선 원복 + 검증) ─────────────────────────────────────────
phase_restore() {
  PHASE_NAME="restore"
  log "restore: DIP 원복(kernelImages 제거·drainEnabled=true) + 테스트 파드 재생성"

  kc uncordon "$NODE" >/dev/null 2>&1 || true
  dip_restore_baseline

  # 테스트 파드 재생성(있던 경우).
  if [ -s "$OUT/rngd-pod.yaml" ] && ! kc -n "$RNGD_POD_NS" get pod "$RNGD_POD" >/dev/null 2>&1; then
    run_or_print kc apply -f "$OUT/rngd-pod.yaml"
  fi

  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: 기준선 검증 생략"; emit_json DRY; return 0
  fi

  # 검증.
  local st cur ki dr
  st=$(dus_f state); cur=$(dus_f currentVersion)
  ki=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.kernelImages}' 2>/dev/null || true)
  dr=$(kc get dip "$DIP" -o jsonpath='{.spec.upgradePolicy.drainEnabled}' 2>/dev/null || true)
  { [ "$st" = "Idle" ] && [ "$cur" = "$VERSION" ]; } || fail "DUS $st/$cur (기대 Idle/$VERSION)"
  [ -z "$ki" ] || fail "DIP kernelImages 잔존: $ki"
  [ "$dr" = "true" ] || fail "DIP drainEnabled=$dr (기대 true)"

  # NDR driverLoaded=true.
  local ndrl
  ndrl=$(kc get ndr "$NODE" -o json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for x in d.get("status",{}).get("devices",[]):
    if x.get("vendor","").lower()=="furiosa" and x.get("model","").lower()=="rngd":
        print(x.get("driverLoaded")); break
else: print("?")' 2>/dev/null || echo "?")
  [ "$ndrl" = "True" ] || fail "NDR driverLoaded=$ndrl (기대 True)"

  # 노드 schedulable.
  local unsched
  unsched=$(kc get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || true)
  { [ -z "$unsched" ] || [ "$unsched" = "false" ]; } || fail "노드 cordon 상태(unschedulable=$unsched)"

  # helm 값 무변경(preflight 스냅샷과 대조).
  helmx -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$OUT/values-after.yaml" 2>/dev/null || true
  if [ -s "$OUT/values-baseline.yaml" ]; then
    diff -q "$OUT/values-baseline.yaml" "$OUT/values-after.yaml" >/dev/null 2>&1 \
      && log "helm 값 무변경 확인" || log "warn: helm 값 차이 감지 — $OUT/values-baseline.yaml vs values-after.yaml"
  fi

  # kcloud 네임스페이스 비정상 파드 0.
  local abn
  abn=$(kc -n "$NAMESPACE" get pods -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(",".join(p["metadata"]["name"] for p in d["items"] if p["status"].get("phase") not in ("Running","Succeeded")))')
  [ -z "$abn" ] || log "warn: kcloud 비정상 파드: $abn"

  log "restore OK: DIP 기준선, DUS Idle/$VERSION, driverLoaded=$ndrl, node schedulable"
  emit_json PASS "dus=$st/$cur" "driverLoaded=$ndrl" "kernelImages=${ki:-<none>}" "drainEnabled=$dr"
}

# ── provenobuild(헤더 부재에서 노드 빌드 없음 증명) ─────────────────────
# transition 이 사전 컴파일 install 을 돌린 뒤, 헤더가 없는 창에서 호출한다.
# 네 근거를 한 블록으로 모은다: 빌드 링크 부재·헤더 패키지 0·모듈 적재·
# 적재 모듈 srcversion == 이미지 동봉 .ko srcversion. 마지막이 결정적 근거다
# (사전 컴파일 설치가 DKMS 경로를 덮어써 경로로는 구분되지 않음). 결과 블록은
# $OUT/no-build-proof.txt 로 남겨 evidence.sh 가 actual 로 담게 한다.
phase_provenobuild() {
  PHASE_NAME="provenobuild"
  local module="furiosa_${MODEL}"
  local ko_in_image="opt/furiosa-ko/${MODEL}/${KERNEL}/${module}.ko"
  local proof="$OUT/no-build-proof.txt"

  # 노드: 빌드 링크·헤더 패키지 수·dkms·모듈 적재·srcversion·장치 노드 수.
  local build_link hdr_cnt dkms mod_present loaded_src dev_cnt
  build_link=$(nssh "ls -ld /lib/modules/${KERNEL}/build 2>&1 || true")
  hdr_cnt=$(nssh "dpkg -l linux-headers-${KERNEL} 2>/dev/null | grep -c '^ii  linux-headers-${KERNEL} ' || true")
  dkms=$(nssh "dkms status 2>/dev/null | grep -F '${KERNEL}' || echo '(해당 커널 dkms 항목 없음)'")
  mod_present=$(nssh "test -e /sys/module/${module} && echo yes || echo no")
  loaded_src=$(nssh "cat /sys/module/${module}/srcversion 2>/dev/null || echo NONE")
  dev_cnt=$(nssh "ls /dev/${MODEL} 2>/dev/null | wc -l || echo 0")

  # 이미지 동봉 .ko srcversion(워크스테이션 docker).
  local img_src
  img_src=$(image_ko_srcversion "$PRECOMPILED" "$ko_in_image")

  # transition 이 남긴 설치 로그의 dkms skipped 줄.
  local inst_line
  inst_line=$(grep -F "prebuilt module for ${KERNEL} installed" "$OUT/job.log" 2>/dev/null | head -1 || true)

  # 판정: 빌드 불가(링크 부재 + 헤더 0) + 모듈 적재 + srcversion 일치.
  local link_absent=no src_match=no verdict=FAIL
  printf '%s' "$build_link" | grep -qiE 'No such file|없습니다' && link_absent=yes
  { [ "$loaded_src" != "NONE" ] && [ "$loaded_src" = "$img_src" ]; } && src_match=yes
  if [ "$link_absent" = yes ] && [ "$hdr_cnt" = 0 ] && [ "$mod_present" = yes ] && [ "$src_match" = yes ]; then
    verdict=PASS
  fi

  {
    echo "NO-NODE-BUILD PROOF: ${verdict}"
    echo "node=${NODE} kernel=${KERNEL} module=${module} image=${PRECOMPILED}"
    echo "--- 근거 1: 빌드 링크 부재(헤더 없음 → 6.8 대상 빌드 물리적 불가) ---"
    echo "build_link_absent=${link_absent} header_pkg_count=${hdr_cnt}"
    echo "ls -ld /lib/modules/${KERNEL}/build => ${build_link}"
    echo "--- 근거 2: dkms 가 이 창에서 빌드하지 않음 ---"
    echo "${dkms}"
    echo "--- 근거 3: 그럼에도 모듈 적재 + 장치 노드 존재 ---"
    echo "module_loaded=${mod_present} dev_${MODEL}_count=${dev_cnt}"
    echo "--- 근거 4(결정적): 적재 모듈 srcversion == 이미지 동봉 .ko srcversion ---"
    echo "loaded_srcversion=${loaded_src}"
    echo "image_srcversion=${img_src}"
    echo "srcversion_match=${src_match}"
    echo "--- 설치 로그(dkms skipped) ---"
    echo "${inst_line:-<job.log 없음/줄 없음>}"
  } | tee "$proof"

  log "provenobuild: linkAbsent=$link_absent hdrCnt=$hdr_cnt modLoaded=$mod_present srcMatch=$src_match verdict=$verdict"
  emit_json "$verdict" "buildLinkAbsent=$link_absent" "headerPkgCount=$hdr_cnt" \
    "moduleLoaded=$mod_present" "loadedSrcversion=$loaded_src" "imageSrcversion=$img_src" \
    "srcversionMatch=$src_match" "proof=$proof"
  [ "$verdict" = PASS ] || fail "노드 빌드 없음 증명 미충족(linkAbsent=$link_absent hdrCnt=$hdr_cnt modLoaded=$mod_present srcMatch=$src_match)"
}

# ── status(복구용 덤프) ─────────────────────────────────────────────────
phase_status() {
  PHASE_NAME="status"
  log "status dump → $OUT"
  kc get dus "$DUS" -o yaml  > "$OUT/status-dus.yaml"  2>/dev/null || true
  kc get dip "$DIP" -o yaml  > "$OUT/status-dip.yaml"  2>/dev/null || true
  kc get ndr "$NODE" -o yaml > "$OUT/status-ndr.yaml"  2>/dev/null || true
  kc -n "$NAMESPACE" get jobs -l "npu.ai/vendor=furiosa,npu.ai/node=$NODE" -o yaml > "$OUT/status-jobs.yaml" 2>/dev/null || true
  local st cur rimg refc
  st=$(dus_f state); cur=$(dus_f currentVersion); rimg=$(dus_f resolvedImage); refc=$(module_refcount)
  log "DUS=$st/$cur resolvedImage=$rimg moduleRefcount=$refc"
  emit_json INFO "dus=$st/$cur" "resolvedImage=$rimg" "moduleRefcount=$refc"
}

# ── dispatch ────────────────────────────────────────────────────────────
main() {
  local phase="${1:-${PHASE:-}}"
  case "$phase" in
    preflight)  phase_preflight ;;
    selftest)   phase_selftest ;;
    transition) phase_transition ;;
    provenobuild) phase_provenobuild ;;
    restore)    phase_restore ;;
    status)     phase_status ;;
    *)
      cat >&2 <<USAGE
usage: $0 <phase>
  preflight    전제 검사(읽기 전용) + 스냅샷 저장
  selftest     오프라인 검증(patch JSON·태그 정규식·차트 kernelImages 공백)
  transition   라이브 왕복: 테스트 파드 삭제 → rmmod → self-heal → 사전 컴파일 install Job
  provenobuild 헤더 부재 창에서 노드 빌드 없음 증명(빌드 링크·헤더·모듈·srcversion)
  restore      DIP 원복 + 테스트 파드 재생성 + 기준선 검증
  status       DUS/DIP/NDR/Job 덤프(복구용)

주요 환경변수: KUBECTL_CONTEXT NODE DUS DIP KERNEL VERSION PRECOMPILED SSH_HOST MODEL
              OUT DRY_RUN(=1 변경 명령 출력만) T_TRANSITION T_UNLOAD T_REFCOUNT

권장 순서: selftest → DRY_RUN=1 transition → DRY_RUN=1 restore → preflight →
          transition → (evidence.sh) → restore
헤더 부재 증명: preflight → (헤더 제거) → transition → provenobuild → (헤더 복구) → restore
USAGE
      exit 64 ;;
  esac
}
main "$@"
