// ============================================================
// apply.go: CRD 번들을 읽어 apiextensions.k8s.io/v1로 server-side apply.
// 상세: operator 바이너리의 apply-crds 서브명령에서 호출한다. go:embed 로 CRD 번들을
// 담고, 릴리스 표식 대조로 하향 적용을 거절한다(판정 근거는 Run·checkRelease 주석 참고).
// 생성일: 2026-04-17 | 수정일: 2026-10-11
// ============================================================
package crdapply

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

//go:embed crd/*.yaml
var crdFS embed.FS

const fieldManager = "kcloud-operator"

// 릴리스 표식. 적용한 operator 의 릴리스 라인과 버전을 CRD annotation 에 남긴다.
// 차트와 operator 이미지가 다른 릴리스 라인이면 업그레이드마다 구 CRD 가 신 CRD 를
// 덮어쓰는데, 그 조합은 --set image.tag 로도 만들어지므로 차트의 kubeVersion 선언으로는
// 막히지 않는다. 표식이 그 적용을 적용 전에 멈추는 유일한 근거다.
const (
	lineAnnotation    = "npu.ai/crd-release-line"
	versionAnnotation = "npu.ai/crd-release-version"
)

// 릴리스 라인. K8s 1.26~1.30 라인의 버전은 -k8s1.28 접미로 끝나고(v0.6.2-k8s1.28),
// 1.31+ 라인은 접미가 없다(v0.8.0). 두 라인의 번호는 서로 단조롭지 않다 — 같은 기능
// 집합에 다른 번호를 붙이므로 버전 숫자만으로는 라인을 구분할 수 없다. 접미가 유일한
// 근거이고, 그래서 한 곳에만 둔다.
const (
	k8s128Suffix = "-k8s1.28"
	lineK8s128   = "k8s1.28"
	lineDefault  = "default"
)

// bundledCRD 는 번들에서 읽은 CRD 하나와, 클러스터에 이미 있는 그 CRD 의 표식이다.
// 대조를 모두 끝낸 뒤에 적용하려고 먼저 모은다 — 섞어 돌리면 앞쪽 CRD 만 덮어쓴 채
// 중간에 멈춘다.
type bundledCRD struct {
	name string
	raw  []byte
	cur  marker
}

// marker 는 CRD 에 박힌 릴리스 표식이다. 둘 중 하나라도 비면 표식 없음으로 다룬다.
type marker struct {
	line    string
	version string
}

func (m marker) empty() bool { return m.line == "" || m.version == "" }

// Run 은 번들된 CRD 를 클러스터에 적용한다. release 는 ldflags 로 박힌 이 바이너리의
// 릴리스 버전이고(정본은 deploy/helm/Chart.yaml 의 appVersion), force 는 하향 적용을
// 강제한다. release 가 비면 대조를 건너뛰고 새 표식도 박지 않지만(빈 값을 박으면 다음
// 적용이 그것을 라인으로 읽는다), 클러스터에 있던 표식은 같은 값으로 다시 실어 지키다.
func Run(ctx context.Context, release string, force bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("kube config 로드 실패: %w", err)
	}
	cli, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("apiextensions client 생성 실패: %w", err)
	}
	return run(ctx, cli, release, force)
}

// run 은 client 를 받는 Run 의 본체다. envtest 가 자기 apiserver 의 client 를 넘긴다.
func run(ctx context.Context, cli apiextclient.Interface, release string, force bool) error {
	log := logf.Log.WithName("crdapply")

	entries, err := fs.ReadDir(crdFS, "crd")
	if err != nil {
		return fmt.Errorf("번들된 CRD 디렉토리 읽기 실패: %w", err)
	}
	bundle := make([]bundledCRD, 0, len(entries))
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
		bundle = append(bundle, bundledCRD{name: crd.Name, raw: raw})
	}

	// 표식은 적용보다 먼저 전부 읽는다. 대조가 거절로 끝나면 하나도 적용하지 않는다.
	for i := range bundle {
		if bundle[i].cur, err = clusterMarker(ctx, cli, bundle[i].name); err != nil {
			return err
		}
	}

	line := releaseLine(release)
	if release == "" {
		log.Info("경고: 릴리스 버전이 비어 세대 대조와 표식 기록을 건너뛴다",
			"힌트", "이미지는 Dockerfile 의 RELEASE_VERSION 빌드 인자로 주입한다")
	} else if err := checkRelease(bundle, release, line, force); err != nil {
		return err
	}

	for _, b := range bundle {
		stamp := marker{line: line, version: release}
		if release == "" {
			// 릴리스 값이 비면 클러스터에 있던 표식을 같은 값으로 다시 싣는다. 같은 field
			// manager 의 server-side apply 는 이번 본문에 없는 필드를 지우므로, 싣지 않으면
			// 표식이 사라지고 그 뒤로는 대조할 근거가 없어진다. 표식이 없던 CRD 는 그대로 둔다.
			stamp = b.cur
		}
		raw := b.raw
		if !stamp.empty() {
			if raw, err = withMarker(b.raw, stamp.line, stamp.version); err != nil {
				return fmt.Errorf("표식 주입 %s: %w", b.name, err)
			}
		}
		if err := applyOne(ctx, cli, b.name, raw); err != nil {
			return fmt.Errorf("apply %s: %w", b.name, err)
		}
	}
	return nil
}

// clusterMarker 는 클러스터에 있는 CRD 의 릴리스 표식을 읽는다. CRD 가 없거나 표식이
// 차 있지 않으면 빈 marker 를 돌린다.
func clusterMarker(ctx context.Context, cli apiextclient.Interface, name string) (marker, error) {
	crd, err := cli.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return marker{}, nil
	}
	if err != nil {
		return marker{}, fmt.Errorf("기존 CRD %s 조회 실패: %w", name, err)
	}
	m := marker{line: crd.Annotations[lineAnnotation], version: crd.Annotations[versionAnnotation]}
	if m.empty() {
		return marker{}, nil
	}
	return m, nil
}

// checkRelease 는 클러스터에 이미 있는 CRD 의 표식과 들어오는 릴리스를 대조한다.
// 거절 조건은 둘이다 — 라인이 다르거나, 같은 라인에서 들어오는 버전이 더 낮을 때.
// 표식이 없으면 허용한다: 기존 클러스터의 CRD 에는 표식이 없고, 그것을 막으면 아무도
// 업그레이드할 수 없다. 같은 버전 재적용도 허용한다.
func checkRelease(bundle []bundledCRD, release, line string, force bool) error {
	log := logf.Log.WithName("crdapply")
	for _, b := range bundle {
		if b.cur.empty() {
			continue
		}
		curLine, curVer := b.cur.line, b.cur.version

		reason := ""
		switch {
		case curLine != line:
			reason = "릴리스 라인이 다르다"
		default:
			cmp, ok := compareVersion(release, curVer)
			if !ok {
				log.Info("버전 표식을 숫자로 읽을 수 없어 대조를 건너뛴다",
					"crd", b.name, "클러스터버전", curVer, "들어오는버전", release)
			} else if cmp < 0 {
				reason = "같은 라인에서 버전이 더 낮다"
			}
		}
		if reason == "" {
			continue
		}

		// 두 값을 다 적는다. 하나만 적으면 운영자가 무엇을 고쳐야 할지 모른다.
		detail := fmt.Sprintf("CRD 하향 적용 거절 — %s: %s, 클러스터 라인=%s 버전=%s, 들어오는 라인=%s 버전=%s",
			b.name, reason, curLine, curVer, line, release)
		if force {
			log.Info("강제 플래그로 하향 적용 진행: " + detail)
			continue
		}
		return fmt.Errorf("%s (강제하려면 apply-crds --force, helm 경로는 --set crdUpgrade.enabled=false 로 이 Job 을 끈다)", detail)
	}
	return nil
}

// releaseLine 은 릴리스 버전 문자열에서 라인을 읽는다. 값은 비지 않는다 — 빈 값을
// annotation 에 박으면 다음 적용이 표식 없음과 구별하지 못한다.
func releaseLine(version string) string {
	if strings.HasSuffix(version, k8s128Suffix) {
		return lineK8s128
	}
	return lineDefault
}

// compareVersion 은 같은 라인 안의 두 릴리스 버전을 비교한다(-1/0/1). 라인 접미는 떼고
// v<major>.<minor>.<patch> 만 본다. 숫자를 못 읽으면 ok=false 로, 호출부는 허용 쪽으로
// 기운다 — 표식이 없을 때 허용하는 것과 같은 이유다.
func compareVersion(a, b string) (int, bool) {
	aMaj, aMin, aPatch, aOK := parseVersion(a)
	bMaj, bMin, bPatch, bOK := parseVersion(b)
	if !aOK || !bOK {
		return 0, false
	}
	for _, pair := range [][2]int{{aMaj, bMaj}, {aMin, bMin}, {aPatch, bPatch}} {
		switch {
		case pair[0] < pair[1]:
			return -1, true
		case pair[0] > pair[1]:
			return 1, true
		}
	}
	return 0, true
}

func parseVersion(v string) (maj, min, patch int, ok bool) {
	n, err := fmt.Sscanf(strings.TrimSuffix(v, k8s128Suffix), "v%d.%d.%d", &maj, &min, &patch)
	return maj, min, patch, err == nil && n == 3
}

// withMarker 는 적용할 raw YAML 의 metadata.annotations 에 표식을 박는다. 타입 객체로
// 다시 직렬화하지 않고 map 으로 왕복하는 이유는, 번들 파일에 없던 필드(status 등)가
// server-side apply 본문에 끼지 않게 하려는 것이다.
func withMarker(raw []byte, line, version string) ([]byte, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("metadata 가 없다")
	}
	ann, ok := meta["annotations"].(map[string]interface{})
	if !ok {
		ann = map[string]interface{}{}
		meta["annotations"] = ann
	}
	ann[lineAnnotation] = line
	ann[versionAnnotation] = version
	return yaml.Marshal(obj)
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
