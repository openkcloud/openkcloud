// ============================================================
// registry_test.go: 선언 레지스트리 정합 시험
// 상세: 모든 항목이 정직성 규칙(Unsupported/Unknown 은 Reason, Native/Software 는 Source)을
//
//	지키고, 알려진 근거 항목이 기대 상태를 갖는지 본다.
//
// 생성일: 2026-09-19 | 수정일: 2026-10-11
// ============================================================
package vendorcap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestEveryEntryIsHonest(t *testing.T) {
	for _, k := range Keys() {
		for _, c := range Capabilities() {
			s, ok := Lookup(k.Vendor, k.Product, c)
			if !ok {
				t.Fatalf("%v/%s 항목 누락 — 모든 키×능력은 명시돼야 한다", k, c)
			}
			switch s.State {
			case StateUnsupported, StateUnknown:
				if s.Reason == "" {
					t.Errorf("%v/%s: %s 인데 Reason 이 없다", k, c, s.State)
				}
			case StateNative, StateSoftware:
				if s.Source == "" {
					t.Errorf("%v/%s: %s 인데 Source 가 없다", k, c, s.State)
				}
			default:
				t.Errorf("%v/%s: 알 수 없는 State %q", k, c, s.State)
			}
		}
	}
}

func TestGroundedEntries(t *testing.T) {
	cases := []struct {
		vendor, product string
		cap             Capability
		want            State
	}{
		{"nvidia", "", CapPartition, StateNative},             // MIG
		{"nvidia", "", CapTimeSlicing, StateSoftware},         // device-plugin replicas
		{"nvidia", "", CapMultiProcess, StateNative},          // MPS control daemon
		{"furiosa", "rngd", CapPartition, StateNative},        // PE-fusion
		{"furiosa", "rngd", CapTimeSlicing, StateUnsupported}, // EBUSY 배타 open
		{"furiosa", "warboy", CapPartition, StateUnsupported}, // 단일 코어
		{"rebellions", "", CapPartition, StateUnsupported},    // 공개 소스 조사
		{"rebellions", "", CapTimeSlicing, StateSoftware},     // 실 워크로드 동시 실행 실측
		{"tenstorrent", "", CapPartition, StateUnsupported},   // 공개 소스 조사
		{"tenstorrent", "", CapDevicePlugin, StateSoftware},   // kcloud 자체 device-plugin
		{"tenstorrent", "", CapTimeSlicing, StateUnsupported}, // co-run 직렬화 실측 — tt-metal UMD KMD 락
	}
	for _, c := range cases {
		got, ok := Lookup(c.vendor, c.product, c.cap)
		if !ok || got.State != c.want {
			t.Errorf("%s/%s %s: got %v(ok=%v) want %s", c.vendor, c.product, c.cap, got.State, ok, c.want)
		}
	}
}

func TestLookupVendorPrefersSupported(t *testing.T) {
	// furiosa 는 rngd 가 partition Native 라 벤더 단위로는 Native 여야 한다(webhook 이 이 값을 쓴다).
	if s := LookupVendor("furiosa", CapPartition); s.State != StateNative {
		t.Fatalf("furiosa partition 벤더 단위 = %s, want Native", s.State)
	}
	if s := LookupVendor("rebellions", CapPartition); s.State != StateUnsupported {
		t.Fatalf("rebellions partition = %s, want Unsupported", s.State)
	}
}

func TestRebellionsPartitionReasonCitesSourceReview(t *testing.T) {
	// Reason 은 rblnlib-go v0.3.0 소스 검토 근거를 인용해야 한다 — "내부 미검토" 문구로
	// 되돌아가는 회귀를 막는다.
	s, ok := Lookup("rebellions", "", CapPartition)
	if !ok {
		t.Fatalf("rebellions/partition 항목 누락")
	}
	if s.State != StateUnsupported {
		t.Fatalf("rebellions partition state = %s, want Unsupported", s.State)
	}
	if !strings.Contains(s.Reason, "rblnlib-go v0.3.0") {
		t.Fatalf("rebellions partition Reason 에 rblnlib-go v0.3.0 검토 근거가 없다: %q", s.Reason)
	}
}

func TestTenstorrentTimeSlicingDowngradedToUnsupported(t *testing.T) {
	// tt-metal UMD 가 /dev/tenstorrent/0 에 KMD 락을 잡아 실제 워크로드 두 개가 직렬화되므로
	// co-run 실측 이후 timeSlicing 은 Unsupported 다. Software 로 되돌아가거나 근거가
	// 직렬화 사실·evidence 출처를 지우면 이 시험이 실패한다(bare os.open 동시 성공만으로 다시
	// 지원으로 읽히는 회귀를 막는다).
	s, ok := Lookup("tenstorrent", "", CapTimeSlicing)
	if !ok {
		t.Fatalf("tenstorrent/timeSlicing 항목 누락")
	}
	if s.State != StateUnsupported {
		t.Fatalf("tenstorrent timeSlicing state = %s, want Unsupported", s.State)
	}
	if !strings.Contains(s.Reason, "직렬화") {
		t.Fatalf("tenstorrent timeSlicing Reason 에 직렬화 근거가 없다: %q", s.Reason)
	}
	if !strings.Contains(s.Source, "test/live/tt-timeslice-throughput") {
		t.Fatalf("tenstorrent timeSlicing Source 에 재현 하네스 경로가 없다: %q", s.Source)
	}
}

func TestRebellionsTimeSlicingGroundedInRealWorkload(t *testing.T) {
	// rebellions ATOM 은 두 실 워크로드가 같은 장치(device 0)에서 동시 진행함을 실측으로 보인다.
	// 주근거는 device_open 이 배리어에 비례해 안 늘어 배타 락이 없다는 점과 rbln-smi 가 NPU 0 에
	// 두 컨텍스트를 본 점이고, 처리량(합 약 1.83×solo)은 보조다. timeSlicing 은 이 실측을 근거로
	// Software 를 유지한다 — 유휴 os.open 데모로 되돌아가거나 근거 없이 Unsupported 로 내려가면
	// 이 시험이 실패한다(약한 근거로의 회귀·무근거 하향 차단).
	s, ok := Lookup("rebellions", "", CapTimeSlicing)
	if !ok {
		t.Fatalf("rebellions/timeSlicing 항목 누락")
	}
	if s.State != StateSoftware {
		t.Fatalf("rebellions timeSlicing state = %s, want Software", s.State)
	}
	if !strings.Contains(s.Source, "test/live/rebellions-timeslice") {
		t.Fatalf("rebellions timeSlicing Source 에 실 워크로드 재현 하네스 경로가 없다: %q", s.Source)
	}
	if !strings.Contains(s.Source, "동시") {
		t.Fatalf("rebellions timeSlicing Source 에 동시 실행 근거가 없다: %q", s.Source)
	}
}

func TestLookupVendorFallbackIsFirstSortedProduct(t *testing.T) {
	// furiosa 의 rngd/warboy 모두 timeSlicing·multiProcess 에서 Native/Software 가 아니므로
	// LookupVendor 는 Keys() 정렬상 첫 product(rngd)의 값을 돌려준다(결정적 폴백).
	if s := LookupVendor("furiosa", CapTimeSlicing); s.State != StateUnsupported {
		t.Fatalf("furiosa timeSlicing 폴백 = %s, want Unsupported(rngd 값)", s.State)
	}
	if s := LookupVendor("furiosa", CapMultiProcess); s.State != StateUnsupported {
		t.Fatalf("furiosa multiProcess 폴백 = %s, want Unsupported(rngd 값)", s.State)
	}
}

func TestAssuranceDefaultsToNone(t *testing.T) {
	// 확인 수준을 적지 않은 항목은 Assurance() 가 AssuranceNone 이어야 한다. 기본값이
	// 그보다 높으면 아무 근거 없이 "확인됐다" 로 읽힌다.
	s, ok := Lookup("furiosa", "warboy", CapReset)
	if !ok {
		t.Fatalf("furiosa/warboy/reset 항목 누락")
	}
	if got := s.Assurance(); got != AssuranceNone {
		t.Errorf("확인 수준 = %s, want %s", got, AssuranceNone)
	}
}

// hwEvidenceRe 는 Source 프로세 안에서 재현 경로 토큰만 뽑는다. test/live/ 를 포함하면서
// 경로 문자(영숫자·_·.·/·-)로만 이뤄진 덩어리를 잡으므로 괄호·세미콜론·한글·공백에서
// 끊긴다 — "…(co-run 실측); 재현은 test/live/x/run.sh" 같은 프로세도 경로만
// 정확히 잘라낸다.
var hwEvidenceRe = regexp.MustCompile(`[\w./-]*test/live/[\w./-]*`)

func TestHardwareVerifiedRequiresEvidencePath(t *testing.T) {
	// AssuranceHardware 는 그 장치가 붙은 노드에서 확인했다는 뜻이라 시스템에서 가장
	// 센 주장이다. 근거 경로가 Source 에 있기만 하면 되는 게 아니라 실제로 존재해야
	// 한다 — 만들지 않은 파일을 인용하면 등급만 높고 근거가 없다.
	//
	// 근거는 이 저장소 안의 재현 하네스(test/live/...)만 인정한다. 형제 docs 저장소는
	// remote 가 없어 이 저장소를 받은 사람이 도달할 수 없으므로 근거로 쓰지 않는다.
	const repoRoot = "../.."

	for k, caps := range registry {
		for c, s := range caps {
			if s.Assurance() != AssuranceHardware {
				continue
			}
			paths := hwEvidenceRe.FindAllString(s.Source, -1)
			if len(paths) == 0 {
				t.Errorf("%v/%v 가 AssuranceHardware 인데 Source 에 재현 경로가 없다: %q", k, c, s.Source)
				continue
			}
			for _, p := range paths {
				full := filepath.Join(repoRoot, p)
				if _, err := os.Stat(full); err != nil {
					t.Errorf("%v/%v 가 인용한 경로가 실재하지 않는다: %q → %s (%v)", k, c, p, full, err)
				}
			}
		}
	}
}

func TestAssuranceAboveNoneNamesAReference(t *testing.T) {
	// 확인 수준을 None 위로 올렸다면 무엇으로 확인했는지가 참조로 남아야 한다. "비지 않음"
	// 만으로는 부족하다 — native()·software() 가 Source 를 필수로 받아 그 검사는 모든 항목을
	// 자동 통과시켜 버린다. 그래서 "검증을 가리키는 종류의 참조"(Go 시험 파일 _test.go,
	// 라이브 하네스 test/live/)를 요구한다 — 둘 다 이 저장소 안이라 열어 볼 수 있다(형제
	// docs 저장소는 remote 가 없어 근거로 못 쓴다). AssuranceHardware 는 더 엄한
	// TestHardwareVerifiedRequiresEvidencePath 가 경로 존재까지 따로 확인한다.
	indicatesVerification := func(ref string) bool {
		return strings.Contains(ref, "_test.go") || strings.Contains(ref, "test/live/")
	}
	// noHarness 는 재현 하네스를 아직 만들지 않은 항목이 스스로를 드러내는 표시다. 근거가
	// 이 저장소 밖에만 있어 독자가 도달할 수 없다는 뜻이며, 하네스가 생기면 지운다. 표시를
	// 쓴 항목은 아래에서 따로 세어 수가 늘면 실패시킨다 — 예외가 조용히 번지지 않게 한다.
	const noHarness = "재현 하네스 미작성"
	// wantNoHarness 는 하네스가 없는 항목 수(nvidia/isolation, furiosa rngd/isolation)다 —
	// 근거는 형제 저장소 tester/ 에 있다.
	const wantNoHarness = 2
	gotNoHarness := 0
	for k, caps := range registry {
		for c, s := range caps {
			if s.Assurance() == AssuranceNone {
				continue
			}
			if strings.Contains(s.Source, noHarness) {
				gotNoHarness++
				continue
			}
			ok := indicatesVerification(s.Source)
			for _, r := range s.TestRefs {
				ok = ok || indicatesVerification(r)
			}
			if !ok {
				t.Errorf("%v/%v 가 %s 인데 검증을 가리키는 참조가 없다(_test.go·test/live/ 중 하나 필요): source=%q testRefs=%v",
					k, c, s.Assurance(), s.Source, s.TestRefs)
			}
		}
	}
	if gotNoHarness != wantNoHarness {
		t.Errorf("재현 하네스 없는 항목 = %d, want %d — 늘었으면 하네스를 만들고, 줄었으면 이 상수를 낮춰라",
			gotNoHarness, wantNoHarness)
	}
}

func TestAssuranceAnswersWithoutLookup(t *testing.T) {
	// matrix.go 처럼 registry 맵을 Lookup 을 거치지 않고 직접 읽거나, 패키지 밖에서
	// Support{} 를 직접 만들어도(예: acceleratordescriptor_controller.go 의
	// sharingSupport 폴백) Assurance() 는 AssuranceNone 을 돌려줘야 한다 — 조회
	// 함수를 거치지 않는 우회 경로가 다시 생겨도 빈 값이 등급으로 잘못 읽히지 않게
	// 값 자신이 답한다.
	raw := registry[Key{"furiosa", "warboy"}][CapReset]
	if got := raw.Assurance(); got != AssuranceNone {
		t.Errorf("registry 맵 직접 접근 Assurance() = %s, want %s", got, AssuranceNone)
	}
	external := Support{State: StateUnknown, Reason: "패키지 밖에서 만든 값(레지스트리 키 없음 폴백 흉내)"}
	if got := external.Assurance(); got != AssuranceNone {
		t.Errorf("패키지 밖 리터럴 Assurance() = %s, want %s", got, AssuranceNone)
	}
}

func TestCapResetIsGroundedForEveryVendor(t *testing.T) {
	// CapReset 이 조사 없이 Unknown 으로 남아 있으면 복구 능력의 근거가 없다.
	// Unknown 으로 두는 것은 괜찮지만 조사했다는 근거는 있어야 한다.
	for _, k := range Keys() {
		s, ok := Lookup(k.Vendor, k.Product, CapReset)
		if !ok {
			t.Errorf("%v 에 CapReset 항목이 없다", k)
			continue
		}
		if s.Source == "" || strings.Contains(s.Source, "미확인") {
			t.Errorf("%v CapReset 에 조사 근거가 없다: %q", k, s.Source)
		}
	}
}

func TestNoCapabilityLeftUnknownWithoutReason(t *testing.T) {
	// Unknown 으로 두는 것은 괜찮으나 왜 모르는지는 있어야 한다. "미실측"·"미조사"
	// 같은 표현만 남아 있으면 조사조차 하지 않은 것이므로 실패시킨다.
	bad := []string{"미실측", "미조사", "미확인"}
	for _, k := range Keys() {
		for _, c := range []Capability{CapIsolation, CapMultiProcess, CapReset, CapRuntime} {
			s, ok := Lookup(k.Vendor, k.Product, c)
			if !ok {
				continue
			}
			for _, b := range bad {
				if strings.Contains(s.Source, b) {
					t.Errorf("%v/%v 근거가 %q 로 남아 있다: %q", k, c, b, s.Source)
				}
			}
		}
	}
}

func TestCapRuntimeIsInvestigatedForEveryVendor(t *testing.T) {
	// "런타임 생애주기 미조사" 로 남아 있으면 미션 목표 3(SDK·런타임·툴킷 생애주기 관리)의
	// 근거가 비어 있다. 조사한 뒤에는 native·unsupported·unknown 중 하나여야 하고, 어느
	// 경우든 "미조사" placeholder 가 남으면 안 된다. 기존 placeholder 는 unknown() 이 채우는
	// Reason 에 있으므로(Source 아님) 두 필드를 모두 본다 — 한쪽만 보면 회귀를 놓친다.
	for _, k := range Keys() {
		s, ok := Lookup(k.Vendor, k.Product, CapRuntime)
		if !ok {
			t.Errorf("%v 에 CapRuntime 항목이 없다", k)
			continue
		}
		if strings.Contains(s.Reason, "미조사") || strings.Contains(s.Source, "미조사") {
			t.Errorf("%v CapRuntime 이 미조사로 남아 있다: reason=%q source=%q", k, s.Reason, s.Source)
		}
	}
}
