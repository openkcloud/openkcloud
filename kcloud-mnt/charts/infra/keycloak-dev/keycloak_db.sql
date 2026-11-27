-- ════════════════════════════════════════════════════════════════
-- 개발 서버(Mac Mini) Keycloak DB 준비 — 테스트베드(openkcloud-db)와 같은 구조
--   전용 DB keycloak(소유자 keycloak, UTF8, 정렬 C, PUBLIC 접속 권한 제거) + 전용 스키마 keycloak(KC_DB_SCHEMA)
--   테이블(약 100개)은 Keycloak 이 처음 기동할 때 스키마 keycloak 에 만든다.
--   적용: Mac Mini 에서 슈퍼유저로 postgres DB 에 접속해 실행(1회)
--         psql -U okestro -d postgres -v ON_ERROR_STOP=1 -f keycloak_db.sql
-- ════════════════════════════════════════════════════════════════

CREATE USER keycloak WITH PASSWORD '<비밀번호>';

-- 개발 서버 기본 정렬(en_US.UTF-8)과 달리 테스트베드와 같은 C 로 만든다(template0 필수)
CREATE DATABASE keycloak OWNER keycloak ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C' TEMPLATE template0;
REVOKE ALL ON DATABASE keycloak FROM PUBLIC;   -- 테스트베드 datacl {keycloak=CTc/keycloak} 과 같게

\connect keycloak
CREATE SCHEMA keycloak AUTHORIZATION keycloak;

-- 확인(테스트베드와 같아야 함)
--   psql -U okestro -d postgres -c '\du keycloak' \
--     -c "SELECT datname, pg_get_userbyid(datdba) AS owner, pg_encoding_to_char(encoding) AS enc, datcollate, datacl
--           FROM pg_database WHERE datname = 'keycloak';"
--     → keycloak | keycloak | UTF8 | C | {keycloak=CTc/keycloak}
--   psql -U okestro -d keycloak -c '\dn'   → keycloak(keycloak), public(pg_database_owner)

-- 롤백
-- DROP DATABASE keycloak;
-- DROP USER keycloak;
