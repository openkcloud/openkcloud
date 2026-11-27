// describe_test.go: CollectNodeDetail 단위 테스트(fake client, envtest 불필요)
// 상세: 노드 하나의 상세(장치·health·evidence·정책)가 다른 노드 것과 섞이지 않고
// 노드 이름으로 걸러지는지 검증한다. status_test.go 의 fake client 헬퍼를 재사용한다.
// 생성일: 2026-08-24 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// TestCollectNodeDetail_조회대상없으면빈값 은 노드는 있으나 NodeDeviceReport·health·
// evidence·policy 가 없는 경우를 본다("둘 다 없으면 오류" 조건과 겹치지 않도록 Node
// 하나는 넣는다 — 완전 빈 클러스터는 별도 시험(...둘다없으면오류)이 다룬다).
func TestCollectNodeDetail_조회대상없으면빈값(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"}}
	c := newTestClient(t, node)
	d, err := c.CollectNodeDetail(context.Background(), "k8s-worker1")
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if d.Health != nil {
		t.Errorf("health 가 없으면 nil 이어야 한다: %+v", d.Health)
	}
	if len(d.Evidence) != 0 || len(d.Policies) != 0 {
		t.Errorf("없는 것을 지어냈다: evidence=%d policies=%d", len(d.Evidence), len(d.Policies))
	}
}

// TestCollectNodeDetail_노드와NDR둘다없으면오류 는 CollectNodeDetail 의 유일한 오류
// 조건을 본다 — 노드도 NodeDeviceReport 도 없을 때만 오류를 반환해야 한다.
func TestCollectNodeDetail_노드와NDR둘다없으면오류(t *testing.T) {
	c := newTestClient(t)
	_, err := c.CollectNodeDetail(context.Background(), "no-such-node")
	if err == nil {
		t.Fatalf("노드·NDR 둘 다 없으면 오류를 반환해야 한다")
	}
}

// TestCollectNodeDetail_값이있으면옮기고다른노드것은섞이지않는다 는 Step 6 시험이다.
// evidence·정책은 다른 노드 것 한 건씩을 함께 넣어 이 노드 것만 걸리는지 본다.
func TestCollectNodeDetail_값이있으면옮기고다른노드것은섞이지않는다(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "k8s-worker1",
			Labels: map[string]string{"kcloud.ai/excluded": "true", "kcloud.ai/excluded-reason": "policy"},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("2"),
			},
		},
	}
	ndr := &npuv1alpha1.NodeDeviceReport{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"},
		Spec:       npuv1alpha1.NodeDeviceReportSpec{NodeName: "k8s-worker1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{
			Devices: []npuv1alpha1.DeviceEntry{
				{Vendor: "nvidia", Model: "a30", Count: 1, PCIeAddress: "0000:18:00.0"},
			},
		},
	}
	ah := &npuv1alpha1.AcceleratorHealth{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker1"},
		Spec:       npuv1alpha1.AcceleratorHealthSpec{NodeName: "k8s-worker1"},
		Status: npuv1alpha1.AcceleratorHealthStatus{
			State: "Healthy", AllocationAllowed: true,
			Devices: []npuv1alpha1.DeviceHealth{{PCIAddress: "0000:18:00.0", State: "Healthy"}},
		},
	}
	expiresAt, err := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	if err != nil {
		t.Fatalf("time.Parse: %v", err)
	}
	expires := metav1.NewTime(expiresAt)
	evThis := &npuv1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "ev-worker1"},
		Spec:       npuv1alpha1.AcceleratorEvidenceSpec{NodeName: "k8s-worker1", Vendor: "nvidia"},
		Status:     npuv1alpha1.AcceleratorEvidenceStatus{Level: "Observed", ExpiresAt: &expires},
	}
	evOther := &npuv1alpha1.AcceleratorEvidence{
		ObjectMeta: metav1.ObjectMeta{Name: "ev-worker2"},
		Spec:       npuv1alpha1.AcceleratorEvidenceSpec{NodeName: "k8s-worker2", Vendor: "nvidia"},
		Status:     npuv1alpha1.AcceleratorEvidenceStatus{Level: "Observed"},
	}
	policyThis := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-worker1"},
		Spec:       npuv1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			Phase:   "Ready",
			Targets: []npuv1alpha1.TargetStatus{{NodeName: "k8s-worker1"}},
		},
	}
	policyOther := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "acpp-worker2"},
		Spec:       npuv1alpha1.AcceleratorPartitionPolicySpec{Vendor: "nvidia"},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			Phase:   "Ready",
			Targets: []npuv1alpha1.TargetStatus{{NodeName: "k8s-worker2"}},
		},
	}

	c := newTestClient(t, node, ndr, ah, evThis, evOther, policyThis, policyOther)
	d, err := c.CollectNodeDetail(context.Background(), "k8s-worker1")
	if err != nil {
		t.Fatalf("CollectNodeDetail: %v", err)
	}

	if !d.Excluded || d.ExcludedReason != "policy" {
		t.Errorf("배제 라벨 미반영: %+v", d)
	}
	if d.Allocatable["nvidia.com/gpu"] != "2" {
		t.Errorf("allocatable 미반영: %+v", d.Allocatable)
	}
	if len(d.Devices) != 1 || d.Devices[0].Product != "nvidia/a30" {
		t.Errorf("장치 미반영: %+v", d.Devices)
	}
	if d.Health == nil || d.Health.State != "Healthy" || !d.Health.AllocationAllowed || len(d.Health.Devices) != 1 {
		t.Fatalf("health 미반영: %+v", d.Health)
	}
	if d.Health.Devices[0].PCIAddress != "0000:18:00.0" {
		t.Errorf("health 장치 미반영: %+v", d.Health.Devices)
	}
	if len(d.Evidence) != 1 || d.Evidence[0].Level != "Observed" {
		t.Fatalf("evidence 가 이 노드 것만 걸리지 않음: %+v", d.Evidence)
	}
	gotExpires, err := time.Parse(time.RFC3339, d.Evidence[0].ExpiresAt)
	if err != nil || !gotExpires.Equal(expiresAt) {
		t.Errorf("evidence.expiresAt 불일치: got=%q want=%q (parse err=%v)", d.Evidence[0].ExpiresAt, expiresAt, err)
	}
	if len(d.Policies) != 1 || d.Policies[0].Name != "acpp-worker1" {
		t.Errorf("정책이 이 노드 것만 걸리지 않음: %+v", d.Policies)
	}
}

// TestListNodeNames_이름순정렬 은 노드 이름이 알파벳순으로 나오는지 본다. 입력 순서
// (zebra→alpha→beta)를 정렬 순서와 다르게 섞는다.
//
// fake.NewClientBuilder() 의 List() 는 이미 정렬된 순서로 반환하므로, 이 시험은
// sort.Strings 를 지워도 실패하지 않는다 — 실제 쿠버네티스 API 서버는 삽입 순서로
// 반환하므로 프로덕션에서는 sort.Strings 가 필수다. 이 한계는 코드 리뷰로 보완한다.
func TestListNodeNames_이름순정렬(t *testing.T) {
	c := newTestClient(t,
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "zebra-node"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "alpha-node"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "beta-node"}},
	)
	got, err := c.ListNodeNames(context.Background())
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	want := []string{"alpha-node", "beta-node", "zebra-node"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestListNodeNames_노드없으면빈슬라이스 는 노드가 없을 때 빈 결과(길이 0)를
// 돌려주는지 검증한다.
func TestListNodeNames_노드없으면빈슬라이스(t *testing.T) {
	c := newTestClient(t)
	got, err := c.ListNodeNames(context.Background())
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("빈 클러스터인데 %v 를 돌려줬다", got)
	}
}

// NodeDeviceReport status.consumers(kubelet PodResources 수집)가 NodeDetail 로 옮겨지는지,
// 수집 실패 사유와 관측 시각도 함께 오는지 본다.
func TestCollectNodeDetail_소비자목록과오류를옮긴다(t *testing.T) {
	at := metav1.NewTime(time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC))
	ndr := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "rngd-1"},
		Status: npuv1alpha1.NodeDeviceReportStatus{ConsumersObservedAt: &at,
			Consumers: []npuv1alpha1.DeviceConsumer{{
				// 역순 픽스처: 수집기가 네임스페이스·파드 순으로 정렬해 돌려줘야 한다.
				DeviceID: "89413086", ResourceName: "furiosa.ai/rngd",
				Namespace: "default", Pod: "rngd-pod", Container: "rngd-pod",
			}, {
				DeviceID: "11111111", ResourceName: "furiosa.ai/rngd",
				Namespace: "default", Pod: "a-pod", Container: "a-pod",
			}}}}
	c := newTestClient(t, ndr)
	d, err := c.CollectNodeDetail(context.Background(), "rngd-1")
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	want := []ConsumerView{{
		Namespace: "default", Pod: "a-pod", Container: "a-pod",
		ResourceName: "furiosa.ai/rngd", DeviceID: "11111111",
	}, {
		Namespace: "default", Pod: "rngd-pod", Container: "rngd-pod",
		ResourceName: "furiosa.ai/rngd", DeviceID: "89413086",
	}}
	if !slices.Equal(d.Consumers, want) || d.ConsumersObservedAt != "2026-10-06T01:02:03Z" || d.ConsumersError != "" {
		t.Fatalf("consumers=%+v at=%q err=%q", d.Consumers, d.ConsumersObservedAt, d.ConsumersError)
	}
	bad := &npuv1alpha1.NodeDeviceReport{ObjectMeta: metav1.ObjectMeta{Name: "n2"},
		Status: npuv1alpha1.NodeDeviceReportStatus{ConsumersError: "podresources 결과 파일 없음"}}
	d2, err := newTestClient(t, bad).CollectNodeDetail(context.Background(), "n2")
	if err != nil {
		t.Fatalf("오류: %v", err)
	}
	if d2.Consumers != nil || d2.ConsumersObservedAt != "" || d2.ConsumersError != "podresources 결과 파일 없음" {
		t.Fatalf("실패 사유만 옮겨야 한다: %+v", d2)
	}
}
