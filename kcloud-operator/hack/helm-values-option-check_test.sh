#!/usr/bin/env bash
# ============================================================
# helm-values-option-check_test.sh: helm-values-option-check.sh 의 판정을 시험한다
# 상세: 가짜 트리로 검사 1(같은 줄 helm+--reuse-values 조합)을 시험한다. 검사 2(값 키
#       검사)는 별도 픽스처가 필요하다 — write_min_chart() 가 만드는 values.yaml·
#       values.schema.json 으로 합집합·표 파싱·와일드카드·allOf 가지 합치기를 run2 로
#       시험한다(세부 근거는 run2()·write_min_chart() 주석 참고).
# 생성일: 2026-09-24 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/helm-values-option-check.sh"
pass=0; fail=0
run() { # run <설명> <기대 종료코드> <트리 준비 함수>
  local desc="$1" want="$2" prep="$3" tmp rc
  tmp="$(mktemp -d)"; "$prep" "$tmp"
  set +e; "$CHECK" "$tmp" >/dev/null 2>&1; rc=$?; set -e
  rm -rf "$tmp"
  if [ "$rc" = "$want" ]; then pass=$((pass+1)); echo "PASS: $desc"
  else fail=$((fail+1)); echo "FAIL: $desc (exit=$rc, want=$want)"; fi
}
prep_clean() { printf 'helm upgrade r c --reset-then-reuse-values\n' > "$1/ok.sh"; }
prep_dirty() { printf 'helm upgrade r c --reuse-values --set a=b\n' > "$1/bad.sh"; }
prep_comment_only() { printf '# --reuse-values 는 쓰지 않는다\n' > "$1/doc.md"; }
prep_doc_with_helm() { printf '값을 바꿔 반영할 때 helm upgrade 에 --reuse-values 를 쓰지 않는다.\n' > "$1/README.md"; }
prep_sh_diff_lines() { printf 'helm upgrade r c --reset-values\n# 주석: --reuse-values 는 쓰지 않는다\n' > "$1/note.sh"; }
run "정상 옵션만 있으면 통과" 0 prep_clean
run "sh 파일 같은 줄에 단독 사용이 있으면 실패" 1 prep_dirty
run "금지를 설명하는 문장(비 sh, helm 없음)은 통과" 0 prep_comment_only
run "금지를 설명하는 문장(비 sh, helm 있음)도 확장자로 통과" 0 prep_doc_with_helm
run "sh 파일이라도 다른 줄이면 통과" 0 prep_sh_diff_lines

# --- 검사 2(값 키 검사) 전용 픽스처: 실제 차트의 driverInstallPolicies.<vendor> allOf 구조
#     (벤더별 제외 제약)를 그대로 축소한 조각이다. nvidia 는 toolkit 허용·trackOnly 금지,
#     tenstorrent 는 반대, furiosa 는 toolkit 자체가 없다 — 이 비대칭이 있어야 "가지를
#     합쳐 재귀하는지"와 "가지를 따로 시도해 하나만 맞아도 통과시키는지"를 가른다.
#     nvidia.toolkit 에는 image·nvidia.driver 에는 trackOnly 를 일부러 선언하지 않는다 —
#     그래야 그 둘의 통과/거절이 데이터가 아니라 스키마 쪽 판정만으로 성립하는지 확인된다.
write_min_chart() {
  mkdir -p "$1/deploy/helm"
  cat > "$1/deploy/helm/values.yaml" <<'YAML'
rebellions:
  enabled: true
  devicePluginUpstream: "docker.io"
  devicePluginRepository: "rebellions/k8s-device-plugin"
driverInstallPolicies:
  nvidia:
    enabled: true
    toolkit:
      enabled: true
    driver:
      version: "1.0"
  furiosa:
    enabled: true
    driver:
      version: "1.0"
  tenstorrent:
    enabled: true
    driver:
      version: "1.0"
YAML
  cat > "$1/deploy/helm/values.schema.json" <<'JSON'
{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "rebellions": {
      "type": "object",
      "additionalProperties": false,
      "properties": { "enabled": {}, "devicePluginUpstream": {}, "devicePluginRepository": {} }
    },
    "driverInstallPolicies": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "nvidia": { "allOf": [{ "$ref": "#/$defs/base" }, { "$ref": "#/$defs/noTrack" }] },
        "furiosa": { "allOf": [{ "$ref": "#/$defs/base" }, { "$ref": "#/$defs/noToolkit" }, { "$ref": "#/$defs/noTrack" }] },
        "tenstorrent": { "allOf": [{ "$ref": "#/$defs/base" }, { "$ref": "#/$defs/noToolkit" }] }
      }
    }
  },
  "$defs": {
    "base": {
      "properties": {
        "enabled": {},
        "toolkit": {
          "type": "object",
          "additionalProperties": false,
          "properties": { "enabled": {}, "image": {} }
        },
        "driver": {
          "type": "object",
          "additionalProperties": false,
          "properties": { "version": {}, "trackOnly": {} }
        }
      }
    },
    "noToolkit": { "properties": { "toolkit": false } },
    "noTrack": { "properties": { "driver": { "properties": { "trackOnly": false } } } }
  }
}
JSON
}

# run2 <설명> <기대 종료코드> <README.md 로 쓸 전체 내용>
# "건너뜀" 이 출력에 있으면 그 자체로 실패 처리한다 — 건너뜀이 성공(exit 0)으로
# 집계되면 검사 2 를 아예 안 돌리고도 시험이 통과해 버리는 함정이 되살아난다.
run2() {
  local desc="$1" want="$2" content="$3" tmp rc out
  tmp="$(mktemp -d)"
  write_min_chart "$tmp"
  printf '%s\n' "$content" > "$tmp/README.md"
  set +e; out="$("$CHECK" "$tmp" 2>&1)"; rc=$?; set -e
  rm -rf "$tmp"
  if [ "$rc" != "$want" ]; then
    fail=$((fail+1)); echo "FAIL: $desc (exit=$rc, want=$want)"; return
  fi
  if printf '%s' "$out" | grep -q "건너뜀"; then
    fail=$((fail+1)); echo "FAIL: $desc (검사 2 가 건너뛰어짐 — values.yaml/schema 픽스처 확인)"; return
  fi
  pass=$((pass+1)); echo "PASS: $desc"
}

TABLE_HEAD='| 파라미터 | 기본값 | 설명 |
|---|---|---|'

run2 "표: 단순 오타 키 적발" 1 \
  "$TABLE_HEAD
| \`bogus.noSuchKey\` | \`\"\"\` | 존재하지 않는 키 |"

run2 "표: 와일드카드 불일치 적발" 1 \
  "$TABLE_HEAD
| \`rebellions.nosuch*\` | \`\"\"\` | 존재하지 않는 접두사 |"

run2 "표: 이어붙이기 실패(없는 형제 키) 적발" 1 \
  "$TABLE_HEAD
| \`rebellions.devicePluginUpstream\` / \`nosuchSibling\` | \`docker.io\` / \`x\` | 없는 형제 키 |"

run2 "--set: furiosa.toolkit.enabled 적발(벤더 제외)" 1 \
  '--set driverInstallPolicies.furiosa.toolkit.enabled=true'

run2 "--set: nvidia.toolkit.enabled 통과" 0 \
  '--set driverInstallPolicies.nvidia.toolkit.enabled=true'

run2 "--set: nvidia.driver.trackOnly 적발(벤더 제외)" 1 \
  '--set driverInstallPolicies.nvidia.driver.trackOnly=true'

run2 "--set: tenstorrent.driver.trackOnly 통과" 0 \
  '--set driverInstallPolicies.tenstorrent.driver.trackOnly=true'

run2 "--set: nvidia.toolkit.image 통과(기본값 없이 스키마만 허용)" 0 \
  '--set driverInstallPolicies.nvidia.toolkit.image=foo'

run2 "--set-string: furiosa.toolkit.enabled 적발" 1 \
  '--set-string driverInstallPolicies.furiosa.toolkit.enabled=true'

echo "PASS $pass / FAIL $fail"
[ "$fail" = 0 ]
