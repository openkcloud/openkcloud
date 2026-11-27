#!/usr/bin/env bash
# ============================================================
# open-console.sh: 관리 콘솔(/ui/)을 토큰이 적용된 상태로 연다
# 상세: SA 토큰을 새로 발급해 주소의 fragment(#token=...)로 붙인다. fragment 는 브라우저가
#       서버로 보내지 않아 operator 접근 로그·프록시·Referer 에 토큰 원문이 남지 않는다.
#       콘솔은 그 값을 읽은 즉시 주소창에서 지우고 sessionStorage 로 옮긴다.
#       사용: NS=kcloud SA=kcloud-operator-console ./hack/open-console.sh [URL]
# 생성일: 2026-09-03
# ============================================================
set -euo pipefail

NS="${NS:-kcloud}"
SA="${SA:-kcloud-operator-console}"
TTL="${TTL:-8h}"
URL="${1:-${CONSOLE_URL:-}}"

if [[ -z "$URL" ]]; then
  # NodePort 로 열려 있으면 그 주소를 조립한다. ClusterIP 면 사용자가 URL 을 넘겨야 한다.
  port=$(kubectl -n "$NS" get svc -l app.kubernetes.io/name -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.spec.type}{" "}{.spec.ports[0].nodePort}{"\n"}{end}' 2>/dev/null \
         | awk '$1 ~ /-api$/ && $2 == "NodePort" {print $3; exit}')
  node=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
  if [[ -z "$port" || -z "$node" ]]; then
    echo "콘솔 주소를 찾지 못했다. URL 을 인자로 넘겨라: $0 https://<host>:<port>/ui/" >&2
    exit 1
  fi
  URL="https://${node}:${port}/ui/"
fi

token=$(kubectl -n "$NS" create token "$SA" --duration="$TTL")
full="${URL%/}/#token=${token}"

echo "$full"
# 브라우저가 있으면 띄우고, 없으면 위 주소를 사람이 복사해 쓴다(헤드리스 서버).
if command -v xdg-open >/dev/null 2>&1 && [[ -n "${DISPLAY:-}" ]]; then
  xdg-open "$full" >/dev/null 2>&1 &
fi
