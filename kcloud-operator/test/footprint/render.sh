#!/usr/bin/env bash
# ============================================================
# render.sh: footprint JSON 을 Markdown 대조표로 렌더한다
# 상세: measure.sh 가 낸 JSON 을 받아 정적/라이브 표와 한계 절을 낸다.
#       실측 데이터 문서와 같은 frontmatter 를 붙여 그대로 커밋 가능.
# 생성일: 2026-09-20
# ============================================================
set -euo pipefail

IN="${1:-/dev/stdin}"
python3 - "$IN" <<'PY'
import sys, json
data = json.load(open(sys.argv[1]))
date = data.get("date", "")
commit = data.get("commit", "")
mode = data.get("mode", "")
targets = data.get("targets", {})
limits = data.get("limits", [])

STATIC_COLS = ["Deployment", "DaemonSet", "StatefulSet", "Job", "CRD",
               "ClusterRole", "Role", "Webhook", "Service", "containers"]
LIVE_COLS = [("namespaces", "네임스페이스"), ("pods", "파드"), ("daemonsets", "DaemonSet"),
             ("containers", "컨테이너"), ("cpuRequestMilli", "CPU 요청(m)"),
             ("memRequestMi", "메모리 요청(Mi)"), ("cpuUsedMilliMedian", "CPU 사용 중앙값(m)"),
             ("memUsedMiMedian", "메모리 사용 중앙값(Mi)")]

def cell(v):
    if v is None: return "—"
    if isinstance(v, list): return ", ".join(v)
    return str(v)

out = []
out.append("---")
out.append('title: "쿠버네티스 리소스 설치량 대조표: kcloud-operator 정적·라이브 측정"')
out.append('description: "kcloud-operator 와 NVIDIA GPU Operator·RBLN NPU Operator Helm 차트를 '
           '렌더해 워크로드·CRD·RBAC 개수를 정적 비교하고, 클러스터에 설치된 kcloud 구성요소의 '
           '파드·컨테이너·CPU/메모리 요청량과 실사용 중앙값을 라이브 측정한 결과."')
out.append(f"created: {date}")
out.append(f"updated: {date}")
out.append("status: measured")
out.append("---")
out.append("")
out.append("# 쿠버네티스 리소스 설치량 대조표")
out.append("")
out.append(f"측정 날짜: {date}")
out.append("")
out.append(f"측정 기준 커밋: `{commit}` · 측정 모드: `{mode}`")
out.append("")
out.append("`helm template` 로 렌더한 매니페스트에서 kind 별 개수를 세어 정적으로 비교했고, "
           "클러스터에 설치된 kcloud 구성요소는 읽기 전용 `kubectl get`·`kubectl top` 으로 "
           "파드·요청량·실사용량을 라이브 측정했다. 사용량 중앙값은 여러 표본의 중앙값이며, "
           "평균 대신 중앙값을 쓴 이유는 기동 직후 한 번의 폭주가 평균을 통째로 끌고 가기 때문이다.")
out.append("")

# 정적 표
out.append("## 정적 측정 (helm 렌더)")
out.append("")
versions = [(n, t.get("chartVersion")) for n, t in targets.items() if t.get("chartVersion")]
if versions:
    out.append("차트 버전: " + ", ".join("`%s` %s" % (n, v) for n, v in versions))
    out.append("")
if any(t.get("static") for t in targets.values()):
    header = "| 대상 | " + " | ".join(STATIC_COLS) + " |"
    sep = "|" + "---|" * (len(STATIC_COLS) + 1)
    out.append(header)
    out.append(sep)
    for name, t in targets.items():
        s = t.get("static")
        if s is None:
            row = "| `" + name + "` | " + " | ".join(["미획득"] * len(STATIC_COLS)) + " |"
        else:
            row = "| `" + name + "` | " + " | ".join(cell(s.get(c)) for c in STATIC_COLS) + " |"
        out.append(row)
    out.append("")
else:
    out.append("정적 측정을 실행하지 않았다.")
    out.append("")

# 라이브 표
out.append("## 라이브 측정 (클러스터 설치 구성요소)")
out.append("")
live_targets = {n: t.get("live") for n, t in targets.items() if t.get("live")}
sample_note = None
for lv in live_targets.values():
    if lv.get("samples") is not None:
        sample_note = "사용량 표본: %s회 × %s초 간격" % (lv.get("samples"), lv.get("intervalSeconds"))
        break
if sample_note:
    out.append(sample_note)
    out.append("")
if live_targets:
    header = "| 대상 | " + " | ".join(label for _, label in LIVE_COLS) + " |"
    sep = "|" + "---|" * (len(LIVE_COLS) + 1)
    out.append(header)
    out.append(sep)
    for name, lv in live_targets.items():
        row = "| `" + name + "` | " + " | ".join(cell(lv.get(k)) for k, _ in LIVE_COLS) + " |"
        out.append(row)
    out.append("")
else:
    out.append("라이브 측정을 실행하지 않았거나 설치된 대상이 없다.")
    out.append("")

# 한계
out.append("## 이 측정의 한계")
out.append("")
if limits:
    for lim in limits:
        out.append(f"- {lim}")
else:
    out.append("- 기록된 한계 없음.")
out.append("")

print("\n".join(out))
PY
