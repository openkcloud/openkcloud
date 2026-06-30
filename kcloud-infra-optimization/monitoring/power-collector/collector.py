"""
Real-time Power and Performance Metrics Collector

This daemon runs on worker nodes to collect high-resolution GPU, NPU, and CPU
metrics from DCGM, Kepler, and node_exporter, storing them in TimescaleDB.
"""

import os
import re
import json
import time
import logging
import subprocess
from datetime import datetime, timezone
from typing import Optional

import psycopg2
import requests
from kubernetes import client, config

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

NODE_NAME      = os.getenv("NODE_NAME", "")
NODE_IP        = os.getenv("NODE_IP", "")       # Downward API: status.hostIP
PROMETHEUS_URL = os.getenv("PROMETHEUS_URL", "http://10.0.4.230:30090")
DB_URL         = os.getenv("DATABASE_URL",
    "postgresql://platform:platform1234@timescaledb.data-pipeline.svc.cluster.local:5432/workload_data")
INTERVAL       = int(os.getenv("COLLECT_INTERVAL_SEC", "30"))

def _init_k8s():
    try:
        config.load_incluster_config()
    except Exception:
        config.load_kube_config()
    return client.CoreV1Api()

def get_active_workloads(v1: client.CoreV1Api) -> list[dict]:
    
    try:
        pods = v1.list_pod_for_all_namespaces(
            field_selector=f"spec.nodeName={NODE_NAME},status.phase=Running"
        )
    except Exception as e:
        logger.warning(f"k8s pod list failed: {e}")
        return [{"workload_id": "idle", "job_type": None, "namespace": NODE_NAME}]

    _SYSTEM_NS = frozenset({
        "kube-system", "kube-public", "kube-node-lease",
        "monitoring", "data-pipeline", "argocd", "istio-system",
        "cert-manager", "local-path-storage", "harbor",
        "knative-serving", "keda", "argo-rollouts", "cilium-secrets",
        "node-feature-discovery", "nfs-provisioner", "oauth2-proxy",
        "opentelemetry-operator-system", "observability",
        "koordinator-system", "fluid-system", "kubeedge",
        "edge-system", "edge-control-plane",
        "volcano-system", "kubeflow-system",
    })

    workloads = []
    for pod in pods.items:
        ns = pod.metadata.namespace
        if ns in _SYSTEM_NS:
            continue
        ann = pod.metadata.annotations or {}
        intent = ann.get("sched.ai/intent", "")
        if not intent:
            continue
        workloads.append({
            "workload_id": f"{ns}/{pod.metadata.name}",
            "job_type":    intent,
            "namespace":   ns,
            "model_name":  ann.get("sched.ai/model-name"),
            "batch_size":  _safe_int(ann.get("sched.ai/batch-size")),
        })

    if not workloads:
        return [{"workload_id": "idle", "job_type": None, "namespace": NODE_NAME,
                 "model_name": None, "batch_size": None}]
    return workloads

class PrometheusClient:
    

    def __init__(self, base_url: str, timeout: int = 10):
        self._url     = base_url.rstrip("/") + "/api/v1/query"
        self._timeout = timeout

    def query(self, promql: str) -> list[dict]:
        
        try:
            resp = requests.get(
                self._url,
                params={"query": promql},
                timeout=self._timeout,
            )
            resp.raise_for_status()
            data = resp.json()
            return data.get("data", {}).get("result", [])
        except Exception as e:
            logger.debug(f"Prometheus query failed ({promql[:60]}...): {e}")
            return []

    def scalar(self, promql: str) -> Optional[float]:
        
        results = self.query(promql)
        if results:
            return _safe_float(results[0]["value"][1])
        return None

_prom: Optional[PrometheusClient] = None

def get_prom() -> PrometheusClient:
    global _prom
    if _prom is None:
        _prom = PrometheusClient(PROMETHEUS_URL)
    return _prom

_energy_prev: dict[str, float] = {}   # key: "{node}/{device_id}"

def collect_gpu_metrics() -> list[dict]:
    
    prom = get_prom()

    base_join = "* on (pod, namespace) group_left(node) kube_pod_info"

    def query_dcgm(metric: str) -> dict:
        
        results = prom.query(f"{metric} {base_join}")
        out = {}
        for r in results:
            if NODE_NAME and r["metric"].get("node") != NODE_NAME:
                continue
            gpu_idx = r["metric"].get("gpu", "0")
            device  = r["metric"].get("device", f"nvidia{gpu_idx}")
            out[gpu_idx] = {
                "value":  _safe_float(r["value"][1]),
                "device": device,
                "model":  r["metric"].get("modelName", ""),
            }
        return out

    power_map = query_dcgm("DCGM_FI_DEV_POWER_USAGE")
    if not power_map:
        logger.debug("DCGM metrics unavailable, falling back to nvidia-smi")
        return _collect_gpu_nvidia_smi()

    util_map      = query_dcgm("DCGM_FI_DEV_GPU_UTIL")
    mem_map       = query_dcgm("DCGM_FI_DEV_FB_USED")
    temp_map      = query_dcgm("DCGM_FI_DEV_GPU_TEMP")
    sm_clk_map    = query_dcgm("DCGM_FI_DEV_SM_CLOCK")
    enc_map       = query_dcgm("DCGM_FI_DEV_ENC_UTIL")
    dec_map       = query_dcgm("DCGM_FI_DEV_DEC_UTIL")
    memcpy_map    = query_dcgm("DCGM_FI_DEV_MEM_COPY_UTIL")
    energy_map    = query_dcgm("DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION")  # mJ counter

    metrics = []
    for gpu_idx, pw in power_map.items():
        dev_id = pw["device"]

        energy_joules = None
        energy_key    = f"{NODE_NAME}/{dev_id}"
        energy_now    = energy_map.get(gpu_idx, {}).get("value")
        if energy_now is not None:
            energy_prev = _energy_prev.get(energy_key)
            if energy_prev is not None and energy_now >= energy_prev:
                energy_joules = round((energy_now - energy_prev) / 1000, 2)
            _energy_prev[energy_key] = energy_now

        metrics.append({
            "device_type":      "gpu",
            "device_id":        dev_id,
            "power_watts":      pw["value"],
            "gpu_util_pct":     util_map.get(gpu_idx, {}).get("value"),
            "memory_used_mb":   mem_map.get(gpu_idx, {}).get("value"),
            "temperature_c":    temp_map.get(gpu_idx, {}).get("value"),
            "energy_joules":    energy_joules,
            "sm_clock_mhz":     sm_clk_map.get(gpu_idx, {}).get("value"),
            "encoder_util_pct": enc_map.get(gpu_idx, {}).get("value"),
            "decoder_util_pct": dec_map.get(gpu_idx, {}).get("value"),
            "mem_copy_util_pct": memcpy_map.get(gpu_idx, {}).get("value"),
            "cpu_pkg_watts":    None,
            "dram_watts":       None,
        })
    return metrics

def _collect_gpu_nvidia_smi() -> list[dict]:
    
    try:
        result = subprocess.run([
            "nvidia-smi",
            "--query-gpu=index,power.draw,utilization.gpu,memory.used,temperature.gpu",
            "--format=csv,noheader,nounits",
        ], capture_output=True, text=True, timeout=10)
        if result.returncode != 0:
            logger.warning(f"nvidia-smi error: {result.stderr.strip()}")
            return []

        metrics = []
        for line in result.stdout.strip().splitlines():
            parts = [p.strip() for p in line.split(",")]
            if len(parts) < 5:
                continue
            metrics.append({
                "device_type":    "gpu",
                "device_id":      f"gpu-{parts[0]}",
                "power_watts":    _safe_float(parts[1]),
                "gpu_util_pct":   _safe_float(parts[2]),
                "memory_used_mb": _safe_float(parts[3]),
                "temperature_c":  _safe_float(parts[4]),
            })
        return metrics
    except FileNotFoundError:
        logger.debug("nvidia-smi not found — no GPU or DCGM not deployed on this node")
        return []
    except Exception as e:
        logger.warning(f"nvidia-smi fallback failed: {e}")
        return []

def collect_cpu_metrics() -> list[dict]:
    
    prom = get_prom()

    cpu_pkg_w = prom.scalar(
        f'kepler_node_cpu_watts{{node_name="{NODE_NAME}", path="aggregated-package"}}'
    )
    dram_w = prom.scalar(
        f'kepler_node_cpu_watts{{node_name="{NODE_NAME}", path="aggregated-dram"}}'
    )
    total_power = None
    if cpu_pkg_w is not None or dram_w is not None:
        total_power = (cpu_pkg_w or 0.0) + (dram_w or 0.0)

    node_inst = f"{NODE_IP}:9100" if NODE_IP else ""
    if node_inst:
        cpu_util = prom.scalar(
            f'100 * (1 - avg(rate(node_cpu_seconds_total{{mode="idle", instance="{node_inst}"}}[2m])))'
        )
        mem_total = prom.scalar(f'node_memory_MemTotal_bytes{{instance="{node_inst}"}}')
        mem_avail = prom.scalar(f'node_memory_MemAvailable_bytes{{instance="{node_inst}"}}')
    else:
        cpu_util = mem_total = mem_avail = None

    mem_used_mb = None
    if mem_total is not None and mem_avail is not None:
        mem_used_mb = round((mem_total - mem_avail) / (1024 * 1024), 1)

    if total_power is None and cpu_util is None and mem_used_mb is None:
        return []

    return [{
        "device_type":      "cpu",
        "device_id":        "cpu0",
        "power_watts":      round(total_power, 2) if total_power is not None else None,
        "gpu_util_pct":     round(cpu_util, 1)    if cpu_util is not None else None,
        "memory_used_mb":   mem_used_mb,
        "temperature_c":    None,
        "energy_joules":    None,
        "sm_clock_mhz":     None,
        "encoder_util_pct": None,
        "decoder_util_pct": None,
        "mem_copy_util_pct": None,
        "cpu_pkg_watts":    round(cpu_pkg_w, 2) if cpu_pkg_w is not None else None,
        "dram_watts":       round(dram_w, 2)    if dram_w is not None else None,
    }]

_POWER_RE = re.compile(r"([\d.]+)\s*W")
_TEMP_RE  = re.compile(r"([\d.]+)\s*°C")
_UTIL_RE  = re.compile(r"([\d.]+)\s*%")

_FURIOSA_PROM_QUERIES = {
    "power_watts":  "furiosa_npu_device_power_consumption_watts",
    "gpu_util_pct": "furiosa_npu_core_utilization_ratio",
    "temperature_c":"furiosa_npu_device_temperature_celsius",
}

def _collect_npu_prometheus() -> list[dict]:
    
    prom = get_prom()
    base_join = "* on (pod, namespace) group_left(node) kube_pod_info"

    power_results = prom.query(
        f'{_FURIOSA_PROM_QUERIES["power_watts"]} {base_join}'
    )
    if not power_results:
        return []

    filtered = [
        r for r in power_results
        if not NODE_NAME or r["metric"].get("node") == NODE_NAME
    ]
    if not filtered:
        return []

    def query_npu(metric: str) -> dict:
        results = prom.query(f"{metric} {base_join}")
        out = {}
        for r in results:
            if NODE_NAME and r["metric"].get("node") != NODE_NAME:
                continue
            dev_id = r["metric"].get("device", r["metric"].get("npu", "npu0"))
            out[dev_id] = _safe_float(r["value"][1])
        return out

    util_map = query_npu(_FURIOSA_PROM_QUERIES["gpu_util_pct"])
    temp_map = query_npu(_FURIOSA_PROM_QUERIES["temperature_c"])

    metrics = []
    for r in filtered:
        dev_id = r["metric"].get("device", r["metric"].get("npu", "npu0"))
        power = _safe_float(r["value"][1])
        util  = util_map.get(dev_id)
        if util is not None and util <= 1.0:
            util = round(util * 100, 1)
        metrics.append({
            "device_type":    "npu",
            "device_id":      dev_id,
            "power_watts":    power,
            "gpu_util_pct":   util,
            "memory_used_mb": None,
            "temperature_c":  temp_map.get(dev_id),
        })
    return metrics

def collect_npu_metrics() -> list[dict]:
    prom_metrics = _collect_npu_prometheus()
    if prom_metrics:
        return prom_metrics

    try:
        result = subprocess.run(
            ["furiosa-smi", "status", "--format", "json"],
            capture_output=True, text=True, timeout=10
        )
        if result.returncode == 0:
            return _parse_npu_json(result.stdout)
    except FileNotFoundError:
        pass
    except Exception:
        pass

    try:
        result = subprocess.run(
            ["furiosactl", "info"],
            capture_output=True, text=True, timeout=10
        )
        if result.returncode == 0:
            return _parse_furiosactl_text(result.stdout)
    except FileNotFoundError:
        logger.debug("furiosactl not found — no NPU on this node")
    except Exception as e:
        logger.warning(f"furiosactl info failed: {e}")
    return []

def _parse_npu_json(stdout: str) -> list[dict]:
    try:
        data = json.loads(stdout)
        metrics = []
        for dev in data.get("devices", []):
            metrics.append({
                "device_type":    "npu",
                "device_id":      dev.get("name", "npu0"),
                "power_watts":    float(dev.get("power_w", 0)) or None,
                "gpu_util_pct":   float(dev.get("utilization_pct", 0)) or None,
                "memory_used_mb": None,
                "temperature_c":  float(dev.get("temperature_c", 0)) or None,
            })
        return metrics
    except Exception as e:
        logger.warning(f"NPU JSON parse failed: {e}")
        return []

def _parse_furiosactl_text(stdout: str) -> list[dict]:
    metrics = []
    for line in stdout.splitlines():
        cols = [c.strip() for c in line.split("|")]
        if len(cols) < 6 or not cols[1].startswith("npu"):
            continue
        power = temp = util = None
        pm = _POWER_RE.search(cols[5] if len(cols) > 5 else "")
        tm = _TEMP_RE.search(cols[4]  if len(cols) > 4 else "")
        um = _UTIL_RE.search(cols[5]  if len(cols) > 5 else "")
        if pm: power = float(pm.group(1))
        if tm: temp  = float(tm.group(1))
        if um: util  = float(um.group(1))
        metrics.append({
            "device_type":      "npu",
            "device_id":        cols[1],
            "power_watts":      power,
            "gpu_util_pct":     util,
            "memory_used_mb":   None,
            "temperature_c":    temp,
            "energy_joules":    None,
            "sm_clock_mhz":     None,
            "encoder_util_pct": None,
            "decoder_util_pct": None,
            "mem_copy_util_pct": None,
            "cpu_pkg_watts":    None,
            "dram_watts":       None,
        })
    return metrics

INSERT_METRICS = 

INSERT_ACCURACY = 

def get_db_conn():
    return psycopg2.connect(DB_URL, connect_timeout=5)

def _match_workload(device_type: str, workloads: list[dict]) -> dict:
    
    npu_types = {"inference"}
    for wl in workloads:
        if wl["workload_id"] == "idle":
            continue
        jt = (wl.get("job_type") or "").lower()
        if device_type == "npu" and jt in npu_types:
            return wl
        if device_type in ("gpu", "cpu") and jt not in npu_types:
            return wl
    for wl in workloads:
        if wl["workload_id"] != "idle":
            return wl
    return workloads[0]

def insert_metrics(metrics: list[dict], workloads: list[dict]):
    if not metrics:
        return
    now = datetime.now(timezone.utc)

    try:
        conn = get_db_conn()
        inserted = 0
        with conn:
            with conn.cursor() as cur:
                for m in metrics:
                    wl = _match_workload(m["device_type"], workloads)
                    cur.execute(INSERT_METRICS, (
                        now,
                        wl["workload_id"],
                        wl["namespace"],
                        wl["job_type"],
                        m["device_type"],
                        m["device_id"],
                        NODE_NAME,
                        m["power_watts"],
                        m["gpu_util_pct"],
                        m["memory_used_mb"],
                        m["temperature_c"],
                        m.get("energy_joules"),
                        m.get("sm_clock_mhz"),
                        m.get("encoder_util_pct"),
                        m.get("decoder_util_pct"),
                        m.get("mem_copy_util_pct"),
                        m.get("cpu_pkg_watts"),
                        m.get("dram_watts"),
                        wl.get("model_name"),
                        wl.get("batch_size"),
                    ))
                    inserted += 1

                    if wl["workload_id"] != "idle" and m["power_watts"] and \
                       m["device_type"] in ("gpu", "npu"):
                        dev = m["device_type"]
                        pw  = m["power_watts"]
                        cur.execute(INSERT_ACCURACY, (
                            dev, dev, pw, dev, pw, dev, wl["workload_id"],
                        ))

        logger.info(
            f"Inserted {inserted} metric(s) | node={NODE_NAME} | "
            f"workloads={[w['workload_id'] for w in workloads]}"
        )
    except Exception as e:
        logger.error(f"DB insert failed: {e}")
    finally:
        try:
            conn.close()
        except Exception:
            pass

def _safe_float(s):
    try:
        v = float(s)
        return v if v >= 0 else None
    except (ValueError, TypeError):
        return None

def _safe_int(s):
    try:
        return int(s)
    except (ValueError, TypeError):
        return None

def main():
    if not NODE_NAME:
        raise RuntimeError("NODE_NAME env var is required (set via downward API)")
    if not NODE_IP:
        logger.warning("NODE_IP not set — node_exporter CPU/memory metrics will be skipped")

    logger.info(
        f"=== Metric Collector v2 started | node={NODE_NAME} ({NODE_IP}) "
        f"interval={INTERVAL}s prometheus={PROMETHEUS_URL} ==="
    )
    v1 = _init_k8s()

    while True:
        try:
            workloads   = get_active_workloads(v1)
            gpu_metrics = collect_gpu_metrics()
            npu_metrics = collect_npu_metrics()
            cpu_metrics = collect_cpu_metrics()
            all_metrics = gpu_metrics + npu_metrics + cpu_metrics

            if all_metrics:
                for m in all_metrics:
                    logger.info(
                        f"  [{m['device_type']}] {m['device_id']}: "
                        f"power={m['power_watts']}W "
                        f"util={m['gpu_util_pct']}% "
                        f"mem={m['memory_used_mb']}MiB "
                        f"temp={m['temperature_c']}°C"
                    )
                insert_metrics(all_metrics, workloads)
            else:
                logger.debug("No metrics collected this cycle")

        except Exception as e:
            logger.error(f"Collection cycle error: {e}")

        time.sleep(INTERVAL)

if __name__ == "__main__":
    main()
