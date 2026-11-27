// ============================================================
// chart_vendor_registry_test.go: 벤더 이미지가 global.vendorRegistry 축을 타는지 고정한다.
// 상세: global.registry 는 우리가 빌드하는 이미지, global.vendorRegistry 는 3rd-party
// 이미지다. 두 축이 섞이면 실재하지 않는 경로가 렌더돼 ImagePullBackOff 로만 드러난다.
// 렌더 결과 문자열만 보고, 그 경로에 이미지가 실재하는지는 네트워크를 타지 않아 보지 않는다.
// 생성일: 2026-09-09 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"os/exec"
	"strings"
	"testing"
)

// 주의: 이 시험은 os/exec 로 helm 을 부른다. Go 테스트 캐시는 그 의존을 보지 못하므로
// 차트만 고치고 손으로 `go test` 를 돌리면 직전 결과가 재사용돼 초록이 나온다 — 차트가
// 깨져 있어도 그렇다. 손으로 돌릴 때는 `-count=1` 을 붙일 것. `make test` 는
// `-coverprofile` 을 쓰고 그 플래그가 캐시를 끄므로 영향이 없다.
// helmTemplate 는 deploy/helm 을 렌더한다. helm 부재 판정은 helm_presence_test.go 의
// helmBinary 하나다 — 로컬에서는 건너뛰고 CI 에서는 하드 실패한다.
func helmTemplate(t *testing.T, args ...string) string {
	t.Helper()
	bin := helmBinary(t)
	out, err := exec.Command(bin, append([]string{"template", "t", "../../deploy/helm"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template 실패: %v\n%s", err, out)
	}
	return string(out)
}

// vendorImageCases 는 벤더 이미지마다 (기본 렌더 경로, 미러 렌더 경로) 쌍이다.
// 미러 경로는 upstream 의 org 경로를 그대로 살린다 — 그 규약을 깨면 이미 미러를
// 채워 둔 클러스터가 다음 upgrade 에서 없는 경로를 당긴다.
var vendorImageCases = []struct {
	name     string
	upstream string
	mirrored string
}{
	{"MPS control daemon", "nvcr.io/nvidia/k8s-device-plugin:v0.19.3", "MIRROR/nvidia/k8s-device-plugin:v0.19.3"},
	{"pre-upgrade kubectl", "docker.io/bitnamilegacy/kubectl:1.28", "MIRROR/bitnamilegacy/kubectl:1.28"},
	{"NVIDIA DRA 드라이버", "registry.k8s.io/dra-driver-nvidia/dra-driver-nvidia-gpu:v0.4.0", "MIRROR/dra-driver-nvidia/dra-driver-nvidia-gpu:v0.4.0"},
	{"RNGD device-plugin", "docker.io/furiosaai/furiosa-device-plugin:2026.1.1", "MIRROR/furiosaai/furiosa-device-plugin:2026.1.1"},
	{"RNGD DRA 드라이버", "docker.io/furiosaai/furiosa-dra-driver:2026.1.1", "MIRROR/furiosaai/furiosa-dra-driver:2026.1.1"},
	{"Rebellions device-plugin", "docker.io/rebellions/k8s-device-plugin:v0.3.6", "MIRROR/rebellions/k8s-device-plugin:v0.3.6"},
	{"Rebellions DRA 드라이버", "docker.io/rebellions/k8s-dra-driver-npu:v0.2.0", "MIRROR/rebellions/k8s-dra-driver-npu:v0.2.0"},
	{"Tenstorrent DRA 드라이버", "ghcr.io/tenstorrent/tt-dra-driver:0.0.48", "MIRROR/tenstorrent/tt-dra-driver:0.0.48"},
	{"Tenstorrent Fabric Manager", "ghcr.io/tenstorrent/tt-fabric-manager:latest", "MIRROR/tenstorrent/tt-fabric-manager:latest"},
}

// TestVendorImagesUseUpstreamWhenVendorRegistryEmpty 는 vendorRegistry 가 비었을 때
// 벤더 이미지가 각자의 upstream 에서 렌더되는지 본다. 공개 배포의 기본 상태다.
func TestVendorImagesUseUpstreamWhenVendorRegistryEmpty(t *testing.T) {
	got := helmTemplate(t)
	for _, c := range vendorImageCases {
		if !strings.Contains(got, c.upstream) {
			t.Errorf("%s: 기본 렌더에 %q 가 없다 — global.registry 축으로 새지 않았는지 볼 것", c.name, c.upstream)
		}
	}
	// 우리 이미지는 반대로 global.registry 를 타야 한다.
	if !strings.Contains(got, "ghcr.io/openkcloud/kcloud-operator:") {
		t.Error("기본 렌더에 ghcr.io/openkcloud/kcloud-operator 가 없다")
	}
	// 벤더 이미지가 우리 레지스트리 아래로 조립되면 존재하지 않는 경로가 된다.
	for _, bad := range []string{
		"ghcr.io/openkcloud/furiosaai/",
		"ghcr.io/openkcloud/rebellions/k8s-device-plugin",
		"ghcr.io/openkcloud/bitnami",
		"ghcr.io/openkcloud/dra-driver-nvidia/",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("벤더 이미지가 우리 레지스트리로 조립됐다: %q", bad)
		}
	}
}

// TestVendorImagesUseVendorRegistryWhenSet 는 vendorRegistry 를 채웠을 때 벤더
// 이미지만 그쪽으로 옮겨가고 우리 이미지는 global.registry 에 남는지 본다.
func TestVendorImagesUseVendorRegistryWhenSet(t *testing.T) {
	got := helmTemplate(t, "--set", "global.registry=OURS", "--set", "global.vendorRegistry=MIRROR")
	for _, c := range vendorImageCases {
		if !strings.Contains(got, c.mirrored) {
			t.Errorf("%s: 미러 렌더에 %q 가 없다", c.name, c.mirrored)
		}
	}
	for _, ours := range []string{
		"OURS/kcloud-operator:",
		"OURS/kcloud-node-manager:",
		"OURS/kcloud-host-exec:",
		"OURS/nvidia-driver-ds:",
		"OURS/kcloud-tt-device-plugin:",
	} {
		if !strings.Contains(got, ours) {
			t.Errorf("우리 이미지가 global.registry 를 안 탔다: %q", ours)
		}
	}
	if strings.Contains(got, "MIRROR/kcloud-operator:") {
		t.Error("우리 이미지가 vendorRegistry 로 새어 나갔다")
	}
}
