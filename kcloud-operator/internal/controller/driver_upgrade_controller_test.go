// ============================================================
// driver_upgrade_controller_test.go: DriverUpgradeReconciler 단위 테스트
// 상세: ensureUpgradeStates()의 currentVersion 동기화 및 findPolicy fallback 로직 검증
//       fake client 기반 — envtest 불필요
// 생성일: 2026-04-21 | 수정일: 2026-10-28
// ============================================================

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/upgrade"

	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

// workerNode는 control-plane 라벨이 없는 일반 워커 노드를 반환합니다.
func workerNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{},
		},
	}
}

func makeNDR(nodeName, model, driverVersion string) *v1alpha1.NodeDeviceReport {
	return &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: nodeName},
		Status: v1alpha1.NodeDeviceReportStatus{
			Devices: []v1alpha1.DeviceEntry{
				{
					Vendor:        "furiosa",
					Model:         model,
					DriverVersion: driverVersion,
				},
			},
		},
	}
}

func makeDIP(name, model, version string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa",
			Model:  model,
			Driver: v1alpha1.DriverSpec{
				Version: version,
				Mode:    "daemonset",
			},
		},
	}
}

// vendor 는 지금 모든 호출이 furiosa 지만, 벤더별 시험을 추가할 때 그대로 쓰도록 남긴다.
//
//nolint:unparam
func makeDUS(name, nodeName, vendor, model, state, currentVersion, desiredVersion string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
			Model:    model,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:          state,
			CurrentVersion: currentVersion,
			DesiredVersion: desiredVersion,
		},
	}
}

func newReconciler(objs ...client.Object) *DriverUpgradeReconciler {
	s := newTestScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
		Build()
	return &DriverUpgradeReconciler{
		Client:       c,
		Scheme:       s,
		StateMachine: &upgrade.UpgradeStateMachine{Client: c},
	}
}

// nodeWithUpgradingLabel 은 driver-upgrading 라벨이 붙은 워커 노드를 반환합니다.
func nodeWithUpgradingLabel(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{upgrade.DriverUpgradingLabelKey: "true"},
		},
	}
}

// dusWithTransition 는 임의의 LastTransitionTime 을 가진 DUS 를 만든다.
func dusWithTransition(name, nodeName, vendor, state string, ago time.Duration) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              state,
			LastTransitionTime: metav1.NewTime(time.Now().Add(-ago)),
		},
	}
}

// TestEnsureUpgradeStates_CurrentVersionSyncFromNDR 는 DUS 가 Idle 상태이고 currentVersion 이
// 빈 문자열인데 NDR 이 desiredVersion 과 동일한 버전을 보고하면 currentVersion 이 채워지는지 본다.
func TestEnsureUpgradeStates_CurrentVersionSyncFromNDR(t *testing.T) {
	const (
		nodeName = "worker-1"
		vendor   = "furiosa"
		model    = "warboy"
		version  = "1.9.8-3"
		dusName  = "worker-1-furiosa"
	)

	node := workerNode(nodeName)
	ndr := makeNDR(nodeName, model, version)
	dip := makeDIP("furiosa-warboy", model, version)
	// DUS는 이미 존재하지만 currentVersion이 비어있음 (버그 재현 상태)
	dus := makeDUS(dusName, nodeName, vendor, model, v1alpha1.UpgradeStateIdle, "", version)

	r := newReconciler(node, ndr, dip, dus)

	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}

	if got.Status.CurrentVersion != version {
		t.Errorf("currentVersion 동기화 실패: got %q, want %q", got.Status.CurrentVersion, version)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state가 변경되어서는 안 됨: got %q", got.Status.State)
	}
}

// TestEnsureUpgradeStates_CreatesDUSWhenAbsent 는 NDR만 있고 DUS가 없을 때 DUS를 자동 생성하는지 검증합니다.
func TestEnsureUpgradeStates_CreatesDUSWhenAbsent(t *testing.T) {
	const (
		nodeName = "worker-2"
		vendor   = "furiosa"
		model    = "warboy"
		version  = "1.9.8-3"
	)

	node := workerNode(nodeName)
	ndr := makeNDR(nodeName, model, version)
	dip := makeDIP("furiosa-warboy", model, version)

	r := newReconciler(node, ndr, dip)

	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}

	dusName := driverUpgradeStateName(nodeName, vendor)
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 자동 생성 실패: %v", err)
	}

	if got.Status.CurrentVersion != version {
		t.Errorf("currentVersion: got %q, want %q", got.Status.CurrentVersion, version)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("초기 state: got %q, want %q", got.Status.State, v1alpha1.UpgradeStateIdle)
	}
}

// TestEnsureUpgradeStates_UpgradeRequiredOnVersionMismatch 는 NDR 버전이 DIP와 다를 때
// DUS가 UpgradeRequired 상태로 전이하는지 검증합니다.
func TestEnsureUpgradeStates_UpgradeRequiredOnVersionMismatch(t *testing.T) {
	const (
		nodeName     = "worker-3"
		vendor       = "furiosa"
		model        = "warboy"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-3-furiosa"
	)

	node := workerNode(nodeName)
	ndr := makeNDR(nodeName, model, installedVer)
	dip := makeDIP("furiosa-warboy", model, desiredVer)
	dus := makeDUS(dusName, nodeName, vendor, model, v1alpha1.UpgradeStateIdle, installedVer, desiredVer)

	r := newReconciler(node, ndr, dip, dus)

	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}

	// 이 경로는 관측만 한다. 업그레이드 진입은 게이트를 가진 상태기계가 정한다.
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state: got %q, want %q", got.Status.State, v1alpha1.UpgradeStateIdle)
	}
	if got.Status.CurrentVersion != installedVer {
		t.Errorf("currentVersion: got %q, want %q", got.Status.CurrentVersion, installedVer)
	}
	if got.Status.DesiredVersion != desiredVer {
		t.Errorf("desiredVersion: got %q, want %q", got.Status.DesiredVersion, desiredVer)
	}
}

// TestReconcile_GatesOpenEntersUpgrade 는 게이트가 열려 있으면 업그레이드가 실제로 시작되는 것을
// 고정한다. 위 시험이 "여기서는 전이하지 않는다" 만 단정하므로, 전이 자체가 사라지는 회귀를
// 이 시험이 잡는다.
func TestReconcile_GatesOpenEntersUpgrade(t *testing.T) {
	const (
		nodeName     = "worker-13"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-13-furiosa"
	)
	dip := dipWithGates(true, installedVer, desiredVer)
	dip.Spec.UpgradePolicy.IdleCooldownSeconds = ptr.To(int32(0))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, installedVer, desiredVer)
	r := newReconciler(workerNode(nodeName), makeNDR(nodeName, "warboy", installedVer), dip, dus)
	r.Recorder = record.NewFakeRecorder(64)
	r.StateMachine.Recorder = r.Recorder

	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: dusName}}); err != nil {
		t.Fatalf("Reconcile 오류: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("state: got %q, want %q — 게이트가 열렸는데 업그레이드로 진입하지 않았다",
			got.Status.State, v1alpha1.UpgradeStateRequired)
	}
}

// TestEnsureUpgradeStates_ResetsStaleRebootAttempts 는 새 모듈 교체 사이클 진입(버전 불일치) 시
// 이전 사이클의 stale RebootAttempts 가 0 으로 리셋됨을 검증한다. stale 이 남으면
// Validating→RebootRequired 게이트(need && RebootAttempts==0)가 막혀 자연 재부팅이 미발화한다.
func TestEnsureUpgradeStates_ResetsStaleRebootAttempts(t *testing.T) {
	const (
		nodeName     = "worker-1"
		vendor       = "furiosa"
		model        = "warboy"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-1-furiosa"
	)

	node := workerNode(nodeName)
	ndr := makeNDR(nodeName, model, installedVer)
	dip := makeDIP("furiosa-warboy", model, desiredVer)
	dus := makeDUS(dusName, nodeName, vendor, model, v1alpha1.UpgradeStateIdle, installedVer, desiredVer)
	dus.Status.RebootAttempts = 1 // 이전 사이클 잔존(stale)
	dus.Status.RebootBootID = "stale-boot-id"

	dip.Spec.UpgradePolicy = &v1alpha1.UpgradePolicy{AutoUpgrade: true, IdleCooldownSeconds: ptr.To(int32(0))}

	r := newReconciler(node, ndr, dip, dus)
	r.Recorder = record.NewFakeRecorder(64)
	r.StateMachine.Recorder = r.Recorder
	ctx := context.Background()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: dusName}}); err != nil {
		t.Fatalf("Reconcile 오류: %v", err)
	}

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("state: got %q, want Required", got.Status.State)
	}
	if got.Status.RebootAttempts != 0 {
		t.Errorf("RebootAttempts: got %d, want 0 (새 사이클 리셋)", got.Status.RebootAttempts)
	}
	if got.Status.RebootBootID != "" {
		t.Errorf("RebootBootID: got %q, want empty (새 사이클 리셋)", got.Status.RebootBootID)
	}
}

// TestFindPolicy_FallbackFirstWins 는 DIP가 2개일 때 첫 번째 fallback이 선택되는지 검증합니다.
func TestFindPolicy_FallbackFirstWins(t *testing.T) {
	dip1 := v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dip-first"},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa",
			Model:  "",
			Driver: v1alpha1.DriverSpec{Version: "1.9.8-3"},
		},
	}
	dip2 := v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dip-second"},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa",
			Model:  "",
			Driver: v1alpha1.DriverSpec{Version: "2.0.0"},
		},
	}

	got := findPolicy([]v1alpha1.DriverInstallPolicy{dip1, dip2}, "furiosa", "warboy")
	if got == nil {
		t.Fatal("findPolicy: nil 반환")
	}
	if got.Name != "dip-first" {
		t.Errorf("첫 번째 fallback이 선택되어야 함: got %q", got.Name)
	}
}

// TestFindPolicy_ProductFamilyPrefix 는 detector 가 제품명을 구체화한 뒤에도 제품군 이름으로
// 쓴 정책이 걸리는지 본다. 표준 PCI DB 계층이 붙으면 NVIDIA 장치 model 이 `a100` 이 아니라
// `a100-pcie-40gb`(실측 10de:20f1)가 되는데, 양쪽 다 "generic" 이 아니면 와일드카드도 안
// 걸려 findPolicy 가 nil 을 돌려줄 수 있다 — 증상이 "아무 일도 안 일어남" 이라 탐지되지 않는다.
// 접두어 경계를 요구하므로 다른 제품군은 걸리지 않아야 한다(과잉 매칭 방어).
func TestFindPolicy_ProductFamilyPrefix(t *testing.T) {
	family := *makeNvidiaDIP("nvidia-a100", "a100")
	other := *makeNvidiaDIP("nvidia-a30", "a30")

	got := findPolicy([]v1alpha1.DriverInstallPolicy{family}, "nvidia", "a100-pcie-40gb")
	if got == nil {
		t.Fatal("제품군 정책 a100 이 장치 a100-pcie-40gb 에 안 걸린다")
	}
	if got.Name != "nvidia-a100" {
		t.Errorf("걸린 정책 = %q, want nvidia-a100", got.Name)
	}

	if got := findPolicy([]v1alpha1.DriverInstallPolicy{other}, "nvidia", "a100-pcie-40gb"); got != nil {
		t.Errorf("다른 제품군 정책 a30 이 장치 a100-pcie-40gb 에 걸렸다: %q", got.Name)
	}

	// 정확일치가 제품군 정책을 이겨야 한다 — 순서상 제품군이 먼저 나와도.
	exact := *makeNvidiaDIP("nvidia-exact", "a100-pcie-40gb")
	got = findPolicy([]v1alpha1.DriverInstallPolicy{family, exact}, "nvidia", "a100-pcie-40gb")
	if got == nil || got.Name != "nvidia-exact" {
		t.Errorf("정확일치 우선이 깨졌다: got %v", got)
	}
}

// makeNvidiaDIP 는 nvidia DIP 픽스처다. 기존 makeDIP 는 vendor 가 furiosa 로 고정돼 있어
// 제품명 구체화(NVIDIA PCI DB) 시험에 쓸 수 없다.
func makeNvidiaDIP(name, model string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Model:  model,
			Driver: v1alpha1.DriverSpec{Version: "580.173.02", Mode: "daemonset"},
		},
	}
}

// TestSyncDUSModel_FollowsDeviceModel 는 장치 model 표기가 바뀌면 DriverUpgradeState.spec.model
// 이 따라오는지 본다. spec.model 은 생성 시점 값이라 갱신되지 않아 라이브에서 이미 어긋나 있을
// 수 있고(DUS `generic` 대 장치 `a30`), `update-pciids` 로 표기가 통째로 바뀌면 접두어·와일드카드
// 규칙으로도 매칭이 끊겨 재부팅 게이트와 자가복구 게이트가 조용히 죽는다.
func TestSyncDUSModel_FollowsDeviceModel(t *testing.T) {
	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1-nvidia"},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: "k8s-worker1", Vendor: "nvidia", Model: "generic",
		},
	}
	r := newReconciler(dus)

	r.syncDUSModel(context.Background(), dus, "a100-pcie-40gb")

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker1-nvidia"}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Spec.Model != "a100-pcie-40gb" {
		t.Errorf("spec.model = %q, want a100-pcie-40gb", got.Spec.Model)
	}

	// 장치 model 이 비면(관측 실패) 기록을 지우지 않는다 — 지우면 벤더 전체 와일드카드가 된다.
	r.syncDUSModel(context.Background(), &got, "")
	var after v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker1-nvidia"}, &after); err != nil {
		t.Fatalf("DUS 재조회 실패: %v", err)
	}
	if after.Spec.Model != "a100-pcie-40gb" {
		t.Errorf("빈 장치 model 에 기록이 덮였다: %q", after.Spec.Model)
	}
}

// TestVendorRepresentativeModels 는 벤더별 대표 model 선정을 본다. DUS 는 (노드, 벤더) 당
// 하나인데 장치는 벤더당 여럿일 수 있어(라이브 worker1 = nvidia a30 + a2), model 이 갈리면
// 단일 제품명이 없다는 뜻이라 "모른다"(generic) 로 남겨야 한다.
func TestVendorRepresentativeModels(t *testing.T) {
	for name, tc := range map[string]struct {
		devices []v1alpha1.DeviceEntry
		want    map[string]string
	}{
		"벤더 안에서 model 이 갈린다": {
			devices: []v1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "a30"}, {Vendor: "nvidia", Model: "a2"},
			},
			want: map[string]string{"nvidia": modelUnknown},
		},
		"셋 중 하나만 다르다": {
			devices: []v1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "a30"}, {Vendor: "nvidia", Model: "a2"},
				{Vendor: "nvidia", Model: "a30"},
			},
			want: map[string]string{"nvidia": modelUnknown},
		},
		"벤더 안에서 model 이 같다": {
			devices: []v1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "a30"}, {Vendor: "nvidia", Model: "a30"},
			},
			want: map[string]string{"nvidia": "a30"},
		},
		"장치 하나 — 기존 동작": {
			devices: []v1alpha1.DeviceEntry{{Vendor: "nvidia", Model: "a30"}},
			want:    map[string]string{"nvidia": "a30"},
		},
		"벤더가 섞여 있어도 벤더별로 판정": {
			devices: []v1alpha1.DeviceEntry{
				{Vendor: "furiosa", Model: "warboy"},
				{Vendor: "nvidia", Model: "a30"}, {Vendor: "nvidia", Model: "a2"},
			},
			want: map[string]string{"furiosa": "warboy", "nvidia": modelUnknown},
		},
	} {
		got := vendorRepresentativeModels(tc.devices)
		if len(got) != len(tc.want) {
			t.Errorf("%s: 벤더 수 = %d, want %d (%v)", name, len(got), len(tc.want), got)
		}
		for vendor, want := range tc.want {
			if got[vendor] != want {
				t.Errorf("%s: %s 대표 model = %q, want %q", name, vendor, got[vendor], want)
			}
		}
	}
}

// TestEnsureUpgradeStates_MixedModelsDoNotLoopWrites 는 혼재 노드에서 spec.model 이 서로를
// 덮는 영구 쓰기 루프가 없는지 본다. DUS 는 (노드, 벤더) 당 하나인데 장치마다 동기화하면
// 매 reconcile 마다 a30 → a2 로 patch 가 두 번 나간다 — API 서버에 쓰기 루프다.
// 같은 NDR 로 두 번 돌려 ResourceVersion 이 안 움직이는 것으로 고정한다.
func TestEnsureUpgradeStates_MixedModelsDoNotLoopWrites(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"}}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "k8s-worker1"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "a30", DriverVersion: "580.173.02"},
			{Vendor: "nvidia", Model: "a2", DriverVersion: "580.173.02"},
		}},
	}
	dip := makeNvidiaDIP("nvidia-generic", modelUnknown)
	r := newReconciler(node, ndr, dip)

	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("1차 ensureUpgradeStates: %v", err)
	}
	var first v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker1-nvidia"}, &first); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if first.Spec.Model != modelUnknown {
		t.Errorf("혼재 노드의 spec.model = %q, want %q", first.Spec.Model, modelUnknown)
	}

	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("2차 ensureUpgradeStates: %v", err)
	}
	var second v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker1-nvidia"}, &second); err != nil {
		t.Fatalf("DUS 재조회 실패: %v", err)
	}
	if second.ResourceVersion != first.ResourceVersion {
		t.Errorf("두 번째 reconcile 이 또 썼다: ResourceVersion %s → %s (model %q)",
			first.ResourceVersion, second.ResourceVersion, second.Spec.Model)
	}
}

// TestEnsureUpgradeStates_UniformModelIsKept 는 벤더 안 model 이 같으면 그 이름이 그대로
// spec.model 에 들어가는지 본다(혼재 대응이 단일 제품 노드를 generic 으로 뭉개면 안 된다).
func TestEnsureUpgradeStates_UniformModelIsKept(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker2"}}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker2"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "k8s-worker2"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "a30", DriverVersion: "580.173.02"},
			{Vendor: "nvidia", Model: "a30", DriverVersion: "580.173.02"},
		}},
	}
	dip := makeNvidiaDIP("nvidia-a30", "a30")
	r := newReconciler(node, ndr, dip)

	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("ensureUpgradeStates: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker2-nvidia"}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Spec.Model != "a30" {
		t.Errorf("단일 제품 노드의 spec.model = %q, want a30", got.Spec.Model)
	}
}

// TestFindPolicy_RngdModelMatch 는 같은 vendor(furiosa) 아래 warboy/rngd 2개 DIP 중
// vendor=furiosa, model=rngd 요청에 rngd DIP 가 정확히 매칭되는지 검증합니다.
func TestFindPolicy_RngdModelMatch(t *testing.T) {
	warboy := *makeDIP("furiosa-warboy", "warboy", "1.9.8-3")
	rngd := *makeDIP("furiosa-rngd", "rngd", "2026.1.0")

	// rngd 요청 → rngd DIP 반환
	got := findPolicy([]v1alpha1.DriverInstallPolicy{warboy, rngd}, "furiosa", "rngd")
	if got == nil {
		t.Fatal("findPolicy(furiosa, rngd): nil 반환")
	}
	if got.Name != "furiosa-rngd" {
		t.Errorf("furiosa/rngd 매칭 실패: got %q, want furiosa-rngd", got.Name)
	}

	// warboy 요청 → warboy DIP 반환 (회귀 방지)
	got = findPolicy([]v1alpha1.DriverInstallPolicy{warboy, rngd}, "furiosa", "warboy")
	if got == nil {
		t.Fatal("findPolicy(furiosa, warboy): nil 반환")
	}
	if got.Name != "furiosa-warboy" {
		t.Errorf("furiosa/warboy 매칭 실패: got %q, want furiosa-warboy", got.Name)
	}
}

// TestEnsureUpgradeStates_RngdCreatesDUS 는 vendor=furiosa, model=rngd 인 NDR+DIP 가
// 주어졌을 때 DUS 가 정확히 rngd model 로 생성되는지 검증합니다.
func TestEnsureUpgradeStates_RngdCreatesDUS(t *testing.T) {
	const (
		nodeName = "rngd-1"
		vendor   = "furiosa"
		model    = "rngd"
		version  = "2026.1.0"
	)

	node := workerNode(nodeName)
	ndr := makeNDR(nodeName, model, version)
	dip := makeDIP("furiosa-rngd", model, version)

	r := newReconciler(node, ndr, dip)

	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}

	dusName := driverUpgradeStateName(nodeName, vendor)
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("RNGD DUS 자동 생성 실패: %v", err)
	}

	if got.Spec.Model != model {
		t.Errorf("DUS.Spec.Model: got %q, want %q", got.Spec.Model, model)
	}
	if got.Status.CurrentVersion != version {
		t.Errorf("currentVersion: got %q, want %q", got.Status.CurrentVersion, version)
	}
}

// TestEnsureUpgradeStates_SkipsControlPlaneNode 는 control-plane 라벨이 있는 노드를 건너뛰는지 검증합니다.
func TestEnsureUpgradeStates_SkipsControlPlaneNode(t *testing.T) {
	const nodeName = "master-1"

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""},
		},
	}
	ndr := makeNDR(nodeName, "warboy", "1.9.8-3")
	dip := makeDIP("furiosa-warboy", "warboy", "1.9.8-3")

	r := newReconciler(node, ndr, dip)

	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}

	dusName := driverUpgradeStateName(nodeName, "furiosa")
	var got v1alpha1.DriverUpgradeState
	err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got)
	if err == nil {
		t.Error("control-plane 노드에 DUS가 생성되어서는 안 됨")
	}
}

// ─────────────────────────────────────────────
// L4 stuck-label sweep / defer cleanup 시나리오
// ─────────────────────────────────────────────

// hasUpgradingLabel 은 노드의 driver-upgrading 라벨 보유 여부를 반환합니다.
func hasUpgradingLabel(t *testing.T, r *DriverUpgradeReconciler, nodeName string) bool {
	t.Helper()
	var node corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: nodeName}, &node); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	_, ok := node.Labels[upgrade.DriverUpgradingLabelKey]
	return ok
}

// TestReconcile_DeferRemovesLabelInClosedStates 는 Reconcile 이 어떤 경로로 끝나든 사이클 종료
// 상태(HostAhead·UnverifiedVersion 포함)면 defer 가 driver-upgrading 라벨을 지우고, 진행 중
// 상태(Draining)면 남긴다는 것을 고정한다. 정책이 없어 Reconcile 이 오류로 끝나도 defer 는 돈다.
func TestReconcile_DeferRemovesLabelInClosedStates(t *testing.T) {
	for _, tc := range []struct {
		state     string
		wantLabel bool
	}{
		{v1alpha1.UpgradeStateHostAhead, false},
		{v1alpha1.UpgradeStateUnverifiedVersion, false},
		{v1alpha1.UpgradeStateDraining, true},
	} {
		t.Run(tc.state, func(t *testing.T) {
			const nodeName, dusName = "worker-defer", "worker-defer-furiosa"
			dus := dusWithTransition(dusName, nodeName, "furiosa", tc.state, 0)
			r := newReconciler(nodeWithUpgradingLabel(nodeName), dus)
			r.Recorder = record.NewFakeRecorder(64)
			r.StateMachine.Recorder = r.Recorder

			_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: dusName}})
			if got := hasUpgradingLabel(t, r, nodeName); got != tc.wantLabel {
				t.Errorf("state=%s 라벨 존재=%v, want %v", tc.state, got, tc.wantLabel)
			}
		})
	}
}

// TestReconcile_DeferKeepsLabelWhileAnotherCycleRunsOnNode 는 같은 노드의 다른 벤더 사이클이 진행 중이면
// 사이클을 닫은 DUS 의 defer 가 노드 라벨을 지우지 않는지 본다. 라벨은 노드 단위라 지우면 진행 중인
// 사이클의 health 복구 억제·device-plugin 차단이 풀린다 — Warboy 가 설치 Secret 을 기다리며 Idle 로
// 재조정되는 동안 같은 노드의 NVIDIA 가 설치하는 경우다.
// 깨는 뮤테이션: defer 의 otherCycleOnNode 확인을 빼면 두 라벨이 지워져 실패한다.
func TestReconcile_DeferKeepsLabelWhileAnotherCycleRunsOnNode(t *testing.T) {
	const nodeName = "worker-shared"
	idle := dusWithTransition(nodeName+"-furiosa", nodeName, "furiosa", v1alpha1.UpgradeStateIdle, 0)
	busy := dusWithTransition(nodeName+"-nvidia", nodeName, "nvidia", v1alpha1.UpgradeStateUpgrading, 0)
	r := newReconciler(nodeWithBothUpgradingLabels(nodeName), idle, busy)
	r.Recorder = record.NewFakeRecorder(64)
	r.StateMachine.Recorder = r.Recorder

	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: idle.Name}})
	if !hasUpgradingLabel(t, r, nodeName) || !hasBlockingLabel(t, r, nodeName) {
		t.Errorf("같은 노드의 NVIDIA 사이클이 진행 중인데 Idle DUS 의 defer 가 라벨을 지웠다")
	}
}

// TestSweepStuckUpgradingLabels_RemovesIdleStuckLabel 는 6일 invariant root cause 재현 시나리오:
// 사이클 mid-flight 에서 reconcile 이 panic 등으로 중단되어 DUS state 가 Idle 로 복귀했지만
// 노드에 driver-upgrading 라벨이 남아있는 경우, sweep 이 자동 제거하는지 검증한다.
func TestSweepStuckUpgradingLabels_RemovesIdleStuckLabel(t *testing.T) {
	const (
		nodeName = "worker-stuck"
		vendor   = "furiosa"
		dusName  = "worker-stuck-furiosa"
	)
	node := nodeWithUpgradingLabel(nodeName)
	dus := dusWithTransition(dusName, nodeName, vendor, v1alpha1.UpgradeStateIdle, 5*time.Minute)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("Idle + grace 경과 stuck 라벨이 제거되지 않음 (root cause 재발)")
	}
}

// TestSweepStuckUpgradingLabels_RemovesFailedStuckLabel 는 Failed 종료 상태 + grace 경과 시
// 라벨이 제거되는지 검증한다.
func TestSweepStuckUpgradingLabels_RemovesFailedStuckLabel(t *testing.T) {
	const (
		nodeName = "worker-failed"
		vendor   = "furiosa"
		dusName  = "worker-failed-furiosa"
	)
	node := nodeWithUpgradingLabel(nodeName)
	dus := dusWithTransition(dusName, nodeName, vendor, "Failed", 5*time.Minute)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("Failed + grace 경과 stuck 라벨이 제거되지 않음")
	}
}

// TestSweepStuckUpgradingLabels_PreservesActiveCycleLabel 는 mid-cycle (Cordoning, Draining,
// Upgrading, Validating, Uncordoning, Rollback, PreFlight, UpgradeRequired) 라벨은
// grace 경과와 무관하게 절대 제거하지 않음을 검증한다 — 정상 사이클 보호 invariant.
func TestSweepStuckUpgradingLabels_PreservesActiveCycleLabel(t *testing.T) {
	activeStates := []string{
		v1alpha1.UpgradeStateRequired,
		v1alpha1.UpgradeStatePreFlight,
		v1alpha1.UpgradeStateCordoning,
		v1alpha1.UpgradeStateDraining,
		v1alpha1.UpgradeStateUpgrading,
		v1alpha1.UpgradeStateValidating,
		v1alpha1.UpgradeStateUncordoning,
		v1alpha1.UpgradeStateRollback,
	}
	for _, st := range activeStates {
		t.Run(st, func(t *testing.T) {
			const (
				nodeName = "worker-active"
				vendor   = "furiosa"
				dusName  = "worker-active-furiosa"
			)
			node := nodeWithUpgradingLabel(nodeName)
			// 일부러 grace 를 한참 넘긴 시각 — mid-cycle 이므로 절대 제거되어선 안 됨
			dus := dusWithTransition(dusName, nodeName, vendor, st, 30*time.Minute)
			r := newReconciler(node, dus)

			if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
				t.Fatalf("sweep 오류: %v", err)
			}
			if !hasUpgradingLabel(t, r, nodeName) {
				t.Errorf("mid-cycle (%s) 라벨이 잘못 제거됨 — 정상 사이클 invariant 위반", st)
			}
		})
	}
}

// TestSweepStuckUpgradingLabels_RemovesTerminalStateLabelAfterGrace 는 HostAhead 와
// UnverifiedVersion 같은 보고 전용 종점 상태도 grace 경과 후 stuck 라벨 제거 대상임을 검증한다.
func TestSweepStuckUpgradingLabels_RemovesTerminalStateLabelAfterGrace(t *testing.T) {
	terminalStates := []string{
		v1alpha1.UpgradeStateHostAhead,
		v1alpha1.UpgradeStateUnverifiedVersion,
	}
	for _, st := range terminalStates {
		t.Run(st, func(t *testing.T) {
			const (
				nodeName = "worker-terminal"
				vendor   = "furiosa"
				dusName  = "worker-terminal-furiosa"
			)
			node := nodeWithUpgradingLabel(nodeName)
			dus := dusWithTransition(dusName, nodeName, vendor, st, 5*time.Minute)
			r := newReconciler(node, dus)

			if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
				t.Fatalf("sweep 오류: %v", err)
			}
			if hasUpgradingLabel(t, r, nodeName) {
				t.Errorf("%s + grace 경과 stuck 라벨이 제거되지 않음", st)
			}
		})
	}
}

// TestSweepStuckUpgradingLabels_PreservesWithinGracePeriod 는 DUS 가 종료 상태이지만
// grace period 이내인 경우 transient 보호를 위해 라벨을 제거하지 않음을 검증한다.
func TestSweepStuckUpgradingLabels_PreservesWithinGracePeriod(t *testing.T) {
	const (
		nodeName = "worker-fresh"
		vendor   = "furiosa"
		dusName  = "worker-fresh-furiosa"
	)
	node := nodeWithUpgradingLabel(nodeName)
	dus := dusWithTransition(dusName, nodeName, vendor, v1alpha1.UpgradeStateIdle, 5*time.Second)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("grace period (30s) 이내 라벨이 잘못 제거됨 — transient 보호 위반")
	}
}

// TestSweepStuckUpgradingLabels_PreservesUnknownOwner 는 노드에 라벨이 있지만
// 매칭되는 DUS 가 하나도 없는 경우 (다른 컨트롤러 소유 가능성) 라벨을 보존함을 검증한다.
func TestSweepStuckUpgradingLabels_PreservesUnknownOwner(t *testing.T) {
	const nodeName = "worker-orphan"
	node := nodeWithUpgradingLabel(nodeName)
	r := newReconciler(node) // DUS 없음

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("DUS 부재 노드의 라벨이 잘못 제거됨 — 미상 소유 보호 위반")
	}
}

// TestSweepStuckUpgradingLabels_MixedDUSStateOnSameNode 는 같은 노드에 vendor 가 다른
// 두 DUS 가 있고 한쪽이 mid-cycle 인 경우 보수적으로 라벨을 보존하는지 검증한다.
// (다른 vendor 가 사이클 진행 중이면 그 라벨은 정상 사용 중)
func TestSweepStuckUpgradingLabels_MixedDUSStateOnSameNode(t *testing.T) {
	const nodeName = "worker-mixed"
	node := nodeWithUpgradingLabel(nodeName)
	idleDUS := dusWithTransition("worker-mixed-furiosa", nodeName, "furiosa",
		v1alpha1.UpgradeStateIdle, 10*time.Minute)
	activeDUS := dusWithTransition("worker-mixed-nvidia", nodeName, "nvidia",
		v1alpha1.UpgradeStateUpgrading, 10*time.Minute)
	r := newReconciler(node, idleDUS, activeDUS)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("mid-cycle DUS 가 공존하는 노드의 라벨이 잘못 제거됨")
	}
}

// TestEnsureUpgradingLabelRemoved_Idempotent 는 라벨이 이미 없을 때 EnsureUpgradingLabelRemoved
// 가 에러 없이 no-op 으로 동작하는지 검증한다 (defer cleanup 의 안전성 보장).
func TestEnsureUpgradingLabelRemoved_Idempotent(t *testing.T) {
	const nodeName = "worker-clean"
	node := workerNode(nodeName) // 라벨 없음
	r := newReconciler(node)

	if err := r.StateMachine.EnsureUpgradingLabelRemoved(context.Background(), nodeName); err != nil {
		t.Fatalf("idempotent 호출 실패: %v", err)
	}
}

// TestEnsureUpgradingLabelRemoved_NodeNotFound 는 노드가 없는 경우 에러 없이 무시함을 검증한다.
func TestEnsureUpgradingLabelRemoved_NodeNotFound(t *testing.T) {
	r := newReconciler() // 노드 없음
	if err := r.StateMachine.EnsureUpgradingLabelRemoved(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("NotFound 무시 실패: %v", err)
	}
}

// ─────────────────────────────────────────────
// detector phase-aware blocking label 시나리오
// ─────────────────────────────────────────────

// nodeWithBothUpgradingLabels 는 driver-upgrading + driver-upgrading-blocking 라벨 둘 다 붙은
// 워커 노드를 반환한다. cordonNode() 가 추가하는 mid-cycle 상태를 시뮬레이트.
func nodeWithBothUpgradingLabels(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				upgrade.DriverUpgradingLabelKey:         "true",
				upgrade.DriverUpgradingBlockingLabelKey: "true",
			},
		},
	}
}

// hasBlockingLabel 은 노드의 driver-upgrading-blocking 라벨 보유 여부를 반환합니다.
func hasBlockingLabel(t *testing.T, r *DriverUpgradeReconciler, nodeName string) bool {
	t.Helper()
	var node corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: nodeName}, &node); err != nil {
		t.Fatalf("노드 조회 실패: %v", err)
	}
	_, ok := node.Labels[upgrade.DriverUpgradingBlockingLabelKey]
	return ok
}

// TestEnsureUpgradingBlockingLabelRemoved_RemovesOnlyBlocking 는 EnsureUpgradingBlockingLabelRemoved
// 가 driver-upgrading-blocking 라벨만 제거하고 driver-upgrading 라벨은 보존하는지 검증한다.
// (handleValidating 진입 시점의 핵심 동작 — detector 만 풀어주고 사이클 추적은 유지)
func TestEnsureUpgradingBlockingLabelRemoved_RemovesOnlyBlocking(t *testing.T) {
	const nodeName = "worker-validating-entry"
	node := nodeWithBothUpgradingLabels(nodeName)
	r := newReconciler(node)

	if err := r.StateMachine.EnsureUpgradingBlockingLabelRemoved(context.Background(), nodeName); err != nil {
		t.Fatalf("EnsureUpgradingBlockingLabelRemoved 실패: %v", err)
	}
	if hasBlockingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading-blocking 라벨이 제거되지 않음 (옵션 A 핵심 동작 깨짐)")
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading 라벨이 잘못 제거됨 — 사이클 추적 라벨은 보존되어야 함")
	}
}

// TestSweepStuckUpgradingLabels_BothLabels 는 두 라벨이 모두 stuck 인 노드에서 sweep 가
// 둘 다 정리하는지 검증한다.
func TestSweepStuckUpgradingLabels_BothLabels(t *testing.T) {
	const (
		nodeName = "worker-both-stuck"
		vendor   = "furiosa"
		dusName  = "worker-both-stuck-furiosa"
	)
	node := nodeWithBothUpgradingLabels(nodeName)
	// Idle 상태 + 5min 경과 — 두 라벨 grace (15s, 30s) 모두 초과
	dus := dusWithTransition(dusName, nodeName, vendor, v1alpha1.UpgradeStateIdle, 5*time.Minute)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading 라벨이 제거되지 않음")
	}
	if hasBlockingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading-blocking 라벨이 제거되지 않음")
	}
}

// TestSweepStuckUpgradingLabels_BlockingShorterGrace 는 driver-upgrading-blocking 라벨이
// 더 짧은 grace (15s) 로 빠르게 sweep 되는지 검증한다.
//
// 시나리오: DUS 종료 상태로 20s 경과 → blocking grace (15s) 초과 + main grace (30s) 미달
// 기대: blocking 만 제거, main 은 보존 (다음 sweep 에서 풀림)
func TestSweepStuckUpgradingLabels_BlockingShorterGrace(t *testing.T) {
	const (
		nodeName = "worker-blocking-only"
		vendor   = "furiosa"
		dusName  = "worker-blocking-only-furiosa"
	)
	node := nodeWithBothUpgradingLabels(nodeName)
	// 20s 경과 — blocking grace(15s) 초과 + main grace(30s) 미달
	dus := dusWithTransition(dusName, nodeName, vendor, v1alpha1.UpgradeStateIdle, 20*time.Second)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if hasBlockingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading-blocking 라벨이 짧은 grace 후에도 제거되지 않음")
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("driver-upgrading 라벨이 30s 미달인데 잘못 제거됨")
	}
}

// ─────────────────────────────────────────────
// Validating self-heal 시나리오
// ─────────────────────────────────────────────

// makeDriverDS 는 Validating self-heal 테스트용 driver DaemonSet 을 만든다.
// kube-system 네임스페이스, container[0].Image=desiredImage 의 단일 컨테이너 Pod 템플릿.
func makeDriverDS(dsName, vendor, desiredImage string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsName,
			Namespace: "kube-system",
		},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: vendor + "-driver", Image: desiredImage},
					},
				},
			},
		},
	}
}

// makeDIPWithImage 는 Image 가 지정된 DIP 를 만든다 (Driver.Mode=daemonset).
func makeDIPWithImage(name, vendor, version, image string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Driver: v1alpha1.DriverSpec{
				Version: version,
				Image:   image,
				Mode:    "daemonset",
			},
		},
	}
}

// newSMWithRecorder 는 Recorder 가 부착된 UpgradeStateMachine 과 fake client 를 만든다.
// state_machine.go 의 handleValidating 이 m.Recorder.Eventf 를 호출하므로 test 에서도 필수.
func newSMWithRecorder(objs ...client.Object) (*upgrade.UpgradeStateMachine, client.Client) {
	s := newTestScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
		Build()
	return &upgrade.UpgradeStateMachine{
		Client:   c,
		Recorder: record.NewFakeRecorder(64),
	}, c
}

// makeUpgradingDUS 는 Upgrading 상태 진입 직후 시점의 DUS 를 만든다 (PreviousImage 비어있음).
func makeUpgradingDUS(name, nodeName, vendor, desiredVersion string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              v1alpha1.UpgradeStateUpgrading,
			DesiredVersion:     desiredVersion,
			LastTransitionTime: metav1.NewTime(time.Now()),
		},
	}
}

// TestUpgrading_PreviousImage_RejectsPlainTag 는 handleUpgrading 이 broken plain tag
// (예: ":580.126.09") 를 PreviousImage 로 캡처하지 않음을 검증한다 — 캡처하면 rollback 때
// broken Pod 로 되돌아간다.
func TestUpgrading_PreviousImage_RejectsPlainTag(t *testing.T) {
	const (
		nodeName   = "worker-rollback-guard"
		vendor     = "nvidia"
		dusName    = "worker-rollback-guard-nvidia"
		dsName     = "kcloud-nvidia-driver"
		plainImage = "registry.example.com/nvidia-driver-ds:580.126.09" // broken plain tag
		desiredImg = "registry.example.com/nvidia-driver-ds:590.48.01-v16"
		desiredVer = "590.48.01"
		policyName = "nvidia-generic"
	)

	dus := makeUpgradingDUS(dusName, nodeName, vendor, desiredVer)
	dip := makeDIPWithImage(policyName, vendor, desiredVer, desiredImg)
	// DS 의 기존 image 가 broken plain tag (rollback 시 회귀 위험)
	ds := makeDriverDS(dsName, vendor, plainImage)
	node := workerNode(nodeName)

	sm, _ := newSMWithRecorder(dus, dip, ds, node)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	// plain tag 는 PreviousImage 로 저장되어선 안 됨 (defense-in-depth).
	if dus.Status.PreviousImage != "" {
		t.Errorf("plain tag 가 PreviousImage 로 잘못 저장됨: got %q, want \"\"",
			dus.Status.PreviousImage)
	}
}

// TestUpgrading_PreviousImage_AcceptsVerifiedTag 는 -v<N> 접미사 검증 빌드 태그가
// PreviousImage 로 정상 저장되는지 확인한다 (회귀 방지).
func TestUpgrading_PreviousImage_AcceptsVerifiedTag(t *testing.T) {
	const (
		nodeName     = "worker-rollback-ok"
		vendor       = "nvidia"
		dusName      = "worker-rollback-ok-nvidia"
		dsName       = "kcloud-nvidia-driver"
		verifiedPrev = "registry.example.com/nvidia-driver-ds:580.126.09-v16"
		desiredImg   = "registry.example.com/nvidia-driver-ds:590.48.01-v16"
		desiredVer   = "590.48.01"
		policyName   = "nvidia-generic"
	)

	dus := makeUpgradingDUS(dusName, nodeName, vendor, desiredVer)
	dip := makeDIPWithImage(policyName, vendor, desiredVer, desiredImg)
	ds := makeDriverDS(dsName, vendor, verifiedPrev)
	node := workerNode(nodeName)

	sm, _ := newSMWithRecorder(dus, dip, ds, node)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	if dus.Status.PreviousImage != verifiedPrev {
		t.Errorf("verified build tag 가 PreviousImage 로 저장 실패: got %q, want %q",
			dus.Status.PreviousImage, verifiedPrev)
	}
}

// ─────────────────────────────────────────────
// IdleCooldown 시나리오(operator 측 핵심 가드)
// ─────────────────────────────────────────────

// drainRecorderEvents 는 FakeRecorder 의 모든 buffered event 를 비워 channel 을 비운다.
// 이전 테스트에서 leak 된 이벤트가 다음 테스트 검증을 오염시키지 않도록 하기 위함.
func drainRecorderEvents(rec *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case ev := <-rec.Events:
			events = append(events, ev)
		default:
			return events
		}
	}
}

// hasRecorderEventReason 은 FakeRecorder 에 기록된 이벤트 중 reason 문자열을 포함하는 것이
// 있는지 확인한다. record.FakeRecorder 의 Eventf 출력 포맷은 "<type> <reason> <message>" 이다.
func hasRecorderEventReason(rec *record.FakeRecorder, reason string) bool {
	for _, ev := range drainRecorderEvents(rec) {
		if strings.Contains(ev, reason) {
			return true
		}
	}
	return false
}

// makeIdleDUSWithLastTransition 은 임의의 LastTransitionTime 을 가진 Idle 상태 DUS 를 만든다.
// IdleCooldown 가드 테스트에서 "방금 Idle 진입" / "cooldown 초과" 두 시나리오를 시뮬레이트하기 위한 helper.
func makeIdleDUSWithLastTransition(name, nodeName, vendor, currentVersion string, lastTransition time.Time) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              v1alpha1.UpgradeStateIdle,
			CurrentVersion:     currentVersion,
			LastTransitionTime: metav1.NewTime(lastTransition),
		},
	}
}

// makeDIPWithCooldown 은 IdleCooldownSeconds 가 지정된 daemonset-mode DIP 를 만든다.
// cooldownSeconds 가 0 이면 disabled, nil 시뮬레이션이 필요할 땐 별도로 IdleCooldownSeconds=nil 로 만들 것.
func makeDIPWithCooldown(name, vendor, version string, cooldownSeconds int32) *v1alpha1.DriverInstallPolicy {
	cd := cooldownSeconds
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Driver: v1alpha1.DriverSpec{
				Version: version,
				Mode:    "daemonset",
			},
			UpgradePolicy: &v1alpha1.UpgradePolicy{
				AutoUpgrade:         true,
				IdleCooldownSeconds: &cd,
			},
		},
	}
}

// TestIdle_CooldownDeferred 는 Idle 진입 직후 (LastTransitionTime=now) 버전 불일치를
// 감지해도 UpgradeRequired 로 전이하지 않고 IdleCooldownDeferred 이벤트와 함께 requeue 만 함을 검증.
// rolling-update 연속 trigger 시 mid-state 오인 방지의 핵심 가드.
func TestIdle_CooldownDeferred(t *testing.T) {
	const (
		nodeName     = "worker-cooldown-defer"
		vendor       = "nvidia"
		dusName      = "worker-cooldown-defer-nvidia"
		installedVer = "580.126.09"
		desiredVer   = "590.48.01"
	)

	// 방금 Idle 진입 (LastTransitionTime=now) — cooldown 미충족
	dus := makeIdleDUSWithLastTransition(dusName, nodeName, vendor, installedVer, time.Now())
	dip := makeDIPWithCooldown("nvidia-generic", vendor, desiredVer, 10)

	sm, _ := newSMWithRecorder(dus)
	rec := sm.Recorder.(*record.FakeRecorder)

	requeue, requeueAfter, err := sm.TransitionState(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if !requeue {
		t.Errorf("cooldown 미충족 시 requeue=true 기대, got false")
	}
	if requeueAfter <= 0 || requeueAfter > 10*time.Second {
		t.Errorf("requeueAfter 가 cooldown 잔여 시간(0 < x ≤ 10s) 이어야 함: got %s", requeueAfter)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("cooldown 미충족 시 state=Idle 유지 기대, got %q", dus.Status.State)
	}
	if dus.Status.DesiredVersion == desiredVer {
		t.Errorf("cooldown 미충족 시 DesiredVersion 갱신되어선 안 됨: got %q", dus.Status.DesiredVersion)
	}
	if !hasRecorderEventReason(rec, "IdleCooldownDeferred") {
		t.Errorf("IdleCooldownDeferred 이벤트가 발행되지 않음")
	}
}

// TestIdle_CooldownExpired 는 Idle 진입 후 cooldown(10s) 초과 (11s 경과) 상태에서 버전 불일치
// 감지 시 UpgradeRequired 로 정상 전이함을 검증한다.
func TestIdle_CooldownExpired(t *testing.T) {
	const (
		nodeName     = "worker-cooldown-expired"
		vendor       = "nvidia"
		dusName      = "worker-cooldown-expired-nvidia"
		installedVer = "580.126.09"
		desiredVer   = "590.48.01"
	)

	// cooldown(10s) 초과 — 정상 전이 기대
	dus := makeIdleDUSWithLastTransition(dusName, nodeName, vendor, installedVer, time.Now().Add(-11*time.Second))
	dip := makeDIPWithCooldown("nvidia-generic", vendor, desiredVer, 10)

	sm, _ := newSMWithRecorder(dus)
	rec := sm.Recorder.(*record.FakeRecorder)

	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("cooldown 충족 시 state=UpgradeRequired 전이 기대, got %q", dus.Status.State)
	}
	if dus.Status.DesiredVersion != desiredVer {
		t.Errorf("DesiredVersion 갱신 기대 (%s), got %q", desiredVer, dus.Status.DesiredVersion)
	}
	if hasRecorderEventReason(rec, "IdleCooldownDeferred") {
		t.Errorf("cooldown 충족 시 IdleCooldownDeferred 이벤트가 발행되어선 안 됨")
	}
}

// TestSweepStuckUpgradingLabels_PreservesActiveCycleBothLabels 는 두 라벨이 모두 있는 mid-cycle
// 노드에서 sweep 가 둘 다 보존하는지 검증한다.
func TestSweepStuckUpgradingLabels_PreservesActiveCycleBothLabels(t *testing.T) {
	const (
		nodeName = "worker-active-both"
		vendor   = "furiosa"
		dusName  = "worker-active-both-furiosa"
	)
	node := nodeWithBothUpgradingLabels(nodeName)
	// mid-cycle (Cordoning) — grace 무관하게 절대 제거되어선 안 됨
	dus := dusWithTransition(dusName, nodeName, vendor, v1alpha1.UpgradeStateCordoning, 30*time.Minute)
	r := newReconciler(node, dus)

	if err := r.sweepStuckUpgradingLabels(context.Background()); err != nil {
		t.Fatalf("sweep 오류: %v", err)
	}
	if !hasUpgradingLabel(t, r, nodeName) {
		t.Errorf("mid-cycle (Cordoning) driver-upgrading 라벨이 잘못 제거됨")
	}
	if !hasBlockingLabel(t, r, nodeName) {
		t.Errorf("mid-cycle (Cordoning) driver-upgrading-blocking 라벨이 잘못 제거됨")
	}
}

// ─────────────────────────────────────────────
// Quiesce-on-Driver-Upgrade 시나리오 (opt-in label 기반 자동 scale=0/restore)
// ─────────────────────────────────────────────

// makeQuiesceLabeledDeploy 는 quiesce-on-driver-upgrade 라벨이 붙고 nodeSelector 의 hostname
// 으로 특정 노드를 타겟하는 Deployment 를 만든다 (gpu-stress 와 동일한 형태).
func makeQuiesceLabeledDeploy(name, nodeName string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kcloud",
			Labels: map[string]string{
				upgrade.QuiesceOnDriverUpgradeLabelKey: "true",
				"app":                                  name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": nodeName,
					},
					Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
				},
			},
		},
	}
}

// makeUnlabeledDeploy 는 quiesce 라벨이 없는 일반 Deployment 를 만든다 (production workload 시뮬레이션).
func makeUnlabeledDeploy(name, namespace, nodeName string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": nodeName,
					},
					Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
				},
			},
		},
	}
}

// makeCordoningDUS 는 quiesce 진입 시점의 DUS (Cordoning state) 를 만든다.
func makeCordoningDUS(name, nodeName, vendor string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:              v1alpha1.UpgradeStateCordoning,
			LastTransitionTime: metav1.NewTime(time.Now()),
		},
	}
}

// TestQuiesce_LabelMatched_Scaled0 는 quiesce 라벨 + 노드 매칭 Deployment 가 spec.replicas=0
// 으로 patch 되고 backup (DUS.Status.QuiescedDeployments) 에 원래 replicas 가 기록되는지 검증한다.
func TestQuiesce_LabelMatched_Scaled0(t *testing.T) {
	const (
		nodeName  = "worker-quiesce-1"
		vendor    = "nvidia"
		dusName   = "worker-quiesce-1-nvidia"
		deployNS  = "kcloud"
		deployNM  = "gpu-stress-single"
		origScale = int32(2)
	)
	deploy := makeQuiesceLabeledDeploy(deployNM, nodeName, origScale)
	dus := makeCordoningDUS(dusName, nodeName, vendor)

	sm, c := newSMWithRecorder(deploy, dus)
	if err := sm.QuiesceLabeledDeployments(context.Background(), dus); err != nil {
		t.Fatalf("QuiesceLabeledDeployments 실패: %v", err)
	}

	// deployment 가 spec.replicas=0 으로 patch 되었는지 검증
	var got appsv1.Deployment
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: deployNS, Name: deployNM}, &got); err != nil {
		t.Fatalf("deployment 조회 실패: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("Deployment.spec.replicas: got %v, want 0", got.Spec.Replicas)
	}

	// backup 에 원래 replicas 가 기록되었는지 검증
	if len(dus.Status.QuiescedDeployments) != 1 {
		t.Fatalf("QuiescedDeployments len: got %d, want 1", len(dus.Status.QuiescedDeployments))
	}
	q := dus.Status.QuiescedDeployments[0]
	if q.Namespace != deployNS || q.Name != deployNM {
		t.Errorf("backup target: got %s/%s, want %s/%s", q.Namespace, q.Name, deployNS, deployNM)
	}
	if q.OriginalReplicas != origScale {
		t.Errorf("OriginalReplicas: got %d, want %d", q.OriginalReplicas, origScale)
	}

	// DeploymentQuiesced 이벤트 검증
	rec := sm.Recorder.(*record.FakeRecorder)
	if !hasRecorderEventReason(rec, "DeploymentQuiesced") {
		t.Errorf("DeploymentQuiesced 이벤트가 발행되지 않음")
	}
}

// TestQuiesce_LabelAbsent_Untouched 는 quiesce 라벨이 없는 Deployment 는 같은 노드 위에 있어도
// scale 변경 없이 그대로 보존됨을 검증한다 (production workload 안전성 invariant).
func TestQuiesce_LabelAbsent_Untouched(t *testing.T) {
	const (
		nodeName  = "worker-quiesce-2"
		vendor    = "nvidia"
		dusName   = "worker-quiesce-2-nvidia"
		deployNS  = "default"
		deployNM  = "production-app"
		origScale = int32(3)
	)
	// 라벨 없는 production workload — 같은 노드 타겟이지만 quiesce 대상 아님
	deploy := makeUnlabeledDeploy(deployNM, deployNS, nodeName, origScale)
	dus := makeCordoningDUS(dusName, nodeName, vendor)

	sm, c := newSMWithRecorder(deploy, dus)
	if err := sm.QuiesceLabeledDeployments(context.Background(), dus); err != nil {
		t.Fatalf("QuiesceLabeledDeployments 실패: %v", err)
	}

	// 라벨 없는 Deployment 는 absolute 보존
	var got appsv1.Deployment
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: deployNS, Name: deployNM}, &got); err != nil {
		t.Fatalf("deployment 조회 실패: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != origScale {
		t.Errorf("라벨 없는 Deployment 의 replicas 가 변경됨: got %v, want %d",
			got.Spec.Replicas, origScale)
	}
	if len(dus.Status.QuiescedDeployments) != 0 {
		t.Errorf("라벨 없는 Deployment 가 backup 에 기록됨: got %d entries",
			len(dus.Status.QuiescedDeployments))
	}
}

// TestRestore_OriginalReplicas 는 backup 의 OriginalReplicas 로 Deployment.spec.replicas 가
// 정확히 복구되고 backup 이 비워지는지 검증한다.
func TestRestore_OriginalReplicas(t *testing.T) {
	const (
		nodeName  = "worker-restore-1"
		vendor    = "nvidia"
		dusName   = "worker-restore-1-nvidia"
		deployNS  = "kcloud"
		deployNM  = "gpu-stress-single"
		origScale = int32(2)
	)
	// 이미 quiesce 된 상태 (replicas=0) + backup 보유 시뮬레이션
	zero := int32(0)
	deploy := makeQuiesceLabeledDeploy(deployNM, nodeName, origScale)
	deploy.Spec.Replicas = &zero

	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State: v1alpha1.UpgradeStateUncordoning,
			QuiescedDeployments: []v1alpha1.QuiescedDeployment{
				{Namespace: deployNS, Name: deployNM, OriginalReplicas: origScale},
			},
		},
	}

	sm, c := newSMWithRecorder(deploy, dus)
	if err := sm.RestoreQuiescedDeployments(context.Background(), dus); err != nil {
		t.Fatalf("RestoreQuiescedDeployments 실패: %v", err)
	}

	// 복구 검증
	var got appsv1.Deployment
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: deployNS, Name: deployNM}, &got); err != nil {
		t.Fatalf("deployment 조회 실패: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != origScale {
		t.Errorf("Deployment.spec.replicas 복구 실패: got %v, want %d",
			got.Spec.Replicas, origScale)
	}

	// backup 비워짐 검증 (다음 cycle 누수 방지)
	if len(dus.Status.QuiescedDeployments) != 0 {
		t.Errorf("backup 이 비워지지 않음: got %d entries",
			len(dus.Status.QuiescedDeployments))
	}

	// DeploymentRestored 이벤트 검증
	rec := sm.Recorder.(*record.FakeRecorder)
	if !hasRecorderEventReason(rec, "DeploymentRestored") {
		t.Errorf("DeploymentRestored 이벤트가 발행되지 않음")
	}
}

// TestRestore_DeploymentMissing_Graceful 은 backup 에 등록된 Deployment 가 운영자에 의해 삭제된
// 상황에서 RestoreQuiescedDeployments 가 에러 없이 graceful skip 하고 backup 을 비우는지 검증한다.
func TestRestore_DeploymentMissing_Graceful(t *testing.T) {
	const (
		nodeName  = "worker-restore-missing"
		vendor    = "nvidia"
		dusName   = "worker-restore-missing-nvidia"
		deployNS  = "kcloud"
		deployNM  = "gpu-stress-deleted"
		origScale = int32(1)
	)
	// Deployment 객체는 의도적으로 client 에 넣지 않음 (사용자가 삭제한 시나리오)
	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State: v1alpha1.UpgradeStateUncordoning,
			QuiescedDeployments: []v1alpha1.QuiescedDeployment{
				{Namespace: deployNS, Name: deployNM, OriginalReplicas: origScale},
			},
		},
	}

	sm, _ := newSMWithRecorder(dus)
	// graceful: 에러 반환되어선 안 됨
	if err := sm.RestoreQuiescedDeployments(context.Background(), dus); err != nil {
		t.Fatalf("RestoreQuiescedDeployments 가 missing deployment 에 대해 에러 반환: %v", err)
	}

	// backup 은 비워져야 함 (다음 cycle 에 잔존 데이터 누수 방지)
	if len(dus.Status.QuiescedDeployments) != 0 {
		t.Errorf("missing deployment 처리 후 backup 이 비워지지 않음: got %d entries",
			len(dus.Status.QuiescedDeployments))
	}

	// DeploymentRestoreSkipped 이벤트 검증 (운영자가 삭제 인지)
	rec := sm.Recorder.(*record.FakeRecorder)
	if !hasRecorderEventReason(rec, "DeploymentRestoreSkipped") {
		t.Errorf("DeploymentRestoreSkipped 이벤트가 발행되지 않음")
	}
}

// TestQuiesce_AnnotationBackup_Persisted 는 quiesce 시 (1) Deployment annotation 으로
// dual-write 가 발생하고 (2) DUS.Status.QuiescedDeployments backup 이 즉시 영구 저장됨을
// 검증한다(operator restart resilience).
func TestQuiesce_AnnotationBackup_Persisted(t *testing.T) {
	const (
		nodeName  = "worker-anno-backup"
		vendor    = "nvidia"
		dusName   = "worker-anno-backup-nvidia"
		deployNS  = "kcloud"
		deployNM  = "gpu-stress-anno"
		origScale = int32(3)
	)
	deploy := makeQuiesceLabeledDeploy(deployNM, nodeName, origScale)
	dus := makeCordoningDUS(dusName, nodeName, vendor)

	sm, c := newSMWithRecorder(deploy, dus)
	if err := sm.QuiesceLabeledDeployments(context.Background(), dus); err != nil {
		t.Fatalf("QuiesceLabeledDeployments 실패: %v", err)
	}

	// (1) annotation dual-write 검증
	var got appsv1.Deployment
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: deployNS, Name: deployNM}, &got); err != nil {
		t.Fatalf("deployment 조회 실패: %v", err)
	}
	backup, ok := got.Annotations[upgrade.QuiesceReplicasBackupAnnotation]
	if !ok {
		t.Fatalf("annotation %q 가 설정되지 않음", upgrade.QuiesceReplicasBackupAnnotation)
	}
	if backup != "3" {
		t.Errorf("annotation 값 불일치: got %q, want %q", backup, "3")
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != 0 {
		t.Errorf("Deployment.spec.replicas: got %v, want 0", got.Spec.Replicas)
	}

	// (2) DUS.Status backup 영구 저장 검증 — fake client 의 status subresource 에서 재조회.
	// Status().Update 가 호출되지 않으면 fresh fetch 시 QuiescedDeployments 가 비어 있음.
	var freshDUS v1alpha1.DriverUpgradeState
	if err := c.Get(context.Background(),
		types.NamespacedName{Name: dusName}, &freshDUS); err != nil {
		t.Fatalf("DUS 재조회 실패: %v", err)
	}
	if len(freshDUS.Status.QuiescedDeployments) != 1 {
		t.Fatalf("Status.QuiescedDeployments 가 영구 저장되지 않음 (Status.Update 누락 의심): "+
			"got %d entries, want 1", len(freshDUS.Status.QuiescedDeployments))
	}
	q := freshDUS.Status.QuiescedDeployments[0]
	if q.Namespace != deployNS || q.Name != deployNM {
		t.Errorf("backup target: got %s/%s, want %s/%s",
			q.Namespace, q.Name, deployNS, deployNM)
	}
	if q.OriginalReplicas != origScale {
		t.Errorf("OriginalReplicas: got %d, want %d", q.OriginalReplicas, origScale)
	}
}

// TestRestore_FromAnnotation_Fallback 은 DUS.Status.QuiescedDeployments 가 비어 있는 상황
// (operator restart 후 status 손실 시뮬레이션) 에서 Deployment annotation 백업으로부터
// fallback 복구가 동작함을 검증한다.
func TestRestore_FromAnnotation_Fallback(t *testing.T) {
	const (
		nodeName  = "worker-anno-fallback"
		vendor    = "nvidia"
		dusName   = "worker-anno-fallback-nvidia"
		deployNS  = "kcloud"
		deployNM  = "gpu-stress-fallback"
		origScale = int32(2)
	)
	// quiesce 직후 operator 가 재기동된 시나리오:
	//   - Deployment 는 replicas=0 으로 quiesce 된 상태 + annotation 에 backup "2" 보유
	//   - DUS.Status.QuiescedDeployments 는 empty (Status.Update 직전에 crash)
	zero := int32(0)
	deploy := makeQuiesceLabeledDeploy(deployNM, nodeName, origScale)
	deploy.Spec.Replicas = &zero
	if deploy.Annotations == nil {
		deploy.Annotations = make(map[string]string)
	}
	deploy.Annotations[upgrade.QuiesceReplicasBackupAnnotation] = "2"

	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State: v1alpha1.UpgradeStateUncordoning,
			// QuiescedDeployments 의도적으로 비어있음 — restart 후 status 손실 시뮬레이션.
		},
	}

	sm, c := newSMWithRecorder(deploy, dus)
	if err := sm.RestoreQuiescedDeployments(context.Background(), dus); err != nil {
		t.Fatalf("RestoreQuiescedDeployments fallback 실패: %v", err)
	}

	// replicas 가 annotation 값으로 복구되었는지 검증
	var got appsv1.Deployment
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: deployNS, Name: deployNM}, &got); err != nil {
		t.Fatalf("deployment 재조회 실패: %v", err)
	}
	if got.Spec.Replicas == nil || *got.Spec.Replicas != origScale {
		t.Errorf("annotation fallback 복구 실패: got replicas %v, want %d",
			got.Spec.Replicas, origScale)
	}

	// 복구 후 annotation 이 제거되었는지 검증 (다음 cycle stale 방지)
	if _, ok := got.Annotations[upgrade.QuiesceReplicasBackupAnnotation]; ok {
		t.Errorf("복구 후 backup annotation 이 제거되지 않음")
	}

	// DeploymentRestoredFromAnnotation 이벤트 검증 — 운영자가 fallback 경로 사용을 인지
	rec := sm.Recorder.(*record.FakeRecorder)
	if !hasRecorderEventReason(rec, "DeploymentRestoredFromAnnotation") {
		t.Errorf("DeploymentRestoredFromAnnotation 이벤트가 발행되지 않음")
	}
}

// dipWithGates 는 업그레이드 게이트(autoUpgrade / verifiedVersions)를 지정한 정책이다.
// dipWithGates 는 게이트 시험 전용 고정 정책이다(furiosa/warboy, 목표 1.9.8-3).
func dipWithGates(autoUpgrade bool, verified ...string) *v1alpha1.DriverInstallPolicy {
	dip := makeDIP("furiosa-warboy", "warboy", "1.9.8-3")
	dip.Spec.UpgradePolicy = &v1alpha1.UpgradePolicy{AutoUpgrade: autoUpgrade}
	dip.Spec.VerifiedVersions = verified
	return dip
}

// TestEnsureUpgradeStates_AutoUpgradeDisabledStaysIdle 는 autoUpgrade=false 인데도 업그레이드가
// 시작되지 않는 것을 고정한다. 판정은 상태기계(handleIdle)가 게이트와 함께 내린다 — 이 sync
// 경로는 버전만 맞춘다.
func TestEnsureUpgradeStates_AutoUpgradeDisabledStaysIdle(t *testing.T) {
	const (
		nodeName     = "worker-9"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-9-furiosa"
	)
	r := newReconciler(
		workerNode(nodeName),
		makeNDR(nodeName, "warboy", installedVer),
		dipWithGates(false),
		makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, installedVer, desiredVer),
	)
	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state: got %q, want %q — 이 경로가 게이트를 건너뛰고 업그레이드를 시작했다",
			got.Status.State, v1alpha1.UpgradeStateIdle)
	}
}

// TestEnsureUpgradeStates_UnverifiedVersionStaysIdle 는 verifiedVersions 화이트리스트 밖 버전으로
// 업그레이드가 시작되지 않는 것을 고정한다.
func TestEnsureUpgradeStates_UnverifiedVersionStaysIdle(t *testing.T) {
	const (
		nodeName     = "worker-10"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-10-furiosa"
	)
	r := newReconciler(
		workerNode(nodeName),
		makeNDR(nodeName, "warboy", installedVer),
		dipWithGates(true, "1.8.0", "1.9.0"),
		makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, installedVer, desiredVer),
	)
	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state: got %q, want %q — 검증되지 않은 버전으로 업그레이드가 시작됐다",
			got.Status.State, v1alpha1.UpgradeStateIdle)
	}
}

// TestEnsureUpgradeStates_PolicyVersionChangeStaysIdle 는 정책 버전이 바뀌었을 때도 게이트가
// 걸린 상태에서는 업그레이드로 진입하지 않는 것을 고정한다. desiredVersion 동기화는 그대로 한다.
func TestEnsureUpgradeStates_PolicyVersionChangeStaysIdle(t *testing.T) {
	const (
		nodeName     = "worker-11"
		installedVer = "1.8.0"
		oldDesired   = "1.8.0"
		newDesired   = "1.9.8-3"
		dusName      = "worker-11-furiosa"
	)
	r := newReconciler(
		workerNode(nodeName),
		makeNDR(nodeName, "warboy", installedVer),
		dipWithGates(false),
		makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, installedVer, oldDesired),
	)
	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DesiredVersion != newDesired {
		t.Errorf("desiredVersion 동기화 실패: got %q, want %q", got.Status.DesiredVersion, newDesired)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state: got %q, want %q — 정책 버전 변경만으로 게이트를 건너뛰었다",
			got.Status.State, v1alpha1.UpgradeStateIdle)
	}
}

// TestEnsureUpgradeStates_NewDUSStartsIdleWhenGated 는 DUS 신규 생성 경로도 같은 규칙을 따르는
// 것을 고정한다. 생성 시점에 State=UpgradeRequired 로 박으면 게이트를 지나칠 길이 없다.
func TestEnsureUpgradeStates_NewDUSStartsIdleWhenGated(t *testing.T) {
	const (
		nodeName     = "worker-12"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-12-furiosa"
	)
	r := newReconciler(
		workerNode(nodeName),
		makeNDR(nodeName, "warboy", installedVer),
		dipWithGates(false),
	)
	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates 오류: %v", err)
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("신규 DUS state: got %q, want %q", got.Status.State, v1alpha1.UpgradeStateIdle)
	}
	if got.Status.CurrentVersion != installedVer || got.Status.DesiredVersion != desiredVer {
		t.Errorf("버전 동기화 실패: current=%q desired=%q", got.Status.CurrentVersion, got.Status.DesiredVersion)
	}
}

// TestEnsureUpgradeStates_MismatchSyncIsIdempotent 는 버전이 어긋난 채로 reconcile 이 반복돼도
// 동기화가 상태를 계속 건드리지 않는 것을 고정한다. 매번 LastTransitionTime 을 갱신하면
// IdleCooldown 이 영원히 차지 않아 업그레이드가 시작되지 못한다.
func TestEnsureUpgradeStates_MismatchSyncIsIdempotent(t *testing.T) {
	const (
		nodeName     = "worker-14"
		installedVer = "1.8.0"
		desiredVer   = "1.9.8-3"
		dusName      = "worker-14-furiosa"
	)
	r := newReconciler(
		workerNode(nodeName),
		makeNDR(nodeName, "warboy", installedVer),
		dipWithGates(true, installedVer, desiredVer),
		makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, "", desiredVer),
	)
	ctx := context.Background()
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("1회차 오류: %v", err)
	}
	var first v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &first); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("2회차 오류: %v", err)
	}
	var second v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &second); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if !first.Status.LastTransitionTime.Equal(&second.Status.LastTransitionTime) {
		t.Errorf("버전이 어긋난 채 반복 동기화가 LastTransitionTime 을 갱신함: %v → %v",
			first.Status.LastTransitionTime, second.Status.LastTransitionTime)
	}
	if second.Status.CurrentVersion != installedVer {
		t.Errorf("currentVersion 동기화 실패: %q", second.Status.CurrentVersion)
	}
}

// ─────────────────────────────────────────────
// 버전 드리프트 감지 시나리오 (감지 전용, 자동 조치 없음)
// ─────────────────────────────────────────────

// TestSyncUpgradeStateDriftConfirmed 는 Idle 에서 정책 선언 버전과 NDR 실측이 유예를 넘겨
// 어긋난 채 유지되면 DriverVersionDrift Warning 이벤트가 발행됨을 고정한다.
func TestSyncUpgradeStateDriftConfirmed(t *testing.T) {
	const (
		nodeName        = "worker-drift"
		dusName         = "worker-drift-furiosa"
		declaredVersion = "580.178.04"
		observedVersion = "595.91.07"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, declaredVersion, declaredVersion)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", declaredVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: observedVersion}
	r.syncUpgradeState(context.Background(), nodeName, device, dip, "warboy", nil, "")

	if !hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("DriverVersionDrift Warning 이벤트를 기대했으나 발행되지 않음")
	}
}

// TestSyncUpgradeStateDriftWithinGrace 는 어긋남이 아직 유예 시간 안이면 이벤트를 발행하지 않고
// DriftFirstSeen 을 그대로 유지함을 고정한다(디바운스, 조기 경보 방지).
func TestSyncUpgradeStateDriftWithinGrace(t *testing.T) {
	const (
		nodeName        = "worker-drift-grace"
		dusName         = "worker-drift-grace-furiosa"
		declaredVersion = "580.178.04"
		observedVersion = "595.91.07"
	)
	firstSeen := metav1.NewTime(time.Now())
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, declaredVersion, declaredVersion)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", declaredVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: observedVersion}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("유예 시간 안인데 DriverVersionDrift 이벤트가 발행됨")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DriftFirstSeen == nil {
		t.Errorf("DriftFirstSeen 이 유예 중에 해제됨 — 유지되어야 함")
	}
}

// TestSyncUpgradeStateDriftNoneResets 는 선언 버전과 실측이 일치하면 이벤트 없이
// DriftFirstSeen 이 해제(nil)됨을 고정한다.
func TestSyncUpgradeStateDriftNoneResets(t *testing.T) {
	const (
		nodeName = "worker-drift-none"
		dusName  = "worker-drift-none-furiosa"
		version  = "580.178.04"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, version, version)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", version)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: version}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("버전이 일치하는데 DriverVersionDrift 이벤트가 발행됨")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DriftFirstSeen != nil {
		t.Errorf("버전 일치 후 DriftFirstSeen 이 해제되지 않음: %v", got.Status.DriftFirstSeen)
	}
}

// TestSyncUpgradeStateDrift_UnverifiedVersionStillObserves 는 허용 목록 밖 버전으로 주차된
// UnverifiedVersion 상태에서도 NDR 실측이 계속 반영됨을 고정한다 — currentVersion 동기화와
// DriverVersionDrift 경고 둘 다. 두 블록을 Idle 로만 제한하면 주차된 노드의 보고 버전이 전이
// 시점 값에 멈추고 사람이 호스트에서 드라이버를 바꾼 것을 알리는 경고가 그 노드·벤더에 대해
// 영구히 평가되지 않는다. 자동 업그레이드를 끈 정책이 바로 그 감지에 기대는 구성이다.
func TestSyncUpgradeStateDrift_UnverifiedVersionStillObserves(t *testing.T) {
	const (
		nodeName        = "worker-drift-parked"
		dusName         = "worker-drift-parked-furiosa"
		declaredVersion = "580.178.04"
		observedVersion = "595.91.07"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy",
		v1alpha1.UpgradeStateUnverifiedVersion, declaredVersion, declaredVersion)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", declaredVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: observedVersion}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	if !hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("주차 상태에서 DriverVersionDrift 경고가 발행되지 않음 — 호스트 밖 변경 감지가 꺼졌다")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.CurrentVersion != observedVersion {
		t.Errorf("주차 상태에서 currentVersion 이 실측을 따르지 않음: got %q, want %q",
			got.Status.CurrentVersion, observedVersion)
	}
	if got.Status.State != v1alpha1.UpgradeStateUnverifiedVersion {
		t.Errorf("관측 갱신이 상태를 바꿈: got %q, want %q",
			got.Status.State, v1alpha1.UpgradeStateUnverifiedVersion)
	}
}

// TestSyncUpgradeStateDrift_HostAheadNoWarning 은 HostAhead 에서 어긋남이 유예를 넘겨도
// DriverVersionDrift 경고를 내지 않음을 고정한다 — 호스트가 정책보다 높다는 같은 사실을 상태
// 기계의 HostDriverAhead 이벤트가 이미 알린다. currentVersion 은 계속 실측을 따른다.
func TestSyncUpgradeStateDrift_HostAheadNoWarning(t *testing.T) {
	const (
		nodeName      = "worker-drift-hostahead"
		dusName       = "worker-drift-hostahead-furiosa"
		policyVersion = "580.126.09"
		hostVersion   = "580.178.04"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy",
		v1alpha1.UpgradeStateHostAhead, policyVersion, policyVersion)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", policyVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: hostVersion, DriverLoaded: true}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, hostVersion)

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("HostAhead 인데 DriverVersionDrift 경고가 발행됨 — HostDriverAhead 와 같은 사실의 중복")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.CurrentVersion != hostVersion {
		t.Errorf("HostAhead 에서 currentVersion 이 실측을 따르지 않음: got %q, want %q",
			got.Status.CurrentVersion, hostVersion)
	}
}

// TestSyncUpgradeStateDrift_RollbackHoldUsesRestoredVersion 은 롤백 뒤 보류 중(목표 = rolledBackVersion)
// 이면 드리프트 기준이 롤백이 복원한 previousVersion 임을 고정한다. 정책 목표와 비교하면 호스트가
// 복원 버전 그대로인데도 유예가 지나면 "operator 밖에서 바뀌었다" 는 거짓 경고가 난다.
func TestSyncUpgradeStateDrift_RollbackHoldUsesRestoredVersion(t *testing.T) {
	const (
		nodeName = "worker-drift-hold"
		dusName  = "worker-drift-hold-furiosa"
		target   = "580.999.99"
		restored = "580.178.04"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, restored, target)
	dus.Status.RolledBackVersion = target
	dus.Status.PreviousVersion = restored
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", target)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: restored}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("롤백 보류 중 호스트가 복원 버전인데 DriverVersionDrift 경고가 발행됨")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DriftFirstSeen != nil {
		t.Errorf("복원 버전과 일치하는데 DriftFirstSeen 이 남음: %v", got.Status.DriftFirstSeen)
	}
}

// TestSyncUpgradeStateDrift_RollbackHoldStillDetectsHostChange 는 보류 중에도 호스트가 복원 버전에서
// 벗어나면 경고한다는 것을 고정한다. 보류를 경고 생략으로 처리하면 이 변경을 놓친다.
func TestSyncUpgradeStateDrift_RollbackHoldStillDetectsHostChange(t *testing.T) {
	const (
		nodeName = "worker-drift-hold-moved"
		dusName  = "worker-drift-hold-moved-furiosa"
		target   = "580.999.99"
		restored = "580.178.04"
		moved    = "595.91.07"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, restored, target)
	dus.Status.RolledBackVersion = target
	dus.Status.PreviousVersion = restored
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", target)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: moved}
	r.syncUpgradeState(context.Background(), nodeName, device, dip, "warboy", nil, "")

	if !hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("보류 중 호스트가 복원 버전에서 바뀌었는데 DriverVersionDrift 경고가 없음")
	}
}

// TestSyncUpgradeStateDrift_RollbackHoldReachedTarget 은 보류 중인 노드가 다른 경로(호스트에서 직접
// 설치 등)로 보류 목표에 도달하면 경고하지 않음을 고정한다. previousVersion 을 기준으로 두면 보류가
// 풀리기 전 reconcile 에서 목표에 수렴한 노드에 DriverVersionDrift 경고가 난다.
func TestSyncUpgradeStateDrift_RollbackHoldReachedTarget(t *testing.T) {
	const (
		nodeName = "worker-drift-hold-reached"
		dusName  = "worker-drift-hold-reached-furiosa"
		target   = "580.126.09"
		previous = "580.178.04"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, target, target)
	dus.Status.RolledBackVersion = target
	dus.Status.PreviousVersion = previous
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", target)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: target}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("보류 목표에 도달한 노드에 DriverVersionDrift 경고가 발행됨")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DriftFirstSeen != nil {
		t.Errorf("관측이 목표와 같은데 DriftFirstSeen 이 남음: %v", got.Status.DriftFirstSeen)
	}
}

// TestSyncUpgradeState_HostSourceAdoptsLoadedVersion 은 versionSource=Host 에서 DUS 의 desired 와
// 드리프트 기준이 정책 버전이 아니라 NDR 이 보고한 로드된 호스트 버전임을 고정한다. 정책 버전을
// 기준으로 두면 desired 동기화가 호스트와 어긋난 값을 쓰고 유예가 지나면 DriverVersionDrift 경고가
// 호스트가 정책과 다른 노드마다 반복된다.
func TestSyncUpgradeState_HostSourceAdoptsLoadedVersion(t *testing.T) {
	const (
		nodeName   = "worker-host-source"
		dusName    = "worker-host-source-furiosa"
		dipVersion = "580.126.09"
		hostLoaded = "580.178.04"
	)
	firstSeen := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateIdle, hostLoaded, dipVersion)
	dus.Status.DriftFirstSeen = &firstSeen

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", dipVersion)
	dip.Spec.Driver.VersionSource = "Host"
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: hostLoaded, DriverLoaded: true}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, hostLoaded)

	if hasRecorderEventReason(fakeRec, "DriverVersionDrift") {
		t.Errorf("Host 모드인데 호스트 버전이 목표와 달라 DriverVersionDrift 경고가 발행됨")
	}
	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DesiredVersion != hostLoaded {
		t.Errorf("DesiredVersion: got %q, want %q(로드된 호스트 버전)", got.Status.DesiredVersion, hostLoaded)
	}
}

// TestEnsureUpgradeStates_HostSourceMatchesStateMachineChoice 는 같은 벤더 NDR 항목이 둘이고
// 로드되지 않은 쪽의 버전이 더 높을 때 DUS 목표가 항목 순서와 무관하게 상태 기계가 고르는 값
// (모듈이 로드된 첫 장치의 버전)임을 고정한다. 목표를 항목마다 따로 구하면 한 reconcile pass 안에서
// desired 가 정책 버전과 호스트 버전 사이를 오가고, 마지막 항목이 로드되지 않은 쪽이면 정책 버전이
// 남아 상태 기계와 어긋난다.
func TestEnsureUpgradeStates_HostSourceMatchesStateMachineChoice(t *testing.T) {
	const (
		nodeName   = "worker-host-two"
		dusName    = "worker-host-two-furiosa"
		model      = "warboy"
		dipVersion = "580.126.09"
		hostLoaded = "580.178.04"
		unloaded   = "595.91.07"
	)
	loadedEntry := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: model, DriverVersion: hostLoaded, DriverLoaded: true}
	unloadedEntry := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: model, DriverVersion: unloaded}
	orders := map[string][]v1alpha1.DeviceEntry{
		"unloaded-first": {unloadedEntry, loadedEntry},
		"loaded-first":   {loadedEntry, unloadedEntry},
	}
	for name, devices := range orders {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existingDUS=%v", name, existing), func(t *testing.T) {
				ndr := makeNDR(nodeName, model, "")
				ndr.Status.Devices = devices
				dip := makeDIP("furiosa-warboy", model, dipVersion)
				dip.Spec.Driver.VersionSource = "Host"
				objs := []client.Object{workerNode(nodeName), ndr, dip}
				if existing {
					objs = append(objs, makeDUS(dusName, nodeName, "furiosa", model,
						v1alpha1.UpgradeStateIdle, hostLoaded, dipVersion))
				}
				r := newReconciler(objs...)
				r.Recorder = record.NewFakeRecorder(8)
				ctx := context.Background()
				if err := r.ensureUpgradeStates(ctx); err != nil {
					t.Fatalf("ensureUpgradeStates 오류: %v", err)
				}
				var got v1alpha1.DriverUpgradeState
				if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
					t.Fatalf("DUS 조회 실패: %v", err)
				}
				want := upgrade.LoadedHostVersion(devices, "furiosa", model)
				if want != hostLoaded {
					t.Fatalf("상태 기계의 선택 = %q, %q 기대(시험 전제)", want, hostLoaded)
				}
				if got.Status.DesiredVersion != want {
					t.Errorf("DesiredVersion = %q, 상태 기계의 선택 %q 기대", got.Status.DesiredVersion, want)
				}
			})
		}
	}
}

// TestSyncUpgradeState_UnverifiedVersionKeepsRebootBudget 는 주차 상태에 넓힌 것이 관측 두
// 블록뿐임을 고정한다. DesiredVersion 되돌림 블록은 재부팅 카운터·마커를 리셋하므로 Idle
// 전용으로 남아야 한다 — 주차 상태까지 넓히면 MaxReboots 상한이 무력화된다.
func TestSyncUpgradeState_UnverifiedVersionKeepsRebootBudget(t *testing.T) {
	const (
		nodeName   = "worker-parked-budget"
		dusName    = "worker-parked-budget-furiosa"
		dusDesired = "580.178.04"
		dipVersion = "595.91.07" // 정책이 선언한 허용 목록 밖 버전(DUS 기록과 다름)
	)
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy",
		v1alpha1.UpgradeStateUnverifiedVersion, dusDesired, dusDesired)
	dus.Status.RebootAttempts = 1

	r := newReconciler(dus)
	fakeRec := record.NewFakeRecorder(8)
	r.Recorder = fakeRec

	dip := makeDIP("furiosa-warboy", "warboy", dipVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: dusDesired}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DesiredVersion != dusDesired {
		t.Errorf("주차 상태에서 DesiredVersion 이 정책값으로 되돌려짐: got %q, want %q",
			got.Status.DesiredVersion, dusDesired)
	}
	if got.Status.RebootAttempts != 1 {
		t.Errorf("RebootAttempts: got %d, want 1 — 주차 상태에서 재부팅 예산이 리셋됨",
			got.Status.RebootAttempts)
	}
}

// ─────────────────────────────────────────────
// 되돌림 vs 롤백 (되돌림은 Idle 에서만)
// ─────────────────────────────────────────────

// TestSyncUpgradeState_RollbackDivergence_PreservesRebootBudget 는 롤백 중(mid-cycle) DUS 에서
// DesiredVersion(==PreviousVersion)이 DIP 선언 버전과 달라도 되돌림 분기가 되돌리지 않음을
// 고정한다. 상태 무관 되돌림은 롤백을 깨고 매 iteration 마다 재부팅 예산을 리셋해 MaxReboots
// 상한을 무력화한다.
func TestSyncUpgradeState_RollbackDivergence_PreservesRebootBudget(t *testing.T) {
	const (
		nodeName    = "worker-rollback"
		dusName     = "worker-rollback-furiosa"
		prevVersion = "1.8.0"   // 롤백 목표: DesiredVersion == PreviousVersion
		dipVersion  = "1.9.8-3" // DIP 는 여전히 상위 버전을 선언(DesiredVersion·PreviousVersion 과 둘 다 다름)
	)
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateValidating, prevVersion, prevVersion)
	dus.Status.PreviousVersion = prevVersion
	dus.Status.RebootAttempts = 1

	r := newReconciler(dus)
	dip := makeDIP("furiosa-warboy", "warboy", dipVersion)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: prevVersion}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.RebootAttempts != 1 {
		t.Errorf("RebootAttempts: got %d, want 1 — 롤백 중인데 재부팅 예산이 리셋됨(MaxReboots 상한 무력화)",
			got.Status.RebootAttempts)
	}
	if got.Status.DesiredVersion != prevVersion {
		t.Errorf("DesiredVersion: got %q, want %q(PreviousVersion) — 되돌림 분기가 롤백 중인 DesiredVersion 을 DIP 선언 버전으로 되돌렸다",
			got.Status.DesiredVersion, prevVersion)
	}
	if got.Status.State != v1alpha1.UpgradeStateValidating {
		t.Errorf("state: got %q, want %q — 유지되어야 함", got.Status.State, v1alpha1.UpgradeStateValidating)
	}
}

// 롤백 보류 중 자체 복구 사이클(startHeldRestore)은 보류 목표 대신 복원 버전을 DesiredVersion 에
// 둔다. 컨트롤러가 이를 정책 버전(보류 목표)으로 덮으면 복구가 실패한 목표를 다시 설치한다.
func TestSyncUpgradeState_HeldRestoreKeepsDesiredVersion(t *testing.T) {
	const (
		nodeName   = "worker-held"
		dusName    = "worker-held-furiosa"
		restored   = "1.8.0"   // 롤백이 복원한 버전
		heldTarget = "1.9.8-3" // 롤백한 목표 = 정책 버전
	)
	dus := makeDUS(dusName, nodeName, "furiosa", "warboy", v1alpha1.UpgradeStateRequired, restored, restored)
	dus.Status.PreviousVersion = restored
	dus.Status.RolledBackVersion = heldTarget
	dus.Status.RolledBackGeneration = 2

	r := newReconciler(dus)
	dip := makeDIP("furiosa-warboy", "warboy", heldTarget)
	device := v1alpha1.DeviceEntry{Vendor: "furiosa", Model: "warboy", DriverVersion: restored}
	ctx := context.Background()
	r.syncUpgradeState(ctx, nodeName, device, dip, "warboy", nil, "")

	var got v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: dusName}, &got); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if got.Status.DesiredVersion != restored {
		t.Errorf("DesiredVersion: got %q, want %q — 보류 중 복구 사이클의 목표를 보류 목표로 덮음",
			got.Status.DesiredVersion, restored)
	}
	if got.Status.State != v1alpha1.UpgradeStateRequired || got.Status.RolledBackVersion != heldTarget {
		t.Errorf("state=%q rolledBack=%q, want %q/%q", got.Status.State, got.Status.RolledBackVersion,
			v1alpha1.UpgradeStateRequired, heldTarget)
	}
}

// TestVendorObservedModels 는 벤더별 관측 모델 목록이 정렬·중복 제거되고, 모델이 빈
// 장치(예: rebellions)는 목록에 들어가지 않는지 본다.
func TestVendorObservedModels(t *testing.T) {
	got := vendorObservedModels([]v1alpha1.DeviceEntry{
		{Vendor: "nvidia", Model: "a30"}, {Vendor: "nvidia", Model: "a2"},
		{Vendor: "nvidia", Model: "a30"}, {Vendor: "furiosa", Model: "warboy"}, {Vendor: "rebellions"},
	})
	if strings.Join(got["nvidia"], ",") != "a2,a30" || strings.Join(got["furiosa"], ",") != "warboy" {
		t.Fatalf("got %v", got)
	}
	if _, has := got["rebellions"]; has {
		t.Fatal("모델이 빈 장치는 목록에 넣지 않는다")
	}
}

// TestEnsureUpgradeStates_MixedModelsRecordsObservedModels 는 혼재 노드에서 spec.model 이
// generic 으로 뭉개져도 status.observedModels 에 실제 관측된 제품 목록이 정렬·중복 제거된
// 상태로 남는지 본다.
func TestEnsureUpgradeStates_MixedModelsRecordsObservedModels(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "mixed-node"}}
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed-node"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "mixed-node"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "nvidia", Model: "a30", DriverVersion: "580.173.02"},
			{Vendor: "nvidia", Model: "a2", DriverVersion: "580.173.02"},
		}},
	}
	dip := makeNvidiaDIP("nvidia-generic", modelUnknown)
	r := newReconciler(node, ndr, dip)
	ctx := context.Background()

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("ensureUpgradeStates: %v", err)
	}

	var dus v1alpha1.DriverUpgradeState
	if err := r.Get(ctx, types.NamespacedName{Name: driverUpgradeStateName("mixed-node", "nvidia")}, &dus); err != nil {
		t.Fatalf("DUS 조회 실패: %v", err)
	}
	if dus.Spec.Model != v1alpha1.DriverModelUnknown {
		t.Fatalf("spec.model = %q, want generic", dus.Spec.Model)
	}
	if strings.Join(dus.Status.ObservedModels, ",") != "a2,a30" {
		t.Fatalf("status.observedModels = %v, want [a2 a30]", dus.Status.ObservedModels)
	}
}

// rebellionsTrackOnlyFixture 는 설치기 없는 벤더의 TrackOnly 정책과 ATOM+ 2장 노드다.
// versionSource·정책 버전·호스트 버전만 케이스별로 바꾼다.
func rebellionsTrackOnlyFixture(versionSource, policyVersion, hostVersion string) (
	*corev1.Node, *v1alpha1.NodeDeviceReport, *v1alpha1.DriverInstallPolicy,
) {
	node := workerNode("worker4")
	node.Labels["kcloud.ai/rebellions.present"] = labelValueTrue
	ndr := &v1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "worker4"},
		Spec:       v1alpha1.NodeDeviceReportSpec{NodeName: "worker4"},
		Status: v1alpha1.NodeDeviceReportStatus{Devices: []v1alpha1.DeviceEntry{
			{Vendor: "rebellions", Model: "atom", PCIeAddress: "0000:c3:00.0",
				DriverLoaded: hostVersion != "", DriverVersion: hostVersion},
			{Vendor: "rebellions", Model: "atom", PCIeAddress: "0000:c4:00.0",
				DriverLoaded: hostVersion != "", DriverVersion: hostVersion},
		}},
	}
	dip := &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "rebellions-atom-ds"},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: "rebellions", Model: "atom",
			NodeSelector: map[string]string{"kcloud.ai/rebellions.present": labelValueTrue},
			Driver: v1alpha1.DriverSpec{
				Mode: "job", TrackOnly: true, Version: policyVersion, VersionSource: versionSource,
			},
		},
	}
	return node, ndr, dip
}

func getTrackOnlyDUS(t *testing.T, r *DriverUpgradeReconciler, name string) v1alpha1.DriverUpgradeState {
	t.Helper()
	var dus v1alpha1.DriverUpgradeState
	if err := r.Get(context.Background(), types.NamespacedName{Name: name}, &dus); err != nil {
		t.Fatalf("DUS %s 조회 실패: %v", name, err)
	}
	return dus
}

// TrackOnly + versionSource=Host: 정책에 버전이 없어도 desired 가 호스트 버전을 따라가
// Idle/3.0.0/3.0.0 으로 수렴하고, 두 번째 pass 에서 다시 쓰지 않는다.
func TestEnsureUpgradeStates_TrackOnlyHostSourceFollowsHostVersion(t *testing.T) {
	const hostVersion = "3.0.0"
	node, ndr, dip := rebellionsTrackOnlyFixture("Host", "", hostVersion)
	r := newReconciler(node, ndr, dip)
	ctx := context.Background()

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("1차 ensureUpgradeStates: %v", err)
	}
	first := getTrackOnlyDUS(t, r, driverUpgradeStateName("worker4", "rebellions"))
	if first.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle", first.Status.State)
	}
	if first.Status.CurrentVersion != hostVersion || first.Status.DesiredVersion != hostVersion {
		t.Errorf("current=%q desired=%q, want 3.0.0/3.0.0 (versionSource=Host 는 호스트 버전을 desired 로 쓴다)",
			first.Status.CurrentVersion, first.Status.DesiredVersion)
	}
	if first.Spec.Model != "atom" {
		t.Errorf("spec.model=%q, want atom", first.Spec.Model)
	}

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("2차 ensureUpgradeStates: %v", err)
	}
	second := getTrackOnlyDUS(t, r, first.Name)
	if second.ResourceVersion != first.ResourceVersion {
		t.Errorf("변화가 없는데 두 번째 pass 가 또 썼다: ResourceVersion %s → %s",
			first.ResourceVersion, second.ResourceVersion)
	}
}

// 드라이버가 올라오지 않은 노드(버전 빈 값)에서는 Host 소스가 정책 버전으로 물러난다.
// 정책 버전도 비면 둘 다 빈 값이고 오류는 없다.
func TestEnsureUpgradeStates_TrackOnlyHostSourceWithoutLoadedDriver(t *testing.T) {
	node, ndr, dip := rebellionsTrackOnlyFixture("Host", "", "")
	r := newReconciler(node, ndr, dip)
	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("ensureUpgradeStates: %v", err)
	}
	dus := getTrackOnlyDUS(t, r, driverUpgradeStateName("worker4", "rebellions"))
	if dus.Status.State != v1alpha1.UpgradeStateIdle || dus.Status.CurrentVersion != "" ||
		dus.Status.DesiredVersion != "" {
		t.Errorf("state=%q current=%q desired=%q, want Idle/빈 값/빈 값",
			dus.Status.State, dus.Status.CurrentVersion, dus.Status.DesiredVersion)
	}
}

// versionSource=Policy 는 종전과 같다 — 정책 버전이 desired 로 남고 호스트와 달라도 Idle 이다.
func TestEnsureUpgradeStates_TrackOnlyPolicySourceKeepsPolicyVersion(t *testing.T) {
	const hostVersion = "3.0.0"
	node, ndr, dip := rebellionsTrackOnlyFixture("Policy", "2.9.0", hostVersion)
	r := newReconciler(node, ndr, dip)
	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("ensureUpgradeStates: %v", err)
	}
	dus := getTrackOnlyDUS(t, r, driverUpgradeStateName("worker4", "rebellions"))
	if dus.Status.State != v1alpha1.UpgradeStateIdle || dus.Status.CurrentVersion != hostVersion ||
		dus.Status.DesiredVersion != "2.9.0" {
		t.Errorf("state=%q current=%q desired=%q, want Idle/3.0.0/2.9.0",
			dus.Status.State, dus.Status.CurrentVersion, dus.Status.DesiredVersion)
	}
}

// nodeSelector 밖 노드(Rebellions 라벨 없음)에는 DUS 가 생기지 않는다 — DIP 를 기본으로 켜도
// 다른 노드에 행이 번지지 않는다.
func TestEnsureUpgradeStates_TrackOnlySkipsNodesOutsideSelector(t *testing.T) {
	node, ndr, dip := rebellionsTrackOnlyFixture("Host", "", "3.0.0")
	delete(node.Labels, "kcloud.ai/rebellions.present")
	r := newReconciler(node, ndr, dip)
	if err := r.ensureUpgradeStates(context.Background()); err != nil {
		t.Fatalf("ensureUpgradeStates: %v", err)
	}
	var dus v1alpha1.DriverUpgradeState
	err := r.Get(context.Background(), types.NamespacedName{Name: driverUpgradeStateName("worker4", "rebellions")}, &dus)
	if err == nil {
		t.Fatalf("셀렉터 밖 노드에 DUS 가 생겼다: %+v", dus.Status)
	}
}

// 실제 API 서버는 Create 때 status 를 버려 두 번째 pass 의 patch 가 desired 를 채운다. fake client 는 그 경로를
// 타지 않으므로 빈 status 의 DUS 를 미리 두고, Idle/호스트 버전으로 채운 뒤 다시 쓰지 않는지 본다.
func TestEnsureUpgradeStates_TrackOnlyHostSourcePatchesEmptyStatus(t *testing.T) {
	const hostVersion = "3.0.0"
	node, ndr, dip := rebellionsTrackOnlyFixture("Host", "", hostVersion)
	seed := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: driverUpgradeStateName("worker4", "rebellions")},
		Spec:       v1alpha1.DriverUpgradeStateSpec{NodeName: "worker4", Vendor: "rebellions", Model: "atom"},
	}
	r := newReconciler(node, ndr, dip, seed)
	ctx := context.Background()

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("1차 ensureUpgradeStates: %v", err)
	}
	first := getTrackOnlyDUS(t, r, seed.Name)
	if first.Status.State != v1alpha1.UpgradeStateIdle || first.Status.CurrentVersion != hostVersion ||
		first.Status.DesiredVersion != hostVersion {
		t.Errorf("state=%q current=%q desired=%q, want Idle/3.0.0/3.0.0",
			first.Status.State, first.Status.CurrentVersion, first.Status.DesiredVersion)
	}

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("2차 ensureUpgradeStates: %v", err)
	}
	second := getTrackOnlyDUS(t, r, seed.Name)
	if second.ResourceVersion != first.ResourceVersion {
		t.Errorf("수렴한 뒤에 다시 썼다: ResourceVersion %s → %s", first.ResourceVersion, second.ResourceVersion)
	}
}

// 이미 Idle 이고 current 가 맞아도 desired 가 낡았으면 patch 로 바로잡는다. 정책 버전을 올리거나
// versionSource 를 Policy 에서 Host 로 바꾼 뒤 기존 DUS 가 따라가는 경로다.
func TestEnsureUpgradeStates_TrackOnlyHostSourceRefreshesStaleDesired(t *testing.T) {
	const hostVersion = "3.0.0"
	node, ndr, dip := rebellionsTrackOnlyFixture("Host", "2.9.0", hostVersion)
	seed := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: driverUpgradeStateName("worker4", "rebellions")},
		Spec:       v1alpha1.DriverUpgradeStateSpec{NodeName: "worker4", Vendor: "rebellions", Model: "atom"},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State: v1alpha1.UpgradeStateIdle, CurrentVersion: hostVersion, DesiredVersion: "2.9.0",
		},
	}
	r := newReconciler(node, ndr, dip, seed)
	ctx := context.Background()

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("1차 ensureUpgradeStates: %v", err)
	}
	first := getTrackOnlyDUS(t, r, seed.Name)
	if first.Status.DesiredVersion != hostVersion {
		t.Errorf("desired=%q, want %s (낡은 desired 를 호스트 버전으로 갱신해야 한다)",
			first.Status.DesiredVersion, hostVersion)
	}

	if err := r.ensureUpgradeStates(ctx); err != nil {
		t.Fatalf("2차 ensureUpgradeStates: %v", err)
	}
	second := getTrackOnlyDUS(t, r, seed.Name)
	if second.ResourceVersion != first.ResourceVersion {
		t.Errorf("갱신한 뒤에 다시 썼다: ResourceVersion %s → %s", first.ResourceVersion, second.ResourceVersion)
	}
}

// 이전 목표 버전이 비어 있으면 화살표 문구 대신 기록 문구를 쓴다.
func TestDesiredVersionChangeMessage(t *testing.T) {
	if got := desiredVersionChangeMessage("", "580.1"); got != "목표 버전 580.1 기록(새 DriverUpgradeState)" {
		t.Errorf("빈 이전 값: %q", got)
	}
	if got := desiredVersionChangeMessage("570.1", "580.1"); got != "정책 버전 변경: 570.1 → 580.1" {
		t.Errorf("이전 값 있음: %q", got)
	}
}
