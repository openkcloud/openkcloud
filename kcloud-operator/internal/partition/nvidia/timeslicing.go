// ============================================================
// timeslicing.go: NVIDIA device-plugin time-slicing config 렌더 + 기대 allocatable 계산
// 상세: DP 의 --config-file 스키마(sharing.timeSlicing.resources[].replicas)를 최소 필드로
// 렌더한다. CLI 플래그(--mig-strategy)는 config 파일 값을 덮으므로 flags 는 렌더하지 않는다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package nvidia

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition"
)

const (
	// SharingOwnerAnnotation 은 DP DaemonSet/ConfigMap 의 sharing 설정을 ACPP 가 소유함을 표시한다
	// (RNGD 의 PartitionOwnerAnnotation 과 동일한 2-writer 조정 패턴).
	SharingOwnerAnnotation = "npu.ai/sharing-owner"
	// SharingConfigKey 는 ConfigMap 내 키.
	SharingConfigKey = "config.yaml"
	// SharingConfigPath 는 컨테이너 안 마운트 경로.
	SharingConfigPath = "/etc/nvidia-device-plugin/config.yaml"
	// SharingVolumeName 은 DP pod 의 볼륨 이름.
	SharingVolumeName = "sharing-config"
	// MPSPipeDir 는 MPS 파이프/shm 의 **호스트** 경로다. control daemon(mps_control_daemon.go,
	// controller 패키지)이 이 값을 mpsPipeDir 로 alias 해 daemon 과 DP 클라이언트가 같은 호스트
	// 디렉터리를 보게 한다. DP 의 MPS_ROOT 도 이 값이다 — upstream 은 그것을 워크로드 pod 에
	// 주입할 마운트의 HostPath 로만 쓴다(internal/plugin/mps.go 의 hostRoot).
	MPSPipeDir = "/tmp/nvidia-mps"
	// MPSContainerRoot 는 위 호스트 경로를 **컨테이너 안에서** 보는 고정 경로다. daemon 과 DP
	// 두 바이너리 모두 이 값을 하드코딩하며(upstream cmd/mps-control-daemon/mps/root.go:26 의
	// ContainerRoot, DP 는 internal/plugin/mps.go:55 가 그것으로 health-check daemon 을 만든다)
	// 설정으로 바꿀 수 없다. 두 렌더러가 각자 "/mps" 를 적으면 다시 어긋나므로 단일 출처로 둔다.
	MPSContainerRoot = "/mps"
	// mpsPipeVolumeName 은 DP pod 에 배선하는 MPS 파이프 hostPath 볼륨 이름.
	mpsPipeVolumeName = "mps-pipe"
	// mpsShmVolumeName 은 DP pod 에 배선하는 MPS shm hostPath 볼륨 이름. upstream 주석대로 MPS
	// daemon health-check 에 필요하다(daemonset-device-plugin.yml).
	mpsShmVolumeName = "mps-shm"
	// mpsRootEnvName 은 DP 바이너리가 요구하는 필수 env 다 — upstream(cmd/nvidia-device-plugin/main.go)
	// 은 sharing strategy 가 mps 면 --mps-root/MPS_ROOT 미설정 시 하드 에러로 기동을 거부한다.
	mpsRootEnvName = "MPS_ROOT"
)

// dpSharingConfig 는 NVIDIA device-plugin config 파일의 우리가 쓰는 부분집합이다.
type dpSharingConfig struct {
	Version string     `json:"version"`
	Sharing *dpSharing `json:"sharing,omitempty"`
}

type dpSharing struct {
	TimeSlicing *dpReplicatedResources `json:"timeSlicing,omitempty"`
	MPS         *dpReplicatedResources `json:"mps,omitempty"`
}

type dpReplicatedResources struct {
	RenameByDefault            bool                   `json:"renameByDefault"`
	FailRequestsGreaterThanOne bool                   `json:"failRequestsGreaterThanOne"`
	Resources                  []dpReplicatedResource `json:"resources"`
}

type dpReplicatedResource struct {
	Name     string `json:"name"`
	Replicas int32  `json:"replicas"`
}

// RenderSharingConfig 는 광고 중인 리소스 이름들에 대해 replica 배수를 적용한 config YAML 을 만든다.
// resources 의 값(개수)은 쓰지 않고 이름만 쓴다 — replica 는 이름 단위 설정이다.
func RenderSharingConfig(resources map[string]int32, l partition.SharingLayout) (string, error) {
	cfg := dpSharingConfig{Version: "v1"}
	switch l.Mode {
	case v1alpha1.SharingModeTimeSliced, v1alpha1.SharingModeMPS:
		if len(resources) == 0 {
			return "", fmt.Errorf("nvidia: no advertised resources to share (%s)", l.Mode)
		}
		names := make([]string, 0, len(resources))
		for n := range resources {
			names = append(names, n)
		}
		sort.Strings(names) // 결정론적 렌더(ConfigMap diff 안정)
		rr := &dpReplicatedResources{
			RenameByDefault:            false, // 리소스 이름 유지 — 사용자 YAML 이 바뀌지 않게.
			FailRequestsGreaterThanOne: l.FailRequestsGreaterThanOne,
		}
		for _, n := range names {
			rr.Resources = append(rr.Resources, dpReplicatedResource{Name: n, Replicas: l.Replicas})
		}
		cfg.Sharing = &dpSharing{}
		if l.Mode == v1alpha1.SharingModeMPS {
			// upstream(api/config/v1/config.go)은 sharing.mps.failRequestsGreaterThanOne 이
			// 미설정(nil)이면 true 로 승격한다 — "historical MPS 동작" 보존(replicas>1 요청 거부).
			// 우리는 이 필드를 항상 명시값으로 렌더하므로(omitempty 없음) 그 nil-승격 경로를 절대
			// 못 탄다. MPSSpec 에는 이를 뒤집을 스펙 필드가 없으므로(범위 밖) 렌더 시점에 같은
			// 기본값을 직접 낸다 — 안 그러면 upstream 의 mps 전용 안전장치를 조용히 뒤집는다.
			rr.FailRequestsGreaterThanOne = true
			cfg.Sharing.MPS = rr
		} else {
			cfg.Sharing.TimeSlicing = rr
		}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ExpectedShared 는 공유 적용 후 기대 allocatable 이다(exclusive 는 항등).
func ExpectedShared(base map[string]int32, l partition.SharingLayout) map[string]int32 {
	out := make(map[string]int32, len(base))
	for k, v := range base {
		if l.Mode == v1alpha1.SharingModeTimeSliced || l.Mode == v1alpha1.SharingModeMPS {
			out[k] = v * l.Replicas
			continue
		}
		out[k] = v
	}
	return out
}

// dpNamespace 는 sharing 배선 대상 device-plugin DaemonSet·ConfigMap 이 사는 네임스페이스다.
const (
	dpNamespace = "kube-system"

	// MigActiveNodeLabel 은 이 노드에 ACPP 가 실제로 관리 중인 MIG GI 가 있는지를 나타낸다.
	// device-plugin 은 이 라벨로 두 DaemonSet(mixed/flat) 중 어느 쪽이 그 노드를 맡을지 가른다 —
	// upstream 이 --mig-strategy=mixed 와 sharing.mps 동시 사용을 거부하므로, 그 플래그를 모든
	// 노드에 무조건 렌더하면 MPS 가 클러스터 전역에서 구조적으로 막힌다. 값은 정확히 "true"
	// 문자열이거나 부재(= flat)다.
	MigActiveNodeLabel = "kcloud.ai/nvidia.mig-active"

	// DevicePluginNameMixed 는 기존 dpName 값과 동일 — 라이브 리소스 이름을 바꾸지 않는다.
	DevicePluginNameMixed = "nvidia-device-plugin"
	DevicePluginNameFlat  = "nvidia-device-plugin-flat"

	SharingConfigMapNameMixed = "nvidia-device-plugin-sharing"
	SharingConfigMapNameFlat  = "nvidia-device-plugin-flat-sharing"
)

// DevicePluginTargetForNode 는 노드의 MigActiveNodeLabel 값에 따라 이 노드를 맡는
// device-plugin DaemonSet 이름과 그 sharing ConfigMap 이름을 돌려준다. 노드 조회 실패는
// 그대로 전파한다(호출자가 판정 불가로 처리 — requeue).
func DevicePluginTargetForNode(ctx context.Context, c client.Client, node string) (dsName, cmName string, err error) {
	var n corev1.Node
	if err := c.Get(ctx, types.NamespacedName{Name: node}, &n); err != nil {
		return "", "", err
	}
	if n.Labels[MigActiveNodeLabel] == "true" {
		return DevicePluginNameMixed, SharingConfigMapNameMixed, nil
	}
	return DevicePluginNameFlat, SharingConfigMapNameFlat, nil
}

// DevicePluginRolling 은 이 노드를 맡는 device-plugin DaemonSet 이 지금 롤아웃 중인지다.
// 롤링 중에는 allocatable 이 잠깐 비는 것이 정상이므로, 광고 감시는 이 창을 장애로 세지 않는다.
// DaemonSet 이 없으면 false 다 — 없는 상태의 광고 부재는 억제할 것이 아니라 드러낼 것이다.
func DevicePluginRolling(ctx context.Context, c client.Client, node string) (bool, error) {
	dsName, _, err := DevicePluginTargetForNode(ctx, c, node)
	if err != nil {
		return false, err
	}
	var ds appsv1.DaemonSet
	if err := c.Get(ctx, types.NamespacedName{Name: dsName, Namespace: dpNamespace}, &ds); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	st := ds.Status
	switch {
	case st.ObservedGeneration < ds.Generation:
		return true, nil // 컨트롤러가 아직 최신 spec 을 못 봤다
	case st.NumberUnavailable > 0:
		return true, nil
	case st.UpdatedNumberScheduled < st.DesiredNumberScheduled:
		return true, nil
	case st.NumberReady < st.DesiredNumberScheduled:
		return true, nil
	}
	return false, nil
}

// SharingConfigMapNameOf 는 live DS 에 이미 배선된 sharing ConfigMap 이름을 읽는다
// (NPUClusterPolicy 의 2-writer 보존이 어느 이름으로 재배선해야 하는지 판단하는 데 쓴다).
// 배선이 없으면 mixed 기본값으로 fallback(과거 단일-DS 상태와 동일 동작 보존).
func SharingConfigMapNameOf(ds *appsv1.DaemonSet) string {
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == SharingVolumeName && v.ConfigMap != nil {
			return v.ConfigMap.Name
		}
	}
	return SharingConfigMapNameMixed
}

// ValidateSharing 은 NVIDIA 가 받을 수 있는 공유 요청인지 본다(현재는 벤더 중립 규칙과 동일 —
// time-slicing 은 DP 기능이라 하드웨어 제약이 없다).
func (b *Backend) ValidateSharing(l partition.SharingLayout) error {
	return partition.ValidateSharingLayout(l)
}

// ExpectedSharedAllocatable 은 공유 적용 후 기대 allocatable 이다(계산값 — 가정하지 않는다).
func (b *Backend) ExpectedSharedAllocatable(base map[string]int32, l partition.SharingLayout) map[string]int32 {
	return ExpectedShared(base, l)
}

// ApplySharing 은 sharing ConfigMap 을 쓰고 DP DaemonSet 에 배선 + owner 어노테이션을 남긴다.
// 반환 state 는 rollback 용 스냅샷(이전 ConfigMap 내용·존재 여부)이다. 순서 주의: 검증 → 소유
// 확인 → ConfigMap → DS — 어떤 mutation 보다 먼저 fail-closed 한다(RenderSharingConfig 는
// replicas 를 검증하지 않으므로 여기서 막지 않으면 DP 가 기동 실패한다). ConfigMap 단계는
// ApplySharingConfig 로 분리돼 있다 — mps-control-daemon 이 그 CM 을 마운트해 기동하므로
// 호출자는 daemon 을 띄우기 전에 CM 만 먼저 쓸 수 있어야 한다. 여기서 다시 부르는 것은 멱등이다.
func (b *Backend) ApplySharing(t partition.Target, l partition.SharingLayout, base map[string]int32) (*partition.SharingRollbackState, error) {
	st, err := b.ApplySharingConfig(t, l, base)
	if err != nil {
		return nil, err
	}
	dsName, cmName, err := DevicePluginTargetForNode(t.Ctx, b.c, t.NodeName)
	if err != nil {
		return st, err // st 는 non-nil — ConfigMap 은 이미 썼다(원복 근거).
	}
	var ds appsv1.DaemonSet
	if err := b.c.Get(t.Ctx, types.NamespacedName{Name: dsName, Namespace: dpNamespace}, &ds); err != nil {
		return st, err
	}
	prev := ds.DeepCopy()
	if ds.Annotations == nil {
		ds.Annotations = map[string]string{}
	}
	ds.Annotations[SharingOwnerAnnotation] = t.Owner
	WireSharing(&ds, l.Mode, cmName)
	if err := b.c.Patch(t.Ctx, &ds, client.MergeFrom(prev)); err != nil {
		return st, err // st 는 non-nil — 부분 적용(ConfigMap 만) 원복 근거.
	}
	return st, nil
}

// ApplySharingConfig 는 ApplySharing 의 ConfigMap 단계까지만 수행한다(DS 배선은 하지 않는다).
// mps-control-daemon 은 이 ConfigMap 을 마운트해 기동하므로 daemon 보다 먼저 존재해야 한다 —
// 거꾸로면 daemon pod 이 ContainerCreating(ConfigMap not found)에 갇혀 준비 게이트를 못 넘는다.
// DS 소유 확인은 여기 남아 있다: 남의 DS 를 겨냥한 요청이면 ConfigMap 조차 쓰지 않아야 한다.
func (b *Backend) ApplySharingConfig(t partition.Target, l partition.SharingLayout, base map[string]int32) (*partition.SharingRollbackState, error) {
	if err := b.ValidateSharing(l); err != nil {
		return nil, err
	}
	dsName, cmName, err := DevicePluginTargetForNode(t.Ctx, b.c, t.NodeName)
	if err != nil {
		return nil, err
	}
	var ds appsv1.DaemonSet
	if err := b.c.Get(t.Ctx, types.NamespacedName{Name: dsName, Namespace: dpNamespace}, &ds); err != nil {
		return nil, err
	}
	if owner := ds.Annotations[SharingOwnerAnnotation]; owner != "" && owner != t.Owner {
		return nil, fmt.Errorf("nvidia: device-plugin sharing owned by %q, not %q", owner, t.Owner)
	}
	rendered, err := RenderSharingConfig(base, l)
	if err != nil {
		return nil, err
	}

	st := &partition.SharingRollbackState{}
	var cm corev1.ConfigMap
	err = b.c.Get(t.Ctx, types.NamespacedName{Name: cmName, Namespace: dpNamespace}, &cm)
	switch {
	case err == nil:
		// 소유 판정은 ConfigMap 자신의 표시로 한다. DS 의 annotation 으로 판정하면 DS 가 재생성될 때
		// (수동 삭제 / helm replace) 표시만 사라지고 ConfigMap 은 남아, 자기가 만든 ConfigMap 을
		// 자기 것으로 인정하지 못한 채 영구히 apply 실패한다(자가치유 불가). 소유 표시는 소유물에 붙인다.
		// 타 소유 표시가 붙어 있으면 덮지 않는다 — 스냅샷은 프로세스 메모리에만 있어 삭제 경로
		// (zero-state)에서 복원할 수 없고, 조용히 지우면 전 nvidia 노드 광고가 바뀐다.
		if owner := cm.Annotations[SharingOwnerAnnotation]; owner != "" && owner != t.Owner {
			return nil, fmt.Errorf("nvidia: sharing configmap %s/%s exists but is owned by %q, not %q", dpNamespace, cmName, owner, t.Owner)
		}
		st.ConfigMapExisted = true
		st.PrevConfigYAML = cm.Data[SharingConfigKey]
		if cm.Annotations == nil {
			cm.Annotations = map[string]string{}
		}
		cm.Annotations[SharingOwnerAnnotation] = t.Owner
		cm.Data = map[string]string{SharingConfigKey: rendered}
		if err := b.c.Update(t.Ctx, &cm); err != nil {
			return nil, err
		}
	case apierrors.IsNotFound(err):
		cm = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: cmName, Namespace: dpNamespace,
				Annotations: map[string]string{SharingOwnerAnnotation: t.Owner},
			},
			Data: map[string]string{SharingConfigKey: rendered},
		}
		if err := b.c.Create(t.Ctx, &cm); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return st, nil
}

// resolveRollbackTarget 은 rollback 대상 DS/CM 이름을 정한다. apply 는 그 시점 노드 라벨로
// 대상을 고르는데, 그 사이 라벨이 뒤집히면(형제 MIG 정책 삭제로 syncMigActiveLabel(false))
// 지금 라벨을 다시 읽는 것만으론 자기가 만든 DS/CM 을 잃는다(조용한 no-op). 그래서 소유
// 표시부터 찾는다 — mixed/flat 을 훑어 SharingOwnerAnnotation==t.Owner 인 DS 를 찾고, 그
// DS 에 실제로 배선된 CM 을 대상으로 삼는다. DS 도 소유를 주장하지 않으면 CM 소유 표시로
// 재시도하고, 그마저 없으면 현재 노드 라벨 기반 해석으로 fallback 한다.
func (b *Backend) resolveRollbackTarget(t partition.Target) (dsName, cmName string, err error) {
	for _, name := range []string{DevicePluginNameMixed, DevicePluginNameFlat} {
		var ds appsv1.DaemonSet
		getErr := b.c.Get(t.Ctx, types.NamespacedName{Name: name, Namespace: dpNamespace}, &ds)
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				continue
			}
			return "", "", getErr
		}
		if ds.Annotations[SharingOwnerAnnotation] == t.Owner {
			return name, SharingConfigMapNameOf(&ds), nil
		}
	}
	for _, pair := range []struct{ cm, ds string }{
		{SharingConfigMapNameMixed, DevicePluginNameMixed},
		{SharingConfigMapNameFlat, DevicePluginNameFlat},
	} {
		var cm corev1.ConfigMap
		getErr := b.c.Get(t.Ctx, types.NamespacedName{Name: pair.cm, Namespace: dpNamespace}, &cm)
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				continue
			}
			return "", "", getErr
		}
		if cm.Annotations[SharingOwnerAnnotation] == t.Owner {
			return pair.ds, pair.cm, nil
		}
	}
	return DevicePluginTargetForNode(t.Ctx, b.c, t.NodeName)
}

// RollbackSharing 은 이전 ConfigMap 내용을 복원하고(없었으면 삭제) 배선·owner 를 되돌린다.
// 이전에도 sharing config 가 있었다면 배선은 유지한다(외부 설정을 끊지 않는다).
// 소유 검사가 **가장 먼저**다 — ConfigMap 을 먼저 지우면 타 소유 DS 배선이 없는 볼륨을 마운트하게 되고,
// DP DaemonSet 은 클러스터 전역 1개라 전 nvidia 노드의 GPU 광고가 사라진다.
func (b *Backend) RollbackSharing(t partition.Target, s partition.SharingRollbackState) error {
	dsName, cmName, err := b.resolveRollbackTarget(t)
	if err != nil {
		return err
	}
	var ds appsv1.DaemonSet
	dsErr := b.c.Get(t.Ctx, types.NamespacedName{Name: dsName, Namespace: dpNamespace}, &ds)
	if dsErr != nil && !apierrors.IsNotFound(dsErr) {
		return dsErr
	}
	if owner := ds.Annotations[SharingOwnerAnnotation]; owner != "" && owner != t.Owner {
		return nil // 타 ACPP 소유 — ConfigMap 도 배선도 건드리지 않는다.
	}

	var cm corev1.ConfigMap
	cmGetErr := b.c.Get(t.Ctx, types.NamespacedName{Name: cmName, Namespace: dpNamespace}, &cm)
	if cmGetErr == nil {
		if owner := cm.Annotations[SharingOwnerAnnotation]; owner != "" && owner != t.Owner {
			return nil // 타 소유 ConfigMap — 삭제 경로(zero-state)가 남의 설정을 지우지 않는다.
		}
	}
	switch {
	case cmGetErr == nil && s.ConfigMapExisted:
		cm.Data = map[string]string{SharingConfigKey: s.PrevConfigYAML}
		if err := b.c.Update(t.Ctx, &cm); err != nil {
			return err
		}
	case cmGetErr == nil:
		if err := b.c.Delete(t.Ctx, &cm); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	case !apierrors.IsNotFound(cmGetErr):
		return cmGetErr
	}
	if s.ConfigMapExisted || apierrors.IsNotFound(dsErr) {
		return nil // 이전 설정이 살아있으면 배선·owner 유지(둘은 함께 움직인다). DS 부재면 되돌릴 배선도 없다.
	}
	prev := ds.DeepCopy()
	delete(ds.Annotations, SharingOwnerAnnotation)
	UnwireSharing(&ds)
	return b.c.Patch(t.Ctx, &ds, client.MergeFrom(prev))
}

// WireSharing 은 DP DaemonSet 에 sharing config 볼륨을 붙인다. mps 모드면 MPS 파이프
// 디렉터리도 함께 붙인다 — control daemon 과 같은 디렉터리를 봐야 클라이언트가 붙는다.
// ACPP 가 공유를 적용할 때와 NPUClusterPolicy 가 소유 DS 를 재렌더할 때 같은 함수를 쓴다.
func WireSharing(ds *appsv1.DaemonSet, mode string, cmName string) {
	ctr := &ds.Spec.Template.Spec.Containers[0]
	arg := "--config-file=" + SharingConfigPath
	if !slices.Contains(ctr.Args, arg) {
		ctr.Args = append(ctr.Args, arg)
	}
	if !slices.ContainsFunc(ctr.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == SharingVolumeName }) {
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{Name: SharingVolumeName, MountPath: path.Dir(SharingConfigPath)})
	}
	if !slices.ContainsFunc(ds.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == SharingVolumeName }) {
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: SharingVolumeName,
			VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			}},
		})
	}
	if mode != v1alpha1.SharingModeMPS {
		return
	}
	// MPS_ROOT — 없으면 DP 바이너리가 기동 자체를 거부한다(하드 에러, silent no-op 이 아니다).
	if !slices.ContainsFunc(ctr.Env, func(e corev1.EnvVar) bool { return e.Name == mpsRootEnvName }) {
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: mpsRootEnvName, Value: MPSPipeDir})
	}
	// 마운트 경로는 컨테이너 고정 경로다(호스트 경로 MPSPipeDir 가 아니다). DP 바이너리의 MPS
	// health-check 는 /mps 만 보므로, 호스트 경로 그대로 마운트하면 daemon 이 정상이어도
	// "failed to send command to MPS daemon" 으로 기동에 실패한다(라이브 v0.5.77).
	if !slices.ContainsFunc(ctr.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == mpsPipeVolumeName }) {
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{Name: mpsPipeVolumeName, MountPath: MPSContainerRoot})
	}
	// mps-shm — upstream 주석: "The MPS /dev/shm is needed to allow for MPS daemon health-checking".
	if !slices.ContainsFunc(ctr.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == mpsShmVolumeName }) {
		ctr.VolumeMounts = append(ctr.VolumeMounts, corev1.VolumeMount{Name: mpsShmVolumeName, MountPath: "/dev/shm"})
	}
	hostPathDir := corev1.HostPathDirectoryOrCreate
	if !slices.ContainsFunc(ds.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == mpsPipeVolumeName }) {
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: mpsPipeVolumeName,
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: MPSPipeDir, Type: &hostPathDir,
			}},
		})
	}
	if !slices.ContainsFunc(ds.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == mpsShmVolumeName }) {
		ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: mpsShmVolumeName,
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: MPSPipeDir + "/shm", Type: &hostPathDir,
			}},
		})
	}
}

// UnwireSharing 은 WireSharing 의 역이다. mode 를 받지 않는다 — MPS 볼륨 제거는 존재 여부만
// 보는 멱등 삭제라 모드 인자가 없어도 대칭이 깨지지 않는다(DeleteFunc 는 없으면 no-op).
func UnwireSharing(ds *appsv1.DaemonSet) {
	ctr := &ds.Spec.Template.Spec.Containers[0]
	ctr.Args = slices.DeleteFunc(ctr.Args, func(a string) bool { return strings.HasPrefix(a, "--config-file=") })
	ctr.Env = slices.DeleteFunc(ctr.Env, func(e corev1.EnvVar) bool { return e.Name == mpsRootEnvName })
	ctr.VolumeMounts = slices.DeleteFunc(ctr.VolumeMounts, func(m corev1.VolumeMount) bool {
		return m.Name == SharingVolumeName || m.Name == mpsPipeVolumeName || m.Name == mpsShmVolumeName
	})
	ds.Spec.Template.Spec.Volumes = slices.DeleteFunc(ds.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool {
		return v.Name == SharingVolumeName || v.Name == mpsPipeVolumeName || v.Name == mpsShmVolumeName
	})
}

// HasMPSVolume 은 live DS 에 이미 MPS 파이프 볼륨이 배선돼 있는지 본다 — NPUClusterPolicy 의
// 2-writer 보존(preserveNvidiaSharing)이 이전에 적용된 모드를 판별하는 데 쓴다.
func HasMPSVolume(ds *appsv1.DaemonSet) bool {
	return slices.ContainsFunc(ds.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == mpsPipeVolumeName })
}
