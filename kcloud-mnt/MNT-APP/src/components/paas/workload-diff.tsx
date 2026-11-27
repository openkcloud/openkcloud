"use client";
/**
 * 워크로드 적용 전 before/after 비교 뷰 — 라인 단위 diff(통합 형태).
 * before(클러스터 현재 상태)와 after(dryRun 적용 결과)를 받아 추가(초록)/삭제(빨강)/유지(회색)로 표시한다.
 * 외부 diff 라이브러리 없이 LCS 로 직접 계산한다(YAML 검토엔 통합 뷰로 충분).
 */
import * as React from "react";

type Line = { type: "same" | "add" | "del"; text: string };

/** LCS 기반 라인 diff — a(before)→b(after) 로 가는 추가/삭제/유지 줄 시퀀스 */
function diffLines(a: string[], b: string[]): Line[] {
  const n = a.length, m = b.length;
  // lcs[i][j] = a[i:] 와 b[j:] 의 최장 공통 부분수열 길이
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: Line[] = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) { out.push({ type: "same", text: a[i] }); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) { out.push({ type: "del", text: a[i] }); i++; }
    else { out.push({ type: "add", text: b[j] }); j++; }
  }
  while (i < n) out.push({ type: "del", text: a[i++] });
  while (j < m) out.push({ type: "add", text: b[j++] });
  return out;
}

const ROW: Record<Line["type"], string> = {
  same: "text-muted-foreground",
  add: "bg-emerald-50 text-emerald-800",
  del: "bg-rose-50 text-rose-800",
};
const SIGN: Record<Line["type"], string> = { same: " ", add: "+", del: "-" };

export function WorkloadDiff({ before, after, action }: { before: string; after: string; action: "create" | "update" }) {
  const lines = React.useMemo(
    () => diffLines(before ? before.split("\n") : [], after ? after.split("\n") : []),
    [before, after],
  );
  const added = lines.filter((l) => l.type === "add").length;
  const removed = lines.filter((l) => l.type === "del").length;

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        {action === "create" ? (
          <span className="rounded bg-emerald-100 px-1.5 py-0.5 font-medium text-emerald-700">신규 생성 — 비교 대상 없음</span>
        ) : (
          <span className="rounded bg-slate-100 px-1.5 py-0.5 font-medium text-slate-600">기존 리소스 변경</span>
        )}
        <span className="text-emerald-700">+{added}</span>
        <span className="text-rose-700">−{removed}</span>
      </div>
      <pre className="max-h-[52vh] overflow-auto rounded-md border bg-slate-50/50 p-2 font-mono text-xs leading-relaxed">
        {lines.map((l, idx) => (
          <div key={idx} className={`whitespace-pre-wrap px-1 ${ROW[l.type]}`}>
            <span className="select-none pr-2 opacity-60">{SIGN[l.type]}</span>{l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}
