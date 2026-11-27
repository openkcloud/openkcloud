package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	backendlog "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	internalqueue "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/queue"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type capturingPreFilter struct {
	sawAttempt atomic.Bool
	deny       bool
}

func (p *capturingPreFilter) Name() string { return "capturingPreFilter" }
func (p *capturingPreFilter) PreFilter(ctx context.Context, pod *v1.Pod) *utils.Status {
	if utils.AttemptFrom(ctx) != nil {
		p.sawAttempt.Store(true)
	}
	if p.deny {
		return utils.NewStatus(utils.Unschedulable, "denied by fake prefilter")
	}
	return utils.NewStatus(utils.Success, "")
}

type fakeBinder struct {
	calls atomic.Int32
	fail  bool
}

func (b *fakeBinder) Name() string { return "fakeBinder" }
func (b *fakeBinder) Bind(ctx context.Context, pod *v1.Pod, nodeName string) *utils.Status {
	b.calls.Add(1)
	if b.fail {
		return utils.NewStatus(utils.Error, "fake bind fail")
	}
	return utils.NewStatus(utils.Success, "")
}

type denyPermit struct{}

func (d *denyPermit) Name() string { return "denyPermit" }
func (d *denyPermit) Permit(ctx context.Context, pod *v1.Pod, nodeName string) (framework.PermitStatus, time.Duration) {
	return framework.PermitDeny, 0
}

func phaseTestSetup(t *testing.T) (*Scheduler, context.Context, *utils.Cache) {
	t.Helper()
	t.Setenv("SCHED_CATALOG_MODE", "off")
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", "")
	backendlog.Init(backendlog.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := utils.NewCache(ctx)
	n := newSchedulerFlowNode("n", "", "")
	n.Status.Allocatable[v1.ResourceCPU] = resource.MustParse("4")
	n.Status.Allocatable[v1.ResourceMemory] = resource.MustParse("8Gi")
	if err := cache.AddNode(n, nil); err != nil {
		t.Fatal(err)
	}
	return &Scheduler{Cache: cache, hamiClient: plugin.NewHAMiExtenderClient()}, ctx, cache
}

func cpuPod(uid string) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: types.UID(uid)},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m")},
			},
		}}},
	}
}

func queued(pod *v1.Pod) *internalqueue.QueuedPodInfo {
	return &internalqueue.QueuedPodInfo{PodInfo: &utils.PodInfo{Pod: pod}}
}

func readEvents(t *testing.T, path string) []decisionrecorder.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []decisionrecorder.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev decisionrecorder.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func TestPhaseTimingMonotonicPreservedForSub(t *testing.T) {
	a := time.Now()
	time.Sleep(2 * time.Millisecond)
	b := time.Now()
	if !strings.Contains(a.String(), "m=") {
		t.Fatalf("expected monotonic in Now(): %s", a)
	}
	d := b.Sub(a)
	if d <= 0 {
		t.Fatalf("Sub lost monotonic duration: %v", d)
	}
	// UTC wall string must not be used for Sub in Payload path.
	wall := a.UTC()
	if strings.Contains(fmt.Sprintf("%v", msBetween(a, b)), "measurement_error") {
		t.Fatal("fresh Now() pair should not be measurement_error")
	}
	_ = wall
}

func TestPhaseTimingPayloadMarksVerificationAndBoundaries(t *testing.T) {
	now := time.Now()
	pt := &PhaseTiming{
		AttemptStartedAt: now,
		FilterStart:      now.Add(1 * time.Millisecond),
		FilterEnd:        now.Add(3 * time.Millisecond),
		ScoreStart:       now.Add(3 * time.Millisecond),
		ScoreEnd:         now.Add(5 * time.Millisecond),
		SelectStart:      now.Add(5 * time.Millisecond),
		SelectEnd:        now.Add(6 * time.Millisecond),
		Outcome:          "select_ok",
		EventRole:        "interim_select",
		SuggestedHost:    "n1",
	}
	p := pt.Payload()
	if p["metric_class"] != "verification_not_official_kpi7" {
		t.Fatalf("metric_class=%v", p["metric_class"])
	}
	if p["prescore_ms"] != nil {
		t.Fatalf("prescore_ms should be nil: %v", p["prescore_ms"])
	}
	b, ok := p["boundaries"].(map[string]any)
	if !ok || b["filter_ms"] == nil || b["bind_ms"] == nil {
		t.Fatalf("boundaries missing: %+v", p["boundaries"])
	}
}

func TestPhaseTimingClockRegressionIsMeasurementError(t *testing.T) {
	a := time.Now()
	b := a.Add(-5 * time.Millisecond)
	v := msBetween(a, b)
	m, ok := v.(map[string]any)
	if !ok || m["status"] != "measurement_error" {
		t.Fatalf("want measurement_error, got %#v", v)
	}
}

func TestSchedulingCyclePreFilterFailureHasTerminalDuration(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	rec, err := decisionrecorder.New(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)
	s.DecisionRecorder = rec

	pre := &capturingPreFilter{deny: true}
	fwk := framework.NewFramework(
		[]framework.PreFilterPlugin{pre},
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit()},
		nil, nil, nil,
	)
	pod := cpuPod("uid-prefilter-fail")
	result, _, status := s.schedulingCycle(ctx, fwk, queued(pod), time.Now())
	if status == nil || status.IsSuccess() {
		t.Fatal("expected prefilter failure")
	}
	if !pre.sawAttempt.Load() {
		t.Fatal("PreFilter did not observe AttemptState")
	}
	if result.Timing == nil || result.Timing.EventRole != "terminal" {
		t.Fatalf("terminal role missing: %+v", result.Timing)
	}
	if result.Timing.AttemptEndedAt.IsZero() {
		t.Fatal("AttemptEndedAt missing on fail_prefilter")
	}
	rec.Close()
	found := false
	for _, ev := range readEvents(t, path) {
		if ev.Kind != decisionrecorder.KindPhaseTiming {
			continue
		}
		if ev.Payload["outcome"] != "fail_prefilter" {
			continue
		}
		found = true
		if ev.Payload["attempt_to_terminal_ms"] == nil {
			t.Fatalf("fail_prefilter missing attempt_to_terminal_ms: %+v", ev.Payload)
		}
		if ev.Payload["attempt_to_bind_result_ms"] != nil {
			t.Fatalf("must not invent bind end on prefilter fail: %+v", ev.Payload)
		}
		if ev.AttemptID != result.AttemptID {
			t.Fatalf("attempt id mismatch")
		}
	}
	if !found {
		t.Fatal("fail_prefilter phase_timing missing")
	}
}

func TestSchedulingCycleAndBindingCycleJoinAttemptID(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	rec, err := decisionrecorder.New(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)
	s.DecisionRecorder = rec

	pre := &capturingPreFilter{}
	binder := &fakeBinder{}
	fwk := framework.NewFramework(
		[]framework.PreFilterPlugin{pre},
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit()},
		[]framework.ScorePlugin{plugin.NewCatalogAwareScore(s.Cache)},
		nil,
		binder,
	)
	pod := cpuPod("uid-cycle-bind")
	start := time.Now()
	result, assumed, status := s.schedulingCycle(ctx, fwk, queued(pod), start)
	if status != nil && !status.IsSuccess() {
		t.Fatalf("schedulingCycle failed: %v", status)
	}
	if !pre.sawAttempt.Load() {
		t.Fatal("PreFilter missing AttemptState")
	}
	if result.Timing == nil || result.Timing.EventRole != "interim_select" {
		t.Fatalf("interim select missing: %+v", result.Timing)
	}
	bindStatus := s.bindingCycle(ctx, fwk, result, assumed, start)
	if bindStatus != nil && !bindStatus.IsSuccess() {
		t.Fatalf("bindingCycle failed: %v", bindStatus)
	}
	if binder.calls.Load() != 1 {
		t.Fatalf("binder calls=%d", binder.calls.Load())
	}
	rec.Close()

	var interim, terminal map[string]any
	for _, ev := range readEvents(t, path) {
		if ev.AttemptID != result.AttemptID {
			t.Fatalf("mixed attempt ids")
		}
		if ev.Kind != decisionrecorder.KindPhaseTiming {
			continue
		}
		switch ev.Payload["event_role"] {
		case "interim_select":
			interim = ev.Payload
		case "terminal":
			terminal = ev.Payload
		}
	}
	if interim == nil || terminal == nil {
		t.Fatalf("need interim+terminal, interim=%v terminal=%v", interim, terminal)
	}
	if terminal["outcome"] != "success" || terminal["attempt_to_terminal_ms"] == nil || terminal["bind_ms"] == nil {
		t.Fatalf("terminal incomplete: %+v", terminal)
	}
	if terminal["boundaries"] == nil {
		t.Fatal("boundaries missing on terminal event")
	}
}

func TestBindingCyclePermitFailureIsTerminalWithoutFakeBindEndInvention(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	rec, err := decisionrecorder.New(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)
	s.DecisionRecorder = rec

	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit()},
		[]framework.ScorePlugin{plugin.NewCatalogAwareScore(s.Cache)},
		[]framework.PermitPlugin{&denyPermit{}},
		&fakeBinder{},
	)
	pod := cpuPod("uid-permit")
	start := time.Now()
	result, assumed, status := s.schedulingCycle(ctx, fwk, queued(pod), start)
	if status != nil && !status.IsSuccess() {
		t.Fatalf("schedule: %v", status)
	}
	st := s.bindingCycle(ctx, fwk, result, assumed, start)
	if st == nil || st.IsSuccess() {
		t.Fatal("expected permit deny")
	}
	rec.Close()
	for _, ev := range readEvents(t, path) {
		if ev.Kind != decisionrecorder.KindPhaseTiming || ev.Payload["event_role"] != "terminal" {
			continue
		}
		if ev.Payload["outcome"] != "fail_bind" || ev.Payload["fail_stage"] != "permit" {
			t.Fatalf("unexpected terminal: %+v", ev.Payload)
		}
		if ev.Payload["attempt_to_terminal_ms"] == nil || ev.Payload["bind_ms"] == nil {
			t.Fatalf("permit fail needs bind_ms (includes permit) and terminal: %+v", ev.Payload)
		}
		return
	}
	t.Fatal("terminal phase_timing missing")
}

func TestBeginAttemptConfigFailureStillRecordsTerminal(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	rec, err := decisionrecorder.New(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)
	s.DecisionRecorder = rec
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))

	fwk := framework.NewFramework(nil, []framework.FilterPlugin{plugin.NewNodeResourcesFit()}, nil, nil, nil)
	pod := cpuPod("uid-config")
	result, _, status := s.schedulingCycle(ctx, fwk, queued(pod), time.Now())
	if status == nil || status.IsSuccess() {
		t.Fatal("expected config failure")
	}
	if result.AttemptID == "" || result.Timing == nil || result.Timing.AttemptEndedAt.IsZero() {
		t.Fatalf("config fail must keep attempt id + end: %+v", result)
	}
	rec.Close()
	for _, ev := range readEvents(t, path) {
		if ev.Kind == decisionrecorder.KindPhaseTiming && ev.Payload["outcome"] == "fail_config" {
			if ev.Payload["attempt_to_terminal_ms"] == nil {
				t.Fatalf("fail_config missing terminal ms: %+v", ev.Payload)
			}
			return
		}
	}
	t.Fatal("fail_config phase_timing missing")
}

func TestRetryGetsNewAttemptIDSamePodUID(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	pre := &capturingPreFilter{deny: true}
	fwk := framework.NewFramework([]framework.PreFilterPlugin{pre}, nil, nil, nil, nil)
	pod := cpuPod("uid-retry")
	r1, _, _ := s.schedulingCycle(ctx, fwk, queued(pod), time.Now())
	r2, _, _ := s.schedulingCycle(ctx, fwk, queued(pod), time.Now())
	if r1.AttemptID == "" || r1.AttemptID == r2.AttemptID {
		t.Fatalf("retries must get distinct attempt ids: %q %q", r1.AttemptID, r2.AttemptID)
	}
}

func TestBeginAttemptBeforePreFilterSharesAttemptID(t *testing.T) {
	s, ctx, _ := phaseTestSetup(t)
	pod := cpuPod("uid-begin")
	st, meta, timing, err := s.beginAttempt(pod)
	if err != nil || st == nil || timing == nil || meta.AttemptID == "" {
		t.Fatalf("beginAttempt: err=%v", err)
	}
	if !strings.Contains(timing.AttemptStartedAt.String(), "m=") {
		t.Fatalf("attempt start lost monotonic: %s", timing.AttemptStartedAt)
	}
	ctx = utils.WithAttempt(ctx, st)
	fwk := framework.NewFramework(nil, []framework.FilterPlugin{plugin.NewNodeResourcesFit()}, nil, nil, nil)
	result, err := s.schedulePod(ctx, fwk, pod)
	if err != nil {
		t.Fatal(err)
	}
	if result.AttemptID != meta.AttemptID {
		t.Fatalf("attempt id recreated: begin=%s schedule=%s", meta.AttemptID, result.AttemptID)
	}
}
