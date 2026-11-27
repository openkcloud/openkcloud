#!/usr/bin/env bash
# ============================================================
# chart-default-installable.sh: 차트 기본값만으로 드라이버 설치가 가능한 조합인지 검사
# 상세: 차트 기본값만으로 드라이버 설치가 가능한지 세 가지를 검사한다 — 기본 렌더에
#       사이트별 kernelImages 가 없는지, 벤더별 driver.version 이 verifiedVersions 에
#       있는지, (CHECK_ARCHIVE=1 일 때만) nvidia 버전이 우분투 아카이브에 실재하는지.
#       각 검사의 근거는 아래 본문 주석에 있다.
# 생성일: 2026-09-26 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
cd "$(dirname "$0")/.."
VALUES="${VALUES:-deploy/helm/values.yaml}"
rendered="$(helm template t deploy/helm -f "${VALUES}")"

fail=0
note() { printf '%s: %s\n' "$1" "$2"; }

# 1) 기본 렌더에 kernelImages 가 없어야 한다 — 사설 레지스트리 주소와 커널 버전이 박힌
#    사이트별 값이 배포 차트 기본값에 섞여 들어가는 실수를 잡는다. "생성 명령 | grep -q
#    패턴" 대신 [[ == * ]] 로 보는 이유는 set -o pipefail 아래서 grep 이 먼저 끝나면
#    SIGPIPE 로 생성 명령이 죽어 판정이 뒤집힐 수 있어서다(검사 3 의 madison 비교도 같다).
if [[ "${rendered}" == *"kernelImages:"* ]]; then
  note "기본 렌더 커널 이미지" "FAIL(기본값에 사이트 값이 있다)"; fail=1
else
  note "기본 렌더 커널 이미지" "OK(없음)"
fi

# 2) 벤더별 driver.version 이 그 벤더 verifiedVersions 에 있어야 한다 — 기본 버전만 바꾸고
#    화이트리스트 갱신을 빠뜨리면 그 벤더 설치가 UnverifiedVersion 으로 거부되는 실수를 잡는다.
python3 - "${VALUES}" <<'PY' || fail=1
import sys, yaml
v = yaml.safe_load(open(sys.argv[1]))
bad = []
for name, p in (v.get('driverInstallPolicies') or {}).items():
    if not isinstance(p, dict) or not p.get('enabled'):
        continue
    ver = ((p.get('driver') or {}).get('version') or '')
    verified = p.get('verifiedVersions') or []
    if ver and verified and ver not in verified:
        bad.append('%s: version %s 가 verifiedVersions 에 없다' % (name, ver))
for b in bad:
    print('  ' + b)
print('%s: %s' % ('벤더별 버전 화이트리스트', 'FAIL' if bad else 'OK'))
sys.exit(1 if bad else 0)
PY

# 3) 선택(CHECK_ARCHIVE=1): PPA 에만 있고 아카이브에는 없는 nvidia 버전을 기본값에 두면
#    이 차트를 그대로 쓰는 다른 클러스터가 설치에 실패한다. apt-cache madison 은 네트워크를
#    타지 않고 사전 apt-get update 로 받아 둔 로컬 목록만 읽어, air-gap·목록이 오래된
#    호스트에서는 실재해도 FAIL 로 나올 수 있어 기본으로 강제하지 않는다.
if [ "${CHECK_ARCHIVE:-0}" = "1" ]; then
  nv="$(python3 - "${VALUES}" <<'PY'
import sys, yaml
v = yaml.safe_load(open(sys.argv[1]))
p = (v.get('driverInstallPolicies') or {}).get('nvidia') or {}
print(((p.get('driver') or {}).get('version') or ''))
PY
)"
  madison_out="$(apt-cache madison "nvidia-kernel-common-${nv%%.*}" 2>/dev/null || true)"
  if [[ "${madison_out}" == *"${nv}"* ]]; then
    note "아카이브 실재(${nv})" "OK"
  else
    note "아카이브 실재(${nv})" "FAIL(아카이브에 없다)"; fail=1
  fi
else
  note "아카이브 실재" "SKIP(CHECK_ARCHIVE=1 로 켠다 — 미실행이며 통과 아님)"
fi

[ "${fail}" -eq 0 ] && { echo "chart-default-installable: OK"; exit 0; }
echo "chart-default-installable: FAIL"; exit 1
