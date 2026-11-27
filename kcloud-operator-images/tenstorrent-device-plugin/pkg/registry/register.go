// ============================================================
// register.go: kubelet device plugin 등록 (RegisterRequest gRPC 호출)
// 상세: kubelet.sock 에 RegisterRequest 전송 → device plugin 활성화
// 생성일: 2026-04-22 | 수정일: 2026-04-22
// ============================================================

package registry

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	registerTimeout = 10 * time.Second
)

// Register: kubelet.sock 에 device plugin 등록 요청을 전송한다.
//
// Parameters:
//   - kubeletSocket: kubelet의 gRPC 소켓 경로 (/var/lib/kubelet/device-plugins/kubelet.sock)
//   - pluginSocket:  이 plugin의 소켓 파일명 (tenstorrent-blackhole.sock)
//   - resourceName:  resource 이름 (tenstorrent.com/blackhole)
func Register(kubeletSocket, pluginSocket, resourceName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), registerTimeout)
	defer cancel()

	conn, err := grpc.NewClient(
		"unix://"+kubeletSocket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("kubelet.sock 클라이언트 생성 실패: %w", err)
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	req := &pluginapi.RegisterRequest{
		Version: pluginapi.Version,
		// kubelet은 Endpoint 를 파일명(basename)으로 기대하며
		// 내부적으로 path.Join(DevicePluginPath, Endpoint) 로 소켓 경로를 구성한다.
		Endpoint:     filepath.Base(pluginSocket),
		ResourceName: resourceName,
		Options: &pluginapi.DevicePluginOptions{
			PreStartRequired:                false,
			GetPreferredAllocationAvailable: false,
		},
	}

	if _, err := client.Register(ctx, req); err != nil {
		return fmt.Errorf("kubelet 등록 실패: %w", err)
	}

	klog.Infof("kubelet 등록 완료: resource=%s, endpoint=%s", resourceName, pluginSocket)
	return nil
}
