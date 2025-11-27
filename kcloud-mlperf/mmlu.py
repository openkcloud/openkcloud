import argparse
import json
import os
import torch
from datetime import datetime, timezone
try:
    from zoneinfo import ZoneInfo  # py3.9+
except Exception:  # pragma: no cover
    ZoneInfo = None  # type: ignore
from collections import Counter, defaultdict
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Tuple


def _lazy_imports():
    global datasets, vllm, SamplingParams, AutoTokenizer, plt
    from datasets import load_dataset  # type: ignore
    from vllm import LLM, SamplingParams  # type: ignore
    from transformers import AutoTokenizer  # type: ignore
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt  # type: ignore
    globals()["datasets"] = load_dataset
    globals()["vllm"] = LLM


def map_model_alias(alias: str) -> str:
    canon = alias.strip().lower()
    if canon in {"llama3.1-8b-instruct", "llama-3.1-8b-instruct", "llama31-8b", "llama3.1"}:
        return "meta-llama/Llama-3.1-8B-Instruct"
    return alias


DOMAINS = {
    "abstract_algebra": "STEM",
    "anatomy": "STEM",
    "astronomy": "STEM",
    "college_biology": "STEM",
    "college_chemistry": "STEM",
    "college_computer_science": "STEM",
    "college_mathematics": "STEM",
    "college_physics": "STEM",
    "computer_security": "STEM",
    "conceptual_physics": "STEM",
    "electrical_engineering": "STEM",
    "elementary_mathematics": "STEM",
    "high_school_biology": "STEM",
    "high_school_chemistry": "STEM",
    "high_school_computer_science": "STEM",
    "high_school_mathematics": "STEM",
    "high_school_physics": "STEM",
    "high_school_statistics": "STEM",
    "machine_learning": "STEM",
    "humanities": "Humanities",  # umbrella if needed
    # Social sciences & other mapping for common tasks
}


def map_task_to_domain(task: str) -> str:
    # Simplified mapping; default to Other
    for k, v in DOMAINS.items():
        if task.startswith(k):
            return v
    if "history" in task or "philosophy" in task or "law" in task:
        return "Humanities"
    if "econom" in task or "geograph" in task or "politic" in task or "psychology" in task:
        return "Social Sciences"
    return "Other"


def build_prompt(question: str, choices: List[str]) -> str:
    options = "\n".join([f"{chr(65+i)}. {c}" for i, c in enumerate(choices)])
    return (
        "You are a helpful assistant. Answer the multiple-choice question by outputting only the letter.\n\n"
        f"Question: {question}\n\n"
        f"Choices:\n{options}\n\n"
        "Answer:"
    )


def evaluate_mmlu(
    model_id: str,
    batch_size: int,
    max_new_tokens: int,
    total_limit: int | None,
    max_model_len: int,
    gpu_memory_utilization: float,
    dtype: str,
) -> Tuple[Dict[str, float], Dict[str, float]]:
    tasks = [
        # A light subset if total_limit is small; otherwise datasets will include all
        "mmlu",
    ]
    ds = datasets("cais/mmlu", "all", split="test")

    tp_size = torch.cuda.device_count() if torch.cuda.is_available() else 1
    llm = vllm(
        model=model_id,
        tensor_parallel_size=max(1, tp_size),
        dtype=dtype,
        trust_remote_code=True,
        max_model_len=max_model_len,
        gpu_memory_utilization=gpu_memory_utilization,
    )
    sp = SamplingParams(max_tokens=max_new_tokens, temperature=0.0, top_p=1.0, top_k=1, seed=42)
    tokenizer = AutoTokenizer.from_pretrained(model_id, trust_remote_code=True, use_fast=True)

    total = 0
    correct = 0
    by_domain_total: Counter[str] = Counter()
    by_domain_correct: Counter[str] = Counter()

    prompts: List[str] = []
    answers: List[str] = []
    domains: List[str] = []
    subjects: List[str] = []
    sample_rows: List[Dict[str, Any]] = []

    for ex in ds:
        if total_limit is not None and total >= total_limit:
            break
        q = ex["question"]
        choices = [ex["choices"][i] for i in range(4)]
        ans = ex["answer"]  # index 0..3
        prompt = build_prompt(q, choices)
        prompts.append(prompt)
        answers.append(chr(65 + int(ans)))
        subject = ex.get("subject", "")
        domain = map_task_to_domain(subject)
        domains.append(domain)
        subjects.append(subject)
        total += 1
        if len(prompts) == batch_size:
            # Generate per-sample to capture latency and tokens
            for i, prm in enumerate(prompts):
                prompt_tokens = len(tokenizer.encode(prm))
                ts = torch.cuda.Event(enable_timing=True) if torch.cuda.is_available() else None
                te = torch.cuda.Event(enable_timing=True) if torch.cuda.is_available() else None
                if ts and te:
                    ts.record()
                t0 = os.times()[4]
                out = llm.generate([prm], sp)
                t1 = os.times()[4]
                if ts and te:
                    te.record(); torch.cuda.synchronize()
                latency_ms = (t1 - t0) * 1000.0
                text = out[0].outputs[0].text.strip() if out and out[0].outputs else ""
                out_tok = 0
                try:
                    out_tok = len(out[0].outputs[0].token_ids)
                except Exception:
                    pass
                pred = text[:1].upper() if text else ""
                if pred == answers[i]:
                    correct += 1
                    by_domain_correct[domains[i]] += 1
                by_domain_total[domains[i]] += 1
                sample_rows.append({
                    "idx": total - len(prompts) + i,
                    "subject": subjects[i],
                    "domain": domains[i],
                    "answer": answers[i],
                    "pred": pred,
                    "correct": int(pred == answers[i]),
                    "latency_ms": latency_ms,
                    "prompt_tokens": prompt_tokens,
                    "output_tokens": out_tok,
                })
            prompts, answers, domains, subjects = [], [], [], []

    # flush
    if prompts:
        for i, prm in enumerate(prompts):
            prompt_tokens = len(tokenizer.encode(prm))
            ts = torch.cuda.Event(enable_timing=True) if torch.cuda.is_available() else None
            te = torch.cuda.Event(enable_timing=True) if torch.cuda.is_available() else None
            if ts and te:
                ts.record()
            t0 = os.times()[4]
            out = llm.generate([prm], sp)
            t1 = os.times()[4]
            if ts and te:
                te.record(); torch.cuda.synchronize()
            latency_ms = (t1 - t0) * 1000.0
            text = out[0].outputs[0].text.strip() if out and out[0].outputs else ""
            out_tok = 0
            try:
                out_tok = len(out[0].outputs[0].token_ids)
            except Exception:
                pass
            pred = text[:1].upper() if text else ""
            if pred == answers[i]:
                correct += 1
                by_domain_correct[domains[i]] += 1
            by_domain_total[domains[i]] += 1
            sample_rows.append({
                "idx": total - len(prompts) + i,
                "subject": subjects[i],
                "domain": domains[i],
                "answer": answers[i],
                "pred": pred,
                "correct": int(pred == answers[i]),
                "latency_ms": latency_ms,
                "prompt_tokens": prompt_tokens,
                "output_tokens": out_tok,
            })

    overall = {"overall_accuracy": (correct / max(1, sum(by_domain_total.values()))) if by_domain_total else 0.0}
    by_domain = {}
    for d in sorted(by_domain_total.keys()):
        by_domain[d] = by_domain_correct[d] / max(1, by_domain_total[d])
    return overall, by_domain, sample_rows


def main() -> None:
    parser = argparse.ArgumentParser(description="MMLU Evaluator (vLLM)")
    parser.add_argument("--model", default="llama3.1-8b-instruct")
    parser.add_argument("--backend", default="vllm")
    parser.add_argument("--batch-size", default="auto")
    parser.add_argument("--max-new-tokens", type=int, default=1)
    parser.add_argument("--results-dir", default="./results/mmlu")
    parser.add_argument("--total-limit", type=int, default=None)
    parser.add_argument("--max-model-len", type=int, default=4096)
    parser.add_argument("--gpu-memory-utilization", type=float, default=0.90)
    parser.add_argument("--precision", choices=["fp16", "bf16"], default="bf16")
    parser.add_argument("--details", type=int, choices=[0,1], default=1)
    args = parser.parse_args()

    if args.backend != "vllm":
        raise SystemExit("Only vLLM backend is supported")

    _lazy_imports()
    results_root = Path(args.results_dir).resolve()

    def _effective_tz():
        tz_name = os.environ.get("TZ")
        if tz_name and ZoneInfo is not None:
            try:
                return ZoneInfo(tz_name)
            except Exception:
                pass
        try:
            return datetime.now().astimezone().tzinfo or timezone.utc
        except Exception:
            return timezone.utc

    def _timestamp() -> str:
        return datetime.now(_effective_tz()).strftime("%Y%m%d-%H%M%S")

    run_dir = results_root / f"{_timestamp()}-mmlu"
    run_dir.mkdir(parents=True, exist_ok=True)

    model_id = map_model_alias(args.model)
    batch_size = os.cpu_count() or 8
    if isinstance(args.batch_size, str) and args.batch_size == "auto":
        bs = max(4, batch_size // 2)
    else:
        try:
            bs = max(1, int(args.batch_size))
        except Exception:
            bs = 8

    dtype = "bfloat16" if args.precision == "bf16" else "float16"
    overall, by_domain, sample_rows = evaluate_mmlu(
        model_id,
        bs,
        args.max_new_tokens,
        args.total_limit,
        args.max_model_len,
        args.gpu_memory_utilization,
        dtype,
    )
    (run_dir / "overall.json").write_text(json.dumps(overall, indent=2))
    (run_dir / "by_domain.json").write_text(json.dumps(by_domain, indent=2))

    # Per-subject accuracy
    by_subject_total: Counter[str] = Counter()
    by_subject_correct: Counter[str] = Counter()
    for r in sample_rows:
        subj = r["subject"] or ""
        by_subject_total[subj] += 1
        by_subject_correct[subj] += r["correct"]
    by_subject = {s: (by_subject_correct[s] / max(1, by_subject_total[s])) for s in by_subject_total}
    (run_dir / "by_subject.json").write_text(json.dumps(by_subject, indent=2))

    # Optional detailed CSV and plots
    if args.details == 1 and sample_rows:
        import csv
        plots = run_dir / "plots"
        plots.mkdir(parents=True, exist_ok=True)
        with open(run_dir / "samples.csv", "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=list(sample_rows[0].keys()))
            w.writeheader(); w.writerows(sample_rows)
        # Latency histogram
        plt.figure(figsize=(5,3))
        plt.hist([r["latency_ms"] for r in sample_rows], bins=20)
        plt.xlabel("Latency (ms)"); plt.ylabel("Count"); plt.tight_layout()
        plt.savefig(plots / "latency_hist.png"); plt.close()
        # Score by subject (bar chart)
        subj_items = sorted(by_subject.items(), key=lambda x: x[0])
        if subj_items:
            labels = [s for s,_ in subj_items]
            vals = [v for _,v in subj_items]
            plt.figure(figsize=(max(6, len(labels)*0.3), 4))
            plt.bar(labels, vals)
            plt.ylabel("Accuracy")
            plt.xticks(rotation=90)
            plt.tight_layout()
            plt.savefig(plots / "score_by_subject.png"); plt.close()

    # simple markdown report
    lines = ["# MMLU Report", "", f"Model: {model_id}", ""]
    lines.append("## Overall")
    lines.append(f"Accuracy: {overall['overall_accuracy']:.4f}")
    lines.append("")
    lines.append("## By Domain")
    for d, acc in sorted(by_domain.items()):
        lines.append(f"- {d}: {acc:.4f}")
    if args.details == 1 and sample_rows:
        lines.append("")
        lines.append("## Files")
        lines.append("- samples.csv (per-sample predictions, latency, tokens)")
        lines.append("- by_subject.json (per-subject accuracy)")
        lines.append("- plots/latency_hist.png; plots/score_by_subject.png")
    (run_dir / "report.md").write_text("\n".join(lines) + "\n")

    print(f"Wrote MMLU results to {run_dir}")


if __name__ == "__main__":
    main()


