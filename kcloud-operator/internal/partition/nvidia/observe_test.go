// ============================================================
// observe_test.go: operator-driven MIG 관측 단위 테스트 — fail-closed parse + Job 렌더 + 구분자 파싱
// 생성일: 2026-07-27
// ============================================================
package nvidia

import (
	"os"
	"strings"
	"testing"
)

// obsTestPCI 는 테스트에서 반복 사용하는 A30 PCI 선택자다(goconst 회피).
const obsTestPCI = "0000:41:00.0"

func TestParseObservation_FailClosed(t *testing.T) {
	// Enabled 인데 -lgi 파싱 불가 → Unknown+Err (조용히 disabled 로 오판 금지).
	got := parseObservation(obsTestPCI, "Enabled, Enabled", "garbage no gi here", "")
	if got.ModeCurrent != modeUnknown || got.Err == "" {
		t.Errorf("Enabled+unparseable lgi must be Unknown+Err, got %+v", got)
	}
	if got.Geometry == geomDisabled {
		t.Errorf("must never report disabled on unparseable Enabled, got %+v", got)
	}

	// pending Unknown → Err.
	got = parseObservation(obsTestPCI, "Disabled, bogus", "", "")
	if got.ModePending != modeUnknown || got.Err == "" {
		t.Errorf("unrecognized pending must be Unknown+Err, got %+v", got)
	}

	// unparseable mode csv → Unknown+Err.
	got = parseObservation(obsTestPCI, "onlyonefield", "", "")
	if got.ModeCurrent != modeUnknown || got.Err == "" {
		t.Errorf("unparseable mode csv must be Unknown+Err, got %+v", got)
	}

	// Disabled → geometry "disabled".
	got = parseObservation(obsTestPCI, "Disabled, Disabled", "No MIG-enabled devices found.", "")
	if got.Err != "" || got.Geometry != geomDisabled {
		t.Errorf("Disabled must be clean geometry=disabled, got %+v", got)
	}

	// Enabled + GI 없음 → geometry ""(안전 baseline), 에러 아님.
	got = parseObservation(obsTestPCI, "Enabled, Enabled", "No GPU instances found.", "")
	if got.Err != "" || got.Geometry != "" || got.ModeCurrent != modeEnabled {
		t.Errorf("Enabled+no-GI must be clean geometry=\"\" (enabled-no-GI), got %+v", got)
	}
}

func TestParseDelimitedMessage(t *testing.T) {
	lgip, err := os.ReadFile("testdata/mig_lgip_580.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Job 이 termination-log 에 쓰는 실제 포맷을 재현(A30: Disabled + 실 lgip fixture).
	msg := "===PCI " + obsTestPCI + "===\n" +
		"<<<MODE\nDisabled, Disabled\n>>>MODE\n" +
		"<<<LGI\nNo MIG-enabled devices found.\n>>>LGI\n" +
		"<<<LGIP\n" + string(lgip) + "\n>>>LGIP\n"

	obs := parseDelimitedMessage(msg, []string{obsTestPCI})
	if len(obs) != 1 {
		t.Fatalf("want 1 observation, got %d", len(obs))
	}
	o := obs[0]
	if o.PCI != obsTestPCI {
		t.Errorf("PCI = %q, want %q", o.PCI, obsTestPCI)
	}
	if o.Err != "" {
		t.Errorf("clean A30 observation should have no Err, got %q", o.Err)
	}
	if o.ModeCurrent != modeDisabled || o.Geometry != geomDisabled {
		t.Errorf("A30 disabled expected, got mode=%q geometry=%q", o.ModeCurrent, o.Geometry)
	}
	profiles := ParseMigProfiles(o.LgipOutput)
	if len(profiles) == 0 {
		t.Fatalf("expected supported profiles parsed from lgip, got none")
	}
	found := false
	for _, p := range profiles {
		if p.Name == "1g.6gb" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 1g.6gb in supported profiles, got %+v", profiles)
	}

	// section 누락 → fail-closed(조용히 드롭 금지).
	missing := parseDelimitedMessage("===PCI 0000:99:00.0===\n", []string{obsTestPCI})
	if len(missing) != 1 || missing[0].Err == "" || missing[0].ModeCurrent != modeUnknown {
		t.Errorf("missing PCI section must fail-close, got %+v", missing)
	}
}

// TestObserveJobNameSeparatesStreams 는 같은 노드를 보는 두 관측 루프가 서로 다른 Job 이름을
// 쓰는지 고정한다. 이름이 같으면 Observe 의 "시작할 때 기존 Job 삭제" 규율이 상대의 Job 을
// 지워, 진 쪽은 fail-closed 관측만 받고 NodeDeviceReport 의 geometry 가 영영 갱신되지 않는다
// (ACPP 가 VerifyingAllocatableResources 에서 무한 정체한다).
func TestObserveJobNameSeparatesStreams(t *testing.T) {
	apply := observeJobName(StreamApply, "worker1")
	standing := observeJobName(StreamStanding, "worker1")
	if apply == standing {
		t.Fatalf("streams share job name %q; concurrent observers will delete each other", apply)
	}
	if same := observeJobName(StreamApply, "worker1"); same != apply {
		t.Errorf("job name must stay deterministic per stream: %q != %q", same, apply)
	}
	if other := observeJobName(StreamApply, "worker3"); other == apply {
		t.Errorf("job name must stay distinct per node, got %q for both", apply)
	}
}

func TestObserveJobRender(t *testing.T) {
	job := renderObserveJob("acpp-mig-observe-abc", "worker1", []string{obsTestPCI}, "harbor/kcloud-host-exec:latest", "kcloud-operator")

	pod := job.Spec.Template.Spec
	if !pod.HostPID {
		t.Errorf("HostPID must be true")
	}
	if pod.RestartPolicy != "Never" {
		t.Errorf("RestartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if pod.NodeName != "worker1" {
		t.Errorf("NodeName = %q, want worker1", pod.NodeName)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit must be 0")
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Errorf("TTLSecondsAfterFinished must be set")
	}
	ctr := pod.Containers[0]
	if ctr.Image != "harbor/kcloud-host-exec:latest" {
		t.Errorf("Image = %q", ctr.Image)
	}
	if ctr.SecurityContext == nil || ctr.SecurityContext.Privileged == nil || !*ctr.SecurityContext.Privileged {
		t.Errorf("container must be privileged")
	}
	script := ctr.Command[len(ctr.Command)-1]
	for _, want := range []string{"nsenter", obsTestPCI, "/dev/termination-log", "mig.mode.current"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "set -e") {
		t.Errorf("observe script must NOT use set -e (must capture failed nvidia-smi as text)")
	}
}

// TestChunkPCIsKeepsOneDevicePerJob 은 관측 Job 하나가 장치 하나만 담는지 고정한다.
// 관측 출력은 /dev/termination-log 로 나가고 kubelet 이 4096 바이트에서 자른다. MIG 를 켠
// A30 한 장의 출력이 약 2.5KB 라 두 장을 한 Job 에 담으면 뒤쪽 장치 섹션이 통째로 사라지고,
// 그 장치가 target 에서 빠지면서 ACPP 가 자기가 적용한 배치를 남의 것으로 판정해 굳는다.
func TestChunkPCIsKeepsOneDevicePerJob(t *testing.T) {
	in := []string{"0000:18:00.0", "0000:af:00.0", "0000:3b:00.0"}
	got := chunkPCIs(in)

	if len(got) != len(in) {
		t.Fatalf("묶음 수 = %d, want %d (장치당 Job 하나)", len(got), len(in))
	}
	var flat []string
	for _, c := range got {
		if len(c) != 1 {
			t.Errorf("묶음 크기 = %d, want 1 — 여러 장치를 한 Job 에 담으면 출력이 잘린다", len(c))
		}
		flat = append(flat, c...)
	}
	for i := range in {
		if i < len(flat) && flat[i] != in[i] {
			t.Errorf("순서 어긋남 [%d]: %q, want %q", i, flat[i], in[i])
		}
	}
}
