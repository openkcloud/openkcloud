-- v0.5: 가속기 모델명 매핑 — Cyborg 가 모델명을 안 주므로 포털에서 product_id → 모델명 관리
--
-- 배경: Cyborg GET /v2/devices 의 model 값이 신형 가속기는 "Device" 로만 와서(pci.ids 미등재)
--       제조사+모델명 표출 불가. ETRI 권고로 포털에서 매핑표를 관리하고 모델명만 표기한다.
-- 키: product_id(std_board_info.product_id, 예 26b9). vendor(PCI id, 예 10de)는 관리/표시용.
-- 적용: psql 수동 실행(자동 마이그레이션 없음). IF NOT EXISTS 라 재실행 안전.
-- 주의: superuser(postgres)로 생성하면 소유자가 postgres 가 되어 앱 유저(portal_user)가 쓰기 불가.
--       아래 OWNER 변경으로 다른 포털 테이블과 동일하게 portal_user 소유로 맞춘다.
CREATE TABLE IF NOT EXISTS accel_model_map (
    product_id VARCHAR(16) PRIMARY KEY,                 -- PCI device id (예: 26b9) — 트레잇/장치 공통 키
    vendor     VARCHAR(16),                             -- PCI vendor id (예: 10de) — 표시·관리용
    model      VARCHAR(64) NOT NULL,                    -- 표시 모델명 (예: L40S)
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE accel_model_map OWNER TO portal_user;
COMMENT ON TABLE accel_model_map IS '가속기 product_id → 모델명 매핑(포털 관리). Cyborg model 미제공 보완';
