#!/usr/bin/env bash
# ============================================================
# userspace-deps-check.sh: 굽힌 nvidia 사용자 공간 .deb 의 호스트 외부 의존 확인
# 상세: dpkg -i 는 의존을 스스로 풀지 않는다. 이미지에 동봉한 사용자 공간 .deb 여섯 개가
#       요구하는 의존 중 굽힌 nvidia 패키지끼리의 의존을 뺀 나머지가 호스트에 없으면
#       설치가 깨진다. 설치 전에 이를 미리 찾아 fail-closed 로 멈추는 데 쓴다.
# 생성일: 2026-09-26 | 수정일: 2026-09-26
# ============================================================

# userspace_deb_depends <디렉터리>: 그 디렉터리의 .deb 들이 요구하는 의존 중 굽힌 nvidia 패키지
#   끼리의 의존을 뺀 패키지 이름을 줄 단위로 낸다. dpkg -i 는 의존을 스스로 풀지 않으므로 호스트에
#   없는 것이 있으면 설치가 깨진다. 자기 제외 집합(own)은 Package 이름만으론 부족하다 —
#   nvidia-kernel-common-580 은 Provides 로 버전을 붙인 가상 이름(nvidia-kernel-common-580-580.126.09)
#   도 내세우고, 다른 굽힌 패키지는 그 가상 이름을 의존으로 적는다. 그래서 Provides 도 own 에
#   함께 넣어야 굽힌 것끼리의 의존이 온전히 빠진다.
userspace_deb_depends() {
  local dir="$1" own d
  own="$(for d in "${dir}"/*.deb; do
           dpkg-deb -f "$d" Package
           dpkg-deb -f "$d" Provides
         done | tr ',' '\n' | sed 's/^ *//; s/ .*//' | grep -v '^$' | sort -u)"
  for d in "${dir}"/*.deb; do
    dpkg-deb -f "$d" Depends
  done | tr ',' '\n' | sed 's/^ *//; s/ .*//' | grep -v '^$' | sort -u \
    | grep -vxF "${own}"
}

# userspace_missing_host_deps <디렉터리>: 위 목록 중 호스트에 설치돼 있지 않은 것만 낸다.
#   설치 상태는 반드시 호스트에서 봐야 한다 — 이 .deb 들은 host 로 dpkg -i 되기 때문이다.
#   컨테이너의 dpkg 를 조회하면 host 에 없는 의존을 있다고 오판해 그 뒤 dpkg -i 가 깨진다.
#   그래서 상태 조회를 ns(host namespace)를 거쳐 한다. ns 가 없으면(컨테이너 밖 시험) 조용히
#   로컬로 되돌아가지 않고 실패한다 — 시험은 로컬 조회를 원하면 ns 를 직접 정의해 그 뜻을
#   코드로 드러낸다. Status 는 dpkg-query 포맷이라 host bash 까지 리터럴로 가야 해 이스케이프한다.
userspace_missing_host_deps() {
  local dir="$1" p
  userspace_deb_depends "${dir}" | while read -r p; do
    ns "dpkg-query -W -f='\${Status}' $p 2>/dev/null" | grep -q 'install ok installed' || echo "$p"
  done
}
