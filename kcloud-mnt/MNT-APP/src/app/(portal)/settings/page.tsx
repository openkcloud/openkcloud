"use client";
/**
 * 시스템 설정 — 허브. 하위 설정 기능으로 이동하는 진입 화면.
 * (메뉴 관리부터 시작, 배치·에이전트 등은 추후 하위로 추가)
 */
import Link from "next/link";
import { FolderTree, CalendarClock, Plug, ChevronRight } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useAuth } from "@/lib/auth";

interface SettingEntry {
  label: string;
  description: string;
  href?: string;
  icon: typeof FolderTree;
  adminOnly?: boolean;
  soon?: boolean;
}

const ENTRIES: SettingEntry[] = [
  { label: "메뉴 관리", description: "LNB 메뉴 정의·순서·역할별 노출 관리", href: "/settings/menus", icon: FolderTree, adminOnly: true },
  { label: "배치 관리", description: "배치 스케줄 등록·실행 이력", icon: CalendarClock, soon: true },
  { label: "에이전트 연결", description: "OpenStack(Nova·Neutron·Cinder) 에이전트 상태", icon: Plug, soon: true },
];

export default function SettingsPage() {
  const isAdmin = useAuth((s) => s.user?.role === "admin");
  const entries = ENTRIES.filter((e) => !e.adminOnly || isAdmin);

  return (
    <div className="space-y-6">
      <PageHeader title="시스템 설정" description="플랫폼 운영 설정 — 메뉴·배치·에이전트" />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {entries.map((e) => {
          const inner = (
            <Card className={`flex items-start gap-3 p-4 transition-colors ${e.href ? "cursor-pointer hover:border-primary/50 hover:bg-secondary/40" : "opacity-60"}`}>
              <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-brand-indigo-100 text-brand-indigo-700">
                <e.icon className="size-4" />
              </div>
              <div className="min-w-0 flex-1 space-y-0.5">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold">{e.label}</span>
                  {e.soon && <Badge variant="outline" className="text-[10px]">준비 중</Badge>}
                  {e.href && <ChevronRight className="ml-auto size-4 text-muted-foreground" />}
                </div>
                <p className="text-xs text-muted-foreground">{e.description}</p>
              </div>
            </Card>
          );
          return e.href ? <Link key={e.label} href={e.href}>{inner}</Link> : <div key={e.label}>{inner}</div>;
        })}
      </div>
    </div>
  );
}
