// ============================================================
// compile.go: descriptor 를 backend 런타임 설정으로 컴파일한다
// 상세: 이 파일이 "생성기" 의 본체다. 설정의 모든 값은 descriptor 에서만 나온다 -
//
//	벤더 기본값을 여기서 채우면 명세서가 단일 출처라는 성질이 깨진다.
//
// 생성일: 2026-08-11 | 수정일: 2026-10-04
// ============================================================
package descriptor

import (
	"encoding/json"
	"fmt"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// dpConfig 는 일반 device-plugin 런타임의 설정 스키마와 1:1 대응한다.
type dpConfig struct {
	ResourceName string   `json:"resourceName"`
	DeviceNodes  []string `json:"deviceNodes"`
	// Replicas 는 spec.Sharing 이 선언됐을 때만 채워진다(0 이면 미공유 - 장치 그대로 광고).
	Replicas int32 `json:"replicas,omitempty"`
}

// ResourceNameFor 는 descriptor 가 광고할 리소스명이다.
func ResourceNameFor(spec npuv1alpha1.AcceleratorDescriptorSpec) string {
	return spec.Vendor + "/" + spec.Product
}

// CompileDevicePlugin 은 descriptor 로 device-plugin 설정을 만든다.
// 장치 발견은 이제 런타임(device-plugin)이 host 를 보고 직접 한다 - 컴파일은
// spec.DeviceNodes 템플릿을 치환 없이 그대로 실어 보낸다.
func CompileDevicePlugin(spec npuv1alpha1.AcceleratorDescriptorSpec) ([]byte, error) {
	if errs := Validate(spec); len(errs) > 0 {
		return nil, fmt.Errorf("descriptor 정합 오류: %w", errs.ToAggregate())
	}
	cfg := dpConfig{ResourceName: ResourceNameFor(spec), DeviceNodes: spec.DeviceNodes}
	if spec.Sharing != nil {
		cfg.Replicas = spec.Sharing.Replicas
	}
	return json.Marshal(cfg)
}

// draConfig 는 일반 DRA 런타임의 설정 스키마와 1:1 대응한다.
// 선언 세 맵은 비면 키 자체가 빠져야 한다 — 빈 맵을 실으면 런타임이 그것을 선언으로 읽는다.
type draConfig struct {
	DriverName    string            `json:"driverName"`
	DeviceClass   string            `json:"deviceClass"`
	DeviceNodes   []string          `json:"deviceNodes"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Capacity      map[string]string `json:"capacity,omitempty"`
	CapacityFiles map[string]string `json:"capacityFiles,omitempty"`
}

// DriverNameFor 는 이 descriptor 가 발행할 DRA 드라이버 이름이다.
// ResourceSlice.spec.driver 와 DeviceClass 셀렉터가 이 값으로 맞물린다.
func DriverNameFor(spec npuv1alpha1.AcceleratorDescriptorSpec) string {
	return spec.Product + "." + spec.Vendor + ".kcloud.ai"
}

// CompileDRA 는 descriptor 로 DRA 런타임 설정을 만든다. 선언한 속성·capacity 는 그대로 넘긴다 —
// 런타임이 sysfs 로 읽을 수 없는 값(제품명 등)이 거기로만 들어온다. 실현 가능성 판정은
// FeasibleBackends 와 같은 규칙을 쓴다 - 두 곳이 갈리면 검증기가 통과시킨 것을
// 컴파일러가 거절하는 일이 생긴다. 장치 발견은 런타임이 spec.DeviceNodes
// 템플릿으로 직접 한다.
func CompileDRA(spec npuv1alpha1.AcceleratorDescriptorSpec) ([]byte, error) {
	probe := spec
	probe.Backends = []string{npuv1alpha1.BackendDRA}
	ok, refusals := FeasibleBackends(probe)
	if len(ok) == 0 {
		if len(refusals) > 0 {
			return nil, fmt.Errorf("DRA backend 생성 불가: %s", refusals[0].Reason)
		}
		return nil, fmt.Errorf("DRA backend 생성 불가")
	}
	return json.Marshal(draConfig{
		DriverName: DriverNameFor(spec), DeviceClass: DriverNameFor(spec), DeviceNodes: spec.DeviceNodes,
		Attributes: spec.Attributes, Capacity: spec.Capacity, CapacityFiles: spec.CapacityFiles,
	})
}
