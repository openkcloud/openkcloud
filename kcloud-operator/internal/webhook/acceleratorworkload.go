// ============================================================
// acceleratorworkload.go: AcceleratorWorkload validating webhook (추상 → 벤더 리소스 변환 검증)
// 상세: kubectl apply 에 즉시 거절 사유를 돌려주기 위한 경로다. 판정 자체는 컨트롤러와 동일한
// intent.Translate 를 쓴다 — failurePolicy=Ignore 라 최종 권위가 아니며, 권위 판정은 컨트롤러가 status 로 남긴다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================

package webhook

import (
	"context"
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// AWValidator 는 AcceleratorWorkload 를 검증한다. 클러스터 상태를 읽어야 하므로 client 를 받는다.
type AWValidator struct {
	Client client.Client
}

func (v *AWValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	w, err := validateAcceleratorWorkload(ctx, v.Client, obj)
	return w, recordRejection("AcceleratorWorkload", err)
}

// ValidateUpdate 는 spec 이 바뀐 업데이트만 검증한다. 매번 다시 번역하면, 클래스가 지워졌거나
// 클러스터가 나빠져 번역이 더는 성립하지 않는 순간부터 라벨 하나 붙이는 것조차 거절된다
// (예: quiesce opt-in). DELETE 는 검증하지 않으므로 그때의 유일한 복구책이
// delete-and-recreate 가 되어 버린다. 덤으로 metadata 만 바뀐 쓰기의 헛번역도 사라진다.
func (v *AWValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldAW, okOld := oldObj.(*npuv1alpha1.AcceleratorWorkload)
	newAW, okNew := newObj.(*npuv1alpha1.AcceleratorWorkload)
	if okOld && okNew && apiequality.Semantic.DeepEqual(oldAW.Spec, newAW.Spec) {
		return nil, nil
	}
	w, err := validateAcceleratorWorkload(ctx, v.Client, newObj)
	return w, recordRejection("AcceleratorWorkload", err)
}

func (v *AWValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateAcceleratorWorkload 는 admission 시점에 컨트롤러와 동일한 intent.Translate 를 돌려
// 클래스 참조·모드·요구사항이 지금 클러스터에서 성립하는지 본다. 스냅샷 조회(Load)가 실패하면
// (API 서버 일시 오류 등) 여기서 거절하지 않고 에러를 그대로 올려 admission 이 지연·에러로
// 끝나게 둔다 — failurePolicy=Ignore 라 웹훅이 타임아웃/에러 나면 API 서버가 알아서 통과시키고,
// 권위 판정은 어차피 컨트롤러가 다시 한다. 여기서 fail-open 을 흉내 내려고 소리 없이
// 통과시키면 실패를 감추게 되므로 하지 않는다.
func validateAcceleratorWorkload(ctx context.Context, c client.Client, obj runtime.Object) (admission.Warnings, error) {
	aw, ok := obj.(*npuv1alpha1.AcceleratorWorkload)
	if !ok {
		return nil, fmt.Errorf("expected AcceleratorWorkload, got %T", obj)
	}
	var class npuv1alpha1.AcceleratorClass
	if err := c.Get(ctx, types.NamespacedName{Name: aw.Spec.Accelerator.Class}, &class); err != nil {
		return nil, fmt.Errorf("spec.accelerator.class %q: %w", aw.Spec.Accelerator.Class, err)
	}
	snap, dra, err := intent.Load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("reading cluster capability: %w", err)
	}
	res, err := intent.TranslateWithDRA(intent.BuildRequest(aw, &class), snap, dra)
	if err != nil {
		return nil, err
	}
	// 공유는 통과하더라도 사용자가 무엇을 받는지 알아야 한다 — 승인 시점에 경고로 말한다.
	if res.Mode == npuv1alpha1.AccessModeShared || res.Mode == npuv1alpha1.AccessModePartitionedShared {
		return admission.Warnings{res.Explanation}, nil
	}
	return nil, nil
}
