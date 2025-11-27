# KCloud Monitor

> AI 반도체 통합 모니터링 플랫폼

KCloud Monitor는 AI 반도체(GPU, NPU)와 클라우드 인프라의 전력 소비 및 성능을 통합 모니터링하는 FastAPI 기반 REST API 서비스입니다. Prometheus를 통해 DCGM, Kepler, IPMI 등 다양한 메트릭을 수집하고 실시간 스트리밍, 데이터 내보내기 등의 기능을 제공합니다.

![GitHub license](https://img.shields.io/badge/license-Apache%202.0-blue.svg)
![Python version](https://img.shields.io/badge/python-3.12-green.svg)
![FastAPI](https://img.shields.io/badge/FastAPI-0.119%2B-teal.svg)
![API Version](https://img.shields.io/badge/API-v0.1.0-blue)

[![Korean](https://img.shields.io/badge/lang-한국어-red)](README.md)
[![English](https://img.shields.io/badge/lang-English-blue)](README_EN.md)

## 주요 기능

### 🚀 핵심 기능

- **AI 반도체 모니터링**: GPU(NVIDIA DCGM), NPU(Furiosa/Rebellions) 성능 및 전력 모니터링
- **인프라 모니터링**: Kubernetes Nodes, Pods, Containers 리소스 및 전력 분해 (Kepler)
- **하드웨어 센서**: IPMI 기반 물리 서버 센서 모니터링 (전력, 온도, 팬, 전압)
- **멀티 클러스터**: 여러 Kubernetes 클러스터 통합 관리 및 모니터링
- **통합 전력 분석**: 클러스터/노드/네임스페이스/리소스 타입별 전력 분해 분석 및 PUE 계산
- **실시간 스트리밍**: WebSocket 및 SSE를 통한 실시간 메트릭 푸시
- **데이터 내보내기**: JSON, CSV, Parquet, Excel, PDF 형식 지원
- **API 메트릭**: Prometheus 형식으로 API 서버 메트릭 노출

### 📊 도메인 아키텍처

1. **Accelerators** - AI 반도체 (GPU, NPU, TPU)
2. **Infrastructure** - 인프라 리소스 (Nodes, Pods, Containers, VMs)
3. **Hardware** - 물리 하드웨어 센서 (IPMI)
4. **Clusters** - 멀티 클러스터 관리
5. **Monitoring** - 통합 모니터링 및 스트리밍
6. **Export** - 데이터 내보내기 및 리포트
7. **System** - 시스템 정보 및 헬스체크

## 빠른 시작

### 1. 환경 설정

```bash
# 1. 저장소 클론
git clone https://github.com/openkcloud/kcloud-monitor.git
cd kcloud-monitor

# 2. 가상환경 생성 및 활성화
python3.12 -m venv .venv
source .venv/bin/activate  # Linux/Mac
# .venv\Scripts\activate   # Windows

# 3. 의존성 설치
pip install -r requirements.txt

# 4. 환경 변수 설정
cp .env.example .env
# .env 파일에서 Prometheus URL 및 인증 정보 설정
```

### 2. Docker로 실행 (권장)

```bash
# 1. 환경 변수 설정
cp .env.example .env
# .env 파일에서 Prometheus URL 및 인증 정보 설정

# 2. Docker Compose로 실행
docker-compose up -d

# 3. 로그 확인
docker-compose logs -f api

# 4. 서비스 상태 확인
curl http://localhost:8000/api/v1/system/health

# 5. API 문서 확인
# http://localhost:8000/docs (Swagger UI)
# http://localhost:8000/redoc (ReDoc)

# 6. 서비스 중지
docker-compose down
```

### 3. 로컬 개발 서버 실행

```bash
# 개발 서버 실행
uvicorn app.main:app --host 127.0.0.1 --port 8001 --reload

# API 문서 확인
# http://127.0.0.1:8001/docs (Swagger UI)
# http://127.0.0.1:8001/redoc (ReDoc)
```

### 4. 기본 사용 예시

```bash
# 헬스체크
curl http://127.0.0.1:8001/api/v1/system/health

# GPU 모니터링
curl -u admin:changeme http://127.0.0.1:8001/api/v1/accelerators/gpus

# 통합 전력 모니터링
curl -u admin:changeme http://127.0.0.1:8001/api/v1/monitoring/power

# 데이터 내보내기 (CSV)
curl -u admin:changeme "http://127.0.0.1:8001/api/v1/export/power?period=1h&format=csv" > power.csv
```

## API 엔드포인트

| 도메인 | 주요 엔드포인트 | 데이터 소스 | 상태 |
|--------|----------------|-------------|------|
| **Accelerators** | `/api/v1/accelerators/gpus` | DCGM | ✅ |
| | `/api/v1/accelerators/npus` | NPU Exporter | ⚠️ |
| **Infrastructure** | `/api/v1/infrastructure/nodes` | Kepler | ✅ |
| | `/api/v1/infrastructure/pods` | Kepler | ✅ |
| **Hardware** | `/api/v1/hardware/ipmi/sensors` | IPMI Exporter | ⚠️ |
| **Clusters** | `/api/v1/clusters` | Prometheus | ✅ |
| **Monitoring** | `/api/v1/monitoring/power` | Kepler + DCGM | ✅ |
| | `/api/v1/monitoring/stream/power` (WS) | Prometheus | ✅ |
| **Export** | `/api/v1/export/power?format=csv` | - | ✅ |
| **System** | `/api/v1/system/health` | - | ✅ |

> 📖 **상세 API 문서**: [spec/API_SPECIFICATION.md](spec/API_SPECIFICATION.md)
> 📋 **엔드포인트 매핑**: [docs/API_ENDPOINT_MAPPING.md](tests/test_docs/API_ENDPOINT_MAPPING.md)

## 시스템 아키텍처

```
┌─────────────────────────────────────────┐
│         Client Applications              │
│   (Dashboard, CLI, Export Tools)         │
└────────────────┬────────────────────────┘
                 │ REST API / WebSocket
┌────────────────┴────────────────────────┐
│         FastAPI Application              │
│  Accelerators│Infrastructure│Hardware│  │
│  Clusters│Monitoring│Export│System      │
└────────────────┬────────────────────────┘
                 │ Prometheus Query API
┌────────────────┴────────────────────────┐
│       Prometheus (Multi-Cluster)         │
└────────────────┬────────────────────────┘
                 │ Metrics Collection
┌────────────────┴────────────────────────┐
│  DCGM│Kepler│IPMI│NPU│OpenStack        │
└────────────────┬────────────────────────┘
                 │ Hardware Metrics
┌────────────────┴────────────────────────┐
│  GPU│NPU│Nodes│Pods│Containers│VMs     │
└─────────────────────────────────────────┘
```

### 데이터 소스

| 데이터 소스 | 용도 | 상태 |
|------------|------|------|
| **DCGM Exporter** | NVIDIA GPU 모니터링 | ✅ |
| **Kepler** | 노드/Pod 전력 분해 | ✅ |
| **IPMI Exporter** | 물리 서버 센서 | ⚠️ 설정 필요 |
| **NPU Exporter** | Furiosa/Rebellions NPU | ⚠️ 설정 필요 |
| **OpenStack** | VM 모니터링 | ❌ 미구현 |

## 로드맵

### ✅ 완료 (v0.1.0)

- 도메인 기반 아키텍처 설계 및 구현
- GPU 모니터링 (DCGM)
- 인프라 모니터링 (Kepler - Nodes/Pods/Containers)
- 멀티 클러스터 지원
- 통합 전력 모니터링 및 분해 분석
- 실시간 스트리밍 (WebSocket, SSE)
- 다양한 포맷 데이터 내보내기 (JSON, CSV, Parquet, Excel, PDF)
- API 메트릭 노출 (Prometheus)

### 🚧 개발 중

- IPMI Exporter 설정
- NPU Exporter 설정
- 테스트 코드 완성 (113 tests, 66 passed)

### 📋 향후 계획

- NPU 모니터링 (Furiosa, Rebellions)
- OpenStack VM 모니터링
- Kubernetes 배포 자동화
- Grafana 대시보드
- Alert Manager 연동

## 환경 설정

주요 환경 변수:

```bash
# Prometheus 연결
PROMETHEUS_URL=http://prometheus-server:9090

# 인증
API_AUTH_USERNAME=admin
API_AUTH_PASSWORD=changeme

# 멀티 클러스터 (옵션)
# PROMETHEUS_CLUSTERS='[{"name":"cluster1","url":"http://prom-cluster1:9090"}]'
```

> 📖 **상세 설정 가이드**: [spec/PROMETHEUS_SETUP.md](spec/PROMETHEUS_SETUP.md)

## 문서

### 📚 프로젝트 문서

| 문서 | 설명 |
|------|------|
| [API 명세서](spec/API_SPECIFICATION.md) | 전체 API 엔드포인트 상세 문서 |
| [아키텍처 설계](spec/ARCHITECTURE.md) | 도메인 기반 아키텍처 설계 철학 |
| [데이터 모델](spec/DATA_MODELS.md) | Pydantic 데이터 모델 명세 |
| [Prometheus 설정](spec/PROMETHEUS_SETUP.md) | Exporter 설정 및 연동 가이드 |
| [개발 가이드](CLAUDE.md) | 개발자를 위한 상세 가이드 |
| [엔드포인트 매핑](tests/test_docs/API_ENDPOINT_MAPPING.md) | 엔드포인트별 구현 상태 |

### 🔧 기술 스택

- **Framework**: FastAPI 0.119+, Python 3.12
- **Data Validation**: Pydantic 2.12+
- **Metrics**: Prometheus Client, DCGM, Kepler
- **Export**: pyarrow, openpyxl, reportlab
- **Server**: Uvicorn (ASGI)

## 기여하기

버그 리포트나 기능 제안은 이슈를 통해 제출해 주세요.

### 개발 환경 설정

```bash
# 1. 포크 및 클론
git clone https://github.com/openkcloud/kcloud-monitor.git

# 2. 브랜치 생성
git checkout -b feature/your-feature

# 3. 변경사항 커밋
git commit -m "Add your feature"

# 4. 푸시 및 PR 생성
git push origin feature/your-feature
```

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
- **문서**: [프로젝트 위키](https://github.com/openkcloud/kcloud-monitor/wiki)

## 감사의 말

- 모든 기여자 분들
- OpenKCloud 커뮤니티
- Prometheus, Kepler, DCGM 프로젝트

---

**KCloud Monitor v0.1.0** | AI 반도체 통합 모니터링 플랫폼

프로젝트 링크: [https://github.com/openkcloud/kcloud-monitor](https://github.com/openkcloud/kcloud-monitor)
