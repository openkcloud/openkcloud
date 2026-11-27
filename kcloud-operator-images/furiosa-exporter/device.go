//go:build smi

// ============================================================
// device.go: furiosa-smi 백엔드(cgo). DeviceSource 의 실구현.
// 상세: 이 파일만 벤더 SDK 를 안다. smi 태그 뒤에 둬 라이브러리 없는 머신에서도 나머지
// 패키지가 빌드·시험되게 한다. Observer 는 시간 창으로 계산하므로 프로세스 수명 동안 재사용한다.
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/furiosa-ai/furiosa-smi-go/pkg/smi"
)

type smiSource struct {
	observer      smi.Observer
	driverVersion string
}

// 1초 표본이 타입 단언으로 이 인터페이스를 찾는다. 시그니처가 어긋나면 표본이 조용히 건너뛰므로 컴파일 때 막는다.
var _ throttleReader = (*smiSource)(nil)

// NewSMISource 는 SMI 를 초기화하고 Observer 를 만든다.
func NewSMISource() (DeviceSource, error) {
	if err := smi.Init(); err != nil {
		return nil, fmt.Errorf("furiosa-smi 초기화 실패: %w", err)
	}
	obs, err := smi.CreateDefaultObserver()
	if err != nil {
		return nil, fmt.Errorf("observer 생성 실패: %w", err)
	}
	src := &smiSource{observer: obs}
	if di, err := smi.DriverInfo(); err != nil {
		log.Printf("드라이버 버전을 읽지 못했다 — driver_version 라벨이 비어 나간다: %v", err)
	} else {
		src.driverVersion = di.String()
	}
	return src, nil
}

// ThrottleReasons 는 장치마다 쓰로틀 사유 비트를 읽는다. 키는 장치 UUID 다. 읽지 못한 장치는 건너뛴다.
func (s *smiSource) ThrottleReasons() (map[string]uint32, error) {
	devs, err := smi.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("장치 목록 실패: %w", err)
	}
	out := make(map[string]uint32, len(devs))
	for _, d := range devs {
		info, err := d.DeviceInfo()
		if err != nil {
			continue
		}
		tr, err := d.ThrottleReason()
		if err != nil {
			continue
		}
		out[info.UUID()] = uint32(tr)
	}
	return out, nil
}

func (s *smiSource) Devices() ([]DeviceReading, error) {
	devs, err := smi.ListDevices()
	if err != nil {
		return nil, fmt.Errorf("장치 목록 실패: %w", err)
	}
	out := make([]DeviceReading, 0, len(devs))
	for _, d := range devs {
		out = append(out, s.read(d))
	}
	return out, nil
}

// read 는 장치 하나를 읽는다. 축마다 따로 실패할 수 있으므로 치명적인 것
// (DeviceInfo)만 Err 로 올리고 나머지는 그 축만 비운다.
func (s *smiSource) read(d smi.Device) DeviceReading {
	info, err := d.DeviceInfo()
	if err != nil {
		return DeviceReading{Err: fmt.Errorf("DeviceInfo 실패: %w", err)}
	}
	r := DeviceReading{
		Index: info.Index(), Name: info.Name(), Serial: info.Serial(),
		UUID: info.UUID(), BDF: info.BDF(), CoreNum: info.CoreNum(),
		Arch: info.Arch().ToString(), FirmwareVersion: info.FirmwareVersion().String(),
		DriverVersion: s.driverVersion,
	}
	if utils, err := s.observer.GetCoreUtilization(d); err == nil {
		for _, u := range utils {
			r.Cores = append(r.Cores, CoreReading{Core: u.Core(), UsagePercent: u.PeUsagePercentage()})
		}
	}
	if freq, err := d.CoreFrequency(); err == nil {
		for _, pe := range freq.PeFrequency() {
			r.CoreClocks = append(r.CoreClocks, CoreClock{Core: pe.Core(), MHz: pe.Frequency()})
		}
	}
	if pc, err := d.DevicePerformanceCounter(); err == nil {
		for _, c := range pc.PerformanceCounter() {
			r.Cycles = append(r.Cycles, CoreCycle{Core: c.Core(), Total: c.CycleCount(), TaskExec: c.TaskExecutionCycle()})
		}
	}
	if g, err := d.GovernorProfile(); err == nil {
		r.Governor = strings.ToLower(g.String())
	}
	if tr, err := d.ThrottleReason(); err == nil {
		r.ThrottleReason, r.ThrottleValid = uint32(tr), true
	}
	if mem, err := d.MemoryUtilization(); err == nil {
		for _, region := range []smi.Memory{mem.Dram(), mem.DramShared()} {
			for _, b := range region.Memory() {
				r.DramTotalBytes += b.TotalBytes()
				r.DramInUseBytes += b.InUseBytes()
			}
		}
	}
	if p, err := d.PowerConsumption(); err == nil {
		r.PowerWatts = p
	}
	if t, err := d.DeviceTemperature(); err == nil {
		r.TempSocPeak, r.TempAmbient = t.SocPeak(), t.Ambient()
	}
	if alive, err := d.Liveness(); err == nil {
		r.Alive, r.AliveValid = alive, true
	}
	return r
}
