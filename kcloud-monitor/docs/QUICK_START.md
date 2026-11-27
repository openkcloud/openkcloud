# Quick Start Guide (v2)

KCloud Monitor v2의 빠른 시작 가이드입니다.

> `/api/v2` 아래 155개 라우트가 실제 데이터소스(Prometheus, Loki, PostgreSQL, OpenStack API)로 동작합니다.
> `PROMETHEUS_URL` 은 필수입니다. 나머지 데이터소스를 설정하지 않은 영역은 503을 반환하거나, `NOT_CONFIGURED` 경고와 함께 빈 데이터를 반환합니다. 영역별 조건은 [API_GUIDE.md](./API_GUIDE.md)를 참고하세요.

## 설치 및 실행

```bash
# 1. 가상환경 및 의존성
python3.12 -m venv .venv
source .venv/bin/activate   # Windows: .venv\Scripts\activate
pip install -r requirements.txt

# 2. 환경 변수
cp .env.example .env
# .env 에서 최소한 아래 값을 수정
#   PROMETHEUS_URL       메트릭 조회 대상 (필수, 비워 두면 서버가 기동하지 않음)
#   API_AUTH_PASSWORD    로그인 비밀번호
#   JWT_SECRET_KEY       JWT 서명 키
# 필요한 영역만 추가로 설정
#   LOKI_URL             로그 API
#   DATABASE_URL         알람·미터링·장애·복구 저장소 (PostgreSQL)
#   OPENSTACK_*          OpenStack API

# 3. 서버 실행
python run.py --port 8000
# 또는: uvicorn app.main:app --host 127.0.0.1 --port 8000 --reload
```

## 기본 사용법

### 1) 헬스체크 (공개)

```bash
curl http://localhost:8000/api/v2/system/health
```

```json
{"status": "healthy", "backends": {"prometheus": "connected"}, "observed_at": "..."}
```

Prometheus 에 닿지 않으면 `status` 가 `degraded`, `backends.prometheus` 가 `unreachable` 입니다.

### 2) 로그인 → JWT

`.env` 의 `API_AUTH_USERNAME` / `API_AUTH_PASSWORD` 로 로그인합니다.

```bash
TOKEN=$(curl -s -X POST http://localhost:8000/api/v2/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<API_AUTH_PASSWORD>"}' | python -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
```

### 3) API 호출

```bash
# 클러스터 목록
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/api/v2/clusters

# 노드 가속기 목록 (계층형 canonical 경로)
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8000/api/v2/clusters/mgmt/nodes/w1/accelerators?limit=10"

# 전력 요약 [P8]
curl -H "Authorization: Bearer $TOKEN" http://localhost:8000/api/v2/monitoring/power/summary
```

### 4) SSE 스트림 (15초마다 heartbeat + 데이터 이벤트, 연결을 끊을 때까지 계속)

```bash
curl -N -H "Authorization: Bearer $TOKEN" \
  http://localhost:8000/api/v2/monitoring/stream/power
```

## API 문서

- Swagger UI: http://localhost:8000/docs — 영역별 태그, 한국어 설명 포함
- ReDoc: http://localhost:8000/redoc
- 전체 라우트 표: [API_GUIDE.md](./API_GUIDE.md)

## Docker

`docker-compose.yml` 은 API(`api`)와 PostgreSQL(`db`) 두 서비스를 띄웁니다. 아래 값이 비어 있으면 compose 가 기동을 거부하므로 `.env` 에 먼저 채워야 합니다.

- `API_AUTH_USERNAME`, `API_AUTH_PASSWORD`
- `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` (`db` 서비스용. 외부 PostgreSQL 을 쓰려면 `.env` 에 `DATABASE_URL` 을 추가하면 API 는 그쪽에 연결)

```bash
cp .env.example .env
# .env 수정 후
docker-compose up -d
curl http://localhost:8000/api/v2/system/health
```

## 문제 해결

| 증상 | 원인/해결 |
|------|----------|
| 401 Unauthorized | 토큰 누락/만료 — `/auth/login` 재발급, 또는 `X-API-Key` 헤더(.env `API_KEY` 설정 시) |
| 로그인 실패 | `.env` 의 `API_AUTH_USERNAME` / `API_AUTH_PASSWORD` 값으로 로그인했는지 확인 |
| `/system/health` 가 `degraded` | `PROMETHEUS_URL` 이 맞는지, 서버에서 접근 가능한지 확인 |
| `/logs/*` 가 503 | `LOKI_URL` 미설정 |
| `/alerts/*` 가 503 `ALERT_STORE_NOT_CONFIGURED` | `DATABASE_URL`(또는 compose 의 `POSTGRES_*`) 미설정 |
| `docker-compose up` 이 변수 오류로 중단 | `.env` 의 `API_AUTH_*`, `POSTGRES_*` 가 비어 있음 |
| 응답이 `not_implemented` | `/openstack/vms/{vm_id}/gpu-passthrough` 는 아직 미구현 |
