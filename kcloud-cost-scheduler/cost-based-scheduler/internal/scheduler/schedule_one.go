/*
Single pod scheduling cycle

This module implements the core logic for scheduling a single pod, orchestrating
the execution of all framework phases from PreFilter through Bind.
*/
package scheduler

import (
	"context"
	"fmt"
	"os"
	"time"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	internalqueue "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/queue"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	"github.com/google/uuid"
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
		logger.Warn(fmt.Sprintf("[schedule] 스케줄링 실패, 재큐(backoff): pod=%s/%s reason=%s attempts=%d",
			pod.Namespace, pod.Name, status.Message(), podInfo.Attempts))
		sched.SchedulingQueue.AddUnschedulable(podInfo)
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

	// Attempt ID + timing start before config/PreFilter (R2): always have an ID
	// and attempt_to_terminal_ms even when required-checks config fails.
	attempt, meta, timing, beginErr := sched.beginAttempt(pod)
	sched.recordSchedulingAttempt(meta)
	if beginErr != nil {
		timing.markTerminal("fail_config", "required_checks_config")
		sched.recordPhaseTiming(meta.AttemptID, timing)
		return ScheduleResult{AttemptID: meta.AttemptID, Timing: timing, nominatingInfo: clearNominatedNode}, podInfo, utils.AsStatus(beginErr)
	}
	ctx = utils.WithAttempt(ctx, attempt)

	timing.PreFilterStart = time.Now()
	preStatus := fwk.RunPreFilterPlugins(ctx, pod)
	timing.PreFilterEnd = time.Now()
	if !preStatus.IsSuccess() {
		logger.Warn(fmt.Sprintf("[schedule] PreFilter 실패: pod=%s/%s reason=%s",
			pod.Namespace, pod.Name, preStatus.Message()))
		timing.markTerminal("fail_prefilter", "prefilter")
		sched.recordPhaseTiming(meta.AttemptID, timing)
		return ScheduleResult{AttemptID: meta.AttemptID, Timing: timing, nominatingInfo: clearNominatedNode}, podInfo,
			utils.NewStatus(utils.Unschedulable).WithError(fmt.Errorf("%s", preStatus.Message()))
	}

	scheduleResult, err := sched.schedulePod(ctx, fwk, pod)
	if scheduleResult.Timing == nil {
		scheduleResult.Timing = timing
	} else {
		// Preserve PreFilter clocks recorded above; keep monotonic start.
		scheduleResult.Timing.AttemptStartedAt = timing.AttemptStartedAt
		scheduleResult.Timing.PreFilterStart = timing.PreFilterStart
		scheduleResult.Timing.PreFilterEnd = timing.PreFilterEnd
	}
	if scheduleResult.AttemptID == "" {
		scheduleResult.AttemptID = meta.AttemptID
	}
	if err != nil {
		logger.Warn(fmt.Sprintf("[schedule] 노드 선택 실패: pod=%s/%s err=%v",
			pod.Namespace, pod.Name, err))
		if scheduleResult.Timing != nil && scheduleResult.Timing.EventRole != "terminal" {
			outcome := scheduleResult.Timing.Outcome
			stage := scheduleResult.Timing.FailStage
			if outcome == "" || outcome == "select_ok" {
				outcome = "fail_schedule"
				stage = "schedule"
			}
			scheduleResult.Timing.markTerminal(outcome, stage)
		}
		sched.recordPhaseTiming(scheduleResult.AttemptID, scheduleResult.Timing)
		if err == ErrNoNodesAvailable {
			return ScheduleResult{AttemptID: scheduleResult.AttemptID, Timing: scheduleResult.Timing, nominatingInfo: clearNominatedNode, PluginResultMap: scheduleResult.PluginResultMap}, podInfo,
				utils.NewStatus(utils.UnschedulableAndUnresolvable).WithError(err)
		}
		scheduleResult.nominatingInfo = clearNominatedNode
		return scheduleResult, podInfo, utils.NewStatus(utils.Unschedulable).WithError(err)
	}

	assumedPodInfo := podInfo.DeepCopy()
	assumedPod := assumedPodInfo.Pod

	if err = sched.assume(klogger, assumedPod, scheduleResult.SuggestedHost); err != nil {
		if scheduleResult.Timing != nil {
			scheduleResult.Timing.markTerminal("fail_assume", "assume")
			sched.recordPhaseTiming(scheduleResult.AttemptID, scheduleResult.Timing)
		}
		return ScheduleResult{AttemptID: scheduleResult.AttemptID, Timing: scheduleResult.Timing, nominatingInfo: clearNominatedNode}, assumedPodInfo, utils.AsStatus(err)
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
	if scheduleResult.Timing == nil {
		// Fallback: prefer ScheduleOne start (monotonic) over wall UTC.
		at := start
		if at.IsZero() {
			at = time.Now()
		}
		scheduleResult.Timing = &PhaseTiming{AttemptStartedAt: at}
	}
	scheduleResult.Timing.BindStart = time.Now()

	queueDone := func() {
		if sched.SchedulingQueue != nil {
			sched.SchedulingQueue.Done(assumedPod.UID)
		}
	}
	queueUnsched := func() {
		if sched.SchedulingQueue != nil {
			sched.SchedulingQueue.AddUnschedulable(assumedPodInfo)
		}
	}

	finishBind := func(ok bool, detail, failStage string) {
		scheduleResult.Timing.BindEnd = time.Now()
		scheduleResult.Timing.SuggestedHost = scheduleResult.SuggestedHost
		if ok {
			scheduleResult.Timing.markTerminal("success", "")
		} else {
			scheduleResult.Timing.markTerminal("fail_bind", failStage)
		}
		sched.recordBindOutcome(scheduleResult.AttemptID, assumedPod, scheduleResult.SuggestedHost, ok, detail)
		sched.recordPhaseTiming(scheduleResult.AttemptID, scheduleResult.Timing)
	}

	if permitStatus := fwk.RunPermitPlugins(ctx, assumedPod, scheduleResult.SuggestedHost); !permitStatus.IsSuccess() {
		logger.Warn(fmt.Sprintf("[bind] Permit 거부, 재큐(backoff): pod=%s/%s reason=%s",
			assumedPod.Namespace, assumedPod.Name, permitStatus.Message()))
		_ = sched.Cache.RemovePod(assumedPod)
		queueDone()
		queueUnsched()
		finishBind(false, permitStatus.Message(), "permit")
		return utils.NewStatus(utils.Unschedulable, permitStatus.Message())
	}

	if hasGPURequest(assumedPod) && isHAMiManagedNode(sched, scheduleResult.SuggestedHost) {
		logger.Info(fmt.Sprintf("[HAMi] bind 시도: pod=%s/%s → node=%s",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost))

		if err := sched.hamiClient.Bind(assumedPod, scheduleResult.SuggestedHost); err != nil {
			logger.Error(fmt.Sprintf("[HAMi] bind 실패, 재큐(backoff): pod=%s/%s err=%v",
				assumedPod.Namespace, assumedPod.Name, err), err)
			_ = sched.Cache.RemovePod(assumedPod)
			queueDone()
			queueUnsched()
			finishBind(false, err.Error(), "hami_bind")
			return utils.NewStatus(utils.Error, err.Error())
		}

		logger.Info(fmt.Sprintf("[HAMi] bind 완료: pod=%s/%s node=%s",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost))
		sched.finishBinding(fwk, assumedPod, scheduleResult.SuggestedHost)
		queueDone()
		finishBind(true, "", "")
		return nil
	}

	if err := sched.runBindPlugin(ctx, fwk, assumedPod, &scheduleResult); err != nil {
		logger.Warn(fmt.Sprintf("[bind] DefaultBinder 실패, 재큐(backoff): pod=%s/%s node=%s err=%v",
			assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost, err))
		_ = sched.Cache.RemovePod(assumedPod)
		queueDone()
		queueUnsched()
		finishBind(false, err.Error(), "default_binder")
		return utils.NewStatus(utils.Error, err.Error())
	}

	queueDone()
	finishBind(true, "", "")
	return nil
}

func (sched *Scheduler) beginAttempt(pod *v1.Pod) (*utils.AttemptState, placementchecks.AttemptMeta, *PhaseTiming, error) {
	// ID + monotonic start before config load so fail_config still has terminal duration.
	started := time.Now()
	meta := placementchecks.AttemptMeta{
		AttemptID:    uuid.NewString(),
		PodNamespace: pod.Namespace,
		PodName:      pod.Name,
		PodUID:       string(pod.UID),
		StartedAt:    started,
	}
	timing := &PhaseTiming{AttemptStartedAt: started}
	cfg, cfgErr := sched.resolveRequiredChecksConfig()
	if cfgErr != nil {
		return nil, meta, timing, fmt.Errorf("required-checks config: %w", cfgErr)
	}
	meta.ConfigVersion = cfg.Version
	snap := placementchecks.Snapshot{ID: uuid.NewString(), TakenAt: started, Nodes: map[string]placementchecks.NodeTelemetry{}}
	if sched.PlacementStore != nil {
		snap = sched.PlacementStore.Snapshot()
		meta.SnapshotID = snap.ID
	}
	return utils.NewAttemptState(meta, snap, placementchecks.ParseRequest(pod), cfg), meta, timing, nil
}

func (sched *Scheduler) schedulePod(ctx context.Context, fwk framework.Framework, pod *v1.Pod) (result ScheduleResult, err error) {
	scheduleResult := ScheduleResult{Timing: &PhaseTiming{}}
	timing := scheduleResult.Timing

	// Reuse attempt prepared before PreFilter when present (ScheduleOne path).
	// Direct schedulePod tests still create a fresh attempt here.
	attempt := utils.AttemptFrom(ctx)
	var meta placementchecks.AttemptMeta
	if attempt != nil {
		meta = attempt.Meta
		timing.AttemptStartedAt = meta.StartedAt
		scheduleResult.AttemptID = meta.AttemptID
	} else {
		var beginErr error
		var begunTiming *PhaseTiming
		attempt, meta, begunTiming, beginErr = sched.beginAttempt(pod)
		if begunTiming != nil {
			timing = begunTiming
			scheduleResult.Timing = timing
		}
		scheduleResult.AttemptID = meta.AttemptID
		if beginErr != nil {
			timing.markTerminal("fail_config", "required_checks_config")
			sched.recordSchedulingAttempt(meta)
			sched.recordPhaseTiming(meta.AttemptID, timing)
			return scheduleResult, beginErr
		}
		ctx = utils.WithAttempt(ctx, attempt)
		sched.recordSchedulingAttempt(meta)
	}

	if sched.Cache.NodeCount() == 0 {
		timing.markTerminal("fail_no_nodes", "cache")
		return scheduleResult, ErrNoNodesAvailable
	}

	if hasGPURequest(pod) {
		sched.refreshStaleGPUMetrics(ctx, 60000)
	}

	nodes := sched.Cache.Nodes()
	if len(nodes) == 0 {
		timing.markTerminal("fail_no_nodes", "cache")
		return scheduleResult, ErrNoNodesAvailable
	}
	base := NewScheduleResult(nodes)
	scheduleResult.PluginResultMap = base.PluginResultMap
	scheduleResult.FeasibleNodes = base.FeasibleNodes

	timing.FilterStart = time.Now()
	if err = sched.runFilterPlugin(ctx, fwk, pod, &scheduleResult); err != nil {
		timing.FilterEnd = time.Now()
		timing.Outcome = "fail_filter"
		timing.FailStage = "filter"
		return scheduleResult, err
	}
	timing.FilterEnd = time.Now()

	if hasGPURequest(pod) && hasHAMiManagedCandidate(sched, scheduleResult.PluginResultMap) {
		feasibleNodes := make([]*v1.Node, 0)
		for nodeName, pr := range scheduleResult.PluginResultMap {
			if !pr.IsFiltered {
				if nodeInfo := sched.Cache.Nodes()[nodeName]; nodeInfo != nil && nodeInfo.Node() != nil {
					feasibleNodes = append(feasibleNodes, nodeInfo.Node())
				}
			}
		}

		if len(feasibleNodes) > 0 {
			timing.HamiStart = time.Now()
			hamiFilteredNodes, hamiErr := sched.hamiClient.Filter(pod, feasibleNodes)
			timing.HamiEnd = time.Now()
			if hamiErr != nil {
				// Preserve prior candidates; record communication failure without inventing PASS.
				for nodeName, pr := range scheduleResult.PluginResultMap {
					if pr.IsFiltered {
						continue
					}
					pr.RejectionReasons = append(pr.RejectionReasons, placementchecks.RejectionReason{
						Stage:  "hami_filter",
						Check:  "hami",
						Code:   "hami_filter_error",
						Detail: hamiErr.Error(),
					})
					scheduleResult.PluginResultMap[nodeName] = pr
				}
			} else {
				keepSet := make(map[string]bool, len(hamiFilteredNodes))
				for _, n := range hamiFilteredNodes {
					keepSet[n.Name] = true
				}
				newFeasible := 0
				for nodeName, pr := range scheduleResult.PluginResultMap {
					if !pr.IsFiltered {
						if !keepSet[nodeName] {
							pr.IsFiltered = true
							st := utils.NewStatus(utils.Unschedulable, "excluded by HAMi filter")
							st.SetPlugin("HAMiExtender")
							pr.FilteredStatus = *st
							pr.RejectionReasons = append(pr.RejectionReasons, placementchecks.RejectionReason{
								Stage:  "hami_filter",
								Check:  "hami",
								Code:   "hami_filtered",
								Detail: "node excluded by HAMi filter",
							})
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

	// Final candidate JSONL after local Filters + HAMi (includes legacy/HAMi exclusions).
	sched.recordFinalCandidates(scheduleResult, pod)

	// Pending 사유를 남길 수 있도록 실패 시에도 Node별 Filter 결과를 함께 돌려준다.
	if scheduleResult.FeasibleNodes == 0 {
		timing.Outcome = "fail_no_feasible"
		timing.FailStage = "filter"
		// Terminal end is stamped by schedulingCycle (or caller) via markTerminal.
		return scheduleResult, fmt.Errorf("대상 Pod %s/%s를 배치할 수 있는 노드가 없음: 모든 노드 필터링됨",
			pod.Namespace, pod.Name)
	}

	// Always run Score plugins — including when only one node is feasible.
	// CatalogAwareScore (shadow) must still look up Catalog and record
	// placement_decision; skipping Score on the single-node fast path hid all
	// Catalog E2E evidence on this cluster (one accelerator node).
	// PreScore prepares unique full-lookup-key Profiles; Score reads shared state.
	timing.PreScoreStart = time.Now()
	if err = sched.runPreScorePlugin(ctx, fwk, pod, &scheduleResult); err != nil {
		timing.PreScoreEnd = time.Now()
		timing.Outcome = "fail_prescore"
		timing.FailStage = "prescore"
		return scheduleResult, err
	}
	timing.PreScoreEnd = time.Now()

	timing.ScoreStart = time.Now()
	if err = sched.runScorePlugin(ctx, fwk, pod, &scheduleResult); err != nil {
		timing.ScoreEnd = time.Now()
		timing.Outcome = "fail_score"
		timing.FailStage = "score"
		return scheduleResult, err
	}
	timing.ScoreEnd = time.Now()

	timing.SelectStart = time.Now()
	err = sched.selectResource(fwk, pod, &scheduleResult)
	timing.SelectEnd = time.Now()
	timing.SuggestedHost = scheduleResult.SuggestedHost
	if err != nil {
		timing.Outcome = "fail_select"
		timing.FailStage = "select"
		return scheduleResult, err
	}
	// Judgment interim only — Bind path emits terminal phase_timing.
	timing.markInterimSelect()
	sched.recordPhaseTiming(scheduleResult.AttemptID, timing)
	return scheduleResult, err
}

func hasHAMiManagedCandidate(sched *Scheduler, results utils.PluginResultMap) bool {
	for nodeName, result := range results {
		if !result.IsFiltered && isHAMiManagedNode(sched, nodeName) {
			return true
		}
	}
	return false
}

func (sched *Scheduler) runFilterPlugin(ctx context.Context, fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	nodes := sched.Cache.Nodes()
	feasibleNodes := 0

	for nodeName, nodeInfo := range nodes {
		pluginResults := fwk.RunFilterPlugins(ctx, pod, nodeInfo)
		if pr, exists := pluginResults[nodeName]; exists {
			existingResult := scheduleResult.PluginResultMap[nodeName]
			existingResult.IsFiltered = pr.IsFiltered
			existingResult.FilteredStatus = pr.FilteredStatus
			existingResult.RequiredChecks = pr.RequiredChecks
			existingResult.RejectionReasons = pr.RejectionReasons
			scheduleResult.PluginResultMap[nodeName] = existingResult
			if !pr.IsFiltered {
				feasibleNodes++
			}
		}
	}

	scheduleResult.FeasibleNodes = feasibleNodes
	return nil
}

func (sched *Scheduler) resolveRequiredChecksConfig() (placementchecks.Config, error) {
	if sched.RequiredChecksConfig != nil {
		return *sched.RequiredChecksConfig, nil
	}
	path := os.Getenv("SCHED_REQUIRED_CHECKS_CONFIG")
	cfg, err := placementchecks.LoadConfigFile(path)
	if err != nil {
		// Never silently replace a failed explicit/invalid load with DefaultConfig.
		return placementchecks.Config{}, err
	}
	sched.RequiredChecksConfig = &cfg
	return cfg, nil
}

func (sched *Scheduler) recordSchedulingAttempt(meta placementchecks.AttemptMeta) {
	if sched.DecisionRecorder == nil {
		return
	}
	sched.DecisionRecorder.Enqueue(decisionrecorder.Event{
		Kind:      decisionrecorder.KindSchedulingAttempt,
		AttemptID: meta.AttemptID,
		Payload: map[string]any{
			"pod_namespace":      meta.PodNamespace,
			"pod_name":           meta.PodName,
			"pod_uid":            meta.PodUID,
			"snapshot_id":        meta.SnapshotID,
			"config_version":     meta.ConfigVersion,
			"attempt_started_at": meta.StartedAt.UTC().Format(time.RFC3339Nano),
			"metric_class":       "verification_not_official_kpi7",
			"retry_note":         "each scheduling try gets a new attempt_id; join retries by pod_uid",
		},
	})
}

func (sched *Scheduler) recordFinalCandidates(scheduleResult ScheduleResult, pod *v1.Pod) {
	if sched.DecisionRecorder == nil {
		return
	}
	podUID := ""
	if pod != nil {
		podUID = string(pod.UID)
	}
	for nodeName, pr := range scheduleResult.PluginResultMap {
		payload := map[string]any{
			"is_filtered":       pr.IsFiltered,
			"rejection_reasons": pr.RejectionReasons,
			"pod_uid":           podUID,
		}
		if pr.RequiredChecks != nil {
			payload["checks"] = pr.RequiredChecks.Checks
		}
		sched.DecisionRecorder.RecordCandidateFinal(scheduleResult.AttemptID, nodeName, payload)
	}
}

func (sched *Scheduler) recordBindOutcome(attemptID string, pod *v1.Pod, nodeName string, ok bool, detail string) {
	if sched.DecisionRecorder == nil || pod == nil {
		return
	}
	payload := map[string]any{
		"pod_namespace": pod.Namespace,
		"pod_name":      pod.Name,
		"pod_uid":       string(pod.UID),
		"success":       ok,
		"metric_class":  "verification_not_official_kpi7",
	}
	if detail != "" {
		payload["detail"] = detail
	}
	sched.DecisionRecorder.Enqueue(decisionrecorder.Event{
		Kind:      decisionrecorder.KindBindOutcome,
		AttemptID: attemptID,
		NodeName:  nodeName,
		Payload:   payload,
	})
}

func (sched *Scheduler) feasibleNodesForScore(scheduleResult *ScheduleResult) []*v1.Node {
	feasibleNodes := make([]*v1.Node, 0)
	for nodeName, pr := range scheduleResult.PluginResultMap {
		if !pr.IsFiltered {
			if nodeInfo := sched.Cache.Nodes()[nodeName]; nodeInfo != nil && nodeInfo.Node() != nil {
				feasibleNodes = append(feasibleNodes, nodeInfo.Node())
			}
		}
	}
	return feasibleNodes
}

func (sched *Scheduler) runPreScorePlugin(ctx context.Context, fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	feasibleNodes := sched.feasibleNodesForScore(scheduleResult)
	if len(feasibleNodes) == 0 {
		return fmt.Errorf("PreScore를 수행할 수 있는 노드가 없음")
	}
	status := fwk.RunPreScorePlugins(ctx, pod, feasibleNodes)
	if !status.IsSuccess() {
		return fmt.Errorf("PreScore 실패: %s", status.Message())
	}
	return nil
}

func (sched *Scheduler) runScorePlugin(ctx context.Context, fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	klogger := klog.FromContext(ctx)

	feasibleNodes := sched.feasibleNodesForScore(scheduleResult)

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
			// Preserve Filter result unless Score/Normalize marked the node filtered
			// for cost ineligibility; always copy cost-first metadata from Score.
			if score.IsFiltered {
				existing.IsFiltered = true
				existing.FilteredStatus = score.FilteredStatus
			}
			existing.CostSelectionActive = score.CostSelectionActive
			existing.CostEligible = score.CostEligible
			existing.PredictedNetEnergyJ = score.PredictedNetEnergyJ
			existing.PredictedServerEnergyJ = score.PredictedServerEnergyJ
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

func (sched *Scheduler) selectResource(fwk framework.Framework, pod *v1.Pod, scheduleResult *ScheduleResult) error {
	bestNode, err := utils.SelectPreferredNode(scheduleResult.PluginResultMap)
	// Record placement_decision after final selection so selected_enforced matches Bind.
	fwk.CommitPlacementDecisions(pod, bestNode, err)
	if err != nil {
		return fmt.Errorf("Pod %s/%s를 위한 적절한 노드를 찾을 수 없음: %w", pod.Namespace, pod.Name, err)
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
