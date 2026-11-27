-- ════════════════════════════════════════════════════════════════
-- 키페어 메뉴를 관리자에게도 노출 — 관리자 화면=Nova 키페어, 사용자 화면=포털 DB SSH키.
--   기존 포털 DB 에 1회 적용(멱등). 적용: psql ... -f menu_keypair_admin.sql
-- ════════════════════════════════════════════════════════════════
BEGIN;

INSERT INTO mnu_role_menu (role_id, menu_id, sort_order, label_override)
SELECT r.id, m.id, NULL, NULL
FROM iam_role r JOIN mnu_menu m ON m.code = 'iaas.keypairs'
WHERE r.code = 'admin'
ON CONFLICT (role_id, menu_id) DO NOTHING;

COMMIT;
