-- ════════════════════════════════════════════════════════════════
-- AI 워크스페이스 신청(tkt_request.type = 'ai-workspace') 공통코드 — 기존 포털 DB 에 1회 적용
--   적용: psql -U portal_user -d openkcloud_portal -f ai_workspace.sql   (여러 번 돌려도 같음)
--   ※ 저장소의 ddl.sql 에는 tkt_* 테이블과 cmn_code.upper_code 가 아직 없어, 운영 DB 기준
--     (VM 프로비저닝 코드가 쓰는 컬럼: cmn_code.upper_code, tkt_provision_step.step_group_code,
--      tkt_provision_substep.substep_group_code)으로 작성했다.
-- ════════════════════════════════════════════════════════════════
BEGIN;

INSERT INTO cmn_code (group_code, code, name, sort_order) VALUES
    ('TKT_TYPE', 'ai-workspace', 'AI 워크스페이스', 30)
ON CONFLICT (group_code, code) DO NOTHING;

INSERT INTO cmn_code_group (group_code, name, description) VALUES
    ('TKT_PROVISION_STEP_AINB',    'AI 워크스페이스 생성 단계',      'AiWorkspaceProvision 워크플로 진행 단계'),
    ('TKT_PROVISION_SUBSTEP_AINB', 'AI 워크스페이스 생성 상세 단계', 'AiWorkspaceProvision 워크플로 상세 단계(upper_code = 단계)')
ON CONFLICT (group_code) DO NOTHING;

INSERT INTO cmn_code (group_code, code, name, sort_order, upper_group_code, upper_code) VALUES
    ('TKT_PROVISION_STEP_AINB',    'check_request',    '신청 확인',              10, NULL,                       NULL),
    ('TKT_PROVISION_STEP_AINB',    'create_workspace', '워크스페이스 생성',      20, NULL,                       NULL),
    ('TKT_PROVISION_STEP_AINB',    'check_ready',      '사용 준비 확인',         30, NULL,                       NULL),
    ('TKT_PROVISION_SUBSTEP_AINB', 'check_name',       '워크스페이스 이름',      10, 'TKT_PROVISION_STEP_AINB', 'check_request'),
    ('TKT_PROVISION_SUBSTEP_AINB', 'check_quota',      '자원 한도',              20, 'TKT_PROVISION_STEP_AINB', 'check_request'),
    ('TKT_PROVISION_SUBSTEP_AINB', 'create_profile',   'Profile·네임스페이스 생성', 30, 'TKT_PROVISION_STEP_AINB', 'create_workspace'),
    ('TKT_PROVISION_SUBSTEP_AINB', 'apply_quota',      '자원 한도 적용',         40, 'TKT_PROVISION_STEP_AINB', 'create_workspace'),
    ('TKT_PROVISION_SUBSTEP_AINB', 'check_namespace',  '네임스페이스 활성',      50, 'TKT_PROVISION_STEP_AINB', 'check_ready')
ON CONFLICT (group_code, code) DO NOTHING;

COMMIT;
