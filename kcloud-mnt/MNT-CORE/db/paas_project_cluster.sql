-- ════════════════════════════════════════════════════════════════
-- PaaS 서비스 클러스터 접근 권한 테이블 — 기존 포털 DB 에 1회 적용(멱등)
--   프로젝트 ↔ 클러스터 N:N. 가시성(목록 노출)은 이 매핑으로 필터한다.
--   namespace NULL=클러스터 전체(전용), 값=그 네임스페이스만(공유).
--   적용: psql -U portal_user -d openkcloud_portal -f paas_project_cluster.sql   (여러 번 돌려도 같음)
-- ════════════════════════════════════════════════════════════════
BEGIN;

CREATE TABLE IF NOT EXISTS paas_project_cluster (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id VARCHAR(64) NOT NULL,
    cluster_id VARCHAR(64) NOT NULL,
    namespace  VARCHAR(253),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, cluster_id, namespace)
);
CREATE INDEX IF NOT EXISTS idx_paas_project_cluster_cluster ON paas_project_cluster (cluster_id);
CREATE INDEX IF NOT EXISTS idx_paas_project_cluster_project ON paas_project_cluster (project_id);
COMMENT ON TABLE paas_project_cluster IS 'PaaS 서비스 클러스터 접근 권한(프로젝트↔클러스터 N:N). namespace NULL=전용, 값=공유 네임스페이스';

COMMIT;
