#!/usr/bin/env bash
# ============================================================
# comment-only-check.sh: BASE..HEAD 변경이 주석과 문서에만 머물렀는지 검사한다
# 상세: Go 는 주석을 뗀 출력과 표식 주석 순서, 셸·Dockerfile·Makefile·Python·HTML 은 주석 줄을 뗀
#       본문, YAML 은 적재한 데이터, 차트 템플릿은 {{/* */}} 와 # 전체 줄을 뗀 소스를 1차로
#       보고 렌더 결과(인증서 필드 제외)를 보조로, CRD 는 description 을 뗀 데이터를 두
#       판본에서 비교한다. 검사 대상은 현재 디렉터리의 git 루트다. --self-test 지원.
# 생성일: 2026-10-11 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail

toolroot="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail=0
bad() { echo "   [FAIL] $*"; fail=$((fail + 1)); }

build_tool() { (cd "$toolroot" && go build -o "$tmp/stripcomments" ./hack/stripcomments); }
go_code()    { "$tmp/stripcomments" - < "$1"; }
go_markers() { grep -E '^[[:space:]]*//[[:space:]]*(\+[A-Za-z]|go:|nolint)' "$1" || true; }
# 주석 줄 전체와, 따옴표가 없는 꼬리 주석만 뗀다. 따옴표가 있으면 본문으로 보고 그대로 비교한다.
strip_hash() { sed -E -e '1{/^#!/d}' -e '/^[[:space:]]*#/d' -e "s/[[:space:]]+#[^\"'\`]*\$//" "$1"; }
# strip_html: HTML 의 전행 주석만 뗀다 — <script> 의 // 줄과 한 줄짜리 <!-- -->. `#` 은 CSS 셀렉터라
# 주석으로 보지 않고, 꼬리 주석도 떼지 않는다(URL 의 // 와 구분할 수 없다).
strip_html() { sed -E -e '/^[[:space:]]*\/\//d' -e '/^[[:space:]]*<!--.*-->[[:space:]]*$/d' "$1"; }
yaml_data() {
  python3 - "$1" "$2" <<'PY'
import sys, yaml
drop = sys.argv[2] == 'nodesc'
def scrub(o):
    if isinstance(o, dict):
        return {k: scrub(v) for k, v in o.items() if not (drop and k == 'description')}
    if isinstance(o, list):
        return [scrub(x) for x in o]
    return o
print(repr([scrub(d) for d in yaml.safe_load_all(open(sys.argv[1]))]))
PY
}
# tpl_code <파일> [notes]: {{/* … */}} 블록(공백 트림 표식 포함, 여러 줄 비탐욕)을 뗀
# 템플릿 소스 — 템플릿 비교의 1차 판정(결정적, 토글과 무관하게 모든 템플릿을 본다). 두
# 번째 인자가 notes 가 아니면 추가로 # 로 시작하는 전체 줄을 떼고, 남는 빈 줄·공백만
# 있는 줄을 전부 버리고 남은 줄만 rstrip 해서 찍는다 — 주석 블록의 줄 수 자체가
# 바뀌면(압축·확장) 그 자리에 남는 빈 줄 수도 달라지는데, 그 차이까지 "코드가 바뀜" 으로
# 보면 주석만 압축한 커밋도 걸린다. notes(NOTES.txt)는 # 줄과 빈 줄이 사용자에게 그대로
# 보이는 출력이라 그 둘은 안 건드리고 {{/* */}} 제거 결과를 그대로 찍는다.
tpl_code() {
  python3 - "$1" "${2:-}" <<'PY'
import re, sys
s = open(sys.argv[1], encoding='utf-8').read()
s = re.sub(r'\{\{-?\s*/\*.*?\*/\s*-?\}\}', '', s, flags=re.S)
if sys.argv[2] == 'notes':
    print(s)
else:
    s = re.sub(r'^\s*#.*$', '', s, flags=re.M)
    for line in s.split('\n'):
        line = line.rstrip()
        if line.strip():
            print(line)
PY
}
# render_scrub: 렌더 출력(여러 YAML 문서)을 적재해 인증서·CA 필드를 재귀로 지운 뒤 비교용으로 찍는다.
render_scrub() {
  python3 - "$1" <<'PY'
import sys, yaml
DROP = {'ca.crt', 'tls.crt', 'tls.key', 'caBundle'}
def scrub(o):
    if isinstance(o, dict):
        return {k: scrub(v) for k, v in o.items() if k not in DROP}
    if isinstance(o, list):
        return [scrub(x) for x in o]
    return o
print(repr([scrub(d) for d in yaml.safe_load_all(open(sys.argv[1], encoding='utf-8'))]))
PY
}

# render_pass <라벨> [값파일 이름]: $tmp/b·$tmp/h 의 deploy/helm 을 렌더해 인증서 필드를
# 지우고 비교한다 — 템플릿 비교의 보조 판정(렌더 자체 실패는 FAIL). 값파일은 그 파일명을
# 두 트리 각각의 deploy/helm 아래에서 찾는다(경로가 트리마다 다르므로 공유 인자로 못 받는다).
render_pass() {
  local label="$1" valuesfile="${2:-}"
  local -a extra_b=() extra_h=()
  if [ -n "$valuesfile" ]; then
    extra_b=(-f "$tmp/b/deploy/helm/$valuesfile")
    extra_h=(-f "$tmp/h/deploy/helm/$valuesfile")
  fi
  helm template x "$tmp/b/deploy/helm" --kube-version 1.34.0 "${extra_b[@]}" > "$tmp/rb.txt" 2>&1 \
    || { bad "차트 렌더 실패(BASE, $label)"; return; }
  helm template x "$tmp/h/deploy/helm" --kube-version 1.34.0 "${extra_h[@]}" > "$tmp/rh.txt" 2>&1 \
    || { bad "차트 렌더 실패(HEAD, $label)"; return; }
  cmp -s <(render_scrub "$tmp/rb.txt") <(render_scrub "$tmp/rh.txt") || bad "차트 렌더 결과가 바뀜($label)"
}

# check <BASE> [HEAD]: 종료 0=주석·문서만 바뀜, 1=그 밖이 바뀜, 2=git 저장소 아님·리비전을 모름.
check() {
  local base="$1" head="${2:-HEAD}" root templates=0 status path list n=0 rc_b rc_h
  root="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "git 저장소가 아니다" >&2; return 2; }
  cd "$root"
  git rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "알 수 없는 리비전: $base" >&2; return 2; }
  git rev-parse --verify --quiet "$head^{commit}" >/dev/null || { echo "알 수 없는 리비전: $head" >&2; return 2; }
  build_tool
  list="$(git diff --name-status "$base" "$head")" \
    || { echo "리비전을 읽을 수 없다: $base..$head" >&2; return 2; }
  while IFS=$'\t' read -r status path; do
    [ -n "$path" ] || continue
    n=$((n + 1))
    case "$status" in
      A|D|R*|C*|T)
        case "$path" in
          *.go|*.sh|*.yaml|*.yml|*.tpl|*.py|*.mk|*.json|*.toml|*.html|go.mod|go.sum|*/go.mod|*/go.sum|Makefile|*/Makefile|Dockerfile*|*/Dockerfile*)
            bad "코드 파일 추가·삭제·이름 변경: $status $path" ;;
        esac
        continue ;;
    esac
    git show "$base:$path" > "$tmp/base" 2>/dev/null || { bad "BASE 에 없는 파일: $path"; continue; }
    git show "$head:$path" > "$tmp/head"
    case "$path" in
      *.go)
        rc_b=0; go_code "$tmp/base" > "$tmp/gb.txt" 2>/dev/null || rc_b=$?
        rc_h=0; go_code "$tmp/head" > "$tmp/gh.txt" 2>/dev/null || rc_h=$?
        if [ "$rc_b" -ne 0 ] || [ "$rc_h" -ne 0 ]; then
          bad "파싱 실패: $path"
        else
          cmp -s "$tmp/gb.txt" "$tmp/gh.txt" || bad "Go 코드가 바뀜: $path"
        fi
        cmp -s <(go_markers "$tmp/base") <(go_markers "$tmp/head") || bad "Go 표식 주석이 바뀜: $path" ;;
      *.tpl)
        # 위치와 무관하게 직접 비교(렌더 보조 비교는 deploy/helm 안에 있을 때만 같이 켠다).
        rc_b=0; tpl_code "$tmp/base" > "$tmp/tb.txt" 2>/dev/null || rc_b=$?
        rc_h=0; tpl_code "$tmp/head" > "$tmp/th.txt" 2>/dev/null || rc_h=$?
        if [ "$rc_b" -ne 0 ] || [ "$rc_h" -ne 0 ]; then
          bad "파싱 실패: $path"
        else
          cmp -s "$tmp/tb.txt" "$tmp/th.txt" || bad "템플릿 코드가 바뀜: $path"
        fi
        case "$path" in deploy/helm/templates/*) templates=1 ;; esac ;;
      deploy/helm/templates/*) templates=1 ;;
      config/crd/bases/*.yaml|internal/crdapply/crd/*.yaml|deploy/helm/crds/*.yaml)
        rc_b=0; yaml_data "$tmp/base" nodesc > "$tmp/yb.txt" 2>/dev/null || rc_b=$?
        rc_h=0; yaml_data "$tmp/head" nodesc > "$tmp/yh.txt" 2>/dev/null || rc_h=$?
        if [ "$rc_b" -ne 0 ] || [ "$rc_h" -ne 0 ]; then
          bad "파싱 실패: $path"
        else
          cmp -s "$tmp/yb.txt" "$tmp/yh.txt" || bad "CRD 가 description 밖에서 바뀜: $path"
        fi ;;
      *.yaml|*.yml)
        rc_b=0; yaml_data "$tmp/base" keep > "$tmp/yb.txt" 2>/dev/null || rc_b=$?
        rc_h=0; yaml_data "$tmp/head" keep > "$tmp/yh.txt" 2>/dev/null || rc_h=$?
        if [ "$rc_b" -ne 0 ] || [ "$rc_h" -ne 0 ]; then
          bad "파싱 실패: $path"
        else
          cmp -s "$tmp/yb.txt" "$tmp/yh.txt" || bad "YAML 데이터가 바뀜: $path"
        fi ;;
      *.sh|*.py|*.mk|Makefile|*/Makefile|Dockerfile*|*/Dockerfile*)
        cmp -s <(strip_hash "$tmp/base") <(strip_hash "$tmp/head") || bad "주석 밖 본문이 바뀜: $path"
        [ "$(head -n1 "$tmp/base")" = "$(head -n1 "$tmp/head")" ] || bad "첫 줄이 바뀜: $path" ;;
      *.html)
        cmp -s <(strip_html "$tmp/base") <(strip_html "$tmp/head") || bad "주석 밖 본문이 바뀜: $path" ;;
      *.md|*.txt|LICENSE|LICENSE.*|*/LICENSE|*/LICENSE.*|OWNERS|PROJECT|.gitignore|.dockerignore|*/.gitignore|*/.dockerignore) ;;
      *)
        # 확장자로 못 가른 파일은 첫 줄 shebang 으로 셸·파이썬만 가른다(그 밖은 그대로 FAIL).
        if [[ "$(head -n1 "$tmp/head" 2>/dev/null)" =~ ^#!.*((ba)?sh|python[0-9.]*)([[:space:]]|$) ]]; then
          cmp -s <(strip_hash "$tmp/base") <(strip_hash "$tmp/head") || bad "주석 밖 본문이 바뀜: $path"
          [ "$(head -n1 "$tmp/base")" = "$(head -n1 "$tmp/head")" ] || bad "첫 줄이 바뀜: $path"
        else
          bad "검사 규칙이 없는 파일 종류: $path"
        fi ;;
    esac
  done <<< "$list"
  if [ "$templates" -eq 1 ]; then
    mkdir -p "$tmp/b" "$tmp/h"
    git archive "$base" deploy/helm | tar -x -C "$tmp/b"
    git archive "$head" deploy/helm | tar -x -C "$tmp/h"
    while IFS= read -r -d '' bf; do
      rel="${bf#"$tmp/b/"}"
      hf="$tmp/h/$rel"
      [ -f "$hf" ] || continue
      mode=""; [ "$(basename "$bf")" = "NOTES.txt" ] && mode="notes"
      cmp -s <(tpl_code "$bf" "$mode") <(tpl_code "$hf" "$mode") || bad "템플릿 코드가 바뀜: $rel"
    done < <(find "$tmp/b/deploy/helm" -path '*/templates/*' ! -name '*.tpl' -type f -print0)
    if command -v helm >/dev/null; then
      render_pass "기본값"
      [ -f "$tmp/b/deploy/helm/values-k8s1.34.yaml" ] && render_pass "values-k8s1.34.yaml" "values-k8s1.34.yaml"
    else
      bad "helm 없음 — 렌더 비교 불가"
    fi
  fi
  if [ "$fail" -ne 0 ]; then echo "주석 밖 변경 $fail 건 ($base..$head)"; return 1; fi
  echo "주석 전용 변경 확인 통과 ($base..$head, 파일 ${n}개)"
}

# self_test: Go·YAML·셸 픽스처(통과 1·위반 4) 에 템플릿 통과·템플릿 위반·알 수 없는 리비전·
# CRD(통과+위반)·확장자 없는 셸(통과+위반)·HTML(통과+위반) 을 더해 열일곱 경우를 본다.
self_test() {
  local r rc out
  r="$(mktemp -d)"
  trap 'rm -rf "$r"' RETURN
  git -C "$r" init -q
  commit() { git -C "$r" add -A && git -C "$r" -c user.email=t@t -c user.name=t commit -q -m "chore: $1"; }
  run_check() { ( cd "$r" && check HEAD~1 HEAD ); }
  printf 'package p\n\n// +kubebuilder:validation:Enum=a;b\n// 설명 하나\nvar X = 1\n' > "$r/a.go"
  printf 'a: 1 # 주석\nb:\n  - x\n' > "$r/v.yaml"
  printf '#!/usr/bin/env bash\n# 머리말\necho hi # 꼬리\n' > "$r/s.sh"
  commit base
  # 1) 주석만 바꿈 → 통과
  printf 'package p\n\n// +kubebuilder:validation:Enum=a;b\n// 설명 둘\nvar X = 1\n' > "$r/a.go"
  printf 'a: 1 # 다른 주석\nb:\n  - x\n' > "$r/v.yaml"
  printf '#!/usr/bin/env bash\n# 새 머리말\necho hi # 새 꼬리\n' > "$r/s.sh"
  commit c1
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 1: 종료 $rc '$out'" >&2; return 1; }
  # 2) Go 코드 변경 → 실패
  printf 'package p\n\n// +kubebuilder:validation:Enum=a;b\n// 설명 둘\nvar X = 2\n' > "$r/a.go"; commit c2
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"Go 코드가 바뀜"* ]] || { echo "self-test 2: 종료 $rc '$out'" >&2; return 1; }
  # 3) 표식 주석 제거 → 실패
  printf 'package p\n\n// 설명 둘\nvar X = 2\n' > "$r/a.go"; commit c3
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"표식 주석이 바뀜"* ]] || { echo "self-test 3: 종료 $rc '$out'" >&2; return 1; }
  # 4) YAML 값 변경 → 실패
  printf 'a: 2 # 다른 주석\nb:\n  - x\n' > "$r/v.yaml"; commit c4
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"YAML 데이터가 바뀜"* ]] || { echo "self-test 4: 종료 $rc '$out'" >&2; return 1; }
  # 5) 셸 본문 변경 → 실패
  printf '#!/usr/bin/env bash\n# 새 머리말\necho bye # 새 꼬리\n' > "$r/s.sh"; commit c5
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"주석 밖 본문이 바뀜"* ]] || { echo "self-test 5: 종료 $rc '$out'" >&2; return 1; }
  # 6) 꺼진 템플릿의 # 전체 줄 주석 3→1 압축(빈 줄 수 변화) + 항상 렌더되는 인증서
  #    템플릿의 주석만 변경(스크럽 없으면 매번 다른 tls.crt 로 렌더가 갈린다) → 둘 다 통과
  mkdir -p "$r/deploy/helm/templates"
  printf 'apiVersion: v2\nname: x\nversion: 0.1.0\n' > "$r/deploy/helm/Chart.yaml"
  printf 'x: false\n' > "$r/deploy/helm/values.yaml"
  printf '{{- if .Values.x }}\n# 설명 하나\n# 설명 둘\n# 설명 셋\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: off-cm\n{{- end }}\n' \
    > "$r/deploy/helm/templates/off.yaml"
  printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: x-cert\n# 인증서 하나\ndata:\n  tls.crt: {{ (genCA "x" 1).Cert | b64enc }}\n' \
    > "$r/deploy/helm/templates/cert.yaml"
  commit c6base
  printf '{{- if .Values.x }}\n# 설명 하나로 합침\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: off-cm\n{{- end }}\n' \
    > "$r/deploy/helm/templates/off.yaml"
  printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: x-cert\n# 인증서 하나로 수정\ndata:\n  tls.crt: {{ (genCA "x" 1).Cert | b64enc }}\n' \
    > "$r/deploy/helm/templates/cert.yaml"
  commit c6
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 6: 종료 $rc '$out'" >&2; return 1; }
  # 7) 꺼진 템플릿의 코드(ConfigMap 이름) 변경 → 실패(인증서 템플릿은 안 건드림)
  printf '{{- if .Values.x }}\n# 설명 하나로 합침\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: off-cm-renamed\n{{- end }}\n' \
    > "$r/deploy/helm/templates/off.yaml"
  commit c7
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"템플릿 코드가 바뀜"* ]] || { echo "self-test 7: 종료 $rc '$out'" >&2; return 1; }
  # 8) 알 수 없는 리비전 → 종료 2
  rc=0; out="$( (cd "$r" && check deadbeef HEAD) 2>&1 )" || rc=$?
  [ "$rc" -eq 2 ] && [[ "$out" == *"알 수 없는 리비전: deadbeef"* ]] || { echo "self-test 8: 종료 $rc '$out'" >&2; return 1; }
  # 9) CRD 꼴 YAML: description 만 바꾸면 통과, enum 값을 바꾸면 실패
  mkdir -p "$r/config/crd/bases"
  printf 'a:\n  description: 하나\n  enum: [c, d]\n' > "$r/config/crd/bases/x.yaml"
  commit c9base
  printf 'a:\n  description: 둘\n  enum: [c, d]\n' > "$r/config/crd/bases/x.yaml"
  commit c9pass
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 9a: 종료 $rc '$out'" >&2; return 1; }
  printf 'a:\n  description: 둘\n  enum: [c, e]\n' > "$r/config/crd/bases/x.yaml"
  commit c9fail
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"CRD 가 description 밖에서 바뀜"* ]] || { echo "self-test 9b: 종료 $rc '$out'" >&2; return 1; }
  # 10) 확장자 없는 셸(shebang 으로 판정): 꼬리 주석만 바꾸면 통과, 본문을 바꾸면 실패
  printf '#!/bin/bash\n# 머리말\necho hi # 꼬리\n' > "$r/probe"
  commit c10base
  printf '#!/bin/bash\n# 새 머리말\necho hi # 새 꼬리\n' > "$r/probe"
  commit c10pass
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 10a: 종료 $rc '$out'" >&2; return 1; }
  printf '#!/bin/bash\n# 새 머리말\necho bye # 새 꼬리\n' > "$r/probe"
  commit c10fail
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"주석 밖 본문이 바뀜"* ]] || { echo "self-test 10b: 종료 $rc '$out'" >&2; return 1; }
  # 11) HTML: <script> 의 전행 // 주석만 바꾸면 통과, CSS 셀렉터(# 로 시작하는 줄)를 바꾸면 실패
  printf '<html>\n<style>\n#a { color: red }\n</style>\n<script>\n// 설명 하나\nvar x = 1;\n</script>\n</html>\n' > "$r/u.html"
  commit c11base
  printf '<html>\n<style>\n#a { color: red }\n</style>\n<script>\n// 설명 둘\nvar x = 1;\n</script>\n</html>\n' > "$r/u.html"
  commit c11pass
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 11a: 종료 $rc '$out'" >&2; return 1; }
  printf '<html>\n<style>\n#b { color: red }\n</style>\n<script>\n// 설명 둘\nvar x = 1;\n</script>\n</html>\n' > "$r/u.html"
  commit c11fail
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"주석 밖 본문이 바뀜"* ]] || { echo "self-test 11b: 종료 $rc '$out'" >&2; return 1; }
  # 12) 이어진 두 문장 사이에 새 전행 주석 → 통과(줄 간격 변화는 코드 변경이 아니다)
  printf 'package p\n\nfunc F() int {\n\ta := 1\n\treturn a\n}\n' > "$r/b.go"; commit c12base
  printf 'package p\n\nfunc F() int {\n\ta := 1\n\t// 설명\n\treturn a\n}\n' > "$r/b.go"; commit c12
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 12: 종료 $rc '$out'" >&2; return 1; }
  # 13) 복합 리터럴 원소 앞 새 주석, 그리고 문장 사이 주석 블록 삭제 → 통과
  printf 'package p\n\nvar V = []int{\n\t1,\n\t2,\n}\n' > "$r/c.go"; commit c13base
  printf 'package p\n\nvar V = []int{\n\t1,\n\t// 둘째\n\t2,\n}\n' > "$r/c.go"; commit c13a
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 13a: 종료 $rc '$out'" >&2; return 1; }
  printf 'package p\n\nfunc F() int {\n\ta := 1\n\treturn a\n}\n' > "$r/b.go"; commit c13b
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 13b: 종료 $rc '$out'" >&2; return 1; }
  # 14) 빈 줄만 추가 → 실패(빈 줄 배치도 코드 변경으로 본다)
  printf 'package p\n\nfunc F() int {\n\ta := 1\n\n\treturn a\n}\n' > "$r/b.go"; commit c14
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"Go 코드가 바뀜"* ]] || { echo "self-test 14: 종료 $rc '$out'" >&2; return 1; }
  # 15) raw string 안의 // 줄은 주석이 아니다 → 바꾸면 실패
  printf 'package p\n\nvar S = `a\n// x\nb`\n' > "$r/d.go"; commit c15base
  printf 'package p\n\nvar S = `a\n// y\nb`\n' > "$r/d.go"; commit c15
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"Go 코드가 바뀜"* ]] || { echo "self-test 15: raw string 변경을 놓침 종료 $rc '$out'" >&2; return 1; }
  # 16) //line 지시문 뒤라도 물리 줄로 판정한다 → 코드 줄을 주석 줄로 착각해 지우면 안 된다
  printf 'package p\n//line a.go:5\n// note\nvar A = 1\nvar B = 2\n' > "$r/e.go"; commit c16base
  printf 'package p\n//line a.go:5\n// note\nvar A = 1\nvar B = 3\n' > "$r/e.go"; commit c16
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"Go 코드가 바뀜"* ]] || { echo "self-test 16: //line 뒤 코드 변경을 놓침 종료 $rc '$out'" >&2; return 1; }
  # 17) 코드로 끝나는 여러 줄 블록 주석(이어진 것 포함): 주석 글만 바꾸면 통과, 그 줄 코드를 바꾸면 실패
  printf 'package p\n\n/* a\n b */ var X = 1\n\n/* c\n d */ /* e\n f */ var Y = 2\n' > "$r/f.go"; commit c17base
  printf 'package p\n\n/* a\n 둘 */ var X = 1\n\n/* c\n 둘 */ /* e\n 셋 */ var Y = 2\n' > "$r/f.go"; commit c17a
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 0 ] || { echo "self-test 17a: 종료 $rc '$out'" >&2; return 1; }
  printf 'package p\n\n/* a\n 둘 */ var X = 1\n\n/* c\n 둘 */ /* e\n 셋 */ var Y = 3\n' > "$r/f.go"; commit c17b
  rc=0; out="$(run_check)" || rc=$?
  [ "$rc" -eq 1 ] && [[ "$out" == *"Go 코드가 바뀜"* ]] || { echo "self-test 17b: 종료 $rc '$out'" >&2; return 1; }
  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  "") echo "용법: $0 BASE [HEAD] | --self-test" >&2; exit 2 ;;
  *) check "$@" ;;
esac
