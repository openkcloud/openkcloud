// ============================================================
// remediation.go: health 원인 → 복구 작업 변환
// 상세: Health 는 장치를 직접 바꾸지 않는다. 원인마다 어떤 AcceleratorOperation 을 만들지만 정하고,
//
//	같은 원인이 이어지는 동안 중복 생성되지 않게 트랜잭션 ID 를 쿨다운 버킷으로 고정한다.
//
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package health

import (
	"fmt"
	"time"

	"kcloud-operator/internal/operation"
)

// RecoveryPlan 은 "무엇을 만들지" 다. 만들지 않기로 한 경우 Create=false 다.
type RecoveryPlan struct {
	Create        bool
	Type          operation.Type
	TransactionID string
	ResourceKeys  []operation.ResourceKey
}

// PlanRecovery 는 원인 하나를 복구 작업 요청으로 옮긴다.
//
// TransactionID 가 중복 억제의 전부다: 같은 원인이 이어지는 동안에는 같은 버킷 → 같은 ID →
// 같은 이름 → 작업이 하나만 생긴다. 쿨다운이 지나야 새 버킷이 열린다.
func PlanRecovery(node, reason string, devicePCIs []string, now time.Time, p EffectivePolicy) RecoveryPlan {
	if !p.RemediationFor(reason).CreateOperation {
		return RecoveryPlan{}
	}
	t, ok := operationForReason(reason)
	if !ok {
		return RecoveryPlan{}
	}
	cooldown := int64(p.RecoveryCooldown / time.Second)
	if cooldown <= 0 {
		cooldown = 1
	}
	return RecoveryPlan{
		Create:        true,
		Type:          t,
		TransactionID: fmt.Sprintf("health-%s-%s-%d", node, reason, now.Unix()/cooldown),
		ResourceKeys:  resourceKeysFor(t, node, devicePCIs),
	}
}

// operationForReason 은 대응표다. 표에 없는 원인은 작업을 만들지 않는다 —
// 모르는 원인에 아무 복구나 붙이는 것이 가장 위험한 자동화다.
func operationForReason(reason string) (operation.Type, bool) {
	switch reason {
	case ReasonDevicePluginDown:
		return operation.DevicePluginRestart, true
	case ReasonAdvertisementMismatch:
		return operation.Revalidate, true
	// 드라이버 미로드는 RecoverDevice 를 만든다 — 본체가 조정자에 등록된 뒤부터다. 본체가
	// 없는 상태에서 작업을 만들면 "no participant registered" 로 즉시 종점이 되고, 그 종점이
	// 실패로 세어져 세 번 만에 멀쩡한 노드가 격리된다. 본체(recoverdevice_participant.go)가
	// 하는 일은 "드라이버를 올리는 주체를 다시 띄운다" 하나이고, 커널 모듈 재적재나 재부팅은
	// 하지 않는다 — 되돌릴 수 없는 행동은 health 의 몫이 아니다.
	case ReasonDriverNotLoaded:
		return operation.RecoverDevice, true
	default:
		return "", false
	}
}

// resourceKeysFor 는 작업이 다투는 자원이다. 키가 비면 충돌 판정이 아무것도 못 찾는다.
func resourceKeysFor(t operation.Type, node string, pcis []string) []operation.ResourceKey {
	switch t {
	case operation.DevicePluginRestart:
		return []operation.ResourceKey{operation.ClusterDPConfigKey("nvidia-device-plugin")}
	case operation.RecoverDevice:
		keys := make([]operation.ResourceKey, 0, len(pcis)+1)
		keys = append(keys, operation.NodeCordonKey(node))
		for _, pci := range pcis {
			keys = append(keys, operation.DeviceDriverKey(pci))
		}
		return keys
	default:
		// Revalidate 는 읽기 전용이라 다투는 자원이 없다(파괴성 표와 같은 판단).
		return nil
	}
}
