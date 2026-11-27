// ============================================================
// furiosa_unified_device_plugin_test.go: Furiosa 통합 device-plugin(A') 단위 테스트
// 상세: ensureFuriosaUnifiedDevicePlugin() 의 단일 DS 렌더(공통 PCI nodeSelector·기본 이미지·
//       RNGD_PARTITION_POLICY env·config 마운트)와 Unified 토글 전환/롤백 시 반대편 DS 정리를
//       fake client 로 검증 (envtest 불필요)
// 생성일: 2026-07-20 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/rngd"
)

// TestFuriosaUnified_DSCreated는 통합 DS 가 공통 PCI nodeSelector·기본 이미지·정책 env 로 생성되는지 검증한다.
func TestFuriosaUnified_DSCreated(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled:       true,
			Unified:       true,
			ConfigMapName: "npu-device-plugin",
			Rngd:          npuv1alpha1.RngdSpec{PartitionPolicy: "dual-core"},
		},
	})

	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedDSName, Namespace: furiosaUnifiedDSNamespace()}, &ds); err != nil {
		t.Fatalf("통합 DaemonSet 생성 실패: %v", err)
	}

	sel := ds.Spec.Template.Spec.NodeSelector
	if sel[furiosaFamilyNodeLabel] != labelValueTrue {
		t.Errorf("nodeSelector: got %v, want %s=true", sel, furiosaFamilyNodeLabel)
	}

	c := ds.Spec.Template.Spec.Containers[0]
	if c.Image != furiosaUnifiedImgDefault {
		t.Errorf("Image: got %q, want default %q", c.Image, furiosaUnifiedImgDefault)
	}
	if len(c.Command) != 0 {
		t.Errorf("Command 는 비어야 함(이미지 ENTRYPOINT): got %v", c.Command)
	}
	var policyEnv string
	for _, e := range c.Env {
		if e.Name == testPartitionPolicyEnv {
			policyEnv = e.Value
		}
	}
	if policyEnv != "dual-core" {
		t.Errorf("RNGD_PARTITION_POLICY env: got %q, want dual-core", policyEnv)
	}

	// ConfigMapName 지정 시 /etc/furiosa 마운트가 존재해야 한다(Warboy config).
	var hasConfig bool
	for _, vm := range c.VolumeMounts {
		if vm.MountPath == "/etc/furiosa" {
			hasConfig = true
		}
	}
	if !hasConfig {
		t.Errorf("config 마운트(/etc/furiosa) 누락: %v", c.VolumeMounts)
	}
}

// TestFuriosaUnified_DefaultImage는 UnifiedDevicePluginImage 미지정 시 기본 이미지가 사용되는지 검증한다.
func TestFuriosaUnified_OverrideImage(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	const custom = "registry.example.com:5000/kcloud/furiosa-unified-device-plugin:0.2.0"
	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled:                  true,
			Unified:                  true,
			UnifiedDevicePluginImage: custom,
		},
	})
	r := newNPUReconciler(policy)
	ctx := context.Background()
	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedDSName, Namespace: furiosaUnifiedDSNamespace()}, &ds); err != nil {
		t.Fatalf("통합 DaemonSet 생성 실패: %v", err)
	}
	if got := ds.Spec.Template.Spec.Containers[0].Image; got != custom {
		t.Errorf("Image override: got %q, want %q", got, custom)
	}
}

// TestFuriosaUnified_PreservesPartitionEnvWhenACPPOwns는 live DS 가 ACPP owner 어노테이션을
// 가질 때 NPUClusterPolicy 가 RNGD_PARTITION_POLICY 를 덮어쓰지 않고 live 값을 보존하는지 검증한다
// (두 writer 조정).
func TestFuriosaUnified_PreservesPartitionEnvWhenACPPOwns(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	existing := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        furiosaUnifiedDSName,
			Namespace:   furiosaUnifiedDSNamespace(),
			Annotations: map[string]string{rngd.PartitionOwnerAnnotation: "acpp-uid-1"},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": furiosaUnifiedDSName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": furiosaUnifiedDSName}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "furiosa-device-plugin",
						Env:  []corev1.EnvVar{{Name: testPartitionPolicyEnv, Value: "quad-core"}},
					}},
				},
			},
		},
	}

	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled: true,
			Unified: true,
			Rngd:    npuv1alpha1.RngdSpec{PartitionPolicy: "dual-core"},
		},
	})

	r := newNPUReconciler(policy, existing)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedDSName, Namespace: furiosaUnifiedDSNamespace()}, &ds); err != nil {
		t.Fatalf("통합 DaemonSet 조회 실패: %v", err)
	}

	got := ""
	for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
		if e.Name == testPartitionPolicyEnv {
			got = e.Value
		}
	}
	if got != "quad-core" {
		t.Errorf("RNGD_PARTITION_POLICY: got %q, want preserved %q (ACPP owns)", got, "quad-core")
	}
	if ds.Annotations[rngd.PartitionOwnerAnnotation] != "acpp-uid-1" {
		t.Errorf("owner annotation not carried forward: got %v", ds.Annotations)
	}
}

// TestFuriosaUnified_OverwritesPartitionEnvWithoutOwner는 owner 어노테이션이 없는 기존 DS 는
// 종전대로 NPUClusterPolicy 값으로 덮이는지 검증한다(회귀 방지).
func TestFuriosaUnified_OverwritesPartitionEnvWithoutOwner(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	existing := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      furiosaUnifiedDSName,
			Namespace: furiosaUnifiedDSNamespace(),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": furiosaUnifiedDSName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app.kubernetes.io/name": furiosaUnifiedDSName}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "furiosa-device-plugin",
						Env:  []corev1.EnvVar{{Name: testPartitionPolicyEnv, Value: "quad-core"}},
					}},
				},
			},
		},
	}

	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled: true,
			Unified: true,
			Rngd:    npuv1alpha1.RngdSpec{PartitionPolicy: "dual-core"},
		},
	})

	r := newNPUReconciler(policy, existing)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedDSName, Namespace: furiosaUnifiedDSNamespace()}, &ds); err != nil {
		t.Fatalf("통합 DaemonSet 조회 실패: %v", err)
	}

	got := ""
	for _, e := range ds.Spec.Template.Spec.Containers[0].Env {
		if e.Name == testPartitionPolicyEnv {
			got = e.Value
		}
	}
	if got != "dual-core" {
		t.Errorf("RNGD_PARTITION_POLICY: got %q, want overwritten %q (no owner)", got, "dual-core")
	}
}

// TestFuriosaUnified_LegacyObjectRemoved 는 옛 자리(kube-system)의 통합 DS 가
// 새 DS 를 만들 때 함께 지워지는지 검증한다. 둘이 공존하면 같은 자원을 두 번 등록한다.
func TestFuriosaUnified_LegacyObjectRemoved(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	legacy := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "furiosa-unified-device-plugin",
			Namespace: "kube-system",
		},
	}
	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled: true, Unified: true, ConfigMapName: "npu-device-plugin",
		},
	})
	r := newNPUReconciler(policy, legacy)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var old appsv1.DaemonSet
	err := r.Get(ctx, types.NamespacedName{
		Name: "furiosa-unified-device-plugin", Namespace: "kube-system"}, &old)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("옛 DaemonSet 이 남아 있다: err=%v", err)
	}
}

// TestDeleteDaemonSetIn 은 존재/부재 DS 이름 삭제가 멱등적으로 동작하는지 검증한다.
// deleteDaemonSetIn 은 kcloud 전용 이름(통합 DS·개명 전 kcloud- 이름)에만 쓴다 — 벤더 일반
// 이름은 소유권 검사가 있는 deleteOwnedDaemonSet* 로 지운다.
func TestDeleteDaemonSetIn(t *testing.T) {
	existing := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: furiosaUnifiedLegacyDSName, Namespace: "kube-system"},
	}
	r := newNPUReconciler(existing)
	ctx := context.Background()

	// 존재하는 DS 삭제 성공
	if err := r.deleteDaemonSetIn(ctx, "kube-system", furiosaUnifiedLegacyDSName); err != nil {
		t.Fatalf("존재 DS 삭제 오류: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedLegacyDSName, Namespace: "kube-system"}, &ds); err == nil {
		t.Errorf("DS 가 삭제되지 않음")
	}

	// 부재 DS 삭제는 NotFound 무시(멱등)
	if err := r.deleteDaemonSetIn(ctx, "kube-system", "no-such-ds"); err != nil {
		t.Errorf("부재 DS 삭제는 nil 이어야 함: %v", err)
	}
}

// TestFuriosaUnified_KcloudNamespace 는 통합 DS 가 operator 네임스페이스에
// 자체 구현 접두사 이름으로 생성되는지 검증한다.
func TestFuriosaUnified_KcloudNamespace(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled:       true,
			Unified:       true,
			ConfigMapName: "npu-device-plugin",
			Rngd:          npuv1alpha1.RngdSpec{PartitionPolicy: "dual-core"},
		},
	})
	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var ds appsv1.DaemonSet
	key := types.NamespacedName{Name: "kcloud-furiosa-device-plugin", Namespace: "kcloud"}
	if err := r.Get(ctx, key, &ds); err != nil {
		t.Fatalf("kcloud 네임스페이스에 통합 DaemonSet 없음: %v", err)
	}
	if ds.Labels["app.kubernetes.io/name"] != "kcloud-furiosa-device-plugin" {
		t.Errorf("이름 라벨: got %q", ds.Labels["app.kubernetes.io/name"])
	}
}

// TestFuriosaUnified_ConfigMapFollowsDSNamespace 는 Warboy config ConfigMap 이 통합 DS 와
// 같은 네임스페이스(kcloud)에 생성되는지 검증한다. 파드는 자기 네임스페이스의 ConfigMap 만
// 마운트할 수 있어, 어긋나면 ContainerCreating 에 고정된다.
func TestFuriosaUnified_ConfigMapFollowsDSNamespace(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled:       true,
			Unified:       true,
			ConfigMapName: "npu-device-plugin",
		},
	})
	r := newNPUReconciler(policy)
	ctx := context.Background()

	if err := r.ensureFuriosaUnifiedDevicePlugin(ctx, policy); err != nil {
		t.Fatalf("ensureFuriosaUnifiedDevicePlugin 오류: %v", err)
	}

	var cm corev1.ConfigMap
	key := types.NamespacedName{Name: "npu-device-plugin", Namespace: furiosaUnifiedDSNamespace()}
	if err := r.Get(ctx, key, &cm); err != nil {
		t.Fatalf("kcloud 네임스페이스에 ConfigMap 없음: %v", err)
	}

	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: furiosaUnifiedDSName, Namespace: furiosaUnifiedDSNamespace()}, &ds); err != nil {
		t.Fatalf("통합 DaemonSet 조회 실패: %v", err)
	}
	var mounted string
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "config" && v.ConfigMap != nil {
			mounted = v.ConfigMap.Name
		}
	}
	if mounted != cm.Name {
		t.Errorf("DS volume 이 참조하는 ConfigMap: got %q, want %q", mounted, cm.Name)
	}
}

// TestFuriosaUnified_RollbackDeletesConfigMap 는 Unified=false 로 되돌릴 때(전체 Reconcile 의
// 롤백 분기) 통합 경로가 남긴 ConfigMap 이 고아로 남지 않는지 검증한다.
// 모델별 Warboy 경로의 동명 ConfigMap 은 kube-system 에 있어 이 경로가 지우는 대상이 아니므로
// 그대로 남아야 한다.
func TestFuriosaUnified_RollbackDeletesConfigMap(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")

	const cmName = "npu-device-plugin"
	unifiedCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: furiosaUnifiedDSNamespace()},
	}
	legacyCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: "kube-system"},
	}
	policy := makePolicy("test-policy", npuv1alpha1.NPUClusterPolicySpec{
		Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/npu-op-detector:test"},
		Furiosa: npuv1alpha1.FuriosaSpec{
			Enabled:       true,
			Unified:       false, // 롤백 상태
			ConfigMapName: cmName,
		},
	})
	r := newNPUReconciler(policy, unifiedCM, legacyCM)
	r.Recorder = record.NewFakeRecorder(50)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace},
	}); err != nil {
		t.Fatalf("Reconcile 오류: %v", err)
	}

	var gone corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: furiosaUnifiedDSNamespace()}, &gone)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("통합 경로 ConfigMap(kcloud)이 롤백 후에도 남아 있다: err=%v", err)
	}

	var kept corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: "kube-system"}, &kept); err != nil {
		t.Fatalf("kube-system 의 레거시 ConfigMap 이 잘못 지워졌다: err=%v", err)
	}
}

// 모델별 Furiosa device-plugin DS 는 벤더 공개 이미지를 그대로 실행하므로 kube-system 에
// 벤더식 이름으로 만들어진다. 옛 두 세대의 이름(v0.7.26 의 kube-system 이름, v0.7.27 한 회차의
// operator 네임스페이스 kcloud- 이름)은 새 DS 를 만들기 전에 지워져야 한다.
// 둘이 함께 살아 있으면 두 파드가 같은 자원을 kubelet 에 중복 등록한다.
func TestFuriosaPerModelDS_VendorNamespaceAndLegacyRemoved(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy []types.NamespacedName
		want   string
		ensure func(r *NPUClusterPolicyReconciler, ctx context.Context, p *npuv1alpha1.NPUClusterPolicy) error
		spec   npuv1alpha1.NPUClusterPolicySpec
	}{
		{
			name: "warboy",
			legacy: []types.NamespacedName{
				{Namespace: "kube-system", Name: furiosaWarboyLegacyDSName},
				{Namespace: "kcloud", Name: furiosaWarboyV0727DSName},
			},
			want: furiosaWarboyDSName,
			ensure: func(r *NPUClusterPolicyReconciler, ctx context.Context, p *npuv1alpha1.NPUClusterPolicy) error {
				return r.ensureFuriosaDevicePlugin(ctx, p)
			},
			spec: npuv1alpha1.NPUClusterPolicySpec{
				Furiosa: npuv1alpha1.FuriosaSpec{Enabled: true, DevicePluginImage: "img:1", ConfigMapName: "npu-device-plugin"},
			},
		},
		{
			name:   "rngd",
			legacy: []types.NamespacedName{{Namespace: "kcloud", Name: furiosaRngdV0727DSName}},
			want:   furiosaRngdDSName,
			ensure: func(r *NPUClusterPolicyReconciler, ctx context.Context, p *npuv1alpha1.NPUClusterPolicy) error {
				return r.ensureFuriosaRngdDevicePlugin(ctx, p, "")
			},
			spec: npuv1alpha1.NPUClusterPolicySpec{
				Furiosa: npuv1alpha1.FuriosaSpec{
					Rngd: npuv1alpha1.RngdSpec{Enabled: true, DevicePluginImage: "img:1"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// operator 네임스페이스를 kube-system 과 다르게 세워야 자리가 실제로 검사된다.
			t.Setenv("OPERATOR_NAMESPACE", "kcloud")
			var objs []client.Object
			for _, ref := range tc.legacy {
				// kcloud 가 만든 옛 DS 는 owner annotation 을 갖는다.
				// 개명 정리는 owner 일치만 지우므로, 옛 DS 에 이 policy 의 소유 표시를 달아 둔다.
				objs = append(objs, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{
					Name: ref.Name, Namespace: ref.Namespace,
					Annotations: map[string]string{ownerAnnotation: "default/test-policy"},
				}})
			}
			r := newNPUReconciler(objs...)
			ctx := context.Background()
			policy := makePolicy("test-policy", tc.spec)

			if err := tc.ensure(r, ctx, policy); err != nil {
				t.Fatalf("ensure 실패: %v", err)
			}

			var ds appsv1.DaemonSet
			key := types.NamespacedName{Name: tc.want, Namespace: "kube-system"}
			if err := r.Get(ctx, key, &ds); err != nil {
				t.Fatalf("kube-system 에 벤더식 이름 DS %q 가 없다: %v", tc.want, err)
			}
			if got := ds.Spec.Selector.MatchLabels["app.kubernetes.io/name"]; got != tc.want {
				t.Errorf("selector 라벨 %q, want %q", got, tc.want)
			}
			// device-plugin 컨테이너 이름은 옛 이름 그대로여야 한다 — ACPP 가 이름으로 집는다.
			if got := ds.Spec.Template.Spec.Containers[0].Name; got != furiosaPluginContainerName {
				t.Errorf("컨테이너 이름 %q, want %q", got, furiosaPluginContainerName)
			}
			var operatorNS appsv1.DaemonSet
			if err := r.Get(ctx, types.NamespacedName{Name: tc.want, Namespace: "kcloud"}, &operatorNS); err == nil {
				t.Errorf("벤더 DS %q 가 operator 네임스페이스에 만들어졌다", tc.want)
			}
			for _, ref := range tc.legacy {
				var gone appsv1.DaemonSet
				if err := r.Get(ctx, ref, &gone); err == nil {
					t.Errorf("옛 DS %s/%s 가 남아 있다", ref.Namespace, ref.Name)
				}
			}
			if tc.name == "warboy" {
				var cm corev1.ConfigMap
				if err := r.Get(ctx, types.NamespacedName{Name: "npu-device-plugin", Namespace: "kube-system"}, &cm); err != nil {
					t.Errorf("Warboy ConfigMap 이 kube-system 에 없다: %v", err)
				}
			}
		})
	}
}
