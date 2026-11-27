// ============================================================
// mps_control_daemon.go: NVIDIA MPS(Multi-Process Service) control daemon 배포
// 상세: device-plugin config 에 sharing.mps 를 넣는 것만으로는 MPS 가 동작하지 않는다 — 노드에
// control daemon 이 떠서 파이프 디렉터리(mpsPipeDir)를 만들어야 하고, device-plugin pod 은
// 같은 디렉터리를 봐야 daemon 에 붙는다. dcgm_exporter.go 와 같은 규약의 opt-in operand 로,
// ACPP 가 sharing.mode=mps 를 요청하면 뜨고 다른 모드로 돌아가면 사라진다(별도 helm 토글 없음).
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/partition/nvidia"
)

const (
	// mpsControlDaemonDSName 은 배포되는 DaemonSet 이름이다. 벤더 이미지를 그대로 쓰므로
	// kcloud- 접두사를 붙이지 않는다(kube-system, 3rd-party 이미지 규약. dcgm-exporter 와 같다).
	mpsControlDaemonDSName = "nvidia-mps-control-daemon"
	// mpsControlDaemonLegacyDSName 은 접두사를 떼기 전 이름이다(v0.7.28 이하). 한 번 정리하면 없는 이름이다.
	mpsControlDaemonLegacyDSName = "kcloud-mps-control-daemon"
	// mpsPipeDir 는 MPS 파이프/shm 디렉터리다. device-plugin 쪽 배선(WireSharing)과 같은 경로를
	// 봐야 하므로 nvidia.MPSPipeDir 를 단일 출처로 alias 한다.
	mpsPipeDir = nvidia.MPSPipeDir
	// mpsControlImageDefault 는 ACPP_MPS_CONTROL_IMAGE 미설정 시 기본 이미지다. device-plugin 과
	// 같은 이미지다 — mps-control-daemon 은 그 바이너리의 서브커맨드다(upstream 과 동일 구조).
	mpsControlImageDefault = "nvcr.io/nvidia/k8s-device-plugin:v0.19.3"
	// mpsContainerRoot 는 daemon 컨테이너 내부의 고정 마운트 경로다. mps-control-daemon 바이너리는
	// ContainerRoot="/mps" 를 하드코딩한다(설정 불가 — upstream cmd/mps-control-daemon/mps/root.go:26).
	// 호스트 쪽 hostPath.Path 는 계속 mpsPipeDir(DP 와 공유하는 경로).
	// DP 쪽 배선(WireSharing)도 같은 값을 써야 둘이 같은 파이프를 본다 — 단일 출처를 alias 한다.
	mpsContainerRoot = nvidia.MPSContainerRoot
)

// ensureMpsControlDaemon 은 mode 가 mps 면 daemon DS 를 보장하고, 아니면 제거한다(멱등).
// runSharing 은 배선 적용 "전에" 이를 호출한다 — client(DP)가 daemon 보다 먼저 뜨면 붙지 못한다.
// self 는 호출한 ACPP 자신의 이름이다 — 참조 카운트(anyPolicyStillUsesMPS)가 자기 자신의 아직
// 안 지워진 저널을 "누군가 아직 쓴다"로 잘못 세지 않게 제외하는 데 쓴다.
// node 는 이 호출이 실제로 겨냥하는 노드다 — 준비 게이트를 그 노드로 한정하는 데만 쓴다(mps 모드
// 전용이며, 정리 경로는 클러스터 전역이라 node 를 보지 않는다).
func (r *AcceleratorPartitionPolicyReconciler) ensureMpsControlDaemon(ctx context.Context, mode, self, node string) error {
	log := logf.FromContext(ctx)
	if mode != npuv1alpha1.SharingModeMPS {
		// daemon 은 클러스터 전역 singleton 이다 — 다른 ACPP 가 아직 mps 를
		// 쓰면 지우면 안 된다. exclusive 가 기본값이라 매 평범한 reconcile/삭제가 이 분기를 타므로,
		// 여기서 참조 카운트 없이 지우면 두 번째 NVIDIA ACPP 가 생기는 순간 daemon 이 항상 지워진다.
		var cur appsv1.DaemonSet
		key := types.NamespacedName{Name: mpsControlDaemonDSName, Namespace: "kube-system"}
		if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
			return nil // 이미 없다 — List 도 Delete 도 할 게 없다.
		} else if err != nil {
			return err
		}
		stillUsed, err := r.anyPolicyStillUsesMPS(ctx, self)
		if err != nil {
			return err
		}
		if stillUsed {
			return nil
		}
		if err := r.Delete(ctx, &cur); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}

	// 개명 전 DS 가 남아 있으면 먼저 지운다 — DS selector 가 immutable 이라 이름을 바꾼 자리는
	// 새로 만들 수밖에 없고, 둘이 같이 살면 control daemon 이 노드당 둘이 된다.
	legacy := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: mpsControlDaemonLegacyDSName, Namespace: "kube-system"}}
	if err := r.Delete(ctx, legacy); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to delete pre-rename mps-control-daemon daemonset")
		return err
	}

	desired := renderMpsControlDaemonDS()
	var cur appsv1.DaemonSet
	key := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}
	if err := r.Get(ctx, key, &cur); apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			log.Error(err, "failed to create mps-control-daemon daemonset")
			return err
		}
	} else if err != nil {
		log.Error(err, "failed to get mps-control-daemon daemonset")
		return err
	} else if !equality.Semantic.DeepEqual(cur.Spec, desired.Spec) {
		cur.Spec = desired.Spec
		if err := r.Update(ctx, &cur); err != nil {
			log.Error(err, "failed to update mps-control-daemon daemonset")
			return err
		}
	}
	log.Info("mps-control-daemon daemonset ensured", "image", desired.Spec.Template.Spec.Containers[0].Image)

	// 준비 게이트. DS 객체가 있다는 것과 daemon 이 돌고 있다는 것은 다르다 — 이미지를 못 당기면
	// (air-gap 에서 코드 기본값 nvcr.io 를 쓰면 실제로 그렇다) DS 는 만들어지고 pod 는
	// ImagePullBackOff 로 남는데, 호출자의 verify 는 광고 개수만 보므로 daemon 부재를 못 잡는다.
	// 판정은 이 정책이 겨냥한 노드로만 한정한다 — DS 전체의 NumberReady == DesiredNumberScheduled
	// 를 요구하면 다른 노드의 daemon pod 장애가 이 정책을 영원히 Applying 에 가둔다. drain/quiesce
	// 단계에 시한이 없고 Failed 전이도 없어, 한번 갇히면 복구 경로가 없다.
	ready, err := r.mpsDaemonReadyOnNode(ctx, node, desired) // 갓 Create 한 노드는 pod 이 아직 없어 여기서 걸리는 것이 정상이다.
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: node=%s has no ready daemon pod (ds numberReady=%d desiredNumberScheduled=%d)",
			ErrMpsDaemonNotReady, node, cur.Status.NumberReady, cur.Status.DesiredNumberScheduled)
	}
	return nil
}

// ErrMpsDaemonNotReady 는 대상 노드의 mps-control-daemon pod 이 아직 Ready 가 아님을 뜻한다.
// 이미지 pull·스케줄링은 transient 이므로 호출자는 이것을 Failed 가 아니라 Applying + requeue 로
// 받아야 한다(runSharing).
var ErrMpsDaemonNotReady = errors.New("mps control daemon not ready")

// mpsDaemonReadyOnNode 는 대상 노드에서 daemon pod 하나가 Ready 인지 본다. DaemonSet status 에는
// 노드별 준비 상태 필드가 없어(집계값뿐) pod 를 직접 본다 — 노드 수만큼의 작은 목록이라 라벨
// 셀렉터로 받고 노드 필터는 메모리에서 한다. 셀렉터는 렌더러(ds)에서 파생한다 — 라벨을 여기
// 복제하면 렌더러 라벨이 바뀐 순간 이 게이트가 아무 pod 도 못 찾는다. 매칭되는 pod 중 하나라도
// Ready 면 준비된 것으로 본다(any-ready) — 롤링 업데이트 중에는 구 pod 이 여전히 daemon 을
// 서비스하고 파이프 디렉터리는 hostPath 라 유지되므로, "지금 동작하는 daemon 이 있는가" 에는 맞다.
func (r *AcceleratorPartitionPolicyReconciler) mpsDaemonReadyOnNode(ctx context.Context, node string, ds *appsv1.DaemonSet) (bool, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ds.Namespace),
		client.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName != node {
			continue
		}
		for _, c := range pods.Items[i].Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
	}
	return false, nil
}

// anyPolicyStillUsesMPS 는 daemon 을 지우기 전에 다른 ACPP 가 아직 mps 를 쓰는지 전수 확인한다.
// 단일 owner annotation(SharingOwnerAnnotation 류)은 "동시에 여러 정책이 쓴다"를 표현할 수 없어
// (한 writer 전제) 여기서는 List 로 참조 카운트한다. self(호출한 ACPP 자신)만 제외한다 —
// handleNvidiaDeletion 은 자기 자신의 저널을 지우기 전에 이 함수를 부르므로, 자기 자신의 stale
// mps 기록을 "아직 쓰는 중" 으로 잘못 세면 그 ACPP 는 영원히 지워지지 않는다. DeletionTimestamp
// 있는 정책을 전부 제외하면 안 된다 — 롤백을 아직 못 한 다른 정책까지 카운트에서 영구히 빠진다.
func (r *AcceleratorPartitionPolicyReconciler) anyPolicyStillUsesMPS(ctx context.Context, self string) (bool, error) {
	var list npuv1alpha1.AcceleratorPartitionPolicyList
	if err := r.List(ctx, &list); err != nil {
		return false, err
	}
	for _, p := range list.Items {
		if p.Name == self {
			continue
		}
		for _, rec := range p.Status.ApplyRecords {
			if rec.SharingMode == npuv1alpha1.SharingModeMPS {
				return true, nil
			}
		}
	}
	return false, nil
}

// renderMpsControlDaemonDS 는 MPS control daemon DaemonSet 을 만든다. privileged + 파이프/shm
// 마운트로 upstream 과 같은 daemon↔client 통신 경로를 재현한다. upstream 의 config-manager
// 사이드카 선택 단계는 없다 — 이 저장소는 descriptor 하나당 단일 CM 을 직접 렌더해 CONFIG_FILE
// 로 바로 가리킨다. sharing ConfigMap 은 device-plugin 과 같은 것을 마운트해야 한다 — 다르면
// daemon 이 기본값(strategy=none)으로 기동해 서버를 아예 안 띄운다. 볼륨 이름(mps-pipe)은 두
// 컨테이너의 VolumeMounts 와 같아야 한다 — 바꾸면 롤링 업데이트 중 마운트가 어긋난다.
func renderMpsControlDaemonDS() *appsv1.DaemonSet {
	image := os.Getenv("ACPP_MPS_CONTROL_IMAGE")
	if image == "" {
		image = mpsControlImageDefault
	}

	labels := map[string]string{
		"app.kubernetes.io/name":      mpsControlDaemonDSName,
		"app.kubernetes.io/component": "mps-control-daemon",
	}
	nvidiaRuntime := vendorNvidia
	hostPathDir := corev1.HostPathDirectoryOrCreate
	bidirectional := corev1.MountPropagationBidirectional

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mpsControlDaemonDSName,
			Namespace: "kube-system",
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					// dcgm-exporter 와 같은 자립 라벨(node-manager 부여, NFD 비의존)로 nvidia 노드를
					// 고르되, mig-active 노드는 뺀다 — 그 노드에는 MPS 가 올 수 없다. 개별 GPU 가
					// MIG 로 MPS 비호환인 경우까지 여기서 가리지는 않는다(capability 판정은 sharing validate 몫).
					// NodeSelector 로는 "라벨 부재" 를 표현할 수 없어 flat device-plugin DS
					// (buildNvidiaDevicePluginDS)와 같은 NodeAffinity + DoesNotExist 패턴을 쓴다.
					Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: vendorPresentLabel(vendorNvidia), Operator: corev1.NodeSelectorOpIn, Values: []string{labelValueTrue}},
								{Key: nvidia.MigActiveNodeLabel, Operator: corev1.NodeSelectorOpDoesNotExist},
							}}},
						},
					}},
					RuntimeClassName: &nvidiaRuntime,
					Tolerations:      []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					// upstream 기본값(enableHostPID=false 시 shareProcessNamespace: true) — daemon 이
					// client 프로세스를 봐야 하는 것은 PID 네임스페이스지 IPC 네임스페이스가 아니다.
					ShareProcessNamespace: boolPtr(true),
					InitContainers: []corev1.Container{{
						Name:            "mps-control-daemon-mounts",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						// upstream 과 동일: shm 을 준비하고 Bidirectional 전파로 host 에 올린다.
						// 이 단계가 없으면 host 재부팅 후 남은 shm 이나 크기 상한 없는 디렉터리를
						// 그대로 쓰게 된다.
						Command:         []string{"mps-control-daemon", "mount-shm"},
						SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
						VolumeMounts: []corev1.VolumeMount{{
							Name:             "mps-pipe",
							MountPath:        mpsContainerRoot,
							MountPropagation: &bidirectional,
						}},
					}},
					Containers: []corev1.Container{{
						Name:            "mps-control-daemon",
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"mps-control-daemon"},
						Env: []corev1.EnvVar{
							{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"},
							{Name: "NVIDIA_DRIVER_CAPABILITIES", Value: "compute,utility"},
							// --config-file 의 env 형태(cmd/mps-control-daemon/main.go). 볼륨만 붙이고
							// 이걸 빼면 daemon 은 마운트된 파일을 읽지 않는다.
							{Name: "CONFIG_FILE", Value: nvidia.SharingConfigPath},
						},
						SecurityContext: &corev1.SecurityContext{Privileged: boolPtr(true)},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "mps-pipe", MountPath: mpsContainerRoot},
							{Name: "mps-shm", MountPath: "/dev/shm"},
							{Name: nvidia.SharingVolumeName, MountPath: path.Dir(nvidia.SharingConfigPath)},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "mps-pipe",
							VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
								Path: mpsPipeDir, Type: &hostPathDir,
							}},
						},
						{
							Name: "mps-shm",
							VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
								Path: mpsPipeDir + "/shm", Type: &hostPathDir,
							}},
						},
						{
							Name: nvidia.SharingVolumeName,
							VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: nvidia.SharingConfigMapNameFlat},
							}},
						},
					},
				},
			},
		},
	}
	// control-plane/master 는 제외 — kcloud-nvidia-toolkit(toolkit_daemonset_controller.go)이
	// 이미 같은 이유로 그 노드를 건너뛰어 containerd 에 nvidia 런타임이 없다. 이 daemon 도
	// RuntimeClassName: nvidia 를 요구하므로 exclusion 없이 control-plane 라벨(kcloud.ai/nvidia.present)이
	// 붙은 노드로 스케줄되면 FailedCreatePodSandBox("no runtime for nvidia")로 영구 대기한다.
	applyControlPlaneExclusion(&ds.Spec.Template.Spec)
	return ds
}
