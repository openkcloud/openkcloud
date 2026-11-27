#!/usr/bin/env python3
"""Device-agnostic sustained LLM matrix (GPU or NPU) for fair compare.

Same protocol as run_gpu_sustained_matrix:
  warmup_s (unscored) → duration_s closed-loop concurrency → drain=0
  (monitor stops when worker pool joins; in-flight tails in duration_s).

Records p50/p95/p99, TTFT, TPOT, success QPS, gross/net J, J/success,
and per-request actual prompt/completion tokens in requests.jsonl.
"""
from __future__ import annotations

import argparse
import csv
import json
import os
import re
import subprocess
import sys
import threading
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))
from heterogeneous_serving_experiment import (  # noqa: E402
    OUTPUT_TOKENS,
    PowerMonitor,
    discover_model,
    percentile,
    streaming_request,
)

SLA_P99_MS = 2115.0


@dataclass
class Agg:
    device: str
    concurrency: int
    repeat: int
    duration_s: float
    requests_total: int
    requests_success: int
    error_rate: float
    qps: float
    latency_p50_ms: float
    latency_p95_ms: float
    latency_p99_ms: float
    ttft_p50_ms: float
    ttft_p95_ms: float
    ttft_p99_ms: float
    tpot_p50_ms: float
    tpot_p95_ms: float
    tpot_p99_ms: float
    sla_pass: bool
    net_energy_j: float
    energy_j: float
    energy_per_success_j: float
    energy_per_out_tok_j: float
    prompt_tokens_sum: int
    completion_tokens_sum: int
    avg_power_w: float
    peak_power_w: float
    util_avg_pct: float
    util_peak_pct: float


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def load_idle(path: Path, source: str) -> float:
    vals = []
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if row.get("source") == source:
                vals.append(float(row["watts"]))
    return sum(vals) / len(vals) if vals else 0.0


def net_energy_from_monitor(mon: PowerMonitor, source: str, idle_w: float) -> float:
    samples = [x for x in mon.samples if x.source == source]
    if len(samples) < 2:
        return 0.0
    net = 0.0
    for i in range(1, len(samples)):
        dt = samples[i].ts - samples[i - 1].ts
        net += max(samples[i].watts - idle_w, 0.0) * dt
    return round(net, 2)


def sustained_load(
    base_url: str,
    model: str,
    concurrency: int,
    duration_s: float,
    warmup_s: float,
    power_source: str,
) -> tuple[list, PowerMonitor, float]:
    if warmup_s > 0:
        warm_end = time.time() + warmup_s

        def warm_one(_: int) -> None:
            while time.time() < warm_end:
                streaming_request(base_url, model)

        with ThreadPoolExecutor(max_workers=min(concurrency, 8)) as ex:
            futs = [ex.submit(warm_one, i) for i in range(min(concurrency, 8))]
            for f in as_completed(futs):
                f.result()

    mon = PowerMonitor(
        [power_source],
        0.5,
        sample_gpu_util=(power_source == "gpu"),
        sample_npu_util=(power_source == "npu"),
    )
    mon.start()
    t0 = time.perf_counter()
    stop_at = time.time() + duration_s
    results: list = []
    lock = threading.Lock()

    def worker(_: int) -> None:
        while time.time() < stop_at:
            r = streaming_request(base_url, model)
            with lock:
                results.append(r)

    with ThreadPoolExecutor(max_workers=concurrency) as ex:
        futs = [ex.submit(worker, i) for i in range(concurrency)]
        for f in as_completed(futs):
            f.result()

    duration = time.perf_counter() - t0
    mon.stop()
    return results, mon, duration


def aggregate(
    results: list,
    mon: PowerMonitor,
    duration: float,
    idle_w: float,
    device: str,
    power_source: str,
    concurrency: int,
    repeat: int,
) -> Agg:
    ok = [r for r in results if not r.error]
    errs = [r for r in results if r.error]
    totals = [r.total_ms for r in ok]
    ttfts = [r.ttft_ms for r in ok]
    tpots = [r.tpot_ms for r in ok if r.tpot_ms is not None]
    err_rate = len(errs) / len(results) if results else 1.0
    p99 = percentile(totals, 0.99) if totals else 1e9
    net_e = net_energy_from_monitor(mon, power_source, idle_w)
    success = len(ok)
    pin = sum(int(r.prompt_tokens or 0) for r in ok)
    pout = sum(int(r.completion_tokens or 0) for r in ok)
    util_key = "gpu_util" if power_source == "gpu" else "npu_util"
    return Agg(
        device=device,
        concurrency=concurrency,
        repeat=repeat,
        duration_s=round(duration, 2),
        requests_total=len(results),
        requests_success=success,
        error_rate=round(err_rate, 4),
        qps=round(success / duration, 3) if duration > 0 else 0.0,
        latency_p50_ms=round(percentile(totals, 0.50), 2) if totals else 0.0,
        latency_p95_ms=round(percentile(totals, 0.95), 2) if totals else 0.0,
        latency_p99_ms=round(p99, 2) if totals else 0.0,
        ttft_p50_ms=round(percentile(ttfts, 0.50), 2) if ttfts else 0.0,
        ttft_p95_ms=round(percentile(ttfts, 0.95), 2) if ttfts else 0.0,
        ttft_p99_ms=round(percentile(ttfts, 0.99), 2) if ttfts else 0.0,
        tpot_p50_ms=round(percentile(tpots, 0.50), 2) if tpots else 0.0,
        tpot_p95_ms=round(percentile(tpots, 0.95), 2) if tpots else 0.0,
        tpot_p99_ms=round(percentile(tpots, 0.99), 2) if tpots else 0.0,
        sla_pass=bool(totals) and p99 <= SLA_P99_MS and err_rate <= 0.01,
        net_energy_j=net_e,
        energy_j=round(mon.energy_j(power_source), 2),
        energy_per_success_j=round(net_e / success, 4) if success else 0.0,
        energy_per_out_tok_j=round(net_e / pout, 6) if pout else 0.0,
        prompt_tokens_sum=pin,
        completion_tokens_sum=pout,
        avg_power_w=mon.avg_w(power_source) or 0.0,
        peak_power_w=mon.peak_w(power_source) or 0.0,
        util_avg_pct=mon.util_avg(util_key) or 0.0,
        util_peak_pct=mon.util_peak(util_key) or 0.0,
    )


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--out-root", required=True)
    p.add_argument("--url", required=True)
    p.add_argument("--device", choices=["gpu", "npu"], required=True)
    p.add_argument("--concurrencies", default="1,2")
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--duration-s", type=float, default=120.0)
    p.add_argument("--warmup-s", type=float, default=10.0)
    p.add_argument("--idle-csv", required=True)
    p.add_argument("--experiment-id", default="")
    p.add_argument("--cohort", default="")
    args = p.parse_args()

    power_source = "gpu" if args.device == "gpu" else "npu"
    out = Path(args.out_root)
    out.mkdir(parents=True, exist_ok=True)
    log_path = out / "probe.log"

    def log(msg: str) -> None:
        line = f"[{now_iso()}] {msg}"
        print(line, flush=True)
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(line + "\n")

    concs = [int(x.strip()) for x in args.concurrencies.split(",") if x.strip()]
    idle_w = load_idle(Path(args.idle_csv), power_source)
    if idle_w <= 0:
        raise SystemExit(f"idle watts missing for source={power_source} in {args.idle_csv}")
    if Path(args.idle_csv).resolve() != (out / "idle-power.csv").resolve():
        (out / "idle-power.csv").write_bytes(Path(args.idle_csv).read_bytes())

    model = discover_model(args.url)
    proto = {
        "experiment_id": args.experiment_id,
        "device": args.device,
        "power_source": power_source,
        "url": args.url,
        "model": model,
        "max_output_tokens": OUTPUT_TOKENS,
        "warmup_s": args.warmup_s,
        "duration_s": args.duration_s,
        "drain_s": 0.0,
        "drain_policy": "none; mon.stop after worker join; in-flight tails in duration_s",
        "inter_repeat_wait_s": 0.0,
        "idle_w": idle_w,
        "idle_csv": str(args.idle_csv),
        "power_interval_s": 0.5,
        "energy": "net=sum max(W-idle,0)*dt; gross=sum W*dt; device_scope=single_source",
        "prompt": "shared BASE_PROMPT + trade-offs question; temperature=0",
        "FURIOSA_METRICS_URL": os.environ.get("FURIOSA_METRICS_URL"),
    }
    (out / "measurement_protocol.json").write_text(json.dumps(proto, indent=2), encoding="utf-8")
    log(
        f"device={args.device} model={model} idle_w={idle_w:.2f} "
        f"concs={concs} repeats={args.repeats} duration={args.duration_s}s"
    )

    summary_rows: list[dict[str, Any]] = []
    cohort = args.cohort or f"{args.device}-sustained"

    for conc in concs:
        for rep in range(1, args.repeats + 1):
            run_dir = out / cohort / f"run-{rep}" / f"c{conc}"
            if (run_dir / "performance.json").exists():
                log(f"SKIP existing device={args.device} c={conc} r={rep}")
                continue
            run_dir.mkdir(parents=True, exist_ok=True)
            log(f"=== START device={args.device} c={conc} r={rep} sustained {args.duration_s}s ===")
            results, mon, duration = sustained_load(
                args.url, model, conc, args.duration_s, args.warmup_s, power_source
            )
            agg = aggregate(results, mon, duration, idle_w, args.device, power_source, conc, rep)
            (run_dir / "performance.json").write_text(json.dumps(asdict(agg), indent=2), encoding="utf-8")
            with open(run_dir / "power.csv", "w", newline="", encoding="utf-8") as f:
                w = csv.writer(f)
                w.writerow(["timestamp_unix", "watts", "source"])
                for s in mon.samples:
                    if s.source == power_source:
                        w.writerow([s.ts, s.watts, s.source])
            with open(run_dir / "util.csv", "w", newline="", encoding="utf-8") as f:
                w = csv.writer(f)
                w.writerow(["timestamp_unix", "metric", "value"])
                for s in mon.util_samples:
                    w.writerow([s.ts, s.source, s.watts])
            with open(run_dir / "requests.jsonl", "w", encoding="utf-8") as f:
                for r in results:
                    f.write(json.dumps(asdict(r)) + "\n")
            summary_rows.append(asdict(agg))
            log(
                f"DONE device={args.device} c={conc} r={rep} dur={agg.duration_s}s "
                f"n={agg.requests_success}/{agg.requests_total} p99={agg.latency_p99_ms} "
                f"qps={agg.qps} netJ={agg.net_energy_j} J/req={agg.energy_per_success_j} "
                f"J/outTok={agg.energy_per_out_tok_j} outTok={agg.completion_tokens_sum} "
                f"SLA={'PASS' if agg.sla_pass else 'FAIL'}"
            )

    if summary_rows:
        with open(out / "summary.csv", "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=list(summary_rows[0].keys()))
            w.writeheader()
            w.writerows(summary_rows)
    log("PROBE COMPLETE")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
