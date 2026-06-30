#!/bin/bash
# ============================================================================
# setup_worker.sh - GPU Worker Node Setup for K-Cloud MLPerf Benchmarks
# ============================================================================
# Run this script on each GPU worker node to join the Kubernetes cluster
# and set up NVIDIA container runtime.
#
# Usage:
#   ./scripts/setup_worker.sh [--config config/cluster.env]
#
# The script automatically:
#   - Detects and frees GPU if in use
#   - Cleans up Calico CNI conflicts
#   - Joins cluster automatically if join command is available
#   - Fetches join command from master if not found locally
#
# Prerequisites:
#   - Ubuntu 20.04/22.04
#   - NVIDIA GPU with driver installed
#   - Root or sudo access
#   - Network connectivity to master node
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

# Parse command line arguments (config file only, everything else is automatic)
CONFIG_FILE="$PROJECT_ROOT/config/cluster.env"

while [[ $# -gt 0 ]]; do
    case $1 in
        --config)
            CONFIG_FILE="$2"
            shift 2
            ;;
        *)
            CONFIG_FILE="$1"
            shift
            ;;
    esac
done

# Load configuration
if [ -f "$PROJECT_ROOT/config/cluster.env.local" ]; then
    CONFIG_FILE="$PROJECT_ROOT/config/cluster.env.local"
fi

if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
    log "Loaded config from $CONFIG_FILE"
fi

# Try to extract MASTER_IP from join command if config is missing
if [ -z "$MASTER_IP" ] && [ -f "$PROJECT_ROOT/config/join-command.sh" ]; then
    # Extract the IP from the kubeadm join command (the IP after the colon in the join address)
    EXTRACTED_IP=$(grep -oE 'kubeadm join [0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' "$PROJECT_ROOT/config/join-command.sh" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)
    if [ -n "$EXTRACTED_IP" ]; then
        MASTER_IP="$EXTRACTED_IP"
        log "Extracted MASTER_IP=$MASTER_IP from join command"
    fi
fi

# Try to get MASTER_USER from current user or common defaults
if [ -z "$MASTER_USER" ]; then
    MASTER_USER="${USER:-$(whoami)}"
    log "Using current user as MASTER_USER: $MASTER_USER"
fi

# Validate required configuration
if [ -z "$MASTER_IP" ]; then
    warn "MASTER_IP not set in config and could not be auto-detected"
    warn "These are required for automated worker setup"
    echo ""
    echo "Please create config/cluster.env.local with:"
    echo "  MASTER_IP=\"<master-ip-address>\""
    echo "  MASTER_USER=\"<master-username>\""
    echo ""
    warn "MASTER_IP is required for automated cluster joining"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║     K-Cloud MLPerf - GPU Worker Node Setup                       ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""

# ============================================================================
# Free GPU - Kill processes using GPU
# ============================================================================
free_gpu() {
    log "Freeing GPU - Killing processes using GPU..."
    
    if ! command -v nvidia-smi &>/dev/null; then
        warn "nvidia-smi not available, skipping GPU cleanup"
        return
    fi
    
    # Check if any processes are using GPU
    GPU_PIDS=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | grep -v '^$' || echo "")
    
    if [ -z "$GPU_PIDS" ]; then
        log "No processes found using GPU"
        return
    fi
    
    log "Found processes using GPU: $GPU_PIDS"
    log "Killing GPU processes..."
    
    for pid in $GPU_PIDS; do
        if kill -0 "$pid" 2>/dev/null; then
            log "  Killing PID $pid..."
            sudo kill -9 "$pid" 2>/dev/null || true
        fi
    done
    
    # Also kill common GPU-using processes by name
    sudo pkill -9 -f "python.*vllm" 2>/dev/null || true
    sudo pkill -9 -f "torch" 2>/dev/null || true
    sudo pkill -9 -f "cuda" 2>/dev/null || true
    
    sleep 2
    
    # Verify GPU is free
    REMAINING_PIDS=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | grep -v '^$' || echo "")
    if [ -z "$REMAINING_PIDS" ]; then
        success "GPU freed successfully"
    else
        warn "Some GPU processes may still be running: $REMAINING_PIDS"
    fi
}

# ============================================================================
# Clean up Calico CNI configuration
# ============================================================================
cleanup_calico_cni() {
    log "Checking for Calico CNI conflicts..."
    
    if [ -f /etc/cni/net.d/10-calico.conflist ]; then
        warn "Found Calico CNI configuration. Removing to prevent conflicts with Flannel."
        sudo rm -f /etc/cni/net.d/10-calico.conflist /etc/cni/net.d/calico-kubeconfig
        
        # Restart kubelet if it's running to pick up CNI changes
        if systemctl is-active --quiet kubelet 2>/dev/null; then
            log "Restarting kubelet to apply CNI changes..."
            sudo systemctl restart kubelet || true
            sleep 2
        fi
        
        success "Calico CNI configuration removed"
    fi
}

# ============================================================================
# Step 1: Check NVIDIA Driver
# ============================================================================
check_nvidia_driver() {
    log "[1/8] Checking NVIDIA driver..."
    
    if ! command -v nvidia-smi &>/dev/null; then
        error "NVIDIA driver not installed. Please install NVIDIA driver first."
    fi
    
    nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader
    success "NVIDIA driver found"
}

# ============================================================================
# Step 2: System Prerequisites
# ============================================================================
install_prerequisites() {
    log "[2/8] Installing prerequisites..."
    
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
# Step 3: Install containerd
# ============================================================================
install_containerd() {
    log "[3/8] Installing containerd..."
    
    if command -v containerd &>/dev/null; then
        success "containerd already installed"
    else
        sudo apt-get update
        sudo apt-get install -y containerd
    fi
    
    # Configure containerd with NVIDIA runtime
    sudo mkdir -p /etc/containerd
    containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
    sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
    
    success "containerd installed"
}

# ============================================================================
# Step 4: Install NVIDIA Container Toolkit
# ============================================================================
install_nvidia_container_toolkit() {
    log "[4/8] Installing NVIDIA Container Toolkit..."
    
    if nvidia-ctk --version &>/dev/null 2>&1; then
        success "NVIDIA Container Toolkit already installed"
    else
        distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
        curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \
            sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg 2>/dev/null || true
        
        curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list | \
            sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
            sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
        
        sudo apt-get update
        sudo apt-get install -y nvidia-container-toolkit
    fi
    
    success "NVIDIA Container Toolkit installed"
}

# ============================================================================
# Step 5: Configure containerd for NVIDIA
# ============================================================================
configure_nvidia_runtime() {
    log "[5/8] Configuring NVIDIA runtime for containerd..."
    
    # Configure containerd to use nvidia runtime
    sudo nvidia-ctk runtime configure --runtime=containerd
    
    # Add nvidia as default runtime handler
    sudo sed -i 's/default_runtime_name = "runc"/default_runtime_name = "nvidia"/' /etc/containerd/config.toml 2>/dev/null || true
    
    # Restart containerd
    sudo systemctl restart containerd
    sudo systemctl enable containerd
    
    success "NVIDIA runtime configured"
}

# ============================================================================
# Step 6: Install kubeadm, kubelet
# ============================================================================
install_kubernetes() {
    log "[6/8] Installing Kubernetes components..."
    
    # Clean up Calico CNI configs before installing Kubernetes
    cleanup_calico_cni
    
    K8S_VERSION="${K8S_VERSION:-1.28}"
    
    if command -v kubelet &>/dev/null; then
        success "Kubernetes already installed: $(kubelet --version)"
        return
    fi
    
    sudo apt-get update
    sudo apt-get install -y apt-transport-https ca-certificates curl gpg
    
    # Add Kubernetes repository
    sudo mkdir -p /etc/apt/keyrings
    curl -fsSL https://pkgs.k8s.io/core:/stable:/v${K8S_VERSION}/deb/Release.key | \
        sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg 2>/dev/null || true
    
    echo "deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v${K8S_VERSION}/deb/ /" | \
        sudo tee /etc/apt/sources.list.d/kubernetes.list
    
    sudo apt-get update
    sudo apt-get install -y kubelet kubeadm kubectl
    sudo apt-mark hold kubelet kubeadm kubectl
    
    success "Kubernetes components installed"
}

# ============================================================================
# Step 7: Create data directories
# ============================================================================
create_directories() {
    log "[7/8] Creating data directories..."
    
    DATA_DIR="${DATA_DIR:-/data}"
    HF_CACHE_DIR="${HF_CACHE_DIR:-/data/hf-cache}"
    
    sudo mkdir -p "$DATA_DIR"
    sudo mkdir -p "$HF_CACHE_DIR"
    sudo mkdir -p "$DATA_DIR/mlcommons-inference"
    sudo chmod -R 777 "$DATA_DIR"
    
    success "Data directories created at $DATA_DIR"
}

# ============================================================================
# Step 8: Label node for GPU scheduling
# ============================================================================
label_node() {
    log "[8/8] GPU node ready for labeling..."
    
    echo ""
    echo "After joining the cluster, run this on the master node:"
    echo "  kubectl label node $(hostname) nvidia.com/gpu.present=true"
    echo ""
}

# ============================================================================
# Clean up incomplete kubeadm join state
# ============================================================================
cleanup_incomplete_join() {
    log "Checking for incomplete kubeadm join state..."
    
    # Always check for and clean up any leftover files that would prevent join
    # This is more aggressive but ensures a clean state
    HAS_PARTIAL_STATE=false
    
    # Check for any kubeadm-related files that indicate a previous attempt
    if [ -f /etc/kubernetes/kubelet.conf ] || \
       [ -d /etc/kubernetes/pki ] || \
       [ -f /etc/kubernetes/kubeadm-config.yaml ] || \
       [ -d /etc/kubernetes/manifests ]; then
        HAS_PARTIAL_STATE=true
    fi
    
    # If kubelet is running, we need to check if it's actually working
    # If kubelet is running but node isn't registered, it's likely a failed join
    if systemctl is-active --quiet kubelet 2>/dev/null; then
        # Check if node is actually registered in cluster
        # If not, we should clean up and rejoin
        if ! kubectl get nodes | grep "$(hostname)" | grep -q " Ready " 2>/dev/null; then
            HAS_PARTIAL_STATE=true
        fi
    fi
    
    if [ "$HAS_PARTIAL_STATE" = true ]; then
        warn "Found incomplete kubeadm join state"
        warn "This usually happens when a previous 'kubeadm join' failed partway through"
        log "Automatically cleaning up incomplete join state..."
        
        # Stop services first to free up ports and resources
        log "Stopping kubelet and containerd..."
        sudo systemctl stop kubelet 2>/dev/null || true
        sudo systemctl stop containerd 2>/dev/null || true
        sleep 3
        
        # Reset kubeadm state (this should handle most cleanup)
        log "Running kubeadm reset..."
        sudo kubeadm reset --force 2>/dev/null || true
        
        # Additional aggressive cleanup to ensure everything is removed
        log "Removing leftover Kubernetes files..."
        sudo rm -rf /etc/kubernetes/pki
        sudo rm -rf /etc/kubernetes/manifests
        sudo rm -f /etc/kubernetes/*.conf
        sudo rm -f /etc/kubernetes/kubeadm-config.yaml
        
        # Clean up iptables rules
        log "Cleaning up iptables rules..."
        sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F && sudo iptables -X || true
        
        # Clean up cni configs (including Calico)
        sudo rm -rf /etc/cni/net.d/*
        
        # Ensure kubelet is stopped
        sudo systemctl stop kubelet 2>/dev/null || true
        sleep 2
        
        # Verify port 10250 is free
        if command -v ss &>/dev/null; then
            if ss -tlnp | grep -q ':10250'; then
                warn "Port 10250 is still in use, may need manual intervention"
            fi
        fi
        
        # Restart containerd (it was stopped for cleanup, but kubeadm join needs it)
        log "Restarting containerd (required for kubeadm join)..."
        sudo systemctl start containerd || true
        sudo systemctl enable containerd || true
        sleep 2
        
        # Verify containerd is running
        if ! systemctl is-active --quiet containerd 2>/dev/null; then
            warn "containerd failed to start, attempting to start again..."
            sudo systemctl start containerd || true
            sleep 2
        fi
        
        if systemctl is-active --quiet containerd 2>/dev/null; then
            success "containerd is running"
        else
            error "containerd failed to start - kubeadm join will fail"
        fi
        
        success "kubeadm join state cleaned up"
    fi
}

# ============================================================================
# Join Cluster
# ============================================================================
join_cluster() {
    log "Joining Kubernetes cluster..."
    
    # Check if already successfully joined by verifying node registration
    # This is more reliable than just checking if kubelet.conf exists
    if [ -f /etc/kubernetes/kubelet.conf ]; then
        if systemctl is-active --quiet kubelet 2>/dev/null; then
            # Check if we can verify node registration (if kubectl is available and configured)
            NODE_REGISTERED=false
            if command -v kubectl &>/dev/null && [ -f ~/.kube/config ]; then
                NODE_NAME=$(hostname)
                if kubectl get node "$NODE_NAME" &>/dev/null 2>&1; then
                    NODE_REGISTERED=true
                    success "Node is already registered in cluster"
                    return
                fi
            fi
            
            # If we can't verify registration (no kubectl/kubeconfig on worker), 
            # check if kubelet is actually working by looking at its status
            if [ "$NODE_REGISTERED" = false ]; then
                # Check if kubelet is actually connected to the API server
                KUBELET_STATUS=$(systemctl status kubelet --no-pager 2>&1 | grep -i "error\|failed\|unable" | head -3)
                
                if [ -n "$KUBELET_STATUS" ]; then
                    warn "kubelet is running but has errors. This indicates a failed join."
                    log "Cleaning up incomplete join state..."
                    cleanup_incomplete_join
                else
                    # kubelet is running but we can't verify registration
                    # On worker nodes, we typically don't have kubeconfig, so we can't verify
                    # But if kubelet is running without errors, assume it's working
                    # However, if join command exists and we're here, something might be wrong
                    # Let's check if we should rejoin by trying to verify via master
                    if [ -n "$MASTER_IP" ] && [ -n "$MASTER_USER" ]; then
                        log "Cannot verify registration locally, checking via master..."
                        SSH_KEY="$HOME/.ssh/id_ed25519_kcloud"
                        if [ -f "$SSH_KEY" ]; then
                            NODE_NAME=$(hostname)
                            if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                               -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                               "${MASTER_USER}@${MASTER_IP}" \
                               "kubectl get node $NODE_NAME &>/dev/null" 2>/dev/null; then
                                success "Node is registered in cluster (verified via master)"
                                return
                            fi
                        fi
                    fi
                    
                    # If we can't verify, assume it's a failed join and clean up
                    warn "kubelet is running but cannot verify node registration."
                    warn "Cleaning up and rejoining to ensure proper state..."
                    cleanup_incomplete_join
                fi
            fi
        fi
    fi
    
    # Clean up any incomplete join state before attempting to join (only if not already cleaned)
    if [ ! -f /etc/kubernetes/kubelet.conf ]; then
        cleanup_incomplete_join
    fi
    
    JOIN_CMD_FILE="$PROJECT_ROOT/config/join-command.sh"
    
    # Setup SSH keys for automated access to master
    setup_ssh_keys() {
        if [ -z "$MASTER_IP" ] || [ -z "$MASTER_USER" ]; then
            warn "MASTER_IP or MASTER_USER not set, cannot setup SSH"
            return 1
        fi
        
        SSH_DIR="$HOME/.ssh"
        # Use a dedicated key for kcloud automation to avoid conflicts with existing keys
        SSH_KEY="$SSH_DIR/id_ed25519_kcloud"
        SSH_PUB_KEY="$SSH_DIR/id_ed25519_kcloud.pub"
        
        # Validate MASTER_IP is not the current host
        CURRENT_IP=$(hostname -I | awk '{print $1}')
        if [ "$MASTER_IP" = "$CURRENT_IP" ]; then
            error "MASTER_IP ($MASTER_IP) is the same as current host IP ($CURRENT_IP)"
            error "MASTER_IP must be the master node IP, not the worker node IP"
            error "Please set MASTER_IP to the master node address (e.g., 129.254.202.182)"
            return 1
        fi
        
        # Create .ssh directory if it doesn't exist
        mkdir -p "$SSH_DIR"
        chmod 700 "$SSH_DIR"
        
        # Generate dedicated SSH key for kcloud (always without passphrase for automation)
        if [ ! -f "$SSH_KEY" ]; then
            log "Generating dedicated SSH key for kcloud automation (no passphrase)..."
            ssh-keygen -t ed25519 -C "kcloud-worker-$(hostname)-auto" -f "$SSH_KEY" -N "" -q
            chmod 600 "$SSH_KEY"
            chmod 644 "$SSH_PUB_KEY"
            success "SSH key generated"
        else
            # Ensure existing key has no passphrase (regenerate if needed)
            if ssh-keygen -y -f "$SSH_KEY" -P "" &>/dev/null; then
                log "Using existing kcloud SSH key"
            else
                log "Existing key has passphrase, regenerating without passphrase for automation..."
                rm -f "$SSH_KEY" "$SSH_PUB_KEY"
                ssh-keygen -t ed25519 -C "kcloud-worker-$(hostname)-auto" -f "$SSH_KEY" -N "" -q
                chmod 600 "$SSH_KEY"
                chmod 644 "$SSH_PUB_KEY"
                success "SSH key regenerated without passphrase"
            fi
        fi
        
        # Test if passwordless SSH already works with our dedicated key
        # Disable SSH agent to force use of our key
        log "Testing passwordless SSH connection to master (${MASTER_IP})..."
        if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
           -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
           "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
            success "Passwordless SSH already configured"
            return 0
        fi
        
        # Try to copy public key to master
        log "Setting up passwordless SSH to master..."
        log "Note: You may be prompted for password once (this is a one-time setup)"
        
        # Method 1: Manual key copy (more reliable than ssh-copy-id for dedicated keys)
        # This gives us full control over which key is used
        log "Copying SSH key to master..."
        log "You will be prompted for the master password (one-time setup)..."
        if [ -f "$SSH_PUB_KEY" ]; then
            PUB_KEY_CONTENT=$(cat "$SSH_PUB_KEY")
            log "Using dedicated SSH key: $SSH_KEY"
            
            # Copy the key to master's authorized_keys
            # This will prompt for password, but ensures we use the right key
            if ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
               "${MASTER_USER}@${MASTER_IP}" \
               "mkdir -p ~/.ssh && chmod 700 ~/.ssh && \
                if ! grep -qF '${PUB_KEY_CONTENT}' ~/.ssh/authorized_keys 2>/dev/null; then \
                  echo '${PUB_KEY_CONTENT}' >> ~/.ssh/authorized_keys; \
                fi && \
                chmod 600 ~/.ssh/authorized_keys" 2>&1; then
                SSH_COPY_EXIT=0
            else
                SSH_COPY_EXIT=$?
            fi
            
            # Wait a moment for the key to be processed
            sleep 2
            
            # Verify it worked by testing passwordless SSH with our dedicated key
            # Disable SSH agent and force use of our key
            if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
               -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
               "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
                success "SSH key copied to master successfully"
                return 0
            elif [ $SSH_COPY_EXIT -eq 0 ]; then
                # Key copy succeeded but passwordless SSH not working yet
                # This can happen if the key was added but needs a moment
                log "Key copied, waiting for SSH to propagate..."
                sleep 3
                if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                   -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                   "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
                    success "SSH key copied to master successfully"
                    return 0
                else
                    warn "Key was copied but passwordless SSH not working"
                    warn "Verifying key on master and checking permissions..."
                    # Check if key is on master and verify permissions
                    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 \
                       "${MASTER_USER}@${MASTER_IP}" \
                       "grep -qF '${PUB_KEY_CONTENT}' ~/.ssh/authorized_keys 2>/dev/null && \
                        echo 'Key found' && \
                        ls -la ~/.ssh/authorized_keys ~/.ssh/ 2>/dev/null || \
                        echo 'Key not found or permission issue'" 2>/dev/null || true
                fi
            else
                log "SSH key copy failed or was cancelled (exit code: $SSH_COPY_EXIT)"
            fi
        fi
        
        # Method 2: Manual method (only if ssh-copy-id didn't work and passwordless SSH still doesn't work)
        # Check again if passwordless SSH works (maybe it just needed a moment)
        sleep 2
        if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
           -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
           "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
            success "Passwordless SSH is now working"
            return 0
        fi
        
        # If still not working, the key might be there but not working
        # This shouldn't happen if Method 1 worked, but we check one more time
        log "Retrying passwordless SSH connection..."
        sleep 2
        if SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
           -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
           "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
            success "Passwordless SSH is now working"
            return 0
        fi
        
        warn "Could not setup passwordless SSH automatically"
        warn "SSH key setup requires password authentication"
        return 1
    }
    
    # Try to fetch join command from master if not present
    # Check if MASTER_IP and MASTER_USER are configured
    if [ -z "$MASTER_IP" ] || [ -z "$MASTER_USER" ]; then
        warn "MASTER_IP or MASTER_USER not configured in config file"
        warn "Please set them in config/cluster.env.local or config/cluster.env"
        error "Cannot fetch join command without master configuration"
    fi
    
    if [ ! -f "$JOIN_CMD_FILE" ]; then
        log "Join command file not found locally, attempting to fetch from master..."
        
        # Always setup SSH keys if passwordless SSH doesn't work
        log "Ensuring SSH access to master (${MASTER_IP}) is configured..."
        SSH_KEY="$HOME/.ssh/id_ed25519_kcloud"
        # Disable SSH agent to force use of our dedicated key
        if ! SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
           -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
           "${MASTER_USER}@${MASTER_IP}" "exit" 2>/dev/null; then
            if ! setup_ssh_keys; then
                error "Could not setup SSH access to master. Please ensure:"
                echo "  1. SSH password authentication is enabled on master"
                echo "  2. You can manually run: ssh-copy-id ${MASTER_USER}@${MASTER_IP}"
                echo "  3. Or copy ~/.ssh/id_ed25519.pub to master's ~/.ssh/authorized_keys"
                exit 1
            fi
        else
            success "Passwordless SSH to master is working"
        fi
        
        if command -v scp &>/dev/null; then
            mkdir -p "$PROJECT_ROOT/config"
            log "Fetching from ${MASTER_USER}@${MASTER_IP}:${PROJECT_ROOT}/config/join-command.sh"
            # Disable SSH agent to force use of our dedicated key
            if SSH_AUTH_SOCK="" scp -i "$SSH_KEY" -o BatchMode=yes -o StrictHostKeyChecking=no \
               -o ConnectTimeout=10 -o IdentitiesOnly=yes \
               "${MASTER_USER}@${MASTER_IP}:${PROJECT_ROOT}/config/join-command.sh" \
               "$JOIN_CMD_FILE" 2>/dev/null; then
                chmod +x "$JOIN_CMD_FILE"
                success "Fetched join command from master"
            else
                # Try alternative: get join command directly via SSH
                log "File fetch failed, trying to get join command directly from master..."
                JOIN_CMD=$(SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o StrictHostKeyChecking=no \
                          -o ConnectTimeout=10 -o IdentitiesOnly=yes \
                          "${MASTER_USER}@${MASTER_IP}" \
                          "cd ${PROJECT_ROOT} && kubeadm token create --print-join-command 2>/dev/null" 2>/dev/null)
                
                if [ -n "$JOIN_CMD" ] && echo "$JOIN_CMD" | grep -q "kubeadm join"; then
                    echo "$JOIN_CMD" > "$JOIN_CMD_FILE"
                    chmod +x "$JOIN_CMD_FILE"
                    success "Got join command directly from master"
                else
                    error "Could not fetch join command from master. Please ensure SSH access is configured."
                fi
            fi
        else
            error "scp not available. Install openssh-client."
        fi
    fi
    
    if [ -f "$JOIN_CMD_FILE" ]; then
        # Ensure containerd is running before join (required by kubeadm)
        if ! systemctl is-active --quiet containerd 2>/dev/null; then
            log "Starting containerd (required for kubeadm join)..."
            sudo systemctl start containerd || true
            sudo systemctl enable containerd || true
            sleep 2
            
            if ! systemctl is-active --quiet containerd 2>/dev/null; then
                error "containerd is not running - kubeadm join will fail"
                error "Please start containerd manually: sudo systemctl start containerd"
                exit 1
            fi
            success "containerd is running"
        fi
        
        log "Using join command from $JOIN_CMD_FILE"
        sudo bash "$JOIN_CMD_FILE"
        success "Joined cluster successfully"
    else
        echo ""
        warn "No join command file found."
        
        # This should not happen if the fetch logic above worked
        error "Join command file not found and could not be fetched from master."
        echo "Please ensure:"
        echo "  1. MASTER_IP and MASTER_USER are set in config/cluster.env"
        echo "  2. SSH access to master is configured"
        echo "  3. Master node has run setup_master.sh to generate join command"
        exit 1
    fi
}

# ============================================================================
# Main
# ============================================================================
main() {
    # Check if running as root or with sudo
    if [ "$EUID" -ne 0 ] && ! sudo -n true 2>/dev/null; then
        error "This script requires root/sudo access"
    fi
    
    # Auto-detect if GPU is in use and free it
    if command -v nvidia-smi &>/dev/null; then
        GPU_PIDS=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null | grep -v '^$' || echo "")
        if [ -n "$GPU_PIDS" ]; then
            warn "GPU is in use. Freeing GPU before setup..."
            free_gpu
        fi
    fi
    
    # Clean up Calico CNI configs at the start
    cleanup_calico_cni
    
    check_nvidia_driver
    install_prerequisites
    install_containerd
    install_nvidia_container_toolkit
    configure_nvidia_runtime
    install_kubernetes
    create_directories
    label_node
    
    echo ""
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║                  Worker Node Setup Complete!                     ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo ""
    
    # Check if node is already joined and healthy
    if [ -f /etc/kubernetes/kubelet.conf ]; then
        if systemctl is-active --quiet kubelet 2>/dev/null; then
            # Check if node is actually Ready (not just kubelet running)
            NODE_READY=false
            if [ -n "$MASTER_IP" ] && [ -n "$MASTER_USER" ]; then
                SSH_KEY="$HOME/.ssh/id_ed25519_kcloud"
                if [ -f "$SSH_KEY" ]; then
                    NODE_NAME=$(hostname)
                    # Check if node is Ready via master
                    NODE_STATUS=$(SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                                 -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                                 "${MASTER_USER}@${MASTER_IP}" \
                                 "kubectl get node $NODE_NAME -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'" 2>/dev/null || echo "")
                    if [ "$NODE_STATUS" = "True" ]; then
                        NODE_READY=true
                        success "Node is Ready in cluster"
                    elif [ "$NODE_STATUS" = "False" ] || [ "$NODE_STATUS" = "Unknown" ]; then
                        warn "Node is NotReady in cluster. Kubelet may not be reporting status."
                        warn "Restarting kubelet to fix node status..."
                        sudo systemctl restart kubelet
                        sleep 5
                        # Check again after restart
                        NODE_STATUS=$(SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                                     -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                                     "${MASTER_USER}@${MASTER_IP}" \
                                     "kubectl get node $NODE_NAME -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'" 2>/dev/null || echo "")
                        if [ "$NODE_STATUS" = "True" ]; then
                            success "Node is now Ready after kubelet restart"
                            NODE_READY=true
                        else
                            warn "Node still not Ready. May need manual intervention."
                        fi
                    fi
                fi
            fi
            
            if [ "$NODE_READY" = true ]; then
                # Node is healthy, verify it stays Ready
                log "Node is Ready, verifying stability..."
                sleep 3
                NODE_STATUS=$(SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                             -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                             "${MASTER_USER}@${MASTER_IP}" \
                             "kubectl get node $NODE_NAME -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'" 2>/dev/null || echo "")
                if [ "$NODE_STATUS" = "True" ]; then
                    success "Node is Ready and stable"
                    return
                else
                    warn "Node became NotReady. Restarting kubelet..."
                    sudo systemctl restart kubelet
                    sleep 5
                fi
            fi
        fi
    fi
    
    # Check if join command file exists and auto-join
    JOIN_CMD_FILE="$PROJECT_ROOT/config/join-command.sh"
    
    if [ -f "$JOIN_CMD_FILE" ]; then
        # Auto-join if join command is available
        log "Join command found, joining cluster..."
        join_cluster
    else
        # Try to fetch join command from master if not found locally
        if [ -n "$MASTER_IP" ] && [ -n "$MASTER_USER" ]; then
            log "Join command not found locally, attempting to fetch from master..."
            join_cluster  # This will try to fetch from master
        else
            warn "Join command file not found: $JOIN_CMD_FILE"
            echo "Run setup_master.sh first to generate the join command, then:"
            echo "  1. Copy config/join-command.sh to this node, or"
            echo "  2. Set MASTER_IP and MASTER_USER in config/cluster.env.local to auto-fetch"
        fi
    fi
    
    # After joining, verify node is Ready and fix if needed
    if [ -f /etc/kubernetes/kubelet.conf ] && systemctl is-active --quiet kubelet 2>/dev/null; then
        if [ -n "$MASTER_IP" ] && [ -n "$MASTER_USER" ]; then
            SSH_KEY="$HOME/.ssh/id_ed25519_kcloud"
            if [ -f "$SSH_KEY" ]; then
                log "Verifying node is Ready in cluster after join..."
                NODE_NAME=$(hostname)
                sleep 3  # Give kubelet a moment to report status
                for i in {1..12}; do
                    NODE_STATUS=$(SSH_AUTH_SOCK="" ssh -i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 \
                                 -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
                                 "${MASTER_USER}@${MASTER_IP}" \
                                 "kubectl get node $NODE_NAME -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'" 2>/dev/null || echo "")
                    if [ "$NODE_STATUS" = "True" ]; then
                        success "Node is Ready in cluster"
                        break
                    elif [ "$NODE_STATUS" = "False" ] || [ "$NODE_STATUS" = "Unknown" ]; then
                        if [ $i -eq 1 ]; then
                            warn "Node is NotReady. Restarting kubelet..."
                            sudo systemctl restart kubelet
                            sleep 5
                        fi
                        log "Waiting for node to become Ready... ($i/12)"
                        sleep 5
                    else
                        log "Waiting for node status... ($i/12)"
                        sleep 5
                    fi
                done
                if [ "$NODE_STATUS" != "True" ]; then
                    warn "Node may still not be Ready. Check manually: kubectl get nodes"
                fi
            fi
        fi
    fi
    
    echo ""
    echo "Next steps (on master node):"
    echo "  1. kubectl get nodes  # Verify worker joined"
    echo "  2. kubectl label node $(hostname) nvidia.com/gpu.present=true"
    echo "  3. ./scripts/preflight.sh  # Verify everything is ready"
    echo "  4. ./scripts/run_benchmarks.sh --smoke  # Run benchmarks"
    echo ""
}

main "$@"
