#!/bin/bash

# Deploy RBAC configurations for kcloud-operator
# This script deploys all RBAC roles, role bindings, and service accounts

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if kubectl is available
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    print_success "kubectl is available"
}

# Function to check if cluster is accessible
check_cluster() {
    if ! kubectl cluster-info &> /dev/null; then
        print_error "Cannot access Kubernetes cluster"
        exit 1
    fi
    print_success "Kubernetes cluster is accessible"
}

# Function to create namespace
create_namespace() {
    local namespace="system"
    
    if kubectl get namespace "$namespace" &> /dev/null; then
        print_warning "Namespace $namespace already exists"
    else
        print_status "Creating namespace $namespace"
        kubectl create namespace "$namespace"
        print_success "Namespace $namespace created"
    fi
}

# Function to deploy RBAC resources
deploy_rbac() {
    local rbac_dir="config/rbac"
    
    print_status "Deploying RBAC resources from $rbac_dir"
    
    # Deploy service accounts
    if kubectl apply -f "$rbac_dir/service_account.yaml"; then
        print_success "Controller service account deployed"
    else
        print_error "Failed to deploy controller service account"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/webhook_service_account.yaml"; then
        print_success "Webhook service account deployed"
    else
        print_error "Failed to deploy webhook service account"
        exit 1
    fi
    
    # Deploy cluster roles
    if kubectl apply -f "$rbac_dir/role.yaml"; then
        print_success "Manager cluster role deployed"
    else
        print_error "Failed to deploy manager cluster role"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/webhook_role.yaml"; then
        print_success "Webhook cluster role deployed"
    else
        print_error "Failed to deploy webhook cluster role"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/metrics_auth_role.yaml"; then
        print_success "Metrics auth cluster role deployed"
    else
        print_error "Failed to deploy metrics auth cluster role"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/metrics_reader_role.yaml"; then
        print_success "Metrics reader cluster role deployed"
    else
        print_error "Failed to deploy metrics reader cluster role"
        exit 1
    fi
    
    # Deploy role bindings
    if kubectl apply -f "$rbac_dir/role_binding.yaml"; then
        print_success "Manager role binding deployed"
    else
        print_error "Failed to deploy manager role binding"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/webhook_role_binding.yaml"; then
        print_success "Webhook role binding deployed"
    else
        print_error "Failed to deploy webhook role binding"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/metrics_auth_role_binding.yaml"; then
        print_success "Metrics auth role binding deployed"
    else
        print_error "Failed to deploy metrics auth role binding"
        exit 1
    fi
    
    # Deploy leader election RBAC
    if kubectl apply -f "$rbac_dir/leader_election_role.yaml"; then
        print_success "Leader election role deployed"
    else
        print_error "Failed to deploy leader election role"
        exit 1
    fi
    
    if kubectl apply -f "$rbac_dir/leader_election_role_binding.yaml"; then
        print_success "Leader election role binding deployed"
    else
        print_error "Failed to deploy leader election role binding"
        exit 1
    fi
}

# Function to deploy user roles
deploy_user_roles() {
    local rbac_dir="config/rbac"
    
    print_status "Deploying user roles"
    
    # Deploy admin role
    if kubectl apply -f "$rbac_dir/workloadoptimizer_admin_role.yaml"; then
        print_success "WorkloadOptimizer admin role deployed"
    else
        print_warning "Failed to deploy WorkloadOptimizer admin role"
    fi
    
    # Deploy editor role
    if kubectl apply -f "$rbac_dir/workloadoptimizer_editor_role.yaml"; then
        print_success "WorkloadOptimizer editor role deployed"
    else
        print_warning "Failed to deploy WorkloadOptimizer editor role"
    fi
    
    # Deploy viewer role
    if kubectl apply -f "$rbac_dir/workloadoptimizer_viewer_role.yaml"; then
        print_success "WorkloadOptimizer viewer role deployed"
    else
        print_warning "Failed to deploy WorkloadOptimizer viewer role"
    fi
}

# Function to verify RBAC deployment
verify_rbac() {
    print_status "Verifying RBAC deployment"
    
    # Check service accounts
    local service_accounts=("controller-manager" "webhook-manager")
    for sa in "${service_accounts[@]}"; do
        if kubectl get serviceaccount "$sa" -n system &> /dev/null; then
            print_success "Service account $sa is deployed"
        else
            print_error "Service account $sa is not deployed"
            exit 1
        fi
    done
    
    # Check cluster roles
    local cluster_roles=("manager-role" "webhook-role" "metrics-auth-role" "metrics-reader" "workloadoptimizer-admin-role" "workloadoptimizer-editor-role" "workloadoptimizer-viewer-role")
    for cr in "${cluster_roles[@]}"; do
        if kubectl get clusterrole "$cr" &> /dev/null; then
            print_success "Cluster role $cr is deployed"
        else
            print_error "Cluster role $cr is not deployed"
            exit 1
        fi
    done
    
    # Check cluster role bindings
    local cluster_role_bindings=("manager-rolebinding" "webhook-rolebinding" "metrics-auth-rolebinding" "leader-election-rolebinding")
    for crb in "${cluster_role_bindings[@]}"; do
        if kubectl get clusterrolebinding "$crb" &> /dev/null; then
            print_success "Cluster role binding $crb is deployed"
        else
            print_error "Cluster role binding $crb is not deployed"
            exit 1
        fi
    done
}

# Function to show RBAC information
show_rbac_info() {
    print_status "RBAC Information:"
    echo
    
    echo "Service Accounts:"
    kubectl get serviceaccounts -n system -l app.kubernetes.io/name=k8s-workload-operator 2>/dev/null || echo "No service accounts found"
    echo
    
    echo "Cluster Roles:"
    kubectl get clusterroles -l app.kubernetes.io/name=k8s-workload-operator 2>/dev/null || echo "No cluster roles found"
    echo
    
    echo "Cluster Role Bindings:"
    kubectl get clusterrolebindings -l app.kubernetes.io/name=k8s-workload-operator 2>/dev/null || echo "No cluster role bindings found"
    echo
}

# Function to create user role binding examples
create_user_examples() {
    print_status "Creating user role binding examples"
    
    # Create example namespace for user testing
    kubectl create namespace kcloud-users --dry-run=client -o yaml | kubectl apply -f -
    
    # Create example user role bindings
    cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: example-admin-binding
  labels:
    app.kubernetes.io/name: k8s-workload-operator
    app.kubernetes.io/component: example
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: workloadoptimizer-admin-role
subjects:
- kind: User
  name: example-admin
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: example-editor-binding
  labels:
    app.kubernetes.io/name: k8s-workload-operator
    app.kubernetes.io/component: example
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: workloadoptimizer-editor-role
subjects:
- kind: User
  name: example-editor
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: example-viewer-binding
  labels:
    app.kubernetes.io/name: k8s-workload-operator
    app.kubernetes.io/component: example
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: workloadoptimizer-viewer-role
subjects:
- kind: User
  name: example-viewer
  apiGroup: rbac.authorization.k8s.io
EOF
    
    print_success "User role binding examples created"
}

# Function to cleanup RBAC resources
cleanup_rbac() {
    print_status "Cleaning up RBAC resources"
    
    # Delete user role binding examples
    kubectl delete clusterrolebinding example-admin-binding example-editor-binding example-viewer-binding --ignore-not-found=true
    
    # Delete RBAC resources
    kubectl delete -f config/rbac/ --ignore-not-found=true
    
    print_success "RBAC resources cleaned up"
}

# Main function
main() {
    print_status "Starting RBAC deployment for kcloud-operator"
    echo
    
    # Check prerequisites
    check_kubectl
    check_cluster
    
    # Create namespace
    create_namespace
    
    # Deploy RBAC resources
    deploy_rbac
    
    # Deploy user roles
    deploy_user_roles
    
    # Verify deployment
    verify_rbac
    
    # Create user examples
    create_user_examples
    
    # Show information
    show_rbac_info
    
    print_success "RBAC deployment completed successfully!"
    echo
    print_status "You can now use the following commands to interact with RBAC:"
    echo "  kubectl get clusterroles -l app.kubernetes.io/name=k8s-workload-operator"
    echo "  kubectl get clusterrolebindings -l app.kubernetes.io/name=k8s-workload-operator"
    echo "  kubectl get serviceaccounts -n system -l app.kubernetes.io/name=k8s-workload-operator"
    echo
    print_status "To cleanup, run: $0 --cleanup"
}

# Parse command line arguments
case "${1:-}" in
    --cleanup)
        cleanup_rbac
        ;;
    --help|-h)
        echo "Usage: $0 [--cleanup|--help]"
        echo "  --cleanup  Remove all RBAC resources"
        echo "  --help     Show this help message"
        ;;
    *)
        main
        ;;
esac
