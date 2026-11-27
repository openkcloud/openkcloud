<!-- README.md: "새 벤더 = device-plugin 코드 0줄" descriptor 라이브 데모(run.sh) 실행법과 판정 기준 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# 새 벤더 = descriptor 하나 (new-vendor-by-descriptor)

## 왜

`AcceleratorDescriptor` 하나로 벤더를 정의하면 벤더별 device-plugin 코드 없이도 그 장치가
광고되고 파드에 할당된다. 이 디렉터리의 `run.sh` 는 그것을 라이브에서 보인다. Tenstorrent 를
NPUClusterPolicy 에서 끄고(소유 device-plugin 회수), descriptor 하나만으로 생성된
device-plugin 이 `tenstorrent/blackhole-p150` 을 광고하며, 소비자 파드가 `/dev/tenstorrent`
주입을 받는 것을 확인한 뒤 원래대로 되돌린다.

이 데모는 공유(replicas)를 빼고 단일 replica 로 "새 벤더 정의" 만 보인다.

## 무엇을 바꾸고 되돌리는가

- 대상 NPUClusterPolicy 의 `spec.tenstorrent.enabled` 필드만 패치한다(false → 원값).
- 자체 `AcceleratorDescriptor`(`new-vendor-tt-blackhole`)와 소비자 파드
  (`default/new-vendor-tt-consumer`) 하나씩을 만들고, 확인 뒤 지운다.
- 다른 워크로드·리소스에는 손대지 않는다.
- 전환 동안 tenstorrent device-plugin(`kcloud/kcloud-tt-device-plugin`)이 잠시 내려갔다가
  복원된다. 그래서 tenstorrent 장치를 쓰는 워크로드가 없는 데모/실험 클러스터(<tenstorrent-node>)에서
  실행한다.
- `cleanup` 은 성공·실패와 무관하게 항상 소비자·descriptor 를 지우고 tenstorrent 를 다시 켠다
  (`trap ... EXIT`).

## 전제

- operator 빌드에 tenstorrent 의 toggle-off(비활성화 시 소유 device-plugin 삭제) 경로가 있어야
  한다. 이 경로가 없는 빌드에서는 `ncp_ds_deleted_on_disable` 검사가 실패한다.
- descriptor auto-apply 가 켜져 있어야 한다. tenstorrent 를 끄면 벤더가 비관리 상태가 되어
  descriptor 가 중재를 통과하고 생성 device-plugin 이 뜬다. tenstorrent 가 켜져 있거나 external
  이면 descriptor 는 이중 광고 방지로 거절되므로, 스크립트는 그 경우 `BLOCKED` 로 끝낸다.
- <tenstorrent-node> 에 tenstorrent 장치가 실재하고 device-plugin 이 떠 있는 상태에서 시작한다.

## 강제 종료 시 수동 복원

`cleanup` 은 `trap ... EXIT` 로 항상 복원하지만, 스크립트가 강제 종료(예: SIGKILL)되면 복원이
실행되지 않는다. 그때는 아래로 수동 복원한다.

```bash
kubectl -n default delete pod new-vendor-tt-consumer --ignore-not-found
kubectl delete acceleratordescriptor new-vendor-tt-blackhole --ignore-not-found
kubectl -n kcloud patch npuclusterpolicy npuclusterpolicy-sample \
  --type=merge -p '{"spec":{"tenstorrent":{"enabled":true}}}'
```

## 실행

```bash
bash test/live/new-vendor-by-descriptor/run.sh

# 명령만 미리 보기(클러스터 접근 없음)
DRY_RUN=1 bash test/live/new-vendor-by-descriptor/run.sh
```

| 변수 | 기본 | 뜻 |
|---|---|---|
| `NAMESPACE` | `kcloud` | NPUClusterPolicy·생성물 네임스페이스 |
| `NCP` | `npuclusterpolicy-sample` | NPUClusterPolicy 이름 |
| `NODE` | `<tenstorrent-node>` | 관측 노드 |
| `NCP_DS` | `kcloud-tt-device-plugin` | NPUClusterPolicy 가 관리하는 벤더 device-plugin |
| `DESC_FILE` | `descriptor.yaml` | 적용할 descriptor(이름은 파일에서 읽음) |
| `CONSUMER_FILE` | `consumer.yaml` | 소비자 파드(이름은 파일에서 읽음) |
| `RESOURCE_DESC` | `tenstorrent/blackhole-p150` | descriptor 가 광고하는 리소스(vendor/product) |
| `RESOURCE_VENDOR` | `tenstorrent.com/blackhole` | NPUClusterPolicy 벤더 광고 |
| `T_DS_DELETE` `T_GEN_UP` `T_CONSUMER` `T_GC` `T_RESTORE` `IV` | 240 / 180 / 120 / 180 / 300 / 10 | 단계별 대기 상한·간격(초) |
| `DRY_RUN` | `0` | `1` 이면 실행할 명령만 출력, 클러스터 접근 없음 |

생성물 device-plugin DaemonSet 이름은 descriptor 이름에서 나온다:
`kcloud-generated-<descriptor 이름>-dp`.

## 단계

1. **preflight** — tenstorrent 의 현재 `enabled` 를 저장하고 시작 상태를 기록한다. NPUClusterPolicy
   가 없거나, tenstorrent 가 이미 꺼져 있거나 external 이면 `BLOCKED` 로 끝낸다.
2. **demo** — 세 단계로 확인한다.
   - `tenstorrent.enabled=false` 로 패치해 소유 device-plugin 이 회수되는지 본다.
   - descriptor 를 적용해 생성 device-plugin 이 뜨고 `tenstorrent/blackhole-p150` 이 광고되는지 본다.
   - 소비자 파드가 스케줄되고 로그에 `/dev/tenstorrent` 주입이 보이는지 확인한다.
3. **cleanup** — 소비자·descriptor 를 지우고 생성물이 GC 되는지 본 뒤, tenstorrent 를 다시 켜
   원래 device-plugin 이 Ready 이고 `tenstorrent.com/blackhole` 광고가 돌아오는지 확인한다. 항상 실행한다.

## 판정

스크립트는 마지막에 JSON 요약 한 줄을 낸다.

```json
{"test":"new-vendor-by-descriptor","vendor":"tenstorrent","node":"<tenstorrent-node>","descriptor":"new-vendor-tt-blackhole","resource":"tenstorrent/blackhole-p150","scenario_minutes":6,"scenario_destructive":"no","phase":"cleanup","checks":{"ncp_ds_deleted_on_disable":true,"generated_dp_created":true,"descriptor_resource_advertised":true,"consumer_scheduled":true,"consumer_saw_device":true,"generated_dp_gc":true,"restored_vendor_dp":true},"fails":0,"verdict":"PASS","dryRun":false,"date":"..."}
```

검사 항목:

- `ncp_ds_deleted_on_disable` — tenstorrent 를 끄자 소유 device-plugin 이 삭제됐다.
- `generated_dp_created` — descriptor 로 생성 device-plugin DaemonSet 이 Ready 가 됐다.
- `descriptor_resource_advertised` — 노드가 `tenstorrent/blackhole-p150` 을 광고한다(0 아님).
- `consumer_scheduled` — 소비자 파드가 Running/Succeeded 다.
- `consumer_saw_device` — 파드 로그에 `/dev/tenstorrent` 주입이 보인다(`No such file` 없음).
- `generated_dp_gc` — descriptor 삭제 후 생성물이 회수됐다.
- `restored_vendor_dp` — tenstorrent 재활성 후 원래 device-plugin 이 Ready 이고 벤더 광고가 돌아왔다.

- `verdict=PASS` 는 demo 가 끝까지 실행됐고(`DEMO_DONE`) 모든 검사가 통과한 경우다. 이때만 종료코드 0.
- 검사가 하나라도 실패하거나 demo 가 중간에 중단되면 `verdict=FAIL`, 종료코드 1.
- 시작 조건이 안 맞으면 `verdict=BLOCKED`, 종료코드 1.
- `DRY_RUN=1` 이면 `verdict=DRY`, 종료코드 0(명령만 출력).

## 이 데모가 증명하지 않는 것

- 공유(SW 시분할, replicas) — 여기서는 단일 replica 로 "새 벤더 정의" 만 본다.
- DRA backend 경로 — 이 데모는 `backends: [devicePlugin]` 만 쓴다.
- 실제 새 벤더(operator 가 모르는 벤더)의 드라이버 설치 — descriptor 는 광고·할당 경로를 다루고,
  드라이버는 별도다.
