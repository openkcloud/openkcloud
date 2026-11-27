// ============================================================
// podresources_test.go: PodResources List·공유파일 변환 단위 테스트
// 상세: 가짜 kubelet PodResourcesLister gRPC 서버로 List 평탄화를 검증하고, consumersFromFile 의
// 부재·오래됨·정상 세 경로를 못박는다.
// 생성일: 2026-10-06 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type fakeLister struct {
	v1.UnimplementedPodResourcesListerServer
	resp *v1.ListPodResourcesResponse
}

func (f *fakeLister) List(context.Context, *v1.ListPodResourcesRequest) (*v1.ListPodResourcesResponse, error) {
	return f.resp, nil
}

func TestList_FlattensContainersAndDevices(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "kubelet.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	v1.RegisterPodResourcesListerServer(srv, &fakeLister{resp: &v1.ListPodResourcesResponse{PodResources: []*v1.PodResources{
		{Name: "p1", Namespace: "a", Containers: []*v1.ContainerResources{
			{Name: "c1", Devices: []*v1.ContainerDevices{{ResourceName: "furiosa.ai/rngd", DeviceIds: []string{"npu0", "npu1"}}}},
			{Name: "c2", Devices: []*v1.ContainerDevices{{ResourceName: "nvidia.com/gpu", DeviceIds: []string{"GPU-abc"}}}},
		}},
		{Name: "cpu-only", Namespace: "a", Containers: []*v1.ContainerResources{{Name: "c"}}},
	}}})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	got, err := List(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	want := []Allocation{
		{Namespace: "a", Pod: "p1", Container: "c1", ResourceName: "furiosa.ai/rngd", DeviceIDs: []string{"npu0", "npu1"}},
		{Namespace: "a", Pod: "p1", Container: "c2", ResourceName: "nvidia.com/gpu", DeviceIDs: []string{"GPU-abc"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestList_MissingSocketIsErrNotExist(t *testing.T) {
	_, err := List(context.Background(), filepath.Join(t.TempDir(), "none.sock"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("소켓 부재는 fs.ErrNotExist 로 매칭돼야 한다: %v", err)
	}
}

func TestConsumersFromFile_StaleAndMissingBecomeError(t *testing.T) {
	dir := t.TempDir()
	if c, at, errText := consumersFromFile(filepath.Join(dir, "allocations.json"), time.Now()); c != nil || at != nil || errText == "" {
		t.Fatalf("파일 부재: consumers 는 nil, 사유는 비어 있지 않아야 한다: %v %v %q", c, at, errText)
	}
	old := `{"observedAt":"2020-01-01T00:00:00Z","allocations":[{"namespace":"a","pod":"p","container":"c","resourceName":"r","deviceIDs":["d"]}]}`
	_ = os.WriteFile(filepath.Join(dir, "allocations.json"), []byte(old), 0o644)
	if c, _, errText := consumersFromFile(filepath.Join(dir, "allocations.json"), time.Now()); c != nil || !strings.Contains(errText, "오래됨") {
		t.Fatalf("2분 넘은 결과는 쓰지 않는다: %v %q", c, errText)
	}
	fresh := `{"observedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","allocations":[{"namespace":"a","pod":"p","container":"c","resourceName":"r","deviceIDs":["d1","d2"]}]}`
	_ = os.WriteFile(filepath.Join(dir, "allocations.json"), []byte(fresh), 0o644)
	c, at, errText := consumersFromFile(filepath.Join(dir, "allocations.json"), time.Now())
	if errText != "" || at == nil || len(c) != 2 ||
		c[0].(map[string]any)["deviceID"] != "d1" || c[1].(map[string]any)["deviceID"] != "d2" || c[0].(map[string]any)["pod"] != "p" {
		t.Fatalf("deviceID 마다 한 항목: %v %v %q", c, at, errText)
	}
	empty := `{"observedAt":"` + time.Now().UTC().Format(time.RFC3339) + `","allocations":[]}`
	_ = os.WriteFile(filepath.Join(dir, "allocations.json"), []byte(empty), 0o644)
	if c, _, errText := consumersFromFile(filepath.Join(dir, "allocations.json"), time.Now()); c == nil || len(c) != 0 || errText != "" {
		t.Fatalf("할당 0건은 빈 목록(nil 아님)이어야 한다: %#v %q", c, errText)
	}
}

func TestIsPodResourcesWriterMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"링크 경로만", []string{"/podresources-writer"}, true},
		{"detector 경로 + 인자", []string{"/detector", "--podresources-writer"}, true},
		{"detector 경로, 인자 없음", []string{"/detector"}, false},
		{"링크 경로 + 무관한 인자", []string{"/podresources-writer", "--other"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPodResourcesWriterMode(tc.args); got != tc.want {
				t.Errorf("isPodResourcesWriterMode(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestWriteAllocationsRoundTrip 은 writeAllocations 가 실제로 내는 바이트를 consumersFromFile 로
// 되읽어 정상·오류·0건 세 경로를 못박는다. 이 라운드트립이 없으면 Allocation 의 json 태그가
// 틀려도(혹은 지워져도) 두 모드 사이의 유일한 계약인 파일 모양이 깨진 채로 통과한다.
func TestWriteAllocationsRoundTrip(t *testing.T) {
	// (가) 정상: allocation 1건, deviceIDs 둘.
	dir := t.TempDir()
	path := filepath.Join(dir, "allocations.json")
	allocs := []Allocation{
		{Namespace: "ns1", Pod: "p1", Container: "c1", ResourceName: "furiosa.ai/rngd", DeviceIDs: []string{"npu0", "npu1"}},
	}
	if err := writeAllocations(path, allocs, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"namespace"`, `"pod"`, `"container"`, `"resourceName"`, `"deviceIDs"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("가) 파일 바이트에 소문자 키 %s 가 없다: %s", key, raw)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("가) 파일 권한이 0644 가 아니다: %v %v", fi, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("가) rename 뒤 임시 파일이 남아 있다: %v", err)
	}
	c, at, errText := consumersFromFile(path, time.Now())
	if errText != "" || at == nil || len(c) != 2 ||
		c[0].(map[string]any)["deviceID"] != "npu0" || c[1].(map[string]any)["deviceID"] != "npu1" ||
		c[0].(map[string]any)["resourceName"] != "furiosa.ai/rngd" {
		t.Fatalf("가) 라운드트립 결과가 다르다: %v %v %q", c, at, errText)
	}

	// (나) 오류: List 가 실패했을 때 그 사유가 파일에 실리고 consumersFromFile 에 그대로 전해진다.
	path = filepath.Join(dir, "err.json")
	listErr := errors.New("boom")
	if err := writeAllocations(path, nil, listErr); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"error":"boom"`) {
		t.Errorf("나) 파일에 error 사유가 없다: %s", raw)
	}
	c, at, errText = consumersFromFile(path, time.Now())
	if c != nil || at != nil || !strings.Contains(errText, "boom") {
		t.Fatalf("나) 오류 경로: consumers 는 nil, 사유에 원인이 포함돼야 한다: %v %v %q", c, at, errText)
	}

	// (다) 0건: List 가 성공했지만 결과가 없을 때(allocs=nil, listErr=nil).
	path = filepath.Join(dir, "empty.json")
	if err := writeAllocations(path, nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"allocations":[]`) {
		t.Errorf("다) 0건은 빈 배열이어야 한다: %s", raw)
	}
	c, at, errText = consumersFromFile(path, time.Now())
	if c == nil || len(c) != 0 || at == nil || errText != "" {
		t.Fatalf("다) 0건 경로: 빈 비-nil 목록이어야 한다: %#v %v %q", c, at, errText)
	}
}
