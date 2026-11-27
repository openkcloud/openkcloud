#!/usr/bin/env bash
# ============================================================
# adopt-helm-resources.sh: 기존 helm hook 객체를 릴리스 자원으로 인계한다(일회성)
# 상세: hook 객체에 helm 소유 표시(라벨 하나·주석 두 개)를 붙여 다음 업그레이드가 인계하게 한다
#       (기본은 미리보기, --apply 로 적용, 삭제는 하지 않는다). helm 이 인계한 정책의 spec 을 새 값으로
#       바꾸지 않을 수 있어(Helm 3), 업그레이드 뒤 manifest 의 DIP 로 kubectl replace 해야 한다.
# 생성일: 2026-09-24 | 수정일: 2026-10-15
# ============================================================
set -euo pipefail
APPLY=0
RELEASE="kcloud-operator"
NAMESPACE="kcloud"
KUBECTL_CONTEXT="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"
while [ $# -gt 0 ]; do
  case "$1" in
    --apply) APPLY=1; shift ;;
    --release) RELEASE="${2:?}"; shift 2 ;;
    --namespace) NAMESPACE="${2:?}"; shift 2 ;;
    *) echo "알 수 없는 인자: $1" >&2; exit 2 ;;
  esac
done
kc() { kubectl --context "$KUBECTL_CONTEXT" "$@"; }

names="$(kc get driverinstallpolicies.npu.ai -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}')"
[ -n "$names" ] || { echo "인계할 DriverInstallPolicy 가 없다"; exit 0; }

while IFS= read -r n; do
  [ -n "$n" ] || continue
  cur="$(kc get driverinstallpolicy "$n" -o jsonpath='{.metadata.annotations.meta\.helm\.sh/release-name}')"
  if [ "$cur" = "$RELEASE" ]; then
    echo "이미 인계됨: $n"
    continue
  fi
  echo "인계 대상: $n"
  if [ "$APPLY" = 1 ]; then
    kc label driverinstallpolicy "$n" app.kubernetes.io/managed-by=Helm --overwrite
    kc annotate driverinstallpolicy "$n" \
      meta.helm.sh/release-name="$RELEASE" \
      meta.helm.sh/release-namespace="$NAMESPACE" --overwrite
  fi
done <<< "$names"

if [ "$APPLY" = 1 ]; then
  echo "인계 완료. 이제 helm upgrade 를 --reset-then-reuse-values 로 실행한다"
else
  echo "미리보기였다. 실제로 붙이려면 --apply 를 준다"
fi
echo "주의: helm 이 인계한 정책의 spec 을 새 값으로 바꾸지 않을 수 있다(Helm 3). 업그레이드 뒤"
echo "      helm get manifest 의 DriverInstallPolicy 문서로 kubectl replace -f 해 spec 을 맞추거나,"
echo "      인계 대신 차트 정책을 이름으로 지우고 업그레이드한다(deploy/helm/README.md)"
