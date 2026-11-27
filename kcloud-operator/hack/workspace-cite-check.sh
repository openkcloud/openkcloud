#!/usr/bin/env bash
# ============================================================
# workspace-cite-check.sh: 추적 파일이 작업공간 디렉터리나 task-N 보고서 경로를 인용하는 것을 막는다
# 상세: 작업공간 디렉터리와 그 안의 task-N 보고서(접미 임의)는 .gitignore 로 막혀 커밋되지
#       않는다. 절대 경로나 디렉터리 접두가 붙어도 인용하면 열 수 없는 경로를 안내받는다.
#       종료 2 는 저장소 아님 또는 git grep 오류다.
# 생성일: 2026-10-11 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail

# 줄 단위 예외. 인용을 데이터로 들고 있는 줄(이 스크립트의 규칙 설명·자가시험 문자열)은
# 줄 끝에 "workspace-cite-check: ignore" 표식을 둔다 — 파일 전체가 아니라 그 줄만 빠진다.
IGNORE_MARK='workspace-cite-check: ignore'
# 표식을 달 수 없는 파일의 정확한 줄. .gitignore·.helmignore 는 줄 끝 # 이 패턴 일부가 되므로
# 작업공간을 무시하는 그 규칙 줄 자체만 허용한다.
ALLOW_LINES='^(\.gitignore|deploy/helm/\.helmignore):[0-9]+:\.(superpowers|omc)/$'

# 패턴은 한 곳에만 정의한다. 앞 그룹의 첫 분기는 절대 경로, 둘째 분기는 (\.\./)* 상위참조와
# 디렉터리 접두(몇 단계든)다 — 디렉터리 뒤에 붙은 작업공간 인용도 잡는다.
# 환경변수로는 덮이지 않는다. 자가시험만 호출 지점 대입(PATTERN='(' scan …)으로 잘못된 정규식을 넣는다.
DEFAULT_PATTERN='(^|[^A-Za-z0-9._/-])(/([A-Za-z0-9._-]+/)+|(\.\./)*([A-Za-z0-9._-]+/)*)(\.superpowers/|\.omc/|task-[0-9]+-[a-z0-9-]+\.md)'  # workspace-cite-check: ignore
PATTERN="$DEFAULT_PATTERN"

# scan <루트>: 위반 유무를 판정. 종료 0=위반 없음, 1=위반 있음, 2=루트가 git 저장소가 아니거나 git grep 오류.
scan() (
  local root="$1" hits rc=0
  cd "$root" || { echo "루트 디렉터리 없음: $root" >&2; return 2; }
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "git 저장소가 아니다: $root" >&2; return 2; }

  # 추적 파일만, 한 프로세스로. /runs/ 아래는 실행이 그 시점에 낸 전사라 제외. 종료 0=적중,
  # 1=무적중, 그 외=오류 — 오류를 위반 없음으로 처리하지 않는다. URL 지우기 같은 재판정
  # 단계는 두지 않는다(같은 줄의 위반을 놓치는 빈틈이었다).
  hits=$(git grep -nIE "$PATTERN" -- . ':(exclude)*/runs/*') || rc=$?
  [ "$rc" -le 1 ] || { echo "git grep 실패(종료 $rc) — 위반 없음으로 처리하지 않는다" >&2; return 2; }
  hits=$(printf '%s\n' "$hits" | grep -vF "$IGNORE_MARK" | grep -vE "$ALLOW_LINES" || true)

  if [ -n "$hits" ]; then
    echo "추적 파일이 .superpowers/·.omc/ 작업공간이나 task-N 보고서 경로를 인용한다."  # workspace-cite-check: ignore
    echo ".gitignore 로 막힌 경로라 받는 사람은 열 수 없는 경로를 안내받는다."
    echo
    echo "$hits"
    return 1
  fi
  echo "작업공간 인용 검사 통과"
)

# self_test: scan() 자체를 임시 저장소에서 검증한다. PATTERN 을 좁히는 변경은 조용히 안
# 잡게 되기 쉬우므로, 고친 뒤에도 이 아홉 경우가 전부 통과해야 한다.
self_test() {
  local tmp out rc expected
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/repo/internal" "$tmp/notgit"
  git -C "$tmp/repo" init -q

  # 1) bare task-N-report.md 인용 → 종료 1, 그 줄이 출력에 있다
  printf '// 참고: task-2-report.md 확인\n' > "$tmp/repo/internal/a.go"  # workspace-cite-check: ignore
  git -C "$tmp/repo" add internal/a.go
  expected=$'추적 파일이 .superpowers/·.omc/ 작업공간이나 task-N 보고서 경로를 인용한다.\n.gitignore 로 막힌 경로라 받는 사람은 열 수 없는 경로를 안내받는다.\n\ninternal/a.go:1:// 참고: task-2-report.md 확인'  # workspace-cite-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 1: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 2) 작업공간 디렉터리 경로 인용 → 종료 1
  rm "$tmp/repo/internal/a.go"
  printf '// 근거: .superpowers/sdd/x/task-1-brief.md 참고\n' > "$tmp/repo/internal/b.go"  # workspace-cite-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'추적 파일이 .superpowers/·.omc/ 작업공간이나 task-N 보고서 경로를 인용한다.\n.gitignore 로 막힌 경로라 받는 사람은 열 수 없는 경로를 안내받는다.\n\ninternal/b.go:1:// 근거: .superpowers/sdd/x/task-1-brief.md 참고'  # workspace-cite-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 2: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 3) 디렉터리 접두가 붙은 작업공간 경로 인용 → 종료 1(선두 문자 클래스가 /
  #    를 거부하면 이 모양을 놓친다)
  rm "$tmp/repo/internal/b.go"
  printf '// 참고: docs/.superpowers/worklog/x.md 확인\n' > "$tmp/repo/internal/c.go"  # doc-cite-check: ignore  # workspace-cite-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'추적 파일이 .superpowers/·.omc/ 작업공간이나 task-N 보고서 경로를 인용한다.\n.gitignore 로 막힌 경로라 받는 사람은 열 수 없는 경로를 안내받는다.\n\ninternal/c.go:1:// 참고: docs/.superpowers/worklog/x.md 확인'  # doc-cite-check: ignore  # workspace-cite-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 3: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 4) report|review|brief|addendum 밖의 task-N 파일명 접미 인용 → 종료 1(접미 확장 확인)
  rm "$tmp/repo/internal/c.go"
  printf '// 참고: task-3-fix-1.md 확인\n' > "$tmp/repo/internal/d.go"  # workspace-cite-check: ignore
  git -C "$tmp/repo" add -A
  expected=$'추적 파일이 .superpowers/·.omc/ 작업공간이나 task-N 보고서 경로를 인용한다.\n.gitignore 로 막힌 경로라 받는 사람은 열 수 없는 경로를 안내받는다.\n\ninternal/d.go:1:// 참고: task-3-fix-1.md 확인'  # workspace-cite-check: ignore
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [ "$out" = "$expected" ] || { echo "self-test 4: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 4b) OMC 상태 디렉터리 경로 인용 → 종료 1(공개 클론에 없다)
  printf '// (.omc/plans/x.md §6 수용 기준)\n' > "$tmp/repo/internal/e.go"  # workspace-cite-check: ignore
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"internal/e.go:1:"* ]] || { echo "self-test 4b: 종료 $rc 출력 '$out'" >&2; return 1; }
  rm "$tmp/repo/internal/e.go"
  # 5) 위반 0건이면 통과
  rm "$tmp/repo/internal/d.go"; git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "작업공간 인용 검사 통과" ] || { echo "self-test 5: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 6) git 저장소가 아니면 종료 2 — 위반 0건 통과로 둔갑하지 않는다
  rc=0; out="$(scan "$tmp/notgit" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 6: git 저장소 아닌 곳에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  # 7) 절대 경로 인용도 잡는다 → 종료 1
  printf '// 근거: /home/u/x/.superpowers/sdd/y/task-1-report.md\n' > "$tmp/repo/internal/f.go"  # workspace-cite-check: ignore
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"internal/f.go:1:"* ]] || { echo "self-test 7: 종료 $rc 출력 '$out'" >&2; return 1; }
  rm "$tmp/repo/internal/f.go"; git -C "$tmp/repo" add -A

  # 8) git grep 오류(잘못된 정규식)는 통과가 아니라 종료 2
  rc=0; out="$(PATTERN='(' scan "$tmp/repo" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 8: 정규식 오류에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) scan "${1:-$(dirname "$0")/..}" ;;
esac
