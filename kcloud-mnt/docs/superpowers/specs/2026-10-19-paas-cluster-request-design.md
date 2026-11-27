# 서비스 클러스터 신청 (새 클러스터) — 설계 문서

작성일: 2026-10-19 · 갱신: 2026-10-20(6차 시험 성공, 구현 계획 조사 반영) · 상태: **초안 (아래 '정해야 할 것' 확정 후 갱신)**
대상 화면: 자원 신청 모달의 '서비스 클러스터 신청' 탭, `/ticket/requests` (자원 요청·신청 관리)

## 목표

사용자가 프로젝트의 **새 서비스 클러스터**를 신청하고, 관리자가 승인·프로비저닝하면
Magnum 으로 클러스터를 만든 뒤 **프로젝트 네임스페이스**를 만들고 **접근 권한(전용)** 을 등록한다.
흐름은 VM·AI 워크스페이스 신청과 같다(티켓 → 승인 → 프로비저닝 워크플로 → 완료/실패).

## 결정 사항 (2026-10-19 확정)

- 범위: **새 클러스터 신청만**. 기존 클러스터에 네임스페이스 연결 신청은 나중 작업.
- 새 클러스터를 만들 때 **프로젝트 네임스페이스도 함께 만든다.**
- 네임스페이스 이름: AI 워크스페이스와 같은 규칙 — 신청 때 `_workspace_name(프로젝트 이름, 프로젝트 ID)` 로
  한 번 정해 `request_spec.namespace` 에 저장하고 다시 계산하지 않는다(`okc_test_prj` → `okc-test-prj`).
  네임스페이스에 라벨 `openkcloud.io/project-id`, 주석 `openkcloud.io/project-name`.
  결과가 `default` 이거나 `kube-` 로 시작하면 대체 이름(`ws-<ID 앞 8자리>`)을 쓴다(쿠버네티스 예약).
- 접근 기준: 프로젝트 멤버(`iam_project_member`). 완료 시 `paas_project_cluster` 에
  (소유 프로젝트, 클러스터, `namespace` NULL = 전용) 1행.
- 신청 가능: 프로젝트 owner·member (VM 과 같음).
- 나중 연결 작업 메모: 공유 행으로 연결된 사용자에게는 그 네임스페이스만 보여야 한다(이번 범위 아님).
- 클러스터 이름: 영문 소문자로 시작, 소문자·숫자·하이픈, 40자 이내. 모든 클러스터 + 진행 중 신청
  (대기·승인·생성 중·실패)과 중복 불가.
- 시간 값: 3절의 임시 값으로 시작하고 VM 처럼 시험해 본 뒤 조정.
- 워크플로: VM 처럼 Temporal 사용. `vm_provision.py` 를 참고해 `kubernetes_provision.py` 를 만든다.
  재활용할 부분(진행 기록·시간 제한·작업 호출)은 별도 공통 파일로 분리해 `vm_provision.py`·`kubernetes_provision.py` 가
  함께 쓴다. **AI 워크스페이스 프로비저닝·MNT-AINB 는 이번 범위에서 제외**(2026-10-20) — `ai_workspace_provision.py` 는 그대로.
- 플레이버: 사용자는 Control Plane·Worker 플레이버를 각각 **그 프로젝트가 쓸 수 있는 플레이버(public + 프로젝트에
  접근이 허용된 비공개)** 목록에서 고르고, **가속기도 고른다.** 관리자는 프로비저닝 때 신청 값을 보고 플레이버·가속기를 정한다.
- **VM 신청도 같은 플레이버 목록(public + 프로젝트)** 으로 바꾼다 — 지금은 public 만.
- 가속기: **Worker 에만** 붙인다. 사용자가 가속기(없음 또는 디바이스 프로파일)를 고르면 맞지 않는 Worker 플레이버는
  **비활성(흐리게 + 사유)** 처리 — VM 신청 템플릿 단계의 '스펙 디스크보다 커서 선택할 수 없습니다' 와 같은 방식.
  Control Plane 목록에서는 가속기 플레이버를 비활성 처리한다.
- 네트워크: **VM 신청처럼 사용자가 희망 네트워크(내부망/인터넷망)를 고른다.** '공인 IP 희망' 은 두지 않는다(노드 공인 IP 미지원).
  관리자는 프로비저닝 때 희망을 참고해 실제 망·서브넷을 고른다 — 어느 쪽이든 API 공인 IP 가 필요하므로 외부망 라우터에 연결된 망이어야 한다.

## 확인한 사실 (2026-10-19 실제 환경 조회)

| 항목 | 내용 |
|---|---|
| Magnum 드라이버 | Cluster API — `magnum_capi_helm`(magnum-conductor 로그). CAPI 객체는 관리 클러스터의 `magnum-{프로젝트 ID}` 네임스페이스. 기존 클러스터 생성 약 13분(`create_timeout` 60분) |
| 기존 클러스터 | 2개, 모두 `admin` 프로젝트 소유 — 다른 프로젝트 소유 클러스터는 아직 없음 |
| 템플릿 | 5개, 모두 `admin` 프로젝트의 public. 노드 플레이버가 가속기와 묶여 있음(`m1.large.rngd` 등) |
| 플레이버 | VM 과 같은 Nova 플레이버(클러스터 전용 자원 없음). `m1.large.rngd` = `m1.large`(4 vCPU·8GB·80GB) + `accel:device_profile=dp-rngd`(Furiosa RNGD 1장), `m1.large.2rngd` = 2장 |
| 가속기 여유 | Furiosa RNGD 4장 모두 사용 중. 남은 AI 칩은 Rebellions 1장(compute2) — `dp-rngd` 는 Furiosa 만 받으므로 RNGD 플레이버 노드는 지금 배치 불가 |
| 템플릿 네트워크 | `k8s-private-net`(10.0.0.0/16) 고정 — **`admin` 소유, 공유 안 됨, RBAC 없음** → 다른 프로젝트 클러스터는 이 망을 쓸 수 없다 |
| 키페어 | 템플릿의 `k8s-key` = `extadmin` 소유. Nova 키페어는 사용자 단위라 `extadmin` 이 어느 프로젝트에 만들든 쓸 수 있다 |
| K8s 버전 | 템플릿 이미지의 Glance 속성 `kube_version` (값 형식이 섞여 있음: `1.36.2`, `v1.34.8`) |
| API 주소 | Cluster API 가 API LB 에 붙인 공인 IP(외부망 `public`, 예: `https://192.168.90.225:6443`) |
| 가용 영역 | `az1` 하나. 기존 클러스터는 생성 때 라벨 `availability_zone=az1` 지정 |
| PaaS 인증 | `MNT-PAAS` 의 Magnum 클라이언트는 `extadmin` 을 **`admin` 프로젝트로만** 스코프 → 지금 코드로는 신청 프로젝트 소유로 만들 수 없다 |
| 시험 프로젝트 | `okc_test_prj`: 망 `my-network` 서브넷 2개(`10.0.0.0/24` DHCP 꺼짐, `10.0.1.0/24` DHCP 켜짐) 모두 `my-router-internal` 에 연결, 이 라우터에 외부 게이트웨이 추가(2026-10-19 사용자 정리). 6차 전 `10.0.1.0/24` 를 `my-router-external`(게이트웨이 호스트 `compute5`)로 옮김(2026-10-20). 쿼터 인스턴스 10(사용 2)·vCPU 20(사용 3)·RAM 50GB(사용 6GB) |
| 서브넷 DHCP | **켜져 있어야 한다.** `okc-prj-test` 1차(`10.0.0.0/24`, DHCP 꺼짐)는 Control Plane VM 이 IPv4·메타데이터를 받지 못해(콘솔: 'Wait for Network to be Configured' 실패, 'No active metadata service found') 설치가 시작되지 않음 → API LB 대상 `ERROR`, Worker VM 은 만들어지지도 않고 75분간 `CREATE_IN_PROGRESS`. Magnum 은 이 서브넷을 거부하지 않는다 |
| 노드 → OpenStack API | **노드가 Keystone(`https://keystone-api.openstack.internal/v3`, 서브넷 DNS 로 해석)에 닿아야 한다.** 클라우드 컨트롤러(OCCM)가 노드에 IP·providerID 를 넣고, calico 는 노드 InternalIP 로 동작한다(`nodeAddressAutodetectionV4: kubernetes: NodeInternalIP`). `okc-prj-test` 2·3차(`10.0.1.0/24`)는 노드 → DNS `192.168.90.253:53` 질의가 시간 초과 → OCCM 종료 → 노드 주소 없음 → calico 실패 → CAPI 가 'providerID 노드' 를 못 찾아 Control Plane 미완료. 관리자 망(`k8s-router`, 게이트웨이 호스트 `controller`)은 됨. 시험 망 노드(라우터 게이트웨이 호스트 `compute2`)에서 직접 시험: 물리 게이트웨이 `192.168.90.1` 은 ping 됨, **MetalLB VIP `192.168.90.253`(DNS)·`.252`(API 입구 istio)는 ping·UDP 53·TCP 443 모두 응답 없음** → 클러스터 기능이 아니라 테스트베드 망 구성 문제. OVN SB `mac_binding`(ip=`.253`)에 `controller`·`compute5` 게이트웨이는 VIP MAC 이 있고 `compute2` 게이트웨이만 없음(ARP 무응답). DNS 설정·앱 자격증명 역할(5차는 `admin` 포함)과는 무관. **6차는 같은 서브넷을 `compute5` 게이트웨이 라우터로 옮겨 정상 완료** |
| 다른 프로젝트 클러스터 | `admin` 스코프로 목록·상세·노드그룹 조회·**삭제** 가능(1차 삭제 76초, VM·API Floating IP·LB 남지 않음). 인증서 CA 조회·CSR 서명만 프로젝트 스코프 필요(4절 `issue_client_cert`). API Floating IP·LB 는 그 프로젝트 소유로 생긴다 |
| API Floating IP | 클러스터 생성 직후 바로 붙는다 — `floating_ip_enabled=false` 로 요청해도 붙음(시험 클러스터 `okc-1node-test`: `192.168.90.237`). Cluster API 관리 클러스터가 이 주소로 접속하므로 끄면 생성이 실패한다(드라이버 문서) |
| 생성 직후 조회 | 생성 요청 후 몇 초 동안 `GET /clusters/{id}` 가 404 또는 400 — 대기 작업은 처음 2분간 이를 견딘다. `api_address` 는 잠깐 `https://:0` 으로 보인 뒤 실제 주소가 된다(30~60초) — 호스트가 비면 '아직 없음'으로 본다 |
| Worker 0 | **완성되지 않는다.** Magnum 은 요청을 받아들이고 K8s API 도 열리지만, Control Plane 노드(taint `node-role.kubernetes.io/control-plane:NoSchedule`)에 배치되지 못하는 애드온 2개(`kube-system/metrics-server`, `node-feature-discovery/node-feature-discovery-gc`)가 Pending 으로 남아 클러스터가 `CREATE_IN_PROGRESS` 에서 끝나지 않음(`okc-1node-test`, 포털 PaaS API 로 확인). 같은 템플릿 1+1(`okc-fip-test`)은 두 애드온 모두 Worker 에 떠서 정상 완료 → **Worker 최소 1** |
| 노드 Floating IP | **지원 안 됨.** `okc-fip-test`(1+1, `floating_ip_enabled=true`)는 10분 15초에 완료됐지만 Control Plane·Worker VM 모두 Floating IP 없음 — API LB 에만 붙음. 이 드라이버는 `floating_ip_enabled` 를 노드에 적용하지 않는다 → 'Worker 공인 IP' 선택은 두지 않는다 |
| 삭제 시간 | `okc-fip-test`(1+1) 삭제 요청 → 15초 뒤 `DELETE_IN_PROGRESS` → **91초에 사라짐**. `okc-1node-test`(생성 중에 삭제)는 60초에 `DELETE_COMPLETE` 가 잠깐 보인 뒤 76초에 사라짐 → 정리 대기는 404 와 `DELETE_COMPLETE` 를 모두 '삭제됨'으로 본다. 두 경우 모두 VM·API Floating IP·LB 남지 않음 |
| 노드그룹 상태 | Worker 노드그룹은 Worker VM 이 생기기도 전(30초)에 `CREATE_COMPLETE` → **Worker 진행 판정에 쓸 수 없다**. Control Plane 노드그룹 완료(약 6분 30초)는 Control Plane 준비 시점과 맞음 |

## 흐름

```
[사용자] 신청 ──POST /tickets/cluster──> [CORE] 확인·저장(pending)
[관리자] 승인 ──PATCH /tickets/{id}────> approved
[관리자] 프로비저닝 설정(네트워크 등) ──POST /tickets/{id}/provision-cluster──> provisioning
          └─ Temporal 워크플로 KubernetesProvision (CORE 워커, 큐 okc-core)
               ├─ PaaS 작업 (PAAS 워커, 큐 okc-paas): 생성 요청 → 준비 대기 → 네임스페이스 생성 / 실패 시 삭제
               └─ DB 기록 (CORE 작업): 단계 기록 → 접근 권한 등록 → 티켓 완료·실패
```

## 1. 신청 (사용자)

| 항목 | 내용 |
|---|---|
| 프로젝트 | 신청 가능한 프로젝트(`GET /tickets/projects`, VM 과 같음) |
| 클러스터 이름 | 이름 규칙(정해야 할 것 3) + 중복 확인(입력 멈춤 0.4초 뒤, 제출 때 서버 재확인) |
| 클러스터 템플릿 | public·숨김 아닌 템플릿. 카드에 이름·K8s 버전(이미지 `kube_version`)·기본 플레이버 |
| Control Plane | 1 또는 3 · 플레이버(프로젝트가 쓸 수 있는 플레이버, 기본값 = 템플릿). 가속기 플레이버는 비활성 — 사유 'Control Plane 에는 가속기 스펙을 쓸 수 없습니다' |
| 가속기 (Worker) | 없음 / 프로젝트 플레이버들에 달린 디바이스 프로파일(표시: 종류·개수, 예 'NPU · Furiosa × 1'). 기본값 = 템플릿 Worker 플레이버의 가속기 |
| Worker | 1~20(0 은 애드온이 배치되지 않아 완성 안 됨 — 시험 확인) · 플레이버(같은 목록, 기본값 = 템플릿). 고른 가속기와 다른 플레이버는 비활성 — 사유 '가속기가 없는 스펙입니다' / '선택한 가속기(…)와 다른 스펙입니다'. 가속기를 바꿔 고른 플레이버가 비활성이 되면 선택을 비운다 |
| 희망 네트워크 | 내부망 / 인터넷망 — VM 신청과 같은 선택지(`USER_NETWORKS`). 공인 IP 희망 토글은 없음 |
| 용도 | 자유 입력(300자, AI 워크스페이스와 같음) |

목업의 'Kubernetes 버전' 선택은 템플릿 선택으로 바뀐다(Magnum 에서 버전은 템플릿 이미지가 정한다).

**서버 확인 (`POST /tickets/cluster`)**: 멤버·역할(`_check_requestable`), 프로젝트(라이브), 이름 규칙·중복,
템플릿 존재·공개, 플레이버 존재, 노드 수 범위. `request_spec` 에 저장:
`projectName, clusterName, templateId, templateName, kubeVersion, masterCount, masterFlavorId, nodeCount,
flavorId, namespace, purpose` (+ 표시용 플레이버 이름·vCPU·RAM).

## 2. 승인·프로비저닝 설정 (관리자)

- 승인·반려: 기존 `PATCH /tickets/{id}` 그대로.
- 프로비저닝 설정 (`POST /tickets/{id}/provision-cluster`, 승인·실패 상태, 토큰의 관리자):
  - **네트워크·서브넷 (필수)**: 프로젝트가 쓸 수 있는 망(소유 + 공유, 외부망 제외). 템플릿의 외부망으로
    공인 IP 를 붙일 수 있는지 IAAS `GET /networks/{id}/fip-reachable` 로 확인(VM 과 같은 확인 재사용).
    서브넷은 **DHCP 켜진 것만** 고를 수 있다(꺼진 서브넷은 비활성 — 사유 'DHCP 가 꺼진 서브넷에는 노드가 주소를 받지 못합니다').
    서버도 같은 조건을 확인한다(시험 확인 — 확인한 사실 '서브넷 DHCP').
  - 플레이버·가속기: 신청 화면과 같은 선택 UI(가속기 → 맞지 않는 Worker 플레이버 비활성)에 신청 값을 채워 보여 주고
    관리자가 정한다. 노드 수도 조정 가능. 서버도 같은 규칙(Control Plane 은 가속기 없음, Worker 는 고른 가속기와 일치)을 확인.
  - 이름 중복 재확인(이전 회차 클러스터가 아직 삭제 중이면 409).
  - 쿼터 사전 확인: 필요한 vCPU·RAM·인스턴스 수가 남은 쿼터 안인지 — VM 처럼 IAAS `/placement-preview` 의 `quota`
    (CORE 에는 쿼터 조회가 없고, 인스턴스 수까지 주는 IAAS API 는 이것뿐). 실행 때 워크플로가 `iaas.check_quota` 로 다시 본다.
- 워크플로 ID: `cluster-provision-{신청ID}-{회차}`. 시작 실패 시 이전 상태·값으로 되돌림(AI 워크스페이스와 같음).

## 3. 워크플로·진행 단계

공통코드 그룹 `TKT_PROVISION_STEP_PAAS` / `TKT_PROVISION_SUBSTEP_PAAS` (SQL 은 사용자가 수동 적용).

| 단계 | 상세 단계 | 표시 값 |
|---|---|---|
| 클러스터 생성 가능 여부 확인중 `check_creatable` | 클러스터 이름 `check_name` | 이름 |
| | 자원 쿼터 `check_quota` | VM 4대 · vCPU 16 · RAM 32GB |
| | 네트워크 `check_network` | 망 이름 / 서브넷 CIDR |
| 클러스터 생성 `create_cluster` | 생성 요청 전송 `send_request` | 클러스터 ID |
| | Control Plane 준비 `create_control_plane` | 1/1 |
| | Worker 준비 `create_workers` | 3/3 |
| | K8s API 연결 `check_api` | API 주소 |
| 네임스페이스 및 접근 권한 `setup_cluster` | 프로젝트 네임스페이스 `create_namespace` | 네임스페이스 이름 |
| | 접근 권한 등록 `grant_access` | 프로젝트 이름 |

- 네트워크 확인 = 서브넷 DHCP 켜짐 + `fip-reachable` 가능(프로비저닝 설정 뒤 망이 바뀌었을 수 있어 실행 때 다시 본다).
- Control Plane 준비 = Control Plane 노드그룹 `CREATE_COMPLETE`. Worker 준비 = Nova 의 Worker VM(이름 `{stack_id}-default-worker-*`)
  `ACTIVE` 수(표시 'n/전체') + 클러스터 `CREATE_COMPLETE` — Worker 노드그룹 상태는 실제보다 먼저 완료로 바뀌어 쓰지 않는다(시험 확인).
- 생성 요청은 재시도하지 않는다(응답 유실 시 중복 생성 위험 — VM 과 같음). 작업은 먼저 같은 이름의 클러스터가
  그 프로젝트에 있는지 보고, 있으면 그것을 이어받는다.
- 접근 권한 등록은 멱등: `UNIQUE (project_id, cluster_id, namespace)` 는 NULL 중복을 못 막으므로
  `WHERE NOT EXISTS (... namespace IS NULL)` 로 넣는다.
- 결과(`result`): `clusterId, clusterName, namespace, apiAddress`.

**시간 (임시 값, VM 처럼 테스트 후 조정)**: 확인 간격 10초 · 생성 대기 최대 45분 · K8s API 연결·네임스페이스 5분 ·
전체 60분 · 실패 정리 대기 15분 · PaaS 작업 큐 대기 2분(넘으면 'PAAS 워커 없음').

**실패 시 정리**: 이번 회차가 만든 클러스터를 Magnum DELETE 하고 사라질 때까지 기다린 뒤 티켓 '실패'.
정리 결과는 실패 사유 끝에 붙이고, 정리 대기를 넘기면 '삭제 진행 중 — 확인 필요' + `result.clusterId` 보존.
재시도 = 새 회차, 처음부터(설정은 이전 값으로 채움).

## 4. 모듈별 변경

**MNT-IAAS**
- `routers/flavors.py`: `GET /projects/{project_id}/flavors`(새) — public + 그 프로젝트에 접근이 허용된 비공개 플레이버.
  응답 형식은 `/flavors` 와 같음(가속기·`deviceProfileMissing` 포함). 지금 `/flavors` 는 Nova 기본 조회라 비공개를
  아예 돌려주지 않는다 — 비공개는 `is_public=None` 으로 받아 플레이버 접근 목록(`os-flavor-access`)으로 거른다.
  VM·클러스터 신청이 함께 쓴다.
- `workflows/activities.py`: `iaas.check_quota`(`get_project_quota` 로 인스턴스·vCPU·RAM 여유), `iaas.check_network`
  (서브넷이 그 망 소속·DHCP 켜짐 + `fip_reachable`). 클러스터 워크플로의 `check_quota`·`check_network` 단계가 쓴다.

**MNT-PAAS**
- `openstack_client.py`: 프로젝트 ID 로 스코프한 Magnum 클라이언트(캐시) — **완료(`e27fd5b`)**. 같은 토큰으로 Nova(Worker VM 수)·
  Glance(이미지 `kube_version`)를 읽는 조회 도우미 추가.
- `routers/clusters.py`: `GET /cluster-name-check?name=`(로그인) — 모든 클러스터 이름과 비교해 사용 가능 여부만 돌려준다
  (`GET /clusters` 는 일반 사용자에게 접근 가능한 클러스터만 주므로 이름 중복 확인에 못 쓴다).
- `workflows/activities.py`(새): `paas.check_name`, `paas.create_cluster`, `paas.watch_cluster`(바뀌면 즉시·아니면 최대 60초 뒤 반환,
  하트비트), `paas.create_namespace`(있으면 라벨만 확인 — 멱등), `paas.delete_cluster`(ID 또는 이름으로 찾아 삭제 + 사라질 때까지 대기).
- `workflows/runtime.py`(새): 워커(큐 `okc-paas`) — 재기동 때 실행 중 작업을 **25초** 기다린다(파드 기본 종료 유예 30초 안).
  생성 요청은 몇 초면 끝나고, 대기 작업은 끊겨도 하트비트로 새 파드에서 다시 실행된다.
- `main.py` 수명주기에서 워커 기동, `config.py` 에 `temporal_api_base`, `requirements.txt` 에 `temporalio==1.34.0`,
  `config.env.example`·`deploy/all.yaml` 에 `TEMPORAL_API_BASE`.
- `routers/templates.py`: 템플릿 요약에 `kubeVersion`(Glance 이미지 속성 `kube_version`, 이미지 ID 별 캐시) 추가.
- `k8s_client.issue_client_cert`: **클러스터 소유 프로젝트 스코프로 인증서 발급(필수)** — 2026-10-19 시험에서
  다른 프로젝트 클러스터의 CA 조회(404)·CSR 서명(400)이 `admin` 스코프로는 'Cluster could not be found' 로 실패하고,
  프로젝트 스코프로는 둘 다 성공. 목록·상세·노드그룹 조회는 `admin` 스코프로 된다. 소유 프로젝트는 `admin` 스코프 상세의 `project_id`.

**MNT-CORE**
- `routers/tickets.py`: `_paas(path, token)` 도우미(`PAAS_API_BASE` — 설정은 이미 있음). **`_iaas()` 처럼 사용자 토큰을 넘긴다**
  (PAAS 는 `465b4f4` 부터 모든 API 에 토큰 필요 — `_ainb()` 는 토큰을 안 보내므로 따르지 않는다). 프로비저닝 API 는 VM 처럼
  토큰 사용자로 관리자를 확인한다,
  `GET /tickets/cluster-name-check`, `POST /tickets/cluster`, `POST /tickets/{id}/provision-cluster`.
  VM 신청(`create_ticket`)의 플레이버 확인도 `/flavors` + public 조건 → 프로젝트 플레이버 API 로 바꾼다.
- `workflows/kubernetes_provision.py`(새): `KubernetesProvision` — `VmProvision` 구조(생성 요청 재시도 없음·상태 변화 감시·
  삭제 후 사라질 때까지 정리)를 따른다. 시작 대기열·가속기/FIP 건너뛰기·부팅 볼륨 재대기는 쓰지 않는다.
- 워크플로 공통 파일(새): `VmProvision` 의 진행 기록(`_idx`·`_go`·`_set_values`·`_fail_current`)·시간 제한(`_within`)·
  작업 호출(`_core`)·실패 사유(`_reason`)를 옮기고 `vm_provision.py` 가 이를 쓰도록 바꾼다
  (동작·명령 순서 그대로 — 실행 중인 워크플로의 재실행 판정이 깨지지 않게). 실패 사유의 워커 이름(IAAS·PAAS)만 인자로.
- `workflows/activities.py`: `core.init_steps_paas`(그룹 `TKT_PROVISION_STEP_PAAS`·`TKT_PROVISION_SUBSTEP_PAAS` 명시,
  `init_steps_ainb` 는 그대로), `core.grant_cluster_access` 추가.
- `workflows/runtime.py`: `start_cluster_provision`, 워커에 워크플로 등록.
- `db/paas_cluster_request.sql`(새): 단계 공통코드. `TKT_TYPE` 의 `cluster` 는 이미 있다. 시험 DB(`tests/run_tests.sh`)에도 적용.

**MNT-APP**
- `resource-request-wizard.tsx` `ClusterRequestForm`: 목업 → 실연동(프로젝트·템플릿·플레이버·이름 확인·제출).
  `VmRequestForm` 스펙 단계: `useFlavors()` + `isPublic` 필터 → 선택한 프로젝트의 플레이버 목록.
- `iaas-api.ts`: 프로젝트 플레이버 훅(`useProjectFlavors`) — VM·클러스터 신청 공용.
- `ticket-api.ts`: 신청·이름 확인·프로비저닝 훅.
- `ticket/requests/page.tsx`: `cluster` 분기 — 목록 스펙 표시, 상세 모달 프로비저닝 설정(네트워크·서브넷, 노드 조정),
  진행 팝업(공통), 완료 카드 → `/paas/clusters`.

## 5. 먼저 확인할 것 (구현 1단계)

2026-10-19 `admin` 프로젝트 시험 클러스터 2개로 확인한 것: Worker 0 불가, 노드 Floating IP 미지원,
노드그룹 상태로 Worker 진행 판정 불가, 생성·삭제 시간(확인한 사실 표 참고). 가용 영역 라벨은 기존 클러스터처럼
`availability_zone=az1` 을 넣는다(PaaS 설정값, 기본 `az1`).

2026-10-19~20 `okc_test_prj` 소유 시험 클러스터 6회(`okc-prj-test`, 프로젝트 스코프 생성)로 확인한 것:
1. **`admin` 스코프로 목록·상세·노드그룹 조회·삭제는 된다. 인증서(CA 조회·CSR 서명)는 안 된다** → `issue_client_cert`
   프로젝트 스코프 필수(4절). 지금 배포된 포털 PaaS 화면도 이 클러스터에서 404 'Cluster could not be found'.
2. 프로젝트 스코프 생성 시 API LB·Floating IP 는 그 프로젝트 소유로 생긴다.
3. 서브넷 DHCP 가 꺼져 있으면 노드가 주소를 못 받아 설치가 시작되지 않는다(1차).
4. 2·3차는 노드가 OpenStack API(MetalLB VIP `192.168.90.252`·DNS `.253`)에 닿지 못해 완료되지 않았다 — 라우터
   외부 게이트웨이가 `compute2` 에 놓인 망. 5차(DNS `.253`, 역할 `admin` 포함)도 같은 곳에서 멈춤. 테스트베드 망 구성 문제라
   인프라 쪽에서 해결한다(4차는 서브넷 DNS 가 잠시 `.212` 로 바뀌어 무효).
5. **6차(게이트웨이 호스트 `compute5`)는 정상 완료** — Control Plane VM ACTIVE 3분 37초, Control Plane 노드그룹 완료
   8분 41초, Worker VM ACTIVE 9분 11초, 클러스터 `CREATE_COMPLETE` **12분 14초**(`HEALTHY`). 다른 프로젝트 소유 클러스터도
   망만 맞으면 끝까지 만들어진다. 바뀐 `issue_client_cert`(소유 프로젝트 스코프)로 CA 조회·인증서 발급 성공.

## 6. 정해야 할 것

1. 테스트베드 망: tenant 라우터의 외부 게이트웨이가 어느 호스트에 놓여도 MetalLB VIP(OpenStack API·DNS)에 닿도록
   인프라 구성 정리(지금 확인된 것은 `controller`·`compute5` 됨, `compute2` 안 됨, `compute1`·`compute6` 미확인). 정리 전까지 관리자 프로비저닝 화면에
   '노드가 OpenStack API 에 닿는 망이어야 합니다' 안내 문구를 둔다(API 로 미리 확인할 방법 없음).

## 범위 밖

- 기존 클러스터에 네임스페이스 연결 신청, 공유 격리(`docs/테넌시_권한경계_정리.md` 6-2).
- 동시 프로비저닝 제한(VM 의 시작 대기열) — 클러스터 신청은 드물어 지금은 두지 않는다.
- 클러스터 삭제·확장 신청(삭제는 관리자 즉시 삭제가 이미 있음).
