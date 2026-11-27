package queue

import (
	"testing"
	"time"
)

func TestRetryBackoffCaps(t *testing.T) {
	d1 := RetryBackoff(1, time.Second, 10*time.Second)
	if d1 != time.Second {
		t.Fatalf("attempt1=%s", d1)
	}
	d3 := RetryBackoff(3, time.Second, 10*time.Second)
	if d3 != 4*time.Second {
		t.Fatalf("attempt3=%s want 4s", d3)
	}
	dBig := RetryBackoff(20, time.Second, 10*time.Second)
	if dBig != 10*time.Second {
		t.Fatalf("capped=%s", dBig)
	}
}
