-- ════════════════════════════════════════════════════════════════
-- AI 반도체(Cyborg) 신규 IaaS 메뉴 2개 — 기존 포털 DB 에 1회 적용(멱등)
--   · 디바이스 프로파일 (/iaas/device-profiles)   — 관리자 전용
--   · 가속기 장치 현황  (/iaas/accelerator-devices) — 관리자 전용(연결 VM 에 모든 프로젝트 VM 이 보임)
--   적용: psql -U portal_user -d openkcloud_portal -f menu_accelerator.sql   (여러 번 돌려도 같음)
-- ════════════════════════════════════════════════════════════════
BEGIN;

-- 서브메뉴 추가 (parent = cloud.iaas)
INSERT INTO mnu_menu (parent_id, menu_type, code, label, path, icon, sort_order)
SELECT p.id, 'submenu', v.code, v.label, v.path, v.icon, v.ord
FROM (VALUES
    ('iaas.deviceprofiles', '디바이스 프로파일', '/iaas/device-profiles',     'Cpu',  95),
    ('iaas.acceldevices',   '가속기 장치 현황',  '/iaas/accelerator-devices', 'Cpu', 100)
) AS v(code, label, path, icon, ord)
JOIN mnu_menu p ON p.code = 'cloud.iaas'
ON CONFLICT (code) DO NOTHING;

-- 역할-메뉴 매핑: 관리자만
INSERT INTO mnu_role_menu (role_id, menu_id, sort_order, label_override)
SELECT r.id, m.id, NULL, NULL
FROM (VALUES
    ('admin', 'iaas.deviceprofiles'),
    ('admin', 'iaas.acceldevices')
) AS v(role, code)
JOIN iam_role r ON r.code = v.role
JOIN mnu_menu m ON m.code = v.code
ON CONFLICT (role_id, menu_id) DO NOTHING;

COMMIT;
