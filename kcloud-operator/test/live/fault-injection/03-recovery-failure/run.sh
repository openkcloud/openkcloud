#!/usr/bin/env bash
# ============================================================
# run.sh: 복구 반복 실패 → 격리(Quarantined) kind 재현 — 자동 Healthy 복귀 없음 확인
# 상세: 가짜 NodeDeviceReport 하나(driverLoaded=false)만 쓰고 나머지는 컨트롤러가 계산한다.
#       드라이버 pod 없는 kind 랩에서 RecoverDevice 가 매번 ApplyFailed 로 끝나 반복 실패→Quarantined 를 만든다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
source "$SCRIPT_DIR/../lib.sh"

DRY_RUN="${DRY_RUN:-0}"

VENDOR="${VENDOR:-nvidia}"
ACCEL_LABEL="kcloud.ai/nvidia.present"
QUARANTINE_LABEL="kcloud.ai/health.quarantined"
POLICY_NAME="${POLICY_NAME:-fault03-fast-policy}"
REPEATED_FAILURE_THRESHOLD="${REPEATED_FAILURE_THRESHOLD:-2}"
MONITOR_INTERVAL_SECONDS="${MONITOR_INTERVAL_SECONDS:-5}"
RECOVERY_COOLDOWN_SECONDS="${RECOVERY_COOLDOWN_SECONDS:-3}"

T_QUARANTINED="${T_QUARANTINED:-300}"
T_NO_SELFHEAL="${T_NO_SELFHEAL:-30}"
IV="${IV:-5}"

NODE=""
CLUSTER_NAME="${CLUSTER_NAME:-}"

OUT="${OUT:-/tmp/fault-03-recovery-failure-evidence}"
mkdir -p "$OUT"

NDR_APPLIED=false
POLICY_APPLIED=false
LABEL_APPLIED=false
DEMO_DONE=false
BLOCKED=false
BLOCKED_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
QUARANTINED_AT=""
FINAL_RECOVERY_FAILURES=""

preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR repeatedFailureThreshold=$REPEATED_FAILURE_THRESHOLD)"
  preflight_kind_node
}

# ndr_apply LOADED — driverLoaded 를 지정값으로 둔다. pcieAddress 는 비워 장치 단위 판정
# (deviceInputsFromNDR)에는 들어가지 않게 한다 — 이 시나리오가 쓰는 것은 노드 단위
# DriverHeartbeat 신호(anyDriverLoaded)뿐이다.
ndr_apply() {
  local loaded="$1"
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
    "{\"status\":{\"observedAt\":\"$(utc_now)\",\"devices\":[{\"vendor\":\"$VENDOR\",\"model\":\"fault-injection-fake\",\"count\":1,\"driverLoaded\":$loaded,\"driverVersion\":\"0.0.0-fake\"}]}}" >&2
}

# policy_apply — 이 노드(ACCEL_LABEL 로 선택)에만 적용되는 전용 빠른 정책. 상태기계 로직은
# 그대로이고 임계·주기·쿨다운 세 숫자만 랩 시간 예산에 맞게 줄인다.
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
    monitorIntervalSeconds: $MONITOR_INTERVAL_SECONDS
    repeatedFailureThreshold: $REPEATED_FAILURE_THRESHOLD
    ndrFreshnessSeconds: 900
  remediation:
    DriverNotLoaded:
      action: RequestRecovery
      recoveryCooldownSeconds: $RECOVERY_COOLDOWN_SECONDS
EOF
  POLICY_APPLIED=true
}

# label_node_last — 입력을 다 갖춘 뒤 마지막에 노드를 라벨링해 첫 reconcile 을 튼다
# (AcceleratorHealthReconciler 는 corev1.Node 감시로만 깨어난다).
label_node_last() {
  kc label node "$NODE" "$ACCEL_LABEL=true" --overwrite >&2
  LABEL_APPLIED=true
}

# recover_op_lines — 이 노드·이 health 소유의 RecoverDevice 작업을 "phase" 한 줄씩 낸다.
recover_op_lines() {
  kubectl get acceleratoroperation -o json 2>/dev/null \
    | jq -r --arg n "$NODE" '.items[] | select(.spec.nodeName==$n and .spec.type=="RecoverDevice" and .spec.owner.kind=="AcceleratorHealth") | .status.phase' 2>/dev/null || true
}

# health_state_line — state/allocationAllowed/recoveryFailures 를 jq 로 읽는다. jsonpath 가
# 아니라 jq 인 이유: allocationAllowed=false 는 Go 구조체의 `omitempty` 때문에 컨트롤러가
# 실제로 쓴 status 에는 키 자체가 빠져 jsonpath 로는 빈 문자열이 된다("false" 문자열이
# 아니다).
health_state_line() {
  kubectl get acceleratorhealth "$NODE" -o json 2>/dev/null \
    | jq -r '(.status.state // "") + "/" + ((.status.allocationAllowed // false) | tostring) + "/" + ((.status.recoveryFailures // 0) | tostring)' 2>/dev/null || true
}

demo() {
  PHASE="demo"
  log "### demo (node=$NODE vendor=$VENDOR driverLoaded=false 고정 → 반복 RecoverDevice 실패 기대)"
  local log_f="$OUT/transitions.log"; : >"$log_f"

  ndr_apply false
  policy_apply
  label_node_last

  PHASE="demo:quarantine"
  if poll_and_log "$log_f" "$T_QUARANTINED" "health_state_line" "^Quarantined/false/${REPEATED_FAILURE_THRESHOLD}\$" "$IV"; then
    QUARANTINED_AT="$(utc_now)"
    FINAL_RECOVERY_FAILURES="$REPEATED_FAILURE_THRESHOLD"
    record "repeated_recovery_failure_quarantined" true \
      "AcceleratorHealth.status(컨트롤러 계산)=Quarantined/allocationAllowed=false/recoveryFailures=$REPEATED_FAILURE_THRESHOLD, sequence_test.go:174 와 같은 상태 (${QUARANTINED_AT})"
  else
    record "repeated_recovery_failure_quarantined" false "${T_QUARANTINED}s 내 Quarantined/false/${REPEATED_FAILURE_THRESHOLD} 미도달"
  fi

  PHASE="demo:rolledback_ops"
  if [ "$DRY_RUN" = "1" ]; then
    record "recover_device_ops_rolled_back" true "[DRY_RUN]"
  else
    local phases rolledback_count
    phases="$(recover_op_lines)"
    rolledback_count="$(printf '%s\n' "$phases" | grep -c '^RolledBack$' || true)"
    printf '%s  recover_op_phases=%s\n' "$(utc_now)" "$(printf '%s' "$phases" | tr '\n' ',')" >>"$log_f"
    record "recover_device_ops_rolled_back" "$([ "$rolledback_count" -ge "$REPEATED_FAILURE_THRESHOLD" ] && echo true || echo false)" \
      "RolledBack 상태 RecoverDevice 작업 $rolledback_count 건(기대 >= $REPEATED_FAILURE_THRESHOLD) — 참가자가 '지울 드라이버 pod 없음'으로 매번 ApplyFailed(recoverdevice_participant.go)"
  fi

  # 격리는 복구를 거쳐야만 풀린다(AllowedTransition) — 신호를 고쳐도 자동으로 Healthy 로
  # 돌아오지 않아야 한다. driverLoaded 를 true 로 되돌려 확인한다.
  PHASE="demo:no_selfheal"
  if [ "$DRY_RUN" = "1" ]; then
    record "quarantine_no_automatic_healthy_return" true "[DRY_RUN]"
  else
    ndr_apply true
    sleep "$T_NO_SELFHEAL"
    local still; still="$(health_state_line | cut -d/ -f1-2)"
    printf '%s  recheck(after driverLoaded=true) state/alloc=%s\n' "$(utc_now)" "$still" >>"$log_f"
    record "quarantine_no_automatic_healthy_return" "$([ "$still" = "Quarantined/false" ] && echo true || echo false)" \
      "driverLoaded=true 로 되돌린 뒤 ${T_NO_SELFHEAL}s 재확인: state/alloc=$still (AllowedTransition(Quarantined,Healthy)=false, sequence_test.go:176 과 같은 불변식)"
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
  log "### cleanup: RecoverDevice 작업·AcceleratorHealth·정책·NDR·라벨 제거"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] 정리 명령 생략"; return 0; fi

  if [ -n "$NODE" ]; then
    for op in $(kubectl get acceleratoroperation -o json 2>/dev/null \
      | jq -r --arg n "$NODE" '.items[] | select(.spec.nodeName==$n and .spec.owner.kind=="AcceleratorHealth") | .metadata.name' 2>/dev/null || true); do
      kc delete acceleratoroperation "$op" --ignore-not-found >&2 || true
    done
    kc delete acceleratorhealth "$NODE" --ignore-not-found >&2 || true
  fi
  if [ "$POLICY_APPLIED" = true ]; then kc delete acceleratorhealthpolicy "$POLICY_NAME" --ignore-not-found >&2 || true; fi
  if [ "$NDR_APPLIED" = true ]; then kc delete nodedevicereport "$NODE" --ignore-not-found >&2 || true; fi
  if [ "$LABEL_APPLIED" = true ] && [ -n "$NODE" ]; then
    kubectl label node "$NODE" "${ACCEL_LABEL}-" "${QUARANTINE_LABEL}-" >&2 2>/dev/null || true
  fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  summary_json "fault-03-recovery-failure" \
    "\"vendor\":\"$VENDOR\",\"phase\":\"$PHASE\",\"repeatedFailureThreshold\":$REPEATED_FAILURE_THRESHOLD,\"quarantinedAt\":\"$QUARANTINED_AT\",\"finalRecoveryFailures\":\"$FINAL_RECOVERY_FAILURES\"" \
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
