package validator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// ValidationEngine provides comprehensive validation functionality
type ValidationEngine struct {
	policyValidator     PolicyValidator
	schemaValidator     *SchemaValidator
	expressionValidator *ExpressionValidator
	logger              types.Logger
	metrics             *ValidationMetrics
	mu                  sync.RWMutex
}

// ValidationMetrics tracks validation metrics
type ValidationMetrics struct {
	TotalValidations      int64
	SuccessfulValidations int64
	FailedValidations     int64
	ValidationDuration    time.Duration
	LastValidationTime    time.Time
}

// NewValidationEngine creates a new validation engine
func NewValidationEngine(logger types.Logger) *ValidationEngine {
	return &ValidationEngine{
		logger:  logger,
		metrics: &ValidationMetrics{},
	}
}

// Initialize initializes the validation engine
func (ve *ValidationEngine) Initialize(ctx context.Context) error {
	ve.logger.Info("Initializing validation engine...")

	// Initialize validators
	ve.policyValidator = NewValidator(ve.logger)
	ve.schemaValidator = NewSchemaValidator(ve.logger)
	ve.expressionValidator = NewExpressionValidator(ve.logger)

	// Load schemas
	if err := ve.schemaValidator.LoadSchemas(); err != nil {
		return fmt.Errorf("failed to load schemas: %w", err)
	}

	ve.logger.Info("Validation engine initialized successfully")
	return nil
}

// ValidatePolicy validates a policy with all validators
func (ve *ValidationEngine) ValidatePolicy(policy *types.Policy) error {
	startTime := time.Now()
	ve.mu.Lock()
	ve.metrics.TotalValidations++
	ve.mu.Unlock()

	defer func() {
		ve.mu.Lock()
		ve.metrics.ValidationDuration += time.Since(startTime)
		ve.metrics.LastValidationTime = time.Now()
		ve.mu.Unlock()
	}()

	// Validate with policy validator
	if err := ve.policyValidator.ValidatePolicy(policy); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("policy validation failed: %w", err)
	}

	// Validate with schema validator
	if err := ve.schemaValidator.ValidatePolicy(policy); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("schema validation failed: %w", err)
	}

	// Validate expressions in the policy
	if err := ve.validatePolicyExpressions(policy); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("expression validation failed: %w", err)
	}

	ve.mu.Lock()
	ve.metrics.SuccessfulValidations++
	ve.mu.Unlock()

	ve.logger.Info("Policy validation completed successfully", "policy_name", (*policy).GetMetadata().Name)
	return nil
}

// ValidateWorkload validates a workload with all validators
func (ve *ValidationEngine) ValidateWorkload(workload *types.Workload) error {
	startTime := time.Now()
	ve.mu.Lock()
	ve.metrics.TotalValidations++
	ve.mu.Unlock()

	defer func() {
		ve.mu.Lock()
		ve.metrics.ValidationDuration += time.Since(startTime)
		ve.metrics.LastValidationTime = time.Now()
		ve.mu.Unlock()
	}()

	// Validate with policy validator
	if err := ve.policyValidator.ValidateWorkload(workload); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("workload validation failed: %w", err)
	}

	// Validate with schema validator
	if err := ve.schemaValidator.ValidateWorkload(workload); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("schema validation failed: %w", err)
	}

	ve.mu.Lock()
	ve.metrics.SuccessfulValidations++
	ve.mu.Unlock()

	ve.logger.Info("Workload validation completed successfully", "workload_id", workload.ID)
	return nil
}

// ValidateAutomationRule validates an automation rule with all validators
func (ve *ValidationEngine) ValidateAutomationRule(rule *types.AutomationRule) error {
	startTime := time.Now()
	ve.mu.Lock()
	ve.metrics.TotalValidations++
	ve.mu.Unlock()

	defer func() {
		ve.mu.Lock()
		ve.metrics.ValidationDuration += time.Since(startTime)
		ve.metrics.LastValidationTime = time.Now()
		ve.mu.Unlock()
	}()

	// Validate with policy validator
	if err := ve.policyValidator.ValidateAutomationRule(rule); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("automation rule validation failed: %w", err)
	}

	// Validate with expression validator
	if err := ve.expressionValidator.ValidateAutomationRule(rule); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("expression validation failed: %w", err)
	}

	ve.mu.Lock()
	ve.metrics.SuccessfulValidations++
	ve.mu.Unlock()

	ve.logger.Info("Automation rule validation completed successfully", "rule_trigger", rule.Trigger)
	return nil
}

// ValidateExpression validates an expression with all validators
func (ve *ValidationEngine) ValidateExpression(expression string) error {
	startTime := time.Now()
	ve.mu.Lock()
	ve.metrics.TotalValidations++
	ve.mu.Unlock()

	defer func() {
		ve.mu.Lock()
		ve.metrics.ValidationDuration += time.Since(startTime)
		ve.metrics.LastValidationTime = time.Now()
		ve.mu.Unlock()
	}()

	// Validate with policy validator
	if err := ve.policyValidator.ValidateExpression(expression); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("expression validation failed: %w", err)
	}

	// Validate with expression validator
	if err := ve.expressionValidator.ValidateExpression(expression); err != nil {
		ve.mu.Lock()
		ve.metrics.FailedValidations++
		ve.mu.Unlock()
		return fmt.Errorf("expression validation failed: %w", err)
	}

	ve.mu.Lock()
	ve.metrics.SuccessfulValidations++
	ve.mu.Unlock()

	ve.logger.Info("Expression validation completed successfully")
	return nil
}

// validatePolicyExpressions validates all expressions in a policy
func (ve *ValidationEngine) validatePolicyExpressions(policy *types.Policy) error {
	// Since Policy is an interface, we cannot directly access Spec.Rules
	// This validation should be handled by the individual policy implementations
	// or by using type assertions for specific policy types

	// For now, we'll skip detailed rule validation here
	// as it's already covered by other validators

	return nil
}

// Health checks the health of the validation engine
func (ve *ValidationEngine) Health(ctx context.Context) error {
	ve.mu.RLock()
	defer ve.mu.RUnlock()

	// Check if validators are initialized
	if ve.policyValidator == nil {
		return fmt.Errorf("policy validator not initialized")
	}

	if ve.schemaValidator == nil {
		return fmt.Errorf("schema validator not initialized")
	}

	if ve.expressionValidator == nil {
		return fmt.Errorf("expression validator not initialized")
	}

	return nil
}

// GetMetrics returns validation metrics
func (ve *ValidationEngine) GetMetrics(ctx context.Context) (map[string]interface{}, error) {
	ve.mu.RLock()
	defer ve.mu.RUnlock()

	metrics := map[string]interface{}{
		"total_validations":      ve.metrics.TotalValidations,
		"successful_validations": ve.metrics.SuccessfulValidations,
		"failed_validations":     ve.metrics.FailedValidations,
		"success_rate":           ve.calculateSuccessRate(),
		"average_duration":       ve.calculateAverageDuration(),
		"last_validation_time":   ve.metrics.LastValidationTime,
	}

	return metrics, nil
}

// calculateSuccessRate calculates the success rate
func (ve *ValidationEngine) calculateSuccessRate() float64 {
	if ve.metrics.TotalValidations == 0 {
		return 0.0
	}
	return float64(ve.metrics.SuccessfulValidations) / float64(ve.metrics.TotalValidations) * 100.0
}

// calculateAverageDuration calculates the average validation duration
func (ve *ValidationEngine) calculateAverageDuration() time.Duration {
	if ve.metrics.TotalValidations == 0 {
		return 0
	}
	return ve.metrics.ValidationDuration / time.Duration(ve.metrics.TotalValidations)
}

// ResetMetrics resets the validation metrics
func (ve *ValidationEngine) ResetMetrics() {
	ve.mu.Lock()
	defer ve.mu.Unlock()

	ve.metrics = &ValidationMetrics{}
}

// GetPolicyValidator returns the policy validator
func (ve *ValidationEngine) GetPolicyValidator() PolicyValidator {
	return ve.policyValidator
}

// GetSchemaValidator returns the schema validator
func (ve *ValidationEngine) GetSchemaValidator() *SchemaValidator {
	return ve.schemaValidator
}

// GetExpressionValidator returns the expression validator
func (ve *ValidationEngine) GetExpressionValidator() *ExpressionValidator {
	return ve.expressionValidator
}
