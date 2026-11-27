// ============================================================
// state_machine_drain_gate_test.go: drain 게이트의 장치 점유 판정 단위 테스트
// 상세: device-plugin 축과 DRA 축을 모두 보고 initContainers·Pending 단계를 빠뜨리지 않는
//       노드 비움 판정을 고정한다. fake client 기반 — envtest 불필요.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================

package upgrade

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

const (
	drainGateNode   = "n-drain"
	drainGateVendor = "nvidia"
)

// makeDrainingDUS 는 Draining 상태의 DUS 를 반환한다(LastTransitionTime=now 로 drain 타임아웃 회피).
func makeDrainingDUS() *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: drainGateNode + "-" + drainGateVendor},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: drainGateNode,
			Vendor:   drainGateVendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              v1alpha1.UpgradeStateDraining,
			LastTransitionTime: metav1.Now(),
		},
	}
}

// makeDrainEnabledDIP 는 drain 활성화 DIP 를 반환한다(ForceUpgrade=false, RollbackOnFailure=false).
func makeDrainEnabledDIP() *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: drainGateVendor},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor:        drainGateVendor,
			UpgradePolicy: &v1alpha1.UpgradePolicy{DrainEnabled: true},
		},
	}
}

// makeDevicePod 는 지정 단계의 Pod 를 반환한다. limits 는 호출자가 컨테이너에 붙인다.
func makeDevicePod(name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName:   drainGateNode,
			Containers: []corev1.Container{{Name: "app", Image: "busybox"}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

// makeDevicePodOnNode 는 지정 노드·단계의 Pod 를 반환한다 — 노드 이름 축 시험용.
func makeDevicePodOnNode(name, nodeName string, phase corev1.PodPhase) *corev1.Pod {
	pod := makeDevicePod(name, phase)
	pod.Spec.NodeName = nodeName
	return pod
}

// deviceLimits 는 가속기 extended resource 1개를 요청하는 ResourceList 다.
func deviceLimits(resName string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceName(resName): resource.MustParse("1")}
}

// assertStaysDraining 은 handleDraining 이 Upgrading 으로 넘어가지 않고 재조정을 예약하는지 본다.
func assertStaysDraining(t *testing.T, sm *UpgradeStateMachine, dus *v1alpha1.DriverUpgradeState,
	dip *v1alpha1.DriverInstallPolicy) {
	t.Helper()
	requeue, after, err := sm.handleDraining(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleDraining 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateDraining {
		t.Fatalf("장치를 쥔 Pod 가 있는데 Draining 을 벗어났다: got=%s", dus.Status.State)
	}
	if !requeue || after == 0 {
		t.Fatalf("drain 미완인데 재조정 예약이 없다: requeue=%v after=%v", requeue, after)
	}
}

// ── 시험 1: DRA 축(resourceClaims)만 쥔 Pod ──────────────────────────
// limits 가 비고 resourceClaims 만 있는 Pod 는 DRA 로 장치를 쥔 Pod 다. limits 만 보는
// 판정은 이것을 "아무것도 안 쥔 Pod" 으로 보고 그대로 커널 모듈 교체를 내보낸다.
func TestHandleDrainingDRAClaimPodBlocksUpgrading(t *testing.T) {
	pod := makeDevicePod("dra-pod", corev1.PodRunning)
	claimName := "gpu-claim"
	pod.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "gpu", ResourceClaimName: &claimName}}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	assertStaysDraining(t, newUpgradeSMWithRecorder(dus, dip, pod), dus, dip)
}

// ── 시험 2: initContainer 만 장치를 요청하는 Pod ─────────────────────
// initContainer 가 장치를 쥐는 동안에도 커널 모듈은 사용 중이다.
func TestHandleDrainingInitContainerDevicePodBlocksUpgrading(t *testing.T) {
	pod := makeDevicePod("init-pod", corev1.PodRunning)
	pod.Spec.InitContainers = []corev1.Container{{
		Name: "warmup", Image: "busybox",
		Resources: corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")},
	}}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	assertStaysDraining(t, newUpgradeSMWithRecorder(dus, dip, pod), dus, dip)
}

// ── 시험 3: Pending 단계의 장치 요청 Pod ─────────────────────────────
// 스케줄만 되고 아직 시작되지 않은 Pod 는 교체 도중에 시작된다. 종료 단계만 제외한다.
func TestHandleDrainingPendingDevicePodBlocksUpgrading(t *testing.T) {
	pod := makeDevicePod("pending-pod", corev1.PodPending)
	pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	assertStaysDraining(t, newUpgradeSMWithRecorder(dus, dip, pod), dus, dip)
}

// ── 시험 3-B: Pending Pod 가 eviction 대상에 들어간다 ────────────────
// hasDeviceWorkloads 가 Pending 을 세기 시작하면 evictDevicePods 도 같은 집합을 봐야 한다.
// 아니면 Draining 이 참인 채 eviction 이 그 Pod 를 건너뛰어 drain 타임아웃까지 머문다.
func TestEvictDevicePodsIncludesPendingPod(t *testing.T) {
	pod := makeDevicePod("pending-evict", corev1.PodPending)
	pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")}

	dus := makeDrainingDUS()
	sm := newUpgradeSMWithRecorder(dus, pod)
	if err := sm.evictDevicePods(context.Background(), dus, false); err != nil {
		t.Fatalf("evictDevicePods 실패: %v", err)
	}

	var got corev1.Pod
	err := sm.Get(context.Background(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &got)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Pending 장치 Pod 가 eviction 대상에서 빠졌다: get err=%v", err)
	}
}

// ── 시험 3-C: 종료 단계 Pod 는 drain 을 막지 않는다 ──────────────────
// Pending 을 포함시키는 변경이 "모든 단계를 센다" 로 번지지 않았는지 본다.
func TestHandleDrainingSucceededDevicePodAllowsUpgrading(t *testing.T) {
	pod := makeDevicePod("done-pod", corev1.PodSucceeded)
	pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, pod)
	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleDraining 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("종료 단계 Pod 만 남았는데 drain 이 끝나지 않았다: got=%s", dus.Status.State)
	}
}

// ── 시험 5: 확장 리소스 판정 범위가 좁지 않다 ─────────────────────────
// 판정은 intent.AcceleratorResourceDomains 에 부분 일치를 걸어 벤더를 가리지 않는다.
// 벤더별 정확 일치·접두 일치로 바꾸면 device-plugin 변형 이름(gpu.shared 등)과
// 카탈로그 밖 제품명이 조용히 빠진다.
func TestPodUsesDeviceKeepsVendorAgnosticSubstringMatch(t *testing.T) {
	caught := []string{
		"nvidia.com/gpu",
		"nvidia.com/mig-1g.5gb",
		"nvidia.com/gpu.shared",
		"furiosa.ai/rngd",
		"beta.furiosa.ai/npu",
		"rebellions.ai/ATOM",
		"rebellions.ai/ATOM-plus",
		"tenstorrent.com/blackhole",
		"tenstorrent.com/wormhole",
	}
	for _, rn := range caught {
		pod := makeDevicePod("p", corev1.PodRunning)
		pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits(rn)}
		if !podUsesDevice(pod) {
			t.Errorf("가속기 리소스를 놓쳤다: %s", rn)
		}
	}

	ignored := []string{"cpu", "memory", "example.com/fpga"}
	for _, rn := range ignored {
		pod := makeDevicePod("p", corev1.PodRunning)
		pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits(rn)}
		if podUsesDevice(pod) {
			t.Errorf("가속기가 아닌 리소스를 장치 점유로 봤다: %s", rn)
		}
	}
}

// ── 시험 4: resource.k8s.io 를 서빙하지 않는 클러스터 ────────────────
// claim 축 조회가 "그 축이 없다" 로 돌아오면 넘긴다. 부재를 이유로 게이트를 막으면 DRA 가
// 꺼진 클러스터에서 drain 이 영원히 끝나지 않는다. 그런 클러스터가 내는 실제 오류는
// RESTMapper 의 NoKindMatchError 다 — scheme 에서 타입만 빼면 fake client 는 다른 오류("no
// kind is registered")를 내 넘기는 분기를 밟지 못하므로(바이너리가 타입을 모르는 경우라
// 운영에서는 일어나지 않는다), interceptor 로 운영과 같은 오류를 돌려준다.
func TestHandleDrainingSkipsClaimGateWhenDRANotServed(t *testing.T) {
	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	sm := &UpgradeStateMachine{
		Client: fake.NewClientBuilder().WithScheme(newUpgradeTestScheme()).
			WithObjects(dus, dip).
			WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList,
					opts ...client.ListOption) error {
					if _, isClaims := list.(*resourcev1.ResourceClaimList); isClaims {
						return &apimeta.NoKindMatchError{
							GroupKind:        schema.GroupKind{Group: resourcev1.GroupName, Kind: "ResourceClaim"},
							SearchedVersions: []string{"v1"},
						}
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Recorder: record.NewFakeRecorder(64),
	}

	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("resource.k8s.io 미서빙 클러스터에서 재조정이 실패했다: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("장치를 쥔 것이 없는데 drain 이 끝나지 않았다: got=%s", dus.Status.State)
	}
}

// ── 시험 4-B: 이 노드에 할당된 claim 이 남으면 막는다 ────────────────
// Pod 축이 비어도 claim 축은 따로 산다 — Pod 이 사라져도 deallocate 되기 전에는 장치가 잡혀
// 있다. 게이트가 실제로 막는지를 고정하지 않으면 시험 4 는 "한 번도 안 막는 게이트" 로도 통과한다.
// 막는 방식은 Pod 축과 같은 정상 재조정이다(오류가 아니다). 막은 주체를 이름으로 특정하는 것과
// 읽기 실패가 여전히 오류인지는 state_machine_claim_gate_test.go 가 맡는다.
func TestHandleDrainingAllocatedClaimBlocksUpgrading(t *testing.T) {
	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: drainGateNode}}
	sm := newUpgradeSMWithRecorder(dus, dip, node, allocatedClaimOnDrainGateNode())
	assertStaysDraining(t, sm, dus, dip)
}

// ── 시험 6: 다른 노드의 장치 Pod 는 이 노드의 drain 을 막지 않는다 ──────
// 판정이 노드 이름을 보지 않고 "장치를 쥔 Pod 가 클러스터에 있는가" 로 느슨해지면
// 관련 없는 노드의 Pod 하나가 모든 노드의 drain 을 영원히 막는다.
func TestHandleDrainingOtherNodeDevicePodDoesNotBlockUpgrading(t *testing.T) {
	pod := makeDevicePodOnNode("other-node-pod", "n-other", corev1.PodRunning)
	pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, pod)
	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleDraining 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("다른 노드의 장치 Pod 가 이 노드의 drain 을 막았다: got=%s", dus.Status.State)
	}
}

// ── 시험 7: Failed 단계의 장치 Pod 는 drain 을 막지 않는다 ──────────────
// podPhaseDrainable 이 제외하는 종료 단계는 Succeeded·Failed 둘이다. Succeeded 만
// 시험하면 Failed 분기가 한 번도 실행되지 않고도 통과한다.
func TestHandleDrainingFailedDevicePodAllowsUpgrading(t *testing.T) {
	pod := makeDevicePod("failed-pod", corev1.PodFailed)
	pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{Limits: deviceLimits("nvidia.com/gpu")}

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, pod)
	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleDraining 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("Failed 단계 Pod 만 남았는데 drain 이 끝나지 않았다: got=%s", dus.Status.State)
	}
}

// ── 시험 8: 장치를 쓰지 않는 Running Pod 만 있으면 drain 이 끝난다 ──────
// 앞의 시험들은 장치 Pod 가 있을 때 막히는지만 본다 — 판정 함수가 무조건 true 를
// 반환해도 전부 통과한다. 장치와 무관한 Pod 만 있을 때 실제로 Upgrading 으로
// 넘어가는지를 봐야 판정이 상수 true 로 퇴화하는 것을 잡아낸다.
func TestHandleDrainingNoDevicePodAllowsUpgrading(t *testing.T) {
	pod := makeDevicePod("plain-pod", corev1.PodRunning)

	dus, dip := makeDrainingDUS(), makeDrainEnabledDIP()
	sm := newUpgradeSMWithRecorder(dus, dip, pod)
	if _, _, err := sm.handleDraining(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleDraining 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUpgrading {
		t.Fatalf("장치를 쓰지 않는 Pod 만 있는데 drain 이 끝나지 않았다: got=%s", dus.Status.State)
	}
}
