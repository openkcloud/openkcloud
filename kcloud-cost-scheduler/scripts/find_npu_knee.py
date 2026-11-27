#!/usr/bin/env python3
"""Find NPU knee from G2(RNGD-only) saturation signals — NOT GPU comparison.

Bottleneck gate (all on G2 curve):
  - NPU util peak/avg approaching saturation
  - latency_p95 / ttft_p95 / p99 jump vs previous concurrency
  - SLA optional backstop

GPU util on G2 = CNN background only (recorded, not used as knee gate).
GPU reference max c from G1 = spillover sizing hint only.
"""
from __future__ import annotations

import argparse
import csv
import json
import statistics
from pathlib import Path


def load(root: Path, cohort: str, c: int) -> list[dict]:
    return [
        json.load(open(p, encoding="utf-8"))
        for p in sorted(root.glob(f"{cohort}/run-*/c{c}/performance.json"))
    ]


def mean(runs: list[dict], key: str) -> float:
    vals = [r[key] for r in runs if key in r and isinstance(r[key], (int, float))]
    return statistics.mean(vals) if vals else 0.0


def find_npu_knee(
    rows: list[dict],
    sla_ms: float,
    npu_util_sat_pct: float,
    p95_jump_ms: float,
    ttft_p95_jump_ms: float,
    p99_jump_ms: float,
) -> tuple[int, str]:
    by_c = {r["concurrency"]: r for r in rows}
    concs = sorted(by_c)
    knee = 0
    reason = "none"
    prev = None
    for c in concs:
        r = by_c[c]
        npu_ok = r["g2_npu_util_peak_pct"] < npu_util_sat_pct if r["g2_npu_util_peak_pct"] else True
        sla_ok = r["g2_p99_ms"] <= sla_ms
        if prev is None:
            stable = True
        else:
            stable = (
                r["g2_p95_ms"] - prev["g2_p95_ms"] <= p95_jump_ms
                and r["g2_ttft_p95_ms"] - prev["g2_ttft_p95_ms"] <= ttft_p95_jump_ms
                and r["g2_p99_ms"] - prev["g2_p99_ms"] <= p99_jump_ms
            )
        if npu_ok and stable and sla_ok:
            knee = c
            reason = "npu_util+p95+ttft+p99_stable"
        elif npu_ok and sla_ok and knee == 0:
            knee = c
            reason = "npu_util+sla_only"
        prev = r
    if knee == 0:
        for c in concs:
            if by_c[c]["g2_p99_ms"] <= sla_ms:
                knee = c
                reason = "sla_fallback"
    return knee, reason


def find_gpu_ref(rows: list[dict], sla_ms: float) -> int:
    knee = 0
    for r in rows:
        if "g1_p99_ms" in r and r["g1_p99_ms"] <= sla_ms:
            knee = r["concurrency"]
    return knee


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--root", required=True)
    p.add_argument("--sla-ms", type=float, default=2115)
    p.add_argument("--npu-util-sat-pct", type=float, default=85.0)
    p.add_argument("--p95-jump-ms", type=float, default=60.0)
    p.add_argument("--ttft-p95-jump-ms", type=float, default=80.0)
    p.add_argument("--p99-jump-ms", type=float, default=80.0)
    args = p.parse_args()
    root = Path(args.root)

    concurrencies = sorted(
        {int(p.name[1:]) for p in root.glob("g2-fixed-split/run-1/c*") if p.name.startswith("c")}
    )
    rows = []
    for c in concurrencies:
        g1 = load(root, "g1-gpu-baseline", c)
        g2 = load(root, "g2-fixed-split", c)
        if not g2:
            continue
        row = {
            "concurrency": c,
            "g2_p99_ms": round(mean(g2, "latency_p99_ms"), 1),
            "g2_p95_ms": round(mean(g2, "latency_p95_ms"), 1),
            "g2_ttft_p95_ms": round(mean(g2, "ttft_p95_ms"), 1),
            "g2_ttft_p99_ms": round(mean(g2, "ttft_p99_ms"), 1),
            "g2_tpot_p50_ms": round(mean(g2, "tpot_p50_ms"), 1),
            "g2_qps": round(mean(g2, "throughput_qps"), 3),
            "g2_duration_s": round(mean(g2, "duration_s"), 1),
            "g2_j_per_req": round(mean(g2, "energy_per_success_j"), 1),
            "g2_npu_avg_w": round(mean(g2, "npu_avg_power_w"), 1),
            "g2_gpu_avg_w": round(mean(g2, "gpu_avg_power_w"), 1),
            "g2_npu_util_peak_pct": round(mean(g2, "npu_util_peak_pct"), 1),
            "g2_npu_util_avg_pct": round(mean(g2, "npu_util_avg_pct"), 1),
            "g2_gpu_util_peak_pct": round(mean(g2, "gpu_util_peak_pct"), 1),
            "g2_gpu_util_avg_pct": round(mean(g2, "gpu_util_avg_pct"), 1),
            "g2_sla_pass": f"{sum(1 for r in g2 if r.get('sla_pass'))}/{len(g2)}",
        }
        if g1:
            row.update({
                "g1_p99_ms": round(mean(g1, "latency_p99_ms"), 1),
                "g1_p95_ms": round(mean(g1, "latency_p95_ms"), 1),
                "g1_qps": round(mean(g1, "throughput_qps"), 3),
                "reference_only": True,
            })
        rows.append(row)

    concs = sorted(r["concurrency"] for r in rows)
    by_c = {r["concurrency"]: r for r in rows}
    for r in rows:
        c = r["concurrency"]
        idx = concs.index(c)
        if idx > 0:
            p = by_c[concs[idx - 1]]
            r["delta_p95_ms"] = round(r["g2_p95_ms"] - p["g2_p95_ms"], 1)
            r["delta_ttft_p95_ms"] = round(r["g2_ttft_p95_ms"] - p["g2_ttft_p95_ms"], 1)
            r["delta_p99_ms"] = round(r["g2_p99_ms"] - p["g2_p99_ms"], 1)
        else:
            r["delta_p95_ms"] = 0.0
            r["delta_ttft_p95_ms"] = 0.0
            r["delta_p99_ms"] = 0.0

    npu_knee, npu_reason = find_npu_knee(
        rows, args.sla_ms, args.npu_util_sat_pct,
        args.p95_jump_ms, args.ttft_p95_jump_ms, args.p99_jump_ms,
    )
    gpu_ref = find_gpu_ref(rows, args.sla_ms)

    out = root / "knee_analysis"
    out.mkdir(parents=True, exist_ok=True)
    csv_path = out / "concurrency_sweep.csv"
    with open(csv_path, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=sorted({k for r in rows for k in r}))
        w.writeheader()
        w.writerows(rows)

    knee_data = {
        "npu_concurrency_cap": npu_knee,
        "npu_knee_reason": npu_reason,
        "gpu_reference_max_c": gpu_ref,
        "criteria": {
            "bottleneck_gate": "G2 RNGD only — NOT GPU comparison",
            "npu_util_sat_pct_max": args.npu_util_sat_pct,
            "p95_jump_ms_max": args.p95_jump_ms,
            "ttft_p95_jump_ms_max": args.ttft_p95_jump_ms,
            "p99_jump_ms_max": args.p99_jump_ms,
            "sla_ms_backstop": args.sla_ms,
            "gpu_util_on_g2": "CNN background only — recorded, not knee gate",
        },
    }
    (out / "knee.json").write_text(json.dumps(knee_data, indent=2), encoding="utf-8")

    print(f"NPU knee: c={npu_knee} ({npu_reason})")
    print(f"GPU ref (G1 SLA max): c={gpu_ref}")
    print("Criteria: NPU util + p95/ttft_p95/p99 delta on G2 curve")
    for r in rows:
        mark = " **knee**" if r["concurrency"] == npu_knee else ""
        print(
            f"  c{r['concurrency']}: NPU util peak={r['g2_npu_util_peak_pct']}% "
            f"p95={r['g2_p95_ms']} Δp95={r['delta_p95_ms']} "
            f"ttft_p95={r['g2_ttft_p95_ms']} GPU util={r['g2_gpu_util_peak_pct']}% (CNN){mark}"
        )


if __name__ == "__main__":
    main()
