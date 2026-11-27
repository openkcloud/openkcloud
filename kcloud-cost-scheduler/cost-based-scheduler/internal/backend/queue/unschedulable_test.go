package queue

import (
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/heap"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
)

func newTestQueue(clk *testingclock.FakeClock) *SchedulingQueue {
	q := &SchedulingQueue{
		clock:                     clk,
		stop:                      make(chan struct{}),
		podInitialBackoffDuration: time.Second,
		podMaxBackoffDuration:     10 * time.Second,
		activeQ:                   newActiveQueue(heap.New(podInfoKeyFunc, heap.LessFunc[*QueuedPodInfo](Less))),
		unschedulablePods:         newUnschedulablePods(),
	}
	q.podBackoffQ = heap.New(podInfoKeyFunc, q.podsCompareBackoffCompleted)
	return q
}

func TestAddUnschedulableUsesBackoffWithoutSleep(t *testing.T) {
	start := time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC)
	clk := testingclock.NewFakeClock(start)
	q := newTestQueue(clk)

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "u1"}}
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 1, Timestamp: start}

	q.AddUnschedulable(pInfo)
	if q.activeQ.len() != 0 {
		t.Fatalf("should not be in activeQ yet")
	}
	if q.podBackoffQ.Len() != 1 {
		t.Fatalf("backoffQ len=%d", q.podBackoffQ.Len())
	}

	q.flushBackoffQCompleted()
	if q.activeQ.len() != 0 {
		t.Fatalf("still backing off at t0")
	}

	clk.Step(time.Second)
	q.flushBackoffQCompleted()
	if q.activeQ.len() != 1 {
		t.Fatalf("expected move to activeQ after backoff, len=%d", q.activeQ.len())
	}
	if q.podBackoffQ.Len() != 0 {
		t.Fatalf("backoffQ should be empty")
	}
}

func TestAddUnschedulablePreservesAttempts(t *testing.T) {
	clk := testingclock.NewFakeClock(time.Now())
	q := newTestQueue(clk)
	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns", UID: "u2"}}
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 3, Timestamp: clk.Now()}
	q.AddUnschedulable(pInfo)
	got, ok := q.podBackoffQ.Peek()
	if !ok || got.Attempts != 3 {
		t.Fatalf("attempts not preserved: ok=%v attempts=%d", ok, got.Attempts)
	}
	want := RetryBackoff(3, time.Second, 10*time.Second)
	if !got.BackoffExpiration.Equal(pInfo.Timestamp.Add(want)) &&
		!got.BackoffExpiration.Equal(clk.Now().Add(want)) {
		// Timestamp was refreshed in AddUnschedulable
		if got.BackoffExpiration.Sub(got.Timestamp) != want {
			t.Fatalf("backoff delay=%s want %s", got.BackoffExpiration.Sub(got.Timestamp), want)
		}
	}
}
