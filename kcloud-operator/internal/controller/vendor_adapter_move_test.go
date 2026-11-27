// ============================================================
// vendor_adapter_move_test.go: 드라이버 생애주기 벤더 판단의 현재 동작 고정 시험
// 상세: 노드 셀렉터·rmmod 명령·Furiosa APT 인증 마운트·runtimeClass 판단을 driveradapter
//       조회로 옮기기 전, 렌더 결과의 벤더별 기대값을 표로 잠근다. 옮긴 뒤에도 이 표가
//       그대로 통과해야 동작이 바뀌지 않았음을 보장한다.
// 생성일: 2026-09-28
// ============================================================

package controller

import (
	"reflect"
	"strings"
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// TestVendorDriverDecisionsByVendor 는 renderDriverDaemonSet 이 벤더·모델마다 내는
// 노드 셀렉터·rmmod 명령·Furiosa APT 인증 마운트를 한 표로 고정한다. 미등록 벤더와
// tenstorrent 는 비어 있지만 nil 이 아닌 셀렉터를 받는다(현재 default 동작).
func TestVendorDriverDecisionsByVendor(t *testing.T) {
	cases := []struct {
		name         string
		vendor       string
		model        string
		wantSelector map[string]string
		wantRmmod    string
		wantAuth     bool
	}{
		{"nvidia", "nvidia", "generic",
			map[string]string{"kcloud.ai/nvidia.present": "true"},
			"rmmod nvidia_uvm nvidia_drm nvidia || true", false},
		{"nvidia mixed case", "NVIDIA", "generic",
			map[string]string{"kcloud.ai/nvidia.present": "true"},
			"rmmod nvidia_uvm nvidia_drm nvidia || true", false},
		{"furiosa warboy", "furiosa", "warboy",
			map[string]string{"kcloud.ai/furiosa.present": "true"},
			"rmmod npu_pdma npu_mgmt || true", true},
		{"furiosa empty model = warboy", "furiosa", "",
			map[string]string{"kcloud.ai/furiosa.present": "true"},
			"rmmod npu_pdma npu_mgmt || true", true},
		{"furiosa rngd", "furiosa", "rngd",
			map[string]string{"kcloud.ai/rngd.present": "true"},
			"rmmod furiosa_rngd || true", false},
		{"furiosa rngd mixed case", "Furiosa", "RNGD",
			map[string]string{"kcloud.ai/rngd.present": "true"},
			"rmmod furiosa_rngd || true", false},
		{"rebellions host-managed", "rebellions", "",
			map[string]string{"kcloud.ai/rebellions.present": "true"},
			"true", false},
		{"tenstorrent empty selector", "tenstorrent", "",
			map[string]string{}, "true", false},
		{"unknown vendor empty selector", "acme", "",
			map[string]string{}, "true", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := renderDriverDaemonSet(renderTestPolicy(tc.vendor, tc.model, "1.0.0"))

			if got := ds.Spec.Template.Spec.NodeSelector; !reflect.DeepEqual(got, tc.wantSelector) {
				t.Errorf("NodeSelector = %#v, want %#v", got, tc.wantSelector)
			}

			cmd := strings.Join(ds.Spec.Template.Spec.Containers[0].Lifecycle.PreStop.Exec.Command, " ")
			// rmmod 는 "driver-ready; <명령>'" 로 박히므로 양쪽을 앵커로 정확히 특정한다.
			if anchor := "driver-ready; " + tc.wantRmmod + "'"; !strings.Contains(cmd, anchor) {
				t.Errorf("PreStop 에 %q 없음: %q", anchor, cmd)
			}

			if got := hasFuriosaAuthMount(ds); got != tc.wantAuth {
				t.Errorf("furiosa-auth 마운트 = %v, want %v", got, tc.wantAuth)
			}
		})
	}
}

// TestVendorRuntimeClassByVendor 는 renderDeployment 이 벤더마다 박는 runtimeClass 를
// 고정한다. NVIDIA 만 "nvidia" 를 받고, 판정은 대소문자를 구분한다("NVIDIA" 는 nil).
func TestVendorRuntimeClassByVendor(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		want   string // "" 이면 runtimeClass nil 기대
	}{
		{"nvidia gets runtimeClass", "nvidia", "nvidia"},
		{"nvidia mixed case is case-sensitive", "NVIDIA", ""},
		{"furiosa none", "furiosa", ""},
		{"rebellions none", "rebellions", ""},
		{"tenstorrent none", "tenstorrent", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &intent.Result{
				Vendor: tc.vendor, Mode: npuv1alpha1.AccessModeExclusive,
				ResourceName: "example.com/dev", Quantity: 1,
			}
			rc := renderDeployment(awFixture(), res).Spec.Template.Spec.RuntimeClassName
			switch {
			case tc.want == "" && rc != nil:
				t.Errorf("runtimeClass = %q, want nil", *rc)
			case tc.want != "" && (rc == nil || *rc != tc.want):
				t.Errorf("runtimeClass = %v, want %q", rc, tc.want)
			}
		})
	}
}
