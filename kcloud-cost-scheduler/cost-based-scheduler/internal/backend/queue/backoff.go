package queue

import (
	"math"
	"time"
)

// RetryBackoff computes the delay before requeueing a failed scheduling attempt.
// Does not sleep — callers schedule the pod for later (P6: no ScheduleOne sleep).
func RetryBackoff(attempt int, initial, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if initial <= 0 {
		initial = time.Second
	}
	if max <= 0 {
		max = 30 * time.Second
	}
	// exponential: initial * 2^(attempt-1), capped
	mult := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(initial) * mult)
	if d > max || d <= 0 {
		return max
	}
	return d
}
