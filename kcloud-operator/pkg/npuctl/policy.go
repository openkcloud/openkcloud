// ============================================================
// policy.go: 공유·분할 정책 조립과 생성·삭제
// 상세: 화면 어휘(intent)를 AcceleratorPartitionPolicy 필드로 옮긴다. 적용 가능 여부는
//       dry-run 으로 기존 validating webhook 이 판정하며 이 파일은 판정을 복제하지 않는다.
// 생성일: 2026-08-11
// ============================================================

package npuctl

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// PolicyRequest 는 마법사가 고른 것만 담는다. 필드를 늘리기 전에 콘솔 정책 설계를
// 먼저 정한다.
type PolicyRequest struct {
	Name     string `json:"name"`
	Vendor   string `json:"vendor"`
	NodeName string `json:"nodeName"`
	// Intent: exclusive|shared|partitioned|partitioned-shared (화면 어휘)
	Intent string `json:"intent"`
	// Sharing: timeSliced|mps. 화면이 auto 를 확정해 보낸다 — 여기에 auto 는 없다.
	Sharing        string `json:"sharing,omitempty"`
	Replicas       int32  `json:"replicas,omitempty"`
	Profile        string `json:"profile,omitempty"`
	CountPerDevice int32  `json:"countPerDevice,omitempty"`
}

var errEmptyPolicy = errors.New("layout 도 sharing 도 없다 — 아무것도 하지 않는 정책이다")

// BuildPartitionPolicy 는 화면 어휘를 정책 필드로 옮긴다. 번역표는 설계 문서 이 정본이다.
// AcceleratorPartitionPolicy 는 cluster-scoped 라 네임스페이스를 정하지 않는다.
func BuildPartitionPolicy(req PolicyRequest) (*npuv1alpha1.AcceleratorPartitionPolicy, error) {
	if req.Name == "" {
		return nil, errors.New("정책 이름이 없다")
	}
	if req.Vendor != "furiosa" && req.Vendor != "nvidia" {
		return nil, fmt.Errorf("정책 대상이 아닌 벤더다: %s (furiosa 또는 nvidia)", req.Vendor)
	}
	if req.NodeName == "" {
		return nil, errors.New("대상 노드가 없다")
	}
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name},
		Spec: npuv1alpha1.AcceleratorPartitionPolicySpec{
			Vendor:       req.Vendor,
			NodeSelector: map[string]string{"kubernetes.io/hostname": req.NodeName},
		},
	}
	if req.Profile != "" {
		if req.CountPerDevice < 1 {
			return nil, errors.New("프로파일을 골랐으면 장치당 인스턴스 수가 1 이상이어야 한다")
		}
		acpp.Spec.Layout = []npuv1alpha1.PartitionLayout{
			{Profile: req.Profile, CountPerDevice: req.CountPerDevice},
		}
	}
	switch req.Sharing {
	case "":
		// 독점 요청이면 sharing.mode 를 exclusive 로 적는다(설계 문서 번역표). 필드를
		// 만들지 않고 두면 layout 도 sharing 도 없는 정책이 되어 errEmptyPolicy 로 거절되는데,
		// 그러면 화면의 첫 선택지("한 작업이 통째로 씁니다")가 반드시 실패한다.
		// 노드를 독점으로 되돌리는 것은 정당한 관리 조작이고 CRD enum 에도 값이 있다.
		// layout 이 이미 있으면(분할 계열) 그 자체로 할 일이 있으므로 sharing 을 덧붙이지
		// 않는다 — 분할된 장치를 "독점" 으로도 표시하면 두 축이 서로 다른 말을 한다.
		if req.Intent == "exclusive" && len(acpp.Spec.Layout) == 0 {
			acpp.Spec.Sharing = &npuv1alpha1.SharingSpec{Mode: npuv1alpha1.SharingModeExclusive}
		}
	case "timeSliced":
		if req.Replicas < 2 {
			return nil, errors.New("공유는 복제수가 2 이상이어야 한다")
		}
		acpp.Spec.Sharing = &npuv1alpha1.SharingSpec{
			Mode:        npuv1alpha1.SharingModeTimeSliced,
			TimeSlicing: &npuv1alpha1.TimeSlicingSpec{Replicas: req.Replicas},
		}
	case "mps":
		if req.Replicas < 2 {
			return nil, errors.New("공유는 복제수가 2 이상이어야 한다")
		}
		acpp.Spec.Sharing = &npuv1alpha1.SharingSpec{
			Mode: npuv1alpha1.SharingModeMPS,
			MPS:  &npuv1alpha1.MPSSpec{Replicas: req.Replicas},
		}
	default:
		return nil, fmt.Errorf("모르는 공유 방식이다: %s", req.Sharing)
	}
	if len(acpp.Spec.Layout) == 0 && acpp.Spec.Sharing == nil {
		return nil, errEmptyPolicy
	}
	return acpp, nil
}

// CreatePartitionPolicy 는 정책을 만든다. dryRun 이면 webhook 만 돌고 남지 않는다.
// 돌려주는 문자열은 만들어진(또는 만들어질) 정책 이름이다.
func (c *Client) CreatePartitionPolicy(ctx context.Context, req PolicyRequest, dryRun bool) (string, error) {
	acpp, err := BuildPartitionPolicy(req)
	if err != nil {
		return "", err
	}
	var opts []client.CreateOption
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	if err := c.c.Create(ctx, acpp, opts...); err != nil {
		return "", err
	}
	return acpp.Name, nil
}

// DeletePartitionPolicy 는 정책을 지운다. 이미 없으면 성공으로 본다 — 되돌리기를 두 번
// 눌러도 안전해야 한다.
func (c *Client) DeletePartitionPolicy(ctx context.Context, name string) error {
	acpp := &npuv1alpha1.AcceleratorPartitionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := c.c.Delete(ctx, acpp); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
