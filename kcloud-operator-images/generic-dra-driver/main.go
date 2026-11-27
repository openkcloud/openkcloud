// ============================================================
// main.go: generic-dra-driver 부트스트랩
// 상세: env 로 설정 경로·노드 이름을 받아 kubeletplugin 을 기동하고 신호를 기다린다.
// 생성일: 2026-08-11
// ============================================================
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run() error {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return fmt.Errorf("NODE_NAME 이 비었다")
	}
	configPath := getenv("CONFIG_PATH", "/etc/generic-dra-driver/config.json")
	cdiDir := getenv("CDI_ROOT", "/etc/cdi")
	hostRoot := getenv("HOST_ROOT", "/host")
	registrarDir := getenv("KUBELET_REGISTRAR_DIRECTORY_PATH", kubeletplugin.KubeletRegistryDir)
	pluginsDir := getenv("KUBELET_PLUGINS_DIRECTORY_PATH", kubeletplugin.KubeletPluginsDir)

	config, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("설정 로드 실패: %w", err)
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("InClusterConfig 실패: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("kube client 생성 실패: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pluginDataDir := pluginsDir + "/" + config.DriverName
	if err := os.MkdirAll(pluginDataDir, 0o750); err != nil {
		return fmt.Errorf("plugin 데이터 디렉터리 생성 실패: %w", err)
	}
	if err := os.MkdirAll(cdiDir, 0o750); err != nil {
		return fmt.Errorf("CDI 디렉터리 생성 실패: %w", err)
	}

	d, err := NewDriver(ctx, client, config, nodeName, cdiDir, hostRoot, registrarDir, pluginDataDir)
	if err != nil {
		return fmt.Errorf("드라이버 기동 실패: %w", err)
	}

	<-ctx.Done()
	stop()

	logger := klog.FromContext(ctx)
	if err := d.Shutdown(logger); err != nil {
		logger.Error(err, "드라이버 종료 실패")
	}
	return nil
}
