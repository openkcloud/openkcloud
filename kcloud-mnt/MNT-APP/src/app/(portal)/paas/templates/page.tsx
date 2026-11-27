"use client";
/**
 * 클러스터 템플릿 — Magnum ClusterTemplate 관리(관리자 전용, Horizon 과 동일 범위).
 * 클러스터 생성의 설계도. 고른 프로젝트 소유로 만든다(Horizon 과 같음) — 이미지·플레이버·고정 네트워크는 그 프로젝트가
 * 쓸 수 있는 것(소유 + 공유)만, 외부 네트워크는 외부망만 드롭다운.
 * 레이아웃·폰트는 IaaS 목록 화면 규약과 동일.
 */
import * as React from "react";
import { Boxes, Search, Plus, Pencil, Trash2, Copy } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { RefreshControls } from "@/components/iaas/refresh-controls";
import { useAuth } from "@/lib/auth";
import {
  useProjects, useProjectImages, useProjectFlavors, useProjectNetworks, useNetworks, useNetwork, useNovaKeypairs,
} from "@/lib/iaas-api";
import {
  useClusterTemplates, useCreateTemplate, useUpdateTemplate, useDeleteTemplate,
  type ClusterTemplate, type ClusterTemplateInput,
} from "@/lib/paas-api";

export default function TemplatesPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const templates = useClusterTemplates();
  const { data: projData } = useProjects();
  const { data: netData } = useNetworks();
  const confirm = useConfirm();
  const del = useDeleteTemplate();
  const [q, setQ] = React.useState("");
  const [projFilter, setProjFilter] = React.useState("all");
  const [pubFilter, setPubFilter] = React.useState("all"); // all | public | private
  const [editing, setEditing] = React.useState<ClusterTemplate | "new" | null>(null);

  const projName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const p of projData?.items ?? []) m[p.id] = p.name;
    return (id: string) => m[id] || id.slice(0, 8) || "—";
  }, [projData]);
  // 외부 네트워크 — ID 로 저장된 값은 이름으로 보인다(예전 템플릿은 이름 그대로)
  const netName = React.useMemo(() => {
    const m: Record<string, string> = {};
    for (const n of netData?.items ?? []) m[n.id] = n.name;
    return (v: string) => m[v] || v;
  }, [netData]);
  // 프로젝트 필터 — 템플릿이 있는 프로젝트만
  const projOptions = React.useMemo(() => {
    const ids = Array.from(new Set((templates.data ?? []).map((t) => t.project_id).filter((id): id is string => !!id)));
    return ids.map((id) => ({ id, name: projName(id) })).sort((a, b) => a.name.localeCompare(b.name));
  }, [templates.data, projName]);

  if (!isAdmin) {
    return <Card><div className="py-10 text-center text-sm text-muted-foreground">관리자 전용 화면입니다.</div></Card>;
  }

  const items = (templates.data ?? []).filter((t) =>
    (!q || t.name.toLowerCase().includes(q.toLowerCase()))
    && (projFilter === "all" || t.project_id === projFilter)
    && (pubFilter === "all" || t.public === (pubFilter === "public")));

  async function onDelete(t: ClusterTemplate) {
    if (!(await confirm({ title: "템플릿 삭제", destructive: true, confirmLabel: "삭제", description: `'${t.name}' 템플릿을 삭제할까요? 이 템플릿으로 만든 클러스터에는 영향이 없습니다.` }))) return;
    del.mutate(t.id, { onSuccess: () => toast.success(`${t.name} 삭제됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
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
          <Select value={pubFilter} onValueChange={setPubFilter}>
            <SelectTrigger className="w-36"><SelectValue placeholder="공용 상태" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">공용 상태</SelectItem>
              <SelectItem value="public">공용</SelectItem>
              <SelectItem value="private">비공용</SelectItem>
            </SelectContent>
          </Select>
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="템플릿 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-56 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {items.length}개</span>
          <div className="ml-auto flex items-center gap-2">
            <RefreshControls queryKeys={[["paas-templates"]]} />
            <Button size="sm" onClick={() => setEditing("new")}><Plus className="size-3.5" /> 템플릿 생성</Button>
          </div>
        </div>
      </Card>

      <Card>
        {templates.isLoading ? (
          <Skeleton className="h-64" />
        ) : templates.isError ? (
          <div className="py-10 text-center text-sm text-rose-500">템플릿 조회 실패 — MNT-PAAS 응답 오류.</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>템플릿</TableHead>
                <TableHead>플레이버(Control Plane / Worker)</TableHead>
                <TableHead>네트워크</TableHead>
                <TableHead>프로젝트</TableHead>
                <TableHead>공용</TableHead>
                <TableHead>LB/FIP</TableHead>
                <TableHead className="w-24" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((t) => (
                <TableRow key={t.id} className="cursor-pointer" onClick={() => setEditing(t)}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Boxes className="size-3.5 text-brand-indigo-600" />
                      <span className="max-w-[240px] truncate font-mono text-sm font-semibold">{t.name}</span>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{t.master_flavor_id || t.flavor_id || "—"} / {t.flavor_id || "—"}</TableCell>
                  <TableCell className="font-mono text-xs">{t.external_network_id ? netName(t.external_network_id) : "—"}</TableCell>
                  <TableCell className="text-xs font-medium">{t.project_id ? projName(t.project_id) : "—"}</TableCell>
                  <TableCell>{t.public ? <Badge variant="success" className="text-[10px]">public</Badge> : <span className="text-muted-foreground">—</span>}</TableCell>
                  <TableCell className="font-mono text-[11px] text-muted-foreground">
                    {[t.master_lb_enabled ? "LB" : null, t.floating_ip_enabled ? "FIP" : null].filter(Boolean).join(" · ") || "—"}
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <div className="flex items-center gap-0.5">
                      <Button variant="ghost" size="icon-sm" title="수정" onClick={() => setEditing(t)}><Pencil className="size-3.5" /></Button>
                      <Button variant="ghost" size="icon-sm" title="복제" onClick={() => setEditing({ ...t, name: `${t.name}-copy` })}><Copy className="size-3.5" /></Button>
                      <Button variant="ghost" size="icon-sm" title="삭제" disabled={del.isPending} onClick={() => onDelete(t)}><Trash2 className="size-3.5 text-rose-600" /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
              {items.length === 0 && (
                <TableRow><TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">템플릿이 없습니다.</TableCell></TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </Card>

      {editing && <TemplateDialog base={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** base=null → 생성, base=템플릿 → 수정(복제도 base 에 name 바꿔 넘기면 '수정'이 아니라 '생성'이 되게 isCopy 로 구분) */
function TemplateDialog({ base, onClose }: { base: ClusterTemplate | null; onClose: () => void }) {
  const create = useCreateTemplate();
  const update = useUpdateTemplate();
  const projects = useProjects();
  const keypairs = useNovaKeypairs();

  // base 에 id 가 있고 updatedAt 등이 있으면 '수정', 복제는 id 가 있어도 이름이 -copy → 생성으로.
  const isCopy = !!base && base.name.endsWith("-copy");
  const isEdit = !!base && !isCopy;

  const [f, setF] = React.useState<ClusterTemplateInput>(() => ({
    name: base?.name ?? "",
    project_id: base?.project_id ?? "",
    coe: base?.coe ?? "kubernetes",
    image_id: base?.image_id ?? "",
    flavor_id: base?.flavor_id ?? "",
    master_flavor_id: base?.master_flavor_id ?? "",
    keypair_id: base?.keypair_id ?? "",
    external_network_id: base?.external_network_id ?? "",
    fixed_network: base?.fixed_network ?? "",
    fixed_subnet: base?.fixed_subnet ?? "",
    dns_nameserver: base?.dns_nameserver ?? "",
    network_driver: base?.network_driver ?? "calico",
    volume_driver: base?.volume_driver ?? "",
    docker_storage_driver: base?.docker_storage_driver ?? "overlay2",
    docker_volume_size: base?.docker_volume_size ?? null,
    server_type: base?.server_type ?? "vm",
    public: base?.public ?? false,
    hidden: base?.hidden ?? false,
    tls_disabled: base?.tls_disabled ?? false,
    master_lb_enabled: base?.master_lb_enabled ?? true,
    floating_ip_enabled: base?.floating_ip_enabled ?? true,
  }));
  const [labelsText, setLabelsText] = React.useState(
    Object.entries(base?.labels ?? {}).map(([k, v]) => `${k}=${v}`).join("\n"),
  );
  const set = (k: keyof ClusterTemplateInput, v: unknown) => setF((p) => ({ ...p, [k]: v }));
  // 소유 프로젝트가 쓸 수 있는 자원 — 이미지(공개·소유·공유받음) · 플레이버(공개·접근 허용) · 네트워크(소유·공유·외부)
  const project = f.project_id || undefined;
  const images = useProjectImages(project);
  const flavors = useProjectFlavors(project);
  const networks = useProjectNetworks(project);
  // 네트워크 · 서브넷은 ID 로 저장 — 이름은 겹칠 수 있고 겹치면 Magnum 이 거절한다(Multiple … same name).
  // 외부 네트워크도 ID — MNT-CORE 의 Floating IP 확인이 라우터 게이트웨이의 네트워크 ID 와 비교한다
  const extNets = (networks.data ?? []).filter((n) => n.kind === "external");
  const extNet = pickByIdOrName(extNets, f.external_network_id);
  const fixedNets = (networks.data ?? []).filter((n) => n.kind !== "external");
  const fixedNet = pickByIdOrName(fixedNets, f.fixed_network);
  // 프로젝트를 바꾸면 그 프로젝트에 매인 값(이미지·플레이버·고정 네트워크)을 비운다 — 외부망은 프로젝트와 무관
  const setProject = (v: string) => setF((p) => ({
    ...p, project_id: v, image_id: "", flavor_id: "", master_flavor_id: "", fixed_network: "", fixed_subnet: "",
  }));
  const pending = create.isPending || update.isPending;
  // Magnum 쿠버네티스 템플릿 필수값 — 미입력이면 생성/저장 불가(탭마다 흩어져 있어 안내도 표시)
  const missing = [
    !f.name && "이름", !f.project_id && "프로젝트", !f.image_id && "이미지",
    !f.flavor_id && "Worker 플레이버", !f.external_network_id && "외부 네트워크",
  ].filter(Boolean) as string[];

  const submit = () => {
    const labels: Record<string, string> = {};
    for (const line of labelsText.split("\n")) {
      const i = line.indexOf("="); if (i < 0) continue;
      const k = line.slice(0, i).trim(); if (k) labels[k] = line.slice(i + 1).trim();
    }
    const body: ClusterTemplateInput = { ...f, labels };
    const done = { onSuccess: () => { toast.success(`${f.name} ${isEdit ? "수정" : "생성"}됨`); onClose(); }, onError: (e: Error) => toast.error(`${isEdit ? "수정" : "생성"} 실패`, { description: String(e.message) }) };
    if (isEdit) update.mutate({ id: base!.id, body }, done);
    else create.mutate(body, done);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{isEdit ? "템플릿 수정" : isCopy ? "템플릿 복제" : "템플릿 생성"}</DialogTitle>
          <DialogDescription>클러스터 생성 설계도(Magnum ClusterTemplate).</DialogDescription>
        </DialogHeader>

        <Tabs defaultValue="info">
          <TabsList>
            <TabsTrigger value="info">정보</TabsTrigger>
            <TabsTrigger value="node">노드</TabsTrigger>
            <TabsTrigger value="network">네트워크</TabsTrigger>
            <TabsTrigger value="labels">라벨</TabsTrigger>
          </TabsList>

          <TabsContent value="info" className="space-y-3 pt-3">
            <Field label="이름 *"><Input value={f.name ?? ""} onChange={(e) => set("name", e.target.value)} placeholder="k8s-template" /></Field>
            <Field label="프로젝트 *">
              {/* 소유 프로젝트 — Magnum 은 만든 뒤 바꿀 수 없어 수정 때는 잠근다 */}
              <DropSelect value={f.project_id ?? ""} onChange={setProject} disabled={isEdit} placeholder="템플릿을 둘 프로젝트 선택"
                options={(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))} />
            </Field>
            <Field label="이미지 *">
              {/* 클러스터 템플릿은 os_distro 속성이 있는 이미지만 쓸 수 있다(Magnum 요구). 없는 이미지는 숨긴다. */}
              <div className="space-y-1">
                <DropSelect value={f.image_id ?? ""} onChange={(v) => set("image_id", v)}
                  placeholder={project ? "이미지 선택(os_distro 있는 이미지)" : "프로젝트 먼저 선택"}
                  options={(images.data ?? []).filter((i) => i.os).map((i) => ({ value: i.id, label: `${i.name} (${i.os})` }))} />
                {f.image_id && !(images.data ?? []).find((i) => i.id === f.image_id)?.kubeVersion && (
                  <p className="text-[10px] text-amber-700">이 이미지에 kube_version 속성이 없어 클러스터 생성이 실패할 수 있습니다. IaaS → 이미지에서 kube_version 을 추가하세요.</p>
                )}
              </div>
            </Field>
            <div className="flex items-center gap-6">
              <SwitchField label="public" checked={!!f.public} onChange={(v) => set("public", v)} />
              <SwitchField label="TLS 비활성" checked={!!f.tls_disabled} onChange={(v) => set("tls_disabled", v)} />
              <SwitchField label="숨김" checked={!!f.hidden} onChange={(v) => set("hidden", v)} />
            </div>
          </TabsContent>

          <TabsContent value="node" className="space-y-3 pt-3">
            <Field label="Control Plane 플레이버">
              <DropSelect value={f.master_flavor_id ?? ""} onChange={(v) => set("master_flavor_id", v)}
                placeholder={project ? "플레이버 선택" : "프로젝트 먼저 선택"}
                options={(flavors.data ?? []).map((x) => ({ value: x.name, label: `${x.name} (${x.vcpus}vCPU·${Math.round(x.ramMb / 1024)}GB)` }))} />
            </Field>
            <Field label="Worker 플레이버 *">
              <DropSelect value={f.flavor_id ?? ""} onChange={(v) => set("flavor_id", v)}
                placeholder={project ? "플레이버 선택" : "프로젝트 먼저 선택"}
                options={(flavors.data ?? []).map((x) => ({ value: x.name, label: `${x.name} (${x.vcpus}vCPU·${Math.round(x.ramMb / 1024)}GB)` }))} />
            </Field>
            <Field label="키페어(선택)">
              <DropSelect value={f.keypair_id ?? ""} onChange={(v) => set("keypair_id", v)} placeholder="Nova 키페어 선택(비우면 노드 SSH 미설정)"
                options={(keypairs.data ?? []).map((k) => ({ value: k.name, label: k.name }))} />
            </Field>
          </TabsContent>

          <TabsContent value="network" className="space-y-3 pt-3">
            <Field label="외부 네트워크 *">
              <DropSelect value={extNet?.id ?? ""} onChange={(v) => set("external_network_id", v)}
                placeholder={project ? "네트워크 선택" : "프로젝트 먼저 선택"}
                options={extNets.map((n) => ({ value: n.id, label: n.name }))} />
            </Field>
            <Field label="네트워크 드라이버"><Input value={f.network_driver ?? ""} onChange={(e) => set("network_driver", e.target.value)} className="font-mono text-xs" placeholder="calico" /></Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="고정 네트워크(선택)">
                <DropSelect value={fixedNet?.id ?? ""} onChange={(v) => { set("fixed_network", v); set("fixed_subnet", ""); }}
                  placeholder={project ? "네트워크 선택" : "프로젝트 먼저 선택"}
                  options={fixedNets.map((n) => ({ value: n.id, label: n.name }))} />
              </Field>
              <Field label="고정 서브넷(선택)">
                <FixedSubnetSelect networkId={fixedNet?.id} value={f.fixed_subnet ?? ""} onChange={(v) => set("fixed_subnet", v)} />
              </Field>
            </div>
            <Field label="DNS"><Input value={f.dns_nameserver ?? ""} onChange={(e) => set("dns_nameserver", e.target.value)} className="font-mono text-xs" placeholder="8.8.8.8" /></Field>
            <div className="flex items-center gap-6">
              <SwitchField label="마스터 LB" checked={!!f.master_lb_enabled} onChange={(v) => set("master_lb_enabled", v)} />
              <SwitchField label="Floating IP" checked={!!f.floating_ip_enabled} onChange={(v) => set("floating_ip_enabled", v)} />
            </div>
          </TabsContent>

          <TabsContent value="labels" className="space-y-2 pt-3">
            <Label>라벨 (KEY=VALUE, 한 줄에 하나)</Label>
            <textarea
              value={labelsText} onChange={(e) => setLabelsText(e.target.value)} rows={6}
              placeholder={"octavia_provider=ovn\nkube_dashboard_enabled=false"}
              className="w-full rounded-md border border-border bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
          </TabsContent>
        </Tabs>

        <DialogFooter className="items-center gap-2 sm:justify-between">
          {missing.length > 0
            ? <span className="text-[11px] text-amber-700">필수 미입력: {missing.join(", ")}</span>
            : <span />}
          <div className="flex gap-2">
            <Button variant="outline" onClick={onClose}>취소</Button>
            <Button disabled={pending || missing.length > 0} onClick={submit}>{isEdit ? "저장" : "생성"}</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}
function SwitchField({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <Switch checked={checked} onCheckedChange={onChange} /> {label}
    </label>
  );
}
function DropSelect({ value, onChange, options, placeholder, disabled }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; placeholder: string; disabled?: boolean }) {
  return (
    <Select value={value || undefined} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger><SelectValue placeholder={placeholder} /></SelectTrigger>
      <SelectContent>
        {options.map((o) => <SelectItem key={o.value} value={o.value} className="font-mono text-xs">{o.label}</SelectItem>)}
      </SelectContent>
    </Select>
  );
}
// 고정 서브넷 — 선택한 고정 네트워크의 상세(서브넷 목록)를 받아 드롭다운(값 = 서브넷 ID). 네트워크 미선택 시 비활성.
function FixedSubnetSelect({ networkId, value, onChange }: { networkId: string | undefined; value: string; onChange: (v: string) => void }) {
  const net = useNetwork(networkId);
  const subnets = net.data?.subnets ?? [];
  return (
    <DropSelect value={pickByIdOrName(subnets, value)?.id ?? ""} onChange={onChange} placeholder={networkId ? "서브넷 선택" : "네트워크 먼저 선택"}
      options={subnets.map((s) => ({ value: s.id, label: `${s.name} (${s.cidr})` }))} />
  );
}
/** 값이 ID 면 그 항목, 예전 템플릿의 이름 값이면 그 이름이 하나뿐일 때만 그 항목(겹치면 고르지 않은 것으로 보인다). */
function pickByIdOrName<T extends { id: string; name: string }>(items: T[], v: string | null | undefined): T | undefined {
  if (!v) return undefined;
  const named = items.filter((x) => x.name === v);
  return items.find((x) => x.id === v) ?? (named.length === 1 ? named[0] : undefined);
}
