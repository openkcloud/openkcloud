#!/usr/bin/env python3
"""Aggregate mixed CNN+LLM G1/G2/G3 runs into summary.csv and result.md."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path


def load_runs(root: Path) -> list[dict]:
    rows = []
    seen: set[str] = set()
    roots = [root]
    link = root / "g1-gpu-baseline"
    if link.is_symlink():
        roots.append(link.resolve())
    for base in roots:
        for perf in sorted(base.glob("**/performance.json")):
            key = str(perf.resolve())
            if key in seen:
                continue
            seen.add(key)
            with open(perf, encoding="utf-8") as f:
                d = json.load(f)
            d["_path"] = str(perf)
            rows.append(d)
    return rows


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--root", default="results/mixed-cnn-llm")
    p.add_argument("--baseline-cohort", default="g1-gpu-baseline")
    args = p.parse_args()
    root = Path(args.root)
    runs = load_runs(root)
    if not runs:
        print("No runs found")
        return

    baseline = args.baseline_cohort
    base_energy_by_c: dict[int, float] = {}
    base_cost_by_c: dict[int, float] = {}
    for c in sorted({int(r.get("concurrency", 0)) for r in runs if r.get("cohort") == baseline}):
        g1 = [
            r for r in runs
            if r.get("cohort") == baseline and r.get("concurrency") == c and r.get("requests_success", 0) > 0
        ]
        if g1:
            base_energy_by_c[c] = sum(r.get("net_energy_j") or r.get("energy_j") or 0 for r in g1) / len(g1)
            base_cost_by_c[c] = sum(r.get("cost_per_success_usd") or 0 for r in g1) / len(g1)

    fields = [
        "run_id", "cohort", "concurrency", "repeat", "allocation_mode", "runtime",
        "requests_success", "error_rate", "throughput_qps",
        "latency_p50_ms", "latency_p95_ms", "latency_p99_ms",
        "ttft_p50_ms", "tpot_p50_ms",
        "avg_power_w", "peak_power_w", "gpu_avg_power_w", "npu_avg_power_w", "idle_power_w",
        "energy_j", "gpu_energy_j", "npu_energy_j", "net_energy_j",
        "gpu_net_energy_j", "npu_net_energy_j",
        "energy_per_success_j", "energy_per_1k_tokens_j",
        "duration_s",
        "energy_cost_usd", "gpu_cost_usd", "npu_cost_usd", "total_cost_usd",
        "cost_per_success_usd",
        "energy_optimization_rate_pct", "cost_optimization_rate_pct", "sla_pass",
    ]

    summary_rows = []
    for r in runs:
        net_e = r.get("net_energy_j") or r.get("energy_j") or 0
        cps = r.get("cost_per_success_usd") or 0
        conc = int(r.get("concurrency") or 0)
        base_e = base_energy_by_c.get(conc, 0.0)
        base_c = base_cost_by_c.get(conc, 0.0)
        e_opt = ((base_e - net_e) / base_e * 100) if base_e > 0 and r.get("cohort") != baseline else 0.0
        c_opt = ((base_c - cps) / base_c * 100) if base_c > 0 and r.get("cohort") != baseline else 0.0
        row = {k: r.get(k, "") for k in fields if k not in ("energy_optimization_rate_pct", "cost_optimization_rate_pct")}
        row["energy_optimization_rate_pct"] = round(e_opt, 2)
        row["cost_optimization_rate_pct"] = round(c_opt, 2)
        summary_rows.append(row)

    summary_path = root / "summary.csv"
    with open(summary_path, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=fields)
        w.writeheader()
        w.writerows(summary_rows)

    paper_path = root / "paper-table.csv"
    with open(paper_path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow([
            "cohort", "concurrency", "p99_ms", "net_energy_j",
            "energy_per_success_j", "energy_per_1k_tokens_j",
            "cost_per_success_usd", "energy_opt_pct", "cost_opt_pct", "sla_pass",
        ])
        for r in summary_rows:
            w.writerow([
                r["cohort"], r["concurrency"], r["latency_p99_ms"], r["net_energy_j"],
                r["energy_per_success_j"], r["energy_per_1k_tokens_j"],
                r["cost_per_success_usd"], r["energy_optimization_rate_pct"],
                r["cost_optimization_rate_pct"], r["sla_pass"],
            ])

    md = root / "result.md"
    lines = [
        "# Mixed CNN(GPU bg) + LLM Experiment\n\n",
        f"Baseline cohort: `{baseline}` (per-concurrency G1 average)\n",
        f"Baseline net_energy_j by concurrency: {base_energy_by_c}\n",
        f"Baseline cost/success by concurrency: {base_cost_by_c}\n\n",
        "Note: Custom Kubernetes Scheduler not used today — manual LLM routing.\n\n",
    ]
    for cohort in (
        "g1-gpu-baseline",
        "g2-fixed-split",
        "g3-rngd-first-overflow",
        "g3-npu-cap-overflow",
        "g3-cap1",
        "g3-cap2",
        "g3-cap3",
        "b0-gpu",
        "b1-rngd",
        "b2-static-overflow",
        "p1-prewarm-overflow",
    ):
        sub = [r for r in summary_rows if r["cohort"] == cohort]
        if not sub:
            continue
        lines.append(f"## {cohort}\n")
        lines.append("| c | p99 | net_E | J/req | J/1Ktok | cost/success | Eopt% | Copt% | SLA |\n")
        lines.append("|---|-----|-------|-------|---------|--------------|-------|-------|-----|\n")
        for r in sub:
            lines.append(
                f"| {r['concurrency']} | {r['latency_p99_ms']} | {r['net_energy_j']} | "
                f"{r['energy_per_success_j']} | {r['energy_per_1k_tokens_j']} | "
                f"{r['cost_per_success_usd']} | {r['energy_optimization_rate_pct']} | "
                f"{r['cost_optimization_rate_pct']} | {r['sla_pass']} |\n"
            )
        lines.append("\n")
    md.write_text("".join(lines), encoding="utf-8")
    print(f"Wrote {summary_path}, {paper_path}, {md}")


if __name__ == "__main__":
    main()
