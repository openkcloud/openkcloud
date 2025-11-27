package evaluator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// conflictResolver implements ConflictResolver interface
type conflictResolver struct {
	logger types.Logger
}

// NewConflictResolver creates a new conflict resolver
func NewConflictResolver(logger types.Logger) ConflictResolver {
	return &conflictResolver{
		logger: logger,
	}
}

// ResolveConflicts resolves conflicts between evaluation results
func (cr *conflictResolver) ResolveConflicts(ctx context.Context, results []*types.EvaluationResult) (*types.ConflictResolution, error) {
	if len(results) == 0 {
		return &types.ConflictResolution{
			ConflictingPolicies: []string{},
			ResolutionStrategy:  "none",
			SelectedPolicy:      "",
			Reason:              "No policies to resolve",
			Timestamp:           time.Now(),
		}, nil
	}

	if len(results) == 1 {
		return &types.ConflictResolution{
			ConflictingPolicies: []string{},
			ResolutionStrategy:  "none",
			SelectedPolicy:      results[0].PolicyName,
			Reason:              "Only one policy applicable",
			Timestamp:           time.Now(),
		}, nil
	}

	// Detect conflicts first
	conflicts, err := cr.DetectConflicts(ctx, results)
	if err != nil {
		return nil, fmt.Errorf("failed to detect conflicts: %w", err)
	}

	if len(conflicts) == 0 {
		return &types.ConflictResolution{
			ConflictingPolicies: []string{},
			ResolutionStrategy:  "none",
			SelectedPolicy:      results[0].PolicyName,
			Reason:              "No conflicts detected",
			Timestamp:           time.Now(),
		}, nil
	}

	// Resolve conflicts using priority-based strategy
	resolution, err := cr.resolveByPriority(ctx, results, conflicts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve conflicts by priority: %w", err)
	}

	return resolution, nil
}

// DetectConflicts detects conflicts in evaluation results
func (cr *conflictResolver) DetectConflicts(ctx context.Context, results []*types.EvaluationResult) ([]*ConflictInfo, error) {
	var conflicts []*ConflictInfo

	// Check for contradictory recommendations
	conflicts = append(conflicts, cr.detectContradictoryRecommendations(results)...)

	// Check for conflicting constraints
	conflicts = append(conflicts, cr.detectConflictingConstraints(results)...)

	// Check for conflicting violations
	conflicts = append(conflicts, cr.detectConflictingViolations(results)...)

	// Check for conflicting scores
	conflicts = append(conflicts, cr.detectConflictingScores(results)...)

	return conflicts, nil
}

// Health checks the health of the conflict resolver
func (cr *conflictResolver) Health(ctx context.Context) error {
	// Test conflict resolution with sample data
	testResults := []*types.EvaluationResult{
		{
			PolicyID:   "policy1",
			PolicyName: "policy1",
			Score:      0.8,
			Applicable: true,
		},
		{
			PolicyID:   "policy2",
			PolicyName: "policy2",
			Score:      0.6,
			Applicable: true,
		},
	}

	_, err := cr.ResolveConflicts(ctx, testResults)
	if err != nil {
		return fmt.Errorf("conflict resolution test failed: %w", err)
	}

	return nil
}

// Helper methods

// resolveByPriority resolves conflicts using priority-based strategy
func (cr *conflictResolver) resolveByPriority(ctx context.Context, results []*types.EvaluationResult, conflicts []*ConflictInfo) (*types.ConflictResolution, error) {
	// Sort results by policy priority (highest first)
	// Note: In a real implementation, you'd get the actual policy priority from storage
	sort.Slice(results, func(i, j int) bool {
		// For now, use score as a proxy for priority
		return results[i].Score > results[j].Score
	})

	selectedPolicy := results[0].PolicyName
	conflictingPolicies := make([]string, len(results)-1)
	for i := 1; i < len(results); i++ {
		conflictingPolicies[i-1] = results[i].PolicyName
	}

	resolution := &types.ConflictResolution{
		ConflictingPolicies: conflictingPolicies,
		ResolutionStrategy:  "priority_based",
		SelectedPolicy:      selectedPolicy,
		Reason:              fmt.Sprintf("Selected policy with highest priority/score: %.2f", results[0].Score),
		Details: map[string]interface{}{
			"selected_score": results[0].Score,
			"conflict_count": len(conflicts),
			"total_policies": len(results),
		},
		Timestamp: time.Now(),
	}

	cr.logger.Info("resolved policy conflicts using priority-based strategy",
		"selected_policy", selectedPolicy,
		"conflicting_policies", conflictingPolicies,
		"conflict_count", len(conflicts))

	return resolution, nil
}

// detectContradictoryRecommendations detects contradictory recommendations
func (cr *conflictResolver) detectContradictoryRecommendations(results []*types.EvaluationResult) []*ConflictInfo {
	var conflicts []*ConflictInfo

	// Group recommendations by type
	recommendationsByType := make(map[string][]*types.Recommendation)
	policyRecommendations := make(map[string]map[string][]*types.Recommendation)

	for _, result := range results {
		if !result.Applicable {
			continue
		}

		policyRecommendations[result.PolicyName] = make(map[string][]*types.Recommendation)

		for _, rec := range result.Recommendations {
			recommendationsByType[rec.Type] = append(recommendationsByType[rec.Type], &rec)
			policyRecommendations[result.PolicyName][rec.Type] = append(policyRecommendations[result.PolicyName][rec.Type], &rec)
		}
	}

	// Check for contradictory recommendations
	for recType, recommendations := range recommendationsByType {
		if len(recommendations) < 2 {
			continue
		}

		// Check for contradictory actions
		actions := make(map[string][]string) // action -> policies
		for _, rec := range recommendations {
			actions[rec.Action] = append(actions[rec.Action], rec.Message)
		}

		if len(actions) > 1 {
			var conflictingPolicies []string
			for _, policy := range policyRecommendations {
				if policyRecs, exists := policy[recType]; exists && len(policyRecs) > 0 {
					conflictingPolicies = append(conflictingPolicies, policyRecs[0].Message)
				}
			}

			conflict := &ConflictInfo{
				Type:        "contradictory_recommendations",
				Severity:    "medium",
				Policies:    conflictingPolicies,
				Description: fmt.Sprintf("Conflicting recommendations for type '%s'", recType),
				Details: map[string]interface{}{
					"recommendation_type": recType,
					"conflicting_actions": actions,
				},
			}
			conflicts = append(conflicts, conflict)
		}
	}

	return conflicts
}

// detectConflictingConstraints detects conflicting constraints
func (cr *conflictResolver) detectConflictingConstraints(results []*types.EvaluationResult) []*ConflictInfo {
	var conflicts []*ConflictInfo

	// Group constraints by type
	constraintsByType := make(map[string][]*types.Constraint)
	policyConstraints := make(map[string]map[string][]*types.Constraint)

	for _, result := range results {
		if !result.Applicable {
			continue
		}

		policyConstraints[result.PolicyName] = make(map[string][]*types.Constraint)

		for _, constraint := range result.Constraints {
			constraintsByType[constraint.Type] = append(constraintsByType[constraint.Type], &constraint)
			policyConstraints[result.PolicyName][constraint.Type] = append(policyConstraints[result.PolicyName][constraint.Type], &constraint)
		}
	}

	// Check for conflicting constraints
	for constraintType, constraints := range constraintsByType {
		if len(constraints) < 2 {
			continue
		}

		// Check for conflicting values
		values := make(map[interface{}][]string) // value -> policies
		for _, constraint := range constraints {
			values[constraint.Value] = append(values[constraint.Value], constraint.Name)
		}

		if len(values) > 1 {
			var conflictingPolicies []string
			for _, policy := range policyConstraints {
				if policyConstraints, exists := policy[constraintType]; exists && len(policyConstraints) > 0 {
					conflictingPolicies = append(conflictingPolicies, policyConstraints[0].Name)
				}
			}

			conflict := &ConflictInfo{
				Type:        "conflicting_constraints",
				Severity:    "high",
				Policies:    conflictingPolicies,
				Description: fmt.Sprintf("Conflicting constraints for type '%s'", constraintType),
				Details: map[string]interface{}{
					"constraint_type":    constraintType,
					"conflicting_values": values,
				},
			}
			conflicts = append(conflicts, conflict)
		}
	}

	return conflicts
}

// detectConflictingViolations detects conflicting violations
func (cr *conflictResolver) detectConflictingViolations(results []*types.EvaluationResult) []*ConflictInfo {
	var conflicts []*ConflictInfo

	// Check for policies that have conflicting violation assessments
	violationTypes := make(map[string][]*types.Violation)
	policyViolations := make(map[string]map[string][]*types.Violation)

	for _, result := range results {
		if !result.Applicable {
			continue
		}

		policyViolations[result.PolicyName] = make(map[string][]*types.Violation)

		for _, violation := range result.Violations {
			violationTypes[violation.Type] = append(violationTypes[violation.Type], &violation)
			policyViolations[result.PolicyName][violation.Type] = append(policyViolations[result.PolicyName][violation.Type], &violation)
		}
	}

	// Check for conflicting violation assessments
	for violationType, violations := range violationTypes {
		if len(violations) < 2 {
			continue
		}

		// Check for conflicting severity levels
		severities := make(map[string][]string) // severity -> policies
		for _, violation := range violations {
			severities[violation.Severity] = append(severities[violation.Severity], violation.Message)
		}

		if len(severities) > 1 {
			var conflictingPolicies []string
			for _, policy := range policyViolations {
				if policyViolations, exists := policy[violationType]; exists && len(policyViolations) > 0 {
					conflictingPolicies = append(conflictingPolicies, policyViolations[0].Message)
				}
			}

			conflict := &ConflictInfo{
				Type:        "conflicting_violations",
				Severity:    "medium",
				Policies:    conflictingPolicies,
				Description: fmt.Sprintf("Conflicting violation assessments for type '%s'", violationType),
				Details: map[string]interface{}{
					"violation_type":         violationType,
					"conflicting_severities": severities,
				},
			}
			conflicts = append(conflicts, conflict)
		}
	}

	return conflicts
}

// detectConflictingScores detects conflicting scores
func (cr *conflictResolver) detectConflictingScores(results []*types.EvaluationResult) []*ConflictInfo {
	var conflicts []*ConflictInfo

	if len(results) < 2 {
		return conflicts
	}

	// Check for significant score differences
	applicableResults := make([]*types.EvaluationResult, 0)
	for _, result := range results {
		if result.Applicable {
			applicableResults = append(applicableResults, result)
		}
	}

	if len(applicableResults) < 2 {
		return conflicts
	}

	// Find min and max scores
	minScore := applicableResults[0].Score
	maxScore := applicableResults[0].Score
	var minPolicy, maxPolicy string

	for _, result := range applicableResults {
		if result.Score < minScore {
			minScore = result.Score
			minPolicy = result.PolicyName
		}
		if result.Score > maxScore {
			maxScore = result.Score
			maxPolicy = result.PolicyName
		}
	}

	// If there's a significant difference in scores, it might indicate a conflict
	scoreDifference := maxScore - minScore
	if scoreDifference > 0.3 { // Threshold for significant difference
		conflict := &ConflictInfo{
			Type:        "conflicting_scores",
			Severity:    "low",
			Policies:    []string{minPolicy, maxPolicy},
			Description: fmt.Sprintf("Significant score difference between policies: %.2f vs %.2f", minScore, maxScore),
			Details: map[string]interface{}{
				"min_score":  minScore,
				"max_score":  maxScore,
				"difference": scoreDifference,
				"min_policy": minPolicy,
				"max_policy": maxPolicy,
			},
		}
		conflicts = append(conflicts, conflict)
	}

	return conflicts
}
