/**
 * LogQL 쿼리 빌더 — 로그·이벤트 화면의 검색/스트림/내보내기가 공유하는 상태·유틸.
 *
 * 규칙(이노그리드 연동 규격):
 *  - 라벨 필터 선택값은 URL 파라미터가 아니라 LogQL 중괄호 {} 내부 스트림 셀렉터로 삽입된다.
 *  - 본문 검색어는 중괄호 뒤 파이프라인 필터( |= "단어" )로 붙인다(입력창에서 직접).
 *  - log_level/start/end/direction/limit 은 URL 쿼리 파라미터로 전달. '전체 레벨'은 log_level 생략.
 */

export const LOG_DEFAULT_SELECTOR = '{job="loki.source.kubernetes.pods"}';

export const LOG_PERIODS = [
  { key: "15m", label: "지난 15m", ms: 15 * 60_000 },
  { key: "1h", label: "지난 1h", ms: 60 * 60_000 },
  { key: "6h", label: "지난 6h", ms: 6 * 60 * 60_000 },
  { key: "24h", label: "지난 24h", ms: 24 * 60 * 60_000 },
  { key: "7d", label: "지난 7d", ms: 7 * 24 * 60 * 60_000 },
] as const;

export const LOG_LEVELS = ["error", "warning", "info", "debug"] as const;

/** 조회 기간 키 → ISO-8601 start/end (프론트에서 계산해 전달). */
export function periodRange(key: string): { start: string; end: string } {
  const p = LOG_PERIODS.find((x) => x.key === key) ?? LOG_PERIODS[1];
  const end = new Date();
  const start = new Date(end.getTime() - p.ms);
  return { start: start.toISOString(), end: end.toISOString() };
}

/**
 * 라벨 조건(key="value")을 LogQL 의 첫 중괄호 {} 안에 삽입/치환한다.
 * 뒤의 파이프라인( |= "..." 등)은 그대로 보존. 같은 key 는 새 값으로 교체.
 */
export function injectLabelSelector(query: string, key: string, value: string): string {
  const pair = `${key}="${value}"`;
  const m = query.match(/\{([^}]*)\}/);
  if (!m) {
    const rest = query.trim();
    return `{${pair}}${rest ? ` ${rest}` : ""}`;
  }
  const parts = m[1]
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .filter((p) => !p.startsWith(`${key}=`));
  parts.push(pair);
  return query.replace(m[0], `{${parts.join(", ")}}`);
}

export interface LogQueryState {
  query: string;
  logLevel: string; // "all" | error | warning | info | debug
  period: string; // LOG_PERIODS key
  direction: "backward" | "forward";
  limit: number;
}

/**
 * search/stream/export 공통 — MNT-OBS 로 보낼 쿼리스트링.
 * 전체 레벨(all)은 log_level 생략. withRange=false 면 start/end 생략(스트림용).
 */
export function buildLogParams(s: LogQueryState, opts?: { withRange?: boolean }): URLSearchParams {
  const p = new URLSearchParams();
  p.set("query", s.query);
  if (s.logLevel && s.logLevel !== "all") p.set("log_level", s.logLevel);
  if (opts?.withRange !== false) {
    const { start, end } = periodRange(s.period);
    p.set("start", start);
    p.set("end", end);
  }
  p.set("direction", s.direction);
  p.set("limit", String(s.limit));
  return p;
}
