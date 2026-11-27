<!-- README.md: NVIDIA ACPP 시분할 적용·해제로 kcloud_acpp_apply_total 라이브 표본을 남기는
     하네스 사용법 | 생성일: 2026-09-23 | 수정일: 2026-10-11 -->

# NVIDIA ACPP 지표 표본 하네스 (<nvidia-node>)

## 왜

`kcloud_acpp_apply_total{vendor,mode,result}` (`result` = ready|disabled|rejected|rollback)는
`internal/metrics/operator.go` 에 정의돼 있고, 훅은 `internal/controller/acpp_sharing.go` 의
`sharingReady`(→ready)와 `runSharingOnly`/`runSharing`의 exclusive 분기(→disabled)에 있다(단위
테스트로는 확인됐으나 라이브 표본은 없다). 이 하네스는 NVIDIA A30 를 가진 <nvidia-node> 에
layout 없는 순수 공유(sharing-only) `AcceleratorPartitionPolicy` 를 적용·해제해 `ready`·`disabled`
두 result 를 실제로 한 번씩 찍고, 스크레이프 방식(`kubectl port-forward` + `curl`)으로
그 증가를 라이브 확인한다.

## 승인

**<nvidia-node> 라이브 실행은 승인 대기(AWAITING USER)다.** 적용·patch·삭제 세 시점 모두 <nvidia-node> 의
NVIDIA device-plugin(`kube-system/nvidia-device-plugin-flat`)이 재시작되고 `nvidia.com/gpu` 광고가
바뀐다. 그 순간 그 노드의 GPU 를 요청하는 새 파드는 스케줄되지 못한다. 승인자는 다음을 확인하고
허가한다:

- <nvidia-node> 에 지금 `nvidia.com/gpu` 를 요청 중인 파드가 없다(하네스 preflight 가 재확인하고,
  있으면 BLOCKED 로 중단한다).
- device-plugin 재시작 3회(적용 1·해제 1·삭제 후 원복 1) 동안 그 노드의 GPU 스케줄링이 일시
  불가한 것을 감수한다.
- operator 자체가 `metrics.enabled=true`(helm upgrade, 롤링 재시작 1회)로 한 번 더 실행되고, 끝에 다시
  꺼진다(재시작 1회 더). 그동안 조정 루프가 잠깐 멈춘다.

승인 전까지는 `DRY_RUN=1` 로만 실행한다(아래 실행 참고). 이 문서·스크립트·매니페스트 자체는
클러스터에 아무 것도 적용하지 않는다.

## 전제

- kubectl context `kubernetes-admin@cluster.local`. 네임스페이스 `kcloud`, helm 릴리스
  `kcloud-operator`.
- <nvidia-node> 에 NVIDIA A30, device-plugin `kube-system/nvidia-device-plugin-flat`
  (label `kcloud.ai/dp-vendor=nvidia`)이 Running.
- 실행 전 클러스터에 `AcceleratorPartitionPolicy`/`AcceleratorDescriptor` 가 없어야 한다(현재
  둘 다 0개 확인됨). preflight 가 동명 ACPP 잔존을 재확인하고, 있으면 BLOCKED 로 멈춘다.
- `metrics.enabled` 은 기본 꺼짐(values.yaml)이다. 하네스가 직접 helm upgrade 로 켜고 끝에
  preflight 가 저장한 `helm get values` 기준선으로 되돌린다 — 사용자가 미리 켜 둘 필요는 없다.
- 실행 호스트에 `kubectl`, `helm`, `curl`, `python3`(PyYAML)이 있어야 한다.

## 실행

저장소 루트에서 실행한다. 산출물은 `$OUT`(기본 `/tmp/nvidia-acpp-metrics-evidence`)에 남고,
끝에 JSON 요약 한 줄이 `$OUT/summary-<시각>.json` 에 append 된다.

```bash
# 승인 전: 명령만 확인(클러스터에 mutating 호출 없음). preflight 의 읽기 전용 조회는
# DRY_RUN 과 무관하게 실제로 실행돼 baseline allocatable·helm values·image tag 를 기록한다.
DRY_RUN=1 bash test/live/nvidia-acpp-metrics/run.sh

# 승인 후: 라이브 실행
bash test/live/nvidia-acpp-metrics/run.sh
```

절차: (1) preflight — context·노드·ACPP 잔존 0·<nvidia-node> 의 GPU 요청 파드 0 확인, baseline
allocatable·`helm get values`·현재 `image.tag` 기록(빈 파일이면 BLOCKED) (2) `metrics.enabled=true,
metrics.service.enabled=true` 로 helm upgrade(`--reset-then-reuse-values`) (3) operator pod 로 포트포워드
(`18083:8080`) 열고 baseline 스크레이프 (4) `acpp-timesliced.yaml` 적용 → `status.phase=Ready`
대기(최대 10분) → 스크레이프 → `mode="timeSliced",result="ready"` 증가 확인 (5)
`spec.sharing`을 `{mode: exclusive, timeSlicing: null}` 로 merge patch → `status.phase=Failed`
대기(최대 5분) → 스크레이프 → `mode="exclusive",result="disabled"` 증가 확인 (6) ACPP 삭제 →
device-plugin 전부 Running·allocatable 원값 복원 대기(최대 5분) (7) helm upgrade
`--reset-values -f <baseline> --set image.tag=<기록값>` 으로 지표 스위치 원복 → 마지막 스크레이프
시도(응답 없음이 정상).

**cleanup 은 실패·중단 시에도 EXIT 트랩으로 실행**돼 (best-effort) exclusive patch → ACPP 삭제 →
device-plugin 복원 확인 → metrics 스위치 원복을 시도한다.

변경하는 것: 자체 ACPP `nvidia-timesliced-metric-probe`, operator 배포의 `metrics.enabled`/
`metrics.service.enabled`(끝에 원복). 다른 노드·워크로드·드라이버 정책은 수정하지 않는다.
재부팅하지 않는다.

## 판정

JSON 요약 한 줄과 검사 항목(단계순):

| 단계 | 검사 | 통과 조건 |
|---|---|---|
| preflight | `context_reachable`·`node_present`·`acpp_not_preexisting`·`zero_gpu_requesting_pods_on_node` | 클러스터 전제 충족 |
| preflight | `baseline_allocatable_recorded`·`operator_pod_found`·`deployed_image_tag_recorded` | 원복에 쓸 값 확보 |
| preflight | `helm_values_baseline_saved` | `helm get values` 저장 파일이 비어 있지 않음(비면 BLOCKED) |
| demo | `port_forward_reachable` | 지표 스위치 on 후 20초 내 `:18083/metrics` 응답 |
| demo | `acpp_ready_reached` | 적용 후 `status.phase=Ready` 도달(최대 10분) |
| demo | **`acpp_apply_total_ready_increased`** | `kcloud_acpp_apply_total{vendor="nvidia",mode="timeSliced",result="ready"}` 가 baseline 대비 ≥1 증가 — 이 하네스의 핵심 표본 |
| demo | `acpp_disable_reached` | exclusive patch 후 `status.phase=Failed` 도달(최대 5분) |
| demo | **`acpp_apply_total_disabled_increased`** | `kcloud_acpp_apply_total{vendor="nvidia",mode="exclusive",result="disabled"}` 가 직전 스크레이프 대비 ≥1 증가 — 두 번째 핵심 표본 |
| cleanup | `device_plugin_and_allocatable_restored` | 삭제 후 device-plugin 전부 Running·`nvidia.com/gpu` allocatable 이 preflight 값과 일치(최대 5분) |
| cleanup | `metrics_endpoint_closed_after_restore` | 지표 스위치 원복 후 `:18083/metrics` 무응답(재부팅 없이 스위치만 다시 꺼졌다는 확인) |

전체 verdict: `DRY_RUN=1` 이면 항상 `DRY`(개별 검사가 FAIL 이어도 exit 0). 스크레이프가
DRY_RUN 에서는 빈 파일이라 두 핵심 증가 검사는 항상 FAIL 로 찍힌다, 이는 알려진 특성이고 실패가
아니다. 실제 실행은 `BLOCKED`(preflight 중단)·`PASS`(전 검사 통과)·`FAIL`(하나 이상 실패, exit 1)
중 하나.

환경변수로 노드·타임아웃·포트·경로를 바꿀 수 있다(스크립트 상단 참조: `NODE`, `T_READY`,
`T_DISABLED`, `T_RESTORE`, `LOCAL_PORT`, `METRICS_PORT`, `OUT`, `VALUES_BASE` 등).

## 한계

- 적용 범위는 device-plugin ConfigMap/DaemonSet 전체(클러스터 전역)이지만, 재시작·검증은
  `nodeSelector` 가 가리키는 <nvidia-node> 만 본다. 다른 nvidia 노드가 있다면 그 노드의
  device-plugin 은 재시작되지 않아 새 ConfigMap 을 당장 반영하지 않는다.
  `acpp-timesliced.yaml` 의 `spec.nodeSelector`(`kubernetes.io/hostname`)가 가리키는
  `spec.nodeSelector` 를 바꾸면 대상 노드를 옮길 수 있다.
- `result="rejected"`·`result="rollback"` 은 이 하네스가 만들지 않는다(각각 잘못된 요청·verify
  미수렴 경로라 표본이 다르다). 그 경로는 다른 작업에서 다룬다.
- `metric_value` 파서는 Prometheus 텍스트의 레이블 순서에 의존하지 않지만, 멀티라인 HELP/TYPE
  주석은 건너뛰고 데이터 라인만 정규식으로 읽는다. exposition 포맷이 크게 바뀌면(예: exemplar
  추가) 재검토가 필요하다.
