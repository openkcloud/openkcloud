// ============================================================
// apply_envtest_test.go: apply-crds 의 표식이 실 apiserver 왕복에서 살아남는지 확인.
// 상세: fake client 는 server-side apply 를 지원하지 않아 표식의 수명을 못 본다 — 같은
// field manager 의 SSA 가 본문에 없는 필드를 지우는 경로라 envtest 가 필요하다. 각
// 시나리오의 근거는 테스트 함수 주석 참고.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package crdapply

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// envtestCRDName 은 번들 안에 실재하는 CRD 하나다. 번들 전체가 적용되지만 표식 확인은
// 이 하나로 한다.
const envtestCRDName = "npuclusterpolicies.npu.ai"

// startEnvtest 는 apiextensions 를 내는 apiserver 를 띄우고 client 를 돌린다.
func startEnvtest(t *testing.T) apiextclient.Interface {
	t.Helper()
	env := &envtest.Environment{}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		dir := envtestBinDir()
		if dir == "" {
			t.Skip("envtest 바이너리가 없다 — KUBEBUILDER_ASSETS 또는 bin/k8s 필요")
		}
		env.BinaryAssetsDirectory = dir
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest 기동 실패: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	cli, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("client 생성 실패: %v", err)
	}
	return cli
}

// envtestBinDir 는 bin/k8s 아래 kube-apiserver 가 있는 디렉토리 하나를 고른다.
// 어느 버전이든 apiextensions/v1 을 내므로 버전을 가리지 않는다.
func envtestBinDir() string {
	entries, _ := filepath.Glob(filepath.Join("..", "..", "bin", "k8s", "*"))
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(e, "kube-apiserver")); err == nil && !st.IsDir() {
			return e
		}
	}
	return ""
}

// readMarker 는 클러스터에 적용된 CRD 의 표식을 읽는다.
func readMarker(t *testing.T, cli apiextclient.Interface) marker {
	t.Helper()
	crd, err := cli.ApiextensionsV1().CustomResourceDefinitions().
		Get(context.Background(), envtestCRDName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("CRD 조회 실패: %v", err)
	}
	return marker{line: crd.Annotations[lineAnnotation], version: crd.Annotations[versionAnnotation]}
}

// 릴리스 값이 빈 바이너리가 적용한 뒤에도 표식이 그대로 남아야 한다. 지워지면 그 뒤로
// 대조할 근거가 없어 게이트 자체가 꺼진다. 발화 경로는 주입을 넘기지 않는 빌드 경로와
// 주입 이전에 발행된 구 이미지다.
func TestRunEmptyReleaseKeepsExistingMarker(t *testing.T) {
	cli := startEnvtest(t)
	ctx := context.Background()

	if err := run(ctx, cli, "v0.8.0", false); err != nil {
		t.Fatalf("첫 적용 실패: %v", err)
	}
	if got := readMarker(t, cli); got != (marker{line: lineDefault, version: "v0.8.0"}) {
		t.Fatalf("첫 적용의 표식이 박히지 않았다: %+v", got)
	}

	if err := run(ctx, cli, "", false); err != nil {
		t.Fatalf("빈 릴리스 적용 실패: %v", err)
	}
	if got := readMarker(t, cli); got != (marker{line: lineDefault, version: "v0.8.0"}) {
		t.Fatalf("빈 릴리스 적용이 표식을 지웠다: %+v", got)
	}

	// 표식이 살아 있으니 그 뒤의 대조도 산다 — 구 라인 적용은 여전히 거절돼야 한다.
	if err := run(ctx, cli, "v0.6.2-k8s1.28", false); err == nil {
		t.Fatal("빈 릴리스 적용 뒤에도 구 라인은 거절돼야 한다")
	}
}

// 표식이 없는 클러스터에 빈 릴리스로 적용하면 표식 없이 그대로다 — 빈 값을 박으면 다음
// 적용이 그것을 라인으로 읽는다.
func TestRunEmptyReleaseDoesNotStamp(t *testing.T) {
	cli := startEnvtest(t)
	if err := run(context.Background(), cli, "", false); err != nil {
		t.Fatalf("빈 릴리스 적용 실패: %v", err)
	}
	if got := readMarker(t, cli); !got.empty() {
		t.Fatalf("빈 릴리스가 표식을 박았다: %+v", got)
	}
	if got := readMarker(t, cli); got.line != "" || got.version != "" {
		t.Fatalf("빈 값이 annotation 으로 박혔다: %+v", got)
	}
}

// 거절은 아무것도 적용하지 않고 끝난다. 섞어 돌리면 앞쪽 CRD 만 덮어쓴 채 멈춘다.
func TestRunRejectionAppliesNothing(t *testing.T) {
	cli := startEnvtest(t)
	ctx := context.Background()

	if err := run(ctx, cli, "v0.8.0", false); err != nil {
		t.Fatalf("첫 적용 실패: %v", err)
	}
	if err := run(ctx, cli, "v0.7.60", false); err == nil {
		t.Fatal("하향 버전은 거절돼야 한다")
	}
	if got := readMarker(t, cli); got.version != "v0.8.0" {
		t.Fatalf("거절했는데 표식이 바뀌었다: %+v", got)
	}

	// 강제 플래그는 같은 상태에서 통과하고 표식을 내려 쓴다.
	if err := run(ctx, cli, "v0.7.60", true); err != nil {
		t.Fatalf("강제 플래그 적용 실패: %v", err)
	}
	if got := readMarker(t, cli); got.version != "v0.7.60" {
		t.Fatalf("강제 적용 뒤 표식이 내려가지 않았다: %+v", got)
	}
}
