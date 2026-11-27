"use client";
/**
 * 설정 관리 — 선택한 서비스 클러스터의 ConfigMap·Secret 조회 + 쓰기(사용자 self-service).
 * 목록은 키만 표시(값 숨김). Secret 값은 '값 보기'로 명시적 열람할 때만 디코드해 가져온다.
 * 레이아웃·폰트는 IaaS 목록 화면 규약과 동일.
 */
import * as React from "react";
import { Search, Lock, Settings2, Plus, Trash2, Eye } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { useConfirm } from "@/components/ui/confirm";
import { ClusterScopeBar, useClusterScope } from "@/components/paas/cluster-scope-bar";
import {
  useK8sConfigs, useNamespaces,
  useCreateConfigMap, useDeleteConfigMap, useCreateSecret, useDeleteSecret, revealSecret,
  type ConfigMapRow, type SecretRow,
} from "@/lib/paas-api";

/** "KEY=VALUE" 여러 줄 → 객체 */
function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const i = line.indexOf("=");
    if (i < 0) continue;
    const k = line.slice(0, i).trim();
    if (k) out[k] = line.slice(i + 1);
  }
  return out;
}

export default function ConfigPage() {
  const { clusterId } = useClusterScope();
  const [namespace, setNamespace] = React.useState("all");
  const [q, setQ] = React.useState("");
  const [dialog, setDialog] = React.useState<{ kind: "cm" | "secret" } | null>(null);
  const [reveal, setReveal] = React.useState<SecretRow | null>(null);
  const [tab, setTab] = React.useState<"cm" | "secret">("cm");

  const data = useK8sConfigs(clusterId, namespace === "all" ? undefined : namespace);
  const cms = (data.data?.configmaps ?? []).filter((c) => !q || c.name.includes(q));
  const secrets = (data.data?.secrets ?? []).filter((s) => !q || s.name.includes(q));

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <ClusterScopeBar
          queryKeys={[["paas-configs"], ["paas-namespaces"]]}
          namespace={namespace}
          onNamespaceChange={setNamespace}
          trailing={
            <Button size="sm" disabled={!clusterId} onClick={() => setDialog({ kind: tab })}>
              <Plus className="size-3.5" /> {tab === "cm" ? "ConfigMap" : "Secret"} 생성
            </Button>
          }
        >
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="이름 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-52 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {cms.length + secrets.length}개</span>
        </ClusterScopeBar>
      </Card>

      {!clusterId ? (
        <Card><EmptyBox text="먼저 서비스 클러스터를 선택하세요." /></Card>
      ) : data.isLoading ? (
        <Skeleton className="h-64" />
      ) : data.isError ? (
        <Card><EmptyBox text="설정 조회 실패 — 클러스터 API 응답 오류." /></Card>
      ) : (
        <Tabs value={tab} onValueChange={(v) => setTab(v as "cm" | "secret")}>
          <TabsList>
            <TabsTrigger value="cm">ConfigMap ({cms.length})</TabsTrigger>
            <TabsTrigger value="secret">Secret ({secrets.length})</TabsTrigger>
          </TabsList>

          <TabsContent value="cm" className="pt-3">
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ConfigMap</TableHead>
                    <TableHead>네임스페이스</TableHead>
                    <TableHead>키</TableHead>
                    <TableHead className="w-12" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {cms.map((c) => <CmRow key={`${c.namespace}/${c.name}`} clusterId={clusterId} c={c} />)}
                  {cms.length === 0 && <EmptyRow colSpan={4} />}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          <TabsContent value="secret" className="pt-3">
            <div className="mb-2 flex items-center gap-1.5 text-xs text-muted-foreground">
              <Lock className="size-3" /> 목록엔 값을 표시하지 않습니다 — '값 보기'로 열람.
            </div>
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Secret</TableHead>
                    <TableHead>네임스페이스</TableHead>
                    <TableHead>타입</TableHead>
                    <TableHead>키</TableHead>
                    <TableHead className="w-20" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {secrets.map((s) => (
                    <SecretRowC key={`${s.namespace}/${s.name}`} clusterId={clusterId} s={s} onReveal={() => setReveal(s)} />
                  ))}
                  {secrets.length === 0 && <EmptyRow colSpan={5} />}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>
        </Tabs>
      )}

      {dialog?.kind === "cm" && <CreateDialog clusterId={clusterId} secret={false} defaultNs={namespace === "all" ? "default" : namespace} onClose={() => setDialog(null)} />}
      {dialog?.kind === "secret" && <CreateDialog clusterId={clusterId} secret defaultNs={namespace === "all" ? "default" : namespace} onClose={() => setDialog(null)} />}
      {reveal && <RevealDialog clusterId={clusterId} s={reveal} onClose={() => setReveal(null)} />}
    </div>
  );
}

function KeyBadges({ keys }: { keys: string[] }) {
  return (
    <div className="flex max-w-[380px] flex-wrap gap-1">
      {keys.slice(0, 6).map((k) => <Badge key={k} variant="outline" className="font-mono text-[10px]">{k}</Badge>)}
      {keys.length > 6 && <span className="text-[11px] text-muted-foreground">+{keys.length - 6}</span>}
      {keys.length === 0 && <span className="text-muted-foreground">—</span>}
    </div>
  );
}

function CmRow({ clusterId, c }: { clusterId: string; c: ConfigMapRow }) {
  const del = useDeleteConfigMap(clusterId);
  const confirm = useConfirm();
  const onDelete = async () => {
    if (!(await confirm({ title: "ConfigMap 삭제", destructive: true, confirmLabel: "삭제", description: `${c.name} 을(를) 삭제할까요?` }))) return;
    del.mutate({ namespace: c.namespace, name: c.name },
      { onSuccess: () => toast.success(`${c.name} 삭제됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  };
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <Settings2 className="size-3.5 text-brand-indigo-600" />
          <span className="max-w-[240px] truncate font-mono text-sm font-semibold">{c.name}</span>
        </div>
      </TableCell>
      <TableCell className="font-mono text-xs">{c.namespace}</TableCell>
      <TableCell><KeyBadges keys={c.keys} /></TableCell>
      <TableCell>
        <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={onDelete}><Trash2 className="size-3.5 text-rose-600" /></Button>
      </TableCell>
    </TableRow>
  );
}

function SecretRowC({ clusterId, s, onReveal }: { clusterId: string; s: SecretRow; onReveal: () => void }) {
  const del = useDeleteSecret(clusterId);
  const confirm = useConfirm();
  const onDelete = async () => {
    if (!(await confirm({ title: "Secret 삭제", destructive: true, confirmLabel: "삭제", description: `${s.name} 을(를) 삭제할까요?` }))) return;
    del.mutate({ namespace: s.namespace, name: s.name },
      { onSuccess: () => toast.success(`${s.name} 삭제됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  };
  return (
    <TableRow>
      <TableCell>
        <div className="flex items-center gap-2">
          <Lock className="size-3.5 text-brand-indigo-600" />
          <span className="max-w-[240px] truncate font-mono text-sm font-semibold">{s.name}</span>
        </div>
      </TableCell>
      <TableCell className="font-mono text-xs">{s.namespace}</TableCell>
      <TableCell className="max-w-[220px] truncate font-mono text-[11px] text-muted-foreground">{s.type}</TableCell>
      <TableCell><KeyBadges keys={s.keys} /></TableCell>
      <TableCell>
        <div className="flex items-center gap-0.5">
          <Button variant="ghost" size="icon-sm" title="값 보기" onClick={onReveal}><Eye className="size-3.5" /></Button>
          <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={onDelete}><Trash2 className="size-3.5 text-rose-600" /></Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function CreateDialog({ clusterId, secret, defaultNs, onClose }: { clusterId: string; secret: boolean; defaultNs: string; onClose: () => void }) {
  const createCm = useCreateConfigMap(clusterId);
  const createSecret = useCreateSecret(clusterId);
  const namespaces = useNamespaces(clusterId);
  const [ns, setNs] = React.useState(defaultNs);
  const [name, setName] = React.useState("");
  const [kv, setKv] = React.useState("");
  const pending = createCm.isPending || createSecret.isPending;

  const submit = () => {
    const data = parseKV(kv);
    const done = { onSuccess: () => { toast.success(`${name} 생성됨`); onClose(); }, onError: (e: Error) => toast.error("생성 실패", { description: String(e.message) }) };
    if (secret) createSecret.mutate({ namespace: ns, name, data }, done);
    else createCm.mutate({ namespace: ns, name, data }, done);
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{secret ? "Secret" : "ConfigMap"} 생성</DialogTitle>
          <DialogDescription>{secret ? "Opaque Secret — 값은 저장 시 base64 로 인코딩됩니다." : "키·값 설정을 만듭니다."}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>네임스페이스</Label>
              <Select value={ns} onValueChange={setNs}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(namespaces.data ?? [ns]).map((n) => <SelectItem key={n} value={n} className="font-mono text-xs">{n}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>이름</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-config" />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>데이터 (KEY=VALUE, 한 줄에 하나)</Label>
            <textarea
              value={kv}
              onChange={(e) => setKv(e.target.value)}
              rows={6}
              placeholder={"LOG_LEVEL=info\nAPI_URL=https://..."}
              className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button disabled={pending || !name} onClick={submit}>생성</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RevealDialog({ clusterId, s, onClose }: { clusterId: string; s: SecretRow; onClose: () => void }) {
  const [data, setData] = React.useState<Record<string, string> | null>(null);
  const [err, setErr] = React.useState<string | null>(null);
  React.useEffect(() => {
    revealSecret(clusterId, s.namespace, s.name).then((d) => setData(d.data)).catch((e) => setErr(String(e.message)));
  }, [clusterId, s]);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Lock className="size-4 text-brand-indigo-600" /> {s.name}</DialogTitle>
          <DialogDescription className="font-mono text-xs">{s.namespace} · {s.type}</DialogDescription>
        </DialogHeader>
        {err ? (
          <div className="py-6 text-center text-sm text-rose-500">{err}</div>
        ) : !data ? (
          <Skeleton className="h-32" />
        ) : (
          <div className="space-y-2">
            {Object.entries(data).map(([k, v]) => (
              <div key={k} className="space-y-1">
                <div className="font-mono text-[11px] text-muted-foreground">{k}</div>
                <pre className="max-h-40 overflow-auto rounded bg-secondary p-2 font-mono text-xs break-all whitespace-pre-wrap">{v}</pre>
              </div>
            ))}
            {Object.keys(data).length === 0 && <div className="text-sm text-muted-foreground">키가 없습니다.</div>}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function EmptyBox({ text }: { text: string }) {
  return <div className="py-10 text-center text-sm text-muted-foreground">{text}</div>;
}
function EmptyRow({ colSpan }: { colSpan: number }) {
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="py-10 text-center text-sm text-muted-foreground">항목이 없습니다.</TableCell>
    </TableRow>
  );
}
