#!/usr/bin/env bash
# ============================================================
# trap-signal_test.sh: 라이브 하네스 run.sh 가 HUP/INT/TERM 을 종료 코드로 바꿔 EXIT 트랩을 돌리는지 확인
# 상세: (1) EXIT 트랩이 있는 run.sh 마다 세 trap 줄이 있는지 정적 확인
#       (2) 같은 관용구의 합성 스크립트에 프로세스 그룹 SIGTERM 을 보내 EXIT 트랩 실행과 종료 코드 143 을 확인
#       클러스터 미접속.
# 생성일: 2026-09-30
# ============================================================
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
while IFS= read -r f; do
  grep -qE "^[[:space:]]*trap .* EXIT" "$f" || continue   # EXIT 트랩이 없는 하네스는 대상 밖
  for sig in HUP INT TERM; do
    grep -qE "^[[:space:]]*trap 'exit 1[0-9]{2}' $sig\$" "$f" || { echo "FAIL $f: $sig trap 없음"; fail=1; }
  done
done < <(find "$HERE" -mindepth 2 -name run.sh)
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/s.sh" <<'EOS2'
#!/usr/bin/env bash
set -euo pipefail
cleanup() { echo "cleanup rc=$1" > "$OUTF"; }
trap 'cleanup $?' EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
sleep 30
EOS2
OUTF="$tmp/out" setsid bash "$tmp/s.sh" & pid=$!
sleep 1; kill -TERM -- "-$pid"; wait "$pid"; rc=$?
[ "$rc" = 143 ] || { echo "FAIL: 종료 코드 $rc (want 143)"; fail=1; }
grep -q "cleanup rc=143" "$tmp/out" 2>/dev/null || { echo "FAIL: EXIT 트랩 미실행"; fail=1; }
[ "$fail" = 0 ] && echo "trap-signal_test: PASS"
exit "$fail"
