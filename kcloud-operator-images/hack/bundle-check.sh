#!/usr/bin/env bash
# ============================================================
# bundle-check.sh: 발행 목록과 저장소 실재의 정합을 검사한다.
# 상세: images.yaml 의 dir/Dockerfile 실재, release-bundle.yaml 키, 발행 워크플로 선택지,
#       README 태그 표가 같은 키 집합인지 본다. 사용: hack/bundle-check.sh [<git ref>]
# 생성일: 2026-10-03 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

cd "$(dirname "$0")/.."
ref="${1:-}"
what="${ref:-작업 트리}"

# ref 가 없으면 체크아웃을, 있으면 그 ref 의 트리를 본다.
read_blob() {
  if [ -n "$ref" ]; then
    git show "${ref}:$1" 2>/dev/null || true
  else
    cat "$1" 2>/dev/null || true
  fi
}

dir_exists() {
  if [ -n "$ref" ]; then
    [ -n "$(git ls-tree -d "$ref" -- "$1")" ]
  else
    [ -d "$1" ]
  fi
}

file_exists() {
  if [ -n "$ref" ]; then
    # ls-tree 는 디렉터리도 출력하므로 blob 인지까지 본다.
    [ "$(git ls-tree "$ref" -- "$1" | awk '{print $2}')" = "blob" ]
  else
    [ -f "$1" ]
  fi
}

images_yaml="$(read_blob images.yaml)"
if [ -z "$images_yaml" ]; then
  echo "!! images.yaml 을 읽지 못했다 (${what})." >&2
  exit 1
fi

# snap yq 는 /tmp 를 읽지 못한다. YAML 파싱은 python3 PyYAML 로 한다.
image_entries() {
  printf '%s\n' "$images_yaml" | python3 -c '
import sys, yaml
d = yaml.safe_load(sys.stdin) or {}
for k, v in (d.get("images") or {}).items():
    v = v or {}
    print("%s\t%s\t%s" % (k, v.get("dir", ""), v.get("dockerfile") or "Dockerfile"))
'
}

bundle_entries() {
  printf '%s\n' "$1" | python3 -c '
import sys, yaml
d = yaml.safe_load(sys.stdin) or {}
for b, items in (d.get("bundles") or {}).items():
    for it in (items or []):
        print("%s\t%s" % (b, (it or {}).get("image", "")))
'
}

fail=0
names=""

# 프로세스 치환 안의 파싱 실패는 set -e 가 잡지 못한다. 먼저 변수로 받는다.
images_list="$(image_entries)"
if [ -z "$images_list" ]; then
  echo "!! images.yaml 에 images 항목이 없다 (${what})." >&2
  exit 1
fi

while IFS=$'\t' read -r name dir dockerfile; do
  [ -n "$name" ] || continue
  names="${names}${name}
"
  if [ -z "$dir" ]; then
    echo "!! images.yaml: ${name} 에 dir 가 없다." >&2
    fail=1
  elif ! dir_exists "$dir"; then
    # dir 가 없으면 그 아래 Dockerfile 도 없다. 한 번만 보고한다.
    echo "!! images.yaml: ${name} 의 dir ${dir} 가 없다 (${what})." >&2
    fail=1
  elif ! file_exists "${dir}/${dockerfile}"; then
    echo "!! images.yaml: ${name} 의 ${dir}/${dockerfile} 가 없다 (${what})." >&2
    fail=1
  fi
done <<< "$images_list"

# 발행 워크플로의 수동 실행 선택지는 images.yaml 키 집합과 같아야 한다. 공개 트리 변환이
# 한쪽만 고치면 빌드 경로가 없는 이미지가 선택지로 남아 고르는 순간 발행이 깨진다.
workflow=.github/workflows/publish-image.yml
workflow_yaml="$(read_blob "$workflow")"
if [ -z "$workflow_yaml" ]; then
  echo "!! ${workflow} 를 읽지 못했다 (${what})." >&2
  fail=1
else
  # YAML 1.1 예약어라 on: 이 불리언 True 로 파싱된다. 두 키를 다 본다.
  choices="$(printf '%s\n' "$workflow_yaml" | python3 -c '
import sys, yaml
w = yaml.safe_load(sys.stdin) or {}
on = w.get("on") or w.get(True) or {}
inputs = (on.get("workflow_dispatch") or {}).get("inputs") or {}
for c in ((inputs.get("image") or {}).get("options") or []):
    print(c)
')"
  if [ -z "$choices" ]; then
    echo "!! ${workflow} 에서 image 선택지를 찾지 못했다 (${what})." >&2
    fail=1
  else
    only_workflow="$(comm -23 <(sort -u <<< "$choices") <(printf '%s' "$names" | sort -u))"
    only_images="$(comm -13 <(sort -u <<< "$choices") <(printf '%s' "$names" | sort -u))"
    if [ -n "$only_workflow" ]; then
      echo "!! ${workflow} 선택지에만 있고 images.yaml 에 없다 (${what}):" \
        "$(tr '\n' ' ' <<< "$only_workflow")" >&2
      fail=1
    fi
    if [ -n "$only_images" ]; then
      echo "!! images.yaml 에만 있고 ${workflow} 선택지에 없다 (${what}):" \
        "$(tr '\n' ' ' <<< "$only_images")" >&2
      fail=1
    fi
  fi
fi

# 발행 절차 문서의 "태그 규약" 표 첫 열도 같은 키 집합이어야 한다. 선택지·매니페스트는 이
# 스크립트가 보는데 그 표만 안 보면, 항목을 더하는 사람이 표를 잊어도 아무것도 울지 않는다
# (operator 의 hack/helm-values-option-check.sh 가 README 표를 values 와 대조하는 것과 같은
# 성격이다). 공개 트리에는 docs/ 가 없어 표가 없으면 건너뛴다 — 다만 표가 있는데 행을 0개
# 읽는 것은 파서가 깨진 것이므로 실패로 낸다.
readme="$(read_blob docs/README.md)"
if [ -z "$readme" ]; then
  echo ">> docs/README.md 가 없어 태그 규약 표 대조는 건너뛴다 (${what})."
else
  # 다른 표(디렉터리 이름)와 섞이지 않게 그 절만 본다. 다음 제목에서 끊는다.
  table_names="$(printf '%s\n' "$readme" \
    | awk '/^### 태그 규약[[:space:]]*$/{f=1;next} /^#{1,6}[[:space:]]/{f=0} f' \
    | sed -nE 's/^\| `([A-Za-z0-9._-]+)` *\|.*/\1/p' | sort -u)"
  if [ -z "$table_names" ]; then
    echo "!! docs/README.md 의 태그 규약 표에서 이미지 이름을 0개 읽었다 (${what})." >&2
    echo "   표 형식(제목 '### 태그 규약', 첫 열 \`이미지명\`)이 바뀌었는지 확인하라." >&2
    fail=1
  else
    only_table="$(comm -23 <(sort -u <<< "$table_names") <(printf '%s' "$names" | sort -u))"
    only_images_doc="$(comm -13 <(sort -u <<< "$table_names") <(printf '%s' "$names" | sort -u))"
    if [ -n "$only_table" ]; then
      echo "!! 태그 규약 표에만 있고 images.yaml 에 없다 (${what}):" \
        "$(tr '\n' ' ' <<< "$only_table")" >&2
      fail=1
    fi
    if [ -n "$only_images_doc" ]; then
      echo "!! images.yaml 에만 있고 태그 규약 표에 없다 (${what}):" \
        "$(tr '\n' ' ' <<< "$only_images_doc")" >&2
      fail=1
    fi
    if [ -z "$only_table" ] && [ -z "$only_images_doc" ]; then
      echo ">> 태그 규약 표 $(wc -l <<< "$table_names")행이 images.yaml 키 집합과 같다."
    fi
  fi
fi

# 공개 브랜치에는 아직 이 파일이 없다. 없으면 검사할 묶음도 없다.
bundle_yaml="$(read_blob release-bundle.yaml)"
if [ -z "$bundle_yaml" ]; then
  echo ">> release-bundle.yaml 이 없어 묶음 검사는 건너뛴다 (${what})."
else
  bundle_list="$(bundle_entries "$bundle_yaml")"
  while IFS=$'\t' read -r bundle image; do
    [ -n "$bundle" ] || continue
    if [ -z "$image" ]; then
      echo "!! release-bundle.yaml: ${bundle} 묶음에 image 없는 항목이 있다." >&2
      fail=1
    elif ! printf '%s' "$names" | grep -qxF -- "$image"; then
      echo "!! release-bundle.yaml: ${bundle} 묶음의 ${image} 가 images.yaml 에 없다." >&2
      fail=1
    fi
  done <<< "$bundle_list"

  # kernel-images 묶음은 operator 저장소의 deploy/kernel-image-targets.yaml 에서 유도한
  # 것이어야 한다. 그 목록을 보는 태그 실재 게이트가 operator 저장소에 있어 목록도 거기
  # 있고(CI 에는 이 저장소가 없다), 그래서 한쪽만 고치는 일이 생긴다. 두 저장소가 다 있을
  # 때 그 어긋남을 여기서 잡는다 — 바꾸는 사람이 어느 쪽에서 돌리든 걸리게 양쪽에 둔다
  # (반대쪽은 operator 의 release-prepare.sh 2단계).
  derive=../../kcloud-operator/hack/kernel-image-targets.sh
  # 호출은 bash "$derive" 이므로 실행 권한은 필요 없다. -x 로 보면 내보내기·복사로 권한만
  # 빠진 트리에서 대조가 조용히 빠진다.
  if [ ! -f "$derive" ]; then
    echo ">> 형제 operator 저장소가 없어 kernel-images 묶음 대조는 건너뛴다 (${derive})."
  elif printf '%s\n' "$bundle_yaml" | bash "$derive" --check-bundle -; then
    :
  else
    echo "!! kernel-images 묶음이 operator 의 발행 대상 목록과 어긋난다 (${what})." >&2
    fail=1
  fi
fi

# 통합 Furiosa 이미지는 Warboy 바이너리를 벤더 공개 이미지에서 꺼내 굽는다. 그 출처를
# images.yaml 의 build-arg 가 적고, 같은 이미지를 operator 차트도 Warboy device-plugin
# 기본값으로 쓴다 — 두 값이 갈리면 차트가 쓰는 것과 다른 벤더 버전으로 구워진다. 차트 값이
# 다른 저장소에 있어 한쪽만 고치는 일이 생기므로 두 저장소가 다 있을 때 여기서 잡는다.
chart_values=../../kcloud-operator/deploy/helm/values.yaml
warboy_declared="$(printf '%s\n' "$images_yaml" | python3 -c '
import sys, yaml
imgs = (yaml.safe_load(sys.stdin) or {}).get("images") or {}
key = "furiosa-unified-device-plugin"
if key not in imgs:
    print("ABSENT")          # 공개 트리에는 이 항목이 없다(공개 체인 제외)
else:
    print(((imgs[key] or {}).get("build_args") or {}).get("WARBOY_IMAGE") or "MISSING")
')"
if [ "$warboy_declared" = "ABSENT" ]; then
  echo ">> images.yaml 에 통합 Furiosa 항목이 없어 Warboy 소스 대조는 건너뛴다 (${what})."
elif [ ! -f "$chart_values" ]; then
  echo ">> 형제 operator 저장소가 없어 Warboy 소스 대조는 건너뛴다 (${chart_values})."
else
  warboy_chart="$(python3 -c '
import sys, yaml
d = yaml.safe_load(open(sys.argv[1])) or {}
print((d.get("furiosa") or {}).get("devicePluginImage") or "MISSING")
' "$chart_values")"
  if [ "$warboy_declared" = "MISSING" ]; then
    echo "!! images.yaml: furiosa-unified-device-plugin 에 build_args.WARBOY_IMAGE 가 없다 (${what})." >&2
    fail=1
  elif [ "$warboy_chart" = "MISSING" ]; then
    echo "!! operator 차트에서 furiosa.devicePluginImage 를 읽지 못했다 (${chart_values})." >&2
    fail=1
  elif [ "$warboy_declared" != "$warboy_chart" ]; then
    echo "!! 통합 Furiosa 이미지의 WARBOY_IMAGE 가 차트 Warboy 기본값과 다르다 (${what}):" >&2
    echo "     images.yaml : ${warboy_declared}" >&2
    echo "     차트 values : ${warboy_chart}" >&2
    fail=1
  else
    echo ">> 통합 Furiosa 이미지의 WARBOY_IMAGE 가 차트 Warboy 기본값과 같다 (${warboy_chart})."
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "!! 발행 목록과 실재가 어긋난다 (${what})." >&2
  exit 1
fi
echo ">> 발행 목록과 실재가 맞는다 (${what})."
