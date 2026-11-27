#!/usr/bin/env bash
# ============================================================
# run.sh: TT Job-모드 업그레이드 전이 kind 재현 — install Job 트리거·성공 수렴·롤백 소진
# 상세: kind 클러스터에 가짜 NDR·DIP·DUS 를 적용해 install Job 트리거→성공 수렴/롤백 소진까지
#       상태기계 제어 흐름을 재현한다(스텁 이미지·가짜 리소스 설계는 README 참고).
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DRY_RUN="${DRY_RUN:-0}"

# ---- 시나리오 파라미터 ----
VENDOR="${VENDOR:-tenstorrent}"
MODEL="${MODEL:-generic}"                 # DeviceModelMatches 의 양방향 와일드카드
GOOD_VERSION="${GOOD_VERSION:-2.0.0}"     # X: 업그레이드 목표 버전(성공 시나리오가 도달)
BAD_VERSION="${BAD_VERSION:-1.0.0}"       # Y: 사이클 시작 시 현재 버전(롤백 대상)
IMAGE_REPO="${IMAGE_REPO:-tt-upgrade-stub}"
GOOD_TAG="${GOOD_TAG:-${GOOD_VERSION}-v1}"   # installer=ngc 라 태그가 곧 버전+variant
BAD_TAG="${BAD_TAG:-${BAD_VERSION}-v1}"      # reconstructPrevImage 가 계산하는 것과 동일 태그여야 함
DIP_NAME="${DIP_NAME:-tt-upgrade-stub-dip}"
DP_DS_NAME="${DP_DS_NAME:-tt-device-plugin-fake}"
JOB_NS="${JOB_NS:-kube-system}"           # naming.OperatorNamespace() 기본값(OPERATOR_NAMESPACE 미설정 시)

NODE=""            # preflight 가 채운다
DUS_NAME=""        # "<NODE>-<VENDOR>" — driverUpgradeStateName() 과 동일 규칙
CLUSTER_NAME="${CLUSTER_NAME:-}"          # preflight 가 컨텍스트에서 유도(kind-<name>)

# ---- 대기 상한(초)·간격 ----
T_NODE_READY="${T_NODE_READY:-60}"
T_DP_READY="${T_DP_READY:-60}"
T_JOB_APPEAR="${T_JOB_APPEAR:-30}"
T_JOB_COMPLETE="${T_JOB_COMPLETE:-60}"
T_CONVERGE="${T_CONVERGE:-240}"           # validationTimeout(180s) + 여유
T_FAILED="${T_FAILED:-240}"
T_FAILED_STABLE="${T_FAILED_STABLE:-15}"  # Failed 도달 후 재확인까지 대기(자기치유 여부 관찰)
IV="${IV:-3}"

OUT="${OUT:-/tmp/tt-upgrade-transition-evidence}"
mkdir -p "$OUT"

# ---- 상태 변수 ----
DP_DS_APPLIED=false
NDR_APPLIED=false
DIP_APPLIED=false
DUS_APPLIED=false
DEMO_DONE=false
BLOCKED=false
BLOCKED_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
SUCCESS_JOB_NAME=""
SUCCESS_IDLE_TS=""
ROLLBACK_FAILED_TS=""
ROLLBACK_STILL_FAILED=""

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# kc: 상태를 바꾸는 kubectl 래퍼. DRY_RUN 이면 명령만 찍고 실행하지 않는다.
kc() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl %s\n' "$*" >&2; return 0; fi
  kubectl "$@"
}

# kcapply: stdin 의 YAML 을 적용. DRY_RUN 이면 YAML 만 찍는다.
kcapply() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl apply -f -  (매니페스트 생략, DRY_RUN)\n' >&2; cat >/dev/null; return 0; fi
  kubectl apply -f -
}

# kubeget: 읽기 전용 kubectl 래퍼. DRY_RUN 이면 클러스터에 접근하지 않고 빈 값을 낸다.
kubeget() {
  if [ "$DRY_RUN" = "1" ]; then return 0; fi
  kubectl "$@"
}

# hostrun: docker/kind 등 로컬 상태를 바꾸는 명령. DRY_RUN 이면 명령만 찍는다.
hostrun() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ %s\n' "$*" >&2; return 0; fi
  "$@"
}

# record NAME true|false [detail] — 검사 결과 집계(중단하지 않음).
record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

# poll_and_log LOGFILE TIMEOUT GETTER_CMD PATTERN — GETTER_CMD(eval)를 IV 초 간격으로
# 실행해 매 관측을 타임스탬프와 함께 LOGFILE 에 남기고, 출력이 PATTERN(ERE)에 매치하면 0 반환.
# 타임아웃 초과 시 1 반환. 관찰 시각을 통째로 남겨 README 타임라인의 근거가 된다.
poll_and_log() {
  local logfile="$1" timeout="$2" getter="$3" pattern="$4"
  if [ "$DRY_RUN" = "1" ]; then
    printf '+ (poll <=%ss, log=%s) %s  [expect: %s]\n' "$timeout" "$(basename "$logfile")" "$getter" "$pattern" >&2
    return 0
  fi
  local elapsed=0 out
  while [ "$elapsed" -lt "$timeout" ]; do
    out="$(eval "$getter" 2>&1 || true)"
    printf '%s  %s\n' "$(utc_now)" "$out" >>"$logfile"
    if printf '%s' "$out" | grep -qE "$pattern"; then return 0; fi
    sleep "$IV"; elapsed=$((elapsed + IV))
  done
  return 1
}

dus_state_line() {
  echo "kubectl get driverupgradestate '$DUS_NAME' -o jsonpath='{.status.state}{\" cur=\"}{.status.currentVersion}{\" des=\"}{.status.desiredVersion}{\" rb=\"}{.status.rollbackAttempts}{\" msg=\"}{.status.message}'"
}

# ── preflight: kind 컨텍스트 확인 + 노드 확인 + stub 이미지 빌드/로드 ──────
preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR model=$MODEL good=$GOOD_VERSION bad=$BAD_VERSION)"

  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] kind 컨텍스트 확인·노드 조회·stub 이미지 빌드+kind load 생략"
    NODE="<dry-run-node>"
    DUS_NAME="${NODE}-${VENDOR}"
    return 0
  fi

  local ctx
  if ! ctx="$(kubectl config current-context 2>&1)"; then
    BLOCKED=true; BLOCKED_REASON="kubectl config current-context 실패: $ctx"
    log "BLOCKED: $BLOCKED_REASON"; return 1
  fi
  case "$ctx" in
    kind-*) : ;;
    *)
      BLOCKED=true
      BLOCKED_REASON="현재 kubectl context='$ctx' 가 kind 클러스터가 아님 — 다른 클러스터를 건드리지 않기 위해 중단"
      log "BLOCKED: $BLOCKED_REASON"; return 1
      ;;
  esac
  CLUSTER_NAME="${CLUSTER_NAME:-${ctx#kind-}}"
  log "  kind context 확인: $ctx (cluster=$CLUSTER_NAME)"

  NODE="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
  if [ -z "$NODE" ]; then
    BLOCKED=true; BLOCKED_REASON="kind 클러스터에 노드가 없음"
    log "BLOCKED: $BLOCKED_REASON"; return 1
  fi
  DUS_NAME="${NODE}-${VENDOR}"
  log "  노드=$NODE DUS=$DUS_NAME"

  if ! kubectl wait --for=condition=Ready "node/$NODE" --timeout="${T_NODE_READY}s" >&2; then
    BLOCKED=true; BLOCKED_REASON="노드 $NODE 가 ${T_NODE_READY}s 내 Ready 아님"
    log "BLOCKED: $BLOCKED_REASON"; return 1
  fi

  log "  stub 이미지 빌드: $IMAGE_REPO:$GOOD_TAG(exit 0) / $IMAGE_REPO:$BAD_TAG(exit 1)"
  hostrun sudo docker build --build-arg STUB_EXIT_CODE=0 -t "$IMAGE_REPO:$GOOD_TAG" "$SCRIPT_DIR/stub" >&2
  hostrun sudo docker build --build-arg STUB_EXIT_CODE=1 -t "$IMAGE_REPO:$BAD_TAG" "$SCRIPT_DIR/stub" >&2
  log "  kind load docker-image → cluster=$CLUSTER_NAME"
  hostrun sudo kind load docker-image "$IMAGE_REPO:$GOOD_TAG" "$IMAGE_REPO:$BAD_TAG" --name "$CLUSTER_NAME" >&2
}

# ── ndr_apply LOADED VERSION — 가짜 NodeDeviceReport 생성/패치 ─────────────
ndr_apply() {
  local loaded="$1" version="$2"
  cat <<EOF | kcapply >&2
apiVersion: npu.ai/v1alpha1
kind: NodeDeviceReport
metadata:
  name: $NODE
spec:
  nodeName: $NODE
EOF
  kc patch nodedevicereport "$NODE" --type=merge --subresource=status -p \
    "{\"status\":{\"devices\":[{\"vendor\":\"$VENDOR\",\"model\":\"$MODEL\",\"count\":1,\"driverLoaded\":$loaded,\"driverVersion\":\"$version\"}]}}" >&2
  NDR_APPLIED=true
}

# ── dip_apply — 공유 DriverInstallPolicy(성공·롤백 두 시나리오가 동일 스펙 사용) ─
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

# ── scenario_success — Idle 트리거 → install Job → 완료 후 NDR 패치 → Idle 수렴 ──
scenario_success() {
  PHASE="scenario_success"
  log "### 시나리오 1(성공): Idle 에서 버전 불일치 감지 → install Job → 완료 후 NDR 패치 → Idle 수렴"
  local log_f="$OUT/success-transitions.log"; : >"$log_f"

  ndr_apply "true" "$BAD_VERSION"

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
  local old_ts; old_ts="$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "1970-01-01T00:00:00Z")"
  kc patch driverupgradestate "$DUS_NAME" --type=merge --subresource=status -p \
    "{\"status\":{\"state\":\"Idle\",\"currentVersion\":\"$BAD_VERSION\",\"desiredVersion\":\"$BAD_VERSION\",\"lastTransitionTime\":\"$old_ts\"}}" >&2
  DUS_APPLIED=true

  # DIP 는 마지막에 — status 가 완전히 갖춰진 뒤에야 reconcile 이 실제로 무언가 한다(경합 회피).
  dip_apply

  local getter; getter="$(dus_state_line)"
  if poll_and_log "$log_f" "$T_JOB_APPEAR" "$getter" "Upgrading|Validating"; then
    record "trigger_idle_to_upgrading" true "버전 불일치(${BAD_VERSION}→${GOOD_VERSION}) 감지, handleIdle 이 전이 시작"
  else
    record "trigger_idle_to_upgrading" false "${T_JOB_APPEAR}s 내 Upgrading/Validating 미도달"
  fi

  SUCCESS_JOB_NAME="$(kubeget get job -n "$JOB_NS" -l "npu.ai/node=$NODE,npu.ai/vendor=$(echo "$VENDOR" | tr '[:upper:]' '[:lower:]')" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")"
  if [ "$DRY_RUN" = "1" ]; then
    record "install_job_created" true "[DRY_RUN]"
  elif [ -n "$SUCCESS_JOB_NAME" ]; then
    kubeget get job "$SUCCESS_JOB_NAME" -n "$JOB_NS" -o yaml >"$OUT/success-install-job.yaml" 2>/dev/null || true
    record "install_job_created" true "job=$JOB_NS/$SUCCESS_JOB_NAME image=$IMAGE_REPO:$GOOD_TAG"
  else
    record "install_job_created" false "install Job 을 찾지 못함(namespace=$JOB_NS)"
  fi

  local job_getter="kubectl get job '$SUCCESS_JOB_NAME' -n '$JOB_NS' -o jsonpath='{.status.succeeded}'"
  if [ "$DRY_RUN" = "1" ]; then
    poll_and_log "$log_f" "$T_JOB_COMPLETE" "$job_getter" "^1$"
    record "install_job_succeeded" true "[DRY_RUN]"
  elif [ -n "$SUCCESS_JOB_NAME" ] && poll_and_log "$log_f" "$T_JOB_COMPLETE" "$job_getter" "^1$"; then
    record "install_job_succeeded" true "stub exit 0 확인(Job.status.succeeded=1)"
  else
    record "install_job_succeeded" false "${T_JOB_COMPLETE}s 내 Job 완료 미확인"
  fi

  log "  install Job 완료 확인 — NDR 을 목표 버전(${GOOD_VERSION})으로 즉시 패치"
  ndr_apply "true" "$GOOD_VERSION"

  if poll_and_log "$log_f" "$T_CONVERGE" "$getter" "^Idle cur=${GOOD_VERSION} des=${GOOD_VERSION}"; then
    SUCCESS_IDLE_TS="$(utc_now)"
    record "success_converged_to_idle" true "Validating→Uncordoning→Idle, currentVersion=${GOOD_VERSION} (${SUCCESS_IDLE_TS})"
  else
    record "success_converged_to_idle" false "${T_CONVERGE}s 내 Idle(${GOOD_VERSION}) 미도달"
  fi

  if [ "$DRY_RUN" = "1" ]; then
    record "node_uncordoned_after_success" true "[DRY_RUN]"
  else
    local unsched; unsched="$(kubeget get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || echo "?")"
    record "node_uncordoned_after_success" "$([ -z "$unsched" ] && echo true || echo false)" "spec.unschedulable='${unsched:-<empty>}'"
  fi

  kubeget get events --field-selector involvedObject.kind=DriverUpgradeState \
    -o custom-columns=TIME:.lastTimestamp,REASON:.reason,MSG:.message --sort-by=.lastTimestamp \
    >"$OUT/success-events.txt" 2>/dev/null || true

  # 다음 시나리오를 위해 DIP·DUS 만 정리(NDR·fake device-plugin 은 재사용).
  kc delete driverinstallpolicy "$DIP_NAME" --ignore-not-found >&2
  kc delete driverupgradestate "$DUS_NAME" --ignore-not-found >&2
  DIP_APPLIED=false; DUS_APPLIED=false
}

# ── scenario_rollback — UpgradeRequired 부터 시작 → 검증 실패 → Rollback 소진 → Failed(유지) ──
scenario_rollback() {
  PHASE="scenario_rollback"
  log "### 시나리오 2(롤백): UpgradeRequired 부터 시작(Idle 트리거는 시나리오 1 이 증명) → 검증 실패 → Rollback 소진 → Failed"
  local log_f="$OUT/rollback-transitions.log"; : >"$log_f"

  # driverLoaded=false — 모듈 미로드를 흉내(설치 실패 후 상태를 반영). Idle 부터 이 값을 주면
  # handleIdle 의 Job-모드 self-heal 분기(driverNotLoaded)가 버전-불일치 분기보다 먼저 발동해
  # PreviousVersion 을 비운다 — 그래서 이 시나리오는 Idle 을 건너뛰고 PreviousVersion 을 직접
  # 채운 UpgradeRequired 에서 시작한다(스크립트 헤더 주석 참고).
  ndr_apply "false" "$BAD_VERSION"

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
  local old_ts; old_ts="$(date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "1970-01-01T00:00:00Z")"
  kc patch driverupgradestate "$DUS_NAME" --type=merge --subresource=status -p \
    "{\"status\":{\"state\":\"UpgradeRequired\",\"currentVersion\":\"$BAD_VERSION\",\"desiredVersion\":\"$GOOD_VERSION\",\"previousVersion\":\"$BAD_VERSION\",\"rollbackAttempts\":0,\"lastTransitionTime\":\"$old_ts\"}}" >&2
  DUS_APPLIED=true

  dip_apply

  local getter; getter="$(dus_state_line)"
  if poll_and_log "$log_f" "$T_JOB_APPEAR" "$getter" "Upgrading|Validating"; then
    record "rollback_scenario_upgrading_started" true "UpgradeRequired→PreFlight→Cordoning→Draining→Upgrading 전이 확인"
  else
    record "rollback_scenario_upgrading_started" false "${T_JOB_APPEAR}s 내 Upgrading/Validating 미도달"
  fi

  # state="Rollback" 자체는 handleRollbackJob 이 즉시 다시 Validating 으로 넘겨(job 생성/재사용
  # 확인 후 바로 전이) 한 reconcile 미만으로 지나간다 — status.state 필드로는 사실상 관측 불가.
  # 대신 handleRollbackJob 이 남기는 message("롤백 시작(job): ...")로 롤백 시도가 실제 있었음을 확인한다.
  if poll_and_log "$log_f" "$T_FAILED" "$getter" "롤백 시작"; then
    record "validation_failure_triggers_rollback" true "NDR.driverLoaded=false 로 DriverModuleValidator 지속 미통과 → validationTimeout 초과 → Rollback(message='롤백 시작')"
  else
    record "validation_failure_triggers_rollback" false "${T_FAILED}s 내 롤백 시작 메시지 미도달"
  fi

  if poll_and_log "$log_f" "$T_FAILED" "$getter" "^Failed"; then
    ROLLBACK_FAILED_TS="$(utc_now)"
    local msg; msg="$(kubeget get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.message}' 2>/dev/null || echo "")"
    record "rollback_exhausted_to_failed" true "rollback Job 도 stub exit 1 로 실패, MaxRollbackAttempts(1) 초과 → Failed (${ROLLBACK_FAILED_TS}) msg='$msg'"
  else
    record "rollback_exhausted_to_failed" false "${T_FAILED}s 내 Failed 미도달"
  fi

  log "  Failed 도달 — ${T_FAILED_STABLE}s 후 재확인(자기치유 가드가 조건 미충족으로 닫혀 있는지)"
  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] ${T_FAILED_STABLE}s 대기 + 재확인 생략"
    record "failed_state_stable_guard_closed" true "[DRY_RUN]"
  else
    sleep "$T_FAILED_STABLE"
    ROLLBACK_STILL_FAILED="$(kubectl get driverupgradestate "$DUS_NAME" -o jsonpath='{.status.state}' 2>/dev/null || echo "")"
    printf '%s  %s\n' "$(utc_now)" "recheck state=$ROLLBACK_STILL_FAILED" >>"$log_f"
    if [ "$ROLLBACK_STILL_FAILED" = "Failed" ]; then
      record "failed_state_stable_guard_closed" true "handleFailed 의 자기치유 가드가 조건(driverLoaded=false) 미충족으로 닫힌 채 유지 — Failed 가 self-heal 되지 않음"
    else
      record "failed_state_stable_guard_closed" false "재확인 시점 state='$ROLLBACK_STILL_FAILED' (Failed 아님 — 예상과 다름)"
    fi
  fi

  kubeget get events --field-selector involvedObject.kind=DriverUpgradeState \
    -o custom-columns=TIME:.lastTimestamp,REASON:.reason,MSG:.message --sort-by=.lastTimestamp \
    >"$OUT/rollback-events.txt" 2>/dev/null || true

  DEMO_DONE=true
}

# ── demo: 공용 fake device-plugin 기동 → 성공 시나리오 → 롤백 시나리오 ─────
demo() {
  PHASE="demo"
  log "### demo (node=$NODE vendor=$VENDOR good=$GOOD_VERSION bad=$BAD_VERSION)"

  cat <<EOF | kcapply >&2
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: $DP_DS_NAME
  namespace: $JOB_NS
  labels:
    app.kubernetes.io/name: $DP_DS_NAME
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: $DP_DS_NAME
  template:
    metadata:
      labels:
        app.kubernetes.io/name: $DP_DS_NAME
    spec:
      tolerations:
      - operator: Exists
      containers:
      - name: fake
        image: busybox:1.36
        command: ["sleep", "3600"]
EOF
  DP_DS_APPLIED=true
  if [ "$DRY_RUN" != "1" ] && kubectl rollout status "ds/$DP_DS_NAME" -n "$JOB_NS" --timeout="${T_DP_READY}s" >&2; then
    record "fake_device_plugin_ready" true "DaemonSet $JOB_NS/$DP_DS_NAME — DevicePluginValidator 가 매칭할 pod 제공(vendor 별칭 'tt' 불필요, vendor-neutral 라벨)"
  elif [ "$DRY_RUN" = "1" ]; then
    record "fake_device_plugin_ready" true "[DRY_RUN]"
  else
    record "fake_device_plugin_ready" false "${T_DP_READY}s 내 롤아웃 미완료"
  fi

  scenario_success
  scenario_rollback
}

# ── cleanup: 두 시나리오가 만든 CR·DaemonSet 제거(항상 실행) ───────────────
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: DIP·DUS·NDR·fake device-plugin 제거 + 노드 uncordon 확인"

  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] 정리 명령 생략"
    return 0
  fi

  if [ "$DIP_APPLIED" = true ]; then
    kc delete driverinstallpolicy "$DIP_NAME" --ignore-not-found >&2 || true
  fi
  if [ "$DUS_APPLIED" = true ] && [ -n "$DUS_NAME" ]; then
    kc delete driverupgradestate "$DUS_NAME" --ignore-not-found >&2 || true
  fi
  if [ "$NDR_APPLIED" = true ] && [ -n "$NODE" ]; then
    kc delete nodedevicereport "$NODE" --ignore-not-found >&2 || true
  fi
  if [ "$DP_DS_APPLIED" = true ]; then
    kc delete daemonset "$DP_DS_NAME" -n "$JOB_NS" --ignore-not-found >&2 || true
  fi
  kc delete job -n "$JOB_NS" -l app.kubernetes.io/name=kcloud-driver-install --ignore-not-found >&2 || true

  if [ -n "$NODE" ]; then
    local unsched; unsched="$(kubectl get node "$NODE" -o jsonpath='{.spec.unschedulable}' 2>/dev/null || echo "")"
    if [ "$unsched" = "true" ]; then
      log "  노드 $NODE 가 여전히 cordon 상태 — 방어적으로 uncordon"
      kubectl patch node "$NODE" --type=merge -p '{"spec":{"unschedulable":false}}' >&2 || true
    fi
    for key in npu.ai/driver-upgrading npu.ai/driver-upgrading-blocking; do
      kubectl label node "$NODE" "${key}-" >&2 2>/dev/null || true
    done
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
  printf '{"test":"tt-upgrade-transition","node":"%s","vendor":"%s","goodVersion":"%s","badVersion":"%s","phase":"%s","successIdleAt":"%s","rollbackFailedAt":"%s","rollbackStillFailed":"%s","checks":{%s},"fails":%s,"blockedReason":"%s","verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$NODE" "$VENDOR" "$GOOD_VERSION" "$BAD_VERSION" "$PHASE" "$SUCCESS_IDLE_TS" "$ROLLBACK_FAILED_TS" "$ROLLBACK_STILL_FAILED" "$CHECK_JSON" "$FAILS" "$BLOCKED_REASON" "$verdict" "$dry" "$(utc_now)" \
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
