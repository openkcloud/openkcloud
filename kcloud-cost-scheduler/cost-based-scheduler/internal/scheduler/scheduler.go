/*
Scheduler core structure and initialization

This module defines the main Scheduler object, assembling the framework, cache,
queue, and informers required to run the scheduling process.
*/
package scheduler

import (
	"context"
	"fmt"
	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	internalqueue "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/queue"
	config "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/config"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	"sync"

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
}

type ScheduleResult struct {
	SuggestedHost   string          // Name of the selected node.
	FeasibleNodes   int             // The number of nodes out of the evaluated ones that fit the pod.
	nominatingInfo  *NominatingInfo // The nominating info for scheduling cycle.
	PluginResultMap utils.PluginResultMap
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

	sched := &Scheduler{
		schedulerConfig: cc,
		Cache:           schedulerCache,
		StopEverything:  stopEverything,
		SchedulingQueue: podQueue,
		fwk:             fwk,
		logger:          logger,
		hamiClient:      plugin.NewHAMiExtenderClient(),
	}

	if err := AddAllEventHandlers(sched, cc.InformerFactory); err != nil {
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
	logger.Info("All scheduler workers stopped")
}

func (sched *Scheduler) fetchNodeGPUMetrics(nodeName string) {
	_ = sched.Cache.UpdateNodeGPUMetrics(nodeName, nil)
}

func (sched *Scheduler) refreshStaleGPUMetrics(_ context.Context, maxAgeMs int64) {
	nodes := sched.Cache.Nodes()
	currentTime := utils.GetCurrentTimeMillis()

	for nodeName, nodeInfo := range nodes {
		if nodeInfo == nil {
			continue
		}
		metricsAge := currentTime - nodeInfo.GPUMetricsUpdatedAt.UnixMilli()
		if metricsAge > maxAgeMs {
			_ = sched.Cache.UpdateNodeGPUMetrics(nodeName, nil)
		}
	}
}

func (sched *Scheduler) gpuMetricsWorker(ctx context.Context) {
	<-ctx.Done() // analysis-engine 연동으로 대체됨 — 컨텍스트 취소 시까지 대기
}
