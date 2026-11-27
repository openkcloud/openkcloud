// ============================================================
// vendor_adapter_move_test.go: install Job 벤더 판단 현재 동작 고정 시험
// 상세: 노드 셀렉터·Furiosa APT 인증 마운트 판단의 벤더별 기대값을 표로 잠근다. 이 표가
// 계속 통과해야 driveradapter 조회로 옮긴 뒤에도 동작이 바뀌지 않았음을 보장한다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================

package driverjob

import (
	"reflect"
	"testing"
)

// TestInstallJobVendorDecisionsByVendor 는 RenderInstallJob 이 벤더·모델마다 내는 노드
// 셀렉터와 Furiosa APT 인증 마운트를 한 표로 고정한다. 미등록 벤더와 tenstorrent 는 nil 이
// 아닌 빈 셀렉터를 받는다(현재 default 동작).
func TestInstallJobVendorDecisionsByVendor(t *testing.T) {
	cases := []struct {
		name         string
		vendor       string
		model        string
		wantSelector map[string]string
		wantAuth     bool
	}{
		{"nvidia", tNvidia, "generic",
			map[string]string{"kcloud.ai/nvidia.present": "true"}, false},
		{"nvidia mixed case", "NVIDIA", "generic",
			map[string]string{"kcloud.ai/nvidia.present": "true"}, false},
		{"furiosa warboy", tFuriosa, "warboy",
			map[string]string{"kcloud.ai/furiosa.present": "true"}, true},
		{"furiosa empty model = warboy", tFuriosa, "",
			map[string]string{"kcloud.ai/furiosa.present": "true"}, true},
		{"furiosa rngd", tFuriosa, "rngd",
			map[string]string{"kcloud.ai/rngd.present": "true"}, false},
		{"furiosa rngd mixed case", "Furiosa", "RNGD",
			map[string]string{"kcloud.ai/rngd.present": "true"}, false},
		{"rebellions host-managed", "rebellions", "",
			map[string]string{"kcloud.ai/rebellions.present": "true"}, false},
		{"tenstorrent empty selector", "tenstorrent", "",
			map[string]string{}, false},
		{"unknown vendor empty selector", "acme", "",
			map[string]string{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := basePolicy()
			pol.Spec.Vendor = tc.vendor
			pol.Spec.Model = tc.model
			job := RenderInstallJob(pol, "n1", "img:tag", "1.0")

			if got := job.Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, tc.wantSelector) {
				t.Errorf("NodeSelector = %#v, want %#v", got, tc.wantSelector)
			}

			gotAuth := false
			for _, v := range job.Spec.Template.Spec.Volumes {
				if v.Name == tFuriosaAuth {
					gotAuth = true
				}
			}
			if gotAuth != tc.wantAuth {
				t.Errorf("furiosa-auth 볼륨 = %v, want %v", gotAuth, tc.wantAuth)
			}
		})
	}
}
