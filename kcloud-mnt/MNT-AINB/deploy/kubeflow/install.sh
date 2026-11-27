#!/usr/bin/env bash
# ============================================================
# AI 노트북 설치 스크립트 — Kubeflow 스택 + AI 노트북(포털·백엔드)
#
# 사용:
#   KUBECONFIG=<대상 클러스터 kubeconfig> VALUES=<설정 파일> ./install.sh [단계 ...]
#   단계: crd notebook profile kfp katib certmanager kserve okcai verify
#   단계를 비우면 처음부터 끝까지 순서대로 한다. 여러 번 돌려도 되게(멱등) 만들었다.
#
# 전제:
#   - 클러스터 관리자 권한 (CRD·ClusterRole을 만든다)
#   - 이미지를 받을 수 있는 상태 (인터넷, 사설 레지스트리, 또는 미리 반입 — images.txt)
#   - 매니페스트는 manifests/ 에 파일로 들어 있다 (인터넷에서 받지 않는다)
#
# 작성 2026-09-29 · ETRI 포팅용, 개발 PC 연습 클러스터(k3d, K8s v1.34)에서 검증
# ============================================================
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
M="$HERE/manifests"
: "${KUBECONFIG:?KUBECONFIG 를 설치할 클러스터로 지정하세요 (기본값을 쓰지 않도록 일부러 요구합니다)}"
VALUES=${VALUES:-$HERE/values.env}
if [ -f "$VALUES" ]; then set -a; . "$VALUES"; set +a; fi

K="kubectl --kubeconfig $KUBECONFIG"
SERVER=$($K config view --minify -o jsonpath='{.clusters[0].cluster.server}')
log() { echo "[$(date +%T)] $*"; }
log "대상 클러스터: $SERVER"
# 다른 클러스터(예: 개발 PC 운영 클러스터)에 잘못 설치하지 않도록 막는다
if [ -n "${PROTECT_SERVER:-}" ] && [ "$SERVER" = "$PROTECT_SERVER" ]; then
  echo "보호 대상 클러스터($PROTECT_SERVER)라 중단합니다"; exit 2
fi

wait_ns() {   # 네임스페이스의 Deployment가 모두 Available 될 때까지
  local ns=$1 t=${2:-600}
  log "  $ns 대기 (최대 ${t}초)"
  $K -n "$ns" wait --for=condition=Available deploy --all --timeout="${t}s"
}
apply_ss() { $K apply --server-side --force-conflicts -f "$1" >/dev/null; log "  적용 $(basename "$1")"; }

st_crd() {
  log "1) Istio CRD — Profile 컨트롤러가 필요로 한다 (Istio 본체는 설치하지 않는다)"
  apply_ss "$M/01-istio-crd-1.24.yaml"
}

st_notebook() {
  log "2) Notebook 컨트롤러"
  apply_ss "$M/02-notebook-controller-v1.10.0.yaml"
  wait_ns notebook-controller-system 300
}

st_profile() {
  log "3) Profile 컨트롤러 + KFAM"
  $K create namespace kubeflow --dry-run=client -o yaml | $K apply -f - >/dev/null
  apply_ss "$M/03-profile-controller-v1.10.0.yaml"
  $K -n kubeflow wait --for=condition=Available deploy/profiles-deployment --timeout=300s
}

st_kfp() {
  log "4) Kubeflow Pipelines 2.4.0 (+Argo, MySQL, MinIO, ML Metadata)"
  apply_ss "$M/04-kfp-cluster-scoped-2.4.0.yaml"
  $K wait --for=condition=established crd --all --timeout=120s >/dev/null
  apply_ss "$M/05-kfp-platform-agnostic-2.4.0.yaml"
  # gcr.io 에서 지워진 이미지 2개는 매니페스트 파일에서 이미 바꿔 두었다(minio, argoexec).
  # 워크플로 컨트롤러 설정에도 argoexec 이미지를 적어 둔다 (개발 클러스터와 같게).
  $K -n kubeflow patch configmap workflow-controller-configmap --type merge \
    -p '{"data":{"executor":"imagePullPolicy: IfNotPresent\nimage: quay.io/argoproj/argoexec:v3.4.17\n"}}' >/dev/null
  wait_ns kubeflow 1200
}

st_katib() {
  log "5) Katib v0.17.0 (HPO)"
  apply_ss "$M/06-katib-standalone-v0.17.0.yaml"
  wait_ns kubeflow 900
}

st_certmanager() {
  log "6) cert-manager v1.16.2 (KServe 웹훅 인증서)"
  apply_ss "$M/07-cert-manager-v1.16.2.yaml"
  wait_ns cert-manager 300
  sleep 15   # 웹훅이 인증서를 받아 응답하기까지 잠깐 걸린다
}

st_kserve() {
  log "7) KServe v0.14.1 — RawDeployment 모드"
  local i
  for i in 1 2 3 4 5; do   # cert-manager 웹훅이 늦게 뜨면 첫 적용이 실패할 수 있어 다시 시도
    if $K apply --server-side --force-conflicts -f "$M/08-kserve-v0.14.1.yaml" >/dev/null 2>&1; then break; fi
    log "  KServe 적용 재시도 $i"; sleep 20
  done
  $K -n kserve wait --for=condition=Available deploy/kserve-controller-manager --timeout=600s
  for i in 1 2 3 4 5; do
    if $K apply --server-side --force-conflicts -f "$M/09-kserve-cluster-resources-v0.14.1.yaml" >/dev/null 2>&1; then break; fi
    log "  서빙 런타임 적용 재시도 $i"; sleep 15
  done
  $K apply -f "$M/10-vllm-openai-runtime.yaml" >/dev/null
  # Knative 없이 쓰는 RawDeployment 모드. ingress 항목에 ingressGateway가 없으면 컨트롤러가 뜨지 않는다.
  $K -n kserve patch configmap inferenceservice-config --type merge -p '{"data":{
    "deploy":"{\"defaultDeploymentMode\":\"RawDeployment\"}",
    "ingress":"{\"ingressGateway\":\"knative-serving/knative-ingress-gateway\",\"ingressDomain\":\"'"${KSERVE_INGRESS_DOMAIN:-openkcloud.local}"'\",\"disableIngressCreation\":true}"}}' >/dev/null
  $K -n kserve rollout restart deploy/kserve-controller-manager >/dev/null
  $K -n kserve rollout status deploy/kserve-controller-manager --timeout=300s
}

st_okcai() {
  log "8) AI 노트북 (백엔드·포털·인그레스)"
  : "${OKC_HOST:?VALUES에 OKC_HOST(포털 도메인)를 적으세요}"
  : "${CMP_USERS:?VALUES에 CMP_USERS(아이디:비밀번호:역할,...)를 적으세요}"
  local sess=${CMP_SESSION_SECRET:-$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 40)}
  # MinIO 접근키는 KFP가 만든 비밀에서 읽는다 (따로 적었으면 그 값을 쓴다)
  local mk ms
  mk=${MODEL_STORE_KEY:-$($K -n kubeflow get secret mlpipeline-minio-artifact -o jsonpath='{.data.accesskey}' | base64 -d)}
  ms=${MODEL_STORE_SECRET:-$($K -n kubeflow get secret mlpipeline-minio-artifact -o jsonpath='{.data.secretkey}' | base64 -d)}
  local out; out=$(mktemp)
  OKC_SESSION="$sess" OKC_MK="$mk" OKC_MS="$ms" python3 - "$HERE/../k8s/okc-ai.yaml" "$out" <<'PY'
import os, re, sys
src, dst = sys.argv[1], sys.argv[2]
s = open(src, encoding="utf-8").read()
e = os.environ
def put(key, val):
    global s
    s, n = re.subn(rf'(^\s*{key}:\s*).*$', lambda m: m.group(1) + '"' + val.replace('"', '\\"') + '"', s, count=1, flags=re.M)
    if n != 1:
        sys.exit(f"okc-ai.yaml 에서 {key} 자리를 못 찾음")
put("CMP_SESSION_SECRET", e["OKC_SESSION"])
put("CMP_USERS", e["CMP_USERS"])
put("MODEL_STORE_KEY", e["OKC_MK"])
put("MODEL_STORE_SECRET", e["OKC_MS"])
url = e.get("PUBLIC_BASE_URL") or f"https://{e['OKC_HOST']}"
put("PUBLIC_BASE_URL", url)
put("CORS_ORIGINS", e.get("CORS_ORIGINS") or url)
put("ACCEL_RESOURCE", e.get("ACCEL_RESOURCE") or "nvidia.com/gpu")
put("GPU_RUNTIME_CLASS", e.get("GPU_RUNTIME_CLASS", ""))
s = re.sub(r'image: okc-ai-backend:\S+', 'image: ' + (e.get("OKC_BACKEND_IMAGE") or "okc-ai-backend:0.2"), s)
s = re.sub(r'image: okc-ai-portal:\S+', 'image: ' + (e.get("OKC_PORTAL_IMAGE") or "okc-ai-portal:0.2"), s)
s, n = re.subn(r'(- host: )okc-ai\.example\.org', r'\g<1>' + e["OKC_HOST"], s)
if n != 1:
    sys.exit("okc-ai.yaml 에서 Ingress host 자리를 못 찾음")
open(dst, "w", encoding="utf-8").write(s)
PY
  $K apply -f "$out" >/dev/null
  rm -f "$out"
  $K -n okc-ai rollout status deploy/okc-ai-backend --timeout=600s
  $K -n okc-ai rollout status deploy/okc-ai-portal --timeout=600s
}

st_verify() {
  log "9) 확인"
  local bad
  bad=$($K get pods -A --no-headers | awk '$4!="Running" && $4!="Completed" {print "  "$1"/"$2" "$4}')
  if [ -n "$bad" ]; then echo "정상이 아닌 파드:"; echo "$bad"; else log "  모든 파드 Running/Completed"; fi
  $K get deploy -A --no-headers | awk '{split($3,a,"/"); if (a[1]!=a[2]) print "  준비 안 된 Deployment: "$1"/"$2" "$3}'
  log "  서빙 런타임 $($K get clusterservingruntimes --no-headers | wc -l)개, Kubeflow CRD $($K get crd --no-headers | grep -c kubeflow.org)개"
}

STAGES=("$@")
[ ${#STAGES[@]} -eq 0 ] && STAGES=(crd notebook profile kfp katib certmanager kserve okcai verify)
for s in "${STAGES[@]}"; do "st_$s"; done
log "끝"
