// ============================================================
// agreement.go: spec·장치·NodeDeviceReport·광고·테스트 Pod 4자 일치 판정
// 상세: 클러스터를 읽지 않는 순수 판정. 하나라도 어긋나면 등급을 주지 않는다 — "일부는 봤다" 를 성공으로
// 승격하는 순간 commit oracle 이 무의미해진다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"fmt"
	"sort"
	"strings"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// Expectation 은 spec 에서 유도한 기대값이다(4자 일치의 기준축).
type Expectation struct {
	Profile        string
	CountPerDevice int32
	// Geometry 는 nvidia.GeometrySummary 로 만든 목표 문자열이다(비면 geometry 축을 보지 않는다 —
	// 공유 전용 정책처럼 파티션 레이아웃이 없는 요청이 여기 해당한다).
	Geometry      string
	Allocatable   map[string]int32
	ProbeResource string
}

// Inputs 는 판정에 필요한 관측값 전부다. 수집은 호출자(Verifier)가 하고 여기서는 비교만 한다.
type Inputs struct {
	Expectation       Expectation
	ObservedGeometry  map[string]string // PCI → 실제 장치에서 읽은 geometry 요약
	ObservationErrors map[string]string // PCI → 관측 실패 사유(비어 있으면 성공)
	ReportGeometry    map[string]string // PCI → NodeDeviceReport 가 보고한 geometry
	// ReportErrors 는 node-agent 가 그 장치를 관측하지 못한 사유다(PCI → 사유). "못 봤다" 는
	// "다르다" 가 아니다 — 이 축은 비교 불가로 기록하고 등급을 Observed 로 묶는다.
	ReportErrors map[string]string
	// ReportValidationPassed 는 node-agent 5단계 Validation 결과다. nil = 아직 안 돌렸다(모름).
	ReportValidationPassed *bool
	// ExpectedDevices 는 descriptor 요청이 resolve 한 장치 수다. 0 이 아니면 ReportGeometry 가 이
	// 수 이상이어야 NDR check 가 통과한다(geometry 를 관측하지 않는 descriptor 의 대조 기준).
	ExpectedDevices int
	// ReportValidationSkipped 가 비어 있지 않으면 node-agent validation 을 판정에 쓰지 않은 사유다
	// (descriptor 가 backend 를 직접 공급하는 요청). 사실은 check 메시지로만 남긴다.
	ReportValidationSkipped string
	Allocatable             map[string]int32
	ProbeAttempted          bool
	ProbeAllocated          bool
	ProbeMessage            string
}

// Result 는 판정 결과다. Agreed=false 면 Level 은 빈 문자열이다.
type Result struct {
	Level  string
	Agreed bool
	Checks []v1alpha1.EvidenceCheck
	Reason string
}

// Evaluate 는 활성화된 체크만 돌려 등급을 매긴다.
//
// 등급은 누적이다: 장치+NDR 이 맞으면 Observed, 거기에 광고가 맞으면 FunctionallyVerified,
// 거기에 테스트 Pod 까지 붙으면 AllocationVerified. 어느 활성 체크든 실패하면 등급 없이
// Agreed=false 다 — 부분 성공에 등급을 주면 Coordinator 가 그 등급으로 commit 해 버린다.
func Evaluate(in Inputs, p EffectivePolicy) Result {
	var res Result
	var failed []string

	record := func(name, target string, passed bool, msg string) {
		res.Checks = append(res.Checks, v1alpha1.EvidenceCheck{
			Name: name, Target: target, Passed: passed, Message: msg,
		})
		if !passed {
			failed = append(failed, name)
		}
	}

	if p.Enabled(v1alpha1.EvidenceCheckDeviceObservation) {
		evalDeviceObservation(in, record)
	}
	reportComparable := true
	if p.Enabled(v1alpha1.EvidenceCheckNodeDeviceReport) {
		reportComparable = evalNodeDeviceReport(in, record)
	}
	advertisementApplicable := true
	if p.Enabled(v1alpha1.EvidenceCheckAdvertisement) {
		advertisementApplicable = evalAdvertisement(in, record)
	}
	if p.Enabled(v1alpha1.EvidenceCheckAllocationProbe) {
		switch {
		case !in.ProbeAttempted:
			record(v1alpha1.EvidenceCheckAllocationProbe, "", false, "probe required by policy but not attempted")
		default:
			record(v1alpha1.EvidenceCheckAllocationProbe, in.Expectation.ProbeResource, in.ProbeAllocated, in.ProbeMessage)
		}
	}

	sortChecks(res.Checks)
	if len(failed) > 0 {
		sort.Strings(failed)
		res.Reason = "checks failed: " + strings.Join(dedupe(failed), ", ")
		return res
	}
	res.Agreed = true
	res.Level = levelFor(p, advertisementApplicable && reportComparable)
	return res
}

// evalDeviceObservation 은 실제 장치가 spec 이 요구한 geometry 인지 본다.
func evalDeviceObservation(in Inputs, record func(name, target string, passed bool, msg string)) {
	const name = v1alpha1.EvidenceCheckDeviceObservation
	if len(in.ObservedGeometry) == 0 && len(in.ObservationErrors) == 0 {
		record(name, "", false, "no device observation available")
		return
	}
	for _, pci := range sortedKeys(in.ObservedGeometry, in.ObservationErrors) {
		if errMsg := in.ObservationErrors[pci]; errMsg != "" {
			record(name, pci, false, "observation failed: "+errMsg)
			continue
		}
		got, ok := in.ObservedGeometry[pci]
		if !ok {
			record(name, pci, false, "no geometry observed")
			continue
		}
		if in.Expectation.Geometry == "" {
			record(name, pci, true, "geometry not constrained by spec")
			continue
		}
		if got != in.Expectation.Geometry {
			record(name, pci, false, fmt.Sprintf("device geometry %q != expected %q", got, in.Expectation.Geometry))
			continue
		}
		record(name, pci, true, "")
	}
}

// evalNodeDeviceReport 는 노드 에이전트 보고가 장치 관측과 어긋나지 않는지 본다.
// 반환값은 "실제로 비교했는가" 다 — 노드 에이전트가 그 장치를 관측조차 못 했으면 비교한 적이
// 없으므로 등급을 Observed 위로 올리지 않는다(evalAdvertisement 와 같은 규율).
func evalNodeDeviceReport(in Inputs, record func(name, target string, passed bool, msg string)) bool {
	const name = v1alpha1.EvidenceCheckNodeDeviceReport
	if in.ReportValidationSkipped != "" {
		// descriptor 가 backend 를 직접 공급하는 요청이다 — 벤더 device-plugin 검증은 그 플러그인이
		// 꺼진 상태를 정상으로 다루지 못해 판정에 못 쓰지만, 그 사실은 check message 로 남긴다.
		record(name, "", true, "node agent validation not applied: "+in.ReportValidationSkipped)
	} else if in.ReportValidationPassed != nil && !*in.ReportValidationPassed {
		record(name, "", false, "node agent validation reported a failure")
		return false
	}
	// NDR 자체가 없거나 장치를 하나도 보고하지 않으면 "일치했다" 가 아니라 "보고서가 없다" 다 —
	// evalDeviceObservation 의 관측 부재 가드와 같은 규율(node-agent 미배포·NDR 삭제 상태를
	// 통과로 승격하지 않는다). geometry 기대 여부와 무관하다 — 공유 전용 정책(Geometry=="")도
	// NDR 존재 자체는 증명해야 한다.
	if len(in.ReportGeometry) == 0 {
		record(name, "", false, "no node device report geometry available")
		return false
	}
	if in.ReportValidationPassed == nil && in.ReportValidationSkipped == "" {
		record(name, "", true, "node agent validation has not run yet")
	}
	// ExpectedDevices 는 descriptor 요청의 대조 기준이다 — descriptor 는 per-PCI geometry 를
	// 관측하지 않으므로(Expectation.Geometry=="") 아래 geometry 비교 루프 대신 보고된 장치 수만 본다.
	if in.ExpectedDevices > 0 && len(in.ReportGeometry) < in.ExpectedDevices {
		record(name, "", false, fmt.Sprintf("report has %d matching devices, expected at least %d",
			len(in.ReportGeometry), in.ExpectedDevices))
		return false
	}
	if in.Expectation.Geometry == "" {
		// 공유 전용 정책처럼 비교할 geometry 가 없다는 사실 자체를 남긴다 — 안 남기면 근거만
		// 봐서는 "볼 게 없었다" 와 "정책이 이 체크를 껐다" 를 구별할 수 없다. 통과 기록이라
		// Agreed·Level 에는 영향이 없다.
		// 비교할 geometry 가 없는 것은 정책 모양(공유 전용)이 그런 것이라 축이 막힌 게 아니다 —
		// 그 경우의 등급 상한은 광고 축(advertisementApplicable)이 이미 정한다. 여기서 또 낮추면
		// 광고까지 대조한 공유 정책이 Observed 로 주저앉는다.
		record(name, "", true, "no report geometry to compare (spec has no geometry constraint)")
		return true
	}
	compared := true
	for _, pci := range sortedKeys(in.ReportGeometry) {
		// 노드 에이전트가 그 장치를 못 봤으면 보고값은 "관측 결과" 가 아니라 공백이다. 그 공백을
		// 기대값과 대조해 불일치로 부르면, 볼 수 없다는 사실이 하드웨어 불일치로 둔갑한다
		// (예: 비특권 detector 는 MIG 를 관측할 수 없다).
		if msg := in.ReportErrors[pci]; msg != "" {
			record(name, pci, true, "node agent could not observe this device: "+msg)
			compared = false
			continue
		}
		if got := in.ReportGeometry[pci]; got != in.Expectation.Geometry {
			record(name, pci, false, fmt.Sprintf("report geometry %q != expected %q", got, in.Expectation.Geometry))
			continue
		}
		record(name, pci, true, "")
	}
	return compared
}

// evalAdvertisement 는 노드가 기대한 자원을 실제로 광고하는지 본다.
// 판정은 "충족" 이다(초과 광고는 통과) — allocatableMet 과 같은 규율이라 두 경로가 갈라지지 않는다.
//
// 반환값은 "실제로 뭔가 비교했는가" 다. RNGD 같은 flat 광고 backend 는 프로파일별 기대 수량이라는
// 개념 자체가 없어 Expectation.Allocatable 이 원천적으로 빈다 — 그 경우 이 체크는 통과 기록만
// 남기고 false 를 돌려줘 levelFor 가 등급을 부풀리지 않게 한다.
func evalAdvertisement(in Inputs, record func(name, target string, passed bool, msg string)) bool {
	const name = v1alpha1.EvidenceCheckAdvertisement
	if len(in.Expectation.Allocatable) == 0 {
		record(name, "", true, "no advertisement expectation to compare")
		return false
	}
	for _, res := range sortedKeys(in.Expectation.Allocatable) {
		want := in.Expectation.Allocatable[res]
		got, ok := in.Allocatable[res]
		if !ok {
			record(name, res, want <= 0, "resource not advertised")
			continue
		}
		if got < want {
			record(name, res, false, fmt.Sprintf("advertised %d < expected %d", got, want))
			continue
		}
		record(name, res, true, "")
	}
	return true
}

// levelFor 는 활성 체크 조합이 도달할 수 있는 최고 등급이다(전부 통과했을 때만 호출된다).
// advertisementApplicable=false 면 광고 축이 아무것도 비교하지 않은 것이므로 Observed 를 넘지
// 못한다 — 비교한 적 없는 걸 FunctionallyVerified 로 주장하지 않는다.
func levelFor(p EffectivePolicy, advertisementApplicable bool) string {
	if p.Enabled(v1alpha1.EvidenceCheckAdvertisement) && !advertisementApplicable {
		return v1alpha1.EvidenceLevelObserved
	}
	if p.Enabled(v1alpha1.EvidenceCheckAllocationProbe) {
		return v1alpha1.EvidenceLevelAllocationVerified
	}
	if p.Enabled(v1alpha1.EvidenceCheckAdvertisement) {
		return v1alpha1.EvidenceLevelFunctionallyVerified
	}
	return v1alpha1.EvidenceLevelObserved
}

func sortChecks(cs []v1alpha1.EvidenceCheck) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Name != cs[j].Name {
			return cs[i].Name < cs[j].Name
		}
		return cs[i].Target < cs[j].Target
	})
}

// sortedKeys 는 여러 맵의 키 합집합을 정렬해 돌려준다(판정 순서 결정론).
func sortedKeys[V any](ms ...map[string]V) []string {
	set := map[string]struct{}{}
	for _, m := range ms {
		for k := range m {
			set[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for i, s := range in {
		if i > 0 && in[i-1] == s {
			continue
		}
		out = append(out, s)
	}
	return out
}
