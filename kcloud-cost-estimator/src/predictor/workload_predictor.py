"""
Workload prediction using ARIMA time series model

This module implements Step 1 of the energy prediction framework:
predicting container CPU utilization based on historical patterns.
"""

import logging
from typing import List, Optional, Tuple
import numpy as np
from datetime import datetime, timedelta

from .models import WorkloadPrediction, HistoricalData

logger = logging.getLogger(__name__)


class WorkloadPredictor:
    """
    ARIMA-based workload predictor for container CPU utilization

    This predictor uses Autoregressive Integrated Moving Average (ARIMA)
    model to forecast future container CPU usage based on historical patterns.
    Supports both static and periodic workload patterns.

    Reference:
    - Section 3.3, Step 1 of Alzamil & Djemame (2017)
    - Uses seasonal ARIMA for periodic patterns
    """

    def __init__(self, auto_select: bool = True):
        """
        Initialize workload predictor

        Args:
            auto_select: If True, automatically select best ARIMA parameters
                        using AIC/BIC criteria (equivalent to R's auto.arima)
        """
        self.auto_select = auto_select
        self.model = None
        self._check_dependencies()

    def _check_dependencies(self):
        """Check if required libraries are available"""
        try:
            import statsmodels.api as sm
            self.statsmodels = sm
        except ImportError:
            logger.warning(
                "statsmodels not installed. "
                "Install with: pip install statsmodels"
            )
            self.statsmodels = None

    def predict(
        self,
        historical_data: HistoricalData,
        horizon_minutes: int = 30,
        container_name: str = "",
        pod_name: str = "",
        namespace: str = "",
    ) -> WorkloadPrediction:
        """
        Predict future container CPU utilization

        Args:
            historical_data: Historical CPU utilization time series
            horizon_minutes: Prediction horizon in minutes
            container_name: Container identifier
            pod_name: Pod identifier
            namespace: Kubernetes namespace

        Returns:
            WorkloadPrediction: Predicted workload with confidence intervals

        Raises:
            ValueError: If historical data is insufficient
        """
        if len(historical_data) < 3:
            raise ValueError(
                "Insufficient historical data. "
                "Need at least 3 data points for ARIMA prediction."
            )

        if self.statsmodels is None:
            logger.warning("Using simple moving average fallback")
            return self._simple_prediction(
                historical_data, horizon_minutes,
                container_name, pod_name, namespace
            )

        # Apply ARIMA prediction
        predicted_values, confidence_interval, metrics = self._arima_predict(
            historical_data.values,
            horizon_minutes
        )

        # Take average of predicted values for the horizon
        predicted_cpu_cores = float(np.mean(predicted_values))

        return WorkloadPrediction(
            container_name=container_name,
            pod_name=pod_name,
            namespace=namespace,
            predicted_cpu_cores=predicted_cpu_cores,
            prediction_timestamp=datetime.utcnow(),
            confidence_interval=confidence_interval,
            accuracy_metrics=metrics,
        )

    def _arima_predict(
        self,
        values: List[float],
        horizon_minutes: int,
    ) -> Tuple[np.ndarray, Optional[Tuple], dict]:
        """
        Apply ARIMA model for prediction

        Args:
            values: Historical CPU utilization values (in cores)
            horizon_minutes: Prediction horizon

        Returns:
            Tuple of (predicted_values, confidence_interval, metrics)
        """
        try:
            from statsmodels.tsa.statespace.sarimax import SARIMAX
            from statsmodels.tsa.stattools import adfuller
        except ImportError:
            raise ImportError("statsmodels required for ARIMA prediction")

        # Convert to numpy array
        ts_data = np.array(values)

        # Check stationarity
        adf_result = adfuller(ts_data)
        is_stationary = adf_result[1] < 0.05  # p-value threshold

        # Auto-select ARIMA parameters
        if self.auto_select:
            order, seasonal_order = self._auto_select_params(
                ts_data, is_stationary
            )
        else:
            # Default parameters for non-seasonal ARIMA
            order = (1, 1, 1) if not is_stationary else (1, 0, 1)
            seasonal_order = (0, 0, 0, 0)

        # Fit ARIMA model
        model = SARIMAX(
            ts_data,
            order=order,
            seasonal_order=seasonal_order,
            enforce_stationarity=False,
            enforce_invertibility=False,
        )

        try:
            fitted_model = model.fit(disp=False)
            self.model = fitted_model

            # Forecast
            forecast_result = fitted_model.forecast(steps=horizon_minutes)
            predicted_values = forecast_result

            # Get confidence intervals if available
            forecast_obj = fitted_model.get_forecast(steps=horizon_minutes)
            conf_int = forecast_obj.conf_int()
            confidence_interval = (
                float(conf_int.iloc[-1, 0]),
                float(conf_int.iloc[-1, 1])
            )

            # Calculate accuracy metrics on training data
            metrics = self._calculate_metrics(fitted_model, ts_data)

            return predicted_values, confidence_interval, metrics

        except Exception as e:
            logger.error(f"ARIMA fitting failed: {e}")
            # Fallback to last value
            return np.array([ts_data[-1]] * horizon_minutes), None, {}

    def _auto_select_params(
        self,
        data: np.ndarray,
        is_stationary: bool
    ) -> Tuple[Tuple, Tuple]:
        """
        Automatically select best ARIMA parameters

        Simplified version of R's auto.arima using AIC criterion.

        Args:
            data: Time series data
            is_stationary: Whether data is stationary

        Returns:
            Tuple of (order, seasonal_order)
        """
        from statsmodels.tsa.statespace.sarimax import SARIMAX

        best_aic = np.inf
        best_order = (1, 1, 1)
        best_seasonal = (0, 0, 0, 0)

        # Search space
        p_range = range(0, 3)
        d_range = [0] if is_stationary else [1]
        q_range = range(0, 3)

        for p in p_range:
            for d in d_range:
                for q in q_range:
                    try:
                        model = SARIMAX(
                            data,
                            order=(p, d, q),
                            enforce_stationarity=False,
                            enforce_invertibility=False,
                        )
                        fitted = model.fit(disp=False)

                        if fitted.aic < best_aic:
                            best_aic = fitted.aic
                            best_order = (p, d, q)

                    except Exception:
                        continue

        logger.info(
            f"Auto-selected ARIMA order: {best_order}, AIC: {best_aic:.2f}"
        )

        return best_order, best_seasonal

    def _calculate_metrics(self, fitted_model, actual_data: np.ndarray) -> dict:
        """Calculate prediction accuracy metrics"""
        predictions = fitted_model.fittedvalues
        residuals = actual_data - predictions

        # Align lengths
        min_len = min(len(actual_data), len(residuals))
        actual = actual_data[-min_len:]
        resid = residuals[-min_len:]

        metrics = {
            "mae": float(np.mean(np.abs(resid))),
            "rmse": float(np.sqrt(np.mean(resid ** 2))),
            "mape": float(np.mean(np.abs(resid / (actual + 1e-10))) * 100),
        }

        return metrics

    def _simple_prediction(
        self,
        historical_data: HistoricalData,
        horizon_minutes: int,
        container_name: str,
        pod_name: str,
        namespace: str,
    ) -> WorkloadPrediction:
        """
        Fallback simple prediction using moving average

        Used when ARIMA dependencies are not available.
        """
        # Simple moving average of last 3 values
        recent_values = historical_data.values[-3:]
        predicted_cpu_cores = float(np.mean(recent_values))

        logger.info(
            f"Using simple moving average prediction: {predicted_cpu_cores:.3f} cores"
        )

        return WorkloadPrediction(
            container_name=container_name,
            pod_name=pod_name,
            namespace=namespace,
            predicted_cpu_cores=predicted_cpu_cores,
            prediction_timestamp=datetime.utcnow(),
            confidence_interval=None,
            accuracy_metrics={"method": "simple_moving_average"},
        )
