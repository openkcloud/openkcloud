<!-- README.md: tenstorrent descriptor evidence allocation probe 라이브 점검 켜는 법 | 생성일: 2026-09-20 -->
# descriptor evidence allocation probe 라이브 점검

## 왜

tenstorrent·rebellions 는 ACPP(AcceleratorPartitionPolicy)가 없는 벤더다. 이전에는 이 벤더들의
`AcceleratorDescriptor` 가 feasible 판정을 받아도 `verification.Verifier.Verify` 를 아무도 부르지
않아 `AcceleratorEvidence` 근거가 전혀 쌓이지 않았고, allocation probe(장치를 실제로 할당받아
`tt-smi -s` 같은 벤더 명령이 성공하는지)도 이 경로에서는 검증할 수 없었다.

`AcceleratorDescriptorReconciler` 에 `Verification *verification.Verifier` 를 주입하면서, feasible
판정이 성공하고 장치가 노드에 resolve 된 descriptor 마다 재조정 때 `Verify` 를 부르도록 연결했다
(`internal/controller/acceleratordescriptor_controller.go` 의 `verifyResolvedDevices`). 이 디렉터리는
그 연결을 라이브에서 켜는 절차와, 자동화한 `run.sh` 를 담는다.

rebellions 도 같은 코드 경로를 그대로 타지만, 검증 노드(<rebellions-node>)가 외부 서비스에 점유돼 있어 이
디렉터리의 라이브 점검은 tenstorrent(<tenstorrent-node>)에 한정한다 — rebellions 는 코드 연결만 공유하고
별도 라이브 점검을 하지 않는다.

## 두 개의 스위치

`test/live/acpp-probe/README.md` 와 같은 두 스위치를 그대로 재사용한다 — probe 실행 여부를
결정하는 메커니즘이 ACPP 경로와 descriptor 경로가 같은 `verification.Verifier` 이기 때문이다.

1. **어떤 명령을 돌릴지 — Helm 값(`acpp.probeImages.tenstorrent`)**
   비어 있으면(기본) `tt-smi -s` 를 실행할 이미지가 없어 probe 는 시도되지 않는다. 값을 채우면
   operator Deployment 가 재시작되고, 그 시점부터 `NewLiveVerifier` 가 이 이미지로 probe Pod 를
   띄운다.
2. **probe 를 언제 돌릴지 — evidence 검증 정책(`AcceleratorVerificationPolicy.spec.checks`)**
   그 노드에 매칭되는 정책의 `checks` 에 `AllocationProbe` 가 있어야 `Verify` 내부가 probe Pod 를
   띄운다. 기본은 꺼져 있다(`internal/verification/policy.go` `DefaultPolicy()`).

**새 동작**: descriptor 경로의 `Verify` 호출은 정책 유무와 무관하게 매 재조정마다
`AcceleratorEvidence` 를 upsert 한다(`internal/verification/verifier.go:127` 의 정책 게이트는 probe
Pod 실행만 억제할 뿐, `Verify` 자체의 근거 upsert 는 막지 않는다). 즉 위 1·2 번을 하나도 안 켜도
resolve 된 노드의 evidence 객체는 생성·갱신되고 등급을 받는다 — `Verify` 요청에 resolve 된 장치
ID 로 만든 관측 geometry(값은 비워 존재만 확인)가 실려 DeviceObservation 이 통과하기 때문이다.
`AllocationProbe` 를 안 켜면 그 정책 조합이 도달할 수 있는 등급(기본 체크 조합에서는 광고 기대량이
없어 `Observed`)에 머물고, `AllocationProbe` 까지 통과하면 그 조합의 상한 등급으로 올라간다. 이
upsert 는 재조정마다 status write 를 하나 더하는 비용이 있다 — probe Pod 를 안 띄워도 발생한다.
문제가 되면 재조정 술어나 upsert 스킵 조건을 별도로 검토한다(probe Pod 가 뜨는 경우의 재기동
비용은 다른 완화 대상이다).

## 켜는 절차

```bash
# 1) probe 이미지를 Helm 값에 채운다.
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set acpp.probeImages.tenstorrent=<레지스트리>:5100/kcloud/probe-tenstorrent:v1
kubectl -n kcloud rollout status deploy/kcloud-operator   # env 반영 재시작 확인

# 2) <tenstorrent-node> 한정 AllocationProbe 검증 정책을 적용한다.
kubectl apply -f test/live/descriptor-probe/avp-allocation-probe-worker3.yaml

# 3) descriptor 재조정을 트리거한다 — AcceleratorVerificationPolicy 는 감시 대상이 아니므로
#    (SetupWithManager 가 AcceleratorDescriptor/NodeDeviceReport/NPUClusterPolicy 만 감시한다)
#    descriptor 자체를 건드려야 다음 재조정이 돈다. spec 을 바꾸지 않는 annotate 로 충분하다.
kubectl annotate acceleratordescriptor <tenstorrent-descriptor-이름> \
  npu.ai/probe-trigger="$(date -u +%FT%TZ)" --overwrite
```

## 관찰

```bash
# probe Pod 는 RestartPolicy Never 로 1회 실행 후 곧바로 지워진다 — 스케줄 직후에만 보인다.
kubectl -n kcloud get pods -l npu.ai/acpp-verify-pod=true -w

# 판정의 근거는 AcceleratorEvidence 의 checks[] 에 남는다 — AllocationProbe 항목의
# passed/message(종료코드·마지막 로그 요약)를 본다.
kubectl get aev <tenstorrent-node> -o yaml
```

## 원복

```bash
kubectl delete -f test/live/descriptor-probe/avp-allocation-probe-worker3.yaml
helm upgrade kcloud-operator deploy/helm -n kcloud --reset-then-reuse-values \
  --set acpp.probeImages.tenstorrent=""
kubectl -n kcloud rollout status deploy/kcloud-operator
```

## run.sh

위 절차(켜기 → 관찰 → 원복)를 한 번에 실행하는 스크립트다. 대상 tenstorrent descriptor 가
클러스터에 이미 있고 `status.resolvedDevices` 가 <tenstorrent-node> 를 포함한다고 전제한다 —
descriptor·소비자 파드를 새로 만들지 않는다(그 데모는 `test/live/new-vendor-by-descriptor` 쪽 몫).

```bash
DRY_RUN=1 bash test/live/descriptor-probe/run.sh   # 클러스터를 건드리지 않고 명령만 확인
bash test/live/descriptor-probe/run.sh             # 실제 실행(비파괴, 끝나면 자동 원복)
```

주요 환경변수(기본값): `NODE=<tenstorrent-node>`, `VENDOR=tenstorrent`,
`RESOURCE=tenstorrent/blackhole-p150`, `DESC_NAME=new-vendor-tt-blackhole`,
`PROBE_IMAGE=<레지스트리>:5100/kcloud/probe-tenstorrent:v1`. 판정은 `AcceleratorEvidence` 의
`AllocationProbe` 체크가 `exit=0` 으로 통과하는지로 낸다 — probe Pod 관찰(`-w` 대응 부분)은
스케줄 즉시 사라지는 특성상 참고용(best-effort)일 뿐, PASS/FAIL 은 이 체크에 걸지 않는다. probe
Pod 가 자원 광고 0 으로 스케줄되지 못하거나 `tt-smi` 가 장치를 못 보면 이 체크가 실패로 남고,
그 사유를 근거(evidence 문서)에 BLOCKED(HARDWARE)로 기록한다.

라이브 Step 5·6 실행은 코드 수정 완료 후 일괄 수행.

## noop-sample.sh

`noop-sample.sh` 는 aev <tenstorrent-node> 의 resourceVersion·observedAt 을 15초 간격 6회 표본 떠
no-op 쓰기 게이트가 재조정에도 값을 그대로 두는지 확인하는 별도 스크립트다(3번째 표본 뒤 descriptor
annotate 로 재조정 1회 강제).
