-- ════════════════════════════════════════════════════════════════
-- Temporal 영속 저장소 준비 — openkcloud_portal DB 내 스키마 분리 방식
--   적용: 슈퍼유저로 openkcloud_portal 에 접속해 실행(테스트베드 배포 전 1회)
--         psql -U postgres -d openkcloud_portal -f temporal_schema.sql
--   구조: 계정별 search_path 고정 → Temporal 서버·temporal-sql-tool 이 해당 스키마만 사용
--         default(실행 상태)    = 계정 temporal      → 스키마 temporal
--         visibility(검색/목록) = 계정 temporal_view → 스키마 temporal_visibility
--   테이블 생성은 mnt-workflow 파드의 initContainer(schema)가 수행(setup-schema + update-schema, 멱등).
-- ════════════════════════════════════════════════════════════════

CREATE USER temporal      WITH PASSWORD '<비밀번호1>';
CREATE USER temporal_view WITH PASSWORD '<비밀번호2>';

CREATE SCHEMA temporal            AUTHORIZATION temporal;
CREATE SCHEMA temporal_visibility AUTHORIZATION temporal_view;

ALTER ROLE temporal      SET search_path = temporal;
ALTER ROLE temporal_view SET search_path = temporal_visibility;

GRANT CONNECT ON DATABASE openkcloud_portal TO temporal, temporal_view;

-- visibility 스키마(v1.2~)가 GIN 인덱스에 쓰는 확장 — 일반 계정은 만들 수 없어 슈퍼유저가 미리 생성
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- 배포 후 확인: temporal·temporal_visibility 에 테이블 생성, public 개수 불변
-- SELECT table_schema, count(*) FROM information_schema.tables
-- WHERE table_schema IN ('temporal','temporal_visibility','public') GROUP BY 1;

-- 롤백
-- DROP SCHEMA temporal CASCADE;
-- DROP SCHEMA temporal_visibility CASCADE;
-- DROP USER temporal;
-- DROP USER temporal_view;
-- DROP EXTENSION btree_gin;   -- 다른 곳에서 쓰지 않을 때만
