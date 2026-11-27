<!-- README.md: Rebellions ATOM descriptor SW 시분할 라이브 데모 사용법 | 생성일: 2026-09-22 | 수정일: 2026-10-11 -->
# Rebellions ATOM descriptor SW 시분할 데모 (<rebellions-node>)

## 왜

<rebellions-node> 의 ATOM 카드가 유휴일 때(외부 holder `atomplus-vllm.service` 중지) 두 프로세스가 같은
`/dev/rbln0` 을 동시에 열 수 있음을 `test/live/concurrent-open/probe.sh` 가 hold 모드로
확인한다(confound 없음). 이 데모는
그 근거가 실제 광고로 이어지는지를 라이브로 보인다 — AcceleratorDescriptor `spec.sharing` 하나로
device-plugin 코드 없이 `rebellions/atom` 을 replicas 개로 광고하고, 소비자 파드 둘이 같은 장치를
나눠 여는 것까지 확인한다.

`test/live/new-vendor-by-descriptor/` 와 절차가 같다(벤더 끄기 → descriptor 광고 → 소비 → 복원).
다른 점은 (1) sharing timeSliced replicas 2 로 **한 장치를 둘로** 광고하고, (2) `deviceNodes` 를
`/dev/rbln0` 하나로 고정해 두 소비자가 **같은 장치**를 받으며, (3) 소비자가 python3 로 실제
`os.open(O_RDWR)` 해 OPEN_OK 를 찍어 동시 open 을 로그로 확인한다는 것이다.

## 전제

- kubectl context 가 <rebellions-node> 가 속한 클러스터(kubernetes-admin@cluster.local).
- NPUClusterPolicy `kcloud/npuclusterpolicy-sample` 의 `spec.rebellions.enabled=true`,
  `advertiseBy=devicePlugin`, namespace `kube-system`(관리 device-plugin `rbln-device-plugin`).
- <rebellions-node> 의 `atomplus-vllm.service` 가 중지(inactive/disabled)돼 카드가 유휴.
- **레지스트리 `internal/vendorcap/registry.go` 의 rebellions `timeSlicing` 이 `Software`** 이고
  그 값을 담은 operator 가 배포돼 있어야 컨트롤러(`sharingRefusal`)가 sharing 광고를 켠다.
  `Unknown` 인 operator 가 돌면 `descriptor.status.refusals` 에 sharing 거절이 남고 광고가
  뜨지 않는다 — 이때 데모는 `descriptor_advertised_n` FAIL 로 그 사실을 기록하고 복원한다.

## 실행

```bash
# 먼저 명령만 확인(클러스터 미접근)
DRY_RUN=1 bash test/live/rebellions-sharing-demo/run.sh

# 라이브
bash test/live/rebellions-sharing-demo/run.sh
```

절차: `rebellions.enabled=false`(rbln-device-plugin 회수) → descriptor 적용(생성 device-plugin 이
`rebellions/atom` 을 2로 광고) → 소비자 2개가 각각 1개를 받아 `/dev/rbln0` 을 열고 OPEN_OK →
파드·descriptor 제거 → 생성물 GC → `rebellions.enabled=true` 복원(원래 device-plugin·`rebellions.ai/ATOM: 2`).
**cleanup 은 실패·중단 시에도 trap 으로 실행**돼 NPUClusterPolicy 를 원값으로 되돌린다.

변경하는 것: NPUClusterPolicy 의 `spec.rebellions.enabled`, 자체 descriptor `rebellions-atom-share-demo`,
소비자 파드 두 개뿐. 다른 노드·워크로드·Helm 값·드라이버 정책에는 손대지 않는다. 재부팅하지 않는다.

## 판정

JSON 요약 한 줄과 검사 항목:

| 검사 | 통과 조건 |
|---|---|
| `start_vendor_advertised` | 시작 시 `rebellions.ai/ATOM` 광고 중 |
| `ncp_ds_deleted_on_disable` | `enabled=false` 후 `rbln-device-plugin` 회수 |
| `descriptor_advertised_n` | 생성 device-plugin 이 `rebellions/atom` 을 replicas(2) 로 광고 |
| `both_consumers_running` | 소비자 2개 동시 Running |
| `both_open_ok` | 두 파드 로그 모두 `OPEN_OK`(같은 `/dev/rbln0`) |
| `generated_dp_gc` | descriptor 삭제 후 생성물 회수 |
| `restored_vendor_dp` | `enabled=true` 복원 후 `rbln-device-plugin` Running·`rebellions.ai/ATOM` 광고 |

환경변수로 노드·리소스명·대기 상한을 바꿀 수 있다(스크립트 상단 참조). 소비자 image 는 내부
레지스트리의 python3 포함 이미지(`<레지스트리>:5100/kcloud/probe-tenstorrent:v1`)를 쓴다.

## 한계

- open 성공/실패만 본다 — 동시 추론 처리량·지연·격리는 측정하지 않는다(격리 없는 SW 시분할).
- 두 소비자가 같은 `/dev/rbln0` 을 받는 것은 `deviceNodes` 를 한 경로로 고정했기 때문이다 —
  물리 장치가 하나라 replicas 광고분이 모두 같은 노드를 가리킨다.
- operator 레지스트리가 `Software` 가 아니면 광고 단계에서 멈춘다(위 전제 참조).
