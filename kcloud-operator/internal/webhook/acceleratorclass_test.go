// ============================================================
// acceleratorclass_test.go: AcceleratorClass validator 테스트
// 상세: CRD 스키마가 못 잡는 것(벤더 중복, profile 형식, product 모호)만 검증한다.
// 생성일: 2026-07-30 | 수정일: 2026-07-30
// ============================================================
package webhook

import (
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func goodClass() *npuv1alpha1.AcceleratorClass {
	return &npuv1alpha1.AcceleratorClass{Spec: npuv1alpha1.AcceleratorClassSpec{
		Class: "medium",
		Mappings: []npuv1alpha1.AcceleratorMapping{
			{Vendor: "nvidia", Product: "A30", NativeProfile: "2g.12gb"},
			{Vendor: "furiosa", Product: "rngd", NativeProfile: "2core.12gb"},
		},
	}}
}

func TestValidateAcceleratorClass(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*npuv1alpha1.AcceleratorClass)
		wantErr bool
	}{
		{"valid", func(*npuv1alpha1.AcceleratorClass) {}, false},
		{"empty mappings", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings = nil }, true},
		{"unknown vendor", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].Vendor = "banana" }, true},
		{"duplicate vendor", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[1].Vendor = c.Spec.Mappings[0].Vendor }, true},
		{"duplicate vendor ignoring case", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[1].Vendor = "NVIDIA" }, true},
		{"profile with space", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].NativeProfile = "2g 12gb" }, true},
		{"profile with slash", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].NativeProfile = "a/b" }, true},
		{"empty profile ok", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].NativeProfile = "" }, false},
		{"dashed profile ok", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].NativeProfile = "vendor-defined" }, false},
		// furiosa 는 벤더 하나에 제품이 둘(rngd/warboy)이라 product 없이는 어느 리소스명인지
		// 못 정한다 — Translate(internal/intent/resources.go)가 나중에 거절할 바에야 admission
		// 에서 바로 막는다.
		{"furiosa without product rejected", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[1].Product = "" }, true},
		{"furiosa unknown product rejected", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[1].Product = "grape" }, true},
		// nvidia 는 제품이 하나뿐이라 product 생략이 허용된다(사람이 읽는 표기일 뿐).
		{"nvidia without product ok", func(c *npuv1alpha1.AcceleratorClass) { c.Spec.Mappings[0].Product = "" }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ac := goodClass()
			c.mutate(ac)
			err := validateAcceleratorClass(ac)
			if (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v got err=%v", c.wantErr, err)
			}
		})
	}
}

func TestValidateAcceleratorClassWrongType(t *testing.T) {
	if err := validateAcceleratorClass(&npuv1alpha1.NPUClusterPolicy{}); err == nil {
		t.Fatal("wrong type accepted")
	}
}
