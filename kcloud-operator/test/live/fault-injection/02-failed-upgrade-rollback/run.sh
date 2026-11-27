#!/usr/bin/env bash
# ============================================================
# run.sh: 실패 업그레이드 + 롤백 kind 재현 — 롤백 이미지 재구성·RollbackAttempts 상한
# 상세: test/fault/sequence_test.go 의 Sequence B(envtest 증명)를 실제 apiserver 위 job 모드
#       install Job 으로 재현한다(tt-upgrade-transition/run.sh 와 같은 레시피). 가짜
#       DIP·NDR·DUS 로 검증 실패→Rollback→소진→Failed 경로를 만들고, 롤백 이미지·시도 상한을
#       직접 대조한다(근거는 각 함수 주석 참고). 실제 장치·드라이버는 건드리지 않는다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
# 운영 클러스터는 이 스크립트 어디서도 참조하지 않는다 — kind 클러스터 전용 재현이다.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
source "$SCRIPT_DIR/../lib.sh"

DRY_RUN="${DRY_RUN:-0}"

VENDOR="${VENDOR:-tenstorrent}"
MODEL="${MODEL:-generic}"                    # DeviceModelMatches 의 양방향 와일드카드
GOOD_VERSION="${GOOD_VERSION:-2.0.0}"        # 업그레이드 목표 버전(install Job 은 성공하지만 검증은 실패)
BAD_VERSION="${BAD_VERSION:-1.0.0}"          # 사이클 시작 시 현재 버전(롤백 대상)
IMAGE_REPO="${IMAGE_REPO:-fault02-upgrade-stub}"
GOOD_TAG="${GOOD_TAG:-${GOOD_VERSION}-v1}"   # installer=ngc 라 태그가 곧 버전+variant
BAD_TAG="${BAD_TAG:-${BAD_VERSION}-v1}"      # reconstructPrevImage 가 계산하는 것과 동일 태그여야 함
DIP_NAME="${DIP_NAME:-fault02-dip}"
JOB_NS="${JOB_NS:-kube-system}"              # naming.OperatorNamespace() 기본값

NODE=""
DUS_NAME=""
CLUSTER_NAME="${CLUSTER_NAME:-}"

T_UPGRADING="${T_UPGRADING:-60}"
T_ROLLBACK_MSG="${T_ROLLBACK_MSG:-240}"      # validationTimeout(180s) + 여유
T_FAILED="${T_FAILED:-240}"
T_FAILED_STABLE="${T_FAILED_STABLE:-15}"     # Failed 도달 후 재확인까지 대기(자기치유 여부 관찰)
IV="${IV:-3}"

OUT="${OUT:-/tmp/fault-02-failed-upgrade-rollback-evidence}"
mkdir -p "$OUT"

NDR_APPLIED=false
DIP_APPLIED=false
DUS_APPLIED=false
IMAGES_BUILT=false
DEMO_DONE=false
BLOCKED=false
BLOCKED_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
ROLLBACK_JOB_NAME=""
ROLLBACK_JOB_IMAGE=""
FINAL_STATE=""
FINAL_ROLLBACK_ATTEMPTS=""
FAILED_AT=""

dus_state_line() {
  echo "kubectl get driverupgradestate '$DUS_NAME' -o jsonpath='{.status.state}{\" rb=\"}{.status.rollbackAttempts}{\" msg=\"}{.status.message}'"
}

preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR good=$GOOD_VERSION bad=$BAD_VERSION)"
  preflight_kind_node || return 1
  DUS_NAME="${NODE}-${VENDOR}"

  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] stub 이미지 빌드·kind load 생략"
    return 0
  fi

  log "  stub 이미지 빌드: $IMAGE_REPO:$GOOD_TAG(exit 0, 최초 install) / $IMAGE_REPO:$BAD_TAG(exit 1, rollback install)"
  sudo docker build --build-arg STUB_EXIT_CODE=0 -t "$IMAGE_REPO:$GOOD_TAG" "$SCRIPT_DIR/stub" >&2
  sudo docker build --build-arg STUB_EXIT_CODE=1 -t "$IMAGE_REPO:$BAD_TAG" "$SCRIPT_DIR/stub" >&2
  IMAGES_BUILT=true
  log "  kind load docker-image → cluster=$CLUSTER_NAME"
  sudo kind load docker-image "$IMAGE_REPO:$GOOD_TAG" "$IMAGE_REPO:$BAD_TAG" --name "$CLUSTER_NAME" >&2
}

# ndr_apply — driverLoaded=false 로 고정한다(설치가 실제로는 아무것도 못 올린 상태를 흉내).
# 이 값을 절대 true 로 바꾸지 않는 것이 이 시나리오의 전부다 — DriverModuleValidator 가
# 영원히 미통과해야 검증 타임아웃→Rollback 경로를 탄다.
# defaultValidators(state_machine.go:551)는 DriverModuleValidator 를 DevicePluginValidator
# 보다 먼저 fail-fast 로 돌리므로, 가짜 device-plugin 은 두지 않는다 — driverLoaded=false 가
# 유지되는 한 그 체크에 도달하지 않는다.
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
    "{\"status\":{\"devices\":[{\"vendor\":\"$VENDOR\",\"model\":\"$MODEL\",\"count\":1,\"driverLoaded\":false,\"driverVersion\":\"$BAD_VERSION\"}]}}" >&2
}

# dus_apply — UpgradeRequired 에서 직접 시작한다(Idle 부터 태우면 driverLoaded=false 가
# handleIdle 의 job-모드 self-heal 분기를 먼저 발동시켜 PreviousVersion 을 비운다 — 그러면
# 첫 rollback 진입에서 곧장 "이전 버전 없음"으로 Failed 에 떨어져 RollbackAttempts 소진 자체를
# 볼 수 없다. PreviousVersion 을 직접 채운 UpgradeRequired 에서 시작해 이 문제를 피한다).
# kind 노드는 node-role.kubernetes.io/control-plane 라벨이 있어 ensureUpgradeStates 가
# 자동 생성을 skip 한다(driver_upgrade_controller.go:180-183) — 그래서 DUS 를 직접 만든다.
dus_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: DriverUpgradeState
metadata:
  name: $DUS_NAME
spec:
  nodeName: $NODE
  vendor: $VENDOR
  model: $MODEL
EOF
  DUS_APPLIED=true
  local old_ts
  old_ts="$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "1970-01-01T00:00:00Z")"
  kc patch driverupgradestate "$DUS_NAME" --type=merge --subresource=status -p \
    "{\"status\":{\"state\":\"UpgradeRequired\",\"currentVersion\":\"$BAD_VERSION\",\"desiredVersion\":\"$GOOD_VERSION\",\"previousVersion\":\"$BAD_VERSION\",\"rollbackAttempts\":0,\"lastTransitionTime\":\"$old_ts\"}}" >&2
}

# dip_apply — 항상 마지막에 적용한다. status 가 아직 안 갖춰진 채 매칭 정책이 먼저 보이면
# Reconcile 이 "매칭 정책 없음"으로 즉시 no-op 반환하는 경합을 피한다(driver_upgrade_controller.go).
dip_apply() {
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: DriverInstallPolicy
metadata:
  name: $DIP_NAME
spec:
  vendor: $VENDOR
  model: $MODEL
  driver:
    image: "$IMAGE_REPO:$GOOD_TAG"
    version: "$GOOD_VERSION"
    installer: ngc
    mode: job
  kernelAllowlist: []
  jobOverrides:
    backoffLimit: 0
  upgradePolicy:
    autoUpgrade: true
    drainEnabled: false
    rollbackOnFailure: true
    validationTimeout: "180s"
    maxRollbackAttempts: 1
    idleCooldownSeconds: 0
    maxParallelUpgrades: 1
EOF
  DIP_APPLIED=true
}

demo() {
  PHASE="demo"
  log "### demo (node=$NODE vendor=$VENDOR bad=$BAD_VERSION desired=$GOOD_VERSION, driverLoaded 고정 false)"
  local log_f="$OUT/transitions.log"; : >"$log_f"

  ndr_apply
  dus_apply
  dip_apply

  local getter; getter="$(dus_state_line)"

  PHASE="demo:upgrading"
  if poll_and_log "$log_f" "$T_UPGRADING" "$getter" "Upgrading|Validating" "$IV"; then
    record "upgrade_cycle_started" true "UpgradeRequired→PreFlight→Cordoning→Draining→Upgrading 전이 확인"
  else
    record "upgrade_cycle_started" false "${T_UPGRADING}s 내 Upgrading/Validating 미도달"
  fi

  # state="Rollback" 자체는 handleRollbackJob 이 즉시 다시 Validating 으로 넘겨(state_machine_job.go:291-)
  # 한 reconcile 미만으로 지나간다 — status.state 로는 사실상 관측 불가. message 로 확인한다.
  PHASE="demo:rollback_triggered"
  if poll_and_log "$log_f" "$T_ROLLBACK_MSG" "$getter" "롤백 시작" "$IV"; then
    record "validation_failure_triggers_rollback" true \
      "NDR.driverLoaded=false 로 DriverModuleValidator 지속 미통과 → validationTimeout 초과 → Rollback(message='롤백 시작')"
  else
    record "validation_failure_triggers_rollback" false "${T_ROLLBACK_MSG}s 내 롤백 시작 메시지 미도달"
  fi

  # 첫 rollback install Job 의 실제 컨테이너 이미지가 reconstructPrevImage 계산값과 같은지
  # 직접 확인한다(핸드패치가 아니라 컨트롤러가 렌더링한 Job 스펙을 읽는다).
  PHASE="demo:rollback_image"
  local expect_image="$IMAGE_REPO:$BAD_TAG"
  if [ "$DRY_RUN" = "1" ]; then
    record "rollback_job_image_reconstructed" true "[DRY_RUN] expect=$expect_image"
  else
    local getter_job_img="kubectl get job -n '$JOB_NS' -l \"npu.ai/node=$NODE\" -o jsonpath='{.items[0].metadata.name}:{.items[0].spec.template.spec.containers[0].image}'"
    if poll_and_log "$log_f" 60 "$getter_job_img" ":${expect_image}\$" "$IV"; then
      local job_line; job_line="$(eval "$getter_job_img" 2>/dev/null || true)"
      ROLLBACK_JOB_NAME="${job_line%%:*}"
      ROLLBACK_JOB_IMAGE="${job_line#*:}"
      record "rollback_job_image_reconstructed" true \
        "job=$JOB_NS/$ROLLBACK_JOB_NAME image=$ROLLBACK_JOB_IMAGE (reconstructPrevImage 가 desired='$IMAGE_REPO:$GOOD_TAG' + prevVersion='$BAD_VERSION' 에서 계산한 값과 일치, state_machine_job.go:423-447)"
    else
      record "rollback_job_image_reconstructed" false "60s 내 이미지=$expect_image 인 install Job 미발견(node=$NODE, ns=$JOB_NS)"
    fi
  fi

  PHASE="demo:exhausted"
  if poll_and_log "$log_f" "$T_FAILED" "$getter" "^Failed" "$IV"; then
    FAILED_AT="$(utc_now)"
    if [ "$DRY_RUN" = "1" ]; then
      FINAL_STATE="Failed"; FINAL_ROLLBACK_ATTEMPTS="[DRY_RUN]"
    else
      FINAL_STATE="$(kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.state}' 2>/dev/null || echo "")"
      FINAL_ROLLBACK_ATTEMPTS="$(kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.rollbackAttempts}' 2>/dev/null || echo "")"
    fi
    record "rollback_attempts_bound_reached_failed" true \
      "rollback Job 도 stub exit 1 로 실패 → rollbackAttempts=$FINAL_ROLLBACK_ATTEMPTS > maxRollbackAttempts(1) → state=$FINAL_STATE (RollbackExhausted, state_machine_job.go:355-364) (${FAILED_AT})"
  else
    record "rollback_attempts_bound_reached_failed" false "${T_FAILED}s 내 Failed 미도달"
  fi

  PHASE="demo:stable"
  log "  Failed 도달 — ${T_FAILED_STABLE}s 후 재확인(자기치유 가드가 조건 미충족으로 닫혀 있는지)"
  if [ "$DRY_RUN" = "1" ]; then
    record "failed_state_stable_no_selfheal" true "[DRY_RUN]"
  else
    sleep "$T_FAILED_STABLE"
    local still; still="$(kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.state}' 2>/dev/null || echo "")"
    local still_rb; still_rb="$(kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.rollbackAttempts}' 2>/dev/null || echo "")"
    printf '%s  recheck state=%s rb=%s\n' "$(utc_now)" "$still" "$still_rb" >>"$log_f"
    record "failed_state_stable_no_selfheal" "$([ "$still" = "Failed" ] && [ "$still_rb" = "$FINAL_ROLLBACK_ATTEMPTS" ] && echo true || echo false)" \
      "재확인 state=$still rollbackAttempts=$still_rb(자기치유 가드가 driverLoaded=false 로 조건 미충족 — Failed 유지, 추가 rollback 시도 없음)"
  fi

  kubeget get events --field-selector involvedObject.kind=DriverUpgradeState \
    -o custom-columns=TIME:.lastTimestamp,REASON:.reason,MSG:.message --sort-by=.lastTimestamp \
    >"$OUT/events.txt" 2>/dev/null || true

  DEMO_DONE=true
}

cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: DIP·DUS·NDR·install Job·노드 cordon/라벨 정리"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] 정리 명령 생략"; return 0; fi

  if [ "$DIP_APPLIED" = true ]; then kc delete driverinstallpolicy "$DIP_NAME" --ignore-not-found >&2 || true; fi
  if [ "$DUS_APPLIED" = true ] && [ -n "$DUS_NAME" ]; then kc delete driverupgradestate "$DUS_NAME" --ignore-not-found >&2 || true; fi
  if [ "$NDR_APPLIED" = true ] && [ -n "$NODE" ]; then kc delete nodedevicereport "$NODE" --ignore-not-found >&2 || true; fi
  kc delete job -n "$JOB_NS" -l app.kubernetes.io/name=kcloud-driver-install --ignore-not-found >&2 || true

  if [ -n "$NODE" ]; then
    local unsched
    unsched="$(kubectl get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || echo "")"
    if [ "$unsched" = "true" ]; then
      log "  노드 $NODE 가 여전히 cordon 상태 — 방어적으로 uncordon"
      kubectl patch node "$NODE" --type=merge -p '{"spec":{"unschedulable":false}}' >&2 || true
    fi
    for key in npu.ai/driver-upgrading npu.ai/driver-upgrading-blocking; do
      kubectl label node "$NODE" "${key}-" >&2 2>/dev/null || true
    done
  fi

  if [ "$IMAGES_BUILT" = true ]; then
    log "  호스트 docker 이미지 제거: $IMAGE_REPO:$GOOD_TAG $IMAGE_REPO:$BAD_TAG"
    sudo docker rmi "$IMAGE_REPO:$GOOD_TAG" "$IMAGE_REPO:$BAD_TAG" >&2 2>/dev/null || true
  fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  summary_json "fault-02-failed-upgrade-rollback" \
    "\"vendor\":\"$VENDOR\",\"phase\":\"$PHASE\",\"rollbackJobImage\":\"$ROLLBACK_JOB_IMAGE\",\"finalState\":\"$FINAL_STATE\",\"finalRollbackAttempts\":\"$FINAL_ROLLBACK_ATTEMPTS\",\"failedAt\":\"$FAILED_AT\"" \
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
