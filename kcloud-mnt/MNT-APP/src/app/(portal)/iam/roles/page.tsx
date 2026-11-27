"use client";
import * as React from "react";
import { ShieldCheck, Plus, Trash2, Save, Undo2, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";

interface Permission {
  id: string;
  label: string;
  category: string;
  admin: boolean;
  user: boolean;
  custom?: boolean;
}

const INITIAL_PERMS: Permission[] = [
  { id: "acc.read", label: "AI반도체 자원 조회", category: "AI 인프라", admin: true, user: true },
  { id: "acc.assign", label: "AI반도체 자원 할당·해제", category: "AI 인프라", admin: true, user: true },
  { id: "vm.lifecycle", label: "VM 라이프사이클 제어", category: "IaaS", admin: true, user: true },
  { id: "vm.migrate", label: "VM 마이그레이션", category: "IaaS", admin: true, user: false },
  { id: "k8s.deploy", label: "K8s 워크로드 배포", category: "PaaS", admin: true, user: true },
  { id: "k8s.cluster", label: "K8s 클러스터 생성/삭제", category: "PaaS", admin: true, user: false },
  { id: "kf.profile", label: "Kubeflow Profile 생성", category: "AI 개발환경", admin: true, user: false },
  { id: "kf.notebook", label: "Notebook 생성·실행", category: "AI 개발환경", admin: true, user: true },
  { id: "kf.serve", label: "추론 서비스 게시 (KServe)", category: "AI 개발환경", admin: true, user: true },
  { id: "tenant.quota", label: "테넌트 쿼터 변경", category: "운영", admin: true, user: false },
  { id: "iam.manage", label: "사용자·역할 관리 (IAM)", category: "운영", admin: true, user: false },
  { id: "iam.apikey", label: "API Key 발급", category: "운영", admin: true, user: true },
  { id: "audit.read", label: "감사 로그 조회", category: "운영", admin: true, user: false },
  { id: "sys.config", label: "시스템 설정 변경", category: "운영", admin: true, user: false },
];

const ROLES = [
  { key: "admin" as const, name: "관리자", desc: "플랫폼 전체 관리·정책·사용자", color: "indigo" },
  { key: "user" as const, name: "유저", desc: "본인 프로젝트 내 자원 활용", color: "mint" },
];

const CATEGORY_ORDER = ["AI 인프라", "IaaS", "PaaS", "AI 개발환경", "운영"];

export default function RolesPage() {
  const [perms, setPerms] = React.useState<Permission[]>(INITIAL_PERMS);
  const [originalPerms, setOriginalPerms] = React.useState<Permission[]>(INITIAL_PERMS);
  const [addOpen, setAddOpen] = React.useState(false);

  const dirty = React.useMemo(
    () => JSON.stringify(perms) !== JSON.stringify(originalPerms),
    [perms, originalPerms],
  );

  const toggle = (id: string, role: "admin" | "user") => {
    setPerms((prev) => prev.map((p) => (p.id === id ? { ...p, [role]: !p[role] } : p)));
  };

  const save = () => {
    setOriginalPerms(perms);
    toast.success("권한 매트릭스 저장됨", {
      description: `${perms.length}개 권한 · 변경사항이 다음 로그인부터 적용된다.`,
    });
  };

  const revert = () => {
    setPerms(originalPerms);
    toast.info("변경사항 되돌림");
  };

  const remove = (id: string) => {
    setPerms((prev) => prev.filter((p) => p.id !== id));
    toast.success("권한 항목 삭제됨");
  };

  const add = (p: Permission) => {
    setPerms((prev) => [...prev, p]);
    toast.success(`커스텀 권한 '${p.label}' 추가됨`);
  };

  const groupedPerms = CATEGORY_ORDER.map((cat) => ({
    category: cat,
    items: perms.filter((p) => p.category === cat),
  })).filter((g) => g.items.length > 0);

  const customCategory = perms.filter((p) => p.custom && !CATEGORY_ORDER.includes(p.category));
  if (customCategory.length > 0) {
    groupedPerms.push({ category: "커스텀", items: customCategory });
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 md:grid-cols-2">
        {ROLES.map((r) => {
          const grantedCount = perms.filter((p) => p[r.key]).length;
          return (
            <Card key={r.key}>
              <CardContent className="space-y-2 p-5">
                <div className="flex items-center gap-2">
                  <ShieldCheck className={`size-5 ${r.color === "indigo" ? "text-brand-indigo-600" : "text-brand-mint-600"}`} />
                  <span className="font-semibold">{r.name}</span>
                  <Badge variant="ghost" className="ml-auto font-mono text-[10px]">
                    {grantedCount} / {perms.length} 권한
                  </Badge>
                </div>
                <div className="text-xs text-muted-foreground">{r.desc}</div>
                <Badge variant="outline" className="font-mono text-[10px]">role: {r.key}</Badge>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <div>
            <CardTitle>권한 매트릭스</CardTitle>
            <CardDescription>
              토글로 역할별 권한을 조정한다. 변경 후 저장하면 다음 세션부터 반영. — SFR.MNT.006/016
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            {dirty && (
              <>
                <Badge variant="warning" className="gap-1 text-[10px]">
                  <Sparkles className="size-2.5" /> 변경사항 있음
                </Badge>
                <Button variant="outline" size="sm" onClick={revert}>
                  <Undo2 className="size-3.5" /> 되돌리기
                </Button>
                <Button size="sm" onClick={save}>
                  <Save className="size-3.5" /> 저장
                </Button>
              </>
            )}
            <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
              <Plus className="size-3.5" /> 권한 추가
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-border bg-secondary/30 text-xs text-muted-foreground">
                <tr>
                  <th className="w-8 px-3 py-3" />
                  <th className="px-4 py-3 text-left font-medium">권한</th>
                  {ROLES.map((r) => (
                    <th key={r.key} className="w-24 px-4 py-3 text-center font-medium">
                      {r.name}
                    </th>
                  ))}
                  <th className="w-12 px-3 py-3" />
                </tr>
              </thead>
              <tbody>
                {groupedPerms.map((g) => (
                  <React.Fragment key={g.category}>
                    <tr className="border-t border-border bg-secondary/20">
                      <td colSpan={ROLES.length + 3} className="px-4 py-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
                        {g.category}
                      </td>
                    </tr>
                    {g.items.map((p) => (
                      <tr key={p.id} className="border-t border-border">
                        <td className="px-3 py-2.5" />
                        <td className="px-4 py-2.5">
                          <div className="flex items-center gap-2">
                            <span>{p.label}</span>
                            {p.custom && <Badge variant="ghost" className="text-[9px]">custom</Badge>}
                          </div>
                          <div className="font-mono text-[10px] text-muted-foreground">{p.id}</div>
                        </td>
                        {ROLES.map((r) => (
                          <td key={r.key} className="px-4 py-2.5 text-center">
                            <Switch checked={p[r.key]} onCheckedChange={() => toggle(p.id, r.key)} />
                          </td>
                        ))}
                        <td className="px-3 py-2.5 text-center">
                          {p.custom && (
                            <Button variant="ghost" size="icon-sm" onClick={() => remove(p.id)}>
                              <Trash2 className="size-3.5 text-rose-600" />
                            </Button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </React.Fragment>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      <AddPermissionDialog open={addOpen} onClose={() => setAddOpen(false)} onAdd={add} existingIds={perms.map((p) => p.id)} />
    </div>
  );
}

function AddPermissionDialog({
  open, onClose, onAdd, existingIds,
}: {
  open: boolean;
  onClose: () => void;
  onAdd: (p: Permission) => void;
  existingIds: string[];
}) {
  const [id, setId] = React.useState("");
  const [label, setLabel] = React.useState("");
  const [category, setCategory] = React.useState("커스텀");
  const [admin, setAdmin] = React.useState(true);
  const [user, setUser] = React.useState(false);

  const idExists = existingIds.includes(id);

  const submit = () => {
    if (!id.trim() || !label.trim()) return;
    if (idExists) {
      toast.error("이미 존재하는 권한 ID입니다");
      return;
    }
    onAdd({ id: id.trim(), label: label.trim(), category, admin, user, custom: true });
    setId("");
    setLabel("");
    setCategory("커스텀");
    setAdmin(true);
    setUser(false);
    onClose();
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>권한 항목 추가</DialogTitle>
          <DialogDescription>
            표준 권한에 없는 커스텀 권한을 정의한다. ID는 소문자·점 표기 (예: <span className="font-mono text-xs">custom.action</span>).
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3 py-2">
          <div>
            <Label>권한 ID</Label>
            <Input
              value={id}
              onChange={(e) => setId(e.target.value.toLowerCase().replace(/[^a-z0-9.]/g, ""))}
              placeholder="custom.data.export"
              className="font-mono text-xs"
            />
            {idExists && <div className="mt-1 text-[11px] text-rose-600">이미 존재하는 ID</div>}
          </div>
          <div>
            <Label>표시 이름</Label>
            <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="데이터 대량 내보내기" />
          </div>
          <div>
            <Label>카테고리</Label>
            <Input value={category} onChange={(e) => setCategory(e.target.value)} placeholder="커스텀" />
          </div>
          <div className="space-y-2 rounded-lg border border-dashed border-border p-3">
            <div className="text-[11px] font-medium">기본 부여 대상</div>
            <div className="flex items-center gap-4">
              <label className="flex items-center gap-2 text-xs cursor-pointer">
                <Switch checked={admin} onCheckedChange={setAdmin} /> 관리자
              </label>
              <label className="flex items-center gap-2 text-xs cursor-pointer">
                <Switch checked={user} onCheckedChange={setUser} /> 유저
              </label>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>취소</Button>
          <Button onClick={submit} disabled={idExists || !id.trim() || !label.trim()}>
            추가
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
