/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// control_plane_exclusion_test.go: control-plane 배제·정책 배제 affinity 합성 검증
// 수정일: 2026-08-12

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kcloud-operator/internal/upgrade"
)

// hasDoesNotExist는 required nodeAffinity 의 모든 term 에 (key, DoesNotExist) 요구사항이
// 존재하는지 확인한다.
func hasDoesNotExist(spec *corev1.PodSpec, key string) bool {
	if spec.Affinity == nil || spec.Affinity.NodeAffinity == nil {
		return false
	}
	ns := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if ns == nil || len(ns.NodeSelectorTerms) == 0 {
		return false
	}
	for _, term := range ns.NodeSelectorTerms {
		found := false
		for _, req := range term.MatchExpressions {
			if req.Key == key && req.Operator == corev1.NodeSelectorOpDoesNotExist {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestApplyControlPlaneExclusion 는 control-plane/master 라벨 노드를 제외하는
// nodeAffinity 가 부착되고, 기존 affinity(driver-upgrade anti-affinity)가 보존되는지 검증한다.
func TestApplyControlPlaneExclusion(t *testing.T) {
	spec := &corev1.PodSpec{}
	// 기존 제약(driver-upgrade)이 먼저 적용된 상태를 모사.
	applyDriverUpgradeAntiAffinity(spec)
	applyControlPlaneExclusion(spec)

	if !hasDoesNotExist(spec, controlPlaneNodeLabel) {
		t.Errorf("control-plane 제외 요구사항이 없음")
	}
	if !hasDoesNotExist(spec, masterNodeLabel) {
		t.Errorf("master 제외 요구사항이 없음")
	}
	// 기존 driver-upgrade 제약이 보존되어야 한다(term 내 AND 누적).
	if !hasDoesNotExist(spec, upgrade.DriverUpgradingBlockingLabelKey) {
		t.Errorf("기존 driver-upgrade 제약이 손실됨")
	}
}

// hasRequirement 는 term 전부에 (key, operator, values) 요구사항이 있는지 확인한다.
func hasRequirement(spec *corev1.PodSpec, key string, op corev1.NodeSelectorOperator, values ...string) bool {
	if spec.Affinity == nil || spec.Affinity.NodeAffinity == nil {
		return false
	}
	ns := spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if ns == nil || len(ns.NodeSelectorTerms) == 0 {
		return false
	}
	sameValues := func(a []string) bool {
		if len(a) != len(values) {
			return false
		}
		for i := range a {
			if a[i] != values[i] {
				return false
			}
		}
		return true
	}
	for _, term := range ns.NodeSelectorTerms {
		found := false
		for _, req := range term.MatchExpressions {
			if req.Key == key && req.Operator == op && sameValues(req.Values) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestApplyExcludeNodeSelector 는 matchLabels·matchExpressions 각 연산자가 반대 방향
// nodeAffinity 요구사항으로 뒤집혀 얹히는 것을 단정한다. nil selector 는 아무것도 안 붙인다.
func TestApplyExcludeNodeSelector(t *testing.T) {
	spec := &corev1.PodSpec{}
	applyDriverUpgradeAntiAffinity(spec) // 기존 제약 보존 확인용
	applyExcludeNodeSelector(spec, &metav1.LabelSelector{
		MatchLabels: map[string]string{"maintenance": "true"},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"canary"}},
			{Key: "cordoned", Operator: metav1.LabelSelectorOpExists},
		},
	})

	if !hasRequirement(spec, "maintenance", corev1.NodeSelectorOpNotIn, "true") {
		t.Errorf("matchLabels 가 NotIn 으로 뒤집히지 않음")
	}
	if !hasRequirement(spec, "tier", corev1.NodeSelectorOpNotIn, "canary") {
		t.Errorf("In 이 NotIn 으로 뒤집히지 않음")
	}
	if !hasRequirement(spec, "cordoned", corev1.NodeSelectorOpDoesNotExist) {
		t.Errorf("Exists 가 DoesNotExist 로 뒤집히지 않음")
	}
	if !hasDoesNotExist(spec, upgrade.DriverUpgradingBlockingLabelKey) {
		t.Errorf("기존 driver-upgrade 제약이 손실됨")
	}

	before := &corev1.PodSpec{}
	applyExcludeNodeSelector(before, nil)
	if before.Affinity != nil {
		t.Errorf("nil selector 인데 affinity 가 생김: %+v", before.Affinity)
	}
}
