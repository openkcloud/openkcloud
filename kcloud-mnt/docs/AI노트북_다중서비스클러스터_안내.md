# AI 노트북(MNT-AINB) 다중 서비스 클러스터 연동

2026-10-28 제안해 주신 방식(클러스터 ID로 MNT-PAAS에서 kubeconfig를 받아오는 방식)을 반영하였습니다.
브랜치 `feature/ainb-multicluster`(develop `7d34e3e` 기준) · 그랜드파워솔루션 · 2026-10-29

## 1. 반영 내용
| 항목 | 내용 |
|---|---|
| 접속 정보 | 고정 kubeconfig(Secret `mnt-ainb-kubeconfig`) 대신, MNT-PAAS가 클러스터마다 발급하는 kubeconfig(`GET /api/paas/clusters/{id}/kubeconfig`)를 사용합니다 |
| 여러 클러스터 | 워크스페이스마다 지정된 서비스 클러스터에서 동작합니다. 목록 화면은 클러스터별 결과를 합쳐 보여 줍니다 |
| 신청 화면 | AI 워크스페이스 신청 때 서비스 클러스터를 선택합니다. 신청 상세에 선택한 클러스터가 표시됩니다 |
| 인증 | MNT-PAAS는 사용자 토큰으로만 kubeconfig를 발급하므로, 사용자가 AI 노트북에 로그인하거나 워크스페이스를 신청할 때 그 사용자의 토큰으로 접속 정보를 받아 보관합니다. 승인 뒤 생성, IDE 연결 유지처럼 사용자 없이 동작하는 작업은 보관된 접속 정보를 사용합니다 |
| 수동 설정 제거 | 차트가 `mnt-ainb-secret`과 접속 정보 보관용 권한(ServiceAccount·Role)을 만들므로, 1차 안내 문서 3-1항 (12)(13)의 수동 단계가 필요하지 않습니다 |
| 기존 방식 유지 | `CLUSTER_SOURCE`를 비우면 예전과 같이 클러스터 하나(`KUBECONFIG` 또는 ServiceAccount)로 동작합니다 |

## 2. 동작 방식
1. **신청** — 사용자가 프로젝트와 서비스 클러스터를 선택하여 신청합니다. 화면은 `GET /api/ainb/clusters`의 `mode`가 `paas`일 때만 클러스터 선택 칸을 표시하며, 선택한 프로젝트 소유의 클러스터를 앞에 보여 줍니다.
2. **신청 확인(MNT-CORE)** — 일반 사용자는 해당 프로젝트에 연결된 클러스터(`paas_project_cluster`)만 선택할 수 있습니다(관리자는 제한 없음). 연결에 네임스페이스가 지정된 공유 클러스터는 그 네임스페이스를 워크스페이스 이름으로 사용합니다.
3. **접속 정보 받기(MNT-AINB)** — MNT-CORE가 신청자 토큰을 `workspace-name-check`에 함께 넘기면, MNT-AINB가 그 토큰으로 해당 클러스터의 kubeconfig를 받아 보관합니다. 권한이 없는 클러스터는 MNT-PAAS 판정(403)에 따라 신청이 거부됩니다.
4. **생성(워크플로)** — 승인 후 `생성 시작`을 누르면 `AiWorkspaceProvision`이 선택한 클러스터 ID를 AINB 작업(`ainb.create_workspace`·`ainb.wait_ready`·`ainb.delete_workspace`)에 넘깁니다. 클러스터 ID가 없는 이전 신청은 기본 클러스터에서 동작합니다.
5. **사용** — 노트북·IDE·학습·모델 배포 요청은 워크스페이스(네임스페이스)가 있는 클러스터로 자동으로 연결됩니다. `?clusterId=`로 직접 지정할 수도 있습니다.

**기존 프로젝트 네임스페이스 사용** — 서비스 클러스터 신청으로 미리 만들어진 프로젝트 네임스페이스(같은 `openkcloud.io/project-id` 라벨)가 있으면, 네임스페이스에 `owner` 주석을 지정하여 Kubeflow Profile이 해당 네임스페이스를 넘겨받도록 하였습니다. 이 경우 워크스페이스를 삭제하여도 네임스페이스는 유지됩니다.

**접속 정보 보관과 갱신** — 관리 클러스터의 Secret `mnt-ainb-clusters`에 보관합니다. 로그인할 때 7일(`CLUSTER_REFRESH_DAYS`)이 지난 접속 정보는 새로 받으며, 관리자가 로그인하면 MNT-PAAS에 더 이상 없는 클러스터의 보관분을 정리합니다. 생성 중·삭제 중이거나 생성에 실패한 클러스터는 건너뜁니다.

**다른 클러스터 접근** — 관리 클러스터에서 서비스 클러스터의 ClusterIP·파드 IP에는 직접 접근할 수 없으므로, KFP·MinIO·모델 서버 호출은 port-forward를 거치도록 변경하였습니다.

## 3. 테스트배드 적용
### 3-1. 차트 변경 사항(`charts/openkcloud-portal`)
| 항목 | 내용 |
|---|---|
| `backends.ainb.clusterStoreSecret` | `mnt-ainb-clusters` — 지정하면 ServiceAccount `mnt-ainb`, Role `mnt-ainb-cluster-store`(Secret 생성, 해당 Secret 조회·수정), RoleBinding을 함께 만듭니다 |
| `backends.ainb.env` | `CLUSTER_SOURCE: paas`, `PAAS_API_BASE`, `CLUSTER_STORE_SECRET` 추가, `KUBECONFIG` 삭제 |
| `secrets.ainb` | `mnt-ainb-secret`을 차트가 만듭니다. 세션 키는 처음 설치할 때 무작위로 생성하고 이후에는 기존 값을 유지합니다. MinIO 계정은 비우면 모듈 기본값을 사용합니다 |
| kubeconfig 볼륨 | `kubeconfigSecret`을 쓰는 경우에도 Secret이 없으면 비운 채 기동하도록 `optional: true`를 지정하였습니다 |

### 3-2. 배포·확인
```bash
./scripts/build-push.sh core app ainb
helm upgrade --install openkcloud-portal charts/openkcloud-portal -n openkcloud-mnt --set global.image.tag=$TAG

kubectl -n openkcloud-mnt get pod -l app=mnt-ainb                     # Running 확인
kubectl -n openkcloud-mnt exec deploy/mnt-ainb -- python -c \
  "import urllib.request as u; print(u.urlopen('http://localhost:8000/api/ainb/clusters').read().decode())"
# {"mode":"paas","items":[...]} — 포털에 로그인한 뒤 다시 조회하면 그 사용자가 쓸 수 있는 클러스터가 표시됩니다
```
- 공통코드 `MNT-CORE/db/ai_workspace.sql`은 1차 안내 문서 (11)과 같이 1회 적용합니다(이미 적용하셨으면 생략합니다).
- 관리 클러스터의 `mnt-ainb` 파드에서 서비스 클러스터 API 서버 주소(kubeconfig의 `server`)로 접속할 수 있어야 합니다. KFP·MinIO·모델 서버 호출도 API 서버를 거치므로 그 밖의 주소는 필요하지 않습니다.
- AI 노트북을 만들 서비스 클러스터에는 Kubeflow Notebook·Profile 컨트롤러가 있어야 합니다. 없는 클러스터에는 `MNT-AINB/deploy/kubeflow/install.sh`로 설치하겠습니다.

## 4. API (추가·변경분)
| API | 내용 |
|---|---|
| `POST /api/core/tickets/ai-workspace` | `clusterId`(선택) 추가. 프로젝트에 연결되지 않은 클러스터는 400(일반 사용자), 신청 항목에 `clusterId`·`clusterName` 저장 |
| `GET /api/ainb/clusters` | `{mode: "single" \| "paas", items: [{id, name, fetchedAt}]}` — 보관된 클러스터 접속 정보 |
| `POST /api/ainb/clusters/sync` | 요청자 토큰으로 쓸 수 있는 클러스터의 접속 정보를 받습니다(`force=true`면 모두 새로) |
| `GET /api/ainb/workspace-name-check` | `clusterId` 추가, 요청의 `Authorization`(신청자 토큰)으로 접속 정보를 받습니다 |
| 목록(`/notebooks`·`/profiles`·`/experiments`·`/serving` 등) | PaaS 모드에서 항목마다 `clusterId`·`clusterName` 추가, `?clusterId=`로 한 클러스터만 조회 |
| `POST /api/ainb/profiles` | `clusterId`(선택) 추가 |

## 5. 시험 결과 (개발 PC)
MNT-PAAS·MNT-CORE 인증을 흉내 낸 시험용 대역(클러스터 3개: 사용 가능 2·생성 중 1, 사용자·관리자 토큰)과, Kubeflow(Notebook·Profile 컨트롤러)가 설치된 쿠버네티스를 서비스 클러스터로 사용하여 확인하였습니다.

| 항목 | 결과 |
|---|---|
| 접속 정보 받기 | 클러스터 미지정·토큰 없음·권한 없음 각각 사유 안내, 사용자 토큰으로 받기 성공, 보관(파일 권한 0600)·재기동 후 재사용 |
| 로그인 뒤 동기화 | 관리자 로그인 시 나머지 클러스터 접속 정보를 백그라운드로 받음, 생성 중 클러스터는 건너뜀, 이미 받은 클러스터는 다시 받지 않음 |
| 목록 | 클러스터별 결과를 합치고 `clusterId`·`clusterName` 표시, `?clusterId=`로 한 클러스터만 조회 |
| 기존 네임스페이스 넘겨받기 | 같은 프로젝트 라벨이면 이름 확인 통과·다른 프로젝트면 불가, 별도 프로세스에서 워크스페이스 생성 작업 실행(보관된 접속 정보만 사용), Profile 반영 후 프로젝트 라벨 유지, 워크스페이스 삭제 후 네임스페이스 유지 |
| 노트북 | 넘겨받은 워크스페이스에 생성 → 실행 → IDE 프록시 연결 → 정지 → 삭제 |
| 기존 방식(클러스터 하나) | 목록·이름 확인·워크스페이스 생성·삭제·GPU 지표 이상 없음 |
| MNT-CORE 자동 시험 | 104개 통과(신규 7개: 클러스터 미지정 신청, 클러스터 지정 신청, 공유 클러스터 네임스페이스, 연결되지 않은 클러스터 거부·관리자 허용, AINB 거부 사유 전달, 생성 시작 시 클러스터 전달, 워크플로의 클러스터 전달·이전 신청 호환·실패 정리) |
| 차트 | `helm lint`·`helm template` — ServiceAccount·Role·RoleBinding, `mnt-ainb-secret`, ConfigMap 환경변수, kubeconfig 볼륨 없음 확인 |
| 화면 | 변경 파일 eslint 통과, 타입 검사에서 변경 파일 오류 없음 |

※ 실제 MNT-PAAS(Magnum) kubeconfig와 Keycloak 로그인 화면은 개발 PC에서 확인하지 못하였습니다. 테스트배드 배포 후 확인하겠습니다.

## 6. 남은 사항
1. **Kubeflow 구성요소** — 서비스 클러스터마다 Notebook·Profile 컨트롤러가 필요합니다. 클러스터가 정해지면 저희가 설치 스크립트로 설치하겠습니다.
2. **가속기·모델 저장소 설정** — 가속기 자원 이름(`ACCEL_RESOURCE`)과 MinIO 계정(`MODEL_STORE_*`)은 모듈 전체에 하나로 지정됩니다. 클러스터마다 가속기가 다르면 클러스터별 지정이 필요하며, 가속기 활용 협의 결과에 따라 반영하겠습니다.
3. **접속 정보 받기** — MNT-PAAS에 서비스 간 인증이 없어 사용자 토큰으로 받습니다. 해당 클러스터를 사용할 수 있는 사용자가 로그인하거나 신청해야 접속 정보가 보관됩니다.
4. **화면 표시** — 워크스페이스·노트북 목록 화면에는 아직 클러스터 이름을 표시하지 않습니다(API 응답에는 포함됩니다).

## 7. 변경 파일
- `MNT-AINB/app/main.py` — 클러스터별 접속(접속 정보 받기·보관·갱신, 요청별 클러스터 지정, 목록 합치기, 기존 네임스페이스 넘겨받기, port-forward 경유), `/api/ainb/clusters`·`/clusters/sync`
- `MNT-AINB/app/workflows/activities.py` — 작업마다 클러스터 지정
- `MNT-AINB/config.env.example`, `MNT-AINB/README.md`
- `MNT-CORE/app/routers/tickets.py` — 신청의 `clusterId`, 프로젝트 연결 확인, 신청자 토큰 전달, 생성 시작 입력
- `MNT-CORE/app/workflows/ai_workspace_provision.py` — AINB 작업에 클러스터 전달
- `MNT-CORE/tests/test_ai_workspace.py`(신규), `tests/test_auth.py`(AINB 호출 대역 인자), `tests/run_tests.sh`(`ai_workspace.sql` 적용)
- `MNT-APP/` — 신청 화면 클러스터 선택(`components/workspace-requests.tsx`), 신청 상세 표시(`ticket/requests/page.tsx`), `lib/ainb-api.ts`·`paas-api.ts`·`ticket-api.ts`·`types.ts`
- `charts/openkcloud-portal/` — `values.yaml`(`backends.ainb`, `secrets.ainb`), `templates/backends.yaml`(ServiceAccount·Role·RoleBinding, 볼륨 optional), `templates/secrets.yaml`(`mnt-ainb-secret`)
