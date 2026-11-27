-- ════════════════════════════════════════════════════════════════
-- MNT-CORE 포털 DB 스키마 v0.2 — 공통코드 계층 · 조직 · 프로젝트 · 자원신청(VM) 티켓
--   선행: ddl.sql(v0.1) + seed.sql 적용 상태
--   적용: psql -U portal_user -d openkcloud_portal -f migration_v0.2_ticket.sql
--   원칙: 자원 실시간 상태는 저장하지 않는다. 티켓에는 신청/프로비저닝 입력값과 결과 식별자만 남긴다.
-- ════════════════════════════════════════════════════════════════

BEGIN;

-- ── 공통코드 계층 ────────────────────────────────────────────────
-- 상위 코드 참조: (upper_group_code, upper_code) → cmn_code(group_code, code).
-- 계층이 없는 코드는 둘 다 NULL. 예) 상세 단계 check_flavor 의 상위 = (TKT_PROVISION_STEP_IAAS, create_server)
ALTER TABLE cmn_code
    ADD COLUMN upper_group_code VARCHAR(50),
    ADD COLUMN upper_code       VARCHAR(50),
    ADD CONSTRAINT fk_cmn_code_upper FOREIGN KEY (upper_group_code, upper_code) REFERENCES cmn_code (group_code, code),
    ADD CONSTRAINT ck_cmn_code_upper CHECK ((upper_group_code IS NULL) = (upper_code IS NULL));
CREATE INDEX idx_cmn_code_upper ON cmn_code (upper_group_code, upper_code);
COMMENT ON COLUMN cmn_code.upper_group_code IS '상위 코드의 그룹 (계층 코드일 때)';
COMMENT ON COLUMN cmn_code.upper_code       IS '상위 코드 (계층 코드일 때)';

-- ── 조직 ─────────────────────────────────────────────────────────
-- 조직에는 슈퍼유저(조직 관리자)와 유저가 속한다. 플랫폼 관리자(admin 역할)는 모든 조직을 본다.
CREATE TABLE iam_org (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code            VARCHAR(50)  NOT NULL UNIQUE,     -- 안정 키 (영문 소문자/숫자/-)
    name            VARCHAR(100) NOT NULL,
    manager_user_id BIGINT REFERENCES iam_user(id) ON DELETE SET NULL,  -- 조직 슈퍼유저
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
COMMENT ON TABLE iam_org IS '조직. 사용자·프로젝트·키페어의 소속 단위';

-- 사용자 소속 조직 (기존 iam_user.org 문자열은 호환을 위해 유지)
ALTER TABLE iam_user ADD COLUMN org_id BIGINT REFERENCES iam_org(id) ON DELETE SET NULL;
CREATE INDEX idx_iam_user_org ON iam_user (org_id);

-- ── 프로젝트-조직 연결 (OpenStack 프로젝트 → 조직) ───────────────
-- 프로젝트 이름·설명·쿼터는 OpenStack 라이브 조회 — 여기에는 조직 할당만 둔다.
-- 프로젝트 멤버(사용자 소속)는 기존 iam_project_member(project_id = 같은 OpenStack UUID)가 원장.
-- (iam_project_member.project_id → iam_project FK 는 미설정: 멤버 기능 담당자와 합의 후 결정)
CREATE TABLE iam_project (
    project_id VARCHAR(64) PRIMARY KEY,                  -- OpenStack(Keystone) 프로젝트 UUID
    org_id     BIGINT      NOT NULL REFERENCES iam_org(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_iam_project_org ON iam_project (org_id);
COMMENT ON TABLE iam_project IS 'OpenStack 프로젝트의 조직 할당(포털 원장). 프로젝트 정보 자체는 OpenStack 라이브 조회';

-- ── 키페어 ───────────────────────────────────────────────────────
-- 기존 iam_ssh_key(owner = iam_user.username) 사용 — 이 마이그레이션에서 생성하지 않는다.
-- 신청자 키 목록(본인 키만): SELECT * FROM iam_ssh_key WHERE owner = :username

-- ── 자원신청 티켓 (1티켓 = VM 1대) ───────────────────────────────
CREATE TABLE tkt_request (
    id             VARCHAR(20) PRIMARY KEY,                 -- 신청 ID: req-YYMMDD-NNN (KST 날짜별 일련번호, MNT-CORE 가 생성)
    type           VARCHAR(20) NOT NULL DEFAULT 'vm',       -- cmn_code(TKT_TYPE): vm|cluster
    status         VARCHAR(20) NOT NULL DEFAULT 'pending',  -- cmn_code(TKT_STATUS)
    -- 티켓의 조직 = 프로젝트의 조직 (iam_project.org_id 를 조인해서 사용 — 별도 컬럼 없음)
    project_id     VARCHAR(64) NOT NULL REFERENCES iam_project(project_id),  -- OpenStack 프로젝트 UUID
    requested_by   BIGINT      NOT NULL REFERENCES iam_user(id),  -- 신청 가능: 해당 프로젝트 멤버 중 owner·member (API 확인)
    -- 사용자 입력: projectName(신청 시점 OpenStack 프로젝트 이름), vmName, networkType(internal|external), floatingIp(공인 IP 희망, 인터넷망일 때만),
    --              flavorId(Nova flavorRef)·flavorName·vcpu·ramGb·diskGb, deviceProfile(Cyborg device-profile, 가속기 없으면 생략),
    --              accelerators([{kind: GPU|NPU, count}] — device-profile groups 의 resources 합산, 가속기 없으면 생략),
    --              imageId·imageName·cloudInit(bool), volumeGb(플레이버 disk=0 일 때, 이미지 min_disk ~ 500),
    --              keypairId(iam_ssh_key.id)·keypairName(선택)
    --              vmName = 서버 이름 = hostname (영문 소문자·숫자·하이픈 2~63자, 프로젝트 내 중복 불가)
    request_spec   JSONB       NOT NULL,
    -- 관리자 입력(프로비저닝 시): networkId, networkName, networkKind(project|shared|external),
    --              floatingNetworkId/Name(내부·공유망 + Floating IP 시 FIP 풀),
    --              fixedIp(external 망 Static IP 직접 입력, 자동이면 생략), securityGroups[](이름)
    provision_spec JSONB,
    -- 프로비저닝 결과: serverId, volumeId, portId, floatingIp, fixedIp
    result         JSONB,
    -- 처리 기록: 승인 단계(승인 / 반려) · 프로비저닝 단계(시작 / 반려)
    approved_by                BIGINT REFERENCES iam_user(id),  -- 승인자 (대기 → 승인)
    approved_at                TIMESTAMPTZ,
    provisioning_by            BIGINT REFERENCES iam_user(id),  -- 프로비저닝 시작자 (승인 → 생성 중)
    provisioning_at            TIMESTAMPTZ,
    approved_reject_by         BIGINT REFERENCES iam_user(id),  -- 승인 단계 반려 (대기 → 반려)
    approved_reject_at         TIMESTAMPTZ,
    approved_reject_reason     TEXT,
    provisioning_reject_by     BIGINT REFERENCES iam_user(id),  -- 프로비저닝 단계 반려 (승인 → 반려)
    provisioning_reject_at     TIMESTAMPTZ,
    provisioning_reject_reason TEXT,
    workflow_id    VARCHAR(255),                            -- Temporal workflow id
    error_message  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_tkt_request_status       ON tkt_request (status);
CREATE INDEX idx_tkt_request_project      ON tkt_request (project_id);
CREATE INDEX idx_tkt_request_requested_by ON tkt_request (requested_by);
COMMENT ON TABLE tkt_request IS '자원신청 티켓. 신청·승인 입력값과 프로비저닝 결과 식별자';

-- ── 프로비저닝 진행 단계 ─────────────────────────────────────────
-- 단계 = cmn_code(TKT_PROVISION_STEP_IAAS) 5개 고정: create_server · attach_network · attach_volume · bind_accelerator · wait_active
-- 워크플로 시작(=회차 시작) 시 5개를 waiting 으로 선등록 → Nova 서버 task_state/vm_state 폴링으로 running/done/failed 갱신.
-- 가속기 없는 플레이버는 bind_accelerator = skipped (상세 단계 행 없음).
-- attempt: 실행 회차. 승인 후 첫 실행 1, 재시도마다 +1 로 단계 행을 새로 등록(이전 회차는 이력으로 보존).
--          진행 팝업은 티켓의 MAX(attempt) 회차를 표시.
CREATE TABLE tkt_provision_step (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id      VARCHAR(20)  NOT NULL REFERENCES tkt_request(id) ON DELETE CASCADE,
    attempt         INT          NOT NULL DEFAULT 1,    -- 실행 회차 (재시도마다 +1)
    seq             INT          NOT NULL,              -- 단계 순서 (cmn_code.sort_order 기준 1~5)
    step_group_code VARCHAR(50)  NOT NULL DEFAULT 'TKT_PROVISION_STEP_IAAS',
    step_code       VARCHAR(50)  NOT NULL,              -- cmn_code(step_group_code)
    status          VARCHAR(20)  NOT NULL DEFAULT 'waiting',  -- cmn_code(TKT_STEP_STATUS)
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    error_message   TEXT,
    UNIQUE (request_id, attempt, seq),
    FOREIGN KEY (step_group_code, step_code) REFERENCES cmn_code (group_code, code),
    CHECK (step_group_code LIKE 'TKT\_PROVISION\_STEP\_%')        -- 단계 그룹만 (상세 단계 코드 혼입 방지)
);
COMMENT ON TABLE tkt_provision_step IS '티켓별 프로비저닝 진행 단계 (진행 팝업의 큰 단계)';

-- ── 프로비저닝 상세 단계 ─────────────────────────────────────────
-- 상세 단계 = cmn_code(TKT_PROVISION_SUBSTEP_IAAS). 상위 단계는 cmn_code.upper_code 로 매핑.
-- 단계 행 등록 시 해당 단계의 상세 단계 중 조건에 맞는 것만 waiting 으로 함께 등록
--   (예: 가속기 없으면 bind_accelerator 상세 단계 없음 / Floating IP 시에만 attach_fip). 부팅 디스크는 항상 Cinder 부팅 볼륨.
-- substep_code 의 상위 코드 = 부모 행 step_code 인지는 애플리케이션(워크플로)이 보장한다.
CREATE TABLE tkt_provision_substep (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    step_id            BIGINT       NOT NULL REFERENCES tkt_provision_step(id) ON DELETE CASCADE,
    seq                INT          NOT NULL,           -- 단계 내 순서 (cmn_code.sort_order 기준)
    substep_group_code VARCHAR(50)  NOT NULL DEFAULT 'TKT_PROVISION_SUBSTEP_IAAS',
    substep_code       VARCHAR(50)  NOT NULL,           -- cmn_code(substep_group_code)
    value              VARCHAR(255),                    -- 실제 값 (예: port-3c91, 192.168.110.142, 202 Accepted)
    status             VARCHAR(20)  NOT NULL DEFAULT 'waiting',  -- cmn_code(TKT_STEP_STATUS)
    started_at         TIMESTAMPTZ,
    ended_at           TIMESTAMPTZ,
    error_message      TEXT,
    UNIQUE (step_id, seq),
    FOREIGN KEY (substep_group_code, substep_code) REFERENCES cmn_code (group_code, code),
    CHECK (substep_group_code LIKE 'TKT\_PROVISION\_SUBSTEP\_%')  -- 상세 단계 그룹만
);
COMMENT ON TABLE tkt_provision_substep IS '프로비저닝 상세 단계별 실제 값·상태 (진행 팝업의 세부 항목)';

-- ── updated_at 자동 갱신 ─────────────────────────────────────────
CREATE TRIGGER trg_iam_org_updated     BEFORE UPDATE ON iam_org     FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_iam_project_updated BEFORE UPDATE ON iam_project FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER trg_tkt_request_updated BEFORE UPDATE ON tkt_request FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ── 공통코드 ─────────────────────────────────────────────────────
-- 티켓 상태: 요청 → 승인 → 생성중 → 완료/실패, 반려
INSERT INTO cmn_code (group_code, code, name, sort_order, attr1) VALUES
    ('TKT_STATUS', 'approved', '승인',  15, 'info'),
    ('TKT_STATUS', 'failed',   '실패',  35, 'destructive');
UPDATE cmn_code SET name = '생성 중' WHERE group_code = 'TKT_STATUS' AND code = 'provisioning';

INSERT INTO cmn_code_group (group_code, name, description) VALUES
    ('TKT_PROVISION_STEP_IAAS',    '프로비저닝 단계',      'VM 프로비저닝 진행 팝업의 단계'),
    ('TKT_PROVISION_SUBSTEP_IAAS', '프로비저닝 상세 단계', 'VM 프로비저닝 단계별 상세 단계 (upper_code = 상위 단계)'),
    ('TKT_STEP_STATUS', '프로비저닝 단계 상태', '프로비저닝 진행 단계별 상태'),
    ('TKT_NETWORK_TYPE', '희망 네트워크 유형', '사용자가 신청 시 선택하는 망 구분');
INSERT INTO cmn_code (group_code, code, name, sort_order, attr1) VALUES
    ('TKT_STEP_STATUS',  'waiting',  '대기',   10, 'secondary'),
    ('TKT_STEP_STATUS',  'running',  '진행 중', 20, 'info'),
    ('TKT_STEP_STATUS',  'done',     '완료',   30, 'success'),
    ('TKT_STEP_STATUS',  'failed',   '실패',   40, 'destructive'),
    ('TKT_STEP_STATUS',  'skipped',  '건너뜀', 50, 'secondary'),
    ('TKT_NETWORK_TYPE', 'internal', '내부망', 10, NULL),
    ('TKT_NETWORK_TYPE', 'external', '인터넷망', 20, NULL);

-- 프로비저닝 단계 (상위)
INSERT INTO cmn_code (group_code, code, name, sort_order) VALUES
    ('TKT_PROVISION_STEP_IAAS', 'create_server',    '가상머신 생성', 10),
    ('TKT_PROVISION_STEP_IAAS', 'attach_network',   '네트워크 연결', 20),
    ('TKT_PROVISION_STEP_IAAS', 'attach_volume',    '스토리지 연결', 30),
    ('TKT_PROVISION_STEP_IAAS', 'bind_accelerator', '가속기 바인딩', 40),
    ('TKT_PROVISION_STEP_IAAS', 'wait_active',      '부팅 완료',     50);

-- 프로비저닝 상세 단계 (하위) — sort_order 는 상위 단계 안에서의 순서
INSERT INTO cmn_code (group_code, code, name, sort_order, upper_group_code, upper_code) VALUES
    ('TKT_PROVISION_SUBSTEP_IAAS', 'check_flavor',         '플레이버 확인',      10, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'check_image',          '이미지 확인',        20, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'set_keypair',          '키페어 지정',        30, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'create_boot_volume',   '부팅 볼륨 생성',     40, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'send_request',         '생성 요청 전송',     50, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'schedule_host',        '호스트 스케줄링',    60, 'TKT_PROVISION_STEP_IAAS', 'create_server'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'check_network',        '네트워크 확인',      10, 'TKT_PROVISION_STEP_IAAS', 'attach_network'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'create_port',          '포트 생성',          20, 'TKT_PROVISION_STEP_IAAS', 'attach_network'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'assign_ip',            'IP 할당',            30, 'TKT_PROVISION_STEP_IAAS', 'attach_network'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'apply_secgroup',       '보안그룹 적용',      40, 'TKT_PROVISION_STEP_IAAS', 'attach_network'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'attach_boot_volume',   '볼륨 연결',          10, 'TKT_PROVISION_STEP_IAAS', 'attach_volume'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'check_mount',          '마운트 확인',        20, 'TKT_PROVISION_STEP_IAAS', 'attach_volume'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'allocate_accelerator', '가속기 할당',        10, 'TKT_PROVISION_STEP_IAAS', 'bind_accelerator'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'bind_arq',             '가속기 요청(ARQ) 바인딩', 20, 'TKT_PROVISION_STEP_IAAS', 'bind_accelerator'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'spawn_instance',       '인스턴스 기동',      10, 'TKT_PROVISION_STEP_IAAS', 'wait_active'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'check_active',         '상태 확인',          20, 'TKT_PROVISION_STEP_IAAS', 'wait_active'),
    ('TKT_PROVISION_SUBSTEP_IAAS', 'attach_fip',           'Floating IP 연결',   30, 'TKT_PROVISION_STEP_IAAS', 'wait_active');

-- ── 시드: 조직 · 사용자 소속 · 프로젝트 조직 할당 ─────────────────
INSERT INTO iam_org (code, name) VALUES
    ('etri',     'ETRI'),
    ('okestro',  '오케스트로');
-- 조직 미지정 사용자 → 오케스트로 (iam_user.org 문자열 기준으로 org_id 연결)
UPDATE iam_user SET org = '오케스트로' WHERE username IN ('ek.lee', 'gy.yang', 'jw.lee') AND org IS NULL;
UPDATE iam_user u SET org_id = o.id FROM iam_org o WHERE o.name = u.org;

-- okc_test_prj (포털 개발용 테스트 프로젝트) → 오케스트로
INSERT INTO iam_project (project_id, org_id)
    SELECT '4ee114f1b9f1423a91bf40dec2fd05fb', id FROM iam_org WHERE code = 'okestro';

COMMIT;

-- ── 롤백 (v0.2 적용 전으로) ──────────────────────────────────────
-- BEGIN;
-- DROP TABLE IF EXISTS tkt_provision_substep, tkt_provision_step, tkt_request, iam_project;
-- ALTER TABLE iam_user DROP COLUMN IF EXISTS org_id;
-- UPDATE iam_user SET org = NULL WHERE username IN ('ek.lee', 'gy.yang', 'jw.lee') AND org = '오케스트로';  -- 시드로 채운 조직 문자열
-- DROP TABLE IF EXISTS iam_org;
-- DELETE FROM cmn_code WHERE group_code = 'TKT_PROVISION_SUBSTEP_IAAS';   -- 하위 코드 먼저 (상위 FK)
-- DELETE FROM cmn_code_group WHERE group_code IN ('TKT_PROVISION_SUBSTEP_IAAS', 'TKT_PROVISION_STEP_IAAS', 'TKT_STEP_STATUS', 'TKT_NETWORK_TYPE');
-- DELETE FROM cmn_code WHERE group_code = 'TKT_STATUS' AND code IN ('approved', 'failed');
-- UPDATE cmn_code SET name = '프로비저닝 중' WHERE group_code = 'TKT_STATUS' AND code = 'provisioning';
-- ALTER TABLE cmn_code DROP CONSTRAINT IF EXISTS fk_cmn_code_upper, DROP CONSTRAINT IF EXISTS ck_cmn_code_upper,
--                      DROP COLUMN IF EXISTS upper_group_code, DROP COLUMN IF EXISTS upper_code;
-- COMMIT;
