package evaluator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// policyEvaluator implements PolicyEvaluator interface
type policyEvaluator struct {
	storage         storage.StorageManager
	ruleEngine      RuleEngine
	logger          types.Logger
	evaluationCount int64
}

// NewPolicyEvaluator creates a new policy evaluator
func NewPolicyEvaluator(storage storage.StorageManager, ruleEngine RuleEngine, logger types.Logger) PolicyEvaluator {
	return &policyEvaluator{
		storage:    storage,
		ruleEngine: ruleEngine,
		logger:     logger,
	}
}

// Evaluate evaluates a workload against applicable policies
func (e *policyEvaluator) Evaluate(ctx context.Context, workload *types.Workload, policies []types.Policy) ([]*types.EvaluationResult, error) {
	startTime := time.Now()
	e.evaluationCount++

	e.logger.WithWorkload(workload.ID, string(workload.Type)).Info("starting policy evaluation")

	var results []*types.EvaluationResult

	for _, policy := range policies {
		result, err := e.EvaluateSingle(ctx, workload, policy)
		if err != nil {
			e.logger.WithError(err).WithPolicy(policy.GetMetadata().Name, "").Error("failed to evaluate policy")
			continue
		}

		results = append(results, result)
	}

	duration := time.Since(startTime)
	e.logger.WithWorkload(workload.ID, string(workload.Type)).WithDuration(duration).Info("completed policy evaluation",
		"policies_evaluated", len(results))

	return results, nil
}

// EvaluateSingle evaluates a workload against a single policy
func (e *policyEvaluator) EvaluateSingle(ctx context.Context, workload *types.Workload, policy types.Policy) (*types.EvaluationResult, error) {
	startTime := time.Now()
	metadata := policy.GetMetadata()

	result := &types.EvaluationResult{
		PolicyID:        metadata.Name,
		PolicyName:      metadata.Name,
		PolicyType:      policy.GetType(),
		Applicable:      false,
		Score:           0.0,
		Violations:      []types.Violation{},
		Recommendations: []types.Recommendation{},
		Constraints:     []types.Constraint{},
		Metrics:         make(map[string]interface{}),
		Timestamp:       time.Now(),
	}

	// Check if policy is applicable to the workload
	applicable := e.isPolicyApplicable(ctx, workload, policy)

	if !applicable {
		result.Applicable = false
		result.Duration = time.Since(startTime)
		return result, nil
	}

	result.Applicable = true

	// Evaluate policy based on type
	var err error
	switch policy.GetType() {
	case types.PolicyTypeCostOptimization:
		err = e.evaluateCostOptimizationPolicy(ctx, workload, policy, result)
	case types.PolicyTypeAutomation:
		err = e.evaluateAutomationPolicy(ctx, workload, policy, result)
	case types.PolicyTypeWorkloadPriority:
		err = e.evaluateWorkloadPriorityPolicy(ctx, workload, policy, result)
	default:
		err = fmt.Errorf("unsupported policy type: %s", policy.GetType())
	}

	if err != nil {
		return nil, fmt.Errorf("failed to evaluate policy: %w", err)
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// GetApplicablePolicies returns policies that are applicable to a workload
func (e *policyEvaluator) GetApplicablePolicies(ctx context.Context, workload *types.Workload, allPolicies []types.Policy) ([]types.Policy, error) {
	var applicablePolicies []types.Policy

	for _, policy := range allPolicies {
		applicable := e.isPolicyApplicable(ctx, workload, policy)

		if applicable {
			applicablePolicies = append(applicablePolicies, policy)
		}
	}

	// Sort by priority (highest first)
	sort.Slice(applicablePolicies, func(i, j int) bool {
		return applicablePolicies[i].GetPriority() > applicablePolicies[j].GetPriority()
	})

	return applicablePolicies, nil
}

// ValidatePolicy validates a policy for correctness
func (e *policyEvaluator) ValidatePolicy(ctx context.Context, policy types.Policy) error {
	metadata := policy.GetMetadata()

	// Basic validation
	if metadata.Name == "" {
		return types.ErrInvalidPolicyName
	}

	if policy.GetPriority() <= 0 {
		return types.ErrInvalidPriority
	}

	// Type-specific validation
	switch policy.GetType() {
	case types.PolicyTypeCostOptimization:
		return e.validateCostOptimizationPolicy(ctx, policy)
	case types.PolicyTypeAutomation:
		return e.validateAutomationPolicy(ctx, policy)
	case types.PolicyTypeWorkloadPriority:
		return e.validateWorkloadPriorityPolicy(ctx, policy)
	default:
		return types.ErrInvalidPolicyType
	}
}

// Health checks the health of the evaluator
func (e *policyEvaluator) Health(ctx context.Context) error {
	// Check storage health
	if err := e.storage.Health(ctx); err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}

	// Check rule engine health
	if err := e.ruleEngine.Health(ctx); err != nil {
		return fmt.Errorf("rule engine health check failed: %w", err)
	}

	return nil
}

// Helper methods

// isPolicyApplicable checks if a policy is applicable to a workload
func (e *policyEvaluator) isPolicyApplicable(ctx context.Context, workload *types.Workload, policy types.Policy) bool {
	metadata := policy.GetMetadata()

	// Check if policy is active
	if policy.GetStatus() != types.PolicyStatusActive {
		return false
	}

	// Check namespace match
	if metadata.Namespace != "" && workload.Metadata.Namespace != metadata.Namespace {
		return false
	}

	// Check label selectors
	if len(metadata.Labels) > 0 {
		if !e.matchesLabelSelectors(workload.Labels, metadata.Labels) {
			return false
		}
	}

	// Type-specific applicability checks
	switch policy.GetType() {
	case types.PolicyTypeCostOptimization:
		return e.isCostOptimizationPolicyApplicable(ctx, workload, policy)
	case types.PolicyTypeAutomation:
		return e.isAutomationPolicyApplicable(ctx, workload, policy)
	case types.PolicyTypeWorkloadPriority:
		return e.isWorkloadPriorityPolicyApplicable(ctx, workload, policy)
	}

	return true
}

// matchesLabelSelectors checks if workload labels match policy label selectors
func (e *policyEvaluator) matchesLabelSelectors(workloadLabels map[string]string, policyLabels map[string]string) bool {
	if workloadLabels == nil {
		workloadLabels = make(map[string]string)
	}

	for key, value := range policyLabels {
		if workloadLabels[key] != value {
			return false
		}
	}

	return true
}

// isCostOptimizationPolicyApplicable checks if cost optimization policy is applicable
func (e *policyEvaluator) isCostOptimizationPolicyApplicable(ctx context.Context, workload *types.Workload, policy types.Policy) bool {
	// Cost optimization policies are generally applicable to all workloads
	// unless specific exclusions are defined
	return true
}

// isAutomationPolicyApplicable checks if automation policy is applicable
func (e *policyEvaluator) isAutomationPolicyApplicable(ctx context.Context, workload *types.Workload, policy types.Policy) bool {
	// Automation policies are evaluated based on conditions
	// This is a simplified check - in practice, you'd evaluate the actual conditions
	return true
}

// isWorkloadPriorityPolicyApplicable checks if workload priority policy is applicable
func (e *policyEvaluator) isWorkloadPriorityPolicyApplicable(ctx context.Context, workload *types.Workload, policy types.Policy) bool {
	// Workload priority policies are generally applicable to all workloads
	return true
}

// evaluateCostOptimizationPolicy evaluates a cost optimization policy
func (e *policyEvaluator) evaluateCostOptimizationPolicy(ctx context.Context, workload *types.Workload, policy types.Policy, result *types.EvaluationResult) error {
	// This is a simplified evaluation - in practice, you'd have more sophisticated logic

	// Calculate cost score based on workload requirements and constraints
	costScore := e.calculateCostScore(workload, policy)
	result.Score = costScore

	// Check for violations
	if costScore < 0.5 {
		violation := types.Violation{
			Type:      "cost_optimization",
			Severity:  "warning",
			Message:   "Workload may not be cost-optimized",
			Field:     "cost_score",
			Value:     costScore,
			Expected:  0.5,
			Timestamp: time.Now(),
		}
		result.Violations = append(result.Violations, violation)
	}

	// Add recommendations
	if costScore < 0.7 {
		recommendation := types.Recommendation{
			Type:      "cost_optimization",
			Priority:  "high",
			Message:   "Consider optimizing workload for better cost efficiency",
			Action:    "review_resource_requirements",
			Impact:    "cost_reduction",
			Effort:    "medium",
			Timestamp: time.Now(),
		}
		result.Recommendations = append(result.Recommendations, recommendation)
	}

	// Add metrics
	result.Metrics["cost_score"] = costScore
	result.Metrics["evaluation_type"] = "cost_optimization"

	return nil
}

// evaluateAutomationPolicy evaluates an automation policy
func (e *policyEvaluator) evaluateAutomationPolicy(ctx context.Context, workload *types.Workload, policy types.Policy, result *types.EvaluationResult) error {
	// This is a simplified evaluation for automation policies

	// Check if automation conditions are met
	conditionsMet := e.checkAutomationConditions(ctx, workload, policy)

	if conditionsMet {
		result.Score = 1.0

		// Add recommendation for automation action
		recommendation := types.Recommendation{
			Type:      "automation",
			Priority:  "high",
			Message:   "Automation conditions met - action should be triggered",
			Action:    "trigger_automation",
			Impact:    "operational_efficiency",
			Effort:    "low",
			Timestamp: time.Now(),
		}
		result.Recommendations = append(result.Recommendations, recommendation)
	} else {
		result.Score = 0.0
	}

	result.Metrics["conditions_met"] = conditionsMet
	result.Metrics["evaluation_type"] = "automation"

	return nil
}

// evaluateWorkloadPriorityPolicy evaluates a workload priority policy
func (e *policyEvaluator) evaluateWorkloadPriorityPolicy(ctx context.Context, workload *types.Workload, policy types.Policy, result *types.EvaluationResult) error {
	// This is a simplified evaluation for workload priority policies

	// Calculate priority score based on workload characteristics
	priorityScore := e.calculatePriorityScore(workload, policy)
	result.Score = priorityScore

	// Check if workload priority matches policy recommendations
	if workload.Priority != types.Priority(priorityScore*1000) {
		recommendation := types.Recommendation{
			Type:      "priority_adjustment",
			Priority:  "medium",
			Message:   "Consider adjusting workload priority",
			Action:    "update_priority",
			Impact:    "scheduling_efficiency",
			Effort:    "low",
			Timestamp: time.Now(),
		}
		result.Recommendations = append(result.Recommendations, recommendation)
	}

	result.Metrics["priority_score"] = priorityScore
	result.Metrics["current_priority"] = workload.Priority
	result.Metrics["evaluation_type"] = "workload_priority"

	return nil
}

// calculateCostScore calculates a cost optimization score for a workload
func (e *policyEvaluator) calculateCostScore(workload *types.Workload, policy types.Policy) float64 {

	score := 0.5 // Base score

	// Adjust based on workload type
	switch workload.Type {
	case types.WorkloadTypeMLTraining:
		score += 0.2 // ML training workloads can benefit from cost optimization
	case types.WorkloadTypeInference:
		score += 0.3 // Inference workloads are typically cost-sensitive
	case types.WorkloadTypeBatch:
		score += 0.4 // Batch workloads are ideal for cost optimization
	default:
		score += 0.1
	}

	// Adjust based on resource requirements
	if workload.Requirements.GPU != nil && workload.Requirements.GPU.Count > 0 {
		score -= 0.1 // GPU workloads are typically more expensive
	}

	// Ensure score is between 0 and 1
	if score > 1.0 {
		score = 1.0
	}
	if score < 0.0 {
		score = 0.0
	}

	return score
}

// checkAutomationConditions checks if automation conditions are met
func (e *policyEvaluator) checkAutomationConditions(ctx context.Context, workload *types.Workload, policy types.Policy) bool {
	return true
}

// calculatePriorityScore calculates a priority score for a workload
func (e *policyEvaluator) calculatePriorityScore(workload *types.Workload, policy types.Policy) float64 {

	score := 0.5 // Base score

	// Adjust based on workload type
	switch workload.Type {
	case types.WorkloadTypeRealTime:
		score = 0.9 // Real-time workloads typically have high priority
	case types.WorkloadTypeInference:
		score = 0.8 // Inference workloads often need quick response
	case types.WorkloadTypeMLTraining:
		score = 0.6 // Training workloads can be lower priority
	case types.WorkloadTypeBatch:
		score = 0.3 // Batch workloads typically have low priority
	default:
		score = 0.5
	}

	// Adjust based on current priority
	currentPriority := float64(workload.Priority)
	if currentPriority > 500 {
		score += 0.2
	} else if currentPriority < 100 {
		score -= 0.2
	}

	// Ensure score is between 0 and 1
	if score > 1.0 {
		score = 1.0
	}
	if score < 0.0 {
		score = 0.0
	}

	return score
}

// validateCostOptimizationPolicy validates a cost optimization policy
func (e *policyEvaluator) validateCostOptimizationPolicy(ctx context.Context, policy types.Policy) error {
	// Basic validation for cost optimization policies
	// In practice, this would validate the policy structure and constraints

	return nil
}

// validateAutomationPolicy validates an automation policy
func (e *policyEvaluator) validateAutomationPolicy(ctx context.Context, policy types.Policy) error {
	// Basic validation for automation policies
	// In practice, this would validate the automation rules and conditions

	return nil
}

// validateWorkloadPriorityPolicy validates a workload priority policy
func (e *policyEvaluator) validateWorkloadPriorityPolicy(ctx context.Context, policy types.Policy) error {
	// Basic validation for workload priority policies
	// In practice, this would validate the priority class definitions

	return nil
}
