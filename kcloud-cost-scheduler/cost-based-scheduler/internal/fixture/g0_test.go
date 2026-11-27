package fixture

import (
	"math"
	"testing"
)

func TestG0OfflineDocumentLoadsAndValidates(t *testing.T) {
	doc, err := Load(DefaultPath())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if doc.Provider != Provider || doc.Measured {
		t.Fatalf("provider/measured = %q/%v, want fixture/false", doc.Provider, doc.Measured)
	}
	if len(doc.ModelPowerCost) != 7 {
		t.Fatalf("model_power_cost rows = %d, want 7", len(doc.ModelPowerCost))
	}

	want := []struct {
		model string
		qty   int
		power float64
		cost  float64
	}{
		{"a100", 1, 250, 0.032},
		{"l4", 1, 75, 0.010},
		{"h100", 1, 350, 0.048},
		{"warboy", 1, 60, 0.0072},
		{"warboy", 2, 105, 0.0130},
		{"warboy", 4, 195, 0.0240},
		{"rngd", 1, 110, 0.0140},
	}
	for _, row := range want {
		got := doc.FindModel(row.model, row.qty)
		if got == nil {
			t.Fatalf("FindModel(%s,%d) = nil", row.model, row.qty)
		}
		if got.PredictedPowerW != row.power || got.PredictedCost != row.cost {
			t.Errorf("%s x%d = power %v cost %v, want %v %v",
				row.model, row.qty, got.PredictedPowerW, got.PredictedCost, row.power, row.cost)
		}
	}
}

func TestG0WorkCostUsesAveragePowerNotPeak(t *testing.T) {
	doc := MustLoad()
	row := doc.FindModel("a100", 1)
	if row == nil {
		t.Fatal("missing a100")
	}

	// duration 3600s = 1 hour → energy_wh = average_power_w
	avg := row.PredictedPowerW
	peak := 400.0
	durationS := 3600.0

	got, err := doc.WorkCost("a100", 1, avg, durationS)
	if err != nil {
		t.Fatalf("WorkCost() error = %v", err)
	}
	if got.EnergyWh != avg {
		t.Errorf("EnergyWh = %v, want average power %v (not peak %v)", got.EnergyWh, avg, peak)
	}

	wantEnergyCost := avg / 1000.0 * doc.Tariff.ElectricityPricePerKWh
	wantDeviceCost := doc.Tariff.DevicePricePerHour["a100"] * 1.0
	if math.Abs(got.EnergyCost-wantEnergyCost) > 1e-9 {
		t.Errorf("EnergyCost = %v, want %v", got.EnergyCost, wantEnergyCost)
	}
	if math.Abs(got.DeviceCost-wantDeviceCost) > 1e-9 {
		t.Errorf("DeviceCost = %v, want %v", got.DeviceCost, wantDeviceCost)
	}
	if math.Abs(got.TotalCostPerWork-(wantEnergyCost+wantDeviceCost)) > 1e-9 {
		t.Errorf("TotalCostPerWork = %v, want %v", got.TotalCostPerWork, wantEnergyCost+wantDeviceCost)
	}
}

func TestG0WorkCostRejectsInvalidInputs(t *testing.T) {
	doc := MustLoad()
	cases := []struct {
		name  string
		model string
		count int
		power float64
		dur   float64
	}{
		{"unknown model", "missing", 1, 100, 10},
		{"zero count", "l4", 0, 100, 10},
		{"zero power", "l4", 1, 0, 10},
		{"negative duration", "l4", 1, 100, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := doc.WorkCost(tc.model, tc.count, tc.power, tc.dur); err == nil {
				t.Fatal("WorkCost() error = nil, want rejection")
			}
		})
	}
}

func TestG0SLAPassFailBoundaries(t *testing.T) {
	doc := MustLoad()

	if !doc.BatchInferencePass(doc.SLA.BatchInference.MaxCompletionS, doc.SLA.BatchInference.MinQPS) {
		t.Error("batch at exact thresholds should pass")
	}
	if doc.BatchInferencePass(doc.SLA.BatchInference.MaxCompletionS+1, doc.SLA.BatchInference.MinQPS) {
		t.Error("batch over max_completion_s should fail")
	}
	if doc.BatchInferencePass(100, doc.SLA.BatchInference.MinQPS-1) {
		t.Error("batch under min_qps should fail")
	}

	if !doc.ServingPass(doc.SLA.Serving.P95LatencyMS, doc.SLA.Serving.MinQPS, 0) {
		t.Error("serving at exact p95/min_qps should pass")
	}
	if doc.ServingPass(doc.SLA.Serving.P95LatencyMS+0.1, doc.SLA.Serving.MinQPS, 0) {
		t.Error("serving over p95 should fail")
	}
	if doc.ServingPass(10, doc.SLA.Serving.MinQPS, doc.SLA.Serving.MaxErrorRate+0.001) {
		t.Error("serving over max_error_rate should fail")
	}

	if !doc.TrainingPass(doc.SLA.Training.MaxCompletionS, doc.SLA.Training.MinStepsPerS) {
		t.Error("training at exact thresholds should pass")
	}
	if doc.TrainingPass(doc.SLA.Training.MaxCompletionS+1, doc.SLA.Training.MinStepsPerS) {
		t.Error("training over max_completion_s should fail")
	}
}

func TestG0FixtureIsNotMeasured(t *testing.T) {
	doc := MustLoad()
	if doc.Measured || doc.Provider != "fixture" || doc.ModelVersion != ModelVersion {
		t.Fatalf("metadata = measured:%v provider:%s version:%s",
			doc.Measured, doc.Provider, doc.ModelVersion)
	}
}
