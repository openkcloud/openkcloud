// ============================================================
// validator.go: 드라이버 업그레이드 검증 단계 (Validator) 인터페이스 + 구현
// 상세: 드라이버 업그레이드 검증을 단계별 책임(DriverModule/DevicePlugin/Workload)으로 나눈
// 인터페이스다. Validator 는 검증 1회 시도(Run)만 하고, 재시도·Event/Metric 발행은 caller 몫이다.
// 생성일: 2026-04-27 | 수정일: 2026-10-13
// ============================================================

package validator

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// Result 는 검증 단계의 결과를 표현한다.
//   - Passed=true 면 caller 는 다음 Validator 로 진행한다.
//   - Passed=false 는 "검증 미통과" — error 와 의미 분리. 재시도는 caller 의
//     Timeout() 안에서 결정한다.
//   - Message 는 Event/로그용 사람이 읽을 수 있는 사유.
type Result struct {
	Passed  bool
	Message string
}

// Validator 는 단일 검증 단계 인터페이스다. Name 은 metric/event label(예:
// "DriverModule", "DevicePlugin"), Timeout 은 controller wall-clock 예산(caller 는
// LastTransitionTime 부터 이 값을 초과하면 Failed/Rollback 으로 전이), Run 은 검증
// 1회 시도로 통과/미통과/오류 중 하나를 반환하며 ctx cancel 시 즉시 반환해야 한다
// (호출자가 reconcile timeout 으로 차단).
type Validator interface {
	Name() string
	Timeout() time.Duration
	Run(ctx context.Context, c client.Client, nodeName, vendor, desiredVersion string) (Result, error)
}

// ─────────────────────────────────────────────
// DriverModuleValidator
// ─────────────────────────────────────────────

// DriverModuleValidator 는 노드의 드라이버 커널 모듈이 desiredVersion 으로 로드되었는지
// NodeDeviceReport(NDR)로 검증한다. 통과 조건: NDR.status.devices[].driverVersion ==
// desiredVersion(vendor 일치) 이고 DriverLoaded=true. timeout 30s — NDR collector 가
// 보통 수 초 내 갱신.
type DriverModuleValidator struct{}

// Name 은 metric/event label.
func (v *DriverModuleValidator) Name() string { return "DriverModule" }

// Timeout 은 caller 가 wall-clock 으로 사용할 budget.
func (v *DriverModuleValidator) Timeout() time.Duration { return 30 * time.Second }

// Run 은 NDR 의 driverVersion 이 desiredVersion 과 일치하는지 1회 확인한다.
//   - desiredVersion 이 비어 있으면 검증 skip — Passed=true.
//   - NDR 미존재 시 Passed=false (collector 가 아직 갱신 못 함, caller 재시도).
//   - vendor 매칭되는 device 가 없거나 driverVersion 이 다르면 Passed=false.
//   - API 오류는 error 로 반환 — caller 재시도 가능.
func (v *DriverModuleValidator) Run(
	ctx context.Context,
	c client.Client,
	nodeName, vendor, desiredVersion string,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if desiredVersion == "" {
		return Result{Passed: true, Message: "desiredVersion 미지정으로 모듈 검증 skip"}, nil
	}

	var ndr v1alpha1.NodeDeviceReport
	// NDR 의 metadata.name 은 nodeName 과 동일하게 관리됨 (driver_upgrade_controller_test.go 참고).
	if err := c.Get(ctx, client.ObjectKey{Name: nodeName}, &ndr); err != nil {
		// NotFound 는 미통과로 처리, 그 외 API 오류는 caller 가 재시도하도록 error 반환.
		if isNotFound(err) {
			return Result{Passed: false, Message: "NDR 미존재 (collector 갱신 대기)"}, nil
		}
		return Result{}, err
	}

	// host 의 (loaded) driver 버전을 캡처해 actionable error 메시지에 사용한다.
	// 동일 vendor 의 device 가 여러 개여도 driverVersion 은 동일하다고 가정 — 첫 발견값을 사용.
	hostVer := ""
	hostDetail := ""
	hostNeedsReboot := false
	for _, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Vendor, vendor) {
			continue
		}
		if !d.DriverLoaded {
			continue
		}
		if hostVer == "" {
			hostVer = d.DriverVersion
			hostDetail = d.DriverVersionDetail
			hostNeedsReboot = d.NeedsReboot
		}
		if d.DriverVersion == desiredVersion {
			return Result{Passed: true, Message: "NDR.driverVersion 이 desiredVersion 과 일치"}, nil
		}
	}
	if hostVer != "" && hostVer != desiredVersion {
		// host kernel module 이 desiredVersion 과 다르면 driver-ds Pod 의 init container
		// (driver-manager) 가 모듈 swap 에 실패했음을 의미. 운영자에게 직접 조치 절차를 제공한다.
		msg := fmt.Sprintf(
			"host kernel module=%s ≠ desired=%s — driver-ds Pod 의 driver-manager init container 가 swap 실패. "+
				"조치: ssh <user>@%s; sudo systemctl stop dcgm-exporter; "+
				"sudo rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia; 그 후 driver-ds Pod 재시작",
			hostVer, desiredVersion, nodeName,
		)
		// needsReboot 이면 위 안내는 틀렸다. 목표 버전 모듈은 이미 디스크에 준비돼 있고 구 모듈이
		// 사용 중이라 삽입만 못 한 상태(계열 전환·같은 계열 patch 교체)라서 rmmod 로는 풀리지
		// 않는다 — 그 지시를 따르는 사람은 시간만 버린다. 교체 주체는 노드 재부팅뿐이고,
		// 그 재부팅은 상태기계가 RebootRequired 로 진행한다.
		if hostNeedsReboot {
			msg = fmt.Sprintf(
				"host kernel module=%s ≠ desired=%s — 목표 버전 모듈은 디스크에 준비됐고 구 모듈이 "+
					"사용 중이라 삽입만 못 한 상태(모듈 교체). 노드 재부팅으로만 교체되며 rmmod 로는 "+
					"풀리지 않는다 — operator 가 재부팅을 진행하므로 수동 조치 불필요 (node=%s)",
				hostVer, desiredVersion, nodeName,
			)
		}
		if hostDetail != "" {
			msg = fmt.Sprintf("%s (detail=%s)", msg, hostDetail)
		}
		return Result{Passed: false, Message: msg}, nil
	}
	return Result{
		Passed:  false,
		Message: "NDR.driverVersion 미보고",
	}, nil
}

// ─────────────────────────────────────────────
// DevicePluginValidator
// ─────────────────────────────────────────────

// DevicePluginValidator 는 device-plugin Pod 가 노드에서 ContainersReady 인지 검증한다.
// 통과 조건: 네임스페이스 전체에서 vendor 라벨/이름이 매칭되고 그 노드에 스케줄된
// 인스턴스가 ContainersReady=True 면 PASS(네임스페이스 하드코딩 금지 — Run 참고).
// timeout 60s — Pod 재시작/시작 시간 고려.
type DevicePluginValidator struct{}

// Name 은 metric/event label.
func (v *DevicePluginValidator) Name() string { return "DevicePlugin" }

// Timeout 은 caller wall-clock budget.
func (v *DevicePluginValidator) Timeout() time.Duration { return 60 * time.Second }

// Run 은 device-plugin Pod 의 Ready 여부를 1회 확인한다.
//   - vendor 가 비면 vendor 매칭 skip.
//   - Pod 미존재 → Passed=false (caller 재시도).
//   - Terminating Pod (DeletionTimestamp) 는 무시.
func (v *DevicePluginValidator) Run(
	ctx context.Context,
	c client.Client,
	nodeName, vendor, _ string,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// device-plugin 은 벤더별로 다른 네임스페이스에 상주한다(3rd-party=kube-system,
	// TT=kcloud=OperatorNamespace). 네임스페이스를 하드코딩하지 않고 전 네임스페이스를
	// 조회하되 아래 nodeName + isDevicePluginPod 필터로 정확히 스코프한다(ClusterRole pods list).
	var pods corev1.PodList
	if err := c.List(ctx, &pods); err != nil {
		return Result{}, err
	}
	found := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Spec.NodeName != nodeName {
			continue
		}
		if pod.DeletionTimestamp != nil {
			continue
		}
		if !isDevicePluginPod(pod, vendor) {
			continue
		}
		found = true
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.ContainersReady && cond.Status == corev1.ConditionTrue {
				return Result{Passed: true, Message: "device-plugin Pod ContainersReady"}, nil
			}
		}
	}
	if !found {
		return Result{Passed: false, Message: "device-plugin Pod 미스케줄 (재시도 대기)"}, nil
	}
	return Result{Passed: false, Message: "device-plugin Pod 가 ContainersReady 가 아님"}, nil
}

// ─────────────────────────────────────────────
// WorkloadValidator
// ─────────────────────────────────────────────

// WorkloadValidator 는 장치 1개를 요청하는 probe 파드를 띄워 벤더 명령의 종료 코드로
// "진짜 쓸 수 있는가" 를 본다. 한 번 호출이 한 번 관찰이다 — 파드가 없으면 만들고 대기,
// Succeeded/Failed 면 지우고 그 결과를, Pending 타임아웃이면 지우고 실패를 반환하며
// 체인이 10초마다 다시 부른다. 정책 opt-in(WorkloadProbe)일 때만 체인에 들어간다.
// 파드 이름은 (노드, desiredVersion)에 고정된다 — Succeeded 뒤 삭제가 실패하면 다음
// Run 도 같은 파드를 다시 보므로, 지워지지 않은 파드를 통과로 보고하지 않는다(의도적).
type WorkloadValidator struct {
	Probe     v1alpha1.WorkloadProbeSpec
	Namespace string
}

// Name 은 metric/event label.
func (v *WorkloadValidator) Name() string { return "Workload" }

// Timeout 은 caller wall-clock budget — Probe.TimeoutSeconds 미지정 시 120s.
func (v *WorkloadValidator) Timeout() time.Duration {
	if v.Probe.TimeoutSeconds > 0 {
		return time.Duration(v.Probe.TimeoutSeconds) * time.Second
	}
	return 120 * time.Second
}

// probeVersionLabel 은 probe 파드가 속한 desiredVersion 사이클을 적는 라벨이다. 이름에도
// desiredVersion 을 해시로 섞어 두지만(probePodName), 이름만으론 사람이 못 읽고 해시 충돌
// 같은 미래 리팩터 실수에도 끄떡없게 라벨로 한 번 더 명시적으로 확인한다(Run 참고).
const probeVersionLabel = "kcloud.ai/probe-version"

// labelValueRe 는 k8s 라벨 값 규칙이다(영숫자로 시작·끝, 중간은 영숫자/._- , 63자 이하).
var labelValueRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?$`)

// sanitizeLabelValue 는 desiredVersion 을 라벨 값으로 다듬는다. 드라이버 버전은 보통
// 숫자·점뿐이라 그대로 쓰면 사람이 읽기 좋다 — 라벨 규칙을 벗어나는 문자가 섞이거나
// 63자를 넘으면(식별 목적만 충족하면 되므로) 해시로 대체한다.
func sanitizeLabelValue(s string) string {
	if labelValueRe.MatchString(s) {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("h%x", sum[:8])
}

// probePodName 은 (노드, desiredVersion) 하나당 결정적인 이름이다(해시 접미 — 63자 제한
// 안에서 입력 길이와 무관). desiredVersion 을 해시에 섞는 이유: vendor+nodeName 만 해시하면
// 이전 사이클(다른 버전)의 Succeeded/Failed 파드가 같은 이름으로 남아 있을 때 이번 사이클이
// 그것을 자기 판정으로 잘못 재사용한다 — 아무것도 실행하지 않고 통과/실패를 내보내게 된다.
func probePodName(vendor, nodeName, desiredVersion string) string {
	sum := sha256.Sum256([]byte(nodeName + "\x00" + desiredVersion))
	return fmt.Sprintf("kcloud-%s-upgrade-probe-%x", vendor, sum[:4])
}

// Run 은 probe 파드 1개의 상태를 1회 관찰한다. vendor 별 리소스명 기본값은 두지 않는다 —
// furiosa 는 product(warboy/rngd)가 있어야 리소스명이 정해지는데 이 함수에는 그 정보가 없다
// (internal/intent.WholeDeviceResource 는 product 를 요구). 그래서 Probe.ResourceName 과 Probe.Image 는
// 둘 다 필수이고, 비면 정책 설정 오류로 미통과 처리한다 — CRD 에 MinLength 를 두지 않는 이유는
// CR 생성 자체를 막기보다 사람이 읽을 수 있는 사유를 Event/상태로 남기기 위해서다.
func (v *WorkloadValidator) Run(ctx context.Context, c client.Client, nodeName, vendor, desiredVersion string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if v.Probe.ResourceName == "" {
		return Result{Passed: false, Message: "workloadProbe.resourceName 이 비어 있다"}, nil
	}
	// 이미지도 기본값을 두지 않는다 — 벤더 CLI 가 든 이미지는 레지스트리마다 다르다. 가드가 없으면
	// c.Create 가 거절되고 Run 이 오류를 반환해 사유가 DUS·Event 가 아니라 로그에만 남는다(차트
	// 기본값이 `enabled:false, image:""` 라 켜기만 하고 이미지를 안 넣는 실수가 이 경로다).
	if v.Probe.Image == "" {
		return Result{Passed: false, Message: "workloadProbe.image 가 비어 있다"}, nil
	}
	key := client.ObjectKey{Namespace: v.Namespace, Name: probePodName(vendor, nodeName, desiredVersion)}
	var pod corev1.Pod
	err := c.Get(ctx, key, &pod)
	switch {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, v.probePod(key, nodeName, vendor, desiredVersion)); err != nil {
			return Result{}, err
		}
		return Result{Passed: false, Message: "workload probe 파드 생성 — 완료 대기"}, nil
	case err != nil:
		return Result{}, err
	}
	// 이름이 이미 (노드, desiredVersion) 에 묶여 있어 정상 경로로는 못 일어나지만, 해시 충돌이나
	// 수동 생성 같은 예외를 방어적으로 잡는다 — 이름만 믿지 않고 라벨로 한 번 더 확인한다.
	if pod.Labels[probeVersionLabel] != sanitizeLabelValue(desiredVersion) {
		msg := "이전 버전의 probe 파드 정리 — 재생성 대기"
		if err := c.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("%s (정리 실패: %v)", msg, err)
		}
		return Result{Passed: false, Message: msg}, nil
	}
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		// 지우지 못하면 통과시키지 않는다 — 다음 Run 이 같은 이름으로 이 파드를 다시 읽어
		// (또) Succeeded 로 판정하는 것 자체는 무해하지만, 지워지지 않는 파드를 "통과" 로
		// 보고하면 운영자가 정리 실패를 못 보고 넘긴다.
		if err := c.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			return Result{Passed: false, Message: fmt.Sprintf("probe 통과했으나 정리 실패: %v", err)}, nil
		}
		return Result{Passed: true, Message: "workload probe 종료 코드 0"}, nil
	case corev1.PodFailed:
		msg := "workload probe 실패(Failed) — 드라이버로 장치를 쓸 수 없다"
		if err := c.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("%s (정리 실패: %v)", msg, err)
		}
		return Result{Passed: false, Message: msg}, nil
	}
	if time.Since(pod.CreationTimestamp.Time) > v.Timeout() {
		msg := "workload probe 가 제한 시간 안에 끝나지 않음(스케줄·기동 지연)"
		if err := c.Delete(ctx, &pod); err != nil && !apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("%s (정리 실패: %v)", msg, err)
		}
		return Result{Passed: false, Message: msg}, nil
	}
	return Result{Passed: false, Message: "workload probe 실행 중"}, nil
}

// probePod 은 노드에 고정(NodeName)된 1회성 파드다. 모든 taint 를 허용해 cordon 에서도
// 뜬다(벤더 노드의 수동 taint 방어). ActiveDeadlineSeconds 를 Timeout() 초로 거는 이유:
// 검증 체인이 Workload 앞에서 끊기거나 상위 제한시간이 지나 Rollback·Failed 로 전이하면
// Run 이 다시 불리지 않아 Pending 파드가 남아 holdsDevice 가 점유자로 계속 세고 ACPP
// apply·CapReset quiesce 를 막는다. kubelet 이 스스로 Failed 로 끝내면 영향이 없다 —
// quiesce.go 호출부와 inventory.go 의 집계가 각각 독립적으로 터미널 파드를 빼므로, 한쪽만 고치면 어긋난다.
func (v *WorkloadValidator) probePod(key client.ObjectKey, nodeName, vendor, desiredVersion string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name,
			Labels: map[string]string{
				"app.kubernetes.io/name": "kcloud-upgrade-probe",
				"kcloud.ai/vendor":       vendor,
				probeVersionLabel:        sanitizeLabelValue(desiredVersion),
			}},
		Spec: corev1.PodSpec{
			NodeName: nodeName, RestartPolicy: corev1.RestartPolicyNever,
			ActiveDeadlineSeconds: ptr.To(int64(v.Timeout() / time.Second)),
			Tolerations:           []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{Name: "probe", Image: v.Probe.Image, Command: v.Probe.Command,
				Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
					corev1.ResourceName(v.Probe.ResourceName): resource.MustParse("1"),
				}}}},
		},
	}
}

// ─────────────────────────────────────────────
// 헬퍼
// ─────────────────────────────────────────────

// isDevicePluginPod 은 Pod 가 device-plugin 인지, (옵션) 특정 vendor 인지 판단한다. 매칭
// 규칙(state_machine.go deleteDevicePluginPods 와 동일): app.kubernetes.io/name 라벨 또는
// Pod 이름에 "device-plugin" 포함. vendor 가 비어있지 않으면 라벨/이름에 vendor 토큰도
// 포함되어야 한다.
func isDevicePluginPod(pod *corev1.Pod, vendor string) bool {
	matchedDevicePlugin := false
	for key, val := range pod.Labels {
		if key == "app.kubernetes.io/name" && strings.Contains(val, "device-plugin") {
			matchedDevicePlugin = true
			break
		}
	}
	if !matchedDevicePlugin && strings.Contains(pod.Name, "device-plugin") {
		matchedDevicePlugin = true
	}
	if !matchedDevicePlugin {
		return false
	}
	if vendor == "" {
		return true
	}
	// vendor 토큰 별칭: TT device-plugin 은 "kcloud-tt-device-plugin" 로 명명되어
	// "tenstorrent" 문자열을 포함하지 않는다. "tt" 를 별칭으로 허용한다.
	// ponytail: 단일 별칭. 벤더가 늘면 map 으로 승격.
	tokens := []string{strings.ToLower(vendor)}
	if strings.EqualFold(vendor, "tenstorrent") {
		tokens = append(tokens, "tt")
	}
	name := strings.ToLower(pod.Name)
	for _, t := range tokens {
		if strings.Contains(name, t) {
			return true
		}
	}
	for _, val := range pod.Labels {
		lv := strings.ToLower(val)
		for _, t := range tokens {
			if strings.Contains(lv, t) {
				return true
			}
		}
	}
	return false
}

// isNotFound 는 apierrors.IsNotFound 의 thin wrapper.
// 별도 함수로 두는 이유: 미래에 NDR 외 다른 sub-resource NotFound 도 처리할 여지.
func isNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
