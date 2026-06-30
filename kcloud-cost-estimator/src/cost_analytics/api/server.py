"""
Cost Analytics API Server

This module exposes REST endpoints for querying workload cost analysis
and prediction data.
"""

import logging
from dataclasses import asdict

from fastapi import FastAPI, HTTPException, Query
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

from ..workload_analyzer.analyzer import analyze, node_summary
from ..cost_model.predictor import CostPredictor

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(
    title="cost-analytics-engine",
    description="GPU/NPU 워크로드 비용 분석 및 예측 서비스",
    version="1.0.0",
)
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

_predictor = CostPredictor()

@app.get("/health")
def health():
    
    return {"status": "healthy", "model_trained": _predictor.is_trained}

@app.get("/analyze")
def analyze_workloads(
    interval: str = Query("1 hour", description="예: '1 hour', '6 hours', '1 day'"),
):
    
    profiles = analyze(interval)
    if not profiles:
        raise HTTPException(
            status_code=404, detail="해당 구간에 데이터가 없습니다."
        )
    return {"profiles": [asdict(p) for p in profiles], "count": len(profiles)}

@app.get("/nodes")
def node_stats(interval: str = Query("1 hour")):
    
    df = node_summary(interval)
    if df.empty:
        raise HTTPException(
            status_code=404, detail="해당 구간에 노드 데이터가 없습니다."
        )
    return {"nodes": df.to_dict(orient="records")}

class WorkloadPredictRequest(BaseModel):
    

    workload_id: str
    job_type: str = "inference"
    batch_size: int = 1

class DevicePrediction(BaseModel):
    

    power_w: float | None = None
    cost_usd: float | None = None
    supported: bool = True

class WorkloadPredictResponse(BaseModel):
    

    gpu: DevicePrediction
    npu: DevicePrediction
    recommended_device: str
    reason: str

_JOB_TYPE_FEATURES: dict[str, dict] = {
    "training": {
        "gpu_util_pct": 90.0, "memory_used_mb": 16384.0,
        "temperature_c": 80.0, "sm_clock_mhz": 1800.0, "energy_joules": 0.0,
    },
    "inference": {
        "gpu_util_pct": 60.0, "memory_used_mb": 4096.0,
        "temperature_c": 65.0, "sm_clock_mhz": 1500.0, "energy_joules": 0.0,
    },
    "serving": {
        "gpu_util_pct": 50.0, "memory_used_mb": 2048.0,
        "temperature_c": 60.0, "sm_clock_mhz": 1400.0, "energy_joules": 0.0,
    },
    "batch": {
        "gpu_util_pct": 75.0, "memory_used_mb": 8192.0,
        "temperature_c": 72.0, "sm_clock_mhz": 1650.0, "energy_joules": 0.0,
    },
}
_DEFAULT_FEATURES = _JOB_TYPE_FEATURES["inference"]

_NPU_POWER_RATIO = 0.15

def _ensure_model_trained() -> None:
    
    if not _predictor.is_trained:
        result = _predictor.train("7 days")
        if "error" in result:
            raise HTTPException(
                status_code=503, detail=f"모델 학습 실패: {result['error']}"
            )

@app.post("/predict", response_model=WorkloadPredictResponse)
def predict_workload(req: WorkloadPredictRequest):
    
    _ensure_model_trained()

    base = _JOB_TYPE_FEATURES.get(req.job_type, _DEFAULT_FEATURES).copy()
    base["batch_size"] = req.batch_size
    duration_h = 1.0

    try:
        gpu_features = {**base, "device_type": "gpu", "job_type": req.job_type}
        gpu_power = _predictor.predict_power(gpu_features)
        gpu_cost = _predictor.predict_cost(gpu_features, duration_h)

        npu_features = {**base, "device_type": "npu", "job_type": req.job_type}
        npu_power = _predictor.predict_power(npu_features)
        if npu_power is None or npu_power <= 0:
            npu_power = (gpu_power or 300.0) * _NPU_POWER_RATIO
        npu_cost = _predictor.predict_cost(npu_features, duration_h)
        if npu_cost is None or npu_cost <= 0:
            npu_cost = (gpu_cost or 0.77) * _NPU_POWER_RATIO
    except Exception as exc:
        logger.error(f"예측 실패: {exc}")
        raise HTTPException(status_code=500, detail=f"예측 오류: {exc}") from exc

    if npu_cost is not None and gpu_cost is not None and npu_cost < gpu_cost:
        recommended = "npu"
        reason = (
            f"NPU 예측 비용({npu_cost:.4f} USD/hr)이 "
            f"GPU({gpu_cost:.4f} USD/hr)보다 낮아 NPU를 추천합니다."
        )
    else:
        recommended = "gpu"
        reason = (
            f"GPU 예측 비용({gpu_cost:.4f} USD/hr)이 기준 이하이거나 "
            "NPU 데이터 부족으로 GPU를 추천합니다."
        )

    logger.info(
        "workload predict: workload_id=%s job_type=%s batch_size=%d → %s",
        req.workload_id, req.job_type, req.batch_size, recommended,
    )
    return WorkloadPredictResponse(
        gpu=DevicePrediction(power_w=gpu_power, cost_usd=gpu_cost, supported=True),
        npu=DevicePrediction(power_w=npu_power, cost_usd=npu_cost, supported=True),
        recommended_device=recommended,
        reason=reason,
    )

class MetricsPredictRequest(BaseModel):
    

    gpu_util_pct: float = 50.0
    memory_used_mb: float = 4096.0
    temperature_c: float = 70.0
    energy_joules: float = 0.0
    sm_clock_mhz: float = 1500.0
    batch_size: int = 8
    device_type: str = "gpu"
    job_type: str = "inference"
    duration_hours: float = 1.0

@app.post("/predict/metrics")
def predict_cost_metrics(req: MetricsPredictRequest):
    
    _ensure_model_trained()

    features = req.model_dump()
    duration_hours = features.pop("duration_hours")
    power_w = _predictor.predict_power(features)
    cost = _predictor.predict_cost(features, duration_hours)

    if power_w is None:
        raise HTTPException(status_code=500, detail="예측에 실패했습니다.")

    return {
        "predicted_power_watts": round(power_w, 2),
        "predicted_cost_usd": cost,
        "duration_hours": duration_hours,
    }

@app.post("/train")
def train_model(interval: str = Query("7 days")):
    
    result = _predictor.train(interval)
    if "error" in result:
        raise HTTPException(status_code=503, detail=result["error"])
    return result
