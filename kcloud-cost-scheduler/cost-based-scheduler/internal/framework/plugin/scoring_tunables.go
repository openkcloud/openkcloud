/*
Scoring normalization tunables

WorkloadAwareCost와 WorkloadAwarePower가 예측값을 0~1 비율로 바꿀 때 쓰는
기준값이다. 실측 데이터가 확보되기 전까지는 고정값을 사용하며, 실험마다
재컴파일하지 않도록 환경변수로 덮어쓸 수 있다.
*/
package plugin

import (
	"os"
	"strconv"

	"k8s.io/klog/v2"
)

const (
	// 기준값이 예측 fixture보다 지나치게 크면 정규화 후 모든 후보가 같은
	// 점수로 뭉개진다. 0.05는 현재 모델별 fixture 범위(0.010~0.032)를
	// 감싸면서 후보 사이 차이를 점수로 남기는 값이다.
	defaultMaxCostUSD  = 0.05
	defaultGPUTDPWatts = 300.0
	// 상한이 카탈로그 최대 모델보다 낮으면 그 이상 전력을 쓰는 모델은 모두
	// ratio 1.0으로 포화되어 같은 점수를 받는다. 200W는 Warboy(60W)와
	// RNGD(110W)를 모두 포화 없이 담는 값이다.
	defaultNPUTDPWatts = 200.0

	envMaxCostUSD  = "SCHED_MAX_COST_USD"
	envGPUTDPWatts = "SCHED_GPU_TDP_WATTS"
	envNPUTDPWatts = "SCHED_NPU_TDP_WATTS"
)

var (
	maxCostUSD  = positiveFloatFromEnv(envMaxCostUSD, defaultMaxCostUSD)
	gpuTDPWatts = positiveFloatFromEnv(envGPUTDPWatts, defaultGPUTDPWatts)
	npuTDPWatts = positiveFloatFromEnv(envNPUTDPWatts, defaultNPUTDPWatts)
)

func positiveFloatFromEnv(key string, fallback float64) float64 {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		klog.InfoS("invalid scoring tunable, using default",
			"env", key, "value", raw, "default", fallback)
		return fallback
	}
	return value
}
