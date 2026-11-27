// ============================================================
// types.go: node-agent 공용 타입 (Snapshot / Validation 결과 / 벤더 매핑)
// 상세: scan 이 만든 Snapshot 을 report/validate 가 공유한다. Validation 결과 타입은
// NDR.status.validation 스키마(ValidationStatus)와 1:1 대응하고, 벤더별 리소스명·/dev glob 매핑도 여기서 관리한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import "time"

// nowFunc 는 테스트에서 시간을 고정할 수 있도록 분리한 시계 훅이다.
var nowFunc = time.Now

// Snapshot 은 1회 스캔 결과다. 4능력(report/validate/metrics/label)이 동일 Snapshot 을
// 소비하여 중복 스캔을 피한다. Validation 은 validate 능력이 채우며 없을 수 있다.
type Snapshot struct {
	Node                string
	Devices             []Detected
	PassthroughReserved bool
	Validation          *ValidationResult
	ScanTime            time.Time
	// MigLgip 은 `nvidia-smi mig -lgip` 원문(노드당 1회 수집, ACPP 의 MIG profile discovery 소스).
	// NVIDIA device 가 없거나 nvidia-smi 부재 시 "". PCI 를 특정 못했을 때(PciAddrs["nvidia"] 공란)
	// buildDevices 의 폴백 경로에서만 쓰인다 — per-PCI 관측이 기본이다.
	MigLgip string
	// PciAddrs 는 vendorKey → 그 벤더 장치의 PCI 주소 목록이다(노드당 1회 수집, Scan() 이 채움).
	// report.go buildDevices 가 이 목록으로 device entry 를 카드 1장당 하나씩 fan-out 한다.
	// NVIDIA 는 여기에 더해 각 PCI 로 observeMig 를 호출해 MIG 필드를 per-PCI 로 채운다. 해당
	// 키가 비어있으면(sysfs 부재 등) 기존 단일 집계 entry(+NVIDIA 는 전역 MigLgip) 폴백을 유지한다.
	PciAddrs map[string][]string
	// Sensors 는 커널 hwmon 에서 읽은 장치 텔레메트리(온도 °C / 전력 W)다. metrics 능력이
	// kcloud_device_* 로 방출한다. hwmon 미등록 벤더(NVIDIA)나 미지원 노드에서는 빈 슬라이스다.
	Sensors []DeviceSensor
	// Occupancy 는 PE 점유 집계(Furiosa RNGD)다. 지원되지 않는 벤더/노드에서는 비어 있다.
	Occupancy []PeOccupancy
}

// ValidationResult 는 NDR.status.validation 에 기록되는 노드 검증 결과다.
// 스키마의 ValidationStatus 와 대응한다. 멀티벤더 노드는 Vendor 에 벤더 목록을,
// Steps 에 벤더 접두사 붙은 단계들을 담는다(단일벤더는 접두사 없음).
type ValidationResult struct {
	Passed      bool             `json:"passed"`
	LastRunTime time.Time        `json:"lastRunTime"`
	Vendor      string           `json:"vendor"`
	Steps       []ValidationStep `json:"steps"`
}

// ValidationStep 은 검증 5단계 각각의 결과다. FAIL 단계를 식별하려면
// Name/Passed/Message 를 담는다.
// NotApplicable 은 Passed 와 별개 축이다 — 통과도 실패도 아닌 "해당없음" 상태를 표시한다
// (예: control-plane 배제 노드의 devicePlugin/sampleWorkload). aggregate 는 이 단계를
// 게이트 실패로 세지 않는다.
type ValidationStep struct {
	Name          string `json:"name"`
	Passed        bool   `json:"passed"`
	NotApplicable bool   `json:"notApplicable,omitempty"`
	Message       string `json:"message,omitempty"`
}

// vendorKey 는 스냅샷의 (vendor, model) 을 검증/매핑용 단일 키로 정규화한다.
// furiosa 는 warboy/rngd 로 분리(리소스명·모듈이 다름), 그 외는 vendor 그대로.
func vendorKey(vendor, model string) string {
	if vendor == "furiosa" && model == "rngd" {
		return "rngd"
	}
	return vendor
}

// vendorResourceNames 는 벤더 키 → device-plugin 리소스명(정확 매칭 후보)이다.
// 값은 deploy/helm/values.yaml 기준. CR 에서 재정의될 수 있어 정확 매칭 실패 시
// allocatableForVendor 가 substring fallback 으로 보완한다.
var vendorResourceNames = map[string][]string{
	"nvidia":      {"nvidia.com/gpu"},
	"furiosa":     {"beta.furiosa.ai/npu", "furiosa.ai/warboy"}, // warboy
	"rngd":        {"furiosa.ai/rngd"},
	"rebellions":  {"rebellions.ai/ATOM", "rebellions.ai/ato"},
	"tenstorrent": {"tenstorrent.com/blackhole"},
}

// vendorSubstrings 는 정확 매칭 실패 시 allocatable 키에서 벤더를 추정할 substring 이다.
var vendorSubstrings = map[string][]string{
	"nvidia":      {"nvidia.com/"},
	"furiosa":     {"furiosa.ai/npu", "furiosa.ai/warboy"},
	"rngd":        {"furiosa.ai/rngd"},
	"rebellions":  {"rebellions.ai/"},
	"tenstorrent": {"tenstorrent.com/"},
}

// vendorDevGlobs 는 벤더 키 → /dev 노드 glob 후보다(deviceNode step 용, best-effort).
// NPU 벤더는 장치 경로가 확정적이지 않아 후보군으로 두고, 미매치+드라이버로드 시
// soft-pass 처리한다(validate.go 참고).
var vendorDevGlobs = map[string][]string{
	"nvidia":      {"/dev/nvidia[0-9]*", "/dev/nvidiactl"},
	"furiosa":     {"/dev/npu*"},
	"rngd":        {"/dev/rngd*", "/dev/npu*"},
	"rebellions":  {"/dev/rbln*"},
	"tenstorrent": {"/dev/tenstorrent/*", "/dev/tenstorrent*"},
}
