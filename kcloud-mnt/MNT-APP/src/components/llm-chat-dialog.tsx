"use client";
import * as React from "react";
import { Send, Bot, User, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { ainbFetch } from "@/lib/ainb-api";

interface Msg {
  role: "user" | "assistant";
  text: string;
  latencyMs?: number;
  tokens?: number;
}

const SAMPLES = [
  "openKcloud가 뭔지 한 문장으로 설명해줘",
  "GPU와 NPU의 차이를 쉽게 알려줘",
  "클라우드에서 AI를 학습시키는 장점 3가지",
];

/** 클러스터에 배포된 LLM과 직접 대화 — 응답 시간·토큰 수 함께 표시 */
export function LlmChatDialog({
  open, onOpenChange, model, node, service,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  model?: string;
  node?: string;
  /** 대화할 서비스 이름 — 없으면 기본 LLM에 연결한다 (LLM이 여러 개일 수 있다) */
  service?: string;
}) {
  const [msgs, setMsgs] = React.useState<Msg[]>([]);
  const [input, setInput] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const endRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [msgs, busy]);

  // 다른 서비스의 채팅창을 열면 이전 서비스의 대화가 그대로 보이던 문제 —
  // 대화 상대(service)가 바뀌면 기록을 비운다
  React.useEffect(() => {
    setMsgs([]);
    setInput("");
  }, [service]);

  async function send(text: string) {
    const q = text.trim();
    if (!q || busy) return;
    setMsgs((m) => [...m, { role: "user", text: q }]);
    setInput("");
    setBusy(true);
    try {
      const path = service
        ? `/metrics/llm/chat?service=${encodeURIComponent(service)}`
        : `/metrics/llm/chat`;
      const res = await ainbFetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt: q, maxTokens: 200 }),
      });
      if (!res.ok) throw new Error(`${res.status}`);
      const d = await res.json();
      setMsgs((m) => [...m, {
        role: "assistant",
        text: d.content,
        latencyMs: Math.round(d.latencyMs),
        tokens: d.usage?.completion_tokens,
      }]);
    } catch (e) {
      setMsgs((m) => [...m, {
        role: "assistant",
        text: `응답을 받지 못했습니다 (${(e as Error).message}). 모델이 준비 중일 수 있습니다.`,
      }]);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Bot className="size-4 text-brand-mint-600" /> 배포된 AI와 대화하기
          </DialogTitle>
          <DialogDescription>
            {model ?? "LLM"} · {node ?? "클러스터 GPU"} 에서 실행 중 — 질문을 보내면 실제 추론이 수행되고
            응답 시간이 함께 표시됩니다
          </DialogDescription>
        </DialogHeader>

        <div className="h-80 space-y-3 overflow-y-auto rounded-lg border border-border bg-secondary/20 p-4">
          {msgs.length === 0 && (
            <div className="space-y-3 py-6 text-center">
              <p className="text-xs text-muted-foreground">아래 예시를 누르거나 직접 질문해 보세요</p>
              <div className="flex flex-wrap justify-center gap-2">
                {SAMPLES.map((s) => (
                  <button key={s} onClick={() => send(s)}
                    className="rounded-full border border-border bg-card px-3 py-1.5 text-[11px] hover:border-primary/50">
                    {s}
                  </button>
                ))}
              </div>
            </div>
          )}
          {msgs.map((m, i) => (
            <div key={i} className={cn("flex gap-2", m.role === "user" && "flex-row-reverse")}>
              <div className={cn(
                "flex size-7 shrink-0 items-center justify-center rounded-full",
                m.role === "user" ? "bg-brand-indigo-100 text-brand-indigo-700"
                                  : "bg-brand-mint-100 text-brand-mint-700",
              )}>
                {m.role === "user" ? <User className="size-3.5" /> : <Bot className="size-3.5" />}
              </div>
              <div className={cn("max-w-[78%] space-y-1", m.role === "user" && "text-right")}>
                <div className={cn(
                  "whitespace-pre-wrap rounded-lg px-3 py-2 text-sm",
                  m.role === "user" ? "bg-brand-indigo-50" : "bg-card border border-border",
                )}>
                  {m.text}
                </div>
                {m.latencyMs != null && (
                  <div className="font-mono text-[10px] text-muted-foreground">
                    응답 {m.latencyMs}ms{m.tokens ? ` · ${m.tokens} 토큰` : ""}
                  </div>
                )}
              </div>
            </div>
          ))}
          {busy && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" /> AI가 생성 중…
            </div>
          )}
          <div ref={endRef} />
        </div>

        <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); send(input); }}>
          <Input
            placeholder="질문을 입력하세요 (예: 클라우드가 뭐야?)"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            disabled={busy}
          />
          <Button type="submit" disabled={busy || !input.trim()}>
            <Send className="size-3.5" /> 보내기
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}
