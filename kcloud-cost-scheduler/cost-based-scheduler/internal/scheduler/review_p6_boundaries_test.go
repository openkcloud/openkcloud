package scheduler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendlog "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	pc "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func reviewP6Setup(t *testing.T, nodes ...*v1.Node) (*Scheduler, framework.Framework, string) {
	t.Helper()
	backendlog.Init(backendlog.NewDefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := utils.NewCache(ctx)
	for _, n := range nodes {
		if err := cache.AddNode(n, nil); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	r, err := decisionrecorder.New(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	s := &Scheduler{Cache: cache, DecisionRecorder: r, hamiClient: plugin.NewHAMiExtenderClient()}
	f := framework.NewFramework(nil, []framework.FilterPlugin{plugin.NewNodeResourcesFit()}, nil, nil, nil)
	return s, f, path
}

func reviewP6Pod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "review", UID: "pod-uid"},
		Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "c"}}},
	}
}

func reviewP6Events(t *testing.T, s *Scheduler, path string) []decisionrecorder.Event {
	t.Helper()
	s.DecisionRecorder.Close()
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

func TestReviewP6R3InvalidConfigDoesNotSilentlyUseRecord(t *testing.T) {
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("SCHED_REQUIRED_CHECK_4_MODE", "enforce")
	p := reviewP6Pod()
	p.Annotations = map[string]string{pc.AnnotationTopologyRequire: "zone=wanted"}
	n := newSchedulerFlowNode("n", "", "")
	n.Labels[pc.NodeLabelTopologyZone] = "wrong"
	s, f, _ := reviewP6Setup(t, n)
	result, err := s.schedulePod(context.Background(), f, p)
	if err == nil {
		t.Fatalf("invalid config silently selected %q with record defaults instead of rejecting config", result.SuggestedHost)
	}
}

func TestReviewP6R4JSONLRetainsFinalLegacyRejection(t *testing.T) {
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", "")
	p := reviewP6Pod()
	p.Spec.Containers[0].Resources.Requests = v1.ResourceList{v1.ResourceMemory: resource.MustParse("2Gi")}
	n := newSchedulerFlowNode("n", "", "")
	n.Status.Allocatable[v1.ResourceMemory] = resource.MustParse("1Gi")
	s, f, path := reviewP6Setup(t, n)
	result, err := s.schedulePod(context.Background(), f, p)
	if err == nil || !result.PluginResultMap["n"].IsFiltered {
		t.Fatal("control: legacy host-memory filter must reject")
	}
	events := reviewP6Events(t, s, path)
	for _, ev := range events {
		if ev.Kind == decisionrecorder.KindCandidateCheckResult {
			if ev.Payload["is_filtered"] != true {
				t.Errorf("JSONL candidate says is_filtered=%v after legacy rejection", ev.Payload["is_filtered"])
			}
			raw, _ := json.Marshal(ev.Payload["rejection_reasons"])
			if !strings.Contains(string(raw), "NodeResourcesFit") {
				t.Errorf("legacy rejection absent: %s", raw)
			}
			return
		}
	}
	t.Fatal("candidate event missing")
}

func TestReviewP6R5AttemptAndBindCanBeJoined(t *testing.T) {
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", "")
	p := reviewP6Pod()
	s, f, path := reviewP6Setup(t, newSchedulerFlowNode("n", "", ""))
	result, err := s.schedulePod(context.Background(), f, p)
	if err != nil {
		t.Fatal(err)
	}
	s.recordBindOutcome(result.AttemptID, p, result.SuggestedHost, true, "")
	events := reviewP6Events(t, s, path)
	candidateID := ""
	bindID := ""
	uidLinked := false
	for _, ev := range events {
		if ev.Kind == decisionrecorder.KindCandidateCheckResult {
			candidateID = ev.AttemptID
		}
		if ev.Kind == decisionrecorder.KindBindOutcome {
			bindID = ev.AttemptID
		}
		if ev.AttemptID != "" && ev.Payload["pod_uid"] == string(p.UID) {
			uidLinked = true
		}
	}
	if candidateID == "" || bindID != candidateID {
		t.Errorf("candidate attempt=%q bind attempt=%q; cannot associate retries", candidateID, bindID)
	}
	if !uidLinked {
		t.Error("no event links attempt_id to pod_uid")
	}
}

type reviewP6HTTP func(*http.Request) (*http.Response, error)

func (f reviewP6HTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReviewP6R6HAMiAllExcludedJSONL(t *testing.T) {
	t.Setenv("SCHED_REQUIRED_CHECKS_CONFIG", "")
	p := reviewP6Pod()
	p.Spec.Containers[0].Resources.Requests = v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}
	nodes := []*v1.Node{newSchedulerFlowNode("a", "l40s", "1"), newSchedulerFlowNode("b", "l40s", "1")}
	for _, n := range nodes {
		n.Labels["gpu.management"] = "hami"
	}
	s, f, path := reviewP6Setup(t, nodes...)
	s.hamiClient = plugin.NewHAMiExtenderClientWithHTTP(&http.Client{Transport: reviewP6HTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"nodeNames":[]}`)), Header: http.Header{}}, nil
	})}, 0)
	result, err := s.schedulePod(context.Background(), f, p)
	if err == nil || result.FeasibleNodes != 0 {
		t.Fatal("control: HAMi should exclude all candidates")
	}
	count := 0
	for _, ev := range reviewP6Events(t, s, path) {
		if ev.Kind == decisionrecorder.KindCandidateCheckResult {
			count++
			raw, _ := json.Marshal(ev.Payload)
			if ev.Payload["is_filtered"] != true || !strings.Contains(string(raw), "hami_filtered") {
				t.Errorf("node=%s final HAMi exclusion absent in JSONL: %s", ev.NodeName, raw)
			}
		}
	}
	if count != 2 {
		t.Errorf("candidate records=%d want 2", count)
	}
}
