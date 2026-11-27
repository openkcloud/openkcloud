// ============================================================
// timeslicing_test.go: NVIDIA device-plugin sharing config 렌더 테스트
// 상세: 스키마 정본은 upstream NVIDIA device-plugin 의 api/config/v1(sharing·replicas 정의)다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
)

// priorConfig 는 우리가 오기 전/이전 apply 로 존재하던 sharing config 내용이다.
const priorConfig = "version: v1\n"

// dpDaemonSet 은 sharing 배선 대상 device-plugin DS 를 흉내낸다(테스트 픽스처).
func dpDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: DevicePluginNameMixed, Namespace: dpNamespace},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: DevicePluginNameMixed, Args: []string{"--mig-strategy=mixed"}}},
		}}},
	}
}

// worker1Node 는 dpDaemonSet(mixed DS)이 맡는 mig-active 노드 픽스처다 — DevicePluginTargetForNode
// 가 노드를 조회하므로 "worker1" 을 쓰는 기존 테스트는 전부 이 노드가 있어야 mixed 로 해석된다.
func worker1Node() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: map[string]string{MigActiveNodeLabel: "true"}}}
}

func TestRenderSharingConfigTimeSlicedGPU(t *testing.T) {
	got, err := RenderSharingConfig(map[string]int32{"nvidia.com/gpu": 1},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"version: v1", "sharing:", "timeSlicing:", "name: nvidia.com/gpu", "replicas: 4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "renameByDefault: true") {
		t.Fatalf("renameByDefault must stay false (resource name stability):\n%s", got)
	}
}

func TestRenderSharingConfigCoversMigResources(t *testing.T) {
	got, err := RenderSharingConfig(map[string]int32{"nvidia.com/mig-1g.6gb": 4},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "name: nvidia.com/mig-1g.6gb") {
		t.Fatalf("MIG resource not in config:\n%s", got)
	}
}

func TestRenderSharingConfigExclusiveIsEmptySharing(t *testing.T) {
	got, err := RenderSharingConfig(map[string]int32{"nvidia.com/gpu": 1},
		partition.SharingLayout{Mode: "exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "timeSlicing") {
		t.Fatalf("exclusive config must not contain timeSlicing:\n%s", got)
	}
}

func TestExpectedSharedMultipliesEachResource(t *testing.T) {
	got := ExpectedShared(map[string]int32{"nvidia.com/mig-1g.6gb": 4, "nvidia.com/gpu": 1},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 3})
	if got["nvidia.com/mig-1g.6gb"] != 12 || got["nvidia.com/gpu"] != 3 {
		t.Fatalf("expected allocatable = %v", got)
	}
}

func TestExpectedSharedExclusiveIsIdentity(t *testing.T) {
	base := map[string]int32{"nvidia.com/gpu": 2}
	got := ExpectedShared(base, partition.SharingLayout{Mode: "exclusive"})
	if got["nvidia.com/gpu"] != 2 {
		t.Fatalf("exclusive must not multiply: %v", got)
	}
}

// MPS 는 device-plugin config 에서 sharing.mps 로 들어간다(timeSliced 와 같은 스키마,
// 다른 키). 키를 틀리면 DP 가 조용히 공유 없이 뜬다.
func TestRenderSharingConfig_MPS(t *testing.T) {
	out, err := RenderSharingConfig(map[string]int32{"nvidia.com/gpu": 1},
		partition.SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 4})
	if err != nil {
		t.Fatalf("렌더 실패: %v", err)
	}
	if !strings.Contains(out, "mps:") {
		t.Errorf("sharing.mps 키가 없다:\n%s", out)
	}
	if strings.Contains(out, "timeSlicing:") {
		t.Errorf("mps 모드인데 timeSlicing 이 렌더됐다:\n%s", out)
	}
	if !strings.Contains(out, "replicas: 4") {
		t.Errorf("replica 가 없다:\n%s", out)
	}
}

// 기대 allocatable 은 두 모드가 같다(replica 배수).
func TestExpectedShared_MPSMultiplies(t *testing.T) {
	got := ExpectedShared(map[string]int32{"nvidia.com/gpu": 2},
		partition.SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 3})
	if got["nvidia.com/gpu"] != 6 {
		t.Errorf("2 x 3 = 6 이어야 함: got %d", got["nvidia.com/gpu"])
	}
}

// upstream device-plugin(api/config/v1/config.go)은 sharing.mps.failRequestsGreaterThanOne
// 이 미설정(nil)이면 true 로 승격한다(historical MPS 안전장치 — replicas>1 요청 거부). 우리는 이 필드를
// 항상 명시값으로 렌더하므로 그 nil-승격 경로를 절대 못 탄다 — 렌더링 시점에 같은 기본값을 직접 내야
// upstream 의 안전 기본값을 조용히 뒤집지 않는다. MPSSpec 에는 이를 뒤집을 스펙 필드가 없으므로(범위 밖)
// mps 는 항상 true 로 고정한다.
func TestRenderSharingConfig_MPSDefaultsFailRequestsGreaterThanOneTrue(t *testing.T) {
	out, err := RenderSharingConfig(map[string]int32{"nvidia.com/gpu": 1},
		partition.SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "failRequestsGreaterThanOne: true") {
		t.Errorf("mps 는 upstream historical 기본값(true)을 내야 한다:\n%s", out)
	}
}

func TestBackendSatisfiesSharingBackend(t *testing.T) {
	var _ partition.SharingBackend = (*Backend)(nil)
}

// sharingFingerprint 는 WireSharing 이 건드리는 모든 필드(arg/env/mount/volume)의 이름 집합이다.
// 개수만 비교하면 엉뚱한 이름을 지우고도 개수가 맞아떨어지면 통과한다 — 이름 집합
// 자체를 비교해야 "붙인 것을 정확히 그대로 뗐다"를 증명한다.
func sharingFingerprint(ds *appsv1.DaemonSet) map[string]bool {
	ctr := ds.Spec.Template.Spec.Containers[0]
	fp := map[string]bool{}
	for _, a := range ctr.Args {
		fp["arg:"+a] = true
	}
	for _, e := range ctr.Env {
		fp["env:"+e.Name] = true
	}
	for _, m := range ctr.VolumeMounts {
		fp["mount:"+m.Name] = true
	}
	for _, v := range ds.Spec.Template.Spec.Volumes {
		fp["volume:"+v.Name] = true
	}
	return fp
}

// Wire 가 붙인 것을 Unwire 가 전부 떼야 한다 — 남으면 exclusive 로 돌아간 뒤에도 마운트가 남는다.
func TestWireUnwireSharing_IsSymmetricForMPS(t *testing.T) {
	ds := dpDaemonSet()
	before := sharingFingerprint(ds)
	WireSharing(ds, v1alpha1.SharingModeMPS, SharingConfigMapNameMixed)
	UnwireSharing(ds)
	after := sharingFingerprint(ds)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("배선이 대칭이 아니다: before=%v after=%v", before, after)
	}
}

// upstream DP 바이너리는 sharing strategy 가 mps 면 --mps-root/MPS_ROOT 가 없으면 기동 자체를
// 거부한다(cmd/nvidia-device-plugin/main.go:231-232). env 가 없으면 DP 가 crash-loop 한다 —
// 조용한 no-op 이 아니다. upstream(daemonset-device-plugin.yml)은 DP 컨테이너에
// mps-shm(→/dev/shm)도 마운트한다 — "MPS daemon health-checking" 에 필요하다는 upstream
// 자신의 주석.
func TestWireSharing_MPSSetsRootEnvAndShmMount(t *testing.T) {
	ds := dpDaemonSet()
	WireSharing(ds, v1alpha1.SharingModeMPS, SharingConfigMapNameMixed)
	ctr := ds.Spec.Template.Spec.Containers[0]

	var gotRoot string
	for _, e := range ctr.Env {
		if e.Name == "MPS_ROOT" {
			gotRoot = e.Value
		}
	}
	if gotRoot != MPSPipeDir {
		t.Errorf("MPS_ROOT=%q, want %q — 없으면 DP 가 기동을 거부한다", gotRoot, MPSPipeDir)
	}

	var gotShm bool
	for _, m := range ctr.VolumeMounts {
		if m.Name == "mps-shm" && m.MountPath == "/dev/shm" {
			gotShm = true
		}
	}
	if !gotShm {
		t.Errorf("mps-shm→/dev/shm 마운트가 없다: %+v", ctr.VolumeMounts)
	}
}

// 이 결함은 DP 가 MPS daemon 과 통신하지 못하는 증상으로 나타난다("...failed to send command
// to MPS daemon") — DP 컨테이너 안에 /mps 가 없으면 이 에러가 난다. upstream 은 두 값을 다른
// 용도로 쓴다: mps.ContainerRoot("/mps")는 DP 자신의 컨테이너 뷰(health-check daemon 생성 경로,
// internal/plugin/mps.go:55)이고, MPS_ROOT(--mps-root)는 호스트 경로로 워크로드 pod 마운트의
// HostPath 가 된다(같은 파일:56). 그래서 마운트 경로는 /mps 하나이고 MPS_ROOT 는 호스트 경로로
// 남아야 한다 — 바꾸면 워크로드 pod 에 없는 호스트 경로가 주입된다.
func TestWireSharing_MPSMountsRootAtContainerRoot(t *testing.T) {
	ds := dpDaemonSet()
	WireSharing(ds, v1alpha1.SharingModeMPS, SharingConfigMapNameMixed)
	ctr := ds.Spec.Template.Spec.Containers[0]

	var got string
	for _, m := range ctr.VolumeMounts {
		if m.Name == "mps-pipe" {
			got = m.MountPath
		}
	}
	if got != MPSContainerRoot {
		t.Errorf("★ mps-pipe 마운트 경로 = %q, want %q — DP 바이너리는 이 경로를 하드코딩해 daemon 을 찾는다",
			got, MPSContainerRoot)
	}
	// 호스트 경로를 컨테이너 경로로 바꾸면 워크로드 pod 이 없는 HostPath 를 받는다.
	for _, e := range ctr.Env {
		if e.Name == "MPS_ROOT" && e.Value != MPSPipeDir {
			t.Errorf("MPS_ROOT=%q, want %q(호스트 경로) — 워크로드 pod 마운트의 HostPath 로 쓰인다",
				e.Value, MPSPipeDir)
		}
	}
}

// daemon 쪽과 DP 쪽이 다른 경로를 보면 통신이 끊긴다 — 둘은 같은 파이프 디렉터리를 봐야
// 통신이 성립한다. 렌더러가 둘로 나뉘어 있으므로(controller / nvidia 패키지) 상수 단일
// 출처만으론 부족해, 실제로 렌더된 두 오브젝트를 맞대어 고정한다(controller 패키지 쪽 절반).
func TestMPSRootIsSameHostPathAndContainerPath(t *testing.T) {
	ds := dpDaemonSet()
	WireSharing(ds, v1alpha1.SharingModeMPS, SharingConfigMapNameMixed)

	var hostPath string
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "mps-pipe" && v.HostPath != nil {
			hostPath = v.HostPath.Path
		}
	}
	if hostPath != MPSPipeDir {
		t.Errorf("DP 의 MPS root 호스트 경로 = %q, want %q", hostPath, MPSPipeDir)
	}
}

func TestApplySharingCreatesConfigMapAndPatchesDS(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	b := New(c)

	st, err := b.ApplySharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 4},
		map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}
	if st.ConfigMapExisted {
		t.Fatalf("first apply must report ConfigMapExisted=false")
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatalf("configmap not created: %v", err)
	}
	if !strings.Contains(cm.Data[SharingConfigKey], "replicas: 4") {
		t.Fatalf("configmap content = %q", cm.Data[SharingConfigKey])
	}

	var got appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[SharingOwnerAnnotation] != "acpp-1" {
		t.Fatalf("owner annotation = %q", got.Annotations[SharingOwnerAnnotation])
	}
	ctr := got.Spec.Template.Spec.Containers[0]
	if len(ctr.VolumeMounts) != 1 || len(got.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("sharing volume/mount not wired: %+v", got.Spec.Template.Spec)
	}
}

// TestApplySharingRejectsInvalidLayout: 렌더러(RenderSharingConfig)는 replicas 를 검증하지 않으므로
// backend 가 fail-closed 로 막아야 한다 — 아니면 DP 가 기동 실패하는 config 가 실려나간다(리뷰 지적).
func TestApplySharingRejectsInvalidLayout(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	b := New(c)

	if _, err := b.ApplySharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 1},
		map[string]int32{"nvidia.com/gpu": 1}); err == nil {
		t.Fatal("replicas=1 must be rejected before any mutation")
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); !apierrors.IsNotFound(err) {
		t.Fatalf("invalid layout must not create a configmap, err=%v", err)
	}
}

// TestApplySharingRefusesForeignOwner: 다른 ACPP 가 소유한 DP 설정은 덮어쓰지 않는다(2-writer 조정).
func TestApplySharingRefusesForeignOwner(t *testing.T) {
	ctx := context.Background()
	ds := dpDaemonSet()
	ds.Annotations = map[string]string{SharingOwnerAnnotation: "acpp-other"}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ds, worker1Node()).Build()
	b := New(c)

	if _, err := b.ApplySharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 4},
		map[string]int32{"nvidia.com/gpu": 1}); err == nil {
		t.Fatal("apply must refuse a device-plugin owned by another ACPP")
	}
}

func TestRollbackSharingRemovesWiringWhenNoPriorConfig(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	b := New(c)
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	st, err := b.ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 4}, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RollbackSharing(tgt, *st); err != nil {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	err = c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("configmap must be deleted on rollback, err=%v", err)
	}
	var got appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &got); err != nil {
		t.Fatal(err)
	}
	if _, owned := got.Annotations[SharingOwnerAnnotation]; owned {
		t.Fatalf("owner annotation must be released on rollback")
	}
	for _, a := range got.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(a, "--config-file=") {
			t.Fatalf("config-file arg must be removed on rollback: %v", got.Spec.Template.Spec.Containers[0].Args)
		}
	}
	if len(got.Spec.Template.Spec.Volumes) != 0 || len(got.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("sharing volume/mount must be removed on rollback: %+v", got.Spec.Template.Spec)
	}
}

// TestRollbackSharingRestoresPriorConfig: 우리가 이전에 적용해 둔 공유 설정이 있으면 재적용 실패 시
// 그 내용으로 되돌리고 배선은 유지한다.
func TestRollbackSharingRestoresPriorConfig(t *testing.T) {
	ctx := context.Background()
	prev := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: SharingConfigMapNameMixed, Namespace: dpNamespace},
		Data:       map[string]string{SharingConfigKey: priorConfig},
	}
	ds := dpDaemonSet()
	ds.Annotations = map[string]string{SharingOwnerAnnotation: "acpp-1"} // 우리가 이미 소유(이전 apply).
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ds, prev, worker1Node()).Build()
	b := New(c)
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	st, err := b.ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 4}, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !st.ConfigMapExisted || st.PrevConfigYAML != priorConfig {
		t.Fatalf("rollback state did not snapshot prior config: %+v", st)
	}
	if err := b.RollbackSharing(tgt, *st); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Data[SharingConfigKey] != priorConfig {
		t.Fatalf("prior config not restored: %q", cm.Data[SharingConfigKey])
	}
}

// TestApplySharingRefusesForeignConfigMap: 남의 소유 표시가 붙은 sharing config 는 덮지 않는다
// (스냅샷은 프로세스 메모리뿐이라 삭제 경로에서 복원 불가 — 조용한 파기 금지).
func TestApplySharingRefusesForeignConfigMap(t *testing.T) {
	ctx := context.Background()
	foreign := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: SharingConfigMapNameMixed, Namespace: dpNamespace,
			Annotations: map[string]string{SharingOwnerAnnotation: "acpp-other"},
		},
		Data: map[string]string{SharingConfigKey: priorConfig},
	}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), foreign, worker1Node()).Build()
	b := New(c)

	if _, err := b.ApplySharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingLayout{Mode: "timeSliced", Replicas: 4},
		map[string]int32{"nvidia.com/gpu": 1}); err == nil {
		t.Fatal("apply must refuse a pre-existing sharing configmap it does not own")
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Data[SharingConfigKey] != priorConfig {
		t.Fatalf("foreign config must be untouched: %q", cm.Data[SharingConfigKey])
	}
}

// TestRollbackSharingLeavesForeignConfigMapIntact: 타 소유 상태에서 zero-state 로 원복이 불려도
// ConfigMap 을 지우면 안 된다 — DP DaemonSet 은 전역 1개라 전 노드 광고가 사라진다.
func TestRollbackSharingLeavesForeignConfigMapIntact(t *testing.T) {
	ctx := context.Background()
	ds := dpDaemonSet()
	ds.Annotations = map[string]string{SharingOwnerAnnotation: "acpp-other"}
	WireSharing(ds, v1alpha1.SharingModeTimeSliced, SharingConfigMapNameMixed)
	foreign := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: SharingConfigMapNameMixed, Namespace: dpNamespace},
		Data:       map[string]string{SharingConfigKey: priorConfig},
	}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(ds, foreign, worker1Node()).Build()

	if err := New(c).RollbackSharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingRollbackState{}); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatalf("foreign configmap must survive: %v", err)
	}
	var got appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[SharingOwnerAnnotation] != "acpp-other" || len(got.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("foreign wiring must survive: %+v", got.Spec.Template.Spec)
	}
}

// TestApplySharingAdoptsOwnConfigMapAfterDaemonSetRecreation: DS 가 재생성돼(수동 삭제/helm replace)
// owner 어노테이션이 사라져도 ConfigMap 에 남은 우리 표시로 재적용이 계속돼야 한다. 소유 판정을
// DS 에 두면 이 상태에서 자기가 만든 ConfigMap 을 남의 것으로 읽어 영구히 apply 실패한다(자가치유 불가).
func TestApplySharingAdoptsOwnConfigMapAfterDaemonSetRecreation(t *testing.T) {
	ctx := context.Background()
	ours := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: SharingConfigMapNameMixed, Namespace: dpNamespace,
			Annotations: map[string]string{SharingOwnerAnnotation: "acpp-1"},
		},
		Data: map[string]string{SharingConfigKey: priorConfig},
	}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), ours, worker1Node()).Build() // 새 DS = 표시 없음
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	if _, err := New(c).ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 4},
		map[string]int32{"nvidia.com/gpu": 1}); err != nil {
		t.Fatalf("re-apply must survive a device-plugin DaemonSet recreation: %v", err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm.Data[SharingConfigKey], "replicas: 4") {
		t.Fatalf("config not re-applied: %q", cm.Data[SharingConfigKey])
	}
}

// TestApplySharingStampsOwnerOnConfigMap: 새로 만든 ConfigMap 에는 소유 표시가 붙어야 한다
// (소유 표시는 소유물에 붙는다 — DS 는 NCP 2-writer 보존 신호로만 쓴다).
func TestApplySharingStampsOwnerOnConfigMap(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	if _, err := New(c).ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 2},
		map[string]int32{"nvidia.com/gpu": 1}); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatal(err)
	}
	if cm.Annotations[SharingOwnerAnnotation] != "acpp-1" {
		t.Fatalf("configmap owner marker = %q", cm.Annotations[SharingOwnerAnnotation])
	}
}

// TestRollbackSharingLeavesForeignMarkedConfigMap: 저널만 보고 zero-state 원복이 걸려도 남의 표시가
// 붙은 ConfigMap 은 지우지 않는다(DS 표시가 없어진 창에서 남의 설정을 삭제하던 갈래를 닫는다).
func TestDevicePluginTargetForNode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	migActiveNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker-mig",
			Labels: map[string]string{MigActiveNodeLabel: "true"},
		},
	}
	flatNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-flat"},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(migActiveNode, flatNode).Build()

	dsName, cmName, err := DevicePluginTargetForNode(context.Background(), cl, "worker-mig")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dsName != DevicePluginNameMixed || cmName != SharingConfigMapNameMixed {
		t.Fatalf("mig-active node resolved to (%s,%s), want (%s,%s)",
			dsName, cmName, DevicePluginNameMixed, SharingConfigMapNameMixed)
	}

	dsName, cmName, err = DevicePluginTargetForNode(context.Background(), cl, "worker-flat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dsName != DevicePluginNameFlat || cmName != SharingConfigMapNameFlat {
		t.Fatalf("flat node resolved to (%s,%s), want (%s,%s)",
			dsName, cmName, DevicePluginNameFlat, SharingConfigMapNameFlat)
	}
}

func TestRollbackSharingLeavesForeignMarkedConfigMap(t *testing.T) {
	ctx := context.Background()
	foreign := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: SharingConfigMapNameMixed, Namespace: dpNamespace,
			Annotations: map[string]string{SharingOwnerAnnotation: "acpp-other"},
		},
		Data: map[string]string{SharingConfigKey: priorConfig},
	}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), foreign, worker1Node()).Build()

	if err := New(c).RollbackSharing(partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"},
		partition.SharingRollbackState{}); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); err != nil {
		t.Fatalf("foreign-marked configmap must survive: %v", err)
	}
}

func TestApplySharingTargetsFlatDaemonSetForNonMigNode(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	flatNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "flat-node"}}
	flatDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: DevicePluginNameFlat, Namespace: dpNamespace},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
			},
		},
	}
	// mixed DS 도 존재시켜, 잘못 대상화되면(=mixed 를 건드리면) 아래 어서션이 그것을 잡는다.
	mixedDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: DevicePluginNameMixed, Namespace: dpNamespace},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "y"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(flatNode, flatDS, mixedDS).Build()
	b := &Backend{c: cl}

	const owner = "acpp-1"
	layout := partition.SharingLayout{Mode: v1alpha1.SharingModeMPS, Replicas: 4}
	_, err := b.ApplySharing(partition.Target{Ctx: context.Background(), NodeName: "flat-node", Owner: owner},
		layout, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatalf("ApplySharing failed: %v", err)
	}

	var gotFlat appsv1.DaemonSet
	if err := cl.Get(context.Background(), types.NamespacedName{Name: DevicePluginNameFlat, Namespace: dpNamespace}, &gotFlat); err != nil {
		t.Fatalf("get flat ds: %v", err)
	}
	if gotFlat.Annotations[SharingOwnerAnnotation] != owner {
		t.Fatalf("flat DS not wired: annotations=%v", gotFlat.Annotations)
	}
	var gotMixed appsv1.DaemonSet
	if err := cl.Get(context.Background(), types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &gotMixed); err != nil {
		t.Fatalf("get mixed ds: %v", err)
	}
	if gotMixed.Annotations[SharingOwnerAnnotation] != "" {
		t.Fatalf("mixed DS was wired but should not have been: annotations=%v", gotMixed.Annotations)
	}
	var cm2 corev1.ConfigMap
	if err := cl.Get(context.Background(), types.NamespacedName{Name: SharingConfigMapNameFlat, Namespace: dpNamespace}, &cm2); err != nil {
		t.Fatalf("sharing configmap not created under flat name: %v", err)
	}
}

// apply 는 노드 라벨로 dsName/cmName 을 정하는데, rollback 이 호출 시점에 라벨을 다시 읽으면
// 그 사이 라벨이 뒤집힌 창에서 자기가 만든 DS/CM 을 잃는다(조용한 no-op). 형제 MIG 정책 삭제
// → syncMigActiveLabel(false) 가 노드를 flat 으로 뒤집으면, sharing 은 이미 mixed 에 배선된
// 채로 이 창에 놓인다.
func TestRollbackSharingFindsOwnedDaemonSetWhenNodeLabelFlips(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	b := New(c)
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	st, err := b.ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 4}, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}

	var n corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: "worker1"}, &n); err != nil {
		t.Fatal(err)
	}
	delete(n.Labels, MigActiveNodeLabel)
	if err := c.Update(ctx, &n); err != nil {
		t.Fatal(err)
	}

	if err := b.RollbackSharing(tgt, *st); err != nil {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm); !apierrors.IsNotFound(err) {
		t.Fatalf("mixed configmap must be deleted on rollback even after the node's label flipped away: err=%v", err)
	}
	var ds appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &ds); err != nil {
		t.Fatal(err)
	}
	if _, owned := ds.Annotations[SharingOwnerAnnotation]; owned {
		t.Fatalf("mixed DS owner annotation must be released on rollback: %v", ds.Annotations)
	}
}

// DS 가 재생성돼(NPUClusterPolicy 재렌더 — preserveNvidiaSharing 은 live 어노테이션이 있을 때만
// 보존) 소유 어노테이션을 잃은 창에 노드 라벨까지 뒤집히면(형제 MIG 정책 삭제), DS scan 도 라벨
// fallback 도 자기가 만든 mixed ConfigMap 을 못 찾아 영구 고아로 남는다(조용한 no-op). CM
// 자체의 소유 표시로 재시도하는 단계가 없으면 이 테스트가 실패한다.
func TestRollbackSharingFindsOwnedConfigMapAfterDaemonSetRecreationAndLabelFlip(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(dpDaemonSet(), worker1Node()).Build()
	b := New(c)
	tgt := partition.Target{Ctx: ctx, NodeName: "worker1", Owner: "acpp-1"}

	st, err := b.ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 4}, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}

	// DS 재생성 시뮬레이션: 어노테이션·배선 소실, ConfigMap 은 그대로(=영향 안 받는 별도 오브젝트).
	var ds appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &ds); err != nil {
		t.Fatal(err)
	}
	fresh := dpDaemonSet()
	ds.Annotations = fresh.Annotations
	ds.Spec = fresh.Spec
	if err := c.Update(ctx, &ds); err != nil {
		t.Fatal(err)
	}

	// 형제 MIG 정책 삭제 → syncMigActiveLabel(false).
	var n corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: "worker1"}, &n); err != nil {
		t.Fatal(err)
	}
	delete(n.Labels, MigActiveNodeLabel)
	if err := c.Update(ctx, &n); err != nil {
		t.Fatal(err)
	}

	if err := b.RollbackSharing(tgt, *st); err != nil {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	err = c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameMixed, Namespace: dpNamespace}, &cm)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("mixed configmap owned by acpp-1 must not survive as a permanent orphan after DS recreation + label flip: err=%v, annotations=%v", err, cm.Annotations)
	}
}

// 소유 DS 가 flat 일 때 rollback 이 SharingConfigMapNameOf(ds) 로 실제 배선된 CM(flat)을
// 따라가는지 고정한다. 이 값을 mixed 로 하드코딩해도 통과하던 이전 가드(소유 DS 가 항상 mixed)와
// 달리, 이 테스트는 소유 DS 가 flat 이라 그 뮤테이션이 flat CM 삭제 누락으로 드러난다.
func TestRollbackSharingFindsOwnedFlatDaemonSet(t *testing.T) {
	ctx := context.Background()
	flatNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "flat-node"}}
	flatDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: DevicePluginNameFlat, Namespace: dpNamespace},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(flatNode, flatDS).Build()
	b := New(c)
	tgt := partition.Target{Ctx: ctx, NodeName: "flat-node", Owner: "acpp-1"}

	st, err := b.ApplySharing(tgt, partition.SharingLayout{Mode: "timeSliced", Replicas: 2}, map[string]int32{"nvidia.com/gpu": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.RollbackSharing(tgt, *st); err != nil {
		t.Fatal(err)
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: SharingConfigMapNameFlat, Namespace: dpNamespace}, &cm); !apierrors.IsNotFound(err) {
		t.Fatalf("flat configmap must be deleted on rollback, err=%v", err)
	}
	var gotDS appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameFlat, Namespace: dpNamespace}, &gotDS); err != nil {
		t.Fatal(err)
	}
	if _, owned := gotDS.Annotations[SharingOwnerAnnotation]; owned {
		t.Fatalf("flat DS owner annotation must be released on rollback: %v", gotDS.Annotations)
	}
}

// scan 이 mixed 를 먼저 훑는데, mixed 는 acpp-2 소유·flat 은 acpp-1 소유인 교차 소유 상태에서
// acpp-1 의 rollback 이 순서상 먼저 만난 mixed 를(동등비교가 무력화되면) 잘못 채택하지 않는지
// 고정한다.
func TestRollbackSharingSkipsForeignOwnedDaemonSetInScanOrder(t *testing.T) {
	ctx := context.Background()
	mixedDS := dpDaemonSet()
	mixedDS.Annotations = map[string]string{SharingOwnerAnnotation: "acpp-2"}

	flatDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: DevicePluginNameFlat, Namespace: dpNamespace,
			Annotations: map[string]string{SharingOwnerAnnotation: "acpp-1"},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "x"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}}},
		},
	}
	WireSharing(flatDS, v1alpha1.SharingModeTimeSliced, SharingConfigMapNameFlat)

	flatCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: SharingConfigMapNameFlat, Namespace: dpNamespace,
			Annotations: map[string]string{SharingOwnerAnnotation: "acpp-1"},
		},
		Data: map[string]string{SharingConfigKey: priorConfig},
	}

	c := fake.NewClientBuilder().WithScheme(scheme()).WithObjects(mixedDS, flatDS, flatCM).Build()

	if err := New(c).RollbackSharing(partition.Target{Ctx: ctx, NodeName: "flat-node", Owner: "acpp-1"},
		partition.SharingRollbackState{}); err != nil {
		t.Fatal(err)
	}

	var gotFlat appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameFlat, Namespace: dpNamespace}, &gotFlat); err != nil {
		t.Fatal(err)
	}
	if _, owned := gotFlat.Annotations[SharingOwnerAnnotation]; owned {
		t.Fatalf("acpp-1's flat wiring must be released, not blocked by acpp-2's mixed ownership: %v", gotFlat.Annotations)
	}

	var gotMixed appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: DevicePluginNameMixed, Namespace: dpNamespace}, &gotMixed); err != nil {
		t.Fatal(err)
	}
	if gotMixed.Annotations[SharingOwnerAnnotation] != "acpp-2" {
		t.Fatalf("acpp-2's mixed ownership must be untouched: %v", gotMixed.Annotations)
	}
}

func TestNoBackCompatAliasesRemain(t *testing.T) {
	// 별칭이 남아 있으면 flat 노드에서 mixed 리소스를 가리키는 사고 경로가 살아 있다는 뜻이다.
	// 컴파일 타임에 막을 방법이 없으므로 소스 스캔으로 회귀를 잠근다.
	src, err := os.ReadFile("timeslicing.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"\n\tdpName ", "\n\tSharingConfigMapName "} {
		if bytes.Contains(src, []byte(alias)) {
			t.Fatalf("하위호환 별칭이 남아 있다: %q", alias)
		}
	}
}
