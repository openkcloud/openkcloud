// ============================================================
// migparse.go: nvidia-smi mig -lgip 출력 파서
// 상세: "MIG <name>" 행에서 profile 이름·Free/Total instances 추출. 순수 함수(on-node 실행 분리).
// 생성일: 2026-07-23 | 수정일: 2026-07-23
// ============================================================
package nvidia

import (
	"strconv"
	"strings"
)

// MigProfile 은 파싱된 MIG GPU instance profile 이다.
type MigProfile struct {
	Name         string // "2g.12gb" (MIG 접두 제거)
	MaxInstances int32  // Free/Total 의 Total
	MemoryGB     int32  // best-effort(정수 반올림, 실패 시 0)
}

// ParseMigProfiles 는 `nvidia-smi mig -lgip` 텍스트에서 profile 목록을 추출한다.
// 형식 예: "|   0  MIG 2g.12gb       14     2/2        11.62 ...". MIG 토큰이 없는 행은 무시.
func ParseMigProfiles(lgip string) []MigProfile {
	lines := strings.Split(lgip, "\n")
	out := make([]MigProfile, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.Trim(line, "| "))
		mi := indexOf(fields, "MIG")
		if mi < 0 || mi+2 >= len(fields) {
			continue
		}
		name := fields[mi+1]
		// name 다음 토큰들 중 "Free/Total"(예 "2/2") 형태를 찾는다.
		var maxInst int32
		for _, f := range fields[mi+2:] {
			if slash := strings.IndexByte(f, '/'); slash > 0 {
				if total, err := strconv.Atoi(f[slash+1:]); err == nil {
					maxInst = int32(total)
					break
				}
			}
		}
		if name == "" || maxInst == 0 {
			continue
		}
		out = append(out, MigProfile{Name: name, MaxInstances: maxInst, MemoryGB: memFromName(name)})
	}
	return out
}

func indexOf(ss []string, target string) int {
	for i, s := range ss {
		if s == target {
			return i
		}
	}
	return -1
}

// memFromName 은 "2g.12gb" → 12 (best-effort). 실패 시 0.
func memFromName(name string) int32 {
	dot := strings.IndexByte(name, '.')
	if dot < 0 || !strings.HasSuffix(name, "gb") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name[dot+1:], "gb"))
	if err != nil {
		return 0
	}
	return int32(n)
}
