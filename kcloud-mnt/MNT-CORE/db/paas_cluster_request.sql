-- ════════════════════════════════════════════════════════════════
-- 서비스 클러스터 신청(tkt_request.type = 'cluster') 진행 단계 공통코드 — 기존 포털 DB 에 1회 적용
--   적용: psql -U portal_user -d openkcloud_portal -f paas_cluster_request.sql   (여러 번 돌려도 같음)
--   TKT_TYPE 의 'cluster' 는 seed.sql 에 이미 있다.
--   KubernetesProvision 워크플로(core.init_steps_paas)가 이 순서(sort_order)대로 단계를 만든다.
--   2026-10-21: '클러스터 생성' 상세 단계 세분화(API 로드밸런서·CP VM·초기화·마무리 추가) + 'K8s API 연결'을
--   '네임스페이스 및 접근 권한' 단계로 이동 — 이미 적용한 DB 에도 다시 돌리면 이름·순서·소속이 바뀐다(DO UPDATE).
--   진행 중인 회차는 등록해 둔 상세 단계 그대로 진행되고, 새 상세 단계는 다음 회차부터 생긴다.
-- ════════════════════════════════════════════════════════════════
BEGIN;

INSERT INTO cmn_code_group (group_code, name, description) VALUES
    ('TKT_PROVISION_STEP_PAAS',    '클러스터 생성 단계',      'KubernetesProvision 워크플로 진행 단계'),
    ('TKT_PROVISION_SUBSTEP_PAAS', '클러스터 생성 상세 단계', 'KubernetesProvision 워크플로 상세 단계(upper_code = 단계)')
ON CONFLICT (group_code) DO NOTHING;

INSERT INTO cmn_code (group_code, code, name, sort_order, upper_group_code, upper_code) VALUES
    ('TKT_PROVISION_STEP_PAAS',    'check_creatable',      '클러스터 생성 가능 여부 확인중', 10, NULL,           NULL),
    ('TKT_PROVISION_STEP_PAAS',    'create_cluster',       '클러스터 생성',       20, NULL,                      NULL),
    ('TKT_PROVISION_STEP_PAAS',    'setup_cluster',        '네임스페이스 및 접근 권한', 30, NULL,                NULL),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'check_name',           '클러스터 이름',       10, 'TKT_PROVISION_STEP_PAAS', 'check_creatable'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'check_quota',          '자원 쿼터',           20, 'TKT_PROVISION_STEP_PAAS', 'check_creatable'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'check_network',        '네트워크',            30, 'TKT_PROVISION_STEP_PAAS', 'check_creatable'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'send_request',         '생성 요청 전송',      40, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'prepare_api',          'API 로드밸런서·공인 IP', 41, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'create_cp_vm',         'Control Plane VM 생성', 42, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'init_control_plane',   'Control Plane 초기화', 43, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'create_workers',       'Worker VM 생성',      44, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'create_control_plane', '노드 준비',           45, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'finish_cluster',       '클러스터 마무리',     46, 'TKT_PROVISION_STEP_PAAS', 'create_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'check_api',            'K8s API 연결',        70, 'TKT_PROVISION_STEP_PAAS', 'setup_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'create_namespace',     '프로젝트 네임스페이스', 80, 'TKT_PROVISION_STEP_PAAS', 'setup_cluster'),
    ('TKT_PROVISION_SUBSTEP_PAAS', 'grant_access',         '접근 권한 등록',      90, 'TKT_PROVISION_STEP_PAAS', 'setup_cluster')
ON CONFLICT (group_code, code) DO UPDATE
   SET name = EXCLUDED.name, sort_order = EXCLUDED.sort_order,
       upper_group_code = EXCLUDED.upper_group_code, upper_code = EXCLUDED.upper_code;

COMMIT;
