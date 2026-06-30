#!/usr/bin/env python3
"""MLPerf full-dataset benchmark for Rebellions Atom+ NPU — FP8 operator path.

Runs CNN-DailyMail validation split (13368 samples) against a pre-compiled
FP8 optimum-rbln model cache (rebellions/Llama-3.1-8B-Instruct).  This is
the live operator path that produces DB rows with model=rebellions/Llama-3.1-8B-Instruct,
precision=fp8, framework=optimum-rbln, npu_type=ATOM, TT100T 1.25-1.29 s,
TPS 79-80.  Outputs result.json compatible with the import-benchmark-result.ts
schema.

Usage on node5:
  python3 atomplus_mlperf_full.py

Env vars:
  RUN_ID            - unique run identifier (auto-generated if not set)
  COMPILE_DIR       - path to pre-compiled RBLN model cache directory
  COMPILE_CACHE_NAME - sub-directory name inside COMPILE_DIR (default: __mnt__models__Llama-3.1-8B-Instruct-FP8)
  MODEL_ID          - model identifier recorded in result.json (default: rebellions/Llama-3.1-8B-Instruct)
  MAX_OUTPUT_TOKENS - max tokens to generate per sample (default 128)
  DATA_NUMBER       - number of validation samples to use (default 13368 = full)
  NPU_EXAM_ID       - if set, update this npu-eval exam via API when done
  BACKEND_URL       - npu-eval API base URL
  OUTPUT_DIR        - directory for result.json and raw JSONL artifact
  LOG_PATH          - path for benchmark log file
"""
import json
import os
import statistics
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

RUN_ID = os.environ.get("RUN_ID", "atomplus-mlperf-" + datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S"))
MODEL_ID = os.environ.get("MODEL_ID", "rebellions/Llama-3.1-8B-Instruct")
COMPILE_CACHE_NAME = os.environ.get("COMPILE_CACHE_NAME", "__mnt__models__Llama-3.1-8B-Instruct-FP8")
COMPILE_DIR = Path(os.environ.get("COMPILE_DIR", "/home/kcloud/cache/rbln-compiled"))
MAX_OUTPUT_TOKENS = int(os.environ.get("MAX_OUTPUT_TOKENS", "128"))
DATA_NUMBER = int(os.environ.get("DATA_NUMBER", "13368"))
OUTPUT_DIR = Path(os.environ.get("OUTPUT_DIR", f"/home/kcloud/results/{RUN_ID}"))
NPU_EXAM_ID = os.environ.get("NPU_EXAM_ID", "")
BACKEND_URL = os.environ.get("BACKEND_URL", "http://192.0.2.41:30980/api/npu-eval")
LOG_PATH = Path(os.environ.get("LOG_PATH", f"/home/kcloud/etri-llm-exam-solution/logs/benchmarks/mlperf_atomplus_{RUN_ID}.log"))

OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
LOG_PATH.parent.mkdir(parents=True, exist_ok=True)

_log_fh = LOG_PATH.open("a")

def log(msg):
    line = f"[{datetime.now(timezone.utc).isoformat()}] {msg}"
    print(line, flush=True)
    _log_fh.write(line + "\n")
    _log_fh.flush()

def patch_exam_api(exam_id, status, error_log=""):
    import urllib.request
    url = f"{BACKEND_URL}/update/{exam_id}"
    payload = json.dumps({"status": status, "error_log": error_log}).encode()
    req = urllib.request.Request(url, data=payload, method="PATCH",
                                  headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            log(f"API PATCH {url} -> {r.status}")
    except Exception as e:
        log(f"API PATCH failed (non-fatal): {e}")

def post_result_api(exam_id, result_payload):
    import urllib.request
    url = f"{BACKEND_URL}/results/create"
    payload = json.dumps(result_payload).encode()
    req = urllib.request.Request(url, data=payload, method="POST",
                                  headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            body = r.read().decode()
            log(f"API POST results -> {r.status}: {body[:200]}")
    except Exception as e:
        log(f"API POST results failed (non-fatal): {e}")

def main():
    started_at = datetime.now(timezone.utc).isoformat()
    log(f"=== Atom+ MLPerf Full Dataset Benchmark ===")
    log(f"RUN_ID={RUN_ID}")
    log(f"MODEL_ID={MODEL_ID}")
    log(f"MAX_OUTPUT_TOKENS={MAX_OUTPUT_TOKENS}")
    log(f"DATA_NUMBER={DATA_NUMBER}")
    log(f"OUTPUT_DIR={OUTPUT_DIR}")
    log(f"LOG_PATH={LOG_PATH}")

    # Mark exam as Running
    if NPU_EXAM_ID:
        patch_exam_api(NPU_EXAM_ID, "Running")

    # Load dataset (validation split = 13368 samples)
    log("Loading CNN-DailyMail validation split...")
    try:
        from datasets import load_dataset
        ds = load_dataset("abisee/cnn_dailymail", "3.0.0", split="validation")
        samples = list(ds)
        if DATA_NUMBER > 0 and DATA_NUMBER < len(samples):
            samples = samples[:DATA_NUMBER]
        log(f"Dataset loaded: {len(samples)} samples")
    except Exception as e:
        err = f"Dataset load failed: {e}"
        log(f"ERROR: {err}")
        if NPU_EXAM_ID:
            patch_exam_api(NPU_EXAM_ID, "Failed", err)
        write_failed_result(started_at, err)
        return 1

    # Load pre-compiled RBLN model
    cached = COMPILE_DIR / COMPILE_CACHE_NAME
    log(f"Loading RBLN model from cache: {cached}")
    try:
        import rebel
        log(f"rebel.device_count()={rebel.device_count()}")
        from optimum.rbln import RBLNAutoModelForCausalLM
        from transformers import AutoTokenizer

        if not cached.exists():
            raise FileNotFoundError(f"Compiled model not found at {cached}")

        model = RBLNAutoModelForCausalLM.from_pretrained(str(cached))
        tokenizer = AutoTokenizer.from_pretrained(str(cached))
        if tokenizer.pad_token is None:
            tokenizer.pad_token = tokenizer.eos_token
        log("Model loaded successfully")
    except Exception as e:
        err = f"Model load failed: {e}"
        log(f"ERROR: {err}")
        if NPU_EXAM_ID:
            patch_exam_api(NPU_EXAM_ID, "Failed", err)
        write_failed_result(started_at, err)
        return 1

    # Warmup (3 samples)
    log("=== Warmup (3 samples) ===")
    for i, sample in enumerate(samples[:3]):
        prompt = f"Summarize the following article:\n\n{sample['article'][:1500]}"
        try:
            inputs = tokenizer(prompt, return_tensors="pt", truncation=True, max_length=896)
            t0 = time.perf_counter()
            model.generate(inputs.input_ids, max_new_tokens=MAX_OUTPUT_TOKENS, do_sample=False)
            t1 = time.perf_counter()
            log(f"  warmup-{i+1}: {t1-t0:.3f}s")
        except Exception as e:
            log(f"  warmup-{i+1} failed: {e}")

    # Full benchmark
    log(f"=== Benchmark: {len(samples)} samples ===")
    results_raw = []
    raw_path = OUTPUT_DIR / "mlperf_atomplus_raw.jsonl"
    errors = 0

    with raw_path.open("w") as rawf:
        for i, sample in enumerate(samples):
            prompt = f"Summarize the following article:\n\n{sample['article'][:1500]}"
            try:
                inputs = tokenizer(prompt, return_tensors="pt", truncation=True, max_length=896)
                t0 = time.perf_counter()
                out = model.generate(inputs.input_ids, max_new_tokens=MAX_OUTPUT_TOKENS, do_sample=False)
                t1 = time.perf_counter()
                gen_tokens = out.shape[-1] - inputs.input_ids.shape[-1]
                row = {
                    "idx": i,
                    "elapsed_s": round(t1 - t0, 6),
                    "output_tokens": int(gen_tokens),
                    "tps": round(gen_tokens / (t1 - t0), 3) if (t1 - t0) > 0 else 0,
                }
                results_raw.append(row)
                rawf.write(json.dumps(row) + "\n")
                rawf.flush()
                if i % 500 == 0 or i < 5:
                    log(f"  sample {i+1}/{len(samples)}: {row['elapsed_s']:.3f}s {row['tps']:.1f} tok/s")
            except Exception as e:
                errors += 1
                log(f"  sample {i+1} error: {e}")
                rawf.write(json.dumps({"idx": i, "error": str(e)}) + "\n")

    completed_at = datetime.now(timezone.utc).isoformat()

    if not results_raw:
        err = f"All {len(samples)} samples failed"
        log(f"ERROR: {err}")
        if NPU_EXAM_ID:
            patch_exam_api(NPU_EXAM_ID, "Failed", err)
        write_failed_result(started_at, err)
        return 1

    # Compute metrics
    times = [r["elapsed_s"] for r in results_raw]
    tps_vals = [r["tps"] for r in results_raw]
    total_tokens = sum(r["output_tokens"] for r in results_raw)
    elapsed_total = sum(times)

    # TT100T: time to generate 100 tokens for a single request (mean per-sample time * 100/128)
    mean_per_sample_s = statistics.mean(times)
    tt100t_s = mean_per_sample_s * (100 / MAX_OUTPUT_TOKENS)

    throughput_tps = total_tokens / elapsed_total if elapsed_total > 0 else 0
    elapsed_wall = (datetime.fromisoformat(completed_at) - datetime.fromisoformat(started_at)).total_seconds()

    summary = {
        "run_id": RUN_ID,
        "hardware": "Rebellions-Atom+",
        "vendor": "rebellions",
        "benchmark": "mlperf",
        "model": MODEL_ID,
        "precision": "fp8",
        "started_at": started_at,
        "completed_at": completed_at,
        "status": "completed",
        "failure_reason": None,
        "tt100t_seconds": round(tt100t_s, 6),
        "elapsed_seconds": round(elapsed_wall, 1),
        "throughput_tokens_per_sec": round(throughput_tps, 3),
        "raw_metrics": {
            "result_perf_tps": round(throughput_tps, 3),
            "result_perf_sps": round(len(results_raw) / elapsed_total, 6) if elapsed_total > 0 else 0,
            "result_perf_tps_best": round(max(tps_vals), 3) if tps_vals else None,
            "result_perf_sps_best": None,
            "result_perf_valid": "VALID" if errors == 0 else "PARTIAL",
            "result_perf_latency": None,
            "result_perf_serv_ttft": None,
            "result_perf_serv_tpot": None,
            "result_acc_rg_1": None,
            "result_acc_rg_2": None,
            "result_acc_rg_l": None,
            "result_acc_rg_lsum": None,
            "result_acc_total": None,
            "result_vram_peak": None,
            "result_gpu_util": None,
            "data_number": len(samples),
            "errors": errors,
            "mean_latency_s": round(mean_per_sample_s, 6),
            "p50_latency_s": round(sorted(times)[len(times)//2], 6),
            "p90_latency_s": round(sorted(times)[int(len(times)*0.9)], 6),
            "p99_latency_s": round(sorted(times)[int(len(times)*0.99)], 6),
            "total_output_tokens": total_tokens,
            "npu_model": "RBLN-CA22",
            "framework": "optimum-rbln",
            "rbln_version": "0.9.3.post1",
        },
        "logs_path": str(LOG_PATH),
        "artifact_path": str(OUTPUT_DIR / "mlperf_atomplus_raw.jsonl"),
        "config_fingerprint": "unfingerprinted",
    }

    result_path = OUTPUT_DIR / "result.json"
    with result_path.open("w") as f:
        json.dump(summary, f, indent=2)

    log("=== RESULTS ===")
    log(f"  Samples: {len(results_raw)}/{len(samples)} (errors: {errors})")
    log(f"  Throughput: {throughput_tps:.2f} tok/s")
    log(f"  TT100T: {tt100t_s:.4f}s")
    log(f"  Mean latency: {mean_per_sample_s:.3f}s")
    log(f"  result.json: {result_path}")

    # Post result to npu-eval API
    if NPU_EXAM_ID:
        result_payload = {
            "exam_id": int(NPU_EXAM_ID),
            "result_number": 1,
            "result_perf_tps": round(throughput_tps, 3),
            "result_perf_sps": round(len(results_raw) / elapsed_total, 6) if elapsed_total > 0 else 0,
            "result_perf_tps_best": round(max(tps_vals), 3) if tps_vals else None,
            "result_perf_valid": "VALID" if errors == 0 else "PARTIAL",
            "result_tt100t": round(tt100t_s, 6),
        }
        post_result_api(NPU_EXAM_ID, result_payload)
        patch_exam_api(NPU_EXAM_ID, "Completed")

    return 0


def write_failed_result(started_at, reason):
    completed_at = datetime.now(timezone.utc).isoformat()
    elapsed = (datetime.fromisoformat(completed_at) - datetime.fromisoformat(started_at)).total_seconds()
    summary = {
        "run_id": RUN_ID,
        "hardware": "Rebellions-Atom+",
        "vendor": "rebellions",
        "benchmark": "mlperf",
        "model": MODEL_ID,
        "precision": "fp8",
        "started_at": started_at,
        "completed_at": completed_at,
        "status": "failed",
        "failure_reason": reason,
        "tt100t_seconds": None,
        "elapsed_seconds": round(elapsed, 1),
        "throughput_tokens_per_sec": None,
        "raw_metrics": {},
        "logs_path": str(LOG_PATH),
        "artifact_path": "",
        "config_fingerprint": "unfingerprinted",
    }
    result_path = OUTPUT_DIR / "result.json"
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    with result_path.open("w") as f:
        json.dump(summary, f, indent=2)
    log(f"Failed result written to {result_path}")


if __name__ == "__main__":
    sys.exit(main())
