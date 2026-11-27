#!/usr/bin/env bash
# ============================================================
# snapshot_test.sh: snapshot.sh 가 아홉 파일을 채우고 같은 시점 두 번 실행해도 같은 값을
#   내는지 확인하는 시험
# 상세: <nvidia-node>/nvidia 읽기 전용 대상 — <tenstorrent-node> 전환과 무관해 언제 실행해도 된다.
#       node-allocatable.json 에는 매 실행마다 달라지는 시각 필드가 없어 diff 비교가 안정적이다.
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
cd "$(dirname "$0")"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
bash snapshot.sh "$out" k8s-worker1 nvidia
for f in node-allocatable.json node-labels.json resourceslices.json resourceclaims.json \
         deviceclasses.json daemonsets.json pods.json cdi-files.txt dev-nodes.txt; do
  [ -s "$out/$f" ] || { echo "FAIL: $f 가 비었다"; exit 1; }
done
echo "PASS: 아홉 파일 모두 채워졌다"
# 같은 시점에 두 번 떠서 같은지 — 떠는 것 자체가 상태를 바꾸면 비교가 무의미하다
out2="$(mktemp -d)"; trap 'rm -rf "$out" "$out2"' EXIT
bash snapshot.sh "$out2" k8s-worker1 nvidia
if diff -q "$out/node-allocatable.json" "$out2/node-allocatable.json" >/dev/null; then
  echo "PASS: 두 번 떠도 같다"
else
  echo "FAIL: 떠는 것이 상태를 바꾼다"; exit 1
fi
