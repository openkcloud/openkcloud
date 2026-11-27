// ============================================================
// acceleratordescriptor_dra_capability_test.go: DRA 서빙 판정 단위 시험
// 상세: draServedByMapper 가 클러스터의 resource.k8s.io/v1 DeviceClass 서빙 여부를
//
//	REST mapper 로 판정하는지 본다(envtest 없이 fake mapper 로).
//
// 생성일: 2026-09-18
// ============================================================
package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDraServedByMapper(t *testing.T) {
	gv := schema.GroupVersion{Group: "resource.k8s.io", Version: "v1"}

	served := meta.NewDefaultRESTMapper([]schema.GroupVersion{gv})
	served.Add(gv.WithKind("DeviceClass"), meta.RESTScopeRoot)
	if !draServedByMapper(served) {
		t.Error("DeviceClass 가 등록된 mapper 는 served=true 여야 한다")
	}

	empty := meta.NewDefaultRESTMapper(nil)
	if draServedByMapper(empty) {
		t.Error("빈 mapper(미서빙)는 served=false 여야 한다")
	}

	if draServedByMapper(nil) {
		t.Error("nil mapper 는 served=false 여야 한다")
	}
}
