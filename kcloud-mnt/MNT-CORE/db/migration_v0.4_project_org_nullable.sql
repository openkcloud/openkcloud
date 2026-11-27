-- v0.4: 프로젝트의 조직(org) 할당을 선택으로 — iam_project.org_id NULL 허용
--
-- 배경: 프로젝트 생성은 Keystone 에만 하고 포털 DB(iam_project)에는 안 들어가 자원신청 목록에서 누락됨.
--       생성 시 포털에 등록(POST /tickets/projects)하되 조직은 선택이므로 org_id 를 NULL 허용으로 바꾼다.
-- 적용: psql 로 수동 실행(자동 마이그레이션 없음). 이미 nullable 이면 no-op.
ALTER TABLE iam_project ALTER COLUMN org_id DROP NOT NULL;
