#!/usr/bin/env bash
# ============================================================
# marker-migrate.sh: 마커 디렉터리 옛 경로 → 새 경로 1회 이관 (세 벤더 설치기 공용)
# 상세: 세 벤더 설치기가 공유하는 이관 스크립트 — 계약은 migrate_marker_dir 주석에 있다.
# 생성일: 2026-09-09 | 수정일: 2026-10-11
# ============================================================

# 개명 전 마커 경로. 시험에서 덮어쓸 수 있게 env 로 연다.
LEGACY_MARKER_DIR="${LEGACY_MARKER_DIR:-/var/lib/npu-operator}"

# migrate_marker_dir <새 마커 경로>
#   개명 전 경로(LEGACY_MARKER_DIR)의 마커를 새 경로로 옮긴다. 멱등이라 매 기동 호출해도 된다.
#   통째 mv 는 안 한다 — kubelet 이 새 hostPath 를 미리 만들어 두어 통째 mv 하면 그 안에
#   npu-operator/ 로 중첩된다. 세 벤더 이미지는 빌드 컨텍스트가 달라 공유 COPY 가 안 돼 각자
#   사본을 두고 동일성은 marker-migrate_test.sh 가 지킨다. 호출부가 ns() 를 미리 정의해야
#   하며, 없으면 경고만 남기고 물러난다(설치는 막지 않는다).
migrate_marker_dir() {
  local new="${1:-}"
  if [ -z "${new}" ]; then
    echo "[WARN] marker-migrate: 새 마커 경로 인자가 없어 이관을 건너뛴다" >&2
    return 0
  fi
  if [ "${LEGACY_MARKER_DIR}" = "${new}" ]; then
    return 0
  fi
  if ! declare -F ns >/dev/null 2>&1; then
    echo "[WARN] marker-migrate: ns 헬퍼가 없어 옛 마커 이관을 건너뛴다" >&2
    return 0
  fi

  local script
  script=$(cat <<EOS
set -u
[ -d '${LEGACY_MARKER_DIR}' ] || exit 0
mkdir -p '${new}' || exit 0
moved=0
for f in '${LEGACY_MARKER_DIR}'/* '${LEGACY_MARKER_DIR}'/.[!.]*; do
  [ -e "\$f" ] || continue
  t='${new}'/\$(basename "\$f")
  # 새 경로에 같은 이름이 이미 있으면 그쪽이 최신이다 — 덮어쓰지 않는다.
  if [ ! -e "\$t" ]; then
    mv "\$f" "\$t" 2>/dev/null && moved=\$((moved+1))
  fi
done
rmdir '${LEGACY_MARKER_DIR}' 2>/dev/null || true
[ "\$moved" -gt 0 ] && echo "[INFO] marker-migrate: '${LEGACY_MARKER_DIR}' → '${new}' 로 \$moved 개 이관"
exit 0
EOS
)
  ns "${script}" || echo "[WARN] marker-migrate: 옛 마커 이관 실패(설치는 계속한다)" >&2
  return 0
}
