#!/usr/bin/env bash
# ============================================================
# run.sh: 건강 저하(AdvertisementMismatch) kind 재현 — 할당 차단·재검증 요청·배치 후보 제외
# 상세: 가짜 NodeDeviceReport·AcceleratorPartitionPolicy·AcceleratorEvidence 입력만 쓰고
#       AcceleratorHealth.status 는 컨트롤러가 계산하게 두고 관측만 한다(DRY_RUN=1 이면 무변경).
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
source "$SCRIPT_DIR/../lib.sh"

DRY_RUN="${DRY_RUN:-0}"

VENDOR="${VENDOR:-nvidia}"
RESOURCE_NAME="${RESOURCE_NAME:-nvidia.com/gpu}"
EXPECTED_COUNT="${EXPECTED_COUNT:-4}"
ACCEL_LABEL="kcloud.ai/nvidia.present"
QUARANTINE_LABEL="kcloud.ai/health.quarantined"
ACPP_NAME="${ACPP_NAME:-fault01-baseline}"
POLICY_NAME="${POLICY_NAME:-fault01-ndr-freshness}"

# Degraded 는 반복되는 blip 이다(policy_apply 주석 참고) — 넉넉한 시간 예산과 촘촘한 표본
# 간격으로 잡는다. 다른 두 검사는 Degraded 를 잡은 직후 바로 확인하므로 짧아도 된다.
T_DEGRADED="${T_DEGRADED:-240}"
IV_DEGRADED="${IV_DEGRADED:-2}"
T_REVALIDATE="${T_REVALIDATE:-60}"
IV="${IV:-5}"

NODE=""
CLUSTER_NAME="${CLUSTER_NAME:-}"

OUT="${OUT:-/tmp/fault-01-health-degraded-evidence}"
mkdir -p "$OUT"

NDR_APPLIED=false
ACPP_APPLIED=false
EVIDENCE_APPLIED=false
POLICY_APPLIED=false
LABEL_APPLIED=false
DEMO_DONE=false
BLOCKED=false
BLOCKED_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
DEGRADED_AT=""

preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR resource=$RESOURCE_NAME expected=$EXPECTED_COUNT)"
  preflight_kind_node
}

# ndr_apply — NDR 생성 + status 패치. observedAt 을 신선하게 유지해야 NDRFreshness 신호가
# 통과한다(90초 기본 유예). vendor 엔트리 하나를 두되 pcieAddress 는 비운다 — 장치 단위
# 판정(deviceInputsFromNDR)은 PCI 없는 장치를 걸러내므로, 이 엔트리는 evidence.sh 의
# vendor 매치용으로만 쓰이고 device-level 분기(EvaluateDevices)에는 들어가지 않는다.
ndr_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: NodeDeviceReport
metadata:
  name: $NODE
spec:
  nodeName: $NODE
EOF
  NDR_APPLIED=true
  kc patch nodedevicereport "$NODE" --type=merge --subresource=status -p \
    "{\"status\":{\"observedAt\":\"$(utc_now)\",\"devices\":[{\"vendor\":\"$VENDOR\",\"model\":\"fault-injection-fake\",\"count\":$EXPECTED_COUNT,\"driverLoaded\":true,\"driverVersion\":\"0.0.0-fake\"}]}}" >&2
}

# acpp_apply — usableBaseline(acceleratorhealth_controller.go)이 요구하는 조건은 "이름이
# 가리키는 ACPP 가 존재하고 삭제 중이 아니다" 뿐이다(수렴·Ready 는 요구하지 않는다). 아무
# 노드도 맞지 않는 nodeSelector 를 줘서 이 ACPP 자신의 컨트롤러는 이 노드에 아무 것도
# 적용하지 않는다(존재만 필요).
acpp_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: AcceleratorPartitionPolicy
metadata:
  name: $ACPP_NAME
spec:
  vendor: $VENDOR
  nodeSelector:
    kcloud.ai/fault-injection-01-noop: "true"
  sharing:
    mode: exclusive
EOF
  ACPP_APPLIED=true
}

# evidence_apply — AcceleratorEvidence: usableBaseline 이 읽는 기준선(advertisedResources)과
# sourcePolicy 를 싣는다. Level 은 판정 입력이 아니라 비워 둔다.
evidence_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: AcceleratorEvidence
metadata:
  name: $NODE
spec:
  nodeName: $NODE
  vendor: $VENDOR
EOF
  EVIDENCE_APPLIED=true
  kc patch acceleratorevidence "$NODE" --type=merge --subresource=status -p \
    "{\"status\":{\"advertisedResources\":{\"$RESOURCE_NAME\":$EXPECTED_COUNT},\"sourcePolicy\":\"$ACPP_NAME\",\"observedAt\":\"$(utc_now)\"}}" >&2
}

# policy_apply — 이 노드(ACCEL_LABEL 로 선택)에만 적용되는 전용 정책. ndrFreshnessSeconds
# 만 늘리고 유예·감시주기·임계는 기본값을 물려받는다(검증하는 기준 자체는 그대로 둔다).
# AdvertisementMismatch 는 health 가 만드는 Revalidate 작업이 떠 있는 동안 RecoveryInFlight 로
# Recovering 이 되며 그 경로가 Advertisement 신호를 기록하지 않아 SuspectedAt 을 지운다 —
# 그래서 Degraded 는 약 55~60초 주기로 반복되는 blip 이다. 기본 NDR 신선도 유예(90초)로는 이
# 반복 대기 중 한 번만 심은 observedAt 이 stale 해져 신호를 못 보게 되므로 이 항목만 늘린다.
policy_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: AcceleratorHealthPolicy
metadata:
  name: $POLICY_NAME
spec:
  selector:
    matchLabels:
      $ACCEL_LABEL: "true"
  checks:
    ndrFreshnessSeconds: 900
EOF
  POLICY_APPLIED=true
}

# label_node_last — 입력을 다 갖춘 뒤 마지막에 노드를 라벨링해 첫 reconcile 을 튼다(주석
# 참고: AcceleratorHealthReconciler 는 Node 변화로만 깨어난다).
label_node_last() {
  kc label node "$NODE" "$ACCEL_LABEL=true" --overwrite >&2
  LABEL_APPLIED=true
}

# op_lines — 이 노드를 대상으로 하는 AcceleratorOperation 을 "type:owner.kind" 한 줄씩 낸다.
op_lines() {
  kubectl get acceleratoroperation -o json 2>/dev/null \
    | jq -r --arg n "$NODE" '.items[] | select(.spec.nodeName==$n) | (.spec.type + ":" + .spec.owner.kind)' 2>/dev/null || true
}

# health_state_line — state/allocationAllowed 를 "State/true"|"State/false" 로 낸다. jsonpath
# 대신 jq 로 읽는다 — AcceleratorHealthStatus.AllocationAllowed 는 `omitempty` 라 컨트롤러가
# 실제로 false 를 쓰면 Go 구조체 마샬링이 그 키를 빼버려 jsonpath 로는 "false" 문자열이 아니라
# 빈 문자열이 된다(디코딩 시 키 부재는 Go bool 기본값 false 와 동치라 operator 로직에는
# 영향 없다). 외부에서 이 값을 읽을 때는 `-o json | jq '(.status.x // false)'` 로 읽어야 한다.
health_state_line() {
  kubectl get acceleratorhealth "$NODE" -o json 2>/dev/null \
    | jq -r '(.status.state // "") + "/" + ((.status.allocationAllowed // false) | tostring)' 2>/dev/null || true
}

demo() {
  PHASE="demo"
  log "### demo (node=$NODE vendor=$VENDOR resource=$RESOURCE_NAME expected=$EXPECTED_COUNT actual=0)"
  local log_f="$OUT/transitions.log"; : >"$log_f"

  ndr_apply
  acpp_apply
  evidence_apply
  policy_apply
  label_node_last

  if poll_and_log "$log_f" "$T_DEGRADED" "health_state_line" "^Degraded/false$" "$IV_DEGRADED"; then
    DEGRADED_AT="$(utc_now)"
    record "advertisement_mismatch_degraded" true \
      "AcceleratorHealth.status(컨트롤러 계산)=Degraded/allocationAllowed=false, fault_test.go:408 과 같은 상태 (${DEGRADED_AT})"
  else
    record "advertisement_mismatch_degraded" false "${T_DEGRADED}s 내 Degraded/false 미도달"
  fi

  PHASE="demo:revalidate"
  # op_lines 는 이 스크립트에 이미 정의된 함수다 — poll_and_log 의 eval 이 같은 셸 프로세스
  # 안에서 돌므로 함수 이름을 그대로 getter 로 넘길 수 있다.
  if [ "$DRY_RUN" = "1" ]; then
    record "revalidate_operation_created" true "[DRY_RUN]"
  elif poll_and_log "$log_f" "$T_REVALIDATE" "op_lines" "^Revalidate:AcceleratorHealth$" "$IV"; then
    record "revalidate_operation_created" true \
      "health 가 Revalidate AcceleratorOperation 생성(remediation.go operationForReason, fault_test.go:413 의 Revalidate 기대와 같음)"
  else
    record "revalidate_operation_created" false "${T_REVALIDATE}s 내 Revalidate 작업 미발견"
  fi

  PHASE="demo:placement"
  if [ "$DRY_RUN" = "1" ]; then
    record "placement_excluded_via_quarantine_label" true "[DRY_RUN]"
  else
    local qval
    qval="$(kubectl get node "$NODE" -o json 2>/dev/null | jq -r --arg k "$QUARANTINE_LABEL" '.metadata.labels[$k] // ""')"
    record "placement_excluded_via_quarantine_label" "$([ "$qval" = "true" ] && echo true || echo false)" \
      "node label ${QUARANTINE_LABEL}='${qval:-<empty>}'(syncQuarantineLabel; intent.ApplyHealth 가 같은 status.allocationAllowed=false 를 읽어 배치 후보에서 뺀다, fault_test.go:424 의 배치 제외와 같은 신호)"
  fi

  kubeget get events --field-selector involvedObject.kind=AcceleratorHealth \
    -o custom-columns=TIME:.lastTimestamp,REASON:.reason,MSG:.message --sort-by=.lastTimestamp \
    >"$OUT/events.txt" 2>/dev/null || true

  DEMO_DONE=true
}

cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: Revalidate 작업·AcceleratorHealth·AcceleratorEvidence·ACPP·NDR·라벨 제거"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] 정리 명령 생략"; return 0; fi

  # 라벨을 먼저 뗀다 — AcceleratorHealth 를 먼저 지우면 그 사이(라벨이 아직 남은 동안)
  # Node 감시 경로가 라벨을 보고 AcceleratorHealth 를 재생성할 수 있다.
  if [ "$LABEL_APPLIED" = true ] && [ -n "$NODE" ]; then
    kubectl label node "$NODE" "${ACCEL_LABEL}-" "${QUARANTINE_LABEL}-" >&2 2>/dev/null || true
  fi
  if [ -n "$NODE" ]; then
    for op in $(kubectl get acceleratoroperation -o json 2>/dev/null \
      | jq -r --arg n "$NODE" '.items[] | select(.spec.nodeName==$n) | .metadata.name' 2>/dev/null || true); do
      kc delete acceleratoroperation "$op" --ignore-not-found >&2 || true
    done
    kc delete acceleratorhealth "$NODE" --ignore-not-found >&2 || true
  fi
  if [ "$EVIDENCE_APPLIED" = true ]; then kc delete acceleratorevidence "$NODE" --ignore-not-found >&2 || true; fi
  if [ "$ACPP_APPLIED" = true ]; then kc delete acceleratorpartitionpolicy "$ACPP_NAME" --ignore-not-found >&2 || true; fi
  if [ "$POLICY_APPLIED" = true ]; then kc delete acceleratorhealthpolicy "$POLICY_NAME" --ignore-not-found >&2 || true; fi
  if [ "$NDR_APPLIED" = true ]; then kc delete nodedevicereport "$NODE" --ignore-not-found >&2 || true; fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  summary_json "fault-01-health-degraded" \
    "\"vendor\":\"$VENDOR\",\"resource\":\"$RESOURCE_NAME\",\"phase\":\"$PHASE\",\"degradedAt\":\"$DEGRADED_AT\"" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  # summary_json 의 반환값이 pipefail 로 여기 넘어온다(lib.sh 참고 — 전역 변수로는 파이프의
  # 서브셸 경계 때문에 못 넘긴다).
  exit $?
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
