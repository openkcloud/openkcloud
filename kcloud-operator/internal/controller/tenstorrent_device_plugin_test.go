// ============================================================
// tenstorrent_device_plugin_test.go: Tenstorrent Blackhole device plugin 단위 테스트
// 상세: ensureTenstorrentDevicePlugin() DS 생성/기본 이미지/nodeSelector override/
//       TT_RESOURCE_NAME env 주입을 fake client 로 검증 (envtest 불필요)
// 생성일: 2026-07-15
// ============================================================

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

// newNPUReconciler은 fake client 기반 NPUClusterPolicyReconciler를 생성한다.
func newNPUReconciler(objs ...client.Object) *NPUClusterPolicyReconciler {
	s := newTestScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		Build()
	return &NPUClusterPolicyReconciler{Client: c, Scheme: s}
}

// makePolicy는 지정한 spec으로 NPUClusterPolicy 객체를 생성한다.
func makePolicy(name string, spec npuv1alpha1.NPUClusterPolicySpec) *npuv1alpha1.NPUClusterPolicy {
	return &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
}

// TestTenstorrentDevicePlugin_DSCreated는 Tenstorrent.Enabled=true 시 DaemonSet이 생성되는지 검증한다.
func TestTenstorrentDevicePlugin_DSCreated(t *testing.T) {
	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Tenstorrent: npuv1alpha1.TenstorrentSpec{
			Enabled: true,
		},
	})

	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureTenstorrentDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureTenstorrentDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		t.Fatalf("DaemonSet 생성 실패: %v", err)
	}

	if ds.Name != ttDaemonSetName {
		t.Errorf("DaemonSet.Name: got %q, want %q", ds.Name, ttDaemonSetName)
	}
	if ds.Namespace != naming.OperatorNamespace() {
		t.Errorf("DaemonSet.Namespace: got %q, want %q", ds.Namespace, naming.OperatorNamespace())
	}
}

// TestTenstorrentDevicePlugin_DefaultImage는 DevicePluginImage 미지정 시 기본 이미지가 사용되는지 검증한다.
func TestTenstorrentDevicePlugin_DefaultImage(t *testing.T) {
	policy := makePolicy("test-policy-img", npuv1alpha1.NPUClusterPolicySpec{
		Tenstorrent: npuv1alpha1.TenstorrentSpec{
			Enabled: true,
			// DevicePluginImage 미지정 → ttImageDefault 사용
		},
	})

	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureTenstorrentDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureTenstorrentDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		t.Fatalf("DaemonSet 조회 실패: %v", err)
	}

	containers := ds.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("컨테이너가 없음")
	}
	if containers[0].Image != ttImageDefault {
		t.Errorf("이미지: got %q, want %q", containers[0].Image, ttImageDefault)
	}
}

// TestTenstorrentDevicePlugin_CustomNodeSelector는 NodeSelector 지정 시 기본값을 대체하는지 검증한다.
func TestTenstorrentDevicePlugin_CustomNodeSelector(t *testing.T) {
	customSel := map[string]string{"kubernetes.io/arch": "amd64", "tt-bh": "true"}
	policy := makePolicy("test-policy-sel", npuv1alpha1.NPUClusterPolicySpec{
		Tenstorrent: npuv1alpha1.TenstorrentSpec{
			Enabled:      true,
			NodeSelector: customSel,
		},
	})

	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureTenstorrentDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureTenstorrentDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		t.Fatalf("DaemonSet 조회 실패: %v", err)
	}

	sel := ds.Spec.Template.Spec.NodeSelector
	for k, v := range customSel {
		if sel[k] != v {
			t.Errorf("NodeSelector[%q]: got %q, want %q", k, sel[k], v)
		}
	}
	// 기본값 키("tenstorrent-blackhole")가 없어야 함
	if _, ok := sel["tenstorrent-blackhole"]; ok {
		t.Error("custom nodeSelector 지정 시 기본 nodeSelector 키가 남아있으면 안 됨")
	}
}

// TestTenstorrentDevicePlugin_DefaultResourceName은 ResourceName 미지정 시 기본 리소스명이 env에 주입되는지 검증한다.
func TestTenstorrentDevicePlugin_DefaultResourceName(t *testing.T) {
	policy := makePolicy("test-policy-res", npuv1alpha1.NPUClusterPolicySpec{
		Tenstorrent: npuv1alpha1.TenstorrentSpec{
			Enabled: true,
			// ResourceName 미지정 → ttResourceDefault 사용
		},
	})

	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureTenstorrentDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureTenstorrentDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		t.Fatalf("DaemonSet 조회 실패: %v", err)
	}

	containers := ds.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatal("컨테이너가 없음")
	}

	var ttResName string
	for _, env := range containers[0].Env {
		if env.Name == "TT_RESOURCE_NAME" {
			ttResName = env.Value
			break
		}
	}
	if ttResName != ttResourceDefault {
		t.Errorf("TT_RESOURCE_NAME env: got %q, want %q", ttResName, ttResourceDefault)
	}
}
