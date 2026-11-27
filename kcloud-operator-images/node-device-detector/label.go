// ============================================================
// label.go: node-agent NFD/GFD 라벨링 능력
// 상세: Snapshot 으로부터 kcloud.ai/* 와 NFD pci-present 라벨을 계산해 노드에 patch 한다.
// kcloud.ai/* 는 완전 재조정(add/update/remove)하고, NFD 는 충돌 회피를 위해 add/update-only 다.
// 생성일: 2026-07-16 | 수정일: 2026-11-03
// ============================================================
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// labelPrefix 는 우리가 완전 관리(add/update/remove)하는 라벨 네임스페이스다.
const labelPrefix = "kcloud.ai/"

// nfdPciPrefix 는 NFD 호환 pci-present 라벨 접두사(add/update-only, 재조정 remove 제외).
const nfdPciPrefix = "feature.node.kubernetes.io/pci-"

// detectorLabelSuffixes 는 computeLabels 가 만들어 내는 kcloud.ai/* 키의 꼬리다.
// (`<벤더>.present`·`.count`·`.product`, `driver-<벤더>.loaded`·`.version`)
var detectorLabelSuffixes = []string{".present", ".count", ".product", ".loaded", ".version"}

// detectorOwns 는 이 키를 detector 가 만들 수 있는지 본다.
//
// 소유를 "내가 붙인 이름표 목록"이 아니라 "내가 만들 수 있는 이름 모양"으로 정의한다. 목록
// 방식은 새 라벨마다 여기 등록해야 하고, 빠뜨리면 다음 스캔에 지워진다 — 실제로 두 번
// 일어났다(mig-active, dra-owned). 모양으로 가르면 새 라벨은 등록 없이도 살아남는다.
func detectorOwns(key string) bool {
	if !strings.HasPrefix(key, labelPrefix) {
		return false
	}
	name := strings.TrimPrefix(key, labelPrefix)
	if name == "kernel-version" {
		return true
	}
	for _, s := range detectorLabelSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// vendorPciID 는 벤더 키 → PCI vendor ID(소문자, 0x 제거)다. NFD 계열 pci-present 라벨용.
var vendorPciID = map[string]string{
	"nvidia":      "10de",
	"furiosa":     "1ed2",
	"rngd":        "1ed2",
	"rebellions":  "1eff",
	"tenstorrent": "1e52",
}

// computeLabels 는 Snapshot 으로부터 부착할 관리 라벨 집합을 계산한다(순수 함수).
// GFD memory/compute-capability 는 sysfs 원천 미확보 시 생략한다(오탐 방지).
func computeLabels(snap *Snapshot) map[string]string {
	labels := map[string]string{}
	if snap == nil || len(snap.Devices) == 0 {
		return labels
	}
	// NFD 계열 — 커널 버전
	if kv := sanitizeLabelValue(readFileTrim(H("/proc/sys/kernel/osrelease"))); kv != "" {
		labels[labelPrefix+"kernel-version"] = kv
	}

	counts := map[string]int{}
	products := map[string]string{}
	var order []string
	for _, d := range snap.Devices {
		key := vendorKey(d.vendor, d.model)
		// NFD 계열 — pci-present
		if id := vendorPciID[key]; id != "" {
			labels[nfdPciPrefix+id+".present"] = "true"
		}
		// 벤더별 present 라벨 — device-plugin/toolkit nodeSelector 자립용(NFD/gpu-operator 불요).
		// kcloud.ai/* 이므로 Label() 의 desired 완전재조정에 포함(stale 자동 제거).
		labels[labelPrefix+key+".present"] = "true"
		// furiosa-family — Warboy(furiosa)·RNGD 는 동일 PCI(0x1ed2) → 통합 DS 공통 셀렉터.
		if vendorPciID[key] == "1ed2" {
			labels[labelPrefix+"furiosa-family.present"] = "true"
		}
		// NFD 계열 — driver loaded/version (벤더 키별)
		labels[labelPrefix+"driver-"+key+".loaded"] = strconv.FormatBool(d.loaded)
		if v := sanitizeLabelValue(d.ver); v != "" {
			labels[labelPrefix+"driver-"+key+".version"] = v
		}
		if _, ok := counts[key]; !ok {
			order = append(order, key)
			products[key] = sanitizeLabelValue(d.model)
		}
		counts[key] += int(d.count)
	}
	// GFD 계열 — product/count. `<벤더>.product` 는 detect() 의 집계값이라 NVIDIA 에서 "generic"
	// 으로 남는다(정본은 report.go 의 per-device model). 모델이 섞이면(A30+A2) 값 하나로 못 담고,
	// "generic" 은 upgrade.deviceModelMatches·controller.findPolicy·partition/nvidia 의 폴백
	// 신호로 쓰이며, sanitizeLabelValue 가 63자에서 잘라 표준 DB 최장 이름(75자)은 실으면 잘린다.
	// ponytail: 갈라진 채로 둔다 — 제품명을 실으려면 Detected 를 카드 단위로 쪼개고 소비자 폴백도 걷어내야 한다.
	for _, key := range order {
		labels[labelPrefix+key+".count"] = strconv.Itoa(counts[key])
		if products[key] != "" {
			labels[labelPrefix+key+".product"] = products[key]
		}
	}
	// GFD 계열 — 가속기 존재(범용). 벤더를 가리지 않고 장치가 하나라도 있으면 붙는다.
	labels[labelPrefix+"accelerator.present"] = "true"
	// gpu.present 는 accelerator.present 의 옛 이름이다. NVIDIA GFD 의 nvidia.com/gpu.present 를
	// 따라 지었지만 NPU 노드에도 붙어 이름이 뜻과 어긋난다. 0.8.1 사용자가 셀렉터로 쓸 수 있어
	// 한 릴리스 동안 함께 붙이고 다음 minor(0.9) 에서 뺀다 — 빼는 순간 완전 재조정이 전 노드에서
	// 즉시 지우므로 그때 CHANGELOG 에 적는다.
	labels[labelPrefix+"gpu.present"] = "true"
	return labels
}

// sanitizeLabelValue 는 문자열을 유효한 k8s 라벨 값으로 만든다.
// 허용: 영숫자·'-'·'_'·'.', 최대 63자, 앞뒤는 영숫자여야 함. 그 외 문자는 '_' 치환.
func sanitizeLabelValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	isAlnum := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	return strings.TrimFunc(out, func(r rune) bool { return !isAlnum(r) })
}

// Label 은 계산된 관리 라벨을 노드에 반영한다(MergeFrom patch).
// kcloud.ai/* 는 desired 로 완전 재조정(add/update/remove), NFD pci-present 는 add/update-only.
// ⚠️ node get/patch RBAC 필요(npu-detector-role 확대 필요). 미부여 시 patch 는 실패한다.
func Label(ctx context.Context, c client.Client, snap *Snapshot) error {
	node := &corev1.Node{}
	if err := c.Get(ctx, client.ObjectKey{Name: snap.Node}, node); err != nil {
		return fmt.Errorf("get node: %w", err)
	}
	desired := computeLabels(snap)
	orig := node.DeepCopy()
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	changed := false
	// add/update desired
	for k, v := range desired {
		if node.Labels[k] != v {
			node.Labels[k] = v
			changed = true
		}
	}
	// remove stale — detector 가 만들 수 있는 이름만(detectorOwns). 같은 kcloud.ai/* 라도
	// 다른 주체가 붙인 이름은 건드리지 않는다. NFD pci-present 도 대상 아님.
	for k := range node.Labels {
		if detectorOwns(k) {
			if _, ok := desired[k]; !ok {
				delete(node.Labels, k)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return c.Patch(ctx, node, client.MergeFrom(orig))
}
