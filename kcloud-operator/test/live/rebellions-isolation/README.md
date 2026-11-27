<!-- README.md: Rebellions ATOM 격리 4축(연산·용량·대역폭·장애) 라이브 실측 하네스 | 생성일: 2026-09-28 | 수정일: 2026-10-11 -->

# Rebellions ATOM 격리 4축 실측 하네스

<rebellions-node> 의 Rebellions ATOM 두 장(장치 0=`0000:c3:00.0`, 장치 1=`0000:c4:00.0`)에 각각 프로세스
하나씩을 붙여 "장치 1 의 부하가 장치 0 으로 새는가"를 잰다. `rebellions-timeslice/`(같은 장치를
두 프로세스가 시분할로 공유하는지)와는 다른 물음이다 — 여기서는 **서로 다른 물리 장치** 사이의
간섭만 본다.

기준은 능력 모델의 확인 수준 분리와 기존 격리 실측(NVIDIA MIG·Furiosa RNGD)이 쓴 형식을
따른다: 연산·용량·대역폭·장애 네 축, 비율 기준 축(연산·대역폭)은 `≥0.95` 를 격리로 판정한다.

## 네 축이 재는 것

| 축 | 방법 | 판정 |
|---|---|---|
| 연산(compute) | 장치 0 solo iters/s 대 장치 1 최대 부하(동시 matmul) 중 장치 0 iters/s | 비율 ≥0.95 → 격리, 미만이면 `compute=<비율>` 로 그대로 적는다 |
| 용량(capacity) | 장치 1 이 K-chain(Linear+ReLU 32겹, 가중치 512MiB)으로 큰 텐서를 쥔 채 장치 0 도 같은 크기로 성공하는지 + `rbln-smi` 메모리 열 스냅샷 | 둘 다 성공(에러 없음)이면 `same`, 아니면 `shrinks` |
| 대역폭(bandwidth) | `OP=scale`(원소별 곱)로 계산을 무시할 만큼 가볍게 해 처리시간을 호스트↔장치 전송이 지배하게 한 뒤 solo 대 co-run 처리량 | 비율 ≥0.95 → 격리, 미만이면 `bandwidth=<비율>` 로 그대로 적는다 |
| 장애(fault) | 장치 1 프로세스를 `kill -9` 하고 이어 형상 불일치 입력으로 실패시키는 동안 장치 0 이 끊김 없이 완주하는지, 사후 `rbln-smi` 가 정상인지 | 끊김 없음+사후 정상 → `contained`, 아니면 `leaks` |

`ponytail`: 용량 축은 최대 할당 가능량을 찾는 이분 탐색을 하지 않는다. 고정 크기(K=32,
dim=2048 — 컴파일 소요가 3.73초로 확인된 크기) 한 점만 잰다. 두 NPU 는 `rbln-smi` 에 별도
`Memory(used/total)` 행으로 잡혀 있어 공유 풀일
가능성이 낮고, 이 하네스의 물음은 "같은 크기가 co-run 에서도 성공하는가" 이지 상한을 정밀히
재는 것이 아니다. 상한을 더 정밀히 재려면 `CAP_K`/`CAP_DIM` 을 올려 재실행한다.

## 실행 순서 (`run.sh`)

1. **preflight**: <rebellions-node> Ready, `NodeDeviceReport` 에 rebellions 장치 2개, `/dev/rbln0`·
   `/dev/rbln1` 존재, 안전 게이트(이 노드에서 `rebellions/atom` 을 요구하는 파드 0개),
   디스크 여유(`df -h /`, 기본 5GiB 이상) 확인.
2. **measure**: `workload.py`·`measure-node.sh` 를 <rebellions-node> 에 `scp` 하고 `measure-node.sh` 를
   `ssh` 로 실행 — `atomplus-vllm.service` 를 멈추고 장치가 free 인지 확인한 뒤 네 축을
   순서대로 돈다(각 축 solo 1회·co-run 1회, 장애 축은 1회). 축별 결과를 `RUN_DIR`(로컬,
   기본 `./​.run/`)에 `<축>.json` 으로 저장한다.
3. **verdict**: 저장된 축별 json 만 읽어 기계 줄 `ISOLATION: compute=<비율> capacity=<same|
   shrinks> bandwidth=<비율> fault=<contained|leaks>` 를 낸다(`verdict.json` 도 함께 남김).
4. **cleanup**: `atomplus-vllm.service` 복원과 <rebellions-node> 산출물 디렉터리(`/home/kcloud/
   rebellions-isolation-<RUN_ID>/`) 삭제를 이중 확인한다 — `measure-node.sh` 자체 트랩이
   이미 하고, `run.sh` 가 한 번 더 본다.

## `PHASE=verdict` — 라이브 접근 없이 재판정

`RUN_DIR` 에 이미 축별 결과가 있으면(이전 라이브 실행이 남긴 것) 장치·클러스터에 다시
접근하지 않고 그 파일만 읽어 `ISOLATION:` 한 줄을 다시 낼 수 있다. `test/collect/evidence.sh`
의 `--command` 로 이 모드를 쓴다 — evidence 레코드를 나중에 다시 검증할 때 <rebellions-node> 를 다시
건드리지 않는다.

```bash
PHASE=verdict RUN_DIR=/absolute/path/to/.run bash test/live/rebellions-isolation/run.sh
```

## 실행

```bash
# 라이브 실행(기본값: <rebellions-node>, device0=0/device1=1, kubernetes-admin@cluster.local 컨텍스트)
bash test/live/rebellions-isolation/run.sh

# 명령만 확인(클러스터·노드 미접근)
DRY_RUN=1 bash test/live/rebellions-isolation/run.sh

# evidence 레코드 생성(라이브 재접근 없이 RUN_DIR 재판정)
bash test/collect/evidence.sh --node <rebellions-node> --vendor rebellions \
  --command "RUN_DIR=$(pwd)/test/live/rebellions-isolation/.run PHASE=verdict bash test/live/rebellions-isolation/run.sh" \
  --expected "ISOLATION: compute=<실측값> capacity=<실측값> bandwidth=<실측값> fault=<실측값>" \
  --id-suffix isolation \
  -o <결과 디렉터리>/<날짜>-rebellions-isolation
```

주요 env: `KUBECTL_CONTEXT`, `NODE_SSH`(기본 <노드 IP>), `DEV0`/`DEV1`(기본 0/1),
`DUR`(연산·대역폭 축 타이머, 기본 20초), `BARRIER`(기본 40초), `CAP_DUR`/`CAP_BARRIER`/
`CAP_K`/`CAP_DIM`(용량 축, 기본 15초/60초/32/2048), `BW_DIM`(대역폭 축, 기본 4096),
`FAULT_DUR`(기본 30초), `RUN_ID`, `RUN_DIR`(기본 `test/live/rebellions-isolation/.run`),
`T_MEASURE`(원격 실행 전체 상한, 기본 900초). 산출물은 `$RUN_DIR` 에 축별 `.json`·
`measure.out`·`verdict.json`·`isolation-line.txt`·`summary-<시각>.json` 으로 남는다(`.run/`
은 `.gitignore` 대상).

## 파일

- `workload.py` — `rebel` 런타임으로 장치를 열고 반복 실행해 처리량·초기화 시간을 내는
  워크로드. `OP=matmul`(연산·장애 축)·`OP=chain`(용량 축, `CHAIN_K` 겹 Linear+ReLU)·
  `OP=scale`(대역폭 축, 원소별 곱)와 `BAD_SHAPE=1`(장애 축의 잘못된 입력 단계)을 지원한다.
- `measure-node.sh` — <rebellions-node> 에서 서비스를 멈추고 네 축 solo·co-run 을 순서대로 돌리는
  원격 실행부. 컴파일 소요가 불확실한 용량 축은 고정 `sleep` 대신 워크로드가 찍는
  `PHASE_READY` 줄을 기다렸다가 `rbln-smi` 를 찍는다(`wait_ready`). 장애 축은 `kill -9`
  대상을 래퍼 서브셸이 아니라 실제 `python3` 워커 PID(pidfile 로 전달)로 정확히 지정한다.
  자체 트랩이 `SERVICE` 복원과 `OUT` 디렉터리 삭제를 함께 한다.
- `run.sh` — preflight + 원격 측정 오케스트레이션 + 판정(`PHASE=verdict` 겸용) + 정리.

## 이미지 요건 (파드 경로의 한계)

`rebellions-timeslice/README.md` 와 같다 — 벤더 런타임 `rebel` 은 <rebellions-node> 호스트에만 설치돼
있고, 든 컨테이너 이미지가 없다(있는 Debian 기반 이미지는 `create_runtime` 에서 glibc ABI
불일치로 SIGSEGV). 그래서 이 하네스도 <rebellions-node> 호스트 프로세스로 측정한다 — 장치 경합은 파드든
호스트 프로세스든 `/dev/rbln*` + KMD 수준에서 같다.

## 한계와 안전

- 측정 1회다. 반복 표본이 아니며, `atomplus-vllm.service` 를 멈추고 측정하므로 유휴 노드
  상태를 가정한다.
- 용량 축은 고정 크기 한 점만 잰다(위 ponytail 메모 참고) — 장치 상한(15.7GiB)에 가까운
  큰 할당에서도 같은 결론인지는 확인하지 않는다.
- 연산·대역폭이 격리 기준(≥0.95)에 못 미쳐도 그 원인(PCIe 대역폭 공유 대 전력·발열 예산
  공유 대 호스트 CPU 디스패치 경합)은 이 하네스가 특정하지 않는다 — 새는지 여부만 잰다.
- 이 하네스는 자체가 띄운 `workload.py` 프로세스와 <rebellions-node> 의 `atomplus-vllm.service` 만
  변경한다. 다른 노드·드라이버·펌웨어·재부팅은 변경하지 않는다.
- <rebellions-node> 는 rebellions 워크로드 검증 노드다. 측정 동안 서비스가 잠시 내려갔다 복원된다.
