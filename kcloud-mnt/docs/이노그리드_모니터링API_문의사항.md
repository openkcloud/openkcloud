# 이노그리드 모니터링 API v2 — 문의/확인 사항

포털 `/monitoring` 연동 중 **가이드와 실제 응답이 다른 지점**을 정리. (2026-10-07 기준, Base `http://localhost:8000/api/v2`, admin 토큰)

---

## A. 활용률 히트맵 — `by=hostname` 미동작 + metric 목록 404 (우선순위 높음)

가이드: 히트맵 = `/monitoring/metrics/timeseries?metric=&period=24h&step=1h&by=hostname` → "호스트 × 시각 24칸", metric 허용 13종.

**실제 관찰**
- `GET /monitoring/metrics/timeseries?metric=gpu_utilization&period=24h&step=1h&by=hostname`
  → `series`가 **1개뿐**이고 `metric: {}`(라벨 비어있음), 값은 전부 `"0"`. **호스트별로 분리되지 않음.**
- `GET /monitoring/metrics` → `{"detail":"Not Found"}` (허용 metric 목록 확인 불가)

**질문**
1. `by=hostname`으로 **호스트별 series**가 나오는 정확한 조건/사용법? (현재 단일 series + 빈 라벨)
2. metric **허용 13종의 정확한 키 이름** 목록? (`/monitoring/metrics` 같은 목록 API가 있나요?)
3. 값이 전부 0인 것은 가속기 idle 때문인지, 아니면 수집 미연동인지?

---

## B. 호스트 맵 — mgmt 노드 `utilization_percent = null`

가이드: 호스트 맵 = `/clusters/{c}/nodes`의 **노드별 `utilization_percent`로 색상 결정**.

**실제 관찰**
- `GET /clusters/mgmt/nodes` → 노드 8개 전부 `utilization_percent: null`, `vendor: null`, `accelerator_count: null` (단 `power_watts`는 존재).
- 반면 서비스클러스터(`/clusters/furiosa/nodes` 등)의 노드는 `utilization_percent` 값 존재(0.0).

**질문**
1. mgmt 노드의 `utilization_percent`가 null인 것이 의도된 것인지? (가속기가 서비스클러스터 VM에 있어서로 추정)
2. mgmt 호스트 맵을 그릴 때 **무엇을 색상 기준으로** 쓰길 권장하나요? (`power_watts`? 아니면 노드 utilization을 추후 채울 예정?)

---

## C. 상위 호스트 — `sort_by=utilization_percent` 미지원

가이드 상태: **"추가"(미구현), 현재 `power_watts` 정렬만 지원**.

**질문**
1. `/clusters/{c}/nodes?sort_by=utilization_percent` **제공 일정**?
2. mgmt 노드 utilization이 null이면 정렬도 불가한데, B와 함께 해결되는지?

---

## D. 그 외 연동 대기/확인 항목

1. **저장소 사용률** `/storage/ceph/capacity` — "개발 중". 제공 일정과 응답 스키마?
2. **쓰로틀링 "횟수"** — 가이드 KPI는 "쓰로틀링 횟수"인데, `/accelerators/{id}/metrics`엔 **`throttled`(bool)만** 있고 누적 횟수 필드가 없음. 누적 카운트 제공 예정인지? (현재 포털은 "현재 throttled 장치 수"로 대체)
3. **오류 로그** — `/logs/search`에 `level`/`cluster` 라벨 누락(가이드도 "개발 중"). 라벨 추가 및 journald(커널 OOM·가속기 XID) 수집 일정?
4. **가속기 개수 불일치** — `/clusters`의 `mgmt.accelerator_count = 4` vs `/monitoring/overview`의 `accelerator_count = 11`. 두 값의 기준 차이와 어느 것이 "mgmt 전체 가속기"인지?
5. **응답 warning 의미** — `REBELLIONS_SCALE_CORRECTED_X1000`(overview), `PUE_COOLING_UNMEASURED_ESTIMATE`(power/efficiency)가 값 신뢰도에 영향 있는지?
6. **rebellions `throttled: null`** — furiosa/nvidia는 bool인데 rebellions만 null인 이유(미지원?).

---

## 참고 — 현재 포털 연동 상태
- ✅ 연동: 가속기 공통지표, KPI(전력효율·가속기사용률·서버총전력·정상노드·쓰로틀장치), 전력 분해, 전력 시계열 3계층
- ⏸ 대기(위 문의 해결 후): 히트맵, 호스트맵(util), 상위호스트(util), 저장소, 오류로그 라벨/XID·OOM
