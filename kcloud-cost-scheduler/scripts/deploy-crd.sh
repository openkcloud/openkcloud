#!/bin/bash

# Deploy CRD manifests for kcloud-operator
# This script deploys the Custom Resource Definitions for WorkloadOptimizer, CostPolicy, and PowerPolicy

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
    local namespace="kcloud-system"
    
    if kubectl get namespace "$namespace" &> /dev/null; then
        print_warning "Namespace $namespace already exists"
    else
        print_status "Creating namespace $namespace"
        kubectl create namespace "$namespace"
        print_success "Namespace $namespace created"
    fi
}

# Function to deploy CRDs
deploy_crds() {
    local crd_dir="config/crd"
    
    print_status "Deploying CRDs from $crd_dir"
    
    # Deploy WorkloadOptimizer CRD
    if kubectl apply -f "$crd_dir/bases/kcloud.io_workloadoptimizers.yaml"; then
        print_success "WorkloadOptimizer CRD deployed"
    else
        print_error "Failed to deploy WorkloadOptimizer CRD"
        exit 1
    fi
    
    # Deploy CostPolicy CRD
    if kubectl apply -f "$crd_dir/bases/kcloud.io_costpolicies.yaml"; then
        print_success "CostPolicy CRD deployed"
    else
        print_error "Failed to deploy CostPolicy CRD"
        exit 1
    fi
    
    # Deploy PowerPolicy CRD
    if kubectl apply -f "$crd_dir/bases/kcloud.io_powerpolicies.yaml"; then
        print_success "PowerPolicy CRD deployed"
    else
        print_error "Failed to deploy PowerPolicy CRD"
        exit 1
    fi
}

# Function to verify CRD deployment
verify_crds() {
    print_status "Verifying CRD deployment"
    
    local crds=("workloadoptimizers.kcloud.io" "costpolicies.kcloud.io" "powerpolicies.kcloud.io")
    
    for crd in "${crds[@]}"; do
        if kubectl get crd "$crd" &> /dev/null; then
            print_success "CRD $crd is deployed"
        else
            print_error "CRD $crd is not deployed"
            exit 1
        fi
    done
}

# Function to deploy sample resources
deploy_samples() {
    local samples_dir="config/samples"
    
    print_status "Deploying sample resources from $samples_dir"
    
    # Deploy WorkloadOptimizer samples
    if kubectl apply -f "$samples_dir/kcloud.io_v1alpha1_workloadoptimizer.yaml"; then
        print_success "WorkloadOptimizer samples deployed"
    else
        print_warning "Failed to deploy WorkloadOptimizer samples"
    fi
    
    # Deploy CostPolicy samples
    if kubectl apply -f "$samples_dir/kcloud.io_v1alpha1_costpolicy.yaml"; then
        print_success "CostPolicy samples deployed"
    else
        print_warning "Failed to deploy CostPolicy samples"
    fi
    
    # Deploy PowerPolicy samples
    if kubectl apply -f "$samples_dir/kcloud.io_v1alpha1_powerpolicy.yaml"; then
        print_success "PowerPolicy samples deployed"
    else
        print_warning "Failed to deploy PowerPolicy samples"
    fi
}

# Function to show CRD information
show_crd_info() {
    print_status "CRD Information:"
    echo
    
    echo "WorkloadOptimizer CRD:"
    kubectl get crd workloadoptimizers.kcloud.io -o jsonpath='{.spec.names.kind}' 2>/dev/null || echo "Not found"
    echo
    
    echo "CostPolicy CRD:"
    kubectl get crd costpolicies.kcloud.io -o jsonpath='{.spec.names.kind}' 2>/dev/null || echo "Not found"
    echo
    
    echo "PowerPolicy CRD:"
    kubectl get crd powerpolicies.kcloud.io -o jsonpath='{.spec.names.kind}' 2>/dev/null || echo "Not found"
    echo
}

# Function to show sample resources
show_samples() {
    print_status "Sample Resources:"
    echo
    
    echo "WorkloadOptimizers:"
    kubectl get workloadoptimizers -o wide 2>/dev/null || echo "No WorkloadOptimizers found"
    echo
    
    echo "CostPolicies:"
    kubectl get costpolicies -o wide 2>/dev/null || echo "No CostPolicies found"
    echo
    
    echo "PowerPolicies:"
    kubectl get powerpolicies -o wide 2>/dev/null || echo "No PowerPolicies found"
    echo
}

# Function to cleanup CRDs
cleanup_crds() {
    print_status "Cleaning up CRDs"
    
    # Delete sample resources first
    kubectl delete -f config/samples/ --ignore-not-found=true
    
    # Delete CRDs
    kubectl delete -f config/crd/bases/ --ignore-not-found=true
    
    print_success "CRDs cleaned up"
}

# Main function
main() {
    print_status "Starting CRD deployment for kcloud-operator"
    echo
    
    # Check prerequisites
    check_kubectl
    check_cluster
    
    # Create namespace
    create_namespace
    
    # Deploy CRDs
    deploy_crds
    
    # Verify deployment
    verify_crds
    
    # Deploy samples
    deploy_samples
    
    # Show information
    show_crd_info
    show_samples
    
    print_success "CRD deployment completed successfully!"
    echo
    print_status "You can now use the following commands to interact with the CRDs:"
    echo "  kubectl get workloadoptimizers"
    echo "  kubectl get costpolicies"
    echo "  kubectl get powerpolicies"
    echo
    print_status "To cleanup, run: $0 --cleanup"
}

# Parse command line arguments
case "${1:-}" in
    --cleanup)
        cleanup_crds
        ;;
    --help|-h)
        echo "Usage: $0 [--cleanup|--help]"
        echo "  --cleanup  Remove all CRDs and sample resources"
        echo "  --help     Show this help message"
        ;;
    *)
        main
        ;;
esac
