<!-- README.md: <nvidia-node> NVIDIA 잔재 패키지 제거 하네스 사용법 | 생성일: 2026-09-28 | 수정일: 2026-10-11 -->
# <nvidia-node> NVIDIA 잔재 패키지 제거

## 왜

<nvidia-node> 에 남은 세 패키지는 실행 중 드라이버와 무관해 해롭지 않다고 판단됐고, 제거는
재부팅 예산이 있는 별도 정비로 미뤄 둔 상태였다. 이번은 그 별도 정비를 사용자가 직접
지시한 실행이다 — 판단을 뒤집는 것이 아니라, 판단 뒤 미뤄둔 제거를 이제 한다.

세 패키지는 모두 실행 중 드라이버(580.126.09)와 무관하다:

| 패키지 | 설치된 버전 | 실행 중 버전과의 관계 |
|---|---|---|
| `nvidia-firmware-580-580.178.04` | 580.178.04 | 계열은 같으나 다른(예전) 버전 — 실행 중인 `nvidia-firmware-580-580.126.09` 와 별개로 설치돼 있다 |
| `nvidia-firmware-595-595.91.07` | 595.91.07 | 아예 다른 major(595) |
| `nvidia-kernel-source-580` | 580.178.04 | 패키지명은 "580" 계열이지만 설치판이 실행 중 버전(580.126.09)이 아니라 578.04 |

## 허용 조치(닫힌 목록) — 이 밖은 컨트롤러 승인 없이 하지 않는다

1. 읽기: `dpkg -l`, `apt-get -s remove …`(시뮬레이션), `cat /sys/module/nvidia/version`,
   `nvidia-smi -L`, `dkms status`, `kubectl get`(NodeDeviceReport·AcceleratorHealth·
   DriverUpgradeState·파드·allocatable).
2. 쓰기: `sudo apt-get remove -y nvidia-firmware-580-580.178.04
   nvidia-firmware-595-595.91.07 nvidia-kernel-source-580` **정확히 이 세 이름**.
   `autoremove`·`purge`·다른 패키지 제거·드라이버 재설치는 하지 않는다.
3. 복구 수단(필요할 때만): 관리 노드(<nvidia-node> 포함) 무승인 재부팅 1회. 그 밖의
   드라이버 재설치·`apt install`은 하지 않는다 — 사후 게이트가 실패하면 멈추고
   컨트롤러에 보고한다(이 하네스가 스스로 재부팅하거나 재설치하지 않는다).
4. 노드 대상은 `kubectl get node <nvidia-node> -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}'`
   로만 구한다. 시범서비스 배포 노드는 접촉 경로 자체가 없다.

## 안전 게이트

1. **`nvidia.com/gpu` 를 requests 나 limits 에 담은 파드가 <nvidia-node> 에 0 개**여야
   한다(uid 중복 제거). 0 이 아니면 `safety_gate` 가 그 목록을 stderr 에 찍고 중단한다.
   `do_remove`·`precheck`·`quiesce`·`main` 이 이 게이트를 통과 뒤에만 진행한다.
2. **시뮬레이션 게이트**: `sudo apt-get -s remove <세 이름>` 출력의 `Remv ` 줄이 정확히
   3개이고, 그 이름이 위 세 패키지와 정확히 같아야 한다. 실행 중 버전과 짝이 맞는
   패키지(`libnvidia-cfg1-580`·`libnvidia-compute-580`·`nvidia-firmware-580-580.126.09`·
   `nvidia-kernel-common-580`) 중 하나라도 제거 목록에 나타나면 별도로 다시 중단한다
   (2차 방어 — 1차 검사가 이미 이를 걸러내지만 명시적으로 한 번 더 본다).
3. **사후 게이트**(`wait-postchecks`, 최대 `POSTCHECK_TIMEOUT`초 폴링, 기본 300 = 5분):
   여섯 조건이 전부 맞아야 통과다 — 로드된 모듈 버전이 여전히 `580.126.09`, `nvidia-smi -L`
   이 A30·A2 두 장치를 보임, `NodeDeviceReport` 의 nvidia 항목이 모두 `driverLoaded=true`,
   `AcceleratorHealth` 가 `Healthy`, `DriverUpgradeState <nvidia-node>-nvidia` 가 `Idle`,
   `allocatable nvidia.com/gpu`=2. 하나라도 시간 안에 맞지 않으면 마지막 관측값을 기록하고
   멈춘다 — 이 하네스는 그 경우에도 재부팅이나 재설치를 스스로 하지 않는다.

## 사용법

```bash
# 미리보기(안전 게이트·사전 상태·시뮬레이션은 실제로 돈다, 제거 명령만 출력)
DRY_RUN=1 bash test/live/nvidia-residue-packages/run.sh

# 실행(전체 절차: safety-gate -> precheck -> simulate -> remove -> postcheck -> wait-postchecks)
bash test/live/nvidia-residue-packages/run.sh

# 단계별 실행(PHASE)
PHASE=safety-gate     bash test/live/nvidia-residue-packages/run.sh
PHASE=precheck        bash test/live/nvidia-residue-packages/run.sh
PHASE=simulate        bash test/live/nvidia-residue-packages/run.sh
PHASE=remove          bash test/live/nvidia-residue-packages/run.sh
PHASE=postcheck       bash test/live/nvidia-residue-packages/run.sh
PHASE=wait-postchecks bash test/live/nvidia-residue-packages/run.sh
PHASE=quiesce         bash test/live/nvidia-residue-packages/run.sh   # 선택 — cordon 만, 기본 흐름엔 없음
PHASE=unquiesce       bash test/live/nvidia-residue-packages/run.sh

# 판정(오프라인, 클러스터 미접속)
RUN_DIR=test/live/nvidia-residue-packages/runs/<RUN_ID>-nvidia PHASE=verdict bash test/live/nvidia-residue-packages/run.sh
# -> RESIDUE_REMOVED: removed=<0..3|missing> running=<버전|missing> nvidia_smi=<ok|fail> gpu=<개수|missing> dus=<상태|missing>
```

인자를 받지 않는다 — 전부 환경변수로 지정한다. `RUN_ID`(기본 UTC 타임스탬프)·
`POSTCHECK_TIMEOUT`(기본 300)·`EXPECTED_VERSION`(기본 580.126.09)·`NODE`(기본
<nvidia-node>)는 환경변수로 덮을 수 있다. 제거 대상 세 패키지 이름은 환경변수로 덮지
않는다 — 그 목록 자체가 게이트의 기준이다.

**`PHASE=quiesce` 를 단독으로 돌렸으면 반드시 `PHASE=unquiesce` 로 되돌린다.** 정상
종료(rc 0)에서는 `cleanup` 이 uncordon 하지 않는다.

## 남기는 것

매 실행은 `runs/<RUN_ID>-nvidia/`(git-ignored) 에 다음을 남긴다.

| 파일 | 내용 |
|---|---|
| `evidence.txt` | 안전 게이트부터 wait-postchecks 까지 각 단계의 원문 출력을 구분선과 함께 순서대로 누적한 것 |
| `pre-dpkg.txt` / `post-dpkg.txt` | 제거 전/후 nvidia 관련 `dpkg -l` 원문(`verdict` 의 `removed` 판정 근거) |
| `pre-module-version.txt` | 제거 전 로드된 모듈 버전 |
| `pre-nvidia-smi.txt` | 제거 전 `nvidia-smi -L` 원문 |
| `pre-dus.txt` / `pre-allocatable.txt` | 제거 전 DUS 상태 / allocatable 값(참고용, verdict 는 안 읽는다) |
| `simulate-output.txt` | `apt-get -s remove` 원문(게이트 판단 근거) |
| `remove-exit.txt` / `remove-ssh-lost.txt` | 제거 명령 종료 코드 또는 SSH 유실 표시 |
| `final-module-version.txt` / `final-nvidia-smi.txt` / `final-allocatable.txt` / `final-dus.txt` | `wait-postchecks` 가 폴링을 멈춘 시점(통과했든 시간 초과했든)의 마지막 관측값 — `PHASE=verdict` 가 이 파일들만 읽는다 |

`DRY_RUN=1` 에서는 `remove`·`postcheck`·`wait-postchecks` 가 실제로 아무것도 바꾸지
않으므로 `post-*`·`final-*` 파일이 생기지 않는다 — 그 상태로 `PHASE=verdict` 를 돌리면
`removed=missing running=missing nvidia_smi=fail gpu=missing dus=missing` 이 나온다(정직한
"아직 없음"이지 실패가 아니다).

## 이 하네스가 하지 않는 것

- 세 이름 밖의 어떤 패키지도 제거하지 않는다(`autoremove`·`purge` 포함).
- 드라이버 재설치·업그레이드·다운그레이드를 하지 않는다.
- 사후 게이트 실패 시 스스로 재부팅하거나 재설치하지 않는다 — 기록하고 멈춘다. 복구가
  필요하면 컨트롤러 승인 뒤 사람이 판단한다.
- device-plugin 파드를 변경하지 않는다(`quiesce` 는 cordon 만 한다) — 이 세 패키지는
  로드된 모듈이나 device-plugin 이 여는 `/dev/nvidia*` 핸들과 무관하다.
- 반복 측정. 한 번의 제거만 실행한다.
