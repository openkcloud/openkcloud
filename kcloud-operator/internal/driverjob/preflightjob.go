// ============================================================
// preflightjob.go: NVIDIA 드라이버 설치 전 점검 Job 렌더러와 hold 충돌 종료 코드
// 상세: install Job 과 같은 이미지·env·노드로 설치기 preflight.sh 만 실행한다(재시도 0, 600초 시한,
//       TTL 은 정책 값과 무관하게 기본값 고정). 종료 코드 3 은 설치기 entrypoint.sh 의
//       EXIT_HOLD_CONFLICT 와 같은 계약이다.
// 생성일: 2026-10-27 | 수정일: 2026-10-27
// ============================================================

package driverjob

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/naming"
)

// ExitCodeHostPackageConflict 는 hold 된 호스트 패키지가 드라이버 설치를 막을 때 설치기가 내는 종료
// 코드다. images 저장소 driver-installer/nvidia/entrypoint.sh 의 EXIT_HOLD_CONFLICT 와 같아야 한다.
// 설치 전 점검 컨테이너와 install 컨테이너가 같은 값을 쓴다.
const ExitCodeHostPackageConflict int32 = 3

// PreflightContainerName 은 설치 전 점검 Job 의 컨테이너 이름이다.
const PreflightContainerName = "driver-preflight"

const (
	preflightCommand         = "/usr/local/bin/preflight.sh"
	preflightComponent       = "driver-preflight"
	preflightDeadlineSeconds = int64(600)
)

// RenderPreflightJob 은 install Job 과 같은 정책·노드·이미지·버전으로 설치 전 점검 Job 을 만든다.
// 설치기의 preflight.sh 만 실행하고 재시도하지 않는다. 그 파일이 없는 옛 이미지는 컨테이너가
// 시작되지 않아 실패로 끝나며, 호출부는 그 실패를 판정 불가로 다룬다.
func RenderPreflightJob(pol *npuv1alpha1.DriverInstallPolicy, nodeName, image, version string) *batchv1.Job {
	job := renderJob(pol, nodeName, image, version, false)
	job.Name = naming.PreflightJobName(pol.Spec.Vendor, pol.Spec.Model, nodeName)
	// renderJob 은 Job 과 파드 템플릿에 같은 라벨 맵을 쓴다 — 한 번 바꾸면 둘 다 바뀐다.
	job.Labels["app.kubernetes.io/component"] = preflightComponent
	backoff := int32(0)
	deadline := preflightDeadlineSeconds
	// operator 가 결과를 읽고 Job 을 지우므로 TTL 은 버려진 Job 정리용이다. 정책 값(0 등)은 따르지 않는다.
	ttl := defaultTTLSecondsAfterFinished
	job.Spec.BackoffLimit = &backoff
	job.Spec.ActiveDeadlineSeconds = &deadline
	job.Spec.TTLSecondsAfterFinished = &ttl
	spec := &job.Spec.Template.Spec
	spec.RestartPolicy = corev1.RestartPolicyNever
	c := &spec.Containers[0]
	c.Name = PreflightContainerName
	c.Command = []string{preflightCommand}
	c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
	return job
}
