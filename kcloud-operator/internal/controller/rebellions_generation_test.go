// ============================================================
// rebellions_generation_test.go: Rebellions device plugin 판 판별 표 시험
// 상세: 태그 형태(v 유무, 포트 붙은 레지스트리, digest, rc 접미사)와 명시 필드 우선, 자원 이름 메모,
//       CDI 종류를 고정한다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"strings"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestRebellionsPluginGeneration(t *testing.T) {
	for _, tc := range []struct {
		name, image, explicit, wantGen, wantNote string
	}{
		{"구판 v 접두", "docker.io/rebellions/k8s-device-plugin:v0.3.6", "", rbllnsGenV03, "태그 v0.3.6"},
		{"구판 v 없음", "docker.io/rebellions/k8s-device-plugin:0.3.6", "", rbllnsGenV03, "태그 0.3.6"},
		{"포트 붙은 레지스트리", "registry.example.com:5100/kcloud/rebellions/k8s-device-plugin:v0.3.6", "", rbllnsGenV03, "태그 v0.3.6"},
		{"신판", "docker.io/rebellions/k8s-device-plugin:v0.4.3", "", rbllnsGenV04, "태그 v0.4.3"},
		{"신판 v 없음", "rebellions/k8s-device-plugin:0.4.3", "", rbllnsGenV04, "태그 0.4.3"},
		{"신판 rc", "rebellions/k8s-device-plugin:v0.4.4-rc1", "", rbllnsGenV04, "태그 v0.4.4-rc1"},
		{"v0.5 이상", "rebellions/k8s-device-plugin:v0.5.0", "", rbllnsGenV04, "태그 v0.5.0, 검증하지 않은 판"},
		{"1.x", "rebellions/k8s-device-plugin:1.0.0", "", rbllnsGenV04, "태그 1.0.0, 검증하지 않은 판"},
		{"latest", "rebellions/k8s-device-plugin:latest", "", rbllnsGenV03, `태그 "latest" 로 판별 불가, 기본 v0.3`},
		{"digest 만", "rebellions/k8s-device-plugin@sha256:abcd", "", rbllnsGenV03, `태그 "" 로 판별 불가, 기본 v0.3`},
		{"태그+digest+포트", "registry.example.com:5100/kcloud/rebellions/k8s-device-plugin:v0.4.3@sha256:abcd", "", rbllnsGenV04, "태그 v0.4.3"},
		{"명시 v0.4 가 태그보다 우선", "rebellions/k8s-device-plugin:v0.3.6", rbllnsGenV04, rbllnsGenV04, "명시 값 v0.4"},
		{"명시 v0.3 가 태그보다 우선", "rebellions/k8s-device-plugin:v0.4.3", rbllnsGenV03, rbllnsGenV03, "명시 값 v0.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := npuv1alpha1.RebellionsSpec{DevicePluginImage: tc.image, DevicePluginGeneration: tc.explicit}
			gen, note := rebellionsPluginGeneration(spec)
			if gen != tc.wantGen || note != tc.wantNote {
				t.Errorf("got (%q, %q), want (%q, %q)", gen, note, tc.wantGen, tc.wantNote)
			}
		})
	}
}

// 신판은 resourceName·resourcePrefix 를 따를 수 없다. 기본값이면 메모가 없고, 바꿨으면 남는다.
func TestRebellionsPluginGenerationResourceNameNote(t *testing.T) {
	base := npuv1alpha1.RebellionsSpec{DevicePluginImage: "rebellions/k8s-device-plugin:v0.4.3",
		ResourceName: "ATOM", ResourcePrefix: "rebellions.ai"}
	if _, note := rebellionsPluginGeneration(base); strings.Contains(note, "resourceName") {
		t.Errorf("기본 자원 이름인데 메모가 붙었다: %q", note)
	}
	custom := base
	custom.ResourcePrefix = "example.com"
	if _, note := rebellionsPluginGeneration(custom); !strings.HasSuffix(note,
		", resourceName·resourcePrefix 는 v0.4 형식에서 쓰이지 않음") {
		t.Errorf("바꾼 prefix 메모가 없다: %q", note)
	}
	legacy := custom
	legacy.DevicePluginImage = "rebellions/k8s-device-plugin:v0.3.6"
	if _, note := rebellionsPluginGeneration(legacy); strings.Contains(note, "resourceName") {
		t.Errorf("구판은 prefix 를 따르므로 메모가 없어야 한다: %q", note)
	}
}

func TestRbllnsCDIKind(t *testing.T) {
	const defaultKind = "rebellions.ai/npu"
	for _, tc := range []struct{ prefix, gen, want string }{
		{"", rbllnsGenV03, defaultKind},
		{"rebellions.ai", rbllnsGenV03, defaultKind},
		{"example.com", rbllnsGenV03, "example.com/npu"},
		{"example.com", rbllnsGenV04, defaultKind},
	} {
		got := rbllnsCDIKind(npuv1alpha1.RebellionsSpec{ResourcePrefix: tc.prefix}, tc.gen)
		if got != tc.want {
			t.Errorf("prefix=%q gen=%s: got %q, want %q", tc.prefix, tc.gen, got, tc.want)
		}
	}
}
