#!/usr/bin/env bash
# ============================================================
# run.sh: Rebellions rbln-k8s-dra-driver 벤더 CDI 수정 반영 여부 재확인(읽기 전용)
# 상세: kubelet plugin(cdi.go)의 rblnRuntimeSpec 이 CDI 최상위 containerEdits 를 읽도록
#       고쳐졌는지 GitHub 에서 읽기 전용으로 확인한다(판정·안전 범위는 README 참고).
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

REPO="rbln-sw/rbln-k8s-dra-driver"
STRUCT_NAME="rblnRuntimeSpec"
FILE_PATH="cmd/npu-kubelet-plugin/cdi.go"
DEFAULT_BRANCH="main"

DRY_RUN="${DRY_RUN:-0}"
OUT="${OUT:-/tmp/rebellions-dra-recheck-evidence}"
mkdir -p "$OUT"

# 상태 변수.
DEMO_DONE=false
BLOCKED=false
BLOCKED_REASON=""
FIXED=false
REL_TAG=""
REL_PUBLISHED=""
FIELDS_FOUND=""
MAIN_STATE=""
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# 구조체 하나의 본문만 잘라 ContainerEdits 필드/태그 존재를 판정하는 파서.
# 중괄호 깊이를 세어 그 구조체의 닫는 '}' 까지만 잘라내므로, 이름이 다른 구조체(예:
# 장치별 containerEdits 를 이미 갖는 rblnRuntimeDevice)를 오검출하지 않는다.
# 인자: <go 파일 경로> <구조체 이름> <추출한 블록을 쓸 경로>
# 출력(stdout, 탭 구분 1줄): found(0/1) has_field(0/1) has_tag(0/1) 필드이름CSV
PY_PARSE_STRUCT=$(cat <<'PYEOF'
import re, sys

path, struct_name, block_out = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path, encoding="utf-8", errors="replace").read()

m = re.search(r'^type\s+' + re.escape(struct_name) + r'\s+struct\s*\{', text, re.MULTILINE)
if not m:
    open(block_out, "w").write("")
    print("0\t0\t0\t")
    sys.exit(0)

i, depth = m.end(), 1
while i < len(text) and depth > 0:
    if text[i] == "{":
        depth += 1
    elif text[i] == "}":
        depth -= 1
    i += 1
body = text[m.end():i - 1]
open(block_out, "w").write(body)

fields = re.findall(r'^\s*([A-Z][A-Za-z0-9_]*)\s+\S', body, re.MULTILINE)
has_field = "ContainerEdits" in fields
has_tag = bool(re.search(r'`[^`\n]*containerEdits[^`\n]*`', body))

print("1\t%d\t%d\t%s" % (int(has_field), int(has_tag), ",".join(fields)))
PYEOF
)

# ── preflight: 도구 확인 + 벤더 최신 릴리스 메타데이터 조회(네트워크 probe 겸용) ──────
preflight() {
  PHASE="preflight"
  log "### preflight: 도구 확인 + 벤더 최신 릴리스 조회"
  local c
  for c in curl jq python3; do
    if ! command -v "$c" >/dev/null 2>&1; then
      log "BLOCKED: 필요한 도구 없음: $c"
      BLOCKED=true; BLOCKED_REASON="tooling"
      return 1
    fi
  done
  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] + curl https://api.github.com/repos/$REPO/releases/latest (생략)"
    return 0
  fi
  local rel_json="$OUT/release-latest.json"
  if ! curl -fsS -m 20 "https://api.github.com/repos/$REPO/releases/latest" -o "$rel_json"; then
    log "BLOCKED: releases/latest 조회 실패(네트워크 또는 rate limit)"
    BLOCKED=true; BLOCKED_REASON="network"
    return 1
  fi
  REL_TAG="$(jq -r '.tag_name // empty' "$rel_json")"
  REL_PUBLISHED="$(jq -r '.published_at // empty' "$rel_json")"
  if [ -z "$REL_TAG" ]; then
    log "BLOCKED: releases/latest 응답에 tag_name 없음 — rate limit 메시지일 수 있음"
    BLOCKED=true; BLOCKED_REASON="network"
    return 1
  fi
  log "  최신 릴리스: $REL_TAG (공개일 $REL_PUBLISHED)"
}

# ── demo: 최신 릴리스 태그 + main 브랜치의 cdi.go 를 읽어 수정 반영 여부 판정 ──────────
demo() {
  PHASE="demo"
  if [ "$DRY_RUN" = "1" ]; then
    log ""
    log "### demo [DRY_RUN]: 아래 조회만 수행한다(상태 변경 없음, 네트워크 호출 생략)"
    log "  + curl https://raw.githubusercontent.com/$REPO/<최신태그>/$FILE_PATH"
    log "  + curl https://raw.githubusercontent.com/$REPO/$DEFAULT_BRANCH/$FILE_PATH"
    log "  + python3 로 '$STRUCT_NAME' 구조체 블록만 추출해 ContainerEdits 필드/태그 확인"
    DEMO_DONE=true
    return 0
  fi

  local tag_go="$OUT/cdi-${REL_TAG}.go"
  if ! curl -fsS -m 20 "https://raw.githubusercontent.com/$REPO/$REL_TAG/$FILE_PATH" -o "$tag_go"; then
    log "BLOCKED: $REL_TAG 의 $FILE_PATH 조회 실패(네트워크)"
    BLOCKED=true; BLOCKED_REASON="network"
    return 1
  fi

  local res found has_field has_tag fields
  res="$(python3 -c "$PY_PARSE_STRUCT" "$tag_go" "$STRUCT_NAME" "$OUT/struct-block-${REL_TAG}.txt")"
  IFS=$'\t' read -r found has_field has_tag fields <<<"$res"
  FIELDS_FOUND="$fields"

  if [ "$found" != "1" ]; then
    log "BLOCKED: $tag_go 에서 '$STRUCT_NAME' 구조체를 찾지 못함 — 상위 파일 구조 변경, 수동 확인 필요"
    BLOCKED=true; BLOCKED_REASON="parse"
    return 1
  fi

  if [ "$has_field" = "1" ] || [ "$has_tag" = "1" ]; then
    FIXED=true
    log "PASS: $REL_TAG 에 '$STRUCT_NAME.ContainerEdits' 반영 확인(필드=$has_field, containerEdits 태그=$has_tag)"
    log "  다음 단계: README 의 '반영 branch' 절차(미러 이미지 재빌드 + 라이브 재검증) 참고"
  else
    FIXED=false
    log "BLOCKED(external): $REL_TAG 에 최상위 ContainerEdits 미반영 — 확인된 필드: ${fields:-(없음)}"
    BLOCKED=true; BLOCKED_REASON="not_reflected"
  fi

  # main 브랜치는 참고용이다 — 판정(PASS/BLOCKED)은 항상 "최신 릴리스" 기준이다.
  # 릴리스 전 main 에만 반영된 경우를 "unreleased fix on main" 으로 구분해 알린다.
  local main_go="$OUT/cdi-${DEFAULT_BRANCH}.go"
  if curl -fsS -m 20 "https://raw.githubusercontent.com/$REPO/$DEFAULT_BRANCH/$FILE_PATH" -o "$main_go"; then
    local res2 m_found m_field m_tag m_fields
    res2="$(python3 -c "$PY_PARSE_STRUCT" "$main_go" "$STRUCT_NAME" "$OUT/struct-block-${DEFAULT_BRANCH}.txt")"
    IFS=$'\t' read -r m_found m_field m_tag m_fields <<<"$res2"
    if [ "$m_found" = "1" ] && { [ "$m_field" = "1" ] || [ "$m_tag" = "1" ]; }; then
      if [ "$FIXED" = true ]; then
        MAIN_STATE="fixed"
      else
        MAIN_STATE="fixed_unreleased"
        log "  참고: $DEFAULT_BRANCH 브랜치에는 반영되어 있으나 아직 릴리스되지 않음(unreleased fix on main)"
      fi
    elif [ "$m_found" = "1" ]; then
      MAIN_STATE="not_fixed"
      log "  참고: $DEFAULT_BRANCH 브랜치도 미반영 — 최신 릴리스와 동일 상태(필드: ${m_fields:-(없음)})"
    else
      MAIN_STATE="parse_error"
      log "  참고: $DEFAULT_BRANCH 브랜치 구조체 파싱 실패(판정에는 영향 없음)"
    fi
  else
    MAIN_STATE="fetch_error"
    log "  참고: $DEFAULT_BRANCH 브랜치 조회 실패(판정에는 영향 없음)"
  fi

  DEMO_DONE=true
}

# ── cleanup: 읽기 전용이라 되돌릴 것이 없다 ───────────────────────────────
cleanup() {
  PHASE="cleanup"
  log "### cleanup: 읽기 전용 조회만 수행 — 되돌릴 상태 변경 없음"
}

# ── finish: JSON 요약 한 줄 + 종료코드 ────────────────────────────────────
finish() {
  cleanup || true
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then
    verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then
    verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FIXED" = true ]; then
    verdict="PASS"
  fi
  printf '{"test":"rebellions-dra-recheck","repo":"%s","struct":"%s","latestTag":"%s","publishedAt":"%s","checkedAt":"%s","fieldsFound":"%s","fixed":%s,"defaultBranch":"%s","mainState":"%s","phase":"%s","reason":"%s","verdict":"%s","dryRun":%s}\n' \
    "$REPO" "$STRUCT_NAME" "${REL_TAG:-}" "${REL_PUBLISHED:-}" "$(utc_now)" "${FIELDS_FOUND:-}" "$FIXED" "$DEFAULT_BRANCH" "${MAIN_STATE:-}" "$PHASE" "${BLOCKED_REASON:-}" "$verdict" "$dry" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "PASS" ] || [ "$verdict" = "DRY" ] || exit 1
  exit 0
}

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight || true
if [ "$BLOCKED" != true ]; then
  demo
fi
# EXIT trap(finish) 이 JSON 요약 출력 + 종료코드를 담당한다.
