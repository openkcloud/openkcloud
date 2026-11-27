-- ════════════════════════════════════════════════════════════════
-- MNT-CORE 시드 v0.1 — 목업(nav.ts v2.1 정렬본) 기준
--   계정 2개(admin/user) · 역할 2종 · LNB 메뉴 전체 · 역할별 매핑
--   적용: psql -U portal_user -d openkcloud_portal -f seed.sql
--   ※ 비밀번호는 목업과 동일(admin/admin, user/user) — 운영 전 반드시 교체
-- ════════════════════════════════════════════════════════════════

BEGIN;

-- ── 0) 공통코드 ──────────────────────────────────────────────────
INSERT INTO cmn_code_group (group_code, name, description) VALUES
    ('TKT_STATUS',      '자원신청 상태', '자원 요청 워크플로우 상태 (목업 RequestStatus)'),
    ('TKT_TYPE',        '자원신청 유형', '신청 대상 자원 유형'),
    ('IAM_USER_STATUS', '계정 상태',     '포털 계정 상태');

-- attr1 = 화면 배지 톤 (프론트 Badge variant 매핑용)
INSERT INTO cmn_code (group_code, code, name, sort_order, attr1) VALUES
    -- 자원신청 상태 (목업: pending | provisioning | completed | rejected)
    ('TKT_STATUS',      'pending',      '승인 대기',      10, 'warning'),
    ('TKT_STATUS',      'provisioning', '프로비저닝 중',  20, 'info'),
    ('TKT_STATUS',      'completed',    '완료',           30, 'success'),
    ('TKT_STATUS',      'rejected',     '반려',           40, 'destructive'),
    -- 자원신청 유형 (목업: vm | cluster)
    ('TKT_TYPE',        'vm',           '가상서버',       10, NULL),
    ('TKT_TYPE',        'cluster',      '클러스터',       20, NULL),
    -- 계정 상태
    ('IAM_USER_STATUS', 'active',       '활성',           10, 'success'),
    ('IAM_USER_STATUS', 'locked',       '잠금',           20, 'warning'),
    ('IAM_USER_STATUS', 'disabled',     '비활성',         30, 'secondary');

-- ── 1) 역할 ──────────────────────────────────────────────────────
INSERT INTO iam_role (code, name, description) VALUES
    ('admin', '관리자', '플랫폼 운영자 — 인프라 현황·서비스 구성·IAM'),
    ('user',  '사용자', 'AI 서비스 개발자 — 자원 신청·활용');

-- ── 2) 사용자 (목업 계정) ────────────────────────────────────────
INSERT INTO iam_user (username, password_hash, name, org) VALUES
    ('admin',   crypt('admin',   gen_salt('bf')), '최현화', 'ETRI'),
    ('user',    crypt('user',    gen_salt('bf')), '윤호영', '오케스트로'),
    ('ek.lee',  crypt('ek.lee',  gen_salt('bf')), '이은경', NULL),
    ('ky.yang', crypt('ky.yang', gen_salt('bf')), '양기영', NULL),
    ('jw.lee',  crypt('jw.lee',  gen_salt('bf')), '이진우', NULL);

-- admin/user 는 username==role code 로 자동 매핑
INSERT INTO iam_user_role (user_id, role_id)
SELECT u.id, r.id FROM iam_user u JOIN iam_role r ON r.code = u.username;
-- 추가 사용자(이은경·양기영·이진우)는 사용자(user) 권한
INSERT INTO iam_user_role (user_id, role_id)
SELECT u.id, (SELECT id FROM iam_role WHERE code = 'user')
  FROM iam_user u WHERE u.username IN ('ek.lee', 'ky.yang', 'jw.lee');

-- ── 3) 메뉴: 섹션 ────────────────────────────────────────────────
INSERT INTO mnu_menu (menu_type, code, label, sort_order) VALUES
    ('section', 'sec.overview',      '개요',                       10),
    ('section', 'sec.observability', '옵저버빌리티',               20),
    ('section', 'sec.cloud',         '클라우드 자원',              30),
    ('section', 'sec.marketplace',   '서비스 마켓플레이스',        40),
    ('section', 'sec.aidev',         'AI 개발환경',                60),
    ('section', 'sec.aiinfra',       'AI 인프라',                  70),
    ('section', 'sec.ops',           '운영',                       80);

-- ── 4) 메뉴: 섹션 하위 ───────────────────────────────────────────
INSERT INTO mnu_menu (parent_id, menu_type, code, label, path, icon, sort_order)
SELECT p.id, 'menu', v.code, v.label, v.path, v.icon, v.ord
FROM (VALUES
    -- 개요
    ('sec.overview',      'dashboard',          '통합 대시보드',        '/dashboard',              'LayoutDashboard', 10),
    -- 옵저버빌리티
    ('sec.observability', 'obs.monitoring',     '인프라 모니터링',      '/monitoring',             'Activity',        10),
    ('sec.observability', 'obs.insights',       'AI 모델 분석',         '/monitoring/insights',    'Sparkles',        20),
    ('sec.observability', 'obs.traces',         '분산 트레이스',        '/monitoring/traces',      'GitBranch',       30),
    ('sec.observability', 'obs.logs',           '로그·이벤트',          '/monitoring/logs',        'ScrollText',      40),
    ('sec.observability', 'obs.alerts',         '알람',                 '/monitoring/alerts',      'Bell',            50),
    -- 클라우드 자원 (ticket 라벨은 역할별 오버라이드: 사용자=자원 요청 관리 / 관리자=자원 신청 관리)
    ('sec.cloud',         'cloud.ticket',       '자원 요청 관리',       '/ticket/requests',        'ClipboardList',   10),
    ('sec.cloud',         'cloud.iaas',         'IaaS (OpenStack)',     '/iaas',                   'Server',          20),
    ('sec.cloud',         'cloud.paas',         'PaaS (Kubernetes)',    '/paas',                   'Boxes',           30),
    -- 서비스 마켓플레이스
    ('sec.marketplace',   'market.catalog',     '서비스 마켓플레이스',  '/catalog',                'ShoppingBag',     10),
    -- AI 개발환경
    ('sec.aidev',         'aidev.notebook',     'AI 노트북',            '/ai-development',         'BrainCircuit',    10),
    -- AI 인프라
    ('sec.aiinfra',       'aiinfra.resourcemap','리소스 현황',          '/resource-map',           'Database',        10),
    ('sec.aiinfra',       'aiinfra.infra',      '인프라 관리',          '/infrastructure/servers', 'Server',          20),
    ('sec.aiinfra',       'aiinfra.topology',   '기술인벤토리 관리',    '/topology',               'Layers',          30),
    -- 운영
    ('sec.ops',           'ops.iam',            'IAM·접근제어',         '/iam',                    'ShieldCheck',     10),
    ('sec.ops',           'ops.settings',       '시스템 설정',          '/settings',               'Settings',        20)
) AS v(parent, code, label, path, icon, ord)
JOIN mnu_menu p ON p.code = v.parent;

-- ── 5) 메뉴: 서브메뉴 ────────────────────────────────────────────
INSERT INTO mnu_menu (parent_id, menu_type, code, label, path, icon, sort_order)
SELECT p.id, 'submenu', v.code, v.label, v.path, v.icon, v.ord
FROM (VALUES
    -- IaaS 하위 (관리자=전체, 사용자=부분집합 — 노출은 mnu_role_menu 가 결정)
    ('cloud.iaas',     'iaas.projects',   '프로젝트',         '/iaas/projects',          'Server',       10),
    ('cloud.iaas',     'iaas.instances',  '가상서버',         '/iaas/instances',         'Server',       20),
    ('cloud.iaas',     'iaas.networks',   '네트워크',         '/iaas/networks',          'Network',      30),
    ('cloud.iaas',     'iaas.routers',    '라우터',           '/iaas/routers',           'Route',        40),
    ('cloud.iaas',     'iaas.secgroups',  '보안 그룹',        '/iaas/security-groups',   'ShieldCheck',  50),
    ('cloud.iaas',     'iaas.keypairs',   '키페어',           '/iaas/keypairs',          'KeyRound',     60),
    ('cloud.iaas',     'iaas.volumes',    '볼륨·스토리지',    '/iaas/volumes',           'HardDrive',    70),
    ('cloud.iaas',     'iaas.images',     '이미지',           '/iaas/images',            'Disc',         80),
    ('cloud.iaas',     'iaas.flavors',    '플레이버',         '/iaas/flavors',           'Cpu',          90),
    ('cloud.iaas',     'iaas.deviceprofiles', '디바이스 프로파일', '/iaas/device-profiles',   'Cpu',      95),
    ('cloud.iaas',     'iaas.acceldevices',   '가속기 장치 현황',  '/iaas/accelerator-devices', 'Cpu',   100),
    -- PaaS 하위
    ('cloud.paas',     'paas.clusters',   '클러스터',         '/paas/clusters',          'Boxes',        10),
    ('cloud.paas',     'paas.workloads',  '워크로드',         '/paas/workloads',         'Boxes',        20),
    ('cloud.paas',     'paas.services',   '서비스·인그레스',  '/paas/services',          'Globe',        30),
    ('cloud.paas',     'paas.storage',    '스토리지',         '/paas/storage',           'Database',     40),
    ('cloud.paas',     'paas.config',     '설정 관리',        '/paas/config',            'Boxes',        50),
    ('cloud.paas',     'paas.templates',  '클러스터 템플릿',  '/paas/templates',         'Boxes',        60),
    -- AI 노트북 하위 (v2.1 C안: 업무 중심 한글 용어)
    ('aidev.notebook', 'aidev.workspace', '워크스페이스',     '/ai-development/profiles',       'BrainCircuit', 10),
    ('aidev.notebook', 'aidev.env',       '개발환경',         '/ai-development/notebooks',      'BrainCircuit', 20),
    ('aidev.notebook', 'aidev.training',  '학습',             '/ai-development/pipelines',      'BrainCircuit', 30),
    ('aidev.notebook', 'aidev.serving',   '모델 배포',        '/ai-development/serving',        'BrainCircuit', 40),
    ('aidev.notebook', 'aidev.perf',      '성능 분석',        '/ai-development/model-insights', 'BrainCircuit', 50),
    -- IAM 하위
    ('ops.iam',        'iam.users',       '사용자',           '/iam/users',              'ShieldCheck',  10),
    ('ops.iam',        'iam.roles',       '역할·권한',        '/iam/roles',              'ShieldCheck',  20),
    ('ops.iam',        'iam.apikeys',     'API Key',          '/iam/api-keys',           'ShieldCheck',  30),
    ('ops.iam',        'iam.audit',       '감사 로그',        '/iam/audit',              'ShieldCheck',  40),
    -- 시스템 설정 하위
    ('ops.settings',   'settings.menus',  '메뉴 관리',        '/settings/menus',         'Settings',     10)
) AS v(parent, code, label, path, icon, ord)
JOIN mnu_menu p ON p.code = v.parent;

-- ── 6) 역할-메뉴 매핑: 사용자 ────────────────────────────────────
-- 섹션 순서: 개요 → 클라우드 자원 → 마켓 → AI 개발환경 → 옵저버빌리티 → AI 인프라
INSERT INTO mnu_role_menu (role_id, menu_id, sort_order, label_override)
SELECT r.id, m.id, v.ord, v.label_ov
FROM (VALUES
    ('user', 'sec.overview',      10,   NULL::varchar),
    ('user', 'sec.cloud',         20,   NULL),
    ('user', 'sec.marketplace',   30,   NULL),
    ('user', 'sec.aidev',         40,   NULL),
    ('user', 'sec.observability', 50,   NULL),
    ('user', 'sec.aiinfra',       60,   NULL),
    ('user', 'dashboard',         NULL, NULL),
    ('user', 'obs.monitoring',    NULL, NULL),
    ('user', 'obs.insights',      NULL, NULL),
    ('user', 'obs.traces',        NULL, NULL),
    ('user', 'obs.logs',          NULL, NULL),
    ('user', 'obs.alerts',        NULL, NULL),
    ('user', 'cloud.ticket',      NULL, NULL),          -- 기본 라벨 '자원 요청 관리'
    ('user', 'cloud.iaas',        NULL, NULL),
    ('user', 'iaas.projects',     NULL, NULL),
    ('user', 'iaas.instances',    NULL, NULL),
    ('user', 'iaas.networks',     NULL, NULL),
    ('user', 'iaas.secgroups',    NULL, NULL),
    ('user', 'iaas.keypairs',     NULL, NULL),
    ('user', 'iaas.volumes',      NULL, NULL),
    ('user', 'cloud.paas',        NULL, NULL),
    ('user', 'paas.clusters',     NULL, NULL),
    ('user', 'paas.workloads',    NULL, NULL),
    ('user', 'paas.services',     NULL, NULL),
    ('user', 'paas.storage',      NULL, NULL),
    ('user', 'paas.config',       NULL, NULL),
    ('user', 'market.catalog',    NULL, NULL),
    ('user', 'aidev.notebook',    NULL, NULL),
    ('user', 'aidev.workspace',   NULL, NULL),
    ('user', 'aidev.env',         NULL, NULL),
    ('user', 'aidev.training',    NULL, NULL),
    ('user', 'aidev.serving',     NULL, NULL),
    ('user', 'aidev.perf',        NULL, NULL),
    ('user', 'aiinfra.resourcemap', NULL, NULL),
    ('user', 'aiinfra.infra',     NULL, NULL),
    ('user', 'aiinfra.topology',  NULL, NULL)
) AS v(role, code, ord, label_ov)
JOIN iam_role r ON r.code = v.role
JOIN mnu_menu m ON m.code = v.code;

-- ── 7) 역할-메뉴 매핑: 관리자 ────────────────────────────────────
-- 섹션 순서: 개요 → 옵저버빌리티 → 클라우드 자원(전체) → 마켓 → AI 개발환경 → AI 인프라 → 운영
-- 관리자 = IaaS/PaaS 전체 자원. 사용자는 부분집합(라우터·이미지 제외). ticket 라벨 '자원 신청 관리'.
INSERT INTO mnu_role_menu (role_id, menu_id, sort_order, label_override)
SELECT r.id, m.id, v.ord, v.label_ov
FROM (VALUES
    ('admin', 'sec.overview',      10,   NULL::varchar),
    ('admin', 'sec.observability', 20,   NULL),
    ('admin', 'sec.cloud',         30,   NULL),
    ('admin', 'sec.marketplace',   40,   NULL),
    ('admin', 'sec.aidev',         60,   NULL),
    ('admin', 'sec.aiinfra',       70,   NULL),
    ('admin', 'sec.ops',           80,   NULL),
    ('admin', 'dashboard',         NULL, NULL),
    ('admin', 'obs.monitoring',    NULL, NULL),
    ('admin', 'obs.insights',      NULL, NULL),
    ('admin', 'obs.traces',        NULL, NULL),
    ('admin', 'obs.logs',          NULL, NULL),
    ('admin', 'obs.alerts',        NULL, NULL),
    ('admin', 'cloud.ticket',      NULL, '자원 신청 관리'),
    ('admin', 'cloud.iaas',        NULL, NULL),
    ('admin', 'iaas.projects',     NULL, NULL),
    ('admin', 'iaas.instances',    NULL, NULL),
    ('admin', 'iaas.networks',     NULL, NULL),
    ('admin', 'iaas.routers',      NULL, NULL),
    ('admin', 'iaas.secgroups',    NULL, NULL),
    -- 키페어: user=포털 DB SSH키, admin=Nova 키페어(같은 메뉴, 화면에서 역할 분기)
    ('admin', 'iaas.keypairs',     NULL, NULL),
    ('admin', 'iaas.volumes',      NULL, NULL),
    ('admin', 'iaas.images',       NULL, NULL),
    ('admin', 'iaas.flavors',      NULL, NULL),
    ('admin', 'iaas.deviceprofiles', NULL, NULL),
    ('admin', 'iaas.acceldevices',   NULL, NULL),
    ('admin', 'cloud.paas',        NULL, NULL),
    ('admin', 'paas.clusters',     NULL, NULL),
    ('admin', 'paas.workloads',    NULL, NULL),
    ('admin', 'paas.services',     NULL, NULL),
    ('admin', 'paas.storage',      NULL, NULL),
    ('admin', 'paas.config',       NULL, NULL),
    ('admin', 'paas.templates',    NULL, NULL),
    ('admin', 'market.catalog',    NULL, NULL),
    ('admin', 'aidev.notebook',    NULL, NULL),
    ('admin', 'aidev.workspace',   NULL, NULL),
    ('admin', 'aidev.env',         NULL, NULL),
    ('admin', 'aidev.training',    NULL, NULL),
    ('admin', 'aidev.serving',     NULL, NULL),
    ('admin', 'aidev.perf',        NULL, NULL),
    ('admin', 'aiinfra.resourcemap', NULL, NULL),
    ('admin', 'aiinfra.infra',     NULL, NULL),
    ('admin', 'aiinfra.topology',  NULL, NULL),
    ('admin', 'ops.iam',           NULL, NULL),
    ('admin', 'iam.users',         NULL, NULL),
    ('admin', 'iam.roles',         NULL, NULL),
    ('admin', 'iam.apikeys',       NULL, NULL),
    ('admin', 'iam.audit',         NULL, NULL),
    ('admin', 'ops.settings',      NULL, NULL),
    ('admin', 'settings.menus',    NULL, NULL)
) AS v(role, code, ord, label_ov)
JOIN iam_role r ON r.code = v.role
JOIN mnu_menu m ON m.code = v.code;

COMMIT;
