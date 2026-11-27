// ============================================================
// state_machine.go: Driver Upgrade 상태 머신
// 상세: 노드별 드라이버 업그레이드 상태 전이를 관리한다. Idle → UpgradeRequired → PreFlight →
//       Cordoning → Draining → Upgrading → Validating → Uncordoning → Idle(실패 시 Rollback).
// 생성일: 2026-04-13 | 수정일: 2026-10-28
// ============================================================

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/draclaim"
	"kcloud-operator/internal/driveradapter"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/validator"
)

const driverUpgradingLabelKey = "npu.ai/driver-upgrading"

// driverUpgradingBlockingLabelKey 는 detector / device-plugin 등 driver pod 의 rmmod 와
// 충돌할 수 있는 워크로드의 spawn 을 차단하는 좁은 lifecycle 의 라벨이다.
// driver-upgrading 라벨이 사이클 전체 (Cordoning ~ Uncordoning) 에 걸친 추적용이라면,
// 이 라벨은 Cordoning 진입 시 추가되고 Validating 진입 시점에 제거된다. Validating 부터
// detector 가 다시 spawn 가능 → NDR 갱신 → DriverModuleValidator 통과 → Validating 데드락 차단.
const driverUpgradingBlockingLabelKey = "npu.ai/driver-upgrading-blocking"

// DriverUpgradingLabelKey 는 외부 패키지(controller 의 stuck-label sweep / defer cleanup)가
// 동일한 라벨을 가리키도록 노출하는 상수다. 절대 다른 키와 혼용 금지.
const DriverUpgradingLabelKey = driverUpgradingLabelKey

// DriverUpgradingBlockingLabelKey 는 좁은 lifecycle 의 detector 차단 라벨을 외부 패키지에
// 노출한다. detector DS nodeAffinity / sweep / defer cleanup 가 이 키를 참조한다.
const DriverUpgradingBlockingLabelKey = driverUpgradingBlockingLabelKey

// quiesceOnDriverUpgradeLabelKey 는 driver upgrade cycle 동안 자동으로 scale=0 으로 quiesce
// 하고 cycle 종료 시 원래 replicas 로 복구할 Deployment 를 식별하는 opt-in 라벨 키이다.
// gpu-stress 같은 NodeAffinity reject 로 Failed Pod 가 누적되는 테스트/내부 워크로드만 라벨을
// 붙이고 production critical workload 는 PDB + drain 표준 흐름을 따른다.
const quiesceOnDriverUpgradeLabelKey = "npu.ai/quiesce-on-driver-upgrade"

// QuiesceOnDriverUpgradeLabelKey 는 외부 패키지(test/manifest)가 동일한 라벨을 가리키도록
// 노출하는 상수다.
const QuiesceOnDriverUpgradeLabelKey = quiesceOnDriverUpgradeLabelKey

// QuiesceReplicasBackupAnnotation 은 quiesce 시 Deployment 의 원래 replicas 값을 DUS.Status.
// QuiescedDeployments 백업과 함께 이중 기록하는 annotation 키다. operator restart 로 status 를
// 잃어도 이 값으로 fallback 복구한다. 값은 base-10 정수 문자열 (예: "3").
const QuiesceReplicasBackupAnnotation = "npu.ai/replicas-backup"

// cordonOwnerAnnotationKey 는 이 상태머신이 직접 노드를 unschedulable 로 뒤집었을 때만 노드에
// 남기는 소유권 표식이며 값은 그 cordon 을 건 DUS 이름이다.
// 왜 라벨이 아니라 별도 annotation 인가: driver-upgrading 라벨은 Failed 전이 시점에
// clearUpgradingLabel 이 지운다(device-plugin 재스케줄 목적). 그래서 Failed 에 도달한 뒤에는
// 라벨로 "이 cordon 이 우리 것인가" 를 물을 수 없다. 표식의 수명이 cordon 의 수명과 같아야 한다.
const cordonOwnerAnnotationKey = "npu.ai/cordoned-by-driver-upgrade"

// failedCordonRecheckInterval 은 Failed 에서 cordon 해제를 보류했을 때(벤더 광고가 아직 0 이
// 아님) 광고를 다시 볼 주기다. 보류에는 재진입이 없으면 의미가 없다 — 광고는 device-plugin 이
// 뒤늦게 재시작하며 떨어질 수 있다.
const failedCordonRecheckInterval = 60 * time.Second

// hostnameLabelKey 는 deploymentTargetsNode 가 nodeSelector / nodeAffinity 매칭에 사용하는
// Kubernetes 표준 노드 hostname 라벨 키다.
const hostnameLabelKey = "kubernetes.io/hostname"

// labelValueTrue 는 boolean 라벨/어노테이션 값으로 사용하는 "true" 문자열이다.
const labelValueTrue = "true"

// defaultIdleCooldown 은 UpgradePolicy.IdleCooldownSeconds 가 nil 일 때 적용되는 기본값이다.
// Idle 진입 후 이 시간 동안은 신규 upgrade trigger 를 거부 (requeue) 하여
// rolling-update 테스트에서 연속 trigger 가 mid-state 로 오인되는 것을 막는다.
// IdleCooldownSeconds 를 0 또는 음수로 두면 cooldown 비활성화.
const defaultIdleCooldown = 10 * time.Second

// UpgradeStateMachine은 노드별 드라이버 업그레이드 상태 전이를 담당합니다.
type UpgradeStateMachine struct {
	client.Client
	// APIReader 는 캐시 없는 읽기 경로다. 설치 Secret 확인(missingInstallSecret)이 쓴다 — 매니저
	// 캐시(Client)로 Secret 을 읽으면 클러스터 전체 Secret informer 가 뜬다. nil 이면 확인을 건너뛴다.
	APIReader client.Reader
	Recorder  record.EventRecorder
}

// TransitionState는 현재 DriverUpgradeState를 읽어 다음 상태로 전이합니다.
// 반환값: requeue 여부, requeueAfter 시간, 에러
func (m *UpgradeStateMachine) TransitionState(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (requeue bool, requeueAfter time.Duration, err error) {
	logger := logf.FromContext(ctx).WithValues(
		"node", state.Spec.NodeName,
		"vendor", state.Spec.Vendor,
		"state", state.Status.State,
	)
	logger.Info("TransitionState 호출")

	// reconcile 진입 시 broken PreviousImage 를 안전하게 정리한다.
	// operator restart / 이전 버전 배포로 인해 plain tag (예: ":580.142") 가
	// PreviousImage 에 잔류하는 경우 rollback 시 broken Pod 으로 회귀하는 것을 방지.
	if state.Status.PreviousImage != "" && !isVerifiedBuildTag(state.Status.PreviousImage) {
		logger.Info("Broken PreviousImage cleared for safety", "previousImage", state.Status.PreviousImage)
		state.Status.PreviousImage = ""
		if err := m.Status().Update(ctx, state); err != nil {
			return false, 0, err
		}
	}

	// 현재 상태를 Prometheus 메트릭에 기록
	metrics.SetUpgradeState(state.Spec.NodeName, state.Spec.Vendor, state.Status.State)

	// 관리자 롤백 요청(어노테이션)은 Idle·Failed 에서만 받는다. 전이 중에는 자동 롤백 경로와 겹친다.
	// 어노테이션은 상태 전이를 영속한 뒤에 지운다. 먼저 지우면 영속 실패 시 재시도 신호가 사라진다.
	// 그래서 이 분기는 상태를 스스로 영속하고, 호출자의 Status().Update 는 멱등 호출이 된다.
	if requestedAt, ok := state.Annotations[v1alpha1.RollbackRequestedAnnotation]; ok {
		if requestedAt == state.Status.ConsumedRollbackRequest {
			// 이미 소비한 요청이 제거 실패로 남은 것이다(Idle·Failed 재진입 이후까지 미뤄져도) —
			// 다시 받아들이지 않고 어노테이션 정리만 재시도한다.
			if err := m.clearRollbackRequest(ctx, state); err != nil {
				logger.Error(err, "소비된 롤백 요청 어노테이션 제거 재시도 실패")
			}
		} else {
			switch {
			case v1alpha1.RollbackRequestAccepted(state.Status.State):
				state.Status.ConsumedRollbackRequest = requestedAt
				requeue, requeueAfter, _ := m.transitionTo(state, v1alpha1.UpgradeStateRollback,
					"관리자 요청 롤백("+requestedAt+")", 0)
				if err := m.Status().Update(ctx, state); err != nil {
					return false, 0, err
				}
				m.Recorder.Eventf(state, corev1.EventTypeNormal, "RollbackRequested",
					"관리자 요청(%s)으로 Rollback 전이 (node=%s)", requestedAt, state.Spec.NodeName)
				if err := m.clearRollbackRequest(ctx, state); err != nil {
					// 상태 전이는 이미 영속되어 있다 — 아래 case(state==Rollback)가 다음
					// reconcile 에서 어노테이션 제거만 재시도한다(재전이는 하지 않는다).
					logger.Error(err, "롤백 요청 어노테이션 제거 실패 — 다음 reconcile 에서 재시도")
				}
				return requeue, requeueAfter, nil
			case state.Status.State == v1alpha1.UpgradeStateRollback:
				// 이 요청으로 이미 Rollback 에 들어간 뒤, 어노테이션 제거만 실패해 남은 경우다.
				// 위 case 조건이 이미 거짓이라 재전이는 없다 — 제거만 재시도한다. 그러지 않으면
				// 상태가 나중에 Idle·Failed 로 돌아왔을 때 같은 요청이 새 요청으로 오인돼
				// 다시 전이한다.
				if err := m.clearRollbackRequest(ctx, state); err != nil {
					logger.Error(err, "롤백 요청 어노테이션 제거 재시도 실패")
				}
			default:
				logger.Info("롤백 요청 보류 — 전이 중", "requestedAt", requestedAt)
			}
		}
	}

	switch state.Status.State {
	case "", v1alpha1.UpgradeStateIdle:
		return m.handleIdle(ctx, state, policy)
	case v1alpha1.UpgradeStateRequired:
		return m.handleUpgradeRequired(ctx, state, policy)
	case v1alpha1.UpgradeStatePreFlight:
		return m.handlePreFlight(ctx, state, policy)
	case v1alpha1.UpgradeStateCordoning:
		return m.handleCordoning(ctx, state)
	case v1alpha1.UpgradeStateDraining:
		return m.handleDraining(ctx, state, policy)
	case v1alpha1.UpgradeStateUpgrading:
		return m.handleUpgrading(ctx, state, policy)
	case v1alpha1.UpgradeStateValidating:
		// install 성공 후 모듈 교체(계열 전환·같은 계열 patch 교체)로 재부팅이 필요하면
		// (NDR.needsReboot), validator 체인 앞에서 RebootRequired 로 분기한다 — 재부팅 전엔
		// validator 가 구 버전을 보고 실패한다.
		// 마커가 없으면 이 분기를 타지 않는다. 마커가 있어도 NDR 반영은 detector 스캔주기(30s)만큼
		// 지연되므로, rebootMarkerIsFresh 로 "이번 install Job 완료보다 나중 관측인가"를 확인해
		// stale 마커로 인한 오판(MaxReboots 초과→Failed)을 막는다.
		if need, nerr := m.nodeNeedsReboot(ctx, state.Spec.NodeName, state.Spec.Vendor, state.Spec.Model); nerr == nil &&
			need && m.rebootMarkerIsFresh(ctx, state) {
			return m.transitionTo(state, v1alpha1.UpgradeStateRebootRequired,
				"커널 모듈 교체 위해 노드 재부팅 필요(needsReboot)", 0)
		}
		return m.handleValidating(ctx, state, policy)
	case v1alpha1.UpgradeStateRebootRequired:
		return m.handleRebootRequired(ctx, state, policy)
	case v1alpha1.UpgradeStateRebooting:
		return m.handleRebooting(ctx, state, policy)
	case v1alpha1.UpgradeStateUncordoning:
		return m.handleUncordoning(ctx, state)
	case v1alpha1.UpgradeStateRollback:
		return m.handleRollback(ctx, state, policy)
	case v1alpha1.UpgradeStateFailed:
		return m.handleFailed(ctx, state)
	case v1alpha1.UpgradeStateHostAhead:
		return m.handleHostAhead(ctx, state, policy)
	case v1alpha1.UpgradeStateUnverifiedVersion:
		return m.handleUnverifiedVersion(ctx, state, policy)
	default:
		logger.Info("알 수 없는 상태, Idle로 리셋", "state", state.Status.State)
		return m.transitionTo(state, v1alpha1.UpgradeStateIdle, "알 수 없는 상태 리셋", 60*time.Second)
	}
}

// ─────────────────────────────────────────────
// 상태 핸들러
// ─────────────────────────────────────────────

// handleUnverifiedVersion 은 UnverifiedVersion 종점이다. 터미널이지만 정책이 바뀌면 나간다 —
// desired 가 verifiedVersions 안으로 들어오거나(목록 추가·버전 되돌림) 목록이 비면 Idle 로
// 돌아가 정상 판정을 다시 받는다.
func (m *UpgradeStateMachine) handleUnverifiedVersion(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	loaded, err := m.loadedHostVersion(ctx, state)
	if err != nil {
		return false, 0, err
	}
	desired := EffectiveDesiredVersion(policy, loaded)
	if len(policy.Spec.VerifiedVersions) == 0 || containsString(policy.Spec.VerifiedVersions, desired) {
		return m.transitionTo(state, v1alpha1.UpgradeStateIdle,
			fmt.Sprintf("목표 버전 %s 가 verifiedVersions 안으로 들어와 Idle 복귀", desired), 0)
	}
	logf.FromContext(ctx).Info("UnverifiedVersion 터미널 상태 — DIP.spec.verifiedVersions 수정 필요 (수동 조치)",
		"node", state.Spec.NodeName, "desiredVersion", state.Status.DesiredVersion)
	return true, 60 * time.Second, nil
}

// handleHostAhead 는 HostAhead 종점이다. 호스트가 더는 정책보다 높지 않으면(정책 상향·
// allowDowngrade 켬·호스트 하향) Idle 로 돌아가 정상 판정을 다시 받는다.
func (m *UpgradeStateMachine) handleHostAhead(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	host, ahead, err := m.hostDriverAhead(ctx, state, policy)
	if err != nil {
		return false, 0, err
	}
	if !ahead {
		return m.transitionTo(state, v1alpha1.UpgradeStateIdle,
			"호스트 드라이버가 더는 정책보다 높지 않아 Idle 복귀", 0)
	}
	logf.FromContext(ctx).Info("HostAhead 종점 — 정책 버전을 올리거나 versionSource=Host 로 바꿀 것",
		"node", state.Spec.NodeName, "host", host, "policy", policy.Spec.Driver.Version)
	return true, 60 * time.Second, nil
}

// markRolledBack 은 이 사이클의 목표를 롤백한 버전으로 기록한다. 같은 목표를 이미 기록했으면
// (잔여 Job 정리 등으로 Rollback 에 다시 들어온 경우) 롤백을 시작할 때의 generation 을 지키고,
// 같은 사이클의 두 번째 롤백은 DesiredVersion 이 이미 이전 버전이라 덮지 않는다.
func markRolledBack(state *v1alpha1.DriverUpgradeState, policy *v1alpha1.DriverInstallPolicy) {
	if state.Status.DesiredVersion == "" || state.Status.DesiredVersion == state.Status.PreviousVersion ||
		state.Status.RolledBackVersion == state.Status.DesiredVersion {
		return
	}
	state.Status.RolledBackVersion = state.Status.DesiredVersion
	state.Status.RolledBackGeneration = policy.Generation
}

// releaseRollbackHold 는 보류가 더는 맞지 않으면 기록을 지운다. 목표나 정책 generation 이 바뀐
// 경우와, 로드된 호스트 버전(loaded)이 보류 목표와 같아진 경우(호스트에서 직접 설치 등)다.
// 두 경우가 겹치면 도달 문구를 남긴다.
func releaseRollbackHold(state *v1alpha1.DriverUpgradeState, policy *v1alpha1.DriverInstallPolicy, desired, loaded string) {
	held := state.Status.RolledBackVersion
	switch {
	case held == "":
		return
	case loaded == held:
		state.Status.Message = fmt.Sprintf("롤백 보류 해제(노드가 보류 목표 %s 에 도달)", held)
	case held != desired || state.Status.RolledBackGeneration != policy.Generation:
		state.Status.Message = fmt.Sprintf("롤백 보류 해제(목표 또는 정책 generation 변경): 이전 보류 목표 %s", held)
	default:
		return
	}
	state.Status.RolledBackVersion = ""
	state.Status.RolledBackGeneration = 0
}

// holdAfterRollback 은 같은 정책에서 이미 롤백한 목표면 true 를 반환해 사이클을 열지 않게 한다.
// 열면 같은 실패와 롤백이 cooldown 간격으로 반복된다. 정책 spec 을 바꾸거나 DUS 를 지우면 다시 시도한다.
// 경고 이벤트는 메시지가 바뀔 때 한 번만 낸다.
func (m *UpgradeStateMachine) holdAfterRollback(
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
	desiredVersion, currentVersion string,
) bool {
	if state.Status.RolledBackVersion == "" || state.Status.RolledBackVersion != desiredVersion ||
		state.Status.RolledBackGeneration != policy.Generation {
		return false
	}
	msg := fmt.Sprintf("목표 %s 는 롤백한 버전 — 정책 spec 변경 또는 DUS 삭제 전까지 재시도 보류(현재 %s)",
		desiredVersion, currentVersion)
	if state.Status.Message != msg {
		state.Status.Message = msg
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeHeldAfterRollback", "%s", msg)
	}
	return true
}

// canRestoreHeld 는 롤백 보류 기록이 있고 복원할 버전(currentVersion)을 알 때 true 다.
// 보류 기록은 있는데 currentVersion 이 비면 복원할 버전을 모르므로 호출부는 정책 목표를 설치한다.
func canRestoreHeld(state *v1alpha1.DriverUpgradeState, currentVersion string) bool {
	return state.Status.RolledBackVersion != "" && currentVersion != ""
}

// startHeldRestore 는 롤백 보류 중 드라이버가 내려간 노드의 복구 사이클을 연다. 실패한 보류 목표가
// 아니라 롤백이 복원한 버전(restored)을 다시 설치한다. 보류 기록과 previousVersion 은 남긴다 —
// 재설치가 실패하면 같은 버전으로 롤백하고, 끝나면 보류와 드리프트 기준이 그대로 이어진다.
// 허용 목록(verifiedVersions)은 보지 않는다. 노드가 쓰던 버전이라 새 목표가 아니다. 설치 Secret 이
// 없으면 openCycle 이 사이클을 열지 않고 Idle 에서 기다리게 한다.
func (m *UpgradeStateMachine) startHeldRestore(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy, restored string) (bool, time.Duration, error) {
	held := state.Status.RolledBackVersion
	return m.openCycle(ctx, state, policy, fmt.Sprintf("Job 모드 복구(롤백 보류 중): %s 재설치", restored), func() {
		state.Status.DesiredVersion = restored
		state.Status.PreviousVersion = restored
		state.Status.PreviousImage = ""
		state.Status.RollbackAttempts = 0
		resetRebootTracking(&state.Status) // 새 사이클: 재부팅 카운터·마커 초기화
		m.Recorder.Eventf(state, corev1.EventTypeNormal, "InstallRequired",
			"Job 모드 복구 필요(driverLoaded=false): 롤백 보류 중이라 보류 목표 %s 대신 복원 버전 %s 재설치", held, restored)
	})
}

// openCycle 은 Idle 에서 새 사이클을 연다 — handleIdle 이 UpgradeRequired 로 가는 길은 모두 여기를
// 지난다. 설치 Secret 이 없으면 prepare 를 부르지 않고 Idle 에 머문다(holdForInstallSecret). 있으면
// prepare 로 새 사이클의 입력을 정리하고 UpgradeRequired 로 전이한다.
func (m *UpgradeStateMachine) openCycle(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy, message string, prepare func()) (bool, time.Duration, error) {
	if held, err := m.holdForInstallSecret(ctx, state, policy); err != nil || held {
		// 새 DUS 는 생성 때 status 가 버려져 상태가 빈 값이다. 기다리는 동안 `kubectl get dus` 와 상태
		// 지표가 Idle 을 보이도록 한 번 맞춘다(목표 버전은 Idle 이 된 뒤 컨트롤러 동기화가 채운다).
		if held && state.Status.State == "" {
			_, _, _ = m.transitionTo(state, v1alpha1.UpgradeStateIdle, state.Status.Message, 0)
		}
		return held, InstallSecretRecheck, err
	}
	prepare()
	return m.transitionTo(state, v1alpha1.UpgradeStateRequired, message, 0)
}

// adoptLoadedVersion 은 새로 만든 DUS 의 currentVersion 이 비어 있고 노드에 드라이버가 로드돼 있으면
// (loaded) 그 버전을 현재 버전으로 기록한다. 빈 현재 버전 때문에 정상 노드에서 복구 사이클(cordon·drain)이
// 열리는 것을 막는 처리이고, 사이클을 열지 않는다는 보장은 아니다. 기록한 뒤에도 버전이 다르면 업그레이드
// 게이트를, 일부 장치가 로드되지 않았으면 복구 판정을 기존대로 거친다. 돌려주는 값은 호출 뒤의
// currentVersion 이다. 기록된 값이 있으면 덮어쓰지 않는다. Job 모드 블록에서만 호출한다(데몬셋·TrackOnly·
// HostAhead 판정 뒤).
func (m *UpgradeStateMachine) adoptLoadedVersion(state *v1alpha1.DriverUpgradeState, loaded string) string {
	if state.Status.CurrentVersion == "" && loaded != "" {
		state.Status.CurrentVersion = loaded
		m.Recorder.Eventf(state, corev1.EventTypeNormal, "CurrentVersionAdopted",
			"노드 %s 에 로드된 드라이버 %s 를 현재 버전으로 기록(새 DriverUpgradeState)", state.Spec.NodeName, loaded)
	}
	return state.Status.CurrentVersion
}

// handleIdle: 버전 불일치 감지 시 UpgradeRequired로 전이
func (m *UpgradeStateMachine) handleIdle(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	loaded, err := m.loadedHostVersion(ctx, state)
	if err != nil {
		return false, 0, err
	}
	desiredVersion := EffectiveDesiredVersion(policy, loaded)
	currentVersion := state.Status.CurrentVersion

	// Idle 에서도 선택 결과를 최신으로 유지한다. 호출부(driver_upgrade_controller.go 의
	// Status().Update)가 전이 여부와 무관하게 status 를 저장하므로 여기서 바꾼 값이 영속한다.
	if err := m.refreshResolvedImage(ctx, state, policy); err != nil {
		return false, 0, err
	}
	releaseRollbackHold(state, policy, desiredVersion, loaded)

	// TrackOnly 정책: 설치/업그레이드 경로를 절대 트리거하지 않는다(버전 관측 전용).
	// 아래 Job 모드 self-heal(driverLoaded=false) 트리거보다 먼저 차단하여, installer 미비
	// 벤더(예: Tenstorrent — 실제 설치는 DKMS 2차)의 self-heal 엣지에서도 install Job 이
	// 생성되지 않게 한다(DriverSpec.TrackOnly 계약: "install Job 을 생성하지 않습니다").
	// DUS 는 driver_upgrade_controller 의 ensureTrackOnlyState 가 Idle 로 동기화하며, 여기서는
	// state=="" 만 1회 Idle 로 정규화해 메트릭/`kubectl get dus` 가 Idle 을 보고하도록 한다.
	if policy.Spec.Driver.TrackOnly {
		if state.Status.State == "" {
			return m.transitionTo(state, v1alpha1.UpgradeStateIdle,
				"TrackOnly: 버전 관측 전용(설치 경로 없음)", 60*time.Second)
		}
		return true, 60 * time.Second, nil
	}

	// 호스트가 정책보다 높고 다운그레이드가 막혀 있으면 사이클을 열지 않는다. 열면 설치기는
	// 설치를 건너뛰고 검증은 정책 버전을 기다리다 실패해 cordon·롤백이 끝없이 반복된다.
	if host, ahead, err := m.hostDriverAhead(ctx, state, policy); err != nil {
		return false, 0, err
	} else if ahead {
		msg := fmt.Sprintf("호스트 드라이버 %s 가 정책 버전 %s 보다 높고 allowDowngrade=false — 설치·cordon 없이 멈춘다. "+
			"정책 버전을 올리거나 versionSource=Host 로 바꿀 것", host, desiredVersion)
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "HostDriverAhead", "%s", msg)
		state.Status.DesiredVersion = desiredVersion
		return m.transitionTo(state, v1alpha1.UpgradeStateHostAhead, msg, 0)
	}

	// Job 모드는 상주 DS 가 없어 driverLoaded=false(초기 미설치 / 재부팅·수동 rmmod
	// 로 모듈 언로드 / 드라이버 crash) 상태를 스스로 복구할 수 없다. 따라서 desired 버전이 지정돼
	// 있고 (a) 아직 아무 버전도 없고 로드된 드라이버도 없거나 (b) NDR 이 driverLoaded=false 를 보고하면,
	// 버전 일치 여부와 무관하게 install Job 을 트리거한다. autoUpgrade 무관(설치/복구는 기대된
	// 동작) — daemonset 의 DS pod 가 항상 modprobe 로 재로딩하는 것과 동등성을 맞춘다.
	// 버전 변경 업그레이드(driver 는 로드됐고 desired 만 상이)는 아래 autoUpgrade 게이트를 존중한다.
	if isJobMode(policy) && desiredVersion != "" {
		currentVersion = m.adoptLoadedVersion(state, loaded)
		needInstall := currentVersion == ""
		if !needInstall {
			notLoaded, err := m.driverNotLoaded(ctx, state.Spec.NodeName, state.Spec.Vendor, state.Spec.Model)
			if err != nil {
				return false, 0, err
			}
			needInstall = notLoaded
		}
		if needInstall {
			if canRestoreHeld(state, currentVersion) {
				return m.startHeldRestore(ctx, state, policy, currentVersion)
			}
			// verifiedVersions 화이트리스트는 설치/복구에도 적용(안전).
			if len(policy.Spec.VerifiedVersions) > 0 && !containsString(policy.Spec.VerifiedVersions, desiredVersion) {
				msg := fmt.Sprintf("Driver version %s 가 verifiedVersions 화이트리스트에 없음: %v",
					desiredVersion, policy.Spec.VerifiedVersions)
				m.Recorder.Eventf(state, corev1.EventTypeWarning, "UnverifiedVersion", "%s", msg)
				state.Status.DesiredVersion = desiredVersion
				return m.transitionTo(state, v1alpha1.UpgradeStateUnverifiedVersion, msg, 0)
			}
			return m.openCycle(ctx, state, policy, fmt.Sprintf("Job 모드 설치/복구: %s", desiredVersion), func() {
				state.Status.DesiredVersion = desiredVersion
				state.Status.PreviousVersion = ""
				state.Status.PreviousImage = ""
				state.Status.RollbackAttempts = 0
				state.Status.RolledBackVersion = ""
				state.Status.RolledBackGeneration = 0
				resetRebootTracking(&state.Status) // 새 사이클: 재부팅 카운터·마커 초기화
				m.Recorder.Eventf(state, corev1.EventTypeNormal, "InstallRequired",
					"Job 모드 설치/복구 필요(driverLoaded=false): 목표 버전 %s (autoUpgrade 무관)", desiredVersion)
			})
		}
	}

	if desiredVersion == "" || desiredVersion == currentVersion {
		// 버전 일치: Idle 유지, 60s 후 재확인.
		// 신규 DUS(state="")는 호스트 드라이버가 이미 목표 버전이면 업그레이드 사이클을
		// 거치지 않으므로 status.state 가 빈 문자열로 남는다. 여기서 1회 Idle 로 정규화하여
		// `kubectl get dus` STATE 와 메트릭이 Idle 을 명확히 보고하도록 한다.
		// 이미 Idle 이면 재기록하지 않아 LastTransitionTime/IdleCooldown churn 을 피한다.
		return m.settleIdle(ctx, state, desiredVersion, "초기 상태 정규화 (버전 일치)")
	}

	// verifiedVersions 화이트리스트 검사는 autoUpgrade 게이트보다 앞이다 — 허용 목록 밖 버전은
	// 자동 업그레이드를 끈 정책에서도 거부 사유를 드러내야 한다. 이 전이는 Job 을 만들지 않고
	// cordon 도 하지 않으며, 버전이 목록에 다시 들어오면 handleUnverifiedVersion 이 Idle 로
	// 되돌린다.
	if len(policy.Spec.VerifiedVersions) > 0 && !containsString(policy.Spec.VerifiedVersions, desiredVersion) {
		msg := fmt.Sprintf("Driver version %s 가 verifiedVersions 화이트리스트에 없음: %v",
			desiredVersion, policy.Spec.VerifiedVersions)
		logger := logf.FromContext(ctx)
		logger.Info("verifiedVersions 검증 실패 — 업그레이드 거부",
			"node", state.Spec.NodeName,
			"desiredVersion", desiredVersion,
			"verifiedVersions", policy.Spec.VerifiedVersions)
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "UnverifiedVersion", "%s", msg)
		state.Status.DesiredVersion = desiredVersion
		return m.transitionTo(state, v1alpha1.UpgradeStateUnverifiedVersion, msg, 0)
	}

	// autoUpgrade가 false이면 이벤트만 발행하고 Idle 유지
	if policy.Spec.UpgradePolicy == nil || !policy.Spec.UpgradePolicy.AutoUpgrade {
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeAvailable",
			"드라이버 버전 불일치: current=%s desired=%s (autoUpgrade 비활성화)", currentVersion, desiredVersion)
		return m.settleIdle(ctx, state, desiredVersion, "초기 상태 정규화 (autoUpgrade 비활성화)")
	}

	if m.holdAfterRollback(state, policy, desiredVersion, currentVersion) {
		return true, 60 * time.Second, nil
	}

	// IdleCooldown 가드 — Idle 진입 직후 (default 10s) 연속 trigger 거부.
	// 사용자가 trigger 를 빠르게 두 번 던지면 첫 사이클의 Uncordoning→Idle 직후 다시 Cordoning 으로
	// 진입해, 검증 스크립트가 mid-state 를 완료로 오인하는 결함을 막는다.
	// IdleCooldownSeconds 가 nil 이면 default, 0/음수면 비활성화.
	cooldown := defaultIdleCooldown
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.IdleCooldownSeconds != nil {
		s := *policy.Spec.UpgradePolicy.IdleCooldownSeconds
		if s <= 0 {
			cooldown = 0
		} else {
			cooldown = time.Duration(s) * time.Second
		}
	}
	if cooldown > 0 && !state.Status.LastTransitionTime.IsZero() {
		elapsed := time.Since(state.Status.LastTransitionTime.Time)
		if elapsed < cooldown {
			remaining := cooldown - elapsed
			logger := logf.FromContext(ctx)
			logger.Info("Idle cooldown 미충족, 트리거 연기",
				"elapsed", elapsed.Round(time.Second),
				"cooldown", cooldown,
				"remaining", remaining.Round(time.Second),
				"node", state.Spec.NodeName)
			if m.Recorder != nil {
				m.Recorder.Eventf(state, corev1.EventTypeNormal, "IdleCooldownDeferred",
					"Idle 진입 후 %s 미만 (cooldown=%s) — %s 후 재시도",
					elapsed.Round(time.Second), cooldown, remaining.Round(time.Second))
			}
			// state 변경 없이 잔여 시간 후 재시도 (transitionTo 미호출 → LastTransitionTime 보존)
			return true, remaining, nil
		}
	}

	// 버전 불일치 + autoUpgrade: UpgradeRequired로 전이
	// PreviousImage는 이번 사이클에서 handleUpgrading이 DS로부터 다시 수집하도록 비움.
	// 이전 업그레이드 값이 남아있으면 잘못된 이미지로 롤백될 수 있음.
	// RollbackAttempts는 새 사이클이므로 0으로 초기화(무한 루프 방지).
	return m.openCycle(ctx, state, policy, fmt.Sprintf("버전 불일치 감지: %s → %s", currentVersion, desiredVersion), func() {
		state.Status.DesiredVersion = desiredVersion
		state.Status.PreviousVersion = currentVersion
		state.Status.PreviousImage = ""
		state.Status.RollbackAttempts = 0
		state.Status.RolledBackVersion = ""
		state.Status.RolledBackGeneration = 0
		resetRebootTracking(&state.Status) // 새 사이클: 재부팅 카운터·마커 초기화
	})
}

// missingInstallSecret 은 설치 파드가 마운트할 Secret 이 운영자 네임스페이스에 없으면 그 이름
// (namespace/name)을, 있거나 필요 없거나 확인할 수 없으면 빈 문자열을 돌려준다. 마운트 여부는 설치
// Job·DaemonSet 렌더러와 같은 어댑터 판단(AptAuthRequired)이다. 확인할 수 없을 때(APIReader 미주입,
// Role 없는 배포의 Forbidden)는 종전처럼 진행시킨다 — 막으면 Secret 이 있는 클러스터에서도 설치가
// 시작되지 않는다. 메타데이터만 읽으므로 자격 증명 값은 operator 로 오지 않는다.
func (m *UpgradeStateMachine) missingInstallSecret(ctx context.Context, policy *v1alpha1.DriverInstallPolicy) (string, error) {
	a, ok := driveradapter.ForModel(policy.Spec.Vendor, policy.Spec.Model)
	if !ok || !a.AptAuthRequired() || m.APIReader == nil {
		return "", nil
	}
	key := types.NamespacedName{Namespace: naming.OperatorNamespace(), Name: driveradapter.AptAuthSecretName}
	var meta metav1.PartialObjectMetadata
	meta.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
	switch err := m.APIReader.Get(ctx, key, &meta); {
	case err == nil:
		return "", nil
	case apierrors.IsNotFound(err):
		return key.String(), nil
	case apierrors.IsForbidden(err):
		logf.FromContext(ctx).V(1).Info("설치 Secret 조회 권한 없음 — 확인 없이 진행", "secret", key.String())
		return "", nil
	default:
		return "", fmt.Errorf("설치 Secret 확인 실패: %w", err)
	}
}

// InstallSecretRecheck 는 설치 Secret 이 없을 때 다시 확인하는 간격이다. Secret 은 watch 하지 않는다.
const InstallSecretRecheck = 60 * time.Second

// holdForInstallSecret 은 설치 Secret 이 없으면 대기 사유를 status.message 에 남기고 true 를 돌려준다
// (사유가 바뀔 때만 경고 이벤트). openCycle 이 사이클을 열기 직전에 부른다 — 기다리는 동안 Idle 에
// 머물러야 목표 버전·autoUpgrade·verifiedVersions 가 매 pass 다시 판정된다. UpgradeRequired 에서
// 기다리면 대기 중에 바뀐 정책이 무시되고 Secret 이 생기는 순간 예전 목표로 사이클이 열린다.
func (m *UpgradeStateMachine) holdForInstallSecret(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy) (bool, error) {
	missing, err := m.missingInstallSecret(ctx, policy)
	if err != nil || missing == "" {
		return false, err
	}
	m.noteWait(state, corev1.EventTypeWarning, "InstallSecretMissing",
		fmt.Sprintf("설치 Secret %s 없음 — 만들면 자동 진행(그때까지 업그레이드 슬롯·cordon 미점유)", missing))
	return true, nil
}

// BackToIdleWithoutInstallSecret 은 UpgradeRequired 의 DUS 를 설치 Secret 이 없으면 Idle 로 되돌리고
// true 를 돌려준다(status 저장은 호출자 몫). 대기는 Idle 이 맡는다(holdForInstallSecret). 슬롯을 잡기
// 직전의 상태머신과 작업을 만들기 직전의 위임 경로가 같은 판정을 쓰도록 여기 하나로 둔다.
func (m *UpgradeStateMachine) BackToIdleWithoutInstallSecret(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy) (bool, error) {
	held, err := m.holdForInstallSecret(ctx, state, policy)
	if err != nil || !held {
		return false, err
	}
	_, _, _ = m.transitionTo(state, v1alpha1.UpgradeStateIdle, state.Status.Message, 0)
	return true, nil
}

// handleUpgradeRequired: 설치 Secret·MaxParallelUpgrades 슬롯 확인 후 PreFlight로 전이
func (m *UpgradeStateMachine) handleUpgradeRequired(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// 설치 파드가 마운트할 Secret 이 없으면 슬롯을 잡지 않고 Idle 로 돌아가 기다린다. 잡으면 설치 파드가
	// 볼륨 마운트에서 멈춘 채 install Job 시한(30분)까지 노드 cordon 과 슬롯을 쥐어, 슬롯을 함께 세는 다른
	// 벤더(같은 노드의 NVIDIA 등)의 설치까지 막는다. 보통은 Idle 이 먼저 막으므로 여기 오는 것은 수정 배포
	// 전에 들어온 DUS 와 전이 직후 Secret 이 지워진 경우다.
	if back, err := m.BackToIdleWithoutInstallSecret(ctx, state, policy); err != nil || back {
		return back, InstallSecretRecheck, err
	}

	maxParallel := int32(1)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.MaxParallelUpgrades > 0 {
		maxParallel = policy.Spec.UpgradePolicy.MaxParallelUpgrades
	} else if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.MaxUnavailable > 0 {
		maxParallel = policy.Spec.UpgradePolicy.MaxUnavailable
	}

	active, err := m.activeUpgrades(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("활성 업그레이드 수 조회 실패: %w", err)
	}

	if int32(len(active)) >= maxParallel {
		// 슬롯 없음: 대기. 슬롯은 벤더 구분 없이 세므로 누가 쥐었는지 남긴다 — 다른 벤더의 사이클이
		// 막고 있어도 이 DUS 에서 이유가 보인다.
		m.noteWait(state, corev1.EventTypeNormal, "UpgradeSlotWait",
			fmt.Sprintf("업그레이드 슬롯 대기(%d/%d 사용 중: %s)", len(active), maxParallel, strings.Join(active, ", ")))
		return true, 30 * time.Second, nil
	}

	// 슬롯 확보: PreFlight로 전이
	return m.transitionTo(state, v1alpha1.UpgradeStatePreFlight, "업그레이드 슬롯 확보", 0)
}

// handlePreFlight: 커널 allowlist·노드 Ready·설치 전 점검(NVIDIA job·apt) 뒤 Cordoning으로 전이
func (m *UpgradeStateMachine) handlePreFlight(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// 노드 커널 버전 조회
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &node); err != nil {
		return false, 0, fmt.Errorf("노드 조회 실패: %w", err)
	}

	kernelVersion := node.Status.NodeInfo.KernelVersion
	// 노드 커널로 사전 컴파일 이미지를 고른다. 여기서 고른 값은 handleUpgradingJob 이 install Job
	// 에 실을 이미지이며, 호출부(driver_upgrade_controller.go Reconcile 의 Status().Update)가 이번
	// 전이의 status 저장에 함께 실어 영속한다.
	resolvedImage, imageSkipReason, imageSkipEvent := resolveDriverImage(policy, kernelVersion)
	state.Status.ResolvedImage = resolvedImage
	// 경고 셋은 점검 Job 이 없는 회차에만 낸다. Job 이 이미 있는 회차는 점검을 기다리는 중이거나 지난 사이클의 Job 이
	// 남은 경우다. 대기 회차마다 10초 간격으로 되풀이하면 client-go 이벤트 스팸 필터(같은 객체·Type 당 burst 25,
	// 5분에 1건)가 뒤따르는 DriverPreflightBlocked·DriverPreflightInconclusive 를 버릴 수 있다.
	waiting := false
	if preflightApplicable(policy) {
		var err error
		if waiting, err = m.preflightInProgress(ctx, state); err != nil {
			return false, 0, err
		}
	}
	if !waiting {
		// 정책이 이 커널용 사전 컴파일 이미지를 선언했지만 쓰지 않았다면 그 이유를 드러낸다. 기본
		// 이미지(DKMS 컴파일)로 되돌아가므로 설치는 올바른 버전으로 진행되지만, 운영자가 정책의 태그를
		// 바로잡을 수 있도록 경고 이벤트와 로그를 남긴다.
		if imageSkipReason != "" {
			logf.FromContext(ctx).Info("사전 컴파일 커널 이미지를 쓰지 않고 기본 이미지로 되돌아감",
				"kernel", kernelVersion, "reason", imageSkipReason)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, imageSkipEvent, "%s", imageSkipReason)
		}
		// DIP spec 을 제3 관리자(kubectl patch/edit, ArgoCD·Flux 등)가 함께 쥐고 있으면 다음 helm
		// upgrade 가 Server-Side Apply 충돌로 멈출 수 있다 — PreFlight 마다 드러내 운영자가 helm
		// 값으로만 바꾸도록 유도한다. 기존 imageSkipReason 경고와 같은 자리·같은 빈도.
		// 제품 자신의 제어 경로(CLI·관리 REST·helm)는 foreignSpecManagers 가 이름으로 제외한다.
		// 문구는 지목된 관리자에 대한 조치만 적는다 — 제외된 이름은 %v 에 나오지 않으므로 그쪽 안내를
		// 섞으면 경고와 조치가 어긋난다. 소유 회수까지 적는 이유는 재발 방지만으로는 선 경고가 지워지지 않기 때문이다.
		if foreign := foreignSpecManagers(policy.ManagedFields); len(foreign) > 0 {
			m.Recorder.Eventf(state, corev1.EventTypeWarning, eventPolicySharedOwnership,
				"DriverInstallPolicy %s 의 spec 을 제3 관리자 %v 가 함께 소유한다 — 값은 helm 값 파일로만 바꿀 것(다음 helm upgrade 의 SSA 충돌 예방). 이미 생긴 소유 항목은 helm 이 같은 필드를 같은 값으로 다시 적용해 소유를 회수할 때까지 남고, 그동안 이 경고도 PreFlight 마다 선다",
				policy.Name, foreign)
		}
		// probe 제한시간이 Validating 전체 예산보다 길면 그 probe 는 결론을 낼 수 없다 — 바깥
		// 예산(handleValidating 의 validationTimeout)이 먼저 만료해 Rollback·Failed 로 전이하고
		// WorkloadValidator.Run 은 다시 불리지 않는다. 생성된 probe 파드는 kubelet 의
		// activeDeadlineSeconds(= 이 TimeoutSeconds)까지 남아 그동안 장치 점유자로 집계된다.
		// 거절하지 않고 경고만 낸다 — 기존 객체를 admission 에서 막으면 운영 중 정책이 갱신 불가가 된다.
		// 예산 파싱은 handleValidating 이 이미 쓰는 parseDuration 을 재사용(새 헬퍼 미추가).
		if wp := policy.Spec.WorkloadProbe; wp != nil && wp.Enabled {
			var rawTimeout string
			if policy.Spec.UpgradePolicy != nil {
				rawTimeout = policy.Spec.UpgradePolicy.ValidationTimeout
			}
			validationTimeout := parseDuration(rawTimeout, 15*time.Minute)
			// 예산 출처를 함께 싣는다 — 빈 값이나 파싱 실패면 parseDuration 이 기본값을 돌려주므로,
			// 그 값을 그대로 쓰면 운영자가 DIP 에서 찾을 수 없는 필드값을 보게 된다.
			budget := fmt.Sprintf("%s(기본값, upgradePolicy.validationTimeout 미설정)", validationTimeout)
			if rawTimeout != "" {
				budget = fmt.Sprintf("%s(upgradePolicy.validationTimeout=%q)", validationTimeout, rawTimeout)
			}
			// timeoutSeconds 가 비면 Timeout() 이 기본값(120s)을 돌려준다 — 실제로 쓰이는 그 값을
			// 비교해야 기본 설정의 모순도 드러난다.
			probeTimeout := (&validator.WorkloadValidator{Probe: *wp}).Timeout()
			if probeTimeout > validationTimeout {
				probeSrc := probeTimeout.String()
				if wp.TimeoutSeconds == 0 {
					probeSrc += "(timeoutSeconds 미설정 기본값)"
				}
				m.Recorder.Eventf(state, corev1.EventTypeWarning, "WorkloadProbeTimeoutExceedsBudget",
					"workloadProbe 제한시간 %s 가 검증 예산 %s 보다 길다 — "+
						"probe 결과가 쓰이지 않고 파드가 그 차이만큼 장치를 점유한다. 둘 중 하나를 고칠 것",
					probeSrc, budget)
			}
		}
	}
	allowlist := policy.Spec.KernelAllowlist

	if len(allowlist) > 0 {
		if !matchesKernelAllowlist(kernelVersion, allowlist) {
			msg := fmt.Sprintf("커널 버전 %s이 allowlist에 없음: %v", kernelVersion, allowlist)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "PreFlightFailed", msg)
			return m.transitionTo(state, v1alpha1.UpgradeStateRollback, msg, 0)
		}
	}

	// 노드 Ready 상태 확인
	if !isNodeReady(&node) {
		return true, 10 * time.Second, nil
	}

	// 설치 전 점검(NVIDIA job·apt): hold 된 호스트 패키지가 설치를 막으면 cordon 전에 끝낸다.
	if preflightApplicable(policy) {
		verdict, cause, err := m.runDriverPreflight(ctx, state, policy)
		if err != nil {
			return false, 0, err
		}
		switch verdict {
		case preflightRunning:
			return true, 10 * time.Second, nil
		case preflightBlocked:
			metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "DriverPreflightBlocked", "%s", cause)
			// cordon 전이라 되돌릴 라벨·quiesce 가 없다. 정리를 부르면 같은 노드 다른 사이클의 라벨·quiesce 를 지우거나 되돌린다.
			return m.transitionTo(state, v1alpha1.UpgradeStateFailed, preflightBlockedMessage(state, cause), 0)
		}
	}

	return m.transitionTo(state, v1alpha1.UpgradeStateCordoning, "PreFlight 검사 통과", 0)
}

// handleCordoning: 노드를 Unschedulable로 설정 후 Draining으로 전이
func (m *UpgradeStateMachine) handleCordoning(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) (bool, time.Duration, error) {
	if err := m.cordonNode(ctx, state.Spec.NodeName, state); err != nil {
		return false, 0, fmt.Errorf("노드 cordon 실패: %w", err)
	}

	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeCordoned",
		"업그레이드를 위해 노드 %s cordon 완료", state.Spec.NodeName)

	// opt-in 라벨이 붙은 Deployment 를 노드별로 quiesce(scale=0)한다.
	// gpu-stress 등 NodeAffinity reject 로 Failed Pod 가 누적되는 결함을 차단한다.
	// 실패 시 사이클은 계속 진행 (best-effort) — backup 이 비어 있으면 restore 도 no-op.
	if err := m.QuiesceLabeledDeployments(ctx, state); err != nil {
		logf.FromContext(ctx).Error(err, "quiesceLabeledDeployments 실패 (사이클은 계속 진행)",
			"node", state.Spec.NodeName)
	}

	return m.transitionTo(state, v1alpha1.UpgradeStateDraining, "노드 cordon 완료", 0)
}

// handleDraining: GPU/NPU 워크로드 Pod 삭제 후 Upgrading으로 전이
func (m *UpgradeStateMachine) handleDraining(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// drainEnabled가 false면 즉시 Upgrading으로
	if policy.Spec.UpgradePolicy == nil || !policy.Spec.UpgradePolicy.DrainEnabled {
		return m.transitionTo(state, v1alpha1.UpgradeStateUpgrading, "drain 비활성화, 즉시 업그레이드", 0)
	}

	// drain timeout 체크
	drainTimeout := parseDuration(policy.Spec.UpgradePolicy.DrainTimeout, 5*time.Minute)
	if !state.Status.LastTransitionTime.IsZero() &&
		time.Since(state.Status.LastTransitionTime.Time) > drainTimeout {
		if policy.Spec.UpgradePolicy.RollbackOnFailure {
			return m.transitionTo(state, v1alpha1.UpgradeStateRollback, "drain 타임아웃: 롤백 시작", 0)
		}
		if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
		}
		if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
				"Failed 전이 중 라벨 제거 실패 (수동 조치 필요): node=%s err=%v", state.Spec.NodeName, err)
		}
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, "drain 타임아웃: 수동 조치 필요", 0)
	}

	// nvidia-persistenced 종료 요청 (실제 중지는 driver-manager initContainer에서 수행)
	if strings.EqualFold(state.Spec.Vendor, "nvidia") {
		m.logPersistencedStop(ctx, state.Spec.NodeName)
	}

	// device-plugin Pod 삭제 (커널 모듈 참조 해제를 위해)
	logger := logf.FromContext(ctx)
	if err := m.deleteDevicePluginPods(ctx, state.Spec.NodeName); err != nil {
		logger.Error(err, "device-plugin Pod 삭제 실패")
		// 비치명적: 계속 진행
	}

	// 디바이스 워크로드 확인
	hasWorkloads, err := m.hasDeviceWorkloads(ctx, state.Spec.NodeName)
	if err != nil {
		return false, 0, fmt.Errorf("디바이스 워크로드 확인 실패: %w", err)
	}

	if hasWorkloads {
		// 워크로드 Pod 삭제 시도: PDB를 존중하는 Eviction API 사용.
		// ForceUpgrade=true일 때만 PDB 위반 시 직접 Delete로 폴백 (kubectl drain --force 동등).
		force := policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.ForceUpgrade
		if err := m.evictDevicePods(ctx, state, force); err != nil {
			return false, 0, fmt.Errorf("디바이스 Pod eviction 실패: %w", err)
		}
		return true, 15 * time.Second, nil
	}

	// Pod 축이 비었어도 claim 축은 따로 본다 — DRA 는 장치를 Pod 이 아니라 ResourceClaim 이 쥔다.
	// kubelet 은 NodeUnprepare → reservedFor 비움 → deallocate 순서라, Pod 이 사라진 뒤에도 claim
	// 이 한동안 할당 상태로 남는 구간을 정상 drain 이 반드시 지난다. 그래서 "아직 할당돼 있다"는
	// Pod 축의 "아직 Pod 가 남았다"와 같은 등급으로 보고 오류가 아니라 정상 재조정(requeue)으로
	// 돌린다 — 읽기 실패(List·Node Get)만 오류로 올린다. transitionTo 를 쓰지 않아
	// LastTransitionTime 은 보존되고, 끝내 비지 않으면 머리의 drain 타임아웃 검사가 처리한다.
	if err := draclaim.AssertNoneAllocatedOnNode(ctx, m.Client, state.Spec.NodeName); err != nil {
		if !errors.Is(err, draclaim.ErrClaimStillAllocated) {
			return false, 0, fmt.Errorf("drain 완료 전 ResourceClaim 검사 실패: %w", err)
		}
		// Status.Message 는 transitionTo 만 쓰므로 Event 가 없으면 왜 멈췄는지 밖에서 보이지
		// 않는다. 재조정 주기가 15초라 Event 폭주 방지 장치는 따로 필요 없다.
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "DrainBlockedByClaim",
			"할당된 ResourceClaim 이 남아 drain 대기 중: %v", err)
		return true, 15 * time.Second, nil
	}

	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeDrained",
		"노드 %s drain 완료", state.Spec.NodeName)
	return m.transitionTo(state, v1alpha1.UpgradeStateUpgrading, "drain 완료", 0)
}

// handleUpgrading: DaemonSet 이미지 업데이트 후 Validating으로 전이 (requeue 20s)
func (m *UpgradeStateMachine) handleUpgrading(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// Job 모드는 install Job 생성/재생성 경로로 분기(daemonset 본문 무변경).
	if isJobMode(policy) {
		return m.handleUpgradingJob(ctx, state, policy)
	}

	// DaemonSet 이름 결정 (driver_daemonset_controller.go line 98 패턴과 일치)
	dsName := naming.DriverDSName(state.Spec.Vendor, state.Spec.Model)

	var ds appsv1.DaemonSet
	if err := m.Get(ctx, types.NamespacedName{Name: dsName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		if apierrors.IsNotFound(err) {
			return true, 20 * time.Second, nil
		}
		return false, 0, fmt.Errorf("DaemonSet 조회 실패: %w", err)
	}

	desiredImage := policy.Spec.Driver.Image
	// 이미지 업데이트
	base := ds.DeepCopy()
	updated := false
	prevImage := ""
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == fmt.Sprintf("%s-driver", state.Spec.Vendor) ||
			i == 0 {
			prevImage = ds.Spec.Template.Spec.Containers[i].Image
			if prevImage != desiredImage {
				ds.Spec.Template.Spec.Containers[i].Image = desiredImage
				updated = true
			}
			break
		}
	}

	if updated {
		// 롤백 시 빌드 접미사 포함 원본 이미지를 복구하기 위해 패치 직전에 기록한다.
		// broken plain tag (예: ":580.126.09") 가 PreviousImage 로 캡처되어 rollback 시
		// broken Pod 으로 회귀하는 것을 막는다. isVerifiedBuildTag 가 true 인
		// (-v<N> 접미사 또는 검증된) 태그만 저장한다.
		if state.Status.PreviousImage == "" && prevImage != "" && prevImage != desiredImage {
			if isVerifiedBuildTag(prevImage) {
				state.Status.PreviousImage = prevImage
			} else {
				logger := logf.FromContext(ctx)
				logger.Info("plain tag 감지 — PreviousImage 저장 skip (broken image rollback 차단)",
					"prevImage", prevImage, "node", state.Spec.NodeName)
				m.Recorder.Eventf(state, corev1.EventTypeWarning, "PreviousImagePlainTagSkipped",
					"plain tag (%s) 감지: PreviousImage 저장을 skip 했습니다 (rollback 시 broken image 회귀 방지). "+
						"DIP.spec.driver.image 를 -v<N> 접미사 빌드 태그로 변경하세요.", prevImage)
			}
		}
		if err := m.Patch(ctx, &ds, client.MergeFrom(base)); err != nil {
			return false, 0, fmt.Errorf("DaemonSet 이미지 업데이트 실패: %w", err)
		}
	}

	// 해당 노드의 기존 드라이버 Pod 삭제 (OnDelete 전략 트리거)
	if err := m.deleteDriverPodOnNode(ctx, dsName, state.Spec.NodeName); err != nil {
		return false, 0, fmt.Errorf("드라이버 Pod 삭제 실패: %w", err)
	}

	m.Recorder.Eventf(state, corev1.EventTypeNormal, "UpgradeStarted",
		"노드 %s 드라이버 업그레이드 시작: %s", state.Spec.NodeName, state.Status.DesiredVersion)

	return m.transitionTo(state, v1alpha1.UpgradeStateValidating, "DaemonSet 이미지 업데이트 완료", 20*time.Second)
}

// validatorsFor 는 정책에 맞는 검증 체인이다. 기본은 DriverModule·DevicePlugin 둘이고, 정책이
// workloadProbe 를 켜면 Workload 를 끝에 더한다(정책 opt-in). 두 실행 경로(DS·Job, 아래
// handleValidating·state_machine_job.go 의 handleValidatingJob)가 같은 체인을 쓴다.
//  1. DriverModule  : 노드의 드라이버 커널 모듈 로드 (NDR.driverVersion 매칭)
//  2. DevicePlugin  : kube-system 의 device-plugin Pod ContainersReady
//  3. Workload      : (opt-in) 샘플 워크로드 probe 파드의 종료 코드
func validatorsFor(policy *v1alpha1.DriverInstallPolicy) []validator.Validator {
	chain := []validator.Validator{&validator.DriverModuleValidator{}, &validator.DevicePluginValidator{}}
	if policy != nil && policy.Spec.WorkloadProbe != nil && policy.Spec.WorkloadProbe.Enabled {
		chain = append(chain, &validator.WorkloadValidator{Probe: *policy.Spec.WorkloadProbe, Namespace: naming.OperatorNamespace()})
	}
	return chain
}

// handleValidating 은 validator 체인을 순차 실행해 새 드라이버가 정상 동작하는지 검증한다.
// validator 가 Passed=true 면 다음으로 진행하고, Passed=false 면 caller wall-clock budget 안에서
// 재시도(requeueAfter 10s)하며, error 는 재시도 가능 API 오류로 보고 controller-runtime 에
// 전파한다. 추가 안전장치: 전체 ValidationTimeout 초과 시 정책에 따라 Rollback/Failed, 드라이버
// Pod CrashLoopBackOff 는 재시도가 무의미하므로 즉시 Rollback/Failed.
func (m *UpgradeStateMachine) handleValidating(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// Job 모드는 "Job 성공 종료 + NDR 일치" 검증 경로로 분기(daemonset 본문 무변경).
	if isJobMode(policy) {
		return m.handleValidatingJob(ctx, state, policy)
	}

	logger := logf.FromContext(ctx)

	// Validating 진입 시점에 detector 차단 라벨만 제거한다.
	// driver-upgrading 라벨은 사이클 추적용으로 보존(Uncordoning 에서 함께 제거).
	// detector 재spawn → NDR 갱신 → DriverModuleValidator 통과로 이어진다.
	if err := m.EnsureUpgradingBlockingLabelRemoved(ctx, state.Spec.NodeName); err != nil {
		logger.Error(err, "Validating 진입 시 blocking 라벨 제거 실패", "node", state.Spec.NodeName)
	} else {
		m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeBlockingLabelRemoved",
			"노드 %s 의 driver-upgrading-blocking 라벨 제거 (Validating 진입)", state.Spec.NodeName)
	}

	// ─── 1. 전체 validation timeout 체크 ─────────────────────
	validationTimeout := parseDuration("", 15*time.Minute)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.ValidationTimeout != "" {
		validationTimeout = parseDuration(policy.Spec.UpgradePolicy.ValidationTimeout, 15*time.Minute)
	}
	if !state.Status.LastTransitionTime.IsZero() &&
		time.Since(state.Status.LastTransitionTime.Time) > validationTimeout {
		if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.RollbackOnFailure {
			return m.transitionTo(state, v1alpha1.UpgradeStateRollback, "검증 타임아웃: 롤백 시작", 0)
		}
		const msg = "검증 타임아웃: 수동 조치 필요"
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "ValidationTimeout", "%s", msg)
		if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
			logger.Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
		}
		if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
			logger.Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
				"Failed 전이 중 라벨 제거 실패 (수동 조치 필요): node=%s err=%v", state.Spec.NodeName, err)
		}
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, msg, 0)
	}

	// ─── 2. CrashLoop 즉시 감지 (재시도 무의미한 hard failure) ───
	// driver-installer Pod 가 CrashLoopBackOff 라면 어떤 validator 도 통과 못 하므로
	// 빠르게 Rollback / Failed 로 전이한다 (legacy 동작 보존).
	dsName := naming.DriverDSName(state.Spec.Vendor, state.Spec.Model)
	desiredImage := policy.Spec.Driver.Image
	ready, crashLoop, err := m.isDriverPodReadyOnNode(ctx, dsName, state.Spec.NodeName, desiredImage)
	if err != nil {
		return false, 0, fmt.Errorf("드라이버 Pod 상태 확인 실패: %w", err)
	}
	if crashLoop {
		if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.RollbackOnFailure {
			return m.transitionTo(state, v1alpha1.UpgradeStateRollback, "드라이버 Pod CrashLoopBackOff: 롤백 시작", 0)
		}
		if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
			logger.Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
		}
		if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
			logger.Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
				"Failed 전이 중 라벨 제거 실패 (수동 조치 필요): node=%s err=%v", state.Spec.NodeName, err)
		}
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, "드라이버 Pod CrashLoopBackOff: 수동 조치 필요", 0)
	}

	// 새 Pod 이 아직 준비되지 않은 경우 (wrong-image 캐시 / NotReady 등) 단순 requeue 한다.
	// ValidationTimeout 안에 안정되지 않으면 위 timeout 가드가 Rollback / Failed 로 전이한다.
	if !ready {
		logger.Info("Validating: 새 Pod 미준비 (이미지 불일치 또는 NotReady)",
			"node", state.Spec.NodeName)
		return true, 10 * time.Second, nil
	}

	// ─── 3. validator 체인 순차 실행 ─────────────────────────
	desiredVersion := state.Status.DesiredVersion
	for _, v := range validatorsFor(policy) {
		// validator 시작 Event (재시도마다 발행되지 않도록 "처음" 진입 시점만 emit 하기 어렵기에
		// per-attempt event 로 발행 — 운영자에 진행 상황 가시성 우선).
		m.Recorder.Eventf(state, corev1.EventTypeNormal,
			fmt.Sprintf("UpgradeValidator-%s-Started", v.Name()),
			"validator 실행: %s (timeout=%s, node=%s)", v.Name(), v.Timeout(), state.Spec.NodeName)

		res, err := v.Run(ctx, m.Client, state.Spec.NodeName, state.Spec.Vendor, desiredVersion)
		if err != nil {
			// 재시도 가능한 API 오류 — controller-runtime 으로 전파.
			return false, 0, fmt.Errorf("validator %s 실행 실패: %w", v.Name(), err)
		}
		if !res.Passed {
			// 검증 미통과 — 상위 ValidationTimeout 안에서 재시도.
			m.Recorder.Eventf(state, corev1.EventTypeNormal,
				fmt.Sprintf("UpgradeValidator-%s-Failed", v.Name()),
				"validator %s 미통과 (재시도 대기): %s", v.Name(), res.Message)
			return true, 10 * time.Second, nil
		}
		m.Recorder.Eventf(state, corev1.EventTypeNormal,
			fmt.Sprintf("UpgradeValidator-%s-Passed", v.Name()),
			"validator %s 통과: %s", v.Name(), res.Message)
	}

	// ─── 4. 모든 validator 통과 → 검증 성공 ──────────────────
	state.Status.CurrentVersion = state.Status.DesiredVersion
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "UpgradeValidated",
		"노드 %s 드라이버 검증 성공: %s", state.Spec.NodeName, state.Status.DesiredVersion)
	return m.transitionTo(state, v1alpha1.UpgradeStateUncordoning, "검증 성공", 0)
}

// handleUncordoning: 노드를 Schedulable로 복구 후 Idle로 전이
func (m *UpgradeStateMachine) handleUncordoning(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) (bool, time.Duration, error) {
	if err := m.uncordonNode(ctx, state.Spec.NodeName, state); err != nil {
		return false, 0, fmt.Errorf("노드 uncordon 실패: %w", err)
	}

	// 사이클 정상 종료 시 quiesce 된 Deployment 를 복구한다.
	// uncordon 후 Idle 진입 직전이 가장 안전한 시점이다 (노드 schedulable + 새 driver 준비 완료).
	if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
		logf.FromContext(ctx).Error(err, "restoreQuiescedDeployments 실패 (수동 조치 필요)",
			"node", state.Spec.NodeName)
	}

	if v := state.Status.RolledBackVersion; v != "" {
		// 보류 중 복구 사이클(startHeldRestore)은 롤백 Job 을 만들지 않아 rollbackAttempts 가 0 이다.
		if state.Status.RollbackAttempts == 0 {
			msg := fmt.Sprintf("자체 복구 완료(롤백 보류 유지): 보류 목표 %s 대신 %s 재설치", v, state.Status.DesiredVersion)
			m.Recorder.Eventf(state, corev1.EventTypeNormal, "SelfHealCompleted", "노드 %s %s", state.Spec.NodeName, msg)
			return m.transitionTo(state, v1alpha1.UpgradeStateIdle, msg, 0)
		}
		msg := fmt.Sprintf("롤백 완료: %s 대신 %s 로 복원", v, state.Status.DesiredVersion)
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackCompleted", "노드 %s %s", state.Spec.NodeName, msg)
		return m.transitionTo(state, v1alpha1.UpgradeStateIdle, msg, 0)
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeUncordoned",
		"노드 %s uncordon 완료, 업그레이드 성공", state.Spec.NodeName)
	metrics.RecordUpgradeComplete(state.Spec.Vendor, "success")
	return m.transitionTo(state, v1alpha1.UpgradeStateIdle, "업그레이드 완료", 0)
}

// handleFailed 는 터미널 Failed 상태를 처리한다. 자동 복구(상태 전이 / DS image patch / install
// Job 재생성)는 하지 않는다 — rollback 무한루프 방지가 그대로 적용된다. 여기서 하는 일은 이
// 사이클이 건 cordon 을 조건이 맞으면 푸는 것뿐이고, 상태는 Failed 로 남아 수동 조치가 필요하다.
// cordon 은 노드 단위 도구라 "이 벤더만 못 쓴다"를 표현하지 못해, 한 벤더의 드라이버 소실이 같은
// 노드의 다른 벤더까지 배치 불가로 만든다 — 그래서 벤더 광고가 실제 0 임을 확인했을 때만 푼다.
// 광고가 0 이라는 관측 하나가 보장은 아니므로, 확인하지 못하면 해제보다 cordon 유지를 우선한다.
func (m *UpgradeStateMachine) handleFailed(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx).WithValues("node", state.Spec.NodeName, "vendor", state.Spec.Vendor)

	// 노드가 이미 목표 버전을 만족하고 드라이버도 로드돼 있으면 Failed 로 붙잡아 둘 이유가 없다 —
	// 실패를 만든 조건이 사라졌는데 나가는 길이 없으면 "수동 조치 필요"가 상태를 잘못 표현한다.
	// Idle 로 보내는 이유: handleIdle 은 버전이 같고 드라이버가 로드돼 있으면 아무것도 하지 않으므로
	// (Job 모드 self-heal 은 driverLoaded=false 로만 발동) 왕복이 생기지 않는다. 조건을 버전만으로
	// 넓히면 Idle 이 설치를 다시 걸어 같은 이유로 실패하는 왕복이 생기고, 그 왕복은 고착보다 나쁘다.
	if recovered, err := m.failedStateRecovered(ctx, state); err != nil {
		return false, 0, err
	} else if recovered {
		m.Recorder.Eventf(state, corev1.EventTypeNormal, "FailedStateRecovered",
			"노드가 목표 버전 %s 를 이미 만족하고 드라이버도 로드됨 — Failed 해제 후 Idle 복귀",
			state.Status.DesiredVersion)
		// 지난 사이클의 재시도 카운터를 물려주지 않는다. 남겨 두면 다음 실패가 첫 시도부터
		// 한도에 걸린다.
		state.Status.RollbackAttempts = 0
		resetRebootTracking(&state.Status)
		// failedStateRecovered 는 NDR 로드 버전이 목표와 같음을 확인한 뒤에만 true 를 돌려준다.
		// 맞춰 두지 않으면 다음 Idle 판정이 사이클 시작 때의 currentVersion 으로 버전 불일치를
		// 보고 사이클을 다시 연다.
		state.Status.CurrentVersion = state.Status.DesiredVersion

		// 우리가 건 cordon 이 있으면 푼다. ACPP quiesce(internal/partition/quiesce.go Cordon) 와
		// 운영자의 수동 cordon 은 표식을 남기지 않으므로, 여기서 직접 소유권을 확인해야 한다.
		// 표식이 없거나 남의 이름이면 그 cordon 은 남겨 둔다(파티션 적용 중 / 정비 중).
		if err := m.releaseOwnCordon(ctx, state); err != nil {
			return false, 0, fmt.Errorf("복구 탈출 중 노드 uncordon 실패: %w", err)
		}

		return m.transitionTo(state, v1alpha1.UpgradeStateIdle,
			fmt.Sprintf("복구 확인(%s): 목표 버전 도달·드라이버 로드", state.Status.DesiredVersion),
			60*time.Second)
	}

	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			// 노드가 없으면 풀 cordon 도 없다(노드 삭제 등) — 터미널 그대로 정지.
			return false, 0, nil
		}
		return false, 0, err
	}

	if err := m.reapStaleCordonClaim(ctx, &node, state); err != nil {
		return false, 0, fmt.Errorf("stale cordon claim 정리 실패: %w", err)
	}

	// 해제 조건 (a) cordon 이 실제로 남아 있고 (b) 그 cordon 의 주인이 이 DUS 여야 한다.
	// 주인 판정은 cordonNode 가 노드를 직접 false→true 로 뒤집었을 때만 남긴 annotation 으로
	// 한다. ACPP quiesce(internal/partition/quiesce.go Cordon)도 운영자의 수동 cordon 도
	// 아무 표식을 남기지 않으므로, 표식이 없거나 남의 이름이면 그 cordon 은 우리 것이 아니다.
	// 남의 cordon 을 우리가 풀면 그 주체의 의도(파티션 적용 중 / 정비 중)를 지우게 된다.
	if !node.Spec.Unschedulable || node.Annotations[cordonOwnerAnnotationKey] != state.Name {
		logger.Info("Failed 터미널 상태 — 해제할 cordon 없음(미소유 또는 이미 해제), 추가 transition 없이 즉시 반환")
		return false, 0, nil
	}

	// 해제 조건 (c) 그 벤더의 광고가 실제 0.
	stillAdvertised, decidable := vendorStillAdvertised(&node, state.Spec.Vendor)
	if !decidable || stillAdvertised != "" {
		// 유지. 보류이지 포기가 아니므로 다시 볼 기회를 남긴다 — 광고는 device-plugin 이
		// 뒤늦게 재시작하며 떨어질 수 있다. 여기서 반환하는 requeue 는 상태 전이도 DS 패치도
		// Job 생성도 하지 않으므로 무한 롤백 방지와 충돌하지 않는다(같은 switch 의
		// UnverifiedVersion 이 이미 같은 방식으로 터미널 상태를 재확인한다).
		logger.Info("Failed 터미널 상태 — cordon 유지(벤더 광고 0 미확인)",
			"advertisedResource", stillAdvertised, "vendorKnown", decidable)
		return true, failedCordonRecheckInterval, nil
	}

	// 해제. 소유권 표식도 같은 patch 로 지운다 — 표식이 남으면 사람이 다시 cordon 했을 때
	// 우리가 또 푼다.
	base := node.DeepCopy()
	node.Spec.Unschedulable = false
	delete(node.Annotations, cordonOwnerAnnotationKey)
	if err := m.Patch(ctx, &node, client.MergeFrom(base)); err != nil {
		return false, 0, fmt.Errorf("터미널 Failed 상태의 cordon 해제 실패: %w", err)
	}

	// 관측 가능성: 왜 풀렸는지를 event 와 status message 양쪽에 남긴다. 원래 실패 사유를
	// 덮지 않고 덧붙인다 — 사람이 봐야 하는 것은 "왜 실패했고 왜 cordon 은 풀렸나" 둘 다다.
	// 이 경로는 소유권 표식을 지우며 딱 한 번만 지나므로 message 가 중복 누적되지 않는다.
	m.Recorder.Eventf(state, corev1.EventTypeWarning, "FailedCordonReleased",
		"드라이버 복구 실패(수동 조치 필요)가 지속되나 벤더 %s 의 장치 광고가 0 이므로 노드 %s cordon 해제 — "+
			"같은 노드의 다른 벤더 장치 배치를 복구한다", state.Spec.Vendor, state.Spec.NodeName)
	state.Status.Message = fmt.Sprintf("%s / cordon 해제됨(%s 광고 0, 다른 벤더 배치 복구)",
		state.Status.Message, state.Spec.Vendor)
	logger.Info("Failed 터미널 상태 — cordon 해제 완료(벤더 광고 0)")
	return false, 0, nil
}

// failedStateRecovered 는 Failed 로 남은 DUS 의 노드가 이미 정책을 만족하는지 본다. 목표 버전이
// NDR 의 로드된 모듈 버전과 같고, 그 장치가 로드돼 있고, node-agent 검증의 driverModule 단계가
// 실패하지 않았을 때만 복구로 본다. currentVersion 은 Failed 에서 갱신되지 않으므로 보지 않는다.
// 로드 상태는 NDR 에서 직접 읽는다. 복귀 뒤에는 목표가 이미 로드돼 있어 Idle 이 할 일이 없고
// (컨트롤러가 Idle 에서 currentVersion 을 NDR 로 맞춘다), 롤백으로 끝난 사이클이면 rolledBackVersion
// 보류가 같은 목표 재시도를 막는다. NDR 이 없거나 이 벤더 장치가 없으면 확인하지 못한 것이므로 false 다.
func (m *UpgradeStateMachine) failedStateRecovered(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) (bool, error) {
	desired := state.Status.DesiredVersion
	if desired == "" {
		return false, nil
	}
	var ndr v1alpha1.NodeDeviceReport
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &ndr); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	matched := false
	for _, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Vendor, state.Spec.Vendor) || !DeviceModelMatches(d.Model, state.Spec.Model) {
			continue
		}
		// CurrentVersion 은 Failed 에서 갱신되지 않으므로 NDR 의 로드 버전을 직접 본다.
		if !d.DriverLoaded || d.DriverVersion != desired {
			return false, nil
		}
		matched = true
	}
	if !matched || driverModuleStepFailed(ndr.Status.Validation,
		validationVendorKey(state.Spec.Vendor, state.Spec.Model)) {
		return false, nil
	}
	return true, nil
}

// validationVendorKey 는 node-agent 검증 단계 이름의 벤더 접두다. detector 의 vendorKey
// (node-device-detector/types.go)와 같은 규칙이다 — furiosa 의 rngd 만 "rngd" 로 가르고
// 나머지는 벤더 이름 그대로다.
func validationVendorKey(vendor, model string) string {
	if strings.EqualFold(vendor, "furiosa") && strings.EqualFold(model, "rngd") {
		return "rngd"
	}
	return vendor
}

// driverModuleStepFailed 는 node-agent 검증의 driverModule 단계가 실패했는지다. 단계 이름은
// 검증 키가 하나인 노드에서 "driverModule", 여럿인 노드에서 "<key>/driverModule" 이다(key 는
// validationVendorKey). 검증을 쓰지 않는 노드(Validation=nil)는 실패로 보지 않는다.
func driverModuleStepFailed(v *v1alpha1.ValidationStatus, key string) bool {
	if v == nil {
		return false
	}
	for _, s := range v.Steps {
		if (s.Name == "driverModule" || strings.EqualFold(s.Name, key+"/driverModule")) && !s.Passed && !s.NotApplicable {
			return true
		}
	}
	return false
}

// vendorStillAdvertised 는 노드가 그 벤더의 extended resource 를 아직 광고 중인지 본다. 반환값은
// (0 이 아닌 리소스명, 판정 가능 여부)이며 ("", true) 만 "광고가 0 임을 확인했다"는 뜻이다. 카탈로그
// (internal/intent)가 모르는 벤더는 decidable=false 다 — 찾지 못한 것을 0 으로 읽지 않는다.
// 고정 이름과 대조하지 않고 allocatable 을 훑어 VendorForResource 로 되짚는 이유: NVIDIA mixed MIG 는
// 프로파일마다 동적 리소스명(nvidia.com/mig-<profile>)으로 광고해 nvidia.com/gpu 만 보면 오판되고,
// furiosa 의 두 제품(rngd/warboy)도 함께 걸린다 — cordon 은 벤더 단위이므로 해제도 전체가 0 일 때만 한다.
func vendorStillAdvertised(node *corev1.Node, vendor string) (string, bool) {
	if !intent.KnownVendor(vendor) {
		return "", false
	}
	for name, q := range node.Status.Allocatable {
		if !strings.EqualFold(intent.VendorForResource(string(name)), vendor) {
			continue
		}
		if !q.IsZero() {
			return string(name), true
		}
	}
	return "", true
}

// handleRollback: 이전 버전으로 DaemonSet 복구 후 Upgrading으로 전이
func (m *UpgradeStateMachine) handleRollback(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	// Job 모드는 "이전 버전 install Job 재실행" 경로로 분기(daemonset 본문 무변경).
	if isJobMode(policy) {
		return m.handleRollbackJob(ctx, state, policy)
	}

	// 롤백 시도 횟수를 제한한다. 반복 실패 시 무한 루프를 방지한다.
	maxRollbacks := int32(3)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.MaxRollbackAttempts > 0 {
		maxRollbacks = policy.Spec.UpgradePolicy.MaxRollbackAttempts
	}
	state.Status.RollbackAttempts++
	if state.Status.RollbackAttempts > maxRollbacks {
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackExhausted",
			"롤백 %d회 초과 (max=%d): 수동 조치 필요 (node=%s)",
			state.Status.RollbackAttempts-1, maxRollbacks, state.Spec.NodeName)
		if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
		}
		if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
				"Failed 전이 중 라벨 제거 실패: node=%s err=%v", state.Spec.NodeName, err)
		}
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed,
			fmt.Sprintf("롤백 %d회 초과: 수동 조치 필요", maxRollbacks), 0)
	}

	prevVersion := state.Status.PreviousVersion
	if prevVersion == "" {
		// 이전 버전 없음: Failed 처리
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackFailed",
			"롤백할 이전 버전 없음: 수동 조치 필요")
		if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
		}
		if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
			logf.FromContext(ctx).Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
				"Failed 전이 중 라벨 제거 실패 (수동 조치 필요): node=%s err=%v", state.Spec.NodeName, err)
		}
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, "이전 버전 없음: 수동 조치 필요", 0)
	}
	markRolledBack(state, policy)
	// DS 를 패치하기 전에 보류 기록을 저장한다. 드라이버 DaemonSet 컨트롤러는 DS 변경 이벤트로 깨어나
	// 정책 이미지로 다시 렌더하는데, 이 기록이 보여야 롤백 이미지를 덮지 않는다(rollbackHeld).
	if err := m.Status().Update(ctx, state); err != nil {
		return false, 0, fmt.Errorf("롤백 보류 기록 저장 실패: %w", err)
	}

	dsName := naming.DriverDSName(state.Spec.Vendor, state.Spec.Model)

	var ds appsv1.DaemonSet
	if err := m.Get(ctx, types.NamespacedName{Name: dsName, Namespace: naming.OperatorNamespace()}, &ds); err != nil {
		return false, 0, fmt.Errorf("DaemonSet 조회 실패: %w", err)
	}

	// 이미지를 이전 버전으로 복구
	// PreviousImage가 저장되어 있으면 전체 이미지 레퍼런스를 그대로 복구 (빌드 접미사 보존).
	// 저장되지 않았다면 RollbackTarget 정책에 따라 처리:
	//   previousValidated : 검증된 image 가 없으므로 Failed 로 전이 (안전).
	//   spec / "" (기본)  : 하위 호환 — 태그만 치환 (legacy, broken image 가능).
	prevImage := state.Status.PreviousImage
	if prevImage == "" {
		safeRollback := ""
		if policy.Spec.UpgradePolicy != nil {
			safeRollback = policy.Spec.UpgradePolicy.RollbackTarget
		}
		if safeRollback == "previousValidated" {
			metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackRefused",
				"RollbackTarget=previousValidated 인데 PreviousImage 미보유: 수동 조치 필요 (node=%s)", state.Spec.NodeName)
			if err := m.RestoreQuiescedDeployments(ctx, state); err != nil {
				logf.FromContext(ctx).Error(err, "Failed 전이 중 quiesce 복구 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
			}
			if err := m.clearUpgradingLabel(ctx, state.Spec.NodeName, state); err != nil {
				logf.FromContext(ctx).Error(err, "Failed 전이 중 라벨 제거 실패 (수동 조치 필요)", "node", state.Spec.NodeName)
				m.Recorder.Eventf(state, corev1.EventTypeWarning, "UpgradeLabelCleanupFailed",
					"Failed 전이 중 라벨 제거 실패: node=%s err=%v", state.Spec.NodeName, err)
			}
			return m.transitionTo(state, v1alpha1.UpgradeStateFailed,
				"RollbackTarget=previousValidated: PreviousImage 미보유로 안전 롤백 불가", 0)
		}
		// PreviousImage 미보유 + RollbackTarget != previousValidated:
		// DIP.spec.driver.image 의 variant 접미사를 추출해 prevVersion 과 결합한 정상 image 를 구성한다.
		// 이렇게 하면 legacy plain-tag 폴백 (예: ":580.142") 으로 ImagePullBackOff 가 발생하는 것을 방지.
		if dipImage := policy.Spec.Driver.Image; dipImage != "" {
			if variant := extractImageVariantSuffix(dipImage); variant != "" {
				if idx := strings.LastIndex(dipImage, ":"); idx >= 0 {
					prevImage = dipImage[:idx+1] + prevVersion + variant
					logf.FromContext(ctx).Info("PreviousImage 미보유 — DIP image variant 로 폴백 구성",
						"previousImage", prevImage, "variant", variant, "dipImage", dipImage)
				}
			}
		}
	}
	base := ds.DeepCopy()
	if len(ds.Spec.Template.Spec.Containers) > 0 {
		i := 0
		if prevImage != "" {
			ds.Spec.Template.Spec.Containers[i].Image = prevImage
		} else {
			img := ds.Spec.Template.Spec.Containers[i].Image
			ds.Spec.Template.Spec.Containers[i].Image = replaceImageTag(img, prevVersion)
		}
	}

	if err := m.Patch(ctx, &ds, client.MergeFrom(base)); err != nil {
		return false, 0, fmt.Errorf("DaemonSet 롤백 이미지 패치 실패: %w", err)
	}

	// 해당 노드 Pod 삭제 (이전 버전 Pod 재생성 트리거)
	if err := m.deleteDriverPodOnNode(ctx, dsName, state.Spec.NodeName); err != nil {
		return false, 0, fmt.Errorf("드라이버 Pod 삭제 실패: %w", err)
	}

	// DesiredVersion을 이전 버전으로 재설정 후 Upgrading(검증)으로
	state.Status.DesiredVersion = prevVersion
	m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackStarted",
		"노드 %s 롤백 시작: → %s", state.Spec.NodeName, prevVersion)

	// 롤백 메트릭 기록
	metrics.RecordRollback(state.Spec.Vendor)
	metrics.RecordUpgradeComplete(state.Spec.Vendor, "rollback")

	return m.transitionTo(state, v1alpha1.UpgradeStateValidating, fmt.Sprintf("롤백 시작: %s", prevVersion), 20*time.Second)
}

// logPersistencedStop은 nvidia-persistenced 종료가 필요함을 로깅합니다.
// 실제 중지는 driver-manager initContainer에서 nsenter를 통해 수행됩니다.
func (m *UpgradeStateMachine) logPersistencedStop(ctx context.Context, nodeName string) {
	logger := logf.FromContext(ctx)
	logger.Info("nvidia-persistenced 중지 예정 (driver-manager initContainer에서 처리)", "node", nodeName)
}

// ─────────────────────────────────────────────
// 헬퍼 함수
// ─────────────────────────────────────────────

// clearRollbackRequest 는 롤백 요청 어노테이션을 metadata patch 로 지운다(status 갱신과 별개).
func (m *UpgradeStateMachine) clearRollbackRequest(ctx context.Context, state *v1alpha1.DriverUpgradeState) error {
	patch := client.MergeFrom(state.DeepCopy())
	delete(state.Annotations, v1alpha1.RollbackRequestedAnnotation)
	if err := m.Patch(ctx, state, patch); err != nil {
		return fmt.Errorf("롤백 요청 어노테이션 제거 실패: %w", err)
	}
	return nil
}

// transitionTo는 상태를 변경하고 requeue 정보를 반환합니다.
// requeueAfter=0이면 즉시 requeue (Requeue=true).
// 상태 전이 시 Prometheus 메트릭을 기록합니다.
func (m *UpgradeStateMachine) transitionTo(
	state *v1alpha1.DriverUpgradeState,
	nextState string,
	message string,
	requeueAfter time.Duration,
) (bool, time.Duration, error) {
	prevState := state.Status.State
	vendor := state.Spec.Vendor
	nodeName := state.Spec.NodeName

	// 이전 상태의 소요 시간 기록
	if !state.Status.LastTransitionTime.IsZero() {
		elapsed := time.Since(state.Status.LastTransitionTime.Time)
		phase := stateToPhase(prevState)
		if phase != "" {
			metrics.RecordPhaseDuration(vendor, phase, elapsed)
		}
	}

	state.Status.State = nextState
	state.Status.LastTransitionTime = metav1.Now()
	state.Status.Message = message

	// 새 상태 게이지 업데이트
	metrics.SetUpgradeState(nodeName, vendor, nextState)

	return true, requeueAfter, nil
}

// stateToPhase는 UpgradeState 상수를 메트릭 phase 레이블로 변환합니다.
// ─────────────────────────────────────────────
// 모듈 교체 재부팅 자동화 (RebootRequired / Rebooting)
// ─────────────────────────────────────────────

// nodeNeedsReboot 는 NDR.status.devices[vendor/model].needsReboot 를 읽는다(driverNotLoaded 와 동일 패턴).
// detector(node-agent)가 마커/버전불일치로 산출하며, operator 는 읽기만 한다(트리거/오케스트레이션 분리).
func (m *UpgradeStateMachine) nodeNeedsReboot(ctx context.Context, nodeName, vendor, model string) (bool, error) {
	var ndr v1alpha1.NodeDeviceReport
	if err := m.Get(ctx, types.NamespacedName{Name: nodeName}, &ndr); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Vendor, vendor) {
			continue
		}
		if !DeviceModelMatches(d.Model, model) {
			continue
		}
		if d.NeedsReboot {
			return true, nil
		}
	}
	return false, nil
}

// rebootMarkerIsFresh 는 needsReboot 관측을 이번 사이클의 재부팅 근거로 써도 되는지 판정한다.
// 기준은 "관측이 이번 install Job 완료보다 나중인가" 다 — 완료 후 남은 마커는 그 Job 이 방금 쓴
// 것이라 진짜이고, 완료 이전 관측은 소거 전 상태를 보는 stale 이다. 시도 횟수(RebootAttempts==0)
// 만으로 가르면 재부팅 후 다시 돈 install Job 이 새로 남긴 마커를 표현할 방법이 없어 예산을 다 쓴
// 상태기계가 Validating 에서 나가지 못하고 멈춘다. 근거가 없으면(NDR 에 observedAt 없음 / Job
// 없음 / 완료시각 없음) 시도 횟수 기준으로 후퇴한다. 재부팅 예산은 handleRebootRequired 가 갖는다.
func (m *UpgradeStateMachine) rebootMarkerIsFresh(ctx context.Context, state *v1alpha1.DriverUpgradeState) bool {
	firstReboot := state.Status.RebootAttempts == 0

	var ndr v1alpha1.NodeDeviceReport
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &ndr); err != nil || ndr.Status.ObservedAt == nil {
		return firstReboot
	}
	jobName := naming.InstallJobName(state.Spec.Vendor, state.Spec.Model, state.Spec.NodeName)
	var job batchv1.Job
	if err := m.Get(ctx, types.NamespacedName{Name: jobName, Namespace: driverjob.Namespace}, &job); err != nil ||
		job.Status.CompletionTime == nil {
		return firstReboot
	}
	// 첫 재부팅은 기존 동작을 그대로 둔다(회귀 0). 관측이 설치 완료보다 나중이면 그 뒤로도 연다.
	return firstReboot || ndr.Status.ObservedAt.After(job.Status.CompletionTime.Time)
}

// maxReboots 는 정책의 MaxReboots(기본 1)를 반환한다.
func maxReboots(policy *v1alpha1.DriverInstallPolicy) int32 {
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.MaxReboots > 0 {
		return policy.Spec.UpgradePolicy.MaxReboots
	}
	return 1
}

// handleRebootRequired 는 안전 게이트 통과 후 재부팅을 트리거하고 Rebooting 으로 전이한다.
// cordon/drain 은 이미 선행 완료(멱등). 무한 재부팅은 MaxReboots 로 차단.
func (m *UpgradeStateMachine) handleRebootRequired(ctx context.Context, state *v1alpha1.DriverUpgradeState, policy *v1alpha1.DriverInstallPolicy) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)

	// 게이트: needsReboot 재확인(NDR stale 로 인한 불필요 재부팅 차단).
	need, err := m.nodeNeedsReboot(ctx, state.Spec.NodeName, state.Spec.Vendor, state.Spec.Model)
	if err != nil {
		return false, 0, err
	}
	if !need {
		return m.transitionTo(state, v1alpha1.UpgradeStateValidating, "needsReboot 해소 — 검증으로 복귀", 0)
	}

	// kured executor 는 미구현 상태다(수요 시 추가 예정). 기본 Job 만 실행한다.
	if policy.Spec.RebootExecutor == "kured" {
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RebootExecutorUnsupported",
			"kured executor 는 1차 미구현(후속 과제) — DIP.driver.rebootExecutor=Job 사용")
		return true, 60 * time.Second, nil
	}

	// 무한 재부팅 방지: 증가 후 초과 시 Failed.
	state.Status.RebootAttempts++
	state.Status.RebootRequestedTime = metav1.Now()
	if state.Status.RebootAttempts > maxReboots(policy) {
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RebootLimitExceeded",
			"재부팅 %d회 후에도 needsReboot 지속 — Failed(수동 조치)", state.Status.RebootAttempts-1)
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, "재부팅 한도 초과: 수동 조치 필요", 0)
	}

	// premature-Ready 가드: reboot Job 생성 **이전에** 현재 node.status.nodeInfo.bootID 를
	// DUS status 에 persist 한다(persist-then-Job). 재부팅으로 bootID 가 바뀌므로 handleRebooting 은
	// 이 값과의 차이로 "실제 재부팅 완료"를 판정한다(kubelet 이 재부팅 직전 잠깐 Ready 를 유지하는 레이스 차단).
	// 캡처 실패 시 빈 값을 저장 → handleRebooting 이 기존 isNodeReady 로 보수 전진(교착 방지 fallback).
	var rbNode corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &rbNode); err == nil {
		state.Status.RebootBootID = rbNode.Status.NodeInfo.BootID
	} else {
		logger.Error(err, "bootID 캡처 실패 — 빈 값 fallback(isNodeReady 보수 전진)", "node", state.Spec.NodeName)
		state.Status.RebootBootID = ""
	}
	// persist-then-Job: Job 이 노드를 재부팅하기 전에 bootID 를 반드시 영구 저장.
	// 실패 시 Job 을 만들지 않고 에러 반환 → outer Reconcile 재시도(RebootAttempts 증가분도 미persist 되어 재진입 시 재보정).
	if err := m.Status().Update(ctx, state); err != nil {
		return false, 0, fmt.Errorf("bootID persist 실패(재부팅 전 필수): %w", err)
	}

	// reboot Job 생성(결정론적 이름=de-facto lease, 중복 무해).
	reboot := driverjob.RenderRebootJob(policy, state.Spec.NodeName, policy.Spec.Driver.Image)
	if err := m.Create(ctx, reboot); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, 0, fmt.Errorf("reboot Job 생성 실패: %w", err)
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "RebootTriggered",
		"노드 %s 재부팅 트리거 (attempt %d/%d)", state.Spec.NodeName, state.Status.RebootAttempts, maxReboots(policy))
	logger.Info("노드 재부팅 트리거", "node", state.Spec.NodeName, "attempt", state.Status.RebootAttempts)
	return m.transitionTo(state, v1alpha1.UpgradeStateRebooting, "재부팅 트리거 완료", 30*time.Second)
}

// handleRebooting 은 재부팅 후 노드 Ready 복귀를 관측해 Upgrading 으로 재진입한다(재설치로 모듈 재로드·
// host needs-reboot 마커 소거 → 이후 Validating 재수렴). rebootTimeout 초과 시 Failed.
// 성공 판정은 Job 결과가 아니라 노드 상태(재부팅이 pod 을 죽이므로).
func (m *UpgradeStateMachine) handleRebooting(ctx context.Context, state *v1alpha1.DriverUpgradeState, policy *v1alpha1.DriverInstallPolicy) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)

	timeout := parseDuration("", 15*time.Minute)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.RebootTimeout != "" {
		timeout = parseDuration(policy.Spec.UpgradePolicy.RebootTimeout, 15*time.Minute)
	}
	if !state.Status.RebootRequestedTime.IsZero() && time.Since(state.Status.RebootRequestedTime.Time) > timeout {
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RebootTimeout",
			"재부팅 후 %s 내 Ready+모듈교체 미완 — Failed", timeout)
		return m.transitionTo(state, v1alpha1.UpgradeStateFailed, "재부팅 타임아웃: 수동 조치 필요", 0)
	}

	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &node); err != nil {
		return true, 30 * time.Second, nil // 재부팅 중 API 접근 불가 가능 → 재시도
	}
	if !isNodeReady(&node) {
		return true, 15 * time.Second, nil // 재부팅 중(NotReady) → 대기
	}
	// premature-Ready 가드: bootID 가 재부팅 직전 캡처값과 아직 같으면 실제 재부팅 전(kubelet 이
	// 잠깐 Ready 를 유지하는 레이스) → 전진 보류. bootID 캡처 실패(빈 값)면 기존 isNodeReady 로 보수 전진.
	if state.Status.RebootBootID != "" && node.Status.NodeInfo.BootID == state.Status.RebootBootID {
		logger.Info("노드 Ready 이나 bootID 미변경 — 재부팅 미완료로 판단, 대기",
			"node", state.Spec.NodeName, "bootID", state.Status.RebootBootID)
		return true, 15 * time.Second, nil
	}
	// Ready 복귀 — 재부팅으로 신 모듈이 로드된다. 구 install Job 을 삭제해 재실행을 강제하고 Upgrading
	// 재진입: 재실행 install Job(entrypoint)이 (a) 모듈 재로드 확인 (b) major 일치 시 host
	// needs-reboot 마커 소거를 수행해야 이후 Validating 이 needsReboot=false 로 정상 검증한다.
	// 재부팅만으로는 host 마커를 지울 주체가 install Job 재실행뿐이므로, Validating 로 직행하면
	// needsReboot 가 남아 RebootRequired 재진입→MaxReboots→Failed 로 이어진다.
	jobName := naming.InstallJobName(state.Spec.Vendor, state.Spec.Model, state.Spec.NodeName)
	oldJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: driverjob.Namespace}}
	if err := client.IgnoreNotFound(m.Delete(ctx, oldJob, client.PropagationPolicy(metav1.DeletePropagationBackground))); err != nil {
		logger.Error(err, "구 install Job 삭제 실패(재실행 강제) — 계속", "job", jobName)
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "RebootSucceeded",
		"노드 %s 재부팅 후 Ready — 드라이버 재설치로 모듈 재로드·마커 소거", state.Spec.NodeName)
	logger.Info("재부팅 완료 — Upgrading 재진입(재설치로 마커 소거·재수렴)", "node", state.Spec.NodeName)
	return m.transitionTo(state, v1alpha1.UpgradeStateUpgrading, "재부팅 후 Ready — 재설치로 재수렴", 0)
}

func stateToPhase(state string) string {
	switch state {
	case v1alpha1.UpgradeStatePreFlight:
		return "preflight"
	case v1alpha1.UpgradeStateCordoning:
		return "cordoning"
	case v1alpha1.UpgradeStateDraining:
		return "draining"
	case v1alpha1.UpgradeStateUpgrading:
		return "upgrading"
	case v1alpha1.UpgradeStateValidating:
		return "validating"
	case v1alpha1.UpgradeStateRebootRequired:
		return "rebootrequired"
	case v1alpha1.UpgradeStateRebooting:
		return "rebooting"
	case v1alpha1.UpgradeStateUncordoning:
		return "uncordoning"
	default:
		return ""
	}
}

// activeUpgrades 는 슬롯을 쥔(isActiveState) DriverUpgradeState 의 이름을 정렬해 반환합니다. 캐시 List 는
// 순서가 정해지지 않는다 — 정렬하지 않으면 대기 사유가 pass 마다 바뀌어 이벤트·status 쓰기가 반복된다.
func (m *UpgradeStateMachine) activeUpgrades(ctx context.Context) ([]string, error) {
	var list v1alpha1.DriverUpgradeStateList
	if err := m.List(ctx, &list); err != nil {
		return nil, err
	}
	var names []string
	for _, s := range list.Items {
		if isActiveState(s.Status.State) {
			names = append(names, s.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// noteWait 는 대기 사유를 status.message 에 남기고, 사유가 바뀔 때만 이벤트를 낸다 — 재확인마다
// 같은 이벤트가 쌓이지 않게 한다.
func (m *UpgradeStateMachine) noteWait(state *v1alpha1.DriverUpgradeState, eventType, reason, msg string) {
	if state.Status.Message == msg {
		return
	}
	state.Status.Message = msg
	if m.Recorder != nil {
		m.Recorder.Eventf(state, eventType, reason, "%s", msg)
	}
}

// isActiveState는 업그레이드가 진행 중인 상태(슬롯 점유)인지 반환합니다.
func isActiveState(state string) bool {
	switch state {
	case v1alpha1.UpgradeStatePreFlight,
		v1alpha1.UpgradeStateCordoning,
		v1alpha1.UpgradeStateDraining,
		v1alpha1.UpgradeStateUpgrading,
		v1alpha1.UpgradeStateValidating,
		v1alpha1.UpgradeStateRebootRequired,
		v1alpha1.UpgradeStateRebooting,
		v1alpha1.UpgradeStateUncordoning,
		v1alpha1.UpgradeStateRollback:
		return true
	}
	return false
}

// cordonNode는 노드를 Unschedulable로 설정하고 driver-upgrading 라벨을 추가합니다.
func (m *UpgradeStateMachine) cordonNode(ctx context.Context, nodeName string, state *v1alpha1.DriverUpgradeState) error {
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return err
	}
	base := node.DeepCopy()
	changed := false
	if !node.Spec.Unschedulable {
		node.Spec.Unschedulable = true
		changed = true
		// 소유권 표식은 우리가 직접 false→true 로 뒤집었을 때만 남긴다. 이미 unschedulable 인
		// 노드는 주인이 따로 있고(ACPP quiesce / 운영자 수동), 거기에 우리 이름을 달면
		// handleFailed 가 나중에 남의 cordon 을 우리 것으로 알고 풀어 버린다.
		if node.Annotations == nil {
			node.Annotations = map[string]string{}
		}
		node.Annotations[cordonOwnerAnnotationKey] = state.Name
	}
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	if node.Labels[driverUpgradingLabelKey] != labelValueTrue {
		node.Labels[driverUpgradingLabelKey] = labelValueTrue
		changed = true
	}
	// 좁은 lifecycle 의 차단 라벨도 같이 추가. Validating 진입 시점에 제거되어
	// detector 가 다시 spawn 가능하게 된다.
	if node.Labels[driverUpgradingBlockingLabelKey] != labelValueTrue {
		node.Labels[driverUpgradingBlockingLabelKey] = labelValueTrue
		changed = true
	}
	if !changed {
		return nil
	}
	if err := m.Patch(ctx, &node, client.MergeFrom(base)); err != nil {
		return err
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeUpgradeLabelApplied",
		"노드 %s에 npu.ai/driver-upgrading 및 driver-upgrading-blocking 라벨 추가 (device-plugin 스케줄 차단)", nodeName)
	return nil
}

// uncordonNode는 노드를 Schedulable로 복구하고 driver-upgrading 라벨을 제거합니다.
func (m *UpgradeStateMachine) uncordonNode(ctx context.Context, nodeName string, state *v1alpha1.DriverUpgradeState) error {
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return err
	}
	base := node.DeepCopy()
	changed := false
	if node.Spec.Unschedulable {
		node.Spec.Unschedulable = false
		changed = true
	}
	if _, ok := node.Labels[driverUpgradingLabelKey]; ok {
		delete(node.Labels, driverUpgradingLabelKey)
		changed = true
	}
	if _, ok := node.Labels[driverUpgradingBlockingLabelKey]; ok {
		delete(node.Labels, driverUpgradingBlockingLabelKey)
		changed = true
	}
	// 우리 소유권 표식도 함께 회수한다. 정상 종료로 cordon 이 풀렸는데 표식이 남으면
	// handleFailed 가 나중에 남의(또는 다음 사이클의) cordon 을 우리 것으로 오인한다.
	if node.Annotations[cordonOwnerAnnotationKey] == state.Name {
		delete(node.Annotations, cordonOwnerAnnotationKey)
		changed = true
	}
	if !changed {
		return nil
	}
	if err := m.Patch(ctx, &node, client.MergeFrom(base)); err != nil {
		return err
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeUpgradeLabelRemoved",
		"노드 %s에서 npu.ai/driver-upgrading + driver-upgrading-blocking 라벨 모두 제거 (device-plugin 재스케줄 허용)", nodeName)
	return nil
}

// settleIdle 은 사이클을 열지 않고 Idle 에 머무는 경로의 공통 마무리다. 직전 사이클이 cordon 한 채
// Failed 로 끝난 노드의 자기 cordon 을 풀고, state=="" 인 새 DUS 는 목표 버전을 채워 Idle 로 1회 정규화한다.
// 이미 Idle 이면 재기록하지 않아 LastTransitionTime/IdleCooldown churn 을 피한다.
func (m *UpgradeStateMachine) settleIdle(ctx context.Context, state *v1alpha1.DriverUpgradeState, desiredVersion, reason string) (bool, time.Duration, error) {
	if err := m.releaseOwnCordon(ctx, state); err != nil {
		return false, 0, fmt.Errorf("idle 복귀 중 자기 cordon 해제 실패: %w", err)
	}
	if state.Status.State == "" {
		state.Status.DesiredVersion = desiredVersion
		return m.transitionTo(state, v1alpha1.UpgradeStateIdle, reason, 60*time.Second)
	}
	return true, 60 * time.Second, nil
}

// releaseOwnCordon 은 이 DUS 가 건 cordon 이 노드에 남아 있으면 푼다(handleFailed 복구 탈출과
// handleIdle 이 공유). 소유 표식이 없거나 남의 이름이면 그 cordon 은 남겨 둔다. 노드가 없으면 할 일이 없다.
func (m *UpgradeStateMachine) releaseOwnCordon(ctx context.Context, state *v1alpha1.DriverUpgradeState) error {
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}
	if err := m.reapStaleCordonClaim(ctx, &node, state); err != nil {
		return fmt.Errorf("stale cordon claim 정리 실패: %w", err)
	}
	if node.Spec.Unschedulable && node.Annotations[cordonOwnerAnnotationKey] == state.Name {
		return m.uncordonNode(ctx, state.Spec.NodeName, state)
	}
	return nil
}

// reapStaleCordonClaim 은 우리가 노드의 unschedulable==false 를 관측했는데 우리 소유 annotation
// 이 남아 있으면 그 annotation 을 지운다. 사람이 uncordon 한 뒤 stale 로 남아 이후 남의 cordon 을
// 오인 해제하는 것을 막는다(마커 수명은 cordon 수명과 같아야 한다는 불변식 유지).
func (m *UpgradeStateMachine) reapStaleCordonClaim(ctx context.Context, node *corev1.Node, state *v1alpha1.DriverUpgradeState) error {
	if node.Spec.Unschedulable || node.Annotations[cordonOwnerAnnotationKey] != state.Name {
		return nil
	}
	patch := client.MergeFrom(node.DeepCopy())
	delete(node.Annotations, cordonOwnerAnnotationKey)
	return m.Patch(ctx, node, patch)
}

// clearUpgradingLabel은 Failed 전이 시 device-plugin 재스케줄이 가능하도록
// driver-upgrading 라벨과 driver-upgrading-blocking 라벨을 모두 제거한다.
// unschedulable은 수동 조치를 위해 보존.
func (m *UpgradeStateMachine) clearUpgradingLabel(ctx context.Context, nodeName string, state *v1alpha1.DriverUpgradeState) error {
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
		return err
	}
	_, hasMain := node.Labels[driverUpgradingLabelKey]
	_, hasBlocking := node.Labels[driverUpgradingBlockingLabelKey]
	if !hasMain && !hasBlocking {
		return nil
	}
	base := node.DeepCopy()
	if hasMain {
		delete(node.Labels, driverUpgradingLabelKey)
	}
	if hasBlocking {
		delete(node.Labels, driverUpgradingBlockingLabelKey)
	}
	if err := m.Patch(ctx, &node, client.MergeFrom(base)); err != nil {
		return err
	}
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "NodeUpgradeLabelRemoved",
		"노드 %s에서 driver-upgrading + driver-upgrading-blocking 라벨 제거 (device-plugin 재스케줄 허용)", nodeName)
	return nil
}

// acceleratorDomains 는 가속기 extended resource 의 도메인 접두다. 리소스명마다 다시
// 유도하지 않도록 한 번만 만든다 — uninstall 게이트(internal/uninstall/gate.go)가 쓰는
// 패턴과 같다.
var acceleratorDomains = intent.AcceleratorResourceDomains()

// hasAcceleratorResourceName 은 리소스명이 등록된 벤더의 가속기 extended resource 도메인 밑에
// 있는지를 본다. intent.AcceleratorResourceDomains 를 그대로 써서 uninstall 게이트
// (internal/uninstall/gate.go)와 같은 fail-closed 판정을 공유한다. hasDeviceWorkloads·
// podUsesDevice 가 이 판정을 따로 복제하면, 벤더 도메인이 하나라도 빠질 때 그 벤더의 파드가
// drain 대상에서 빠져나간다.
func hasAcceleratorResourceName(rn string) bool {
	for _, domain := range acceleratorDomains {
		if strings.Contains(rn, domain) {
			return true
		}
	}
	return false
}

// hasDeviceWorkloads는 노드에 GPU/NPU 장치를 쥔 Pod가 남아 있는지 확인합니다.
// 판정은 podUsesDevice 하나에 모아 둔다 — eviction 대상 선정(evictDevicePods)과 같은
// 집합을 봐야 하기 때문이다. 양쪽이 갈리면 drain 이 끝나지 않는다.
func (m *UpgradeStateMachine) hasDeviceWorkloads(ctx context.Context, nodeName string) (bool, error) {
	var podList corev1.PodList
	if err := m.List(ctx, &podList); err != nil {
		return false, err
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName || !podPhaseDrainable(pod) {
			continue
		}
		if podUsesDevice(pod) {
			return true, nil
		}
	}
	return false, nil
}

// evictDevicePods는 노드에서 GPU/NPU 리소스를 사용하는 Pod를 PDB를 존중하며 축출합니다.
// policy/v1 Eviction API를 사용하므로 API server가 PodDisruptionBudget을 자동 검사합니다.
// 에러 처리: 429(PDB 위반)는 force=false면 다음 주기 재시도, force=true면 Delete로 폴백
// (kubectl drain --force 동등). 404(이미 삭제됨)는 성공 처리. 기타 에러는 상위로 전파.
func (m *UpgradeStateMachine) evictDevicePods(ctx context.Context, state *v1alpha1.DriverUpgradeState, force bool) error {
	logger := logf.FromContext(ctx)
	var podList corev1.PodList
	if err := m.List(ctx, &podList); err != nil {
		return err
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != state.Spec.NodeName || !podPhaseDrainable(pod) {
			continue
		}
		if !podUsesDevice(pod) {
			continue
		}

		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}
		err := m.SubResource("eviction").Create(ctx, pod, eviction)
		if err == nil || apierrors.IsNotFound(err) {
			// 성공 또는 이미 삭제됨
			continue
		}
		if apierrors.IsTooManyRequests(err) {
			// PDB 위반. force면 Delete로 폴백, 아니면 다음 주기에 재시도.
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "EvictionBlocked",
				"PDB로 인해 Pod %s/%s eviction 차단 (force=%v)", pod.Namespace, pod.Name, force)
			if !force {
				logger.Info("PDB 위반으로 eviction 지연, 재시도 예정",
					"pod", pod.Name, "namespace", pod.Namespace)
				continue
			}
			logger.Info("ForceUpgrade=true, PDB 무시하고 Delete 폴백",
				"pod", pod.Name, "namespace", pod.Namespace)
			if delErr := m.Delete(ctx, pod); client.IgnoreNotFound(delErr) != nil {
				return fmt.Errorf("force delete 실패 (pod=%s/%s): %w", pod.Namespace, pod.Name, delErr)
			}
			continue
		}
		// 기타 에러는 상위로 전파
		return fmt.Errorf("eviction 실패 (pod=%s/%s): %w", pod.Namespace, pod.Name, err)
	}
	return nil
}

// podPhaseDrainable 은 drain 판정이 세는 단계인지다 — 종료 단계(Succeeded/Failed)만 제외한다.
// Pending 을 빼면 스케줄만 된 장치 요청 Pod 가 커널 모듈 교체 도중에 시작된다.
// hasDeviceWorkloads 와 evictDevicePods 가 이 함수를 함께 써서 같은 집합을 본다.
func podPhaseDrainable(pod *corev1.Pod) bool {
	return pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

// podUsesDevice는 Pod가 GPU/NPU 장치를 쥐고 있는지 반환합니다 — device-plugin 축(extended
// resource limits, containers + initContainers)과 DRA 축(pod.spec.resourceClaims)을 모두 봅니다.
// DRA 로 뜬 Pod 은 resources.limits 가 비어 있어(장치를 resourceClaims 로 쥐기 때문) limits 만
// 보면 drain 대상에서 빠지고 그 위에서 커널 모듈이 교체된다. claim 을 DeviceClass→driver 로
// 되짚어 벤더를 가리지는 않는다 — 이 판정은 안전 게이트라 과하게 잡는 쪽이 옳다(모자라면 장치가
// 밑에서 바뀐다).
func podUsesDevice(pod *corev1.Pod) bool {
	if len(pod.Spec.ResourceClaims) > 0 {
		return true
	}
	for _, ctrs := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, c := range ctrs {
			for resName := range c.Resources.Limits {
				if hasAcceleratorResourceName(string(resName)) {
					return true
				}
			}
		}
	}
	return false
}

// deleteDevicePluginPods는 해당 노드의 device-plugin Pod를 삭제합니다.
// device-plugin은 GPU 리소스를 요청하지 않지만 /dev/nvidia*를 직접 마운트하여
// 커널 모듈 참조를 잡으므로, drain 시 삭제해야 rmmod가 가능합니다.
func (m *UpgradeStateMachine) deleteDevicePluginPods(ctx context.Context, nodeName string) error {
	// device-plugin 은 벤더별로 다른 네임스페이스에 상주한다(3rd-party=kube-system,
	// TT=kcloud). 하드코딩 대신 전 네임스페이스 조회 후 nodeName+이름/라벨로 필터.
	var podList corev1.PodList
	if err := m.List(ctx, &podList); err != nil {
		return err
	}
	logger := logf.FromContext(ctx)
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		isDevicePlugin := false
		for key, val := range pod.Labels {
			if key == "app.kubernetes.io/name" && strings.Contains(val, "device-plugin") {
				isDevicePlugin = true
				break
			}
		}
		if !isDevicePlugin && strings.Contains(pod.Name, "device-plugin") {
			isDevicePlugin = true
		}
		if isDevicePlugin {
			logger.Info("device-plugin Pod 삭제", "pod", pod.Name, "node", nodeName)
			if err := m.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
				return err
			}
		}
	}
	return nil
}

// deleteDriverPodOnNode는 해당 노드의 드라이버 DaemonSet Pod를 삭제합니다 (OnDelete 전략 트리거).
func (m *UpgradeStateMachine) deleteDriverPodOnNode(ctx context.Context, dsName string, nodeName string) error {
	var podList corev1.PodList
	if err := m.List(ctx, &podList, client.InNamespace(naming.OperatorNamespace())); err != nil {
		return err
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		// DaemonSet 소유자 확인
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == "DaemonSet" && ref.Name == dsName {
				if err := m.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// isDriverPodReadyOnNode는 해당 노드의 드라이버 Pod가 ContainersReady인지 확인합니다.
// desiredImage가 지정된 경우 Pod의 컨테이너 이미지가 일치하는지도 검증합니다.
// 이전 버전 Pod의 Ready 상태를 새 버전으로 오판하는 것을 방지합니다.
// 반환값: (ready bool, crashLoop bool, err error)
func (m *UpgradeStateMachine) isDriverPodReadyOnNode(ctx context.Context, dsName string, nodeName string, desiredImage ...string) (bool, bool, error) {
	var podList corev1.PodList
	if err := m.List(ctx, &podList, client.InNamespace(naming.OperatorNamespace())); err != nil {
		return false, false, err
	}
	logger := logf.FromContext(ctx)
	for _, pod := range podList.Items {
		if pod.Spec.NodeName != nodeName {
			continue
		}
		// Terminating Pod 스킵: 이전 Pod와 새 Pod 공존 구간에서 오판 방지
		if pod.DeletionTimestamp != nil {
			continue
		}
		isDSPod := false
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == "DaemonSet" && ref.Name == dsName {
				isDSPod = true
				break
			}
		}
		if !isDSPod {
			continue
		}

		// 이미지 검증: desiredImage가 지정된 경우 Pod 이미지가 일치하는지 확인
		if len(desiredImage) > 0 && desiredImage[0] != "" {
			podImage := ""
			if len(pod.Spec.Containers) > 0 {
				podImage = pod.Spec.Containers[0].Image
			}
			if podImage != desiredImage[0] {
				logger.Info("이전 버전 Pod 감지, 새 Pod 대기 중",
					"pod", pod.Name, "podImage", podImage, "desiredImage", desiredImage[0])
				return false, false, nil
			}
		}

		// CrashLoopBackOff 확인
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				return false, true, nil
			}
		}

		// ContainersReady 확인
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.ContainersReady && cond.Status == corev1.ConditionTrue {
				return true, false, nil
			}
		}
		return false, false, nil
	}
	return false, false, nil
}

// isNodeReady는 노드가 Ready 상태인지 확인합니다.
func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// 커널 이미지를 쓰지 않았을 때 그 사유를 드러낼 이벤트 reason. 둘을 나눈 이유는 상황이 다르기
// 때문이다 — 하나는 이미지 참조가 있는데 검증에서 거절된 것이고, 하나는 참조를 만들지조차 못한
// 것이다. 같은 reason 으로 내면 운영자가 "거절된 이미지" 를 찾아 헤맨다.
const (
	eventKernelImageRejected     = "KernelImageRejected"
	eventKernelImageNotAssembled = "KernelImageNotAssembled"
	// eventKernelImageMissing 은 정책이 kernelImages 를 선언했는데 노드 커널 항목이 없어 기본
	// 이미지(DKMS)로 되돌아갈 때 낸다. 커널이 OS 업데이트로 바뀐 뒤 조용히 느린 경로로 가는 것을 드러낸다.
	eventKernelImageMissing = "KernelImageMissing"
	// eventPolicySharedOwnership 은 DIP spec 을 제3 관리자가 함께 소유할 때 낸다 — 다음 helm upgrade 가
	// Server-Side Apply 충돌로 멈출 수 있다.
	eventPolicySharedOwnership = "PolicySharedOwnership"
)

// assembleKernelImage 는 태그 규약으로 노드 커널용 사전 컴파일 이미지 참조를 조립한다. 조립 조건이
// 서지 않으면 빈 문자열을 돌려준다. 두 번째 반환값은 조립을 포기한 이유이며, 애초에 조립할 것이
// 없으면 빈 문자열이다 — 알릴 것이 없는데 경고를 내면 PreFlight 마다 같은 경고가 반복된다.
// 조립을 켜는 것은 spec.driver.kernelImageBuildRevision 이다. 버전 토큰은 spec.driver.version,
// variant 토큰은 spec.driver.kernelImageVariant, 커널 토큰은 노드가 보고한 커널, 저장소 경로는
// spec.driver.image 에서 온다. variant 유무는 벤더마다 달라 차트가 채우며 여기서는 분기하지 않는다.
func assembleKernelImage(policy *v1alpha1.DriverInstallPolicy, kernelVersion string) (image string, skipReason string) {
	build := policy.Spec.Driver.KernelImageBuildRevision
	version := policy.Spec.Driver.Version
	// 버전 토큰 없이 조립하면 "-k<커널>-vN" 이 되어 규약을 벗어난다. 버전을 비우는 것은 설치기가
	// 버전을 고르는 하위호환 경로이고 그 경로에는 사전 컴파일 이미지가 없으므로, 조립할 것이 없는
	// 상태로 본다(CRD CEL 이 이 조합을 입구에서 거절하므로 여기는 이중 방어다).
	if build == "" || version == "" || policy.Spec.Driver.Image == "" {
		return "", ""
	}
	// 아래 세 토막은 그대로 image reference 의 태그로 들어간다. 태그에 쓸 수 없는 문자가 섞이면
	// Job 에 실을 수 없는 참조가 만들어지므로 조립하지 않는다. 커널은 노드가 보고한 값이고(자체
	// 빌드 커널의 localversion 접미사가 그렇다) 버전은 운영자가 적는 값이다 — 둘 다 본다.
	// 그런 노드·정책은 kernelImages 로 따로 지정한다.
	notAssembled := func(what string) (string, string) {
		return "", fmt.Sprintf(
			"커널 %s 의 사전 컴파일 이미지를 조립하지 못했다: %s에 이미지 태그로 쓸 수 없는 문자가 있다",
			kernelVersion, what)
	}
	if !tagSafeTokenRe.MatchString(kernelVersion) {
		return notAssembled("커널 문자열")
	}
	if !tagSafeTokenRe.MatchString(version) {
		return notAssembled("spec.driver.version")
	}
	tag := version
	if variant := policy.Spec.Driver.KernelImageVariant; variant != "" {
		tag += "-" + variant
	}
	return replaceImageTag(policy.Spec.Driver.Image, tag+"-k"+kernelVersion+"-"+build), ""
}

// tagSafeTokenRe 는 조립에 넣을 토막을 image reference 의 태그 안에 그대로 써도 되는지 본다.
// docker 태그가 받는 문자(영숫자·점·밑줄·하이픈)만 허용한다.
var tagSafeTokenRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// resolveDriverImage 는 노드 커널에 맞는 사전 컴파일 이미지(정책 선언 또는 태그 규약으로 조립)가
// 있으면 그것을, 아니면 기본 설치 이미지를 돌려준다. 둘째 반환값은 그 이미지를 쓰지 않은 이유
// (썼거나 애초에 없으면 빈 문자열), 셋째는 그 이유를 드러낼 이벤트 reason 이다. 무시하는 경우는
// 둘: 태그가 검증 빌드 태그(…-vN)가 아니거나, 태그의 버전 토큰이 spec.driver.version 과 달라
// 사전 컴파일 모듈·사용자공간과 어긋날 때다. spec.driver.version 이 비면 대조 기준이 없어 이
// 버전 검사를 건너뛴다 — 그 경로는 설치기가 버전을 고르는 하위호환 경로다.
func resolveDriverImage(
	policy *v1alpha1.DriverInstallPolicy, kernelVersion string,
) (image string, skipReason string, skipEventReason string) {
	if kernelVersion == "" {
		return policy.Spec.Driver.Image, "", ""
	}
	// 맵에 적힌 항목이 조립보다 우선한다 — 규약을 벗어난 이미지의 예외 지정 수단이다.
	ref := ""
	origin := "맵에 선언한"
	if img, ok := policy.Spec.Driver.KernelImages[kernelVersion]; ok {
		ref = string(img)
	} else {
		assembled, assembleSkip := assembleKernelImage(policy, kernelVersion)
		if assembled == "" {
			if assembleSkip == "" {
				if len(policy.Spec.Driver.KernelImages) == 0 {
					return policy.Spec.Driver.Image, "", ""
				}
				keys := make([]string, 0, len(policy.Spec.Driver.KernelImages))
				for k := range policy.Spec.Driver.KernelImages {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				return policy.Spec.Driver.Image, fmt.Sprintf(
					"커널 %s 의 사전 컴파일 이미지가 kernelImages 에 없어 기본 이미지(DKMS 컴파일)로 되돌아간다 — 선언된 커널: %s",
					kernelVersion, strings.Join(keys, ", ")), eventKernelImageMissing
			}
			return policy.Spec.Driver.Image, assembleSkip, eventKernelImageNotAssembled
		}
		ref, origin = assembled, "규약으로 조립한"
	}
	if !isVerifiedBuildTag(ref) {
		return policy.Spec.Driver.Image, fmt.Sprintf(
			"커널 %s 의 %s 사전 컴파일 이미지 %q 가 검증 빌드 태그(…-vN)가 아니어서 쓰지 않는다",
			kernelVersion, origin, ref), eventKernelImageRejected
	}
	if want := policy.Spec.Driver.Version; want != "" {
		if got := extractKernelImageVersion(ref); got != want {
			return policy.Spec.Driver.Image, fmt.Sprintf(
				"커널 %s 의 %s 사전 컴파일 이미지 버전 토큰 %q 가 spec.driver.version %q 와 달라서 쓰지 않는다",
				kernelVersion, origin, got, want), eventKernelImageRejected
		}
	}
	return ref, "", ""
}

// ownSpecManagerPrefixes 는 경고에서 제외하는 관리자 이름 접두 목록이다 — helm 과 제품 자신의
// 제어 경로. pkg/npuctl.UpgradeVendor 는 FieldManager `kcloud-operator-upgrade` 를 명시한다.
// `manager`·`kubectl-npu` 는 FieldManager 를 명시하기 전 API 서버가 User-Agent 에서 이름을
// 유도했을 때 남은 과거 항목이라, 라이브 managedFields 에 그 이름이 더는 없을 때 접두 목록에서
// 제거한다. `manager` 는 kubebuilder 기본 바이너리 이름이라 같은 이름의 제3 컨트롤러도 함께
// 빠진다 — 자기 제어 경로를 "외부 개입"으로 잘못 지목하는 쪽이 더 나쁜 오류이므로 그 방향을 택한다.
var ownSpecManagerPrefixes = []string{"helm", "kubectl-npu", "kcloud-operator", "manager"}

// isOwnSpecManager 는 관리자 이름이 제외 접두 중 하나로 시작하는지 본다.
func isOwnSpecManager(name string) bool {
	for _, p := range ownSpecManagerPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// foreignSpecManagers 는 spec 을 쥔 제3 field manager 목록이다(정렬·중복 제거). 둘이 같은 spec
// 필드를 쥐면 helm 이 값을 바꾸는 다음 upgrade 가 충돌한다. kubectl-label·kubectl-annotate 처럼
// metadata 만 쥔 관리자는 세지 않는다.
// Operation 은 가리지 않는다: helm 은 운용 환경에 따라 Apply 로도 Update 로도 남고 둘 다 이름으로
// 빠진다. 반대로 SSA 로 소유하는 ArgoCD·Flux·`helm --server-side` 는 Apply 만 쓴다 — Operation 을
// Update 로 한정하면 다음 helm upgrade 를 실제로 멈추는 이들을 놓친다.
func foreignSpecManagers(entries []metav1.ManagedFieldsEntry) []string {
	seen := map[string]bool{}
	for _, e := range entries {
		if isOwnSpecManager(e.Manager) || e.FieldsV1 == nil {
			continue
		}
		if strings.Contains(string(e.FieldsV1.Raw), `"f:spec"`) {
			seen[e.Manager] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// refreshResolvedImage 는 status.ResolvedImage 를 지금 정책 기준으로 다시 계산한다. PreFlight
// 에서만 계산하면 정책에서 kernelImages 항목을 뺀 뒤에도 옛 태그가 status 에 남아 운영자가 그
// 이미지를 아직 쓰는 것으로 오해한다. 노드 조회는 controller-runtime 캐시를 쓰므로 API 호출이
// 늘지 않는다. 노드를 찾지 못하면 값을 그대로 둔다 — 마지막 관측값을 지우는 것은 얻는 것이 없다.
// 이미지를 쓰지 않은 사유는 여기서 버린다. 경고 이벤트는 실제 설치가 일어나는 PreFlight 에서만
// 낸다(Idle 은 60초마다 재진입해 같은 경고가 반복된다).
func (m *UpgradeStateMachine) refreshResolvedImage(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) error {
	var node corev1.Node
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("노드 조회 실패: %w", err)
	}
	image, _, _ := resolveDriverImage(policy, node.Status.NodeInfo.KernelVersion)
	state.Status.ResolvedImage = image
	return nil
}

// matchesKernelAllowlist는 커널 버전이 allowlist 패턴 중 하나와 일치하는지 확인합니다.
// 패턴에서 "*"는 정규식 ".*"로 변환됩니다.
func matchesKernelAllowlist(kernelVersion string, allowlist []string) bool {
	for _, pattern := range allowlist {
		// glob 스타일 → 정규식
		regexStr := "^" + regexp.QuoteMeta(pattern) + "$"
		regexStr = strings.ReplaceAll(regexStr, `\*`, `.*`)
		if matched, err := regexp.MatchString(regexStr, kernelVersion); err == nil && matched {
			return true
		}
	}
	return false
}

// parseDuration은 문자열을 time.Duration으로 파싱합니다. 실패 시 defaultDur 반환.
func parseDuration(s string, defaultDur time.Duration) time.Duration {
	if s == "" {
		return defaultDur
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultDur
	}
	return d
}

// replaceImageTag는 이미지 문자열의 태그를 newTag로 교체합니다.
// 예: "registry/img:oldTag" → "registry/img:newTag"
// imageVariantSuffixRe 는 verified build tag 의 variant 접미사를 매치한다.
// 예: "580.142-v172" → "-v172", "1.7.8-v2" → "-v2".
var imageVariantSuffixRe = regexp.MustCompile(`-v[0-9]+(\.[0-9]+)?$`)

// extractImageVariantSuffix 는 image reference 의 tag 부분에서 variant 접미사를 추출한다.
// 매치되지 않으면 빈 문자열 반환.
func extractImageVariantSuffix(image string) string {
	idx := strings.LastIndex(image, ":")
	if idx == -1 {
		return ""
	}
	return imageVariantSuffixRe.FindString(image[idx+1:])
}

// kernelImageVersionRe 는 커널 이미지 태그 앞머리의 드라이버 버전 토큰을 매치한다.
// 태그 규약은 "<version>[-<variant>]-k<kernel>-vN" 이며, version 은 숫자로 시작하는 점 구분
// 버전이다. 하위 번호가 있으면 "-<숫자>" 하나까지 포함한다(예 "1.9.8-3"). variant(예 "rngd")
// 와 커널·빌드 접미사는 숫자로 시작하지 않으므로 여기에 걸리지 않는다. variant 쪽 전제는
// CRD 패턴이 강제한다 — kernelImageVariant 는 영문자로 시작해야 한다.
var kernelImageVersionRe = regexp.MustCompile(`^[0-9][0-9.]*(-[0-9]+)?`)

// extractKernelImageVersion 은 image reference 태그 앞머리의 드라이버 버전 토큰만 뽑는다.
// variant·커널·빌드 접미사는 버린다. 예: "…:2026.3.1-rngd-k6.8.0-138-generic-v1" → "2026.3.1".
// 태그가 없거나 버전 토큰이 없으면 빈 문자열을 돌려준다.
func extractKernelImageVersion(image string) string {
	idx := strings.LastIndex(image, ":")
	if idx == -1 {
		return ""
	}
	return kernelImageVersionRe.FindString(image[idx+1:])
}

// precompiledKernelImageRe 는 사전 컴파일 커널 이미지 태그의 커널 토큰(-k<커널버전>)을 매치한다.
// 태그 규약 "<version>[-<variant>]-k<kernel>-vN" 에서 커널 버전은 숫자로 시작하므로 "-k<숫자>"
// 로 식별한다. 버전 비종속 태그(예 580.159.03-v179)에는 이 토큰이 없다.
var precompiledKernelImageRe = regexp.MustCompile(`-k[0-9]`)

// isPrecompiledKernelImage 는 image reference 의 태그가 사전 컴파일 커널 이미지인지 반환한다.
// 커널 이미지는 특정 드라이버 버전의 모듈을 담아 그 버전 전용이다 — DRIVER_VERSION 만 바꿔서는
// 다른 버전을 설치하지 못한다. 되돌리기·재구성 경로가 이 판별로 버전 종속 이미지를 거른다.
func isPrecompiledKernelImage(image string) bool {
	idx := strings.LastIndex(image, ":")
	if idx == -1 {
		return false
	}
	return precompiledKernelImageRe.MatchString(image[idx+1:])
}

// replaceImageTag 는 image reference 의 tag 만 newTag 로 치환한다.
// newTag 가 plain (variant 없음) 이고 기존 tag 에 variant 가 있으면, variant 를 보존하여
// rollback 시 broken plain tag (예: ":580.142") 가 생성되는 것을 차단한다.
func replaceImageTag(image string, newTag string) string {
	idx := strings.LastIndex(image, ":")
	if idx == -1 {
		return image + ":" + newTag
	}
	// newTag 가 이미 variant 포함 (예: "580.142-v172") 이면 그대로 사용
	if !strings.Contains(newTag, "-v") {
		if v := imageVariantSuffixRe.FindString(image[idx+1:]); v != "" {
			newTag = newTag + v
		}
	}
	return image[:idx+1] + newTag
}

// verifiedBuildTagRegexp 는 검증된 빌드 태그 패턴을 정의한다.
// 허용: "vN"(v1, v16), ".+-vN"(1.9.8-3-v2, 580.126.09-v16, 1.7.8-v3), "latest"(개발 환경).
// 거부: plain semver(580.126.09, 1.9.8-3, 590.48.01) — broken plain tag 가 rollback 시
// PreviousImage 로 저장되어 rollback 발동 시 broken Pod 으로 회귀하는 것을 막는 defense-in-depth.
var verifiedBuildTagRegexp = regexp.MustCompile(`:(v[0-9]+|.+-v[0-9]+|latest)$`)

// isVerifiedBuildTag 는 image reference 의 태그가 검증된 빌드 태그인지 확인한다.
// "<registry>/<repo>:<tag>" 형태의 image reference 에서 tag 부분을 추출해 검사.
// tag 가 없는 reference (e.g., "alpine") 는 false 반환.
func isVerifiedBuildTag(image string) bool {
	idx := strings.LastIndex(image, ":")
	if idx == -1 {
		return false
	}
	return verifiedBuildTagRegexp.MatchString(image[idx:])
}

// EnsureUpgradingLabelRemoved 는 노드에서 npu.ai/driver-upgrading 라벨을 idempotent 하게 제거한다.
// controller 의 defer cleanup / stuck-label sweep 가 호출하는 외부 진입점이다. 라벨이 없으면
// no-op(API 호출 0), 있으면 MergeFrom patch 로 제거하고 Conflict 시 최대 3회 재시도한다.
// reconcile context cancel 과 무관하게 cleanup 이 진행되도록 호출자가 별도 ctx 를 주입할 책임이 있다.
func (m *UpgradeStateMachine) EnsureUpgradingLabelRemoved(ctx context.Context, nodeName string) error {
	return m.ensureLabelRemoved(ctx, nodeName, driverUpgradingLabelKey)
}

// EnsureUpgradingBlockingLabelRemoved 는 노드에서 driver-upgrading-blocking 라벨을 idempotent
// 하게 제거한다. controller 의 stuck-label sweep 가 좁은 lifecycle 의 라벨을 빨리 정리하기 위해 호출.
func (m *UpgradeStateMachine) EnsureUpgradingBlockingLabelRemoved(ctx context.Context, nodeName string) error {
	return m.ensureLabelRemoved(ctx, nodeName, driverUpgradingBlockingLabelKey)
}

// ensureLabelRemoved 는 지정한 키의 라벨을 idempotent 하게 제거한다.
// Conflict 시 최대 3회 재시도. 라벨 미존재 시 no-op (API patch 호출 0).
func (m *UpgradeStateMachine) ensureLabelRemoved(ctx context.Context, nodeName, labelKey string) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		var node corev1.Node
		if err := m.Get(ctx, types.NamespacedName{Name: nodeName}, &node); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if _, ok := node.Labels[labelKey]; !ok {
			return nil
		}
		base := node.DeepCopy()
		delete(node.Labels, labelKey)
		if err := m.Patch(ctx, &node, client.MergeFrom(base)); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				continue
			}
			return err
		}
		return nil
	}
	return fmt.Errorf("%d회 conflict 재시도 후 라벨 %s 제거 실패: %w", maxRetries, labelKey, lastErr)
}

// ─────────────────────────────────────────────
// Quiesce-on-Driver-Upgrade (opt-in label)
// ─────────────────────────────────────────────

// QuiesceLabeledDeployments 는 cordon 직후 호출되어 노드별로 quiesce-on-driver-upgrade 라벨이
// 붙은 Deployment 를 spec.replicas=0 으로 patch 하고, 원래 replicas 를 state.Status.
// QuiescedDeployments backup 에 기록한다. 매칭 기준(deploymentTargetsNode)은 PodTemplate 의
// nodeSelector["kubernetes.io/hostname"] 또는 nodeAffinity required term 의 hostname In 매칭
// 뿐이며, 다른 라벨 기반 selector 는 범위 밖이다. quiesce 실패는 best-effort 로 사이클 진행을
// 막지 않는다.
func (m *UpgradeStateMachine) QuiesceLabeledDeployments(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) error {
	logger := logf.FromContext(ctx)

	var deploys appsv1.DeploymentList
	if err := m.List(ctx, &deploys, client.MatchingLabels{
		quiesceOnDriverUpgradeLabelKey: labelValueTrue,
	}); err != nil {
		return fmt.Errorf("quiesce 후보 Deployment list 실패: %w", err)
	}

	quiesced := make([]v1alpha1.QuiescedDeployment, 0, len(deploys.Items))
	for i := range deploys.Items {
		d := &deploys.Items[i]
		if !deploymentTargetsNode(d, state.Spec.NodeName) {
			continue
		}
		if d.Spec.Replicas == nil || *d.Spec.Replicas == 0 {
			// 이미 0 (다른 controller 가 scale 한 상태) — 복구 대상 아님.
			continue
		}
		original := *d.Spec.Replicas
		base := d.DeepCopy()
		// annotation 으로도 backup 을 이중 기록한다 — operator restart 등으로 DUS.Status 를
		// 잃어도 annotation 으로 fallback 복구할 수 있다.
		if d.Annotations == nil {
			d.Annotations = make(map[string]string)
		}
		d.Annotations[QuiesceReplicasBackupAnnotation] = strconv.Itoa(int(original))
		zero := int32(0)
		d.Spec.Replicas = &zero
		if err := m.Patch(ctx, d, client.MergeFrom(base)); err != nil {
			logger.Error(err, "Deployment quiesce 실패",
				"deploy", d.Name, "namespace", d.Namespace, "node", state.Spec.NodeName)
			continue
		}
		quiesced = append(quiesced, v1alpha1.QuiescedDeployment{
			Namespace:        d.Namespace,
			Name:             d.Name,
			OriginalReplicas: original,
		})
		logger.Info("Deployment quiesced for driver upgrade",
			"deploy", d.Name, "namespace", d.Namespace, "from", original, "to", 0,
			"node", state.Spec.NodeName)
		if m.Recorder != nil {
			m.Recorder.Eventf(state, corev1.EventTypeNormal, "DeploymentQuiesced",
				"Deployment %s/%s scaled %d → 0 for driver upgrade (node=%s)",
				d.Namespace, d.Name, original, state.Spec.NodeName)
		}
	}
	state.Status.QuiescedDeployments = quiesced

	// status backup 을 즉시 영구 저장한다 — handleCordoning 이 다음 상태(Draining)로 넘어가기
	// 전에 commit 해야 operator restart 내구성이 보장된다. 외부 Reconcile 의 최종 Status.Update
	// 가 bumped ResourceVersion 을 받아도 conflict 시 requeue 로 처리되므로 race 안전하다.
	if err := m.Status().Update(ctx, state); err != nil {
		logger.Error(err, "quiesce status backup 영구 저장 실패",
			"node", state.Spec.NodeName, "count", len(quiesced))
		return fmt.Errorf("failed to persist quiesce status backup: %w", err)
	}
	return nil
}

// RestoreQuiescedDeployments 는 cycle 종료(Idle/Failed) 시 호출되어 backup 의 OriginalReplicas 로
// Deployment.spec.replicas 를 복구하고 backup 을 비운다. backup 이 비어 있으면(operator restart 로
// status 를 잃은 경우) annotation fallback 으로 복구를 시도한다. Deployment NotFound(이미 삭제됨)
// 는 이벤트만 발행하고 넘어가고, 개별 patch 실패는 로그·이벤트로 보고한 뒤 다른 entry 는 계속
// 처리한다. 모든 entry 처리 후 backup 을 nil 로 비워 다음 cycle 에 누수되지 않게 한다.
func (m *UpgradeStateMachine) RestoreQuiescedDeployments(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) error {
	logger := logf.FromContext(ctx)
	if len(state.Status.QuiescedDeployments) == 0 {
		// status 손실 시 annotation 기반 fallback 복구를 시도한다.
		// 정상 흐름에서는 backup 이 채워져 있으므로 no-op 으로 끝난다.
		return m.restoreFromAnnotationFallback(ctx, state)
	}
	for _, q := range state.Status.QuiescedDeployments {
		var d appsv1.Deployment
		if err := m.Get(ctx, types.NamespacedName{Namespace: q.Namespace, Name: q.Name}, &d); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("restore: deployment 미발견 (이미 삭제됨)",
					"deploy", q.Name, "namespace", q.Namespace)
				if m.Recorder != nil {
					m.Recorder.Eventf(state, corev1.EventTypeWarning, "DeploymentRestoreSkipped",
						"Deployment %s/%s 미발견 (이미 삭제됨) — restore skip",
						q.Namespace, q.Name)
				}
				continue
			}
			logger.Error(err, "restore: deployment Get 실패",
				"deploy", q.Name, "namespace", q.Namespace)
			continue
		}
		base := d.DeepCopy()
		original := q.OriginalReplicas
		d.Spec.Replicas = &original
		// restore 와 동시에 backup annotation 을 제거해 다음 cycle 의 fallback 이 stale 값을
		// 쓰지 않게 한다.
		if d.Annotations != nil {
			delete(d.Annotations, QuiesceReplicasBackupAnnotation)
		}
		if err := m.Patch(ctx, &d, client.MergeFrom(base)); err != nil {
			logger.Error(err, "restore patch 실패",
				"deploy", q.Name, "namespace", q.Namespace)
			if m.Recorder != nil {
				m.Recorder.Eventf(state, corev1.EventTypeWarning, "DeploymentRestoreFailed",
					"Deployment %s/%s 복구 실패 (수동 조치 필요): %v",
					q.Namespace, q.Name, err)
			}
			continue
		}
		logger.Info("Deployment restored after driver upgrade",
			"deploy", q.Name, "namespace", q.Namespace, "replicas", original)
		if m.Recorder != nil {
			m.Recorder.Eventf(state, corev1.EventTypeNormal, "DeploymentRestored",
				"Deployment %s/%s scaled 0 → %d after driver upgrade",
				q.Namespace, q.Name, original)
		}
	}
	state.Status.QuiescedDeployments = nil
	return nil
}

// restoreFromAnnotationFallback 은 DUS.Status.QuiescedDeployments 가 비어있는 상황 (operator
// restart 후 status 손실 등) 에서 Deployment annotation (npu.ai/replicas-backup) 으로부터
// replicas 를 복구하는 fallback 경로다. 매칭 기준은 quiesce 라벨 + node hostname 매칭 +
// annotation 존재이며, annotation 이 없거나 값이 invalid 한 Deployment 는 graceful skip 한다.
func (m *UpgradeStateMachine) restoreFromAnnotationFallback(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
) error {
	logger := logf.FromContext(ctx)

	var deploys appsv1.DeploymentList
	if err := m.List(ctx, &deploys, client.MatchingLabels{
		quiesceOnDriverUpgradeLabelKey: labelValueTrue,
	}); err != nil {
		return fmt.Errorf("annotation fallback: deployment list 실패: %w", err)
	}

	for i := range deploys.Items {
		d := &deploys.Items[i]
		if !deploymentTargetsNode(d, state.Spec.NodeName) {
			continue
		}
		backupVal, ok := d.Annotations[QuiesceReplicasBackupAnnotation]
		if !ok {
			continue
		}
		n, err := strconv.Atoi(backupVal)
		if err != nil || n < 0 {
			logger.Info("annotation fallback: invalid backup value — skip",
				"deploy", d.Name, "namespace", d.Namespace, "value", backupVal)
			continue
		}
		original := int32(n)
		base := d.DeepCopy()
		d.Spec.Replicas = &original
		delete(d.Annotations, QuiesceReplicasBackupAnnotation)
		if err := m.Patch(ctx, d, client.MergeFrom(base)); err != nil {
			logger.Error(err, "annotation fallback patch 실패",
				"deploy", d.Name, "namespace", d.Namespace)
			continue
		}
		logger.Info("Deployment restored via annotation fallback",
			"deploy", d.Name, "namespace", d.Namespace, "replicas", original,
			"node", state.Spec.NodeName)
		if m.Recorder != nil {
			m.Recorder.Eventf(state, corev1.EventTypeNormal, "DeploymentRestoredFromAnnotation",
				"Deployment %s/%s scaled 0 → %d via annotation backup (status was lost)",
				d.Namespace, d.Name, original)
		}
	}
	return nil
}

// deploymentTargetsNode 는 Deployment 의 PodTemplate 이 특정 노드를 대상으로 하는지 hostname
// 기반으로만 확인한다. 매칭 규칙: (1) spec.template.spec.nodeSelector["kubernetes.io/hostname"]
// == nodeName, 또는 (2) nodeAffinity 의 requiredDuringSchedulingIgnoredDuringExecution 에서
// NodeSelectorTerms 중 하나의 MatchExpressions 가 hostname In [...nodeName...]. preferred
// DuringScheduling / 다른 라벨 기반은 false 를 반환한다 — 잘못된 quiesce 를 피하기 위한 보수적
// false negative 처리다.
func deploymentTargetsNode(d *appsv1.Deployment, nodeName string) bool {
	if d == nil || nodeName == "" {
		return false
	}
	podSpec := d.Spec.Template.Spec
	// 1. nodeSelector hostname 직접 매칭
	if v, ok := podSpec.NodeSelector[hostnameLabelKey]; ok && v == nodeName {
		return true
	}
	// 2. nodeAffinity required term 의 hostname In 매칭
	if podSpec.Affinity == nil || podSpec.Affinity.NodeAffinity == nil ||
		podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false
	}
	for _, term := range podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			if expr.Key != hostnameLabelKey || expr.Operator != corev1.NodeSelectorOpIn {
				continue
			}
			for _, val := range expr.Values {
				if val == nodeName {
					return true
				}
			}
		}
	}
	return false
}

// resetRebootTracking 은 새 업그레이드 사이클에 들어갈 때 이전 사이클의 재부팅 흔적을 지운다.
// 카운터만 지우고 마커(RebootRequestedTime/RebootBootID)를 남기면, 모듈 교체 재부팅 게이트가
// 낡은 bootID 를 현재 부팅으로 착각한다.
func resetRebootTracking(s *v1alpha1.DriverUpgradeStateStatus) {
	s.RebootAttempts = 0
	s.RebootRequestedTime = metav1.Time{}
	s.RebootBootID = ""
}

// versionSourceHost 는 spec.driver.versionSource 의 호스트 채택 값이다(CRD 열거값 Policy|Host).
const versionSourceHost = "Host"

// EffectiveDesiredVersion 은 이 노드에서 operator 가 목표로 삼을 드라이버 버전이다.
// versionSource=Host 이고 모듈이 로드돼 있으면 호스트 버전을 채택한다 — 설치기가 호스트 버전을
// 채택하고 설치를 건너뛰므로 정책 버전을 목표로 두면 검증이 끝내 실패한다. 로드된 버전이 없으면
// 정책 버전이다.
func EffectiveDesiredVersion(policy *v1alpha1.DriverInstallPolicy, loadedHost string) string {
	if strings.EqualFold(policy.Spec.Driver.VersionSource, versionSourceHost) && loadedHost != "" {
		return loadedHost
	}
	return policy.Spec.Driver.Version
}

// containsString 는 slice 에 s 가 포함되어 있으면 true 를 반환한다.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// DeviceModelMatches 는 정책·상태의 모델 이름(specModel)과 NodeDeviceReport 의 장치 모델
// (deviceModel)이 같은 대상을 가리키는지 판정하는 유일한 정본이다 — 소비자가 각자 규칙을
// 복제하면 한쪽만 고치는 결함이 생긴다. "generic" 은 모른다는 표시(detector 가 제품을 판정하지
// 못했을 때의 값)라 어느 쪽에 있든 와일드카드로 본다. detector 가 PCI DB 로 구체화하면 장치
// 이름이 `a100-pcie-40gb` 처럼 되어 사용자가 쓰는 제품군 이름 `a100` 과 문자열이 안 맞으므로,
// 한쪽이 다른 쪽의 `-` 경계 접두어면 같은 대상으로 본다(양방향, 경계로 과잉 매칭 방어).
func DeviceModelMatches(deviceModel, specModel string) bool {
	if specModel == "" || strings.EqualFold(specModel, "generic") {
		return true
	}
	if strings.EqualFold(deviceModel, "generic") {
		return true
	}
	if strings.EqualFold(deviceModel, specModel) {
		return true
	}
	return modelFamilyPrefix(specModel, deviceModel) || modelFamilyPrefix(deviceModel, specModel)
}

// modelFamilyPrefix 는 short 가 long 의 제품군 접두어인지 본다 — `-` 경계까지 요구해
// `a1` 이 `a100-pcie-40gb` 에 걸리지 않게 한다.
func modelFamilyPrefix(short, long string) bool {
	return len(long) > len(short) && long[len(short)] == '-' &&
		strings.EqualFold(long[:len(short)], short)
}
