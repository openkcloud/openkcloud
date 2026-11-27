#!/usr/bin/env bash
# ============================================================
# release-prepare.sh: push 직전까지의 발행 준비를 한 번에 돌린다
# 상세: 선행 조건과 정합 검사를 돌리고, 필요한 태그를 로컬에만 만들어 push 명령을
#       출력한다(push 없음). 기본은 dry-run, --apply 가 태그를 만든다. 판정 근거는
#       각 단계·함수의 본문 주석 참고(CHECK_REGISTRY·pend 완화·커널 묶음 태그 차단 등).
# 용법: hack/release-prepare.sh [--apply] [<묶음 이름>]
# 생성일: 2026-10-03 | 수정일: 2026-10-15
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."

op="$PWD"
img="$PWD/../images/kcloud-operator-images"
pub=public/main
chain=hack/public-chain.sh
# 태그를 만들지 않는 묶음. 사유는 3단계 want_bundle_tag 머리 주석에 있다. 이름을 여기
# 다시 적지 않고 유도 스크립트가 내주는 값으로 받아, 이름을 바꾸는 사람이 이 가드를 함께
# 고치지 않아도 따라오게 한다. 소스를 긁지 않는 이유: 그 파일의 bundle_name 할당을 정규식
# 으로 읽으면, 선언이 bundle_name="kernel-images" 처럼 바뀔 때 값에 따옴표가 섞여 들어와
# 빈 값 가드를 비켜가고 비교만 어긋나 가드가 조용히 사라진다. 선언 형식은 또 바뀔 수
# 있으므로 정규식 대신 셸이 값을 풀어 주는 모드로 받는다.
derive_sh=hack/kernel-image-targets.sh
blocked_bundle="$(bash "$derive_sh" --bundle-name 2>/dev/null || true)"

apply=0
bundle=""
for a in "$@"; do
  case "$a" in
    --apply) apply=1 ;;
    --dry-run) apply=0 ;;
    -*) echo "용법: $0 [--apply] [<묶음 이름>]" >&2; exit 2 ;;
    *) bundle="$a" ;;
  esac
done

[ -d "$img/.git" ] || { echo "!! 형제 images 저장소가 없다 — ${img}" >&2; exit 1; }
img="$(cd "$img" && pwd)"   # 출력에 ../ 가 섞인 경로를 내지 않는다

fail=0
ok()   { printf '   [OK]   %s\n' "$*"; }
bad()  { printf '   [FAIL] %s\n' "$*"; fail=$((fail + 1)); }
note() { printf '   [--]   %s\n' "$*"; }
step() { printf '\n%s\n' "$*"; }
indent() { sed 's/^/          /'; }

# 패턴을 코드에 두 번 적지 않는다. 셸 스크립트의 단일 인용 할당에서 그대로 읽는다.
var_of() { grep -oP "^$2='\K[^']+" "$1" 2>/dev/null || true; }

printf '발행 준비 — %s\n' "$([ "$apply" -eq 1 ] && echo '--apply (로컬 태그 생성)' || echo 'dry-run (아무것도 바꾸지 않는다)')"

# ------------------------------------------------------------ 1단계
step "== 1단계 선행 조건 =="

chart=deploy/helm/Chart.yaml
ver="$(grep -oP '^version:\s*\K\S+' "$chart" || true)"
app="$(grep -oP '^appVersion:\s*\K\S+' "$chart" || true)"
op_tag="$app"
# release.yml 이 태그 push 에서 하는 대조와 같은 규칙이다(라인 태그는 -k8s<minor> 접미).
expected="${app#v}"; expected="${expected%-k8s*}"
if [ -z "$app" ] || [ -z "$ver" ]; then
  bad "${chart} 에서 version·appVersion 을 읽지 못했다"
elif [ "$ver" != "$expected" ]; then
  bad "${chart} version ${ver} 가 appVersion ${app} 과 어긋난다 — 태그 push 에서 release.yml 이 거절한다"
else
  ok "차트 version ${ver} / appVersion ${app} — operator 발행 태그는 ${op_tag}"
fi

# 프리셋 image.tag 대조는 chart_version_sync_test.go 가 이미 하므로 여기 다시 적지 않는다.
# 아래 두 Go 시험은 envtest(API 서버)가 필요하고, internal/controller 의 유일한 TestMain
# 이 -run 필터와 무관하게 항상 먼저 돈다. KUBEBUILDER_ASSETS 없이 부르면 TestMain 이
# panic 해 늘 FAIL 하며 --apply 를 영구 차단하는 셀프 함정이 된다. Makefile 의 test
# 타깃과 같은 방법으로 직접 만들어 넘긴다(--bin-dir 은 절대경로 필수, TestMain 자체
# 폴백엔 기대지 않는다) — 자산을 못 구하면 건너뛰지 않고 FAIL 로 처리한다.
k8s_ver="$(go list -m -f '{{.Version}}' k8s.io/api 2>/dev/null | awk -F'[v.]' '{printf "1.%d", $3}')"
assets=""
if [ -n "$k8s_ver" ] && [ -x "$op/bin/setup-envtest" ]; then
  assets="$("$op/bin/setup-envtest" use "$k8s_ver" --bin-dir "$op/bin" -p path 2>/dev/null || true)"
fi
[ -n "$assets" ] && [ -d "$assets" ] || assets=""

log="$(mktemp)"; blob="$(mktemp)"; trap 'rm -f "$log" "$blob"' EXIT
if [ -z "$assets" ]; then
  bad "프리셋 image.tag 대조를 돌리지 못했다 — envtest 자산 없음(make setup-envtest 먼저)"
elif KUBEBUILDER_ASSETS="$assets" go test ./internal/controller/ -count=1 \
     -run 'TestChartAppVersionMatchesPresets|TestDefaultValuesTagMatchesAppVersion' >"$log" 2>&1; then
  ok "작업 트리 프리셋 image.tag 가 차트 선언과 맞는다 (chart_version_sync_test.go)"
else
  bad "프리셋 image.tag 대조 실패 (chart_version_sync_test.go)"
  indent <"$log"
fi

# 두 라인 프리셋이 operator 이미지 말고는 같은 이미지를 쓰는지 본다. 라인별로 갈리는
# 산출물이 operator 하나뿐이라는 것이 릴리스 세트의 전제이고, 프리셋에 다른 이미지가
# 끼면 그 전제가 조용히 깨진다. 반대 라인 프리셋은 이 차트에 없으므로(kubeVersion 선언과
# 실재를 맞추려고 뺐다) 그 브랜치의 blob 을 꺼내 같은 Go 시험에 넘긴다 — 규칙을 두 번
# 적지 않는다. ref 가 없으면 건너뛰지 않고 걸린 것으로 센다. 건너뛰면 이 검사가 한 번도
# 돌지 않은 채 늘 초록이 된다.
other_ref=release/k8s-1.28
other_preset=deploy/helm/values-k8s1.28.yaml
if ! git show "${other_ref}:${other_preset}" >"$blob" 2>"$log"; then
  bad "${other_ref}:${other_preset} 를 읽지 못했다 — 두 라인 프리셋 대조를 할 수 없다"
  indent <"$log"
elif [ ! -s "$blob" ]; then
  # 빈 blob 은 Go 시험을 공허하게 통과시킨다 — 이미지 좌표가 0건이라 아무것도 걸리지 않는다.
  bad "${other_ref}:${other_preset} 가 비었다 — 대조할 내용이 없다"
elif [ -z "$assets" ]; then
  bad "두 라인 프리셋 대조를 돌리지 못했다 — envtest 자산 없음(make setup-envtest 먼저)"
elif KCLOUD_OTHER_LINE_PRESET="$blob" KUBEBUILDER_ASSETS="$assets" go test ./internal/controller/ -count=1 \
       -run TestReleaseLinePresetsShareNonOperatorImages >"$log" 2>&1; then
  ok "두 라인 프리셋이 operator 이미지 말고는 같은 이미지를 쓴다 (${other_ref} blob 대조)"
else
  bad "프리셋이 operator 외 이미지를 재정의한다 (${other_ref}:${other_preset} 대조)"
  indent <"$log"
fi

# Magnum 계약(release.yaml)의 valuesURL 은 커밋 SHA 로 고정한 raw 주소다. 프리셋을 고치고 SHA 를
# 옮기지 않으면 주소는 살아 있어도 지난 프리셋을 돌려주고, 차트 스키마가 그 값을 거절하면 Add-on
# 설치가 검증 단계에서 멈춘다. 이 차트 버전의 프로파일은 고정 SHA 의 프리셋이 작업 트리 프리셋과
# 바이트까지 같고 그 SHA 가 공개 브랜치에 있어야 한다. 다른 라인 프로파일은 그 SHA 가 이미 공개된
# 브랜치(upstream 원격 추적 ref)에 있는지만 본다 — 그 라인의 차트는 이 트리에 없다.
rel=deploy/integration/release.yaml
pin_re='^https://raw\.githubusercontent\.com/[^/]+/[^/]+/([0-9a-f]{40})/(.+)$'
profiles="$(awk '/^[[:space:]]*-[[:space:]]*range:/ { cv = ""; u = "" }
  /^[[:space:]]*chartVersion:/ { cv = $2; gsub(/"/, "", cv) }
  /^[[:space:]]*valuesURL:/ { u = $2 }
  cv != "" && u != "" { print cv, u; cv = ""; u = "" }' "$rel")"
[ -n "$profiles" ] || bad "${rel} 에서 chartVersion·valuesURL 쌍을 읽지 못했다 — 고정 프리셋 대조를 할 수 없다"
while read -r cv url; do
  [ -n "$cv" ] || continue
  if ! [[ "$url" =~ $pin_re ]]; then
    bad "${rel}: chartVersion ${cv} 의 valuesURL 이 커밋 SHA 고정 주소가 아니다 — ${url}"
    continue
  fi
  sha="${BASH_REMATCH[1]}"; path="${BASH_REMATCH[2]}"
  if ! git cat-file -e "${sha}^{commit}" 2>/dev/null; then
    bad "${rel}: chartVersion ${cv} 의 고정 커밋 ${sha:0:12} 가 이 저장소에 없다"
  elif [ "$cv" = "$ver" ]; then
    if ! git merge-base --is-ancestor "$sha" "$pub"; then
      bad "${rel}: 고정 커밋 ${sha:0:12} 가 ${pub} 에 없다 — push 뒤 그 주소는 404 다"
    elif ! git show "${sha}:${path}" 2>/dev/null | cmp -s - "$path"; then
      bad "${rel}: 고정 커밋 ${sha:0:12} 의 ${path} 가 작업 트리 프리셋과 다르다 — ${pub} 에서 그 파일을 마지막으로 바꾼 커밋 $(git log -1 --format=%H "$pub" -- "$path") 로 옮길 것"
    else
      ok "${rel}: chartVersion ${cv} 의 고정 프리셋(${sha:0:12})이 작업 트리 프리셋과 같고 ${pub} 에 있다"
    fi
  elif [ -n "$(git for-each-ref --contains "$sha" refs/remotes/upstream)" ]; then
    ok "${rel}: chartVersion ${cv} 의 고정 커밋 ${sha:0:12} 는 이미 공개된 브랜치에 있다"
  else
    bad "${rel}: chartVersion ${cv} 의 고정 커밋 ${sha:0:12} 가 공개된 브랜치(upstream)에 없다"
  fi
done <<<"$profiles"

# 문서에 SHA 고정 주소를 다시 적으면 release.yaml 과 따로 낡는다. 예시는 <valuesURL> 로 둔다.
# 주소 앞부분을 줄인 꼴(".../<SHA>/deploy/helm/values…")과 SHA 만 변수로 빼 둔 꼴(VALUES_SHA= 등, 따옴표 포함)도 같다.
dup="$(git grep -nE 'raw\.githubusercontent\.com/[^/]+/[^/]+/[0-9a-f]{40}/|[0-9a-f]{40}/deploy/helm/values|[A-Z_]*SHA=["'"'"']?[0-9a-f]{40}' -- . ":(exclude)${rel}" || true)"
if [ -n "$dup" ]; then
  bad "${rel} 밖에 커밋 SHA 고정 raw 주소가 있다 — <valuesURL> 로 바꿀 것"
  printf '%s\n' "$dup" | indent
else
  ok "커밋 SHA 고정 raw 주소는 ${rel} 에만 있다"
fi

for r in "$op" "$img"; do
  if [ -n "$(git -C "$r" status --porcelain)" ]; then
    bad "$(basename "$r") 작업 트리가 깨끗하지 않다 — 커밋되지 않은 변경은 발행본에 들어가지 않는다"
    git -C "$r" status --porcelain | indent
  else
    ok "$(basename "$r") 작업 트리 깨끗"
  fi
done

priv="$(var_of hack/private-ip-check.sh PRIV_RE)"
[ -n "$priv" ] || bad "hack/private-ip-check.sh 에서 PRIV_RE 를 읽지 못했다 — 사설 문자열 검사를 할 수 없다"

for kind in operator images; do
  case "$kind" in
    operator) r="$op"; ex="$(var_of "$chain" OP_EXCLUDE_RE)" ;;
    images)   r="$img"; ex="$(var_of "$chain" EXCLUDE_RE)" ;;
  esac
  if ! git -C "$r" rev-parse --verify -q "$pub" >/dev/null; then
    bad "${kind}: ${pub} 브랜치가 없다"
    continue
  fi
  head="$(git -C "$r" rev-parse --abbrev-ref HEAD)"

  # 공개 브랜치가 내부 작업 내용을 담는가. 커밋 도달 가능성이 아니라 두 트리의 파일
  # 목록 차이를 본다 — 공개 체인은 commit-tree 로 새 커밋을 만들어 해시가 늘 달라지므로
  # 도달 가능성으로 보면 영원히 빨강이다. 공개 체인이 빼는 경로와 내용을 고치는 파일은
  # 재생성 뒤에도 차이가 남으므로 실패로 세지 않는다.
  if [ -z "$ex" ]; then
    note "${kind}: ${chain} 가 없어 제외 경로를 알 수 없다 — 내부 작업 반영 검사는 건너뛴다"
  else
    left="$(git -C "$r" diff --name-only "$head" "$pub" | { grep -vE "$ex" || true; })"
    # 공개 체인이 변환하는 파일 목록도 그 스크립트에서 읽는다(prune_blob 호출 대상).
    xform=""; shown=""
    xform="$(grep -oP '^\s+prune_blob "\$c" \K\S+' "$chain" || true)"
    if [ -n "$xform" ] && [ -n "$left" ]; then
      shown="$(printf '%s\n' "$left" | { grep -Fxf <(printf '%s\n' "$xform") || true; })"
      left="$(printf '%s\n' "$left" | { grep -vFxf <(printf '%s\n' "$xform") || true; })"
    fi
    if [ -n "$left" ]; then
      bad "${kind}: ${head} 의 내용이 ${pub} 에 없다 — 통합과 공개 체인 재생성이 먼저다"
      printf '%s\n' "$left" | indent
    else
      ok "${kind}: ${pub} 가 ${head} 의 내용을 담는다 (공개 체인 제외·변환 대상 빼고)"
    fi
    if [ -n "$shown" ]; then
      note "${kind}: 공개 체인이 변환하는 파일이라 차이가 정상이다 — $(printf '%s' "$shown" | tr '\n' ' ')"
      note "${kind}: 변환 대상에 새 항목이 늘어난 경우는 이 검사가 보지 못한다 — 2단계 bundle-check 가 공개 ref 에서 본다"
    fi
  fi

  # 사설 문자열은 경로만 적는다. 매칭된 줄을 출력하면 그 주소가 이 출력에 실린다.
  if [ -n "$priv" ]; then
    hits="$(git -C "$r" grep -l -I -E "$priv" "$pub" -- . 2>/dev/null || true)"
    if [ -n "$hits" ]; then
      bad "${kind}: ${pub} 트리에 사설 문자열이 있는 파일 $(printf '%s\n' "$hits" | grep -c .)건"
      printf '%s\n' "$hits" | indent
    else
      ok "${kind}: ${pub} 트리 사설 문자열 0건"
    fi
  fi
done

# ------------------------------------------------------------ 2단계
step "== 2단계 정합 검사 =="

out="$(bash "$img/hack/bundle-check.sh" "$pub" 2>&1)" && rc=0 || rc=$?
printf '%s\n' "$out" | indent
if [ "$rc" -eq 0 ]; then
  ok "images: 발행 목록과 ${pub} 트리의 실재가 맞는다"
else
  bad "images: 발행 목록과 ${pub} 트리의 실재가 어긋난다"
fi

echo
# 위 bundle-check 는 공개 ref 를 보고, 그 ref 에 release-bundle.yaml 이 없으면 묶음 검사를
# 건너뛴다 — 그러면 kernel-images 묶음 대조도 함께 빠진다. 작업 트리의 묶음은 여기서 직접
# 본다. 발행 대상 목록(operator)과 묶음(images) 중 한쪽만 고치면 굽는 태그와 당기는 태그가
# 갈린다.
out="$(bash hack/kernel-image-targets.sh --check-bundle "$img/release-bundle.yaml" 2>&1)" && rc=0 || rc=$?
printf '%s\n' "$out" | indent
if [ "$rc" -eq 0 ]; then
  ok "커널 이미지 발행 대상과 images 작업 트리의 묶음이 맞는다"
else
  bad "커널 이미지 발행 대상과 images 작업 트리의 묶음이 어긋난다"
fi

echo
out="$(bash hack/refresh-image-digests.sh --check 2>&1)" && rc=0 || rc=$?
printf '%s\n' "$out" | indent
# 두 빨강을 구분한다 — 기록과 다른 digest 가 올라와 있는 것(결함)과, 아직 발행되지 않아
# 대조할 것이 없는 것(발행 뒤 해소)은 다른 사실이다. pend 는 반드시 어긋남 전체를
# 설명해야 note 로 눅인다 — kcloud-operator 처럼 합당한 미발행 항목이 하나 있다고 해서
# 그 옆의 다른 어긋남(예: 발행 접두 아래 401, 허용 목록 밖)까지 덩달아 완화되면 안 된다.
mis="$(printf '%s\n' "$out" | LC_ALL=C grep -c ' -> 레지스트리 ' || true)"
pend="$(printf '%s\n' "$out" | LC_ALL=C grep -c '발행본이 없다' || true)"
total="$(printf '%s\n' "$out" | LC_ALL=C grep -oP '어긋남 \K[0-9]+' | head -1 || true)"
if [ "$rc" -eq 0 ]; then
  ok "image-manifest digest 가 레지스트리와 맞는다"
elif [ "$mis" -eq 0 ] && [ "$pend" -gt 0 ] && [ "$pend" = "${total:-}" ]; then
  note "미발행 ${pend}건 때문에 빨강이다 — 발행 뒤 해소된다. 선행 조건 실패로 세지 않는다"
else
  bad "image-manifest digest 가 레지스트리와 어긋난다 (어긋남 ${total:-?}건, 그중 합당한 미발행 ${pend}건)"
fi

echo
# 기본은 꺼져 있는 검사(CHECK_REGISTRY)지만 release-prepare 는 건너뛰지 않고 직접 돌린다 —
# 네트워크가 없거나 미발행 태그가 있으면 그대로 실패로 보고한다. note 로 눅이지 않는다.
out="$(bash hack/image-tag-check.sh 2>&1)" && rc=0 || rc=$?
printf '%s\n' "$out" | indent
if [ "$rc" -eq 0 ]; then
  ok "차트 기본값(exporter 포함)이 당기는 이미지 태그가 레지스트리에 있다 (image-tag-check)"
else
  bad "이미지 태그 실재 확인 실패 — 네트워크가 없거나 미발행 태그가 있다 (image-tag-check)"
fi

# 커널별 사전 컴파일 이미지는 위 rc 에 들어가지 않는다 — 아직 굽지 않은 것이 정답인 구간이
# 있어 image-tag-check 가 결함으로 세지 않는다. 그 수를 여기서 읽어 눈에 띄게 둔다.
# 미발행을 선행 조건 실패로 세지 않는 이유는 push 로 해소되는 사실이기 때문이다 — "발행
# 경로가 올바른 내용을 만들 수 없다" 는 다른 사실이고 3단계의 blocked_bundle 이 막는다.
# 세 수를 다 읽는다 — 발행 수를 읽지 않으면 "없다고 확답받은 것이 없다"(kmiss==0)를 "다
# 있다" 로 적어, 요율 제한으로 9건 중 8건이 401 이어도 초록이 된다.
ktotal="$(printf '%s\n' "$out" | LC_ALL=C grep -oP '커널 이미지 — 대상 \K[0-9]+' | head -1 || true)"
kpub="$(printf '%s\n' "$out" | LC_ALL=C grep -oP '커널 이미지 — 대상 [0-9]+건 중 발행 \K[0-9]+' | head -1 || true)"
kmiss="$(printf '%s\n' "$out" | LC_ALL=C grep -oP '커널 이미지 — 대상 [0-9]+건 중 발행 [0-9]+건, 미발행 \K[0-9]+' | head -1 || true)"
kskip="$(printf '%s\n' "$out" | LC_ALL=C grep -oP '커널 이미지 — 대상 [0-9]+건 중 발행 [0-9]+건, 미발행 [0-9]+건, 조회 못 함 \K[0-9]+' | head -1 || true)"
if [ -z "$ktotal" ]; then
  if printf '%s\n' "$out" | LC_ALL=C grep -q '커널 이미지 발행 대상 0건'; then
    note "커널 이미지 발행 대상 0건 — deploy/kernel-image-targets.yaml 이 비어 있다"
  else
    bad "커널 이미지 발행 대상 보고를 찾지 못했다 — image-tag-check 가 그 목록을 보지 않았다"
  fi
elif [ -z "$kpub" ] || [ -z "$kmiss" ] || [ -z "$kskip" ]; then
  bad "커널 이미지 집계 세 수를 다 읽지 못했다 — image-tag-check 출력 형식이 바뀌었다"
elif [ "$kskip" -gt 0 ]; then
  # 모르는 것을 "있다" 로도 "없다" 로도 적지 않는다. 릴리스 게이트는 전부 확답을 요구한다.
  bad "커널 이미지 ${kskip}/${ktotal}건을 조회하지 못했다 — 발행 상태를 모르는 채로 발행할 수 없다"
elif [ "$kmiss" -gt 0 ]; then
  note "커널 이미지 ${kmiss}/${ktotal}건 미발행 — 항목별 build-arg 전달 경로가 생기기 전에는" \
       "bundle/${blocked_bundle} 태그를 push 하지 말 것"
elif [ "$kpub" -eq "$ktotal" ]; then
  ok "커널 이미지 발행 대상 ${ktotal}건이 모두 레지스트리에 있다"
else
  bad "커널 이미지 집계가 맞지 않는다 — 대상 ${ktotal}건, 발행 ${kpub}건, 미발행 ${kmiss}건"
fi

echo
# manifest-coverage 의 역방향(렌더 → 매니페스트)은 기본이 경고뿐이다(상시 빨강으로 무관한
# 작업을 막지 않으려는 선택, test/airgap/manifest-coverage.sh 머리말 참고) — release-prepare
# 는 건너뛰지 않고 CHECK_REVERSE_COVERAGE=1 로 돌려 미등재를 실패로 올린다. 정방향
# (images.yaml → 매니페스트)은 토글과 무관하게 항상 실패로 올라간다(기존 동작 그대로).
out="$(CHECK_REVERSE_COVERAGE=1 bash test/airgap/manifest-coverage.sh 2>&1)" && rc=0 || rc=$?
printf '%s\n' "$out" | indent
if [ "$rc" -eq 0 ]; then
  ok "images.yaml·차트 렌더가 쓰는 이미지가 모두 image-manifest.yaml 에 등재돼 있다 (manifest-coverage)"
else
  bad "image-manifest.yaml 등재가 어긋난다 — 위 FAIL/WARN 줄 참고 (manifest-coverage)"
fi

# ------------------------------------------------------------ 3단계
step "== 3단계 태그 =="

plan=""       # "<repo>\t<태그>\t<커밋>\t<설명>" — --apply 가 만들 태그
pushable=""   # "<repo>\t<태그>" — push 명령에 넣을 태그

# want_tag: 태그가 가리켜야 할 커밋을 대조한다. 있으면 확인만, 없으면 만들 목록에 넣는다.
want_tag() {
  local r="$1" t="$2" target="$3" what="$4" at cur
  if ! at="$(git -C "$r" rev-parse --verify -q "${target}^{commit}")"; then
    bad "$(basename "$r"): ${target} 가 없어 ${t} 의 대상 커밋을 정할 수 없다"
    return
  fi
  if cur="$(git -C "$r" rev-parse --verify -q "refs/tags/${t}^{commit}")"; then
    if [ "$cur" = "$at" ]; then
      ok "${t} 이미 있고 ${target} (${at:0:9}) 를 가리킨다"
      pushable="${pushable}${r}	${t}
"
    else
      bad "${t} 이 ${cur:0:9} 를 가리킨다 — ${target} 는 ${at:0:9} 다. 태그를 움직이지 않는다"
    fi
    return
  fi
  note "${t} 없음 → ${target} (${at:0:9}) 에 만든다 — ${what}"
  plan="${plan}${r}	${t}	${at}	${what}
"
  pushable="${pushable}${r}	${t}
"
}

want_tag "$op" "$op_tag" "$pub" "operator 이미지·차트 발행"

# blocked_bundle 은 태그를 만들지 않는 묶음이다 — publish-bundle.yml 이 images.yaml 에서만
# build-arg 를 읽어 커널마다 다른 PREBUILD_* 를 못 넘기므로, push 하면 사전 컴파일이 아닌
# 이미지가 커널 태그를 달고 발행되고 재발행이 거절돼 그 빌드 개정은 못 쓰게 된다. "미발행
# 이면 실패" 와 달리 push 가 악화시켜 2단계는 note 로 두고 태그 생성만 막는다. 지우는 조건:
# publish-bundle.yml 이 build-arg 를 병합하거나 사전 컴파일 라벨 대조 검사가 생길 때.
# want_bundle_tag 로 모아 둔다 — 두 호출부가 각자 부르면 한쪽에만 가드가 걸린다.
want_bundle_tag() {
  local name="$1"
  # 막을 이름을 받지 못했으면 가드가 없는 것과 같다. 빈 문자열을 비교에 쓰면 어떤 묶음도
  # 걸리지 않아 가드가 조용히 사라지므로, 모르는 채로 태그를 만들지 않고 멈춘다.
  # 받지 못하는 경우는 --bundle-name 모드가 없어졌거나(모드 이름 변경) 그 스크립트가 먼저
  # 실패하는 때다. 값이 섞여 들어오는 경우는 모드 호출로 바꾼 뒤 더 이상 없다.
  if [ -z "$blocked_bundle" ]; then
    bad "images: ${derive_sh} --bundle-name 이 묶음 이름을 내주지 않았다 —" \
        "막을 묶음을 모르는 채로 태그를 만들지 않는다"
    return
  fi
  if [ "$name" = "$blocked_bundle" ]; then
    bad "images: ${name} 묶음 태그를 만들지 않는다 — 항목별 build-arg 전달 경로가 생기기" \
        "전에는 이 태그를 push 하지 말라 (사유는 이 스크립트의 blocked_bundle 주석과" \
        "images 저장소 release-bundle.yaml 의 ${name} 주석)"
    return
  fi
  want_tag "$img" "bundle/${name}" "$pub" "묶음 ${name} 발행"
}

# 묶음 이름은 release-bundle.yaml 의 키에서 읽는다. snap yq 는 /tmp 를 읽지 못해 PyYAML 을 쓴다.
names="$(python3 -c '
import sys, yaml
print("\n".join((yaml.safe_load(open(sys.argv[1])) or {}).get("bundles") or {}))
' "$img/release-bundle.yaml" 2>/dev/null || true)"
count="$(printf '%s\n' "$names" | grep -c . || true)"
if [ "$count" -eq 0 ]; then
  bad "images: release-bundle.yaml 에서 묶음을 읽지 못했다"
elif [ -n "$bundle" ]; then
  if printf '%s\n' "$names" | grep -qxF -- "$bundle"; then
    want_bundle_tag "$bundle"
  else
    bad "images: release-bundle.yaml 에 ${bundle} 묶음이 없다 (있는 것: $(printf '%s' "$names" | tr '\n' ' '))"
  fi
elif [ "$count" -eq 1 ]; then
  want_bundle_tag "$names"
else
  bad "images: 묶음이 ${count}개다 — 인자로 이름을 지정하라 ($(printf '%s' "$names" | tr '\n' ' '))"
fi

if [ "$apply" -eq 1 ]; then
  if [ "$fail" -ne 0 ]; then
    echo "!! 걸린 항목 ${fail}건. 태그를 만들지 않는다." >&2
    exit 1
  fi
  while IFS=$'\t' read -r r t at what; do
    [ -n "$t" ] || continue
    git -C "$r" tag -a "$t" -m "$what" "$at"
    ok "${t} 생성 — $(basename "$r") ${at:0:9} (push 하지 않는다)"
  done <<< "$plan"
elif [ -n "$plan" ]; then
  note "dry-run 이라 만들지 않았다. 만들려면 --apply 를 준다"
fi

# ------------------------------------------------------------ 4단계
step "== 4단계 push 명령 — 사용자가 실행한다 =="
echo "   브랜치를 먼저, 태그를 나중에 올린다. 태그 push 가 발행을 트리거하므로 순서가"
echo "   바뀌면 발행이 태그 커밋 이전의 옛 코드로 돈다."
echo

# 주소는 코드에 적지 않고 remote 에서 읽는다. 공개 발행은 upstream, 없으면 origin 이다.
remote_of() {
  local n
  for n in upstream origin; do
    if git -C "$1" remote get-url "$n" >/dev/null 2>&1; then printf '%s' "$n"; return 0; fi
  done
  return 1
}
for r in "$op" "$img"; do
  if rn="$(remote_of "$r")"; then
    printf '   # %s -> %s\n' "$(basename "$r")" "$(git -C "$r" remote get-url "$rn")"
    printf '   git -C %s push %s %s:main\n' "$r" "$rn" "$pub"
  else
    bad "$(basename "$r"): upstream·origin remote 가 없어 push 명령을 적을 수 없다"
  fi
done
echo
while IFS=$'\t' read -r r t; do
  [ -n "$t" ] || continue
  rn="$(remote_of "$r")" || continue
  printf '   git -C %s push %s %s\n' "$r" "$rn" "$t"
done <<< "$pushable"

step "== 정리 =="
if [ "$fail" -ne 0 ]; then
  echo "   걸린 항목 ${fail}건. 위 [FAIL] 을 해소한 뒤 다시 돌린다."
  exit 1
fi
echo "   선행 조건·정합 검사 통과. push 는 사용자가 한다."
