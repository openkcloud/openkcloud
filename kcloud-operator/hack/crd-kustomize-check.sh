#!/usr/bin/env bash
# ============================================================
# crd-kustomize-check.sh: config/crd/kustomization.yaml 이 config/crd/bases 전체를 나열하는지 검사
# 상세: controller-gen 은 bases 에 CRD 를 쓰지만 kustomization 의 resources 목록은 손으로만
#       바뀌어, make install 이 일부 CRD 만 적용하는 상태가 조용히 지속될 수 있다.
#       두 집합이 다르면 diff 를 출력하고 exit 1.
# 생성일: 2026-09-21 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
want=$(ls config/crd/bases/*.yaml | xargs -n1 basename | sort)
got=$(grep -E '^- bases/' config/crd/kustomization.yaml | sed 's#^- bases/##' | sort)
if [ "$want" != "$got" ]; then
  echo "config/crd/kustomization.yaml 의 resources 가 config/crd/bases 와 다르다(< bases, > kustomization):" >&2
  diff <(echo "$want") <(echo "$got") >&2 || true
  exit 1
fi
echo "crd-kustomize-check: CRD $(echo "$want" | wc -l)개 일치"
