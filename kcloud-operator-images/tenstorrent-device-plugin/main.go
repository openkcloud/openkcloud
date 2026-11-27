// ============================================================
// main.go: Tenstorrent Blackhole K8s Device Plugin 진입점
// 상세: graceful shutdown, fsnotify 기반 kubelet.sock 재등록, OS signal 처리
// 생성일: 2026-04-22 | 수정일: 2026-04-22
// ============================================================

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/kcloud/tenstorrent-device-plugin/pkg/plugin"
	"github.com/kcloud/tenstorrent-device-plugin/pkg/registry"
	"k8s.io/klog/v2"
)

const (
	kubeletSocket  = "/var/lib/kubelet/device-plugins/kubelet.sock"
	pluginSocket   = "/var/lib/kubelet/device-plugins/tenstorrent-blackhole.sock"
	resourceName   = "tenstorrent.com/blackhole"
)

func main() {
	flag.Parse()
	klog.InitFlags(nil)
	defer klog.Flush()

	klog.Infof("Tenstorrent Blackhole device plugin starting (resource=%s)", resourceName)

	// OS signal 처리
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// plugin 서버 생성
	srv := plugin.NewServer(resourceName, pluginSocket)

	// kubelet.sock fsnotify 감시 (kubelet 재시작 시 재등록)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		klog.Fatalf("fsnotify 초기화 실패: %v", err)
	}
	defer watcher.Close()

	if err := watcher.Add("/var/lib/kubelet/device-plugins"); err != nil {
		klog.Warningf("kubelet device-plugins 디렉토리 감시 실패 (무시): %v", err)
	}

	// 초기 기동
	if err := startPlugin(srv); err != nil {
		klog.Fatalf("device plugin 기동 실패: %v", err)
	}

	for {
		select {
		case sig := <-sigCh:
			klog.Infof("signal %v 수신 → 종료", sig)
			srv.Stop()
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// kubelet.sock 삭제 감지 → 재기동
			if event.Name == kubeletSocket && event.Has(fsnotify.Remove) {
				klog.Info("kubelet.sock 삭제 감지 → 재기동 대기 중...")
				// kubelet 재기동 대기
				waitForKubeletSocket(kubeletSocket, 30*time.Second)
				klog.Info("kubelet.sock 복구 → plugin 재등록")
				srv.Stop()
				if err := startPlugin(srv); err != nil {
					klog.Fatalf("plugin 재기동 실패: %v", err)
				}
			}

		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			klog.Warningf("fsnotify 오류: %v", watchErr)
		}
	}
}

// startPlugin: plugin 서버를 시작하고 kubelet에 등록한다.
func startPlugin(srv *plugin.Server) error {
	// 기존 소켓 제거
	if err := os.Remove(pluginSocket); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("기존 plugin 소켓 제거 실패: %w", err)
	}

	// gRPC 서버 시작
	if err := srv.Start(); err != nil {
		return fmt.Errorf("plugin 서버 시작 실패: %w", err)
	}

	// kubelet에 등록
	if err := registry.Register(kubeletSocket, pluginSocket, resourceName); err != nil {
		srv.Stop()
		return fmt.Errorf("kubelet 등록 실패: %w", err)
	}

	klog.Info("device plugin 기동 + kubelet 등록 완료")
	return nil
}

// waitForKubeletSocket: kubelet.sock 이 나타날 때까지 폴링한다.
func waitForKubeletSocket(sockPath string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			return
		}
		time.Sleep(1 * time.Second)
	}
	klog.Warningf("kubelet.sock 복구 타임아웃 (%v)", timeout)
}
