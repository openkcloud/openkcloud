# KCloud Monitor 문서

KCloud Monitor (AI Accelerator & Infrastructure Monitoring API) 공식 문서입니다.

## 📚 문서 목록

### [빠른 시작 가이드 (QUICK_START.md)](./QUICK_START.md)
KCloud Monitor를 처음 사용하는 경우 여기서 시작하세요.

**내용**:
- 설치 및 실행 방법
- 기본 사용법 (GPU 모니터링, 전력 모니터링)
- 실시간 데이터 스트리밍 (WebSocket, SSE)
- 멀티 클러스터 사용
- 문제 해결

**대상**: 모든 사용자

---

### [API 사용 가이드 (API_GUIDE.md)](./API_GUIDE.md)
전체 API 엔드포인트의 상세 사용법입니다.

**내용**:
- 7개 도메인별 API 상세 설명
  - Accelerators (GPU, NPU)
  - Infrastructure (Node, Pod, Container)
  - Hardware (IPMI)
  - Clusters (멀티 클러스터)
  - Monitoring (통합 모니터링 + 스트리밍)
  - Export (데이터 내보내기)
  - System (헬스체크, 메트릭)
- 요청/응답 예시
- 오류 처리

**대상**: API 개발자, 시스템 통합자

---

### [아키텍처 개요 (ARCHITECTURE_OVERVIEW.md)](./ARCHITECTURE_OVERVIEW.md)
KCloud Monitor의 7-도메인 아키텍처와 핵심 컴포넌트 설명입니다.

**내용**:
- 시스템 아키텍처 다이어그램
- 7-도메인 구조 설명
- 핵심 컴포넌트 (Prometheus Client, Cache, Stream 등)
- 데이터 흐름
- 성능 최적화 및 확장성
- 기술 스택

**대상**: 아키텍트, 시니어 개발자, DevOps

---

## 🚀 사용 시나리오별 가이드

### 처음 사용하는 경우
1. [QUICK_START.md](./QUICK_START.md) - 설치 및 기본 사용법
2. [API_GUIDE.md](./API_GUIDE.md) - 필요한 API 찾기
3. Swagger UI (http://localhost:8001/docs) - 인터랙티브 테스트

### API 통합 개발자
1. [API_GUIDE.md](./API_GUIDE.md) - 전체 API 명세
2. [ARCHITECTURE_OVERVIEW.md](./ARCHITECTURE_OVERVIEW.md) - 데이터 모델 이해
3. OpenAPI JSON (http://localhost:8001/openapi.json) - 코드 생성

### 시스템 관리자
1. [QUICK_START.md](./QUICK_START.md) - 배포 및 운영
2. [ARCHITECTURE_OVERVIEW.md](./ARCHITECTURE_OVERVIEW.md) - 모니터링 및 확장성
3. `/api/v1/system/health` - 헬스체크
4. `/api/v1/system/metrics` - Prometheus 메트릭

### 데이터 분석가
1. [API_GUIDE.md](./API_GUIDE.md) - Export API 섹션
2. `/api/v1/export/*` - CSV, Excel, Parquet 내보내기
3. `/api/v1/monitoring/timeseries/*` - 시계열 데이터

## 🔗 추가 리소스

### 온라인 API 문서
- **Swagger UI**: http://localhost:8001/docs
- **ReDoc**: http://localhost:8001/redoc
- **OpenAPI JSON**: http://localhost:8001/openapi.json

### 상세 설계 문서
프로젝트 루트의 `spec/` 폴더에서 더 상세한 설계 문서를 확인하세요:
- [ARCHITECTURE.md](../spec/ARCHITECTURE.md) - 전체 아키텍처 상세
- [API_SPECIFICATION.md](../spec/API_SPECIFICATION.md) - 완전한 API 명세
- [DATA_MODELS.md](../spec/DATA_MODELS.md) - 데이터 모델 정의
- [PROMETHEUS_SETUP.md](../spec/PROMETHEUS_SETUP.md) - Prometheus 설정

### 프로젝트 정보
- **저장소**: GitHub (TBD)
- **버전**: v0.1.0
- **Python**: 3.12+
- **FastAPI**: 0.104+

## 📖 문서 규칙

### API 예시 형식
모든 API 예시는 다음 형식을 따릅니다:

```bash
# 요청
curl -u admin:changeme http://localhost:8001/api/v1/[endpoint]

# 응답 (JSON)
{
  "timestamp": "2024-01-01T12:00:00Z",
  "data": { ... }
}
```

### 호스트네임 규칙
문서의 모든 예시는 다음 일반 호스트네임을 사용합니다:
- `master1`, `master2`, `master3`: 마스터 노드
- `worker1`, `worker2`, `worker3`: 워커 노드
- `cluster1`, `cluster2`: 클러스터 이름

실제 환경에서는 각자의 호스트네임으로 대체하세요.

### 단위 명시
모든 필드명은 단위를 명시합니다:
- `power_watts`: 와트 단위 전력
- `temperature_celsius`: 섭씨 온도
- `memory_used_mb`: 메가바이트 메모리
- `cpu_utilization_percent`: 퍼센트 사용률
- `fan_speed_rpm`: RPM

## 🆘 지원

### 문제 발생 시
1. [QUICK_START.md](./QUICK_START.md)의 문제 해결 섹션 확인
2. `/api/v1/system/health`로 서버 상태 확인
3. GitHub Issues에 문제 보고

### 기능 요청
- GitHub Issues에 Feature Request 작성
- 사용 사례 및 필요성 설명

### 기여
- Pull Request 환영
- 코드 스타일: Black, Flake8
- 테스트 작성 필수

## 📝 문서 업데이트

**마지막 업데이트**: 2024-01-17
**문서 버전**: v1.0.0
**API 버전**: v0.1.0

## 라이선스

TBD (프로젝트 라이선스 정보)
