# 아키텍처 개요 (v2)

KCloud Monitor v2의 아키텍처 개요입니다. v1(7-도메인 평면 구조, 단일 Prometheus)은 프로토타입으로 종료되었고, v2는 계층형 자원 모델 기반으로 재설계되었습니다.

## 시스템 위치

KCloud Monitor는 OpenKCloud 관측 플랫폼의 6개 서비스(Monitor / Logger / Alerter / Metering / Healer / AI Engine) 중 **자원·전력 모니터링(Monitor)** 을 담당하는 서비스입니다. 이 저장소는 Monitor만 다룹니다.

- 담당 범위: 이기종 AI 반도체 인프라 자원 모니터링(OPT.001) + 물리 노드·워크로드 전력 모니터링(OPT.002)
- resource-map(자원 계보 원장)은 Monitor가 소유하며, Metering/Alerter/Healer가 내부 API로 조회합니다.

## 6계층 자원 모델

물리 하드웨어부터 AI 워크로드 컨테이너까지 6개 계층 + 1개 가속기 계층으로 구성됩니다.

| 계층 | 실제 객체 | 관리 관점의 의미 |
|------|----------|----------------|
| **Layer 0** | 물리 서버 | 최상위 하드웨어 호스트, BMC/IPMI 대상 |
| **Layer 1** | 관리 클러스터 노드 | 물리 서버의 Kubernetes 표현 (동일 실체) |
| **Layer 2** | OpenStack Pod | 관리 클러스터 위에서 실행되는 OpenStack 제어면 |
| **Layer 2b** | Nova compute | 물리 서버 위 하이퍼바이저 프로세스 |
| **Layer 3** | OpenStack VM | 서비스 클러스터 노드의 실체 |
| **Layer 4** | 서비스 클러스터 노드 | VM 내부의 Kubernetes Node |
| **Layer 5** | 서비스 Pod / 컨테이너 | AI 서비스 실행 단위 (GPU 할당은 **컨테이너** 단위) |
| **Layer A** | GPU / NPU | Layer 0에 부착되고 Layer 3/4/5에서 소비되는 가속 자원 |

**핵심 원칙**:
- GPU/NPU는 물리 서버에 부착(`attached_to`)되고, VM으로 패스스루(`passthrough_to`)되며, 최종적으로 컨테이너에 할당(`allocated_to`)됩니다.
- 서비스 클러스터는 Magnum이 OpenStack **프로젝트** 단위로 생성합니다(프로젝트 = 과금 단위).
- OpenStack 자원은 관리 클러스터에만 존재하며, 클러스터 경로 없이 `/openstack/*` 로 조회합니다.

```
Physical Server (L0) ──── GPU/NPU (LA)
     │                       │
     ├── Mgmt K8s Node (L1)  │ passthrough_to
     │                       ▼
     └── OpenStack VM (L3) ◄─┘
             │
             └── Service K8s Node (L4)
                     │
                     └── AI Service Pod (L5)
                             │
                             └── Container ◄── allocated_to ── GPU/NPU
```

## 데이터소스 (v1 → v2)

v1은 단일 Prometheus만 사용했습니다. v2는 멀티 클러스터 + OpenStack을 다루므로 다중 백엔드로 확장합니다.

| 백엔드 | 용도 | 질의 | 상태 |
|--------|------|------|------|
| **Prometheus** | 메트릭 조회 (`PROMETHEUS_URL`). 클러스터는 `cluster` 라벨로 구분 | PromQL | ✅ 사용 중 |
| **Loki** | 로그 검색·스트림, 로그 알람 (`LOKI_URL`) | LogQL | ✅ 사용 중 |
| **PostgreSQL** | 알람 정책·이력, 미터링, 장애 이력, 후속 조치 정책 (`DATABASE_URL`). resource-map 원장(자원 계보·이력)도 여기에 둘 예정 | SQL | ✅ 사용 중 |
| **OpenStack API** | VM·하이퍼바이저·프로젝트 메타데이터, VM 전력 귀속 매핑 (`OPENSTACK_*`) | REST | ✅ 사용 중 |
| **Mimir** | 중앙 메트릭 저장소 — 각 클러스터의 Alloy가 remote_write. PromQL 호환이라 `PROMETHEUS_URL` 로 지정 | PromQL | 📋 목표 구조 |
| **Redis(+Streams)** | 캐시 + 서비스 간 이벤트 버스 | — | 📋 목표 구조 |
| **libvirt** | GPU passthrough 확정 | — | 📋 미구현 |

수집 exporter(클러스터 측): DCGM(GPU), Furiosa Metrics Exporter(NPU), Kepler(전력), node-exporter, ipmi-exporter(BMC), kube-state-metrics, cAdvisor, rook-ceph-mgr/exporter(`ceph_*`).

## URL 설계 원칙 (`/api/v2`)

1. **계층형 canonical**: 모든 자원은 클러스터에서 시작 — `/clusters/{c}/nodes/{n}/accelerators/{id}/partitions/{pid}`
2. **전역 진입점**: 포탈용 인덱스 `/workloads/pods`, `/workloads/services` — 응답에 `_links.self` + `_links.canonical` 필수
3. **별칭(단축 경로)**: UUID 직접 조회용 4종(가속기/파티션/Pod/컨테이너) — canonical로 연결
4. **벤더 중립**: GPU/NPU는 `accelerators`로 통합, MIG/vGPU/NPU slice는 `partitions`로 통합
5. **스트리밍은 SSE 통일**: v1 WebSocket 폐기, `text/event-stream` + 15초 heartbeat + `Last-Event-ID` 재개

## 전력 계층 (P1~P8)

전력은 **물리 계층에서 실측**하고 상위 가상 계층으로 **귀속(attribution)** 합니다. 스택: `물리 서버 → 관리 K8s(물리 위, 실측) → OpenStack → Magnum 서비스 K8s(VM) → Pod`.

| # | 대상 | 방식 | 엔드포인트 |
|---|------|------|-----------|
| P1 | 클러스터 합계 | 집계 | `/clusters/{c}/power` |
| P2 | 노드 (Kepler/RAPL) | 실측·귀속 | `/clusters/{c}/nodes/{n}/power` |
| P3 | 노드 (IPMI BMC) | **실측** | `/clusters/{c}/nodes/{n}/hardware/power` |
| P4 | 가속기 (DCGM) | **실측** | `.../accelerators/{id}/power` |
| P5 | 파티션 (MIG/vGPU) | 비례 추정 | `.../partitions/{pid}/power` |
| P6 | OpenStack VM | 귀속(QEMU CPU time) | `/openstack/vms/{vm_id}/power` |
| P7 | Pod | 귀속(Kepler / VM 재배분) | `/clusters/{c}/workloads/pods/{ns}/{pod}/power` |
| P8 | 시스템 전체 | 집계 | `/monitoring/power/summary` |

모든 전력 응답은 신뢰도를 표기합니다:

| 필드 | 값 |
|------|-----|
| `source` | `kepler` \| `dcgm` \| `ipmi` \| `derived` |
| `power_estimation` | `direct`(실측) \| `attributed`(귀속) \| `proportional`(비례 추정) |
| `attribution.method` | `measured_ipmi`, `measured_rapl`, `measured_dcgm`, `attributed_cpu_time`, `host_power_attribution`, `vm_power_split`, `nova_vm_mapping`, `estimated_proportional` |

## Resource-Map (자원 계보)

GPU→VM→Pod 교차 추적을 위한 원장입니다.

현재는 `GET /resource-map/pods/{pod}` 하나가 구현되어 있으며, 원장 없이 Prometheus 라벨(`kube_pod_info`, `kube_node_info.system_uuid`, `openstack_nova_server_status`)을 이어 붙여 Pod → 워크로드 → 가속기 → K8s 노드 → VM → 물리서버를 추적합니다. 아래 우선순위에 따른 원장 기반 확정은 구현 예정입니다.

attachment 확정 근거는 우선순위를 따릅니다:

| 순위 | 소스 | 용도 |
|------|------|------|
| 1 | nova-compute / libvirt hostdev | GPU/NPU ↔ VM attachment **확정 경로** |
| 2 | Nova Placement API | `pci.report_in_placement` 활성 환경에서만 (현 환경 OFF 확정) |
| 3 | sysfs / PCI / driver binding | `vfio-pci` 바인딩 보조 근거 |
| 4 | guest CLI (`nvidia-smi` 등) | VM 내부 인식 확인 |
| 5 | runtime inspect / CDI / DCGM PID | **컨테이너 할당 확정** |
| 6 | intent (flavor/device_spec) | 준비 상태 판단 |
| 7 | log / journal | 보조 증거 (단독 확정 금지) |

## 공통 응답 정책

- `status`: `success` | `partial`(+`warnings[]`, `partial_sources[]`) | `error`
- 모든 응답에 `observed_at`(수집 시각)
- 에러 스키마: `{status, error: {code, message, retryable}, request_id, observed_at}`
- 모든 응답 헤더에 `X-Request-ID`(상관관계 추적)

**성능 목표(NFR)**: 일반 조회 P95 ≤ 2초, 무거운 집계(토폴로지/대형 summary) P95 ≤ 5초, SSE 첫 이벤트 ≤ 5초, 실시간 메트릭 지연 ≤ 60초, resource-map 갱신 지연 ≤ 5분.

## 현재 구현 상태

| 구성 요소 | 상태 |
|-----------|------|
| v2 라우팅·인증·경로 구조 (155 라우트) | ✅ 동작 |
| 미들웨어 (Request-ID, 자체 메트릭, 레이트리밋, CORS) | ✅ 동작 |
| System 헬스/버전/메트릭 | ✅ 동작 |
| Clusters·Nodes·Accelerators·Storage(Ceph)·OpenStack·Workloads·Monitoring·Export 조회 | ✅ 동작 |
| Logs(Loki)·Alerts·SLO·Metering·Faults·Recovery | ✅ 동작 |
| 데이터소스 클라이언트 (Prometheus/Loki/PostgreSQL/OpenStack) | ✅ 동작 |
| VM GPU passthrough 확인 (libvirt) | 📋 미구현 (스텁 응답) |
| resource-map 원장(가속기·VM 계보, 이력, Discovery), 전력 귀속 recording rules | 📋 미구현 |
| Mimir·Redis 연동 | 📋 목표 구조 |
