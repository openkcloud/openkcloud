"use client";
/**
 * 시스템 설정 — 메뉴 관리(관리자 전용).
 * mnu_menu 원장 CRUD + 형제 내 순서변경 + 노출 역할(mnu_role_menu). 저장 즉시 사이드바 반영.
 */
import * as React from "react";
import { Plus, Pencil, Trash2, ChevronUp, ChevronDown, FolderTree } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { useConfirm } from "@/components/ui/confirm";
import { useAuth } from "@/lib/auth";
import {
  useAllMenus, useCreateMenu, useUpdateMenu, useDeleteMenu, useReorderMenus,
  type AdminMenu, type AdminMenuInput,
} from "@/lib/menu-api";

const TYPE_LABEL: Record<string, string> = { section: "섹션", menu: "메뉴", submenu: "하위" };
const ALL_ROLES = ["admin", "user"] as const;

export default function SettingsPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const menus = useAllMenus();
  const reorder = useReorderMenus();
  const del = useDeleteMenu();
  const confirm = useConfirm();
  const [edit, setEdit] = React.useState<AdminMenu | "new" | null>(null);

  const items = menus.data ?? [];
  const byParent = React.useMemo(() => {
    const m = new Map<number | null, AdminMenu[]>();
    for (const it of items) {
      const k = it.parent_id;
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(it);
    }
    for (const arr of m.values()) arr.sort((a, b) => a.sort_order - b.sort_order || a.id - b.id);
    return m;
  }, [items]);

  if (!isAdmin) {
    return (
      <div className="space-y-6">
        <PageHeader title="시스템 설정" description="플랫폼 운영 설정" />
        <Card><div className="py-10 text-center text-sm text-muted-foreground">관리자 전용 화면입니다.</div></Card>
      </div>
    );
  }

  function move(it: AdminMenu, dir: -1 | 1) {
    const sibs = byParent.get(it.parent_id) ?? [];
    const i = sibs.findIndex((s) => s.id === it.id);
    const j = i + dir;
    if (j < 0 || j >= sibs.length) return;
    const a = sibs[i], b = sibs[j];
    reorder.mutate(
      [{ id: a.id, sort_order: b.sort_order }, { id: b.id, sort_order: a.sort_order }],
      { onError: (e) => toast.error("순서 변경 실패", { description: String((e as Error).message) }) },
    );
  }

  async function onDelete(it: AdminMenu) {
    const kids = byParent.get(it.id)?.length ?? 0;
    if (!(await confirm({
      title: "메뉴 삭제", destructive: true, confirmLabel: "삭제",
      description: `'${it.label}'${kids ? ` 및 하위 ${kids}개` : ""} 를 삭제할까요? 사이드바에서 즉시 사라집니다.`,
    }))) return;
    del.mutate(it.id, { onSuccess: () => toast.success(`${it.label} 삭제됨`), onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
  }

  function renderRows(parentId: number | null, depth: number): React.ReactNode[] {
    const sibs = byParent.get(parentId) ?? [];
    return sibs.flatMap((it, i) => [
      <Row
        key={it.id}
        it={it} depth={depth}
        first={i === 0} last={i === sibs.length - 1}
        onUp={() => move(it, -1)} onDown={() => move(it, 1)}
        onEdit={() => setEdit(it)} onDelete={() => onDelete(it)}
      />,
      ...renderRows(it.id, depth + 1),
    ]);
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="시스템 설정"
        description="LNB 메뉴 정의 · 순서 · 역할별 노출을 관리합니다."
        actions={<Button size="sm" onClick={() => setEdit("new")}><Plus className="size-3.5" /> 메뉴 추가</Button>}
      />

      <Card>
        {menus.isLoading ? (
          <Skeleton className="h-96" />
        ) : menus.isError ? (
          <div className="py-10 text-center text-sm text-rose-500">메뉴 조회 실패 — MNT-CORE 응답 오류.</div>
        ) : (
          <div className="divide-y divide-border">
            <div className="flex items-center gap-3 px-4 py-2 text-[11px] font-medium text-muted-foreground">
              <span className="flex-1">메뉴 (라벨 · 코드)</span>
              <span className="w-16">유형</span>
              <span className="w-28">경로</span>
              <span className="w-24">노출 역할</span>
              <span className="w-16 text-center">활성</span>
              <span className="w-28 text-right">관리</span>
            </div>
            {renderRows(null, 0)}
          </div>
        )}
      </Card>

      {edit && <MenuDialog base={edit === "new" ? null : edit} allMenus={items} onClose={() => setEdit(null)} />}
    </div>
  );
}

function Row({
  it, depth, first, last, onUp, onDown, onEdit, onDelete,
}: {
  it: AdminMenu; depth: number; first: boolean; last: boolean;
  onUp: () => void; onDown: () => void; onEdit: () => void; onDelete: () => void;
}) {
  const update = useUpdateMenu();
  return (
    <div className={`flex items-center gap-3 px-4 py-2 text-sm hover:bg-secondary/40 ${!it.is_active ? "opacity-50" : ""}`}>
      <div className="flex flex-1 items-center gap-2" style={{ paddingLeft: depth * 20 }}>
        {it.menu_type === "section" ? <FolderTree className="size-3.5 text-brand-indigo-600" /> : <span className="w-3.5" />}
        <span className={it.menu_type === "section" ? "font-semibold" : ""}>{it.label}</span>
        <span className="font-mono text-[11px] text-muted-foreground">{it.code}</span>
      </div>
      <span className="w-16"><Badge variant="outline" className="text-[10px]">{TYPE_LABEL[it.menu_type]}</Badge></span>
      <span className="w-28 truncate font-mono text-[11px] text-muted-foreground">{it.path || "—"}</span>
      <span className="w-24">
        <div className="flex gap-0.5">
          {it.roles.length === 0 ? <span className="text-[11px] text-muted-foreground">없음</span> :
            it.roles.map((r) => <Badge key={r} variant="secondary" className="text-[10px]">{r}</Badge>)}
        </div>
      </span>
      <span className="flex w-16 justify-center">
        <Switch
          checked={it.is_active}
          onCheckedChange={(v) => update.mutate({ id: it.id, body: { is_active: v } }, {
            onError: (e) => toast.error("변경 실패", { description: String((e as Error).message) }),
          })}
        />
      </span>
      <span className="flex w-28 items-center justify-end gap-0.5">
        <Button variant="ghost" size="icon-sm" title="위로" disabled={first} onClick={onUp}><ChevronUp className="size-3.5" /></Button>
        <Button variant="ghost" size="icon-sm" title="아래로" disabled={last} onClick={onDown}><ChevronDown className="size-3.5" /></Button>
        <Button variant="ghost" size="icon-sm" title="수정" onClick={onEdit}><Pencil className="size-3.5" /></Button>
        <Button variant="ghost" size="icon-sm" title="삭제" onClick={onDelete}><Trash2 className="size-3.5 text-rose-600" /></Button>
      </span>
    </div>
  );
}

function MenuDialog({ base, allMenus, onClose }: { base: AdminMenu | null; allMenus: AdminMenu[]; onClose: () => void }) {
  const create = useCreateMenu();
  const update = useUpdateMenu();
  const isEdit = !!base;
  const [f, setF] = React.useState<AdminMenuInput>(() => ({
    parent_id: base?.parent_id ?? null,
    menu_type: base?.menu_type ?? "submenu",
    code: base?.code ?? "",
    label: base?.label ?? "",
    path: base?.path ?? "",
    icon: base?.icon ?? "",
    is_active: base?.is_active ?? true,
    roles: base?.roles ?? ["admin"],
  }));
  const set = (k: keyof AdminMenuInput, v: unknown) => setF((p) => ({ ...p, [k]: v }));
  const pending = create.isPending || update.isPending;

  // 상위 후보: 섹션·메뉴만(하위의 하위는 허용 안 함), 자기 자신 제외
  const parents = allMenus.filter((m) => m.menu_type !== "submenu" && m.id !== base?.id);

  const toggleRole = (r: string) =>
    setF((p) => ({ ...p, roles: p.roles?.includes(r) ? p.roles.filter((x) => x !== r) : [...(p.roles ?? []), r] }));

  const submit = () => {
    const body: AdminMenuInput = { ...f, path: f.path || null, icon: f.icon || null };
    const done = { onSuccess: () => { toast.success(`${f.label} ${isEdit ? "수정" : "추가"}됨`); onClose(); }, onError: (e: Error) => toast.error(`${isEdit ? "수정" : "추가"} 실패`, { description: String(e.message) }) };
    if (isEdit) update.mutate({ id: base!.id, body }, done);
    else create.mutate(body, done);
  };

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? "메뉴 수정" : "메뉴 추가"}</DialogTitle>
          <DialogDescription>LNB 메뉴 정의. 저장 시 사이드바에 즉시 반영됩니다.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>유형</Label>
              <Select value={f.menu_type} onValueChange={(v) => set("menu_type", v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="section">섹션(최상위)</SelectItem>
                  <SelectItem value="menu">메뉴</SelectItem>
                  <SelectItem value="submenu">하위</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>상위 메뉴</Label>
              <Select value={f.parent_id ? String(f.parent_id) : "none"} onValueChange={(v) => set("parent_id", v === "none" ? null : Number(v))}>
                <SelectTrigger><SelectValue placeholder="없음" /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">없음(최상위)</SelectItem>
                  {parents.map((p) => <SelectItem key={p.id} value={String(p.id)}>{p.label}</SelectItem>)}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>코드 (고유)</Label>
              <Input value={f.code ?? ""} onChange={(e) => set("code", e.target.value)} placeholder="paas.custom" className="font-mono text-xs" disabled={isEdit} />
            </div>
            <div className="space-y-1.5">
              <Label>라벨</Label>
              <Input value={f.label ?? ""} onChange={(e) => set("label", e.target.value)} placeholder="메뉴 이름" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label>경로 (섹션은 비움)</Label>
              <Input value={f.path ?? ""} onChange={(e) => set("path", e.target.value)} placeholder="/paas/custom" className="font-mono text-xs" />
            </div>
            <div className="space-y-1.5">
              <Label>아이콘 (lucide 이름)</Label>
              <Input value={f.icon ?? ""} onChange={(e) => set("icon", e.target.value)} placeholder="Boxes" className="font-mono text-xs" />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>노출 역할</Label>
            <div className="flex gap-4">
              {ALL_ROLES.map((r) => (
                <label key={r} className="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={f.roles?.includes(r) ?? false} onChange={() => toggleRole(r)} className="size-4" /> {r}
                </label>
              ))}
            </div>
          </div>
          <label className="flex items-center justify-between rounded-md border border-border px-3 py-2">
            <span className="text-sm">활성</span>
            <Switch checked={!!f.is_active} onCheckedChange={(v) => set("is_active", v)} />
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button disabled={pending || !f.code || !f.label} onClick={submit}>{isEdit ? "저장" : "추가"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
