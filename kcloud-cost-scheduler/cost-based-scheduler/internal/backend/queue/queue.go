/*
Scheduling queue management

This module implements the PriorityQueue, managing the lifecycle of pods waiting
to be scheduled, including sorting, backoff mechanisms, and event handling.
*/
package queue

import (
	"reflect"
	"sync"
	"time"

	heap "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/heap"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

const (
	DefaultPodInitialBackoffDuration         time.Duration = 1 * time.Second
	DefaultPodMaxBackoffDuration             time.Duration = 10 * time.Second
	DefaultPodMaxInUnschedulablePodsDuration time.Duration = 5 * time.Minute
)

type SchedulingQueue struct {
	*nominator
	stop                              chan struct{}
	clock                             clock.Clock
	lock                              sync.RWMutex
	activeQ                           *activeQueue
	podBackoffQ                       *heap.Heap[*QueuedPodInfo]
	unschedulablePods                 *UnschedulablePods
	podInitialBackoffDuration         time.Duration
	podMaxBackoffDuration             time.Duration
	podMaxInUnschedulablePodsDuration time.Duration
	nsLister                          listersv1.NamespaceLister
}

type LessFunc func(podInfo1, podInfo2 *QueuedPodInfo) bool

func NewSchedulingQueue(lessFn LessFunc, informerFactory informers.SharedInformerFactory) *SchedulingQueue {
	sq := &SchedulingQueue{
		clock:                             clock.RealClock{},
		stop:                              make(chan struct{}),
		podInitialBackoffDuration:         DefaultPodInitialBackoffDuration,
		podMaxBackoffDuration:             DefaultPodMaxBackoffDuration,
		podMaxInUnschedulablePodsDuration: DefaultPodMaxInUnschedulablePodsDuration,
		activeQ:                           newActiveQueue(heap.New(podInfoKeyFunc, heap.LessFunc[*QueuedPodInfo](lessFn))),
		unschedulablePods:                 newUnschedulablePods(),
	}
	sq.podBackoffQ = heap.New(podInfoKeyFunc, sq.podsCompareBackoffCompleted)
	sq.nsLister = informerFactory.Core().V1().Namespaces().Lister()
	podLister := informerFactory.Core().V1().Pods().Lister()
	sq.nominator = newPodNominator(podLister)

	return sq
}

func (q *SchedulingQueue) Run(  ) {
	go wait.Until(func() {
		q.flushBackoffQCompleted()
	}, 1.0*time.Second, q.stop)
	go wait.Until(func() {
		q.flushUnschedulablePodsLeftover()
	}, 30*time.Second, q.stop)
}

func (q *SchedulingQueue) Close() {
	q.lock.Lock()
	defer q.lock.Unlock()
	close(q.stop)
	q.activeQ.close()
	q.activeQ.broadcast()
}

func (q *SchedulingQueue) newQueuedPodInfo(pod *v1.Pod, plugins ...string) *QueuedPodInfo {
	now := q.clock.Now()
	podInfo, _ := utils.NewPodInfo(pod)
	return &QueuedPodInfo{
		PodInfo:                 podInfo,
		Timestamp:               now,
		InitialAttemptTimestamp: nil,
		UnschedulablePlugins:    sets.New(plugins...),
	}
}

func (q *SchedulingQueue) Add(pod *v1.Pod) {
	q.lock.Lock()
	defer q.lock.Unlock()

	pInfo := q.newQueuedPodInfo(pod)
	wasEmpty := q.activeQ.addOrUpdate(pInfo)
	if wasEmpty {
		q.activeQ.broadcast()
	}
}

func (q *SchedulingQueue) Update(oldPod, newPod *v1.Pod) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	var defaultLogger klog.Logger

	if oldPod != nil {
		oldPodInfo := newQueuedPodInfoForLookup(oldPod)

		if pInfo := q.activeQ.update(newPod, oldPodInfo); pInfo != nil {
			q.UpdateNominatedPod(defaultLogger, oldPod, pInfo.PodInfo)
			return nil
		}

		if pInfo, exists := q.podBackoffQ.Get(oldPodInfo); exists {
			_ = pInfo.Update(newPod)
			q.UpdateNominatedPod(defaultLogger, oldPod, pInfo.PodInfo)
			q.podBackoffQ.AddOrUpdate(pInfo)
			return nil
		}
	}

	if pInfo := q.unschedulablePods.get(newPod); pInfo != nil {
		_ = pInfo.Update(newPod)
		q.UpdateNominatedPod(defaultLogger, oldPod, pInfo.PodInfo)
		if isPodUpdated(oldPod, newPod) {
			if q.isPodBackingoff(pInfo) {
				q.podBackoffQ.AddOrUpdate(pInfo)
				q.unschedulablePods.delete(pInfo.Pod)
				return nil
			}

			return nil
		}

		q.unschedulablePods.addOrUpdate(pInfo)
		return nil
	}

	return nil
}

func (q *SchedulingQueue) Delete(pod *v1.Pod) error {
	q.lock.Lock()
	defer q.lock.Unlock()
	q.DeleteNominatedPodIfExists(pod)
	pInfo := newQueuedPodInfoForLookup(pod)
	if err := q.activeQ.delete(pInfo); err != nil {
		q.podBackoffQ.Delete(pInfo)
		if pInfo = q.unschedulablePods.get(pod); pInfo != nil {
			q.unschedulablePods.delete(pod)
		}
	}

	return nil
}

func (q *SchedulingQueue) MoveAllToActiveOrBackoffQueue() {

}

func (q *SchedulingQueue) Len() int {
	q.lock.Lock()
	defer q.lock.Unlock()

	return q.activeQ.len()
}

func (q *SchedulingQueue) Empty() bool {
	return q.activeQ.len() == 0
}

func (q *SchedulingQueue) Pop() (*QueuedPodInfo, error) {
	return q.activeQ.pop()
}

func (q *SchedulingQueue) Done(pod types.UID) {
	q.activeQ.done(pod)
}

func (q *SchedulingQueue) podsCompareBackoffCompleted(pInfo1, pInfo2 *QueuedPodInfo) bool {
	bo1 := q.getBackoffTime(pInfo1)
	bo2 := q.getBackoffTime(pInfo2)
	return bo1.Before(bo2)
}

func (q *SchedulingQueue) isPodBackingoff(podInfo *QueuedPodInfo) bool {
	boTime := q.getBackoffTime(podInfo)
	return boTime.After(q.clock.Now())
}

func (q *SchedulingQueue) getBackoffTime(podInfo *QueuedPodInfo) time.Time {
	duration := q.calculateBackoffDuration(podInfo)
	backoffTime := podInfo.Timestamp.Add(duration)
	return backoffTime
}

func (q *SchedulingQueue) calculateBackoffDuration(podInfo *QueuedPodInfo) time.Duration {
	if podInfo.Attempts == 0 {
		return 0
	}

	duration := q.podInitialBackoffDuration
	for i := 1; i < podInfo.Attempts; i++ {
		if duration > q.podMaxBackoffDuration-duration {
			return q.podMaxBackoffDuration
		}
		duration += duration
	}
	return duration
}

func (q *SchedulingQueue) flushBackoffQCompleted() {

}

func (q *SchedulingQueue) flushUnschedulablePodsLeftover() {


}

func (q *SchedulingQueue) moveToActiveQ(pInfo *QueuedPodInfo, event string) bool {
	added := false



	return added
}

type activeQueue struct {
	lock   sync.RWMutex
	queue  *heap.Heap[*QueuedPodInfo]
	cond   sync.Cond
	closed bool
}

func newActiveQueue(queue *heap.Heap[*QueuedPodInfo]) *activeQueue {
	aq := &activeQueue{
		queue: queue,
	}
	aq.cond.L = &aq.lock

	return aq
}

func (aq *activeQueue) close() {
	aq.lock.Lock()
	aq.closed = true
	aq.lock.Unlock()
}

func (aq *activeQueue) broadcast() {
	aq.cond.Broadcast()
}

func (aq *activeQueue) len() int {
	return aq.queue.Len()
}

func (aq *activeQueue) has(pInfo *QueuedPodInfo) bool {
	aq.lock.RLock()
	defer aq.lock.RUnlock()
	return aq.queue.Has(pInfo)
}

func (aq *activeQueue) list() []*v1.Pod {
	aq.lock.RLock()
	defer aq.lock.RUnlock()
	var result []*v1.Pod
	for _, pInfo := range aq.queue.List() {
		result = append(result, pInfo.Pod)
	}
	return result
}

func (aq *activeQueue) update(newPod *v1.Pod, oldPodInfo *QueuedPodInfo) *QueuedPodInfo {
	aq.lock.Lock()
	defer aq.lock.Unlock()

	if pInfo, exists := aq.queue.Get(oldPodInfo); exists {
		_ = pInfo.Update(newPod)
		aq.queue.AddOrUpdate(pInfo)
		return pInfo
	}
	return nil
}

func (aq *activeQueue) delete(pInfo *QueuedPodInfo) error {
	aq.lock.Lock()
	defer aq.lock.Unlock()

	return aq.queue.Delete(pInfo)
}

func (aq *activeQueue) addOrUpdate(pInfo *QueuedPodInfo) bool {
	aq.lock.Lock()
	defer aq.lock.Unlock()
	wasEmpty := aq.queue.Len() == 0
	aq.queue.AddOrUpdate(pInfo)
	return wasEmpty
}

func (aq *activeQueue) pop() (*QueuedPodInfo, error) {
	aq.lock.Lock()
	defer aq.lock.Unlock()

	return aq.unlockedPop()
}

func (aq *activeQueue) unlockedPop() (*QueuedPodInfo, error) {
	for aq.queue.Len() == 0 {
		if aq.closed {
			return nil, nil
		}
		aq.cond.Wait()
	}
	pInfo, err := aq.queue.Pop()
	if err != nil {
		return nil, err
	}
	pInfo.Attempts++

	pInfo.UnschedulablePlugins.Clear()
	pInfo.PendingPlugins.Clear()

	return pInfo, nil
}

func (aq *activeQueue) done(pod types.UID) {
	aq.lock.Lock()
	defer aq.lock.Unlock()
}

type UnschedulablePods struct {
	podInfoMap map[string]*QueuedPodInfo
	keyFunc    func(*v1.Pod) string
}

func (u *UnschedulablePods) addOrUpdate(pInfo *QueuedPodInfo) {
	podID := u.keyFunc(pInfo.Pod)
	u.podInfoMap[podID] = pInfo
}

func (u *UnschedulablePods) delete(pod *v1.Pod) {
	podID := u.keyFunc(pod)
	delete(u.podInfoMap, podID)
}

func (u *UnschedulablePods) get(pod *v1.Pod) *QueuedPodInfo {
	podKey := u.keyFunc(pod)
	if pInfo, exists := u.podInfoMap[podKey]; exists {
		return pInfo
	}
	return nil
}

func (u *UnschedulablePods) clear() {
	u.podInfoMap = make(map[string]*QueuedPodInfo)
}

func newUnschedulablePods() *UnschedulablePods {
	return &UnschedulablePods{
		podInfoMap: make(map[string]*QueuedPodInfo),
		keyFunc:    utils.GetPodFullName,
	}
}

func podInfoKeyFunc(pInfo *QueuedPodInfo) string {
	return cache.NewObjectName(pInfo.Pod.Namespace, pInfo.Pod.Name).String()
}

type QueuedPodInfo struct {
	*utils.PodInfo
	Timestamp               time.Time
	Attempts                int
	BackoffExpiration       time.Time
	InitialAttemptTimestamp *time.Time
	UnschedulablePlugins    sets.Set[string]
	PendingPlugins          sets.Set[string]
}

func (pqi *QueuedPodInfo) DeepCopy() *QueuedPodInfo {
	return &QueuedPodInfo{
		PodInfo:                 pqi.PodInfo.DeepCopy(),
		Timestamp:               pqi.Timestamp,
		Attempts:                pqi.Attempts,
		InitialAttemptTimestamp: pqi.InitialAttemptTimestamp,
		UnschedulablePlugins:    pqi.UnschedulablePlugins.Clone(),
		PendingPlugins:          pqi.PendingPlugins.Clone(),
	}
}

func newQueuedPodInfoForLookup(pod *v1.Pod, plugins ...string) *QueuedPodInfo {
	return &QueuedPodInfo{
		PodInfo: &utils.PodInfo{Pod: pod},
	}
}

func isPodUpdated(oldPod, newPod *v1.Pod) bool {
	strip := func(pod *v1.Pod) *v1.Pod {
		p := pod.DeepCopy()
		p.ResourceVersion = ""
		p.Generation = 0
		p.Status = v1.PodStatus{
			ResourceClaimStatuses: pod.Status.ResourceClaimStatuses,
		}
		p.ManagedFields = nil
		p.Finalizers = nil
		return p
	}
	return !reflect.DeepEqual(strip(oldPod), strip(newPod))
}

func Less(pInfo1, pInfo2 *QueuedPodInfo) bool {
	p1 := PodPriority(pInfo1.Pod)
	p2 := PodPriority(pInfo2.Pod)
	return (p1 > p2) || (p1 == p2 && pInfo1.Timestamp.Before(pInfo2.Timestamp))
}

func PodPriority(pod *v1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}
	return 0
}
