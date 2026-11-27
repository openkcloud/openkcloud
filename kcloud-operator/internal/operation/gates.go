// ============================================================
// gates.go: 조정자 축별 기능 게이트 (비교군)
// 상세: 각 안전 축을 하나씩 끌 수 있게 한다. 목적은 오직 측정이다 — "조정자가 없었으면 어땠는가"
// 를 옛 코드를 되살리지 않고 같은 하네스로 답하기 위한 것이다.
// 생성일: 2026-08-04 | 수정일: 2026-10-11
// ============================================================
package operation

// Gates 는 끌 수 있는 안전 축이다. 필드가 전부 Disable 형태인 것은 의도다 — 제로값이 "전부 켬"
// 이어야 게이트를 모르는 코드가 안전한 쪽으로 동작한다(Enable* 로 적으면 필드를 안 채운 곳이
// 전부 무방비가 된다). 환경변수로도 CRD 로도 노출하지 않는다 — 운영에서 끌 이유가 없고, 끌 수
// 있는 손잡이를 배포 표면에 두면 언젠가 누군가 끈다. 비교군을 재는 시험만 이 구조체를 채운다.
type Gates struct {
	// DisableConflict 는 진입 판정(충돌 행렬)을 끈다 — 모든 작업이 곧바로 진입한다.
	DisableConflict bool
	// DisableLease 는 노드 잠금을 끈다 — 여럿이 동시에 같은 노드를 만질 수 있다.
	DisableLease bool
	// DisableJournal 은 선행 기록을 끈다 — 크래시 후 무엇을 했는지 알 수 없다.
	DisableJournal bool
	// DisableFencing 은 세대 펜싱을 끈다 — 낡은 프로세스의 기록이 그대로 반영된다.
	DisableFencing bool
	// DisableVerify 는 검증 게이트를 끈다 — 근거 없이 성공을 확정한다.
	DisableVerify bool
}

// Baseline 은 의 비교군 이름과 그 구성이다.
type Baseline struct {
	Name  string
	Desc  string
	Gates Gates
}

// Baselines 는 B0~B3 네 구성이다. B3 는 게이트가 B2 와 같고 Health Manager 가 함께 도는지로
// 갈린다 — 조정자 축이 아니라서 여기서 끄고 켤 것이 없다.
func Baselines() []Baseline {
	return []Baseline{
		{Name: "B0", Desc: "조정자 없음(축 전부 꺼짐)", Gates: Gates{
			DisableConflict: true, DisableLease: true,
			DisableJournal: true, DisableFencing: true, DisableVerify: true,
		}},
		{Name: "B1", Desc: "잠금만", Gates: Gates{
			DisableConflict: true, DisableJournal: true, DisableFencing: true, DisableVerify: true,
		}},
		{Name: "B2", Desc: "조정자 전체(충돌+저널+펜싱+검증)", Gates: Gates{}},
		{Name: "B3", Desc: "B2 + Health Manager", Gates: Gates{}},
	}
}
