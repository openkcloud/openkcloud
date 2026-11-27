# API 사용 가이드 (v2)

KCloud Monitor v2 API 가이드입니다. `/api/v2` 아래 **155개 라우트**가 등록되어 있고, `GET /openstack/vms/{vm_id}/gpu-passthrough` 하나를 제외한 모든 엔드포인트가 실제 데이터소스(Prometheus, Loki, PostgreSQL, OpenStack API)로 동작합니다.

데이터소스를 설정하지 않으면 영역별로 아래처럼 동작합니다.

| 영역 | 필요한 설정 | 미설정 시 |
|------|------------|----------|
| 대부분의 조회 API | `PROMETHEUS_URL` | 빈 값이면 서버가 기동하지 않음. 주소에 닿지 않으면 빈 결과, `/system/health` 는 `degraded` |
| Logs | `LOKI_URL` | 503 |
| Alerts | `DATABASE_URL` | 503 `ALERT_STORE_NOT_CONFIGURED` |
| Metering · Faults · Recovery | `DATABASE_URL` | 일부 경로 503 (각 섹션 참고) |
| OpenStack | `OPENSTACK_*` | HTTP 200, `status: "partial"`, `warnings: ["NOT_CONFIGURED"]`, 빈 데이터 |

## 목차
- [기본 정보](#기본-정보)
- [인증](#인증)
- [공통 파라미터](#공통-파라미터)
- [공통 응답 정책](#공통-응답-정책)
- [미구현 엔드포인트](#미구현-엔드포인트)
- [엔드포인트 전체 목록](#엔드포인트-전체-목록)
- [SSE 스트리밍](#sse-스트리밍)

## 기본 정보

```
Base URL: http://localhost:8000/api/v2
Swagger:  http://localhost:8000/docs
```

- 경로 약어: `{c}` = cluster, `{n}` = node, `{id}` = accelerator ID, `{pid}` = partition ID, `{ns}` = namespace
- 네이밍: URL 경로 kebab-case(`gpu-passthrough`), 쿼리/JSON 필드 snake_case(`sort_by`, `power_watts`), 단위 접미사(`_watts`, `_percent`, `_celsius`)

## 인증

JWT Bearer 또는 `X-API-Key` 병행 인증입니다. System(`/system/*`)과 Auth(`/auth/*`)만 공개입니다.

```bash
# 1) 로그인 → JWT 발급
curl -X POST http://localhost:8000/api/v2/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<API_AUTH_PASSWORD>"}'

# 2) Bearer 토큰으로 호출
curl -H "Authorization: Bearer <TOKEN>" http://localhost:8000/api/v2/clusters

# (대안) API Key — .env에 API_KEY 설정 시
curl -H "X-API-Key: <KEY>" http://localhost:8000/api/v2/clusters
```

| 엔드포인트 | 설명 |
|-----------|------|
| `POST /auth/login` | JSON 계정(`API_AUTH_USERNAME` / `API_AUTH_PASSWORD`)으로 JWT 발급 |
| `GET /auth/verify` | 토큰 유효성 확인 |

로그인 계정은 `.env` 의 `API_AUTH_USERNAME`, `API_AUTH_PASSWORD` 값입니다. HTTP Basic 로그인(`POST /auth/token`)은 삭제되었습니다.

> 목표 구조는 API Gateway가 발급하는 JWT(RBAC/테넌트 클레임) + service-to-service JWT입니다. 현재 라우터는 Gateway 도입 전 개발용입니다.

## 공통 파라미터

**목록형 API** (`limit`/`offset` 페이징 + 라벨 필터):

| 파라미터 | 기본값 | 설명 |
|---------|--------|------|
| `limit` | `100` | 최대 반환 수 (max `1000`) |
| `offset` | `0` | 페이징 오프셋 |
| `sort_by` / `sort_order` | - / `asc` | 정렬 |
| `project` | - | OpenStack 프로젝트 필터 |
| `namespace` | - | K8s 네임스페이스 필터 |
| `service_name` | - | 논리 서비스 필터 |
| `workload_type` | - | `deployment`, `statefulset`, `job` 등 |
| `status` / `search` | - | 상태 필터 / 텍스트 검색 |
| `cluster` | - | 전역 목록(`/workloads/*`)에서만 — 클러스터 범위 경로에서는 경로 파라미터 |

**시계열 API**:

| 파라미터 | 기본값 | 설명 |
|---------|--------|------|
| `period` | `1h` | 조회 기간 (`start` 미지정 시) |
| `start` / `end` | - / now | ISO 8601 |
| `step` | `5m` | 데이터 포인트 간격 |
| `aggregation` | `avg` | `avg` / `min` / `max` / `sum` |

## 공통 응답 정책

모든 응답(REST, SSE data 이벤트)에 데이터 신선도 필드가 포함됩니다.

| 필드 | 설명 |
|------|------|
| `status` | `success` \| `partial` \| `error` (미구현 엔드포인트: `not_implemented`) |
| `observed_at` | 데이터 수집 시각 (ISO 8601) |
| `warnings[]` | `STALE_DATA`, `PARTIAL_SOURCE`, `ESTIMATED_POWER` 등 (정상 시 생략) |
| `partial_sources[]` | 일부 데이터소스 장애 시 실패 소스 목록 |

에러 스키마:

```json
{
  "status": "error",
  "error": {"code": "VALIDATION_ERROR", "message": "...", "retryable": false},
  "request_id": "req-...",
  "observed_at": "2026-08-06T00:00:00Z"
}
```

전력 응답에는 신뢰도 필드가 추가됩니다: `source`(kepler|dcgm|ipmi|derived), `power_estimation`(direct|attributed|proportional), `attribution.method`.

## 미구현 엔드포인트

아래 엔드포인트는 아직 HTTP 200 + 스텁 응답(`status: not_implemented`, `warnings: ["NOT_IMPLEMENTED"]`)을 반환합니다.

| 엔드포인트 | 사유 |
|-----------|------|
| `GET /openstack/vms/{vm_id}/gpu-passthrough` | libvirt hostdev 연동 전 |

```json
{
  "status": "not_implemented",
  "api": "GET /api/v2/openstack/vms/abc/gpu-passthrough",
  "path_template": "/api/v2/openstack/vms/{vm_id}/gpu-passthrough",
  "description": "VM GPU passthrough 확인(libvirt hostdev)",
  "data_sources": ["libvirt(hostdev)"],
  "data": null,
  "observed_at": "2026-08-06T00:00:00Z",
  "warnings": ["NOT_IMPLEMENTED"]
}
```

## 엔드포인트 전체 목록

`/api/v2` 아래 **155 라우트**입니다. 메서드 표기가 없으면 `GET` 입니다. 정확한 파라미터와 응답 스키마는 Swagger(`/docs`)를 참고하세요.

| 영역 | 수량 |
|------|------|
| Auth / System | 2 / 3 |
| Clusters | 5 |
| Nodes + Hardware(IPMI) | 9 + 3 |
| Accelerators + Partitions | 7 + 4 |
| 별칭(짧은 경로) | 4 |
| Storage (Ceph) | 9 |
| OpenStack | 10 |
| Workloads 클러스터 범위 / 전역 | 11 / 10 |
| Monitoring | 15 |
| Logs | 10 |
| Export / Resource-Map | 3 / 1 |
| Alerts / SLO | 15 / 3 |
| Metering / Faults / Recovery Policies / Recovery | 21 / 3 / 5 / 2 |

### Clusters (5)

| 엔드포인트 | 설명 |
|-----------|------|
| `/clusters` (`type`, `project`, `limit`, `offset`) | 클러스터 목록 (management/service 구분) |
| `/clusters/{c}` | 클러스터 상세 |
| `/clusters/{c}/resource` | 클러스터 자원 현황 |
| `/clusters/{c}/topology` | 계층형 토폴로지 |
| `/clusters/{c}/power` | 클러스터 가속기 전력 합계 [P1] |

### Nodes (9) + Hardware/IPMI (3)

| 엔드포인트 | 설명 |
|-----------|------|
| `/clusters/{c}/nodes` (`node_type` + 목록 파라미터) · `/nodes/{n}` | 노드 목록 / 상세. 목록 항목의 `utilization_percent` (호스트 가속기 사용률 평균)로 호스트맵 색상 결정 |
| `/clusters/{c}/nodes/{n}/metrics` | CPU/MEM/Disk/Net 종합 [M1] |
| `/clusters/{c}/nodes/{n}/cpu` · `/memory` · `/storage` · `/network` | 자원별 상세 |
| `/clusters/{c}/nodes/{n}/power` · `/power/timeseries` | 서버 전력 현재값 / 시계열 [P2] |
| `/clusters/{c}/nodes/{n}/hardware/sensors` | IPMI 센서 전체 (물리 노드 전용) |
| `/clusters/{c}/nodes/{n}/hardware/power` | BMC 전력 실측 [P3] |
| `/clusters/{c}/nodes/{n}/hardware/temperature` | IPMI 온도 |

### Accelerators (7) + Partitions (4)

| 엔드포인트 | 설명 |
|-----------|------|
| `.../nodes/{n}/accelerators` · `/topology` | GPU/NPU 목록 / 가속기 간 연결 토폴로지 |
| `.../accelerators/{id}` · `/metrics` · `/temperature` | 상세 / 실시간 메트릭 [M2] / 온도 |
| `.../accelerators/{id}/power` · `/power/timeseries` | 가속기 전력 [P4] |
| `.../accelerators/{id}/partitions` · `/{pid}` | 파티션(MIG/vGPU/NPU slice) 목록 / 상세 |
| `.../partitions/{pid}/power` · `/power/timeseries` | 파티션 전력 추정 [P5] |

### Storage — Ceph (9)

클러스터 경로 없이 `/storage/ceph/*` 로 조회합니다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/storage/ceph/summary` · `/health` | Ceph 요약 / 상태 점검 상세 |
| `/storage/ceph/capacity` · `/capacity/timeseries` | 용량 / 시계열 |
| `/storage/ceph/osds` · `/osds/{osd_id}` | OSD 목록 / 상세 |
| `/storage/ceph/pools` · `/pools/{pool}` | 풀 목록 / 상세 |
| `/storage/ceph/pgs` | PG 상태 요약 |

### OpenStack (10)

클러스터 경로 없이 `/openstack/*` 로 조회합니다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/openstack/projects` · `/{project_id}` | 프로젝트(과금 단위) 목록 / 상세 |
| `/openstack/hypervisors` · `/{host}` · `/{host}/vms` | 하이퍼바이저 목록 / 상세 / 배치된 VM |
| `/openstack/vms` · `/{vm_id}` · `/{vm_id}/metrics` | VM 목록 / 상세 / 사용량 메트릭 |
| `/openstack/vms/{vm_id}/power` | VM 추정 전력 [P6] |
| `/openstack/vms/{vm_id}/gpu-passthrough` | passthrough 장치 (**미구현, 스텁**) |

### Workloads — 클러스터 범위 (11)

| 엔드포인트 | 설명 |
|-----------|------|
| `/clusters/{c}/workloads/pods` · `/summary` · `/{ns}/{pod}` | Pod 목록 / 집계 / 상세 |
| `/clusters/{c}/workloads/pods/{ns}/{pod}/power` | Pod 추정 전력 [P7] |
| `/clusters/{c}/workloads/pods/{ns}/{pod}/containers` · `/containers/{name}/metrics` | 컨테이너 목록 / 메트릭 |
| `/clusters/{c}/workloads/pods/{ns}/{pod}/accelerators` | Pod 할당 가속기 |
| `/clusters/{c}/workloads/containers` · `/{container_id}` | 클러스터 전역 컨테이너 목록 / 상세 |
| `/clusters/{c}/namespaces` · `/{ns}` | 네임스페이스 목록 / 상세 |

### Workloads — 전역 진입점 (10, 포탈용)

응답에 `_links.self` + `_links.canonical` 포함. 목록은 `cluster` 쿼리로 범위를 좁힐 수 있습니다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/workloads/pods` · `/summary` | 전 클러스터 Pod 목록 / 집계 |
| `/workloads/pods/{c}/{ns}/{pod}` (+ `/power`, `/containers`, `/accelerators`) | canonical 위임 |
| `/workloads/services` | 논리 서비스 목록 (`service_name` 파생 그룹) |
| `/workloads/services/{c}/{ns}/{name}` (+ `/pods`, `/power`) | 서비스 상세 / 소속 Pod / 추정 전력 합계 |

### Monitoring — 횡단 집계 (15)

| 엔드포인트 | 설명 |
|-----------|------|
| `/monitoring/overview` | 전체 인프라 KPI |
| `/monitoring/nodes/timeseries` | 노드 수 시계열 |
| `/monitoring/power/summary` [P8] · `/timeseries` | 전력 요약 / 전체 전력 시계열 |
| `/monitoring/power/breakdown` (`dimension=vendor\|cluster\|node\|accelerator`) | 기준별 전력 분포 |
| `/monitoring/power/efficiency` · `/efficiency/timeseries` | 가속기 전력 효율 / 시계열 |
| `/monitoring/accelerators/utilization` · `/utilization/timeseries` (`by=node`) | 가속기 사용률 / 시계열. `by=node` 면 호스트별 값도 반환 |
| `/monitoring/metrics/query` · `/metrics/timeseries` (`metric`, `by=hostname\|cluster`) [M5] | 메트릭 현재값 / 시계열 (허용 목록 기반). `by=hostname` 이면 호스트별 한 줄, step 구간 평균으로 묶여 히트맵에 바로 쓸 수 있음 |
| `/monitoring/temperature/timeseries` | 가속기 온도 시계열 |
| `/monitoring/throttling/timeseries` (`cluster`, `node`, `acc_id`, `detail`, `bucket`) | 가속기 쓰로틀링 시계열 |
| `/monitoring/stream/power` · `/stream/metrics` | **SSE** 실시간 스트림 |

### Logs — 로그 조회 (10)

Loki 에 LogQL 을 보내 조회합니다. `LOKI_URL` 이 없으면 503. 공통 조건은 `start`, `end`, `limit`, `direction`(`forward`\|`backward`), `cursor`, `log_level`(`error`\|`warning`\|`info`\|`debug`).

| 엔드포인트 | 설명 |
|-----------|------|
| `/logs/search` (`query`, `cluster`) | 로그 검색 (LogQL) |
| `/logs/stream` | 실시간 로그 스트림 (SSE) |
| `/logs/export` (`format=json\|csv`) | 로그 내보내기 |
| `/logs/labels` · `/label-values` (`label`) | 라벨 이름 / 값 목록 |
| `/logs/volume` | 로그 볼륨 통계 |
| `/logs/clusters/{c}/search` | 클러스터 로그 검색 |
| `/logs/clusters/{c}/nodes/{n}/logs` | 노드 시스템 로그 |
| `/logs/clusters/{c}/pods/{ns}/{pod}/logs` (`container`) | Pod 로그 |
| `/logs/clusters/{c}/accelerators/{id}/logs` | 가속기 드라이버 로그 |

### Export (3) / Resource-Map (1) / System (3)

| 엔드포인트 | 설명 |
|-----------|------|
| `/export/power` · `/metrics` (`format=csv\|json`, 기본 `csv`) | 전력 / 메트릭 데이터 내보내기 |
| `/export/report` (`report_type=daily\|weekly\|monthly`) | 운영 요약 리포트 |
| `/resource-map/pods/{pod}` (`cluster`, `namespace`) | Pod → 워크로드 → 가속기 → K8s 노드 → VM → 물리서버 추적 |
| `/system/health` · `/version` · `/metrics` | 헬스 / 버전 / 자체 Prometheus 메트릭 (**공개**) |

### Alerts — 알람 (15)

평가는 서버 안에서 주기적으로(`ALERT_EVAL_INTERVAL_SEC`, 기본 30초) 수행되고, API 는 정책·채널·알람 저장소를 읽고 씁니다. `DATABASE_URL` 이 없으면 모든 경로가 503 `ALERT_STORE_NOT_CONFIGURED`.

| 엔드포인트 | 설명 |
|-----------|------|
| `/alerts/metrics` | 알람 정책에 쓸 수 있는 지표 목록 |
| `GET·POST /alerts/policies` · `GET·PATCH·DELETE /alerts/policies/{policy_id}` | 알람 정책 관리 |
| `POST /alerts/policies/{policy_id}/preview` | 저장된 정책을 지금 값으로 미리 평가 |
| `GET·POST /alerts/channels` · `PATCH·DELETE /alerts/channels/{channel_id}` | 알림 채널 관리 (`email` \| `slack` \| `webhook`) |
| `POST /alerts/channels/{channel_id}/test` | 채널 시험 발송 |
| `/alerts/active` (`state=pending\|firing\|resolved`) | 활성 알람 목록 (기본 pending + firing) |
| `POST /alerts/active/{fingerprint}/ack` | 활성 알람 확인 처리 |
| `/alerts/events` (`policy_id`, `since`, `limit`) | 알람 이력 |

- 정책 종류: `resource`(가속기 메트릭), `log`(로그 단어 건수, Loki), `slo`(vLLM 서빙 지연·대기 요청 수), `composite`(조건 여러 개를 같은 클러스터에서 모두 만족).
- 이메일 채널은 `SMTP_*` 설정이 필요합니다.

### SLO — LLM 서빙 (3)

vLLM 히스토그램(TTFT, TPOT)을 Prometheus 에서 읽어 분위수, 목표 달성률, 남은 오류 예산을 계산합니다. 서빙 하나는 (cluster, model) 쌍으로 구분합니다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/slo/services` (`cluster`, `window`, `ttft_target_ms`, `tpot_target_ms`, `objective_percent`) | SLO 목록 |
| `/slo/services/{c}/{model}` · `/timeseries` | SLO 상세 / 시계열 |

### Metering — 미터링, 단가 정책, 비용 예측 (21)

서버가 매시 가속기 카드별 점유, 사용, 가중 시간과 전력량(kWh)을 계측해 사용량 레코드로 쌓고, 비용은 사용량 x 그날 유효 단가로 계산한다. 금액은 원(`_krw`), 월과 일의 경계는 KST. 지금 계측하는 자원은 가속기뿐이다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/metering/usage/summary` | 기간 합계: 점유, 사용, 가중, 유휴 시간, 실사용률, 전력량, 비용 |
| `/metering/usage/by-resource` · `/by-project` · `/by-cluster` | 자원 유형(벤더, 모델), OpenStack 프로젝트, 클러스터별 사용량과 비용 |
| `/metering/usage/timeseries` (`step=1h\|1d`) | 사용량, 비용 시계열 |
| `/metering/usage/details` (`limit`, `offset`) | 카드 x 1시간 사용량 레코드 원본 |
| `/metering/policies/resource-types` | 과금 자원 유형, 청구 단위, 계약 형태 목록 |
| `GET·POST /metering/policies` · `GET·PUT·DELETE /metering/policies/{policy_id}` | 단가 정책 관리. 덮어쓰지 않고 새 버전 추가, 시작일은 내일 이후(소급 시 409) |
| `POST /metering/policies/simulate` | 같은 사용량에 후보 단가를 대입한 비용 비교 (저장 안 함) |
| `/metering/predictions/monthly` (`method=moving_average\|linear_trend`) | 이번 달 누적 비용과 월말 예측, 신뢰구간, 예산 판정 |
| `/metering/predictions/budget-status` | 예산별 누적 소진율과 월말 초과 예상 |
| `POST /metering/predictions/what-if` | 남은 기간 가속기 구성, 사용 시간을 바꿨을 때의 월말 비용 |
| `GET·POST /metering/budgets` · `GET·PUT·DELETE /metering/budgets/{budget_id}` | 월 예산 관리 (프로젝트, 클러스터 범위) |

공통 조회 조건은 `period`(`current_month` \| `last_month`), `start`, `end`, `project`, `cluster`, `resource_type`.

- `DATABASE_URL` 이 없으면 사용량, 단가 정책, 예산, 예산 현황 경로는 503 `METERING_STORE_NOT_CONFIGURED`. 월말 예측과 가상 시나리오는 Prometheus 에서 점유 시간을 바로 세는 임시 경로로 동작한다.
- 결측은 0 으로 채우지 않는다. 계측되지 않은 구간은 `PARTIAL_HISTORY`, 프로젝트 매핑이 없으면 `NO_PROJECT_MAPPING`(`project` null), 단가가 없으면 `PRICE_NOT_DEFINED`(`unpriced_hours`) 경고가 붙는다.
- 점유 시간은 카드 사용률 지표가 수집된 분의 비율이다. 수집이 끊긴 분과 꺼진 VM 에 붙은 카드(지표 없음)는 점유로 세지 않는다.
- 프로젝트는 클러스터의 Magnum 매핑으로 정하고, 매핑이 없는 단독 VM 클러스터는 카드 호스트와 같은 이름의 VM 이 속한 OpenStack 프로젝트로 정한다. 물리 노드에 꽂힌 카드는 `project` null 이다.
- 단가 정책과 가상 시나리오의 `model` 은 모델 이름에 단어로 들어 있어야 맞는다. `A10` 은 `NVIDIA A10` 에 맞고 `NVIDIA A100` 에는 맞지 않는다.
- 단가 정책 409 사유: `POLICY_BACKDATED`, `POLICY_OVERLAP`, `POLICY_ALREADY_EFFECTIVE`, `POLICY_INVALID_PERIOD`, `POLICY_GAP`(이전 버전을 대신하는 버전에 종료일을 넣어 그 뒤 단가가 비게 되는 수정). 예약된 미래 버전보다 앞에 시작하는 버전은 추가할 수 없어, 뒤 버전을 지우고 시작일 순서대로 다시 넣는다.
- 월말 예측의 `confidence_interval` 은 일평균 계산에 쓴 완결된 날이 1일뿐이면 `null` 이다.
- NPU(RNGD, ATOM) 초기 단가는 공개 단가가 없어 비교 대상 GPU 의 시장 중앙값을 넣은 임시값이다. 정책의 `source` 에 근거가 적혀 있다.

### Faults — 장애 정보 분석 (3)

알람 평가기의 firing 알람과 고정 PromQL 신호를 장애로 분류하고, 장애마다 증거와 원인 분석을 돌려준다. 조회 전용이다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/faults/active` (`cluster`, `fault_type`, `severity`) | 진행 중 장애. critical 먼저 |
| `/faults/history` (`start`, `status`, `limit`) | 장애 이력. 알람 이력의 발생, 해제 기록으로 만든다 (기본 7일) |
| `/faults/{fault_id}` | 장애 상세: 증거(`evidence`)와 원인 분석(`analysis`) |

- 탐지 경로는 두 가지다. `alert` 는 알람 정책 중 장애로 보는 지표(가속기 상태 이상, 온도, 수집 중단, XID 로그, OOM 로그, 재시작 반복 로그, 하드웨어와 디스크 오류 로그)이고, `signal` 은 정책 없이도 보는 고정 신호(`xid`, `oom_killed`, `restart_loop`, `npu_dead`, `npu_unhealthy`, `node_not_ready`, `vm_error`)다. NPU 는 XID 가 없어 Furiosa 는 `furiosa_npu_alive` 0, Rebellions 는 `RBLN_DEVICE_STATUS:HEALTH` 가 0 이 아닌 카드를 본다. 사용률, 메모리 포화, SLO 알람은 장애로 보지 않는다.
- 장애 유형은 `accelerator_error`, `npu_fault`, `workload_oom`, `workload_crash_loop`, `node_not_ready`, `vm_down`, `hardware_fault`.
- 증거는 발생 30분 전부터 10분 뒤까지의 메트릭, 관련 로그 최대 20줄(XID 코드, OOM 프로세스 같은 추출 필드 포함), 대상에서 물리서버까지의 경로와 영향 Pod 다. 종류별 수집 결과는 `evidence.collection_status` 에 있고, 일부가 빠지면 `EVIDENCE_PARTIAL` 경고가 붙는다.
- NPU 장애의 로그 증거는 드라이버 패턴에 더해 오류 단어(error, fatal, panic)가 있는 줄만 남긴다. 쓰로틀 메트릭은 사유(`labels.label`)별 시계열로 나온다.
- 영향 Pod 는 대상의 K8s 노드에서 센다. VM 장애는 같은 이름의 K8s 노드가 있는 서비스 클러스터에서 찾는다. K8s 노드가 아닌 단독 VM 은 `scope` 가 `vm` 이고 Pod 수는 null 이다. GPU 장애는 exporter 가 카드별 Pod 를 알려 주면 그 카드의 Pod 만(`scope` `accelerator`), 알려 주지 않으면 노드의 Pod 전체(`scope` `node`)다.
- 원인 분석은 규칙으로 낸 추정이다. `confidence` 는 원인을 직접 가리키는 증거가 있으면 `high`, 탐지 신호뿐이면 `medium`, 증거를 수집하지 못했으면 `low`. `evidence_basis` 에는 장애를 잡은 탐지 경로(`alert`, `signal`)와 판단에 쓴 증거(`logs`, `metrics`)가 들어간다.
- `DATABASE_URL` 이 없으면 `/faults/active` 는 고정 신호만 보고(`ALERT_STORE_NOT_CONFIGURED` 경고), `/faults/history` 는 503. 고정 신호 장애는 신호가 사라지면 이력에 남지 않는다.

### Recovery Policies — 후속 조치 정책 (5)

장애 유형별로 어떤 절차를 권고할지 적어 둔 정책을 관리한다. 정책은 권고 전용(`mode` 는 항상 `recommendation-only`)이고 이 API 는 조치를 실행하지 않는다.

| 엔드포인트 | 설명 |
|-----------|------|
| `GET·POST /recovery/policies` (`fault_type`, `enabled`) | 정책 목록, 생성 |
| `GET·PUT·DELETE /recovery/policies/{policy_id}` | 정책 상세, 전체 교체, 삭제 |

- 정책은 적용 조건(`fault_type`, `vendor_scope`, `error_codes`)과 조치 절차(`steps`)로 이뤄진다. 한 장애에 여러 정책이 맞으면 XID 코드를 지정한 정책, 벤더를 지정한 정책 순으로 우선한다.
- 절차 단계는 `manual_review`(확인), `manual_action`(관리자가 직접 실행, `risk_level` 필수), `record_feedback`(결과 기록) 세 종류다.
- `steps[].reference_queries` 에는 `{fault_id}`, `{cluster}`, `{node}`, `{namespace}`, `{pod}`, `{container}`, `{accelerator_id}`, `{vm_id}` 자리를 쓸 수 있고, 복구 정보를 만들 때 장애 대상 값으로 채워진다.
- 첫 기동 때 초기 정책 7건(GPU 오류, NPU 오류, 메모리 부족 종료, 재시작 반복, 노드 NotReady, VM 비정상, 서버 하드웨어 오류)이 들어간다. `DATABASE_URL` 이 없으면 초기 정책을 읽기 전용으로 돌려주고(`RECOVERY_STORE_NOT_CONFIGURED` 경고) 생성, 수정, 삭제는 503.
- 규칙 위반은 422, `detail.errors` 에 `RISK_LEVEL_REQUIRED`, `UNKNOWN_PLACEHOLDER` 등.

### Recovery — 복구 정보 (2)

진행 중 장애에 후속 조치 정책을 대입해 복구 절차와 관련 정보를 만든다. 저장하지 않고 조회할 때마다 만들며, 조치를 실행하지 않는다.

| 엔드포인트 | 설명 |
|-----------|------|
| `/recovery/recommendations` (`cluster`, `fault_type`, `priority`) | 장애별 복구 정보 요약. `urgent` 먼저 |
| `/recovery/recommendations/{recommendation_id}` | 복구 정보 상세 |

- 상세는 추정 원인(`probable_cause`, `confidence`), 조치 절차(`recommended_actions`, 참고 경로가 그 장애의 대상 값으로 채워짐), 영향 범위(`affected_scope`: 가속기, Pod, 워크로드, 물리서버까지의 경로), 조치 뒤 확인 방법(`verification`)을 담는다.
- 우선순위는 장애 심각도에서 나온다. `critical` 은 `urgent`, `warning` 은 `normal`.
- 영향 범위를 정하지 못하면 `affected_scope.status` 가 `unknown` 이고 수치는 null 이다 (0 과 구분). 단독 VM 처럼 Pod 로 셀 수 없는 대상은 `status` 가 `ok` 여도 `pod_count`, `workloads` 가 null 이다.
- 맞는 정책이 없는 장애는 목록에서 빠지고 `NO_POLICY_MATCHED` 경고가 붙는다. 상세는 404.
- 검토 상태(수락, 거절)와 조치 결과는 저장하지 않는다.

### 별칭 — 단축 경로 (4)

UUID 직접 조회용. 응답 `_links.canonical`로 정규 경로 안내.

| 별칭 | canonical |
|------|-----------|
| `/clusters/{c}/accelerators/{id}` | `/clusters/{c}/nodes/{n}/accelerators/{id}` |
| `/clusters/{c}/accelerators/{id}/partitions/{pid}` | `.../accelerators/{id}/partitions/{pid}` |
| `/clusters/{c}/pods/{ns}/{pod}` | `/clusters/{c}/workloads/pods/{ns}/{pod}` |
| `/clusters/{c}/containers/{id}` | `/clusters/{c}/workloads/containers/{id}` |

## SSE 스트리밍

v1 WebSocket은 폐기되고 SSE로 통일되었습니다.

- `Content-Type: text/event-stream`, 15초마다 `heartbeat` 이벤트(`observed_at`만 포함) 다음에 데이터 이벤트 1개를 보내며, 클라이언트가 연결을 끊을 때까지 반복합니다.
- 데이터 이벤트 이름은 `/monitoring/stream/power` 가 `power`(전력 요약), `/monitoring/stream/metrics` 가 `metric`(`metric` 으로 지정한 메트릭 현재값)입니다.
- 조회에 실패하면 `error` 이벤트(`{"error": "poll_failed", "observed_at": ...}`)를 보내고 스트림은 계속됩니다.
- 이벤트마다 `id` 가 붙지만, `Last-Event-ID` 헤더로 이어 받는 기능은 아직 없습니다.

```bash
curl -N -H "Authorization: Bearer <TOKEN>" \
  http://localhost:8000/api/v2/monitoring/stream/power
```

실시간 로그 스트림은 `/logs/stream` 을 사용합니다.
