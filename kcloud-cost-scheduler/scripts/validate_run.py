#!/usr/bin/env python3
"""R1 gate: validate run artifacts under a performance.json directory."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path
from typing import Optional


def _count_jsonl(path: Path) -> int:
    if not path.exists():
        return 0
    return sum(1 for line in path.read_text(encoding="utf-8").splitlines() if line.strip())


def _power_sources(path: Path) -> set[str]:
    if not path.exists():
        return set()
    with path.open(encoding="utf-8", newline="") as f:
        return {row["source"] for row in csv.DictReader(f) if row.get("source")}


def validate_run_dir(
    run_dir: Path,
    expected_requests: Optional[int] = None,
    *,
    require_cnn: Optional[bool] = None,
) -> list[str]:
    errors: list[str] = []
    perf_path = run_dir / "performance.json"
    if not perf_path.exists():
        return [f"missing performance.json in {run_dir}"]

    perf = json.loads(perf_path.read_text(encoding="utf-8"))
    power_path = run_dir / "power.csv"
    requests_path = run_dir / "requests.jsonl"
    util_path = run_dir / "util.csv"

    for key in ("run_id", "cohort", "concurrency", "trace_id", "lifecycle"):
        if key not in perf or not perf[key]:
            errors.append(f"performance.json missing {key}")

    lifecycle = perf.get("lifecycle") or {}
    for key in ("load_start_utc", "load_end_utc", "load_start_mono", "load_end_mono"):
        if key not in lifecycle:
            errors.append(f"lifecycle missing {key}")

    notes = [str(n).lower() for n in (perf.get("notes") or [])]
    cnn_noted = any("cnn" in n for n in notes)
    if require_cnn is None:
        require_cnn = cnn_noted
    cnn = perf.get("cnn_telemetry") or {}
    if require_cnn:
        if not cnn:
            errors.append("cnn_telemetry missing (CNN background expected)")
        elif cnn.get("images_delta", 0) <= 0 and cnn.get("forwards_delta", 0) <= 0:
            errors.append("cnn_telemetry has zero images/forwards delta")

    if expected_requests is None:
        expected_requests = int(perf.get("requests_total") or 100)

    route = perf.get("route_summary") or {}
    req_total = int(route.get("total") or perf.get("requests_total") or 0)
    gpu_c = int(route.get("gpu_count") or 0)
    npu_c = int(route.get("npu_count") or 0)
    err_c = int(route.get("error_count") or 0)
    if req_total and gpu_c + npu_c + err_c != req_total:
        errors.append(
            f"route_summary mismatch: gpu({gpu_c})+npu({npu_c})+err({err_c}) != total({req_total})"
        )

    n_req = _count_jsonl(requests_path)
    if n_req != expected_requests:
        errors.append(f"requests.jsonl has {n_req} lines, expected {expected_requests}")

    cohort = perf.get("cohort", "")
    gpu_only = cohort in ("g1-gpu-baseline", "b0-gpu")
    mixed = "g3" in cohort or cohort in ("g2-fixed-split", "b1-rngd", "b2-static-overflow", "p1-prewarm-overflow")

    sources = _power_sources(power_path)
    if not sources:
        errors.append("power.csv missing or empty")
    if "gpu" not in sources:
        errors.append("power.csv missing gpu source")
    if mixed and "npu" not in sources:
        errors.append("power.csv missing npu source (mixed cohort)")

    if gpu_only and not util_path.exists():
        errors.append("util.csv missing for GPU cohort (gpu util required)")

    if perf.get("idle_power_w", 0) <= 0:
        errors.append("idle_power_w is zero — idle-power.csv may be missing upstream")

    return errors


def validate_tree(root: Path, expected_requests: Optional[int] = None) -> dict[str, list[str]]:
    results: dict[str, list[str]] = {}
    for perf in sorted(root.glob("**/performance.json")):
        run_dir = perf.parent
        errs = validate_run_dir(run_dir, expected_requests)
        if errs:
            results[str(run_dir.relative_to(root))] = errs
    return results


def main() -> None:
    p = argparse.ArgumentParser(description="R1 validation gate for experiment runs")
    p.add_argument("path", help="run directory or results root")
    p.add_argument("--expected-requests", type=int, default=None,
                   help="override; default uses performance.json requests_total")
    p.add_argument("--require-cnn", action="store_true", help="force CNN telemetry check")
    p.add_argument("--no-require-cnn", action="store_true", help="skip CNN telemetry check")
    p.add_argument("--strict", action="store_true", help="exit 1 on any error")
    args = p.parse_args()
    path = Path(args.path)
    require_cnn: Optional[bool] = None
    if args.require_cnn:
        require_cnn = True
    elif args.no_require_cnn:
        require_cnn = False

    if (path / "performance.json").exists():
        errors = validate_run_dir(path, args.expected_requests, require_cnn=require_cnn)
        if errors:
            print(f"FAIL {path}:")
            for e in errors:
                print(f"  - {e}")
            raise SystemExit(1)
        print(f"PASS {path}")
        return

    all_errors = validate_tree(path, args.expected_requests)
    if not all_errors:
        print(f"PASS all runs under {path}")
        return

    print(f"FAIL {len(all_errors)} run(s) under {path}:")
    for rel, errs in all_errors.items():
        print(f"  {rel}:")
        for e in errs:
            print(f"    - {e}")
    if args.strict:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
