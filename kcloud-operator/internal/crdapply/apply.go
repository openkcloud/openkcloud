// apply.go: CRD 번들을 읽어 apiextensions.k8s.io/v1로 server-side apply.
// 상세: operator 바이너리의 apply-crds 서브명령에서 호출. go:embed로 CRD 번들.
// 생성일: 2026-04-17
package crdapply

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

//go:embed crd/*.yaml
var crdFS embed.FS

const fieldManager = "kcloud-operator"

func Run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("kube config 로드 실패: %w", err)
	}
	cli, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("apiextensions client 생성 실패: %w", err)
	}

	entries, err := fs.ReadDir(crdFS, "crd")
	if err != nil {
		return fmt.Errorf("번들된 CRD 디렉토리 읽기 실패: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := "crd/" + e.Name()
		raw, err := crdFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		crd := &apiextv1.CustomResourceDefinition{}
		if err := yaml.Unmarshal(raw, crd); err != nil {
			return fmt.Errorf("unmarshal %s: %w", path, err)
		}
		if err := applyOne(ctx, cli, crd.Name, raw); err != nil {
			return fmt.Errorf("apply %s: %w", crd.Name, err)
		}
	}
	return nil
}

// applyOne은 raw YAML bytes를 Server-Side Apply(ApplyPatchType)로 전송.
func applyOne(ctx context.Context, cli apiextclient.Interface, name string, raw []byte) error {
	result := cli.ApiextensionsV1().RESTClient().
		Patch(types.ApplyPatchType).
		Resource("customresourcedefinitions").
		Name(name).
		Param("fieldManager", fieldManager).
		Param("force", "true").
		Body(raw).
		Do(ctx)
	if err := result.Error(); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// loadConfig는 in-cluster 우선, 실패 시 kubeconfig로 fallback.
func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}
