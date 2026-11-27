// ============================================================
// acpp_verifier_test.go: allocatableMet 순수 함수 + 벤더 probe 파드 + allocationProber 어댑터 unit 테스트
// 생성일: 2026-07-23 | 수정일: 2026-10-11
// ============================================================
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"kcloud-operator/internal/partition"
)

// TestNewLiveVerifier_ProbeImage 는 ACPP_PROBE_IMAGE 미설정 시 fallback(registry.k8s.io/pause:3.9),
// 설정 시 override 값을 사용함을 검증한다(air-gap 미러 이미지 지정 seam).
func TestNewLiveVerifier_ProbeImage(t *testing.T) {
	t.Run("falls back when unset", func(t *testing.T) {
		v := NewLiveVerifier(nil, nil).(*liveVerifier)
		if v.probeImage != defaultProbeImage {
			t.Errorf("probeImage = %q, want fallback %q", v.probeImage, defaultProbeImage)
		}
	})
	t.Run("uses override when set", func(t *testing.T) {
		t.Setenv("ACPP_PROBE_IMAGE", "registry.example.com:5000/kcloud/pause:3.9")
		v := NewLiveVerifier(nil, nil).(*liveVerifier)
		if v.probeImage != "registry.example.com:5000/kcloud/pause:3.9" {
			t.Errorf("probeImage = %q, want override", v.probeImage)
		}
	})
}

func TestAllocatableMet(t *testing.T) {
	node := &corev1.Node{Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
		"furiosa.ai/rngd": resource.MustParse("4"),
	}}}

	cases := []struct {
		name     string
		resource string
		want     int32
		expect   bool
	}{
		{"met exactly", "furiosa.ai/rngd", 4, true},
		{"met with margin", "furiosa.ai/rngd", 2, true},
		{"not met", "furiosa.ai/rngd", 8, false},
		{"resource absent", "furiosa.ai/warboy", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allocatableMet(node, c.resource, c.want); got != c.expect {
				t.Errorf("allocatableMet(%q, %d) = %v, want %v", c.resource, c.want, got, c.expect)
			}
		})
	}
}

// stubVerifier 는 partition.Verifier 를 고정 응답으로 구현한다(allocationProber 어댑터 전용 테스트 seam).
type stubVerifier struct {
	res       *partition.VerifyResult
	err       error
	gotVendor string
}

func (s *stubVerifier) VerifyAllocatable(partition.Target, map[string]int32) (*partition.VerifyResult, error) {
	return nil, nil
}
func (s *stubVerifier) VerifyAllocation(t partition.Target, _ string) (*partition.VerifyResult, error) {
	s.gotVendor = t.Vendor
	return s.res, s.err
}

// TestAllocationProber_Probe 는 NewAllocationProber 가 기존 partition.Verifier.VerifyAllocation 을
// verification.AllocationProber 계약(ProbeResult, err)으로 정확히 옮기는지 확인한다 —
// nil verifier(미설정), 프로브 자체 에러, nil 결과, 정상 결과, 종료코드 전달 다섯 갈래.
func TestAllocationProber_Probe(t *testing.T) {
	t.Run("nil verifier reports not-allocated without error", func(t *testing.T) {
		p := NewAllocationProber(nil)
		res, err := p.Probe(context.Background(), "node-1", "nvidia.com/mig-1g.6gb", "nvidia")
		if res.Allocated || res.Output == "" || err != nil {
			t.Fatalf("Probe() = (%+v, %v), want (not-allocated with reason, nil)", res, err)
		}
	})
	t.Run("verifier error propagates", func(t *testing.T) {
		wantErr := errors.New("probe pod scheduling failed")
		p := NewAllocationProber(&stubVerifier{err: wantErr})
		res, err := p.Probe(context.Background(), "node-1", "nvidia.com/mig-1g.6gb", "nvidia")
		if res.Allocated || !errors.Is(err, wantErr) {
			t.Fatalf("Probe() = (%+v, %v), want (not-allocated, %v)", res, err, wantErr)
		}
	})
	t.Run("nil result is not-allocated without error", func(t *testing.T) {
		p := NewAllocationProber(&stubVerifier{res: nil})
		res, err := p.Probe(context.Background(), "node-1", "nvidia.com/mig-1g.6gb", "")
		if res.Allocated || res.Output == "" || err != nil {
			t.Fatalf("Probe() = (%+v, %v), want (not-allocated with reason, nil)", res, err)
		}
	})
	t.Run("successful allocation reports true", func(t *testing.T) {
		p := NewAllocationProber(&stubVerifier{res: &partition.VerifyResult{TestPodAllocated: true}})
		res, err := p.Probe(context.Background(), "node-1", "nvidia.com/mig-1g.6gb", "")
		if !res.Allocated || err != nil {
			t.Fatalf("Probe() = (%+v, %v), want (allocated, nil)", res, err)
		}
		if res.ExitCode != nil {
			t.Errorf("미등록 벤더 probe 는 종료코드를 남기지 않는다: %d", *res.ExitCode)
		}
	})
	t.Run("unallocated result reports false", func(t *testing.T) {
		p := NewAllocationProber(&stubVerifier{res: &partition.VerifyResult{TestPodAllocated: false}})
		res, err := p.Probe(context.Background(), "node-1", "nvidia.com/mig-1g.6gb", "")
		if res.Allocated || err != nil {
			t.Fatalf("Probe() = (%+v, %v), want (not-allocated, nil)", res, err)
		}
	})
	t.Run("vendor and exit code cross the seam", func(t *testing.T) {
		exit := int32(3)
		sv := &stubVerifier{res: &partition.VerifyResult{
			TestPodAllocated: false, ProbeExitCode: &exit, ProbeOutput: "no devices found"}}
		p := NewAllocationProber(sv)
		res, err := p.Probe(context.Background(), "node-1", "rebellions.ai/ATOM", vendorRebellions)
		if err != nil {
			t.Fatalf("Probe() err = %v", err)
		}
		if sv.gotVendor != vendorRebellions {
			t.Errorf("Target.Vendor = %q, want rebellions", sv.gotVendor)
		}
		if res.ExitCode == nil || *res.ExitCode != 3 || res.Output != "no devices found" {
			t.Errorf("ProbeResult = %+v, want exit 3 + 출력", res)
		}
	})
}

// TestVerifyAllocatableStillChecksDevicePluginNode 는 기존 동작이 남아 있는지 본다.
func TestVerifyAllocatableStillChecksDevicePluginNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			"nvidia.com/gpu": *resource.NewQuantity(4, resource.DecimalSI),
		}},
	}
	c := fake.NewClientBuilder().WithScheme(sharingScheme()).WithObjects(node).Build()
	v := &liveVerifier{client: c}
	res, err := v.VerifyAllocatable(
		partition.Target{Ctx: context.Background(), NodeName: "n1"},
		map[string]int32{"nvidia.com/gpu": 4})
	if err != nil {
		t.Fatalf("VerifyAllocatable 실패: %v", err)
	}
	if !res.AllocatableConverged {
		t.Error("device-plugin 노드가 기대를 충족했는데 미수렴 판정했다")
	}
}

// stubLogGetter 는 probe 파드 로그 조회 seam 의 테스트 대역이다.
type stubLogGetter struct {
	out string
	err error
}

func (s stubLogGetter) TailLogs(context.Context, string, string, int64) (string, error) {
	return s.out, s.err
}

// probeClient 는 kubelet 역할을 대신한다 — 생성된 probe 파드에 곧바로 원하는 상태를 기록해,
// 검증 루프가 폴링에 붙잡히지 않게 한다(migjob_reboot_test 의 succeedOnCreate 와 같은 장치).
func probeClient(t *testing.T, status corev1.PodStatus, captured **corev1.Pod) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(sharingScheme()).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return cl.Create(ctx, obj, opts...)
				}
				*captured = pod.DeepCopy()
				pod.Status = status
				return cl.Create(ctx, pod, opts...)
			},
		}).Build()
}

// terminatedStatus 는 probe 컨테이너가 exit 로 끝난 파드 상태다.
func terminatedStatus(phase corev1.PodPhase, exit int32) corev1.PodStatus {
	return corev1.PodStatus{
		Phase: phase,
		ContainerStatuses: []corev1.ContainerStatus{{
			Name:  probeContainerName,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exit}},
		}},
	}
}

// TestVerifyAllocationRunsVendorProbe 는 등록 벤더(nvidia)면 pause 스케줄 확인이 아니라 실제
// 벤더 명령을 돌려 종료코드로 판정하는지 본다 — 파드가 뜨기만 하는 것은 장치가 동작한다는 증거가 아니다.
func TestVerifyAllocationRunsVendorProbe(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "registry.example.com:5000/kcloud/cuda:12.4-base")
	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, terminatedStatus(corev1.PodSucceeded, 0), &pod), stubLogGetter{out: "GPU 0: NVIDIA A100"})
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: "nvidia"}, "nvidia.com/gpu")
	if err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if pod == nil {
		t.Fatal("probe 파드가 생성되지 않았다")
	}
	c := pod.Spec.Containers[0]
	if c.Image != "registry.example.com:5000/kcloud/cuda:12.4-base" {
		t.Errorf("Image = %q, want 벤더 probe 이미지", c.Image)
	}
	if len(c.Command) != 1 || c.Command[0] != "nvidia-smi" {
		t.Errorf("Command = %v, want [nvidia-smi]", c.Command)
	}
	if len(c.Args) != 1 || c.Args[0] != "-L" {
		t.Errorf("Args = %v, want [-L]", c.Args)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want Never", pod.Spec.RestartPolicy)
	}
	if q, ok := c.Resources.Limits["nvidia.com/gpu"]; !ok || q.Value() != 1 {
		t.Errorf("limits = %v, want nvidia.com/gpu: 1", c.Resources.Limits)
	}
	if pod.Spec.HostPID || (c.SecurityContext != nil && c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged) {
		t.Error("nvidia probe 에 특권이 붙었다 — 표에 없는 권한을 주면 안 된다")
	}
	if !res.TestPodAllocated {
		t.Error("exit 0 인 probe 를 실패로 판정했다")
	}
	if res.ProbeExitCode == nil || *res.ProbeExitCode != 0 {
		t.Errorf("ProbeExitCode = %v, want 0", res.ProbeExitCode)
	}
	if res.ProbeOutput != "GPU 0: NVIDIA A100" {
		t.Errorf("ProbeOutput = %q, want 로그 마지막 줄", res.ProbeOutput)
	}
}

// TestVerifyAllocationVendorProbeFailureCarriesExitCode 는 벤더 명령이 실패로 끝났을 때
// 종료코드와 로그가 결과에 실리는지 본다(근거 Message 의 재료).
func TestVerifyAllocationVendorProbeFailureCarriesExitCode(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "registry.example.com:5000/kcloud/cuda:12.4-base")
	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, terminatedStatus(corev1.PodFailed, 3), &pod), stubLogGetter{out: "no devices found"})
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: "nvidia"}, "nvidia.com/gpu")
	if err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if res.TestPodAllocated {
		t.Error("exit 3 인 probe 를 통과로 판정했다")
	}
	if res.ProbeExitCode == nil || *res.ProbeExitCode != 3 {
		t.Fatalf("ProbeExitCode = %v, want 3", res.ProbeExitCode)
	}
	if res.ProbeOutput != "no devices found" {
		t.Errorf("ProbeOutput = %q, want probe 로그", res.ProbeOutput)
	}
}

// TestVerifyAllocationUnregisteredVendorKeepsPauseProbe 는 표에 없는 벤더(또는 이미지 미주입)면
// 기존 pause + Running 판정이 그대로인지 본다 — 이미지를 못 받은 벤더가 조용히 실패로 바뀌면 안 된다.
func TestVerifyAllocationUnregisteredVendorKeepsPauseProbe(t *testing.T) {
	// 벤더 env 가 채워져 있어도 Target.Vendor 가 비면 벤더 probe 로 전환되지 않아야 한다(레거시 보장).
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "example.invalid/probe:1")
	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, corev1.PodStatus{Phase: corev1.PodRunning}, &pod), stubLogGetter{out: "unused"})
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1"}, "furiosa.ai/rngd")
	if err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if pod == nil {
		t.Fatal("probe 파드가 생성되지 않았다")
	}
	c := pod.Spec.Containers[0]
	if c.Image != defaultProbeImage || len(c.Command) != 0 {
		t.Errorf("미등록 벤더는 pause 파드여야 한다: image=%q command=%v", c.Image, c.Command)
	}
	if !res.TestPodAllocated {
		t.Error("Running 파드를 할당 실패로 판정했다")
	}
	if res.ProbeExitCode != nil {
		t.Errorf("pause probe 는 종료코드를 남기지 않는다: %d", *res.ProbeExitCode)
	}
}

// TestVerifyAllocationRebellionsProbeIsPrivileged 는 rbln-stat 이 호스트 네임스페이스를 필요로 하는
// 표의 예외(Privileged+HostPID)가 파드에 실제로 실리는지 본다.
func TestVerifyAllocationRebellionsProbeIsPrivileged(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_REBELLIONS", "registry.example.com:5000/kcloud/rbln-probe:v1")
	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, terminatedStatus(corev1.PodSucceeded, 0), &pod), stubLogGetter{})
	if _, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: vendorRebellions}, "rebellions.ai/ATOM"); err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if pod == nil {
		t.Fatal("probe 파드가 생성되지 않았다")
	}
	if !pod.Spec.HostPID {
		t.Error("rebellions probe 에 HostPID 가 없다")
	}
	sc := pod.Spec.Containers[0].SecurityContext
	if sc == nil || sc.Privileged == nil || !*sc.Privileged {
		t.Error("rebellions probe 에 Privileged 가 없다")
	}
	if pod.Spec.Containers[0].Command[0] != "rbln-stat" {
		t.Errorf("Command = %v, want [rbln-stat]", pod.Spec.Containers[0].Command)
	}
}

// TestFuriosaProbeHostPathBinary 는 furiosa probe 가 ACPP_PROBE_HOSTBIN_FURIOSA 로 받은
// 호스트 바이너리 경로를 표에 채우고, 그 경로를 파드에 읽기 전용으로 마운트하는지 본다 —
// 공식 이미지엔 furiosa-smi CLI 가 없어 호스트 바이너리를 빌려 써야 한다.
func TestFuriosaProbeHostPathBinary(t *testing.T) {
	const hostBin = "/usr/bin/furiosa-smi"
	t.Setenv("ACPP_PROBE_IMAGE_FURIOSA", "registry.example.com:5000/kcloud/furiosaai/furiosa-smi:2026.1.0")
	t.Setenv("ACPP_PROBE_HOSTBIN_FURIOSA", hostBin)

	if got := vendorProbeTable()[vendorFuriosa].HostPathBinary; got != hostBin {
		t.Errorf("HostPathBinary = %q, want %q", got, hostBin)
	}

	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, terminatedStatus(corev1.PodSucceeded, 0), &pod), stubLogGetter{})
	if _, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: vendorFuriosa}, "furiosa.ai/rngd"); err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if pod == nil {
		t.Fatal("probe 파드가 생성되지 않았다")
	}
	var vol *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == "vendor-cli" {
			vol = &pod.Spec.Volumes[i]
		}
	}
	if vol == nil || vol.HostPath == nil || vol.HostPath.Path != hostBin {
		t.Fatalf("vendor-cli Volume = %+v, want HostPath %q", vol, hostBin)
	}
	var mount *corev1.VolumeMount
	for i := range pod.Spec.Containers[0].VolumeMounts {
		if pod.Spec.Containers[0].VolumeMounts[i].Name == "vendor-cli" {
			mount = &pod.Spec.Containers[0].VolumeMounts[i]
		}
	}
	if mount == nil || mount.MountPath != hostBin || !mount.ReadOnly {
		t.Fatalf("vendor-cli VolumeMount = %+v, want MountPath %q ReadOnly", mount, hostBin)
	}
}

// TestVerifyAllocationLogFailureKeepsVerdict 는 로그를 못 읽는 것이 판정을 뒤집지 않는지 본다.
// pods/log 권한이 빠진 클러스터에서 종료코드 3(진짜 실패)이 "로그 실패" 로 뭉개지거나, 반대로
// 로그가 없다는 이유로 판정이 흔들리면 근거가 거짓을 말하게 된다.
func TestVerifyAllocationLogFailureKeepsVerdict(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "registry.example.com:5000/kcloud/cuda:12.4-base")
	var pod *corev1.Pod
	v := NewLiveVerifier(probeClient(t, terminatedStatus(corev1.PodFailed, 3), &pod),
		stubLogGetter{err: errors.New(`pods "acpp-verify-x" is forbidden`)})
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: vendorNvidia}, "nvidia.com/gpu")
	if err != nil {
		t.Fatalf("로그 조회 실패가 probe 에러로 번졌다: %v", err)
	}
	if res.TestPodAllocated {
		t.Error("exit 3 인 probe 를 통과로 판정했다")
	}
	if res.ProbeExitCode == nil || *res.ProbeExitCode != 3 {
		t.Fatalf("ProbeExitCode = %v, want 3", res.ProbeExitCode)
	}
	if !strings.Contains(res.ProbeOutput, "logs unavailable") {
		t.Errorf("ProbeOutput = %q, want 로그 조회 실패 사유", res.ProbeOutput)
	}
}

// TestVerifyAllocationAdmissionRejectionHasNoExitCode 는 컨테이너가 뜨지도 못한 실패를 본다.
// kubelet 이 자원 부족으로 파드를 거절하면(phase=Failed, reason=OutOf<resource>) 종료코드가 없다 —
// 이걸 0 으로 적으면 근거에 "exit=0 인데 실패" 가 남는다. 하필 이 probe 가 잡으려는 실패가 그것이다.
func TestVerifyAllocationAdmissionRejectionHasNoExitCode(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "registry.example.com:5000/kcloud/cuda:12.4-base")
	var pod *corev1.Pod
	c := probeClient(t, corev1.PodStatus{
		Phase:   corev1.PodFailed,
		Reason:  "OutOfnvidia.com/gpu",
		Message: "Pod was rejected: Node didn't have enough resource: nvidia.com/gpu",
	}, &pod)
	v := NewLiveVerifier(c, stubLogGetter{out: "should not be used"})
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: vendorNvidia}, "nvidia.com/gpu")
	if err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if res.TestPodAllocated {
		t.Error("admission 거절을 통과로 판정했다")
	}
	if res.ProbeExitCode != nil {
		t.Errorf("종료코드가 없는 실패에 exit=%d 를 지어냈다", *res.ProbeExitCode)
	}
	if !strings.Contains(res.ProbeOutput, "OutOfnvidia.com/gpu") {
		t.Errorf("ProbeOutput = %q, want 거절 사유", res.ProbeOutput)
	}
	// 근거 Message 까지 확인 — 종료코드가 없으면 exit= 가 붙어서는 안 된다.
	msg, perr := NewAllocationProber(&stubVerifier{res: res}).
		Probe(context.Background(), "n1", "nvidia.com/gpu", vendorNvidia)
	if perr != nil {
		t.Fatalf("Probe err = %v", perr)
	}
	if strings.Contains(msg.Message(), "exit=") {
		t.Errorf("Message = %q, want exit= 없음", msg.Message())
	}
}

// TestVerifyAllocationTimeoutRecordsCause 는 상한까지 끝나지 않은 probe 의 사유가 남는지 본다.
// 파드는 곧 지워지므로 여기서 안 적으면 ImagePullBackOff 였다는 사실이 통째로 사라진다.
func TestVerifyAllocationTimeoutRecordsCause(t *testing.T) {
	t.Setenv("ACPP_PROBE_IMAGE_NVIDIA", "registry.example.com:5000/kcloud/cuda:12.4-base")
	var pod *corev1.Pod
	c := probeClient(t, corev1.PodStatus{
		Phase: corev1.PodPending,
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: probeContainerName,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "ImagePullBackOff", Message: "back-off pulling image",
			}},
		}},
	}, &pod)
	v := NewLiveVerifier(c, stubLogGetter{}).(*liveVerifier)
	v.timeout = 20 * time.Millisecond // 240s 를 기다리지 않고 타임아웃 경로만 본다
	res, err := v.VerifyAllocation(
		partition.Target{Ctx: context.Background(), NodeName: "n1", Vendor: vendorNvidia}, "nvidia.com/gpu")
	if err != nil {
		t.Fatalf("VerifyAllocation 실패: %v", err)
	}
	if res.TestPodAllocated {
		t.Error("끝나지 않은 probe 를 통과로 판정했다")
	}
	if !strings.Contains(res.ProbeOutput, "timeout") || !strings.Contains(res.ProbeOutput, "ImagePullBackOff") {
		t.Errorf("ProbeOutput = %q, want 타임아웃 + 대기 사유", res.ProbeOutput)
	}
}
