#!/usr/bin/env bash
# MNT-CORE 자동 시험 — 일회용 PostgreSQL(Docker, localhost:55432/okc_test)에 스키마를 깔고 pytest 를 돌린 뒤 지운다.
# 공용 개발 DB 는 쓰지 않는다. 준비: Docker, .venv 에 pip install -r requirements-dev.txt
set -euo pipefail
cd "$(dirname "$0")/.."

NAME=okc-core-test-pg
docker rm -f "$NAME" >/dev/null 2>&1 || true
trap 'docker rm -f "$NAME" >/dev/null 2>&1 || true' EXIT
docker run -d --name "$NAME" -p 55432:5432 -e POSTGRES_USER=portal_user -e POSTGRES_PASSWORD=test-only \
  -e POSTGRES_DB=okc_test postgres:17 >/dev/null
for _ in $(seq 1 40); do docker exec "$NAME" pg_isready -U portal_user -d okc_test >/dev/null 2>&1 && break; sleep 1; done
sleep 1
for f in db/ddl.sql db/seed.sql db/migration_v0.2_ticket.sql db/migration_v0.3_auth.sql db/migration_v0.4_project_org_nullable.sql db/provision_queue.sql db/paas_cluster_request.sql db/ai_workspace.sql db/migration_v0.5_accel_model_map.sql db/migration_v0.6_tkt_title.sql db/migration_v0.7_tkt_finished.sql; do
  docker exec -i "$NAME" psql -q -U portal_user -d okc_test -v ON_ERROR_STOP=1 < "$f" >/dev/null
done

TEST_DATABASE_URL="postgresql+psycopg://portal_user:test-only@localhost:55432/okc_test" \
  .venv/bin/python -m pytest tests "$@"
