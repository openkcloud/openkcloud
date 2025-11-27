#!/bin/bash

# Policy Engine System Validation Script
set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_ROOT/build"
TEST_TIMEOUT="5m"
SERVER_STARTUP_TIMEOUT="30s"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test results
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

run_test() {
    local test_name="$1"
    local test_command="$2"
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    
    log_info "Running test: $test_name"
    
    if eval "$test_command"; then
        log_success "Test passed: $test_name"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        return 0
    else
        log_error "Test failed: $test_name"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        return 1
    fi
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check Go installation
    if ! command -v go &> /dev/null; then
        log_error "Go is not installed"
        exit 1
    fi
    
    # Check Go version
    go_version=$(go version | cut -d' ' -f3)
    log_info "Go version: $go_version"
    
    # Check required tools
    local tools=("make" "docker" "kubectl" "jq")
    for tool in "${tools[@]}"; do
        if ! command -v "$tool" &> /dev/null; then
            log_warn "$tool is not installed (some tests may be skipped)"
        fi
    done
    
    log_success "Prerequisites check completed"
}

build_application() {
    log_info "Building application..."
    
    cd "$PROJECT_ROOT"
    
    # Build main application
    make build
    if [ ! -f "$BUILD_DIR/policy-engine" ]; then
        log_error "Failed to build main application"
        exit 1
    fi
    
    # Build CLI
    make build-cli
    if [ ! -f "$BUILD_DIR/policy-cli" ]; then
        log_error "Failed to build CLI"
        exit 1
    fi
    
    log_success "Application built successfully"
}

run_unit_tests() {
    log_info "Running unit tests..."
    
    cd "$PROJECT_ROOT"
    
    # Run Go tests
    if go test -v -timeout "$TEST_TIMEOUT" ./...; then
        log_success "Unit tests passed"
    else
        log_error "Unit tests failed"
        exit 1
    fi
}

start_test_server() {
    log_info "Starting test server..."
    
    cd "$PROJECT_ROOT"
    
    # Kill any existing server
    pkill -f "policy-engine" || true
    
    # Start server in background
    "$BUILD_DIR/policy-engine" &
    SERVER_PID=$!
    
    # Wait for server to start
    local count=0
    while [ $count -lt 30 ]; do
        if curl -s http://localhost:8080/health > /dev/null 2>&1; then
            log_success "Test server started successfully"
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done
    
    log_error "Test server failed to start"
    kill $SERVER_PID 2>/dev/null || true
    exit 1
}

stop_test_server() {
    log_info "Stopping test server..."
    
    if [ -n "$SERVER_PID" ]; then
        kill $SERVER_PID 2>/dev/null || true
        wait $SERVER_PID 2>/dev/null || true
    fi
    
    # Kill any remaining processes
    pkill -f "policy-engine" || true
    
    log_success "Test server stopped"
}

test_api_endpoints() {
    log_info "Testing API endpoints..."
    
    # Test health endpoint
    run_test "Health Check" "curl -s http://localhost:8080/health | jq -r '.status' | grep -q 'healthy'"
    
    # Test metrics endpoint
    run_test "Metrics Endpoint" "curl -s http://localhost:8080/metrics | grep -q 'policy_engine_'"
    
    # Test policies endpoint
    run_test "Policies List" "curl -s http://localhost:8080/api/v1/policies | jq -r '.policies' | grep -q '\\[\\]'"
    
    # Test workloads endpoint
    run_test "Workloads List" "curl -s http://localhost:8080/api/v1/workloads | jq -r '.workloads' | grep -q '\\[\\]'"
    
    # Test automation endpoint
    run_test "Automation Rules List" "curl -s http://localhost:8080/api/v1/automation/rules | jq -r '.automation_rules' | grep -q '\\[\\]'"
}

test_policy_operations() {
    log_info "Testing policy operations..."
    
    # Create a test policy
    local policy_data='{
        "name": "validation-test-policy",
        "description": "Policy for system validation",
        "type": "cost-optimization",
        "enabled": true,
        "rules": [{
            "name": "test-rule",
            "description": "Test rule",
            "condition": "cpu_usage > 80",
            "action": "scale_up",
            "priority": 1
        }]
    }'
    
    # Create policy
    local policy_response=$(curl -s -X POST -H "Content-Type: application/json" \
        -d "$policy_data" http://localhost:8080/api/v1/policies)
    local policy_id=$(echo "$policy_response" | jq -r '.id')
    
    if [ "$policy_id" != "null" ] && [ -n "$policy_id" ]; then
        run_test "Policy Creation" "true"
        
        # Get policy
        run_test "Policy Retrieval" "curl -s http://localhost:8080/api/v1/policies/$policy_id | jq -r '.id' | grep -q '$policy_id'"
        
        # Update policy
        local updated_data=$(echo "$policy_data" | jq '.description = "Updated policy description"')
        run_test "Policy Update" "curl -s -X PUT -H 'Content-Type: application/json' -d '$updated_data' http://localhost:8080/api/v1/policies/$policy_id | jq -r '.description' | grep -q 'Updated policy description'"
        
        # Delete policy
        run_test "Policy Deletion" "curl -s -X DELETE http://localhost:8080/api/v1/policies/$policy_id | jq -r '.message' | grep -q 'Policy deleted successfully'"
    else
        run_test "Policy Creation" "false"
    fi
}

test_workload_operations() {
    log_info "Testing workload operations..."
    
    # Create a test workload
    local workload_data='{
        "name": "validation-test-workload",
        "description": "Workload for system validation",
        "namespace": "default",
        "resources": {
            "cpu": "100m",
            "memory": "128Mi"
        },
        "labels": {
            "app": "validation-test"
        }
    }'
    
    # Create workload
    local workload_response=$(curl -s -X POST -H "Content-Type: application/json" \
        -d "$workload_data" http://localhost:8080/api/v1/workloads)
    local workload_id=$(echo "$workload_response" | jq -r '.id')
    
    if [ "$workload_id" != "null" ] && [ -n "$workload_id" ]; then
        run_test "Workload Creation" "true"
        
        # Get workload
        run_test "Workload Retrieval" "curl -s http://localhost:8080/api/v1/workloads/$workload_id | jq -r '.id' | grep -q '$workload_id'"
        
        # Update workload
        local updated_data=$(echo "$workload_data" | jq '.description = "Updated workload description"')
        run_test "Workload Update" "curl -s -X PUT -H 'Content-Type: application/json' -d '$updated_data' http://localhost:8080/api/v1/workloads/$workload_id | jq -r '.description' | grep -q 'Updated workload description'"
        
        # Delete workload
        run_test "Workload Deletion" "curl -s -X DELETE http://localhost:8080/api/v1/workloads/$workload_id | jq -r '.message' | grep -q 'Workload deleted successfully'"
    else
        run_test "Workload Creation" "false"
    fi
}

test_evaluation_operations() {
    log_info "Testing evaluation operations..."
    
    # Create policy and workload for evaluation
    local policy_data='{
        "name": "evaluation-test-policy",
        "description": "Policy for evaluation testing",
        "type": "cost-optimization",
        "enabled": true,
        "rules": [{
            "name": "memory-rule",
            "description": "Memory optimization rule",
            "condition": "memory_usage > 90",
            "action": "scale_down",
            "priority": 1
        }]
    }'
    
    local workload_data='{
        "name": "evaluation-test-workload",
        "description": "Workload for evaluation testing",
        "namespace": "default",
        "resources": {
            "cpu": "200m",
            "memory": "256Mi"
        }
    }'
    
    # Create policy
    local policy_response=$(curl -s -X POST -H "Content-Type: application/json" \
        -d "$policy_data" http://localhost:8080/api/v1/policies)
    local policy_id=$(echo "$policy_response" | jq -r '.id')
    
    # Create workload
    local workload_response=$(curl -s -X POST -H "Content-Type: application/json" \
        -d "$workload_data" http://localhost:8080/api/v1/workloads)
    local workload_id=$(echo "$workload_response" | jq -r '.id')
    
    if [ "$policy_id" != "null" ] && [ "$workload_id" != "null" ]; then
        # Test workload evaluation
        local eval_response=$(curl -s -X POST http://localhost:8080/api/v1/evaluations/workload/$workload_id)
        local eval_id=$(echo "$eval_response" | jq -r '.evaluation_id')
        
        if [ "$eval_id" != "null" ] && [ -n "$eval_id" ]; then
            run_test "Workload Evaluation" "true"
            
            # Test evaluation history
            run_test "Evaluation History" "curl -s http://localhost:8080/api/v1/evaluations/history | jq -r '.evaluations' | grep -q '$eval_id'"
        else
            run_test "Workload Evaluation" "false"
        fi
        
        # Clean up
        curl -s -X DELETE http://localhost:8080/api/v1/policies/$policy_id > /dev/null
        curl -s -X DELETE http://localhost:8080/api/v1/workloads/$workload_id > /dev/null
    else
        run_test "Workload Evaluation" "false"
    fi
}

test_cli_operations() {
    log_info "Testing CLI operations..."
    
    # Test CLI help
    run_test "CLI Help" "$BUILD_DIR/policy-cli --help | grep -q 'Policy Engine CLI'"
    
    # Test CLI status
    run_test "CLI Status" "$BUILD_DIR/policy-cli --server-host localhost --server-port 8080 status | grep -q 'Policy Engine Status'"
    
    # Test CLI ping
    run_test "CLI Ping" "$BUILD_DIR/policy-cli --server-host localhost --server-port 8080 ping | grep -q 'Ping successful'"
    
    # Test CLI metrics
    run_test "CLI Metrics" "$BUILD_DIR/policy-cli --server-host localhost --server-port 8080 metrics | grep -q 'policy_engine_'"
}

test_docker_build() {
    log_info "Testing Docker build..."
    
    cd "$PROJECT_ROOT"
    
    # Build Docker image
    if docker build -t policy-engine:test . > /dev/null 2>&1; then
        run_test "Docker Build" "true"
        
        # Test Docker run (short-lived)
        if timeout 10s docker run --rm policy-engine:test --help > /dev/null 2>&1; then
            run_test "Docker Run" "true"
        else
            run_test "Docker Run" "false"
        fi
    else
        run_test "Docker Build" "false"
    fi
}

test_makefile_targets() {
    log_info "Testing Makefile targets..."
    
    cd "$PROJECT_ROOT"
    
    # Test various make targets
    run_test "Make Help" "make help | grep -q 'Available targets'"
    run_test "Make Version" "make version | grep -q 'Application:'"
    run_test "Make Clean" "make clean"
    run_test "Make Build" "make build"
    run_test "Make Build CLI" "make build-cli"
}

test_example_files() {
    log_info "Testing example files..."
    
    # Check if example files exist and are valid
    local example_files=(
        "examples/policies/cost-optimization-policy.yaml"
        "examples/policies/automation-rule.yaml"
        "examples/policies/workload-priority-policy.yaml"
        "examples/workloads/sample-workload.yaml"
    )
    
    for file in "${example_files[@]}"; do
        if [ -f "$PROJECT_ROOT/$file" ]; then
            run_test "Example File: $file" "true"
        else
            run_test "Example File: $file" "false"
        fi
    done
}

run_integration_tests() {
    log_info "Running integration tests..."
    
    cd "$PROJECT_ROOT"
    
    # Run Go integration tests
    if go test -v -timeout "$TEST_TIMEOUT" ./tests/...; then
        run_test "Integration Tests" "true"
    else
        run_test "Integration Tests" "false"
    fi
}

generate_test_report() {
    log_info "Generating test report..."
    
    echo ""
    echo "=========================================="
    echo "         SYSTEM VALIDATION REPORT        "
    echo "=========================================="
    echo ""
    echo "Total Tests: $TOTAL_TESTS"
    echo "Passed: $PASSED_TESTS"
    echo "Failed: $FAILED_TESTS"
    echo ""
    
    if [ $FAILED_TESTS -eq 0 ]; then
        echo -e "${GREEN}✅ All tests passed! System validation successful.${NC}"
        return 0
    else
        echo -e "${RED}❌ $FAILED_TESTS tests failed. System validation unsuccessful.${NC}"
        return 1
    fi
}

# Main validation function
main() {
    log_info "Starting Policy Engine System Validation..."
    echo ""
    
    # Check prerequisites
    check_prerequisites
    echo ""
    
    # Build application
    build_application
    echo ""
    
    # Run unit tests
    run_unit_tests
    echo ""
    
    # Test Makefile targets
    test_makefile_targets
    echo ""
    
    # Test example files
    test_example_files
    echo ""
    
    # Test Docker build
    test_docker_build
    echo ""
    
    # Start test server
    start_test_server
    echo ""
    
    # Test API endpoints
    test_api_endpoints
    echo ""
    
    # Test CRUD operations
    test_policy_operations
    echo ""
    
    test_workload_operations
    echo ""
    
    # Test evaluation operations
    test_evaluation_operations
    echo ""
    
    # Test CLI operations
    test_cli_operations
    echo ""
    
    # Stop test server
    stop_test_server
    echo ""
    
    # Run integration tests
    run_integration_tests
    echo ""
    
    # Generate report
    generate_test_report
}

# Cleanup function
cleanup() {
    log_info "Cleaning up..."
    stop_test_server
    
    # Clean up any temporary files
    rm -f /tmp/policy-engine-test-*
    
    log_success "Cleanup completed"
}

# Set up trap for cleanup
trap cleanup EXIT

# Run main function
main "$@"


