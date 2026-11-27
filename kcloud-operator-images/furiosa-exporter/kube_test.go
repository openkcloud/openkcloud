// ============================================================
// kube_test.go: kubelet 할당 기록 → 장치별 컨테이너 표와 namespace·pod·container 라벨 시험
// 생성일: 2026-10-15
// ============================================================
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func podRes(ns, pod string, ctrs ...*podresourcesv1.ContainerResources) *podresourcesv1.PodResources {
	return &podresourcesv1.PodResources{Namespace: ns, Name: pod, Containers: ctrs}
}

func ctr(name, resource string, ids ...string) *podresourcesv1.ContainerResources {
	return &podresourcesv1.ContainerResources{Name: name,
		Devices: []*podresourcesv1.ContainerDevices{{ResourceName: resource, DeviceIds: ids}}}
}

func TestDeviceOwnersKeepsOnlyFuriosaDevices(t *testing.T) {
	resp := &podresourcesv1.ListPodResourcesResponse{PodResources: []*podresourcesv1.PodResources{
		podRes("team-a", "llm-0", ctr("server", "furiosa.ai/rngd", "U-A")),
		podRes("team-b", "train-0", ctr("main", "nvidia.com/gpu", "GPU-1")),
		podRes("team-c", "batch-0", ctr("worker", "furiosa.ai/rngd", "U-B", "U-C")),
		podRes("kube-system", "idle"), // 장치 없는 파드
	}}
	got := deviceOwners(resp)
	want := map[string]podRef{
		"U-A": {"team-a", "llm-0", "server"},
		"U-B": {"team-c", "batch-0", "worker"},
		"U-C": {"team-c", "batch-0", "worker"},
	}
	if len(got) != len(want) {
		t.Fatalf("표 = %v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %+v, want %+v", id, got[id], w)
		}
	}
}

func TestDeviceOwnersFirstContainerWins(t *testing.T) {
	resp := &podresourcesv1.ListPodResourcesResponse{PodResources: []*podresourcesv1.PodResources{
		podRes("ns", "p", ctr("first", "furiosa.ai/rngd", "U-A"), ctr("second", "furiosa.ai/rngd", "U-A")),
	}}
	if got := deviceOwners(resp)["U-A"].Container; got != "first" {
		t.Errorf("container = %q, want first", got)
	}
}

// 읽지 못한 회차에 앞 회차 표를 붙들면 끝난 파드 이름이 계속 붙는다.
func TestOwnerTableClearsWhenListFails(t *testing.T) {
	ok := &podresourcesv1.ListPodResourcesResponse{PodResources: []*podresourcesv1.PodResources{
		podRes("team-a", "llm-0", ctr("server", "furiosa.ai/rngd", "U-A")),
	}}
	var fail bool
	o := &ownerTable{list: func(context.Context) (*podresourcesv1.ListPodResourcesResponse, error) {
		if fail {
			return nil, errors.New("소켓 없음")
		}
		return ok, nil
	}}
	o.refresh(context.Background())
	if got := o.owner("U-A"); got.Pod != "llm-0" {
		t.Fatalf("첫 회차 pod = %q, want llm-0", got.Pod)
	}
	fail = true
	o.refresh(context.Background())
	if got := o.owner("U-A"); got != (podRef{}) {
		t.Errorf("실패 회차 뒤 = %+v, want 빈 값", got)
	}
	fail = false
	o.refresh(context.Background())
	if got := o.owner("U-A"); got.Pod != "llm-0" {
		t.Errorf("복구 회차 pod = %q, want llm-0", got.Pod)
	}
}

func gatherKube(t *testing.T, owner func(string) podRef, name, want string, samples ...DeviceSample) {
	t.Helper()
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return samples }, Owner: owner}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	if err := testutil.GatherAndCompare(reg, strings.NewReader(sortLabels(want)), name); err != nil {
		t.Error(err)
	}
}

func TestMetricsKubeLabelsOnAllocatedDevice(t *testing.T) {
	a := sample()
	a.AliveValid, a.Alive = true, true
	a.Cores = []CoreReading{{Core: 0, UsagePercent: 85}}
	b := sample()
	b.Name, b.UUID, b.PCI = "npu1", "U-B", "0000:28:00.0"
	b.AliveValid, b.Alive = true, true
	owner := func(uuid string) podRef {
		if uuid == "U-A" {
			return podRef{"team-a", "llm-0", "server"}
		}
		return podRef{}
	}
	lblB := strings.NewReplacer(`device="npu0"`, `device="npu1"`, `pci_bus_id="0000:27:00.0"`, `pci_bus_id="0000:28:00.0"`, `uuid="U-A"`, `uuid="U-B"`).Replace(lbl)
	gatherKube(t, owner, "furiosa_npu_alive", `
# HELP furiosa_npu_alive 장치 liveness(0/1)
# TYPE furiosa_npu_alive gauge
furiosa_npu_alive{`+lbl+`,core="0-7",namespace="team-a",pod="llm-0",container="server"} 1
furiosa_npu_alive{`+lblB+`,core="0-7",namespace="",pod="",container=""} 1
`, a, b)
	gatherKube(t, owner, "furiosa_npu_core_utilization", `
# HELP furiosa_npu_core_utilization PE 코어 사용률(%)
# TYPE furiosa_npu_core_utilization gauge
furiosa_npu_core_utilization{`+lbl+`,core="0",namespace="team-a",pod="llm-0",container="server"} 85
`, a)
}

// Owner 가 없으면(플래그 꺼짐) 세 라벨이 없어야 한다 — 공식 바이너리 기본값과 같은 라벨 구성.
func TestMetricsNoKubeLabelsWhenDisabled(t *testing.T) {
	s := sample()
	s.AliveValid, s.Alive = true, true
	gather(t, s, "furiosa_npu_alive", `
# HELP furiosa_npu_alive 장치 liveness(0/1)
# TYPE furiosa_npu_alive gauge
furiosa_npu_alive{`+lbl+`,core="0-7"} 1
`)
}

type fakeKubelet struct {
	podresourcesv1.UnimplementedPodResourcesListerServer
	resp *podresourcesv1.ListPodResourcesResponse
}

func (f *fakeKubelet) List(context.Context, *podresourcesv1.ListPodResourcesRequest) (*podresourcesv1.ListPodResourcesResponse, error) {
	return f.resp, nil
}

// 실제 kubelet 처럼 unix 소켓 gRPC 로 List 를 불러, 대상 주소 형식까지 확인한다.
func TestListPodResourcesOverUnixSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "kubelet.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	podresourcesv1.RegisterPodResourcesListerServer(srv, &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{
		PodResources: []*podresourcesv1.PodResources{podRes("team-a", "llm-0", ctr("server", "furiosa.ai/rngd", "U-A"))},
	}})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	resp, err := listPodResources(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	if got := deviceOwners(resp)["U-A"]; got != (podRef{"team-a", "llm-0", "server"}) {
		t.Errorf("U-A = %+v", got)
	}
	if _, err := listPodResources(context.Background(), filepath.Join(t.TempDir(), "none.sock")); err == nil {
		t.Error("없는 소켓에서 오류가 나지 않았다")
	}
}

// device plugin 이 UUID 가 아닌 장치 ID 를 쓰면 List 는 성공하고 라벨만 빈다. 그 상태를 한 번 알린다.
func TestOwnerTableWarnsOnceWhenNoDeviceIDMatches(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	resp := &podresourcesv1.ListPodResourcesResponse{PodResources: []*podresourcesv1.PodResources{
		podRes("team-a", "llm-0", ctr("server", "furiosa.ai/rngd", "npu0_cores_0-3")),
	}}
	o := &ownerTable{list: func(context.Context) (*podresourcesv1.ListPodResourcesResponse, error) { return resp, nil }}
	o.refresh(context.Background())
	samples := []DeviceSample{sample()} // UUID "U-A"
	o.warnUnmatched(samples)
	o.warnUnmatched(samples)
	if got := strings.Count(buf.String(), "하나도 맞지 않아"); got != 1 {
		t.Fatalf("경고 %d번, want 1번\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "npu0_cores_0-3") {
		t.Errorf("경고에 장치 ID 예시가 없다: %s", buf.String())
	}

	buf.Reset()
	resp.PodResources[0].Containers[0].Devices[0].DeviceIds = []string{"U-A"}
	m := &ownerTable{list: o.list}
	m.refresh(context.Background())
	m.warnUnmatched(samples)
	if buf.Len() != 0 {
		t.Errorf("UUID 가 맞는데 경고가 나왔다: %s", buf.String())
	}
}

// 12종 지표 전부가 같은 세 라벨을 단다(실측과 같은 범위를 시험으로 고정한다).
func TestMetricsKubeLabelsOnEveryFamily(t *testing.T) {
	s := sample()
	s.Cores = []CoreReading{{Core: 0, UsagePercent: 10}}
	s.CoreClocks = []CoreClock{{Core: 0, MHz: 2000}}
	s.Cycles = []CoreCycle{{Core: 0, Total: 5, TaskExec: 2}}
	s.Governor, s.ThrottleValid, s.ThrottleReason = "performance", true, 0
	s.DramTotalBytes, s.DramInUseBytes = 100, 10
	s.PowerWatts, s.TempSocPeak, s.TempAmbient = 39, 40, 30
	s.AliveValid, s.Alive = true, true
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} },
		Throttle: func(string) (map[string]float64, bool) { return map[string]float64{}, true },
		Owner:    func(string) podRef { return podRef{"team-a", "llm-0", "server"} }}
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(mfs) != 12 {
		t.Fatalf("지표 종류 %d개, want 12", len(mfs))
	}
	want := map[string]string{"namespace": "team-a", "pod": "llm-0", "container": "server"}
	for _, mf := range mfs {
		for _, mt := range mf.GetMetric() {
			got := map[string]string{}
			for _, lp := range mt.GetLabel() {
				got[lp.GetName()] = lp.GetValue()
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s: %s=%q, want %q", mf.GetName(), k, got[k], v)
				}
			}
		}
	}
}

// 한 스크레이프 안에서 장치마다 소유 조회는 한 번이다 — 시계열마다 조회하면 그 사이 표가 바뀌어
// 같은 장치의 시계열이 서로 다른 파드를 가리킬 수 있다.
func TestMetricsLooksUpOwnerOncePerDevice(t *testing.T) {
	s := sample()
	s.Cores = []CoreReading{{Core: 0, UsagePercent: 10}, {Core: 1, UsagePercent: 20}}
	s.AliveValid, s.Alive = true, true
	s.PowerWatts = 39
	calls := 0
	m := &Metrics{Node: "rngd-1", Samples: func() []DeviceSample { return []DeviceSample{s} },
		Owner: func(string) podRef { calls++; return podRef{"ns", fmt.Sprintf("pod-%d", calls), "c"} }}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("Owner 호출 %d번, want 장치당 1번", calls)
	}
	for _, mf := range mfs {
		for _, mt := range mf.GetMetric() {
			for _, lp := range mt.GetLabel() {
				if lp.GetName() == "pod" && lp.GetValue() != "pod-1" {
					t.Errorf("%s: pod=%q, want 한 스크레이프 안에서 pod-1 하나", mf.GetName(), lp.GetValue())
				}
			}
		}
	}
}
