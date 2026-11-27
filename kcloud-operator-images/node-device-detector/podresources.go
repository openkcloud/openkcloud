// ============================================================
// podresources.go: kubelet PodResources 수집·공유파일 변환
// 상세: 사이드카 모드(--podresources-writer)는 kubelet 소켓을 List 해 공유 파일에 원자적으로
// 쓰고, detector 모드는 그 파일을 읽어 NDR status.consumers 로 옮긴다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// podResourcesAllocationsPath 는 사이드카가 쓰고 detector 가 읽는 공유 emptyDir 파일이다.
// var(상수 아님) — 시험이 임시 디렉터리 경로로 바꿔 끼워 Report 호출부를 그대로 고정한다.
var podResourcesAllocationsPath = "/run/podresources/allocations.json"

// isPodResourcesWriterMode 는 사이드카 writer 모드 여부를 고른다. operator 는 이 컨테이너를
// Command: ["/podresources-writer"] 로 띄우므로 실행 파일 이름만으로 충분하다 — Args 의
// --podresources-writer 는 중복 방어다(인자가 유실돼도 uid 0 으로 detector 루프가 돌지 않는다).
func isPodResourcesWriterMode(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if filepath.Base(args[0]) == "podresources-writer" {
		return true
	}
	return len(args) > 1 && args[1] == "--podresources-writer"
}

// Allocation 의 json 태그는 공유 파일의 소문자 키 모양을 고정한다(파일을 읽는 쪽은 대소문자를
// 가리지 않지만, 쓰는 쪽은 태그가 정한 모양 그대로 내야 한다).
type Allocation struct {
	Namespace    string   `json:"namespace"`
	Pod          string   `json:"pod"`
	Container    string   `json:"container"`
	ResourceName string   `json:"resourceName"`
	DeviceIDs    []string `json:"deviceIDs"`
}

// List 는 kubelet PodResources API 의 List 를 부른다(타임아웃 5초, unix 소켓). 소켓이 없으면
// fs.ErrNotExist 를 감싼 오류다. 장치가 없는 컨테이너는 결과에 넣지 않는다.
func List(ctx context.Context, socketPath string) ([]Allocation, error) {
	if _, err := os.Stat(socketPath); err != nil {
		return nil, fmt.Errorf("podresources 소켓 %s: %w", socketPath, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("podresources dial: %w", err)
	}
	defer conn.Close()
	resp, err := v1.NewPodResourcesListerClient(conn).List(ctx, &v1.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("podresources list: %w", err)
	}
	var out []Allocation
	for _, p := range resp.GetPodResources() {
		for _, c := range p.GetContainers() {
			for _, d := range c.GetDevices() {
				if len(d.GetDeviceIds()) == 0 {
					continue
				}
				out = append(out, Allocation{Namespace: p.GetNamespace(), Pod: p.GetName(), Container: c.GetName(),
					ResourceName: d.GetResourceName(), DeviceIDs: append([]string(nil), d.GetDeviceIds()...)})
			}
		}
	}
	return out, nil
}

type allocationsFile struct {
	ObservedAt  string       `json:"observedAt"`
	Error       string       `json:"error,omitempty"`
	Allocations []Allocation `json:"allocations"`
}

// writeAllocations 는 결과를 임시 파일에 쓰고 rename 한다 — detector 가 반쪽 파일을 읽지 않게.
// List 가 실패했으면 allocations 를 비우고 error 에 사유를 적는다(detector 가 consumersError 로 옮긴다).
func writeAllocations(path string, allocs []Allocation, listErr error) error {
	f := allocationsFile{ObservedAt: time.Now().UTC().Format(time.RFC3339), Allocations: allocs}
	if listErr != nil {
		f.Error, f.Allocations = listErr.Error(), nil
	}
	if f.Allocations == nil && listErr == nil {
		f.Allocations = []Allocation{}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// consumersFromFile 은 사이드카가 쓴 파일을 NDR status.consumers 항목(unstructured 맵)으로 옮긴다.
// 반환: (consumers, observedAt, errText). 파일 부재·파싱 실패·error 필드·2분 초과는 consumers=nil +
// errText. 할당 0건은 빈(비nil) 목록 — "모른다" 와 "없다" 를 구분한다.
// 반환형은 []any(원소는 map[string]any) — typed slice 는 runtime.DeepCopyJSON 이 패닉한다.
func consumersFromFile(path string, now time.Time) ([]any, *time.Time, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "podresources 결과 파일 없음: " + err.Error()
	}
	var f allocationsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, "podresources 결과 파싱 실패: " + err.Error()
	}
	if f.Error != "" {
		return nil, nil, "podresources 수집 실패: " + f.Error
	}
	at, err := time.Parse(time.RFC3339, f.ObservedAt)
	if err != nil {
		return nil, nil, "podresources 결과 시각 해석 실패: " + err.Error()
	}
	if now.Sub(at) > 2*time.Minute {
		return nil, nil, "podresources 결과가 오래됨(" + f.ObservedAt + ")"
	}
	out := make([]any, 0, len(f.Allocations))
	for _, a := range f.Allocations {
		for _, id := range a.DeviceIDs {
			out = append(out, map[string]any{
				"deviceID": id, "resourceName": a.ResourceName,
				"namespace": a.Namespace, "pod": a.Pod, "container": a.Container,
			})
		}
	}
	return out, &at, ""
}

// runPodResourcesWriter 는 writer 모드다. 10초마다 List → writeAllocations. 실패도 파일에
// 적는다(사이드카가 죽지 않고, detector 가 사유를 보고한다). 첫 성공 수집·쓰기만 stdout 에
// 한 번 남긴다(이후 성공 주기는 침묵) — 실패 주기 로그와 합쳐 파드 로그로 수집 여부를 본다.
func runPodResourcesWriter(ctx context.Context, socketPath, outPath string) {
	loggedStart := false
	for {
		allocs, err := List(ctx, socketPath)
		if werr := writeAllocations(outPath, allocs, err); werr != nil {
			fmt.Println("podresources write err:", werr)
		} else if err == nil && !loggedStart {
			fmt.Println("podresources writer started:", len(allocs))
			loggedStart = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}
