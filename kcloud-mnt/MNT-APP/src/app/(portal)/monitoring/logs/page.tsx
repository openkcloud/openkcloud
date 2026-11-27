"use client";
import * as React from "react";
import { toast } from "sonner";
import { Search, AlertCircle, Download, Radio, Tags, Loader2 } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  LOG_DEFAULT_SELECTOR, LOG_PERIODS, LOG_LEVELS, injectLabelSelector, buildLogParams, type LogQueryState,
} from "@/lib/logql";
import {
  useLogSearch, useLogLabels, useLogLabelValues, useLogVolume,
  logStreamUrl, logExportUrl, type LogRow,
} from "@/lib/obs-api";

const INITIAL: LogQueryState = {
  query: LOG_DEFAULT_SELECTOR,
  logLevel: "all",
  period: "1h",
  direction: "backward",
  limit: 100,
};

export default function LogsPage() {
  const [query, setQuery] = React.useState(INITIAL.query);
  const [logLevel, setLogLevel] = React.useState(INITIAL.logLevel);
  const [period, setPeriod] = React.useState(INITIAL.period);
  const [direction, setDirection] = React.useState<"backward" | "forward">(INITIAL.direction);
  const [limit, setLimit] = React.useState(INITIAL.limit);
  const [labelKey, setLabelKey] = React.useState("cluster");
  // 검색은 '검색' 시점 스냅샷으로만 실행(매 렌더 start/end 재계산 방지)
  const [submitted, setSubmitted] = React.useState<string | null>(() => buildLogParams(INITIAL).toString());
  const [streamUrl, setStreamUrl] = React.useState<string | null>(null);
  const [liveRows, setLiveRows] = React.useState<LogRow[]>([]);

  const draft = (): LogQueryState => ({ query, logLevel, period, direction, limit });

  const search = useLogSearch(streamUrl ? null : submitted);
  const labels = useLogLabels();
  const labelValues = useLogLabelValues(labelKey);
  const volume = useLogVolume();

  const live = !!streamUrl;
  const rows = live ? liveRows : (search.data?.logs ?? []);

  // SSE 스트림 수신 — streamUrl 이 생기면 열고, 사라지면 닫는다.
  React.useEffect(() => {
    if (!streamUrl) return;
    const es = new EventSource(streamUrl);
    const onLog = (e: MessageEvent) => {
      try {
        const row = JSON.parse(e.data) as LogRow;
        setLiveRows((prev) => [row, ...prev].slice(0, 300));
      } catch { /* 파싱 불가 라인 무시 */ }
    };
    es.addEventListener("log", onLog as EventListener);
    es.onerror = () => { es.close(); setStreamUrl(null); toast.error("실시간 스트림 연결이 끊겼습니다."); };
    return () => es.close();
  }, [streamUrl]);

  const onSearch = () => {
    if (live) setStreamUrl(null);
    setSubmitted(buildLogParams(draft()).toString());
  };
  const onApplyLabel = (value: string) => {
    if (value === "__all__") return;
    setQuery((q) => injectLabelSelector(q, labelKey, value));
  };
  const onToggleLive = () => {
    if (live) { setStreamUrl(null); return; }
    setLiveRows([]);
    setStreamUrl(logStreamUrl(buildLogParams(draft(), { withRange: false })));
  };
  const onExport = (format: "csv" | "json") => {
    const a = document.createElement("a");
    a.href = logExportUrl(format, buildLogParams(draft()));
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    a.remove();
    toast.success(`${format.toUpperCase()} 내보내기`);
  };

  const volRows = volume.data?.volume ?? [];
  const maxVol = Math.max(1, ...volRows.map((v) => Number(v.volume) || 0));

  return (
    <div className="space-y-4">
      {/* 검색 조건 */}
      <Card className="p-4">
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-3">
            <div className="relative grow">
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") onSearch(); }}
                placeholder='{job="loki.source.kubernetes.pods"} |= "RabbitMQ"'
                className="w-full min-w-[320px] pl-9 font-mono text-xs"
              />
            </div>
            <Select value={logLevel} onValueChange={setLogLevel}>
              <SelectTrigger className="w-[130px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">전체 레벨</SelectItem>
                {LOG_LEVELS.map((l) => <SelectItem key={l} value={l}>{l}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select value={period} onValueChange={setPeriod}>
              <SelectTrigger className="w-[120px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                {LOG_PERIODS.map((p) => <SelectItem key={p.key} value={p.key}>{p.label}</SelectItem>)}
              </SelectContent>
            </Select>
            <Select value={direction} onValueChange={(v) => setDirection(v as "backward" | "forward")}>
              <SelectTrigger className="w-[130px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="backward">최신부터</SelectItem>
                <SelectItem value="forward">오래된 것부터</SelectItem>
              </SelectContent>
            </Select>
            <Select value={String(limit)} onValueChange={(v) => setLimit(Number(v))}>
              <SelectTrigger className="w-[110px]"><SelectValue /></SelectTrigger>
              <SelectContent>
                {[50, 100, 500, 1000].map((n) => <SelectItem key={n} value={String(n)}>{n}행</SelectItem>)}
              </SelectContent>
            </Select>
            <Button size="sm" onClick={onSearch} disabled={search.isFetching && !live}>
              {search.isFetching && !live ? <Loader2 className="size-3.5 animate-spin" /> : <Search className="size-3.5" />} 검색
            </Button>
            <Badge variant="ghost" className="font-mono text-[10px]">/logs/search</Badge>
          </div>

          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-3">
            <Tags className="size-3.5 text-muted-foreground" />
            <span className="text-[11px] text-muted-foreground">라벨 필터</span>
            <Select value={labelKey} onValueChange={setLabelKey}>
              <SelectTrigger className="w-[150px] font-mono text-xs"><SelectValue /></SelectTrigger>
              <SelectContent>
                {(labels.data?.labels ?? []).map((k) => (
                  <SelectItem key={k} value={k} className="font-mono text-xs">{k}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value="__all__" onValueChange={onApplyLabel}>
              <SelectTrigger className="w-[220px] font-mono text-xs">
                <SelectValue placeholder={labelValues.isFetching ? "불러오는 중…" : "값 선택 → 쿼리에 추가"} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__all__" className="text-xs text-muted-foreground">값 선택…</SelectItem>
                {(labelValues.data?.values ?? []).map((v) => (
                  <SelectItem key={v} value={v} className="font-mono text-xs">{v}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant={live ? "default" : "outline"} size="sm" onClick={onToggleLive}>
              <Radio className={cn("size-3.5", live && "animate-pulse")} /> {live ? "실시간 중" : "실시간"}
            </Button>
            <Button variant="outline" size="sm" onClick={() => onExport("csv")}>
              <Download className="size-3.5" /> CSV
            </Button>
            <Button variant="outline" size="sm" onClick={() => onExport("json")}>
              <Download className="size-3.5" /> JSON
            </Button>
            <span className="ml-auto font-mono text-[11px] text-muted-foreground">
              {live ? `LIVE · ${rows.length}행` : `${rows.length}행`}
            </span>
          </div>
        </div>
      </Card>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
        {/* 검색 결과 */}
        <Card className="overflow-hidden bg-slate-950 text-slate-100">
          <div className="divide-y divide-slate-800">
            {search.isError && !live && (
              <div className="flex items-center gap-2 px-4 py-6 text-[11px] text-rose-300">
                <AlertCircle className="size-4" /> 로그 조회 실패 — MNT-OBS 응답 오류.
              </div>
            )}
            {!search.isError && rows.map((log, i) => (
              <div key={i} className="px-4 py-2 font-mono text-[11px] hover:bg-slate-900">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="tabular-nums text-slate-500">{log.timestamp.slice(11, 23)}</span>
                  <LevelBadge level={log.log_level} />
                  {Object.entries(log.labels).slice(0, 4).map(([k, v]) => (
                    <span key={k} className="rounded bg-slate-800 px-1.5 py-0 text-[10px] text-brand-mint-400">{k}={v}</span>
                  ))}
                  {Object.entries(log.detected_fields ?? {}).map(([k, v]) => (
                    <span key={k} className="rounded bg-amber-500/20 px-1.5 py-0 text-[10px] font-semibold text-amber-300">{k} {v}</span>
                  ))}
                </div>
                <div className="mt-1 whitespace-pre-wrap text-slate-200">{log.message}</div>
              </div>
            ))}
            {!search.isError && rows.length === 0 && (
              <div className="flex items-center gap-2 px-4 py-6 text-[11px] text-slate-400">
                {search.isFetching
                  ? <><Loader2 className="size-4 animate-spin" /> 조회 중…</>
                  : <><AlertCircle className="size-4" /> 조건에 맞는 로그가 없습니다.</>}
              </div>
            )}
          </div>
        </Card>

        {/* 로그 볼륨 통계 */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center justify-between text-base">
              로그 볼륨
              <Badge variant="ghost" className="font-mono text-[10px]">/logs/volume</Badge>
            </CardTitle>
            <CardDescription>라벨 조합별로 쌓인 양(bytes).</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2.5">
            {volume.isLoading && <div className="text-[11px] text-muted-foreground">불러오는 중…</div>}
            {volRows.map((v, i) => {
              const label = Object.entries(v.labels).map(([k, val]) => `${k}="${val}"`).join(", ");
              const bytes = Number(v.volume) || 0;
              return (
                <div key={i} className="space-y-1">
                  <div className="flex items-center justify-between gap-2 font-mono text-[10px]">
                    <span className="truncate text-muted-foreground">{label}</span>
                    <span className="shrink-0 tabular-nums">{(bytes / 1_000_000).toFixed(1)} MB</span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
                    <div className="h-full rounded-full bg-brand-indigo-500" style={{ width: `${(bytes / maxVol) * 100}%` }} />
                  </div>
                </div>
              );
            })}
            {!volume.isLoading && volRows.length === 0 && (
              <div className="text-[11px] text-muted-foreground">볼륨 데이터 없음.</div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function LevelBadge({ level }: { level: string }) {
  const map: Record<string, string> = {
    error: "bg-rose-500/20 text-rose-300",
    warning: "bg-amber-500/20 text-amber-300",
    info: "bg-sky-500/20 text-sky-300",
    debug: "bg-slate-700 text-slate-400",
  };
  return (
    <span className={cn("rounded px-1.5 py-0 text-center text-[9px] font-semibold uppercase tracking-wider", map[level] ?? "bg-slate-700 text-slate-400")}>
      {level}
    </span>
  );
}
