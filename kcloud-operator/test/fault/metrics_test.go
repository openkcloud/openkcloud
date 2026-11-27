// ============================================================
// metrics_test.go: 장애 주입 결과 지표 수집
// 상세: 각 시나리오가 판정과 관측값을 여기에 적고, 스위트 종료 시 마크다운 표로 떨군다.
//
//	숫자를 "좋아 보이게" 고르지 않는다 — 미실행 항목은 미실행으로 남긴다.
//
// 생성일: 2026-08-04
// ============================================================
package fault

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// caseResult 는 시나리오 하나의 결과다.
type caseResult struct {
	ID       string
	Title    string
	Verdict  string // PASS / FAIL / SKIP
	Expected string
	Observed string
	Note     string
}

// counters 는 정확성 지표다.
type counters struct {
	WrongSucceeded    int // 실제로 실패했는데 Succeeded 로 끝난 횟수
	StaleWriterWins   int // 옛 epoch 의 결과가 반영된 횟수
	ConcurrentApplies int // 충돌하는 작업이 동시에 Applying 이던 표본 수
	OrphanLeases      int // 종점 뒤 남은 Lease 수
	Rollbacks         int
	ManualRecovery    int
}

// compareResult 는 비교군 한 칸이다(구성 × 장애).
type compareResult struct {
	Baseline, BaselineDesc string
	ProbeID, Probe, Axis   string
	Expect, Observed       string
	Bad                    bool // 잘못된 판정이면 참
}

type suiteReport struct {
	mu       sync.Mutex
	compares []compareResult
	cases    []caseResult
	c        counters
	start    time.Time
}

func newReport() *suiteReport { return &suiteReport{start: time.Now()} }

func (r *suiteReport) add(res caseResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cases = append(r.cases, res)
}

func (r *suiteReport) addCompare(res compareResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compares = append(r.compares, res)
}

func (r *suiteReport) count(f func(*counters)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.c)
}

// write 는 결과를 마크다운으로 남긴다. 파일은 git 추적 대상이다 — 실행 때마다 갱신된다.
func (r *suiteReport) write(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sort.Slice(r.cases, func(i, j int) bool { return r.cases[i].ID < r.cases[j].ID })

	var b strings.Builder
	// 문서 게이트(validate_docs.py)가 docs/ 아래 마크다운에 frontmatter 4키를 요구한다.
	// 자동 생성물이라고 예외가 아니다 — 생성기가 직접 채운다. updated 는 실행일이다.
	b.WriteString("---\n")
	b.WriteString("title: \"Stage 5 장애 주입 결과\"\n")
	b.WriteString("description: \"test/fault 스위트가 실행될 때마다 덮어쓰는 장애 주입 하네스 판정표와 정확성 지표.\"\n")
	b.WriteString("created: 2026-07-31\n")
	fmt.Fprintf(&b, "updated: %s\n", time.Now().Format("2006-01-02"))
	b.WriteString("---\n\n")
	b.WriteString("<!--\n")
	b.WriteString("============================================================\n")
	b.WriteString("stage5-metrics.md: 장애 주입 하네스 실행 결과 (자동 생성)\n")
	b.WriteString("상세: test/fault 스위트가 실행될 때마다 덮어쓴다. 손으로 고치지 않는다.\n")
	b.WriteString("============================================================\n-->\n\n")
	b.WriteString("# Stage 5 장애 주입 결과\n\n")
	// 소요 시간은 적지 않는다 — 실행마다 달라져 재생성 결과가 매번 diff 로 잡히고,
	// 그 잡음 때문에 릴리스 게이트의 "트리 깨끗함" 이 절대 성립하지 않는다.
	b.WriteString("## 시나리오별 판정\n\n")
	b.WriteString("| ID | 시나리오 | 판정 | 기대 | 관측 | 비고 |\n|---|---|:--:|---|---|---|\n")
	for _, c := range r.cases {
		fmt.Fprintf(&b, "| %s | %s | **%s** | %s | %s | %s |\n",
			c.ID, c.Title, c.Verdict, c.Expected, c.Observed, c.Note)
	}
	b.WriteString("\n## 정확성 지표\n\n")
	b.WriteString("| 지표 | 값 |\n|---|--:|\n")
	fmt.Fprintf(&b, "| 잘못된 Succeeded | %d |\n", r.c.WrongSucceeded)
	fmt.Fprintf(&b, "| stale writer 반영 성공 | %d |\n", r.c.StaleWriterWins)
	fmt.Fprintf(&b, "| 충돌 작업 동시 Applying | %d |\n", r.c.ConcurrentApplies)
	fmt.Fprintf(&b, "| 종점 후 잔여 Lease | %d |\n", r.c.OrphanLeases)
	fmt.Fprintf(&b, "| rollback 도달 | %d |\n", r.c.Rollbacks)
	fmt.Fprintf(&b, "| ManualRecoveryRequired 도달 | %d |\n", r.c.ManualRecovery)
	if len(r.compares) > 0 {
		b.WriteString("\n## 비교군 B0~B3\n\n")
		b.WriteString("같은 장애를 기능별 게이트만 바꿔 네 구성에 주입했다. `잘못된 판정` 은 그 구성에서 " +
			"안전 기대가 깨진 표본 수다.\n\n")
		byBase := map[string][]compareResult{}
		var order []string
		for _, c := range r.compares {
			if _, ok := byBase[c.Baseline]; !ok {
				order = append(order, c.Baseline)
			}
			byBase[c.Baseline] = append(byBase[c.Baseline], c)
		}
		b.WriteString("| 구성 | 설명 | 잘못된 판정 / 표본 |\n|---|---|--:|\n")
		for _, name := range order {
			bad := 0
			for _, c := range byBase[name] {
				if c.Bad {
					bad++
				}
			}
			fmt.Fprintf(&b, "| **%s** | %s | %d / %d |\n",
				name, byBase[name][0].BaselineDesc, bad, len(byBase[name]))
		}
		b.WriteString("\n### 장애별 상세\n\n")
		b.WriteString("| 구성 | 장애 | 대상 기능 | 기대 | 관측 | 판정 |\n|---|---|---|---|---|:--:|\n")
		for _, name := range order {
			for _, c := range byBase[name] {
				verdict := "OK"
				if c.Bad {
					verdict = "**깨짐**"
				}
				fmt.Fprintf(&b, "| %s | %s %s | %s | %s | %s | %s |\n",
					c.Baseline, c.ProbeID, c.Probe, c.Axis, c.Expect, c.Observed, verdict)
			}
		}
		b.WriteString("\nB3 는 게이트가 B2 와 같다 — 차이는 Health Manager 가 함께 도는지이고, " +
			"그 축은 F06·F12·SeqD 가 잰다.\n")
	}

	b.WriteString("\n앞의 네 지표는 **0이어야 정상**이다. rollback·ManualRecovery 는 그 자체가 실패가 아니라 " +
		"주입한 장애에 대한 올바른 반응이므로 개수만 기록한다.\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
