# AI 노트북(MNT-AINB) 포털 통합 — 1차 통합본

2026-10-12 요청하신 develop 기준 통합에 대한 1차 결과를 정리하였습니다.
브랜치 `feature/ainb-integration`(develop `e86d313`까지 반영) → develop 병합 요청(PR) · 그랜드파워솔루션 · 2026-10-14

## 1. 요청 대비 반영 내용
| 요청 | 반영 내용 |
|---|---|
| develop 기준 | `feature/ainb-integration` 브랜치로 작업하였으며, develop 기존 코드는 연동에 필요한 부분만 수정하였습니다 |
| 화면은 MNT-APP | AI 노트북 메뉴(워크스페이스·개발환경·학습·모델 배포·성능 분석)를 MNT-APP으로 이식하였으며, `/api/ainb` 실데이터로 동작합니다 |
| cmp-backend → MNT-AINB | `MNT-AINB/` 모듈(FastAPI)로 정리하였습니다. 내부 라우트는 유지하고 외부 경로만 `/api/ainb`로 제공합니다 |
| 단일 게이트웨이 | 차트 `backends.ainb`의 VirtualService에 `/api/ainb`와 `/notebook/`(노트북 IDE) 경로를 추가하였습니다. 포털과 같은 주소를 사용하므로 CORS 설정은 필요하지 않습니다 |
| 공통 인증 | MNT-AINB 자체 계정을 제거하였습니다. 포털 로그인 시 MNT-CORE 계정으로 확인한 뒤 IDE 접속용 세션 쿠키만 발급합니다(6장 참고) |
| 신청·승인 상태는 포털 DB | 자원신청 유형 `ai-workspace`(`tkt_request`)와 Temporal 워크플로 `AiWorkspaceProvision`을 사용합니다. 진행 단계는 `tkt_provision_step`·`tkt_provision_substep`에 기록합니다 |

## 2. AI 워크스페이스 신청부터 생성까지 (워크스페이스 = 프로젝트 1:1)
1. **사용자** — AI 노트북 › 워크스페이스 › `워크스페이스 신청`에서 신청합니다(자원 신청 마법사의 `AI 워크스페이스` 탭도 동일합니다). 프로젝트·사용 목적·한도(vCPU·메모리·가속기·저장)를 입력하며, 이미 워크스페이스가 있는 프로젝트는 선택할 수 없습니다.
2. **관리자** — 신청 관리에서 `승인` 후 `생성 시작`을 선택합니다(이때 한도를 조정할 수 있습니다). 진행 상황은 VM과 동일한 진행 창에서 확인합니다.
3. **워크플로** — `AiWorkspaceProvision`(MNT-CORE, 큐 `okc-core`)이 MNT-AINB 활동(큐 `okc-ainb`)을 호출합니다.
   - 신청 확인(이름·한도) → 워크스페이스 생성(Profile·네임스페이스·ResourceQuota) → 사용 준비 확인(네임스페이스 Active·쿼터 적용)
   - 실패 시 해당 회차에서 생성한 Profile만 삭제하고 '실패'로 기록하며, 관리자가 `생성 시작`으로 재시도할 수 있습니다.
4. **연결** — Profile에 라벨 `openkcloud.io/project-id`(프로젝트 ID)와 주석 `openkcloud.io/project-name`을 지정합니다. 화면은 이 라벨로 포털 프로젝트와 워크스페이스를 연결하며, 워크스페이스 이름은 프로젝트 이름(영문 소문자·숫자·하이픈, 63자 이내)을 사용합니다.

## 3. 테스트배드 적용 — `포털_배포_가이드.md`에 추가할 내용
> 2026-10-29 다중 서비스 클러스터 연동 이후 아래 (12)(13)은 차트가 처리하므로 필요하지 않습니다 — `AI노트북_다중서비스클러스터_안내.md`를 참고해 주시기 바랍니다.

### 3-1. 최초 1회 (1장 (10) 다음)
```bash
# (11) 공통코드 — 자원신청 유형 ai-workspace, 진행 단계 코드 (반복 실행해도 결과가 동일합니다)
kubectl -n openkcloud-mnt exec -i openkcloud-db-1 -- \
  env PGPASSWORD="${PGPW}" psql -h openkcloud-db-rw -U portal_user -d openkcloud_portal < MNT-CORE/db/ai_workspace.sql

# (12) 앱 시크릿 — MNT-AINB. 세션 서명 키를 고정해야 재배포 후에도 IDE 로그인이 유지됩니다
kubectl -n openkcloud-mnt create secret generic mnt-ainb-secret \
  --from-literal=CMP_SESSION_SECRET="$(openssl rand -hex 32)" \
  --from-literal=MODEL_STORE_KEY='<MinIO 접근 키>' --from-literal=MODEL_STORE_SECRET='<MinIO 비밀 키>'

# (13) 서비스 클러스터(k8s-okc-nb) kubeconfig — MNT-AINB가 Profile·노트북을 생성하는 대상 클러스터입니다
kubectl -n openkcloud-mnt create secret generic mnt-ainb-kubeconfig --from-file=config=<서비스 클러스터 kubeconfig>
#   관리 클러스터의 파드에서 이 kubeconfig의 API 서버 주소로 접속 가능한지 먼저 확인해 주시기 바랍니다
```
- MinIO 계정은 서비스 클러스터의 Kubeflow Pipelines MinIO(`minio-service.kubeflow`) 계정이며, 학습 결과·모델 저장과 모델 배포(KServe)에 사용합니다.
- 서비스 클러스터에는 Notebook·Profile 컨트롤러만 설치되어 있으므로, 학습·모델 배포 화면을 사용하시려면 `MNT-AINB/deploy/kubeflow/install.sh`로 나머지 구성요소를 설치해야 합니다(`MNT-AINB/README.md` 참고).

### 3-2. values.yaml (`backends.ainb.env`) 확인
| 키 | 기본값 | 의미 |
|---|---|---|
| `PUBLIC_BASE_URL` | `https://portal.kcloud.etri:25443` | 브라우저에서 여는 포털 주소(IDE·모델 엔드포인트 안내용) |
| `DEFAULT_NAMESPACE` | `okc` | 기본 워크스페이스(테스트배드 Profile `okc`) |
| `ACCEL_RESOURCE` | `furiosa.ai/rngd` | 가속기 자원 이름(device plugin 설치 후 노출되는 이름) |
| `KUBECONFIG` | `/etc/mnt-ainb/kubeconfig/config` | (13) 시크릿이 마운트되는 위치 |
- MNT-CORE의 `AINB_API_BASE`는 `http://mnt-ainb.openkcloud-mnt.svc.cluster.local`로 설정하였습니다.
- IDE 연결(port-forward)과 성능 분석 상태를 프로세스가 보관하므로 `replicas: 1`로 고정하였습니다.

### 3-3. 빌드·배포·검증
```bash
./scripts/build-push.sh          # 전체 빌드 기본값에 ainb(MNT-AINB → 이미지 mnt-ainb)를 추가하였습니다
helm upgrade --install openkcloud-portal charts/openkcloud-portal -n openkcloud-mnt --set global.image.tag=$TAG

kubectl -n openkcloud-mnt exec deploy/mnt-ainb -- python -c \
  "import urllib.request as u; print(u.urlopen('http://localhost:8000/healthz').read().decode())"   # {"ok":true}
kubectl -n openkcloud-mnt exec deploy/mnt-ainb -- kubectl get profiles    # 서비스 클러스터의 Profile이 조회되면 kubeconfig 정상
# Temporal: 배포 가이드 3장의 확인 명령에서 --task-queue okc-ainb 조회 시 Pollers에 activity가 보이면 AINB 워커가 연결된 상태입니다
```
브라우저에서 포털 › AI 노트북 › 워크스페이스를 열면, 관리자는 전체 워크스페이스를, 사용자는 본인 프로젝트의 워크스페이스만 확인할 수 있습니다.

## 4. API (추가·변경분)
| API | 내용 |
|---|---|
| `POST /api/core/tickets/ai-workspace` | 신청. `{requestedBy, projectId, purpose, cpu, memoryGi, acceleratorCount, storageGi}` → 201. 프로젝트 구성원이 아니면 403, 처리 중인 신청(대기·승인·생성 중·실패)이 있거나 해당 프로젝트의 워크스페이스가 이미 있으면 409를 반환합니다. 워크스페이스를 삭제한 프로젝트는 다시 신청할 수 있습니다 |
| `PATCH /api/core/tickets/{id}` | 승인·반려(기존과 동일) |
| `POST /api/core/tickets/{id}/provision-ai-workspace` | 생성 시작·재시도(관리자). `{processedBy, expectedStatus, cpu?, memoryGi?, acceleratorCount?, storageGi?}` — 값을 비우면 신청 값을 사용합니다 |
| `GET /api/core/tickets/{id}/steps` | 진행 단계(기존과 동일, 코드 그룹 `TKT_PROVISION_STEP_AINB`·`TKT_PROVISION_SUBSTEP_AINB`) |
| `GET /api/ainb/workspace-name-check?name=&projectId=` | 이름 사용 가능 여부와 해당 프로젝트의 워크스페이스(라벨) 존재 여부 `{available, reason}` |
| `GET /api/ainb/profiles` | 항목에 `projectId`·`projectName` 추가 |
| `POST /api/ainb/auth/login` · `logout`, `GET /api/ainb/auth/me` | IDE 세션 쿠키 발급·해제·확인 |
| 그 밖의 `/api/ainb/*` | 기존 cmp-backend `/api/v1/*`와 동일(접두어만 다름) |
| `/notebook/{ns}/{name}/*` | 노트북 IDE 프록시(HTTP·WebSocket), 세션 쿠키 필요 |

## 5. 시험 결과 (개발 PC)
PostgreSQL 17 시험 DB(운영 DB 기준 `tkt_*` 컬럼을 가정하여 구성), Temporal 개발 서버, MNT-IAAS 대역(프로젝트 2개), Kubeflow(Notebook·Profile 컨트롤러)가 설치된 쿠버네티스, 게이트웨이와 같은 경로 규칙의 시험용 프록시를 구성하여 확인하였습니다.

| 항목 | 결과 |
|---|---|
| 로그인 | 잘못된 비밀번호 401, 올바른 비밀번호 200 및 세션 쿠키 발급 |
| 신청 API | 201, 처리 중인 신청이 있는 프로젝트 409, 워크스페이스가 있는 프로젝트 409, 일반 사용자의 승인 시도 403 |
| 화면 — 사용자 신청 | 워크스페이스가 있거나 신청이 처리 중인 프로젝트는 선택 불가, 신청 후 '승인 대기' 표시 |
| 화면 — 관리자 | 유형 'AI 워크스페이스' 표시, 승인 → 생성 시작(vCPU 1→2 조정) → 3/3 단계 완료(약 4.5초) |
| 반려 | 사유 미입력 시 확정 불가, 사용자 화면에 반려 사유 표시, 반려 후 같은 프로젝트 재신청 가능 |
| 실패 → 재시도 | 같은 이름의 다른 프로젝트 워크스페이스가 있을 때 '실패'와 사유 표시, 정리 단계에서 해당 워크스페이스는 삭제하지 않음, 정리 후 재시도 시 2차 시도 완료 |
| 삭제 후 재신청 | 워크스페이스를 삭제한 프로젝트는 '완료' 신청 기록이 있어도 재신청 가능 |
| 클러스터 | Profile 라벨 `project-id`, ResourceQuota `cpu 2 / memory 1Gi / requests.storage 1Gi`, 가속기 1장 신청 시 `requests.<가속기 자원>: 1` 추가(0장이면 항목 없음) |
| 화면 — 사용자 확인 | 프로젝트 선택 목록에 새 워크스페이스가 표시되고 전환 가능 |
| 노트북 | 생성 → 1분 이내 실행 → IDE(VS Code)가 포털 안에서 `/notebook/{ns}/{name}/` 경로로 열림 → 정지 → 시작 → 삭제(볼륨 포함 정리) |
| IDE 경로 | 쿠키가 없으면 401 및 로그인 안내 화면, 쿠키가 있으면 프록시 통과 |
| 배포 형태 화면 | `build:gateway` 정적 빌드를 nginx와 같은 규칙으로 서빙 — AI 노트북 화면 7개(개요·워크스페이스·개발환경·학습·실험·모델 배포·성능 분석) 오류 없음, 백엔드 호출 모두 200 |
| 차트 | `helm template` — mnt-ainb Deployment·Service, VirtualService 경로 `/api/ainb`·`/notebook/` 생성 |
| 이미지 | `docker build` 후 uid 10001로 기동, `/healthz`·`/api/ainb/profiles` 정상 |
| 프런트 | 정적 빌드 통과, 변경 파일 eslint 신규 경고 없음 |
| develop 병합 | 최신 develop(`e86d313`) 병합 후 정적 빌드·차트 렌더·MNT-CORE/MNT-AINB 모듈 로딩 재확인 |

## 6. Keycloak 적용 전 임시 인증
- AI 노트북 전용 계정은 두지 않습니다. 포털 로그인(MNT-CORE) 직후 화면이 같은 계정으로 `POST /api/ainb/auth/login`을 호출하면, MNT-AINB는 MNT-CORE 로그인 API로 계정을 확인한 뒤 서명된 세션 쿠키(`okc_session`, HttpOnly, 8시간)를 발급합니다. 이 과정이 실패하더라도 포털 로그인은 유지되며 IDE 접속만 제한됩니다.
- 세션 쿠키는 노트북 IDE 프록시(`/notebook/*`)에서만 확인합니다. IDE는 브라우저가 직접 여는 주소이므로 화면의 토큰을 함께 전달할 수 없기 때문입니다.
- `/api/ainb/*`는 현재 요청자를 확인하지 않습니다(포털의 다른 모듈과 같은 단계입니다). '내 프로젝트만 보기'는 화면에서 걸러 표시합니다.
- 10/13 회신 주신 방식(포털 로그인 세션 스토어 `openkcloud-auth` 값 사용)에 따라, AI 노트북 화면은 해당 값(사용자·역할)을 그대로 사용합니다. 위 세션 쿠키는 브라우저 저장소 값을 전달할 수 없는 IDE 경로를 위해 추가한 것입니다.
- Keycloak·oauth2-proxy 연동 후에는 게이트웨이가 전달하는 사용자 정보를 사용하도록 변경하고, 이 쿠키는 제거하겠습니다.

## 7. 확인 요청 및 남은 사항
1. **DB 컬럼** — 저장소의 `ddl.sql`에 `tkt_*` 테이블 정의가 없어, VM 프로비저닝 코드가 사용하는 운영 DB 컬럼(`cmn_code.upper_code`, `tkt_provision_step.step_group_code`, `tkt_provision_substep.substep_group_code`)을 기준으로 `ai_workspace.sql`과 진행 단계 기록을 작성하였습니다. 실제 스키마와 일치하는지 확인 부탁드립니다.
2. **관리자 직접 생성** — 기존 `워크스페이스 생성(관리자)` 버튼은 프로젝트 라벨 없이 생성하므로 포털 프로젝트와 연결되지 않습니다. 신청 경로를 이용해 주시거나, 필요하시면 프로젝트 선택 기능을 추가하겠습니다.
3. **주소 변경** — 정적 내보내기(static export)에서는 동적 경로를 사용할 수 없어 `/ai-development/notebooks/{id}` → `/ai-development/notebooks/detail/?id=`, `/notebooks/{id}/ide` → `/notebooks/ide/?id=`로 변경하였습니다.
4. **시간 표시** — 신청 일시가 DB 시각(UTC) 그대로 표시됩니다. 신청 관리 화면의 기존 표시 방식(`fmtDt`)과 동일하게 맞추었으므로, VM 상세 화면처럼 수정하실 때 함께 반영하시면 됩니다.
5. **가속기** — 서비스 클러스터에 Furiosa device plugin이 설치되어야 `furiosa.ai/rngd` 자원이 노출되고 가속기 노트북을 실행할 수 있습니다.
6. **이미지 수신** — 테스트배드에서 외부 이미지 수신이 불안정한 경우, `deploy/kubeflow/images.txt`의 이미지를 Harbor로 옮긴 뒤 매니페스트의 image 항목을 변경해 주시기 바랍니다.
7. **기존 화면에서 유지한 부분** — 노트북 삭제 확인은 브라우저 기본 확인창(`window.confirm`)을 사용합니다. '마지막 활동'은 백엔드가 날짜만 제공하므로 '몇 시간 전' 표시가 정확하지 않습니다.

## 8. 변경 파일
- `MNT-AINB/` — 신규 모듈(앱·Temporal 활동·Dockerfile·`deploy/`·`README.md`)
- `MNT-CORE/` — `routers/tickets.py`(ai-workspace 신청·생성 시작), `workflows/ai_workspace_provision.py`, `workflows/activities.py`·`runtime.py`, `db/ai_workspace.sql`
- `MNT-APP/` — AI 노트북 화면(`app/(portal)/ai-development/*`, `app/(fullscreen)/notebooks/ide`), 신청 관리(`ticket/requests`), 자원 신청 마법사, `lib/ainb-api.ts`·`project-context.ts`·`auth.ts`·`notebook-api.ts`·`ticket-api.ts`
- `charts/openkcloud-portal/` — `backends.ainb`, kubeconfig 마운트, 게이트웨이 추가 경로
- `scripts/build-push.sh` — 모듈 `ainb` 추가
