// control_test.go: UpgradeVendor/SetVendorEnabled 단위 테스트(fake client)
// 상세: 가드레일에 따라 제어 경로는 라이브 클러스터에서 실행하지 않고 fake client로만 검증한다.
// 생성일: 2026-07-16 | 수정일: 2026-10-27
package npuctl

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestUpgradeVendor_PatchesVersionAndUpgradePolicy(t *testing.T) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Model:  "generic",
			Driver: npuv1alpha1.DriverSpec{Version: "580.159.03"},
		},
	}
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "npuclusterpolicy-sample", Namespace: "kcloud"},
	}
	c := newTestClient(t, dip, ncp)

	result, err := c.UpgradeVendor(context.Background(), "nvidia", "595.58.03", UpgradeOptions{Auto: true, Force: true})
	if err != nil {
		t.Fatalf("UpgradeVendor: %v", err)
	}
	if result.NoChange {
		t.Fatalf("버전이 바뀌었으므로 NoChange=false 여야 함: %+v", result)
	}
	if result.PreviousVersion != "580.159.03" || result.NewVersion != "595.58.03" {
		t.Errorf("버전 필드 불일치: %+v", result)
	}

	var got npuv1alpha1.DriverInstallPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: dip.Name}, &got); err != nil {
		t.Fatalf("patch 후 DIP Get 실패: %v", err)
	}
	if got.Spec.Driver.Version != "595.58.03" {
		t.Errorf("DIP.spec.driver.version이 patch 되지 않음: got=%s", got.Spec.Driver.Version)
	}
	if got.Spec.UpgradePolicy == nil || !got.Spec.UpgradePolicy.AutoUpgrade || !got.Spec.UpgradePolicy.ForceUpgrade {
		t.Errorf("upgradePolicy가 auto/force 옵션대로 patch 되지 않음: %+v", got.Spec.UpgradePolicy)
	}
}

func TestUpgradeVendor_NoChangeWhenSameVersion(t *testing.T) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Driver: npuv1alpha1.DriverSpec{Version: "580.159.03"},
		},
	}
	c := newTestClient(t, dip)

	result, err := c.UpgradeVendor(context.Background(), "nvidia", "580.159.03", UpgradeOptions{})
	if err != nil {
		t.Fatalf("UpgradeVendor: %v", err)
	}
	if !result.NoChange {
		t.Errorf("동일 버전이면 NoChange=true 여야 함: %+v", result)
	}
}

func TestUpgradeVendor_AmbiguousVendorRequiresModel(t *testing.T) {
	warboy := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-warboy-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa",
			Model:  "warboy",
			Driver: npuv1alpha1.DriverSpec{Version: "1.9.8-3"},
		},
	}
	// rngd는 vendorKeyForDIP 규칙상 "rngd" 버킷으로 분리되므로 furiosa 조회와 충돌하지 않아야 한다.
	rngd := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-rngd-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa",
			Model:  "rngd",
			Driver: npuv1alpha1.DriverSpec{Version: "2026.1.0"},
		},
	}
	c := newTestClient(t, warboy, rngd)

	// furiosa 조회는 warboy 하나만 매칭되어야 함(rngd는 별도 vendor key).
	if _, err := c.UpgradeVendor(context.Background(), "furiosa", "1.9.9-1", UpgradeOptions{}); err != nil {
		t.Errorf("furiosa(=warboy만 매칭)는 성공해야 하는데 실패: %v", err)
	}

	if _, err := c.UpgradeVendor(context.Background(), "does-not-exist", "1.0.0", UpgradeOptions{}); err == nil {
		t.Errorf("매칭 없는 vendor는 에러여야 함")
	} else if !strings.Contains(err.Error(), "매칭되는 DriverInstallPolicy가 없습니다") {
		t.Errorf("에러 메시지가 예상과 다름: %v", err)
	}
}

// UpgradeVendor 는 FieldManager 를 명시한다 — 지정하지 않으면 API 서버가 바이너리 파일명에서
// 이름을 유도해 managedFields 항목이 실행 환경마다 달라진다. newTestClient 의 공유 fake 는
// managedFields 를 읽기에서 지우므로 쓰지 않고, interceptor.Funcs 의 Patch 훅으로 호출 시점의
// PatchOption 을 직접 가로챈다.
func TestUpgradeVendor_SetsFieldManager(t *testing.T) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "nvidia",
			Model:  "generic",
			Driver: npuv1alpha1.DriverSpec{Version: "580.159.03"},
		},
	}
	var got string
	fc := fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(dip).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object,
				patch client.Patch, opts ...client.PatchOption) error {
				po := &client.PatchOptions{}
				po.ApplyOptions(opts)
				got = po.FieldManager
				return cl.Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	c := newClientFromRuntimeClient(fc)

	if _, err := c.UpgradeVendor(context.Background(), "nvidia", "580.178.04", UpgradeOptions{}); err != nil {
		t.Fatalf("UpgradeVendor: %v", err)
	}
	if got != upgradeFieldManager {
		t.Fatalf("FieldManager=%q (%q 기대)", got, upgradeFieldManager)
	}
}

func TestSetVendorEnabled_TogglesAndReportsNoChange(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "npuclusterpolicy-sample", Namespace: "kcloud"},
		Spec: npuv1alpha1.NPUClusterPolicySpec{
			Furiosa: npuv1alpha1.FuriosaSpec{Enabled: true, Rngd: npuv1alpha1.RngdSpec{Enabled: false}},
		},
	}
	c := newTestClient(t, ncp)

	// rngd: false -> true (변경)
	result, err := c.SetVendorEnabled(context.Background(), "rngd", true)
	if err != nil {
		t.Fatalf("SetVendorEnabled(rngd): %v", err)
	}
	if result.NoChange || result.PreviousEnabled || !result.NewEnabled {
		t.Errorf("rngd enable 결과 불일치: %+v", result)
	}

	var got npuv1alpha1.NPUClusterPolicy
	ncpKey := client.ObjectKey{Name: ncp.Name, Namespace: ncp.Namespace}
	if err := c.c.Get(context.Background(), ncpKey, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Spec.Furiosa.Rngd.Enabled {
		t.Errorf("NCP.spec.furiosa.rngd.enabled이 patch 되지 않음")
	}
	if !got.Spec.Furiosa.Enabled {
		t.Errorf("furiosa.enabled은 그대로 유지되어야 함(rngd만 patch)")
	}

	// furiosa: true -> true (변경 없음)
	result2, err := c.SetVendorEnabled(context.Background(), "furiosa", true)
	if err != nil {
		t.Fatalf("SetVendorEnabled(furiosa): %v", err)
	}
	if !result2.NoChange {
		t.Errorf("동일 값이면 NoChange=true 여야 함: %+v", result2)
	}
}

func TestSetVendorEnabled_UnknownVendor(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "npuclusterpolicy-sample", Namespace: "kcloud"},
	}
	c := newTestClient(t, ncp)

	if _, err := c.SetVendorEnabled(context.Background(), "unknown-vendor", true); err == nil {
		t.Errorf("알 수 없는 vendor는 에러여야 함")
	}
}

func TestSetVendorEnabled_NoClusterPolicy(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.SetVendorEnabled(context.Background(), "nvidia", true); err == nil {
		t.Errorf("NPUClusterPolicy가 없으면 에러여야 함")
	}
}

// rollbackTestNode·rollbackTestDUSName 은 아래 TriggerDriverRollback 시험들이 공유하는
// 노드·DUS 이름이다(goconst — 리터럴 반복 대신 상수로 뺀다).
const (
	rollbackTestNode    = "k8s-worker1"
	rollbackTestDUSName = rollbackTestNode + "-" + VendorNvidia
)

// TestTriggerDriverRollback_단일매칭이면요청어노테이션 은 node+vendor 로 유일하게 찾은 DUS 에
// 롤백 요청 어노테이션이 붙는지(status 는 CLI 가 건드리지 않음), 그리고 patch 이전 값
// (PreviousState·PreviousVersion)이 결과에 그대로 옮겨지는지 본다. spec 은 건드리지 않아야 한다.
func TestTriggerDriverRollback_단일매칭이면요청어노테이션(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: rollbackTestDUSName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: rollbackTestNode, Vendor: VendorNvidia, Model: "a30"},
		Status: npuv1alpha1.DriverUpgradeStateStatus{
			State: npuv1alpha1.UpgradeStateIdle, CurrentVersion: "580.173.02", PreviousVersion: "575.64.03",
		},
	}
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	c := newTestClient(t, dus, dip)

	result, err := c.TriggerDriverRollback(context.Background(), rollbackTestNode, VendorNvidia, "")
	if err != nil {
		t.Fatalf("TriggerDriverRollback: %v", err)
	}
	if result.DUSName != rollbackTestDUSName || result.Node != rollbackTestNode ||
		result.Vendor != VendorNvidia || result.Model != "a30" {
		t.Errorf("결과 식별 필드 불일치: %+v", result)
	}
	if result.PreviousState != npuv1alpha1.UpgradeStateIdle || result.PreviousVersion != "575.64.03" {
		t.Errorf("patch 이전 값이 결과에 옮겨지지 않음: %+v", result)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: dus.Name}, &got); err != nil {
		t.Fatalf("patch 후 DUS Get 실패: %v", err)
	}
	if got.Status.State != npuv1alpha1.UpgradeStateIdle {
		t.Errorf("CLI 가 status 를 직접 바꿨다: got=%s", got.Status.State)
	}
	if _, has := got.Annotations[npuv1alpha1.RollbackRequestedAnnotation]; !has {
		t.Error("롤백 요청 어노테이션이 없다")
	}
	if got.Spec.Vendor != VendorNvidia || got.Spec.Model != "a30" {
		t.Errorf("spec 이 건드려짐(안 돼야 함): %+v", got.Spec)
	}
}

// TestTriggerDriverRollback_전이중상태는거절 은 상태 기계가 이미 전이 중인(Upgrading 등)
// DUS 에는 롤백 요청 어노테이션을 붙이지 않고 CLI 가 즉시 거절하는지 본다 — 전이 중에는
// 상태 기계의 자동 롤백 경로가 이미 있어 관리자 요청과 겹치면 진행 상태가 뒤섞인다.
func TestTriggerDriverRollback_전이중상태는거절(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: rollbackTestDUSName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: rollbackTestNode, Vendor: VendorNvidia, Model: "a30"},
		Status:     npuv1alpha1.DriverUpgradeStateStatus{State: npuv1alpha1.UpgradeStateUpgrading},
	}
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	c := newTestClient(t, dus, dip)
	if _, err := c.TriggerDriverRollback(context.Background(), rollbackTestNode, VendorNvidia, ""); err == nil {
		t.Fatal("Upgrading 상태의 롤백 요청을 받아들였다")
	}
}

// TestTriggerDriverRollback_노드없음 은 매칭되는 DUS 가 하나도 없으면(노드가 없거나 그
// 벤더 장치가 없는 노드) 에러를 내는지 본다.
func TestTriggerDriverRollback_노드없음(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: rollbackTestDUSName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: rollbackTestNode, Vendor: VendorNvidia},
	}
	c := newTestClient(t, dus)

	if _, err := c.TriggerDriverRollback(context.Background(), "no-such-node", VendorNvidia, ""); err == nil {
		t.Errorf("매칭 없는 노드는 에러여야 함")
	} else if !strings.Contains(err.Error(), "매칭되는 DriverUpgradeState가 없습니다") {
		t.Errorf("에러 메시지가 예상과 다름: %v", err)
	}
}

// TestTriggerDriverRollback_벤더생략시노드에하나뿐이면자동해결 은 --vendor 를 생략해도 그
// 노드에 DUS 가 하나뿐이면(흔한 단일 벤더 노드) 그대로 해결되는지 본다.
func TestTriggerDriverRollback_벤더생략시노드에하나뿐이면자동해결(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: rollbackTestDUSName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: rollbackTestNode, Vendor: VendorNvidia},
	}
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	c := newTestClient(t, dus, dip)

	result, err := c.TriggerDriverRollback(context.Background(), rollbackTestNode, "", "")
	if err != nil {
		t.Fatalf("vendor 생략인데 하나뿐이면 성공해야 함: %v", err)
	}
	if result.Vendor != VendorNvidia {
		t.Errorf("자동 해결된 vendor 불일치: got=%s", result.Vendor)
	}
}

// TestTriggerDriverRollback_벤더생략시여러개면모호에러 는 한 노드에 벤더가 다른 DUS 가
// 둘 이상(하이브리드 노드)이면 --vendor 없이는 특정할 수 없어 에러로 후보를 안내하는지
// 본다 — DUS 는 (node,vendor)로만 유일하므로(model 로는 안 갈림) 이 경우만 진짜 모호하다.
func TestTriggerDriverRollback_벤더생략시여러개면모호에러(t *testing.T) {
	dusNvidia := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "hybrid-node-nvidia"},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "hybrid-node", Vendor: VendorNvidia},
	}
	dusFuriosa := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "hybrid-node-furiosa"},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "hybrid-node", Vendor: "furiosa"},
	}
	c := newTestClient(t, dusNvidia, dusFuriosa)

	_, err := c.TriggerDriverRollback(context.Background(), "hybrid-node", "", "")
	if err == nil {
		t.Fatalf("벤더 둘 이상이면 --vendor 없이는 에러여야 함")
	}
	if !strings.Contains(err.Error(), "--vendor로 특정하세요") {
		t.Errorf("에러 메시지가 --vendor 를 안내하지 않음: %v", err)
	}
	if !strings.Contains(err.Error(), "furiosa") || !strings.Contains(err.Error(), VendorNvidia) {
		t.Errorf("에러 메시지에 후보 벤더 목록이 없음: %v", err)
	}
}

// TestTriggerDriverRollback_Model불일치면에러 는 --model 이 실제 DUS.spec.model 과
// 다르면(오타 등) 에러를 내는지 본다 — DUS 는 model 로 갈리지 않으므로 이 값은 선별
// 기준이 아니라 확인용이다.
func TestTriggerDriverRollback_Model불일치면에러(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1.cluster.local-rngd"},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "rngd-1.cluster.local", Vendor: "rngd", Model: "rngd"},
	}
	c := newTestClient(t, dus)

	_, err := c.TriggerDriverRollback(context.Background(), "rngd-1.cluster.local", "rngd", "warboy")
	if err == nil {
		t.Fatalf("model 불일치면 에러여야 함")
	}
	if !strings.Contains(err.Error(), `model="rngd"`) || !strings.Contains(err.Error(), `--model="warboy"`) {
		t.Errorf("에러 메시지가 실제·요청 model 을 모두 담지 않음: %v", err)
	}
}

// TestTriggerDriverRollback_TrackOnly면거절 은 TrackOnly 정책(예: Tenstorrent — installer
// 이미지가 없어 버전 관측만 하는 벤더) 이 걸린 대상을 거절하는지 본다.
// ensureTrackOnlyState(internal/controller/driver_upgrade_controller.go)가 매 reconcile
// 마다 status.state 를 Idle 로 강제하므로, 걸러내지 않으면 patch 는 성공하고 다음
// reconcile 에서 조용히 취소된다 — "롤백을 시작했다"는 거짓 신호가 된다(review 지적).
func TestTriggerDriverRollback_TrackOnly면거절(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "k8s-worker3-tenstorrent"},
		Spec: npuv1alpha1.DriverUpgradeStateSpec{
			NodeName: "k8s-worker3", Vendor: VendorTenstorrent, Model: "blackhole",
		},
	}
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "tenstorrent-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: VendorTenstorrent,
			Driver: npuv1alpha1.DriverSpec{TrackOnly: true},
		},
	}
	c := newTestClient(t, dus, dip)

	_, err := c.TriggerDriverRollback(context.Background(), "k8s-worker3", VendorTenstorrent, "")
	if err == nil {
		t.Fatalf("TrackOnly 대상은 거절해야 하는데 성공함")
	}
	if !strings.Contains(err.Error(), "TrackOnly") {
		t.Errorf("거절 사유가 TrackOnly 를 이름으로 대지 않음: %v", err)
	}
	if !strings.Contains(err.Error(), "tenstorrent-ds") {
		t.Errorf("거절 메시지가 어느 DriverInstallPolicy 때문인지 안 밝힘: %v", err)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: dus.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.State == npuv1alpha1.UpgradeStateRollback {
		t.Errorf("거절됐는데 status.state 가 Rollback 으로 patch 됨(거짓 신호)")
	}
}

// TestTriggerDriverRollback_RNGD는자기정책으로판정 은 furiosa 벤더 아래 warboy(TrackOnly
// 아님)·rngd(TrackOnly) 두 DriverInstallPolicy 가 공존할 때, rngd DUS 롤백이 원시
// vendor="furiosa" 가 같은 warboy 정책으로 잘못 판정되지 않고 자기 정책(rngd)의 TrackOnly로
// 정확히 거절되는지 본다 — resolveDIP 호출 전 vendorKeyForDIP 변환이 실제로 효과가 있는지
// 증명한다(그 변환이 없으면 이 시험은 에러 없이 통과해 버린다).
func TestTriggerDriverRollback_RNGD는자기정책으로판정(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "rngd-1.cluster.local-furiosa"},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "rngd-1.cluster.local", Vendor: "furiosa", Model: "rngd"},
	}
	warboy := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-warboy-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: "furiosa", Model: "warboy"},
	}
	rngd := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-rngd-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa", Model: "rngd",
			Driver: npuv1alpha1.DriverSpec{TrackOnly: true},
		},
	}
	c := newTestClient(t, dus, warboy, rngd)

	// vendor 생략 — 이 노드엔 DUS 가 하나뿐이라 resolveDUS 는 문제없이 찾는다. 이 시험의
	// 초점은 그 뒤 resolveDIP 가 어느 DriverInstallPolicy 를 고르느냐다.
	_, err := c.TriggerDriverRollback(context.Background(), "rngd-1.cluster.local", "", "")
	if err == nil {
		t.Fatalf("rngd 는 TrackOnly 라 거절해야 하는데 성공함(warboy 정책으로 잘못 판정됐을 가능성)")
	}
	if !strings.Contains(err.Error(), "furiosa-rngd-ds") {
		t.Errorf("자기 정책(furiosa-rngd-ds)이 아니라 다른 이름이 걸림: %v", err)
	}
	if strings.Contains(err.Error(), "furiosa-warboy-ds") {
		t.Errorf("warboy 정책이 잘못 끼어듦: %v", err)
	}
}

// TestPreviewDriverRollback_값만돌려주고Patch안함 은 PreviewDriverRollback 이
// TriggerDriverRollback 과 같은 요약(Node·Vendor·PreviousState·PreviousVersion)을
// 돌려주면서도 DUS 를 전혀 바꾸지 않는지 본다 — 확인 프롬프트가 뭔가를 확정하기 전에
// 안전하게 미리 볼 수 있어야 한다.
func TestPreviewDriverRollback_값만돌려주고Patch안함(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: rollbackTestDUSName},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: rollbackTestNode, Vendor: VendorNvidia, Model: "a30"},
		Status: npuv1alpha1.DriverUpgradeStateStatus{
			State: npuv1alpha1.UpgradeStateFailed, CurrentVersion: "580.173.02", PreviousVersion: "575.64.03",
		},
	}
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	c := newTestClient(t, dus, dip)

	preview, err := c.PreviewDriverRollback(context.Background(), rollbackTestNode, VendorNvidia, "")
	if err != nil {
		t.Fatalf("PreviewDriverRollback: %v", err)
	}
	if preview.Node != rollbackTestNode || preview.Vendor != VendorNvidia ||
		preview.PreviousState != npuv1alpha1.UpgradeStateFailed || preview.PreviousVersion != "575.64.03" {
		t.Errorf("미리보기 값 불일치: %+v", preview)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: dus.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.State != npuv1alpha1.UpgradeStateFailed {
		t.Errorf("미리보기만 했는데 status.state 가 바뀜: got=%s", got.Status.State)
	}
}

// TestTriggerDriverRollback_모델뭉개진혼재노드는거절 은 한 노드에 같은 벤더 아래 서로
// 다른 product 장치가 섞여(furiosa Warboy+RNGD 혼재) DUS.spec.model 이 "generic"(=
// internal/controller 의 modelUnknown)으로 뭉개진 경우를 다룬다. 이 값을 그대로
// vendorKeyForDIP 에 넘기면 "furiosa" 키가 나와 warboy(TrackOnly 아님) 정책과 매칭돼
// TrackOnly 검사를 조용히 통과해 버린다 — 이 시험이 걸러내지 못하면 CLI 는 어느
// 정책이 적용되는지 알 수 없는 채로 롤백을 받아들이게 된다.
func TestTriggerDriverRollback_모델뭉개진혼재노드는거절(t *testing.T) {
	dus := &npuv1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "hybrid-node1-furiosa"},
		Spec:       npuv1alpha1.DriverUpgradeStateSpec{NodeName: "hybrid-node1", Vendor: "furiosa", Model: "generic"},
	}
	warboy := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-warboy-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: "furiosa", Model: "warboy"},
	}
	rngd := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "furiosa-rngd-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "furiosa", Model: "rngd",
			Driver: npuv1alpha1.DriverSpec{TrackOnly: true},
		},
	}
	c := newTestClient(t, dus, warboy, rngd)

	_, err := c.TriggerDriverRollback(context.Background(), "hybrid-node1", "", "")
	if err == nil {
		t.Fatalf("model 이 뭉개진 대상은 거절해야 하는데 성공함(warboy 정책으로 잘못 판정됐을 가능성)")
	}
	if !strings.Contains(err.Error(), "hybrid-node1") || !strings.Contains(err.Error(), "furiosa") {
		t.Errorf("거절 메시지에 노드·벤더가 없음: %v", err)
	}

	var got npuv1alpha1.DriverUpgradeState
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: dus.Name}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.State == npuv1alpha1.UpgradeStateRollback {
		t.Errorf("거절됐는데 status.state 가 Rollback 으로 patch 됨(거짓 신호)")
	}
}

// previewUpgradeFromVersion 은 아래 미리보기 시험들이 공유하는 patch 이전 드라이버
// 버전이다(goconst — 리터럴 반복 대신 상수로 뺀다).
const previewUpgradeFromVersion = "580.126.09"

func TestPreviewVendorUpgrade_Patch없이요약만(t *testing.T) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	dip.Spec.Driver.Version = previewUpgradeFromVersion
	c := newTestClient(t, dip)
	got, err := c.PreviewVendorUpgrade(context.Background(), VendorNvidia, "580.178.04", UpgradeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.PolicyName != "nvidia-gpu-ds" || got.NoChange ||
		got.PreviousVersion != previewUpgradeFromVersion || got.NewVersion != "580.178.04" {
		t.Fatalf("preview = %+v", got)
	}
	var after npuv1alpha1.DriverInstallPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "nvidia-gpu-ds"}, &after); err != nil {
		t.Fatal(err)
	}
	if after.Spec.Driver.Version != previewUpgradeFromVersion {
		t.Fatalf("미리보기가 patch 했다: %s", after.Spec.Driver.Version)
	}
}

func TestPreviewVendorEnabled_Patch없이요약만(t *testing.T) {
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp"}}
	ncp.Spec.Rebellions.Enabled = true
	c := newTestClient(t, ncp)
	got, err := c.PreviewVendorEnabled(context.Background(), VendorRebellions, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PreviousEnabled || got.NewEnabled || got.NoChange {
		t.Fatalf("preview = %+v", got)
	}
}

func TestUpgradeVendor_Force는어노테이션을남기지않는다(t *testing.T) {
	dip := &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "nvidia-gpu-ds"},
		Spec:       npuv1alpha1.DriverInstallPolicySpec{Vendor: VendorNvidia, Model: "generic"},
	}
	dip.Spec.Driver.Version = previewUpgradeFromVersion
	ncp := &npuv1alpha1.NPUClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "ncp"}}
	c := newTestClient(t, dip, ncp)
	opts := UpgradeOptions{Force: true}
	if _, err := c.UpgradeVendor(context.Background(), VendorNvidia, "580.178.04", opts); err != nil {
		t.Fatal(err)
	}
	var after npuv1alpha1.NPUClusterPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "ncp"}, &after); err != nil {
		t.Fatal(err)
	}
	if _, has := after.Annotations["npu.ai/force-upgrade"]; has {
		t.Fatal("읽는 코드가 없는 어노테이션을 남겼다")
	}
	var dipAfter npuv1alpha1.DriverInstallPolicy
	_ = c.c.Get(context.Background(), client.ObjectKey{Name: "nvidia-gpu-ds"}, &dipAfter)
	if dipAfter.Spec.UpgradePolicy == nil || !dipAfter.Spec.UpgradePolicy.ForceUpgrade {
		t.Fatal("--force 의 실제 의미(upgradePolicy.forceUpgrade)가 사라졌다")
	}
}

// newTrackOnlyRebellionsClient 는 설치 경로 없이 버전만 추적하는 Rebellions 정책(rebellions-atom-ds) 하나만 가진
// 시험용 Client 다. upgrade 거절 시험들이 같은 정책을 쓴다.
func newTrackOnlyRebellionsClient(t *testing.T) *Client {
	t.Helper()
	return newTestClient(t, &npuv1alpha1.DriverInstallPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "rebellions-atom-ds"},
		Spec: npuv1alpha1.DriverInstallPolicySpec{
			Vendor: "rebellions", Model: "atom",
			Driver: npuv1alpha1.DriverSpec{Mode: "job", TrackOnly: true, VersionSource: "Host"},
		},
	})
}

// assertTrackOnlyRefusal 은 err 가 TrackOnly 정책 때문인 거절이고 저장된 정책의 버전이 바뀌지 않았는지 본다.
func assertTrackOnlyRefusal(t *testing.T, c *Client, err error) {
	t.Helper()
	if !strings.Contains(err.Error(), "TrackOnly") || !strings.Contains(err.Error(), "rebellions-atom-ds") {
		t.Errorf("거절 사유가 TrackOnly 와 정책 이름을 대지 않음: %v", err)
	}
	var got npuv1alpha1.DriverInstallPolicy
	if err := c.c.Get(context.Background(), client.ObjectKey{Name: "rebellions-atom-ds"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Driver.Version != "" {
		t.Errorf("거절했는데 spec.driver.version 이 %q 로 바뀜", got.Spec.Driver.Version)
	}
}

// TestUpgradeVendor_TrackOnly면거절 은 설치 경로가 없는 정책(Rebellions)에 대한 upgrade 를 거절하는지 본다.
// 거절하지 않으면 patch 는 성공하지만 아무 일도 일어나지 않고, 확인 문구는 설치·재부팅을 경고해 거짓 신호가 된다.
func TestUpgradeVendor_TrackOnly면거절(t *testing.T) {
	c := newTrackOnlyRebellionsClient(t)

	_, err := c.UpgradeVendor(context.Background(), "rebellions", "3.1.0", UpgradeOptions{})
	if err == nil {
		t.Fatalf("TrackOnly 정책에 대한 upgrade 는 거절해야 하는데 성공함")
	}
	assertTrackOnlyRefusal(t, c, err)
}

// TestPreviewVendorUpgrade_TrackOnly면거절 은 확인 프롬프트 앞의 미리보기도 TrackOnly 정책을 거절하는지 본다.
// 미리보기가 통과하면 --yes 없는 upgrade 는 설치·재부팅 경고를 먼저 보이고 확인 뒤에야 거절해 거짓 신호가 된다.
func TestPreviewVendorUpgrade_TrackOnly면거절(t *testing.T) {
	c := newTrackOnlyRebellionsClient(t)

	_, err := c.PreviewVendorUpgrade(context.Background(), "rebellions", "3.1.0", UpgradeOptions{})
	if err == nil {
		t.Fatalf("TrackOnly 정책에 대한 미리보기는 거절해야 하는데 성공함")
	}
	assertTrackOnlyRefusal(t, c, err)
}
