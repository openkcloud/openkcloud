package enforcer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// policyEnforcer implements PolicyEnforcer interface
type policyEnforcer struct {
	enforcementEngine EnforcementEngine
	storage           storage.StorageManager
	logger            types.Logger
	enforcements      map[string]*EnforcementStatus
	mu                sync.RWMutex
}

// NewPolicyEnforcer creates a new policy enforcer
func NewPolicyEnforcer(enforcementEngine EnforcementEngine, storage storage.StorageManager, logger types.Logger) PolicyEnforcer {
	return &policyEnforcer{
		enforcementEngine: enforcementEngine,
		storage:           storage,
		logger:            logger,
		enforcements:      make(map[string]*EnforcementStatus),
	}
}

// Enforce enforces a policy decision
func (pe *policyEnforcer) Enforce(ctx context.Context, decision *types.Decision) error {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	// Check if enforcement is already in progress
	if status, exists := pe.enforcements[decision.ID]; exists {
		if status.Status == EnforcementStateRunning {
			return fmt.Errorf("enforcement already in progress for decision %s", decision.ID)
		}
	}

	// Create enforcement status
	status := &EnforcementStatus{
		DecisionID: decision.ID,
		Status:     EnforcementStatePending,
		Progress:   0.0,
		Message:    "Enforcement pending",
		StartedAt:  time.Now(),
		Details:    make(map[string]interface{}),
		Events:     []EnforcementEvent{},
	}

	pe.enforcements[decision.ID] = status

	// Start enforcement in background
	go pe.executeEnforcement(ctx, decision, status)

	return nil
}

// EnforceMany enforces multiple policy decisions
func (pe *policyEnforcer) EnforceMany(ctx context.Context, decisions []*types.Decision) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(decisions))

	for _, decision := range decisions {
		wg.Add(1)
		go func(d *types.Decision) {
			defer wg.Done()
			if err := pe.Enforce(ctx, d); err != nil {
				errChan <- err
			}
		}(decision)
	}

	wg.Wait()
	close(errChan)

	// Collect any errors
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to enforce %d decisions: %v", len(errors), errors)
	}

	return nil
}

// GetEnforcementStatus gets the status of policy enforcement
func (pe *policyEnforcer) GetEnforcementStatus(ctx context.Context, decisionID string) (*EnforcementStatus, error) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	status, exists := pe.enforcements[decisionID]
	if !exists {
		return nil, fmt.Errorf("enforcement status not found for decision %s", decisionID)
	}

	// Return a copy to avoid modification
	statusCopy := *status
	return &statusCopy, nil
}

// CancelEnforcement cancels ongoing policy enforcement
func (pe *policyEnforcer) CancelEnforcement(ctx context.Context, decisionID string) error {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	status, exists := pe.enforcements[decisionID]
	if !exists {
		return fmt.Errorf("enforcement status not found for decision %s", decisionID)
	}

	if status.Status != EnforcementStateRunning {
		return fmt.Errorf("cannot cancel enforcement in state %s", status.Status)
	}

	// Update status to cancelled
	status.Status = EnforcementStateCancelled
	status.Message = "Enforcement cancelled"
	status.CompletedAt = &time.Time{}
	now := time.Now()
	status.CompletedAt = &now
	if status.StartedAt.IsZero() {
		duration := time.Since(status.StartedAt)
		status.Duration = &duration
	}

	// Add event
	event := EnforcementEvent{
		Type:      "cancelled",
		Message:   "Enforcement cancelled by user",
		Timestamp: now,
	}
	status.Events = append(status.Events, event)

	pe.logger.Info("cancelled policy enforcement", "decision_id", decisionID)

	return nil
}

// Health checks the health of the enforcer
func (pe *policyEnforcer) Health(ctx context.Context) error {
	// Check enforcement engine health
	if err := pe.enforcementEngine.Health(ctx); err != nil {
		return fmt.Errorf("enforcement engine health check failed: %w", err)
	}

	// Check storage health
	if err := pe.storage.Health(ctx); err != nil {
		return fmt.Errorf("storage health check failed: %w", err)
	}

	return nil
}

// executeEnforcement executes policy enforcement for a decision
func (pe *policyEnforcer) executeEnforcement(ctx context.Context, decision *types.Decision, status *EnforcementStatus) {
	pe.mu.Lock()
	status.Status = EnforcementStateRunning
	status.Message = "Enforcement in progress"
	pe.mu.Unlock()

	// Add start event
	event := EnforcementEvent{
		Type:      "started",
		Message:   "Enforcement started",
		Timestamp: time.Now(),
	}
	pe.addEvent(status, event)

	defer func() {
		pe.mu.Lock()
		if status.Status == EnforcementStateRunning {
			now := time.Now()
			status.CompletedAt = &now
			duration := time.Since(status.StartedAt)
			status.Duration = &duration
			status.Status = EnforcementStateCompleted
			status.Message = "Enforcement completed successfully"
			status.Progress = 100.0
		}
		pe.mu.Unlock()

		// Add completion event
		completionEvent := EnforcementEvent{
			Type:      "completed",
			Message:   status.Message,
			Timestamp: time.Now(),
		}
		pe.addEvent(status, completionEvent)
	}()

	// Get workload information
	workload, err := pe.storage.Workload().Get(ctx, decision.WorkloadID)
	if err != nil {
		pe.updateStatus(status, EnforcementStateFailed, fmt.Sprintf("Failed to get workload: %v", err))
		return
	}

	// Generate actions based on decision type
	actions, err := pe.generateActions(ctx, decision, workload)
	if err != nil {
		pe.updateStatus(status, EnforcementStateFailed, fmt.Sprintf("Failed to generate actions: %v", err))
		return
	}

	// Execute actions
	totalActions := len(actions)
	for i, action := range actions {
		pe.mu.Lock()
		status.Progress = float64(i) / float64(totalActions) * 100.0
		pe.mu.Unlock()

		// Add action start event
		actionEvent := EnforcementEvent{
			Type:      "action_started",
			Message:   fmt.Sprintf("Executing action: %s", action.Type),
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"action_type":   action.Type,
				"action_target": action.Target,
			},
		}
		pe.addEvent(status, actionEvent)

		// Execute action
		result, err := pe.enforcementEngine.ExecuteAction(ctx, action)
		if err != nil {
			pe.addEvent(status, EnforcementEvent{
				Type:      "action_failed",
				Message:   fmt.Sprintf("Action failed: %v", err),
				Timestamp: time.Now(),
				Data: map[string]interface{}{
					"action_type": action.Type,
					"error":       err.Error(),
				},
			})
			pe.updateStatus(status, EnforcementStateFailed, fmt.Sprintf("Action execution failed: %v", err))
			return
		}

		// Add action completion event
		actionCompleteEvent := EnforcementEvent{
			Type:      "action_completed",
			Message:   fmt.Sprintf("Action completed: %s", action.Type),
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"action_type": action.Type,
				"success":     result.Success,
				"duration":    result.Duration,
			},
		}
		pe.addEvent(status, actionCompleteEvent)

		pe.logger.Info("executed action",
			"decision_id", decision.ID,
			"action_type", action.Type,
			"success", result.Success,
			"duration", result.Duration)
	}

	// Update decision status
	decision.SetStatus(types.DecisionStatusCompleted)
	if err := pe.storage.Decision().Update(ctx, decision); err != nil {
		pe.logger.WithError(err).Warn("failed to update decision status")
	}
}

// generateActions generates actions based on decision type
func (pe *policyEnforcer) generateActions(ctx context.Context, decision *types.Decision, workload *types.Workload) ([]*Action, error) {
	var actions []*Action

	switch decision.Type {
	case types.DecisionTypeSchedule:
		actions = pe.generateScheduleActions(decision, workload)
	case types.DecisionTypeReschedule:
		actions = pe.generateRescheduleActions(decision, workload)
	case types.DecisionTypeMigrate:
		actions = pe.generateMigrateActions(decision, workload)
	case types.DecisionTypeScale:
		actions = pe.generateScaleActions(decision, workload)
	case types.DecisionTypeTerminate:
		actions = pe.generateTerminateActions(decision, workload)
	case types.DecisionTypeSuspend:
		actions = pe.generateSuspendActions(decision, workload)
	case types.DecisionTypeResume:
		actions = pe.generateResumeActions(decision, workload)
	case types.DecisionTypeOptimize:
		actions = pe.generateOptimizeActions(decision, workload)
	default:
		return nil, fmt.Errorf("unsupported decision type: %s", decision.Type)
	}

	return actions, nil
}

// createAction creates a new action with common parameters
func (pe *policyEnforcer) createAction(actionType string, target string, parameters map[string]interface{}, timeout time.Duration) *Action {
	params := make(map[string]interface{})
	for k, v := range parameters {
		params[k] = v
	}
	return &Action{
		Type:       actionType,
		Target:     target,
		Parameters: params,
		Timeout:    timeout,
	}
}

// createNotificationAction creates a notification action
func (pe *policyEnforcer) createNotificationAction(target, message, workloadID, decisionID string) *Action {
	return &Action{
		Type:   ActionTypeNotify,
		Target: target,
		Parameters: map[string]interface{}{
			"message":     message,
			"workload_id": workloadID,
			"decision_id": decisionID,
		},
		Timeout: 30 * time.Second,
	}
}

func (pe *policyEnforcer) generateScheduleActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":         workload.ID,
		"cluster_id":          decision.ClusterID,
		"node_id":             decision.NodeID,
		"recommended_cluster": decision.RecommendedCluster,
		"recommended_node":    decision.RecommendedNode,
		"resources":           workload.Requirements,
	}
	actions := []*Action{pe.createAction(ActionTypeSchedule, decision.WorkloadID, parameters, 5*time.Minute)}

	if decision.Message != "" {
		actions = append(actions, pe.createNotificationAction("scheduler", decision.Message, workload.ID, decision.ID))
	}

	return actions
}

func (pe *policyEnforcer) generateRescheduleActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":         workload.ID,
		"current_cluster":     decision.ClusterID,
		"recommended_cluster": decision.RecommendedCluster,
		"reason":              decision.Reason,
	}
	return []*Action{pe.createAction(ActionTypeReschedule, decision.WorkloadID, parameters, 10*time.Minute)}
}

func (pe *policyEnforcer) generateMigrateActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":        workload.ID,
		"source_cluster":     decision.ClusterID,
		"target_cluster":     decision.RecommendedCluster,
		"source_node":        decision.NodeID,
		"target_node":        decision.RecommendedNode,
		"migration_strategy": "live",
	}
	return []*Action{pe.createAction(ActionTypeMigrate, decision.WorkloadID, parameters, 15*time.Minute)}
}

func (pe *policyEnforcer) generateScaleActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":     workload.ID,
		"scale_factor":    decision.Details["scale_factor"],
		"scale_direction": decision.Details["scale_direction"],
	}
	return []*Action{pe.createAction(ActionTypeScale, decision.WorkloadID, parameters, 5*time.Minute)}
}

func (pe *policyEnforcer) generateTerminateActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":  workload.ID,
		"reason":       decision.Reason,
		"grace_period": "30s",
	}
	return []*Action{pe.createAction(ActionTypeTerminate, decision.WorkloadID, parameters, 2*time.Minute)}
}

func (pe *policyEnforcer) generateSuspendActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id": workload.ID,
		"reason":      decision.Reason,
	}
	return []*Action{pe.createAction(ActionTypeSuspend, decision.WorkloadID, parameters, 2*time.Minute)}
}

func (pe *policyEnforcer) generateResumeActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id": workload.ID,
		"reason":      decision.Reason,
	}
	return []*Action{pe.createAction(ActionTypeResume, decision.WorkloadID, parameters, 2*time.Minute)}
}

func (pe *policyEnforcer) generateOptimizeActions(decision *types.Decision, workload *types.Workload) []*Action {
	parameters := map[string]interface{}{
		"workload_id":   workload.ID,
		"optimizations": decision.Details["optimizations"],
	}
	actions := []*Action{pe.createAction(ActionTypeUpdate, decision.WorkloadID, parameters, 5*time.Minute)}

	actions = append(actions, pe.createNotificationAction("optimizer", "Workload optimization completed", workload.ID, decision.ID))

	return actions
}

// updateStatus updates enforcement status
func (pe *policyEnforcer) updateStatus(status *EnforcementStatus, state EnforcementState, message string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	status.Status = state
	status.Message = message
	if state == EnforcementStateCompleted || state == EnforcementStateFailed {
		now := time.Now()
		status.CompletedAt = &now
		duration := time.Since(status.StartedAt)
		status.Duration = &duration
		status.Progress = 100.0
	}
}

// addEvent adds an event to enforcement status
func (pe *policyEnforcer) addEvent(status *EnforcementStatus, event EnforcementEvent) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	status.Events = append(status.Events, event)
}
