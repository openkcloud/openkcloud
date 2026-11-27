#!/usr/bin/env python3
"""Join Shadow placement_decision with a new MEASURED run → prediction error.

Writes a single linked record. Does NOT upsert into the prediction-source
reference rows used for the prior Catalog lookup (separate out_dir / experiment).
"""
from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--catalog-db", required=True)
    p.add_argument("--trace-id", required=True)
    p.add_argument("--run-dir", required=True, help="new measured run directory")
    p.add_argument("--out", required=True)
    p.add_argument("--predicted-from", default="catalog_placement", help="label only")
    args = p.parse_args()

    run_dir = Path(args.run_dir)
    perf = load_json(run_dir / "performance.json")
    ctx_path = run_dir / "workload_context.json"
    ctx = load_json(ctx_path) if ctx_path.exists() else {}

    con = sqlite3.connect(args.catalog_db)
    con.row_factory = sqlite3.Row
    row = con.execute(
        "SELECT * FROM placement_decision WHERE trace_id=? ORDER BY evaluated_at DESC LIMIT 1",
        (args.trace_id,),
    ).fetchone()
    if not row:
        print(f"FAIL: no placement_decision for trace_id={args.trace_id}", file=sys.stderr)
        return 2
    decision = dict(row)
    for k in ("candidates_json", "score_components_json", "regret_json"):
        if isinstance(decision.get(k), str) and decision[k]:
            try:
                decision[k] = json.loads(decision[k])
            except json.JSONDecodeError:
                pass

    cands = decision.get("candidates_json") or []
    if isinstance(cands, str):
        cands = json.loads(cands)
    primary = cands[0] if cands else {}
    extras = primary.get("extras") or {}
    predicted_p99 = primary.get("predicted_p99_ms")
    if predicted_p99 is None:
        predicted_p99 = decision.get("predicted_p99_ms")
    predicted_energy = primary.get("predicted_net_energy_j")
    if predicted_energy is None:
        predicted_energy = decision.get("predicted_net_energy_j")

    actual_p99 = perf.get("latency_p99_ms")
    actual_energy = perf.get("net_energy_j")
    sla_pass = perf.get("sla_pass")

    err_p99 = None
    if predicted_p99 is not None and actual_p99 is not None:
        err_p99 = float(actual_p99) - float(predicted_p99)
    err_energy = None
    if predicted_energy is not None and actual_energy is not None:
        err_energy = float(actual_energy) - float(predicted_energy)

    linked = {
        "created_at": _now(),
        "experiment_id": perf.get("experiment_id") or ctx.get("experiment_id"),
        "trace_id": args.trace_id,
        "note": (
            "New MEASURED run linked to prior Catalog prediction. "
            "Prediction source rows are NOT overwritten."
        ),
        "placement": {
            "decision_id": decision.get("decision_id"),
            "mode": decision.get("mode"),
            "selected_shadow": decision.get("selected_shadow"),
            "selected_enforced": decision.get("selected_enforced"),
            "match_type": extras.get("match_type"),
            "environment_key": extras.get("environment_key"),
            "catalog_lookup_key": extras.get("catalog_lookup_key") or extras.get("profile_key"),
            "predicted_p99_ms": predicted_p99,
            "predicted_net_energy_j": predicted_energy,
            "predicted_sla_pass": extras.get("sla_pass"),
            "returned_score": primary.get("returned_score"),
            "confidence": primary.get("confidence") or decision.get("confidence"),
        },
        "measured": {
            "run_dir": str(run_dir),
            "run_id": perf.get("run_id"),
            "run_uuid": perf.get("run_uuid"),
            "concurrency": perf.get("concurrency"),
            "latency_p99_ms": actual_p99,
            "net_energy_j": actual_energy,
            "sla_pass": sla_pass,
            "duration_s": perf.get("duration_s"),
            "requests_total": perf.get("requests_total"),
            "bind_node": perf.get("node") or ctx.get("node"),
        },
        "prediction_error": {
            "p99_ms_actual_minus_predicted": err_p99,
            "net_energy_j_actual_minus_predicted": err_energy,
            "predicted_from": args.predicted_from,
        },
    }

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(linked, indent=2), encoding="utf-8")
    print(json.dumps(linked["prediction_error"] | {"out": str(out), "actual_p99": actual_p99, "predicted_p99": predicted_p99}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
