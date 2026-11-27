"use client";
import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { useAuth } from "@/lib/auth";
import { getNavByRole, type NavItem } from "@/lib/nav";
import { useMenusFromCore } from "@/lib/menu-api";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";

export function AppSidebar() {
  const user = useAuth((s) => s.user);
  const pathname = usePathname();

  // MNT-CORE 메뉴 API 우선 — 실패·로딩 중엔 정적 nav.ts 폴백(백엔드 없이도 기존대로 동작)
  const { data: coreSections } = useMenusFromCore(user?.role);
  const sections = React.useMemo(
    () => coreSections ?? (user ? getNavByRole(user.role) : []),
    [coreSections, user],
  );
  const activeHref = React.useMemo(
    () => bestMatch(normalizePath(pathname), collectTargets(sections.flatMap((s) => s.items))),
    [pathname, sections],
  );

  return (
    <aside className="hidden h-screen w-[260px] shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground md:flex">
      <Link
        href="/dashboard"
        className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5 transition-colors hover:bg-sidebar-accent/30"
        title="홈으로"
      >
        <div className="flex size-9 items-center justify-center rounded-lg bg-gradient-to-br from-brand-mint-400 to-brand-indigo-400 font-bold text-sidebar-primary-foreground shadow-md">
          K
        </div>
        <div className="flex flex-col leading-tight">
          <span className="text-sm font-semibold text-white">openKcloud</span>
          <span className="text-[10px] uppercase tracking-wider text-sidebar-foreground/70">
            AI 반도체 클라우드 CMP
          </span>
        </div>
      </Link>
      <ScrollArea className="flex-1">
        <nav className="flex flex-col gap-1 p-3">
          {sections.map((section) => (
            <div key={section.label} className="mb-3">
              <div className="px-3 py-2 text-[10px] font-semibold uppercase tracking-wider text-sidebar-foreground/50">
                {section.label}
              </div>
              <div className="flex flex-col gap-0.5">
                {section.items.map((item) => (
                  <SidebarItem key={item.href} item={item} activeHref={activeHref} />
                ))}
              </div>
            </div>
          ))}
        </nav>
      </ScrollArea>
    </aside>
  );
}

// trailingSlash:true 환경에서도 활성 판정이 되도록 경로 끝 슬래시 정규화
function normalizePath(p: string) {
  return p.length > 1 && p.endsWith("/") ? p.slice(0, -1) : p;
}

// 모든 nav 경로(항목·자식·match) 수집
function collectTargets(items: NavItem[], out: string[] = []): string[] {
  for (const it of items) {
    out.push(normalizePath(it.href));
    if (it.match) for (const m of it.match) out.push(normalizePath(m));
    if (it.children) collectTargets(it.children, out);
  }
  return out;
}

// 현재 경로에 매칭되는 nav 경로 중 가장 구체적인(긴) 것 하나만 활성 — 상위가 하위 접두사라 중복 active 되는 문제 방지
function bestMatch(here: string, targets: string[]): string | null {
  let best: string | null = null;
  for (const t of targets) {
    if ((here === t || (t !== "/" && here.startsWith(t + "/"))) && (best === null || t.length > best.length)) {
      best = t;
    }
  }
  return best;
}

function SidebarItem({
  item,
  activeHref,
  depth = 0,
}: {
  item: NavItem;
  activeHref: string | null;
  depth?: number;
}) {
  const matchesActive = (it: { href: string; match?: string[] }) =>
    normalizePath(it.href) === activeHref ||
    !!it.match?.some((m) => normalizePath(m) === activeHref);
  const hasChildren = !!item.children?.length;
  // 리프는 자신이 활성, 부모는 자식 중 활성이 있을 때(섹션 강조·자동 펼침)
  const isActive = hasChildren ? !!item.children?.some(matchesActive) : matchesActive(item);
  const [open, setOpen] = React.useState(isActive);
  React.useEffect(() => {
    if (isActive) setOpen(true);
  }, [isActive]);

  const Icon = item.icon;

  if (hasChildren) {
    return (
      <div>
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className={cn(
            "group flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-[13px] font-medium transition-colors",
            "text-sidebar-foreground hover:bg-sidebar-accent hover:text-white",
            isActive && "text-white",
          )}
          style={{ paddingLeft: `${0.75 + depth * 0.75}rem` }}
        >
          <Icon className="size-4 shrink-0" />
          <span className="flex-1 text-left">{item.label}</span>
          {item.badge && (
            <Badge
              variant="success"
              className="h-4 bg-brand-mint-500/20 px-1.5 text-[9px] text-brand-mint-200"
            >
              {item.badge}
            </Badge>
          )}
          <ChevronRight
            className={cn(
              "size-3.5 shrink-0 transition-transform",
              open && "rotate-90",
            )}
          />
        </button>
        {open && (
          <div className="mt-0.5 flex flex-col gap-0.5">
            {item.children!.map((child) => (
              <Link
                key={child.href + child.label}
                href={child.href}
                className={cn(
                  "relative flex items-center gap-2.5 rounded-md py-1.5 pl-10 pr-3 text-[12.5px] transition-colors",
                  "text-sidebar-foreground/70 hover:bg-sidebar-accent/70 hover:text-white",
                  matchesActive(child) &&
                    "bg-sidebar-accent font-semibold text-white before:absolute before:left-0 before:top-1/2 before:h-5 before:w-[3px] before:-translate-y-1/2 before:rounded-r-full before:bg-sidebar-primary",
                )}
              >
                {child.label}
              </Link>
            ))}
          </div>
        )}
      </div>
    );
  }

  return (
    <Link
      href={item.href}
      className={cn(
        "group relative flex items-center gap-2.5 rounded-md px-3 py-2 text-[13px] font-medium transition-colors",
        "text-sidebar-foreground hover:bg-sidebar-accent hover:text-white",
        isActive &&
          "bg-sidebar-accent font-semibold text-white before:absolute before:left-0 before:top-1/2 before:h-5 before:w-[3px] before:-translate-y-1/2 before:rounded-r-full before:bg-sidebar-primary",
      )}
    >
      <Icon className="size-4 shrink-0" />
      <span className="flex-1">{item.label}</span>
      {item.badge && (
        <Badge
          variant="success"
          className="h-4 bg-brand-mint-500/20 px-1.5 text-[9px] text-brand-mint-200"
        >
          {item.badge}
        </Badge>
      )}
    </Link>
  );
}
