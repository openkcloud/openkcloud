#!/usr/bin/env python3
"""
"""

from datetime import datetime, timedelta
from typing import Dict, List, Any, Optional
import json
import hashlib

class RedisKeys:
    """
    

    NAMESPACE = "kcloud"
    SEPARATOR = ":"
    

    CLUSTER = "cluster"
    METRICS = "metrics"
    ALERTS = "alerts"
    DASHBOARD = "dashboard"
    USER = "user"
    SESSION = "session"
    CACHE = "cache"
    STATS = "stats"
    LOCK = "lock"
    
    @classmethod
    def _build_key(cls, *parts: str) -> str:
        """
        return cls.SEPARATOR.join([cls.NAMESPACE] + list(parts))
    

    @classmethod
    def cluster_current(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.CLUSTER, cluster_name, "current")
    
    @classmethod
    def cluster_status(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.CLUSTER, cluster_name, "status")
    
    @classmethod
    def cluster_config(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.CLUSTER, cluster_name, "config")
    
    @classmethod
    def cluster_list(cls) -> str:
        """
        return cls._build_key(cls.CLUSTER, "active_list")
    

    @classmethod
    def metrics_latest(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.METRICS, cluster_name, "latest")
    
    @classmethod
    def metrics_history(cls, cluster_name: str, duration: str = "1h") -> str:
        """
        return cls._build_key(cls.METRICS, cluster_name, "history", duration)
    
    @classmethod
    def metrics_summary(cls, cluster_name: str, period: str = "hour") -> str:
        """
        return cls._build_key(cls.METRICS, cluster_name, "summary", period)
    

    @classmethod
    def alerts_active(cls) -> str:
        """
        return cls._build_key(cls.ALERTS, "active")
    
    @classmethod
    def alerts_by_cluster(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.ALERTS, "by_cluster", cluster_name)
    
    @classmethod
    def alerts_by_severity(cls, severity: str) -> str:
        """
        return cls._build_key(cls.ALERTS, "by_severity", severity.lower())
    
    @classmethod
    def alerts_history(cls, date: str = None) -> str:
        """
        if not date:
            date = datetime.now().strftime("%Y%m%d")
        return cls._build_key(cls.ALERTS, "history", date)
    
    @classmethod
    def alert_cooldown(cls, rule_name: str, cluster_name: str) -> str:
        """
        return cls._build_key(cls.ALERTS, "cooldown", rule_name, cluster_name)
    

    @classmethod
    def dashboard_cache(cls, timestamp: int = None) -> str:
        """
        if not timestamp:
            timestamp = int(datetime.now().timestamp())
        return cls._build_key(cls.DASHBOARD, "cache", str(timestamp))
    
    @classmethod
    def dashboard_config(cls, user_id: str) -> str:
        """
        return cls._build_key(cls.DASHBOARD, "config", user_id)
    
    @classmethod
    def dashboard_widgets(cls, dashboard_id: str) -> str:
        """
        return cls._build_key(cls.DASHBOARD, "widgets", dashboard_id)
    

    @classmethod
    def user_session(cls, user_id: str) -> str:
        """
        return cls._build_key(cls.USER, user_id, "session")
    
    @classmethod
    def user_preferences(cls, user_id: str) -> str:
        """
        return cls._build_key(cls.USER, user_id, "preferences")
    
    @classmethod
    def user_online(cls) -> str:
        """
        return cls._build_key(cls.USER, "online")
    

    @classmethod
    def cache_api_response(cls, endpoint: str, params_hash: str) -> str:
        """
        return cls._build_key(cls.CACHE, "api", endpoint, params_hash)
    
    @classmethod
    def cache_query_result(cls, query_hash: str) -> str:
        """
        return cls._build_key(cls.CACHE, "query", query_hash)
    

    @classmethod
    def stats_hourly(cls, cluster_name: str, hour: str) -> str:
        """
        return cls._build_key(cls.STATS, "hourly", cluster_name, hour)
    
    @classmethod
    def stats_daily(cls, cluster_name: str, date: str) -> str:
        """
        return cls._build_key(cls.STATS, "daily", cluster_name, date)
    
    @classmethod
    def stats_global(cls, metric: str) -> str:
        """
        return cls._build_key(cls.STATS, "global", metric)
    

    @classmethod
    def lock_cluster_operation(cls, cluster_name: str, operation: str) -> str:
        """
        return cls._build_key(cls.LOCK, "cluster", cluster_name, operation)
    
    @classmethod
    def lock_metrics_collection(cls, cluster_name: str) -> str:
        """
        return cls._build_key(cls.LOCK, "metrics", cluster_name)


class RedisPubSubChannels:
    """
    

    ALERTS_NEW = "kcloud:events:alerts:new"
    ALERTS_RESOLVED = "kcloud:events:alerts:resolved"
    
    METRICS_UPDATED = "kcloud:events:metrics:updated"
    METRICS_BATCH = "kcloud:events:metrics:batch"
    
    CLUSTER_STATUS_CHANGED = "kcloud:events:cluster:status"
    CLUSTER_CREATED = "kcloud:events:cluster:created"
    CLUSTER_DELETED = "kcloud:events:cluster:deleted"
    
    DASHBOARD_REFRESH = "kcloud:events:dashboard:refresh"
    USER_ACTIVITY = "kcloud:events:user:activity"
    

    @classmethod
    def cluster_events_pattern(cls, cluster_name: str) -> str:
        """
        return f"kcloud:events:cluster:{cluster_name}:*"
    
    @classmethod
    def user_notifications_pattern(cls, user_id: str) -> str:
        """
        return f"kcloud:notifications:user:{user_id}:*"


class RedisDataTypes:
    """
    
    @staticmethod
    def serialize_cluster_metrics(metrics: Dict[str, Any]) -> str:
        """
        return json.dumps({
            **metrics,
            'timestamp': datetime.now().isoformat(),
            '_type': 'cluster_metrics'
        })
    
    @staticmethod
    def deserialize_cluster_metrics(data: str) -> Dict[str, Any]:
        """
        return json.loads(data)
    
    @staticmethod
    def create_alert_payload(alert_id: str, cluster_name: str,
                           severity: str, message: str, metadata: Dict = None) -> str:
        """
        return json.dumps({
            'alert_id': alert_id,
            'cluster_name': cluster_name,
            'severity': severity,
            'message': message,
            'timestamp': datetime.now().isoformat(),
            'metadata': metadata or {},
            '_type': 'alert'
        })
    
    @staticmethod
    def create_dashboard_cache(clusters_data: Dict, summary: Dict,
                             alerts_count: int) -> str:
        """
        return json.dumps({
            'clusters': clusters_data,
            'summary': summary,
            'alerts_count': alerts_count,
            'generated_at': datetime.now().isoformat(),
            'expires_at': (datetime.now() + timedelta(seconds=30)).isoformat(),
            '_type': 'dashboard_cache'
        })
    
    @staticmethod
    def hash_query_params(**params) -> str:
        """
        param_str = json.dumps(params, sort_keys=True)
        return hashlib.md5(param_str.encode()).hexdigest()


class RedisExpirePolicy:
    """
    

    CLUSTER_CURRENT = 60 * 5
    METRICS_LATEST = 60 * 2
    METRICS_HISTORY = 60 * 60
    ALERTS_ACTIVE = 60 * 60 * 24
    DASHBOARD_CACHE = 30
    USER_SESSION = 60 * 60 * 4
    API_CACHE = 60 * 15
    QUERY_CACHE = 60 * 5
    LOCK = 60 * 2
    

    @classmethod
    def metrics_history_ttl(cls, duration: str) -> int:
        """
        duration_map = {
            '1h': 60 * 60 * 2,
            '6h': 60 * 60 * 12,
            '24h': 60 * 60 * 48,
            '7d': 60 * 60 * 24 * 14
        }
        return duration_map.get(duration, cls.METRICS_HISTORY)
    
    @classmethod
    def alert_cooldown_ttl(cls, cooldown_minutes: int) -> int:
        """
        return cooldown_minutes * 60 + 10


# Redis 키 구조 문서화
REDIS_KEY_DOCUMENTATION = {
    "데이터 구조": {
        "String": ["cluster:*:current", "metrics:*:latest", "cache:*"],
        "Hash": ["user:*:session", "dashboard:config:*", "stats:*"],
        "List": ["metrics:*:history:*", "alerts:history:*"],
        "Set": ["cluster:active_list", "alerts:by_cluster:*", "user:online"],
        "Sorted Set": ["alerts:active (score=timestamp)"],
        "Stream": ["events:* (미래 구현)"]
    },
    "만료 정책": {
        "실시간 데이터": "30초 - 5분",
        "메트릭 히스토리": "1시간 - 48시간",
        "사용자 세션": "4시간",
        "캐시 데이터": "5분 - 15분",
        "분산 락": "2분"
    },
    "메모리 사용량 추정": {
        "클러스터 10개": "약 50-100MB",
        "메트릭 데이터": "클러스터당 5-10MB/시간",
        "알림 데이터": "일일 1-5MB",
        "캐시 데이터": "10-20MB"
    }
}

if __name__ == "__main__":

    print("🔑 Redis 키 구조 테스트")
    print("=" * 50)
    
    cluster_name = "kcloud-dev-cluster"
    user_id = "user-123"
    
    print(" 클러스터 키:")
    print(f"  현재 상태: {RedisKeys.cluster_current(cluster_name)}")
    print(f"  설정: {RedisKeys.cluster_config(cluster_name)}")
    
    print(f"\n 메트릭 키:")
    print(f"  최신: {RedisKeys.metrics_latest(cluster_name)}")
    print(f"  히스토리: {RedisKeys.metrics_history(cluster_name, '1h')}")
    
    print(f"\n[ALERT] 알림 키:")
    print(f"  활성 알림: {RedisKeys.alerts_active()}")
    print(f"  클러스터별: {RedisKeys.alerts_by_cluster(cluster_name)}")
    
    print(f"\n👤 사용자 키:")
    print(f"  세션: {RedisKeys.user_session(user_id)}")
    print(f"  대시보드 설정: {RedisKeys.dashboard_config(user_id)}")
    
    print(f"\n Pub/Sub 채널:")
    print(f"  새 알림: {RedisPubSubChannels.ALERTS_NEW}")
    print(f"  메트릭 업데이트: {RedisPubSubChannels.METRICS_UPDATED}")
    
    print(f"\n 만료 정책:")
    print(f"  클러스터 현재 상태: {RedisExpirePolicy.CLUSTER_CURRENT}초")
    print(f"  대시보드 캐시: {RedisExpirePolicy.DASHBOARD_CACHE}초")
