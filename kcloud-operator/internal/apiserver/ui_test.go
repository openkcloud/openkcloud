// ============================================================
// ui_test.go: 정적 대시보드 서빙 테스트
// 상세: 셸은 무인증 200 이어야 하고(브라우저가 헤더를 못 싣는다), 그 안에 클러스터 데이터가
//       한 글자도 없어야 하며, 기존 /api 경로를 가로채지 않아야 한다.
// 생성일: 2026-07-30 | 수정일: 2026-10-11
// ============================================================

package apiserver

import (
	"net/http"
	"strings"
	"testing"
)

func TestUI_ServedWithoutToken(t *testing.T) {
	s, _ := newServer(t, authnOpts{}) // 인증 실패 설정 — 그래도 200 이어야 한다.
	w := do(t, s, "GET", "/ui/", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("정적 셸은 무인증 200 이어야 함: got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type 이 text/html 이어야 함: %q", ct)
	}
	if !strings.Contains(w.Body.String(), "kcloud accelerator console") {
		t.Fatalf("대시보드 HTML 이 아님")
	}
}

func TestUI_RootAlsoServesShell(t *testing.T) {
	s, _ := newServer(t, authnOpts{})
	if w := do(t, s, "GET", "/", "", ""); w.Code != http.StatusOK {
		t.Fatalf("루트도 셸을 내야 함: got %d", w.Code)
	}
}

// 셸에는 클러스터 사실이 들어 있으면 안 된다 — 무인증으로 나가기 때문이다.
func TestUI_ShellCarriesNoClusterData(t *testing.T) {
	s, _ := newServer(t, authnOpts{}, invObjects()...)
	body := do(t, s, "GET", "/ui/", "", "").Body.String()
	for _, secret := range []string{"GPU-aaa", "worker1", "rngd-1", "0000:3b:00.0"} {
		if strings.Contains(body, secret) {
			t.Fatalf("정적 셸에 클러스터 데이터가 새어나감: %q", secret)
		}
	}
}

// UI 라우트가 API 라우트를 가로채지 않아야 한다.
func TestUI_DoesNotShadowAPI(t *testing.T) {
	s, _ := newServer(t, authnOpts{})
	if w := do(t, s, "GET", "/api/v1/status", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("API 경로는 여전히 인증을 요구해야 함: got %d", w.Code)
	}
}

// 외부 리소스를 부르면 air-gap 에서 화면이 깨진다 — 절대경로 http(s) 참조가 없어야 한다.
func TestUI_HasNoExternalReferences(t *testing.T) {
	body := string(indexHTML)
	for _, bad := range []string{"http://", "https://", "//cdn.", "integrity="} {
		if strings.Contains(body, bad) {
			t.Fatalf("외부 리소스 참조가 있으면 air-gap 에서 깨진다: %q", bad)
		}
	}
}

// stale/pending 행은 정상 행과 시각적으로 달라야 한다(inventory.go 정직성 계약). 셸 JS 를
// 직접 실행할 수는 없으니, 그 처리 코드가 셸에 존재한다는 사실 자체를 고정한다 — 이렇게 해 두면
// 이후 누군가 이 분기를 지워도(예: 표 리팩터 중 실수로) 이 테스트가 즉시 깨진다.
func TestUI_RendersStaleAndPending(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{"x.pending", "x.stale", "x.staleReason", "d.pending", "d.stale", "d.staleReason"} {
		if !strings.Contains(body, want) {
			t.Fatalf("셸 JS 가 %q 를 다뤄야 함(stale/pending 은 정상 행과 구분되어야 한다)", want)
		}
	}
}

// 검증 등급(Verified/Documented/LegacyDocumented) 을 뭉개면 "지원" 과 "문서상 지원" 이 같아
// 보인다 — partitionBadge 가 profiles[].supportLevel 을 실제로 소비하는지 고정한다.
func TestUI_RendersPartitionSupportLevel(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{"supportLevel", "hardwareSupported", "partitionBadge"} {
		if !strings.Contains(body, want) {
			t.Fatalf("셸 JS 가 %q 를 다뤄야 함(파티션 지원 등급이 안 보이면 안 됨)", want)
		}
	}
}

// replica 는 광고 배수일 뿐 물리 장치 개수가 아니다 — "×N" 배지만 있으면 "장치 N 개" 로 오독된다.
// 대시보드 표에 공유·격리 없음 문구가 실제로 붙는지 고정한다.
func TestUI_RendersSharedNoIsolationWording(t *testing.T) {
	body := string(indexHTML)
	if !strings.Contains(body, "물리 장치 1개 공유") {
		t.Fatalf("time-sliced 배지에 '물리 장치 1개 공유' 문구가 없음 — replica 를 독립 장치로 오독하게 둔다")
	}
}

// oversubscribed: 광고 수가 물리 장치 수를 넘는다는 관측만으로 "시분할"·"격리 없음" 이라고
// 단정하면 안 된다(RNGD PE 처럼 하드웨어 격리가 있는 경우가 있다) — shareCell 이 이 상태를 별도
// 문구로 렌더하는지 고정한다. shareCell 의 호출부는 TestUI_HonestyBadgesAreActuallyCalled 가 이미
// 고정하므로(호출부가 지워지면 그쪽이 깨진다), 여기서는 이 상태 전용 문구만 고정한다.
func TestUI_RendersOversubscribedWording(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{"oversubscribed", "초과 광고", "원인 미확정"} {
		if !strings.Contains(body, want) {
			t.Fatalf("shareCell 에 oversubscribed 전용 문구 %q 가 없음 — 원인 미확정 상태를 시분할/격리없음으로 단정하면 안 됨", want)
		}
	}
}

// cordoned 행도 stale/pending 과 같은 부류다 — 장치는 있어도 스케줄되지 않는다. 대시보드 표는
// invTree 와 달리 노드 헤더가 없으므로 healthBadge 가 schedulable 을 직접 반영해야 한다.
// 주의: "!x.schedulable" 이나 "cordoned" 만으로는 고정되지 않는다 — 둘 다 집계 카드와
// 인벤토리 탭 노드 헤더에 이미 있어서 healthBadge 분기를 지워도 통과한다. 이 분기에만
// 있는 캡션을 검사한다.
func TestUI_RendersCordonedRow(t *testing.T) {
	body := string(indexHTML)
	const want = "스케줄 불가 — 배치되지 않음"
	if !strings.Contains(body, want) {
		t.Fatalf("healthBadge 에 cordoned 캡션 %q 가 없음(cordoned 행이 정상 행과 구분되지 않음)", want)
	}
}

// 네 상태다. "측정해서 못 한다" 와 "아직 못 봤다" 를 같게 그리면 관측이 비어 있는 장치가
// 못 하는 장치로 보여 화면이 선택지를 잠근다. 반대로 미관측 칸이 미지원 칸을 삼키면 진짜로
// 안 되는 장치를 고르게 된다 — 네 갈래가 모두 남아 있어야 한다.
// 어서션 문자열은 shareBadge 안에서만 나오는 것을 고른다 — "미관측 — 판정 불가" 는
// partitionBadge 에도 있어서, 그것만 찾으면 공유 분기를 지워도 이 시험이 통과한다.
func TestUI_ShareBadgeFourStateIsPinned(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{
		"m.verified", "m.supported",
		"지원(검증됨)", "미검증(",
		`if (!m.verification || m.verification === "required")`, "공유 미관측 — 판정 불가",
		`'<span class="badge bad">미지원' + (m.reason`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shareBadge 가 %q 분기를 다뤄야 함(네 상태 중 하나가 사라지면 안 됨)", want)
		}
	}
}

// 선택 가능 판정이 배지와 같은 축을 쓰는지. 두 곳이 갈리면 경고 배지를 단 채 회색인
// 라디오가 생긴다 — 화면이 스스로 모순된다.
func TestUI_공유_선택_판정은_배지와_같은_축(t *testing.T) {
	body := string(indexHTML)
	if !strings.Contains(body, "function shareSelectable(m)") {
		t.Fatal("공유 선택 판정 함수가 없다")
	}
	for _, want := range []string{
		"shareSelectable(s.timeSlicing)", "shareSelectable(s.multiProcess)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("마법사가 %q 를 쓰지 않는다 — 판정이 두 곳으로 갈린다", want)
		}
	}
	// supported 만 보고 잠그던 옛 판정이 남아 있으면 안 된다.
	if strings.Contains(body, "!(s.timeSlicing && s.timeSlicing.supported)") {
		t.Error("supported 만 보는 옛 게이팅이 남아 있다 — 미관측 장치가 잠긴다")
	}
}

// 정직성 배지는 정의만으로는 아무것도 못 한다 — 호출부가 사라지면 화면에서 사라진다.
// 위 테스트들은 전부 헬퍼 "본문"(파라미터명이 다르거나 그 안의 문자열)에 어서션을 걸어서,
// 호출부만 지워도(헬퍼 자체는 죽은 코드로 남아) 계속 통과한다. 여기서는 호출부에만
// 나타나는 형태를 고정한다. healthBadge 는 정의(`function healthBadge(x) {`)와
// 호출부(`healthBadge(x)`)가 같은 파라미터명을 써서 단순 "healthBadge(x)" 로는 구분이
// 안 된다 — 호출부에만 있는 뒤따르는 인자까지 고정한다.
func TestUI_HonestyBadgesAreActuallyCalled(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{
		"healthBadge(x), esc(x.nodeName)", "partitionBadge(c.partition)",
		"shareBadge(s.timeSlicing)", "shareBadge(s.multiProcess)", "shareBadge(s.brokered)",
		"shareCell(x),", "shareCell(d)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("정직성 배지 호출부 %q 가 없음 — 헬퍼가 있어도 화면엔 안 나온다", want)
		}
	}
}

// 논리 파티션 수는 profile 이름 개수가 아니라 장치당 인스턴스 합이다. 카드가 인스턴스를
// 실제로 읽는지, 그리고 미상 행을 조용히 빼지 않고 표시하는지 고정한다. 호출부에서만 나오는
// "countPerDevice" 를 검사한다 — 헬퍼 정의부나 주석에만 있는 경우 삭제해도 이 테스트가
// 계속 통과하는 함정을 피하기 위해서다(TestUI_HonestyBadgesAreActuallyCalled 와 같은 규율).
func TestUI_LogicalPartitionCardUsesInstanceCounts(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{"partitionInstances", "countPerDevice", "미상 "} {
		if !strings.Contains(body, want) {
			t.Fatalf("대시보드 카드가 %q 를 다뤄야 함", want)
		}
	}
}

// hardwareSupported=false 는 "측정된 미지원" 과 "미관측" 둘 다일 수 있다(NVIDIA 백엔드가
// 관측 실패에도 false 를 쓴다). 화면이 둘을 갈라야 한다 — verification 분기를 고정한다.
func TestUI_PartitionBadgeSeparatesUnobserved(t *testing.T) {
	body := string(indexHTML)
	for _, want := range []string{`p.verification === "required"`, "미관측 — 판정 불가", "파티션 불가"} {
		if !strings.Contains(body, want) {
			t.Fatalf("partitionBadge 가 %q 를 다뤄야 함(관측 실패를 하드웨어 사실로 단언하면 안 된다)", want)
		}
	}
}

// 마법사가 벤더 용어를 병기하는지 — 결과로만 물으면 용어를 아는 관리자가 무엇이 걸리는지 모른다.
func TestUI_마법사_벤더_용어_병기(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		"Time-Slicing", "Multi-Process", "Multi-Instance",
		"exclusive", "shared", "partitioned", "partitioned-shared",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("마법사에 %q 표기가 없다", want)
		}
	}
}

// 고급 설정은 접지 않는다 — 접혀 있으면 그 기능이 없어 보인다.
func TestUI_고급_설정은_펼침(t *testing.T) {
	html := string(indexHTML)
	if strings.Contains(html, "<details") && !strings.Contains(html, "<details open") {
		t.Error("고급 설정을 details 로 감쌌으면 open 이어야 한다")
	}
	if !strings.Contains(html, "구현 방식") {
		t.Error("구현 방식 선택 칸이 없다")
	}
}

// blockAfter 는 marker 뒤 첫 "{" 부터 짝이 맞는 "}" 까지를 돌려준다. 파일 전역 문자열
// 검사는 그 코드가 "어디에" 있는지 보지 못해, 잠금 구문을 통째로 지워도 다른 곳에 같은
// 문자열이 남아 있으면 통과한다 — 검사 범위를 해당 블록으로 좁힌다.
func blockAfter(t *testing.T, body, marker string) string {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("%q 를 찾지 못했다", marker)
	}
	j := strings.Index(body[i:], "{")
	if j < 0 {
		t.Fatalf("%q 뒤에 블록이 없다", marker)
	}
	start := i + j
	depth := 0
	for k := start; k < len(body); k++ {
		switch body[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return body[start : k+1]
			}
		}
	}
	t.Fatalf("%q 블록의 닫는 괄호를 찾지 못했다", marker)
	return ""
}

// 적용 버튼 잠금은 이 배치의 안전 경계에서 가장 중요한 한 줄이다. 잠금을 세우는 자리가
// 셋이고 여는 자리가 하나여야 하며, 셋 중 하나만 사라져도 검증하지 않은 값이 적용된다.
// 파일 전역 문자열 검사로는 셋을 통째로 지워도 잡히지 않는다.
func TestUI_적용_버튼_잠금_세_자리(t *testing.T) {
	body := string(indexHTML)

	// ① 처음부터 잠겨 있어야 한다 — 화면을 열자마자 누를 수 있으면 나머지가 무의미하다.
	if !strings.Contains(body, `<button id="wApply" disabled>`) {
		t.Error("적용 버튼이 disabled 로 시작하지 않는다")
	}
	// ② 장치·사용 방식·구현 방식을 바꾸면 다시 잠긴다(renderWizard 안에서).
	if !strings.Contains(blockAfter(t, body, "function renderWizard()"), `$("wApply").disabled = true`) {
		t.Error("선택지 재계산이 적용 버튼을 다시 잠그지 않는다")
	}
	// ③ 값 입력을 바꿔도 다시 잠긴다 — 이 칸들은 renderWizard 를 부르지 않는다.
	lockBody := blockAfter(t, body, `["wRep", "wCount", "wName", "wProf"].forEach`)
	if !strings.Contains(lockBody, `$("wApply").disabled = true`) {
		t.Error("값 입력 변경이 적용 버튼을 다시 잠그지 않는다")
	}
	if !strings.Contains(lockBody, "wz.confirmed = false") {
		t.Error("값 입력 변경이 파괴적 재확인 표시를 초기화하지 않는다")
	}
	// ④ 여는 자리는 검증 통과 한 곳뿐이다.
	if n := strings.Count(body, `$("wApply").disabled = false`); n != 1 {
		t.Errorf("적용 버튼을 여는 곳이 %d 곳이다 — 검증 통과 한 곳이어야 한다", n)
	}
	if !strings.Contains(blockAfter(t, body, "async function checkPolicy()"), `$("wApply").disabled = false`) {
		t.Error("적용 버튼을 여는 곳이 검증 함수 안이 아니다")
	}
}

// 프로파일 목록이 빈 채로 분할을 고르면 서버는 "layout 도 sharing 도 없다" 로 거절하는데,
// 그 문구로는 사용자가 무엇을 고쳐야 하는지 모른다 — 화면이 아는 사실을 먼저 말한다.
func TestUI_분할은_프로파일_없이_보내지_않는다(t *testing.T) {
	body := blockAfter(t, string(indexHTML), "function buildReq()")
	if !strings.Contains(body, "파티션 프로파일이 관측되지 않았다") {
		t.Error("프로파일이 없을 때의 안내가 없다 — 빈 layout 이 서버로 나간다")
	}
}

// 장치가 상한을 보고하지 않았는데 "최대 16" 이라고 적으면, 관측되지 않은 값을 그 장치의
// 사실로 내는 것이다. 게다가 그 16 은 timeSlicing 스키마 상한이고 mps 에는 상한이 없다.
func TestUI_복제수_상한은_보고된_값일_때만_단정(t *testing.T) {
	body := blockAfter(t, string(indexHTML), "function renderWizard()")
	if strings.Contains(body, `"최대 " + maxR;`) {
		t.Error("상한 미보고 상태를 확정 문구로 낸다")
	}
	if !strings.Contains(body, "상한 미보고") {
		t.Error("상한이 보고되지 않았을 때의 문구가 없다")
	}
	if !strings.Contains(body, "이 장치가 보고한 값") {
		t.Error("보고된 상한과 스키마 상한을 구분하지 않는다")
	}
}

// 미리보기 뒤 입력을 바꾸면 적용이 다시 잠겨야 한다 — 잠기지 않으면 복제수 2 로 검증하고
// 99 로 적용하는 길이 열린다. 값 입력 칸은 renderWizard 를 부르지 않으므로 별도 바인딩이 있어야 한다.
func TestUI_미리보기_후_입력_변경은_적용을_다시_잠근다(t *testing.T) {
	html := string(indexHTML)
	for _, id := range []string{"wRep", "wCount", "wName", "wProf"} {
		if !strings.Contains(html, `"`+id+`"`) {
			t.Errorf("입력 칸 %s 가 없다", id)
		}
	}
	if !strings.Contains(html, `["wRep", "wCount", "wName", "wProf"].forEach`) {
		t.Error("값 입력 변경이 적용 버튼을 다시 잠그지 않는다")
	}
}

// 진행 표시는 실패 단계를 감추지 않아야 한다 — 감추면 13분 기다린 뒤에야 실패를 안다.
func TestUI_진행_표시_되돌림_단계_포함(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{"비우기", "적용", "검증", "광고", "되돌림"} {
		if !strings.Contains(html, want) {
			t.Errorf("진행 표시에 %q 단계가 없다", want)
		}
	}
	if !strings.Contains(html, "stopProgress") {
		t.Error("폴링을 멈추는 경로가 없다 — 탭을 떠나도 계속 돈다")
	}
}

// 세 라우트는 서버에 있는데 화면이 안 썼다 — 잇는다.
func TestUI_미사용_라우트_활용(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{"/api/v1/status", "/api/v1/classes", "/api/v1/consumers"} {
		if !strings.Contains(html, want) {
			t.Errorf("%s 를 화면이 쓰지 않는다", want)
		}
	}
}

// 기존 OS 추종 블록을 지우면 토글을 "시스템" 으로 둔 사용자의 다크모드가 사라진다.
func TestUI_테마_토글과_OS_추종_공존(t *testing.T) {
	html := string(indexHTML)
	if !strings.Contains(html, "prefers-color-scheme: dark") {
		t.Error("OS 추종 블록이 사라졌다")
	}
	if !strings.Contains(html, `[data-theme="dark"]`) {
		t.Error("강제 어둡게 선택자가 없다")
	}
	if !strings.Contains(html, `[data-theme="light"]`) {
		t.Error("강제 밝게 선택자가 없다")
	}
	if !strings.Contains(html, "kcloud-theme") {
		t.Error("선택을 저장하지 않는다")
	}
}

// 상태 탭은 터미널(kubectl npu status)과 나란히 놓고 대조하는 화면이다 — 세 절이 같은
// 순서로 있어야 하고, 배선(nav 버튼·section·loaders 키) 중 하나만 빠져도 탭이 열리지 않는다.
// 헬퍼 정의가 아니라 호출부를 고정한다(정의만 남고 호출이 사라지면 화면엔 안 나온다).
func TestUIHasStatusTab(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`data-tab="st"`,     // nav 버튼
		`<section id="st">`, // 표시 영역
		"st: loadStatus",    // loaders 등록 = 렌더 함수 호출부
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("상태 탭 배선 %q 가 없다 — 하나만 빠져도 탭이 비어 열린다", want)
		}
	}

	body := blockAfter(t, html, "async function loadStatus()")
	// 터미널과 같은 세 절, 같은 순서. 순서가 바뀌면 나란히 대조가 깨진다.
	for i, want := range []string{"드라이버 설치 정책", "노드 장치", "클러스터 정책"} {
		j := strings.Index(body, want)
		if j < 0 {
			t.Fatalf("상태 탭에 %q 절이 없다", want)
		}
		if i > 0 && j < strings.Index(body, []string{"드라이버 설치 정책", "노드 장치"}[i-1]) {
			t.Errorf("%q 절이 앞 절보다 먼저 나온다 — 터미널 출력과 순서가 다르다", want)
		}
	}
	// 응답 필드를 실제로 읽는지. 화면에서만 되는 축 셋(바인딩·광고량·갱신 상태)이 여기 있다.
	for _, want := range []string{
		"driverInstallPolicies", "driverLoaded", "driverBinding", "vfio-pci",
		"passthroughReserved", "allocatable", "upgrades", "clusterPolicies",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("상태 탭이 %q 를 다루지 않는다", want)
		}
	}
	// product 는 구 detector 에서 비어 온다 — 지어내지 말고 vendor/model 로 폴백한다.
	if !strings.Contains(body, "d.product ||") {
		t.Error("장치 표시명이 product 빈 값을 폴백하지 않는다")
	}
	// 가속기 리소스 판별은 규칙이어야 한다. 벤더 리소스명을 박아두면 설정이 다른 클러스터의
	// 광고량이 조용히 사라진다.
	for _, banned := range []string{`"nvidia.com/gpu"`, `"furiosa.ai/rngd"`} {
		if strings.Contains(body, banned) {
			t.Errorf("가속기 리소스를 화이트리스트 %q 로 고정했다 — 벤더 리소스명은 설정에 따라 달라진다", banned)
		}
	}
	// 빈 응답에서도 세 절이 "없음" 으로 보여야 한다 — 절이 통째로 사라지면 조회 실패와 구분이 안 된다.
	if !strings.Contains(body, "없음") {
		t.Error("빈 응답일 때의 '없음' 표기가 없다")
	}
}

// 배제 배지는 operator 가 붙인 라벨을 옮긴 값이다 — 여기서 사유를 다시 판정하면 규칙이
// 두 곳으로 갈린다. 헬퍼 정의가 아니라 호출부(exclBadge(n) 를 실제로 부르는 자리)를
// 고정한다. 호출부는 둘이어야 한다 — 가속기 없는 control-plane 은 status.devices 가 비어
// 장치 표에 행이 0개이므로, 노드 단위 표(노드별 광고량)에도 배제 열이 있어야 배제 사실이
// 화면에 남는다. 장치 표에만 있으면 GPU 없는 master 에서 배지가 사라진다.
func TestUIStatusTabRendersExclusionBadge(t *testing.T) {
	body := blockAfter(t, string(indexHTML), "async function loadStatus()")
	if n := strings.Count(body, "exclBadge(n)"); n < 2 {
		t.Errorf("상태 탭의 exclBadge 호출이 %d 곳 — 장치 표와 노드별 광고량 표 둘 다여야 한다", n)
	}
	if !strings.Contains(body, `table(["노드", "가속기 리소스", "배제", "예약"], adv)`) {
		t.Error("노드별 광고량 표에 배제 열이 없다 — 장치 0개 배제 노드가 이 표에만 나온다")
	}
}

// 사유가 비면 빈 괄호(`제외됨()`)를 내지 않는다 — 사유가 있는 줄 알게 만든다. 헬퍼 본문의
// 조건 분기 자체가 이 규칙이라 여기서는 정의를 고정한다.
func TestUIExclusionBadgeOmitsEmptyReasonParens(t *testing.T) {
	body := blockAfter(t, string(indexHTML), "function exclBadge(n)")
	if !strings.Contains(body, `n.excludedReason ? "(" + esc(n.excludedReason) + ")" : ""`) {
		t.Error("배제 사유가 빌 때 괄호를 생략하는 분기가 없다")
	}
}

// dry-run 관문이 화면에 실제로 걸려 있는지 — 없으면 검증 없이 적용된다.
func TestUI_적용_전_dryRun_호출(t *testing.T) {
	html := string(indexHTML)
	if !strings.Contains(html, "dryRun=true") {
		t.Error("적용 전 dryRun 호출이 없다")
	}
	if !strings.Contains(html, "/api/v1/policies") {
		t.Error("정책 쓰기 라우트를 부르지 않는다")
	}
}

// 토큰 자동 적용(#token=...)은 두 성질을 함께 만족해야 한다: fragment 에서 읽을 것,
// 읽은 뒤 주소창에서 지울 것. 하나만 있으면 토큰이 새로고침·북마크·화면 공유로 다시 샌다.
// 셸 JS 를 여기서 실행할 수 없으니 두 호출부가 존재한다는 사실을 고정한다.
func TestUI_AcceptsTokenFromFragmentAndClearsIt(t *testing.T) {
	body := string(indexHTML)
	// 주석이 아니라 호출부를 고정한다 — 같은 낱말이 위 설명에도 있어서, 낱말만 세면
	// 코드를 지워도 통과한다.
	for _, want := range []string{"location.hash", `get("token")`, `history.replaceState(null, ""`} {
		if !strings.Contains(body, want) {
			t.Fatalf("fragment 토큰 처리 누락: %q", want)
		}
	}
	// 질의 문자열로 받으면 접근 로그·Referer 에 토큰 원문이 남는다 — 그 경로를 만들지 않는다.
	if strings.Contains(body, "location.search)") && strings.Contains(body, `searchParams.get("token")`) {
		t.Fatal("토큰을 질의 문자열에서 읽으면 서버 로그에 남는다")
	}
}
