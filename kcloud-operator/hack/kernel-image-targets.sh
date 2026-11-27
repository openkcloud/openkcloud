#!/usr/bin/env bash
# ============================================================
# kernel-image-targets.sh: 커널별 사전 컴파일 이미지의 발행 대상을 유도한다
# 상세: deploy/kernel-image-targets.yaml 의 커널·벤더 목록과 deploy/helm/values.yaml 의
#       버전·variant·저장소 경로를 합쳐 조합마다 전체 이미지 참조를 만든다(태그 규약은
#       internal/upgrade 의 assembleKernelImage 와 동일). 이 목록이 이 저장소에 있는 이유·
#       대상 0건의 의미는 derive()·--bundle-name 분기·deploy/kernel-image-targets.yaml 참고.
# 생성일: 2026-10-05 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."

targets="${KERNEL_IMAGE_TARGETS:-deploy/kernel-image-targets.yaml}"
values=deploy/helm/values.yaml
# 이 목록이 operator 저장소에 있는 이유: 이를 읽는 게이트(hack/image-tag-check.sh)가
# 여기 있고, CI 에는 형제 images 저장소가 없다.
# images 저장소 release-bundle.yaml 에서 이 묶음 하나만 본다. 다른 묶음은 이 검사 대상이 아니다.
bundle_name=kernel-images

[ -f "$targets" ] || { echo "!! 발행 대상 목록이 없다 — ${targets}" >&2; exit 1; }
[ -f "$values" ] || { echo "!! ${values} 가 없다" >&2; exit 1; }

# 대상이 0건(커널 목록이나 targets 가 비었으면)이면 아무 줄도 내지 않는다 — 호출부가
# 조회·대조를 건너뛴다. 이것은 사전 컴파일 경로를 끄는 수단이 아니다(그 스위치는 차트의
# driver.kernelImageBuildRevision, 네 벤더 모두 기본 빈 값).
# "<images.yaml 키><TAB><태그><TAB><전체 참조><TAB><공개 묶음 여부>" 를 조합마다 한 줄.
# snap yq 는 /tmp 를 읽지 못한다. YAML 파싱은 python3 PyYAML 로 한다.
derive() {
  python3 - "$targets" "$values" <<'PY'
import sys, yaml

t = yaml.safe_load(open(sys.argv[1])) or {}
v = yaml.safe_load(open(sys.argv[2])) or {}
registry = ((v.get("global") or {}).get("registry") or "").strip("/")
policies = v.get("driverInstallPolicies") or {}

kernels = [k for k in (t.get("kernels") or []) if k]
rows = []
for vendor, spec in sorted((t.get("targets") or {}).items()):
    spec = spec or {}
    rev = str(spec.get("buildRevision") or "")
    driver = (policies.get(vendor) or {}).get("driver") or {}
    repo = str(driver.get("repository") or "")
    version = str(driver.get("version") or "")
    variant = str(driver.get("kernelImageVariant") or "")
    # 토막 하나가 비면 규약을 벗어난 태그가 조립된다. 조용히 빼지 않고 걸린 것으로 보고한다 —
    # 빠진 항목은 조회도 묶음 대조도 받지 않아 영원히 보이지 않는다.
    if not rev:
        sys.exit("!! %s: buildRevision 이 비었다 — %s 를 고쳐라" % (vendor, sys.argv[1]))
    if not repo or not version:
        sys.exit("!! %s: %s 의 driverInstallPolicies.%s.driver 에 repository·version 이 없다"
                 % (vendor, sys.argv[2], vendor))
    # 참·거짓이 아닌 값은 거절한다. 조용히 yes 로 보면 publicBundle: "false"(문자열)나 오타가
    # 공개 묶음에 항목을 넣는다. 이 파일에는 스키마가 없어 여기가 유일한 검사 자리다.
    pub_raw = spec.get("publicBundle", True)
    if not isinstance(pub_raw, bool):
        sys.exit("!! %s: publicBundle 이 참·거짓이 아니다 (%r) — %s 를 고쳐라"
                 % (vendor, pub_raw, sys.argv[1]))
    pub = "yes" if pub_raw else "no"
    for kernel in kernels:
        tag = version + (("-" + variant) if variant else "") + "-k" + kernel + "-" + rev
        ref = "%s/%s:%s" % (registry, repo, tag) if registry else "%s:%s" % (repo, tag)
        rows.append("\t".join([repo, tag, ref, pub]))

print("\n".join(rows), end="\n" if rows else "")
PY
}

# 묶음 파일에서 kernel-images 묶음의 "<키><TAB><태그>" 를 낸다. 경로가 - 면 stdin 을 읽는다.
# stdin 은 임시 파일로 받는다 — 아래 heredoc 이 python 의 stdin 을 차지하므로 파이프로
# 넘기면 python 이 자료를 읽지 못하고 앞쪽 cat 이 SIGPIPE 로 죽는다.
bundle_rows() {
  local src="$1" tmp="" rc=0
  if [ "$src" = "-" ]; then
    tmp="$(mktemp)"; cat >"$tmp"; src="$tmp"
  fi
  python3 - "$bundle_name" "$src" <<'PY' || rc=$?
import sys, yaml
d = yaml.safe_load(open(sys.argv[2])) or {}
for item in ((d.get("bundles") or {}).get(sys.argv[1]) or []):
    item = item or {}
    print("%s\t%s" % (item.get("image", ""), item.get("tag", "")))
PY
  [ -z "$tmp" ] || rm -f "$tmp"
  return "$rc"
}

# 빈 문자열에 printf '%s\n' 을 쓰면 빈 줄 하나가 생겨 comm 이 그것을 항목으로 센다.
lines() { [ -z "$1" ] || printf '%s\n' "$1"; }

case "${1:-}" in
  --bundle-name)
    # 묶음 이름을 내주는 모드. hack/release-prepare.sh 가 태그 생성을 막을 묶음을 이것으로
    # 받는다 — 이 파일의 bundle_name 할당을 정규식으로 긁으면, 선언에 따옴표가 붙을 때
    # 값에 따옴표가 섞여 비교가 어긋나 가드가 조용히 사라진다. 셸이 값을 풀어 주는
    # 모드로 내주면 선언 형식이 어떻게 바뀌어도 받는 쪽이 깨지지 않는다.
    printf '%s\n' "$bundle_name"
    ;;
  --refs)
    derive | cut -f3
    ;;
  --bundle)
    derive | awk -F'\t' '$4=="yes"{print $1"\t"$2}'
    ;;
  --check-bundle)
    src="${2:-}"
    [ -n "$src" ] || { echo "용법: $0 --check-bundle <release-bundle.yaml 경로|->" >&2; exit 2; }
    [ "$src" = "-" ] || [ -f "$src" ] || { echo "!! 묶음 파일이 없다 — ${src}" >&2; exit 1; }
    want="$(derive | awk -F'\t' '$4=="yes"{print $1"\t"$2}' | sort)"
    have="$(bundle_rows "$src" | sort)"
    # 대상이 0건이어도 그냥 통과시키지 않는다 — 목록을 비웠는데 묶음에 항목이 남아 있으면
    # 아무도 선언하지 않은 이미지를 발행하게 된다. 양쪽이 다 비었을 때만 할 일이 없다.
    if [ -z "$want" ] && [ -z "$have" ]; then
      echo ">> 커널 이미지 발행 대상 0건 — ${bundle_name} 묶음도 비어 있다(대조할 것이 없다)"
      exit 0
    fi
    only_want="$(comm -23 <(lines "$want") <(lines "$have") | grep -c . || true)"
    only_have="$(comm -13 <(lines "$want") <(lines "$have") | grep -c . || true)"
    if [ "$only_want" -eq 0 ] && [ "$only_have" -eq 0 ]; then
      echo ">> ${bundle_name} 묶음 $(printf '%s\n' "$have" | grep -c .)건이 ${targets} 에서 유도한 것과 같다"
      exit 0
    fi
    echo "!! ${bundle_name} 묶음이 ${targets} 에서 유도한 것과 어긋난다" >&2
    if [ "$only_want" -gt 0 ]; then
      echo "   목록에 있는데 묶음에 없다 ${only_want}건:" >&2
      comm -23 <(lines "$want") <(lines "$have") | sed 's/^/     - /' >&2
    fi
    if [ "$only_have" -gt 0 ]; then
      echo "   묶음에 있는데 목록에 없다 ${only_have}건:" >&2
      comm -13 <(lines "$want") <(lines "$have") | sed 's/^/     - /' >&2
    fi
    exit 1
    ;;
  *)
    echo "용법: $0 --bundle-name | --refs | --bundle | --check-bundle <release-bundle.yaml 경로|->" >&2
    exit 2
    ;;
esac
