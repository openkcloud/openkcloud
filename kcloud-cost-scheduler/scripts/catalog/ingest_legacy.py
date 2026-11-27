#!/usr/bin/env python3
"""Legacy import of 9/1~9/3 performance.json trees as source=legacy_import."""

from __future__ import annotations

import argparse
import json
import sys
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from catalog.repository import open_catalog  # noqa: E402


def import_tree(root: Path, db_path: Path, experiment_id: str) -> int:
    root = Path(root)
    repo = open_catalog(db_path)
    n = 0
    for perf in sorted(root.glob("**/performance.json")):
        run_dir = perf.parent
        data = json.loads(perf.read_text(encoding="utf-8"))
        run_uuid = data.get("run_uuid") or str(uuid.uuid4())
        run_id = data.get("run_id") or f"{run_dir.parent.name}/{run_dir.name}"
        trace_id = data.get("trace_id") or run_uuid
        try:
            repo.insert_workload_run({
                "run_uuid": run_uuid,
                "experiment_id": experiment_id,
                "run_id": run_id,
                "trace_id": trace_id,
                "workload_id": data.get("workload_id") or run_id,
                "cohort": data.get("cohort"),
                "concurrency": data.get("concurrency"),
                "repeat_idx": data.get("repeat"),
                "allocation_mode": data.get("allocation_mode"),
                "runtime": data.get("runtime"),
                "model": data.get("model"),
                "source": "legacy_import",
                "quality_flag": "legacy_missing_fields",
                "decision_reason": "experimental_assignment",
                "routing_policy": data.get("allocation_mode") or "",
                "decision_branch": None,
                "npu_cap": data.get("npu_cap") or 0,
                "submitted_at": data.get("started_at"),
                "scheduled_at": None,
                "container_started_at": None,
                "endpoint_ready_at": None,
                "load_started_at": (data.get("lifecycle") or {}).get("load_start_utc"),
                "load_ended_at": (data.get("lifecycle") or {}).get("load_end_utc"),
                "duration_s": data.get("duration_s"),
                "latency_p99_ms": data.get("latency_p99_ms"),
                "net_energy_j": data.get("net_energy_j"),
                "energy_per_success_j": data.get("energy_per_success_j"),
                "sla_pass": 1 if data.get("sla_pass") else 0,
                "requests_total": data.get("requests_total"),
                "requests_success": data.get("requests_success"),
                "error_rate": data.get("error_rate"),
                "git_sha": data.get("git_sha"),
                "result_path": str(run_dir),
            })
            n += 1
        except RuntimeError as e:
            print(f"skip conflict {run_dir}: {e}")
    print(f"legacy imported {n} runs from {root} -> {db_path}")
    return n


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--root", required=True, help="results tree with performance.json")
    p.add_argument("--db", default="/var/lib/kcloud/catalog/kcloud.db")
    p.add_argument("--experiment-id", required=True)
    args = p.parse_args()
    import_tree(Path(args.root), Path(args.db), args.experiment_id)


if __name__ == "__main__":
    main()
