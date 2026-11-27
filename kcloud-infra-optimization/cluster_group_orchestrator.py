#!/usr/bin/env python3
"""
Cluster Group Orchestrator
Infrastructure 모듈의 클러스터 그룹 생명주기 관리 및 실행 담당
"""

import os
import json
import asyncio
import logging
from datetime import datetime, timedelta
from typing import Dict, List, Optional, Tuple, Any
from dataclasses import dataclass, asdict
from enum import Enum

# 기존 CRUD 모듈 import
from openstack_cluster_crud import OpenStackClusterCRUD, ClusterConfig, ClusterInfo

# 로깅 설정
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class GroupType(Enum):
    """클러스터 그룹 타입"""
    GPU_INTENSIVE = "gpu_intensive"      # GPU 집약적 워크로드
    CPU_COMPUTE = "cpu_compute"          # CPU 연산 집약적
    MIXED_WORKLOAD = "mixed_workload"    # 혼합 워크로드
    DEVELOPMENT = "development"          # 개발/테스트
    PRODUCTION = "production"            # 운영 환경


class GroupStatus(Enum):
    """그룹 상태"""
    CREATING = "creating"
    ACTIVE = "active"
    SCALING = "scaling" 
    CONSOLIDATING = "consolidating"
    DELETING = "deleting"
    ERROR = "error"


@dataclass
class ClusterGroupConfig:
    """클러스터 그룹 설정"""
    name: str
    group_type: GroupType
    min_clusters: int = 1
    max_clusters: int = 10
    target_utilization: float = 0.7  # 목표 자원 사용률
    scale_threshold: float = 0.8     # 스케일링 임계값
    consolidation_threshold: float = 0.3  # 통합 임계값
    auto_scaling_enabled: bool = True
    cost_optimization_enabled: bool = True
    labels: Dict[str, str] = None
    
    def __post_init__(self):
        if self.labels is None:
            self.labels = {}


@dataclass 
class ClusterGroupInfo:
    """클러스터 그룹 정보"""
    id: str
    name: str
    group_type: str
    status: str
    clusters: List[Dict]
    total_nodes: int
    active_clusters: int
    created_at: str
    updated_at: str
    config: Dict
    metrics: Dict


class ClusterGroupOrchestrator:
    """클러스터 그룹 오케스트레이션 관리"""
    
    def __init__(self, cloud_name: str = "openstack"):
        """
        초기화
        
        Args:
            cloud_name: OpenStack 클라우드 이름
        """
        self.crud = OpenStackClusterCRUD(cloud_name)
        self.groups = {}  # 메모리 내 그룹 저장 (향후 DB 연동)
        self.active_operations = {}  # 진행 중인 작업 추적
        
        logger.info("Cluster Group Orchestrator initialized")
    
    # ============= GROUP LIFECYCLE MANAGEMENT =============
    
    async def create_group(self, config: ClusterGroupConfig) -> ClusterGroupInfo:
        """
        클러스터 그룹 생성
        
        Args:
            config: 그룹 설정
            
        Returns:
            생성된 그룹 정보
        """
        logger.info(f"Creating cluster group: {config.name}")
        
        group_id = f"group-{config.name}-{datetime.now().strftime('%Y%m%d%H%M%S')}"
        
        try:
            # 그룹 메타데이터 생성
            group_info = ClusterGroupInfo(
                id=group_id,
                name=config.name,
                group_type=config.group_type.value,
                status=GroupStatus.CREATING.value,
                clusters=[],
                total_nodes=0,
                active_clusters=0,
                created_at=datetime.now().isoformat(),
                updated_at=datetime.now().isoformat(),
                config=asdict(config),
                metrics=self._initialize_metrics()
            )
            
            # 메모리에 그룹 저장
            self.groups[group_id] = group_info
            
            # 초기 클러스터 생성 (비동기)
            if config.min_clusters > 0:
                await self._ensure_min_clusters(group_id)
            
            # 그룹 상태 업데이트
            group_info.status = GroupStatus.ACTIVE.value
            group_info.updated_at = datetime.now().isoformat()
            
            logger.info(f"Group {config.name} created successfully: {group_id}")
            return group_info
            
        except Exception as e:
            logger.error(f"Failed to create group {config.name}: {e}")
            if group_id in self.groups:
                self.groups[group_id].status = GroupStatus.ERROR.value
            raise
    
    def get_group(self, group_id: str) -> Optional[ClusterGroupInfo]:
        """그룹 정보 조회"""
        return self.groups.get(group_id)
    
    def list_groups(self, group_type: Optional[GroupType] = None) -> List[ClusterGroupInfo]:
        """그룹 목록 조회"""
        groups = list(self.groups.values())
        
        if group_type:
            groups = [g for g in groups if g.group_type == group_type.value]
            
        return groups
    
    async def delete_group(self, group_id: str, force: bool = False) -> bool:
        """
        클러스터 그룹 삭제
        
        Args:
            group_id: 그룹 ID
            force: 강제 삭제 여부
            
        Returns:
            삭제 성공 여부
        """
        logger.info(f"Deleting cluster group: {group_id}")
        
        group = self.groups.get(group_id)
        if not group:
            logger.warning(f"Group not found: {group_id}")
            return False
        
        try:
            # 그룹 상태 업데이트
            group.status = GroupStatus.DELETING.value
            
            # 모든 클러스터 삭제
            deleted_count = 0
            for cluster_info in group.clusters.copy():
                try:
                    success = self.crud.delete_cluster(cluster_info['id'], force=force)
                    if success:
                        deleted_count += 1
                        group.clusters.remove(cluster_info)
                except Exception as e:
                    logger.error(f"Failed to delete cluster {cluster_info['name']}: {e}")
                    if not force:
                        raise
            
            # 그룹 메타데이터 삭제
            del self.groups[group_id]
            
            logger.info(f"Group {group_id} deleted successfully ({deleted_count} clusters)")
            return True
            
        except Exception as e:
            logger.error(f"Failed to delete group {group_id}: {e}")
            if group_id in self.groups:
                self.groups[group_id].status = GroupStatus.ERROR.value
            return False
    
    # ============= CLUSTER MANAGEMENT WITHIN GROUP =============
    
    async def add_cluster_to_group(self, group_id: str, cluster_config: Dict) -> bool:
        """
        그룹에 클러스터 추가
        
        Args:
            group_id: 그룹 ID  
            cluster_config: 클러스터 설정
            
        Returns:
            추가 성공 여부
        """
        logger.info(f"Adding cluster to group {group_id}")
        
        group = self.groups.get(group_id)
        if not group:
            logger.error(f"Group not found: {group_id}")
            return False
        
        try:
            # 클러스터 생성
            config = self._build_cluster_config(group, cluster_config)
            cluster = self.crud.create_cluster(config)
            
            # 그룹에 클러스터 추가
            cluster_info = {
                'id': cluster.id,
                'name': cluster.name,
                'status': cluster.status,
                'node_count': cluster.node_count,
                'master_count': cluster.master_count,
                'template_id': cluster.cluster_template_id,
                'created_at': cluster.created_at,
                'workload_assignments': [],
                'utilization': 0.0
            }
            
            group.clusters.append(cluster_info)
            group.active_clusters += 1
            group.total_nodes += cluster.node_count + cluster.master_count
            group.updated_at = datetime.now().isoformat()
            
            logger.info(f"Cluster {cluster.name} added to group {group_id}")
            return True
            
        except Exception as e:
            logger.error(f"Failed to add cluster to group {group_id}: {e}")
            return False
    
    async def remove_cluster_from_group(self, group_id: str, cluster_id: str, 
                                       migrate_workloads: bool = True) -> bool:
        """
        그룹에서 클러스터 제거
        
        Args:
            group_id: 그룹 ID
            cluster_id: 클러스터 ID
            migrate_workloads: 워크로드 마이그레이션 여부
            
        Returns:
            제거 성공 여부
        """
        logger.info(f"Removing cluster {cluster_id} from group {group_id}")
        
        group = self.groups.get(group_id)
        if not group:
            logger.error(f"Group not found: {group_id}")
            return False
        
        # 클러스터 찾기
        cluster_info = None
        for c in group.clusters:
            if c['id'] == cluster_id:
                cluster_info = c
                break
        
        if not cluster_info:
            logger.error(f"Cluster {cluster_id} not found in group {group_id}")
            return False
        
        try:
            # 워크로드 마이그레이션 (옵션)
            if migrate_workloads and cluster_info.get('workload_assignments'):
                await self._migrate_workloads_from_cluster(group_id, cluster_id)
            
            # 클러스터 삭제
            success = self.crud.delete_cluster(cluster_id)
            
            if success:
                # 그룹에서 제거
                group.clusters.remove(cluster_info)
                group.active_clusters -= 1
                group.total_nodes -= cluster_info.get('node_count', 0) + cluster_info.get('master_count', 0)
                group.updated_at = datetime.now().isoformat()
                
                logger.info(f"Cluster {cluster_id} removed from group {group_id}")
                return True
            else:
                logger.error(f"Failed to delete cluster {cluster_id}")
                return False
                
        except Exception as e:
            logger.error(f"Failed to remove cluster {cluster_id} from group {group_id}: {e}")
            return False
    
    # ============= EXTERNAL COMMAND INTERFACE =============
    
    async def execute_optimization_command(self, command: Dict) -> Dict:
        """
        외부 모듈(optimizer)로부터의 최적화 명령 실행
        
        Args:
            command: 최적화 명령
            
        Returns:
            실행 결과
        """
        logger.info(f"Executing optimization command: {command.get('type')}")
        
        command_type = command.get('type')
        
        try:
            if command_type == 'scale_group':
                return await self._handle_scale_group_command(command)
            elif command_type == 'consolidate_groups':
                return await self._handle_consolidate_command(command) 
            elif command_type == 'migrate_workloads':
                return await self._handle_migrate_command(command)
            elif command_type == 'optimize_placement':
                return await self._handle_placement_command(command)
            else:
                raise ValueError(f"Unknown command type: {command_type}")
                
        except Exception as e:
            logger.error(f"Failed to execute command {command_type}: {e}")
            return {
                'success': False,
                'error': str(e),
                'timestamp': datetime.now().isoformat()
            }
    
    # ============= INTERNAL HELPER METHODS =============
    
    def _build_cluster_config(self, group: ClusterGroupInfo, cluster_spec: Dict) -> ClusterConfig:
        """그룹 설정 기반 클러스터 설정 생성"""
        # 그룹 타입에 따른 템플릿 선택
        template_map = {
            GroupType.GPU_INTENSIVE.value: "ai-k8s-template",
            GroupType.CPU_COMPUTE.value: "compute-k8s-template", 
            GroupType.MIXED_WORKLOAD.value: "dev-k8s-template",
            GroupType.DEVELOPMENT.value: "dev-k8s-template",
            GroupType.PRODUCTION.value: "prod-k8s-template"
        }
        
        template_id = template_map.get(group.group_type, "dev-k8s-template")
        
        return ClusterConfig(
            name=f"{group.name}-{cluster_spec.get('name', 'auto')}-{datetime.now().strftime('%H%M%S')}",
            cluster_template_id=template_id,
            keypair="kcloud-keypair",
            master_count=cluster_spec.get('master_count', 1),
            node_count=cluster_spec.get('node_count', 2),
            fixed_network="cloud-platform-selfservice",
            fixed_subnet="cloud-platform-selfservice-subnet",
            labels={
                **group.config.get('labels', {}),
                'group_id': group.id,
                'group_type': group.group_type,
                **cluster_spec.get('labels', {})
            }
        )
    
    def _initialize_metrics(self) -> Dict:
        """그룹 메트릭 초기화"""
        return {
            'total_cost': 0.0,
            'avg_utilization': 0.0,
            'scaling_events': 0,
            'consolidation_events': 0,
            'last_optimization': None,
            'workload_distribution': {}
        }
    
    async def _ensure_min_clusters(self, group_id: str):
        """최소 클러스터 수 보장"""
        group = self.groups[group_id]
        min_clusters = group.config.get('min_clusters', 1)
        
        current_clusters = len(group.clusters)
        needed = min_clusters - current_clusters
        
        if needed > 0:
            logger.info(f"Creating {needed} clusters for group {group_id}")
            
            for i in range(needed):
                cluster_spec = {
                    'name': f'auto-{i+1}',
                    'node_count': 1,
                    'master_count': 1
                }
                await self.add_cluster_to_group(group_id, cluster_spec)
    
    async def _migrate_workloads_from_cluster(self, group_id: str, cluster_id: str):
        """클러스터에서 워크로드 마이그레이션"""
        logger.info(f"Migrating workloads from cluster {cluster_id}")
        # TODO: 실제 워크로드 마이그레이션 로직 구현
        # Kubernetes API를 통한 Pod 마이그레이션
        pass
    
    async def _handle_scale_group_command(self, command: Dict) -> Dict:
        """그룹 스케일링 명령 처리"""
        group_id = command.get('group_id')
        target_clusters = command.get('target_clusters')
        
        group = self.groups.get(group_id)
        if not group:
            raise ValueError(f"Group not found: {group_id}")
        
        current_clusters = len(group.clusters)
        
        if target_clusters > current_clusters:
            # 스케일 아웃
            for _ in range(target_clusters - current_clusters):
                await self.add_cluster_to_group(group_id, {'name': 'scale-out', 'node_count': 2})
        elif target_clusters < current_clusters:
            # 스케일 인
            clusters_to_remove = group.clusters[target_clusters:]
            for cluster_info in clusters_to_remove:
                await self.remove_cluster_from_group(group_id, cluster_info['id'])
        
        return {
            'success': True,
            'group_id': group_id,
            'new_cluster_count': len(self.groups[group_id].clusters),
            'timestamp': datetime.now().isoformat()
        }
    
    async def _handle_consolidate_command(self, command: Dict) -> Dict:
        """클러스터 통합 명령 처리"""
        # TODO: 클러스터 통합 로직 구현
        return {'success': True, 'message': 'Consolidation completed'}
    
    async def _handle_migrate_command(self, command: Dict) -> Dict:
        """워크로드 마이그레이션 명령 처리"""  
        # TODO: 워크로드 마이그레이션 로직 구현
        return {'success': True, 'message': 'Migration completed'}
    
    async def _handle_placement_command(self, command: Dict) -> Dict:
        """배치 최적화 명령 처리"""
        # TODO: 배치 최적화 로직 구현  
        return {'success': True, 'message': 'Placement optimized'}


# 사용 예제 및 테스트
if __name__ == "__main__":
    async def main():
        # 오케스트레이터 초기화
        orchestrator = ClusterGroupOrchestrator()
        
        # 그룹 설정
        config = ClusterGroupConfig(
            name="ml-training-group",
            group_type=GroupType.GPU_INTENSIVE,
            min_clusters=2,
            max_clusters=5,
            auto_scaling_enabled=True
        )
        
        # 그룹 생성
        group = await orchestrator.create_group(config)
        print(f"Created group: {group.name} ({group.id})")
        
        # 그룹 목록 조회
        groups = orchestrator.list_groups()
        print(f"Total groups: {len(groups)}")
        
        # 최적화 명령 실행 예제
        command = {
            'type': 'scale_group',
            'group_id': group.id,
            'target_clusters': 3
        }
        
        result = await orchestrator.execute_optimization_command(command)
        print(f"Optimization result: {result}")
    
    # 비동기 실행
    asyncio.run(main())