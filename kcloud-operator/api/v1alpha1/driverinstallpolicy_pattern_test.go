// ============================================================
// driverinstallpolicy_pattern_test.go: DIP CRD image pattern 검증 단위 테스트
// 상세: kubebuilder Pattern annotation 으로 정의된 DriverSpec.Image 의 syntactic 검증
//       regex 가 sub-version (예: "-v17.1") 를 허용하고 invalid char 는 차단함을 검증
//       (followup plan §F4)
// 생성일: 2026-04-28
// ============================================================

package v1alpha1

import (
	"regexp"
	"testing"
)

// dipImagePattern 은 driverinstallpolicy_types.go 의 DriverSpec.Image 에 부착된
// kubebuilder Pattern annotation 과 동일한 regex 다. CRD 가 빌드되지 않은 상태에서도
// 단위 테스트가 가능하도록 코드와 정확히 동일한 문자열을 명시한다 (drift 시 양쪽 모두 갱신).
const dipImagePattern = `^[a-zA-Z0-9._:/-]+:[a-zA-Z0-9._+-]+$`

// TestDIPPattern_AllowsSubVersion 은 minor sub-version 접미사 (예: "-v17.1", "-v172") 를
// 포함한 image tag 가 CRD pattern 에서 허용됨을 검증한다 (followup plan §F4 — image variant
// 운영 자유도 확보).
func TestDIPPattern_AllowsSubVersion(t *testing.T) {
	re := regexp.MustCompile(dipImagePattern)
	cases := []string{
		"foo:v17",
		"foo:v17.1",
		"foo:v172",
		"foo:580.142-v17",
		"foo:580.142-v17.1",
		"foo:580.142-v17.2",
		"foo:580.142-v172",
		"foo:580.142-v18",
		"foo:590.48.01-v17",
		"foo:590.48.01-v17.1",
		"foo:latest",
		"registry.example.com/nvidia-driver-ds:580.142-v17.1",
		"registry.example.com/nvidia-driver-ds:580.142-v172",
		"ghcr.io/you/repo:1.7.8",
		"129.254.202.88:5100/furiosa-driver-installer:1.7.8",
	}
	for _, img := range cases {
		if !re.MatchString(img) {
			t.Errorf("DIP pattern 이 합법 image 를 거부함: %q", img)
		}
	}
}

// TestDIPPattern_RejectsInvalidChars 는 docker tag 로 허용되지 않는 문자 (공백, `*`, `@`,
// 한글 등) 가 포함된 image 가 CRD pattern 에서 차단됨을 검증한다.
//
// 의미론적 검증 (broken plain semver tag 차단) 은 isVerifiedBuildTag 가 담당하므로
// 여기서는 syntactic invalid 만 거부하면 충분하다.
func TestDIPPattern_RejectsInvalidChars(t *testing.T) {
	re := regexp.MustCompile(dipImagePattern)
	cases := []string{
		"foo:bar baz",    // space in tag
		"foo:bar*",       // glob char
		"foo:bar@sha",    // @ char (digest 표기는 별도 형식)
		"foo:한글",         // non-ASCII
		"no-colon-image", // 콜론 없음 (registry/repo:tag 형식 아님)
		":no-name-tag",   // host 부 비어있음
		"name:",          // tag 부 비어있음
		"foo:bar/baz",    // tag 에 slash 금지
		"foo:bar\nbaz",   // newline
		"",               // empty
	}
	for _, img := range cases {
		if re.MatchString(img) {
			t.Errorf("DIP pattern 이 invalid image 를 허용함: %q", img)
		}
	}
}
