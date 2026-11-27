<!-- README.md: managementMode=external 라이브 데모(run.sh) 실행법과 판정 기준 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# managementMode=external 라이브 데모

## 왜

한 벤더의 `managementMode` 를 `external` 로 두면 kcloud 는 그 벤더의 device-plugin/DRA/exporter 를
배포하지도 삭제하지도 않고, detector 와 `kcloud.ai/*` 라벨, NodeDeviceReport, 건강·evidence 관측만
유지한다. 이 디렉터리의 `run.sh` 는 그 동작을 라이브에서 확인한다. 벤더를 external 로 돌린 뒤
device-plugin DaemonSet 이 없고, present 라벨이 유지되며, NPUClusterPolicy 의 Ready 사유가
`ExternalManaged` 인지, 같은 벤더의 AcceleratorDescriptor 가 중재에서 거절되는지 확인하고, 끝에
원래 상태로 되돌린다.

운영 문서는 관리 모드 안내에 있다.

## 무엇을 바꾸고 되돌리는가

- 대상 NPUClusterPolicy 의 `spec.<vendor>.enabled` 와 `spec.<vendor>.managementMode` 필드만 패치한다.
- 자체 AcceleratorDescriptor(`ext-mode-<vendor>-refusal-probe`) 하나를 만들고, 거절을 확인한 뒤 지운다.
- 외부 워크로드와 다른 리소스에는 손대지 않는다.
- 데모가 external 로 전환하는 동안 그 벤더의 device-plugin 이 잠시 내려갔다가 복원된다. 그래서 그
  벤더 장치를 쓰는 워크로드가 없는 데모/실험 클러스터에서 실행한다.
- `cleanup` 은 성공·실패와 무관하게 항상 `managementMode` 를 제거하고 `enabled` 를 원값으로 되돌린다
  (`trap ... EXIT`).

## 전제

- operator 빌드에 tenstorrent·rebellions 의 toggle-off(비활성화 시 소유 device-plugin 삭제) 경로가
  있어야 한다. 이 경로가 없는 빌드에서는 데모 1단계(`ds_deleted_on_disable`)가 실패한다. nvidia 는
  기존부터 toggle-off 삭제 경로가 있다.
- 대상 벤더 장치가 관측 노드에 실재하고, device-plugin 이 떠 있는 상태에서 시작한다.

## 강제 종료 시 수동 복원

`cleanup` 은 `trap ... EXIT` 로 항상 복원하지만, 스크립트가 강제 종료(예: SIGKILL)되면 복원이
실행되지 않는다. 그때는 아래로 수동 복원한다.

```bash
# tenstorrent
kubectl -n kcloud patch npuclusterpolicy npuclusterpolicy-sample \
  --type=merge -p '{"spec":{"tenstorrent":{"enabled":true,"managementMode":null}}}'

# nvidia
kubectl -n kcloud patch npuclusterpolicy npuclusterpolicy-sample \
  --type=merge -p '{"spec":{"nvidia":{"enabled":true,"managementMode":null}}}'
```

## 실행

```bash
# 기본 프로파일: tenstorrent (<tenstorrent-node>)
bash test/live/external-mode/run.sh

# nvidia 프로파일 (<nvidia-node>)
VENDOR=nvidia bash test/live/external-mode/run.sh
```

프로파일과 대기 상한은 환경변수로 덮는다.

| 변수 | tenstorrent 기본 | nvidia 기본 | 뜻 |
|---|---|---|---|
| `VENDOR` | `tenstorrent` | `nvidia` | 대상 벤더 프로파일 |
| `NAMESPACE` | `kcloud` | `kcloud` | NPUClusterPolicy 네임스페이스 |
| `NCP` | `npuclusterpolicy-sample` | `npuclusterpolicy-sample` | NPUClusterPolicy 이름 |
| `DS_NS` | `kcloud` | `kube-system` | device-plugin DaemonSet 네임스페이스 |
| `DS_NAME` | `kcloud-tt-device-plugin` | `nvidia-device-plugin-flat` | device-plugin DaemonSet 이름 |
| `NODE` | `<tenstorrent-node>` | `<nvidia-node>` | 관측 노드 |
| `RESOURCE` | `tenstorrent.com/blackhole` | `nvidia.com/gpu` | allocatable 리소스 이름 |
| `T_DELETE` `T_EXTERNAL` `T_DESC` `T_RESTORE` `IV` | 240 / 120 / 90 / 300 / 10 | 좌동 | 단계별 대기 상한·간격(초) |

## 단계

1. **preflight** — 대상 벤더의 현재 `enabled`·`managementMode` 를 저장하고 시작 상태를 기록한다.
   이미 external 이거나 NPUClusterPolicy 가 없으면 `BLOCKED` 로 끝낸다.
2. **demo** — 세 단계로 확인한다.
   - `enabled=false` 로 패치해 소유 DaemonSet 이 회수되는지 본다(토글 off 삭제 경로).
   - `enabled=true` + `managementMode=external` 로 패치한 뒤 DaemonSet 부재, present 라벨 유지,
     Ready 사유 `ExternalManaged` 를 확인한다.
   - 같은 벤더 descriptor 를 적용해 `status.refusals` 사유에 `NPUClusterPolicy 가 관리 중` 이
     들어가는지 확인하고 지운다.
3. **cleanup** — `managementMode` 를 제거하고 `enabled` 를 원값으로 되돌린 뒤, DaemonSet 이 다시
   Ready 이고 리소스 allocatable 이 돌아오는지 확인한다. 항상 실행한다.

## 판정

스크립트는 마지막에 JSON 요약 한 줄을 낸다.

```json
{"test":"external-mode","vendor":"tenstorrent","node":"<tenstorrent-node>","ns":"kcloud","ncp":"npuclusterpolicy-sample","phase":"cleanup","checks":{"ds_deleted_on_disable":true,"ds_absent_while_external":true,"present_label_kept":true,"ready_reason_externalmanaged":true,"descriptor_refused":true,"restored_ds_ready":true,"restored_allocatable":true},"fails":0,"verdict":"PASS","date":"..."}
```

- `verdict=PASS` 는 demo 가 끝까지 실행됐고(`DEMO_DONE=true`) 모든 검사가 통과한 경우다. 이때만 종료코드 0.
- 검사가 하나라도 실패하거나 demo 가 중간에 중단되면 `verdict=FAIL`, 종료코드 1. `set -e` 로 demo
  중간 명령이 실패하면 `DEMO_DONE` 이 서지 않아 남은 검사가 없어도 PASS 로 새지 않는다.
- 시작 조건이 안 맞으면 `verdict=BLOCKED`, 종료코드 1.
- 요약은 `$OUT`(기본 `/tmp/external-mode-evidence/`)에도 남는다.

## 이 데모가 증명하지 않는 것

이 데모는 kcloud 가 external 벤더의 배포물을 만들지 않고 관측·라벨만 유지하는지, descriptor 중재가
external 을 관리 중으로 보는지, 전환 뒤 복원이 되는지를 확인한다. 다음은 확인하지 않는다.

- external 벤더의 실제 외부 device-plugin(다른 operator 가 배포한 것)이 장치를 정상 광고·할당하는지.
- external 벤더의 드라이버 버전 드리프트 관측과 그에 대한 조치.
- DriverInstallPolicy 동작(nvidia-toolkit 배포 등). external 은 DriverInstallPolicy 에 영향을 주지
  않는다.
- nvidia 프로파일은 `run.sh` 로 자동 확인하지 않는다 — external 전환 동안 기존 device-plugin
  DaemonSet 의 UID 가 바뀌지 않는지(kcloud 가 삭제·재생성하지 않는다는 뜻)는 수동으로 확인한다.
