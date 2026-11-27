-- ════════════════════════════════════════════════════════════════
-- MNT-CORE 포털 DB 스키마 v0.3 — Keycloak 로그인 연동(사용자 연결 · 로그인 세션)
--   선행: ddl.sql(v0.1) + seed.sql + migration_v0.2_ticket.sql 적용 상태
--   적용: psql -U portal_user -d openkcloud_portal -v ON_ERROR_STOP=1 -f migration_v0.3_auth.sql
--   사용자는 포털 DB 가 기준(역할·소속). Keycloak 은 로그인(누구인가)만 맡고, 토큰의
--   preferred_username ↔ iam_user.username 으로 연결한 뒤 sub 를 저장한다.
-- ════════════════════════════════════════════════════════════════

BEGIN;

-- ── 포털 사용자 ↔ Keycloak 사용자 ────────────────────────────────
ALTER TABLE iam_user
    ADD COLUMN keycloak_sub VARCHAR(64) UNIQUE,
    ALTER COLUMN password_hash DROP NOT NULL;
COMMENT ON COLUMN iam_user.keycloak_sub  IS 'Keycloak 사용자 ID(토큰 sub). 첫 로그인 때 username 으로 찾아 저장';
COMMENT ON COLUMN iam_user.password_hash IS '포털 DB 비밀번호 로그인(전환 전)용. Keycloak 전환 후 미사용 → 정리 예정';

-- ── 로그인 세션 (Keycloak 세션 sid 단위 — 같은 브라우저의 탭들은 한 세션) ──
-- 끝난 세션의 토큰은 만료 전이라도 거부한다(관리자 강제 로그아웃·다른 곳 로그인·로그아웃).
CREATE TABLE iam_user_session (
    sid        VARCHAR(64) PRIMARY KEY,                 -- Keycloak 세션 ID(토큰 sid)
    user_id    BIGINT      NOT NULL REFERENCES iam_user(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),      -- CORE 가 이 세션을 처음 본 시각
    ended_at   TIMESTAMPTZ,                             -- NULL = 사용 중
    end_reason VARCHAR(50)                              -- cmn_code(IAM_SESSION_END_REASON)
);
CREATE INDEX idx_iam_user_session_active ON iam_user_session (user_id) WHERE ended_at IS NULL;
ALTER TABLE iam_user_session OWNER TO portal_user;   -- 슈퍼유저로 적용해도 CORE(portal_user)가 쓸 수 있게
COMMENT ON TABLE iam_user_session IS '포털 로그인 세션(Keycloak sid). 끝난 세션은 원인과 함께 남겨 화면 안내·토큰 거부에 쓴다';

INSERT INTO cmn_code_group (group_code, name, description) VALUES
    ('IAM_SESSION_END_REASON', '세션 종료 원인', '로그인 세션이 끝난 원인 — 화면 안내 문구 선택에 사용');
INSERT INTO cmn_code (group_code, code, name, description, sort_order) VALUES
    ('IAM_SESSION_END_REASON', 'logout',    '로그아웃',           NULL, 10),
    ('IAM_SESSION_END_REASON', 'admin',     '관리자 강제 종료',   NULL, 20),
    ('IAM_SESSION_END_REASON', 'duplicate', '다른 곳에서 로그인', NULL, 30),
    ('IAM_SESSION_END_REASON', 'idle',      '미사용 시간 초과',   'session idle timeout — 입력 없이 Keycloak 미사용 시간이 지남', 40);

COMMIT;

-- ── 확인 ──────────────────────────────────────────────────────────
-- \d iam_user                → keycloak_sub(UNIQUE) 있음, password_hash 의 not null 없음
-- \d iam_user_session        → 테이블 + idx_iam_user_session_active
-- SELECT code, name FROM cmn_code WHERE group_code = 'IAM_SESSION_END_REASON' ORDER BY sort_order;  → 4행

-- ── 롤백 (v0.3 적용 전으로) ──────────────────────────────────────
-- BEGIN;
-- DROP TABLE IF EXISTS iam_user_session;
-- DELETE FROM cmn_code_group WHERE group_code = 'IAM_SESSION_END_REASON';   -- cmn_code 는 CASCADE
-- ALTER TABLE iam_user DROP COLUMN IF EXISTS keycloak_sub;
-- ALTER TABLE iam_user ALTER COLUMN password_hash SET NOT NULL;            -- 비밀번호 없는 사용자가 생겼다면 먼저 채울 것
-- COMMIT;
