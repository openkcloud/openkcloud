/*
Kubernetes event definitions

This module defines the specific Kubernetes cluster events (e.g., NodeAdd, PodDelete)
that the scheduler listens to in order to update its internal cache and queues.
*/
package scheduler

import (
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

type ActionType int64

const (
	Add ActionType = 1 << iota
	Delete
	UpdateNodeAllocatable
	UpdateNodeLabel
	UpdateNodeTaint
	UpdateNodeCondition
	UpdateNodeAnnotation
	UpdatePodLabel
	UpdatePodScaleDown
	UpdatePodTolerations
	UpdatePodSchedulingGatesEliminated
	UpdatePodGeneratedResourceClaim
	updatePodOther
	All    ActionType = 1<<iota - 1
	Update            = UpdateNodeAllocatable | UpdateNodeLabel | UpdateNodeTaint | UpdateNodeCondition | UpdateNodeAnnotation | UpdatePodLabel | UpdatePodScaleDown | UpdatePodTolerations | UpdatePodSchedulingGatesEliminated | UpdatePodGeneratedResourceClaim | updatePodOther
	none   ActionType = 0
)

func (a ActionType) String() string {
	switch a {
	case Add:
		return "Add"
	case Delete:
		return "Delete"
	case UpdateNodeAllocatable:
		return "UpdateNodeAllocatable"
	case UpdateNodeLabel:
		return "UpdateNodeLabel"
	case UpdateNodeTaint:
		return "UpdateNodeTaint"
	case UpdateNodeCondition:
		return "UpdateNodeCondition"
	case UpdateNodeAnnotation:
		return "UpdateNodeAnnotation"
	case UpdatePodLabel:
		return "UpdatePodLabel"
	case UpdatePodScaleDown:
		return "UpdatePodScaleDown"
	case UpdatePodTolerations:
		return "UpdatePodTolerations"
	case UpdatePodSchedulingGatesEliminated:
		return "UpdatePodSchedulingGatesEliminated"
	case UpdatePodGeneratedResourceClaim:
		return "UpdatePodGeneratedResourceClaim"
	case updatePodOther:
		return "Update"
	case All:
		return "All"
	case Update:
		return "Update"
	}

	return ""
}

var (
	basicActionTypes = []ActionType{Add, Delete, Update}
	podActionTypes = []ActionType{UpdatePodLabel, UpdatePodScaleDown, UpdatePodTolerations, UpdatePodSchedulingGatesEliminated, UpdatePodGeneratedResourceClaim}
	nodeActionTypes = []ActionType{UpdateNodeAllocatable, UpdateNodeLabel, UpdateNodeTaint, UpdateNodeCondition, UpdateNodeAnnotation}
)

type EventResource string

type ClusterEvent struct {
	Resource   EventResource
	ActionType ActionType

	label string
}

const (
	Pod                   EventResource = "Pod"
	AssignedPod           EventResource = "AssignedPod"
	UnschedulablePod      EventResource = "UnschedulablePod"
	Node                  EventResource = "Node"
	PersistentVolume      EventResource = "PersistentVolume"
	PersistentVolumeClaim EventResource = "PersistentVolumeClaim"
	CSINode               EventResource = "storage.k8s.io/CSINode"
	CSIDriver             EventResource = "storage.k8s.io/CSIDriver"
	VolumeAttachment      EventResource = "storage.k8s.io/VolumeAttachment"
	CSIStorageCapacity    EventResource = "storage.k8s.io/CSIStorageCapacity"
	StorageClass          EventResource = "storage.k8s.io/StorageClass"
	ResourceClaim         EventResource = "resource.k8s.io/ResourceClaim"
	ResourceSlice         EventResource = "resource.k8s.io/ResourceSlice"
	DeviceClass           EventResource = "resource.k8s.io/DeviceClass"
	WildCard              EventResource = "*"
)

var (
	allResources = []EventResource{
		Pod,
		AssignedPod,
		UnschedulablePod,
		Node,
		PersistentVolume,
		PersistentVolumeClaim,
		CSINode,
		CSIDriver,
		CSIStorageCapacity,
		StorageClass,
		VolumeAttachment,
		ResourceClaim,
		ResourceSlice,
		DeviceClass,
	}
)

type podChangeExtractor func(newPod *v1.Pod, oldPod *v1.Pod) ActionType

func PodSchedulingPropertiesChange(newPod *v1.Pod, oldPod *v1.Pod) (events []ClusterEvent) {
	r := AssignedPod
	if newPod.Spec.NodeName == "" {
		r = UnschedulablePod
	}

	podChangeExtracters := []podChangeExtractor{
		extractPodLabelsChange,
		extractPodScaleDown,
		extractPodSchedulingGateEliminatedChange,
		extractPodTolerationChange,
	}

	for _, fn := range podChangeExtracters {
		if event := fn(newPod, oldPod); event != none {
			events = append(events, ClusterEvent{Resource: r, ActionType: event})
		}
	}

	if len(events) == 0 {
		events = append(events, ClusterEvent{Resource: r, ActionType: updatePodOther})
	}

	return
}

func extractPodScaleDown(newPod, oldPod *v1.Pod) ActionType {

	newPodRequests := utils.PodRequests(newPod)
	oldPodRequests := utils.PodRequests(oldPod)

	for rName, oldReq := range oldPodRequests {
		newReq, ok := newPodRequests[rName]
		if !ok {
			return UpdatePodScaleDown
		}

		if oldReq.MilliValue() > newReq.MilliValue() {
			return UpdatePodScaleDown
		}
	}

	return none
}

func extractPodLabelsChange(newPod *v1.Pod, oldPod *v1.Pod) ActionType {
	if isLabelChanged(newPod.GetLabels(), oldPod.GetLabels()) {
		return UpdatePodLabel
	}
	return none
}

func isLabelChanged(newLabels map[string]string, oldLabels map[string]string) bool {
	return !equality.Semantic.DeepEqual(newLabels, oldLabels)
}

func extractPodTolerationChange(newPod *v1.Pod, oldPod *v1.Pod) ActionType {
	if len(newPod.Spec.Tolerations) != len(oldPod.Spec.Tolerations) {
		return UpdatePodTolerations
	}

	return none
}

func extractPodSchedulingGateEliminatedChange(newPod *v1.Pod, oldPod *v1.Pod) ActionType {
	if len(newPod.Spec.SchedulingGates) == 0 && len(oldPod.Spec.SchedulingGates) != 0 {
		return UpdatePodSchedulingGatesEliminated
	}

	return none
}

const (
	ScheduleAttemptFailure = "ScheduleAttemptFailure"
	BackoffComplete = "BackoffComplete"
	ForceActivate = "ForceActivate"
	UnschedulableTimeout = "UnschedulableTimeout"
)

var (
	EventAssignedPodAdd = ClusterEvent{Resource: AssignedPod, ActionType: Add}
	EventAssignedPodUpdate = ClusterEvent{Resource: AssignedPod, ActionType: Update}
	EventAssignedPodDelete = ClusterEvent{Resource: AssignedPod, ActionType: Delete}
	EventUnscheduledPodAdd = ClusterEvent{Resource: UnschedulablePod, ActionType: Add}
	EventUnscheduledPodUpdate = ClusterEvent{Resource: UnschedulablePod, ActionType: Update}
	EventUnscheduledPodDelete = ClusterEvent{Resource: UnschedulablePod, ActionType: Delete}
	EventUnschedulableTimeout = ClusterEvent{Resource: WildCard, ActionType: All, label: UnschedulableTimeout}
	EventForceActivate = ClusterEvent{Resource: WildCard, ActionType: All, label: ForceActivate}
)

func NodeSchedulingPropertiesChange(newNode *v1.Node, oldNode *v1.Node) (events []ClusterEvent) {
	nodeChangeExtracters := []nodeChangeExtractor{
		extractNodeSpecUnschedulableChange,
		extractNodeAllocatableChange,
		extractNodeLabelsChange,
		extractNodeTaintsChange,
		extractNodeConditionsChange,
		extractNodeAnnotationsChange,
	}

	for _, fn := range nodeChangeExtracters {
		if event := fn(newNode, oldNode); event != none {
			events = append(events, ClusterEvent{Resource: Node, ActionType: event})
		}
	}
	return
}

type nodeChangeExtractor func(newNode *v1.Node, oldNode *v1.Node) ActionType

func extractNodeAllocatableChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	if !equality.Semantic.DeepEqual(oldNode.Status.Allocatable, newNode.Status.Allocatable) {
		return UpdateNodeAllocatable
	}
	return none
}

func extractNodeLabelsChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	if isLabelChanged(newNode.GetLabels(), oldNode.GetLabels()) {
		return UpdateNodeLabel
	}
	return none
}

func extractNodeTaintsChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	if !equality.Semantic.DeepEqual(newNode.Spec.Taints, oldNode.Spec.Taints) {
		return UpdateNodeTaint
	}
	return none
}

func extractNodeConditionsChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	strip := func(conditions []v1.NodeCondition) map[v1.NodeConditionType]v1.ConditionStatus {
		conditionStatuses := make(map[v1.NodeConditionType]v1.ConditionStatus, len(conditions))
		for i := range conditions {
			conditionStatuses[conditions[i].Type] = conditions[i].Status
		}
		return conditionStatuses
	}
	if !equality.Semantic.DeepEqual(strip(oldNode.Status.Conditions), strip(newNode.Status.Conditions)) {
		return UpdateNodeCondition
	}
	return none
}

func extractNodeSpecUnschedulableChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	if newNode.Spec.Unschedulable != oldNode.Spec.Unschedulable && !newNode.Spec.Unschedulable {
		return UpdateNodeTaint
	}
	return none
}

func extractNodeAnnotationsChange(newNode *v1.Node, oldNode *v1.Node) ActionType {
	if !equality.Semantic.DeepEqual(oldNode.GetAnnotations(), newNode.GetAnnotations()) {
		return UpdateNodeAnnotation
	}
	return none
}
