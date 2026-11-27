// ============================================================
// acpp_verifier.go: ACPP 하드웨어 검증 구현 — Node allocatable + 1-파티션 테스트 Pod (spec 전제2/3)
// 상세: VerifyAllocatable 은 Node.status.allocatable 비교로 판정하고, VerifyAllocation 은
// 1-파티션 probe Pod 로 판정한다 — 표에 등록된 벤더는 명령 종료코드로, 그 외는 Running 전이로 본다.
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/internal/naming"
	"kcloud-operator/internal/partition"
	"kcloud-operator/internal/verification"
	"kcloud-operator/internal/verifyprobe"
)

// verifyPollInterval/verifyTimeout 은 probe Pod 대기 폴링 파라미터(MVP-1 고정값).
// verifyProbeTimeout 은 벤더 명령 probe 전용 상한 — rbln-stat 처럼 호스트 네임스페이스를 거치는
// 명령은 pause 스케줄 확인보다 오래 걸린다.
const (
	verifyPollInterval = 2 * time.Second
	verifyTimeout      = 180 * time.Second
	verifyProbeTimeout = 240 * time.Second
)

// defaultProbeImage 는 ACPP_PROBE_IMAGE 미설정 시 fallback — air-gap(Harbor mirror) 클러스터는
// 이 이미지가 미러링되어 있지 않으면 ImagePullBackOff 로 verify 가 실패한다.
const defaultProbeImage = "registry.k8s.io/pause:3.9"

// probeLogLines/probeLogLimitBytes 는 판정 근거로 남기는 probe 파드 로그 상한이다 —
// 줄 수만 제한하면 한 줄이 수 MB 인 로그가 근거 객체를 부풀린다.
const (
	probeLogLines      int64 = 20
	probeLogLimitBytes int64 = 64 << 10
)

// probeContainerName 은 probe 컨테이너 이름이다(상태를 이름으로 찾는다 — 인덱스로 찾으면
// 나중에 컨테이너가 하나 더 붙는 순간 엉뚱한 상태를 읽는다).
const probeContainerName = "verify"

// vendorProbe 는 벤더 하나의 기능 probe 한 줄이다. Image 는 ACPP_PROBE_IMAGE_<VENDOR> 로 주입한다 —
// 벤더 CLI 가 들어 있는 이미지는 클러스터마다 미러 경로가 달라 코드에 못 박을 수 없다.
type vendorProbe struct {
	Image      string
	Command    string
	Args       []string
	Privileged bool
	HostPID    bool
	// HostPathBinary 는 이미지에 CLI 가 없고 호스트 바이너리를 빌려 써야 할 때의 경로다
	// (예: /usr/bin/furiosa-smi). 설정되면 같은 경로에 읽기 전용으로 마운트한다.
	HostPathBinary string
}

// vendorProbeTable 은 환경변수로 이미지가 주입된 벤더만 담은 실행표를 만든다. 명령 자체는
// internal/verifyprobe 가 정하고, 이미지·호스트 바이너리는 배포 환경(환경변수)이 정한다.
// Image 가 주입되지 않은 벤더는 미등록으로 취급해 기존 pause/Running 판정을 유지한다 —
// 이미지를 못 받았다고 검증이 실패로 뒤집히면 배포 순서만으로 근거 등급이 떨어진다.
func vendorProbeTable() map[string]vendorProbe {
	vendors := verifyprobe.Vendors()
	out := make(map[string]vendorProbe, len(vendors))
	for _, vendor := range vendors {
		img := os.Getenv("ACPP_PROBE_IMAGE_" + strings.ToUpper(vendor))
		if img == "" {
			continue
		}
		p, _ := verifyprobe.Lookup(vendor)
		vp := vendorProbe{Image: img, Command: p.Command, Args: p.Args, Privileged: p.Privileged, HostPID: p.HostPID}
		if hb := os.Getenv("ACPP_PROBE_HOSTBIN_" + strings.ToUpper(vendor)); hb != "" {
			vp.HostPathBinary = hb
		}
		out[vendor] = vp
	}
	return out
}

// PodLogGetter 는 probe 파드의 마지막 로그를 읽는 seam 이다. controller-runtime client 는 log
// 서브리소스를 못 읽어 clientset 이 필요하고, 그 의존을 컨트롤러에 심지 않으려고 cmd/main.go 에서 주입한다.
type PodLogGetter interface {
	TailLogs(ctx context.Context, namespace, name string, lines int64) (string, error)
}

// NewPodLogGetter 는 manager 의 rest.Config 로 clientset 기반 로그 조회기를 만든다.
func NewPodLogGetter(cfg *rest.Config) (PodLogGetter, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("probe log getter: %w", err)
	}
	return &clientsetLogGetter{cs: cs}, nil
}

type clientsetLogGetter struct{ cs kubernetes.Interface }

func (g *clientsetLogGetter) TailLogs(ctx context.Context, namespace, name string, lines int64) (string, error) {
	raw, err := g.cs.CoreV1().Pods(namespace).
		GetLogs(name, &corev1.PodLogOptions{
			TailLines: &lines, LimitBytes: ptr.To(probeLogLimitBytes),
		}).DoRaw(ctx)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// liveVerifier 는 실 클러스터(Node/Pod) 를 대상으로 partition.Verifier 를 구현한다.
type liveVerifier struct {
	client     client.Client
	probeImage string
	probes     map[string]vendorProbe
	logs       PodLogGetter
	// timeout 이 0 이 아니면 대기 상한을 덮어쓴다(테스트에서 타임아웃 경로를 짧게 돌리는 seam).
	timeout time.Duration
}

// NewLiveVerifier 는 실 클러스터 검증기를 만든다. pause probe 이미지는 ACPP_PROBE_IMAGE,
// 벤더 기능 probe 이미지는 ACPP_PROBE_IMAGE_<VENDOR> 환경변수로 주입한다(air-gap 미러 경로).
// logs 가 nil 이면 판정은 그대로 하고 로그만 비운다.
func NewLiveVerifier(c client.Client, logs PodLogGetter) partition.Verifier {
	img := os.Getenv("ACPP_PROBE_IMAGE")
	if img == "" {
		img = defaultProbeImage
	}
	return &liveVerifier{client: c, probeImage: img, probes: vendorProbeTable(), logs: logs}
}

// allocatableMet 는 node.status.allocatable[resourceName] 이 want 이상인지 판정하는 순수 함수다
// (VerifyAllocatable 의 테스트 가능한 핵심 — 라이브 client 없이 unit 테스트).
func allocatableMet(node *corev1.Node, resourceName string, want int32) bool {
	q, ok := node.Status.Allocatable[corev1.ResourceName(resourceName)]
	if !ok {
		return false
	}
	return q.Value() >= int64(want)
}

// VerifyAllocatable 는 대상 Node 의 allocatable 이 expected 를 모두 충족하는지 확인한다.
// VerifyAllocatable 은 기대 allocatable 로 수렴할 때까지 폴링한다(verifyTimeout). device-plugin 재시작 후
// MIG 조각 재광고에는 수 초~수십 초가 걸리므로 단발 확인은 조기 false 를 내어 불필요한 rollback 을 유발한다.
func (v *liveVerifier) VerifyAllocatable(t partition.Target, expected map[string]int32) (*partition.VerifyResult, error) {
	ctx, cancel := context.WithTimeout(t.Ctx, verifyTimeout)
	defer cancel()
	var lastSnapshot map[string]int32
	for {
		var node corev1.Node
		if err := v.client.Get(ctx, types.NamespacedName{Name: t.NodeName}, &node); err != nil {
			return nil, fmt.Errorf("acpp verify: get node %q: %w", t.NodeName, err)
		}
		snapshot := make(map[string]int32, len(expected))
		converged := true
		for res, want := range expected {
			if !allocatableMet(&node, res, want) {
				converged = false
			}
			if q, ok := node.Status.Allocatable[corev1.ResourceName(res)]; ok {
				snapshot[res] = int32(q.Value())
			}
		}
		lastSnapshot = snapshot
		if converged {
			return &partition.VerifyResult{AllocatableConverged: true, Snapshot: snapshot}, nil
		}
		select {
		case <-ctx.Done():
			return &partition.VerifyResult{AllocatableConverged: false, Snapshot: lastSnapshot}, nil
		case <-time.After(verifyPollInterval):
		}
	}
}

// VerifyAllocation 은 대상 노드에 1-파티션(resourceName: 1) probe Pod 를 스케줄한다.
// 표에 등록된 벤더는 벤더 명령을 돌려 종료코드로 판정하고(Succeeded=0 통과), 그 외에는 기존대로
// Running 전이로 판정한다. 끝나면 Pod 를 지운다. 스케줄 실패/타임아웃은 실패로 취급한다.
func (v *liveVerifier) VerifyAllocation(t partition.Target, resourceName string) (*partition.VerifyResult, error) {
	probe, registered := v.probes[t.Vendor]
	pod := v.probePod(t.NodeName, resourceName, probe, registered)
	if err := v.client.Create(t.Ctx, pod); err != nil {
		return nil, fmt.Errorf("acpp verify: create test pod: %w", err)
	}
	defer func() {
		_ = v.client.Delete(context.Background(), pod)
	}()

	timeout := verifyTimeout
	if registered {
		timeout = verifyProbeTimeout
	}
	if v.timeout > 0 {
		timeout = v.timeout
	}
	ctx, cancel := context.WithTimeout(t.Ctx, timeout)
	defer cancel()
	var last corev1.Pod
	for {
		var got corev1.Pod
		if err := v.client.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &got); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("acpp verify: get test pod: %w", err)
			}
		} else {
			last = got
			if registered {
				// 기능 probe 는 Running 을 통과로 보지 않는다 — 명령이 끝나야 장치를 봤는지 알 수 있다.
				switch got.Status.Phase {
				case corev1.PodSucceeded, corev1.PodFailed:
					return v.probeOutcome(ctx, &got), nil
				}
			} else {
				switch got.Status.Phase {
				case corev1.PodRunning:
					return &partition.VerifyResult{TestPodAllocated: true}, nil
				case corev1.PodFailed:
					return &partition.VerifyResult{TestPodAllocated: false}, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return &partition.VerifyResult{
				TestPodAllocated: false,
				ProbeOutput:      timeoutOutput(timeout, &last),
			}, nil
		case <-time.After(verifyPollInterval):
		}
	}
}

// timeoutOutput 은 상한까지 끝나지 않은 probe 의 사유를 한 줄로 남긴다. 파드는 곧 지워지므로
// 여기서 적지 않으면 ImagePullBackOff 였는지 스케줄이 안 됐는지 근거만 보고는 알 수 없다.
func timeoutOutput(timeout time.Duration, pod *corev1.Pod) string {
	if pod.Name == "" {
		return fmt.Sprintf("timeout after %s: probe pod not observed", timeout)
	}
	out := fmt.Sprintf("timeout after %s: phase=%s", timeout, pod.Status.Phase)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != probeContainerName || cs.State.Waiting == nil {
			continue
		}
		out += " waiting=" + cs.State.Waiting.Reason
		if cs.State.Waiting.Message != "" {
			out += " (" + cs.State.Waiting.Message + ")"
		}
	}
	return out
}

// probePod 는 probe Pod 를 조립한다. registered 면 벤더 명령 + 표의 권한을, 아니면 pause 이미지를 쓴다.
func (v *liveVerifier) probePod(nodeName, resourceName string, probe vendorProbe, registered bool) *corev1.Pod {
	image := v.probeImage
	if registered {
		image = probe.Image
	}
	c := corev1.Container{
		Name:  probeContainerName,
		Image: image,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName(resourceName): resource.MustParse("1"),
			},
		},
	}
	spec := corev1.PodSpec{
		NodeName:      nodeName,
		RestartPolicy: corev1.RestartPolicyNever,
	}
	if registered {
		c.Command = []string{probe.Command}
		c.Args = probe.Args
		if probe.Privileged {
			c.SecurityContext = &corev1.SecurityContext{Privileged: ptr.To(true)}
		}
		spec.HostPID = probe.HostPID
		if probe.HostPathBinary != "" {
			// 이미지에 CLI 가 없는 벤더는 호스트 바이너리를 같은 경로에 읽기 전용으로 빌려 쓴다.
			spec.Volumes = append(spec.Volumes, corev1.Volume{
				Name: "vendor-cli",
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
					Path: probe.HostPathBinary, Type: ptr.To(corev1.HostPathFile),
				}},
			})
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name: "vendor-cli", MountPath: probe.HostPathBinary, ReadOnly: true,
			})
		}
	}
	spec.Containers = []corev1.Container{c}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "acpp-verify-",
			Namespace:    naming.OperatorNamespace(),
			Labels:       map[string]string{"npu.ai/acpp-verify-pod": "true"},
		},
		Spec: spec,
	}
}

// probeOutcome 은 끝난 probe 파드의 종료코드와 마지막 로그를 판정 결과로 옮긴다.
// 파드는 곧 지워지므로 여기서 읽지 않으면 실패 사유가 남지 않는다.
//
// 컨테이너가 아예 뜨지 못한 실패(kubelet admission 거절 — phase=Failed, reason=OutOf<resource>)
// 에는 종료코드가 없다. 그 경우를 0 으로 적으면 근거에 "exit=0 인데 실패" 라는 거짓말이 남는다 —
// 하필 이 probe 가 잡으려는 실패가 그것이다. 종료코드는 nil 로 두고 파드 상태 사유를 대신 싣는다.
func (v *liveVerifier) probeOutcome(ctx context.Context, pod *corev1.Pod) *partition.VerifyResult {
	exit := probeExitCode(pod)
	res := &partition.VerifyResult{
		TestPodAllocated: pod.Status.Phase == corev1.PodSucceeded && (exit == nil || *exit == 0),
		ProbeExitCode:    exit,
	}
	if exit == nil {
		res.ProbeOutput = strings.TrimSpace(pod.Status.Reason + " " + pod.Status.Message)
		return res
	}
	res.ProbeOutput = v.tailLogs(ctx, pod.Namespace, pod.Name)
	return res
}

// probeExitCode 는 probe 컨테이너의 종료코드를 찾는다. 컨테이너가 끝난 적 없으면 nil 이다.
func probeExitCode(pod *corev1.Pod) *int32 {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != probeContainerName {
			continue
		}
		if term := cs.State.Terminated; term != nil {
			return &term.ExitCode
		}
	}
	return nil
}

// tailLogs 는 probe 파드의 마지막 줄들을 읽는다. 로그를 못 읽는 것은 판정을 뒤집지 않는다 —
// 사유만 문자열로 남긴다.
func (v *liveVerifier) tailLogs(ctx context.Context, namespace, name string) string {
	if v.logs == nil {
		return ""
	}
	out, err := v.logs.TailLogs(ctx, namespace, name, probeLogLines)
	if err != nil {
		return fmt.Sprintf("logs unavailable: %v", err)
	}
	return strings.TrimSpace(out)
}

// allocationProber 는 기존 partition.Verifier 의 테스트 Pod 프로브를 verification 계약으로
// 올린다 — 계약만 바꾸고 구현은 그대로 둔다. 이미 동작하는 프로브 로직을 다시 쓸 이유가 없다.
type allocationProber struct{ v partition.Verifier }

// NewAllocationProber 는 partition.Verifier 를 verification.AllocationProber 로 감싼다.
func NewAllocationProber(v partition.Verifier) verification.AllocationProber {
	return &allocationProber{v: v}
}

func (a *allocationProber) Probe(ctx context.Context, nodeName, resourceName, vendor string) (verification.ProbeResult, error) {
	if a.v == nil {
		return verification.ProbeResult{Output: "no verifier configured"}, nil
	}
	res, err := a.v.VerifyAllocation(
		partition.Target{Ctx: ctx, NodeName: nodeName, Vendor: vendor}, resourceName)
	if err != nil {
		return verification.ProbeResult{}, err
	}
	if res == nil {
		return verification.ProbeResult{Output: "verifier returned no result"}, nil
	}
	return verification.ProbeResult{
		Allocated: res.TestPodAllocated,
		ExitCode:  res.ProbeExitCode,
		Output:    res.ProbeOutput,
	}, nil
}
