<!-- README.md: <rngd-node> Furiosa RNGD 사전 컴파일 이미지 같은 버전 왕복 실행·복구 절차 | 생성일: 2026-09-24 | 수정일: 2026-11-04 -->

# Furiosa RNGD 사전 컴파일 이미지 라이브 왕복 (<rngd-node>)

## 왜

`deploy/helm` 의 DIP `furiosa-rngd-ds` 는 DKMS 설치 이미지
(`furiosa-driver-ds:v4`)로 드라이버를 설치한다. 이 경로는 노드에서 커널 모듈을 매번
컴파일한다. 이 디렉터리는 다른 설치 경로를 검증한다. DIP `spec.driver.kernelImages` 에 노드
커널 버전을 키로 사전 컴파일 이미지를 지정하면, 상태기계가 preflight 에서 노드 커널로 그 맵을
찾아 `status.resolvedImage` 로 고르고 install Job 이미지로 쓴다. 설치 컨테이너는 이미지에 구운
`.ko` 를 그대로 적재하며, 로그에 `prebuilt module found for <kernel>` 와
`prebuilt module for <kernel> installed (dkms skipped)` 줄을 남긴다.

`run.sh` 는 <rngd-node> 에서 같은 버전(2026.3.1)의 이미지 왕복을 실행한다. 사전 컴파일 이미지로
설치되는지(resolvedImage·Job 이미지·prebuilt 로그), 드라이버가 실제로 다시 적재되는지(NDR
driverLoaded·driverVersion·`furiosa-smi info`)를 확인한 뒤, DIP 를 기준선으로 되돌린다. 왕복은
승인된 작업이다.

## TT 하네스와 다른 점

이 노드의 실제 상태 때문에 `test/live/tt-kmd-precompiled` 와 세 가지가 다르다.

1. **버전 변경이 아니라 같은 버전 재설치다.** DIP `driver.version` 과 노드 설치 버전이 둘 다
   2026.3.1 이다. `handleIdle` 은 `desiredVersion == currentVersion` 이면 즉시 Idle 로 되돌아가
   전이를 걸지 않는다. 같은 버전에서 설치 Job 을 일으키는 유일한 경로는 Job 모드 self-heal
   (`driverLoaded=false`)이다. 그래서 노드에서 `rmmod furiosa_rngd` 로 모듈을 내려
   `/sys/module/furiosa_rngd` 를 없애면(detector 의 `rngdDriverLoaded()` 가 이 파일만 본다)
   self-heal 이 사전 컴파일 이미지로 install Job 을 돌린다.

2. **helm 오버레이가 아니라 `kubectl patch` 로 kernelImages 를 준다.** 차트
   `templates/driverinstallpolicy.yaml` 은 `kernelImages` 를 tenstorrent 블록에만 렌더하고
   furiosa/rngd 블록에는 렌더하지 않는다. 그래서 helm values 오버레이로는 사전 컴파일 이미지를
   주입할 수 없다. 라이브 DIP 오브젝트를 직접 patch 한다. 상태기계 `resolveDriverImage` 는
   벤더와 무관하게 `policy.Spec.Driver.KernelImages` 를 읽으므로 이 방식이 동작한다. helm
   릴리스 값은 수정하지 않으므로 원복 후 helm 사용자 값에 잔재가 남지 않는다. 차트가 furiosa
   kernelImages 를 렌더하도록 고치는 것은 별도 작업이다(보고서에 결함으로 기록).
   **이 patch 는 영구 설정이 아니다.** 정책 오브젝트가 helm hook(`hook-delete-policy:
   before-hook-creation`)이라 다음 helm 업그레이드에서 삭제·재생성되면 patch 로 넣은
   kernelImages·drainEnabled 값이 사라진다. 시험 중 helm 업그레이드가 없으면 문제없으나,
   뒤에 읽는 사람이 영구 설정으로 오해하지 않도록 restore 단계에서 명시적으로 원복한다.

3. **`drainEnabled=false` 로 낮춰 cordon 만 한다.** <rngd-node> 은 taint 가 없어 argocd·kubeflow·
   istio-ingressgateway 등 무관한 워크로드가 함께 실행된다. DIP 기본값 `drainEnabled=true` 로
   전이하면 이들이 전부 evict 된다. `handleDraining` 은 `drainEnabled=false` 면 evict 없이 바로
   Upgrading 으로 넘어간다. 오버레이가 이 값을 false 로 주고, 원복 때 true 로 되돌린다.

## 전제

- 오퍼레이터 v0.7.53 이상이 라이브(helm 릴리스 `kcloud-operator`, 네임스페이스 `kcloud`).
  kernelImages → resolvedImage 코드가 이 이미지에 있어야 한다. <rngd-node> 실측은 v0.7.54.
- <rngd-node>: 커널 `6.8.0-138-generic`(kernelAllowlist `6.8.*` 안), furiosa-driver-rngd 2026.3.1
  로드, DUS `<rngd-node>.cluster.local-furiosa` Idle/2026.3.1, Secure Boot 비활성.
- 레지스트리에 사전 컴파일 이미지 존재:
  `<레지스트리>:5100/kcloud/furiosa-driver-ds:2026.3.1-rngd-k6.8.0-138-generic-v1`.
- **모듈 보유자가 삭제 가능한 테스트 파드 하나뿐이어야 한다.** <rngd-node> 에서 `/dev/rngd/*` 를
  여는 프로세스가 `default/rngd-pod`(소유자 없는 `sleep infinity` 테스트 파드) 하나뿐이라,
  이를 지우면 참조 카운트가 0 으로 떨어져 모듈을 깨끗이 내릴 수 있다. 호스트 데몬이나 다른
  파드가 모듈을 잡고 있으면 preflight 가 그 사실을 기록하고 transition 이 참조 카운트 0
  대기에서 BLOCKED 로 끝난다.
- **호스트에 커널 헤더가 이미 있다.** `linux-headers-6.8.0-138-generic` 가 설치돼 있어, 이
  노드에서는 사전 컴파일이 노드 DKMS 빌드를 절약하지 못한다(postinst 가 헤더로 빌드). 사전
  컴파일의 빌드 절약은 헤더 없는 호스트에서만 나타난다. 또한 같은 버전 재설치라 설치기가 APT
  설치를 skip 하므로 APT-postinst 의 dkms 정직 로그 줄은 찍히지 않는다. 사전 컴파일 경로의
  dkms 판정 증거는 `... installed (dkms skipped)` 한 줄이다. 이 한계는 아래 한계 절 참고.
- 실행 호스트에 `helm` v4, `kubectl`, `python3`(PyYAML), <rngd-node> 로의 SSH(`ssh
  <노드 IP>`, 무암호 sudo), 레지스트리 조회 수단(`sudo -n docker manifest inspect
  --insecure` 또는 `skopeo`).

## 사용법

저장소 루트에서 실행한다. 각 페이즈는 별도 서브커맨드다. **먼저 `selftest`(오프라인)와
`DRY_RUN=1` 로 경로를 확인한 뒤** 실제로 실행한다. 산출물은 `$OUT`(기본
`/tmp/furiosa-prebuilt-evidence`)에 남고, 페이즈마다 JSON 요약 1줄이 `$OUT/summary.jsonl` 에
append 된다.

```bash
S=test/live/furiosa-prebuilt/run.sh
export KUBECTL_CONTEXT=kubernetes-admin@cluster.local
export OUT=/tmp/furiosa-prebuilt-evidence

# 0) 오프라인 + DRY(클러스터 무변경)
bash "$S" selftest
DRY_RUN=1 bash "$S" transition
DRY_RUN=1 bash "$S" restore

# 1) 실제 실행 — 순서대로
bash "$S" preflight
bash "$S" transition
# 전이 성공 후, 되돌리기 전에 증거를 수집한다:
bash test/collect/evidence.sh --node <rngd-node>.cluster.local --vendor furiosa \
  --command "kubectl --context kubernetes-admin@cluster.local -n kcloud get jobs \
    -l npu.ai/vendor=furiosa,npu.ai/node=<rngd-node>.cluster.local \
    -o jsonpath={.items[0].spec.template.spec.containers[0].image}" \
  --expected "2026.3.1-rngd-k6.8.0-138-generic-v1" \
  -o <docs 절대경로>/development/evidence/<날짜>-furiosa-prebuilt-roundtrip
bash "$S" restore
```

종료 코드는 PASS·DRY 가 0, BLOCKED 가 2, FAIL 이 1 이다. 전이 타임아웃은 `T_TRANSITION`(기본
1200초), rmmod 후 self-heal 감지 대기는 `T_UNLOAD`(기본 180초), 참조 카운트 0 대기는
`T_REFCOUNT`(기본 120초)로 조정한다.

`status` 는 언제든 DUS·DIP·NDR·Job·참조 카운트를 `$OUT` 으로 덤프하는 읽기 전용 복구 도구다.

## 각 페이즈가 기록하는 것

- **preflight**: context 도달, Ready 노드 수, 노드 커널==6.8.0-138-generic, DUS
  Idle/2026.3.1, DIP 기준선(mode=job·version=2026.3.1·verifiedVersions 포함·kernelImages
  부재), 사전 컴파일 이미지 존재, NDR furiosa/rngd(driverLoaded=true·version=2026.3.1·binding·
  passthrough), 호스트 dpkg 버전·모듈 참조 카운트·Secure Boot, 모듈 `/dev/rngd` 보유 프로세스
  목록, 스냅샷(`dus-before`·`dip-before`·`ndr-before`·`rngd-pod`·helm values). 하나라도
  어긋나면 BLOCKED.
- **selftest**(오프라인): DIP patch JSON 이 kernelImages·drainEnabled=false 를 세우는지, 사전
  컴파일 태그가 검증 빌드 태그 정규식을 통과하는지, 차트 furiosa/rngd DIP 에 kernelImages 가
  없는지(kubectl patch 근거).
- **transition**: 테스트 파드 삭제, 참조 카운트 0 대기, DIP patch(kernelImages·
  drainEnabled=false) 확인, `rmmod furiosa_rngd`, DUS 전이 표(`transitions.log`),
  `status.resolvedImage`(사전 컴파일 태그와 일치), install Job 이름·이미지·소요 초, Job 로그의
  `prebuilt module for <kernel> installed (dkms skipped)` 줄, APT-postinst dkms 판정 줄의
  부재(사유 기록), NDR driverVersion, 노드 uncordon, `furiosa-smi info` 종료 코드. EXIT 트랩이
  미완료 종료 시 노드 uncordon·테스트 파드 재생성·DIP 원복을 수행한다.
- **restore**: DIP 원복(kernelImages 제거·drainEnabled=true), 테스트 파드 재생성, DUS
  Idle/2026.3.1, NDR driverLoaded=true, 노드 schedulable, helm 값 무변경(스냅샷 대조), kcloud
  비정상 파드 확인.
- **status**: DUS/DIP/NDR/Job/참조 카운트 덤프.

## 복구 절차 (전이 실패 시 — 순서대로)

전이가 실패하면 아래를 위에서부터 밟는다. **모든 조치는 시각과 함께 기록한다.** transition 의
EXIT 트랩이 1·4·5·6 을 best-effort 로 자동 수행하지만, 실행자는 스크립트 종료와 별개로 상태를
지켜본다.

1. **DUS 상태를 읽는다.** `bash test/live/furiosa-prebuilt/run.sh status` 또는
   `kubectl --context kubernetes-admin@cluster.local get dus <rngd-node>.cluster.local-furiosa -o
   yaml`. Idle/2026.3.1 이면 전이는 끝난 것이다.
2. **자동 롤백 관측.** 검증 실패 시 상태기계가 롤백 Job 을 돌린다. Idle 로 돌아오면 이번
   실행은 FAIL 로 남기되 노드는 복구된 것이다.
3. **모듈이 안 올라온다.** 수동 재적재: `ssh <노드 IP> sudo modprobe furiosa_rngd` 후
   `/sys/module/furiosa_rngd` 확인. furiosa-rngd device-plugin 파드를 삭제해 재적재를 유도한다.
   **재부팅하지 않는다.**
4. **노드가 cordon 채로 남았다.** `kubectl uncordon <rngd-node>.cluster.local`.
5. **DIP 잔재.** `kubectl patch dip furiosa-rngd-ds --type=json -p '[{"op":"remove","path":
   "/spec/driver/kernelImages"}]'` + `--type=merge -p
   '{"spec":{"upgradePolicy":{"drainEnabled":true}}}'`. `dip-before.yaml` 과 대조.
6. **테스트 파드 미복원.** `kubectl apply -f $OUT/rngd-pod.yaml`.

## 한계

- 노드 하나(<rngd-node>), 커널 하나(`6.8.0-138-generic`)의 같은 버전 왕복이다. 다른 커널의
  kernelImages 매칭, 다른 버전으로의 전이는 이 실행에서 검증되지 않는다.
- 같은 버전 재설치라 설치기가 APT 설치를 skip 한다. 따라서 APT-postinst 의 dkms 정직 로그
  (`DKMS 가 … 빌드함` / `DKMS 빌드 없음`)는 나타나지 않는다. 사전 컴파일 경로의 dkms 판정은
  `prebuilt module for <kernel> installed (dkms skipped)` 한 줄로 확인한다. 버전 변경 전이에서
  APT-postinst dkms 판정을 보려면 별도 시나리오가 필요하다.
- 이 호스트는 커널 헤더가 있어 사전 컴파일이 노드 DKMS 빌드를 절약하지 못한다. 빌드 절약
  효과는 헤더 없는 호스트에서만 관측된다.
- 차트가 furiosa/rngd DIP 에 kernelImages 를 렌더하지 않아 kubectl patch 로 대체한다. helm
  경로의 렌더는 이 실행에서 검증되지 않는다(차트 공백).
- 모듈 unload 는 참조 카운트가 0 일 때만 한다. 호스트 데몬이나 삭제 불가 워크로드가 모듈을
  잡고 있으면 BLOCKED 로 끝난다.
