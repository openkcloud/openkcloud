// ============================================================
// occupancy_test.go: PE 점유 수집 단위 테스트
// 상세: RNGD 실측 레이아웃(rngd!npu0mgmt/pe_occupancy)을 합성 트리로 재현해 집계·장치명 추출·
// 비정상 입력 skip 을 검증한다.
// 생성일: 2026-07-29 | 수정일: 2026-10-11
// ============================================================
package main

import (
	"path/filepath"
	"testing"
)

func TestCollectOccupancy_Rngd(t *testing.T) {
	root := t.TempDir()
	// 실측: PE 당 한 줄. 여기서는 8 PE 중 3개 점유.
	writeSysFile(t, filepath.Join(root, "class/rngd_mgmt/rngd!npu0mgmt/pe_occupancy"), "0\n1\n0\n1\n0\n0\n1\n0")
	// pe 노드(비-mgmt)는 무시돼야 한다.
	writeSysFile(t, filepath.Join(root, "class/rngd_mgmt/rngd!npu0pe0/alloc_status"), "")

	got := collectOccupancyFrom(root)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %+v", got)
	}
	o := got[0]
	if o.Device != "npu0" || o.Vendor != "furiosa" || o.Model != "rngd" {
		t.Errorf("meta = %+v", o)
	}
	if o.Occupied != 3 || o.Total != 8 {
		t.Errorf("occupied/total = %v/%v, want 3/8", o.Occupied, o.Total)
	}
}

// 숫자가 아닌 내용은 추정하지 않고 skip 한다.
func TestCollectOccupancy_Garbage(t *testing.T) {
	root := t.TempDir()
	writeSysFile(t, filepath.Join(root, "class/rngd_mgmt/rngd!npu0mgmt/pe_occupancy"), "yes\nno")
	if got := collectOccupancyFrom(root); len(got) != 0 {
		t.Errorf("expected skip, got %+v", got)
	}
}

// RNGD 가 없는 노드(경로 부재)에서는 빈 결과.
func TestCollectOccupancy_Absent(t *testing.T) {
	if got := collectOccupancyFrom(t.TempDir()); len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

// 다중 카드: npu0/npu1 이 각각 집계되고 device 순으로 정렬된다.
func TestCollectOccupancy_MultiCard(t *testing.T) {
	root := t.TempDir()
	writeSysFile(t, filepath.Join(root, "class/rngd_mgmt/rngd!npu1mgmt/pe_occupancy"), "1\n1")
	writeSysFile(t, filepath.Join(root, "class/rngd_mgmt/rngd!npu0mgmt/pe_occupancy"), "0\n0")
	got := collectOccupancyFrom(root)
	if len(got) != 2 || got[0].Device != "npu0" || got[1].Device != "npu1" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got[1].Occupied != 2 {
		t.Errorf("npu1 occupied = %v, want 2", got[1].Occupied)
	}
}
