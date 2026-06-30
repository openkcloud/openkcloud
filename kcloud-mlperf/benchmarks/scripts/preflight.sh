#!/bin/bash
# ============================================================================
# preflight.sh - Pre-flight Checks for K-Cloud MLPerf Benchmarks
# ============================================================================
# Run this script before running benchmarks to verify the cluster is ready.
# It checks all prerequisites and can auto-fix common issues.
#
# Usage:
#   ./scripts/preflight.sh [--fix]
#
# Options:
#   --fix    Attempt to automatically fix any issues found
# ============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Track issues
ISSUES=0
WARNINGS=0
AUTO_FIX="${1:-}"

log() { echo -e "${BLUE}[CHECK]${NC} $1"; }
pass() { echo -e "  ${GREEN}✓${NC} $1"; }
fail() { echo -e "  ${RED}✗${NC} $1"; ((ISSUES++)); }
warn() { echo -e "  ${YELLOW}⚠${NC} $1"; ((WARNINGS++)); }
fix() { echo -e "  ${BLUE}→${NC} $1"; }

# Load configuration
CONFIG_FILE="$PROJECT_ROOT/config/cluster.env"
if [ -f "$PROJECT_ROOT/config/cluster.env.local" ]; then
    CONFIG_FILE="$PROJECT_ROOT/config/cluster.env.local"
fi
[ -f "$CONFIG_FILE" ] && source "$CONFIG_FILE"

NAMESPACE="${BENCHMARK_NAMESPACE:-mlperf}"

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║     K-Cloud MLPerf - Pre-flight Checks                           ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""

# ============================================================================
# Check 1: kubectl connectivity
# ============================================================================
check_kubectl() {
    log "Kubernetes Connectivity"
    
    if ! command -v kubectl &>/dev/null; then
        fail "kubectl not installed"
        return
    fi
    pass "kubectl installed"
    
    if ! kubectl cluster-info &>/dev/null 2>&1; then
        fail "Cannot connect to Kubernetes cluster"
        echo "    Possible causes:"
        echo "    - Cluster not running"
        echo "    - Wrong IP in ~/.kube/config"
        echo "    - API server not responding"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            fix "Checking if master IP changed..."
            CURRENT_IP=$(hostname -I | awk '{print $1}')
            KUBECONFIG_IP=$(grep "server:" ~/.kube/config 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)
            
            if [ "$CURRENT_IP" != "$KUBECONFIG_IP" ]; then
                fix "Master IP changed from $KUBECONFIG_IP to $CURRENT_IP"
                fix "Updating kubeconfig..."
                sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" ~/.kube/config
                
                # Also update Kubernetes configs
                if [ -d /etc/kubernetes/manifests ]; then
                    fix "Updating /etc/kubernetes/ configs..."
                    sudo sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" /etc/kubernetes/manifests/*.yaml 2>/dev/null || true
                    sudo sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" /etc/kubernetes/*.conf 2>/dev/null || true
                    
                    fix "Regenerating API server certificate..."
                    sudo kubeadm certs renew apiserver --config /etc/kubernetes/kubeadm-config.yaml 2>/dev/null || true
                    
                    fix "Restarting kubelet..."
                    sudo systemctl restart kubelet
                    sleep 10
                fi
            fi
        fi
        return
    fi
    pass "Cluster reachable"
    
    # Check API server health
    if kubectl get --raw='/healthz' &>/dev/null 2>&1; then
        pass "API server healthy"
    else
        fail "API server not healthy"
    fi
}

# ============================================================================
# Check 2: Nodes
# ============================================================================
check_nodes() {
    log "Kubernetes Nodes"
    
    NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l)
    if [ "$NODE_COUNT" -eq 0 ]; then
        fail "No nodes found"
        return
    fi
    pass "$NODE_COUNT node(s) found"
    
    # Check for ready nodes
    READY_NODES=$(kubectl get nodes --no-headers 2>/dev/null | grep -c " Ready " || echo 0)
    if [ "$READY_NODES" -eq "$NODE_COUNT" ]; then
        pass "All nodes Ready"
    else
        fail "Some nodes not Ready"
        kubectl get nodes
    fi
    
    # Check for GPU nodes
    GPU_NODES=$(kubectl get nodes -l nvidia.com/gpu.present=true --no-headers 2>/dev/null | wc -l)
    if [ "$GPU_NODES" -eq 0 ]; then
        warn "No GPU nodes labeled (nvidia.com/gpu.present=true)"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            # Try to find nodes with GPU and label them
            for node in $(kubectl get nodes --no-headers | awk '{print $1}'); do
                GPU_COUNT=$(kubectl get node $node -o jsonpath='{.status.capacity.nvidia\.com/gpu}' 2>/dev/null)
                if [ -n "$GPU_COUNT" ] && [ "$GPU_COUNT" -gt 0 ]; then
                    fix "Labeling node $node with nvidia.com/gpu.present=true"
                    kubectl label node $node nvidia.com/gpu.present=true --overwrite
                fi
            done
        fi
    else
        pass "$GPU_NODES GPU node(s) labeled"
    fi
}

# ============================================================================
# Check 3: NVIDIA RuntimeClass
# ============================================================================
check_runtime_class() {
    log "NVIDIA RuntimeClass"
    
    if kubectl get runtimeclass nvidia &>/dev/null 2>&1; then
        pass "RuntimeClass 'nvidia' exists"
    else
        fail "RuntimeClass 'nvidia' not found"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            fix "Creating NVIDIA RuntimeClass..."
            cat <<EOF | kubectl apply -f -
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nvidia
handler: nvidia
EOF
        fi
    fi
}

# ============================================================================
# Check 4: NVIDIA Device Plugin
# ============================================================================
check_device_plugin() {
    log "NVIDIA Device Plugin"
    
    if kubectl get pods -n kube-system -l name=nvidia-device-plugin-ds --no-headers 2>/dev/null | grep -q Running; then
        pass "NVIDIA Device Plugin running"
    else
        warn "NVIDIA Device Plugin not running"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            fix "Installing NVIDIA Device Plugin..."
            kubectl apply -f https://raw.githubusercontent.com/NVIDIA/k8s-device-plugin/v0.14.0/nvidia-device-plugin.yml
            sleep 10
        fi
    fi
    
    # Check if GPUs are allocatable
    TOTAL_GPUS=$(kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}' 2>/dev/null | tr -s ' ' '\n' | grep -v '^$' | paste -sd+ | bc 2>/dev/null || echo "0")
    if [ -n "$TOTAL_GPUS" ] && [ "$TOTAL_GPUS" != "0" ]; then
        pass "$TOTAL_GPUS GPU(s) allocatable"
    else
        warn "No GPUs allocatable in cluster (device plugin may still be starting)"
    fi
}

# ============================================================================
# Check 5: Namespace
# ============================================================================
check_namespace() {
    log "Benchmark Namespace ($NAMESPACE)"
    
    if kubectl get namespace $NAMESPACE &>/dev/null 2>&1; then
        pass "Namespace '$NAMESPACE' exists"
    else
        warn "Namespace '$NAMESPACE' not found"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            fix "Creating namespace $NAMESPACE..."
            kubectl create namespace $NAMESPACE
        fi
    fi
}

# ============================================================================
# Check 6: HuggingFace Token Secret
# ============================================================================
check_hf_secret() {
    log "HuggingFace Token Secret"
    
    if kubectl get secret hf-token -n $NAMESPACE &>/dev/null 2>&1; then
        pass "HuggingFace token secret exists"
    else
        fail "HuggingFace token secret not found"
        echo "    Required for accessing Llama 3.1 model"
        
        if [ "$AUTO_FIX" = "--fix" ]; then
            if [ -n "$HF_TOKEN" ]; then
                fix "Creating HuggingFace token secret..."
                kubectl create namespace $NAMESPACE 2>/dev/null || true
                kubectl create secret generic hf-token -n $NAMESPACE --from-literal=HF_TOKEN="$HF_TOKEN"
            else
                echo "    Set HF_TOKEN in config/cluster.env.local or run:"
                echo "    kubectl create secret generic hf-token -n $NAMESPACE --from-literal=HF_TOKEN=<your-token>"
            fi
        fi
    fi
}

# ============================================================================
# Check 7: Worker data directories
# ============================================================================
check_data_dirs() {
    log "Worker Node Data Directories"
    
    # Check if we can create a test pod to verify
    TEST_POD_NAME="preflight-check-$(date +%s)"
    
    cat <<EOF | kubectl apply -f - >/dev/null 2>&1
apiVersion: v1
kind: Pod
metadata:
  name: $TEST_POD_NAME
  namespace: $NAMESPACE
spec:
  restartPolicy: Never
  nodeSelector:
    nvidia.com/gpu.present: "true"
  containers:
  - name: check
    image: alpine
    command: ["sh", "-c", "test -d /data && echo 'OK' || echo 'MISSING'"]
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    hostPath:
      path: /data
      type: DirectoryOrCreate
EOF
    
    sleep 5
    RESULT=$(kubectl logs $TEST_POD_NAME -n $NAMESPACE 2>/dev/null || echo "FAILED")
    kubectl delete pod $TEST_POD_NAME -n $NAMESPACE --force --grace-period=0 &>/dev/null 2>&1 || true
    
    if [ "$RESULT" = "OK" ]; then
        pass "Data directory /data accessible on GPU node"
    else
        warn "Could not verify data directory on GPU node"
    fi
}

# ============================================================================
# Check 8: Network connectivity
# ============================================================================
check_network() {
    log "Network Connectivity"
    
    # Check DNS
    if kubectl run dns-test --image=alpine --restart=Never --rm -it -- nslookup kubernetes.default &>/dev/null 2>&1; then
        pass "Cluster DNS working"
    else
        warn "Cluster DNS might have issues"
    fi
}

# ============================================================================
# Summary
# ============================================================================
print_summary() {
    echo ""
    echo "════════════════════════════════════════════════════════════════════"
    echo " Pre-flight Check Summary"
    echo "════════════════════════════════════════════════════════════════════"
    
    if [ "$ISSUES" -eq 0 ] && [ "$WARNINGS" -eq 0 ]; then
        echo -e " ${GREEN}All checks passed!${NC} Cluster is ready for benchmarking."
    elif [ "$ISSUES" -eq 0 ]; then
        echo -e " ${YELLOW}$WARNINGS warning(s)${NC} - Cluster may work but review warnings above."
    else
        echo -e " ${RED}$ISSUES issue(s)${NC} and ${YELLOW}$WARNINGS warning(s)${NC} found."
        echo ""
        echo " Run with --fix to attempt automatic fixes:"
        echo "   $0 --fix"
    fi
    
    echo ""
    echo " Quick commands:"
    echo "   kubectl get nodes -o wide"
    echo "   kubectl get pods -n $NAMESPACE"
    echo "   ./scripts/run_benchmarks.sh --smoke --mlperf"
    echo ""
}

# ============================================================================
# Main
# ============================================================================
main() {
    check_kubectl
    
    # Only continue if kubectl works
    if kubectl cluster-info &>/dev/null 2>&1; then
        check_nodes
        check_runtime_class
        check_device_plugin
        check_namespace
        check_hf_secret
        # Skip these as they can be slow
        # check_data_dirs
        # check_network
    fi
    
    print_summary
    
    # Exit with error if issues found
    [ "$ISSUES" -eq 0 ]
}

main "$@"
