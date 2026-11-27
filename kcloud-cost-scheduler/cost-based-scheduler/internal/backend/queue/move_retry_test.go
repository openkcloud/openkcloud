package queue

import (
	"testing"
	"time"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/heap"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	testingclock "k8s.io/utils/clock/testing"
)

func newTestQueueFull(clk *testingclock.FakeClock) *SchedulingQueue {
	q := &SchedulingQueue{
		clock:                             clk,
		stop:                              make(chan struct{}),
		podInitialBackoffDuration:         time.Second,
		podMaxBackoffDuration:             10 * time.Second,
		podMaxInUnschedulablePodsDuration: 5 * time.Minute,
		activeQ:                           newActiveQueue(heap.New(podInfoKeyFunc, heap.LessFunc[*QueuedPodInfo](Less))),
		unschedulablePods:                 newUnschedulablePods(),
	}
	q.podBackoffQ = heap.New(podInfoKeyFunc, q.podsCompareBackoffCompleted)
	return q
}

func testPod(name, uid string) *v1.Pod {
	return &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(uid)}}
}

func TestMoveAllPromotesUnschedulableNotBackingOff(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	clk := testingclock.NewFakeClock(start)
	q := newTestQueueFull(clk)

	pod := testPod("wait", "u-wait")
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 1, Timestamp: start.Add(-2 * time.Second)}
	// Backoff of 1s already expired relative to now.
	pInfo.BackoffExpiration = start.Add(-time.Second)
	q.unschedulablePods.addOrUpdate(pInfo)

	q.MoveAllToActiveOrBackoffQueue()
	if q.activeQ.len() != 1 {
		t.Fatalf("expected activeQ=1 got %d", q.activeQ.len())
	}
	if q.unschedulablePods.get(pod) != nil {
		t.Fatal("should leave unschedulable")
	}
	if q.podBackoffQ.Len() != 0 {
		t.Fatalf("backoffQ should be empty, got %d", q.podBackoffQ.Len())
	}
}

func TestMoveAllKeepsBackoffQUntilExpiry(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	clk := testingclock.NewFakeClock(start)
	q := newTestQueueFull(clk)

	pod := testPod("bo", "u-bo")
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 1, Timestamp: start}
	q.AddUnschedulable(pInfo)
	if q.podBackoffQ.Len() != 1 {
		t.Fatalf("setup backoffQ=%d", q.podBackoffQ.Len())
	}

	// Resource event while still backing off — must NOT promote early.
	q.MoveAllToActiveOrBackoffQueue()
	if q.activeQ.len() != 0 {
		t.Fatalf("backoff must wait for timer, activeQ=%d", q.activeQ.len())
	}
	if q.podBackoffQ.Len() != 1 {
		t.Fatalf("should remain in backoffQ, len=%d", q.podBackoffQ.Len())
	}

	clk.Step(time.Second)
	q.flushBackoffQCompleted()
	if q.activeQ.len() != 1 {
		t.Fatalf("after backoff expiry activeQ=%d", q.activeQ.len())
	}
}

func TestMoveAllUnschedulableStillBackingOffGoesToBackoffQ(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	clk := testingclock.NewFakeClock(start)
	q := newTestQueueFull(clk)

	pod := testPod("ub", "u-ub")
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 2, Timestamp: start}
	// 2s backoff still active
	pInfo.BackoffExpiration = start.Add(2 * time.Second)
	q.unschedulablePods.addOrUpdate(pInfo)

	q.MoveAllToActiveOrBackoffQueue()
	if q.activeQ.len() != 0 {
		t.Fatalf("still backing off → not active, got %d", q.activeQ.len())
	}
	if q.podBackoffQ.Len() != 1 {
		t.Fatalf("expected backoffQ=1 got %d", q.podBackoffQ.Len())
	}
	if q.unschedulablePods.get(pod) != nil {
		t.Fatal("should not remain unschedulable")
	}
}

func TestFlushUnschedulableLeftoverMovesAged(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	clk := testingclock.NewFakeClock(start)
	q := newTestQueueFull(clk)
	q.podMaxInUnschedulablePodsDuration = 5 * time.Minute

	pod := testPod("old", "u-old")
	pi, _ := utils.NewPodInfo(pod)
	ts := start.Add(-6 * time.Minute)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 1, Timestamp: ts, InitialAttemptTimestamp: &ts}
	pInfo.BackoffExpiration = ts // not backing off relative to now
	q.unschedulablePods.addOrUpdate(pInfo)

	q.flushUnschedulablePodsLeftover()
	if q.activeQ.len() != 1 {
		t.Fatalf("aged unschedulable should move to activeQ, got %d", q.activeQ.len())
	}
	if q.unschedulablePods.get(pod) != nil {
		t.Fatal("should be removed from unschedulable")
	}
}

func TestMoveToActiveQDedup(t *testing.T) {
	clk := testingclock.NewFakeClock(time.Now())
	q := newTestQueueFull(clk)
	pod := testPod("dup", "u-dup")
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 0, Timestamp: clk.Now()}

	if !q.moveToActiveQ(pInfo, "test") {
		t.Fatal("first move should succeed")
	}
	if !q.moveToActiveQ(pInfo, "test") {
		t.Fatal("second move should still update/return true without panic")
	}
	if q.activeQ.len() != 1 {
		t.Fatalf("dedup activeQ len=%d", q.activeQ.len())
	}
}
