// ============================================================
// dra_driver.go: 벤더 DRA 드라이버 배포·회수
// 상세: 벤더 chart 를 그대로 설치하지 않고 우리 라벨·이미지·네임스페이스로 옮겨 만든다 —
// chart 의 affinity 가 NFD 와 벤더 탐지기 라벨 셋을 요구해 우리 클러스터에서는 어느
// 노드에도 스케줄되지 않기 때문이다. 사설 미러 경로는 여기서 조립하지 않는다 — 레지스트리는
// helm 이 조립해 완성된 경로를 spec 으로 내려준다.
// 생성일: 2026-08-07 | 수정일: 2026-10-13
// ============================================================

package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

const (
	// draDSNameFuriosa 는 배포되는 DaemonSet 이름이다.
	draDSNameFuriosa = "kcloud-furiosa-dra-driver"
	// draDriverNameFuriosa 는 이 드라이버가 발행물에 쓰는 spec.driver 값이다.
	// 발행 관측이 이 값으로 ResourceSlice 를 고른다.
	draDriverNameFuriosa = "npu.furiosa.ai"
	// draDriverImageFuriosaDefault 는 spec 미지정 시 기본 이미지다.
	// 벤더 chart 2026.1.1 이 쓰는 것과 같다.
	draDriverImageFuriosaDefault = "docker.io/furiosaai/furiosa-dra-driver:2026.1.1"
	// draDriverCommandFuriosa 는 이미지의 CMD 다. 이 이미지에는 ENTRYPOINT 가 없어서
	// args 만 주면 CMD 가 통째로 밀려 바이너리가 사라져 exec 가 실패한다.
	draDriverCommandFuriosa = "./dra-kubelet-plugin"
	// draCDIRoot 는 드라이버가 CDI 스펙을 쓰는 경로다. init container 가 containerd 에
	// 등록하는 경로와 같아야 한다.
	draCDIRoot = "/var/run/cdi"
	// draNamespace 는 드라이버 오브젝트를 두는 네임스페이스다. 벤더 chart 는
	// kube-system 이지만 우리 operand 규약을 따른다.
	draNamespace = "kcloud"

	// draDSNameRebellions 는 배포되는 DaemonSet 이름이다.
	draDSNameRebellions = "kcloud-rebellions-dra-driver"
	// draDriverNameRebellions 는 이 드라이버가 발행물에 쓰는 spec.driver 값이다.
	// 벤더 chart 의 DeviceClass 이름과 같다(reference/rbln-k8s-dra-driver).
	draDriverNameRebellions = "npu.rebellions.ai"
	// draDriverImageRebellionsDefault 는 spec 미지정 시 기본 이미지다. 벤더 chart 기본값
	// (appVersion v0.2.1)은 레지스트리에 없고, latest 는 재현 불가라 v0.2.0 을 쓴다.
	draDriverImageRebellionsDefault = "docker.io/rebellions/k8s-dra-driver-npu:v0.2.0"
	// draHealthcheckPortRebellions 은 바이너리 헬스체크 포트다. 기본값 -1 은 꺼짐을
	// 뜻한다. 벤더 chart 기본값 51515 를 그대로 쓴다(NVIDIA 렌더러의 51516 과 겹치지 않음).
	draHealthcheckPortRebellions int32 = 51515
)

// vendorRebellions 는 DRA 축에서 이 벤더를 가리키는 이름이다.
const vendorRebellions = "rebellions"

// renderFuriosaDRADriver 는 Furiosa DRA 드라이버 오브젝트 다섯을 만든다.
// ServiceAccount · ClusterRole · ClusterRoleBinding · DaemonSet · DeviceClass.
func renderFuriosaDRADriver(policy *npuv1alpha1.NPUClusterPolicy) []client.Object {
	spec := policy.Spec.Furiosa.Rngd.DRA
	labels := map[string]string{
		"app.kubernetes.io/name":      draDSNameFuriosa,
		"app.kubernetes.io/component": "dra-driver",
	}

	sa := &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameFuriosa, Namespace: draNamespace, Labels: labels},
	}

	// 벤더 chart 의 ClusterRole 규칙 8줄을 그대로 옮긴다. resourceclaims/driver 의
	// associated-node:update 는 1.34 세대 권한이고, 빠지면 드라이버가 조용히 실패한다.
	cr := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameFuriosa, Labels: labels},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims"},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims/status"},
				Verbs: []string{"update", "patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims/driver"},
				Verbs: []string{"associated-node:update", "associated-node:patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaimtemplates"},
				Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaimtemplates/status"},
				Verbs: []string{"update", "patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices"},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices/status"},
				Verbs: []string{"update", "patch"}},
		},
	}

	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameFuriosa, Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: draDSNameFuriosa},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: draDSNameFuriosa, Namespace: draNamespace}},
	}

	// 벤더 chart 의 namespace Role 은 pods·deployments·jobs 전권(*)이다. 그대로 옮기지
	// 않는다 — 장치 발행자가 그 권한을 쓸 이유가 없고, 옮기려면 operator 자신이 먼저
	// 그 전권을 들고 있어야 한다(권한 상승 방지 규칙). 필요가 드러나면 그때 최소로 준다.

	dc := &resourcev1.DeviceClass{
		TypeMeta:   metav1.TypeMeta{APIVersion: "resource.k8s.io/v1", Kind: "DeviceClass"},
		ObjectMeta: metav1.ObjectMeta{Name: draDriverNameFuriosa, Labels: labels},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{
				CEL: &resourcev1.CELDeviceSelector{
					Expression: "device.driver == '" + draDriverNameFuriosa + "'",
				},
			}},
		},
	}

	return []client.Object{sa, cr, crb, renderFuriosaDRADaemonSet(spec, labels), dc}
}

// renderFuriosaDRADaemonSet 은 드라이버 DaemonSet 을 만든다. 벤더 chart 의
// 컨테이너·볼륨은 그대로 쓰고 스케줄 조건만 우리 것으로 바꾼다.
func renderFuriosaDRADaemonSet(spec *npuv1alpha1.DRASpec,
	labels map[string]string) *appsv1.DaemonSet {
	image := draDriverImageFuriosaDefault
	if spec != nil && spec.Image != "" {
		image = spec.Image
	}
	args := []string{"--node-name=$(NODE_NAME)", "--cdi-root=$(CDI_ROOT)"}
	if spec != nil && len(spec.Args) > 0 {
		args = spec.Args
	}
	env := []corev1.EnvVar{
		{Name: "CDI_ROOT", Value: draCDIRoot},
		{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
		}},
	}
	if spec != nil {
		env = mergeEnv(env, spec.Env)
	}

	noEscalate := false
	hostPathDir := corev1.HostPathDirectoryOrCreate
	hostVol := func(name, path string) corev1.Volume {
		return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: path, Type: &hostPathDir},
		}}
	}

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: draDSNameFuriosa, Namespace: draNamespace, Labels: labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// 벤더 chart 의 affinity 는 NFD 와 벤더 탐지기 라벨 셋을 요구한다.
					// 우리는 둘 다 안 쓰므로 자립 라벨로 갈아 끼운다.
					NodeSelector:       map[string]string{vendorPresentLabel(vendorRngd): labelValueTrue},
					Affinity:           nil,
					ServiceAccountName: draDSNameFuriosa,
					PriorityClassName:  "system-node-critical",
					// nsenter -t 1 이 호스트 PID 네임스페이스를 요구한다.
					HostPID:        true,
					Tolerations:    []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					InitContainers: []corev1.Container{renderCDIInitContainer()},
					Containers: []corev1.Container{{
						Name:    "plugin",
						Image:   image,
						Command: []string{draDriverCommandFuriosa},
						Args:    args,
						Env:     env,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: &noEscalate,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
							{Name: "plugins", MountPath: "/var/lib/kubelet/plugins"},
							{Name: "cdi", MountPath: draCDIRoot},
							{Name: "dev-fs", MountPath: "/dev"},
							{Name: "sys-fs", MountPath: "/sys"},
						},
					}},
					Volumes: []corev1.Volume{
						hostVol("plugins-registry", "/var/lib/kubelet/plugins_registry"),
						hostVol("plugins", "/var/lib/kubelet/plugins"),
						hostVol("cdi", draCDIRoot),
						hostVol("dev-fs", "/dev"),
						hostVol("sys-fs", "/sys"),
						hostVol("host-etc-containerd", "/etc/containerd"),
					},
				},
			},
		},
	}
	// 이 DaemonSet 은 RNGD 전용이지만(nodeSelector 가 rngd.present 만 본다) furiosa(Warboy)
	// 쪽에는 이를 대신할 DRA DaemonSet 이 따로 없다 — 되돌린 뒤 두 벤더 키 중 어느 쪽이
	// retired 든 이 DaemonSet 이 그 노드를 피하게 둘 다 건다(device-plugin 쪽 통합 DS 가
	// excludeDRAOwnedNodes 를 furiosa·rngd 둘 다에 거는 것과 같은 이유).
	excludeDRARetiredNodes(&ds.Spec.Template.Spec, vendorFuriosa)
	excludeDRARetiredNodes(&ds.Spec.Template.Spec, vendorRngd)
	return ds
}

// mergeEnv 는 base 에 extra 를 더한다. 같은 이름이면 extra 가 이긴다.
func mergeEnv(base, extra []corev1.EnvVar) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(base)+len(extra))
	override := map[string]corev1.EnvVar{}
	for _, e := range extra {
		override[e.Name] = e
	}
	for _, e := range base {
		if o, ok := override[e.Name]; ok {
			out = append(out, o)
			delete(override, e.Name)
			continue
		}
		out = append(out, e)
	}
	for _, e := range extra {
		if _, still := override[e.Name]; still {
			out = append(out, e)
			delete(override, e.Name)
		}
	}
	return out
}

// renderCDIInitContainer 는 containerd 에 CDI 를 켜는 init container 다. 이미 켜져 있으면
// 아무것도 하지 않는다 — 매번 재시작하면 노드가 흔들린다. 판정과 기록 위치는 둘 다 "유효
// 설정" 기준이다 — 같은 plugin 테이블을 다시 선언하는 drop-in 이 있으면 그쪽이 이기므로,
// base 가 conf.d 를 import 하면 거기에 쓰고 마지막에 실제로 켜졌는지 다시 확인한다.
// 이미지는 kcloud-host-exec(HOST_EXEC_IMAGE) 재사용이다 — python3 은 없고 awk·grep·nsenter
// 는 있다. systemctl 도 없지만 nsenter 로 호스트 것을 부르므로 무관하다.
func renderCDIInitContainer() corev1.Container {
	const script = `set -eu
DIR=/host/etc/containerd
CFG=$DIR/config.toml
SEC='[plugins."io.containerd.grpc.v1.cri"]'

# 유효 설정이 이미 켜져 있으면 아무것도 하지 않는다. 파일이 아니라 containerd 가
# 실제로 읽은 값을 본다 — 파일에 true 가 있어도 뒤 파일이 덮으면 꺼져 있다.
if nsenter -t 1 -m -u -i -n -p -- containerd config dump 2>/dev/null |
     grep -q 'enable_cdi[[:space:]]*=[[:space:]]*true'; then
  echo "CDI already enabled (effective) - no-op"
  exit 0
fi

# 이 plugin 테이블을 선언하는 파일 중 **마지막에 로드되는 것**을 고른다.
# containerd 의 import 병합은 같은 plugin 테이블을 재선언하면 앞 파일의 그 테이블을
# 통째로 대체한다. 새 drop-in 을 만들어 넣으면 거기 있던 다른 설정(예: nvidia 런타임
# 등록)이 사라진다 — 라이브 클러스터에서 실제로 그렇게 만들어 device-plugin 이
# "no runtime for nvidia" 로 뜨지 못했다. 그래서 새 파일을 만들지 않는다.
TARGET=$CFG
if grep -qE '^[[:space:]]*imports[[:space:]]*=' "$CFG" && [ -d "$DIR/conf.d" ]; then
  for f in $(ls "$DIR"/conf.d/*.toml 2>/dev/null | sort); do
    if grep -qF "$SEC" "$f"; then TARGET=$f; fi
  done
fi
echo "CDI target: $TARGET"
cp "$TARGET" "$TARGET.bak.cdi"

if grep -qE '^[[:space:]]*enable_cdi[[:space:]]*=' "$TARGET"; then
  # 이미 키가 있으면 값만 바꾼다. 같은 테이블에 키가 두 번 있으면 TOML 파싱이
  # 실패해 containerd 가 아예 안 뜬다.
  sed -i -E 's/^([[:space:]]*)enable_cdi[[:space:]]*=.*/\1enable_cdi = true/' "$TARGET"
else
  # 공백을 걷어낸 줄이 대상 섹션과 정확히 같을 때만 그 뒤에 넣는다. 부분일치를
  # 쓰면 하위 섹션에 걸려 중복 섹션이 생기고 containerd 가 안 뜬다.
  awk -v sec="$SEC" '
{ line = $0; t = line; sub(/^[[:space:]]+/, "", t); sub(/[[:space:]]+$/, "", t); print line }
t == sec && !done {
  ind = line; sub(/[^[:space:]].*$/, "", ind)
  print ind "  enable_cdi = true"
  done = 1
}
' "$TARGET" > "$TARGET.new"
  if grep -q 'enable_cdi' "$TARGET.new"; then
    mv "$TARGET.new" "$TARGET"
  else
    rm -f "$TARGET.new"
    printf '\n%s\n  enable_cdi = true\n' "$SEC" >> "$TARGET"
  fi
fi

# spec 경로는 containerd 기본값이 이미 /etc/cdi 와 /var/run/cdi 다. 다시 쓰지 않는다 —
# 쓸수록 덮어쓰기 사고 면적만 넓어진다.

nsenter -t 1 -m -u -i -n -p -- systemctl restart containerd

# 적용을 확인한다. 실패하면 되돌리고 종료 코드로 드러낸다 — 조용한 미작동 금지.
if ! nsenter -t 1 -m -u -i -n -p -- containerd config dump 2>/dev/null |
       grep -q 'enable_cdi[[:space:]]*=[[:space:]]*true'; then
  echo "CDI 적용 실패 — 유효 설정이 여전히 꺼져 있다. 원복한다" >&2
  cp "$TARGET.bak.cdi" "$TARGET"
  nsenter -t 1 -m -u -i -n -p -- systemctl restart containerd
  exit 1
fi
echo "CDI enabled; containerd restarted"
`
	priv := true
	return corev1.Container{
		Name:            "enable-cdi",
		Image:           HostExecImage(),
		Command:         []string{"sh", "-c", script},
		SecurityContext: &corev1.SecurityContext{Privileged: &priv},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "host-etc-containerd", MountPath: "/host/etc/containerd"},
		},
	}
}

// nvidiaDRABlockReason 은 NVIDIA DRA 드라이버를 배포하면 안 되는 사유다. 배포해도 되면
// 빈 문자열이다. 게이트가 둘 붙는 이유는 벤더 chart 구조다: (1) gpus.enabled=false 로
// 깔면 DaemonSet·DeviceClass 가 나오지 않고 RBAC 껍데기만 남는다 — chart 자신이
// device-plugin 과의 공존을 막는다. (2) gpus.enabled 는 DaemonSet 하나에 하나라 노드
// 단위로 나눌 수 없다. 둘 다 조용히 반쪽 상태를 만드는 대신 거절로 드러낸다.
func nvidiaDRABlockReason(policy *npuv1alpha1.NPUClusterPolicy) string {
	if !policy.Spec.Nvidia.DRA.IsEnabled() {
		return ""
	}
	if policy.Spec.Nvidia.AdvertisedBy() != npuv1alpha1.AdvertiseByDRA {
		return "NVIDIA DRA 드라이버는 advertiseBy: dra 일 때만 배포한다. " +
			"벤더 chart 가 device-plugin 과의 공존을 막고, 광고를 끈 채로 깔면 " +
			"발행물 없는 껍데기만 남는다. advertiseBy 를 dra 로 바꾼 뒤 다시 켤 것"
	}
	if policy.Spec.Nvidia.AdvertiseByNodeSelector != nil {
		return "NVIDIA 는 노드 일부만 DRA 로 넘길 수 없다. 벤더 chart 의 GPU 발행 스위치가 " +
			"DaemonSet 단위라 노드마다 다른 값을 줄 수 없다. " +
			"advertiseByNodeSelector 를 비워 전부 넘기거나 전부 device-plugin 으로 둘 것"
	}
	return ""
}

// draVendor 는 DRA 드라이버 배포 대상 벤더 하나다.
type draVendor struct {
	name string
	spec *npuv1alpha1.DRASpec
	// render 가 nil 이면 이 벤더는 아직 렌더러가 없다. 켜도 배포하지 않고 사유를 남긴다.
	render func(*npuv1alpha1.NPUClusterPolicy) []client.Object
	// driverName 은 발행물 관측에 쓰는 spec.driver 값이다. render 가 nil 이면 빈 값.
	driverName string
	// dsName 은 배포 여부 판정에 쓰는 DaemonSet 이름이다. render 가 nil 이면 빈 값.
	dsName string
}

// draVendors 는 이 정책이 선언한 DRA 축을 벤더 목록으로 편다. 선언되지 않은
// 벤더는 목록에 넣지 않는다 — status 를 쓰지 않는 것과 Absent 를 쓰는 것은 다르다.
func draVendors(policy *npuv1alpha1.NPUClusterPolicy) []draVendor {
	out := make([]draVendor, 0, 3)
	if s := policy.Spec.Furiosa.Rngd.DRA; s != nil {
		out = append(out, draVendor{
			name: vendorFuriosa, spec: s,
			render: renderFuriosaDRADriver, driverName: draDriverNameFuriosa,
			dsName: draDSNameFuriosa,
		})
	}
	if s := policy.Spec.Nvidia.DRA; s != nil {
		out = append(out, draVendor{
			name: vendorNvidia, spec: s,
			render: renderNvidiaDRADriver, driverName: draDriverNameNvidia,
			dsName: draDSNameNvidia,
		})
	}
	if s := policy.Spec.Rebellions.DRA; s != nil {
		out = append(out, draVendor{
			name: vendorRebellions, spec: s,
			render: renderRebellionsDRADriver, driverName: draDriverNameRebellions,
			dsName: draDSNameRebellions,
		})
	}
	if s := policy.Spec.Tenstorrent.DRA; s != nil {
		out = append(out, draVendor{
			name: vendorTenstorrent, spec: s,
			render: renderTenstorrentDRADriver, driverName: draDriverNameTenstorrent,
			dsName: draDSNameTenstorrent,
		})
	}
	return out
}

// ensureDRADriver 는 벤더별 dra.enabled 에 따라 드라이버를 배포/갱신하거나 회수하고,
// 발행물을 관측해 status 에 기록한다. 광고 주체는 건드리지 않는다 — 드라이버가
// 없어도 device-plugin 광고는 그대로 살아 있어야 한다.
func (r *NPUClusterPolicyReconciler) ensureDRADriver(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy,
) error {
	vendors := draVendors(policy)
	out := make([]npuv1alpha1.DRADriverStatus, 0, len(vendors))
	for _, v := range vendors {
		st, err := r.reconcileOneDRADriver(ctx, policy, v)
		if err != nil {
			return err
		}
		out = append(out, st)
	}
	policy.Status.DRADrivers = out
	return nil
}

// ignoredDRAConfigNote 는 이 벤더가 반영하지 않는 설정이 들어왔을 때 그 사실을 문장으로
// 돌려준다. 반영하지 않을 것이 없으면 빈 문자열이다. Tenstorrent 만 Args·Env 를 반영하지
// 않는다 — 장치 발견을 FM agent 에 맡기는 구조라 --fabric-manager-agent-address·
// --device-profile 이 선택 인자가 아니고, args 를 통째로 갈아끼우면 드라이버가 기동하지
// 못한다. 무시 자체는 맞지만 조용히 무시하면 사용자는 설정이 반영됐다고 믿는다.
func ignoredDRAConfigNote(vendor string, spec *npuv1alpha1.DRASpec) string {
	if vendor != vendorTenstorrent || spec == nil {
		return ""
	}
	var ignored []string
	if len(spec.Args) > 0 {
		ignored = append(ignored, "args")
	}
	if len(spec.Env) > 0 {
		ignored = append(ignored, "env")
	}
	if len(ignored) == 0 {
		return ""
	}
	return fmt.Sprintf("spec.dra.%s 는 이 벤더에서 반영하지 않는다 — 드라이버가 FM agent "+
		"주소와 device-profile 인자를 잃으면 기동하지 못한다. 설정은 저장되지만 "+
		"컨테이너에 전달되지 않는다", strings.Join(ignored, "·"))
}

// priorDRAMessage 는 직전 조정이 이 벤더에 남긴 status.message 다. ensureDRADriver 가
// 목록을 통째로 교체하기 전에 읽으므로 아직 이전 회차 값이다. 없으면 빈 문자열.
func priorDRAMessage(policy *npuv1alpha1.NPUClusterPolicy, vendor string) string {
	for _, d := range policy.Status.DRADrivers {
		if d.Vendor == vendor {
			return d.Message
		}
	}
	return ""
}

// dualAdvertiseNode 는 한 노드에서 한 벤더가 device-plugin 과 DRA 양쪽으로 동시에
// 광고 중인 물량이다. ResourceQty 는 device-plugin allocatable, Slices 는 그 벤더
// 드라이버가 이 노드에 낸 ResourceSlice 의 장치 수다(intent.DRACapability.SlicesByNodeDriver
// 와 같은 집계 방식 — device 개수를 "slice 수" 라 부르는 것은 그 타입의 기존 용례를 따른 것).
type dualAdvertiseNode struct {
	Node        string
	ResourceQty int64
	Slices      int32
}

// dualAdvertiseNodes 는 driverName 드라이버가 ResourceSlice 를 내는 노드 중, 같은 resourceName
// 을 device-plugin allocatable 로도 아직 내놓는 노드를 찾는다(rebellionsDualAdvertiseNote 의
// 일반화 — 벤더마다 드라이버 이름·리소스명만 다르다). 이중광고 노드가 없으면 빈 슬라이스.
// ponytail: 벤더마다 불러 ResourceSliceList 를 벤더 수만큼 다시 List 한다. 슬라이스 수가
// 작아 지금은 문제 없다 — 늘어나면 호출부에서 intent.LoadDRACapability 공유로 바꾼다.
func (r *NPUClusterPolicyReconciler) dualAdvertiseNodes(
	ctx context.Context, vendor, driverName, resourceName string,
) ([]dualAdvertiseNode, error) {
	var slices resourcev1.ResourceSliceList
	if err := r.List(ctx, &slices); err != nil {
		return nil, fmt.Errorf("%s ResourceSlice 조회 실패: %w", vendor, err)
	}
	counts := map[string]int32{}
	for i := range slices.Items {
		s := &slices.Items[i]
		if s.Spec.Driver == driverName && s.Spec.NodeName != nil && *s.Spec.NodeName != "" {
			counts[*s.Spec.NodeName] += int32(len(s.Spec.Devices))
		}
	}
	if len(counts) == 0 {
		return nil, nil
	}

	extRes := corev1.ResourceName(resourceName)
	var dual []dualAdvertiseNode
	for name, k := range counts {
		var node corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: name}, &node); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("%s 노드 %s 조회 실패: %w", vendor, name, err)
		}
		if qty, ok := node.Status.Allocatable[extRes]; ok && qty.Value() > 0 {
			dual = append(dual, dualAdvertiseNode{Node: name, ResourceQty: qty.Value(), Slices: k})
		}
	}
	sort.Slice(dual, func(i, j int) bool { return dual[i].Node < dual[j].Node })
	return dual, nil
}

// rebellionsDualAdvertiseNote 는 Rebellions ResourceSlice(DRA)를 내는 노드 중 같은
// extended resource 를 device-plugin allocatable 로도 광고하는 노드를 찾아 통지
// 문장으로 돌려준다. 이중광고 노드가 없으면 빈 문자열.
func (r *NPUClusterPolicyReconciler) rebellionsDualAdvertiseNote(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy,
) (string, error) {
	resName := policy.Spec.Rebellions.ResourceName
	if resName == "" {
		resName = rbllnsResourceNameDefault
	}
	resPrefix := policy.Spec.Rebellions.ResourcePrefix
	if resPrefix == "" {
		resPrefix = rbllnsResourcePrefixDfault
	}
	extRes := resPrefix + "/" + resName

	dual, err := r.dualAdvertiseNodes(ctx, vendorRebellions, draDriverNameRebellions, extRes)
	if err != nil {
		return "", err
	}
	if len(dual) == 0 {
		return "", nil
	}
	names := make([]string, len(dual))
	for i, d := range dual {
		names[i] = d.Node
	}
	return fmt.Sprintf(
		"이중광고: 노드 %v 가 %s 을 device-plugin 과 DRA 로 동시 광고한다(스케줄러가 "+
			"카드 1장을 자원 2개로 본다). 해당 노드에서 device-plugin 광고를 끄거나 DRA 를 끄라",
		names, extRes), nil
}

// advertiseVendorDriverName 은 advertiseVendors() 의 다섯 벤더 키가 발행물에 쓰는
// spec.driver 값이다(vendorTable 을 읽는다). 모르는 벤더는 빈 문자열 — dualAdvertiseNodes 는
// 그러면 어떤 슬라이스와도 안 맞아 항상 0건이다.
func advertiseVendorDriverName(vendor string) string {
	return vendorTable[vendor].driverName
}

// advertiseVendorDRADSName 은 advertiseVendors() 의 다섯 벤더 키가 배포 여부 확인에 쓰는
// DRA DaemonSet 이름이다(vendorTable 을 읽는다). 모르는 벤더는 빈 문자열 — draDaemonSetExists 는
// 그러면 조회 없이 false 를 돌려준다.
func advertiseVendorDRADSName(vendor string) string {
	return vendorTable[vendor].dsName
}

// advertiseVendorResourceName 은 이 벤더가 device-plugin 으로 광고하는 전체 장치 리소스명이다
// (vendorTable 을 읽는다). 모르는 벤더는 빈 문자열이다.
func advertiseVendorResourceName(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, vendor string) string {
	if a, ok := vendorTable[vendor]; ok {
		return a.resource(ctx, policy)
	}
	return ""
}

// detectDualAdvertisement 는 advertiseVendors() 다섯 벤더를 모두 훑어, device-plugin 과 DRA 가
// 같은 물리 장치를 동시에 광고하는 노드가 있는지를 top-level Condition(ConditionDualAdvertisement)
// 으로 남긴다. spec 의 advertiseBy·dra.enabled 가 아니라 실측(allocatable·ResourceSlice)만
// 본다 — devicePlugin 으로 되돌려도 DRA 발행이 아직 안 꺼진 창을 그대로 드러내야 한다.
// 벤더별 Rebellions 통지(rebellionsDualAdvertiseNote, DRADriverStatus.Message)는 별개다 —
// 이 Condition 은 정책 전체 단위의 표면이다.
func (r *NPUClusterPolicyReconciler) detectDualAdvertisement(ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy) {
	logger := logf.FromContext(ctx)

	var parts []string
	for vendor := range advertiseVendors(policy) {
		driverName := advertiseVendorDriverName(vendor)
		resourceName := advertiseVendorResourceName(ctx, policy, vendor)
		if driverName == "" || resourceName == "" {
			continue
		}
		nodes, err := r.dualAdvertiseNodes(ctx, vendor, driverName, resourceName)
		if err != nil {
			// 부분 결과로 조건을 잘못 내리지 않는다 — 이번 pass 는 건너뛰고 다음 reconcile 에서 다시 본다.
			logger.Error(err, "이중광고 감지 조회 실패", "vendor", vendor)
			return
		}
		for _, n := range nodes {
			parts = append(parts, fmt.Sprintf("%s: %s(%s=%d, slices=%d)", vendor, n.Node, resourceName, n.ResourceQty, n.Slices))
		}
	}
	sort.Strings(parts)

	// 전이 판정용 이전 상태는 SetStatusCondition 전에 값으로 잡는다 — FindStatusCondition 이
	// 돌려주는 포인터를 나중에 비교하면 SetStatusCondition 의 제자리 수정 때문에 항상 "바뀌지
	// 않음" 으로 보인다(vendoroperator_detect.go 의 같은 함정 참고).
	prevStatus := metav1.ConditionUnknown
	if prev := meta.FindStatusCondition(policy.Status.Conditions, npuv1alpha1.ConditionDualAdvertisement); prev != nil {
		prevStatus = prev.Status
	}

	if len(parts) == 0 {
		meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:               npuv1alpha1.ConditionDualAdvertisement,
			Status:             metav1.ConditionFalse,
			Reason:             "NoDualAdvertise",
			Message:            "device-plugin 과 DRA 가 같은 장치를 동시에 광고하는 노드가 없다",
			ObservedGeneration: policy.Generation,
		})
		return
	}

	message := strings.Join(parts, "; ")
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:               npuv1alpha1.ConditionDualAdvertisement,
		Status:             metav1.ConditionTrue,
		Reason:             "DualAdvertiseDetected",
		Message:            message,
		ObservedGeneration: policy.Generation,
	})
	// 전이(→True, 최초 포함)에서만 경고한다 — 매 reconcile 마다 내면 이벤트가 넘친다.
	if prevStatus != metav1.ConditionTrue && r.Recorder != nil {
		r.Recorder.Eventf(policy, corev1.EventTypeWarning, "DualAdvertise", "%s", message)
	}
}

// reconcileOneDRADriver 는 벤더 하나를 맞춘다. 판정 순서가 곧 안전 순서다 —
// 끄기가 먼저이고, 렌더러 없는 벤더는 배포 시도조차 하지 않는다.
func (r *NPUClusterPolicyReconciler) reconcileOneDRADriver(
	ctx context.Context, policy *npuv1alpha1.NPUClusterPolicy, v draVendor,
) (npuv1alpha1.DRADriverStatus, error) {
	st := npuv1alpha1.DRADriverStatus{Vendor: v.name, Phase: npuv1alpha1.DRAPhaseAbsent}

	// delegated·external 이면 이 벤더의 DRA 드라이버를 배포도 회수도 하지 않는다 — kcloud 가
	// 소유하지 않으므로 관측만 한다(배포 여부는 IsOperatorOwned() 하나로 묻는다).
	// (managed→delegated·external 로 바뀌어 기존 드라이버가 남아 있어도 지우지 않는다 — Global Constraint.)
	if !vendorIsOperatorOwned(policy, v.name) {
		st.Message = "delegated·external 관리 모드 — kcloud 가 DRA 드라이버를 배포·회수하지 않는다"
		return st, nil
	}

	if !v.spec.IsEnabled() {
		// 배포한 적이 없으면 지울 것도 없다. DaemonSet 존재 여부를 먼저 보는 이유는
		// 매 reconcile 마다 삭제를 쏘면 API 소음이 되고, 한 번도 켠 적 없는 클러스터에서도
		// 삭제 권한을 요구하게 되기 때문이다.
		deployed, err := r.draDeployed(ctx, v)
		if err != nil || !deployed {
			return st, err
		}
		return st, r.deleteDRAObjects(ctx, v.render(policy))
	}

	note := ignoredDRAConfigNote(v.name, v.spec)
	if note != "" {
		// 통지 내용이 바뀔 때만 이벤트를 낸다. 발행물이 관측되지 않으면 draRequeue
		// (npuclusterpolicy_controller.go)가 30초마다 재조정하므로, 무조건 내면 spec 이
		// 그대로인데도 같은 경고가 30초마다 갱신된다.
		if r.Recorder != nil && !strings.Contains(priorDRAMessage(policy, v.name), note) {
			r.Recorder.Eventf(policy, corev1.EventTypeWarning, "DRAConfigIgnored",
				"%s DRA: %s", v.name, note)
		}
		st.Message = note
	}

	if v.name == vendorNvidia {
		if reason := nvidiaDRABlockReason(policy); reason != "" {
			st.Phase = npuv1alpha1.DRAPhaseBlocked
			st.Message = reason
			return st, nil
		}
	}

	if v.render == nil {
		st.Phase = npuv1alpha1.DRAPhaseBlocked
		st.Message = v.name + " 는 아직 DRA 드라이버 렌더러가 없다 — 배포하지 않는다"
		return st, nil
	}

	objs := v.render(policy)
	for _, o := range objs {
		if ds, ok := o.(*appsv1.DaemonSet); ok {
			setOwnerAnnotation(&ds.ObjectMeta, policy)
			// control-plane 노드에는 올리지 않는다 — 그 노드에도 벤더 라벨이 붙어 있을 수
			// 있고, 장치가 없으니 벤더 prestart 가 init 에서 멈춘 채 남는다. 다른 operand 와
			// 같은 규약이다.
			applyControlPlaneExclusion(&ds.Spec.Template.Spec)
			applyImagePullSecrets(&ds.Spec.Template.Spec, policy.Spec.ImagePullSecrets)
			if err := r.createOrUpdateDS(ctx, ds); err != nil {
				return st, err
			}
			continue
		}
		if err := r.applyDRAObject(ctx, o); err != nil {
			return st, err
		}
	}

	st.DriverName = v.driverName
	nodes, err := r.countPublishingNodes(ctx, v.driverName)
	if err != nil {
		return st, err
	}
	st.PublishedNodes = nodes
	// 발행물이 완료 기준이다. 파드가 Running 이어도 ResourceSlice 가 없으면
	// 사용자는 이 드라이버로 아무것도 요청할 수 없다.
	if nodes == 0 {
		st.Phase = npuv1alpha1.DRAPhaseInstalling
		st.Message = "드라이버는 배포됐으나 ResourceSlice 발행이 아직 관측되지 않음"
		if note != "" {
			// 구분자가 ". " 인 것은 통지 문장이 자기 안에 이미 " — " 를 쓰기 때문이다.
			st.Message += ". " + note
		}
		return st, nil
	}

	// advertiseBy=dra 로 전환하지 않은(파일럿) 노드의 이중광고를 감지한다 — DRA 를 켜도
	// device-plugin 광고가 그대로 살아 있을 수 있다. 같은 노드가 extended
	// resource 를 allocatable(device-plugin)과 ResourceSlice(DRA) 양쪽으로 내면 스케줄러는
	// 카드 1장을 자원 2개로 본다. 스케줄링 후보 경로는 이미 이 노드를 뺀다
	// (AWReasonDRADoubleAdvertised, intent/translate.go) — 여기서는 관리자에게 표면화만 한다.
	// 통지 내용이 바뀔 때만 이벤트를 낸다(위 DRAConfigIgnored 와 같은 이유).
	if v.name == vendorRebellions {
		dualNote, err := r.rebellionsDualAdvertiseNote(ctx, policy)
		if err != nil {
			return st, err
		}
		if dualNote != "" {
			if r.Recorder != nil && !strings.Contains(priorDRAMessage(policy, v.name), dualNote) {
				r.Recorder.Eventf(policy, corev1.EventTypeWarning, "RebellionsDualAdvertise", "%s", dualNote)
			}
			if st.Message != "" {
				st.Message += ". " + dualNote
			} else {
				st.Message = dualNote
			}
		}
	}

	st.Phase = npuv1alpha1.DRAPhaseReady
	return st, nil
}

// draDeployed 는 이 벤더의 드라이버가 배포돼 있는지를 DaemonSet 존재로 판정한다.
func (r *NPUClusterPolicyReconciler) draDeployed(ctx context.Context, v draVendor) (bool, error) {
	if v.render == nil {
		return false, nil
	}
	var ds appsv1.DaemonSet
	err := r.Get(ctx, types.NamespacedName{Name: v.dsName, Namespace: draNamespace}, &ds)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// draDaemonSetExists 는 이름으로 DRA DaemonSet 존재 여부만 본다 — draDeployed 와 달리
// draVendor(render 필드 포함) 를 요구하지 않는다. advertiseVendors() 축의 다섯 벤더 키 중
// rngd 는 draVendors() 표에 별도 항목이 없어(furiosa 항목이 그 DaemonSet 을 겸함) draVendor
// 값을 만들어 넘기기 애매하다 — reconcileDRARetiredLabel 이 dra.enabled=false 로 retired
// 라벨을 지우기 전에 그 벤더의 DaemonSet 이 실제로 사라졌는지 확인하는 데 쓴다.
func (r *NPUClusterPolicyReconciler) draDaemonSetExists(ctx context.Context, dsName string) (bool, error) {
	if dsName == "" {
		return false, nil
	}
	var ds appsv1.DaemonSet
	err := r.Get(ctx, types.NamespacedName{Name: dsName, Namespace: draNamespace}, &ds)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// applyDRAObject 는 오브젝트 하나를 만들거나 갱신한다.
func (r *NPUClusterPolicyReconciler) applyDRAObject(ctx context.Context, desired client.Object) error {
	cur, ok := desired.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("오브젝트 복제 실패: %T", desired)
	}
	key := client.ObjectKeyFromObject(desired)
	if err := r.Get(ctx, key, cur); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	desired.SetResourceVersion(cur.GetResourceVersion())
	return r.Update(ctx, desired)
}

// deleteDRAObjects 는 렌더된 오브젝트를 지운다. 이미 없으면 조용히 넘어간다.
func (r *NPUClusterPolicyReconciler) deleteDRAObjects(ctx context.Context, objs []client.Object) error {
	for _, o := range objs {
		if err := r.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// countPublishingNodes 는 이 드라이버가 ResourceSlice 를 발행 중인 노드 수를 센다.
// 같은 노드가 여러 slice 를 내도 한 번만 센다.
func (r *NPUClusterPolicyReconciler) countPublishingNodes(ctx context.Context, driver string) (int32, error) {
	if driver == "" {
		return 0, nil
	}
	var slices resourcev1.ResourceSliceList
	if err := r.List(ctx, &slices); err != nil {
		// 클러스터에 이 API 가 없으면 발행물도 없다. 목록 실패로 reconcile 을 죽이지 않는다.
		if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			return 0, nil
		}
		return 0, err
	}
	seen := map[string]bool{}
	for i := range slices.Items {
		s := &slices.Items[i]
		if s.Spec.Driver != driver || s.Spec.NodeName == nil {
			continue
		}
		seen[*s.Spec.NodeName] = true
	}
	return int32(len(seen)), nil //nolint:gosec // 노드 수가 int32 를 넘을 일은 없다
}

const (
	// draDSNameNvidia 는 배포되는 DaemonSet 이름이다.
	draDSNameNvidia = "kcloud-nvidia-dra-driver"
	// draDriverNameNvidia 는 발행물의 spec.driver 값이다. DeviceClass 셋이 모두
	// 이 driver 를 가리키므로 발행 관측은 이 하나로 센다.
	draDriverNameNvidia = "gpu.nvidia.com"
	// draDriverImageNvidiaDefault 는 spec 미지정 시 기본 이미지다.
	draDriverImageNvidiaDefault = "registry.k8s.io/dra-driver-nvidia/dra-driver-nvidia-gpu:v0.4.0"
	// draKubeletPluginArgsNvidia 는 chart 가 주는 실행 스크립트다. MASK 처리 뒤
	// 플러그인을 띄운다. 그대로 옮긴다 — 우리가 줄이면 벤더 동작이 바뀐다.
	draKubeletPluginArgsNvidia = `if [ "${MASK_NVIDIA_DRIVER_PARAMS}" = "true" ]; then
  cp /proc/driver/nvidia/params /root/gpu-params
  sed -i 's/^ModifyDeviceFiles: 1$/ModifyDeviceFiles: 0/' /root/gpu-params
  mount --bind /root/gpu-params /proc/driver/nvidia/params
fi
gpu-kubelet-plugin -v $(LOG_VERBOSITY)`
)

// renderNvidiaDRADriver 는 NVIDIA DRA 드라이버 오브젝트를 만든다.
// ServiceAccount · ClusterRole · ClusterRoleBinding · DaemonSet · DeviceClass 3종.
//
// 벤더 chart(v0.5.0-dev)에서 다중 노드 NVLink 축은 통째로 뺀다 — GPU 노드가 하나뿐이라
// 쓸 데가 없고, chart 는 그 값을 꺼도 관련 오브젝트를 남긴다. namespace Role 도 그
// 축 전용(computedomaincliques)이라 함께 뺀다.
func renderNvidiaDRADriver(policy *npuv1alpha1.NPUClusterPolicy) []client.Object {
	spec := policy.Spec.Nvidia.DRA
	labels := map[string]string{
		"app.kubernetes.io/name":      draDSNameNvidia,
		"app.kubernetes.io/component": "dra-driver",
	}

	sa := &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameNvidia, Namespace: draNamespace, Labels: labels},
	}

	// chart 의 ClusterRole 5줄에서 resource.nvidia.com(다중 노드 NVLink) 하나를 뺀 넷이다.
	cr := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameNvidia, Labels: labels},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims"},
				Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices"},
				Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
			{APIGroups: []string{""}, Resources: []string{"nodes"},
				Verbs: []string{"get", "list", "watch", "update", "patch"}},
			{APIGroups: []string{""}, Resources: []string{"pods"},
				Verbs: []string{"get", "list", "watch"}},
		},
	}

	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameNvidia, Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: draDSNameNvidia},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: draDSNameNvidia, Namespace: draNamespace}},
	}

	out := []client.Object{sa, cr, crb, renderNvidiaDRADaemonSet(spec, labels)}
	for _, dc := range []struct{ name, kind string }{
		{"gpu.nvidia.com", "gpu"},
		{"mig.nvidia.com", "mig"},
		{"vfio.gpu.nvidia.com", "vfio"},
	} {
		out = append(out, &resourcev1.DeviceClass{
			TypeMeta:   metav1.TypeMeta{APIVersion: "resource.k8s.io/v1", Kind: "DeviceClass"},
			ObjectMeta: metav1.ObjectMeta{Name: dc.name, Labels: labels},
			Spec: resourcev1.DeviceClassSpec{
				Selectors: []resourcev1.DeviceSelector{{
					CEL: &resourcev1.CELDeviceSelector{
						Expression: "device.driver == '" + draDriverNameNvidia +
							"' && device.attributes['" + draDriverNameNvidia + "'].type == '" + dc.kind + "'",
					},
				}},
			},
		})
	}
	return out
}

// renderNvidiaDRADaemonSet 은 kubelet-plugin DaemonSet 을 만든다. 컨테이너·볼륨·환경변수는
// chart 원본을 그대로 쓰고 스케줄 조건과 init container 순서만 우리 것으로 바꾼다.
func renderNvidiaDRADaemonSet(spec *npuv1alpha1.DRASpec,
	labels map[string]string) *appsv1.DaemonSet {
	image := draDriverImageNvidiaDefault
	if spec != nil && spec.Image != "" {
		image = spec.Image
	}
	args := []string{draKubeletPluginArgsNvidia}
	if spec != nil && len(spec.Args) > 0 {
		args = spec.Args
	}
	env := []corev1.EnvVar{
		{Name: "HTTP_ENDPOINT", Value: ":8080"},
		{Name: "METRICS_PATH", Value: "/metrics"},
		{Name: "LOG_VERBOSITY", Value: "4"},
		{Name: "MASK_NVIDIA_DRIVER_PARAMS", Value: ""},
		{Name: "NVIDIA_DRIVER_ROOT", Value: "/"},
		{Name: "NVIDIA_VISIBLE_DEVICES", Value: "void"},
		{Name: "CDI_ROOT", Value: draCDIRoot},
		{Name: "NVIDIA_MIG_CONFIG_DEVICES", Value: "all"},
		{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
		}},
		{Name: "NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "IMAGE_NAME", Value: image},
		{Name: "IMAGE_PULL_POLICY", Value: string(corev1.PullIfNotPresent)},
		{Name: "KUBELET_REGISTRAR_DIRECTORY_PATH", Value: "/var/lib/kubelet/plugins_registry"},
		{Name: "KUBELET_PLUGINS_DIRECTORY_PATH", Value: "/var/lib/kubelet/plugins"},
		{Name: "HEALTHCHECK_PORT", Value: "51516"},
	}
	if spec != nil {
		env = mergeEnv(env, spec.Env)
	}

	priv := true
	dirOrCreate := corev1.HostPathDirectoryOrCreate
	bidirectional := corev1.MountPropagationBidirectional
	hostToContainer := corev1.MountPropagationHostToContainer
	hostVol := func(name, path string) corev1.Volume {
		return corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: path, Type: &dirOrCreate},
		}}
	}

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: draDSNameNvidia, Namespace: draNamespace, Labels: labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// chart affinity 는 NFD 4종과 외부 operator 라벨 1종의 OR 이다.
					// 우리는 둘 다 안 쓰므로 자립 라벨로 갈아 끼운다.
					NodeSelector:       map[string]string{vendorPresentLabel(vendorNvidia): labelValueTrue},
					Affinity:           nil,
					ServiceAccountName: draDSNameNvidia,
					PriorityClassName:  "system-node-critical",
					// CDI init container 가 nsenter -t 1 로 호스트 containerd 를 재시작한다.
					HostPID:     true,
					Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					InitContainers: []corev1.Container{
						// CDI 활성화가 반드시 먼저다. 벤더 prestart 가 CDI 없는 상태에서 돌면 안 된다.
						renderCDIInitContainer(),
						{
							Name:            "vendor-prestart",
							Image:           image,
							Command:         []string{"bash", "/usr/bin/kubelet-plugin-prestart.sh"},
							SecurityContext: &corev1.SecurityContext{Privileged: &priv},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "driver-root-parent", MountPath: "/driver-root-parent"},
							},
						},
					},
					Containers: []corev1.Container{{
						Name:            "gpus",
						Image:           image,
						Command:         []string{"bash", "-c"},
						Args:            args,
						Env:             env,
						SecurityContext: &corev1.SecurityContext{Privileged: &priv},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
							{Name: "plugins", MountPath: "/var/lib/kubelet/plugins", MountPropagation: &bidirectional},
							{Name: "cdi", MountPath: draCDIRoot},
							{Name: "driver-root", MountPath: "/driver-root", MountPropagation: &hostToContainer},
						},
					}},
					Volumes: []corev1.Volume{
						hostVol("plugins-registry", "/var/lib/kubelet/plugins_registry"),
						hostVol("plugins", "/var/lib/kubelet/plugins"),
						hostVol("cdi", draCDIRoot),
						hostVol("driver-root-parent", "/"),
						hostVol("driver-root", "/"),
						hostVol("host-etc-containerd", "/etc/containerd"),
					},
				},
			},
		},
	}
	// nvidiaDRABlockReason 이 advertiseBy: dra 가 아니면 이미 배포 자체를 거절하므로 이
	// 노드가 retired 인 채로 이 DaemonSet 이 뜰 일은 실제로 없다 — 그래도 다섯 벤더 모두
	// 같은 규칙을 갖도록 대칭으로 건다.
	excludeDRARetiredNodes(&ds.Spec.Template.Spec, vendorNvidia)
	return ds
}

// renderRebellionsDRADriver 는 벤더 chart(k8s-dra-driver-npu)를 우리 규약으로 옮긴 것이다.
// 바꾼 것은 둘이다. nodeSelector 를 벤더 라벨에서 자립 라벨로 바꾸고, 네임스페이스를
// 우리 것으로 고정한다. 나머지 컨테이너·볼륨·환경변수는 원본 그대로다.
func renderRebellionsDRADriver(policy *npuv1alpha1.NPUClusterPolicy) []client.Object {
	spec := policy.Spec.Rebellions.DRA
	labels := map[string]string{
		"app.kubernetes.io/name":      draDSNameRebellions,
		"app.kubernetes.io/component": "dra-driver",
	}

	sa := &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameRebellions, Namespace: draNamespace, Labels: labels},
	}

	// 벤더 chart clusterrole.yaml 세 줄 그대로다.
	cr := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameRebellions, Labels: labels},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims"},
				Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"nodes"},
				Verbs: []string{"get"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices"},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		},
	}

	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameRebellions, Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: draDSNameRebellions},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: draDSNameRebellions, Namespace: draNamespace}},
	}

	dc := &resourcev1.DeviceClass{
		TypeMeta:   metav1.TypeMeta{APIVersion: "resource.k8s.io/v1", Kind: "DeviceClass"},
		ObjectMeta: metav1.ObjectMeta{Name: draDriverNameRebellions, Labels: labels},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{
				CEL: &resourcev1.CELDeviceSelector{
					Expression: "device.driver == '" + draDriverNameRebellions + "'",
				},
			}},
		},
	}

	return []client.Object{sa, cr, crb, renderRebellionsDRADaemonSet(spec, labels), dc}
}

// renderRebellionsDRADaemonSet 은 kubeletplugin DaemonSet 을 만든다.
func renderRebellionsDRADaemonSet(spec *npuv1alpha1.DRASpec, labels map[string]string) *appsv1.DaemonSet {
	image := draDriverImageRebellionsDefault
	if spec != nil && spec.Image != "" {
		image = spec.Image
	}
	command := []string{"npu-kubelet-plugin"}
	if spec != nil && len(spec.Args) > 0 {
		command = append(command, spec.Args...)
	}
	env := []corev1.EnvVar{
		{Name: "DRIVER_NAME", Value: draDriverNameRebellions},
		{Name: "CDI_ROOT", Value: draCDIRoot},
		{Name: "KUBELET_REGISTRAR_DIRECTORY_PATH", Value: "/var/lib/kubelet/plugins_registry"},
		{Name: "KUBELET_PLUGINS_DIRECTORY_PATH", Value: "/var/lib/kubelet/plugins"},
		{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
		}},
		{Name: "NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "HEALTHCHECK_PORT", Value: fmt.Sprintf("%d", draHealthcheckPortRebellions)},
	}
	if spec != nil {
		env = mergeEnv(env, spec.Env)
	}
	// probe 포트는 병합이 끝난 env 를 되읽어 정한다. spec.Env 가 HEALTHCHECK_PORT 를 덮어썼는데
	// probe 가 상수 포트를 찌르면 바이너리와 kubelet 이 다른 포트를 보고 liveness 가 재시작
	// 루프에 빠진다. 리터럴 값이 1~65535 범위일 때만 붙인다 — 벗어나면 GRPCAction.Port 계약
	// 위반으로 API 서버가 DaemonSet 전체를 거절한다. -1 은 벤더 관례상 헬스체크를 끈다는
	// 뜻이고, ValueFrom(Secret·ConfigMap 등)은 렌더 시점에 값을 몰라 같은 이유로 probe 를
	// 붙이지 않는다. env 값 자체는 사용자 것이니 그대로 둔다.
	var healthPort int32
	var attachProbe bool
	for _, e := range env {
		if e.Name != "HEALTHCHECK_PORT" {
			continue
		}
		if e.ValueFrom == nil {
			if v, err := strconv.ParseInt(e.Value, 10, 32); err == nil && v >= 1 && v <= 65535 {
				healthPort = int32(v)
				attachProbe = true
			}
		}
		break
	}

	hostPathDir := func(p string) corev1.VolumeSource {
		return corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: p}}
	}

	plugin := corev1.Container{
		Name:            "plugin",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         command,
		Env:             env,
		SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
			{Name: "plugins", MountPath: "/var/lib/kubelet/plugins"},
			{Name: "cdi", MountPath: draCDIRoot},
			{Name: "host-dev", MountPath: "/dev"},
			{Name: "host-run-rbln", MountPath: "/run/rbln"},
			{Name: "host-usr-bin", MountPath: "/host/usr/bin", ReadOnly: true},
		},
	}
	if attachProbe {
		// 벤더 chart 가 같은 포트에 gRPC 헬스체크를 건다. HEALTHCHECK_PORT 를 켜기만 하고
		// probe 를 안 걸면 쓰이지 않는 설정으로 남는다. 지연·주기·실패 임계값은 벤더 chart
		// 원본을 확인하지 못해 고른 보수적 기본값이다 — 이 드라이버가 CDI 단계를 넘겨 뜬
		// 적이 없어 실측치가 없다.
		plugin.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				GRPC: &corev1.GRPCAction{Port: healthPort},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			FailureThreshold:    3,
		}
	}

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: draDSNameRebellions, Namespace: draNamespace, Labels: labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: draDSNameRebellions,
					HostPID:            true,
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					InitContainers:     []corev1.Container{renderCDIInitContainer()},
					PriorityClassName:  "system-node-critical",
					NodeSelector: map[string]string{
						vendorPresentLabel(vendorRebellions): labelValueTrue,
					},
					Containers: []corev1.Container{plugin},
					Volumes: []corev1.Volume{
						{Name: "plugins-registry", VolumeSource: hostPathDir("/var/lib/kubelet/plugins_registry")},
						{Name: "plugins", VolumeSource: hostPathDir("/var/lib/kubelet/plugins")},
						{Name: "cdi", VolumeSource: hostPathDir(draCDIRoot)},
						{Name: "host-dev", VolumeSource: hostPathDir("/dev")},
						{Name: "host-run-rbln", VolumeSource: hostPathDir("/run/rbln")},
						{Name: "host-usr-bin", VolumeSource: hostPathDir("/usr/bin")},
						{Name: "host-etc-containerd", VolumeSource: hostPathDir("/etc/containerd")},
					},
				},
			},
		},
	}
	excludeDRARetiredNodes(&ds.Spec.Template.Spec, vendorRebellions)
	return ds
}

const (
	// draDSNameTenstorrent 는 배포되는 DaemonSet 이름이다.
	draDSNameTenstorrent = "kcloud-tenstorrent-dra-driver"
	// draDriverNameTenstorrent 는 이 드라이버가 발행물에 쓰는 spec.driver 값이다.
	draDriverNameTenstorrent = "tenstorrent.com"
	// draDriverImageTenstorrentDefault 는 spec 미지정 시 기본 이미지다.
	// 벤더가 chart 를 주지 않아 태그를 우리가 고정한다.
	draDriverImageTenstorrentDefault = "ghcr.io/tenstorrent/tt-dra-driver:0.0.48"
	// draFMImageTenstorrentDefault 는 장치 발견을 맡는 보조 데몬이다. 드라이버가 이것에 gRPC 로
	// 물어야 ASIC 을 찾는다 — 없으면 CrashLoop 에 빠진다. draDriverImageRebellionsDefault 와
	// 달리 latest 를 쓴다 — ghcr.io/tenstorrent/tt-fabric-manager 가 공개하는 태그는
	// latest·main·sha 뿐이라 버전 태그가 없다. 재현 가능한 배포가 필요하면 sha 태그나 digest 를
	// tenstorrent.dra.fabricManager.tag 로 덮고, air-gap 이면 사설 미러에 올려
	// global.vendorRegistry 를 준다.
	draFMImageTenstorrentDefault = "ghcr.io/tenstorrent/tt-fabric-manager:latest"
	// draFMPortTenstorrent 는 FM agent 가 듣는 포트다. 드라이버 기본값과 맞춘다.
	draFMPortTenstorrent = 50053
)

// renderTenstorrentDRADriver 는 Tenstorrent DRA 드라이버 오브젝트 여섯을 만든다.
// 다른 벤더와 달리 ConfigMap 이 하나 더 붙는다 — FM agent 가 설정 파일 없이
// 기동을 거부하는데 그 파일이 이미지에 없다.
func renderTenstorrentDRADriver(policy *npuv1alpha1.NPUClusterPolicy) []client.Object {
	spec := policy.Spec.Tenstorrent.DRA
	labels := map[string]string{
		"app.kubernetes.io/name":      draDSNameTenstorrent,
		"app.kubernetes.io/component": "dra-driver",
	}

	sa := &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameTenstorrent, Namespace: draNamespace, Labels: labels},
	}
	cr := &rbacv1.ClusterRole{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRole"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameTenstorrent, Labels: labels},
		// 규칙 8종은 renderFuriosaDRADriver 의 ClusterRole 을 그대로 복제한 것이다.
		// 손으로 좁혀 쓰지 않는다 — 이 집합은 1.34 에서 DRA 가 동작을 확인한 판이고
		// resourceclaims/driver 의 associated-node 권한을 담는다. 좁히면 "claim 이 노드에
		// 묶이지 않음" 실패가 운영에서야 드러난다.
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list", "watch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims"},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims/status"},
				Verbs: []string{"update", "patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaims/driver"},
				Verbs: []string{"associated-node:update", "associated-node:patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaimtemplates"},
				Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceclaimtemplates/status"},
				Verbs: []string{"update", "patch"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices"},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			{APIGroups: []string{"resource.k8s.io"}, Resources: []string{"resourceslices/status"},
				Verbs: []string{"update", "patch"}},
		},
	}
	crb := &rbacv1.ClusterRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "ClusterRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameTenstorrent, Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: draDSNameTenstorrent},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: draDSNameTenstorrent, Namespace: draNamespace}},
	}
	// 바이너리 문자열에서 뽑은 최소 설정이다. 벤더가 예제를 주지 않아 이 두 줄로 검증을
	// 통과시켰다 — 벤더 판이 오르면 여기가 먼저 깨진다. listen_address 가 가리키는 FM
	// controller 는 어디에도 배치하지 않아 agent 가 등록에 실패하고 노드마다 초당 1회
	// 재발견을 재시도한다. DRA 경로 자체는 끝까지 성립한다(GetTopology 는 등록 여부와
	// 무관하게 응답한다). 여러 노드에 걸친 fabric 토폴로지는 controller 배치(별도 과제) 전까지
	// 쓸 수 없다 — 같은 경고가 values.yaml 의 tenstorrent.dra 블록에도 있다.
	cm := &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Name: draDSNameTenstorrent, Namespace: draNamespace, Labels: labels},
		Data: map[string]string{
			"config.yaml": "controller:\n  listen_address: \"127.0.0.1:50051\"\n",
		},
	}
	dc := &resourcev1.DeviceClass{
		TypeMeta:   metav1.TypeMeta{APIVersion: "resource.k8s.io/v1", Kind: "DeviceClass"},
		ObjectMeta: metav1.ObjectMeta{Name: draDriverNameTenstorrent, Labels: labels},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{
				CEL: &resourcev1.CELDeviceSelector{
					Expression: "device.driver == '" + draDriverNameTenstorrent + "'",
				},
			}},
		},
	}
	return []client.Object{sa, cr, crb, cm, renderTenstorrentDRADaemonSet(spec, labels), dc}
}

// renderTenstorrentDRADaemonSet 은 드라이버와 FM agent 를 한 파드에 담는다.
// 드라이버가 localhost 로 FM 에 질의하므로 같은 파드여야 한다.
func renderTenstorrentDRADaemonSet(
	spec *npuv1alpha1.DRASpec, labels map[string]string,
) *appsv1.DaemonSet {
	image := draDriverImageTenstorrentDefault
	if spec != nil && spec.Image != "" {
		image = spec.Image
	}
	fmImage := draFMImageTenstorrentDefault
	if spec != nil && spec.FabricManagerImage != "" {
		fmImage = spec.FabricManagerImage
	}
	// spec.Args·spec.Env 는 이 벤더에서만 반영하지 않는다. 다른 세 렌더러가 이 자리에서
	// args 를 통째로 갈아끼우는데, 여기서 같은 일을 하면 --fabric-manager-agent-address 와
	// --device-profile 이 사라져 드라이버가 기동하지 못한다 — 이 벤더는 장치 발견을 FM
	// agent 에 맡기는 구조라 그 둘이 선택 인자가 아니다. 덮어쓰기를 열려면 통째 교체가
	// 아니라 병합 규칙을 먼저 정해야 하고, 컨테이너가 둘이라 어느 쪽에 적용할지도 함께
	// 정해야 한다. 그때까지는 이미지 덮어쓰기만 연다.
	hostPathDir := func(p string) corev1.VolumeSource {
		return corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: p}}
	}
	priv := true

	ds := &appsv1.DaemonSet{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DaemonSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name: draDSNameTenstorrent, Namespace: draNamespace, Labels: labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: draDSNameTenstorrent,
					PriorityClassName:  "system-node-critical",
					HostPID:            true,
					NodeSelector: map[string]string{
						vendorPresentLabel(vendorTenstorrent): labelValueTrue,
					},
					Tolerations:    []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					InitContainers: []corev1.Container{renderCDIInitContainer()},
					// 목록 순서는 기동 순서를 뜻하지 않는다 — Kubernetes 는 일반 컨테이너를
					// 동시에 시작한다. FM 이 늦게 떠도 무해하다: 드라이버는 gRPC 등록 실패를
					// 초당 1회 재시도하고, GetTopology 는 FM 준비 여부와 무관하게 응답한다.
					Containers: []corev1.Container{
						{
							Name:            "plugin",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// 벤더 이미지는 ENTRYPOINT 가 없고 Cmd=[tt-dra-driver-kubeletplugin] 뿐이다.
							// Args 를 주면 Cmd 가 통째로 덮이므로 Command 를 명시하지 않으면 kubelet 이
							// 첫 인자(--device-profile)를 실행 파일로 exec 해 StartError 로 죽는다.
							Command: []string{"tt-dra-driver-kubeletplugin"},
							// force-fallback-discovery 를 넣지 않는다. 이 메인보드는
							// 벤더 화이트리스트에 없어 fallback 이 빈 ResourceSlice 를 낸다.
							Args: []string{
								"--device-profile", "tenstorrent",
								"--fabric-manager-agent-address",
								fmt.Sprintf("localhost:%d", draFMPortTenstorrent),
								"--cdi-root", draCDIRoot,
							},
							// tt-dra-driver-kubeletplugin 은 --node-name(=$NODE_NAME)이 필수다.
							// 다른 벤더 DRA 렌더처럼 downward API 로 노드명을 주입한다 — 없으면
							// "Required flag node-name not set" 로 크래시한다.
							Env: []corev1.EnvVar{
								{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
								}},
							},
							SecurityContext: &corev1.SecurityContext{Privileged: &priv},
							// plugins-registry 는 다른 세 벤더 모두가 마운트하는 kubelet 플러그인
							// 등록 경로다(표준 DRA kubeletplugin 라이브러리가 요구). 마운트 구성은
							// Furiosa chart 를 그대로 본떴으므로 이 경로도 포함한다 — 빠지면 kubelet
							// 이 플러그인을 찾지 못해 장치 주입이 조용히 실패한다(Rebellions 가
							// host-dev 볼륨 누락으로 겪은 것과 같은 종류).
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugins-registry", MountPath: "/var/lib/kubelet/plugins_registry"},
								{Name: "plugins", MountPath: "/var/lib/kubelet/plugins"},
								{Name: "cdi", MountPath: draCDIRoot},
								{Name: "host-dev", MountPath: "/dev"},
							},
						},
						{
							Name:            "fabric-manager",
							Image:           fmImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// 이미지 Cmd 는 tt-fabric-manager-controller 지만 plugin 이 dial 하는 건
							// localhost:50053 의 agent 다. 이미지에 tt-fabric-manager-agent 바이너리가
							// 있으므로 그것을 Command 로 명시한다(없으면 Args 가 Cmd 를 덮어 --host-id 를
							// exec 해 StartError — plugin 과 같은 원인).
							Command: []string{"tt-fabric-manager-agent"},
							// 등호 표기는 Unknown argument 로 거부된다. 공백 분리만 받는다.
							Args:            []string{"--host-id", "0"},
							SecurityContext: &corev1.SecurityContext{Privileged: &priv},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "fm-config", MountPath: "/etc/fabric-manager"},
								{Name: "host-dev", MountPath: "/dev"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "plugins-registry", VolumeSource: hostPathDir("/var/lib/kubelet/plugins_registry")},
						{Name: "plugins", VolumeSource: hostPathDir("/var/lib/kubelet/plugins")},
						{Name: "cdi", VolumeSource: hostPathDir(draCDIRoot)},
						{Name: "host-dev", VolumeSource: hostPathDir("/dev")},
						{Name: "host-etc-containerd", VolumeSource: hostPathDir("/etc/containerd")},
						{Name: "fm-config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: draDSNameTenstorrent},
							},
						}},
					},
				},
			},
		},
	}
	excludeDRARetiredNodes(&ds.Spec.Template.Spec, vendorTenstorrent)
	return ds
}
