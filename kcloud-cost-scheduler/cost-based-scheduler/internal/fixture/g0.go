/*
Package fixture holds G0 offline absolute-value tables.

These numbers are provider=fixture / NOT MEASURED. They are the canonical
offline inputs for predicted power/cost annotations and for SLA/tariff
decomposition checks. Real measurements replace the same keys later.
*/
package fixture

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	Provider       = "fixture"
	ModelVersion   = "virtual-v1"
	CostUnit       = "USD/work"
	PowerUnit      = "W"
	DefaultRelPath = "config/fixtures/g0-offline.yaml"
)

type ModelPowerCost struct {
	Model           string  `yaml:"model"`
	Resource        string  `yaml:"resource"`
	Quantity        int     `yaml:"quantity"`
	PredictedPowerW float64 `yaml:"predicted_power_w"`
	PredictedCost   float64 `yaml:"predicted_cost"`
}

type Tariff struct {
	Currency               string             `yaml:"currency"`
	ElectricityPricePerKWh float64            `yaml:"electricity_price_per_kwh"`
	DevicePricePerHour     map[string]float64 `yaml:"device_price_per_hour"`
}

type BatchInferenceSLA struct {
	MaxCompletionS float64 `yaml:"max_completion_s"`
	MinQPS         float64 `yaml:"min_qps"`
}

type ServingSLA struct {
	P95LatencyMS float64 `yaml:"p95_latency_ms"`
	MinQPS       float64 `yaml:"min_qps"`
	MaxErrorRate float64 `yaml:"max_error_rate"`
}

type TrainingSLA struct {
	MaxCompletionS float64 `yaml:"max_completion_s"`
	MinStepsPerS   float64 `yaml:"min_steps_per_s"`
}

type SLA struct {
	BatchInference BatchInferenceSLA `yaml:"batch_inference"`
	Serving        ServingSLA        `yaml:"serving"`
	Training       TrainingSLA       `yaml:"training"`
}

type Document struct {
	Provider       string           `yaml:"provider"`
	ModelVersion   string           `yaml:"model_version"`
	Measured       bool             `yaml:"measured"`
	CostUnit       string           `yaml:"cost_unit"`
	PowerUnit      string           `yaml:"power_unit"`
	Notes          string           `yaml:"notes"`
	ModelPowerCost []ModelPowerCost `yaml:"model_power_cost"`
	Tariff         Tariff           `yaml:"tariff"`
	SLA            SLA              `yaml:"sla"`
}

type WorkCostBreakdown struct {
	EnergyWh         float64
	EnergyCost       float64
	DeviceCost       float64
	TotalCostPerWork float64
}

var (
	loadOnce sync.Once
	cached   *Document
	loadErr  error
)

func DefaultPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return DefaultRelPath
	}
	// internal/fixture -> cost-based-scheduler root
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	return filepath.Join(root, DefaultRelPath)
}

func Load(path string) (*Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return &doc, nil
}

func MustLoad() *Document {
	loadOnce.Do(func() {
		cached, loadErr = Load(DefaultPath())
	})
	if loadErr != nil {
		panic(loadErr)
	}
	return cached
}

func (d *Document) Validate() error {
	if d.Provider != Provider {
		return fmt.Errorf("provider = %q, want %q", d.Provider, Provider)
	}
	if d.Measured {
		return fmt.Errorf("measured must be false for offline fixtures")
	}
	if d.CostUnit != CostUnit {
		return fmt.Errorf("cost_unit = %q, want %q", d.CostUnit, CostUnit)
	}
	if len(d.ModelPowerCost) == 0 {
		return fmt.Errorf("model_power_cost is empty")
	}
	for i, row := range d.ModelPowerCost {
		if row.Model == "" || row.Resource == "" || row.Quantity <= 0 {
			return fmt.Errorf("model_power_cost[%d]: invalid identity", i)
		}
		if row.PredictedPowerW <= 0 || row.PredictedCost <= 0 {
			return fmt.Errorf("model_power_cost[%d]: power/cost must be positive", i)
		}
		if _, ok := d.Tariff.DevicePricePerHour[row.Model]; !ok {
			return fmt.Errorf("model_power_cost[%d]: missing device_price_per_hour for %s", i, row.Model)
		}
	}
	if d.Tariff.ElectricityPricePerKWh <= 0 {
		return fmt.Errorf("electricity_price_per_kwh must be positive")
	}
	return nil
}

// FindModel returns the row for model+quantity, or nil.
func (d *Document) FindModel(model string, quantity int) *ModelPowerCost {
	for i := range d.ModelPowerCost {
		row := &d.ModelPowerCost[i]
		if row.Model == model && row.Quantity == quantity {
			return row
		}
	}
	return nil
}

// AnnotationMaps builds predicted-*-by-model strings for the given model keys.
// When multiple quantities exist for one model (warboy), the first matching
// quantity in the table order is used for Score-style single-value lookup.
func (d *Document) PredictedCostByModelAnnotation() string {
	return joinModelValues(d.ModelPowerCost, func(r ModelPowerCost) string {
		return fmt.Sprintf("%s=%g", r.Model, r.PredictedCost)
	})
}

func (d *Document) PredictedPowerByModelAnnotation() string {
	return joinModelValues(d.ModelPowerCost, func(r ModelPowerCost) string {
		return fmt.Sprintf("%s=%g", r.Model, r.PredictedPowerW)
	})
}

func joinModelValues(rows []ModelPowerCost, format func(ModelPowerCost) string) string {
	seen := map[string]bool{}
	out := ""
	for _, row := range rows {
		if seen[row.Model] {
			continue
		}
		seen[row.Model] = true
		if out != "" {
			out += ","
		}
		out += format(row)
	}
	return out
}

// EnergyWh uses average execution power, not peak.
func EnergyWh(averagePowerW, durationS float64) float64 {
	if averagePowerW <= 0 || durationS <= 0 {
		return 0
	}
	return averagePowerW * durationS / 3600.0
}

// WorkCost breaks energy and device usage into USD/work using tariff fixtures.
func (d *Document) WorkCost(model string, deviceCount int, averagePowerW, durationS float64) (WorkCostBreakdown, error) {
	price, ok := d.Tariff.DevicePricePerHour[model]
	if !ok {
		return WorkCostBreakdown{}, fmt.Errorf("unknown model %q in device_price_per_hour", model)
	}
	if deviceCount <= 0 || averagePowerW <= 0 || durationS <= 0 {
		return WorkCostBreakdown{}, fmt.Errorf("deviceCount/power/duration must be positive")
	}
	energyWh := EnergyWh(averagePowerW, durationS)
	energyCost := energyWh / 1000.0 * d.Tariff.ElectricityPricePerKWh
	deviceCost := price * durationS / 3600.0 * float64(deviceCount)
	return WorkCostBreakdown{
		EnergyWh:         energyWh,
		EnergyCost:       energyCost,
		DeviceCost:       deviceCost,
		TotalCostPerWork: energyCost + deviceCost,
	}, nil
}

func (d *Document) BatchInferencePass(completionS, qps float64) bool {
	s := d.SLA.BatchInference
	return completionS > 0 && completionS <= s.MaxCompletionS && qps >= s.MinQPS
}

func (d *Document) ServingPass(p95MS, qps, errorRate float64) bool {
	s := d.SLA.Serving
	return p95MS > 0 && p95MS <= s.P95LatencyMS && qps >= s.MinQPS && errorRate >= 0 && errorRate <= s.MaxErrorRate
}

func (d *Document) TrainingPass(completionS, stepsPerS float64) bool {
	s := d.SLA.Training
	return completionS > 0 && completionS <= s.MaxCompletionS && stepsPerS >= s.MinStepsPerS
}
