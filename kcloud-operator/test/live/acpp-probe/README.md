<!-- README.md: 벤더별 기능 probe(ACPP allocation probe) 라이브 점검 켜는 법 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# ACPP 벤더 기능 probe 라이브 점검

## 왜

`internal/controller/acpp_verifier.go` 의 `vendorProbes` 표는
등록된 벤더에 한해 `nvidia-smi -L` / `tt-smi -s` / `furiosa-smi info` / `rbln-stat` 를 probe Pod
에서 1회 실행하고 종료코드로 판정한다. 표에 이미지가 없는 벤더는 기존 pause/Running 판정을
그대로 쓴다(회귀 0). 이 디렉터리는 그 표를 **라이브에서 켜는 절차**와 **적용 샘플**을
함께 담는다.

nvidia probe 는 <nvidia-node> A30 에 적용한다. 두 단계로 나뉜다 — 1단계는
`deletionPolicy: Retain`(`acpp-a30-mig-retain.yaml`), 2단계는 baseline 복원용 `RestoreMode`
(`acpp-a30-mig-restoremode.yaml`)이며, 둘 다 `avp-allocation-probe-worker1.yaml`(<nvidia-node> 에만
AllocationProbe 를 켠 검증 정책)과 함께 적용한다. 적용 뒤 NodeDeviceReport 관측 지연으로 약 4분간
`Verified=False`/`EvidenceDisagreement`(report geometry 가 기대 지오메트리와 불일치)를 거치는데, 이
구간에도 AllocationProbe 는 이미 `exit=0` 으로 통과한다. 수렴하면 `Verified=True`/
`AllocatableConverged` → evidence Level `AllocationVerified` 로 올라간다. probe 파드는 매 검증
pass 마다 다시 뜬다. `Retain` 은 MIG mode 를 `Enabled` 로 남겨 GPU 0 이 다시 광고되지 않으므로
(gpu 1 유지), baseline(gpu 2)으로 되돌리려면 `RestoreMode` 로 재적용해야 한다(<nvidia-node> 재부팅
동반).

매 pass 마다 다시 뜨는 문제는 `internal/verification/verifier.go`(`reusableProbe`)가
완화한다. 같은 노드·벤더·자원의 직전 AllocationProbe 가 evidence 안에서 이미 pass 했고
재사용 창(직전 실제 probe 시각+TTL, 기본 30분) 안이면 probe Pod 를 다시 띄우지 않고
그 결과를 재사용한다 — 재사용된 check 는 message 에 `reused` 문구를 남기므로 `kubectl get aev
<노드> -o yaml` 로 확인할 수 있다. 이후 라이브 재실행은 같은 TTL 창 안에서 반복되는 검증 pass 마다
스케줄되는 probe Pod 수가 줄어드는지로 이 완화를 확인한다(창이 만료된 뒤 첫 pass 는 여전히
probe 한다).

## 두 개의 스위치

벤더 기능 probe 가 실제로 도는지는 서로 다른 두 스위치가 함께 결정한다. 하나만 켜면 아무 일도
안 일어난다.

1. **어떤 명령을 돌릴지 — Helm 값(`acpp.probeImages.<vendor>`)**
   비어 있으면(기본) 그 벤더는 `vendorProbeTable()` 에 아예 없는 것으로 취급돼 pause/Running
   판정을 쓴다. 값을 채우면 operator Deployment 에 `ACPP_PROBE_IMAGE_<VENDOR>` env 가 주입되고,
   그 값을 `NewLiveVerifier`가 **operator 기동 시점에 한 번** 읽는다 — 그래서 값을 바꾸면
   operator Pod 가 재시작(RollingUpdate)돼야 반영된다(`helm upgrade` 는 env 가 바뀌면 자동으로
   재시작을 유발한다).
2. **probe 를 언제 돌릴지 — evidence 검증 정책(`AcceleratorVerificationPolicy.spec.checks`)**
   벤더 기능 probe(종료코드 판정)는 **evidence 경로에서만** 돈다 — `gateReady` 가 `Verify` 를
   부르고 그 정책의 `checks` 에 `AllocationProbe` 가 켜져 있을 때다. `AcceleratorPartitionPolicy`
   apply(파티션 변경) 경로는 벤더 probe 를 돌리지 않는다. apply 에서 `VerifyAllocation` 을 부르는
   backend 는 rngd 하나뿐이고(`internal/partition/rngd/backend.go:311`) 그때 `Target.Vendor` 가
   비어 있어 `acpp.probeImages.furiosa` 를 채워도 pause probe 로 떨어진다. nvidia backend 는
   `VerifyAllocatable`(allocatable 수렴 확인)만 부르고 probe Pod 를 띄우지 않는다
   (`internal/partition/nvidia/backend.go:471`). 따라서 furiosa/rngd apply 경로의 벤더 probe 는
   아직 E2E 미검증이다(NEEDS E2E). `AcceleratorEvidence`(capability status, `kubectl get aev`)의
   `AllocationProbe` 체크는 기본 꺼져 있다(`internal/verification/policy.go` `DefaultPolicy()` —
   매 검증마다 테스트 Pod 를 띄우면 노드가 불안정해진다). 이 checks 를 정책으로 켜야 근거 재계산
   시에도 벤더 probe 가 돈다. `avp-allocation-probe.yaml`(전 노드)과
   `avp-allocation-probe-worker1.yaml`(<nvidia-node> 한정)이 그 정책 샘플이다.

## 켜는 절차

```bash
# 1) 대상 벤더의 probe 이미지를 Helm 값에 채운다(예: nvidia + furiosa 만).
#    이미지 경로는 deploy/integration/image-manifest.yaml 의 kcloud/vendor 목록 참고.
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set acpp.probeImages.nvidia=<레지스트리>:5100/kcloud/nvidia/cuda:12.9.1-base-ubuntu22.04 \
  --set acpp.probeImages.furiosa=<레지스트리>:5100/kcloud/furiosaai/furiosa-smi:2026.1.0

# furiosa 는 NEEDS VENDOR RESEARCH 상태다 — 이미지에 furiosa-smi CLI 바이너리가 없어(라이브러리만
# 포함) 이대로 켜면 probe 가 매번 exec 실패로 끝난다. HostPathBinary 연결(코드 변경 필요) 전까지는
# furiosa 값을 비워 두고 기존 pause 판정에 맡기는 편이 안전하다.

kubectl -n kcloud rollout status deploy/kcloud-operator   # env 반영 재시작 확인

# 2) (선택) capability status 재검증에도 같은 probe 를 쓰려면 정책을 적용한다. 노드 한정 정책
#    avp-allocation-probe-worker1.yaml 을 <nvidia-node> 에 적용한다(전 노드 샘플은
#    avp-allocation-probe.yaml).
kubectl apply -f test/live/acpp-probe/avp-allocation-probe-worker1.yaml
```

## 관찰

```bash
# probe Pod 는 RestartPolicy Never 로 1회 실행 후 곧바로 지워진다 — 스케줄 직후에만 보인다.
kubectl -n kcloud get pods -l npu.ai/acpp-verify-pod=true -w

# ACPP 파티션 apply 흐름의 판정은 그 AcceleratorPartitionPolicy 의 상태/이벤트에 남는다.
kubectl get acpp <정책이름> -o yaml

# evidence 검증 정책을 켰다면(2단계) 노드별 AcceleratorEvidence 의 checks[] 에서
# AllocationProbe 항목의 passed/message(종료코드·마지막 로그 요약)를 본다.
kubectl get aev <노드이름> -o yaml
```

## 원복

```bash
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set acpp.probeImages.nvidia="" --set acpp.probeImages.furiosa=""
kubectl delete -f test/live/acpp-probe/avp-allocation-probe-worker1.yaml   # 2단계를 적용했다면
```
