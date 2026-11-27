#!/usr/bin/env bash
# ============================================================
# comment-check.sh: 주석·문서에 남은 이력 서술·세션 식별자·날짜·긴 주석 블록을 거부한다
# 상세: 머리말 수정일의 괄호 설명, 머리말 밖의 날짜, 과거 서술 종결, 세션 식별자, 7줄 이상
#       주석 블록을 위반으로 본다. 규칙은 아래 PY 한 곳에만 둔다. 인자: [ROOT] [PREFIX…].
#       --self-test 는 임시 저장소에서 스물다섯 경우로 자가 검증한다.
# 생성일: 2026-10-11 | 수정일: 2026-10-14
# ============================================================
set -euo pipefail

# 예외. 각 줄은 "경로 — 이유" 다.
#   hack/comment-check.sh        — 규칙 문자열과 자가시험 픽스처 자체.
#   hack/comment-only-check.sh   — 자가시험 픽스처 문자열.
#   hack/workspace-cite-check.sh — .superpowers·작업공간·task-N 문자열이 자기 규칙 설명이다.
#   config/default/kustomization.yaml·config/manager/manager.yaml·config/manifests/
#   kustomization.yaml·config/prometheus/kustomization.yaml — kubebuilder 가 생성한
#   스캐폴드(주석 처리된 예시 템플릿). 우리 서술이 아니고 주석 해제로 켜는 경로다.
ALLOW='^(hack/comment-check\.sh|hack/comment-only-check\.sh|hack/workspace-cite-check\.sh|config/default/kustomization\.yaml|config/manager/manager\.yaml|config/manifests/kustomization\.yaml|config/prometheus/kustomization\.yaml)$'

PY=$(cat <<'EOF'
import os, re, signal, subprocess, sys
signal.signal(signal.SIGPIPE, signal.SIG_DFL)
root = sys.argv[1]
prefixes = [p.rstrip('/') for p in sys.argv[2:]]
allow = re.compile(os.environ['ALLOW'])
skip = re.compile(r'(^|/)(go\.sum|go\.mod|.*\.json|zz_generated.*|.*\.pb\.go|.*\.tgz|.*\.png|.*\.svg|LICENSE.*)$'
                  r'|(^|/)(testdata|fixtures|runs|vendor)/|^(config/crd/bases|internal/crdapply/crd|deploy/helm/crds)/')
HDR = re.compile(r'(생성일|수정일)')
PAREN = re.compile(r'(생성일|수정일): ?20\d\d-\d\d-\d\d ?\(')
DATE = re.compile(r'\b20\d\d-\d\d-\d\d\b')
PAST = re.compile(r'[했였됐었았]다(?=[.,)]|$)')
TOKEN = re.compile(r'\b(D|I|C|M|R|N|U|HL|WP|EC)-\d+\b|review-[a-z]\d+\b|\bTask ?\d+\b|계획 [A-Z]\b|계획서'
                   r'|수정 라운드|\bRound ?\d|브리프|addendum|\bRuling\b|리뷰어|리뷰가 |리뷰에서|원안|수정 파도'
                   r'|재리뷰|superpowers|\bSDD\b')
MARKER = re.compile(r'^\s*//\s*(\+[A-Za-z]|go:|nolint|lint:|revive:)')
FENCE = re.compile(r'^\s*`{3,}')
CLOSER = re.compile(r'^\s*(//|#)\s*=+\s*$')
MAXBLOCK = 6
def ls(*opts):
    r = subprocess.run(['git', 'ls-files', *opts], cwd=root, capture_output=True, text=True)
    if r.returncode != 0:
        sys.stderr.write('git ls-files 실패(종료 %d): %s\n' % (r.returncode, r.stderr.strip()))
        sys.exit(2)
    return set(r.stdout.split('\n'))
files = sorted(ls('--cached', '--others', '--exclude-standard') - ls('--deleted') - {''})
hit = set()
checked = set()
unread = []
viol = []

def add(f, i, kind, line):
    viol.append('%s:%d: [%s] %s' % (f, i, kind, line.strip()[:120]))

def four_checks(f, i, kind, text):
    if PAREN.search(text):
        add(f, i, '머리말 괄호', text)
    if DATE.search(text) and not HDR.search(text):
        add(f, i, '날짜', text)
    if TOKEN.search(text):
        add(f, i, '식별자', text)
    if kind != 'md' and PAST.search(text):
        add(f, i, '과거 서술', text)

def tail_comment(line, marker):
    # 전체 줄 주석이 아닌 줄의 꼬리 주석 본문을 돌려준다. marker 앞이 공백이 아니면 None.
    # marker 앞부분에서 "·'·백틱을 각각 세어 셋 다 짝수일 때만 꼬리 주석으로 본다(문자열
    # 리터럴 안에 있으면 그 중 하나가 홀수다). 블록 길이 계산에는 안 넣는다.
    idx = line.find(marker)
    if idx <= 0 or line[idx - 1] not in ' \t':
        return None
    pre = line[:idx]
    for q in ('"', "'", '`'):
        if len(re.findall(r'(?<!\\)' + re.escape(q), pre)) % 2:
            return None
    return line[idx + len(marker):]

def header_extent(lines, cm):
    # 파일 1·2번째 줄에서 시작하는 주석 run 안에서 생성일|수정일 줄을 찾고, 그 뒤로도 같은
    # run 이 이어지는 동안 닫는 ==== 줄이 나올 때까지(그 줄 포함) 머리말로 본다 — 생성일
    # 줄의 설명이 다음 줄로 이어져도(연속행) 닫는 줄 전까지는 머리말이다. 그 run 안에 닫는
    # 줄이 없으면 생성일 줄까지만. 생성일 줄 자체가 없으면 0(머리말 면제 없음).
    hstart = 0
    if lines and cm.match(lines[0]):
        hstart = 1
    elif len(lines) > 1 and cm.match(lines[1]):
        hstart = 2
    if hstart == 0:
        return 0
    i = hstart
    hdr_line = 0
    while i <= len(lines) and cm.match(lines[i - 1]):
        if hdr_line == 0 and HDR.search(lines[i - 1]):
            hdr_line = i
        if hdr_line and CLOSER.match(lines[i - 1]):
            return i
        i += 1
    return hdr_line

def frontmatter_extent(lines):
    # Markdown 파일의 첫 줄이 정확히 --- 면 그 뒤의 닫는 --- 줄(포함)까지를 YAML frontmatter
    # 로 보고 그 줄 번호(1-based)를 돌려준다. 첫 줄이 --- 가 아니거나 닫는 줄이 없으면 0.
    if not lines or lines[0].rstrip() != '---':
        return 0
    for i in range(2, len(lines) + 1):
        if lines[i - 1].rstrip() == '---':
            return i
    return 0

for f in files:
    m = []
    if prefixes:
        m = [p for p in prefixes if f == p or f.startswith(p + '/')]
        if not m:
            continue
        hit.update(m)
    if allow.search(f) or skip.search(f):
        continue
    base = os.path.basename(f)
    if f.endswith('.go'):
        kind, cm = 'go', re.compile(r'^\s*//')
    elif f.endswith('.md'):
        kind, cm = 'md', None
    elif f.startswith('deploy/helm/templates/') or f.endswith('.tpl'):
        kind, cm = 'tpl', None
    elif f.endswith(('.sh', '.yaml', '.yml', '.py', '.mk')) or base.startswith(('Dockerfile', 'Makefile')):
        kind, cm = 'hash', re.compile(r'^\s*#(?!!)')
    else:
        continue
    try:
        lines = open(os.path.join(root, f), encoding='utf-8').read().split('\n')
    except (OSError, UnicodeDecodeError):
        unread.append(f)
        continue
    checked.update(m)
    hdr_end = header_extent(lines, cm) if cm is not None else 0
    if hdr_end:
        hstart = 1 if cm.match(lines[0]) else 2
        borders = sum(1 for j in range(hstart, hdr_end + 1) if CLOSER.match(lines[j - 1]))
        hdr_len = (hdr_end - hstart + 1) - borders
        if hdr_len > MAXBLOCK:
            add(f, hstart, '긴 머리말 %d줄' % hdr_len, lines[hstart - 1])
    fm_end = frontmatter_extent(lines) if kind == 'md' else 0
    run = start = 0
    inblock = False
    in_fence = False
    for i, line in enumerate(lines, 1):
        if kind == 'md' and FENCE.match(line):
            in_fence = not in_fence
        if cm is not None:
            is_c = cm.match(line) is not None
        else:
            is_c = True
        if is_c and not (kind == 'md' and (in_fence or i <= fm_end)):
            four_checks(f, i, kind, line)
        elif cm is not None and not is_c:
            tail = tail_comment(line, '//' if kind == 'go' else '#')
            if tail is not None:
                four_checks(f, i, kind, tail)
        if cm is not None:
            if is_c and not MARKER.match(line):
                if run == 0:
                    start = i
                run += 1
            else:
                if run > MAXBLOCK and start > hdr_end:
                    add(f, start, '긴 블록 %d줄' % run, lines[start - 1])
                run = 0
            if i == hdr_end:
                # 머리말은 여기서 끝난다 — 주석이 안 끊기고 이어져도 다음 줄부터 새 블록이다.
                run = 0
        elif kind == 'tpl':
            if '{{/*' in line:
                inblock, run, start = True, 0, i
            if inblock:
                run += 1
            if '*/}}' in line and inblock:
                inblock = False
                if run > MAXBLOCK:
                    add(f, start, '긴 블록 %d줄' % run, lines[start - 1])
                run = 0
    if cm is not None and run > MAXBLOCK and start > hdr_end:
        add(f, start, '긴 블록 %d줄' % run, lines[start - 1])

for p in prefixes:
    if p not in hit:
        sys.stderr.write('인자가 가리키는 파일이 없다: %s\n' % p)
        sys.exit(2)
for p in prefixes:
    if p not in checked:
        sys.stderr.write('검사 대상 파일 없이 건너뛴 인자: %s\n' % p)
for f in unread:
    sys.stderr.write('읽지 못해 건너뛴 파일: %s\n' % f)
if viol:
    print('주석·문서 정리 규칙 위반 %d건' % len(viol))
    print('\n'.join(viol))
if unread:
    sys.exit(2)
if viol:
    sys.exit(1)
print('주석 정리 검사 통과')
EOF
)

# scan <루트> [접두…]: 종료 0=위반 없음, 1=위반 있음, 2=루트가 git 저장소가 아니거나 인자가 가리키는
# 파일 없음 또는 읽지 못한 파일 있음 또는 git ls-files 실패(검사 파일 0개를 통과로 읽지 않게).
scan() {
  local root="$1"; shift
  ( cd "$root" 2>/dev/null && git rev-parse --is-inside-work-tree >/dev/null 2>&1 ) \
    || { echo "git 저장소가 아니다: $root" >&2; return 2; }
  ALLOW="$ALLOW" python3 -c "$PY" "$root" "$@"
}

# self_test: 규칙마다 한 경우씩(전체 줄 하나 + 꼬리 주석 하나), 통과 경우 하나, 접두 필터 하나,
# git 아님 하나, 머리말 면제 경계(설명 블록 하나 + 연속행 하나) 둘, 꼬리 주석 따옴표 짝(위반+통과) 둘,
# Markdown frontmatter 제외 하나, 긴 머리말(연속행 포함 위반 하나 + 7줄 위반 하나) 둘,
# 미추적 파일 인자 하나, 가리키는 파일이 없는 인자 하나, 빈 접두 하나, 삭제된 추적 파일 인자 하나.
# 건너뜀 파일만 가리키는 인자 하나, 지원하지 않는 확장자 하나, 읽기 실패 둘, git ls-files 실패 하나.
self_test() {
  local tmp rc out
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  mkdir -p "$tmp/repo/a" "$tmp/repo/b" "$tmp/notgit"
  git -C "$tmp/repo" init -q
  expect() { # expect <번호> <기대 종료> <출력에 있어야 할 문자열> <scan 인자…>
    local n="$1" want="$2" needle="$3"; shift 3
    rc=0; out="$(scan "$@" 2>&1)" || rc=$?
    [ "$rc" -eq "$want" ] && [[ "$out" == *"$needle"* ]] \
      || { echo "self-test $n: 종료 $rc(기대 $want) 출력 '$out'" >&2; return 1; }
  }
  # 1) 머리말 괄호
  printf '// ====\n// x.go: 설명\n// 생성일: 2026-01-01 | 수정일: 2026-01-02 (토큰 제거)\n// ====\npackage a\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 1 1 '[머리말 괄호]' "$tmp/repo"
  # 2) 머리말 밖 날짜
  printf '// ====\n// x.go: 설명\n// 생성일: 2026-01-01 | 수정일: 2026-01-02\n// ====\npackage a\n\n// 2026-03-01 에 바꿈\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 2 1 '[날짜]' "$tmp/repo"
  # 3) 과거 서술
  printf 'package a\n\n// 원래는 이렇게 했다.\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 3 1 '[과거 서술]' "$tmp/repo"
  # 4) 식별자
  printf 'package a\n\n// D-9 참고\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 4 1 '[식별자]' "$tmp/repo"
  # 5) 7줄 블록(파일 중간)
  printf 'package a\n\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\n// 7\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 5 1 '[긴 블록 7줄]' "$tmp/repo"
  # 6) 통과: 머리말 5줄 + 표식 8줄 연속 + 6줄 블록
  printf '// ====\n// x.go: 설명\n// 상세: 둘\n// 생성일: 2026-01-01 | 수정일: 2026-01-02\n// ====\npackage a\n\n// +kubebuilder:a\n// +kubebuilder:b\n// +kubebuilder:c\n// +kubebuilder:d\n// +kubebuilder:e\n// +kubebuilder:f\n// +kubebuilder:g\n// +optional\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 6 0 '주석 정리 검사 통과' "$tmp/repo"
  # 7) Markdown 식별자
  printf '# 제목\n\nTask 3 에서 넣은 절차다.\n' > "$tmp/repo/b/r.md"
  git -C "$tmp/repo" add -A; expect 7 1 '[식별자]' "$tmp/repo"
  # 8) 꼬리 주석 식별자(a/ 로 범위를 좁혀 b/ 의 7번 위반과 섞이지 않게 본다)
  printf 'package a\n\nvar X = 1 // D-9 참고\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 8 1 '[식별자]' "$tmp/repo" a
  # 9) 접두 필터: a/ 를 통과 픽스처로 되돌리면 위반은 b/ 에만 남아 a/ 만 보면 통과
  printf '// ====\n// x.go: 설명\n// 상세: 둘\n// 생성일: 2026-01-01 | 수정일: 2026-01-02\n// ====\npackage a\n\n// +kubebuilder:a\n// +kubebuilder:b\n// +kubebuilder:c\n// +kubebuilder:d\n// +kubebuilder:e\n// +kubebuilder:f\n// +kubebuilder:g\n// +optional\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\nvar X = 1\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 9 0 '주석 정리 검사 통과' "$tmp/repo" a
  # 10) git 저장소 아님
  expect 10 2 'git 저장소가 아니다' "$tmp/notgit"
  # 11) 머리말 4줄 뒤 바로 7줄 설명이 이어지는 파일 → 머리말 면제는 머리말까지만, 7줄 블록은 위반
  printf '// ====\n// x.go: 설명\n// 생성일: 2026-01-01 | 수정일: 2026-01-02\n// ====\n// 1\n// 2\n// 3\n// 4\n// 5\n// 6\n// 7\npackage a\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 11 1 '[긴 블록 7줄]' "$tmp/repo" a
  # 12) 생성일 줄이 연속행으로 이어지고 그 뒤에 닫는 줄이 있는 머리말 → 연속행도 머리말로
  #     한 번에 세어진다(별도 [긴 블록] 이 아니라 [긴 머리말 8줄] 하나로, 테두리 둘 제외)
  printf '// ====\n// x.go: 설명\n// 생성일: 2026-01-01 | 수정일: 2026-01-02 — 이어지는 설명이다\n// 둘째 줄\n// 셋째 줄\n// 넷째 줄\n// 다섯째 줄\n// 여섯째 줄\n// 일곱째 줄\n// ====\npackage a\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 12 1 '[긴 머리말 8줄]' "$tmp/repo" a
  # 13) 꼬리 주석 앞부분의 따옴표가 짝(두 개)이면 문자열 밖이므로 검사한다 → 식별자
  printf 'package a\n\nvar x = "a" // D-9\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 13 1 '[식별자]' "$tmp/repo" a
  # 14) marker 가 문자열 리터럴 안(앞부분 따옴표가 홀수)이면 꼬리 주석으로 안 본다 → 통과
  printf 'package a\n\nvar x = "// D-9"\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 14 0 '주석 정리 검사 통과' "$tmp/repo" a
  # 15) 첫 줄이 --- 인 Markdown frontmatter 의 날짜는 메타데이터라 제외, 본문은 깨끗 → 통과
  printf -- '---\ntitle: 문서\ndate: 2026-01-01\n---\n# 제목\n\n본문은 깨끗하다.\n' > "$tmp/repo/b/r.md"
  git -C "$tmp/repo" add -A; expect 15 0 '주석 정리 검사 통과' "$tmp/repo" b
  # 16) 머리말 본문이 테두리 제외 7줄이면 위반(면제 범위는 그대로, 길이만 따로 본다)
  printf '// ====\n// x.go: 설명\n// 생성일: 2026-01-01 | 수정일: 2026-01-02\n// 1\n// 2\n// 3\n// 4\n// 5\n// ====\npackage a\n' > "$tmp/repo/a/x.go"
  git -C "$tmp/repo" add -A; expect 16 1 '[긴 머리말 7줄]' "$tmp/repo" a
  # 17) 미추적 새 파일을 인자로 넘기면 검사한다(커밋 전 확인이 거짓 통과가 되지 않게)
  printf 'package a\n\n// D-9 참고\nvar Y = 1\n' > "$tmp/repo/a/new.go"
  expect 17 1 '[식별자]' "$tmp/repo" a/new.go
  rm -f "$tmp/repo/a/new.go"
  # 18) 아무 파일도 가리키지 않는 인자는 종료 2(오타 난 경로가 통과로 보이지 않게)
  expect 18 2 '가리키는 파일이 없다' "$tmp/repo" a/nope.go
  # 19) 빈 접두는 어떤 파일도 가리키지 않으므로 종료 2(아무것도 안 보고 통과하지 않게)
  expect 19 2 '가리키는 파일이 없다' "$tmp/repo" ''
  # 20) 추적됐다가 디스크에서 지워진 파일을 접두로 주면 종료 2(열지 못해 건너뛴 채 통과하지 않게)
  printf 'package a\n' > "$tmp/repo/a/g.go"
  git -C "$tmp/repo" add a/g.go; rm -f "$tmp/repo/a/g.go"
  expect 20 2 '가리키는 파일이 없다' "$tmp/repo" a/g.go
  # 21) 건너뜀 규칙(.json)에 걸리는 파일만 가리키는 인자는 종료 0 이되 검사하지 않았다고 알린다
  printf '{}\n' > "$tmp/repo/a/x.json"
  expect 21 0 '검사 대상 파일 없이 건너뛴 인자: a/x.json' "$tmp/repo" a/x.json
  # 22) 지원하지 않는 확장자(.txt)만 가리키는 인자도 같다
  printf 'D-9\n' > "$tmp/repo/a/n.txt"
  expect 22 0 '검사 대상 파일 없이 건너뛴 인자: a/n.txt' "$tmp/repo" a/n.txt
  # 23) UTF-8 로 읽지 못한 파일이 있으면 알리고 종료 2(검사가 불완전하다)
  printf '\xff\xfe\n' > "$tmp/repo/a/bad.go"
  expect 23 2 '읽지 못해 건너뛴 파일: a/bad.go' "$tmp/repo" a/bad.go
  # 24) git ls-files 가 실패하면 종료 2(rev-parse 는 index 를 읽지 않으므로 손상된 index 를 따로 가리킨다)
  printf 'x' > "$tmp/badindex"
  GIT_INDEX_FILE="$tmp/badindex" expect 24 2 'git ls-files 실패' "$tmp/repo"
  # 25) 읽지 못한 파일과 위반이 함께 있으면 위반을 출력하고 종료 2
  printf 'package a\n\n// D-9 참고\nvar X = 1\n' > "$tmp/repo/a/v.go"
  git -C "$tmp/repo" add a/v.go
  expect 25 2 '[식별자]' "$tmp/repo" a/bad.go a/v.go
  echo "self-test ok"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) scan "${1:-$(cd "$(dirname "$0")/.." && pwd)}" "${@:2}" ;;
esac
