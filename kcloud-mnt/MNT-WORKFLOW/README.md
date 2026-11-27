# MNT-WORKFLOW — Temporal 서버

프로비저닝 워크플로(예: VM 자원신청 승인 → 생성)의 **작업 장부** 역할을 하는 Temporal 서버 모듈입니다.
이 모듈에는 우리 코드가 없습니다. 공식 이미지를 설정해서 띄우기만 하고, 워크플로 코드는 CORE·IAAS 에 있습니다.

## Temporal 구성 요소

| 구성 | 하는 일 | 위치 |
|---|---|---|
| Temporal 서버 | 워크플로 진행 기록 저장, 작업 큐 관리 | **MNT-WORKFLOW** (테스트베드: `mnt-workflow` 파드, 로컬: 개발 서버) |
| 클라이언트 | 워크플로 시작 요청 | MNT-CORE (프로비저닝 API) |
| 워커 | 서버에서 할 일을 가져가 우리 코드 실행 | MNT-CORE 워커(워크플로 코드 + DB 기록), MNT-IAAS 워커(OpenStack 작업) |

CORE 와 IAAS 는 이 흐름에서 서로 직접 호출하지 않고, 모든 연락이 Temporal 서버를 거칩니다.

```
관리자 '프로비저닝' → CORE API(클라이언트) → Temporal 서버에 워크플로 시작
  → CORE 워커: 워크플로 코드 실행 → OpenStack 작업은 IAAS 큐에 넣음
  → IAAS 워커: OpenStack 호출 → 결과 보고
  → CORE 워커: 결과 받아 DB 기록(단계·상세 단계·티켓 상태) → 다음 단계 …
```

서버 안의 `worker` 서비스는 Temporal **내부 관리용**이며, 우리가 만드는 CORE·IAAS 워커와 다릅니다.

## 로컬 개발

```bash
cd MNT-WORKFLOW
brew install temporal      # 최초 1회
./run-local.sh             # gRPC localhost:7233, 웹 화면 http://localhost:8233
```

- 기록은 `.temporal/dev.db` 에 남아 재시작해도 유지됩니다(지우면 초기화, git 제외).
- `default` 네임스페이스는 자동으로 만들어집니다.
- CORE·IAAS 는 지금처럼 `uvicorn` 으로 띄우고, `config.env` 에 `TEMPORAL_API_BASE=localhost:7233` 을 둡니다.

## 테스트베드 배포 구성

```
Pod mnt-workflow (replicas 1, 배포 전략 Recreate)
 ├─ initContainer schema  admin-tools — Temporal 스키마 설치·업그레이드(멱등)
 ├─ temporal              server — frontend·history·matching·worker 한 프로세스, :7233
 └─ ui                    웹 화면 :8080 → 127.0.0.1:7233
Service mnt-workflow      7233(gRPC, CORE·IAAS 의 TEMPORAL_API_BASE) / 8080(웹 화면)
Job mnt-workflow-namespace-<릴리스 번호>   'default' 네임스페이스가 없을 때만 생성(보관 30일)
VirtualService openkcloud-portal-temporal-ui   https://temporal.kcloud.etri → 웹 화면 (portal-gateway)
```

- 저장소: 포털 DB `openkcloud_portal` 안의 스키마 `temporal`(실행 기록)·`temporal_visibility`(검색·목록).
  계정별 `search_path` 로 고정해서 포털 테이블(`public`)과 섞이지 않습니다.
- Recreate: 새 파드를 먼저 띄우면 잠깐 서버 2개가 같은 DB 를 쓰게 되므로, 옛 파드를 내린 뒤 띄웁니다.
  몇 초 끊기지만 CORE·IAAS 워커는 자동으로 다시 연결하고, 진행 중인 워크플로는 기록된 지점부터 이어집니다.

## 테스트베드 첫 배포 순서

1. **DB 준비(슈퍼유저, 1회)** — `db/temporal_schema.sql` 의 비밀번호를 채워 적용
   (계정 `temporal`·`temporal_view`, 스키마, `btree_gin` 확장)
2. **배포(시크릿 함께 생성)** — 1번 비밀번호를 `charts/openkcloud-portal/values.yaml` 의 `workflow.dbPassword` 에 적어 커밋하면
   포털 배포 때 차트가 `mnt-workflow-secret` 을 만든다(`docs/포털_배포_가이드.md` 1장 (6))
   ```bash
   helm upgrade --install openkcloud-portal charts/openkcloud-portal -n openkcloud-mnt --set global.image.tag=<태그>
   ```
   - 비워 두면 클러스터에 있는 기존 시크릿 값을 그대로 쓴다. 값을 바꾸면 mnt-workflow 파드가 다시 시작된다.
3. **확인**
   ```bash
   kubectl -n openkcloud-mnt get pod -l app=mnt-workflow          # 2/2 Running
   kubectl -n openkcloud-mnt logs -l app=mnt-workflow-namespace    # "successfully registered" 또는 "이미 있음"
   ```
   웹 화면: https://temporal.kcloud.etri (DNS·hosts 에서 ingress 로 연결 필요)

단독 배포(차트 없이)는 `deploy/all.yaml` 을 쓰고, 내용은 차트와 같게 유지합니다.

## 버전 올리기

- 이미지는 공식 이미지를 Harbor(`harbor.io:7443/mnt/mnt-workflow/temporalio/*`)로 옮겨 씁니다. 새 버전도 먼저 옮깁니다.
  노드가 amd64 이므로 `--platform linux/amd64` 로 받습니다(Mac(arm64)에서 그냥 받으면 arm64 이미지가 올라가 파드가 `exec format error`).
  ```bash
  for i in server:1.31.3 admin-tools:1.31.3 ui:2.55.0; do
    docker pull --platform linux/amd64 temporalio/$i
    docker tag temporalio/$i harbor.io:7443/mnt/mnt-workflow/temporalio/$i
    docker push harbor.io:7443/mnt/mnt-workflow/temporalio/$i
  done
  ```
- `values.yaml` 의 `workflow.images` 에서 `server`·`adminTools` 를 **같은 버전**으로 바꿉니다(`deploy/all.yaml` 도 함께).
  파드가 다시 뜰 때 initContainer 가 스키마를 자동으로 업그레이드합니다.
- 마이너 버전은 **한 단계씩** 올립니다(1.31 → 1.32 → 1.33). 건너뛰면 스키마·데이터 호환이 보장되지 않습니다.
- 현재: server·admin-tools `1.31.3`, ui `2.55.0`.

## 주의

- `NUM_HISTORY_SHARDS`(4)는 처음 만든 뒤에는 바꿀 수 없습니다. 바꾸려면 Temporal 데이터를 새로 만들어야 합니다.
- 웹 화면에는 로그인이 없고 워크플로 강제 종료 같은 조작도 가능합니다. 사내망에서만 열리게 하세요(Keycloak 연동 전까지).
