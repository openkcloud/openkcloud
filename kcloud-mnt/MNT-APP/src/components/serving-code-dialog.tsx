"use client";
import * as React from "react";
import { toast } from "sonner";
import { Copy, Check } from "lucide-react";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import type { KFInferenceService } from "@/lib/types";

/** 배포된 서비스를 실제로 호출해보는 예제 코드 */
export function ServingCodeDialog({
  service, onClose,
}: {
  service: KFInferenceService | null;
  onClose: () => void;
}) {
  const [copied, setCopied] = React.useState<string | null>(null);
  if (!service) return null;

  const isLlm = service.modelName === "vllm";
  // LLM은 예측 API가 아니라 대화 API를 쓴다 (MNT-AINB 게이트웨이 경로 /api/ainb)
  const base = service.endpoint.replace(/\/api\/[^/]+\/serving\/.*$/, "");
  const url = isLlm
    ? `${base}/api/ainb/metrics/llm/chat?service=${service.name}`
    : service.endpoint;

  const samples: { key: string; label: string; hint: string; code: string }[] =
    isLlm
      ? [
          {
            key: "py",
            label: "파이썬",
            hint: "노트북에서 그대로 실행할 수 있습니다",
            code: `import requests

URL = "${url}"

def ask(question):
    r = requests.post(URL, json={"prompt": question, "maxTokens": 200},
                      timeout=120)
    r.raise_for_status()
    d = r.json()
    print(d["content"])
    print(f"({d['latencyMs']:.0f}ms, {d['usage']['completion_tokens']}토큰)")

ask("DataMatrix 코드가 뭔지 한 문장으로 설명해줘")`,
          },
          {
            key: "curl",
            label: "터미널 (curl)",
            hint: "설치 없이 바로 확인할 때",
            code: `curl -X POST "${url}" \\
  -H "Content-Type: application/json" \\
  -d '{"prompt":"안녕하세요","maxTokens":100}'`,
          },
        ]
      : [
          {
            key: "py",
            label: "파이썬",
            hint: "복사해서 노트북에 붙여넣으면 그대로 실행됩니다",
            code: `import base64, requests

URL = "${url}"

# 판정할 사진 한 장 (파일명을 바꿔 쓰세요)
IMAGE = "qr_crops/098010S8R7P9KV7_260331_20260317-001.jpg"

with open(IMAGE, "rb") as f:
    b64 = base64.b64encode(f.read()).decode()

r = requests.post(URL, json={"instances": [{"image_b64": b64}]}, timeout=60)
p = r.json()["predictions"][0]

print(f"파일: {IMAGE.split('/')[-1]}")
print(f"판정: {p['prediction']}  (확신도 {p['confidence']}%)")
print(f"  읽을 수 있음(OK)  {p['probabilities']['OK']}%")
print(f"  읽기 어려움(FAIL) {p['probabilities']['FAIL']}%")`,
          },
          {
            key: "batch",
            label: "여러 장 한 번에",
            hint: "현장에서는 보통 묶어서 보냅니다",
            code: `import base64, glob, requests

URL = "${url}"

def to_b64(path):
    with open(path, "rb") as f:
        return {"image_b64": base64.b64encode(f.read()).decode()}

files = sorted(glob.glob("qr_crops_failed/*.jpg"))[:20]   # 폴더는 바꿔 쓰세요
r = requests.post(URL, json={"instances": [to_b64(f) for f in files]},
                  timeout=120)
r.raise_for_status()

for path, p in zip(files, r.json()["predictions"]):
    mark = "불량 의심" if p["prediction"] == "FAIL" else "정상"
    print(f"{path:40} {mark} ({p['confidence']}%)")`,
          },
          {
            key: "curl",
            label: "터미널 (curl)",
            hint: "설치 없이 바로 확인할 때",
            code: `IMG=$(base64 -w0 qr_crops_failed/$(ls qr_crops_failed | head -1))
curl -X POST "${url}" \\
  -H "Content-Type: application/json" \\
  -d "{\\"instances\\":[{\\"image_b64\\":\\"$IMG\\"}]}"`,
          },
        ];

  async function copy(key: string, code: string) {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(key);
      toast.success("복사했습니다 — 노트북에 붙여넣으세요");
      setTimeout(() => setCopied(null), 2000);
    } catch {
      toast.error("복사에 실패했습니다 — 직접 선택해 복사하세요");
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            예제 코드 — {service.name}
            <Badge variant="outline" className="text-[10px]">
              {isLlm ? "대화형 LLM" : service.modelName}
            </Badge>
          </DialogTitle>
          <DialogDescription>
            이 서비스를 실제로 호출하는 코드입니다. 복사해서 노트북에 붙여넣으면
            바로 실행됩니다.
          </DialogDescription>
        </DialogHeader>

        <Tabs defaultValue={samples[0].key}>
          <TabsList>
            {samples.map((s) => (
              <TabsTrigger key={s.key} value={s.key}>{s.label}</TabsTrigger>
            ))}
          </TabsList>
          {samples.map((s) => (
            <TabsContent key={s.key} value={s.key} className="mt-3 space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-[11px] text-muted-foreground">{s.hint}</p>
                <Button size="sm" variant="outline"
                        onClick={() => copy(s.key, s.code)}>
                  {copied === s.key
                    ? <><Check className="size-3.5" /> 복사됨</>
                    : <><Copy className="size-3.5" /> 복사</>}
                </Button>
              </div>
              <pre className="max-h-[320px] overflow-auto rounded-lg border border-border
                              bg-secondary/40 p-3 font-mono text-[11.5px] leading-relaxed">
                {s.code}
              </pre>
            </TabsContent>
          ))}
        </Tabs>

        <div className="rounded-md border border-sky-200 bg-sky-50/70 p-2.5 text-[11px] leading-snug text-sky-900">
          <b>호출 주소</b>
          <div className="mt-0.5 break-all font-mono">{url}</div>
          <div className="mt-1">
            같은 사내망이면 어느 PC에서든 이 주소로 호출할 수 있습니다.
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
