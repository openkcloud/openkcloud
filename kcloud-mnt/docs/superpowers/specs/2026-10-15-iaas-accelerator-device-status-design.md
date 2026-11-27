# IaaS 가속기 장치 현황 (Cyborg 5-2) 설계

작성: 2026-10-15 · 브랜치: `CLOUDDEV-4663` · 근거: ETRI kcloud-cyborg 포털 연동 가이드 5-2

## 목표

물리 노드의 가속기(GPU/NPU) 인벤토리와 **사용 현황**을 포털에서 가시화한다(조회 전용, 일반 사용자 포함).
장치별로 종류·모델·벤더·Resource Class·Trait·연결 VM·사용 상태를 보여준다.

## 백엔드 — `MNT-IAAS/app/routers/flavors.py` (Cyborg 헬퍼 재사용)

신규: `GET /accelerator-devices?type=&vendor=&hostname=` → `{items, count}`

집계 소스:
- Cyborg `/devices` — `id`(kcloud 커스텀 정수 ID)·uuid·hostname·type·model·vendor
- Cyborg `/deployables` — device_id·rp_uuid
- Cyborg `/accelerator_requests`(key `arqs`) — instance_uuid·device_rp_uuid·state
- Placement `/resource_providers/{rp}/inventories` — Resource Class (키)
- Placement `/resource_providers/{rp}/traits` — Trait (CUSTOM_* 만 노출)
- Nova `/servers/detail?all_tenants=1` — instance_uuid → 이름

Placement 전용 microversion 헤더: `OpenStack-API-Version: placement 1.6`(traits 조회 1.6+). Cyborg 는 `2.2`.

매핑:
- ARQ.`device_rp_uuid` ↔ Deployable.`rp_uuid`
- Deployable.`device_id` ↔ Device.`id`(없으면 uuid 보조)

사용 상태 판정(가이드):
- `used`(사용 중): 장치 rp 에 바인딩된 VM(instance_uuid) ≥ 1
- `available`(사용 가능): 장치는 있고 바인딩 VM 없음
- `unknown`(알 수 없음): `/deployables` 또는 `/accelerator_requests` 조회 실패, 또는 장치에 매칭되는 rp 가 없음 → **절대 available 로 간주하지 않음**

응답 item: `{id, uuid, hostname, type, model, vendor, resourceClasses:[], traits:[], usage, instances:[{id, name}]}`.
필터는 type·vendor(대소문자 무시 일치)·hostname(정확 일치). pagination 없음.
조회 전용이라 `require_admin` 없음(일반 사용자 허용).

## 프론트

- `iaas-api.ts`: `AcceleratorDevice` 타입 + `useAcceleratorDevices(filters)` (폴링 주기 사용).
- 화면 `MNT-APP/src/app/(portal)/iaas/accelerator-devices/page.tsx`:
  - 상단 툴바(Card): type·vendor·hostname 필터 + 새로고침.
  - 테이블: **노드(hostname)별 그룹** 헤더 + 장치 행(종류 배지·모델·벤더·Resource Class·Trait 배지·**사용 상태 배지**·연결 VM 이름).
  - 사용 상태 색: used=호박/주의, available=초록, unknown=회색.
- `nav.ts`: IaaS 하위 "가속기 장치 현황" — **roles 미지정(사용자·관리자 공통)**.

## 검증

eslint·tsc·py_compile + 테스트베드 조회(장치 목록·사용 상태·연결 VM·필터). 쓰기 없음(조회 전용)이라 기존 자원에 영향 없음.

## 범위 밖(YAGNI)

장치 설정 변경(조회 전용), rp 수동 지정, 실시간 스트리밍.
