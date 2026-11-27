// ============================================================
// preflightjob_test.go: 설치 전 점검 Job 렌더러 시험
// 상세: 이름·라벨·재시도 0·시한·TTL·command·종료 메시지 정책을 고정하고, install Job 렌더 결과가
//       바뀌지 않는지 함께 본다. 종료 코드 계약 값도 고정한다.
// 생성일: 2026-10-27 | 수정일: 2026-10-28
// ============================================================

package driverjob

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

const (
	tPreflightNode  = "worker1"
	tPreflightImage = "reg/nvidia-driver-ds:v9"
	tPreflightVer   = "580.178.04"
)

func preflightPolicy() *npuv1alpha1.DriverInstallPolicy {
	pol := basePolicy()
	pol.Name = "nvidia-gpu-ds"
	pol.UID = "uid-1"
	pol.Spec.Vendor = tNvidia
	pol.Spec.Model = "generic"
	pol.Spec.Driver = npuv1alpha1.DriverSpec{Mode: "job", Installer: npuv1alpha1.DriverInstallerAPT}
	return pol
}

func TestRenderPreflightJob_Shape(t *testing.T) {
	job := RenderPreflightJob(preflightPolicy(), tPreflightNode, tPreflightImage, tPreflightVer)
	if job.Name != naming.PreflightJobName(tNvidia, "generic", tPreflightNode) || job.Namespace != Namespace {
		t.Fatalf("name/ns = %s/%s", job.Namespace, job.Name)
	}
	if job.Labels["app.kubernetes.io/component"] != "driver-preflight" ||
		job.Spec.Template.Labels["app.kubernetes.io/component"] != "driver-preflight" {
		t.Errorf("component 라벨 = %v / %v", job.Labels, job.Spec.Template.Labels)
	}
	if *job.Spec.BackoffLimit != 0 || *job.Spec.ActiveDeadlineSeconds != 600 {
		t.Errorf("backoff=%d deadline=%d", *job.Spec.BackoffLimit, *job.Spec.ActiveDeadlineSeconds)
	}
	spec := job.Spec.Template.Spec
	if spec.RestartPolicy != corev1.RestartPolicyNever || spec.NodeName != tPreflightNode || !spec.HostPID {
		t.Errorf("restart=%s node=%s hostPID=%v", spec.RestartPolicy, spec.NodeName, spec.HostPID)
	}
	c := spec.Containers[0]
	if c.Name != PreflightContainerName || c.Image != tPreflightImage ||
		!reflect.DeepEqual(c.Command, []string{"/usr/local/bin/preflight.sh"}) {
		t.Errorf("container name=%s image=%s command=%v", c.Name, c.Image, c.Command)
	}
	if c.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
		t.Errorf("terminationMessagePolicy=%s", c.TerminationMessagePolicy)
	}
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Error("privileged 가 아니다")
	}
	if got := jobEnv(t, c.Env, "DRIVER_VERSION"); got != tPreflightVer {
		t.Errorf("DRIVER_VERSION=%q", got)
	}
	if len(job.OwnerReferences) != 1 || job.OwnerReferences[0].Name != "nvidia-gpu-ds" {
		t.Errorf("owner=%v", job.OwnerReferences)
	}
}

func TestRenderPreflightJob_InstallJobUnchanged(t *testing.T) {
	pol := preflightPolicy()
	_ = RenderPreflightJob(pol, tPreflightNode, tPreflightImage, tPreflightVer)
	inst := RenderInstallJob(pol, tPreflightNode, tPreflightImage, tPreflightVer)
	c := inst.Spec.Template.Spec.Containers[0]
	if inst.Labels["app.kubernetes.io/component"] != "driver-install" || c.Name != InstallContainerName ||
		len(c.Command) != 0 || inst.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Errorf("install Job 이 바뀌었다: component=%s name=%s command=%v restart=%s",
			inst.Labels["app.kubernetes.io/component"], c.Name, c.Command, inst.Spec.Template.Spec.RestartPolicy)
	}
}

// 정책의 TTL 이 0 이거나 짧으면 operator 가 결과를 읽기 전에 점검 Job 이 지워져 PreFlight 가 점검 Job 의
// 생성·삭제를 반복한다. 점검 Job 의 TTL 은 정책 값을 상속하지 않고 고정하며, install Job 은 정책 값을 쓴다.
func TestRenderPreflightJob_TTLIgnoresPolicyOverride(t *testing.T) {
	pol := preflightPolicy()
	zero := int32(0)
	pol.Spec.JobOverrides = &npuv1alpha1.JobOverrides{TTLSecondsAfterFinished: &zero}
	pre := RenderPreflightJob(pol, tPreflightNode, tPreflightImage, tPreflightVer)
	inst := RenderInstallJob(pol, tPreflightNode, tPreflightImage, tPreflightVer)
	if pre.Spec.TTLSecondsAfterFinished == nil || inst.Spec.TTLSecondsAfterFinished == nil {
		t.Fatalf("ttl 미설정: preflight=%v install=%v",
			pre.Spec.TTLSecondsAfterFinished, inst.Spec.TTLSecondsAfterFinished)
	}
	if got := *pre.Spec.TTLSecondsAfterFinished; got != 600 {
		t.Errorf("점검 Job ttl=%d, want 600 — 정책 값 0 을 상속하면 결과를 읽기 전에 지워진다", got)
	}
	if got := *inst.Spec.TTLSecondsAfterFinished; got != 0 {
		t.Errorf("install Job ttl=%d, want 0 — 정책 값을 그대로 써야 한다", got)
	}
	if got := *pol.Spec.JobOverrides.TTLSecondsAfterFinished; got != 0 {
		t.Errorf("렌더가 정책의 ttl 을 %d 로 바꿨다", got)
	}
}

func TestExitCodeHostPackageConflictMatchesInstaller(t *testing.T) {
	if ExitCodeHostPackageConflict != 3 {
		t.Errorf("ExitCodeHostPackageConflict=%d, 설치기 EXIT_HOLD_CONFLICT=3 과 같아야 한다", ExitCodeHostPackageConflict)
	}
}
