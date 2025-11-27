package automation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// ruleExecutor implements RuleExecutor interface
type ruleExecutor struct {
	conditionEvaluator ConditionEvaluator
	actionExecutors    map[string]ActionExecutor
	mu                 sync.RWMutex
	logger             types.Logger
}

// NewRuleExecutor creates a new rule executor
func NewRuleExecutor(conditionEvaluator ConditionEvaluator, logger types.Logger) RuleExecutor {
	return &ruleExecutor{
		conditionEvaluator: conditionEvaluator,
		actionExecutors:    make(map[string]ActionExecutor),
		logger:             logger,
	}
}

// RegisterActionExecutor registers an action executor
func (re *ruleExecutor) RegisterActionExecutor(executor ActionExecutor) {
	re.mu.Lock()
	defer re.mu.Unlock()

	// Register for all action types this executor can handle
	actionTypes := []string{
		ActionTypeNotify,
		ActionTypeScale,
		ActionTypeMigrate,
		ActionTypeTerminate,
		ActionTypeSuspend,
		ActionTypeResume,
		ActionTypeUpdate,
		ActionTypeCreate,
		ActionTypeDelete,
		ActionTypeSchedule,
		ActionTypeReschedule,
		ActionTypeOptimize,
	}

	for _, actionType := range actionTypes {
		if executor.CanExecute(actionType) {
			re.actionExecutors[actionType] = executor
		}
	}
}

// ExecuteRule executes an automation rule
func (re *ruleExecutor) ExecuteRule(ctx context.Context, rule *AutomationRule, contextData map[string]interface{}) (*ExecutionResult, error) {
	startTime := time.Now()

	re.logger.Info("executing automation rule", "rule_id", rule.ID, "rule_name", rule.Name)

	result := &ExecutionResult{
		RuleID:    rule.ID,
		Success:   false,
		Message:   "",
		Duration:  time.Since(startTime),
		Timestamp: startTime,
		Actions:   []*ActionResult{},
		Metadata:  make(map[string]interface{}),
	}

	// Evaluate conditions
	conditionsMet, err := re.conditionEvaluator.EvaluateConditions(ctx, rule.Conditions, contextData)
	if err != nil {
		result.Error = fmt.Sprintf("condition evaluation failed: %v", err)
		result.Duration = time.Since(startTime)
		return result, nil
	}

	if !conditionsMet {
		result.Success = true
		result.Message = "Conditions not met, rule not executed"
		result.Duration = time.Since(startTime)
		re.logger.Debug("rule conditions not met", "rule_id", rule.ID)
		return result, nil
	}

	re.logger.Info("rule conditions met, executing actions", "rule_id", rule.ID, "actions_count", len(rule.Actions))

	// Execute actions
	var actionResults []*ActionResult
	for i, action := range rule.Actions {
		re.logger.Debug("executing action", "rule_id", rule.ID, "action_index", i, "action_type", action.Type)

		actionResult, err := re.executeAction(ctx, action, contextData)
		if err != nil {
			re.logger.WithError(err).Error("action execution failed", "rule_id", rule.ID, "action_index", i)

			actionResult = &ActionResult{
				ActionType: action.Type,
				Success:    false,
				Message:    fmt.Sprintf("Action execution failed: %v", err),
				Duration:   time.Since(startTime),
				Timestamp:  time.Now(),
				Error:      err.Error(),
			}
		}

		actionResults = append(actionResults, actionResult)

		// If action failed and rule should stop on failure, break
		if !actionResult.Success && re.shouldStopOnFailure(rule) {
			re.logger.Warn("stopping rule execution due to action failure", "rule_id", rule.ID, "action_index", i)
			break
		}
	}

	result.Actions = actionResults
	result.Duration = time.Since(startTime)

	// Determine overall success
	allActionsSuccessful := true
	for _, actionResult := range actionResults {
		if !actionResult.Success {
			allActionsSuccessful = false
			break
		}
	}

	result.Success = allActionsSuccessful
	if allActionsSuccessful {
		result.Message = fmt.Sprintf("Rule executed successfully, %d actions completed", len(actionResults))
	} else {
		result.Message = fmt.Sprintf("Rule execution completed with failures, %d actions executed", len(actionResults))
	}

	re.logger.Info("rule execution completed",
		"rule_id", rule.ID,
		"success", result.Success,
		"actions_count", len(actionResults),
		"duration", result.Duration)

	return result, nil
}

// ValidateRule validates an automation rule
func (re *ruleExecutor) ValidateRule(ctx context.Context, rule *AutomationRule) error {
	if rule.ID == "" {
		return fmt.Errorf("rule ID cannot be empty")
	}

	if rule.Name == "" {
		return fmt.Errorf("rule name cannot be empty")
	}

	if len(rule.Conditions) == 0 {
		return fmt.Errorf("rule must have at least one condition")
	}

	if len(rule.Actions) == 0 {
		return fmt.Errorf("rule must have at least one action")
	}

	// Validate conditions
	for i, condition := range rule.Conditions {
		if err := re.validateCondition(condition); err != nil {
			return fmt.Errorf("condition %d validation failed: %w", i, err)
		}
	}

	// Validate actions
	for i, action := range rule.Actions {
		if err := re.validateAction(action); err != nil {
			return fmt.Errorf("action %d validation failed: %w", i, err)
		}
	}

	// Validate schedule if present
	if rule.Schedule != nil {
		if err := re.validateSchedule(rule.Schedule); err != nil {
			return fmt.Errorf("schedule validation failed: %w", err)
		}
	}

	return nil
}

// Health checks the health of the rule executor
func (re *ruleExecutor) Health(ctx context.Context) error {
	// Check condition evaluator health
	if err := re.conditionEvaluator.Health(ctx); err != nil {
		return fmt.Errorf("condition evaluator health check failed: %w", err)
	}

	// Check action executors health
	re.mu.RLock()
	defer re.mu.RUnlock()

	for actionType, executor := range re.actionExecutors {
		if err := executor.Health(ctx); err != nil {
			return fmt.Errorf("action executor health check failed for type %s: %w", actionType, err)
		}
	}

	return nil
}

// Helper methods

// executeAction executes a single action
func (re *ruleExecutor) executeAction(ctx context.Context, action *Action, contextData map[string]interface{}) (*ActionResult, error) {
	startTime := time.Now()

	re.mu.RLock()
	executor, exists := re.actionExecutors[action.Type]
	re.mu.RUnlock()

	if !exists {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("No executor found for action type: %s", action.Type),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      fmt.Sprintf("executor not found for action type %s", action.Type),
		}, nil
	}

	// Execute with timeout if specified
	if action.Timeout != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *action.Timeout)
		defer cancel()
	}

	// Execute action with retry if configured
	if action.Retry != nil {
		return re.executeActionWithRetry(ctx, executor, action, contextData)
	}

	return executor.ExecuteAction(ctx, action)
}

// executeActionWithRetry executes an action with retry logic
func (re *ruleExecutor) executeActionWithRetry(ctx context.Context, executor ActionExecutor, action *Action, contextData map[string]interface{}) (*ActionResult, error) {
	retryConfig := action.Retry
	var lastResult *ActionResult
	var lastErr error

	for attempt := 0; attempt <= retryConfig.MaxRetries; attempt++ {
		result, err := executor.ExecuteAction(ctx, action)
		lastResult = result
		lastErr = err

		// If successful, return result
		if err == nil && result != nil && result.Success {
			result.RetryCount = attempt
			return result, nil
		}

		// If this is the last attempt, don't sleep
		if attempt == retryConfig.MaxRetries {
			break
		}

		// Calculate backoff delay
		delay := re.calculateBackoffDelay(retryConfig, attempt)

		re.logger.Warn("action execution failed, retrying",
			"action_type", action.Type,
			"attempt", attempt+1,
			"max_retries", retryConfig.MaxRetries,
			"delay", delay,
			"error", err)

		// Sleep for the calculated delay
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	// All retries exhausted
	if lastResult != nil {
		lastResult.RetryCount = retryConfig.MaxRetries
		return lastResult, lastErr
	}

	return nil, fmt.Errorf("action execution failed after %d retries: %v", retryConfig.MaxRetries, lastErr)
}

// calculateBackoffDelay calculates the delay for the next retry attempt
func (re *ruleExecutor) calculateBackoffDelay(retryConfig *RetryConfig, attempt int) time.Duration {
	baseDelay := retryConfig.Interval

	switch retryConfig.Backoff {
	case "exponential":
		delay := baseDelay
		for i := 0; i < attempt; i++ {
			delay *= 2
		}
		return delay
	case "linear":
		return baseDelay * time.Duration(attempt+1)
	default: // "fixed"
		return baseDelay
	}
}

// shouldStopOnFailure determines if rule execution should stop on action failure
func (re *ruleExecutor) shouldStopOnFailure(rule *AutomationRule) bool {
	// Check metadata for stop_on_failure setting
	if stopOnFailure, exists := rule.Metadata["stop_on_failure"]; exists {
		if stop, ok := stopOnFailure.(bool); ok {
			return stop
		}
	}

	// Default behavior: continue on failure
	return false
}

// validateCondition validates a condition
func (re *ruleExecutor) validateCondition(condition *Condition) error {
	if condition.Field == "" {
		return fmt.Errorf("condition field cannot be empty")
	}

	if condition.Operator == "" {
		return fmt.Errorf("condition operator cannot be empty")
	}

	// Validate operator
	validOperators := []string{
		OperatorEquals, OperatorNotEquals, OperatorGreaterThan, OperatorLessThan,
		OperatorGreaterThanOrEqual, OperatorLessThanOrEqual, OperatorContains,
		OperatorNotContains, OperatorStartsWith, OperatorEndsWith, OperatorRegex,
		OperatorIn, OperatorNotIn,
	}

	valid := false
	for _, op := range validOperators {
		if condition.Operator == op {
			valid = true
			break
		}
	}

	if !valid {
		return fmt.Errorf("invalid condition operator: %s", condition.Operator)
	}

	return nil
}

// validateAction validates an action
func (re *ruleExecutor) validateAction(action *Action) error {
	if action.Type == "" {
		return fmt.Errorf("action type cannot be empty")
	}

	// Check if executor exists for this action type
	re.mu.RLock()
	_, exists := re.actionExecutors[action.Type]
	re.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no executor found for action type: %s", action.Type)
	}

	return nil
}

// validateSchedule validates a schedule
func (re *ruleExecutor) validateSchedule(schedule *Schedule) error {
	// Validate cron expression if present
	if schedule.Cron != "" {
		// In a real implementation, you would validate the cron expression
		// For now, we'll do a basic check
		if len(schedule.Cron) < 5 {
			return fmt.Errorf("invalid cron expression: %s", schedule.Cron)
		}
	}

	// Validate interval if present
	if schedule.Interval != "" {
		// In a real implementation, you would parse and validate the interval
		// For now, we'll do a basic check
		if len(schedule.Interval) < 2 {
			return fmt.Errorf("invalid interval: %s", schedule.Interval)
		}
	}

	// Validate time window
	if !schedule.StartTime.IsZero() && !schedule.EndTime.IsZero() {
		if schedule.StartTime.After(schedule.EndTime) {
			return fmt.Errorf("start time cannot be after end time")
		}
	}

	return nil
}
