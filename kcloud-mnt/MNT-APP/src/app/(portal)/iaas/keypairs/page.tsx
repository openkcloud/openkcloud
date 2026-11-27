"use client";
import * as React from "react";
import { KeyRound, Plus, Trash2, Copy, Download, ShieldAlert, Loader2, Search } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { useAuth } from "@/lib/auth";
import { useConfirm } from "@/components/ui/confirm";
import { useSshKeys, useCreateSshKey, useDeleteSshKey, type SshKey } from "@/lib/sshkey-api";
import { useNovaKeypairs, useImportNovaKeypair, useDeleteNovaKeypair, type NovaKeypair } from "@/lib/iaas-api";
import { generateRsaSshKeypair, downloadText } from "@/lib/ssh-keygen";

// 키페어 메뉴 — 역할로 화면 분기: 관리자=Nova 키페어(클러스터 노드 인프라 키), 사용자=포털 DB SSH키(self-service).
export default function KeypairsPage() {
  const role = useAuth((s) => s.user?.role);
  if (role === "admin") return <NovaKeypairsView />;
  return <UserKeypairsView />;
}

// 내 SSH 키(키페어) — 사용자 self-service. 개인키는 서버에 저장하지 않고 브라우저에서만 다운로드.
function UserKeypairsView() {
  const owner = useAuth((s) => s.user?.username);
  const { data, isLoading } = useSshKeys(owner);
  const del = useDeleteSshKey(owner ?? "");
  const confirm = useConfirm();
  const [genOpen, setGenOpen] = React.useState(false);
  const [importOpen, setImportOpen] = React.useState(false);

  const copy = (t: string, label: string) => { navigator.clipboard.writeText(t); toast.success(`${label} 복사됨`); };

  async function remove(k: SshKey) {
    if (!(await confirm({
      title: "SSH 키 삭제", destructive: true, confirmLabel: "삭제",
      description: `'${k.name}' 키를 삭제할까요? 이미 VM에 심긴 공개키는 그대로 남습니다(파일). ${k.vmCount > 0 ? `현재 ${k.vmCount}개 VM과 연결됨.` : ""}`,
    }))) return;
    del.mutate(k.id, {
      onSuccess: () => toast.success(`${k.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  if (!owner) return <Card className="p-6 text-sm text-muted-foreground">로그인 후 이용 가능합니다.</Card>;
  if (isLoading || !data) return <Skeleton className="h-96" />;

  return (
    <div className="space-y-4">
      <div className="rounded-md border border-brand-indigo-200 bg-brand-indigo-50/50 p-3 text-[11px] text-brand-indigo-900">
        SSH 키는 <strong>내 개인 자원</strong>입니다(프로젝트와 무관). <strong>개인키(private key)는 서버에 저장되지 않고</strong> 생성 시 브라우저에서 한 번만 내려받습니다 — 분실하면 재발급해야 합니다. 가상서버 요청 시 여기 등록한 공개키를 선택해 접속에 사용합니다.
      </div>

      <Card className="p-4">
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground">내 키 {data.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setImportOpen(true)}>공개키 가져오기</Button>
            <Button size="sm" onClick={() => setGenOpen(true)}><Plus className="size-3.5" /> SSH 키 생성</Button>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>이름</TableHead>
              <TableHead>지문 (SHA256)</TableHead>
              <TableHead className="text-center">연결된 VM</TableHead>
              <TableHead>생성일</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.map((k) => (
              <TableRow key={k.id}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <KeyRound className="size-3.5 text-brand-indigo-600" />
                    <span className="font-mono text-sm font-semibold">{k.name}</span>
                  </div>
                </TableCell>
                <TableCell>
                  <button
                    type="button"
                    className="max-w-[320px] cursor-pointer truncate font-mono text-[11px] text-muted-foreground hover:text-foreground"
                    title="공개키 복사"
                    onClick={() => copy(k.publicKey, "공개키")}
                  >
                    {k.fingerprint}
                  </button>
                </TableCell>
                <TableCell className="text-center font-mono tabular-nums" title="포털을 통해 이 키로 프로비저닝된 VM 수">
                  {k.vmCount}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{k.createdAt?.slice(0, 10) || "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="icon-sm" title="공개키 복사" onClick={() => copy(k.publicKey, "공개키")}>
                      <Copy className="size-3.5" />
                    </Button>
                    <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(k)}>
                      <Trash2 className="size-3.5 text-rose-600" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {data.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-10 text-center text-sm text-muted-foreground">
                  등록된 SSH 키가 없습니다. “SSH 키 생성” 또는 “공개키 가져오기”로 추가하세요.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <GenerateKeyDialog owner={owner} open={genOpen} onClose={() => setGenOpen(false)} />
      <ImportKeyDialog owner={owner} open={importOpen} onClose={() => setImportOpen(false)} />
    </div>
  );
}

function GenerateKeyDialog({ owner, open, onClose }: { owner: string; open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const create = useCreateSshKey(owner);
  React.useEffect(() => { if (open) setName(""); }, [open]);

  async function submit() {
    const nm = name.trim();
    if (!nm) return toast.error("키 이름을 입력하세요");
    setBusy(true);
    try {
      // 1) 브라우저에서 키페어 생성
      const { publicKey, privateKeyPem } = await generateRsaSshKeypair(`${owner}@openkcloud`);
      // 2) 공개키만 서버에 등록
      await create.mutateAsync({ name: nm, publicKey });
      // 3) 개인키는 그 자리에서 로컬 다운로드(서버 저장 안 함)
      downloadText(`${nm}.pem`, privateKeyPem);
      toast.success(`SSH 키 '${nm}' 생성됨`, { description: "개인키(.pem)를 내려받았습니다. 안전하게 보관하세요 (chmod 600)." });
      onClose();
    } catch (e) {
      toast.error("생성 실패", { description: String((e as Error).message) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>SSH 키 생성</DialogTitle>
          <DialogDescription>브라우저에서 키페어를 생성합니다. 개인키(.pem)는 이 자리에서 1회 다운로드되며 서버에 저장되지 않습니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>키 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-laptop" disabled={busy} />
          </div>
          <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-2.5 text-[11px] text-amber-900">
            <ShieldAlert className="mt-0.5 size-3.5 shrink-0" />
            개인키는 분실 시 복구 불가입니다. 다운로드한 .pem 파일을 안전하게 보관하세요.
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>취소</Button>
          <Button onClick={submit} disabled={busy}>
            {busy ? <><Loader2 className="size-3.5 animate-spin" /> 생성 중…</> : <><Download className="size-3.5" /> 생성 + 개인키 다운로드</>}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ImportKeyDialog({ owner, open, onClose }: { owner: string; open: boolean; onClose: () => void }) {
  const [name, setName] = React.useState("");
  const [pub, setPub] = React.useState("");
  const create = useCreateSshKey(owner);
  React.useEffect(() => { if (open) { setName(""); setPub(""); } }, [open]);

  function submit() {
    if (!name.trim()) return toast.error("키 이름을 입력하세요");
    if (!pub.trim()) return toast.error("공개키를 붙여넣으세요");
    create.mutate(
      { name: name.trim(), publicKey: pub.trim() },
      {
        onSuccess: () => { toast.success(`SSH 키 '${name}' 등록됨`); onClose(); },
        onError: (e) => toast.error("등록 실패", { description: String((e as Error).message) }),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !create.isPending && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>공개키 가져오기</DialogTitle>
          <DialogDescription>이미 가진 공개키(~/.ssh/id_*.pub)를 붙여넣어 등록합니다. 개인키는 입력하지 마세요.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5">
            <Label>키 이름</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="my-laptop" />
          </div>
          <div className="space-y-1.5">
            <Label>공개키 (OpenSSH)</Label>
            <textarea
              value={pub}
              onChange={(e) => setPub(e.target.value)}
              placeholder="ssh-rsa AAAA... 또는 ssh-ed25519 AAAA..."
              rows={4}
              className="flex w-full rounded-md border border-input bg-card px-3 py-2 font-mono text-xs shadow-xs ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={create.isPending}>취소</Button>
          <Button onClick={submit} disabled={create.isPending}>{create.isPending ? "등록 중…" : "등록"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── 관리자: Nova 키페어(클러스터 노드 SSH·인프라). 공개키 import 만(개인키 서버 비노출). ──
function NovaKeypairsView() {
  const { data, isLoading } = useNovaKeypairs();
  const del = useDeleteNovaKeypair();
  const confirm = useConfirm();
  const [q, setQ] = React.useState("");
  const [importOpen, setImportOpen] = React.useState(false);
  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter((k) => !q || k.name.toLowerCase().includes(q.toLowerCase()));

  async function remove(k: NovaKeypair) {
    if (!(await confirm({
      title: "키페어 삭제", destructive: true, confirmLabel: "삭제",
      description: `${k.name} 을(를) Nova 에서 삭제할까요? 이 키로 만든 노드 접속에 영향이 있을 수 있습니다.`,
    }))) return;
    del.mutate(k.name, {
      onSuccess: () => toast.success(`키페어 ${k.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="키페어 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개 · Nova 키페어(클러스터 노드 SSH)</span>
          <div className="ml-auto">
            <Button size="sm" onClick={() => setImportOpen(true)}><Plus className="size-3.5" /> 공개키 등록</Button>
          </div>
        </div>
      </Card>
      <Card>
        <Table>
          <TableHeader><TableRow>
            <TableHead>이름</TableHead><TableHead>지문(fingerprint)</TableHead><TableHead>타입</TableHead><TableHead className="w-12" />
          </TableRow></TableHeader>
          <TableBody>
            {items.map((k) => (
              <TableRow key={k.name}>
                <TableCell><div className="flex items-center gap-2"><KeyRound className="size-3.5 text-brand-indigo-600" /><span className="font-mono text-sm font-semibold">{k.name}</span></div></TableCell>
                <TableCell className="font-mono text-[11px] text-muted-foreground">{k.fingerprint || "—"}</TableCell>
                <TableCell className="font-mono text-xs">{k.type}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(k)}><Trash2 className="size-3.5 text-rose-600" /></Button>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && <TableRow><TableCell colSpan={4} className="py-10 text-center text-sm text-muted-foreground">Nova 키페어가 없습니다. 공개키를 등록하세요.</TableCell></TableRow>}
          </TableBody>
        </Table>
      </Card>
      {importOpen && <ImportNovaKeypairDialog onClose={() => setImportOpen(false)} />}
    </div>
  );
}

function ImportNovaKeypairDialog({ onClose }: { onClose: () => void }) {
  const imp = useImportNovaKeypair();
  const [name, setName] = React.useState("");
  const [publicKey, setPublicKey] = React.useState("");
  function submit() {
    if (!name.trim() || !publicKey.trim()) return toast.error("이름과 공개키를 입력하세요");
    imp.mutate({ name: name.trim(), publicKey: publicKey.trim() }, {
      onSuccess: (k) => { toast.success(`키페어 ${k.name} 등록됨`); onClose(); },
      onError: (e) => toast.error("등록 실패", { description: String((e as Error).message) }),
    });
  }
  return (
    <Dialog open onOpenChange={(o) => !o && !imp.isPending && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>공개키 등록 (Nova)</DialogTitle>
          <DialogDescription>클러스터 노드 접속용 공개키를 Nova 에 등록합니다. 개인키는 서버에 저장하지 않습니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div className="space-y-1.5"><Label>이름</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="k8s-node-key" className="font-mono text-xs" /></div>
          <div className="space-y-1.5"><Label>공개키 (ssh-rsa / ssh-ed25519 …)</Label>
            <textarea value={publicKey} onChange={(e) => setPublicKey(e.target.value)} rows={5}
              placeholder="ssh-ed25519 AAAA..." className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring" /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={imp.isPending}>취소</Button>
          <Button onClick={submit} disabled={imp.isPending}>{imp.isPending ? "등록 중…" : "등록"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
