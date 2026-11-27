// ============================================================
// rebellions_generation.go: Rebellions device plugin 판(v0.3·v0.4) 판별
// 상세: 구판(v0.3.x, SR-IOV 기반 sriovdp)과 신판(v0.4 이상, rbln-device-plugin)은 인자·설정 형식과
//       CDI 요청 이름이 달라 DaemonSet 을 다르게 렌더링한다. 명시 필드가 없으면 이미지 태그로 판별한다.
//       1.34 라인과 1.28 라인이 같은 파일을 쓴다.
// 생성일: 2026-10-24 | 수정일: 2026-10-24
// ============================================================

package controller

import (
	"fmt"
	"strconv"
	"strings"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
)

// Rebellions device plugin 판. CRD enum(devicePluginGeneration)과 같은 문자열이다.
const (
	rbllnsGenV03 = "v0.3"
	rbllnsGenV04 = "v0.4"
)

// rebellionsPluginGeneration 은 렌더링할 판과 조건 메시지에 붙일 메모를 돌려준다.
// 명시 필드가 이미지 태그보다 우선한다. 태그로 판별할 수 없으면 차트 기본 판(v0.3)으로 본다.
func rebellionsPluginGeneration(spec npuv1alpha1.RebellionsSpec) (string, string) {
	gen, note := rbllnsGenerationFromImage(spec.DevicePluginImage)
	if spec.DevicePluginGeneration != "" {
		gen, note = spec.DevicePluginGeneration, "명시 값 "+spec.DevicePluginGeneration
	}
	if gen == rbllnsGenV04 && rbllnsCustomResourceName(spec) {
		note += ", resourceName·resourcePrefix 는 v0.4 형식에서 쓰이지 않음"
	}
	return gen, note
}

// rbllnsGenerationFromImage 는 이미지 태그의 major.minor 로 판을 정한다.
func rbllnsGenerationFromImage(image string) (string, string) {
	tag := rbllnsImageTag(image)
	major, minor, ok := rbllnsMajorMinor(tag)
	if !ok {
		return rbllnsGenV03, fmt.Sprintf("태그 %q 로 판별 불가, 기본 v0.3", tag)
	}
	note := "태그 " + tag
	switch {
	case major == 0 && minor <= 3:
		return rbllnsGenV03, note
	case major == 0 && minor == 4:
		return rbllnsGenV04, note
	default:
		return rbllnsGenV04, note + ", 검증하지 않은 판"
	}
}

// rbllnsImageTag 는 이미지 참조의 태그다. digest 와 레지스트리 포트는 태그로 보지 않는다.
func rbllnsImageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	name := ref[strings.LastIndex(ref, "/")+1:]
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// rbllnsMajorMinor 는 "v0.4.3"·"0.4.3-rc1" 같은 태그의 앞 두 자리를 읽는다.
func rbllnsMajorMinor(tag string) (int, int, bool) {
	parts := strings.SplitN(strings.TrimPrefix(tag, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// rbllnsCustomResourceName 은 자원 이름을 기본값(ATOM, rebellions.ai) 밖으로 바꿨는지 본다.
func rbllnsCustomResourceName(spec npuv1alpha1.RebellionsSpec) bool {
	return (spec.ResourceName != "" && spec.ResourceName != rbllnsResourceNameDefault) ||
		(spec.ResourcePrefix != "" && spec.ResourcePrefix != rbllnsResourcePrefixDfault)
}

// rbllnsCDIKind 는 플러그인이 CDI 주석에 쓰는 종류다. 구판은 ConfigMap 의 resourcePrefix 를
// vendor 로 쓰고(관측: rebellions.ai/npu=rbln1), 신판은 rebellions.ai/npu 로 고정한다.
func rbllnsCDIKind(spec npuv1alpha1.RebellionsSpec, gen string) string {
	prefix := spec.ResourcePrefix
	if gen == rbllnsGenV04 || prefix == "" {
		prefix = rbllnsResourcePrefixDfault
	}
	return prefix + "/npu"
}
