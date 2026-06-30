#!/bin/bash
# ============================================================================
# run_benchmarks.sh - K-Cloud LLM Benchmark Suite
# ============================================================================
# Portable benchmark suite for bare-metal Kubernetes clusters with GPU support.
#
# Usage:
#   ./scripts/run_benchmarks.sh [options]
#
# Options:
#   --smoke        Quick test with 10 samples (~15 min)
#   --mlperf       Run only MLPerf benchmark
#   --mmlu         Run only MMLU-Pro benchmark
#   --inference    Run only LLM Inference benchmark
#   --skip-checks  Skip pre-flight checks
#   --fix          Auto-fix issues during pre-flight
# ============================================================================
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
K8S_DIR="$PROJECT_DIR/k8s"
JOBS_DIR="$K8S_DIR/jobs"
BENCHMARKS_DIR="$PROJECT_DIR/benchmarks"

# Load configuration
CONFIG_FILE="$PROJECT_DIR/config/cluster.env"
if [ -f "$PROJECT_DIR/config/cluster.env.local" ]; then
    CONFIG_FILE="$PROJECT_DIR/config/cluster.env.local"
fi
[ -f "$CONFIG_FILE" ] && source "$CONFIG_FILE"

NAMESPACE="${BENCHMARK_NAMESPACE:-mlperf}"

# Defaults
SMOKE_TEST=false
RUN_MLPERF=true
RUN_MMLU=true
RUN_INFERENCE=true
SKIP_PREFLIGHT=false
AUTO_FIX=false
HF_TOKEN="${HF_TOKEN:-}"
RUN_ID="$(date +%Y%m%d-%H%M%S)"
RESULTS_DIR="$PROJECT_DIR/results/$RUN_ID"
SUMMARY_FILE="$RESULTS_DIR/summary.txt"

for arg in "$@"; do
    case $arg in
        --smoke) SMOKE_TEST=true ;;
        --mlperf) RUN_MMLU=false; RUN_INFERENCE=false ;;
        --mmlu) RUN_MLPERF=false; RUN_INFERENCE=false ;;
        --inference) RUN_MLPERF=false; RUN_MMLU=false ;;
        --skip-checks) SKIP_PREFLIGHT=true ;;
        --fix) AUTO_FIX=true ;;
        --help|-h)
            echo "Usage: $0 [--smoke] [--mlperf|--mmlu|--inference] [--skip-checks] [--fix]"
            echo "  --smoke        Quick test with 10 samples (~15 min)"
            echo "  --mlperf       Run only MLPerf"
            echo "  --mmlu         Run only MMLU-Pro"
            echo "  --inference    Run only LLM Inference"
            echo "  --skip-checks  Skip pre-flight checks"
            echo "  --fix          Auto-fix issues during pre-flight"
            exit 0 ;;
    esac
done

# ============================================================================
# Pre-flight Checks with Auto-Recovery
# ============================================================================
run_preflight() {
    if [ "$SKIP_PREFLIGHT" = true ]; then
        echo "[Preflight] Skipped (--skip-checks)"
        return 0
    fi
    
    echo "[Preflight] Running cluster checks..."
    
    # Quick connectivity check
    if ! kubectl cluster-info &>/dev/null 2>&1; then
        echo "[Preflight] Cannot connect to cluster. Attempting auto-recovery..."
        
        # Check if master IP changed
        CURRENT_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
        KUBECONFIG_IP=$(grep "server:" ~/.kube/config 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1)
        
        if [ -n "$CURRENT_IP" ] && [ -n "$KUBECONFIG_IP" ] && [ "$CURRENT_IP" != "$KUBECONFIG_IP" ]; then
            echo "[Preflight] Master IP changed: $KUBECONFIG_IP -> $CURRENT_IP"
            
            if [ "$AUTO_FIX" = true ]; then
                echo "[Preflight] Updating configuration..."
                
                # Update kubeconfig
                sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" ~/.kube/config
                
                # Update Kubernetes manifests if on master
                if [ -d /etc/kubernetes/manifests ]; then
                    sudo sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" /etc/kubernetes/manifests/*.yaml 2>/dev/null || true
                    sudo sed -i "s/$KUBECONFIG_IP/$CURRENT_IP/g" /etc/kubernetes/*.conf 2>/dev/null || true
                    
                    # Regenerate certificates
                    if [ -f /etc/kubernetes/kubeadm-config.yaml ]; then
                        sudo kubeadm certs renew apiserver --config /etc/kubernetes/kubeadm-config.yaml 2>/dev/null || true
                    fi
                    
                    sudo systemctl restart kubelet
                    echo "[Preflight] Waiting for API server to restart..."
                    sleep 15
                fi
            else
                echo "[Preflight] Run with --fix to auto-recover"
                echo "  Or manually update ~/.kube/config with the new IP"
                exit 1
            fi
        fi
        
        # Retry connection
        if ! kubectl cluster-info &>/dev/null 2>&1; then
            echo "[Preflight] ERROR: Cannot connect to Kubernetes cluster"
            echo ""
            echo "Troubleshooting steps:"
            echo "  1. Check if kubelet is running: sudo systemctl status kubelet"
            echo "  2. Check API server: sudo crictl ps | grep kube-apiserver"
            echo "  3. Run preflight.sh for detailed diagnostics: ./scripts/preflight.sh --fix"
            exit 1
        fi
        
        echo "[Preflight] Cluster connectivity restored!"
    fi
    
    echo "[Preflight] ✓ Cluster is reachable"
    
    # Quick GPU check
    GPU_NODES=$(kubectl get nodes -l nvidia.com/gpu.present=true --no-headers 2>/dev/null | wc -l)
    if [ "$GPU_NODES" -eq 0 ]; then
        echo "[Preflight] WARNING: No GPU nodes labeled"
        
        if [ "$AUTO_FIX" = true ]; then
            # Try to auto-label nodes with GPUs
            for node in $(kubectl get nodes --no-headers | awk '{print $1}'); do
                GPU_COUNT=$(kubectl get node $node -o jsonpath='{.status.capacity.nvidia\.com/gpu}' 2>/dev/null)
                if [ -n "$GPU_COUNT" ] && [ "$GPU_COUNT" -gt 0 ]; then
                    echo "[Preflight] Labeling $node with nvidia.com/gpu.present=true"
                    kubectl label node $node nvidia.com/gpu.present=true --overwrite
                fi
            done
        fi
    else
        echo "[Preflight] ✓ $GPU_NODES GPU node(s) available"
    fi
    
    # Quick RuntimeClass check
    if ! kubectl get runtimeclass nvidia &>/dev/null 2>&1; then
        echo "[Preflight] WARNING: NVIDIA RuntimeClass not found"
        
        if [ "$AUTO_FIX" = true ]; then
            echo "[Preflight] Creating NVIDIA RuntimeClass..."
            cat <<EOF | kubectl apply -f -
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nvidia
handler: nvidia
EOF
        fi
    else
        echo "[Preflight] ✓ NVIDIA RuntimeClass present"
    fi
    
    echo ""
}

# Run pre-flight checks first
run_preflight

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║   K-Cloud LLM Benchmark Suite - Llama 3.1 8B                     ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
if [ "$SMOKE_TEST" = true ]; then
    echo "Mode: SMOKE TEST (10 samples each, ~15 min)"
    SAMPLE_SPLIT="[:10]"
else
    echo "Mode: FULL DATASET (8-10 hours)"
    echo "  - MLPerf: ~11k samples (CNN/DailyMail test set)"
    echo "  - MMLU-Pro: ~12k questions"
    echo "  - Progress updates: every 1% or 60 seconds"
    echo "  - Heartbeat status: every 2 minutes during job execution"
    SAMPLE_SPLIT=""
fi
echo "Date: $(date)"
echo ""
mkdir -p "$RESULTS_DIR"
{
    echo "K-Cloud LLM Benchmark Suite"
    echo "Run ID: $RUN_ID"
    echo "Date: $(date)"
    echo "Mode: $([ "$SMOKE_TEST" = true ] && echo 'Smoke Test' || echo 'Full Dataset')"
    echo "Components: MLPerf=$RUN_MLPERF MMLU=$RUN_MMLU Inference=$RUN_INFERENCE"
    echo ""
} > "$SUMMARY_FILE"

status() {
    echo "[$(date '+%H:%M:%S')] $*"
}

check_runtime_and_gpu() {
    status "[1a] Validating GPU runtime..."
    if ! kubectl get runtimeclass nvidia >/dev/null 2>&1; then
        echo "ERROR: RuntimeClass 'nvidia' not found."
        exit 1
    fi

    local gpu_nodes
    gpu_nodes=$(kubectl get nodes -l nvidia.com/gpu.present=true --no-headers 2>/dev/null | wc -l | xargs)
    if [ "${gpu_nodes:-0}" -eq 0 ]; then
        echo "ERROR: No GPU nodes labeled with nvidia.com/gpu.present=true"
        exit 1
    fi
    echo "✓ RuntimeClass 'nvidia' present; GPU nodes available: $gpu_nodes"
    echo ""
}

ensure_hf_secret() {
    if kubectl get secret hf-token -n $NAMESPACE >/dev/null 2>&1; then
        local placeholder current
        placeholder=$(echo -n "YOUR_HUGGINGFACE_TOKEN" | base64)
        current=$(kubectl get secret hf-token -n $NAMESPACE -o jsonpath='{.data.HF_TOKEN}' 2>/dev/null || echo "")

        if [ "$current" = "$placeholder" ]; then
            if [ -z "$HF_TOKEN" ]; then
                echo "ERROR: hf-token secret contains placeholder. Export HF_TOKEN to recreate it."
                exit 1
            fi
            echo "Updating hf-token secret with provided HF_TOKEN..."
            kubectl delete secret hf-token -n $NAMESPACE >/dev/null 2>&1 || true
            kubectl create secret generic hf-token --from-literal=HF_TOKEN="$HF_TOKEN" -n $NAMESPACE
        fi
    else
        if [ -z "$HF_TOKEN" ]; then
            echo "ERROR: Set HF_TOKEN env var"
            exit 1
        fi
        kubectl create secret generic hf-token --from-literal=HF_TOKEN="$HF_TOKEN" -n $NAMESPACE
    fi
}

setup_benchmark_configmaps() {
    status "[2a] Setting up benchmark script ConfigMaps..."
    
    kubectl delete configmap benchmark-scripts -n $NAMESPACE --ignore-not-found=true >/dev/null 2>&1
    
    # MLPerf uses official MLCommons repo (cloned via initContainer)
    # Only MMLU and Inference need ConfigMaps
    kubectl create configmap benchmark-scripts -n $NAMESPACE \
        --from-file=mmlu_pro_cot.py="$BENCHMARKS_DIR/mmlu_pro_cot.py" \
        --from-file=inference_throughput.py="$BENCHMARKS_DIR/inference_throughput.py"
    
    echo "✓ Benchmark scripts ConfigMap created"
    echo "  Note: MLPerf uses official MLCommons LoadGen (auto-cloned)"
}

# Load YAML template and substitute variables
load_job_yaml() {
    local yaml_file=$1
    local yaml_content
    yaml_content=$(cat "$yaml_file")
    # Substitute ${SAMPLE_SPLIT} with actual value
    echo "${yaml_content//\$\{SAMPLE_SPLIT\}/$SAMPLE_SPLIT}"
}

# Check cluster
status "[1/4] Checking Cluster..."
kubectl cluster-info > /dev/null || { echo "ERROR: Cannot connect to cluster"; exit 1; }
echo "✓ Cluster OK"
kubectl get nodes -o wide
echo ""
check_runtime_and_gpu

# Setup
status "[2/4] Setting up namespace..."
kubectl apply -f "$K8S_DIR/00-namespace.yaml" 2>/dev/null || true
ensure_hf_secret
setup_benchmark_configmaps
echo "✓ Namespace, secrets, and ConfigMaps ready"
echo ""

# Function to run job and wait for completion
run_job() {
    local job_name=$1
    local description=$2
    local yaml_content=$3
    local log_file="$RESULTS_DIR/${job_name}.log"
    local diag_file="$RESULTS_DIR/${job_name}-diagnostics.log"
    local manifest_file="$RESULTS_DIR/${job_name}-manifest.yaml"
    local metrics_file="$RESULTS_DIR/${job_name}-metrics.txt"
    
    echo "════════════════════════════════════════════════════════════════════"
    echo "  $description"
    echo "════════════════════════════════════════════════════════════════════"
    echo "$yaml_content" > "$manifest_file"
    status "Saved manifest -> $manifest_file"
    
    # Delete existing job AND any orphaned pods from previous runs
    status "Cleaning up any existing job and pods..."
    kubectl delete job $job_name -n $NAMESPACE --ignore-not-found=true 2>/dev/null
    kubectl delete pods -n $NAMESPACE -l job-name=$job_name --ignore-not-found=true 2>/dev/null
    sleep 3
    
    # Verify cleanup - ensure no pods exist before creating new job
    local remaining_pods=$(kubectl get pods -n $NAMESPACE -l job-name=$job_name --no-headers 2>/dev/null | wc -l)
    if [ "$remaining_pods" -gt 0 ]; then
        status "Waiting for old pods to terminate..."
        kubectl wait --for=delete pods -n $NAMESPACE -l job-name=$job_name --timeout=60s 2>/dev/null || true
    fi
    
    # Apply job
    status "Creating job $job_name..."
    echo "$yaml_content" | kubectl apply -f -
    
    # Wait for pod to start (max 20 attempts = ~100 seconds)
    echo "Waiting for pod to start..."
    local pod=""
    local pod_status=""
    for i in $(seq 1 20); do
        pod=$(kubectl get pods -n $NAMESPACE -l job-name=$job_name -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
        if [ -n "$pod" ]; then
            pod_status=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null)
            if [ "$pod_status" = "Running" ]; then
                echo "Pod $pod is Running on $(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.spec.nodeName}')"
                break
            elif [ "$pod_status" = "Succeeded" ] || [ "$pod_status" = "Failed" ]; then
                echo "Pod $pod finished with status: $pod_status"
                break
            else
                status "Pod $pod state: $pod_status (waiting...)"

                # Fail fast if scheduler already marked it unschedulable
                pod_scheduled_status=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.conditions[?(@.type=="PodScheduled")].status}' 2>/dev/null || true)
                pod_scheduled_reason=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.conditions[?(@.type=="PodScheduled")].reason}' 2>/dev/null || true)
                if [ "$pod_scheduled_status" = "False" ] && [ "$pod_scheduled_reason" = "Unschedulable" ]; then
                    echo "ERROR: Pod $pod is Unschedulable"
                    kubectl describe pod $pod -n $NAMESPACE || true
                    kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20 || true
                    {
                        echo "[$(date '+%F %T')] $description unschedulable"
                        kubectl describe pod $pod -n $NAMESPACE
                        kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20
                    } > "$diag_file" 2>&1 || true
                    echo "FAIL $description" >> "$SUMMARY_FILE"
                    return 1
                fi
            fi
        fi
        status "Waiting for pod... attempt $i/20"
        sleep 5
    done
    
    if [ -z "$pod" ]; then
        echo "ERROR: Pod failed to start"
        kubectl describe job $job_name -n $NAMESPACE
        kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20
        {
            echo "[$(date '+%F %T')] $description pod failed to start"
            kubectl describe job $job_name -n $NAMESPACE
            kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20
        } > "$diag_file" 2>&1
        echo "FAIL $description" >> "$SUMMARY_FILE"
        return 1
    fi

    if [ "$pod_status" = "Pending" ]; then
        echo "ERROR: Pod $pod is still Pending after waiting"
        kubectl describe pod $pod -n $NAMESPACE || true
        kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20 || true
        {
            echo "[$(date '+%F %T')] $description still pending after timeout"
            kubectl describe pod $pod -n $NAMESPACE
            kubectl get events -n $NAMESPACE --sort-by=.metadata.creationTimestamp | tail -n 20
        } > "$diag_file" 2>&1 || true
        echo "FAIL $description" >> "$SUMMARY_FILE"
        return 1
    fi
    
    # Stream logs
    echo ""
    echo "--- Streaming logs (real-time) ---"
    status "Log file: $log_file"
    
    if command -v stdbuf >/dev/null 2>&1; then
        stdbuf -oL kubectl logs -f $pod -n $NAMESPACE 2>&1 | stdbuf -oL tee -a "$log_file" &
    else
        kubectl logs -f $pod -n $NAMESPACE 2>&1 | tee -a "$log_file" &
    fi
    local logs_pid=$!
    
    # Wait for job to complete
    echo ""
    local poll_count=0
    local start_time=$(date +%s)
    local max_wait_time=7200  # 2 hours max wait time
    while true; do
        sleep 10
        poll_count=$((poll_count + 1))
        local now=$(date +%s)
        local elapsed=$((now - start_time))
        
        # Check for timeout
        if [ $elapsed -gt $max_wait_time ]; then
            kill $logs_pid 2>/dev/null || true
            wait $logs_pid 2>/dev/null || true
            echo ""
            echo "✗ $description: TIMEOUT (exceeded ${max_wait_time}s)"
            echo "FAIL $description (timeout)" >> "$SUMMARY_FILE"
            return 1
        fi
        
        if [ $((poll_count % 3)) -eq 0 ]; then
            local elapsed_min=$((elapsed / 60))
            local pod_status_now=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
            
            local recent_progress=$(kubectl logs $pod -n $NAMESPACE --tail=10 2>/dev/null | grep -E '^\s*\[|samples/s|q/s|ETA:|%' | tail -3)
            if [ -n "$recent_progress" ]; then
                echo ""
                status "[Progress Update] Elapsed: ${elapsed_min}m | Pod: $pod_status_now"
                echo "$recent_progress"
            else
                status "[Heartbeat] Job: $job_name | Pod: $pod_status_now | Elapsed: ${elapsed_min}m"
            fi
        fi
        
        # Check Job status first (most reliable)
        succeeded=$(kubectl get job $job_name -n $NAMESPACE -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "")
        failed=$(kubectl get job $job_name -n $NAMESPACE -o jsonpath='{.status.failed}' 2>/dev/null || echo "")
        
        if [ "$succeeded" = "1" ]; then
            kill $logs_pid 2>/dev/null || true
            wait $logs_pid 2>/dev/null || true
            echo ""
            # Extract metrics from log file
            extract_metrics "$job_name" "$log_file" "$metrics_file"
            echo "✓ $description: PASS"
            echo "PASS $description" >> "$SUMMARY_FILE"
            return 0
        elif [ "$failed" = "1" ]; then
            kill $logs_pid 2>/dev/null || true
            wait $logs_pid 2>/dev/null || true
            echo ""
            echo "✗ $description: FAIL"
            kubectl describe pod $pod -n $NAMESPACE || true
            {
                echo "[$(date '+%F %T')] $description failed"
                kubectl describe pod $pod -n $NAMESPACE
            } > "$diag_file" 2>&1 || true
            echo "FAIL $description" >> "$SUMMARY_FILE"
            return 1
        fi
        
        # Also check Pod status as fallback
        local current_pod_status=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
        if [ "$current_pod_status" = "Succeeded" ]; then
            # Pod succeeded but Job status might not be updated yet, wait a bit more
            sleep 5
            # Re-check Job status
            succeeded=$(kubectl get job $job_name -n $NAMESPACE -o jsonpath='{.status.succeeded}' 2>/dev/null || echo "")
            if [ "$succeeded" = "1" ]; then
                kill $logs_pid 2>/dev/null || true
                wait $logs_pid 2>/dev/null || true
                echo ""
                # Extract metrics from log file
                extract_metrics "$job_name" "$log_file" "$metrics_file"
                echo "✓ $description: PASS"
                echo "PASS $description" >> "$SUMMARY_FILE"
                return 0
            else
                # Pod succeeded but Job not marked as succeeded - check exit code
                local exit_code=$(kubectl get pod $pod -n $NAMESPACE -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null || echo "")
                if [ "$exit_code" = "0" ] || [ -z "$exit_code" ]; then
                    kill $logs_pid 2>/dev/null || true
                    wait $logs_pid 2>/dev/null || true
                    echo ""
                    # Extract metrics from log file
                    extract_metrics "$job_name" "$log_file" "$metrics_file"
                    echo "✓ $description: PASS (Pod succeeded)"
                    echo "PASS $description" >> "$SUMMARY_FILE"
                    return 0
                fi
            fi
        elif [ "$current_pod_status" = "Failed" ]; then
            kill $logs_pid 2>/dev/null || true
            wait $logs_pid 2>/dev/null || true
            echo ""
            echo "✗ $description: FAIL"
            kubectl describe pod $pod -n $NAMESPACE || true
            {
                echo "[$(date '+%F %T')] $description failed"
                kubectl describe pod $pod -n $NAMESPACE
            } > "$diag_file" 2>&1 || true
            echo "FAIL $description" >> "$SUMMARY_FILE"
            return 1
        fi
    done
}

# Extract metrics from benchmark logs
extract_metrics() {
    local job_name=$1
    local log_file=$2
    local metrics_file=$3
    
    case "$job_name" in
        mlperf-bench)
            # Extract ROUGE scores from official evaluation.py output
            # Format: {'rouge1': '27.7066', 'rouge2': '10.8145', 'rougeL': '18.7711', ...}
            local results_line=$(grep "rouge1" "$log_file" 2>/dev/null | tail -1)
            local rouge1=$(echo "$results_line" | grep -oP "'rouge1': '\K[0-9.]+")
            local rouge2=$(echo "$results_line" | grep -oP "'rouge2': '\K[0-9.]+")
            local rougel=$(echo "$results_line" | grep -oP "'rougeL': '\K[0-9.]+")
            local gen_num=$(echo "$results_line" | grep -oP "'gen_num': \K[0-9]+")
            # Fallback: try alternate format
            [ -z "$rougel" ] && rougel=$(grep "ROUGE-L:" "$log_file" 2>/dev/null | tail -1 | awk '{print $2}')
            {
                echo "ROUGE_1=$rouge1"
                echo "ROUGE_2=$rouge2"
                echo "ROUGE_L=$rougel"
                echo "TOTAL_SAMPLES=$gen_num"
            } > "$metrics_file"
            ;;
        mmlu-bench)
            # Extract accuracy
            local accuracy=$(grep "Overall Accuracy:" "$log_file" 2>/dev/null | tail -1 | grep -oE '[0-9.]+%' | head -1)
            local correct=$(grep "Overall Accuracy:" "$log_file" 2>/dev/null | tail -1 | grep -oE '[0-9]+/[0-9]+' | head -1)
            local throughput=$(grep "Throughput:" "$log_file" 2>/dev/null | tail -1 | grep -oE '[0-9.]+ q/s' | head -1)
            {
                echo "ACCURACY=$accuracy"
                echo "CORRECT=\"($correct)\""
                echo "THROUGHPUT=\"$throughput\""
            } > "$metrics_file"
            ;;
        inference-bench)
            # Extract inference throughput
            local single_throughput=$(grep "tok/s" "$log_file" 2>/dev/null | grep "Tokens:" | tail -1 | grep -oE '[0-9.]+ tok/s' | head -1)
            local batch_throughput=$(grep "Throughput:" "$log_file" 2>/dev/null | tail -1 | grep -oE '[0-9.]+ tok/s' | head -1)
            local gpu=$(grep "GPU:" "$log_file" 2>/dev/null | tail -1 | sed 's/.*GPU: *//')
            {
                echo "SINGLE_THROUGHPUT=\"$single_throughput\""
                echo "BATCH_THROUGHPUT=\"$batch_throughput\""
                echo "GPU=\"$gpu\""
            } > "$metrics_file"
            ;;
    esac
}

# Results tracking
declare -A BENCHMARK_STATUS
declare -A BENCHMARK_METRICS

echo "[3/4] Running Benchmarks..."
echo ""

# MLPerf - load from YAML file
if [ "$RUN_MLPERF" = true ]; then
    MLPERF_YAML=$(load_job_yaml "$JOBS_DIR/mlperf-job.yaml")
    if run_job "mlperf-bench" "MLPerf Inference" "$MLPERF_YAML"; then
        BENCHMARK_STATUS["mlperf"]="PASS"
        if [ -f "$RESULTS_DIR/mlperf-bench-metrics.txt" ]; then
            source "$RESULTS_DIR/mlperf-bench-metrics.txt" 2>/dev/null || true
            BENCHMARK_METRICS["mlperf"]="ROUGE-1: ${ROUGE_1:-N/A} | ROUGE-2: ${ROUGE_2:-N/A} | ROUGE-L: ${ROUGE_L:-N/A} | Samples: ${TOTAL_SAMPLES:-N/A}"
        fi
    else
        BENCHMARK_STATUS["mlperf"]="FAIL"
        BENCHMARK_METRICS["mlperf"]="(benchmark failed)"
    fi
    echo ""
fi

# MMLU-Pro - load from YAML file
if [ "$RUN_MMLU" = true ]; then
    MMLU_YAML=$(load_job_yaml "$JOBS_DIR/mmlu-job.yaml")
    if run_job "mmlu-bench" "MMLU-Pro Benchmark" "$MMLU_YAML"; then
        BENCHMARK_STATUS["mmlu"]="PASS"
        if [ -f "$RESULTS_DIR/mmlu-bench-metrics.txt" ]; then
            source "$RESULTS_DIR/mmlu-bench-metrics.txt" 2>/dev/null || true
            BENCHMARK_METRICS["mmlu"]="Accuracy: ${ACCURACY:-N/A} ${CORRECT:-} | ${THROUGHPUT:-N/A}"
        fi
    else
        BENCHMARK_STATUS["mmlu"]="FAIL"
        BENCHMARK_METRICS["mmlu"]="(benchmark failed)"
    fi
    echo ""
fi

# Inference - load from YAML file
if [ "$RUN_INFERENCE" = true ]; then
    INF_YAML=$(load_job_yaml "$JOBS_DIR/inference-job.yaml")
    if run_job "inference-bench" "LLM Inference Test" "$INF_YAML"; then
        BENCHMARK_STATUS["inference"]="PASS"
        if [ -f "$RESULTS_DIR/inference-bench-metrics.txt" ]; then
            source "$RESULTS_DIR/inference-bench-metrics.txt" 2>/dev/null || true
            BENCHMARK_METRICS["inference"]="Batch: ${BATCH_THROUGHPUT:-N/A} | Single: ${SINGLE_THROUGHPUT:-N/A}"
        fi
    else
        BENCHMARK_STATUS["inference"]="FAIL"
        BENCHMARK_METRICS["inference"]="(benchmark failed)"
    fi
    echo ""
fi

# Print detailed summary
print_summary() {
    echo ""
    echo "[4/4] Final Summary"
    echo "╔══════════════════════════════════════════════════════════════════════════╗"
    echo "║                         BENCHMARK RESULTS                                ║"
    echo "╠══════════════════════════════════════════════════════════════════════════╣"
    
    if [ "$RUN_MLPERF" = true ]; then
        local status="${BENCHMARK_STATUS["mlperf"]:-N/A}"
        local metrics="${BENCHMARK_METRICS["mlperf"]:-N/A}"
        local icon="✓"; [ "$status" = "FAIL" ] && icon="✗"
        printf "║  %-74s ║\n" "MLPerf Inference: $status $icon"
        printf "║    %-72s ║\n" "$metrics"
    fi
    
    if [ "$RUN_MMLU" = true ]; then
        local status="${BENCHMARK_STATUS["mmlu"]:-N/A}"
        local metrics="${BENCHMARK_METRICS["mmlu"]:-N/A}"
        local icon="✓"; [ "$status" = "FAIL" ] && icon="✗"
        printf "║  %-74s ║\n" "MMLU-Pro: $status $icon"
        printf "║    %-72s ║\n" "$metrics"
    fi
    
    if [ "$RUN_INFERENCE" = true ]; then
        local status="${BENCHMARK_STATUS["inference"]:-N/A}"
        local metrics="${BENCHMARK_METRICS["inference"]:-N/A}"
        local icon="✓"; [ "$status" = "FAIL" ] && icon="✗"
        printf "║  %-74s ║\n" "LLM Inference: $status $icon"
        printf "║    %-72s ║\n" "$metrics"
    fi
    
    echo "╠══════════════════════════════════════════════════════════════════════════╣"
    printf "║  %-74s ║\n" "Run ID: $RUN_ID"
    printf "║  %-74s ║\n" "Completed: $(date '+%Y-%m-%d %H:%M:%S')"
    printf "║  %-74s ║\n" "Mode: $([ "$SMOKE_TEST" = true ] && echo 'Smoke Test (10 samples)' || echo 'Full Dataset')"
    printf "║  %-74s ║\n" "Results: $RESULTS_DIR"
    echo "╚══════════════════════════════════════════════════════════════════════════╝"
}

print_summary
