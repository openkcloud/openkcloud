"use client";
import * as React from "react";
import { useQueryClient, useIsFetching, type QueryKey } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { usePollingInterval, POLL_OPTIONS } from "@/lib/polling";

/** 자동 폴링 주기 선택 + 수동 새로고침 + 로딩 표시 — IaaS 목록 화면 공용. */
export function RefreshControls({ queryKeys }: { queryKeys: QueryKey[] }) {
  const qc = useQueryClient();
  const intervalMs = usePollingInterval((s) => s.intervalMs);
  const setIntervalMs = usePollingInterval((s) => s.setIntervalMs);

  // 이 화면의 쿼리 중 하나라도 fetch 중이면 로딩 표시 (초기·폴링·수동·재방문 전부 커버)
  const fetching = useIsFetching({
    predicate: (q) =>
      queryKeys.some((k) => {
        const key = k as readonly unknown[];
        return key.every((seg, i) => q.queryKey[i] === seg);
      }),
  });
  const busy = fetching > 0;

  function refresh() {
    queryKeys.forEach((k) => qc.invalidateQueries({ queryKey: k }));
  }

  return (
    <div className="flex items-center gap-1.5">
      {busy && (
        <span className="flex items-center gap-1 text-[11px] text-muted-foreground">
          <RefreshCw className="size-3 animate-spin" /> 불러오는 중…
        </span>
      )}
      <Select value={String(intervalMs)} onValueChange={(v) => setIntervalMs(Number(v))}>
        <SelectTrigger className="h-8 w-[132px] text-xs" title="자동 새로고침 주기">
          <RefreshCw className="size-3.5 text-muted-foreground" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {POLL_OPTIONS.map((o) => (
            <SelectItem key={o.value} value={String(o.value)}>{o.label}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button variant="outline" size="icon-sm" title="새로고침" onClick={refresh}>
        <RefreshCw className={busy ? "size-4 animate-spin" : "size-4"} />
      </Button>
    </div>
  );
}
