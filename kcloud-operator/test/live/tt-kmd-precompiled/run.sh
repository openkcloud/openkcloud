#!/usr/bin/env bash
# ============================================================
# run.sh: <tenstorrent-node> tt-kmd 사전 컴파일 이미지 왕복(2.11.0→2.8.0→2.11.0) 실행·관측
# 상세: values 오버레이로 kernelImages·allowDowngrade·autoUpgrade 를 줘 전이를 시작한다(phase 별
#       기록 항목은 README 참고). DRY_RUN=1 은 명령만 출력한다(클러스터 무변경).
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

# ── 설정(환경변수로 재정의) ─────────────────────────────────────────────
export KUBECTL_CONTEXT="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"
export NAMESPACE="${NAMESPACE:-kcloud}"
export RELEASE="${RELEASE:-kcloud-operator}"
export NODE="${NODE:-k8s-worker3}"
export DUS="${DUS:-k8s-worker3-tenstorrent}"
export DIP="${DIP:-tenstorrent-blackhole-ds}"
export REGISTRY="${REGISTRY:?REGISTRY 을 지정하십시오}"
export KERNEL="${KERNEL:-5.15.0-190-generic}"
export PRECOMPILED_SUFFIX="${PRECOMPILED_SUFFIX:-v1}"
export SSH_HOST="${SSH_HOST:?SSH_HOST 을 지정하십시오}"
PROBE_IMAGE="${PROBE_IMAGE:?PROBE_IMAGE 을 지정하십시오}"
EXPECT_READY="${EXPECT_READY:-5}"
BASE_VERSION="${BASE_VERSION:-2.11.0}"       # 기준선(원복 후) DUS 버전
BASE_TAG="${BASE_TAG:-2.11.0-v1}"            # 기준선 DIP 기본 이미지 태그(DKMS)

DRY_RUN="${DRY_RUN:-0}"
T_TRANSITION="${T_TRANSITION:-1200}"         # 전이 1건 타임아웃(초), 기본 20분
HELM_TIMEOUT="${HELM_TIMEOUT:-10m}"          # helm --wait 타임아웃
HELM_EXTRA_FLAGS="${HELM_EXTRA_FLAGS:-}"     # 예: --force-conflicts
EXPECT_PREV_IMAGE="${EXPECT_PREV_IMAGE:-}"   # 설정 시 첫 non-Idle 의 previousImage 를 이 값과 대조

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SELF_DIR/../../.." && pwd)"
export CHART_DIR="${CHART_DIR:-$REPO_ROOT/deploy/helm}"
export IMAGE_TAG="${IMAGE_TAG:-v0.7.53}"
export OUT="${OUT:-/tmp/tt-kmd-precompiled-evidence}"
mkdir -p "$OUT"
# 기준선(원복 대상) 기본값은 preflight 가 저장하는 $OUT/values-baseline.yaml 이다.
export VALUES_BASE="${VALUES_BASE:-$OUT/values-baseline.yaml}"

PHASE_NAME="init"

# ── 헬퍼 ────────────────────────────────────────────────────────────────
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$OUT/run.log" >&2; }
kc()  { kubectl --context "$KUBECTL_CONTEXT" "$@"; }
helmx() { helm --kube-context "$KUBECTL_CONTEXT" "$@"; }
dus_f() { kc get dus "$DUS" -o jsonpath="{.status.$1}" 2>/dev/null || true; }

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

# values 오버레이 생성(PyYAML). $1=version $2=tag $3=outfile
gen_overlay() {
  local version="$1" tag="$2" outfile="$3"
  local precompiled="$REGISTRY/tenstorrent-driver-ds:${version}-k${KERNEL}-${PRECOMPILED_SUFFIX}"
  V="$version" T="$tag" K="$KERNEL" P="$precompiled" python3 - "$outfile" <<'PY'
import os, sys, yaml
overlay = {
    "driverInstallPolicies": {
        "tenstorrent": {
            "driver": {
                "version": os.environ["V"],
                "tag": os.environ["T"],
                "allowDowngrade": True,
                "kernelImages": {os.environ["K"]: os.environ["P"]},
            },
            "upgradePolicy": {"autoUpgrade": True},
        }
    }
}
with open(sys.argv[1], "w") as f:
    yaml.safe_dump(overlay, f, default_flow_style=False, sort_keys=False, allow_unicode=True)
PY
  echo "$precompiled"
}

# NODE·tenstorrent install Job(최신) 정보: name\timage\tstart\tcompletion\tdurationSec
job_info() {
  local jf="$OUT/.jobs.json"
  kc -n "$NAMESPACE" get jobs -l "npu.ai/vendor=tenstorrent,npu.ai/node=$NODE" -o json > "$jf" 2>/dev/null || echo '{"items":[]}' > "$jf"
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

# ── preflight(전량 읽기 전용) ───────────────────────────────────────────
phase_preflight() {
  PHASE_NAME="preflight"
  log "preflight: context=$KUBECTL_CONTEXT node=$NODE dus=$DUS"
  log "OUT=$OUT VALUES_BASE=$VALUES_BASE"

  kc version >/dev/null 2>&1 || blocked "context $KUBECTL_CONTEXT 도달 불가"

  local ready
  ready=$(kc get nodes -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(sum(1 for n in d["items"] for c in n["status"]["conditions"] if c["type"]=="Ready" and c["status"]=="True"))')
  [ "$ready" = "$EXPECT_READY" ] || blocked "Ready 노드 $ready (기대 $EXPECT_READY)"

  local st cur
  st=$(dus_f state); cur=$(dus_f currentVersion)
  if [ "$st" != "Idle" ] || [ "$cur" != "$BASE_VERSION" ]; then blocked "DUS $st/$cur (기대 Idle/$BASE_VERSION)"; fi

  # helm 리비전·배포 이미지 태그 기록(값으로 실패시키지 않음).
  local rev depimg
  rev=$(helmx -n "$NAMESPACE" list -o json | python3 -c 'import json,os,sys;d=json.load(sys.stdin);print(next((x["revision"] for x in d if x["name"]==os.environ["RELEASE"]),"?"))')
  depimg=$(kc -n "$NAMESPACE" get deploy "$RELEASE" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)
  log "helm release rev=$rev deployImage=$depimg"

  # NODE 에서 tenstorrent 자원 요청 파드 0.
  local ttp
  ttp=$(kc get pods -A -o json | python3 -c 'import json,os,sys;n=os.environ["NODE"];d=json.load(sys.stdin);print(sum(1 for p in d["items"] if p["spec"].get("nodeName")==n for c in p["spec"]["containers"] for k in (c.get("resources",{}).get("requests",{}) or {}) if "tenstorrent" in k))')
  [ "$ttp" = "0" ] || blocked "$NODE 에 tenstorrent 자원 요청 파드 $ttp 개"

  # 사전 컴파일 이미지 두 개가 레지스트리에 있는지(HTTP Harbor → --insecure 필수).
  local ref
  for v in "$BASE_VERSION" 2.8.0; do
    ref="$REGISTRY/tenstorrent-driver-ds:${v}-k${KERNEL}-${PRECOMPILED_SUFFIX}"
    if command -v skopeo >/dev/null 2>&1; then
      skopeo inspect --tls-verify=false "docker://$ref" >/dev/null 2>&1 || blocked "이미지 없음: $ref"
    else
      sudo -n docker manifest inspect --insecure "$ref" >/dev/null 2>&1 || blocked "이미지 없음(또는 sudo docker 불가): $ref"
    fi
  done

  # 노드 커널·드라이버·Secure Boot 상태.
  local sshout uname_r sysver sb
  sshout=$(ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 "$SSH_HOST" 'uname -r; cat /sys/module/tenstorrent/version 2>/dev/null; mokutil --sb-state 2>&1' 2>/dev/null || true)
  uname_r=$(printf '%s\n' "$sshout" | sed -n '1p')
  sysver=$(printf '%s\n' "$sshout" | sed -n '2p')
  sb=$(printf '%s\n' "$sshout" | sed -n '3,$p' | tr '\n' ' ')
  log "node kernel=$uname_r sysDriver=$sysver secureBoot=[$sb]"
  [ "$uname_r" = "$KERNEL" ] || blocked "노드 커널 $uname_r != kernelImages 키 $KERNEL"
  if printf '%s' "$sb" | grep -qi 'SecureBoot enabled'; then blocked "Secure Boot enabled — HARDWARE(왕복 불가)"; fi

  {
    echo "context=$KUBECTL_CONTEXT ready=$ready dus=$st/$cur helmRev=$rev deployImage=$depimg"
    echo "ttPodsOnNode=$ttp kernel=$uname_r sysDriver=$sysver secureBoot=$sb"
  } > "$OUT/preflight.txt"
  kc get dus "$DUS" -o yaml > "$OUT/dus-before.yaml" 2>/dev/null || true
  kc get dip "$DIP" -o yaml > "$OUT/dip-before.yaml" 2>/dev/null || true

  # 배포 전 사용자 helm values 를 기준선으로 저장한다 — restore 가 이 파일(VALUES_BASE 기본값)로
  # 원복한다. 전역 제약(배포 전 helm get values 저장)을 하네스가 소유한다. 빈 파일이면 restore 의
  # --reset-values 가 사용자 값을 덮어쓰므로 즉시 BLOCKED.
  helmx -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$OUT/values-baseline.yaml" 2>/dev/null || true
  [ -s "$OUT/values-baseline.yaml" ] || blocked "기준선 helm values 저장 실패(빈 파일): $OUT/values-baseline.yaml"
  log "기준선 helm values 저장: $OUT/values-baseline.yaml (restore 기준)"

  log "preflight OK"
  emit_json PASS "ready=$ready" "dus=$st/$cur" "helmRev=$rev" "deployImage=$depimg" \
    "kernel=$uname_r" "sysDriver=$sysver" "secureBoot=$sb"
}

# ── transition <version> <tag> ──────────────────────────────────────────
phase_transition() {
  PHASE_NAME="transition-${1:-?}"
  local version="${1:?version 인자 필요 (예: 2.8.0)}" tag="${2:?tag 인자 필요 (예: 2.8.0-v1)}"
  local overlay="$OUT/overlay-${version}.yaml" precompiled
  precompiled=$(gen_overlay "$version" "$tag" "$overlay")
  log "transition → $version (tag=$tag, resolvedImage 기대=$precompiled)"
  log "생성 오버레이 $overlay:"; sed 's/^/    /' "$overlay" | tee -a "$OUT/run.log" >&2

  # shellcheck disable=SC2086
  run_or_print helmx upgrade "$RELEASE" "$CHART_DIR" -n "$NAMESPACE" \
    --reset-then-reuse-values -f "$overlay" --wait --timeout "$HELM_TIMEOUT" $HELM_EXTRA_FLAGS

  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: 전이 관찰 생략"
    emit_json DRY "version=$version" "tag=$tag" "overlay=$overlay" "resolvedImageExpected=$precompiled"
    return 0
  fi

  # DUS 전이 폴링. 첫 non-Idle 에서 previousImage 캡처, Idle/<version> 에서 종료.
  local tlog="$OUT/transitions-${version}.log"; : > "$tlog"
  local t0 state cur pimg prev_seen=0 rimg
  t0=$(date +%s)
  while :; do
    state=$(dus_f state); cur=$(dus_f currentVersion); rimg=$(dus_f resolvedImage); pimg=$(dus_f previousImage)
    printf '%s state=%s current=%s resolvedImage=%s previousImage=%s msg=%s\n' \
      "$(date -u +%FT%TZ)" "$state" "$cur" "$rimg" "$pimg" "$(dus_f message)" >> "$tlog"
    if [ "$prev_seen" = 0 ] && [ "$state" != "Idle" ] && [ -n "$pimg" ]; then
      prev_seen=1
      log "previousImage=$pimg"
      if [ -n "$EXPECT_PREV_IMAGE" ] && [ "$pimg" != "$EXPECT_PREV_IMAGE" ]; then
        kc get dus "$DUS" -o yaml > "$OUT/dus-fail-${version}.yaml" 2>/dev/null || true
        fail "previousImage=$pimg (기대 $EXPECT_PREV_IMAGE)"
      fi
    fi
    [ "$state" = "Idle" ] && [ "$cur" = "$version" ] && break
    case "$state" in
      Failed|UnverifiedVersion)
        kc get dus "$DUS" -o yaml > "$OUT/dus-fail-${version}.yaml" 2>/dev/null || true
        fail "터미널 $state: $(dus_f message)" ;;
    esac
    [ $(( $(date +%s) - t0 )) -lt "$T_TRANSITION" ] || {
      kc get dus "$DUS" -o yaml > "$OUT/dus-fail-${version}.yaml" 2>/dev/null || true
      fail "타임아웃 ${T_TRANSITION}s (state=$state current=$cur)"; }
    sleep 10
  done
  log "DUS Idle/$version 수렴"

  # resolvedImage == 사전 컴파일 태그.
  rimg=$(dus_f resolvedImage)
  [ "$rimg" = "$precompiled" ] || fail "resolvedImage=$rimg (기대 $precompiled)"

  # install Job(최신) 이미지·소요 시간.
  local jn ji jst jct jdur
  IFS=$'\t' read -r jn ji jst jct jdur < <(job_info)
  if [ "$jn" = "NONE" ] || [ -z "$jn" ]; then fail "install Job 미발견(labels npu.ai/vendor=tenstorrent,npu.ai/node=$NODE)"; fi
  kc -n "$NAMESPACE" get job "$jn" -o yaml > "$OUT/job-${version}.yaml" 2>/dev/null || true
  kc -n "$NAMESPACE" logs "job/$jn" --tail=-1 > "$OUT/job-${version}.log" 2>/dev/null || true
  [ "$ji" = "$precompiled" ] || fail "Job 이미지=$ji (기대 $precompiled)"
  log "install Job=$jn image=$ji durationSec=$jdur (start=$jst completion=$jct)"

  # 사전 컴파일 설치 로그 줄 확인(dkms skipped).
  local prebuilt_line
  prebuilt_line=$(grep -F "prebuilt module for $KERNEL" "$OUT/job-${version}.log" | head -1 || true)
  [ -n "$prebuilt_line" ] || fail "Job 로그에 'prebuilt module for $KERNEL' 줄 없음 (DKMS 로 빌드된 정황)"
  printf '%s' "$prebuilt_line" | grep -qi 'dkms skipped' || fail "prebuilt 줄에 'dkms skipped' 없음: $prebuilt_line"
  log "prebuilt 로그: $prebuilt_line"

  # NDR driverVersion == version.
  local ndrv
  ndrv=$(kc get ndr "$NODE" -o jsonpath='{.status.devices[?(@.vendor=="tenstorrent")].driverVersion}' 2>/dev/null || true)
  [ "$ndrv" = "$version" ] || fail "NDR driverVersion=$ndrv (기대 $version)"

  # /sys/module/tenstorrent/version == version.
  local sysver
  sysver=$(ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 "$SSH_HOST" 'cat /sys/module/tenstorrent/version 2>/dev/null' 2>/dev/null | tr -d '[:space:]' || true)
  [ "$sysver" = "$version" ] || fail "/sys/module/tenstorrent/version=$sysver (기대 $version)"

  # 장치 재광고 대기 후 tt-smi probe(비대화형, exit 0).
  kc wait --for=jsonpath='{.status.allocatable.tenstorrent\.com/blackhole}'=1 node/"$NODE" --timeout=300s >/dev/null 2>&1 \
    || log "warn: allocatable tenstorrent.com/blackhole=1 대기 실패(probe 로 재확인)"
  tt_smi_probe "$version" || fail "tt-smi probe 실패"

  kc get ndr "$NODE" -o yaml > "$OUT/ndr-${version}.yaml" 2>/dev/null || true
  log "transition → $version PASS"
  emit_json PASS "version=$version" "resolvedImage=$rimg" "jobImage=$ji" "jobName=$jn" \
    "jobDurationSec=$jdur" "prebuiltLog=$prebuilt_line" "previousImage=$pimg" \
    "ndrDriverVersion=$ndrv" "sysDriverVersion=$sysver"
}

# tt-smi probe 파드: 생성 → Succeeded 대기 → 로그 → 삭제. $1=version(로그 태그)
tt_smi_probe() {
  local version="$1" pod="tt-smi-check"
  run_or_print kc -n "$NAMESPACE" delete pod "$pod" --ignore-not-found >/dev/null 2>&1 || true
  run_or_print kc -n "$NAMESPACE" run "$pod" --restart=Never --image="$PROBE_IMAGE" \
    --overrides='{"spec":{"nodeName":"'"$NODE"'","containers":[{"name":"p","image":"'"$PROBE_IMAGE"'","command":["tt-smi","-s"],"resources":{"limits":{"tenstorrent.com/blackhole":"1"}}}]}}' \
    >/dev/null 2>&1 || { log "probe 파드 생성 실패"; return 1; }
  if kc -n "$NAMESPACE" wait --for=jsonpath='{.status.phase}'=Succeeded pod/"$pod" --timeout=180s >/dev/null 2>&1; then
    kc -n "$NAMESPACE" logs "$pod" > "$OUT/tt-smi-${version}.log" 2>&1 || true
    run_or_print kc -n "$NAMESPACE" delete pod "$pod" --ignore-not-found >/dev/null 2>&1 || true
    log "tt-smi probe Succeeded (exit 0), 로그 $OUT/tt-smi-${version}.log"
    return 0
  fi
  kc -n "$NAMESPACE" logs "$pod" > "$OUT/tt-smi-${version}.log" 2>&1 || true
  kc -n "$NAMESPACE" describe pod "$pod" >> "$OUT/tt-smi-${version}.log" 2>&1 || true
  run_or_print kc -n "$NAMESPACE" delete pod "$pod" --ignore-not-found >/dev/null 2>&1 || true
  return 1
}

# ── restore(기준선 원복 + 검증) ─────────────────────────────────────────
phase_restore() {
  PHASE_NAME="restore"
  [ -f "$VALUES_BASE" ] || blocked "VALUES_BASE 파일 없음: $VALUES_BASE"
  log "restore: --reset-values -f $VALUES_BASE --set image.tag=$IMAGE_TAG"

  # shellcheck disable=SC2086
  run_or_print helmx upgrade "$RELEASE" "$CHART_DIR" -n "$NAMESPACE" \
    --reset-values -f "$VALUES_BASE" --set "image.tag=$IMAGE_TAG" --wait --timeout "$HELM_TIMEOUT" $HELM_EXTRA_FLAGS

  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: 기준선 검증 생략"
    emit_json DRY "valuesBase=$VALUES_BASE" "imageTag=$IMAGE_TAG"
    return 0
  fi

  # DIP 기본값 복귀.
  local img ki ad
  img=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.image}' 2>/dev/null || true)
  ki=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.kernelImages}' 2>/dev/null || true)
  ad=$(kc get dip "$DIP" -o jsonpath='{.spec.driver.allowDowngrade}' 2>/dev/null || true)
  [ "$img" = "$REGISTRY/tenstorrent-driver-ds:$BASE_TAG" ] || fail "DIP image=$img (기대 $BASE_TAG)"
  [ -z "$ki" ] || fail "DIP kernelImages 잔존: $ki"
  [ "$ad" = "false" ] || [ -z "$ad" ] || fail "DIP allowDowngrade=$ad (기대 false)"

  # DUS Idle/BASE_VERSION.
  local st cur
  st=$(dus_f state); cur=$(dus_f currentVersion)
  if [ "$st" != "Idle" ] || [ "$cur" != "$BASE_VERSION" ]; then fail "DUS $st/$cur (기대 Idle/$BASE_VERSION)"; fi

  # helm get values 에 tenstorrent·metrics 오버레이 없음.
  helmx -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$OUT/values-after.yaml" 2>/dev/null || true
  python3 - "$OUT/values-after.yaml" <<'PY' || fail "helm get values 에 tenstorrent/metrics 오버레이 잔존"
import sys, yaml
d = yaml.safe_load(open(sys.argv[1])) or {}
dip = (d.get("driverInstallPolicies") or {})
bad = []
if "tenstorrent" in dip:
    bad.append("driverInstallPolicies.tenstorrent")
if "metrics" in d:
    bad.append("metrics")
if bad:
    print("잔존:", bad); sys.exit(1)
PY

  # 5 Ready, 비정상 파드 0.
  local ready abn
  ready=$(kc get nodes -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(sum(1 for n in d["items"] for c in n["status"]["conditions"] if c["type"]=="Ready" and c["status"]=="True"))')
  [ "$ready" = "$EXPECT_READY" ] || fail "Ready 노드 $ready (기대 $EXPECT_READY)"
  abn=$(kc -n "$NAMESPACE" get pods -o json | python3 -c 'import json,sys;d=json.load(sys.stdin);print(",".join(p["metadata"]["name"] for p in d["items"] if p["status"].get("phase") not in ("Running","Succeeded")))')
  [ -z "$abn" ] || fail "kcloud 네임스페이스 비정상 파드: $abn"

  # 테스트 probe 파드 잔존 0.
  if kc -n "$NAMESPACE" get pod tt-smi-check >/dev/null 2>&1; then fail "테스트 파드 tt-smi-check 잔존"; fi

  log "restore OK: DIP $BASE_TAG, DUS Idle/$BASE_VERSION, values clean, $ready Ready"
  emit_json PASS "dipImage=$img" "dus=$st/$cur" "ready=$ready"
}

# ── status(복구용 일괄 덤프) ────────────────────────────────────────────
phase_status() {
  PHASE_NAME="status"
  log "status dump → $OUT"
  kc get dus "$DUS" -o yaml       | tee "$OUT/status-dus.yaml"  >/dev/null 2>&1 || true
  kc get dip "$DIP" -o yaml       > "$OUT/status-dip.yaml"      2>/dev/null || true
  kc get ndr "$NODE" -o yaml      > "$OUT/status-ndr.yaml"      2>/dev/null || true
  kc -n "$NAMESPACE" get jobs -l "npu.ai/vendor=tenstorrent,npu.ai/node=$NODE" -o yaml > "$OUT/status-jobs.yaml" 2>/dev/null || true
  local st cur pimg rimg
  st=$(dus_f state); cur=$(dus_f currentVersion); pimg=$(dus_f previousImage); rimg=$(dus_f resolvedImage)
  log "DUS=$st/$cur previousImage=$pimg resolvedImage=$rimg"
  emit_json INFO "dus=$st/$cur" "previousImage=$pimg" "resolvedImage=$rimg"
}

# ── selftest(오프라인: 오버레이·helm 템플릿 렌더 검증) ───────────────────
phase_selftest() {
  PHASE_NAME="selftest"
  local tmp="$OUT/selftest"; mkdir -p "$tmp"
  local ov="$tmp/overlay-2.8.0.yaml" pc
  pc=$(gen_overlay 2.8.0 2.8.0-v1 "$ov")
  grep -q "kernelImages:" "$ov" || fail "오버레이에 kernelImages 없음"
  grep -q "$pc" "$ov"           || fail "오버레이에 사전 컴파일 ref 없음: $pc"
  grep -q "autoUpgrade: true" "$ov" || fail "오버레이에 autoUpgrade:true 없음"
  grep -q "allowDowngrade: true" "$ov" || fail "오버레이에 allowDowngrade:true 없음"
  # helm 템플릿이 tenstorrent DIP 에 kernelImages·allowDowngrade·autoUpgrade 를 렌더하는지.
  local rendered="$tmp/dip.yaml"
  helm template "$RELEASE" "$CHART_DIR" \
    --set "global.registry=$REGISTRY" -f "$ov" \
    --show-only templates/driverinstallpolicy.yaml > "$rendered" 2>/dev/null \
    || fail "helm template 실패"
  python3 - "$rendered" "$pc" <<'PY' || fail "helm template 에 tenstorrent DIP 렌더 미검증"
import sys, yaml
docs = [d for d in yaml.safe_load_all(open(sys.argv[1])) if d]
tt = next((d for d in docs if d.get("spec", {}).get("vendor") == "tenstorrent"), None)
assert tt is not None, "tenstorrent DIP 미렌더"
drv = tt["spec"]["driver"]
assert drv.get("version") == "2.8.0", drv.get("version")
assert drv.get("allowDowngrade") is True, drv.get("allowDowngrade")
ki = drv.get("kernelImages") or {}
assert sys.argv[2] in ki.values(), ki
assert tt["spec"]["upgradePolicy"].get("autoUpgrade") is True, tt["spec"]["upgradePolicy"]
print("selftest: tenstorrent DIP renders kernelImages/allowDowngrade/autoUpgrade OK")
PY
  log "selftest OK"
  emit_json PASS "renderedDip=$rendered"
}

# ── dispatch ────────────────────────────────────────────────────────────
main() {
  local phase="${1:-${PHASE:-}}"
  case "$phase" in
    preflight)  phase_preflight ;;
    transition) shift || true; phase_transition "${1:-}" "${2:-}" ;;
    restore)    phase_restore ;;
    status)     phase_status ;;
    selftest)   phase_selftest ;;
    *)
      cat >&2 <<USAGE
usage: $0 <phase> [args]
  preflight                     전제 검사(읽기 전용)
  transition <version> <tag>    예: transition 2.8.0 2.8.0-v1
  restore                       사용자 값을 기준선으로 원복 + 검증
  status                        DUS/DIP/NDR/Job 덤프(복구용)
  selftest                      오프라인 오버레이·helm 템플릿 검증

주요 환경변수: KUBECTL_CONTEXT CHART_DIR IMAGE_TAG VALUES_BASE OUT
              DRY_RUN(=1 변경 명령 출력만) T_TRANSITION HELM_TIMEOUT
              HELM_EXTRA_FLAGS EXPECT_PREV_IMAGE
USAGE
      exit 64 ;;
  esac
}
main "$@"
