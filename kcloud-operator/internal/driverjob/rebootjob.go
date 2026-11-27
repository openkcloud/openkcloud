// ============================================================
// rebootjob.go: 커널 모듈 교체용 노드 재부팅 Job 렌더러
// 상세: install Job 의 축소판 — privileged/hostPID 로 host PID1 네임스페이스에서 systemctl reboot 를
// 실행한다. 노드가 재부팅되면 pod 은 자연 소멸하므로, 성공 판정은 Job 결과가 아니라 상태기계의
// 노드 Ready 전이로 한다.
// 생성일: 2026-07-20 | 수정일: 2026-10-13
// ============================================================
package driverjob

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

// RenderRebootJob 은 DriverInstallPolicy 소유 재부팅 Job 이다(기존 호출자 호환).
func RenderRebootJob(pol *npuv1alpha1.DriverInstallPolicy, nodeName, image string) *batchv1.Job {
	owner := metav1.OwnerReference{
		APIVersion: "npu.ai/v1alpha1", Kind: "DriverInstallPolicy", Name: pol.Name, UID: pol.UID,
	}
	return RenderRebootJobFor(owner, pol.Spec.ImagePullSecrets, nodeName, image)
}

// RenderRebootJobFor 는 대상 노드를 재부팅하는 privileged one-shot Job 을 소유자 중립으로 만든다
// (ACPP MIG mode enable 도 이 경로를 쓴다 — Ampere pending MIG 는 재부팅으로만 확정되므로).
// image 는 nsenter 를 포함한 이미지(driver-installer / kcloud-host-exec). Namespace 는 install Job 과 동일.
func RenderRebootJobFor(owner metav1.OwnerReference, pullSecrets []corev1.LocalObjectReference, nodeName, image string) *batchv1.Job {
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-node-reboot",
		"app.kubernetes.io/component": "node-reboot",
		"npu.ai/node":                 nodeName,
	}
	ttl := int32(300)
	backoff := int32(0) // 재부팅은 재시도 무의미(노드가 죽음) — 실패 시 상태기계가 재발행/타임아웃 처리.

	podSpec := corev1.PodSpec{
		NodeName:      nodeName, // 대상 노드 고정
		HostPID:       true,
		HostNetwork:   true,
		RestartPolicy: corev1.RestartPolicyNever,
		// cordoned/tainted 노드에도 스케줄되도록 모든 taint 허용(재부팅은 이미 drain 완료 노드 대상).
		Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
		Containers: []corev1.Container{{
			Name:            "reboot",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			// ponytail: 나이브하게 systemctl reboot 1발. graceful timeout·sysrq 강제 폴백은 hang 노드에서 필요해지면 추가.
			Command: []string{
				"nsenter", "--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid",
				"--", "systemctl", "reboot", "--no-wall",
			},
			SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
		}},
	}
	applyImagePullSecrets(&podSpec, pullSecrets)

	owner.Controller = boolPtr(true)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            naming.RebootJobName(nodeName),
			Namespace:       Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}
