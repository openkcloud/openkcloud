-- v0.6: 자원 신청 제목 — tkt_request.title 별도 컬럼
--
-- 배경: 신청 목록에 '신청 제목'을 표기·입력한다. request_spec(JSON)은 OpenStack 호출용 데이터라
--       거기 섞지 않고 별도 컬럼으로 관리한다. VM·클러스터 신청에만 입력(선택).
-- 적용: psql 수동 실행(자동 마이그레이션 없음). IF NOT EXISTS 라 재실행 안전.
ALTER TABLE tkt_request ADD COLUMN IF NOT EXISTS title VARCHAR(200);
COMMENT ON COLUMN tkt_request.title IS '신청 제목(사용자 입력, VM·클러스터). request_spec 과 분리';
