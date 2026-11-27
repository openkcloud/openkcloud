#!/usr/bin/env bash
# ============================================================
# setup.sh: kind 실험 클러스터(kcloud-lab) 부트스트랩 — CRD 적용 + operator 클러스터 밖 구동
# 상세: 운영 클러스터와 분리된 kind 클러스터를 만들고 operator CRD 를 적용한 뒤,
#       operator 를 `make run` 으로 호스트에서 백그라운드 구동한다(이미지 로드·웹훅
#       인증서 설정 불필요). docker 가 sudo 전용인 호스트라 kind 도 sudo 로 구동하고
#       kubeconfig 만 현재 사용자 소유로 바꾼다. 재실행 안전, DRY_RUN=1 지원.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

CLUSTER_NAME="${CLUSTER_NAME:-kcloud-lab}"
LAB_DIR="${LAB_DIR:-/tmp/kcloud-lab}"
KUBECONFIG_PATH="$LAB_DIR/kubeconfig"
OPERATOR_LOG="$LAB_DIR/operator.log"
OPERATOR_PID_FILE="$LAB_DIR/operator.pid"
READY_TIMEOUT="${READY_TIMEOUT:-180}"   # make run 이 "starting manager" 로그를 찍을 때까지 대기 상한(초)
CRD_DIR="$REPO_ROOT/deploy/helm/crds"   # helm 차트가 배포하는 CRD 전체 세트(13개)

DRY_RUN="${DRY_RUN:-0}"

log() { printf '%s\n' "$*" >&2; }

if [ "$DRY_RUN" = "1" ]; then
  cat >&2 <<EOF
+ mkdir -p $LAB_DIR
+ sudo kind create cluster --name $CLUSTER_NAME --kubeconfig $KUBECONFIG_PATH
+ sudo chown $(id -u):$(id -g) $KUBECONFIG_PATH
+ env KUBECONFIG=$KUBECONFIG_PATH kubectl apply -f $CRD_DIR/
+ env KUBECONFIG=$KUBECONFIG_PATH make -C $REPO_ROOT run  (백그라운드, 로그 $OPERATOR_LOG, PID파일 $OPERATOR_PID_FILE)
+ grep -q 'starting manager' $OPERATOR_LOG   # 최대 ${READY_TIMEOUT}s 대기
EOF
  exit 0
fi

mkdir -p "$LAB_DIR"

# --- 1. kind 클러스터(이미 있으면 재사용) ----------------------------------
if sudo kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "kind 클러스터 $CLUSTER_NAME 이미 존재 — 재사용"
  if [ ! -s "$KUBECONFIG_PATH" ]; then
    log "kubeconfig 없음 — 기존 클러스터에서 다시 받음"
    sudo kind get kubeconfig --name "$CLUSTER_NAME" | sudo tee "$KUBECONFIG_PATH" >/dev/null
    sudo chown "$(id -u):$(id -g)" "$KUBECONFIG_PATH"
  fi
else
  log "kind 클러스터 $CLUSTER_NAME 생성"
  # docker 가 sudo 로만 붙는 호스트라 kind 도 sudo 로 구동한다. kubeconfig 는 root
  # 소유로 만들어지므로 이후 kubectl/make 가 쓸 수 있게 현재 사용자로 넘긴다.
  sudo kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG_PATH"
  sudo chown "$(id -u):$(id -g)" "$KUBECONFIG_PATH"
fi

# --- 2. CRD 적용 ------------------------------------------------------------
# deploy/helm/crds/ 는 helm 차트가 실제로 배포하는 CRD 전체 세트(13개)이고, config/crd/bases/
# 와 diff -r 기준으로 내용이 같다(make crd-check 가 이 일치를 지킨다). kustomize 없이 차트가
# 배포하는 CRD 디렉터리를 그대로 적용한다.
log "CRD 적용(kubectl apply -f deploy/helm/crds/)"
env KUBECONFIG="$KUBECONFIG_PATH" kubectl apply -f "$CRD_DIR/"

# --- 3. operator 구동(클러스터 밖, make run) --------------------------------
# OPERATOR_NAMESPACE 를 지정하지 않으면 kube-system 기본값을 쓴다(internal/naming.
# OperatorNamespace). kube-system 은 kind 클러스터에 기본으로 있으므로 따로 만들
# 필요가 없다. webhook-cert-path 를 안 주므로 웹훅도 등록되지 않아 인증서도 불필요.
if [ -f "$OPERATOR_PID_FILE" ] && kill -0 "$(cat "$OPERATOR_PID_FILE")" 2>/dev/null; then
  log "operator 이미 구동 중(PID $(cat "$OPERATOR_PID_FILE")) — 다시 띄우지 않음"
else
  log "operator 구동(make run, 백그라운드) — 첫 실행은 generate/fmt/vet 때문에 수십 초 걸릴 수 있음"
  : > "$OPERATOR_LOG"
  setsid env KUBECONFIG="$KUBECONFIG_PATH" make -C "$REPO_ROOT" run \
    >"$OPERATOR_LOG" 2>&1 </dev/null &
  echo "$!" > "$OPERATOR_PID_FILE"

  log "manager 기동 대기(최대 ${READY_TIMEOUT}s)"
  elapsed=0
  until grep -q "starting manager" "$OPERATOR_LOG" 2>/dev/null; do
    if ! kill -0 "$(cat "$OPERATOR_PID_FILE")" 2>/dev/null; then
      log "operator 프로세스가 죽었음 — 로그 마지막 40줄:"
      tail -n 40 "$OPERATOR_LOG" >&2 || true
      exit 1
    fi
    if [ "$elapsed" -ge "$READY_TIMEOUT" ]; then
      log "manager 기동 대기 시간 초과(${READY_TIMEOUT}s) — 로그 마지막 40줄:"
      tail -n 40 "$OPERATOR_LOG" >&2 || true
      exit 1
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  log "operator 기동 확인(PID $(cat "$OPERATOR_PID_FILE"))"
fi

log "완료 — KUBECONFIG=$KUBECONFIG_PATH  로그=$OPERATOR_LOG"
