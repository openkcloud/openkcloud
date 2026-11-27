// ============================================================
// node_exclusion_test.go: 배제 노드 라벨 발행 검증
// 상세: nodeExclusion 판정(control-plane/master → 배제, 벤더 excludeNodeSelector →
//       policy 배제, control-plane 이 policy 보다 강함)과 reconcileNodeExclusionLabels
//       의 부여·회수(배제가 풀리면 라벨을 지운다)를 확인한다.
// 생성일: 2026-08-12 | 수정일: 2026-10-11
// ============================================================

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// TestNodeExclusion_ControlPlane 는 control-plane·master 라벨 노드가 배제로,
// 일반 워커는 비배제로 판정되는 것을 단정한다.
func TestNodeExclusion_ControlPlane(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{}

	master := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-master", Labels: map[string]string{controlPlaneNodeLabel: ""},
	}}
	if excluded, reason := nodeExclusion(master, policy); !excluded || reason != exclusionReasonControlPlane {
		t.Errorf("control-plane 노드 판정 = (%v, %q), want (true, %q)", excluded, reason, exclusionReasonControlPlane)
	}

	legacyMaster := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "old-master", Labels: map[string]string{masterNodeLabel: ""},
	}}
	if excluded, reason := nodeExclusion(legacyMaster, policy); !excluded || reason != exclusionReasonControlPlane {
		t.Errorf("master 노드 판정 = (%v, %q), want (true, %q)", excluded, reason, exclusionReasonControlPlane)
	}

	worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: map[string]string{}}}
	if excluded, reason := nodeExclusion(worker, policy); excluded || reason != "" {
		t.Errorf("워커 노드 판정 = (%v, %q), want (false, \"\")", excluded, reason)
	}
}

// TestNodeExclusion_PolicySelector 는 벤더 excludeNodeSelector 에 걸리는 노드가 policy
// 사유로 배제되고, 걸리지 않는 노드는 그대로인 것을 단정한다.
func TestNodeExclusion_PolicySelector(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Tenstorrent: npuv1alpha1.TenstorrentSpec{
				ExcludeNodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"maintenance": labelValueTrue}},
			},
		},
	}
	excludedNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker3", Labels: map[string]string{"maintenance": labelValueTrue},
	}}
	if excluded, reason := nodeExclusion(excludedNode, policy); !excluded || reason != exclusionReasonPolicy {
		t.Errorf("policy 배제 판정 = (%v, %q), want (true, %q)", excluded, reason, exclusionReasonPolicy)
	}

	otherNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: map[string]string{}}}
	if excluded, reason := nodeExclusion(otherNode, policy); excluded || reason != "" {
		t.Errorf("셀렉터 불일치 노드 판정 = (%v, %q), want (false, \"\")", excluded, reason)
	}
}

// TestNodeExclusion_ControlPlaneOverridesPolicy 는 control-plane 배제를 정책으로 끌 수 없는
// 안전 규칙을 단정한다 — 빈 excludeNodeSelector(전체 매치)로도, control-plane 과 policy 가
// 동시에 걸려도 사유는 언제나 control-plane 이다(더 강한 이유가 이긴다).
func TestNodeExclusion_ControlPlaneOverridesPolicy(t *testing.T) {
	master := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-master", Labels: map[string]string{controlPlaneNodeLabel: "", "maintenance": labelValueTrue},
	}}

	emptySelector := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{ExcludeNodeSelector: &metav1.LabelSelector{}},
		},
	}
	if excluded, reason := nodeExclusion(master, emptySelector); !excluded || reason != exclusionReasonControlPlane {
		t.Errorf("빈 excludeNodeSelector 상태 판정 = (%v, %q), want (true, %q)", excluded, reason, exclusionReasonControlPlane)
	}

	bothMatch := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Tenstorrent: npuv1alpha1.TenstorrentSpec{
				ExcludeNodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"maintenance": labelValueTrue}},
			},
		},
	}
	if excluded, reason := nodeExclusion(master, bothMatch); !excluded || reason != exclusionReasonControlPlane {
		t.Errorf("동시 매치 판정 = (%v, %q), want (true, %q)", excluded, reason, exclusionReasonControlPlane)
	}
}

// excludeRenderRequirements 는 sel 로 렌더한 PodSpec 의 nodeAffinity 요구사항을 돌려준다.
func excludeRenderRequirements(sel *metav1.LabelSelector) []corev1.NodeSelectorRequirement {
	var spec corev1.PodSpec
	applyExcludeNodeSelector(&spec, sel)
	if spec.Affinity == nil || spec.Affinity.NodeAffinity == nil ||
		spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return nil
	}
	var reqs []corev1.NodeSelectorRequirement
	for _, term := range spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		reqs = append(reqs, term.MatchExpressions...)
	}
	return reqs
}

// requirementExcludes 는 reqs(배제 = NOT 조건의 AND)가 이 라벨 집합을 뺐는지 본다 —
// 요구사항 하나라도 불만족이면 스케줄 불가, 즉 배제다.
func requirementExcludes(reqs []corev1.NodeSelectorRequirement, nodeLabels map[string]string) bool {
	for _, r := range reqs {
		v, present := nodeLabels[r.Key]
		switch r.Operator {
		case corev1.NodeSelectorOpNotIn:
			for _, want := range r.Values {
				if present && v == want {
					return true
				}
			}
		case corev1.NodeSelectorOpIn:
			hit := false
			for _, want := range r.Values {
				if present && v == want {
					hit = true
				}
			}
			if !hit {
				return true
			}
		case corev1.NodeSelectorOpExists:
			if !present {
				return true
			}
		case corev1.NodeSelectorOpDoesNotExist:
			if present {
				return true
			}
		}
	}
	return false
}

// TestExclusionConditions_LabelAndRenderPathsAgree 는 조건이 둘이고 노드가 하나만 맞을 때
// 라벨 경로와 렌더 경로가 같은 판정을 내는지 본다. 셀렉터를 통째로 AND 로 평가하면 이 입력을
// 미배제로 보고 렌더는 배제로 봐서, allocatable 이 0 인데 화면은 "배제 아님" 을 보고하는
// 식으로 두 경로가 갈라진다. 그 회귀를 한 시험에서 막는다.
func TestExclusionConditions_LabelAndRenderPathsAgree(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"a": "1", "b": "2"}}
	nodeLabels := map[string]string{"a": "1", "b": "3"} // a 만 맞는다
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: nodeLabels}}
	policy := &npuv1alpha1.NPUClusterPolicy{
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Nvidia: npuv1alpha1.NvidiaSpec{ExcludeNodeSelector: sel},
		},
	}

	labelPath := matchesAnyExcludeNodeSelector(node, policy)
	renderPath := requirementExcludes(excludeRenderRequirements(sel), nodeLabels)
	if !labelPath || !renderPath {
		t.Errorf("조건 하나만 맞는 노드 판정: 라벨 경로 = %v, 렌더 경로 = %v, 둘 다 true 여야 함",
			labelPath, renderPath)
	}
	if labelPath != renderPath {
		t.Errorf("두 경로가 갈라졌다: 라벨 = %v, 렌더 = %v", labelPath, renderPath)
	}
}

// TestExclusionConditions_DropsInvalidConditions 는 values 없는 In 과 알 수 없는 operator 가
// 버려지는지 본다. 그대로 렌더하면 DaemonSet 이 오브젝트 검증에서 거부되고(values must be
// specified when operator is In or NotIn) 그 오류가 Reconcile 로 올라가 관계없는 벤더와
// side operand 까지 멈춘다. 렌더된 요구사항에 Values 가 빈 In/NotIn 이 하나도 없어야 한다.
func TestExclusionConditions_DropsInvalidConditions(t *testing.T) {
	for name, sel := range map[string]*metav1.LabelSelector{
		"values 없는 In": {MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "maintenance", Operator: metav1.LabelSelectorOpIn},
		}},
		"values 없는 NotIn": {MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "maintenance", Operator: metav1.LabelSelectorOpNotIn, Values: []string{}},
		}},
		"알 수 없는 operator": {MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "maintenance", Operator: metav1.LabelSelectorOperator("Equals"), Values: []string{"true"}},
		}},
	} {
		kept, dropped := exclusionConditions(sel)
		if len(kept) != 0 || len(dropped) != 1 {
			t.Errorf("%s: kept = %v, dropped = %v, want kept 0 / dropped 1", name, kept, dropped)
		}
		for _, r := range excludeRenderRequirements(sel) {
			if (r.Operator == corev1.NodeSelectorOpIn || r.Operator == corev1.NodeSelectorOpNotIn) &&
				len(r.Values) == 0 {
				t.Errorf("%s: 렌더에 Values 가 빈 %s 요구사항이 남음(key=%s)", name, r.Operator, r.Key)
			}
			if r.Operator != corev1.NodeSelectorOpIn && r.Operator != corev1.NodeSelectorOpNotIn &&
				r.Operator != corev1.NodeSelectorOpExists && r.Operator != corev1.NodeSelectorOpDoesNotExist {
				t.Errorf("%s: 렌더에 알 수 없는 operator %q 가 남음", name, r.Operator)
			}
		}
	}
}

// TestExclusionConditions_MatchLabelsOrderIsDeterministic 는 matchLabels 를 여러 번 렌더해도
// 요구사항 순서가 같은지 본다. map 순회 순서가 비결정적이면 렌더된 DaemonSet 의 nodeAffinity
// 순서가 매 reconcile 마다 달라져 불필요한 갱신이 반복된다.
func TestExclusionConditions_MatchLabelsOrderIsDeterministic(t *testing.T) {
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{
		"zone": "b", "arch": "x", "maintenance": "true", "pool": "spot", "tier": "cold",
	}}
	first := excludeRenderRequirements(sel)
	for i := 0; i < 20; i++ {
		got := excludeRenderRequirements(sel)
		if len(got) != len(first) {
			t.Fatalf("요구사항 개수가 흔들림: %d != %d", len(got), len(first))
		}
		for j := range got {
			if got[j].Key != first[j].Key {
				t.Fatalf("%d 회차 %d 번째 키 = %q, 첫 회차 = %q — 순서가 비결정적",
					i, j, got[j].Key, first[j].Key)
			}
		}
	}
}

// TestReconcileNodeExclusionLabels_CorrectsFalseLabelValue 는 손으로 붙인
// kcloud.ai/excluded=false 가 교정되는지 본다. 라벨 존재만 보면 배제 노드에서 "이미 맞다" 로
// 판정돼 값이 영구히 false 로 남고, npuctl·detector 는 "true" 만 배제로 인정하므로 화면은
// 배제가 아니다.
func TestReconcileNodeExclusionLabels_CorrectsFalseLabelValue(t *testing.T) {
	master := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-master",
		Labels: map[string]string{
			controlPlaneNodeLabel:   "",
			nodeExcludedLabel:       "false",
			nodeExcludedReasonLabel: exclusionReasonControlPlane,
		},
	}}
	r := nodeExclusionReconciler(master)
	if err := r.reconcileNodeExclusionLabels(context.Background(), &npuv1alpha1.NPUClusterPolicy{}); err != nil {
		t.Fatalf("reconcileNodeExclusionLabels 오류: %v", err)
	}
	var got corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-master"}, &got); err != nil {
		t.Fatalf("master 조회 실패: %v", err)
	}
	if got.Labels[nodeExcludedLabel] != labelValueTrue {
		t.Errorf("%s = %q, want %q — false 값이 교정되지 않음",
			nodeExcludedLabel, got.Labels[nodeExcludedLabel], labelValueTrue)
	}
}

// TestCleanupOwnedResources_ReclaimsExclusionLabels 는 정책 삭제 경로가 배제 라벨을
// 회수하는지 본다. 남으면 정책이 없는데도 CLI·콘솔이 "제외됨(policy)" 을 계속 보고하고
// detector 는 그 노드 검증을 계속 해당없음으로 접는다.
func TestCleanupOwnedResources_ReclaimsExclusionLabels(t *testing.T) {
	master := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-master",
		Labels: map[string]string{
			controlPlaneNodeLabel:   "",
			nodeExcludedLabel:       labelValueTrue,
			nodeExcludedReasonLabel: exclusionReasonControlPlane,
		},
	}}
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-policy", Namespace: draNamespace},
	}
	r := nodeExclusionReconciler(master, policy)

	if err := r.cleanupOwnedResources(context.Background(), policy); err != nil {
		t.Fatalf("cleanupOwnedResources 오류: %v", err)
	}
	var got corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-master"}, &got); err != nil {
		t.Fatalf("master 조회 실패: %v", err)
	}
	if _, ok := got.Labels[nodeExcludedLabel]; ok {
		t.Errorf("정책 삭제 후에도 %s 가 남음: %v", nodeExcludedLabel, got.Labels)
	}
	if _, ok := got.Labels[nodeExcludedReasonLabel]; ok {
		t.Errorf("정책 삭제 후에도 %s 가 남음: %v", nodeExcludedReasonLabel, got.Labels)
	}
}

// TestCleanupOwnedResources_ReclaimsDRALabels 는 정책 삭제 경로가 다섯 벤더의 dra-owned·
// dra-retired 노드 라벨을 회수하는지 본다. 남으면 정책이 없는데도 DRA DaemonSet 렌더가
// 그 노드를 계속 피하고 CLI 는 DRA 소유 노드로 보고한다.
func TestCleanupOwnedResources_ReclaimsDRALabels(t *testing.T) {
	worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-worker3",
		Labels: map[string]string{
			npuv1alpha1.DRAOwnedNodeLabel("tenstorrent"):  labelValueTrue,
			npuv1alpha1.DRARetiredNodeLabel("rebellions"): labelValueTrue,
			"kcloud.ai/tenstorrent.present":               labelValueTrue,
		},
	}}
	untouched := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-worker1", Labels: map[string]string{"kcloud.ai/nvidia.present": labelValueTrue},
	}}
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-policy", Namespace: draNamespace},
	}
	r := nodeExclusionReconciler(worker, untouched, policy)

	if err := r.cleanupOwnedResources(context.Background(), policy); err != nil {
		t.Fatalf("cleanupOwnedResources 오류: %v", err)
	}
	var got corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker3"}, &got); err != nil {
		t.Fatalf("worker3 조회 실패: %v", err)
	}
	for _, key := range []string{
		npuv1alpha1.DRAOwnedNodeLabel("tenstorrent"),
		npuv1alpha1.DRARetiredNodeLabel("rebellions"),
	} {
		if _, ok := got.Labels[key]; ok {
			t.Errorf("정책 삭제 후에도 %s 가 남음: %v", key, got.Labels)
		}
	}
	if got.Labels["kcloud.ai/tenstorrent.present"] != labelValueTrue {
		t.Errorf("present 라벨이 함께 지워짐: %v", got.Labels)
	}
	var other corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-worker1"}, &other); err != nil {
		t.Fatalf("worker1 조회 실패: %v", err)
	}
	if other.Labels["kcloud.ai/nvidia.present"] != labelValueTrue {
		t.Errorf("라벨 없는 노드가 바뀜: %v", other.Labels)
	}
}

// TestNodeEventFilter_SkipsHeartbeatUpdates 는 노드 watch 가 kubelet 하트비트로 reconcile 을
// 폭주시키지 않는지 본다. 배제 축이 보는 것은 라벨뿐이라 라벨이 안 바뀐 Update 는 통과해선
// 안 되고, 새 control-plane 노드 합류(Create)와 이탈(Delete)은 통과해야 한다.
func TestNodeEventFilter_SkipsHeartbeatUpdates(t *testing.T) {
	base := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "worker1", Labels: map[string]string{"zone": "a"},
	}}
	heartbeat := base.DeepCopy()
	heartbeat.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	if nodeEventFilter.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: heartbeat}) {
		t.Error("라벨이 안 바뀐 Update 가 통과함 — 하트비트마다 reconcile 이 돈다")
	}

	labeled := base.DeepCopy()
	labeled.Labels["maintenance"] = labelValueTrue
	if !nodeEventFilter.Update(event.UpdateEvent{ObjectOld: base, ObjectNew: labeled}) {
		t.Error("라벨이 바뀐 Update 가 막힘 — 배제 라벨 부착이 아무 효과가 없다")
	}
	if !nodeEventFilter.Create(event.CreateEvent{Object: base}) {
		t.Error("Create 가 막힘 — 새 control-plane 노드에 배제 라벨이 안 붙는다")
	}
	if !nodeEventFilter.Delete(event.DeleteEvent{Object: base}) {
		t.Error("Delete 가 막힘")
	}
}

// TestMapNodeToClusterPolicies 는 노드 이벤트가 NCP 요청으로 옮겨지는지 본다.
func TestMapNodeToClusterPolicies(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-policy", Namespace: draNamespace},
	}
	r := nodeExclusionReconciler(policy)
	reqs := r.mapNodeToClusterPolicies(context.Background(),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1"}})
	if len(reqs) != 1 || reqs[0].Name != "cluster-policy" || reqs[0].Namespace != draNamespace {
		t.Errorf("노드 이벤트 매핑 = %v, want kcloud/cluster-policy 1건", reqs)
	}
}

func nodeExclusionReconciler(objs ...client.Object) *NPUClusterPolicyReconciler {
	s := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &NPUClusterPolicyReconciler{Client: c, Scheme: s, Recorder: record.NewFakeRecorder(64)}
}

// TestReconcileNodeExclusionLabels_PublishesAndClears 는 control-plane 노드에 배제 라벨이
// 붙고, 워커에는 안 붙으며, 라벨(control-plane 자체)이 없어지면 배제 라벨도 지워지는(상태를
// 남기지 않는) 왕복을 확인한다.
func TestReconcileNodeExclusionLabels_PublishesAndClears(t *testing.T) {
	policy := &npuv1alpha1.NPUClusterPolicy{}
	master := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "k8s-master", Labels: map[string]string{controlPlaneNodeLabel: ""},
	}}
	worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker1", Labels: map[string]string{}}}
	r := nodeExclusionReconciler(master, worker)

	if err := r.reconcileNodeExclusionLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileNodeExclusionLabels 오류: %v", err)
	}

	var gotMaster corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-master"}, &gotMaster); err != nil {
		t.Fatalf("master 조회 실패: %v", err)
	}
	if gotMaster.Labels[nodeExcludedLabel] != labelValueTrue {
		t.Errorf("master 의 %s = %q, want %q", nodeExcludedLabel, gotMaster.Labels[nodeExcludedLabel], labelValueTrue)
	}
	if gotMaster.Labels[nodeExcludedReasonLabel] != exclusionReasonControlPlane {
		t.Errorf("master 의 %s = %q, want %q", nodeExcludedReasonLabel,
			gotMaster.Labels[nodeExcludedReasonLabel], exclusionReasonControlPlane)
	}

	var gotWorker corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "worker1"}, &gotWorker); err != nil {
		t.Fatalf("worker 조회 실패: %v", err)
	}
	if _, ok := gotWorker.Labels[nodeExcludedLabel]; ok {
		t.Errorf("워커에 배제 라벨이 붙음: %v", gotWorker.Labels)
	}

	// control-plane 라벨을 떼면(배제가 풀리면) 두 라벨 모두 지워져야 한다 — 상태를 남기지 않는다.
	patch := client.MergeFrom(gotMaster.DeepCopy())
	delete(gotMaster.Labels, controlPlaneNodeLabel)
	if err := r.Patch(context.Background(), &gotMaster, patch); err != nil {
		t.Fatalf("control-plane 라벨 제거 실패: %v", err)
	}
	if err := r.reconcileNodeExclusionLabels(context.Background(), policy); err != nil {
		t.Fatalf("reconcileNodeExclusionLabels(해제) 오류: %v", err)
	}
	var gotAfter corev1.Node
	if err := r.Get(context.Background(), types.NamespacedName{Name: "k8s-master"}, &gotAfter); err != nil {
		t.Fatalf("master 재조회 실패: %v", err)
	}
	if _, ok := gotAfter.Labels[nodeExcludedLabel]; ok {
		t.Errorf("배제 해제 후에도 %s 가 남음: %v", nodeExcludedLabel, gotAfter.Labels)
	}
	if _, ok := gotAfter.Labels[nodeExcludedReasonLabel]; ok {
		t.Errorf("배제 해제 후에도 %s 가 남음: %v", nodeExcludedReasonLabel, gotAfter.Labels)
	}
}
