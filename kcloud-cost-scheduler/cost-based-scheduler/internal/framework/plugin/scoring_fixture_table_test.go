package plugin

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/fixture"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// G0 공식 fixture 표의 각 행이 현재 Cost/Power 수식과 일치하는지 확인한다.
func TestScoringFixtureTableMatchesFormula(t *testing.T) {
	doc := fixture.MustLoad()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, row := range doc.ModelPowerCost {
		name := fmt.Sprintf("%s_x%d", row.Model, row.Quantity)
		t.Run(name, func(t *testing.T) {
			cache := utils.NewCache(ctx)
			node := fixtureNode(name+"-node", row.Resource, strconv.Itoa(row.Quantity), row.Model)
			if err := cache.AddNode(node, nil); err != nil {
				t.Fatalf("AddNode() error = %v", err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: name + "-pod",
				Annotations: map[string]string{
					"sched.ai/predicted-cost-by-model":  fmt.Sprintf("%s=%g", row.Model, row.PredictedCost),
					"sched.ai/predicted-power-by-model": fmt.Sprintf("%s=%g", row.Model, row.PredictedPowerW),
					"sched.ai/prediction-provider":      fixture.Provider,
				},
			}}

			wantCost := expectedCostScore(row.PredictedCost)
			wantPower := expectedPowerScore(row.Resource, row.PredictedPowerW)

			costScore, costStatus := NewWorkloadAwareCost(cache).Score(ctx, pod, node.Name)
			if !costStatus.IsSuccess() {
				t.Fatalf("cost status = %s", costStatus.Message())
			}
			if costScore != wantCost {
				t.Errorf("cost score = %d, want %d (cost=%g, maxCostUSD=%v)",
					costScore, wantCost, row.PredictedCost, maxCostUSD)
			}

			powerScore, powerStatus := NewWorkloadAwarePower(cache).Score(ctx, pod, node.Name)
			if !powerStatus.IsSuccess() {
				t.Fatalf("power status = %s", powerStatus.Message())
			}
			if powerScore != wantPower {
				t.Errorf("power score = %d, want %d (power=%g)",
					powerScore, wantPower, row.PredictedPowerW)
			}

			t.Logf("%-8s %-20s x%d  cost=%-8g score=%2d   power=%-5g score=%2d   total=%d",
				row.Model, row.Resource, row.Quantity,
				row.PredictedCost, costScore, row.PredictedPowerW, powerScore, costScore+powerScore)
		})
	}
}

func expectedCostScore(costUSD float64) int64 {
	normalized := costUSD / maxCostUSD
	if normalized > 1.0 {
		normalized = 1.0
	}
	return int64(25.0 * (1.0 - normalized))
}

func expectedPowerScore(resourceName string, powerW float64) int64 {
	tdp := gpuTDPWatts
	if resourceName != "nvidia.com/gpu" {
		tdp = npuTDPWatts
	}
	ratio := powerW / tdp
	if ratio > 1.0 {
		ratio = 1.0
	}
	return int64(15.0 * (1.0 - ratio*0.5))
}

// 정규화 기준값이 fixture보다 지나치게 크면 서로 다른 비용이 같은 점수로
// 뭉개진다. 08.25 실험에서 A100과 L4가 모두 24점이던 상태를 회귀로 고정한다.
func TestCostScoreDistinguishesFixtureRange(t *testing.T) {
	doc := fixture.MustLoad()
	a100 := doc.FindModel("a100", 1)
	l4 := doc.FindModel("l4", 1)
	if a100 == nil || l4 == nil {
		t.Fatal("G0 fixture missing a100 or l4")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	a100Node := fixtureNode("a100-node", a100.Resource, "1", a100.Model)
	l4Node := fixtureNode("l4-node", l4.Resource, "1", l4.Model)
	for _, node := range []*corev1.Node{a100Node, l4Node} {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "cost-resolution",
		Annotations: map[string]string{
			"sched.ai/predicted-cost-by-model": fmt.Sprintf("%s=%g,%s=%g",
				a100.Model, a100.PredictedCost, l4.Model, l4.PredictedCost),
		},
	}}

	scorePlugin := NewWorkloadAwareCost(cache)
	a100Score, _ := scorePlugin.Score(ctx, pod, a100Node.Name)
	l4Score, _ := scorePlugin.Score(ctx, pod, l4Node.Name)

	if a100Score == l4Score {
		t.Fatalf("cost scores are identical (%d) for %.3gx cost difference; "+
			"maxCostUSD=%v is too large to resolve the fixture range",
			a100Score, a100.PredictedCost/l4.PredictedCost, maxCostUSD)
	}
	if l4Score <= a100Score {
		t.Errorf("cost scores = a100:%d l4:%d, want cheaper L4 to score higher",
			a100Score, l4Score)
	}
}

// NPU 상한이 카탈로그 최대 모델보다 낮으면 Warboy와 RNGD가 모두 포화되어
// 전력 점수로 구분되지 않는다.
func TestNPUPowerScoreDistinguishesWarboyFromRNGD(t *testing.T) {
	doc := fixture.MustLoad()
	warboy := doc.FindModel("warboy", 1)
	rngd := doc.FindModel("rngd", 1)
	if warboy == nil || rngd == nil {
		t.Fatal("G0 fixture missing warboy or rngd")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cache := utils.NewCache(ctx)
	warboyNode := fixtureNode("warboy-node", warboy.Resource, "1", warboy.Model)
	rngdNode := fixtureNode("rngd-node", rngd.Resource, "1", rngd.Model)
	for _, node := range []*corev1.Node{warboyNode, rngdNode} {
		if err := cache.AddNode(node, nil); err != nil {
			t.Fatalf("AddNode(%s) error = %v", node.Name, err)
		}
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "npu-power-resolution",
		Annotations: map[string]string{
			"sched.ai/predicted-power-by-model": fmt.Sprintf("%s=%g,%s=%g",
				warboy.Model, warboy.PredictedPowerW, rngd.Model, rngd.PredictedPowerW),
		},
	}}

	scorePlugin := NewWorkloadAwarePower(cache)
	warboyScore, _ := scorePlugin.Score(ctx, pod, warboyNode.Name)
	rngdScore, _ := scorePlugin.Score(ctx, pod, rngdNode.Name)

	if warboyScore == rngdScore {
		t.Fatalf("NPU power scores are identical (%d) for %gW vs %gW; "+
			"npuTDPWatts=%v saturates both models",
			warboyScore, warboy.PredictedPowerW, rngd.PredictedPowerW, npuTDPWatts)
	}
	if warboyScore <= rngdScore {
		t.Errorf("power scores = warboy:%d rngd:%d, want lower-power Warboy to score higher",
			warboyScore, rngdScore)
	}
}

func fixtureNode(name, resourceName, quantity, model string) *corev1.Node {
	labels := map[string]string{}
	if resourceName == "nvidia.com/gpu" {
		labels["nvidia.com/gpu.product"] = model
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:                resource.MustParse("16"),
			corev1.ResourceMemory:             resource.MustParse("64Gi"),
			corev1.ResourceName(resourceName): resource.MustParse(quantity),
		}},
	}
}
