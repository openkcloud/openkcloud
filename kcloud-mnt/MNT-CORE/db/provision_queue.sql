-- ════════════════════════════════════════════════════════════════
-- VM 프로비저닝 시작 대기(예약) — 프로비저닝을 누른 시점의 예상 대기 시간을 신청에 남긴다(예약 모달에 고정 표시).
--   기존 포털 DB 에 1회 적용(멱등). 적용: psql -U portal_user -d openkcloud_portal -f provision_queue.sql   (여러 번 돌려도 같음)
-- ════════════════════════════════════════════════════════════════
ALTER TABLE tkt_request ADD COLUMN IF NOT EXISTS queue_expected_sec INTEGER;
COMMENT ON COLUMN tkt_request.queue_expected_sec IS
    'VM 프로비저닝을 누른 시점의 시작 예상 대기(초) — 0 이면 바로 시작. 예약 모달에 고정 표시(현재 대기가 넘어가도 그대로)';
