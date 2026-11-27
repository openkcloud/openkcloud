#!/usr/bin/env bash
# ============================================================
# run.sh: Tenstorrent install-Job 멱등 경로 라이브 실행
# 상세: render/main.go 로 뽑은 install Job YAML 을 상태기계 없이 그대로 적용한다. 실제 설치/
#       rmmod 시도를 감지하면 대기를 끊고 Job 을 지운 뒤 FAIL 로 기록한다(재시도 없음).
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

DIP="${DIP:-tenstorrent-blackhole-ds}"
NODE="${NODE:-k8s-worker3}"
NAMESPACE="${NAMESPACE:-kcloud}"
DUS="${DUS:-k8s-worker3-tenstorrent}"
TIMEOUT="${TIMEOUT:-600}"
INTERVAL="${INTERVAL:-5}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

utc_now() { date -u +%FT%TZ; }

# 노드의 tenstorrent 관련 라벨/allocatable 만 뽑아 JSON 한 줄로 — before/after 비교용.
node_snapshot() {
  kubectl get node "$NODE" -o json 2>&1 | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)
except Exception as e:
    print(json.dumps({'error':str(e)})); sys.exit(0)
labels={k:v for k,v in d['metadata']['labels'].items() if 'tenstorrent' in k}
alloc={k:v for k,v in d['status']['allocatable'].items() if 'tenstorrent' in k}
print(json.dumps({'labels':labels,'allocatable':alloc}, sort_keys=True))
" 2>&1
}

dus_snapshot() {
  kubectl get dus "$DUS" -o json 2>&1 \
    | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)
except Exception as e:
    print(json.dumps({'error':str(e)})); sys.exit(0)
s=d.get('status',{})
print(json.dumps({'state':s.get('state'),'currentVersion':s.get('currentVersion'),'desiredVersion':s.get('desiredVersion')}, sort_keys=True))
" 2>&1
}

echo "=== before: DUS ${DUS} ==="
dus_before=$(dus_snapshot); echo "$dus_before"
echo "=== before: node ${NODE} labels/allocatable ==="
node_before=$(node_snapshot); echo "$node_before"

# 최소 안전 가드 — DUS 가 Idle 이고 버전이 일치하지 않으면(업그레이드 진행 중 등) 아예 적용하지 않는다.
dus_state=$(printf '%s' "$dus_before" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('state',''))" 2>/dev/null || echo "")
dus_cur=$(printf '%s' "$dus_before" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('currentVersion',''))" 2>/dev/null || echo "")
dus_des=$(printf '%s' "$dus_before" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('desiredVersion',''))" 2>/dev/null || echo "")
if [ "$dus_state" != "Idle" ] || [ "$dus_cur" != "$dus_des" ] || [ -z "$dus_cur" ]; then
  echo "BLOCKED: DUS ${DUS} 가 Idle/버전일치 상태가 아니다(state=${dus_state} current=${dus_cur} desired=${dus_des}) — 적용 중단" >&2
  printf '{"job":"","namespace":"%s","node":"%s","phase":"BLOCKED_BY_CONSTRAINT","skipBranch":"n/a","verdict":"BLOCKED","stateUnchanged":false,"date":"%s"}\n' \
    "$NAMESPACE" "$NODE" "$(utc_now)"
  exit 1
fi

echo "=== rendering Job ==="
# render 는 set -e 아래라 실패하면 스크립트가 JSON 요약 없이 죽는다 — 호출자가 실패를 인지하도록
# 여기서 명시적으로 가드해 JSON 요약을 반드시 낸다.
if ! job_yaml=$(go run ./test/live/tt-install-job/render -dip "$DIP" -node "$NODE" -namespace "$NAMESPACE"); then
  echo "render 실패 — install Job 을 못 뽑음, 적용하지 않음" >&2
  printf '{"job":"","namespace":"%s","node":"%s","phase":"RenderFailed","skipBranch":"n/a","verdict":"BLOCKED","stateUnchanged":false,"date":"%s"}\n' \
    "$NAMESPACE" "$NODE" "$(utc_now)"
  exit 1
fi
printf '%s\n' "$job_yaml"

echo "=== applying ==="
apply_rc=0
apply_out=$(printf '%s\n' "$job_yaml" | kubectl apply -f - 2>&1) || apply_rc=$?
echo "$apply_out"

job_name=$(printf '%s\n' "$apply_out" | sed -n 's#^job\.batch/\([^ ]*\) .*#\1#p' | head -1)

if [ "$apply_rc" -ne 0 ] || [ -z "$job_name" ]; then
  echo "apply 실패 또는 Job 이름을 못 얻음 — 중단(지울 Job 없음)" >&2
  printf '{"job":"","namespace":"%s","node":"%s","phase":"ApplyFailed","skipBranch":"n/a","verdict":"FAIL","stateUnchanged":false,"date":"%s"}\n' \
    "$NAMESPACE" "$NODE" "$(utc_now)"
  exit 1
fi
echo "job_name=${job_name}"

danger_re='기존 tenstorrent .* 언로드 시작|DKMS 빌드|DKMS install|baked tt-kmd .* 소스를'
skip_re='설치 단계 건너뜀'

phase="Timeout"
skip_branch="unknown"
elapsed=0
logs=""
while [ "$elapsed" -lt "$TIMEOUT" ]; do
  succeeded=$(kubectl get job "$job_name" -n "$NAMESPACE" -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "")
  failed=$(kubectl get job "$job_name" -n "$NAMESPACE" -o jsonpath='{.status.failed}' 2>/dev/null || echo "")
  logs=$(kubectl logs "job/${job_name}" -n "$NAMESPACE" --all-containers=true 2>&1 || echo "")

  if grep -E "$danger_re" >/dev/null <<<"$logs" && ! grep -E "$skip_re" >/dev/null <<<"$logs"; then
    echo "위험: 로그가 스킵이 아니라 실제 설치/언로드 시도를 보임 — 즉시 Job 삭제" >&2
    phase="Aborted"
    skip_branch="install_or_unload_attempted"
    break
  fi
  if [ "${succeeded:-0}" -ge 1 ] 2>/dev/null; then phase="Succeeded"; break; fi
  if [ "${failed:-0}" -ge 1 ] 2>/dev/null; then phase="Failed"; break; fi
  sleep "$INTERVAL"
  elapsed=$((elapsed + INTERVAL))
done

echo "=== job logs (job/${job_name}, phase=${phase}) ==="
printf '%s\n' "$logs"

if grep -E "$skip_re" >/dev/null <<<"$logs"; then
  skip_branch="skip"
elif [ "$skip_branch" != "install_or_unload_attempted" ]; then
  skip_branch="not_observed"
fi

echo "=== after: DUS ${DUS} ==="
dus_after=$(dus_snapshot); echo "$dus_after"
echo "=== after: node ${NODE} labels/allocatable ==="
node_after=$(node_snapshot); echo "$node_after"

echo "=== deleting job/${job_name} ==="
kubectl delete job "$job_name" -n "$NAMESPACE" --ignore-not-found=true 2>&1 || true

state_unchanged=false
[ "$dus_before" = "$dus_after" ] && [ "$node_before" = "$node_after" ] && state_unchanged=true

verdict="FAIL"
if [ "$phase" = "Succeeded" ] && [ "$skip_branch" = "skip" ] && [ "$state_unchanged" = "true" ]; then
  verdict="PASS"
fi

printf '{"job":"%s","namespace":"%s","node":"%s","phase":"%s","skipBranch":"%s","stateUnchanged":%s,"verdict":"%s","date":"%s"}\n' \
  "$job_name" "$NAMESPACE" "$NODE" "$phase" "$skip_branch" "$state_unchanged" "$verdict" "$(utc_now)"

# JSON 요약을 먼저 낸 뒤, PASS 가 아니면 CI/호출자가 실패로 인지하도록 비영 종료.
[ "$verdict" = PASS ] || exit 1
