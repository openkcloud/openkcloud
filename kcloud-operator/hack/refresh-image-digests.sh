#!/usr/bin/env bash
# ============================================================
# refresh-image-digests.sh: 발행 뒤 image-manifest.yaml 의 digest 를 레지스트리 조회로 갱신
# 상세: deploy/integration/image-manifest.yaml 의 kcloud: 절 중 digest 키가 있는 항목마다
#       레지스트리에 물어 digest 를 갱신한다(조회만, push 없음). 판정 근거(404 확답·
#       401/403 예외·KCLOUD_REGISTRY 사설 미러 구분)는 본문 주석 참고.
# 용법: [KCLOUD_REGISTRY=<host>[:<port>]/<project>] hack/refresh-image-digests.sh [--check]
# 생성일: 2026-10-03 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."
. hack/registry-head.sh

check=0
case "${1:-}" in
  --check) check=1 ;;
  "") ;;
  *) echo "용법: $0 [--check]" >&2; exit 2 ;;
esac

manifest=deploy/integration/image-manifest.yaml
values=deploy/helm/values.yaml

# snap yq 는 /tmp 를 읽지 못한다. YAML 파싱은 python3 PyYAML 로 한다.
chart_registry="$(python3 -c '
import sys, yaml
print((((yaml.safe_load(open(sys.argv[1])) or {}).get("global") or {}).get("registry") or "").strip("/"))
' "$values")"
registry="${KCLOUD_REGISTRY:-$chart_registry}"
[ -n "$registry" ] || { echo "!! 레지스트리를 알 수 없다 (${values} global.registry 가 비었다)." >&2; exit 1; }
host="${registry%%/*}"
prefix="${registry#*/}"
[ "$prefix" = "$registry" ] && prefix=""
# KCLOUD_REGISTRY 로 사설 미러를 보는 중이면 거기서는 401 이 정상(자격 증명 필요)이다 —
# "발행 접두 아래 401 은 결함" 규칙은 차트가 실제로 발행하는 레지스트리(chart_registry)를
# 볼 때만 쓴다. image-tag-check.sh 의 own 접두 대조와 같은 가드다.
own_registry=0
[ "$registry" = "$chart_registry" ] && own_registry=1

entries="$(python3 -c '
import sys, yaml
d = yaml.safe_load(open(sys.argv[1])) or {}
for e in (d.get("kcloud") or []):
    e = e or {}
    if "digest" not in e:
        continue
    print("%s\t%s\t%s" % (e.get("image", ""), e.get("tag", ""), e.get("digest") or ""))
' "$manifest")"
[ -n "$entries" ] || { echo "!! ${manifest} kcloud: 절에 digest 항목이 없다." >&2; exit 1; }

# own_registry 일 때만 — 차트가 실제로 발행하는 레지스트리 아래서는 익명 조회
# 거부(401·403)가 image-tag-check.sh 의 "자기 발행 접두 아래" 예외와 같은 뜻이다 — 공개
# 경로로 당길 수 없다는 확답이라 결함(어긋남)으로 센다. ALLOWED_PRIVATE(hack/registry-head.sh
# 에서 source, image-tag-check.sh 와 공유)에 있는 항목만 예외로 건너뛴다.
updates=""   # 갱신할 "<이미지>\t<digest>" 줄
ok=0; mismatch=0; stale=0; allowed=0
mis_list=""; skip_list=""; allow_list=""

while IFS=$'\t' read -r name tag recorded; do
  [ -n "$name" ] || continue
  read -r code got <<<"$(head_manifest "$host" "${prefix:+${prefix}/}${name}" "$tag")"
  case "$code" in
    200)
      if [ -z "$got" ]; then
        skip_list="${skip_list}  - ${name}:${tag} (digest 헤더 없음)
"
      elif [ "$got" = "$recorded" ]; then
        ok=$((ok + 1))
      else
        mismatch=$((mismatch + 1))
        mis_list="${mis_list}  - ${name}:${tag} 기록 ${recorded:-(없음)} -> 레지스트리 ${got}
"
        updates="${updates}${name}	${got}
"
      fi
      ;;
    404)
      # 레지스트리의 확답이다. 기록도 비어 있으면 미발행 사실이 그대로 적힌 것이라 맞는다.
      if [ -z "$recorded" ]; then
        ok=$((ok + 1))
      else
        mismatch=$((mismatch + 1)); stale=$((stale + 1))
        mis_list="${mis_list}  - ${name}:${tag} 기록 ${recorded} 인데 레지스트리에 그 태그 발행본이 없다
"
      fi
      ;;
    401|403)
      if [ "$own_registry" -ne 1 ]; then
        # 사설 미러를 보는 중이면 401 은 자격 증명이 필요하다는 뜻일 뿐, 그 이미지가
        # 미발행이라는 확답이 아니다 — 결함으로 세지 않는다.
        skip_list="${skip_list}  - ${name}:${tag} (조회 못 함: ${code})
"
        continue
      fi
      reason="${ALLOWED_PRIVATE[${name}:${tag}]:-}"
      if [ -n "$reason" ]; then
        allowed=$((allowed + 1))
        allow_list="${allow_list}  - ${name}:${tag} (허용된 비공개: ${reason})
"
      else
        mismatch=$((mismatch + 1)); stale=$((stale + 1))
        # "발행본이 없다" 문구를 피한다 — release-prepare.sh 의 pend 분류(그 문구를 찾아
        # note 로 눅인다)를 타면 image-manifest 에만 있는 이미지의 401 이 어디서도 hard
        # FAIL 이 되지 않는다. image-tag-check.sh 와 같은 문구("공개 경로로 당길 수
        # 없다")를 써서 늘 어긋남 그대로 bad 로 분류되게 한다.
        mis_list="${mis_list}  - ${name}:${tag} 익명 조회 거부(${code}) — 발행 접두 아래라 공개 경로로 당길 수 없다
"
      fi
      ;;
    *)
      # 빈 값·429·5xx 는 응답 자체가 없거나 다시 물으면 달라질 수 있는 경우다.
      skip_list="${skip_list}  - ${name}:${tag} (조회 못 함: ${code:-응답 없음})
"
      ;;
  esac
done <<< "$entries"

skipped="$(printf '%s' "$skip_list" | grep -c . || true)"
echo ">> ${registry} 기준 — 일치 ${ok}건, 어긋남 ${mismatch}건, 건너뜀 ${skipped}건, 허용된 비공개 ${allowed}건"
[ -n "$mis_list" ] && { echo "어긋난 항목:"; printf '%s' "$mis_list"; }
[ -n "$skip_list" ] && { echo "건너뛴 항목(파일 미변경):"; printf '%s' "$skip_list"; }
[ -n "$allow_list" ] && { echo "허용된 비공개 항목(결함 아님):"; printf '%s' "$allow_list"; }

if [ "$check" -eq 1 ]; then
  [ "$mismatch" -eq 0 ] || exit 1
  # 전부 건너뛰면 이 검사는 아무것도 확인하지 못한다. 초록으로 넘기지 않는다.
  if [ "$ok" -eq 0 ]; then
    echo "!! 확인한 항목이 0건이다. 레지스트리에 닿지 못했거나 전부 자격 증명이 필요하다." >&2
    exit 1
  fi
  echo ">> digest ${ok}건이 레지스트리와 맞는다 (건너뛴 ${skipped}건은 확인 못 함, 허용된 비공개 ${allowed}건)"
  exit 0
fi

if [ -n "$updates" ]; then
  # 주석·순서를 보존해야 하므로 YAML 을 다시 쓰지 않고 digest 줄만 바꾼다.
  printf '%s' "$updates" | python3 -c '
import re, sys
path = sys.argv[1]
upd = dict(l.rstrip("\n").split("\t", 1) for l in sys.stdin if l.strip())
out, sect, cur = [], None, None
for line in open(path).read().splitlines(True):
    top = re.match(r"([A-Za-z][A-Za-z0-9_]*):", line)
    if top:
        sect, cur = top.group(1), None
    elif sect == "kcloud":
        img = re.match(r"\s*-\s*image:\s*(\S+)", line)
        if img:
            cur = img.group(1)
        elif cur in upd:
            m = re.match(r"(\s*digest:\s*)(\S*)(.*)", line)
            if m:
                line = "%s%s%s\n" % (m.group(1), upd.pop(cur), m.group(3).rstrip("\n"))
    out.append(line)
if upd:
    sys.exit("!! digest 줄을 찾지 못한 항목: %s" % ", ".join(sorted(upd)))
open(path, "w").write("".join(out))
' "$manifest"
  echo ">> ${manifest} 의 digest $(printf '%s' "$updates" | grep -c .)건 갱신 (push 하지 않는다)"
fi

if [ "$stale" -gt 0 ]; then
  echo "!! 발행본이 없어 갱신할 수 없는 항목 ${stale}건 남았다. 그 태그를 발행해야 한다." >&2
  exit 1
fi
