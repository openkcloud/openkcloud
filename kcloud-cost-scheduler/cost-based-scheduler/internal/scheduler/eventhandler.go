/*
Cluster event handlers

This module implements the event handlers that react to changes in the cluster state,
updating the in-memory cache and moving pods between queues accordingly.
*/
package scheduler

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
)

func AddAllEventHandlers(
	sched *Scheduler,
	informerFactory informers.SharedInformerFactory,
) error {
	informerFactory.Core().V1().Pods().Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj any) bool {
				switch t := obj.(type) {
				case *v1.Pod:
					return assignedPod(t) 
				case cache.DeletedFinalStateUnknown:
					if _, ok := t.Obj.(*v1.Pod); ok {
						return true
					}
					return false
				default:
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.addPodToCache,
				UpdateFunc: sched.updatePodInCache,
				DeleteFunc: sched.deletePodFromCache,
			},
		},
	)

	informerFactory.Core().V1().Pods().Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj any) bool {
				switch t := obj.(type) {
				case *v1.Pod:
					return !assignedPod(t) && responsibleForPod(t)
				case cache.DeletedFinalStateUnknown:
					return false
				default:
					logger.Warn(fmt.Sprintf("[error] unable to handle object in %T: %T\n", sched, obj))
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.addPodToSchedulingQueue,
				UpdateFunc: sched.updatePodInSchedulingQueue,
				DeleteFunc: sched.deletePodFromSchedulingQueue,
			},
		},
	)

	informerFactory.Core().V1().Nodes().Informer().AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj any) bool {
				switch obj.(type) {
				case *v1.Node:
					return true
				case cache.DeletedFinalStateUnknown:
					return false
				default:
					logger.Warn(fmt.Sprintf("[error] unable to handle object in %T: %T\n", sched, obj))
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    sched.addNodeToCache,
				UpdateFunc: sched.updateNodeInCache,
				DeleteFunc: sched.deleteNodeFromCache,
			},
		},
	)

	return nil
}

func (sched *Scheduler) addNodeToCache(obj any) {
	node, ok := obj.(*v1.Node)
	if !ok {
		logger.Warn(fmt.Sprintf("[error] cannot convert to *v1.Node -> %+v", obj))
		return
	}

	logger.Info(fmt.Sprintf("[event] add new node {%s} to cache\n", node.Name))

	err := sched.Cache.AddNode(node, sched.schedulerConfig.HostKubeClient)
	if err != nil {
		klog.ErrorS(nil, "cannot add node [", node.Name, "]")
	}

	go sched.fetchNodeGPUMetrics(node.Name)

	sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue()
}

func (sched *Scheduler) updateNodeInCache(oldObj, newObj any) {
	oldNode, ok := oldObj.(*v1.Node)
	if !ok {
		klog.ErrorS(nil, "cannot convert oldObj to *v1.Node", "oldObj", oldObj)
		return
	}

	newNode, ok := newObj.(*v1.Node)
	if !ok {
		klog.ErrorS(nil, "cannot convert newObj to *v1.Node", "newObj", newObj)
		return
	}

	err := sched.Cache.UpdateNode(oldNode, newNode)
	if err != nil {
		klog.ErrorS(nil, "cannot Update Node [", newNode.Name, "]")
	}

	go sched.fetchNodeGPUMetrics(newNode.Name)

	if event := NodeSchedulingPropertiesChange(newNode, oldNode); event != nil {
		sched.SchedulingQueue.MoveAllToActiveOrBackoffQueue()
	}
}

func (sched *Scheduler) deleteNodeFromCache(obj any) {
	var node *v1.Node
	switch t := obj.(type) {
	case *v1.Node:
		node = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		node, ok = t.Obj.(*v1.Node)
		if !ok {
			return
		}
	default:
		return
	}


	if err := sched.Cache.RemoveNode(node); err != nil {
	}
}

func (sched *Scheduler) addPodToSchedulingQueue(obj any) {
	pod := obj.(*v1.Pod)
	gpuRequest := "none"
	if len(pod.Spec.Containers) > 0 {
		if gpu, ok := pod.Spec.Containers[0].Resources.Requests["nvidia.com/gpu"]; ok {
			gpuRequest = gpu.String()
		}
	}
	logger.Info(fmt.Sprintf("[event] add pod {%s/%s} to scheduling queue (GPU: %s)\n",
		pod.Namespace, pod.Name, gpuRequest))
	sched.SchedulingQueue.Add(pod)
}

func (sched *Scheduler) updatePodInSchedulingQueue(oldObj, newObj any) {
	oldPod, newPod := oldObj.(*v1.Pod), newObj.(*v1.Pod)
	if oldPod.ResourceVersion == newPod.ResourceVersion {
		return
	}

	isAssumed, err := sched.Cache.IsAssumedPod(newPod)
	if err != nil {
	}
	if isAssumed {
		return
	}

	if err := sched.SchedulingQueue.Update(oldPod, newPod); err != nil {
		logger.Warn(fmt.Sprintf("[error] unable to update %T: %v\n", newObj, err))
	}
}

func (sched *Scheduler) deletePodFromSchedulingQueue(obj any) {
	var pod *v1.Pod
	switch t := obj.(type) {
	case *v1.Pod:
		pod = obj.(*v1.Pod)
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*v1.Pod)
		if !ok {
			logger.Warn(fmt.Sprintf("[error] unable to convert object %T to *v1.Pod in %T\n", obj, sched))
			return
		}
	default:
		logger.Warn(fmt.Sprintf("[error] unable to handle object in %T: %T\n", sched, obj))
		return
	}

	if err := sched.SchedulingQueue.Delete(pod); err != nil {
		logger.Warn(fmt.Sprintf("[error] unable to dequeue %T: %v\n", obj, err))
	}

}

func (sched *Scheduler) addPodToCache(obj any) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return
	}

	if err := sched.Cache.AddPod(pod); err != nil {
		logger.Warn(fmt.Sprintf("[event] cache AddPod 실패: pod=%s/%s err=%v", pod.Namespace, pod.Name, err))
	}
}

func (sched *Scheduler) updatePodInCache(oldObj, newObj any) {
	oldPod, ok := oldObj.(*v1.Pod)
	if !ok {
		return
	}
	newPod, ok := newObj.(*v1.Pod)
	if !ok {
		return
	}

	if err := sched.Cache.UpdatePod(oldPod, newPod); err != nil {
		logger.Warn(fmt.Sprintf("[event] cache UpdatePod 실패: pod=%s/%s err=%v", oldPod.Namespace, oldPod.Name, err))
	}
}

func (sched *Scheduler) deletePodFromCache(obj any) {
	var pod *v1.Pod
	switch t := obj.(type) {
	case *v1.Pod:
		pod = t
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*v1.Pod)
		if !ok {
			logger.Warn(fmt.Sprintf("cannot convert to *v1.Pod -> %+v", t.Obj))
			return
		}
	default:
		logger.Warn(fmt.Sprintf("cannot convert to *v1.Pod -> %+v", t))
		return
	}

	logger.Info(fmt.Sprintf("[event] delete pod {%s} from cache\n", pod.Name))
	if err := sched.Cache.RemovePod(pod); err != nil {
		klog.ErrorS(err, "[error] scheduler cache remove pod failed", "pod", klog.KObj(pod))
	}

}

func assignedPod(pod *v1.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

func responsibleForPod(pod *v1.Pod) bool {
	responsibleForPod := (pod.Spec.SchedulerName == "cost-based-scheduler")
	return responsibleForPod
}
