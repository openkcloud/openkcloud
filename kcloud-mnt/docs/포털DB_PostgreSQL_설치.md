# 포털 DB (PostgreSQL) 설치 — 관리 K8s + CloudNativePG

- 작성일: 2026-10-01
- 대상: openKcloud 포털 운영 DB (사용자·자원신청·감사 등 포털 메타데이터)
- 위치: 관리 K8s 클러스터, 네임스페이스 `openkcloud-mnt`
- 결과: PostgreSQL 17.11 단일 인스턴스 기동 + 접속 검증 완료

---

## 1. 개요 / 결정사항

- 포털 앱을 **관리 K8s 파드**로 배포할 계획 → DB도 **같은 클러스터 파드**로 배치(앱-DB 동일 계층: Secret 주입·DNS·자동 페일오버·오픈스택 비의존 운영).
- 배포는 **CloudNativePG 오퍼레이터** 사용(선언형 CR 하나로 Pod/PVC/Service/백업/페일오버 관리).
- 스토리지는 관리 클러스터 기본 **Rook-Ceph RBD**(StorageClass `general`) 사용.
- DR: Ceph 운명공유(오픈스택 Cinder와 동일 Ceph) 때문에, 추후 `pg_dump` 논리백업을 오프-Ceph로 별도 구성 예정.

---

## 2. 사전 조건 / 환경

- kubectl 관리자 접근(master3).
- 레지스트리 egress: ghcr.io 정상(`curl https://ghcr.io/v2/` → 401=정상), PG 17.11 이미지 pull 가능.
- 노드: 워커 compute1/5/6(128c/503Gi), controller·mlt, 컨트롤플레인 master1~3(96c/503Gi). 사용률 <5%.
- 주의: **master1~3에 NoSchedule taint 없음** → 파드가 마스터에도 뜰 수 있어 nodeAffinity로 제외 필요.

---

## 3. 설치 절차

### 3-1. CloudNativePG 오퍼레이터
```bash
kubectl apply --server-side -f \
  https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.24/releases/cnpg-1.24.1.yaml
kubectl -n cnpg-system rollout status deploy/cnpg-controller-manager --timeout=300s
kubectl -n cnpg-system get pods     # cnpg-controller-manager 1/1 Running
```

### 3-2. 네임스페이스
```bash
kubectl create namespace openkcloud-mnt
```

### 3-3. Secret + PostgreSQL Cluster (매니페스트)
`openkcloud-mnt-db.yaml`:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: openkcloud-db-app-cred
  namespace: openkcloud-mnt
type: kubernetes.io/basic-auth
stringData:
  username: portal_user
  password: <DB_PASSWORD>            # 운영 전환 시 교체 + SealedSecrets
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: openkcloud-db
  namespace: openkcloud-mnt
spec:
  instances: 1
  imageName: ghcr.io/cloudnative-pg/postgresql:17.11
  storage:
    storageClass: general          # Rook-Ceph RBD (블록, RWO)
    size: 50Gi
  bootstrap:
    initdb:
      database: openkcloud_portal
      owner: portal_user
      secret:
        name: openkcloud-db-app-cred
  resources:
    requests: { cpu: "1", memory: 4Gi }
    limits:   { cpu: "4", memory: 8Gi }
  postgresql:
    parameters:
      max_connections: "200"
      shared_buffers: "2GB"          # RAM(8Gi)의 ~25%
      effective_cache_size: "6GB"    # ~75%
      work_mem: "16MB"
      maintenance_work_mem: "512MB"
  affinity:
    enablePodAntiAffinity: true
    topologyKey: kubernetes.io/hostname      # 복제본을 서로 다른 노드에 분산(HA 대비)
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: node-role.kubernetes.io/control-plane
                operator: DoesNotExist        # master1~3 제외 → 워커에만 배치
```
적용:
```bash
kubectl apply -f openkcloud-mnt-db.yaml
```

> 수정 후 재적용도 동일: `kubectl apply -f openkcloud-mnt-db.yaml` (기존 리소스 업데이트, 파드 롤링 재생성, PVC=데이터 유지).

---

## 4. 검증

### 4-1. 리소스 상태
```bash
kubectl -n openkcloud-mnt get clusters.postgresql.cnpg.io openkcloud-db   # CNPG는 FQN으로(아래 주의)
kubectl -n openkcloud-mnt get pods,svc,pvc -o wide
```
기대 결과:
- Pod `openkcloud-db-1` **Running(1/1)**, NODE=워커(예: compute5)
- Service `openkcloud-db-rw`(쓰기/primary) · `-ro`(읽기) · `-r` 모두 5432
- PVC `openkcloud-db-1` **Bound, 50Gi, `general`, RWO**

### 4-2. DB·롤 생성 확인 (postgres, peer)
```bash
kubectl -n openkcloud-mnt exec -it openkcloud-db-1 -- \
  psql -U postgres -d openkcloud_portal -c '\du' -c '\l'
```
→ 롤 `portal_user` 존재, DB `openkcloud_portal`(owner portal_user) 확인.

### 4-3. 앱 접속 경로 검증 (portal_user, TCP+비번)
```bash
kubectl -n openkcloud-mnt exec -it openkcloud-db-1 -- \
  env PGPASSWORD='<DB_PASSWORD>' psql -h openkcloud-db-rw -U portal_user -d openkcloud_portal \
  -c 'select current_user, current_database();'
```
→ `portal_user | openkcloud_portal` 반환 = 성공.

---

## 5. 접속 정보

| 항목 | 값 |
|---|---|
| Host(쓰기) | `openkcloud-db-rw.openkcloud-mnt.svc.cluster.local` |
| Host(읽기) | `openkcloud-db-ro.openkcloud-mnt.svc.cluster.local` |
| Port | `5432` |
| Database | `openkcloud_portal` |
| User / Password | `portal_user` / `<DB_PASSWORD>` (Secret `openkcloud-db-app-cred`) |
| DSN | `postgresql://portal_user:<DB_PASSWORD>@openkcloud-db-rw.openkcloud-mnt:5432/openkcloud_portal` |

앱 파드 주입:
```yaml
env:
  - name: DB_USER
    valueFrom: { secretKeyRef: { name: openkcloud-db-app-cred, key: username } }
  - name: DB_PASS
    valueFrom: { secretKeyRef: { name: openkcloud-db-app-cred, key: password } }
```

---

## 6. 운영 메모 / 함정

- **이름 충돌**: `kubectl get cluster`는 CAPI(`clusters.cluster.x-k8s.io`)와 충돌 → CNPG 조회는 반드시 **`kubectl get clusters.postgresql.cnpg.io`**.
- **peer 인증**: 파드 내 로컬소켓 `psql -U portal_user`는 peer 인증 실패가 정상(OS유저 postgres≠portal_user). 앱/검증은 **TCP(-h)+비밀번호**로 접속.
- **비밀번호 평문**: 매니페스트 git 커밋 금지 → 운영 전 **SealedSecrets/External Secrets** 전환.
- **스토리지 공유 운명**: `general`(Ceph RBD)은 오픈스택 Cinder와 동일 Ceph → DR용 오프-Ceph 백업 별도 필요.

---

## 7. Temporal 저장소 (포털 DB 안 스키마 분리)

프로비저닝 워크플로를 관리하는 Temporal 서버(MNT-WORKFLOW)의 저장소를 별도 DB 없이 `openkcloud_portal` 안에 둔다.

| 계정 | 스키마 | 용도 |
|---|---|---|
| `temporal` | `temporal` | 실행 기록(워크플로 상태·이력) |
| `temporal_view` | `temporal_visibility` | 검색·목록(웹 화면의 워크플로 목록 등) |

- 계정마다 `search_path` 를 자기 스키마로 고정 → Temporal 테이블이 포털 테이블(`public`)과 섞이지 않는다.
- 준비(슈퍼유저 1회): `MNT-WORKFLOW/db/temporal_schema.sql` — 계정·스키마·`btree_gin` 확장. 실행 절차는 `포털_배포_가이드.md` 1장 (9)·(10).
- 테이블은 mnt-workflow 파드의 initContainer 가 Temporal 도구로 설치·업그레이드한다(멱등). 1.31.3 기준 `temporal` 40개, `temporal_visibility` 3개.
- 주의
  - `btree_gin` 은 일반 계정이 만들 수 없어 슈퍼유저가 미리 만든다(없으면 검색 스키마 설치 실패).
  - 계정 하나·스키마 하나로 합치면 두 저장소의 버전 테이블(`schema_version`)이 겹쳐 검색 테이블이 만들어지지 않는다(검증함). 계정 2개 + 스키마 2개 유지.
  - 반드시 `-d openkcloud_portal` 에 접속해서 실행한다. 계정은 서버 전체 공통이지만 스키마·확장은 접속한 DB 에만 생긴다 — 테스트배드 첫 배포 때 `postgres` DB 에 적용돼 initContainer 가 실패했다(2026-10-12).

확인:
```sql
SELECT table_schema, count(*) FROM information_schema.tables
 WHERE table_schema IN ('temporal', 'temporal_visibility', 'public') GROUP BY 1;
```

---

## 8. Keycloak 저장소 (전용 DB)

Keycloak 은 포털 DB 와 섞지 않고 **전용 DB `keycloak`** 안의 **스키마 `keycloak`** 을 쓴다(Keycloak 이 테이블 약 100개를 직접 만들고 버전을 올릴 때 스스로 바꾸므로 분리).

| 항목 | 값 (테스트배드 확인, 2026-10-12) |
|---|---|
| 계정 | `keycloak` (특수 권한 없음) |
| DB | `keycloak` — 소유자 `keycloak`, UTF8, 정렬 `C`, 접속 권한 `{keycloak=CTc/keycloak}`(PUBLIC 제거) |
| 스키마 | `keycloak` (소유자 `keycloak`) — Keycloak 설정 `KC_DB_SCHEMA=keycloak` |
| 테이블 | 26.7.4 기준 스키마 `keycloak` 에 100개 (Keycloak 이 처음 기동할 때 생성) |
| Keycloak 접속 설정 | `KC_DB_URL_HOST=openkcloud-db-rw.openkcloud-mnt.svc.cluster.local`, `KC_DB_URL_DATABASE=keycloak`, `KC_DB_USERNAME=keycloak` |

- 준비 SQL(슈퍼유저, `postgres` DB 에 접속해 1회): `charts/infra/keycloak-dev/keycloak_db.sql` — 계정·DB·스키마. 개발 서버(Mac Mini)도 같은 SQL 로 같은 구조를 만든다.
  - 개발 서버 PostgreSQL 은 기본 정렬이 `en_US.UTF-8` 이라 SQL 에서 `C` 를 지정한다(`TEMPLATE template0`).

확인(테스트배드·개발 같은 결과여야 함):
```sql
SELECT datname, pg_get_userbyid(datdba) AS owner, pg_encoding_to_char(encoding) AS enc, datcollate, datacl
  FROM pg_database WHERE datname = 'keycloak';                      -- keycloak | keycloak | UTF8 | C | {keycloak=CTc/keycloak}
-- keycloak DB 에 접속해서
SELECT count(*) FROM information_schema.tables WHERE table_schema = 'keycloak';   -- 100
```

---

## 9. 남은 작업 (체크리스트)

- [ ] HA 전환: `instances: 3` (용량 충분, podAntiAffinity로 워커 분산)
- [ ] 스토리지 확장 필요 시 50Gi→100Gi (RBD 온라인 확장)
- [ ] `pg_dump` 오프-Ceph 백업 CronJob 구성 (DR)
- [ ] 비밀번호 Secret 보안 전환 (SealedSecrets)
- [ ] 스키마 설계(그린필드) — 원칙: "신청·이력·식별·감사만 DB, 자원 실시간 상태는 OpenStack/K8s 조회"
- [ ] 포털 앱 K8s 파드 배포 후 DB 연결(env 주입)
