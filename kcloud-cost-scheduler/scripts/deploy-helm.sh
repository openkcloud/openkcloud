#!/bin/bash

# KCloud Workload Optimizer Operator Helm Deployment Script
# This script deploys the KCloud Workload Optimizer Operator using Helm

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
RELEASE_NAME="kcloud-operator"
NAMESPACE="kcloud-operator-system"
CHART_PATH="./charts/kcloud-operator"
VALUES_FILE=""
DRY_RUN=false
UPGRADE=false
UNINSTALL=false
STATUS=false
TEMPLATE=false
PACKAGE=false
LINT=false

# Function to print colored output
print_info() {
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

# Function to show usage
show_usage() {
    cat << EOF
Usage: $0 [OPTIONS]

KCloud Workload Optimizer Operator Helm Deployment Script

OPTIONS:
    -r, --release-name NAME     Helm release name (default: kcloud-operator)
    -n, --namespace NAME        Kubernetes namespace (default: kcloud-operator-system)
    -c, --chart-path PATH       Path to Helm chart (default: ./charts/kcloud-operator)
    -f, --values-file FILE      Custom values file
    -d, --dry-run              Dry run mode (show what would be deployed)
    -u, --upgrade              Upgrade existing release
    -U, --uninstall            Uninstall release
    -s, --status               Show release status
    -t, --template             Generate templates
    -p, --package              Package chart
    -l, --lint                 Lint chart
    -h, --help                 Show this help message

EXAMPLES:
    # Install with default values
    $0

    # Install with custom values
    $0 -f custom-values.yaml

    # Upgrade existing release
    $0 -u

    # Dry run installation
    $0 -d

    # Uninstall release
    $0 -U

    # Show release status
    $0 -s

    # Package chart
    $0 -p

    # Lint chart
    $0 -l

EOF
}

# Function to check prerequisites
check_prerequisites() {
    print_info "Checking prerequisites..."
    
    # Check if helm is installed
    if ! command -v helm &> /dev/null; then
        print_error "Helm is not installed. Please install Helm first."
        exit 1
    fi
    
    # Check if kubectl is installed
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed. Please install kubectl first."
        exit 1
    fi
    
    # Check if chart directory exists
    if [ ! -d "$CHART_PATH" ]; then
        print_error "Chart directory not found: $CHART_PATH"
        exit 1
    fi
    
    print_success "Prerequisites check passed"
}

# Function to lint chart
lint_chart() {
    print_info "Linting Helm chart..."
    if helm lint "$CHART_PATH"; then
        print_success "Chart linting passed"
    else
        print_error "Chart linting failed"
        exit 1
    fi
}

# Function to package chart
package_chart() {
    print_info "Packaging Helm chart..."
    if helm package "$CHART_PATH"; then
        print_success "Chart packaged successfully"
    else
        print_error "Chart packaging failed"
        exit 1
    fi
}

# Function to generate templates
generate_templates() {
    print_info "Generating Helm templates..."
    if [ -n "$VALUES_FILE" ]; then
        helm template "$RELEASE_NAME" "$CHART_PATH" -f "$VALUES_FILE"
    else
        helm template "$RELEASE_NAME" "$CHART_PATH"
    fi
}

# Function to show status
show_status() {
    print_info "Showing release status..."
    if helm status "$RELEASE_NAME" --namespace "$NAMESPACE" 2>/dev/null; then
        print_success "Release status retrieved"
    else
        print_warning "Release not found or not accessible"
    fi
}

# Function to uninstall release
uninstall_release() {
    print_info "Uninstalling release: $RELEASE_NAME"
    if helm uninstall "$RELEASE_NAME" --namespace "$NAMESPACE"; then
        print_success "Release uninstalled successfully"
    else
        print_error "Failed to uninstall release"
        exit 1
    fi
}

# Function to install/upgrade release
deploy_release() {
    local action="install"
    if [ "$UPGRADE" = true ]; then
        action="upgrade"
    fi
    
    print_info "Deploying release: $RELEASE_NAME (action: $action)"
    
    local helm_cmd="helm $action $RELEASE_NAME $CHART_PATH"
    helm_cmd="$helm_cmd --create-namespace --namespace $NAMESPACE"
    
    if [ -n "$VALUES_FILE" ]; then
        helm_cmd="$helm_cmd -f $VALUES_FILE"
    fi
    
    if [ "$DRY_RUN" = true ]; then
        helm_cmd="$helm_cmd --dry-run --debug"
    fi
    
    if eval "$helm_cmd"; then
        if [ "$DRY_RUN" = true ]; then
            print_success "Dry run completed successfully"
        else
            print_success "Release deployed successfully"
        fi
    else
        print_error "Failed to deploy release"
        exit 1
    fi
}

# Function to show post-deployment info
show_post_deployment_info() {
    if [ "$DRY_RUN" = true ] || [ "$UNINSTALL" = true ] || [ "$STATUS" = true ] || [ "$TEMPLATE" = true ] || [ "$PACKAGE" = true ] || [ "$LINT" = true ]; then
        return
    fi
    
    print_info "Post-deployment information:"
    echo ""
    echo "1. Check the operator status:"
    echo "   kubectl get pods --namespace $NAMESPACE -l app.kubernetes.io/name=kcloud-operator"
    echo ""
    echo "2. View the operator logs:"
    echo "   kubectl logs --namespace $NAMESPACE -l app.kubernetes.io/name=kcloud-operator"
    echo ""
    echo "3. Check CRDs:"
    echo "   kubectl get crd | grep kcloud.io"
    echo ""
    echo "4. Check webhooks:"
    echo "   kubectl get mutatingwebhookconfigurations"
    echo "   kubectl get validatingwebhookconfigurations"
    echo ""
    echo "5. Access metrics endpoint:"
    echo "   kubectl port-forward --namespace $NAMESPACE svc/$RELEASE_NAME 8080:8080"
    echo "   # Then visit http://localhost:8080/metrics"
    echo ""
    echo "6. Check sample resources:"
    echo "   kubectl get workloadoptimizer --all-namespaces"
    echo "   kubectl get costpolicy --all-namespaces"
    echo "   kubectl get powerpolicy --all-namespaces"
    echo ""
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -r|--release-name)
            RELEASE_NAME="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -c|--chart-path)
            CHART_PATH="$2"
            shift 2
            ;;
        -f|--values-file)
            VALUES_FILE="$2"
            shift 2
            ;;
        -d|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -u|--upgrade)
            UPGRADE=true
            shift
            ;;
        -U|--uninstall)
            UNINSTALL=true
            shift
            ;;
        -s|--status)
            STATUS=true
            shift
            ;;
        -t|--template)
            TEMPLATE=true
            shift
            ;;
        -p|--package)
            PACKAGE=true
            shift
            ;;
        -l|--lint)
            LINT=true
            shift
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Main execution
main() {
    print_info "Starting KCloud Workload Optimizer Operator Helm deployment"
    print_info "Release name: $RELEASE_NAME"
    print_info "Namespace: $NAMESPACE"
    print_info "Chart path: $CHART_PATH"
    
    if [ -n "$VALUES_FILE" ]; then
        print_info "Values file: $VALUES_FILE"
    fi
    
    check_prerequisites
    
    if [ "$LINT" = true ]; then
        lint_chart
    fi
    
    if [ "$PACKAGE" = true ]; then
        package_chart
    fi
    
    if [ "$TEMPLATE" = true ]; then
        generate_templates
    fi
    
    if [ "$STATUS" = true ]; then
        show_status
    fi
    
    if [ "$UNINSTALL" = true ]; then
        uninstall_release
    fi
    
    if [ "$LINT" = false ] && [ "$PACKAGE" = false ] && [ "$TEMPLATE" = false ] && [ "$STATUS" = false ] && [ "$UNINSTALL" = false ]; then
        deploy_release
    fi
    
    show_post_deployment_info
    
    print_success "Helm deployment script completed successfully"
}

# Run main function
main "$@"
