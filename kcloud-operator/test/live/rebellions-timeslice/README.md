<!-- README.md: Rebellions 시분할 실 워크로드 동시 실행 하네스 | 생성일: 2026-09-25 | 수정일: 2026-10-11 -->

# Rebellions 시분할 실 워크로드 동시 실행 하네스

<rebellions-node> 의 Rebellions ATOM 한 장을 operator 의 descriptor 공유 경로로 SW 시분할(replicas 2)
했을 때, 실제 워크로드 두 개가 같은 장치에서 동시에 도는지, 처리량이 어떻게 나뉘는지를
라이브로 측정한다. 장치를 혼자 쓰는 solo 처리량과, 프로세스 두 개가 같은 장치(DEVICE_ID=0)를
동시에 쓰는 co-run 처리량을 비교한다.

Tenstorrent 선례(유휴 동시 open + 공유 데모로 올린 등급이 실 워크로드 co-run 실측에서
tt-metal UMD 의 KMD 락에 의한 직렬화로 다시 내려간 사례)에 따라, 같은 실 워크로드 실측을
Rebellions 에도 적용하는 것이 이 하네스다.

## 무엇을 측정하나

`workload.py` 는 torch `Linear(K, K)`(기본 K=2048)를 `rebel.compile_from_torch` 로 컴파일하고
`create_runtime(device=DEVICE_ID)` 으로 장치를 연 뒤, `DURATION` 초(기본 30초) 동안 그 런타임을
반복 실행해 초당 실행 횟수(iters_per_sec)를 낸다. 첫 실행은 장치 로드로 느리므로 워밍업 몇
회를 타이머 밖에서 소진하고 정상 상태 처리량만 잰다. 각 프로세스는 결과를 `RESULT {json}` 한
줄로 낸다.

`device_open_seconds`(create_runtime, 장치 open 소요)와 `init_seconds`(프로세스 시작→워밍업
종료), 이 초기화 시간 두 값은 반드시 따로 남긴다. Tenstorrent 에서 결정적 증거는 이 값이었다:
배타적 락이면 진 프로세스의 open 이 이긴 프로세스의 수명에 비례해 늘어난다. Rebellions 에서는
두 프로세스 모두 `device_open_seconds≈0.01s` 로, 직렬화 없이 동시에 장치를 연다.

## 같은 장치 고정

<rebellions-node> 는 ATOM 이 두 장(`/dev/rbln0`, `/dev/rbln1`)이다. 두 프로세스를 같은 장치에 고정하지
않으면 서로 다른 장치를 잡고 통과해 시분할을 전혀 증명하지 못한다. 두 가지 방법으로 함께 고정한다.

1. descriptor 의 `deviceNodes` 를 `/dev/rbln0`(물리 NPU)·`/dev/rsd0`(공유 제어 장치) 둘로
   고정한다. 그러면 두 소비자가 같은 물리 장치를 받는다. 두 장치 노드는 <rebellions-node> 의
   `/etc/cdi/rbln.yaml` 이 정의하는 벤더 CDI 컨테이너 편집과 같은 목록이다. 이 고정은 `pods`
   모드에서만 의미가 있고, `host` 모드는 descriptor 가 주입하는 장치 노드를 쓰지 않는다.
2. `workload.py` 가 `create_runtime(device=0)` 으로 0번을 명시적으로 연다.

측정 구간 중간에 `rbln-smi` 를 찍어 두 PID 가 NPU 0 에 컨텍스트를 올렸는지(같은 장치 공유),
NPU 0 만 사용률이 오르고 NPU 1 은 유휴인지 확인한다.

## 실행 순서

`run.sh` 는 다음을 차례로 한다.

1. preflight: <rebellions-node> Ready, NodeDeviceReport 의 rebellions 장치, `/dev/rbln0`, 서비스 존재 확인.
2. advertise: `rebellions.enabled=false` 로 벤더 device-plugin 을 잠시 회수하고, descriptor 를
   적용해 `rebellions/atom` 이 2개로 광고되는지 확인한 뒤, descriptor 를 거두고 벤더를 되살린다.
   광고가 켜진다는 것은 컨트롤러의 `sharingRefusal` 이 rebellions timeSlicing=Software 를
   받아들였다는 뜻이다. 실측의 전제는 아니므로 이 단계 실패는 측정을 막지 않는다.
3. measure: `MEASURE_MODE` 에 따라 둘 중 하나로 실측한다(아래 "파드 경로" 참고).
   - `host`(기본): <rebellions-node> 에서 `atomplus-vllm.service` 를 멈추고 장치가 free 인지 확인한 뒤,
     solo 1회 → co-run 2회(같은 DEVICE_ID, 같은 START_AT 배리어)로 `workload.py` 를 원격
     프로세스로 돌린다.
   - `pods`: 같은 절차를 descriptor 가 광고한 `rebellions/atom` 자원으로 스케줄한 파드
     3개(solo, corun-a, corun-b)로 돌린다.
4. verdict: 겹침을 먼저 보고, 겹쳤으면 동시 진행 여부를 판정한다. `pods` 모드는 표준출력
   마지막에 `TIMESLICE_PODS: solo=… a=… b=… sum=… overlap=… smi_pids=…` 한 줄을 더 찍는다.
5. cleanup: 파드 모드면 파드 3개와 ConfigMap 을 먼저 지운 뒤, descriptor 를 지우고 벤더를
   되살리고 서비스 복원을 확인한다. 실패·중단 시에도 `run.sh` 의 EXIT trap 이 정리를 맡는다
   (`host` 모드는 `measure-node.sh` 자체 trap 도 이중으로 서비스를 복원한다).

## 판정 기준

두 co-run 측정 구간의 겹침을 먼저 본다(`overlap ≥ DURATION 의 80%` 이고 양수). 겹치지 않으면
처리량을 비교하지 않고 `fail` 이다. 그 사실과 초기화 시간을 기록하는 것이 결과다.

겹쳤으면 동시 실행 성립을 본다: 두 프로세스가 모두 완주하고 각각 solo 의 0.3배 이상 처리량을
낸다(실질적 진행). 직렬화면 진 프로세스가 배리어를 놓쳐 `error` 로 죽거나 진행이 0 에
수렴한다(Tenstorrent 선례). <rebellions-node> 실측값은 solo 394.73 it/s, co-run 각각 약 0.92배,
합 약 1.83배(같은 장치인데 합이 solo 를 크게 넘음 = 진짜 동시 실행, 시분할 직렬화 아님)로
`verdict=pass` 다.

## 실행

```bash
# 라이브 실행, host 모드(기본값: <rebellions-node>, 30초, kubernetes-admin@cluster.local 컨텍스트)
bash test/live/rebellions-timeslice/run.sh

# 라이브 실행, pods 모드(descriptor 가 광고한 rebellions/atom 으로 파드 3개 스케줄)
MEASURE_MODE=pods bash test/live/rebellions-timeslice/run.sh

# 명령만 확인(클러스터·노드 미접근)
DRY_RUN=1 bash test/live/rebellions-timeslice/run.sh
DRY_RUN=1 MEASURE_MODE=pods bash test/live/rebellions-timeslice/run.sh

# 오프라인 재판정: 이전 실행이 남긴 solo/corun json 셋만으로 verdict 를 다시 낸다(클러스터·
# SSH 접근 없음). OUT 은 그 세 json(과 mode 를 담은 verdict.json)이 있는 디렉터리.
PHASE=verdict OUT=/tmp/rebellions-timeslice-evidence bash test/live/rebellions-timeslice/run.sh

# evidence 레코드 생성(라이브 실행 포함)
bash test/collect/evidence.sh --node <rebellions-node> --vendor rebellions \
  --command "bash test/live/rebellions-timeslice/run.sh" --expected "verdict=pass" \
  --mode sharing --backend devicePlugin --resource rebellions/atom \
  -o <결과 디렉터리>/<날짜>-rebellions-timeslice
```

주요 env: `KUBECTL_CONTEXT`, `NODE_SSH`(기본값은 `kubectl get node $NODE` 의 InternalIP다.
환경변수로 지정하면 그 값을 그대로 쓴다), `MEASURE_MODE`(host|pods, 기본 host), `DURATION`,
`BARRIER_SECONDS`, `MATMUL_DIM`, `DEVICE_ID`(기본 0), `SERVICE`(기본 atomplus-vllm.service),
`OUT`. `pods` 모드 전용: `CONSUMER_NS`(기본 default), `POD_WAIT_SECONDS`(기본 420),
`RBLN_NPU_NAME`(기본 RBLN-CA22), `CACHE_DIR`(기본 /home/kcloud/rbln-ts-cache). 산출물은 `$OUT`
에 각 phase 의 `.json`·`verdict.json`·`summary-<시각>.json` 으로 남는다(`host` 모드는
`measure.out` 도, `pods` 모드는 `corun-smi.txt` 도 남긴다).

## 파일

- `workload.py` 는 rebel 런타임으로 장치를 열고 반복 연산해 처리량·초기화 시간을 내는 워크로드다.
  `PHASE=compile-only` 로 호스트 사전 컴파일 진입점도 겸한다(아래 "파드 경로" 참조).
- `measure-node.sh` 는 <rebellions-node> 에서 서비스를 멈추고 solo·co-run 을 돌리는 원격 실행부다(자체 trap
  복원, `host` 모드 전용).
- `run.sh` 는 운영자 광고 확인, 실측(host 원격 프로세스 또는 pods) 오케스트레이션, 판정, 정리를
  수행한다.
- `descriptor.yaml` 은 `rebellions/atom` 을 `/dev/rbln0`·`/dev/rsd0` 둘로 시분할(replicas 2)
  광고하는 정의다.
- `pod-solo.yaml`·`pod-a.yaml`·`pod-b.yaml` 은 `pods` 모드의 소비자 파드 셋이다(아래 "파드
  경로" 참조).

## 파드 경로 (`MEASURE_MODE=pods`)

파드 경로는 이미지 저장소의 `rbln-workload:ubuntu22.04-v1`(호스트와 같은 Ubuntu 22.04 계열,
glibc 2.35) 이미지로 열려 있다 — `pods` 모드가 이 이미지로 같은 측정을 파드 3개(solo, corun-a,
corun-b)로 돌린다. 내부 python 이미지(Debian 13, glibc 2.41)는 네이티브 초기화 중 SIGSEGV 로
죽는다. `host` 모드(<rebellions-node> 호스트 프로세스로 직접 실행)가 기본값인 이유다.

파드는 <rebellions-node> 의 `/etc/cdi/rbln.yaml` 이 정의하는 벤더 CDI 컨테이너 편집을 범용 device-plugin
으로 재현해 받는다(각 장치 노드는 호스트 경로==컨테이너 경로로 주입된다).

| 종류 | 무엇 | 컨테이너 경로 | 근거 |
|---|---|---|---|
| 장치 노드 | 물리 NPU | `/dev/rbln0` | descriptor 가 광고·주입 |
| 장치 노드 | 공유 제어 장치 | `/dev/rsd0` | descriptor 가 광고·주입(빠지면 NPU 자동탐지 실패) |
| 라이브러리 | `librbln-thunk.so.3.0.0` | `/usr/lib/librbln-thunk.so.3.0.0`(읽기 전용) | 없으면 `create_runtime` 이 실패한다 |
| 라이브러리 | `librbln-ccl.so.3.0.0` | `/usr/lib/librbln-ccl.so.3.0.0`(읽기 전용) | 벤더 CDI 목록 |
| 라이브러리 | `librbln-ml.so` | `/usr/lib/librbln-ml.so`(읽기 전용) | 벤더 CDI 목록 |
| 도구 | `rbln-stat` | `/usr/bin/rbln-stat`(읽기 전용) | 벤더 CDI 목록 |
| 도구 | `rbln-smi` | `/usr/bin/rbln-smi`(읽기 전용) | 벤더 CDI 목록 |
| dist-packages | 호스트 site-packages 둘 | `/host-local`·`/host-lib`(읽기 전용) | `import rebel` 에 필요 |
| 사전 컴파일 캐시 | `/home/kcloud/rbln-ts-cache` | `/models`(읽기 전용) | 아래 "컴파일은 어디서" 참조 |

근거는 <rebellions-node> 라이브 실행이다 — 마운트 중 하나라도 빠지면 컨테이너 안 NPU 자동탐지·런타임
초기화가 실패한다.

컴파일은 파드 안이 아니라 호스트에서 한다. 이 CDI 마운트를 전부 갖춰도 파드 안
`compile_from_torch` 는 벤더가 실패 원인을 봉인해 둔 채(암호화된 진단 파일) 항상
실패한다.
그래서 기본 설계는 **호스트 사전 컴파일 + 파드 적재**다: `run.sh` 의 `precompile_host` 가
측정 전에 ssh 로 <rebellions-node> 에서 `workload.py` 를 `PHASE=compile-only` 로 한 번 실행해
`/home/kcloud/rbln-ts-cache/matmul-<MATMUL_DIM>-<RBLN_NPU_NAME>.rbln` 을 만들고(있으면
재사용, 컴파일은 CPU 측 20~60초), 세 파드는 `RBLN_COMPILED_PATH` 로 그 파일을
`rebel.RBLNCompiledModel` 로 적재만 한다(파드 안 컴파일 없음).

`RBLN_COMPILED_PATH`·`RBLN_NPU_NAME`·`MATMUL_DIM`·`DURATION`·`DEVICE_ID` 는 `run.sh` 가
`render_pod` 로 세 파드 매니페스트에 채운다(단일 출처는 `run.sh` 변수, 렌더 결과는
`pod-*.yaml.rendered.yaml` 이고 cleanup 이 지운다). 매니페스트에 적힌 값은 사람이 `kubectl
apply` 로 직접 쓸 때의 기본값이다. `MATMUL_DIM=1024 MEASURE_MODE=pods bash run.sh` 처럼
값을 바꾸면 사전 컴파일 파일 이름과 파드 env 가 함께 바뀐다. `render_test.sh` 가 이 렌더를
클러스터 없이 확인한다. tester 저장소의 데모 사본(`demo/b/manifests/17-*.yaml`)은 여전히
고정값이다. 그 데모는 이 하네스가 만든 `/home/kcloud/rbln-ts-cache/matmul-2048-RBLN-CA22.rbln`
을 전제하므로 기본값을 바꾸면 데모 사본도 함께 고쳐야 한다.

`RBLN_NPU_NAME` 은 `rebel.get_npu_name()` 자동탐지가 실패할 때만 쓰이는 폴백이며, 실제로는
자동탐지가 성공한다.

서비스 복원은 시작값 기준을 따른다. <rebellions-node> 의 `atomplus-vllm.service` 는 이 하네스가 아닌 다른
계정이 운영한다. `preflight` 가 기록한 시작값(`active`/`inactive`)이 복원 기준이고, 시작값이
`inactive` 였으면 측정이 끝나도 켜지 않는다(이 하네스가 멈춘 것만 다시 켠다). `host`·`pods`
두 모드 모두 이 규칙을 따른다. 측정 중 다른 계정이 이 서비스를 되살리면(예상과 다른 상태) 그
사실만 `service_state_drift` 로 기록하고 판정에는 반영하지 않는다.

노드 주소는 `NODE_SSH` 기본값인 `kubectl get node $NODE` 의 InternalIP 를 쓴다(고정 IP 를
하드코딩하지 않는다). 환경변수로 지정하면 그 값을 그대로 쓴다.

pods 모드의 판정 줄은 `compute_verdict` 끝에 `TIMESLICE_PODS: solo=<it/s> a=<비> b=<비>
sum=<비> overlap=<초> smi_pids=<n>` 한 줄을 더 찍는다(`jq` 가 있으면 `jq`, 없으면 python3
한 줄로 같은 줄을 낸다). `verdict.json` 은 `"mode":"host"|"pods"` 를 기록한다.

`PHASE=verdict OUT=<dir>` 로 이전 실행이 남긴 solo/corun json 셋만 다시 읽어 오프라인으로
판정한다(클러스터·SSH 접근 없음, `trap` 을 걸기 전에 끝낸다). `OUT` 의 `verdict.json` 에
있던 `mode` 값을 그대로 이어받는다.

이 경로가 변경하는 대상은 닫힌 목록이다: 자체 descriptor, <rebellions-node> 의 `atomplus-vllm.service`,
`default` 네임스페이스의 `rbln-ts-solo`/`rbln-ts-corun-a`/`rbln-ts-corun-b` 파드와
`rbln-ts-workload` ConfigMap, `/home/kcloud/rbln-ts-cache/` 아래 파일 생성이다. 그 밖의
노드·드라이버·펌웨어·재부팅은 변경하지 않는다.

## 한계와 안전

- 복제본 사이에 격리·QoS·메모리 상한이 없다. 두 프로세스는 같은 `/dev/rbln0` 을 쓰며, 동시
  진행은 장치가 여러 컨텍스트를 멀티플렉싱하기 때문이다(격리가 아니다).
- 측정 1회다. 반복 표본이 아니며, `atomplus-vllm.service` 를 멈추고 측정하므로 유휴 노드 상태를
  가정한다.
- 이 하네스는 자체 descriptor 와 NPUClusterPolicy 의 `rebellions.enabled` 필드, <rebellions-node> 의
  `atomplus-vllm.service` 를 변경한다(`pods` 모드는 여기에 더해 `rbln-ts-*` 파드 셋과
  `rbln-ts-workload` ConfigMap, `/home/kcloud/rbln-ts-cache/` 아래 파일 생성도 포함한다).
  다른 노드·드라이버·펌웨어·재부팅은 변경하지 않는다.
- <rebellions-node> 는 rebellions 워크로드 검증 노드다. 이 서비스는 다른 계정이 운영하므로, 측정 동안
  잠시 내려가더라도 복원은 시작값(`active`/`inactive`) 기준이다. 시작값이 `inactive` 였으면
  측정 뒤에도 켜지 않는다.
