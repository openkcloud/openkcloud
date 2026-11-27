# KCloud Monitor

> AI 반도체 통합 모니터링 플랫폼 (v2)

KCloud Monitor는 AI 반도체(GPU, NPU)와 클라우드 인프라(K8s·OpenStack)의 자원·전력을 통합 모니터링하는 FastAPI 기반 REST API 서비스입니다.

![GitHub license](https://img.shields.io/badge/license-Apache%202.0-blue.svg)
![Python version](https://img.shields.io/badge/python-3.12-green.svg)
![FastAPI](https://img.shields.io/badge/FastAPI-0.119%2B-teal.svg)
![API Version](https://img.shields.io/badge/API-v2-blue)

[![Korean](https://img.shields.io/badge/lang-한국어-red)](README.md)
[![English](https://img.shields.io/badge/lang-English-blue)](README_EN.md)

## 현재 상태

v1 API(프로토타입)는 종료되었고, `/api/v2` 아래 **155개 라우트**가 동작합니다.
`GET /openstack/vms/{vm_id}/gpu-passthrough` 하나만 아직 스텁(`status: not_implemented`)을 반환합니다.

- 메트릭: Prometheus (`PROMETHEUS_URL`)
- 로그: Loki (`LOKI_URL`)
- 알람·미터링·장애·복구 저장소: PostgreSQL (`DATABASE_URL`)
- VM·하이퍼바이저·프로젝트: OpenStack API (`OPENSTACK_*`)

`PROMETHEUS_URL` 은 필수입니다. 나머지 데이터소스를 설정하지 않은 영역은 503을 반환하거나, `NOT_CONFIGURED` 경고와 함께 빈 데이터를 반환합니다. 영역별 조건은 [API 가이드](docs/API_GUIDE.md)를 참고하세요.

## API 구조 (`/api/v2`)

| 영역 | 경로 | 내용 | 수량 |
|------|------|------|------|
| Clusters | `/clusters/{c}` | 클러스터 상세·자원 현황·토폴로지·전력 [P1] | 5 |
| Nodes & Hardware | `/clusters/{c}/nodes/{n}/*` | 노드 메트릭·전력 [P2] + IPMI 실측 [P3] | 12 |
| Accelerators & Partitions | `.../accelerators/{id}/*` | GPU/NPU 통합, 파티션(MIG/vGPU/slice) [P4·P5] | 11(+별칭 2) |
| Storage (Ceph) | `/storage/ceph/*` | Rook-Ceph 요약·용량·OSD·풀·PG | 9 |
| OpenStack | `/openstack/*` | 프로젝트·하이퍼바이저·VM·전력 귀속 [P6] | 10 |
| Workloads | `/clusters/{c}/workloads/*`, `/clusters/{c}/namespaces` | Pod/Container/Namespace [P7] | 11(+별칭 2) |
| Workloads (전역) | `/workloads/*` | 포탈용 전역 진입점 (`_links.canonical`) | 10 |
| Monitoring | `/monitoring/*` | 횡단 전력 집계 [P8]·사용률·온도·쓰로틀링 시계열·SSE 스트림 | 15 |
| Logs | `/logs/*` | Loki 로그 검색·스트림·내보내기 | 10 |
| Export | `/export/*` | 전력/메트릭 내보내기(csv·json), 운영 리포트 | 3 |
| Resource-Map | `/resource-map/pods/{pod}` | Pod → 노드 → VM → 물리서버 추적 | 1 |
| Alerts | `/alerts/*` | 알람 정책·알림 채널·활성 알람·이력 | 15 |
| SLO | `/slo/*` | LLM 서빙 SLO(TTFT·TPOT) | 3 |
| Metering | `/metering/*` | 사용량·단가 정책·비용 예측·예산 | 21 |
| Faults / Recovery | `/faults/*`, `/recovery/*` | 장애 분석, 후속 조치 정책, 복구 정보 | 3 / 7 |
| System | `/system/*` | 헬스·버전·자체 메트릭 (공개) | 3 |
| Auth | `/auth/*` | JWT 발급·검증 (Gateway 도입 전 개발용) | 2 |

## 빠른 시작

```bash
# 1. 의존성 설치
python3.12 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt

# 2. 환경 변수 (PROMETHEUS_URL, API_AUTH_PASSWORD, JWT_SECRET_KEY 등 수정)
cp .env.example .env

# 3. 서버 실행
python run.py --port 8000
```

```bash
# 헬스체크 (공개)
curl http://localhost:8000/api/v2/system/health

# 로그인 → 토큰 (.env 의 API_AUTH_USERNAME / API_AUTH_PASSWORD)
curl -X POST http://localhost:8000/api/v2/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<API_AUTH_PASSWORD>"}'

# 조회 예시 (전력 요약)
curl -H "Authorization: Bearer <TOKEN>" \
  http://localhost:8000/api/v2/monitoring/power/summary
```

- Swagger UI: http://localhost:8000/docs
- Docker Compose 실행은 [빠른 시작](docs/QUICK_START.md#docker)을 참고하세요. `POSTGRES_*` 값이 필요합니다.

## 아키텍처

| 백엔드 | 용도 | 상태 |
|--------|------|------|
| **Prometheus** (PromQL) | 메트릭 조회 — DCGM, Furiosa/Rebellions, Kepler, node-exporter, IPMI, kube-state-metrics, Ceph, vLLM | ✅ 사용 중 |
| **Loki** (LogQL) | 로그 검색·스트림, 로그 알람 | ✅ 사용 중 |
| **PostgreSQL** | 알람 정책·이력, 미터링, 장애 이력, 후속 조치 정책 | ✅ 사용 중 |
| **OpenStack API** | VM·하이퍼바이저·프로젝트 메타데이터, VM 전력 귀속 | ✅ 사용 중 |
| **Mimir** | 중앙 메트릭 저장소 (클러스터별 Alloy remote_write) | 📋 목표 구조 — 현재는 `PROMETHEUS_URL` 로 PromQL 호환 백엔드 지정 |
| **Redis(+Streams)** | 캐시 + 서비스 간 이벤트 버스 | 📋 목표 구조 |
| **libvirt** | GPU passthrough 확정 | 📋 미구현 |

전력 계층(P1~P8)·신뢰도 표기(`power_estimation`, `attribution.method`)는
[아키텍처 개요](docs/ARCHITECTURE_OVERVIEW.md#전력-계층-p1p8)를 참고하세요.

## 로드맵

- ✅ v2 라우팅/인증/경로 구조
- ✅ clusters·nodes·accelerators·storage(Ceph)·openstack·workloads·monitoring·export 조회
- ✅ 로그 조회(Loki), 알람, LLM 서빙 SLO, 미터링·비용 예측, 장애 분석·복구 정보
- 🚧 VM GPU passthrough 확인(libvirt), resource-map 확장(가속기·VM 계보, 이력)
- 📋 resource-map Discovery 수집기, 전력 귀속 recording rules

## 문서

| 문서 | 설명 |
|------|------|
| [빠른 시작](docs/QUICK_START.md) | 설치·실행·로그인·API 호출·Docker |
| [API 가이드](docs/API_GUIDE.md) | 전체 155 라우트 표·인증·공통 파라미터·응답 정책·SSE |
| [아키텍처 개요](docs/ARCHITECTURE_OVERVIEW.md) | 6계층 자원 모델·데이터소스·전력 계층 P1~P8·resource-map |
| [NPU 수집 설정](docs/PROMETHEUS_NPU_SETUP.md) | Furiosa exporter + hwmon 수집 환경 |

### 🔧 기술 스택

- **Framework**: FastAPI 0.119+, Python 3.12
- **Data Validation**: Pydantic 2.12+
- **Metrics**: prometheus-client (자체 메트릭 노출)
- **Server**: Uvicorn (ASGI)

## 기여하기

버그 리포트나 기능 제안은 이슈를 통해 제출해 주세요.

### 브랜치 전략

- `main`: 릴리즈 브랜치 (릴리즈 버전을 올릴 때만 `develop`에서 통합)
- `develop`: 통합 브랜치 (모든 PR의 대상)
- 작업 브랜치: `develop`에서 분기하며 접두어로 용도를 구분합니다.
  - `feat/<이름>`: 기능 추가
  - `fix/<이름>`: 버그 수정
  - `docs/<이름>`: 문서 수정

### 기여 절차

```bash
# 1. GitHub에서 저장소를 포크한 뒤, 본인 포크를 클론
git clone https://github.com/<your-id>/kcloud-monitor.git
cd kcloud-monitor
git remote add upstream https://github.com/openkcloud/kcloud-monitor.git

# 2. 최신 develop에서 작업 브랜치 생성
git fetch upstream
git checkout -b feat/your-feature upstream/develop

# 3. 변경사항 커밋 (아래 커밋 메시지 규칙 참고)
git commit -m "✨ 추가(scope): 변경 내용 요약"

# 4. 본인 포크에 푸시한 뒤, openkcloud/kcloud-monitor의 develop으로 PR 생성
git push origin feat/your-feature
```

### 커밋 메시지 규칙

`<이모지> <유형>(<범위>): <요약>` 형식을 사용합니다.

| 이모지 | 유형 | 용도 |
|--------|------|------|
| ✨ | 추가 | 기능·테스트 추가 |
| 🔧 | 수정 | 기존 동작 수정, 설정 변경 |
| 🔧 | docs | 문서 수정 |

예시: `✨ 추가(crud): get_npu_summary 정의`, `🔧 docs(readme): 문서 링크 정리`

## 라이선스

이 프로젝트는 Apache License 2.0 하에 배포됩니다. 자세한 내용은 [LICENSE](LICENSE) 파일을 참조하세요.

```
Copyright 2025 OpenKCloud Community

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
```

## 문의

- **개발**: OpenKCloud 커뮤니티
- **이슈**: [GitHub Issues](https://github.com/openkcloud/kcloud-monitor/issues)

---

**KCloud Monitor v2** | AI 반도체 통합 모니터링 플랫폼
