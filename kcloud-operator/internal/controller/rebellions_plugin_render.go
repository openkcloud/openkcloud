// ============================================================
// rebellions_plugin_render.go: Rebellions device plugin 컨테이너·볼륨의 판별 렌더링
// 상세: v0.3 은 SR-IOV 기반 구판 구성(인자, /etc/pcidp ConfigMap, 9개 마운트)에 rbln-smi 탐색용 PATH 를 더한다.
//       v0.4 는 신판 구성(인자 없음, 환경변수, 4개 마운트, gRPC liveness)이다. 두 판 모두 init container 용
//       cdi 볼륨을 둔다. DaemonSet 조립과 노드 배치 규칙은 라인별 ensureRebellionsDevicePlugin 이 맡는다.
//       1.34 라인과 1.28 라인이 같은 파일을 쓴다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// rbllnsV03Path 는 v0.3.6 이미지 기본 PATH 뒤에 호스트 /usr/bin·/usr/local/bin 마운트를 붙인 값이다.
	// 구판은 rbln-smi 를 /usr/bin/rbln-smi 다음에 PATH 에서만 찾는다(이미지 바이너리 문자열 확인).
	// 파드의 PATH 는 이미지 값을 통째로 바꾸므로 기본값을 그대로 앞에 둔다.
	rbllnsV03Path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/host/usr/bin:/host/driver/usr/bin"
	// rbllnsHealthPort 는 신판의 gRPC 상태 확인 포트 기본값이다(벤더 차트 livenessProbe 와 같다).
	rbllnsHealthPort = 51515
)

// rbllnsPluginContainer 는 판에 맞는 플러그인 컨테이너다.
func rbllnsPluginContainer(gen, image string) corev1.Container {
	c := corev1.Container{
		Name:            rbllnsDaemonSetName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			Privileged: boolPtr(true),
			RunAsUser:  int64Ptr(0),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("250m"),
				corev1.ResourceMemory: resource.MustParse("40Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("200Mi"),
			},
		},
	}
	if gen == rbllnsGenV04 {
		svc := "liveness"
		// 신판은 위치 인자나 모르는 플래그가 있으면 기동하지 않는다 — 인자를 주지 않고 이미지 진입점을 쓴다.
		// generic 이름을 끄면 ATOM+ 는 rebellions.ai/ATOM 으로 광고된다(구판과 같은 자원 이름).
		c.Env = []corev1.EnvVar{{Name: "USE_GENERIC_RESOURCE_NAME", Value: "false"}}
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler:     corev1.ProbeHandler{GRPC: &corev1.GRPCAction{Port: rbllnsHealthPort, Service: &svc}},
			PeriodSeconds:    10,
			FailureThreshold: 3,
		}
		c.VolumeMounts = []corev1.VolumeMount{
			{Name: "devicesock", MountPath: "/var/lib/kubelet/device-plugins"},
			{Name: "host-dev", MountPath: "/dev"},
			{Name: "host-usr-bin", MountPath: "/host/usr/bin", ReadOnly: true},
			{Name: "host-driver-usr-bin", MountPath: "/host/driver/usr/bin", ReadOnly: true},
		}
		return c
	}
	c.Args = []string{"--log-dir=device-plugin", "--log-level=10"}
	c.Env = []corev1.EnvVar{{Name: "PATH", Value: rbllnsV03Path}}
	c.VolumeMounts = []corev1.VolumeMount{
		{Name: "devicesock", MountPath: "/var/lib/kubelet/device-plugins"},
		{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
		{Name: "log", MountPath: "/var/log"},
		{Name: "config-volume", MountPath: "/etc/pcidp"},
		{Name: "device-info", MountPath: "/var/run/k8s.cni.cncf.io/devinfo/dp"},
		{Name: "host-usr-bin", MountPath: "/host/usr/bin", ReadOnly: true},
		{Name: "host-driver-usr-bin", MountPath: "/host/driver/usr/bin", ReadOnly: true},
		{Name: "host-dev", MountPath: "/dev"},
		{Name: "host-sys", MountPath: "/sys"},
	}
	return c
}

// rbllnsPluginVolumes 는 판에 맞는 볼륨이다. 두 판 모두 init container 용 cdi 볼륨을 끝에 둔다.
func rbllnsPluginVolumes(gen, cmName string) []corev1.Volume {
	hostPath := func(name, path string) corev1.Volume {
		return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: path}}}
	}
	dirOrCreate := corev1.HostPathDirectoryOrCreate
	cdi := corev1.Volume{Name: rbllnsCDIVolumeName, VolumeSource: corev1.VolumeSource{
		HostPath: &corev1.HostPathVolumeSource{Path: rbllnsCDIHostDir, Type: &dirOrCreate}}}
	if gen == rbllnsGenV04 {
		return []corev1.Volume{
			hostPath("devicesock", "/var/lib/kubelet/device-plugins"),
			hostPath("host-dev", "/dev"),
			hostPath("host-usr-bin", "/usr/bin"),
			hostPath("host-driver-usr-bin", "/usr/local/bin"),
			cdi,
		}
	}
	return []corev1.Volume{
		hostPath("devicesock", "/var/lib/kubelet/device-plugins"),
		hostPath("plugins-registry", "/var/lib/kubelet/plugins_registry"),
		hostPath("log", "/var/log"),
		{Name: "config-volume", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: cmName}}}},
		hostPath("device-info", "/var/run/k8s.cni.cncf.io/devinfo/dp"),
		hostPath("host-usr-bin", "/usr/bin"),
		hostPath("host-driver-usr-bin", "/usr/local/bin"),
		hostPath("host-dev", "/dev"),
		hostPath("host-sys", "/sys"),
		cdi,
	}
}
