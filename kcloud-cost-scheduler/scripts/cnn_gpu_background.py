#!/usr/bin/env python3
"""Long-running ResNet50 GPU background load for mixed CNN+LLM experiments."""

from __future__ import annotations

import argparse
import json
import signal
import time
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--batch", type=int, default=8)
    p.add_argument(
        "--sleep-ms",
        type=float,
        default=0.0,
        help="pause after each forward pass (reduce GPU pressure / target util)",
    )
    p.add_argument("--precision", choices=["fp16", "fp32"], default="fp16")
    p.add_argument("--status-file", default="/tmp/cnn-gpu-bg-status.json")
    p.add_argument("--stop-file", default="/tmp/cnn-gpu-bg.stop")
    args = p.parse_args()

    import torch
    import torchvision.models as models

    stop = {"flag": False}

    def _stop(*_):
        stop["flag"] = True

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)
    Path(args.stop_file).unlink(missing_ok=True)

    device = torch.device("cuda")
    dtype = torch.float16 if args.precision == "fp16" else torch.float32
    model = models.resnet50(weights=models.ResNet50_Weights.DEFAULT)
    model = model.to(device=device, dtype=dtype).eval()
    dummy = torch.randn(args.batch, 3, 224, 224, device=device, dtype=dtype)

    with torch.no_grad():
        for _ in range(10):
            model(dummy)
        torch.cuda.synchronize()

    started = time.time()
    images = 0
    forwards = 0
    latencies: list[float] = []

    def write_status(running: bool):
        elapsed = max(time.time() - started, 1e-6)
        payload = {
            "running": running,
            "batch": args.batch,
            "sleep_ms": args.sleep_ms,
            "precision": args.precision,
            "started_at": started,
            "elapsed_s": round(elapsed, 2),
            "images": images,
            "forwards": forwards,
            "throughput_ips": round(images / elapsed, 2),
            "latency_ms_avg": round(sum(latencies[-100:]) / max(len(latencies[-100:]), 1), 3),
            "vram_mb": float(torch.cuda.memory_allocated(device) // (1024 * 1024)),
        }
        Path(args.status_file).write_text(json.dumps(payload), encoding="utf-8")

    write_status(True)
    with torch.no_grad():
        while not stop["flag"] and not Path(args.stop_file).exists():
            t0 = time.perf_counter()
            model(dummy)
            torch.cuda.synchronize()
            latencies.append((time.perf_counter() - t0) * 1000)
            images += args.batch
            forwards += 1
            if args.sleep_ms > 0:
                time.sleep(args.sleep_ms / 1000.0)
            if images % (args.batch * 50) == 0:
                write_status(True)

    write_status(False)
    Path(args.stop_file).unlink(missing_ok=True)


if __name__ == "__main__":
    main()
