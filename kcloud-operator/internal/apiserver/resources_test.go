// ============================================================
// resources_test.go: CR 투영(classes/policies/workloads) 테스트
// 상세: 미수렴 정책이 converged=false 로 표시되는지, 거절 워크로드가 축과 원문 메시지를
//       모두 노출하는지, 축 추출에 실패해도 메시지가 살아남는지 본다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

func TestAxisFromMessage(t *testing.T) {
	// 리터럴로 손으로 쓰지 않고 실제 생산자(intent.Reject.Error())로 메시지를 만든다 —
	// mode.go 의 Sprintf 포맷이 바뀌면 이 테스트도 같이 깨져야 한다.
	msg := (&intent.Reject{
		Axis: "spec.accelerator.access.mode", Reason: "BackendUnsupported",
		Message: "node worker1 does not advertise time-slicing",
	}).Error()
	if got := axisFromMessage(msg); got != "spec.accelerator.access.mode" {
		t.Fatalf("축 추출 실패: %q", got)
	}
	if got := axisFromMessage("그냥 메시지"); got != "" {
		t.Fatalf("축이 없으면 빈 문자열이어야 함: %q", got)
	}
}

// 구조 필드 경로와 구버전 폴백 경로가 둘 다 축을 낸다. 폴백을 지우면 구버전 객체의 축이
// 조용히 사라지므로 둘 다 고정한다.
func TestToWorkloadView_RejectionPrefersStructuredThenFallsBack(t *testing.T) {
	base := v1alpha1.AcceleratorWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "team-a"},
		Spec: v1alpha1.AcceleratorWorkloadSpec{
			Accelerator: v1alpha1.AcceleratorRequest{
				Class:  "small",
				Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4},
			},
			Workload: v1alpha1.WorkloadTemplate{Image: "harbor/x:1"},
		},
	}

	structured := base
	structured.Status = v1alpha1.AcceleratorWorkloadStatus{
		Phase: v1alpha1.AWPhaseRejected,
		Conditions: []metav1.Condition{{
			Type: v1alpha1.AWCondTranslated, Status: metav1.ConditionFalse,
			Reason: v1alpha1.AWReasonBackendUnsupported, Message: "no node time-slices this device",
		}},
		Rejection: &v1alpha1.WorkloadRejection{
			Axis: intent.AxisMode, Reason: v1alpha1.AWReasonBackendUnsupported,
			Message: "no node time-slices this device",
		},
	}
	if got := toWorkloadView(structured).Rejection.Axis; got != intent.AxisMode {
		t.Errorf("구조 필드 축: got %q", got)
	}

	legacy := base
	legacy.Status = v1alpha1.AcceleratorWorkloadStatus{
		Phase: v1alpha1.AWPhaseRejected,
		Conditions: []metav1.Condition{{
			Type:   v1alpha1.AWCondTranslated,
			Status: metav1.ConditionFalse,
			Reason: v1alpha1.AWReasonBackendUnsupported,
			Message: (&intent.Reject{
				Axis: intent.AxisMode, Reason: v1alpha1.AWReasonBackendUnsupported,
				Message: "no node time-slices this device",
			}).Error(),
		}},
		// Rejection 은 구버전 operator 가 쓴 객체를 흉내내 nil 로 둔다.
	}
	if got := toWorkloadView(legacy).Rejection.Axis; got != intent.AxisMode {
		t.Errorf("구버전 폴백 축: got %q", got)
	}
}

func TestToPolicyView_UnconvergedIsFlagged(t *testing.T) {
	p := v1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-policy", Generation: 3},
		Spec: v1alpha1.AcceleratorPartitionPolicySpec{
			Vendor: "nvidia",
			Layout: []v1alpha1.PartitionLayout{{Profile: "1g.6gb", CountPerDevice: 4}},
			Sharing: &v1alpha1.SharingSpec{
				Mode:        v1alpha1.SharingModeTimeSliced,
				TimeSlicing: &v1alpha1.TimeSlicingSpec{Replicas: 4},
			},
		},
		Status: v1alpha1.AcceleratorPartitionPolicyStatus{
			ObservedGeneration: 2,
			Targets: []v1alpha1.TargetStatus{{
				NodeName:       "worker1",
				Phase:          v1alpha1.ACPPPhaseApplying,
				ResolvedLayout: []v1alpha1.ResolvedLayoutEntry{{Profile: "1g.6gb"}},
				Advertisement:  v1alpha1.AdvertisementStatus{AdvertisedResources: map[string]int32{"nvidia.com/mig-1g.6gb": 4}},
			}},
		},
	}
	got := toPolicyView(p)
	if got.Converged {
		t.Fatalf("observedGeneration(2) != generation(3) 이면 converged=false 여야 함")
	}
	if got.SharingMode != v1alpha1.SharingModeTimeSliced || got.SharingReplicas != 4 {
		t.Fatalf("공유 요청이 실려야 함: %#v", got)
	}
	if len(got.RequestedProfiles) != 1 || got.RequestedProfiles[0] != "1g.6gb" {
		t.Fatalf("요청 프로파일이 실려야 함: %#v", got.RequestedProfiles)
	}
	if len(got.Targets) != 1 || got.Targets[0].Advertised["nvidia.com/mig-1g.6gb"] != 4 {
		t.Fatalf("타깃 광고량이 실려야 함: %#v", got.Targets)
	}
}

func TestToWorkloadView_RejectionKeepsAxisAndMessage(t *testing.T) {
	aw := v1alpha1.AcceleratorWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "team-a"},
		Spec: v1alpha1.AcceleratorWorkloadSpec{
			Accelerator: v1alpha1.AcceleratorRequest{
				Class:  "small",
				Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeShared, Replicas: 4},
			},
			Workload: v1alpha1.WorkloadTemplate{Image: "harbor/x:1"},
		},
		Status: v1alpha1.AcceleratorWorkloadStatus{
			Phase: v1alpha1.AWPhaseRejected,
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.AWCondTranslated,
				Status: metav1.ConditionFalse,
				Reason: v1alpha1.AWReasonBackendUnsupported,
				Message: (&intent.Reject{
					Axis: "spec.accelerator.access.mode", Reason: v1alpha1.AWReasonBackendUnsupported,
					Message: "no node time-slices this device",
				}).Error(),
			}},
		},
	}
	got := toWorkloadView(aw)
	if got.Rejection == nil {
		t.Fatalf("거절 워크로드는 rejection 이 있어야 함")
	}
	if got.Rejection.Axis != "spec.accelerator.access.mode" {
		t.Fatalf("사용자가 고칠 축이 노출되어야 함: %q", got.Rejection.Axis)
	}
	if got.Rejection.Message == "" {
		t.Fatalf("원문 메시지를 지우면 안 됨")
	}
	if got.Mode != v1alpha1.AccessModeShared || got.AccessReplicas != 4 {
		t.Fatalf("요청 내용이 실려야 함: %#v", got)
	}
}

func TestToWorkloadView_TranslatedHasNoRejection(t *testing.T) {
	aw := v1alpha1.AcceleratorWorkload{
		ObjectMeta: metav1.ObjectMeta{Name: "ok", Namespace: "team-a"},
		Spec: v1alpha1.AcceleratorWorkloadSpec{
			Accelerator: v1alpha1.AcceleratorRequest{Class: "small", Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}},
			Workload:    v1alpha1.WorkloadTemplate{Image: "harbor/x:1"},
		},
		Status: v1alpha1.AcceleratorWorkloadStatus{
			Phase:    v1alpha1.AWPhaseTranslated,
			Resolved: &v1alpha1.ResolvedAllocation{Vendor: "nvidia", ResourceName: "nvidia.com/gpu", Quantity: 1},
			Conditions: []metav1.Condition{{
				Type: v1alpha1.AWCondTranslated, Status: metav1.ConditionTrue,
				Reason: v1alpha1.AWReasonTranslated, Message: "nvidia whole device on [worker1], exclusive",
			}},
		},
	}
	got := toWorkloadView(aw)
	if got.Rejection != nil {
		t.Fatalf("성공한 번역에 rejection 이 붙으면 안 됨: %#v", got.Rejection)
	}
	if got.Resolved == nil || got.Resolved.ResourceName != "nvidia.com/gpu" {
		t.Fatalf("resolved 가 실려야 함: %#v", got.Resolved)
	}
}

func TestClassesPoliciesWorkloads_Endpoints(t *testing.T) {
	objs := append(invObjects(),
		&v1alpha1.AcceleratorClass{
			ObjectMeta: metav1.ObjectMeta{Name: "small"},
			Spec: v1alpha1.AcceleratorClassSpec{
				Class:    "small",
				Mappings: []v1alpha1.AcceleratorMapping{{Vendor: "nvidia", NativeProfile: "1g.6gb"}},
			},
		},
		&v1alpha1.AcceleratorWorkload{
			ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "team-a"},
			Spec: v1alpha1.AcceleratorWorkloadSpec{
				Accelerator: v1alpha1.AcceleratorRequest{Class: "small", Access: v1alpha1.AccessSpec{Mode: v1alpha1.AccessModeExclusive}},
				Workload:    v1alpha1.WorkloadTemplate{Image: "harbor/x:1"},
			},
		},
	)
	s, _ := newServer(t, okAuth(), objs...)

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/classes", 1},
		{"/api/v1/policies", 1},
		{"/api/v1/workloads", 1},
	} {
		w := do(t, s, "GET", tc.path, "", "tok")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: 200 이어야 함: got %d (%s)", tc.path, w.Code, w.Body.String())
		}
		_, total, _, _ := decodePage[map[string]any](t, w.Body.Bytes())
		if total != tc.want {
			t.Fatalf("%s: total=%d, want %d", tc.path, total, tc.want)
		}
	}
	_ = client.ObjectKey{} // import 고정(다른 테스트가 client 를 쓴다)
}

// TestClassesPoliciesWorkloads_ForbiddenWithoutAuthz 는 각 라우트가 자기 리소스의 list 권한을
// 실제로 요구하는지 본다 — authz 를 빼먹으면 무권한 요청도 200 이 나간다.
func TestClassesPoliciesWorkloads_ForbiddenWithoutAuthz(t *testing.T) {
	for _, tc := range []struct {
		path     string
		resource string
	}{
		{"/api/v1/classes", "acceleratorclasses"},
		{"/api/v1/policies", "acceleratorpartitionpolicies"},
		{"/api/v1/workloads", "acceleratorworkloads"},
	} {
		opts := okAuth()
		opts.denyResources = map[string]bool{tc.resource: true}
		s, _ := newServer(t, opts)
		w := do(t, s, "GET", tc.path, "", "tok")
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: %s 없이는 403 이어야 함: got %d (%s)", tc.path, tc.resource, w.Code, w.Body.String())
		}
	}
}
