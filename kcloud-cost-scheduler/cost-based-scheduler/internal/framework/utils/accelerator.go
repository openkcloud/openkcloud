/*
Hardware accelerator metrics integration

This module handles connections to Prometheus or other metrics providers to
fetch power, cost, and utilization data for GPUs and NPUs.
*/
package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"gopkg.in/yaml.v3"
)

type AcceleratorSpecs struct {
	CostPerHour       float64 `yaml:"cost_per_hour"`
	PerfScore         float64 `yaml:"perf_score"`
	PowerWatts        float64 `yaml:"power_watts"`
	PerfPerWatt       float64 `yaml:"perf_per_watt"`
	InferenceAffinity float64 `yaml:"inference_affinity"`
	TrainingAffinity  float64 `yaml:"training_affinity"`
}

type Accelerator struct {
	Name     string            `yaml:"name"`
	Type     string            `yaml:"type"`     // e.g., "gpu", "npu", "mig"
	Vendor   string            `yaml:"vendor"`   // e.g., "nvidia", "furiosa"
	Model    string            `yaml:"model"`    // e.g., "a100", "h100", "mig-1g.24gb"
	MemoryGB int               `yaml:"memory_gb"`
	Specs    AcceleratorSpecs  `yaml:"specs"`
	Labels   map[string]string `yaml:"labels"` // Additional matching labels
}

type AffinityConfig struct {
	PodAffinityWeight         float64 `yaml:"pod_affinity_weight"`
	SameJobBonusPerPod        float64 `yaml:"same_job_bonus_per_pod"`
	SameQueueBonusPerPod      float64 `yaml:"same_queue_bonus_per_pod"`
	AntiAffinityPenaltyPerPod float64 `yaml:"anti_affinity_penalty_per_pod"`
	DefaultMode               string  `yaml:"default_mode"`
}

type ScoringWeights struct {
	CostEfficiencyMax   float64 `yaml:"cost_efficiency_max"`
	PowerEfficiencyMax  float64 `yaml:"power_efficiency_max"`
	WorkloadAffinityMax float64 `yaml:"workload_affinity_max"`
	PodAffinityMax      float64 `yaml:"pod_affinity_max"`
	BaseScore           float64 `yaml:"base_score"`
}

type AcceleratorConfig struct {
	Accelerators   []Accelerator  `yaml:"accelerators"`
	AffinityConfig AffinityConfig `yaml:"affinity_config"`
	ScoringWeights ScoringWeights `yaml:"scoring_weights"`
}

type AcceleratorMetrics struct {
	CostPerHour       float64
	PerfScore         float64
	PowerWatts        float64
	PerfPerWatt       float64
	InferenceAffinity float64
	TrainingAffinity  float64
	Found             bool
}

type AcceleratorMetricsProvider interface {
	GetMetrics(node *corev1.Node) AcceleratorMetrics
	GetMetricsByGPUInfo(gpuType, gpuModel string) AcceleratorMetrics
	GetConfig() *AcceleratorConfig
}

type ConfigBasedMetricsProvider struct {
	config *AcceleratorConfig
}

func NewConfigBasedMetricsProvider(configPath string) (*ConfigBasedMetricsProvider, error) {
	config, err := loadAcceleratorConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &ConfigBasedMetricsProvider{
		config: config,
	}, nil
}

func (p *ConfigBasedMetricsProvider) GetConfig() *AcceleratorConfig {
	return p.config
}

func (p *ConfigBasedMetricsProvider) GetMetrics(node *corev1.Node) AcceleratorMetrics {
	if node == nil {
		return AcceleratorMetrics{Found: false}
	}

	gpuType := node.Labels["accelerator.type"]
	gpuModel := node.Labels["accelerator.model"]

	if gpuType != "" && gpuModel != "" {
		metrics := p.GetMetricsByGPUInfo(gpuType, gpuModel)
		if metrics.Found {
			klog.V(4).InfoS("Found metrics from config by labels",
				"node", node.Name,
				"gpuType", gpuType,
				"gpuModel", gpuModel)
			return metrics
		}
	}

	gpuType, gpuModel = detectGPUFromResources(node)
	if gpuType != "" {
		metrics := p.GetMetricsByGPUInfo(gpuType, gpuModel)
		if metrics.Found {
			klog.V(4).InfoS("Found metrics from config by resource detection",
				"node", node.Name,
				"gpuType", gpuType,
				"gpuModel", gpuModel)
			return metrics
		}
	}

	if product, ok := node.Labels["nvidia.com/gpu.product"]; ok {
		metrics := p.matchByProductName(product)
		if metrics.Found {
			klog.V(4).InfoS("Found metrics from config by gpu.product label",
				"node", node.Name,
				"product", product)
			return metrics
		}
	}

	metrics := getMetricsFromNodeLabels(node)
	if metrics.Found {
		klog.V(4).InfoS("Using fallback node label metrics",
			"node", node.Name)
	}

	return metrics
}

func (p *ConfigBasedMetricsProvider) GetMetricsByGPUInfo(gpuType, gpuModel string) AcceleratorMetrics {
	if p.config == nil {
		return AcceleratorMetrics{Found: false}
	}

	gpuType = strings.ToLower(gpuType)
	gpuModel = strings.ToLower(gpuModel)

	for _, acc := range p.config.Accelerators {
		accType := strings.ToLower(acc.Type)
		accModel := strings.ToLower(acc.Model)

		if accType == gpuType && accModel == gpuModel {
			return AcceleratorMetrics{
				CostPerHour:       acc.Specs.CostPerHour,
				PerfScore:         acc.Specs.PerfScore,
				PowerWatts:        acc.Specs.PowerWatts,
				PerfPerWatt:       acc.Specs.PerfPerWatt,
				InferenceAffinity: acc.Specs.InferenceAffinity,
				TrainingAffinity:  acc.Specs.TrainingAffinity,
				Found:             true,
			}
		}

		if accType == gpuType && strings.Contains(gpuModel, accModel) {
			return AcceleratorMetrics{
				CostPerHour:       acc.Specs.CostPerHour,
				PerfScore:         acc.Specs.PerfScore,
				PowerWatts:        acc.Specs.PowerWatts,
				PerfPerWatt:       acc.Specs.PerfPerWatt,
				InferenceAffinity: acc.Specs.InferenceAffinity,
				TrainingAffinity:  acc.Specs.TrainingAffinity,
				Found:             true,
			}
		}
	}

	return AcceleratorMetrics{Found: false}
}

func (p *ConfigBasedMetricsProvider) matchByProductName(product string) AcceleratorMetrics {
	if p.config == nil {
		return AcceleratorMetrics{Found: false}
	}

	product = strings.ToLower(product)

	normalizeStr := func(s string) string {
		s = strings.ReplaceAll(s, "-", " ")
		s = strings.ReplaceAll(s, "_", " ")
		return strings.ToLower(s)
	}
	normProduct := normalizeStr(product)

	for _, acc := range p.config.Accelerators {
		accModel := normalizeStr(acc.Model)
		accName := normalizeStr(acc.Name)

		if strings.Contains(normProduct, accModel) || strings.Contains(normProduct, accName) {
			return AcceleratorMetrics{
				CostPerHour:       acc.Specs.CostPerHour,
				PerfScore:         acc.Specs.PerfScore,
				PowerWatts:        acc.Specs.PowerWatts,
				PerfPerWatt:       acc.Specs.PerfPerWatt,
				InferenceAffinity: acc.Specs.InferenceAffinity,
				TrainingAffinity:  acc.Specs.TrainingAffinity,
				Found:             true,
			}
		}
	}

	return AcceleratorMetrics{Found: false}
}

func detectGPUFromResources(node *corev1.Node) (gpuType, gpuModel string) {
	allocatable := node.Status.Allocatable

	if gpu, ok := allocatable["nvidia.com/gpu"]; ok && gpu.Value() > 0 {
		gpuType = "gpu"
		if model, ok := node.Labels["nvidia.com/gpu.product"]; ok {
			gpuModel = model
		}
		return gpuType, gpuModel
	}

	for resourceName := range allocatable {
		resourceStr := string(resourceName)
		if strings.HasPrefix(resourceStr, "nvidia.com/mig-") {
			gpuType = "mig"
			gpuModel = strings.TrimPrefix(resourceStr, "nvidia.com/mig-")
			return gpuType, gpuModel
		}
	}

	if npu, ok := allocatable["furiosa.ai/warboy"]; ok && npu.Value() > 0 {
		gpuType = "npu"
		gpuModel = "warboy"
		return gpuType, gpuModel
	}

	return "", ""
}

func getMetricsFromNodeLabels(node *corev1.Node) AcceleratorMetrics {
	labels := node.Labels
	metrics := AcceleratorMetrics{Found: false}

	if val, ok := labels["accelerator.cost-per-hour"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.CostPerHour = parsed
			metrics.Found = true
		}
	}

	if val, ok := labels["accelerator.perf-score"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.PerfScore = parsed
			metrics.Found = true
		}
	}

	if val, ok := labels["accelerator.power-watts"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.PowerWatts = parsed
			metrics.Found = true
		}
	}

	if val, ok := labels["accelerator.perf-per-watt"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.PerfPerWatt = parsed
			metrics.Found = true
		}
	}

	if val, ok := labels["accelerator.inference-affinity"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.InferenceAffinity = parsed
			metrics.Found = true
		}
	}

	if val, ok := labels["accelerator.training-affinity"]; ok {
		if parsed, err := strconv.ParseFloat(val, 64); err == nil {
			metrics.TrainingAffinity = parsed
			metrics.Found = true
		}
	}

	return metrics
}

func loadAcceleratorConfig(configPath string) (*AcceleratorConfig, error) {
	if configPath == "" {
		paths := []string{
			"/etc/scheduler/accelerator-config.yaml",
			"./accelerator-config.yaml",
			"./config/accelerator-config.yaml",
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
	}

	if configPath == "" {
		klog.V(2).Info("No accelerator config file found, using defaults")
		return getDefaultAcceleratorConfig(), nil
	}

	klog.V(2).InfoS("Loading accelerator config", "path", configPath)

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config AcceleratorConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}

	klog.V(2).InfoS("Loaded accelerator config",
		"acceleratorCount", len(config.Accelerators))

	return &config, nil
}

func getDefaultAcceleratorConfig() *AcceleratorConfig {
	return &AcceleratorConfig{
		Accelerators: []Accelerator{
			{
				Name: "NVIDIA RTX PRO 6000 Blackwell", Type: "gpu", Vendor: "nvidia",
				Model: "rtx-pro-6000-blackwell", MemoryGB: 96,
				Specs: AcceleratorSpecs{
					CostPerHour: 2.50, PerfScore: 2519, PowerWatts: 300,
					PerfPerWatt: 8.40, InferenceAffinity: 0.85, TrainingAffinity: 1.0,
				},
			},
			{
				Name: "NVIDIA RTX A6000", Type: "gpu", Vendor: "nvidia",
				Model: "rtx-a6000", MemoryGB: 48,
				Specs: AcceleratorSpecs{
					CostPerHour: 1.20, PerfScore: 774, PowerWatts: 300,
					PerfPerWatt: 2.58, InferenceAffinity: 0.8, TrainingAffinity: 0.9,
				},
			},
			{
				Name: "Furiosa Warboy", Type: "npu", Vendor: "furiosa", Model: "warboy",
				MemoryGB: 32,
				Specs: AcceleratorSpecs{
					CostPerHour: 0.50, PerfScore: 500, PowerWatts: 50,
					PerfPerWatt: 10.0, InferenceAffinity: 1.0, TrainingAffinity: 0.2,
				},
			},
			{
				Name: "NVIDIA A100 80GB", Type: "gpu", Vendor: "nvidia", Model: "a100",
				MemoryGB: 80,
				Specs: AcceleratorSpecs{
					CostPerHour: 2.0, PerfScore: 1000, PowerWatts: 400,
					PerfPerWatt: 2.5, InferenceAffinity: 0.7, TrainingAffinity: 1.0,
				},
			},
			{
				Name: "NVIDIA H100 80GB", Type: "gpu", Vendor: "nvidia", Model: "h100",
				MemoryGB: 80,
				Specs: AcceleratorSpecs{
					CostPerHour: 4.0, PerfScore: 2000, PowerWatts: 700,
					PerfPerWatt: 2.86, InferenceAffinity: 0.8, TrainingAffinity: 1.0,
				},
			},
			{
				Name: "NVIDIA MIG 1g.24gb", Type: "mig", Vendor: "nvidia", Model: "1g.24gb",
				MemoryGB: 24,
				Specs: AcceleratorSpecs{
					CostPerHour: 0.3, PerfScore: 150, PowerWatts: 60,
					PerfPerWatt: 2.5, InferenceAffinity: 1.0, TrainingAffinity: 0.3,
				},
			},
		},
		AffinityConfig: AffinityConfig{
			PodAffinityWeight:         20.0,
			SameJobBonusPerPod:        2.0,
			SameQueueBonusPerPod:      1.0,
			AntiAffinityPenaltyPerPod: 5.0,
			DefaultMode:               "preferred",
		},
		ScoringWeights: ScoringWeights{
			CostEfficiencyMax:   50.0,
			PowerEfficiencyMax:  30.0,
			WorkloadAffinityMax: 20.0,
			PodAffinityMax:      20.0,
			BaseScore:           0.0,
		},
	}
}
