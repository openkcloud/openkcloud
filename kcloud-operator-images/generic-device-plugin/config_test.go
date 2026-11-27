// ============================================================
// config_test.go: 일반 device-plugin 설정 읽기·검증·발견 시험
// 상세: Device 는 이제 설정이 아니라 host /dev 발견 결과다. 스키마(deviceNodes 템플릿) 검증과
// 발견 로직(discoverDevices/discoverIDs) 을 함께 시험한다. host 는 tempdir 로 흉내낸다.
// 생성일: 2026-08-11 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("표본 작성 실패: %v", err)
	}
	return p
}

func TestLoadConfigValid(t *testing.T) {
	p := writeTemp(t, "c.json", `{
	  "resourceName": "furiosa.ai/rngd",
	  "deviceNodes": ["/dev/rngd/{{id}}/mgmt", "/dev/rngd/{{id}}/pe0"]
	}`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("유효 설정이 거절됐다: %v", err)
	}
	if c.ResourceName != "furiosa.ai/rngd" || len(c.DeviceNodes) != 2 {
		t.Fatalf("설정이 잘못 읽혔다: %+v", c)
	}
}

func TestConfigValidateRejects(t *testing.T) {
	cases := []struct{ name, body, wantSub string }{
		{"리소스명 없음", `{"deviceNodes":["/dev/a"]}`, "resourceName"},
		{"리소스명에 슬래시 없음", `{"resourceName":"rngd","deviceNodes":["/dev/a"]}`, "resourceName"},
		{"deviceNodes 없음", `{"resourceName":"v/r","deviceNodes":[]}`, "deviceNodes"},
		{"deviceNodes 상대경로", `{"resourceName":"v/r","deviceNodes":["dev/a"]}`, "절대경로"},
		// replicas 범위는 operator 쪽 CRD 검증과 별개로 런타임이 다시 본다 — 잘못된 값으로
		// 광고를 시작하면 스케줄러가 없는 장치에 파드를 붙인다.
		{"replicas 상한 초과", `{"resourceName":"v/r","deviceNodes":["/dev/a"],"replicas":17}`, "replicas"},
		{"replicas 음수", `{"resourceName":"v/r","deviceNodes":["/dev/a"],"replicas":-1}`, "replicas"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeTemp(t, "c.json", c.body)
			_, err := LoadConfig(p)
			if err == nil {
				t.Fatal("거절돼야 하는데 통과했다")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("사유에 %q 가 없다: %v", c.wantSub, err)
			}
		})
	}
}

// glob 은 호스트 루트를 갈아끼울 수 있어야 한다. 컨테이너 안에서는 /host 아래에 붙는다.
func TestResolveNodesUsesRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dev", "rngd"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"npu0mgmt", "npu0pe0", "npu1mgmt"} {
		if err := os.WriteFile(filepath.Join(root, "dev", "rngd", n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveNodes([]string{"/dev/rngd/npu0*"}, root)
	if err != nil {
		t.Fatalf("resolveNodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("npu0 것 둘만 나와야 한다: %v", got)
	}
	// 반환은 컨테이너에 넣을 **호스트 경로**여야 한다. root 접두사가 남으면 안 된다.
	for _, g := range got {
		if !strings.HasPrefix(g, "/dev/rngd/npu0") {
			t.Errorf("호스트 경로가 아니다: %s", g)
		}
	}
}

// touch 는 root 밑에 빈 파일을 만든다(host /dev 흉내).
func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverCleanAnchor(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/tenstorrent/0")
	got, err := discoverDevices([]string{"/dev/tenstorrent/{{id}}"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	want := []Device{{ID: "0", NodeGlobs: []string{"/dev/tenstorrent/0"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("발견 결과가 다르다: %+v", got)
	}
}

func TestDiscoverMultipleIndicesAndShared(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/acme/0")
	touch(t, root, "dev/acme/1")
	touch(t, root, "dev/acmectl")
	got, err := discoverDevices([]string{"/dev/acme/{{id}}", "/dev/acmectl"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("장치 2개여야 한다: %+v", got)
	}
	if got[0].ID != "0" || got[1].ID != "1" {
		t.Fatalf("인덱스가 정렬·추출되지 않았다: %+v", got)
	}
	// 공통 고정 노드가 모든 장치에 붙어야 한다.
	if got[0].NodeGlobs[1] != "/dev/acmectl" || got[1].NodeGlobs[1] != "/dev/acmectl" {
		t.Fatalf("공통 노드가 안 붙었다: %+v", got)
	}
}

func TestDiscoverFixedSingleDevice(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/nvidia0")
	touch(t, root, "dev/nvidiactl")
	got, err := discoverDevices([]string{"/dev/nvidia0", "/dev/nvidiactl"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 1 || got[0].ID != "0" {
		t.Fatalf("고정 단일 장치여야 한다: %+v", got)
	}
}

func TestDiscoverRejectsAdjacentWildcard(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/rngd/npu0")
	// {{id}} 뒤에 * 가 붙으면 인덱스 성분이 모호하다 — 거절.
	if _, err := discoverDevices([]string{"/dev/rngd/{{id}}*"}, root); err == nil {
		t.Fatal("인접 와일드카드는 거절돼야 한다")
	}
}

// by-id·by-path 같은 디렉터리가 /dev/<벤더>/ 아래 섞여 있어도 장치로 세면 안 된다.
// tenstorrent 는 /dev/tenstorrent/ 아래 카드 번호(0)와 by-id 디렉터리가 함께 있어, 디렉터리까지 세면 과다광고된다.
func TestDiscoverSkipsNonDeviceDirs(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/tenstorrent/0")
	if err := os.MkdirAll(filepath.Join(root, "dev/tenstorrent/by-id"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := discoverDevices([]string{"/dev/tenstorrent/{{id}}"}, root)
	if err != nil {
		t.Fatalf("발견 실패: %v", err)
	}
	if len(got) != 1 || got[0].ID != "0" {
		t.Fatalf("디렉터리(by-id)가 장치로 잡혔다: %+v", got)
	}
}

func TestDiscoverExpandsReplicasSharingSameNodes(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/tenstorrent/0")
	got, err := discoverDevicesWithReplicas([]string{"/dev/tenstorrent/{{id}}"}, root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("replicas 3 → 장치 3개, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ID] = true
		if len(d.NodeGlobs) != 1 || d.NodeGlobs[0] != "/dev/tenstorrent/0" {
			t.Fatalf("모든 replica 가 같은 /dev 를 가리켜야 한다: %+v", d)
		}
	}
	for _, want := range []string{"0-0", "0-1", "0-2"} {
		if !ids[want] {
			t.Fatalf("replica id %q 없음: %v", want, ids)
		}
	}
}

func TestDiscoverReplicasAppliesToFixedDeviceToo(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "dev/nvidia0")
	got, _ := discoverDevicesWithReplicas([]string{"/dev/nvidia0"}, root, 2)
	if len(got) != 2 {
		t.Fatalf("고정 장치(조기 return 경로)도 확장돼야 한다: %d", len(got))
	}
}

// 상한(16)은 경계값이라 통과해야 한다 — 17 만 막고 16 도 함께 막는 off-by-one 회귀 방지.
func TestConfigValidateAcceptsMaxReplicas(t *testing.T) {
	p := writeTemp(t, "c.json", `{"resourceName":"v/r","deviceNodes":["/dev/a"],"replicas":16}`)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("replicas 16 은 통과해야 한다: %v", err)
	}
	if c.Replicas != 16 {
		t.Fatalf("replicas 가 안 실렸다: %+v", c)
	}
}
