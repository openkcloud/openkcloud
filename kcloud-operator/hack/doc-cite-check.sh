#!/usr/bin/env bash
# ============================================================
# doc-cite-check.sh: 코드·설정·문서가 가리키는 docs/*.md 경로가 실재하는지 검사
# 상세: git 추적 파일에서 docs/<경로>.md·../docs/<경로>.md 인용이 가리키는 파일이
#       실재하는지 검사한다(판정 근거는 scan()·self_test() 의 본문 주석 참고).
# 생성일: 2026-10-05 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

# 인용을 데이터로 들고 있는 줄(표 정렬 픽스처, 이 스크립트의 자가시험 문자열)은 줄 끝에
# "doc-cite-check: ignore" 표식을 둔다 — 파일 전체가 아니라 그 줄만 검사에서 빠진다.
IGNORE_MARK='doc-cite-check: ignore'
# 형제 저장소 참조 ../docs/…md 는 앞이 영숫자·_·. 만 아니면 인용으로 본다 — 쉘 변수 뒤의
# $(DIR)/../docs 도 잡지만, ../../docs 처럼 연쇄된 상위참조는 바로 앞 세 글자가 "../" 라
# 제외한다(report.write("../../docs/x.md") 같은 쓰기 경로를 인용으로 오인하지 않는다).
# 트리 안 bare docs/…md 는 앞 글자가 경로 문자(영숫자 _ / . -)가 아니어야 한다 — URL 의
# /docs/x.md 가 빠진다. 둘 다 저장소 루트 기준 상대경로로 해석한다.
CITE_RE='(?:(?<!\.\./)(?<![[:alnum:]_.])\.\./|(?<![[:alnum:]_/.-]))docs/[[:alnum:]_./-]+\.md'

# scan <루트>: 표준출력에 없는 경로만 "파일:줄: 경로", 표준오류에 집계.
# 종료 0=없는 경로 없음(인용 0건 포함), 1=없는 경로 있음, 2=스캔 자체 실패(git 저장소 아님,
# grep 오류). ../docs 인용은 <루트>/../docs 가 있을 때만 검사하고 없으면 건너뛴 수만 적는다.
scan() (
  local root="$1" total=0 missing=0 skipped=0 sibling=0 f n p hits rc
  cd "$root" || { echo "루트 디렉터리 없음: $root" >&2; return 2; }
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "git 저장소가 아니다: $root" >&2; return 2; }
  [ -d ../docs ] && sibling=1
  # 추적 파일만, 한 프로세스로. 종료 0=매치, 1=매치 없음, 그 외=오류(0건 통과로 둔갑시키지 않는다).
  rc=0; hits="$(git grep -I -noP "$CITE_RE" -- '*.go' '*.sh' '*.yaml' '*.yml' '*.md' '*.tpl' 'Makefile' '*/Makefile')" || rc=$?
  [ "$rc" -le 1 ] || { echo "git grep 실패(종료 $rc) — 정규식이나 git 상태를 확인" >&2; return 2; }
  while IFS=: read -r f n p; do
    [ -n "$f" ] || continue
    # 표식이 있는 줄의 인용은 데이터다 — 건너뛴다
    if sed -n "${n}p" "$f" | grep -qF "$IGNORE_MARK"; then
      continue
    fi
    if [[ "$p" == ../docs/* ]] && [ "$sibling" -eq 0 ]; then
      skipped=$((skipped + 1))
      continue
    fi
    total=$((total + 1))
    # 트리 안 경로는 "추적 중" 이어야 실재다 — 디스크에만 있는(추적 제외) 문서는 CI 체크아웃에 없다.
    if [[ "$p" == ../docs/* ]]; then
      [ -e "$p" ] || { echo "$f:$n: $p"; missing=$((missing + 1)); }
    elif ! git ls-files --error-unmatch -- "$p" >/dev/null 2>&1; then
      echo "$f:$n: $p"
      missing=$((missing + 1))
    fi
  done <<<"$hits"
  echo "인용 ${total}건, 없는 경로 ${missing}건, 형제 저장소 부재로 건너뜀 ${skipped}건" >&2
  [ "$missing" -eq 0 ]
)

self_test() {
  local tmp out err rc
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/repo/docs" "$tmp/docs"
  git -C "$tmp/repo" init -q
  echo '# ok' > "$tmp/repo/docs/ok.md"
  echo '# 디스크에만 있고 추적하지 않는 문서' > "$tmp/repo/docs/untracked.md"
  echo '# ok' > "$tmp/docs/sib-ok.md"
  printf '// 근거: docs/ok.md 와 docs/missing.md 와 docs/untracked.md, ../docs/sib-ok.md 와 ../docs/sib-missing.md, https://example.com/x/docs/url.md\n' > "$tmp/repo/x.go"  # doc-cite-check: ignore
  printf '// 표식 줄: docs/ignored-missing.md  // doc-cite-check: ignore\n// 표식 없는 줄: docs/also-missing.md\n' > "$tmp/repo/y.go"  # doc-cite-check: ignore
  git -C "$tmp/repo" add x.go y.go docs/ok.md  # doc-cite-check: ignore
  # 1) 트리 안 부재·미추적·형제 부재는 찍히고, URL 과 표식 줄은 빠진다
  rc=0; out="$(scan "$tmp/repo" 2>"$tmp/err")" || rc=$?
  [ "$rc" -eq 1 ] || { echo "self-test 1: 종료 $rc (기대 1)" >&2; return 1; }
  [ "$out" = $'x.go:1: docs/missing.md\nx.go:1: docs/untracked.md\nx.go:1: ../docs/sib-missing.md\ny.go:2: docs/also-missing.md' ] || { echo "self-test 1: 출력 '$out'" >&2; return 1; }  # doc-cite-check: ignore
  [[ "$out" != *ignored-missing* ]] || { echo "self-test 1: 표식 줄이 집계됨" >&2; return 1; }
  grep -q '형제 저장소 부재로 건너뜀 0건' "$tmp/err" || { echo "self-test 1: 집계 '$(cat "$tmp/err")'" >&2; return 1; }
  # 2) 형제 저장소가 없으면 ../docs 인용은 건너뛰고 트리 안 인용만 본다
  rm -rf "$tmp/docs"
  rc=0; out="$(scan "$tmp/repo" 2>"$tmp/err")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = $'x.go:1: docs/missing.md\nx.go:1: docs/untracked.md\ny.go:2: docs/also-missing.md' ] || { echo "self-test 2: 종료 $rc 출력 '$out'" >&2; return 1; }  # doc-cite-check: ignore
  grep -q '형제 저장소 부재로 건너뜀 2건' "$tmp/err" || { echo "self-test 2: 집계 '$(cat "$tmp/err")'" >&2; return 1; }
  # 3) 인용 0건은 통과
  : > "$tmp/repo/x.go"; : > "$tmp/repo/y.go"; git -C "$tmp/repo" add x.go y.go
  rc=0; err="$(scan "$tmp/repo" 2>&1 >/dev/null)" || rc=$?
  [ "$rc" -eq 0 ] && [[ "$err" == *"인용 0건"* ]] || { echo "self-test 3: 종료 $rc 표준오류 '$err'" >&2; return 1; }
  # 4) git 저장소가 아니면 스캔 실패(종료 2) — 0건 통과로 둔갑하지 않는다
  mkdir -p "$tmp/notgit"
  rc=0; err="$(scan "$tmp/notgit" 2>&1 >/dev/null)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 4: git 저장소 아닌 곳에서 종료 $rc (기대 2) '$err'" >&2; return 1; }
  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) scan "${1:-$(git rev-parse --show-toplevel)}" ;;
esac
