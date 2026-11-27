<!-- README.md: kind 실험 클러스터(kcloud-lab) 부트스트랩 사용법 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->

# kind 실험 클러스터(kcloud-lab)

Tenstorrent 업그레이드 전이 재현, 결함 주입 시나리오 3종, 벤더 차트 설치
자국 비교 등 뒤 검증 작업이 공유하는 실험용 kind 클러스터 부트스트랩이다.
운영 클러스터와 완전히 분리된 별도 kind 클러스터에서 operator 를
구동해, 실제 가속기 없이 제어 흐름만 재현한다.

## 전제

- `kind`(검증 버전: v0.30.0), `kubectl` 설치.
- `docker` — 이 호스트는 일반 사용자로 docker 소켓에 접근할 수 없어 `sudo`
  로만 쓸 수 있다. 스크립트도 `kind` 명령을 `sudo` 로 실행한다. 비밀번호
  없이 `sudo` 가 되는 환경을 전제한다. 그렇지 않으면 스크립트가 암호 입력
  프롬프트에서 멈춘다.
- `kwok`, `crane`, `skopeo` 는 이 호스트에 없다. 이 랩은 실제 kind 노드
  위에서 operator 를 구동하는 방식만 쓴다.
- 저장소 루트에서 `make run` 이 도는 데 필요한 Go 툴체인과 `bin/controller-gen`
  이 이미 받아져 있어야 한다.

## 사용법

```bash
bash test/lab/kind/setup.sh      # kind 클러스터 생성 + CRD 적용 + operator 백그라운드 구동

KUBECONFIG=/tmp/kcloud-lab/kubeconfig kubectl get nodes
KUBECONFIG=/tmp/kcloud-lab/kubeconfig kubectl get crd | grep -c npu.ai   # 13 이어야 함

bash test/lab/kind/teardown.sh   # operator 프로세스 정리 + 클러스터 삭제
```

### CRD 적용 방식

`setup.sh` 는 `deploy/helm/crds/` 디렉터리를 통째로 `kubectl apply -f` 한다.
이 디렉터리는 helm 차트가 실제로 배포하는 CRD 전체 세트(13개)이고, `config/crd/bases/`
와 `diff -r` 기준으로 내용이 같다(`make crd-check` 가 이 일치를 지킨다). kustomize
없이 차트가 배포하는 CRD 디렉터리를 그대로 적용한다.

`DRY_RUN=1` 을 붙이면 실제로는 아무것도 하지 않고 실행될 명령만 출력한다.

```bash
DRY_RUN=1 bash test/lab/kind/setup.sh
DRY_RUN=1 bash test/lab/kind/teardown.sh
```

두 스크립트 모두 재실행에 안전하다(idempotent). `setup.sh` 는 `kcloud-lab`
클러스터가 이미 있으면 재사용하고, operator 가 이미 떠 있으면 다시 띄우지
않는다. `teardown.sh` 는 클러스터나 operator 프로세스가 이미 없어도 그대로
성공(exit 0)한다.

### 경로·환경변수

| 이름 | 기본값 | 용도 | 적용 스크립트 |
|---|---|---|---|
| `LAB_DIR` | `/tmp/kcloud-lab` | kubeconfig·operator 로그·PID 파일을 두는 디렉터리 | setup, teardown |
| `CLUSTER_NAME` | `kcloud-lab` | kind 클러스터 이름 | setup, teardown |
| `READY_TIMEOUT` | `180` | `make run` 이 `starting manager` 로그를 찍을 때까지 대기 상한(초) | setup |

`LAB_DIR` 아래 생기는 파일:

- `kubeconfig` — 이 kind 클러스터 전용 kubeconfig. `sudo kind create
  cluster` 가 root 소유로 만들기 때문에 `setup.sh` 가 현재 사용자로
  `chown` 해 둔다.
- `operator.log` — `make run` 의 표준출력·표준에러. `tail -f
  /tmp/kcloud-lab/operator.log` 로 조정 로그를 실시간으로 볼 수 있다.
  `teardown.sh` 는 이 파일을 지우지 않으므로 정리 이후에도 확인할 수 있다.
- `operator.pid` — 백그라운드로 띄운 `make run` 의 PID. `setsid` 로 새
  세션을 열어 띄우므로 이 PID 는 곧 프로세스 그룹 ID(PGID)이기도 하다.
  `teardown.sh` 는 이 값으로 `make` → `go run` → 컴파일된 바이너리 전체를
  한 번에 정리한다.

## 운영 클러스터와의 분리

두 스크립트는 `kubectl`/`make` 호출마다 `KUBECONFIG=$LAB_DIR/kubeconfig` 를
명시적으로 넘긴다. 사용자 기본 `~/.kube/config`(운영 클러스터를
가리킴)는 어느 스크립트도 읽거나 쓰지 않는다. 두 스크립트를 몇 번 돌려도
`kubectl config current-context` 는 그대로 `kubernetes-admin@cluster.local`
로 남는다.

## 뒤 검증 작업과의 관계

이 부트스트랩(kind 클러스터 + CRD + 클러스터 밖 operator)은 뒤 검증 작업이
그대로 재사용하는 공용 토대다. 각 작업은 이 위에 자신에게 필요한 가짜
리소스(AcceleratorDescriptor, NPUClusterPolicy 등)를 적용하거나 벤더 차트를
설치해서 검증한다.

## 한계

kind 노드에는 실제 가속기 장치가 없다. 이 랩은 operator 의 제어 흐름(CRD
reconcile, 상태 전이, 컨디션 갱신)을 재현하는 토대이며, 드라이버 로딩이나
장치 초기화 같은 하드웨어 거동은 재현하지 못한다.
