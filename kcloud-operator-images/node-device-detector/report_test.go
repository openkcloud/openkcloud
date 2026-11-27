// ============================================================
// report_test.go: NDR 기록 빌더 단위 테스트
// 상세: buildDevices 필드 구성(회귀 0)과 buildValidation additive 매핑 검증.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func TestBuildDevices_Fields(t *testing.T) {
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06", detail: "NVRM 580.65.06"},
	}}
	devs := buildDevices(snap)
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devs))
	}
	d := devs[0]
	// 기존 main 루프와 동일한 키 집합(회귀 0).
	for _, k := range []string{"vendor", "model", "count", "driverLoaded", "driverVersion", "driverVersionDetail", "driverBinding"} {
		if _, ok := d[k]; !ok {
			t.Errorf("device map missing key %q", k)
		}
	}
	if d["count"] != int32(2) {
		t.Errorf("count should be int32(2), got %T %v", d["count"], d["count"])
	}
	if d["vendor"] != "nvidia" || d["driverVersion"] != "580.65.06" {
		t.Errorf("unexpected device field values: %+v", d)
	}
}

// needs-reboot 마커 → NVIDIA device 만 needsReboot=true, 타 벤더/마커부재는 키 생략(회귀 0).
func TestBuildDevices_NeedsReboot(t *testing.T) {
	tmp := t.TempDir()
	old := hostPrefix
	hostPrefix = tmp
	defer func() { hostPrefix = old }()
	if err := os.MkdirAll(filepath.Join(tmp, "var/lib/kcloud-operator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "var/lib/kcloud-operator/needs-reboot"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580"},
		{vendor: "furiosa", model: "warboy", count: 1, loaded: true, ver: "1.9.8-3"},
	}}
	devs := buildDevices(snap)
	if devs[0]["needsReboot"] != true {
		t.Errorf("nvidia device needsReboot=%v, want true(마커 존재)", devs[0]["needsReboot"])
	}
	if _, ok := devs[1]["needsReboot"]; ok {
		t.Error("furiosa device 에 needsReboot 키가 있음(NVIDIA 한정이어야)")
	}

	// 마커 제거 → nvidia 도 키 생략(회귀 0).
	os.Remove(filepath.Join(tmp, "var/lib/kcloud-operator/needs-reboot"))
	if _, ok := buildDevices(snap)[0]["needsReboot"]; ok {
		t.Error("마커 부재인데 needsReboot 키가 있음(바이트 비동등)")
	}
}

// 개명 전 노드는 마커가 아직 /var/lib/npu-operator 에 있다. 설치기가 그 노드를 한 번
// 이관하기 전까지 detector 는 옛 경로도 읽어야 한다. 새 경로가 있으면 그쪽이 우선이다.
func TestBuildDevices_NeedsReboot_LegacyMarkerPath(t *testing.T) {
	tmp := t.TempDir()
	old := hostPrefix
	hostPrefix = tmp
	defer func() { hostPrefix = old }()

	legacy := filepath.Join(tmp, "var/lib/npu-operator")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "needs-reboot"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "580"},
	}}
	if buildDevices(snap)[0]["needsReboot"] != true {
		t.Error("옛 경로 마커를 못 읽음 — 이관 전 노드에서 재부팅 신호가 사라진다")
	}

	// 새 경로에는 마커가 없다 → 옛 경로만 지우면 신호도 사라져야 한다(폴백이 전부).
	os.Remove(filepath.Join(legacy, "needs-reboot"))
	if _, ok := buildDevices(snap)[0]["needsReboot"]; ok {
		t.Error("두 경로 모두 마커가 없는데 needsReboot 키가 있음")
	}
}

// 로드==디스크면 스테일 재부팅 마커를 무시한다. needs-reboot 마커는 install Job 재실행만
// 지워 Idle 에서 로드==디스크여도 래치돼, 스테일 마커 하나가 다음 사이클의 재부팅 예산을
// 소모한다. 디스크 버전과 로드 버전이 같으면 needsReboot 를 싣지 않고, 디스크 미상이면
// 기존대로 마커를 존중한다.
func TestBuildDevices_NeedsRebootSuppressedWhenLoadedEqualsDisk(t *testing.T) {
	tmp := t.TempDir()
	old := hostPrefix
	hostPrefix = tmp
	defer func() { hostPrefix = old }()
	if err := os.MkdirAll(filepath.Join(tmp, "var/lib/kcloud-operator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "var/lib/kcloud-operator/needs-reboot"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	// Case 1: 로드==디스크(ver="580.178.04", onDiskVer="580.178.04") + 마커 존재
	// → needsReboot 키 미생성(마커는 스테일)
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "580.178.04", onDiskVer: "580.178.04"},
	}}
	devs := buildDevices(snap)
	if _, ok := devs[0]["needsReboot"]; ok {
		t.Errorf("loaded==disk 인데 needsReboot 키가 있음: %+v", devs[0])
	}

	// Case 2: 로드!=디스크(ver="595.91.07", onDiskVer="580.178.04") + 마커 존재
	// → needsReboot==true(마커 존중, 진짜 미스매치)
	snap = &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "595.91.07", onDiskVer: "580.178.04"},
	}}
	devs = buildDevices(snap)
	if devs[0]["needsReboot"] != true {
		t.Errorf("loaded!=disk 인데 needsReboot 가 설정 안 됨: got %v, want true", devs[0]["needsReboot"])
	}

	// Case 3: 디스크 미상(onDiskVer="") + 마커 존재 → needsReboot==true(마커 존중, 기존 동작)
	snap = &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "595.91.07", onDiskVer: ""},
	}}
	devs = buildDevices(snap)
	if devs[0]["needsReboot"] != true {
		t.Errorf("onDiskVer 미상인데 needsReboot 가 설정 안 됨: got %v, want true", devs[0]["needsReboot"])
	}

	// Case 4: 양쪽 공백(ver="", onDiskVer="", loaded=false) + 마커 존재 → needsReboot==true
	// 드라이버 미로드 + .run 설치(deb 패키지 없음)의 경우. 공백 가드가 없으면
	// loadedEqualsDisk = ("" == "") → true → needsReboot 를 잘못 억제한다.
	// 이 경우 재부팅이 필요하므로 마커를 반드시 존중해야 한다.
	snap = &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: false, ver: "", onDiskVer: ""},
	}}
	devs = buildDevices(snap)
	if devs[0]["needsReboot"] != true {
		t.Errorf("드라이버 미로드인데 needsReboot 가 설정 안 됨: got %v, want true", devs[0]["needsReboot"])
	}
}

// migLgipOutput 은 NVIDIA device 에만 부착, 값은 Snapshot.MigLgip 그대로.
func TestBuildDevices_MigLgipOutput(t *testing.T) {
	snap := &Snapshot{
		MigLgip: "sample lgip output",
		Devices: []Detected{
			{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "580.65.06"},
			{vendor: "furiosa", model: "warboy", count: 1, loaded: true, ver: "1.9.8-3"},
		},
	}
	devs := buildDevices(snap)
	if devs[0]["migLgipOutput"] != "sample lgip output" {
		t.Errorf("nvidia device migLgipOutput = %v, want snap.MigLgip", devs[0]["migLgipOutput"])
	}
	if _, ok := devs[1]["migLgipOutput"]; ok {
		t.Error("furiosa device 에 migLgipOutput 키가 있음(NVIDIA 한정이어야)")
	}
}

// snap.PciAddrs["nvidia"] 가 있으면 nvidia device 는 PCI 개수만큼 fan-out 되고, 각 entry 에
// pcieAddress + per-PCI MIG 필드(fail-closed observeMig 결과)가 붙는다.
func TestBuildDevices_NvidiaPerPciFanOut(t *testing.T) {
	snap := &Snapshot{
		MigLgip:  "global fallback output(사용 안 됨)",
		PciAddrs: map[string][]string{"nvidia": {"0000:01:00.0", "0000:65:00.0"}},
		Devices: []Detected{
			{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"},
			{vendor: "furiosa", model: "warboy", count: 1, loaded: true, ver: "1.9.8-3"},
		},
	}
	devs := buildDevices(snap)
	if len(devs) != 3 {
		t.Fatalf("expected 3 device entries(2 nvidia PCI + 1 furiosa), got %d: %+v", len(devs), devs)
	}
	gotPci := map[string]bool{}
	for _, d := range devs[:2] {
		if d["vendor"] != "nvidia" {
			t.Fatalf("expected nvidia entries first, got %+v", d)
		}
		pci, _ := d["pcieAddress"].(string)
		if pci == "" {
			t.Errorf("nvidia fan-out entry missing pcieAddress: %+v", d)
		}
		gotPci[pci] = true
		// nvidia-smi 부재(CI) → fail-closed Unknown+Err, 전역 MigLgip 는 쓰지 않음(빈 문자열).
		if d["migModeCurrent"] != "Unknown" || d["migObservationError"] == "" {
			t.Errorf("expected fail-closed Unknown+Err per-PCI, got %+v", d)
		}
		if d["migLgipOutput"] == snap.MigLgip {
			t.Error("per-PCI fan-out 이 전역 MigLgip 를 그대로 복붙함(더 이상 허용 안 됨)")
		}
		// FIX 1: fan-out entry 는 물리 GPU 1개씩 → count 는 항상 1(집계 d.count=2 복붙 금지).
		if d["count"] != int32(1) {
			t.Errorf("fan-out entry count = %v, want int32(1)(집계 count 복붙 금지)", d["count"])
		}
	}
	if !gotPci["0000:01:00.0"] || !gotPci["0000:65:00.0"] {
		t.Errorf("missing expected PCI addresses in fan-out: %+v", gotPci)
	}
	if devs[2]["vendor"] != "furiosa" {
		t.Errorf("furiosa entry should be untouched by nvidia fan-out: %+v", devs[2])
	}
}

// FIX 1: snap.PciAddrs["nvidia"] 가 비어있으면(폴백) 단일 집계 entry 는 d.count 를 그대로 유지한다
// (fan-out count=1 강제와 별개 — 폴백은 기존 집계 시맨틱 그대로, 회귀 0).
func TestBuildDevices_NvidiaFallbackKeepsAggregateCount(t *testing.T) {
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06"},
	}}
	devs := buildDevices(snap)
	if len(devs) != 1 {
		t.Fatalf("expected 1 fallback aggregate entry, got %d", len(devs))
	}
	if devs[0]["count"] != int32(2) {
		t.Errorf("fallback entry count = %v, want int32(2)(집계 유지)", devs[0]["count"])
	}
}

// 비-NVIDIA 벤더도 PCI 주소가 있으면 카드 1장당 entry 로 갈라진다. 갈라지지 않으면 소비자
// (rngd backend deviceID)가 한 노드의 카드를 전부 같은 ID 로 뭉개고, intent.ApplyHealth 가
// 고장 카드 1장을 개별로 뺄 수 없다 — 실제로 있었던 구멍이다.
// MIG 필드는 NVIDIA 전용이므로 여기 붙으면 안 된다.
func TestBuildDevices_NonNvidiaPerPciFanOut(t *testing.T) {
	snap := &Snapshot{
		PciAddrs: map[string][]string{"rngd": {"0000:27:00.0", "0000:5e:00.0"}},
		Devices: []Detected{
			{vendor: "furiosa", model: "rngd", count: 2, loaded: true, ver: "2025.2.0"},
		},
	}
	devs := buildDevices(snap)
	if len(devs) != 2 {
		t.Fatalf("카드 2장 → entry 2개여야 한다, got %d: %+v", len(devs), devs)
	}
	for i, want := range []string{"0000:27:00.0", "0000:5e:00.0"} {
		if devs[i]["pcieAddress"] != want {
			t.Errorf("devs[%d] pcieAddress = %v, want %q", i, devs[i]["pcieAddress"], want)
		}
		// 집계 count(2)를 entry 마다 복붙하면 소비자의 합산이 4로 부푼다.
		if devs[i]["count"] != int32(1) {
			t.Errorf("devs[%d] count = %v, want int32(1)", i, devs[i]["count"])
		}
		for _, k := range migCarryFields {
			if _, ok := devs[i][k]; ok {
				t.Errorf("devs[%d] 에 NVIDIA 전용 MIG 키 %q 가 붙었다", i, k)
			}
		}
	}
}

// 주소를 못 찾은 벤더(sysfs 부재 등)는 기존 단일 집계 entry 그대로다 — pcieAddress 키를
// 만들지 않고 count 도 집계값을 유지한다(회귀 0).
func TestBuildDevices_NonNvidiaNoPciKeepsAggregate(t *testing.T) {
	snap := &Snapshot{Devices: []Detected{
		{vendor: "tenstorrent", model: "blackhole-p150", count: 3, loaded: true},
	}}
	devs := buildDevices(snap)
	if len(devs) != 1 {
		t.Fatalf("expected 1 aggregate entry, got %d", len(devs))
	}
	if _, ok := devs[0]["pcieAddress"]; ok {
		t.Errorf("주소가 없는데 pcieAddress 키가 생겼다: %+v", devs[0])
	}
	if devs[0]["count"] != int32(3) {
		t.Errorf("count = %v, want int32(3)", devs[0]["count"])
	}
}

// 회귀 0: NVIDIA fan-out entry 의 키 집합·값이 그대로인지 못박는다. 비-NVIDIA fan-out 을
// 얹으면서 MIG 부착을 공통 경로로 밀어 올렸다면 여기가 깨진다.
func TestBuildDevices_NvidiaEntryShapeUnchanged(t *testing.T) {
	// driverBinding 은 실 sysfs 를 훑으므로(deviceDriverBinding→bindingSummary) 가짜 트리를
	// 깔아 둔다. 안 그러면 빌드 호스트에 GPU 가 있느냐로 결과가 갈린다(실제로 이 저장소를
	// 빌드하는 k8s-master 에는 GPU 가 있어 통과하고, 없는 CI 에서는 "none" 이 나온다).
	root := fakeSysfsPci(t)
	fakePciDev(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200")

	snap := &Snapshot{
		PciAddrs: map[string][]string{"nvidia": {"0000:18:00.0"}},
		Devices: []Detected{
			{vendor: "nvidia", model: "generic", count: 2, loaded: true, ver: "580.65.06", detail: "NVRM 580.65.06"},
		},
	}
	devs := buildDevices(snap)
	if len(devs) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(devs))
	}
	want := map[string]any{
		// model 은 픽스처(0x10de:0x20b7 = A30) 의 PCI 판정 결과다. 이 픽스처는 원래
		// driverBinding 을 결정적으로 만들려고 깔았는데, 이제 제품명도 여기서 나온다.
		"vendor": "nvidia", "model": "a30", "count": int32(1),
		"driverLoaded": true, "driverVersion": "580.65.06", "driverVersionDetail": "NVRM 580.65.06",
		"driverBinding": "nvidia", "pcieAddress": "0000:18:00.0",
		"migModeCurrent": "Unknown", "migModePending": "Unknown",
		"migCurrentGeometry": "", "migLgipOutput": "",
	}
	got := devs[0]
	// migObservationError 는 nvidia-smi 부재(CI) 메시지라 문구를 못박지 않고 존재만 본다.
	errStr, ok := got["migObservationError"].(string)
	if !ok || errStr == "" {
		t.Errorf("migObservationError 가 비었다(fail-closed 여야 함): %+v", got)
	}
	if len(got) != len(want)+1 {
		t.Errorf("키 집합이 달라졌다: got %v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v(%T), want %v(%T)", k, got[k], got[k], w, w)
		}
	}
}

// 회귀 방어: PCI 로 제품명을 알아낼 수 없으면 model 은 detect() 가 준 "generic" 그대로다. 이
// 폴백이 깨지면 소비자(kcloud-operator partition/nvidia backend)의 fail-closed 판정이 무너진다.
// 주소가 없는 집계 폴백과, 주소는 있는데 sysfs 를 못 읽는 경우를 둘 다 본다. 가짜 sysfs 루트를
// 반드시 깔아야 한다 — 안 그러면 이 저장소를 빌드하는 호스트의 실제 GPU 를 읽어 통과 여부가
// 호스트마다 갈린다(ShapeUnchanged 와 같은 함정).
func TestBuildDevices_ModelStaysGenericWithoutPCI(t *testing.T) {
	fakeSysfsPci(t) // 빈 트리: 어떤 주소도 vendor/device 를 못 읽는다

	noAddr := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 2, loaded: true},
	}}
	if got := buildDevices(noAddr)[0]["model"]; got != "generic" {
		t.Errorf("주소 없는 집계 entry model = %v, want \"generic\"", got)
	}

	unreadable := &Snapshot{
		PciAddrs: map[string][]string{"nvidia": {"0000:18:00.0"}},
		Devices: []Detected{
			{vendor: "nvidia", model: "generic", count: 1, loaded: true},
		},
	}
	if got := buildDevices(unreadable)[0]["model"]; got != "generic" {
		t.Errorf("sysfs 를 못 읽었는데 model = %v, want \"generic\"(폴백 유지)", got)
	}
}

// fan-out entry 마다 자기 PCI 주소의 장치 ID 로 제품명이 갈린다. 한 노드에 서로 다른 GPU 가
// 꽂혀 있어도 두 entry 가 같은 이름을 받으면 안 된다 — 벤더 단위 집계로 돌아간 것이다.
//
// 픽스처는 k8s-worker1 실측이다: 0000:18:00.0 = 0x10de:0x20b7 A30,
// 0000:86:00.0 = 0x10de:0x25b6 A2.
func TestBuildDevices_ModelFromPCI(t *testing.T) {
	root := fakeSysfsPci(t)
	fakePciDev(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200")
	fakePciDev(t, root, "0000:86:00.0", "0x10de", "0x25b6", "0x030200")

	snap := &Snapshot{
		PciAddrs: map[string][]string{"nvidia": {"0000:18:00.0", "0000:86:00.0"}},
		Devices: []Detected{
			{vendor: "nvidia", model: "generic", count: 2, loaded: true},
		},
	}
	devs := buildDevices(snap)
	if len(devs) != 2 {
		t.Fatalf("카드 2장 → entry 2개여야 한다, got %d: %+v", len(devs), devs)
	}
	for i, want := range []string{"a30", "a2"} {
		if devs[i]["model"] != want {
			t.Errorf("devs[%d](%v) model = %v, want %q",
				i, devs[i]["pcieAddress"], devs[i]["model"], want)
		}
	}
}

// 이미 구체적인 model 이 붙어 있으면 PCI 판정이 덮지 않는다. 표가 틀렸을 때 detect() 가
// 제대로 알아낸 이름을 잃는 쪽이 더 나쁘다.
func TestBuildDevices_ModelFromPCIDoesNotOverrideKnownModel(t *testing.T) {
	root := fakeSysfsPci(t)
	fakePciDev(t, root, "0000:18:00.0", "0x10de", "0x20b7", "0x030200")

	snap := &Snapshot{
		PciAddrs: map[string][]string{"nvidia": {"0000:18:00.0"}},
		Devices: []Detected{
			{vendor: "nvidia", model: "h100", count: 1, loaded: true},
		},
	}
	if got := buildDevices(snap)[0]["model"]; got != "h100" {
		t.Errorf("PCI 판정이 이미 알아낸 model 을 덮었다: got %v, want \"h100\"", got)
	}
}

func TestBuildValidation_Nil(t *testing.T) {
	if got := buildValidation(nil); got != nil {
		t.Errorf("buildValidation(nil) should be nil, got %+v", got)
	}
}

func TestBuildValidation_Map(t *testing.T) {
	v := &ValidationResult{
		Passed:      true,
		LastRunTime: time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC),
		Vendor:      "nvidia",
		Steps: []ValidationStep{
			{Name: "driverModule", Passed: true, Message: "driver loaded"},
			{Name: "devicePlugin", Passed: true},
		},
	}
	m := buildValidation(v)
	if m["passed"] != true {
		t.Errorf("passed = %v, want true", m["passed"])
	}
	if m["lastRunTime"] != "2026-07-16T10:00:00Z" {
		t.Errorf("lastRunTime = %v", m["lastRunTime"])
	}
	steps, ok := m["steps"].([]map[string]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("steps wrong: %T %v", m["steps"], m["steps"])
	}
	// message 없는 step 은 message 키를 생략(omitempty 시맨틱).
	if _, ok := steps[1]["message"]; ok {
		t.Errorf("step without message should omit message key: %+v", steps[1])
	}
	if steps[0]["message"] != "driver loaded" {
		t.Errorf("step0 message = %v", steps[0]["message"])
	}
	// notApplicable=false(zero value) 는 키 자체를 생략한다(devicePlugin, 위 픽스처).
	if _, ok := steps[1]["notApplicable"]; ok {
		t.Errorf("notApplicable=false 인 step 은 그 키를 생략해야 한다: %+v", steps[1])
	}
}

// TestBuildValidation_NotApplicableStep 은 이 함수가 ValidationStep.NotApplicable 을
// map 으로 실어 나르는지 본다 — Report()/buildValidation 은 unstructured map 을 손으로
// 만들므로 struct 에 필드를 추가한 것만으로는 NDR 에 실리지 않는다. notApplicable 이 실제로
// 이렇게 빠진 적이 있다(필드에는 있어도 NDR JSON 에는 빠질 수 있다).
func TestBuildValidation_NotApplicableStep(t *testing.T) {
	v := &ValidationResult{
		Passed: true, LastRunTime: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), Vendor: "nvidia",
		Steps: []ValidationStep{
			{Name: "devicePlugin", Passed: true, NotApplicable: true, Message: excludedMessage("control-plane")},
		},
	}
	steps := buildValidation(v)["steps"].([]map[string]any)
	if steps[0]["notApplicable"] != true {
		t.Errorf("notApplicable 이 map 에 실리지 않았다: %+v", steps[0])
	}
}

// TestPreserveObservedMig 는 detector 가 관측하지 못한 MIG 값을 기존 보고서에서 물려받는지 본다.
//
// detector 는 비특권이라 /dev/nvidia* 가 없어 MIG 를 못 본다(호스트 nvidia-smi 는 실행되지만
// GPU 를 열지 못해 exit 9). 매 주기 "관측 실패" 를 덮어쓰면 operator 가 특권 Job 으로 채운
// 값이 30초마다 지워진다 — label.go 의 같은 사고와 같은 종류다.
// 깨는 뮤테이션: Report 의 preserveObservedMig 호출을 지우면 관측값이 사라져 실패한다.
func TestPreserveObservedMig(t *testing.T) {
	fresh := []map[string]any{
		{"pcieAddress": "0000:41:00.0", "migModeCurrent": "Unknown", "migModePending": "Unknown",
			"migObservationError": "mode query failed: exit status 9"},
		{"pcieAddress": "0000:81:00.0", "migModeCurrent": "Unknown",
			"migObservationError": "mode query failed: exit status 9"},
	}
	old := []any{
		map[string]any{"pcieAddress": "0000:41:00.0", "migModeCurrent": "Enabled",
			"migModePending": "Enabled", "migCurrentGeometry": "1g.6gb x4", "migLgipOutput": "raw"},
		// 저쪽도 관측 못 한 항목은 물려줄 것이 없다.
		map[string]any{"pcieAddress": "0000:81:00.0", "migModeCurrent": "Unknown",
			"migObservationError": "이전 주기도 실패"},
	}
	preserveObservedMig(fresh, old)

	if fresh[0]["migModeCurrent"] != "Enabled" || fresh[0]["migCurrentGeometry"] != "1g.6gb x4" {
		t.Fatalf("관측된 MIG 값을 물려받지 못했다: %+v", fresh[0])
	}
	if _, has := fresh[0]["migObservationError"]; has {
		t.Fatalf("물려받았으면 관측 실패 표시는 남으면 안 된다: %+v", fresh[0])
	}
	if fresh[1]["migObservationError"] == nil {
		t.Fatalf("물려받을 값이 없으면 관측 실패를 그대로 둬야 한다: %+v", fresh[1])
	}
}

// TestPreserveObservedMigDoesNotOverrideFreshObservation 은 detector 가 실제로 관측했을 때는
// 새 값이 이기는지 본다 — 물려받기 규칙이 "우리가 실패했을 때만" 이어야 한다.
func TestPreserveObservedMigDoesNotOverrideFreshObservation(t *testing.T) {
	fresh := []map[string]any{
		{"pcieAddress": "0000:41:00.0", "migModeCurrent": "Disabled", "migCurrentGeometry": "disabled"},
	}
	old := []any{
		map[string]any{"pcieAddress": "0000:41:00.0", "migModeCurrent": "Enabled",
			"migCurrentGeometry": "1g.6gb x4"},
	}
	preserveObservedMig(fresh, old)
	if fresh[0]["migModeCurrent"] != "Disabled" {
		t.Fatalf("직접 관측한 값이 옛 값에 덮였다: %+v", fresh[0])
	}
}

// TestReportPreservesObservedMig 는 **Report 가 실제로** 물려받기를 부르는지 본다.
// 헬퍼만 시험하면 호출부를 지워도 통과한다 — 실제로 그렇게 속은 적이 있다.
func TestReportPreservesObservedMig(t *testing.T) {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(ndrGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(ndrGVK.GroupVersion().WithKind("NodeDeviceReportList"), &unstructured.UnstructuredList{})

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(ndrGVK)
	existing.SetName("n1")
	existing.Object["spec"] = map[string]any{"nodeName": "n1"}
	existing.Object["status"] = map[string]any{"devices": []any{
		map[string]any{"pcieAddress": "0000:41:00.0", "migModeCurrent": "Enabled",
			"migCurrentGeometry": "1g.6gb x4"},
	}}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	snap := &Snapshot{Node: "n1", PciAddrs: map[string][]string{"nvidia": {"0000:41:00.0"}},
		Devices: []Detected{{vendor: "nvidia", model: "A30", count: 1, loaded: true}}}
	if err := Report(context.Background(), c, snap); err != nil {
		t.Fatal(err)
	}

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(ndrGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: "n1"}, got); err != nil {
		t.Fatal(err)
	}
	devs, _ := got.Object["status"].(map[string]any)["devices"].([]any)
	if len(devs) != 1 {
		t.Fatalf("device 항목 수가 다르다: %+v", devs)
	}
	d := devs[0].(map[string]any)
	if d["migModeCurrent"] != "Enabled" || d["migCurrentGeometry"] != "1g.6gb x4" {
		t.Fatalf("Report 가 관측값을 덮어썼다(호출부 미연결): %+v", d)
	}
}

// TestReportReplacesConsumersEachCycle 은 Report 가 매 주기 consumers/consumersError 를
// 배타로 교체하는지 — 그리고 attachConsumers 호출부 자체가 실제로 연결돼 있는지 — 못박는다.
// 헬퍼만 시험하면 호출부를 지워도 통과한다(TestReportPreservesObservedMig 의 주석과 같은 함정).
func TestReportReplacesConsumersEachCycle(t *testing.T) {
	dir := t.TempDir()
	orig := podResourcesAllocationsPath
	podResourcesAllocationsPath = filepath.Join(dir, "allocations.json")
	t.Cleanup(func() { podResourcesAllocationsPath = orig })

	s := runtime.NewScheme()
	s.AddKnownTypeWithName(ndrGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(ndrGVK.GroupVersion().WithKind("NodeDeviceReportList"), &unstructured.UnstructuredList{})
	c := fake.NewClientBuilder().WithScheme(s).Build()
	snap := &Snapshot{Node: "n1"}

	status := func() map[string]any {
		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(ndrGVK)
		if err := c.Get(context.Background(), client.ObjectKey{Name: "n1"}, got); err != nil {
			t.Fatal(err)
		}
		st, _ := got.Object["status"].(map[string]any)
		return st
	}

	// 1) create 경로, 파일 없음: consumers·consumersObservedAt 키 없음, consumersError 비어 있지 않음.
	if err := Report(context.Background(), c, snap); err != nil {
		t.Fatal(err)
	}
	st := status()
	if _, ok := st["consumers"]; ok {
		t.Fatalf("1) 파일이 없는데 consumers 키가 있다: %+v", st)
	}
	if _, ok := st["consumersObservedAt"]; ok {
		t.Fatalf("1) 파일이 없는데 consumersObservedAt 키가 있다: %+v", st)
	}
	if errText, _ := st["consumersError"].(string); errText == "" {
		t.Fatalf("1) consumersError 가 비어 있다: %+v", st)
	}

	// 2) update 경로, 신선한 파일(allocation 하나, deviceIDs ["d1"]): consumers 길이 1·deviceID "d1",
	//    consumersObservedAt 있음, consumersError 키 없음.
	fresh := `{"observedAt":"` + time.Now().UTC().Format(time.RFC3339) +
		`","allocations":[{"namespace":"a","pod":"p","container":"c","resourceName":"r","deviceIDs":["d1"]}]}`
	if err := os.WriteFile(podResourcesAllocationsPath, []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Report(context.Background(), c, snap); err != nil {
		t.Fatal(err)
	}
	st = status()
	consumers, ok := st["consumers"].([]any)
	if !ok || len(consumers) != 1 || consumers[0].(map[string]any)["deviceID"] != "d1" {
		t.Fatalf("2) consumers 가 deviceID d1 하나여야 한다: %+v", st)
	}
	if _, ok := st["consumersObservedAt"]; !ok {
		t.Fatalf("2) consumersObservedAt 키가 없다: %+v", st)
	}
	if _, ok := st["consumersError"]; ok {
		t.Fatalf("2) 성공 주기인데 consumersError 가 남아 있다: %+v", st)
	}

	// 3) update 경로, 파일 삭제: consumers·consumersObservedAt 키 없음(지워짐), consumersError 다시 채워짐.
	if err := os.Remove(podResourcesAllocationsPath); err != nil {
		t.Fatal(err)
	}
	if err := Report(context.Background(), c, snap); err != nil {
		t.Fatal(err)
	}
	st = status()
	if _, ok := st["consumers"]; ok {
		t.Fatalf("3) 실패 주기인데 이전 consumers 가 남았다: %+v", st)
	}
	if _, ok := st["consumersObservedAt"]; ok {
		t.Fatalf("3) 실패 주기인데 이전 consumersObservedAt 가 남았다: %+v", st)
	}
	if errText, _ := st["consumersError"].(string); errText == "" {
		t.Fatalf("3) consumersError 가 다시 채워지지 않았다: %+v", st)
	}

	// 4) update 경로, 신선한 파일이지만 allocations 가 빈 배열: consumers == []any{}(키 있음·길이 0),
	//    consumersError 키 없음.
	empty := `{"observedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","allocations":[]}`
	if err := os.WriteFile(podResourcesAllocationsPath, []byte(empty), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Report(context.Background(), c, snap); err != nil {
		t.Fatal(err)
	}
	st = status()
	consumers, ok = st["consumers"].([]any)
	if !ok || len(consumers) != 0 {
		t.Fatalf("4) 할당 0건은 빈 목록(키는 있어야 한다): %+v", st)
	}
	if _, ok := st["consumersError"]; ok {
		t.Fatalf("4) 할당 0건인데 consumersError 키가 있다: %+v", st)
	}
}

// TestBuildDevices_OnDiskVersion: NVIDIA onDiskVer 는 NDR 에 방출되고, 다른 벤더는 키를 만들지 않는다.
func TestBuildDevices_OnDiskVersion(t *testing.T) {
	snap := &Snapshot{Devices: []Detected{
		{vendor: "nvidia", model: "generic", count: 1, loaded: true, ver: "595.91.07", detail: "NVRM 595.91.07", onDiskVer: "580.178.04"},
		{vendor: "furiosa", model: "warboy", count: 1, loaded: true, ver: "1.9.8-3", detail: "driver=1.9.8-3", onDiskVer: ""},
	}}
	devs := buildDevices(snap)
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}

	// NVIDIA onDiskVer 방출
	if devs[0]["vendor"] != "nvidia" {
		t.Fatalf("expected nvidia first, got %+v", devs[0])
	}
	if devs[0]["driverVersionOnDisk"] != "580.178.04" {
		t.Errorf("nvidia onDiskVer = %v, want 580.178.04", devs[0]["driverVersionOnDisk"])
	}

	// Furiosa 는 키 없음(값이 빈 문자열이므로 조건부 생략)
	if _, ok := devs[1]["driverVersionOnDisk"]; ok {
		t.Errorf("furiosa 에 driverVersionOnDisk 키가 있음(NVIDIA 한정이어야): %+v", devs[1])
	}
}

// TestHandCopiedStructFieldCounts 는 손복사 매핑의 원본 구조체 필드 수를 못박는다.
//
// buildValidation 은 ValidationStep 4필드를, buildDevices 는 Detected 7필드를 손으로 map 에
// 옮긴다. 필드를 늘리고 매핑을 안 고치면 그 값은 NDR 에 실리지 않는다 — NotApplicable 이
// 실제로 그렇게 빠진 적이 있다. 이 시험은 필드 수가 바뀌는 순간 빨개져 매핑을 함께 고치게 한다.
func TestHandCopiedStructFieldCounts(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		want int
		fix  string
	}{
		{reflect.TypeOf(ValidationStep{}), 4, "buildValidation(report.go)"},
		{reflect.TypeOf(Detected{}), 7, "buildDevices 의 newBase(report.go)"},
	} {
		if got := tc.typ.NumField(); got != tc.want {
			t.Errorf("%s 필드 수가 %d → %d 로 바뀌었다. 필드를 추가했으면 %s 도 고쳐라 "+
				"(매핑에 안 넣으면 그 값은 NDR 에 실리지 않는다). 매핑을 고쳤으면 이 기대값을 %d 로 갱신하라.",
				tc.typ.Name(), tc.want, got, tc.fix, got)
		}
	}
}
