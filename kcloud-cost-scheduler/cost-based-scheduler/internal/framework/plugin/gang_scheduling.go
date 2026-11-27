/*
GangScheduling Permit Plugin

This plugin enforces co-scheduling by ensuring all pods in a gang are ready
before permitting them to bind.
*/
package plugin

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

const (
	GangSchedulingName    = "GangScheduling"
	gangSizeAnnotation    = "sched.ai/gang-size"
	gangTimeoutAnnotation = "sched.ai/gang-timeout" // 초 단위, 기본 60
	defaultGangTimeout    = 60 * time.Second
)

// gangEntry는 하나의 Job에 속한 Pod들이 Permit에 모이는 과정을 추적한다.
// 도착 수를 정수로 세면 timeout 후 재큐된 같은 Pod가 다시 Permit을 통과할 때
// 중복 계수되어 Pod 하나로 gang이 해제될 수 있으므로 고유 UID 집합을 쓴다.
type gangEntry struct {
	mu        sync.Mutex
	expected  int
	arrived   map[types.UID]struct{}
	releaseCh chan struct{} // gang이 모두 모이면 닫힌다
	failCh    chan struct{} // gang이 timeout되면 닫힌다
	resolved  bool
	released  bool
}

func newGangEntry(expected int) *gangEntry {
	return &gangEntry{
		expected:  expected,
		arrived:   make(map[types.UID]struct{}),
		releaseCh: make(chan struct{}),
		failCh:    make(chan struct{}),
	}
}

// resolve는 entry.mu를 잡은 상태에서 호출해야 한다.
func (e *gangEntry) resolve(released bool) bool {
	if e.resolved {
		return false
	}
	e.resolved = true
	e.released = released
	if released {
		close(e.releaseCh)
	} else {
		close(e.failCh)
	}
	return true
}

func (e *gangEntry) progress() (arrived, expected int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.arrived), e.expected
}

type GangScheduling struct {
	mu    sync.Mutex
	gangs map[string]*gangEntry
	// Permit에서 대기로 판정된 Pod가 합류한 세대를 기억한다. WaitForGang이
	// jobKey로 다시 조회하면 그 사이 교체된 다른 세대를 기다릴 수 있다.
	waiting map[types.UID]*gangEntry
}

var _ framework.PermitPlugin = &GangScheduling{}

func NewGangScheduling() *GangScheduling {
	return &GangScheduling{
		gangs:   make(map[string]*gangEntry),
		waiting: make(map[types.UID]*gangEntry),
	}
}

func (g *GangScheduling) Name() string { return GangSchedulingName }

func (g *GangScheduling) Permit(ctx context.Context, pod *v1.Pod, nodeName string) (framework.PermitStatus, time.Duration) {
	gangSize, timeout := g.parseAnnotations(pod)
	if gangSize <= 1 {
		return framework.PermitSuccess, 0
	}

	jobKey := gangJobKey(pod)
	if jobKey == "" {
		return framework.PermitSuccess, 0 // ownerRef 없는 Pod는 Gang 불필요
	}

	g.mu.Lock()
	entry, exists := g.gangs[jobKey]
	if !exists {
		entry = newGangEntry(gangSize)
		g.gangs[jobKey] = entry
	}
	g.mu.Unlock()

	entry.mu.Lock()
	if entry.resolved {
		// 실패한 세대는 즉시 map에서 제거되므로 여기 남은 것은 성공한 세대다.
		// Bind 재시도로 다시 들어온 Pod를 다시 대기시키지 않는다.
		entry.mu.Unlock()
		klog.V(3).InfoS("GangScheduling: gang already released, permitting immediately",
			"pod", klog.KObj(pod), "job", jobKey)
		return framework.PermitSuccess, 0
	}
	entry.arrived[pod.UID] = struct{}{}
	arrived, expected := len(entry.arrived), entry.expected
	if arrived >= expected {
		entry.resolve(true)
		entry.mu.Unlock()
		klog.V(2).InfoS("GangScheduling: all pods ready, releasing gang",
			"job", jobKey, "size", expected)
		g.discardAfter(jobKey, entry, timeout)
		return framework.PermitSuccess, 0
	}
	entry.mu.Unlock()

	klog.V(3).InfoS("GangScheduling: pod arrived at Permit",
		"pod", klog.KObj(pod), "job", jobKey,
		"arrived", arrived, "expected", expected, "remaining", expected-arrived)

	g.mu.Lock()
	g.waiting[pod.UID] = entry
	g.mu.Unlock()

	return framework.PermitWait, timeout
}

func (g *GangScheduling) WaitForGang(ctx context.Context, pod *v1.Pod, timeout time.Duration) *utils.Status {
	jobKey := gangJobKey(pod)
	if jobKey == "" {
		return utils.NewStatus(utils.Success, "")
	}

	g.mu.Lock()
	entry, ok := g.waiting[pod.UID]
	delete(g.waiting, pod.UID)
	g.mu.Unlock()

	if !ok {
		// Permit이 대기로 판정하지 않았거나 이미 처리된 Pod다.
		return utils.NewStatus(utils.Success, "")
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-entry.releaseCh:
		klog.V(3).InfoS("GangScheduling: pod released from wait", "pod", klog.KObj(pod))
		return utils.NewStatus(utils.Success, "")
	case <-entry.failCh:
		arrived, expected := entry.progress()
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("gang %s: peer pod timed out waiting for %d pods (arrived: %d)",
				jobKey, expected, arrived))
	case <-timer.C:
		// 한 Pod가 시간을 초과하면 이 세대는 누구도 성립시킬 수 없다. 세대를
		// 실패로 닫아 나머지 대기 Pod가 각자의 timeout까지 자원을 붙잡지 않게 한다.
		entry.mu.Lock()
		firstToFail := entry.resolve(false)
		arrived, expected := len(entry.arrived), entry.expected
		entry.mu.Unlock()
		if firstToFail {
			g.discard(jobKey, entry)
			klog.V(2).InfoS("GangScheduling: gang wait timed out",
				"pod", klog.KObj(pod), "job", jobKey, "timeout", timeout,
				"expected", expected, "arrived", arrived)
		}
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("gang %s: timed out waiting for %d pods (arrived: %d)",
				jobKey, expected, arrived))
	case <-ctx.Done():
		return utils.NewStatus(utils.Unschedulable, "context cancelled")
	}
}

// discard는 해당 세대가 아직 등록돼 있을 때만 제거한다. 이미 새 세대로
// 교체된 경우 그 세대를 지우지 않는다.
func (g *GangScheduling) discard(jobKey string, entry *gangEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if current, ok := g.gangs[jobKey]; ok && current == entry {
		delete(g.gangs, jobKey)
	}
}

func (g *GangScheduling) discardAfter(jobKey string, entry *gangEntry, grace time.Duration) {
	time.AfterFunc(grace, func() { g.discard(jobKey, entry) })
}

func (g *GangScheduling) parseAnnotations(pod *v1.Pod) (size int, timeout time.Duration) {
	size = 1
	timeout = defaultGangTimeout

	if s, ok := pod.Annotations[gangSizeAnnotation]; ok {
		if n, err := strconv.Atoi(s); err == nil && n > 1 {
			size = n
		}
	}
	if t, ok := pod.Annotations[gangTimeoutAnnotation]; ok {
		if sec, err := strconv.Atoi(t); err == nil && sec > 0 {
			timeout = time.Duration(sec) * time.Second
		}
	}
	return
}

func gangJobKey(pod *v1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return fmt.Sprintf("%s/%s/%s", pod.Namespace, ref.Kind, ref.Name)
		}
	}
	return ""
}
