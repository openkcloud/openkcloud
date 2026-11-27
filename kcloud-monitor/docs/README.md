# KCloud Monitor 문서

KCloud Monitor v2 (AI 반도체 통합 모니터링 API) 공식 문서입니다.

> `/api/v2` 아래 155개 라우트가 동작합니다. `GET /openstack/vms/{vm_id}/gpu-passthrough` 하나만 아직 스텁 응답입니다.

## 📚 문서 목록

### [빠른 시작 (QUICK_START.md)](./QUICK_START.md)
설치·실행, 환경 변수, 로그인, API 호출, SSE, Docker, 문제 해결. **처음이라면 여기서 시작하세요.**

### [API 사용 가이드 (API_GUIDE.md)](./API_GUIDE.md)
`/api/v2` 전체 155 라우트 표, 영역별 필요한 데이터소스 설정, 인증, 공통 파라미터, 공통 응답 정책(envelope·에러 스키마), 미구현 엔드포인트, SSE 계약.

### [아키텍처 개요 (ARCHITECTURE_OVERVIEW.md)](./ARCHITECTURE_OVERVIEW.md)
6계층 자원 모델(물리→관리K8s→OpenStack→서비스K8s→Pod), 데이터소스(Prometheus·Loki·PostgreSQL·OpenStack, 목표 구조 Mimir·Redis), URL 설계 원칙, 전력 계층 P1~P8과 신뢰도 표기, resource-map(자원 계보)과 SoT 우선순위, NFR, 현재 구현 상태.

### [Prometheus NPU 수집 설정 (PROMETHEUS_NPU_SETUP.md)](./PROMETHEUS_NPU_SETUP.md)
Furiosa Metrics Exporter(1차) + node_exporter hwmon(보조) 수집 환경 구성.

## 문서 체계

- `docs/` (이 폴더): 저장소 공개 문서 — 사용·운영 관점

## 외부 문서

- Swagger UI: http://localhost:8000/docs (서버 실행 시)
- ReDoc: http://localhost:8000/redoc
