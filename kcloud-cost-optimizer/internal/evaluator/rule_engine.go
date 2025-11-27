package evaluator

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antonmedv/expr"
	"github.com/kcloud-opt/policy/internal/types"
)

// ruleEngine implements RuleEngine interface using expr library
type ruleEngine struct {
	logger types.Logger
}

// NewRuleEngine creates a new rule engine
func NewRuleEngine(logger types.Logger) RuleEngine {
	return &ruleEngine{
		logger: logger,
	}
}

// EvaluateCondition evaluates a condition against context
func (re *ruleEngine) EvaluateCondition(ctx context.Context, condition string, context map[string]interface{}) (bool, error) {
	startTime := time.Now()

	// Validate the condition first
	if err := re.ValidateRule(ctx, condition); err != nil {
		return false, fmt.Errorf("invalid condition: %w", err)
	}

	// Compile the expression
	program, err := expr.Compile(condition, expr.Env(context))
	if err != nil {
		return false, fmt.Errorf("failed to compile condition: %w", err)
	}

	// Evaluate the expression
	result, err := expr.Run(program, context)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate condition: %w", err)
	}

	// Convert result to boolean
	boolean, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("condition did not evaluate to boolean, got: %T", result)
	}

	duration := time.Since(startTime)
	re.logger.WithDuration(duration).Debug("evaluated condition", "condition", condition, "result", boolean)

	return boolean, nil
}

// EvaluateExpression evaluates an expression against context
func (re *ruleEngine) EvaluateExpression(ctx context.Context, expression string, context map[string]interface{}) (interface{}, error) {
	startTime := time.Now()

	// Validate the expression first
	if err := re.ValidateRule(ctx, expression); err != nil {
		return nil, fmt.Errorf("invalid expression: %w", err)
	}

	// Compile the expression
	program, err := expr.Compile(expression, expr.Env(context))
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Evaluate the expression
	result, err := expr.Run(program, context)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate expression: %w", err)
	}

	duration := time.Since(startTime)
	re.logger.WithDuration(duration).Debug("evaluated expression", "expression", expression, "result", result)

	return result, nil
}

// ValidateRule validates a rule for syntax correctness
func (re *ruleEngine) ValidateRule(ctx context.Context, rule string) error {
	if strings.TrimSpace(rule) == "" {
		return fmt.Errorf("rule cannot be empty")
	}

	// Try to compile the rule to check syntax
	_, err := expr.Compile(rule)
	if err != nil {
		return fmt.Errorf("invalid rule syntax: %w", err)
	}

	return nil
}

// Health checks the health of the rule engine
func (re *ruleEngine) Health(ctx context.Context) error {
	// Test basic functionality
	testContext := map[string]interface{}{
		"test":  true,
		"value": 42,
	}

	// Test condition evaluation
	_, err := re.EvaluateCondition(ctx, "test == true", testContext)
	if err != nil {
		return fmt.Errorf("condition evaluation test failed: %w", err)
	}

	// Test expression evaluation
	_, err = re.EvaluateExpression(ctx, "value * 2", testContext)
	if err != nil {
		return fmt.Errorf("expression evaluation test failed: %w", err)
	}

	return nil
}

// Helper functions for common rule patterns

// EvaluateWorkloadCondition evaluates a workload-specific condition
func (re *ruleEngine) EvaluateWorkloadCondition(ctx context.Context, condition string, workload *types.Workload) (bool, error) {
	context := re.buildWorkloadContext(workload)
	return re.EvaluateCondition(ctx, condition, context)
}

// EvaluateClusterCondition evaluates a cluster-specific condition
func (re *ruleEngine) EvaluateClusterCondition(ctx context.Context, condition string, clusterInfo *ClusterInfo) (bool, error) {
	context := re.buildClusterContext(clusterInfo)
	return re.EvaluateCondition(ctx, condition, context)
}

// EvaluateNodeCondition evaluates a node-specific condition
func (re *ruleEngine) EvaluateNodeCondition(ctx context.Context, condition string, nodeInfo *NodeInfo) (bool, error) {
	context := re.buildNodeContext(nodeInfo)
	return re.EvaluateCondition(ctx, condition, context)
}

// buildWorkloadContext builds context for workload evaluation
func (re *ruleEngine) buildWorkloadContext(workload *types.Workload) map[string]interface{} {
	context := map[string]interface{}{
		"workload": map[string]interface{}{
			"id":          workload.ID,
			"name":        workload.Name,
			"type":        string(workload.Type),
			"status":      string(workload.Status),
			"priority":    int(workload.Priority),
			"labels":      workload.Labels,
			"annotations": workload.Annotations,
			"created_at":  workload.CreatedAt,
			"updated_at":  workload.UpdatedAt,
		},
		"requirements": map[string]interface{}{
			"cpu":     workload.Requirements.CPU,
			"memory":  workload.Requirements.Memory,
			"storage": workload.Requirements.Storage,
		},
		"metadata": map[string]interface{}{
			"namespace":   workload.Metadata.Namespace,
			"owner":       workload.Metadata.Owner,
			"team":        workload.Metadata.Team,
			"project":     workload.Metadata.Project,
			"environment": workload.Metadata.Environment,
			"cost_center": workload.Metadata.CostCenter,
		},
	}

	// Add GPU requirements if present
	if workload.Requirements.GPU != nil {
		context["requirements"].(map[string]interface{})["gpu"] = map[string]interface{}{
			"count":  workload.Requirements.GPU.Count,
			"type":   workload.Requirements.GPU.Type,
			"memory": workload.Requirements.GPU.Memory,
		}
	}

	// Add NPU requirements if present
	if workload.Requirements.NPU != nil {
		context["requirements"].(map[string]interface{})["npu"] = map[string]interface{}{
			"count":     workload.Requirements.NPU.Count,
			"type":      workload.Requirements.NPU.Type,
			"memory":    workload.Requirements.NPU.Memory,
			"precision": workload.Requirements.NPU.Precision,
		}
	}

	// Add network requirements if present
	if workload.Requirements.Network != nil {
		context["requirements"].(map[string]interface{})["network"] = map[string]interface{}{
			"bandwidth": workload.Requirements.Network.Bandwidth,
			"latency":   workload.Requirements.Network.Latency,
			"protocols": workload.Requirements.Network.Protocols,
		}
	}

	// Add constraints if present
	if workload.Constraints != nil {
		context["constraints"] = map[string]interface{}{
			"max_cost_per_hour":  workload.Constraints.MaxCostPerHour,
			"max_execution_time": workload.Constraints.MaxExecutionTime,
			"preferred_clusters": workload.Constraints.PreferredClusters,
			"forbidden_clusters": workload.Constraints.ForbiddenClusters,
		}
	}

	return context
}

// buildClusterContext builds context for cluster evaluation
func (re *ruleEngine) buildClusterContext(clusterInfo *ClusterInfo) map[string]interface{} {
	context := map[string]interface{}{
		"cluster": map[string]interface{}{
			"id":          clusterInfo.ID,
			"name":        clusterInfo.Name,
			"type":        clusterInfo.Type,
			"status":      clusterInfo.Status,
			"labels":      clusterInfo.Labels,
			"annotations": clusterInfo.Annotations,
		},
	}

	// Add capacity information
	if clusterInfo.Capacity != nil {
		context["capacity"] = map[string]interface{}{
			"cpu":     clusterInfo.Capacity.CPU,
			"memory":  clusterInfo.Capacity.Memory,
			"storage": clusterInfo.Capacity.Storage,
		}

		if clusterInfo.Capacity.GPU != nil {
			context["capacity"].(map[string]interface{})["gpu"] = map[string]interface{}{
				"count":  clusterInfo.Capacity.GPU.Count,
				"type":   clusterInfo.Capacity.GPU.Type,
				"memory": clusterInfo.Capacity.GPU.Memory,
			}
		}
	}

	// Add allocated information
	if clusterInfo.Allocated != nil {
		context["allocated"] = map[string]interface{}{
			"cpu":     clusterInfo.Allocated.CPU,
			"memory":  clusterInfo.Allocated.Memory,
			"storage": clusterInfo.Allocated.Storage,
		}
	}

	// Add available information
	if clusterInfo.Available != nil {
		context["available"] = map[string]interface{}{
			"cpu":     clusterInfo.Available.CPU,
			"memory":  clusterInfo.Available.Memory,
			"storage": clusterInfo.Available.Storage,
		}
	}

	// Add cost information
	if clusterInfo.Cost != nil {
		context["cost"] = map[string]interface{}{
			"cost_per_hour":   clusterInfo.Cost.CostPerHour,
			"cost_per_cpu":    clusterInfo.Cost.CostPerCPU,
			"cost_per_memory": clusterInfo.Cost.CostPerMemory,
			"cost_per_gpu":    clusterInfo.Cost.CostPerGPU,
			"currency":        clusterInfo.Cost.Currency,
		}
	}

	// Add power information
	if clusterInfo.Power != nil {
		context["power"] = map[string]interface{}{
			"consumption": clusterInfo.Power.PowerConsumption,
			"efficiency":  clusterInfo.Power.PowerEfficiency,
			"limit":       clusterInfo.Power.PowerLimit,
			"unit":        clusterInfo.Power.Unit,
		}
	}

	// Add performance information
	if clusterInfo.Performance != nil {
		context["performance"] = map[string]interface{}{
			"latency":      clusterInfo.Performance.Latency,
			"throughput":   clusterInfo.Performance.Throughput,
			"availability": clusterInfo.Performance.Availability,
			"reliability":  clusterInfo.Performance.Reliability,
		}
	}

	return context
}

// buildNodeContext builds context for node evaluation
func (re *ruleEngine) buildNodeContext(nodeInfo *NodeInfo) map[string]interface{} {
	context := map[string]interface{}{
		"node": map[string]interface{}{
			"id":          nodeInfo.ID,
			"name":        nodeInfo.Name,
			"cluster_id":  nodeInfo.ClusterID,
			"status":      nodeInfo.Status,
			"labels":      nodeInfo.Labels,
			"annotations": nodeInfo.Annotations,
		},
	}

	// Add capacity information
	if nodeInfo.Capacity != nil {
		context["capacity"] = map[string]interface{}{
			"cpu":     nodeInfo.Capacity.CPU,
			"memory":  nodeInfo.Capacity.Memory,
			"storage": nodeInfo.Capacity.Storage,
		}
	}

	// Add allocated information
	if nodeInfo.Allocated != nil {
		context["allocated"] = map[string]interface{}{
			"cpu":     nodeInfo.Allocated.CPU,
			"memory":  nodeInfo.Allocated.Memory,
			"storage": nodeInfo.Allocated.Storage,
		}
	}

	// Add available information
	if nodeInfo.Available != nil {
		context["available"] = map[string]interface{}{
			"cpu":     nodeInfo.Available.CPU,
			"memory":  nodeInfo.Available.Memory,
			"storage": nodeInfo.Available.Storage,
		}
	}

	// Add cost information
	if nodeInfo.Cost != nil {
		context["cost"] = map[string]interface{}{
			"cost_per_hour":   nodeInfo.Cost.CostPerHour,
			"cost_per_cpu":    nodeInfo.Cost.CostPerCPU,
			"cost_per_memory": nodeInfo.Cost.CostPerMemory,
			"currency":        nodeInfo.Cost.Currency,
		}
	}

	// Add power information
	if nodeInfo.Power != nil {
		context["power"] = map[string]interface{}{
			"consumption": nodeInfo.Power.PowerConsumption,
			"efficiency":  nodeInfo.Power.PowerEfficiency,
			"limit":       nodeInfo.Power.PowerLimit,
			"unit":        nodeInfo.Power.Unit,
		}
	}

	// Add performance information
	if nodeInfo.Performance != nil {
		context["performance"] = map[string]interface{}{
			"latency":      nodeInfo.Performance.Latency,
			"throughput":   nodeInfo.Performance.Throughput,
			"availability": nodeInfo.Performance.Availability,
			"reliability":  nodeInfo.Performance.Reliability,
		}
	}

	return context
}

// ParseMemoryString parses memory string (e.g., "1Gi", "512Mi") to bytes
func ParseMemoryString(memoryStr string) (int64, error) {
	if memoryStr == "" {
		return 0, nil
	}

	// Regular expression to match memory format
	re := regexp.MustCompile(`^(\d+)([KMGTPE]?i?)$`)
	matches := re.FindStringSubmatch(strings.ToUpper(memoryStr))

	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid memory format: %s", memoryStr)
	}

	value, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value: %s", matches[1])
	}

	unit := matches[2]
	multiplier := int64(1)

	switch unit {
	case "KI":
		multiplier = 1024
	case "MI":
		multiplier = 1024 * 1024
	case "GI":
		multiplier = 1024 * 1024 * 1024
	case "TI":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "PI":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	case "EI":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	case "K":
		multiplier = 1000
	case "M":
		multiplier = 1000 * 1000
	case "G":
		multiplier = 1000 * 1000 * 1000
	case "T":
		multiplier = 1000 * 1000 * 1000 * 1000
	case "P":
		multiplier = 1000 * 1000 * 1000 * 1000 * 1000
	case "E":
		multiplier = 1000 * 1000 * 1000 * 1000 * 1000 * 1000
	}

	return value * multiplier, nil
}

// Common rule patterns and helpers

// IsWorkloadType checks if workload is of a specific type
func IsWorkloadType(workload *types.Workload, workloadType types.WorkloadType) bool {
	return workload.Type == workloadType
}

// HasLabel checks if workload has a specific label
func HasLabel(workload *types.Workload, key, value string) bool {
	if workload.Labels == nil {
		return false
	}
	return workload.Labels[key] == value
}

// HasAnnotation checks if workload has a specific annotation
func HasAnnotation(workload *types.Workload, key, value string) bool {
	if workload.Annotations == nil {
		return false
	}
	return workload.Annotations[key] == value
}

// MatchesPattern checks if workload name matches a pattern
func MatchesPattern(workload *types.Workload, pattern string) bool {
	return workload.MatchesPattern(pattern)
}

// IsHighPriority checks if workload has high priority
func IsHighPriority(workload *types.Workload) bool {
	return workload.Priority >= types.PriorityHigh
}

// IsCriticalPriority checks if workload has critical priority
func IsCriticalPriority(workload *types.Workload) bool {
	return workload.Priority >= types.PriorityCritical
}
