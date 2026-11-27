#!/usr/bin/env bash
# ============================================================
# image-tag-check.sh: 차트가 당기는 이미지 태그가 레지스트리에 실재하는지 확인
# 상세: helm 렌더에서 수집한 이미지 참조마다 레지스트리에 매니페스트 HEAD 를 물어 실제로
#       발행됐는지 본다(태그 문자열 형식만 보는 기존 검사와 다르다). 수집·분류·예외·커널
#       이미지 별도 집계·조회실패 임계치의 근거는 각 구간 본문 주석 참고.
# 용법: hack/image-tag-check.sh [--collect-only|--kernel-refs]
# 생성일: 2026-10-04 | 수정일: 2026-10-15
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."
. hack/registry-head.sh

chart=deploy/helm
values=$chart/values.yaml

# 수집기: 렌더(stdin)에서 "<분류><TAB><참조>" 를 자리마다 한 줄씩 낸다.
# snap yq 는 /tmp 를 읽지 못한다. YAML 파싱은 python3 PyYAML 로 한다.
collect() {
  python3 -c '
import sys, re, yaml

# <호스트>[:<포트>]/<경로>:<태그> 또는 <경로>@sha256:... — 호스트에 점이나 포트가 있어야
# 레지스트리로 본다(nvidia.com/gpu 같은 라벨 값이 끼지 않게).
REF = r"(:[A-Za-z0-9][A-Za-z0-9._-]*|@sha256:[0-9a-f]{64})$"
SHAPE = re.compile(r"^[a-z0-9][a-z0-9.:-]*/[a-z0-9][a-z0-9._/-]*" + REF)
# 이름 신호로 잡힌 자리는 레지스트리 호스트가 없어도 받는다(nginx:1.25 꼴). 다만 태그나
# digest 는 있어야 한다 — 그래야 NOT_AN_IMAGE=kube-system 처럼 이름만 걸린 자리가 빠진다.
LOOSE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]*" + REF)
PLACEHOLDER = re.compile(r"[<>${}]")

def shaped(v):
    if not SHAPE.match(v):
        return False
    host = v.split("/")[0]
    return "." in host or ":" in host

# kernelImages 처럼 맵을 담는 복수형 이름도 같은 신호로 본다.
def named(k):
    return re.search(r"(?i)images?$", k) is not None

# 같은 자리를 두 신호가 함께 잡으면 구체적인 분류를 남긴다(숫자 작은 쪽이 우선).
PRIO = {"image": 0, "key": 1, "map": 2, "env": 3, "arg": 4, "shape": 5}
sites = {}

def record(path, cls, ref):
    ref = ref.strip()
    if not ref or PLACEHOLDER.search(ref):
        return
    cur = sites.get(path)
    if cur is None or PRIO[cls] < PRIO[cur[0]]:
        sites[path] = (cls, ref)

def walk(node, path):
    if isinstance(node, dict):
        name, value = node.get("name"), node.get("value")
        if isinstance(name, str) and isinstance(value, str) and re.search(r"(?i)image", name) \
                and LOOSE.match(value):
            record(path + "/value", "env", value)
        for k, v in node.items():
            k = str(k)
            if isinstance(v, str):
                if named(k) and LOOSE.match(v):
                    record(path + "/" + k, "image" if k == "image" else "key", v)
                elif shaped(v):
                    record(path + "/" + k, "shape", v)
            elif isinstance(v, dict) and named(k):
                # kernelImages 는 <커널 버전>: <이미지> 맵이다. 키가 커널 버전이라 이름
                # 신호가 없고, 값도 레지스트리 호스트 없이 적을 수 있어(CRD 가 허용한다)
                # 값 모양 신호에도 걸리지 않는다. 맵 이름으로 받는다.
                for mk, mv in v.items():
                    if isinstance(mv, str) and LOOSE.match(mv):
                        record("%s/%s/%s" % (path, k, mk), "map", mv)
            walk(v, path + "/" + k)
    elif isinstance(node, list):
        for i, v in enumerate(node):
            p = "%s/%d" % (path, i)
            if isinstance(v, str):
                flag, _, rhs = v.partition("=")
                if rhs and LOOSE.match(rhs) and (re.search(r"(?i)image", flag) or shaped(rhs)):
                    record(p, "arg", rhs)
                elif shaped(v):
                    record(p, "shape", v)
            walk(v, p)

# 자리 식별에 문서 번호를 넣는다. 같은 kind 가 여러 번 나오면 경로 문자열이 겹쳐
# 서로 다른 자리가 하나로 접힌다(Job 셋, DriverInstallPolicy 넷).
for i, doc in enumerate(yaml.safe_load_all(sys.stdin.read())):
    if doc:
        walk(doc, "%d/%s" % (i, doc.get("kind", "?") if isinstance(doc, dict) else "?"))

for cls, ref in sites.values():
    print("%s\t%s" % (cls, ref))
'
}

# helm stderr 는 버리지 않고 모아 둔다. 렌더가 깨진 것과 모을 이미지가 없는 것은 다르다.
renderlog="$(mktemp)"
trap 'rm -f "$renderlog"' EXIT
render() { helm template kcloud "$chart" "$@" 2>"$renderlog"; }

# 수집기만 돌린다. 레지스트리에 닿지 않으므로 hack/image-tag-check_test.sh 가 이 경로로
# 분류·중복 제거·자리표시자 걸러내기를 고정한다.
if [ "${1:-}" = "--collect-only" ]; then
  collect
  exit 0
fi

# 커널별 사전 컴파일 이미지의 대상 목록. 차트 렌더에는 없다 — 조립은 노드 커널을 받아
# 런타임에 일어나므로 렌더에 그 태그가 적히지 않는다. 그래서 목록을 따로 읽는다.
kernel_refs() { bash hack/kernel-image-targets.sh --refs; }

# 조회 없이 "무엇을 물을지" 만 낸다. internal/upgrade/kernel_image_targets_test.go 가
# 이 경로로 목록을 보는 분기를 고정한다 — 그 분기가 사라지면 이 모드도 사라진다.
if [ "${1:-}" = "--kernel-refs" ]; then
  kernel_refs
  exit 0
fi

# exporter 는 기본값이 꺼짐이지만 1.31~1.34 프리셋이 켠다. 켜서 렌더해야 발행 전에 그 이미지도 조회한다.
if ! default_render="$(render --set furiosa.exporter.enabled=true)"; then
  echo "!! 차트 기본값(exporter 포함) 렌더가 실패했다: $(head -1 "$renderlog")" >&2
  exit 1
fi
default_rows="$(printf '%s\n' "$default_render" | collect | sort)"
[ -n "$default_rows" ] || { echo "!! 차트 기본값 렌더에서 이미지를 하나도 못 모았다 — 수집기 확인" >&2; exit 1; }

label() {
  case "$1" in
    image) echo "image: 필드          " ;;
    key)   echo "*Image 키            " ;;
    map)   echo "*Images 맵 값        " ;;
    arg)   echo "커맨드 인자          " ;;
    env)   echo "환경변수             " ;;
    shape) echo "그 밖(값 모양만 일치)" ;;
  esac
}

echo ">> 차트 기본값(exporter 포함) 렌더 — 참조 자리 $(printf '%s\n' "$default_rows" | grep -c .)개, 고유 이미지 $(printf '%s\n' "$default_rows" | cut -f2 | sort -u | grep -c .)개"
for cls in image key map arg env shape; do
  rows="$(printf '%s\n' "$default_rows" | awk -F'\t' -v c="$cls" '$1==c')"
  n="$(printf '%s' "$rows" | grep -c . || true)"
  [ "$n" -gt 0 ] || continue
  echo "   $(label "$cls") 자리 ${n}개 / 고유 $(printf '%s\n' "$rows" | cut -f2 | sort -u | grep -c .)개"
done

# 차트가 직접 발행하는 접두. 이 아래는 익명 조회가 되어야 공개 경로 설치가 성립한다.
own="$(python3 -c '
import sys, yaml
print((((yaml.safe_load(open(sys.argv[1])) or {}).get("global") or {}).get("registry") or "").strip("/"))
' "$values")"

published=0; missing=0; skipped=0; allowed=0
# 건너뜀 둘을 가른다 — unresolved 는 다시 돌리면 달라질 수 있는 조회 실패(자격 증명·요율
# 제한·무응답)이고, unqueryable 은 레지스트리 주소가 없어 물을 곳 자체가 없는 자리다.
unresolved=0; unqueryable=0
miss_list=""; skip_list=""; allow_list=""

while read -r ref; do
  [ -n "$ref" ] || continue
  host="${ref%%/*}"
  rest="${ref#*/}"
  if [ "$host" = "$ref" ] || { [ "${host#*.}" = "$host" ] && [ "${host#*:}" = "$host" ]; }; then
    skipped=$((skipped + 1)); unqueryable=$((unqueryable + 1))
    skip_list="${skip_list}  - ${ref} (레지스트리 주소가 없어 어디에 물을지 알 수 없다)
"
    continue
  fi
  case "$rest" in
    *@sha256:*) repo="${rest%@*}"; tag="sha256:${rest##*@sha256:}" ;;
    *) repo="${rest%:*}"; tag="${rest##*:}" ;;
  esac
  read -r code _ <<<"$(head_manifest_retry "$host" "$repo" "$tag")"
  case "$code" in
    200) published=$((published + 1)) ;;
    404)
      missing=$((missing + 1))
      miss_list="${miss_list}  - ${ref} (레지스트리가 그 태그는 없다고 답했다)
"
      ;;
    401|403)
      if [ -n "$own" ] && [ "${ref#"$own"/}" != "$ref" ]; then
        bare="${ref#"$own"/}"
        reason="${ALLOWED_PRIVATE[$bare]:-}"
        if [ -n "$reason" ]; then
          allowed=$((allowed + 1))
          allow_list="${allow_list}  - ${ref} (허용된 비공개: ${reason})
"
        else
          missing=$((missing + 1))
          miss_list="${miss_list}  - ${ref} (익명 조회 거부 ${code} — 공개 경로로 당길 수 없다)
"
        fi
      else
        skipped=$((skipped + 1)); unresolved=$((unresolved + 1))
        skip_list="${skip_list}  - ${ref} (자격 증명이 필요하다: ${code})
"
      fi
      ;;
    *)
      skipped=$((skipped + 1)); unresolved=$((unresolved + 1))
      skip_list="${skip_list}  - ${ref} (조회 못 함: ${code:-응답 없음})
"
      ;;
  esac
done <<< "$(printf '%s\n' "$default_rows" | cut -f2 | sort -u)"

echo ">> 조회 — 발행 ${published}건, 미발행 ${missing}건, 건너뜀 ${skipped}건(조회 실패 ${unresolved}, 주소 없음 ${unqueryable}), 허용된 비공개 ${allowed}건"
[ -n "$miss_list" ] && { echo "미발행(설치하면 당기지 못한다):"; printf '%s' "$miss_list"; }
[ -n "$skip_list" ] && { echo "건너뛴 항목(발행 여부를 확인하지 못했다):"; printf '%s' "$skip_list"; }
[ -n "$allow_list" ] && { echo "허용된 비공개 항목(결함 아님):"; printf '%s' "$allow_list"; }

# ---------------------------------------------------------------- 커널 이미지
# 위 조회·분류 규칙은 같다(200 발행 / 404 미발행 / 그 밖 조회 못 함). 미발행을 missing 에
# 합치지 않는 이유는 결함이 아니라 아직 굽지 않은 이미지라서다 — 합치면 평시에 빨강이 된다.
# release-prepare.sh 가 세 수를 다 읽는다 — 발행 수를 빼면 "조회 못 함" 이 "다 있다" 로
# 읽힌다. 묶음 태그로는 못 굽지만(blocked_bundle) images 저장소 workflow_dispatch 로
# 일부는 항목별 수동 발행이 가능하다. 그 결과가 태그와 맞는지는 아직 검사하지 않는다.
kernel_rows="$(kernel_refs | sort -u)"
kernel_n="$(printf '%s\n' "$kernel_rows" | grep -c . || true)"
echo
if [ "$kernel_n" -eq 0 ]; then
  echo ">> 커널 이미지 발행 대상 0건 — deploy/kernel-image-targets.yaml 의 대상이 비어 있다(조회할 것이 없다)"
else
  # 확답을 못 받은 자리를 둘로 가른다 — 위 기본 렌더 조회가 unresolved·unqueryable 로
  # 가른 것과 같은 구분이다. 주소가 없어 애초에 물을 수 없는 자리는 일시적 장애가 아니므로
  # 아래 비율 기준에서 빠진다.
  kpub=0; kmiss=0; kunres=0; kunq=0; kmiss_list=""
  while read -r ref; do
    [ -n "$ref" ] || continue
    host="${ref%%/*}"; rest="${ref#*/}"
    # 레지스트리 주소가 없으면 어디에 물을지 알 수 없다(global.registry 가 빈 차트).
    if [ "$host" = "$ref" ] || { [ "${host#*.}" = "$host" ] && [ "${host#*:}" = "$host" ]; }; then
      kunq=$((kunq + 1)); continue
    fi
    repo="${rest%:*}"; tag="${rest##*:}"
    read -r code _ <<<"$(head_manifest_retry "$host" "$repo" "$tag")"
    case "$code" in
      200) kpub=$((kpub + 1)) ;;
      404)
        kmiss=$((kmiss + 1))
        kmiss_list="${kmiss_list}  - ${ref}
"
        ;;
      *) kunres=$((kunres + 1)) ;;
    esac
  done <<< "$kernel_rows"
  kskip=$((kunres + kunq))
  echo ">> 커널 이미지 — 대상 ${kernel_n}건 중 발행 ${kpub}건, 미발행 ${kmiss}건, 조회 못 함 ${kskip}건(조회 실패 ${kunres}, 주소 없음 ${kunq})"
  [ -n "$kmiss_list" ] && {
    echo "커널 이미지 미발행(결함 아님 — 아직 굽지 않았다. 묶음 태그 push 로는 해소되지 않는다):"
    printf '%s' "$kmiss_list"
  }
  # 조회 실패가 넷 중 하나를 넘으면 이 구간은 발행 상태를 확인했다고 할 수 없다. 전부
  # 실패(kpub+kmiss==0)만 보면 9건 중 1건만 확답인 상태도 통과해 미발행 0건이 "다 있다"
  # 로 읽힌다. 분자·분모를 위 기본 렌더 조회와 같게 둔다(정책을 두 번 정의하지 않는다):
  # 분자는 조회 실패뿐이고 분모는 레지스트리가 확답을 줄 수 있는 자리(발행+미발행+조회
  # 실패)다. 주소가 없어 못 묻는 자리를 분모에 넣으면 빈 차트에서 이 구간이 통째로
  # 빨강이 된다. 커널 조회가 요청을 아홉 건 늘려 요율 제한이 실제로 일어날 수 있다.
  kqueryable=$((kpub + kmiss + kunres))
  if [ "$kunres" -gt 0 ] && [ $((kunres * 4)) -gt "$kqueryable" ]; then
    echo "!! 커널 이미지 조회 실패 ${kunres}건이 조회 대상 ${kqueryable}건의 4분의 1을 넘는다 — 발행 상태를 확인하지 못했다." >&2
    exit 1
  fi
fi
echo

# 다른 프리셋은 수집만 한다. 사설 미러·자리표시자를 가리켜 익명 조회가 성립하지 않는다.
preset_fail=0
for f in "$chart"/values-*.yaml; do
  [ -f "$f" ] || continue
  if ! out="$(render -f "$f")"; then
    preset_fail=$((preset_fail + 1))
    echo ">> $(basename "$f") — 렌더 실패(수집 못 함): $(head -1 "$renderlog")"
    continue
  fi
  rows="$(printf '%s\n' "$out" | collect | cut -f2 | sort -u || true)"
  n="$(printf '%s' "$rows" | grep -c . || true)"
  if [ "$n" -eq 0 ]; then
    echo ">> $(basename "$f") — 자리표시자를 걸러낸 뒤 남은 이미지 0개(조회 대상 아님)"
  else
    extra="$(comm -13 <(printf '%s\n' "$default_rows" | cut -f2 | sort -u) <(printf '%s\n' "$rows") | grep -c . || true)"
    echo ">> $(basename "$f") — 고유 ${n}개(기본값에 없는 것 ${extra}개), 조회 건너뜀: 사설 미러·자리표시자"
  fi
done

if [ "$preset_fail" -gt 0 ]; then
  echo "!! 프리셋 렌더 ${preset_fail}건이 실패했다 — 그 프리셋의 이미지는 수집조차 못 했다." >&2
  exit 1
fi
if [ "$missing" -gt 0 ]; then
  echo "!! 미발행 ${missing}건 — 그 태그를 발행하거나 차트가 가리키는 태그를 고쳐야 한다." >&2
  exit 1
fi
# 전부 건너뛰면 이 검사는 아무것도 확인하지 못한다. 초록으로 넘기지 않는다.
if [ "$published" -eq 0 ]; then
  echo "!! 발행을 확인한 항목이 0건이다 — 레지스트리에 닿지 못했다." >&2
  exit 1
fi
# 부분 장애도 초록이 되지 않게 한다. 기준: 레지스트리가 확답을 준 자리(발행+미발행) 대비
# 조회 실패가 4분의 1을 넘으면 확인한 것이 너무 적다고 본다. 레지스트리 주소가 없어
# 애초에 물을 수 없는 자리는 이 비율에서 뺀다 — 일시적 장애가 아니라 영구적 성격이다.
queryable=$((published + missing + unresolved))
if [ "$unresolved" -gt 0 ] && [ $((unresolved * 4)) -gt "$queryable" ]; then
  echo "!! 조회 실패 ${unresolved}건이 조회 대상 ${queryable}건의 4분의 1을 넘는다 — 확인한 것이 너무 적다." >&2
  exit 1
fi
echo ">> 기본값(exporter 포함) 설치가 당기는 이미지 ${published}건이 레지스트리에 있다 (건너뛴 ${skipped}건은 확인 못 함, 허용된 비공개 ${allowed}건)"
