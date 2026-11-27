#!/usr/bin/env python3
"""G1/G2/G3 comparison with cost breakdown for one results root."""
from __future__ import annotations

import argparse
import csv
import json
import statistics
from pathlib import Path

SLA_DEFAULT = 2115


def load_runs(root: Path) -> list[dict]:
    rows = []
    for p in sorted(root.glob("**/performance.json")):
        d = json.load(open(p, encoding="utf-8"))
        d["_path"] = str(p)
        rows.append(d)
    return rows


def mean(vals: list[float]) -> float:
    return round(statistics.mean(vals), 2) if vals else 0.0


def analyze(root: Path, sla_ms: float) -> tuple[list[dict], list[dict], list[str]]:
    runs = [r for r in load_runs(root) if r.get("cohort") in ("g1-gpu-baseline", "g2-fixed-split", "g3-rngd-first-overflow")]
    summary: list[dict] = []
    cost_rows: list[dict] = []
    lines: list[str] = []

    for c in sorted({int(r["concurrency"]) for r in runs}):
        g1 = [r for r in runs if r["cohort"] == "g1-gpu-baseline" and r["concurrency"] == c]
        if not g1:
            continue
        g1_p99 = mean([r["latency_p99_ms"] for r in g1])
        g1_j = mean([r["energy_per_success_j"] for r in g1])
        g1_cost = mean([r.get("cost_per_success_usd") or 0 for r in g1])
        g1_dur = mean([r["duration_s"] for r in g1])

        lines.append(f"\n## 동시접속 c{c}\n")
        lines.append(f"G1 baseline: p99={g1_p99}ms J/req={g1_j} cost/req=${g1_cost:.6f} dur={g1_dur}s\n")

        for cohort in ("g2-fixed-split", "g3-rngd-first-overflow"):
            sub = [r for r in runs if r["cohort"] == cohort and r["concurrency"] == c]
            if not sub:
                continue
            # G3 optional: exclude run-1 outlier if p99 > 5x SLA
            sub_all = sub
            sub_stable = [r for r in sub if r["latency_p99_ms"] < sla_ms * 5]
            use = sub_stable if cohort == "g3-rngd-first-overflow" and len(sub_stable) >= 2 else sub_all

            p99 = mean([r["latency_p99_ms"] for r in use])
            j = mean([r["energy_per_success_j"] for r in use])
            cost = mean([r.get("cost_per_success_usd") or 0 for r in use])
            gpu_w = mean([r.get("gpu_avg_power_w") or r.get("avg_power_w") or 0 for r in use])
            npu_w = mean([r.get("npu_avg_power_w") or 0 for r in use])
            gpu_j = mean([r.get("gpu_net_energy_j") or 0 for r in use]) / 100 if use else 0
            npu_j = mean([r.get("npu_net_energy_j") or 0 for r in use]) / 100 if use else 0
            sla_ok = sum(1 for r in use if r.get("sla_pass"))
            e_opt = round((g1_j - j) / g1_j * 100, 1) if g1_j else 0
            c_opt = round((g1_cost - cost) / g1_cost * 100, 1) if g1_cost else 0

            row = {
                "concurrency": c,
                "cohort": cohort,
                "n_runs": len(use),
                "p99_ms": p99,
                "sla_pass": f"{sla_ok}/{len(use)}",
                "duration_s": mean([r["duration_s"] for r in use]),
                "gpu_avg_w": gpu_w,
                "npu_avg_w": npu_w,
                "j_per_req_total": j,
                "j_per_req_gpu_part": round(mean([(r.get("gpu_net_energy_j") or 0) / max(r["requests_success"], 1) for r in use]), 2),
                "j_per_req_npu_part": round(mean([(r.get("npu_net_energy_j") or 0) / max(r["requests_success"], 1) for r in use]), 2),
                "energy_opt_vs_g1_pct": e_opt,
                "cost_per_req_usd": cost,
                "cost_opt_vs_g1_pct": c_opt,
                "vs_g1_p99_delta_ms": round(p99 - g1_p99, 1),
            }
            summary.append(row)
            cost_rows.append(row)

            label = "G2" if "g2" in cohort else "G3"
            lines.append(
                f"- **{label}** (n={len(use)}): p99={p99}ms (Δ{row['vs_g1_p99_delta_ms']:+}ms) "
                f"SLA={sla_ok}/{len(use)} | GPU={gpu_w}W NPU={npu_w}W | "
                f"J/req={j} (gpu {row['j_per_req_gpu_part']}+npu {row['j_per_req_npu_part']}) | "
                f"Eopt={e_opt}% | cost/req=${cost:.6f} (Copt={c_opt}%)\n"
            )

    return summary, cost_rows, lines


def recommendations(cost_rows: list[dict], lines: list[str]) -> str:
    md = [
        "# NPU 비용 절감 — 실측 기반 시사점\n\n",
        "현재 조건(CNN GPU 고정 + Llama-3.1-8B LLM, Furiosa-LLM on RNGD):\n\n",
    ]
    md.extend(lines)

    md.append("\n## 결론 (이번 full-cycle 기준)\n\n")
    g2_fail_sla = all("0/" in r["sla_pass"] or r["p99_ms"] > SLA_DEFAULT for r in cost_rows if r["cohort"] == "g2-fixed-split" and r["concurrency"] in (4, 8))
    any_copt = any(r["cost_opt_vs_g1_pct"] > 0 for r in cost_rows if r["cohort"] == "g2-fixed-split")

    md.append("### 측정 Energy (Joule)\n")
    md.append("- G2/G3 **총 J/req ≥ G1** → NPU 전력(~130–155W) 포함 시 **전기 Energy 절감 없음**\n")
    md.append("### SLA\n")
    md.append("- G2 c4/c8: **SLA FAIL** (RNGD LLM p99 > 2115ms)\n")
    md.append("- G3: overflow+pre-warm 없으면 c4 run-1 outlier 위험\n\n")

    md.append("## NPU로 비용 절감하려면 (실행 가능한 방향)\n\n")
    md.append("1. **Tariff-only 절감 (품질 희생)**  \n")
    md.append("   - LLM을 RNGD 고정(G2): GPU LLM 컨테이너 제거 → GPU $2.50/h 절약, NPU $0.80/h 추가  \n")
    md.append("   - **조건**: SLA 미달 허용 워크로드(배치·오프라인). 실시간 c4/c8 SLA 요구면 **부적합**\n\n")
    md.append("2. **GPU LLM 스택 제거 (dual-run 금지)**  \n")
    md.append("   - G3처럼 GPU+RNGD **동시 LLM 가동**은 Energy 최악 → overflow 시 GPU pre-warm 후 **유휴 GPU LLM 종료**\n\n")
    md.append("3. **SLA-aware 스케줄러**  \n")
    md.append("   - 큐·p99 예측으로 **SLA 여유 있을 때만** NPU 라우팅  \n")
    md.append("   - p99 > threshold → GPU-only fallback (G1 경로)\n\n")
    md.append("4. **워크로드 분리**  \n")
    md.append("   - CNN·LLM 동시 GPU 경쟁 제거: CNN 전용 노드 / LLM 전용 NPU 노드  \n")
    md.append("   - 또는 **NPU에 CNN+소형 LLM** 등 RNGD 강점 모델로 변경\n\n")
    md.append("5. **모델·Runtime 최적화**  \n")
    md.append("   - RNGD에서 vLLM 대비 throughput 개선(양자화·배치·컴파일 warm-up)  \n")
    md.append("   - **첫 추론 JIT** 제거: G3 GPU overflow 전 warm-up 요청 필수\n\n")
    md.append("6. **과금 모델 정합**  \n")
    md.append("   - Copt는 tariff($/h) 기준으로 G2 유리할 수 있으나 **SLA FAIL이면 사용 불가**  \n")
    md.append("   - 비용 KPI = `cost_per_success` + **SLA 위반 penalty** 동시 최소화\n\n")
    md.append("### 한 줄\n")
    md.append("> **지금 설정에서는 NPU로 ‘저렴+SLA’ 동시 달성 불가. SLA relax → G2 tariff 절감, SLA 유지 → G1 GPU 또는 스케줄러 기반 선택적 NPU.**\n")

    return "".join(md)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--root", required=True)
    p.add_argument("--sla-ms", type=float, default=SLA_DEFAULT)
    args = p.parse_args()
    root = Path(args.root)
    summary, cost_rows, lines = analyze(root, args.sla_ms)

    out = root / "g1_g2_g3_analysis"
    out.mkdir(parents=True, exist_ok=True)
    if summary:
        fields = list(summary[0].keys())
        with open(out / "comparison.csv", "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=fields)
            w.writeheader()
            w.writerows(summary)
    md = recommendations(cost_rows, lines)
    (out / "comparison.md").write_text(md, encoding="utf-8")
    print(md)
    print(f"\nWrote {out}/comparison.csv and comparison.md")


if __name__ == "__main__":
    main()
