/*
Pod nomination tracking

This module tracks pods that have been evaluated but not yet bound, ensuring
their requested resources are temporarily reserved to prevent over-allocation.
*/
package queue

import (
	"slices"
	"sync"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

type nominator struct {
	nLock sync.RWMutex

	podLister listersv1.PodLister
	nominatedPods map[string][]podRef
	nominatedPodToNode map[types.UID]string
}

func newPodNominator(podLister listersv1.PodLister) *nominator {
	return &nominator{
		podLister:          podLister,
		nominatedPods:      make(map[string][]podRef),
		nominatedPodToNode: make(map[types.UID]string),
	}
}

func (npm *nominator) AddNominatedPod(logger klog.Logger, pi *utils.PodInfo, nominatingInfo *utils.NominatingInfo) {
	npm.nLock.Lock()
	npm.addNominatedPodUnlocked(logger, pi, nominatingInfo)
	npm.nLock.Unlock()
}

func (npm *nominator) addNominatedPodUnlocked(logger klog.Logger, pi *utils.PodInfo, nominatingInfo *utils.NominatingInfo) {
	npm.deleteUnlocked(pi.Pod)

	var nodeName string
	if nominatingInfo.Mode() == utils.ModeOverride {
		nodeName = nominatingInfo.NominatedNodeName
	} else if nominatingInfo.Mode() == utils.ModeNoop {
		if pi.Pod.Status.NominatedNodeName == "" {
			return
		}
		nodeName = pi.Pod.Status.NominatedNodeName
	}

	if npm.podLister != nil {
		updatedPod, err := npm.podLister.Pods(pi.Pod.Namespace).Get(pi.Pod.Name)
		if err != nil {
			logger.V(4).Info("Pod doesn't exist in podLister, aborted adding it to the nominator", "pod", klog.KObj(pi.Pod))
			return
		}
		if updatedPod.Spec.NodeName != "" {
			logger.V(4).Info("Pod is already scheduled to a node, aborted adding it to the nominator", "pod", klog.KObj(pi.Pod), "node", updatedPod.Spec.NodeName)
			return
		}
	}

	npm.nominatedPodToNode[pi.Pod.UID] = nodeName
	for _, np := range npm.nominatedPods[nodeName] {
		if np.uid == pi.Pod.UID {
			logger.V(4).Info("Pod already exists in the nominator", "pod", np.uid)
			return
		}
	}
	npm.nominatedPods[nodeName] = append(npm.nominatedPods[nodeName], podToRef(pi.Pod))
}

func (npm *nominator) UpdateNominatedPod(logger klog.Logger, oldPod *v1.Pod, newPodInfo *utils.PodInfo) {
	npm.nLock.Lock()
	defer npm.nLock.Unlock()
	var nominatingInfo *utils.NominatingInfo
	if nominatedNodeName(oldPod) == "" && nominatedNodeName(newPodInfo.Pod) == "" {
		if nnn, ok := npm.nominatedPodToNode[oldPod.UID]; ok {
			nominatingInfo = &utils.NominatingInfo{
				NominatingMode:    utils.ModeOverride,
				NominatedNodeName: nnn,
			}
		}
	}
	npm.deleteUnlocked(oldPod)
	npm.addNominatedPodUnlocked(logger, newPodInfo, nominatingInfo)
}

func (npm *nominator) DeleteNominatedPodIfExists(pod *v1.Pod) {
	npm.nLock.Lock()
	npm.deleteUnlocked(pod)
	npm.nLock.Unlock()
}

func (npm *nominator) deleteUnlocked(p *v1.Pod) {
	nnn, ok := npm.nominatedPodToNode[p.UID]
	if !ok {
		return
	}
	for i, np := range npm.nominatedPods[nnn] {
		if np.uid == p.UID {
			npm.nominatedPods[nnn] = append(npm.nominatedPods[nnn][:i], npm.nominatedPods[nnn][i+1:]...)
			if len(npm.nominatedPods[nnn]) == 0 {
				delete(npm.nominatedPods, nnn)
			}
			break
		}
	}
	delete(npm.nominatedPodToNode, p.UID)
}

func (npm *nominator) nominatedPodsForNode(nodeName string) []podRef {
	npm.nLock.RLock()
	defer npm.nLock.RUnlock()
	return slices.Clone(npm.nominatedPods[nodeName])
}

func nominatedNodeName(pod *v1.Pod) string {
	return pod.Status.NominatedNodeName
}

type podRef struct {
	name      string
	namespace string
	uid       types.UID
}

func podToRef(pod *v1.Pod) podRef {
	return podRef{
		name:      pod.Name,
		namespace: pod.Namespace,
		uid:       pod.UID,
	}
}

func (np podRef) toPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      np.name,
			Namespace: np.namespace,
			UID:       np.uid,
		},
	}
}
