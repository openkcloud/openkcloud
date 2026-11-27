#!/usr/bin/env bash
# ============================================================
# registry-head.sh: 레지스트리 매니페스트 HEAD 조회 공유 함수 (source 해서 쓴다)
# 상세: refresh-image-digests.sh·image-tag-check.sh 가 같은 조회·판정 규칙(상태 코드를
#       그대로 돌려주고 해석은 호출부가 함 — 404=확답, 401/403/빈값=조회 못 함)을 쓰도록
#       한 곳에 둔다. ALLOWED_PRIVATE 도 같은 이유로 여기 둔다(한쪽에만 있으면 같은
#       이미지를 두 검사가 다르게 판정한다).
# 생성일: 2026-10-04 | 수정일: 2026-10-11
# ============================================================

# 발행 접두 아래지만 의도적으로 비공개인 이미지의 허용 목록. 항목마다 비공개인 이유를
# 적는다 — 전부 조건부 설정(acpp.probeImages.*)이나 테스트 하네스에서만 쓰여 기본 설치를
# 막지 않고, GHCR 발행 계획이 없다. 키는 "<image>:<tag>"(발행 접두를 뗀 bare 이름).
declare -A ALLOWED_PRIVATE=(
  ["probe-tenstorrent:v1"]="acpp.probeImages.tenstorrent 설정 시에만 쓰는 내부 빌드, 기본 설치엔 없다"
  ["probe-rebellions:v1"]="acpp.probeImages.rebellions 설정 시에만 쓰는 내부 빌드, 기본 설치엔 없다"
  ["rebellions/rbln-workload:ubuntu22.04-v1"]="test/live/rebellions-timeslice 하네스 전용 내부 빌드, 차트에 없다"
  ["furiosa-unified-device-plugin:0.1.0"]="벤더 플러그인을 그대로 쓰는 원칙으로 발행하지 않는 자체 구현, furiosa.unified.enabled=false 기본이라 기본 설치엔 없다"
)

# 멀티아치 index 도 받아야 docker pull 이 보는 digest 와 같은 값이 온다.
REGISTRY_HEAD_ACCEPT='application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'

# docker.io 의 v2 API 는 registry-1.docker.io 가 받는다. docker.io 로 물으면 웹사이트로 302 된다.
registry_api_host() {
  case "$1" in
    docker.io|index.docker.io) echo registry-1.docker.io ;;
    *) echo "$1" ;;
  esac
}

# head_manifest <host> <repo> <참조(태그 또는 digest)>
# "<상태코드> <digest>" 를 돌려준다. 응답이 없으면 둘 다 빈 값이다.
head_manifest() {
  local host repo ref url chal realm svc scope tok=""
  host="$(registry_api_host "$1")"; repo="$2"; ref="$3"
  url="https://${host}/v2/${repo}/manifests/${ref}"
  # 토큰은 WWW-Authenticate 챌린지의 realm 에서 받는다(GHCR 은 realm 에 scope 가 이미
  # 있고, nvcr.io 는 /proxy_auth 형태라 realm 을 꼭 읽어야 한다).
  chal="$(curl -sSL -m 20 -I "$url" 2>/dev/null | tr -d '\r' | grep -i '^www-authenticate:' | tail -1 || true)"
  realm="$(sed -nE 's/.*realm="([^"]*)".*/\1/p' <<<"$chal")"
  if [ -n "$realm" ]; then
    svc="$(sed -nE 's/.*service="([^"]*)".*/\1/p' <<<"$chal")"
    scope="$(sed -nE 's/.*scope="([^"]*)".*/\1/p' <<<"$chal")"
    tok="$(curl -sS -m 20 "${realm}?service=${svc}&scope=${scope:-repository:${repo}:pull}" 2>/dev/null |
      python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("token") or d.get("access_token") or "")' 2>/dev/null || true)"
  fi
  # -L 로 리다이렉트를 따라간다 — registry.k8s.io 는 지역 미러로 307 을 낸다.
  curl -sSL -m 20 -o /dev/null -D - ${tok:+-H "Authorization: Bearer ${tok}"} -H "Accept: ${REGISTRY_HEAD_ACCEPT}" "$url" 2>/dev/null |
    tr -d '\r' |
    awk 'tolower($1) ~ /^http\//{c=$2} tolower($1)=="docker-content-digest:"{d=$2} END{print c, d}'
}

# head_manifest 를 한 번 재시도한다. 다시 물으면 달라질 수 있는 응답만 대상이다 —
# 무응답, 429(요율 제한), 5xx 다. 404 와 401·403 은 레지스트리의 확답이라 그대로 쓴다.
head_manifest_retry() {
  local out code
  out="$(head_manifest "$@")"
  read -r code _ <<<"$out"
  case "${code:-}" in
    ""|429|5[0-9][0-9]) sleep 2; out="$(head_manifest "$@")" ;;
  esac
  printf '%s' "$out"
}
