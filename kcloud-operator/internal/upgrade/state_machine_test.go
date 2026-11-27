// ============================================================
// state_machine_test.go: UpgradeStateMachine 단위 테스트
// 상세: PreviousImage broken tag 정리, Rollback 소진 시 Failed 터미널 정지, 소비된 rollback
//       요청 어노테이션 재소비 차단을 fake client 로 검증한다.
// 생성일: 2026-04-28 | 수정일: 2026-10-14
// ============================================================

package upgrade

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// newUpgradeTestScheme 은 upgrade 패키지 테스트용 scheme 을 반환한다.
func newUpgradeTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	return s
}

// newUpgradeSMWithRecorder 는 Recorder 가 부착된 UpgradeStateMachine 과 fake client 를 반환한다.
func newUpgradeSMWithRecorder(objs ...client.Object) *UpgradeStateMachine {
	s := newUpgradeTestScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.DriverUpgradeState{}).
		Build()
	return &UpgradeStateMachine{
		Client:   c,
		Recorder: record.NewFakeRecorder(64),
	}
}

// makeIdleDUSWithPreviousImage 는 Idle 상태에서 PreviousImage 가 지정된 DUS 를 반환한다.
// reconcile 진입 시 broken tag 정리 검증에 사용.
func makeIdleDUSWithPreviousImage(name, nodeName, vendor, version, previousImage string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:          v1alpha1.UpgradeStateIdle,
			CurrentVersion: version,
			PreviousImage:  previousImage,
		},
	}
}

// makeIdleDIP 는 autoUpgrade 비활성화 DIP 를 반환한다.
// handleIdle 이 버전 일치로 즉시 리턴되므로 K8s 리소스 추가 불필요.
func makeIdleDIP(name, vendor, version string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Driver: v1alpha1.DriverSpec{Version: version},
		},
	}
}

// TestPreviousImage_BrokenTag_Cleared 는 reconcile 진입 시 plain tag (예: ":580.142") 가
// PreviousImage 에 잔류하는 경우 자동 정리됨을 검증한다 — broken plain tag 가 rollback 시
// 그대로 쓰이는 회귀를 막는다.
func TestPreviousImage_BrokenTag_Cleared(t *testing.T) {
	const (
		nodeName    = "worker-sanitize-broken"
		vendor      = "nvidia"
		version     = "580.142"
		dusName     = "worker-sanitize-broken-nvidia"
		brokenImage = "registry.example.com/nvidia-driver-ds:580.142" // plain tag — no -v<N>
	)

	dus := makeIdleDUSWithPreviousImage(dusName, nodeName, vendor, version, brokenImage)
	dip := makeIdleDIP("nvidia-generic", vendor, version)

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	// broken plain tag 는 reconcile 진입 시 즉시 정리되어야 한다.
	if dus.Status.PreviousImage != "" {
		t.Errorf("broken plain tag 가 PreviousImage 에 잔류: got %q, want \"\"", dus.Status.PreviousImage)
	}
}

// TestPreviousImage_VerifiedTag_Kept 는 reconcile 진입 시 -v<N> 접미사 검증 빌드 태그가
// PreviousImage 에 그대로 보존됨을 검증한다 (회귀 방지).
func TestPreviousImage_VerifiedTag_Kept(t *testing.T) {
	const (
		nodeName      = "worker-sanitize-verified"
		vendor        = "nvidia"
		version       = "580.142"
		dusName       = "worker-sanitize-verified-nvidia"
		verifiedImage = "registry.example.com/nvidia-driver-ds:580.142-v172" // -v<N> suffix
	)

	dus := makeIdleDUSWithPreviousImage(dusName, nodeName, vendor, version, verifiedImage)
	dip := makeIdleDIP("nvidia-generic-v", vendor, version)

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	// verified build tag 는 보존되어야 한다 (sanitize 대상 아님).
	if dus.Status.PreviousImage != verifiedImage {
		t.Errorf("verified build tag 가 의도치 않게 제거됨: got %q, want %q",
			dus.Status.PreviousImage, verifiedImage)
	}
}

// TestHandleIdle_EmptyState_NormalizedToIdle 는 state="" 인 신규 DUS 가 현재버전==목표버전
// 이어서 업그레이드 사이클을 거치지 않을 때, handleIdle 이 status.state 를 Idle 로 1회
// 정규화해 `kubectl get dus` STATE 가 빈 문자열로 남지 않게 하는지 검증한다.
func TestHandleIdle_EmptyState_NormalizedToIdle(t *testing.T) {
	const (
		nodeName = "worker-empty-normalize"
		vendor   = "nvidia"
		version  = "580.159.03"
		dusName  = "worker-empty-normalize-nvidia"
	)

	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:          "", // 신규 DUS: 미초기화 상태
			CurrentVersion: version,
		},
	}
	dip := makeIdleDIP("nvidia-generic", vendor, version) // desired == current

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("빈 상태 정규화 실패: state = %q, want %q", dus.Status.State, v1alpha1.UpgradeStateIdle)
	}
}

// TestReplaceImageTag_PreservesVariantSuffix 는 replaceImageTag 가 newTag 가 plain 일 때
// 기존 tag 의 variant suffix (-v<N>) 를 보존함을 검증한다.
func TestReplaceImageTag_PreservesVariantSuffix(t *testing.T) {
	cases := []struct {
		name     string
		image    string
		newTag   string
		expected string
	}{
		{"plain newTag preserves -v172", "registry/repo:595.58.03-v172", "580.142", "registry/repo:580.142-v172"},
		{"plain newTag preserves -v16", "registry/repo:580.142-v16", "535.288.01", "registry/repo:535.288.01-v16"},
		{"plain newTag preserves -v2", "registry/repo:1.7.8-v2", "1.9.8-3", "registry/repo:1.9.8-3-v2"},
		{"newTag already has variant", "registry/repo:580.142-v16", "590.48.01-v172", "registry/repo:590.48.01-v172"},
		{"image without tag adds plain", "registry/repo", "580.142", "registry/repo:580.142"},
		{"image with plain tag, no variant to preserve", "registry/repo:580.142", "535.288.01", "registry/repo:535.288.01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := replaceImageTag(tc.image, tc.newTag)
			if got != tc.expected {
				t.Errorf("replaceImageTag(%q, %q) = %q, want %q", tc.image, tc.newTag, got, tc.expected)
			}
		})
	}
}

// TestExtractImageVariantSuffix 는 extractImageVariantSuffix 가 tag 에서 -v<N> 접미사를
// 정확히 추출하고, 없으면 빈 문자열을 반환함을 검증한다.
func TestExtractImageVariantSuffix(t *testing.T) {
	cases := map[string]string{
		"registry/repo:580.142-v172": "-v172",
		"registry/repo:1.7.8-v2":     "-v2",
		"registry/repo:580.142":      "",
		"registry/repo":              "",
		// 커널 토큰은 variant 가 아니므로 재구성에 쓰지 않는다(kernelImages 는 previousImage
		// 캡처만 사용). variant 접미사는 태그 맨 끝 -v1 이며, 그 앞의 커널 토큰은 무시된다.
		"reg/kcloud/tenstorrent-driver-ds:2.11.0-k5.15.0-190-generic-v1": "-v1",
	}
	for image, expected := range cases {
		if got := extractImageVariantSuffix(image); got != expected {
			t.Errorf("extractImageVariantSuffix(%q) = %q, want %q", image, got, expected)
		}
	}
}

// ─────────────────────────────────────────────
// verifiedVersions 화이트리스트 검증 테스트
// ─────────────────────────────────────────────

func int32Ptr(v int32) *int32 { return &v }

// makeDIPWithVerifiedVersions 는 autoUpgrade=true + verifiedVersions 가 지정된 DIP 를 반환한다.
func makeDIPWithVerifiedVersions(name, vendor, version string, verifiedVersions []string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Driver: v1alpha1.DriverSpec{
				Version: version,
				Image:   "registry/driver:" + version,
			},
			UpgradePolicy: &v1alpha1.UpgradePolicy{
				AutoUpgrade:         true,
				IdleCooldownSeconds: int32Ptr(0), // cooldown 비활성화 (즉시 trigger)
			},
			VerifiedVersions: verifiedVersions,
		},
	}
}

// makeIdleDUSWithCurrentVersion 은 지정한 currentVersion 으로 Idle 상태인 DUS 를 반환한다.
func makeIdleDUSWithCurrentVersion(name, nodeName, vendor, currentVersion string) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:          v1alpha1.UpgradeStateIdle,
			CurrentVersion: currentVersion,
		},
	}
}

// TestVerifiedVersions_Reject 는 DIP.spec.verifiedVersions 에 없는 버전이
// desiredVersion 으로 지정된 경우 DUS 가 UnverifiedVersion 터미널 상태로 전이함을 검증한다.
func TestVerifiedVersions_Reject(t *testing.T) {
	const (
		nodeName       = "worker-vv-reject"
		vendor         = "nvidia"
		currentVersion = "535.288.01"
		desiredVersion = "999.0.0" // 화이트리스트에 없는 버전
		dusName        = "worker-vv-reject-nvidia"
	)
	verifiedVersions := []string{"535.288.01", "580.126.09", "580.142", "595.58.03"}

	dus := makeIdleDUSWithCurrentVersion(dusName, nodeName, vendor, currentVersion)
	dip := makeDIPWithVerifiedVersions("nvidia-generic-reject", vendor, desiredVersion, verifiedVersions)

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	if dus.Status.State != v1alpha1.UpgradeStateUnverifiedVersion {
		t.Errorf("verifiedVersions 외 버전: UnverifiedVersion 상태 기대, got %q", dus.Status.State)
	}
}

// TestVerifiedVersions_Pass 는 DIP.spec.verifiedVersions 에 포함된 버전이
// desiredVersion 으로 지정된 경우 DUS 가 UpgradeRequired 로 정상 전이함을 검증한다.
func TestVerifiedVersions_Pass(t *testing.T) {
	const (
		nodeName       = "worker-vv-pass"
		vendor         = "nvidia"
		currentVersion = "535.288.01"
		desiredVersion = "580.142" // 화이트리스트에 있는 버전
		dusName        = "worker-vv-pass-nvidia"
	)
	verifiedVersions := []string{"535.288.01", "580.126.09", "580.142", "595.58.03"}

	dus := makeIdleDUSWithCurrentVersion(dusName, nodeName, vendor, currentVersion)
	dip := makeDIPWithVerifiedVersions("nvidia-generic-pass", vendor, desiredVersion, verifiedVersions)

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("verifiedVersions 내 버전: UpgradeRequired 상태 기대, got %q", dus.Status.State)
	}
}

// TestVerifiedVersions_Empty_Skip 는 DIP.spec.verifiedVersions 가 비어있을 때
// 버전 검증을 skip 하고 기존 동작(UpgradeRequired 전이)을 유지함을 검증한다 (backward compat).
func TestVerifiedVersions_Empty_Skip(t *testing.T) {
	const (
		nodeName       = "worker-vv-empty"
		vendor         = "furiosa"
		currentVersion = "1.7.8"
		desiredVersion = "1.9.8-3"
		dusName        = "worker-vv-empty-furiosa"
	)

	dus := makeIdleDUSWithCurrentVersion(dusName, nodeName, vendor, currentVersion)
	// verifiedVersions 없음 → 검증 skip
	dip := makeDIPWithVerifiedVersions("furiosa-warboy-empty", vendor, desiredVersion, nil)

	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()

	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패: %v", err)
	}

	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Errorf("verifiedVersions 비어있으면 UpgradeRequired 기대 (backward compat), got %q", dus.Status.State)
	}
}

// makeRollbackDIP 는 maxRollbackAttempts 가 명시된 DIP 를 반환한다.
// MaxRollbackAttempts 초과 시 Failed 전이 검증용.
//
//nolint:unparam // name·vendor 는 오늘 모든 호출에서 같다 — 롤백 시험이 공유하는 nvidia 픽스처 값이라
func makeRollbackDIP(name, vendor string, maxRollback int32) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Driver: v1alpha1.DriverSpec{Version: "580.142"},
			UpgradePolicy: &v1alpha1.UpgradePolicy{
				MaxRollbackAttempts: maxRollback,
			},
		},
	}
}

// makeRollbackDUS 는 Rollback 상태에서 RollbackAttempts 가 max 와 동일한 DUS 를 반환한다.
// 다음 reconcile 1 회 호출 시 attempts 가 max+1 로 증가 → Failed 전이.
//
//nolint:unparam // name·nodeName·vendor 는 오늘 모든 호출에서 같다 — 롤백 시험이 공유하는 nvidia 픽스처 값이라
func makeRollbackDUS(name, nodeName, vendor string, attempts int32) *v1alpha1.DriverUpgradeState {
	return &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:            v1alpha1.UpgradeStateRollback,
			CurrentVersion:   "535.288",
			DesiredVersion:   "580.142",
			PreviousVersion:  "535.288",
			RollbackAttempts: attempts,
		},
	}
}

// TestRollbackExhaustion_TransitionsToFailed 는 RollbackAttempts 가 maxRollbackAttempts 에
// 도달한 상태에서 단 1 회의 reconcile 호출만으로 Failed 터미널 상태로 전이됨을 검증한다 —
// 무한 롤백 루프를 막는 차단선이다.
func TestRollbackExhaustion_TransitionsToFailed(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		dusName  = "k8s-worker1-nvidia"
	)

	// max=3 이고 attempts=3 (이미 한도까지 시도). 다음 호출 → 4 → Failed.
	dus := makeRollbackDUS(dusName, nodeName, vendor, 3)
	dip := makeRollbackDIP("nvidia-generic", vendor, 3)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}

	sm := newUpgradeSMWithRecorder(dus, dip, node)
	ctx := context.Background()

	// 1 회 호출 → Failed 전이가 발생해야 한다.
	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatalf("TransitionState 실패 (1st call): %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateFailed {
		t.Fatalf("rollback exhaustion 후 state mismatch: got %q, want %q",
			dus.Status.State, v1alpha1.UpgradeStateFailed)
	}
	// attempts 는 한 번 증가했어야 함 (3 → 4).
	if dus.Status.RollbackAttempts != 4 {
		t.Errorf("RollbackAttempts mismatch: got %d, want 4", dus.Status.RollbackAttempts)
	}
}

// TestFailedState_IsTerminal_NoFurtherTransition 은 Failed 상태에서 추가 reconcile 이
// 어떠한 transition 도 트리거하지 않음을 검증한다 (idempotent stop).
func TestFailedState_IsTerminal_NoFurtherTransition(t *testing.T) {
	const (
		nodeName = "k8s-worker1"
		vendor   = "nvidia"
		dusName  = "k8s-worker1-nvidia"
	)

	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: dusName},
		Spec: v1alpha1.DriverUpgradeStateSpec{
			NodeName: nodeName,
			Vendor:   vendor,
		},
		Status: v1alpha1.DriverUpgradeStateStatus{
			State:            v1alpha1.UpgradeStateFailed,
			CurrentVersion:   "535.288",
			DesiredVersion:   "580.142",
			RollbackAttempts: 4,
			Message:          "롤백 3회 초과: 수동 조치 필요",
		},
	}
	dip := makeRollbackDIP("nvidia-generic", vendor, 3)
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}

	sm := newUpgradeSMWithRecorder(dus, dip, node)
	ctx := context.Background()

	// Failed 상태에서 reconcile 을 여러 번 호출해도 state 가 변하지 않아야 한다.
	for i := 0; i < 3; i++ {
		requeue, requeueAfter, err := sm.TransitionState(ctx, dus, dip)
		if err != nil {
			t.Fatalf("TransitionState 실패 (call #%d): %v", i+1, err)
		}
		if requeue {
			t.Errorf("Failed 상태에서 requeue=true 반환 (call #%d): 자동 재시도 금지 위반", i+1)
		}
		if requeueAfter != 0 {
			t.Errorf("Failed 상태에서 requeueAfter=%v 반환 (call #%d): 자동 재시도 금지 위반",
				requeueAfter, i+1)
		}
		if dus.Status.State != v1alpha1.UpgradeStateFailed {
			t.Fatalf("Failed 상태가 변경됨 (call #%d): got %q, want %q",
				i+1, dus.Status.State, v1alpha1.UpgradeStateFailed)
		}
		if dus.Status.RollbackAttempts != 4 {
			t.Errorf("Failed 상태에서 RollbackAttempts 가 변경됨 (call #%d): got %d, want 4",
				i+1, dus.Status.RollbackAttempts)
		}
	}
}

// podWithResource 는 지정한 extended resource 를 limits 에 요청하는 Pod 를 만든다.
func podWithResource(resourceName string) *corev1.Pod {
	return &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceName(resourceName): qty(1)},
		},
	}}}}
}

// TestPodUsesDevice_RecognizesEveryVendorResource 는 podUsesDevice(hasDeviceWorkloads 와
// evictDevicePods 가 함께 쓰는 판정)가 등록된 벤더 넷의 가속기 리소스를 모두 인식하는지 본다.
// 하드코딩 목록에서 벤더 하나라도 빠지면, 그 벤더의 파드가 드라이버 업그레이드 drain 에서 장치
// 파드로 인식되지 못해 PDB 를 존중하는 eviction 대상에서 빠진다.
func TestPodUsesDevice_RecognizesEveryVendorResource(t *testing.T) {
	cases := []struct {
		name     string
		resource string
		want     bool
	}{
		{"nvidia", "nvidia.com/gpu", true},
		{"furiosa rngd", "furiosa.ai/rngd", true},
		{"furiosa warboy", "beta.furiosa.ai/npu", true},
		{"tenstorrent", "tenstorrent.com/blackhole", true},
		{"rebellions", "rebellions.ai/ATOM", true},
		{"non-accelerator", "cpu", false},
	}
	for _, c := range cases {
		if got := podUsesDevice(podWithResource(c.resource)); got != c.want {
			t.Errorf("%s: podUsesDevice(%q) = %v, want %v", c.name, c.resource, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────
// 소비된 rollback 요청 재소비 차단 테스트
// ─────────────────────────────────────────────

// TestRollbackRequest_StaleAnnotationNotReconsumed 는 이미 소비한 롤백 요청 어노테이션이
// (제거 patch 실패 등으로) 남아 있을 때, 같은 값을 다시 Rollback 으로 받아들이지 않고
// 어노테이션 정리만 재시도함을 검증한다.
func TestRollbackRequest_StaleAnnotationNotReconsumed(t *testing.T) {
	dus := makeIdleDUSWithCurrentVersion("w1-tenstorrent", "w1", "tenstorrent", "580.126.09")
	dus.Status.PreviousVersion, dus.Status.PreviousImage = "580.100.00", "reg/tenstorrent-driver-ds:v3" // 롤백 가능한 형상
	dus.Annotations = map[string]string{v1alpha1.RollbackRequestedAnnotation: "2026-10-08T01:02:03Z"}
	dus.Status.ConsumedRollbackRequest = "2026-10-08T01:02:03Z" // 이미 한 번 소비됨
	dip := makeIdleDIP("tenstorrent-generic", "tenstorrent", "580.126.09")
	sm := newUpgradeSMWithRecorder(dus, dip)
	ctx := context.Background()
	if _, _, err := sm.TransitionState(ctx, dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateRollback {
		t.Fatal("소비된 요청으로 다시 Rollback 에 들어갔다")
	}
	var got v1alpha1.DriverUpgradeState
	if err := sm.Get(ctx, client.ObjectKeyFromObject(dus), &got); err != nil {
		t.Fatal(err)
	}
	if _, still := got.Annotations[v1alpha1.RollbackRequestedAnnotation]; still {
		t.Fatal("stale 어노테이션이 정리되지 않았다")
	}
}

// TestRollbackRequest_RecordsConsumedValue 는 새 롤백 요청이 받아들여질 때 요청 값이
// status.consumedRollbackRequest 에 기록됨을 검증한다(다음 reconcile 부터 재소비 차단 근거).
func TestRollbackRequest_RecordsConsumedValue(t *testing.T) {
	dus := makeIdleDUSWithCurrentVersion("w1-nvidia", "w1", "nvidia", "580.126.09")
	dus.Status.PreviousVersion, dus.Status.PreviousImage = "580.100.00", "reg/nvidia-driver-ds:v3"
	dus.Annotations = map[string]string{v1alpha1.RollbackRequestedAnnotation: "2026-10-08T02:00:00Z"}
	dip := makeIdleDIP("nvidia-generic", "nvidia", "580.126.09")
	sm := newUpgradeSMWithRecorder(dus, dip)
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRollback || dus.Status.ConsumedRollbackRequest != "2026-10-08T02:00:00Z" {
		t.Fatalf("state=%s consumedRollbackRequest=%q", dus.Status.State, dus.Status.ConsumedRollbackRequest)
	}
}

// TestValidatorsFor_WorkloadOnlyWhenEnabled 는 validatorsFor 가 기본 체인(DriverModule·
// DevicePlugin) 둘만 반환하고, 정책이 workloadProbe 를 켜면 Workload 를 끝에 더하는 것을
// 검증한다(정책 opt-in).
func TestValidatorsFor_WorkloadOnlyWhenEnabled(t *testing.T) {
	p := &v1alpha1.DriverInstallPolicy{}
	if n := len(validatorsFor(p)); n != 2 {
		t.Fatalf("기본 체인은 2개: %d", n)
	}
	// WorkloadProbe 가 설정돼 있어도 Enabled:false 면 체인에 들지 않는다 — nil 가드만으론
	// 통과하는 변이를 잡기 위한 케이스(Enabled 가드 자체를 고정).
	p.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{Enabled: false, Image: "img", ResourceName: "nvidia.com/gpu"}
	if n := len(validatorsFor(p)); n != 2 {
		t.Fatalf("WorkloadProbe 가 있어도 Enabled:false 면 2개: %d", n)
	}
	p.Spec.WorkloadProbe.Enabled = true
	if n := len(validatorsFor(p)); n != 3 {
		t.Fatalf("켜면 3개: %d", n)
	}
}

// ─────────────────────────────────────────────
// UnverifiedVersion 자동 복귀 테스트
// ─────────────────────────────────────────────

// TestUnverifiedVersion_ReturnsToIdleWhenDesiredBecomesVerified 는 DUS 가
// UnverifiedVersion 터미널 상태인 동안 DIP.spec.verifiedVersions 가 정책 버전을
// 다시 포함하도록 바뀌면(화이트리스트 추가·버전 되돌림) 삭제·재생성 없이 Idle 로
// 복귀함을 검증한다.
func TestUnverifiedVersion_ReturnsToIdleWhenDesiredBecomesVerified(t *testing.T) {
	dus := makeIdleDUSWithCurrentVersion("w3-tenstorrent", "w3", "tenstorrent", "2.11.0")
	dus.Status.State, dus.Status.DesiredVersion = v1alpha1.UpgradeStateUnverifiedVersion, "2.12.0"
	dip := makeDIPWithVerifiedVersions("tt-generic", "tenstorrent", "2.11.0", []string{"2.8.0", "2.11.0"}) // 정책이 되돌아감
	sm := newUpgradeSMWithRecorder(dus, dip)
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("정책이 검증된 버전으로 돌아왔는데 터미널에 남아 있다: %s", dus.Status.State)
	}
}

// TestUnverifiedVersion_StaysWhileDesiredUnverified 는 정책 버전이 여전히
// verifiedVersions 밖이면 터미널 상태를 유지하고 60초 뒤 재확인함을 검증한다.
func TestUnverifiedVersion_StaysWhileDesiredUnverified(t *testing.T) {
	dus := makeIdleDUSWithCurrentVersion("w3-tenstorrent", "w3", "tenstorrent", "2.11.0")
	dus.Status.State, dus.Status.DesiredVersion = v1alpha1.UpgradeStateUnverifiedVersion, "2.12.0"
	dip := makeDIPWithVerifiedVersions("tt-generic", "tenstorrent", "2.12.0", []string{"2.8.0", "2.11.0"})
	sm := newUpgradeSMWithRecorder(dus, dip)
	requeue, after, err := sm.TransitionState(context.Background(), dus, dip)
	if err != nil || dus.Status.State != v1alpha1.UpgradeStateUnverifiedVersion || !requeue || after != 60*time.Second {
		t.Fatalf("미검증 버전이 그대로면 터미널 유지·60초 requeue: state=%s requeue=%v after=%v err=%v", dus.Status.State, requeue, after, err)
	}
}
