#!/usr/bin/env bash
# ============================================================
# local-image-gc.sh: 빌드 호스트의 로컬 docker 이미지를 저장소별 최신 N개만 남기고 정리
# 상세: $DEV_REGISTRY 의 빌드 저장소별 최신 N개 태그만 남기고 나머지를 지운다. 안전·건너뜀
#       조건, 마지막 단계의 종료 코드 승계는 본문 주석과 -h 출력 참고.
# 생성일: 2026-09-23 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

keep=3
dry_run=0
while [ $# -gt 0 ]; do
  case "$1" in
    --keep)
      keep="${2:?--keep 값 필요}"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      echo "용법: $0 [--keep N(기본 3)] [--dry-run]"
      echo "  \$DEV_REGISTRY 의 kcloud 빌드 저장소에서 저장소별 최신 N개 태그만 남기고"
      echo "  나머지를 지운다. 라이브 파드·DriverInstallPolicy 가 참조하는 태그는 항상 보존한다."
      exit 0
      ;;
    *)
      echo "알 수 없는 인자: $1" >&2
      exit 2
      ;;
  esac
done

DOCKER="${DOCKER:-sudo -n docker}"
sudo -n true 2>/dev/null || DOCKER="docker"
read -ra docker_cmd <<< "$DOCKER"
docker_ok=1
"${docker_cmd[@]}" version >/dev/null 2>&1 || docker_ok=0

KUBECTL="${KUBECTL:-kubectl --context kubernetes-admin@cluster.local}"
read -ra kubectl_cmd <<< "$KUBECTL"

# 개발 레지스트리 주소는 환경변수로 받는다 — 공개 저장소에 내부 호스트를 박지 않는다.
DEV_REGISTRY="${DEV_REGISTRY:?DEV_REGISTRY 를 지정하십시오 (예: DEV_REGISTRY=registry.example:5100)}"
pattern="^$(printf '%s' "$DEV_REGISTRY" | sed 's/[.[\*^$]/\\&/g')/(kcloud/|nvidia-driver-ds|npu-operator|furiosa-driver-ds)"

if ! live_pods="$("${kubectl_cmd[@]}" get pods -A -o jsonpath='{range .items[*]}{range .spec.containers[*]}{.image}{"\n"}{end}{range .spec.initContainers[*]}{.image}{"\n"}{end}{end}' 2>&1)"; then
  echo "kubectl 로 라이브 파드 이미지 조회 실패 — 안전을 위해 삭제 없이 중단:" >&2
  echo "$live_pods" >&2
  exit 1
fi
if ! live_driver="$("${kubectl_cmd[@]}" get driverinstallpolicies.npu.ai -o jsonpath='{range .items[*]}{.spec.driver.image}{"\n"}{range .spec.driver.kernelImages.*}{@}{"\n"}{end}{end}' 2>&1)"; then
  echo "kubectl 로 DriverInstallPolicy 이미지 조회 실패 — 안전을 위해 삭제 없이 중단:" >&2
  echo "$live_driver" >&2
  exit 1
fi
live_refs="$(printf '%s\n%s\n' "$live_pods" "$live_driver" | grep -v '^$' | sort -u)"

repos=""
if [ "$docker_ok" = 1 ]; then
  repos="$("${docker_cmd[@]}" images --format '{{.Repository}}' | grep -E "$pattern" | sort -u || true)"
fi

to_delete=()
while IFS= read -r repo; do
  [ -n "$repo" ] || continue
  tags="$("${docker_cmd[@]}" images --format '{{.CreatedAt}}\t{{.Repository}}:{{.Tag}}' "$repo" | sort -r)"
  n=0
  while IFS=$'\t' read -r _created ref; do
    [ -n "$ref" ] || continue
    n=$((n + 1))
    [ "$n" -le "$keep" ] && continue
    grep -qxF "$ref" <<< "$live_refs" && continue
    to_delete+=("$ref")
  done <<< "$tags"
done <<< "$repos"

echo "보존 목록(라이브 참조):"
if [ -n "$live_refs" ]; then
  echo "  ${live_refs//$'\n'/$'\n  '}"
else
  echo "  (없음)"
fi

if [ "${#to_delete[@]}" -eq 0 ]; then
  echo "삭제 후보 없음(저장소별 최신 ${keep}개 이하이거나 전부 라이브 참조)"
else
  echo "삭제 후보(저장소별 최신 ${keep}개 제외, 라이브 참조 제외):"
  printf '  %s\n' "${to_delete[@]}"
fi

if [ "$dry_run" != 1 ]; then
  if [ "${#to_delete[@]}" -gt 0 ]; then
    "${docker_cmd[@]}" rmi "${to_delete[@]}" || echo "일부 이미지 삭제 실패 — 계속 진행" >&2
  fi
  if [ "$docker_ok" = 1 ]; then
    "${docker_cmd[@]}" builder prune -a -f
    "${docker_cmd[@]}" image prune -f
  else
    echo "docker 사용 불가 — builder/image prune 건너뜀" >&2
  fi
fi

# --dry-run 이든 아니든 마지막은 항상 이 호출 — 종료 코드도 여기서 그대로 이어받는다.
exec "$here/build-host-disk-check.sh"
