// describe.go: pkg/npuctl 노드 하나 상세 수집 로직
// 상세: Node/NodeDeviceReport/AcceleratorHealth/AcceleratorEvidence/AcceleratorPartitionPolicy를
// 노드 이름 하나로 모아 NodeDetail 로 옮긴다. 다른 컨트롤러가 CRD 에 적어 둔 값을 그대로
// 옮기기만 하고 다시 판정하지 않는다.
// 생성일: 2026-08-24 | 수정일: 2026-10-11
package npuctl

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// CollectNodeDetail 은 노드 하나의 상세를 모은다. 조회 다섯 건을 각각 독립으로 다룬다 —
// health·evidence·policy 는 없을 수 있고, 없는 것은 오류가 아니라 빈 값이다. 노드와
// NodeDeviceReport 가 둘 다 없을 때만 오류를 돌려준다(CollectStatus 와 같은 규약).
func (c *Client) CollectNodeDetail(ctx context.Context, name string) (*NodeDetail, error) {
	var node corev1.Node
	nodeErr := c.c.Get(ctx, client.ObjectKey{Name: name}, &node)

	var ndr npuv1alpha1.NodeDeviceReport
	ndrErr := c.c.Get(ctx, client.ObjectKey{Name: name}, &ndr)

	if nodeErr != nil && ndrErr != nil {
		return nil, fmt.Errorf("노드 %q 를 찾지 못했습니다: node=%v ndr=%v", name, nodeErr, ndrErr)
	}

	d := &NodeDetail{Name: name}
	if nodeErr == nil {
		d.Excluded = node.Labels[nodeExcludedLabel] == labelValueTrue
		d.ExcludedReason = node.Labels[nodeExcludedReasonLabel]
		d.Allocatable = map[string]string{}
		for res, qty := range node.Status.Allocatable {
			d.Allocatable[string(res)] = qty.String()
		}
	}
	if ndrErr == nil {
		for _, dev := range ndr.Status.Devices {
			d.Devices = append(d.Devices, deviceStatusFrom(dev))
		}
		if ndr.Status.Consumers != nil {
			d.Consumers = make([]ConsumerView, 0, len(ndr.Status.Consumers))
			for _, c := range ndr.Status.Consumers {
				d.Consumers = append(d.Consumers, ConsumerView{
					Namespace: c.Namespace, Pod: c.Pod, Container: c.Container,
					ResourceName: c.ResourceName, DeviceID: c.DeviceID,
				})
			}
			// kubelet PodResources 응답 순서는 고정되지 않으므로 네임스페이스·파드·
			// 컨테이너·장치 순으로 정렬해 관측마다 행 순서가 흔들리지 않게 한다.
			sort.Slice(d.Consumers, func(i, j int) bool {
				a, b := d.Consumers[i], d.Consumers[j]
				if a.Namespace != b.Namespace {
					return a.Namespace < b.Namespace
				}
				if a.Pod != b.Pod {
					return a.Pod < b.Pod
				}
				if a.Container != b.Container {
					return a.Container < b.Container
				}
				return a.DeviceID < b.DeviceID
			})
		}
		if ndr.Status.ConsumersObservedAt != nil {
			d.ConsumersObservedAt = ndr.Status.ConsumersObservedAt.UTC().Format(time.RFC3339)
		}
		d.ConsumersError = ndr.Status.ConsumersError
	}
	// 아래 셋은 없어도 정상이다 — 오류를 삼키되 값을 지어내지 않는다.
	var ah npuv1alpha1.AcceleratorHealth
	if err := c.c.Get(ctx, client.ObjectKey{Name: name}, &ah); err == nil {
		d.Health = healthViewFrom(ah.Status)
	}
	var evs npuv1alpha1.AcceleratorEvidenceList
	if err := c.c.List(ctx, &evs); err == nil {
		for _, e := range evs.Items {
			if e.Spec.NodeName == name {
				d.Evidence = append(d.Evidence, evidenceViewFrom(e))
			}
		}
	}
	var acpps npuv1alpha1.AcceleratorPartitionPolicyList
	if err := c.c.List(ctx, &acpps); err == nil {
		for _, p := range acpps.Items {
			for _, t := range p.Status.Targets {
				if t.NodeName == name {
					d.Policies = append(d.Policies, PolicyView{
						Name: p.Name, Vendor: p.Spec.Vendor, Phase: p.Status.Phase,
					})
					break
				}
			}
		}
	}
	return d, nil
}

// deviceStatusFrom 은 NodeDeviceReport.status.devices 한 항목을 DeviceStatus 로 옮긴다.
// Product 조합은 buildNodeStatuses(status.go)와 같은 규칙이다 — 모델 미판정은 "generic".
func deviceStatusFrom(dev npuv1alpha1.DeviceEntry) DeviceStatus {
	product := dev.Vendor + "/" + dev.Model
	if dev.Model == "" {
		product = dev.Vendor + "/generic"
	}
	return DeviceStatus{
		Vendor:        dev.Vendor,
		Model:         dev.Model,
		Count:         dev.Count,
		DriverLoaded:  dev.DriverLoaded,
		DriverVersion: dev.DriverVersion,
		DriverBinding: dev.DriverBinding,
		NeedsReboot:   dev.NeedsReboot,
		PCIeAddress:   dev.PCIeAddress,
		Product:       product,
	}
}

// healthViewFrom 은 AcceleratorHealth.status 를 표시용 사본으로 옮긴다.
func healthViewFrom(status npuv1alpha1.AcceleratorHealthStatus) *HealthView {
	hv := &HealthView{
		State:             status.State,
		Reason:            status.Reason,
		AllocationAllowed: status.AllocationAllowed,
	}
	for _, dh := range status.Devices {
		hv.Devices = append(hv.Devices, DeviceHealthView{
			PCIAddress: dh.PCIAddress,
			State:      dh.State,
			Reason:     dh.Reason,
		})
	}
	return hv
}

// evidenceViewFrom 은 AcceleratorEvidence 하나를 표시용 사본으로 옮긴다.
// ExpiresAt 은 *metav1.Time 이라 nil 이면 빈 문자열로 둔다.
func evidenceViewFrom(e npuv1alpha1.AcceleratorEvidence) EvidenceView {
	ev := EvidenceView{
		Vendor: e.Spec.Vendor,
		Level:  e.Status.Level,
	}
	if e.Status.ExpiresAt != nil {
		ev.ExpiresAt = e.Status.ExpiresAt.Format(time.RFC3339)
	}
	return ev
}

// ListNodeNames 는 클러스터의 모든 노드 이름을 이름순으로 돌려준다. describe 명령이
// 노드 이름 없이 불렸을 때 순회 대상을 얻는 용도다 — CollectStatus 는 DriverInstallPolicy·
// DriverUpgradeState 까지 함께 긁어와 이 목적에는 과하다.
func (c *Client) ListNodeNames(ctx context.Context) ([]string, error) {
	var nodes corev1.NodeList
	if err := c.c.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("노드 목록 조회 실패: %w", err)
	}
	names := make([]string, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return names, nil
}
