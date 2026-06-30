"""
ML Predictor FastAPI Server

This module exposes REST endpoints for triggering model training and requesting
power predictions for specific nodes or workloads.
"""

import logging
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, HTTPException, Query
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel, Field

from ..power_predictor.predictor import PowerPredictor
from ..data_pipeline.analyzer import analyze_workloads, get_node_summary

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

app = FastAPI(
    title="kcloud-opt-predictor",
    description="AI반도체(GPU) 전력 소비 예측 및 운영 비용 산출 서비스",
    version="2.0.0",
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

_predictor = PowerPredictor()

class PredictRequest(BaseModel):
    
    avg_gpu_util: float       = Field(...,  example=72.5,  description="GPU SM 이용률 (%)")
    avg_mem_util: float       = Field(...,  example=45.0,  description="메모리 대역폭 이용률 (%)")
    avg_mem_used_mb: float    = Field(...,  example=8192.0, description="VRAM 사용량 (MB)")
    avg_temp_c: float         = Field(...,  example=68.0,  description="GPU 온도 (°C)")
    avg_sm_clock_mhz: float   = Field(...,  example=1740.0, description="SM 클럭 (MHz)")
    batch_size: int           = Field(1,    description="배치 크기")
    model_params_b: float     = Field(8.0,  description="모델 파라미터 수 (십억 단위)")
    precision_enc: int        = Field(1,    description="정밀도 인코딩 (fp32=0, fp16=1, int8=2, bf16=3)")
    model_domain: int         = Field(1,    description="워크로드 도메인 (0=Vision, 1=LLM, 2=NLP)")
    dcgm_mem_copy_util: float = Field(0.0,  description="DCGM 메모리 복사 이용률 (%)")
    kepler_cpu_pkg_w: float   = Field(0.0,  description="CPU 패키지 전력 (W)")
    kepler_dram_w: float      = Field(0.0,  description="DRAM 전력 (W)")
    tdp_watt: float           = Field(300.0, description="GPU TDP (W)")
    duration_hours: float     = Field(1.0,  description="가동 시간 (시간) — 비용 계산 전용")

class PowerResponse(BaseModel):
    predicted_power_watts: float
    status: str = "success"

class CostResponse(BaseModel):
    predicted_cost_usd: float
    predicted_power_watts: float
    duration_hours: float

@app.get("/healthz")
def liveness_check():
    return {"status": "ok"}

@app.get("/health")
def health_check():
    return {
        "status": "healthy",
        "service": "ml-power-predictor",
        "model_trained": _predictor.is_trained,
        "training_meta": _predictor.training_meta,
    }

@app.post("/api/v1/predict/power", response_model=PowerResponse)
def predict_power(req: PredictRequest):
    
    features = req.model_dump(exclude={"duration_hours"})
    power = _predictor.predict_power(features)
    if power is None:
        raise HTTPException(status_code=503, detail="모델이 준비되지 않았습니다. /api/v1/models/train_csv 로 먼저 학습하세요.")
    return PowerResponse(predicted_power_watts=power)

@app.post("/api/v1/predict/cost", response_model=CostResponse)
def predict_cost(req: PredictRequest):
    
    duration = req.duration_hours
    features = req.model_dump(exclude={"duration_hours"})
    power = _predictor.predict_power(features)
    cost  = _predictor.predict_cost(features, duration)
    if power is None or cost is None:
        raise HTTPException(status_code=503, detail="모델이 준비되지 않았습니다.")
    return CostResponse(
        predicted_cost_usd=cost,
        predicted_power_watts=power,
        duration_hours=duration,
    )

@app.get("/api/v1/models")
def list_models():
    return {
        "models": [{
            "id": "power_predictor_xgboost",
            "type": "gradient_boosting",
            "is_active": _predictor.is_trained,
            "features": _predictor.feature_cols,
            "training_meta": _predictor.training_meta,
        }]
    }

@app.post("/api/v1/models/train_csv")
def train_from_csv(csv_path: Optional[str] = Query(None, description="CSV 파일명 (예: data.csv)")):
    
    if csv_path:
        # Prevent path traversal by allowing only filenames, no directories or ..
        if os.path.isabs(csv_path) or ".." in csv_path or "/" in csv_path:
            raise HTTPException(status_code=400, detail="유효하지 않은 파일 경로입니다. 파일명만 입력하세요.")
        
        path = Path(__file__).parents[1] / "data_pipeline" / csv_path
    else:
        path = None
        
    result = _predictor.train_from_csv(path)
    if "error" in result:
        raise HTTPException(status_code=422, detail=result["error"])
    return result

@app.post("/api/v1/models/retrain")
def retrain_model(interval: str = Query("7 days", description="학습 기간 (DB 불가 시 CSV fallback)")):
    
    result = _predictor.train(interval)
    if "error" in result:
        raise HTTPException(status_code=503, detail=result["error"])
    return result

@app.get("/api/v1/analysis/workloads")
def get_workload_analysis(interval: str = Query("1 hour", description="분석 기간")):
    profiles = analyze_workloads(interval)
    if not profiles:
        raise HTTPException(status_code=404, detail="해당 기간의 데이터가 없습니다.")
    return {"profiles": profiles, "count": len(profiles)}

@app.get("/api/v1/analysis/nodes")
def get_node_analysis(interval: str = Query("1 hour", description="집계 기간")):
    summary = get_node_summary(interval)
    if not summary:
        raise HTTPException(status_code=404, detail="해당 기간의 노드 데이터가 없습니다.")
    return {"nodes": summary}
