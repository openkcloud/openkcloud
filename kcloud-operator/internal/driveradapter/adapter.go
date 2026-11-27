// ============================================================
// adapter.go: 드라이버 생애주기에서 벤더마다 다른 판단을 모으는 어댑터
// 상세: 컨트롤러가 벤더 이름으로 분기하지 않고 (vendor, product) 로 어댑터를 조회해 묻는다.
// 레지스트리는 vendorcap 이 아는 조합마다 한 항목을 갖는다(adapter_test 가 강제). 지금은
// 자리만 만들고 판단을 옮기지 않아, 각 메서드는 컨트롤러가 내는 것과 같은 값을 돌려준다.
// 생성일: 2026-09-28 | 수정일: 2026-10-11
// ============================================================
package driveradapter

import (
	"sort"
	"strings"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/internal/vendorcap"
)

// 벤더·제품 이름. internal/controller 도 같은 문자열을 상수로 두지만 그 패키지를 여기서
// import 하면 순환이 생겨(뒤 태스크가 controller→driveradapter 로 의존) 여기 따로 둔다.
const (
	vendorNvidia      = "nvidia"
	vendorFuriosa     = "furiosa"
	vendorRebellions  = "rebellions"
	vendorTenstorrent = "tenstorrent"
	productWarboy     = "warboy"
	productRngd       = "rngd"
)

// presentLabelValue 는 자립 라벨의 값이다(node-manager 규약).
const presentLabelValue = "true"

// shellNoop 은 내릴 커널 모듈이 없는 벤더의 rmmod no-op 명령이다.
const shellNoop = "true"

// AptAuthSecretName 은 AptAuthRequired 가 참인 벤더의 설치 DaemonSet·Job 이 /secrets 에
// 마운트하는 Secret 이름이다(네임스페이스 kcloud, 키 furiosa.conf). 이름의 단일 출처 —
// 마운트 두 곳(driverjob·driver_daemonset_controller)이 이 상수를 쓴다.
const AptAuthSecretName = "furiosa-apt-auth"

// Key 는 (vendor, product) 다. vendorcap 의 키 모양을 그대로 쓴다 — 두 레지스트리가 같은
// 어휘를 공유해야 "능력 모델이 아는 벤더마다 어댑터가 있다" 는 완전성 시험이 성립한다.
type Key = vendorcap.Key

// DriverAdapter 는 드라이버 생애주기에서 벤더마다 다른 판단을 모은다. 컨트롤러는 벤더
// 이름으로 분기하지 않고 이 인터페이스로 묻는다. 값으로 답할 수 없는 물음은 벤더의 현재
// 기본 동작(빈 값·no-op)을 그대로 돌려준다 — 그것은 구현 누락이 아니라 정상 상태다.
// AdvertisedResource·InstallMode·NeedsRebootAfterInstall·DriverLoadedFrom·ExporterRequired 는
// 컨트롤러의 벤더 분기가 아니라(각각 internal/intent.productResource·spec.driver.mode·
// NodeDeviceReport 필드·spec.nvidia.dcgmExporter 가 담당) 이 인터페이스에 없다.
type DriverAdapter interface {
	// Key 는 이 어댑터가 담당하는 (vendor, product) 다.
	Key() Key

	// PresentNodeSelector 는 이 벤더 장치가 있는 노드를 고르는 셀렉터다(node-manager 가
	// 부여하는 자립 라벨, NFD 비의존). 셀렉터가 없는 벤더는 nil 을 돌려준다.
	// (구 internal/controller/driver_daemonset_controller.go 의 vendorNodeSelector 에서 이동)
	PresentNodeSelector() map[string]string

	// RmmodCommand 는 설치 DaemonSet 이 구 커널 모듈을 내릴 때 쓰는 셸 명령이다. 내릴
	// 모듈이 없는 벤더(호스트 관리·설치 추적만)는 no-op("true")을 돌려준다.
	// (구 internal/controller/driver_daemonset_controller.go 의 vendorRmmodCommand 에서 이동)
	RmmodCommand() string

	// ToolkitRequired 는 container-toolkit(containerd 런타임 등록) 조정이 필요한지다.
	// 현재 NVIDIA 만 참이다.
	// (internal/controller/toolkit_daemonset_controller.go 의 toolkitVendorSupported)
	ToolkitRequired() bool

	// AptAuthRequired 는 설치 DaemonSet 이 사설 APT repo 인증 secret 을 마운트해야 하는지다.
	// 현재 Furiosa Warboy 만 참이다.
	// (구 internal/controller/driver_daemonset_controller.go 의 furiosaAptAuthRequired 에서 이동)
	AptAuthRequired() bool

	// RuntimeClass 는 이 벤더 장치를 쓰는 파드에 박을 runtimeClassName 이다. 필요 없는
	// 벤더는 빈 문자열이다. 현재 NVIDIA 만 "nvidia" 를 돌려준다.
	// (internal/controller/acceleratorworkload_controller.go 의 res.Vendor==vendorNvidia 분기)
	RuntimeClass() string
}

// adapter 는 자료 기반 구현이다. 벤더를 늘리려면 컨트롤러 본문이 아니라 registry 에 항목을
// 더한다 — 이 어댑터의 목적이다. key 는 조회 시 Lookup 이 채운다(registry 키와 어긋날 수
// 없게 한 곳에서만 정한다).
type adapter struct {
	key          Key
	presentLabel string // "" 이면 노드 셀렉터 없음(현재 default 동작)
	rmmod        string
	toolkit      bool
	aptAuth      bool
	runtimeClass string
}

func (a adapter) Key() Key { return a.key }

func (a adapter) PresentNodeSelector() map[string]string {
	if a.presentLabel == "" {
		return nil
	}
	// 매 호출 새 맵을 만든다 — 저장한 맵을 돌려주면 호출자의 수정이 레지스트리를 오염시킨다.
	return map[string]string{a.presentLabel: presentLabelValue}
}

func (a adapter) RmmodCommand() string  { return a.rmmod }
func (a adapter) ToolkitRequired() bool { return a.toolkit }
func (a adapter) AptAuthRequired() bool { return a.aptAuth }
func (a adapter) RuntimeClass() string  { return a.runtimeClass }

// 인터페이스 만족을 컴파일 타임에 강제한다.
var _ DriverAdapter = adapter{}

// registry 는 정본 표다. 키는 vendorcap 레지스트리와 같은 (vendor, product) 어휘를 쓴다.
// 값은 지금 컨트롤러가 내는 것과 같다 — 판단을 고치지 않는다.
var registry = map[Key]adapter{
	{Vendor: vendorNvidia, Product: ""}: {
		presentLabel: npuv1alpha1.PresentNodeLabel(vendorNvidia),
		rmmod:        "rmmod nvidia_uvm nvidia_drm nvidia || true",
		toolkit:      true,
		runtimeClass: "nvidia",
	},
	{Vendor: vendorFuriosa, Product: productWarboy}: {
		presentLabel: npuv1alpha1.PresentNodeLabel(vendorFuriosa),
		rmmod:        "rmmod npu_pdma npu_mgmt || true",
		aptAuth:      true,
	},
	{Vendor: vendorFuriosa, Product: productRngd}: {
		presentLabel: npuv1alpha1.PresentNodeLabel(productRngd),
		rmmod:        "rmmod furiosa_rngd || true",
	},
	{Vendor: vendorRebellions, Product: ""}: {
		// 드라이버는 호스트가 관리해 설치 DaemonSet 이 없다 — rmmod 는 no-op.
		presentLabel: npuv1alpha1.PresentNodeLabel(vendorRebellions),
		rmmod:        shellNoop,
	},
	{Vendor: vendorTenstorrent, Product: ""}: {
		// 설치는 DKMS 2차(TrackOnly)라 벤더 노드 셀렉터 경로에 도달하지 않는다 — 현재
		// default 동작대로 셀렉터 없음·rmmod no-op.
		rmmod: shellNoop,
	},
}

// Lookup 은 (vendor, product) 의 어댑터를 돌려준다. 없으면 ok=false.
func Lookup(vendor, product string) (DriverAdapter, bool) {
	k := Key{Vendor: vendor, Product: product}
	a, ok := registry[k]
	if !ok {
		return nil, false
	}
	a.key = k
	return a, true
}

// ForModel 은 사용자 표기 (vendor, model) 로 어댑터를 조회한다. Lookup 이 정확한 product 키를
// 요구하는 것과 달리, 벤더 이름을 소문자로 맞추고 model 을 그 벤더의 product 로 해소한다 —
// furiosa 만 model 로 갈린다(model="rngd" 는 rngd, 빈 값·그 밖의 model 은 warboy). 나머지
// 벤더는 model 과 무관하게 product 가 빈 값이다. 모르는 벤더는 Lookup 과 같이 nil,false 이며
// 호출부의 기존 default 경로로 이어진다. 벤더 지식(제품 해소)을 이 한 곳에 모은다.
func ForModel(vendor, model string) (DriverAdapter, bool) {
	v := strings.ToLower(vendor)
	product := ""
	if v == vendorFuriosa {
		if strings.EqualFold(model, productRngd) {
			product = productRngd
		} else {
			product = productWarboy
		}
	}
	return Lookup(v, product)
}

// Keys 는 정렬된 키 목록이다(출력 결정성). vendorcap.Keys() 와 같은 정렬 규칙을 쓴다.
func Keys() []Key {
	out := make([]Key, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Vendor != out[j].Vendor {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].Product < out[j].Product
	})
	return out
}
