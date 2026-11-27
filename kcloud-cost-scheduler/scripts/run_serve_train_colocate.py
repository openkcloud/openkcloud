#!/usr/bin/env python3
"""Colocate fixed-step GPU training with sustained LLM serving load.

Starts serving load in a thread, runs training for --train-steps, joins load.
Aborts early on serving SLA fail if --abort-on-sla.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--out-dir", required=True)
    p.add_argument("--serve-url", required=True)
    p.add_argument("--serve-concurrency", type=int, default=30)
    p.add_argument("--serve-duration-s", type=float, default=120.0)
    p.add_argument("--serve-warmup-s", type=float, default=10.0)
    p.add_argument("--train-steps", type=int, default=6000)
    p.add_argument("--train-batch", type=int, default=32)
    p.add_argument("--train-precision", default="fp16")
    p.add_argument("--idle-csv", required=True)
    p.add_argument("--sla-p99-ms", type=float, default=2115.0)
    p.add_argument("--abort-on-sla", action="store_true")
    p.add_argument("--experiment-id", default="serve-plus-train")
    p.add_argument("--variant", default="default")
    args = p.parse_args()

    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)
    serve_dir = out / "serving"
    train_dir = out / "training"
    serve_dir.mkdir(exist_ok=True)
    train_dir.mkdir(exist_ok=True)

    meta = {
        "started_at": now_iso(),
        "variant": args.variant,
        "experiment_id": args.experiment_id,
        "serve": {
            "url": args.serve_url,
            "concurrency": args.serve_concurrency,
            "duration_s": args.serve_duration_s,
            "warmup_s": args.serve_warmup_s,
            "sla_p99_ms": args.sla_p99_ms,
        },
        "train": {
            "steps": args.train_steps,
            "batch": args.train_batch,
            "precision": args.train_precision,
        },
        "abort_on_sla": args.abort_on_sla,
    }
    (out / "colocate_plan.json").write_text(json.dumps(meta, indent=2), encoding="utf-8")

    serve_rc = {"code": None, "done": False}
    train_rc = {"code": None, "done": False}

    def run_serve() -> None:
        cmd = [
            sys.executable,
            str(SCRIPT_DIR / "run_device_sustained_matrix.py"),
            "--out-root",
            str(serve_dir),
            "--url",
            args.serve_url,
            "--device",
            "gpu",
            "--concurrencies",
            str(args.serve_concurrency),
            "--repeats",
            "1",
            "--duration-s",
            str(args.serve_duration_s),
            "--warmup-s",
            str(args.serve_warmup_s),
            "--idle-csv",
            args.idle_csv,
            "--experiment-id",
            f"{args.experiment_id}-serve",
            "--cohort",
            "serve-sustained",
        ]
        print(f"[serve] START {now_iso()} {' '.join(cmd)}", flush=True)
        serve_rc["code"] = subprocess.call(cmd)
        serve_rc["done"] = True
        print(f"[serve] DONE rc={serve_rc['code']} {now_iso()}", flush=True)

    def run_train() -> None:
        # small delay so serving warmup begins first
        time.sleep(max(args.serve_warmup_s - 2.0, 1.0))
        cmd = [
            sys.executable,
            str(SCRIPT_DIR / "gpu_train_fixed_steps.py"),
            "--out-dir",
            str(train_dir),
            "--experiment-id",
            f"{args.experiment_id}-train",
            "--batch",
            str(args.train_batch),
            "--steps",
            str(args.train_steps),
            "--precision",
            args.train_precision,
            "--idle-csv",
            args.idle_csv,
        ]
        print(f"[train] START {now_iso()} {' '.join(cmd)}", flush=True)
        train_rc["code"] = subprocess.call(cmd)
        train_rc["done"] = True
        print(f"[train] DONE rc={train_rc['code']} {now_iso()}", flush=True)

    ts = threading.Thread(target=run_serve, daemon=False)
    tt = threading.Thread(target=run_train, daemon=False)
    ts.start()
    tt.start()
    tt.join()
    ts.join()

    # collect results
    serve_perf = None
    for p in serve_dir.glob("serve-sustained/run-*/c*/performance.json"):
        serve_perf = json.loads(p.read_text(encoding="utf-8"))
        break
    train_perf = None
    tr = train_dir / "train_result.json"
    if tr.exists():
        train_perf = json.loads(tr.read_text(encoding="utf-8"))

    abort = None
    if train_perf and train_perf.get("abort_reason"):
        abort = f"train:{train_perf['abort_reason']}"
    if serve_perf:
        if serve_perf.get("error_rate", 0) > 0.01:
            abort = abort or "serve:error_rate"
        if serve_perf.get("latency_p99_ms", 0) > args.sla_p99_ms:
            reason = f"serve:sla_p99={serve_perf['latency_p99_ms']}>{args.sla_p99_ms}"
            if args.abort_on_sla:
                abort = abort or reason
            else:
                # record but don't force fail if not aborting
                pass

    summary = {
        "ended_at": now_iso(),
        "variant": args.variant,
        "serve_rc": serve_rc["code"],
        "train_rc": train_rc["code"],
        "abort_reason": abort,
        "serving": serve_perf,
        "training": train_perf,
        "deltas_note": "Compare serving p99/TTFT vs serve-only baseline; train step_ms vs train-solo",
    }
    (out / "colocate_summary.json").write_text(json.dumps(summary, indent=2), encoding="utf-8")
    print(json.dumps(summary, indent=2), flush=True)
    if abort and args.abort_on_sla:
        return 3
    if serve_rc["code"] not in (0, None) or train_rc["code"] not in (0, None):
        return 4
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
