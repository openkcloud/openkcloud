#!/usr/bin/env python3
"""Ingest a single experiment run directory into Catalog (measured strict)."""

from __future__ import annotations

import argparse
import csv
import json
import sys
import uuid
from pathlib import Path

# allow `python3 scripts/catalog/ingest_run.py`
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from catalog.repository import open_catalog  # noqa: E402


def _classify_endpoint(endpoint: str) -> str:
    ep = (endpoint or "").rstrip("/")
    if "8001" in ep:
        return "npu"
    if "8000" in ep:
        return "gpu"
    return "unknown"


def ingest_run_dir(run_dir: Path, db_path: Path, *, strict: bool = True) -> str:
    run_dir = Path(run_dir)
    perf_path = run_dir / "performance.json"
    if not perf_path.exists():
        raise SystemExit(f"missing {perf_path}")

    perf = json.loads(perf_path.read_text(encoding="utf-8"))
    ctx_path = run_dir / "workload_context.json"
    ctx = json.loads(ctx_path.read_text(encoding="utf-8")) if ctx_path.exists() else {}

    run_uuid = perf.get("run_uuid") or str(uuid.uuid4())
    experiment_id = perf.get("experiment_id") or ctx.get("experiment_id") or "unknown"
    run_id = perf.get("run_id") or run_dir.name
    trace_id = perf.get("trace_id") or ctx.get("trace_id") or run_uuid
    workload_id = perf.get("workload_id") or ctx.get("workload_id") or run_id
    source = perf.get("source") or "measured"

    route = perf.get("route_summary") or {}
    total = int(route.get("total") or perf.get("requests_total") or 0)
    gpu_c = int(route.get("gpu_count") or 0)
    npu_c = int(route.get("npu_count") or 0)
    err_c = int(route.get("error_count") or 0)
    if strict and total and gpu_c + npu_c + err_c != total:
        raise SystemExit(
            f"route conservation failed: gpu({gpu_c})+npu({npu_c})+err({err_c}) != total({total})"
        )

    cnn = perf.get("cnn_telemetry") or {}
    notes = perf.get("notes") or []
    cnn_noted = any("cnn" in str(n).lower() for n in notes)
    if strict and cnn_noted:
        # Defense in depth: E0 also runs validate_run.py under --strict, but a
        # lone ingest_run.py call must not accept a CNN-noted run with zero delta.
        if not cnn:
            raise SystemExit("cnn_telemetry missing while notes claim CNN background")
        if cnn.get("images_delta", 0) <= 0 and cnn.get("forwards_delta", 0) <= 0:
            raise SystemExit(
                f"cnn_telemetry has zero images/forwards delta: {cnn}"
            )

    lifecycle = perf.get("lifecycle") or {}
    repo = open_catalog(db_path)

    # reference profile from measured run (exploratory)
    profile_key = ctx.get("profile_key") or f"measured:{run_id}"
    repo.upsert_reference_profile({
        "profile_key": profile_key,
        "model": perf.get("model") or ctx.get("model"),
        "runtime": perf.get("runtime") or ctx.get("runtime"),
        "accelerator": ctx.get("accelerator") or "gpu",
        "precision": ctx.get("precision") or "fp16",
        "input_tokens": ctx.get("input_tokens"),
        "output_tokens": ctx.get("output_tokens"),
        "context_length": ctx.get("context_length"),
        "batch": ctx.get("batch") or 1,
        "concurrency": perf.get("concurrency"),
        "predicted_p99_ms": perf.get("latency_p99_ms"),
        "predicted_duration_s": perf.get("duration_s"),
        "predicted_net_energy_j": perf.get("net_energy_j"),
        "predicted_server_energy_j": perf.get("server_energy_j"),
        "source": "MEASURED_EXPLORATORY" if source == "measured" else source,
        # The context's own confidence describes the request spec; a profile
        # derived from a completed run is evidence, so it is graded by source.
        "confidence": "MEASURED" if source == "measured" else "REFERENCE",
    })

    repo.insert_workload_run({
        "run_uuid": run_uuid,
        "experiment_id": experiment_id,
        "run_id": run_id,
        "trace_id": trace_id,
        "workload_id": workload_id,
        "cohort": perf.get("cohort"),
        "concurrency": perf.get("concurrency"),
        "repeat_idx": perf.get("repeat"),
        "allocation_mode": perf.get("allocation_mode"),
        "runtime": perf.get("runtime"),
        "model": perf.get("model"),
        "source": source,
        "quality_flag": "ok" if source == "measured" else "legacy",
        "decision_reason": ctx.get("decision_reason") or "experimental_assignment",
        "routing_policy": ctx.get("routing_policy") or "",
        "decision_branch": None,  # never auto-label B3 from cohort
        "npu_cap": perf.get("npu_cap") or 0,
        "submitted_at": lifecycle.get("submitted_at") or perf.get("started_at"),
        "scheduled_at": lifecycle.get("scheduled_at"),  # Docker = NULL
        "container_started_at": lifecycle.get("container_started_at"),
        "endpoint_ready_at": lifecycle.get("endpoint_ready_at"),
        "load_started_at": lifecycle.get("load_start_utc") or lifecycle.get("load_started_at"),
        "load_ended_at": lifecycle.get("load_end_utc") or lifecycle.get("load_ended_at"),
        "duration_s": perf.get("duration_s"),
        "latency_p99_ms": perf.get("latency_p99_ms"),
        # Device net (historical). Server total is separate — never equate the two.
        "net_energy_j": perf.get("net_energy_j"),
        "server_energy_j": perf.get("server_energy_j"),
        "energy_per_success_j": perf.get("energy_per_success_j"),
        "sla_pass": 1 if perf.get("sla_pass") else 0,
        "requests_total": perf.get("requests_total"),
        "requests_success": perf.get("requests_success"),
        "error_rate": perf.get("error_rate"),
        "git_sha": perf.get("git_sha"),
        "result_path": str(run_dir),
        "environment_key": ctx.get("environment_key") or None,
    })

    req_path = run_dir / "requests.jsonl"
    req_rows = []
    if req_path.exists():
        for line in req_path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            r = json.loads(line)
            idx = r.get("request_index", len(req_rows))
            req_rows.append({
                "request_id": r.get("request_id") or f"{run_uuid}:{idx}",
                "run_uuid": run_uuid,
                "trace_id": trace_id,
                "request_index": idx,
                "endpoint": r.get("endpoint"),
                "route_device": _classify_endpoint(r.get("endpoint", "")),
                "ttft_ms": r.get("ttft_ms"),
                "tpot_ms": r.get("tpot_ms"),
                "total_ms": r.get("total_ms"),
                "prompt_tokens": r.get("prompt_tokens"),
                "completion_tokens": r.get("completion_tokens"),
                "error": r.get("error"),
            })
    if strict and req_rows and total and len(req_rows) != total:
        raise SystemExit(f"expected {total} request samples, got {len(req_rows)}")
    repo.insert_request_samples(req_rows)

    accel_rows = []
    power_path = run_dir / "power.csv"
    if power_path.exists():
        with power_path.open(encoding="utf-8", newline="") as f:
            for row in csv.DictReader(f):
                accel_rows.append({
                    "run_uuid": run_uuid,
                    "sample_timestamp": float(row["timestamp_unix"]),
                    "accelerator_uuid": None,
                    "source": row["source"],
                    "metric": "power_w",
                    "value": float(row["watts"]),
                })
    util_path = run_dir / "util.csv"
    if util_path.exists():
        with util_path.open(encoding="utf-8", newline="") as f:
            for row in csv.DictReader(f):
                accel_rows.append({
                    "run_uuid": run_uuid,
                    "sample_timestamp": float(row["timestamp_unix"]),
                    "accelerator_uuid": None,
                    "source": row["source"],
                    "metric": "util_pct",
                    "value": float(row["pct"]),
                })
    if strict:
        sources = {r["source"] for r in accel_rows if r["metric"] == "power_w"}
        if "gpu" not in sources:
            raise SystemExit("power samples missing gpu")
    repo.insert_accelerator_samples(accel_rows)

    repo.upsert_interference({
        "run_uuid": run_uuid,
        "colocated_json": {"cnn": bool(cnn), "notes": perf.get("notes")},
        "cnn_images_delta": cnn.get("images_delta"),
        "cnn_forwards_delta": cnn.get("forwards_delta"),
        "cnn_throughput_ips": cnn.get("throughput_ips"),
        "gpu_util_avg_pct": perf.get("gpu_util_avg_pct"),
        "npu_util_avg_pct": perf.get("npu_util_avg_pct"),
    })

    # experimental placement label — NOT decision_branch
    repo.insert_placement_decision({
        "decision_id": str(uuid.uuid4()),
        "trace_id": trace_id,
        "run_uuid": run_uuid,
        "mode": "experimental",
        "decision_reason": "experimental_assignment",
        "routing_policy": ctx.get("routing_policy") or perf.get("allocation_mode") or "",
        "decision_branch": None,
        "candidates_json": {"cohort": perf.get("cohort"), "npu_cap": perf.get("npu_cap")},
        "selected_shadow": None,
        "selected_enforced": None,
        "selected_actual": perf.get("allocation_mode"),
        "predicted_p99_ms": None,
        "predicted_duration_s": None,
        "predicted_net_energy_j": None,
        "confidence": "SPEC",
        "score_components_json": {},
        "regret_json": None,
        "model_version": "none",
    })

    print(f"ingested run_uuid={run_uuid} path={run_dir} requests={len(req_rows)} samples={len(accel_rows)}")
    return run_uuid


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("run_dir")
    p.add_argument("--db", default="/var/lib/kcloud/catalog/kcloud.db")
    p.add_argument("--strict", action="store_true", default=True)
    p.add_argument("--no-strict", action="store_true")
    args = p.parse_args()
    strict = not args.no_strict
    ingest_run_dir(Path(args.run_dir), Path(args.db), strict=strict)


if __name__ == "__main__":
    main()
