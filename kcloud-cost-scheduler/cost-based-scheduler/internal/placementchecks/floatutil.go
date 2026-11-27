package placementchecks

import "math"

func floatIsFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
