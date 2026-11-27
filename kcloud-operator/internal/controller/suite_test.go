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

// suite_test.go: internal/controller envtest 스위트 부트스트랩
// 상세: envtest 로 API 서버를 띄워 CRD 를 적재하고, 패키지 전체가 공유하는 client·config·
// 컨텍스트를 준비한다(BeforeSuite/AfterSuite). 서버 버전을 실측해 ENVTEST_K8S_VERSION 과
// 다른 자산이 조용히 쓰이는 것을 막는다. 개별 컨트롤러 시험은 같은 패키지의 *_test.go 에 있다.
// 생성일: 2025-07-04 | 수정일: 2026-10-11

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	// 경합(다중 제어 평면·다중 에이전트) 아래에서도 시간 기반 단언이 안정적으로 수렴하도록
	// 기본 타임아웃을 올린다. 개별 스펙이 명시한 WithTimeout 은 이 값을 덮어쓴다.
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = npuv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// envtest 가 띄운 API 서버가 Makefile 의 ENVTEST_K8S_VERSION 과 같은 선상인지 실측한다 — 자산 선택이
	// 조용히 다른 버전을 쓰면 시험이 다른 서버를 상대로 초록이 된다.
	if want := os.Getenv("KCLOUD_EXPECT_K8S_MINOR"); want != "" {
		dc, err := discovery.NewDiscoveryClientForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		v, err := dc.ServerVersion()
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Major+"."+v.Minor).To(Equal(want), "envtest 서버 버전이 기대와 다르다")
	}
})

var _ = AfterSuite(func() {
	DeferCleanup(func() {
		cancel()
		Expect(testEnv.Stop()).To(Succeed())
	})

	By("맨 Delete 로 남은 Terminating NPUClusterPolicy·AcceleratorPartitionPolicy 검사")
	leaks := map[string][]string{}
	if v := terminatingNCPs(); len(v) > 0 {
		leaks["NPUClusterPolicy"] = v
	}
	if v := terminatingACPPs(); len(v) > 0 {
		leaks["AcceleratorPartitionPolicy"] = v
	}
	Expect(leaks).To(BeEmpty(),
		"Reconcile 을 부른 스펙은 cleanup 에서 deleteNCPAndWait·deleteACPPAndWait 을 써야 한다")
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured — run
// 'make setup-envtest' beforehand to set them up.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
