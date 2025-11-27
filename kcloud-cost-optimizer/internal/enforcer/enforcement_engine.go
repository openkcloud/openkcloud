package enforcer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// enforcementEngine implements EnforcementEngine interface
type enforcementEngine struct {
	executors map[string]ActionExecutor
	mu        sync.RWMutex
	logger    types.Logger
}

// NewEnforcementEngine creates a new enforcement engine
func NewEnforcementEngine(logger types.Logger) EnforcementEngine {
	return &enforcementEngine{
		executors: make(map[string]ActionExecutor),
		logger:    logger,
	}
}

// RegisterExecutor registers an action executor
func (ee *enforcementEngine) RegisterExecutor(executor ActionExecutor) error {
	ee.mu.Lock()
	defer ee.mu.Unlock()

	// Find action types this executor can handle
	// For now, we'll use a simple approach - in practice, you'd have a method
	// to get the supported action types from the executor
	actionTypes := []string{
		ActionTypeSchedule,
		ActionTypeReschedule,
		ActionTypeMigrate,
		ActionTypeScale,
		ActionTypeTerminate,
		ActionTypeSuspend,
		ActionTypeResume,
		ActionTypeNotify,
		ActionTypeUpdate,
		ActionTypeDelete,
		ActionTypeCreate,
	}

	for _, actionType := range actionTypes {
		if executor.CanExecute(actionType) {
			ee.executors[actionType] = executor
			ee.logger.Info("registered action executor", "action_type", actionType)
		}
	}

	return nil
}

// UnregisterExecutor unregisters an action executor
func (ee *enforcementEngine) UnregisterExecutor(actionType string) error {
	ee.mu.Lock()
	defer ee.mu.Unlock()

	if _, exists := ee.executors[actionType]; !exists {
		return fmt.Errorf("executor for action type %s not found", actionType)
	}

	delete(ee.executors, actionType)
	ee.logger.Info("unregistered action executor", "action_type", actionType)

	return nil
}

// ExecuteAction executes a single action
func (ee *enforcementEngine) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	ee.mu.RLock()
	executor, exists := ee.executors[action.Type]
	ee.mu.RUnlock()

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

	// Validate action
	if err := executor.Validate(action); err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Action validation failed: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Set timeout if not specified
	if action.Timeout == 0 {
		action.Timeout = 5 * time.Minute
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, action.Timeout)
	defer cancel()

	// Execute with retry if retry policy is specified
	var result *ActionResult
	var err error

	if action.RetryPolicy != nil {
		result, err = ee.executeWithRetry(ctx, executor, action)
	} else {
		result, err = executor.Execute(ctx, action)
	}

	if err != nil {
		result = &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Action execution failed: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}
	}

	// Ensure duration is set
	result.Duration = time.Since(startTime)

	ee.logger.Info("executed action",
		"action_type", action.Type,
		"target", action.Target,
		"success", result.Success,
		"duration", result.Duration,
		"retry_count", result.RetryCount)

	return result, nil
}

// ExecuteActions executes multiple actions
func (ee *enforcementEngine) ExecuteActions(ctx context.Context, actions []*Action) ([]*ActionResult, error) {
	var results []*ActionResult
	var wg sync.WaitGroup
	resultChan := make(chan *ActionResult, len(actions))

	// Execute actions concurrently
	for _, action := range actions {
		wg.Add(1)
		go func(a *Action) {
			defer wg.Done()
			result, err := ee.ExecuteAction(ctx, a)
			if err != nil {
				result = &ActionResult{
					ActionType: a.Type,
					Success:    false,
					Message:    fmt.Sprintf("Action execution error: %v", err),
					Timestamp:  time.Now(),
					Error:      err.Error(),
				}
			}
			resultChan <- result
		}(action)
	}

	// Wait for all actions to complete
	wg.Wait()
	close(resultChan)

	// Collect results
	for result := range resultChan {
		results = append(results, result)
	}

	return results, nil
}

// Health checks the health of the enforcement engine
func (ee *enforcementEngine) Health(ctx context.Context) error {
	ee.mu.RLock()
	defer ee.mu.RUnlock()

	// Check health of all registered executors
	for actionType, executor := range ee.executors {
		if err := executor.Health(ctx); err != nil {
			return fmt.Errorf("executor health check failed for action type %s: %w", actionType, err)
		}
	}

	return nil
}

// executeWithRetry executes an action with retry logic
func (ee *enforcementEngine) executeWithRetry(ctx context.Context, executor ActionExecutor, action *Action) (*ActionResult, error) {
	retryPolicy := action.RetryPolicy
	var lastResult *ActionResult
	var lastErr error

	for attempt := 0; attempt <= retryPolicy.MaxRetries; attempt++ {
		result, err := executor.Execute(ctx, action)
		lastResult = result
		lastErr = err

		// If successful, return result
		if err == nil && result != nil && result.Success {
			result.RetryCount = attempt
			return result, nil
		}

		// If this is the last attempt, don't sleep
		if attempt == retryPolicy.MaxRetries {
			break
		}

		// Calculate backoff delay
		delay := ee.calculateBackoffDelay(retryPolicy, attempt)

		ee.logger.Warn("action execution failed, retrying",
			"action_type", action.Type,
			"attempt", attempt+1,
			"max_retries", retryPolicy.MaxRetries,
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
		lastResult.RetryCount = retryPolicy.MaxRetries
		return lastResult, lastErr
	}

	return nil, fmt.Errorf("action execution failed after %d retries: %v", retryPolicy.MaxRetries, lastErr)
}

// calculateBackoffDelay calculates the delay for the next retry attempt
func (ee *enforcementEngine) calculateBackoffDelay(retryPolicy *RetryPolicy, attempt int) time.Duration {
	baseDelay := retryPolicy.Interval

	switch retryPolicy.Backoff {
	case BackoffTypeLinear:
		return baseDelay * time.Duration(attempt+1)
	case BackoffTypeExponential:
		delay := baseDelay
		for i := 0; i < attempt; i++ {
			delay *= 2
		}
		return delay
	case BackoffTypeFixed:
		return baseDelay
	default:
		return baseDelay
	}
}
