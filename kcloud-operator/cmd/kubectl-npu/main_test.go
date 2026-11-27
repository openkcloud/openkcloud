// main_test.go: kubectl-npu 사람용 텍스트 출력 포맷 테스트
// 상세: 같은 노드에 붙은 장치 여러 대가 표에서 서로 구분되는지 본다(벤더·모델·버전이
// 같고 PCI 만 다른 경우). describe 명령의 렌더·인자 파싱도 여기서 시험한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-28
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"kcloud-operator/pkg/npuctl"
)

// captureStdout 는 f 가 stdout 에 쓴 내용을 문자열로 돌려준다.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = orig
	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestPrintStatusText_DistinguishesDevicesOnSameNode 는 벤더·모델·버전이 모두 같고 PCI
// 주소만 다른 두 장치가 표에서 다른 행으로 읽히는지 본다 — PCI 를 행에 포함하지 않으면
// 나머지 열이 전부 같아져 두 줄이 완전히 동일해져 구분할 수 없다.
func TestPrintStatusText_DistinguishesDevicesOnSameNode(t *testing.T) {
	status := &npuctl.ClusterStatus{
		Nodes: []npuctl.NodeStatus{{
			Name: "k8s-worker1",
			Devices: []npuctl.DeviceStatus{
				{
					Vendor: "nvidia", Product: "nvidia/generic", Count: 1,
					DriverLoaded: true, DriverVersion: "580.173.02",
					PCIeAddress: "0000:18:00.0",
				},
				{
					Vendor: "nvidia", Product: "nvidia/generic", Count: 1,
					DriverLoaded: true, DriverVersion: "580.173.02",
					PCIeAddress: "0000:86:00.0",
				},
			},
		}},
	}

	out := captureStdout(t, func() { printStatusText(status) })

	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "k8s-worker1") {
			rows = append(rows, line)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("장치 2대에 대해 행 2개가 나와야 함, got %d:\n%s", len(rows), out)
	}
	if rows[0] == rows[1] {
		t.Errorf("같은 노드의 두 장치 행이 동일해 구분 불가:\n%q", rows[0])
	}
	for i, pci := range []string{"0000:18:00.0", "0000:86:00.0"} {
		if !strings.Contains(rows[i], pci) {
			t.Errorf("행 %d 에 PCI 주소 %q 가 없음: %q", i, pci, rows[i])
		}
	}
}

// TestPrintStatusText_NoteShowsExclusionReason 는 배제된 노드의 행에만 NOTE 가 붙고
// 배제되지 않은 노드에는 배지도 이유도 안 붙는지 본다. operator 가 붙인 라벨을 옮긴
// 값이므로 여기서 사유를 다시 판정하지 않는다.
func TestPrintStatusText_NoteShowsExclusionReason(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{
			Name: "k8s-master", Excluded: true, ExcludedReason: "control-plane",
			Devices: []npuctl.DeviceStatus{
				{Product: "nvidia/generic", PCIeAddress: "0000:01:00.0",
					Count: 1, DriverLoaded: true, DriverVersion: "580.159.03"},
			},
		},
		{
			Name: "k8s-worker1",
			Devices: []npuctl.DeviceStatus{
				{Product: "nvidia/a30", PCIeAddress: "0000:18:00.0",
					Count: 1, DriverLoaded: true, DriverVersion: "580.173.02"},
			},
		},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	var masterLine, workerLine string
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(ln, "k8s-master"):
			masterLine = ln
		case strings.Contains(ln, "k8s-worker1"):
			workerLine = ln
		}
	}
	if !strings.Contains(masterLine, "제외됨(control-plane)") {
		t.Errorf("배제된 노드 행에 사유가 없음: %q", masterLine)
	}
	if strings.Contains(workerLine, "제외됨") {
		t.Errorf("배제되지 않은 노드 행에 제외 표시가 붙음: %q", workerLine)
	}
}

// TestPrintStatusText_ShowsExclusionForNodeWithoutDevices 는 가속기 없는 배제 노드가 표에
// 남는지 본다. 배제 표시를 장치 루프 안에만 두면 status.devices 가 빈 노드는 행이 0개라
// 라벨이 붙어 있어도 화면에서 사라진다 — GPU 없는 control-plane 이 일반적 형상이다.
// 배제 아닌 장치 0개 노드는 기존대로 행을 내지 않는다.
func TestPrintStatusText_ShowsExclusionForNodeWithoutDevices(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "k8s-master", Excluded: true, ExcludedReason: "control-plane"},
		{Name: "k8s-idle"},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	var masterLine string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "k8s-master") {
			masterLine = ln
		}
	}
	if !strings.Contains(masterLine, "제외됨(control-plane)") {
		t.Errorf("장치 0개 배제 노드가 표에서 사라짐: %q", masterLine)
	}
	if strings.Contains(out, "k8s-idle") {
		t.Error("배제 아닌 장치 0개 노드에 행이 생김 — 기존 동작이 바뀌었다")
	}
}

// TestPrintStatusText_ClusterPolicyShowsVendorOperatorLines 는 NPUClusterPolicy 절에
// 벤더 operator 조건이 Ready 줄 바로 다음에, 전달받은 순서대로 나오는지 본다.
func TestPrintStatusText_ClusterPolicyShowsVendorOperatorLines(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{
		{
			Namespace: "kcloud", Name: "default",
			Ready: "True", ReadyReason: "AllComponentsReady",
			VendorOperators: []npuctl.VendorOperatorStatus{
				{Vendor: "nvidia", Status: "False", Reason: "NoClusterPolicy"},
				{Vendor: "rebellions", Status: "Unknown", Reason: "StatusProbeUnavailable"},
			},
		},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	section := out[strings.Index(out, "=== NPUClusterPolicy ==="):]
	section = section[:strings.Index(section, "\n\n=== DriverUpgradeState ===")]
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("헤더+Ready+벤더2줄=4줄이어야 하는데 %d줄: %q", len(lines), lines)
	}
	if lines[1] != "  kcloud/default: Ready=True (AllComponentsReady)" {
		t.Errorf("Ready 줄이 다르다: %q", lines[1])
	}
	if !strings.Contains(lines[2], "nvidia") || !strings.Contains(lines[2], "False") ||
		!strings.Contains(lines[2], "NoClusterPolicy") {
		t.Errorf("nvidia 벤더 줄이 다르다: %q", lines[2])
	}
	if !strings.Contains(lines[3], "rebellions") || !strings.Contains(lines[3], "Unknown") ||
		!strings.Contains(lines[3], "StatusProbeUnavailable") {
		t.Errorf("rebellions 벤더 줄이 다르다: %q", lines[3])
	}
}

// TestPrintStatusText_ClusterPolicyNoVendorOperatorsOutputsNoExtraLines 는 벤더 조건이
// 없는 정책에서 NPUClusterPolicy 절 출력이 종전과 동일한 한 줄만 내는지 본다(회귀 방지).
func TestPrintStatusText_ClusterPolicyNoVendorOperatorsOutputsNoExtraLines(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{
		{Namespace: "kcloud", Name: "default", Ready: "True", ReadyReason: "AllComponentsReady"},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	section := out[strings.Index(out, "=== NPUClusterPolicy ==="):]
	section = section[:strings.Index(section, "\n\n=== DriverUpgradeState ===")]
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더+Ready줄=2줄이어야 하는데 %d줄 (벤더 조건 없는데 줄이 더 남음): %q", len(lines), lines)
	}
	if lines[1] != "  kcloud/default: Ready=True (AllComponentsReady)" {
		t.Errorf("Ready 줄이 바뀜: %q", lines[1])
	}
}

// TestPrintStatusText_ClusterPolicyVendorColumnWidthMeasuredFromData 는 벤더 이름이
// 고정폭(12자)을 넘어도 그 줄만 밀리지 않고 STATUS 열이 다른 줄과 같은 자리에서
// 시작하는지 본다 — printTable 이 이미 겪은 증상(main.go:28 주석)과 같은 패턴이라
// 벤더 줄에도 같은 원칙(폭을 데이터에서 잰다)을 적용했는지 고정한다. 고정폭
// (`%-12s`)으로 되돌리면 이 시험이 FAIL 한다. "longvendorname" 은 실재 벤더가
// 아니라 폭 측정만 보려고 쓴 합성 이름이다.
func TestPrintStatusText_ClusterPolicyVendorColumnWidthMeasuredFromData(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{
		{
			Namespace: "kcloud", Name: "default",
			Ready: "True", ReadyReason: "AllComponentsReady",
			VendorOperators: []npuctl.VendorOperatorStatus{
				{Vendor: "longvendorname", Status: "False", Reason: "NoClusterPolicy"},
				{Vendor: "nvidia", Status: "Unknown", Reason: "StatusProbeUnavailable"},
			},
		},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	section := out[strings.Index(out, "=== NPUClusterPolicy ==="):]
	section = section[:strings.Index(section, "\n\n=== DriverUpgradeState ===")]
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("헤더+Ready+벤더2줄=4줄이어야 하는데 %d줄: %q", len(lines), lines)
	}

	longRow, shortRow := lines[2], lines[3]
	wantCol := runeColStart(t, longRow, "False")
	gotCol := runeColStart(t, shortRow, "Unknown")
	if gotCol != wantCol {
		t.Errorf("STATUS 열이 %d 에서 시작해야 하는데 %d 다(벤더 이름이 길어지면 밀림) — %q vs %q",
			wantCol, gotCol, longRow, shortRow)
	}
}

// TestPrintStatusText_ClusterPolicyNoReadyConditionStillShowsVendorLines 는 Ready
// 조건이 없는("no conditions") 정책에서도 벤더 조건이 있으면 벤더 줄이 나오는지
// 본다 — main.go 의 if/else 재구성(Ready 유무와 무관하게 벤더 루프를 공통으로
// 실행)이 지키는 조합이다.
func TestPrintStatusText_ClusterPolicyNoReadyConditionStillShowsVendorLines(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{
		{
			Namespace: "kcloud", Name: "default",
			VendorOperators: []npuctl.VendorOperatorStatus{
				{Vendor: "nvidia", Status: "False", Reason: "NoClusterPolicy"},
				{Vendor: "rebellions", Status: "Unknown", Reason: "StatusProbeUnavailable"},
			},
		},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	section := out[strings.Index(out, "=== NPUClusterPolicy ==="):]
	section = section[:strings.Index(section, "\n\n=== DriverUpgradeState ===")]
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("헤더+no conditions+벤더2줄=4줄이어야 하는데 %d줄: %q", len(lines), lines)
	}
	if lines[1] != "  kcloud/default: no conditions" {
		t.Errorf("no conditions 줄이 다르다: %q", lines[1])
	}
	if !strings.Contains(lines[2], "nvidia") || !strings.Contains(lines[2], "False") ||
		!strings.Contains(lines[2], "NoClusterPolicy") {
		t.Errorf("nvidia 벤더 줄이 다르다: %q", lines[2])
	}
	if !strings.Contains(lines[3], "rebellions") || !strings.Contains(lines[3], "Unknown") ||
		!strings.Contains(lines[3], "StatusProbeUnavailable") {
		t.Errorf("rebellions 벤더 줄이 다르다: %q", lines[3])
	}
}

// TestPrintStatusText_OmitsEmptyReasonParens 는 사유가 빈 배제 노드에 빈 괄호(`제외됨()`)를
// 내지 않는지 본다 — 사유가 있는 줄 알게 만든다.
func TestPrintStatusText_OmitsEmptyReasonParens(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "k8s-master", Excluded: true},
	}}
	out := captureStdout(t, func() { printStatusText(status) })
	if strings.Contains(out, "제외됨()") {
		t.Errorf("사유가 빈데 빈 괄호를 냈다: %q", out)
	}
	if !strings.Contains(out, "제외됨") {
		t.Errorf("배제 표시 자체가 사라졌다: %q", out)
	}
}

// tenstorrent/blackhole-p150(26자)처럼 긴 장치 이름이 고정폭 15자 열을 넘기면 그
// 행부터 PCI 이후 열이 전부 밀린다. 표를 눈으로 훑는 것이 이 명령의 용도이므로 한
// 행이라도 어긋나면 열을 따라갈 수 없다. 헤더와 모든 데이터 행에서 각 열의 시작
// 위치가 같은지 본다 — 폭을 하드코딩으로 되돌리면 이 단언이 걸린다.
func TestPrintStatusText_ColumnsAlignWithLongestDeviceName(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "k8s-worker3", Devices: []npuctl.DeviceStatus{
			{Product: "tenstorrent/blackhole-p150", PCIeAddress: "0000:af:00.0",
				Count: 1, DriverLoaded: true, DriverVersion: "2.8.0"},
		}},
		{Name: "rngd-1.cluster.local", Devices: []npuctl.DeviceStatus{
			{Product: "furiosa/rngd", PCIeAddress: "0000:27:00.0",
				Count: 1, DriverLoaded: true, DriverVersion: "2026.3.0"},
		}},
		{Name: "k8s-worker1", Devices: []npuctl.DeviceStatus{
			{Product: "nvidia/a30", PCIeAddress: "0000:18:00.0",
				Count: 1, DriverLoaded: true, DriverVersion: "580.173.02"},
		}},
	}}
	out := captureStdout(t, func() { printStatusText(status) })

	var section []string
	in := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "=== NodeDeviceReports ===") {
			in = true
			continue
		}
		if in {
			if strings.TrimSpace(ln) == "" {
				break
			}
			section = append(section, ln)
		}
	}
	if len(section) < 4 {
		t.Fatalf("장치 절이 헤더 + 3행이어야 한다: %q", section)
	}

	// 헤더의 PCI 열 시작 위치를 기준으로 삼는다. 데이터 행의 PCI 주소도 같은 자리에서
	// 시작해야 한다 — 앞 열이 넘치면 그 행만 오른쪽으로 밀린다.
	want := strings.Index(section[0], "PCI")
	if want <= 0 {
		t.Fatalf("헤더에 PCI 열이 없다: %q", section[0])
	}
	for _, ln := range section[1:] {
		got := strings.Index(ln, "0000:")
		if got != want {
			t.Errorf("PCI 열이 %d 에서 시작해야 하는데 %d 다 — 행: %q", want, got, ln)
		}
	}
}

// eightRuneKorean 은 8 rune·24 byte(rune 당 3byte)인 고정 테스트 셀이다 — len()(byte)과
// utf8.RuneCountInString(rune)이 다른 값을 내도록 일부러 공백 없이 고른다.
const eightRuneKorean = "가나다라마바사아"

// runeColStart 는 표 출력 한 줄에서 marker 가 시작하는 위치를 rune 오프셋으로 잰다.
// byte 오프셋(strings.Index 그대로)을 쓰면 한글이 섞인 접두 부분에서 실제 터미널 열과
// 어긋난 값이 나온다.
func runeColStart(t *testing.T, line, marker string) int {
	t.Helper()
	idx := strings.Index(line, marker)
	if idx < 0 {
		t.Fatalf("%q 에서 %q 를 못 찾았다", line, marker)
	}
	return utf8.RuneCountInString(line[:idx])
}

// TestPrintTable_한글REASON은절대rune폭으로찍힌다 는 REASON 열에 8-rune(24-byte) 한글
// 셀이 있을 때 SOURCE 열이 "가장 긴 셀의 rune 수 + 구분자 2칸" 위치에서 시작하는지
// 절대값으로 본다. 행끼리 상대 비교(두 행의 SOURCE 위치가 같다)만으로는 width 계산이
// len()(byte)으로 퇴행해도 그 잘못된 폭이 모든 행에 똑같이 적용돼 여전히 정렬돼 보여
// 못 걸린다 — 그래서 절대 위치를 rune 폭 공식으로 못박는다. len() 으로 재면 SOURCE 가
// 24+2=26 에서 시작해 want(8+2=10)와 어긋나 실패한다.
func TestPrintTable_한글REASON은절대rune폭으로찍힌다(t *testing.T) {
	if n := utf8.RuneCountInString(eightRuneKorean); n != 8 {
		t.Fatalf("테스트 전제가 깨졌다: rune 수 %d", n)
	}
	out := captureStdout(t, func() {
		printTable([]string{"REASON", "SOURCE"}, [][]string{
			{eightRuneKorean, "docs/a.md"}, // doc-cite-check: ignore
			{"ok", "docs/b.md"},            // doc-cite-check: ignore
		})
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	want := utf8.RuneCountInString(eightRuneKorean) + 2             // 열 폭(가장 긴 셀) + 구분자 2칸
	if got := runeColStart(t, lines[1], "docs/a.md"); got != want { // doc-cite-check: ignore
		t.Errorf("한글 행 SOURCE 가 rune %d 에서 시작해야 하는데 %d", want, got)
	}
	if got := runeColStart(t, lines[2], "docs/b.md"); got != want { // doc-cite-check: ignore
		t.Errorf("ASCII 행 SOURCE 가 rune %d 에서 시작해야 하는데 %d — byte 폭(len())으로 퇴행하면 %d 이 된다",
			want, got, len(eightRuneKorean)+2)
	}
}

// TestRenderTable_한글REASON은절대rune폭으로찍힌다 는 위와 같은 절대폭 성질을 renderTable
// (describe 하위 표 전용, 매 줄 앞 들여쓰기 2칸)에서도 본다.
func TestRenderTable_한글REASON은절대rune폭으로찍힌다(t *testing.T) {
	out := renderTable([]string{"REASON", "SOURCE"}, [][]string{
		{eightRuneKorean, "docs/a.md"}, // doc-cite-check: ignore
		{"ok", "docs/b.md"},            // doc-cite-check: ignore
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	want := 2 + utf8.RuneCountInString(eightRuneKorean) + 2         // 들여쓰기 2 + 열 폭 + 구분자 2칸
	if got := runeColStart(t, lines[1], "docs/a.md"); got != want { // doc-cite-check: ignore
		t.Errorf("한글 행 SOURCE 가 rune %d 에서 시작해야 하는데 %d", want, got)
	}
	if got := runeColStart(t, lines[2], "docs/b.md"); got != want { // doc-cite-check: ignore
		t.Errorf("ASCII 행 SOURCE 가 rune %d 에서 시작해야 하는데 %d — byte 폭(len())으로 퇴행하면 %d 이 된다",
			want, got, 2+len(eightRuneKorean)+2)
	}
}

// TestRenderNodeDetail_없는값은대시 는 health·evidence·policy 가 없을 때 렌더가 값을
// 지어내지 않고 "—" 로 표시하는지 본다.
func TestRenderNodeDetail_없는값은대시(t *testing.T) {
	d := &npuctl.NodeDetail{Name: "k8s-worker1"}
	out := renderNodeDetail(d)
	for _, want := range []string{"k8s-worker1", "HEALTH", "—"} {
		if !strings.Contains(out, want) {
			t.Errorf("출력에 %q 가 없다:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Healthy", "Verified"} {
		if strings.Contains(out, bad) {
			t.Errorf("없는 값을 지어냈다(%q):\n%s", bad, out)
		}
	}
}

// TestRenderNodeDetail_배제사유표시 는 배제된 노드의 사유가 NODE 줄에 붙는지 본다.
func TestRenderNodeDetail_배제사유표시(t *testing.T) {
	d := &npuctl.NodeDetail{Name: "k8s-master", Excluded: true, ExcludedReason: "control-plane"}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "제외됨(control-plane)") {
		t.Errorf("배제 표시가 없다:\n%s", out)
	}
}

// TestRenderNodeDetail_광고량은규칙으로가른다 는 ADVERTISED 가 cpu/memory 등 비가속기
// 리소스를 걸러내고 "/" 포함 가속기 리소스만 내는지 본다.
func TestRenderNodeDetail_광고량은규칙으로가른다(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name: "k8s-worker1",
		Allocatable: map[string]string{
			"cpu":            "32",
			"memory":         "128Gi",
			"pods":           "110",
			"hugepages-2Mi":  "0",
			"nvidia.com/gpu": "2",
		},
	}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "nvidia.com/gpu") || !strings.Contains(out, "2") {
		t.Errorf("가속기 리소스가 안 보인다:\n%s", out)
	}
	for _, bad := range []string{"cpu", "memory", "pods", "hugepages"} {
		if strings.Contains(out, bad) {
			t.Errorf("비가속기 리소스가 새어 나왔다(%q):\n%s", bad, out)
		}
	}
}

// TestParseDescribeArgs_노드아니면실패 는 첫 인자가 "node" 가 아니면 ok=false 인지 본다.
func TestParseDescribeArgs_노드아니면실패(t *testing.T) {
	if _, _, ok := parseDescribeArgs([]string{"pod", "foo"}); ok {
		t.Error("node 가 아닌 서브커맨드에 ok=true 를 냈다")
	}
}

// TestParseDescribeArgs_이름생략하면전체노드 는 "node" 만 주면(이름 생략) 전체 노드
// 조회로 받아들이는지 본다 — 서브커맨드("node") 자체가 없으면 여전히 실패다.
func TestParseDescribeArgs_이름생략하면전체노드(t *testing.T) {
	name, _, ok := parseDescribeArgs([]string{"node"})
	if !ok {
		t.Fatal("이름 생략에 ok=false 를 냈다 — 전체 노드 조회가 안 된다")
	}
	if name != "" {
		t.Errorf("이름 생략인데 name=%q", name)
	}
	if _, _, ok := parseDescribeArgs(nil); ok {
		t.Error("node 서브커맨드 자체가 없는데 ok=true 를 냈다")
	}
}

// TestParseDescribeArgs_이름생략플래그만 은 "node --json"(이름 생략 + 플래그)에서도
// 전체 노드 + JSON 이 함께 파싱되는지 본다.
func TestParseDescribeArgs_이름생략플래그만(t *testing.T) {
	name, asJSON, ok := parseDescribeArgs([]string{"node", "--json"})
	if !ok || name != "" || !asJSON {
		t.Errorf("got name=%q asJSON=%v ok=%v, want name=\"\" asJSON=true ok=true", name, asJSON, ok)
	}
}

// TestParseDescribeArgs_이름과JSON플래그 는 정상 인자에서 이름과 --json 플래그를
// 제대로 뽑는지 본다.
func TestParseDescribeArgs_이름과JSON플래그(t *testing.T) {
	name, asJSON, ok := parseDescribeArgs([]string{"node", "k8s-worker1", "--json"})
	if !ok || name != "k8s-worker1" || !asJSON {
		t.Errorf("got name=%q asJSON=%v ok=%v", name, asJSON, ok)
	}
	name, asJSON, ok = parseDescribeArgs([]string{"node", "k8s-worker1"})
	if !ok || name != "k8s-worker1" || asJSON {
		t.Errorf("got name=%q asJSON=%v ok=%v", name, asJSON, ok)
	}
}

// TestRenderNodeDetails_실패노드는건너뛰고빈줄은실제출력블록수를따른다 는 cmdDescribe
// 전체노드 경로와 같은 전제(조회 실패 노드는 details 목록에서 이미 걸러짐)로
// renderNodeDetails 를 시험한다. 빈 줄 판정에 루프 인덱스 `i > 0` 을 그대로 쓰면
// names=[실패, ok, ok] 처럼 앞쪽 노드가 실패할 때 처음 찍히는 블록 앞에도 빈 줄이
// 붙는다 — 실패한 노드가 details 슬라이스에 애초에 들어오지 않으므로 이 결함은
// 구조적으로 재발할 수 없다.
func TestRenderNodeDetails_실패노드는건너뛰고빈줄은실제출력블록수를따른다(t *testing.T) {
	// 빈 목록: 출력 없음.
	if out := renderNodeDetails(nil); out != "" {
		t.Errorf("빈 목록인데 출력이 났다: %q", out)
	}

	// 노드 하나: 앞뒤 빈 줄 없음.
	one := renderNodeDetails([]*npuctl.NodeDetail{{Name: "n1"}})
	if strings.HasPrefix(one, "\n") || strings.HasSuffix(one, "\n\n") {
		t.Errorf("노드 하나인데 여분의 빈 줄이 있다: %q", one)
	}
	if strings.Count(one, "\n\n") != 0 {
		t.Errorf("노드 하나인데 블록 구분 빈 줄이 났다: %q", one)
	}

	// 목록 조회와 상세 조회 사이 경합으로 첫 노드(n0)가 실패해 details 에서 빠지고
	// n1·n2 만 남은 상황을 재현 — cmdDescribe 가 채우는 details 와 동일한 모양이다.
	details := []*npuctl.NodeDetail{{Name: "n1"}, {Name: "n2"}}
	out := renderNodeDetails(details)
	if strings.HasPrefix(out, "\n") {
		t.Errorf("첫 노드가 실패해도 실제 첫 출력 블록 앞엔 빈 줄이 없어야 한다: %q", out)
	}
	n1Block := renderNodeDetail(details[0])
	n2Block := renderNodeDetail(details[1])
	want := n1Block + "\n" + n2Block
	if out != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

// TestRenderNodeDetail_Health사유없으면빈괄호없음 은 Reason 이 빈 Health 에서 "Healthy ()"
// 같은 빈 괄호를 내지 않는지 본다.
func TestRenderNodeDetail_Health사유없으면빈괄호없음(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name:   "k8s-worker1",
		Health: &npuctl.HealthView{State: "Healthy", Reason: "", AllocationAllowed: true},
	}
	out := renderNodeDetail(d)
	if strings.Contains(out, "()") {
		t.Errorf("사유가 빈데 빈 괄호를 냈다:\n%s", out)
	}
	if !strings.Contains(out, "Healthy") {
		t.Errorf("HEALTH 상태가 안 보인다:\n%s", out)
	}
}

// TestRenderNodeDetail_Evidence식별자없음은명시 는 Vendor·Level 이 둘 다 비고 ExpiresAt 만
// 있는 evidence(라이브 k8s-master 실재 상태)가 라벨 없이 만료일만 흘리지 않고
// "식별자 없음" 으로 명시되는지 본다.
func TestRenderNodeDetail_Evidence식별자없음은명시(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name:     "k8s-master",
		Evidence: []npuctl.EvidenceView{{Vendor: "", Level: "", ExpiresAt: "2026-08-04T15:23:46+09:00"}},
	}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "식별자 없음") {
		t.Errorf("식별자 없는 evidence 가 명시되지 않았다:\n%s", out)
	}
	if !strings.Contains(out, "2026-08-04T15:23:46+09:00") {
		t.Errorf("만료 시각이 표에서 사라졌다:\n%s", out)
	}
}

// TestRenderNodeDetail_Evidence식별자없음표열정렬 은 VENDOR·LEVEL 이 둘 다 빈 evidence
// 때문에 EXPIRES 열 위치가 실제 vendor·level 이 있는 표와 달라지지 않는지 본다.
// "식별자 없음"(16바이트) 문자열을 VENDOR 칸에 직접 넣으면 그 길이가 헤더("VENDOR",
// 6바이트)보다 커져 renderTable 의 폭 측정(len() 기준)이 어긋나고, EXPIRES 열이 실제
// vendor 가 있는 표보다 오른쪽으로 밀린다 — "—" 로 바꾸면 두 표의 EXPIRES 열이 같은
// 자리에서 시작한다.
func TestRenderNodeDetail_Evidence식별자없음표열정렬(t *testing.T) {
	withVendor := renderNodeDetail(&npuctl.NodeDetail{
		Name:     "k8s-worker1",
		Evidence: []npuctl.EvidenceView{{Vendor: "nvidia", Level: "L1", ExpiresAt: "2026-08-04T15:23:46+09:00"}},
	})
	withoutVendor := renderNodeDetail(&npuctl.NodeDetail{
		Name:     "k8s-master",
		Evidence: []npuctl.EvidenceView{{Vendor: "", Level: "", ExpiresAt: "2026-08-04T15:23:46+09:00"}},
	})
	headerLine := func(out string) string {
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "VENDOR") && strings.Contains(ln, "EXPIRES") {
				return ln
			}
		}
		return ""
	}
	h1, h2 := headerLine(withVendor), headerLine(withoutVendor)
	if h1 == "" || h2 == "" {
		t.Fatalf("EVIDENCE 헤더를 못 찾았다: %q / %q", h1, h2)
	}
	pos1, pos2 := strings.Index(h1, "EXPIRES"), strings.Index(h2, "EXPIRES")
	if pos1 != pos2 {
		t.Errorf("식별자 없는 evidence 때문에 EXPIRES 열 위치가 달라졌다: 실제 vendor=%d, 식별자 없음=%d", pos1, pos2)
	}
}

// TestRenderNodeDetail_장치표에헤더와정렬이있다 는 DEVICES 절이 PRODUCT/PCI 헤더를
// 갖고, 긴 제품명이 있어도 PCI 열이 모든 행에서 같은 자리에 오는지 본다.
func TestRenderNodeDetail_장치표에헤더와정렬이있다(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name: "k8s-worker1",
		Devices: []npuctl.DeviceStatus{
			{
				Product: "tenstorrent/blackhole-p150", PCIeAddress: "0000:af:00.0",
				Count: 1, DriverLoaded: true, DriverVersion: "2.8.0",
			},
			{
				Product: "nvidia/a30", PCIeAddress: "0000:18:00.0",
				Count: 1, DriverLoaded: true, DriverVersion: "580.173.02",
			},
		},
	}
	out := renderNodeDetail(d)
	var section []string
	in := false
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "DEVICES:") {
			in = true
			continue
		}
		if in {
			if ln == "" || !strings.HasPrefix(ln, "  ") {
				break
			}
			section = append(section, ln)
		}
	}
	if len(section) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(section), section)
	}
	want := strings.Index(section[0], "PCI")
	if want <= 0 {
		t.Fatalf("헤더에 PCI 열이 없다: %q", section[0])
	}
	for _, ln := range section[1:] {
		if got := strings.Index(ln, "0000:"); got != want {
			t.Errorf("PCI 열이 %d 에서 시작해야 하는데 %d 다: %q", want, got, ln)
		}
	}
}

// TestRenderNodeDetail_Health장치행이표다 는 HEALTH 요약 줄은 산문으로 남고
// 그 아래 장치별 행이 PCI/STATE/REASON 헤더 있는 표인지 본다.
func TestRenderNodeDetail_Health장치행이표다(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name: "k8s-worker1",
		Health: &npuctl.HealthView{
			State: "Healthy", AllocationAllowed: true,
			Devices: []npuctl.DeviceHealthView{
				{PCIAddress: "0000:18:00.0", State: "Healthy"},
				{PCIAddress: "0000:86:00.0", State: "Healthy"},
			},
		},
	}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "HEALTH:     Healthy allocationAllowed=true\n") {
		t.Errorf("요약 줄이 산문이 아니다:\n%s", out)
	}
	if !strings.Contains(out, "STATE") {
		t.Errorf("장치별 행 표 헤더가 없다:\n%s", out)
	}
}

// TestRenderNodeDetail_광고표에헤더가있다 는 ADVERTISED 가 RESOURCE/QTY 헤더 있는
// 표로 나오는지 본다.
func TestRenderNodeDetail_광고표에헤더가있다(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name:        "k8s-worker1",
		Allocatable: map[string]string{"nvidia.com/gpu": "2"},
	}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "RESOURCE") || !strings.Contains(out, "QTY") {
		t.Errorf("광고 표 헤더가 없다:\n%s", out)
	}
}

// TestRenderNodeDetail_정책표에헤더가있다 는 POLICY 가 NAME/VENDOR/PHASE 헤더 있는
// 표로 나오는지 본다.
func TestRenderNodeDetail_정책표에헤더가있다(t *testing.T) {
	d := &npuctl.NodeDetail{
		Name:     "k8s-worker1",
		Policies: []npuctl.PolicyView{{Name: "demo-mig", Vendor: "nvidia", Phase: "Ready"}},
	}
	out := renderNodeDetail(d)
	if !strings.Contains(out, "PHASE") {
		t.Errorf("정책 표 헤더가 없다:\n%s", out)
	}
	if !strings.Contains(out, "demo-mig") || !strings.Contains(out, "Ready") {
		t.Errorf("정책 값이 안 보인다:\n%s", out)
	}
}

// TestRenderTable_폭은데이터에서잰다 는 헤더보다 긴 값이 있을 때 그 열 전체가
// 그 값 길이에 맞춰 넓어지는지 본다 — printTable 과 같은 원칙(고정폭 하드코딩 금지).
// 이 저장소는 이 규칙을 어겨 실제로 라이브 표가 어긋난 전례가 있다
// (TestPrintStatusText_ColumnsAlignWithLongestDeviceName 참조).
func TestRenderTable_폭은데이터에서잰다(t *testing.T) {
	out := renderTable(
		[]string{"PRODUCT", "PCI"},
		[][]string{
			{"tenstorrent/blackhole-p150", "0000:af:00.0"},
			{"nvidia/a30", "0000:18:00.0"},
		},
	)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	want := strings.Index(lines[0], "PCI")
	if want <= 0 {
		t.Fatalf("헤더에 PCI 열이 없다: %q", lines[0])
	}
	for _, ln := range lines[1:] {
		if got := strings.Index(ln, "0000:"); got != want {
			t.Errorf("PCI 열이 %d 에서 시작해야 하는데 %d 다: %q", want, got, ln)
		}
	}
}

// TestRenderTable_빈행이면헤더만 은 rows 가 비었을 때 헤더 줄만 나오는지 본다.
func TestRenderTable_빈행이면헤더만(t *testing.T) {
	out := renderTable([]string{"A", "B"}, nil)
	if strings.TrimRight(out, "\n") != "  A  B" {
		t.Errorf("빈 표 출력이 다르다: %q", out)
	}
}

// TestRenderTable_들여쓰기 는 헤더·행이 모두 접두어(들여쓰기 두 칸)를 갖는지 본다 —
// DEVICES: 같은 절 라벨 아래 붙는 표라 왼쪽에 여백이 있어야 절과 구분된다.
func TestRenderTable_들여쓰기(t *testing.T) {
	out := renderTable([]string{"A"}, [][]string{{"x"}})
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(ln, "  ") {
			t.Errorf("들여쓰기 없는 줄: %q", ln)
		}
	}
}

// TestParsePolicyArgs_Create 는 "policy create" 인자를 파싱하는지 본다.
func TestParsePolicyArgs_Create(t *testing.T) {
	action, req, _, dryRun, err := parsePolicyArgs([]string{
		"create", "--name", "p1", "--vendor", "nvidia", "--node", "n1", "--intent", "partitioned",
		"--profile", "1g.5gb", "--count-per-device", "7", "--dry-run"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if action != "create" {
		t.Errorf("action: got %q, want %q", action, "create")
	}
	if req.Name != "p1" {
		t.Errorf("Name: got %q, want %q", req.Name, "p1")
	}
	if req.Vendor != "nvidia" {
		t.Errorf("Vendor: got %q, want %q", req.Vendor, "nvidia")
	}
	if req.NodeName != "n1" {
		t.Errorf("NodeName: got %q, want %q", req.NodeName, "n1")
	}
	if req.Intent != "partitioned" {
		t.Errorf("Intent: got %q, want %q", req.Intent, "partitioned")
	}
	if req.Profile != "1g.5gb" {
		t.Errorf("Profile: got %q, want %q", req.Profile, "1g.5gb")
	}
	if req.CountPerDevice != 7 {
		t.Errorf("CountPerDevice: got %d, want %d", req.CountPerDevice, 7)
	}
	if !dryRun {
		t.Errorf("dryRun: got %v, want true", dryRun)
	}
}

// TestParsePolicyArgs_Delete 는 "policy delete" 인자를 파싱하는지 본다.
func TestParsePolicyArgs_Delete(t *testing.T) {
	action, _, name, _, err := parsePolicyArgs([]string{"delete", "p1"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if action != "delete" {
		t.Errorf("action: got %q, want %q", action, "delete")
	}
	if name != "p1" {
		t.Errorf("name: got %q, want %q", name, "p1")
	}
}

// TestParsePolicyArgs_Unknown 는 알 수 없는 action 에 오류를 낸다.
func TestParsePolicyArgs_Unknown(t *testing.T) {
	_, _, _, _, err := parsePolicyArgs([]string{"frob"})
	if err == nil {
		t.Errorf("error: got nil, want error for unknown action")
	}
}

// assertColumnsInOrder 는 한 줄 안에서 want 의 문자열들이 그 순서대로 나오는지 본다.
// 매 검색을 이전 일치 지점 뒤부터 다시 시작하므로, 앞선 값의 일부가 뒤 값과 우연히
// 같아도(예: 자릿수) 순서가 뒤바뀌면 반드시 걸린다 — Contains 만으로는 열 순서가
// 바뀌어도 통과한다.
func assertColumnsInOrder(t *testing.T, line string, want []string) {
	t.Helper()
	rest := line
	consumed := 0
	for _, w := range want {
		idx := strings.Index(rest, w)
		if idx < 0 {
			t.Errorf("%q 를 순서대로 찾지 못했다(그 앞부분까지 이미 소비: %q): 전체 줄 %q", w, line[:consumed], line)
			return
		}
		rest = rest[idx+len(w):]
		consumed += idx + len(w)
	}
}

// assertHeaderExact 는 표 첫 줄이 want 를 정확히 그 순서·개수로만 담는지 본다.
// assertColumnsInOrder(부분 문자열 검색)는 헤더가 "PCI"→"PCIX"처럼 늘어나도 옛 값을
// 여전히 포함해 통과한다 — 문서가 이 헤더 문자열을 그대로 인용하므로 늘거나 줄어드는
// 변경도 반드시 걸려야 한다. printTable 의 헤더 셀은 공백을 포함하지 않으므로
// strings.Fields 로 열을 나눠 정확히 비교한다.
func assertHeaderExact(t *testing.T, header string, want []string) {
	t.Helper()
	got := strings.Fields(header)
	if len(got) != len(want) {
		t.Fatalf("헤더 열 개수가 다르다: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("헤더 열 %d: got %q, want %q(전체: got %v, want %v)", i, got[i], w, got, want)
		}
	}
}

// TestPrintDevicesText_헤더와열순서 는 devices 표의 머리 문자열과 열 순서, 데이터 행의
// 값 순서를 고정한다. 발표·문서가 이 출력을 인용하므로 머리가 바뀌면 문서가 낡는다.
func TestPrintDevicesText_헤더와열순서(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name: "k8s-worker1",
		Devices: []npuctl.DeviceStatus{{
			Vendor: "nvidia", Model: "a30", PCIeAddress: "0000:18:00.0",
			Count: 2, DriverLoaded: true, DriverVersion: "580.173.02",
		}},
	}}}
	out := captureStdout(t, func() { printDevicesText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NODE", "VENDOR", "MODEL", "PCI", "COUNT", "DRIVER_VER", "LOADED"})
	assertColumnsInOrder(t, lines[1], []string{"k8s-worker1", "nvidia", "a30", "0000:18:00.0", "2", "580.173.02", "true"})
}

// TestPrintDevicesText_빈값은대시 는 Model·PCI·DriverVersion 이 빈 장치 행에서 그 칸이
// "—" 로 나오는지 본다(renderNodeDetail 의 기존 표기 규칙과 동일) — 빈 칸으로 비워
// 값이 없다는 사실을 숨기지 않는다.
func TestPrintDevicesText_빈값은대시(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name:    "k8s-worker1",
		Devices: []npuctl.DeviceStatus{{Vendor: "nvidia"}},
	}}}
	out := captureStdout(t, func() { printDevicesText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	if n := strings.Count(lines[1], "—"); n != 3 {
		t.Errorf("MODEL·PCI·DRIVER_VER 세 칸이 대시여야 하는데 대시 %d개: %q", n, lines[1])
	}
}

// TestPrintVendorsText_헤더와열순서 는 vendors 표의 머리 문자열과 열 순서, 데이터 행의
// 값 순서를 고정한다.
func TestPrintVendorsText_헤더와열순서(t *testing.T) {
	status := &npuctl.ClusterStatus{Vendors: []npuctl.VendorStatus{
		{
			Name: "rngd", SubVendorOf: "furiosa", Enabled: true,
			DriverInstallPolicies: []npuctl.DIPStatus{{Name: "dip1", DesiredVersion: "2026.3.0"}},
		},
	}}
	out := captureStdout(t, func() { printVendorsText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NAME", "SUBVENDOR_OF", "ENABLED", "VERSION"})
	assertColumnsInOrder(t, lines[1], []string{"rngd", "furiosa", "true", "2026.3.0"})
}

// TestPrintVendorsText_최상위벤더와미설정은대시 는 SubVendorOf 가 없는 최상위 벤더와
// DriverInstallPolicy 가 하나도 없는 벤더에서 그 칸이 "—" 로 나오는지, Enabled=false 는
// 값을 지어내지 않고 "false" 그대로 나오는지 본다.
func TestPrintVendorsText_최상위벤더와미설정은대시(t *testing.T) {
	status := &npuctl.ClusterStatus{Vendors: []npuctl.VendorStatus{
		{Name: "tenstorrent", Enabled: false},
	}}
	out := captureStdout(t, func() { printVendorsText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	row := lines[1]
	if !strings.Contains(row, "false") {
		t.Errorf("Enabled=false 가 안 보인다: %q", row)
	}
	if n := strings.Count(row, "—"); n != 2 {
		t.Errorf("SUBVENDOR_OF·VERSION 두 칸이 대시여야 하는데 대시 %d개: %q", n, row)
	}
}

// TestPrintHealthText_헤더와열순서 는 health 표의 머리 문자열과 열 순서를 고정한다.
// 장치별 분해가 있으면 장치마다 한 행을 낸다.
func TestPrintHealthText_헤더와열순서(t *testing.T) {
	details := []*npuctl.NodeDetail{{
		Name: "k8s-worker1",
		Health: &npuctl.HealthView{
			State: "Healthy", AllocationAllowed: true,
			Devices: []npuctl.DeviceHealthView{
				{PCIAddress: "0000:18:00.0", State: "Healthy", Reason: ""},
				{PCIAddress: "0000:86:00.0", State: "Degraded", Reason: "overheat"},
			},
		},
	}}
	out := captureStdout(t, func() { printHealthText(details) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NODE", "PCI", "STATE", "REASON", "ALLOCATION_ALLOWED"})
	assertColumnsInOrder(t, lines[1], []string{"k8s-worker1", "0000:18:00.0", "Healthy", "—", "true"})
	assertColumnsInOrder(t, lines[2], []string{"k8s-worker1", "0000:86:00.0", "Degraded", "overheat", "true"})
}

// TestPrintHealthText_장치분해없으면집계행_Health없으면제외 는 두 가지를 함께 본다:
// 장치별 분해가 없는 노드는 노드 집계 State/Reason 으로 한 행을 내고(PCI·빈 Reason 은
// 대시), Health 자체가 없는 노드(가속기 없는 노드가 보통 이렇다)는 아예 행을 내지
// 않는다 — 값을 지어내 채우지 않는다.
func TestPrintHealthText_장치분해없으면집계행_Health없으면제외(t *testing.T) {
	details := []*npuctl.NodeDetail{
		{Name: "k8s-master"}, // Health == nil
		{Name: "k8s-worker1", Health: &npuctl.HealthView{State: "Healthy", AllocationAllowed: true}},
	}
	out := captureStdout(t, func() { printHealthText(details) })
	if strings.Contains(out, "k8s-master") {
		t.Errorf("Health 가 없는 노드가 행으로 나왔다:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	row := lines[1]
	if !strings.Contains(row, "k8s-worker1") || !strings.Contains(row, "Healthy") || !strings.Contains(row, "true") {
		t.Errorf("노드 집계 행 값이 안 보인다: %q", row)
	}
	if n := strings.Count(row, "—"); n != 2 {
		t.Errorf("PCI·REASON 두 칸이 대시여야 하는데 대시 %d개: %q", n, row)
	}
}

// TestPrintDriverStatusText_헤더와열순서 는 driver status 표의 머리 문자열과 열 순서,
// 데이터 행의 값 순서를 고정한다.
func TestPrintDriverStatusText_헤더와열순서(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name: "k8s-worker1",
		Upgrades: []npuctl.UpgradeStatus{{
			Vendor: "nvidia", Model: "a30", State: "Idle",
			CurrentVersion: "580.173.02", DesiredVersion: "580.173.02",
		}},
	}}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NODE", "VENDOR", "MODEL", "STATE", "CURRENT", "DESIRED"})
	assertColumnsInOrder(t, lines[1], []string{"k8s-worker1", "nvidia", "a30", "Idle", "580.173.02", "580.173.02"})
}

// TestPrintDriverStatusText_빈값은대시 는 Model·State·Current·Desired 가 빈 업그레이드
// 행에서 그 칸이 "—" 로 나오는지 본다(devices·vendors·health 와 같은 표기 규칙).
func TestPrintDriverStatusText_빈값은대시(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name:     "k8s-worker1",
		Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia"}},
	}}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	if n := strings.Count(lines[1], "—"); n != 4 {
		t.Errorf("MODEL·STATE·CURRENT·DESIRED 네 칸이 대시여야 하는데 대시 %d개: %q", n, lines[1])
	}
}

// TestPrintDriverStatusText_Failed상세 는 Failed 행의 사유만 표 아래 상세 절에 나오는지 본다. 사유가 있어도 Idle·HostAhead
// 행의 것은 나오지 않는다.
func TestPrintDriverStatusText_Failed상세(t *testing.T) {
	msg := "설치 전 점검 실패(노드 cordon 하지 않음): hold 된 패키지(libx11-6)가 드라이버 설치를 막는다"
	hostAheadMsg := "호스트 드라이버 590.48.01 가 정책 버전 580.126.09 보다 높다"
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "worker1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Failed", Message: msg}}},
		{Name: "worker2", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Idle", Message: "검증 성공(job)"}}},
		{Name: "worker3", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "HostAhead", Message: hostAheadMsg}}},
	}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	if !strings.Contains(out, "\n\nFailed 상세:\n  worker1/nvidia: "+msg+"\n") {
		t.Errorf("Failed 상세 줄 없음: %q", out)
	}
	if strings.Contains(out, "검증 성공(job)") {
		t.Errorf("Idle 행의 메시지가 상세에 나왔다: %q", out)
	}
	if strings.Contains(out, hostAheadMsg) {
		t.Errorf("HostAhead 행의 메시지가 상세에 나왔다: %q", out)
	}
}

// TestPrintDriverStatusText_Failed메시지없으면상세없음 은 사유가 빈 Failed 행에 상세 절을 만들지 않는다.
func TestPrintDriverStatusText_Failed메시지없으면상세없음(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "worker1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Failed"}}},
	}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	if strings.Contains(out, "Failed 상세") {
		t.Errorf("사유 없는 Failed 에 상세 절: %q", out)
	}
}

// TestPrintStatusText_DUS절에도Failed상세 는 status 명령의 DriverUpgradeState 절에도
// Failed 행의 사유를 표 아래 상세 절에 나타내는지 본다.
func TestPrintStatusText_DUS절에도Failed상세(t *testing.T) {
	msg := "설치 전 점검 실패(노드 cordon 하지 않음): hold 된 패키지(libx11-6)가 드라이버 설치를 막는다"
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "worker1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Failed", Message: msg}}},
	}}
	out := captureStdout(t, func() { printStatusText(status) })
	if !strings.Contains(out, "\n\nFailed 상세:\n  worker1/nvidia: "+msg+"\n") {
		t.Errorf("status 의 Failed 상세 없음: %q", out)
	}
}

// TestBuildDiagnoseRows_HostAhead에는사유미부착 는 diagnose 의 HostAhead 행에 사유가 붙지
// 않는지 본다 — HostAhead 는 호스트 드라이버가 목표보다 높은 상태이고 실패가 아니므로
// 실패 사유를 붙이지 않는다.
func TestBuildDiagnoseRows_HostAhead에는사유미부착(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "worker1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "HostAhead", Message: "호스트가 더 높다"}}},
	}}
	for _, r := range buildDiagnoseRows(status, nil, nil, nil) {
		if r.category == "upgrade" && strings.Contains(r.detail, "호스트가 더 높다") {
			t.Errorf("HostAhead 행에 사유가 붙었다: %q", r.detail)
		}
	}
}

// TestBuildDiagnoseRows_Failed사유포함 은 diagnose 의 Failed upgrade 행에 DUS 사유가 붙는지 본다.
func TestBuildDiagnoseRows_Failed사유포함(t *testing.T) {
	msg := "설치 실패(job): 되돌릴 이전 버전이 없어 롤백하지 않는다"
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "worker1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Failed", Message: msg}}},
	}}
	for _, r := range buildDiagnoseRows(status, nil, nil, nil) {
		if r.category == "upgrade" && r.node == "worker1" {
			if !strings.Contains(r.detail, "state=Failed") || !strings.Contains(r.detail, " — "+msg) {
				t.Errorf("detail=%q", r.detail)
			}
			return
		}
	}
	t.Error("Failed upgrade 행이 없다")
}

// TestPrintAllocationStatusText_헤더와열순서 는 allocation status 표의 머리 문자열과
// 열 순서, 데이터 행의 값 순서를 고정한다. 열 순서는 AllocationRow 필드 선언 순서다.
func TestPrintAllocationStatusText_헤더와열순서(t *testing.T) {
	rows := []npuctl.AllocationRow{{
		Vendor: "nvidia", Enabled: true, Declared: "devicePlugin",
		DevicePluginNodes: []string{"k8s-worker1", "k8s-worker2"},
	}}
	out := captureStdout(t, func() { printAllocationStatusText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0],
		[]string{
			"VENDOR", "ENABLED", "DECLARED", "DEVICE_PLUGIN_NODES", "DRA_NODES",
			"AMBIGUOUS_PRODUCT_NODES", "RETIRED_NODES", "REFUSED",
		})
	assertColumnsInOrder(t, lines[1], []string{"nvidia", "true", "devicePlugin", "k8s-worker1,k8s-worker2"})
}

// TestPrintAllocationStatusText_빈목록과미선언은대시 는 Declared 가 빈 벤더(꺼진 벤더)와
// 네 노드 목록이 모두 빈 벤더에서 그 칸들이 "—" 로 나오는지 본다 — AmbiguousProductNodes·
// RetiredNodes 는 DevicePluginNodes·DRANodes 어느 쪽에도 접히지 않고 독립된 열로 남아야 한다.
func TestPrintAllocationStatusText_빈목록과미선언은대시(t *testing.T) {
	rows := []npuctl.AllocationRow{
		{Vendor: "rebellions", Enabled: false},
		{
			Vendor: "furiosa", Enabled: true, Declared: "dra",
			AmbiguousProductNodes: []string{"hybrid-node"},
			RetiredNodes:          []string{"retired-node"},
		},
	}
	out := captureStdout(t, func() { printAllocationStatusText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("헤더 + 데이터 2행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	if n := strings.Count(lines[1], "—"); n != 6 {
		t.Errorf("rebellions 행은 DECLARED·네 노드 목록·REFUSED 여섯 칸이 대시여야 하는데 대시 %d개: %q", n, lines[1])
	}
	if !strings.Contains(lines[2], "hybrid-node") {
		t.Errorf("AmbiguousProductNodes 값이 안 보인다: %q", lines[2])
	}
	if !strings.Contains(lines[2], "retired-node") {
		t.Errorf("RetiredNodes 값이 안 보인다: %q", lines[2])
	}
	if n := strings.Count(lines[2], "—"); n != 3 {
		t.Errorf("furiosa 행은 DEVICE_PLUGIN_NODES·DRA_NODES·REFUSED 세 칸만 대시여야 하는데"+
			"(ambiguous·retired 는 값이 있음) 대시 %d개: %q", n, lines[2])
	}
}

// TestPrintAllocationStatusText_거절노드는사유와함께REFUSED열에 는 Switch 에 Applied=false
// 항목이 있으면 REFUSED 열에 "노드(사유)" 로 나오고, Applied=true 항목은 그 열에 안 나오는지
// 본다.
func TestPrintAllocationStatusText_거절노드는사유와함께REFUSED열에(t *testing.T) {
	rows := []npuctl.AllocationRow{{
		Vendor: "nvidia", Enabled: true, Declared: "dra",
		Switch: []npuctl.AllocationSwitch{
			{Node: "n1", Mode: "dra", Applied: false, Reason: "장치를 쥔 워크로드가 있다"},
			{Node: "n2", Mode: "dra", Applied: true},
		},
	}}
	out := captureStdout(t, func() { printAllocationStatusText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	if !strings.Contains(lines[1], "n1(장치를 쥔 워크로드가 있다)") {
		t.Errorf("REFUSED 열에 거절 노드·사유가 안 보인다: %q", lines[1])
	}
	if strings.Contains(lines[1], "n2(") {
		t.Errorf("Applied=true 노드가 REFUSED 열에 나온다: %q", lines[1])
	}
}

// TestParseDriverRollbackArgs_노드필수 는 --node 가 없으면 에러인지 본다.
func TestParseDriverRollbackArgs_노드필수(t *testing.T) {
	if _, err := parseDriverRollbackArgs([]string{"--vendor", "nvidia"}); err == nil {
		t.Errorf("--node 없이는 에러여야 함")
	}
}

// TestParseDriverRollbackArgs_전체인자 는 네 플래그가 모두 올바르게 채워지는지 본다.
func TestParseDriverRollbackArgs_전체인자(t *testing.T) {
	got, err := parseDriverRollbackArgs([]string{"--node", "n1", "--vendor", "nvidia", "--model", "a30", "--force"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := driverRollbackArgs{node: "n1", vendor: "nvidia", model: "a30", force: true}
	if got != want {
		t.Errorf("파싱 결과 불일치: got %+v, want %+v", got, want)
	}
}

// TestParseDriverRollbackArgs_Force생략기본값false 는 --force 를 안 쓰면 false 로 남는지
// 본다(확인 게이트가 기본으로 걸려 있어야 한다).
func TestParseDriverRollbackArgs_Force생략기본값false(t *testing.T) {
	got, err := parseDriverRollbackArgs([]string{"--node", "n1"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.force {
		t.Errorf("--force 를 안 썼는데 force=true 로 파싱됨")
	}
}

// errReader 는 항상 에러를 내는 io.Reader — confirmRollback(force=true) 가 in 을 전혀
// 읽지 않는다는 것을 증명하는 데 쓴다(읽었다면 이 에러가 나야 한다).
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, fmt.Errorf("읽으면 안 되는데 읽음") }

// TestConfirmRollback_Force면리더를읽지않고통과 는 force=true 면 in 을 전혀 읽지 않고도
// 바로 통과하는지 본다 — 비대화식(스크립트·사고 대응) 경로가 항상 있어야 한다는 계약.
func TestConfirmRollback_Force면리더를읽지않고통과(t *testing.T) {
	ok, msg := confirmRollback(errReader{}, true)
	if !ok || msg != "" {
		t.Errorf("force=true 면 (true, \"\") 여야 하는데 got (%v, %q)", ok, msg)
	}
}

// TestConfirmRollback_y입력이면통과 는 force=false 라도 입력 첫 줄이 y/yes(대소문자
// 무관)면 통과하는지 본다.
func TestConfirmRollback_y입력이면통과(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", "YES\n", "  y  \n"} {
		ok, msg := confirmRollback(strings.NewReader(in), false)
		if !ok || msg != "" {
			t.Errorf("입력 %q 는 통과해야 하는데 got (%v, %q)", in, ok, msg)
		}
	}
}

// TestConfirmRollback_다른입력이면거절_force안내 는 y/yes 가 아닌 입력(빈 입력 포함)이면
// 거절하고, 그 메시지가 반드시 "--force" 를 언급하는지 본다 — 대화식만으로는 스크립트가
// 못 쓰고 시험도 약해진다.
func TestConfirmRollback_다른입력이면거절_force안내(t *testing.T) {
	for _, in := range []string{"n\n", "no\n", "", "\n"} {
		ok, msg := confirmRollback(strings.NewReader(in), false)
		if ok {
			t.Errorf("입력 %q 는 거절해야 하는데 통과함", in)
		}
		if !strings.Contains(msg, "--force") {
			t.Errorf("입력 %q 의 거절 메시지가 --force 를 안내하지 않음: %q", in, msg)
		}
	}
}

// --- allocation switch ---

// TestParseAllocationSwitchArgs_전체인자 는 --vendor·--to·--force 가 모두 올바르게
// 채워지는지 본다.
func TestParseAllocationSwitchArgs_전체인자(t *testing.T) {
	got, err := parseAllocationSwitchArgs([]string{"--vendor", "nvidia", "--to", "dra", "--force"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := allocationSwitchArgs{vendor: "nvidia", to: "dra", force: true}
	if got != want {
		t.Errorf("파싱 결과 불일치: got %+v, want %+v", got, want)
	}
}

// TestParseAllocationSwitchArgs_Force생략기본값false 는 --force 를 안 쓰면 false 로
// 남는지 본다(확인 게이트가 기본으로 걸려야 한다).
func TestParseAllocationSwitchArgs_Force생략기본값false(t *testing.T) {
	got, err := parseAllocationSwitchArgs([]string{"--vendor", "nvidia", "--to", "dra"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.force {
		t.Errorf("--force 를 안 썼는데 force=true 로 파싱됨")
	}
}

// TestParseAllocationSwitchArgs_필수누락 은 --vendor·--to 중 하나라도 없으면 에러인지 본다.
func TestParseAllocationSwitchArgs_필수누락(t *testing.T) {
	if _, err := parseAllocationSwitchArgs([]string{"--vendor", "nvidia"}); err == nil {
		t.Errorf("--to 없이는 에러여야 함")
	}
	if _, err := parseAllocationSwitchArgs([]string{"--to", "dra"}); err == nil {
		t.Errorf("--vendor 없이는 에러여야 함")
	}
}

// --- partition apply/get ---

// TestPrintPartitionText_헤더와열순서 는 partition get 표의 머리 문자열과 열 순서,
// 데이터 행의 값 순서를 고정한다.
func TestPrintPartitionText_헤더와열순서(t *testing.T) {
	rows := []npuctl.PartitionRow{
		{Node: "k8s-worker1", Vendor: "nvidia", Profile: "1g.6gb", CountPerDevice: 2, Phase: "Ready"},
	}
	out := captureStdout(t, func() { printPartitionText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NODE", "VENDOR", "PROFILE", "COUNT_PER_DEVICE", "PHASE"})
	assertColumnsInOrder(t, lines[1], []string{"k8s-worker1", "nvidia", "1g.6gb", "2", "Ready"})
}

// TestParsePartitionApplyArgs_전체인자 는 다섯 플래그가 모두 올바르게 채워지는지 본다.
func TestParsePartitionApplyArgs_전체인자(t *testing.T) {
	got, err := parsePartitionApplyArgs([]string{
		"--node", "n1", "--vendor", "nvidia", "--profile", "1g.6gb", "--count-per-device", "2", "--force",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := partitionApplyArgs{node: "n1", vendor: "nvidia", profile: "1g.6gb", countPerDevice: 2, force: true}
	if got != want {
		t.Errorf("파싱 결과 불일치: got %+v, want %+v", got, want)
	}
}

// TestParsePartitionApplyArgs_Force생략기본값false 는 --force 를 안 쓰면 false 로 남는지 본다.
func TestParsePartitionApplyArgs_Force생략기본값false(t *testing.T) {
	got, err := parsePartitionApplyArgs([]string{
		"--node", "n1", "--vendor", "nvidia", "--profile", "1g.6gb", "--count-per-device", "1",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.force {
		t.Errorf("--force 를 안 썼는데 force=true 로 파싱됨")
	}
}

// TestParsePartitionApplyArgs_필수누락 은 --node·--vendor·--profile 중 하나라도 없으면
// 에러인지 본다 — vendor 를 생략해도 자동으로 추측하지 않는다(대상 판정을 CLI 가 대신
// 하지 않는다는 원칙 그대로).
func TestParsePartitionApplyArgs_필수누락(t *testing.T) {
	cases := [][]string{
		{"--vendor", "nvidia", "--profile", "1g.6gb", "--count-per-device", "1"},
		{"--node", "n1", "--profile", "1g.6gb", "--count-per-device", "1"},
		{"--node", "n1", "--vendor", "nvidia", "--count-per-device", "1"},
	}
	for _, args := range cases {
		if _, err := parsePartitionApplyArgs(args); err == nil {
			t.Errorf("%v 는 필수 인자가 빠졌는데 에러가 없다", args)
		}
	}
}

// TestScanFlags_값누락뒤플래그는거절 은 값이 필요한 플래그 바로 뒤에 다른 플래그
// 이름이 오는 경우(사용자가 값 하나를 빠뜨리고 습관적으로 --force 를 마지막에 붙인
// 경우)를 네 명령 전부에서 고정한다. 그 토큰을 값으로 삼키면 뒤로 밀린 진짜 --force
// 가 정상 위치에서 인식돼 force=true·에러 없음이 된다 — 그래서 지금은 그 자리에서
// 즉시 에러를 내고, force 는 건드려지기 전에 반환되므로 항상 false 다.
func TestScanFlags_값누락뒤플래그는거절(t *testing.T) {
	t.Run("partition_apply", func(t *testing.T) {
		got, err := parsePartitionApplyArgs([]string{
			"--node", "n1", "--vendor", "nvidia", "--profile", "--count-per-device", "2", "--force",
		})
		if err == nil {
			t.Fatalf("에러가 없다: %+v", got)
		}
		if got.force {
			t.Errorf("에러인데 force=true: %+v", got)
		}
	})
	t.Run("sharing_apply", func(t *testing.T) {
		got, err := parseSharingApplyArgs([]string{
			"--node", "n1", "--vendor", "nvidia", "--mode", "--replicas", "2", "--force",
		})
		if err == nil {
			t.Fatalf("에러가 없다: %+v", got)
		}
		if got.force {
			t.Errorf("에러인데 force=true: %+v", got)
		}
	})
	t.Run("allocation_switch", func(t *testing.T) {
		got, err := parseAllocationSwitchArgs([]string{"--to", "dra", "--vendor", "--force", "--force"})
		if err == nil {
			t.Fatalf("에러가 없다: %+v", got)
		}
		if got.force {
			t.Errorf("에러인데 force=true: %+v", got)
		}
	})
	t.Run("driver_rollback", func(t *testing.T) {
		got, err := parseDriverRollbackArgs([]string{"--node", "n1", "--vendor", "--model", "--force"})
		if err == nil {
			t.Fatalf("에러가 없다: %+v", got)
		}
		if got.force {
			t.Errorf("에러인데 force=true: %+v", got)
		}
	})
}

// TestScanFlags_마지막토큰이값필요플래그면거절 은 값이 필요한 플래그가 인자 목록의
// 맨 끝에 와 뒤에 아무 토큰도 없는 경우를 본다.
func TestScanFlags_마지막토큰이값필요플래그면거절(t *testing.T) {
	got, err := parsePartitionApplyArgs([]string{"--node", "n1", "--vendor", "nvidia", "--profile"})
	if err == nil {
		t.Fatalf("에러가 없다: %+v", got)
	}
	if got.force {
		t.Errorf("에러인데 force=true: %+v", got)
	}
}

// TestParsePartitionApplyArgs_Force는값으로삼켜져도false 는 --force 자신이 어떤 플래그의
// "값"으로 잘못 지정되는(사용자가 실수로 그 자리에 쓴) 경우도 여전히 거절되는지 본다 —
// 값이 "--"로 시작하는 토큰이면 무엇이든(설령 "--force"라는 문자열이라도) 값으로
// 받아들이지 않는다.
func TestParsePartitionApplyArgs_Force는값으로삼켜져도false(t *testing.T) {
	got, err := parsePartitionApplyArgs([]string{
		"--node", "n1", "--vendor", "nvidia", "--profile", "--force", "--count-per-device", "1",
	})
	if err == nil {
		t.Fatalf("에러가 없다: %+v", got)
	}
	if got.force {
		t.Errorf("에러인데 force=true: %+v", got)
	}
	// 반복된 --force 는(값 자리가 아니라 플래그 자리에 오면) 여전히 무해하게 true 로만
	// 남아야 한다(스위치라 반복은 무해) — 정상 경로가 이번 수정으로 깨지지 않았는지 확인.
	got2, err := parsePartitionApplyArgs([]string{
		"--node", "n1", "--vendor", "nvidia", "--profile", "p", "--count-per-device", "1", "--force", "--force",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !got2.force {
		t.Errorf("반복된 --force 인데 false 로 파싱됨: %+v", got2)
	}
}

// --- sharing apply/get ---

// TestPrintSharingText_헤더와열순서 는 sharing get 표의 머리 문자열과 열 순서를 고정한다.
func TestPrintSharingText_헤더와열순서(t *testing.T) {
	rows := []npuctl.SharingRow{{Node: "k8s-worker1", Vendor: "nvidia", Mode: "timeSliced", Replicas: 4, Phase: "Ready"}}
	out := captureStdout(t, func() { printSharingText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"NODE", "VENDOR", "MODE", "REPLICAS", "PHASE"})
	assertColumnsInOrder(t, lines[1], []string{"k8s-worker1", "nvidia", "timeSliced", "4", "Ready"})
}

// TestParseSharingApplyArgs_전체인자 는 다섯 플래그가 모두 올바르게 채워지는지 본다.
func TestParseSharingApplyArgs_전체인자(t *testing.T) {
	got, err := parseSharingApplyArgs([]string{
		"--node", "n1", "--vendor", "nvidia", "--mode", "mps", "--replicas", "3", "--force",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := sharingApplyArgs{node: "n1", vendor: "nvidia", mode: "mps", replicas: 3, force: true}
	if got != want {
		t.Errorf("파싱 결과 불일치: got %+v, want %+v", got, want)
	}
}

// TestParseSharingApplyArgs_Force생략기본값false 는 --force 를 안 쓰면 false 로 남는지 본다.
func TestParseSharingApplyArgs_Force생략기본값false(t *testing.T) {
	got, err := parseSharingApplyArgs([]string{"--node", "n1", "--vendor", "nvidia", "--mode", "exclusive"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.force {
		t.Errorf("--force 를 안 썼는데 force=true 로 파싱됨")
	}
}

// TestParseSharingApplyArgs_필수누락 은 --node·--vendor·--mode 중 하나라도 없으면
// 에러인지 본다.
func TestParseSharingApplyArgs_필수누락(t *testing.T) {
	if _, err := parseSharingApplyArgs([]string{"--vendor", "nvidia", "--mode", "mps"}); err == nil {
		t.Errorf("--node 없이는 에러여야 함")
	}
}

// TestPrintApplyResult_상태별출력 은 partition apply·sharing apply 가 공유하는 결과
// 출력이 생성·patch·무변경 세 경우를 서로 다른 마지막 줄로 구분하는지, 생성·patch
// 두 경우는 실제 적용 결과를 컨트롤러가 판정한다는 고지를 담는지 본다 — admission
// webhook 이 이 CRD 에서 failurePolicy=Ignore 라 거절은 컨트롤러에서만 올 수 있으므로,
// 이 고지가 없으면 출력이 이미 완료된 것처럼 읽힌다.
func TestPrintApplyResult_상태별출력(t *testing.T) {
	cases := []struct {
		name              string
		created, noChange bool
		want              []string
	}{
		{"생성", true, false, []string{"생성됨", "컨트롤러가", "get"}},
		{"patch", false, false, []string{"patch됨", "컨트롤러가", "get"}},
		{"무변경", false, true, []string{"바뀐 것 없음"}},
	}
	for _, tc := range cases {
		out := captureStdout(t, func() { printApplyResult("p1", "n1", "nvidia", tc.created, tc.noChange) })
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: 출력에 %q 가 없다: %q", tc.name, want, out)
			}
		}
		if tc.noChange && strings.Contains(out, "컨트롤러가") {
			t.Errorf("%s: 무변경인데 컨트롤러 고지가 나옴(patch 자체가 없었다): %q", tc.name, out)
		}
	}
}

// --- diagnose ---

// TestBuildDiagnoseRows_문제없으면빈행 은 전부 정상이면 행이 없는지 본다 — diagnose 는
// 새 판정을 더하지 않으므로 "정상" 판정 자체를 지어내 행을 채우지 않는다.
func TestBuildDiagnoseRows_문제없으면빈행(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "n1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Idle"}}},
	}}
	details := []*npuctl.NodeDetail{{Name: "n1", Health: &npuctl.HealthView{State: "Healthy"}}}
	alloc := []npuctl.AllocationRow{{Vendor: "nvidia", Enabled: true, Declared: "devicePlugin"}}
	caps := []npuctl.VendorCapabilityRow{{Vendor: "nvidia", Capability: "partition", Verified: "verified"}}
	rows := buildDiagnoseRows(status, details, alloc, caps)
	if len(rows) != 0 {
		t.Fatalf("전부 정상인데 행이 나옴: %+v", rows)
	}
}

// TestBuildDiagnoseRows_네범주각각걸림 은 health(비정상 상태)·upgrade(Failed)·
// allocation(모호 노드)·capability(degraded) 네 범주가 각각 한 행씩 걸리는지, 정상인
// 형제 항목(다른 벤더·다른 노드)은 안 걸리는지 본다.
func TestBuildDiagnoseRows_네범주각각걸림(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "n1", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Failed", DesiredVersion: "580.126.10"}}},
		{Name: "n2", Upgrades: []npuctl.UpgradeStatus{{Vendor: "nvidia", State: "Idle"}}},
	}}
	details := []*npuctl.NodeDetail{
		{Name: "n1", Health: &npuctl.HealthView{State: "Degraded", Reason: "overheat"}},
		{Name: "n2", Health: &npuctl.HealthView{State: "Healthy"}},
	}
	alloc := []npuctl.AllocationRow{{Vendor: "rngd", AmbiguousProductNodes: []string{"hybrid-node1"}}}
	caps := []npuctl.VendorCapabilityRow{
		{Vendor: "nvidia", Capability: "timeSlicing", Verified: npuctl.VerificationDegraded},
		{Vendor: "nvidia", Capability: "partition", Verified: "verified"},
	}
	rows := buildDiagnoseRows(status, details, alloc, caps)

	byCategory := map[string]int{}
	for _, r := range rows {
		byCategory[r.category]++
	}
	for _, cat := range []string{"health", "upgrade", "allocation", "capability"} {
		if byCategory[cat] != 1 {
			t.Errorf("범주 %s 는 정확히 1행이어야 하는데 %d행: %+v", cat, byCategory[cat], rows)
		}
	}
	for _, r := range rows {
		if r.node == "n2" {
			t.Errorf("정상 노드 n2 가 행에 섞임: %+v", r)
		}
	}
}

func TestDipVersionCell_HostModeMarked(t *testing.T) {
	host := npuctl.DIPStatus{DesiredVersion: "580.126.09", VersionSource: "Host"}
	if got := dipVersionCell(host); got != "580.126.09 (versionSource=Host)" {
		t.Errorf("Host 모드 = %q", got)
	}
	policy := npuctl.DIPStatus{DesiredVersion: "580.126.09", VersionSource: "Policy"}
	if got := dipVersionCell(policy); got != "580.126.09" {
		t.Errorf("Policy 모드 = %q", got)
	}
	dip := npuctl.DIPStatus{DesiredVersion: "580.126.09", VersionSource: "host"}
	if got := dipVersionCell(dip); got != "580.126.09 (versionSource=Host)" {
		t.Errorf("소문자 host = %q", got)
	}
	if got := dipVersionCell(npuctl.DIPStatus{VersionSource: "Host"}); got != "" {
		t.Errorf("버전 없는 Host 정책 = %q, want 빈 값", got)
	}
}

func TestBuildDiagnoseRows_종점과롤백보류(t *testing.T) {
	up := func(u npuctl.UpgradeStatus) []npuctl.UpgradeStatus {
		u.Vendor = "nvidia"
		return []npuctl.UpgradeStatus{u}
	}
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{
		{Name: "n1", Upgrades: up(npuctl.UpgradeStatus{
			State: "HostAhead", CurrentVersion: "580.178.04", DesiredVersion: "580.126.09"})},
		{Name: "n2", Upgrades: up(npuctl.UpgradeStatus{State: "UnverifiedVersion", DesiredVersion: "590.1.1"})},
		{Name: "n3", Upgrades: up(npuctl.UpgradeStatus{
			State: "Idle", CurrentVersion: "580.178.04", DesiredVersion: "580.126.09", RolledBackVersion: "580.126.09"})},
		{Name: "n4", Upgrades: up(npuctl.UpgradeStatus{
			State: "Idle", CurrentVersion: "580.178.04", DesiredVersion: "580.178.04"})},
		// 롤백이 진행 중인 노드는 보류가 아니다 — RolledBackVersion 이 Rollback 중에도 채워져 있다.
		{Name: "n5", Upgrades: up(npuctl.UpgradeStatus{
			State: "Rollback", CurrentVersion: "580.178.04", DesiredVersion: "580.126.09", RolledBackVersion: "580.126.09"})},
	}}
	rows := buildDiagnoseRows(status, nil, nil, nil)
	got := map[string]string{}
	for _, r := range rows {
		got[r.node] = r.detail
	}
	if len(rows) != 3 || got["n4"] != "" || got["n5"] != "" {
		t.Fatalf("행 %d개 %+v — n1·n2·n3 셋이어야 한다(n4 정상, n5 롤백 진행 중은 행 없음)", len(rows), rows)
	}
	if !strings.Contains(got["n1"], "state=HostAhead") || !strings.Contains(got["n1"], "current=580.178.04") {
		t.Errorf("n1 detail=%q", got["n1"])
	}
	if !strings.Contains(got["n3"], "rolledBack=580.126.09") || !strings.Contains(got["n3"], "정책 spec 변경 또는 DUS 삭제 전까지") {
		t.Errorf("n3 detail=%q — 롤백 보류가 보이지 않는다", got["n3"])
	}
}

// TestPrintDiagnoseText_헤더와열순서 는 diagnose 표의 머리 문자열과 열 순서를 고정한다.
func TestPrintDiagnoseText_헤더와열순서(t *testing.T) {
	rows := []diagnoseRow{{category: "health", node: "n1", vendor: "nvidia", detail: "state=Degraded reason=overheat"}}
	out := captureStdout(t, func() { printDiagnoseText(rows) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("헤더 + 데이터 1행이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"CATEGORY", "NODE", "VENDOR", "DETAIL"})
	assertColumnsInOrder(t, lines[1], []string{"health", "n1", "nvidia", "state=Degraded reason=overheat"})
}

// TestPrintDiagnoseText_빈행이면헤더만 은 문제가 없을 때 헤더만 찍히는지 본다.
func TestPrintDiagnoseText_빈행이면헤더만(t *testing.T) {
	out := captureStdout(t, func() { printDiagnoseText(nil) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("헤더 한 줄만이어야 하는데 %d 줄: %q", len(lines), lines)
	}
	assertHeaderExact(t, lines[0], []string{"CATEGORY", "NODE", "VENDOR", "DETAIL"})
}

func TestParseUpgradeArgs(t *testing.T) {
	p, err := parseUpgradeArgs([]string{"nvidia", "--version", "580.126.09", "--model", "generic", "--force", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if p.vendor != "nvidia" || p.version != "580.126.09" || p.model != "generic" || !p.force || !p.yes || p.auto {
		t.Fatalf("parsed = %+v", p)
	}
	if _, err := parseUpgradeArgs([]string{"nvidia"}); err == nil {
		t.Fatal("--version 없이 통과했다")
	}
}

func TestParseToggleArgs(t *testing.T) {
	p, err := parseToggleArgs([]string{"rebellions", "--enabled", "false", "-y"})
	if err != nil {
		t.Fatal(err)
	}
	if p.vendor != "rebellions" || p.enabled || !p.yes {
		t.Fatalf("parsed = %+v", p)
	}
	if _, err := parseToggleArgs([]string{"rebellions", "--enabled", "maybe"}); err == nil {
		t.Fatal("--enabled 값 검증이 없다")
	}
}

func TestConfirmAction_생략플래그이름을안내(t *testing.T) {
	ok, msg := confirmAction(strings.NewReader("n\n"), false, "--yes")
	if ok || !strings.Contains(msg, "--yes") {
		t.Fatalf("got ok=%v msg=%q", ok, msg)
	}
	if ok, _ := confirmAction(errReader{}, true, "--yes"); !ok {
		t.Fatal("skip=true 면 읽지 않고 통과해야 한다")
	}
}

// TestConfirmAction_비대화식입력없음이면거절 은 표준입력이 파이프·리다이렉트로 이어졌지만
// 아무것도 보내지 않은 경우(EOF)를 다룬다 — 읽을 줄이 없다는 것과 "n" 을 구분하지 않고
// 똑같이 거절한다: 입력이 없으면 안전한 쪽(거절)으로 fail 하는 계약.
func TestConfirmAction_비대화식입력없음이면거절(t *testing.T) {
	ok, msg := confirmAction(strings.NewReader(""), false, "--yes")
	if ok {
		t.Fatal("EOF(입력 없음)인데 통과했다")
	}
	if !strings.Contains(msg, "--yes") {
		t.Fatalf("거절 메시지가 --yes 를 안내하지 않음: %q", msg)
	}
}

// TestPrintDriverStatusText_관측목록표기 는 driver status 표의 MODEL 칸이 대표값 generic
// 뒤에 관측 목록을 보이는지 본다. status 표(printStatusText)는 modelCell 로 이미 보이므로,
// 한쪽만 감추면 관리자가 두 명령의 결과를 다르게 읽는다.
func TestPrintDriverStatusText_관측목록표기(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name: "k8s-worker3",
		Upgrades: []npuctl.UpgradeStatus{{
			Vendor: "furiosa", Model: "generic", Models: []string{"rngd", "warboy"},
			State: "Idle",
		}},
	}}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	if !strings.Contains(out, "generic(rngd,warboy)") {
		t.Errorf("MODEL 칸에 관측 목록이 안 보인다: %q", out)
	}
}

// CONSUMERS 절은 세 상태를 구분한다 — 모름(—), 없음, 목록. 지어내지 않는다.
func TestRenderNodeDetail_소비자절(t *testing.T) {
	unknown := renderNodeDetail(&npuctl.NodeDetail{Name: "n1"})
	if !strings.Contains(unknown, "CONSUMERS:  —") {
		t.Errorf("모름은 대시여야 한다:\n%s", unknown)
	}
	failed := renderNodeDetail(&npuctl.NodeDetail{Name: "n1", ConsumersError: "podresources 결과 파일 없음"})
	if !strings.Contains(failed, "CONSUMERS:  — (podresources 결과 파일 없음)") {
		t.Errorf("실패 사유가 붙어야 한다:\n%s", failed)
	}
	none := renderNodeDetail(&npuctl.NodeDetail{Name: "n1", ConsumersObservedAt: "2026-10-06T01:02:03Z"})
	if !strings.Contains(none, "CONSUMERS:  없음") {
		t.Errorf("관측했는데 비면 없음이어야 한다:\n%s", none)
	}
	some := renderNodeDetail(&npuctl.NodeDetail{Name: "rngd-1", ConsumersObservedAt: "2026-10-06T01:02:03Z",
		Consumers: []npuctl.ConsumerView{{
			Namespace: "default", Pod: "rngd-pod", Container: "rngd-pod",
			ResourceName: "furiosa.ai/rngd", DeviceID: "89413086",
		}}})
	for _, want := range []string{"CONSUMERS:", "default/rngd-pod", "furiosa.ai/rngd", "89413086", "NAMESPACE/POD"} {
		if !strings.Contains(some, want) {
			t.Errorf("출력에 %q 가 없다:\n%s", want, some)
		}
	}
}

func TestVendorVersions_HostModeMarked(t *testing.T) {
	v := npuctl.VendorStatus{DriverInstallPolicies: []npuctl.DIPStatus{
		{DesiredVersion: "580.126.09", VersionSource: "Host"},
		{DesiredVersion: "1.9.9-3", VersionSource: "Policy"},
	}}
	if got := vendorVersions(v); got != "580.126.09 (versionSource=Host),1.9.9-3" {
		t.Errorf("vendorVersions = %q", got)
	}
}

func TestPrintStatusText_HostModeVersionCell(t *testing.T) {
	status := &npuctl.ClusterStatus{Vendors: []npuctl.VendorStatus{{
		Name: "nvidia",
		DriverInstallPolicies: []npuctl.DIPStatus{{
			Name: "nvidia", Vendor: "nvidia", DesiredVersion: "580.126.09", VersionSource: "Host",
		}},
	}}}
	out := captureStdout(t, func() { printStatusText(status) })
	if !strings.Contains(out, "580.126.09 (versionSource=Host)") {
		t.Errorf("DriverInstallPolicies VERSION 칸에 Host 표시가 없다: %q", out)
	}
}

func TestUpgradeStateCell_RollbackHold(t *testing.T) {
	cases := []struct {
		u    npuctl.UpgradeStatus
		want string
	}{
		{npuctl.UpgradeStatus{State: "Idle", RolledBackVersion: "580.999.99"}, "Idle(rolledBack=580.999.99)"},
		{npuctl.UpgradeStatus{State: "Rollback", RolledBackVersion: "580.999.99"}, "Rollback"},
		{npuctl.UpgradeStatus{State: "Idle"}, "Idle"},
		{npuctl.UpgradeStatus{}, ""},
	}
	for _, c := range cases {
		if got := upgradeStateCell(c.u); got != c.want {
			t.Errorf("upgradeStateCell(%+v) = %q, want %q", c.u, got, c.want)
		}
	}
}

func TestPrintDriverStatusText_롤백보류표기(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{
		Name: "k8s-worker1",
		Upgrades: []npuctl.UpgradeStatus{{
			Vendor: "nvidia", Model: "a30", State: "Idle",
			CurrentVersion: "580.178.04", DesiredVersion: "580.999.99", RolledBackVersion: "580.999.99",
		}},
	}}}
	out := captureStdout(t, func() { printDriverStatusText(status) })
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{"k8s-worker1", "nvidia", "a30", "Idle(rolledBack=580.999.99)",
		"580.178.04", "580.999.99"}
	assertColumnsInOrder(t, lines[1], want)

	out = captureStdout(t, func() { printStatusText(status) })
	if !strings.Contains(out, "Idle(rolledBack=580.999.99)") {
		t.Errorf("status 의 DriverUpgradeState 표에 롤백 보류가 없다: %q", out)
	}
}

func TestBuildDiagnoseRows_Failed에롤백기록(t *testing.T) {
	status := &npuctl.ClusterStatus{Nodes: []npuctl.NodeStatus{{Name: "n1", Upgrades: []npuctl.UpgradeStatus{{
		Vendor: "nvidia", State: "Failed", CurrentVersion: "580.178.04",
		DesiredVersion: "580.999.99", RolledBackVersion: "580.999.99",
	}}}}}
	rows := buildDiagnoseRows(status, nil, nil, nil)
	if len(rows) != 1 || !strings.Contains(rows[0].detail, "state=Failed") || strings.Contains(rows[0].detail, "재시도 보류") {
		t.Errorf("Failed 는 Failed 행 하나여야 한다(보류 행 아님): %+v", rows)
	}
}

func TestDipVersionCell_TrackOnly(t *testing.T) {
	want := dashIfEmpty("") + " (trackOnly)"
	if got := dipVersionCell(npuctl.DIPStatus{TrackOnly: true, VersionSource: "Host"}); got != want {
		t.Errorf("빈 목표 버전의 TrackOnly 칸 = %q, want %q", got, want)
	}
	if got := dipVersionCell(npuctl.DIPStatus{TrackOnly: true, DesiredVersion: "3.0.0"}); got != "3.0.0 (trackOnly)" {
		t.Errorf("TrackOnly 칸 = %q, want %q", got, "3.0.0 (trackOnly)")
	}
	if got := dipVersionCell(npuctl.DIPStatus{DesiredVersion: "2.11.0", VersionSource: "Policy"}); got != "2.11.0" {
		t.Errorf("설치 정책 칸이 바뀌었다: %q", got)
	}
}

// device-plugin 조건은 operator 줄 뒤에 종류 열(device-plugin)과 메시지를 달고 나온다 — operator 의
// NotDetected(외부 operator 미감지)와 kcloud 가 배포한 플러그인의 준비 상태를 한 절에서 구분해 읽게 한다.
func TestPrintStatusText_ClusterPolicyShowsDevicePluginLinesWithKindAndMessage(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{{
		Namespace: "kcloud", Name: "default", Ready: "True", ReadyReason: "AllResourcesReady",
		VendorOperators: []npuctl.VendorOperatorStatus{
			{Vendor: "rebellions", Status: "Unknown", Reason: "NotDetected"},
		},
		DevicePlugins: []npuctl.VendorConditionStatus{
			{Vendor: "rebellions", Status: "True", Reason: "Prepared", Message: "판: v0.3 형식(태그 v0.3.6)"},
		},
	}}}
	out := captureStdout(t, func() { printStatusText(status) })

	section := out[strings.Index(out, "=== NPUClusterPolicy ==="):]
	section = section[:strings.Index(section, "\n\n=== DriverUpgradeState ===")]
	lines := strings.Split(strings.TrimRight(section, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("헤더+Ready+operator 1줄+device-plugin 1줄=4줄이어야 하는데 %d줄: %q", len(lines), lines)
	}
	if !strings.Contains(lines[2], "operator") || !strings.Contains(lines[2], "NotDetected") {
		t.Errorf("operator 줄에 종류 표시가 없다: %q", lines[2])
	}
	if !strings.Contains(lines[3], "device-plugin") || !strings.Contains(lines[3], "True") ||
		!strings.Contains(lines[3], "Prepared") || !strings.Contains(lines[3], "판: v0.3 형식(태그 v0.3.6)") {
		t.Errorf("device-plugin 줄이 다르다: %q", lines[3])
	}
	if runeColStart(t, lines[2], "Unknown") != runeColStart(t, lines[3], "True") {
		t.Errorf("STATUS 열이 두 종류 사이에서 어긋난다: %q vs %q", lines[2], lines[3])
	}
}

// diagnose 는 준비되지 않은 device plugin 을 문제 행으로 낸다. True 인 벤더는 내지 않는다.
func TestBuildDiagnoseRows_DevicePluginNotReady(t *testing.T) {
	status := &npuctl.ClusterStatus{ClusterPolicies: []npuctl.ClusterPolicyStatus{{
		Namespace: "kcloud", Name: "default", Ready: "True",
		DevicePlugins: []npuctl.VendorConditionStatus{
			{Vendor: "rebellions", Status: "False", Reason: "RblnSmiMissing", Message: "RblnSmiMissing: worker4"},
			{Vendor: "furiosa", Status: "True", Reason: "Prepared"},
		},
	}}}
	rows := buildDiagnoseRows(status, nil, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("문제 행 %d개 (1 기대): %+v", len(rows), rows)
	}
	if rows[0].category != "device-plugin" || rows[0].vendor != "rebellions" ||
		!strings.Contains(rows[0].detail, "RblnSmiMissing") || !strings.Contains(rows[0].detail, "kcloud/default") {
		t.Errorf("device-plugin 행이 다르다: %+v", rows[0])
	}
}
