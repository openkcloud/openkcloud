#!/usr/bin/env bash
# ============================================================
# evidence.sh: evidence 레코드 수집 (JSON+MD, 스키마 검증)
# 상세: 라이브 클러스터 메타데이터(kcloud 버전·커밋·K8s·노드·NodeDeviceReport)와
#       한 테스트 명령의 실행 결과를 모아 evidence 레코드 필드의 JSON/Markdown 짝을 만든다.
#       측정값을 지어내지 않는다 — NDR 에 없는 필드는 "미수집" 으로 남긴다.
#       --validate 는 schema.json 의 필수 키·verdict enum 으로 레코드를 검사한다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OPERATOR_REPO="$(cd "${SCRIPT_DIR}/../.." && pwd)"
UNCOLLECTED="미수집"
NA="해당 없음"
BT='`'   # Markdown inline-code 백틱(포맷 문자열에 직접 넣으면 shellcheck SC2016)

usage() {
  cat <<'USAGE'
용법:
  evidence.sh --node <n> --vendor <v> --command "<cmd>" --expected "<text>" \
              [--actual-file <path>] [--backend devicePlugin|dra] \
              [--mode <partition/sharing>] \
              [--resource <name>] [--firmware <ver>] [--sdk <ver>] [--id-suffix <slug>] \
              [--limit "<한계>"]... [-o <출력 prefix>] [--schema <path>]
  evidence.sh --validate <FILE.json> [--schema <path>]

동작(수집):
  - 메타데이터: kcloudVersion(operator Deployment 이미지 태그), commit(operator Deployment 로그의
    starting manager 줄 → 없으면 컨테이너 안 /manager --version → 그래도 없으면 로컬
    체크아웃 HEAD 에 -checkout 표시),
    k8sVersion, node 의 os/kernel/containerRuntime, NodeDeviceReport 의 벤더 엔트리에서
    device/driver/firmware/sdk, node allocatable 에서 resourceName(--resource 로 override).
    firmware/sdk 는 NDR 에 없어도 알려진 값을 --firmware/--sdk 로 채울 수 있다.
  - actual: --command 실행의 stdout+stderr 결합. duration: 명령 실측 소요 시간(초).
    --actual-file 지정 시 명령을 실행하지 않고 그 파일 내용을 actual 로 쓴다(라이브
    조건이 이미 지난 사전 캡처용). 이때 --command 는 그 캡처를 낸 재현 가능한 명령의
    provenance 로 기록되고, verdict 는 여전히 --expected 포함 여부로 판정한다.
  - verdict: actual 에 --expected 문자열이 포함되면 pass, 아니면 fail.
             node/NDR/벤더 엔트리가 없으면 명령을 실행하지 않고 blocked(exit 2).
  - 출력: <prefix>.json 과 <prefix>.md. 생성 직후 schema.json 으로 자체 검증.
  - doc_id/title: 기본은 벤더·노드·날짜로 만든다(같은 노드·벤더로 하루 두 레코드를
    만들면 겹친다). 구분이 필요하면 --id-suffix 로 접미사를 더한다(기본 동작은 그대로).
  - 종료 코드: pass=0, fail=1, blocked=2.
USAGE
}

# validate_record FILE SCHEMA — 필수 키 존재(비-null) + verdict enum 검사.
validate_record() {
  local file="$1" schema="$2"
  if [ ! -f "$file" ]; then echo "검증 실패: 파일 없음 ${file}" >&2; return 1; fi
  if [ ! -f "$schema" ]; then echo "검증 실패: 스키마 없음 ${schema}" >&2; return 1; fi
  local report
  if ! report=$(jq --slurpfile s "$schema" '
        . as $rec
        | { missing: [ $s[0].requiredKeys[] as $k | select(($rec[$k]) == null) | $k ],
            verdict: ($rec.verdict // null),
            verdictOk: (($s[0].verdictEnum | index($rec.verdict)) != null) }
      ' "$file" 2>/dev/null); then
    echo "검증 실패: ${file} 파싱 불가(JSON 아님)" >&2; return 1
  fi
  local missing verdict_ok verdict ok
  missing=$(printf '%s' "$report" | jq -r '.missing | join(", ")')
  verdict_ok=$(printf '%s' "$report" | jq -r '.verdictOk')
  verdict=$(printf '%s' "$report" | jq -r '.verdict // "(없음)"')
  ok=1
  if [ -n "$missing" ]; then echo "  누락 필수 키: ${missing}" >&2; ok=0; fi
  if [ "$verdict_ok" != "true" ]; then
    echo "  verdict 값이 enum(pass|fail|blocked) 밖: ${verdict}" >&2; ok=0
  fi
  if [ "$ok" -eq 1 ]; then echo "검증 통과: ${file}"; return 0; fi
  echo "검증 실패: ${file}" >&2; return 1
}

# jq_field JSON FILTER — JSON 이 비어있거나 필터 결과가 null 이면 "미수집".
jq_field() {
  local json="$1" filter="$2" out
  if [ -z "$json" ]; then printf '%s' "$UNCOLLECTED"; return; fi
  if ! out=$(printf '%s' "$json" | jq -r "$filter" 2>/dev/null); then
    printf '%s' "$UNCOLLECTED"; return
  fi
  if [ -z "$out" ] || [ "$out" = "null" ]; then printf '%s' "$UNCOLLECTED"; return; fi
  printf '%s' "$out"
}

# read_build_commit — 라이브 operator 의 빌드 커밋을 읽는다. 순서:
#  (1) Deployment 로그의 "starting manager" 줄의 commit 필드.
#  (2) 안 되면 컨테이너 안 `/manager --version`(이전 이미지는 모르는 플래그라 종료 코드 2로
#      끝나 stdout 이 비어 다음으로 넘어간다).
#  (3) 둘 다 없으면 로컬 체크아웃 HEAD 에 "-checkout" 을 붙여 라이브 커밋이 아님을 드러낸다.
# baked ldflags(-X main.buildCommit) 값은 kubectl 의 PodSpec env 로 보이지 않아 이 두 경로를 쓴다.
read_build_commit() {
  local log_line live_commit local_head
  log_line="$(kubectl -n kcloud logs deploy/kcloud-operator 2>/dev/null | grep -m1 'starting manager' || true)"
  if [ -n "$log_line" ]; then
    live_commit="$(printf '%s' "$log_line" | grep -oE '\{.*\}' | jq -r '.commit // empty' 2>/dev/null || true)"
  fi
  if [ -z "${live_commit:-}" ]; then
    live_commit="$(kubectl -n kcloud exec deploy/kcloud-operator -- /manager --version 2>/dev/null | head -1 | grep -oE '^[0-9a-f]{7,40}$' || true)"
  fi
  if [ -n "${live_commit:-}" ]; then
    printf '%s' "$live_commit"; return
  fi
  local_head="$(git -C "$OPERATOR_REPO" rev-parse --short HEAD 2>/dev/null || true)"
  if [ -n "$local_head" ]; then
    printf '%s-checkout' "$local_head"
  else
    printf '%s' "$UNCOLLECTED"
  fi
}

# ---- 인자 파싱 ----
mode="collect"
validate_file=""
node=""
vendor=""
command_str=""
actual_file_override=""
expected=""
backend="$NA"
part_mode="$NA"
resource_override=""
firmware_override=""
sdk_override=""
id_suffix=""
out=""
schema="${SCRIPT_DIR}/schema.json"
limits=()

while [ $# -gt 0 ]; do
  case "$1" in
    --validate) mode="validate"; validate_file="${2:-}"; shift 2 ;;
    --node) node="${2:-}"; shift 2 ;;
    --vendor) vendor="${2:-}"; shift 2 ;;
    --command) command_str="${2:-}"; shift 2 ;;
    --actual-file) actual_file_override="${2:-}"; shift 2 ;;
    --expected) expected="${2:-}"; shift 2 ;;
    --backend) backend="${2:-}"; shift 2 ;;
    --mode) part_mode="${2:-}"; shift 2 ;;
    --resource) resource_override="${2:-}"; shift 2 ;;
    --firmware) firmware_override="${2:-}"; shift 2 ;;
    --sdk) sdk_override="${2:-}"; shift 2 ;;
    --id-suffix) id_suffix="${2:-}"; shift 2 ;;
    --limit) limits+=("${2:-}"); shift 2 ;;
    -o|--out) out="${2:-}"; shift 2 ;;
    --schema) schema="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "알 수 없는 인자: $1" >&2; usage; exit 64 ;;
  esac
done

if [ "$mode" = "validate" ]; then
  if [ -z "$validate_file" ]; then echo "--validate 에는 파일 경로가 필요하다" >&2; exit 64; fi
  validate_record "$validate_file" "$schema"
  exit $?
fi

# ---- 수집 모드: 필수 인자 확인 ----
missing_args=""
[ -n "$node" ] || missing_args="${missing_args} --node"
[ -n "$vendor" ] || missing_args="${missing_args} --vendor"
[ -n "$command_str" ] || missing_args="${missing_args} --command"
[ -n "$expected" ] || missing_args="${missing_args} --expected"
if [ -n "$missing_args" ]; then
  echo "필수 인자 누락:${missing_args}" >&2; usage; exit 64
fi

date_str="$(date +%F)"
run_utc="$(date -u +%FT%TZ)"

# ---- 클러스터 메타데이터 ----
node_json="$(kubectl get node "$node" -o json 2>/dev/null || true)"
ndr_json="$(kubectl get nodedevicereport "$node" -o json 2>/dev/null || true)"

blocked_reason=""
entry=""
if [ -z "$node_json" ]; then
  blocked_reason="노드 ${node} 를 찾을 수 없음"
elif [ -z "$ndr_json" ]; then
  blocked_reason="NodeDeviceReport ${node} 가 없음"
else
  entry="$(printf '%s' "$ndr_json" | jq -c --arg v "$vendor" 'first(.status.devices[]? | select(.vendor==$v)) // empty' 2>/dev/null || true)"
  if [ -z "$entry" ]; then
    blocked_reason="NodeDeviceReport ${node} 에 vendor=${vendor} 엔트리 없음"
  fi
fi

# operator Deployment 이미지 태그
img="$(kubectl -n kcloud get deploy kcloud-operator -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null || true)"
if [ -n "$img" ]; then kcloud_version="${img##*:}"; else kcloud_version="$UNCOLLECTED"; fi

commit="$(read_build_commit)"
k8s_json="$(kubectl version -o json 2>/dev/null || true)"
k8s_version="$(jq_field "$k8s_json" '.serverVersion.gitVersion')"

os_str="$(jq_field "$node_json" '.status.nodeInfo.osImage')"
kernel="$(jq_field "$node_json" '.status.nodeInfo.kernelVersion')"
runtime="$(jq_field "$node_json" '.status.nodeInfo.containerRuntimeVersion')"

device="$(jq_field "$entry" '.model')"
driver="$(jq_field "$entry" '.driverVersion')"
firmware="$(jq_field "$entry" '.firmwareVersion')"
sdk="$(jq_field "$entry" '.sdk')"   # NDR 에 SDK 필드가 없어 기본은 미수집

# NDR 에 없지만 알려진 값(플래시 번들 펌웨어·SDK 런타임 버전)은 --firmware/--sdk 로 채운다.
if [ -n "$firmware_override" ]; then firmware="$firmware_override"; fi
if [ -n "$sdk_override" ]; then sdk="$sdk_override"; fi

# resourceName: --resource override 또는 node allocatable 에서 도메인에 벤더+"." 포함 키
if [ -n "$resource_override" ]; then
  resource_name="$resource_override"
elif [ -n "$node_json" ]; then
  resource_name="$(printf '%s' "$node_json" | jq -r --arg v "$vendor" '
    [ (.status.allocatable // {}) | to_entries[] | .key
      | select( (split("/")[0]) as $d | ($d|contains($v)) and ($d|contains(".")) ) ]
    | first // "미수집"' 2>/dev/null || echo "$UNCOLLECTED")"
else
  resource_name="$UNCOLLECTED"
fi

# ---- 명령 실행 또는 blocked ----
actual_file="$(mktemp)"
trap 'rm -f "$actual_file"' EXIT

if [ -n "$blocked_reason" ]; then
  verdict="blocked"
  reason="$blocked_reason"
  duration=0
  cmd_rc="n/a"
  printf '(명령 미실행 — 전제 조건 미충족: %s)\n' "$blocked_reason" > "$actual_file"
elif [ -n "$actual_file_override" ]; then
  # 사전 캡처 주입: --command 는 그 캡처를 낸 재현 가능한 명령의 provenance,
  # actual 은 그 캡처 파일. 조립 시점에 명령을 재실행하지 않는다(라이브 조건이 이미 지나감).
  if [ ! -f "$actual_file_override" ]; then
    echo "actual-file 없음: ${actual_file_override}" >&2; exit 64
  fi
  cat "$actual_file_override" > "$actual_file"
  cmd_rc="n/a(사전 캡처)"
  duration=0
  if grep -qF -- "$expected" "$actual_file"; then
    verdict="pass"
    reason="actual 에 expected 문자열 포함(사전 캡처, 명령 미재실행)"
  else
    verdict="fail"
    reason="actual 에 expected 문자열 미포함(사전 캡처, 명령 미재실행)"
  fi
else
  start="$(date +%s)"
  set +e
  bash -c "$command_str" > "$actual_file" 2>&1
  cmd_rc=$?
  set -e
  end="$(date +%s)"
  duration=$((end - start))
  if grep -qF -- "$expected" "$actual_file"; then
    verdict="pass"
    reason="actual 에 expected 문자열 포함(명령 exit=${cmd_rc})"
  else
    verdict="fail"
    reason="actual 에 expected 문자열 미포함(명령 exit=${cmd_rc})"
  fi
fi
actual="$(cat "$actual_file")"

# ---- limits JSON 배열 ----
if [ "${#limits[@]}" -eq 0 ]; then
  limits_json="[]"
else
  limits_json="$(printf '%s\n' "${limits[@]}" | jq -R . | jq -s .)"
fi

# ---- 출력 prefix ----
if [ -z "$out" ]; then
  out="./evidence-${vendor}-${node}-${date_str}"
fi
out_dir="$(dirname "$out")"
mkdir -p "$out_dir"

# ---- JSON 조립 ----
jq -n \
  --arg date "$date_str" \
  --arg kcloudVersion "$kcloud_version" \
  --arg commit "$commit" \
  --arg k8sVersion "$k8s_version" \
  --arg os "$os_str" \
  --arg kernel "$kernel" \
  --arg containerRuntime "$runtime" \
  --arg device "$device" \
  --arg driver "$driver" \
  --arg firmware "$firmware" \
  --arg sdk "$sdk" \
  --arg resourceName "$resource_name" \
  --arg backend "$backend" \
  --arg mode "$part_mode" \
  --arg command "$command_str" \
  --arg expected "$expected" \
  --arg actual "$actual" \
  --argjson duration "$duration" \
  --arg verdict "$verdict" \
  --arg reason "$reason" \
  --arg node "$node" \
  --arg vendor "$vendor" \
  --argjson limits "$limits_json" \
  --arg liveRunUtc "$run_utc" \
  --arg cmdExit "$cmd_rc" \
  '{date:$date, kcloudVersion:$kcloudVersion, commit:$commit, k8sVersion:$k8sVersion,
    os:$os, kernel:$kernel, containerRuntime:$containerRuntime,
    device:$device, driver:$driver, firmware:$firmware, sdk:$sdk,
    resourceName:$resourceName, backend:$backend, mode:$mode,
    command:$command, expected:$expected, actual:$actual, duration:$duration,
    verdict:$verdict, reason:$reason, node:$node, vendor:$vendor, limits:$limits,
    liveRunUtc:$liveRunUtc, commandExit:$cmdExit}' > "${out}.json"

# ---- Markdown 조립 (4-backtick fence 로 actual 내부 3-backtick 충돌 회피) ----
commit_md="${BT}${commit}${BT}"
command_md="${BT}${command_str}${BT}"
expected_md="${BT}${expected}${BT}"
# id_suffix 가 있으면 title/doc_id 에 덧붙여 같은 노드·벤더·날짜의 다른 측정 종류를
# 구분한다(기본은 빈 문자열 — 기존 레코드와 동작이 같다).
title_suffix=""
doc_id_suffix=""
if [ -n "$id_suffix" ]; then
  title_suffix=" (${id_suffix})"
  doc_id_suffix="-${id_suffix}"
fi
{
  printf -- '---\n'
  printf -- 'title: "evidence: %s / %s — %s%s"\n' "$vendor" "$node" "$date_str" "$title_suffix"
  printf -- 'description: "evidence 레코드(JSON 짝: %s.json). test/collect/evidence.sh 자동 생성, 라이브 실행 UTC %s."\n' "$(basename "$out")" "$run_utc"
  printf -- 'created: %s\n' "$date_str"
  printf -- 'updated: %s\n' "$date_str"
  printf -- 'status: evidence\n'
  printf -- 'doc_id: operator-evidence-%s-%s-%s%s\n' "$vendor" "$node" "$date_str" "$doc_id_suffix"
  printf -- '---\n\n'
  printf -- '# evidence: %s / %s\n\n' "$vendor" "$node"
  printf -- '- 날짜: %s / kcloud %s / 커밋 kcloud-operator %s / Kubernetes %s\n' "$date_str" "$kcloud_version" "$commit_md" "$k8s_version"
  printf -- '- 노드: %s / OS %s / 커널 %s / 컨테이너 런타임 %s\n' "$node" "$os_str" "$kernel" "$runtime"
  printf -- '- 장치: %s / 드라이버: %s / 펌웨어: %s / SDK·런타임: %s\n' "$device" "$driver" "$firmware" "$sdk"
  printf -- '- 리소스명: %s / 할당 backend: %s / 파티션·공유 모드: %s\n' "$resource_name" "$backend" "$part_mode"
  printf -- '- 테스트 명령: %s\n' "$command_md"
  printf -- '- 기대 결과: %s 문자열이 실제 출력에 포함될 것\n' "$expected_md"
  printf -- '- 판정: **%s** — %s\n' "$verdict" "$reason"
  printf -- '- 소요 시간: %s초\n\n' "$duration"
  printf -- '## 한계\n\n'
  if [ "${#limits[@]}" -eq 0 ]; then
    printf -- '- 기록된 한계 없음\n\n'
  else
    for l in "${limits[@]}"; do printf -- '- %s\n' "$l"; done
    printf -- '\n'
  fi
  printf -- '## 실제 결과 (명령 stdout+stderr, verbatim)\n\n'
  printf -- '````text\n'
  cat "$actual_file"
  printf -- '\n````\n'
} > "${out}.md"

echo "생성: ${out}.json / ${out}.md"

# ---- 자체 스키마 검증 ----
validate_record "${out}.json" "$schema" || true

case "$verdict" in
  pass) exit 0 ;;
  fail) exit 1 ;;
  blocked) exit 2 ;;
esac
