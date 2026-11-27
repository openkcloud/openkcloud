"use client";
import * as React from "react";
import { Disc, Search, Trash2, Settings, Loader2, Plus, X } from "lucide-react";
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
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import {
  useImages, useSetImageVisibility, useDeleteImage, useProjects, byProjectThenName,
  useImage, useUpdateImageProperties, useRegisterImage, type ImageSummary,
} from "@/lib/iaas-api";

// 자주 쓰는 Glance 공통 속성 — 새 속성 추가 시 빠른 채움(PaaS 클러스터 템플릿은 os_distro 필수)
const COMMON_PROPS = ["os_distro", "kube_version", "os_version", "architecture", "hw_disk_bus", "hw_scsi_model"];
const DISK_FORMATS = ["qcow2", "raw", "vmdk", "vdi", "iso", "ami"];

const VIS_VARIANT: Record<string, "success" | "secondary" | "warning" | "outline"> = {
  public: "success", shared: "warning", private: "outline", community: "secondary",
};

// 이미지 — OpenStack Glance 실연동(라이브 조회, 포털 DB 캐시 없음). 관리자 전용.
export default function ImagesPage() {
  const { data, isLoading } = useImages();
  const { data: projData } = useProjects();
  const del = useDeleteImage();
  const confirm = useConfirm();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [selected, setSelected] = React.useState<ImageSummary | undefined>();
  const [registerOpen, setRegisterOpen] = React.useState(false);

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] || id.slice(0, 8) || "—";
  }, [projData]);
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set((data ?? []).map((i) => i.tenantId).filter(Boolean)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [data, projName]);

  if (isLoading || !data) return <Skeleton className="h-96" />;
  const items = data.filter(
    (i) => (!q || i.name.toLowerCase().includes(q.toLowerCase())) && (projFilter === "all" || i.tenantId === projFilter),
  ).sort(byProjectThenName(projName));

  async function remove(img: ImageSummary) {
    if (img.protected) {
      await confirm({ title: "삭제 불가", confirmLabel: "확인", description: "보호(protected) 이미지입니다. 보호 해제 후 삭제할 수 있습니다." });
      return;
    }
    if (!(await confirm({ title: "이미지 삭제", destructive: true, confirmLabel: "삭제", description: `${img.name} 을(를) 삭제할까요? 되돌릴 수 없습니다.` }))) return;
    del.mutate(img.id, {
      onSuccess: () => toast.success(`이미지 ${img.name} 삭제됨`),
      onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <Select value={projFilter} onValueChange={setProjFilter}>
            <SelectTrigger className="w-48"><SelectValue placeholder="프로젝트 필터" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">전체 프로젝트</SelectItem>
              {projOptions.map((p) => (<SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>))}
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="이미지 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["iaas-images"]]} />
            <Button size="sm" onClick={() => setRegisterOpen(true)}><Plus className="size-3.5" /> 이미지 등록</Button>
          </div>
        </div>
      </Card>

      <Card>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>프로젝트</TableHead>
              <TableHead>이미지</TableHead>
              <TableHead>OS</TableHead>
              <TableHead>포맷</TableHead>
              <TableHead className="text-center">크기</TableHead>
              <TableHead>가시성</TableHead>
              <TableHead>상태</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((i) => (
              <TableRow key={i.id} className="cursor-pointer" onClick={() => setSelected(i)}>
                <TableCell className="text-xs font-medium">{i.tenantId ? projName(i.tenantId) : "—"}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Disc className="size-3.5 text-brand-indigo-600" />
                    <span className="max-w-[220px] truncate font-mono text-sm font-semibold">{i.name}</span>
                  </div>
                </TableCell>
                <TableCell className="text-xs">{i.os || "—"}</TableCell>
                <TableCell className="font-mono text-xs">{i.diskFormat}</TableCell>
                <TableCell className="text-center font-mono tabular-nums">{i.sizeGb}GB</TableCell>
                <TableCell><Badge variant={VIS_VARIANT[i.visibility] ?? "secondary"} className="text-[10px]">{i.visibility}</Badge></TableCell>
                <TableCell><Badge variant={i.status === "active" ? "success" : "secondary"} className="text-[10px]">{i.status}</Badge></TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setSelected(i)}><Settings className="size-3.5" /> 관리</Button>
                    <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => remove(i)}>
                      <Trash2 className="size-3.5 text-rose-600" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {items.length === 0 && (
              <TableRow><TableCell colSpan={8} className="py-10 text-center text-sm text-muted-foreground">이미지가 없습니다.</TableCell></TableRow>
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(undefined)}>
        <DialogContent className="max-w-2xl">
          {selected && <ImageDetailBody image={selected} projectLabel={projName(selected.tenantId)} />}
        </DialogContent>
      </Dialog>

      <RegisterImageDialog key={registerOpen ? "reg-open" : "reg-closed"} open={registerOpen} onClose={() => setRegisterOpen(false)} />
    </div>
  );
}

function ImageDetailBody({ image, projectLabel }: { image: ImageSummary; projectLabel: string }) {
  const setVis = useSetImageVisibility();
  const [vis, setVis_] = React.useState(image.visibility);
  const detail = useImage(image.id);

  function applyVis(v: string) {
    setVis_(v);
    setVis.mutate({ id: image.id, visibility: v }, {
      onSuccess: () => toast.success(`가시성 → ${v}`),
      onError: (e) => { setVis_(image.visibility); toast.error("가시성 변경 실패", { description: String((e as Error).message) }); },
    });
  }

  return (
    <>
      <DialogHeader>
        <div className="flex items-center gap-3 pr-8">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
            <Disc className="size-5" />
          </div>
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate font-mono text-lg">{image.name}</DialogTitle>
            <DialogDescription className="truncate text-xs"><span className="font-mono">{image.id}</span> · 프로젝트 {projectLabel}</DialogDescription>
          </div>
        </div>
      </DialogHeader>

      <Card className="space-y-2.5 p-4 text-xs">
        <KV label="OS" value={image.os || "—"} />
        <KV label="디스크 포맷" value={image.diskFormat} mono />
        <KV label="컨테이너 포맷" value={image.containerFormat || "—"} mono />
        <KV label="크기" value={`${image.sizeGb} GB`} />
        <KV label="최소 디스크/RAM" value={`${image.minDisk}GB / ${image.minRam}MB`} />
        <KV label="상태" value={image.status} />
        <KV label="보호(protected)" value={image.protected ? "예" : "아니오"} />
        <div className="flex items-center justify-between gap-3">
          <span className="shrink-0 text-muted-foreground">가시성</span>
          <Select value={vis} onValueChange={applyVis}>
            <SelectTrigger className="h-8 w-40"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="public">public</SelectItem>
              <SelectItem value="private">private</SelectItem>
              <SelectItem value="shared">shared</SelectItem>
              <SelectItem value="community">community</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {setVis.isPending && <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground"><Loader2 className="size-3 animate-spin" /> 변경 중…</div>}
      </Card>

      {detail.isLoading
        ? <div className="flex items-center gap-1.5 py-2 text-[11px] text-muted-foreground"><Loader2 className="size-3 animate-spin" /> 속성 불러오는 중…</div>
        : <ImageProperties key={image.id} imageId={image.id} properties={detail.data?.properties ?? {}} />}
    </>
  );
}

// 이미지 속성(메타데이터) 편집 — os_distro 등 추가·수정·삭제. Horizon 의 '메타데이터'에 해당.
function ImageProperties({ imageId, properties }: { imageId: string; properties: Record<string, string> }) {
  const upd = useUpdateImageProperties();
  const [draft, setDraft] = React.useState<Record<string, string>>(properties);
  const [newKey, setNewKey] = React.useState("");
  const [newVal, setNewVal] = React.useState("");

  const keys = Object.keys(draft).sort();
  const dirty = React.useMemo(() => {
    const a = JSON.stringify(Object.entries(draft).sort());
    const b = JSON.stringify(Object.entries(properties).sort());
    return a !== b;
  }, [draft, properties]);

  function addNew() {
    const k = newKey.trim();
    if (!k) return toast.error("속성 키를 입력하세요");
    if (k in draft) return toast.error("이미 있는 속성입니다");
    setDraft((d) => ({ ...d, [k]: newVal }));
    setNewKey(""); setNewVal("");
  }
  function removeKey(k: string) { setDraft((d) => { const n = { ...d }; delete n[k]; return n; }); }

  function save() {
    const set: Record<string, string> = {};
    for (const [k, v] of Object.entries(draft)) if (properties[k] !== v) set[k] = v;
    const remove = Object.keys(properties).filter((k) => !(k in draft));
    if (!Object.keys(set).length && !remove.length) return;
    upd.mutate({ id: imageId, set, remove }, {
      onSuccess: () => toast.success("속성 저장됨"),
      onError: (e) => toast.error("속성 저장 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Card className="space-y-3 p-4">
      <div className="flex items-center justify-between">
        <span className="text-xs font-semibold">속성(메타데이터)</span>
        {(!draft.os_distro || !draft.kube_version) && (
          <span className="text-[10px] text-amber-700">
            PaaS 클러스터 템플릿 생성에는 {[!draft.os_distro && "os_distro", !draft.kube_version && "kube_version"].filter(Boolean).join(", ")} 가 필요합니다.
          </span>
        )}
      </div>

      <div className="space-y-2">
        {keys.length === 0 && <p className="text-[11px] text-muted-foreground">등록된 속성이 없습니다.</p>}
        {keys.map((k) => (
          <div key={k} className="flex items-center gap-2">
            <span className="w-56 shrink-0 break-all font-mono text-[11px]" title={k}>{k}</span>
            <Input value={draft[k]} onChange={(e) => setDraft((d) => ({ ...d, [k]: e.target.value }))} className="h-8 font-mono text-xs" />
            <Button variant="ghost" size="icon-sm" title="속성 제거" onClick={() => removeKey(k)}><X className="size-3.5 text-rose-600" /></Button>
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2 border-t pt-2">
        <Input list="common-props" value={newKey} onChange={(e) => setNewKey(e.target.value)} placeholder="키 (예: os_distro)" className="h-8 w-40 font-mono text-xs" />
        <datalist id="common-props">{COMMON_PROPS.map((p) => <option key={p} value={p} />)}</datalist>
        <Input value={newVal} onChange={(e) => setNewVal(e.target.value)} placeholder="값 (예: ubuntu)" className="h-8 font-mono text-xs" />
        <Button variant="outline" size="sm" className="h-8 shrink-0" onClick={addNew}><Plus className="size-3.5" /> 추가</Button>
      </div>

      <div className="flex justify-end">
        <Button size="sm" disabled={!dirty || upd.isPending} onClick={save}>{upd.isPending ? "저장 중…" : "속성 저장"}</Button>
      </div>
    </Card>
  );
}

// 이미지 등록 — URL 가져오기(web-download). Glance 가 URL 에서 직접 받아옴(포털 미경유).
function RegisterImageDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const reg = useRegisterImage();
  const [f, setF] = React.useState({
    name: "", url: "", diskFormat: "qcow2", containerFormat: "bare",
    visibility: "private", minDisk: "0", minRam: "0", osDistro: "", kubeVersion: "",
  });
  const set = (k: string, v: string) => setF((s) => ({ ...s, [k]: v }));

  function submit() {
    if (!f.name.trim() || !f.url.trim()) return toast.error("이름과 URL 을 입력하세요");
    reg.mutate({
      name: f.name.trim(), url: f.url.trim(), diskFormat: f.diskFormat, containerFormat: f.containerFormat,
      visibility: f.visibility, minDisk: Number(f.minDisk) || 0, minRam: Number(f.minRam) || 0,
      properties: {
        ...(f.osDistro.trim() ? { os_distro: f.osDistro.trim() } : {}),
        ...(f.kubeVersion.trim() ? { kube_version: f.kubeVersion.trim() } : {}),
      },
    }, {
      onSuccess: (img) => { toast.success(`이미지 ${img.name} 등록 시작됨`, { description: `상태: ${img.status} — 가져오기가 끝나면 active 가 됩니다.` }); onClose(); },
      onError: (e) => toast.error("이미지 등록 실패", { description: String((e as Error).message) }),
    });
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && !reg.isPending && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>이미지 등록</DialogTitle>
          <DialogDescription>URL 가져오기(web-download) — Glance 가 지정한 URL 에서 직접 내려받습니다. 등록 직후 상태는 importing 이며 완료되면 active 가 됩니다.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-1">
          <div className="space-y-1.5">
            <Label>이름</Label>
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="ubuntu-jammy-kube-rbln-2.0.1" className="font-mono text-xs" />
          </div>
          <div className="space-y-1.5">
            <Label>이미지 URL</Label>
            <Input value={f.url} onChange={(e) => set("url", e.target.value)} placeholder="https://.../ubuntu-jammy.qcow2" className="font-mono text-xs" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>디스크 포맷</Label>
              <Select value={f.diskFormat} onValueChange={(v) => set("diskFormat", v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>{DISK_FORMATS.map((d) => <SelectItem key={d} value={d}>{d}</SelectItem>)}</SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>컨테이너 포맷</Label>
              <Input value={f.containerFormat} onChange={(e) => set("containerFormat", e.target.value)} className="font-mono text-xs" />
            </div>
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <Label>가시성</Label>
              <Select value={f.visibility} onValueChange={(v) => set("visibility", v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="private">private</SelectItem>
                  <SelectItem value="public">public</SelectItem>
                  <SelectItem value="shared">shared</SelectItem>
                  <SelectItem value="community">community</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5"><Label>최소 디스크(GB)</Label><Input type="number" min={0} value={f.minDisk} onChange={(e) => set("minDisk", e.target.value)} /></div>
            <div className="space-y-1.5"><Label>최소 RAM(MB)</Label><Input type="number" min={0} value={f.minRam} onChange={(e) => set("minRam", e.target.value)} /></div>
          </div>
          <div className="space-y-1.5">
            <Label>os_distro (권장)</Label>
            <Input list="common-os-distro" value={f.osDistro} onChange={(e) => set("osDistro", e.target.value)} placeholder="ubuntu" className="font-mono text-xs" />
            <datalist id="common-os-distro">{["ubuntu", "centos", "rhel", "rocky", "debian", "fedora"].map((o) => <option key={o} value={o} />)}</datalist>
          </div>
          <div className="space-y-1.5">
            <Label>kube_version (클러스터용 이미지면 권장)</Label>
            <Input value={f.kubeVersion} onChange={(e) => set("kubeVersion", e.target.value)} placeholder="예: 1.36.2" className="font-mono text-xs" />
            <p className="text-[10px] text-muted-foreground">PaaS 클러스터 템플릿은 os_distro 와 kube_version 이 모두 있어야 생성됩니다. 나머지 속성은 등록 후 관리 화면에서 추가할 수 있습니다.</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={reg.isPending}>취소</Button>
          <Button onClick={submit} disabled={reg.isPending}>{reg.isPending ? "등록 중…" : "등록"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KV({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className={mono ? "min-w-0 break-all text-right font-mono" : "min-w-0 break-words text-right"}>{value}</span>
    </div>
  );
}
