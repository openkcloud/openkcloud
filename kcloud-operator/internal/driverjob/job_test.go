// ============================================================
// job_test.go: install Job 빌더 단위 테스트
// 상세: privileged/hostPID/OnFailure/ttl/backoff/RUN_MODE env/nodeName 고정,
//       PreStop·probe 제거(완료 시 rmmod 참사 방지), Furiosa secret,
//       imagePullSecrets, JobOverrides override, ownerReference(DIP) 검증.
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================

package driverjob

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driveradapter"
)

// 반복 리터럴 상수(goconst 회피).
const (
	tNvidia      = "nvidia"
	tFuriosa     = "furiosa"
	tFuriosaAuth = "furiosa-auth"
)

func basePolicy() *npuv1alpha1.DriverInstallPolicy {
	return &npuv1alpha1.DriverInstallPolicy{}
}

func TestRenderInstallJob_CoreShape(t *testing.T) {
	pol := basePolicy()
	pol.Name = "kcloud-nvidia"
	pol.Spec.Vendor = tNvidia
	pol.Spec.Model = "generic"
	pol.Spec.Driver.AllowDowngrade = false
	pol.Spec.Driver.SkipOnPassthrough = true

	job := RenderInstallJob(pol, "worker1", "reg/nvidia:580.126.09-v16", "580.126.09")

	if job.Namespace != Namespace {
		t.Errorf("namespace=%q, want %q", job.Namespace, Namespace)
	}
	// 결정적 이름
	if job.Name == "" || job.Name[:7] != "kcloud-" {
		t.Errorf("job name 형식 오류: %q", job.Name)
	}
	spec := job.Spec.Template.Spec
	if spec.NodeName != "worker1" {
		t.Errorf("nodeName=%q, want worker1 (노드 고정)", spec.NodeName)
	}
	if spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Errorf("restartPolicy=%q, want OnFailure", spec.RestartPolicy)
	}
	if !spec.HostPID {
		t.Error("hostPID=false, want true (nsenter 전제)")
	}
	if len(spec.Containers) != 1 {
		t.Fatalf("containers=%d, want 1", len(spec.Containers))
	}
	c := spec.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Error("privileged=true 아님")
	}
	if c.Image != "reg/nvidia:580.126.09-v16" {
		t.Errorf("image=%q, want desired image", c.Image)
	}

	// kernelImages 정책은 커널 태그 이미지를 image 인자로 넘긴다. RenderInstallJob 은 태그를
	// 해석하지 않고 받은 이미지를 그대로 컨테이너에 실어야 한다(선택은 상태기계가 이미 끝냈다).
	const kernelTagImg = "reg/kcloud/tenstorrent-driver-ds:2.11.0-k5.15.0-190-generic-v1"
	kJob := RenderInstallJob(pol, "worker1", kernelTagImg, "2.11.0")
	if kc := kJob.Spec.Template.Spec.Containers[0].Image; kc != kernelTagImg {
		t.Errorf("kernel-tagged image=%q, want %q (그대로)", kc, kernelTagImg)
	}

	// CRITICAL: PreStop rmmod / probe 는 없어야 한다 (완료 시 방금 설치한 모듈 언로드 참사 방지).
	if c.Lifecycle != nil {
		t.Error("Lifecycle(PreStop) 이 존재 — Job 완료 시 rmmod 참사 위험")
	}
	if c.LivenessProbe != nil || c.StartupProbe != nil {
		t.Error("probe 가 존재 — Job 에는 부적절")
	}

	// TTL / backoff / activeDeadline 기본값
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != defaultTTLSecondsAfterFinished {
		t.Errorf("ttl 기본값 불일치: %v", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != defaultBackoffLimit {
		t.Errorf("backoffLimit 기본값 불일치: %v", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != defaultActiveDeadlineSeconds {
		t.Errorf("activeDeadlineSeconds 기본값 불일치: %v", job.Spec.ActiveDeadlineSeconds)
	}
}

func TestRenderInstallJob_RunModeEnv(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	pol.Spec.Driver.VersionSource = "Host"
	pol.Spec.Driver.AllowDowngrade = true
	pol.Spec.Driver.SkipOnPassthrough = false

	job := RenderInstallJob(pol, "worker1", "img:tag", "1.2.3")
	env := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["RUN_MODE"] != "job" {
		t.Errorf("RUN_MODE=%q, want job (entrypoint 상주 루프 분기)", env["RUN_MODE"])
	}
	if env["DRIVER_VERSION"] != "1.2.3" {
		t.Errorf("DRIVER_VERSION=%q, want 1.2.3", env["DRIVER_VERSION"])
	}
	if env["VENDOR"] != tNvidia {
		t.Errorf("VENDOR=%q, want nvidia", env["VENDOR"])
	}
	if env["ALLOW_DOWNGRADE"] != "true" {
		t.Errorf("ALLOW_DOWNGRADE=%q, want true", env["ALLOW_DOWNGRADE"])
	}
	if env["VERSION_SOURCE"] != "Host" {
		t.Errorf("VERSION_SOURCE=%q, want Host", env["VERSION_SOURCE"])
	}
	if env["SKIP_ON_PASSTHROUGH"] != "false" {
		t.Errorf("SKIP_ON_PASSTHROUGH=%q, want false", env["SKIP_ON_PASSTHROUGH"])
	}
}

func TestRenderInstallJob_VersionSourceDefault(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	// VersionSource 미지정 → "Policy" fallback (하위호환)
	job := RenderInstallJob(pol, "n1", "img:tag", "1.0")
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "VERSION_SOURCE" && e.Value != "Policy" {
			t.Errorf("VERSION_SOURCE fallback=%q, want Policy", e.Value)
		}
	}
}

func TestRenderInstallJob_FuriosaSecret(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tFuriosa
	pol.Spec.Model = "warboy"
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.7.8")

	foundVol := false
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == tFuriosaAuth && v.Secret != nil && v.Secret.SecretName == driveradapter.AptAuthSecretName {
			foundVol = true
		}
	}
	if !foundVol {
		t.Error("furiosa-auth secret volume 미부착")
	}
	foundMount := false
	for _, m := range job.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == tFuriosaAuth && m.MountPath == "/secrets" {
			foundMount = true
		}
	}
	if !foundMount {
		t.Error("furiosa-auth mount 미부착")
	}
}

func TestRenderInstallJob_NoFuriosaSecretForNvidia(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.0")
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == tFuriosaAuth {
			t.Error("nvidia 에 furiosa-auth 가 부착됨")
		}
	}
}

// RNGD 는 공개 repo 라 furiosa-apt-auth secret 마운트가 없어야 한다 — secret 부재 클러스터에서
// install Job 파드가 스케줄 실패하는 버그를 Job 경로에 복제하지 않는다.
func TestRenderInstallJob_NoFuriosaSecretForRNGD(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tFuriosa
	pol.Spec.Model = "rngd"
	job := RenderInstallJob(pol, "rngd-1", "img:tag", "2026.1.0")
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Name == tFuriosaAuth {
			t.Error("RNGD 에 furiosa-auth 가 부착됨 (공개 repo — 마운트 불필요)")
		}
	}
	for _, m := range job.Spec.Template.Spec.Containers[0].VolumeMounts {
		if m.Name == tFuriosaAuth {
			t.Error("RNGD 컨테이너에 furiosa-auth 마운트가 존재")
		}
	}
}

func TestRenderInstallJob_HostMounts(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.0")
	want := map[string]bool{
		"host-modules": false, "host-src": false, "host-etc": false,
		"host-var": false, "device-plugins": false,
	}
	for _, m := range job.Spec.Template.Spec.Containers[0].VolumeMounts {
		if _, ok := want[m.Name]; ok {
			want[m.Name] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("host mount %q 누락", name)
		}
	}
}

func TestRenderInstallJob_JobOverrides(t *testing.T) {
	bl := int32(9)
	ttl := int32(120)
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	pol.Spec.JobOverrides = &npuv1alpha1.JobOverrides{
		BackoffLimit:            &bl,
		TTLSecondsAfterFinished: &ttl,
		ServiceAccountName:      "sa-driver",
		PriorityClassName:       "high",
	}
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.0")
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 9 {
		t.Errorf("backoffLimit override 미적용: %v", job.Spec.BackoffLimit)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 120 {
		t.Errorf("ttl override 미적용: %v", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.Template.Spec.ServiceAccountName != "sa-driver" {
		t.Errorf("SA override 미적용: %q", job.Spec.Template.Spec.ServiceAccountName)
	}
	if job.Spec.Template.Spec.PriorityClassName != "high" {
		t.Errorf("priorityClass override 미적용: %q", job.Spec.Template.Spec.PriorityClassName)
	}
}

func TestRenderInstallJob_ImagePullSecrets(t *testing.T) {
	pol := basePolicy()
	pol.Spec.Vendor = tNvidia
	pol.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "regcred"}}
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.0")
	if len(job.Spec.Template.Spec.ImagePullSecrets) != 1 ||
		job.Spec.Template.Spec.ImagePullSecrets[0].Name != "regcred" {
		t.Errorf("imagePullSecrets 미적용: %v", job.Spec.Template.Spec.ImagePullSecrets)
	}
}

func TestRenderInstallJob_OwnerIsDIP(t *testing.T) {
	pol := basePolicy()
	pol.Name = "my-dip"
	pol.UID = "uid-123"
	pol.Spec.Vendor = tNvidia
	job := RenderInstallJob(pol, "worker1", "img:tag", "1.0")
	if len(job.OwnerReferences) != 1 {
		t.Fatalf("ownerReferences=%d, want 1", len(job.OwnerReferences))
	}
	ref := job.OwnerReferences[0]
	if ref.Kind != "DriverInstallPolicy" || ref.Name != "my-dip" || string(ref.UID) != "uid-123" {
		t.Errorf("ownerReference 불일치: %+v", ref)
	}
}
