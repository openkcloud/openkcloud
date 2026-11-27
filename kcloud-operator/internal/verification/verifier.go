// ============================================================
// verifier.go: 검증 실행과 근거 기록
// 상세: 노드·NodeDeviceReport·정책을 읽어 판정 입력을 모으고, Evaluate 결과를 AcceleratorEvidence 로
// upsert 한다. 판정 실패는 에러가 아니라 등급 없는 근거다.
// 생성일: 2026-07-31 | 수정일: 2026-10-11
// ============================================================
package verification

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/metrics"
)

// AllocationProber 는 "이 자원을 실제로 할당받아 장치를 쓸 수 있는가" 를 묻는 seam 이다.
// 실 구현은 probe Pod 를 띄우는 기존 liveVerifier 이고, 테스트는 fake 를 넣는다. vendor 를 받는
// 이유는 벤더마다 장치 동작을 확인하는 명령이 다르기 때문이다 — 스케줄만 확인하는 pause Pod 는
// 장치가 실제로 보이는지 증명하지 못한다.
type AllocationProber interface {
	Probe(ctx context.Context, nodeName, resourceName, vendor string) (ProbeResult, error)
}

// ProbeResult 는 probe 한 번의 결과다. ExitCode 는 벤더 기능 probe 가 종료코드로 판정했을 때만
// 채워진다(스케줄만 보는 pause probe 는 nil).
type ProbeResult struct {
	Allocated bool
	ExitCode  *int32
	Output    string
}

// Message 는 근거 check 에 남길 한 줄이다 — 종료코드로 판정했으면 exit=<n> 를 앞세운다.
func (r ProbeResult) Message() string {
	var parts []string
	if r.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit=%d", *r.ExitCode))
	}
	if r.Output != "" {
		parts = append(parts, r.Output)
	}
	return strings.Join(parts, " ")
}

// Request 는 한 번의 검증 요청이다. 장치 관측값은 호출자가 이미 갖고 있으므로 그대로 받는다 —
// 이 패키지가 관측 Job 을 또 띄우면 같은 일을 두 번 한다.
type Request struct {
	NodeName     string
	Vendor       string
	SourcePolicy string
	Generation   int64
	Expectation  Expectation

	ObservedGeometry  map[string]string
	ObservationErrors map[string]string
	// TargetDevices 는 이 정책이 실제로 건드린 장치다(PCI). 비어 있으면 벤더 장치 전부를 본다.
	// 채워지면 보고서 대조를 그 장치들로 좁힌다 — 같은 노드의 비대상 GPU(예: 파티션하지 않는 A2)를
	// 정책의 기대 geometry 로 판정하면 절대 통과할 수 없는 실패가 난다.
	TargetDevices []string
	// Product 는 descriptor 요청의 제품명(예: blackhole-p150)이다. 비어 있지 않으면 보고서 대조를
	// 벤더 문자열이 아니라 제품(DeviceEntry.Model)으로 하고 ExpectedDevices 개 이상 보고돼야 한다 —
	// descriptor 는 NDR 과 벤더 어휘가 다를 수 있고(rngd 특례) per-PCI geometry 를 관측하지 않는다.
	Product         string
	ExpectedDevices int
	// DescriptorManaged 는 descriptor 가 backend 를 직접 공급하는 요청이다. node-agent 의 벤더
	// device-plugin 검증(validation.passed)은 벤더 플러그인이 꺼진 상태를 실패로 보고하므로
	// 이 요청에는 적용하지 않고 그 사실만 check 메시지에 남긴다.
	DescriptorManaged bool
}

// Verifier 는 검증을 실행하고 근거를 남긴다.
type Verifier struct {
	Client client.Client
	// Prober 가 nil 이면 정책이 프로브를 요구해도 돌리지 않는다(그리고 통과시키지도 않는다).
	Prober AllocationProber
	// Now 는 만료 시각 계산의 시계 seam 이다(nil 이면 time.Now).
	Now func() time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now == nil {
		return time.Now()
	}
	return v.Now()
}

// Load 는 노드의 근거를 읽는다. 없으면 (nil, nil) — 부재는 오류가 아니라 상태다.
func (v *Verifier) Load(ctx context.Context, nodeName string) (*v1alpha1.AcceleratorEvidence, error) {
	var ev v1alpha1.AcceleratorEvidence
	if err := v.Client.Get(ctx, types.NamespacedName{Name: nodeName}, &ev); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ev, nil
}

// Verify 는 판정 입력을 모아 Evaluate 를 돌리고 결과를 evidence 로 남긴다.
func (v *Verifier) Verify(ctx context.Context, req Request) (*v1alpha1.AcceleratorEvidence, error) {
	var node corev1.Node
	if err := v.Client.Get(ctx, types.NamespacedName{Name: req.NodeName}, &node); err != nil {
		return nil, fmt.Errorf("verify: get node %q: %w", req.NodeName, err)
	}
	ndr, err := v.loadReport(ctx, req.NodeName)
	if err != nil {
		return nil, err
	}
	policy, err := v.resolvePolicy(ctx, node.Labels)
	if err != nil {
		return nil, err
	}

	in := Inputs{
		Expectation:       req.Expectation,
		ObservedGeometry:  req.ObservedGeometry,
		ObservationErrors: req.ObservationErrors,
		ReportGeometry:    reportGeometry(ndr, req.Vendor, req.TargetDevices),
		ReportErrors:      reportErrors(ndr, req.Vendor, req.TargetDevices),
		Allocatable:       allocatableOf(&node),
	}
	if req.Product != "" {
		in.ReportGeometry = reportGeometryByProduct(ndr, req.Product)
		in.ExpectedDevices = req.ExpectedDevices
	}
	if ndr != nil && ndr.Status.Validation != nil {
		if req.DescriptorManaged {
			in.ReportValidationSkipped = "descriptor-managed backend, vendor device-plugin validation is not authoritative"
		} else {
			passed := ndr.Status.Validation.Passed
			in.ReportValidationPassed = &passed
		}
	}
	var probedAt *metav1.Time
	if policy.Enabled(v1alpha1.EvidenceCheckAllocationProbe) && v.Prober != nil && req.Expectation.ProbeResource != "" {
		existing, lerr := v.Load(ctx, req.NodeName)
		if lerr != nil {
			return nil, fmt.Errorf("verify: load evidence %q: %w", req.NodeName, lerr)
		}
		now := v.now()
		if msg, prev, reused := reusableProbe(existing, req, now, policy.TTL); reused {
			// 직전 pass 가 같은 벤더·자원으로 이미 통과했고 재사용 창(실제 probe 시각+TTL) 안이다 —
			// probe Pod 를 또 띄우지 않는다(수렴 한 번에 11~17개까지 스케줄된 라이브 기록,
			// test/live/acpp-probe/README.md). 창의 기준 시각(prev)을 그대로 이어실어 재사용이
			// 창을 밀지 못하게 한다 — 안 그러면 매 재조정이 창을 연장해 실 probe 가 영영 안 돈다.
			in.ProbeAttempted, in.ProbeAllocated, in.ProbeMessage = true, true, msg
			probedAt = prev
		} else {
			pr, perr := v.Prober.Probe(ctx, req.NodeName, req.Expectation.ProbeResource, req.Vendor)
			ran := metav1.NewTime(now)
			probedAt = &ran
			if perr != nil {
				// 프로브 자체가 실패한 것은 "할당 못 받았다" 와 다르다 — 시도했고 판정 불가로 기록한다.
				in.ProbeAttempted, in.ProbeAllocated, in.ProbeMessage = true, false, perr.Error()
			} else {
				in.ProbeAttempted, in.ProbeAllocated, in.ProbeMessage = true, pr.Allocated, pr.Message()
			}
		}
	}

	res := Evaluate(in, policy)
	// AllocationProbe check 에 실제 probe 시각을 실어 다음 pass 의 재사용 창 기준으로 남긴다
	// (재사용 pass 는 이 값을 바꾸지 않고 직전 값을 그대로 이어싣는다 — reusableProbe 참고).
	if probedAt != nil {
		for i := range res.Checks {
			if res.Checks[i].Name == v1alpha1.EvidenceCheckAllocationProbe {
				res.Checks[i].ProbedAt = probedAt
			}
		}
	}
	return v.upsert(ctx, req, policy, res, &node, ndr)
}

// reusableProbe 는 직전 AllocationProbe 결과를 새 probe 대신 쓸 수 있는지 본다. 재사용 창은
// "직전 실제 probe 시각(check.ProbedAt) + TTL" 이다 — evidence 자신의 ExpiresAt 을 창으로 쓰면
// upsert 가 매 재조정마다 그것을 갱신해 창이 무한히 밀려 실 probe 가 영영 안 돌게 된다. 창의
// 상한은 TTL 자체다. 대상 식별은 벤더와 check 의 Target(Evaluate 가 Expectation.ProbeResource 를
// 그대로 기록)이다. 실패했던 probe·다른 자원/벤더를 향한 probe·ProbedAt 이 없는 check 는 재사용하지
// 않고 다시 확인하며, 돌려주는 probedAt 을 호출부가 새 check 에 그대로 이어실어 창이 밀리지 않게 한다.
func reusableProbe(ev *v1alpha1.AcceleratorEvidence, req Request, now time.Time, ttl time.Duration) (message string, probedAt *metav1.Time, reused bool) {
	if ev == nil || ev.Spec.Vendor != req.Vendor {
		return "", nil, false
	}
	for i := range ev.Status.Checks {
		c := ev.Status.Checks[i]
		if c.Name != v1alpha1.EvidenceCheckAllocationProbe || c.Target != req.Expectation.ProbeResource {
			continue
		}
		if !c.Passed || c.ProbedAt == nil || !now.Before(c.ProbedAt.Add(ttl)) {
			return "", nil, false
		}
		return reuseMessage(c.Message), c.ProbedAt, true
	}
	return "", nil, false
}

// probeReuseMarker 는 재사용된 AllocationProbe check message 앞에 한 번만 붙는 표시다.
const probeReuseMarker = "probe result reused, no pod scheduled this pass"

// reuseMessage 는 원본 probe message 를 보존한 채 재사용 표시를 한 번만 붙인다. 이미 표시가
// 있으면 그대로 돌려줘, 재사용이 반복돼도 message 가 pass 마다 자라지 않는다(etcd 객체 무한 성장 방지).
func reuseMessage(orig string) string {
	if strings.HasPrefix(orig, probeReuseMarker) {
		return orig
	}
	if orig == "" {
		return probeReuseMarker
	}
	return probeReuseMarker + ": " + orig
}

func (v *Verifier) loadReport(ctx context.Context, nodeName string) (*v1alpha1.NodeDeviceReport, error) {
	var ndr v1alpha1.NodeDeviceReport
	if err := v.Client.Get(ctx, types.NamespacedName{Name: nodeName}, &ndr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("verify: get NodeDeviceReport %q: %w", nodeName, err)
	}
	return &ndr, nil
}

func (v *Verifier) resolvePolicy(ctx context.Context, labels map[string]string) (EffectivePolicy, error) {
	var list v1alpha1.AcceleratorVerificationPolicyList
	if err := v.Client.List(ctx, &list); err != nil {
		return EffectivePolicy{}, fmt.Errorf("verify: list AcceleratorVerificationPolicy: %w", err)
	}
	return Resolve(list.Items, labels), nil
}

// upsert 는 노드당 하나인 evidence 를 만들거나 갱신한다.
func (v *Verifier) upsert(ctx context.Context, req Request, p EffectivePolicy, res Result,
	node *corev1.Node, ndr *v1alpha1.NodeDeviceReport) (*v1alpha1.AcceleratorEvidence, error) {
	ev := &v1alpha1.AcceleratorEvidence{ObjectMeta: metav1.ObjectMeta{Name: req.NodeName}}
	err := v.Client.Get(ctx, types.NamespacedName{Name: req.NodeName}, ev)
	switch {
	case apierrors.IsNotFound(err):
		ev.Spec = v1alpha1.AcceleratorEvidenceSpec{NodeName: req.NodeName, Vendor: req.Vendor}
		if cerr := v.Client.Create(ctx, ev); cerr != nil {
			return nil, fmt.Errorf("verify: create evidence %q: %w", req.NodeName, cerr)
		}
	case err != nil:
		return nil, fmt.Errorf("verify: get evidence %q: %w", req.NodeName, err)
	}

	now := v.now()
	expires := metav1.NewTime(now.Add(p.TTL))
	next := v1alpha1.AcceleratorEvidenceStatus{
		Level:        res.Level,
		Fingerprint:  Compute(node, ndr, req.Vendor, req.Generation),
		Checks:       res.Checks,
		ObservedAt:   metav1.NewTime(now),
		ExpiresAt:    &expires,
		SourcePolicy: req.SourcePolicy,
		Reason:       res.Reason,
	}
	// 광고량은 **통과했을 때만** 남긴다. 실패한 검증의 기대값을 기준선으로 저장하면 drift 감시가
	// 한 번도 성립한 적 없는 숫자와 현재를 비교하게 된다.
	if res.Agreed {
		next.AdvertisedResources = req.Expectation.Allocatable
	}
	// 내용이 같고 만료까지 TTL/2 보다 많이 남았으면 쓰지 않는다 — 재조정마다 노드당 무조건 status 를
	// 갱신하던 비용(etcd 쓰기)을 없앤다. ExpiresAt 은 TTL/2 안쪽으로 들어오면 갱신해
	// CheckFreshness(freshness.go)·usableBaseline(acceleratorhealth_controller.go)이 만료로 읽지 않게 한다.
	// 불변식: 이 게이트 때문에 재조정 간격이 TTL/2 보다 짧아야 evidence 가 만료 전에 늘 갱신된다 —
	// 게이트가 없다면 TTL 보다 짧기만 해도 충분하다.
	if statusEquivalent(ev.Status, next) && ev.Status.ExpiresAt != nil &&
		now.Add(p.TTL/2).Before(ev.Status.ExpiresAt.Time) {
		return ev, nil
	}
	ev.Status = next
	if uerr := v.Client.Status().Update(ctx, ev); uerr != nil {
		return nil, fmt.Errorf("verify: update evidence status %q: %w", req.NodeName, uerr)
	}
	// no-op 게이트를 지나 실제로 status 를 쓴 경우에만 센다.
	metrics.RecordEvidenceRecord(req.Vendor, res.Level)
	return ev, nil
}

// statusEquivalent 는 매 pass 바뀌는 두 시각(ObservedAt·ExpiresAt)을 뺀 나머지가 같은지 본다.
// Checks[].ProbedAt 은 포함한다 — probe 가 실제로 다시 돌면 써야 한다(reusableProbe 창의 기준).
func statusEquivalent(a, b v1alpha1.AcceleratorEvidenceStatus) bool {
	a.ObservedAt, b.ObservedAt = metav1.Time{}, metav1.Time{}
	a.ExpiresAt, b.ExpiresAt = nil, nil
	return equality.Semantic.DeepEqual(a, b)
}

// reportGeometry 는 NodeDeviceReport 에서 벤더 장치의 geometry 를 뽑는다.
func reportGeometry(ndr *v1alpha1.NodeDeviceReport, vendor string, targets []string) map[string]string {
	if ndr == nil {
		return nil
	}
	want := make(map[string]bool, len(targets))
	for _, t := range targets {
		want[t] = true
	}
	out := map[string]string{}
	for i, d := range ndr.Status.Devices {
		if vendor != "" && !strings.EqualFold(d.Vendor, vendor) {
			continue
		}
		// PCI 가 없는 벤더(Furiosa 는 detector 가 PCI 를 채우지 않는다)도 "보고서에 장치가 있다"
		// 는 사실은 남겨야 한다 — PCI 없는 항목을 버리면 보고서가 멀쩡한데도 "보고서 없음" 으로
		// 읽혀 근거가 영영 등급을 못 받는다.
		if len(want) > 0 && !want[d.PCIeAddress] {
			continue
		}
		key := d.PCIeAddress
		if key == "" {
			key = fmt.Sprintf("%s/%s#%d", d.Vendor, d.Model, i)
		}
		out[key] = d.MigCurrentGeometry
	}
	return out
}

// reportGeometryByProduct 는 제품명(DeviceEntry.Model)으로 보고서 장치를 고른다. descriptor 요청은
// 벤더 어휘가 NDR 과 다를 수 있어(rngd 는 NDR vendor=furiosa/model=rngd) 벤더 키로 고르면 빈
// 보고서가 된다. 키는 descriptor 의 stableID 규약(PCI-<addr>, 없으면 model#i)과 맞춘다.
func reportGeometryByProduct(ndr *v1alpha1.NodeDeviceReport, product string) map[string]string {
	if ndr == nil {
		return nil
	}
	out := map[string]string{}
	for i, d := range ndr.Status.Devices {
		if !strings.EqualFold(d.Model, product) {
			continue
		}
		key := "PCI-" + d.PCIeAddress
		if d.PCIeAddress == "" {
			key = fmt.Sprintf("%s#%d", d.Model, i)
		}
		out[key] = d.MigCurrentGeometry
	}
	return out
}

// reportErrors 는 NodeDeviceReport 가 남긴 장치별 관측 실패 사유다(PCI → 사유).
func reportErrors(ndr *v1alpha1.NodeDeviceReport, vendor string, targets []string) map[string]string {
	if ndr == nil {
		return nil
	}
	want := make(map[string]bool, len(targets))
	for _, t := range targets {
		want[t] = true
	}
	out := map[string]string{}
	for _, d := range ndr.Status.Devices {
		if vendor != "" && !strings.EqualFold(d.Vendor, vendor) {
			continue
		}
		if len(want) > 0 && !want[d.PCIeAddress] {
			continue
		}
		if d.MigObservationError != "" && d.PCIeAddress != "" {
			out[d.PCIeAddress] = d.MigObservationError
		}
	}
	return out
}

// allocatableOf 는 노드 allocatable 을 int32 맵으로 옮긴다(판정 입력 형식).
// 가속기 자원 수량은 작지만 memory 처럼 바이트 단위로 큰 값이 섞여 들어올 수 있다 — int32 로
// 그냥 캐스팅하면 조용히 음수로 뒤집힌다(drift 감시가 그 값을 비교하면 원인 불명의 오탐이 된다).
// int32 상한을 넘으면 clamp 한다.
func allocatableOf(node *corev1.Node) map[string]int32 {
	out := make(map[string]int32, len(node.Status.Allocatable))
	for k, q := range node.Status.Allocatable {
		v := q.Value()
		if v > math.MaxInt32 {
			v = math.MaxInt32
		}
		out[string(k)] = int32(v)
	}
	return out
}
