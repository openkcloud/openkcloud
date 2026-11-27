#!/usr/bin/env bash
# ============================================================
# noop-sample.sh: AcceleratorEvidence no-op 쓰기 게이트 라이브 표본 스크립트
# 상세: aev <tenstorrent-node> 의 resourceVersion·observedAt 을 15초 간격 6회 표본 떠
#       재조정에도 값이 그대로인지 본다(3번째 표본 뒤 descriptor annotate 로 재조정 1회 강제).
# 생성일: 2026-09-21 | 수정일: 2026-10-11
# ============================================================
set -uo pipefail

for i in 1 2 3 4 5 6; do
  rv=$(kubectl get aev k8s-worker3 -o jsonpath='{.metadata.resourceVersion}' 2>/dev/null)
  ob=$(kubectl get aev k8s-worker3 -o jsonpath='{.status.observedAt}' 2>/dev/null)
  printf 'sample %d rv=%s observedAt=%s\n' "$i" "$rv" "$ob"
  if [ "$i" = "3" ]; then
    kubectl annotate adesc new-vendor-tt-blackhole "npu.ai/probe-trigger=$(date -u +%FT%TZ)" --overwrite >/dev/null 2>&1 \
      && echo ">> forced reconcile via descriptor annotate after sample 3"
  fi
  if [ "$i" != "6" ]; then sleep 15; fi
done
