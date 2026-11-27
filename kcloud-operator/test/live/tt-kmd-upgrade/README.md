<!-- README.md: <tenstorrent-node> tt-kmd 2.8.0 → 2.11.0 라이브 업그레이드 실행·복구 절차 | 생성일: 2026-09-21 | 수정일: 2026-10-11 -->

# Tenstorrent tt-kmd 2.11.0 라이브 업그레이드 (<tenstorrent-node>)

## 왜

<tenstorrent-node> 노드는 그동안 tt-kmd 2.8.0 이 현재=목표 버전으로 고정돼 있어
상태기계의 실제 업그레이드 전이(install Job → 검증 → Idle 수렴, 실패 시 자동 롤백)를 실제
하드웨어로 켠 적이 없다. `test/live/tt-install-job/README.md` 와
`test/live/tt-upgrade-transition`(kind 재현)이 같은 누락을 기록했다. 이 디렉터리는 그 누락을
실제 드라이버 교체로 메운다 — 버전을 2.8.0 → 2.11.0 으로 올려 상태기계 전이를 하드웨어에서
관측한다.

## 전제

- 오퍼레이터 v0.7.51 이 라이브(helm 릴리스 `kcloud-operator`, 네임스페이스 `kcloud`).
- DIP `tenstorrent-blackhole-ds` 에 `spec.driver.imageVersionBaked=true` 렌더 — 전이 시작 시
  상태기계가 `status.previousImage = …/tenstorrent-driver-ds:2.8.0-v1` 을 캡처하고, 롤백 Job 은
  이 이미지(2.8.0)를 재설치한다.
- Harbor 에 두 드라이버 이미지 존재: `…/tenstorrent-driver-ds:2.8.0-v1`,
  `…/tenstorrent-driver-ds:2.11.0-v1`(digest sha256:76a5fca9…).
- <tenstorrent-node>: kernel 5.15.0-190-generic(kernelAllowlist `5.15.*`·`6.8.*` 안), tt-kmd 2.8.0 로드,
  NDR `driverVersion: "2.8.0"`·`validation.passed: true`, DUS `<tenstorrent-node>` Idle/2.8.0,
  tenstorrent 자원을 요청하는 파드 0, `kcloud-tt-device-plugin` Running.
- 상태기계는 `autoUpgrade=true` 이고 목표 버전이 `verifiedVersions` 안일 때만 시작한다. 검증은
  NDR `driverVersion == "2.11.0"`(detector 가 `/sys/module/tenstorrent/version` 을 읽음)을 요구한다.
  `validationTimeout` 10m, `maxRollbackAttempts` 3. TT 는 재부팅이 필요 없다(rmmod/modprobe).

## 사용법

저장소 루트에서 실행한다. **먼저 DRY 로 preflight 와 helm 명령을 확인한 뒤** 실제 실행한다.

```bash
# 1) DRY: preflight 통과 확인 + helm 명령을 실행하지 않고 출력만
DRY_RUN=1 bash test/live/tt-kmd-upgrade/run.sh

# 2) 실제 실행(드라이버 교체)
bash test/live/tt-kmd-upgrade/run.sh
```

기본값은 스크립트 상단 환경변수로 덮어쓸 수 있다(`NODE`·`NEW_VERSION`·`NEW_TAG`·`OUT`·
`T_CONVERGE` 등). 산출물은 `$OUT`(기본 `/tmp/tt-kmd-upgrade-evidence`)에 남는다:
`run.log`, `transitions.log`, `install-job.log`, `install-jobs.yaml`, `tt-smi.log`,
`dus-before.yaml`·`ndr-before.yaml`·`ndr-after.yaml`(+ 실패 시 `dus-after.yaml`),
`live-values-before.yaml`.

## 관측 항목

- **DUS 전이**: `Idle → UpgradeRequired → PreFlight → Cordoning → Draining → Upgrading →
  Validating → Uncordoning → Idle`(`transitions.log`).
- **previousImage(롤백 대상)**: 첫 non-Idle 상태에서 `status.previousImage` 가
  `…/tenstorrent-driver-ds:2.8.0-v1` 인지 확인. 아니면 즉시 FAIL.
- **install Job 로그**: DKMS 빌드/설치 줄과 `modprobe` 성공 줄(`install-job.log`).
- **노드 라벨**: `kcloud.ai/driver-tenstorrent.version == 2.11.0`.
- **NDR**: `status.devices[tenstorrent].driverVersion == 2.11.0`.
- **allocatable**: `tenstorrent.com/blackhole == 1` 복구.
- **tt-smi probe**: `probe-tenstorrent:v1` 파드가 `tt-smi -s` 로 장치를 보고 Succeeded(exit 0),
  로그의 driver 항목이 2.11.0(`tt-smi.log`).

## 복구 절차 (검증 실패 시 — 순서대로)

검증이 실패하면 아래를 위에서부터 밟는다. **모든 조치는 시각과 함께 기록한다.**

1. **자동 롤백을 먼저 관측한다.** 검증 실패 시 상태기계가 `previousImage`(`2.8.0-v1`)로 롤백
   Job 을 돌린다. `Idle/2.8.0` 으로 돌아오면 이번 실행은 FAIL 로 기록하고, evidence 에
   `-rollback` 레코드를 따로 남긴다(전이 표에 롤백 구간 포함). 여기서 멈추면 노드는 2.8.0 이다.
2. **터미널 `Failed`(롤백 3회 소진).** values 를 되돌린다:
   ```bash
   helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values --force-conflicts \
     --set driverInstallPolicies.tenstorrent.driver.tag=2.8.0-v1 \
     --set driverInstallPolicies.tenstorrent.driver.version=2.8.0 \
     --set driverInstallPolicies.tenstorrent.upgradePolicy.autoUpgrade=false
   kubectl delete dus <tenstorrent-node>   # ensureUpgradeStates 가 NDR 기준 Idle 로 재생성
   ```
   NDR 이 `driverLoaded=false` 면 self-heal 분기가 2.8.0 이미지로 install Job 을 자동으로 실행한다.
3. **NDR 이 2.11.0 로드 상태로 Failed 에 앉으면**, `allowDowngrade=true`·`autoUpgrade=true` 를
   잠시 켜 2.8.0 다운그레이드 Job 을 돌리거나, 2.11.0 을 유지한 채 **AWAITING USER** 로 기록한다.
   둘 중 무엇을 택했는지 기록해 둔다.
4. **rmmod 가 계속 실패(refcount)** 하면 device-plugin 파드를 삭제하고 재시도한다. 그래도 안 되면
   `kubectl cordon <tenstorrent-node>` 뒤 노드를 재부팅하고(승인 범위 — <tenstorrent-node> 한정) self-heal 을
   기다린다. 재부팅은 복구 절차의 마지막 수단이며 <tenstorrent-node> 에만 쓴다. **<rebellions-node> 는 절대 바꾸지 않는다.**

## 한계

- 노드 하나의 한 번 전이다. 재부팅 경로(`RebootRequired`)는 TT 가 세우지 않으므로 이 실행에서
  검증되지 않는다.
- 롤백 경로는 검증이 실패했을 때만 실제로 관측된다(성공 경로에서는 관측되지 않음).
- `verifiedVersions` 는 `OLD_VERSION`·`NEW_VERSION` 두 값만 넣는다(차트 기본값과 같은 목록이어야
  하며, 다르면 성공 뒤 `helm get values` 에 그 차이가 남는다).

## 운영 주의

- `run.sh` 가 `return 1` 로 끝나도(예: previousImage 불일치, 타임아웃) helm values 는 이미 바뀐
  뒤라 DUS 상태기계는 계속 진행한다. 실행자는 스크립트 종료와 별개로
  `kubectl get dus <tenstorrent-node> -w` 로 전이를 계속 지켜봐야 한다.
- FAIL 경로는 `autoUpgrade=true` 를 그대로 남긴다. 자동 롤백이 계속 시도되도록 하는 설계이며,
  위 복구 절차 2번(터미널 Failed)에서 값을 되돌리기 전까지는 유지된다.
- 성공 경로의 `helm_set false` 는 `--reset-then-reuse-values` 와 `--set` 조합이라 tenstorrent 값
  네 개(`driver.tag`·`driver.version`·`verifiedVersions`·`upgradePolicy.autoUpgrade`)가 릴리스
  user values 에 영구히 남는다(라이브 `helm get values` 로 확인). 이후 차트 기본값을 올려도 이 네
  값을 직접 reset 하기 전까지는 운영 클러스터에 반영되지 않는다. 정리는 `helm get values -o yaml` 을 저장해
  `driverInstallPolicies.tenstorrent` 블록을 지운 파일로 `helm upgrade --reset-values -f <파일>` 을
  한 번 실행하는 것이다.
- 전제 확인의 파드 검사는 `containers` 만 센다. `initContainers` 로 tenstorrent 자원을 요청하는
  파드가 있으면 이 검사가 놓친다.
