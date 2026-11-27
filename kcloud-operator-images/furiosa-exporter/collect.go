// ============================================================
// collect.go: 장치 판독을 지표 샘플로 변환한다(순수 로직, cgo 없음)
// 상세: DeviceSource 뒤에 벤더 SDK 를 숨겨 실장치 없이 시험 가능하게 한다. 장치 하나가 실패해도
// 나머지 샘플은 살려, 부분 실패가 노드 전체 관측을 지우지 않는다.
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================
package main

// CoreReading 은 PE 코어 하나의 사용률이다.
type CoreReading struct {
	Core         uint32
	UsagePercent float64
}

// CoreClock 은 PE 코어 하나의 동작 주파수다. 단위는 MHz — 벤더 SMI 원단위를 그대로 쓴다.
type CoreClock struct {
	Core uint32
	MHz  uint32
}

// CoreCycle 은 PE 코어 하나의 누적 사이클이다. Total 은 전체, TaskExec 는 작업 실행에 쓴 사이클이다.
type CoreCycle struct {
	Core     uint32
	Total    uint64
	TaskExec uint64
}

// DeviceReading 은 벤더 SDK 에서 읽은 장치 하나의 원본 값이다.
// Err 가 비어 있지 않으면 그 장치는 이번 회차에 건너뛴다.
type DeviceReading struct {
	Index           uint32
	Name            string
	Serial          string
	UUID            string
	BDF             string
	Arch            string
	FirmwareVersion string
	DriverVersion   string
	CoreNum         uint32
	Cores           []CoreReading
	CoreClocks      []CoreClock
	Cycles          []CoreCycle
	DramTotalBytes  uint64
	DramInUseBytes  uint64
	PowerWatts      float64
	TempSocPeak     float64
	TempAmbient     float64
	Alive           bool
	AliveValid      bool   // Alive 를 실제로 읽었는지. 읽지 못한 0 과 죽은 0 을 가른다
	Governor        string // "performance" | "powersave". 읽지 못하면 빈 문자열
	ThrottleReason  uint32 // 비트마스크. 0 이 정상
	ThrottleValid   bool   // ThrottleReason 을 실제로 읽었는지. 0 과 미수집을 가른다
	Err             error
}

// DeviceSource 는 장치 판독의 공급원이다. 실구현은 device.go 의 smi 백엔드이고
// 시험은 가짜를 넣는다.
type DeviceSource interface {
	Devices() ([]DeviceReading, error)
}

// DeviceSample 은 지표로 방출할 장치 하나의 값이다.
type DeviceSample struct {
	PCI             string
	Name            string
	UUID            string
	Arch            string
	FirmwareVersion string
	DriverVersion   string
	CoreNum         uint32
	Cores           []CoreReading
	CoreClocks      []CoreClock
	Cycles          []CoreCycle
	Governor        string
	ThrottleReason  uint32
	ThrottleValid   bool
	DramTotalBytes  uint64
	DramInUseBytes  uint64
	PowerWatts      float64
	TempSocPeak     float64
	TempAmbient     float64
	Alive           bool
	AliveValid      bool
}

// Collect 는 공급원의 판독을 샘플로 옮긴다. 개별 장치 오류는 그 장치만 버린다.
func Collect(src DeviceSource) ([]DeviceSample, error) {
	readings, err := src.Devices()
	if err != nil {
		return nil, err
	}
	out := make([]DeviceSample, 0, len(readings))
	for _, r := range readings {
		if r.Err != nil {
			continue
		}
		out = append(out, DeviceSample{
			PCI:             r.BDF,
			Name:            r.Name,
			UUID:            r.UUID,
			Arch:            r.Arch,
			FirmwareVersion: r.FirmwareVersion,
			DriverVersion:   r.DriverVersion,
			CoreNum:         r.CoreNum,
			Cores:           r.Cores,
			CoreClocks:      r.CoreClocks,
			Cycles:          r.Cycles,
			Governor:        r.Governor,
			ThrottleReason:  r.ThrottleReason,
			ThrottleValid:   r.ThrottleValid,
			DramTotalBytes:  r.DramTotalBytes,
			DramInUseBytes:  r.DramInUseBytes,
			PowerWatts:      r.PowerWatts,
			TempSocPeak:     r.TempSocPeak,
			TempAmbient:     r.TempAmbient,
			Alive:           r.Alive,
			AliveValid:      r.AliveValid,
		})
	}
	return out, nil
}
