-- ════════════════════════════════════════════════════════════════
-- MNT-CORE 포털 DB 스키마 v0.1 — 사용자 · 권한(역할) · 메뉴
--   대상: PostgreSQL 17 (openkcloud_portal)
--   원칙: 포털 고유 데이터만 저장. 자원 실시간 상태는 저장하지 않는다(인프라 라이브 조회).
--   적용: psql -U portal_user -d openkcloud_portal -f ddl.sql
--
--   [명명 규칙]
--   · 테이블 = <도메인 접두어>_<단수 명사>, 소문자 snake_case
--     도메인: iam_(사용자·권한) mnu_(메뉴) tkt_(자원신청) mkt_(마켓) aud_(감사) cmn_(공통코드)
--   · PK = id, FK = <참조 단수>_id, 시간 = created_at/updated_at
--   · 인덱스 = idx_<테이블>_<컬럼>, 트리거 = trg_<테이블>_<용도>
-- ════════════════════════════════════════════════════════════════

-- 시드의 비밀번호 해시(bcrypt)에 사용
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ── 공통코드: 그룹 ───────────────────────────────────────────────
-- 운영 중 추가·변경될 수 있는 업무 코드의 원장. (스키마 구조에 묶인
-- 고정 enum — 예: mnu_menu.menu_type — 은 CHECK 로 유지한다.)
CREATE TABLE cmn_code_group (
    group_code  VARCHAR(50)  PRIMARY KEY,          -- 예: TKT_STATUS (대문자 스네이크)
    name        VARCHAR(100) NOT NULL,
    description TEXT,
    is_active   BOOLEAN      NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE cmn_code_group IS '공통코드 그룹 (업무 코드 분류 단위)';

-- ── 공통코드: 상세 ───────────────────────────────────────────────
-- 사용처 테이블은 code 문자열만 저장한다(어느 그룹인지 컬럼 주석으로 명시).
CREATE TABLE cmn_code (
    group_code  VARCHAR(50)  NOT NULL REFERENCES cmn_code_group(group_code) ON DELETE CASCADE,
    code        VARCHAR(50)  NOT NULL,             -- 로직 참조용 안정 키 (소문자)
    name        VARCHAR(100) NOT NULL,             -- 화면 표시명
    description TEXT,
    sort_order  INT          NOT NULL DEFAULT 0,
    is_active   BOOLEAN      NOT NULL DEFAULT true,
    attr1       VARCHAR(100),                      -- 여분 속성 1 (예: 상태 배지 톤)
    attr2       VARCHAR(100),                      -- 여분 속성 2
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (group_code, code)
);
COMMENT ON TABLE cmn_code IS '공통코드 상세. 상태·유형 등 업무 코드의 값과 표시명';

-- ── 역할 (권한 그룹) ──────────────────────────────────────────────
-- 목업 기준 admin/user 2종. IAM 화면의 역할 추가에 대비해 테이블로 관리.
CREATE TABLE iam_role (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code        VARCHAR(50)  NOT NULL UNIQUE,     -- 'admin' | 'user' (로직 참조용 안정 키)
    name        VARCHAR(100) NOT NULL,            -- '관리자' | '사용자'
    description TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE iam_role IS '역할(권한 그룹). 메뉴 노출과 API 인가의 기준 단위';

-- ── 사용자 ────────────────────────────────────────────────────────
CREATE TABLE iam_user (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      VARCHAR(100) NOT NULL UNIQUE,
    password_hash TEXT         NOT NULL,          -- bcrypt(pgcrypto crypt) / 추후 Keycloak 전환 시 미사용
    name          VARCHAR(100) NOT NULL,          -- 표시 이름
    email         VARCHAR(255) UNIQUE,
    org           VARCHAR(100),                   -- 소속 (ETRI, 오케스트로 …)
    status        VARCHAR(20)  NOT NULL DEFAULT 'active',  -- cmn_code(IAM_USER_STATUS): active|locked|disabled
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE iam_user IS '포털 계정. Keycloak 도입 후에는 sub 매핑 테이블로 확장';

-- ── 사용자-역할 매핑 (M:N) ───────────────────────────────────────
-- 목업은 1인 1역할이지만 역할 확장을 고려해 M:N.
CREATE TABLE iam_user_role (
    user_id    BIGINT NOT NULL REFERENCES iam_user(id) ON DELETE CASCADE,
    role_id    BIGINT NOT NULL REFERENCES iam_role(id) ON DELETE CASCADE,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);

-- ── 사용자 SSH 공개키 (키페어) ────────────────────────────────────
-- 사용자 self-service. 개인키(private)는 절대 저장하지 않는다(브라우저에서만 1회 다운로드).
-- owner = 포털 사용자 식별자(현재 iam_user.username, Keycloak 도입 후 sub). 프로젝트가 아니라 "사용자" 스코프.
CREATE TABLE iam_ssh_key (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner       VARCHAR(100) NOT NULL,            -- 포털 사용자(username/sub)
    name        VARCHAR(100) NOT NULL,            -- 사용자가 붙인 키 이름
    public_key  TEXT         NOT NULL,            -- OpenSSH authorized_keys 형식(ssh-rsa/ed25519 …)
    fingerprint VARCHAR(120) NOT NULL,            -- SHA256:...
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);
COMMENT ON TABLE iam_ssh_key IS '사용자 SSH 공개키. 개인키 미저장(브라우저 전용). owner=iam_user.username→Keycloak sub';

-- ── 프로젝트 멤버 (포털 관리, OpenStack 멤버와 별개) ────────────────
-- project_id = OpenStack Keystone 프로젝트 UUID(포털 DB엔 프로젝트 원장이 없으므로 문자열 참조).
-- role = 포털 레벨 역할(owner|member|viewer) — OpenStack Keystone role assignment 와 무관.
CREATE TABLE iam_project_member (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,                       -- OpenStack 프로젝트 UUID
    user_id    BIGINT NOT NULL REFERENCES iam_user(id) ON DELETE CASCADE,
    role       VARCHAR(20) NOT NULL DEFAULT 'member',      -- owner | member | viewer
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, user_id)
);
COMMENT ON TABLE iam_project_member IS '프로젝트-사용자 멤버십(포털). project_id=OpenStack UUID, role=포털 역할(OpenStack과 별개)';

-- ── PaaS 서비스 클러스터 접근 권한 (프로젝트 ↔ 클러스터 N:N) ──────
-- 서비스 클러스터는 인프라상 OpenStack 프로젝트 1개 소유(Magnum project_id)지만,
-- 포털에선 여러 프로젝트에 "볼 권한"을 부여한다(소유 ≠ 사용권한). 가시성은 이 매핑으로 필터한다.
-- namespace = NULL 이면 클러스터 전체(전용), 값이 있으면 그 네임스페이스만(공유) — 내부 격리용(2단계).
CREATE TABLE paas_project_cluster (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,                       -- OpenStack 프로젝트 UUID(불변 키). 표시명은 조인으로
    cluster_id VARCHAR(64) NOT NULL,                       -- Magnum 클러스터 UUID
    namespace  VARCHAR(253),                               -- NULL=클러스터 전체(전용), 값=그 네임스페이스만(공유)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, cluster_id, namespace)
);
CREATE INDEX idx_paas_project_cluster_cluster ON paas_project_cluster (cluster_id);
CREATE INDEX idx_paas_project_cluster_project ON paas_project_cluster (project_id);
COMMENT ON TABLE paas_project_cluster IS 'PaaS 서비스 클러스터 접근 권한(프로젝트↔클러스터 N:N). namespace NULL=전용, 값=공유 네임스페이스';

-- ── 메뉴 (계층형: 섹션 → 메뉴 → 서브메뉴) ────────────────────────
-- 프론트 nav.ts 의 정적 정의를 DB 원장으로. 같은 path 라도 배치가 다르면
-- 별도 행이다(예: /iaas/networks 는 사용자 IaaS 하위와 관리자 구성 섹션에 각각 존재).
CREATE TABLE mnu_menu (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    parent_id  BIGINT REFERENCES mnu_menu(id) ON DELETE CASCADE,
    menu_type  VARCHAR(10)  NOT NULL DEFAULT 'menu'
               CHECK (menu_type IN ('section', 'menu', 'submenu')),
    code       VARCHAR(100) NOT NULL UNIQUE,     -- 시드·프론트 매핑용 안정 식별자
    label      VARCHAR(100) NOT NULL,            -- 기본 라벨 (역할별 라벨은 mnu_role_menu.label_override)
    path       VARCHAR(255),                     -- section 은 NULL
    icon       VARCHAR(50),                      -- lucide-react 아이콘 이름
    sort_order INT          NOT NULL DEFAULT 0,  -- 기본 순서 (역할별 순서는 mnu_role_menu.sort_order)
    is_active  BOOLEAN      NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_mnu_menu_parent ON mnu_menu (parent_id);
COMMENT ON TABLE mnu_menu IS 'LNB 메뉴 원장(계층형). 역할별 노출·순서·라벨은 mnu_role_menu 가 결정';

-- ── 역할-메뉴 매핑 ───────────────────────────────────────────────
-- 목업의 역할별 차이를 그대로 표현한다:
--   · 노출 여부   = 행 존재 여부 (사용자/관리자 메뉴 구성이 다름)
--   · 역할별 순서 = sort_order   (섹션 순서가 역할마다 다름: 사용자는 자원 우선, 관리자는 옵저버빌리티 우선)
--   · 역할별 라벨 = label_override (/ticket/requests: 사용자 "자원 요청 관리" / 관리자 "자원 신청 관리")
CREATE TABLE mnu_role_menu (
    role_id        BIGINT NOT NULL REFERENCES iam_role(id) ON DELETE CASCADE,
    menu_id        BIGINT NOT NULL REFERENCES mnu_menu(id) ON DELETE CASCADE,
    sort_order     INT,                           -- NULL 이면 mnu_menu.sort_order 사용
    label_override VARCHAR(100),                  -- NULL 이면 mnu_menu.label 사용
    PRIMARY KEY (role_id, menu_id)
);
COMMENT ON TABLE mnu_role_menu IS '역할별 메뉴 노출·정렬·라벨 오버라이드';

-- ── updated_at 자동 갱신 ─────────────────────────────────────────
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_iam_role_updated       BEFORE UPDATE ON iam_role       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_iam_user_updated       BEFORE UPDATE ON iam_user       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_mnu_menu_updated       BEFORE UPDATE ON mnu_menu       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_cmn_code_group_updated BEFORE UPDATE ON cmn_code_group FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_cmn_code_updated       BEFORE UPDATE ON cmn_code       FOR EACH ROW EXECUTE FUNCTION set_updated_at();
