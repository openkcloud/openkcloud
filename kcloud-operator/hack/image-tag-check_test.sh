#!/usr/bin/env bash
# ============================================================
# image-tag-check_test.sh: image-tag-check.sh 수집기의 분류를 시험한다
# 상세: --collect-only 로 수집기만 돌려 레지스트리 없이 고정한다. 픽스처 한 벌에 수집기가
#       구분해야 할 자리 종류(필드·키·인자·환경변수·자리표시자·digest·kernelImages 맵 등)를
#       섞어 넣는다. 조회 판정(404 와 401 의 구분)은 네트워크가 필요해 여기서 다루지 않는다.
# 생성일: 2026-10-04 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/image-tag-check.sh"
pass=0; fail=0

check() { # check <설명> <기대값> <실제값>
  if [ "$2" = "$3" ]; then pass=$((pass+1)); echo "PASS: $1"
  else fail=$((fail+1)); echo "FAIL: $1"; echo "  기대: $2"; echo "  실제: $3"; fi
}

# 같은 kind 문서가 둘(아래 NPUClusterPolicy 두 블록)인 것은 자리 경로에 문서 번호가
# 빠지면 두 자리가 하나로 접히는 회귀를 잡는다 — 접히면 image: 자리 수가 줄어든다.
render="$(cat <<'YAML'
kind: Deployment
spec:
  template:
    spec:
      containers:
      - image: "ghcr.io/example/operator:v9"
        args:
        - "--generated-dp-image=ghcr.io/example/dp:v1"
        - "--leader-elect"
        env:
        - name: FUTURE_SIDECAR_IMAGE
          value: "ghcr.io/example/sidecar:v2"
        - name: RENAMED_WITHOUT_THE_WORD
          value: "docker.io/example/renamed:v3"
        - name: NOT_AN_IMAGE
          value: "kube-system"
        nodeSelector:
          nvidia.com/gpu: "true"
        tolerations:
        - key: node-role.kubernetes.io/master
---
kind: NPUClusterPolicy
spec:
  detector:
    image: "ghcr.io/example/operator:v9"
  vendor:
    devicePluginImage: "ghcr.io/example/dp-vendor:v4"
    pinnedImage: "ghcr.io/example/pinned@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    mirroredImage: "<your-registry>/example/mirrored:v5"
---
kind: NPUClusterPolicy
spec:
  detector:
    image: "ghcr.io/example/second-doc:v6"
---
kind: DriverInstallPolicy
spec:
  driver:
    mode: job
    kernelImages:
      6.8.0-90-generic: "bare-local-image:580-6.8.0-90-generic"
      6.8.0-91-generic: "ghcr.io/example/kernel:580-6.8.0-91-generic"
      6.8.0-92-generic: ""
YAML
)"

got="$(printf '%s\n' "$render" | bash "$CHECK" --collect-only | sort)"

want="$(cat <<'TSV'
arg	ghcr.io/example/dp:v1
env	ghcr.io/example/sidecar:v2
image	ghcr.io/example/operator:v9
image	ghcr.io/example/operator:v9
image	ghcr.io/example/second-doc:v6
key	ghcr.io/example/dp-vendor:v4
key	ghcr.io/example/pinned@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
map	bare-local-image:580-6.8.0-90-generic
map	ghcr.io/example/kernel:580-6.8.0-91-generic
shape	docker.io/example/renamed:v3
TSV
)"

check "수집 분류와 자리 수가 그대로다" "$want" "$got"
check "자리 수" "10" "$(printf '%s\n' "$got" | grep -c .)"
check "고유 이미지 수" "9" "$(printf '%s\n' "$got" | cut -f2 | sort -u | grep -c .)"
check "자리표시자는 수집하지 않는다" "0" "$(printf '%s\n' "$got" | grep -c 'your-registry' || true)"
check "태그 없는 라벨 값은 수집하지 않는다" "0" "$(printf '%s\n' "$got" | grep -cE 'nvidia\.com/gpu|node-role' || true)"
# 문서 번호가 빠지면 두 NPUClusterPolicy 의 spec/detector/image 가 한 자리로 접혀 둘이 된다.
check "같은 kind 문서 둘이 한 자리로 접히지 않는다" "3" "$(printf '%s\n' "$got" | awk -F'\t' '$1=="image"' | grep -c .)"
check "이름에 image 가 있어도 태그 없는 값은 수집하지 않는다" "0" "$(printf '%s\n' "$got" | grep -c 'kube-system' || true)"
# kernelImages 는 키가 커널 버전이고 값에 레지스트리 호스트가 없을 수 있다 — 둘 다 받는다.
check "kernelImages 맵 값을 레지스트리 없이도 수집한다" "1" "$(printf '%s\n' "$got" | grep -c '^map	bare-local-image:')"
check "kernelImages 빈 값은 수집하지 않는다" "2" "$(printf '%s\n' "$got" | awk -F'\t' '$1=="map"' | grep -c .)"

echo "--- pass=${pass} fail=${fail}"
[ "$fail" -eq 0 ]
