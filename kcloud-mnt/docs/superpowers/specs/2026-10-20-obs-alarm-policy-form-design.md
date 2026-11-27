# 알람 정책 생성 폼 + 미리 평가 — 설계 문서

작성일: 2026-10-20
근거: 이노그리드 전달 스펙(알람 최신 업데이트) + `docs/알람정책 생성 UI(예상).html` 목업
대상: MNT-OBS(백엔드 중개) · MNT-APP `/monitoring/alerts`(프론트)

## 목표

알람 탭에 **정책 생성 폼**(현재 "제공 예정" 버튼만 존재)을 신규 구현하고, 생성된 정책을 **현재 수집값 기준으로 미리 평가(preview)** 하는 기능을 추가한다. Monitor v2 API를 OBS가 중개한다.

## 결정 사항 (브레인스토밍 확정)

- 범위: **OBS 백엔드 + 프론트 전부**
- 조건 연산자(op): **4종** `>= > <= <` (목업 기준)
- duration·window_min 입력: **프리셋 드롭다운** (duration `[0m,2m,3m,5m,10m]`, window_min `[1,5,10,30]분`)
- preview(미리 평가): **이번에 포함** — 정책 목록 각 행 "미리 평가" 버튼 → 결과 모달 + 생성 성공 토스트
- 지표(metric) 목록: **`/alerts/metrics` 동적 로드** (목업의 6종 하드코딩 아님). 응답 `data[].{key,label,unit,kind}`, `kind`(resource|log|slo)로 감시대상 필드 제어(POST엔 kind 미전송)

## 실측 응답 형태 (테스트베드 Monitor v2)

- `/alerts/metrics` → `{data:[{key,label,unit,kind}]}`
- `/alerts/channels` → `{data:[{id,type,config,...}]}` — `type=email`→email, `type=webhook & config.format=slack`→slack, 그 외 webhook→webhook
- `/logs/label-values?label=cluster|container|unit` → `{data:[...]}` (OBS: `{values:[...]}`)
- `/clusters/{cluster}/nodes` → `{nodes:[{nodename,...}]}`
- `/clusters/{cluster}/nodes/{node}/accelerators` → `{data:[{acc_id,...}]}`
- `/slo/services?cluster=` → `{items:[{model}], warnings:["NO_DATA"]?}` (응답 ~4초)
- `POST /alerts/policies` 성공 → `201 {data:{...}}`(서버가 `kind` 자동 결정) / 실패 → `422 {detail:{errors:[{field,message}]}}`
- `POST /alerts/policies/{id}/preview` → 평가 결과(충족 여부)

## 아키텍처

```
CreatePolicyDialog(폼)
  ├─ 지표/채널/라벨값/노드/가속기/모델  ← obs 훅 ← MNT-OBS 중개 ← Monitor v2
  └─ POST /monitoring/alerts/policies → 201 성공(목록 갱신+토스트) / 422 인라인 에러
정책 목록 행 "미리 평가" → POST …/preview → PolicyPreviewResult(모달)
```

## 백엔드 (MNT-OBS `app/routers/monitoring.py`)

### 1) 422 통과 (정책·채널 생성)
현재 `alerts_policy_create`/`alerts_channel_create`는 모든 예외를 502로 변환 → 422 `detail.errors` 전달 불가.
수정: `httpx.HTTPStatusError`는 **업스트림 status·본문 그대로** 전달.
```python
import httpx
def _passthrough(e: Exception) -> HTTPException:
    if isinstance(e, httpx.HTTPStatusError):
        body = e.response.json() if e.response.content else None
        detail = (body or {}).get("detail", body) if isinstance(body, dict) else str(e)
        return HTTPException(status_code=e.response.status_code, detail=detail)
    return HTTPException(status_code=502, detail=f"monitor api error: {type(e).__name__}: {e}")
```
`alerts_policy_create`/`alerts_channel_create`의 `except` 를 `raise _passthrough(e)` 로 교체.
> 주의: `monitor_client.request()`가 내부적으로 `raise_for_status()` 하므로 4xx는 `HTTPStatusError`로 올라온다.

### 2) 신규 프록시
```python
@router.get("/monitoring/clusters/{cluster}/nodes/{node}/accelerators")
def node_accelerators(cluster: str, node: str):
    try: return {"accelerators": monitor.get(f"/clusters/{cluster}/nodes/{node}/accelerators").get("data", [])}
    except Exception as e: raise _mon_err(e)

@router.get("/monitoring/slo/services")
def slo_services(cluster: str = Query(...)):
    try:
        d = monitor.get("/slo/services", params={"cluster": cluster})
    except Exception as e: raise _mon_err(e)
    return {"items": d.get("items", []), "warnings": d.get("warnings", [])}

@router.post("/monitoring/alerts/policies/{policy_id}/preview")
def alerts_policy_preview(policy_id: str, body: dict = Body(default={})):
    try: return monitor.request("POST", f"/alerts/policies/{policy_id}/preview", json=body or {})
    except Exception as e: raise _passthrough(e)
```
*(클러스터 목록=기존 `logs/label-values?label=cluster`, 노드=기존 `/monitoring/nodes?cluster=`, 컨테이너/유닛=기존 `label-values`, metrics·channels 기존 재사용.)*

## 프론트 훅 (`MNT-APP/src/lib/obs-api.ts`)

- `useCreatePolicy()` — `POST /monitoring/alerts/policies`. 실패 시 응답 JSON(`detail.errors`)을 에러에 실어 호출부가 파싱.
- `usePreviewPolicy()` — `POST /monitoring/alerts/policies/{id}/preview`.
- `useNodeAccelerators(cluster, node)` — `enabled: !!cluster && !!node`.
- `useSloServices(cluster)` — `enabled: !!cluster`, ~4초 로딩.
- 재사용: `useAlertMetrics`, `useAlertChannels`, `useLogLabelValues`, `useNodes`, `useAlertPolicies`(목록 갱신).
- 참고: `obsSend`는 비정상 응답 시 에러 본문을 던지도록 보장(422 `detail.errors` 파싱 위해). 필요 시 보강.

## 프론트 UI (`MNT-APP/src/app/(portal)/monitoring/alerts/page.tsx`)

### 정책 생성 버튼/다이얼로그
- "정책 생성(제공 예정)" → **"정책 생성"** → `CreatePolicyDialog` 오픈.

### `CreatePolicyDialog` 동작 (목업 로직)
**조건(conditions) 배열** — "모두 만족하면 울림", 1개 이상:
- 지표 select: `label · {resource:카드별, log:로그, slo:모델별}[kind]`
- op select: `>= > <= <`
- threshold number(placeholder=단위)
- window: `kind==resource` → "지금 값"(window_min 미전송) / 그 외 → `[1,5,10,30]분` 프리셋
- "조건 추가" / 조건 2개↑면 각 행 삭제 버튼

**감시 대상(selector)** — "안 고르면 전체", 6필드 전체 노출(클러스터·노드·가속기 카드·모델·컨테이너·유닛), 비허용은 **비활성("사용 안 함")**:
- 허용 규칙 `allowedSelectorFields(conds)`:
  - 조건 1개: `resource→[cluster,node,acc_id]`, `log→[cluster,container,unit]`, `slo→[cluster,model]`
  - 조건 2개↑: `[cluster]` + (로그 조건 포함 시 `container,unit` 추가)
- cascading: `cluster` 선택 시 `node/acc_id/model` 옵션 로드 & 하위 초기화. `node` 선택 시 `acc_id` 로드. `container/unit`은 전역. `model`은 `slo/services`(4초 스피너, `NO_DATA`면 안내).
- 비활성 필드 값은 전송 시 비움.

**기타 입력**: duration 프리셋 · severity(`info|warning|critical` 세그) · channels(**등록된 종류만 활성** — `channelKind(channel)` 매핑으로 email/slack/webhook 버튼 enable).

**Selector 조립** `buildSelector(tg)`: 선택된 `{k:v}`만 `k=v` 로 쉼표 연결, 없으면 `""`. 허용문자 `A-Za-z0-9_.-/`.

**검증·제출**:
- 클라 선검증: 이름 공백·threshold 비숫자 → 해당 칸 인라인 + "서버로 보내지 않음" 배너, 전송 중단.
- body: `{name, description, conditions:[{metric,op,threshold,window_min?}], selector, duration, severity, channels, enabled:true}` (conditions 항상 배열).
- 성공(201): 토스트 + `useAlertPolicies` 무효화 + 모달 닫기.
- 실패(422): `detail.errors[].field`(name/selector/channels/thN) → 칸별 인라인 + `.bad` 하이라이트 + 배너. `selector` 데이터없음(NO_DATA류)은 **"다시 시도"** 버튼.

### 미리 평가(preview)
- 정책 목록 각 행에 "미리 평가" 버튼 → `usePreviewPolicy(id)` → `PolicyPreviewResult` 모달(충족/미충족 + 조건별 현재값).
- 생성 성공 토스트에서도 바로 평가 유도(선택).

### 컴포넌트 분리(단일 책임)
- `CreatePolicyDialog` — 폼 상태·검증·제출
- `PolicyPreviewResult` — 평가 결과 표시
- 순수 헬퍼: `allowedSelectorFields(conds)`, `channelKind(channel)`, `buildSelector(tg)`, `metricKind(metrics,key)`

## 범위 밖
- 정책 수정(이미 `PolicyEditDialog` 존재) 확장, 채널 생성 UI 변경(기존 유지).
- op 6종(스펙) — 목업 기준 4종만.

## 검증
- `py_compile` (monitoring.py), `tsc --noEmit`·`eslint`(obs-api.ts, alerts/page.tsx)
- 로컬 Mac은 Monitor v2 미도달 → **실동작은 테스트베드**(터널+MNT-OBS 또는 배포): 생성 성공/422 인라인, selector 동적 활성, SLO 4초 로딩, 미리 평가 결과 확인
