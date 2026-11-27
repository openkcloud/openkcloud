<!-- README.md: 벤더 장치 리셋 실측 하네스 사용법 | 생성일: 2026-09-28 | 수정일: 2026-10-11 -->
# 벤더 장치 리셋 실측

## 왜

`internal/vendorcap/registry.go` 의 `CapReset` 등급은 이 하네스 실측의 결과다 — nvidia 는
`Native`(확인 수준 `hardware`), tenstorrent 는 `Unsupported`다(리셋 명령 실행이 노드 급정지를
유발해 `investigate-pcie.sh` 로 원인을 좁혔다). 이 하네스는 실제 노드에서 벤더 리셋 명령을
실행해 그 확인 수준의 근거를 남긴다.

대상은 벤더가 장치 리셋 명령을 노출하는 두 벤더뿐이다 — nvidia(`nvidia-smi -r`)·tenstorrent(`tt-smi -r`).
furiosa(warboy·rngd)·rebellions 는 공개 인터페이스에 리셋 수단 자체가 없어 다루지 않는다.

| 벤더 | 노드 | 대상 장치 | 리셋 명령 |
|---|---|---|---|
| nvidia | <nvidia-node> | A30(`0000:18:00.0`), 기본 TARGET | `nvidia-smi -i <UUID> -r` |
| tenstorrent | <tenstorrent-node> | Blackhole p150(`0000:af:00.0`) | `tt-smi -r <UMD chip ID>` |

## 안전 조건 (하네스가 모든 쓰기 앞에 스스로 확인한다)

1. **그 노드에서 그 벤더 확장 자원(`nvidia.com/gpu`·`tenstorrent.com/blackhole`)을
   requests/limits 에 담은 파드가 0 개**여야 한다. 0 이 아니면 `safety_gate` 가 그 파드
   목록을 stderr 에 찍고 중단한다. 이 게이트를 실행 직전 다시 통과하는 함수는 정확히
   `do_reset`(PHASE=reset)·`quiesce`·`reboot_recover`·`precheck`·`main`·`workload_check`
   여섯이다.
   `unquiesce` 는 게이트를 걸지 않는다 — 되돌리기(uncordon)를 "장치를 쓰는 파드가
   생겼다"는 이유로 막으면 cordon 이 안 풀린 채 남기 때문이다.
2. device-plugin DaemonSet 파드(`kube-system/nvidia-device-plugin-flat`,
   `kcloud/kcloud-tt-device-plugin`)는 확장 자원을 요구하지 않지만 장치를 열고 있을 수
   있다. `reset` 이 "장치 사용 중"으로 실패하면 그때만 `PHASE=quiesce`(cordon + 그
   device-plugin 파드 삭제)를 판단해서 쓴다 — 미리 하지 않는다. **허용된 조치는 이
   cordon + 해당 DS 파드 삭제뿐이다.** 그 밖의 조치(프로세스 kill, 커스텀 taint,
   operator 상태 라벨 재사용 등)는 이 하네스에 없고, 다시 쓰려면 컨트롤러 승인이
   먼저 필요하다 — 실제로 세 건 있었다.
   **한계**: DaemonSet 파드는 `node.kubernetes.io/unschedulable` 기본 toleration이 있어
   cordon 만으로는 재스케줄을 막지 못한다. `quiesce` 의 실제 효과는 파드 삭제가 만드는
   짧은 창뿐이다 — 이 창 안에 리셋이 끝나지 않으면 재시도가 필요할 수 있다.
3. 리셋 뒤 장치가 정해진 시간 안에 돌아오지 않으면 `PHASE=reboot-recover` 로 그 노드를
   재부팅해 복구한다. 관리 노드(<nvidia-node>·<tenstorrent-node> 포함)는 재부팅에 사용자
   확인이 필요 없다 — 재부팅이 필요했다는 사실 자체를 결과로 기록한다(숨기지 않는다).
4. 한 번에 한 벤더·한 노드. 클러스터 노드 목록(`kubectl get nodes` 의 InternalIP) 밖의
   어떤 호스트에도 접촉하지 않는다(시범서비스 배포 노드 포함) — 이 하네스가 접속하는
   노드는 `kubectl get node <NODE> -o jsonpath=...` 로 클러스터에 실재하는 노드에서만
   IP 를 구하므로 그 목록 밖의 주소로 향할 경로 자체가 없다.
5. `PHASE=bmc-sel`(ipmitool 설치+BMC SEL 조회)은 **<tenstorrent-node> 에서만** 허용한다 —
   `NODE` 가 그 밖이면(예: `VENDOR=nvidia` 로 호출) 아무것도
   하지 않고 거절한다. 실행하는 ipmitool 명령은 `sel elist`·`sel info`·`mc info`·
   `chassis status` 읽기 4종뿐이다 — `power`·`chassis power`·`sel clear` 같은 쓰기 명령은
   없다. BMC 는 그 노드 안의 `/dev/ipmi0` 로만 접근한다 — 네트워크로 BMC 에 접속하지
   않는다.

## 성공 판정 (넷 중 하나라도 어긋나면 "성공"이 아니다)

1. 리셋 명령이 exit 0.
2. 리셋 뒤에도 같은 장치 목록·같은 PCI 주소가 보인다(`wait-recover` 가 리셋 전 장치
   개수와 같아질 때까지 최대 `RECOVER_TIMEOUT`초 폴링, 기본 300).
3. `NodeDeviceReport` 가 리셋 전과 같은 장치 수를 보이고 `AcceleratorHealth` 가
   `Healthy` 로 돌아온다(`wait-advertise` 가 최대 `ADVERTISE_TIMEOUT`초 폴링, 기본 300 —
   5분 상한).
4. `DriverUpgradeState` 가 대기 내내 `Idle` 그대로다 — 폴링 중 한 번이라도 `Idle` 이
   아니면 `wait-advertise` 가 실패로 끝낸다.

## 사용법

```bash
# 미리보기(안전 게이트·장치 목록·dmesg 는 실제로 읽는다, 리셋·cordon·재부팅·시험 파드는 출력만)
DRY_RUN=1 VENDOR=nvidia bash test/live/device-reset/run.sh
DRY_RUN=1 VENDOR=tenstorrent bash test/live/device-reset/run.sh

# 실행(전체 절차: safety-gate -> precheck -> reset -> wait-recover -> postcheck ->
#  wait-advertise -> workload-check)
VENDOR=nvidia bash test/live/device-reset/run.sh
VENDOR=tenstorrent bash test/live/device-reset/run.sh

# 단계별 실행(PHASE) — quiesce/unquiesce/reboot-recover 는 all 에 없다, 필요할 때만 수동으로
PHASE=safety-gate  VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=precheck     VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=reset        VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=wait-recover VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=postcheck    VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=wait-advertise VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=workload-check VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=quiesce      VENDOR=nvidia bash test/live/device-reset/run.sh   # reset 이 busy 로 막힐 때만
PHASE=unquiesce    VENDOR=nvidia bash test/live/device-reset/run.sh
PHASE=reboot-recover VENDOR=nvidia bash test/live/device-reset/run.sh # 장치가 안 돌아올 때만

# BMC SEL(<tenstorrent-node> 전용) — SEL_STAGE=before|after(기본 before).
# 같은 RUN_ID 로 before 뒤 after 를 부르면 sel-delta.txt(신규 SEL 항목)까지 만든다.
SEL_STAGE=before RUN_ID=<고정> VENDOR=tenstorrent PHASE=bmc-sel bash test/live/device-reset/run.sh
SEL_STAGE=after  RUN_ID=<위와 같은 값> VENDOR=tenstorrent PHASE=bmc-sel bash test/live/device-reset/run.sh

# 판정(오프라인, 클러스터 미접속 — VENDOR 도 필요 없다)
RUN_DIR=test/live/device-reset/runs/<RUN_ID>-<vendor> PHASE=verdict bash test/live/device-reset/run.sh
# -> RESET_VERDICT: <PASS|FAIL> rc=<n|missing> devices=<same|changed|missing> node=<ready|rebooted|unknown>
# -> RESET_REPRO: rc=<n|ssh-lost|missing> node=<ready|rebooted|unknown> sel_new=<n|missing> last_kernel_line=<yes|no|missing>
#    (sel-delta.txt·dmesg-live.log·reset-start-utc.txt 가 없으면 missing, 지어내지 않는다)

# 시험 훅 — FORCE_FAIL_AT=<phase 이름> 을 주면 그 함수 진입 직후 강제로 실패해 trap
# cleanup 의 동작(라이브 없이)을 확인할 수 있다. DRY_RUN=1 과 함께 쓴다.
DRY_RUN=1 FORCE_FAIL_AT=quiesce VENDOR=nvidia PHASE=quiesce bash test/live/device-reset/run.sh
```

**`PHASE=quiesce` 를 단독으로 돌렸으면 반드시 `PHASE=unquiesce` 로 되돌린다** — 정상
종료(rc 0)에서는 `cleanup` 이 uncordon 하지 않으므로, 잊으면 노드가 cordon 된 채로 남는다.

`VENDOR` 만 필수다(단, `PHASE=verdict` 는 `RUN_DIR` 만 필요하고 `VENDOR` 조차 필요 없다).
`NODE`·`TARGET`·`RESOURCE`·`RECOVER_TIMEOUT`·`ADVERTISE_TIMEOUT`·`RUN_ID` 는 벤더별
기본값이 있고 환경변수로 덮을 수 있다(예: `TARGET=GPU-9e5765e7-583f-e904-a7d2-57880c865213`
로 A2 를 대신 대상). 인자를 받지 않는다 — 전부 환경변수로 지정한다.

## 남기는 것

매 실행은 `runs/<RUN_ID>-<VENDOR>/`(git-ignored) 에 다음을 남긴다.

| 파일 | 내용 |
|---|---|
| `evidence.txt` | 안전 게이트부터 workload-check 까지 각 단계의 원문 출력을 구분선과 함께 순서대로 누적한 것 — `test/collect/evidence.sh --actual-file` 의 입력이 된다 |
| `pre-devices.txt` / `post-devices.txt` | 리셋 전/후 장치 목록 원문 |
| `pre-device-count.txt` | 리셋 전 장치 개수(재열거 판정 기준선) |
| `pre-ndr-count.txt` | 리셋 전 `NodeDeviceReport` 이 벤더 엔트리 수(광고 복구 판정 기준선) |
| `pre-dmesg-lines.txt` | 리셋 전 `dmesg` 총 줄 수(리셋 뒤 새 줄만 추리는 기준) |
| `pre-boot-id.txt` / `post-boot-id.txt` | 리셋 전/후 `/proc/sys/kernel/random/boot_id`(`PHASE=verdict` 의 node 판정 기준) |
| `reset-exit.txt` | 리셋 명령 종료 코드(`PHASE=verdict` 의 rc) |
| `recover-elapsed.txt` / `advertise-elapsed.txt` | 각 대기 단계가 실제로 걸린 시간(초) |
| `reset-start-utc.txt` | `do_reset` 이 리셋 명령을 실제로 보내기 직전 UTC 시각(`PHASE=verdict` 의 `last_kernel_line` 기준시) |
| `dmesg-live.log` / `journal-live.log` | 리셋 직전부터 백그라운드 SSH 로 흘려 받은 원격 `dmesg -w -T`·`journalctl -kf` 실시간 스트림 — SSH 가 끊기면 마지막 줄이 크래시 직전 커널 기록 |
| `sel-before.txt` / `sel-after.txt` | `PHASE=bmc-sel`(<tenstorrent-node> 전용)이 `SEL_STAGE=before|after` 로 남기는 `sudo ipmitool sel elist` 원문 |
| `sel-delta.txt` | `SEL_STAGE=after` 실행 시 `sel-before.txt`가 있으면 만드는 `diff`(신규 SEL 항목은 `>` 로 시작하는 줄) |

`post-devices.txt`·`post-boot-id.txt` 는 `postcheck` 가 남긴다 — `do_reset` 이 실패하면
`main` 이 그 자리에서 멈추므로(이후 단계가 전부 실행되지 않는다) 이 두 파일이 아예
없을 수 있다. `PHASE=verdict` 는 이 경우를 `devices=missing`·`node=unknown` 으로,
지어내지 않고 그대로 낸다.

## 이 하네스가 하지 않는 것

- 펌웨어 플래시(`-r` 은 리셋이지 플래시가 아니다 — 플래시 옵션을 쓰지 않는다).
- 파티션·공유가 켜진 상태에서의 리셋 확인. 이 실측은 파티션·공유 없이 평시 상태에서만
  했다 — 그 상태에서의 리셋 동작은 이 결과가 말해주지 않는다.
- 반복 측정. 한 벤더당 한 번의 리셋만 실행한다(측정이 한 번이라는 것 자체가 한계다).
- 대상 노드 외의 노드에 대한 어떤 조치. `quiesce`/`reboot-recover` 는 `VENDOR` 로 정해진
  그 노드에만 적용된다.
- `remote()` 는 ServerAlive 로 원격 호스트 급정지 뒤 약 6초 안에 ssh-lost 를 판정한다.

## 급정지 원인 조사 (investigate-pcie.sh)

`tt-smi -r 0` 실행 직후 <tenstorrent-node> 이 급정지·자동 재부팅한 사건(두 차례 재현됨)의
근본 원인을 읽기 전용으로 좁히는 수집기다. `run.sh` 와 별개이고 리셋을 실행하지
않는다 — SEL 상세·PCI 설정·`_OSC` 로그·이전 부팅 커널 로그·tt 버전을 읽기 명령으로만
모아 `runs/<UTC>-investigate/` 에 남기고 `ROOTCAUSE:` 판정 줄을 찍는다. 원격 sudo 는
`sudo -n`(비대화형)으로만 쓰고, `LC_ALL=C` 로 원격 로캘 경고가 출력에 섞이지 않게 한다.

```bash
# 수집(읽기만) — <tenstorrent-node> 에서 15개 파일 + verdict.txt 를 남긴다
bash test/live/device-reset/investigate-pcie.sh

# 판정만 다시(오프라인, 클러스터 미접속) — 저장된 run 디렉터리 파일만 읽는다
RUN=$(ls -d test/live/device-reset/runs/*-investigate | tail -1)
PHASE=verdict OUT_DIR=$RUN bash test/live/device-reset/investigate-pcie.sh
```

판정 줄 형식:

```
ROOTCAUSE: sel_target=<rootport|device|unknown> aer_owner=<os|firmware|unknown> \
           dpc=<on|off|unknown> hypothesis=<platform-policy|board-link-down|kmd> \
           fw=<tt 펌웨어 번들> kmd=<tt-kmd 버전>
```

- `sel_target` 은 BMC SEL `Critical Interrupt` 레코드의 Event Data(RAW) 바이트 2 를 PCI
  버스 번호로 보고 tenstorrent 장치 버스·루트 포트 버스와 대조한 결과다(BMC 벤더 의존 해석이다).
- `aer_owner` 는 `_OSC` 협상에서 AER 를 OS 가 넘겨받았는지, 또는 APEI firmware first
  모드인지로 정한다.
- 수집은 멱등이라 여러 번 실행해도 안전하고, 판정 자동 분류가 원문과 어긋나면 스크립트의
  분류 로직을 고쳐 `PHASE=verdict` 로 다시 확인한다.
