// ============================================================
// present_label_test.go: PresentNodeLabel 의 키 형식 시험
// 상세: 벤더 존재 라벨의 단일 출처가 "kcloud.ai/<vendor>.present" 형식을 지키는지 본다.
// 생성일: 2026-09-30
// ============================================================
package v1alpha1

import "testing"

func TestPresentNodeLabel(t *testing.T) {
	cases := map[string]string{
		"nvidia":      "kcloud.ai/nvidia.present",
		"rngd":        "kcloud.ai/rngd.present",
		"tenstorrent": "kcloud.ai/tenstorrent.present",
	}
	for vendor, want := range cases {
		if got := PresentNodeLabel(vendor); got != want {
			t.Errorf("PresentNodeLabel(%s) = %q, want %q", vendor, got, want)
		}
	}
}
