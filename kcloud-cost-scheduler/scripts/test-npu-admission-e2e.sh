#!/usr/bin/env bash

# Verifies the real API Server admission path:
# Kueue Workload -> workload webhook -> Pod webhook -> sched.ai annotations.
#
# Prerequisites:
#   - a reachable Kubernetes cluster with cert-manager and Kueue installed
#   - the manager image available to the cluster
#
# Optional local Kind flow:
#   BUILD_LOCAL_IMAGE=true KIND_CLUSTER=<cluster> ./scripts/test-npu-admission-e2e.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-k8s-workload-operator-system}"
TEST_NAMESPACE="${TEST_NAMESPACE:-kcloud-npu-admission-e2e}"
MANAGER_IMAGE="${MANAGER_IMAGE:-kcloud-cost-scheduler:webhook-e2e}"
BUILD_LOCAL_IMAGE="${BUILD_LOCAL_IMAGE:-false}"
KIND_CLUSTER="${KIND_CLUSTER:-}"
KEEP_TEST_NAMESPACE="${KEEP_TEST_NAMESPACE:-false}"

require_command() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "required command not found: $1" >&2
        exit 1
    fi
}

assert_equal() {
    local label="$1"
    local expected="$2"
    local actual="$3"
    if [[ "${actual}" != "${expected}" ]]; then
        echo "FAIL: ${label}: expected '${expected}', got '${actual}'" >&2
        return 1
    fi
    echo "PASS: ${label}=${actual}"
}

annotation_value() {
    local resource="$1"
    local name="$2"
    local key="$3"
    kubectl -n "${TEST_NAMESPACE}" get "${resource}" "${name}" \
        -o "go-template={{ index .metadata.annotations \"${key}\" }}"
}

print_failure_diagnostics() {
    echo "Admission E2E failed; preserving namespace ${TEST_NAMESPACE}." >&2
    kubectl -n "${OPERATOR_NAMESPACE}" get deploy,pod,service,certificate 2>/dev/null || true
    kubectl -n "${OPERATOR_NAMESPACE}" logs \
        deployment/k8s-workload-operator-controller-manager --tail=200 2>/dev/null || true
}

cleanup_success() {
    if [[ "${KEEP_TEST_NAMESPACE}" == "true" ]]; then
        echo "Keeping test namespace ${TEST_NAMESPACE}."
        return
    fi
    kubectl delete namespace "${TEST_NAMESPACE}" --ignore-not-found=true --wait=false >/dev/null
}

trap print_failure_diagnostics ERR

require_command kubectl
kubectl cluster-info >/dev/null

if ! kubectl get crd certificates.cert-manager.io >/dev/null 2>&1; then
    echo "cert-manager Certificate API is not installed." >&2
    exit 1
fi
if ! kubectl get crd workloads.kueue.x-k8s.io >/dev/null 2>&1; then
    echo "Kueue Workload API is not installed." >&2
    exit 1
fi
if ! kubectl get --raw /apis/kueue.x-k8s.io/v1beta1 >/dev/null 2>&1; then
    echo "Kueue v1beta1 is not served; update the webhook rule and Pod lookup GVK first." >&2
    exit 1
fi

if [[ "${BUILD_LOCAL_IMAGE}" == "true" ]]; then
    require_command docker
    require_command kind
    if [[ -z "${KIND_CLUSTER}" ]]; then
        echo "KIND_CLUSTER is required when BUILD_LOCAL_IMAGE=true." >&2
        exit 1
    fi
    docker build -t "${MANAGER_IMAGE}" "${REPO_ROOT}"
    kind load docker-image "${MANAGER_IMAGE}" --name "${KIND_CLUSTER}"
fi

kubectl apply -k "${REPO_ROOT}/config/default"
kubectl -n "${OPERATOR_NAMESPACE}" set image \
    deployment/k8s-workload-operator-controller-manager \
    manager="${MANAGER_IMAGE}"

kubectl -n "${OPERATOR_NAMESPACE}" wait --for=condition=Ready \
    certificate/k8s-workload-operator-serving-cert --timeout=180s
kubectl -n "${OPERATOR_NAMESPACE}" rollout status \
    deployment/k8s-workload-operator-controller-manager --timeout=180s

kubectl create namespace "${TEST_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f - <<EOF
apiVersion: kueue.x-k8s.io/v1beta1
kind: Workload
metadata:
  name: npu-annotation-e2e
  namespace: ${TEST_NAMESPACE}
  annotations:
    sched.ai/predicted-power-npu: "60"
    sched.ai/predicted-cost-npu: "0.0072"
    sched.ai/prediction-provider: "e2e-fixture"
    sched.ai/prediction-model-version: "fixture-v1"
spec:
  active: false
  podSets:
  - name: main
    count: 1
    template:
      spec:
        restartPolicy: Never
        containers:
        - name: npu
          image: registry.k8s.io/pause:3.10
          resources:
            requests:
              cpu: "1"
              furiosa.ai/warboy: "1"
EOF

assert_equal "Workload optimized" "true" \
    "$(annotation_value workload npu-annotation-e2e kcloud.io/optimized)"
assert_equal "Workload accelerator type" "npu" \
    "$(annotation_value workload npu-annotation-e2e sched.ai/acc-type)"
assert_equal "Workload NPU count" "1" \
    "$(annotation_value workload npu-annotation-e2e sched.ai/npu-count)"
assert_equal "Workload NPU vendor" "furiosa" \
    "$(annotation_value workload npu-annotation-e2e sched.ai/acc-vendor)"

kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: npu-annotation-e2e-pod
  namespace: ${TEST_NAMESPACE}
  annotations:
    kueue.x-k8s.io/workload: npu-annotation-e2e
spec:
  restartPolicy: Never
  containers:
  - name: npu
    image: registry.k8s.io/pause:3.10
    resources:
      requests:
        cpu: "1"
        furiosa.ai/warboy: "1"
EOF

assert_equal "Pod scheduler" "keti-cost-scheduler" \
    "$(kubectl -n "${TEST_NAMESPACE}" get pod npu-annotation-e2e-pod -o jsonpath='{.spec.schedulerName}')"
assert_equal "Pod accelerator type" "npu" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/acc-type)"
assert_equal "Pod NPU count" "1" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/npu-count)"
assert_equal "Pod NPU vendor" "furiosa" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/acc-vendor)"
assert_equal "Workload-to-Pod predicted power" "60" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/predicted-power-npu)"
assert_equal "Workload-to-Pod predicted cost" "0.0072" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/predicted-cost-npu)"
assert_equal "Prediction provider" "e2e-fixture" \
    "$(annotation_value pod npu-annotation-e2e-pod sched.ai/prediction-provider)"

trap - ERR
cleanup_success
echo "NPU Admission E2E PASS"
