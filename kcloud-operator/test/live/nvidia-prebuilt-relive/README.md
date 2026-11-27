<!-- README.md: <nvidia-node> NVIDIA 사전 컴파일 라이브 재확인 하네스 사용법 | 생성일: 2026-09-28 |
     수정일: 2026-10-11 -->
# <nvidia-node> NVIDIA 사전 컴파일 라이브 재확인

## 왜

images 저장소의 사전 컴파일 정합성 가드는 더 이상 `apt-cache madison` 아카이브 조회를 쓰지
않는다 — 그 조회를 제거한 뒤 다시 빌드한 이미지로 빈 노드 전이를 돌려 확인해야 한다.
<nvidia-node> 에 떠 있던 `-v2` 이미지는 그 제거보다 먼저 빌드돼, 지금 라이브에 있는 것은
제거 이전 버전이다. 이 하네스는 제거를 담은 새 이미지(`-v3`, 이 하네스 밖에서
`make build-precompiled`로 미리 빌드·push)를 DIP 에 등록하고, 9개 패키지 제거+재부팅으로
노드를 빈 상태로 만든 뒤 operator 자기복구 설치 Job 을 관찰해 그 재확인을 한다.

## 허용 조치(닫힌 목록) — 이 밖은 컨트롤러 승인 없이 하지 않는다

1. 읽기: `kubectl get`(DIP·DUS·node·pods·acceleratorhealth), `dpkg -l`, GRUB 설정 조회,
   `cat /sys/module/nvidia/version`, `kubectl logs`.
2. 쓰기: DIP `nvidia-gpu-ds` 의 `spec.driver.kernelImages["5.15.0-171-generic"]` patch,
   `kubectl cordon`/`uncordon <nvidia-node>`, `sudo apt-get remove -y` **정확히 이 9개
   패키지**(`PACKAGES` 배열, 순서·이름 고정), `sudo reboot`(관리 노드 무승인).
   `autoremove`·`purge`·다른 패키지 제거·수동 dpkg/apt 재설치는 하지 않는다.
3. 복구 수단(실패했을 때만): `PHASE=rollback-dip` 으로 DIP 를 이전 값(`-v2`)으로 되돌리고
   operator 자기복구가 다시 돌 때까지 기다린다(30분). 그래도 안 되면 수동 정리 절차를
   컨트롤러 승인 하에 각 명령 전 보고하며 따른다 — 이 하네스가 스스로 하지 않는다.
4. 노드 대상은 `kubectl get node <nvidia-node> -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}'`
   로만 구한다. 시범서비스 배포 노드는 접촉 경로 자체가 없다.
   <nvidia-node> 밖의 어떤 노드에도 접촉하지 않는다.

## 안전 게이트

- **`nvidia.com/gpu` 를 requests 나 limits 에 담은 파드가 <nvidia-node> 에 0 개**여야
  한다(uid 중복 제거). `precheck`·`quiesce`·`empty_node`·`reboot_recover`·`main`
  전부 이 게이트를 통과한 뒤에만 진행한다.
- **GRUB 확인**(게이트는 아니고 기록): `precheck` 가 `GRUB_DEFAULT`·`saved_entry` 가
  둘 다 `KERNEL`(기본 `5.15.0-171-generic`)을 가리키는지 evidence 에 남긴다. 이
  기록만으로 재부팅 뒤 실제로 그 커널로 뜬다는 보장은 없다 — `wait-empty`/`observe`
  단계가 실제 모듈·Job 관찰로 확인한다.
- **재부팅 완성 판정**: SSH 로 잰 `uptime<600` 초(=새 boot)와 `kubectl` 이 보는
  node Ready=True 가 **같은 폴링 순간**에 동시에 성립해야 통과다. 재부팅 전 오래 떠 있던
  stale Ready(uptime 87897초)를 새 Ready 로 착각할 뻔한 함정이 있다 — 이 동시 조건이
  그 함정을 구조적으로 막는다.
- **관찰 종료 조건**(`observe`): 설치 Job 파드가 `Succeeded`이고 DUS 가 `Idle`로
  돌아온 뒤, 모듈 버전==정책 버전·allocatable==2·health==Healthy 세 조건이 모두
  맞아야 통과다.

## 사용법

```bash
# 미리보기(안전 게이트·상태 조회는 실제로 돈다, 상태를 바꾸는 명령은 출력만)
DRY_RUN=1 bash test/live/nvidia-prebuilt-relive/run.sh

# 전체 절차(precheck→dip-patch→quiesce→empty-node→reboot-recover→wait-empty→observe→unquiesce)
# 이미지(-v3)는 이 하네스 밖에서 images 저장소 make build-precompiled 로 먼저 빌드·push 해 둔다.
RUN_ID=<임의ID> bash test/live/nvidia-prebuilt-relive/run.sh

# 단계별 실행(PHASE)
PHASE=precheck        bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=dip-patch        bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=quiesce          bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=empty-node       bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=reboot-recover   bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=wait-empty       bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=observe          bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=unquiesce        bash test/live/nvidia-prebuilt-relive/run.sh
PHASE=rollback-dip     bash test/live/nvidia-prebuilt-relive/run.sh   # 실패했을 때만

# 판정(오프라인, 클러스터 미접속) — 모든 phase 가 같은 RUN_ID(=같은 OUT_DIR) 를 써야 한다
RUN_ID=<같은 ID> 로 진행한 뒤:
RUN_DIR=test/live/nvidia-prebuilt-relive/runs/<RUN_ID>-nvidia PHASE=verdict bash test/live/nvidia-prebuilt-relive/run.sh
# -> RELIVE: image=<태그> madison=<n|missing> apt_install=<n|missing> dpkg_i=<n|missing> module=<버전|missing> gpu=<개수|missing> dus=<상태|missing>
```

환경변수로 덮을 수 있는 값: `RUN_ID`(기본 UTC 타임스탬프)·`KERNEL`(기본
`5.15.0-171-generic`)·`DRIVER_VERSION`(기본 `580.126.09`)·`NEW_VARIANT`(기본
`v3`)·`REGISTRY_REPO`(기본 `<레지스트리>:5100/kcloud/nvidia-driver-ds`)·
`IMAGE_TAG`(위 셋으로 계산한 값을 통째로 덮고 싶을 때)·`REBOOT_TIMEOUT`(기본
540초)·`OBSERVE_TIMEOUT`(기본 540초). **제거 대상 9개 패키지 이름·순서
(`PACKAGES` 배열)는 환경변수로 덮지 않는다** — images 저장소 설치기의 정리 명령과
정확히 같아야 한다는 것이 이 절차의 근거다.

**`PHASE=quiesce` 를 단독으로 돌렸으면 반드시 `PHASE=unquiesce` 로 되돌린다.** 정상
종료(rc 0)에서는 `cleanup` 이 uncordon 하지 않는다. `dip-patch` 로 바꾼 DIP 값은
`cleanup` 이 되돌리지 않는다 — 되돌릴 필요가 있으면 컨트롤러 판단 하에
`PHASE=rollback-dip` 을 따로 부른다.

## 남기는 것

매 실행은 `runs/<RUN_ID>-nvidia/`(git-ignored) 에 다음을 남긴다.

| 파일 | 내용 |
|---|---|
| `evidence.txt` | 각 단계의 원문 출력을 구분선과 함께 순서대로 누적한 것 |
| `pre-module-version.txt` / `pre-allocatable.txt` | 재부팅 전 기준선(참고용) |
| `dip-before.txt` / `dip-after.txt` | DIP patch 이전/이후 `kernelImages[KERNEL]` 값 |
| `image-tag.txt` | 이번 전이에 쓴 이미지 태그(`RELIVE` 줄의 `image=`) |
| `remove-exit.txt` / `remove-ssh-lost.txt` | 패키지 제거 명령 종료 코드 또는 SSH 유실 표시 |
| `reboot-start-utc.txt` / `reboot-elapsed.txt` | 재부팅 시각 / 완료까지 걸린 초 |
| `install-job.log` | 설치 Job 파드 로그 전문 — `PHASE=verdict` 가 이 파일에서 `madison`·`apt_install`·`dpkg_i` 를 센다 |
| `final-module-version.txt` / `final-allocatable.txt` / `final-dus.txt` | `observe` 가 마감 시점에 관측한 값 — `PHASE=verdict` 가 이 파일들만 읽는다 |

## `madison`/`apt_install` 이 실제로 증명하는 것과 증명하지 못하는 것

`entrypoint.sh` 의 `ns()` 래퍼는 실행할 명령 문자열 자체를 로그로 echo 하지 않고
(`nsenter ... bash -lc "$*"` 뿐), `apt-cache madison`·`apt-get install` 같은 apt
하위 명령도 스스로 자기 호출 문자열을 표준출력에 되찍지 않는다. 그래서
`install-job.log` 를 문자열로 grep 하는 `madison`·`apt_install` 두 수치는, 코드
경로가 그 명령을 실제로 불렀는지와 무관하게 **구조적으로 항상 0에 가깝다** — 값이
0 이라는 사실 하나만으로 "이번 실행이 그 명령을 안 불렀다"를 증명하지 못한다(부재
지표가 건너뛴 일도 충족하는 함정, `lesson_absence_metric_satisfied_by_skipped_work`
와 같은 종류).

`apt_install=0` 은 "이 Job 이 apt 를 아예 안 불렀다"는 뜻이 아니다 — entrypoint 는
`build-essential`·`curl`·`dkms`·`jq` 등 드라이버와 무관한 기본 도구 설치에 여전히
`apt-get update`/`apt-get install` 을 쓴다(로그 앞부분 "Apt update + base tools" 절).
`apt_install=0` 이 가리키는 것은 **드라이버 사용자 공간 패키지**(`libnvidia-compute`·
`nvidia-utils`·`nvidia-kernel-common` 등)를 apt 로 설치한 적이 없다는 것뿐이다.

이 재확인이 실제로 기대는 근거는 따로 있다.

1. **이미지 자체의 정적 확인**(라이브 로그가 아니라 이미지 파일 확인):
   `sudo docker run --rm --entrypoint sh <태그> -c 'grep -c "apt-cache madison" /usr/local/bin/entrypoint.sh'`
   가 0 — 이 이미지의 `entrypoint.sh` 에는 그 조회를 부르는 코드 자체가 없다는
   뜻이라, 실행 여부와 무관하게 성립하는 강한 증거다.
2. **`dpkg_i`(=`install-job.log` 의 `Preparing to unpack .../<pkg>_<ver>_<arch>.deb`
   줄 수) ≥ 1**: 이것은 dpkg 자신이 각 `.deb` 파일을 처리할
   때 찍는 출력이라 echo 유무와 무관하게 실제로 그 파일이 언패킹됐다는 적극 증거다.
   `Unpacking <패키지명> (<버전>) ...` 줄도 같은 개수를 내지만 패키지명만 찍혀 `.deb`
   파일 자체를 가리키지 않는다 — `dpkg_i` 는 전자를 센다.
3. **건너뜀 아님**(`already installed and healthy|skipping` 부재, evidence 에만
   기록·판정 기계 줄에는 없음)와 모듈 버전·srcversion 일치: 설치가 실제로 일어났고
   건너뛰지 않았다는 보강 증거.

`RELIVE` 줄의 `madison`/`apt_install` 은 위 RELIVE 줄 형식대로 남기지만,
이 절차의 실제 판정 근거는 위 1~3 이다 — 보고서는 이 셋을 함께 적는다.

## 이 하네스가 하지 않는 것

- <nvidia-node> 밖의 어떤 노드에도 접촉하지 않는다.
- 9개 패키지 밖의 어떤 패키지도 제거하지 않는다(`autoremove`·`purge` 포함).
- 실패 시 스스로 수동 dpkg/apt 재설치를 하지 않는다 — 기록하고 멈춘다.
  `PHASE=rollback-dip` 으로 DIP 만 되돌리고, 그 이상은 컨트롤러 승인 뒤 사람이 판단한다.
- device-plugin·dcgm-exporter DaemonSet 파드를 직접 변경하지 않는다(`quiesce` 는
  cordon 만 한다) — 드라이버가 사라지면 CrashLoop 이 정상이고 설치 뒤 스스로 복귀한다.
- 반복 측정. 한 번의 전이만 실행한다.
