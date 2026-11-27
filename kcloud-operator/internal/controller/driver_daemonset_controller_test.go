// ============================================================
// driver_daemonset_controller_test.go: renderDriverDaemonSet 단위 테스트
// 상세: operator pod anti-affinity, PreStop timeout 30s,
// TerminationGracePeriodSeconds=60s 가 DS spec 에 들어 있는지, 롤백 보류 중 이미지 유지 검증
// 생성일: 2026-04-27 | 수정일: 2026-10-14
// ============================================================

package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "kcloud-operator/api/v1alpha1"
)

// renderTestPolicy 는 renderDriverDaemonSet 입력용 최소 DIP 를 만든다.
func renderTestPolicy(vendor, model, version string) *v1alpha1.DriverInstallPolicy {
	return &v1alpha1.DriverInstallPolicy{
		Spec: v1alpha1.DriverInstallPolicySpec{
			Vendor: vendor,
			Model:  model,
			Driver: v1alpha1.DriverSpec{
				Version: version,
				Image:   "registry.example/driver:" + version,
				Mode:    "daemonset",
			},
		},
	}
}

// hasFuriosaAuthMount 는 DS pod spec 에 furiosa-auth secret 볼륨/마운트가 존재하는지 반환한다.
func hasFuriosaAuthMount(ds *appsv1.DaemonSet) bool {
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "furiosa-auth" {
			return true
		}
	}
	return false
}

// TestRenderDriverDaemonSet_FuriosaAuthWarboyOnly 는 furiosa-apt-auth secret 마운트가
// Warboy(사설 repo 인증 필수)에만 부착되고 RNGD(공개 repo)에는 부착되지 않음을 검증한다.
// 회귀 시나리오: RNGD 가 warboy 전제를 상속받으면 secret 부재 클러스터에서 파드 스케줄이
// 실패한다. Warboy 는 하위호환으로 마운트 유지.
func TestRenderDriverDaemonSet_FuriosaAuthWarboyOnly(t *testing.T) {
	cases := []struct {
		name      string
		vendor    string
		model     string
		wantMount bool
	}{
		{"warboy has auth", "furiosa", "warboy", true},
		{"furiosa empty model = warboy", "furiosa", "", true},
		{"rngd no auth (public repo)", "furiosa", "rngd", false},
		{"rngd mixed case no auth", "Furiosa", "RNGD", false},
		{"nvidia no auth", "nvidia", "generic", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := renderDriverDaemonSet(renderTestPolicy(tc.vendor, tc.model, "1.0.0"))
			if got := hasFuriosaAuthMount(ds); got != tc.wantMount {
				t.Errorf("furiosa-auth 마운트=%v, want %v (vendor=%s model=%s)",
					got, tc.wantMount, tc.vendor, tc.model)
			}
		})
	}
}

// TestRenderDriverDaemonSet_AntiAffinityWithOperator 는 driver pod 가 operator pod 와
// 같은 노드에 spawn 되지 않도록 anti-affinity 가 적용되었는지 검증한다.
// 회귀 시나리오: operator pod 가 driver pod 와 같은 노드에 떠 있다가 노드 reboot
// 시 둘 다 사망 → driver-upgrading 라벨이 영구 stuck 되는 경로를 차단.
func TestRenderDriverDaemonSet_AntiAffinityWithOperator(t *testing.T) {
	ds := renderDriverDaemonSet(renderTestPolicy("nvidia", "generic", "590.48.01"))

	if ds.Spec.Template.Spec.Affinity == nil ||
		ds.Spec.Template.Spec.Affinity.PodAntiAffinity == nil {
		t.Fatalf("PodAntiAffinity 미설정 — operator pod 와의 노드 공유 차단 안 됨")
	}
	preferred := ds.Spec.Template.Spec.Affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if len(preferred) == 0 {
		t.Fatalf("PreferredDuringScheduling 항목 0 — anti-affinity 가 아무 효과 없음")
	}
	term := preferred[0].PodAffinityTerm
	if term.TopologyKey != "kubernetes.io/hostname" {
		t.Errorf("TopologyKey=%q, expected kubernetes.io/hostname", term.TopologyKey)
	}
	if term.LabelSelector == nil ||
		term.LabelSelector.MatchLabels["app.kubernetes.io/name"] != "kcloud-operator" {
		t.Errorf("operator label selector 미일치: %+v", term.LabelSelector)
	}
	if term.LabelSelector.MatchLabels["app.kubernetes.io/component"] != "controller" {
		t.Errorf("operator component label 미일치: %+v", term.LabelSelector.MatchLabels)
	}
}

// TestRenderDriverDaemonSet_TerminationGrace 는 PreStop 의 rmmod hang 시에도
// kubelet 이 일정 시간 안에 강제 종료할 수 있도록 grace 가 설정되었는지 검증한다.
func TestRenderDriverDaemonSet_TerminationGrace(t *testing.T) {
	ds := renderDriverDaemonSet(renderTestPolicy("nvidia", "generic", "590.48.01"))

	if ds.Spec.Template.Spec.TerminationGracePeriodSeconds == nil {
		t.Fatalf("TerminationGracePeriodSeconds 미설정 — PreStop hang 시 무한 대기 위험")
	}
	got := *ds.Spec.Template.Spec.TerminationGracePeriodSeconds
	if got < 30 || got > 120 {
		t.Errorf("TerminationGracePeriodSeconds=%d (기대: 30~120)", got)
	}
}

// TestRenderDriverDaemonSet_PreStopTimeout 는 PreStop 명령이 timeout 으로 감싸여
// rmmod hang 시 자동 종료되는지 검증한다. nvidia/furiosa/RNGD 벤더별로 확인.
func TestRenderDriverDaemonSet_PreStopTimeout(t *testing.T) {
	cases := []struct {
		vendor string
		model  string
	}{
		{"nvidia", "generic"},
		{"furiosa", "warboy"},
		{"furiosa", "rngd"},
	}
	for _, tc := range cases {
		t.Run(tc.vendor+"/"+tc.model, func(t *testing.T) {
			ds := renderDriverDaemonSet(renderTestPolicy(tc.vendor, tc.model, "1.0.0"))
			if len(ds.Spec.Template.Spec.Containers) == 0 {
				t.Fatalf("driver 컨테이너 없음")
			}
			lc := ds.Spec.Template.Spec.Containers[0].Lifecycle
			if lc == nil || lc.PreStop == nil || lc.PreStop.Exec == nil {
				t.Fatalf("PreStop exec 핸들러 미설정")
			}
			cmd := strings.Join(lc.PreStop.Exec.Command, " ")
			if !strings.Contains(cmd, "timeout 30") {
				t.Errorf("PreStop 에 timeout 미적용: %q", cmd)
			}
			// rmmod 실패가 PreStop 자체 실패로 전파되어 grace 종료되지 않게 || true 보장
			if !strings.Contains(cmd, "|| true") {
				t.Errorf("PreStop 종료 코드 무시 (|| true) 누락: %q", cmd)
			}
		})
	}
}

const (
	heldImage   = "registry.example/driver:580.126.09" // 상태 기계가 롤백으로 패치한 이미지
	policyImage = "registry.example/driver:590.48.01"  // renderTestPolicy 가 만드는 정책 이미지
)

// heldDSFixture 는 롤백 이미지로 패치된 드라이버 DS 와 그 정책, 그 둘을 담은 reconciler 다.
func heldDSFixture(t *testing.T) (*DriverDaemonSetReconciler, client.Client, *appsv1.DaemonSet, int64) {
	t.Helper()
	pol := renderTestPolicy("nvidia", "a30", "590.48.01")
	pol.Name = "nvidia-a30"
	ds := renderDriverDaemonSet(pol)
	ds.Spec.Template.Spec.Containers[0].Image = heldImage
	s := newTestScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pol, ds).Build()
	var stored v1alpha1.DriverInstallPolicy
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(pol), &stored); err != nil {
		t.Fatalf("정책 조회 실패: %v", err)
	}
	return &DriverDaemonSetReconciler{Client: c, Scheme: s, Recorder: record.NewFakeRecorder(16)}, c, ds, stored.Generation
}

// heldDUSFor 는 롤백을 기록한 DUS 를 만들어 저장한다.
func heldDUSFor(t *testing.T, c client.Client, vendor, model string, gen int64) {
	t.Helper()
	dus := &v1alpha1.DriverUpgradeState{
		ObjectMeta: metav1.ObjectMeta{Name: "w1-" + vendor},
		Spec:       v1alpha1.DriverUpgradeStateSpec{NodeName: "w1", Vendor: vendor, Model: model},
		Status: v1alpha1.DriverUpgradeStateStatus{State: v1alpha1.UpgradeStateIdle,
			RolledBackVersion: "590.48.01", RolledBackGeneration: gen},
	}
	if err := c.Create(context.Background(), dus); err != nil {
		t.Fatalf("DUS 생성 실패: %v", err)
	}
}

func reconcileAndImage(t *testing.T, r *DriverDaemonSetReconciler, c client.Client, ds *appsv1.DaemonSet) string {
	t.Helper()
	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("Reconcile 실패: %v", err)
	}
	var got appsv1.DaemonSet
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(ds), &got); err != nil {
		t.Fatalf("DS 조회 실패: %v", err)
	}
	return got.Spec.Template.Spec.Containers[0].Image
}

// 롤백 보류 중(같은 정책 generation)에는 DS 이미지를 정책 이미지로 되돌리지 않는다. 되돌리면
// 롤백한 노드의 다음 파드가 실패한 이미지로 기동하고, 그 버전이 로드되면 보류가 경고 없이 풀린다.
func TestDriverDS_KeepsImageWhileRollbackHeld(t *testing.T) {
	r, c, ds, gen := heldDSFixture(t)
	heldDUSFor(t, c, "nvidia", "a30", gen)
	if img := reconcileAndImage(t, r, c, ds); img != heldImage {
		t.Errorf("DS image=%q, want %q", img, heldImage)
	}
}

// 정책 spec 이 바뀌면(generation 증가) 보류는 풀리는 중이므로 정책 이미지로 렌더한다.
func TestDriverDS_RerendersAfterPolicyChange(t *testing.T) {
	r, c, ds, gen := heldDSFixture(t)
	heldDUSFor(t, c, "nvidia", "a30", gen-1)
	if img := reconcileAndImage(t, r, c, ds); img != policyImage {
		t.Errorf("DS image=%q, want %q", img, policyImage)
	}
}

// 다른 벤더 DUS 의 보류는 이 정책의 DS 이미지를 붙잡지 않는다.
func TestDriverDS_OtherVendorHoldDoesNotPin(t *testing.T) {
	r, c, ds, gen := heldDSFixture(t)
	heldDUSFor(t, c, "furiosa", "warboy", gen)
	if img := reconcileAndImage(t, r, c, ds); img != policyImage {
		t.Errorf("DS image=%q, want %q", img, policyImage)
	}
}
