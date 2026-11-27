<!-- README.md: air-gap 이미지 참조 검사(check.sh) 실행법과 판정 기준 | 생성일: 2026-09-20 -->
# air-gap 이미지 참조 검사 (airgap)

## 왜

air-gap(사설 미러) 클러스터는 모든 이미지를 단일 미러에서 당겨야 한다. 차트 어딘가에 공개
레지스트리(docker.io·nvcr.io·registry.k8s.io 등)나 개발 레지스트리(`DEV_REGISTRY` 로 지정)로
가는 이미지가 남아 있으면, air-gap 에서 그 파드는 이미지를 못 받아 뜨지 못한다.
`check.sh` 는 `deploy/helm` 을 air-gap values 로 렌더해서 **모든 이미지 참조가 미러 prefix 로
시작하는지** 검사한다. 설치는 하지 않는다 — `helm template` 은 클러스터에 접근하지 않는 오프라인
렌더다.

## 무엇을 검사하는가

- `helm template <release> deploy/helm -f deploy/helm/values-airgap.example.yaml` 로 렌더한다
  (`--registry` 를 주면 그 미러로 치환해 렌더).
- 렌더 결과에서 이미지 참조를 뽑는다:
  - 컨테이너·initContainer 의 `image:`
  - operator args 의 `--generated-dp-image=` / `--generated-dra-image=`
  - env 의 `value:`(이미지 형태만)
  - CR/ConfigMap 에 실린 벤더 이미지 필드(`devicePluginImage:`·`unifiedDevicePluginImage:`·
    `fabricManagerImage:` 등 `*Image:`)
- 뽑은 참조가 전부 `--registry` 미러 prefix 로 시작하는지 본다. 하나라도 벗어나면 그 목록을
  내고 종료코드 1.

## 실행

```bash
# 기본: values 파일의 global.registry(플레이스홀더 <your-registry>)를 미러 prefix 로 검사
bash test/airgap/check.sh

# 실제 미러로 검사(모든 이미지가 이 prefix 로 치환되는지)
bash test/airgap/check.sh --registry registry.internal:5000/kcloud

# 명령만 미리 보기(렌더도 하지 않음)
DRY_RUN=1 bash test/airgap/check.sh
```

| 변수/인자 | 기본 | 뜻 |
|---|---|---|
| `--registry` / `REGISTRY` | values 의 `global.registry` (`<your-registry>`) | 검사할 미러 prefix |
| `CHART` | `deploy/helm` | 렌더할 차트 |
| `VALUES` | `deploy/helm/values-airgap.example.yaml` | air-gap 오버라이드 values |
| `RELEASE` | `kc` | helm 릴리스 이름 |
| `OUT` | `/tmp/airgap-check-evidence` | 렌더·요약 저장 위치 |
| `DRY_RUN` | `0` | `1` 이면 명령만 출력 |

기본값(`<your-registry>`)으로 돌리면 렌더된 모든 이미지가 `<your-registry>/` 로 시작하는지만
본다 — 즉 "미러로 못 가는 하드코딩 이미지가 없는지" 를 검사한다. `--registry` 로 실제 미러를
주면 values 의 `<your-registry>` 를 그 값으로 치환해 렌더한 뒤 같은 검사를 한다.

## 판정

스크립트는 마지막에 JSON 요약 한 줄을 낸다.

```json
{"test":"airgap-image-refs","registry":"<your-registry>","images":24,"offenders":0,"verdict":"PASS","dryRun":false,"date":"..."}
```

- `verdict=PASS` — 이미지를 하나 이상 뽑았고 전부 미러 prefix 다. 종료코드 0.
- `verdict=FAIL` — 미러 밖 이미지가 있거나(목록 출력), 이미지를 하나도 못 뽑았다(추출기·values
  이상). 종료코드 1.
- `verdict=BLOCKED` — helm 이 없다. 종료코드 1.
- `verdict=DRY` — `DRY_RUN=1`, 명령만 출력. 종료코드 0.

## 이 검사가 증명하지 않는 것

- 미러에 그 이미지가 **실제로 올라가 있는지** — 이 검사는 참조 경로만 본다. 미러 재고 확인은
  별도다.
- 설치·기동 성공 — 렌더만 하고 설치하지 않는다.
- `imagePullSecrets`·미러 인증 설정의 정합성.
