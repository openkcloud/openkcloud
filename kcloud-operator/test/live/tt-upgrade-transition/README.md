<!-- README.md: TT Job-모드 업그레이드 전이 kind 재현 사용법과 결과 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->

# Tenstorrent Job-모드 업그레이드 전이 — kind 재현

## 왜 kind 에서 재현하는가

<tenstorrent-node> 노드는 tt-kmd 2.8.0 이 현재=목표 버전으로 고정돼 있어, 상태기계의
업그레이드 전이(`Upgrading→Validating→Uncordoning` 성공 경로, `Validating→Rollback→Failed`
실패 경로)를 실제 하드웨어로 켤 방법이 없다. 이 제약은 새로 발견한 것이 아니라
`test/live/tt-install-job/README.md` 가 이미 같은 결론을 남겼다 —
"상태기계의 업그레이드 전이 경로 자체는 미검증이다... 실제 업그레이드 전이를 라이브로 보려면
버전을 실제로 바꿔야 하는데, 이 fixture 의 안전 조건(버전 2.8.0 고정)과 상충해 범위 밖이다"
(실측도 동일하게
"state-machine upgrade transition BLOCKED_BY_CONSTRAINT (version pinned)" 로 기록).

이 디렉터리는 그 갭을 메운다. 실제 tt-kmd 드라이버는 건드리지 않되, `test/lab/kind/setup.sh`
가 띄운 kind 클러스터의 실제 apiserver·controller-runtime 위에서 같은 제어 흐름(전이 조건,
install Job 생성, validator 체인, 롤백 소진, 자기치유 가드)을 재현한다.

## 제약 사실 (근거 인용)

1. **전이는 버전 문자열 불일치로만 발동한다.** `handleIdle`(`internal/upgrade/state_machine.go:245`)
   은 `desiredVersion == currentVersion` 이면 Idle 을 유지하고, `:318-327` 에서
   `desiredVersion != currentVersion` 이고 `autoUpgrade` 가 켜져 있을 때만
   `state.Status.PreviousVersion = currentVersion` 을 채우고 `UpgradeRequired` 로 전이한다.
   이미지 태그는 이 조건에 들어가지 않는다 — `policy.Spec.Driver.Version` 문자열만 비교한다.
2. **그래서 같은 버전을 다시 빌드한 태그(예: `2.8.0-v2`)는 업그레이드로 취급되지 않는다.**
   버전 문자열이 그대로면 (1)의 조건이 거짓이라 `handleIdle` 이 Idle 을 유지한다.
3. **설치 스크립트도 baked 버전을 권위값으로 삼는다.**
   `images/kcloud-operator-images/driver-installer/tenstorrent/entrypoint.sh:36`
   (`TARGET_VERSION="${TT_KMD_VERSION:-}"`, Dockerfile 이 빌드 시점에 굽는 값,
   `driver-installer/tenstorrent/Dockerfile:13` 의 `ARG TT_KMD_VERSION="2.8.0"`)이 DIP 가 보낸
   `DRIVER_VERSION` env 보다 우선하며(`entrypoint.sh:161` 경고 로그), 로드된 DKMS 버전과
   TARGET_VERSION 이 같으면 설치를 건너뛴다(`entrypoint.sh:176-178`, "설치 단계 건너뜀").
   initContainer `driver-manager.sh` 도 같은 규칙으로 모듈 해제 자체를 skip 한다
   (`driver-manager.sh:51`, `if [[ "$CURRENT_VER" == "$DRIVER_VERSION" ]]`).
4. **운영 클러스터의 tt-kmd 는 2.8.0 이 baked 값이다.** 억지로 버전 문자열만 올리면(이미지는 그대로 두고
   `DIP.spec.driver.version` 만 상향) 실제 설치된 커널 모듈은 그대로 2.8.0 인데 상태기계는
   `desiredVersion` 을 새 값으로 보고 전이를 시작하고, `handleValidatingJob`
   (`internal/upgrade/state_machine_job.go:134`, validator 체인은 `:212-233`)의
   `DriverModuleValidator` 가 NDR 의 실제 로드 버전(여전히 2.8.0)과 불일치를 감지해 검증
   실패로 판정한다 — 그래서 이 방법은 유효한 업그레이드 증명이 아니라 반드시 롤백을 유발하는
   조작일 뿐이다.

**결론:** 실제 하드웨어에서의 드라이버 교체는 이미지가 아니라 baked `TT_KMD_VERSION` 이 바뀌어야
가능하고, 그 값은 새 tt-kmd 릴리스를 반영해 이미지를 재빌드해야 바뀐다.

## 이번 실행 시점 재확인 — 판정에 영향을 주는 사실

**판정 분기 A(새 버전 존재)의 전제가 실제로 참이다.** 이 작업의 판정 분기는 "새 tt-kmd 버전이
실행 시점에 존재하면 A(라이브 업그레이드), 없으면 B(kind 재현)" 인데, GitHub tt-kmd 릴리스를
읽기전용으로 조회한 결과(`api.github.com/repos/tenstorrent/tt-kmd/releases/latest`)
**`ttkmd-2.11.0`(정식 릴리스, draft/prerelease 아님)이 이미 나와 있다** — 운영 클러스터의 baked
값(2.8.0)보다 최신이므로, 코드가 정의한 분기 조건만 보면 A 의 전제가 성립한다.

그럼에도 이 작업은 B(kind 재현)로 진행했다 — 이 작업의 지시가 "운영 클러스터를 절대 건드리지 않는다"
로 명시적으로 못박혀 있고, 이미지 재빌드 + 라이브 업그레이드/롤백 실행은 그 경계를 넘는 별도
작업이기 때문이다. 그래서 "새 버전이 없다"가 아니라 **"새 버전은 있지만 이 작업 권한 밖"**
이 정확한 상태다.

> 위 인용은 스냅샷 시점의 값이다. 2.11.0 업그레이드를 마쳤고 현재 목록은
> `["2.8.0", "2.11.0"]` 이다(`deploy/helm/values.yaml`).

아래는 그 후속(A 분기) 작업을 실제로 착수할 때 밟아야 할 절차다 — 이 작업
범위 밖이며 실행하지 않았다:

1. `TT_KMD_VERSION=2.11.0` 으로 `driver-installer/tenstorrent` 이미지 재빌드·배포.
2. **`verifiedVersions` 화이트리스트에 `"2.11.0"` 추가.** 운영 클러스터의 Tenstorrent
   `DriverInstallPolicy` 는 `verifiedVersions: ["2.8.0", "2.9.0"]` 만 선언해 뒀다
   (`deploy/helm/values.yaml:633-636`). `handleIdle` 은 `desiredVersion` 이 이 목록에
   없으면 업그레이드를 거부하고 터미널 `UnverifiedVersion` 상태로 보낸다
   (`internal/upgrade/state_machine.go:299-313`) — job-모드 self-heal 경로도 같은
   게이트를 그대로 적용한다(`:225-231`). 이 단계를 건너뛰고 `desiredVersion: 2.11.0`
   만 선언하면 이번에 kind 로 검증한 업그레이드 경로가 아니라 곧장 `UnverifiedVersion`
   으로 떨어진다 — helm value(`values.yaml` 의 `verifiedVersions` 배열에 `"2.11.0"`
   추가) 또는 DIP 를 직접 `kubectl patch` 하는 두 방법 중 하나로 먼저 넓혀야 한다.
3. `DriverInstallPolicy.spec.driver.version` 을 `2.11.0` 으로 올려 실제 업그레이드 트리거.
4. 검증·롤백 결과를 이 kind 재현과 같은 방식(`test/collect/evidence.sh`)으로 기록.

1·3·4 단계(이미지 재빌드·운영 클러스터 라이브 검증) 자체는 후속 작업으로 분리해 사용자/조정자의
결정을 받아야 한다 — 5주 전에 나온 tt-kmd 2.11.0 반영 여부가 이 프로젝트의 별도 결정
사항이다.

## kind 재현 설계

### install Job 스텁 이미지

`RenderInstallJob`/`RenderRollbackJob`(`internal/driverjob/job.go:51,66`) 이 만드는 컨테이너는
`Command` 를 오버라이드하지 않는다 — 이미지 자체의 ENTRYPOINT/CMD 가 유일한 동작이다.
`stub/Dockerfile` 은 `busybox:1.36` 기반에 `STUB_EXIT_CODE` 빌드 인자를 굽는다: `exit 0` 은
설치 성공, `exit 1` 은 설치 실패를 흉내낸다. `DriverInstallPolicy.spec.driver.installer: ngc` 를
택한 이유는 `reconstructPrevImage`(`internal/upgrade/state_machine_job.go:425-447`)가 `ngc`
인스톨러에서만 이미지 태그의 `-v<N>` variant 접미사를 보존한 채 버전 부분을 치환하기
때문이다 — desired 이미지 태그를 `2.0.0-v1` 로 두면 롤백 대상 이미지는 코드가 자동으로
`1.0.0-v1` 을 계산한다. `apt`/`script` 인스톨러는 롤백도 같은 이미지를 재사용해(버전은
`DRIVER_VERSION` env 로만 구분) 성공/실패를 이미지로 분리할 수 없다 — 그래서 `ngc` 를 택했다.
run.sh 의 preflight 가 두 태그를 빌드하고 `kind load docker-image` 로 로드한다.

### 가짜 리소스

- `NodeDeviceReport`(이름=kind 노드): `status.devices[0]` 에 `vendor: tenstorrent`,
  `driverLoaded`·`driverVersion` 을 시나리오별로 채운다.
- `DriverInstallPolicy`: 두 시나리오가 완전히 같은 스펙을 공유한다(`mode: job`,
  `autoUpgrade: true`, `drainEnabled: false`, `rollbackOnFailure: true`,
  `validationTimeout: 180s`, `maxRollbackAttempts: 1`, `idleCooldownSeconds: 0`,
  `jobOverrides.backoffLimit: 0`). `drainEnabled: false` 는 의도적 축소다 — 이 재현은
  install Job 트리거·검증·롤백에 집중하고, daemonset 경로와 공유하는 기존 drain/evict 흐름은
  범위 밖으로 둔다.
- `DriverUpgradeState`: **직접 생성한다.** kind 는 단일 노드 클러스터이고 그 노드는
  `node-role.kubernetes.io/control-plane` 라벨을 갖는데, `ensureUpgradeStates`
  (`internal/controller/driver_upgrade_controller.go:180,183`)가 control-plane/master
  라벨이 붙은 노드를 명시적으로 건너뛴다 — 그래서 NDR+DIP 만으로는 이 kind 클러스터에서 DUS
  가 자동 생성되지 않는다(실측 확인, 아래 "한계" 참고). `Reconcile`(같은 파일 :56-152)의 나머지
  단계는 `req.NamespacedName` 으로 DUS 를 조회하고 vendor/model 로 매칭 정책을 찾을 뿐 노드
  라벨을 다시 검사하지 않으므로, DUS 를 직접 만들어도 상태기계 자체의 제어 흐름은 동일하게
  탄다 — 검증 대상(전이 조건·Job 생성·validator·롤백)은 이 우회의 영향을 받지 않는다.
  `+kubebuilder:subresource:status` 라 `status` 는 별도 patch 로 채운다. **DIP 는 항상
  마지막에 적용한다** — DUS 를 먼저 만들면 매칭 정책이 없어 `Reconcile` 3단계가 즉시 no-op
  반환하므로(위 파일 :116), status 패치 전에 reconcile 이 빈 상태를 관측해 엉뚱한 분기
  (예: `currentVersion==""` self-heal)를 타는 경합을 피할 수 있다.
- 가짜 device-plugin: `DevicePluginValidator`(`internal/validator/validator.go:172`)가
  통과하려면 이름/라벨에 `device-plugin` 을 포함한 Pod 가 노드에서 `ContainersReady` 여야
  한다. **Pod 대신 DaemonSet 으로 둔다** — `handleValidatingJob` 의
  `restartStaleDevicePluginPods`(`internal/upgrade/state_machine_job.go:201`)가 install
  Job 완료 시각보다 먼저 뜬 device-plugin Pod 를 1회 삭제해 재스캔을 유도한다(실측: 성공
  시나리오에서 매번 발생, `kubectl get events -n kube-system` 의 `tt-device-plugin-fake-*`
  `Killing`/재생성 이력으로 확인). 맨 Pod 라면 그걸로 끝이지만 DaemonSet 이면 자동으로
  재생성되고, 새 Pod 는 그 Job 완료 이후 시각이라 재삭제되지 않는다(자기제한적, 코드 주석과
  일치).

### 왜 롤백 시나리오는 `UpgradeRequired` 부터 시작하는가

Idle 트리거 자체는 성공 시나리오가 이미 증명한다. 롤백 시나리오까지 Idle 부터 다시 태우려면
NDR 을 `driverLoaded=false`(모듈 미로드 흉내)로 둬야 하는데, 그러면 `handleIdle` 의 Job-모드
self-heal 분기(`driverNotLoaded`, 같은 파일 :215-243)가 버전-불일치 분기보다 먼저 발동해
`state.Status.PreviousVersion = ""` 로 비운다(:234) — 이러면 `handleRollbackJob`
(`state_machine_job.go:291`)이 첫 진입에서 곧장 "롤백할 이전 버전 없음" 으로 Failed 에
떨어져 `MaxRollbackAttempts` 소진 자체를 검증할 수 없다. 이는 회피가 아니라 self-heal 과
버전-불일치 두 분기가 같은 신호(`driverLoaded`)를 다르게 쓰는 데서 오는 코드상의 경계다.
그래서 이 시나리오는 `PreviousVersion` 을 직접 채운 `UpgradeRequired` 에서 시작해
`Upgrading→Validating→Rollback→Failed` 구간에 집중한다.

## 사용법

```bash
# 사전: kind 랩이 떠 있어야 한다(test/lab/kind 공용 부트스트랩)
bash test/lab/kind/setup.sh
export KUBECONFIG=/tmp/kcloud-lab/kubeconfig   # 기본 ~/.kube/config(운영 클러스터)는 절대 쓰지 않는다

bash test/live/tt-upgrade-transition/run.sh
# 또는 미리보기(클러스터 접근 0회, 로컬 docker build/kind load 도 생략)
DRY_RUN=1 bash test/live/tt-upgrade-transition/run.sh

bash test/lab/kind/teardown.sh   # 확인이 끝나면 랩 자체를 정리
```

스크립트는 kind 컨텍스트(`kind-*`)가 아니면 아무 것도 만들지 않고 `BLOCKED` 로 즉시 종료한다.
`OUT`(기본 `/tmp/tt-upgrade-transition-evidence`)에 두 시나리오의 전이 로그·이벤트 스냅샷·
install Job YAML·JSON 요약이 남는다. 주요 조정 가능 변수: `GOOD_VERSION`(기본 `2.0.0`),
`BAD_VERSION`(기본 `1.0.0`), `T_CONVERGE`/`T_FAILED`(기본 240s). 재실행해도 안전하다 —
`demo()` 시작에서 NDR/DUS/DIP 를 매번 새로 적용한다.

## 결과 (kind v0.30.0 / kindest/node:v1.34.0, operator `make run` out-of-cluster)

두 시나리오 모두 실제로 실행해 `verdict: PASS`, `fails: 0` 을 확인했다(`summary-*.json`).
아래 시각은 `success-transitions.log`/`rollback-transitions.log`(이 스크립트가 매 poll 마다
남긴 관측)에서 그대로 옮긴 것이다.

### 시나리오 1(성공): `Idle → Upgrading → Validating → Uncordoning → Idle`

| 시각(UTC) | 상태 | 관측 |
|---|---|---|
| 13:39:12 | (Idle→...→Draining) | 버전 불일치(1.0.0→2.0.0) 감지, cordon 완료 |
| 13:39:13 | Upgrading→Validating | `InstallJobCreated`: `kcloud-tenstorrent-generic-install-86324eb0` (image=`tt-upgrade-stub:2.0.0-v1`) |
| 13:39:16 | Validating | install Job `succeeded=1`(stub `exit 0`) → 스크립트가 NDR 을 `driverVersion=2.0.0` 으로 즉시 patch |
| 13:39:43 | Validating | `DevicePlugin` validator 1회 미통과("Pod 미스케줄") — `restartStaleDevicePluginPods` 가 기존 fake pod 를 방금 재시작시킨 직후라 아직 Ready 전 |
| 13:39:53 | **Idle** | `DriverModule` 통과(NDR 일치) + `DevicePlugin` 통과(재시작된 pod Ready) → `CurrentVersion=2.0.0` → Uncordoning → Idle("업그레이드 완료") |

Job 생성(13:39:13)부터 Idle 수렴(13:39:53)까지 40초. 노드 `spec.unschedulable` 은 수렴 후
공란(uncordoned) 확인.

### 시나리오 2(롤백): `UpgradeRequired → Upgrading → Validating → Rollback → Failed(유지)`

| 시각(UTC) | 상태 | 관측 |
|---|---|---|
| 13:39:53 | UpgradeRequired(직접 주입) | `PreviousVersion=1.0.0` 사전 기입, NDR `driverLoaded=false` |
| 13:39:53 | Draining→Upgrading | "노드 cordon 완료" |
| 13:39:56 | Validating | install Job 생성(desired=2.0.0, stub `exit 0`) |
| (반복) | Validating | `DriverModule` validator 지속 미통과 — "NDR.driverVersion 미보고"(`driverLoaded=false` 라 호스트 버전을 아예 못 읽음, `internal/validator/validator.go` 의 최종 catch-all 분기) |
| 13:43:06 | Validating(`msg=롤백 시작(job)`) | `validationTimeout(180s)` 초과 → `Rollback`(전이 자체는 한 reconcile 미만이라 `status.state` 로는 사실상 관측 불가 — 아래 "한계" 참고) → `handleRollbackJob` 이 rollback Job(image=`tt-upgrade-stub:1.0.0-v1`, `exit 1`) 생성, `rollbackAttempts=1` |
| 13:43:09 | **Failed** | rollback Job 도 실패(`backoffLimit=0` 즉시 실패) → 재시도 시도 시 `rollbackAttempts=2 > maxRollbackAttempts(1)` → `RollbackExhausted`, message="롤백 1회 초과: 수동 조치 필요 / cordon 해제됨(tenstorrent 광고 0, 다른 벤더 배치 복구)" |
| 13:43:24 | **Failed(유지)** | 15초 뒤 재확인 — 여전히 `Failed`. `handleFailed` 의 자기치유 가드(`failedStateRecovered`, `state_machine.go:831-851`)는 `desiredVersion==currentVersion && driverLoaded` 를 요구하는데, 이 시나리오는 `driverLoaded=false` 를 계속 보고해 조건 미충족 — 가드가 조건에 맞을 때만 연다는 문서화된 동작대로 **닫힌 채 유지**됐다 |

UpgradeRequired 진입(13:39:53)부터 Failed 도달(13:43:09)까지 3분 16초(대부분
`validationTimeout=180s` 대기). Failed 는 이후 스크립트가 정리(cleanup)할 때까지 self-heal
되지 않았다.

**참고(자기치유 가드가 여는 경우도 이번 세션에서 관측됨):** 설계를 검증하는 과정에서
`driverLoaded=true` 로 둔 채(모듈은 로드돼 있지만 버전만 다른, 더 흔한 실패 모양) 같은 롤백을
태운 적이 있는데, 이때는 Failed 도달 즉시(같은 reconcile 틱 이내) `FailedStateRecovered`
이벤트와 함께 Idle 로 되돌아갔다 — `state.Status.DesiredVersion`(롤백 대상 `1.0.0`)과
`state.Status.CurrentVersion`(`1.0.0`, 애초에 한 번도 안 바뀜)이 우연히 같아지고 드라이버도
로드된 것으로 보고돼 가드 조건이 그 즉시 참이 됐기 때문이다. 두 관측을 합치면 가드가 조건에
따라 정확히 열리고 닫힌다는 것을 확인한 셈이다 — 이 README 의 표는 조건이 닫혀 유지되는
쪽(더 드물고 문서 확인 가치가 큰 경우)을 정본으로 남긴다.

## Evidence

- 스크립트 자체 출력: `$OUT/success-transitions.log`, `$OUT/rollback-transitions.log`,
  `$OUT/success-events.txt`, `$OUT/rollback-events.txt`, `$OUT/success-install-job.yaml`,
  `$OUT/summary-*.json`.

## 한계

- **kind 재현은 제어 흐름만 증명한다.** 전이 조건·Job 생성·validator 체인·롤백 소진·자기치유
  가드가 실제 apiserver 위에서 코드가 문서화한 대로 동작하는지를 보이는 것이며, 실제 tt-kmd
  커널 모듈의 빌드·로드·언로드는 재현하지 않는다(스텁 이미지는 `exit 0`/`exit 1` 만 한다).
- **DUS 자동 생성 경로(`ensureUpgradeStates`)는 이 kind 랩에서 검증되지 않는다.** 단일 노드
  kind 클러스터의 유일한 노드가 `node-role.kubernetes.io/control-plane` 라벨을 가져
  `ensureUpgradeStates` 가 건너뛰기 때문에(위 "가짜 리소스" 절 참고), 이 재현은 DUS 를
  직접 만든다. NDR+DIP 만으로 DUS 가 자동으로 생겨야 하는 라이브 경로 자체는 운영 클러스터(워커
  노드, control-plane 라벨 없음)의 기존 운영 이력으로 이미 검증돼 있다 — 이 재현이 새로
  검증하는 것은 아니다.
- **`drainEnabled: false` 로 drain/evict 흐름은 범위 밖이다.** daemonset 경로와 공유하는
  기존 코드라 이 재현이 다시 확인할 필요가 적다고 판단했다.
- **실제 하드웨어 업그레이드는 `AWAITING USER(재빌드 2.11.0 → verifiedVersions 추가 →
  <tenstorrent-node> 라이브)`.** 위 "이번 실행 시점 재확인" 참고.
