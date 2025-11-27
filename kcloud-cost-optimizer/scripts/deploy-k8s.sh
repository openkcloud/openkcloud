#!/bin/bash

# Policy Engine Kubernetes Deployment Script
set -e

# Configuration
NAMESPACE="policy-engine"
APP_NAME="policy-engine"
IMAGE_TAG="${IMAGE_TAG:-latest}"
DOMAIN="${DOMAIN:-policy-engine.local}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if kubectl is installed
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not installed"
        exit 1
    fi
    
    # Check if kustomize is installed
    if ! command -v kustomize &> /dev/null; then
        log_warn "kustomize is not installed, using kubectl apply -k"
        USE_KUBECTL_K=true
    else
        USE_KUBECTL_K=false
    fi
    
    # Check cluster connection
    if ! kubectl cluster-info &> /dev/null; then
        log_error "Cannot connect to Kubernetes cluster"
        exit 1
    fi
    
    log_info "Prerequisites check completed"
}

build_image() {
    log_info "Building Docker image..."
    docker build -t ${APP_NAME}:${IMAGE_TAG} .
    log_info "Docker image built successfully"
}

deploy_namespace() {
    log_info "Creating namespace..."
    kubectl apply -f k8s/namespace.yaml
    log_info "Namespace created"
}

deploy_config() {
    log_info "Deploying configuration..."
    kubectl apply -f k8s/configmap.yaml
    kubectl apply -f k8s/secret.yaml
    log_info "Configuration deployed"
}

deploy_rbac() {
    log_info "Deploying RBAC..."
    kubectl apply -f k8s/rbac.yaml
    log_info "RBAC deployed"
}

deploy_app() {
    log_info "Deploying application..."
    
    # Update image tag in deployment
    if [ "$USE_KUBECTL_K" = true ]; then
        kubectl apply -k k8s/
    else
        kustomize build k8s/ | kubectl apply -f -
    fi
    
    log_info "Application deployed"
}

wait_for_deployment() {
    log_info "Waiting for deployment to be ready..."
    kubectl wait --for=condition=available --timeout=300s deployment/${APP_NAME} -n ${NAMESPACE}
    log_info "Deployment is ready"
}

show_status() {
    log_info "Deployment status:"
    echo ""
    kubectl get pods -n ${NAMESPACE}
    echo ""
    kubectl get svc -n ${NAMESPACE}
    echo ""
    kubectl get ingress -n ${NAMESPACE}
}

show_urls() {
    log_info "Service URLs:"
    echo ""
    echo "Cluster IP: kubectl get svc ${APP_NAME}-service -n ${NAMESPACE} -o jsonpath='{.spec.clusterIP}:{.spec.ports[0].port}'"
    echo "Health Check: kubectl port-forward svc/${APP_NAME}-service 8080:8080 -n ${NAMESPACE}"
    echo "Metrics: kubectl port-forward svc/${APP_NAME}-service 9090:9090 -n ${NAMESPACE}"
    echo ""
    echo "API Endpoints:"
    echo "  Health: /health"
    echo "  Metrics: /metrics"
    echo "  Policies: /api/v1/policies"
    echo "  Workloads: /api/v1/workloads"
    echo "  Evaluations: /api/v1/evaluations"
    echo "  Automation: /api/v1/automation"
}

cleanup() {
    log_warn "Cleaning up deployment..."
    kubectl delete -k k8s/ --ignore-not-found=true
    log_info "Cleanup completed"
}

# Main deployment function
deploy() {
    log_info "Starting Policy Engine Kubernetes deployment..."
    
    check_prerequisites
    build_image
    deploy_namespace
    deploy_config
    deploy_rbac
    deploy_app
    wait_for_deployment
    show_status
    show_urls
    
    log_info "Deployment completed successfully!"
}

# Command line argument handling
case "${1:-deploy}" in
    deploy)
        deploy
        ;;
    cleanup)
        cleanup
        ;;
    status)
        show_status
        ;;
    *)
        echo "Usage: $0 {deploy|cleanup|status}"
        echo ""
        echo "Commands:"
        echo "  deploy  - Deploy Policy Engine to Kubernetes (default)"
        echo "  cleanup - Remove Policy Engine from Kubernetes"
        echo "  status  - Show deployment status"
        echo ""
        echo "Environment variables:"
        echo "  IMAGE_TAG - Docker image tag (default: latest)"
        echo "  DOMAIN    - Domain for ingress (default: policy-engine.local)"
        exit 1
        ;;
esac


