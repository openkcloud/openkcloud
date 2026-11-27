// ============================================================
// hostcmdjob.go: 호스트 PID1 네임스페이스에서 명령 하나를 실행하는 privileged one-shot Job 렌더러
// 상세: 재부팅 Job(rebootjob.go)과 같은 뼈대이되 명령·이름을 받는다. DeviceReset 이 descriptor 의
// resetCommand 를 실행할 때 쓴다. backoffLimit 0 — 리셋 명령을 자동으로 다시 돌리지 않는다.
// 성공·실패는 Job status 로 판정한다.
// 생성일: 2026-09-30 | 수정일: 2026-10-11
// ============================================================
package driverjob

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RenderDeviceResetJobFor 는 nodeName 에서 command 를 nsenter 로 실행하는 Job 이다.
// image 는 nsenter 를 포함한 이미지(kcloud-host-exec). Namespace 는 install Job 과 같다.
//
// 이름이 용도를 좁게 가리키는 이유: component 라벨이 "device-reset" 으로 고정돼 있고
// internal/uninstall 이 그 라벨로 Job 을 지운다. 일반적인 이름은 다른 용도의 두 번째 호출자를
// 불러들이고, 그러면 uninstall 이 그 Job 까지 함께 지운다.
func RenderDeviceResetJobFor(pullSecrets []corev1.LocalObjectReference, nodeName, image, name string, command []string) *batchv1.Job {
	labels := map[string]string{
		"app.kubernetes.io/name":      "kcloud-host-command",
		"app.kubernetes.io/component": "device-reset",
		"npu.ai/node":                 nodeName,
	}
	ttl := int32(300)
	backoff := int32(0)
	args := append([]string{"nsenter", "--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--"}, command...)
	podSpec := corev1.PodSpec{
		NodeName: nodeName,
		HostPID:  true,
		// hostNetwork 는 두지 않는다 — nsenter --net 이 이미 호스트 네트워크 네임스페이스로 들어간다.
		RestartPolicy: corev1.RestartPolicyNever,
		// cordoned/tainted 노드에도 스케줄되어야 한다 — 리셋 대상 노드는 이미 비워 둔 상태다.
		Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
		Containers: []corev1.Container{{
			Name:            "command",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         args,
			SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
		}},
	}
	applyImagePullSecrets(&podSpec, pullSecrets)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			BackoffLimit:            &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
}
