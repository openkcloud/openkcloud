package automation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kcloud-opt/policy/internal/types"
)

// scheduler implements Scheduler interface
type scheduler struct {
	scheduledRules map[string]*AutomationRule
	mu             sync.RWMutex
	logger         types.Logger
}

// NewScheduler creates a new scheduler
func NewScheduler(logger types.Logger) Scheduler {
	return &scheduler{
		scheduledRules: make(map[string]*AutomationRule),
		logger:         logger,
	}
}

// ScheduleRule schedules a rule for execution
func (s *scheduler) ScheduleRule(ctx context.Context, rule *AutomationRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rule.Schedule == nil {
		return fmt.Errorf("rule has no schedule")
	}

	s.scheduledRules[rule.ID] = rule
	s.logger.Info("scheduled automation rule", "rule_id", rule.ID, "rule_name", rule.Name)

	return nil
}

// UnscheduleRule unschedules a rule
func (s *scheduler) UnscheduleRule(ctx context.Context, ruleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.scheduledRules[ruleID]; !exists {
		return fmt.Errorf("rule %s is not scheduled", ruleID)
	}

	delete(s.scheduledRules, ruleID)
	s.logger.Info("unscheduled automation rule", "rule_id", ruleID)

	return nil
}

// GetScheduledRules returns all scheduled rules
func (s *scheduler) GetScheduledRules(ctx context.Context) ([]*AutomationRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rules []*AutomationRule
	for _, rule := range s.scheduledRules {
		// Return a copy to avoid modification
		ruleCopy := *rule
		rules = append(rules, &ruleCopy)
	}

	return rules, nil
}

// Health checks the health of the scheduler
func (s *scheduler) Health(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Basic health check - ensure map is accessible
	_ = len(s.scheduledRules)

	return nil
}

// Helper methods

// IsRuleScheduled checks if a rule is scheduled
func (s *scheduler) IsRuleScheduled(ruleID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.scheduledRules[ruleID]
	return exists
}

// GetNextExecutionTime calculates the next execution time for a rule
func (s *scheduler) GetNextExecutionTime(rule *AutomationRule) *time.Time {
	if rule.Schedule == nil {
		return nil
	}

	now := time.Now()

	// Handle cron-based scheduling
	if rule.Schedule.Cron != "" {
		// In a real implementation, you would use a cron parser
		// For now, we'll use a simple interval-based approach
		nextExecution := now.Add(30 * time.Second) // Simplified
		return &nextExecution
	}

	// Handle interval-based scheduling
	if rule.Schedule.Interval != "" {
		duration, err := s.parseInterval(rule.Schedule.Interval)
		if err != nil {
			s.logger.WithError(err).Warn("failed to parse interval", "rule_id", rule.ID, "interval", rule.Schedule.Interval)
			return nil
		}

		nextExecution := now.Add(duration)
		return &nextExecution
	}

	// Handle time window scheduling
	if !rule.Schedule.StartTime.IsZero() {
		if now.Before(rule.Schedule.StartTime) {
			return &rule.Schedule.StartTime
		}

		// If we're past the start time, calculate next execution
		if !rule.Schedule.EndTime.IsZero() && now.Before(rule.Schedule.EndTime) {
			// Execute every minute within the time window
			nextExecution := now.Add(time.Minute)
			return &nextExecution
		}
	}

	return nil
}

// parseInterval parses an interval string and returns a duration
func (s *scheduler) parseInterval(interval string) (time.Duration, error) {
	// Simple interval parsing - in practice, you'd have more sophisticated parsing
	switch interval {
	case "1s":
		return time.Second, nil
	case "30s":
		return 30 * time.Second, nil
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "10m":
		return 10 * time.Minute, nil
	case "30m":
		return 30 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "12h":
		return 12 * time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported interval: %s", interval)
	}
}

// ShouldExecute checks if a rule should be executed now
func (s *scheduler) ShouldExecute(rule *AutomationRule, lastExecuted *time.Time) bool {
	if rule.Schedule == nil {
		return false
	}

	now := time.Now()

	// Check if we're within the time window
	if !rule.Schedule.StartTime.IsZero() && now.Before(rule.Schedule.StartTime) {
		return false
	}

	if !rule.Schedule.EndTime.IsZero() && now.After(rule.Schedule.EndTime) {
		return false
	}

	// Check if enough time has passed since last execution
	if lastExecuted != nil {
		nextExecution := s.GetNextExecutionTime(rule)
		if nextExecution != nil && now.Before(*nextExecution) {
			return false
		}
	}

	return true
}
