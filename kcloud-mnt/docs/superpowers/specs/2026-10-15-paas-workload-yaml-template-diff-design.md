# PaaS 워크로드 YAML — 템플릿 선택 + 적용 전 before/after 비교 설계

작성: 2026-10-15 · 브랜치: `CLOUDDEV-4664` · 모듈: MNT-APP(프론트) + MNT-PAAS(백엔드)

## 배경·목표

PaaS 워크로드 생성 화면의 YAML 경로는 현재 고정 샘플 1개를 textarea 로 편집하고 "적용"을 누르면 **곧바로 server-side apply** 된다(`workloads/page.tsx` CreateDialog). 기존 워크로드의 "보기·수정" 다이얼로그(YamlDialog)도 "저장" 시 즉시 apply 된다. 반영 전 결과를 확인할 방법이 없다.

다음 3가지를 추가한다.

1. **워크로드별 템플릿 선택 → YAML 주입**: 종류(Deployment/StatefulSet/DaemonSet/Job/CronJob)를 고르면 해당 시작 매니페스트가 편집창에 채워진다.
2. **적용 전 before/after 비교**: "적용"(생성) 또는 "저장"(수정)을 누르면 즉시 실행하지 않고, 현재 상태(before)와 적용 결과(after)를 비교해 보여준다.
3. **비교 화면에서 실행**: 비교를 확인한 뒤 `실행` 버튼을 눌러야 실제로 반영된다.

생성·수정 다이얼로그 **둘 다**에 적용한다.

## 핵심 설계 결정

- **비교는 서버 dryRun 기반**(정확 비교). after 는 사용자가 쓴 텍스트가 아니라, 쿠버네티스가 기본값·정규화까지 채운 **실제 적용 결과**(`dryRun=All`)다. `kubectl diff` 와 동일한 신뢰도.
- 백엔드는 **미리보기 엔드포인트 1개만 추가**하고, 실제 실행용 기존 apply 경로는 변경하지 않는다.
- diff 렌더링은 외부 라이브러리 없이 **간단한 라인 단위 diff 컴포넌트를 직접 구현**한다(저장소에 diff/에디터 의존성 없음, YAML 검토엔 통합 뷰로 충분).

## 구성요소

### 1. 백엔드 — `MNT-PAAS/app/routers/k8s.py`

신규: `POST /clusters/{cluster_id}/workloads/apply/preview`
- body: `{ "yaml": "<manifest>" }`
- 응답: `{ "before": "<yaml|''>", "after": "<yaml>", "action": "create" | "update" }`
- 처리:
  1. 기존 `apply_workload` 와 동일하게 파싱·검증(단일 리소스, `kind`·`metadata.name` 존재, `_WORKLOAD_API` 허용 종류). 네임스페이스 보정 동일.
  2. **before**: `k8s_get(_wl_path(kind, ns, name))` 시도.
     - 성공 → `managedFields`·`status` 제거 후 `yaml.safe_dump` → `before`, `action="update"`.
     - 404 → `before=""`, `action="create"`. (그 외 예외는 `_err(e)`)
  3. **after**: apply 와 같은 PATCH(`application/apply-patch+yaml`, `fieldManager=okc-portal&force=true`)에 **`&dryRun=All`** 추가 호출 → 반환 객체에서 `managedFields`·`status` 제거 후 dump → `after`.
- 실제 실행용 `POST .../workloads/apply`, `PUT .../yaml` 은 **변경 없음**.

### 2. 프론트 — `MNT-APP/src/lib/paas-api.ts`

- `previewWorkloadApply(clusterId, yaml): Promise<{ before: string; after: string; action: "create" | "update" }>` 추가. `fetchWorkloadYaml` 과 같은 평범한 GET/POST 헬퍼 패턴(뮤테이션 아님 — 아무것도 바꾸지 않는다).

### 3. 프론트 — `MNT-APP/src/components/paas/workload-diff.tsx` (신규)

- props: `{ before: string; after: string; action: "create" | "update" }`.
- LCS 기반 라인 diff 계산 → 통합 뷰: 추가(초록)/삭제(빨강)/유지(회색) 줄.
- `action === "create"` 면 비교 대상이 없으므로 "신규 생성 — 비교 대상 없음" 안내와 함께 after 전체를 추가(초록)로 표시.
- 다른 화면에서도 재사용할 수 있도록 데이터만 받는 순수 표시 컴포넌트로 둔다.

### 4. 프론트 — `MNT-APP/src/app/(portal)/paas/workloads/page.tsx`

- **`WORKLOAD_TEMPLATES`**: `Record<Kind, string>` — Deployment/StatefulSet/DaemonSet/Job/CronJob 각각의 시작 매니페스트. 기존 단일 `YAML_SAMPLE` 을 대체.
- **CreateDialog YAML 탭**:
  - 상단에 템플릿 `Select`(종류 5종). 선택 시 `setYaml(WORKLOAD_TEMPLATES[kind])`.
  - 로컬 state `preview: { before, after, action } | null`.
  - "적용" → `previewWorkloadApply` 호출(로딩 표시). 성공 → `preview` 설정 → 다이얼로그 본문을 **비교 뷰로 전환**. 실패 → 토스트, 편집 유지.
  - 비교 뷰: `WorkloadDiff` + 하단 `뒤로`(preview=null 로 편집 복귀) / `실행`(기존 `apply.mutate(yaml)` → 성공 시 토스트·닫기).
- **YamlDialog(수정)**:
  - "저장" → `previewWorkloadApply(text)` → 비교 뷰 → `실행`(기존 `update.mutate({kind,namespace,name,yaml:text})`).
  - preview 엔드포인트는 yaml 에서 kind/ns/name 을 끌어오므로 생성·수정 공용.

## 데이터 흐름

```
[편집] --적용/저장--> previewWorkloadApply(yaml)
                         └─ 백엔드: before(GET) + after(dryRun apply)
       <--{before,after,action}--
[비교 뷰] --실행--> 기존 apply.mutate(yaml) / update.mutate(...)  (실제 반영)
          --뒤로--> [편집]
```

## 에러 처리

- preview 실패(파싱 오류·권한·네트워크): 토스트로 사유 표시, 비교 뷰로 넘어가지 않고 편집 화면 유지.
- 실행(apply/update) 실패: 기존과 동일하게 토스트.
- dryRun 은 클러스터에 아무것도 생성·변경하지 않아야 한다(검증 항목).

## 검증

- 정적 빌드 통과 + 변경 파일 eslint 신규 경고 없음.
- 테스트배드 수동 확인:
  - 템플릿 5종 선택 시 각 매니페스트가 채워짐.
  - 신규 이름: 비교 뷰가 "신규 생성"으로 after 만 표시, `실행` 후 실제 생성.
  - 기존 워크로드 수정: before(현재)·after(dryRun) diff 표시, `실행` 후 반영.
  - preview 를 띄우기만 하고 `뒤로`/닫기 했을 때 클러스터에 생성/변경이 **없음**(dryRun 비영속 확인).

## 범위 밖(YAGNI)

- 사이드바이사이드(좌우 2열) diff — 통합 뷰로 충분.
- 템플릿 서버 저장·관리 화면 — 종류별 정적 템플릿만.
- Service/Ingress 등 비(非)워크로드 종류 — 기존 허용 범위(`_WORKLOAD_API`) 유지.
