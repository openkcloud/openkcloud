// ============================================================
// state_machine_hostahead_test.go: 호스트 상위 버전 게이트(HostAhead 종점) 시험
// 상세: 로드된 호스트 모듈이 정책보다 높고 다운그레이드가 막혀 있으면 종점에 멈추는지, 정책이
//       따라오면 Idle 로 돌아오는지, versionSource=Host 가 호스트 버전을 목표로 삼는지 본다.
// 생성일: 2026-10-12 | 수정일: 2026-10-12
// ============================================================

package upgrade

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"

	"kcloud-operator/api/v1alpha1"
)

// 정책 580.126.09, 호스트 로드 590.48.01, allowDowngrade=false → Job·cordon 없이 HostAhead.
func TestHandleIdle_HostAhead_ParksWithoutCycle(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verNew, verNew)
	dip := makeJobDIP(verOld, imgOld, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))

	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatalf("handleIdle 실패: %v", err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateHostAhead {
		t.Fatalf("state=%q, HostAhead 기대", dus.Status.State)
	}
	if !hasEvent(drainEvents(sm), "HostDriverAhead") {
		t.Error("HostDriverAhead 이벤트가 없다")
	}
	var jobs batchv1.JobList
	if err := sm.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 {
		t.Errorf("Job %d개 생성됨, 0 기대", len(jobs.Items))
	}
}

// DaemonSet 모드도 같은 게이트를 지난다.
func TestHandleIdle_HostAhead_DaemonsetMode(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verNew, verNew)
	dip := makeDaemonsetDIP(verOld, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateHostAhead {
		t.Fatalf("state=%q, HostAhead 기대", dus.Status.State)
	}
}

// allowDowngrade=true 면 게이트를 지나지 않는다.
func TestHandleIdle_HostAhead_AllowDowngradeSkipsGate(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verNew, verNew)
	dip := makeJobDIP(verOld, imgOld, true, true)
	dip.Spec.Driver.AllowDowngrade = true
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateHostAhead {
		t.Fatal("allowDowngrade=true 인데 HostAhead 로 갔다")
	}
}

// 모듈이 로드되지 않은 장치는 판정에 쓰지 않는다 — self-heal 재설치가 그대로 일어나야 한다.
func TestHandleIdle_HostAhead_IgnoresUnloadedDevice(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verOld, verOld)
	dip := makeJobDIP(verOld, imgOld, false, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(false, verNew))
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateRequired {
		t.Fatalf("state=%q, UpgradeRequired(self-heal) 기대", dus.Status.State)
	}
}

// 다른 벤더 장치의 큰 버전 숫자는 비교하지 않는다. 그 장치를 목록 맨 앞에 두고 모델도 같게 해서
// 벤더 비교만이 그 장치를 걸러내게 한다 — 벤더 비교가 빠지면 첫 장치의 큰 버전이 채택된다.
func TestHandleIdle_HostAhead_IgnoresOtherVendor(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verOld, verOld)
	dip := makeJobDIP(verOld, imgOld, true, true)
	ndr := makeNDRWithDriver(true, verOld)
	ndr.Status.Devices = append([]v1alpha1.DeviceEntry{{
		Vendor: "furiosa", Model: jModel, DriverVersion: "2026.3.1", DriverLoaded: true,
	}}, ndr.Status.Devices...)
	sm := newUpgradeSMWithRecorder(dus, dip, ndr)
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateHostAhead {
		t.Fatal("다른 벤더 버전으로 HostAhead 판정을 했다")
	}
}

// 파싱할 수 없는 버전은 게이트를 걸지 않는다(종전 동작).
func TestHandleIdle_HostAhead_UnparseableKeepsOldBehavior(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, "weird", "weird")
	dip := makeJobDIP(verOld, imgOld, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, "weird"))
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State == v1alpha1.UpgradeStateHostAhead {
		t.Fatal("파싱 불가 버전으로 HostAhead 판정을 했다")
	}
}

// 종점: 여전히 상위면 머문다(60s 재확인), 정책이 따라오면 Idle 로 돌아온다.
func TestHostAhead_StaysWhileAhead(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateHostAhead, verNew, verOld)
	dip := makeJobDIP(verOld, imgOld, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	_, requeue, err := sm.TransitionState(context.Background(), dus, dip)
	if err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateHostAhead || requeue != 60*time.Second {
		t.Fatalf("state=%q requeue=%s, HostAhead·60s 기대", dus.Status.State, requeue)
	}
}

func TestHostAhead_ReturnsToIdleWhenPolicyCatchesUp(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateHostAhead, verNew, verOld)
	dip := makeJobDIP(verNew, imgOld, true, true)
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, Idle 기대", dus.Status.State)
	}
}

// versionSource=Host: 로드된 호스트 버전을 목표로 삼아 정책이 낮아도 Idle 에 머문다(사이클·HostAhead 없음).
func TestHandleIdle_HostSource_AdoptsLoadedVersion(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, verNew, verNew)
	dip := makeJobDIP(verOld, imgOld, true, true)
	dip.Spec.Driver.VersionSource = versionSourceHost
	dip.Spec.UpgradePolicy.IdleCooldownSeconds = int32Ptr(0) // 쿨다운이 불일치 전이를 가리지 않게
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, Idle 기대", dus.Status.State)
	}
}

// versionSource=Host 인데 로드된 장치가 없으면 정책 버전이 목표다(self-heal 이 정책으로 설치).
func TestEffectiveDesiredVersion(t *testing.T) {
	dip := makeJobDIP(verOld, imgOld, true, true)
	if got := EffectiveDesiredVersion(dip, verNew); got != verOld {
		t.Errorf("Policy 모드 = %q, %q 기대", got, verOld)
	}
	dip.Spec.Driver.VersionSource = versionSourceHost
	if got := EffectiveDesiredVersion(dip, verNew); got != verNew {
		t.Errorf("Host 모드·로드됨 = %q, %q 기대", got, verNew)
	}
	if got := EffectiveDesiredVersion(dip, ""); got != verOld {
		t.Errorf("Host 모드·로드 없음 = %q, %q 기대", got, verOld)
	}
}

// versionSource=Host 에서 UnverifiedVersion 의 탈출 판정도 호스트 버전을 쓴다. 정책 버전으로 보면
// Idle 이 호스트 버전을 거부해 주차하고 종점이 정책 버전을 승인해 되돌리는 왕복이 생긴다.
func TestUnverifiedVersion_HostSource_ExitsOnLoadedVersion(t *testing.T) {
	dus := makeJobDUS(v1alpha1.UpgradeStateUnverifiedVersion, verNew, verNew)
	dip := makeJobDIP(verOld, imgOld, true, true)
	dip.Spec.Driver.VersionSource = versionSourceHost
	dip.Spec.VerifiedVersions = []string{verNew}
	sm := newUpgradeSMWithRecorder(dus, dip, makeNDRWithDriver(true, verNew))
	if _, _, err := sm.TransitionState(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, 호스트 버전이 허용 목록 안이므로 Idle 기대", dus.Status.State)
	}
}

// 같은 벤더 장치가 여럿이면 모듈이 로드된 첫 장치의 버전을 쓴다. 로드되지 않은 장치(더 높은 버전)와
// 다른 벤더 장치는 건너뛰고, 모델은 DeviceModelMatches 로 맞춘다.
func TestLoadedHostVersion(t *testing.T) {
	devices := []v1alpha1.DeviceEntry{
		{Vendor: "furiosa", Model: jModel, DriverVersion: "2026.3.1", DriverLoaded: true},
		{Vendor: jVendor, Model: jModel, DriverVersion: verNew, DriverLoaded: false},
		{Vendor: "NVIDIA", Model: jModel, DriverVersion: "580.178.04", DriverLoaded: true},
		{Vendor: jVendor, Model: jModel, DriverVersion: "570.1.1", DriverLoaded: true},
	}
	if got := LoadedHostVersion(devices, jVendor, jModel); got != "580.178.04" {
		t.Errorf("로드된 첫 장치 = %q, 580.178.04 기대", got)
	}
	if got := LoadedHostVersion(devices[1:2], jVendor, jModel); got != "" {
		t.Errorf("로드된 장치 없음 = %q, 빈 문자열 기대", got)
	}
	if got := LoadedHostVersion(devices, "amd", jModel); got != "" {
		t.Errorf("벤더 불일치 = %q, 빈 문자열 기대", got)
	}
	if got := LoadedHostVersion(nil, jVendor, jModel); got != "" {
		t.Errorf("장치 없음 = %q, 빈 문자열 기대", got)
	}
}

// 같은 벤더 NDR 항목이 둘이고 로드되지 않은 쪽이 앞이며 버전이 더 높아도, 상태 기계는 로드된 장치의
// 버전을 목표로 삼아 Idle 에 머문다.
func TestHandleIdle_HostSource_SkipsUnloadedSameVendorEntry(t *testing.T) {
	const hostLoaded = "580.178.04"
	dus := makeJobDUS(v1alpha1.UpgradeStateIdle, hostLoaded, hostLoaded)
	dip := makeDaemonsetDIP(verOld, true) // Job 모드는 로드되지 않은 장치 하나로도 self-heal 이 열린다
	dip.Spec.Driver.VersionSource = versionSourceHost
	dip.Spec.UpgradePolicy.IdleCooldownSeconds = int32Ptr(0) // 쿨다운이 불일치 전이를 가리지 않게
	ndr := makeNDRWithDriver(true, hostLoaded)
	ndr.Status.Devices = append([]v1alpha1.DeviceEntry{{
		Vendor: jVendor, Model: jModel, DriverVersion: verNew, DriverLoaded: false,
	}}, ndr.Status.Devices...)
	sm := newUpgradeSMWithRecorder(dus, dip, ndr)
	if _, _, err := sm.handleIdle(context.Background(), dus, dip); err != nil {
		t.Fatal(err)
	}
	if dus.Status.State != v1alpha1.UpgradeStateIdle {
		t.Fatalf("state=%q, Idle 기대", dus.Status.State)
	}
}
