#!/usr/bin/env bash
# ============================================================
# read-commit-test.sh: evidence.sh read_build_commit 의 폴백 순서 자체 점검(클러스터 불필요)
# 상세: PATH 앞에 가짜 kubectl 을 두고 (1) 기동 로그 (2) /manager --version (3) 체크아웃 폴백
#       순서를 확인한다. 용법: bash test/collect/read-commit-test.sh
# 생성일: 2026-09-21 | 수정일: 2026-09-21
# ============================================================
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/kubectl" <<'EOF2'
#!/usr/bin/env bash
case "$*" in
  *" logs "*) printf '%s' "${FAKE_LOG:-}" ;;
  *" exec "*) [ -n "${FAKE_VERSION:-}" ] || { echo "flag provided but not defined: -version" >&2; exit 2; }
              printf '%s\n' "$FAKE_VERSION" ;;
esac
EOF2
chmod +x "$tmp/kubectl"
export PATH="$tmp:$PATH"
# evidence.sh 에서 함수 본문만 읽는다(인자 파싱·수집은 실행하지 않는다).
# shellcheck disable=SC1090
source <(sed -n '/^read_build_commit() {/,/^}/p' "$HERE/evidence.sh")
OPERATOR_REPO="$(cd "$HERE/../.." && pwd)"
UNCOLLECTED="uncollected"
fail=0
t() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got=[$2] want=[$3]"; fail=1; fi; }
log_line=$'2026-09-21T00:00:00Z\tINFO\tsetup\tstarting manager\t{"commit": "abc1234"}'
t "기동 로그가 우선" "$(FAKE_LOG="$log_line" FAKE_VERSION=def5678 read_build_commit)" "abc1234"
t "로그가 없으면 --version" "$(FAKE_LOG= FAKE_VERSION=def5678 read_build_commit)" "def5678"
t "둘 다 없으면 체크아웃 표시" "$(FAKE_LOG= FAKE_VERSION= read_build_commit)" "$(git -C "$OPERATOR_REPO" rev-parse --short HEAD)-checkout"
exit "$fail"
