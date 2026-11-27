<!-- README.md: Tenstorrent install-Job 멱등 경로 라이브 검증 사용법 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# Tenstorrent install-Job 멱등 경로 라이브 검증

## 왜

이번 검증은 operator 가 install Job 을 만드는 것을 그대로 관찰하는 방식을 가정했으나, 그 경로는
이번 조건에서 막혀 있다 — `internal/upgrade` 상태기계는 **업그레이드 전이(desired != current)**
이거나 **NDR 이 driverLoaded=false 를 보고하는 self-heal** 일 때만 install Job 을 렌더한다
(`internal/upgrade/state_machine_job.go` `handleUpgradingJob`, `driverNotLoaded`). 이번 검증은 버전을
2.8.0 에 고정하고 모듈을 언로드하지 않는 것이 안전 조건이라 둘 다 금지된다 — 상태기계를 통해서는
트리거할 방법이 없다. 그래서 설치 Job 은 driverjob.RenderInstallJob(policy, <tenstorrent-node>, 2.8.0-v1,
2.8.0) 로 렌더해 -manual 접미로 직접 적용한다(상태 기계는 정확한 이름으로만 Job 을 찾으므로 채택되지
않는다); 버전이 같아 entrypoint 가 SKIP_INSTALL 로 멱등 종료한다.

대신 `images/kcloud-operator-images/driver-installer/tenstorrent/entrypoint.sh:164-192` 를 보면,
현재 DKMS 버전이 target(DIP 가 선언한 버전)과 같으면 설치 단계를 건너뛴다(`SKIP_INSTALL=1` →
"설치 단계 건너뜀"). <tenstorrent-node> 는 이미 2.8.0 이 로드돼 있고 DIP 도 2.8.0 을 선언하므로, 상태기계를
거치지 않고 **정책이 이미 선언한 image/version 그대로** `internal/driverjob.RenderInstallJob` 을
호출해 나온 Job 을 그대로 적용해도 entrypoint 는 스킵 분기만 탄다 — 멱등·비파괴. 이게 이 디렉터리가
쓰는 라이브 검증 방법이다.

## 구성

- `dip-tenstorrent-blackhole.yaml` — 라이브 DIP `tenstorrent-blackhole-ds` spec 스냅샷
  (기록용 fixture-of-record, run.sh 가 적용하는 대상이 아니다 — run.sh 는 실제 라이브 DIP 를 직접
  조회한다). 라이브와 이 파일이 벌어지면 아래 조건(버전 일치·trackOnly:false)이 더 이상 성립하지
  않을 수 있으니 재실행 전 `kubectl get dip tenstorrent-blackhole-ds -o yaml` 로 대조할 것.
- `render/main.go` — 라이브 DIP 를 읽어 `internal/driverjob.RenderInstallJob` 으로 Job YAML 을
  뽑아 stdout 에 낸다. 상태기계를 거치지 않는다. 플래그: `-dip`(기본 `tenstorrent-blackhole-ds`),
  `-node`(기본 `<tenstorrent-node>`), `-namespace`(기본 `kcloud`). `-namespace` 는 DIP 조회에는 쓰이지
  않는다(DIP 는 cluster-scoped 라 controller-runtime 이 조용히 무시한다) — 대신 렌더된 Job 의
  namespace 를 이 값으로 덮어쓴다. `internal/driverjob.Namespace` 는 `OPERATOR_NAMESPACE`
  Downward API env(클러스터 안에서만 채워짐)로 정해지는데, 이 프로그램은 로컬 `go run` 이라 그
  env 가 없어 패키지 기본값 `kube-system` 으로 떨어진다 — 실제 operator 배포 namespace(`kcloud`)와
  다르므로 반드시 덮어써야 한다.
- `run.sh` — render 출력을 `kubectl apply -f -` 로 적용, `.status.succeeded`/`.status.failed` 를
  폴링, 로그 확인, DUS/노드 라벨·allocatable 전후 스냅샷, Job 삭제, JSON 요약 한 줄 출력까지 한다.

## 사전 조건 (매 실행 전 확인)

`run.sh` 는 DUS 가 `Idle` 이고 `currentVersion==desiredVersion` 인지는 스스로 재확인해 어긋나면
`BLOCKED_BY_CONSTRAINT` 로 적용 자체를 하지 않는다. 그러나 아래 둘은 클러스터 전역 조회라 스크립트
밖에서 호출자가 직접 확인해야 한다:

- **TT 워크로드 파드 없음** — `kubectl get pods -A -o json` 을 훑어 어떤 컨테이너의
  `resources.limits`/`requests` 에도 `tenstorrent` 문자열을 포함한 리소스 키가 없어야 한다(있으면
  적용 자체를 하지 않는다 — install Job 은 host-privileged 라 동시 워크로드가 있으면 안전하지 않다).
- **노드 라벨** `kcloud.ai/driver-tenstorrent.version=2.8.0`(`kubectl get node <tenstorrent-node> -o json`).

## 실행

```bash
cd kcloud-operator
DIP=tenstorrent-blackhole-ds NODE=<tenstorrent-node> NAMESPACE=kcloud DUS=<tenstorrent-node> \
  bash test/live/tt-install-job/run.sh
```

모든 env 변수는 선택이며 기본값이 위와 같다. `TIMEOUT`(기본 600초), `INTERVAL`(폴링 간격, 기본 5초)도
조정 가능하다.

## 기대 출력

DUS/노드 전후 스냅샷, 렌더된 Job YAML, `kubectl apply` 결과, Job 로그, 삭제 확인을 순서대로 찍은 뒤
마지막 줄에 JSON 요약 하나:

```json
{"job":"kcloud-tenstorrent-blackhole-p150-install-<8hex>-manual","namespace":"kcloud","node":"<tenstorrent-node>","phase":"Succeeded","skipBranch":"skip","stateUnchanged":true,"verdict":"PASS","date":"..."}
```

- `phase`: `Succeeded`/`Failed`/`Timeout`/`Aborted`/`ApplyFailed`.
- `skipBranch`: 로그에서 "설치 단계 건너뜀"(entrypoint.sh:190)을 찾으면 `skip`, 실제 설치/언로드
  시도 문구(`DKMS 빌드`/`DKMS install`/`baked tt-kmd .* 소스를`/`기존 tenstorrent .* 언로드 시작`)를
  스킵 문구 없이 보면 `install_or_unload_attempted`(이 경우 대기를 끊고 즉시 Job 을 지운다 — 재시도
  없음), 둘 다 없으면 `not_observed`.
- `stateUnchanged`: DUS·노드 라벨·allocatable 스냅샷이 실행 전후로 바이트 단위로 같은지.
- `verdict=PASS` 는 `phase=Succeeded` **그리고** `skipBranch=skip` **그리고** `stateUnchanged=true`
  셋 다일 때만. 그 외는 `FAIL`(`BLOCKED_BY_CONSTRAINT` 가드가 먼저 걸리면 `verdict=BLOCKED`).

## 한계

- **상태기계의 업그레이드 전이 경로 자체는 미검증이다.** 이 fixture 는 `RenderInstallJob` 함수만
  재사용하고 `handleUpgradingJob`/`handleValidatingJob`(Job 생성→검증→전이)을 우회한다. 실제 업그레이드
  전이를 라이브로 보려면 버전을 실제로 바꿔야 하는데, 이 fixture 의 안전 조건(버전 2.8.0 고정)과
  상충해 범위 밖이다(`BLOCKED_BY_CONSTRAINT`).
- **재부팅 폴백 없음** — `entrypoint.sh` 의 `unload_old`(90-93행)는 `rmmod` 가 10회 재시도 후에도
  실패하면(참조 카운트>0) 그대로 `return 1` 로 Job 을 실패시킨다. 재부팅 후 재시도하는 별도 경로가
  없다. 이번 검증은 애초에 언로드 분기를 타지 않으므로(버전 불변) 이 경로 자체를 실행하지 않는다.
- **TT 전용 롤백 분기 없음** — install Job 실패 시 상태기계는 generic `DriverUpgradeState` 의
  Failed/Rollback 전이에 의존한다(`RollbackTarget`/`RollbackOnFailure`). 이 fixture 는 상태기계
  바깥에서 Job 을 직접 적용하므로 실패해도 그 전이가 트리거되지 않는다 — 미검증.
  `RollbackOnFailure:true` 가 DIP 에 있어도 이 경로에서는 무의미하다.
  Job이 실제로 실패하면(`phase=Failed`) 재시도하지 말고 로그를 그대로 기록한다.
- **`versionSource: Policy`** — 정책이 선언한 버전을 그대로 신뢰한다. 누군가 이 fixture 와 무관하게
  DIP `.spec.driver.version` 을 올리면, 다음 정상 reconcile 에서 상태기계가 실제로 모듈을 교체한다.
  `autoUpgrade:false` 는 자동 트리거만 막을 뿐 수동 스펙 변경까지 막지는 않는다.
- TT 워크로드 파드 부재 확인은 `run.sh` 실행 시작 시점 호출자의 1회 확인일 뿐(위 사전 조건 절),
  실행 중 지속 감시가 아니다.

## 알려진 stale fixture

루트(별도 git 저장소가 아닌 untracked) `tester/manifests/driver-upgrade/manifests/`
`07_driverinstallpolicy-tenstorrent-trackonly.yaml` 은 `trackOnly: true`, 이미지 태그
`0.1.1` 을 선언한다 — 라이브(`trackOnly: false`, `tenstorrent-driver-ds:2.8.0-v1`)와 맞지 않는
stale 상태다. 참조하지 말 것. 이 디렉터리의 `dip-tenstorrent-blackhole.yaml` 이 실제 스냅샷이다.
