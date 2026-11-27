#!/usr/bin/env bash
# 로컬 개발용 Temporal 서버(개발 서버) — 테스트베드의 mnt-workflow 파드 대신 사용
#   준비: brew install temporal
#   gRPC   localhost:7233  ← CORE·IAAS 의 TEMPORAL_API_BASE
#   웹 화면 http://localhost:8233
#   기록은 .temporal/dev.db 에 남아 재시작해도 유지된다(지우면 초기화). 'default' 네임스페이스는 자동 생성.
set -euo pipefail
cd "$(dirname "$0")"

command -v temporal >/dev/null || { echo "temporal CLI 가 없습니다 — brew install temporal" >&2; exit 1; }
mkdir -p .temporal
exec temporal server start-dev --ip 127.0.0.1 --port 7233 --ui-port 8233 --db-filename .temporal/dev.db
