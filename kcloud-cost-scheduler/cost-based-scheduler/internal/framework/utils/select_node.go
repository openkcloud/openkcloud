package utils

import "fmt"

// SelectPreferredNode picks the Bind target from scored PluginResultMap.
//
// When CostSelectionActive is set on any node (Enforce cost-first policy):
//  1. consider only non-filtered CostEligible nodes
//  2. lower CostCompareEnergyJ wins (server total when set, else device net)
//  3. equal energy → higher TotalNodeScore (other plugins)
//  4. still tied → lexicographically smaller node name (stable, matches tests)
//  5. zero eligible → error (no fallback to TotalNodeScore)
//
// When cost selection is not active, use max TotalNodeScore among non-filtered
// nodes (legacy sum ranking), with the same name tie-break.
func SelectPreferredNode(results PluginResultMap) (string, error) {
	costMode := false
	for _, pr := range results {
		if pr.CostSelectionActive {
			costMode = true
			break
		}
	}

	best := ""
	if costMode {
		var bestEnergy float64
		bestScore := 0
		for name, pr := range results {
			ePtr := pr.CostCompareEnergyJ()
			if pr.IsFiltered || !pr.CostEligible || ePtr == nil {
				continue
			}
			e := *ePtr
			if best == "" ||
				e < bestEnergy ||
				(e == bestEnergy && pr.TotalNodeScore > bestScore) ||
				(e == bestEnergy && pr.TotalNodeScore == bestScore && name < best) {
				best = name
				bestEnergy = e
				bestScore = pr.TotalNodeScore
			}
		}
		if best == "" {
			return "", fmt.Errorf("no valid cost-eligible node for Bind")
		}
		return best, nil
	}

	bestScore := 0
	first := true
	for name, pr := range results {
		if pr.IsFiltered {
			continue
		}
		if first ||
			pr.TotalNodeScore > bestScore ||
			(pr.TotalNodeScore == bestScore && name < best) {
			best = name
			bestScore = pr.TotalNodeScore
			first = false
		}
	}
	if best == "" {
		return "", fmt.Errorf("no feasible scored node for Bind")
	}
	return best, nil
}
