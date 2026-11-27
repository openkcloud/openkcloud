<!-- README.md: AcceleratorHealth 건강 저하 관측 데모(run.sh) 실행법과 판정 기준 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# 건강 저하 관측 데모 (health-degraded-observe)

## 왜

`AcceleratorHealth` 는 노드마다 하나씩 있는 관측 산출물이다. 광고 붕괴·드라이버 이상·device-plugin
다운 같은 저하가 생기면 이 객체의 `state`(Healthy/Degraded/Unhealthy/Quarantined/Recovering)와
`allocationAllowed`, `reason`, `signals`, `devices` 가 그 사실을 담고, 저하 노드는 배치 후보에서
빠진다. 이 디렉터리의 `run.sh` 는 그 관측 표면을 라이브에서 읽어 표로 내고, 과거 라이브
evidence 가 기록한 저하 전이와 대조한다.

이 데모는 **장애를 주입하지 않는다.** 실제 장애 주입(device-plugin 강제 다운, 이미지 손상 등)은
파괴적이라 관측 데모의 범위 밖이다(`SCENARIO_DESTRUCTIVE="no"`). 대신 두 가지를 비파괴로 보인다.

- 저하가 지금 있으면 그대로 관측된다 — 표의 STATE/REASON/FAILING SIGNALS 가 그 관측이다.
- 저하가 없으면(정상 클러스터) 과거 evidence 의 복구 종점(Healthy·할당 허용)과 현재가 일치함을
  확인하고, 과거 evidence 가 기록한 저하 전이를 인용해 "저하 때 이 표면이 어떻게 움직이는지" 를 보인다.

## 무엇을 읽는가

- `kubectl get ah -o json` 한 번(읽기 전용). 노드별로 다음을 표로 낸다.
  - `state`, `allocationAllowed`, `reason`
  - 실패한 `signals`(name, 장치 신호면 target PCI 병기)
  - 비정상 `devices`(PCI:state)
- 상태를 바꾸는 명령은 하나도 실행하지 않는다.

## 과거 evidence 대조

과거 라이브 evidence 가 기록한 저하 전이를 기준으로 삼는다.

| 시점 | 관측 |
|---|---|
| T0 | device-plugin 광고 붕괴 주입 (`nvidia.com/gpu` 4 → 0) |
| +32s | `state=Recovering`, `allocationAllowed=false` — 신규 할당 차단(게이트 작동) |
| +48s | `state=Healthy` 복귀 |
| +58s | 광고 복원 (`nvidia.com/gpu=4`) |

복구 작업은 전 구간 정확히 1건(중복 생성 없음)이었다. 스크립트는 기본으로 이 evidence 파일을
`EVIDENCE` 환경변수로 준 경로에서 찾는다.
다른 위치면 `EVIDENCE=<경로>` 로 지정한다.

## 실행

```bash
bash test/live/health-degraded-observe/run.sh

# 명령만 미리 보기(클러스터 접근 없음)
DRY_RUN=1 bash test/live/health-degraded-observe/run.sh
```

| 변수 | 기본 | 뜻 |
|---|---|---|
| `EVIDENCE` | 위 상대경로 | 과거 라이브 evidence 파일 경로 |
| `OUT` | `/tmp/health-degraded-observe-evidence` | JSON 요약 저장 위치 |
| `DRY_RUN` | `0` | `1` 이면 실행할 읽기 명령만 출력 |

## 판정

스크립트는 마지막에 JSON 요약 한 줄을 낸다.

```json
{"test":"health-degraded-observe","scenario_destructive":"no","objects":5,"degraded":0,"phase":"cleanup","checks":{"ah_objects_present":true,"state_readable":true,"signals_readable":true,"allocation_gate_consistent":true},"fails":0,"verdict":"PASS","dryRun":false,"date":"..."}
```

검사 항목:

- `ah_objects_present` — `AcceleratorHealth` 객체가 하나 이상 있다.
- `state_readable` — 모든 객체에서 `state` 가 읽힌다.
- `signals_readable` — 모든 객체에 `signals` 가 하나 이상 있다.
- `allocation_gate_consistent` — 과거 evidence 가 세운 불변식이 현재도 성립한다:
  Healthy 노드는 `allocationAllowed=true` 이고 **게이트 신호가 모두 정상**이다; 고장 노드
  (Unhealthy·Quarantined·Recovering)는 `allocationAllowed=false` 다(게이트가 저하 노드를 배치에서 뺐다).

`DeviceObservation`(MIG 관측) 신호는 게이트 판정에서 뺀다. health 컨트롤러가 이 신호로는 상태를
정하지 않기 때문이다 — `internal/health/device.go:60-66` 이 "MIG 관측 실패는 상태 무관"(예:
<control-plane> 의 GTX 970 은 MIG 미지원이라 관측이 영원히 실패하지만 GPU 는 정상)이라 명시한다.
나머지 신호(NodeReady·NDRFreshness·DriverHeartbeat·DevicePlugin·Advertisement·RepeatedFailure·
DeviceDriver)는 실패 시 `internal/health/evaluate.go:81-150`·`device.go` 에서 비-Healthy 로
조기 반환된다 — 그래서 Healthy 노드면 이 신호들은 전부 정상이어야 한다. `DeviceObservation`
실패는 관측 표에 `(info)` 로 그대로 남겨 운영자가 볼 수 있게 한다.

- `verdict=PASS` 는 관측 표면이 읽히고(`DEMO_DONE`) 위 검사가 모두 통과한 경우다. 이때만 종료코드 0.
- 검사가 하나라도 실패하면 `verdict=FAIL`, 종료코드 1.
- CRD 미서빙·접근 불가면 `verdict=BLOCKED`, 종료코드 1.
- `DRY_RUN=1` 이면 `verdict=DRY`, 종료코드 0(명령만 출력).

`degraded` 가 0보다 커도 그 자체로 FAIL 이 아니다 — 저하 관측은 이 데모의 목적이지 실패가
아니다. FAIL 은 관측 표면이 안 읽히거나 게이트 불변식이 깨졌을 때다.

## 이 데모가 증명하지 않는 것

- 실제 장애 주입과 그에 따른 복구 작업 생성·격리 전이. 파괴적이라 범위 밖이다(`BLOCKED
  (scope: fault injection)` 로 남긴다). 그 경로의 라이브 판정은 envtest·장애 주입 하네스에 있다.
- 복구 작업이 정확히 1건인지, 반복 실패 시 격리되는지 — 위 evidence 와 하네스가 고정한다.
