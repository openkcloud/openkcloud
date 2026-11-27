// ============================================================
// main.go: 일반 device-plugin 기동
// 상세: 설정을 읽고 gRPC 소켓을 열어 kubelet 에 등록한다. kubelet 재시작으로 소켓이
// 지워지면 감시해 다시 등록한다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func main() {
	cfgPath := flag.String("config", "/etc/kcloud/backend.json", "설정 파일 경로")
	root := flag.String("host-root", "/host", "호스트 루트가 마운트된 위치")
	pluginDir := flag.String("plugin-dir", pluginapi.DevicePluginPath, "kubelet device-plugin 디렉터리")
	flag.Parse()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("설정을 읽지 못했다: %v", err)
	}
	// 시작 로그용 1회 발견 — 실패해도 기동은 계속한다. ListDevices 가 호출마다
	// 다시 발견하므로 여기서 못 찾아도 host 가 나중에 준비되면 광고가 따라잡는다.
	// replicas 를 반영한 쪽(ListDevices 와 같은 함수)을 쓴다 — 물리 장치 수만 찍으면
	// 로그와 실제 광고 수가 어긋나 SW 시분할이 안 걸린 것처럼 보인다.
	devs, err := discoverDevicesWithReplicas(cfg.DeviceNodes, *root, cfg.Replicas)
	if err != nil {
		log.Printf("초기 장치 발견 실패(계속 진행): %v", err)
	}
	if cfg.Replicas >= 2 {
		log.Printf("광고 대상 %s, 장치 %d개(replicas %d)", cfg.ResourceName, len(devs), cfg.Replicas)
	} else {
		log.Printf("광고 대상 %s, 장치 %d개(replicas 미적용)", cfg.ResourceName, len(devs))
	}

	sock := filepath.Join(*pluginDir, "kcloud-generic-"+sanitize(cfg.ResourceName)+".sock")
	_ = os.Remove(sock)

	lis, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("소켓 열기 실패: %v", err)
	}
	srv := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(srv, NewPlugin(cfg, *root))
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("gRPC 종료: %v", err)
		}
	}()

	if err := register(sock, cfg.ResourceName, *pluginDir); err != nil {
		log.Fatalf("kubelet 등록 실패: %v", err)
	}
	log.Printf("등록 완료: %s", sock)

	// kubelet 재시작으로 소켓이 사라지면 프로세스를 끝낸다. DaemonSet 이 다시 띄우고
	// 그때 재등록된다 — 재등록 로직을 자체 구현하는 것보다 단순하고 실패 모드가 적다.
	for {
		if _, err := os.Stat(sock); err != nil {
			log.Printf("소켓이 사라졌다 — 종료해 재기동을 유도한다")
			return
		}
		time.Sleep(10 * time.Second)
	}
}

// register 는 kubelet 에 이 플러그인을 알린다.
//
// pluginapi.KubeletSocket 은 이미 전체 경로다("/var/lib/kubelet/device-plugins/kubelet.sock").
// pluginDir 과 그대로 Join 하면 경로가 두 번 붙는다 — KubeletSocket 이 이미 전체 경로이기 때문이다.
// 그래서 filepath.Base 로 파일명만 뽑아 쓴다. 디렉터리를 바꿔야 하면 여기서 파일명만 다시 붙인다.
func register(sock, resourceName, pluginDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kubeletSock := filepath.Join(pluginDir, filepath.Base(pluginapi.KubeletSocket))
	conn, err := grpc.NewClient("unix://"+kubeletSock,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = pluginapi.NewRegistrationClient(conn).Register(ctx, &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     filepath.Base(sock),
		ResourceName: resourceName,
	})
	return err
}

// sanitize 는 리소스명을 파일명으로 쓸 수 있게 만든다.
func sanitize(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r == '/' || r == '.' {
			out[i] = '-'
		}
	}
	return string(out)
}
