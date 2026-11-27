# shellcheck shell=bash
# ============================================================
# lib.sh: fault-injection 3개 시나리오(01/02/03)가 공유하는 저수준 헬퍼
# 상세: kubectl DRY_RUN 래퍼·폴링·검사 집계를 한 곳에 모은다 — source 전용이며 그 자체로는
#       부작용이 없다. 각 run.sh 가 자신의 시나리오 로직에서 이 함수들을 부른다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# kc: 상태를 바꾸는 kubectl 래퍼. DRY_RUN=1 이면 명령만 찍고 실행하지 않는다.
kc() {
  if [ "${DRY_RUN:-0}" = "1" ]; then printf '+ kubectl %s\n' "$*" >&2; return 0; fi
  kubectl "$@"
}

# kcapply: stdin 의 YAML 을 적용. DRY_RUN=1 이면 YAML 만 찍는다(적용하지 않는다).
kcapply() {
  if [ "${DRY_RUN:-0}" = "1" ]; then printf '+ kubectl apply -f -  (매니페스트 생략, DRY_RUN)\n' >&2; cat >/dev/null; return 0; fi
  kubectl apply -f -
}

# kubeget: 읽기 전용 kubectl 래퍼. DRY_RUN=1 이면 클러스터에 접근하지 않고 빈 값을 낸다.
kubeget() {
  if [ "${DRY_RUN:-0}" = "1" ]; then return 0; fi
  kubectl "$@"
}

# record NAME true|false [detail] — 검사 결과를 CHECK_JSON/FAILS 전역에 집계한다(중단하지
# 않는다). 호출부가 CHECK_JSON="" FAILS=0 을 미리 선언해 둬야 한다.
record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

# poll_and_log LOGFILE TIMEOUT GETTER_CMD PATTERN [INTERVAL] — GETTER_CMD(eval)를 INTERVAL 초
# 간격(기본 3)으로 실행해 매 관측을 타임스탬프와 함께 LOGFILE 에 남기고, 출력이 PATTERN(ERE)에
# 매치하면 0 을 반환한다. 타임아웃 초과 시 1.
poll_and_log() {
  local logfile="$1" timeout="$2" getter="$3" pattern="$4" interval="${5:-3}"
  if [ "${DRY_RUN:-0}" = "1" ]; then
    printf '+ (poll <=%ss, log=%s) %s  [expect: %s]\n' "$timeout" "$(basename "$logfile")" "$getter" "$pattern" >&2
    return 0
  fi
  local elapsed=0 out
  while [ "$elapsed" -lt "$timeout" ]; do
    out="$(eval "$getter" 2>&1 || true)"
    printf '%s  %s\n' "$(utc_now)" "$out" >>"$logfile"
    if printf '%s' "$out" | grep -qE "$pattern"; then return 0; fi
    sleep "$interval"; elapsed=$((elapsed + interval))
  done
  return 1
}

# preflight_kind_node — kubectl 컨텍스트가 kind 인지 확인하고 NODE/CLUSTER_NAME 전역을
# 채운다. 실패하면 BLOCKED=true BLOCKED_REASON=... 을 설정하고 1 을 반환한다 — 중단(그 뒤
# demo 를 부르지 않는 것)은 호출부 책임이다. 다른 클러스터를 잘못 건드리지 않기
# 위한 마지막 방어선이다.
preflight_kind_node() {
  if [ "${DRY_RUN:-0}" = "1" ]; then
    NODE="<dry-run-node>"; CLUSTER_NAME="${CLUSTER_NAME:-kcloud-lab}"
    log "  [DRY_RUN] kind 컨텍스트 확인·노드 조회 생략"
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
  NODE="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
  if [ -z "$NODE" ]; then
    BLOCKED=true; BLOCKED_REASON="kind 클러스터에 노드가 없음"
    log "BLOCKED: $BLOCKED_REASON"; return 1
  fi
  log "  kind context 확인: $ctx (cluster=$CLUSTER_NAME, node=$NODE)"
  return 0
}

# summary_json TESTNAME [EXTRA_FIELDS_JSON] — 공통 JSON 요약 한 줄을 stdout 에 찍고, PASS·DRY
# 면 0, FAIL·BLOCKED 면 1 을 반환한다(return, exit 아님). 판정을 전역 변수에 남기지 않는 이유:
# 호출부가 `summary_json ... | tee -a file` 처럼 파이프 뒤에서 쓰는데, 파이프 왼쪽은 서브셸에서
# 돈다 — 그 서브셸의 전역 변수는 파이프가 끝나면 사라져 PASS 인데도 exit 1 이 나는 버그가
# 생긴다. `set -o pipefail` 이 있으면 이 반환값이 파이프 전체의 종료코드가 된다(호출부는
# `summary_json ... | tee -a "$OUT/..."; exit $?`). EXTRA_FIELDS_JSON 은 `"key":"value"` 나열(선택).
summary_json() {
  local test_name="$1" extra="${2:-}"
  local verdict="FAIL"
  if [ "${DRY_RUN:-0}" = "1" ]; then
    verdict="DRY"
  elif [ "${BLOCKED:-false}" = true ]; then
    verdict="BLOCKED"
  elif [ "${DEMO_DONE:-false}" = true ] && [ "${FAILS:-1}" -eq 0 ]; then
    verdict="PASS"
  fi
  printf '{"test":"%s","node":"%s"%s,"checks":{%s},"fails":%s,"blockedReason":"%s","verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$test_name" "${NODE:-}" "${extra:+,$extra}" "$CHECK_JSON" "${FAILS:-0}" "${BLOCKED_REASON:-}" \
    "$verdict" "$([ "${DRY_RUN:-0}" = "1" ] && echo true || echo false)" "$(utc_now)"
  [ "$verdict" = "PASS" ] || [ "$verdict" = "DRY" ]
}
