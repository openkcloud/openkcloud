#!/usr/bin/env bash
# ============================================================
# local-image-gc_test.sh: initContainer·kernelImages 참조 조회·보호를 확인(가짜 kubectl/docker)
# 상세: 스크립트가 필수로 요구하는 DEV_REGISTRY 는 가짜 docker 의 저장소 접두와 같은 값으로 넘긴다.
# 생성일: 2026-09-23 | 수정일: 2026-10-13
# ============================================================
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
kubectl_log="$tmp/kubectl.log"

cat > "$tmp/docker" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  "images --format {{.Repository}}")
    printf '%s\n' "registry.test:5100/kcloud/test-init" "registry.test:5100/kcloud/test-ki"
    ;;
  "images --format"*"/test-init")
    printf '2026-09-22 10:00:00 +0900 KST\tregistry.test:5100/kcloud/test-init:new\n'
    printf '2026-09-20 10:00:00 +0900 KST\tregistry.test:5100/kcloud/test-init:old\n'
    ;;
  "images --format"*"/test-ki")
    printf '2026-09-22 10:00:00 +0900 KST\tregistry.test:5100/kcloud/test-ki:new\n'
    printf '2026-09-20 10:00:00 +0900 KST\tregistry.test:5100/kcloud/test-ki:kernel-only\n'
    ;;
  *) echo "";;
esac
EOF
chmod +x "$tmp/docker"

cat > "$tmp/kubectl" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "$kubectl_log"
case "\$*" in
  *"get pods -A"*) echo "registry.test:5100/kcloud/test-init:old";;
  *driverinstallpolicies*)
    # 새 jsonpath 가 kernelImages 를 읽을 때만 그 값을 돌려준다. spec.driver.image 만 읽던
    # 옛 쿼리로는 이 참조가 안 잡혀 kernel-only 가 삭제 후보가 되므로 아래 (2)가 RED 다.
    case "\$*" in
      *kernelImages*) echo "registry.test:5100/kcloud/test-ki:kernel-only";;
    esac
    ;;
  *) echo "";;
esac
EOF
chmod +x "$tmp/kubectl"

# DOCKER=docker: sudo -n docker 는 secure_path 로 PATH 를 리셋해 가짜 docker 를 우회하므로,
# plain docker 를 강제해 이 디렉터리의 가짜 스텁이 확실히 잡히게 한다. 가짜 kubectl 은
# jsonpath 를 실행하지 않고 호출 인자만 보므로, 조회가 initContainers·kernelImages 를 실제로
# 요청하는지는 로그(kubectl_log)에 남은 호출 인자 문자열로 따로 확인한다.
# --keep 1: 저장소별 2개 태그 중 최신 1개만 keep-count 로 보존되므로, old·kernel-only 는
# 라이브 참조가 없으면 삭제 후보가 되어야 한다.
out="$(DEV_REGISTRY=registry.test:5100 DOCKER=docker PATH="$tmp:$PATH" bash "$here/local-image-gc.sh" --keep 1 --dry-run)"
candidates="$(echo "$out" | awk '/^삭제 후보/{f=1} f')"

# (1) initContainer 전용 참조 보호.
grep -q 'initContainers' "$kubectl_log" || {
  echo "FAIL: kubectl pods 조회 jsonpath 가 initContainers 를 포함하지 않음"
  cat "$kubectl_log"
  exit 1
}
if echo "$candidates" | grep -qF 'test-init:old'; then
  echo "FAIL: initContainer 전용 참조가 삭제 후보에 포함됨"
  echo "$out"
  exit 1
fi
if ! echo "$out" | grep -qF 'test-init:old'; then
  echo "FAIL: initContainer 전용 참조가 보존 목록에 없음(라이브 참조로 인식되지 않음)"
  echo "$out"
  exit 1
fi

# (2) kernelImages 전용 참조 보호. DriverInstallPolicy 조회 jsonpath 가 kernelImages 값을
#     읽어야 하고, kernelImages 로만 참조되는 태그가 삭제 후보에 없어야 한다.
grep -q 'kernelImages' "$kubectl_log" || {
  echo "FAIL: DriverInstallPolicy 조회 jsonpath 가 kernelImages 를 포함하지 않음"
  cat "$kubectl_log"
  exit 1
}
if echo "$candidates" | grep -qF 'test-ki:kernel-only'; then
  echo "FAIL: kernelImages 전용 참조가 삭제 후보에 포함됨"
  echo "$out"
  exit 1
fi
if ! echo "$out" | grep -qF 'test-ki:kernel-only'; then
  echo "FAIL: kernelImages 전용 참조가 보존 목록에 없음(라이브 참조로 인식되지 않음)"
  echo "$out"
  exit 1
fi

echo "PASS: initContainers·kernelImages 참조 보호 확인"
