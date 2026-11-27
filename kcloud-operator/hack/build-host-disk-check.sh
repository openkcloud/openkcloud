#!/usr/bin/env bash
# ============================================================
# build-host-disk-check.sh: 루트 파일시스템 여유율과 docker 이미지/빌드 캐시 사용량을 확인
# 상세: df -P / 마지막 줄의 Use% 로 freePercent = 100 - Use% 를 계산한다. docker 사용량은
#       sudo -n docker 로 먼저 시도하고 실패하면 plain docker, 그것도 실패하면
#       "docker: unavailable" 로 표시한다 — docker 조회 성공 여부는 종료 코드에 영향을 주지
#       않는다. 종료 코드는 오직 여유율이 --min-free-percent 미만인지로만 결정한다.
# 생성일: 2026-09-23 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

min_free=20
json=0
while [ $# -gt 0 ]; do
  case "$1" in
    --min-free-percent)
      min_free="${2:?--min-free-percent 값 필요}"
      shift 2
      ;;
    --json)
      json=1
      shift
      ;;
    -h|--help)
      echo "용법: $0 [--min-free-percent N(기본 20)] [--json]"
      echo "  루트 파일시스템 여유율과 docker 이미지/빌드 캐시 사용량을 확인한다."
      echo "  여유율이 N% 미만이면 WARN 을 stderr 에 내고 종료 코드 1."
      exit 0
      ;;
    *)
      echo "알 수 없는 인자: $1" >&2
      exit 2
      ;;
  esac
done

# df -P / 마지막 줄: Filesystem 1024-blocks Used Available Capacity Mounted-on
df_line="$(df -P / | tail -n1)"
read -r _ _ _ avail_field used_pct_raw _ <<< "$df_line"
used_percent="${used_pct_raw%\%}"
free_percent=$((100 - used_percent))

# avail 필드가 순수 숫자면 1024-바이트 블록(df -P 실사용), 뒤에 단위 글자가 붙으면
# 이미 그 단위 값(시험용 가짜 df 의 "374G" 형태)으로 보고 그대로 쓴다.
if [[ "$avail_field" =~ ^([0-9]+)([A-Za-z]*)$ ]]; then
  avail_num="${BASH_REMATCH[1]}"
  avail_unit="${BASH_REMATCH[2]}"
  if [ -z "$avail_unit" ]; then
    avail_gib=$((avail_num / 1024 / 1024))
  else
    avail_gib="$avail_num"
  fi
else
  avail_gib=0
fi

# docker: sudo -n docker 우선, sudo 비밀번호 없이 안 되면 plain docker 로 폴백
DOCKER="${DOCKER:-sudo -n docker}"
sudo -n true 2>/dev/null || DOCKER="docker"
read -ra docker_cmd <<< "$DOCKER"

docker_summary="unavailable"
docker_json="null"
if images_line="$("${docker_cmd[@]}" system df --format '{{.Type}} {{.TotalCount}} {{.Size}}' 2>/dev/null | grep '^Images ')"; then
  docker_summary="$images_line"
  read -r _ d_count d_size <<< "$images_line"
  docker_json="{\"images\":${d_count:-0},\"size\":\"${d_size:-}\"}"
fi

verdict="ok"
[ "$free_percent" -lt "$min_free" ] && verdict="warn"

if [ "$json" = 1 ]; then
  printf '{"usedPercent":%d,"freePercent":%d,"availGiB":%d,"docker":%s,"verdict":"%s"}\n' \
    "$used_percent" "$free_percent" "$avail_gib" "$docker_json" "$verdict"
else
  echo "root: used=${used_percent}% free=${free_percent}% avail=${avail_gib}G"
  echo "docker: ${docker_summary}"
fi

if [ "$free_percent" -lt "$min_free" ]; then
  echo "WARN: free ${free_percent}% < ${min_free}% — kubelet imagefs 임계(15%) 근접, hack/local-image-gc.sh 실행 권장" >&2
  exit 1
fi
