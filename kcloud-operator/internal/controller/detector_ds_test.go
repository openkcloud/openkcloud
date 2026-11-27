// ============================================================
// detector_ds_test.go: node-manager DaemonSet 에 PodResources 읽기 사이드카가 붙는지 고정
// 상세: kubelet pod-resources 소켓은 root 소유(디렉터리 drwxr-x---, 소켓 srwxr-xr-x 모두
// root:root)라 detector(비특권 nonroot)가 직접 읽지 못한다. 분리한 사이드카 컨테이너가 uid 0
// 으로 소켓을 읽어 메모리 emptyDir 에 쓰고, detector 는 그 emptyDir 만 읽기 전용으로 받는다.
// detector 본체의 비특권 보안 컨텍스트와 호스트 소켓 비접근도 함께 고정한다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// 컨테이너 둘(detector/podresources)의 SecurityContext·마운트를 각각 전부 고정하는
// 단일 회귀 가드라 분기가 많다. 쪼개면 "둘이 함께 맞는지"가 아니라 "따로 맞는지"를
// 보게 되어 목적이 바뀐다.
// nolint:gocyclo
func TestRenderDetectorDS_PodResourcesSidecar(t *testing.T) {
	ds := renderDetectorDS("example/node-manager:test", nil)
	var detector, sidecar *corev1.Container
	for i := range ds.Spec.Template.Spec.Containers {
		switch ds.Spec.Template.Spec.Containers[i].Name {
		case "detector":
			detector = &ds.Spec.Template.Spec.Containers[i]
		case "podresources":
			sidecar = &ds.Spec.Template.Spec.Containers[i]
		}
	}
	if detector == nil || sidecar == nil {
		t.Fatalf("컨테이너 둘(detector, podresources)이어야 한다: %+v", ds.Spec.Template.Spec.Containers)
	}
	if sc := detector.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" ||
		sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("detector 는 여전히 비특권·nonroot·권한 상승 금지·읽기 전용 rootfs·능력 없음·RuntimeDefault seccomp 여야 한다: %+v", sc)
	}
	sc := sidecar.SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 0 || sc.Privileged != nil && *sc.Privileged ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" ||
		sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem ||
		sc.RunAsNonRoot == nil || *sc.RunAsNonRoot ||
		sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("사이드카는 uid 0·능력 없음·읽기 전용 rootfs·비특권·권한 상승 금지·RuntimeDefault seccomp 여야 한다: %+v", sc)
	}
	if sidecar.Image != detector.Image || len(sidecar.Command) != 1 || sidecar.Command[0] != "/podresources-writer" ||
		len(sidecar.Args) != 1 || sidecar.Args[0] != "--podresources-writer" {
		t.Fatalf("사이드카는 같은 이미지에 /podresources-writer 실행·--podresources-writer 인자: image=%q command=%v args=%v",
			sidecar.Image, sidecar.Command, sidecar.Args)
	}
	// writer 모드는 NODE_NAME 을 읽지 않는다 — env 를 아예 주지 않는다.
	if len(sidecar.Env) != 0 {
		t.Fatalf("사이드카는 env 가 없어야 한다: %+v", sidecar.Env)
	}
	mounts := map[string]corev1.VolumeMount{}
	for _, m := range sidecar.VolumeMounts {
		mounts[m.MountPath] = m
	}
	if m, ok := mounts["/var/lib/kubelet/pod-resources"]; !ok || !m.ReadOnly {
		t.Fatalf("사이드카의 pod-resources 마운트는 읽기 전용이어야 한다: %+v", mounts)
	}
	if m, ok := mounts["/run/podresources"]; !ok || m.ReadOnly {
		t.Fatalf("사이드카의 공유 디렉터리는 쓰기 가능이어야 한다: %+v", mounts)
	}
	var detectorShare *corev1.VolumeMount
	for i := range detector.VolumeMounts {
		if detector.VolumeMounts[i].MountPath == "/run/podresources" {
			detectorShare = &detector.VolumeMounts[i]
		}
	}
	if detectorShare == nil || !detectorShare.ReadOnly {
		t.Fatal("detector 는 공유 디렉터리를 읽기 전용으로 받아야 한다")
	}
	for _, m := range detector.VolumeMounts {
		if m.MountPath == "/var/lib/kubelet/pod-resources" {
			t.Fatal("detector 에는 소켓 마운트를 주지 않는다 — 읽을 수도 없고 권한 범위만 넓어진다")
		}
	}
	vols := map[string]corev1.Volume{}
	for _, v := range ds.Spec.Template.Spec.Volumes {
		vols[v.Name] = v
	}
	share := vols["podresources-share"]
	if share.EmptyDir == nil || share.EmptyDir.Medium != corev1.StorageMediumMemory ||
		share.EmptyDir.SizeLimit == nil || share.EmptyDir.SizeLimit.Cmp(resource.MustParse("1Mi")) != 0 {
		t.Fatalf("공유 볼륨은 메모리 emptyDir, 상한 1Mi 여야 한다: %+v", share.VolumeSource)
	}
	sock := vols["kubelet-podresources"]
	if sock.HostPath == nil || sock.HostPath.Path != "/var/lib/kubelet/pod-resources" ||
		sock.HostPath.Type == nil || *sock.HostPath.Type != corev1.HostPathDirectoryOrCreate {
		t.Fatalf("소켓 볼륨은 hostPath /var/lib/kubelet/pod-resources, DirectoryOrCreate 여야 한다: %+v", sock.VolumeSource)
	}
}
