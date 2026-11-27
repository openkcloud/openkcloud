#!/usr/bin/env bash
# Entry for a Kubernetes RNGD LLM Job: furiosa-llm serve + fixed request list + stop.
set -euo pipefail
OUT=/out
mkdir -p "$OUT"
echo "pod_start_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee "$OUT/pod_lifecycle.txt"
export HF_HOME="${HF_HOME:-/hf}"
MODEL="${JOB_LLM_MODEL:-furiosa-ai/Llama-3.1-8B-Instruct}"
REQ="${JOB_REQUESTS_FILE:-/requests/requests_smoke_n4_in149_out128.jsonl}"
C="${JOB_CONCURRENCY:-4}"

# Prove device visibility before serve
furiosa-smi info 2>&1 | tee "$OUT/furiosa_smi_info.txt" || true
ls -la /dev/rngd 2>&1 | tee "$OUT/dev_rngd_ls.txt" || true
echo "NVIDIA_VISIBLE_DEVICES=${NVIDIA_VISIBLE_DEVICES:-}" | tee -a "$OUT/pod_lifecycle.txt"
echo "CUDA_VISIBLE_DEVICES=${CUDA_VISIBLE_DEVICES:-}" | tee -a "$OUT/pod_lifecycle.txt"

furiosa-llm serve "$MODEL" --host 127.0.0.1 --port 8000 \
  >"$OUT/engine.log" 2>&1 &
EPID=$!
echo "engine_pid=$EPID model=$MODEL" | tee -a "$OUT/pod_lifecycle.txt"

READY=0
for i in $(seq 1 180); do
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
    tail -120 "$OUT/engine.log" || true
    exit 43
  fi
  sleep 5
done
if [ "$READY" != 1 ]; then
  echo "not_ready" | tee -a "$OUT/pod_lifecycle.txt"
  tail -120 "$OUT/engine.log" || true
  kill "$EPID" 2>/dev/null || true
  exit 43
fi

python3 /scripts/llm_fixed_request_list.py \
  --endpoint http://127.0.0.1:8000/v1/chat/completions \
  --requests-file "$REQ" \
  --concurrency "$C" --timeout-s 300 \
  --result-file "$OUT/result.json" | tee "$OUT/summary.json"

python3 /scripts/token_usage_check.py --result "$OUT/result.json" --out "$OUT/usage_check.json"

kill "$EPID" 2>/dev/null || true
wait "$EPID" 2>/dev/null || true
echo "pod_end_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$OUT/pod_lifecycle.txt"
