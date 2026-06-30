"""
Hardware Performance Benchmark Runner

This tool executes inference workloads on GPU/NPU devices while monitoring
real-time power consumption and throughput metrics for data collection.
"""

import os
import re
import sys
import time
import socket
import logging
import threading
import statistics
import subprocess
from typing import Optional

import grpc
import numpy as np
import requests

import benchmark_pb2
import benchmark_pb2_grpc

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger(__name__)

CATALOG_SERVICE_ADDR = os.getenv(
    "CATALOG_SERVICE_ADDR",
    "catalog-service.kcp-control-plane.svc.cluster.local:50051",
)
DATABASE_URL = os.getenv(
    "DATABASE_URL",
    "postgresql://platform:platform1234"
    "@timescaledb.data-pipeline.svc.cluster.local:5432/workload_data",
)
PROMETHEUS_URL     = os.getenv("PROMETHEUS_URL",  "http://10.0.4.230:30090")
BENCH_DURATION_SEC = int(os.getenv("BENCH_DURATION_SEC", "60"))
NPU_INDEX          = int(os.getenv("NPU_INDEX", "0"))

_GPU_MODELS_DEFAULT = (
    "resnet50,efficientnet_b0,efficientnet_v2_s,"
    "yolov5m,yolov5l,yolov8n,ssd_mobilenet"
)
_NPU_MODELS_DEFAULT = "yolov8n,yolov5m,yolov5l,resnet50_i8"

GPU_MODELS    = [m.strip() for m in os.getenv("GPU_MODELS",  _GPU_MODELS_DEFAULT).split(",") if m.strip()]
GPU_BATCHES   = [int(b)    for b in os.getenv("GPU_BATCHES", "1,4,8,16").split(",")           if b.strip()]
GPU_PRECISION = os.getenv("GPU_PRECISION", "fp16")
NPU_MODELS    = [m.strip() for m in os.getenv("NPU_MODELS",  _NPU_MODELS_DEFAULT).split(",")  if m.strip()]
NPU_BATCHES   = [int(b)    for b in os.getenv("NPU_BATCHES", "1,4,8").split(",")              if b.strip()]

_NPU_BASE_Q  = "/root/models/warboy-vision-models/models/quantized_onnx"
_NPU_BASE_FP = "/root/models/warboy-vision-models/models/onnx"

NPU_MODEL_REGISTRY: dict[str, dict] = {
    "yolov8n": {
        "path":        f"{_NPU_BASE_Q}/object_detection/yolov8n_i8.onnx",
        "input_shape": (1, 3, 640, 640),
        "input_dtype": "uint8",
        "task":        "inference",
        "precision":   "int8",
    },
    "yolov5m": {
        "path":        f"{_NPU_BASE_FP}/object_detection/yolov5m.onnx",
        "input_shape": (1, 3, 640, 640),
        "input_dtype": "float32",
        "task":        "inference",
        "precision":   "fp32",
    },
    "yolov5l": {
        "path":        f"{_NPU_BASE_FP}/object_detection/yolov5l.onnx",
        "input_shape": (1, 3, 640, 640),
        "input_dtype": "float32",
        "task":        "inference",
        "precision":   "fp32",
    },
    "resnet50_i8": {
        "path":        f"{_NPU_BASE_Q}/classification/resnet50_i8.onnx",
        "input_shape": (1, 3, 224, 224),
        "input_dtype": "uint8",
        "task":        "inference",
        "precision":   "int8",
    },
    "efficientnet_b0_i8": {
        "path":        f"{_NPU_BASE_Q}/classification/efficientnet_b0_i8.onnx",
        "input_shape": (1, 3, 224, 224),
        "input_dtype": "uint8",
        "task":        "inference",
        "precision":   "int8",
    },
    "ssd_mobilenet_i8": {
        "path":        f"{_NPU_BASE_Q}/detection/ssd_mobilenet_v1_i8.onnx",
        "input_shape": (1, 3, 300, 300),
        "input_dtype": "uint8",
        "task":        "inference",
        "precision":   "int8",
    },
}

MODEL_METADATA: dict[str, dict] = {
    "resnet50":           {"model_params_b": 0.0256, "model_domain": 0, "framework_type": 0, "input_size": 224, "output_len": 0, "tdp_w": 300.0},
    "efficientnet_b0":    {"model_params_b": 0.0054, "model_domain": 0, "framework_type": 0, "input_size": 224, "output_len": 0, "tdp_w": 300.0},
    "efficientnet_v2_s":  {"model_params_b": 0.0218, "model_domain": 0, "framework_type": 0, "input_size": 384, "output_len": 0, "tdp_w": 300.0},
    "yolov5m":            {"model_params_b": 0.0213, "model_domain": 0, "framework_type": 0, "input_size": 640, "output_len": 0, "tdp_w": 300.0},
    "yolov5l":            {"model_params_b": 0.0461, "model_domain": 0, "framework_type": 0, "input_size": 640, "output_len": 0, "tdp_w": 300.0},
    "yolov8n":            {"model_params_b": 0.0032, "model_domain": 0, "framework_type": 0, "input_size": 640, "output_len": 0, "tdp_w": 300.0},
    "ssd_mobilenet":      {"model_params_b": 0.0069, "model_domain": 0, "framework_type": 0, "input_size": 300, "output_len": 0, "tdp_w": 300.0},
    "resnet50_i8":        {"model_params_b": 0.0256, "model_domain": 0, "framework_type": 2, "input_size": 224, "output_len": 0, "tdp_w": 40.0},
    "efficientnet_b0_i8": {"model_params_b": 0.0054, "model_domain": 0, "framework_type": 2, "input_size": 224, "output_len": 0, "tdp_w": 40.0},
    "ssd_mobilenet_i8":   {"model_params_b": 0.0069, "model_domain": 0, "framework_type": 2, "input_size": 300, "output_len": 0, "tdp_w": 40.0},
}
_PREC_ENC = {"fp32": 0, "fp16": 1, "int8": 2, "bf16": 3}



class GPUMetricsMonitor:
    

    def __init__(self):
        self._power_samples: list[float] = []
        self._util_samples:  list[float] = []
        self._mem_samples:   list[float] = []
        self._clock_samples: list[float] = []
        self._running = False
        self._handle  = None
        self._nvml    = None
        try:
            import pynvml
            pynvml.nvmlInit()
            self._handle = pynvml.nvmlDeviceGetHandleByIndex(0)
            self._nvml   = pynvml
            logger.info("pynvml 초기화 완료")
        except Exception as e:
            logger.warning(f"pynvml 사용 불가: {e} → 전력 0으로 기록")

    def start(self):
        self._power_samples.clear()
        self._util_samples.clear()
        self._mem_samples.clear()
        self._clock_samples.clear()
        self._running = True
        if self._handle:
            threading.Thread(target=self._loop, daemon=True).start()

    def _loop(self):
        while self._running:
            try:
                pynvml = self._nvml
                h      = self._handle
                mw     = pynvml.nvmlDeviceGetPowerUsage(h)
                self._power_samples.append(mw / 1000.0)
                util   = pynvml.nvmlDeviceGetUtilizationRates(h)
                self._util_samples.append(float(util.gpu))
                self._mem_samples.append(float(util.memory))
                clock  = pynvml.nvmlDeviceGetClockInfo(h, pynvml.NVML_CLOCK_SM)
                self._clock_samples.append(float(clock))
            except Exception:
                pass
            time.sleep(0.1)

    def stop(self) -> dict:
        self._running = False
        time.sleep(0.15)
        def _avg(lst): return round(statistics.mean(lst), 2) if lst else 0.0
        return {
            "power_w":          _avg(self._power_samples),
            "avg_gpu_util":     _avg(self._util_samples),
            "avg_mem_util":     _avg(self._mem_samples),
            "avg_sm_clock_mhz": _avg(self._clock_samples),
        }



class NPUPowerMonitor:
    _POWER_RE = re.compile(r"([\d.]+)\s*W")
    _ANSI_RE  = re.compile(r'\x1b\[[0-9;]*m')

    def __init__(self, npu_index: int = 0):
        self._idx     = npu_index
        self._samples: list[float] = []
        self._running = False

    def _sample_once(self) -> Optional[float]:
        try:
            r = subprocess.run(["furiosactl", "info"],
                               capture_output=True, text=True, timeout=2)
            for line in r.stdout.splitlines():
                line = self._ANSI_RE.sub("", line)
                cols = [c.strip() for c in line.split("|")]
                if len(cols) >= 6 and cols[1] == f"npu{self._idx}":
                    m = self._POWER_RE.search(cols[5])
                    if m:
                        return float(m.group(1))
        except Exception:
            pass
        return None

    def start(self):
        self._samples.clear()
        self._running = True
        threading.Thread(target=self._loop, daemon=True).start()

    def _loop(self):
        while self._running:
            v = self._sample_once()
            if v is not None:
                self._samples.append(v)
            time.sleep(0.5)

    def stop(self) -> float:
        self._running = False
        time.sleep(0.6)
        if self._samples:
            logger.info(
                f"  NPU 전력 샘플 n={len(self._samples)} "
                f"min={min(self._samples):.1f} max={max(self._samples):.1f} "
                f"avg={statistics.mean(self._samples):.2f} W"
            )
            return round(statistics.mean(self._samples), 2)
        logger.warning("  NPU 전력 샘플 없음 (furiosactl 미설치?)")
        return 0.0



def _prom_instant(query: str, prom_url: str = PROMETHEUS_URL) -> Optional[float]:
    
    try:
        r = requests.get(
            f"{prom_url}/api/v1/query",
            params={"query": query},
            timeout=5,
        )
        data = r.json().get("data", {}).get("result", [])
        if data:
            return float(data[0]["value"][1])
    except Exception as e:
        logger.debug(f"Prometheus 쿼리 실패 ({query[:60]}): {e}")
    return None


def collect_dcgm_metrics(node: str, gpu_idx: int = 0) -> dict:
    
    label = f'gpu="{gpu_idx}",instance=~".*{node}.*"'
    metrics = {}
    for key, metric in [
        ("dcgm_enc_util",      f"DCGM_FI_DEV_ENC_UTIL{{{label}}}"),
        ("dcgm_dec_util",      f"DCGM_FI_DEV_DEC_UTIL{{{label}}}"),
        ("dcgm_mem_copy_util", f"DCGM_FI_DEV_MEM_COPY_UTIL{{{label}}}"),
    ]:
        v = _prom_instant(metric)
        metrics[key] = v if v is not None else 0.0
    logger.info(f"DCGM 수집: {metrics}")
    return metrics


def collect_kepler_metrics(node: str) -> dict:
    
    label = f'instance=~".*{node}.*"'
    cpu_w = _prom_instant(f"rate(kepler_node_cpu_joules_total{{{label}}}[30s])")
    drm_w = _prom_instant(f"rate(kepler_node_dram_joules_total{{{label}}}[30s])")
    result = {
        "kepler_cpu_pkg_w": round(cpu_w, 2) if cpu_w else 0.0,
        "kepler_dram_w":    round(drm_w, 2) if drm_w else 0.0,
    }
    logger.info(f"Kepler 수집: {result}")
    return result



_ENRICH_SQL = 


def enrich_perf_catalog(
    node_id: str, model_name: str, task_type: str, batch_size: int,
    precision: str, n_samples: int,
    gpu_metrics: dict, dcgm_metrics: dict, kepler_metrics: dict,
    device_type: str = "gpu",
) -> bool:
    
    try:
        import psycopg2
        meta = MODEL_METADATA.get(model_name, {})
        if device_type == "npu":
            framework_type = 2   # Furiosa
            tdp_w          = 40.0
        else:
            framework_type = meta.get("framework_type", 0)
            tdp_w          = meta.get("tdp_w", 300.0)
        params = {
            "avg_gpu_util":       gpu_metrics.get("avg_gpu_util", 0.0),
            "avg_mem_util":       gpu_metrics.get("avg_mem_util", 0.0),
            "avg_sm_clock_mhz":   gpu_metrics.get("avg_sm_clock_mhz", 0.0),
            "dcgm_enc_util":      dcgm_metrics.get("dcgm_enc_util", 0.0),
            "dcgm_dec_util":      dcgm_metrics.get("dcgm_dec_util", 0.0),
            "dcgm_mem_copy_util": dcgm_metrics.get("dcgm_mem_copy_util", 0.0),
            "kepler_cpu_pkg_w":   kepler_metrics.get("kepler_cpu_pkg_w", 0.0),
            "kepler_dram_w":      kepler_metrics.get("kepler_dram_w", 0.0),
            "model_domain":       meta.get("model_domain", 0),
            "model_params_b":     meta.get("model_params_b", 0.0),
            "framework_type":     framework_type,
            "precision_enc":      _PREC_ENC.get(precision, 1),
            "input_size":         meta.get("input_size", 224),
            "output_len":         meta.get("output_len", 0),
            "tdp_w":              tdp_w,
            "n_samples":          n_samples,
            "node_id":            node_id,
            "model_name":         model_name,
            "task_type":          task_type,
            "batch_size":         batch_size,
            "precision":          precision,
        }
        with psycopg2.connect(DATABASE_URL) as conn:
            with conn.cursor() as cur:
                cur.execute(_ENRICH_SQL, params)
                updated = cur.rowcount
        if updated > 0:
            logger.info(f"  perf_catalog XGBoost 특징량 UPDATE 완료 ({updated}행)")
            return True
        logger.warning(f"  perf_catalog UPDATE 대상 없음 — {model_name} batch={batch_size} (gRPC 저장 여부 확인 필요)")
        return False
    except Exception as e:
        logger.warning(f"  perf_catalog enrich 실패: {e}")
        return False



def get_gpu_info() -> benchmark_pb2.HardwareInfo:
    try:
        import pynvml
        pynvml.nvmlInit()
        h       = pynvml.nvmlDeviceGetHandleByIndex(0)
        name    = pynvml.nvmlDeviceGetName(h)
        vram_mb = pynvml.nvmlDeviceGetMemoryInfo(h).total // (1024 * 1024)
        driver  = pynvml.nvmlSystemGetDriverVersion()
        return benchmark_pb2.HardwareInfo(
            device_name=name, device_type="gpu",
            vram_mb=vram_mb, driver_version=driver, memory_bandwidth_gbs=1792.0,
        )
    except Exception:
        pass
    try:
        import torch
        name    = torch.cuda.get_device_name(0)
        vram_mb = torch.cuda.get_device_properties(0).total_memory // (1024 * 1024)
        return benchmark_pb2.HardwareInfo(
            device_name=name, device_type="gpu", vram_mb=vram_mb, driver_version="unknown",
        )
    except Exception:
        return benchmark_pb2.HardwareInfo(
            device_name="unknown-gpu", device_type="gpu", vram_mb=0, driver_version="unknown",
        )


def get_npu_info() -> benchmark_pb2.HardwareInfo:
    try:
        _ANSI = re.compile(r'\x1b\[[0-9;]*m')
        r = subprocess.run(["furiosactl", "info"], capture_output=True, text=True, timeout=5)
        for line in r.stdout.splitlines():
            line = _ANSI.sub("", line)
            cols = [c.strip() for c in line.split("|")]
            if len(cols) >= 6 and cols[1] == f"npu{NPU_INDEX}":
                return benchmark_pb2.HardwareInfo(
                    device_name=f"Furiosa {cols[2].capitalize()}",
                    device_type="npu", driver_version=cols[3],
                    vram_mb=8192, memory_bandwidth_gbs=300.0,
                )
    except Exception as e:
        logger.warning(f"furiosactl info 파싱 실패: {e}")
    return benchmark_pb2.HardwareInfo(
        device_name="Furiosa Warboy", device_type="npu",
        vram_mb=8192, memory_bandwidth_gbs=300.0, driver_version="unknown",
    )



def _load_gpu_model(model_name: str):
    
    import torch
    import torchvision.models as models

    m = model_name.lower()
    if m == "resnet50":
        return models.resnet50(weights=models.ResNet50_Weights.DEFAULT), (3, 224, 224)
    if m == "efficientnet_b0":
        return models.efficientnet_b0(weights=models.EfficientNet_B0_Weights.DEFAULT), (3, 224, 224)
    if m == "efficientnet_v2_s":
        return models.efficientnet_v2_s(weights=models.EfficientNet_V2_S_Weights.DEFAULT), (3, 384, 384)
    if m == "ssd_mobilenet":
        model = models.ssdlite320_mobilenet_v3_large(
            weights=models.SSDLite320_MobileNet_V3_Large_Weights.DEFAULT
        )
        return model, (3, 320, 320)
    if m == "yolov8n":
        try:
            from ultralytics import YOLO
            yolo  = YOLO("yolov8n.pt")
            return yolo.model, (3, 640, 640)
        except ImportError:
            model = torch.hub.load("ultralytics/yolov5", "yolov5n", pretrained=True, trust_repo=True)
            return model, (3, 640, 640)
    if m in ("yolov5m", "yolov5l"):
        model = torch.hub.load("ultralytics/yolov5", m, pretrained=True, trust_repo=True)
        return model, (3, 640, 640)
    raise ValueError(f"지원하지 않는 GPU 모델: {model_name}")


def run_gpu_model(
    model_name: str, batch_size: int, precision: str, duration_sec: int, node_name: str
) -> tuple[benchmark_pb2.PerformanceMetrics, dict, dict, dict, int]:
    
    import torch

    device = torch.device("cuda")
    dtype  = {"fp16": torch.float16, "bf16": torch.bfloat16}.get(precision, torch.float32)

    logger.info(f"[GPU] {model_name} 로드 중 | precision={precision} batch={batch_size}")
    model, (c, h, w) = _load_gpu_model(model_name)
    model = model.to(device=device, dtype=dtype).eval()
    dummy = torch.randn(batch_size, c, h, w, device=device, dtype=dtype)

    with torch.no_grad():
        for _ in range(10):
            model(dummy)
    torch.cuda.synchronize()

    gpu_mon = GPUMetricsMonitor()
    gpu_mon.start()
    time.sleep(2)
    dcgm_snap   = collect_dcgm_metrics(node_name)
    kepler_snap = collect_kepler_metrics(node_name)

    latencies: list[float] = []
    t0_total   = time.perf_counter()
    total_imgs = 0

    with torch.no_grad():
        while time.perf_counter() - t0_total < duration_sec:
            t0 = time.perf_counter()
            model(dummy)
            torch.cuda.synchronize()
            latencies.append((time.perf_counter() - t0) * 1000)
            total_imgs += batch_size

    elapsed     = time.perf_counter() - t0_total
    gpu_metrics = gpu_mon.stop()
    latencies.sort()
    n = len(latencies)

    mem_mb = float(torch.cuda.memory_allocated(device) // (1024 * 1024))
    logger.info(
        f"[GPU] {model_name} batch={batch_size} | "
        f"qps={total_imgs/elapsed:.1f} p99={latencies[int(n*0.99)]:.1f}ms "
        f"power={gpu_metrics['power_w']}W util={gpu_metrics['avg_gpu_util']}%"
    )
    perf = benchmark_pb2.PerformanceMetrics(
        throughput_qps=round(total_imgs / elapsed, 1),
        latency_p50_ms=round(latencies[n // 2], 2),
        latency_p95_ms=round(latencies[int(n * 0.95)], 2),
        latency_p99_ms=round(latencies[int(n * 0.99)], 2),
        power_w=gpu_metrics["power_w"],
        memory_used_mb=mem_mb,
    )
    return perf, gpu_metrics, dcgm_snap, kepler_snap, total_imgs



def run_npu_model(
    model_name: str, batch_size: int, duration_sec: int
) -> tuple[benchmark_pb2.PerformanceMetrics, int]:
    
    from furiosa.runtime.sync import create_runner

    spec = NPU_MODEL_REGISTRY.get(model_name)
    if spec is None:
        raise ValueError(f"NPU 모델 미등록: {model_name}. 등록된 모델: {list(NPU_MODEL_REGISTRY)}")
    if not os.path.exists(spec["path"]):
        raise FileNotFoundError(f"모델 파일 없음: {spec['path']}")

    logger.info(f"[NPU] {model_name} 컴파일 중 (첫 실행 30~120초 소요 가능)")
    c, h, w = spec["input_shape"][1:]

    with create_runner(spec["path"]) as runner:
        dummy = (
            np.random.randint(0, 255, (1, c, h, w), dtype=np.uint8)
            if spec["input_dtype"] == "uint8"
            else np.random.rand(1, c, h, w).astype(np.float32)
        )
        logger.info(f"[NPU] {model_name} 워밍업 중...")
        for _ in range(5):
            runner.run([dummy])

        logger.info(f"[NPU] {model_name} batch={batch_size} 벤치마킹 {duration_sec}s...")
        pwr = NPUPowerMonitor(NPU_INDEX)
        pwr.start()

        latencies:  list[float] = []
        t0_total    = time.perf_counter()
        total_imgs  = 0

        while time.perf_counter() - t0_total < duration_sec:
            t0 = time.perf_counter()
            for _ in range(batch_size):
                runner.run([dummy])
            latencies.append((time.perf_counter() - t0) * 1000)
            total_imgs += batch_size

        elapsed   = time.perf_counter() - t0_total
        avg_power = pwr.stop()

    latencies.sort()
    n = len(latencies)
    logger.info(
        f"[NPU] {model_name} batch={batch_size} | "
        f"qps={total_imgs/elapsed:.1f} p99={latencies[int(n*0.99)]:.1f}ms power={avg_power}W"
    )
    perf = benchmark_pb2.PerformanceMetrics(
        throughput_qps=round(total_imgs / elapsed, 1),
        latency_p50_ms=round(latencies[n // 2], 2),
        latency_p95_ms=round(latencies[int(n * 0.95)], 2),
        latency_p99_ms=round(latencies[int(n * 0.99)], 2),
        power_w=avg_power,
        memory_used_mb=0.0,
    )
    return perf, total_imgs



def send_grpc(
    stub, node_id: str, hw_info: benchmark_pb2.HardwareInfo,
    model_name: str, task: str, precision: str, batch: int,
    metrics: benchmark_pb2.PerformanceMetrics,
) -> bool:
    resp = stub.SubmitResult(benchmark_pb2.BenchmarkResult(
        node_id=node_id,
        hardware=hw_info,
        workload=benchmark_pb2.WorkloadSpec(
            model_name=model_name, task_type=task,
            precision=precision, batch_size=batch,
            framework="pytorch" if hw_info.device_type == "gpu" else "furiosa",
        ),
        metrics=metrics,
        timestamp=int(time.time()),
        source="measured",
    ))
    status = "OK" if resp.success else f"FAIL: {resp.message}"
    logger.info(f"  gRPC [{model_name} batch={batch}] → {status}")
    return resp.success



def main():
    node_id = os.getenv("NODE_NAME", socket.gethostname())
    logger.info(f"=== Benchmark Runner v2 | node={node_id} ===")

    try:
        import torch
        has_gpu = torch.cuda.is_available()
    except ImportError:
        has_gpu = False
    has_npu = os.path.exists("/dev/npu0")

    if not has_gpu and not has_npu:
        logger.error("GPU(CUDA) 및 NPU(/dev/npu0) 모두 없음 — 종료")
        sys.exit(1)
    logger.info(f"디바이스 | GPU={has_gpu}  NPU={has_npu}")

    stub = None
    for attempt in range(10):
        try:
            channel = grpc.insecure_channel(CATALOG_SERVICE_ADDR)
            grpc.channel_ready_future(channel).result(timeout=5)
            stub = benchmark_pb2_grpc.CatalogServiceStub(channel)
            logger.info(f"gRPC 연결 완료 → {CATALOG_SERVICE_ADDR}")
            break
        except Exception as e:
            logger.warning(f"gRPC 연결 {attempt+1}/10: {e}")
            time.sleep(3)
    if stub is None:
        logger.error("catalog-service 연결 실패 — 종료")
        sys.exit(1)

    results: list[dict] = []

    if has_gpu:
        hw_info = get_gpu_info()
        logger.info(f"GPU: {hw_info.device_name}  VRAM={hw_info.vram_mb}MiB")
        logger.info(f"GPU 모델: {GPU_MODELS}  배치: {GPU_BATCHES}  정밀도: {GPU_PRECISION}")

        for model_name in GPU_MODELS:
            for batch in GPU_BATCHES:
                try:
                    perf, gpu_m, dcgm_m, kepler_m, n_samp = run_gpu_model(
                        model_name, batch, GPU_PRECISION, BENCH_DURATION_SEC, node_id
                    )
                    ok = send_grpc(stub, node_id, hw_info, model_name,
                                   "inference", GPU_PRECISION, batch, perf)
                    if ok:
                        time.sleep(0.5)
                        enrich_perf_catalog(
                            node_id, model_name, "inference", batch,
                            GPU_PRECISION, n_samp,
                            gpu_m, dcgm_m, kepler_m,
                        )
                    results.append({"model": model_name, "batch": batch, "device": "gpu",
                                    "ok": ok, "qps": perf.throughput_qps, "power": perf.power_w})
                except Exception as e:
                    logger.error(f"GPU {model_name} batch={batch} 실패: {e}", exc_info=True)
                    results.append({"model": model_name, "batch": batch, "device": "gpu",
                                    "ok": False, "error": str(e)})

    if has_npu:
        hw_info = get_npu_info()
        logger.info(f"NPU: {hw_info.device_name}  fw={hw_info.driver_version}")
        logger.info(f"NPU 모델: {NPU_MODELS}  배치: {NPU_BATCHES}")

        for model_name in NPU_MODELS:
            spec = NPU_MODEL_REGISTRY.get(model_name)
            if spec is None:
                logger.warning(f"NPU 모델 미등록 건너뜀: {model_name}")
                continue
            if not os.path.exists(spec["path"]):
                logger.warning(f"NPU 모델 파일 없음 건너뜀: {spec['path']}")
                continue

            for batch in NPU_BATCHES:
                try:
                    perf, n_samp = run_npu_model(model_name, batch, BENCH_DURATION_SEC)
                    prec = spec.get("precision", "int8")
                    ok   = send_grpc(stub, node_id, hw_info, model_name,
                                     "inference", prec, batch, perf)
                    if ok:
                        time.sleep(0.5)
                        enrich_perf_catalog(
                            node_id, model_name, "inference", batch, prec, n_samp,
                            gpu_metrics={}, dcgm_metrics={}, kepler_metrics={},
                            device_type="npu",
                        )
                    results.append({"model": model_name, "batch": batch, "device": "npu",
                                    "ok": ok, "qps": perf.throughput_qps, "power": perf.power_w})
                except Exception as e:
                    logger.error(f"NPU {model_name} batch={batch} 실패: {e}", exc_info=True)
                    results.append({"model": model_name, "batch": batch, "device": "npu",
                                    "ok": False, "error": str(e)})

    logger.info("=" * 70)
    logger.info("벤치마크 완료 요약")
    logger.info("=" * 70)
    ok_cnt   = sum(1 for r in results if r.get("ok"))
    fail_cnt = len(results) - ok_cnt
    logger.info(f"전체: {len(results)}건  성공: {ok_cnt}  실패: {fail_cnt}")
    for r in results:
        if r.get("ok"):
            logger.info(
                f"  [{r['device'].upper()}] {r['model']} batch={r['batch']} "
                f"qps={r.get('qps', 0):.1f} power={r.get('power', 0):.1f}W → OK"
            )
        else:
            logger.info(
                f"  [{r['device'].upper()}] {r['model']} batch={r['batch']} "
                f"→ FAIL ({r.get('error', 'gRPC 오류')})"
            )
    logger.info("=" * 70)
    logger.info(">>> analysis-engine POST /train 으로 XGBoost 재학습을 실행하세요 <<<")


if __name__ == "__main__":
    main()
