<!-- README.md: Tenstorrent 시분할 실 워크로드 처리량 하네스 | 생성일: 2026-09-22 | 수정일: 2026-10-11 -->

# Tenstorrent 시분할 실 워크로드 처리량 하네스

<tenstorrent-node> 의 Tenstorrent Blackhole p150 한 장을 operator 의 descriptor 공유 경로로
SW 시분할(replicas 2)했을 때, 실제 워크로드 처리량이 어떻게 나뉘는지 라이브로 측정한다.
장치를 혼자 쓰는 solo 처리량과, 파드 두 개가 같은 장치를 동시에 쓰는 co-run 처리량을
비교해 시분할이 처리량을 직렬화(각 파드 약 0.5배)하는지 확인한다.

## 무엇을 측정하나

`workload.py` 는 `ttnn.open_device` 로 장치를 열고 bf16 2048×2048 텐서 두 개를 장치에 올린
뒤, `ttnn.matmul` 을 `DURATION` 초(기본 60초) 동안 반복해 초당 실행 횟수(iters_per_sec)를
낸다. 첫 matmul 은 커널 컴파일로 느리므로 워밍업 몇 회를 타이머 밖에서 소진하고 정상 상태
처리량만 잰다. 각 파드는 결과를 `RESULT {json}` 한 줄과 `metric=matmul_per_s value=<수>` 한
줄로 stdout 에 찍는다.

## 실행 순서

`run.sh` 는 다음을 차례로 수행한다.

1. preflight: NPUClusterPolicy 존재와 `tenstorrent.enabled=true`, hugepages-1Gi 광고를 확인한다.
2. prepull: 파생 이미지를 타이밍 전에 <tenstorrent-node> 에 미리 내려받는다.
3. setup: `tenstorrent.enabled=false` 로 벤더 device-plugin 을 회수하고, `workload.py` 를 담은
   ConfigMap 을 만들고, descriptor 를 적용해 `tenstorrent/blackhole-p150` 이 2개로 광고되기를
   기다린다.
4. measure: solo 파드 한 개 → co-run 파드 두 개(같은 장치) → solo 파드 한 개 순으로 실행한다.
   solo 를 앞뒤로 두 번 재 클럭 드리프트를 상쇄한다. co-run 두 파드는 START_AT 배리어로 측정 창을
   정렬해 같은 시각에 타이머를 시작한다(아래 "co-run 시작 배리어").
5. verdict: 네 측정으로 판정을 계산한다.
6. cleanup: 파드·descriptor·ConfigMap 을 지우고 `tenstorrent.enabled=true` 로 복원한 뒤,
   벤더 device-plugin 재기동과 벤더 리소스 재광고, NodeDeviceReport 의 `validation.passed=true`
   를 확인한다. 실패·중단 시에도 EXIT trap 이 복원을 보장한다.

## 판정 기준

두 solo 측정의 평균을 solo 기준으로 삼는다. 다음을 모두 만족하면 `verdict=pass` 다.

- co-run 각 파드의 처리량이 solo 평균의 0.35~0.65배(직렬화로 약 0.5배).
- 두 co-run 처리량의 합이 solo 평균의 1.1배 이하(같은 장치라 합쳐도 늘지 않음).
- 두 co-run 파드 모두 완주.
- 두 co-run 파드의 start/end 타임스탬프가 겹침(동시 실행 입증, 겹침이 DURATION 의 80% 이상이고
  양수).

하나라도 어긋나면 `verdict=fail` 이고 수치를 함께 남긴다. 파드가 기동하지 못하거나 ttnn 이
예외를 던지면 `verdict=blocked` 으로 분류한다(장치 오류는 HARDWARE, 그 밖의 소프트웨어·런타임
문제는 ENVIRONMENT).

## 실행

```bash
# 라이브 실행(기본값: <tenstorrent-node>, 60초, kubernetes-admin@cluster.local 컨텍스트)
bash test/live/tt-timeslice-throughput/run.sh

# 명령만 확인(클러스터 미접근)
DRY_RUN=1 bash test/live/tt-timeslice-throughput/run.sh

# 짧은 스모크(예: 15초)
DURATION=15 bash test/live/tt-timeslice-throughput/run.sh
```

주요 env: `KUBECTL_CONTEXT`(기본 `kubernetes-admin@cluster.local`), `NODE`, `DURATION`,
`IMAGE`, `BARRIER_SECONDS`(co-run 시작 배리어, 기본 180), `OUT`(증거 출력 디렉터리). 산출물은
`$OUT` 에 각 phase 의 `.json`·`.log`, `verdict.json`, `summary-<시각>.json` 으로 남는다.

## co-run 시작 배리어

co-run 두 파드는 스케줄·기동·`ttnn.open_device`·워밍업에 걸리는 시간이 서로 달라, 배리어 없이는
한 파드가 타이머를 끝낸 뒤에야 다른 파드가 시작해 측정 창이 겹치지 않는다. 그러면 두 값은 사실상
solo 를 두 번 잰 것이라 경합을 측정하지 못한다. 이를 막으려고 `run.sh` 는 co-run 적용 직전에
`START_AT = now + BARRIER_SECONDS`(epoch 초)를 계산해 두 파드에 같은 값을 넘긴다.

- `workload.py` 는 `START_AT` 이 있으면 `open_device` 와 워밍업까지 마친 뒤 그 시각까지 대기했다가
  타이머를 시작한다. 두 파드가 같은 시각에 시작하므로 측정 창이 거의 완전히 겹친다.
- `START_AT` 이 이미 5초 넘게 지났으면 파드는 어긋난 창을 재지 않고 `error` 를 담은 RESULT 를 찍고
  exit 1 로 실패한다.
- **이 카드에서 배리어 초과는 튜닝 문제가 아니다 — `BARRIER_SECONDS` 를 키우지 말 것.** tt-metal 의
  user-mode driver 가 `/dev/tenstorrent/0` 핸들에 KMD 락을 잡고 프로세스 수명 동안 유지한다(로그:
  `Waiting for KMD lock ... currently held by another handle`). 경쟁에서 이긴 파드가 락을 쥔 채
  배리어에서 대기하는 동안 진 파드는 그 락을 기다리므로, 진 파드의 init 이
  `배리어 + DURATION + 이긴 파드 init` 이 된다(실측 251/490/971초 @ 배리어 180/420/900초). 배리어를
  키우면 마감 시각이 진 파드 지연만큼 그대로 밀려 두 파드는 영영 겹치지 못한다.
- co-run 처리량을 재려면 장치를 배타적으로 잡지 않는 런타임이 필요하다. tt-metal 워크로드로는 같은
  카드 동시 사용이 불가하며, 현재 그런 대체 경로는 없다.
- solo 파드는 `START_AT` 을 받지 않아 배리어 없이 즉시 시작한다.
- 겹침은 사후에 verdict 가 다시 검증한다(두 co-run 창의 겹침이 `DURATION` 의 80% 이상).

## 이미지와 버전 선택

`Dockerfile` 은 베이스 `tt-metalium` 이미지에 CPU 전용 torch 2.2.1 을 더한 파생(`-torch`) 이미지를
만든다. 베이스의 ttnn 은 torch 를 무조건 import 하므로 torch 없이는 `import ttnn` 가 실패하기
때문이다. 장치 연산은 ttnn 이 tt-metal 로 수행하고 torch 는 import 의존성만 충족한다. torch 는
ttnn 이 설치된 venv(`/opt/venv`)의 site-packages 에 넣는다 — venv 에 pip 모듈이 없는 베이스에서도
되도록 venv python3 로 시스템 pip 을 빌려(PYTHONPATH) 설치한다.

베이스 태그는 `BASE_IMAGE` build-arg 로 고른다. **카드 펌웨어(플래시 번들)와 맞는 tt-metal 런타임을
써야 초기화가 성공한다** — 런타임이 펌웨어보다 크게 오래되면 Blackhole 초기화 중 abort 한다
(v0.58.0-rc4 가 그 예다). 카드 펌웨어는 `tt-smi -s` 의 `FLASH_BUNDLE_VERSION` 으로 확인하고, 그에
맞는 최신 태그는 GHCR `ghcr.io/tenstorrent/tt-metal/tt-metalium-ubuntu-22.04-release-amd64` 에서
고른다. 번들 19.6.0.0(TT-KMD 2.11.0) 에는 v0.79.0 이 맞는다.

```bash
TAG=v0.79.0
GHCR=ghcr.io/tenstorrent/tt-metal/tt-metalium-ubuntu-22.04-release-amd64
LOCAL=<레지스트리>:5100/kcloud/tenstorrent/tt-metalium
# 1) GHCR 태그를 로컬 레지스트리에 미러
sudo docker pull "$GHCR:$TAG"
sudo docker tag  "$GHCR:$TAG" "$LOCAL:$TAG"
sudo docker push "$LOCAL:$TAG"
# 2) 파생(-torch) 이미지 빌드·push
sudo docker build --build-arg BASE_IMAGE="$LOCAL:$TAG" -t "$LOCAL:$TAG-torch" \
  test/live/tt-timeslice-throughput/
sudo docker push "$LOCAL:$TAG-torch"
# 3) import 확인(exit 0 이어야 라이브 실행)
sudo docker run --rm --entrypoint python3 "$LOCAL:$TAG-torch" -c 'import ttnn'
# 4) 라이브 실행
DURATION=60 IMAGE="$LOCAL:$TAG-torch" bash test/live/tt-timeslice-throughput/run.sh
```

`BASE_IMAGE` 기본값은 `tt-metalium:v0.58.0-rc4` 이며, 파드 매니페스트의 이미지는 `run.sh` 가
`IMAGE=` 로 덮어쓴다.

## 한계와 안전

- 복제본 사이에 격리·QoS·메모리 상한이 없다. 두 파드는 같은 `/dev/tenstorrent/0` 을 쓰며,
  처리량 반감은 격리가 아니라 설계상 직렬화(시분할)다.
- 이 하네스는 자체 descriptor·ConfigMap·소비자 파드와 NPUClusterPolicy 의
  `tenstorrent.enabled` 필드만 변경한다. DriverInstallPolicy·DriverUpgradeState·Helm 값·다른
  노드에는 손대지 않고 재부팅·펌웨어 변경도 하지 않는다.
- 전환 동안 tenstorrent device-plugin 이 잠시 내려갔다 복원되므로, tenstorrent 장치를 쓰는
  워크로드가 없는 검증 노드(<tenstorrent-node>)에서만 실행한다.
