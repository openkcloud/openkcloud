#!/usr/bin/env python3
"""Join shadow decisions to actual run results and record prediction error.

A shadow recommendation only becomes real "regret" when the alternative was
also measured. Until a paired run exists this script records the prediction
error and marks the counterfactual as unmeasured rather than inventing a number.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Optional

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from catalog.repository import open_catalog  # noqa: E402


STATUS_MATCHED = "shadow_matched_actual"
STATUS_COUNTERFACTUAL = "counterfactual_unmeasured"
STATUS_SINGLE_CANDIDATE = "single_candidate"
STATUS_NO_PREDICTION = "no_prediction"


def _error(predicted: Optional[float], actual: Optional[float]) -> Optional[dict[str, Any]]:
    if predicted is None or actual is None:
        return None
    signed = float(predicted) - float(actual)
    out: dict[str, Any] = {"predicted": float(predicted), "actual": float(actual), "signed": round(signed, 4)}
    if actual:
        out["abs_pct"] = round(abs(signed) / abs(float(actual)) * 100.0, 2)
    return out


def _placement_status(decision: dict[str, Any], actual_target: Optional[str]) -> str:
    if decision.get("predicted_p99_ms") is None and decision.get("predicted_net_energy_j") is None:
        return STATUS_NO_PREDICTION
    try:
        candidates = json.loads(decision.get("candidates_json") or "[]")
    except json.JSONDecodeError:
        candidates = []
    if len(candidates) < 2:
        return STATUS_SINGLE_CANDIDATE
    shadow = decision.get("selected_shadow")
    if shadow and actual_target and shadow == actual_target:
        return STATUS_MATCHED
    return STATUS_COUNTERFACTUAL


def build_regret(decision: dict[str, Any], run: dict[str, Any]) -> dict[str, Any]:
    # Docker runs have no node placement, so the actual "target" is the
    # allocation mode that was executed.
    actual_target = run.get("allocation_mode")

    status = _placement_status(decision, actual_target)
    regret: dict[str, Any] = {
        "placement_status": status,
        # Only a measured counterfactual can produce a regret value.
        "placement_regret_j": 0.0 if status == STATUS_MATCHED else None,
        "selected_shadow": decision.get("selected_shadow"),
        "actual_target": actual_target,
        "run_source": run.get("source"),
        "quality_flag": run.get("quality_flag"),
        "sla_pass": bool(run.get("sla_pass")),
    }

    errors = {
        "p99_ms": _error(decision.get("predicted_p99_ms"), run.get("latency_p99_ms")),
        "duration_s": _error(decision.get("predicted_duration_s"), run.get("duration_s")),
        "net_energy_j": _error(decision.get("predicted_net_energy_j"), run.get("net_energy_j")),
    }
    regret["prediction_error"] = {k: v for k, v in errors.items() if v is not None}
    return regret


def record(db_path: Path, *, verbose: bool = True) -> int:
    repo = open_catalog(db_path)
    joined = 0
    for decision in repo.list_unjoined_decisions():
        trace_id = decision.get("trace_id")
        if not trace_id:
            continue
        run = repo.get_run_by_trace(trace_id)
        if not run:
            continue
        regret = build_regret(decision, run)
        repo.record_decision_outcome(
            decision["decision_id"],
            run_uuid=run["run_uuid"],
            selected_actual=run.get("allocation_mode"),
            regret_json=regret,
        )
        joined += 1
        if verbose:
            err = regret.get("prediction_error", {})
            p99 = err.get("p99_ms", {}).get("abs_pct")
            energy = err.get("net_energy_j", {}).get("abs_pct")
            print(
                f"joined decision={decision['decision_id'][:8]} trace={trace_id} "
                f"status={regret['placement_status']} p99_err={p99}% energy_err={energy}%"
            )
    if verbose:
        print(f"regret recorded for {joined} decision(s)")
    return joined


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--db", default="/var/lib/kcloud/catalog/kcloud.db")
    p.add_argument("--quiet", action="store_true")
    args = p.parse_args()
    record(Path(args.db), verbose=not args.quiet)


if __name__ == "__main__":
    main()
