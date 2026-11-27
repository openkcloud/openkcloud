package plugin

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	utils "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/framework/utils"
	pc "github.com/KETI-Cloud-Platform/kcloud-cost-scheduler/cost-based-scheduler/internal/placementchecks"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type reviewP6RoundTrip func(*http.Request) (*http.Response, error)

func (f reviewP6RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReviewP6R1MissingResourceViewKeepsLegacyRejection(t *testing.T) {
	p := &v1.Pod{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "c", Resources: v1.ResourceRequirements{
		Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
	}}}}}
	n := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}}
	ni := utils.NewNodeInfo()
	ni.SetNode(n)
	plain := NewNodeResourcesFit().Filter(context.Background(), p, ni)
	if plain.IsSuccess() {
		t.Fatal("control must reject GPU absent from nil allocatable")
	}
	st := utils.NewAttemptState(pc.AttemptMeta{AttemptID: "a", StartedAt: time.Now()}, pc.Snapshot{}, pc.ParseRequest(p), pc.DefaultConfig())
	st.EnsureEvaluated(utils.EvalInputFromAttempt(st, p, n, "n", utils.NodeResourceViewFromNodeInfo(ni)))
	got := NewNodeResourcesFit().Filter(utils.WithAttempt(context.Background(), st), p, ni)
	if got.IsSuccess() {
		t.Fatal("record-mode cached resource_view_missing changed legacy GPU REJECT into Success")
	}
}

func TestReviewP6R2HAMiErrorsReachCaller(t *testing.T) {
	p := &v1.Pod{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "c", Resources: v1.ResourceRequirements{
		Requests: v1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")},
	}}}}}
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"bad_json", 200, "{"},
		{"http_500", 500, "failed"},
		{"response_error", 200, `{"error":"device inventory unavailable"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewHAMiExtenderClientWithHTTP(&http.Client{Transport: reviewP6RoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}, 0)
			_, err := c.Filter(p, []*v1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "n"}}})
			if err == nil {
				t.Fatal("HAMi failure returned nil error; hami_filter_error branch cannot observe this failure")
			}
		})
	}
	t.Run("network_error", func(t *testing.T) {
		defer func() {
			if v := recover(); v != nil {
				t.Errorf("HAMi transport error panicked instead of returning error: %v", v)
			}
		}()
		c := NewHAMiExtenderClientWithHTTP(&http.Client{Transport: reviewP6RoundTrip(func(*http.Request) (*http.Response, error) {
			return nil, io.ErrUnexpectedEOF
		})}, 0)
		_, err := c.Filter(p, []*v1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "n"}}})
		if err == nil {
			t.Error("transport error was not returned to scheduler")
		}
	})
}
