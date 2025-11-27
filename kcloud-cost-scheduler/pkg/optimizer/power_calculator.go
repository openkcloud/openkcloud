/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package optimizer

// PowerCalculator calculates power consumption of workloads
type PowerCalculator struct {
	// Power per CPU core in Watts
	CPUPowerPerCore float64
	// Power per GB memory in Watts
	MemoryPowerPerGB float64
	// Power per GPU in Watts
	GPUPowerPerUnit float64
	// Power per NPU in Watts
	NPUPowerPerUnit float64
	// Base system power in Watts
	BaseSystemPower float64
}

// NewPowerCalculator creates a new power calculator with default values
func NewPowerCalculator() *PowerCalculator {
	return &PowerCalculator{
		CPUPowerPerCore:  15.0,  // 15W per CPU core
		MemoryPowerPerGB: 0.5,   // 0.5W per GB memory
		GPUPowerPerUnit:  300.0, // 300W per GPU (NVIDIA A100 TDP)
		NPUPowerPerUnit:  250.0, // 250W per NPU
		BaseSystemPower:  50.0,  // 50W base system power
	}
}

// CalculatePower calculates the total power consumption in Watts
func (p *PowerCalculator) CalculatePower(cpuCores, memoryGB float64, gpuCount, npuCount int32) float64 {
	cpuPower := cpuCores * p.CPUPowerPerCore
	memoryPower := memoryGB * p.MemoryPowerPerGB
	gpuPower := float64(gpuCount) * p.GPUPowerPerUnit
	npuPower := float64(npuCount) * p.NPUPowerPerUnit

	totalPower := p.BaseSystemPower + cpuPower + memoryPower + gpuPower + npuPower

	return totalPower
}

// CalculateDailyEnergy calculates energy consumption in kWh for 24 hours
func (p *PowerCalculator) CalculateDailyEnergy(powerWatts float64) float64 {
	return (powerWatts * 24) / 1000.0 // Convert to kWh
}

// CalculateMonthlyEnergy calculates energy consumption in kWh for 30 days
func (p *PowerCalculator) CalculateMonthlyEnergy(powerWatts float64) float64 {
	return (powerWatts * 24 * 30) / 1000.0 // Convert to kWh
}
