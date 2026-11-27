// ============================================================
// lease.go: 노드 단위 Lease
// 상세: coordination.k8s.io/v1 Lease 로 "같은 노드를 두 프로세스가 동시에 만지지 않는다" 를 강제한다.
// 기존 노드 annotation owner-lock 과 달리 만료가 있어, 소유자가 사라져도 노드가 영구히 잠기지 않는다.
// 생성일: 2026-08-01 | 수정일: 2026-10-11
// ============================================================
package operation

import (
	"context"
	"fmt"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NodeLeaseName 은 노드 Lease 의 이름이다.
//
// ponytail: 노드 단위 Lease 하나로 시작한다. 장치 단위 병렬 적용이 필요해지면 이름을
// 장치 키 기준으로 쪼갠다 — 지금은 Admit 이 자원 키로 이미 걸러 주므로 노드 단위로 충분하다.
func NodeLeaseName(node string) string { return "kcloud-node-" + node }

// LeaseManager 는 노드 Lease 를 잡고 놓는다.
type LeaseManager struct {
	Client    client.Client
	Namespace string
	// Holder 는 이 프로세스의 기본 식별자다(Acquire 인자로 덮어쓸 수 있다).
	Holder   string
	Duration time.Duration
	// Now 는 시계 seam 이다(nil 이면 time.Now).
	Now func() time.Time
}

func (m *LeaseManager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

func (m *LeaseManager) duration() time.Duration {
	if m.Duration <= 0 {
		return 60 * time.Second
	}
	return m.Duration
}

// Acquire 는 노드 Lease 를 잡거나 갱신한다.
//
// 이미 자기 것이면 갱신하고 true. 남의 것이고 아직 살아 있으면 false(에러 아님 — 대기는 정상
// 상태다). 남의 것인데 만료됐으면 회수한다.
func (m *LeaseManager) Acquire(ctx context.Context, node, holder string) (bool, metav1.Time, error) {
	now := m.now()
	renew := metav1.NewMicroTime(now)
	seconds := int32(m.duration().Seconds())
	expiry := metav1.NewTime(now.Add(m.duration()))

	var lease coordv1.Lease
	key := types.NamespacedName{Name: NodeLeaseName(node), Namespace: m.Namespace}
	err := m.Client.Get(ctx, key, &lease)
	switch {
	case apierrors.IsNotFound(err):
		lease = coordv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
			Spec: coordv1.LeaseSpec{
				HolderIdentity:       &holder,
				LeaseDurationSeconds: &seconds,
				AcquireTime:          &renew,
				RenewTime:            &renew,
			},
		}
		if cerr := m.Client.Create(ctx, &lease); cerr != nil {
			if apierrors.IsAlreadyExists(cerr) {
				return false, metav1.Time{}, nil // 경합에서 졌다 — 대기.
			}
			return false, metav1.Time{}, fmt.Errorf("lease: create %s: %w", key.Name, cerr)
		}
		return true, expiry, nil
	case err != nil:
		return false, metav1.Time{}, fmt.Errorf("lease: get %s: %w", key.Name, err)
	}

	if cur := lease.Spec.HolderIdentity; cur != nil && *cur != holder && !leaseExpired(&lease, now) {
		return false, metav1.Time{}, nil
	}
	lease.Spec.HolderIdentity = &holder
	lease.Spec.LeaseDurationSeconds = &seconds
	lease.Spec.RenewTime = &renew
	if lease.Spec.AcquireTime == nil {
		lease.Spec.AcquireTime = &renew
	}
	if uerr := m.Client.Update(ctx, &lease); uerr != nil {
		if apierrors.IsConflict(uerr) {
			return false, metav1.Time{}, nil // 다른 프로세스가 먼저 썼다 — 다음 pass 에 다시 시도.
		}
		return false, metav1.Time{}, fmt.Errorf("lease: update %s: %w", key.Name, uerr)
	}
	return true, expiry, nil
}

// Release 는 자기 Lease 만 놓는다. 남의 것이면 아무것도 하지 않는다.
func (m *LeaseManager) Release(ctx context.Context, node, holder string) error {
	var lease coordv1.Lease
	key := types.NamespacedName{Name: NodeLeaseName(node), Namespace: m.Namespace}
	if err := m.Client.Get(ctx, key, &lease); err != nil {
		return client.IgnoreNotFound(err)
	}
	if cur := lease.Spec.HolderIdentity; cur == nil || *cur != holder {
		return nil
	}
	return client.IgnoreNotFound(m.Client.Delete(ctx, &lease,
		client.PropagationPolicy(metav1.DeletePropagationBackground)))
}

// leaseExpired 는 갱신 시각 + 유효기간이 지났는지다. 갱신 시각이나 유효기간이 없으면 만료로 본다 —
// 언제까지 유효한지 모르는 Lease 로 노드를 영구히 잠글 수는 없다.
func leaseExpired(l *coordv1.Lease, now time.Time) bool {
	if l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return true
	}
	return !now.Before(l.Spec.RenewTime.Add(time.Duration(*l.Spec.LeaseDurationSeconds) * time.Second))
}
