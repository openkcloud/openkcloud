# 키페어 이원화 — 사용자(포털 DB) / 관리자(Nova) 설계

작성: 2026-10-16 · 브랜치: CLOUDDEV-4664(또는 신규 PaaS/IaaS 브랜치) · 모듈: MNT-IAAS + MNT-APP

## 배경

"키페어"가 두 용도로 쓰이는데 현재는 한 종류(사용자 포털 DB SSH키)만 있다.

1. **일반 VM SSH 접속** — 사용자가 자원신청으로 받은 자기 VM 에 들어갈 때. (현재 구현: 포털 DB `iam_ssh_key`, 개인키는 브라우저 생성·로컬 다운로드, 서버 비노출)
2. **클러스터 노드 VM SSH 접속** — Magnum 이 노드(Control Plane·Worker)에 공개키를 심어 관리자가 노드에 직접 SSH. 이건 **Nova keypair(인프라 자원)** 이며, 지금 클러스터 템플릿 폼에서 `keypair_id` 를 **자유 입력**한다(실존 Nova 키와 불일치 위험).

두 키페어는 **별개 자원**이다(포털 DB SSH키는 Nova 에 없고, Nova keypair 는 포털 DB 에 없음). 클러스터 노드 접근(keypair)과 쿠버네티스 접근(kubeconfig)도 별개 레이어다.

운영 정책: 사용자는 VM 을 직접 받는 경우도, 노트북/클러스터만 소비하는 경우도 **둘 다 있다**(사용자별로 다름). 따라서 사용자 키페어(DB)는 유지한다.

## 설계

| 영역 | 경로/화면 | 역할 | 저장소 | 용도 |
|---|---|---|---|---|
| 사용자 키페어 | `/iaas/keypairs` (역할=user 화면) | user | 포털 DB `iam_ssh_key` | 내 VM SSH. 개인키 브라우저 생성·로컬 다운로드(서버 비노출) — 기존 그대로 |
| 관리자 Nova 키페어 | `/iaas/keypairs` (역할=admin 화면) | admin | **Nova (os-keypairs)** | 클러스터 노드 VM SSH 접속용 인프라 키 |
| 템플릿 keypair | 클러스터 템플릿 생성 폼 | admin | (Nova 참조) | 위 Nova 키페어 목록에서 **셀렉트**(자유입력 폐기), 비워도 됨(optional) |

**메뉴 구성**: 같은 "키페어" 메뉴 하나. 화면(`/iaas/keypairs`)이 `useAuth` 역할로 분기 — user 면 기존 DB 키페어 화면, admin 이면 Nova 키페어 화면. nav 의 `roles: ["user"]` → 제거(공통 노출), 메뉴 시드도 admin 매핑 추가.

**Nova 등록 방식**: **공개키 import 만**(개인키 서버 비노출 철학 유지). 생성(Nova 가 개인키 발급)은 하지 않는다.

## 구현

### 백엔드 — MNT-IAAS (신규 라우터 `routers/keypairs.py`, 관리자 전용)
- `GET /keypairs` — Nova `GET /os-keypairs` 목록 → `[{name, fingerprint, publicKey, type}]`
- `POST /keypairs` (admin) — 공개키 import. body `{name, publicKey}` → Nova `POST /os-keypairs` `{keypair:{name, public_key}}`
- `DELETE /keypairs/{name}` (admin) — Nova `DELETE /os-keypairs/{name}`
- Nova 호출은 flavors.py 의 `_nova` 와 같은 패턴(openstack_client / 엔드포인트 override). require_admin 가드.

### 프론트 — MNT-APP
- `iaas-api.ts`: `useNovaKeypairs`·`useImportNovaKeypair`·`useDeleteNovaKeypair` + `NovaKeypair` 타입.
- `/iaas/keypairs/page.tsx`: 최상위에서 역할 분기 — `role === "admin"` 이면 `<NovaKeypairsView/>`, 아니면 기존 사용자 화면. 기존 사용자 화면 로직은 그대로 유지(별도 컴포넌트로 추출).
- `NovaKeypairsView`(신규): 목록(이름·fingerprint·타입) + 공개키 import 다이얼로그 + 삭제(useConfirm). 플레이버 화면 규약.
- 템플릿 폼: keypair 자유입력 `Input` → `useNovaKeypairs` 드롭다운(DropSelect, 비움 허용).
- `nav.ts`: 키페어 항목 `roles` 제거(user·admin 공통).

### 메뉴 시드 — MNT-CORE
- `seed.sql`: `iaas.keypairs` 를 admin 역할 매핑에도 추가.
- 기존 DB 멱등 마이그레이션 `menu_keypair_admin.sql`: admin↔iaas.keypairs 매핑 INSERT(ON CONFLICT DO NOTHING).

## 에러 처리
- Nova import 중복 이름 → Nova 409 메시지 토스트. 삭제 사용중 등 → Nova 메시지 그대로.
- 템플릿에서 선택한 keypair 가 Nova 에서 삭제돼 없을 수 있음 — 드롭다운 목록 기준으로만 선택, 과거 값은 표시.

## 검증
- eslint·tsc(변경 파일)·py_compile.
- 테스트베드: 관리자 로그인 → 키페어 화면이 Nova 뷰로, 공개키 import/삭제. 사용자 로그인 → 기존 DB 키페어 뷰 그대로. 템플릿 폼에서 Nova 키페어 드롭다운 선택.

## 범위 밖(YAGNI)
- Nova 키페어 생성(개인키 발급) — import 만. 사용자↔Nova 키 동기화. 키페어로 쿠버네티스 접근(그건 kubeconfig).
