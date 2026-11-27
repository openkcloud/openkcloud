// ============================================================
// naming.go: 드라이버 DaemonSet 이름 생성 규칙 중앙화
// 상세: driver_daemonset_controller(생성)와 upgrade/state_machine(조회)이
//       동일한 DS 이름을 사용하도록 단일 헬퍼로 통일한다. 불일치 시
//       업그레이드 상태머신이 드라이버 DS를 찾지 못한다.
// 생성일: 2026-06-02 | 수정일: 2026-10-28
// ============================================================

// Package naming centralizes Kubernetes resource naming rules shared across
// the controller and upgrade packages to avoid divergence.
package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// 벤더 문자열 상수 — 매핑 switch 에서 반복 사용(goconst 회피).
const (
	vendorNvidia  = "nvidia"
	vendorFuriosa = "furiosa"
)

// KubeSystemNamespace 는 3rd-party device-plugin 이 상주하는 시스템 네임스페이스다.
// operator 부속이 kcloud 로 이동해도 device-plugin 은 여기 고정하며, 문자열 중복(goconst)도 방지한다.
const KubeSystemNamespace = "kube-system"

// operatorNamespaceDefault 는 OPERATOR_NAMESPACE env 미설정 시 기본값.
// 기존 배치(kube-system)와 동일하게 두어 env 미설정 시 회귀 0(무해 no-op)을 보장한다.
const operatorNamespaceDefault = KubeSystemNamespace

// OperatorNamespace 는 operator 가 관리하는 워크로드(driver DaemonSet / install Job /
// toolkit DaemonSet)를 생성·조회할 네임스페이스를 반환한다. helm 이 Downward API 로
// 주입하는 `OPERATOR_NAMESPACE`(=release namespace) 를 사용하며, 미설정 시 kube-system.
// ⚠️ device-plugin(3rd party)은 이 값이 아니라 kube-system 에 고정 유지한다.
func OperatorNamespace() string {
	if ns := os.Getenv("OPERATOR_NAMESPACE"); ns != "" {
		return ns
	}
	return operatorNamespaceDefault
}

// DriverDSName returns the driver DaemonSet name for a given vendor/model.
// Mapping: nvidia → "kcloud-nvidia-driver"(model 무시), furiosa →
// "kcloud-furiosa-<model>-driver"(model 비면 "kcloud-furiosa-driver"), default →
// "kcloud-<vendor>[-<model>]-driver". model 이 비면 이중 하이픈 없이 세그먼트를 생략한다.
func DriverDSName(vendor, model string) string {
	v := strings.ToLower(vendor)
	m := strings.ToLower(model)
	switch v {
	case vendorNvidia:
		return "kcloud-nvidia-driver"
	case vendorFuriosa:
		if m == "" {
			return "kcloud-furiosa-driver"
		}
		return "kcloud-furiosa-" + m + "-driver"
	default:
		if m == "" {
			return "kcloud-" + v + "-driver"
		}
		return "kcloud-" + v + "-" + m + "-driver"
	}
}

// driverJobPrefix 는 드라이버 Job 이름의 벤더·모델 접두다(InstallJobName·PreflightJobName 공용).
func driverJobPrefix(vendor, model string) string {
	v := strings.ToLower(vendor)
	m := strings.ToLower(model)
	switch v {
	case vendorNvidia:
		return "kcloud-nvidia"
	case vendorFuriosa:
		if m == "" {
			return "kcloud-furiosa"
		}
		return "kcloud-furiosa-" + m
	default:
		if m == "" {
			return "kcloud-" + v
		}
		return "kcloud-" + v + "-" + m
	}
}

// nodeHash8 은 노드 이름 sha256 앞 8자다(DNS-1123 을 벗어난 노드명에도 안전한 접미사).
func nodeHash8(nodeName string) string {
	sum := sha256.Sum256([]byte(nodeName))
	return hex.EncodeToString(sum[:])[:8]
}

// InstallJobName returns the driver install Job name for a given vendor/model/node.
// 형식: "kcloud-<vendor>[-<model>]-install-<8hex>"(세그먼트 규칙은 DriverDSName 과 동일).
// <8hex>는 sha256(nodeName) 앞 8자로, 노드명이 DNS-1123 label 제약을 벗어나도 안전한
// 접미사를 보장한다. 동일 (vendor,model,node) 는 항상 같은 이름을 내 동시성 lease 역할을
// 하며, 호출자의 Get-before-Create + AlreadyExists 처리와 합쳐 중복 Job 생성을 막는다.
func InstallJobName(vendor, model, nodeName string) string {
	return driverJobPrefix(vendor, model) + "-install-" + nodeHash8(nodeName)
}

// PreflightJobName 은 설치 전 점검 Job 의 결정론적 이름이다. install Job 과 접두·노드 해시가 같고
// 가운데 토막만 다르다 — 같은 (vendor,model,node) 에 점검 Job 은 하나만 생긴다.
func PreflightJobName(vendor, model, nodeName string) string {
	return driverJobPrefix(vendor, model) + "-preflight-" + nodeHash8(nodeName)
}

// RebootJobName 은 노드 재부팅 Job 의 결정론적 이름이다. node 해시로 중복 생성을
// 방지(de-facto lease) — 벤더 무관(재부팅은 host 커널 문제라 노드 단위).
func RebootJobName(nodeName string) string {
	return "kcloud-node-reboot-" + nodeHash8(nodeName)
}

// AcppRebootJobName 은 ACPP(MIG mode enable)가 요청하는 재부팅 Job 이름이다.
// RebootJobName 과 반드시 달라야 한다 — 이름이 같으면 드라이버 업그레이드 경로와 서로의 Job 을
// 자기 것으로 오인해(한쪽은 재부팅을 쏜 적 없이 대기 상태를 영속, 다른 쪽은 AlreadyExists 로
// 재시도 소모) 두 상태기계가 동시에 망가진다.
func AcppRebootJobName(nodeName string) string {
	return RebootJobName(nodeName) + "-acpp"
}

// DeviceResetJobName 은 DeviceReset 작업이 만드는 호스트 명령 Job 의 결정론적 이름이다
// (노드+벤더+작업 해시). 재부팅 Job 과 겹치면 서로의 Job 을 자기 것으로 오인한다.
// transactionID 를 해시에 넣는 이유: (노드,벤더)만으로 정하면 앞선 작업의 성공 Job 을
// TTL 이 걷기 전에 새 작업이 자기 것으로 읽어, 리셋을 실행하지 않고도 성공을 보고한다.
// 작업마다 이름이 갈려야 이 오인이 성립하지 않는다.
func DeviceResetJobName(nodeName, vendor, transactionID string) string {
	sum := sha256.Sum256([]byte(nodeName + "/" + vendor + "/" + transactionID))
	return "kcloud-device-reset-" + hex.EncodeToString(sum[:])[:8]
}

// ToolkitLegacyDSName 은 접두사를 떼기 전 NVIDIA toolkit DaemonSet 이름이다.
// nvidia-container-toolkit 은 벤더 이미지를 그대로 쓰는 워크로드라 접두사를 뗐고,
// 옛 오브젝트는 생성 전에 한 번 정리한다(두 DS 가 같은 노드에서 containerd 설정을
// 서로 덮으면 유효 설정이 어느 쪽인지 알 수 없다).
const ToolkitLegacyDSName = "kcloud-nvidia-toolkit"

// ToolkitDSName returns the container-toolkit DaemonSet name for a given vendor/model.
// DriverDSName 과 동일한 규칙에 "-toolkit" 접미사만 달라, 노드에서 driver/toolkit DS 를
// 짝으로 식별한다. Mapping: nvidia → "nvidia-toolkit"(model 무시, 벤더 이미지 그대로라
// 접두사 없음), furiosa → "kcloud-furiosa-<model>-toolkit"(model 비면 "-toolkit"),
// default → "kcloud-<vendor>[-<model>]-toolkit".
func ToolkitDSName(vendor, model string) string {
	v := strings.ToLower(vendor)
	m := strings.ToLower(model)
	switch v {
	case vendorNvidia:
		return "nvidia-toolkit"
	case vendorFuriosa:
		if m == "" {
			return "kcloud-furiosa-toolkit"
		}
		return "kcloud-furiosa-" + m + "-toolkit"
	default:
		if m == "" {
			return "kcloud-" + v + "-toolkit"
		}
		return "kcloud-" + v + "-" + m + "-toolkit"
	}
}
