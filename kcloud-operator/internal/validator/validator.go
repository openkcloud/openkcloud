// ============================================================
// validator.go: 드라이버 업그레이드 검증 단계 (Validator) 인터페이스 + 구현
// 상세: architectural plan §4.4.3 — 단일 단계 handleValidating 을
//       단계별 책임으로 분리한다 (DriverModule / DevicePlugin / Workload).
//       각 Validator 는 controller-runtime client 와 기본 식별자만 받아
//       단일 검증 시도(Run)를 수행한다. caller 는 timeout 내에서 재시도하고
//       Event/Metric 을 발행한다.
// 생성일: 2026-04-27 | 수정일: 2026-04-28
// ============================================================

package validator

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// Validator 는 단일 검증 단계 인터페이스.
//
// 의미:
//   - Name 은 metric/event label 로 사용된다 (예: "DriverModule", "DevicePlugin").
//   - Timeout 은 controller wall-clock 예산 — caller 는 LastTransitionTime 부터
//     이 값을 초과하면 Failed/Rollback 으로 전이한다.
//   - Run 은 검증 1회 시도 — 통과/미통과/오류 셋 중 하나를 반환한다.
//     ctx cancel 시 즉시 반환해야 한다 (호출자가 reconcile timeout 으로 차단).
type Validator interface {
	Name() string
	Timeout() time.Duration
	Run(ctx context.Context, c client.Client, nodeName, vendor, desiredVersion string) (Result, error)
}

// ─────────────────────────────────────────────
// DriverModuleValidator
// ─────────────────────────────────────────────

// DriverModuleValidator 는 노드의 드라이버 커널 모듈이 desiredVersion 으로 로드되었는지
// NodeDeviceReport (NDR) 를 통해 검증한다.
//
// 통과 조건: NDR.status.devices[].driverVersion == desiredVersion (vendor 일치)
//   - DriverLoaded=true 도 함께 요구.
//
// timeout 30s — NDR collector 가 보통 수 초 내 갱신.
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
				"조치: ssh kcloud@%s; sudo systemctl stop dcgm-exporter; "+
				"sudo rmmod nvidia_uvm nvidia_drm nvidia_modeset nvidia; 그 후 driver-ds Pod 재시작",
			hostVer, desiredVersion, nodeName,
		)
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
//
// 통과 조건: kube-system namespace 에서 vendor 라벨/이름 매칭되는 Pod 중
//
//	노드에 스케줄된 인스턴스가 ContainersReady=True 면 PASS.
//
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
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.InNamespace("kube-system")); err != nil {
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
// WorkloadValidator (skeleton)
// ─────────────────────────────────────────────

// WorkloadValidator 는 GPU/NPU sample 워크로드 1개를 spawn 해 ResourceAllocated 까지
// 도달하는지 검증할 예정 (architectural plan §4.4.3).
// 본 작업에서는 인터페이스 정의 + skeleton 만 제공 — 실제 워크로드 spawn 은 후속 PR.
//
// timeout 120s — Pod 스케줄 + 컨테이너 시작 + 리소스 할당 시간 포함.
type WorkloadValidator struct{}

// Name 은 metric/event label.
func (v *WorkloadValidator) Name() string { return "Workload" }

// Timeout 은 caller wall-clock budget.
func (v *WorkloadValidator) Timeout() time.Duration { return 120 * time.Second }

// Run 은 현재 항상 Passed=true 를 반환한다 (skeleton).
//
// TODO(후속 PR): vendor 별 sample workload 를 spawn 하고 ResourceAllocated 확인.
func (v *WorkloadValidator) Run(
	ctx context.Context,
	_ client.Client,
	_, _, _ string,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Passed: true, Message: "WorkloadValidator skeleton — 실제 spawn 미구현"}, nil
}

// ─────────────────────────────────────────────
// 헬퍼
// ─────────────────────────────────────────────

// isDevicePluginPod 은 Pod 가 device-plugin 인지 (옵션) 특정 vendor 인지 판단한다.
//
// 매칭 규칙 (state_machine.go deleteDevicePluginPods 와 동일):
//   - app.kubernetes.io/name 라벨에 "device-plugin" 부분 포함, 또는
//   - Pod 이름에 "device-plugin" 포함.
//
// vendor 가 비어있지 않다면 라벨/이름에 vendor 토큰이 포함되어야 한다.
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
	v := strings.ToLower(vendor)
	if strings.Contains(strings.ToLower(pod.Name), v) {
		return true
	}
	for _, val := range pod.Labels {
		if strings.Contains(strings.ToLower(val), v) {
			return true
		}
	}
	return false
}

// isNotFound 는 apierrors.IsNotFound 의 thin wrapper.
// 별도 함수로 두는 이유: 미래에 NDR 외 다른 sub-resource NotFound 도 처리할 여지.
func isNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
