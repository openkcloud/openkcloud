-- v0.7: 자원 신청 마감(완료·실패) 기록 — tkt_request.finished_by · finished_at
--
-- 배경: 신청 상세 '진행 결과'에 승인 · 프로비저닝 · 반려에 더해 완료 · 실패 줄을 보인다(처리자 + 시각).
--       처리자 = 그 회차 프로비저닝(재시도)을 시작한 관리자(provisioning_by), 시각 = 워크플로가 마감한 때(core.finish).
--       프로비저닝·재시도(새 회차)를 시작하면 비운다.
-- 적용: psql 수동 실행(자동 마이그레이션 없음). IF NOT EXISTS 라 재실행 안전.
ALTER TABLE tkt_request ADD COLUMN IF NOT EXISTS finished_by BIGINT REFERENCES iam_user(id);
ALTER TABLE tkt_request ADD COLUMN IF NOT EXISTS finished_at TIMESTAMPTZ;
COMMENT ON COLUMN tkt_request.finished_by IS '마감(완료·실패) 처리자 — 그 회차 프로비저닝을 시작한 관리자';
COMMENT ON COLUMN tkt_request.finished_at IS '마감(완료·실패) 시각 — 워크플로가 끝낸 때, 프로비저닝·재시도 시작 때 비움';

-- 이미 끝난 신청 채우기 — 마지막 회차 단계의 가장 늦은 종료 시각(비어 있는 것만, 재실행 안전)
UPDATE tkt_request r
   SET finished_by = r.provisioning_by,
       finished_at = (SELECT max(s.ended_at) FROM tkt_provision_step s
                       WHERE s.request_id = r.id
                         AND s.attempt = (SELECT max(attempt) FROM tkt_provision_step WHERE request_id = r.id))
 WHERE r.status IN ('completed', 'failed') AND r.finished_at IS NULL;
