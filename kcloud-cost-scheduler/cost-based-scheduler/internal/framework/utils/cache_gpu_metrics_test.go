package utils

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// R12 from REVIEW_P1P2.md
func TestReviewLegacyUnsupportedPayloadMustNotStampTime(t *testing.T) {
	cache := NewCache(context.Background())
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "review-node"}}
	if err := cache.AddNode(node, nil); err != nil {
		t.Fatal(err)
	}
	before := cache.Nodes()["review-node"].GPUMetricsUpdatedAt
	err := cache.UpdateNodeGPUMetrics("review-node", struct{}{})
	after := cache.Nodes()["review-node"].GPUMetricsUpdatedAt
	if !after.Equal(before) {
		t.Fatalf("empty struct stamped time despite no measured data: err=%v before=%s after=%s", err, before, after)
	}
	if err == nil {
		t.Fatal("expected error for unsupported payload")
	}
}

func TestUpdateNodeGPUMetricsRejectsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := NewCache(ctx)
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	if err := cache.AddNode(node, nil); err != nil {
		t.Fatal(err)
	}
	before := cache.nodes["n1"].GPUMetricsUpdatedAt
	time.Sleep(2 * time.Millisecond)
	err := cache.UpdateNodeGPUMetrics("n1", nil)
	if err == nil {
		t.Fatal("expected error on nil metrics")
	}
	after := cache.nodes["n1"].GPUMetricsUpdatedAt
	if !after.Equal(before) && after != before {
		// zero and zero are ok; if before was zero, still zero
	}
	if !after.IsZero() && after.After(before) && !before.IsZero() {
		t.Fatal("timestamp must not advance on nil update")
	}
	if !before.IsZero() && after != before {
		t.Fatalf("timestamp changed: %v -> %v", before, after)
	}
	if err := cache.UpdateNodeGPUMetrics("n1", map[string]any{"ok": true}); err == nil {
		t.Fatal("untyped map must not stamp time")
	}
	if err := cache.UpdateNodeGPUMetrics("n1", struct{}{}); err == nil {
		t.Fatal("empty struct must not stamp time")
	}
	if err := cache.UpdateNodeGPUMetrics("n1", AppliedNodeGPUMetrics{}); err == nil {
		t.Fatal("empty DeviceUUIDs must not stamp time")
	}
	if err := cache.UpdateNodeGPUMetrics("n1", AppliedNodeGPUMetrics{DeviceUUIDs: []string{"GPU-1"}}); err != nil {
		t.Fatal(err)
	}
	if cache.nodes["n1"].GPUMetricsUpdatedAt.IsZero() {
		t.Fatal("expected timestamp after validated measured payload")
	}
}
