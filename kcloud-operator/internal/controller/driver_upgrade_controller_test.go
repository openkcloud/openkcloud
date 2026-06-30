// ============================================================
// driver_upgrade_controller_test.go: DriverUpgradeReconciler 단위 테스트
// 상세: ensureUpgradeStates()의 currentVersion 동기화 및 findPolicy fallback 로직 검증
//       fake client 기반 — envtest 불필요
// 생성일: 2026-04-21
// ============================================================

package controller

import (
	"context"
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

// TestEnsureUpgradeStates_CurrentVersionSyncFromNDR 는 P0-2 버그를 직접 재현합니다.
// 시나리오: DUS가 Idle 상태이고 currentVersion이 빈 문자열인데
//
//	NDR이 desiredVersion과 동일한 버전을 보고하는 경우 → currentVersion이 채워져야 한다.
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

	if got.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("state: got %q, want %q", got.Status.State, v1alpha1.UpgradeStateRequired)
	}
	if got.Status.CurrentVersion != installedVer {
		t.Errorf("currentVersion: got %q, want %q", got.Status.CurrentVersion, installedVer)
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

// TestFindPolicy_RngdModelMatch 는 같은 vendor(furiosa) 아래 warboy/rngd 2개 DIP 중
// vendor=furiosa, model=rngd 요청에 rngd DIP 가 정확히 매칭되는지 검증합니다.
// (B-5 RNGD 호환성 확인: findPolicy 가 model 기반 분기를 지원해야 함)
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
// 옵션 A: detector phase-aware blocking label 시나리오
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
// Validating self-heal 시나리오 (architectural plan §4.A)
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
// (예: ":580.126.09") 를 PreviousImage 로 캡처하지 않음을 검증한다.
// (architectural plan §3.4 — rollback 시 broken Pod 회귀 차단)
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
// IdleCooldown 시나리오 (followup plan §F3 — operator 측 P0 가드)
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
// followup plan §F3 — rolling-update 연속 trigger 시 mid-state 오인 방지의 P0 가드.
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
// A6.1 Quiesce-on-Driver-Upgrade 시나리오
// (architectural plan §A6.1 — opt-in label 기반 자동 scale=0/restore)
// ─────────────────────────────────────────────

// makeQuiesceLabeledDeploy 는 quiesce-on-driver-upgrade 라벨이 붙고 nodeSelector 의 hostname
// 으로 특정 노드를 타겟하는 Deployment 를 만든다 (gpu-stress 와 동일한 형태).
func makeQuiesceLabeledDeploy(name, nodeName string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "npu-operator",
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
		deployNS  = "npu-operator"
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
		deployNS  = "npu-operator"
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
		deployNS  = "npu-operator"
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
// 검증한다 (followup plan §F2 — operator restart resilience).
func TestQuiesce_AnnotationBackup_Persisted(t *testing.T) {
	const (
		nodeName  = "worker-anno-backup"
		vendor    = "nvidia"
		dusName   = "worker-anno-backup-nvidia"
		deployNS  = "npu-operator"
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
// fallback 복구가 동작함을 검증한다 (followup plan §F2).
func TestRestore_FromAnnotation_Fallback(t *testing.T) {
	const (
		nodeName  = "worker-anno-fallback"
		vendor    = "nvidia"
		dusName   = "worker-anno-fallback-nvidia"
		deployNS  = "npu-operator"
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
