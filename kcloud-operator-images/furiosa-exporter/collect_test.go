// ============================================================
// collect_test.go: 수집 변환 로직 시험(실장치·cgo 없이)
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================
package main

import (
	"fmt"
	"testing"
)

var errBoom = fmt.Errorf("장치 읽기 실패")

type fakeSource struct {
	readings []DeviceReading
	err      error
}

func (f fakeSource) Devices() ([]DeviceReading, error) { return f.readings, f.err }

func TestCollectMapsReadingToSample(t *testing.T) {
	src := fakeSource{readings: []DeviceReading{{
		Index: 0, Name: "npu0", BDF: "0000:27:00.0", CoreNum: 8,
		Cores:          []CoreReading{{Core: 0, UsagePercent: 42.5}, {Core: 1, UsagePercent: 0}},
		DramTotalBytes: 48 << 30, DramInUseBytes: 12 << 30,
		PowerWatts: 91.5, TempSocPeak: 61, TempAmbient: 33, Alive: true,
	}}}

	got, err := Collect(src)
	if err != nil {
		t.Fatalf("Collect 오류: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("샘플 수 = %d, 기대 1", len(got))
	}
	s := got[0]
	if s.PCI != "0000:27:00.0" {
		t.Errorf("PCI = %q, 기대 0000:27:00.0", s.PCI)
	}
	if s.Name != "npu0" {
		t.Errorf("Name = %q, 기대 npu0", s.Name)
	}
	if len(s.Cores) != 2 || s.Cores[0].UsagePercent != 42.5 {
		t.Errorf("Cores = %+v", s.Cores)
	}
	if s.DramInUseBytes != 12<<30 || s.DramTotalBytes != 48<<30 {
		t.Errorf("DRAM = %d/%d", s.DramInUseBytes, s.DramTotalBytes)
	}
}

// 장치 하나가 실패해도 나머지는 살아야 한다. 부분 실패로 전체가 죽으면
// 노드 한 장의 고장이 그 노드 전체 관측을 지운다.
func TestCollectSkipsFailedDeviceKeepsOthers(t *testing.T) {
	src := fakeSource{readings: []DeviceReading{
		{Index: 0, BDF: "0000:27:00.0", Err: errBoom},
		{Index: 1, Name: "npu1", BDF: "0000:2a:00.0", Alive: true},
	}}
	got, err := Collect(src)
	if err != nil {
		t.Fatalf("Collect 오류: %v", err)
	}
	if len(got) != 1 || got[0].Name != "npu1" {
		t.Fatalf("살아남은 샘플 = %+v, 기대 npu1 하나", got)
	}
}

// 공급원 자체가 실패하면 그건 부분 실패가 아니다 — 오류를 그대로 올린다.
func TestCollectPropagatesSourceError(t *testing.T) {
	if _, err := Collect(fakeSource{err: errBoom}); err == nil {
		t.Fatal("공급원 오류가 삼켜졌다")
	}
}

func TestCollectCarriesUUID(t *testing.T) {
	src := fakeSource{readings: []DeviceReading{{
		Index: 0, BDF: "0000:27:00.0", UUID: "UUID-A", Alive: true,
	}}}
	got, err := Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("샘플 1건을 기대했는데 %d건이다", len(got))
	}
	if got[0].UUID != "UUID-A" {
		t.Errorf("UUID=%q, 기대값 %q", got[0].UUID, "UUID-A")
	}
}

func TestCollectCarriesExtendedFields(t *testing.T) {
	src := fakeSource{readings: []DeviceReading{{
		Index: 0, BDF: "0000:27:00.0", UUID: "UUID-A", Alive: true,
		CoreClocks:     []CoreClock{{Core: 0, MHz: 1800}},
		Governor:       "performance",
		ThrottleReason: 2,
		ThrottleValid:  true,
		Arch:           "rngd", FirmwareVersion: "2026.3.0, 2d3f72a", DriverVersion: "2026.3.1, e28c5c0",
		CoreNum: 8, AliveValid: true,
		Cycles: []CoreCycle{{Core: 0, Total: 1000, TaskExec: 400}},
	}}}
	got, err := Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("샘플 1건을 기대했는데 %d건이다", len(got))
	}
	s := got[0]
	if want := []CoreClock{{Core: 0, MHz: 1800}}; len(s.CoreClocks) != len(want) || s.CoreClocks[0] != want[0] {
		t.Errorf("CoreClocks=%+v, 기대값 %+v", s.CoreClocks, want)
	}
	if s.Governor != "performance" {
		t.Errorf("Governor=%q, 기대값 %q", s.Governor, "performance")
	}
	if s.ThrottleReason != 2 {
		t.Errorf("ThrottleReason=%d, 기대값 2", s.ThrottleReason)
	}
	if !s.ThrottleValid {
		t.Error("ThrottleValid=false, 기대값 true")
	}
	if s.Arch != "rngd" || s.FirmwareVersion != "2026.3.0, 2d3f72a" || s.DriverVersion != "2026.3.1, e28c5c0" {
		t.Errorf("Arch/Firmware/Driver = %q/%q/%q", s.Arch, s.FirmwareVersion, s.DriverVersion)
	}
	if s.CoreNum != 8 {
		t.Errorf("CoreNum=%d, 기대값 8", s.CoreNum)
	}
	if !s.AliveValid {
		t.Error("AliveValid=false, 기대값 true")
	}
	if want := []CoreCycle{{Core: 0, Total: 1000, TaskExec: 400}}; len(s.Cycles) != len(want) || s.Cycles[0] != want[0] {
		t.Errorf("Cycles=%+v, 기대값 %+v", s.Cycles, want)
	}
}
