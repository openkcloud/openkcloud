// ============================================================
// acceleratorclass.go: AcceleratorClass validating webhook
// 상세: CRD structural schema 가 enum·MinItems 를 강제하므로 여기서는 스키마가 못 잡는 것만 본다 —
// 벤더 중복(선택 비결정), nativeProfile 형식(리소스명에 들어간다), product 모호(furiosa 등, 리소스명이 안 갈린다).
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package webhook

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
)

// nativeProfileRe 는 extended resource 이름에 안전하게 들어갈 수 있는 profile 표기다.
var nativeProfileRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]*[A-Za-z0-9])?$`)

// ACValidator 는 AcceleratorClass 를 검증한다.
type ACValidator struct{}

func (v *ACValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("AcceleratorClass", validateAcceleratorClass(obj))
}

func (v *ACValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return nil, recordRejection("AcceleratorClass", validateAcceleratorClass(newObj))
}

func (v *ACValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateAcceleratorClass(obj runtime.Object) error {
	ac, ok := obj.(*npuv1alpha1.AcceleratorClass)
	if !ok {
		return fmt.Errorf("expected AcceleratorClass, got %T", obj)
	}
	if len(ac.Spec.Mappings) == 0 {
		return fmt.Errorf("spec.mappings must not be empty")
	}
	seen := make(map[string]bool, len(ac.Spec.Mappings))
	for i, m := range ac.Spec.Mappings {
		vendor := strings.ToLower(strings.TrimSpace(m.Vendor))
		if !knownVendors[vendor] {
			return fmt.Errorf("spec.mappings[%d].vendor %q is not a known vendor (allowed: nvidia, furiosa, tenstorrent, rebellions)", i, m.Vendor)
		}
		if seen[vendor] {
			return fmt.Errorf("spec.mappings[%d]: vendor %q appears more than once, which would make the vendor choice non-deterministic", i, vendor)
		}
		seen[vendor] = true
		if m.NativeProfile != "" && !nativeProfileRe.MatchString(m.NativeProfile) {
			return fmt.Errorf("spec.mappings[%d].nativeProfile %q must be alphanumeric with dots or dashes (it becomes part of an extended resource name)", i, m.NativeProfile)
		}
		// 벤더 아래 제품이 둘 이상이면(furiosa) product 로만 리소스명이 갈린다 — 여기서
		// 막지 않으면 이 클래스를 쓰는 모든 워크로드가 Translate 에서 거절당한다.
		if _, err := intent.WholeDeviceResource(m.Vendor, m.Product); err != nil {
			return fmt.Errorf("spec.mappings[%d]: %w", i, err)
		}
	}
	return nil
}
