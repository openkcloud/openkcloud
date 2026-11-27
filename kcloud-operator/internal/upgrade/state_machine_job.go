// ============================================================
// state_machine_job.go: driver Job 모드 상태 핸들러 (Mode=job)
// 상세: state_machine.go 의 handleUpgrading/handleValidating/handleRollback 의 Mode=job 대체
//       경로다. DS patch 를 install Job 생성/재생성으로, Pod Ready 관찰을 Job 완료+NDR 일치로 재정의한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-28
// ============================================================

package upgrade

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilversion "k8s.io/apimachinery/pkg/util/version"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/internal/metrics"
	"kcloud-operator/internal/naming"
)

// isJobMode 는 정책이 install-Job 모드(Mode=job)인지 반환한다.
// 빈 값/"daemonset" 은 기존 DS 경로(false).
func isJobMode(policy *v1alpha1.DriverInstallPolicy) bool {
	return policy != nil && policy.Spec.Driver.Mode == "job"
}

// driverNotLoaded 는 노드의 NDR 이 vendor/model 매칭 device 를 "존재하지만 driverLoaded=false"
// 로 보고하는지 반환한다(Job 모드 self-heal 트리거용, handleIdle). Job 모드는 상주 DS 가 없어
// detector 는 driverVersion 을 dpkg 패키지에서 읽으므로, 모듈만 언로드(재부팅/수동 rmmod)되면
// driverLoaded=false 이지만 driverVersion 은 남는다 — 버전 일치만으로는 복구를 트리거하지 못하는
// 갭을 이 헬퍼가 메운다. NDR 미존재 / 매칭 device 없음 / 로드됨 이면 false. 빈 model 정책은
// vendor 만 매칭한다.
func (m *UpgradeStateMachine) driverNotLoaded(ctx context.Context, nodeName, vendor, model string) (bool, error) {
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
		if !d.DriverLoaded {
			return true, nil
		}
	}
	return false, nil
}

// LoadedHostVersion 은 vendor·model 과 맞고 모듈이 로드된 첫 장치의 드라이버 버전이다. 맞는 로드
// 장치가 없으면 빈 문자열이다. 로드되지 않은 장치는 보지 않는다 — 그 노드는 self-heal 재설치가
// 맡는다. 상태 기계와 컨트롤러가 같은 NDR 장치 목록에서 같은 값을 얻도록 이 함수 하나가 정한다.
func LoadedHostVersion(devices []v1alpha1.DeviceEntry, vendor, model string) string {
	for _, d := range devices {
		if strings.EqualFold(d.Vendor, vendor) && DeviceModelMatches(d.Model, model) &&
			d.DriverLoaded && d.DriverVersion != "" {
			return d.DriverVersion
		}
	}
	return ""
}

// loadedHostVersion 은 이 DUS 의 vendor·model 로 NDR 에서 구한 LoadedHostVersion 이다.
// NDR 이 없으면 빈 문자열이다.
func (m *UpgradeStateMachine) loadedHostVersion(ctx context.Context, state *v1alpha1.DriverUpgradeState) (string, error) {
	var ndr v1alpha1.NodeDeviceReport
	if err := m.Get(ctx, types.NamespacedName{Name: state.Spec.NodeName}, &ndr); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	return LoadedHostVersion(ndr.Status.Devices, state.Spec.Vendor, state.Spec.Model), nil
}

// hostDriverAhead 는 로드된 호스트 드라이버가 정책 버전보다 높고 다운그레이드가 막혀 있는지다.
// 버전을 파싱할 수 없으면 판정하지 않는다(종전 동작). 비교는 점으로 나뉜 숫자 기준이며
// Debian revision 은 무시한다 — 설치기의 다운그레이드 가드와 같은 결과다. versionSource=Host 는
// 호스트 버전을 목표로 채택하므로 상위 판정이 없다.
func (m *UpgradeStateMachine) hostDriverAhead(ctx context.Context, state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy) (string, bool, error) {
	if policy.Spec.Driver.AllowDowngrade || policy.Spec.Driver.Version == "" ||
		strings.EqualFold(policy.Spec.Driver.VersionSource, versionSourceHost) {
		return "", false, nil
	}
	want, err := utilversion.ParseGeneric(policy.Spec.Driver.Version)
	if err != nil {
		return "", false, nil
	}
	host, err := m.loadedHostVersion(ctx, state)
	if err != nil || host == "" {
		return "", false, err
	}
	got, err := utilversion.ParseGeneric(host)
	if err != nil || !got.GreaterThan(want) {
		return "", false, nil
	}
	return host, true, nil
}

// ─────────────────────────────────────────────
// Upgrading (job): install Job 생성/재생성
// ─────────────────────────────────────────────

// handleUpgradingJob 은 desired 이미지로 install Job 을 생성(또는 잔여 Job 재생성)하고
// Validating 으로 전이한다. 결정적 Job 이름이 곧 lease 이므로 Get-before-Create 로
// 중복 생성을 방지한다.
func (m *UpgradeStateMachine) handleUpgradingJob(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)

	// preflight 이 노드 커널로 고른 이미지를 우선 쓴다. 비어 있으면(구 DUS·커널맵 미선언) 정책
	// 기본 이미지로 폴백한다.
	desiredImage, desiredVersion := installTargetOf(state, policy)

	// 업그레이드 착수 시점에 rollback 대상(이전 버전 이미지)을 캡처한다. Job 모드는 상주
	// DS 가 없어 이전 이미지를 읽을 소스가 없으므로, DIP image 의 variant 접미사 + PreviousVersion
	// 으로 검증된 build tag 를 재구성해 저장한다. 재구성 불가 시 빈 값 유지 → rollback 은 Failed(안전).
	// 재구성의 입력은 desiredImage(= status.resolvedImage)가 아니라 policy.Spec.Driver.Image 다 —
	// 아래 handleRollbackJob 의 재구성과 같은 입력을 써야 두 자리가 갈리지 않는다.
	captureJobPreviousImage(state, policy.Spec.Driver.Image, policy.Spec.Driver.Installer, policy.Spec.Driver.ImageVersionBaked)

	// 롤백 보류 중 복구 사이클과 롤백 사이클의 재부팅 뒤 재진입은 보류 목표가 아니라 되돌린 버전을
	// 설치한다. resolvedImage 는 정책 목표로 고른 이미지라 쓰지 않고 롤백과 같은 이전 이미지를 쓴다.
	if held := state.Status.RolledBackVersion; held != "" && desiredVersion != held {
		if state.Status.PreviousImage == "" {
			metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
			m.Recorder.Eventf(state, corev1.EventTypeWarning, "RestoreImageRefused",
				"복원 버전 이미지 재구성 실패(job): 수동 조치 필요 (node=%s)", state.Spec.NodeName)
			return m.jobTransitionToFailed(ctx, state, "복원 버전 이미지 재구성 실패(job): 수동 조치 필요")
		}
		desiredImage = state.Status.PreviousImage
	}

	jobName := naming.InstallJobName(state.Spec.Vendor, state.Spec.Model, state.Spec.NodeName)
	var job batchv1.Job
	err := m.Get(ctx, types.NamespacedName{Name: jobName, Namespace: driverjob.Namespace}, &job)
	if apierrors.IsNotFound(err) {
		desired := driverjob.RenderInstallJob(policy, state.Spec.NodeName, desiredImage, desiredVersion)
		if err := m.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return false, 0, fmt.Errorf("install Job 생성 실패: %w", err)
		}
		m.Recorder.Eventf(state, corev1.EventTypeNormal, "InstallJobCreated",
			"노드 %s install Job 생성: %s (image=%s, version=%s)",
			state.Spec.NodeName, jobName, desiredImage, desiredVersion)
		return m.transitionTo(state, v1alpha1.UpgradeStateValidating, "install Job 생성", 20*time.Second)
	}
	if err != nil {
		return false, 0, fmt.Errorf("install Job 조회 실패: %w", err)
	}

	// 잔여 Job 존재(이전 사이클 산물). 목표와 불일치면 삭제 후 재생성(requeue → NotFound 경로).
	if !jobTargetsInstall(&job, desiredImage, desiredVersion) {
		logger.Info("이전 사이클 install Job 목표 불일치 — 삭제 후 재생성",
			"job", jobName,
			"currentImage", jobContainerImage(&job), "desiredImage", desiredImage,
			"currentVersion", jobDriverVersion(&job), "desiredVersion", desiredVersion)
		if err := m.deleteInstallJob(ctx, &job); client.IgnoreNotFound(err) != nil {
			return false, 0, fmt.Errorf("잔여 install Job 삭제 실패: %w", err)
		}
		return true, 5 * time.Second, nil
	}

	// 같은 목표라도 실패한 Job 은 지우고 다시 만든다. 재사용하면 새 사이클이 cordon·drain 뒤 곧바로 같은 실패로
	// 끝난다(handleRollbackJob 이 실패한 rollback Job 에 하는 처리와 같다).
	if jobFailed(&job) {
		logger.Info("같은 목표의 install Job 이 실패 상태 — 삭제 후 재생성", "job", jobName)
		if err := m.deleteInstallJob(ctx, &job); client.IgnoreNotFound(err) != nil {
			return false, 0, fmt.Errorf("실패한 install Job 삭제 실패: %w", err)
		}
		return true, installJobRecheckDelay, nil
	}

	// 동일 이미지 Job 이 이미 존재(요청 중복/재진입) — Validating 으로 진행.
	return m.transitionTo(state, v1alpha1.UpgradeStateValidating, "install Job 재사용", 20*time.Second)
}

// ─────────────────────────────────────────────
// Validating (job): Job 성공 종료 + NDR 일치 확인
// ─────────────────────────────────────────────

// handleValidatingJob 은 install Job 의 성공 종료를 확인한 뒤, daemonset 과 동일한
// validator 체인(DriverModule=NDR + DevicePlugin)을 재사용해 최종 검증한다.
func (m *UpgradeStateMachine) handleValidatingJob(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)

	// detector 차단 라벨 제거(daemonset 과 동일 — detector 재spawn→NDR 갱신 가능케).
	if err := m.EnsureUpgradingBlockingLabelRemoved(ctx, state.Spec.NodeName); err != nil {
		logger.Error(err, "Validating(job) 진입 시 blocking 라벨 제거 실패", "node", state.Spec.NodeName)
	}

	// ─── 1. Job 상태 판정 ───
	jobName := naming.InstallJobName(state.Spec.Vendor, state.Spec.Model, state.Spec.NodeName)
	var job batchv1.Job
	err := m.Get(ctx, types.NamespacedName{Name: jobName, Namespace: driverjob.Namespace}, &job)
	jobFound := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return false, 0, fmt.Errorf("install Job 조회 실패: %w", err)
	}

	// install Job 이 아직 끝나지 않았으면 검증 예산을 쓰지 않는다. Job 에는 자기 시한
	// (activeDeadlineSeconds, 기본 30m)이 있고 초과하면 Failed 로 끝나 handleUnfinishedInstallJob 이
	// 받는다. 여기서 별도 시계를 돌리면 정상 진행 중인 설치를 실패로 단정해, 그 롤백이 없는 이미지를
	// 집어 노드가 굳는 결과로 이어진다.
	if jobFound {
		if !jobComplete(&job) {
			return m.handleUnfinishedInstallJob(ctx, state, policy, &job)
		}
		m.clearInstallRetryNote(state)
	}

	// ─── 2. 검증 예산 가드 — Job 이 끝난 뒤(또는 사라진 뒤)부터 잰다 ───
	validationTimeout := parseDuration("", 15*time.Minute)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.ValidationTimeout != "" {
		validationTimeout = parseDuration(policy.Spec.UpgradePolicy.ValidationTimeout, 15*time.Minute)
	}
	since := state.Status.LastTransitionTime.Time
	if jobFound && job.Status.CompletionTime != nil && job.Status.CompletionTime.After(since) {
		since = job.Status.CompletionTime.Time
	}
	if !since.IsZero() && time.Since(since) > validationTimeout {
		if hasRollbackTarget(state) && policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.RollbackOnFailure {
			return m.transitionTo(state, v1alpha1.UpgradeStateRollback, "검증 타임아웃(job): 롤백 시작", 0)
		}
		// 롤백 없이 Failed 로 가는 경로의 지표와 경고는 여기서 남긴다. 롤백 대상이 없는 첫 설치는 롤백 단계를
		// 거치지 않으므로 그 단계가 남기던 기록이 없다.
		msg := validationTimeoutFailedMessage(state)
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "ValidationTimeout", "%s", msg)
		return m.jobTransitionToFailed(ctx, state, msg)
	}

	if !jobFound {
		// Job 미발견(아직 미생성 / 완료 후 TTL GC). 검증 예산 안에서 재확인 대기.
		logger.Info("install Job 미발견 — 재확인 대기", "job", jobName)
		return true, 10 * time.Second, nil
	}

	// device-plugin 재스캔 레이스 방지: drain 으로 재생성된 device-plugin Pod 가
	// install Job 완료(모듈 재로드→/dev 재생성) '전'에 기동했다면, 기동 1회 스캔에서
	// 디바이스를 못 봐 0개를 광고한 채 굳는다(allocatable=0). Job 완료 시각 이전에 뜬
	// plugin 을 1회 바운스해 디바이스 존재 상태에서 재스캔시킨다(자기제한적: 재기동 Pod 는
	// 완료 이후 시각이라 재삭제되지 않음).
	if job.Status.CompletionTime != nil {
		restarted, rerr := m.restartStaleDevicePluginPods(ctx, state.Spec.NodeName, *job.Status.CompletionTime)
		if rerr != nil {
			return false, 0, fmt.Errorf("stale device-plugin 재시작 실패: %w", rerr)
		}
		if restarted > 0 {
			return true, 10 * time.Second, nil // 재스캔 Pod Ready 대기 후 validator 진입
		}
	}

	// ─── 3. Job Complete → validator 체인 순차 실행(완전 재사용) ───
	desiredVersion := state.Status.DesiredVersion
	for _, v := range validatorsFor(policy) {
		m.Recorder.Eventf(state, corev1.EventTypeNormal,
			fmt.Sprintf("UpgradeValidator-%s-Started", v.Name()),
			"validator 실행(job): %s (node=%s)", v.Name(), state.Spec.NodeName)

		res, err := v.Run(ctx, m.Client, state.Spec.NodeName, state.Spec.Vendor, desiredVersion)
		if err != nil {
			return false, 0, fmt.Errorf("validator %s 실행 실패: %w", v.Name(), err)
		}
		if !res.Passed {
			m.Recorder.Eventf(state, corev1.EventTypeNormal,
				fmt.Sprintf("UpgradeValidator-%s-Failed", v.Name()),
				"validator %s 미통과(재시도 대기): %s", v.Name(), res.Message)
			return true, 10 * time.Second, nil
		}
		m.Recorder.Eventf(state, corev1.EventTypeNormal,
			fmt.Sprintf("UpgradeValidator-%s-Passed", v.Name()),
			"validator %s 통과: %s", v.Name(), res.Message)
	}

	// ─── 4. 모든 validator 통과 → 검증 성공 ───
	state.Status.CurrentVersion = state.Status.DesiredVersion
	m.Recorder.Eventf(state, corev1.EventTypeNormal, "UpgradeValidated",
		"노드 %s 드라이버 검증 성공(job): %s", state.Spec.NodeName, state.Status.DesiredVersion)
	return m.transitionTo(state, v1alpha1.UpgradeStateUncordoning, "검증 성공(job)", 0)
}

// handleUnfinishedInstallJob 은 끝나지 않은 install Job 을 본다. Failed 조건이면(backoffLimit 소진·
// activeDeadlineSeconds 초과) 실패로 마무리한다. NVIDIA apt 설치기가 hold 충돌(종료 코드 3)로 끝냈으면 OnFailure
// 재시작을 기다리지 않고 Job 을 지운 뒤 실패로 마무리한다 — 같은 hold 에 다시 막힌다. 그 밖에는 직전
// 시도의 실패 사유를 남기고 완료를 기다린다. 끝난 Job 은 파드를 조회하지 않으므로 여기로 오지 않는다.
func (m *UpgradeStateMachine) handleUnfinishedInstallJob(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
	job *batchv1.Job,
) (bool, time.Duration, error) {
	term, err := m.jobTermination(ctx, job, driverjob.InstallContainerName)
	if err != nil {
		return false, 0, fmt.Errorf("install Job 파드 조회 실패: %w", err)
	}
	switch {
	case jobFailed(job):
		return m.installJobFailed(ctx, state, policy, job, term)
	// 종료 코드 3 = hold 충돌은 NVIDIA apt 설치기(점검 대상 정책)만의 계약이다. 그 밖의 3 은 아래 일반 실패로 다룬다.
	case term.found && term.exitCode == driverjob.ExitCodeHostPackageConflict && preflightApplicable(policy):
		if err := m.deleteInstallJob(ctx, job); client.IgnoreNotFound(err) != nil {
			return false, 0, fmt.Errorf("hold 충돌 install Job 삭제 실패: %w", err)
		}
		requeue, after, err := m.installJobFailed(ctx, state, policy, job, term)
		// 지운 Job 이 캐시에 남아 있으면 롤백이 그것을 진행 중인 롤백 Job 으로 읽는다. 캐시가 따라올 시간을 준다.
		if err == nil && state.Status.State == v1alpha1.UpgradeStateRollback {
			after = installJobRecheckDelay
		}
		return requeue, after, err
	}
	m.noteInstallRetry(state, term)
	logf.FromContext(ctx).Info("install Job 진행 중 — 완료 대기", "job", job.Name)
	return true, 10 * time.Second, nil
}

// restartStaleDevicePluginPods 는 since(=install Job 완료 시각) 이전에 기동한 노드의
// device-plugin Pod 를 삭제해 재스캔을 유도한다. 반환값은 삭제 건수.
// device-plugin 은 벤더별로 다른 네임스페이스(kube-system / kcloud)에 있으므로 전 네임스페이스를
// 조회하고 nodeName + device-plugin 이름/라벨로 필터한다(deleteDevicePluginPods 와 동일 규칙).
func (m *UpgradeStateMachine) restartStaleDevicePluginPods(ctx context.Context, nodeName string, since metav1.Time) (int, error) {
	var podList corev1.PodList
	if err := m.List(ctx, &podList); err != nil {
		return 0, err
	}
	logger := logf.FromContext(ctx)
	deleted := 0
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName != nodeName || pod.DeletionTimestamp != nil {
			continue
		}
		isDP := false
		for key, val := range pod.Labels {
			if key == "app.kubernetes.io/name" && strings.Contains(val, "device-plugin") {
				isDP = true
				break
			}
		}
		if !isDP && strings.Contains(pod.Name, "device-plugin") {
			isDP = true
		}
		if !isDP {
			continue
		}
		// Job 완료 이후(=디바이스 존재 후) 기동한 Pod 는 최신 스캔 — 유지.
		if pod.Status.StartTime == nil || !pod.Status.StartTime.Before(&since) {
			continue
		}
		logger.Info("stale device-plugin Pod 재시작(재스캔)", "pod", pod.Name, "node", nodeName)
		if err := m.Delete(ctx, pod); client.IgnoreNotFound(err) != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// ─────────────────────────────────────────────
// Rollback (job): 이전 버전 install Job 재실행
// ─────────────────────────────────────────────

// handleRollbackJob 은 이전 버전 이미지로 install Job 을 재실행한다. 1차 범위는 "재-Job 실행"
// 으로 한정하며 DS 이미지 복원 시맨틱은 없다. 이전 이미지(캡처본 또는 variant 재구성) 부재 시
// Failed(안전)로 전이한다.
//
// 카운팅 규칙: RollbackAttempts 는 rollback Job 을 실제로 새로 생성하는 시점(아래 "absent →
// create")에만 1회 증가한다. 잔여 Job 삭제→requeue 단계는 카운트하지 않아 이중 증가를 막는다.
func (m *UpgradeStateMachine) handleRollbackJob(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)

	maxRollbacks := int32(3)
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.MaxRollbackAttempts > 0 {
		maxRollbacks = policy.Spec.UpgradePolicy.MaxRollbackAttempts
	}

	prevVersion := state.Status.PreviousVersion
	if !hasRollbackTarget(state) {
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackFailed",
			"롤백할 이전 버전 없음(job): 수동 조치 필요 (node=%s)", state.Spec.NodeName)
		return m.jobTransitionToFailed(ctx, state, "이전 버전 없음(job): 수동 조치 필요")
	}
	markRolledBack(state, policy)

	// rollback 대상 이미지: 캡처된 PreviousImage 우선, 없으면 DIP variant 재구성. kernelImages
	// 정책도 재구성은 policy.Spec.Driver.Image(DKMS 이미지)에서 해 커널 토큰이 입력에 없으므로
	// 별도 가드가 필요 없다(결과 …:<prev>-vN 는 이전 버전 DKMS 태그로 존재한다).
	prevImage := state.Status.PreviousImage
	if prevImage == "" {
		prevImage = reconstructPrevImage(policy.Spec.Driver.Image, policy.Spec.Driver.Installer, prevVersion, policy.Spec.Driver.ImageVersionBaked)
	}
	if prevImage == "" {
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackRefused",
			"이전 이미지 미보유 + variant 재구성 실패(job): 수동 조치 필요 (node=%s)", state.Spec.NodeName)
		return m.jobTransitionToFailed(ctx, state, "rollback 대상 이미지 재구성 실패(job): 수동 조치 필요")
	}

	jobName := naming.InstallJobName(state.Spec.Vendor, state.Spec.Model, state.Spec.NodeName)
	var job batchv1.Job
	getErr := m.Get(ctx, types.NamespacedName{Name: jobName, Namespace: driverjob.Namespace}, &job)
	jobExists := getErr == nil
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return false, 0, fmt.Errorf("rollback: install Job 조회 실패: %w", getErr)
	}

	if jobExists && jobTargetsInstall(&job, prevImage, prevVersion) {
		// 이미 rollback 대상 Job 이 존재.
		if jobFailed(&job) {
			// rollback Job 도 실패 → 삭제해 다음 사이클에서 재시도(재생성 시 카운트).
			logger.Info("rollback Job 실패 — 삭제 후 재시도", "job", jobName)
			if err := m.deleteInstallJob(ctx, &job); client.IgnoreNotFound(err) != nil {
				return false, 0, fmt.Errorf("실패한 rollback Job 삭제 실패: %w", err)
			}
			return true, 5 * time.Second, nil
		}
		// 진행 중/완료 → Validating 이 관찰. (카운트 없음)
		state.Status.DesiredVersion = prevVersion
		return m.transitionTo(state, v1alpha1.UpgradeStateValidating,
			fmt.Sprintf("rollback Job 진행(job): %s", prevVersion), 20*time.Second)
	}

	if jobExists {
		// 대상과 다른(실패한 desired 이미지) 잔여 Job → 삭제 후 재생성. (카운트 없음)
		logger.Info("rollback: 잔여 install Job 삭제 후 재생성",
			"job", jobName, "current", jobContainerImage(&job), "rollbackTo", prevImage)
		if err := m.deleteInstallJob(ctx, &job); client.IgnoreNotFound(err) != nil {
			return false, 0, fmt.Errorf("rollback: 잔여 install Job 삭제 실패: %w", err)
		}
		return true, 5 * time.Second, nil
	}

	// Job 부재 → rollback Job 을 새로 생성 = 1회 attempt.
	state.Status.RollbackAttempts++
	if state.Status.RollbackAttempts > maxRollbacks {
		msg := withCause(fmt.Sprintf("롤백 %d회 초과: 수동 조치 필요", maxRollbacks), lastStatusCause(state))
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackExhausted", "%s (node=%s)", msg, state.Spec.NodeName)
		return m.jobTransitionToFailed(ctx, state, msg)
	}

	metrics.RecordRollback(state.Spec.Vendor)
	// RenderInstallJob 이 아니라 RenderRollbackJob 이다 — 롤백 Job 은 다운그레이드를 허용받아야
	// 하고 정책 선언 버전이 아니라 지시받은 이전 버전을 설치해야 한다. 일반 설치 Job 으로 렌더링하면
	// installer 자신의 다운그레이드 가드가 거부해 롤백이 무동작이 된다.
	desired := driverjob.RenderRollbackJob(policy, state.Spec.NodeName, prevImage, prevVersion)
	if err := m.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, 0, fmt.Errorf("rollback install Job 생성 실패: %w", err)
	}
	state.Status.DesiredVersion = prevVersion
	m.Recorder.Eventf(state, corev1.EventTypeWarning, "RollbackStarted",
		"노드 %s 롤백 시작(job): → %s (image=%s)", state.Spec.NodeName, prevVersion, prevImage)
	metrics.RecordUpgradeComplete(state.Spec.Vendor, "rollback")
	return m.transitionTo(state, v1alpha1.UpgradeStateValidating,
		fmt.Sprintf("롤백 시작(job): %s", prevVersion), 20*time.Second)
}

// ─────────────────────────────────────────────
// job 헬퍼
// ─────────────────────────────────────────────

// jobTransitionToFailed 는 Job 모드 Failed 전이 시 quiesce 복구 + 라벨 정리 후 Failed 로 전이한다
// (daemonset 의 Failed 전이 정리 순서와 동일).
func (m *UpgradeStateMachine) jobTransitionToFailed(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	msg string,
) (bool, time.Duration, error) {
	logger := logf.FromContext(ctx)
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

// deleteInstallJob 은 install Job 을 background propagation 으로 삭제한다(파드 cascade 삭제).
func (m *UpgradeStateMachine) deleteInstallJob(ctx context.Context, job *batchv1.Job) error {
	return m.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))
}

// installRetryPrefix 는 install Job 재시도 중 상태 메시지의 앞부분이다. 파드가 지워진 Failed 에서
// 이 접두로 시작하는 상태 메시지를 마지막 실패의 원인으로 읽는다.
const installRetryPrefix = "install Job 재시도 중"

const (
	// installJobDoneMessage 는 install Job 이 끝난 뒤 재시도 중 문구를 대신하는 상태 메시지다.
	installJobDoneMessage = "install Job 완료 — 검증 중"
	// holdConflictDefaultCause 는 종료 코드 3(hold 충돌)인데 종료 메시지가 비었을 때의 원인 문구다.
	holdConflictDefaultCause = "hold 된 호스트 패키지가 드라이버 설치를 막는다(종료 메시지 없음)"
	// installJobRecheckDelay 는 install Job 을 지운 뒤 다시 보기까지의 간격이다.
	installJobRecheckDelay = 5 * time.Second
)

// clearInstallRetryNote 는 install Job 이 끝났는데 상태 메시지가 아직 재시도 중이면 검증 중으로 바꾼다.
// 남겨 두면 검증을 기다리는 동안과 검증 시한 Failed 메시지의 "마지막 검증" 꼬리에서 설치 실패처럼 읽힌다.
func (m *UpgradeStateMachine) clearInstallRetryNote(state *v1alpha1.DriverUpgradeState) {
	if strings.HasPrefix(state.Status.Message, installRetryPrefix) {
		m.noteWait(state, corev1.EventTypeNormal, "InstallJobCompleted", installJobDoneMessage)
	}
}

// noteInstallRetry 는 재시도 중인 install Job 의 직전 시도가 실패했으면 그 사유를 상태 메시지와
// 이벤트에 남긴다. Job 컨트롤러는 backoffLimit 에 닿으면 파드를 지우므로, 이 메시지가 Failed 전이
// 때 읽을 수 있는 마지막 실패 기록이다. 메시지가 바뀔 때만 이벤트가 난다(noteWait).
func (m *UpgradeStateMachine) noteInstallRetry(state *v1alpha1.DriverUpgradeState, term containerTermination) {
	if !term.found || term.exitCode == 0 {
		return
	}
	m.noteWait(state, corev1.EventTypeWarning, "InstallAttemptFailed",
		withCause(fmt.Sprintf("%s(exit=%d)", installRetryPrefix, term.exitCode), term.message))
}

// installJobFailed 는 install Job 실패를 마무리한다. 되돌릴 이전 버전이 없으면(첫 설치, 이전 버전을 모르는
// 복구 사이클) 롤백을 거치지 않는다 — 롤백은 "이전 버전 없음" 으로 끝나며 설치 원인을 덮는다. 원인은
// installFailureCause 가 골라 상태 메시지와 이벤트에 싣는다.
func (m *UpgradeStateMachine) installJobFailed(
	ctx context.Context,
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
	job *batchv1.Job,
	term containerTermination,
) (bool, time.Duration, error) {
	cause := installFailureCause(state, policy, job, term)
	exit := "파드 종료 기록 없음"
	if term.found {
		exit = fmt.Sprintf("exit=%d", term.exitCode)
	}
	m.Recorder.Eventf(state, corev1.EventTypeWarning, "InstallJobFailed",
		"노드 %s install Job 실패(%s): %s", state.Spec.NodeName, exit, withCause("", cause))
	if !hasRollbackTarget(state) {
		metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
		return m.jobTransitionToFailed(ctx, state, noRollbackTargetFailedMessage(state, cause))
	}
	if policy.Spec.UpgradePolicy != nil && policy.Spec.UpgradePolicy.RollbackOnFailure {
		return m.transitionTo(state, v1alpha1.UpgradeStateRollback, withCause("install Job 실패: 롤백 시작", cause), 0)
	}
	metrics.RecordUpgradeComplete(state.Spec.Vendor, "failure")
	return m.jobTransitionToFailed(ctx, state, withCause("install Job 실패: 수동 조치 필요", cause))
}

// installFailureCause 는 install Job 실패의 원인 문구다. 설치 컨테이너 종료 메시지가 먼저다. hold 충돌 계약이
// 있는 정책(preflightApplicable)에서 종료 코드 3 인데 메시지가 비면 기본 사유를 쓴다. Job 컨트롤러는
// backoffLimit·시한에 닿으면 실행 중 파드를 지워 종료 기록이 없으므로, 그때는 Job 의 Failed 조건 사유를 앞에
// 두고 재시도 중에 상태 메시지에 남긴 마지막 실패를 뒤에 잇는다. 재시도가 있었어도 시한 초과 같은 Job 쪽
// 사유가 가려지지 않는다.
func installFailureCause(
	state *v1alpha1.DriverUpgradeState,
	policy *v1alpha1.DriverInstallPolicy,
	job *batchv1.Job,
	term containerTermination,
) string {
	if term.found {
		switch {
		case term.message != "":
			return term.message
		case term.exitCode == driverjob.ExitCodeHostPackageConflict && preflightApplicable(policy):
			return holdConflictDefaultCause
		}
	}
	cause := jobFailedReason(job)
	if strings.HasPrefix(state.Status.Message, installRetryPrefix) {
		cause = withCause(cause, state.Status.Message)
	}
	return cause
}

// hasRollbackTarget 은 되돌릴 이전 버전이 있는지다. 사이클을 열 때 currentVersion 을 옮겨 두므로
// 첫 설치와 이전 버전을 모르는 복구 사이클은 빈 값이다. Validating 과 handleRollbackJob 이 이 판정 하나를 쓴다.
func hasRollbackTarget(state *v1alpha1.DriverUpgradeState) bool {
	return state.Status.PreviousVersion != ""
}

// noRollbackTargetFailedMessage 는 되돌릴 이전 버전이 없는 설치 실패(첫 설치, 이전 버전을 모르는 복구
// 사이클)의 상태 메시지다. Failed 는 정책을 다시 읽지 않으므로 재시도 방법을 끝에 둔다.
func noRollbackTargetFailedMessage(state *v1alpha1.DriverUpgradeState, cause string) string {
	return withCause("설치 실패(job): 되돌릴 이전 버전이 없어 롤백하지 않는다", cause) +
		fmt.Sprintf(" — 원인을 조치한 뒤 kubectl delete dus %s 로 다시 시도한다", state.Name)
}

// withCause 는 msg 뒤에 설치 컨테이너가 남긴 원인을 붙인다. 원인이 없으면 msg 그대로다.
func withCause(msg, cause string) string {
	switch {
	case cause == "":
		return msg
	case msg == "":
		return cause
	}
	return msg + " — " + cause
}

// lastStatusCause 는 롤백 소진 메시지에 이어 붙일 직전 상태 메시지다(롤백 전이에 실린 설치 원인). 비면 빈 값.
func lastStatusCause(state *v1alpha1.DriverUpgradeState) string {
	if state.Status.Message == "" {
		return ""
	}
	return "직전 상태: " + state.Status.Message
}

// installTargetOf 는 install Job 이 기본으로 설치할 이미지·버전이다. 설치와 설치 전 점검이 같은
// 대상을 보도록 한 곳에서 고른다.
func installTargetOf(state *v1alpha1.DriverUpgradeState, policy *v1alpha1.DriverInstallPolicy) (image, version string) {
	image = state.Status.ResolvedImage
	if image == "" {
		image = policy.Spec.Driver.Image
	}
	version = state.Status.DesiredVersion
	if version == "" {
		version = policy.Spec.Driver.Version
	}
	return image, version
}

// captureJobPreviousImage 는 업그레이드 착수 시 rollback 대상 이미지를 DUS status 에 캡처한다.
// 이미 캡처됐거나 PreviousVersion 이 없으면 no-op. 재구성 결과가 검증된 build tag 일 때만 저장한다.
// dipImage 는 policy.Spec.Driver.Image 다 — status.resolvedImage 를 주면 안 된다(reconstructPrevImage
// 주석 참고).
func captureJobPreviousImage(state *v1alpha1.DriverUpgradeState, dipImage, installer string, versionBaked bool) {
	if state.Status.PreviousImage != "" || state.Status.PreviousVersion == "" {
		return
	}
	candidate := reconstructPrevImage(dipImage, installer, state.Status.PreviousVersion, versionBaked)
	if candidate != "" {
		state.Status.PreviousImage = candidate
	}
}

// reconstructPrevImage 는 DIP 이미지의 variant 접미사(-vN)와 prevVersion 을 결합해 이전 버전
// 이미지를 재구성한다. variant 미검출/검증 실패 시 빈 문자열을 돌려준다.
// dipImage 는 반드시 policy.Spec.Driver.Image 여야 한다 — status.resolvedImage 를 주면 조립·맵으로
// 고른 커널 이미지의 빌드 개정이 결과 태그에 붙는데, 그 개정은 일반 설치 이미지의 개정과 다른 수라
// (kernelImageBuildRevision 이 별도 필드인 이유) 그렇게 만든 태그는 거의 항상 레지스트리에 없어
// 고착으로 이어진다. spec.driver.image 는 사이트가 적은 발행된 참조라 그 개정은 항상 실재한다.
func reconstructPrevImage(dipImage, installer, prevVersion string, versionBaked bool) string {
	if dipImage == "" || prevVersion == "" {
		return ""
	}
	// apt/script 인스톨러는 보통 이미지가 버전을 담지 않는다 — 드라이버 버전은 DRIVER_VERSION 환경
	// 변수로 들어간다. 그런데도 태그를 버전으로 갈아 끼우면 레지스트리에 없는 이미지가 만들어지고,
	// rollback Job 이 ImagePullBackOff 로 앉아 노드가 cordon 상태로 고착된다.
	// 되돌릴 대상은 같은 이미지 + 이전 버전이다. 단 DIP 가 imageVersionBaked 를 선언한 이미지는
	// 태그가 곧 설치 버전이라(tenstorrent-driver-ds:2.8.0-v1) 같은 이미지로는 이전 버전을 설치할
	// 수 없다 — ngc 와 같이 이전 버전 태그를 재구성한다.
	if installer != v1alpha1.DriverInstallerNGC && !versionBaked {
		// 예외: 사전 컴파일 커널 이미지(태그 <version>-k<kernel>-vN)는 apt 라도 태그에 박힌
		// 버전의 모듈만 담아 DRIVER_VERSION 으로 다른 버전을 설치하지 못한다. 되돌릴 버전이 그
		// 박힌 버전과 다르면 설치기 정합성 가드가 동봉 모듈≠DRIVER_VERSION 으로 거부해(노드가
		// CrashLoopBackOff 가 된다) 재구성 불가로 돌려 호출부가 버전 비종속 기본 이미지로
		// 되돌아가게 한다. 박힌 버전이 같으면 그대로 쓴다.
		if isPrecompiledKernelImage(dipImage) && extractKernelImageVersion(dipImage) != prevVersion {
			return ""
		}
		return dipImage
	}
	variant := extractImageVariantSuffix(dipImage)
	if variant == "" {
		return ""
	}
	idx := strings.LastIndex(dipImage, ":")
	if idx < 0 {
		return ""
	}
	candidate := dipImage[:idx+1] + prevVersion + variant
	if !isVerifiedBuildTag(candidate) {
		return ""
	}
	return candidate
}

// jobContainerImage 는 Job pod template 의 첫 컨테이너 이미지를 반환한다.
func jobContainerImage(job *batchv1.Job) string {
	cs := job.Spec.Template.Spec.Containers
	if len(cs) > 0 {
		return cs[0].Image
	}
	return ""
}

// jobTargetsInstall 은 잔여 Job 이 지금 하려는 설치(이미지 + 목표 버전)와 같은 것인지 반환한다.
// 이미지만으로는 판정할 수 없다. apt/script 인스톨러는 같은 이미지가 모든 드라이버 버전을 설치하고
// 목표 버전은 DRIVER_VERSION env 로만 들어간다 — 그래서 지난 사이클이 남긴 완료된 Job 과 이번
// 사이클의 Job 이 이미지 상으로는 구별되지 않고, 판정을 이미지에만 맡기면 새 사이클이 그 Job 을
// 재사용해 실제 설치 없이 앞 Job 의 결과로 검증이 끝난다. DRIVER_VERSION 이 없는 Job(구 operator
// 산물)은 불일치로 본다 — 삭제 후 재생성이 안전한 쪽이다.
func jobTargetsInstall(job *batchv1.Job, image, version string) bool {
	return jobContainerImage(job) == image && jobDriverVersion(job) == version
}

// jobDriverVersion 은 Job pod template 첫 컨테이너의 DRIVER_VERSION env 값이다(없으면 "").
func jobDriverVersion(job *batchv1.Job) string {
	cs := job.Spec.Template.Spec.Containers
	if len(cs) == 0 {
		return ""
	}
	for _, e := range cs[0].Env {
		if e.Name == "DRIVER_VERSION" {
			return e.Value
		}
	}
	return ""
}

// jobComplete 는 Job 이 성공 종료했는지(Complete condition 또는 Succeeded>0) 반환한다.
func jobComplete(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return job.Status.Succeeded > 0
}

// jobFailed 는 Job 이 hard failure(Failed condition, backoffLimit 소진 등)인지 반환한다.
func jobFailed(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// containerTermination 은 Job 파드 컨테이너가 남긴 마지막 종료 기록이다.
type containerTermination struct {
	found    bool
	exitCode int32
	reason   string
	message  string
}

// jobPods 는 job 이 소유한 파드다. job-name 라벨로 고르고, job 에 UID 가 있으면 그 Job 이 controller 인
// 파드만 남긴다. 같은 이름 Job 을 지우고 다시 만든 직후에는 지워지는 중인 옛 파드가 같은 라벨로 남기 때문이다.
// UID 가 비어 있으면 소유 관계를 알 수 없어 라벨만 본다.
func (m *UpgradeStateMachine) jobPods(ctx context.Context, job *batchv1.Job) ([]corev1.Pod, error) {
	var pods corev1.PodList
	if err := m.List(ctx, &pods, client.InNamespace(job.Namespace),
		client.MatchingLabels{batchv1.JobNameLabel: job.Name}); err != nil {
		return nil, err
	}
	out := make([]corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		if job.UID != "" && !metav1.IsControlledBy(&pods.Items[i], job) {
			continue
		}
		out = append(out, pods.Items[i])
	}
	return out, nil
}

// jobPodWaiting 은 job 파드에서 container 가 기다리는 사유와 메시지다. 기다리는 컨테이너가 없으면 빈 값이다.
// 메시지는 여러 줄일 수 있어(pull 오류 문구 등) 이벤트용 한 줄로 접는다.
func (m *UpgradeStateMachine) jobPodWaiting(
	ctx context.Context, job *batchv1.Job, container string,
) (string, string, error) {
	pods, err := m.jobPods(ctx, job)
	if err != nil {
		return "", "", err
	}
	for i := range pods {
		for _, cs := range pods[i].Status.ContainerStatuses {
			if cs.Name == container && cs.State.Waiting != nil {
				return cs.State.Waiting.Reason, strings.Join(strings.Fields(cs.State.Waiting.Message), " "), nil
			}
		}
	}
	return "", "", nil
}

// jobTermination 은 job 파드들에서 container 이름의 가장 최근 종료 기록을 고른다. 현재 상태와
// 직전 상태(OnFailure 재시작 전)를 함께 본다. 파드가 없으면(TTL 정리 등) found=false 다.
func (m *UpgradeStateMachine) jobTermination(
	ctx context.Context, job *batchv1.Job, container string,
) (containerTermination, error) {
	pods, err := m.jobPods(ctx, job)
	if err != nil {
		return containerTermination{}, err
	}
	var best containerTermination
	var bestAt time.Time
	for i := range pods {
		for _, cs := range pods[i].Status.ContainerStatuses {
			if cs.Name != container {
				continue
			}
			for _, t := range []*corev1.ContainerStateTerminated{cs.State.Terminated, cs.LastTerminationState.Terminated} {
				if t == nil || (best.found && !t.FinishedAt.After(bestAt)) {
					continue
				}
				// 종료 메시지는 여러 줄일 수 있어(점검 Job 의 로그 꼬리 등) 상태 메시지·이벤트용 한 줄로 접는다.
				best = containerTermination{found: true, exitCode: t.ExitCode, reason: t.Reason,
					message: strings.Join(strings.Fields(t.Message), " ")}
				bestAt = t.FinishedAt.Time
			}
		}
	}
	return best, nil
}

// validationTimeoutFailedMessage 는 검증 예산이 끝나 Failed 로 갈 때의 메시지다. 상태 전이는
// cordon 을 그대로 두므로(벤더 광고가 0 일 때만 해제) 사람이 무엇을 해야 하는지를 적는다 —
// 설치된 버전이 목표와 달라 검증이 안 끝나는 경우(계열 최신이 깔린 첫 설치)가 대표 사례다.
// Failed 는 정책 변경을 다시 읽지 않으므로 재생성 단계를 적는다.
func validationTimeoutFailedMessage(state *v1alpha1.DriverUpgradeState) string {
	msg := fmt.Sprintf("검증 타임아웃(job): 수동 조치 필요 — 노드 드라이버가 목표 %s 와 다르면 "+
		"kubectl uncordon %s 뒤 DriverInstallPolicy spec.driver.version 을 설치된 버전으로 맞추거나 "+
		"allowDowngrade=true 로 두고, kubectl delete dus %s 로 DriverUpgradeState 를 재생성한다(Failed 상태는 정책을 "+
		"다시 읽지 않는다)", state.Status.DesiredVersion, state.Spec.NodeName, state.Name)
	if state.Status.Message != "" {
		msg += ". 마지막 검증: " + state.Status.Message
	}
	return msg
}
