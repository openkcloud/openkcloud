#!/usr/bin/env python3
"""GPU LLM sustained-load matrix on Kubernetes — reuses the token_length_campaign collector.

Stream parse + power trapezoid: token_length_campaign (StreamState/request/Samplers/integrate).
Adds only: k8s runtime capture, quiet-hours stop, resume (skip existing performance.json).

ttft_ms from the token_length_campaign collector = first non-empty content.
first_sse_ms is recorded separately. GPU and server (NETIO) energy are never summed.
"""
from __future__ import annotations

import argparse
import csv
import json
import os
import math
import re
import subprocess
import sys
import threading
import time
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional
from zoneinfo import ZoneInfo

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from heterogeneous_serving_experiment import BASE_PROMPT, OUTPUT_TOKENS, discover_model  # noqa: E402
from token_length_campaign import Samplers, integrate, p99, request  # noqa: E402

DEFAULT_CONCS = [10, 20, 30, 40, 45, 50, 55, 60, 70, 80, 100]
SLA_P99_MS = 2115.0
PROMPT = BASE_PROMPT + "What are the top three trade-offs?"


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def pct(xs: list[float], q: float) -> float:
    if not xs:
        return 0.0
    s = sorted(xs)
    if q >= 0.99:
        # match token_length_campaign index rule for p99
        return float(s[min(int(0.99 * len(s)), len(s) - 1)])
    idx = min(max(int(math.ceil(q * len(s)) - 1), 0), len(s) - 1)
    return float(s[idx])


@dataclass
class Agg:
    concurrency: int
    repeat: int
    duration_s: float
    requests_total: int
    requests_success: int
    error_rate: float
    qps: float
    latency_p50_ms: float
    latency_p95_ms: float
    latency_p99_ms: float
    first_sse_p50_ms: float
    first_sse_p95_ms: float
    first_sse_p99_ms: float
    # token_length_campaign definition: first non-empty content
    ttft_p50_ms: float
    ttft_p95_ms: float
    ttft_p99_ms: float
    tpot_p50_ms: float
    tpot_p95_ms: float
    tpot_p99_ms: float
    sla_pass: bool
    energy_j: float  # GPU gross (device only)
    net_energy_j: float  # GPU net vs idle
    energy_per_success_j: float
    gpu_energy_eligible: bool
    server_energy_j: Optional[float]
    server_energy_eligible: bool
    server_power_note: str
    gpu_util_avg_pct: float
    gpu_util_peak_pct: float
    gpu_mem_avg_mb: float
    gpu_mem_peak_mb: float
    avg_power_w: float
    peak_power_w: float
    vllm_running_peak: Optional[float]
    vllm_running_mean: Optional[float]
    vllm_waiting_peak: Optional[float]
    vllm_waiting_mean: Optional[float]
    vllm_kv_pct_peak: Optional[float]
    vllm_kv_pct_mean: Optional[float]
    collector: str = "token_length_campaign"
    ttft_definition: str = "first_non_empty_content"
    first_sse_definition: str = "first_sse_data_event"


def scrape_vllm_gauges(metrics_url: str) -> dict[str, Optional[float]]:
    out: dict[str, Optional[float]] = {"running": None, "waiting": None, "kv_pct": None}
    pat = re.compile(
        r"^(vllm:num_requests_running|vllm:num_requests_waiting|vllm:kv_cache_usage_perc)\{[^}]*\}\s+([0-9.eE+-]+)"
    )
    try:
        text = urllib.request.urlopen(metrics_url, timeout=2).read().decode()
    except Exception:
        return out
    for line in text.splitlines():
        m = pat.match(line)
        if not m:
            continue
        name, val = m.group(1), float(m.group(2))
        if name.endswith("running"):
            out["running"] = val
        elif name.endswith("waiting"):
            out["waiting"] = val
        elif name.endswith("kv_cache_usage_perc"):
            out["kv_pct"] = val * 100.0
    return out


class MetricsSampler:
    def __init__(self, metrics_url: str, interval_s: float = 0.5):
        self.metrics_url = metrics_url
        self.interval_s = interval_s
        self.rows: list[dict[str, Any]] = []
        self._stop = threading.Event()
        self._th: Optional[threading.Thread] = None

    def start(self) -> None:
        self._stop.clear()
        self.rows = []
        self._th = threading.Thread(target=self._loop, daemon=True)
        self._th.start()

    def stop(self) -> None:
        self._stop.set()
        if self._th:
            self._th.join(timeout=5)

    def _loop(self) -> None:
        while not self._stop.is_set():
            g = scrape_vllm_gauges(self.metrics_url)
            self.rows.append({"timestamp_unix": time.time(), **g})
            self._stop.wait(self.interval_s)

    def summary(self) -> dict[str, Optional[float]]:
        def peak(k: str) -> Optional[float]:
            vals = [r[k] for r in self.rows if r.get(k) is not None]
            return max(vals) if vals else None

        def mean(k: str) -> Optional[float]:
            vals = [r[k] for r in self.rows if r.get(k) is not None]
            return round(sum(vals) / len(vals), 3) if vals else None

        return {
            "samples": len(self.rows),
            "running_peak": peak("running"),
            "running_mean": mean("running"),
            "waiting_peak": peak("waiting"),
            "waiting_mean": mean("waiting"),
            "kv_pct_peak": peak("kv_pct"),
            "kv_pct_mean": mean("kv_pct"),
        }


def _parse_vllm_argv(argv: list[Any], cfg: dict[str, Any]) -> None:
    for i, a in enumerate(argv):
        if a == "--max-model-len" and i + 1 < len(argv):
            cfg["max_model_len"] = int(argv[i + 1])
        if a == "--max-num-seqs" and i + 1 < len(argv):
            cfg["max_num_seqs"] = int(argv[i + 1])
        if a == "--max-num-batched-tokens" and i + 1 < len(argv):
            cfg["max_num_batched_tokens"] = int(argv[i + 1])
        if a == "--enable-chunked-prefill":
            cfg["enable_chunked_prefill"] = True
        if a == "--no-enable-chunked-prefill":
            cfg["enable_chunked_prefill"] = False
        if isinstance(a, str) and a.startswith("--max-model-len="):
            cfg["max_model_len"] = int(a.split("=", 1)[1])
        if isinstance(a, str) and a.startswith("--gpu-memory-utilization="):
            cfg["gpu_memory_utilization"] = float(a.split("=", 1)[1])
        if a == "--gpu-memory-utilization" and i + 1 < len(argv):
            cfg["gpu_memory_utilization"] = float(argv[i + 1])
        if isinstance(a, str) and a.startswith("--revision="):
            cfg["revision"] = a.split("=", 1)[1]
        if a == "--revision" and i + 1 < len(argv):
            cfg["revision"] = str(argv[i + 1])


def _parse_engine_log_lines(logs: str, cfg: dict[str, Any]) -> None:
    for line in logs.splitlines():
        m = re.search(r"max_num_seqs[=:\s]+(\d+)", line, re.I)
        if m and cfg.get("max_num_seqs") is None:
            cfg["max_num_seqs"] = int(m.group(1))
        m = re.search(r"max_num_batched_tokens[=:\s]+(\d+)", line, re.I)
        if m and cfg.get("max_num_batched_tokens") is None:
            cfg["max_num_batched_tokens"] = int(m.group(1))
        m = re.search(r"Chunked prefill is enabled with max_num_batched_tokens=(\d+)", line, re.I)
        if m:
            cfg["enable_chunked_prefill"] = True
            if cfg.get("max_num_batched_tokens") is None:
                cfg["max_num_batched_tokens"] = int(m.group(1))


def capture_runtime_config(
    container: str,
    metrics_url: str,
    port: int,
    *,
    k8s_namespace: str = "",
    k8s_pod: str = "",
    manifest_path: str = "",
) -> dict[str, Any]:
    cfg: dict[str, Any] = {
        "container": container,
        "cli_cmd": None,
        "max_num_seqs": None,
        "max_num_batched_tokens": None,
        "enable_chunked_prefill": None,
        "max_model_len": None,
        "gpu_memory_utilization": None,
        "revision": None,
        "runtime_class_name": None,
        "source_notes": [],
        "collection_mode": "k8s_manifest_logs_metrics",
    }
    if manifest_path and Path(manifest_path).exists():
        try:
            import yaml  # type: ignore

            docs = list(yaml.safe_load_all(Path(manifest_path).read_text(encoding="utf-8")))
            pod = next((d for d in docs if d and d.get("kind") == "Pod"), None)
            if pod:
                cfg["runtime_class_name"] = pod.get("spec", {}).get("runtimeClassName")
                c0 = (pod.get("spec", {}).get("containers") or [{}])[0]
                argv = c0.get("args") or []
                cfg["cli_cmd"] = argv
                cfg["resources"] = c0.get("resources")
                _parse_vllm_argv(argv, cfg)
                cfg["source_notes"].append(f"manifest_parsed:{manifest_path}")
        except Exception as e:
            cfg["source_notes"].append(f"manifest_parse_failed:{e}")

    ns = k8s_namespace or "kcp-workload"
    pod = k8s_pod or container
    try:
        raw = subprocess.check_output(
            ["kubectl", "-n", ns, "get", "pod", pod, "-o", "json"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
        live = json.loads(raw)
        cfg["runtime_class_name"] = cfg.get("runtime_class_name") or live.get("spec", {}).get(
            "runtimeClassName"
        )
        c0 = (live.get("spec", {}).get("containers") or [{}])[0]
        argv = c0.get("args") or cfg.get("cli_cmd") or []
        if not cfg.get("cli_cmd"):
            cfg["cli_cmd"] = argv
        _parse_vllm_argv(argv, cfg)
        cfg["resources"] = cfg.get("resources") or c0.get("resources")
        cfg["source_notes"].append(f"kubectl_pod_json:{ns}/{pod}")
    except Exception as e:
        cfg["source_notes"].append(f"kubectl_pod_failed:{e}")

    try:
        logs = subprocess.check_output(
            ["kubectl", "-n", ns, "logs", pod, "--tail=500"],
            stderr=subprocess.STDOUT,
            text=True,
            errors="replace",
        )
        _parse_engine_log_lines(logs, cfg)
        cfg["source_notes"].append("kubectl_logs_parsed")
        cfg["engine_log_excerpt"] = "\n".join(logs.splitlines()[-80:])
    except Exception as e:
        cfg["source_notes"].append(f"kubectl_logs_failed:{e}")

    try:
        text = urllib.request.urlopen(metrics_url, timeout=2).read().decode()
        for line in text.splitlines():
            if line.startswith("vllm:cache_config_info{"):
                cfg["cache_config_info_labels"] = line[line.find("{") + 1 : line.rfind("}")]
                break
    except Exception as e:
        cfg["source_notes"].append(f"metrics_failed:{e}")

    cfg["captured_at"] = now_iso()
    cfg["endpoint"] = f"http://127.0.0.1:{port}"
    return cfg


def load_idle(path: Path) -> float:
    if not path.exists():
        return 0.0
    vals = []
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            if row.get("source") == "gpu":
                vals.append(float(row["watts"]))
    return sum(vals) / len(vals) if vals else 0.0


def sustained_load(
    base_url: str,
    model: str,
    concurrency: int,
    duration_s: float,
    warmup_s: float,
    run_dir: Path,
    netio_url: str,
    idle_gpu_w: float,
) -> tuple[list[dict[str, Any]], float, MetricsSampler, dict[str, Any]]:
    """Closed-loop load using token_length_campaign request() + Samplers/integrate."""
    metrics_url = base_url.rstrip("/") + "/metrics"
    streams_dir = run_dir / "streams"
    streams_dir.mkdir(parents=True, exist_ok=True)
    samples_dir = run_dir / "samples"

    # warmup (not scored) — same collector
    if warmup_s > 0:
        warm_end = time.time() + warmup_s

        def warm_one(i: int) -> None:
            while time.time() < warm_end:
                request(
                    base_url.rstrip("/"),
                    model,
                    PROMPT,
                    OUTPUT_TOKENS,
                    stream_path=streams_dir / f"warmup-{i}-{uuid.uuid4().hex[:8]}.jsonl",
                    request_id=f"warmup-{i}",
                )

        with ThreadPoolExecutor(max_workers=min(concurrency, 8)) as ex:
            futs = [ex.submit(warm_one, i) for i in range(min(concurrency, 8))]
            for f in as_completed(futs):
                f.result()

    # Power: reuse token_length_campaign Samplers (gpu + server NETIO + host + npu attempts + runtime)
    if samples_dir.exists():
        # resume safety: unique subdir
        samples_dir = run_dir / f"samples-{int(time.time())}"
    sampler_power = Samplers(samples_dir, netio_url, endpoint=base_url.rstrip("/"))
    metrics = MetricsSampler(metrics_url, 0.5)
    sampler_power.start()
    metrics.start()

    t0_wall = time.time()
    stop_at = t0_wall + duration_s
    results: list[dict[str, Any]] = []
    lock = threading.Lock()
    counter = 0

    def worker(_: int) -> None:
        nonlocal counter
        while time.time() < stop_at:
            with lock:
                counter += 1
                rid = f"{counter:05d}"
            path = streams_dir / f"{rid}.jsonl"
            row = request(
                base_url.rstrip("/"),
                model,
                PROMPT,
                OUTPUT_TOKENS,
                stream_path=path,
                request_id=rid,
            )
            with lock:
                results.append(row)

    with ThreadPoolExecutor(max_workers=concurrency) as ex:
        futs = [ex.submit(worker, i) for i in range(concurrency)]
        for f in as_completed(futs):
            f.result()

    t1_wall = time.time()
    duration = t1_wall - t0_wall
    sampler_power.close()
    metrics.stop()

    gpu_int = integrate(sampler_power.samples.get("gpu") or [], t0_wall, t1_wall, idle=idle_gpu_w)
    server_samples = sampler_power.samples.get("server") or []
    server_int = integrate(server_samples, t0_wall, t1_wall, idle=0.0) if server_samples else None

    # NETIO time validity: inspect last raw agent time from server.jsonl if present
    server_note_parts = []
    server_eligible = False
    server_energy = None
    if server_int is not None:
        server_note_parts.append(
            f"server_coverage={server_int['coverage']:.4f} gaps={server_int['gaps']} eligible_cov={server_int['eligible']}"
        )
        # Check agent clock from raw samples file
        bad_agent = 0
        total_ok = 0
        sj = samples_dir / "server.jsonl"
        if sj.exists():
            for line in sj.read_text(encoding="utf-8").splitlines():
                try:
                    obj = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if not obj.get("ok"):
                    continue
                total_ok += 1
                agent_t = str(((obj.get("raw") or {}).get("Agent") or {}).get("Time") or "")
                if agent_t.startswith("1970") or not agent_t:
                    bad_agent += 1
        if total_ok and bad_agent == total_ok:
            server_note_parts.append("netio_agent_time_invalid_exclude")
            server_eligible = False
            server_energy = None
        else:
            server_eligible = bool(server_int["eligible"]) and bad_agent == 0
            server_energy = round(server_int["gross_J"], 2) if server_eligible else None
            if bad_agent:
                server_note_parts.append(f"netio_bad_agent_time={bad_agent}/{total_ok}")
                server_eligible = False
                server_energy = None
    else:
        server_note_parts.append("no_server_samples")

    energy_meta = {
        "gpu_integrate": gpu_int,
        "server_integrate": server_int,
        "server_eligible": server_eligible,
        "server_energy_j": server_energy,
        "server_note": "; ".join(server_note_parts),
        "devices_not_sum_with_server": True,
    }
    (run_dir / "energy_integrate.json").write_text(json.dumps(energy_meta, indent=2), encoding="utf-8")
    return results, duration, metrics, energy_meta


def aggregate(
    results: list[dict[str, Any]],
    duration: float,
    metrics: MetricsSampler,
    energy_meta: dict[str, Any],
    concurrency: int,
    repeat: int,
) -> Agg:
    ok = [r for r in results if not r.get("error")]
    errs = [r for r in results if r.get("error")]
    totals = [float(r["total_ms"]) for r in ok]
    first_sse = [float(r["first_sse_ms"]) for r in ok if r.get("first_sse_ms") is not None]
    ttfts = [float(r["ttft_ms"]) for r in ok if r.get("ttft_ms") is not None]
    tpots = [float(r["request_mean_tpot_ms"]) for r in ok if r.get("request_mean_tpot_ms") is not None]
    err_rate = len(errs) / len(results) if results else 1.0
    lat_p99 = pct(totals, 0.99) if totals else 1e9
    gi = energy_meta.get("gpu_integrate") or {}
    gpu_gross = float(gi.get("gross_J") or 0.0)
    gpu_net = float(gi.get("net_J") or 0.0)
    gpu_elig = bool(gi.get("eligible"))
    success = len(ok)
    vs = metrics.summary()

    # util/power from gpu samples file averages (optional)
    avg_w = peak_w = 0.0
    util_avg = util_peak = mem_avg = mem_peak = 0.0
    gs = (energy_meta.get("gpu_integrate") and []) or []
    # read from integrate input not stored; approximate from metrics only for util
    # Prefer sampling gpu.jsonl if present next to energy file — caller stores under run_dir/samples
    return Agg(
        concurrency=concurrency,
        repeat=repeat,
        duration_s=round(duration, 2),
        requests_total=len(results),
        requests_success=success,
        error_rate=round(err_rate, 4),
        qps=round(success / duration, 3) if duration > 0 else 0.0,
        latency_p50_ms=round(pct(totals, 0.50), 2) if totals else 0.0,
        latency_p95_ms=round(pct(totals, 0.95), 2) if totals else 0.0,
        latency_p99_ms=round(lat_p99, 2) if totals else 0.0,
        first_sse_p50_ms=round(pct(first_sse, 0.50), 2) if first_sse else 0.0,
        first_sse_p95_ms=round(pct(first_sse, 0.95), 2) if first_sse else 0.0,
        first_sse_p99_ms=round(pct(first_sse, 0.99), 2) if first_sse else 0.0,
        ttft_p50_ms=round(pct(ttfts, 0.50), 2) if ttfts else 0.0,
        ttft_p95_ms=round(pct(ttfts, 0.95), 2) if ttfts else 0.0,
        ttft_p99_ms=round(pct(ttfts, 0.99), 2) if ttfts else 0.0,
        tpot_p50_ms=round(pct(tpots, 0.50), 2) if tpots else 0.0,
        tpot_p95_ms=round(pct(tpots, 0.95), 2) if tpots else 0.0,
        tpot_p99_ms=round(pct(tpots, 0.99), 2) if tpots else 0.0,
        sla_pass=bool(totals) and lat_p99 <= SLA_P99_MS and err_rate <= 0.01,
        energy_j=round(gpu_gross, 2),
        net_energy_j=round(gpu_net, 2),
        energy_per_success_j=round(gpu_gross / success, 2) if success else 0.0,
        gpu_energy_eligible=gpu_elig,
        server_energy_j=energy_meta.get("server_energy_j"),
        server_energy_eligible=bool(energy_meta.get("server_eligible")),
        server_power_note=str(energy_meta.get("server_note") or ""),
        gpu_util_avg_pct=util_avg,
        gpu_util_peak_pct=util_peak,
        gpu_mem_avg_mb=mem_avg,
        gpu_mem_peak_mb=mem_peak,
        avg_power_w=avg_w,
        peak_power_w=peak_w,
        vllm_running_peak=vs.get("running_peak"),
        vllm_running_mean=vs.get("running_mean"),
        vllm_waiting_peak=vs.get("waiting_peak"),
        vllm_waiting_mean=vs.get("waiting_mean"),
        vllm_kv_pct_peak=vs.get("kv_pct_peak"),
        vllm_kv_pct_mean=vs.get("kv_pct_mean"),
    )


def _fill_gpu_telemetry(agg: Agg, samples_dir: Path) -> Agg:
    path = samples_dir / "gpu.jsonl"
    if not path.exists():
        return agg
    watts = []
    utils = []
    mems = []
    for line in path.read_text(encoding="utf-8").splitlines():
        try:
            o = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not o.get("ok"):
            continue
        if "watts" in o:
            watts.append(float(o["watts"]))
        if "utilization_pct" in o:
            utils.append(float(o["utilization_pct"]))
        if "memory_MiB" in o:
            mems.append(float(o["memory_MiB"]))
    if watts:
        agg.avg_power_w = round(sum(watts) / len(watts), 2)
        agg.peak_power_w = round(max(watts), 2)
    if utils:
        agg.gpu_util_avg_pct = round(sum(utils) / len(utils), 2)
        agg.gpu_util_peak_pct = round(max(utils), 2)
    if mems:
        agg.gpu_mem_avg_mb = round(sum(mems) / len(mems), 2)
        agg.gpu_mem_peak_mb = round(max(mems), 2)
    return agg


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--out-root", required=True)
    p.add_argument("--gpu-url", default="http://127.0.0.1:8000")
    p.add_argument("--container", default="llama-gpu-bench")
    p.add_argument("--concurrencies", default=",".join(map(str, DEFAULT_CONCS)))
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--duration-s", type=float, default=120.0)
    p.add_argument("--warmup-s", type=float, default=10.0)
    p.add_argument("--idle-csv", default="")
    p.add_argument("--experiment-id", default="")
    p.add_argument("--k8s-namespace", default="")
    p.add_argument("--k8s-pod", default="")
    p.add_argument("--manifest-path", default="")
    p.add_argument("--netio-url", default=os.environ.get("NETIO_URL"), required="NETIO_URL" not in os.environ,
                   help="NETIO PDU netio.json URL (or set NETIO_URL)")
    p.add_argument("--stop-before-kst", default="")
    p.add_argument("--cell-budget-s", type=float, default=280.0)
    args = p.parse_args()

    out = Path(args.out_root)
    out.mkdir(parents=True, exist_ok=True)
    log_path = out / "probe.log"

    def log(msg: str) -> None:
        line = f"[{now_iso()}] {msg}"
        print(line, flush=True)
        with open(log_path, "a", encoding="utf-8") as f:
            f.write(line + "\n")

    concs = [int(x.strip()) for x in args.concurrencies.split(",") if x.strip()]
    port = int(args.gpu_url.rstrip("/").split(":")[-1])
    metrics_url = args.gpu_url.rstrip("/") + "/metrics"
    model = discover_model(args.gpu_url)

    idle_path = Path(args.idle_csv) if args.idle_csv else out / "idle-power.csv"
    if not idle_path.exists():
        raise SystemExit(f"idle-power.csv missing: {idle_path}")
    if idle_path.resolve() != (out / "idle-power.csv").resolve():
        (out / "idle-power.csv").write_bytes(idle_path.read_bytes())
    idle_w = load_idle(out / "idle-power.csv")

    runtime = capture_runtime_config(
        args.container,
        metrics_url,
        port,
        k8s_namespace=args.k8s_namespace,
        k8s_pod=args.k8s_pod or args.container,
        manifest_path=args.manifest_path,
    )
    runtime.update(
        {
            "output_tokens": OUTPUT_TOKENS,
            "load_mode": "sustained_closed_loop_concurrency",
            "duration_s_target": args.duration_s,
            "repeats": args.repeats,
            "concurrencies": concs,
            "experiment_id": args.experiment_id,
            "endpoint": args.gpu_url,
            "collector": "token_length_campaign",
            "note": (
                "Uses token_length_campaign StreamState/request/Samplers/integrate. "
                "ttft_ms=first non-empty content; first_sse_ms separate. "
                "GPU and NETIO server energy never summed. "
                "Invalid NETIO agent time excluded from representative server energy."
            ),
        }
    )
    (out / "runtime_config.json").write_text(json.dumps(runtime, indent=2), encoding="utf-8")
    log(
        f"model={model} idle_w={idle_w:.1f} concs={concs} repeats={args.repeats} "
        f"duration={args.duration_s}s collector=token_length_campaign"
    )
    log(
        f"runtime_config: max_model_len={runtime.get('max_model_len')} "
        f"batched={runtime.get('max_num_batched_tokens')} "
        f"chunked={runtime.get('enable_chunked_prefill')} "
        f"revision={runtime.get('revision')} rc={runtime.get('runtime_class_name')}"
    )

    summary_rows: list[dict[str, Any]] = []
    if (out / "summary.csv").exists():
        with open(out / "summary.csv", newline="", encoding="utf-8") as f:
            summary_rows.extend(list(csv.DictReader(f)))

    def past_deadline() -> tuple[bool, str]:
        if not args.stop_before_kst:
            return False, ""
        hh, mm = [int(x) for x in args.stop_before_kst.split(":", 1)]
        now = datetime.now(ZoneInfo("Asia/Seoul"))
        deadline = now.replace(hour=hh, minute=mm, second=0, microsecond=0)
        remain = (deadline - now).total_seconds()
        if remain <= args.cell_budget_s:
            return True, f"stop_before_kst={args.stop_before_kst} remain_s={remain:.0f}"
        return False, ""

    aborted = False
    for conc in concs:
        for rep in range(1, args.repeats + 1):
            run_dir = out / "g1-gpu-baseline" / f"run-{rep}" / f"c{conc}"
            if (run_dir / "performance.json").exists():
                log(f"SKIP existing c={conc} r={rep}")
                if not any(
                    str(r.get("concurrency")) == str(conc) and str(r.get("repeat")) == str(rep)
                    for r in summary_rows
                ):
                    summary_rows.append(json.loads((run_dir / "performance.json").read_text(encoding="utf-8")))
                continue
            stop, why = past_deadline()
            if stop:
                log(f"ABORT_TIME_WINDOW before c={conc} r={rep}: {why}")
                with open(out / "incomplete_cells.jsonl", "a", encoding="utf-8") as f:
                    f.write(json.dumps({"concurrency": conc, "repeat": rep, "reason": why, "at": now_iso()}) + "\n")
                aborted = True
                break
            run_dir.mkdir(parents=True, exist_ok=True)
            log(f"=== START c={conc} r={rep} sustained {args.duration_s}s ===")
            results, duration, metrics, energy_meta = sustained_load(
                args.gpu_url,
                model,
                conc,
                args.duration_s,
                args.warmup_s,
                run_dir,
                args.netio_url,
                idle_w,
            )
            agg = aggregate(results, duration, metrics, energy_meta, conc, rep)
            # fill util from samples
            samples_dirs = sorted(run_dir.glob("samples*"))
            if samples_dirs:
                agg = _fill_gpu_telemetry(agg, samples_dirs[-1])

            (run_dir / "performance.json").write_text(json.dumps(asdict(agg), indent=2), encoding="utf-8")
            with open(run_dir / "vllm_metrics.csv", "w", newline="", encoding="utf-8") as f:
                w = csv.DictWriter(f, fieldnames=["timestamp_unix", "running", "waiting", "kv_pct"])
                w.writeheader()
                for r in metrics.rows:
                    w.writerow(r)
            (run_dir / "vllm_metrics_summary.json").write_text(
                json.dumps(metrics.summary(), indent=2), encoding="utf-8"
            )
            with open(run_dir / "requests.jsonl", "w", encoding="utf-8") as f:
                for r in results:
                    # drop huge output_text from summary line optional — keep for audit but cap?
                    f.write(json.dumps(r, ensure_ascii=False) + "\n")

            row = asdict(agg)
            summary_rows = [
                r
                for r in summary_rows
                if not (str(r.get("concurrency")) == str(conc) and str(r.get("repeat")) == str(rep))
            ]
            summary_rows.append(row)
            with open(out / "summary.csv", "w", newline="", encoding="utf-8") as f:
                keys: list[str] = []
                for r in summary_rows:
                    for k in r.keys():
                        if k not in keys:
                            keys.append(k)
                w = csv.DictWriter(f, fieldnames=keys)
                w.writeheader()
                w.writerows(summary_rows)
            log(
                f"DONE c={conc} r={rep} dur={agg.duration_s}s n={agg.requests_total} "
                f"p99={agg.latency_p99_ms} SLA={'PASS' if agg.sla_pass else 'FAIL'} "
                f"sse_p50={agg.first_sse_p50_ms} content_ttft_p50={agg.ttft_p50_ms} "
                f"gpuJ={agg.energy_j} gpu_elig={agg.gpu_energy_eligible} "
                f"serverJ={agg.server_energy_j if agg.server_energy_eligible else 'excluded'}"
            )
        if aborted:
            break

    log("PROBE COMPLETE" if not aborted else "PROBE PAUSED_TIME_WINDOW")
    return 0 if not aborted else 12


if __name__ == "__main__":
    raise SystemExit(main())
