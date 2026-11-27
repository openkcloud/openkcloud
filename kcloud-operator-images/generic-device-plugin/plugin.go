// ============================================================
// plugin.go: kubelet device-plugin 인터페이스 구현
// 상세: 벤더 지식이 없다. host /dev 발견 결과를 광고하고, 할당되면 그 장치의 /dev 노드만
// 컨테이너에 넣는다. 남의 장치를 넣지 않는 것이 이 파일의 유일한 안전 요구다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"fmt"
	"time"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// Plugin 은 설정 하나에 대응하는 device-plugin 서비스다.
type Plugin struct {
	pluginapi.UnimplementedDevicePluginServer
	cfg  *Config
	root string // 호스트 루트가 마운트된 위치
}

// NewPlugin 은 설정과 호스트 루트로 서비스를 만든다.
func NewPlugin(cfg *Config, root string) *Plugin {
	return &Plugin{cfg: cfg, root: root}
}

// ListDevices 는 현재 상태의 장치 목록이다. host /dev 를 매번 다시 열거하므로
// hotplug·health 가 바로 반영된다. glob 이 아무것도 못 맞히면 Unhealthy —
// 없는 것을 Healthy 로 광고하면 스케줄러가 못 쓰는 노드에 파드를 붙인다.
//
// ponytail: 호출마다 재발견 — 카드 수가 아주 많아지면(수백대) 캐시+fsnotify 로 바꿀 것.
func (p *Plugin) ListDevices() []*pluginapi.Device {
	devs, err := discoverDevicesWithReplicas(p.cfg.DeviceNodes, p.root, p.cfg.Replicas)
	if err != nil {
		return nil // 발견 자체가 실패하면 아무것도 광고하지 않는다.
	}
	out := make([]*pluginapi.Device, 0, len(devs))
	for _, d := range devs {
		health := pluginapi.Healthy
		nodes, err := resolveNodes(d.NodeGlobs, p.root)
		if err != nil || len(nodes) == 0 {
			health = pluginapi.Unhealthy
		}
		out = append(out, &pluginapi.Device{ID: d.ID, Health: health})
	}
	return out
}

// GetDevicePluginOptions — 이 런타임은 특별한 옵션을 쓰지 않는다.
func (p *Plugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (
	*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// ListAndWatch 는 목록을 보내고, 이후 상태가 바뀔 때마다 다시 보낸다.
func (p *Plugin) ListAndWatch(_ *pluginapi.Empty, srv pluginapi.DevicePlugin_ListAndWatchServer) error {
	last := ""
	for {
		devs := p.ListDevices()
		cur := fmt.Sprintf("%v", devs)
		if cur != last {
			if err := srv.Send(&pluginapi.ListAndWatchResponse{Devices: devs}); err != nil {
				return err
			}
			last = cur
		}
		select {
		case <-srv.Context().Done():
			return nil
		case <-time.After(10 * time.Second):
		}
	}
}

// Allocate 는 요청된 장치의 /dev 노드를 컨테이너에 넣는다. byID 는 매 호출 재발견한
// 결과로 조립한다 — ListDevices 가 광고한 것과 같은 원천을 써야 ID 가 어긋나지 않는다.
func (p *Plugin) Allocate(_ context.Context, req *pluginapi.AllocateRequest) (
	*pluginapi.AllocateResponse, error) {
	devs, err := discoverDevicesWithReplicas(p.cfg.DeviceNodes, p.root, p.cfg.Replicas)
	if err != nil {
		return nil, fmt.Errorf("장치 발견 실패: %w", err)
	}
	byID := map[string]Device{}
	for _, d := range devs {
		byID[d.ID] = d
	}

	resp := &pluginapi.AllocateResponse{}
	for _, cr := range req.ContainerRequests {
		out := &pluginapi.ContainerAllocateResponse{}
		for _, id := range cr.DevicesIds {
			d, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("모르는 장치를 요청했다: %s", id)
			}
			nodes, err := resolveNodes(d.NodeGlobs, p.root)
			if err != nil {
				return nil, fmt.Errorf("장치 %s 의 노드 해석 실패: %w", id, err)
			}
			if len(nodes) == 0 {
				return nil, fmt.Errorf("장치 %s 의 노드가 하나도 없다", id)
			}
			for _, n := range nodes {
				out.Devices = append(out.Devices, &pluginapi.DeviceSpec{
					HostPath:      n,
					ContainerPath: n,
					Permissions:   "rw",
				})
			}
		}
		resp.ContainerResponses = append(resp.ContainerResponses, out)
	}
	return resp, nil
}

// PreStartContainer — 쓰지 않는다.
func (p *Plugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (
	*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// GetPreferredAllocation — 선호 없음. kubelet 이 알아서 고른다.
func (p *Plugin) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (
	*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}
