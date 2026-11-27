/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/partition/nvidia"
)

var _ = Describe("NPUClusterPolicy Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		npuclusterpolicy := &npuv1alpha1.NPUClusterPolicy{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind NPUClusterPolicy")
			err := k8sClient.Get(ctx, typeNamespacedName, npuclusterpolicy)
			if err != nil && errors.IsNotFound(err) {
				resource := &npuv1alpha1.NPUClusterPolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: npuv1alpha1.NPUClusterPolicySpec{
						// detector.image 는 ensureDetector 의 필수 필드 — 미지정 시
						// reconcile 이 에러 경로로 진입한다. 벤더(nvidia/furiosa/rebellions)는
						// 기본 Enabled=false 라 reconcile 에서 skip 되어 성공 경로에 도달한다.
						Detector: &npuv1alpha1.DetectorSpec{
							Image: "registry.example.com/npu-op-detector:test",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &npuv1alpha1.NPUClusterPolicy{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance NPUClusterPolicy")
			deleteNCPAndWait(resource.Name, resource.Namespace)
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &NPUClusterPolicyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				// Recorder 주입 — 미설정 시 reconcile 의 Eventf 호출에서 nil deref panic.
				Recorder: record.NewFakeRecorder(100),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

var _ = Describe("ensureNvidiaDevicePlugin renders mixed and flat DaemonSets", func() {
	It("gives the flat DS an affinity that excludes mig-active nodes and no --mig-strategy flag", func() {
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-split-test"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{DevicePluginImage: "img:v1"},
			},
		}
		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		Expect(r.ensureNvidiaDevicePlugin(ctx, policy)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"}})
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin-flat", Namespace: "kube-system"}})
		})

		var mixed appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin", Namespace: "kube-system"}, &mixed)).To(Succeed())
		Expect(mixed.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--mig-strategy=mixed"))
		Expect(mixed.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue(nvidia.MigActiveNodeLabel, "true"))

		var flat appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin-flat", Namespace: "kube-system"}, &flat)).To(Succeed())
		Expect(flat.Spec.Template.Spec.Containers[0].Args).NotTo(ContainElement("--mig-strategy=mixed"))
		Expect(flat.Spec.Template.Spec.Affinity).NotTo(BeNil())
		terms := flat.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
		found := false
		for _, term := range terms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == nvidia.MigActiveNodeLabel && expr.Operator == corev1.NodeSelectorOpDoesNotExist {
					found = true
				}
			}
		}
		Expect(found).To(BeTrue(), "flat DS must exclude mig-active nodes via DoesNotExist affinity")

		// 둘 다 restartNvidiaDevicePlugin 이 찾는 공통 라벨(kcloud.ai/dp-vendor)을 공유한다.
		Expect(mixed.Spec.Template.Labels).To(HaveKeyWithValue(nvidiaDevicePluginVendorLabel, "nvidia"))
		Expect(flat.Spec.Template.Labels).To(HaveKeyWithValue(nvidiaDevicePluginVendorLabel, "nvidia"))

		// mixed 의 selector 는 기존 라이브 값 그대로여야 한다 — 바뀌면 기존 클러스터에서
		// immutable 필드라 422 로 영구 거부된다.
		Expect(mixed.Spec.Selector.MatchLabels).To(Equal(map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}))

		// 두 DS 의 selector 는 서로의 template label 과 매치되면 안 된다(pod 을 서로 훔치지 않음).
		mixedSel, err := metav1.LabelSelectorAsSelector(mixed.Spec.Selector)
		Expect(err).NotTo(HaveOccurred())
		flatSel, err := metav1.LabelSelectorAsSelector(flat.Spec.Selector)
		Expect(err).NotTo(HaveOccurred())
		Expect(mixedSel.Matches(labels.Set(flat.Spec.Template.Labels))).To(BeFalse(), "mixed selector must not match flat pods")
		Expect(flatSel.Matches(labels.Set(mixed.Spec.Template.Labels))).To(BeFalse(), "flat selector must not match mixed pods")
	})

	// 기존 selector 를 가진 mixed DS 가 라이브에 이미 있는 업그레이드 경로다.
	// selector 가 바뀌면 DaemonSet.spec.selector 는 immutable 이라 API 서버가 422 로 영구 거부한다.
	It("succeeds against a pre-existing mixed DS with the old (pre-split) selector", func() {
		oldSelectorLabels := map[string]string{"app.kubernetes.io/name": "nvidia-device-plugin"}
		live := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: oldSelectorLabels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: oldSelectorLabels},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin", Image: "old:v0"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, live)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"}})
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin-flat", Namespace: "kube-system"}})
		})

		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-upgrade-test"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{DevicePluginImage: "img:v1"},
			},
		}
		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		Expect(r.ensureNvidiaDevicePlugin(ctx, policy)).To(Succeed(), "must not fail with a 422 on an upgrade from the pre-split selector")

		var flat appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "nvidia-device-plugin-flat", Namespace: "kube-system"}, &flat)).To(Succeed(),
			"flat DS must still be created even when mixed already existed")
	})
})

// Nvidia.Enabled=false 시 mixed/flat DaemonSet 을 모두 회수한다 — 단, 이 policy 가 owner
// annotation 으로 소유를 인정한 것만. 실제로는 enabled=true 로 한 번이라도 reconcile 되면
// ensureNvidiaDevicePlugin(:418)이 매번 annotation 을 stamp 하므로, 토글 off 시점엔 거의 항상
// owned 상태다 — 그래서 두 DS 모두 owner annotation 을 미리 달아 그 상태를 재현한다.
var _ = Describe("NPUClusterPolicy reconcile with Nvidia.Enabled=false", func() {
	It("removes both the mixed and flat NVIDIA device plugin DaemonSets when owned", func() {
		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-nvidia-disable-test", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/npu-op-detector:test"},
				Nvidia:   npuv1alpha1.NvidiaSpec{Enabled: false, DevicePluginImage: "img:v1"},
			},
		}
		owner := map[string]string{ownerAnnotation: policy.Namespace + "/" + policy.Name}

		selMixed := map[string]string{"app.kubernetes.io/name": nvidia.DevicePluginNameMixed}
		selFlat := map[string]string{"app.kubernetes.io/name": nvidia.DevicePluginNameFlat}
		mixed := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system", Annotations: owner},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: selMixed},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: selMixed},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "d", Image: "old:v0"}}},
				},
			},
		}
		flat := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameFlat, Namespace: "kube-system", Annotations: owner},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: selFlat},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: selFlat},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "d", Image: "old:v0"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, mixed)).To(Succeed())
		Expect(k8sClient.Create(ctx, flat)).To(Succeed())
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		DeferCleanup(func() {
			deleteNCPAndWait(policy.Name, policy.Namespace)
			_ = k8sClient.Delete(ctx, mixed)
			_ = k8sClient.Delete(ctx, flat)
		})

		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(50)}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}})
		Expect(err).NotTo(HaveOccurred())

		for _, name := range []string{nvidia.DevicePluginNameMixed, nvidia.DevicePluginNameFlat} {
			var ds appsv1.DaemonSet
			getErr := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "kube-system"}, &ds)
			Expect(errors.IsNotFound(getErr)).To(BeTrue(), name+" 가 남아 있다 — 토글 off 는 소유한 DS 를 모두 지워야 한다")
		}
	})

	// 이 operator 가 한 번도 reconcile 하지 않은(=owner annotation 없는) DS 는 이름이 같아도
	// 지우면 안 된다 — 흡수 대상 3rd-party DS 보호. 소유권 검사를 걷어내면 이 스펙이 실패로
	// 뒤집힌다(뮤테이션으로 확인).
	It("does not remove a same-named DaemonSet it has never owned", func() {
		selMixed := map[string]string{"app.kubernetes.io/name": nvidia.DevicePluginNameMixed}
		unowned := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: selMixed},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: selMixed},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "d", Image: "3rd-party:v0"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, unowned)).To(Succeed())

		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-nvidia-disable-unowned-test", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/npu-op-detector:test"},
				Nvidia:   npuv1alpha1.NvidiaSpec{Enabled: false, DevicePluginImage: "img:v1"},
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		DeferCleanup(func() {
			deleteNCPAndWait(policy.Name, policy.Namespace)
			_ = k8sClient.Delete(ctx, unowned)
		})

		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(50)}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}})
		Expect(err).NotTo(HaveOccurred())

		var ds appsv1.DaemonSet
		getErr := k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}, &ds)
		Expect(getErr).NotTo(HaveOccurred(), "owner annotation 이 없는 3rd-party DS 를 지웠다 — 흡수 전 리소스가 보호돼야 한다")
	})
})

// TestOwnedScanNamespaces: detector 이동 dual-ns 스캔 고정.
// env 미설정 → kube-system 1개(회귀 0). 설정 → kube-system + kcloud 2개(과도기 orphan 방지).
func TestOwnedScanNamespaces(t *testing.T) {
	t.Setenv("OPERATOR_NAMESPACE", "")
	if got := ownedScanNamespaces(); len(got) != 1 || got[0] != naming.KubeSystemNamespace {
		t.Errorf("env 미설정 시 %v, want [kube-system]", got)
	}
	t.Setenv("OPERATOR_NAMESPACE", "kcloud")
	got := ownedScanNamespaces()
	if len(got) != 2 || got[0] != naming.KubeSystemNamespace || got[1] != "kcloud" {
		t.Errorf("env=kcloud 시 %v, want [kube-system kcloud]", got)
	}
}

// TestIsDevicePluginResource: device-plugin 보존 정책 고정.
// 5종 device-plugin=보존(true), operator 관리(driver/toolkit/detector)=삭제 대상(false).
func TestIsDevicePluginResource(t *testing.T) {
	preserve := []string{"nvidia-device-plugin", "furiosa-device-plugin", "furiosa-rngd-device-plugin",
		"kcloud-tt-device-plugin", "rbln-device-plugin", "rbln-device-plugin-config"}
	for _, n := range preserve {
		if !isDevicePluginResource(n) {
			t.Errorf("%q 는 device-plugin(보존 대상)이어야 함", n)
		}
	}
	del := []string{"kcloud-nvidia-driver", "kcloud-nvidia-toolkit", "kcloud-node-manager", "kcloud-furiosa-warboy-driver"}
	for _, n := range del {
		if isDevicePluginResource(n) {
			t.Errorf("%q 는 operator 관리(삭제 대상)여야 함", n)
		}
	}
}

// TestEnsureNvidiaDevicePluginPreservesSharingConfig: ACPP 가 sharing 을 소유(owner 어노테이션)한 DS 는
// NPUClusterPolicy 재렌더에도 config-file arg/volume/mount 배선과 owner 어노테이션이 살아남아야 한다(2-writer 조정).
func TestEnsureNvidiaDevicePluginPreservesSharingConfig(t *testing.T) {
	live := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "nvidia-device-plugin",
			Namespace:   "kube-system",
			Annotations: map[string]string{nvidia.SharingOwnerAnnotation: "acpp-shared"},
		},
		// live 는 실제 reconcile 산출물을 흉내낸다 — ensureNvidiaDevicePlugin 은 항상 이 컨테이너를 만든다.
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nvidia-device-plugin"}},
		}}},
	}
	nvidia.WireSharing(live, npuv1alpha1.SharingModeTimeSliced, nvidia.SharingConfigMapNameMixed)

	desired := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nvidia-device-plugin", Args: []string{"--mig-strategy=mixed"}}},
		}}},
	}
	preserveNvidiaSharing(live, desired)

	if desired.Annotations[nvidia.SharingOwnerAnnotation] != "acpp-shared" {
		t.Fatalf("owner annotation lost: %v", desired.Annotations)
	}
	var hasArg, hasVol, hasMount bool
	for _, a := range desired.Spec.Template.Spec.Containers[0].Args {
		if a == "--config-file="+nvidia.SharingConfigPath {
			hasArg = true
		}
	}
	for _, v := range desired.Spec.Template.Spec.Volumes {
		if v.Name == nvidia.SharingVolumeName {
			hasVol = true
		}
	}
	for _, m := range desired.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == nvidia.SharingVolumeName {
			hasMount = true
		}
	}
	if !hasArg || !hasVol || !hasMount {
		t.Fatalf("sharing wiring lost: arg=%v vol=%v mount=%v", hasArg, hasVol, hasMount)
	}
}

// TestEnsureNvidiaDevicePluginUnownedStaysPlain: owner 어노테이션이 없는(ACPP 미개입) DS 는
// sharing 배선을 얻으면 안 된다(회귀 — 기존 순정 device-plugin 동작 불변).
func TestEnsureNvidiaDevicePluginUnownedStaysPlain(t *testing.T) {
	live := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"}}
	desired := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-device-plugin", Namespace: "kube-system"},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nvidia-device-plugin", Args: []string{"--mig-strategy=mixed"}}},
		}}},
	}
	preserveNvidiaSharing(live, desired)
	if len(desired.Spec.Template.Spec.Volumes) != 0 {
		t.Fatalf("unowned DS must not gain sharing volume: %+v", desired.Spec.Template.Spec.Volumes)
	}
}

// TestFlatAffinityRenderIsDeterministic: flat DS 의 NodeAffinity 는 map 순회(무작위 순서)로
// 조립되므로 sort.Strings 없이는 매 reconcile 마다 MatchExpressions 순서가 흔들려 DeepEqual 이
// 불일치 → DS 가 매번 롤링 재시작한다. sort.Strings 자체는 이미 있다 — 이 테스트는
// 그걸 회귀로부터 고정한다.
func TestFlatAffinityRenderIsDeterministic(t *testing.T) {
	r := &NPUClusterPolicyReconciler{}
	base := map[string]string{"a.io/x": "1", "b.io/y": "2", "c.io/z": "3", "d.io/w": "4"}
	seen := map[string]struct{}{}
	for i := 0; i < 200; i++ {
		ds := r.buildNvidiaDevicePluginDS(&npuv1alpha1.NPUClusterPolicy{}, nvidia.DevicePluginNameFlat, base, false)
		key := ""
		for _, e := range ds.Spec.Template.Spec.Affinity.NodeAffinity.
			RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions {
			key += e.Key + ","
		}
		seen[key] = struct{}{}
	}
	if len(seen) != 1 {
		t.Fatalf("flat affinity render must be deterministic, got %d orderings", len(seen))
	}
}

// rebellionsPolicy는 device-plugin 렌더에 필요한 최소 정책이다.
func rebellionsPolicy() *npuv1alpha1.NPUClusterPolicy {
	p := &npuv1alpha1.NPUClusterPolicy{}
	p.Spec.Rebellions.Enabled = true
	p.Spec.Rebellions.DevicePluginImage = "docker.io/rebellions/k8s-device-plugin:v0.3.6"
	p.Spec.Rebellions.Namespace = naming.KubeSystemNamespace
	return p
}

// 단일 파일 hostPath(type=File)로 rbln-smi 를 연결하면 SDK 가 /usr/bin 에 설치한 노드에서 검사가 실패해
// 파드가 ContainerCreating 에서 멈춘다. 구판이 rbln-smi 를 찾는 경로는 컨테이너 PATH 로 알려 준다
// (rebellions_plugin_render.go rbllnsV03Path).
func TestEnsureRebellionsDevicePlugin_NoSingleFileSmiBridge(t *testing.T) {
	p := rebellionsPolicy()
	r := newDRAReconciler(p)
	if err := r.ensureRebellionsDevicePlugin(context.Background(), p); err != nil {
		t.Fatalf("ensureRebellionsDevicePlugin: %v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(), types.NamespacedName{
		Name: rbllnsDaemonSetName, Namespace: naming.KubeSystemNamespace}, &ds); err != nil {
		t.Fatalf("DaemonSet 이 생성되지 않았다: %v", err)
	}
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "host-rbln-smi" {
			t.Fatal("host-rbln-smi 볼륨이 남아 있다 — /usr/bin 설치 노드에서 파드가 멈춘다")
		}
	}
	for _, m := range ds.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.MountPath == "/usr/bin/rbln-smi" {
			t.Fatal("/usr/bin/rbln-smi 마운트가 남아 있다")
		}
	}
	// 호스트 /usr/bin 자체를 읽기 전용으로 붙이는 경로는 유지한다. 벤더 이미지가
	// 그 아래에서 도구를 찾는 대체 경로다.
	var hasUsrBin bool
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "host-usr-bin" && v.HostPath != nil && v.HostPath.Path == "/usr/bin" {
			hasUsrBin = true
		}
	}
	if !hasUsrBin {
		t.Error("host-usr-bin 볼륨까지 사라졌다 — 제거 범위가 넓다")
	}
}

var _ = Describe("NPUClusterPolicy managementMode=external/delegated", func() {
	ctx := context.Background()

	newExternalNvidiaPolicy := func(name string) *npuv1alpha1.NPUClusterPolicy {
		return &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "external"},
				},
			},
		}
	}

	newDelegatedNvidiaPolicy := func(name string) *npuv1alpha1.NPUClusterPolicy {
		return &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			},
		}
	}

	reconcileExternal := func(policy *npuv1alpha1.NPUClusterPolicy) {
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		DeferCleanup(func() { deleteNCPAndWait(policy.Name, policy.Namespace) })
		r := &NPUClusterPolicyReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(100),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	It("does not create the vendor device-plugin DaemonSet and reports Ready=ExternalManaged", func() {
		policy := newExternalNvidiaPolicy("np-external-nvidia")
		reconcileExternal(policy)

		for _, name := range []string{nvidia.DevicePluginNameMixed, nvidia.DevicePluginNameFlat} {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "kube-system"}, &appsv1.DaemonSet{})
			Expect(errors.IsNotFound(err)).To(BeTrue(), "external vendor must not create %s", name)
		}

		var got npuv1alpha1.NPUClusterPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &got)).To(Succeed())
		cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("ExternalManaged"))
	})

	// delegated 도 external 과 마찬가지로 데이터플레인을 배포하지 않는다 — Ready 사유가
	// 이를 숨기면 안 된다. 이 체크가 없으면 delegated 단독 구성이 기본 AllResourcesReady 로 샌다.
	It("reports Ready=ExternalManaged with a message naming the delegated vendor", func() {
		policy := newDelegatedNvidiaPolicy("np-delegated-nvidia")
		reconcileExternal(policy)

		var got npuv1alpha1.NPUClusterPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &got)).To(Succeed())
		cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("ExternalManaged"))
		Expect(cond.Message).To(ContainSubstring("nvidia"))
		Expect(cond.Message).To(ContainSubstring("delegated"))
	})

	It("leaves a pre-existing DaemonSet of the same name (no owner annotation) untouched", func() {
		existing := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "third-party"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "third-party"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "third:v1"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}})
		})

		reconcileExternal(newExternalNvidiaPolicy("np-external-preexisting"))

		var survived appsv1.DaemonSet
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nvidia.DevicePluginNameMixed, Namespace: "kube-system"}, &survived)).To(Succeed())
		Expect(survived.Spec.Template.Spec.Containers[0].Image).To(Equal("third:v1"))
	})

	It("does not deploy the vendor's exporter or DRA driver operands", func() {
		// DRA 드라이버가 생성될 네임스페이스 — 게이트 전(RED)엔 여기 DS 가 생기고, 게이트 후엔 비어야 한다.
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: draNamespace}})

		policy := &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "np-external-operands", Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:           true,
					DevicePluginImage: "img:v1",
					ManagementSpec:    npuv1alpha1.ManagementSpec{ManagementMode: "external"},
					DcgmExporter:      &npuv1alpha1.DcgmExporterSpec{Enabled: true},
					DRA:               &npuv1alpha1.DRASpec{Enabled: true},
				},
			},
		}
		reconcileExternal(policy)

		// NPUClusterPolicy 가 배포하는 nvidia operand 2종(dcgm-exporter, DRA driver)이 없어야 한다.
		err := k8sClient.Get(ctx, types.NamespacedName{Name: dcgmExporterDSName, Namespace: "kube-system"}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "external must not create dcgm-exporter")
		err = k8sClient.Get(ctx, types.NamespacedName{Name: draDSNameNvidia, Namespace: draNamespace}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "external must not create nvidia DRA driver")
		// toolkit 은 NPUClusterPolicy 가 아니라 DriverInstallPolicy(ToolkitDaemonSetReconciler)가
		// 배포하므로 이 reconcile 경로엔 애초에 없다 — 여기서 검증 대상이 아니다.

		var got npuv1alpha1.NPUClusterPolicy
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, &got)).To(Succeed())
		cond := apimeta.FindStatusCondition(got.Status.Conditions, "Ready")
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("ExternalManaged"))
	})
})

var _ = Describe("NPUClusterPolicy delete-on-disable (owned DaemonSet recovery)", func() {
	ctx := context.Background()

	// ownedDS 는 ensure 경로가 남기는 것과 같은 owner annotation 을 단 DaemonSet 이다.
	ownedDS := func(name, ns, owner string) *appsv1.DaemonSet {
		return &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: ns,
				Annotations: map[string]string{ownerAnnotation: owner},
			},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img:v1"}}},
				},
			},
		}
	}
	basePolicy := func(name string) *npuv1alpha1.NPUClusterPolicy {
		return &npuv1alpha1.NPUClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: npuv1alpha1.NPUClusterPolicySpec{
				Detector: &npuv1alpha1.DetectorSpec{Image: "registry.example.com/detector:test"},
			},
		}
	}
	runReconcile := func(policy *npuv1alpha1.NPUClusterPolicy) {
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		DeferCleanup(func() { deleteNCPAndWait(policy.Name, policy.Namespace) })
		r := &NPUClusterPolicyReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(100)}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}})
		Expect(err).NotTo(HaveOccurred())
	}
	deleteDS := func(name, ns string) {
		_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}})
	}

	It("removes the tenstorrent owned DaemonSet when disabled", func() {
		ns := naming.OperatorNamespace()
		pol := basePolicy("np-tt-disable")
		Expect(k8sClient.Create(ctx, ownedDS(ttDaemonSetName, ns, "default/np-tt-disable"))).To(Succeed())
		DeferCleanup(func() { deleteDS(ttDaemonSetName, ns) })
		runReconcile(pol) // tenstorrent.enabled defaults false
		err := k8sClient.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: ns}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "disabled tenstorrent must recover its owned DS")
	})

	It("keeps a same-name tenstorrent DaemonSet that we do not own", func() {
		ns := naming.OperatorNamespace()
		foreign := ownedDS(ttDaemonSetName, ns, "") // no owner annotation
		foreign.Annotations = nil
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		DeferCleanup(func() { deleteDS(ttDaemonSetName, ns) })
		runReconcile(basePolicy("np-tt-foreign"))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: ns}, &appsv1.DaemonSet{})).To(Succeed(), "foreign DS must survive")
	})

	It("does not delete the tenstorrent owned DaemonSet when the vendor is external", func() {
		ns := naming.OperatorNamespace()
		pol := basePolicy("np-tt-external")
		pol.Spec.Tenstorrent = npuv1alpha1.TenstorrentSpec{ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"}}
		Expect(k8sClient.Create(ctx, ownedDS(ttDaemonSetName, ns, "default/np-tt-external"))).To(Succeed())
		DeferCleanup(func() { deleteDS(ttDaemonSetName, ns) })
		runReconcile(pol)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ttDaemonSetName, Namespace: ns}, &appsv1.DaemonSet{})).To(Succeed(), "external must neither create nor delete")
	})

	It("removes the rebellions owned DaemonSet when disabled", func() {
		pol := basePolicy("np-rbln-disable")
		ns := rbllnsResolveNamespace(pol)
		Expect(k8sClient.Create(ctx, ownedDS(rbllnsDaemonSetName, ns, "default/np-rbln-disable"))).To(Succeed())
		DeferCleanup(func() { deleteDS(rbllnsDaemonSetName, ns) })
		runReconcile(pol)
		err := k8sClient.Get(ctx, types.NamespacedName{Name: rbllnsDaemonSetName, Namespace: ns}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "disabled rebellions must recover its owned DS")
	})

	It("removes the furiosa warboy and rngd owned DaemonSets when disabled", func() {
		ns := furiosaVendorDSNamespace()
		Expect(k8sClient.Create(ctx, ownedDS(furiosaWarboyDSName, ns, "default/np-furiosa-disable"))).To(Succeed())
		Expect(k8sClient.Create(ctx, ownedDS(furiosaRngdDSName, ns, "default/np-furiosa-disable"))).To(Succeed())
		DeferCleanup(func() { deleteDS(furiosaWarboyDSName, ns); deleteDS(furiosaRngdDSName, ns) })
		runReconcile(basePolicy("np-furiosa-disable")) // furiosa + rngd default disabled
		errW := k8sClient.Get(ctx, types.NamespacedName{Name: furiosaWarboyDSName, Namespace: ns}, &appsv1.DaemonSet{})
		errR := k8sClient.Get(ctx, types.NamespacedName{Name: furiosaRngdDSName, Namespace: ns}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(errW)).To(BeTrue(), "disabled furiosa warboy must be recovered")
		Expect(errors.IsNotFound(errR)).To(BeTrue(), "disabled furiosa rngd must be recovered")
	})

	It("keeps a same-name dcgm-exporter that we do not own when disabled", func() {
		// dcgm-exporter 는 벤더 일반 이름이라 운영자가 upstream 차트로 같은 이름을 설치할 수 있다.
		// 소유 표시가 없으면 토글 off 회수 경로가 지워선 안 된다(매 reconcile 실행되는 경로).
		foreign := ownedDS(dcgmExporterDSName, naming.KubeSystemNamespace, "")
		foreign.Annotations = nil
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		DeferCleanup(func() { deleteDS(dcgmExporterDSName, naming.KubeSystemNamespace) })
		runReconcile(basePolicy("np-dcgm-foreign")) // nvidia/dcgmExporter 기본 비활성(managed)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dcgmExporterDSName, Namespace: naming.KubeSystemNamespace}, &appsv1.DaemonSet{})).
			To(Succeed(), "소유 표시 없는 동명 dcgm-exporter 는 보존돼야 한다")
	})

	It("removes an owned dcgm-exporter when disabled", func() {
		owned := ownedDS(dcgmExporterDSName, naming.KubeSystemNamespace, "default/np-dcgm-owned")
		Expect(k8sClient.Create(ctx, owned)).To(Succeed())
		DeferCleanup(func() { deleteDS(dcgmExporterDSName, naming.KubeSystemNamespace) })
		runReconcile(basePolicy("np-dcgm-owned"))
		err := k8sClient.Get(ctx, types.NamespacedName{Name: dcgmExporterDSName, Namespace: naming.KubeSystemNamespace}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "소유한 dcgm-exporter 는 회수돼야 한다")
	})

	It("keeps a same-name furiosa-device-plugin (pre-rename generic name) that we do not own", func() {
		// furiosa-device-plugin 은 kcloud 접두어 없는 벤더 일반 이름이라 3rd-party 가 같은 이름을 쓸 수 있다.
		// 비활성 정리 경로(cleanupDisabledFuriosaLegacyDS)는 소유 표시가 없으면 지워선 안 된다.
		foreign := ownedDS(furiosaWarboyLegacyDSName, naming.KubeSystemNamespace, "")
		foreign.Annotations = nil
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		DeferCleanup(func() { deleteDS(furiosaWarboyLegacyDSName, naming.KubeSystemNamespace) })
		runReconcile(basePolicy("np-furiosa-legacy-foreign")) // furiosa 기본 비활성(managed)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: furiosaWarboyLegacyDSName, Namespace: naming.KubeSystemNamespace}, &appsv1.DaemonSet{})).
			To(Succeed(), "소유 표시 없는 동명 furiosa-device-plugin 은 보존돼야 한다")
	})

	It("removes an owned furiosa-device-plugin (pre-rename generic name) when disabled", func() {
		owned := ownedDS(furiosaWarboyLegacyDSName, naming.KubeSystemNamespace, "default/np-furiosa-legacy-owned")
		Expect(k8sClient.Create(ctx, owned)).To(Succeed())
		DeferCleanup(func() { deleteDS(furiosaWarboyLegacyDSName, naming.KubeSystemNamespace) })
		runReconcile(basePolicy("np-furiosa-legacy-owned"))
		err := k8sClient.Get(ctx, types.NamespacedName{Name: furiosaWarboyLegacyDSName, Namespace: naming.KubeSystemNamespace}, &appsv1.DaemonSet{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "소유한 옛 이름 furiosa-device-plugin 은 회수돼야 한다")
	})
})

// nonOperandVendorNames 는 external·delegated 를 모드별로 갈라 담아야 한다 — 둘 다 데이터플레인이
// 없어도 정상이라는 결과는 같지만, Ready 메시지가 어느 벤더가 어느 모드인지 밝히려면 섞이면 안 된다.
// furiosa 케이스는 rngd 만 켜져 있고(Furiosa.Enabled=false) 상위 furiosa 모드를 따르는 경로를 겸해 본다.
func TestNonOperandVendorNames(t *testing.T) {
	cases := []struct {
		name          string
		policy        *npuv1alpha1.NPUClusterPolicy
		wantExternal  []string
		wantDelegated []string
	}{
		{
			name: "external only (furiosa, rngd 만 활성)",
			policy: &npuv1alpha1.NPUClusterPolicy{Spec: npuv1alpha1.NPUClusterPolicySpec{
				Furiosa: npuv1alpha1.FuriosaSpec{
					Enabled:        false,
					ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
					Rngd:           npuv1alpha1.RngdSpec{Enabled: true},
				},
			}},
			wantExternal: []string{vendorFuriosa},
		},
		{
			name: "delegated only (nvidia)",
			policy: &npuv1alpha1.NPUClusterPolicy{Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:        true,
					ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			}},
			wantDelegated: []string{vendorNvidia},
		},
		{
			name: "external 과 delegated 동시(nvidia=external, tenstorrent=delegated)",
			policy: &npuv1alpha1.NPUClusterPolicy{Spec: npuv1alpha1.NPUClusterPolicySpec{
				Nvidia: npuv1alpha1.NvidiaSpec{
					Enabled:        true,
					ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "external"},
				},
				Tenstorrent: npuv1alpha1.TenstorrentSpec{
					Enabled:        true,
					ManagementSpec: npuv1alpha1.ManagementSpec{ManagementMode: "delegated"},
				},
			}},
			wantExternal:  []string{vendorNvidia},
			wantDelegated: []string{vendorTenstorrent},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotExternal, gotDelegated := nonOperandVendorNames(tc.policy)
			if !reflect.DeepEqual(gotExternal, tc.wantExternal) {
				t.Errorf("external = %v, want %v", gotExternal, tc.wantExternal)
			}
			if !reflect.DeepEqual(gotDelegated, tc.wantDelegated) {
				t.Errorf("delegated = %v, want %v", gotDelegated, tc.wantDelegated)
			}
		})
	}
}

// nonOperandReadyMessage 는 external·delegated 어느 쪽이든, 둘 다든 벤더 이름을 한 줄에서
// 읽을 수 있어야 한다 — 셋 중 어느 조합이 와도 메시지에 그 벤더 이름과 모드가 같이 남는다.
func TestNonOperandReadyMessage(t *testing.T) {
	cases := []struct {
		name            string
		external        []string
		delegated       []string
		wantContainsAll []string
	}{
		{
			name:            "external only",
			external:        []string{vendorNvidia},
			wantContainsAll: []string{vendorNvidia, "external"},
		},
		{
			name:            "delegated only",
			delegated:       []string{vendorNvidia},
			wantContainsAll: []string{vendorNvidia, "delegated"},
		},
		{
			name:            "external 과 delegated 동시",
			external:        []string{vendorNvidia},
			delegated:       []string{vendorTenstorrent},
			wantContainsAll: []string{vendorNvidia, vendorTenstorrent, "external", "delegated"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := nonOperandReadyMessage(tc.external, tc.delegated)
			if strings.Contains(msg, "\n") {
				t.Errorf("message 는 한 줄이어야 한다: %q", msg)
			}
			for _, want := range tc.wantContainsAll {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q 에 %q 가 없다", msg, want)
				}
			}
		})
	}
}

// createOrUpdateDS 는 deletionTimestamp 가 찍힌(삭제 진행 중) DaemonSet 을 Update 하지 않고
// 오류를 돌려 다음 reconcile 로 backoff 재시도해야 한다 — 죽어가는 객체를 갱신하면 GC 가 곧 지워
// 광고가 사라진다(비활성→즉시 재활성 경쟁).
func TestCreateOrUpdateDS_RefusesTerminatingDaemonSet(t *testing.T) {
	ctx := context.Background()
	existing := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kcloud-tt-device-plugin", Namespace: "kcloud",
			Finalizers: []string{"kcloud.ai/test-hold"},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "x"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img:v1"}}},
			},
		},
	}
	r := newNPUReconciler(existing)

	// finalizer 가 있어 Delete 는 객체를 지우지 않고 deletionTimestamp 만 찍는다(terminating).
	if err := r.Delete(ctx, existing); err != nil {
		t.Fatalf("terminating 유도용 Delete 실패: %v", err)
	}
	var cur appsv1.DaemonSet
	key := types.NamespacedName{Name: existing.Name, Namespace: existing.Namespace}
	if err := r.Get(ctx, key, &cur); err != nil {
		t.Fatalf("terminating DS 조회 실패: %v", err)
	}
	if cur.DeletionTimestamp.IsZero() {
		t.Fatalf("전제 위반: finalizer 로 deletionTimestamp 를 유도해야 한다")
	}

	desired := cur.DeepCopy()
	desired.Finalizers = nil
	desired.Spec.Template.Spec.Containers[0].Image = "img:v2"
	if err := r.createOrUpdateDS(ctx, desired); err == nil {
		t.Fatalf("삭제 진행 중 DaemonSet 에는 createOrUpdateDS 가 오류를 돌려줘야 한다(backoff)")
	}

	// cleanup: finalizer 제거로 GC 진행.
	cur.Finalizers = nil
	_ = r.Update(ctx, &cur)
}
