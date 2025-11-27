package evaluator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// evaluationEngine implements EvaluationEngine interface
type evaluationEngine struct {
	policyEvaluator  PolicyEvaluator
	conflictResolver ConflictResolver
	storage          storage.StorageManager
	logger           types.Logger
}

// NewEvaluationEngine creates a new evaluation engine
func NewEvaluationEngine(policyEvaluator PolicyEvaluator, conflictResolver ConflictResolver, storage storage.StorageManager, logger types.Logger) EvaluationEngine {
	return &evaluationEngine{
		policyEvaluator:  policyEvaluator,
		conflictResolver: conflictResolver,
		storage:          storage,
		logger:           logger,
	}
}

// EvaluateWorkload evaluates a workload against all applicable policies
func (ee *evaluationEngine) EvaluateWorkload(ctx context.Context, workload *types.Workload, options *EvaluationOptions) ([]*types.EvaluationResult, error) {
	startTime := time.Now()

	ee.logger.WithWorkload(workload.ID, string(workload.Type)).Info("starting workload evaluation")

	// Get all active policies
	allPolicies, err := ee.storage.Policy().GetActivePolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active policies: %w", err)
	}

	// Filter applicable policies
	applicablePolicies, err := ee.policyEvaluator.GetApplicablePolicies(ctx, workload, allPolicies)
	if err != nil {
		return nil, fmt.Errorf("failed to get applicable policies: %w", err)
	}

	// Limit policies if specified
	if options != nil && options.MaxPolicies > 0 && len(applicablePolicies) > options.MaxPolicies {
		applicablePolicies = applicablePolicies[:options.MaxPolicies]
	}

	if len(applicablePolicies) == 0 {
		ee.logger.WithWorkload(workload.ID, string(workload.Type)).Info("no applicable policies found")
		return []*types.EvaluationResult{}, nil
	}

	// Evaluate against applicable policies
	results, err := ee.policyEvaluator.Evaluate(ctx, workload, applicablePolicies)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate policies: %w", err)
	}

	// Resolve conflicts if multiple policies apply
	if len(results) > 1 {
		conflictResolution, err := ee.conflictResolver.ResolveConflicts(ctx, results)
		if err != nil {
			ee.logger.WithError(err).Warn("failed to resolve policy conflicts")
		} else if conflictResolution.ResolutionStrategy != "none" {
			ee.logger.Info("policy conflicts resolved",
				"strategy", conflictResolution.ResolutionStrategy,
				"selected_policy", conflictResolution.SelectedPolicy,
				"conflicting_policies", conflictResolution.ConflictingPolicies)
		}
	}

	// Store evaluation results
	for _, result := range results {
		if err := ee.storage.Evaluation().Create(ctx, result); err != nil {
			ee.logger.WithError(err).WithPolicy(result.PolicyID, result.PolicyName).Warn("failed to store evaluation result")
		}
	}

	duration := time.Since(startTime)
	ee.logger.WithWorkload(workload.ID, string(workload.Type)).WithDuration(duration).Info("completed workload evaluation",
		"policies_evaluated", len(results),
		"applicable_policies", len(applicablePolicies))

	return results, nil
}

// EvaluateWithContext evaluates with additional context
func (ee *evaluationEngine) EvaluateWithContext(ctx context.Context, evalCtx *EvaluationContext, options *EvaluationOptions) ([]*types.EvaluationResult, error) {
	if evalCtx.Workload == nil {
		return nil, fmt.Errorf("workload is required in evaluation context")
	}

	// For now, we'll use the workload from context
	// In a more sophisticated implementation, we might use cluster and node information
	// to influence the evaluation
	return ee.EvaluateWorkload(ctx, evalCtx.Workload, options)
}

// GetRecommendedDecision gets the recommended decision based on evaluation results
func (ee *evaluationEngine) GetRecommendedDecision(ctx context.Context, results []*types.EvaluationResult) (*types.Decision, error) {
	if len(results) == 0 {
		return nil, fmt.Errorf("no evaluation results to base decision on")
	}

	// Sort results by score (highest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	bestResult := results[0]
	workloadID := ""

	// Extract workload ID from context (this would be better passed as parameter)
	if len(results) > 0 {
		// In a real implementation, you'd get this from the evaluation context
		workloadID = "unknown-workload"
	}

	decision := &types.Decision{
		ID:         fmt.Sprintf("decision-%s-%d", workloadID, time.Now().UnixNano()),
		Type:       ee.determineDecisionType(bestResult),
		Status:     types.DecisionStatusPending,
		Reason:     ee.determineDecisionReason(bestResult),
		WorkloadID: workloadID,
		PolicyID:   bestResult.PolicyID,
		Confidence: bestResult.Score,
		Score:      bestResult.Score,
		Message:    ee.generateDecisionMessage(bestResult),
		Details:    make(map[string]interface{}),
		Metadata: types.DecisionMetadata{
			Source:    "policy_evaluator",
			Version:   "1.0",
			Timestamp: time.Now(),
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Add evaluation details
	decision.Details["evaluation_results"] = results
	decision.Details["policy_name"] = bestResult.PolicyName
	decision.Details["policy_type"] = bestResult.PolicyType
	decision.Details["violations_count"] = len(bestResult.Violations)
	decision.Details["recommendations_count"] = len(bestResult.Recommendations)

	// Add recommendations to decision
	if len(bestResult.Recommendations) > 0 {
		decision.Details["primary_recommendation"] = bestResult.Recommendations[0]
	}

	ee.logger.Info("generated recommended decision",
		"decision_id", decision.ID,
		"decision_type", decision.Type,
		"policy_id", decision.PolicyID,
		"confidence", decision.Confidence,
		"score", decision.Score)

	return decision, nil
}

// Health checks the health of the evaluation engine
func (ee *evaluationEngine) Health(ctx context.Context) error {
	// Check policy evaluator health
	if err := ee.policyEvaluator.Health(ctx); err != nil {
		return fmt.Errorf("policy evaluator health check failed: %w", err)
	}

	// Check conflict resolver health
	if err := ee.conflictResolver.Health(ctx); err != nil {
		return fmt.Errorf("conflict resolver health check failed: %w", err)
	}

	// Check storage health
	if err := ee.storage.Health(ctx); err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}

	return nil
}

// GetMetrics returns evaluation engine metrics
func (ee *evaluationEngine) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	metrics := map[string]interface{}{
		"engine_type": "policy_evaluation",
		"components": map[string]interface{}{
			"policy_evaluator":  ee.policyEvaluator != nil,
			"conflict_resolver": ee.conflictResolver != nil,
			"storage_manager":   ee.storage != nil,
		},
	}

	// Add policy evaluator metrics if available
	if policyEvaluator, ok := ee.policyEvaluator.(*policyEvaluator); ok {
		metrics["policy_evaluator_metrics"] = map[string]interface{}{
			"evaluations_count": policyEvaluator.evaluationCount,
		}
	}

	return metrics, nil
}

// Helper methods

// determineDecisionType determines the decision type based on evaluation result
func (ee *evaluationEngine) determineDecisionType(result *types.EvaluationResult) types.DecisionType {
	switch result.PolicyType {
	case types.PolicyTypeCostOptimization:
		return types.DecisionTypeOptimize
	case types.PolicyTypeAutomation:
		return types.DecisionTypeSchedule
	case types.PolicyTypeWorkloadPriority:
		return types.DecisionTypeSchedule
	default:
		return types.DecisionTypeSchedule
	}
}

// determineDecisionReason determines the decision reason based on evaluation result
func (ee *evaluationEngine) determineDecisionReason(result *types.EvaluationResult) types.DecisionReason {
	switch result.PolicyType {
	case types.PolicyTypeCostOptimization:
		return types.DecisionReasonCostOptimization
	case types.PolicyTypeAutomation:
		return types.DecisionReasonAutomationRule
	case types.PolicyTypeWorkloadPriority:
		return types.DecisionReasonPolicyCompliance
	default:
		return types.DecisionReasonPolicyCompliance
	}
}

// generateDecisionMessage generates a human-readable message for the decision
func (ee *evaluationEngine) generateDecisionMessage(result *types.EvaluationResult) string {
	if len(result.Recommendations) > 0 {
		return result.Recommendations[0].Message
	}

	if len(result.Violations) > 0 {
		return fmt.Sprintf("Policy violation detected: %s", result.Violations[0].Message)
	}

	return fmt.Sprintf("Policy evaluation completed with score %.2f", result.Score)
}
