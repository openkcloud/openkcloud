#!/usr/bin/env bash
# ============================================================
# private-ip-check.sh: 내부 주소·계정이 추적 파일에 들어오는 것을 막는다
# 상세: 내부 대역 둘·RFC1918 사설 대역·내부 계정·기본 인증을 추적 파일에서 찾는다.
#       hack/public-chain.sh 의 PRIV_RE 를 포함하되 그것이 놓치는 192.168 대역과 10.0.x
#       내부 Harbor 주소도 잡는다. 줄 끝 표식으로 줄 단위 예외를 둔다. 종료 2 는 저장소 아님 또는 git grep 오류다.
# 생성일: 2026-09-30 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail

# 내부 대역 둘, RFC1918 사설 대역 셋, 내부 계정, 기본 인증.
# 172.16/12 는 컨테이너 런타임이 쓰는 구간이라 뺀다 — 노출돼도 내부 정보가 아니다.
# hack/release-prepare.sh 가 이 줄을 홑따옴표 한 줄 그대로 읽으므로 형태를 바꾸지 않는다.
PRIV_RE='(^|[^0-9.])(10\.254\.|129\.254\.|192\.168\.|10\.0\.[0-9]+\.)[0-9]|[A-Za-z0-9._-]+@(10\.254\.|129\.254\.|192\.168\.|10\.0\.[0-9]+\.)[0-9]|admin:[A-Za-z0-9]'

# 줄 단위 예외. 주소를 데이터로 들고 있는 줄은 줄 끝에 "private-ip-check: ignore" 표식을 둔다.
# 이 스크립트의 자가시험은 주소를 실행 시점에 조립하므로 표식이 필요 없다 — 공개 트리 검사는
# 표식을 읽지 못한다.
IGNORE_MARK='private-ip-check: ignore'
# 표식을 달 수 없는 파일의 정확한 줄. 지금 없다.
ALLOW_LINES='^$'

# 환경변수로는 덮이지 않는다. 자가시험만 호출 지점 대입(PATTERN='(' scan …)으로 잘못된 정규식을 넣는다.
PATTERN="$PRIV_RE"

# scan <루트>: 위반 유무를 판정. 종료 0=위반 없음, 1=위반 있음, 2=루트가 git 저장소가 아니거나 git grep 오류.
# runs/ 아래도 검사한다 — 전사에 남은 내부 주소도 공개 저장소로 나가면 안 된다.
scan() (
  local root="$1" hits rc=0
  cd "$root" || { echo "루트 디렉터리 없음: $root" >&2; return 2; }
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "git 저장소가 아니다: $root" >&2; return 2; }

  # 추적 파일만, 한 프로세스로. 종료 0=적중, 1=무적중, 그 외=오류 — 오류를 위반 없음으로 처리하지 않는다.
  hits=$(git grep -nIE "$PATTERN" -- .) || rc=$?
  [ "$rc" -le 1 ] || { echo "git grep 실패(종료 $rc) — 위반 없음으로 처리하지 않는다" >&2; return 2; }
  hits=$(printf '%s\n' "$hits" | grep -vF "$IGNORE_MARK" | grep -vE "$ALLOW_LINES" || true)

  if [ -n "$hits" ]; then
    echo "내부 주소나 계정이 추적 파일에 있다. 공개 저장소로 나가면 안 된다."
    echo "주소는 환경변수로 받고 산문에는 자리표시자를 쓴다."
    echo
    echo "$hits"
    return 1
  fi
  echo "내부 주소 검사 통과"
)

# self_test: scan() 자체를 임시 저장소에서 검증한다. 주소 문자열은 printf 인자로 쪼개 넣어
# 이 파일에 PRIV_RE 에 걸리는 리터럴이 남지 않게 한다.
self_test() {
  local tmp out rc
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/repo/internal" "$tmp/notgit"
  git -C "$tmp/repo" init -q

  # 1) 사설 대역 주소 → 종료 1, 그 줄이 출력에 있다
  printf '// host %s.168.0.1\n' 192 > "$tmp/repo/internal/a.go"
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"internal/a.go:1:"* ]] || { echo "self-test 1: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 2) 같은 줄에 줄 끝 표식이 있으면 그 줄은 빠진다 → 종료 0
  printf '// host %s.168.0.1  // private-ip-check: ignore\n' 192 > "$tmp/repo/internal/a.go"
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "내부 주소 검사 통과" ] || { echo "self-test 2: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 3) 위반 0건이면 통과
  printf '// host <내부 주소>\n' > "$tmp/repo/internal/a.go"
  git -C "$tmp/repo" add -A
  rc=0; out="$(scan "$tmp/repo")" || rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "내부 주소 검사 통과" ] || { echo "self-test 3: 종료 $rc 출력 '$out'" >&2; return 1; }

  # 4) git 저장소가 아니면 종료 2 — 위반 0건 통과로 둔갑하지 않는다
  rc=0; out="$(scan "$tmp/notgit" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 4: git 저장소 아닌 곳에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  # 5) git grep 오류(잘못된 정규식)는 통과가 아니라 종료 2
  rc=0; out="$(PATTERN='(' scan "$tmp/repo" 2>&1)" || rc=$?
  [ "$rc" -eq 2 ] || { echo "self-test 5: 정규식 오류에서 종료 $rc (기대 2) '$out'" >&2; return 1; }

  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) scan "${1:-$(dirname "$0")/..}" ;;
esac
