// main.go: kubectl-npu plugin — NPU Operator CLI
// 상세: kubectl npu status[--json]/driver-version/upgrade/toggle/describe 등을 제공한다.
// 조회·제어는 pkg/npuctl(kcloud-operator 모듈, replace 로 로컬 참조)에 위임하고, 이
// 파일은 CLI 인자 파싱과 사람용/JSON 출력 서식만 맡는다.
// 생성일: 2026-03-25 | 수정일: 2026-10-28
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	npuv1alpha1 "kcloud-operator/api/v1alpha1"
	"kcloud-operator/pkg/npuctl"
)

// printTable은 열 폭을 데이터에서 재어 표를 낸다. 고정폭 서식(%-15s)은 값 하나만
// 넘쳐도 그 행부터 열이 밀려 표를 훑을 수 없다 — 노드 <rngd-node>.cluster.local(20자)
// 처럼 클러스터마다 이름 길이가 다르므로 폭은 잴 수밖에 없다. 폭은 byte 가 아니라
// rune 개수로 잰다 — len()(byte 길이)로 재면 한글처럼 rune 당 3byte 인 문자열에서
// 필요한 것보다 크게 재져 열이 행마다 다르게 밀린다. 한글은 터미널에서 실제로 2칸을
// 차지하므로 rune 단위로도 완전한 정렬은 아니다 — 동아시아 폭 문자는 별도 과제다.
func printTable(header []string, rows [][]string) {
	width := make([]int, len(header))
	for i, h := range header {
		width[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(width) {
				width[i] = maxInt(width[i], utf8.RuneCountInString(c))
			}
		}
	}
	line := func(cells []string) {
		var b strings.Builder
		for i, c := range cells {
			if i == len(cells)-1 {
				b.WriteString(c)
				break
			}
			fmt.Fprintf(&b, "%-*s  ", width[i], c)
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	line(header)
	for _, r := range rows {
		line(r)
	}
}

// renderTable 은 printTable 과 같은 정렬 원칙(폭을 헤더·데이터 중 더 긴 쪽에서 잰다,
// 고정폭 하드코딩 금지)을 쓰되 표준출력에 바로 찍지 않고 문자열로 돌려준다.
// describe 명령의 각 절은 문자열을 조립해 한 번에 반환해야 해서 printTable(표준출력
// 직접 기록)을 그대로 못 쓴다 — printTable 은 다른 네 명령이 공유하므로 건드리지 않는다.
// 매 줄 앞에 2칸을 들여써서 절 라벨(DEVICES: 등) 아래 붙는 하위 표임을 나타낸다.
func renderTable(header []string, rows [][]string) string {
	width := make([]int, len(header))
	for i, h := range header {
		width[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(width) {
				width[i] = maxInt(width[i], utf8.RuneCountInString(c))
			}
		}
	}
	var b strings.Builder
	line := func(cells []string) {
		var lb strings.Builder
		lb.WriteString("  ")
		for i, c := range cells {
			if i == len(cells)-1 {
				lb.WriteString(c)
				break
			}
			fmt.Fprintf(&lb, "%-*s  ", width[i], c)
		}
		b.WriteString(strings.TrimRight(lb.String(), " "))
		b.WriteString("\n")
	}
	line(header)
	for _, r := range rows {
		line(r)
	}
	return b.String()
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "status":
		cmdStatus(os.Args[2:])
	case "driver-version":
		cmdDriverVersion()
	case "upgrade":
		cmdUpgrade(os.Args[2:])
	case "toggle":
		cmdToggle(os.Args[2:])
	case "describe":
		cmdDescribe(os.Args[2:])
	case "policy":
		cmdPolicy(os.Args[2:])
	case "capabilities":
		cmdCapabilities(os.Args[2:])
	case "devices":
		cmdDevices()
	case "vendors":
		cmdVendors()
	case "health":
		cmdHealth()
	case "driver":
		cmdDriver(os.Args[2:])
	case "allocation":
		cmdAllocation(os.Args[2:])
	case "partition":
		cmdPartition(os.Args[2:])
	case "sharing":
		cmdSharing(os.Args[2:])
	case "diagnose":
		cmdDiagnose()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`kubectl-npu: NPU Operator CLI Plugin

Usage:
  kubectl npu status                              노드×벤더 집약 상태(텍스트) 출력
  kubectl npu status --json                       위와 동일한 내용을 JSON으로 출력(관리툴 소비용)
  kubectl npu driver-version                       노드별 설치된 드라이버 버전 표
  kubectl npu upgrade <vendor> --version VER [--model M] [--force] [--auto] [--yes]
                                                    DriverInstallPolicy.spec.driver.version patch
  kubectl npu toggle <vendor> --enabled true|false [--yes]
                                                    NPUClusterPolicy.spec.<vendor>.enabled patch
  kubectl npu policy create|delete                 AcceleratorPartitionPolicy 정책 생성·삭제
  kubectl npu describe node [<노드>]                노드 하나(또는 전체)의 장치·광고량·소비자·health·evidence·정책
  kubectl npu describe node [<노드>] --json         위와 동일한 내용을 JSON 으로
  kubectl npu capabilities [--vendor furiosa|nvidia|rebellions|tenstorrent] [--json]
                                                    벤더×능력 선언·검증 상태(furiosa 는 warboy·rngd 두 product 를 포함)
  kubectl npu devices                              클러스터 전체 장치 인벤토리(노드×PCI 단위)
  kubectl npu vendors                              벤더별 활성화 여부·목표 드라이버 버전
  kubectl npu health                               노드×장치 AcceleratorHealth 상태
  kubectl npu driver status                        노드×벤더 DriverUpgradeState 단계(State/Current/Desired)
  kubectl npu driver rollback --node <노드> [--vendor v] [--model m] [--force]
                                                    DriverUpgradeState.metadata 에 롤백 요청 어노테이션 patch(Idle·Failed 만)
  kubectl npu allocation status                    벤더별 allocation API(devicePlugin|dra) 노출 상태
  kubectl npu allocation switch --vendor v --to devicePlugin|dra [--force]
                                                    NPUClusterPolicy.spec.<vendor>.advertiseBy patch(선언만, 전환 판정은 컨트롤러)
  kubectl npu partition get                        노드×벤더 파티션 레이아웃(AcceleratorPartitionPolicy.spec.layout) 현황
  kubectl npu partition apply --node n --vendor v --profile p --count-per-device n [--force]
                                                    파티션 정책 적용(있으면 layout만 patch, 없으면 생성)
  kubectl npu sharing get                          노드×벤더 공유 방식(AcceleratorPartitionPolicy.spec.sharing) 현황
  kubectl npu sharing apply --node n --vendor v --mode timeSliced|mps|exclusive [--replicas n] [--force]
                                                    공유 정책 적용(있으면 sharing만 patch, 없으면 생성)
  kubectl npu diagnose                             health·driver·allocation·capabilities·device-plugin 중 문제 있는 항목만 모아 냄

Vendors: nvidia, furiosa, rngd(furiosa RNGD 서브벤더), rebellions, tenstorrent

Examples:
  kubectl npu status
  kubectl npu status --json
  kubectl npu driver-version
  kubectl npu upgrade nvidia --version 580.126.10 --auto
  kubectl npu upgrade furiosa --model rngd --version 2026.2.0 --force
  kubectl npu toggle rngd --enabled true
  kubectl npu policy create --name p1 --vendor nvidia --node n1 --intent partitioned --profile 1g.5gb
  kubectl npu policy delete p1
  kubectl npu describe node k8s-worker1
  kubectl npu describe node                        (전체 노드)
  kubectl npu devices
  kubectl npu vendors
  kubectl npu health
  kubectl npu driver status
  kubectl npu driver rollback --node k8s-worker1 --vendor nvidia --force
  kubectl npu allocation status
  kubectl npu allocation switch --vendor nvidia --to dra --force
  kubectl npu partition get
  kubectl npu partition apply --node k8s-worker1 --vendor nvidia --profile 1g.6gb --count-per-device 2 --force
  kubectl npu sharing get
  kubectl npu sharing apply --node k8s-worker1 --vendor nvidia --mode timeSliced --replicas 4 --force
  kubectl npu diagnose`)
}

func newClientOrExit() *npuctl.Client {
	c, err := npuctl.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return c
}

func cmdStatus(args []string) {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	c := newClientOrExit()
	status, err := c.CollectStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting status: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printStatusText(status)
}

// printStatusText는 기존 kubectl-npu status의 출력 형식(DriverInstallPolicies/NodeDeviceReports/
// NPUClusterPolicy 3단 텍스트 표)을 npuctl.ClusterStatus로부터 재구성하여 하위호환을 유지한다.
func printStatusText(status *npuctl.ClusterStatus) {
	fmt.Println("=== DriverInstallPolicies ===")
	var dipRows [][]string
	for _, v := range status.Vendors {
		for _, dip := range v.DriverInstallPolicies {
			dipRows = append(dipRows, []string{dip.Name, dip.Vendor, dip.Model, dipVersionCell(dip)})
		}
	}
	printTable([]string{"NAME", "VENDOR", "MODEL", "VERSION"}, dipRows)

	fmt.Println("\n=== NodeDeviceReports ===")
	var ndrRows [][]string
	for _, n := range status.Nodes {
		// note는 배제 사유다. operator 가 붙인 라벨을 그대로 옮긴 값이라(NodeStatus.Excluded)
		// 여기서 다시 판정하지 않는다 — 배제 아닌 노드는 빈 문자열로 마지막 열이 트림된다.
		// 사유가 비면 빈 괄호(`제외됨()`)를 내지 않는다.
		note := ""
		if n.Excluded {
			note = "제외됨"
			if n.ExcludedReason != "" {
				note = fmt.Sprintf("제외됨(%s)", n.ExcludedReason)
			}
		}
		// 가속기 없는 배제 노드(GPU 없는 control-plane 이 일반적 형상)도 행을 낸다 — 배제
		// 표시를 장치 루프 안에만 두면 status.devices 가 비어 행이 0개가 되고 라벨이 붙어
		// 있어도 화면에서 사라진다. 배제 아닌 장치 0개 노드는 기존대로 행을 내지 않는다.
		if len(n.Devices) == 0 && n.Excluded {
			ndrRows = append(ndrRows, []string{n.Name, "—", "—", "—", "—", "—", note})
		}
		for _, d := range n.Devices {
			ndrRows = append(ndrRows, []string{
				n.Name, d.Product, d.PCIeAddress,
				strconv.FormatInt(int64(d.Count), 10), strconv.FormatBool(d.DriverLoaded), d.DriverVersion,
				note,
			})
		}
	}
	printTable([]string{"NODE", "DEVICE", "PCI", "COUNT", "LOADED", "VERSION", "NOTE"}, ndrRows)

	fmt.Println("\n=== NPUClusterPolicy ===")
	if len(status.ClusterPolicies) == 0 {
		fmt.Println("  (no NPUClusterPolicy found)")
	}
	for _, cp := range status.ClusterPolicies {
		if cp.Ready == "" {
			fmt.Printf("  %s/%s: no conditions\n", cp.Namespace, cp.Name)
		} else {
			fmt.Printf("  %s/%s: Ready=%s (%s)\n", cp.Namespace, cp.Name, cp.Ready, cp.ReadyReason)
		}
		printVendorConditionLines(cp)
	}

	fmt.Println("\n=== DriverUpgradeState ===")
	var upRows [][]string
	for _, n := range status.Nodes {
		for _, u := range n.Upgrades {
			upRows = append(upRows, []string{
				n.Name, u.Vendor, modelCell(u.Model, u.Models), upgradeStateCell(u), u.CurrentVersion, u.DesiredVersion,
			})
		}
	}
	printTable([]string{"NODE", "VENDOR", "MODEL", "STATE", "CURRENT", "DESIRED"}, upRows)
	printFailedDetails(failedUpgradeLines(status))
}

// vendorConditionLine 은 NPUClusterPolicy 절의 벤더 조건 한 줄이다. kind 는 조건의 종류다 —
// "operator" 는 외부 벤더 operator 감지(<Vendor>OperatorReady), "device-plugin" 은 kcloud 가 배포한
// device plugin 의 준비 상태(<Vendor>DevicePluginReady). 종류를 적지 않으면 operator 의 NotDetected 가
// "장치 미감지" 로 읽힌다.
type vendorConditionLine struct {
	vendor, kind, status, reason, message string
}

// printVendorConditionLines 는 벤더 조건을 operator, device-plugin 순으로 낸다 — 항목이 없으면 줄을 내지 않는다.
// 폭은 printTable 과 같은 원칙으로 데이터에서 잰다(고정폭은 이름이 길어지면 그 줄만 밀린다). 메시지는
// device-plugin 줄에만 붙인다 — 판 형식·실패 노드가 메시지에만 있고, operator 메시지는 긴 설명 문장이다.
func printVendorConditionLines(cp npuctl.ClusterPolicyStatus) {
	lines := make([]vendorConditionLine, 0, len(cp.VendorOperators)+len(cp.DevicePlugins))
	for _, vo := range cp.VendorOperators {
		lines = append(lines, vendorConditionLine{
			vendor: vo.Vendor, kind: "operator", status: vo.Status, reason: vo.Reason,
		})
	}
	for _, dp := range cp.DevicePlugins {
		lines = append(lines, vendorConditionLine{
			vendor: dp.Vendor, kind: "device-plugin", status: dp.Status, reason: dp.Reason, message: dp.Message,
		})
	}
	vendorWidth, kindWidth, statusWidth := 0, 0, 0
	for _, l := range lines {
		vendorWidth = maxInt(vendorWidth, utf8.RuneCountInString(l.vendor))
		kindWidth = maxInt(kindWidth, utf8.RuneCountInString(l.kind))
		statusWidth = maxInt(statusWidth, utf8.RuneCountInString(l.status))
	}
	for _, l := range lines {
		reason := l.reason
		if reason == "" {
			reason = "-"
		}
		row := fmt.Sprintf("    %-*s %-*s %-*s %s",
			vendorWidth, l.vendor, kindWidth, l.kind, statusWidth, l.status, reason)
		if l.message != "" {
			row += "  " + l.message
		}
		fmt.Println(row)
	}
}

// modelCell 은 DUS 의 model 열이다 — 대표값이 generic(모름)이면 관측 목록을 괄호에 보인다.
func modelCell(model string, observed []string) string {
	if model == npuv1alpha1.DriverModelUnknown && len(observed) > 0 {
		return model + "(" + strings.Join(observed, ",") + ")"
	}
	return dashIfEmpty(model)
}

// upgradeStateCell 은 DUS 표의 STATE 칸이다. Idle 인데 롤백한 목표가 남아 있으면 그 목표의 재시도를
// 보류하는 중이라 함께 적는다(diagnose 의 보류 행과 같은 조건). 공백 없이 붙여 열 구분을 지킨다.
func upgradeStateCell(u npuctl.UpgradeStatus) string {
	if u.State == npuv1alpha1.UpgradeStateIdle && u.RolledBackVersion != "" {
		return u.State + "(rolledBack=" + u.RolledBackVersion + ")"
	}
	return u.State
}

func cmdDriverVersion() {
	c := newClientOrExit()
	status, err := c.CollectStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var rows [][]string
	for _, n := range status.Nodes {
		for _, d := range n.Devices {
			rows = append(rows, []string{
				n.Name, d.Vendor, d.Model, d.DriverVersion, strconv.FormatBool(d.DriverLoaded),
			})
		}
	}
	printTable([]string{"NODE", "VENDOR", "MODEL", "DRIVER_VER", "LOADED"}, rows)
}

// upgradeArgs 는 `kubectl npu upgrade` 의 인자다. --force 는 확인 생략이 아니라 drain 강제
// (upgradePolicy.forceUpgrade)다 — 확인 생략은 --yes(-y) 로 따로 받는다.
type upgradeArgs struct {
	vendor, version, model string
	force, auto, yes       bool
}

func parseUpgradeArgs(args []string) (upgradeArgs, error) {
	if len(args) < 1 {
		return upgradeArgs{}, fmt.Errorf("usage: kubectl npu upgrade <vendor> --version <ver> " +
			"[--model <model>] [--force] [--auto] [--yes]")
	}
	p := upgradeArgs{vendor: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--version":
			if i+1 < len(args) {
				p.version = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				p.model = args[i+1]
				i++
			}
		case "--force":
			p.force = true
		case "--auto":
			p.auto = true
		case "--yes", "-y":
			p.yes = true
		}
	}
	if p.version == "" {
		return upgradeArgs{}, fmt.Errorf("--version is required")
	}
	return p, nil
}

// toggleArgs 는 `kubectl npu toggle` 의 인자다.
type toggleArgs struct {
	vendor       string
	enabled, yes bool
}

func parseToggleArgs(args []string) (toggleArgs, error) {
	if len(args) < 1 {
		return toggleArgs{}, fmt.Errorf("usage: kubectl npu toggle <vendor> --enabled <true|false> [--yes]")
	}
	p := toggleArgs{vendor: args[0]}
	enabledStr := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--enabled":
			if i+1 < len(args) {
				enabledStr = args[i+1]
				i++
			}
		case "--yes", "-y":
			p.yes = true
		}
	}
	if enabledStr != "true" && enabledStr != "false" {
		return toggleArgs{}, fmt.Errorf("--enabled must be 'true' or 'false'")
	}
	p.enabled = enabledStr == "true"
	return p, nil
}

func cmdUpgrade(args []string) {
	parsed, err := parseUpgradeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	opts := npuctl.UpgradeOptions{Model: parsed.model, Force: parsed.force, Auto: parsed.auto}
	c := newClientOrExit()
	ctx := context.Background()
	if !parsed.yes {
		preview, err := c.PreviewVendorUpgrade(ctx, parsed.vendor, parsed.version, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if preview.NoChange {
			fmt.Println("Version already set. No change needed.")
			return
		}
		fmt.Fprintf(os.Stderr,
			"DriverInstallPolicy %s 의 드라이버 버전을 %s → %s 로 바꿉니다(설치·재부팅으로 이어질 수 있음). 계속하시겠습니까? [y/N]: ",
			preview.PolicyName, dashIfEmpty(preview.PreviousVersion), preview.NewVersion)
	}
	ok, msg := confirmAction(os.Stdin, parsed.yes, "--yes")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		os.Exit(1)
	}
	result, err := c.UpgradeVendor(ctx, parsed.vendor, parsed.version, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Vendor:          %s\n", result.Vendor)
	fmt.Printf("Policy:          %s\n", result.PolicyName)
	fmt.Printf("Previous Version: %s\n", result.PreviousVersion)
	fmt.Printf("New Version:     %s\n", result.NewVersion)
	if result.NoChange {
		fmt.Println("Version already set. No change needed.")
		return
	}
	fmt.Printf("✓ DriverInstallPolicy %q patched: %s → %s\n", result.PolicyName, result.PreviousVersion, result.NewVersion)
	fmt.Println()
	fmt.Println("Monitor progress:")
	fmt.Println("  kubectl get pods -n kube-system | grep npu-op-installer")
	fmt.Println("  kubectl get events --field-selector reason=UpgradeStarted")
	fmt.Println("  kubectl npu driver-version")
}

func cmdToggle(args []string) {
	parsed, err := parseToggleArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	c := newClientOrExit()
	ctx := context.Background()
	if !parsed.yes {
		preview, err := c.PreviewVendorEnabled(ctx, parsed.vendor, parsed.enabled)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if preview.NoChange {
			fmt.Printf("%s.enabled is already %v. No change needed.\n", preview.Vendor, preview.NewEnabled)
			return
		}
		fmt.Fprintf(os.Stderr,
			"NPUClusterPolicy.spec.%s.enabled 를 %v → %v 로 바꿉니다(광고·DaemonSet 이 바뀝니다). 계속하시겠습니까? [y/N]: ",
			preview.Vendor, preview.PreviousEnabled, preview.NewEnabled)
	}
	ok, msg := confirmAction(os.Stdin, parsed.yes, "--yes")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		os.Exit(1)
	}
	result, err := c.SetVendorEnabled(ctx, parsed.vendor, parsed.enabled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if result.NoChange {
		fmt.Printf("%s.enabled is already %v. No change needed.\n", result.Vendor, result.NewEnabled)
		return
	}
	fmt.Printf("✓ NPUClusterPolicy.spec.%s.enabled patched: %v → %v\n",
		result.Vendor, result.PreviousEnabled, result.NewEnabled)
}

// parseDescribeArgs 는 "node [<이름>] [--json]" 을 파싱한다. 이름을 생략하면
// name=="" 을 돌려주고 ok=true 다 — 호출부(cmdDescribe)가 이걸 "전체 노드 조회" 로
// 해석한다. args[0] 이 "node" 가 아니거나 아예 없으면 ok=false.
func parseDescribeArgs(args []string) (name string, asJSON bool, ok bool) {
	if len(args) < 1 || args[0] != "node" {
		return "", false, false
	}
	rest := args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		name = rest[0]
		rest = rest[1:]
	}
	for _, a := range rest {
		if a == "--json" {
			asJSON = true
		}
	}
	return name, asJSON, true
}

// collectAllNodeDetails 는 클러스터 모든 노드의 상세를 모은다. ListNodeNames·
// CollectNodeDetail 사이의 경합(노드 삭제 등)으로 일부가 실패해도 그 노드만 건너뛰고
// 나머지는 잃지 않는다 — cmdDescribe(전체 노드 경로)와 cmdHealth 가 같은 정책을 공유한다.
func collectAllNodeDetails(ctx context.Context, c *npuctl.Client) []*npuctl.NodeDetail {
	names, err := c.ListNodeNames(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	details := make([]*npuctl.NodeDetail, 0, len(names))
	for _, n := range names {
		detail, err := c.CollectNodeDetail(ctx, n)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error (%s): %v\n", n, err)
			continue
		}
		details = append(details, detail)
	}
	return details
}

func cmdDescribe(args []string) {
	name, asJSON, ok := parseDescribeArgs(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu describe node [<노드>] [--json]")
		os.Exit(1)
	}

	c := newClientOrExit()
	ctx := context.Background()

	if name != "" {
		detail, err := c.CollectNodeDetail(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		printNodeDetail(detail, asJSON)
		return
	}

	// details 는 조회 실패 노드를 걸러낸 목록이다 — collectAllNodeDetails 참조. JSON·텍스트
	// 두 출력 모두 이 걸러진 목록을 쓰므로 아래 renderNodeDetails 가 빈 줄을 셀 때
	// 실패한 노드의 인덱스를 신경 쓸 필요가 없다(실패 노드는 애초에 목록에 없다).
	details := collectAllNodeDetails(ctx, c)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(details); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Print(renderNodeDetails(details))
}

// renderNodeDetails 는 여러 노드 상세를 이어 붙인다 — 블록 사이 빈 줄 하나, 처음과 끝에는
// 없음. details 는 이미 조회 실패 노드를 걸러낸 목록이라고 가정한다.
func renderNodeDetails(details []*npuctl.NodeDetail) string {
	var sb strings.Builder
	for i, d := range details {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(renderNodeDetail(d))
	}
	return sb.String()
}

// printNodeDetail 은 단일 노드 조회 결과를 텍스트 또는 JSON 으로 낸다.
func printNodeDetail(detail *npuctl.NodeDetail, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(detail); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Print(renderNodeDetail(detail))
}

// isAcceleratorResource 는 allocatable 키가 가속기 리소스인지를 이름 목록이 아니라
// 규칙으로 가른다 — internal/apiserver/ui/index.html 의 같은 규칙과 짝을 맞춘다.
// 벤더 이름을 박아두면 새 벤더의 광고량이 조용히 빠진다.
func isAcceleratorResource(key string) bool {
	if !strings.Contains(key, "/") {
		return false
	}
	for _, prefix := range []string{"cpu", "memory", "pods", "ephemeral-storage", "hugepages-", "attachable-volumes-"} {
		if strings.HasPrefix(key, prefix) {
			return false
		}
	}
	return true
}

// renderNodeDetail 은 노드 하나의 상세를 사람이 읽는 텍스트로 낸다. 다른 컨트롤러가
// 아직 안 적어 없는 값(consumers·health·evidence·policy 등)은 추측해 채우지 않고 "—" 로 낸다.
// 장치 표는 printTable(다른 명령이 공유)을 건드리지 않고 이 함수 안에서 직접 구성한다.
func renderNodeDetail(d *npuctl.NodeDetail) string {
	var b strings.Builder

	node := d.Name
	if d.Excluded {
		if d.ExcludedReason != "" {
			node += fmt.Sprintf("  제외됨(%s)", d.ExcludedReason)
		} else {
			node += "  제외됨"
		}
	}
	fmt.Fprintf(&b, "NODE:       %s\n", node)

	if len(d.Devices) == 0 {
		fmt.Fprintf(&b, "DEVICES:    —\n")
	} else {
		fmt.Fprintf(&b, "DEVICES:\n")
		rows := make([][]string, len(d.Devices))
		for i, dev := range d.Devices {
			rows[i] = []string{dev.Product, dev.PCIeAddress, strconv.Itoa(int(dev.Count)),
				strconv.FormatBool(dev.DriverLoaded), dev.DriverVersion}
		}
		b.WriteString(renderTable([]string{"PRODUCT", "PCI", "COUNT", "LOADED", "DRIVER"}, rows))
	}

	var acc []string
	for k := range d.Allocatable {
		if isAcceleratorResource(k) {
			acc = append(acc, k)
		}
	}
	sort.Strings(acc)
	if len(acc) == 0 {
		fmt.Fprintf(&b, "ADVERTISED: —\n")
	} else {
		fmt.Fprintf(&b, "ADVERTISED:\n")
		rows := make([][]string, len(acc))
		for i, k := range acc {
			rows[i] = []string{k, d.Allocatable[k]}
		}
		b.WriteString(renderTable([]string{"RESOURCE", "QTY"}, rows))
	}

	switch {
	case d.ConsumersError != "":
		fmt.Fprintf(&b, "CONSUMERS:  — (%s)\n", d.ConsumersError)
	case d.ConsumersObservedAt == "":
		fmt.Fprintf(&b, "CONSUMERS:  —\n")
	case len(d.Consumers) == 0:
		fmt.Fprintf(&b, "CONSUMERS:  없음\n")
	default:
		fmt.Fprintf(&b, "CONSUMERS:\n")
		rows := make([][]string, len(d.Consumers))
		for i, c := range d.Consumers {
			rows[i] = []string{c.Namespace + "/" + c.Pod, c.Container, c.ResourceName, c.DeviceID}
		}
		b.WriteString(renderTable([]string{"NAMESPACE/POD", "CONTAINER", "RESOURCE", "DEVICE"}, rows))
	}

	if d.Health == nil {
		fmt.Fprintf(&b, "HEALTH:     —\n")
	} else {
		// NODE 줄과 같은 규칙 — Reason 이 비면 빈 괄호를 내지 않는다.
		health := d.Health.State
		if d.Health.Reason != "" {
			health += fmt.Sprintf(" (%s)", d.Health.Reason)
		}
		fmt.Fprintf(&b, "HEALTH:     %s allocationAllowed=%v\n", health, d.Health.AllocationAllowed)
		if len(d.Health.Devices) > 0 {
			rows := make([][]string, len(d.Health.Devices))
			for i, dh := range d.Health.Devices {
				rows[i] = []string{dh.PCIAddress, dh.State, dh.Reason}
			}
			b.WriteString(renderTable([]string{"PCI", "STATE", "REASON"}, rows))
		}
	}

	if len(d.Evidence) == 0 {
		fmt.Fprintf(&b, "EVIDENCE:   —\n")
	} else {
		rows := make([][]string, len(d.Evidence))
		noIdentifier := false
		for i, ev := range d.Evidence {
			vendor, level := ev.Vendor, ev.Level
			// Vendor·Level 이 둘 다 빈 조각을 표 칸에 그대로 두면 만료일만 남아 무엇에
			// 대한 만료인지 알 수 없다. "식별자 없음" 문자열을 VENDOR 칸에 직접 넣으면
			// 그 길이(16바이트)가 renderTable 이 표 폭을 재는 len() 과 어긋나 LEVEL·
			// EXPIRES 열이 밀린다 — 다른 빈 값과 같은 "—" 를 쓰고, 식별자가 없다는
			// 사실은 라벨 줄에 따로 적는다.
			if vendor == "" && level == "" {
				noIdentifier = true
				vendor, level = "—", "—"
			}
			rows[i] = []string{vendor, level, ev.ExpiresAt}
		}
		if noIdentifier {
			fmt.Fprintf(&b, "EVIDENCE:   식별자 없음\n")
		} else {
			fmt.Fprintf(&b, "EVIDENCE:\n")
		}
		b.WriteString(renderTable([]string{"VENDOR", "LEVEL", "EXPIRES"}, rows))
	}

	if len(d.Policies) == 0 {
		fmt.Fprintf(&b, "POLICY:     —\n")
	} else {
		fmt.Fprintf(&b, "POLICY:\n")
		rows := make([][]string, len(d.Policies))
		for i, p := range d.Policies {
			rows[i] = []string{p.Name, p.Vendor, p.Phase}
		}
		b.WriteString(renderTable([]string{"NAME", "VENDOR", "PHASE"}, rows))
	}

	return b.String()
}

// maxInt 는 두 정수 중 큰 값. 표 열 폭을 데이터에서 잴 때 쓴다.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// dashIfEmpty 는 빈 문자열을 "—" 로 바꾼다(renderNodeDetail 의 기존 표기 규칙과 동일).
// 값이 없다는 사실을 빈 칸이 아니라 명시적인 기호로 드러낸다 — 지어내지 않는다.
func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// parsePolicyArgs 는 policy 서브커맨드 인자를 파싱한다(순수 함수 — os.Exit/client 없이 시험 가능).
func parsePolicyArgs(args []string) (action string, req npuctl.PolicyRequest, name string, dryRun bool, err error) {
	if len(args) < 1 {
		return "", req, "", false, fmt.Errorf("usage: kubectl npu policy <create|delete>")
	}
	action = args[0]
	rest := args[1:]
	switch action {
	case "create":
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--name":
				if i+1 < len(rest) {
					req.Name = rest[i+1]
					i++
				}
			case "--vendor":
				if i+1 < len(rest) {
					req.Vendor = rest[i+1]
					i++
				}
			case "--node":
				if i+1 < len(rest) {
					req.NodeName = rest[i+1]
					i++
				}
			case "--intent":
				if i+1 < len(rest) {
					req.Intent = rest[i+1]
					i++
				}
			case "--sharing":
				if i+1 < len(rest) {
					req.Sharing = rest[i+1]
					i++
				}
			case "--profile":
				if i+1 < len(rest) {
					req.Profile = rest[i+1]
					i++
				}
			case "--replicas":
				if i+1 < len(rest) {
					v, e := strconv.Atoi(rest[i+1])
					if e != nil {
						return action, req, "", false, e
					}
					req.Replicas = int32(v)
					i++
				}
			case "--count-per-device":
				if i+1 < len(rest) {
					v, e := strconv.Atoi(rest[i+1])
					if e != nil {
						return action, req, "", false, e
					}
					req.CountPerDevice = int32(v)
					i++
				}
			case "--dry-run":
				dryRun = true
			}
		}
		return action, req, "", dryRun, nil
	case "delete":
		if len(rest) < 1 {
			return action, req, "", false, fmt.Errorf("usage: kubectl npu policy delete <name>")
		}
		return action, req, rest[0], false, nil
	default:
		return action, req, "", false, fmt.Errorf("unknown policy action: %s", action)
	}
}

// cmdPolicy 는 policy 서브커맨드를 처리한다. parsePolicyArgs 로 파싱한 뒤
// CreatePartitionPolicy 또는 DeletePartitionPolicy 를 호출한다.
func cmdPolicy(args []string) {
	action, req, name, dryRun, err := parsePolicyArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	c := newClientOrExit()
	ctx := context.Background()

	switch action {
	case "create":
		result, err := c.CreatePartitionPolicy(ctx, req, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ AcceleratorPartitionPolicy created: %s\n", result)
	case "delete":
		err := c.DeletePartitionPolicy(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ AcceleratorPartitionPolicy deleted: %s\n", name)
	}
}

// cmdCapabilities 는 벤더×제품×능력의 선언(Declared)과 검증(Verified) 상태를 낸다.
// 데이터는 pkg/npuctl.CollectVendorCapabilities — REST /api/v1/vendors/capabilities 와 같은 함수다.
func cmdCapabilities(args []string) {
	asJSON := false
	vendor := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			asJSON = true
		case args[i] == "--vendor" && i+1 < len(args):
			vendor = args[i+1]
			i++
		}
	}
	c := newClientOrExit()
	rows, err := c.CollectVendorCapabilities(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting capabilities: %v\n", err)
		os.Exit(1)
	}
	if vendor != "" {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Vendor == vendor {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.Vendor, r.Product, r.Capability, r.Declared, r.Verified, r.Reason, r.Source})
	}
	printTable([]string{"VENDOR", "PRODUCT", "CAPABILITY", "DECLARED", "VERIFIED", "REASON", "SOURCE"}, table)
}

// cmdDevices 는 클러스터 전체 장치 인벤토리를 노드×PCI 단위로 낸다.
// 데이터는 pkg/npuctl.CollectStatus — status 명령의 NodeDeviceReports 절과 같은 자료다.
func cmdDevices() {
	c := newClientOrExit()
	status, err := c.CollectStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting status: %v\n", err)
		os.Exit(1)
	}
	printDevicesText(status)
}

// printDevicesText 는 ClusterStatus.Nodes[].Devices 를 한 장치당 한 행으로 낸다.
// status 명령처럼 배제·정책 등 다른 관심사를 섞지 않고 장치 인벤토리만 본다.
func printDevicesText(status *npuctl.ClusterStatus) {
	var rows [][]string
	for _, n := range status.Nodes {
		for _, d := range n.Devices {
			rows = append(rows, []string{
				n.Name, dashIfEmpty(d.Vendor), dashIfEmpty(d.Model), dashIfEmpty(d.PCIeAddress),
				strconv.FormatInt(int64(d.Count), 10), dashIfEmpty(d.DriverVersion), strconv.FormatBool(d.DriverLoaded),
			})
		}
	}
	printTable([]string{"NODE", "VENDOR", "MODEL", "PCI", "COUNT", "DRIVER_VER", "LOADED"}, rows)
}

// cmdVendors 는 벤더별 활성화 여부·목표 드라이버 버전을 낸다.
// 데이터는 pkg/npuctl.CollectStatus — status 명령의 DriverInstallPolicies 절과 같은 자료를
// 벤더 하나당 한 행으로 요약한다.
func cmdVendors() {
	c := newClientOrExit()
	status, err := c.CollectStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting status: %v\n", err)
		os.Exit(1)
	}
	printVendorsText(status)
}

// printVendorsText 는 ClusterStatus.Vendors 를 벤더 하나당 한 행으로 낸다.
func printVendorsText(status *npuctl.ClusterStatus) {
	rows := make([][]string, 0, len(status.Vendors))
	for _, v := range status.Vendors {
		rows = append(rows, []string{v.Name, dashIfEmpty(v.SubVendorOf), strconv.FormatBool(v.Enabled), vendorVersions(v)})
	}
	printTable([]string{"NAME", "SUBVENDOR_OF", "ENABLED", "VERSION"}, rows)
}

// dipVersionCell 은 DIP 표의 VERSION 칸이다. TrackOnly 면 설치 경로가 없다는 성격을 적는다(목표 버전이
// 비면 versionSource=Host 로 호스트 버전이 곧 목표다). versionSource=Host 인 설치 정책은 정책 버전이 호스트
// 무드라이버 노드에만 쓰이는 값이라 그 사실을 함께 적는다.
func dipVersionCell(dip npuctl.DIPStatus) string {
	if dip.TrackOnly {
		return dashIfEmpty(dip.DesiredVersion) + " (trackOnly)"
	}
	if strings.EqualFold(dip.VersionSource, "Host") && dip.DesiredVersion != "" {
		return dip.DesiredVersion + " (versionSource=Host)"
	}
	return dip.DesiredVersion
}

// vendorVersions 는 한 벤더에 걸린 DriverInstallPolicy 들의 VERSION 칸(dipVersionCell)을 쉼표로 잇는다.
// 보통은 벤더당 DIP 가 하나라 값도 하나지만, 슬라이스라 여럿이어도 잃지 않는다.
func vendorVersions(v npuctl.VendorStatus) string {
	var out []string
	for _, dip := range v.DriverInstallPolicies {
		if dip.DesiredVersion != "" {
			out = append(out, dipVersionCell(dip))
		}
	}
	if len(out) == 0 {
		return "—"
	}
	return strings.Join(out, ",")
}

// cmdHealth 는 노드×장치 AcceleratorHealth 상태를 낸다.
// 데이터는 pkg/npuctl.CollectNodeDetail(전체 노드) — describe 의 HEALTH 절과 같은 자료를
// 노드를 넘나드는 한 표로 모은다.
func cmdHealth() {
	c := newClientOrExit()
	details := collectAllNodeDetails(context.Background(), c)
	printHealthText(details)
}

// printHealthText 는 노드별 HealthView 를 낸다. Health 가 아예 없는 노드(AcceleratorHealth
// CR 없음 — 가속기 없는 노드가 보통 이렇다)는 건너뛴다: 값을 지어내 대시로 채우지 않는다.
// 장치별 분해(Devices)가 있으면 장치마다 한 행, 없으면 노드 집계 상태로 한 행을 낸다.
func printHealthText(details []*npuctl.NodeDetail) {
	var rows [][]string
	for _, d := range details {
		if d.Health == nil {
			continue
		}
		allocAllowed := strconv.FormatBool(d.Health.AllocationAllowed)
		if len(d.Health.Devices) == 0 {
			rows = append(rows, []string{
				d.Name, "—", dashIfEmpty(d.Health.State), dashIfEmpty(d.Health.Reason), allocAllowed,
			})
			continue
		}
		for _, dh := range d.Health.Devices {
			rows = append(rows, []string{
				d.Name, dashIfEmpty(dh.PCIAddress), dashIfEmpty(dh.State), dashIfEmpty(dh.Reason), allocAllowed,
			})
		}
	}
	printTable([]string{"NODE", "PCI", "STATE", "REASON", "ALLOCATION_ALLOWED"}, rows)
}

// cmdDriver 는 "driver status|rollback" 서브커맨드를 나눈다.
func cmdDriver(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu driver <status|rollback> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		cmdDriverStatus()
	case "rollback":
		cmdDriverRollback(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown driver action: %s\n", args[0])
		os.Exit(1)
	}
}

// cmdDriverStatus 는 노드×벤더 단위 DriverUpgradeState 단계를 낸다.
// 데이터는 pkg/npuctl.CollectStatus — status 명령의 DriverUpgradeStates 절과 같은 자료
// (ClusterStatus.Nodes[].Upgrades)에 MODEL 열을 더해, driver rollback 이 --vendor·
// --model 을 고를 근거를 이 표에서 바로 확인할 수 있게 한다.
func cmdDriverStatus() {
	c := newClientOrExit()
	status, err := c.CollectStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting status: %v\n", err)
		os.Exit(1)
	}
	printDriverStatusText(status)
}

// failedUpgradeLines 는 Failed 인 업그레이드 행의 사유를 "  <노드>/<벤더>: <메시지>" 줄로 낸다(사유가 빈 행은 뺀다).
func failedUpgradeLines(status *npuctl.ClusterStatus) []string {
	var lines []string
	for _, n := range status.Nodes {
		for _, u := range n.Upgrades {
			if u.State == npuv1alpha1.UpgradeStateFailed && u.Message != "" {
				lines = append(lines, fmt.Sprintf("  %s/%s: %s", n.Name, u.Vendor, u.Message))
			}
		}
	}
	return lines
}

// printFailedDetails 는 표 아래에 "Failed 상세" 절을 덧붙인다. 줄이 없으면 아무것도 쓰지 않는다.
func printFailedDetails(lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Failed 상세:")
	for _, l := range lines {
		fmt.Println(l)
	}
}

// printDriverStatusText 는 ClusterStatus.Nodes[].Upgrades 를 노드×벤더 한 행으로 내고, Failed 인 행의
// 사유를 표 아래 "Failed 상세" 절에 한 줄씩 덧붙인다(사유가 길어 표 열로 두지 않는다).
func printDriverStatusText(status *npuctl.ClusterStatus) {
	var rows [][]string
	for _, n := range status.Nodes {
		for _, u := range n.Upgrades {
			rows = append(rows, []string{
				n.Name, u.Vendor, modelCell(u.Model, u.Models), dashIfEmpty(upgradeStateCell(u)),
				dashIfEmpty(u.CurrentVersion), dashIfEmpty(u.DesiredVersion),
			})
		}
	}
	printTable([]string{"NODE", "VENDOR", "MODEL", "STATE", "CURRENT", "DESIRED"}, rows)
	printFailedDetails(failedUpgradeLines(status))
}

// driverRollbackArgs 는 "driver rollback" 인자 파싱 결과다.
type driverRollbackArgs struct {
	node   string
	vendor string
	model  string
	force  bool
}

// parseDriverRollbackArgs 는 "--node <n> [--vendor <v>] [--model <m>] [--force]" 를
// 파싱한다(순수 함수 — os.Exit·client 없이 시험 가능). --node 만 필수다 — vendor 를
// 생략하면 pkg/npuctl.resolveDUS 가 그 노드에 DriverUpgradeState 가 하나뿐일 때만
// 자동으로 고르고, 둘 이상이면(하이브리드 노드) 에러로 후보를 안내한다.
func parseDriverRollbackArgs(args []string) (driverRollbackArgs, error) {
	var a driverRollbackArgs
	force, err := scanFlags(args,
		map[string]*string{"--node": &a.node, "--vendor": &a.vendor, "--model": &a.model}, nil)
	a.force = force
	if err != nil {
		return a, err
	}
	if a.node == "" {
		return a, fmt.Errorf("--node는 필수입니다")
	}
	return a, nil
}

// confirmAction 은 생략 플래그(skip) 또는 대화식 확인(in 첫 줄이 y/yes, 대소문자 무관) 여부를
// 가른다. skip=true 면 in 을 전혀 읽지 않고 즉시 통과한다 — 비대화식(스크립트·사고 대응)
// 경로가 항상 있어야 한다는 계약을 지킨다. 입력이 없을 때(EOF, 파이프에 아무것도 안 보냄)도
// "y/yes 가 아닌 입력"과 똑같이 거절한다 — 안전한 쪽으로 fail. 거절 메시지는 그 명령의
// 생략 플래그 이름을 안내한다(rollback·switch·apply 는 --force, upgrade·toggle 은 --yes —
// upgrade 의 --force 는 drain 강제라는 다른 뜻이다).
func confirmAction(in io.Reader, skip bool, skipFlag string) (ok bool, msg string) {
	if skip {
		return true, ""
	}
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return true, ""
	}
	return false, "확인되지 않았습니다 — " + skipFlag + " 로 확인 없이 실행하거나 프롬프트에 y 로 답하세요"
}

// confirmRollback 은 --force 로 생략하는 네 명령(driver rollback·allocation switch·
// partition apply·sharing apply)의 기존 진입점이다.
func confirmRollback(in io.Reader, force bool) (ok bool, msg string) {
	return confirmAction(in, force, "--force")
}

// cmdDriverRollback 은 DriverUpgradeState.metadata 에 롤백 요청 어노테이션을 patch한다.
// status 는 컨트롤러만 쓴다 — 실제 이전 버전 복구는 상태머신이 다음 reconcile 에서
// 어노테이션을 읽어 수행한다. 이 명령은 요청 신호만 남긴다.
func cmdDriverRollback(args []string) {
	parsed, err := parseDriverRollbackArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu driver rollback --node <노드> [--vendor <벤더>] [--model <모델>] [--force]")
		os.Exit(1)
	}

	c := newClientOrExit()
	ctx := context.Background()

	// 확인 없이 실행되는 경로(--force)는 미리보기 조회 자체를 건너뛴다 — 스크립트·사고
	// 대응 경로는 조회 왕복 하나라도 줄이는 편이 낫고, 어차피 사람에게 보여줄 프롬프트가
	// 없다. 대화식 경로만 먼저 조회해 "무엇을 되돌리는지"(현재 상태·이전 버전)를 확인
	// 받기 전에 보여준다 — 반사적으로 y 를 누르지 않게 한다.
	if !parsed.force {
		preview, err := c.PreviewDriverRollback(ctx, parsed.node, parsed.vendor, parsed.model)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr,
			"노드 %s 의 %s 드라이버를 이전 버전으로 되돌립니다 — 현재 상태: %s, 이전 버전: %s. 계속하시겠습니까? [y/N]: ",
			preview.Node, preview.Vendor, dashIfEmpty(preview.PreviousState), dashIfEmpty(preview.PreviousVersion))
	}
	ok, msg := confirmRollback(os.Stdin, parsed.force)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		os.Exit(1)
	}

	result, err := c.TriggerDriverRollback(ctx, parsed.node, parsed.vendor, parsed.model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("DriverUpgradeState: %s\n", result.DUSName)
	fmt.Printf("Node:               %s\n", result.Node)
	fmt.Printf("Vendor:             %s\n", result.Vendor)
	fmt.Printf("Model:              %s\n", dashIfEmpty(result.Model))
	fmt.Printf("Previous State:     %s\n", dashIfEmpty(result.PreviousState))
	fmt.Printf("Previous Version:   %s\n", dashIfEmpty(result.PreviousVersion))
	fmt.Println("✓ 롤백 요청을 기록했습니다 — 상태 기계가 다음 reconcile 에서 Rollback 으로 전이합니다(kubectl get dus 로 확인)")
}

// cmdAllocation 은 "allocation status|switch" 서브커맨드를 나눈다.
func cmdAllocation(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu allocation <status|switch> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "status":
		cmdAllocationStatus()
	case "switch":
		cmdAllocationSwitch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown allocation action: %s\n", args[0])
		os.Exit(1)
	}
}

// cmdAllocationStatus 는 벤더별 allocation API(devicePlugin|dra) 노출 상태를 낸다.
// 데이터는 pkg/npuctl.CollectAllocationStatus 다.
func cmdAllocationStatus() {
	c := newClientOrExit()
	rows, err := c.CollectAllocationStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting allocation status: %v\n", err)
		os.Exit(1)
	}
	printAllocationStatusText(rows)
}

// printAllocationStatusText 는 AllocationRow 를 벤더 하나당 한 행으로 낸다. 열 순서는
// AllocationRow 필드 선언 순서 그대로다 — 새 판단을 더하지 않는다. AmbiguousProductNodes
// 는 DevicePluginNodes·DRANodes 어느 쪽에도 접지 않고 별도 열로 그대로 보여준다
// (furiosa 통합 DaemonSet 노드처럼 라벨만으로 product 를 못 가르는 경우, allocation.go 참고).
func printAllocationStatusText(rows []npuctl.AllocationRow) {
	header := []string{
		"VENDOR", "ENABLED", "DECLARED", "DEVICE_PLUGIN_NODES", "DRA_NODES",
		"AMBIGUOUS_PRODUCT_NODES", "RETIRED_NODES", "REFUSED",
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.Vendor, strconv.FormatBool(r.Enabled), dashIfEmpty(r.Declared),
			joinOrDash(r.DevicePluginNodes), joinOrDash(r.DRANodes),
			joinOrDash(r.AmbiguousProductNodes), joinOrDash(r.RetiredNodes),
			refusedCell(r.Switch),
		})
	}
	printTable(header, table)
}

// refusedCell 은 선언과 관측이 다른 노드를 "node(reason)" 로 잇는다. 없으면 "—".
func refusedCell(switches []npuctl.AllocationSwitch) string {
	var items []string
	for _, s := range switches {
		if !s.Applied {
			items = append(items, s.Node+"("+s.Reason+")")
		}
	}
	return joinOrDash(items)
}

// joinOrDash 는 노드 이름 목록을 쉼표로 잇는다. 비어 있으면 "—"(dashIfEmpty 와 같은 관례).
func joinOrDash(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, ",")
}

// cmdAllocationSwitch 는 NPUClusterPolicy.spec.<vendor>.advertiseBy 를 patch한다 —
// 선언만 바꾼다. 실제 전환은 컨트롤러가 노드별로 판정하고 거절할 수 있다(자세한 내용은
// pkg/npuctl.SwitchAllocationAPI 참고) — 이 명령은 그 판정을 대신하지 않는다.
func cmdAllocationSwitch(args []string) {
	parsed, err := parseAllocationSwitchArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu allocation switch --vendor <벤더> --to <devicePlugin|dra> [--force]")
		os.Exit(1)
	}

	c := newClientOrExit()
	ctx := context.Background()

	if !parsed.force {
		preview, err := c.PreviewAllocationSwitch(ctx, parsed.vendor, parsed.to)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr,
			"vendor %s 의 allocation API 선언을 %s → %s 로 바꿉니다. 계속하시겠습니까? [y/N]: ",
			parsed.vendor, preview.Previous, preview.New)
	}
	ok, msg := confirmRollback(os.Stdin, parsed.force)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		os.Exit(1)
	}

	result, err := c.SwitchAllocationAPI(ctx, parsed.vendor, parsed.to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Vendor:   %s\n", result.Vendor)
	fmt.Printf("Previous: %s\n", result.Previous)
	fmt.Printf("New:      %s\n", result.New)
	if result.NoChange {
		fmt.Println("✓ 이미 그 값이라 바뀐 것 없음")
		return
	}
	fmt.Println("✓ NPUClusterPolicy.spec.<vendor>.advertiseBy patch됨(선언만 — 노드별 실제 전환은")
	fmt.Println("  `kubectl npu allocation status`, 거절 사유는 status.advertiseSwitch 로 확인)")
}

// allocationSwitchArgs 는 "allocation switch" 인자 파싱 결과다.
type allocationSwitchArgs struct {
	vendor string
	to     string
	force  bool
}

// parseAllocationSwitchArgs 는 "--vendor <v> --to <devicePlugin|dra> [--force]" 를
// 파싱한다(순수 함수). vendor·to 값 자체의 타당성은 pkg/npuctl.SwitchAllocationAPI 가
// 확인한다 — 여기서는 필수 인자 존재만 본다.
func parseAllocationSwitchArgs(args []string) (allocationSwitchArgs, error) {
	var a allocationSwitchArgs
	force, err := scanFlags(args, map[string]*string{"--vendor": &a.vendor, "--to": &a.to}, nil)
	a.force = force
	if err != nil {
		return a, err
	}
	if a.vendor == "" || a.to == "" {
		return a, fmt.Errorf("--vendor 와 --to 는 필수입니다")
	}
	return a, nil
}

// cmdPartition 은 "partition get|apply" 서브커맨드를 나눈다.
func cmdPartition(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu partition <get|apply> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "get":
		cmdPartitionGet()
	case "apply":
		cmdPartitionApply(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown partition action: %s\n", args[0])
		os.Exit(1)
	}
}

// cmdPartitionGet 은 노드×벤더 파티션 레이아웃 현황을 낸다.
// 데이터는 pkg/npuctl.CollectPartitionStatus — AcceleratorPartitionPolicy.status.targets
// 로 이미 해석된 노드만 낸다(NodeSelector 를 다시 풀지 않는다).
func cmdPartitionGet() {
	c := newClientOrExit()
	rows, err := c.CollectPartitionStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting partition status: %v\n", err)
		os.Exit(1)
	}
	printPartitionText(rows)
}

// printPartitionText 는 PartitionRow 를 profile 하나당 한 행으로 낸다.
func printPartitionText(rows []npuctl.PartitionRow) {
	header := []string{"NODE", "VENDOR", "PROFILE", "COUNT_PER_DEVICE", "PHASE"}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.Node, r.Vendor, dashIfEmpty(r.Profile), strconv.FormatInt(int64(r.CountPerDevice), 10), dashIfEmpty(r.Phase),
		})
	}
	printTable(header, table)
}

// partitionApplyArgs 는 "partition apply" 인자 파싱 결과다.
type partitionApplyArgs struct {
	node           string
	vendor         string
	profile        string
	countPerDevice int32
	force          bool
}

// scanFlags 는 partition apply·sharing apply·allocation switch·driver rollback 인자
// 파서가 공유하는 최소 스캐너다 — "--flag 값" 문자열 플래그(strs), "--flag N" 정수
// 플래그(ints), "--force" 스위치 하나를 받는다. 값이 필요한 플래그의 다음 토큰이 "--"
// 로 시작하면 그 토큰을 값으로 삼키지 않고 즉시 에러를 낸다 — 그러지 않으면 "--profile
// --count-per-device 2 --force" 처럼 값 하나를 빠뜨린 사용자의 뒤로 밀린 "--force"가
// 정상 플래그 자리에서 인식돼 확인 없이 통과해버린다(네 명령 모두에서 재현 가능).
func scanFlags(args []string, strs map[string]*string, ints map[string]*int32) (force bool, err error) {
	requireValue := func(i int) (string, error) {
		if i+1 >= len(args) {
			return "", fmt.Errorf("%s 는 값이 필요합니다", args[i])
		}
		if strings.HasPrefix(args[i+1], "--") {
			return "", fmt.Errorf("%s 는 값이 필요한데 다음 토큰이 플래그입니다(%s)", args[i], args[i+1])
		}
		return args[i+1], nil
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--force":
			force = true
		case strs[args[i]] != nil:
			v, e := requireValue(i)
			if e != nil {
				return force, e
			}
			*strs[args[i]] = v
			i++
		case ints[args[i]] != nil:
			v, e := requireValue(i)
			if e != nil {
				return force, e
			}
			n, e := strconv.Atoi(v)
			if e != nil {
				return force, e
			}
			*ints[args[i]] = int32(n)
			i++
		}
	}
	return force, nil
}

// parsePartitionApplyArgs 는 "--node <n> --vendor <v> --profile <p> --count-per-device <n>
// [--force]" 를 파싱한다(순수 함수). 네 값 모두 apply 의 대상·요청값을 이루므로 필수다 —
// vendor 를 devices 목록에서 대신 추측하지 않는다(대상 판정을 CLI 가 대신하지 않는다).
func parsePartitionApplyArgs(args []string) (partitionApplyArgs, error) {
	var a partitionApplyArgs
	force, err := scanFlags(args,
		map[string]*string{"--node": &a.node, "--vendor": &a.vendor, "--profile": &a.profile},
		map[string]*int32{"--count-per-device": &a.countPerDevice})
	a.force = force
	if err != nil {
		return a, err
	}
	if a.node == "" || a.vendor == "" || a.profile == "" {
		return a, fmt.Errorf("--node, --vendor, --profile 는 필수입니다")
	}
	return a, nil
}

// describeLayout 은 확인 프롬프트에 쓰는 사람 읽기용 레이아웃 표시다. profile 이 없으면
// count-per-device 를 같이 보이지 않는다 — "—(count-per-device=0)" 처럼 없는 값 옆에
// 실제 측정인 것처럼 0을 붙이면 오해를 준다.
func describeLayout(profile string, countPerDevice int32) string {
	if profile == "" {
		return "—"
	}
	return fmt.Sprintf("%s(count-per-device=%d)", profile, countPerDevice)
}

// describeSharing 은 확인 프롬프트에 쓰는 사람 읽기용 공유 표시다. exclusive(또는 빈
// 값)는 replicas 가 의미 없으므로 같이 보이지 않는다.
func describeSharing(mode string, replicas int32) string {
	switch mode {
	case "", npuv1alpha1.SharingModeExclusive:
		return dashIfEmpty(mode)
	default:
		return fmt.Sprintf("%s(replicas=%d)", mode, replicas)
	}
}

// cmdPartitionApply 는 node+vendor 의 파티션 레이아웃을 profile/count-per-device 로 맞춘다.
// dupl 린터가 cmdSharingApply 와 구조가 같다고 번갈아 지목한다 — 이유는 아래 nolint 지시문 참고.
//
//nolint:dupl // cmdSharingApply 의 nolint 주석 참고 — 짝을 이루는 두 함수 중 이쪽도
func cmdPartitionApply(args []string) {
	parsed, err := parsePartitionApplyArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu partition apply --node <노드> --vendor <벤더> "+
			"--profile <프로파일> --count-per-device <n> [--force]")
		os.Exit(1)
	}
	c := newClientOrExit()
	ctx := context.Background()

	runApplyCommand(parsed.force,
		func() (string, error) {
			preview, err := c.PreviewPartitionApply(ctx, parsed.node, parsed.vendor, parsed.profile, parsed.countPerDevice)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"노드 %s 의 %s 파티션을 %s 로 적용합니다 — 현재: %s. 계속하시겠습니까? [y/N]: ",
				preview.Node, preview.Vendor, describeLayout(preview.Profile, preview.CountPerDevice),
				describeLayout(preview.CurrentProfile, preview.CurrentCountPerDevice)), nil
		},
		func() (applyOutcome, error) {
			result, err := c.ApplyPartition(ctx, parsed.node, parsed.vendor, parsed.profile, parsed.countPerDevice)
			if err != nil {
				return applyOutcome{}, err
			}
			return applyOutcome{result.Policy, result.Node, result.Vendor, result.Created, result.NoChange}, nil
		})
}

// applyOutcome 은 partition apply·sharing apply 결과를 printApplyResult 가 받는 공통
// 모양으로 좁힌 것이다 — PartitionApplyResult·SharingApplyResult 는 각자 다른 필드도
// 갖지만(Profile/CountPerDevice 대 Mode/Replicas), 이 다섯 필드는 두 타입에 공통이다.
type applyOutcome struct {
	policy, node, vendor string
	created, noChange    bool
}

// runApplyCommand 는 partition apply·sharing apply 가 공유하는 "미리보기(--force 없으면)
// →확인→적용→결과 출력" 흐름이다 — 이 자리에 있던 거의 같은 코드 두 벌을(dupl 이 지적)
// 하나로 묶는다. prompt 는 force 가 아닐 때만 불려 확인 프롬프트 전체 문자열을 돌려주고,
// apply 는 실제 patch/생성을 한다. 두 콜백 모두 호출부(cmdPartitionApply·cmdSharingApply)
// 가 이미 가진 client·parsed 인자를 클로저로 captures 한다 — 이 함수 자체는 어떤 CRD
// 인지 모른다.
func runApplyCommand(force bool, prompt func() (string, error), apply func() (applyOutcome, error)) {
	if !force {
		p, err := prompt()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprint(os.Stderr, p)
	}
	ok, msg := confirmRollback(os.Stdin, force)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		os.Exit(1)
	}
	outcome, err := apply()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	printApplyResult(outcome.policy, outcome.node, outcome.vendor, outcome.created, outcome.noChange)
}

// printApplyResult 는 partition apply·sharing apply 가 공유하는 결과 출력이다. CR 을
// 썼다는 사실만 알린다 — "정상 적용됐다"고 말하지 않는다. 이 정책은 그 뒤 컨트롤러의
// Discover→Validate→Diff→Apply→Verify 단계를 거치고 Failed·RollingBack 등으로 끝날 수
// 있으며(admission webhook 은 이 CRD 에서 failurePolicy=Ignore 라 항상 먼저 막아 주지
// 않는다), 그 판정은 이 명령이 돌려주지 않는다 — created/patch 를 완료로 오인하지
// 않도록 확인 명령을 함께 낸다(allocation switch 의 "선언만" 고지와 같은 이유).
func printApplyResult(policy, node, vendor string, created, noChange bool) {
	fmt.Printf("Policy: %s\n", policy)
	fmt.Printf("Node:   %s\n", node)
	fmt.Printf("Vendor: %s\n", vendor)
	if noChange {
		fmt.Println("✓ 요청값이 현재값과 같아 바뀐 것 없음")
		return
	}
	if created {
		fmt.Println("✓ AcceleratorPartitionPolicy 생성됨(선언만 — 실제 적용 결과는 컨트롤러가")
	} else {
		fmt.Println("✓ AcceleratorPartitionPolicy patch됨(선언만 — 실제 적용 결과는 컨트롤러가")
	}
	fmt.Println("  판정합니다: Ready·Failed·RollingBack 등. `kubectl npu partition get`/")
	fmt.Println("  `sharing get` 로 확인하세요)")
}

// cmdSharing 은 "sharing get|apply" 서브커맨드를 나눈다.
func cmdSharing(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu sharing <get|apply> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "get":
		cmdSharingGet()
	case "apply":
		cmdSharingApply(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown sharing action: %s\n", args[0])
		os.Exit(1)
	}
}

// cmdSharingGet 은 노드×벤더 공유 방식 현황을 낸다.
func cmdSharingGet() {
	c := newClientOrExit()
	rows, err := c.CollectSharingStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting sharing status: %v\n", err)
		os.Exit(1)
	}
	printSharingText(rows)
}

// printSharingText 는 SharingRow 를 노드×벤더 한 행으로 낸다.
func printSharingText(rows []npuctl.SharingRow) {
	header := []string{"NODE", "VENDOR", "MODE", "REPLICAS", "PHASE"}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{
			r.Node, r.Vendor, dashIfEmpty(r.Mode), strconv.FormatInt(int64(r.Replicas), 10), dashIfEmpty(r.Phase),
		})
	}
	printTable(header, table)
}

// sharingApplyArgs 는 "sharing apply" 인자 파싱 결과다.
type sharingApplyArgs struct {
	node     string
	vendor   string
	mode     string
	replicas int32
	force    bool
}

// parseSharingApplyArgs 는 "--node <n> --vendor <v> --mode <timeSliced|mps|exclusive>
// [--replicas <n>] [--force]" 를 파싱한다(순수 함수). --replicas 는 exclusive 에서는
// 쓰이지 않지만(pkg/npuctl.ApplySharing 이 무시한다), 파싱 자체는 항상 받는다.
func parseSharingApplyArgs(args []string) (sharingApplyArgs, error) {
	var a sharingApplyArgs
	force, err := scanFlags(args,
		map[string]*string{"--node": &a.node, "--vendor": &a.vendor, "--mode": &a.mode},
		map[string]*int32{"--replicas": &a.replicas})
	a.force = force
	if err != nil {
		return a, err
	}
	if a.node == "" || a.vendor == "" || a.mode == "" {
		return a, fmt.Errorf("--node, --vendor, --mode 는 필수입니다")
	}
	return a, nil
}

// cmdSharingApply 는 node+vendor 의 공유 방식을 mode/replicas 로 맞춘다. 두 함수가 다른
// 것은 메서드 호출(PreviewSharingApply·ApplySharing)과 서식(describeSharing)뿐이라 인자
// 개수가 cmdPartitionApply 와 같아 dupl 이 구조만 보고 겹친다고 본다. 레이아웃과 공유는
// 서로 다른 필드를 가진 별개 도메인이라, 이 이상 합치면 제네릭 서술자로 둘을 억지로
// 묶어야 해서 각 명령의 흐름을 한눈에 못 읽게 된다 — 반복되는 얇은 배선은 합치지 않는다.
//
//nolint:dupl // runApplyCommand 가 실제 공유 로직(확인→적용→출력)을 이미 뽑아냈다 — 남는
func cmdSharingApply(args []string) {
	parsed, err := parseSharingApplyArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Usage: kubectl npu sharing apply --node <노드> --vendor <벤더> "+
			"--mode <timeSliced|mps|exclusive> [--replicas <n>] [--force]")
		os.Exit(1)
	}
	c := newClientOrExit()
	ctx := context.Background()

	runApplyCommand(parsed.force,
		func() (string, error) {
			preview, err := c.PreviewSharingApply(ctx, parsed.node, parsed.vendor, parsed.mode, parsed.replicas)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"노드 %s 의 %s 공유 방식을 %s 로 적용합니다 — 현재: %s. 계속하시겠습니까? [y/N]: ",
				preview.Node, preview.Vendor, describeSharing(preview.Mode, preview.Replicas),
				describeSharing(preview.CurrentMode, preview.CurrentReplicas)), nil
		},
		func() (applyOutcome, error) {
			result, err := c.ApplySharing(ctx, parsed.node, parsed.vendor, parsed.mode, parsed.replicas)
			if err != nil {
				return applyOutcome{}, err
			}
			return applyOutcome{result.Policy, result.Node, result.Vendor, result.Created, result.NoChange}, nil
		})
}

// diagnoseRow 는 diagnose 표의 한 행이다 — 이미 다른 명령이 읽는 값 중 정상이 아닌
// 것만 모은다. 새 판정 기준을 두지 않는다: health 는 npuctl.HealthStateHealthy 가
// 아닌 값(internal/health.StateHealthy 를 pkg/npuctl 이 중개), upgrade 는 Failed·HostAhead·
// UnverifiedVersion 상수와 DUS 의 rolledBackVersion, allocation 은 AmbiguousProductNodes,
// capability 는 npuctl.VerificationDegraded 상수를 그대로 쓴다.
type diagnoseRow struct {
	category string
	node     string
	vendor   string
	detail   string
}

// buildDiagnoseRows 는 이미 조회된 네 결과(status·헬스·allocation·capabilities)를
// 합쳐 문제 있는 행만 추린다 — 클러스터를 다시 읽지 않고, 여기서 새 상태를 판정하지도
// 않는다(diagnose 는 새 source of truth 를 두지 않는다, 아무것도 고치지 않는다).
func buildDiagnoseRows(
	status *npuctl.ClusterStatus, details []*npuctl.NodeDetail,
	alloc []npuctl.AllocationRow, caps []npuctl.VendorCapabilityRow,
) []diagnoseRow {
	var rows []diagnoseRow
	for _, d := range details {
		if d.Health == nil {
			continue
		}
		if d.Health.State != "" && d.Health.State != npuctl.HealthStateHealthy {
			rows = append(rows, diagnoseRow{
				category: "health", node: d.Name,
				detail: fmt.Sprintf("state=%s reason=%s", d.Health.State, dashIfEmpty(d.Health.Reason)),
			})
		}
		for _, dh := range d.Health.Devices {
			if dh.State != "" && dh.State != npuctl.HealthStateHealthy {
				rows = append(rows, diagnoseRow{
					category: "health", node: d.Name,
					detail: fmt.Sprintf("pci=%s state=%s reason=%s", dh.PCIAddress, dh.State, dashIfEmpty(dh.Reason)),
				})
			}
		}
	}
	if status != nil {
		for _, n := range status.Nodes {
			for _, u := range n.Upgrades {
				switch {
				case u.State == npuv1alpha1.UpgradeStateFailed || u.State == npuv1alpha1.UpgradeStateHostAhead ||
					u.State == npuv1alpha1.UpgradeStateUnverifiedVersion:
					detail := fmt.Sprintf("state=%s current=%s desired=%s", u.State,
						dashIfEmpty(u.CurrentVersion), dashIfEmpty(u.DesiredVersion))
					if u.State == npuv1alpha1.UpgradeStateFailed && u.Message != "" {
						detail += " — " + u.Message
					}
					rows = append(rows, diagnoseRow{category: "upgrade", node: n.Name, vendor: u.Vendor, detail: detail})
				case u.State == npuv1alpha1.UpgradeStateIdle && u.RolledBackVersion != "":
					rows = append(rows, diagnoseRow{
						category: "upgrade", node: n.Name, vendor: u.Vendor,
						detail: fmt.Sprintf("state=%s rolledBack=%s current=%s — 정책 spec 변경 또는 DUS 삭제 전까지 재시도 보류",
							u.State, u.RolledBackVersion, dashIfEmpty(u.CurrentVersion)),
					})
				}
			}
		}
		for _, cp := range status.ClusterPolicies {
			for _, dp := range cp.DevicePlugins {
				if dp.Status == "True" {
					continue
				}
				rows = append(rows, diagnoseRow{
					category: "device-plugin", vendor: dp.Vendor,
					detail: fmt.Sprintf("policy=%s/%s status=%s reason=%s %s", cp.Namespace, cp.Name, dp.Status,
						dashIfEmpty(dp.Reason), dp.Message),
				})
			}
		}
	}
	for _, r := range alloc {
		for _, node := range r.AmbiguousProductNodes {
			rows = append(rows, diagnoseRow{
				category: "allocation", node: node, vendor: r.Vendor,
				detail: "product 미상(ambiguous) — allocation status 참고",
			})
		}
	}
	for _, r := range caps {
		if r.Verified == npuctl.VerificationDegraded {
			rows = append(rows, diagnoseRow{
				category: "capability", vendor: r.Vendor,
				detail: fmt.Sprintf("capability=%s verified=%s", r.Capability, r.Verified),
			})
		}
	}
	return rows
}

// printDiagnoseText 는 diagnoseRow 를 표로 낸다. 문제가 없으면 머리만 찍는다.
func printDiagnoseText(rows []diagnoseRow) {
	header := []string{"CATEGORY", "NODE", "VENDOR", "DETAIL"}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.category, dashIfEmpty(r.node), dashIfEmpty(r.vendor), r.detail})
	}
	printTable(header, table)
}

// cmdDiagnose 는 devices·vendors·health·driver status·allocation status·capabilities 가
// 이미 읽는 데이터를 모아 문제 있는 항목만 낸다. 새로 조회하지 않고(같은 Client 메서드
// 재사용), 아무것도 고치지 않는다(조회 전용 — 확인 게이트 없음).
func cmdDiagnose() {
	c := newClientOrExit()
	ctx := context.Background()

	status, err := c.CollectStatus(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting status: %v\n", err)
		os.Exit(1)
	}
	details := collectAllNodeDetails(ctx, c)
	alloc, err := c.CollectAllocationStatus(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting allocation status: %v\n", err)
		os.Exit(1)
	}
	caps, err := c.CollectVendorCapabilities(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error collecting capabilities: %v\n", err)
		os.Exit(1)
	}
	printDiagnoseText(buildDiagnoseRows(status, details, alloc, caps))
}
