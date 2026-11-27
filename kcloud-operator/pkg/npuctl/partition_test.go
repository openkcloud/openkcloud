// partition_test.go: CollectPartitionStatus/CollectSharingStatus/ApplyPartition/
// ApplySharing 단위 테스트(fake client, envtest 불필요)
// 상세: 조회는 status.targets 로 이미 해석된 노드만 내는지, 적용은 대상 없음·값 같음·
// 벤더 미지원·삭제 중 네 경우를 각각 거절/무변경 하는지, layout·sharing 두 축이
// 서로를 건드리지 않는지를 검증한다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// testProfile1g6gb 는 이 파일 여러 시험이 공유하는 기본 profile 값이다(goconst).
const testProfile1g6gb = "1g.6gb"

func testNode() *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
}

// singleTargetStatus 는 status.targets 에 노드 하나만 있는 흔한 fixture 모양이다.
func singleTargetStatus(node, phase string) npuv1alpha1.AcceleratorPartitionPolicyStatus {
	return npuv1alpha1.AcceleratorPartitionPolicyStatus{
		Targets: []npuv1alpha1.TargetStatus{{NodeName: node, Phase: phase}},
	}
}

// timeSlicedSharing 은 timeSliced 모드 SharingSpec fixture 를 만든다(goconst·lll 정리).
func timeSlicedSharing(replicas int32) *npuv1alpha1.SharingSpec {
	return &npuv1alpha1.SharingSpec{
		Mode: npuv1alpha1.SharingModeTimeSliced, TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: replicas},
	}
}

// singleLayoutSpec 은 layout 하나만 있는 흔한 AcceleratorPartitionPolicySpec fixture 다.
func singleLayoutSpec(vendor, profile string, count int32) npuv1alpha1.AcceleratorPartitionPolicySpec {
	return npuv1alpha1.AcceleratorPartitionPolicySpec{
		Vendor: vendor, Layout: []npuv1alpha1.PartitionLayout{{Profile: profile, CountPerDevice: count}},
	}
}

// TestCollectPartitionStatus_레이아웃있는노드만 은 spec.layout 이 있고 status.targets 로
// 해석된 노드만 나오는지, profile 이 둘이면 행도 둘인지 본다.
func TestCollectPartitionStatus_레이아웃있는노드만(t *testing.T) {
	withLayout := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p1"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor: "nvidia",
			Layout: []npuv1alpha1.PartitionLayout{
				{Profile: testProfile1g6gb, CountPerDevice: 2},
				{Profile: "2g.12gb", CountPerDevice: 1},
			},
		},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			Targets: []npuv1alpha1.TargetStatus{{NodeName: "n1", Phase: "Ready"}},
		},
	}
	sharingOnly := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p2"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "rebellions",
			Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeTimeSliced},
		},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{
			Targets: []npuv1alpha1.TargetStatus{{NodeName: "n2", Phase: "Ready"}},
		},
	}
	unresolved := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p3"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor: "nvidia",
			Layout: []npuv1alpha1.PartitionLayout{{Profile: testProfile1g6gb, CountPerDevice: 1}},
		},
	}
	c := newTestClient(t, withLayout, sharingOnly, unresolved)

	rows, err := c.CollectPartitionStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectPartitionStatus: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("layout 이 2개인 정책 하나만(unresolved·sharing-only 는 제외) 2행이어야 하는데 %d행: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Node != "n1" || r.Vendor != "nvidia" || r.Phase != "Ready" {
			t.Errorf("행 값 불일치: %+v", r)
		}
	}
}

// TestCollectSharingStatus_모드와replicas 는 timeSliced·mps 각각 replicas 가 옮겨지는지,
// sharing 이 없는 정책은 빠지는지 본다.
func TestCollectSharingStatus_모드와replicas(t *testing.T) {
	ts := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p1"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "nvidia",
			Sharing: timeSlicedSharing(4),
		},
		Status: singleTargetStatus("n1", "Ready"),
	}
	noSharing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "p2"},
		Spec:       singleLayoutSpec("nvidia", testProfile1g6gb, 1),
		Status:     singleTargetStatus("n2", "Ready"),
	}
	c := newTestClient(t, ts, noSharing)

	rows, err := c.CollectSharingStatus(context.Background())
	if err != nil {
		t.Fatalf("CollectSharingStatus: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("sharing 있는 정책 하나만 나와야 하는데 %d행: %+v", len(rows), rows)
	}
	if rows[0].Node != "n1" || rows[0].Mode != npuv1alpha1.SharingModeTimeSliced || rows[0].Replicas != 4 {
		t.Errorf("행 값 불일치: %+v", rows[0])
	}
}

// TestApplyPartition_노드없음 은 클러스터에 없는 노드를 겨냥하면 거절하는지 본다
// (4절 표 — 대상 없음).
func TestApplyPartition_노드없음(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.ApplyPartition(context.Background(), "ghost", "nvidia", testProfile1g6gb, 1); err == nil {
		t.Fatalf("존재하지 않는 노드인데 에러가 없다")
	}
}

// TestApplyPartition_벤더미지원이면거절 은 tenstorrent(partition 완전 미지원, 두 product
// 없이 단일 행)를 겨냥하면 patch 전에 거절하는지 본다 — 거절 메시지가 등급을 담아야
// webhook 에러와 구분된다(4절 표 — 벤더 미지원). fake client 는 webhook 을 태우지
// 않으므로, 이 사전 점검이 없으면 아래 write 는 조용히 성공해 버린다.
func TestApplyPartition_벤더미지원이면거절(t *testing.T) {
	c := newTestClient(t, testNode())
	_, err := c.ApplyPartition(context.Background(), "n1", "tenstorrent", testProfile1g6gb, 1)
	if err == nil {
		t.Fatalf("tenstorrent 는 partition 미지원인데 에러가 없다")
	}
	if !strings.Contains(err.Error(), "Unsupported") {
		t.Errorf("거절 메시지에 등급(Unsupported)이 안 보인다: %v", err)
	}
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := c.c.List(context.Background(), &acpps); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(acpps.Items) != 0 {
		t.Errorf("거절됐는데 정책이 만들어졌다: %+v", acpps.Items)
	}
}

// TestApplyPartition_없으면생성 은 기존 정책이 없을 때 새로 만드는지, Created=true 인지 본다.
func TestApplyPartition_없으면생성(t *testing.T) {
	c := newTestClient(t, testNode())
	result, err := c.ApplyPartition(context.Background(), "n1", "nvidia", testProfile1g6gb, 2)
	if err != nil {
		t.Fatalf("ApplyPartition: %v", err)
	}
	if !result.Created || result.NoChange {
		t.Errorf("새로 만들었어야 하는데: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: result.Policy}, &got); err != nil {
		t.Fatalf("생성된 정책 Get 실패: %v", err)
	}
	if len(got.Spec.Layout) != 1 || got.Spec.Layout[0].Profile != testProfile1g6gb ||
		got.Spec.Layout[0].CountPerDevice != 2 {
		t.Errorf("layout 이 요청대로 안 만들어짐: %+v", got.Spec.Layout)
	}
}

// TestApplyPartition_있으면패치_sharing유지 는 기존 정책의 layout 만 바뀌고 sharing 은
// 그대로인지 본다 — partition apply 가 sharing 축을 건드리면 안 된다.
func TestApplyPartition_있으면패치_sharing유지(t *testing.T) {
	existing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "existing"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "nvidia",
			Layout:  []npuv1alpha1.PartitionLayout{{Profile: testProfile1g6gb, CountPerDevice: 1}},
			Sharing: timeSlicedSharing(4),
		},
		Status: singleTargetStatus("n1", "Ready"),
	}
	c := newTestClient(t, testNode(), existing)

	result, err := c.ApplyPartition(context.Background(), "n1", "nvidia", "2g.12gb", 1)
	if err != nil {
		t.Fatalf("ApplyPartition: %v", err)
	}
	if result.Created || result.NoChange || result.Policy != "existing" {
		t.Errorf("기존 정책을 patch 했어야 하는데: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "existing"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Spec.Layout) != 1 || got.Spec.Layout[0].Profile != "2g.12gb" {
		t.Errorf("layout 이 patch 되지 않음: %+v", got.Spec.Layout)
	}
	if got.Spec.Sharing == nil || got.Spec.Sharing.TimeSlicing == nil || got.Spec.Sharing.TimeSlicing.Replicas != 4 {
		t.Errorf("sharing 이 partition apply 로 훼손됨: %+v", got.Spec.Sharing)
	}
}

// TestApplyPartition_값같으면NoChange 는 요청값이 현재값과 같으면 patch 없이 성공하는지
// 본다(4절 표 — 값 같음. UpgradeVendor·SetVendorEnabled 와 같은 기존 NoChange 관례).
func TestApplyPartition_값같으면NoChange(t *testing.T) {
	existing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", ResourceVersion: "1"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor: "nvidia",
			Layout: []npuv1alpha1.PartitionLayout{{Profile: testProfile1g6gb, CountPerDevice: 2}},
		},
		Status: singleTargetStatus("n1", "Ready"),
	}
	c := newTestClient(t, testNode(), existing)

	result, err := c.ApplyPartition(context.Background(), "n1", "nvidia", testProfile1g6gb, 2)
	if err != nil {
		t.Fatalf("ApplyPartition: %v", err)
	}
	if !result.NoChange || result.Created {
		t.Errorf("값이 같으므로 NoChange 여야 하는데: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "existing"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion != "1" {
		t.Errorf("NoChange 인데 patch 가 실행돼 ResourceVersion 이 바뀜: %s", got.ResourceVersion)
	}
}

// TestApplyPartition_삭제중이면거절 은 대상 정책이 DeletionTimestamp 를 가지면(삭제
// 처리 중) patch 하지 않고 거절하는지 본다(4절 표 — 다른 컨트롤러가 필드를 소유:
// 삭제 reconcile 경로가 이 오브젝트를 정리하는 중이라 spec patch 가 무의미하다).
func TestApplyPartition_삭제중이면거절(t *testing.T) {
	now := metav1.Now()
	deleting := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deleting", DeletionTimestamp: &now, Finalizers: []string{"npu.ai/test-finalizer"},
		},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor: "nvidia",
			Layout: []npuv1alpha1.PartitionLayout{{Profile: testProfile1g6gb, CountPerDevice: 1}},
		},
		Status: singleTargetStatus("n1", "RollingBack"),
	}
	c := newTestClient(t, testNode(), deleting)

	_, err := c.ApplyPartition(context.Background(), "n1", "nvidia", "2g.12gb", 1)
	if err == nil {
		t.Fatalf("삭제 중인 정책인데 에러가 없다")
	}
	if !strings.Contains(err.Error(), "삭제") {
		t.Errorf("거절 메시지가 삭제 처리 중임을 안내하지 않음: %v", err)
	}
}

// TestApplyPartition_모호하면거절 은 같은 node+vendor 를 겨냥한 정책이 둘이면(정상
// 상황에서는 webhook 의 target-conflict 방지로 안 생겨야 하지만) 방어적으로 거절하는지
// 본다 — resolveDUS·resolveDIP 와 같은 fail-closed 관례.
func TestApplyPartition_모호하면거절(t *testing.T) {
	mk := func(name string) *npuv1alpha1.AcceleratorPartitionPolicy {
		return &npuv1alpha1.AcceleratorPartitionPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       singleLayoutSpec("nvidia", testProfile1g6gb, 1),
			Status:     npuv1alpha1.AcceleratorPartitionPolicyStatus{Targets: []npuv1alpha1.TargetStatus{{NodeName: "n1"}}},
		}
	}
	c := newTestClient(t, testNode(), mk("p1"), mk("p2"))
	if _, err := c.ApplyPartition(context.Background(), "n1", "nvidia", testProfile1g6gb, 1); err == nil {
		t.Fatalf("정책이 2개인데 에러가 없다")
	}
}

// TestPreviewPartitionApply_쓰지않음 은 Preview 가 ApplyPartition 과 같은 요약을 내되
// 아무것도 patch 하지 않는지 본다.
func TestPreviewPartitionApply_쓰지않음(t *testing.T) {
	existing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", ResourceVersion: "1"},
		Spec:       singleLayoutSpec("nvidia", testProfile1g6gb, 1),
		Status:     npuv1alpha1.AcceleratorPartitionPolicyStatus{Targets: []npuv1alpha1.TargetStatus{{NodeName: "n1"}}},
	}
	c := newTestClient(t, testNode(), existing)

	result, err := c.PreviewPartitionApply(context.Background(), "n1", "nvidia", "2g.12gb", 1)
	if err != nil {
		t.Fatalf("PreviewPartitionApply: %v", err)
	}
	if result.CurrentProfile != testProfile1g6gb || result.CurrentCountPerDevice != 1 {
		t.Errorf("현재값이 안 보인다: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "existing"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion != "1" || got.Spec.Layout[0].Profile != testProfile1g6gb {
		t.Errorf("Preview 가 patch 를 실행함: %+v", got)
	}
}

// --- sharing ---

// TestApplySharing_없으면생성_timeSliced 는 기존 정책이 없을 때 sharing 만 담은 정책을
// 만드는지 본다.
func TestApplySharing_없으면생성_timeSliced(t *testing.T) {
	c := newTestClient(t, testNode())
	result, err := c.ApplySharing(context.Background(), "n1", "nvidia", npuv1alpha1.SharingModeTimeSliced, 4)
	if err != nil {
		t.Fatalf("ApplySharing: %v", err)
	}
	if !result.Created {
		t.Errorf("새로 만들었어야: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: result.Policy}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Sharing == nil || got.Spec.Sharing.Mode != npuv1alpha1.SharingModeTimeSliced ||
		got.Spec.Sharing.TimeSlicing.Replicas != 4 {
		t.Errorf("sharing 이 요청대로 안 만들어짐: %+v", got.Spec.Sharing)
	}
	if len(got.Spec.Layout) != 0 {
		t.Errorf("sharing apply 가 layout 을 만들면 안 됨: %+v", got.Spec.Layout)
	}
}

// TestApplySharing_있으면패치_layout유지 는 기존 정책의 sharing 만 바뀌고 layout 은
// 그대로인지 본다.
func TestApplySharing_있으면패치_layout유지(t *testing.T) {
	existing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "existing"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "nvidia",
			Layout:  []npuv1alpha1.PartitionLayout{{Profile: testProfile1g6gb, CountPerDevice: 2}},
			Sharing: timeSlicedSharing(2),
		},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{Targets: []npuv1alpha1.TargetStatus{{NodeName: "n1"}}},
	}
	c := newTestClient(t, testNode(), existing)

	result, err := c.ApplySharing(context.Background(), "n1", "nvidia", npuv1alpha1.SharingModeMPS, 3)
	if err != nil {
		t.Fatalf("ApplySharing: %v", err)
	}
	if result.NoChange || result.Created {
		t.Errorf("patch 했어야: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "existing"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Sharing.Mode != npuv1alpha1.SharingModeMPS || got.Spec.Sharing.MPS.Replicas != 3 {
		t.Errorf("sharing 이 patch 되지 않음: %+v", got.Spec.Sharing)
	}
	if len(got.Spec.Layout) != 1 || got.Spec.Layout[0].Profile != testProfile1g6gb ||
		got.Spec.Layout[0].CountPerDevice != 2 {
		t.Errorf("layout 이 sharing apply 로 훼손됨: %+v", got.Spec.Layout)
	}
}

// TestApplySharing_삭제중이면거절 은 TestApplyPartition_삭제중이면거절 의 sharing 쪽
// 짝이다 — 이 검사가 resolvePartitionApply·resolveSharingApply 에 각자 복제돼 있으면
// sharing 쪽 시험이 없는 한 그 복제 하나를 지워도 다른 시험들은 전부 통과해버린다.
// 지금은 두 함수가 공유하는 resolveNodeACPP 안에 검사가 하나뿐이라, 이 시험이
// partition·sharing 양쪽 경로를 다 지킨다.
func TestApplySharing_삭제중이면거절(t *testing.T) {
	now := metav1.Now()
	deleting := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "deleting", DeletionTimestamp: &now, Finalizers: []string{"npu.ai/test-finalizer"},
		},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "nvidia",
			Sharing: timeSlicedSharing(2),
		},
		Status: singleTargetStatus("n1", "RollingBack"),
	}
	c := newTestClient(t, testNode(), deleting)

	_, err := c.ApplySharing(context.Background(), "n1", "nvidia", npuv1alpha1.SharingModeMPS, 3)
	if err == nil {
		t.Fatalf("삭제 중인 정책인데 에러가 없다")
	}
	if !strings.Contains(err.Error(), "삭제") {
		t.Errorf("거절 메시지가 삭제 처리 중임을 안내하지 않음: %v", err)
	}
}

// TestApplySharing_값같으면NoChange 는 sharing apply 도 값이 같으면 무변경인지 본다.
func TestApplySharing_값같으면NoChange(t *testing.T) {
	existing := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "existing", ResourceVersion: "1"},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:  "nvidia",
			Sharing: &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeMPS, MPS: &npuv1alpha1.MPSSpec{Replicas: 3}},
		},
		Status: npuv1alpha1.AcceleratorPartitionPolicyStatus{Targets: []npuv1alpha1.TargetStatus{{NodeName: "n1"}}},
	}
	c := newTestClient(t, testNode(), existing)

	result, err := c.ApplySharing(context.Background(), "n1", "nvidia", npuv1alpha1.SharingModeMPS, 3)
	if err != nil {
		t.Fatalf("ApplySharing: %v", err)
	}
	if !result.NoChange {
		t.Errorf("값이 같으므로 NoChange 여야: %+v", result)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "existing"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResourceVersion != "1" {
		t.Errorf("NoChange 인데 patch 됨: %s", got.ResourceVersion)
	}
}

// TestApplySharing_exclusive는공유끄기 는 --mode exclusive 가 layout 없이 sharing.mode=
// exclusive 를 만드는지 본다(BuildPartitionPolicy 의 기존 규칙 재사용 — Intent=exclusive
// 는 layout 이 없을 때만 그 값을 만든다).
func TestApplySharing_exclusive는공유끄기(t *testing.T) {
	c := newTestClient(t, testNode())
	result, err := c.ApplySharing(context.Background(), "n1", "nvidia", npuv1alpha1.SharingModeExclusive, 0)
	if err != nil {
		t.Fatalf("ApplySharing: %v", err)
	}
	var got npuv1alpha1.AcceleratorPartitionPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: result.Policy}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Sharing == nil || got.Spec.Sharing.Mode != npuv1alpha1.SharingModeExclusive {
		t.Errorf("exclusive 가 만들어지지 않음: %+v", got.Spec.Sharing)
	}
}

// TestApplySharing_미지원이면거절 은 furiosa(timeSliced 두 product 모두 미지원)를
// 겨냥하면 patch 전에 거절하는지 본다(4절 표 — 벤더 미지원).
func TestApplySharing_미지원이면거절(t *testing.T) {
	c := newTestClient(t, testNode())
	_, err := c.ApplySharing(context.Background(), "n1", "furiosa", npuv1alpha1.SharingModeTimeSliced, 4)
	if err == nil {
		t.Fatalf("furiosa 는 timeSliced 미지원인데 에러가 없다")
	}
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := c.c.List(context.Background(), &acpps); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(acpps.Items) != 0 {
		t.Errorf("거절됐는데 정책이 만들어졌다: %+v", acpps.Items)
	}
}

// TestApplySharing_모르는모드면거절 은 timeSliced|mps|exclusive 가 아닌 값을 거절하는지 본다.
func TestApplySharing_모르는모드면거절(t *testing.T) {
	c := newTestClient(t, testNode())
	if _, err := c.ApplySharing(context.Background(), "n1", "nvidia", "auto", 0); err == nil {
		t.Fatalf("알 수 없는 모드인데 에러가 없다")
	}
}
