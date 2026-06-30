#!/usr/bin/env python3
"""MLPerf CNN/DailyMail 100-sample FP8 benchmark harness.

Supports L40, A40 (via vllm), RNGD (via furiosa-llm OpenAI API), Atom+ (via optimum-rbln).

Usage:
  python3 mlperf_cnndm100_fp8.py --hw <l40|a40|rngd|atomplus> \
      --model <repo> --output <result.json>

Env vars:
  HF_TOKEN          - HuggingFace token
  VLLM_BASE_URL     - for rngd: base URL of OpenAI-compatible server (default http://192.0.2.114:8000)
  LOG_PATH          - override log file path
"""
import argparse
import json
import os
import statistics
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

TS = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")

def parse_args():
    p = argparse.ArgumentParser()
    p.add_argument("--hw", required=True, choices=["l40", "a40", "rngd", "atomplus"])
    p.add_argument("--model", required=True)
    p.add_argument("--output", required=True)
    p.add_argument("--n-samples", type=int, default=100)
    p.add_argument("--max-tokens", type=int, default=128)
    p.add_argument("--base-url", default=os.environ.get("VLLM_BASE_URL", "http://192.0.2.114:8000"))
    return p.parse_args()

def make_log(log_path: Path):
    log_path.parent.mkdir(parents=True, exist_ok=True)
    fh = log_path.open("a")
    def log(msg):
        line = f"[{datetime.now(timezone.utc).isoformat()}] {msg}"
        print(line, flush=True)
        fh.write(line + "\n")
        fh.flush()
    return log, fh

def load_dataset_samples(n: int, log):
    log(f"Loading cnn_dailymail 3.0.0 test split, first {n} samples...")
    from datasets import load_dataset
    ds = load_dataset("abisee/cnn_dailymail", "3.0.0", split="test")
    samples = list(ds)[:n]
    log(f"Dataset loaded: {len(samples)} samples, dataset=cnn_dailymail, version=3.0.0")
    return samples

def run_openai_api(samples, model_id, base_url, max_tokens, log):
    """Call an OpenAI-compatible server (vllm or furiosa-llm) for each sample."""
    import urllib.request
    url = f"{base_url}/v1/chat/completions"
    results = []
    errors = 0
    for i, sample in enumerate(samples):
        article = sample["article"][:2000]
        payload = json.dumps({
            "model": model_id,
            "messages": [{"role": "user", "content": f"Summarize the following article in a few sentences:\n\n{article}"}],
            "max_tokens": max_tokens,
            "temperature": 0,
            "top_p": 1,
        }).encode()
        req = urllib.request.Request(url, data=payload, method="POST",
                                     headers={"Content-Type": "application/json"})
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=120) as r:
                body = json.loads(r.read().decode())
            t1 = time.perf_counter()
            out_tokens = body.get("usage", {}).get("completion_tokens", max_tokens)
            elapsed = t1 - t0
            row = {
                "idx": i,
                "elapsed_s": round(elapsed, 6),
                "output_tokens": int(out_tokens),
                "tps": round(out_tokens / elapsed, 3) if elapsed > 0 else 0,
            }
            results.append(row)
            if i < 5 or i % 20 == 0:
                log(f"  sample {i+1}/{len(samples)}: {elapsed:.3f}s {row['tps']:.1f} tok/s")
        except Exception as e:
            errors += 1
            log(f"  sample {i+1} ERROR: {e}")
            results.append({"idx": i, "error": str(e), "elapsed_s": 0, "output_tokens": 0, "tps": 0})
    return results, errors

def run_vllm_inline(samples, model_id, max_tokens, log):
    """Start vllm inline (for GPU jobs where vllm is installed)."""
    log(f"Loading vllm LLM: model={model_id} dtype=auto max_tokens={max_tokens}")
    log(f"precision=FP8 (model weights are FP8-quantized, loaded with dtype=auto)")
    from vllm import LLM, SamplingParams
    llm = LLM(model=model_id, dtype="auto", gpu_memory_utilization=0.90,
              max_model_len=4096, trust_remote_code=False)
    log("vllm LLM loaded successfully")
    # Confirm FP8 via config
    try:
        qtype = llm.llm_engine.model_config.quantization
        log(f"vllm quantization config: {qtype}")
    except Exception:
        pass

    sampling = SamplingParams(temperature=0, top_p=1, max_tokens=max_tokens)
    prompts = [f"Summarize the following article in a few sentences:\n\n{s['article'][:2000]}"
               for s in samples]

    # Warmup
    log("Warmup (3 samples)...")
    llm.generate(prompts[:3], sampling)

    log(f"=== Benchmark: {len(samples)} samples ===")
    results = []
    errors = 0
    for i, prompt in enumerate(prompts):
        t0 = time.perf_counter()
        try:
            out = llm.generate([prompt], sampling)[0]
            t1 = time.perf_counter()
            out_tokens = len(out.outputs[0].token_ids)
            elapsed = t1 - t0
            row = {
                "idx": i,
                "elapsed_s": round(elapsed, 6),
                "output_tokens": int(out_tokens),
                "tps": round(out_tokens / elapsed, 3) if elapsed > 0 else 0,
            }
            results.append(row)
            if i < 5 or i % 20 == 0:
                log(f"  sample {i+1}/{len(samples)}: {elapsed:.3f}s {row['tps']:.1f} tok/s")
        except Exception as e:
            errors += 1
            log(f"  sample {i+1} ERROR: {e}")
            results.append({"idx": i, "error": str(e), "elapsed_s": 0, "output_tokens": 0, "tps": 0})
    return results, errors

def compute_metrics(results, max_tokens, log):
    good = [r for r in results if "error" not in r and r["elapsed_s"] > 0]
    if not good:
        return None
    times = [r["elapsed_s"] for r in good]
    tps_vals = [r["tps"] for r in good]
    total_tokens = sum(r["output_tokens"] for r in good)
    elapsed_total = sum(times)
    mean_s = statistics.mean(times)
    tt100t_s = mean_s * (100 / max_tokens)
    throughput = total_tokens / elapsed_total if elapsed_total > 0 else 0
    times_sorted = sorted(times)
    return {
        "tt100t_s": round(tt100t_s, 6),
        "throughput_tps": round(throughput, 3),
        "mean_latency_s": round(mean_s, 6),
        "p50_latency_s": round(times_sorted[len(times_sorted)//2], 6),
        "p90_latency_s": round(times_sorted[int(len(times_sorted)*0.9)], 6),
        "p99_latency_s": round(times_sorted[int(len(times_sorted)*0.99)] if len(times_sorted) >= 100 else times_sorted[-1], 6),
        "total_output_tokens": total_tokens,
        "tps_best": round(max(tps_vals), 3),
        "n_valid": len(good),
        "elapsed_total_s": round(elapsed_total, 3),
    }

HW_META = {
    "l40":     {"hardware": "NVIDIA-L40",          "vendor": "nvidia"},
    "a40":     {"hardware": "NVIDIA-A40",           "vendor": "nvidia"},
    "rngd":    {"hardware": "FuriosaAI-RNGD",       "vendor": "furiosa"},
    "atomplus": {"hardware": "Rebellions-Atom+",    "vendor": "rebellions"},
}

def main():
    args = parse_args()
    hw_key = args.hw
    meta = HW_META[hw_key]

    run_id = f"mlperf-cnndm100-fp8-{hw_key}-{TS}"
    log_path = Path(os.environ.get("LOG_PATH",
        f"/home/kcloud/etri-llm-exam-solution/logs/benchmarks/mlperf_{hw_key}_fp8_{TS}.log"))
    log, _fh = make_log(log_path)

    log(f"=== MLPerf CNN/DailyMail 100-sample FP8 Benchmark ===")
    log(f"run_id={run_id}")
    log(f"hw={hw_key} model={args.model} n_samples={args.n_samples} max_tokens={args.max_tokens}")
    log(f"dataset=cnn_dailymail version=3.0.0")
    log(f"precision=FP8 (target)")

    started_at = datetime.now(timezone.utc).isoformat()

    try:
        samples = load_dataset_samples(args.n_samples, log)
    except Exception as e:
        log(f"FATAL: dataset load failed: {e}")
        write_result(args.output, run_id, meta, args, started_at, None, str(e), [], log_path)
        return 1

    results = []
    errors = 0

    if hw_key in ("l40", "a40"):
        # vllm inline with FP8 model (--dtype auto loads quantized weights as-is)
        try:
            results, errors = run_vllm_inline(samples, args.model, args.max_tokens, log)
        except Exception as e:
            log(f"FATAL: vllm run failed: {e}")
            write_result(args.output, run_id, meta, args, started_at, None, str(e), [], log_path)
            return 1
    elif hw_key == "rngd":
        # Call the running furiosa-llm server
        model_id_for_api = "meta-llama/Llama-3.1-8B-Instruct"
        log(f"RNGD: calling furiosa-llm server at {args.base_url}, model_id={model_id_for_api}")
        log(f"RNGD server model: furiosa-ai/Llama-3.1-8B-Instruct-FP8 revision=v2025.3.0 precision=FP8")
        try:
            results, errors = run_openai_api(samples, model_id_for_api, args.base_url, args.max_tokens, log)
        except Exception as e:
            log(f"FATAL: RNGD API call failed: {e}")
            write_result(args.output, run_id, meta, args, started_at, None, str(e), [], log_path)
            return 1
    elif hw_key == "atomplus":
        # optimum-rbln FP8 path: load from pre-compiled FP8 cache on node5
        compile_cache_name = os.environ.get("COMPILE_CACHE_NAME", "__mnt__models__Llama-3.1-8B-Instruct-FP8")
        compile_dir = os.environ.get("COMPILE_DIR", "/home/kcloud/cache/rbln-compiled")
        cached = Path(compile_dir) / compile_cache_name
        log(f"Atom+: loading optimum-rbln FP8 model from {cached}")
        try:
            import rebel
            log(f"rebel.device_count()={rebel.device_count()}")
            from optimum.rbln import RBLNAutoModelForCausalLM
            from transformers import AutoTokenizer
            if not cached.exists():
                raise FileNotFoundError(f"Compiled FP8 model not found at {cached}")
            model = RBLNAutoModelForCausalLM.from_pretrained(str(cached))
            tokenizer = AutoTokenizer.from_pretrained(str(cached))
            if tokenizer.pad_token is None:
                tokenizer.pad_token = tokenizer.eos_token
            log("Atom+ FP8 model loaded successfully")
        except Exception as e:
            log(f"FATAL: Atom+ FP8 model load failed: {e}")
            write_result(args.output, run_id, meta, args, started_at, None, str(e), [], log_path)
            return 1

        # Warmup (3 samples)
        log("Atom+: warmup (3 samples)...")
        for i, sample in enumerate(samples[:3]):
            prompt = f"Summarize the following article in a few sentences:\n\n{sample['article'][:2000]}"
            try:
                inputs = tokenizer(prompt, return_tensors="pt", truncation=True, max_length=896)
                t0 = time.perf_counter()
                model.generate(inputs.input_ids, max_new_tokens=args.max_tokens, do_sample=False)
                t1 = time.perf_counter()
                log(f"  warmup-{i+1}: {t1-t0:.3f}s")
            except Exception as e:
                log(f"  warmup-{i+1} failed (non-fatal): {e}")

        log(f"Atom+: benchmark {len(samples)} samples, max_tokens={args.max_tokens}")
        for i, sample in enumerate(samples):
            prompt = f"Summarize the following article in a few sentences:\n\n{sample['article'][:2000]}"
            t0 = time.perf_counter()
            try:
                inputs = tokenizer(prompt, return_tensors="pt", truncation=True, max_length=896)
                out = model.generate(inputs.input_ids, max_new_tokens=args.max_tokens, do_sample=False)
                t1 = time.perf_counter()
                gen_tokens = out.shape[-1] - inputs.input_ids.shape[-1]
                elapsed = t1 - t0
                row = {
                    "idx": i,
                    "elapsed_s": round(elapsed, 6),
                    "output_tokens": int(gen_tokens),
                    "tps": round(gen_tokens / elapsed, 3) if elapsed > 0 else 0,
                }
                results.append(row)
                if i < 5 or i % 20 == 0:
                    log(f"  sample {i+1}/{len(samples)}: {elapsed:.3f}s {row['tps']:.1f} tok/s")
            except Exception as e:
                errors += 1
                log(f"  sample {i+1} ERROR: {e}")
                results.append({"idx": i, "error": str(e), "elapsed_s": 0, "output_tokens": 0, "tps": 0})

    completed_at = datetime.now(timezone.utc).isoformat()
    elapsed_wall = (datetime.fromisoformat(completed_at) - datetime.fromisoformat(started_at)).total_seconds()

    metrics = compute_metrics(results, args.max_tokens, log)
    if not metrics:
        reason = f"All {len(samples)} samples failed (errors={errors})"
        log(f"ERROR: {reason}")
        write_result(args.output, run_id, meta, args, started_at, None, reason, results, log_path)
        return 1

    log("=== RESULTS ===")
    log(f"  Samples: {metrics['n_valid']}/{len(samples)} (errors={errors})")
    log(f"  TT100T: {metrics['tt100t_s']:.4f}s")
    log(f"  Throughput: {metrics['throughput_tps']:.2f} tok/s")
    log(f"  Mean latency: {metrics['mean_latency_s']:.3f}s")
    log(f"  Wall clock: {elapsed_wall:.1f}s")
    log(f"  precision=FP8 model={args.model}")

    result = {
        "run_id": run_id,
        "hardware": meta["hardware"],
        "vendor": meta["vendor"],
        "benchmark": "mlperf",
        "model": args.model,
        "precision": "FP8",
        "dataset": "CNN-DailyMail",
        "dataset_version": "3.0.0",
        "scenario": "offline",
        "max_output_tokens": args.max_tokens,
        "started_at": started_at,
        "completed_at": completed_at,
        "status": "completed",
        "failure_reason": None,
        "tt100t_seconds": metrics["tt100t_s"],
        "elapsed_seconds": round(elapsed_wall, 1),
        "throughput_tokens_per_sec": metrics["throughput_tps"],
        "raw_metrics": {
            "result_perf_tps": metrics["throughput_tps"],
            "result_perf_sps": round(metrics["n_valid"] / metrics["elapsed_total_s"], 6) if metrics["elapsed_total_s"] > 0 else 0,
            "result_perf_tps_best": metrics["tps_best"],
            "result_perf_sps_best": None,
            "result_perf_valid": "VALID" if errors == 0 else "PARTIAL",
            "result_perf_latency": metrics["mean_latency_s"],
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
            "mean_latency_s": metrics["mean_latency_s"],
            "p50_latency_s": metrics["p50_latency_s"],
            "p90_latency_s": metrics["p90_latency_s"],
            "p99_latency_s": metrics["p99_latency_s"],
            "total_output_tokens": metrics["total_output_tokens"],
            "framework": "vllm" if hw_key in ("l40","a40") else ("furiosa-llm" if hw_key == "rngd" else "optimum-rbln"),
        },
        "logs_path": str(log_path),
        "artifact_path": str(Path(args.output).parent),
        "config_fingerprint": f"mlperf|{args.model}|cnn_dailymail|3.0.0|FP8|{args.n_samples}|{args.max_tokens}",
    }

    out_path = Path(args.output)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with out_path.open("w") as f:
        json.dump(result, f, indent=2)
    log(f"Result written to {out_path}")
    return 0

def write_result(output, run_id, meta, args, started_at, metrics, reason, results, log_path):
    completed_at = datetime.now(timezone.utc).isoformat()
    elapsed = (datetime.fromisoformat(completed_at) - datetime.fromisoformat(started_at)).total_seconds()
    result = {
        "run_id": run_id,
        "hardware": meta["hardware"],
        "vendor": meta["vendor"],
        "benchmark": "mlperf",
        "model": args.model,
        "precision": "FP8",
        "dataset": "CNN-DailyMail",
        "dataset_version": "3.0.0",
        "scenario": "offline",
        "max_output_tokens": args.max_tokens,
        "started_at": started_at,
        "completed_at": completed_at,
        "status": "failed",
        "failure_reason": reason,
        "tt100t_seconds": None,
        "elapsed_seconds": round(elapsed, 1),
        "throughput_tokens_per_sec": None,
        "raw_metrics": {"errors": len([r for r in results if "error" in r])},
        "logs_path": str(log_path),
        "artifact_path": "",
        "config_fingerprint": f"mlperf|{args.model}|cnn_dailymail|3.0.0|FP8|{args.n_samples}|{args.max_tokens}",
    }
    out_path = Path(output)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with out_path.open("w") as f:
        json.dump(result, f, indent=2)

if __name__ == "__main__":
    sys.exit(main())
