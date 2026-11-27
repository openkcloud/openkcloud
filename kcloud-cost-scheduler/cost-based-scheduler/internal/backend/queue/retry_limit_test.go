package queue

import (
	"testing"
	"time"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	testingclock "k8s.io/utils/clock/testing"
)

func TestAddUnschedulableRespectsMaxAttempts(t *testing.T) {
	clk := testingclock.NewFakeClock(time.Now())
	q := newTestQueue(clk)
	q.SetMaxSchedulingAttempts(2)

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "u"}}
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 2, Timestamp: clk.Now()}
	q.AddUnschedulable(pInfo)

	if q.podBackoffQ.Len() != 0 {
		t.Fatalf("expected no backoff requeue at max attempts")
	}
	if q.unschedulablePods.get(pod) == nil {
		t.Fatal("expected unschedulable retention at max attempts")
	}
}

func TestDeleteCancelsBackoffRetry(t *testing.T) {
	clk := testingclock.NewFakeClock(time.Now())
	q := newTestQueue(clk)

	pod := &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "u"}}
	pi, _ := utils.NewPodInfo(pod)
	pInfo := &QueuedPodInfo{PodInfo: pi, Attempts: 1, Timestamp: clk.Now()}
	q.AddUnschedulable(pInfo)
	if q.podBackoffQ.Len() != 1 {
		t.Fatalf("setup: backoffQ=%d", q.podBackoffQ.Len())
	}
	if err := q.Delete(pod); err != nil {
		t.Fatal(err)
	}
	if q.podBackoffQ.Len() != 0 {
		t.Fatalf("delete left backoff entry: %d", q.podBackoffQ.Len())
	}
	if q.unschedulablePods.get(pod) != nil {
		t.Fatal("delete left unschedulable entry")
	}
}
