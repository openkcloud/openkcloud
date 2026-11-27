package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendlog "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/backend/log"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/decisionrecorder"
	framework "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework"
	"github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/plugin"
	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Catalog off must not disable required-check JSONL (attempt + candidate + bind).
func TestCatalogOffStillRecordsJSONL(t *testing.T) {
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

	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	rec, err := decisionrecorder.New(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.Close)

	s := &Scheduler{
		Cache:            cache,
		DecisionRecorder: rec,
		hamiClient:       plugin.NewHAMiExtenderClient(),
	}
	fwk := framework.NewFramework(
		nil,
		[]framework.FilterPlugin{plugin.NewNodeResourcesFit()},
		[]framework.ScorePlugin{plugin.NewCatalogAwareScore(cache)},
		nil,
		nil,
	)

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "uid-catalog-off"},
		Spec: v1.PodSpec{Containers: []v1.Container{{
			Name: "c",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{v1.ResourceCPU: resource.MustParse("100m")},
			},
		}}},
	}
	result, err := s.schedulePod(ctx, fwk, pod)
	if err != nil {
		t.Fatal(err)
	}
	s.recordBindOutcome(result.AttemptID, pod, result.SuggestedHost, true, "")
	rec.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	attemptID := ""
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev decisionrecorder.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		kinds[ev.Kind]++
		if ev.AttemptID != "" {
			if attemptID == "" {
				attemptID = ev.AttemptID
			} else if ev.AttemptID != attemptID {
				t.Fatalf("mixed attempt ids: %q vs %q", attemptID, ev.AttemptID)
			}
		}
		if ev.Payload["pod_uid"] == string(pod.UID) && ev.AttemptID == "" {
			t.Fatalf("pod_uid without attempt_id: %+v", ev)
		}
	}
	if kinds[decisionrecorder.KindSchedulingAttempt] < 1 {
		t.Fatalf("missing scheduling_attempt: %v", kinds)
	}
	if kinds[decisionrecorder.KindCandidateCheckResult] < 1 {
		t.Fatalf("missing candidate_check_result: %v", kinds)
	}
	if kinds[decisionrecorder.KindBindOutcome] < 1 {
		t.Fatalf("missing bind_outcome: %v", kinds)
	}
}
