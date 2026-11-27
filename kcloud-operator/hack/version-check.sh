#!/usr/bin/env bash
# ============================================================
# version-check.sh: 문서의 "현재 릴리스 세트" 표기가 Chart.yaml 과 일치하는지 확인
# 상세: deploy/helm/Chart.yaml 의 appVersion·npu.ai/paired-release 주석을 docs 저장소의
#       "현재 (릴리스) 세트는 vX.Y.Z" 문장과 대조한다. Chart↔values 정합·태그↔appVersion
#       정합은 각각 chart_version_sync_test.go·release.yml 이 보므로 여기서는 문서만 본다.
#       docs 저장소가 없으면(CI) 건너뛴다.
# 생성일: 2026-09-21 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
docs_dir="$repo_root/../docs"
chart="$repo_root/deploy/helm/Chart.yaml"

if [[ ! -d "$docs_dir" ]]; then
  echo "docs 저장소 없음(${docs_dir}) — 건너뜀"
  exit 0
fi

app_version=$(grep -oP '^appVersion:\s*\K\S+' "$chart" || true)
paired_release=$(grep -oP '^\s+npu\.ai/paired-release:\s*\K\S+' "$chart" || true)

if [[ -z "$app_version" ]]; then
  echo "version-check: ${chart} 에 appVersion 이 없다" >&2
  exit 1
fi
if [[ -z "$paired_release" ]]; then
  echo "version-check: ${chart} annotations 에 npu.ai/paired-release 가 없다" >&2
  exit 1
fi

fail=0

# check_doc: 문서 파일 하나에서 "현재 릴리스 세트" 문장을 찾아 Chart.yaml 값과 대조한다.
# 두 문서의 문장이 줄바꿈으로 나뉘어 있어(마크다운 개행), perl -0777 로 파일 전체를
# 하나의 문자열로 읽고 /s(dotall) 로 줄바꿈을 건너뛰며 매칭한다. paired 매칭은
# .*? 대신 (?:(?!\n\n).)*? 로 묶어, 토큰이 빠졌을 때 같은 단락 밖(뒤쪽 각주 등)의
# 다른 vX.Y.Z-k8s1.28 값을 잘못 집지 않게 막는다.
check_doc() {
  local doc="$1"
  if [[ ! -f "$doc" ]]; then
    echo "version-check: 문서 없음 — ${doc}" >&2
    fail=1
    return
  fi

  local cur paired
  cur=$(perl -0777 -ne 'print $1 if /현재(?: 릴리스)? 세트는 .*?(v[0-9]+\.[0-9]+\.[0-9]+)/s' "$doc" || true)
  paired=$(perl -0777 -ne 'print $1 if /현재(?: 릴리스)? 세트는 (?:(?!\n\n).)*?v[0-9]+\.[0-9]+\.[0-9]+(?:(?!\n\n).)*?(v[0-9]+\.[0-9]+\.[0-9]+-k8s1\.28)/s' "$doc" || true)

  if [[ -z "$cur" ]]; then
    echo "version-check: ${doc} 에 '현재 (릴리스 )세트는 vX.Y.Z' 문장이 없다" >&2
    fail=1
    return
  fi
  if [[ "$cur" != "$app_version" ]]; then
    echo "version-check: ${doc} 현재 릴리스 세트=${cur}, Chart.yaml appVersion=${app_version} (불일치)" >&2
    fail=1
  fi

  if [[ -z "$paired" ]]; then
    echo "version-check: ${doc} 에 K8s 1.28 라인 토큰(vX.Y.Z-k8s1.28)이 없다" >&2
    fail=1
    return
  fi
  if [[ "$paired" != "$paired_release" ]]; then
    echo "version-check: ${doc} K8s 1.28 토큰=${paired}, Chart.yaml paired-release=${paired_release} (불일치)" >&2
    fail=1
  fi
}

check_doc "$docs_dir/README.md"
check_doc "$docs_dir/status.md"

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

echo "version-check: 문서↔Chart 릴리스 세트 정합 확인 완료 (appVersion=${app_version}, paired-release=${paired_release})"
