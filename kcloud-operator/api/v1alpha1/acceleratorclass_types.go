// ============================================================
// acceleratorclass_types.go: AcceleratorClass CRD 타입 (추상 사용자 API)
// 상세: 사용자가 장치를 모르고 쓰는 등급 선언 — 요구사항(격리/메모리)과 벤더별
// native profile 매핑만 담는다. 컨트롤러·status 없음(매칭 결과는 AcceleratorWorkload.status).
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AcceleratorRequirements 는 장치가 만족해야 할 최소 조건이다.
// 빈 값은 "요구 없음" 이고, 값이 있는데 장치 쪽 근거가 없으면 거절한다(fail closed).
type AcceleratorRequirements struct {
	// MinimumIsolation 은 최소 격리 등급이다. 실측 근거가 없는 장치는
	// IsolationCapability 가 빈 문자열이며, 그런 장치는 이 요구를 만족하지 않는 것으로 본다.
	// +kubebuilder:validation:Enum=none;process;subdevice;device;hardware
	// +optional
	MinimumIsolation string `json:"minimumIsolation,omitempty"`
	// MinimumMemory 는 워크로드 하나가 쓸 수 있어야 하는 최소 장치 메모리다(예 8Gi).
	// 전체 장치 모드는 NodeDeviceReport 의 memoryMiB 와, 분할 모드는 profile 이름에
	// 인코딩된 메모리(1g.6gb → 6Gi)와 비교한다.
	// +optional
	MinimumMemory *resource.Quantity `json:"minimumMemory,omitempty"`
}

// AcceleratorMapping 은 한 벤더에서 이 클래스가 무엇으로 실현되는지다.
type AcceleratorMapping struct {
	// +kubebuilder:validation:Enum=nvidia;furiosa;rebellions;tenstorrent
	Vendor string `json:"vendor"`
	// Product 는 사람이 읽는 제품 표기(A30, rngd 등)다. 벤더 아래 제품이 하나뿐이면 생략 가능하고,
	// 둘 이상이면(furiosa: rngd/warboy) 리소스명을 이것으로 가르므로 필수다 — 비우면 webhook 이
	// 거절한다.
	// +optional
	Product string `json:"product,omitempty"`
	// NativeProfile 은 partitioned 계열 모드에서 요구하는 벤더 공식 profile
	// (NVIDIA 1g.6gb / 2g.12gb, RNGD 2core.12gb / 4core.24gb)이다.
	// exclusive/shared 전용 매핑은 비워 둔다.
	// +optional
	NativeProfile string `json:"nativeProfile,omitempty"`
	// DeviceClassName 은 이 벤더를 DRA 로 요청할 때 쓸 DeviceClass 이름이다.
	// DRADriver 와 함께 있어야 하며, 없으면 이 벤더는 DRA 경로를 지원하지 않는다.
	// +optional
	DeviceClassName string `json:"deviceClassName,omitempty"`
	// DRADriver 는 그 DeviceClass 가 고르는 ResourceSlice.spec.driver 다.
	// 노드 후보 판정에 쓴다 — DeviceClass 의 CEL 을 해석하지 않기 위해 명시받는다.
	// +optional
	DRADriver string `json:"draDriver,omitempty"`
}

// AcceleratorClassSpec 는 등급 선언이다.
type AcceleratorClassSpec struct {
	// Class 는 사람이 읽는 등급 라벨(small|medium|large 등)이다. 매칭에 쓰지 않는다.
	// +optional
	Class string `json:"class,omitempty"`
	// +optional
	Requirements AcceleratorRequirements `json:"requirements,omitempty"`
	// Mappings 는 이 클래스를 실현할 수 있는 벤더 목록이다. 순서가 기본 선호 순서다.
	// +kubebuilder:validation:MinItems=1
	Mappings []AcceleratorMapping `json:"mappings"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=aclass
// +kubebuilder:printcolumn:name="CLASS",type=string,JSONPath=".spec.class"
// +kubebuilder:printcolumn:name="ISOLATION",type=string,JSONPath=".spec.requirements.minimumIsolation"
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
type AcceleratorClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AcceleratorClassSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type AcceleratorClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AcceleratorClass `json:"items"`
}
