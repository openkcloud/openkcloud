package validator

import (
	"context"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
	"github.com/xeipuuv/gojsonschema"
)

// PolicyValidator defines the interface for policy validation
type PolicyValidator interface {
	// ValidatePolicy validates a policy against all validation rules
	ValidatePolicy(policy *types.Policy) error

	// ValidateWorkload validates a workload
	ValidateWorkload(workload *types.Workload) error

	// ValidateAutomationRule validates an automation rule
	ValidateAutomationRule(rule *types.AutomationRule) error

	// ValidateExpression validates a policy expression
	ValidateExpression(expression string) error

	// ValidateTimeRange validates a time range
	ValidateTimeRange(startTime, endTime time.Time) error

	// ValidatePercentage validates a percentage value
	ValidatePercentage(value string) error
}

// SchemaValidatorInterface defines the interface for schema validation
type SchemaValidatorInterface interface {
	// LoadSchemas loads JSON schemas for validation
	LoadSchemas() error

	// ValidatePolicy validates a policy against JSON schema
	ValidatePolicy(policy *types.Policy) error

	// ValidateWorkload validates a workload against JSON schema
	ValidateWorkload(workload *types.Workload) error

	// ValidateJSON validates JSON data against a schema
	ValidateJSON(data []byte, schemaName string) error

	// ValidateYAML validates YAML data against a schema
	ValidateYAML(data []byte, schemaName string) error

	// GetSchema returns a loaded schema by name
	GetSchema(name string) (*gojsonschema.Schema, error)

	// ListSchemas returns a list of loaded schema names
	ListSchemas() []string
}

// ExpressionValidatorInterface defines the interface for expression validation
type ExpressionValidatorInterface interface {
	// ValidateExpression validates a policy expression
	ValidateExpression(expression string) error

	// ValidateCondition validates a condition expression
	ValidateCondition(condition string) error

	// ValidateRule validates a rule expression
	ValidateRule(rule *types.Rule) error

	// ValidateTrigger validates a trigger string
	ValidateTrigger(trigger string) error

	// ValidateAutomationRule validates an automation rule
	ValidateAutomationRule(rule *types.AutomationRule) error
}

// ValidationEngine provides comprehensive validation functionality
type ValidationEngineInterface interface {
	// Initialize initializes the validation engine
	Initialize(ctx context.Context) error

	// ValidatePolicy validates a policy with all validators
	ValidatePolicy(policy *types.Policy) error

	// ValidateWorkload validates a workload with all validators
	ValidateWorkload(workload *types.Workload) error

	// ValidateAutomationRule validates an automation rule with all validators
	ValidateAutomationRule(rule *types.AutomationRule) error

	// ValidateExpression validates an expression with all validators
	ValidateExpression(expression string) error

	// Health checks the health of the validation engine
	Health(ctx context.Context) error

	// GetMetrics returns validation metrics
	GetMetrics(ctx context.Context) (map[string]interface{}, error)
}
