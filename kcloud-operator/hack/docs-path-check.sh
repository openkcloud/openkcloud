#!/usr/bin/env bash
# ============================================================
# docs-path-check.sh: 형제 docs 저장소 경로가 코드에 들어오는 것을 막는다
# 상세: docs 저장소는 remote 가 없어 이 저장소만 받은 사람은 docs/... 경로에 도달할 수
#       없다. 절대 경로 인용도 막는다. 종료 2 는 저장소 아님 또는 git grep 오류다.
#       판정 근거·경계 사례는 PATTERN·scan()·self_test() 의 본문 주석 참고.
# 생성일: 2026-09-30 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail

# 줄 단위 예외. 인용을 데이터로 들고 있는 줄(이 스크립트의 자가시험 문자열, 문서 저장소가
# 없으면 건너뛰는 시험 줄)은 줄 끝에 "docs-path-check: ignore" 표식을 둔다.
IGNORE_MARK='docs-path-check: ignore'
# 표식을 달 수 없는 파일의 정확한 줄. Makefile 의 줄 끝 주석은 help 문구나 변수 값에 섞이므로
# 줄 내용으로 고정한다 — capmatrix 가 문서 저장소로 매트릭스를 생성하는 대상 경로 세 줄이다.
ALLOW_LINES='^Makefile:[0-9]+:(capmatrix: ## Generate compatibility matrix into the docs repo \(\.\./docs/features/compatibility_matrix\.md\)\.|[[:space:]]go run \./cmd/capmatrix( -out| \| cmp -) \$\(MAKEFILE_DIR\)/\.\./docs/features/compatibility_matrix\.md)$'

DIRS='development|validation|feasibility|research|records|operations|features|comparison|history|decisions|architecture'

# 경로 하나짜리 매치식. 앞 그룹의 첫 분기는 절대 경로, 둘째 분기는 선택적 ../ 다.
# 환경변수로는 덮이지 않는다. 자가시험만 호출 지점 대입(PATTERN='(' scan …)으로 잘못된 정규식을 넣는다.
DEFAULT_PATTERN="(^|[^A-Za-z0-9._/-])(/([A-Za-z0-9._-]+/)+|(\\.\\./)?)(docs/)?($DIRS)/[A-Za-z0-9._/-]+\\.(md|json)"
PATTERN="$DEFAULT_PATTERN"

# scan <루트>: 위반 유무를 판정. 종료 0=위반 없음, 1=위반 있음, 2=루트가 git 저장소가 아니거나 git grep 오류.
scan() (
  local root="$1" hits rc=0
  cd "$root" || { echo "루트 디렉터리 없음: $root" >&2; return 2; }
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "git 저장소가 아니다: $root" >&2; return 2; }

  # 추적 파일만, 한 프로세스로. runs/ 아래는 실행이 그 시점에 낸 전사라 사후에 고치지 않는다.
  # 종료 0=적중, 1=무적중, 그 외=오류 — 오류를 위반 없음으로 처리하지 않는다.
  # URL 안의 경로는 정규식이 이미 거부한다 — `scheme://host/dir/x.md` 의 `dir` 앞 글자가 `/`
  # 라 선두 문자 클래스에 걸린다. 별도로 "URL 을 지운 사본으로 재판정"하는 단계는 두지 않는다 —
  # 그런 치환은 다음 공백까지 먹어 URL 과 쉼표·괄호로 붙은 실제 위반까지 지운다(자가시험 4·5 참고).
  hits=$(git grep -nIE "$PATTERN" -- . ':(exclude)*/runs/*') || rc=$?
  [ "$rc" -le 1 ] || { echo "git grep 실패(종료 $rc) — 위반 없음으로 처리하지 않는다" >&2; return 2; }
  hits=$(printf '%s\n' "$hits" | grep -vF "$IGNORE_MARK" | grep -vE "$ALLOW_LINES" || true)

  if [ -n "$hits" ]; then
    echo "형제 docs 저장소 경로가 코드에 있다. 그 저장소는 remote 가 없어 도달할 수 없다."
    echo "근거는 test/live/ 하네스나 *_test.go 로 적고, 사실과 날짜는 산문으로 남겨라."
    echo
    echo "$hits"
    return 1
  fi
  echo "docs 경로 검사 통과"
)

# self_test: scan() 자체를 임시 저장소에서 검증한다. 정규식을 넓히는 변경은 조용히 안
# 잡게 되기 쉬우므로, 넓힌 뒤에도 이 아홉 경우가 전부 통과해야 한다.
self_test() {
  local tmp out rc expected
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/repo/internal" "$tmp/notgit"
  git -C "$tmp/repo" init -q
  printf '// ../docs/decisions/x.md\n' > "$tmp/repo/internal/a.go"  # doc-cite-check: ignore  # docs-path-check: ignore
  printf '// https://example.com/docs/decisions/x.md\n' > "$tmp/repo/internal/b.go"
  git -C "$tmp/repo" add internal/a.go internal/b.go

  # 1) 상대경로 ../docs/... 는 잡힌다
  expected=$'형제 docs 저장소 경로가 코드에 있다. 그 저장소는 remote 가 없어 도달할 수 없다.\n근거는 test/live/ 하네스나 *_test.go 로 적고, 사실과 날짜는 산문으로 남겨라.\n\ninternal/a.go:1:// ../docs/decisions/x.md'  # doc-cite-check: ignore  # docs-path-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 1: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 2) URL 안의 경로는 잡히지 않는다 — 결정적 기전은 정규식의 선두 문자 클래스다. docs/
  #    앞 글자가 / 라 매치가 시작되지 못한다. 별도의 URL 제외 단계는 없다 — 정규식만으로 0건이다.
  rm "$tmp/repo/internal/a.go"; git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "docs 경로 검사 통과" ] || { echo "self-test 2: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 3) 같은 줄에 URL 과 실제 위반이 같이 있으면 그 줄 전체(URL 포함)가 출력된다. 정규식이
  #    위반 쪽을 직접 잡으므로 URL 이 붙어 있어도 가려지지 않는다 — 줄 단위 버리기나 URL
  #    지우기 단계가 있으면 이 경우가 조용히 통과한다.
  rm "$tmp/repo/internal/b.go"
  printf '// 참고: https://example.com/x 와 ../docs/decisions/real.md\n' > "$tmp/repo/internal/c.go"  # doc-cite-check: ignore  # docs-path-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'형제 docs 저장소 경로가 코드에 있다. 그 저장소는 remote 가 없어 도달할 수 없다.\n근거는 test/live/ 하네스나 *_test.go 로 적고, 사실과 날짜는 산문으로 남겨라.\n\ninternal/c.go:1:// 참고: https://example.com/x 와 ../docs/decisions/real.md'  # doc-cite-check: ignore  # docs-path-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 3: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 4) URL 과 위반이 쉼표로 붙어 있어도(공백 없음) 위반 자체의 선두 경계가 쉼표라 잡힌다 —
  #    URL 을 지운 사본으로 재판정하던 옛 단계라면 쉼표 뒤까지 먹어 놓쳤을 자리다.
  rm "$tmp/repo/internal/c.go"
  printf '// 참고: https://example.com/x,../docs/decisions/real.md 확인\n' > "$tmp/repo/internal/d.go"  # doc-cite-check: ignore  # docs-path-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'형제 docs 저장소 경로가 코드에 있다. 그 저장소는 remote 가 없어 도달할 수 없다.\n근거는 test/live/ 하네스나 *_test.go 로 적고, 사실과 날짜는 산문으로 남겨라.\n\ninternal/d.go:1:// 참고: https://example.com/x,../docs/decisions/real.md 확인'  # doc-cite-check: ignore  # docs-path-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 4: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 5) 괄호로 붙어도 같은 이유로 잡힌다.
  rm "$tmp/repo/internal/d.go"
  printf '// 참고: (https://example.com/x)../docs/decisions/real.md 확인\n' > "$tmp/repo/internal/e.go"  # doc-cite-check: ignore  # docs-path-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'형제 docs 저장소 경로가 코드에 있다. 그 저장소는 remote 가 없어 도달할 수 없다.\n근거는 test/live/ 하네스나 *_test.go 로 적고, 사실과 날짜는 산문으로 남겨라.\n\ninternal/e.go:1:// 참고: (https://example.com/x)../docs/decisions/real.md 확인'  # doc-cite-check: ignore  # docs-path-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 5: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 6) 위반 0건이면 통과
  rm "$tmp/repo/internal/e.go"; git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "docs 경로 검사 통과" ] || { echo "self-test 6: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 7) git 저장소가 아니면 종료 2 — 위반 0건 통과로 둔갑하지 않는다
  rc=0; out="$(scan "$tmp/notgit" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 7: git 저장소 아닌 곳에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  # 8) 절대 경로 인용도 잡는다 → 종료 1
  printf '// 근거: /home/u/x/docs/decisions/y.md\n' > "$tmp/repo/internal/f.go"  # docs-path-check: ignore
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"internal/f.go:1:"* ]] || { echo "self-test 8: 종료 $rc 출력 '$out'" >&2; return 1; }
  rm "$tmp/repo/internal/f.go"; git -C "$tmp/repo" add -A

  # 9) git grep 오류(잘못된 정규식)는 통과가 아니라 종료 2
  rc=0; out="$(PATTERN='(' scan "$tmp/repo" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 9: 정규식 오류에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) scan "${1:-$(dirname "$0")/..}" ;;
esac
