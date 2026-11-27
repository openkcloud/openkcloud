#!/usr/bin/env bash
# ============================================================
# run.sh: <tenstorrent-node> tt-kmd 2.8.0 → 2.11.0 라이브 업그레이드(DriverInstallPolicy 경로) 실행·관측
# 상세: values 네 값(tag·version·verifiedVersions·autoUpgrade)을 바꿔 helm upgrade 로 전이를
#       시작한다. 성공하면 autoUpgrade 만 되돌리고, 실패하면 README 의 복구 절차를 따른다.
# 생성일: 2026-09-21 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

OUT="${OUT:-/tmp/tt-kmd-upgrade-evidence}"
DRY_RUN="${DRY_RUN:-0}"
NODE="${NODE:-k8s-worker3}"
DUS="${DUS:-k8s-worker3-tenstorrent}"
DIP="${DIP:-tenstorrent-blackhole-ds}"
NAMESPACE="${NAMESPACE:-kcloud}"
RELEASE="${RELEASE:-kcloud-operator}"
OLD_VERSION="${OLD_VERSION:-2.8.0}"; OLD_TAG="${OLD_TAG:-2.8.0-v1}"
NEW_VERSION="${NEW_VERSION:-2.11.0}"; NEW_TAG="${NEW_TAG:-2.11.0-v1}"
REGISTRY="${REGISTRY:?REGISTRY 을 지정하십시오}"
PROBE_IMAGE="${PROBE_IMAGE:?PROBE_IMAGE 을 지정하십시오}"
T_CONVERGE="${T_CONVERGE:-1500}"   # validationTimeout 10m + Job 빌드 여유
CONTEXT_WANT="kubernetes-admin@cluster.local"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"
mkdir -p "$OUT"
DEMO_DONE=0; VERDICT="FAIL"; REASON=""
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" | tee -a "$OUT/run.log" >&2; }
run() { if [ "$DRY_RUN" = "1" ]; then log "DRY: $*"; else log "+ $*"; "$@"; fi; }
dus_field() { kubectl get dus "$DUS" -o jsonpath="{.status.$1}" 2>/dev/null || true; }

helm_set() {  # $1=autoUpgrade $2=tag $3=version
  run helm upgrade "$RELEASE" deploy/helm -n "$NAMESPACE" --reset-then-reuse-values --force-conflicts \
    --set "driverInstallPolicies.tenstorrent.driver.tag=$2" \
    --set "driverInstallPolicies.tenstorrent.driver.version=$3" \
    --set "driverInstallPolicies.tenstorrent.verifiedVersions={$OLD_VERSION,$NEW_VERSION}" \
    --set "driverInstallPolicies.tenstorrent.upgradePolicy.autoUpgrade=$1"
}

preflight() {
  [ "$(kubectl config current-context)" = "$CONTEXT_WANT" ] || { REASON="context $(kubectl config current-context)"; VERDICT="BLOCKED"; return 1; }
  [ "$(dus_field state)" = "Idle" ] && [ "$(dus_field currentVersion)" = "$OLD_VERSION" ] || { REASON="DUS not Idle/$OLD_VERSION: $(dus_field state)/$(dus_field currentVersion)"; VERDICT="BLOCKED"; return 1; }
  [ "$(kubectl get dip "$DIP" -o jsonpath='{.spec.driver.imageVersionBaked}')" = "true" ] || { REASON="DIP imageVersionBaked != true"; VERDICT="BLOCKED"; return 1; }
  local tt_pods; tt_pods=$(kubectl get pods -A -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for p in d["items"] for c in p["spec"]["containers"] for k in (c.get("resources",{}).get("requests",{}) or {}) if "tenstorrent" in k))')
  [ "$tt_pods" = "0" ] || { REASON="tenstorrent 자원 요청 파드 $tt_pods 개"; VERDICT="BLOCKED"; return 1; }
  # HTTP Harbor 이므로 manifest inspect 에 --insecure 필수(없으면 no such manifest 오탐).
  for tag in "$NEW_TAG" "$OLD_TAG"; do
    sudo docker manifest inspect --insecure "$REGISTRY/tenstorrent-driver-ds:$tag" >/dev/null 2>&1 || { REASON="image $tag missing in Harbor"; VERDICT="BLOCKED"; return 1; }
  done
  kubectl get node "$NODE" -o jsonpath='{.status.nodeInfo.kernelVersion}' | grep -Eq '^(5\.15|6\.8)\.' || { REASON="kernel outside allowlist"; VERDICT="BLOCKED"; return 1; }
  helm -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$OUT/live-values-before.yaml"
  kubectl get dus "$DUS" -o yaml > "$OUT/dus-before.yaml"
  kubectl get ndr "$NODE" -o yaml > "$OUT/ndr-before.yaml"
  log "preflight ok: DUS Idle/$OLD_VERSION, images $NEW_TAG/$OLD_TAG present"
}

demo() {
  helm_set true "$NEW_TAG" "$NEW_VERSION"
  [ "$DRY_RUN" = "1" ] && { DEMO_DONE=1; VERDICT="DRY"; return 0; }
  local t0 prev_seen=0 state cur msg pimg
  t0=$(date +%s)
  while :; do
    state=$(dus_field state); cur=$(dus_field currentVersion); msg=$(dus_field message)
    printf '%s state=%s current=%s prevImage=%s msg=%s\n' "$(date -u +%FT%TZ)" "$state" "$cur" "$(dus_field previousImage)" "$msg" >> "$OUT/transitions.log"
    # 첫 non-Idle 상태에서 previousImage 를 확인한다. 다만 상태 전이와 previousImage 기록이
    # 같은 reconcile 라도 폴링 타이밍상 아직 비어 있을 수 있으므로, 값이 채워진 뒤에만 판정한다.
    if [ "$prev_seen" = 0 ] && [ "$state" != "Idle" ]; then
      pimg=$(dus_field previousImage)
      if [ -n "$pimg" ]; then
        prev_seen=1
        [ "$pimg" = "$REGISTRY/tenstorrent-driver-ds:$OLD_TAG" ] || { REASON="previousImage=$pimg (want $OLD_TAG)"; return 1; }
        log "previousImage captured as $OLD_TAG (rollback target verified)"
      fi
    fi
    if [ "$state" = "Idle" ] && [ "$cur" = "$NEW_VERSION" ]; then break; fi
    case "$state" in Failed|UnverifiedVersion) REASON="terminal $state: $msg"; return 1;; esac
    [ $(( $(date +%s) - t0 )) -lt "$T_CONVERGE" ] || { REASON="timeout ${T_CONVERGE}s in state $state"; return 1; }
    sleep 10
  done
  kubectl -n "$NAMESPACE" get jobs -o yaml > "$OUT/install-jobs.yaml" || true
  kubectl -n "$NAMESPACE" logs -l app.kubernetes.io/component=driver-install --tail=-1 > "$OUT/install-job.log" 2>/dev/null || true
  [ "$(kubectl get node "$NODE" -o jsonpath='{.metadata.labels.kcloud\.ai/driver-tenstorrent\.version}')" = "$NEW_VERSION" ] || { REASON="node label version != $NEW_VERSION"; return 1; }
  [ "$(kubectl get ndr "$NODE" -o jsonpath='{.status.devices[?(@.vendor=="tenstorrent")].driverVersion}')" = "$NEW_VERSION" ] || { REASON="NDR driverVersion != $NEW_VERSION"; return 1; }
  kubectl wait --for=jsonpath='{.status.allocatable.tenstorrent\.com/blackhole}'=1 node/"$NODE" --timeout=300s || { REASON="allocatable not restored"; return 1; }
  # tt-smi probe: 비대화형(파드 생성 → Succeeded 대기 → 로그 → 삭제). detector 가 읽는
  # /sys/module/tenstorrent/version 을 파드 관점에서 교차 확인한다. exit 0(Succeeded)+로그가 산출물.
  local probe_pod="tt-smi-check"
  kubectl -n "$NAMESPACE" delete pod "$probe_pod" --ignore-not-found >/dev/null 2>&1 || true
  kubectl -n "$NAMESPACE" run "$probe_pod" --restart=Never --image="$PROBE_IMAGE" \
    --overrides='{"spec":{"nodeName":"'"$NODE"'","containers":[{"name":"p","image":"'"$PROBE_IMAGE"'","command":["tt-smi","-s"],"resources":{"limits":{"tenstorrent.com/blackhole":"1"}}}]}}' \
    >/dev/null 2>&1 || { REASON="tt-smi probe 파드 생성 실패"; return 1; }
  if kubectl -n "$NAMESPACE" wait --for=jsonpath='{.status.phase}'=Succeeded pod/"$probe_pod" --timeout=180s >/dev/null 2>&1; then
    kubectl -n "$NAMESPACE" logs "$probe_pod" > "$OUT/tt-smi.log" 2>&1 || true
    kubectl -n "$NAMESPACE" delete pod "$probe_pod" --ignore-not-found >/dev/null 2>&1 || true
  else
    kubectl -n "$NAMESPACE" logs "$probe_pod" > "$OUT/tt-smi.log" 2>&1 || true
    kubectl -n "$NAMESPACE" describe pod "$probe_pod" >> "$OUT/tt-smi.log" 2>&1 || true
    kubectl -n "$NAMESPACE" delete pod "$probe_pod" --ignore-not-found >/dev/null 2>&1 || true
    REASON="tt-smi probe 미완료(Succeeded 아님)"; return 1
  fi
  DEMO_DONE=1; VERDICT="PASS"
}

cleanup() {
  if [ "$DRY_RUN" != "1" ]; then
    if [ "$VERDICT" = "PASS" ]; then
      helm_set false "$NEW_TAG" "$NEW_VERSION" || log "warn: autoUpgrade 복원 실패 — 수동 확인"
    else
      log "FAIL/BLOCKED — 자동 롤백 관측(README 복구 절차). DUS: $(dus_field state)/$(dus_field currentVersion) msg=$(dus_field message)"
      kubectl get dus "$DUS" -o yaml > "$OUT/dus-after.yaml" 2>/dev/null || true
    fi
    kubectl get ndr "$NODE" -o yaml > "$OUT/ndr-after.yaml" 2>/dev/null || true
  fi
  [ "$DEMO_DONE" = 1 ] || { [ "$VERDICT" = "DRY" ] || VERDICT="${VERDICT:-FAIL}"; }
  printf '{"verdict":"%s","node":"%s","from":"%s","to":"%s","reason":"%s","out":"%s"}\n' "$VERDICT" "$NODE" "$OLD_VERSION" "$NEW_VERSION" "${REASON//\"/\'}" "$OUT"
  case "$VERDICT" in PASS|DRY) exit 0;; *) exit 1;; esac
}
trap cleanup EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight && demo
