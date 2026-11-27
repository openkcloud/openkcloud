#!/usr/bin/env python3
"""Recompute net_energy_j from power.csv using corrected idle-power.csv."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path


def load_idle(path: Path) -> dict[str, float]:
    buckets: dict[str, list[float]] = {}
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            buckets.setdefault(row["source"], []).append(float(row["watts"]))
    return {k: sum(v) / len(v) for k, v in buckets.items() if v}


def net_energy_from_csv(power_csv: Path, idle_by_source: dict[str, float]) -> dict[str, float]:
    samples: dict[str, list[tuple[float, float]]] = {}
    with open(power_csv, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            src = row["source"]
            samples.setdefault(src, []).append((float(row["timestamp_unix"]), float(row["watts"])))

    per_source: dict[str, float] = {}
    total = 0.0
    for src, pts in samples.items():
        idle = idle_by_source.get(src, 0.0)
        src_net = 0.0
        for i in range(1, len(pts)):
            dt = pts[i][0] - pts[i - 1][0]
            src_net += max(pts[i][1] - idle, 0.0) * dt
        per_source[src] = round(src_net, 2)
        total += src_net
    return {
        "net_energy_j": round(total, 2),
        "gpu_net_energy_j": per_source.get("gpu", 0.0),
        "npu_net_energy_j": per_source.get("npu", 0.0),
        "idle_power_w": round(sum(idle_by_source.values()), 2),
    }


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--root", required=True)
    p.add_argument("--idle-csv", default="")
    args = p.parse_args()
    root = Path(args.root)
    idle_path = Path(args.idle_csv) if args.idle_csv else root / "idle-power.csv"
    idle_map = load_idle(idle_path)
    if not idle_map:
        raise SystemExit(f"No idle data in {idle_path}")

    updated = 0
    for perf_path in sorted(root.glob("**/performance.json")):
        if "g3-cap" in str(perf_path):
            continue
        run_dir = perf_path.parent
        power_csv = run_dir / "power.csv"
        if not power_csv.exists():
            continue
        costs = net_energy_from_csv(power_csv, idle_map)
        with open(perf_path, encoding="utf-8") as f:
            data = json.load(f)
        success = data.get("requests_success") or 0
        data.update(costs)
        if success:
            data["energy_per_success_j"] = round(costs["net_energy_j"] / success, 2)
        with open(perf_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2)
        updated += 1
        print(f"rescored {perf_path.relative_to(root)} net={costs['net_energy_j']}J idle={costs['idle_power_w']}W")

    print(f"Updated {updated} runs using idle {idle_map}")


if __name__ == "__main__":
    main()
