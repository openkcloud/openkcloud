// ============================================================
// gate.go: helm uninstall 의 pre-delete 게이트 — 사용 중이면 보류, 아니면 전량 삭제
// 상세: 가속기를 쓰는 Pod 가 하나라도 있으면 아무것도 지우지 않고 보류한다(Execute 참고).
// 없으면 npu.ai CR·DaemonSet·Job 등을 정리한다. 호스트 드라이버 패키지·커널 모듈은
// 건드리지 않는다 — 이 게이트는 Kubernetes 오브젝트만 다룬다.
// 생성일: 2026-09-10 | 수정일: 2026-10-28
// ============================================================
package uninstall

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/intent"
	"kcloud-operator/internal/naming"
)

// acceleratorDomains 는 가속기 extended resource 의 도메인 접두다. 정확한 제품명
// 목록(intent.VendorForResource)이 아니라 도메인으로 보는 이유는 게이트의 판정이
// fail-closed 여야 하기 때문이다 — 카탈로그에 없는 제품명이나 device-plugin 의 변형
// 이름(nvidia.com/mig-1g.5gb, nvidia.com/gpu.shared)도 사용으로 센다.
// intent.AcceleratorResourceDomains 가 정본이다 — 드라이버 업그레이드 drain
// (internal/upgrade/state_machine.go)도 같은 집합을 써야 두 목록이 갈리지 않는다.
var acceleratorDomains = intent.AcceleratorResourceDomains()

// waitKinds 는 삭제 후 실제로 사라질 때까지 기다리는 종류다. operator 가 finalizer 로
// DaemonSet 을 정리하므로, 기다리지 않고 다음 단계로 넘어가면 아직 지워지는 중인
// DaemonSet 을 우리가 또 지우거나 operator 가 다시 만드는 경합이 생긴다.
var waitKinds = map[string]bool{
	"NPUClusterPolicy":           true,
	"DriverInstallPolicy":        true,
	"AcceleratorPartitionPolicy": true,
}

// managedComponents 는 operator 가 만든 DaemonSet 이 다는 app.kubernetes.io/component
// 값이다. 이름을 몰라도 잡히므로 벤더가 늘어도 이 목록만 유지하면 된다.
var managedComponents = []string{
	"driver",
	"toolkit",
	"telemetry",
	"dra-driver",
	"generated-dra-driver",
	"generated-device-plugin",
	"mps-control-daemon",
}

// logf·logln 은 진행 상황을 out 에 적는다. 출력 실패는 무시한다 — 이 Job 의 성패는
// 삭제가 됐는지로 정해지고, stdout 이 막혔다고 삭제를 되돌릴 수는 없다.
func logf(out io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
func logln(out io.Writer, a ...any)               { _, _ = fmt.Fprintln(out, a...) }

// managedJobComponents 는 operator 가 만든 Job 이 다는 component 라벨이다. driver-install·driver-preflight Job 은
// DriverInstallPolicy 가 owner 라 정책을 지우면 GC 가 걷는다. mig-observe·mig-apply·device-reset Job 은 owner 가
// 없고, node-reboot 은 위임 경로만 owner 가 없다(드라이버 업그레이드·ACPP 경로는 각 정책이 owner). owner 가 없는
// Job 은 CR 을 지워도 남으므로 GC 시점에 기대지 않고 모두 여기서 지운다 — 남기면 릴리스를 지운 뒤에도 네임스페이스에
// Job 과 그 Pod 가 남는다.
var managedJobComponents = []string{
	"mig-observe",
	"mig-apply",
	"driver-install",
	"driver-preflight",
	"node-reboot",
	"device-reset",
}

// Options 는 게이트 동작을 정하는 값이다.
type Options struct {
	// SkipUsageCheck 가 참이면 사용 중 Pod 검사를 건너뛴다. 클러스터 자체를 지우는
	// 경로(Magnum 클러스터 삭제)에서만 참이다.
	SkipUsageCheck bool
	// Timeout 은 CR 하나가 finalizer 처리로 사라질 때까지 기다리는 상한이다.
	Timeout time.Duration
	// Poll 은 그 대기의 확인 주기다.
	Poll time.Duration
}

// OptionsFromEnv 는 hook Job 이 주입하는 환경변수를 읽는다.
func OptionsFromEnv() Options {
	opt := Options{Timeout: 5 * time.Minute, Poll: 2 * time.Second}
	if os.Getenv("UNINSTALL_SKIP_USAGE_CHECK") == "true" {
		opt.SkipUsageCheck = true
	}
	if v := os.Getenv("UNINSTALL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opt.Timeout = d
		}
	}
	return opt
}

// Run 은 in-cluster 설정으로 클라이언트를 만들어 게이트를 실행한다.
func Run(ctx context.Context) error {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("kube config 로드 실패: %w", err)
	}
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return err
	}
	if err := npuv1alpha1.AddToScheme(s); err != nil {
		return err
	}
	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		return fmt.Errorf("client 생성 실패: %w", err)
	}
	return Execute(ctx, c, OptionsFromEnv(), os.Stdout)
}

// Execute 는 게이트 본체다. 사용 중이면 아무것도 지우지 않고 error 를 돌려준다.
func Execute(ctx context.Context, c client.Client, opt Options, out io.Writer) error {
	if !opt.SkipUsageCheck {
		users, err := Consumers(ctx, c)
		if err != nil {
			return fmt.Errorf("pod 목록 조회 실패: %w", err)
		}
		if len(users) > 0 {
			logf(out, "가속기를 사용 중인 Pod %d 개 — 삭제를 보류한다.\n\n", len(users))
			logf(out, "%-32s %-48s %s\n", "NAMESPACE", "NAME", "RESOURCE")
			for _, u := range users {
				logf(out, "%-32s %-48s %s\n", u.Namespace, u.Name, u.Resource)
			}
			logf(out, "\n해당 Pod 를 먼저 정리한 뒤 helm uninstall 을 다시 실행한다.\n")
			return fmt.Errorf("가속기 사용 Pod %d 개로 삭제 보류", len(users))
		}
		logln(out, "가속기 사용 Pod 없음 — 삭제를 진행한다.")
	} else {
		logln(out, "UNINSTALL_SKIP_USAGE_CHECK=true — 사용 중 검사를 건너뛴다.")
	}

	if err := deleteCRs(ctx, c, opt, out); err != nil {
		return err
	}
	if err := deleteDaemonSets(ctx, c, out); err != nil {
		return err
	}
	if err := deleteJobs(ctx, c, out); err != nil {
		return err
	}
	return deleteOwnedObjects(ctx, c, out)
}

// Consumer 는 가속기를 요청한 Pod 하나다.
type Consumer struct {
	Namespace string
	Name      string
	Resource  string
}

// Consumers 는 가속기를 요청한 Pod 전부다. 종료된 Pod(Succeeded/Failed)는 장치를
// 잡고 있지 않으므로 뺀다.
func Consumers(ctx context.Context, c client.Client) ([]Consumer, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods); err != nil {
		return nil, err
	}
	var out []Consumer
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if res := acceleratorUse(p); res != "" {
			out = append(out, Consumer{Namespace: p.Namespace, Name: p.Name, Resource: res})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// acceleratorUse 는 Pod 가 요청한 가속기 자원의 이름이다. 요청이 없으면 "".
// device-plugin 축은 컨테이너 requests/limits 를 보고, DRA 축은 pod spec 의
// resourceClaims 참조를 본다. 참조하는 claim 이 어느 드라이버 것인지는 여기서
// 가리지 않는다 — 가리려면 ResourceClaim 과 DeviceClass 를 따라가야 하고,
// 못 가린 채 지우는 것보다 넓게 보류하는 편이 안전하다.
func acceleratorUse(p *corev1.Pod) string {
	found := map[string]bool{}
	ctrs := make([]corev1.Container, 0, len(p.Spec.InitContainers)+len(p.Spec.Containers))
	ctrs = append(ctrs, p.Spec.InitContainers...)
	ctrs = append(ctrs, p.Spec.Containers...)
	for i := range ctrs {
		for _, rl := range []corev1.ResourceList{ctrs[i].Resources.Limits, ctrs[i].Resources.Requests} {
			for name := range rl {
				if isAcceleratorResource(string(name)) {
					found[string(name)] = true
				}
			}
		}
	}
	if len(found) > 0 {
		names := make([]string, 0, len(found))
		for n := range found {
			names = append(names, n)
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	if len(p.Spec.ResourceClaims) > 0 {
		names := make([]string, 0, len(p.Spec.ResourceClaims))
		for _, rc := range p.Spec.ResourceClaims {
			names = append(names, rc.Name)
		}
		sort.Strings(names)
		return "resourceClaim:" + strings.Join(names, ",")
	}
	return ""
}

// isAcceleratorResource 는 자원명이 가속기 도메인에 속하는지다.
func isAcceleratorResource(name string) bool {
	for _, d := range acceleratorDomains {
		if strings.HasPrefix(name, d) {
			return true
		}
	}
	return false
}

// crKinds 는 scheme 에 등록된 npu.ai 그룹의 CR 종류다. 목록형(XList)이 함께 등록된
// 것만 CR 로 센다 — metav1.AddToGroupVersion 이 같은 그룹에 끼워 넣는 WatchEvent·
// ListOptions 류를 이 조건이 걸러 낸다.
func crKinds(s *runtime.Scheme) []string {
	gv := npuv1alpha1.GroupVersion
	out := make([]string, 0, len(s.AllKnownTypes()))
	for gvk := range s.AllKnownTypes() {
		if gvk.GroupVersion() != gv || strings.HasSuffix(gvk.Kind, "List") {
			continue
		}
		if !s.Recognizes(gv.WithKind(gvk.Kind + "List")) {
			continue
		}
		out = append(out, gvk.Kind)
	}
	sort.Strings(out)
	return out
}

// crTiers 는 CR 삭제 순서다. 워크로드를 먼저 걷어 내고, 정책 CR 은 마지막에 지운다 —
// NPUClusterPolicy·DriverInstallPolicy 를 먼저 지우면 operator 가 DaemonSet 을 걷는
// 동안 남은 워크로드가 장치를 잃는다.
func crTiers(s *runtime.Scheme) [][]string {
	first := "AcceleratorWorkload"
	last := []string{"NPUClusterPolicy", "DriverInstallPolicy"}
	isLast := map[string]bool{last[0]: true, last[1]: true}

	var mid []string
	var hasFirst bool
	for _, k := range crKinds(s) {
		switch {
		case k == first:
			hasFirst = true
		case isLast[k]:
		default:
			mid = append(mid, k)
		}
	}
	tiers := [][]string{}
	if hasFirst {
		tiers = append(tiers, []string{first})
	}
	if len(mid) > 0 {
		tiers = append(tiers, mid)
	}
	for _, k := range last {
		tiers = append(tiers, []string{k})
	}
	return tiers
}

// deleteCRs 는 npu.ai 그룹 CR 을 순서대로 지운다.
func deleteCRs(ctx context.Context, c client.Client, opt Options, out io.Writer) error {
	for _, tier := range crTiers(c.Scheme()) {
		for _, kind := range tier {
			n, err := deleteKind(ctx, c, kind)
			if err != nil {
				return fmt.Errorf("%s 삭제 실패: %w", kind, err)
			}
			if n > 0 {
				logf(out, "CR 삭제 요청: %s %d 개\n", kind, n)
			}
		}
		for _, kind := range tier {
			if !waitKinds[kind] {
				continue
			}
			if err := waitGone(ctx, c, kind, opt, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// listKind 는 한 종류의 CR 전부다. CRD 가 이미 없으면 빈 목록으로 본다.
func listKind(ctx context.Context, c client.Client, kind string) (*unstructured.UnstructuredList, error) {
	ul := &unstructured.UnstructuredList{}
	ul.SetGroupVersionKind(npuv1alpha1.GroupVersion.WithKind(kind + "List"))
	if err := c.List(ctx, ul); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return &unstructured.UnstructuredList{}, nil
		}
		return nil, err
	}
	return ul, nil
}

// deleteKind 는 한 종류의 CR 을 전부 지우고 지운 개수를 돌려준다.
func deleteKind(ctx context.Context, c client.Client, kind string) (int, error) {
	ul, err := listKind(ctx, c, kind)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range ul.Items {
		if err := c.Delete(ctx, &ul.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return n, err
		}
		n++
	}
	return n, nil
}

// waitGone 은 CR 이 실제로 사라질 때까지 기다린다. 상한을 넘기면 finalizer 를 벗겨
// 지우고 경고를 남긴다 — operator Deployment 가 이미 죽어 finalizer 를 처리할 주체가
// 없는 경우 여기서 풀지 않으면 삭제가 영영 끝나지 않는다.
func waitGone(ctx context.Context, c client.Client, kind string, opt Options, out io.Writer) error {
	deadline := time.Now().Add(opt.Timeout)
	for {
		ul, err := listKind(ctx, c, kind)
		if err != nil {
			return fmt.Errorf("%s 조회 실패: %w", kind, err)
		}
		if len(ul.Items) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			logf(out, "경고: %s %d 개가 %s 안에 사라지지 않아 finalizer 를 제거한다.\n",
				kind, len(ul.Items), opt.Timeout)
			for i := range ul.Items {
				item := &ul.Items[i]
				item.SetFinalizers(nil)
				if err := c.Update(ctx, item); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("%s/%s finalizer 제거 실패: %w", item.GetNamespace(), item.GetName(), err)
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opt.Poll):
		}
	}
}

// managedDaemonSets 는 operator 가 만들 수 있는 DaemonSet 의 자리 전부다. 라벨 정리로
// 잡히지 않는 device-plugin 계열은 이름으로 집는다(벤더 이미지를 그대로 쓰는 DaemonSet
// 에는 component 라벨이 없다). 옛 이름도 함께 둔다 — 여러 세대를 건너뛴 설치에서도
// 남은 오브젝트가 없어야 한다.
func managedDaemonSets() []client.ObjectKey {
	ks := naming.KubeSystemNamespace
	ns := naming.OperatorNamespace()
	keys := []client.ObjectKey{}
	for _, n := range []string{
		"furiosa-warboy-device-plugin",
		"furiosa-rngd-device-plugin",
		"furiosa-device-plugin",
		"furiosa-unified-device-plugin",
		"nvidia-device-plugin",
		"nvidia-device-plugin-flat",
		"rbln-device-plugin",
		"nvidia-toolkit",
		"kcloud-nvidia-toolkit",
		"dcgm-exporter",
		"kcloud-dcgm-exporter",
		"nvidia-mps-control-daemon",
	} {
		keys = append(keys, client.ObjectKey{Namespace: ks, Name: n})
	}
	for _, n := range []string{
		"kcloud-tt-device-plugin",
		"kcloud-furiosa-device-plugin",
		"kcloud-furiosa-warboy-device-plugin",
		"kcloud-furiosa-rngd-device-plugin",
		"kcloud-node-manager",
		"kcloud-furiosa-exporter",
	} {
		keys = append(keys, client.ObjectKey{Namespace: ns, Name: n})
	}
	return keys
}

// componentLabel 은 operator 가 만든 오브젝트를 종류별로 가르는 라벨 키다.
const componentLabel = "app.kubernetes.io/component"

// ownerAnnotation 은 operator 가 자기가 만든 오브젝트에 남기는 표시다(내용은 정책 CR 의
// namespace/name). ownerReference 가 없어서 CR 을 지워도 GC 가 걷지 않는 오브젝트에
// 쓴다 — 예: rbln-device-plugin 의 ClusterRole·ClusterRoleBinding·ServiceAccount 와
// ConfigMap 둘, kcloud-node-manager 의 ServiceAccount.
const ownerAnnotation = "npu.ai/owner"

// cleanupNamespaces 는 라벨로 훑을 네임스페이스다. 두 값이 같으면 한 번만 훑는다.
func cleanupNamespaces() []string {
	out := []string{naming.KubeSystemNamespace}
	if ns := naming.OperatorNamespace(); ns != naming.KubeSystemNamespace {
		out = append(out, ns)
	}
	return out
}

// operatorOwned 는 그 오브젝트를 operator 가 만들었는지다. 표시 방식이 두 가지다 —
// 정책 CR 이 만든 것은 owner 어노테이션을, DRA 드라이버 계열은 component 라벨을 단다.
func operatorOwned(o client.Object) bool {
	if _, ok := o.GetAnnotations()[ownerAnnotation]; ok {
		return true
	}
	comp := o.GetLabels()[componentLabel]
	for _, c := range managedComponents {
		if comp == c {
			return true
		}
	}
	return false
}

// sweep 은 한 종류를 훑어 operator 가 만든 것만 지운다. 어노테이션은 색인이 없어
// 서버측 필터가 불가능하므로 전부 받아 와 여기서 가린다.
func sweep(ctx context.Context, c client.Client, out io.Writer, kind string,
	list client.ObjectList, opts ...client.ListOption) (int, error) {
	if err := c.List(ctx, list, opts...); err != nil {
		return 0, fmt.Errorf("%s 조회 실패: %w", kind, err)
	}
	n := 0
	err := meta.EachListItem(list, func(o runtime.Object) error {
		obj, ok := o.(client.Object)
		if !ok || !operatorOwned(obj) {
			return nil
		}
		if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("%s %s/%s 삭제 실패: %w", kind, obj.GetNamespace(), obj.GetName(), err)
		}
		logf(out, "%s 삭제: %s/%s\n", kind, obj.GetNamespace(), obj.GetName())
		n++
		return nil
	})
	return n, err
}

// deleteOwnedObjects 는 DaemonSet·Job 말고 operator 가 남긴 오브젝트를 걷는다.
// ServiceAccount 와 ConfigMap 은 정리 대상 네임스페이스로 범위를 좁힌다 — 어노테이션이
// 없는 남의 오브젝트를 훑을 이유가 없다.
func deleteOwnedObjects(ctx context.Context, c client.Client, out io.Writer) error {
	total := 0
	for _, tc := range []struct {
		kind string
		list client.ObjectList
	}{
		{"ClusterRole", &rbacv1.ClusterRoleList{}},
		{"ClusterRoleBinding", &rbacv1.ClusterRoleBindingList{}},
	} {
		n, err := sweep(ctx, c, out, tc.kind, tc.list)
		if err != nil {
			return err
		}
		total += n
	}
	for _, ns := range cleanupNamespaces() {
		for _, tc := range []struct {
			kind string
			list client.ObjectList
		}{
			{"ServiceAccount", &corev1.ServiceAccountList{}},
			{"ConfigMap", &corev1.ConfigMapList{}},
		} {
			n, err := sweep(ctx, c, out, tc.kind, tc.list, client.InNamespace(ns))
			if err != nil {
				return err
			}
			total += n
		}
	}
	logf(out, "정리 완료: 부속 오브젝트 %d 개 삭제.\n", total)
	return nil
}

// deleteDaemonSets 는 남은 DaemonSet 을 이름과 라벨 양쪽으로 정리한다. 이름 목록은
// 후보일 뿐이다 — 실제로 존재해도 소유 표시(operatorOwned)가 없으면 지우지 않는다.
// 같은 이름을 벤더 operator 나 사람이 쓰고 있을 수 있어서다.
func deleteDaemonSets(ctx context.Context, c client.Client, out io.Writer) error {
	deleted := 0
	for _, key := range managedDaemonSets() {
		ds := &appsv1.DaemonSet{}
		if err := c.Get(ctx, key, ds); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("DaemonSet %s/%s 조회 실패: %w", key.Namespace, key.Name, err)
		}
		if !operatorOwned(ds) {
			logf(out, "소유 표시 없는 DaemonSet 은 보존한다: %s/%s\n", key.Namespace, key.Name)
			continue
		}
		if err := c.Delete(ctx, ds); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("DaemonSet %s/%s 삭제 실패: %w", key.Namespace, key.Name, err)
		}
		logf(out, "DaemonSet 삭제: %s/%s\n", key.Namespace, key.Name)
		deleted++
	}

	// 라벨 경로도 이름 경로와 같은 소유 판정(operatorOwned)을 거친다 — 서버 필터가 이미
	// 관리 component 만 고르지만, 판정 지점을 sweep 하나로 두어 조건이 두 곳에 갈라지지 않게 한다.
	for _, ns := range cleanupNamespaces() {
		for _, comp := range managedComponents {
			n, err := sweep(ctx, c, out, "DaemonSet", &appsv1.DaemonSetList{},
				client.InNamespace(ns), client.MatchingLabels{componentLabel: comp})
			if err != nil {
				return err
			}
			deleted += n
		}
	}
	logf(out, "정리 완료: DaemonSet %d 개 삭제.\n", deleted)
	return nil
}

// deleteJobs 는 operator 가 만든 Job 을 component 라벨로 지운다. Job 만 지우면 Pod 가
// 남으므로 Background 전파로 함께 걷는다.
func deleteJobs(ctx context.Context, c client.Client, out io.Writer) error {
	bg := client.PropagationPolicy(metav1.DeletePropagationBackground)
	deleted := 0
	for _, ns := range cleanupNamespaces() {
		for _, comp := range managedJobComponents {
			var list batchv1.JobList
			if err := c.List(ctx, &list, client.InNamespace(ns),
				client.MatchingLabels{componentLabel: comp}); err != nil {
				return fmt.Errorf("job 조회 실패(%s, %s): %w", ns, comp, err)
			}
			for i := range list.Items {
				item := &list.Items[i]
				if err := c.Delete(ctx, item, bg); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("job %s/%s 삭제 실패: %w", ns, item.Name, err)
				}
				logf(out, "Job 삭제: %s/%s (component=%s)\n", ns, item.Name, comp)
				deleted++
			}
		}
	}
	logf(out, "정리 완료: Job %d 개 삭제.\n", deleted)
	return nil
}
