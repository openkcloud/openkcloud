"""
openKcloud CMP — 요구사항 정의서 v1.1 기반 Mockup 반영 검토 (Excel)

generate_requirements_review.py 에 정의된 데이터 배열을 그대로 가져와서
xlsx 파일로 출력. 시트별로 카테고리를 분리하고, 표지·범례·집계 시트를 함께 제공.
"""

import sys
import io
import os

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8")

# 같은 디렉터리의 .py 모듈에서 데이터 import
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from generate_requirements_review import (
    UFR_SVC, UFR_MON, UFR_CST, UFR_LLM, UFR_MNT, UFR_STD, UFR_OPN,
    UNR,
    SFR_SVC, SIR_SVC, SFR_OPT, SIR_OPT, SFR_LLM, SIR_LLM, SFR_MNT, SIR_MNT,
    SNR_PF, SNR_SA, SNR_SE, SNR_SS, SNR_SM,
)

from openpyxl import Workbook
from openpyxl.styles import Alignment, Border, Side, Font, PatternFill
from openpyxl.utils import get_column_letter
from openpyxl.worksheet.table import Table, TableStyleInfo

# ─────────────────────────────────────────────────────────────
# 공통 스타일
# ─────────────────────────────────────────────────────────────

HDR_FILL = PatternFill("solid", fgColor="1B3A8A")  # K-Cloud Indigo
HDR_FONT = Font(name="맑은 고딕", size=10, bold=True, color="FFFFFF")

CELL_FONT = Font(name="맑은 고딕", size=10)
ID_FONT = Font(name="Consolas", size=9, bold=True, color="1B3A8A")

WRAP = Alignment(wrap_text=True, vertical="top", horizontal="left")
CENTER = Alignment(wrap_text=True, vertical="center", horizontal="center")

BORDER = Border(
    left=Side(style="thin", color="C7CFE2"),
    right=Side(style="thin", color="C7CFE2"),
    top=Side(style="thin", color="C7CFE2"),
    bottom=Side(style="thin", color="C7CFE2"),
)

# 상태 셀 채움
STATUS_FILLS = {
    "완전 반영": PatternFill("solid", fgColor="DCFCE7"),  # mint-100
    "부분 반영": PatternFill("solid", fgColor="FEF3C7"),  # amber-100
    "향후 구현": PatternFill("solid", fgColor="FEE2E2"),  # rose-100
}


def detect_status(reflect: str) -> str:
    if reflect.startswith("✅"):
        return "완전 반영"
    if reflect.startswith("🟡"):
        return "부분 반영"
    if reflect.startswith("⏳"):
        return "향후 구현"
    return ""


# ─────────────────────────────────────────────────────────────
# 시트 작성 헬퍼
# ─────────────────────────────────────────────────────────────

UFR_COLS = ["Req.ID", "상세 내용", "출처", "상태", "Mockup 반영사항"]
SFR_COLS = ["Req.ID", "상세 내용", "관련 UR", "구현년도", "중요도", "담당기관", "상태", "Mockup 반영사항"]

UFR_WIDTHS = [14, 60, 28, 12, 70]
SFR_WIDTHS = [14, 50, 14, 12, 10, 18, 12, 60]


def write_section_header(ws, title: str, subtitle: str = ""):
    ws["A1"] = title
    ws["A1"].font = Font(name="맑은 고딕", size=14, bold=True, color="1B3A8A")
    if subtitle:
        ws["A2"] = subtitle
        ws["A2"].font = Font(name="맑은 고딕", size=10, color="64748B")
    ws.row_dimensions[1].height = 22


def write_table(ws, items, cols, widths, start_row: int, status_col_idx: int, reflect_col_idx: int):
    # 컬럼 폭
    for i, w in enumerate(widths, start=1):
        ws.column_dimensions[get_column_letter(i)].width = w

    # 헤더
    for ci, label in enumerate(cols, start=1):
        c = ws.cell(row=start_row, column=ci, value=label)
        c.fill = HDR_FILL
        c.font = HDR_FONT
        c.alignment = CENTER
        c.border = BORDER
    ws.row_dimensions[start_row].height = 28

    # 데이터
    for ri, item in enumerate(items, start=start_row + 1):
        if len(cols) == 5:  # UFR
            req_id, content, source, reflect = item
            status = detect_status(reflect)
            row_vals = [req_id, content, source, status, reflect]
        else:  # SFR/SIR/SNR
            req_id, content, related, impl_year, priority, owner, reflect = item
            status = detect_status(reflect)
            row_vals = [req_id, content, related, impl_year, priority, owner, status, reflect]

        for ci, val in enumerate(row_vals, start=1):
            c = ws.cell(row=ri, column=ci, value=val)
            c.border = BORDER
            c.alignment = WRAP
            c.font = CELL_FONT
            if ci == 1:
                c.font = ID_FONT
                c.alignment = Alignment(wrap_text=False, vertical="top", horizontal="left")
            if ci == status_col_idx:
                c.alignment = CENTER
                fill = STATUS_FILLS.get(status)
                if fill:
                    c.fill = fill
                c.font = Font(name="맑은 고딕", size=9, bold=True)

        # 행 높이 가변 (내용 길이 기반)
        content_len = max(len(str(content)), len(str(reflect)))
        ws.row_dimensions[ri].height = max(34, min(120, 18 + content_len // 4))

    # 첫 행을 고정
    ws.freeze_panes = ws.cell(row=start_row + 1, column=1)


def add_ufr_sheet(wb: Workbook, title: str, subtitle: str, items):
    ws = wb.create_sheet(title=title[:31])  # sheet name max 31 chars
    write_section_header(ws, title, subtitle)
    write_table(ws, items, UFR_COLS, UFR_WIDTHS, start_row=4, status_col_idx=4, reflect_col_idx=5)
    return ws


def add_sfr_sheet(wb: Workbook, title: str, subtitle: str, items):
    ws = wb.create_sheet(title=title[:31])
    write_section_header(ws, title, subtitle)
    write_table(ws, items, SFR_COLS, SFR_WIDTHS, start_row=4, status_col_idx=7, reflect_col_idx=8)
    return ws


# ─────────────────────────────────────────────────────────────
# 표지 & 집계
# ─────────────────────────────────────────────────────────────

def add_cover(wb: Workbook, sections):
    ws = wb.create_sheet(title="표지·범례·집계", index=0)
    for col, w in enumerate([28, 14, 14, 14, 14], start=1):
        ws.column_dimensions[get_column_letter(col)].width = w

    ws["A1"] = "AI반도체 클라우드 서비스 플랫폼"
    ws["A1"].font = Font(name="맑은 고딕", size=18, bold=True, color="1B3A8A")
    ws["A2"] = "요구사항 정의서 v1.1 — Mockup 반영 검토"
    ws["A2"].font = Font(name="맑은 고딕", size=13, bold=True, color="475569")
    ws["A3"] = "기준: 요구사항 정의서 v1.1 (2026.03.20)  ·  작성: 오케스트로(주) · openKcloud CMP Mockup v1.0  ·  2026.06.04"
    ws["A3"].font = Font(name="맑은 고딕", size=10, color="64748B")

    ws["A5"] = "범례"
    ws["A5"].font = Font(name="맑은 고딕", size=12, bold=True)

    legends = [
        ("완전 반영", "mockup 페이지/기능에서 시각화 또는 인터랙션 시연 가능", "DCFCE7"),
        ("부분 반영", "UI 자리 마련, 실 알고리즘은 다른 기관 영역이거나 향후 구현",  "FEF3C7"),
        ("향후 구현", "명세상 자리는 있으나 구체 UI 미반영 (차차년도 구현 예정)",     "FEE2E2"),
    ]
    for i, (label, desc, color) in enumerate(legends, start=6):
        c = ws.cell(row=i, column=1, value=label)
        c.fill = PatternFill("solid", fgColor=color)
        c.font = Font(name="맑은 고딕", size=10, bold=True)
        c.alignment = CENTER
        c.border = BORDER
        d = ws.cell(row=i, column=2, value=desc)
        d.alignment = WRAP
        d.font = CELL_FONT
        ws.merge_cells(start_row=i, start_column=2, end_row=i, end_column=5)
        d.border = BORDER
        ws.row_dimensions[i].height = 22

    # 집계 표
    ws["A10"] = "카테고리별 반영 현황"
    ws["A10"].font = Font(name="맑은 고딕", size=12, bold=True)

    headers = ["카테고리", "총 건수", "완전 반영 ✅", "부분 반영 🟡", "향후 구현 ⏳"]
    for ci, label in enumerate(headers, start=1):
        c = ws.cell(row=11, column=ci, value=label)
        c.fill = HDR_FILL
        c.font = HDR_FONT
        c.alignment = CENTER
        c.border = BORDER
    ws.row_dimensions[11].height = 26

    total = {"total": 0, "ok": 0, "partial": 0, "future": 0}
    row = 12
    for name, items in sections:
        ok = sum(1 for it in items if it[-1].startswith("✅"))
        pa = sum(1 for it in items if it[-1].startswith("🟡"))
        fu = sum(1 for it in items if it[-1].startswith("⏳"))
        total["total"] += len(items)
        total["ok"] += ok
        total["partial"] += pa
        total["future"] += fu

        ws.cell(row=row, column=1, value=name).font = CELL_FONT
        ws.cell(row=row, column=2, value=len(items)).font = CELL_FONT
        ws.cell(row=row, column=3, value=ok).font = CELL_FONT
        ws.cell(row=row, column=4, value=pa).font = CELL_FONT
        ws.cell(row=row, column=5, value=fu).font = CELL_FONT
        for ci in range(1, 6):
            cell = ws.cell(row=row, column=ci)
            cell.border = BORDER
            cell.alignment = CENTER if ci > 1 else Alignment(wrap_text=True, vertical="center", horizontal="left")
        row += 1

    # 합계 행
    sum_row = row
    ws.cell(row=sum_row, column=1, value="합계").font = Font(name="맑은 고딕", size=10, bold=True)
    ws.cell(row=sum_row, column=2, value=total["total"]).font = Font(name="맑은 고딕", size=10, bold=True)
    ws.cell(row=sum_row, column=3, value=total["ok"]).font = Font(name="맑은 고딕", size=10, bold=True, color="047857")
    ws.cell(row=sum_row, column=4, value=total["partial"]).font = Font(name="맑은 고딕", size=10, bold=True, color="B45309")
    ws.cell(row=sum_row, column=5, value=total["future"]).font = Font(name="맑은 고딕", size=10, bold=True, color="BE123C")
    for ci in range(1, 6):
        cell = ws.cell(row=sum_row, column=ci)
        cell.fill = PatternFill("solid", fgColor="F1F5F9")
        cell.border = BORDER
        cell.alignment = CENTER if ci > 1 else Alignment(wrap_text=True, vertical="center", horizontal="left")

    # 반영률
    if total["total"]:
        rate = total["ok"] / total["total"] * 100
        ws.cell(row=sum_row + 2, column=1, value=f"전체 반영률 (완전 + 부분): {(total['ok'] + total['partial']) / total['total'] * 100:.1f}%").font = Font(name="맑은 고딕", size=11, bold=True, color="1B3A8A")
        ws.cell(row=sum_row + 3, column=1, value=f"완전 반영률: {rate:.1f}%").font = Font(name="맑은 고딕", size=10, color="047857")


# ─────────────────────────────────────────────────────────────
# 전체 통합 시트
# ─────────────────────────────────────────────────────────────

def add_all_in_one(wb: Workbook, ufr_sections, sfr_sections):
    ws = wb.create_sheet(title="전체 요구사항", index=1)
    cols = ["카테고리", "Req.ID", "상세 내용", "관련 UR", "구현년도", "중요도", "담당기관", "출처", "상태", "Mockup 반영사항"]
    widths = [22, 14, 50, 14, 10, 8, 18, 22, 10, 56]

    for i, w in enumerate(widths, start=1):
        ws.column_dimensions[get_column_letter(i)].width = w

    ws["A1"] = "전체 요구사항 — 통합 뷰 (필터·검색용)"
    ws["A1"].font = Font(name="맑은 고딕", size=14, bold=True, color="1B3A8A")

    # 헤더 (row 3)
    for ci, label in enumerate(cols, start=1):
        c = ws.cell(row=3, column=ci, value=label)
        c.fill = HDR_FILL
        c.font = HDR_FONT
        c.alignment = CENTER
        c.border = BORDER
    ws.row_dimensions[3].height = 28

    row = 4
    # UFR sections (4-tuple)
    for category, items in ufr_sections:
        for req_id, content, source, reflect in items:
            status = detect_status(reflect)
            vals = [category, req_id, content, "", "", "", "", source, status, reflect]
            _write_row(ws, row, vals, status, status_col_idx=9)
            row += 1
    # SFR sections (7-tuple)
    for category, items in sfr_sections:
        for req_id, content, related, impl_year, priority, owner, reflect in items:
            status = detect_status(reflect)
            vals = [category, req_id, content, related, impl_year, priority, owner, "", status, reflect]
            _write_row(ws, row, vals, status, status_col_idx=9)
            row += 1

    # 필터 자동 적용
    ws.auto_filter.ref = f"A3:{get_column_letter(len(cols))}{row - 1}"
    ws.freeze_panes = "A4"


def _write_row(ws, row, vals, status, status_col_idx):
    for ci, val in enumerate(vals, start=1):
        c = ws.cell(row=row, column=ci, value=val)
        c.border = BORDER
        c.alignment = WRAP
        c.font = CELL_FONT
        if ci == 2:
            c.font = ID_FONT
            c.alignment = Alignment(wrap_text=False, vertical="top", horizontal="left")
        if ci == status_col_idx:
            c.alignment = CENTER
            fill = STATUS_FILLS.get(status)
            if fill:
                c.fill = fill
            c.font = Font(name="맑은 고딕", size=9, bold=True)
    # 행 높이
    content = str(vals[2]) if len(vals) > 2 else ""
    reflect = str(vals[-1])
    ws.row_dimensions[row].height = max(30, min(110, 16 + max(len(content), len(reflect)) // 5))


# ─────────────────────────────────────────────────────────────
# main
# ─────────────────────────────────────────────────────────────

UFR_SECTIONS = [
    ("UFR.SVC · AI반도체 클라우드 서비스 제공", UFR_SVC),
    ("UFR.MON · 모니터링·트레이싱", UFR_MON),
    ("UFR.CST · 운용 비용 최적화", UFR_CST),
    ("UFR.LLM · 초거대 AI 클라우드 서비스", UFR_LLM),
    ("UFR.MNT · 통합 관리·개방형 API (오케스트로)", UFR_MNT),
    ("UFR.STD · 표준", UFR_STD),
    ("UFR.OPN · 공개 SW", UFR_OPN),
    ("UNR · 사용자 비기능 요구사항", UNR),
]

SFR_SECTIONS = [
    ("SFR.SVC · AI반도체 클라우드 서비스 프레임워크", SFR_SVC),
    ("SIR.SVC · 인터페이스", SIR_SVC),
    ("SFR.OPT · 최적화 프레임워크", SFR_OPT),
    ("SIR.OPT · 인터페이스", SIR_OPT),
    ("SFR.LLM · 초거대 AI 서비스 프레임워크", SFR_LLM),
    ("SIR.LLM · 인터페이스", SIR_LLM),
    ("SFR.MNT · 통합 관리 프레임워크 (오케스트로)", SFR_MNT),
    ("SIR.MNT · 인터페이스", SIR_MNT),
    ("SNR.PF · 성능", SNR_PF),
    ("SNR.SA · 가용성", SNR_SA),
    ("SNR.SE · 확장성", SNR_SE),
    ("SNR.SS · 보안", SNR_SS),
    ("SNR.SM · 유지보수", SNR_SM),
]


def main():
    wb = Workbook()
    # 기본 시트 제거
    wb.remove(wb.active)

    # 1) 표지·범례·집계
    add_cover(wb, UFR_SECTIONS + SFR_SECTIONS)

    # 2) 통합 뷰
    add_all_in_one(wb, UFR_SECTIONS, SFR_SECTIONS)

    # 3) UFR 카테고리별 시트
    short_titles_ufr = {
        "UFR.SVC · AI반도체 클라우드 서비스 제공": "UFR.SVC",
        "UFR.MON · 모니터링·트레이싱": "UFR.MON",
        "UFR.CST · 운용 비용 최적화": "UFR.CST",
        "UFR.LLM · 초거대 AI 클라우드 서비스": "UFR.LLM",
        "UFR.MNT · 통합 관리·개방형 API (오케스트로)": "UFR.MNT ⭐",
        "UFR.STD · 표준": "UFR.STD",
        "UFR.OPN · 공개 SW": "UFR.OPN",
        "UNR · 사용자 비기능 요구사항": "UNR",
    }
    for name, items in UFR_SECTIONS:
        add_ufr_sheet(wb, short_titles_ufr.get(name, name), name, items)

    # 4) SFR/SIR/SNR 카테고리별 시트
    short_titles_sfr = {
        "SFR.SVC · AI반도체 클라우드 서비스 프레임워크": "SFR.SVC",
        "SIR.SVC · 인터페이스": "SIR.SVC",
        "SFR.OPT · 최적화 프레임워크": "SFR.OPT",
        "SIR.OPT · 인터페이스": "SIR.OPT",
        "SFR.LLM · 초거대 AI 서비스 프레임워크": "SFR.LLM",
        "SIR.LLM · 인터페이스": "SIR.LLM",
        "SFR.MNT · 통합 관리 프레임워크 (오케스트로)": "SFR.MNT ⭐",
        "SIR.MNT · 인터페이스": "SIR.MNT",
        "SNR.PF · 성능": "SNR.PF",
        "SNR.SA · 가용성": "SNR.SA",
        "SNR.SE · 확장성": "SNR.SE",
        "SNR.SS · 보안": "SNR.SS",
        "SNR.SM · 유지보수": "SNR.SM",
    }
    for name, items in SFR_SECTIONS:
        add_sfr_sheet(wb, short_titles_sfr.get(name, name), name, items)

    # 저장
    out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "요구사항_정의서_mockup_반영_검토서.xlsx")
    wb.save(out_path)
    size_kb = os.path.getsize(out_path) / 1024
    print(f"[OK] saved: {out_path}  ({size_kb:.1f} KB, {len(wb.sheetnames)} sheets)")
    for s in wb.sheetnames:
        print(f"  - {s}")


if __name__ == "__main__":
    main()
