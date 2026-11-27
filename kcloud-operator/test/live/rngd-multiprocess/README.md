<!-- README.md: RNGD 다중 프로세스 공유 가능성 실측 하네스 | 생성일: 2026-07-05 | 수정일: 2026-10-11 -->

# RNGD 다중 프로세스 실측

## 왜
RNGD 공유(time-slicing 유사 UX)의 가능 여부는 **런타임이 한 장치/파티션에 다중 프로세스 context 를
허용하는가**로 나뉜다. furiosa device-plugin 에는 replica 옵션이 없으므로(furiosa-device-plugin·
libfuriosa-kubernetes 소스 확인), 공유를 제공하려면
(a) 다중 프로세스가 되면 operator 가 replica 광고를 추가하거나, (b) 안 되면 Broker 가 필요하다.

## 방법(호스트 레벨)
컨테이너 이미지(`furiosa-smi`)로 파드에서 관찰하는 방법은 Harbor 에 그 이미지가 없어(카탈로그
확인, air-gap 이라 대체 불가) 쓸 수 없다.
대신 **컨테이너 없이 <rngd-node> 을 SSH 로 직접 접속**해, PE 캐릭터 디바이스(`/dev/rngd/npu0peN`,
world-rw)를 두 프로세스가 동시에 `open()` 할 수 있는지로 판정한다(`probe.sh`):

1. baseline 기록(`pe_occupancy`, `furiosa-smi ps`).
2. 프로세스 A 가 `npu0pe0` 을 열고 N 초 보유.
3. A 가 보유한 동안 프로세스 B 가 **같은** `npu0pe0` 을 여는 시도 — 이게 실측 대상.
4. 대조군: B 가 **다른** PE(`npu0pe1`) 를 여는 시도 — 계측기가 유효하려면 이건 성공해야 한다
   (안 그러면 "동시 접근 배타적"이 아니라 "장치가 아예 고장"인 걸 오인할 수 있음).
5. 정리 후 baseline 복원 확인.

컨테이너 기반 2단계 측정(동시 추론 throughput/P99/fault 전파)은 여전히 이미지가 필요하며 아래
"2단계 측정" 절에 남겨둔다.

## 전제
- 노드 `<rngd-node>`(<rngd-node>), SSH 접근(`NODE_HOST`, 기본 `<user>@<rngd-node>`)
- PE 디바이스 노드가 world-rw 로 노출돼 있어야 함(실제 권한 `crw-rw-rw-`)
- 다른 테넌트가 해당 PE 를 쓰고 있지 않아야 함(`pe_occupancy` 전부 0, idle 이면 충족)

## 실행
```bash
NODE_HOST=<user>@<rngd-node> HOLD_SECONDS=20 bash test/live/rngd-multiprocess/probe.sh 2>&1 | tee /tmp/rngd-probe.log
```

## 판정 기준
| B(동일 PE) | C(대조군, 다른 PE) | 의미 | 다음 행동 |
|---|---|---|---|
| 성공 | 성공 | 동시 접근 허용 후보 | 2단계 측정(동시 추론 throughput/P99/OOM 전파) 후 `KCLOUD_RNGD_MULTIPROCESS=verified` |
| 실패 | 성공 | 동시 접근 배타적(계측기 유효, 대조군이 이를 증명) | capability 는 `supported:false` 유지(`unsupported`, verified). Broker 는 후속 계획(별도 설계) |
| 실패 | 실패 | **계측기 무효** — 장치 자체가 이 open() 방식으로 접근 불가한 상태(고장/드라이버 fault 등), 동시성과 무관 | `VerificationRequired` 유지. 원인 규명(드라이버/FW 상태) 후 재실행 |
| 성공 | 실패 | 있을 수 없는 조합(대조군이 더 어려운 케이스) — 계측 자체를 의심 | 재실행, 로그 재확인 |

## 2단계 측정(1단계에서 "동시 접근 허용 후보"가 나왔을 때만, 컨테이너 이미지 필요)
1. 두 pod 에서 동일 모델 추론 루프 60초 — 각각 throughput 기록
2. 단독 실행 대비 저하율, P95/P99 latency
3. 한 pod 강제 종료(`kubectl delete pod --grace-period=0`) 후 다른 pod 생존 여부 → fault isolation 등급 근거
4. 결과를 기록하고, 이 결과가 있을 때만
   `SharingCapability.MultiProcess.Verification=verified` 로 승격한다.
   (이 단계는 `furiosa-smi` 를 담은 이미지를 Harbor 에 올려야 실행 가능 — 아직 없음.)

## 결과 기록 양식
| 날짜 | 노드 | 방법 | B(동일 PE) | C(대조군) | 판정 |
|---|---|---|---|---|---|
| <날짜> | <rngd-node> | host-level open() (`probe.sh`, `HOLD_SECONDS=15`) | `OPEN_FAIL errno=2` | `OPEN_FAIL errno=2` | **계측기 무효** — A 자신의(동시성 없는 단독) open 도 `A_OPEN_FAIL errno=2` 로 실패. `dmesg -T`: 오픈 시각과 정확히 일치하는 `furiosa_rngd 0000:27:00.0: admin cmd failed. FW returns err(-2)` / `NPU0 (E) [_npu_init:122] PEn. npu init command failed: -2` (PE0~7 전부). root 로도 동일 실패(권한 문제 아님). 동시성과 무관한 드라이버/FW 레벨 admin-cmd 실패로 판단 — `MultiProcess` capability 는 `VerificationRequired` 로 유지, env 미설정. |
