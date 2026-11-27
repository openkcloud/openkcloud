// ============================================================
// imagepull_test.go: imagePullSecrets 전파 + imagePullPolicy 불변식 회귀 가드
// 상세: 자식 DS(detector/driver) 빌더가 policy.Spec.ImagePullSecrets 를 pod spec 에
// 전파하고, air-gap 프리로드용 imagePullPolicy=IfNotPresent 불변식을 건드리지 않는지 검증한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// assertAllContainersIfNotPresent 는 (init 포함) 모든 컨테이너의 ImagePullPolicy 가
// IfNotPresent 인지 검증한다. Always 로의 회귀는 노드 프리로드(air-gap) 경로를 깨뜨린다.
func assertAllContainersIfNotPresent(t *testing.T, name string, spec corev1.PodSpec) {
	t.Helper()
	all := append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...)
	if len(all) == 0 {
		t.Fatalf("%s: 컨테이너가 0개 — 빌더 출력이 비정상", name)
	}
	for _, c := range all {
		if c.ImagePullPolicy != corev1.PullIfNotPresent {
			t.Errorf("%s: container %q ImagePullPolicy=%q, 기대 IfNotPresent (air-gap 불변식 위반)",
				name, c.Name, c.ImagePullPolicy)
		}
	}
}

// TestChildBuilders_ImagePullPolicyIfNotPresent 는 detector/driver DS 빌더의
// 전 컨테이너가 IfNotPresent 를 유지하는지 확인하는 회귀 가드다.
func TestChildBuilders_ImagePullPolicyIfNotPresent(t *testing.T) {
	detector := renderDetectorDS("registry.example/npu-detector:test", nil)
	assertAllContainersIfNotPresent(t, "detector", detector.Spec.Template.Spec)

	driver := renderDriverDaemonSet(renderTestPolicy("nvidia", "generic", "590.48.01"))
	assertAllContainersIfNotPresent(t, "driver", driver.Spec.Template.Spec)
}

// TestImagePullSecrets_DetectorPropagation 는 detector DS 빌더가 pull secret 을
// pod spec 에 전파하고, 미지정 시 nil(하위호환)을 유지하는지 검증한다.
func TestImagePullSecrets_DetectorPropagation(t *testing.T) {
	secrets := []corev1.LocalObjectReference{{Name: "harbor-pull"}}

	withSecrets := renderDetectorDS("registry.example/npu-detector:test", secrets)
	got := withSecrets.Spec.Template.Spec.ImagePullSecrets
	if len(got) != 1 || got[0].Name != "harbor-pull" {
		t.Errorf("detector imagePullSecrets 전파 실패: %+v", got)
	}

	withoutSecrets := renderDetectorDS("registry.example/npu-detector:test", nil)
	if withoutSecrets.Spec.Template.Spec.ImagePullSecrets != nil {
		t.Errorf("detector imagePullSecrets 미지정 시 nil 이어야 함(하위호환): %+v",
			withoutSecrets.Spec.Template.Spec.ImagePullSecrets)
	}
}

// TestImagePullSecrets_DriverPropagation 는 driver DS 빌더가 pull secret 을
// pod spec 에 전파하고, 미지정 시 nil(하위호환)을 유지하는지 검증한다.
func TestImagePullSecrets_DriverPropagation(t *testing.T) {
	pol := renderTestPolicy("nvidia", "generic", "590.48.01")
	pol.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "harbor-pull"}}

	withSecrets := renderDriverDaemonSet(pol)
	got := withSecrets.Spec.Template.Spec.ImagePullSecrets
	if len(got) != 1 || got[0].Name != "harbor-pull" {
		t.Errorf("driver imagePullSecrets 전파 실패: %+v", got)
	}

	bare := renderDriverDaemonSet(renderTestPolicy("nvidia", "generic", "590.48.01"))
	if bare.Spec.Template.Spec.ImagePullSecrets != nil {
		t.Errorf("driver imagePullSecrets 미지정 시 nil 이어야 함(하위호환): %+v",
			bare.Spec.Template.Spec.ImagePullSecrets)
	}
}
