"""
Cost Prediction Model

This module calculates the estimated financial cost of workloads based on
hardware specifications, power usage, and current pricing tiers.
"""

import logging
import os
import requests
from typing import Optional

logger = logging.getLogger(__name__)

PREDICTOR_URL = os.getenv("PREDICTOR_URL", "http://ml-power-predictor:8003")
ELECTRICITY_RATE_USD_KWH = float(os.getenv("ELECTRICITY_RATE_USD_KWH", "0.12"))

class CostPredictor:
    

    def __init__(self):
        self._is_trained = False
        self._check_remote_health()

    def _check_remote_health(self):
        try:
            resp = requests.get(f"{PREDICTOR_URL}/health", timeout=3)
            if resp.status_code == 200:
                self._is_trained = resp.json().get("model_trained", False)
        except requests.RequestException:
            logger.warning(f"ml-power-predictor 연결 실패: {PREDICTOR_URL}")

    @property
    def is_trained(self) -> bool:
        self._check_remote_health()
        return self._is_trained

    def train(self, interval: str = "7 days") -> dict:
        
        try:
            resp = requests.post(f"{PREDICTOR_URL}/api/v1/models/retrain", params={"interval": interval}, timeout=60)
            resp.raise_for_status()
            self._is_trained = True
            return resp.json()
        except requests.RequestException as e:
            logger.error(f"ml-power-predictor 학습 트리거 실패: {e}")
            return {"error": str(e)}

    def predict_power(self, features: dict) -> Optional[float]:
        
        try:
            resp = requests.post(
                f"{PREDICTOR_URL}/api/v1/predict/power",
                json=features,
                timeout=5
            )
            resp.raise_for_status()
            return resp.json().get("predicted_power_watts")
        except requests.RequestException as e:
            logger.warning(f"ml-power-predictor predict_power 실패: {e}")
            return None

    def predict_cost(self, features: dict, duration_hours: float = 1.0) -> Optional[float]:
        
        features_copy = features.copy()
        features_copy["duration_hours"] = duration_hours
        try:
            resp = requests.post(
                f"{PREDICTOR_URL}/api/v1/predict/cost",
                json=features_copy,
                timeout=5
            )
            resp.raise_for_status()
            return resp.json().get("predicted_cost_usd")
        except requests.RequestException as e:
            logger.warning(f"ml-power-predictor predict_cost 실패: {e}")
            return None
