#!/usr/bin/env bash
# Entry for a Kubernetes GPU LLM Job: start vLLM, run fixed list, stop engine, exit.
set -euo pipefail
OUT=/out
mkdir -p "$OUT"
echo "pod_start_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee "$OUT/pod_lifecycle.txt"
export HF_HOME="${HF_HOME:-/hf}"

vllm serve meta-llama/Llama-3.1-8B-Instruct \
  --max-model-len 4096 --dtype bfloat16 \
  --gpu-memory-utilization 0.9 \
  --host 127.0.0.1 --port 8000 \
  >"$OUT/engine.log" 2>&1 &
EPID=$!
echo "engine_pid=$EPID" | tee -a "$OUT/pod_lifecycle.txt"

READY=0
for i in $(seq 1 90); do
  if python3 - <<'PY'
import urllib.request
urllib.request.urlopen("http://127.0.0.1:8000/v1/models", timeout=2).read()
print("ok")
PY
  then
    echo "ready_$i utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$OUT/pod_lifecycle.txt"
    READY=1
    break
  fi
  if ! kill -0 "$EPID" 2>/dev/null; then
    echo "engine_died" | tee -a "$OUT/pod_lifecycle.txt"
    tail -100 "$OUT/engine.log" || true
    exit 43
  fi
  sleep 5
done
if [ "$READY" != 1 ]; then
  echo "not_ready" | tee -a "$OUT/pod_lifecycle.txt"
  tail -100 "$OUT/engine.log" || true
  kill "$EPID" 2>/dev/null || true
  exit 43
fi
grep -iE 'dtype|bfloat|torch.bfloat' "$OUT/engine.log" | head -20 | tee "$OUT/dtype_evidence.txt" || true

python3 /scripts/llm_fixed_request_list.py \
  --endpoint http://127.0.0.1:8000/v1/chat/completions \
  --requests-file /requests/requests_n600_in149_out128.jsonl \
  --concurrency 4 --timeout-s 300 \
  --result-file "$OUT/result.json" | tee "$OUT/summary.json"

python3 /scripts/token_usage_check.py --result "$OUT/result.json" --out "$OUT/usage_check.json"

kill "$EPID" 2>/dev/null || true
wait "$EPID" 2>/dev/null || true
echo "pod_end_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$OUT/pod_lifecycle.txt"
