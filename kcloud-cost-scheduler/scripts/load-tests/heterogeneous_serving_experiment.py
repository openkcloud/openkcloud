#!/usr/bin/env python3
"""B0/B1/B2/P1 heterogeneous LLM serving experiment — fixed G0 inputs."""

from __future__ import annotations

import argparse
import concurrent.futures
import csv
import json
import os
import re
import statistics
import subprocess
import threading
import time
import urllib.error
import urllib.request
import uuid
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


# G0 fixed inputs
INPUT_TOKENS_TARGET = 256
OUTPUT_TOKENS = 128
CONTEXT = 4096
WARMUP_REQUESTS = 10
REQUESTS_PER_RUN = 100
CONCURRENCIES = (1, 4, 8)
REPEATS = 3

ELECTRICITY_USD_PER_KWH = 0.12
GPU_USD_PER_HOUR = 2.50
NPU_USD_PER_HOUR = 0.80

# ~256-token prompt (repeat until tokenizer would hit target; fixed text for reproducibility)
BASE_PROMPT = (
    "Summarize the following technical topic in structured bullet points. "
    "Topic: Kubernetes heterogeneous scheduling for LLM inference workloads "
    "combining GPU and NPU accelerators with SLA-aware routing, queue monitoring, "
    "and cost optimization based on measured energy and device utilization. "
    "Include deployment considerations, device plugin requirements, model serving "
    "runtimes such as vLLM and Furiosa-LLM, power sampling methodology, and "
    "evaluation metrics including TTFT, TPOT, p99 latency, throughput, and "
    "cost per successful request under varying concurrency levels. "
    "Question: "
)


@dataclass
class PowerSample:
    ts: float
    watts: float
    source: str


@dataclass
class RequestResult:
    ttft_ms: float
    tpot_ms: Optional[float]
    total_ms: float
    prompt_tokens: int
    completion_tokens: int
    error: Optional[str] = None
    endpoint: str = "default"
    request_id: str = ""


@dataclass
class RunResult:
    run_id: str
    cohort: str
    concurrency: int
    repeat: int
    allocation_mode: str
    runtime: str
    model: str
    started_at: str
    ended_at: str
    requests_total: int
    requests_success: int
    error_rate: float
    throughput_qps: float
    ttft_p50_ms: float
    ttft_p95_ms: float
    ttft_p99_ms: float
    latency_p50_ms: float
    latency_p95_ms: float
    latency_p99_ms: float
    tpot_p50_ms: float
    avg_power_w: float
    peak_power_w: float
    gpu_avg_power_w: float
    npu_avg_power_w: float
    gpu_util_avg_pct: float
    gpu_util_peak_pct: float
    npu_util_avg_pct: float
    npu_util_peak_pct: float
    idle_power_w: float
    duration_s: float
    energy_j: float
    gpu_energy_j: float
    npu_energy_j: float
    net_energy_j: float
    gpu_net_energy_j: float
    npu_net_energy_j: float
    completion_tokens_total: int
    energy_per_success_j: float
    energy_per_1k_tokens_j: float
    energy_cost_usd: float
    gpu_cost_usd: float
    npu_cost_usd: float
    total_cost_usd: float
    cost_per_success_usd: float
    sla_pass: bool
    notes: list[str] = field(default_factory=list)
    experiment_id: str = ""
    run_uuid: str = ""
    workload_id: str = ""
    source: str = "measured"
    trace_id: str = ""
    git_sha: str = ""
    npu_cap: int = 0
    route_summary: dict = field(default_factory=dict)
    cnn_telemetry: dict = field(default_factory=dict)
    lifecycle: dict = field(default_factory=dict)
    request_details: list[dict] = field(default_factory=list)
    gpu_mem_avg_mb: float = 0.0
    gpu_mem_peak_mb: float = 0.0
    throughput_tok_s: float = 0.0


def percentile(vals: list[float], pct: float) -> float:
    if not vals:
        return 0.0
    s = sorted(vals)
    idx = min(int(len(s) * pct), len(s) - 1)
    return s[idx]


def gpu_power_w() -> Optional[float]:
    try:
        out = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=power.draw", "--format=csv,noheader,nounits"],
            text=True,
            timeout=5,
        ).strip()
        return float(out.split("\n")[0])
    except Exception:
        return None


def npu_power_from_exporter(device: str = "npu0") -> Optional[float]:
    url = os.environ.get("FURIOSA_METRICS_URL", "http://127.0.0.1:6254/metrics")
    try:
        with urllib.request.urlopen(url, timeout=3) as resp:
            body = resp.read().decode("utf-8", errors="replace")
        for line in body.splitlines():
            if not line.startswith("furiosa_npu_hw_power"):
                continue
            if f'device="{device}"' not in line or 'label="rms"' not in line:
                continue
            val = line.rsplit(" ", 1)[-1].strip()
            return float(val)
    except Exception:
        return None


def npu_power_w(device: Optional[str] = None) -> Optional[float]:
    # Prefer explicit device, then npu0 (current RNGD), then npu1 (legacy), then any.
    candidates = []
    if device:
        candidates.append(device)
    candidates.extend(["npu0", "npu1"])
    seen = set()
    for dev in candidates:
        if dev in seen:
            continue
        seen.add(dev)
        w = npu_power_from_exporter(dev)
        if w is not None:
            return w
    try:
        out = subprocess.check_output(["furiosa-smi", "info"], text=True, timeout=5)
        for line in out.splitlines():
            if "|" not in line or "Power" in line or "Arch" in line or "Device" in line:
                continue
            cols = [c.strip() for c in line.split("|")]
            if len(cols) < 6:
                continue
            # RNGD: | rngd | npu0 | ... | 40.32 W | ...
            dev = cols[2] if cols[1] in ("rngd", "warboy") else cols[1]
            if dev.startswith("npu"):
                m = re.search(r"([\d.]+)\s*W", cols[5])
                if m:
                    return float(m.group(1))
    except Exception:
        return None
    return None


def npu_util_pct() -> Optional[float]:
    """Peak NPU core utilization 0-100 from furiosa-metrics-exporter."""
    url = os.environ.get("FURIOSA_METRICS_URL", "http://127.0.0.1:6254/metrics")
    try:
        with urllib.request.urlopen(url, timeout=3) as resp:
            body = resp.read().decode("utf-8", errors="replace")
        vals = []
        for line in body.splitlines():
            if not line.startswith("furiosa_npu_core_utilization"):
                continue
            if 'device="npu1"' not in line:
                continue
            v = float(line.rsplit(" ", 1)[-1].strip())
            # exporter may return 0-1 or 0-100
            vals.append(v * 100.0 if v <= 1.0 else v)
        return max(vals) if vals else None
    except Exception:
        return None


def gpu_util_pct() -> Optional[float]:
    try:
        out = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits"],
            text=True,
            timeout=5,
        ).strip()
        return float(out.split("\n")[0])
    except Exception:
        return None


def gpu_mem_used_mb() -> Optional[float]:
    try:
        out = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=memory.used", "--format=csv,noheader,nounits"],
            text=True,
            timeout=5,
        ).strip()
        return float(out.split("\n")[0])
    except Exception:
        return None


def verify_power_sources(sources: list[str], duration_s: float = 10.0, interval_s: float = 0.5) -> dict[str, int]:
    """Sample power sources; return count per source (pre-run gate)."""
    counts: dict[str, int] = {s: 0 for s in sources}
    deadline = time.time() + duration_s
    while time.time() < deadline:
        for src in sources:
            w = gpu_power_w() if src == "gpu" else npu_power_w() if src == "npu" else None
            if w is not None:
                counts[src] += 1
        time.sleep(interval_s)
    return counts


class PowerMonitor:
    def __init__(
        self,
        sources: list[str],
        interval_s: float = 0.5,
        sample_gpu_util: bool = False,
        sample_npu_util: bool = False,
    ):
        self.sources = sources
        self.interval_s = interval_s
        self.sample_gpu_util = sample_gpu_util
        self.sample_npu_util = sample_npu_util
        self.samples: list[PowerSample] = []
        self.util_samples: list[PowerSample] = []  # watts field reused as pct
        self._stop = threading.Event()
        self._thread: Optional[threading.Thread] = None

    def _read(self, source: str) -> Optional[float]:
        if source == "gpu":
            return gpu_power_w()
        if source == "npu":
            return npu_power_w()
        return None

    def start(self):
        def loop():
            while not self._stop.is_set():
                for src in self.sources:
                    w = self._read(src)
                    if w is not None:
                        self.samples.append(PowerSample(time.time(), w, src))
                ts = time.time()
                if self.sample_gpu_util:
                    gu = gpu_util_pct()
                    if gu is not None:
                        self.util_samples.append(PowerSample(ts, gu, "gpu_util"))
                    gm = gpu_mem_used_mb()
                    if gm is not None:
                        self.util_samples.append(PowerSample(ts, gm, "gpu_mem_mb"))
                if self.sample_npu_util:
                    nu = npu_util_pct()
                    if nu is not None:
                        self.util_samples.append(PowerSample(ts, nu, "npu_util"))
                time.sleep(self.interval_s)

        self._thread = threading.Thread(target=loop, daemon=True)
        self._thread.start()

    def stop(self):
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=3)

    def util_avg(self, source: str) -> float:
        vals = [s.watts for s in self.util_samples if s.source == source]
        return round(statistics.mean(vals), 2) if vals else 0.0

    def util_peak(self, source: str) -> float:
        vals = [s.watts for s in self.util_samples if s.source == source]
        return round(max(vals), 2) if vals else 0.0

    def avg_w(self, source: Optional[str] = None) -> float:
        vals = [s.watts for s in self.samples if source is None or s.source == source]
        return round(statistics.mean(vals), 2) if vals else 0.0

    def peak_w(self, source: Optional[str] = None) -> float:
        vals = [s.watts for s in self.samples if source is None or s.source == source]
        return round(max(vals), 2) if vals else 0.0

    def energy_j(self, source: Optional[str] = None) -> float:
        vals = [s for s in self.samples if source is None or s.source == source]
        if len(vals) < 2:
            return 0.0
        total = 0.0
        for i in range(1, len(vals)):
            dt = vals[i].ts - vals[i - 1].ts
            total += vals[i].watts * dt
        return round(total, 2)


def wait_http(url: str, timeout_s: int = 900) -> bool:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=5) as resp:
                if resp.status == 200:
                    return True
        except urllib.error.URLError:
            pass
        time.sleep(5)
    return False


def discover_model(base_url: str) -> str:
    with urllib.request.urlopen(f"{base_url.rstrip('/')}/v1/models", timeout=10) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    models = data.get("data") or []
    if not models:
        raise RuntimeError(f"No models at {base_url}")
    return models[0]["id"]


def streaming_request(base_url: str, model: str, max_tokens: int = OUTPUT_TOKENS) -> RequestResult:
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": BASE_PROMPT + "What are the top three trade-offs?"}],
        "max_tokens": max_tokens,
        "stream": True,
        "stream_options": {"include_usage": True},
        "temperature": 0,
    }
    req = urllib.request.Request(
        f"{base_url.rstrip('/')}/v1/chat/completions",
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    t0 = time.perf_counter()
    ttft_ms: Optional[float] = None
    prompt_tokens = 0
    completion_tokens = 0
    output_count = 0
    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            for raw in resp:
                line = raw.decode("utf-8", errors="replace").strip()
                if not line.startswith("data:"):
                    continue
                chunk_data = line[5:].strip()
                if chunk_data == "[DONE]":
                    break
                now = time.perf_counter()
                if ttft_ms is None:
                    ttft_ms = (now - t0) * 1000
                try:
                    chunk = json.loads(chunk_data)
                    usage = chunk.get("usage")
                    if usage:
                        prompt_tokens = int(usage.get("prompt_tokens") or prompt_tokens)
                        completion_tokens = int(usage.get("completion_tokens") or completion_tokens)
                    delta = chunk["choices"][0].get("delta", {})
                    if delta.get("content"):
                        output_count += 1
                except (json.JSONDecodeError, KeyError, IndexError):
                    pass
    except Exception as e:
        return RequestResult(0, None, (time.perf_counter() - t0) * 1000, 0, 0, str(e), base_url, str(uuid.uuid4()))

    total_ms = (time.perf_counter() - t0) * 1000
    if ttft_ms is None:
        return RequestResult(0, None, total_ms, 0, 0, "no stream tokens", base_url, str(uuid.uuid4()))

    comp = completion_tokens or output_count
    tpot_ms = None
    if comp > 1:
        tpot_ms = (total_ms - ttft_ms) / (comp - 1)

    return RequestResult(
        ttft_ms=round(ttft_ms, 2),
        tpot_ms=round(tpot_ms, 2) if tpot_ms is not None else None,
        total_ms=round(total_ms, 2),
        prompt_tokens=prompt_tokens,
        completion_tokens=comp,
        endpoint=base_url,
        request_id=str(uuid.uuid4()),
    )


def run_load(
    base_urls: list[str],
    model: str,
    concurrency: int,
    num_requests: int,
    power_sources: list[str],
    power_interval: float = 0.5,
    sample_gpu_util: bool = False,
    sample_npu_util: bool = False,
) -> tuple[list[RequestResult], PowerMonitor, float, dict[str, Any]]:
    mon = PowerMonitor(
        power_sources,
        power_interval,
        sample_gpu_util=sample_gpu_util,
        sample_npu_util=sample_npu_util,
    )
    mon.start()
    load_start_mono = time.perf_counter()
    load_start_utc = datetime.now(timezone.utc).isoformat()
    results: list[RequestResult] = []
    url_cycle = base_urls if len(base_urls) > 1 else base_urls * 1
    idx = 0

    def one_request(_: int) -> RequestResult:
        nonlocal idx
        url = url_cycle[idx % len(url_cycle)]
        idx += 1
        return streaming_request(url, model)

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as ex:
        futs = [ex.submit(one_request, i) for i in range(num_requests)]
        for f in concurrent.futures.as_completed(futs):
            results.append(f.result())

    duration = time.perf_counter() - load_start_mono
    load_end_mono = time.perf_counter()
    load_end_utc = datetime.now(timezone.utc).isoformat()
    mon.stop()
    lifecycle = {
        "load_start_utc": load_start_utc,
        "load_end_utc": load_end_utc,
        "load_start_mono": round(load_start_mono, 6),
        "load_end_mono": round(load_end_mono, 6),
        "duration_s": round(duration, 3),
    }
    return results, mon, duration, lifecycle


def compute_costs(
    mon: PowerMonitor,
    duration_s: float,
    power_sources: list[str],
    gpu_active: bool,
    npu_active: bool,
    idle_by_source: Optional[dict[str, float]] = None,
) -> dict:
    energy_j = sum(mon.energy_j(s) for s in power_sources)
    idle_by_source = idle_by_source or {}
    net_energy_j = 0.0
    per_source_net: dict[str, float] = {}
    for s in power_sources:
        samples = [x for x in mon.samples if x.source == s]
        idle = idle_by_source.get(s, 0.0)
        src_net = 0.0
        if len(samples) < 2:
            per_source_net[s] = 0.0
            continue
        for i in range(1, len(samples)):
            dt = samples[i].ts - samples[i - 1].ts
            src_net += max(samples[i].watts - idle, 0.0) * dt
        per_source_net[s] = round(src_net, 2)
        net_energy_j += src_net
    energy_cost = energy_j / 3_600_000 * ELECTRICITY_USD_PER_KWH
    gpu_cost = GPU_USD_PER_HOUR * duration_s / 3600 if gpu_active else 0.0
    npu_cost = NPU_USD_PER_HOUR * duration_s / 3600 if npu_active else 0.0
    total = energy_cost + gpu_cost + npu_cost
    return {
        "energy_j": round(energy_j, 2),
        "net_energy_j": round(net_energy_j, 2),
        "gpu_net_energy_j": per_source_net.get("gpu", 0.0),
        "npu_net_energy_j": per_source_net.get("npu", 0.0),
        "energy_cost_usd": round(energy_cost, 6),
        "gpu_cost_usd": round(gpu_cost, 6),
        "npu_cost_usd": round(npu_cost, 6),
        "total_cost_usd": round(total, 6),
    }


def load_idle_by_source(out_root: Path) -> dict[str, float]:
    path = out_root / "idle-power.csv"
    if not path.exists():
        return {}
    buckets: dict[str, list[float]] = {}
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            buckets.setdefault(row["source"], []).append(float(row["watts"]))
    return {k: (sum(v) / len(v)) for k, v in buckets.items() if v}


def require_idle_sources(out_root: Path, required: list[str]) -> dict[str, float]:
    idle_map = load_idle_by_source(out_root)
    missing = [s for s in required if s not in idle_map or idle_map[s] <= 0]
    if missing:
        raise SystemExit(
            f"idle-power.csv missing or empty for {missing} under {out_root} — run sample_idle first"
        )
    return idle_map


def git_sha() -> str:
    try:
        return subprocess.check_output(
            ["git", "rev-parse", "--short", "HEAD"],
            cwd=Path(__file__).resolve().parent,
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except Exception:
        return "unknown"


def read_cnn_status(status_file: Path) -> dict[str, Any]:
    if not status_file.exists():
        return {}
    try:
        return json.loads(status_file.read_text(encoding="utf-8"))
    except Exception:
        return {}


def cnn_delta(start: dict[str, Any], end: dict[str, Any]) -> dict[str, Any]:
    def num(d: dict, k: str) -> float:
        v = d.get(k)
        return float(v) if v is not None else 0.0

    images_start, images_end = num(start, "images"), num(end, "images")
    forwards_start, forwards_end = num(start, "forwards"), num(end, "forwards")
    elapsed = max(num(end, "elapsed_s") - num(start, "elapsed_s"), 0.0)
    images_delta = max(images_end - images_start, 0)
    forwards_delta = max(forwards_end - forwards_start, 0)
    return {
        "status_file": str(start.get("_path") or ""),
        "batch": end.get("batch") or start.get("batch"),
        "sleep_ms": end.get("sleep_ms") if end.get("sleep_ms") is not None else start.get("sleep_ms"),
        "precision": end.get("precision") or start.get("precision"),
        "images_start": images_start,
        "images_end": images_end,
        "images_delta": images_delta,
        "forwards_start": forwards_start,
        "forwards_end": forwards_end,
        "forwards_delta": forwards_delta,
        "elapsed_s": round(elapsed, 3),
        "throughput_ips": round(images_delta / elapsed, 2) if elapsed > 0 else 0.0,
        "latency_ms_avg_end": end.get("latency_ms_avg"),
    }


def classify_endpoint(endpoint: str, gpu_url: str, npu_url: str) -> str:
    ep = endpoint.rstrip("/")
    if ep == gpu_url.rstrip("/"):
        return "gpu"
    if ep == npu_url.rstrip("/"):
        return "npu"
    if "8000" in ep and "8001" not in ep:
        return "gpu"
    if "8001" in ep:
        return "npu"
    return "unknown"


def route_summary_from_results(
    results: list[RequestResult],
    gpu_url: str = "",
    npu_url: str = "",
) -> dict[str, int]:
    gpu_c = npu_c = err_c = unknown_c = 0
    for r in results:
        if r.error:
            err_c += 1
            continue
        kind = classify_endpoint(r.endpoint, gpu_url, npu_url)
        if kind == "gpu":
            gpu_c += 1
        elif kind == "npu":
            npu_c += 1
        else:
            unknown_c += 1
    return {
        "gpu_count": gpu_c,
        "npu_count": npu_c,
        "error_count": err_c,
        "unknown_count": unknown_c,
        "total": len(results),
    }


def build_run_result(
    cohort: str,
    concurrency: int,
    repeat: int,
    allocation_mode: str,
    runtime: str,
    model: str,
    results: list[RequestResult],
    mon: PowerMonitor,
    duration_s: float,
    power_sources: list[str],
    gpu_active: bool,
    npu_active: bool,
    sla_p99_ms: float,
    notes: list[str],
    lifecycle: Optional[dict[str, Any]] = None,
    cnn_telemetry: Optional[dict[str, Any]] = None,
    gpu_url: str = "",
    npu_url: str = "",
    npu_cap: int = 0,
    trace_id: str = "",
    experiment_id: str = "",
    run_uuid: str = "",
    workload_id: str = "",
    source: str = "measured",
) -> RunResult:
    ok = [r for r in results if not r.error]
    errs = [r for r in results if r.error]
    ttfts = [r.ttft_ms for r in ok]
    totals = [r.total_ms for r in ok]
    tpots = [r.tpot_ms for r in ok if r.tpot_ms is not None]
    p99 = percentile(totals, 0.99)
    err_rate = len(errs) / len(results) if results else 1.0
    sla_pass = p99 <= sla_p99_ms and err_rate <= 0.01
    idle_map = getattr(build_run_result, "_idle_map", {}) or {}
    costs = compute_costs(mon, duration_s, power_sources, gpu_active, npu_active, idle_map)
    success = len(ok)
    completion_tokens = sum(r.completion_tokens for r in ok)
    idle_power = round(sum(idle_map.get(s, 0.0) for s in power_sources), 2)
    net_e = costs["net_energy_j"]
    gpu_e = mon.energy_j("gpu") if "gpu" in power_sources else 0.0
    npu_e = mon.energy_j("npu") if "npu" in power_sources else 0.0
    gpu_net = costs.get("gpu_net_energy_j", 0.0)
    npu_net = costs.get("npu_net_energy_j", 0.0)
    lifecycle = lifecycle or {}
    started_at = lifecycle.get("load_start_utc") or datetime.now(timezone.utc).isoformat()
    ended_at = lifecycle.get("load_end_utc") or datetime.now(timezone.utc).isoformat()
    route_summary = route_summary_from_results(results, gpu_url, npu_url)
    run_uuid = run_uuid or str(uuid.uuid4())
    trace_id = trace_id or str(uuid.uuid4())
    experiment_id = experiment_id or os.environ.get("EXPERIMENT_ID", "local")
    run_id = f"{cohort}-c{concurrency}-r{repeat}"
    workload_id = workload_id or f"{experiment_id}:{run_id}"

    # Normalize lifecycle field names for P0 contract
    lifecycle = {
        **lifecycle,
        "submitted_at": lifecycle.get("submitted_at") or started_at,
        "scheduled_at": lifecycle.get("scheduled_at"),  # Docker experiments: NULL
        "container_started_at": lifecycle.get("container_started_at"),
        "endpoint_ready_at": lifecycle.get("endpoint_ready_at"),
        "load_started_at": lifecycle.get("load_start_utc") or lifecycle.get("load_started_at"),
        "load_ended_at": lifecycle.get("load_end_utc") or lifecycle.get("load_ended_at"),
    }

    return RunResult(
        run_id=run_id,
        cohort=cohort,
        concurrency=concurrency,
        repeat=repeat,
        allocation_mode=allocation_mode,
        runtime=runtime,
        model=model,
        started_at=started_at,
        ended_at=ended_at,
        requests_total=len(results),
        requests_success=success,
        error_rate=round(err_rate, 4),
        throughput_qps=round(success / duration_s, 3) if duration_s > 0 else 0,
        ttft_p50_ms=round(percentile(ttfts, 0.50), 2),
        ttft_p95_ms=round(percentile(ttfts, 0.95), 2),
        ttft_p99_ms=round(percentile(ttfts, 0.99), 2),
        latency_p50_ms=round(percentile(totals, 0.50), 2),
        latency_p95_ms=round(percentile(totals, 0.95), 2),
        latency_p99_ms=round(p99, 2),
        tpot_p50_ms=round(percentile(tpots, 0.50), 2),
        avg_power_w=mon.avg_w(),
        peak_power_w=mon.peak_w(),
        gpu_avg_power_w=mon.avg_w("gpu"),
        npu_avg_power_w=mon.avg_w("npu"),
        gpu_util_avg_pct=mon.util_avg("gpu_util"),
        gpu_util_peak_pct=mon.util_peak("gpu_util"),
        npu_util_avg_pct=mon.util_avg("npu_util"),
        npu_util_peak_pct=mon.util_peak("npu_util"),
        gpu_mem_avg_mb=mon.util_avg("gpu_mem_mb"),
        gpu_mem_peak_mb=mon.util_peak("gpu_mem_mb"),
        idle_power_w=idle_power,
        duration_s=round(duration_s, 2),
        energy_j=costs["energy_j"],
        gpu_energy_j=gpu_e,
        npu_energy_j=npu_e,
        net_energy_j=net_e,
        gpu_net_energy_j=gpu_net,
        npu_net_energy_j=npu_net,
        completion_tokens_total=completion_tokens,
        energy_per_success_j=round(net_e / success, 2) if success else 0,
        energy_per_1k_tokens_j=round(net_e / completion_tokens * 1000, 2) if completion_tokens else 0,
        energy_cost_usd=costs["energy_cost_usd"],
        gpu_cost_usd=costs["gpu_cost_usd"],
        npu_cost_usd=costs["npu_cost_usd"],
        total_cost_usd=costs["total_cost_usd"],
        cost_per_success_usd=round(costs["total_cost_usd"] / success, 6) if success else 0,
        sla_pass=sla_pass,
        notes=notes,
        experiment_id=experiment_id,
        run_uuid=run_uuid,
        workload_id=workload_id,
        source=source,
        trace_id=trace_id,
        git_sha=git_sha(),
        npu_cap=npu_cap,
        route_summary=route_summary,
        cnn_telemetry=cnn_telemetry or {},
        lifecycle=lifecycle,
        request_details=[],
        throughput_tok_s=round(completion_tokens / duration_s, 2) if duration_s > 0 else 0,
    )


def save_run(
    out_dir: Path,
    run: RunResult,
    mon: PowerMonitor,
    required_sources: list[str],
    request_results: list[RequestResult],
    strict: bool = False,
    workload_context: Optional[dict[str, Any]] = None,
):
    # P0 fail-fast: never overwrite an existing run directory
    if out_dir.exists() and (out_dir / "performance.json").exists():
        raise RuntimeError(
            f"run path already exists (fail-fast, no rename/overwrite): {out_dir}. "
            "Use a new EXPERIMENT_ID or different repeat."
        )
    out_dir.mkdir(parents=True, exist_ok=True)
    perf_path = out_dir / "performance.json"
    power_path = out_dir / "power.csv"
    util_path = out_dir / "util.csv"
    requests_path = out_dir / "requests.jsonl"

    with open(perf_path, "w", encoding="utf-8") as f:
        json.dump(asdict(run), f, indent=2, ensure_ascii=False)
    with open(power_path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["timestamp_unix", "watts", "source"])
        for s in mon.samples:
            w.writerow([s.ts, s.watts, s.source])
    with open(util_path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["timestamp_unix", "pct", "source"])
        for s in mon.util_samples:
            w.writerow([s.ts, s.watts, s.source])
    with open(requests_path, "w", encoding="utf-8") as f:
        for i, r in enumerate(request_results):
            row = asdict(r)
            row["request_index"] = i
            if not row.get("request_id"):
                row["request_id"] = f"{run.run_uuid}:{i}"
            f.write(json.dumps(row, ensure_ascii=False) + "\n")
    if workload_context is not None:
        (out_dir / "workload_context.json").write_text(
            json.dumps(workload_context, indent=2, ensure_ascii=False), encoding="utf-8"
        )

    present = {s.source for s in mon.samples}
    missing = [s for s in required_sources if s not in present]
    if missing:
        raise RuntimeError(f"power.csv missing sources {missing} in {out_dir}")

    if strict:
        from validate_run import validate_run_dir

        require_cnn = any("cnn" in str(n).lower() for n in (run.notes or []))
        errors = validate_run_dir(
            out_dir,
            expected_requests=len(request_results),
            require_cnn=require_cnn,
        )
        if errors:
            raise RuntimeError(f"R1 validation failed for {out_dir}:\n  " + "\n  ".join(errors))

    print(f"Saved {perf_path} (sources: {sorted(present)}, requests: {len(request_results)})")


def run_cohort(
    cohort: str,
    base_urls: list[str],
    model: str,
    allocation_mode: str,
    runtime: str,
    power_sources: list[str],
    gpu_active: bool,
    npu_active: bool,
    out_root: Path,
    sla_p99_ms: float,
    split_note: str = "",
    concurrencies: tuple[int, ...] = CONCURRENCIES,
    repeats: int = REPEATS,
    gpu_url: str = "",
    npu_url: str = "",
    cnn_status_file: Optional[Path] = None,
    strict: bool = False,
    sample_gpu_util: bool = False,
    sample_npu_util: bool = False,
    experiment_id: str = "",
    routing_policy: str = "",
    accelerator: str = "gpu",
    requests_per_run: int = 0,
    warmup_requests: int = 0,
):
    from catalog.workload_context import parse_workload_context

    gpu_url = gpu_url or (base_urls[0] if base_urls else "")
    experiment_id = experiment_id or os.environ.get("EXPERIMENT_ID", "local")
    n_req = requests_per_run if requests_per_run > 0 else REQUESTS_PER_RUN
    n_warm = warmup_requests if warmup_requests > 0 else WARMUP_REQUESTS
    for conc in concurrencies:
        for rep in range(1, repeats + 1):
            print(f"\n=== {cohort} concurrency={conc} repeat={rep} requests={n_req} ===")
            print(f"Warm-up {n_warm} requests...")
            run_load(
                base_urls, model, min(conc, 4), n_warm, power_sources,
                sample_gpu_util=sample_gpu_util, sample_npu_util=sample_npu_util,
            )
            notes = [split_note] if split_note else []
            run_uuid = str(uuid.uuid4())
            trace_id = str(uuid.uuid4())
            run_id = f"{cohort}-c{conc}-r{rep}"
            workload_id = f"{experiment_id}:{run_id}"
            ctx = parse_workload_context(
                experiment_id=experiment_id,
                workload_id=workload_id,
                trace_id=trace_id,
                model=model,
                runtime=runtime,
                accelerator=accelerator,
                concurrency=conc,
                slo_p99_ms=sla_p99_ms,
                routing_policy=routing_policy or allocation_mode,
                decision_reason="experimental_assignment",
            )
            cnn_start = read_cnn_status(cnn_status_file) if cnn_status_file else {}
            results, mon, duration, lifecycle = run_load(
                base_urls, model, conc, n_req, power_sources,
                sample_gpu_util=sample_gpu_util, sample_npu_util=sample_npu_util,
            )
            cnn_end = read_cnn_status(cnn_status_file) if cnn_status_file else {}
            cnn_telemetry = cnn_delta(cnn_start, cnn_end) if cnn_status_file else {}
            run = build_run_result(
                cohort, conc, rep, allocation_mode, runtime, model,
                results, mon, duration, power_sources, gpu_active, npu_active,
                sla_p99_ms, notes,
                lifecycle=lifecycle,
                cnn_telemetry=cnn_telemetry,
                gpu_url=gpu_url,
                npu_url=npu_url,
                experiment_id=experiment_id,
                run_uuid=run_uuid,
                workload_id=workload_id,
                trace_id=trace_id,
            )
            run_dir = out_root / cohort / f"run-{rep}" / f"c{conc}"
            save_run(
                run_dir, run, mon, power_sources, results, strict=strict,
                workload_context=ctx.to_dict(),
            )
            print(json.dumps({k: v for k, v in asdict(run).items() if k not in ("request_details",)}, indent=2))


def run_mixed_load(
    gpu_url: str,
    npu_url: str,
    gpu_model: str,
    npu_model: str,
    concurrency: int,
    num_requests: int,
    power_sources: list[str],
    npu_cap: int = 0,
    gpu_frac: float = 0.0,
    switch_after: int = 0,
    sample_gpu_util: bool = False,
    sample_npu_util: bool = False,
) -> tuple[list[RequestResult], PowerMonitor, float, dict[str, Any]]:
    mon = PowerMonitor(
        power_sources,
        sample_gpu_util=sample_gpu_util,
        sample_npu_util=sample_npu_util,
    )
    mon.start()
    load_start_mono = time.perf_counter()
    load_start_utc = datetime.now(timezone.utc).isoformat()
    results: list[RequestResult] = []
    npu_sem = threading.Semaphore(npu_cap) if npu_cap > 0 else None

    def mixed_request(i: int) -> RequestResult:
        if npu_sem is not None:
            if npu_sem.acquire(blocking=False):
                try:
                    return streaming_request(npu_url, npu_model)
                finally:
                    npu_sem.release()
            return streaming_request(gpu_url, gpu_model)
        if i < switch_after:
            return streaming_request(npu_url, npu_model)
        import random
        if gpu_frac > 0 and random.random() < gpu_frac:
            return streaming_request(gpu_url, gpu_model)
        return streaming_request(npu_url, npu_model)

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as ex:
        futs = [ex.submit(mixed_request, i) for i in range(num_requests)]
        for f in concurrent.futures.as_completed(futs):
            results.append(f.result())

    duration = time.perf_counter() - load_start_mono
    load_end_mono = time.perf_counter()
    load_end_utc = datetime.now(timezone.utc).isoformat()
    mon.stop()
    lifecycle = {
        "load_start_utc": load_start_utc,
        "load_end_utc": load_end_utc,
        "load_start_mono": round(load_start_mono, 6),
        "load_end_mono": round(load_end_mono, 6),
        "duration_s": round(duration, 3),
    }
    return results, mon, duration, lifecycle


def run_mixed_cohort(
    cohort: str,
    gpu_url: str,
    npu_url: str,
    gpu_model: str,
    npu_model: str,
    power_sources: list[str],
    out_root: Path,
    sla_p99_ms: float,
    npu_cap: int,
    gpu_frac: float,
    switch_after: int,
    gpu_prewarm: int,
    cnn_note: str,
    concurrencies: tuple[int, ...],
    repeats: int,
    cnn_status_file: Optional[Path],
    strict: bool,
    sample_gpu_util: bool,
    sample_npu_util: bool,
    experiment_id: str = "",
    routing_policy: str = "",
    requests_per_run: int = 0,
    warmup_requests: int = 0,
):
    from catalog.workload_context import parse_workload_context

    experiment_id = experiment_id or os.environ.get("EXPERIMENT_ID", "local")
    n_req = requests_per_run if requests_per_run > 0 else REQUESTS_PER_RUN
    n_warm = warmup_requests if warmup_requests > 0 else WARMUP_REQUESTS
    for conc in concurrencies:
        for rep in range(1, repeats + 1):
            cap_note = (
                f"npu_concurrency_cap={npu_cap}"
                if npu_cap > 0
                else f"switch_after={switch_after} gpu_fraction={gpu_frac}"
            )
            print(f"\n=== {cohort} c={conc} r={rep} {cap_note} requests={n_req} ===")
            print(f"Warm-up {n_warm} requests on NPU...")
            run_load(
                [npu_url], npu_model, min(conc, 4), n_warm, power_sources,
                sample_gpu_util=sample_gpu_util, sample_npu_util=sample_npu_util,
            )
            if gpu_prewarm > 0:
                print(f"GPU pre-warm {gpu_prewarm} requests...")
                run_load(
                    [gpu_url], gpu_model, min(conc, 4), gpu_prewarm, power_sources,
                    sample_gpu_util=sample_gpu_util, sample_npu_util=sample_npu_util,
                )
            notes = [x for x in (cap_note, cnn_note) if x]
            run_uuid = str(uuid.uuid4())
            trace_id = str(uuid.uuid4())
            run_id = f"{cohort}-c{conc}-r{rep}"
            workload_id = f"{experiment_id}:{run_id}"
            mode = "npu-cap-overflow" if npu_cap > 0 else "rngd-first-gpu-overflow"
            policy = routing_policy or mode
            ctx = parse_workload_context(
                experiment_id=experiment_id,
                workload_id=workload_id,
                trace_id=trace_id,
                model=npu_model,
                runtime="mixed",
                accelerator="mixed",
                concurrency=conc,
                slo_p99_ms=sla_p99_ms,
                routing_policy=policy,
                decision_reason="experimental_assignment",
            )
            cnn_start = read_cnn_status(cnn_status_file) if cnn_status_file else {}
            results, mon, duration, lifecycle = run_mixed_load(
                gpu_url, npu_url, gpu_model, npu_model, conc, n_req,
                power_sources, npu_cap=npu_cap, gpu_frac=gpu_frac,
                switch_after=switch_after,
                sample_gpu_util=sample_gpu_util, sample_npu_util=sample_npu_util,
            )
            cnn_end = read_cnn_status(cnn_status_file) if cnn_status_file else {}
            cnn_telemetry = cnn_delta(cnn_start, cnn_end) if cnn_status_file else {}
            run = build_run_result(
                cohort, conc, rep, mode, "mixed",
                npu_model, results, mon, duration, power_sources, True, True,
                sla_p99_ms, notes,
                lifecycle=lifecycle,
                cnn_telemetry=cnn_telemetry,
                gpu_url=gpu_url,
                npu_url=npu_url,
                npu_cap=npu_cap,
                experiment_id=experiment_id,
                run_uuid=run_uuid,
                workload_id=workload_id,
                trace_id=trace_id,
            )
            run_dir = out_root / cohort / f"run-{rep}" / f"c{conc}"
            save_run(
                run_dir, run, mon, power_sources, results, strict=strict,
                workload_context=ctx.to_dict(),
            )
            print(json.dumps({k: v for k, v in asdict(run).items() if k not in ("request_details",)}, indent=2))


def main():
    p = argparse.ArgumentParser()
    p.add_argument(
        "--cohort",
        required=True,
        choices=[
            "g1-gpu-baseline",
            "g2-fixed-split",
            "g3-rngd-first-overflow",
            "g3-npu-cap-overflow",
            "g3-cap1",
            "g3-cap2",
            "g3-cap3",
            "b0-gpu",
            "b1-rngd",
            "b2-static-overflow",
            "p1-prewarm-overflow",
        ],
    )
    p.add_argument("--base-url", action="append", dest="base_urls", default=[])
    p.add_argument("--gpu-url", default="http://127.0.0.1:8000")
    p.add_argument("--npu-url", default="http://127.0.0.1:8001")
    p.add_argument("--model", default="")
    p.add_argument("--out-root", default="results/mixed-cnn-llm")
    p.add_argument("--sla-p99-ms", type=float, default=60000.0)
    p.add_argument("--gpu-fraction", type=float, default=0.0)
    p.add_argument("--gpu-start-after", type=int, default=0)
    p.add_argument("--power-sources", default="", help="comma list: gpu,npu")
    p.add_argument("--concurrencies", default="", help="comma list, e.g. 4,8 (default 1,4,8)")
    p.add_argument("--repeats", type=int, default=0, help="override REPEATS (default from script constant)")
    p.add_argument("--verify-power", action="store_true", help="sample power sources before runs")
    p.add_argument("--npu-concurrency-cap", type=int, default=0, help="G3: max concurrent NPU requests, rest GPU")
    p.add_argument("--gpu-prewarm", type=int, default=0, help="G3: GPU warm-up requests before measured run")
    p.add_argument("--cnn-bg", action="store_true", help="note that CNN GPU background is running")
    p.add_argument("--cnn-status-file", default="/tmp/cnn-gpu-bg-status.json")
    p.add_argument("--require-idle", action="store_true", help="fail if idle-power.csv missing required sources")
    p.add_argument("--strict", action="store_true", help="R1 gate: validate run artifacts before accepting")
    p.add_argument("--experiment-id", default=os.environ.get("EXPERIMENT_ID", "local"))
    p.add_argument("--requests-per-run", type=int, default=0,
                   help="measured requests per run (default REQUESTS_PER_RUN=100)")
    p.add_argument("--warmup-requests", type=int, default=0,
                   help="warmup requests before each measured run (default WARMUP_REQUESTS)")
    args = p.parse_args()

    out_root = Path(args.out_root)
    cohort = args.cohort
    cnn_status = Path(args.cnn_status_file) if args.cnn_bg else None
    cnn_note = "cnn_gpu_background=resnet50" if args.cnn_bg else ""
    experiment_id = args.experiment_id
    requests_per_run = args.requests_per_run if args.requests_per_run > 0 else REQUESTS_PER_RUN
    warmup_requests = args.warmup_requests if args.warmup_requests > 0 else WARMUP_REQUESTS
    if args.requests_per_run > 0 or args.warmup_requests > 0:
        print(f"requests_per_run={requests_per_run} warmup_requests={warmup_requests}")

    def sources_or(default: list[str]) -> list[str]:
        if args.power_sources.strip():
            return [s.strip() for s in args.power_sources.split(",") if s.strip()]
        return default

    concurrencies = CONCURRENCIES
    if args.concurrencies.strip():
        concurrencies = tuple(int(x.strip()) for x in args.concurrencies.split(",") if x.strip())
    repeats = args.repeats if args.repeats > 0 else REPEATS

    power_src = sources_or([])
    if args.require_idle and power_src:
        build_run_result._idle_map = require_idle_sources(out_root, power_src)  # type: ignore[attr-defined]
    else:
        build_run_result._idle_map = load_idle_by_source(out_root)  # type: ignore[attr-defined]
        if args.require_idle and not build_run_result._idle_map:  # type: ignore[attr-defined]
            raise SystemExit(f"No idle-power.csv under {out_root}")

    def util_flags(sources: list[str], gpu_active: bool, npu_active: bool) -> tuple[bool, bool]:
        return (
            "gpu" in sources or gpu_active or args.cnn_bg,
            "npu" in sources or npu_active,
        )

    power_src = sources_or([])
    if args.verify_power and power_src:
        counts = verify_power_sources(power_src, duration_s=10.0)
        print(f"Pre-run power verification: {counts}")
        missing = [s for s in power_src if counts.get(s, 0) < 3]
        if missing:
            raise SystemExit(f"Power verification failed — no samples for: {missing}")

    if cohort in ("g1-gpu-baseline", "b0-gpu"):
        urls = [args.gpu_url]
        model = args.model or discover_model(urls[0])
        ps = sources_or(["gpu"])
        sg, sn = util_flags(ps, True, False)
        run_cohort(
            cohort, urls, model, "gpu-baseline-cnn+llm", "vllm",
            ps, True, False, out_root, args.sla_p99_ms, cnn_note,
            concurrencies, repeats,
            gpu_url=args.gpu_url, npu_url=args.npu_url,
            cnn_status_file=cnn_status, strict=args.strict,
            sample_gpu_util=sg, sample_npu_util=sn,
            experiment_id=experiment_id, routing_policy="g1-gpu-only", accelerator="gpu",
            requests_per_run=requests_per_run, warmup_requests=warmup_requests,
        )

    elif cohort in ("g2-fixed-split", "b1-rngd"):
        urls = [args.npu_url]
        model = args.model or discover_model(urls[0])
        ps = sources_or(["gpu", "npu"])
        sg, sn = util_flags(ps, False, True)
        run_cohort(
            cohort, urls, model, "fixed-split-cnn=gpu-llm=rngd", "furiosa-llm",
            ps, False, True, out_root, args.sla_p99_ms, cnn_note,
            concurrencies, repeats,
            gpu_url=args.gpu_url, npu_url=args.npu_url,
            cnn_status_file=cnn_status, strict=args.strict,
            sample_gpu_util=sg, sample_npu_util=sn,
            experiment_id=experiment_id, routing_policy="g2-fixed-npu", accelerator="npu",
            requests_per_run=requests_per_run, warmup_requests=warmup_requests,
        )

    elif cohort in (
        "g3-rngd-first-overflow",
        "g3-npu-cap-overflow",
        "g3-cap1",
        "g3-cap2",
        "g3-cap3",
        "b2-static-overflow",
        "p1-prewarm-overflow",
    ):
        npu_url = args.npu_url
        gpu_url = args.gpu_url
        npu_model = args.model or discover_model(npu_url)
        gpu_model = discover_model(gpu_url)
        gpu_frac = max(0.0, min(1.0, args.gpu_fraction))
        switch_after = args.gpu_start_after
        npu_cap = max(0, args.npu_concurrency_cap)
        if cohort.startswith("g3-cap") and npu_cap == 0:
            npu_cap = int(cohort.replace("g3-cap", ""))
        power_sources = sources_or(["gpu", "npu"])
        sg, sn = util_flags(power_sources, True, True)
        run_mixed_cohort(
            cohort=cohort,
            gpu_url=gpu_url,
            npu_url=npu_url,
            gpu_model=gpu_model,
            npu_model=npu_model,
            power_sources=power_sources,
            out_root=out_root,
            sla_p99_ms=args.sla_p99_ms,
            npu_cap=npu_cap,
            gpu_frac=gpu_frac,
            switch_after=switch_after,
            gpu_prewarm=args.gpu_prewarm,
            cnn_note=cnn_note,
            concurrencies=concurrencies,
            repeats=repeats,
            cnn_status_file=cnn_status,
            strict=args.strict,
            sample_gpu_util=sg,
            sample_npu_util=sn,
            experiment_id=experiment_id,
            routing_policy="g3-npu-cap-overflow" if npu_cap > 0 else "g3-rngd-first-overflow",
            requests_per_run=requests_per_run,
            warmup_requests=warmup_requests,
        )

    else:
        raise SystemExit(f"Unknown cohort {cohort}")


if __name__ == "__main__":
    main()
