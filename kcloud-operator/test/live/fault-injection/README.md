<!-- README.md: 파괴적 장애 주입 3종 kind 재현 사용법 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->

# 파괴적 장애 주입 kind 재현 (fault-injection)

`test/fault`(envtest)가 이미 세 파괴적 시나리오의 제어 흐름을 증명했다 — 건강 실패,
실패 업그레이드+롤백, 복구 반복 실패. 이 디렉터리는 그 증명을 운영 클러스터가
아니라 `test/lab/kind`가 띄우는 실험 클러스터 위에서, 가짜 신호로 비파괴 재현하고 각각
evidence 로 남긴다.

## 시나리오 ↔ envtest 스펙 대응표

| # | 디렉터리 | 재현하는 전이 | 대응하는 envtest 스펙 | 신호 |
|---|---|---|---|---|
| 01 | `01-health-degraded/` | `AdvertisementMismatch` → `Degraded`, `allocationAllowed=false`, `Revalidate` 작업 생성, 노드 배치 제외 라벨 | `test/fault/fault_test.go:396-428`(F12, `StateDegraded` :408) | 가짜 `NodeDeviceReport`+`AcceleratorPartitionPolicy`+`AcceleratorEvidence` |
| 02 | `02-failed-upgrade-rollback/` | job 모드 업그레이드 검증 영구 실패 → `Rollback` → 롤백 Job 도 실패 → `RollbackAttempts` 소진 → `Failed`(유지) | `test/fault/sequence_test.go:69-113`(Sequence B, 롤백 절반) | 가짜 `DriverInstallPolicy`(mode=job)+`NodeDeviceReport`(driverLoaded=false)+직접 생성한 `DriverUpgradeState` |
| 03 | `03-recovery-failure/` | `RecoverDevice` 복구 반복 실패(참가자가 자연 발생시킴) → `Quarantined`, 자동 `Healthy` 복귀 없음 | `test/fault/sequence_test.go:141-178`(Sequence D, `StateQuarantined` :174, `AllowedTransition` 불변식 :176), `test/fault/fault_test.go:345-367`(F10, 보상 한도 후 `ManualRecoveryRequired` — 같은 "반복 실패→자동 실행 중단" 결론의 다른 레이어) | 가짜 `NodeDeviceReport`(driverLoaded=false) 하나뿐 |

세 스크립트 모두 `test/fault/fault_test.go:194`(F06, 동시 진입 충돌 판정)이 다루는
"충돌하는 작업 동시 진입" 관점은 건드리지 않는다 — 그 관점은 이 세 시나리오와 독립이다.

## 실행 방법

공용 부트스트랩(`test/lab/kind/{setup.sh,teardown.sh}`, 별도 문서:
`test/lab/kind/README.md`)에 의존한다. 이 디렉터리는 부트스트랩을 새로 만들지 않고
재사용한다.

```bash
bash test/lab/kind/setup.sh                       # kind 클러스터 + CRD + operator(클러스터 밖) 구동
export KUBECONFIG=/tmp/kcloud-lab/kubeconfig

bash test/live/fault-injection/01-health-degraded/run.sh
bash test/live/fault-injection/02-failed-upgrade-rollback/run.sh
bash test/live/fault-injection/03-recovery-failure/run.sh

bash test/lab/kind/teardown.sh                    # kind 클러스터 + operator 프로세스 정리
```

세 스크립트는 서로 독립이다 — 순서를 바꾸거나 하나만 실행해도 된다(각자 자신이 만든
자원만 정리한다). 세 스크립트 다 `DRY_RUN=1`을 지원한다(클러스터 상태를 바꾸는 명령을
하나도 실행하지 않고 그 명령만 stderr 에 찍는다).

```bash
DRY_RUN=1 bash test/live/fault-injection/01-health-degraded/run.sh
```

`bash -n`·`shellcheck -x`(같은 디렉터리에서 실행 — `source ../lib.sh` 를 상대경로로
따라가게 하려면 `-x` 와 함께 그 디렉터리 안에서 돌려야 한다)로 정적 검사한다.

## 공용 헬퍼(`lib.sh`)

신규 `run.sh` 는 보통 `lib/*.sh` 같은 공용 소스를 쓰지 않고 스크립트 하나로 완결하지만, 이
세 스크립트는 같은 배치로 만들어진 형제 시나리오이고 그 중 02 는 `test/live/tt-upgrade-transition/run.sh`(같은 저장소의
기존 작업)이 이미 검증한 기법을 그대로 재사용한다. 그 저수준 배관(kubectl `DRY_RUN`
래퍼, 폴링, 검사 집계, 공통 JSON 요약)을 세 스크립트가 각자 베껴 쓰면 세 벌의 사본이
생기므로, `lib.sh` 하나로 좁힌 의도적 예외다. 시나리오별 매니페스트(NDR·ACPP·DIP·DUS·
정책)는 각 `run.sh` 안에 그대로 남아 있다.

## 라이브 실행에서 드러난 것 (재사용 시 참고)

- **`AcceleratorHealthStatus.AllocationAllowed` 는 `omitempty`.** 컨트롤러가 실제로
  `false` 를 쓰면(Go 구조체 마샬링이 그 필드를 status 에서 뺀다) `kubectl -o
  jsonpath='{.status.allocationAllowed}'` 는 `"false"` 문자열이 아니라 **빈 문자열**을
  낸다. operator 자신의 로직에는 영향이 없다(역직렬화 시 키 부재는 Go bool 기본값
  `false` 와 동치) — 다만 이 필드를 읽는 외부 스크립트는 jsonpath 대신 `kubectl ... -o
  json | jq '(.status.allocationAllowed // false)'` 로 읽어야 한다. 01·03 번 스크립트가
  이렇게 읽는다(1차 라이브 실행에서 jsonpath 로 읽다가 실패해 발견).
- **`AdvertisementMismatch` 로 인한 `Degraded` 는 고정 상태가 아니라 반복되는
  blip 이다.** health 자신이 만드는 `Revalidate` 작업이 존재하는 동안은
  `RecoveryInFlight` 로 `Recovering` 이 되고, 그 경로는 `Advertisement` 신호 자체를
  기록하지 않아 `SuspectedAt`(유예 타이머 기준시각)을 지운다(`nextSuspectedAt`). 그
  결과 `Degraded` 는 약 55~90초 주기로 나타났다 사라지기를 반복한다 — 01 번 스크립트는
  넉넉한 시간 예산(240초)과 촘촘한 표본 간격(2초)으로 이 blip 을 잡는다. 코드 결함으로
  보진 않았다(격리·재검증·차단은 매번 정확히 일어난다는 것을 확인했다) — 다만 "Degraded
  가 안정적으로 유지된다"고 가정하고 폴링을 짜면 이 스크립트가 겪은 것과 같은 오탐을
  만든다.
- **기본 NDR 신선도 유예(90초)가 다분 시나리오에서 만료될 수 있다.** 이 스크립트들은
  실제 node-agent 처럼 관측을 주기적으로 갱신하지 않고 `NodeDeviceReport.status.observedAt`
  을 한 번만 심는다 — 시나리오가 90초를 넘겨 돌면 의도한 신호(Degraded/Quarantined)
  대신 `Unknown`(`ObservationStale`)으로 새어 버린다(01 번 1차 실행에서 실측). 01·03 번은
  전용 `AcceleratorHealthPolicy` 로 `ndrFreshnessSeconds` 만 넉넉히 늘려 피한다(그 정책이
  검증하려는 기준인 유예·임계·주기는 건드리지 않는다).

## 한계

kind 랩은 실제 가속기 장치가 없다. 세 시나리오 모두 제어 흐름(CRD reconcile, 상태 전이,
컨디션·라벨 갱신)만 재현하며, 하드웨어 자체의 고장 거동(드라이버가 실제로 응답을 멈추는
방식, 실제 재부팅이 필요한 복구 등)은 이 랩의 범위가 아니다. 그 판정은
라이브 문서와 `test/fault` 의 envtest 하네스가 맡는다. 02 번은 kind 노드가
`node-role.kubernetes.io/control-plane` 라벨을 가져 `DriverUpgradeState` 자동생성 경로
(`ensureUpgradeStates`)를 이 랩 구조로는 검증하지 못한다 — 운영 클러스터(워커 노드,
그 라벨 없음)의 기존 운영 이력이 그 경로의 실질 증거다.
