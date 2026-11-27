// ============================================================
// workload.go: AcceleratorWorkload 생성·삭제
// 상세: 콘솔 배포 화면이 쓰는 두 메서드. 조립·검증은 CRD 스키마와 webhook 몫이고
//       여기서는 CR 을 그대로 만들고 지우기만 한다(정책 쓰기 경로와 같은 분업).
// 생성일: 2026-10-06
// ============================================================

package npuctl

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// CreateAcceleratorWorkload 는 AcceleratorWorkload 를 만든다. admission(webhook) 거절은
// 그대로 오류로 돌려준다 — 호출자가 사용자에게 보여 줄 문장이 그 안에 있다.
func (c *Client) CreateAcceleratorWorkload(ctx context.Context, aw *npuv1alpha1.AcceleratorWorkload) error {
	return c.c.Create(ctx, aw)
}

// DeleteAcceleratorWorkload 는 이름으로 지운다. 없으면 NotFound 를 그대로 돌려준다(호출자가 구분).
func (c *Client) DeleteAcceleratorWorkload(ctx context.Context, namespace, name string) error {
	aw := &npuv1alpha1.AcceleratorWorkload{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	return c.c.Delete(ctx, aw)
}
