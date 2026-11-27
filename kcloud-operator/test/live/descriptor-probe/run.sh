#!/usr/bin/env bash
# ============================================================
# run.sh: tenstorrent descriptor evidence allocation probe 라이브 점검(비파괴)
# 상세: 클러스터에 있는 tenstorrent AcceleratorDescriptor 에 AllocationProbe 를 켠
#       AcceleratorVerificationPolicy(AVP)를 적용해 재조정시키고, probe Pod(tt-smi -s)가
#       뜨고 AllocationProbe 검사가 exit=0 으로 통과하는지 본다. 새로 만드는 오브젝트는
#       AVP 하나뿐이다(비파괴). DRY_RUN=1 이면 명령만 출력, 세부는 cleanup() 등 참고.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 시나리오 성격(기록용).
SCENARIO_MINUTES=4
SCENARIO_DESTRUCTIVE="no"

DRY_RUN="${DRY_RUN:-0}"

NAMESPACE="${NAMESPACE:-kcloud}"   # operator Deployment + probe Pod 네임스페이스
DEPLOY="${DEPLOY:-kcloud-operator}"
RELEASE="${RELEASE:-kcloud-operator}"
HELM_CHART="${HELM_CHART:-deploy/helm}"   # repo 루트 기준 상대경로 — 저장소 루트에서 실행 전제

NODE="${NODE:-k8s-worker3}"
VENDOR="${VENDOR:-tenstorrent}"
RESOURCE="${RESOURCE:-tenstorrent/blackhole-p150}"
# DESC_NAME 은 라이브 클러스터에 이미 존재해야 하는 tenstorrent descriptor 다(이 스크립트는
# descriptor 를 새로 만들지 않는다 — 새 벤더 데모는 test/live/new-vendor-by-descriptor 쪽 몫이다).
DESC_NAME="${DESC_NAME:-new-vendor-tt-blackhole}"

PROBE_IMAGE="${PROBE_IMAGE:?PROBE_IMAGE 을 지정하십시오}"
# ORIG_PROBE_IMAGE 는 정리 단계에서 되돌릴 값이다. 문서화된 baseline 은 빈 문자열(probeImages
# 미설정) 이다 — helm values 를 직접 파싱하지 않는 이유는 이 값이 이미 알려진 상수이기 때문이다.
ORIG_PROBE_IMAGE="${ORIG_PROBE_IMAGE:-}"

AVP_FILE="${AVP_FILE:-$SCRIPT_DIR/avp-allocation-probe-worker3.yaml}"

LEFTOVER_NS="${LEFTOVER_NS:-default}"
LEFTOVER_POD="${LEFTOVER_POD:-tt-pod}"

# 대기 상한(초)·간격.
T_ROLLOUT="${T_ROLLOUT:-180}"
T_AEV="${T_AEV:-180}"
IV="${IV:-5}"

OUT="${OUT:-/tmp/descriptor-probe-evidence}"
mkdir -p "$OUT"

# 이름은 매니페스트에서 읽어 스크립트와 파일이 어긋나지 않게 한다.
name_from() { sed -nE 's/^[[:space:]]*name:[[:space:]]*([^[:space:]]+).*/\1/p' "$1" | head -1; }
AVP_NAME="${AVP_NAME:-$(name_from "$AVP_FILE")}"

# 상태 변수.
DEMO_DONE=false
BLOCKED=false
CLEANED=false
AVP_APPLIED=false
PROBE_IMAGE_SET=false
ANNOTATED=false
CHECK_JSON=""
FAILS=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# kc: 상태를 바꾸는 kubectl 래퍼. DRY_RUN 이면 명령만 찍고 실행하지 않는다.
kc() {
  if [ "$DRY_RUN" = "1" ]; then
    printf '+ kubectl %s\n' "$*" >&2
    return 0
  fi
  kubectl "$@"
}

# kubeget: 읽기 전용 kubectl 래퍼. DRY_RUN 이면 클러스터에 접근하지 않고 빈 값을 낸다.
kubeget() {
  if [ "$DRY_RUN" = "1" ]; then return 0; fi
  kubectl "$@"
}

# helmrun: 상태를 바꾸는 helm 래퍼. DRY_RUN 이면 명령만 찍는다.
helmrun() {
  if [ "$DRY_RUN" = "1" ]; then
    printf '+ helm %s\n' "$*" >&2
    return 0
  fi
  helm "$@"
}

# record NAME true|false [detail] — 검사 결과 집계(중단하지 않음).
record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

# poll TIMEOUT INTERVAL predicate... — predicate 성공까지 최대 TIMEOUT 초 대기.
poll() {
  local t="$1" iv="$2"; shift 2
  if [ "$DRY_RUN" = "1" ]; then printf '+ (poll ≤%ss) %s\n' "$t" "$*" >&2; return 0; fi
  local e=0
  while [ "$e" -lt "$t" ]; do
    if "$@"; then return 0; fi
    sleep "$iv"; e=$((e + iv))
  done
  return 1
}

desc_present() { kubeget get acceleratordescriptor "$DESC_NAME" >/dev/null 2>&1; }
desc_resolved_for_node() {
  kubeget get acceleratordescriptor "$DESC_NAME" -o yaml 2>/dev/null | grep -q "node: $NODE"
}
deploy_present() { kubeget -n "$NAMESPACE" get deploy "$DEPLOY" >/dev/null 2>&1; }

# aev_check_line — AcceleratorEvidence(node) 의 AllocationProbe 체크 한 줄(name=passed=message).
aev_check_line() {
  kubeget get aev "$NODE" -o jsonpath='{range .status.checks[*]}{.name}={.passed}={.message}{"\n"}{end}' 2>/dev/null \
    | grep '^AllocationProbe=' || true
}
allocation_probe_passed() {
  local line; line="$(aev_check_line)"
  [ -n "$line" ] && printf '%s' "$line" | grep -qE '^AllocationProbe=true=(probe result reused, no pod scheduled this pass: )?exit=0'
}

# ── preflight: 전제 확인 + 자체 잔재 청소 ─────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight (node=$NODE vendor=$VENDOR desc=$DESC_NAME resource=$RESOURCE)"

  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] 잔재 pod 정리·descriptor 존재·resolvedDevices·operator Deployment 확인"
    return 0
  fi

  # 이 스크립트 자체 테스트 잔재(default/tt-pod) 는 외부 워크로드가 아니므로 라이브 전에 지운다.
  # kubeget/kc 는 여기서부터 DRY_RUN="0" 이 보장되므로(위에서 조기 반환) 실제 kubectl 로 통한다.
  if kubeget -n "$LEFTOVER_NS" get pod "$LEFTOVER_POD" >/dev/null 2>&1; then
    log "  잔재 pod $LEFTOVER_NS/$LEFTOVER_POD 발견 — 삭제"
    kc -n "$LEFTOVER_NS" delete pod "$LEFTOVER_POD" --ignore-not-found >&2 || true
  fi

  if ! desc_present; then
    log "BLOCKED: AcceleratorDescriptor $DESC_NAME 없음 — 라이브에 먼저 적용 필요, 중단"
    BLOCKED=true; return 1
  fi
  if ! desc_resolved_for_node; then
    log "BLOCKED: descriptor $DESC_NAME 의 status.resolvedDevices 에 노드 $NODE 없음(장치 미관측) — 중단"
    BLOCKED=true; return 1
  fi
  if ! deploy_present; then
    log "BLOCKED: $NAMESPACE/$DEPLOY Deployment 없음 — operator 미배포, 중단"
    BLOCKED=true; return 1
  fi
  log "  시작 상태: aev($NODE) AllocationProbe 체크=$(aev_check_line || echo '<none>')"
}

# ── demo: probe 이미지 설정 → AVP 적용 → 재조정 → evidence 확인 ──────────
demo() {
  PHASE="demo"

  log "### demo 1: acpp.probeImages.$VENDOR=$PROBE_IMAGE 설정 → operator 재시작 대기"
  helmrun upgrade "$RELEASE" "$HELM_CHART" -n "$NAMESPACE" --reset-then-reuse-values \
    --set "acpp.probeImages.$VENDOR=$PROBE_IMAGE" >&2
  PROBE_IMAGE_SET=true
  if kc -n "$NAMESPACE" rollout status "deploy/$DEPLOY" --timeout="${T_ROLLOUT}s" >&2; then
    record "operator_rolled_out" true
  else
    record "operator_rolled_out" false "$T_ROLLOUT 초 내 rollout 미완료"
  fi

  log "### demo 2: AllocationProbe 검증 정책 적용 ($AVP_FILE)"
  kc apply -f "$AVP_FILE" >&2
  AVP_APPLIED=true

  log "### demo 3: descriptor 재조정 트리거(annotate touch) — AVP 는 감시 대상이 아니라 직접 건드려야 한다"
  kc annotate acceleratordescriptor "$DESC_NAME" "npu.ai/probe-trigger=$(utc_now)" --overwrite >&2
  ANNOTATED=true

  # probe Pod 는 RestartPolicy Never 로 1회 실행 후 곧바로 지워진다 — 스케줄 시점을 놓쳤어도
  # AcceleratorEvidence 의 AllocationProbe 체크(exit 코드 포함)가 판정의 근거다.
  log "### demo 4: probe Pod 관찰(best-effort, 참고용) + aev($NODE) AllocationProbe 체크 대기"
  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] kubectl -n $NAMESPACE get pods -l npu.ai/acpp-verify-pod=true"
  else
    kubeget -n "$NAMESPACE" get pods -l npu.ai/acpp-verify-pod=true --no-headers 2>/dev/null \
      | while IFS= read -r line; do log "  probe pod 관찰: $line"; done || true
  fi

  if [ "$DRY_RUN" = "1" ] || poll "$T_AEV" "$IV" allocation_probe_passed; then
    record "allocation_probe_exit0" true "$(aev_check_line)"
  else
    record "allocation_probe_exit0" false "$T_AEV 초 내 미통과: $(aev_check_line || echo '<체크 없음>')"
  fi

  DEMO_DONE=true
}

# ── cleanup: AVP 제거 → probe 이미지 복원(항상 실행) ──────────────────────
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: AVP 제거 → acpp.probeImages.$VENDOR 복원"

  if [ "$ANNOTATED" = true ]; then
    kc annotate acceleratordescriptor "$DESC_NAME" "npu.ai/probe-trigger-" >&2 || true
  fi
  if [ "$AVP_APPLIED" = true ]; then
    kc delete -f "$AVP_FILE" --ignore-not-found >&2 || true
  fi
  if [ "$PROBE_IMAGE_SET" = true ]; then
    helmrun upgrade "$RELEASE" "$HELM_CHART" -n "$NAMESPACE" --reset-then-reuse-values \
      --set "acpp.probeImages.$VENDOR=$ORIG_PROBE_IMAGE" >&2 || \
      log "  경고: probeImages 복원 helm upgrade 실패 — 수동 확인 필요"
    kc -n "$NAMESPACE" rollout status "deploy/$DEPLOY" --timeout="${T_ROLLOUT}s" >&2 || \
      log "  경고: 복원 rollout 미확인 — 수동 확인 필요"
  else
    log "  복원 불필요 — probeImages 를 바꾼 적 없음"
  fi
}

# ── finish: cleanup 보장 + JSON 요약 한 줄 + 종료코드 ─────────────────────
finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then
    verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then
    verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then
    verdict="PASS"
  fi
  printf '{"test":"descriptor-probe","vendor":"%s","node":"%s","descriptor":"%s","resource":"%s","scenario_minutes":%s,"scenario_destructive":"%s","phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DESC_NAME" "$RESOURCE" "$SCENARIO_MINUTES" "$SCENARIO_DESTRUCTIVE" "$PHASE" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "$(utc_now)" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "PASS" ] || [ "$verdict" = "DRY" ] || exit 1
  exit 0
}

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight || true
if [ "$BLOCKED" != true ]; then
  demo
fi
# EXIT trap(finish) 이 cleanup 실행 + JSON 요약 + 종료코드를 담당한다.
