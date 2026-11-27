package plugin

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func gangPod(name string, uid types.UID, gangSize int, timeoutSeconds string) *corev1.Pod {
	controller := true
	annotations := map[string]string{
		gangSizeAnnotation: strconv.Itoa(gangSize),
	}
	if timeoutSeconds != "" {
		annotations[gangTimeoutAnnotation] = timeoutSeconds
	}
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:        name,
		Namespace:   "default",
		UID:         uid,
		Annotations: annotations,
		OwnerReferences: []metav1.OwnerReference{{
			Kind:       "Job",
			Name:       "gang-job",
			Controller: &controller,
		}},
	}}
}

func TestGangSchedulingReleasesWhenAllPodsArrive(t *testing.T) {
	ctx := context.Background()
	g := NewGangScheduling()

	podA := gangPod("pod-a", "uid-a", 2, "5")
	podB := gangPod("pod-b", "uid-b", 2, "5")

	statusA, timeoutA := g.Permit(ctx, podA, "node-1")
	if statusA != framework.PermitWait {
		t.Fatalf("first pod Permit = %v, want PermitWait", statusA)
	}

	waitErr := make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if st := g.WaitForGang(ctx, podA, timeoutA); !st.IsSuccess() {
			waitErr <- st.Message()
			return
		}
		waitErr <- ""
	}()

	statusB, _ := g.Permit(ctx, podB, "node-2")
	if statusB != framework.PermitSuccess {
		t.Fatalf("second pod Permit = %v, want PermitSuccess", statusB)
	}

	select {
	case msg := <-waitErr:
		if msg != "" {
			t.Fatalf("first pod WaitForGang failed: %s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first pod was not released after the gang assembled")
	}
	wg.Wait()
}

// bindingCycle의 context가 취소되면 대기가 중단되고 Pod는 재큐된다. 이때
// 도착 수를 정수로 세면 같은 Pod가 두 번 계수되어 Pod 하나로 gang이 해제된다.
func TestGangSchedulingDoesNotCountRequeuedPodTwice(t *testing.T) {
	g := NewGangScheduling()
	pod := gangPod("pod-a", "uid-a", 2, "30")

	cancelledCtx, cancel := context.WithCancel(context.Background())
	status, timeout := g.Permit(cancelledCtx, pod, "node-1")
	if status != framework.PermitWait {
		t.Fatalf("Permit = %v, want PermitWait", status)
	}
	cancel()
	if st := g.WaitForGang(cancelledCtx, pod, timeout); st.IsSuccess() {
		t.Fatal("WaitForGang succeeded on a cancelled context, want failure")
	}

	// 재큐된 동일 Pod가 다시 도착한다. 실제 Pod는 여전히 1개이므로 gang은
	// 완성되지 않아야 한다.
	status, _ = g.Permit(context.Background(), pod, "node-1")
	if status == framework.PermitSuccess {
		t.Fatal("requeued pod released a gang of 2 by itself; " +
			"arrived count is not deduplicated by pod UID")
	}
}

// 한 Pod가 timeout되면 같은 gang의 다른 대기 Pod도 각자의 timeout을 기다리지
// 않고 함께 실패해야 한다. 대기 중에는 Assume된 자원이 묶여 있다.
func TestGangSchedulingTimeoutFailsPeerWaitersFast(t *testing.T) {
	ctx := context.Background()
	g := NewGangScheduling()

	shortPod := gangPod("pod-short", "uid-short", 3, "1")
	longPod := gangPod("pod-long", "uid-long", 3, "30")

	_, shortTimeout := g.Permit(ctx, shortPod, "node-1")
	_, longTimeout := g.Permit(ctx, longPod, "node-2")

	peerDone := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		g.WaitForGang(ctx, longPod, longTimeout)
		peerDone <- time.Since(start)
	}()

	if st := g.WaitForGang(ctx, shortPod, shortTimeout); st.IsSuccess() {
		t.Fatal("short-timeout pod succeeded, want timeout")
	}

	select {
	case elapsed := <-peerDone:
		if elapsed > 10*time.Second {
			t.Errorf("peer waiter took %s, want release shortly after the gang failed", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("peer waiter was not released when the gang timed out")
	}
}

func TestGangSchedulingBypassesNonGangPods(t *testing.T) {
	ctx := context.Background()
	g := NewGangScheduling()

	single := gangPod("solo", "uid-solo", 1, "")
	if status, _ := g.Permit(ctx, single, "node-1"); status != framework.PermitSuccess {
		t.Errorf("gang-size=1 Permit = %v, want PermitSuccess", status)
	}

	orphan := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:        "orphan",
		Namespace:   "default",
		UID:         "uid-orphan",
		Annotations: map[string]string{gangSizeAnnotation: "2"},
	}}
	if status, _ := g.Permit(ctx, orphan, "node-1"); status != framework.PermitSuccess {
		t.Errorf("pod without controller ownerRef Permit = %v, want PermitSuccess", status)
	}
}

func TestGangSchedulingConcurrentArrivalReleasesOnce(t *testing.T) {
	ctx := context.Background()
	g := NewGangScheduling()

	const gangSize = 8
	var wg sync.WaitGroup
	successes := make(chan struct{}, gangSize)

	for i := 0; i < gangSize; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			pod := gangPod("pod-"+strconv.Itoa(index), types.UID("uid-"+strconv.Itoa(index)), gangSize, "10")
			status, timeout := g.Permit(ctx, pod, "node-1")
			if status == framework.PermitSuccess {
				successes <- struct{}{}
				return
			}
			if st := g.WaitForGang(ctx, pod, timeout); st.IsSuccess() {
				successes <- struct{}{}
			}
		}(i)
	}

	wg.Wait()
	close(successes)

	released := 0
	for range successes {
		released++
	}
	if released != gangSize {
		t.Errorf("released pods = %d, want %d", released, gangSize)
	}
}
