// ============================================================
// chart_webhook_inject_scope_test.go: webhook.injectScope 두 값의 차트 렌더 결과를 고정한다.
// 상세: pod(기본)는 opt-in 라벨 objectSelector 의 mpod.npu.ai 하나, namespace 는 거기에 라벨 네임스페이스
// selector 의 mpod-namespace.npu.ai 와 operator 인자가 더해진다. 두 값 외는 스키마가 거절한다.
// 생성일: 2026-10-26 | 수정일: 2026-10-26
// ============================================================
package controller

import (
	"strings"
	"testing"
)

const (
	podScopeWebhookName = "name: mpod.npu.ai"
	nsScopeWebhookName  = "name: mpod-namespace.npu.ai"
)

// 기본값(pod)은 기존 렌더와 같아야 한다 — 라벨 opt-in webhook 하나, Pod 라벨 objectSelector,
// operator 인자 --webhook-inject-scope=pod.
func TestChartWebhookInjectScopePodIsDefault(t *testing.T) {
	got := helmTemplate(t, "--set", "webhook.enabled=true")
	for _, want := range []string{
		podScopeWebhookName,
		"--webhook-inject-scope=pod",
		"objectSelector:\n    matchLabels:\n      kcloud.ai/inject: \"true\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("기본 렌더에 %q 가 없다", want)
		}
	}
	if strings.Contains(got, nsScopeWebhookName) {
		t.Errorf("기본값(pod)인데 네임스페이스 범위 webhook 이 렌더됐다")
	}
}

// namespace 는 pod 범위 webhook 을 그대로 두고(AcceleratorWorkload 가 붙이는 Pod 라벨 경로 유지)
// 라벨 네임스페이스 webhook 을 더한다. 그 webhook 은 objectSelector 가 없어야 한다.
func TestChartWebhookInjectScopeNamespaceAddsNamespaceWebhook(t *testing.T) {
	got := helmTemplate(t, "--set", "webhook.enabled=true", "--set", "webhook.injectScope=namespace")
	for _, want := range []string{
		podScopeWebhookName,
		nsScopeWebhookName,
		"--webhook-inject-scope=namespace",
		"- key: kcloud.ai/inject\n      operator: In\n      values: [\"true\"]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("namespace 렌더에 %q 가 없다", want)
		}
	}
	if n := strings.Count(got, "objectSelector:"); n != 1 {
		t.Errorf("objectSelector 는 pod 범위 webhook 하나에만 있어야 한다, got %d", n)
	}
	// 라벨 네임스페이스 webhook 이 kube-system 제외를 잃으면 시스템 Pod 가 mutating 대상이 된다.
	idx := strings.Index(got, nsScopeWebhookName)
	if idx < 0 {
		t.Fatalf("네임스페이스 범위 webhook 이 렌더되지 않았다")
	}
	nsBlock := got[idx:]
	if !strings.Contains(nsBlock, "values: [\"kube-system\"]") {
		t.Errorf("네임스페이스 범위 webhook 에 kube-system 제외가 없다")
	}
	// 같은 라벨 키를 쓴다 — injectLabel 을 바꾸면 두 webhook 이 함께 따라가야 한다.
	got = helmTemplate(t, "--set", "webhook.enabled=true", "--set", "webhook.injectScope=namespace",
		"--set", "webhook.injectLabel=example.com/gpu")
	if !strings.Contains(got, "- key: example.com/gpu\n      operator: In") {
		t.Errorf("네임스페이스 selector 가 injectLabel 값을 따라가지 않는다")
	}
}

func TestChartWebhookInjectScopeRejectsUnknownValue(t *testing.T) {
	ok, out := helmRenderWithSet(t, "webhook.injectScope=cluster")
	if ok {
		t.Fatalf("injectScope=cluster 가 거절되지 않았다 — 스키마 enum 이 빠졌다")
	}
	if !strings.Contains(out, "injectScope") {
		t.Errorf("거절 이유에 injectScope 가 없다:\n%s", out)
	}
}
