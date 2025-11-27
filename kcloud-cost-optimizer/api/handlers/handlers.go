package handlers

import (
	"github.com/kcloud-opt/policy/internal/automation"
	"github.com/kcloud-opt/policy/internal/evaluator"
	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// Handlers contains all HTTP handlers for the policy engine API
type Handlers struct {
	Policy     *PolicyHandler
	Workload   *WorkloadHandler
	Evaluation *EvaluationHandler
	Automation *AutomationHandler
	Health     *HealthHandler
}

// NewHandlers creates a new handlers instance with all dependencies
func NewHandlers(
	storage storage.StorageManager,
	evaluator evaluator.EvaluationEngine,
	automation automation.AutomationEngine,
	logger types.Logger,
) *Handlers {
	return &Handlers{
		Policy:     NewPolicyHandler(storage, logger),
		Workload:   NewWorkloadHandler(storage, logger),
		Evaluation: NewEvaluationHandler(storage, evaluator, logger),
		Automation: NewAutomationHandler(storage, automation, logger),
		Health:     NewHealthHandler(storage, evaluator, automation, logger),
	}
}
