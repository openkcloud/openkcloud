#!/usr/bin/env python3
"""L40S LSU-reference CNN throughput (not the fixed-image CNN workload).

Matches KETRIS lsu/benchmark.py contract:
  ResNet50 weights=None, FP16, 224x224, CUDA Event GPU timing.
Provisional LSU = throughput_img_s / 42.16 (ConfigMap unit).

Does not install LSU / change MPS / join Kubernetes.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path

import torch
from torchvision.models import resnet50

# Locked against live ConfigMap unit (LSU_SOURCE_AUDIT RESULT.md)
UNIT_IMG_PER_S_PROVISIONAL = 42.16
REFERENCE_WORKLOAD = "resnet50_inference_fp16"


def _driver_version() -> str:
    try:
        out = subprocess.check_output(
            [
                "nvidia-smi",
                "--query-gpu=driver_version",
                "--format=csv,noheader",
            ],
            text=True,
            timeout=10,
        )
        return out.strip().splitlines()[0].strip()
    except Exception as e:  # noqa: BLE001
        return f"unavailable:{e}"


def _docker_image_digest(image: str) -> dict:
    try:
        fmt = "{{.Id}} {{json .RepoDigests}}"
        out = subprocess.check_output(
            ["docker", "image", "inspect", image, "--format", fmt],
            text=True,
            timeout=30,
        ).strip()
        parts = out.split(" ", 1)
        return {
            "image": image,
            "id": parts[0] if parts else None,
            "repo_digests_raw": parts[1] if len(parts) > 1 else None,
        }
    except Exception as e:  # noqa: BLE001
        return {"image": image, "error": str(e)}


def measure_once(
    gpu_id: int,
    batch: int,
    iters: int,
    warmup: int,
    dtype: torch.dtype,
) -> dict:
    torch.cuda.set_device(gpu_id)
    device = torch.device(f"cuda:{gpu_id}")
    name = torch.cuda.get_device_name(gpu_id)
    torch.backends.cudnn.benchmark = True

    # LSU benchmark: random init, no pretrained weights
    model = resnet50(weights=None).to(device=device, dtype=dtype).eval()
    x = torch.randn(batch, 3, 224, 224, device=device, dtype=dtype)

    with torch.inference_mode():
        for _ in range(warmup):
            model(x)
        torch.cuda.synchronize(device)

        start = torch.cuda.Event(enable_timing=True)
        end = torch.cuda.Event(enable_timing=True)
        start.record()
        for _ in range(iters):
            model(x)
        end.record()
        torch.cuda.synchronize(device)

    elapsed_ms = float(start.elapsed_time(end))
    total_imgs = batch * iters
    throughput = total_imgs / (elapsed_ms / 1000.0)
    latency_ms = elapsed_ms / iters

    return {
        "gpu_id": gpu_id,
        "device": name,
        "throughput_img_s": round(throughput, 3),
        "latency_ms_per_batch": round(latency_ms, 4),
        "elapsed_ms_cuda_event": round(elapsed_ms, 3),
        "total_images": total_imgs,
        "batch": batch,
        "iters": iters,
        "warmup": warmup,
        "dtype": str(dtype).replace("torch.", ""),
        "weights": None,
        "input_shape": [batch, 3, 224, 224],
        "timing": "cuda_event",
        "cudnn_benchmark": True,
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out-dir", required=True)
    ap.add_argument("--batch", type=int, default=32)
    ap.add_argument("--iters", type=int, default=200)
    ap.add_argument("--warmup", type=int, default=50)
    ap.add_argument("--repeats", type=int, default=3)
    ap.add_argument("--gpu", type=int, default=0)
    ap.add_argument(
        "--unit-img-per-s",
        type=float,
        default=UNIT_IMG_PER_S_PROVISIONAL,
        help="provisional 1 LSU = this img/s (ConfigMap 42.16)",
    )
    ap.add_argument(
        "--pytorch-image",
        default="pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime",
        help="record digest of image used for the fixed-image CNN workload; host torch may differ",
    )
    args = ap.parse_args()

    if not torch.cuda.is_available():
        raise SystemExit("CUDA unavailable")

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    dtype = torch.float16

    env = {
        "host": platform.node(),
        "checked_utc": datetime.now(timezone.utc).isoformat(),
        "checked_kst": datetime.now().astimezone().isoformat(),
        "torch": torch.__version__,
        "torch_cuda": torch.version.cuda,
        "cudnn": getattr(torch.backends.cudnn, "version", lambda: None)(),
        "gpu_name": torch.cuda.get_device_name(args.gpu),
        "driver_version": _driver_version(),
        "nvidia_smi_query": subprocess.check_output(
            [
                "nvidia-smi",
                "--query-gpu=name,memory.total,driver_version",
                "--format=csv",
            ],
            text=True,
        ).strip(),
        "pytorch_docker_image": _docker_image_digest(args.pytorch_image),
        "run_mode": "host_python",
        "cwd": os.getcwd(),
    }
    (out_dir / "environment.json").write_text(
        json.dumps(env, indent=2) + "\n", encoding="utf-8"
    )

    condition_check = {
        "reference_workload": REFERENCE_WORKLOAD,
        "matches_lsu_benchmark_source": {
            "model": "torchvision resnet50",
            "weights": None,
            "dtype": "float16",
            "input": "randn(batch,3,224,224)",
            "timing": "torch.cuda.Event",
            "cudnn_benchmark": True,
        },
        "differs_from_cnn_gpu_fixed_images": {
            "fixed_images_weights": "ResNet50_Weights.DEFAULT (pretrained)",
            "fixed_images_batch_default": 8,
            "fixed_images_termination": "fixed image count M (wall/perf_counter style loop)",
            "fixed_images_purpose": "work-quantum energy, not LSU unit calibration",
            "lsu_purpose": "img/s → provisional LSU via unit 42.16",
        },
        "config_locked": {
            "batch": args.batch,
            "warmup": args.warmup,
            "iters": args.iters,
            "repeats": args.repeats,
            "unit_img_per_s": args.unit_img_per_s,
        },
        "condition_match_verdict": (
            "LSU_benchmark_contract_ok_not_fixed_images_cnn"
        ),
    }
    (out_dir / "condition_check.json").write_text(
        json.dumps(condition_check, indent=2) + "\n", encoding="utf-8"
    )

    rows = []
    for r in range(1, args.repeats + 1):
        print(f"[lsu-cnn] repeat {r}/{args.repeats} ...", flush=True)
        m = measure_once(args.gpu, args.batch, args.iters, args.warmup, dtype)
        m["repeat"] = r
        m["provisional_lsu"] = round(m["throughput_img_s"] / args.unit_img_per_s, 3)
        m["provisional_lsu_int"] = max(
            1, round(m["throughput_img_s"] / args.unit_img_per_s)
        )
        rows.append(m)
        (out_dir / f"rep{r:02d}.json").write_text(
            json.dumps(m, indent=2) + "\n", encoding="utf-8"
        )
        print(
            f"[lsu-cnn] r{r} {m['throughput_img_s']} img/s "
            f"→ LSU≈{m['provisional_lsu']} (÷{args.unit_img_per_s})",
            flush=True,
        )
        time.sleep(2)

    thr = [r["throughput_img_s"] for r in rows]
    thr_sorted = sorted(thr)
    mid = thr_sorted[len(thr_sorted) // 2]
    summary = {
        "campaign": "L40S_LSU_CNN_BENCH",
        "reference_workload": REFERENCE_WORKLOAD,
        "unit_img_per_s": args.unit_img_per_s,
        "unit_source": "ConfigMap resnet50_inference_fp16 unit 42.16 (provisional)",
        "config": {
            "batch": args.batch,
            "warmup": args.warmup,
            "iters": args.iters,
            "repeats": args.repeats,
            "weights": None,
            "dtype": "float16",
            "input": "224x224",
            "timing": "cuda_event",
        },
        "throughput_img_s": thr,
        "throughput_mean": round(sum(thr) / len(thr), 3),
        "throughput_median": mid,
        "provisional_lsu_per_rep": [r["provisional_lsu"] for r in rows],
        "provisional_lsu_from_median": round(mid / args.unit_img_per_s, 3),
        "provisional_lsu_int_from_median": max(1, round(mid / args.unit_img_per_s)),
        "measurements": rows,
        "environment_relpath": "environment.json",
        "condition_check_relpath": "condition_check.json",
        "scope_note": (
            "throughput + provisional LSU only; "
            "no LSU install / MPS change / K8s join"
        ),
        "saved_utc": datetime.now(timezone.utc).isoformat(),
    }
    (out_dir / "aggregate.json").write_text(
        json.dumps(summary, indent=2) + "\n", encoding="utf-8"
    )
    print(json.dumps({k: summary[k] for k in (
        "throughput_img_s",
        "throughput_median",
        "provisional_lsu_from_median",
        "unit_img_per_s",
    )}, indent=2))


if __name__ == "__main__":
    main()
