# 서비스 클러스터 신청 — 구현 계획

작성일: 2026-10-20 · 설계: `docs/superpowers/specs/2026-10-19-paas-cluster-request-design.md`
상태: **결정 1~5 확정(2026-10-20)** — 0단계 완료, 1단계부터 진행
범위: **AI 워크스페이스 프로비저닝·MNT-AINB 는 제외**(사용자 결정 2026-10-20) — `ai_workspace_provision.py`·`init_steps_ainb` 는 손대지 않는다.

단계마다 따로 시험·커밋한다(커밋은 사용자). SQL 은 사용자가 직접 적용한다.
배포 순서: SQL → CORE → IAAS → PAAS → APP (`build-push.sh` 전체 빌드 + `helm upgrade`, 빌드 전 `master3` 에서 `git pull`).

---

## 설계와 다르게 가야 하는 곳 (코드 조사 결과)

| 설계 문서 | 실제 코드 | 계획 |
|---|---|---|
| CORE `_paas()` 는 `_ainb()` 와 같은 방식 | `_ainb()` 는 인증 헤더를 안 보낸다. PAAS 는 `465b4f4` 부터 모든 API 에 Bearer 토큰 필요 | `_iaas()` 처럼 **사용자 토큰을 넘기는** `_paas(path, token)` |
| 이름 중복 = 모든 클러스터 | PAAS `GET /clusters` 는 일반 사용자에게 접근 가능한 클러스터만 준다 | PAAS 에 `GET /cluster-name-check` 새로 (결정 1) |
| 쿼터는 IAAS `get_project_quota` 재사용 | CORE 는 쿼터를 어디서도 부르지 않는다. 인스턴스 수까지 주는 IAAS API 는 `/placement-preview`(관리자) 하나 | 결정 2 |
| 네트워크 확인은 IAAS `fip-reachable` | 워크플로에서 부를 IAAS 작업이 없다 | IAAS 작업 `iaas.check_network` 새로 (결정 3) |
| 템플릿 `kubeVersion` = 이미지 `kube_version` | PAAS 는 Magnum 주소만 안다(Glance 호출 수단 없음) | 결정 4 |
| `_workspace_name` 에 `default`·`kube-` 제외 | 지금 함수에는 없다. AI 워크스페이스도 이 함수를 쓴다 | 클러스터용 `_namespace_name()` 이 감싸서 제외(AI 동작 그대로) |
| 프로비저닝 화면에서 `fip-reachable` 표시 | VM 화면도 표시 안 함(서버에서만 확인, 실패 시 오류 토스트) | 같은 방식: 서버 확인 + DHCP 꺼진 서브넷만 화면에서 비활성 |

## 결정 필요

1. **이름 중복 확인** — **확정**: PAAS `GET /api/paas/cluster-name-check?name=` (로그인만 확인, 안에서는 admin 스코프로
   모든 Magnum 클러스터를 본다 — 이름만 돌려주므로 다른 프로젝트 정보는 새지 않는다). CORE 이름 확인은 이것 + DB 진행 중 신청.
2. **쿼터** — **확정**: 프로비저닝 API 는 VM 처럼 IAAS `/placement-preview` 의 `quota` 로 미리 확인,
   워크플로 `check_quota` 단계는 IAAS 작업 `iaas.check_quota` 새로(실행 때 다시 확인).
3. **네트워크 확인 작업** — **확정**: IAAS 작업 `iaas.check_network(networkId, subnetId, floatingNetworkId)` 새로
   (서브넷 DHCP + `networks.fip_reachable` 재사용).
4. **템플릿 K8s 버전** — **확정**: PAAS 가 같은 토큰으로 Glance 이미지 속성(`kube_version`)을 읽는다(템플릿 5개, 이미지 ID 별 캐시).
5. **종료 유예** — **확정**: 차트는 그대로(파드 기본 30초). PAAS 워커의 작업 대기(`graceful_shutdown_timeout`)는 **25초**.
   생성 요청은 몇 초면 끝나고, 대기 작업은 끊겨도 하트비트로 새 파드에서 다시 실행된다.
   IAAS 워커도 90초(실제로는 30초에 잘림) → **25초로 맞춘다**(1단계, 사용자 결정 2026-10-20).

범위 밖(기록만): AI 워크스페이스 프로비저닝의 관리자 확인이 본문 `processedBy` 기준(보안 문제), IAAS 권한 시험표 누락 6개(기존 실패).

---

## 0단계 — 공통코드 SQL·문서

- `MNT-CORE/db/paas_cluster_request.sql`(새): `TKT_PROVISION_STEP_PAAS`·`TKT_PROVISION_SUBSTEP_PAAS` 그룹 + 단계 코드
  (설계 3절 표, `db/ai_workspace.sql` 형식, `ON CONFLICT DO NOTHING`). **운영 DB 적용은 사용자.**
- `MNT-CORE/tests/run_tests.sh`: 시험 DB 적용 목록에 `paas_cluster_request.sql` 추가.
- 설계 문서 4절을 위 '다르게 가야 하는 곳' 으로 고친다.

## 1단계 — MNT-IAAS

| 파일 | 내용 |
|---|---|
| `app/routers/flavors.py` | `GET /projects/{project_id}/flavors` (`project_member`) — Nova `/flavors/detail?is_public=None` → 공개 + `os-flavor-access` 에 그 프로젝트가 있는 비공개. 응답은 `/flavors` 와 같은 `_to_flavor`. 선례: `routers/images.py` `GET /projects/{id}/images` |
| `app/workflows/activities.py` | `iaas.check_quota(projectId, need)` — `get_project_quota` 로 인스턴스·vCPU·RAM 여유 확인, 부족하면 `_fail("…")`, 표시 값 반환 |
| | `iaas.check_network(networkId, subnetId, floatingNetworkId)` — 서브넷이 그 망 소속·DHCP 켜짐 + `fip_reachable` 가능, 표시 값 '망 이름 / CIDR' 반환 |
| `app/workflows/runtime.py` | `GRACEFUL_SHUTDOWN` 90초 → 25초(결정 5), 주석 수정 |
| `tests/test_permissions.py` | 시험표에 `GET /projects/{MINE|OTHER}/flavors` 2줄 |
| `tests/test_project_flavors.py`(새) | 가짜 `_nova` 로: 공개 + 허용 비공개만, 다른 프로젝트 비공개 제외 |
| `tests/test_cluster_activities.py`(새) | `check_quota`·`check_network` — 가짜 쿼터·Neutron 으로 충분/부족, DHCP 꺼짐, FIP 불가 |

확인: IAAS 시험 전체(기존 실패 1건 = 시험표 누락 6개 그대로인지).

## 2단계 — MNT-PAAS (워커·작업·이름 확인)

| 파일 | 내용 |
|---|---|
| `requirements.txt` | `temporalio>=1.34` (IAAS 와 같은 표기) |
| `app/config.py` | `temporal_api_base`, `cluster_availability_zone="az1"` |
| `app/openstack_client.py` | 같은 토큰으로 다른 서비스 조회하는 `os_get(service, path)` — Nova(Worker VM 수), Glance(결정 4). override·catalog 두 방식 |
| `app/routers/clusters.py` | `GET /cluster-name-check?name=` (로그인) → `{available, reason}` — 규칙(소문자 시작·소문자·숫자·하이픈·40자) + 모든 클러스터 이름 |
| `app/routers/templates.py` | 요약에 `kubeVersion` (결정 4) |
| `app/workflows/activities.py`(새) | `paas.check_name(name)` · `paas.create_cluster(projectId, spec)`(소유 프로젝트 스코프 POST, 재시도 없음) · `paas.watch_cluster(clusterId, last, maxSeconds)`(IAAS `watch_server` 방식: 바뀌면 즉시·아니면 최대 60초 뒤 반환, 하트비트. 반환 = 상태·사유·API 주소(`https://:0` 은 없음으로)·Control Plane 노드그룹 완료·Worker VM ACTIVE 수) · `paas.create_namespace(clusterId, name, projectId, projectName)`(있으면 라벨이 같은 프로젝트인지 확인 — 멱등) · `paas.delete_cluster(projectId, name, clusterId, deadline)`(ID 또는 이름으로 찾아 삭제, 404·`DELETE_COMPLETE` = 삭제됨, 하트비트) |
| `app/workflows/runtime.py`(새) | 워커(큐 `okc-paas`) — `graceful_shutdown_timeout` 25초(결정 5), 실패 시 5초 뒤 재시도 |
| `app/main.py` | lifespan 에서 `temporal_api_base` 있을 때만 워커 시작, 종료 때 기다림 |
| `config.env.example`·`deploy/all.yaml`·차트 `values.yaml` | `TEMPORAL_API_BASE` |
| `tests/conftest.py` | `TEMPORAL_API_BASE=""` |
| `tests/test_permissions.py` | 시험표에 `/cluster-name-check` |
| `tests/test_cluster_activities.py`(새) | 가짜 Magnum·K8s 로 각 작업: 생성 요청 본문(템플릿·노드 수·플레이버·망·`availability_zone` 라벨·`master_lb_enabled`), 상태 변화 반환, 네임스페이스 409 처리(같은/다른 프로젝트), 삭제 대기 |

확인: PAAS 시험 전체.

## 3단계 — MNT-CORE (워크플로·API)

**3-1. 워크플로 공통 파일 분리 (동작 변화 없음)**
- `app/workflows/provision_base.py`(새): `_idx`·`_go`·`_set_values`·`_fail_current`·`_within`·`_core` 를 기반 클래스로,
  `reason(e, worker)` 와 공용 상수(`CORE_QUEUE`·`QUERY_RETRY`·`NO_RETRY`·`DB_RETRY`·`HEARTBEAT`·`END`).
  샌드박스에서 돌므로 `asyncio`·`datetime`·`temporalio` 외 import 금지(시험은 샌드박스를 안 써서 못 잡는다 — 직접 확인).
- `vm_provision.py` 가 이를 쓰게 바꾼다(`ai_workspace_provision.py` 는 범위 밖 — 그대로 둔다). activity 이름·순서·인자 그대로.
  다른 곳이 쓰는 이름(`CORE_QUEUE`·`IAAS_QUEUE`·`QUEUE_WAIT_MAX`·`ADMIT_POLL`·`RUN_MAX`·`PROVISION_MAX`)은 `vm_provision` 에 그대로 둔다
  (`_iaas`·`_admit` 은 시험이 monkeypatch 하는 모듈 전역을 실행 때 읽어야 하므로 각 파일에 남김).
- 확인: `test_vm_provision.py`·`test_provision_queue.py` 통과(Temporal CLI·Docker DB 필요).

**3-2. 클러스터 워크플로**
- `activities.py`: `init_steps_paas` 추가(`init_steps_ainb` 와 같은 방식, 그룹 `TKT_PROVISION_STEP_PAAS`·`TKT_PROVISION_SUBSTEP_PAAS` 명시 — `init_steps_ainb` 는 그대로).
  `grant_cluster_access(projectId, clusterId)` — `WHERE NOT EXISTS (… namespace IS NULL)` 로 멱등.
- `kubernetes_provision.py`(새) `KubernetesProvision`: 설계 3절 단계 순서.
  `check_name`(paas) → `check_quota`(iaas) → `check_network`(iaas) → `send_request`(paas, 재시도 없음) →
  `create_control_plane`·`create_workers`(paas `watch_cluster` 반복, 생성 대기 45분) → `check_api`(paas) → `create_namespace` →
  `grant_access`(core) → 완료. 실패 시 이번 회차가 만든(또는 만들었을 수 있는) 클러스터를 `paas.delete_cluster` 로 정리(15분).
  시간 값은 설계 3절 임시 값.
- `runtime.py`: `start_cluster_provision`, 워커에 `KubernetesProvision` 등록.

**3-3. API (`app/routers/tickets.py`)**
- `_paas(path, token)` — `_iaas` 와 같은 오류 처리.
- `GET /tickets/cluster-name-check?name` → `{available, reason}` (규칙 → PAAS 이름 확인 → DB 진행 중 신청, 모든 프로젝트 기준).
- `POST /tickets/cluster` — `_check_requestable` → 프로젝트·템플릿(공개·숨김 아님)·플레이버(프로젝트 플레이버 API)·
  가속기 규칙·노드 수(Control Plane 1|3, Worker 1~20)·이름 → `request_spec`(설계 1절 키 + 희망 네트워크) 저장.
- `POST /tickets/{id}/provision-cluster` — **토큰 사용자로 관리자 확인**(`_admin_id(db, user.username)`, VM 방식),
  승인·실패 상태, 망·서브넷(DHCP·`fip-reachable`), 플레이버·가속기·노드 수, 이름 재확인, 쿼터(결정 2) →
  회차·`cluster-provision-{id}-{n}` → 시작 실패 시 되돌림.
- VM `create_ticket` 플레이버 확인: `/flavors` + `isPublic` → `/projects/{pid}/flavors`.

**3-4. 시험**
- `tests/test_cluster_ticket.py`(새): 신청·이름 확인·프로비저닝 API(가짜 `_iaas`·`_paas`·`runtime`), 관리자 아님 403, DHCP 꺼진 서브넷 400.
- `tests/test_kubernetes_provision.py`(새): `test_vm_provision.py` 처럼 로컬 Temporal + 가짜 작업 — 정상 완료, Control Plane 시간 초과 → 정리 → 실패 사유.
- `init_steps_paas`·`grant_cluster_access` DB 시험.

## 4단계 — MNT-APP

| 파일 | 내용 |
|---|---|
| `lib/types.ts` | `ResourceRequest` 클러스터 필드(`templateId·templateName·kubeVersion·masterCount·masterFlavorId·nodeCount·flavorId·namespace·networkType` 등), 클러스터 `ProvisionSpec` |
| `lib/iaas-api.ts` | `useProjectFlavors(projectId)` — VM·클러스터 공용 |
| `lib/ticket-api.ts` | `useClusterNameCheck`, `useCreateClusterTicket`, `useProvisionClusterTicket` |
| `components/resource-request-wizard.tsx` | `ClusterRequestForm` 목업 → 실연동(프로젝트·이름 확인 0.4초·템플릿 카드·Control Plane/Worker 플레이버·가속기 비활성+사유·희망 네트워크·용도). `VmRequestForm` 플레이버 → `useProjectFlavors(project)` |
| `app/(portal)/ticket/requests/page.tsx` | `cluster` 분기: 목록 스펙, 상세, 프로비저닝 설정(망 → `useNetwork` 의 서브넷, DHCP 꺼짐 비활성+사유, 노드·플레이버·가속기 조정, '노드가 OpenStack API 에 닿는 망이어야 합니다' 안내), `provision()`, 진행 팝업 3방향 분기(제목 `clusterName`, 완료 카드 → `/paas/clusters`, 재시도) |

확인: `npx tsc --noEmit`, 바뀐 파일 `eslint`(프론트 시험 없음, `ignoreBuildErrors: true` 라 빌드로는 타입 오류를 못 잡는다).
작성 전 `node_modules/next/dist/docs/` 에서 쓰는 API 확인(Next.js 16.2.6).

## 5단계 — 배포·실제 시험

1. SQL 적용(사용자) → CORE·IAAS·PAAS·APP 배포.
2. `okc_test_prj` 에서 신청 → 승인 → 프로비저닝(망 `my-network` / `10.0.1.0/24`, 게이트웨이 `compute5` 라우터) → 완료 →
   포털 클러스터 목록에 보이는지(일반 사용자 = 프로젝트 멤버), 네임스페이스·접근 권한 행 확인.
3. 실패 경로: DHCP 꺼진 서브넷 거부, (가능하면) 생성 중 실패 → 정리 → 재시도.
4. 시간 값 조정(설계 3절 임시 값).
