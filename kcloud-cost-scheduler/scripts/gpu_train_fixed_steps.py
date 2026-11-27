#!/usr/bin/env python3
"""Fixed-step GPU training benchmark (ResNet50 + synthetic data).

NOT the same as cnn_gpu_background.py (that is eval/no_grad inference).
This runs real train steps: forward + loss + backward + optimizer.
"""
from __future__ import annotations

import argparse
import csv
import json
import statistics
import subprocess
import sys
import threading
import time
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


@dataclass
class TrainResult:
    experiment_id: str
    model: str
    data: str
    batch: int
    precision: str
    amp: bool
    optimizer: str
    lr: float
    steps: int
    image_size: int
    num_classes: int
    wall_s: float
    step_ms_mean: float
    step_ms_p50: float
    step_ms_p99: float
    images_per_s: float
    samples_processed: int
    peak_allocated_mb: float
    peak_reserved_mb: float
    energy_j_gross: float
    energy_j_net: float
    idle_w: float
    avg_power_w: float
    peak_power_w: float
    gpu_util_avg_pct: float
    gpu_util_peak_pct: float
    abort_reason: Optional[str]
    started_at: str
    ended_at: str


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def gpu_power_w() -> Optional[float]:
    try:
        out = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=power.draw", "--format=csv,noheader,nounits"],
            text=True,
            timeout=3,
        )
        return float(out.strip().split("\n")[0])
    except Exception:
        return None


def gpu_util_pct() -> Optional[float]:
    try:
        out = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits"],
            text=True,
            timeout=3,
        )
        return float(out.strip().split("\n")[0])
    except Exception:
        return None


class PowerSampler:
    def __init__(self, interval_s: float = 0.5):
        self.interval_s = interval_s
        self.samples: list[tuple[float, float]] = []
        self.utils: list[tuple[float, float]] = []
        self._stop = threading.Event()
        self._th: Optional[threading.Thread] = None

    def start(self) -> None:
        self._stop.clear()
        self.samples = []
        self.utils = []

        def loop() -> None:
            while not self._stop.is_set():
                ts = time.time()
                w = gpu_power_w()
                u = gpu_util_pct()
                if w is not None:
                    self.samples.append((ts, w))
                if u is not None:
                    self.utils.append((ts, u))
                time.sleep(self.interval_s)

        self._th = threading.Thread(target=loop, daemon=True)
        self._th.start()

    def stop(self) -> None:
        self._stop.set()
        if self._th:
            self._th.join(timeout=3)

    def energy_j(self, idle_w: float = 0.0) -> tuple[float, float]:
        if len(self.samples) < 2:
            return 0.0, 0.0
        gross = net = 0.0
        for i in range(1, len(self.samples)):
            dt = self.samples[i][0] - self.samples[i - 1][0]
            w = self.samples[i][1]
            gross += w * dt
            net += max(w - idle_w, 0.0) * dt
        return round(gross, 2), round(net, 2)


def load_idle(path: Path) -> float:
    if not path.exists():
        return 0.0
    vals = []
    with path.open(newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if row.get("source") == "gpu":
                vals.append(float(row["watts"]))
    return sum(vals) / len(vals) if vals else 0.0


def percentile(vals: list[float], q: float) -> float:
    if not vals:
        return 0.0
    s = sorted(vals)
    if len(s) == 1:
        return s[0]
    idx = min(len(s) - 1, max(0, int(round(q * (len(s) - 1)))))
    return s[idx]


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--out-dir", required=True)
    p.add_argument("--experiment-id", default="gpu-train-fixed-steps")
    p.add_argument("--batch", type=int, default=32)
    p.add_argument("--steps", type=int, default=500)
    p.add_argument("--precision", choices=["fp16", "fp32"], default="fp16")
    p.add_argument("--lr", type=float, default=0.01)
    p.add_argument("--image-size", type=int, default=224)
    p.add_argument("--num-classes", type=int, default=1000)
    p.add_argument("--idle-csv", default="")
    p.add_argument("--oom-abort", action="store_true", default=True)
    args = p.parse_args()

    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=True)

    cfg = {
        "experiment_id": args.experiment_id,
        "model": "torchvision.models.resnet50(weights=None)",
        "data": "synthetic_uniform_randn_nchw",
        "batch": args.batch,
        "steps": args.steps,
        "precision": args.precision,
        "amp": args.precision == "fp16",
        "optimizer": "SGD",
        "lr": args.lr,
        "momentum": 0.9,
        "weight_decay": 1e-4,
        "loss": "CrossEntropyLoss",
        "image_size": args.image_size,
        "num_classes": args.num_classes,
        "note": "Fixed-step training benchmark; not ImageNet download. Distinct from cnn_gpu_background inference load.",
    }
    (out / "train_config.json").write_text(json.dumps(cfg, indent=2), encoding="utf-8")

    idle_w = load_idle(Path(args.idle_csv)) if args.idle_csv else 0.0
    if args.idle_csv and Path(args.idle_csv).resolve() != (out / "idle-power.csv").resolve():
        if Path(args.idle_csv).exists():
            (out / "idle-power.csv").write_bytes(Path(args.idle_csv).read_bytes())

    import torch
    import torch.nn as nn
    import torchvision.models as models

    if not torch.cuda.is_available():
        raise SystemExit("CUDA required")

    device = torch.device("cuda:0")
    torch.cuda.set_device(device)
    torch.cuda.empty_cache()
    torch.cuda.reset_peak_memory_stats(0)

    model = models.resnet50(weights=None, num_classes=args.num_classes).to(device)
    model.train()
    opt = torch.optim.SGD(model.parameters(), lr=args.lr, momentum=0.9, weight_decay=1e-4)
    loss_fn = nn.CrossEntropyLoss()
    use_amp = args.precision == "fp16"
    scaler = torch.cuda.amp.GradScaler(enabled=use_amp)

    # warmup 5 steps (not scored in step stats, but included in wall/energy window?
    # Protocol: warmup outside scored window for step times; energy covers scored steps only.)
    x = torch.randn(args.batch, 3, args.image_size, args.image_size, device=device)
    y = torch.randint(0, args.num_classes, (args.batch,), device=device)
    for _ in range(5):
        opt.zero_grad(set_to_none=True)
        with torch.cuda.amp.autocast(enabled=use_amp):
            loss = loss_fn(model(x), y)
        scaler.scale(loss).backward()
        scaler.step(opt)
        scaler.update()
    torch.cuda.synchronize()

    sampler = PowerSampler(0.5)
    sampler.start()
    started_at = now_iso()
    t_wall0 = time.perf_counter()
    step_ms: list[float] = []
    abort_reason = None
    completed = 0

    try:
        for step in range(args.steps):
            # fresh synthetic batch each step (data movement included)
            x = torch.randn(args.batch, 3, args.image_size, args.image_size, device=device)
            y = torch.randint(0, args.num_classes, (args.batch,), device=device)
            t0 = time.perf_counter()
            opt.zero_grad(set_to_none=True)
            with torch.cuda.amp.autocast(enabled=use_amp):
                logits = model(x)
                loss = loss_fn(logits, y)
            scaler.scale(loss).backward()
            scaler.step(opt)
            scaler.update()
            torch.cuda.synchronize()
            step_ms.append((time.perf_counter() - t0) * 1000.0)
            completed = step + 1
            if (step + 1) % 50 == 0:
                print(
                    f"step={step+1}/{args.steps} loss={float(loss):.4f} "
                    f"step_ms={step_ms[-1]:.2f} alloc_mb={torch.cuda.memory_allocated()/1e6:.0f}",
                    flush=True,
                )
    except torch.cuda.OutOfMemoryError as e:
        abort_reason = f"cuda_oom:{e}"
        print(abort_reason, flush=True)
    except Exception as e:
        abort_reason = f"error:{type(e).__name__}:{e}"
        print(abort_reason, flush=True)

    wall_s = time.perf_counter() - t_wall0
    sampler.stop()
    ended_at = now_iso()
    gross, net = sampler.energy_j(idle_w)
    watts = [w for _, w in sampler.samples]
    utils = [u for _, u in sampler.utils]

    # persist power
    with (out / "power.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["timestamp_unix", "watts", "source"])
        for ts, val in sampler.samples:
            w.writerow([ts, val, "gpu"])
    with (out / "util.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["timestamp_unix", "metric", "value"])
        for ts, val in sampler.utils:
            w.writerow([ts, "gpu_util", val])
    with (out / "step_times_ms.csv").open("w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["step", "step_ms"])
        for i, ms in enumerate(step_ms, 1):
            w.writerow([i, round(ms, 3)])

    samples = completed * args.batch
    result = TrainResult(
        experiment_id=args.experiment_id,
        model="resnet50",
        data="synthetic_randn",
        batch=args.batch,
        precision=args.precision,
        amp=use_amp,
        optimizer="SGD",
        lr=args.lr,
        steps=args.steps,
        image_size=args.image_size,
        num_classes=args.num_classes,
        wall_s=round(wall_s, 3),
        step_ms_mean=round(statistics.mean(step_ms), 3) if step_ms else 0.0,
        step_ms_p50=round(percentile(step_ms, 0.50), 3) if step_ms else 0.0,
        step_ms_p99=round(percentile(step_ms, 0.99), 3) if step_ms else 0.0,
        images_per_s=round(samples / wall_s, 3) if wall_s > 0 else 0.0,
        samples_processed=samples,
        peak_allocated_mb=round(torch.cuda.max_memory_allocated(0) / (1024 * 1024), 2),
        peak_reserved_mb=round(torch.cuda.max_memory_reserved(0) / (1024 * 1024), 2),
        energy_j_gross=gross,
        energy_j_net=net,
        idle_w=round(idle_w, 3),
        avg_power_w=round(statistics.mean(watts), 2) if watts else 0.0,
        peak_power_w=round(max(watts), 2) if watts else 0.0,
        gpu_util_avg_pct=round(statistics.mean(utils), 2) if utils else 0.0,
        gpu_util_peak_pct=round(max(utils), 2) if utils else 0.0,
        abort_reason=abort_reason,
        started_at=started_at,
        ended_at=ended_at,
    )
    payload = asdict(result)
    payload["steps_completed"] = completed
    payload["steps_requested"] = args.steps
    (out / "train_result.json").write_text(json.dumps(payload, indent=2), encoding="utf-8")
    print(json.dumps(payload, indent=2), flush=True)
    return 2 if abort_reason else 0


if __name__ == "__main__":
    raise SystemExit(main())
