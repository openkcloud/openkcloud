"""
Pod 추적(resource map) 응답 스키마.

Pod에서 워크로드, 가속기, K8s 노드, VM, 물리서버까지 이어지는 경로.
"""
from typing import Optional

from pydantic import BaseModel, Field

from app.schemas._common import DESC_OBSERVED_AT, DESC_WARNINGS, _now


class TraceHop(BaseModel):
    """Pod 추적 경로의 단계 하나."""

    layer: str = Field(..., description="단계 이름 (pod | workload | accelerator | k8s_node | vm | physical_server)")
    status: str = Field(..., description="단계 확인 결과 (ok | none | skipped | unavailable). ok: 찾음, none: 해당 자원 없음, skipped: 거치지 않는 단계, unavailable: 데이터 없어 확인 불가")
    cluster: Optional[str] = Field(None, description="소속 클러스터 이름 (Pod, 워크로드, K8s 노드 단계만)")
    name: Optional[str] = Field(None, description="자원 이름")
    id: Optional[str] = Field(None, description="자원 고유 ID (Pod uid, 노드 system_uuid, VM uuid, 가속기 UUID)")
    state: Optional[str] = Field(None, description="자원 자체 상태 (예: Running, Ready, ACTIVE)")
    project: Optional[str] = Field(None, description="VM이 속한 OpenStack 프로젝트 ID (VM 단계만)")
    via: Optional[str] = Field(None, description="이 단계를 찾을 때 쓴 메트릭과 라벨")
    href: Optional[str] = Field(None, description="이 자원의 상세 API 경로")


class PodCandidate(BaseModel):
    """같은 이름 Pod 후보 하나."""

    cluster: str = Field(..., description="Pod가 있는 클러스터 이름")
    namespace: str = Field(..., description="Pod가 있는 네임스페이스")
    href: str = Field(..., description="이 후보로 다시 추적하는 API 경로")


class PodTraceResponse(BaseModel):
    """Pod 자원 추적 응답."""

    status: str = Field(..., description="추적 결과 (success | partial | ambiguous). partial: 일부 단계 데이터 없음, ambiguous: 같은 이름 Pod 여러 개")
    pod: str = Field(..., description="입력한 Pod 이름")
    cluster: Optional[str] = Field(None, description="Pod가 확인된 클러스터. 후보가 여러 개면 null")
    namespace: Optional[str] = Field(None, description="Pod가 확인된 네임스페이스. 후보가 여러 개면 null")
    path: list[TraceHop] = Field(default_factory=list, description="Pod에서 물리서버까지 순서대로 나열한 단계")
    candidates: list[PodCandidate] = Field(default_factory=list, description="같은 이름 Pod가 여러 개일 때 후보 목록")
    warnings: list[str] = Field(default_factory=list, description=DESC_WARNINGS)
    observed_at: str = Field(default_factory=_now, description=DESC_OBSERVED_AT)
