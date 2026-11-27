// ============================================================
// profiles_test.go: ValidateLayoutShape/KnownMIGProfiles 시험
// 상세: 삭제 실험으로 CountPerDevice 경계를 실제로 잡는지 확인(TestValidateLayoutShape 참조).
// 생성일: 2026-08-24 | 수정일: 2026-10-11
// ============================================================
package partition

import (
	"os"
	"strings"
	"testing"
)

func TestValidateLayoutShape(t *testing.T) {
	cases := []struct {
		name    string
		vendor  string
		layout  []Layout
		wantErr bool
	}{
		{"정상", "nvidia", []Layout{{Profile: "1g.6gb", CountPerDevice: 4}}, false},
		{"프로파일 형식 오류", "nvidia", []Layout{{Profile: "1g6gb", CountPerDevice: 4}}, true},
		{"개수 0", "nvidia", []Layout{{Profile: "1g.6gb", CountPerDevice: 0}}, true},
		{"개수 음수", "nvidia", []Layout{{Profile: "1g.6gb", CountPerDevice: -1}}, true},
		{"레이아웃 둘", "nvidia", []Layout{{Profile: "1g.6gb", CountPerDevice: 4}, {Profile: "2g.12gb", CountPerDevice: 1}}, true},
		// 빈 레이아웃은 여기서 통과한다 — sharing 만 있는 정책(순수 공유)이 정상이고, 레이아웃과
		// sharing 이 둘 다 비었는지는 ACPP 전체를 봐야 알 수 있어 webhook 이 판정한다.
		{"빈 레이아웃은 여기서 통과", "nvidia", nil, false},
		{"furiosa 는 프로파일 형식을 강제하지 않음", "furiosa", []Layout{{Profile: "dual-core", CountPerDevice: 2}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLayoutShape(tc.vendor, tc.layout)
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestKnownMIGProfiles_모르는제품은통과(t *testing.T) {
	if _, ok := KnownMIGProfiles("geforce-gtx-970"); ok {
		t.Error("표에 없는 제품이 걸렸다 — fail-open 이어야 한다")
	}
	got, ok := KnownMIGProfiles("a30")
	if !ok || len(got) == 0 {
		t.Errorf("a30 프로파일 표가 비었다: %v %v", got, ok)
	}
}

// a100 은 MIG 가능한 실제 제품이지만 이 저장소에 실측 근거(testdata)가 없어 표에 없다.
// "표에 없으면 판정하지 않는다" 는 fail-open 의 의미가 미확인 제품에서도 성립함을 못박는다.
func TestKnownMIGProfiles_A100은실측근거없어표에없음(t *testing.T) {
	if _, ok := KnownMIGProfiles("a100-pcie-40gb"); ok {
		t.Error("a100 은 실측 근거가 없어 표에 없어야 한다 — 있다면 검증 안 된 값이 섞인 것")
	}
}

// generic 은 detector 가 제품을 판정하지 못했다는 표시라 표의 어떤 항목과도 같지 않아야 한다.
// upgrade.DeviceModelMatches 를 가드 없이 그대로 쓰면 그 함수의 원 도메인(업그레이드 정책
// 매칭)에서의 와일드카드 취급 때문에 표의 첫 항목(a30)에 오매칭된다 — 이 시험은 그 가드가
// KnownMIGProfiles 안에 있어야 함을 못박는다. 대소문자 무관도 함께 확인한다.
func TestKnownMIGProfiles_generic은판정하지않음(t *testing.T) {
	for _, product := range []string{"generic", "Generic", ""} {
		if _, ok := KnownMIGProfiles(product); ok {
			t.Errorf("product=%q: generic/빈 문자열이 표에 걸렸다 — 제품 미판정은 fail-open 이어야 한다", product)
		}
	}
}

func TestNvidiaProfileRegexDefinedOnce(t *testing.T) {
	src, err := os.ReadFile("nvidia/backend.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "regexp.MustCompile(`^\\d+g\\.\\d+gb$`)") {
		t.Fatalf("nvidia/backend.go 가 MIG 프로파일 정규식을 따로 정의한다 — profiles.go 의 IsNvidiaMIGProfile 을 써야 한다")
	}
}

func TestIsNvidiaMIGProfile(t *testing.T) {
	for p, want := range map[string]bool{"1g.6gb": true, "2g.12gb": true, "1g": false, "x1g.6gb": false, "1g.6gbx": false} {
		if got := IsNvidiaMIGProfile(p); got != want {
			t.Errorf("%q: got %v want %v", p, got, want)
		}
	}
}
