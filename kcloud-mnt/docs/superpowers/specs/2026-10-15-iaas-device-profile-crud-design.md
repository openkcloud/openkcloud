# IaaS 디바이스 프로파일(Cyborg) 관리 — 별도 메뉴 CRUD 설계

작성: 2026-10-15 · 브랜치: `CLOUDDEV-4663` · 모듈: MNT-IAAS(백엔드) + MNT-APP(프론트)

## 배경·목표

플레이버는 가속기를 `extra_specs.accel:device_profile` = Cyborg device-profile 로 가리킨다. 현재 포털은
device-profile 을 **목록 조회(선택용)** 만 하고, 프로파일 자체를 **생성·삭제할 수 없다**(Cyborg 에 직접 만들어야 함).
이걸 포털에서 관리하도록 **별도 메뉴(관리자 전용)** 로 CRUD 를 추가한다.

- device-profile 은 Cyborg 독립 자원이고 **1개 프로파일 : N개 플레이버**(재사용)이므로 플레이버 화면이 아니라 전용 화면이 맞다.
- Cyborg device_profile 은 **불변**(update API 없음) → **목록·생성·삭제**만. 변경은 삭제 후 재생성.

> ⚠️ 운영 안전: 테스트베드에 이미 존재하는 device-profile 은 **절대 삭제·변경하지 않는다**. 삭제는 화면에서 사용자가 명시적으로 실행할 때만 동작하는 기능이며, 구현·검증 과정에서 기존 프로파일에 어떤 쓰기도 하지 않는다.

## Cyborg 자료구조

device_profile = `{ name, uuid, description?, groups: [ { "resources:<class>": "<count>", "trait:<name>": "required|forbidden", ... } ] }`

- `resources:<class>` — 가속기 리소스 클래스와 개수. 알려진 매핑: `PGPU`=GPU, `CUSTOM_AICHIP`=NPU (그 외 class 는 이름 그대로).
- `trait:<name>` — 특정 하드웨어 지정. 값은 `required` 또는 `forbidden`.
- group 은 여러 개 가능(요청 슬롯 단위). 보통 1개.

## 백엔드 — `MNT-IAAS/app/routers/flavors.py`

`_cyborg` 헬퍼와 `/device-profiles` 목록이 이미 이 파일에 있어 같은 파일에 추가한다.

1. `GET /device-profiles` **강화**: 기존 `{name, uuid}` → `{name, uuid, description, accelerators: [{kind, count}], groups}`.
   `accelerators` 는 기존 `_dp_accelerators(groups)` 재사용.
2. `POST /device-profiles` (admin): body
   ```
   { name, description?, groups: [ { resources: [{resourceClass, count}], traits: [{name, policy}] } ] }
   ```
   → Cyborg 그룹 dict 로 변환: `{"resources:<class>": "<count>", "trait:<name>": "<policy>"}`.
   Cyborg POST 는 **리스트 바디**(`[{...}]`)를 받는다 — `[payload]` 로 전송하고 응답의 생성 결과를 돌려준다.
   (※ 테스트베드 Cyborg 버전에 따라 단일 객체일 수 있어 배포 검증에서 확인)
3. `DELETE /device-profiles/{uuid}` (admin): `DELETE /v2/device_profiles/{uuid}`.

검증: 이름 필수, group 최소 1개, 각 group 에 resource 최소 1개(count ≥ 1).

## 프론트 — `MNT-APP/src/lib/iaas-api.ts`

- `DeviceProfile` 타입 **확장**(선택 필드 추가): `{ name, uuid, description?, accelerators?: {kind,count}[], groups?: Record<string,string>[] }`.
  → 기존 플레이버 선택(`useDeviceProfiles`, name/uuid 만 읽음)과 관리 화면이 **같은 쿼리를 공유**(중복 호출 없음).
- `useDeviceProfiles()` 유지(쿼리키 `["iaas-device-profiles"]`).
- `CreateDeviceProfileInput` + `useCreateDeviceProfile()`·`useDeleteDeviceProfile()` 추가 — 성공 시 `["iaas-device-profiles"]`·`["iaas-flavors"]` 무효화(플레이버의 `deviceProfileMissing` 재평가).

## 프론트 — 화면 `MNT-APP/src/app/(portal)/iaas/device-profiles/page.tsx` (신규)

- 레이아웃: 플레이버 화면 규약(툴바 Card + 테이블 Card), 관리자 전용.
- 테이블: 이름 · 가속기(종류/개수 배지) · 설명 · 연결 플레이버 수(`useFlavors` 로 `accelerator===name` 집계) · uuid.
- 생성 다이얼로그(폼):
  - 이름(필수), 설명(선택)
  - **그룹[]**(기본 1개, "+ 그룹 추가"/삭제). 각 그룹:
    - 리소스 행[]: 리소스 클래스(드롭다운 `PGPU`/`CUSTOM_AICHIP` + 직접 입력) + 개수
    - 트레잇 행[]: 트레잇 이름 + required/forbidden (고급, 선택)
- 삭제: `useConfirm` 확인창 후 `useDeleteDeviceProfile`. **연결된 플레이버가 있으면 경고**(삭제 시 그 플레이버가 `deviceProfileMissing` 이 됨)하고 그래도 진행은 사용자 선택.

## 프론트 — `MNT-APP/src/lib/nav.ts`

IaaS children 에 추가: `{ label: "디바이스 프로파일", href: "/iaas/device-profiles", icon: Cpu, roles: ["admin"] }` (플레이버 다음).

## 에러 처리

- 생성 검증 실패 → 400, 화면 토스트. Cyborg 오류 → 502 메시지 그대로 토스트.
- 삭제 실패(사용 중 등) → Cyborg 상태·메시지 토스트.

## 검증

- eslint(변경 파일) + `tsc`(변경 파일 클린) + `py_compile`.
- 테스트베드 수동: 목록 표시, **새** 프로파일 생성(GPU count·NPU count·trait 포함), 플레이버 생성에서 그 프로파일 선택 가능, 생성한 프로파일 삭제. **기존 프로파일은 건드리지 않음.**

## 범위 밖(YAGNI)

- 수정(Cyborg 불변). 플레이버 화면 변경(선택 로직 그대로). device-profile 과 flavor 자동 연결.
