#!/usr/bin/env python3
"""
Virtual Cluster REST API
OpenStack 클러스터 CRUD 작업을 위한 FastAPI 엔드포인트
"""

from fastapi import FastAPI, HTTPException, BackgroundTasks, Query, Depends
from fastapi.middleware.cors import CORSMiddleware
from contextlib import asynccontextmanager
from pydantic import BaseModel, Field
from typing import Dict, List, Optional, Any
from datetime import datetime
import uvicorn
import asyncio
import json

from openstack_cluster_crud import (
    OpenStackClusterCRUD,
    ClusterConfig,
    ClusterInfo,
    ClusterStatus
)

# CRUD 컨트롤러 인스턴스 관리
_crud_controller: Optional[OpenStackClusterCRUD] = None


# ============= Lifespan 이벤트 =============
@asynccontextmanager
async def lifespan(app: FastAPI):
    """애플리케이션 생명주기 관리"""
    global _crud_controller
    # 시작 시 초기화
    try:
        _crud_controller = OpenStackClusterCRUD(cloud_name="openstack")
        print("Connected to OpenStack")
    except Exception as e:
        print(f"Failed to connect to OpenStack: {e}")
        raise
    
    yield
    
    # 종료 시 정리
    _crud_controller = None
    print("Shutting down API server")


# FastAPI 앱 생성
app = FastAPI(
    title="Virtual Cluster Management API",
    description="OpenStack Magnum 클러스터 CRUD 작업 API",
    version="1.0.0",
    lifespan=lifespan
)

# CORS 설정
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# ============= Dependency Injection =============
def get_crud_controller() -> OpenStackClusterCRUD:
    """
    CRUD 컨트롤러 의존성 제공
    
    Returns:
        OpenStackClusterCRUD 인스턴스
        
    Raises:
        HTTPException: 컨트롤러가 초기화되지 않은 경우
    """
    global _crud_controller
    if _crud_controller is None:
        raise HTTPException(
            status_code=503,
            detail="OpenStack connection not initialized. Please check server logs."
        )
    return _crud_controller


# ============= Pydantic 모델 =============
class ClusterCreateRequest(BaseModel):
    """클러스터 생성 요청 모델"""
    name: str = Field(..., description="클러스터 이름")
    cluster_template_id: str = Field(..., description="클러스터 템플릿 ID")
    keypair: str = Field(default="ketilinux", description="SSH 키페어")
    master_count: int = Field(default=1, ge=1, le=3, description="마스터 노드 수")
    node_count: int = Field(default=2, ge=1, le=100, description="워커 노드 수")
    master_flavor: Optional[str] = Field(None, description="마스터 노드 플레이버")
    flavor: Optional[str] = Field(None, description="워커 노드 플레이버")
    docker_volume_size: int = Field(default=50, ge=10, description="Docker 볼륨 크기 (GB)")
    labels: Optional[Dict[str, str]] = Field(None, description="클러스터 레이블")
    fixed_network: Optional[str] = Field(None, description="고정 네트워크")
    fixed_subnet: Optional[str] = Field(None, description="고정 서브넷")
    floating_ip_enabled: bool = Field(default=True, description="플로팅 IP 활성화")


class ClusterUpdateRequest(BaseModel):
    """클러스터 업데이트 요청 모델"""
    node_count: Optional[int] = Field(None, ge=1, le=100, description="워커 노드 수")
    max_node_count: Optional[int] = Field(None, ge=1, le=100, description="최대 노드 수")
    min_node_count: Optional[int] = Field(None, ge=1, le=100, description="최소 노드 수")


class ClusterResponse(BaseModel):
    """클러스터 응답 모델"""
    id: str
    name: str
    status: str
    stack_id: str
    master_count: int
    node_count: int
    keypair: str
    cluster_template_id: str
    api_address: Optional[str]
    coe_version: Optional[str]
    created_at: str
    updated_at: Optional[str]
    health_status: Optional[str]
    health_status_reason: Optional[str]
    project_id: str
    user_id: str
    node_addresses: List[str]
    master_addresses: List[str]


class TemplateResponse(BaseModel):
    """클러스터 템플릿 응답 모델"""
    id: str
    name: str
    coe: str
    image_id: Optional[str] = None
    flavor_id: Optional[str] = None
    master_flavor_id: Optional[str] = None
    keypair_id: Optional[str] = None
    public: bool = False
    created_at: Optional[str] = None


class StatusResponse(BaseModel):
    """상태 응답 모델"""
    status: str
    message: str
    timestamp: str


class ErrorResponse(BaseModel):
    """에러 응답 모델"""
    error: str
    detail: str
    timestamp: str


# ============= Health Check =============
@app.get("/", response_model=StatusResponse)
async def root():
    """헬스 체크 엔드포인트"""
    return StatusResponse(
        status="healthy",
        message="Virtual Cluster Management API is running",
        timestamp=datetime.now().isoformat()
    )


@app.get("/health", response_model=StatusResponse)
async def health_check(crud: OpenStackClusterCRUD = Depends(get_crud_controller)):
    """상세 헬스 체크"""
    try:
        # OpenStack 연결 확인
        templates = crud.get_cluster_templates()
        return StatusResponse(
            status="healthy",
            message=f"Connected to OpenStack, {len(templates)} templates available",
            timestamp=datetime.now().isoformat()
        )
    except Exception as e:
        raise HTTPException(status_code=503, detail=str(e))


# ============= Template Endpoints =============
@app.get("/api/v1/templates", response_model=List[TemplateResponse])
async def list_templates(crud: OpenStackClusterCRUD = Depends(get_crud_controller)):
    """사용 가능한 클러스터 템플릿 목록 조회"""
    try:
        templates = crud.get_cluster_templates()
        return templates
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


# ============= Cluster CRUD Endpoints =============
@app.post("/api/v1/clusters", response_model=ClusterResponse, status_code=201)
async def create_cluster(
    request: ClusterCreateRequest,
    background_tasks: BackgroundTasks,
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """새 클러스터 생성"""
    try:
        config = ClusterConfig(
            name=request.name,
            cluster_template_id=request.cluster_template_id,
            keypair=request.keypair,
            master_count=request.master_count,
            node_count=request.node_count,
            master_flavor=request.master_flavor,
            flavor=request.flavor,
            docker_volume_size=request.docker_volume_size,
            labels=request.labels,
            fixed_network=request.fixed_network,
            fixed_subnet=request.fixed_subnet,
            floating_ip_enabled=request.floating_ip_enabled
        )
        
        # 비동기로 생성 (오래 걸리므로)
        cluster_info = crud.create_cluster(config)
        
        return ClusterResponse(**cluster_info.__dict__)
        
    except Exception as e:
        raise HTTPException(status_code=400, detail=str(e))


@app.get("/api/v1/clusters", response_model=List[ClusterResponse])
async def list_clusters(
    status: Optional[str] = Query(None, description="필터: 클러스터 상태"),
    name: Optional[str] = Query(None, description="필터: 클러스터 이름"),
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """클러스터 목록 조회"""
    try:
        filters = {}
        if status:
            filters["status"] = status
        if name:
            filters["name"] = name
            
        clusters = crud.list_clusters(filters=filters if filters else None)
        return [ClusterResponse(**cluster.__dict__) for cluster in clusters]
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/api/v1/clusters/{cluster_id}", response_model=ClusterResponse)
async def get_cluster(
    cluster_id: str,
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """특정 클러스터 정보 조회"""
    try:
        cluster = crud.get_cluster(cluster_id=cluster_id)
        return ClusterResponse(**cluster.__dict__)
        
    except Exception as e:
        if "not found" in str(e).lower():
            raise HTTPException(status_code=404, detail=f"Cluster not found: {cluster_id}")
        raise HTTPException(status_code=500, detail=str(e))


@app.patch("/api/v1/clusters/{cluster_id}", response_model=ClusterResponse)
async def update_cluster(
    cluster_id: str,
    request: ClusterUpdateRequest,
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """클러스터 업데이트 (노드 수 조정)"""
    try:
        cluster = crud.update_cluster(
            cluster_id=cluster_id,
            node_count=request.node_count,
            max_node_count=request.max_node_count,
            min_node_count=request.min_node_count
        )
        return ClusterResponse(**cluster.__dict__)
        
    except Exception as e:
        if "not found" in str(e).lower():
            raise HTTPException(status_code=404, detail=f"Cluster not found: {cluster_id}")
        raise HTTPException(status_code=400, detail=str(e))


@app.delete("/api/v1/clusters/{cluster_id}", response_model=StatusResponse)
async def delete_cluster(
    cluster_id: str,
    force: bool = Query(False, description="강제 삭제 여부"),
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """클러스터 삭제"""
    try:
        success = crud.delete_cluster(cluster_id, force=force)
        
        if success:
            return StatusResponse(
                status="success",
                message=f"Cluster {cluster_id} deleted successfully",
                timestamp=datetime.now().isoformat()
            )
        else:
            raise HTTPException(status_code=400, detail="Failed to delete cluster")
            
    except Exception as e:
        if "not found" in str(e).lower() and not force:
            raise HTTPException(status_code=404, detail=f"Cluster not found: {cluster_id}")
        raise HTTPException(status_code=500, detail=str(e))


# ============= Cluster Operations =============
@app.post("/api/v1/clusters/{cluster_id}/resize", response_model=ClusterResponse)
async def resize_cluster(
    cluster_id: str,
    node_count: int = Query(..., ge=1, le=100, description="새 노드 수"),
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """클러스터 노드 수 조정 (간편 API)"""
    try:
        cluster = crud.resize_cluster(cluster_id, node_count)
        return ClusterResponse(**cluster.__dict__)
        
    except Exception as e:
        if "not found" in str(e).lower():
            raise HTTPException(status_code=404, detail=f"Cluster not found: {cluster_id}")
        raise HTTPException(status_code=400, detail=str(e))


@app.get("/api/v1/clusters/{cluster_id}/kubeconfig")
async def get_cluster_kubeconfig(
    cluster_id: str,
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """클러스터 kubeconfig 조회"""
    try:
        config = crud.get_cluster_credentials(cluster_id)
        return config
        
    except Exception as e:
        if "not found" in str(e).lower():
            raise HTTPException(status_code=404, detail=f"Cluster not found: {cluster_id}")
        raise HTTPException(status_code=500, detail=str(e))


# ============= Maintenance Operations =============
@app.post("/api/v1/maintenance/cleanup", response_model=StatusResponse)
async def cleanup_stuck_clusters(
    hours: int = Query(24, ge=1, le=168, description="경과 시간 (시간 단위)"),
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """오래된 stuck 클러스터 정리"""
    try:
        deleted = crud.cleanup_stuck_clusters(hours=hours)
        
        return StatusResponse(
            status="success",
            message=f"Cleaned up {len(deleted)} stuck clusters",
            timestamp=datetime.now().isoformat()
        )
        
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


# ============= Batch Operations =============
@app.post("/api/v1/batch/clusters", response_model=List[ClusterResponse])
async def create_multiple_clusters(
    requests: List[ClusterCreateRequest],
    background_tasks: BackgroundTasks,
    crud: OpenStackClusterCRUD = Depends(get_crud_controller)
):
    """여러 클러스터 동시 생성"""
    created_clusters = []
    errors = []
    
    for req in requests:
        try:
            config = ClusterConfig(
                name=req.name,
                cluster_template_id=req.cluster_template_id,
                keypair=req.keypair,
                master_count=req.master_count,
                node_count=req.node_count,
                master_flavor=req.master_flavor,
                flavor=req.flavor,
                docker_volume_size=req.docker_volume_size,
                labels=req.labels,
                fixed_network=req.fixed_network,
                fixed_subnet=req.fixed_subnet,
                floating_ip_enabled=req.floating_ip_enabled
            )
            
            cluster_info = crud.create_cluster(config)
            created_clusters.append(ClusterResponse(**cluster_info.__dict__))
            
        except Exception as e:
            errors.append({"cluster": req.name, "error": str(e)})
    
    if errors:
        raise HTTPException(
            status_code=207,  # Multi-Status
            detail={"created": created_clusters, "errors": errors}
        )
    
    return created_clusters


# ============= Main =============
if __name__ == "__main__":
    uvicorn.run(
        "cluster_api:app",
        host="0.0.0.0",
        port=8000,
        reload=True,
        log_level="info"
    )