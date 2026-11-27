package utils

import "testing"

func TestSelectPreferredNodeCostFirstThenTotal(t *testing.T) {
	eLo, eHi := 300.0, 900.0
	results := PluginResultMap{
		"gpu-node": {
			TotalNodeScore:      50,
			CostSelectionActive: true,
			CostEligible:        true,
			PredictedNetEnergyJ: &eHi,
		},
		"npu-node": {
			TotalNodeScore:      10,
			CostSelectionActive: true,
			CostEligible:        true,
			PredictedNetEnergyJ: &eLo,
		},
	}
	host, err := SelectPreferredNode(results)
	if err != nil || host != "npu-node" {
		t.Fatalf("got %q (%v), want npu-node", host, err)
	}
}

func TestSelectPreferredNodeServerEnergyOverridesDeviceNet(t *testing.T) {
	// Device net favors gpu, server total favors npu → server wins.
	devLo, devHi := 100.0, 900.0
	srvLo, srvHi := 500.0, 800.0
	results := PluginResultMap{
		"gpu-node": {
			TotalNodeScore:         10,
			CostSelectionActive:    true,
			CostEligible:           true,
			PredictedNetEnergyJ:    &devLo,
			PredictedServerEnergyJ: &srvHi,
		},
		"npu-node": {
			TotalNodeScore:         10,
			CostSelectionActive:    true,
			CostEligible:           true,
			PredictedNetEnergyJ:    &devHi,
			PredictedServerEnergyJ: &srvLo,
		},
	}
	host, err := SelectPreferredNode(results)
	if err != nil || host != "npu-node" {
		t.Fatalf("got %q (%v), want npu-node (lower server energy)", host, err)
	}
}

func TestSelectPreferredNodeEqualEnergyUsesTotalScore(t *testing.T) {
	e := 300.0
	results := PluginResultMap{
		"gpu-node": {
			TotalNodeScore:      40,
			CostSelectionActive:  true,
			CostEligible:        true,
			PredictedNetEnergyJ: &e,
		},
		"npu-node": {
			TotalNodeScore:      10,
			CostSelectionActive:  true,
			CostEligible:        true,
			PredictedNetEnergyJ: &e,
		},
	}
	host, err := SelectPreferredNode(results)
	if err != nil || host != "gpu-node" {
		t.Fatalf("got %q (%v), want gpu-node", host, err)
	}
}

func TestSelectPreferredNodeNoEligibleErrors(t *testing.T) {
	results := PluginResultMap{
		"gpu-node": {IsFiltered: true, TotalNodeScore: 40},
		"npu-node": {IsFiltered: true, TotalNodeScore: 10},
	}
	if _, err := SelectPreferredNode(results); err == nil {
		t.Fatal("want error when no feasible node")
	}
}

func TestSelectPreferredNodeCostActiveNoEligibleNoFallback(t *testing.T) {
	// Enforce on, zero CostEligible: must not pick GPU by TotalNodeScore.
	results := PluginResultMap{
		"gpu-node": {CostSelectionActive: true, TotalNodeScore: 40},
		"npu-node": {CostSelectionActive: true, IsFiltered: true, TotalNodeScore: 10},
	}
	if host, err := SelectPreferredNode(results); err == nil {
		t.Fatalf("got %q, want error (no TotalScore fallback under cost policy)", host)
	}
}
