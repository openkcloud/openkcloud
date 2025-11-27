"""
Collector Module Main Application
power data collection and cost conversion API server
"""

from fastapi import FastAPI, HTTPException, BackgroundTasks
from fastapi.middleware.cors import CORSMiddleware
import uvicorn
from typing import Optional, Dict, Any
import uuid
from datetime import datetime, timedelta
import logging

from .power_client import PowerClient
from .power_metrics import PowerCalculator
from .data_processor import DataProcessor
from .config.settings import get_settings
from .predictor import (
    EnergyPredictor,
    CalibrationTool,
    HistoricalData,
)

# Logging configuration (initial default, refined after loading settings)
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# FastAPI app initialization
app = FastAPI(
    title="kcloud-collector",
    description="power data collection and cost conversion API",
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc"
)

# Load settings
settings = get_settings()

# Refine logging level from settings
try:
    logger.setLevel(getattr(logging, settings.log_level.upper(), logging.INFO))
except Exception:
    logger.setLevel(logging.INFO)

# CORS configuration (from settings)
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_allow_origins,
    allow_credentials=True,
    allow_methods=settings.cors_allow_methods,
    allow_headers=settings.cors_allow_headers,
)

# Request ID middleware
@app.middleware("http")
async def add_request_id_header(request, call_next):
    request_id = request.headers.get("X-Request-ID") or str(uuid.uuid4())
    response = await call_next(request)
    response.headers["X-Request-ID"] = request_id
    return response

# Global instances
power_client = None
power_calculator = None
data_processor = None
energy_predictor = None
calibration_tool = None

@app.on_event("startup")
async def startup_event():
    """Initialize on application startup"""
    global power_client, power_calculator, data_processor, energy_predictor, calibration_tool

    logger.info("Starting Collector module...")
    
    try:
        # Initialize power client
        power_client = PowerClient(
            prometheus_url=settings.power_prometheus_url,
            metrics_interval=settings.power_metrics_interval
        )
        
        # Initialize power calculator
        power_calculator = PowerCalculator(
            electricity_rate=settings.electricity_rate,
            cooling_factor=settings.cooling_factor,
            carbon_rate=settings.carbon_rate
        )
        
        # Initialize data processor
        data_processor = DataProcessor(
            redis_url=settings.redis_url,
            influxdb_url=settings.influxdb_url,
            influxdb_bucket=settings.influxdb_bucket
        )
        
        # Test power connection
        await power_client.health_check()
        logger.info("connection successful")

        # Initialize energy predictor
        energy_predictor = EnergyPredictor()
        logger.info("Energy predictor initialized")

        # Initialize calibration tool
        calibration_tool = CalibrationTool()
        logger.info("Calibration tool initialized")

        logger.info("Collector module initialization completed")
        
    except Exception as e:
        logger.error(f"Initialization failed: {e}")
        raise

@app.get("/")
async def root():
    """Root endpoint"""
    return {
        "service": "kcloud-collector",
        "version": "1.0.0",
        "description": "Power data collection and cost conversion",
        "status": "running"
    }


@app.get("/info")
async def info():
    """Service info endpoint"""
    return {
        "service": "kcloud-collector",
        "version": "1.0.0",
        "config": {
            "prometheus_url": settings.power_prometheus_url,
            "metrics_interval": settings.power_metrics_interval,
            "redis_url": settings.redis_url,
            "influxdb_url": settings.influxdb_url,
            "influxdb_bucket": settings.influxdb_bucket,
            "log_level": settings.log_level,
            "cors_allow_origins": settings.cors_allow_origins,
        }
    }

@app.get("/health")
async def health_check():
    """Health check"""
    try:
        # Guard against uninitialized components
        if power_client is None or data_processor is None:
            return {
                "status": "starting",
                "timestamp": datetime.utcnow().isoformat(),
                "services": {
                    "power": False if power_client is None else "unknown",
                    "database": False if data_processor is None else "unknown",
                }
            }

        # Check power connection
        power_status = await power_client.health_check()

        # Check database connection
        db_status = await data_processor.health_check()
        
        return {
            "status": "healthy",
            "timestamp": datetime.utcnow().isoformat(),
            "services": {
                "power": power_status,
                "database": db_status
            }
        }
    except Exception as e:
        raise HTTPException(status_code=503, detail=f"Health check failed: {str(e)}")


@app.get("/ready")
async def readiness():
    if power_client is None or data_processor is None:
        raise HTTPException(status_code=503, detail="Service starting: dependencies not ready")
    return {"status": "ready", "timestamp": datetime.utcnow().isoformat()}


@app.get("/live")
async def liveness():
    return {"status": "alive", "timestamp": datetime.utcnow().isoformat()}

# =============================================================================
# Power data collection API
# =============================================================================

@app.get("/power/current")
async def get_current_power(
    namespace: Optional[str] = None,
    workload: Optional[str] = None,
    time_range: str = "5m"
):
    """Query real-time power data"""
    try:
        if power_client is None:
            raise HTTPException(status_code=503, detail="Service starting: power client not ready")
        power_data = await power_client.get_container_power_metrics(
            namespace=namespace,
            workload=workload,
            time_range=time_range
        )
        
        return {
            "data": power_data,
            "timestamp": datetime.utcnow().isoformat(),
            "time_range": time_range
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Power data query failed: {str(e)}")

@app.get("/power/containers")
async def get_container_power(
    namespace: Optional[str] = None,
    limit: int = 100
):
    """Query power data by container"""
    try:
        if power_client is None:
            raise HTTPException(status_code=503, detail="Service starting: power client not ready")
        containers = await power_client.get_all_container_metrics(
            namespace=namespace,
            limit=limit
        )
        
        return {
            "containers": containers,
            "count": len(containers),
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Container power data query failed: {str(e)}")

@app.get("/power/nodes")
async def get_node_power():
    """Query power data by node"""
    try:
        if power_client is None:
            raise HTTPException(status_code=503, detail="Service starting: power client not ready")
        nodes = await power_client.get_node_power_metrics()
        
        return {
            "nodes": nodes,
            "count": len(nodes),
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Node power data query failed: {str(e)}")

# =============================================================================
# Cost calculation API
# =============================================================================

@app.get("/cost/current")
async def get_current_cost(
    namespace: Optional[str] = None,
    workload: Optional[str] = None
):
    """Calculate current operation costs"""
    try:
        if power_client is None or power_calculator is None:
            raise HTTPException(status_code=503, detail="Service starting: dependencies not ready")
        # Collect power data
        power_data = await power_client.get_container_power_metrics(
            namespace=namespace,
            workload=workload,
            time_range="1h"
        )
        
        # Calculate costs
        cost_data = power_calculator.calculate_total_cost(power_data)
        
        return {
            "cost": cost_data,
            "timestamp": datetime.utcnow().isoformat(),
            "period": "1 hour"
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Cost calculation failed: {str(e)}")

@app.get("/cost/hourly")
async def get_hourly_cost(
    start: Optional[datetime] = None,
    end: Optional[datetime] = None,
    namespace: Optional[str] = None
):
    """Hourly cost analysis"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        if not start:
            start = datetime.utcnow() - timedelta(hours=24)
        if not end:
            end = datetime.utcnow()
            
        cost_data = await data_processor.get_hourly_cost_analysis(
            start_time=start,
            end_time=end,
            namespace=namespace
        )
        
        return {
            "cost_analysis": cost_data,
            "period": f"{start.isoformat()} to {end.isoformat()}",
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Hourly cost analysis failed: {str(e)}")

@app.get("/cost/workload/{workload_id}")
async def get_workload_cost(workload_id: str):
    """Detailed cost analysis by workload"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        cost_breakdown = await data_processor.get_workload_cost_breakdown(workload_id)
        
        return {
            "workload_id": workload_id,
            "cost_breakdown": cost_breakdown,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Workload cost analysis failed: {str(e)}")

# =============================================================================
# Power profiling API
# =============================================================================

@app.get("/profile/workload-types")
async def get_workload_types():
    """Power profile by workload type"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        profiles = await data_processor.get_workload_power_profiles()
        
        return {
            "profiles": profiles,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Profile query failed: {str(e)}")

@app.post("/profile/classify")
async def classify_workload(power_data: Dict):
    """Classify workload power patterns"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        classification = await data_processor.classify_workload_pattern(power_data)
        
        return {
            "classification": classification,
            "confidence": classification.get("confidence", 0.0),
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Workload classification failed: {str(e)}")

# =============================================================================
# Background data collection tasks
# =============================================================================

@app.post("/collect/start")
async def start_collection(background_tasks: BackgroundTasks):
    """Start background data collection"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        background_tasks.add_task(data_processor.start_continuous_collection)
        
        return {
            "message": "Data collection started",
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Data collection start failed: {str(e)}")

@app.post("/collect/stop")
async def stop_collection():
    """Stop background data collection"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        await data_processor.stop_continuous_collection()
        
        return {
            "message": "Data collection stopped",
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Data collection stop failed: {str(e)}")

# =============================================================================
# Metrics and statistics
# =============================================================================

@app.get("/metrics/summary")
async def get_metrics_summary():
    """Collection metrics summary"""
    try:
        if data_processor is None:
            raise HTTPException(status_code=503, detail="Service starting: data processor not ready")
        summary = await data_processor.get_collection_summary()
        
        return {
            "summary": summary,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Metrics summary query failed: {str(e)}")

# =============================================================================
# Energy prediction API
# =============================================================================

@app.post("/predict/energy")
async def predict_container_energy(request: Dict[str, Any]):
    """
    Predict future energy consumption for a container

    Request body:
    {
        "container_name": str,
        "pod_name": str,
        "namespace": str,
        "historical_cpu_cores": [float],  # Time series of CPU usage in cores
        "container_cpu_request": float,   # CPU request in cores
        "node_current_util": float,       # Current node CPU utilization %
        "node_idle_util": float,          # Node idle CPU utilization %
        "containers_on_node": [           # Other containers on same node
            {"cpu_request": float, "cpu_util": float}
        ],
        "prediction_horizon_minutes": int  # Optional, default 30
    }
    """
    try:
        if energy_predictor is None:
            raise HTTPException(status_code=503, detail="Service starting: predictor not ready")
        # Parse request
        container_name = request.get("container_name", "")
        pod_name = request.get("pod_name", "")
        namespace = request.get("namespace", "")
        historical_cpu = request.get("historical_cpu_cores", [])
        container_cpu_request = request.get("container_cpu_request", 1.0)
        node_current_util = request.get("node_current_util", 0.0)
        node_idle_util = request.get("node_idle_util", 0.0)
        containers_on_node = request.get("containers_on_node", [])
        horizon_minutes = request.get("prediction_horizon_minutes", 30)

        # Create historical data
        historical_data = HistoricalData(
            timestamps=[datetime.utcnow() - timedelta(minutes=i) for i in range(len(historical_cpu))],
            values=historical_cpu,
            metric_name="cpu_cores"
        )

        # Predict
        prediction = energy_predictor.predict_container_energy(
            container_name=container_name,
            pod_name=pod_name,
            namespace=namespace,
            historical_workload=historical_data,
            container_cpu_request=container_cpu_request,
            node_current_util=node_current_util,
            node_idle_util=node_idle_util,
            containers_on_node=containers_on_node,
            prediction_horizon_minutes=horizon_minutes,
        )

        return {
            "prediction": {
                "container_name": prediction.container_name,
                "pod_name": prediction.pod_name,
                "namespace": prediction.namespace,
                "predicted_power_watts": prediction.predicted_power_watts,
                "prediction_timestamp": prediction.prediction_timestamp.isoformat(),
                "prediction_horizon_minutes": prediction.prediction_horizon_minutes,
                "confidence_interval": prediction.confidence_interval,
            },
            "status": "success"
        }

    except Exception as e:
        logger.error(f"Energy prediction failed: {e}")
        raise HTTPException(status_code=500, detail=f"Energy prediction failed: {str(e)}")


@app.post("/calibrate")
async def calibrate_models(request: Dict[str, Any]):
    """
    Calibrate prediction models using measurement data

    Request body:
    {
        "container_node_data": [
            {"container_cpu_cores": float, "node_cpu_util_percent": float}
        ],
        "node_power_data": [
            {"node_cpu_util_percent": float, "node_power_watts": float}
        ]
    }
    """
    try:
        if calibration_tool is None or energy_predictor is None:
            raise HTTPException(status_code=503, detail="Service starting: calibration not ready")
        container_node_data = request.get("container_node_data", [])
        node_power_data = request.get("node_power_data", [])

        if not container_node_data or not node_power_data:
            raise HTTPException(
                status_code=400,
                detail="Both container_node_data and node_power_data required"
            )

        # Calibrate
        config = calibration_tool.calibrate_from_prometheus_data(
            container_node_data=container_node_data,
            node_power_data=node_power_data
        )

        # Update predictor with new config
        energy_predictor.update_calibration(config)

        return {
            "calibration": {
                "container_to_node_slope": config.container_to_node_slope,
                "container_to_node_intercept": config.container_to_node_intercept,
                "node_util_to_power_slope": config.node_util_to_power_slope,
                "node_util_to_power_intercept": config.node_util_to_power_intercept,
                "node_idle_power_watts": config.node_idle_power_watts,
                "node_max_power_watts": config.node_max_power_watts,
            },
            "status": "calibration_successful"
        }

    except Exception as e:
        logger.error(f"Calibration failed: {e}")
        raise HTTPException(status_code=500, detail=f"Calibration failed: {str(e)}")


@app.get("/calibration/config")
async def get_calibration_config():
    """Get current calibration configuration"""
    try:
        if energy_predictor is None:
            raise HTTPException(status_code=503, detail="Service starting: predictor not ready")
        config = energy_predictor.config

        return {
            "calibration": {
                "container_to_node_slope": config.container_to_node_slope,
                "container_to_node_intercept": config.container_to_node_intercept,
                "node_util_to_power_slope": config.node_util_to_power_slope,
                "node_util_to_power_intercept": config.node_util_to_power_intercept,
                "node_idle_power_watts": config.node_idle_power_watts,
                "node_max_power_watts": config.node_max_power_watts,
            },
            "note": "Using paper defaults from Dell X3430 unless calibrated"
        }

    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to get config: {str(e)}")

if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host="0.0.0.0",
        port=8001,
        reload=True,
        log_level="info"
    )
