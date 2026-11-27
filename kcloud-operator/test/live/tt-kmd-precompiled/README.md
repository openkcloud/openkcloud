<!-- README.md: <tenstorrent-node> tt-kmd 사전 컴파일 이미지 왕복(2.11.0→2.8.0→2.11.0) 실행·복구 절차 | 생성일: 2026-09-22 | 수정일: 2026-10-11 -->

# Tenstorrent tt-kmd 사전 컴파일 이미지 왕복 (<tenstorrent-node>)

## 왜

`test/live/tt-kmd-upgrade` 는 DKMS 설치 이미지(`tenstorrent-driver-ds:<version>-v1`)로 버전 전이를
검증한다. 이 경로는 노드에서 커널 모듈을 매번 컴파일하므로 설치에 약 21초가 걸린다.
이 디렉터리는 다른 설치 경로를 검증한다. DIP `spec.driver.kernelImages` 에 노드 커널 버전을
키로 사전 컴파일 이미지를 지정하면, 상태기계가 preflight 에서 노드 커널로 그 맵을 찾아
`status.resolvedImage` 로 고르고 install Job 이미지로 쓴다. 설치 컨테이너는 DKMS 컴파일 없이
사전 빌드된 모듈을 적재하며, 로그에 `prebuilt module for <kernel> installed (dkms skipped)` 줄을
남긴다.

`run.sh` 는 <tenstorrent-node> 에서 2.11.0 → 2.8.0 → 2.11.0 왕복을 실행한다. 두 전이 모두 사전 컴파일
이미지로 설치되는지(resolvedImage·Job 이미지·`dkms skipped` 로그), 버전이 실제로 바뀌는지(NDR
driverVersion·`/sys/module/tenstorrent/version`·tt-smi probe)를 확인한 뒤, 사용자 값을 기준선으로
되돌려 tenstorrent 오버레이와 지표 스위치를 제거한다. 왕복은 승인된 작업이다.

## 전제

- 오퍼레이터 v0.7.53 이 라이브(helm 릴리스 `kcloud-operator`, 네임스페이스 `kcloud`).
  `kernelImages` 렌더링과 preflight resolvedImage 설정 코드가 이 이미지에 포함돼 있어야 한다.
- <tenstorrent-node>: 커널 `5.15.0-190-generic`(kernelAllowlist `5.15.*`·`6.8.*` 안), tt-kmd 2.11.0 로드,
  DUS `<tenstorrent-node>` Idle/2.11.0, tenstorrent 자원을 요청하는 파드 0, Secure Boot 비활성.
- 레지스트리에 사전 컴파일 이미지 두 개 존재:
  `<레지스트리>:5100/kcloud/tenstorrent-driver-ds:2.8.0-k5.15.0-190-generic-v1`,
  `…:2.11.0-k5.15.0-190-generic-v1`.
- DIP 기본값은 `mode: job`·`imageVersionBaked: true` 이다. `kernelImages` 는 `mode: job` 에서만
  유효하다(CRD XValidation). 전이 시작 시 상태기계가 `status.previousImage` 로 직전 이미지를
  캡처한다. 이 캡처값은 이전 버전의 DKMS 태그다(`resolvedImage` 에서 커널 토큰을 떼고 `-vN` 만 남긴
  재구성). 캡처값이 없으면 `driver.image` 에서 같은 방식으로 재구성하므로 롤백은 거부되지 않는다. 사전
  컴파일 설치라도 롤백 대상은 노드 DKMS 빌드라 헤더·컴파일러가 필요하다.
- 실행 호스트에 `helm` v4, `kubectl`, `python3`(PyYAML), <tenstorrent-node> 로의 SSH(`ssh <노드 IP>`),
  레지스트리 조회 수단(`skopeo` 또는 `sudo -n docker manifest inspect --insecure`)이 있어야 한다.

## 사용법

저장소 루트에서 실행한다. 각 페이즈는 별도 서브커맨드다. **먼저 `DRY_RUN=1` 로 변경 명령과 생성
values 파일을 확인한 뒤** 실제로 실행한다. 산출물은 `$OUT`(기본 `/tmp/tt-kmd-precompiled-evidence`)에
남고, 페이즈마다 JSON 요약 1줄이 `$OUT/summary.jsonl` 에 append 된다. `preflight` 는 배포 전
`helm get values` 를 `$OUT/values-baseline.yaml` 로 저장하고, `restore` 는 그 파일(`VALUES_BASE`
기본값)로 원복한다.

```bash
S=test/live/tt-kmd-precompiled/run.sh
export KUBECTL_CONTEXT=kubernetes-admin@cluster.local
export CHART_DIR=deploy/helm            # 격리 clone 의 차트 경로로 대체 가능
export IMAGE_TAG=v0.7.53                 # restore 가 --set image.tag 에 쓰는 값
export OUT=/tmp/tt-kmd-precompiled-evidence   # 산출물·기준선(values-baseline.yaml) 위치
# VALUES_BASE 기본값 = $OUT/values-baseline.yaml (preflight 가 저장). 다른 기준선을 쓸 때만 지정.

# 0) DRY: 변경 명령·오버레이만 출력(클러스터 무변경)
DRY_RUN=1 bash "$S" preflight
DRY_RUN=1 bash "$S" transition 2.8.0 2.8.0-v1
DRY_RUN=1 bash "$S" transition 2.11.0 2.11.0-v1
DRY_RUN=1 bash "$S" restore

# 1) 실제 실행 — 순서대로
bash "$S" preflight
EXPECT_PREV_IMAGE=<레지스트리>:5100/kcloud/tenstorrent-driver-ds:2.11.0-v1 \
  bash "$S" transition 2.8.0 2.8.0-v1
bash "$S" transition 2.11.0 2.11.0-v1
bash "$S" restore
```

`transition 2.8.0` 은 2.11.0 에서 2.8.0 으로 내리는 다운그레이드다. 오버레이가
`driver.allowDowngrade=true` 를 주므로 설치가 진행된다. `EXPECT_PREV_IMAGE` 를 주면 첫 non-Idle
상태에서 캡처된 `previousImage` 를 그 값과 대조하고 다르면 즉시 FAIL 한다(2.8.0 전이의 직전
이미지는 `2.11.0-v1`). 두 번째 전이(2.11.0)는 `previousImage` 를 기록만 한다.

종료 코드는 PASS·DRY 가 0, BLOCKED 가 2, FAIL 이 1 이다. 전이 1건 타임아웃은 `T_TRANSITION`(기본
1200초), helm `--wait` 타임아웃은 `HELM_TIMEOUT`(기본 10m)로 조정한다. 필드매니저 충돌 시
`HELM_EXTRA_FLAGS=--force-conflicts` 를 준다.

`status` 는 언제든 DUS·DIP·NDR·Job 을 `$OUT` 으로 덤프하는 읽기 전용 복구 도구다. `selftest` 는
클러스터 없이 오버레이 생성과 helm 템플릿 렌더(`kernelImages`·`allowDowngrade`·`autoUpgrade`)를
검증한다.

## 각 페이즈가 기록하는 것

- **preflight**: context 도달, Ready 노드 수(기대 5), DUS Idle/2.11.0, helm 리비전·배포 이미지 태그,
  <tenstorrent-node> 의 tenstorrent 자원 요청 파드 수(기대 0), 사전 컴파일 이미지 두 개 존재, 노드 커널·
  `/sys/module/tenstorrent/version`·Secure Boot 상태, 배포 전 helm values 를
  `$OUT/values-baseline.yaml` 로 저장(restore 기준). 하나라도 어긋나면 BLOCKED 로 종료한다.
  Secure Boot 가 enabled 면 왕복을 HARDWARE 사유로 BLOCKED 처리한다.
- **transition <version> <tag>**: 생성한 오버레이, `helm upgrade --reset-then-reuse-values -f` 명령,
  DUS 전이 표(`transitions-<version>.log`, 상태·resolvedImage·previousImage·메시지를 시각과 함께),
  `status.resolvedImage`(사전 컴파일 태그와 일치해야 함), install Job 이름·컨테이너 이미지·시작·
  완료 시각·소요 초, Job 로그의 `prebuilt module for <kernel>` 줄(`dkms skipped` 포함 확인·인용),
  `previousImage`, NDR driverVersion, `/sys/module/tenstorrent/version`, tt-smi probe 로그(exit 0).
- **restore**: `helm upgrade --reset-values -f <baseline> --set image.tag` 명령, 원복 후 DIP 기본값
  (이미지 `…:2.11.0-v1`, kernelImages 없음, allowDowngrade false), DUS Idle/2.11.0,
  `helm get values` 에 `driverInstallPolicies.tenstorrent`·`metrics` 오버레이가 없음, Ready 노드 5,
  kcloud 네임스페이스 비정상 파드 0, 테스트 probe 파드 잔존 0.
- **status**: DUS/DIP/NDR/tenstorrent install Job 의 현재 상태를 `$OUT` 으로 덤프한다.

## 복구 절차 (전이 실패 시 — 순서대로)

전이가 실패하면 아래를 위에서부터 밟는다. **모든 조치는 시각과 함께 기록한다.** `run.sh` 가
비정상 종료해도 helm values 는 이미 바뀐 뒤이므로 상태기계는 계속 진행한다. 실행자는 스크립트
종료와 별개로 상태를 지켜봐야 한다.

1. **DUS 상태를 읽는다.** `bash test/live/tt-kmd-precompiled/run.sh status` 또는
   `kubectl --context kubernetes-admin@cluster.local get dus <tenstorrent-node> -o yaml` 로
   `status.state`·`currentVersion`·`previousImage`·`resolvedImage`·`message` 를 확인한다.
   `Idle/2.8.0` 또는 `Idle/2.11.0` 이면 전이 자체는 끝난 것이므로 어느 버전에 앉았는지만 기록한다.
2. **자동 롤백을 관측한다.** 검증이 실패하면 상태기계가 `previousImage`(캡처된 직전 이미지)로 롤백
   Job 을 돌린다. `Rollback` 상태가 지나 `Idle/<이전 버전>` 으로 돌아오면 이번 실행은 FAIL 로 남기되
   노드는 정상 버전으로 복구된 것이다.
3. **터미널 `Failed`(롤백 소진) 또는 원치 않는 버전에 앉음.** 목표 버전으로 강제 복구하려면
   그 버전을 previous 태그로 지정하고 다운그레이드를 허용해 상태기계에 다시 태운다:
   ```bash
   helm upgrade kcloud-operator "$CHART_DIR" -n kcloud \
     --kube-context kubernetes-admin@cluster.local --reset-then-reuse-values \
     --set driverInstallPolicies.tenstorrent.driver.version=2.11.0 \
     --set driverInstallPolicies.tenstorrent.driver.tag=2.11.0-v1 \
     --set driverInstallPolicies.tenstorrent.driver.allowDowngrade=true \
     --set driverInstallPolicies.tenstorrent.upgradePolicy.autoUpgrade=true
   kubectl --context kubernetes-admin@cluster.local delete dus <tenstorrent-node>
   ```
   DUS 를 지우면 `ensureUpgradeStates` 가 NDR 기준으로 Idle 상태를 재생성한다. NDR 이
   `driverLoaded=false` 면 self-heal 분기가 install Job 을 자동 실행한다. 사전 컴파일 경로로
   되돌리려면 위 `--set` 대신 `run.sh transition` 이 쓰는 kernelImages 오버레이를 다시 적용한다.
4. **`rmmod` 가 계속 실패(refcount)** 하면 tt device-plugin 파드를 삭제하고 재시도한다. 그래도 안
   되면 노드를 재부팅한다. <tenstorrent-node> 재부팅은 승인 범위다:
   ```bash
   kubectl --context kubernetes-admin@cluster.local cordon <tenstorrent-node>
   ssh <노드 IP> sudo reboot
   ```
   재부팅 후 노드가 Ready 로 복귀하면 uncordon 하고 self-heal 을 기다린다. 재부팅은 복구 절차의
   마지막 수단이며 <tenstorrent-node> 에만 쓴다. **다른 노드는 재부팅하지 않는다.**
5. **helm 값 기준선 복구.** 왕복 오버레이(tenstorrent·metrics)를 사용자 값에서 완전히 제거하려면
   `restore` 를 실행한다:
   ```bash
   OUT=/tmp/tt-kmd-precompiled-evidence \
   IMAGE_TAG=v0.7.53 bash test/live/tt-kmd-precompiled/run.sh restore
   ```
   `restore` 는 `--reset-values -f <baseline>` 로 릴리스 사용자 값을 기준선으로 완전히 덮어쓴다.
   `--reuse-values` 는 맵에서 키를 지우지 못하므로 kernelImages·autoUpgrade 잔존을 없애려면 반드시
   `--reset-values` 경로인 `restore` 를 쓴다.

## 한계

- 노드 하나(<tenstorrent-node>), 커널 하나(`5.15.0-190-generic`)의 왕복이다. 다른 커널의 kernelImages
  매칭은 이 실행에서 검증되지 않는다.
- 자동 롤백 경로는 검증이 실패했을 때만 실제로 관측된다. 성공 왕복에서는 관측되지 않는다.
- preflight 의 파드 검사는 `containers` 만 센다. `initContainers` 로 tenstorrent 자원을 요청하는
  파드가 있으면 이 검사가 놓친다.
- Tenstorrent 는 rmmod/modprobe 로 교체되며 재부팅 경로(`RebootRequired`)를 세우지 않으므로 이
  실행에서 재부팅 전이는 검증되지 않는다.
