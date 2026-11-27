// ============================================================
// discovery.go: Tenstorrent 디바이스 검색 + fsnotify 기반 변경 감지
// 상세: /dev/tenstorrent/* glob + /sys/class/tenstorrent/<id>/ 메타 조회
// 생성일: 2026-04-22 | 수정일: 2026-04-22
// ============================================================

package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"k8s.io/klog/v2"
)

// 경로 상수 (테스트에서 devBasePathVar/sysBasePathVar 로 오버라이드 가능)
const (
	devBasePath = "/dev/tenstorrent"
	sysBasePath = "/sys/class/tenstorrent"
)

// 패키지 변수: 테스트에서 임시 교체하여 fake FS 사용
var (
	devBasePathVar = devBasePath
	sysBasePathVar = sysBasePath
)

// Device: 검색된 Tenstorrent 디바이스 정보
type Device struct {
	// ID: 디바이스 인덱스 문자열 (예: "0", "1")
	ID string
	// DevPath: 디바이스 파일 경로 (예: /dev/tenstorrent/0)
	DevPath string
	// SysPath: sysfs 경로 (예: /sys/class/tenstorrent/0)
	SysPath string
	// CardType: 카드 타입 (예: "blackhole", "wormhole")
	CardType string
	// NUMANode: NUMA 노드 번호 (-1이면 미확인)
	NUMANode int
	// Health: "Healthy" 또는 "Unhealthy"
	Health string
}

// Discover: /dev/tenstorrent/* 을 스캔하여 디바이스 목록을 반환한다.
func Discover() ([]Device, error) {
	pattern := filepath.Join(devBasePathVar, "*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %q 실패: %w", pattern, err)
	}

	var devices []Device
	for _, devPath := range matches {
		info, err := os.Stat(devPath)
		if err != nil {
			klog.Warningf("stat %q 실패 (건너뜀): %v", devPath, err)
			continue
		}
		// 파일(char device) 또는 디렉토리가 아닌 경우만 처리
		if info.IsDir() {
			continue
		}

		id := filepath.Base(devPath)
		dev := Device{
			ID:      id,
			DevPath: devPath,
			SysPath: filepath.Join(sysBasePathVar, id),
			Health:  "Healthy",
		}

		// sysfs 메타 조회
		dev.CardType = readSysAttr(dev.SysPath, "card_type")
		dev.NUMANode = readNUMANode(dev.SysPath)

		// Blackhole 카드만 포함 (tenstorrent.com/blackhole resource)
		if !isBlackhole(dev.CardType) {
			klog.V(4).Infof("디바이스 %s: card_type=%q → Blackhole 아님, 건너뜀", id, dev.CardType)
			continue
		}

		devices = append(devices, dev)
	}

	klog.V(2).Infof("Tenstorrent Blackhole 디바이스 %d개 발견", len(devices))
	return devices, nil
}

// Watch: /dev/tenstorrent 디렉토리를 감시하며 변경 시 ch 로 디바이스 목록을 전송한다.
// 호출자는 ctx 취소 또는 채널 close 로 종료한다.
func Watch(stopCh <-chan struct{}, ch chan<- []Device) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify 초기화 실패: %w", err)
	}

	if err := ensureDevDir(); err != nil {
		watcher.Close()
		return err
	}

	if err := watcher.Add(devBasePathVar); err != nil {
		watcher.Close()
		return fmt.Errorf("%q 감시 추가 실패: %w", devBasePathVar, err)
	}

	go func() {
		defer watcher.Close()
		// 초기 목록 전송
		sendDiscovery(ch)

		// debounce: 짧은 시간 내 연속 이벤트 묶음 처리
		timer := time.NewTimer(0)
		timer.Stop()

		for {
			select {
			case <-stopCh:
				return
			case _, ok := <-watcher.Events:
				if !ok {
					return
				}
				timer.Reset(500 * time.Millisecond)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				klog.Warningf("fsnotify 오류: %v", err)
			case <-timer.C:
				sendDiscovery(ch)
			}
		}
	}()

	return nil
}

// sendDiscovery: 디바이스 목록을 발견하여 ch 에 전송한다.
func sendDiscovery(ch chan<- []Device) {
	devs, err := Discover()
	if err != nil {
		klog.Errorf("디바이스 검색 실패: %v", err)
		return
	}
	ch <- devs
}

// ensureDevDir: /dev/tenstorrent 디렉토리가 없으면 생성한다.
func ensureDevDir() error {
	if _, err := os.Stat(devBasePathVar); os.IsNotExist(err) {
		klog.Warningf("%q 디렉토리 없음 → 생성 시도", devBasePathVar)
		if mkErr := os.MkdirAll(devBasePathVar, 0755); mkErr != nil {
			return fmt.Errorf("%q 생성 실패: %w", devBasePathVar, mkErr)
		}
	}
	return nil
}

// readSysAttr: sysfs 속성 파일을 읽어 trim 후 반환한다. 실패 시 빈 문자열.
func readSysAttr(sysPath, attr string) string {
	data, err := os.ReadFile(filepath.Join(sysPath, attr))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readNUMANode: sysfs numa_node 속성을 정수로 반환한다. 실패 시 -1.
func readNUMANode(sysPath string) int {
	val := readSysAttr(sysPath, "numa_node")
	if val == "" {
		return -1
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err != nil {
		return -1
	}
	return n
}

// isBlackhole: card_type 이 Blackhole 계열인지 확인한다.
func isBlackhole(cardType string) bool {
	lower := strings.ToLower(cardType)
	// card_type 이 비어있으면 (sysfs 없는 환경) 일단 포함
	if lower == "" {
		return true
	}
	return strings.Contains(lower, "blackhole") || strings.Contains(lower, "p150") || strings.Contains(lower, "p300")
}
