// ============================================================
// policy_test.go: 공유·분할 정책 조립 시험
// 상세: 화면 어휘를 AcceleratorPartitionPolicy 필드로 옮기는 번역과 거절 경로를 본다.
//       클러스터에 붙지 않는다 — 조립만 검사한다.
// 생성일: 2026-08-11
// ============================================================

package npuctl

import (
	"testing"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

func TestBuildPolicy_공유는_시분할_필드로(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p1", Vendor: "nvidia", NodeName: "k8s-worker1",
		Intent: "shared", Sharing: "timeSliced", Replicas: 4,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if got.Spec.Sharing == nil || got.Spec.Sharing.Mode != npuv1alpha1.SharingModeTimeSliced {
		t.Fatalf("sharing.mode 가 timeSliced 여야 한다: %+v", got.Spec.Sharing)
	}
	if got.Spec.Sharing.TimeSlicing == nil || got.Spec.Sharing.TimeSlicing.Replicas != 4 {
		t.Fatalf("timeSlicing.replicas 4 여야 한다: %+v", got.Spec.Sharing)
	}
	if len(got.Spec.Layout) != 0 {
		t.Fatalf("공유 단독이면 layout 이 비어야 한다: %+v", got.Spec.Layout)
	}
	if got.Spec.NodeSelector["kubernetes.io/hostname"] != "k8s-worker1" {
		t.Fatalf("nodeSelector 가 노드를 지목해야 한다: %+v", got.Spec.NodeSelector)
	}
}

func TestBuildPolicy_다중프로세스는_mps_필드로(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p2", Vendor: "nvidia", NodeName: "n1",
		Intent: "shared", Sharing: "mps", Replicas: 2,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if got.Spec.Sharing.Mode != npuv1alpha1.SharingModeMPS || got.Spec.Sharing.MPS == nil {
		t.Fatalf("mps 필드로 가야 한다: %+v", got.Spec.Sharing)
	}
	if got.Spec.Sharing.TimeSlicing != nil {
		t.Fatalf("mps 면 timeSlicing 을 채우지 않는다: %+v", got.Spec.Sharing)
	}
}

func TestBuildPolicy_분할은_layout_으로(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p3", Vendor: "furiosa", NodeName: "rngd-1",
		Intent: "partitioned", Profile: "2core.12gb", CountPerDevice: 2,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if len(got.Spec.Layout) != 1 || got.Spec.Layout[0].Profile != "2core.12gb" {
		t.Fatalf("layout 에 프로파일이 실려야 한다: %+v", got.Spec.Layout)
	}
	if got.Spec.Layout[0].CountPerDevice != 2 {
		t.Fatalf("countPerDevice 2 여야 한다: %+v", got.Spec.Layout[0])
	}
	if got.Spec.Sharing != nil {
		t.Fatalf("분할 단독이면 sharing 을 만들지 않는다: %+v", got.Spec.Sharing)
	}
}

func TestBuildPolicy_분할후공유는_둘_다(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p4", Vendor: "nvidia", NodeName: "n1",
		Intent: "partitioned-shared", Profile: "1g.6gb", CountPerDevice: 4,
		Sharing: "timeSliced", Replicas: 2,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if len(got.Spec.Layout) != 1 || got.Spec.Sharing == nil {
		t.Fatalf("layout 과 sharing 이 둘 다 있어야 한다: %+v", got.Spec)
	}
}

func TestBuildPolicy_독점은_sharing_을_만들지_않는다(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p5", Vendor: "nvidia", NodeName: "n1", Intent: "exclusive",
		Profile: "1g.6gb", CountPerDevice: 1,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if got.Spec.Sharing != nil {
		t.Fatalf("exclusive 는 sharing 필드를 만들지 않는다: %+v", got.Spec.Sharing)
	}
}

// 화면의 첫 선택지다. sharing 을 만들지 않고 두면 빈 정책이 되어 반드시 거절되므로,
// 노드를 독점으로 되돌리는 정당한 조작이 화면에서 아예 불가능해진다.
func TestBuildPolicy_독점_의도는_exclusive_모드로(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p9", Vendor: "nvidia", NodeName: "n1", Intent: "exclusive",
	})
	if err != nil {
		t.Fatalf("독점 요청은 성립해야 한다: %v", err)
	}
	if got.Spec.Sharing == nil || got.Spec.Sharing.Mode != npuv1alpha1.SharingModeExclusive {
		t.Fatalf("sharing.mode 가 exclusive 여야 한다: %+v", got.Spec.Sharing)
	}
	if got.Spec.Sharing.TimeSlicing != nil || got.Spec.Sharing.MPS != nil {
		t.Fatalf("독점은 하위 스펙을 만들지 않는다: %+v", got.Spec.Sharing)
	}
	if len(got.Spec.Layout) != 0 {
		t.Fatalf("독점 단독이면 layout 이 비어야 한다: %+v", got.Spec.Layout)
	}
}

// 아무것도 안 하는 정책을 만들면 상태기계가 돌 대상이 없다 — 조립 단계에서 막는다.
// intent 가 exclusive 면 sharing.mode 로 표현되므로 빈 정책이 아니다. 빈 정책은 화면이
// 공유 계열을 골라 놓고 구현 방식을 확정하지 않은 경우처럼 아무 축도 안 채운 요청이다.
func TestBuildPolicy_빈_정책은_거절(t *testing.T) {
	if _, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p6", Vendor: "nvidia", NodeName: "n1", Intent: "shared",
	}); err == nil {
		t.Fatal("layout 도 sharing 도 없으면 에러여야 한다")
	}
}

// 벤더 enum 은 furiosa;nvidia 뿐이다. 다른 값을 서버가 받아 주면 CRD 거절이 화면에
// 도달하기 전 조립까지는 통과해 원인 지목이 흐려진다.
func TestBuildPolicy_지원하지_않는_벤더_거절(t *testing.T) {
	if _, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p7", Vendor: "tenstorrent", NodeName: "k8s-worker3",
		Intent: "shared", Sharing: "timeSliced", Replicas: 2,
	}); err == nil {
		t.Fatal("furiosa/nvidia 가 아니면 에러여야 한다")
	}
}

// 이름이 없으면 apiserver 가 거절하지만 그 거절문은 사용자에게 불친절하다.
func TestBuildPolicy_이름_없으면_거절(t *testing.T) {
	if _, err := BuildPartitionPolicy(PolicyRequest{
		Vendor: "nvidia", NodeName: "n1", Intent: "shared", Sharing: "timeSliced", Replicas: 2,
	}); err == nil {
		t.Fatal("이름이 없으면 에러여야 한다")
	}
}

// 공유 계열인데 복제수가 2 미만이면 공유가 아니다.
func TestBuildPolicy_공유_복제수_하한(t *testing.T) {
	if _, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p8", Vendor: "nvidia", NodeName: "n1",
		Intent: "shared", Sharing: "timeSliced", Replicas: 1,
	}); err == nil {
		t.Fatal("공유 계열 복제수 1 은 에러여야 한다")
	}
}

// AcceleratorPartitionPolicy 는 cluster-scoped 다(CRD scope: Cluster). 네임스페이스를
// 채우면 apiserver 가 지워 버리고, 화면은 있지도 않은 네임스페이스 축이 있다고 믿는다.
func TestBuildPolicy_네임스페이스는_비어_있다(t *testing.T) {
	got, err := BuildPartitionPolicy(PolicyRequest{
		Name: "p9", Vendor: "nvidia", NodeName: "n1",
		Intent: "shared", Sharing: "timeSliced", Replicas: 2,
	})
	if err != nil {
		t.Fatalf("에러 없어야 한다: %v", err)
	}
	if got.Namespace != "" {
		t.Fatalf("cluster-scoped 라 네임스페이스가 비어야 한다: %q", got.Namespace)
	}
}
