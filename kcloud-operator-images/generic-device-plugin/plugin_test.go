// ============================================================
// plugin_test.go: device-plugin gRPC 동작 시험
// 상세: kubelet 없이 서비스 메서드를 직접 불러 판정한다. 등록은 별도 경로라 여기 없다. 장치는
// 이제 설정이 아니라 host /dev 발견 결과이므로, root tempdir 에 파일을 직접 깔아 발견을 유도한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// testPlugin 은 rngd/0 (mgmt+pe0 둘 다)·rngd/1 (mgmt 만) 을 host 에 깔아 두 장치를
// 발견시킨다. 1 번은 pe0 를 일부러 비워 "일부 노드만 있어도 Healthy" 도 같이 확인한다.
func testPlugin(t *testing.T) (*Plugin, string) {
	t.Helper()
	root := t.TempDir()
	for _, n := range []string{"dev/rngd/0/mgmt", "dev/rngd/0/pe0", "dev/rngd/1/mgmt"} {
		p := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &Config{
		ResourceName: "furiosa.ai/rngd",
		DeviceNodes:  []string{"/dev/rngd/{{id}}/mgmt", "/dev/rngd/{{id}}/pe0"},
	}
	return NewPlugin(cfg, root), root
}

// host 에 실재하는 장치가 전부 Healthy 로 발견돼야 한다.
func TestListDevicesReportsAll(t *testing.T) {
	p, _ := testPlugin(t)
	devs := p.ListDevices()
	if len(devs) != 2 {
		t.Fatalf("장치 2개여야 한다: %+v", devs)
	}
	for _, d := range devs {
		if d.Health != pluginapi.Healthy {
			t.Errorf("%s 가 Healthy 가 아니다: %s", d.ID, d.Health)
		}
	}
}

// 할당하면 그 장치의 /dev 노드만 들어가야 한다. 남의 장치가 새면 격리가 깨진다.
func TestAllocateInjectsOnlyRequestedDevice(t *testing.T) {
	p, _ := testPlugin(t)
	resp, err := p.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"0"}}},
	})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(resp.ContainerResponses) != 1 {
		t.Fatalf("응답 1개여야 한다: %+v", resp)
	}
	got := resp.ContainerResponses[0].Devices
	if len(got) != 2 {
		t.Fatalf("장치 0 것 둘이어야 한다: %+v", got)
	}
	for _, d := range got {
		if d.HostPath != d.ContainerPath {
			t.Errorf("호스트와 컨테이너 경로가 달라졌다: %+v", d)
		}
		if d.Permissions != "rw" {
			t.Errorf("권한이 rw 가 아니다: %+v", d)
		}
		if strings.Contains(d.HostPath, "/rngd/1/") {
			t.Errorf("요청하지 않은 장치가 샜다: %+v", d)
		}
	}
}

// 모르는 장치를 요청하면 거절한다. 조용히 빈 응답을 주면 파드가 장치 없이 뜬다.
func TestAllocateRejectsUnknownDevice(t *testing.T) {
	p, _ := testPlugin(t)
	_, err := p.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"npu9"}}},
	})
	if err == nil {
		t.Fatal("모르는 장치인데 통과했다")
	}
}

// {{id}} 발견 대상이 host 에 하나도 없으면 광고 자체가 없어야 한다(Unhealthy 장치가
// 아니라 목록이 빈다) — 없는 것을 지어내 광고하지 않는다.
func TestListDevicesEmptyWhenNoneDiscovered(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{ResourceName: "v/r", DeviceNodes: []string{"/dev/rngd/{{id}}/mgmt"}}
	p := NewPlugin(cfg, root)
	if devs := p.ListDevices(); len(devs) != 0 {
		t.Fatalf("발견된 게 없으면 광고도 없어야 한다: %+v", devs)
	}
}

// {{id}} 없는 고정 단일 장치는 host 부재와 무관하게 항상 광고되지만, 노드가
// 하나도 안 잡히면 Unhealthy 여야 한다 — 없는 것을 Healthy 로 광고하지 않는다.
func TestListDevicesFixedDeviceUnhealthyWhenMissing(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{ResourceName: "v/r", DeviceNodes: []string{"/dev/nope/thing"}}
	p := NewPlugin(cfg, root)
	devs := p.ListDevices()
	if len(devs) != 1 || devs[0].Health != pluginapi.Unhealthy {
		t.Fatalf("없는 고정 장치는 Unhealthy 여야 한다: %+v", devs)
	}
}

// replicas>1 이면 광고된 <id>-<n> 을 요청해도 원본 장치와 같은 /dev 가 주입돼야 한다.
func TestAllocateInjectsOnlyRequestedDeviceWithReplicas(t *testing.T) {
	p, _ := testPlugin(t)
	p.cfg.Replicas = 2
	resp, err := p.Allocate(context.Background(), &pluginapi.AllocateRequest{
		ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"0-1"}}},
	})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	got := resp.ContainerResponses[0].Devices
	if len(got) != 2 {
		t.Fatalf("장치 0 것 둘이어야 한다: %+v", got)
	}
	for _, d := range got {
		if !strings.Contains(d.HostPath, "/rngd/0/") {
			t.Errorf("replica 0-1 이 장치 0 의 /dev 를 가리키지 않는다: %+v", d)
		}
	}
}
