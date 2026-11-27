#!/usr/bin/env bash
# ============================================================
# marker-migrate_test.sh: 마커 디렉터리 이관 함수 시험 (세 벤더 설치기 공용)
# 상세: migrate_marker_dir 가 옛 경로(/var/lib/npu-operator) 마커를 잃지 않고 새 경로로
#       멱등하게 옮기는지, 세 벤더 사본이 동일한지를 고정한다.
# 생성일: 2026-09-09
# ============================================================
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/.." && pwd)"
FAILED=0

CANON="${HERE}/furiosa/marker-migrate.sh"
COPIES=(
  "${HERE}/nvidia/marker-migrate.sh"
  "${HERE}/tenstorrent/marker-migrate.sh"
)

check() {
  local label="$1" want="$2" got="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "  [FAIL] ${label}: got '${got}', want '${want}'"
    FAILED=1
  else
    echo "  [ok]   ${label}"
  fi
}

echo "== 사본 동일성 =="
for c in "${COPIES[@]}"; do
  if [[ ! -f "${c}" ]]; then
    echo "  [FAIL] ${c} 이 없다"
    FAILED=1
  elif ! cmp -s "${CANON}" "${c}"; then
    echo "  [FAIL] ${c} 가 ${CANON} 와 다르다(사본 갈라짐)"
    FAILED=1
  else
    echo "  [ok]   ${c##*/} @ $(dirname "${c}" | xargs basename)"
  fi
done

# shellcheck source=/dev/null
source "${CANON}"

# ns 는 원래 nsenter 로 호스트에서 도는 래퍼다. 시험에서는 로컬에서 실행한다 —
# 이관이 쓰는 것은 mkdir/mv/rmdir 뿐이라 호스트 네임스페이스가 필요 없다.
ns() { bash -c "$1"; }

echo "== 이관 =="
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
LEGACY_MARKER_DIR="${tmp}/old"
NEW="${tmp}/new"

mkdir -p "${LEGACY_MARKER_DIR}"
echo 580.65.06 > "${LEGACY_MARKER_DIR}/nvidia.ok"
touch "${LEGACY_MARKER_DIR}/driver.ready" "${LEGACY_MARKER_DIR}/needs-reboot"

migrate_marker_dir "${NEW}" >/dev/null
check "마커가 새 경로로 옮겨진다"  "580.65.06" "$(cat "${NEW}/nvidia.ok" 2>/dev/null)"
check "driver.ready 도 옮겨진다"    "yes"       "$([ -f "${NEW}/driver.ready" ] && echo yes || echo no)"
check "빈 옛 디렉터리는 지워진다"    "no"        "$([ -d "${LEGACY_MARKER_DIR}" ] && echo yes || echo no)"

echo "== 멱등 =="
migrate_marker_dir "${NEW}" >/dev/null
check "다시 불러도 실패하지 않는다"  "0"         "$?"
check "옮긴 내용이 유지된다"        "580.65.06" "$(cat "${NEW}/nvidia.ok" 2>/dev/null)"

echo "== 새 경로 우선 =="
# kubelet 이 새 hostPath 를 먼저 만들고 설치기가 이미 새 마커를 쓴 뒤라면,
# 옛 경로에 남은 낡은 사본이 그것을 덮어써서는 안 된다.
tmp2="$(mktemp -d)"
LEGACY_MARKER_DIR="${tmp2}/old"
NEW2="${tmp2}/new"
mkdir -p "${LEGACY_MARKER_DIR}" "${NEW2}"
echo stale > "${LEGACY_MARKER_DIR}/nvidia.ok"
echo fresh > "${NEW2}/nvidia.ok"
echo x     > "${LEGACY_MARKER_DIR}/furiosa.dpkg"

migrate_marker_dir "${NEW2}" >/dev/null
check "새 경로 값이 낡은 값에 덮이지 않는다" "fresh" "$(cat "${NEW2}/nvidia.ok")"
check "겹치지 않는 마커는 옮겨진다"          "x"     "$(cat "${NEW2}/furiosa.dpkg" 2>/dev/null)"
check "충돌 파일이 남아 옛 경로는 유지된다"  "yes"   "$([ -d "${LEGACY_MARKER_DIR}" ] && echo yes || echo no)"
rm -rf "${tmp2}"

echo "== ns 부재 =="
# nsenter 를 못 쓰는 설치기(entrypoint-v17 계열 pod 가 hostPID 없이 도는 경우)에서
# 이관은 건너뛰되 설치 자체를 죽이지 않아야 한다.
unset -f ns
migrate_marker_dir "/tmp/does-not-matter" 2>/dev/null
check "ns 가 없어도 0 으로 돌아온다" "0" "$?"

echo
if [[ ${FAILED} -eq 0 ]]; then
  echo "PASS"
else
  echo "FAIL"
fi
exit ${FAILED}
