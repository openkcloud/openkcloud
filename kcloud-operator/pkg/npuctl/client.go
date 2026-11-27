// client.go: pkg/npuctl 의 k8s API 클라이언트 생성
// 상세: kubectl-npu(및 향후 internal/apiserver)가 공유하는 controller-runtime 기반
//
//	클라이언트 래퍼. NDR/DIP/DUS/NPUClusterPolicy(v1alpha1) + Node(core/v1) 조회에 사용.
//
// 생성일: 2026-07-16 | 수정일: 2026-10-13
package npuctl

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// Client는 npu.ai CR(NDR/DIP/DUS/NPUClusterPolicy)과 core/v1 Node를
// 조회·패치하기 위한 얇은 래퍼입니다. 내부적으로 controller-runtime client.Client를
// 사용하므로(= client-go 기반), operator 컨트롤러와 동일한 타입 정의(api/v1alpha1)를 공유합니다.
type Client struct {
	c client.Client
}

// Scheme은 Client 생성에 사용하는 runtime.Scheme을 구성합니다.
// kubectl-npu 등 외부 소비자가 동일 스킴이 필요할 때 재사용할 수 있도록 exported 합니다.
func Scheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("clientgoscheme 등록 실패: %w", err)
	}
	if err := npuv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("npu.ai/v1alpha1 스킴 등록 실패: %w", err)
	}
	return scheme, nil
}

// NewClient는 kubeconfig(기본 로딩 규칙: KUBECONFIG 환경변수 또는 ~/.kube/config)로부터
// rest.Config를 구성하여 Client를 생성합니다. kubectl-npu 등 클러스터 외부 CLI에서 사용합니다.
func NewClient() (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, nil).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig 로드 실패: %w", err)
	}
	return NewClientForConfig(cfg)
}

// NewClientForConfig는 주어진 rest.Config로 Client를 생성합니다.
// 테스트나, 이미 rest.Config를 보유한 호출자(예: in-cluster 실행)를 위한 진입점입니다.
func NewClientForConfig(cfg *rest.Config) (*Client, error) {
	scheme, err := Scheme()
	if err != nil {
		return nil, err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("k8s 클라이언트 생성 실패: %w", err)
	}
	return &Client{c: withCLIFieldOwner(c)}, nil
}

// withCLIFieldOwner 는 CLI 쓰기의 managedFields 관리자 이름을 kubectl-npu 로 고정한다. 호출마다
// FieldOwner 를 넘기는 쓰기(UpgradeVendor 의 kcloud-operator-upgrade)는 그 값이 우선한다.
func withCLIFieldOwner(c client.Client) client.Client {
	return client.WithFieldOwner(c, "kubectl-npu")
}

// NewFromClient는 이미 구성된 controller-runtime client.Client 로부터 Client를 생성합니다.
// operator in-cluster(예: internal/apiserver)가 매니저의 캐시 client 를 재사용하거나,
// 단위 테스트가 fake client 를 주입하는 진입점입니다(새 커넥션·스킴 재구성 불필요).
func NewFromClient(c client.Client) *Client {
	return &Client{c: c}
}

// newClientFromRuntimeClient는 NewFromClient 의 단위 테스트용 별칭입니다.
func newClientFromRuntimeClient(c client.Client) *Client {
	return NewFromClient(c)
}
