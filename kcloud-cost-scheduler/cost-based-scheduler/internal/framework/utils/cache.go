/*
In-memory scheduling state cache

This module manages the scheduler's view of the cluster, efficiently tracking
node availability, active pods, and image deployments to avoid redundant API calls.
*/
package utils

import (
	"context"
	"fmt"
	"sync"
	"time"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Cache struct {
	stop               <-chan struct{}
	mu                 sync.RWMutex
	nodes              map[string]*NodeInfo          // 클러스터 내 모든 노드 정보
	podStates          map[string]*podState          // 클러스터 내 모든 Pod 상태 정보
	assumedPods        sets.Set[string]              // 스케줄링 결정 후 아직 바인딩되지 않은 Pod 목록
	imageStates        map[string]*ImageStateSummary // 클러스터 내 컨테이너 이미지 분포 정보
	totalNodeCount     int
	availableNodeCount int
}

func NewCache(ctx context.Context) *Cache {
	return &Cache{
		stop:               ctx.Done(),
		nodes:              make(map[string]*NodeInfo),
		podStates:          make(map[string]*podState),
		assumedPods:        sets.New[string](),
		imageStates:        make(map[string]*ImageStateSummary),
		totalNodeCount:     0,
		availableNodeCount: 0,
	}
}

func (cache *Cache) Nodes() map[string]*NodeInfo {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	
	// Return a copy of the map to prevent external modification of the internal cache.
	// This ensures immutability of the returned map.
	nodesCopy := make(map[string]*NodeInfo, len(cache.nodes))
	for k, v := range cache.nodes {
		nodesCopy[k] = v
	}
	return nodesCopy
}

func (cache *Cache) InitCache(client *kubernetes.Clientset) error {
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.Info("캐시 초기화 실패")
		return err
	}

	for _, node := range nodes.Items {
		cache.AddNode(&node, client)
	}

	return nil
}

func (cache *Cache) NodeInfoExist(pod *corev1.Pod) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.nodes[pod.Spec.NodeName]
	return ok
}

func (cache *Cache) AddNode(node *corev1.Node, client *kubernetes.Clientset) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodes[node.Name]
	if !ok {
		n = NewNodeInfo()
		cache.nodes[node.Name] = n
		cache.totalNodeCount++
	} else {
		cache.removeNodeImageStates(node)
	}

	cache.addNodeImageStates(node, n)
	n.SetNode(node)
	return nil
}

func (cache *Cache) UpdateNode(oldNode, newNode *corev1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodes[newNode.Name]
	if !ok {
		n = NewNodeInfo()
		cache.nodes[newNode.Name] = n
	} else {
		cache.removeNodeImageStates(n.Node())
	}

	cache.addNodeImageStates(newNode, n)
	n.SetNode(newNode)

	return nil
}

func (cache *Cache) RemoveNode(node *corev1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodes[node.Name]
	if !ok {
		return fmt.Errorf("노드 %v를 찾을 수 없음", node.Name)
	}

	n.RemoveNode()
	cache.removeNodeImageStates(node)
	delete(cache.nodes, node.Name)
	cache.totalNodeCount--

	return nil
}

func (cache *Cache) NodeCount() int {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return len(cache.nodes)
}

func (cache *Cache) PodCount() (int, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	count := 0
	for _, n := range cache.nodes {
		count += len(n.Pods)
	}
	return count, nil
}

func (cache *Cache) AddPod(pod *corev1.Pod) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	switch {
	case ok && cache.assumedPods.Has(key):
		if err = cache.updatePod(currState.pod, pod); err != nil {
			return err
		}
	case !ok:
		if err = cache.addPod(pod, false); err != nil {
			return err
		}
	default:
		return fmt.Errorf("Pod %v가 이미 캐시에 존재함", key)
	}
	return nil
}

func (cache *Cache) addPod(pod *corev1.Pod, assumePod bool) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}
	n, ok := cache.nodes[pod.Spec.NodeName]
	if !ok {
		n = NewNodeInfo()
		cache.nodes[pod.Spec.NodeName] = n
		cache.totalNodeCount++
	}
	n.AddPod(pod)
	ps := &podState{
		pod: pod,
	}
	cache.podStates[key] = ps
	if assumePod {
		cache.assumedPods.Insert(key)
	}
	return nil
}

func (cache *Cache) UpdatePod(oldPod, newPod *corev1.Pod) error {
	key, err := GetPodKey(oldPod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	if _, ok := cache.podStates[key]; !ok {
		return fmt.Errorf("업데이트할 Pod %v를 찾을 수 없음", key)
	}

	if cache.assumedPods.Has(key) {
		cache.assumedPods.Delete(key)
	}

	return cache.updatePod(oldPod, newPod)
}

func (cache *Cache) updatePod(oldPod, newPod *corev1.Pod) error {
	if err := cache.removePod(oldPod); err != nil {
		return err
	}
	return cache.addPod(newPod, false)
}

func (cache *Cache) RemovePod(pod *corev1.Pod) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	if !ok {
		return fmt.Errorf("제거할 Pod %v를 찾을 수 없음", key)
	}

	return cache.removePod(currState.pod)
}

func (cache *Cache) removePod(pod *corev1.Pod) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	if n, ok := cache.nodes[pod.Spec.NodeName]; ok {
		if err := n.RemovePod(pod); err != nil {
			return err
		}
		if len(n.Pods) == 0 && n.Node() == nil {
			delete(cache.nodes, pod.Spec.NodeName)
		}
	}

	delete(cache.podStates, key)
	delete(cache.assumedPods, key)
	return nil
}

func (cache *Cache) AssumePod(logger klog.Logger, pod *corev1.Pod) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.podStates[key]; ok {
		return fmt.Errorf("이미 캐시에 존재하는 Pod %v는 Assume할 수 없음", key)
	}

	return cache.addPod(pod, true)
}

func (cache *Cache) IsConfirmedPod(pod *corev1.Pod) (bool, error) {
	key, err := GetPodKey(pod)
	if err != nil {
		return false, err
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, inStates := cache.podStates[key]
	inAssumed := cache.assumedPods.Has(key)
	return inStates && !inAssumed, nil
}

func (cache *Cache) IsAssumedPod(pod *corev1.Pod) (bool, error) {
	key, err := GetPodKey(pod)
	if err != nil {
		return false, err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.assumedPods.Has(key), nil
}

func (cache *Cache) FinishBinding(pod *corev1.Pod) error {
	key, err := GetPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	if currState, ok := cache.podStates[key]; ok && cache.assumedPods.Has(key) {
		currState.bindingFinished = true
	}
	return nil
}

func (cache *Cache) addNodeImageStates(node *corev1.Node, nodeInfo *NodeInfo) {
	newSum := make(map[string]*ImageStateSummary)

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			state, ok := cache.imageStates[name]
			if !ok {
				state = &ImageStateSummary{
					size:  image.SizeBytes,
					nodes: sets.New(node.Name),
				}
				cache.imageStates[name] = state
			} else {
				state.nodes.Insert(node.Name)
			}
			newSum[name] = state
		}
	}
	nodeInfo.ImageStates = newSum
}

func (cache *Cache) removeNodeImageStates(node *corev1.Node) {
	if node == nil {
		return
	}

	for _, image := range node.Status.Images {
		for _, name := range image.Names {
			if state, ok := cache.imageStates[name]; ok {
				state.nodes.Delete(node.Name)
				if len(state.nodes) == 0 {
					delete(cache.imageStates, name)
				}
			}
		}
	}
}

type ImageStateSummary struct {
	size  int64
	nodes sets.Set[string]
}

func (cache *Cache) UpdateNodeGPUMetrics(nodeName string, gpuMetrics any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	nodeInfo, ok := cache.nodes[nodeName]
	if !ok {
		return fmt.Errorf("노드 %s를 캐시에서 찾을 수 없음", nodeName)
	}

	nodeInfo.GPUMetricsUpdatedAt = time.Now()
	return nil
}
