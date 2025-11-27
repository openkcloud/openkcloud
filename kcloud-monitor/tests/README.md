# AI Chip Monitor - Test Suite

## 📁 구조

```
tests/
├── conftest.py           # pytest 설정 및 공통 픽스처
├── unit/                # 단위 테스트
│   ├── api/            # API 엔드포인트 테스트 (8개)
│   │   ├── test_auth_api.py           # 인증 API (✅ 13 passed)
│   │   ├── test_system_api.py         # 시스템 API (✅ 5 passed)
│   │   ├── test_accelerators_api.py   # GPU/NPU API
│   │   ├── test_infrastructure_api.py # 노드/파드/컨테이너 API
│   │   ├── test_hardware_api.py       # 하드웨어(IPMI) API
│   │   ├── test_clusters_api.py       # 클러스터 관리 API
│   │   ├── test_monitoring_api.py     # 모니터링 API
│   │   └── test_export_api.py         # 데이터 익스포트 API
│   │
│   └── services/        # 서비스 레이어 테스트 (5개)
│       ├── test_cache.py              # ✅ 14/14 passed
│       ├── test_prometheus.py         # ✅ 대부분 통과
│       ├── test_prometheus_validation.py
│       ├── test_cluster_registry.py   # 일부 수정 필요
│       └── test_csv_exporter.py       # 일부 수정 필요
```

## 🚀 테스트 실행

### 전체 테스트 실행
```bash
pytest tests/
```

### API 테스트만 실행
```bash
pytest tests/unit/api/ -v
```

### Services 테스트만 실행
```bash
pytest tests/unit/services/ -v
```

### 인증 테스트만 실행 (빠른 검증)
```bash
pytest tests/unit/api/test_auth_api.py tests/unit/api/test_system_api.py -v
```

### 커버리지 포함 실행
```bash
pytest tests/ --cov=app --cov-report=html
```

## ✅ 현재 상태

| 카테고리 | 파일 수 | 테스트 수 | 상태 |
|---------|--------|----------|------|
| **API Tests** | 8개 | 43개 | ⚠️ 18 passed, 25 needs work |
| **Service Tests** | 5개 | 96개 | ✅ 80% passed |
| **Total** | 13개 | 139개 | 🔄 진행 중 |

### 완전히 작동하는 테스트
- ✅ `test_auth_api.py` - JWT 인증 테스트 (13/13 passed)
- ✅ `test_system_api.py` - 시스템 헬스체크 (5/6 passed)
- ✅ `test_cache.py` - 캐시 서비스 (14/14 passed)
- ✅ `test_prometheus.py` - Prometheus 클라이언트 (대부분 통과)

### 작업 필요
- ⚠️ API 테스트 - 실제 CRUD 함수 모킹 필요
- ⚠️ `test_cluster_registry.py` - 일부 수정 필요
- ⚠️ `test_csv_exporter.py` - 일부 수정 필요

## 🔧 픽스처 (conftest.py)

공통으로 사용하는 테스트 픽스처:

- `test_settings` - 테스트용 설정 오버라이드
- `client` - FastAPI TestClient
- `auth_token` - 유효한 JWT 토큰
- `auth_headers` - Authorization 헤더
- `sample_gpu_data` - 샘플 GPU 데이터
- `sample_node_data` - 샘플 노드 데이터

## 📝 테스트 작성 가이드

### API 엔드포인트 테스트 예시

```python
def test_endpoint_with_auth(client, auth_headers):
    """인증이 필요한 엔드포인트 테스트"""
    response = client.get("/api/v1/some/endpoint", headers=auth_headers)
    assert response.status_code == 200
```

### 인증 체크 테스트

```python
def test_endpoint_requires_auth(client):
    """인증 없이 호출 시 403 반환 확인"""
    response = client.get("/api/v1/some/endpoint")
    assert response.status_code == 403
```

### 모킹 예시

```python
from unittest.mock import patch

def test_with_mocking(client, auth_headers):
    with patch('app.crud.some_function') as mock_func:
        mock_func.return_value = {"result": "mocked"}

        response = client.get("/api/v1/endpoint", headers=auth_headers)
        assert response.status_code == 200
```

## 🎯 다음 단계

1. **API 테스트 완성**: 실제 CRUD 함수를 모킹하여 나머지 API 테스트 수정
2. **Service 테스트 수정**: cluster_registry, csv_exporter 실패 케이스 수정
3. **통합 테스트 추가**: E2E 시나리오 테스트 (선택)
4. **CI/CD 통합**: GitHub Actions에 테스트 자동화

## 📚 참고

- [pytest 공식 문서](https://docs.pytest.org/)
- [FastAPI 테스트 가이드](https://fastapi.tiangolo.com/tutorial/testing/)
- [Python unittest.mock](https://docs.python.org/3/library/unittest.mock.html)
