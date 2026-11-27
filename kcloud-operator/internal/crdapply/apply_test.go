// ============================================================
// apply_test.go: apply-crds 의 CRD 하향 적용 거절을 고정한다.
// 상세: 수용 기준은 테스트 함수마다 "수용 기준 N" 주석으로 적는다. 거절 메시지가 네 값을
// 모두 담는지도 함께 본다 — 하나만 적으면 운영자가 무엇을 고쳐야 할지 모른다.
// 생성일: 2026-10-04 | 수정일: 2026-10-11
// ============================================================
package crdapply

import (
	"strings"
	"testing"
)

const testCRDName = "npuclusterpolicies.npu.ai"

// clusterCRD 는 클러스터에 이미 있는 CRD 하나의 표식을 만든다. line·version 이 비면
// 표식 없는 CRD 다(이 바이너리 이전에 적용된 상태).
func clusterCRD(line, version string) marker {
	return marker{line: line, version: version}
}

// checkOne 은 번들에 CRD 하나만 있는 상태로 대조를 돌린다.
func checkOne(t *testing.T, cur marker, release string, force bool) error {
	t.Helper()
	return checkRelease([]bundledCRD{{name: testCRDName, cur: cur}},
		release, releaseLine(release), force)
}

// 수용 기준 1: 신 CRD(표식 있음)가 있는 클러스터에 구 라인 operator 이미지로 돌리면 거절.
func TestCheckReleaseRejectsOtherLine(t *testing.T) {
	err := checkOne(t, clusterCRD(lineDefault, "v0.8.0"), "v0.6.2-k8s1.28", false)
	if err == nil {
		t.Fatal("다른 라인 적용은 거절돼야 한다")
	}
	// 메시지에 네 값이 다 있어야 운영자가 무엇을 고쳐야 할지 안다.
	for _, want := range []string{lineDefault, "v0.8.0", lineK8s128, "v0.6.2-k8s1.28", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("거절 메시지에 %q 가 없다: %v", want, err)
		}
	}

	// 반대 방향도 같다 — 1.28 라인 CRD 가 박힌 클러스터에 1.31+ 라인 이미지.
	if err := checkOne(t, clusterCRD(lineK8s128, "v0.6.2-k8s1.28"), "v0.8.0", false); err == nil {
		t.Fatal("반대 방향 라인 교차도 거절돼야 한다")
	}
}

// 수용 기준 2: 같은 라인에서 더 낮은 버전이면 거절.
func TestCheckReleaseRejectsLowerVersionInSameLine(t *testing.T) {
	err := checkOne(t, clusterCRD(lineDefault, "v0.8.0"), "v0.7.60", false)
	if err == nil {
		t.Fatal("같은 라인의 낮은 버전은 거절돼야 한다")
	}
	for _, want := range []string{"v0.8.0", "v0.7.60"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("거절 메시지에 %q 가 없다: %v", want, err)
		}
	}

	// 1.28 라인 안에서도 같은 판정이다 — 접미를 뗀 숫자로 비교한다.
	if err := checkOne(t, clusterCRD(lineK8s128, "v0.6.2-k8s1.28"), "v0.6.1-k8s1.28", false); err == nil {
		t.Fatal("1.28 라인의 낮은 버전도 거절돼야 한다")
	}
	// 더 높은 버전은 통과한다(정상 업그레이드).
	if err := checkOne(t, clusterCRD(lineDefault, "v0.7.60"), "v0.8.0", false); err != nil {
		t.Fatalf("정상 업그레이드는 통과해야 한다: %v", err)
	}
}

// 수용 기준 3: 강제 플래그를 주면 진행한다(되돌리기를 영구히 막지 않는다).
func TestCheckReleaseForceProceeds(t *testing.T) {
	if err := checkOne(t, clusterCRD(lineDefault, "v0.8.0"), "v0.6.2-k8s1.28", true); err != nil {
		t.Fatalf("강제 플래그는 라인 교차를 통과시켜야 한다: %v", err)
	}
	if err := checkOne(t, clusterCRD(lineDefault, "v0.8.0"), "v0.7.60", true); err != nil {
		t.Fatalf("강제 플래그는 하향 버전을 통과시켜야 한다: %v", err)
	}
}

// 수용 기준 4: 표식이 없는 클러스터에서는 진행하고 표식을 박는다.
// 기존 클러스터의 CRD 에는 표식이 없다. 막으면 아무도 업그레이드할 수 없다.
func TestCheckReleaseAllowsUnmarkedClusterAndStamps(t *testing.T) {
	if err := checkOne(t, clusterCRD("", ""), "v0.8.0", false); err != nil {
		t.Fatalf("표식 없는 클러스터는 통과해야 한다: %v", err)
	}
	// CRD 가 아직 없는 클러스터(최초 설치)도 빈 표식으로 같은 길을 간다.
	if err := checkRelease([]bundledCRD{{name: testCRDName}}, "v0.8.0", lineDefault, false); err != nil {
		t.Fatalf("CRD 가 없는 클러스터는 통과해야 한다: %v", err)
	}

	// "박는다" 쪽. 적용 본문에 표식이 실제로 들어가야 다음 적용이 대조할 것이 생긴다.
	raw := []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: " + testCRDName + "\n")
	out, err := withMarker(raw, lineDefault, "v0.8.0")
	if err != nil {
		t.Fatalf("표식 주입 실패: %v", err)
	}
	for _, want := range []string{lineAnnotation + ": " + lineDefault, versionAnnotation + ": v0.8.0"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("적용 본문에 %q 가 없다:\n%s", want, out)
		}
	}
	// 번들 파일에 없던 필드를 끼워 넣지 않는다 — server-side apply 본문이 커지면
	// 이 field manager 가 소유를 주장하는 범위가 넓어진다.
	if strings.Contains(string(out), "status:") || strings.Contains(string(out), "creationTimestamp") {
		t.Errorf("적용 본문에 번들에 없던 필드가 끼었다:\n%s", out)
	}
	// 이미 있던 annotation 은 남는다.
	raw = append(raw, "  annotations:\n    controller-gen.kubebuilder.io/version: v0.18.0\n"...)
	if out, err = withMarker(raw, lineDefault, "v0.8.0"); err != nil {
		t.Fatalf("표식 주입 실패: %v", err)
	}
	if !strings.Contains(string(out), "controller-gen.kubebuilder.io/version") {
		t.Errorf("기존 annotation 이 사라졌다:\n%s", out)
	}
}

// 수용 기준 5: 같은 버전 재적용은 진행한다.
func TestCheckReleaseAllowsSameVersion(t *testing.T) {
	if err := checkOne(t, clusterCRD(lineDefault, "v0.8.0"), "v0.8.0", false); err != nil {
		t.Fatalf("같은 버전 재적용은 통과해야 한다: %v", err)
	}
}

// 라인 판정은 버전 문자열의 접미 하나로만 한다. 두 라인의 번호는 서로 단조롭지 않아
// 숫자로는 라인을 알 수 없다.
func TestReleaseLine(t *testing.T) {
	cases := map[string]string{
		"v0.8.0":          lineDefault,
		"v0.6.2-k8s1.28":  lineK8s128,
		"v0.99.0-k8s1.28": lineK8s128,
		"":                lineDefault,
	}
	for in, want := range cases {
		if got := releaseLine(in); got != want {
			t.Errorf("releaseLine(%q)=%q, want %q", in, got, want)
		}
	}
	// 빈 라인 값을 내지 않는다 — 빈 값을 박으면 다음 적용이 표식 없음과 구별하지 못한다.
	if releaseLine("") == "" {
		t.Error("라인 값은 비어서는 안 된다")
	}
}

// 읽을 수 없는 버전 표식은 대조를 건너뛴다(허용). 표식이 없을 때 허용하는 것과 같은
// 이유다 — 읽지 못하는 값 때문에 업그레이드가 막히면 고칠 방법이 없다.
func TestCompareVersionUnparsableAllows(t *testing.T) {
	if _, ok := compareVersion("v0.8.0", "garbage"); ok {
		t.Error("읽을 수 없는 표식은 ok=false 여야 한다")
	}
	if err := checkOne(t, clusterCRD(lineDefault, "garbage"), "v0.8.0", false); err != nil {
		t.Fatalf("읽을 수 없는 표식은 통과해야 한다: %v", err)
	}
}
