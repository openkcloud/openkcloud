#!/usr/bin/env bash
# ============================================================
# check.sh: air-gap 배포 values 로 helm 렌더 후 모든 이미지 참조가 단일 미러 prefix 인지 검사
# 상세: deploy/helm 을 air-gap values 로 오프라인 helm template 렌더해(설치 없음) 이미지 참조를
#       전부 뽑고 --registry 미러 prefix 로 시작하는지 검사한다. 자세한 사용법은 README.md.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

DRY_RUN="${DRY_RUN:-0}"
CHART="${CHART:-$REPO_ROOT/deploy/helm}"
VALUES="${VALUES:-$REPO_ROOT/deploy/helm/values-airgap.example.yaml}"
RELEASE="${RELEASE:-kc}"

# --registry <mirror> 로 미러 prefix 지정(env REGISTRY 도 가능). 미지정이면 values 파일의
# global.registry 값을 기본으로 쓴다(플레이스홀더 <your-registry>).
REGISTRY="${REGISTRY:-}"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --registry) REGISTRY="${2:-}"; shift 2 ;;
    --registry=*) REGISTRY="${1#--registry=}"; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

OUT="${OUT:-/tmp/airgap-check-evidence}"
mkdir -p "$OUT"

log() { printf '%s\n' "$*" >&2; }
utc_now() { date -u +%FT%TZ; }

# values 파일의 global.registry 기본값(플레이스홀더)을 읽는다.
default_registry() {
  sed -nE 's/^[[:space:]]*registry:[[:space:]]*"?([^"#[:space:]]+)"?.*/\1/p' "$VALUES" | head -1
}
[ -n "$REGISTRY" ] || REGISTRY="$(default_registry)"

# 렌더 결과에서 이미지 참조를 뽑는다.
#  - image:/…Image:/--…-image= 값은 모양과 무관하게 전부 채택한다 — 태그·레지스트리 경로가
#    없는 참조도 air-gap 에서는 미러로 끌어와야 하며, 버리면 그 파드만 ImagePullBackOff 가
#    나는데 검사는 통과하는 결함이 된다.
#  - 일반 value: 줄은 이미지가 아닌 값도 많으므로 이미지 모양(HOST/PATH:TAG)인 것만 남긴다.
extract_images() {
  local f="$1"
  {
    sed -E -n -e 's/#.*$//' \
      -e 's/.*-image=[[:space:]]*"?([^[:space:]"]+)"?.*/\1/p' "$f"
    sed -E -n -e 's/#.*$//' \
      -e 's/.*[Ii]mage:[[:space:]]*"?([^[:space:]"]+)"?.*/\1/p' "$f"
    sed -E -n -e 's/#.*$//' \
      -e 's/.*[[:space:]]value:[[:space:]]*"?([^[:space:]"]+)"?.*/\1/p' "$f" \
      | grep -E '^[^[:space:]/]+/[^[:space:]]*:[^[:space:]]+$'
  } | sed '/^$/d' | sort -u
}

verdict="FAIL"; dry="false"; TOTAL=0; OFFENDERS=0

finish() {
  printf '{"test":"airgap-image-refs","registry":"%s","images":%s,"offenders":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$REGISTRY" "$TOTAL" "$OFFENDERS" "$verdict" "$dry" "$(utc_now)" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "PASS" ] || [ "$verdict" = "DRY" ] || exit 1
  exit 0
}
trap finish EXIT

log "### air-gap 이미지 참조 검사 (chart=$CHART registry=$REGISTRY)"

if [ "$DRY_RUN" = "1" ]; then
  log "+ sed 's#<your-registry>#$REGISTRY#g' $VALUES > <tmp-values>"
  log "+ helm template $RELEASE $CHART -f <tmp-values>"
  log "  이후 image:/…Image:/value:/--…-image= 값을 뽑아 전부 '$REGISTRY/' 로 시작하는지 검사"
  verdict="DRY"; dry="true"
  exit 0
fi

if ! command -v helm >/dev/null 2>&1; then
  log "BLOCKED: helm 없음 — 렌더 불가"
  verdict="BLOCKED"
  exit 1
fi

# 미러 prefix 로 치환한 임시 values(기본 플레이스홀더면 그대로). 렌더는 오프라인.
TMP_VALUES="$OUT/values-rendered-$(date +%H%M%S).yaml"
sed "s#<your-registry>#${REGISTRY}#g" "$VALUES" > "$TMP_VALUES"

RENDER="$OUT/render-$(date +%H%M%S).yaml"
if ! helm template "$RELEASE" "$CHART" -f "$TMP_VALUES" > "$RENDER" 2>"$OUT/helm-err.txt"; then
  log "FAIL: helm template 실패 — $OUT/helm-err.txt 참조"
  head -20 "$OUT/helm-err.txt" >&2 || true
  verdict="FAIL"
  exit 1
fi

# 이미지 목록.
mapfile -t IMAGES < <(extract_images "$RENDER")
TOTAL="${#IMAGES[@]}"
log "이미지 참조 ${TOTAL}개 추출"

if [ "$TOTAL" -eq 0 ]; then
  log "FAIL: 렌더에서 이미지 참조를 하나도 못 찾음 — 추출기·values 확인"
  verdict="FAIL"
  exit 1
fi

# prefix 검사.
OFFENDER_LIST=""
for ref in "${IMAGES[@]}"; do
  case "$ref" in
    "$REGISTRY"/*) : ;;                          # 미러 prefix — 정상
    *) OFFENDERS=$((OFFENDERS + 1)); OFFENDER_LIST="${OFFENDER_LIST}\n  - $ref" ;;
  esac
done

# 개발 레지스트리 누수는 별도로도 알린다(prefix 검사에 이미 잡히지만 명시).
# 주소를 소스에 박지 않는다 — 공개 저장소에 내부 호스트가 실리면 안 된다.
DEV_REGISTRY="${DEV_REGISTRY:-}"
if [ -n "$DEV_REGISTRY" ] && grep -qF "$DEV_REGISTRY" "$RENDER"; then
  log "경고: 렌더에 개발 레지스트리 $DEV_REGISTRY 문자열이 있다 — air-gap values 에서 새면 안 된다"
fi

if [ "$OFFENDERS" -gt 0 ]; then
  log "FAIL: 미러 prefix '$REGISTRY/' 밖 이미지 ${OFFENDERS}개:"
  printf '%b\n' "$OFFENDER_LIST" >&2
  verdict="FAIL"
  exit 1
fi

log "PASS: 이미지 ${TOTAL}개 전부 '$REGISTRY/' prefix"
verdict="PASS"
exit 0
