#!/usr/bin/env bash
# ============================================================
# verify.sh: <rngd-node> 사전 컴파일 왕복 증거 확인 절차 (읽기 전용, evidence.sh --command 용)
# 상세: 적재된 furiosa_rngd 모듈이 사전 컴파일 이미지에 동봉된 모듈과 같은지 srcversion
#       대조로 증명한다(클러스터 무변경). 각 확인 항목은 아래 "== N. ... ==" 블록 참고
#       (Job 은 완료 후 GC 되므로 하네스가 저장한 $OUT/job.yaml·job.log 로 대체한다).
# 생성일: 2026-09-24 | 수정일: 2026-10-11
# ============================================================
set -uo pipefail

CTX="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"
NS="${NAMESPACE:-kcloud}"
NODE="${NODE:-rngd-1.cluster.local}"
DUS="${DUS:-rngd-1.cluster.local-furiosa}"
SSH_HOST="${SSH_HOST:?SSH_HOST 을 지정하십시오}"
IMG="${PRECOMPILED:?PRECOMPILED 을 지정하십시오}"
KO="${KO_PATH:-/opt/furiosa-ko/rngd/6.8.0-138-generic/furiosa_rngd.ko}"
DKMS_KO="/lib/modules/6.8.0-138-generic/updates/dkms/furiosa_rngd.ko"
OUT="${OUT:-/tmp/furiosa-prebuilt-evidence}"
kc() { kubectl --context "$CTX" "$@"; }
nssh() { ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 "$SSH_HOST" "$@"; }

echo "== 1. DUS resolvedImage =="
kc get dus "$DUS" -o jsonpath='{.status.state}/{.status.currentVersion} resolvedImage={.status.resolvedImage}'; echo

echo "== 2. install Job 이미지·시각 (완료 후 GC 됨 → 하네스 저장본) =="
if kc -n "$NS" get jobs -l "npu.ai/vendor=furiosa,npu.ai/node=$NODE" -o name 2>/dev/null | grep -q job; then
  kc -n "$NS" get jobs -l "npu.ai/vendor=furiosa,npu.ai/node=$NODE" \
    -o jsonpath='{range .items[*]}name={.metadata.name} image={.spec.template.spec.containers[0].image} start={.status.startTime} end={.status.completionTime}{"\n"}{end}'
elif [ -s "$OUT/job.yaml" ]; then
  python3 - "$OUT/job.yaml" <<'PY'
import sys, yaml, datetime
j = yaml.safe_load(open(sys.argv[1]))
c = j["spec"]["template"]["spec"]["containers"][0]
st = j.get("status", {}).get("startTime", ""); ct = j.get("status", {}).get("completionTime", "")
dur = ""
if st and ct:
    f = "%Y-%m-%dT%H:%M:%SZ"
    dur = int((datetime.datetime.strptime(ct, f) - datetime.datetime.strptime(st, f)).total_seconds())
print(f"name={j['metadata']['name']} image={c['image']} start={st} end={ct} durationSec={dur}")
PY
else
  echo "Job GC 됨, 저장본 없음"
fi

echo "== 3. install Job 로그 (사전 컴파일 설치 경로) =="
if [ -s "$OUT/job.log" ]; then cat "$OUT/job.log"; else echo "저장 로그 없음: $OUT/job.log"; fi

echo "== 4. 노드 적재 모듈 =="
echo -n "loaded srcversion: "; nssh 'cat /sys/module/furiosa_rngd/srcversion 2>/dev/null'
echo -n "loaded vermagic:   "; nssh "modinfo -F vermagic $DKMS_KO 2>/dev/null"
echo -n "on-disk filename:  "; nssh "modinfo -F filename $DKMS_KO 2>/dev/null"
echo "  (filename 이 DKMS 출력 경로임에 유의 — 사전 컴파일 설치가 그 자리를 덮어쓴다.)"

echo "== 5. 이미지 안 모듈 srcversion (docker run, HTTP 레지스트리) =="
IMG_SRC=$(sudo -n docker run --rm --entrypoint modinfo "$IMG" -F srcversion "$KO" 2>/dev/null | tr -d '[:space:]')
echo "image srcversion: ${IMG_SRC:-<읽기 실패>}"

echo "== 6. srcversion 대조 (경로가 아니라 바이너리로 사전 컴파일본임을 증명) =="
LOADED_SRC=$(nssh 'cat /sys/module/furiosa_rngd/srcversion 2>/dev/null' | tr -d '[:space:]')
if [ -n "$IMG_SRC" ] && [ "$LOADED_SRC" = "$IMG_SRC" ]; then
  echo "srcversion MATCH: $LOADED_SRC (적재 모듈 == 이미지 동봉 모듈)"
else
  echo "srcversion MISMATCH: loaded=$LOADED_SRC image=${IMG_SRC:-<none>}"
fi

echo "== 7. NDR driverVersion (전이 전후 동일 2026.3.1; 전이 중 driverLoaded false→true) =="
kc get ndr "$NODE" -o json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for x in d["status"]["devices"]:
    if x.get("vendor","").lower()=="furiosa": print("driverVersion="+str(x.get("driverVersion")),"driverLoaded="+str(x.get("driverLoaded")))'

echo "== 8. furiosa-smi 종료 코드 =="
nssh 'furiosa-smi info >/dev/null 2>&1; echo "furiosa-smi info exit="$?'
