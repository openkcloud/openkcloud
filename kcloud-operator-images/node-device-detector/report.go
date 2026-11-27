// ============================================================
// report.go: Snapshot → NodeDeviceReport(status) 기록 능력
// 상세: 기존 감지 결과(devices/passthroughReserved)를 unstructured NDR 로 R/W 한다. Validation
// 결과가 있으면 status.validation 을 additive 로 덧붙인다(기존 소비자 무영향).
// 생성일: 2026-07-16 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	ndrGV  = schema.GroupVersion{Group: "npu.ai", Version: "v1alpha1"}
	ndrGVK = schema.GroupVersionKind{Group: ndrGV.Group, Version: ndrGV.Version, Kind: "NodeDeviceReport"}
)

// buildDevices 는 Snapshot 의 Detected 목록을 NDR status.devices 맵으로 변환한다.
// 필드 구성은 기존 main 루프와 바이트 동등(회귀 0). 각 device 에 대표 드라이버 바인딩을 채운다.
func buildDevices(snap *Snapshot) []map[string]any {
	// cross-major 재부팅 필요 신호. install/driver-manager 가 rmmod 실패+Require 시 host
	// 노드-레벨 마커를 남기고, detector 는 host-var 마운트로 읽기만 한다.
	// ponytail: 단일 노드-레벨 마커라 NVIDIA device 에만 적용(멀티벤더 오탐 방지). 타 벤더는
	// rmmod 로 수렴해 reboot 가 불요하다. 벤더별 마커·메이저불일치는 후속 과제다.
	needsRebootMarker := fileExists(markerPath("needs-reboot"))

	devices := make([]map[string]any, 0, len(snap.Devices))
	for _, d := range snap.Devices {
		newBase := func() map[string]any {
			dev := map[string]any{
				"vendor":              d.vendor,
				"model":               d.model,
				"count":               int32(d.count),
				"driverLoaded":        d.loaded,
				"driverVersion":       d.ver,    // 통일된 짧은 버전 (숫자형만)
				"driverVersionDetail": d.detail, // 상세(한 줄)
				// 모든 벤더 device 에 대표 드라이버 바인딩을 보고한다
				// (self-driver / vfio-pci / mixed / none).
				"driverBinding": deviceDriverBinding(d),
			}
			// onDiskVer 는 값 있을 때만 기록(false 는 생략 → 기존 NDR 과 바이트 동등, 회귀 0).
			if d.onDiskVer != "" {
				dev["driverVersionOnDisk"] = d.onDiskVer
			}
			// needsReboot 는 true 일 때만 기록(false 는 생략 → 기존 NDR 과 바이트 동등, 회귀 0).
			// 로드==디스크(둘 다 알려졌고 같음)면 마커는 스테일이다 — install 없이 안 지워지는 마커가
			// 재부팅 예산을 갉지 않도록 무시한다. 디스크 미상이면 판단 못 하므로 마커를 존중한다.
			loadedEqualsDisk := d.ver != "" && d.onDiskVer != "" && d.ver == d.onDiskVer
			if needsRebootMarker && strings.EqualFold(d.vendor, "nvidia") && !loadedEqualsDisk {
				dev["needsReboot"] = true
			}
			return dev
		}

		// PCI 주소마다 별도 device entry 를 만든다 — 집계 entry 에 주소 하나만 붙이는 방식도 답이
		// 못 된다(카드 2장 이상이면 나머지 카드엔 거짓). NVIDIA MIG 관측·NPU 카드 식별 모두 per-PCI
		// 부착이 필요하다 — PCI 가 없으면 rngd backend 의 deviceID() 가 model 폴백으로 카드를 전부
		// 같은 ID 로 뭉개며, intent.ApplyHealth 의 장치별 제외도 PCI 가 없으면 못 한다. 소비자는
		// 이미 "벤더당 entry 1개" 를 가정 못 한다(<nvidia-node> nvidia 2개) — count 합산은 fan-out 시
		// entry 당 count=1 이라 보존되고, 주소 목록도 같은 match 함수에서 나와 개수가 일치한다.
		pcis := snap.PciAddrs[vendorKey(d.vendor, d.model)]
		isNvidia := strings.EqualFold(d.vendor, "nvidia")
		if len(pcis) == 0 {
			// PCI 목록을 못 찾으면(sysfs 부재 등) 기존 단일 집계 entry 를 유지한다(회귀 0).
			dev := newBase()
			if isNvidia {
				// NVIDIA 는 전역 MigLgip 폴백까지. MIG 필드는 pci 공란으로 fail-closed(Unknown+Err).
				attachMigObservation(dev, observeMig(""))
				dev["migLgipOutput"] = snap.MigLgip // 전역 집계 producer 와 바이트 동등 폴백
			}
			devices = append(devices, dev)
			continue
		}
		for _, pci := range pcis {
			dev := newBase()
			dev["count"] = int32(1) // fan-out entry: 물리 카드 1장당 1 entry (집계 count 복붙 금지)
			dev["pcieAddress"] = pci
			// detect() 는 벤더 단위 집계라 model 이 "generic" 으로 뭉개진다. 카드를 개별로
			// 지목할 수 있는 건 여기가 처음이므로, PCI ID 로 실제 제품명을 찾아 덮는다.
			// 이 값이 제품명의 정본이다 — 노드 라벨 `<벤더>.product` 는 집계값 "generic" 으로
			// 남아 갈라진다(사유는 label.go 의 product/count 주석).
			// 판정 실패(빈 문자열)면 기존 값을 그대로 둔다 — 이 폴백이 깨지면 소비자
			// (partition/nvidia backend)의 fail-closed 판정이 무너진다.
			if isNvidia && d.model == "generic" {
				if p := productFromPCI(H(hostSysPciDevices), pci); p != "" {
					dev["model"] = p
				}
			}
			if isNvidia {
				obs := observeMig(pci)
				attachMigObservation(dev, obs)
				dev["migLgipOutput"] = obs.LgipOutput // per-PCI 관측값 — 전역 복붙이 아니다
			}
			devices = append(devices, dev)
		}
	}
	return devices
}

// attachMigObservation 은 MigObservation 을 NDR device entry 의 4개 MIG 필드로 매핑한다
// (DeviceEntry.MigModeCurrent/MigModePending/MigCurrentGeometry/MigObservationError).
func attachMigObservation(dev map[string]any, obs MigObservation) {
	dev["migModeCurrent"] = obs.ModeCurrent
	dev["migModePending"] = obs.ModePending
	dev["migCurrentGeometry"] = obs.Geometry
	dev["migObservationError"] = obs.Err
}

// migCarryFields 는 관측 주체가 바뀔 때 함께 옮겨야 하는 MIG 필드다. 넷을 따로 옮기면
// mode 는 새 값, geometry 는 옛 값 같은 섞인 상태가 만들어진다.
var migCarryFields = []string{
	"migModeCurrent", "migModePending", "migCurrentGeometry", "migObservationError", "migLgipOutput",
}

// preserveObservedMig 는 **이번에 관측하지 못한** GPU 의 MIG 필드를 기존 보고서 값으로 되돌린다.
// detector 는 비특권 컨테이너라 `/dev/nvidia*` 가 없어 MIG 를 못 본다(호스트 nvidia-smi 는 실행
// 되지만 GPU 를 못 열어 exit 9). 매 주기 "관측 실패" 를 써 넣으면 operator 가 특권 Job 으로
// 채운 값을 30초마다 지운다 — label.go 의 operatorOwnedLabels 와 같은 종류의 사고다.
//
// 규칙: 우리가 실패하고 저쪽이 성공한 경우에만 물려받는다. detector 가 다시 관측하면 새 값이 이긴다.
func preserveObservedMig(fresh []map[string]any, old []any) {
	prior := map[string]map[string]any{}
	for _, e := range old {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		pci, _ := m["pcieAddress"].(string)
		if pci == "" {
			continue
		}
		if errStr, _ := m["migObservationError"].(string); errStr != "" {
			continue // 저쪽도 관측 못 한 값이다 — 물려받을 것이 없다
		}
		prior[pci] = m
	}
	for _, dev := range fresh {
		if errStr, _ := dev["migObservationError"].(string); errStr == "" {
			continue // 이번에 관측했다 — 새 값이 이긴다
		}
		pci, _ := dev["pcieAddress"].(string)
		p, ok := prior[pci]
		if !ok {
			continue
		}
		for _, k := range migCarryFields {
			delete(dev, k)
			if v, has := p[k]; has {
				dev[k] = v
			}
		}
	}
}

// buildValidation 은 ValidationResult 를 NDR status.validation 맵으로 변환한다.
// nil 이면 nil 을 반환하여 status 에 키를 쓰지 않는다(additive-only).
func buildValidation(v *ValidationResult) map[string]any {
	if v == nil {
		return nil
	}
	steps := make([]map[string]any, 0, len(v.Steps))
	for _, s := range v.Steps {
		st := map[string]any{"name": s.Name, "passed": s.Passed}
		if s.NotApplicable {
			st["notApplicable"] = true
		}
		if s.Message != "" {
			st["message"] = s.Message
		}
		steps = append(steps, st)
	}
	return map[string]any{
		"passed":      v.Passed,
		"lastRunTime": v.LastRunTime.UTC().Format("2006-01-02T15:04:05Z"),
		"vendor":      v.Vendor,
		"steps":       steps,
	}
}

// attachConsumers 는 이번 주기의 PodResources 판정으로 status 의 세 키를 덮어쓴다.
// consumers 와 consumersError 는 배타다(CRD 계약): 성공이면 consumers·consumersObservedAt 를
// 두고 consumersError 를 지우며, 실패면 그 반대다. 이전 주기의 값은 남기지 않는다 —
// consumersFromFile 은 늘 판정을 내므로 "모른다" 가 없고, validation 의 additive 선례와 다르다.
func attachConsumers(status map[string]any, path string, now time.Time) {
	consumers, at, errText := consumersFromFile(path, now)
	if consumers != nil {
		status["consumers"] = consumers
		status["consumersObservedAt"] = at.UTC().Format(time.RFC3339)
		delete(status, "consumersError")
		return
	}
	delete(status, "consumers")
	delete(status, "consumersObservedAt")
	status["consumersError"] = errText
}

// Report 는 Snapshot 을 NDR 로 기록한다(Get-or-Create + status update).
// 기존 main 루프의 write 로직을 그대로 옮긴 것으로, validation 만 additive 로 추가된다.
func Report(ctx context.Context, c client.Client, snap *Snapshot) error {
	node := snap.Node
	devices := buildDevices(snap)
	validation := buildValidation(snap.Validation)

	ndr := &unstructured.Unstructured{}
	ndr.SetGroupVersionKind(ndrGVK)
	ndr.SetName(node)

	// Get or Create
	if err := c.Get(ctx, client.ObjectKey{Name: node}, ndr); err != nil {
		status := map[string]any{
			"devices":             devices,
			"passthroughReserved": snap.PassthroughReserved,
			// 관측 시각을 함께 보낸다. 이 값이 없으면 health 판정이 보고서의 신선도를 알 수 없어
			// 모든 노드를 Unknown 으로 둔다(operator 쪽 계약).
			"observedAt": time.Now().UTC().Format(time.RFC3339),
		}
		if validation != nil {
			status["validation"] = validation
		}
		attachConsumers(status, podResourcesAllocationsPath, time.Now())
		ndr.Object = map[string]any{
			"apiVersion": ndrGV.String(),
			"kind":       "NodeDeviceReport",
			"metadata":   map[string]any{"name": node},
			"spec":       map[string]any{"nodeName": node},
			"status":     status,
		}
		if err := c.Create(ctx, ndr); err != nil {
			return fmt.Errorf("create NDR: %w", err)
		}
		_ = c.Status().Update(ctx, ndr)
		fmt.Println("NDR created for", node)
		return nil
	}

	if ndr.Object["status"] == nil {
		ndr.Object["status"] = map[string]any{}
	}
	st := ndr.Object["status"].(map[string]any)
	if old, ok := st["devices"].([]any); ok {
		preserveObservedMig(devices, old)
	}
	st["devices"] = devices
	st["passthroughReserved"] = snap.PassthroughReserved
	st["observedAt"] = time.Now().UTC().Format(time.RFC3339)
	if validation != nil {
		st["validation"] = validation
	}
	attachConsumers(st, podResourcesAllocationsPath, time.Now())
	if err := c.Status().Update(ctx, ndr); err != nil {
		if ndr.Object["spec"] == nil {
			ndr.Object["spec"] = map[string]any{"nodeName": node}
		}
		_ = c.Update(ctx, ndr)
	}
	fmt.Println("NDR updated for", node, "devices:", len(devices))
	return nil
}
