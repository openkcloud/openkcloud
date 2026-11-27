#!/usr/bin/env bash
# ============================================================
# measure.sh: kcloud-operator 의 K8s 리소스 설치량을 정적·라이브로 측정한다
# 상세: 정적은 helm 렌더에서 kind 별 개수를, 라이브는 운영 클러스터에 설치된
#       구성요소의 파드·요청량·사용량 중앙값을 잰다. 결과는 JSON 하나로 낸다.
#       클러스터는 읽기 전용(get/top)으로만 건드린다. 사용량 중앙값은 Prometheus 가
#       있으면 PromQL 10분 창을, 없으면 kubectl top 을, 그것도 없으면 null 을 쓴다.
# 생성일: 2026-09-20 | 수정일: 2026-09-21
# ============================================================
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/count.sh
source "$HERE/lib/count.sh"

REPO_ROOT="$(cd "$HERE/../.." && pwd)"

# 벤더 차트 위치 — 둘 다 reference/PROVENANCE.md 에 출처·버전·sha256 을 고정해 뒀다.
# nvidia 는 Apache-2.0 이라 tgz 를 저장소에 그대로 커밋해 네트워크 없이 재현된다.
REFDIR="$HERE/reference"
NVIDIA_TGZ="${NVIDIA_TGZ:-$REFDIR/gpu-operator-v25.10.1.tgz}"
NVIDIA_SHA256="e13359580e9e0bfbf560fb38e39911b76a17bdbb5b0110fdd911f22467c8d339"
# rbln 은 사유 라이선스라 tgz 를 커밋하지 않는다(.gitignore). ensure_rbln_chart() 가
# 실행 시 이 URL 에서 받아 RBLN_TGZ 자리에 캐시하고 sha256 을 대조한다.
RBLN_VERSION="0.5.0"
RBLN_TGZ="${RBLN_TGZ:-$REFDIR/rbln-npu-operator-chart-${RBLN_VERSION}.tgz}"
RBLN_URL="${RBLN_URL:-https://github.com/RBLN-SW/rbln-npu-operator/releases/download/v${RBLN_VERSION}/rbln-npu-operator-chart-${RBLN_VERSION}.tgz}"
RBLN_SHA256="a33880c79f73b54f837706970ab22fbf901030c1fd11f56a90ed672f5fd6de3c"

MODE="both"
CHARTS_CSV="kcloud,nvidia-gpu-operator,rbln-npu-operator"
SAMPLES="${SAMPLES:-5}"
INTERVAL="${INTERVAL:-10}"
DRY_RUN="${DRY_RUN:-0}"
OUT="${OUT:-$HERE/footprint-$(date +%Y%m%d).json}"

# 라이브 파드 필터: kcloud 가 배포·소유하는 구성요소만 센다. api-demo 같은 사용자
# 워크로드와 kube-system 의 무관한 시스템 파드는 이 정규식으로 걸러진다.
KCLOUD_RX='^(kcloud|nvidia|furiosa|rbln|dcgm|tt)-'

usage() {
  echo "usage: measure.sh [-m static|live|both] [--charts a,b,c] [-o OUT.json]"
  echo "  env: SAMPLES(기본 5) INTERVAL(기본 10초) DRY_RUN=1 OUT"
  echo "       NVIDIA_TGZ RBLN_TGZ RBLN_URL PROM_URL(자세한 설명은 README.md)"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -m) MODE="${2:?}"; shift 2 ;;
    --charts) CHARTS_CSV="${2:?}"; shift 2 ;;
    -o) OUT="${2:?}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "모르는 인자: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$MODE" in static|live|both) ;; *) echo "-m 은 static|live|both 여야 함" >&2; exit 2 ;; esac
require_cmd helm jq python3 curl || exit 1
# 정적 렌더는 count.sh 의 yaml_docs 가 PyYAML 을 쓴다. python3 만으로는 부족하다.
if [[ "$MODE" != live ]]; then
  python3 -c 'import yaml' 2>/dev/null \
    || { echo "필요한 python 모듈 없음: PyYAML (pip install pyyaml)" >&2; exit 1; }
fi
# kubectl 은 라이브 관측에만 필요하다 — 정적 렌더는 클러스터에 접근하지 않는다.
if [[ "$MODE" != static ]]; then
  require_cmd kubectl || exit 1
fi

IFS=',' read -r -a CHARTS <<<"$CHARTS_CSV"

# chart_source <이름> — 그 차트의 helm 소스(로컬 경로 또는 tgz). rbln 은 ensure_rbln_chart
# 가 먼저 받아 둔 자리를 가리키기만 한다(부재 시 이후 -e 검사가 "미획득"으로 남긴다).
chart_source() {
  case "$1" in
    kcloud)              echo "$REPO_ROOT/deploy/helm" ;;
    nvidia-gpu-operator) echo "$NVIDIA_TGZ" ;;
    rbln-npu-operator)   echo "$RBLN_TGZ" ;;
    *) return 1 ;;
  esac
}

# sha256_ok <파일> <기대값> — 파일의 sha256 이 기대값과 같으면 0.
sha256_ok() {
  [[ "$(sha256sum "$1" 2>/dev/null | awk '{print $1}')" == "$2" ]]
}

# ensure_rbln_chart — RBLN 차트 tgz(RBLN_TGZ)가 없으면 RBLN_URL 에서 받고, 있으면
# sha256 만 대조한다. 라이선스가 재배포를 명확히 허용하지 않아 이 tgz 는 저장소에
# 커밋하지 않는다(reference/.gitignore). 대조 불일치는 손상·변조로 보고 스크립트
# 전체를 즉시 실패시킨다(명령 치환 안에서 부르면 안 됨 — exit 이 하위 셸만 끝낸다).
# 다운로드 자체의 실패(오프라인 등)는 1 을 반환할 뿐 실패시키지 않는다 — 호출부가
# 기존 "chart not obtainable" 경로(파일 부재)로 자연스럽게 떨어지게 둔다.
ensure_rbln_chart() {
  if [[ -e "$RBLN_TGZ" ]]; then
    sha256_ok "$RBLN_TGZ" "$RBLN_SHA256" && return 0
    echo "RBLN 차트 sha256 불일치: $RBLN_TGZ (기대 $RBLN_SHA256) — 손상 또는 변조 의심" >&2
    exit 1
  fi
  curl -fsSL -m 60 -o "$RBLN_TGZ.tmp" "$RBLN_URL" 2>/dev/null || { rm -f "$RBLN_TGZ.tmp"; return 1; }
  if sha256_ok "$RBLN_TGZ.tmp" "$RBLN_SHA256"; then
    mv "$RBLN_TGZ.tmp" "$RBLN_TGZ"
    return 0
  fi
  echo "내려받은 RBLN 차트 sha256 불일치: $RBLN_URL (기대 $RBLN_SHA256)" >&2
  rm -f "$RBLN_TGZ.tmp"
  exit 1
}

# live_namespaces — 라이브 측정 대상 네임스페이스 중 실재하는 것만 개행 구분으로.
# DRY_RUN 에서는 클러스터를 조회하지 않고 대상 후보를 그대로 낸다(DRY 는 클러스터 호출 0회).
live_namespaces() {
  local want=(kcloud kube-system rbln-system) ns
  if [[ "$DRY_RUN" == 1 ]]; then
    printf '%s\n' "${want[@]}"
    return
  fi
  for ns in "${want[@]}"; do
    kubectl get ns "$ns" >/dev/null 2>&1 && echo "$ns"
  done
}

want_static() { [[ "$MODE" == static || "$MODE" == both ]]; }
want_live()   { [[ "$MODE" == live   || "$MODE" == both ]]; }

LIMITS=()
TARGETS='{}'
TMPD="$(mktemp -d)"
# PF_PID: lib/count.sh 의 prom_used() 가 포트포워드 자식을 띄우는 동안 채운다 — curl 이
# set -e 로 함수 정리 코드에 못 닿고 중단되거나(타임아웃 등) 이 스크립트 자체가 신호로
# 죽어도, 이 EXIT trap 이 마지막으로 한 번 더 kill 해 포트포워드가 안 남게 한다.
trap 'rm -rf "$TMPD"; [[ -n "${PF_PID:-}" ]] && kill "$PF_PID" 2>/dev/null' EXIT

for name in "${CHARTS[@]}"; do
  static_json="null"
  live_json="null"
  chart_version="null"

  if want_static; then
    # 둘 다 명령 치환 밖에서 직접 부른다 — sha256 불일치 시 exit 1 이 스크립트
    # 전체를 끝내야 하는데, 명령 치환 안이면 하위 셸만 끝난다.
    if [[ "$name" == rbln-npu-operator && "$DRY_RUN" != 1 ]]; then
      ensure_rbln_chart || true
    elif [[ "$name" == nvidia-gpu-operator && "$DRY_RUN" != 1 && -e "$NVIDIA_TGZ" ]]; then
      sha256_ok "$NVIDIA_TGZ" "$NVIDIA_SHA256" || {
        echo "NVIDIA 차트 sha256 불일치: $NVIDIA_TGZ (기대 $NVIDIA_SHA256) — 손상 또는 변조 의심" >&2
        exit 1
      }
    fi
    if ! src="$(chart_source "$name")"; then
      LIMITS+=("$name: 알 수 없는 차트")
    elif [[ "$DRY_RUN" == 1 ]]; then
      echo "helm template $name $src --include-crds"
      echo "helm show chart $src  (chartVersion)"
    elif [[ ! -e "$src" ]]; then
      LIMITS+=("chart not obtainable: $name ($src 없음 — NEEDS VENDOR CHART)")
    elif helm template "$name" "$src" --include-crds >"$TMPD/$name.yaml" 2>"$TMPD/$name.err"; then
      static_json="$(kind_counts "$TMPD/$name.yaml")"
      # 비교 차트 버전을 기록한다 — 어느 버전과 대조했는지 결과 문서만으로 알 수 있게.
      cv="$(helm show chart "$src" 2>/dev/null | sed -n 's/^version:[[:space:]]*//p' | head -1)"
      [[ -n "$cv" ]] && chart_version="$(jq -Rn --arg v "$cv" '$v')"
      case "$name" in
        kcloud)
          LIMITS+=("kcloud 정적 Job 은 모두 Helm hook Job(CRD apply/cleanup·pre-delete gate)이라 상시 워크로드가 아니다.") ;;
        rbln-npu-operator)
          LIMITS+=("rbln-npu-operator 차트는 사유 라이선스라 저장소에 커밋하지 않고 릴리스에서 sha256 검증 후 받는다(reference/PROVENANCE.md) — 오프라인이면 이 값은 null.") ;;
      esac
    else
      LIMITS+=("chart not obtainable: $name (렌더 실패: $(head -1 "$TMPD/$name.err"))")
    fi
  fi

  # kcloud 정적 렌더가 실패해 static null 이 되면 게이트가 측정 부재를 통과시키지 않도록
  # 여기서 종료한다(벤더 차트는 미획득이어도 null+한계로 남긴다). DRY_RUN 은 예외.
  if want_static && [[ "$DRY_RUN" != 1 && "$name" == kcloud && "$static_json" == null ]]; then
    echo "kcloud 차트 정적 렌더 실패 — static null 로 게이트를 통과시키지 않도록 종료(1)" >&2
    [[ -s "$TMPD/$name.err" ]] && head -20 "$TMPD/$name.err" >&2
    exit 1
  fi

  # 라이브는 클러스터에 설치된 kcloud 만 잰다. 벤더 차트는 미설치라 live=null.
  if want_live && [[ "$name" == kcloud ]]; then
    mapfile -t NSL < <(live_namespaces)
    if [[ "$DRY_RUN" == 1 ]]; then
      echo "kubectl get pods,daemonsets -n {${NSL[*]}} -o json  (필터 $KCLOUD_RX)"
      echo "Prometheus 발견(PROM_URL 또는 label app.kubernetes.io/name=prometheus) → 되면 PromQL 10분 창"
      echo "실패/부재 시 kubectl top pods --containers  x${SAMPLES} @${INTERVAL}s → 그것도 없으면 null"
    else
      req="$(pod_requests "$KCLOUD_RX" "${NSL[@]}")"
      dsc="$(ds_count "$KCLOUD_RX" "${NSL[@]}")"
      # 사용량 중앙값: Prometheus 가 있으면 그걸로, 없거나 질의가 실패하면 kubectl top 으로.
      prom="$(discover_prometheus)"
      used_cpu=null; used_mem=null
      if [[ -n "$prom" ]]; then
        read -r used_cpu used_mem <<<"$(prom_used "$prom" "$KCLOUD_RX")"
        if [[ "$used_cpu" != null ]]; then
          LIMITS+=("사용량 중앙값 출처: Prometheus 10분 창 30초 스텝($prom)")
        else
          LIMITS+=("Prometheus 질의 실패/무응답($prom) — kubectl top 로 폴백")
        fi
      else
        LIMITS+=("Prometheus 미발견 — kubectl top 로 직접 진행")
      fi
      if [[ "$used_cpu" == null ]]; then
        read -r used_cpu used_mem <<<"$(top_used "$KCLOUD_RX" "$SAMPLES" "$INTERVAL" "${NSL[@]}")"
        [[ "$used_cpu" == null ]] && LIMITS+=("사용량 중앙값 표본 없음(Prometheus 없음/실패, kubectl top 도 표본 없음) — null")
      fi
      if ! kubectl get ns rbln-system >/dev/null 2>&1; then
        LIMITS+=("rbln-system 네임스페이스 없음 — RBLN 라이브 관측 불가(정적 렌더만)")
      fi
      nsjson="$(printf '%s\n' "${NSL[@]}" | jq -R . | jq -cs .)"
      live_json="$(jq -cn \
        --argjson req "$req" --argjson ns "$nsjson" --argjson ds "$dsc" \
        --argjson smp "$SAMPLES" --argjson iv "$INTERVAL" \
        --arg uc "$used_cpu" --arg um "$used_mem" '
        { namespaces: $ns,
          pods: $req.pods,
          daemonsets: $ds,
          containers: $req.containers,
          cpuRequestMilli: $req.cpuRequestMilli,
          memRequestMi: $req.memRequestMi,
          cpuUsedMilliMedian: (if $uc=="null" or $uc=="" then null else ($uc|tonumber) end),
          memUsedMiMedian:    (if $um=="null" or $um=="" then null else ($um|tonumber) end),
          samples: $smp,
          intervalSeconds: $iv }')"
    fi
  fi

  TARGETS="$(jq -c --arg n "$name" --argjson s "$static_json" --argjson l "$live_json" --argjson cv "$chart_version" \
    '.[$n] = {static: $s, live: $l, chartVersion: $cv}' <<<"$TARGETS")"
done

if want_static; then
  LIMITS+=("정적 표는 차트 설치분만 센다 — kcloud(NPUClusterPolicy·DriverInstallPolicy)·NVIDIA(ClusterPolicy)·RBLN 모두 정책 CR 로 런타임에 DaemonSet 을 만들므로 DaemonSet 열은 설치 시점 값이며 라이브 열과 다르다")
  LIMITS+=("kcloud 차트는 기본값으로 렌더 — webhook/metrics Service 는 기본 비활성이라 각각 0, 활성 시 +1/+1")
  LIMITS+=("벤더 차트(nvidia-gpu-operator, rbln-npu-operator)는 클러스터 미설치 — 정적 렌더 전용")
fi

[[ "$DRY_RUN" == 1 ]] && exit 0

COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
if [[ "${#LIMITS[@]}" -gt 0 ]]; then
  limits_json="$(printf '%s\n' "${LIMITS[@]}" | jq -R . | jq -cs .)"
else
  limits_json='[]'
fi

jq -n \
  --arg date "$(date +%F)" \
  --arg commit "$COMMIT" \
  --arg mode "$MODE" \
  --argjson targets "$TARGETS" \
  --argjson limits "$limits_json" \
  '{date: $date, commit: $commit, mode: $mode, targets: $targets, limits: $limits}' >"$OUT"

echo "wrote $OUT" >&2
