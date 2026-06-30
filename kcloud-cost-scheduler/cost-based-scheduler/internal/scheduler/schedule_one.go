/*
Single pod scheduling cycle

This module implements the core logic for scheduling a single pod, orchestrating
the execution of all framework phases from PreFilter through Bind.
*/
package scheduler

import (
	"context"
	"fmt"
	"time"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	internalqueue "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/queue"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

func (sched *Scheduler) ScheduleOne(ctx context.Context) {
	podInfo, err := sched.NextPod()
	if err != nil {
		logger.Warn(fmt.Sprintf("[schedule] 큐에서 Pod 획득 실패: %v", err))
		return
	}
	if podInfo == nil || podInfo.Pod == nil {
		return // 큐가 닫힌 경우 종료
	}

	pod := podInfo.Pod

	fwk, err := sched.frameworkForPod(pod)
	if err != nil {
		logger.Warn(fmt.Sprintf("[schedule] 프레임워크 조회 실패, pod=%s/%s: %v",
			pod.Namespace, pod.Name, err))
		sched.SchedulingQueue.Done(pod.UID)
		return
	}

	start := time.Now()
	schedulingCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if pod.Labels["sched.ai/evicted-by-rescheduler"] == "true" {
		logger.Info(fmt.Sprintf("[schedule] 재스케줄러 축출 Pod 감지: pod=%s/%s",
			pod.Namespace, pod.Name))
	}

	logger.Info(fmt.Sprintf("[schedule] 사이클 시작: pod=%s/%s", pod.Namespace, pod.Name))

	scheduleResult, assumedPodInfo, status := sched.schedulingCycle(schedulingCycleCtx, fwk, podInfo, start)
	if !status.IsSuccess() {
		sched.SchedulingQueue.Done(pod.UID)
		if confirmed, _ := sched.Cache.IsConfirmedPod(pod); confirmed {
			logger.Info(fmt.Sprintf("[schedule] 이미 스케줄링 완료, 재큐 생략: pod=%s/%s",
				pod.Namespace, pod.Name))
			return
		}
		logger.Warn(fmt.Sprintf("[schedule] 스케줄링 실패, 재큐: pod=%s/%s reason=%s",
			pod.Namespace, pod.Name, status.Message()))
		sched.SchedulingQueue.Add(pod)
		return
	}

	logger.Info(fmt.Sprintf("[schedule] 노드 선택 완료: pod=%s/%s → node=%s (elapsed=%s)",
		pod.Namespace, pod.Name, scheduleResult.SuggestedHost, time.Since(start)))

	go func() {
		bindingCycleCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		status := sched.bindingCycle(bindingCycleCtx, fwk, scheduleResult, assumedPodInfo, start)
		if !status.IsSuccess() {
			logger.Warn(fmt.Sprintf("[schedule] 바인딩 실패: pod=%s/%s node=%s reason=%s",
				assumedPodInfo.Pod.Namespace, assumedPodInfo.Pod.Name,
				scheduleResult.SuggestedHost, status.Message()))
		}
	}()
}

func (sched *Scheduler) frameworkForPod(pod *v1.Pod) (framework.Framework, error) {
	return sched.fwk, nil
}

var clearNominatedNode = &NominatingInfo{NominatingMode: ModeOverride, NominatedNodeName: ""}

func (sched *Scheduler) schedulingCycle(
	ctx context.Context,
	fwk framework.Framework,
	podInfo *internalqueue.QueuedPodInfo,
	start time.Time) (ScheduleResult, *internalqueue.QueuedPodInfo, *utils.Status) {

	klogger := klog.FromContext(ctx)
	pod := podInfo.Pod

	if preStatus := fwk.RunPreFilterPlugins(ctx, pod); !preStatus.IsSuccess() {
		logger.Warn(fmt.Sprintf("[schedule] PreFilter 실패: pod=%s/%s reason=%s",
			pod.Namespace, pod.Name, preStatus.Message()))
		return ScheduleResult{nominatingInfo: clearNominatedNode}, podInfo,
			utils.NewStatus(utils.Unschedulable).WithError(fmt.Errorf("%s", preStatus.Message()))
	}

	scheduleResult, err := sched.schedulePod(ctx, fwk, pod)
	if err != nil {
		logger.Warn(fmt.Sprintf("[schedule] 노드 선택 실패: pod=%s/%s err=%v",
			pod.Namespace, pod.Name, err))
		if err == ErrNoNodesAvailable {
			return ScheduleResult{nominatingInfo: clearNominatedNode}, podInfo,
				utils.NewStatus(utils.UnschedulableAndUnresolvable).WithError(err)
		}
		return ScheduleResult{}, podInfo, utils.NewStatus(utils.Unschedulable).WithError(err)
	}

	assumedPodInfo := podInfo.DeepCopy()
	assumedPod := assumedPodInfo.Pod

	if err = sched.assume(klogger, assumedPod, scheduleResult.SuggestedHost); err != nil {
		return ScheduleResult{nominatingInfo: clearNominatedNode}, assumedPodInfo, utils.AsStatus(err)
	}

	return scheduleResult, assumedPodInfo, nil
}

func (sched *Scheduler) assume(klogger klog.Logger, assumed *v1.Pod, host string) error {
	assumed.Spec.NodeName = host

	if err := sched.Cache.AssumePod(klogger, assumed); err != nil {
		klogger.Error(err, "스케줄러 캐시 AssumePod 실패")
		return err
	}
	if sched.SchedulingQueue != nil {
		sched.SchedulingQueue.DeleteNominatedPodIfExists(assumed)
	}
	return nil
}

func (sched *Scheduler) bindingCycle(
	ctx context.Context,
	fwk framework.Framework,
	scheduleResult ScheduleResult,
	assumedPodInfo *internalqueue.QueuedPodInfo,
	start time.Time) *utils.Status {

	assumedPod := assumedPodInfo.Pod

	if permitStatus := fwk.RunPermitPlugins(ctx, assumedPod, scheduleResult.SuggestedHost); !permitStatus.IsSuccess() {
		logger.Warn(fmt.Sprintf("[bind] Permit 거부, 재큐: pod=%s/%s reason=%s",
			assumedPod.Namespace, assumedPod.Name, permitStatus.Message()))
		_ = sched.Cache.RemovePod(assumedPod)
		sched.SchedulingQueue.Done(assumedPod.UID)
		sched.SchedulingQueue.Add(assumedPod)
		return utils.NewStatus(utils.Unschedulable, permitStatus.Message())
	}

	if hasGPURequest(assumedPod) && isHAMiManagedNode(sched, scheduleResult.SuggestedHost) {
		logger.Info(fmt.Sprintf("[HAMi] bind 시도: pod=%s/%s → node=%s",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost))

		if err := sched.hamiClient.Bind(assumedPod, scheduleResult.SuggestedHost); err != nil {
			logger.Error(fmt.Sprintf("[HAMi] bind 실패, 재큐: pod=%s/%s err=%v",
				assumedPod.Namespace, assumedPod.Name, err), err)
			_ = sched.Cache.RemovePod(assumedPod)
			sched.SchedulingQueue.Done(assumedPod.UID)
			sched.SchedulingQueue.Add(assumedPod)
			return utils.NewStatus(utils.Error, err.Error())
		}

		logger.Info(fmt.Sprintf("[HAMi] bind 완료: pod=%s/%s node=%s",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost))
		sched.finishBinding(fwk, assumedPod, scheduleResult.SuggestedHost)
		sched.SchedulingQueue.Done(assumedPod.UID)
		return nil
	}

	if err := sched.runBindPlugin(ctx, fwk, assumedPod, &scheduleResult); err != nil {
		logger.Warn(fmt.Sprintf("[bind] DefaultBinder 실패, 재큐: pod=%s/%s node=%s err=%v",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost, err))
		_ = sched.Cache.RemovePod(assumedPod)
		sched.SchedulingQueue.Done(assumedPod.UID)
		sched.SchedulingQueue.Add(assumedPod)
		return utils.NewStatus(utils.Error, err.Error())
	}

	sched.SchedulingQueue.Done(assumedPod.UID)
	return nil
}

func (sched *Scheduler) schedulePod(ctx context.Context, fwk framework.Framework, pod *v1.Pod) (result ScheduleResult, err error) {
	if sched.Cache.NodeCount() == 0 {
		return result, ErrNoNodesAvailable
	}

	if hasGPURequest(pod) {
		sched.refreshStaleGPUMetrics(ctx, 60000)
	}

	nodes := sched.Cache.Nodes()
	if len(nodes) == 0 {
		return result, ErrNoNodesAvailable
	}
	scheduleResult := NewScheduleResult(nodes)

	if err = sched.runFilterPlugin(ctx, fwk, pod, &scheduleResult); err != nil {
		return result, err
	}

	if hasGPURequest(pod) {
		feasibleNodes := make([]*v1.Node, 0)
		for nodeName, pr := range scheduleResult.PluginResultMap {
			if !pr.IsFiltered {
				if nodeInfo := sched.Cache.Nodes()[nodeName]; nodeInfo != nil && nodeInfo.Node() != nil {
					feasibleNodes = append(feasibleNodes, nodeInfo.Node())
				}
			}
		}

		if len(feasibleNodes) > 0 {
			hamiFilteredNodes, hamiErr := sched.hamiClient.Filter(pod, feasibleNodes)
			if hamiErr == nil {
				keepSet := make(map[string]bool, len(hamiFilteredNodes))
				for _, n := range hamiFilteredNodes {
					keepSet[n.Name] = true
				}
				newFeasible := 0
				for nodeName, pr := range scheduleResult.PluginResultMap {
					if !pr.IsFiltered {
						if !keepSet[nodeName] {
							pr.IsFiltered = true
							scheduleResult.PluginResultMap[nodeName] = pr
						} else {
							newFeasible++
						}
					}
				}
				scheduleResult.FeasibleNodes = newFeasible
			}
		}
	}

	if scheduleResult.FeasibleNodes == 0 {
		return result, fmt.Errorf("대상 Pod %s/%s를 배치할 수 있는 노드가 없음: 모든 노드 필터링됨",
			pod.Namespace, pod.Name)
	}

	if scheduleResult.FeasibleNodes == 1 {
		for name, pr := range scheduleResult.PluginResultMap {
			if !pr.IsFiltered {
				scheduleResult.SuggestedHost = name
				return scheduleResult, nil
			}
		}
		return result, fmt.Errorf("가용 노드 수 불일치: pod %s/%s", pod.Namespace, pod.Name)
	}

	if err = sched.runScorePlugin(ctx, fwk, pod, &scheduleResult); err != nil {
		return result, err
	}

	err = sched.selectResource(pod, &scheduleResult)
	return scheduleResult, err
}

func (sched *Scheduler) runFilterPlugin(ctx context.Context, fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	nodes := sched.Cache.Nodes()
	feasibleNodes := 0

	for nodeName, nodeInfo := range nodes {
		pluginResults := fwk.RunFilterPlugins(ctx, pod, nodeInfo)
		if pr, exists := pluginResults[nodeName]; exists {
			existingResult := scheduleResult.PluginResultMap[nodeName]
			existingResult.IsFiltered = pr.IsFiltered
			scheduleResult.PluginResultMap[nodeName] = existingResult
			if !pr.IsFiltered {
				feasibleNodes++
			}
		}
	}

	scheduleResult.FeasibleNodes = feasibleNodes
	return nil
}

func (sched *Scheduler) runScorePlugin(ctx context.Context, fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	klogger := klog.FromContext(ctx)

	feasibleNodes := make([]*v1.Node, 0)
	for nodeName, pr := range scheduleResult.PluginResultMap {
		if !pr.IsFiltered {
			if nodeInfo := sched.Cache.Nodes()[nodeName]; nodeInfo != nil && nodeInfo.Node() != nil {
				feasibleNodes = append(feasibleNodes, nodeInfo.Node())
			}
		}
	}

	if len(feasibleNodes) == 0 {
		return fmt.Errorf("스코어링을 수행할 수 있는 노드가 없음")
	}

	scores, status := fwk.RunScorePlugins(ctx, pod, feasibleNodes)
	if !status.IsSuccess() {
		return fmt.Errorf("스코어링 실패: %s", status.Message())
	}

	for nodeName, score := range scores {
		if existing, ok := scheduleResult.PluginResultMap[nodeName]; ok {
			existing.Scores = score.Scores
			existing.TotalNodeScore = score.TotalNodeScore
			scheduleResult.PluginResultMap[nodeName] = existing

			for _, pluginScore := range score.Scores {
				klogger.V(4).Info("플러그인 점수",
					"pod", klog.KObj(pod),
					"node", nodeName,
					"plugin", pluginScore.PluginName,
					"score", pluginScore.Score)
			}
			klogger.V(3).Info("노드 합계 점수",
				"pod", klog.KObj(pod),
				"node", nodeName,
				"totalScore", score.TotalNodeScore)
		}
	}

	return nil
}

func (sched *Scheduler) selectResource(pod *v1.Pod, scheduleResult *ScheduleResult) error {
	var bestNode string
	bestScore := -1

	for nodeName, pr := range scheduleResult.PluginResultMap {
		if !pr.IsFiltered && pr.TotalNodeScore > bestScore {
			bestScore = pr.TotalNodeScore
			bestNode = nodeName
		}
	}

	if bestNode == "" {
		return fmt.Errorf("Pod %s/%s를 위한 적절한 노드를 찾을 수 없음", pod.Namespace, pod.Name)
	}

	scheduleResult.SuggestedHost = bestNode
	return nil
}

func (sched *Scheduler) runBindPlugin(ctx context.Context, fwk framework.Framework, assumed *v1.Pod, scheduleResult *ScheduleResult) error {
	defer func() {
		sched.finishBinding(fwk, assumed, scheduleResult.SuggestedHost)
	}()

	status := fwk.RunBindPlugin(ctx, assumed, scheduleResult.SuggestedHost)
	if !status.IsSuccess() {
		return fmt.Errorf("바인딩 실패: %s", status.Message())
	}
	return nil
}

func (sched *Scheduler) finishBinding(fwk framework.Framework, assumed *v1.Pod, targetNode string) {
	_ = sched.Cache.FinishBinding(assumed)
}

func hasGPURequest(pod *v1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if _, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
			return true
		}
		if _, ok := container.Resources.Limits["nvidia.com/gpu"]; ok {
			return true
		}
	}
	return false
}

func isHAMiManagedNode(sched *Scheduler, nodeName string) bool {
	nodes := sched.Cache.Nodes()
	nodeInfo, ok := nodes[nodeName]
	if !ok || nodeInfo.Node() == nil {
		return false
	}
	return nodeInfo.Node().Labels["gpu.management"] == "hami"
}
