#!/usr/bin/env python3
"""Drive a fixed LLM request list to completion.

Streaming OpenAI-compatible chat completions:
  - first_sse_ms: time to first SSE data line
  - ttft_ms: time to first non-empty content delta (SLO definition)
Does not stop because a wall clock budget elapsed — only when every request in
the list has a terminal status (ok / http_error / timeout / client_error /
incomplete_stream).

Engine/model load is outside this process. Orchestrators must record LLM
load_* intervals from the serving container and merge with CNN intervals via
job_phase_timing.py (union, not sum). This runner emits run_start/run_end for
the request batch only.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def load_requests(path: Path) -> list[dict[str, Any]]:
    text = path.read_text(encoding="utf-8")
    if path.suffix == ".jsonl":
        out = []
        for line in text.splitlines():
            line = line.strip()
            if line:
                out.append(json.loads(line))
        return out
    data = json.loads(text)
    if isinstance(data, list):
        return data
    raise SystemExit("request file must be JSON list or JSONL")


def _pct(xs: list[float], p: float) -> float | None:
    if not xs:
        return None
    s = sorted(xs)
    return s[min(int(p * len(s)), len(s) - 1)]


def one_chat(url: str, req: dict[str, Any], timeout_s: float) -> dict[str, Any]:
    payload = {
        "model": req.get("model", "default"),
        "messages": req.get("messages")
        or [{"role": "user", "content": req.get("prompt", "")}],
        "max_tokens": req.get("max_tokens", 128),
        "temperature": req.get("temperature", 0),
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    body = json.dumps(payload).encode("utf-8")
    http_req = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    t0 = time.perf_counter()
    started = datetime.now(timezone.utc).isoformat()
    first_sse_ms: float | None = None
    ttft_ms: float | None = None
    last_content_ms: float | None = None
    prompt_tokens: int | None = None
    completion_tokens: int | None = None
    finish_reason: str | None = None
    stream_done = False
    empty_content_events = 0
    content_chunks = 0
    text_parts: list[str] = []

    try:
        with urllib.request.urlopen(http_req, timeout=timeout_s) as resp:
            code = getattr(resp, "status", 200)
            for raw in resp:
                elapsed_ms = (time.perf_counter() - t0) * 1000.0
                line = raw.decode("utf-8", errors="replace").rstrip("\r\n")
                if not line.startswith("data:"):
                    continue
                data = line[5:].strip()
                if first_sse_ms is None:
                    first_sse_ms = elapsed_ms
                if data == "[DONE]":
                    stream_done = True
                    break
                try:
                    obj = json.loads(data)
                except json.JSONDecodeError:
                    continue
                if obj.get("error"):
                    return {
                        "request_id": req.get("request_id"),
                        "status": "http_error",
                        "http_status": code,
                        "started_utc": started,
                        "ended_utc": datetime.now(timezone.utc).isoformat(),
                        "latency_ms": elapsed_ms,
                        "first_sse_ms": first_sse_ms,
                        "ttft_ms": ttft_ms,
                        "error": str(obj["error"]),
                    }
                usage = obj.get("usage")
                if usage is not None:
                    prompt_tokens = int(usage.get("prompt_tokens") or 0)
                    completion_tokens = int(usage.get("completion_tokens") or 0)
                choices = obj.get("choices") or []
                if not choices:
                    continue
                ch0 = choices[0]
                if ch0.get("finish_reason") is not None:
                    finish_reason = ch0.get("finish_reason")
                delta = ch0.get("delta") or {}
                piece = delta.get("content")
                if isinstance(piece, str) and piece != "":
                    if ttft_ms is None:
                        ttft_ms = elapsed_ms  # first non-empty content
                    last_content_ms = elapsed_ms
                    content_chunks += 1
                    text_parts.append(piece)
                else:
                    empty_content_events += 1

        latency_ms = (time.perf_counter() - t0) * 1000.0
        incomplete = (
            not stream_done
            or ttft_ms is None
            or finish_reason is None
            or prompt_tokens is None
            or completion_tokens is None
            or int(prompt_tokens) <= 0
            or int(completion_tokens) <= 0
        )
        tpot_ms = None
        if ttft_ms is not None and completion_tokens is not None and completion_tokens > 1 and last_content_ms is not None:
            tpot_ms = (last_content_ms - ttft_ms) / (completion_tokens - 1)

        return {
            "request_id": req.get("request_id"),
            "status": "incomplete_stream" if incomplete else "ok",
            "http_status": code,
            "started_utc": started,
            "ended_utc": datetime.now(timezone.utc).isoformat(),
            "latency_ms": latency_ms,
            "first_sse_ms": first_sse_ms,
            "ttft_ms": ttft_ms,
            "ttft_definition": "first_non_empty_content",
            "last_content_ms": last_content_ms,
            "tpot_ms": tpot_ms,
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "finish_reason": finish_reason,
            "stream_done": stream_done,
            "content_chunks": content_chunks,
            "empty_content_events": empty_content_events,
            "output_text_len": sum(len(t) for t in text_parts),
            "error": "incomplete stream/content/usage/finish_reason" if incomplete else None,
        }
    except urllib.error.HTTPError as e:
        return {
            "request_id": req.get("request_id"),
            "status": "http_error",
            "http_status": e.code,
            "started_utc": started,
            "ended_utc": datetime.now(timezone.utc).isoformat(),
            "latency_ms": (time.perf_counter() - t0) * 1000.0,
            "first_sse_ms": first_sse_ms,
            "ttft_ms": ttft_ms,
            "error": str(e),
        }
    except TimeoutError:
        return {
            "request_id": req.get("request_id"),
            "status": "timeout",
            "started_utc": started,
            "ended_utc": datetime.now(timezone.utc).isoformat(),
            "latency_ms": (time.perf_counter() - t0) * 1000.0,
            "first_sse_ms": first_sse_ms,
            "ttft_ms": ttft_ms,
        }
    except Exception as e:  # noqa: BLE001
        return {
            "request_id": req.get("request_id"),
            "status": "client_error",
            "started_utc": started,
            "ended_utc": datetime.now(timezone.utc).isoformat(),
            "latency_ms": (time.perf_counter() - t0) * 1000.0,
            "first_sse_ms": first_sse_ms,
            "ttft_ms": ttft_ms,
            "error": str(e),
        }


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--endpoint", required=True, help="OpenAI-compatible /v1/chat/completions URL")
    p.add_argument("--requests-file", required=True)
    p.add_argument("--concurrency", type=int, default=4)
    p.add_argument("--timeout-s", type=float, default=120.0)
    p.add_argument("--result-file", required=True)
    p.add_argument("--ready-file", default="")
    args = p.parse_args()

    reqs = load_requests(Path(args.requests_file))
    for i, r in enumerate(reqs):
        if "request_id" not in r:
            r["request_id"] = f"r{i:05d}"
    raw = Path(args.requests_file).read_bytes()
    list_sha = hashlib.sha256(raw).hexdigest()

    if args.ready_file:
        ready = Path(args.ready_file)
        while not ready.exists():
            time.sleep(0.05)

    started = datetime.now(timezone.utc).isoformat()
    run_start_unix = time.time()
    t0 = time.perf_counter()
    results: list[dict[str, Any]] = []
    with ThreadPoolExecutor(max_workers=max(1, args.concurrency)) as ex:
        futs = [ex.submit(one_chat, args.endpoint, r, args.timeout_s) for r in reqs]
        for fut in as_completed(futs):
            results.append(fut.result())
    results.sort(key=lambda x: str(x.get("request_id")))
    run_end_unix = time.time()
    ended = datetime.now(timezone.utc).isoformat()

    ok = sum(1 for r in results if r.get("status") == "ok")
    ttfts = [float(r["ttft_ms"]) for r in results if r.get("ttft_ms") is not None]
    summary = {
        "job_id": "llm",
        "requests_file_sha256": list_sha,
        "n_requested": len(reqs),
        "n_completed_terminal": len(results),
        "n_ok": ok,
        "error_rate": 1.0 - (ok / max(len(reqs), 1)),
        "concurrency": args.concurrency,
        "endpoint": args.endpoint,
        "stream": True,
        "ttft_definition": "first_non_empty_content",
        "ttft_count": len(ttfts),
        "ttft_p50_ms": _pct(ttfts, 0.50),
        "ttft_p99_ms": _pct(ttfts, 0.99),
        "started_utc": started,
        "ended_utc": ended,
        "run_start_unix": run_start_unix,
        "run_end_unix": run_end_unix,
        "duration_s": time.perf_counter() - t0,
        "note": "load_* must be supplied by orchestrator from engine lifecycle; "
        "merge overlaps with job_phase_timing.py",
        "results": results,
    }
    Path(args.result_file).write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({k: summary[k] for k in summary if k != "results"}, indent=2))
    if len(results) != len(reqs):
        raise SystemExit(2)


if __name__ == "__main__":
    main()
