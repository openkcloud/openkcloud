// ============================================================
// main.go: DriverInstallPolicy 로부터 Tenstorrent install Job YAML 렌더
// 상세: 상태기계(internal/upgrade)를 거치지 않고 정책이 선언한 image/version 그대로
//       internal/driverjob.RenderInstallJob 을 직접 호출해 install Job YAML 을 stdout 에 낸다.
// 생성일: 2026-09-20 | 수정일: 2026-10-11
// ============================================================

// Command render prints the install Job that internal/driverjob would build for a
// DriverInstallPolicy, without routing through the upgrade state machine.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	sigsyaml "sigs.k8s.io/yaml"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/driverjob"
	"kcloud-operator/pkg/npuctl"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}

func run() error {
	dipName := flag.String("dip", "tenstorrent-blackhole-ds", "DriverInstallPolicy 이름")
	node := flag.String("node", "k8s-worker3", "설치 대상 노드")
	// DriverInstallPolicy 는 cluster-scoped 라 이 값으로 DIP 를 조회하지는 않는다(client.Get 이
	// namespaced 아닌 타입엔 namespace 를 붙이지 않아 조용히 무시된다). 대신 렌더된 Job 의
	// namespace 를 이 값으로 덮어쓴다: driverjob.Namespace 는 OPERATOR_NAMESPACE env(Downward
	// API, 클러스터 안에서만 채워짐)로 정해지는데 이 프로그램은 로컬 go run 이라 그 env 가 없어
	// 패키지 기본값 kube-system 으로 떨어진다 — 실제 배포 namespace(kcloud)와 다르다.
	namespace := flag.String("namespace", "kcloud", "install Job 을 렌더할 namespace(operator 실제 namespace)")
	flag.Parse()

	scheme, err := npuctl.Scheme()
	if err != nil {
		return fmt.Errorf("scheme 구성 실패: %w", err)
	}
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("kubeconfig 로드 실패: %w", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("k8s 클라이언트 생성 실패: %w", err)
	}

	var pol npuv1alpha1.DriverInstallPolicy
	key := client.ObjectKey{Name: *dipName, Namespace: *namespace}
	if err := c.Get(context.Background(), key, &pol); err != nil {
		return fmt.Errorf("DriverInstallPolicy %s 조회 실패: %w", *dipName, err)
	}

	job := driverjob.RenderInstallJob(&pol, *node, pol.Spec.Driver.Image, pol.Spec.Driver.Version)
	job.Name += "-manual"
	job.Namespace = *namespace
	// RenderInstallJob 은 controller-runtime client.Create 용으로 TypeMeta 를 비워둔다(스킴이
	// GVK 를 안다). kubectl apply -f - 는 apiVersion/kind 를 직접 읽어야 하므로 여기서 채운다.
	job.TypeMeta = metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"}

	out, err := sigsyaml.Marshal(job)
	if err != nil {
		return fmt.Errorf("YAML 직렬화 실패: %w", err)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		return fmt.Errorf("stdout 쓰기 실패: %w", err)
	}
	return nil
}
