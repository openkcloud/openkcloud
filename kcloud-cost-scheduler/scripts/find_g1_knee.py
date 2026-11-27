#!/usr/bin/env python3
"""Find G1 GPU knee C* from concurrency sweep (R2 gate).

C* = last concurrency where all repeats pass SLA (default 3/3).
Safe cap = last concurrency with p99 <= 95% of SLA and error <= 1%.
"""

from __future__ import annotations

import argparse
import csv
import json
import statistics
from pathlib import Path


def load_runs(root: Path, cohort: str, c: int) -> list[dict]:
    return [
        json.loads(p.read_text(encoding="utf-8"))
        for p in sorted(root.glob(f"{cohort}/run-*/c{c}/performance.json"))
    ]


def mean(runs: list[dict], key: str) -> float:
    vals = [r[key] for r in runs if key in r and isinstance(r[key], (int, float))]
    return statistics.mean(vals) if vals else 0.0


def sla_pass_count(runs: list[dict]) -> str:
    ok = sum(1 for r in runs if r.get("sla_pass"))
    return f"{ok}/{len(runs)}"


def find_knee(rows: list[dict], repeats: int) -> tuple[int, int, str]:
    """Return (C*, safe_cap, reason)."""
    by_c = {r["concurrency"]: r for r in rows}
    concs = sorted(by_c)
    c_star = 0
    safe_cap = 0
    reason = "none"

    for c in concs:
        r = by_c[c]
        passed = r["sla_pass_count"] == f"{repeats}/{repeats}"
        if passed:
            c_star = c
            reason = "last_full_sla_pass"
        if r["p99_ms"] <= r["sla_threshold_ms"] and r["error_rate_pct"] <= 1.0:
            safe_cap = c

    if c_star == 0:
        for c in concs:
            ok, total = by_c[c]["sla_pass_count"].split("/")
            if int(ok) > 0:
                c_star = c
                reason = "partial_sla_fallback"
    return c_star, safe_cap, reason


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--root", required=True, help="results root with g1-gpu-baseline/")
    p.add_argument("--cohort", default="g1-gpu-baseline")
    p.add_argument("--sla-ms", type=float, default=2115.0)
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--safe-sla-fraction", type=float, default=0.95)
    args = p.parse_args()
    root = Path(args.root)
    sla_threshold = args.sla_ms * args.safe_sla_fraction

    concurrencies = sorted(
        {int(p.name[1:]) for p in root.glob(f"{args.cohort}/run-1/c*") if p.name.startswith("c")}
    )
    if not concurrencies:
        concurrencies = sorted(
            {int(p.name[1:]) for p in root.glob(f"{args.cohort}/run-*/c*") if p.name.startswith("c")}
        )

    rows: list[dict] = []
    for c in concurrencies:
        runs = load_runs(root, args.cohort, c)
        if not runs:
            continue
        rows.append({
            "concurrency": c,
            "p99_ms": round(mean(runs, "latency_p99_ms"), 1),
            "p95_ms": round(mean(runs, "latency_p95_ms"), 1),
            "ttft_p95_ms": round(mean(runs, "ttft_p95_ms"), 1),
            "qps": round(mean(runs, "throughput_qps"), 3),
            "duration_s": round(mean(runs, "duration_s"), 2),
            "net_energy_j": round(mean(runs, "net_energy_j"), 1),
            "energy_per_success_j": round(mean(runs, "energy_per_success_j"), 1),
            "gpu_util_peak_pct": round(mean(runs, "gpu_util_peak_pct"), 1),
            "gpu_util_avg_pct": round(mean(runs, "gpu_util_avg_pct"), 1),
            "gpu_avg_power_w": round(mean(runs, "gpu_avg_power_w"), 1),
            "cnn_images_delta": round(
                statistics.mean(
                    [(r.get("cnn_telemetry") or {}).get("images_delta", 0) for r in runs]
                ),
                0,
            ),
            "sla_pass_count": sla_pass_count(runs),
            "error_rate_pct": round(mean(runs, "error_rate") * 100, 2),
            "sla_ms": args.sla_ms,
            "sla_threshold_ms": sla_threshold,
        })

    c_star, safe_cap, reason = find_knee(rows, args.repeats)

    out_dir = root / "knee_analysis"
    out_dir.mkdir(parents=True, exist_ok=True)
    csv_path = out_dir / "g1_concurrency_sweep.csv"
    with csv_path.open("w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=sorted({k for r in rows for k in r}))
        w.writeheader()
        w.writerows(rows)

    knee_data = {
        "gpu_knee_c_star": c_star,
        "gpu_safe_cap": safe_cap,
        "knee_reason": reason,
        "sla_ms": args.sla_ms,
        "safe_sla_fraction": args.safe_sla_fraction,
        "repeats_required": args.repeats,
        "crossover_points": {
            "c_star": c_star,
            "c_star_plus_1": c_star + 1 if c_star else None,
            "c_star_plus_2": c_star + 2 if c_star else None,
        },
    }
    (out_dir / "g1_knee.json").write_text(json.dumps(knee_data, indent=2), encoding="utf-8")

    print(f"G1 knee C*={c_star} ({reason}), safe_cap={safe_cap}")
    print(f"Crossover: C*={c_star}, C*+1={c_star + 1}, C*+2={c_star + 2}")
    print(f"Wrote {csv_path} and {out_dir / 'g1_knee.json'}")
    for r in rows:
        mark = " **C***" if r["concurrency"] == c_star else ""
        print(
            f"  c{r['concurrency']}: p99={r['p99_ms']} ms SLA={r['sla_pass_count']} "
            f"GPU util peak={r['gpu_util_peak_pct']}% net_J={r['net_energy_j']}{mark}"
        )


if __name__ == "__main__":
    main()
