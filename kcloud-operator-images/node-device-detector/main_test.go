// ============================================================
// main_test.go: 부트스트랩 헬퍼 단위 테스트
// 상세: fetchAllocatable 이 Node 객체에서 allocatable 과 배제 라벨(kcloud.ai/excluded)을 같은
// 조회로 함께 꺼내는지 검증한다. 새 API 호출을 안 만드는 것이 핵심이라, 라벨 유무·값까지 한 시험으로 고정한다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchAllocatable_ExcludedLabel(t *testing.T) {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)

	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"excluded=true", map[string]string{excludedNodeLabel: "true"}, true},
		{"excluded=false", map[string]string{excludedNodeLabel: "false"}, false},
		{"라벨 없음", nil, false},
		{"엉뚱한 값", map[string]string{excludedNodeLabel: "yes"}, false}, // 정확히 "true" 만 인정
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: tc.labels},
				Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
					"nvidia.com/gpu": resource.MustParse("2"),
				}},
			}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(node).Build()
			alloc, excluded, _ := fetchAllocatable(context.Background(), c, "n1")
			if excluded != tc.want {
				t.Errorf("excluded = %v, want %v", excluded, tc.want)
			}
			if alloc["nvidia.com/gpu"] != 2 {
				t.Errorf("allocatable 이 함께 안 실려왔다: %+v", alloc)
			}
		})
	}
}

// TestFetchAllocatable_ExcludedReasonLabel 은 배제 사유 라벨이 같은 Node 조회에 실려
// 오는지 본다. detector 가 사유를 안 읽으면 단계 문구가 사유를 추측하게 된다.
func TestFetchAllocatable_ExcludedReasonLabel(t *testing.T) {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)

	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"policy", map[string]string{excludedNodeLabel: "true", excludedReasonNodeLabel: "policy"}, "policy"},
		{"control-plane", map[string]string{excludedNodeLabel: "true", excludedReasonNodeLabel: "control-plane"}, "control-plane"},
		{"사유 라벨 없음", map[string]string{excludedNodeLabel: "true"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: tc.labels}}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(node).Build()
			_, excluded, reason := fetchAllocatable(context.Background(), c, "n1")
			if !excluded {
				t.Fatalf("excluded 여야 한다")
			}
			if reason != tc.want {
				t.Errorf("reason = %q, want %q", reason, tc.want)
			}
		})
	}
}

// TestFetchAllocatable_NodeGetFails 는 조회 실패 시 nil, false 로 안전하게 degrade 하는지 본다.
func TestFetchAllocatable_NodeGetFails(t *testing.T) {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).Build() // "missing" 노드 없음
	alloc, excluded, reason := fetchAllocatable(context.Background(), c, "missing")
	if alloc != nil || excluded || reason != "" {
		t.Errorf("fetchAllocatable(missing) = (%v, %v, %q), want (nil, false, \"\")", alloc, excluded, reason)
	}
}
