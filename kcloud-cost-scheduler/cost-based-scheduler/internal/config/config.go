/*
Scheduler configuration parser

This module handles the loading and parsing of the scheduler's configuration
file (e.g., KubeSchedulerConfiguration), enabling dynamic plugin weighting.
*/
package config

import (
	"context"
	"os"

	logger "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type SchedulerConfig struct {
	HostKubeClient   *kubernetes.Clientset
	InformerFactory  informers.SharedInformerFactory
	Framework        framework.Framework
	Cache            *utils.Cache
	MetricsProvider  utils.AcceleratorMetricsProvider
}

func CreateDefaultConfig(ctx context.Context) *SchedulerConfig {
	hostConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("Failed to get cluster config", err)
		os.Exit(1)
	}
	hostKubeClient := kubernetes.NewForConfigOrDie(hostConfig)

	informerFactory := informers.NewSharedInformerFactory(hostKubeClient, 0)

	cache := utils.NewCache(ctx)

	configPath := os.Getenv("ACCELERATOR_CONFIG_PATH")
	metricsProvider, err := utils.NewConfigBasedMetricsProvider(configPath)
	if err != nil {
		logger.Warn("Failed to create metrics provider, using defaults: %v", err)
		metricsProvider, _ = utils.NewConfigBasedMetricsProvider("")
	}

	preFilterPlugins := []framework.PreFilterPlugin{
		plugin.NewDurationAwareScheduling(cache), // long|short 클래스 노드 확인
	}

	filterPlugins := []framework.FilterPlugin{
		plugin.NewTaintToleration(), // NoSchedule/NoExecute taint 노드 필터
		plugin.NewNodeSelector(),    // nodeSelector / nodeName 필터
		plugin.NewNodeResourcesFit(), // CPU/Memory/GPU/NPU 리소스 필터
	}

	scorePlugins := []framework.ScorePlugin{
		plugin.NewCostEfficiency(cache, metricsProvider),   // 0- 50 pts
		plugin.NewPowerEfficiency(cache, metricsProvider),  // 0- 30 pts
		plugin.NewPodAffinity(cache),                       // 0- 20 pts
		plugin.NewBinPacking(cache),                        // 0-100 pts: 노드 집적률 극대화
		plugin.NewDurationAwareScheduling(cache),           // 0- 20 pts: 장기/단기 클래스 매칭
		plugin.NewWorkloadAwareCost(cache),                 // 0- 25 pts: 예측 비용 보완
		plugin.NewWorkloadAwarePower(cache),                // 0- 15 pts: 예측 전력 보완
	}

	permitPlugins := []framework.PermitPlugin{
		plugin.NewGangScheduling(), // sched.ai/gang-size annotation 기반
	}

	bindPlugin := plugin.NewDefaultBinder(hostKubeClient)

	fwk := framework.NewFramework(preFilterPlugins, filterPlugins, scorePlugins, permitPlugins, bindPlugin)

	return &SchedulerConfig{
		InformerFactory:  informerFactory,
		HostKubeClient:   hostKubeClient,
		Framework:        fwk,
		Cache:            cache,
		MetricsProvider:  metricsProvider,
	}
}
