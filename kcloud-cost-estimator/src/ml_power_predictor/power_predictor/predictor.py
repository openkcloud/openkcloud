"""
XGBoost Power Prediction Model

This module wraps the XGBoost regressor, providing training, inference, and
model persistence capabilities for estimating node power usage.
"""

import logging
import pickle
from pathlib import Path
from typing import Optional

import os

import numpy as np
import pandas as pd
from sklearn.model_selection import train_test_split
from sklearn.metrics import mean_absolute_error, r2_score, mean_squared_error

try:
    import xgboost as xgb
    XGB_AVAILABLE = True
except ImportError:
    XGB_AVAILABLE = False

logger = logging.getLogger(__name__)

ELECTRICITY_RATE = float(os.getenv("ELECTRICITY_RATE_USD_KWH", "0.12"))

FEATURE_COLS = [
    "avg_gpu_util",       
    "avg_mem_util",       
    "avg_mem_used_mb",    
    "avg_temp_c",         
    "avg_sm_clock_mhz",   
    "batch_size",         
    "model_params_b",     
    "precision_enc",      
    "model_domain",       
    "dcgm_mem_copy_util", 
    "kepler_cpu_pkg_w",   
    "kepler_dram_w",      
    "tdp_watt",           
]
TARGET_COL = "avg_power_watt"

DEFAULT_CSV  = Path(__file__).parents[1] / "data_pipeline" / "perf_catalog_final.csv"
MODEL_FILE   = Path(__file__).parent / "trained_models" / "power_predictor.pkl"

class PowerPredictor:
    

    def __init__(self):
        self.model = None
        self.is_trained = False
        self.feature_cols = list(FEATURE_COLS)
        self.training_meta: dict = {}

    def _prepare_features(self, df: pd.DataFrame) -> pd.DataFrame:
        X = df.copy()
        X["batch_size"] = np.log1p(X["batch_size"].fillna(1).astype(float))
        for col in self.feature_cols:
            if col in X.columns:
                X[col] = X[col].fillna(0.0).astype(float)
        return X

    def _train_on_df(self, df: pd.DataFrame, source_label: str = "df") -> dict:
        
        if not XGB_AVAILABLE:
            return {"error": "xgboost 미설치"}

        df = df.dropna(subset=[TARGET_COL])
        if len(df) < 50:
            return {"error": f"학습 데이터 부족: {len(df)}행 (최소 50행 필요)"}

        X = self._prepare_features(df)
        available = [c for c in self.feature_cols if c in X.columns]
        X_input = X[available].fillna(0)
        y = df[TARGET_COL].astype(float)

        X_train, X_test, y_train, y_test = train_test_split(
            X_input, y, test_size=0.2, random_state=42
        )

        self.model = xgb.XGBRegressor(
            n_estimators=500,
            max_depth=6,
            learning_rate=0.05,
            subsample=0.8,
            colsample_bytree=0.8,
            min_child_weight=3,
            random_state=42,
            verbosity=0,
            early_stopping_rounds=30,
        )
        self.model.fit(
            X_train, y_train,
            eval_set=[(X_test, y_test)],
            verbose=False,
        )

        y_pred = self.model.predict(X_test)
        mae  = float(mean_absolute_error(y_test, y_pred))
        r2   = float(r2_score(y_test, y_pred))
        rmse = float(np.sqrt(mean_squared_error(y_test, y_pred)))

        self.feature_cols  = available
        self.is_trained    = True
        self.training_meta = {
            "source":       source_label,
            "n_samples":    len(df),
            "n_features":   len(available),
            "features":     available,
            "mae_watts":    round(mae, 2),
            "rmse_watts":   round(rmse, 2),
            "r2_score":     round(r2, 4),
            "model_dist":   df["model_name"].value_counts().to_dict()
                            if "model_name" in df.columns else {},
        }
        self._save()

        logger.info(
            f"학습 완료 [{source_label}]: {len(df)}행, "
            f"MAE={mae:.2f}W, RMSE={rmse:.2f}W, R²={r2:.4f}"
        )
        return {"status": "success", **self.training_meta}

    def train_from_csv(self, csv_path: Optional[Path] = None) -> dict:
        
        path = Path(csv_path) if csv_path else DEFAULT_CSV
        if not path.exists():
            return {"error": f"CSV 없음: {path}"}

        df = pd.read_csv(path)
        logger.info(f"CSV 로드: {path.name} ({len(df)}행)")
        return self._train_on_df(df, source_label=path.name)

    def train(self, interval: str = "7 days") -> dict:
        
        try:
            from ..data_pipeline.fetcher import fetch_raw
            df = fetch_raw(interval)
            if df.empty or len(df) < 50:
                raise ValueError(f"DB 데이터 부족: {len(df)}행")

            COL_MAP = {
                "gpu_util_pct":      "avg_gpu_util",
                "memory_used_mb":    "avg_mem_used_mb",
                "temperature_c":     "avg_temp_c",
                "sm_clock_mhz":      "avg_sm_clock_mhz",
                "power_watts":       TARGET_COL,
                "mem_copy_util_pct": "dcgm_mem_copy_util",
                "cpu_pkg_watts":     "kepler_cpu_pkg_w",
                "dram_watts":        "kepler_dram_w",
            }
            df = df.rename(columns=COL_MAP)
            if "avg_mem_util" not in df.columns:
                df["avg_mem_util"] = df.get("avg_mem_used_mb", 0) / 49140.0 * 100
            for col, val in [
                ("precision_enc", 1), ("model_domain", 0),
                ("model_params_b", 0.0), ("tdp_watt", 300.0),
            ]:
                if col not in df.columns:
                    df[col] = val

            return self._train_on_df(df, source_label=f"timescaledb/{interval}")

        except Exception as e:
            logger.warning(f"DB 학습 불가 ({e}), CSV fallback")
            return self.train_from_csv()

    def predict_power(self, features: dict) -> Optional[float]:
        
        if not self.is_trained:
            self._load()
        if self.model is None:
            return None

        row = pd.DataFrame([features])
        X   = self._prepare_features(row)
        available = [c for c in self.feature_cols if c in X.columns]
        pred = float(self.model.predict(X[available].fillna(0))[0])
        return round(max(pred, 0.0), 2)

    def predict_cost(
        self,
        features: dict,
        duration_hours: float = 1.0,
        rate_usd_kwh: float = 0.12,
    ) -> Optional[float]:
        
        power_w = self.predict_power(features)
        if power_w is None:
            return None
        kwh = (power_w / 1000.0) * duration_hours
        return round(kwh * rate_usd_kwh, 6)

    def _save(self):
        MODEL_FILE.parent.mkdir(parents=True, exist_ok=True)
        with open(MODEL_FILE, "wb") as f:
            pickle.dump({
                "model":         self.model,
                "feature_cols":  self.feature_cols,
                "training_meta": self.training_meta,
            }, f)

    def _load(self):
        if not MODEL_FILE.exists():
            return
        try:
            with open(MODEL_FILE, "rb") as f:
                data = pickle.load(f)
            self.model         = data["model"]
            self.feature_cols  = data.get("feature_cols", FEATURE_COLS)
            self.training_meta = data.get("training_meta", {})
            self.is_trained    = True
            logger.info("기존 학습 모델 로드 완료")
        except Exception as e:
            logger.error(f"모델 로드 실패: {e}")
