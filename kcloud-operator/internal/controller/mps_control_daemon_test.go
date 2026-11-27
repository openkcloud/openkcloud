// ============================================================
// mps_control_daemon_test.go: MPS control daemon operand 단위 테스트
// 상세: mps 모드에서 daemon 이 뜨는지, 다른 모드에서 멱등 정리되는지 확인한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

// newMpsTestReconciler 는 DS CRUD 만 필요한 가벼운 fake-client 리컨실러다(acpp_sharing_test.go
// 의 sharingFixture 보다 가벼움 — ACPP/NDR 없이 daemon 보장/정리만 검증).
func newMpsTestReconciler() *AcceleratorPartitionPolicyReconciler {
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).Build()
	return &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme()}
}

// seedMpsDaemon 은 daemon DS 를 만들어 두는 픽스처 단계다. 갓 만든 DS 는 아직 Ready 가 아닌 것이
// 정상이므로(fake client 에는 daemonset 컨트롤러가 없어 status 가 영영 0 이다) ErrMpsDaemonNotReady
// 는 성공으로 취급한다 — 이 테스트들의 관심사는 참조 카운트와 정리다.
func seedMpsDaemon(t *testing.T, ctx context.Context, r *AcceleratorPartitionPolicyReconciler, self, node string) {
	t.Helper()
	if err := r.ensureMpsControlDaemon(ctx, npuv1alpha1.SharingModeMPS, self, node); err != nil && !errors.Is(err, ErrMpsDaemonNotReady) {
		t.Fatalf("daemon 사전 생성 실패: %v", err)
	}
}

// mps 모드에서는 control daemon 이 반드시 떠야 한다 — DP 만으로는 MPS 가 동작하지 않는다.
// 그리고 갓 만든 DS 는 "보장됐다" 가 아니다: pod 가 아직 스케줄조차 안 됐으므로
// 여기서 nil 을 반환하면 호출자가 곧바로 배선·검증으로 진행해 daemon 없는 mps 를 Ready 로 찍는다.
func TestMpsControlDaemon_CreatedForMPSMode(t *testing.T) {
	r := newMpsTestReconciler()
	err := r.ensureMpsControlDaemon(context.Background(), npuv1alpha1.SharingModeMPS, "", "worker1")
	if !errors.Is(err, ErrMpsDaemonNotReady) {
		t.Fatalf("갓 만든 DS 는 미준비로 보고돼야 한다: err=%v", err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(context.Background(), types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds); err != nil {
		t.Fatalf("DS 가 없다: %v", err)
	}
	// daemon 이 client 프로세스를 보려면 PID 네임스페이스 공유가 필요하다(upstream 기본값도
	// shareProcessNamespace) — hostIPC 는 다른 커널 기능이라 이 채널에 쓰이지 않는다.
	if ds.Spec.Template.Spec.ShareProcessNamespace == nil || !*ds.Spec.Template.Spec.ShareProcessNamespace {
		t.Error("shareProcessNamespace 없이는 daemon 이 client 프로세스를 보지 못한다")
	}
	if !nvidiaPresentRequired(&ds) {
		t.Errorf("nvidia 노드에만 떠야 한다: %+v", ds.Spec.Template.Spec.Affinity)
	}
	// daemon 바이너리는 ContainerRoot="/mps" 를 하드코딩한다(설정 불가, upstream
	// cmd/mps-control-daemon/mps/root.go:26) — 컨테이너 내부 마운트 경로가 반드시 "/mps" 여야
	// 한다. 호스트 쪽 Path 는 계속 mpsPipeDir(DP 와 공유하는 경로)이어도 된다.
	var mountPath string
	for _, m := range ds.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "mps-pipe" {
			mountPath = m.MountPath
		}
	}
	if mountPath != "/mps" {
		t.Errorf("daemon 컨테이너의 mps-pipe 마운트 경로 = %q, want \"/mps\"(하드코딩된 ContainerRoot)", mountPath)
	}
}

// nvidiaPresentRequired 는 렌더된 DS 가 nvidia 노드로 스케줄을 한정하는지 본다(NodeSelector 든
// NodeAffinity 든 — 배선 수단이 아니라 "nvidia 노드에만 뜬다"는 성질을 고정한다).
func nvidiaPresentRequired(ds *appsv1.DaemonSet) bool {
	if ds.Spec.Template.Spec.NodeSelector["kcloud.ai/nvidia.present"] == labelValueTrue {
		return true
	}
	for _, term := range nodeSelectorTerms(ds) {
		for _, e := range term.MatchExpressions {
			if e.Key == "kcloud.ai/nvidia.present" && e.Operator == corev1.NodeSelectorOpIn &&
				len(e.Values) == 1 && e.Values[0] == labelValueTrue {
				return true
			}
		}
	}
	return false
}

func nodeSelectorTerms(ds *appsv1.DaemonSet) []corev1.NodeSelectorTerm {
	aff := ds.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil || aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return nil
	}
	return aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
}

// daemon 은 device-plugin 과 **같은** sharing ConfigMap 을 마운트하고 그것을 가리키는
// CONFIG_FILE 을 들어야 한다. 없으면 이미지 내장 기본값(sharing.timeSlicing{})으로 기동해
// strategy=none 으로 판정하고 MPS 서버를 아예 띄우지 않는다. upstream(daemonset-mps-control-
// daemon.yml)도 같은 CM 을 mps-control-daemon 에 마운트한다. 마운트 대상은 항상 flat CM 이다
// — mig-active 노드는 mpsBlockedByMigStrategy 가 MPS 자체를 거절하므로 mixed CM 을 볼 일이 없다.
func TestMpsControlDaemon_MountsFlatSharingConfig(t *testing.T) {
	ds := renderMpsControlDaemonDS()

	var vol *corev1.Volume
	for i := range ds.Spec.Template.Spec.Volumes {
		if ds.Spec.Template.Spec.Volumes[i].Name == nvidia.SharingVolumeName {
			vol = &ds.Spec.Template.Spec.Volumes[i]
		}
	}
	if vol == nil || vol.ConfigMap == nil {
		t.Fatalf("★ sharing ConfigMap 볼륨이 없다 — daemon 이 이미지 내장 기본값으로 기동한다: %+v", ds.Spec.Template.Spec.Volumes)
	}
	if vol.ConfigMap.Name != nvidia.SharingConfigMapNameFlat {
		t.Errorf("마운트 대상 = %q, want %q(flat)", vol.ConfigMap.Name, nvidia.SharingConfigMapNameFlat)
	}

	ctr := ds.Spec.Template.Spec.Containers[0]
	wantDir := path.Dir(nvidia.SharingConfigPath)
	var mounted string
	for _, m := range ctr.VolumeMounts {
		if m.Name == nvidia.SharingVolumeName {
			mounted = m.MountPath
		}
	}
	if mounted != wantDir {
		t.Errorf("sharing config 마운트 경로 = %q, want %q", mounted, wantDir)
	}
	// upstream 은 mps-control-daemon-ctr 에 CONFIG_FILE 을 준다(cmd/mps-control-daemon/main.go 의
	// --config-file EnvVars). 볼륨만 붙이고 이걸 빼면 daemon 은 파일을 읽지 않는다.
	var configFile string
	for _, e := range ctr.Env {
		if e.Name == "CONFIG_FILE" {
			configFile = e.Value
		}
	}
	if configFile != nvidia.SharingConfigPath {
		t.Errorf("★ CONFIG_FILE = %q, want %q", configFile, nvidia.SharingConfigPath)
	}
}

// mig-active 노드에는 MPS 가 올 수 없다(mpsBlockedByMigStrategy 가 거절) — daemon 도 갈
// 이유가 없다. flat device-plugin DS(buildNvidiaDevicePluginDS)와 같은 DoesNotExist 규칙을 쓴다.
// 그 노드로 스케줄되면 mount 할 flat CM 조차 그 노드의 정책과 무관하다.
func TestMpsControlDaemon_SkipsMigActiveNodes(t *testing.T) {
	ds := renderMpsControlDaemonDS()
	for _, term := range nodeSelectorTerms(ds) {
		for _, e := range term.MatchExpressions {
			if e.Key == nvidia.MigActiveNodeLabel && e.Operator == corev1.NodeSelectorOpDoesNotExist {
				return
			}
		}
	}
	t.Fatalf("★ mig-active 노드를 제외하지 않는다: nodeSelector=%v affinity=%+v",
		ds.Spec.Template.Spec.NodeSelector, ds.Spec.Template.Spec.Affinity)
}

// kcloud-nvidia-toolkit(toolkit_daemonset_controller.go)은 control-plane/master
// 노드를 제외하고 그 노드에는 containerd nvidia 런타임이 없다. daemon 은 RuntimeClassName: nvidia
// 를 요구하므로 exclusion 없이 control-plane 노드(kcloud.ai/nvidia.present=true 라벨을 가진
// k8s-master)로 스케줄되면 FailedCreatePodSandBox("no runtime for nvidia")로 영구 대기한다.
// toolkit 이 이미 쓰는 applyControlPlaneExclusion(control-plane·master 두 라벨 DoesNotExist)과
// 같은 규칙을 daemon 도 걸어야 한다.
func TestMpsControlDaemon_SkipsControlPlaneNodes(t *testing.T) {
	ds := renderMpsControlDaemonDS()
	for _, term := range nodeSelectorTerms(ds) {
		hasControlPlane, hasMaster := false, false
		for _, e := range term.MatchExpressions {
			if e.Key == controlPlaneNodeLabel && e.Operator == corev1.NodeSelectorOpDoesNotExist {
				hasControlPlane = true
			}
			if e.Key == masterNodeLabel && e.Operator == corev1.NodeSelectorOpDoesNotExist {
				hasMaster = true
			}
		}
		if hasControlPlane && hasMaster {
			return
		}
	}
	t.Fatalf("★ control-plane/master 노드를 제외하지 않는다 — nvidia 런타임 없는 노드에 스케줄되면 "+
		"영구 ContainerCreating 이 된다: affinity=%+v", ds.Spec.Template.Spec.Affinity)
}

// control daemon 과 device-plugin 이 호스트 MPS root 로 서로 다른 경로를 보면 같은 파이프
// 디렉터리를 보지 못한다. 두 렌더러가 서로 다른 패키지에 있으므로 여기서 실제 산출물을
// 맞대어 고정한다.
func TestMpsRootWiringIsSymmetricBetweenDaemonAndDevicePlugin(t *testing.T) {
	daemonHost, daemonCtr := mpsRootMountOf(renderMpsControlDaemonDS(), "mps-pipe")

	dp := &appsv1.DaemonSet{Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin"}}},
	}}}
	nvidia.WireSharing(dp, npuv1alpha1.SharingModeMPS, nvidia.SharingConfigMapNameFlat)
	dpHost, dpCtr := mpsRootMountOf(dp, "mps-pipe")

	if daemonHost != dpHost {
		t.Errorf("★ 호스트 경로가 다르다: daemon=%q device-plugin=%q — 같은 파이프를 못 본다", daemonHost, dpHost)
	}
	if daemonCtr != dpCtr {
		t.Errorf("★ 컨테이너 경로가 다르다: daemon=%q device-plugin=%q — 두 바이너리 모두 %q 를 하드코딩한다",
			daemonCtr, dpCtr, nvidia.MPSContainerRoot)
	}
	if daemonCtr != nvidia.MPSContainerRoot {
		t.Errorf("컨테이너 경로 = %q, want %q", daemonCtr, nvidia.MPSContainerRoot)
	}
}

// upstream(daemonset-mps-control-daemon.yml)은 mps-control-daemon-ctr 앞에 mount-shm init
// container 를 둔다 — /mps 에 sized tmpfs 를 미리 깔아 host 재부팅 후 남은 shm 이나 크기 상한
// 없는 디렉터리를 그대로 쓰지 않게 한다. 이 테스트가 그 init container 를 고정한다.
func TestMPSDaemonHasMountShmInitContainer(t *testing.T) {
	ds := renderMpsControlDaemonDS()
	if len(ds.Spec.Template.Spec.InitContainers) == 0 {
		t.Fatalf("mount-shm init container 가 없다")
	}
	ic := ds.Spec.Template.Spec.InitContainers[0]
	if ic.Name != "mps-control-daemon-mounts" {
		t.Fatalf("init container 이름 = %q", ic.Name)
	}
	if got := strings.Join(ic.Command, " "); got != "mps-control-daemon mount-shm" {
		t.Fatalf("command = %q", got)
	}
	if ic.SecurityContext == nil || ic.SecurityContext.Privileged == nil || !*ic.SecurityContext.Privileged {
		t.Fatalf("mount-shm 은 privileged 여야 한다")
	}
	var bidi bool
	for _, m := range ic.VolumeMounts {
		if m.MountPropagation != nil && *m.MountPropagation == corev1.MountPropagationBidirectional {
			bidi = true
			// mount-shm 이 tmpfs 를 까는 자리가 daemon 컨테이너가 실제로 읽는 mps-root 와
			// 어긋나면 daemon 은 아무도 안 보는 곳에 shm 을 깔게 된다 — 렌더 필드만 보는
			// 이웃 테스트들은 이 어긋남을 잡지 못한다.
			if m.MountPath != nvidia.MPSContainerRoot {
				t.Fatalf("mount-shm 마운트 경로 = %q, want %q", m.MountPath, nvidia.MPSContainerRoot)
			}
		}
	}
	if !bidi {
		t.Fatalf("mps-root 마운트는 Bidirectional 이어야 host 로 전파된다: %+v", ic.VolumeMounts)
	}
	if ic.Image != ds.Spec.Template.Spec.Containers[0].Image {
		t.Fatalf("init container 이미지는 daemon 과 같아야 한다: %q vs %q", ic.Image, ds.Spec.Template.Spec.Containers[0].Image)
	}
}

// mpsRootMountOf 는 볼륨의 hostPath 와 그 볼륨이 컨테이너에 걸리는 경로를 함께 돌려준다.
func mpsRootMountOf(ds *appsv1.DaemonSet, volume string) (hostPath, mountPath string) {
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == volume && v.HostPath != nil {
			hostPath = v.HostPath.Path
		}
	}
	for _, m := range ds.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == volume {
			mountPath = m.MountPath
		}
	}
	return hostPath, mountPath
}

// 회귀: time-slicing 은 MPS daemon 을 쓰지 않으므로 MPS 마운트가 붙으면 안 된다.
func TestWireSharingTimeSlicedHasNoMpsMounts(t *testing.T) {
	dp := &appsv1.DaemonSet{Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "nvidia-device-plugin"}}},
	}}}
	nvidia.WireSharing(dp, npuv1alpha1.SharingModeTimeSliced, nvidia.SharingConfigMapNameFlat)
	for _, m := range dp.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == "mps-pipe" || m.Name == "mps-shm" {
			t.Errorf("time-sliced 에 MPS 마운트가 붙었다: %+v", m)
		}
	}
	for _, e := range dp.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "MPS_ROOT" {
			t.Errorf("time-sliced 에 MPS_ROOT 가 붙었다: %+v", e)
		}
	}
}

// 모드가 mps 가 아니면 남아 있으면 안 된다(멱등 정리).
func TestMpsControlDaemon_RemovedForOtherModes(t *testing.T) {
	for _, mode := range []string{npuv1alpha1.SharingModeExclusive, npuv1alpha1.SharingModeTimeSliced} {
		t.Run(mode, func(t *testing.T) {
			r := newMpsTestReconciler()
			ctx := context.Background()
			seedMpsDaemon(t, ctx, r, "", "worker1")
			if err := r.ensureMpsControlDaemon(ctx, mode, "", "worker1"); err != nil {
				t.Fatalf("정리 실패: %v", err)
			}
			var ds appsv1.DaemonSet
			err := r.Get(ctx, types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds)
			if err == nil {
				t.Fatal("DS 가 남아있다")
			}
			if !apierrors.IsNotFound(err) {
				t.Fatalf("예상치 못한 오류: %v", err)
			}
		})
	}
}

// DS 가 없는 상태에서 비활성 호출은 no-op 이어야 한다(멱등).
func TestMpsControlDaemon_DisabledNoopWhenAbsent(t *testing.T) {
	r := newMpsTestReconciler()
	if err := r.ensureMpsControlDaemon(context.Background(), npuv1alpha1.SharingModeExclusive, "", "worker1"); err != nil {
		t.Fatalf("no-op 이어야 하는데 오류: %v", err)
	}
}

// daemon 은 클러스터 전역 singleton 이라 참조 카운트가 없으면, mps 를
// 전혀 모르는 다른 ACPP(B)의 평범한 reconcile 이나 삭제가 A 가 아직 쓰는 daemon 을 지운다.
func TestMpsControlDaemon_SurvivesUnrelatedPolicyReconcileAndDeletion(t *testing.T) {
	ctx := context.Background()
	policyA := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-a", UID: "uid-a"},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			ApplyRecords: []npuv1alpha1.ApplyRecord{{NodeName: "node1", SharingMode: npuv1alpha1.SharingModeMPS}},
		},
	}
	policyB := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-b", UID: "uid-b"},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			// 평범한 MIG 파티셔닝 정책 — sharing 을 전혀 모른다(SharingMode 는 항상 빈 값).
			ApplyRecords: []npuv1alpha1.ApplyRecord{{NodeName: "node2"}},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node2", Annotations: map[string]string{migOwnerAnnotation: string(policyB.UID)}},
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(policyA, policyB, node2).
		WithStatusSubresource(&npuv1alpha1.AcceleratorPartitionPolicy{}).Build()
	r := &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme()}

	// A 가 이미 daemon 을 띄워 둔 상태를 흉내낸다.
	seedMpsDaemon(t, ctx, r, policyA.Name, "node1")
	assertDaemonExists := func(step string) {
		t.Helper()
		var ds appsv1.DaemonSet
		if err := r.Get(ctx, types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds); err != nil {
			t.Fatalf("%s: daemon 이 사라졌다 — A 는 여전히 mps 를 쓴다: %v", step, err)
		}
	}

	// B 의 평범한 reconcile(disableSharing 이 부르는 것과 같은 호출)이 daemon 을 지우면 안 된다.
	if err := r.ensureMpsControlDaemon(ctx, npuv1alpha1.SharingModeExclusive, policyB.Name, "node2"); err != nil {
		t.Fatal(err)
	}
	assertDaemonExists("B 의 평범한 reconcile 이후")

	// B 삭제도 마찬가지다.
	if err := r.handleNvidiaDeletion(ctx, policyB); err != nil {
		t.Fatalf("B 삭제 실패: %v", err)
	}
	assertDaemonExists("B 삭제 이후")
}

// DeletionTimestamp 있는 모든 정책을 넓게 제외하면, CleanupBlocked 로 멈춘 정책(삭제는 시작됐지만
// 자기 mps 배선은 아직 롤백 못 함)까지 참조 카운트에서 영구히 빠져 무관한 정책의 평범한
// reconcile 이 daemon 을 지워버린다. self(호출한 정책) 하나만 제외해야 한다.
// 차단 상태는 손으로 조립하지 않고 실제 삭제 경로(handleNvidiaDeletion 의 소유권 충돌 분기 →
// setDeletionBlocked)로 만든다 — 차단된 정책은 DeletionTimestamp + finalizer 를 유지한 채 mps
// 기록이 롤백되지 않고 남는다는 전제까지 같이 고정된다.
func TestMpsControlDaemon_SurvivesCleanupBlockedSiblingsMPSRecord(t *testing.T) {
	ctx := context.Background()
	blocked := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "acpp-blocked", UID: "uid-blocked",
			Finalizers: []string{acppFinalizer}, // Delete 가 지우지 않고 DeletionTimestamp 만 찍게.
		},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			ApplyRecords: []npuv1alpha1.ApplyRecord{{
				NodeName: "node1", MigPhase: npuv1alpha1.MigPhaseReady, // phase≥Applying → 하드웨어 변경됨.
				SharingMode: npuv1alpha1.SharingModeMPS, SharingReplicas: 4,
				GPUPCIs: []string{sharingPCI}, BaselineGPUCount: 1, ExpectedFullGPUCount: 1,
			}},
		},
	}
	other := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-other", UID: "uid-other"},
	}
	// node1 의 owner lock 이 남의 것 → 삭제가 소유권 충돌로 들어간다. allocatable 도 회수 후
	// 기대값(ExpectedFullGPUCount=1, MIG 대상이 아닌 GPU 한 장)으로 복원돼 있지 않아
	// assertMigEmptyAndBaseline 이 실패하고 CleanupBlocked 로 차단된다.
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node1", Annotations: map[string]string{migOwnerAnnotation: "uid-someone-else"}},
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(blocked, other, node1).
		WithStatusSubresource(&npuv1alpha1.AcceleratorPartitionPolicy{}).
		// sharingFixture 와 같은 인덱스 — 없으면 삭제 경로의 DP 재시작이 인위적으로 실패해,
		// 이 spec 이 검증하려는 참조 카운트가 아니라 그 실패에 기대어 통과한다.
		WithIndex(&corev1.Pod{}, "spec.nodeName", func(o client.Object) []string {
			return []string{o.(*corev1.Pod).Spec.NodeName}
		}).Build()
	r := &AcceleratorPartitionPolicyReconciler{Client: c, Scheme: c.Scheme()}
	r.NvidiaObserverFactory = func(client.Client) nvidia.Observer { return emptyObserver{} }

	seedMpsDaemon(t, ctx, r, blocked.Name, "node1")

	// 실제 삭제 경로로 CleanupBlocked 를 만든다.
	if err := c.Delete(ctx, blocked); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Name: blocked.Name}, blocked); err != nil {
		t.Fatal(err)
	}
	if err := r.handleNvidiaDeletion(ctx, blocked); err == nil {
		t.Fatal("소유권 충돌 + 복원 미증명이면 삭제가 차단돼야 한다(CleanupBlocked)")
	}
	var stored npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.Get(ctx, types.NamespacedName{Name: blocked.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Status.ApplyRecords[0].MigPhase; got != npuv1alpha1.MigPhaseCleanupBlocked {
		t.Fatalf("차단 상태에 도달하지 못했다: MigPhase=%q", got)
	}
	if stored.DeletionTimestamp.IsZero() || len(stored.Finalizers) == 0 {
		t.Fatalf("차단된 정책은 DeletionTimestamp + finalizer 를 유지해야 이 케이스가 성립한다")
	}
	if got := stored.Status.ApplyRecords[0].SharingMode; got != npuv1alpha1.SharingModeMPS {
		t.Fatalf("롤백되지 않은 mps 기록이 남아 있어야 한다: SharingMode=%q", got)
	}

	// other 의 평범한 reconcile 이 daemon 을 지우면 안 된다 — blocked 가 아직 mps 를 쓴다.
	if err := r.ensureMpsControlDaemon(ctx, npuv1alpha1.SharingModeExclusive, other.Name, "node1"); err != nil {
		t.Fatal(err)
	}
	var ds appsv1.DaemonSet
	if err := r.Get(ctx, types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}, &ds); err != nil {
		t.Fatalf("daemon 이 사라졌다 — CleanupBlocked 인 정책이 아직 mps 를 쓴다: %v", err)
	}
}
