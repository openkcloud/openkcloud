// ============================================================
// rebootjob_test.go: 노드 재부팅 Job 렌더러 테스트
// 상세: 소유자 중립 렌더러(RenderRebootJobFor)와 DriverInstallPolicy 위임(RenderRebootJob)이
//
//	같은 podSpec 을 내는지 — ACPP MIG mode enable 이 이 경로를 재사용해도 기존 동작이 안 바뀜을 고정.
//
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package driverjob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestRenderRebootJobForAcceptsArbitraryOwner(t *testing.T) {
	owner := metav1.OwnerReference{APIVersion: "npu.ai/v1alpha1", Kind: "AcceleratorPartitionPolicy", Name: "acpp-1", UID: "uid-1"}
	job := RenderRebootJobFor(owner, nil, "worker1", "harbor/kcloud/driver-installer:v1")
	if job.OwnerReferences[0].Kind != "AcceleratorPartitionPolicy" {
		t.Fatalf("owner kind = %s", job.OwnerReferences[0].Kind)
	}
	if job.Spec.Template.Spec.NodeName != "worker1" {
		t.Fatalf("nodeName = %s", job.Spec.Template.Spec.NodeName)
	}
	if !*job.Spec.Template.Spec.Containers[0].SecurityContext.Privileged {
		t.Fatalf("reboot job must be privileged")
	}
	if job.Labels["app.kubernetes.io/component"] != "node-reboot" {
		t.Fatalf("component label = %s", job.Labels["app.kubernetes.io/component"])
	}
}

// TestRenderRebootJobPreservesDriverInstallPolicyBehavior 는 기존 호출자(드라이버 교체 재부팅)의
// 렌더 결과가 추출 리팩터로 바뀌지 않았음을 고정한다.
func TestRenderRebootJobPreservesDriverInstallPolicyBehavior(t *testing.T) {
	pol := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dip-1", UID: "dip-uid"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{ImagePullSecrets: []corev1.LocalObjectReference{{Name: "harbor"}}},
	}
	job := RenderRebootJob(pol, "worker1", "img:v1")
	o := job.OwnerReferences[0]
	if o.Kind != "DriverInstallPolicy" || o.Name != "dip-1" || string(o.UID) != "dip-uid" || !*o.Controller {
		t.Fatalf("owner = %+v", o)
	}
	spec := job.Spec.Template.Spec
	if !spec.HostPID || !spec.HostNetwork || spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("podSpec regressed: %+v", spec)
	}
	if len(spec.ImagePullSecrets) != 1 || spec.ImagePullSecrets[0].Name != "harbor" {
		t.Fatalf("imagePullSecrets = %+v", spec.ImagePullSecrets)
	}
	if *job.Spec.BackoffLimit != 0 || *job.Spec.TTLSecondsAfterFinished != 300 {
		t.Fatalf("backoff/ttl regressed: %d/%d", *job.Spec.BackoffLimit, *job.Spec.TTLSecondsAfterFinished)
	}
}
