// ============================================================
// matrix.go: 레지스트리 → 호환성 매트릭스 Markdown
// 상세: 정적 문서는 명세서 층(Declared)과 검증 열(Support.Assurance())만 싣는다. 검증
//       열은 이 저장소 안 근거(단위/클러스터 시험·하드웨어 evidence)를 반영하고,
//       라이브 성적표(kubectl npu capabilities)의 Verified 열과는 별개다.
// 생성일: 2026-09-19 | 수정일: 2026-10-11
// ============================================================

package vendorcap

import (
	"fmt"
	"strings"
)

// verificationLabel 은 매트릭스 검증 열의 값이다. State 가 Unknown 이면 아무것도 확인한
// 바가 없으므로 —, 그 외(Native/Software/Unsupported)는 Support.Assurance() 를 그대로
// 옮겨 적는다. Unsupported 를 Unknown 과 묶어 감추지 않는 이유: "지원 안 함" 은 근거 없는
// 추정일 수도, 하드웨어로 재현해 확인한 결론일 수도 있다(예: tenstorrent timeSlicing 은
// co-run 실측으로 확인한 Unsupported) — 이 둘은 다른 정보이고, 감추면 매트릭스만 보고는
// 구별할 수 없다. 등급 판정은 여기서 다시 하지 않는다 — Assurance() 가 유일한 판정 지점이다.
func verificationLabel(s Support) string {
	if s.State == StateUnknown {
		return "—"
	}
	return string(s.Assurance())
}

// RenderMatrix 는 결정적 Markdown 이다(Keys/Capabilities 가 정렬됨).
func RenderMatrix() string {
	var b strings.Builder
	b.WriteString("---\ntitle: \"호환성 매트릭스\"\ndescription: \"internal/vendorcap 레지스트리에서 생성. 손으로 고치지 말고 make capmatrix 로 재생성.\"\nstatus: generated\ndoc_id: operator-compatibility-matrix\n---\n\n")
	b.WriteString("# 호환성 매트릭스\n\n")
	b.WriteString("상태: Native=벤더 제공, Software=kcloud SW 구현, Unsupported=미지원(사유), Unknown=조사 필요. 검증은 Support.Assurance() 의 확인 수준(none/unit/cluster/hardware)이며, `kubectl npu capabilities` 의 라이브 Verified 열(실행 중인 노드에서 다시 확인한 값)과는 별개다. Unsupported 행의 검증 값도 같은 기준이다 — 지원 여부가 아니라 그 미지원 판정을 어디까지 확인했는지를 뜻한다(예: hardware 는 실제 장치로 재현해 확인한 미지원).\n\n")
	b.WriteString("| 벤더 | 제품 | 능력 | 상태 | 검증 | 사유/근거 |\n|---|---|---|---|---|---|\n")
	for _, k := range Keys() {
		for _, c := range Capabilities() {
			s := registry[k][c]
			// 사유와 근거가 둘 다 있으면 둘 다 싣는다("사유 (근거)"). 사유만 보이면 Unsupported
			// 행(예 furiosa/warboy timeSlicing)의 evidence 경로가 매트릭스에서 사라져, 문서만
			// 읽는 사람이 근거를 추적할 수 없다.
			note := s.Reason
			switch {
			case note == "":
				note = s.Source
			case s.Source != "":
				note += " (" + s.Source + ")"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", k.Vendor, k.Product, c, s.State, verificationLabel(s), strings.ReplaceAll(note, "|", "\\|"))
		}
	}
	return b.String()
}
