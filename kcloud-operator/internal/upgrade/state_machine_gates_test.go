// state_machine_gates_test.go: idle cooldown defer + kernel allowlist preflight 게이트 단위 테스트
// 상세: 실제 트리거(cordon/drain/driver 변경) 없이 순수 state-machine 분기를 fake client 로
// 검증한다(IdleCooldown·kernel allowlist·Idle 게이트 순서·WorkloadProbe 예산 경고).
// 생성일: 2026-07-21 | 수정일: 2026-10-13
package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// drainEvents 는 FakeRecorder 채널에 쌓인 이벤트 문자열을 전부 비워 반환한다("Normal Reason msg" 형식).
func drainEvents(sm *UpgradeStateMachine) []string {
	rec := sm.Recorder.(*record.FakeRecorder)
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func hasEvent(events []string, reason string) bool {
	for _, e := range events {
		if strings.Contains(e, reason) {
			return true
		}
	}
	return false
}

// makeDaemonsetDIP 는 mode 미지정(daemonset) DIP 를 반환한다 — job self-heal 분기를 우회해
// handleIdle 의 버전 불일치+cooldown 경로를 직접 태우기 위함.
func makeDaemonsetDIP(version string, autoUpgrade bool) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: jVendor},
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: jVendor, Model: jModel,
			Driver:        v1alpha1.DriverSpec{Version: version}, // Mode="" = daemonset
			UpgradePolicy: &v1alpha1.UpgradePolicy{AutoUpgrade: autoUpgrade},
		},
	}
}

// ── 항목1: Idle cooldown defer ──────────────────────────────────────
// Idle 진입(LastTransitionTime=now) 직후 버전 불일치 트리거는 default 10s cooldown 미충족으로
// 연기된다: IdleCooldownDeferred 이벤트 + 잔여시간 requeue + 상태 불변(Idle 유지, cordon 시작 안 함).
func TestHandleIdle_CooldownNotElapsed_Deferred(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verOld, verOld) // LastTransitionTime=now
	dip := makeDaemonsetDIP(verNew, true)                        // desired=verNew ≠ current=verOld
	sm := newUpgradeSMWithRecorder(dus, dip)

	_, requeue, err := sm.handleIdle(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleIdle 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Errorf("state=%q, want Idle (cooldown 중 전이 금지)", dus.Status.State)
	}
	if requeue <= 0 || requeue > 10*time.Second {
		t.Errorf("requeue=%v, want (0, 10s] 잔여 cooldown", requeue)
	}
	if !hasEvent(drainEvents(sm), "IdleCooldownDeferred") {
		t.Error("IdleCooldownDeferred 이벤트 미발행")
	}
}

// cooldown 비활성화(IdleCooldownSeconds=0)면 즉시 트리거되어 Required(업그레이드 슬롯 확보)로 전이.
func TestHandleIdle_CooldownDisabled_TriggersImmediately(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verOld, verOld)
	dip := makeDaemonsetDIP(verNew, true)
	zero := int32(0)
	dip.Spec.UpgradePolicy.IdleCooldownSeconds = &zero
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleIdle 실패: %v", err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateIdle {
		t.Error("cooldown=0 인데 Idle 유지됨 (즉시 트리거 실패)")
	}
}

// ── 항목2a: Kernel allowlist preflight 게이트 ───────────────────────
// PreFlight 에서 노드 커널이 allowlist 와 불일치하면 PreFlightFailed 이벤트 + Rollback 전이.
// cordon(Cordoning) 이전 단계이므로 노드는 cordon 되지 않는다.
func TestHandlePreFlight_KernelNotInAllowlist_Rollback(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, verOld, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Spec.KernelAllowlist = []string{"6.8.*"} // glob — 5.15 불포함
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRollback {
		t.Errorf("state=%q, want Rollback (커널 불일치)", dus.Status.State)
	}
	if !hasEvent(drainEvents(sm), "PreFlightFailed") {
		t.Error("PreFlightFailed 이벤트 미발행")
	}
}

// 커널이 allowlist 와 일치하면 PreFlight 통과 → Cordoning 전이.
func TestHandlePreFlight_KernelInAllowlist_Cordoning(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status: corev1.NodeStatus{
			NodeInfo:   corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, verOld, verNew)
	dip := makeJobDIP(verNew, imgNew, true, true)
	dip.Spec.KernelAllowlist = []string{"5.15.*"} // glob — 일치
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateCordoning {
		t.Errorf("state=%q, want Cordoning (커널 일치 통과)", dus.Status.State)
	}
}

// ── 항목3: Idle 게이트 순서(verifiedVersions vs autoUpgrade) ─────────
// autoUpgrade 가 꺼져 있어도 허용 목록 밖 버전은 UnverifiedVersion 으로 드러난다 — autoUpgrade
// 게이트가 먼저 오면 UpgradeAvailable 경고만 내고 Idle 에 머물러 진짜 이유(목록 밖)가 가려진다.
func TestHandleIdle_AutoUpgradeOffAndUnverified_Transitions(t *testing.T) {
	dip := makeDaemonsetDIP("580.178.04", false)
	dip.Spec.VerifiedVersions = []string{"580.159.03"}
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, "580.159.03", "580.178.04")
	sm := newUpgradeSMWithRecorder(dus, dip)

	_, _, err := sm.handleIdle(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleIdle: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUnverifiedVersion {
		t.Fatalf("state=%q (UnverifiedVersion 기대) — autoUpgrade 게이트가 허용 목록 검사를 가린다", dus.Status.State)
	}
	if !hasEvent(drainEvents(sm), "UnverifiedVersion") {
		t.Fatal("UnverifiedVersion 이벤트가 없다")
	}
}

// autoUpgrade 가 꺼져 있고 버전이 허용 목록 안이면 종전대로 Idle 에 머문다.
func TestHandleIdle_AutoUpgradeOffAndVerified_StaysIdle(t *testing.T) {
	dip := makeDaemonsetDIP("580.159.03", false)
	dip.Spec.VerifiedVersions = []string{"580.159.03", "580.178.04"}
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip)

	_, _, err := sm.handleIdle(context.Background(), dus, dip)
	if err != nil {
		t.Fatalf("handleIdle: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q (Idle 기대) — 허용 목록 안 버전인데 Idle 에 머물지 않는다", dus.Status.State)
	}
	if !hasEvent(drainEvents(sm), "UpgradeAvailable") {
		t.Fatal("UpgradeAvailable 경고가 없다")
	}
}

// 허용 목록 검사는 IdleCooldown 가드보다 앞이다 — 쿨다운 창 안이어도 목록 밖 버전은 바로
// UnverifiedVersion 으로 간다. 이 전이는 Job 도 cordon 도 만들지 않아 쿨다운이 막으려던
// 되풀이와 무관하다.
func TestHandleIdle_UnverifiedTransitionsInsideCooldown(t *testing.T) {
	dip := makeDaemonsetDIP("580.178.04", true)
	dip.Spec.VerifiedVersions = []string{"580.159.03"}
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, "580.159.03", "580.178.04")
	dus.Status.LastTransitionTime = metav1.Now() // 쿨다운 창 안
	sm := newUpgradeSMWithRecorder(dus, dip)

	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleIdle: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateUnverifiedVersion {
		t.Fatalf("state=%q (UnverifiedVersion 기대) — 쿨다운이 허용 목록 거부를 가린다", dus.Status.State)
	}
}

// ── 항목4: WorkloadProbe 제한시간 vs 검증 예산 PreFlight 경고 ──────────
// probe 제한시간이 검증 예산보다 길면 PreFlight 가 경고를 낸다 — 그 조합에서는 바깥 예산이
// 먼저 만료해 probe 결과가 쓰이지 않고, 파드는 kubelet 제한시간까지 남는다.
func TestHandlePreFlight_ProbeTimeoutExceedsBudget_Warns(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy.ValidationTimeout = "1m"
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 300,
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if !hasEvent(drainEvents(sm), "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("경고 이벤트가 없다 — 설정 모순이 보이지 않는다")
	}
}

// 합당한 조합(probe 제한시간 < 검증 예산)에서는 경고가 없다.
func TestHandlePreFlight_ProbeTimeoutWithinBudget_NoWarn(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy.ValidationTimeout = "15m"
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 120,
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if hasEvent(drainEvents(sm), "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("합당한 조합인데 경고가 떴다")
	}
}

// timeoutSeconds 를 비우면 기본 120s 가 쓰인다 — 그 값이 검증 예산(1m)보다 길면 경고해야 하고,
// 그 값이 정책에 적힌 것처럼 보이면 안 된다.
func TestHandlePreFlight_ProbeTimeoutDefaultExceedsBudget(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy.ValidationTimeout = "1m"
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", // TimeoutSeconds=0 → 120s
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	events := drainEvents(sm)
	if !hasEvent(events, "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("기본 probe 제한시간(120s) > 예산(1m) 인데 경고가 없다")
	}
	if !hasEvent(events, "timeoutSeconds 미설정 기본값") {
		t.Errorf("경고가 기본값을 정책값처럼 보인다 — 출처 표기가 없다: %v", events)
	}
}

// validationTimeout 이 비면 경고는 parseDuration 기본값으로 비교하되, 그 값을 정책이 쓴 것처럼
// 보이면 안 된다 — 운영자가 DIP 를 열면 그 필드가 없어 메시지를 대조할 수 없다.
func TestHandlePreFlight_ProbeTimeoutExceedsDefaultBudget_MarksDefault(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy.ValidationTimeout = "" // 미설정 — 기본값 15분이 적용된다
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 1200,
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	events := drainEvents(sm)
	if !hasEvent(events, "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("기본 예산을 넘겼는데 경고가 없다")
	}
	if !hasEvent(events, "미설정") {
		t.Errorf("경고가 기본값을 정책값처럼 보인다 — 출처 표기가 없다: %v", events)
	}
}

// upgradePolicy 자체가 없으면 기본 예산(15분)과 비교한다 — 그 분기가 패닉 없이 돌아야 한다.
func TestHandlePreFlight_NoUpgradePolicy_UsesDefaultBudget(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy = nil
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 1200,
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if !hasEvent(drainEvents(sm), "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("20분 probe 가 기본 15분 예산을 넘는데 경고가 없다")
	}
}

// 두 시간이 같으면 경고하지 않는다 — 가드가 초과만 본다.
func TestHandlePreFlight_ProbeTimeoutEqualsBudget_NoWarn(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: jNode},
		Status:     corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "5.15.0-181-generic"}},
	}
	dip := makeDaemonsetDIP("580.159.03", true)
	dip.Spec.UpgradePolicy.ValidationTimeout = "2m"
	dip.Spec.WorkloadProbe = &v1alpha1.WorkloadProbeSpec{
		Enabled: true, Image: "probe:v1", ResourceName: "nvidia.com/gpu", TimeoutSeconds: 120,
	}
	dus := makeJobDUS(v1alpha1.UpgradeStatePreFlight, "535.288.01", "580.159.03")
	sm := newUpgradeSMWithRecorder(dus, dip, node)

	if _, _, err := sm.handlePreFlight(context.Background(), dus, dip); err != nil {
		t.Fatalf("handlePreFlight: %v", err)
	}
	if hasEvent(drainEvents(sm), "WorkloadProbeTimeoutExceedsBudget") {
		t.Fatal("같은 값인데 경고가 떴다")
	}
}
