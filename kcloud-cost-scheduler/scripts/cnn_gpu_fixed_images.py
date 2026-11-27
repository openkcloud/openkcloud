#!/usr/bin/env python3
"""ResNet50 GPU inference with fixed image count termination.

Processes exactly --images images (batch-aligned), synchronizes the last GPU
op, then exits. Synthetic tensor input (not a dataset accuracy run).

Emits per-job phase bounds (unix) for orchestrator union timing:
  load_* → warm_* → optional wait_* (ready-file) → run_*
"""

from __future__ import annotations

import argparse
import hashlib
import json
import time
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--images", type=int, required=True, help="total images to complete (M)")
    p.add_argument("--batch", type=int, default=8)
    p.add_argument("--precision", choices=["fp16", "fp32"], default="fp16")
    p.add_argument("--seed", type=int, default=20261027)
    p.add_argument("--warmup-batches", type=int, default=10)
    p.add_argument("--status-file", default="")
    p.add_argument("--result-file", default="")
    p.add_argument(
        "--ready-file",
        default="",
        help="if set, wait until this path exists before measured loop",
    )
    args = p.parse_args()

    if args.images <= 0:
        raise SystemExit("--images must be > 0")
    if args.batch <= 0 or args.images % args.batch != 0:
        raise SystemExit("--images must be a positive multiple of --batch")

    import torch
    import torchvision.models as models

    torch.manual_seed(args.seed)
    if torch.cuda.is_available():
        torch.cuda.manual_seed_all(args.seed)

    device = torch.device("cuda")
    dtype = torch.float16 if args.precision == "fp16" else torch.float32

    load_start_unix = time.time()
    weights = models.ResNet50_Weights.DEFAULT
    model = models.resnet50(weights=weights)
    model = model.to(device=device, dtype=dtype).eval()
    dummy = torch.randn(args.batch, 3, 224, 224, device=device, dtype=dtype)
    if torch.cuda.is_available():
        torch.cuda.synchronize()
    load_end_unix = time.time()

    input_sha = hashlib.sha256(
        f"seed={args.seed};shape={tuple(dummy.shape)};dtype={dtype}".encode()
    ).hexdigest()

    warm_start_unix = time.time()
    with torch.no_grad():
        for _ in range(max(args.warmup_batches, 0)):
            model(dummy)
        torch.cuda.synchronize()
    warm_end_unix = time.time()

    wait_start_unix: float | None = None
    wait_end_unix: float | None = None
    if args.ready_file:
        wait_start_unix = time.time()
        ready = Path(args.ready_file)
        while not ready.exists():
            time.sleep(0.05)
        wait_end_unix = time.time()

    started = time.time()
    started_perf = time.perf_counter()
    images = 0
    forwards = 0
    latencies_ms: list[float] = []

    def write_status(running: bool) -> None:
        if not args.status_file:
            return
        elapsed = max(time.time() - started, 1e-6)
        payload = {
            "running": running,
            "batch": args.batch,
            "precision": args.precision,
            "target_images": args.images,
            "images": images,
            "forwards": forwards,
            "started_at": started,
            "elapsed_s": round(elapsed, 3),
            "throughput_ips": round(images / elapsed, 3),
            "input_sha256": input_sha,
            "weights": str(weights),
        }
        Path(args.status_file).write_text(json.dumps(payload), encoding="utf-8")

    write_status(True)
    with torch.no_grad():
        while images < args.images:
            t0 = time.perf_counter()
            model(dummy)
            torch.cuda.synchronize()
            latencies_ms.append((time.perf_counter() - t0) * 1000.0)
            images += args.batch
            forwards += 1
            if forwards % 50 == 0:
                write_status(True)

    ended = time.time()
    ended_perf = time.perf_counter()
    write_status(False)

    result = {
        "ok": images == args.images,
        "job_id": "cnn",
        "images_requested": args.images,
        "images_completed": images,
        "batch": args.batch,
        "precision": args.precision,
        "seed": args.seed,
        "input_sha256": input_sha,
        "weights": str(weights),
        "warmup_batches": args.warmup_batches,
        "load_start_unix": load_start_unix,
        "load_end_unix": load_end_unix,
        "warm_start_unix": warm_start_unix,
        "warm_end_unix": warm_end_unix,
        "wait_start_unix": wait_start_unix,
        "wait_end_unix": wait_end_unix,
        "run_start_unix": started,
        "run_end_unix": ended,
        "started_unix": started,
        "ended_unix": ended,
        "duration_s": ended_perf - started_perf,
        "throughput_ips": images / max(ended_perf - started_perf, 1e-6),
        "latency_ms_avg": sum(latencies_ms) / max(len(latencies_ms), 1),
        "vram_mb": float(torch.cuda.memory_allocated(device) // (1024 * 1024)),
    }
    text = json.dumps(result, indent=2)
    if args.result_file:
        Path(args.result_file).write_text(text + "\n", encoding="utf-8")
    print(text)
    if images != args.images:
        raise SystemExit(2)


if __name__ == "__main__":
    main()
