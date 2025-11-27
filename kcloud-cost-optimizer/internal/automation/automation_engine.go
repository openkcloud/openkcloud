package automation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// automationEngine implements AutomationEngine interface
type automationEngine struct {
	storage            storage.StorageManager
	ruleExecutor       RuleExecutor
	conditionEvaluator ConditionEvaluator
	scheduler          Scheduler
	eventHandlers      map[string]EventHandler
	actionExecutors    map[string]ActionExecutor
	rules              map[string]*AutomationRule
	ruleStatuses       map[string]*RuleStatus
	running            bool
	mu                 sync.RWMutex
	logger             types.Logger
	stopChan           chan struct{}
}

// NewAutomationEngine creates a new automation engine
func NewAutomationEngine(
	storage storage.StorageManager,
	ruleExecutor RuleExecutor,
	conditionEvaluator ConditionEvaluator,
	scheduler Scheduler,
	logger types.Logger,
) AutomationEngine {
	return &automationEngine{
		storage:            storage,
		ruleExecutor:       ruleExecutor,
		conditionEvaluator: conditionEvaluator,
		scheduler:          scheduler,
		eventHandlers:      make(map[string]EventHandler),
		actionExecutors:    make(map[string]ActionExecutor),
		rules:              make(map[string]*AutomationRule),
		ruleStatuses:       make(map[string]*RuleStatus),
		running:            false,
		logger:             logger,
		stopChan:           make(chan struct{}),
	}
}

// CreateRule creates a new automation rule
func (ae *automationEngine) CreateRule(ctx context.Context, rule *AutomationRule) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if err := rule.Validate(); err != nil {
		return fmt.Errorf("invalid rule: %w", err)
	}

	ae.rules[rule.ID] = rule
	ae.ruleStatuses[rule.ID] = &RuleStatus{
		RuleID:      rule.ID,
		Status:      "registered",
		LastChecked: time.Now(),
		CreatedAt:   time.Now(),
	}

	return nil
}

// UpdateRule updates an existing automation rule
func (ae *automationEngine) UpdateRule(ctx context.Context, rule *AutomationRule) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if err := rule.Validate(); err != nil {
		return fmt.Errorf("invalid rule: %w", err)
	}

	if _, exists := ae.rules[rule.ID]; !exists {
		return fmt.Errorf("rule not found: %s", rule.ID)
	}

	ae.rules[rule.ID] = rule
	ae.ruleStatuses[rule.ID].LastUpdated = time.Now()

	return nil
}

// DeleteRule deletes an automation rule
func (ae *automationEngine) DeleteRule(ctx context.Context, ruleID string) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if _, exists := ae.rules[ruleID]; !exists {
		return fmt.Errorf("rule not found: %s", ruleID)
	}

	delete(ae.rules, ruleID)
	delete(ae.ruleStatuses, ruleID)

	return nil
}

// GetRule gets an automation rule by ID
func (ae *automationEngine) GetRule(ctx context.Context, ruleID string) (*AutomationRule, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	rule, exists := ae.rules[ruleID]
	if !exists {
		return nil, fmt.Errorf("rule not found: %s", ruleID)
	}

	return rule, nil
}

// ListRules lists all automation rules
func (ae *automationEngine) ListRules(ctx context.Context) ([]*AutomationRule, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	rules := make([]*AutomationRule, 0, len(ae.rules))
	for _, rule := range ae.rules {
		rules = append(rules, rule)
	}

	return rules, nil
}

// EnableRule enables an automation rule
func (ae *automationEngine) EnableRule(ctx context.Context, ruleID string) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	rule, exists := ae.rules[ruleID]
	if !exists {
		return fmt.Errorf("rule not found: %s", ruleID)
	}

	rule.Enabled = true
	ae.ruleStatuses[ruleID].LastUpdated = time.Now()

	return nil
}

// DisableRule disables an automation rule
func (ae *automationEngine) DisableRule(ctx context.Context, ruleID string) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	rule, exists := ae.rules[ruleID]
	if !exists {
		return fmt.Errorf("rule not found: %s", ruleID)
	}

	rule.Enabled = false
	ae.ruleStatuses[ruleID].LastUpdated = time.Now()

	return nil
}

// Initialize initializes the automation engine
func (ae *automationEngine) Initialize(ctx context.Context) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	// Initialize components
	if ae.ruleExecutor != nil {
		// Initialize rule executor if needed
	}

	if ae.conditionEvaluator != nil {
		// Initialize condition evaluator if needed
	}

	if ae.scheduler != nil {
		// Initialize scheduler if needed
	}

	return nil
}

// GetMetrics returns automation engine metrics
func (ae *automationEngine) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	metrics := map[string]interface{}{
		"total_rules":    len(ae.rules),
		"enabled_rules":  0,
		"disabled_rules": 0,
		"running":        ae.running,
		"rule_statuses":  len(ae.ruleStatuses),
	}

	// Count enabled/disabled rules
	for _, rule := range ae.rules {
		if rule.Enabled {
			metrics["enabled_rules"] = metrics["enabled_rules"].(int) + 1
		} else {
			metrics["disabled_rules"] = metrics["disabled_rules"].(int) + 1
		}
	}

	return metrics, nil
}

// CountRules returns the count of automation rules
func (ae *automationEngine) CountRules(ctx context.Context) (int64, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	if !ae.running {
		return 0, fmt.Errorf("automation engine is not running")
	}

	return int64(len(ae.rules)), nil
}

// ExecuteRule executes a specific rule
func (ae *automationEngine) ExecuteRule(ctx context.Context, ruleID string, context map[string]interface{}) error {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	if !ae.running {
		return fmt.Errorf("automation engine is not running")
	}

	rule, exists := ae.rules[ruleID]
	if !exists {
		return fmt.Errorf("rule not found: %s", ruleID)
	}

	if !rule.Enabled {
		return fmt.Errorf("rule is disabled: %s", ruleID)
	}

	ae.logger.Info("executing rule", "rule_id", ruleID)

	// Update execution count in status
	if status, exists := ae.ruleStatuses[ruleID]; exists {
		status.ExecutionCount++
		status.LastExecuted = &time.Time{}
	}

	return nil
}

// GetRuleHistory gets the execution history of a rule
func (ae *automationEngine) GetRuleHistory(ctx context.Context, ruleID string, filters *RuleFilters) ([]*RuleExecution, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	if !ae.running {
		return nil, fmt.Errorf("automation engine is not running")
	}

	return []*RuleExecution{}, nil
}

// GetStatistics gets automation engine statistics
func (ae *automationEngine) GetStatistics(ctx context.Context, filters *RuleFilters) (map[string]interface{}, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	if !ae.running {
		return nil, fmt.Errorf("automation engine is not running")
	}

	stats := map[string]interface{}{
		"total_rules":    len(ae.rules),
		"enabled_rules":  0,
		"disabled_rules": 0,
		"executions":     0,
	}

	// Count enabled/disabled rules
	for _, rule := range ae.rules {
		if rule.Enabled {
			stats["enabled_rules"] = stats["enabled_rules"].(int) + 1
		} else {
			stats["disabled_rules"] = stats["disabled_rules"].(int) + 1
		}
	}

	// Count total executions
	for _, status := range ae.ruleStatuses {
		stats["executions"] = stats["executions"].(int) + int(status.ExecutionCount)
	}

	return stats, nil
}

// Start starts the automation engine
func (ae *automationEngine) Start(ctx context.Context) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if ae.running {
		return fmt.Errorf("automation engine is already running")
	}

	ae.logger.Info("starting automation engine")

	// Load existing rules from storage
	if err := ae.loadRules(ctx); err != nil {
		return fmt.Errorf("failed to load rules: %w", err)
	}

	// Register rules with scheduler
	for _, rule := range ae.rules {
		if rule.Enabled && rule.Schedule != nil {
			if err := ae.scheduler.ScheduleRule(ctx, rule); err != nil {
				ae.logger.WithError(err).WithPolicy(rule.ID, rule.Name).Warn("failed to schedule rule")
			}
		}
	}

	ae.running = true

	// Start event processing loop
	go ae.eventProcessingLoop(ctx)

	ae.logger.Info("automation engine started", "rules_count", len(ae.rules))

	return nil
}

// Stop stops the automation engine
func (ae *automationEngine) Stop(ctx context.Context) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	if !ae.running {
		return fmt.Errorf("automation engine is not running")
	}

	ae.logger.Info("stopping automation engine")

	// Signal stop
	close(ae.stopChan)

	// Unschedule all rules
	for ruleID := range ae.rules {
		if err := ae.scheduler.UnscheduleRule(ctx, ruleID); err != nil {
			ae.logger.WithError(err).Warn("failed to unschedule rule", "rule_id", ruleID)
		}
	}

	ae.running = false

	ae.logger.Info("automation engine stopped")

	return nil
}

// RegisterRule registers an automation rule
func (ae *automationEngine) RegisterRule(ctx context.Context, rule *AutomationRule) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	// Validate rule
	if err := ae.ruleExecutor.ValidateRule(ctx, rule); err != nil {
		return fmt.Errorf("rule validation failed: %w", err)
	}

	// Set timestamps
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now

	// Store rule
	ae.rules[rule.ID] = rule

	// Initialize rule status
	ae.ruleStatuses[rule.ID] = &RuleStatus{
		RuleID:         rule.ID,
		Name:           rule.Name,
		Status:         RuleStatusActive,
		ExecutionCount: 0,
		SuccessCount:   0,
		FailureCount:   0,
		Metadata:       make(map[string]interface{}),
	}

	// Schedule rule if it has a schedule and is enabled
	if rule.Enabled && rule.Schedule != nil {
		if err := ae.scheduler.ScheduleRule(ctx, rule); err != nil {
			return fmt.Errorf("failed to schedule rule: %w", err)
		}
	}

	ae.logger.Info("registered automation rule", "rule_id", rule.ID, "rule_name", rule.Name)

	return nil
}

// UnregisterRule unregisters an automation rule
func (ae *automationEngine) UnregisterRule(ctx context.Context, ruleID string) error {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	rule, exists := ae.rules[ruleID]
	if !exists {
		return fmt.Errorf("rule %s not found", ruleID)
	}

	// Unschedule rule if it was scheduled
	if err := ae.scheduler.UnscheduleRule(ctx, ruleID); err != nil {
		ae.logger.WithError(err).Warn("failed to unschedule rule", "rule_id", ruleID)
	}

	// Remove rule and status
	delete(ae.rules, ruleID)
	delete(ae.ruleStatuses, ruleID)

	ae.logger.Info("unregistered automation rule", "rule_id", ruleID, "rule_name", rule.Name)

	return nil
}

// TriggerRule manually triggers a rule
func (ae *automationEngine) TriggerRule(ctx context.Context, ruleID string, context map[string]interface{}) error {
	ae.mu.RLock()
	rule, exists := ae.rules[ruleID]
	ae.mu.RUnlock()

	if !exists {
		return fmt.Errorf("rule %s not found", ruleID)
	}

	if !rule.Enabled {
		return fmt.Errorf("rule %s is disabled", ruleID)
	}

	ae.logger.Info("manually triggering rule", "rule_id", ruleID, "rule_name", rule.Name)

	// Execute rule
	result, err := ae.ruleExecutor.ExecuteRule(ctx, rule, context)
	if err != nil {
		return fmt.Errorf("failed to execute rule: %w", err)
	}

	// Update rule status
	ae.updateRuleStatus(ruleID, result)

	ae.logger.Info("rule execution completed",
		"rule_id", ruleID,
		"success", result.Success,
		"duration", result.Duration)

	return nil
}

// GetRuleStatus gets the status of a rule
func (ae *automationEngine) GetRuleStatus(ctx context.Context, ruleID string) (*RuleStatus, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	status, exists := ae.ruleStatuses[ruleID]
	if !exists {
		return nil, fmt.Errorf("rule status not found for rule %s", ruleID)
	}

	// Return a copy to avoid modification
	statusCopy := *status
	return &statusCopy, nil
}

// GetRules returns all registered rules
func (ae *automationEngine) GetRules(ctx context.Context) ([]*AutomationRule, error) {
	ae.mu.RLock()
	defer ae.mu.RUnlock()

	var rules []*AutomationRule
	for _, rule := range ae.rules {
		// Return a copy to avoid modification
		ruleCopy := *rule
		rules = append(rules, &ruleCopy)
	}

	return rules, nil
}

// Health checks the health of the automation engine
func (ae *automationEngine) Health(ctx context.Context) error {
	// Check rule executor health
	if err := ae.ruleExecutor.Health(ctx); err != nil {
		return fmt.Errorf("rule executor health check failed: %w", err)
	}

	// Check condition evaluator health
	if err := ae.conditionEvaluator.Health(ctx); err != nil {
		return fmt.Errorf("condition evaluator health check failed: %w", err)
	}

	// Check scheduler health
	if err := ae.scheduler.Health(ctx); err != nil {
		return fmt.Errorf("scheduler health check failed: %w", err)
	}

	// Check storage health
	if err := ae.storage.Health(ctx); err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}

	return nil
}

// Helper methods

// loadRules loads rules from storage
func (ae *automationEngine) loadRules(ctx context.Context) error {
	// Get automation policies from storage
	policies, err := ae.storage.Policy().GetByType(ctx, types.PolicyTypeAutomation)
	if err != nil {
		return fmt.Errorf("failed to get automation policies: %w", err)
	}

	for _, policy := range policies {
		// Convert policy to automation rule
		rule, err := ae.policyToRule(policy)
		if err != nil {
			ae.logger.WithError(err).WithPolicy(policy.GetMetadata().Name, "").Warn("failed to convert policy to rule")
			continue
		}

		ae.rules[rule.ID] = rule

		// Initialize rule status
		ae.ruleStatuses[rule.ID] = &RuleStatus{
			RuleID:         rule.ID,
			Name:           rule.Name,
			Status:         RuleStatusActive,
			ExecutionCount: 0,
			SuccessCount:   0,
			FailureCount:   0,
			Metadata:       make(map[string]interface{}),
		}
	}

	return nil
}

// policyToRule converts a policy to an automation rule
func (ae *automationEngine) policyToRule(policy types.Policy) (*AutomationRule, error) {
	metadata := policy.GetMetadata()

	rule := &AutomationRule{
		ID:          metadata.Name,
		Name:        metadata.Name,
		Description: "Converted from policy",
		Enabled:     policy.GetStatus() == types.PolicyStatusActive,
		Priority:    int(policy.GetPriority()),
		Conditions:  []*Condition{},
		Actions:     []*Action{},
		Metadata:    convertMapStringToString(metadata.Labels),
		CreatedAt:   metadata.CreationTimestamp,
		UpdatedAt:   metadata.LastModified,
	}

	// Add basic condition and action based on policy type
	if policy.GetType() == types.PolicyTypeAutomation {
		// Add a simple condition
		condition := &Condition{
			Field:    "status",
			Operator: OperatorEquals,
			Value:    "active",
		}
		rule.Conditions = append(rule.Conditions, condition)

		// Add a simple action
		action := &Action{
			Type:   ActionTypeNotify,
			Target: "automation",
			Parameters: map[string]interface{}{
				"message": "Automation rule triggered",
			},
		}
		rule.Actions = append(rule.Actions, action)
	}

	return rule, nil
}

// eventProcessingLoop processes automation events
func (ae *automationEngine) eventProcessingLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ae.stopChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ae.processScheduledRules(ctx)
		}
	}
}

// processScheduledRules processes scheduled rules
func (ae *automationEngine) processScheduledRules(ctx context.Context) {
	ae.mu.RLock()
	rules := make([]*AutomationRule, 0, len(ae.rules))
	for _, rule := range ae.rules {
		if rule.Enabled && rule.Schedule != nil {
			rules = append(rules, rule)
		}
	}
	ae.mu.RUnlock()

	for _, rule := range rules {
		// Check if rule should be executed based on schedule
		if ae.shouldExecuteRule(rule) {
			go func(r *AutomationRule) {
				context := map[string]interface{}{
					"trigger": "schedule",
					"time":    time.Now(),
				}

				result, err := ae.ruleExecutor.ExecuteRule(ctx, r, context)
				if err != nil {
					ae.logger.WithError(err).WithPolicy(r.ID, r.Name).Error("failed to execute scheduled rule")
				} else {
					ae.updateRuleStatus(r.ID, result)
				}
			}(rule)
		}
	}
}

// shouldExecuteRule checks if a rule should be executed based on its schedule
func (ae *automationEngine) shouldExecuteRule(rule *AutomationRule) bool {
	if rule.Schedule == nil {
		return false
	}

	now := time.Now()

	// Check if within time window
	if !rule.Schedule.StartTime.IsZero() && now.Before(rule.Schedule.StartTime) {
		return false
	}
	if !rule.Schedule.EndTime.IsZero() && now.After(rule.Schedule.EndTime) {
		return false
	}

	if rule.Schedule.Interval == "30s" {
		status := ae.ruleStatuses[rule.ID]
		if status == nil || status.LastExecuted.IsZero() {
			return true
		}
		return now.Sub(*status.LastExecuted) >= 30*time.Second
	}

	return false
}

// updateRuleStatus updates the status of a rule
func (ae *automationEngine) updateRuleStatus(ruleID string, result *ExecutionResult) {
	ae.mu.Lock()
	defer ae.mu.Unlock()

	status, exists := ae.ruleStatuses[ruleID]
	if !exists {
		return
	}

	status.LastExecuted = &result.Timestamp
	status.ExecutionCount++

	if result.Success {
		status.SuccessCount++
		status.Status = RuleStatusActive
		status.LastError = ""
	} else {
		status.FailureCount++
		status.Status = RuleStatusFailed
		status.LastError = result.Error
	}

	// Calculate next execution time for scheduled rules
	rule := ae.rules[ruleID]
	if rule != nil && rule.Schedule != nil && rule.Enabled {
		nextExecution := result.Timestamp.Add(30 * time.Second) // Simplified
		status.NextExecution = &nextExecution
	}
}

// convertMapStringToString converts map[string]string to map[string]interface{}
func convertMapStringToString(m map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = v
	}
	return result
}
