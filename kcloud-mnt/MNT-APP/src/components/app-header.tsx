"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { Bell, ChevronDown, LogOut, Search, Settings, UserRound } from "lucide-react";
import { useAuth, roleLabel } from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { getNavByRole } from "@/lib/nav";
import type { Role } from "@/lib/auth";

function findCrumbs(pathname: string, role: Role | undefined) {
  const crumbs: { label: string; href: string }[] = [];
  if (!role) return crumbs;
  for (const section of getNavByRole(role)) {
    for (const item of section.items) {
      if (!(pathname === item.href || pathname.startsWith(item.href + "/"))) continue;
      if (item.children?.length) {
        // 그룹 항목은 하위가 실제로 매칭될 때만 추가 (bare 그룹 크럼 방지)
        const child = item.children.find((c) => pathname === c.href || pathname.startsWith(c.href + "/"));
        if (child) {
          crumbs.push({ label: item.label, href: item.href });
          crumbs.push({ label: child.label, href: child.href });
        }
      } else {
        crumbs.push({ label: item.label, href: item.href });
      }
    }
  }
  return crumbs;
}

export function AppHeader() {
  const pathname = usePathname();
  const user = useAuth((s) => s.user);
  const logout = useAuth((s) => s.logout);
  const crumbs = findCrumbs(pathname, user?.role);

  // 로그아웃 — 서버가 포털·Keycloak 세션을 끝내고, 이 탭은 안내 없이 로그인 화면으로 간다(auth.ts)
  function handleLogout() {
    void logout();
  }

  return (
    <header className="sticky top-0 z-30 flex h-16 items-center gap-4 border-b border-border bg-card/80 px-6 backdrop-blur supports-[backdrop-filter]:bg-card/60">
      <div className="flex flex-1 items-center gap-3">
        <div className="flex items-center gap-1 text-sm text-muted-foreground">
          {crumbs.map((c, i) => (
            <React.Fragment key={c.href}>
              {i > 0 && <span className="text-border">/</span>}
              <span
                className={
                  i === crumbs.length - 1
                    ? "font-medium text-foreground"
                    : ""
                }
              >
                {c.label}
              </span>
            </React.Fragment>
          ))}
        </div>
        <Badge variant="ghost" className="hidden gap-1 lg:inline-flex" title="이노그리드 KCloud Monitor API v2.0 (260409) 호환">
          <span className="size-1.5 rounded-full bg-brand-mint-500" />
          <span className="font-mono text-[10px]">KCloud v2 · observed 30s ago</span>
        </Badge>
      </div>
      <div className="relative hidden lg:block">
        <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
        <Input
          type="search"
          placeholder="자원·VM·파드·테넌트 검색…"
          className="w-72 pl-9"
        />
        <kbd className="pointer-events-none absolute right-2 top-2 inline-flex h-5 select-none items-center gap-1 rounded border border-border bg-secondary px-1.5 text-[10px] font-medium text-muted-foreground opacity-100">
          ⌘K
        </kbd>
      </div>
      <Button variant="ghost" size="icon" className="relative">
        <Bell className="size-4" />
        <span className="absolute right-1.5 top-1.5 size-2 rounded-full bg-brand-mint-500" />
      </Button>
      {user && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              className="flex h-auto items-center gap-2.5 px-2 py-1.5"
            >
              <Avatar className="size-8">
                <AvatarFallback className="bg-brand-indigo-100 text-brand-indigo-800">
                  {user.displayName.slice(0, 1)}
                </AvatarFallback>
              </Avatar>
              <div className="hidden flex-col items-start leading-tight md:flex">
                <span className="text-sm font-medium">{user.displayName}</span>
                <span className="text-[11px] text-muted-foreground">{user.org}</span>
              </div>
              <ChevronDown className="size-3.5 text-muted-foreground" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-60">
            <DropdownMenuLabel className="font-normal">
              <div className="flex flex-col gap-1">
                <p className="text-sm font-semibold">{user.displayName}</p>
                <p className="text-xs text-muted-foreground">{user.email}</p>
                <Badge variant="default" className="mt-1.5 w-fit">
                  {roleLabel(user.role)}
                </Badge>
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem>
              <UserRound className="size-4" /> 내 프로필
            </DropdownMenuItem>
            <DropdownMenuItem>
              <Settings className="size-4" /> 환경 설정
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={handleLogout}
              className="text-red-600 focus:text-red-700"
            >
              <LogOut className="size-4" /> 로그아웃
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </header>
  );
}
