"""
Infrastructure Module Main Application
OpenStack Magnum cluster management and Heat template API server
"""
from fastapi import FastAPI, HTTPException, BackgroundTasks, Depends
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
import uvicorn
from typing import Optional, Dict, List, Any
from datetime import datetime
import logging
from .magnum_client import MagnumClient
from .cluster_manager import ClusterManager
from .heat_templates import HeatTemplateManager
from .config.settings import get_settings
from .models import (
    ClusterCreateRequest,
    ClusterScaleRequest,
    ClusterResponse,
    WorkloadRequirements,
    ClusterStatus
)
# Logging configuration
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)
app = FastAPI(
    title="kcloud-infrastructure",
    description="OpenStack Magnum cluster management and Heat template API",
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc"
)
# CORS configuration
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)
# Load settings
settings = get_settings()
# Global instances
magnum_client = None
cluster_manager = None
heat_manager = None
@app.on_event("startup")
async def startup_event():
"""
    """Initialize on application startup"""
    global magnum_client, cluster_manager, heat_manager
    
    logger.info("Starting Infrastructure module...")
    
    try:
        # Initialize OpenStack Magnum client
        magnum_client = MagnumClient(
            auth_url=settings.openstack_auth_url,
            project_name=settings.openstack_project_name,
            username=settings.openstack_username,
            password=settings.openstack_password,
            region_name=settings.openstack_region_name
        )
        
        # Initialize cluster manager
        cluster_manager = ClusterManager(magnum_client=magnum_client)
        
        # Initialize Heat template manager
        heat_manager = HeatTemplateManager(
            auth_url=settings.openstack_auth_url,
            project_name=settings.openstack_project_name,
            username=settings.openstack_username,
            password=settings.openstack_password
        )
        
        # Test OpenStack connection
        await magnum_client.health_check()
        logger.info("OpenStack Magnum connection successful")
        
        # Load cluster templates
        await heat_manager.load_templates()
        logger.info("Heat template loading completed")
        
        logger.info("Infrastructure module initialization completed")
        
    except Exception as e:
        logger.error(f"Initialization failed: {e}")
        raise

@app.get("/")
async def root():
    """Root endpoint"""
    return {
        "service": "kcloud-infrastructure",
        "version": "1.0.0",
        "description": "OpenStack Magnum cluster management",
        "status": "running"
    }

@app.get("/health")
async def health_check():
    """Health check"""
    try:
        # Check OpenStack connection
        openstack_status = await magnum_client.health_check()
        
        # Check Heat service
        heat_status = await heat_manager.health_check()
        
        return {
            "status": "healthy",
            "timestamp": datetime.utcnow().isoformat(),
            "services": {
                "openstack": openstack_status,
                "heat": heat_status
            }
        }
    except Exception as e:
        raise HTTPException(status_code=503, detail=f"Health check failed: {str(e)}")

# =============================================================================
# Cluster management API
# =============================================================================

@app.post("/clusters", response_model=ClusterResponse)
async def create_cluster(
    cluster_request: ClusterCreateRequest,
    background_tasks: BackgroundTasks
):
    """Create new Magnum cluster"""
    try:
        logger.info(f"Cluster creation request: {cluster_request.name}")
        
        # Create cluster (async)
        cluster = await cluster_manager.create_cluster(cluster_request)
        
        # Start cluster status monitoring in background
        background_tasks.add_task(
            cluster_manager.monitor_cluster_creation,
            cluster.id
        )
        
        logger.info(f"Cluster creation started: {cluster.name} ({cluster.id})")
        
        return ClusterResponse(
            id=cluster.id,
            name=cluster.name,
            status=cluster.status,
            node_count=cluster.node_count,
            master_count=cluster.master_count,
            created_at=cluster.created_at,
            labels=cluster.labels
        )
        
    except Exception as e:
        logger.error(f"Cluster creation failed: {e}")
        raise HTTPException(status_code=500, detail=f"Cluster creation failed: {str(e)}")

@app.get("/clusters", response_model=List[ClusterResponse])
async def list_clusters(
    status: Optional[str] = None,
    workload_type: Optional[str] = None,
    limit: int = 50
):
    """Query cluster list"""
    try:
        clusters = await cluster_manager.list_clusters(
            status=status,
            workload_type=workload_type,
            limit=limit
        )
        
        return [
            ClusterResponse(
                id=c.id,
                name=c.name,
                status=c.status,
                node_count=c.node_count,
                master_count=c.master_count,
                created_at=c.created_at,
                labels=c.labels
            ) for c in clusters
        ]
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"cluster 목록 query failed: {str(e)}")

@app.get("/clusters/{cluster_id}", response_model=ClusterResponse)
async def get_cluster(cluster_id: str):
    """try:
        cluster = await cluster_manager.get_cluster(cluster_id)
        
        if not cluster:
        
        return ClusterResponse(
            id=cluster.id,
            name=cluster.name,
            status=cluster.status,
            node_count=cluster.node_count,
            master_count=cluster.master_count,
            created_at=cluster.created_at,
            updated_at=cluster.updated_at,
            labels=cluster.labels,
            api_address=cluster.api_address,
            coe_version=cluster.coe_version
        )
        
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"cluster query failed: {str(e)}")

@app.put("/clusters/{cluster_id}/scale")
async def scale_cluster(
    cluster_id: str,
    scale_request: ClusterScaleRequest,
    background_tasks: BackgroundTasks
):
"""cluster scaling"""try:
        

        result = await cluster_manager.scale_cluster(
            cluster_id=cluster_id,
            node_count=scale_request.node_count
        )
        

        background_tasks.add_task(
            cluster_manager.monitor_cluster_scaling,
            cluster_id,
            scale_request.node_count
        )
        
        return {
            "cluster_id": cluster_id,
            "target_node_count": scale_request.node_count,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"cluster scaling failed: {str(e)}")

@app.delete("/clusters/{cluster_id}")
async def delete_cluster(cluster_id: str):
"""cluster deletion"""try:
        logger.info(f"cluster deletion: {cluster_id}")
        
        await cluster_manager.delete_cluster(cluster_id)
        
        return {
            "cluster_id": cluster_id,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"cluster deletion failed: {str(e)}")

# =============================================================================
# =============================================================================

@app.post("/match/workload")
async def match_workload_to_cluster(workload_req: WorkloadRequirements):
"""
    try:
        suitable_clusters = await cluster_manager.find_suitable_clusters(workload_req)
        if suitable_clusters:
            best_cluster = suitable_clusters[0]
            return {
                "action": "use_existing",
                "cluster": {
                    "id": best_cluster.id,
                    "name": best_cluster.name,
                    "status": best_cluster.status,
                    "available_resources": best_cluster.available_resources
                },
                "alternatives": [
                    {
                        "id": c.id,
                        "name": c.name,
                        "suitability_score": c.suitability_score
                    } for c in suitable_clusters[1:3]
                ]
            }
        else:
            cluster_template = await cluster_manager.recommend_cluster_template(workload_req)
            return {
                "action": "create_new",
                "recommended_template": cluster_template,
                "estimated_cost": cluster_template.get("estimated_cost_per_hour"),
                "estimated_creation_time": "5-10 minutes"
            }
    except Exception as e:
@app.get("/clusters/available")
async def get_available_clusters():
"""
    """try:
        available_clusters = await cluster_manager.get_available_clusters()
        
        return {
            "clusters": [
                {
                    "id": c.id,
                    "name": c.name,
                    "workload_type": c.labels.get("workload_type"),
                    "available_cpu": c.available_resources.get("cpu"),
                    "available_memory": c.available_resources.get("memory"),
                    "available_gpu": c.available_resources.get("gpu"),
                    "utilization": c.utilization_percentage
                } for c in available_clusters
            ],
            "total_count": len(available_clusters),
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:

# =============================================================================
# cluster template API
# =============================================================================

@app.get("/templates")
async def list_cluster_templates():
"""
    try:
        templates = await heat_manager.list_templates()
        return {
            "templates": templates,
            "count": len(templates),
            "timestamp": datetime.utcnow().isoformat()
        }
    except Exception as e:
@app.get("/templates/{template_id}")
async def get_cluster_template(template_id: str):
"""
    """try:
        template = await heat_manager.get_template(template_id)
        
        if not template:
        
        return template
        
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"template query failed: {str(e)}")

# =============================================================================
# =============================================================================

@app.get("/clusters/{cluster_id}/status", response_model=ClusterStatus)
async def get_cluster_status(cluster_id: str):
"""
    try:
        status = await cluster_manager.get_cluster_detailed_status(cluster_id)
        return status
    except Exception as e:
@app.get("/clusters/{cluster_id}/metrics")
async def get_cluster_metrics(cluster_id: str):
"""
    """try:
        metrics = await cluster_manager.get_cluster_metrics(cluster_id)
        
        return {
            "cluster_id": cluster_id,
            "metrics": metrics,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:

@app.get("/clusters/{cluster_id}/costs")
async def get_cluster_costs(cluster_id: str):
"""
    try:
        costs = await cluster_manager.calculate_cluster_costs(cluster_id)
        return {
            "cluster_id": cluster_id,
            "costs": costs,
            "timestamp": datetime.utcnow().isoformat()
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"cluster cost query failed: {str(e)}")
# =============================================================================
# =============================================================================
@app.post("/optimize/clusters")
async def optimize_all_clusters(background_tasks: BackgroundTasks):
"""
    """try:

        background_tasks.add_task(cluster_manager.optimize_all_clusters)
        
        return {
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:

@app.post("/cleanup/idle-clusters")
async def cleanup_idle_clusters():
"""
    try:
        cleaned_up = await cluster_manager.cleanup_idle_clusters()
        
        return {
            "message": f"{len(cleaned_up)}개의 유휴 cluster 정리 완료",
            "cleaned_clusters": cleaned_up,
            "timestamp": datetime.utcnow().isoformat()
        }
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"유휴 cluster 정리 failed: {str(e)}")

if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host="0.0.0.0",
        port=8006,
        reload=True,
        log_level="info"
    )
