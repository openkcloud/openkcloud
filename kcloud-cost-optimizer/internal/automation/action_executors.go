package automation

import (
	"context"
	"fmt"
	"time"

	"github.com/kcloud-opt/policy/internal/storage"
	"github.com/kcloud-opt/policy/internal/types"
)

// baseActionExecutor provides common functionality for action executors
type baseActionExecutor struct {
	actionTypes []string
	logger      types.Logger
}

// CanExecute checks if this executor can handle the given action type
func (bae *baseActionExecutor) CanExecute(actionType string) bool {
	for _, supportedType := range bae.actionTypes {
		if supportedType == actionType {
			return true
		}
	}
	return false
}

// Health checks the health of the executor
func (bae *baseActionExecutor) Health(ctx context.Context) error {
	return nil
}

// notifyActionExecutor executes notification actions
type notifyActionExecutor struct {
	baseActionExecutor
}

// NewNotifyActionExecutor creates a new notify action executor
func NewNotifyActionExecutor(logger types.Logger) ActionExecutor {
	return &notifyActionExecutor{
		baseActionExecutor: baseActionExecutor{
			actionTypes: []string{ActionTypeNotify},
			logger:      logger,
		},
	}
}

// ExecuteAction executes the notification action
func (nae *notifyActionExecutor) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	nae.logger.Info("executing notify action", "target", action.Target)

	message := "Notification sent"
	if msg, exists := action.Parameters["message"]; exists {
		if msgStr, ok := msg.(string); ok {
			message = msgStr
		}
	}

	// Simulate notification sending
	time.Sleep(50 * time.Millisecond)

	nae.logger.Info("notification sent", "target", action.Target, "message", message)

	return &ActionResult{
		ActionType: action.Type,
		Success:    true,
		Message:    message,
		Duration:   time.Since(startTime),
		Timestamp:  time.Now(),
		Data: map[string]interface{}{
			"target":  action.Target,
			"message": message,
		},
	}, nil
}

// scaleActionExecutor executes scaling actions
type scaleActionExecutor struct {
	baseActionExecutor
	storage storage.StorageManager
}

// NewScaleActionExecutor creates a new scale action executor
func NewScaleActionExecutor(storage storage.StorageManager, logger types.Logger) ActionExecutor {
	return &scaleActionExecutor{
		baseActionExecutor: baseActionExecutor{
			actionTypes: []string{ActionTypeScale},
			logger:      logger,
		},
		storage: storage,
	}
}

// ExecuteAction executes the scaling action
func (sae *scaleActionExecutor) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	sae.logger.Info("executing scale action", "target", action.Target)

	// Get workload ID from parameters
	workloadID, ok := action.Parameters["workload_id"].(string)
	if !ok {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    "workload_id parameter is required",
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      "missing workload_id parameter",
		}, nil
	}

	// Get scale factor
	scaleFactor, ok := action.Parameters["scale_factor"].(float64)
	if !ok {
		scaleFactor = 1.0
	}

	// Get workload from storage
	workload, err := sae.storage.Workload().Get(ctx, workloadID)
	if err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to get workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Apply scaling (simplified)
	if scaleFactor > 1.0 {
		// Scale up
		workload.Requirements.CPU = int(float64(workload.Requirements.CPU) * scaleFactor)
	} else if scaleFactor < 1.0 {
		// Scale down
		workload.Requirements.CPU = int(float64(workload.Requirements.CPU) * scaleFactor)
		if workload.Requirements.CPU < 1 {
			workload.Requirements.CPU = 1
		}
	}

	workload.UpdatedAt = time.Now()

	// Update workload in storage
	if err := sae.storage.Workload().Update(ctx, workload); err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to update workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Simulate scaling work
	time.Sleep(200 * time.Millisecond)

	sae.logger.Info("scale action completed", "workload_id", workloadID, "scale_factor", scaleFactor)

	return &ActionResult{
		ActionType: action.Type,
		Success:    true,
		Message:    fmt.Sprintf("Workload scaled by factor %.2f", scaleFactor),
		Duration:   time.Since(startTime),
		Timestamp:  time.Now(),
		Data: map[string]interface{}{
			"workload_id":  workloadID,
			"scale_factor": scaleFactor,
			"new_cpu":      workload.Requirements.CPU,
		},
	}, nil
}

// migrateActionExecutor executes migration actions
type migrateActionExecutor struct {
	baseActionExecutor
	storage storage.StorageManager
}

// NewMigrateActionExecutor creates a new migrate action executor
func NewMigrateActionExecutor(storage storage.StorageManager, logger types.Logger) ActionExecutor {
	return &migrateActionExecutor{
		baseActionExecutor: baseActionExecutor{
			actionTypes: []string{ActionTypeMigrate},
			logger:      logger,
		},
		storage: storage,
	}
}

// ExecuteAction executes the migration action
func (mae *migrateActionExecutor) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	mae.logger.Info("executing migrate action", "target", action.Target)

	// Get workload ID from parameters
	workloadID, ok := action.Parameters["workload_id"].(string)
	if !ok {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    "workload_id parameter is required",
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      "missing workload_id parameter",
		}, nil
	}

	// Get source and target clusters
	sourceCluster, _ := action.Parameters["source_cluster"].(string)
	targetCluster, _ := action.Parameters["target_cluster"].(string)

	// Get workload from storage
	workload, err := mae.storage.Workload().Get(ctx, workloadID)
	if err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to get workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Update workload cluster information
	if workload.Labels == nil {
		workload.Labels = make(map[string]string)
	}

	if sourceCluster != "" {
		workload.Labels["previous_cluster"] = sourceCluster
	}
	if targetCluster != "" {
		workload.Labels["cluster"] = targetCluster
	}

	workload.UpdatedAt = time.Now()

	// Update workload in storage
	if err := mae.storage.Workload().Update(ctx, workload); err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to update workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Simulate migration work
	time.Sleep(500 * time.Millisecond)

	mae.logger.Info("migrate action completed", "workload_id", workloadID, "source", sourceCluster, "target", targetCluster)

	return &ActionResult{
		ActionType: action.Type,
		Success:    true,
		Message:    fmt.Sprintf("Workload migrated from %s to %s", sourceCluster, targetCluster),
		Duration:   time.Since(startTime),
		Timestamp:  time.Now(),
		Data: map[string]interface{}{
			"workload_id":    workloadID,
			"source_cluster": sourceCluster,
			"target_cluster": targetCluster,
		},
	}, nil
}

// terminateActionExecutor executes termination actions
type terminateActionExecutor struct {
	baseActionExecutor
	storage storage.StorageManager
}

// NewTerminateActionExecutor creates a new terminate action executor
func NewTerminateActionExecutor(storage storage.StorageManager, logger types.Logger) ActionExecutor {
	return &terminateActionExecutor{
		baseActionExecutor: baseActionExecutor{
			actionTypes: []string{ActionTypeTerminate},
			logger:      logger,
		},
		storage: storage,
	}
}

// ExecuteAction executes the termination action
func (tae *terminateActionExecutor) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	tae.logger.Info("executing terminate action", "target", action.Target)

	// Get workload ID from parameters
	workloadID, ok := action.Parameters["workload_id"].(string)
	if !ok {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    "workload_id parameter is required",
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      "missing workload_id parameter",
		}, nil
	}

	// Get workload from storage
	workload, err := tae.storage.Workload().Get(ctx, workloadID)
	if err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to get workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Update workload status to completed
	workload.Status = types.WorkloadStatusCompleted
	workload.UpdatedAt = time.Now()

	// Update workload in storage
	if err := tae.storage.Workload().Update(ctx, workload); err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to update workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Simulate termination work
	time.Sleep(300 * time.Millisecond)

	tae.logger.Info("terminate action completed", "workload_id", workloadID)

	return &ActionResult{
		ActionType: action.Type,
		Success:    true,
		Message:    "Workload terminated successfully",
		Duration:   time.Since(startTime),
		Timestamp:  time.Now(),
		Data: map[string]interface{}{
			"workload_id": workloadID,
			"status":      workload.Status,
		},
	}, nil
}

// updateActionExecutor executes update actions
type updateActionExecutor struct {
	baseActionExecutor
	storage storage.StorageManager
}

// NewUpdateActionExecutor creates a new update action executor
func NewUpdateActionExecutor(storage storage.StorageManager, logger types.Logger) ActionExecutor {
	return &updateActionExecutor{
		baseActionExecutor: baseActionExecutor{
			actionTypes: []string{ActionTypeUpdate},
			logger:      logger,
		},
		storage: storage,
	}
}

// ExecuteAction executes the update action
func (uae *updateActionExecutor) ExecuteAction(ctx context.Context, action *Action) (*ActionResult, error) {
	startTime := time.Now()

	uae.logger.Info("executing update action", "target", action.Target)

	// Get workload ID from parameters
	workloadID, ok := action.Parameters["workload_id"].(string)
	if !ok {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    "workload_id parameter is required",
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      "missing workload_id parameter",
		}, nil
	}

	// Get workload from storage
	workload, err := uae.storage.Workload().Get(ctx, workloadID)
	if err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to get workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Apply updates from parameters
	if optimizations, exists := action.Parameters["optimizations"]; exists {
		uae.logger.Info("applying optimizations", "workload_id", workloadID, "optimizations", optimizations)
	}

	// Update workload
	workload.UpdatedAt = time.Now()

	if err := uae.storage.Workload().Update(ctx, workload); err != nil {
		return &ActionResult{
			ActionType: action.Type,
			Success:    false,
			Message:    fmt.Sprintf("Failed to update workload: %v", err),
			Duration:   time.Since(startTime),
			Timestamp:  time.Now(),
			Error:      err.Error(),
		}, nil
	}

	// Simulate update work
	time.Sleep(200 * time.Millisecond)

	uae.logger.Info("update action completed", "workload_id", workloadID)

	return &ActionResult{
		ActionType: action.Type,
		Success:    true,
		Message:    "Workload updated successfully",
		Duration:   time.Since(startTime),
		Timestamp:  time.Now(),
		Data: map[string]interface{}{
			"workload_id": workloadID,
		},
	}, nil
}
