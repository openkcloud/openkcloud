package automation

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// conditionEvaluator implements ConditionEvaluator interface
type conditionEvaluator struct {
	logger types.Logger
}

// NewConditionEvaluator creates a new condition evaluator
func NewConditionEvaluator(logger types.Logger) ConditionEvaluator {
	return &conditionEvaluator{
		logger: logger,
	}
}

// EvaluateCondition evaluates a condition against context
func (ce *conditionEvaluator) EvaluateCondition(ctx context.Context, condition *Condition, context map[string]interface{}) (bool, error) {
	// Get the value from context
	value, exists := ce.getValueFromContext(context, condition.Field)
	if !exists {
		ce.logger.Debug("field not found in context", "field", condition.Field)
		return false, nil
	}

	// Evaluate condition based on operator
	result, err := ce.evaluateOperator(condition.Operator, value, condition.Value)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate operator %s: %w", condition.Operator, err)
	}

	// Check duration requirement if specified
	if condition.Duration != nil && result {
		return ce.checkDurationRequirement(ctx, condition, context, *condition.Duration)
	}

	return result, nil
}

// EvaluateConditions evaluates multiple conditions
func (ce *conditionEvaluator) EvaluateConditions(ctx context.Context, conditions []*Condition, context map[string]interface{}) (bool, error) {
	if len(conditions) == 0 {
		return true, nil
	}

	// All conditions must be true (AND logic)
	for i, condition := range conditions {
		result, err := ce.EvaluateCondition(ctx, condition, context)
		if err != nil {
			return false, fmt.Errorf("condition %d evaluation failed: %w", i, err)
		}

		if !result {
			ce.logger.Debug("condition not met", "condition_index", i, "field", condition.Field, "operator", condition.Operator)
			return false, nil
		}
	}

	return true, nil
}

// Health checks the health of the condition evaluator
func (ce *conditionEvaluator) Health(ctx context.Context) error {
	// Test basic functionality
	testContext := map[string]interface{}{
		"test_field": "test_value",
		"number":     42,
	}

	testCondition := &Condition{
		Field:    "test_field",
		Operator: OperatorEquals,
		Value:    "test_value",
	}

	result, err := ce.EvaluateCondition(ctx, testCondition, testContext)
	if err != nil {
		return fmt.Errorf("condition evaluation test failed: %w", err)
	}

	if !result {
		return fmt.Errorf("condition evaluation test returned unexpected result")
	}

	return nil
}

// Helper methods

// getValueFromContext gets a value from context using dot notation
func (ce *conditionEvaluator) getValueFromContext(context map[string]interface{}, field string) (interface{}, bool) {
	// Handle dot notation (e.g., "workload.status")
	parts := strings.Split(field, ".")
	current := context

	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part, return the value
			value, exists := current[part]
			return value, exists
		}

		// Navigate deeper
		next, exists := current[part]
		if !exists {
			return nil, false
		}

		nextMap, ok := next.(map[string]interface{})
		if !ok {
			return nil, false
		}

		current = nextMap
	}

	return nil, false
}

// evaluateOperator evaluates a condition based on the operator
func (ce *conditionEvaluator) evaluateOperator(operator string, actualValue, expectedValue interface{}) (bool, error) {
	switch operator {
	case OperatorEquals:
		return ce.equals(actualValue, expectedValue), nil
	case OperatorNotEquals:
		return !ce.equals(actualValue, expectedValue), nil
	case OperatorGreaterThan:
		return ce.greaterThan(actualValue, expectedValue)
	case OperatorLessThan:
		return ce.lessThan(actualValue, expectedValue)
	case OperatorGreaterThanOrEqual:
		result, err := ce.greaterThan(actualValue, expectedValue)
		if err != nil {
			return false, err
		}
		if result {
			return true, nil
		}
		return ce.equals(actualValue, expectedValue), nil
	case OperatorLessThanOrEqual:
		result, err := ce.lessThan(actualValue, expectedValue)
		if err != nil {
			return false, err
		}
		if result {
			return true, nil
		}
		return ce.equals(actualValue, expectedValue), nil
	case OperatorContains:
		return ce.contains(actualValue, expectedValue), nil
	case OperatorNotContains:
		return !ce.contains(actualValue, expectedValue), nil
	case OperatorStartsWith:
		return ce.startsWith(actualValue, expectedValue), nil
	case OperatorEndsWith:
		return ce.endsWith(actualValue, expectedValue), nil
	case OperatorRegex:
		return ce.regexMatch(actualValue, expectedValue)
	case OperatorIn:
		return ce.in(actualValue, expectedValue), nil
	case OperatorNotIn:
		return !ce.in(actualValue, expectedValue), nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", operator)
	}
}

// equals checks if two values are equal
func (ce *conditionEvaluator) equals(actual, expected interface{}) bool {
	// Handle nil values
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}

	// Convert to strings for comparison if types don't match
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return actualStr == expectedStr
}

// greaterThan checks if actual value is greater than expected value
func (ce *conditionEvaluator) greaterThan(actual, expected interface{}) (bool, error) {
	actualNum, err := ce.toNumber(actual)
	if err != nil {
		return false, fmt.Errorf("cannot convert actual value to number: %w", err)
	}

	expectedNum, err := ce.toNumber(expected)
	if err != nil {
		return false, fmt.Errorf("cannot convert expected value to number: %w", err)
	}

	return actualNum > expectedNum, nil
}

// lessThan checks if actual value is less than expected value
func (ce *conditionEvaluator) lessThan(actual, expected interface{}) (bool, error) {
	actualNum, err := ce.toNumber(actual)
	if err != nil {
		return false, fmt.Errorf("cannot convert actual value to number: %w", err)
	}

	expectedNum, err := ce.toNumber(expected)
	if err != nil {
		return false, fmt.Errorf("cannot convert expected value to number: %w", err)
	}

	return actualNum < expectedNum, nil
}

// contains checks if actual value contains expected value
func (ce *conditionEvaluator) contains(actual, expected interface{}) bool {
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return strings.Contains(actualStr, expectedStr)
}

// startsWith checks if actual value starts with expected value
func (ce *conditionEvaluator) startsWith(actual, expected interface{}) bool {
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return strings.HasPrefix(actualStr, expectedStr)
}

// endsWith checks if actual value ends with expected value
func (ce *conditionEvaluator) endsWith(actual, expected interface{}) bool {
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	return strings.HasSuffix(actualStr, expectedStr)
}

// regexMatch checks if actual value matches the regex pattern in expected value
func (ce *conditionEvaluator) regexMatch(actual, expected interface{}) (bool, error) {
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	regex, err := regexp.Compile(expectedStr)
	if err != nil {
		return false, fmt.Errorf("invalid regex pattern: %w", err)
	}

	return regex.MatchString(actualStr), nil
}

// in checks if actual value is in the expected slice
func (ce *conditionEvaluator) in(actual, expected interface{}) bool {
	// Convert expected to slice
	expectedSlice, ok := expected.([]interface{})
	if !ok {
		// Try to convert from other slice types
		switch v := expected.(type) {
		case []string:
			expectedSlice = make([]interface{}, len(v))
			for i, item := range v {
				expectedSlice[i] = item
			}
		case []int:
			expectedSlice = make([]interface{}, len(v))
			for i, item := range v {
				expectedSlice[i] = item
			}
		default:
			return false
		}
	}

	// Check if actual is in the slice
	for _, item := range expectedSlice {
		if ce.equals(actual, item) {
			return true
		}
	}

	return false
}

// toNumber converts a value to a number
func (ce *conditionEvaluator) toNumber(value interface{}) (float64, error) {
	switch v := value.(type) {
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		// Try to parse as number
		var result float64
		_, err := fmt.Sscanf(v, "%f", &result)
		if err != nil {
			return 0, fmt.Errorf("cannot parse string as number: %s", v)
		}
		return result, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to number", value)
	}
}

// checkDurationRequirement checks if a condition has been true for the specified duration
func (ce *conditionEvaluator) checkDurationRequirement(ctx context.Context, condition *Condition, context map[string]interface{}, duration time.Duration) (bool, error) {
	// This is a simplified implementation
	// In a real implementation, you would track the state of conditions over time

	// For now, we'll just return true if the condition is met
	// In practice, you would need to store the timestamp when the condition first became true
	// and check if enough time has passed

	ce.logger.Debug("checking duration requirement", "field", condition.Field, "duration", duration)

	// Simulate duration check - in reality, this would check historical data
	return true, nil
}
