<!-- README.md: kcloud-operator K8s 리소스 설치량(footprint) 측정 도구 안내 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->

# 리소스 설치량(footprint) 측정

kcloud-operator 가 클러스터에 얼마만큼의 K8s 리소스를 설치하는지 재현 가능하게
측정한다. 두 가지를 함께 잰다. 정적 측정은 `helm template` 으로 렌더한 매니페스트에서
kind 별 개수를 세고, 라이브 측정은 클러스터에 실제로 설치된 구성요소의 파드 수·요청량·
실사용량을 읽는다. 결과는 JSON 하나로 내고, 같은 JSON 을 Markdown 대조표로 렌더한다.
벤더 오퍼레이터(NVIDIA GPU Operator, RBLN NPU Operator)와 나란히 비교하고, 이후
회귀를 감시하는 기준값으로 쓴다.

## 사용법

```bash
# 정적+라이브 모두 측정해 기본 경로(footprint-<날짜>.json)에 JSON 을 낸다.
./measure.sh -m both

# 정적만 / 라이브만.
./measure.sh -m static
./measure.sh -m live

# 대상 차트 지정(기본: kcloud,nvidia-gpu-operator,rbln-npu-operator).
./measure.sh -m static --charts kcloud,nvidia-gpu-operator

# 출력 경로 지정.
./measure.sh -m both -o /path/to/footprint.json

# JSON 을 Markdown 대조표로 렌더.
./render.sh footprint-20260920.json > footprint-20260920.md
```

### 환경 변수

측정 동작은 환경 변수로 조정한다.

- `SAMPLES` — 라이브 사용량 표본 수. 기본 5(Prometheus 를 쓰면 표본이 아니라 10분 창
  질의 하나이므로 이 값은 kubectl top 폴백에만 적용된다).
- `INTERVAL` — 표본 간 간격(초). 기본 10(위와 같은 이유로 kubectl top 폴백 전용).
- `DRY_RUN=1` — 실제 실행 없이 수행할 명령만 출력.
- `NVIDIA_TGZ` — NVIDIA GPU Operator 차트 tgz 경로. 기본은 이 저장소에 커밋된
  `reference/gpu-operator-v25.10.1.tgz` 로, 네트워크 없이 재현된다.
- `RBLN_TGZ` — RBLN NPU Operator 차트 tgz 캐시 경로. 기본은
  `reference/rbln-npu-operator-chart-0.5.0.tgz`(라이선스상 커밋하지 않음, `.gitignore`
  대상). 없으면 `RBLN_URL`(기본은 릴리스 v0.5.0 자산)에서 받아 sha256 을 대조하고,
  있으면 sha256 만 대조한다. 대조 불일치는 스크립트 전체를 실패시킨다. 출처·버전·
  sha256 은 `reference/PROVENANCE.md` 에 고정.
- `PROM_URL` — 있으면 이 Prometheus 서버(`http://host:port` 형태)로 라이브 사용량
  중앙값을 질의한다. 없으면 레이블 `app.kubernetes.io/name=prometheus` 또는 이름
  패턴으로 클러스터에서 찾아 포트포워드해 질의한다. 그것도 없거나 질의가 실패하면
  `kubectl top` 으로, 표본도 없으면 `null` 로 떨어진다.
- `OUT` — JSON 출력 경로. `-o` 인자와 같은 역할.

## 측정 방법

### 정적 측정

각 차트를 `helm template <이름> <소스> --include-crds` 로 렌더한 뒤, 렌더된 매니페스트에서
Deployment·DaemonSet·StatefulSet·Job·CRD·ClusterRole·Role·Webhook·Service 개수와
워크로드 컨테이너 수를 센다. 차트 소스는 다음과 같다.

- `kcloud` — 이 저장소의 `deploy/helm`.
- `nvidia-gpu-operator` — 이 저장소에 커밋된 `reference/gpu-operator-v25.10.1.tgz`
  (Apache-2.0 라이선스라 그대로 재배포).
- `rbln-npu-operator` — `reference/rbln-npu-operator-chart-0.5.0.tgz`. 사유
  라이선스라 저장소에 커밋하지 않고, 없으면 릴리스에서 받아 sha256 을 대조한 뒤
  이 자리에 캐시한다.

두 차트 모두 출처 URL·버전·sha256 을 `reference/PROVENANCE.md` 에 고정해 뒀다.
sha256 이 맞지 않으면(손상·변조 의심) 측정 스크립트가 즉시 실패한다.

각 정적 대상마다 `helm show chart` 로 읽은 `chartVersion` 을 결과 JSON 에 함께 기록하고,
Markdown 표 위에 어느 버전과 대조했는지 한 줄로 낸다.

YAML 을 JSON 으로 바꿀 때 snap 으로 설치된 yq 가 임시 디렉터리를 읽지 못하는 문제가
있어, 이 도구는 python3 의 yaml 모듈로 파싱한다.

### 라이브 측정

클러스터는 읽기 전용으로만 건드린다. `kubectl get` 과 `kubectl top` 만 쓰고, 아무것도
만들거나 바꾸거나 지우지 않는다. 대상 네임스페이스는 `kcloud`·`kube-system`·`rbln-system`
가운데 실재하는 것이며, 그중 이름이 정규식 `^(kcloud|nvidia|furiosa|rbln|dcgm|tt)-` 에
맞는 파드·DaemonSet 만 센다. 이 필터는 kcloud 가 배포·소유하는 구성요소만 남기고,
같은 네임스페이스에 있어도 사용자 워크로드(예: `api-demo`)나 무관한 시스템 파드는
제외한다. 요청량은 파드 스펙의 `resources.requests` 합이고(CPU 는 milli, 메모리는 Mi),
사용량 중앙값은 Prometheus 가 있으면 그 우선이다: `PROM_URL` 이나 클러스터에서 찾은
Prometheus 서버로 10분 창(30초 스텝)의 `rate(container_cpu_usage_seconds_total[5m])`
합과 `container_memory_working_set_bytes` 합을 질의해 30초 스텝 표본들의 중앙값을
쓴다. Prometheus 가 없거나 질의가 실패하면 `kubectl top pods --containers` 를
`SAMPLES` 회 표본한 총합의 중앙값으로 떨어지고, 그것도 표본이 없으면 `null` 이다.
평균이 아니라 중앙값을 쓰는 이유는 기동 직후 한 번의 폭주가 평균을 통째로 끌고 가기
때문이다.

## 이 측정의 한계

- 정적 표는 차트가 설치하는 리소스만 센다. kcloud(NPUClusterPolicy·DriverInstallPolicy),
  NVIDIA GPU Operator(ClusterPolicy), RBLN NPU Operator 셋 다 정책 CR 로 런타임에 DaemonSet 을
  만든다. 따라서 정적 표의 DaemonSet 열은 차트 설치 시점 값이고, 같은 문서의 라이브 열과
  다르다(kcloud 의 그 런타임 값은 라이브 행에 있다). 정적 "DaemonSet 0" 을 "이 operator 는
  DaemonSet 을 설치하지 않는다" 로 읽으면 안 된다.
- 라이브 필터 `^(kcloud|nvidia|furiosa|rbln|dcgm|tt)-` 는 ACPP 검증 probe 파드
  (`acpp-verify-*`, `acpp_verifier.go` 의 GenerateName)를 이름에서 제외한다. probe 파드는
  검증 중에만 잠깐 뜨는 일시 워크로드라 상주 설치량에 넣지 않는다.
- kcloud 차트를 기본값으로 렌더하므로 webhook 과 metrics Service 가 기본 비활성이라
  각각 0 으로 잡힌다. 두 기능을 켜면 Webhook 과 Service 가 각각 1 씩 늘어난다.
- kcloud 정적 Job 은 모두 Helm hook Job(CRD apply/cleanup·pre-delete gate)이라 상시
  워크로드가 아니다. 회귀 기준값을 볼 때 이 Job 수는 상주 파드 수와 구분해서 읽어야 한다.
- rbln-npu-operator 차트는 사유 라이선스라 저장소에 커밋하지 않고 릴리스에서 받아
  sha256 을 대조한다(`reference/PROVENANCE.md`). 네트워크가 없고 로컬 캐시도 없으면
  이 대상의 정적 값은 `null` 이다.
- 벤더 오퍼레이터 차트는 이 클러스터에 설치돼 있지 않아 정적 렌더만 가능하고 라이브
  값은 없다. kind 클러스터에 장치 없이 설치했을 때 상주 파드가 공정하게 세어지는지는
  벤더별로 다르다.
- `rbln-system` 네임스페이스가 없으면 RBLN 구성요소의 라이브 관측이 불가능하다.
- 라이브 사용량 중앙값은 Prometheus 나 metrics-server(`kubectl top`)가 있어야 나온다.
  둘 다 없으면 사용량 값은 `null` 로 기록하고 한계 절에 남긴다.
- 차트를 얻을 수 없으면(로컬 경로 없음, 렌더 실패) 그 대상의 정적 값을 `null` 로 두고
  사유를 한계에 기록한다. 숫자를 지어내지 않는다.

## 출력 JSON 구조

`targets.<대상>.static` 의 키(`Deployment`, `DaemonSet`, `StatefulSet`, `Job`, `CRD`,
`ClusterRole`, `Role`, `Webhook`, `Service`, `containers`)는 이후 기준값·회귀 감시가
그대로 참조하므로 이름을 바꾸지 않는다.

```json
{
  "date": "2026-09-20",
  "commit": "<short sha>",
  "mode": "both",
  "targets": {
    "kcloud": {
      "static": { "Deployment": 1, "…": 0, "containers": 5 },
      "live": {
        "namespaces": ["kcloud", "kube-system"],
        "pods": 14, "daemonsets": 10, "containers": 14,
        "cpuRequestMilli": 350, "memRequestMi": 168,
        "cpuUsedMilliMedian": 167, "memUsedMiMedian": 644
      }
    },
    "nvidia-gpu-operator": { "static": { "…": 0 }, "live": null }
  },
  "limits": ["…"]
}
```

## baseline 갱신

`make footprint-check` 가 회귀로 실패했는데 그 증가가 의도된 것이라면, `make footprint`
로 다시 측정한 뒤 `jq '.targets.kcloud.static' footprint.json` 출력을
`test/footprint/baseline.json` 의 같은 경로에 옮겨 쓰고 `_comment` 의 날짜·commit 을
갱신한다.
