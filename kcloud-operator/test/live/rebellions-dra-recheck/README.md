<!-- README.md: Rebellions DRA 벤더 CDI 수정 재확인 스크립트 사용법과 최근 확인 결과 | 생성일: 2026-09-20 | 수정일: 2026-10-11 -->
# Rebellions DRA — 벤더 CDI 수정 재확인

## 왜

Rebellions DRA 경로(kubelet plugin `rbln-k8s-dra-driver`)는 벤더 결함으로 막혀 있다.
컨테이너 툴킷(`rbln-ctk cdi generate`, container toolkit 0.2.1)이 만드는 CDI 스펙은 UMD
마운트·훅을 스펙 **최상위 `containerEdits`** 에 쓰는데, kubelet plugin 의
`getRuntimeUMDEdits()`(`cmd/npu-kubelet-plugin/cdi.go`)는 `devices[name=runtime].containerEdits`
만 읽는다. `rblnRuntimeSpec` 구조체에 최상위 `containerEdits` 필드가 없어 그 내용이 unmarshal
단계에서 버려지고, `common` 장치가 비어 CDI 검증에서 거부돼 kubelet plugin 이
CrashLoopBackOff 에 빠진다. 결과적으로 `ResourceSlice` 가 발행되지 않는다.

이 결함의 분석과 수정안(PR 초안 포함)은 이 체크아웃 밖의 미추적 디렉터리
`rebellions-dra-fix-20260917/`(`PR-draft-ko.md`, 커밋 `fb7cdc0`, <rebellions-node> 라이브 검증 기록 포함)에
있다. 그 수정은 kcloud 내부에서 만든 것이라 upstream 에는 아직 없다 — 벤더가 릴리스해야
`main`(kcloud-operator)이 그 이미지를 받아 쓸 수 있다. 이 디렉터리는 **벤더가 upstream 에
그 수정을 반영했는지 주기적으로 재확인**하는 읽기 전용 스크립트를 담는다. 코드 수정이 아니라
외부 상태 확인 자동화다 — 판정이 뒤집히면(반영 확인) 그때 실제 반영 작업(이미지 재빌드·라이브
재검증)을 별도로 진행한다.

## 확인 대상과 판정 기준

- **저장소**: `rbln-sw/rbln-k8s-dra-driver` (GitHub)
- **파일**: `cmd/npu-kubelet-plugin/cdi.go`
- **구조체**: `rblnRuntimeSpec`
- **판정 기준**: 그 구조체가 `ContainerEdits` 필드를 선언하는지(또는 그 필드에
  `containerEdits` 태그가 붙어 있는지). 지금(미반영) 은 `Kind`, `Devices` 두 필드만 있다.
  같은 파일의 `rblnRuntimeDevice` 구조체는 이미 장치별 `containerEdits` 태그를 갖고 있으나
  이는 **다른 구조체**이며 이 결함과 무관하다 — `run.sh` 의 파서는 중괄호 깊이를 세어
  `rblnRuntimeSpec` 블록만 잘라내므로 이 둘을 섞지 않는다.

확인은 **최신 릴리스**(`releases/latest`) 기준으로 판정하고, `main` 브랜치는 참고용으로만
같이 확인한다(릴리스 전에 `main` 에만 먼저 반영되는 경우를 "unreleased fix on main" 으로
구분해 알린다 — 판정 자체는 바꾸지 않는다).

## 실행 방법

```bash
# 무엇을 조회할지만 보기(네트워크 호출 없음)
DRY_RUN=1 test/live/rebellions-dra-recheck/run.sh

# 실제 확인(읽기 전용 — GitHub API/raw 조회만, 클러스터·<rebellions-node> 접근 없음)
test/live/rebellions-dra-recheck/run.sh

# evidence 위치 바꾸기(기본 /tmp/rebellions-dra-recheck-evidence)
OUT=/path/to/dir test/live/rebellions-dra-recheck/run.sh
```

한 실행에 GitHub 호출은 최대 3회(`releases/latest`, 최신 태그의 raw 파일, `main` 의 raw
파일)뿐이다. `kubectl` 은 전혀 쓰지 않는다 — <rebellions-node> 는 `atomplus-vllm.service` 가 ATOM
장치를 점유하므로 이 스크립트는 장치를 열거나 클러스터 상태를 건드리지 않는다.

`$OUT` 아래에 매 실행마다 원본 API 응답(`release-latest.json`)과 두 브랜치의 `cdi.go`
전문, 그리고 `rblnRuntimeSpec` 만 추출한 구조체 블록(`struct-block-*.txt`)을 남긴다.

## 판정 규칙

**반영 확인(verdict=PASS)** — 벤더가 최신 릴리스에 `ContainerEdits` 를 반영했을 때. 이때부터는
재확인이 아니라 실제 반영 작업이다. 이 계획에서는 실행하지 않고 절차만 남긴다.

1. `deploy/helm/values.yaml` 의 `rebellions.dra.repository`/`rebellions.dra.tag` 를 그
   반영된 벤더 릴리스 태그로 갱신한다(현재 `v0.2.0` 고정, 미반영 상태 기준).
2. images 저장소(`images/kcloud-operator-images/generic-dra-driver`)에서 그 벤더 이미지를
   감싸는 미러가 있다면 재빌드한다.
3. <rebellions-node> 의 외부 서비스 `atomplus-vllm.service` 가 ATOM 2장을 **놓아준 구간**을 확인한
   뒤에만, 그리고 **사용자 승인 하에**, `rebellions.dra.enabled=true` 로 helm upgrade 해
   kubelet plugin 을 재배포한다.
4. kubelet plugin 파드가 `1/1 Running` 이고 `ResourceSlice`(`<rebellions-node>-npu.rebellions.ai`)가
   발행되는지로 DRA 광고 스위치를 라이브 검증한다(`PR-draft-ko.md` 의 커밋 `fb7cdc0` 라이브
   검증과 같은 관측 대상).
5. 검증 후 <rebellions-node> 는 외부 서비스가 상시 점유하는 노드이므로 기본은 원복
   (`rebellions.dra.enabled=false`)한다.

**미반영(verdict=BLOCKED, reason=not_reflected)** — 지금 확인된 분기. 벤더 릴리스 일정에
달린 외부 차단이라 이 스크립트로는 풀 수 없다. 다음 재확인까지 대기한다.

**네트워크 실패/rate limit(verdict=BLOCKED, reason=network)** — GitHub API 접근 실패 시.
재확인 자체가 안 된 것이므로 다시 실행한다.

## 최근 확인 결과

```json
{"test":"rebellions-dra-recheck","repo":"rbln-sw/rbln-k8s-dra-driver","struct":"rblnRuntimeSpec","latestTag":"v0.2.0","publishedAt":"2026-09-12T00:15:55Z","checkedAt":"2026-09-20T08:49:31Z","fieldsFound":"Kind,Devices","fixed":false,"defaultBranch":"main","mainState":"not_fixed","phase":"cleanup","reason":"not_reflected","verdict":"BLOCKED","dryRun":false}
```

`v0.2.0`(최신 릴리스)과 `main`(HEAD `e08884c`) 모두 `rblnRuntimeSpec` 에
`Kind`, `Devices` 필드만 있고 `ContainerEdits` 가 없다 — 두 곳 다 미반영. 판정:
**BLOCKED(external)**.
