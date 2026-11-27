#!/usr/bin/env python3
"""Recompute p99 / token sums / J/token from probe cell originals.

Usage:
  python3 verify_k2_jtoken_from_raw.py --cell-dir /path/to/in149_out128/run-1 --idle-w 35.6
"""
from __future__ import annotations

import argparse
import csv
import json
import statistics
from pathlib import Path


def percentile(vals: list[float], pct: float) -> float:
    if not vals:
        return float("nan")
    s = sorted(vals)
    idx = min(int(len(s) * pct), len(s) - 1)
    return s[idx]


def energy_from_power_csv(path: Path) -> float:
    rows = [r for r in csv.DictReader(path.open()) if r.get("source") == "gpu"]
    if len(rows) < 2:
        return 0.0
    total = 0.0
    for i in range(1, len(rows)):
        dt = float(rows[i]["timestamp_unix"]) - float(rows[i - 1]["timestamp_unix"])
        total += float(rows[i]["watts"]) * dt
    return total


def net_energy(path: Path, idle_w: float) -> float:
    rows = [r for r in csv.DictReader(path.open()) if r.get("source") == "gpu"]
    if len(rows) < 2:
        return 0.0
    total = 0.0
    for i in range(1, len(rows)):
        dt = float(rows[i]["timestamp_unix"]) - float(rows[i - 1]["timestamp_unix"])
        total += max(float(rows[i]["watts"]) - idle_w, 0.0) * dt
    return total


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--cell-dir", required=True)
    p.add_argument("--idle-w", type=float, required=True)
    args = p.parse_args()
    d = Path(args.cell_dir)
    reqs = []
    for line in (d / "requests.jsonl").read_text(encoding="utf-8").splitlines():
        if line.strip():
            reqs.append(json.loads(line))
    ok = [r for r in reqs if r.get("success") or r.get("error") in (None, "", "ok")]
    # prefer explicit success flag
    ok = [r for r in reqs if r.get("success") is True or (r.get("error") is None and r.get("usage_present"))]
    ok = [r for r in reqs if not r.get("error")]
    totals = [float(r["total_ms"]) for r in ok]
    out_sum = sum(int(r["completion_tokens"]) for r in ok)
    in_sum = sum(int(r["prompt_tokens"]) for r in ok)
    power = d / "power_timeseries.csv"
    gross = energy_from_power_csv(power)
    net = net_energy(power, args.idle_w)
    perf = json.loads((d / "performance.json").read_text(encoding="utf-8"))
    report = {
        "cell_dir": str(d),
        "requests_total": len(reqs),
        "requests_success_recomputed": len(ok),
        "requests_success_reported": perf.get("requests_success"),
        "latency_p99_recomputed": round(percentile(totals, 0.99), 2) if totals else None,
        "latency_p99_reported": perf.get("latency_p99_ms"),
        "completion_tokens_sum_recomputed": out_sum,
        "completion_tokens_sum_reported": perf.get("actual_completion_tokens_sum"),
        "prompt_tokens_sum_recomputed": in_sum,
        "energy_j_recomputed": round(gross, 2),
        "energy_j_reported": perf.get("energy_j"),
        "net_energy_j_recomputed": round(net, 2),
        "net_energy_j_reported": perf.get("net_energy_j"),
        "j_per_act_out_recomputed": round(net / out_sum, 6) if out_sum else None,
        "j_per_act_out_reported": perf.get("net_j_per_actual_out_token"),
    }
    print(json.dumps(report, indent=2))
    mismatches = []
    for name, recomputed, reported in [
        ("requests_success", report["requests_success_recomputed"], report["requests_success_reported"]),
        ("completion_tokens_sum", report["completion_tokens_sum_recomputed"], report["completion_tokens_sum_reported"]),
        ("latency_p99", report["latency_p99_recomputed"], report["latency_p99_reported"]),
        ("net_energy_j", report["net_energy_j_recomputed"], report["net_energy_j_reported"]),
        ("j_per_act_out", report["j_per_act_out_recomputed"], report["j_per_act_out_reported"]),
    ]:
        if name in ("latency_p99", "net_energy_j", "j_per_act_out") and recomputed is not None and reported is not None:
            if abs(float(recomputed) - float(reported)) > max(0.05, 0.002 * abs(float(reported))):
                mismatches.append(name)
        elif recomputed != reported:
            mismatches.append(name)
    if mismatches:
        print("MISMATCH", mismatches)
        return 2
    print("VERIFY_OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
