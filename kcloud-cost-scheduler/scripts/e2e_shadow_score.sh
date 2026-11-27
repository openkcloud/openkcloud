#!/usr/bin/env bash
# Catalog API (Python) → Go Shadow Score end-to-end check.
#
# Seeds a throwaway catalog with a GPU and an NPU reference profile, serves it
# over HTTP, drives the Go CatalogAwareScore plugin against it, then asserts a
# single placement_decision row came back. Proves the Python/Go profile_key
# contract and that shadow mode records without touching Bind.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCHED_DIR="$(cd "${SCRIPT_DIR}/../cost-based-scheduler" && pwd)"
PORT="${E2E_CATALOG_PORT:-18199}"
WORKDIR="$(mktemp -d)"
DB="${WORKDIR}/e2e.db"
API_PID=""

cleanup() {
  [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true
  wait "$API_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "== seeding catalog: $DB"
python3 - "$DB" "$SCRIPT_DIR" <<'PY'
import sys
from pathlib import Path

sys.path.insert(0, sys.argv[2])
from catalog.repository import open_catalog
from catalog.workload_context import build_profile_key

repo = open_catalog(Path(sys.argv[1]))
# Must mirror newCatalogTestPod() in the Go tests.
variants = [
    ("meta-llama/Llama-3.1-8B-Instruct", "vllm", "gpu", 900.0),
    ("furiosa-ai/Llama-3.1-8B-Instruct", "furiosa-llm", "npu", 300.0),
]
for model, runtime, accel, energy in variants:
    key = build_profile_key(model, runtime, accel, "fp16", 256, 128, 4096, 5)
    repo.upsert_reference_profile({
        "profile_key": key,
        "model": model,
        "runtime": runtime,
        "accelerator": accel,
        "precision": "fp16",
        "input_tokens": 256,
        "output_tokens": 128,
        "context_length": 4096,
        "batch": 1,
        "concurrency": 5,
        "predicted_p99_ms": 1500.0,
        "predicted_duration_s": 20.0,
        "predicted_net_energy_j": energy,
        "source": "MEASURED_EXPLORATORY",
        "confidence": "MEASURED",
    })
    print(f"  seeded {accel}: {key}")
PY

echo "== starting catalog API on :$PORT"
python3 "${SCRIPT_DIR}/catalog/catalog_api.py" --db "$DB" --port "$PORT" \
  >"${WORKDIR}/api.log" 2>&1 &
API_PID=$!

for _ in $(seq 1 40); do
  if curl -sf "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -sf "http://127.0.0.1:${PORT}/health" || { echo "catalog API failed to start"; cat "${WORKDIR}/api.log"; exit 1; }
echo

echo "== driving Go shadow score"
cd "$SCHED_DIR"
SCHED_CATALOG_URL="http://127.0.0.1:${PORT}" \
SCHED_CATALOG_MODE=shadow \
SCHED_SHADOW_SCORE_WEIGHT=0 \
SCHED_CATALOG_TIMEOUT_MS=2000 \
SCHED_PREDICTION_MIN_CONFIDENCE=MEASURED \
  go test -tags e2e -count=1 -run TestShadowScoreE2E ./internal/framework/plugin/

echo "== verifying stored decision"
python3 - "$DB" <<'PY'
import json
import sqlite3
import sys

conn = sqlite3.connect(sys.argv[1])
conn.row_factory = sqlite3.Row
rows = conn.execute("SELECT * FROM placement_decision").fetchall()
assert len(rows) == 1, f"placement_decision rows = {len(rows)}, want exactly 1 per cycle"
d = dict(rows[0])

cands = json.loads(d["candidates_json"])
assert len(cands) == 2, f"candidates = {len(cands)}, want 2"
assert all(c["found"] for c in cands), f"catalog miss in candidates: {cands}"
assert d["mode"] == "shadow", d["mode"]
assert d["selected_shadow"] == "npu-node", d["selected_shadow"]
assert d["selected_enforced"] is None, d["selected_enforced"]
assert d["decision_branch"] is None, "decision_branch must not be inferred"
assert d["confidence"] == "MEASURED", d["confidence"]
assert d["trace_id"] == "e2e-pod-trace", d["trace_id"]

comps = json.loads(d["score_components_json"])
assert comps["candidate_count"] == 2, comps
assert comps["weight"] == 0, comps
assert comps["raw_shadow_score"] > 0, "raw shadow score must be computed at weight=0"

print("  decision_id     :", d["decision_id"])
print("  selected_shadow :", d["selected_shadow"])
print("  candidates      :", [(c["node"], c["predicted_net_energy_j"]) for c in cands])
print("  raw_shadow_score:", comps["raw_shadow_score"])
PY

echo
echo "E2E PASS — Catalog API → Go Shadow Score, 1 decision, Bind untouched"
