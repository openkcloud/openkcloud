/*
Scheduler core structure and initialization

This module defines the main Scheduler object, assembling the framework, cache,
queue, and informers required to run the scheduling process.
*/
package scheduler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	internalqueue "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/queue"
	config "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/config"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	"k8s.io/apimachinery/pkg/util/wait"
)

var MainScheduler *Scheduler

var ErrNoNodesAvailable = fmt.Errorf("no nodes available to schedule pods")

type Scheduler struct {
	schedulerConfig *config.SchedulerConfig
	Cache           *utils.Cache
	NextPod         func() (*internalqueue.QueuedPodInfo, error)
	StopEverything  <-chan struct{}
	SchedulingQueue *internalqueue.SchedulingQueue
	fwk             framework.Framework
	logger          *logger.Logger
	hamiClient      *plugin.HAMiExtenderClient
	// PlacementStore holds measured telemetry for required checks (P2/P6).
	// Nil → empty snapshot; checks needing measurements become UNEVALUATED.
	PlacementStore *placementchecks.Store
	// DecisionRecorder writes JSONL attempt/check/bind events (optional; Catalog-independent).
	DecisionRecorder *decisionrecorder.Recorder
	// RequiredChecksConfig is loaded once at start (or first successful resolve).
	// Nil means resolve via SCHED_REQUIRED_CHECKS_CONFIG; load errors are not
	// silently replaced with DefaultConfig.
	RequiredChecksConfig *placementchecks.Config
}

type ScheduleResult struct {
	SuggestedHost   string          // Name of the selected node.
	FeasibleNodes   int             // The number of nodes out of the evaluated ones that fit the pod.
	nominatingInfo  *NominatingInfo // The nominating info for scheduling cycle.
	PluginResultMap utils.PluginResultMap
	// AttemptID links Filter/HAMi/Bind JSONL events for one scheduling try.
	AttemptID string
	// Timing is optional scheduling-latency clocks (verification class).
	Timing *PhaseTiming
}

func NewScheduleResult(nodeInfoMap map[string]*utils.NodeInfo) ScheduleResult {
	pluginResultMap := make(utils.PluginResultMap, len(nodeInfoMap))

	for nodeName, nodeInfo := range nodeInfoMap {
		gpuScores := make(map[string]*utils.GPUScore)

		if nodeInfo != nil && nodeInfo.GPUMap != nil {
			for gpuID := range nodeInfo.GPUMap {
				gpuScores[gpuID] = &utils.GPUScore{}
			}
		}

		pluginResultMap[nodeName] = utils.PluginResult{
			GPUScores:      gpuScores,
			IsFiltered:     false,
			Scores:         make([]utils.PluginScore, 0),
			TotalNodeScore: 0,
		}
	}

	return ScheduleResult{
		SuggestedHost:   "",
		FeasibleNodes:   0,
		nominatingInfo:  nil,
		PluginResultMap: pluginResultMap,
	}
}

type NominatingMode int

const (
	ModeNoop NominatingMode = iota
	ModeOverride
)

type NominatingInfo struct {
	NominatedNodeName string
	NominatingMode    NominatingMode
}

func NewScheduler(ctx context.Context, cc *config.SchedulerConfig) (*Scheduler, error) {
	stopEverything := ctx.Done()
	podQueue := internalqueue.NewSchedulingQueue(internalqueue.Less, cc.InformerFactory)
	logger := logger.NewLogger(logger.NewDefaultConfig())

	fwk := cc.Framework
	if fwk == nil {
		return nil, fmt.Errorf("framework is required")
	}

	schedulerCache := cc.Cache
	if schedulerCache == nil {
		schedulerCache = utils.NewCache(ctx)
	}

	// Validate required-checks config at start — never silently fall back to record defaults.
	rcCfg, err := placementchecks.LoadConfigFile(os.Getenv("SCHED_REQUIRED_CHECKS_CONFIG"))
	if err != nil {
		return nil, fmt.Errorf("required-checks config: %w", err)
	}

	if maxAtt := os.Getenv("SCHED_POD_MAX_SCHEDULING_ATTEMPTS"); maxAtt != "" {
		n, err := strconv.Atoi(maxAtt)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("SCHED_POD_MAX_SCHEDULING_ATTEMPTS: invalid %q", maxAtt)
		}
		podQueue.SetMaxSchedulingAttempts(n)
	}

	var rec *decisionrecorder.Recorder
	if path := os.Getenv("SCHED_DECISION_JSONL"); path != "" {
		qsize := 256
		if raw := os.Getenv("SCHED_DECISION_QUEUE_SIZE"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("SCHED_DECISION_QUEUE_SIZE: invalid %q", raw)
			}
			qsize = n
		}
		rec, err = decisionrecorder.New(path, qsize)
		if err != nil {
			return nil, fmt.Errorf("decision recorder: %w", err)
		}
	}

	sched := &Scheduler{
		schedulerConfig:      cc,
		Cache:                schedulerCache,
		StopEverything:       stopEverything,
		SchedulingQueue:      podQueue,
		fwk:                  fwk,
		logger:               logger,
		hamiClient:           plugin.NewHAMiExtenderClient(),
		RequiredChecksConfig: &rcCfg,
		DecisionRecorder:     rec,
	}

	if err := AddAllEventHandlers(sched, cc.InformerFactory); err != nil {
		if rec != nil {
			rec.Close()
		}
		return nil, fmt.Errorf("adding event handlers: %w", err)
	}

	sched.NextPod = podQueue.Pop

	return sched, nil
}

func (sched *Scheduler) InitScheduler() error {
	sched.schedulerConfig.InformerFactory.WaitForCacheSync(sched.StopEverything)

	return nil
}

func (sched *Scheduler) Run(ctx context.Context) {
	var internalWG sync.WaitGroup

	internalWG.Add(1)
	go func() {
		defer internalWG.Done()
		sched.schedulerConfig.InformerFactory.Start(ctx.Done())
	}()

	internalWG.Add(1)
	go func() {
		defer internalWG.Done()
		sched.SchedulingQueue.Run()
	}()

	internalWG.Add(1)
	go func() {
		defer internalWG.Done()
		wait.UntilWithContext(ctx, sched.ScheduleOne, 0)
	}()

	internalWG.Add(1)
	go func() {
		defer internalWG.Done()
		sched.gpuMetricsWorker(ctx)
	}()

	<-ctx.Done()
	sched.SchedulingQueue.Close()

	internalWG.Wait()
	if sched.DecisionRecorder != nil {
		sched.DecisionRecorder.Close()
	}
	logger.Info("All scheduler workers stopped")
}

func (sched *Scheduler) fetchNodeGPUMetrics(nodeName string) {
	// P8 will push measured telemetry into placementchecks.Store.
	// Do not call UpdateNodeGPUMetrics(nil) — that previously only stamped Now().
	_ = nodeName
}

func (sched *Scheduler) refreshStaleGPUMetrics(_ context.Context, maxAgeMs int64) {
	// No-op until a real collector exists (P8). Previously this refreshed
	// GPUMetricsUpdatedAt with nil payloads, forging freshness.
	_ = maxAgeMs
}

func (sched *Scheduler) gpuMetricsWorker(ctx context.Context) {
	// Live NVML/NETIO collection is P8. Do not invent metrics here.
	<-ctx.Done()
}
