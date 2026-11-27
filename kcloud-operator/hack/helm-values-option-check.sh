#!/usr/bin/env bash
# ============================================================
# helm-values-option-check.sh: helm upgrade 의 --reuse-values 단독 사용을 금지한다
# 상세: 검사 1 은 *.sh 파일에서 helm 과 --reuse-values 가 한 셸 문장에 같이 있으면 거부한다
#       (--reset-then-reuse-values 를 쓴다). 검사 2 는 문서가 권하는 값 키(--set/--set-string,
#       Configuration 표)가 values.yaml 또는 values.schema.json 에 실재하는지 본다. 판정
#       근거는 각 함수의 본문 주석 참고.
# 생성일: 2026-09-24 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail
root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

# Helm v4.0.1 에서 --reuse-values 는 직전 릴리스 계산값을 통째로 재사용해, 사용자 값으로
# 덮지 않은 차트 기본값 변경이 반영되지 않는다 — 차트만 고친 변경이 조용히 빠진다. 대신
# --reset-then-reuse-values 를 쓴다("then-" 뒤가 대시 하나짜리 "-reuse-values" 라 부분
# 문자열로 걸리지 않아 오탐이 없다). \ 로 이어진 줄들을 하나의 셸 문장으로 합쳐서 판정하는
# 이유는 `helm upgrade ... \` 다음 줄에 `--reuse-values` 가 오는 실제 흔한 형태를 물리적
# 줄 단위로는 놓치기 때문이다.
hits="$(find "$root" -type f -name '*.sh' \
    -not -path '*/.git/*' -not -path '*/.superpowers/*' \
    -not -name 'helm-values-option-check*' -print0 \
  | xargs -0 -r awk '
      function flush() {
        if (buf != "" && buf ~ /helm/ && buf ~ /--reuse-values/) {
          print FILENAME ":" startline ":" buf
        }
        buf = ""
      }
      FNR == 1 { flush() }
      {
        if (buf == "") startline = FNR
        line = $0
        if (sub(/\\[ \t]*$/, " ", line)) { buf = buf line }
        else { buf = buf line; flush() }
      }
      END { flush() }
    ' || true)"

if [ -n "$hits" ]; then
  echo "helm-values-option-check: --reuse-values 단독 사용 발견 — --reset-then-reuse-values 로 바꾼다" >&2
  printf '%s\n' "$hits" >&2
  exit 1
fi
echo "helm-values-option-check: --reuse-values 단독 사용 0건"

# 검사 2: 문서가 권하는 값 키(--set/--set-string, Configuration 표)가 실재하는지.
python3 - "$root" <<'PY'
import json
import os
import re
import sys

import yaml
# PyYAML/json 으로 직접 읽는다 — snap 으로 설치된 yq 는 /tmp 를 못 읽어 이 스크립트의
# 임시 트리(self-test, CI)에서 쓸 수 없다.

root = sys.argv[1]
values_path = os.path.join(root, "deploy/helm/values.yaml")
if not os.path.isfile(values_path):
    print("helm-values-option-check: values.yaml 없음 — 검사 2 건너뜀")
    sys.exit(0)

with open(values_path, encoding="utf-8") as f:
    data_tree = yaml.safe_load(f) or {}

schema_path = os.path.join(root, "deploy/helm/values.schema.json")
schema_tree = {}
defs = {}
if os.path.isfile(schema_path):
    with open(schema_path, encoding="utf-8") as f:
        schema_tree = json.load(f)
    defs = schema_tree.get("$defs") or {}


def path_exists_data(node, segs):
    """values.yaml 데이터 트리에서 경로가 실재하는지(기본값이 선언된 키)."""
    if not segs:
        return True
    if not isinstance(node, dict):
        return False
    seg, rest = segs[0], segs[1:]
    if seg.startswith("<") and seg.endswith(">"):
        return any(path_exists_data(v, rest) for v in node.values())
    if seg.endswith("*"):
        prefix = seg[:-1]
        return any(path_exists_data(v, rest) for k, v in node.items() if k.startswith(prefix))
    if seg not in node:
        return False
    return path_exists_data(node[seg], rest)


def resolve_ref(node):
    if isinstance(node, dict) and "$ref" in node:
        return defs.get(node["$ref"].split("/")[-1], {})
    return node


def branches_of(node):
    """$ref 를 풀고, allOf 가 있으면 그 가지들을 재귀적으로 펼친 리스트로 돌려준다."""
    node = resolve_ref(node)
    if isinstance(node, dict) and "allOf" in node:
        out = []
        for b in node["allOf"]:
            out.extend(branches_of(b))
        return out
    return [node]


# <vendor> 처럼 꺾쇠로 감싼 세그먼트와 kubectl* 처럼 끝에 별표가 붙은 세그먼트는 Configuration
# 표가 벤더 공통 키·접두사 묶음 키를 적는 방식이다 — 해당 위치의 자식 키 중 하나(또는 그
# 접두사로 시작하는 키 중 하나)라도 나머지 경로를 만족하면 허용으로 본다.
def seg_matches(seg, k):
    if seg.startswith("<") and seg.endswith(">"):
        return True
    if seg.endswith("*"):
        return k.startswith(seg[:-1])
    return k == seg


def merged_child_schemas(branches, seg):
    """allOf 가지 전체에서 seg 하나에 대한 서브스키마를 모은다. 한 가지라도 seg 를
    false 로 닫아 두면(벤더별 제외 정의) None(=금지)을 돌려준다 — 교집합 의미라
    다른 가지가 실스키마를 들고 있어도 이긴다. 아무 가지도 seg 를 적어두지 않았으면
    빈 리스트(=판단 보류, 호출부가 열림/닫힘을 따로 본다)."""
    found = []
    for b in branches:
        if not isinstance(b, dict):
            continue
        props = b.get("properties") or {}
        if seg in props:
            if props[seg] is False:
                return None
            found.append(props[seg])
    return found


def path_exists_schema(node, segs):
    """values.schema.json 에서 경로가 허용되는지(기본값 없이도 스키마가 허용하는 키 포함).

    allOf 가지 중 하나가 어떤 키를 false 로 닫아 두면(벤더별 제외) 다른 가지가 그
    키를 허용해도 거절해야 한다 — 이건 교집합 의미다. 그런데 그 키 밑으로 더 내려갈
    때도 같은 교집합이 다시 적용돼야 한다(예: tenstorrent 전용 제외 정의가
    "driver.trackOnly" 만 따로 닫아 두고 "driver" 자체는 열어 둔다 — 두 가지 모두의
    driver 서브스키마를 "합쳐서" 다음 세그먼트를 봐야 trackOnly 거절이 살아난다).
    그래서 매 깊이마다 가지들을 따로 재귀하지 않고, 이 키를 적어 둔 모든 가지의
    서브스키마를 합쳐 새 allOf 노드로 묶은 뒤 그걸로 재귀한다.
    """
    if not segs:
        return True
    branches = branches_of(node)
    seg, rest = segs[0], segs[1:]
    wildcard = (seg.startswith("<") and seg.endswith(">")) or seg.endswith("*")

    if wildcard:
        names = set()
        for b in branches:
            if isinstance(b, dict):
                names |= set((b.get("properties") or {}).keys())
        for name in names:
            if not seg_matches(seg, name):
                continue
            sub = merged_child_schemas(branches, name)
            if sub:
                if path_exists_schema({"allOf": sub}, rest):
                    return True
        return False

    sub = merged_child_schemas(branches, seg)
    if sub is None:
        return False  # 한 가지가 명시적으로 닫음
    if sub:
        return path_exists_schema({"allOf": sub}, rest)
    # 어느 가지도 이 키를 적어두지 않았다 — properties 자체가 없는 리프, 또는 모든 가지가
    # additionalProperties 를 열어 둔 블록이면 통과(오탐보다 누락이 낫다).
    has_props = any(isinstance(b, dict) and isinstance(b.get("properties"), dict) for b in branches)
    if not has_props:
        return True
    closed = any(isinstance(b, dict) and b.get("additionalProperties", True) is False for b in branches)
    return not closed


def declared(raw_key):
    # 값("=" 뒤)은 보지 않고 키만 본다 — --set "key=${VAR}" 처럼 인용부호로 감싸인 호출을
    # 위해 추출한 키 앞뒤의 따옴표를 벗긴다.
    segs = raw_key.strip("\"'").split(".")
    return path_exists_data(data_tree, segs) or path_exists_schema(schema_tree, segs)


TARGET_DOCS = [
    "README.md",
    "deploy/helm/README.md",
    "deploy/helm/INSTALL.md",
    "deploy/helm/UPGRADE.md",
    "deploy/helm/templates/NOTES.txt",
    "deploy/helm/install.sh",
]

bad = []
checked = {"set": 0, "table": 0}

set_re = re.compile(r"--set(?:-string)?\s+([^\s=]+)=")
# 헤더 셀(구분선 바로 위 행)의 첫 칸이 이 중 하나일 때만 그 표의 데이터 행을 본다 — 이
# 저장소에는 키가 아닌 것(CRD Kind 이름, Reconciler 이름, 리네임 전/후 이름)을 첫 칸에
# 코드체로 적는 표도 있어, 헤더로 거르지 않으면 그 표들이 전부 오탐이 된다.
ALLOWED_TABLE_HEADERS = {"파라미터", "값", "values 키", "values key"}


def is_sep_row(line):
    inner = line.replace("|", "")
    return bool(inner.strip()) and all(c in "-: " for c in inner)


def check_table_cell(doc, lineno, cell1):
# 데이터 행 첫 칸의 코드체(`...`) 조각을 전부 뽑는다 — 첫 조각은 그대로 키 경로, 그 뒤
# 조각("`b`"/"`.b`" 처럼 이어 붙는 조각)은 먼저 독립 경로로 시도하고, 아니면 앞 조각의
# 마지막 세그먼트부터 차례로 떼어내 그 자리에 끼워 넣어 본다(예: "driverInstallPolicies.
# furiosa.driver" / ".rngd.driver" 는 "driverInstallPolicies.rngd.driver" 로 성립).
# 이 저장소 문서에는 이 규칙으로 못 푸는 형태가 아직 없다 — 오탐이 나오면 그때 사유와
# 함께 예외를 추가한다.
    frags = re.findall(r"`([^`]+)`", cell1)
    if not frags:
        return
    checked["table"] += len(frags)
    first_segs = frags[0].strip("\"'").split(".")
    if not declared(frags[0]):
        bad.append("%s:%d: 표 키 %s (없음)" % (doc, lineno, frags[0]))
    for cont in frags[1:]:
        frag = cont[1:] if cont.startswith(".") else cont
        if declared(frag):
            continue
        frag_segs = frag.strip("\"'").split(".")
        ok = False
        for k in range(1, len(first_segs)):
            if path_exists_data(data_tree, first_segs[:-k] + frag_segs) or path_exists_schema(
                schema_tree, first_segs[:-k] + frag_segs
            ):
                ok = True
                break
        if not ok:
            bad.append("%s:%d: 표 키 %s (이어붙여도 없음)" % (doc, lineno, cont))


for doc in TARGET_DOCS:
    path = os.path.join(root, doc)
    if not os.path.isfile(path):
        continue
    in_key_table = False
    prev_line = None
    with open(path, encoding="utf-8") as f:
        for lineno, raw_line in enumerate(f, 1):
            line = raw_line.rstrip("\n")

            for m in set_re.finditer(line):
                checked["set"] += 1
                if not declared(m.group(1)):
                    bad.append("%s:%d: --set %s=... (없음)" % (doc, lineno, m.group(1)))

            is_row = line.lstrip().startswith("|") and line.rstrip().endswith("|")
            if is_row:
                if is_sep_row(line):
                    hm = re.match(r"^\s*\|\s*([^|]*?)\s*\|", prev_line) if prev_line else None
                    header1 = hm.group(1).strip() if hm else ""
                    in_key_table = header1 in ALLOWED_TABLE_HEADERS
                elif in_key_table:
                    cm = re.match(r"^\s*\|([^|]*)\|", line)
                    check_table_cell(doc, lineno, cm.group(1) if cm else "")
                prev_line = line
            else:
                in_key_table = False
                prev_line = None

if bad:
    print("helm-values-option-check: 문서가 권하는 값 키 중 없는 것 발견", file=sys.stderr)
    for b in bad:
        print("  " + b, file=sys.stderr)
    sys.exit(1)
print(
    "helm-values-option-check: 문서 값 키 전부 확인됨 (--set %d건, 표 키 %d건)"
    % (checked["set"], checked["table"])
)
PY
