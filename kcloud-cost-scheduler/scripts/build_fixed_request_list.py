#!/usr/bin/env python3
"""Build a fixed LLM request list targeting prompt_tokens≈target_in and max_tokens=out.

Uses the model tokenizer/chat template. Verifies each row's tokenized length before write.
Actual serving usage may still differ slightly; prep runs must confirm with usage.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--model", default="meta-llama/Llama-3.1-8B-Instruct")
    p.add_argument("--n", type=int, default=60, help="number of requests in this list")
    p.add_argument("--target-in", type=int, default=149)
    p.add_argument("--max-tokens", type=int, default=128)
    p.add_argument("--out", required=True)
    p.add_argument("--tolerance", type=int, default=0, help="allowed |tok-target| when packing")
    args = p.parse_args()

    from transformers import AutoTokenizer

    tok = AutoTokenizer.from_pretrained(args.model, use_fast=True)

    def count_prompt(user_text: str) -> int:
        messages = [{"role": "user", "content": user_text}]
        ids = tok.apply_chat_template(messages, tokenize=True, add_generation_prompt=True)
        return len(ids)

    # Grow a base paragraph then trim/pad to hit target_in.
    base = (
        "You are assisting a controlled energy measurement. "
        "Reply with a careful technical answer. "
        "Topic index {i}. "
        "Context: scheduling, accelerators, throughput, and power. "
    )
    filler = " Detail about system load, batch size, tokens, and latency. "

    rows = []
    for i in range(args.n):
        text = base.format(i=i)
        while count_prompt(text) < args.target_in - args.tolerance:
            text += filler
        # shrink if overshot
        while count_prompt(text) > args.target_in + args.tolerance and len(text) > 20:
            text = text[: max(20, len(text) - 12)]
        # fine character adjust
        guard = 0
        while count_prompt(text) < args.target_in and guard < 500:
            text += " x"
            guard += 1
        while count_prompt(text) > args.target_in and guard < 1000 and len(text) > 10:
            text = text[:-1]
            guard += 1
        n_tok = count_prompt(text)
        if abs(n_tok - args.target_in) > args.tolerance:
            raise SystemExit(f"request {i}: got {n_tok} tokens want {args.target_in}")
        rows.append(
            {
                "request_id": f"req-{i:05d}",
                "model": args.model,
                "messages": [{"role": "user", "content": text}],
                "max_tokens": args.max_tokens,
                "temperature": 0,
                "target_prompt_tokens": args.target_in,
                "tokenized_prompt_tokens": n_tok,
            }
        )

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    with out.open("w", encoding="utf-8") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    digest = hashlib.sha256(out.read_bytes()).hexdigest()
    meta = {
        "path": str(out),
        "sha256": digest,
        "n": args.n,
        "model": args.model,
        "target_prompt_tokens": args.target_in,
        "max_tokens": args.max_tokens,
        "tokenized_prompt_tokens_all": args.target_in,
        "tokenizer": type(tok).__name__,
    }
    meta_path = out.with_suffix(out.suffix + ".meta.json")
    meta_path.write_text(json.dumps(meta, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(meta, indent=2))


if __name__ == "__main__":
    main()
