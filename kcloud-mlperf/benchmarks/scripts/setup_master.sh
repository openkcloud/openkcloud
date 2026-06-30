#!/bin/bash
# ============================================================================
# setup_master.sh - Master Node Setup for K-Cloud MLPerf Benchmarks
# ============================================================================
# Run this script on the master/control-plane node to set up Kubernetes.
#
# Usage:
#   ./scripts/setup_master.sh [--config config/cluster.env]
#
# Prerequisites:
#   - Ubuntu 20.04/22.04
#   - Root or sudo access
#   - Network connectivity to worker nodes
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

log() { echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"; }
success() { echo -e "${GREEN}✓${NC} $1"; }
warn() { echo -e "${YELLOW}⚠${NC} $1"; }
error() { echo -e "${RED}✗${NC} $1"; exit 1; }

# Load configuration
CONFIG_FILE="${1:-$PROJECT_ROOT/config/cluster.env}"
if [ -f "$PROJECT_ROOT/config/cluster.env.local" ]; then
    CONFIG_FILE="$PROJECT_ROOT/config/cluster.env.local"
fi

if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
    log "Loaded config from $CONFIG_FILE"
else
    warn "No config file found. Using defaults."
    MASTER_IP=$(hostname -I | awk '{print $1}')
    POD_NETWORK_CIDR="10.244.0.0/16"
    K8S_VERSION="1.28"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║     K-Cloud MLPerf - Master Node Setup                           ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "  Master IP: $MASTER_IP"
echo "  K8s Version: $K8S_VERSION"
echo "  Pod Network: $POD_NETWORK_CIDR"
echo ""

# ============================================================================
# Step 1: System Prerequisites
# ============================================================================
install_prerequisites() {
    log "[1/8] Installing prerequisites..."
    
    # Disable swap
    sudo swapoff -a
    sudo sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab
    
    # Load required modules
    cat <<EOF | sudo tee /etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF
    sudo modprobe overlay
    sudo modprobe br_netfilter
    
    # Sysctl settings
    cat <<EOF | sudo tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF
    sudo sysctl --system >/dev/null 2>&1
    
    success "Prerequisites configured"
}

# ============================================================================
# Step 2: Install containerd
# ============================================================================
install_containerd() {
    log "[2/8] Installing containerd..."
    
    if command -v containerd &>/dev/null; then
        success "containerd already installed"
        return
    fi
    
    sudo apt-get update
    sudo apt-get install -y containerd
    
    # Configure containerd
    sudo mkdir -p /etc/containerd
    containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
    sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
    
    sudo systemctl restart containerd
    sudo systemctl enable containerd
    
    success "containerd installed and configured"
}

# ============================================================================
# Step 3: Install kubeadm, kubelet, kubectl
# ============================================================================
install_kubernetes() {
    log "[3/8] Installing Kubernetes components..."
    
    if command -v kubeadm &>/dev/null; then
        success "Kubernetes already installed: $(kubeadm version -o short)"
        return
    fi
    
    sudo apt-get update
    sudo apt-get install -y apt-transport-https ca-certificates curl gpg
    
    # Add Kubernetes repository
    curl -fsSL https://pkgs.k8s.io/core:/stable:/v${K8S_VERSION}/deb/Release.key | \
        sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
    
    echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v${K8S_VERSION}/deb/ /" | \
        sudo tee /etc/apt/sources.list.d/kubernetes.list
    
    sudo apt-get update
    sudo apt-get install -y kubelet kubeadm kubectl
    sudo apt-mark hold kubelet kubeadm kubectl
    
    success "Kubernetes components installed"
}

# ============================================================================
# Step 3.5: Clean up incomplete kubeadm state
# ============================================================================
cleanup_incomplete_state() {
    # Check if cluster is fully initialized
    if [ -f /etc/kubernetes/admin.conf ]; then
        # Cluster is initialized, check if it's working
        if kubectl cluster-info &>/dev/null 2>&1; then
            return 0  # Cluster is working fine
        fi
    fi
    
    # Check for partial state (pki directory exists but admin.conf doesn't)
    # OR check for external CA config issues
    HAS_PARTIAL_STATE=false
    if [ -d /etc/kubernetes/pki ] && [ ! -f /etc/kubernetes/admin.conf ]; then
        HAS_PARTIAL_STATE=true
    fi
    
    # Check if kubeadm-config.yaml references external CA but files are missing
    if [ -f /etc/kubernetes/kubeadm-config.yaml ] && [ ! -f /etc/kubernetes/admin.conf ]; then
        if grep -q "externalCA" /etc/kubernetes/kubeadm-config.yaml 2>/dev/null; then
            HAS_PARTIAL_STATE=true
        fi
    fi
    
    if [ "$HAS_PARTIAL_STATE" = true ]; then
        warn "Found incomplete kubeadm state (partial certificates/config without admin.conf)"
        warn "This usually happens when a previous 'kubeadm init' failed partway through"
        log "Automatically cleaning up incomplete state..."
        
        # Reset kubeadm state
        sudo kubeadm reset --force 2>/dev/null || true
        
        # Additional cleanup
        sudo rm -rf /etc/kubernetes/pki
        sudo rm -rf /etc/kubernetes/manifests
        sudo rm -f /etc/kubernetes/*.conf
        sudo rm -f /etc/kubernetes/kubeadm-config.yaml
        sudo rm -rf /var/lib/etcd
        sudo rm -rf ~/.kube/config
        
        # Clean up iptables rules
        sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F && sudo iptables -X || true
        
        success "kubeadm state cleaned up"
    fi
    
    # Check for kubelet service issues
    if systemctl is-active --quiet kubelet 2>/dev/null; then
        # Stop kubelet if it's running but cluster isn't initialized
        if [ ! -f /etc/kubernetes/admin.conf ]; then
            log "Stopping kubelet service..."
            sudo systemctl stop kubelet || true
        fi
    fi
}

# ============================================================================
# Step 4: Initialize Kubernetes cluster
# ============================================================================
init_cluster() {
    log "[4/8] Initializing Kubernetes cluster..."
    
    # Check if already initialized and working
    if [ -f /etc/kubernetes/admin.conf ]; then
        if kubectl cluster-info &>/dev/null 2>&1; then
            warn "Cluster already initialized and working. Skipping."
            return
        else
            warn "Cluster appears initialized but not responding. You may need to reset."
        fi
    fi
    
    # Clean up any incomplete state before initializing
    cleanup_incomplete_state
    
    # Create kubeadm config
    cat > /tmp/kubeadm-config.yaml <<EOF
apiVersion: kubeadm.k8s.io/v1beta3
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: ${MASTER_IP}
  bindPort: 6443
---
apiVersion: kubeadm.k8s.io/v1beta3
kind: ClusterConfiguration
kubernetesVersion: v${K8S_VERSION}.0
networking:
  podSubnet: ${POD_NETWORK_CIDR}
controlPlaneEndpoint: "${MASTER_IP}:6443"
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
EOF
    
    # Save config for future reference
    sudo mkdir -p /etc/kubernetes
    sudo cp /tmp/kubeadm-config.yaml /etc/kubernetes/kubeadm-config.yaml
    
    # Initialize cluster
    log "Running kubeadm init (this may take a few minutes)..."
    sudo kubeadm init --config=/tmp/kubeadm-config.yaml
    
    # Setup kubeconfig for current user
    mkdir -p $HOME/.kube
    sudo cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
    sudo chown $(id -u):$(id -g) $HOME/.kube/config
    
    success "Kubernetes cluster initialized"
}

# ============================================================================
# Step 5: Install CNI (Flannel)
# ============================================================================
install_cni() {
    log "[5/8] Installing Flannel CNI..."
    
    # Remove any conflicting CNI configurations (e.g., Calico)
    if [ -f /etc/cni/net.d/10-calico.conflist ]; then
        log "Removing conflicting Calico CNI configuration..."
        sudo rm -f /etc/cni/net.d/10-calico.conflist /etc/cni/net.d/calico-kubeconfig
    fi
    
    if kubectl get pods -n kube-flannel 2>/dev/null | grep -q Running; then
        success "Flannel already installed"
        # Restart kubelet to pick up CNI changes if we removed Calico
        if [ ! -f /etc/cni/net.d/10-calico.conflist ] && [ -f /etc/cni/net.d/10-flannel.conflist ]; then
            log "Restarting kubelet to apply CNI changes..."
            sudo systemctl restart kubelet || true
            sleep 5
        fi
        return
    fi
    
    kubectl apply -f https://github.com/flannel-io/flannel/releases/latest/download/kube-flannel.yml
    
    log "Waiting for Flannel to be ready..."
    kubectl wait --for=condition=ready pod -l app=flannel -n kube-flannel --timeout=120s || true
    
    # Restart kubelet to ensure it picks up Flannel CNI config
    log "Restarting kubelet to apply Flannel CNI..."
    sudo systemctl restart kubelet || true
    sleep 5
    
    success "Flannel CNI installed"
}

# ============================================================================
# Step 6: Create NVIDIA RuntimeClass
# ============================================================================
create_nvidia_runtime() {
    log "[6/8] Creating NVIDIA RuntimeClass..."
    
    if kubectl get runtimeclass nvidia 2>/dev/null; then
        success "NVIDIA RuntimeClass already exists"
        return
    fi
    
    cat <<EOF | kubectl apply -f -
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nvidia
handler: nvidia
EOF
    
    success "NVIDIA RuntimeClass created"
}

# ============================================================================
# Step 7: Install NVIDIA Device Plugin
# ============================================================================
install_nvidia_device_plugin() {
    log "[7/8] Installing NVIDIA Device Plugin..."
    
    # Check if device plugin is already running and GPUs are allocatable
    if kubectl get pods -n kube-system -l name=nvidia-device-plugin-ds --no-headers 2>/dev/null | grep -q Running; then
        TOTAL_GPUS=$(kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}' 2>/dev/null | tr -s ' ' '\n' | grep -v '^$' | wc -l)
        if [ -n "$TOTAL_GPUS" ] && [ "$TOTAL_GPUS" != "0" ]; then
            success "NVIDIA Device Plugin already running - $TOTAL_GPUS GPU(s) allocatable"
            return
        fi
    fi
    
    # Install NVIDIA Device Plugin with proper configuration for control-plane nodes
    log "Installing NVIDIA Device Plugin with control-plane tolerations..."
    kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-device-plugin-daemonset
  namespace: kube-system
spec:
  selector:
    matchLabels:
      name: nvidia-device-plugin-ds
  updateStrategy:
    type: RollingUpdate
  template:
    metadata:
      labels:
        name: nvidia-device-plugin-ds
    spec:
      tolerations:
      - key: node-role.kubernetes.io/control-plane
        operator: Exists
        effect: NoSchedule
      - key: nvidia.com/gpu
        operator: Exists
        effect: NoSchedule
      hostNetwork: true
      runtimeClassName: nvidia
      priorityClassName: system-node-critical
      containers:
      - image: nvcr.io/nvidia/k8s-device-plugin:v0.14.0
        name: nvidia-device-plugin-ctr
        env:
        - name: FAIL_ON_INIT_ERROR
          value: "false"
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop: ["ALL"]
        volumeMounts:
        - name: device-plugin
          mountPath: /var/lib/kubelet/device-plugins
        - name: nvidia-driver
          mountPath: /usr/local/nvidia
          readOnly: true
      volumes:
      - name: device-plugin
        hostPath:
          path: /var/lib/kubelet/device-plugins
      - name: nvidia-driver
        hostPath:
          path: /usr
          type: Directory
EOF
    
    log "Waiting for NVIDIA Device Plugin to be ready..."
    kubectl wait --for=condition=ready pod -n kube-system -l name=nvidia-device-plugin-ds --timeout=120s || true
    
    # Verify GPUs are allocatable
    sleep 10
    TOTAL_GPUS=$(kubectl get nodes -o jsonpath='{.items[*].status.allocatable.nvidia\.com/gpu}' 2>/dev/null | tr -s ' ' '\n' | grep -v '^$' | wc -l)
    if [ -n "$TOTAL_GPUS" ] && [ "$TOTAL_GPUS" != "0" ]; then
        success "NVIDIA Device Plugin installed - $TOTAL_GPUS GPU(s) allocatable"
    else
        warn "NVIDIA Device Plugin installed but GPUs not yet allocatable (may need a moment)"
    fi
}

# ============================================================================
# Step 8: Generate worker join command
# ============================================================================
generate_join_command() {
    log "[8/8] Generating worker join command..."
    
    JOIN_CMD=$(kubeadm token create --print-join-command)
    
    # Save join command
    echo "$JOIN_CMD" > "$PROJECT_ROOT/config/join-command.sh"
    chmod +x "$PROJECT_ROOT/config/join-command.sh"
    
    success "Join command saved to config/join-command.sh"
    echo ""
    echo "Run this on worker nodes:"
    echo "  sudo $JOIN_CMD"
}

# ============================================================================
# Main
# ============================================================================
main() {
    # Check if running as root or with sudo
    if [ "$EUID" -ne 0 ] && ! sudo -n true 2>/dev/null; then
        error "This script requires root/sudo access"
    fi
    
    install_prerequisites
    install_containerd
    install_kubernetes
    init_cluster
    install_cni
    create_nvidia_runtime
    install_nvidia_device_plugin
    generate_join_command
    
    echo ""
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║                    Master Node Setup Complete!                   ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo ""
    kubectl cluster-info
    echo ""
    kubectl get nodes
    echo ""
    echo "Next steps:"
    echo "  1. Run setup_worker.sh on each GPU worker node"
    echo "  2. Run preflight.sh to verify the cluster"
    echo "  3. Run run_benchmarks.sh to start benchmarking"
    echo ""
}

main "$@"
