#!/usr/bin/env bash
# ============================================================
# rbac-count.sh: helm 차트를 렌더해 ClusterRole·Role 별 권한 동사 쌍 수를 센다
# 상세: 렌더 결과의 모든 ClusterRole·Role 에서 규칙마다 (apiGroup, resource, verb) 삼중쌍을
#       모아 distinct 개수를 센다(`*` verb 는 리소스당 한 쌍으로 계산, 확장하지 않음). 이 값은
#       특권 사용량 실측의 rbac_verb_pairs/role_verb_pairs 와
#       같은 정의다. 기본은 표(PAIRS KIND NAME), --json 은 "kind/name"→수 객체를 출력한다.
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

json=0
[[ "${1:-}" == "--json" ]] && json=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART="$HERE/../deploy/helm"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

helm template kcloud-operator "$CHART" >"$tmp"

python3 - "$tmp" "$json" <<'PY'
import sys, json, yaml

path = sys.argv[1]
as_json = sys.argv[2] == "1"

with open(path) as f:
    docs = list(yaml.safe_load_all(f))

counts = {}
for doc in docs:
    if not doc:
        continue
    kind = doc.get("kind")
    if kind not in ("ClusterRole", "Role"):
        continue
    name = doc.get("metadata", {}).get("name", "?")
    triples = set()
    for rule in (doc.get("rules") or []):
        for group in (rule.get("apiGroups") or [""]):
            for res in (rule.get("resources") or []):
                for verb in (rule.get("verbs") or []):
                    triples.add((group, res, verb))
    counts["%s/%s" % (kind, name)] = len(triples)

if as_json:
    print(json.dumps(counts, indent=2, sort_keys=True))
else:
    print("%5s  %-13s  %s" % ("PAIRS", "KIND", "NAME"))
    cr_total = 0
    role_total = 0
    for key in sorted(counts):
        kind, name = key.split("/", 1)
        n = counts[key]
        if kind == "ClusterRole":
            cr_total += n
        else:
            role_total += n
        print("%5d  %-13s  %s" % (n, kind, name))
    print("---")
    print("%5d  %-13s  %s" % (cr_total, "ClusterRole", "(total)"))
    print("%5d  %-13s  %s" % (role_total, "Role", "(total)"))
PY
