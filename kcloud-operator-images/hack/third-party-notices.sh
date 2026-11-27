#!/usr/bin/env bash
# ============================================================
# third-party-notices.sh: Go 바이너리에 들어가는 서드파티 모듈의 라이선스 고지문을 만든다
# 상세: 링크되는 패키지 디렉터리에서 모듈 루트까지 올라가며 LICENSE·COPYING·NOTICE 를 모두 싣고
#       Go 툴체인 LICENSE 도 싣는다. Apache-2.0 본문은 같은 글이면 한 번만 싣는다.
#       네트워크 없이 모듈 캐시를 읽는다. Go 이외 구성 요소는 이미지 안 고지 위치만 안내한다.
# 생성일: 2026-10-13 | 수정일: 2026-10-14
# ============================================================
set -euo pipefail
# 정렬·ls 순서가 로케일에 따라 달라지면 CI 와 로컬의 결과가 갈린다. GOPROXY 는 호출자가 정하지 않았을 때만 끈다.
export GOFLAGS=-mod=readonly GOPROXY="${GOPROXY:-off}" GOOS=linux LC_ALL=C

classify() {  # 라이선스 본문 첫 4KB 의 열쇠말로 SPDX 를 고른다
  local f="$1" h
  h="$(head -c 4096 "$f")"
  case "$h" in
    *"Apache License"*"Version 2.0"*) echo Apache-2.0 ;;
    *"Permission is hereby granted, free of charge"*) echo MIT ;;
    *"Redistribution and use in source and binary forms"*"Neither the name"*) echo BSD-3-Clause ;;
    *"Redistribution and use in source and binary forms"*) echo BSD-2-Clause ;;
    *"Permission to use, copy, modify, and/or distribute"*) echo ISC ;;
    *"Mozilla Public License"*) echo MPL-2.0 ;;
    *) echo UNKNOWN ;;
  esac
}

# 빈 줄을 뺀 글이 같으면 같은 본문으로 본다. 앞뒤에 덧붙은 저작권 줄·다른 라이선스는 글이 달라지므로 같지 않다.
norm() { grep -av '^[[:space:]]*$' "$1" || true; }
same() { diff -q <(norm "$1") <(norm "$2") >/dev/null; }

# 링크되는 패키지마다 "경로 버전 모듈디렉터리 디렉터리" 를 내고, 패키지 디렉터리에서 모듈 루트까지의
# 상위 디렉터리를 모두 포함한다(하위 디렉터리에 따로 붙은 LICENSE 를 놓치지 않으려는 것).
modules() {  # <디렉터리>:<패턴>[:<빌드 태그>] 마다
  local spec dir pat tags
  for spec in "$@"; do
    IFS=: read -r dir pat tags <<<"$spec"
    (cd "$dir" && go list ${tags:+-tags="$tags"} -deps -f '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}} {{.Dir}}{{end}}{{end}}' "$pat")
  done | awk 'NF==4 { d=$4; while (length(d) >= length($3)) { print $1, $2, $3, d; sub(/\/[^\/]*$/, "", d) } }' | sort -u
}

# 디렉터리 안의 LICENSE·LICENCE·COPYING·NOTICE 류 파일 이름(license.go 같은 소스 파일은 제외)
licfiles() { ls "$1" | grep -iE '^(LICENSE|LICENCE|COPYING|NOTICE)([-_.].*)?$' | grep -v '\.go$' || true; }

render() {  # 표준 입력: modules 출력. Apache-2.0 본문은 목록 안 첫 Apache 파일을 한 번 싣는다.
  local path ver dir sub rel f name spdx cur="" hdr=0 s dup apache_file="" unknown=0 seen=()
  echo "이 소프트웨어에 포함된 서드파티 구성 요소와 그 라이선스 고지다. 자동 생성 파일이며 이 저장소의 고지문 재생성 명령(operator: make notices, images: hack/third-party-notices-images.sh)으로 다시 만든다."
  echo
  # Go 이외 구성 요소는 싣지 않는다. 대신 그 고지가 이미지 안 어디에 있는지 적는다.
  echo "이 파일은 Go 바이너리에 링크되는 모듈과 Go 표준 라이브러리만 다룬다. 컨테이너 이미지의 베이스 이미지와 빌드 때 설치한 OS 패키지의 고지는 각 이미지 안에 배포판이 둔 위치에 있다. 베이스가 Ubuntu 나 distroless 인 이미지는 패키지마다 /usr/share/doc/<패키지>/copyright 에 저작권 표시와 라이선스 조건이 있고, GPL 처럼 여러 패키지가 함께 쓰는 라이선스의 전문은 /usr/share/common-licenses 에 있다. 베이스가 Alpine 인 이미지는 패키지별 라이선스 이름만 /lib/apk/db/installed 에 기록한다. 베이스가 scratch 인 이미지는 Go 바이너리만 담으므로 이 파일이 그 이미지의 고지 전부다. 각 이미지의 베이스는 Dockerfile 의 FROM 줄에 있다. tenstorrent-driver-ds 이미지는 tt-kmd 커널 모듈 소스(GPL-2.0-only)를 /opt/tt-kmd-src/<버전> 에 담고, 같은 디렉터리에 그 저장소의 LICENSE 파일을 둔다."
  echo
  # Go 바이너리에는 표준 라이브러리와 런타임이 정적으로 링크된다. 판 번호는 싣지 않는다 — 툴체인 판이 달라도 LICENSE 글이 같으면 결과가 같다.
  f="$(go env GOROOT)/LICENSE"
  [ -f "$f" ] || { echo "Go 툴체인 LICENSE 가 없다: $f" >&2; return 3; }
  echo "== Go standard library and runtime — $(classify "$f")"
  echo; cat "$f"; echo
  while read -r path ver dir sub; do
    if [ "$path $ver" != "$cur" ]; then
      [ -z "$cur" ] || echo
      cur="$path $ver"; hdr=0; seen=()
      if [ -z "$(licfiles "$dir" | grep -viE '^NOTICE' || true)" ]; then
        echo "== $path $ver — 라이선스 파일 없음"; hdr=1; unknown=1
      fi
    fi
    rel="${sub#"$dir"}"; rel="${rel#/}"; rel="${rel:+$rel/}"
    for f in $(licfiles "$sub"); do
      [ -f "$sub/$f" ] || continue
      dup=0
      for s in ${seen[@]+"${seen[@]}"}; do if cmp -s "$sub/$f" "$s"; then dup=1; break; fi; done
      [ "$dup" = 0 ] || continue
      seen+=("$sub/$f"); name="$rel$f"
      case "$f" in
        [Nn][Oo][Tt][Ii][Cc][Ee]*) echo; echo "-- $path NOTICE ($name)"; echo; cat "$sub/$f"; continue ;;
      esac
      spdx="$(classify "$sub/$f")"
      [ "$spdx" = UNKNOWN ] && unknown=1
      if [ "$hdr" = 0 ]; then echo "== $path $ver — $spdx"; hdr=1; else echo; echo "-- $path $name ($spdx)"; fi
      if [ "$spdx" = Apache-2.0 ]; then
        [ -n "$apache_file" ] || apache_file="$sub/$f"
        if same "$sub/$f" "$apache_file"; then continue; fi
      fi
      echo; cat "$sub/$f"
    done
  done
  [ -z "$cur" ] || echo
  if [ -n "$apache_file" ]; then
    echo "== Apache License 2.0 본문(위에서 Apache-2.0 으로 표시한 파일 가운데 본문이 이 글과 같은 것의 공통)"
    echo; cat "$apache_file"
  fi
  [ "$unknown" = 0 ] || { echo "분류하지 못한 라이선스가 있다" >&2; return 3; }
}

usage() {
  echo "용법: $0 <모듈디렉터리>:<패키지패턴>[:<빌드태그>] ... | --write <파일> <인자...> | --check <파일> <인자...>" >&2
  exit 2
}

case "${1:-}" in
  --check|--write)
    [ $# -ge 3 ] || usage
    mode="$1"; f="$2"; shift 2
    tmp="$(mktemp "$f.XXXXXX")"; trap 'rm -f "$tmp"' EXIT
    modules "$@" | render > "$tmp"
    if [ "$mode" = --write ]; then
      chmod 644 "$tmp"; mv "$tmp" "$f"
    else
      diff -q "$tmp" "$f" >/dev/null || { echo "$f 가 현재 의존성과 다르다 — 이 저장소의 고지문 재생성 명령(operator: make notices, images: hack/third-party-notices-images.sh)으로 다시 만들 것" >&2; exit 1; }
    fi
    ;;
  ""|--*) usage ;;
  *) modules "$@" | render ;;
esac
