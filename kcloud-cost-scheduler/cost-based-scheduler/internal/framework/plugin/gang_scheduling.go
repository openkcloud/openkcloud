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
	"k8s.io/klog/v2"
)

const (
	GangSchedulingName    = "GangScheduling"
	gangSizeAnnotation    = "sched.ai/gang-size"
	gangTimeoutAnnotation = "sched.ai/gang-timeout" // 초 단위, 기본 60
	defaultGangTimeout    = 60 * time.Second
)

type gangEntry struct {
	expected int           // 필요한 총 Pod 수
	arrived  int           // Permit에 도달한 Pod 수
	releaseCh chan struct{} // 닫히면 모든 대기 Pod 해제
	mu       sync.Mutex
}

type GangScheduling struct {
	mu    sync.Mutex
	gangs map[string]*gangEntry // jobKey → gangEntry
}

var _ framework.PermitPlugin = &GangScheduling{}

func NewGangScheduling() *GangScheduling {
	return &GangScheduling{
		gangs: make(map[string]*gangEntry),
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
		entry = &gangEntry{
			expected:  gangSize,
			releaseCh: make(chan struct{}),
		}
		g.gangs[jobKey] = entry
	}
	entry.mu.Lock()
	entry.arrived++
	arrived := entry.arrived
	expected := entry.expected
	entry.mu.Unlock()
	g.mu.Unlock()

	klog.V(3).InfoS("GangScheduling: pod arrived at Permit",
		"pod", klog.KObj(pod), "job", jobKey,
		"arrived", arrived, "expected", expected, "remaining", expected-arrived)

	if arrived >= expected {
		g.mu.Lock()
		if e, ok := g.gangs[jobKey]; ok {
			select {
			case <-e.releaseCh:
			default:
				close(e.releaseCh)
				klog.V(2).InfoS("GangScheduling: all pods ready, releasing gang",
					"job", jobKey, "size", expected)
			}
			delete(g.gangs, jobKey) // 엔트리 정리
		}
		g.mu.Unlock()
		return framework.PermitSuccess, 0
	}

	return framework.PermitWait, timeout
}

func (g *GangScheduling) WaitForGang(ctx context.Context, pod *v1.Pod, timeout time.Duration) *utils.Status {
	jobKey := gangJobKey(pod)
	if jobKey == "" {
		return utils.NewStatus(utils.Success, "")
	}

	g.mu.Lock()
	entry, ok := g.gangs[jobKey]
	g.mu.Unlock()

	if !ok {
		return utils.NewStatus(utils.Success, "")
	}

	select {
	case <-entry.releaseCh:
		klog.V(3).InfoS("GangScheduling: pod released from wait", "pod", klog.KObj(pod))
		return utils.NewStatus(utils.Success, "")
	case <-time.After(timeout):
		klog.V(2).InfoS("GangScheduling: gang wait timed out",
			"pod", klog.KObj(pod), "job", jobKey, "timeout", timeout, "expected", entry.expected, "arrived", entry.arrived)
		g.mu.Lock()
		if e, ok := g.gangs[jobKey]; ok {
			select {
			case <-e.releaseCh:
			default:
			}
			delete(g.gangs, jobKey)
		}
		g.mu.Unlock()
		return utils.NewStatus(utils.Unschedulable,
			fmt.Sprintf("gang %s: timed out waiting for %d pods (arrived: %d)", jobKey, entry.expected, entry.arrived))
	case <-ctx.Done():
		return utils.NewStatus(utils.Unschedulable, "context cancelled")
	}
}

func (g *GangScheduling) getExpected(jobKey string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.gangs[jobKey]; ok {
		return e.expected
	}
	return 0
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
