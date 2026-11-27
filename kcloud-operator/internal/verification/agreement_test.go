// ============================================================
// agreement_test.go: 4자 일치 판정 테스트
// 상세: spec·장치·NodeDeviceReport·광고·테스트 Pod 중 하나라도 어긋나면 등급이 붙지 않는다는 규율을
// 축별로 고정한다. 기대 geometry 문자열은 프로덕션 헬퍼로 만든다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"strings"
	"testing"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

const testPCI = "0000:41:00.0"

// agreeingInputs 는 네 축이 전부 일치하는 입력이다(테스트들이 여기서 한 축씩 망가뜨린다).
func agreeingInputs() Inputs {
	geom := nvidia.GeometrySummary("1g.6gb", 4)
	passed := true
	return Inputs{
		Expectation: Expectation{
			Profile: "1g.6gb", CountPerDevice: 4, Geometry: geom,
			Allocatable:   map[string]int32{"nvidia.com/mig-1g.6gb": 4, "nvidia.com/gpu": 1},
			ProbeResource: "nvidia.com/mig-1g.6gb",
		},
		ObservedGeometry:       map[string]string{testPCI: geom},
		ObservationErrors:      map[string]string{},
		ReportGeometry:         map[string]string{testPCI: geom},
		ReportValidationPassed: &passed,
		Allocatable:            map[string]int32{"nvidia.com/mig-1g.6gb": 4, "nvidia.com/gpu": 1},
	}
}

func TestAgreementReachesFunctionallyVerifiedByDefault(t *testing.T) {
	got := Evaluate(agreeingInputs(), DefaultPolicy())
	if !got.Agreed {
		t.Fatalf("네 축이 일치하는데 불일치 판정: %+v", got)
	}
	if got.Level != v1alpha1.EvidenceLevelFunctionallyVerified {
		t.Fatalf("level = %q, want %q", got.Level, v1alpha1.EvidenceLevelFunctionallyVerified)
	}
}

func TestDeviceGeometryMismatchBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	in.ObservedGeometry[testPCI] = nvidia.GeometrySummary("2g.12gb", 2)
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || got.Level != "" {
		t.Fatalf("장치가 spec 과 다른데 통과했다: %+v", got)
	}
	if !checkFailed(got, v1alpha1.EvidenceCheckDeviceObservation) {
		t.Fatalf("실패한 체크가 기록되지 않았다: %+v", got.Checks)
	}
}

func TestObservationErrorBlocksCommit(t *testing.T) {
	// 관측 실패는 "일치했다" 가 아니라 "모른다" 다 — 모르면 통과시키지 않는다.
	in := agreeingInputs()
	in.ObservationErrors[testPCI] = "nvidia-smi failed"
	if got := Evaluate(in, DefaultPolicy()); got.Agreed {
		t.Fatalf("관측 실패인데 통과했다: %+v", got)
	}
}

func TestReportGeometryMismatchBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	in.ReportGeometry[testPCI] = nvidia.GeometrySummary("1g.6gb", 2)
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || !checkFailed(got, v1alpha1.EvidenceCheckNodeDeviceReport) {
		t.Fatalf("NDR 불일치가 통과했다: %+v", got)
	}
}

func TestReportValidationFailureBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	failed := false
	in.ReportValidationPassed = &failed
	if got := Evaluate(in, DefaultPolicy()); got.Agreed {
		t.Fatalf("노드 에이전트 검증이 실패했는데 통과했다: %+v", got)
	}
}

func TestAbsentReportBlocksCommit(t *testing.T) {
	// 검증 플래그도 모르고(nil) 보고서 geometry 도 전혀 없으면 "모르는 축이 통과했다" 가 아니라
	// "보고서가 없다" 다 — node-agent 가 죽었거나 아직 배포되지 않은 노드가 이 축을 공짜로
	// 통과해서는 안 된다(TestUnknownReportValidationDoesNotBlock 과 다른 케이스: 그 테스트는
	// geometry 가 채워져 있고 검증 플래그만 모른다).
	in := agreeingInputs()
	in.ReportGeometry = map[string]string{}
	in.ReportValidationPassed = nil
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || !checkFailed(got, v1alpha1.EvidenceCheckNodeDeviceReport) {
		t.Fatalf("보고서가 전무한데 통과했다: %+v", got)
	}
}

func TestAbsentReportBlocksCommitEvenForShareOnlyPolicy(t *testing.T) {
	// 공유 전용 정책(Geometry=="")도 NDR 자체가 없으면 통과해서는 안 된다 — "geometry 축을
	// 안 본다" 와 "보고서가 아예 없다" 는 다르다. NDR 부재 검사를 Geometry!="" 와 함께 조건부로
	// 걸면 공유 전용 정책에서 이 경로가 빠진다.
	in := agreeingInputs()
	in.Expectation.Geometry = ""
	in.ReportGeometry = map[string]string{}
	in.ReportValidationPassed = nil
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || !checkFailed(got, v1alpha1.EvidenceCheckNodeDeviceReport) {
		t.Fatalf("공유 전용 정책인데 보고서 전무가 통과했다: %+v", got)
	}
}

func TestUnknownReportValidationDoesNotBlock(t *testing.T) {
	// 아직 검증을 한 번도 안 돌린 노드(nil)는 "실패" 가 아니다 — 다른 축이 맞으면 통과시키되,
	// 그 사실을 체크 메시지에 남긴다.
	in := agreeingInputs()
	in.ReportValidationPassed = nil
	got := Evaluate(in, DefaultPolicy())
	if !got.Agreed {
		t.Fatalf("검증 미실행을 실패로 취급했다: %+v", got)
	}
}

func TestAdvertisementShortfallBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	in.Allocatable["nvidia.com/mig-1g.6gb"] = 2
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || !checkFailed(got, v1alpha1.EvidenceCheckAdvertisement) {
		t.Fatalf("광고 부족이 통과했다: %+v", got)
	}
}

func TestAdvertisementSurplusIsAllowed(t *testing.T) {
	// 기대보다 많이 광고하는 것은 부족과 다르다 — 다른 정책이 같은 노드에 자원을 더 얹었을 수
	// 있다. 판정 기준은 "충족" 이지 "동일" 이 아니다(allocatableMet 과 같은 규율).
	in := agreeingInputs()
	in.Allocatable["nvidia.com/mig-1g.6gb"] = 8
	if got := Evaluate(in, DefaultPolicy()); !got.Agreed {
		t.Fatalf("초과 광고를 불일치로 판정했다: %+v", got)
	}
}

func TestVacuousAdvertisementCapsLevelAtObserved(t *testing.T) {
	// RNGD 같은 flat 광고 backend 는 프로파일별 기대 수량이라는 개념이 없어
	// Expectation.Allocatable 이 원천적으로 빈다 — 광고 체크는 켜져 있지만 아무것도 비교하지
	// 않았으므로, 실제로 확인한 적 없는 FunctionallyVerified 를 주장해서는 안 된다.
	in := agreeingInputs()
	in.Expectation.Allocatable = map[string]int32{}
	got := Evaluate(in, DefaultPolicy())
	if !got.Agreed {
		t.Fatalf("광고 기대값이 없다고 불일치로 판정했다: %+v", got)
	}
	if got.Level != v1alpha1.EvidenceLevelObserved {
		t.Fatalf("level = %q, want %q", got.Level, v1alpha1.EvidenceLevelObserved)
	}
}

func TestAdvertisementCheckSkippedWhenPolicyDisablesIt(t *testing.T) {
	in := agreeingInputs()
	in.Allocatable = map[string]int32{}
	p := DefaultPolicy()
	p.Checks = map[string]bool{
		v1alpha1.EvidenceCheckDeviceObservation: true,
		v1alpha1.EvidenceCheckNodeDeviceReport:  true,
	}
	got := Evaluate(in, p)
	if !got.Agreed {
		t.Fatalf("끈 체크가 판정에 끼어들었다: %+v", got)
	}
	if got.Level != v1alpha1.EvidenceLevelObserved {
		t.Fatalf("level = %q, want %q", got.Level, v1alpha1.EvidenceLevelObserved)
	}
}

func TestProbeRaisesLevelToAllocationVerified(t *testing.T) {
	in := agreeingInputs()
	in.ProbeAttempted, in.ProbeAllocated = true, true
	p := DefaultPolicy()
	p.Checks[v1alpha1.EvidenceCheckAllocationProbe] = true
	got := Evaluate(in, p)
	if got.Level != v1alpha1.EvidenceLevelAllocationVerified {
		t.Fatalf("level = %q", got.Level)
	}
}

func TestProbeFailureBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	in.ProbeAttempted, in.ProbeAllocated = true, false
	p := DefaultPolicy()
	p.Checks[v1alpha1.EvidenceCheckAllocationProbe] = true
	if got := Evaluate(in, p); got.Agreed {
		t.Fatalf("프로브 실패가 통과했다: %+v", got)
	}
}

func TestEnabledProbeNotAttemptedBlocksCommit(t *testing.T) {
	// 정책이 프로브를 요구했는데 돌리지 못했으면 "돌렸고 통과" 로 승격하지 않는다.
	in := agreeingInputs()
	in.ProbeAttempted = false
	p := DefaultPolicy()
	p.Checks[v1alpha1.EvidenceCheckAllocationProbe] = true
	if got := Evaluate(in, p); got.Agreed {
		t.Fatalf("요구된 프로브를 건너뛰고 통과했다: %+v", got)
	}
}

func TestNoObservedDevicesBlocksCommit(t *testing.T) {
	in := agreeingInputs()
	in.ObservedGeometry = map[string]string{}
	if got := Evaluate(in, DefaultPolicy()); got.Agreed {
		t.Fatalf("관측된 장치가 없는데 통과했다: %+v", got)
	}
}

func TestChecksAreSortedForStableStatus(t *testing.T) {
	// evidence.status.checks 가 매번 순서를 바꾸면 아무것도 안 바뀐 reconcile 이 status write 가 된다.
	got := Evaluate(agreeingInputs(), DefaultPolicy())
	for i := 1; i < len(got.Checks); i++ {
		prev, cur := got.Checks[i-1], got.Checks[i]
		if prev.Name > cur.Name || (prev.Name == cur.Name && prev.Target > cur.Target) {
			t.Fatalf("checks 정렬이 깨졌다: %+v", got.Checks)
		}
	}
}

func checkFailed(r Result, name string) bool {
	for _, c := range r.Checks {
		if c.Name == name && !c.Passed {
			return true
		}
	}
	return false
}

// TestUnobservableReportDeviceCapsLevel 은 노드 에이전트가 장치를 관측하지 못한 경우를 "불일치" 가
// 아니라 "비교 불가" 로 다루되, 등급을 Observed 위로 올리지 않는지 고정한다. 불일치로 다루면
// 비특권 detector 가 MIG 를 못 보는 클러스터에서 모든 MIG 근거가 영구 실패하고, 그냥 통과시키면
// 보지도 않은 축을 검증했다고 주장하게 된다.
func TestUnobservableReportDeviceCapsLevel(t *testing.T) {
	in := Inputs{
		Expectation: Expectation{
			Geometry: "1g.6gb x4", ProbeResource: "nvidia.com/mig-1g.6gb",
			Allocatable: map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		},
		ObservedGeometry: map[string]string{"0000:18:00.0": "1g.6gb x4"},
		ReportGeometry:   map[string]string{"0000:18:00.0": ""},
		ReportErrors:     map[string]string{"0000:18:00.0": "mode query failed: exit status 9"},
		Allocatable:      map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		ProbeAttempted:   true, ProbeAllocated: true,
	}
	res := Evaluate(in, DefaultPolicy())
	if !res.Agreed {
		t.Fatalf("관측 불가는 불일치가 아니다: %s / %+v", res.Reason, res.Checks)
	}
	if res.Level != v1alpha1.EvidenceLevelObserved {
		t.Fatalf("level = %q, want %q (비교하지 못한 축이 있으면 등급을 올리지 않는다)",
			res.Level, v1alpha1.EvidenceLevelObserved)
	}
}

func TestDescriptorReportCountBelowExpectedBlocks(t *testing.T) {
	// descriptor 요청은 geometry 를 비교하지 않는 대신 보고된 장치 수가 기대 수 이상이어야 한다.
	in := agreeingInputs()
	in.Expectation.Geometry = ""
	in.ReportGeometry = map[string]string{"PCI-0000:af:00.0": ""}
	in.ExpectedDevices = 2
	got := Evaluate(in, DefaultPolicy())
	if got.Agreed || !checkFailed(got, v1alpha1.EvidenceCheckNodeDeviceReport) {
		t.Fatalf("보고 장치가 기대보다 적은데 통과했다: %+v", got)
	}
}

func TestDescriptorReportCountMetPasses(t *testing.T) {
	in := agreeingInputs()
	in.Expectation.Geometry = ""
	in.ReportGeometry = map[string]string{"PCI-0000:af:00.0": ""}
	in.ExpectedDevices = 1
	got := Evaluate(in, DefaultPolicy())
	if !got.Agreed {
		t.Fatalf("보고 장치 수가 충족되는데 실패했다: %+v", got)
	}
}

func TestSkippedReportValidationDoesNotBlock(t *testing.T) {
	// descriptor 가 backend 를 공급하는 요청은 벤더 device-plugin 이 꺼진 상태의 validation 실패를
	// 근거 실패로 삼지 않는다 — 그 사실은 check 메시지에 남는다.
	in := agreeingInputs()
	failed := false
	in.ReportValidationPassed = &failed
	in.ReportValidationSkipped = "descriptor-managed backend"
	got := Evaluate(in, DefaultPolicy())
	if !got.Agreed {
		t.Fatalf("검증 미적용 요청을 실패로 취급했다: %+v", got)
	}
	var msg string
	for _, c := range got.Checks {
		if c.Name == v1alpha1.EvidenceCheckNodeDeviceReport && c.Target == "" {
			msg = c.Message
		}
	}
	if !strings.Contains(msg, "not applied") {
		t.Fatalf("검증 미적용 사실이 check 메시지에 없다: %+v", got.Checks)
	}
}
