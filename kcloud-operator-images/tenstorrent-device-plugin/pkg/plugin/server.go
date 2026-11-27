// ============================================================
// server.go: Tenstorrent Blackhole device plugin gRPC 서버
// 상세: kubelet device plugin API v1beta1 5개 RPC 구현
//       (GetDevicePluginOptions, ListAndWatch, GetPreferredAllocation, Allocate, PreStartContainer)
// 생성일: 2026-04-22 | 수정일: 2026-04-22
// ============================================================

package plugin

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	"github.com/kcloud/tenstorrent-device-plugin/pkg/discovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// Server: device plugin gRPC 서버
type Server struct {
	pluginapi.UnimplementedDevicePluginServer // protobuf 호환 필수 embed
	resourceName string
	socketPath   string
	grpcServer   *grpc.Server
	mu           sync.Mutex
	stopCh       chan struct{}
}

// NewServer: Server 인스턴스를 생성한다.
func NewServer(resourceName, socketPath string) *Server {
	return &Server{
		resourceName: resourceName,
		socketPath:   socketPath,
	}
}

// Start: gRPC 서버를 시작하고 kubelet device plugin API를 등록한다.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopCh = make(chan struct{})

	lis, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}

	s.grpcServer = grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(s.grpcServer, s)

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			klog.Errorf("gRPC 서버 오류: %v", err)
		}
	}()

	// 서버 기동 대기
	return s.waitForServer(5 * time.Second)
}

// Stop: gRPC 서버를 정상 종료한다.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.grpcServer != nil {
		s.grpcServer.Stop()
		s.grpcServer = nil
	}
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh = nil
	}
}

// waitForServer: 소켓 파일 생성을 폴링하며 서버 기동을 확인한다.
func (s *Server) waitForServer(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(
			"unix://"+s.socketPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return os.ErrDeadlineExceeded
}

// ─── DevicePlugin gRPC Interface 구현 ───────────────────────

// GetDevicePluginOptions: plugin 옵션 반환 (PreStartRequired=false)
func (s *Server) GetDevicePluginOptions(_ context.Context, _ *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		PreStartRequired:                false,
		GetPreferredAllocationAvailable: false,
	}, nil
}

// ListAndWatch: 디바이스 목록을 스트리밍으로 kubelet에 전송한다.
func (s *Server) ListAndWatch(_ *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	klog.Info("ListAndWatch 시작")

	devCh := make(chan []discovery.Device, 1)
	if err := discovery.Watch(s.stopCh, devCh); err != nil {
		return err
	}

	for {
		select {
		case <-s.stopCh:
			klog.Info("ListAndWatch 종료")
			return nil
		case devs, ok := <-devCh:
			if !ok {
				return nil
			}
			resp := &pluginapi.ListAndWatchResponse{}
			for _, d := range devs {
				resp.Devices = append(resp.Devices, &pluginapi.Device{
					ID:     d.ID,
					Health: d.Health,
				})
			}
			klog.V(2).Infof("ListAndWatch: %d개 디바이스 보고", len(resp.Devices))
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

// Allocate: 요청된 디바이스 ID에 대해 컨테이너 마운트 정보를 반환한다.
func (s *Server) Allocate(_ context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	resp := &pluginapi.AllocateResponse{}
	for _, containerReq := range req.ContainerRequests {
		containerResp := &pluginapi.ContainerAllocateResponse{}
		for _, devID := range containerReq.DevicesIds {
			devPath := "/dev/tenstorrent/" + devID
			containerResp.Devices = append(containerResp.Devices, &pluginapi.DeviceSpec{
				HostPath:      devPath,
				ContainerPath: devPath,
				Permissions:   "rw",
			})
			klog.V(2).Infof("Allocate: 디바이스 %s → %s 마운트", devID, devPath)
		}
		resp.ContainerResponses = append(resp.ContainerResponses, containerResp)
	}
	return resp, nil
}

// GetPreferredAllocation: 선호 할당 없음 (기본 동작 위임)
func (s *Server) GetPreferredAllocation(_ context.Context, _ *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

// PreStartContainer: PreStart 없음
func (s *Server) PreStartContainer(_ context.Context, _ *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}
