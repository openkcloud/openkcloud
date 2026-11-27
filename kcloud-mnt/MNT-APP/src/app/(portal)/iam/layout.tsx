"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ShieldCheck, Plus } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const TABS = [
  { href: "/iam/users", label: "사용자" },
  { href: "/iam/roles", label: "역할·권한" },
  { href: "/iam/api-keys", label: "API Key" },
  { href: "/iam/audit", label: "감사 로그" },
];

export default function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  return (
    <div className="space-y-6">
      <PageHeader
        title="IAM·접근 제어"
        description={
          <span>
            RBAC · OAuth 2.0 · OPA · MFA — SFR.MNT.006~009 / SFR.MNT.016/017/022/025~027
          </span>
        }
        actions={
          <Button size="sm">
            <Plus className="size-4" /> 새 사용자
          </Button>
        }
      />
      <div className="flex items-center gap-1 border-b border-border">
        {TABS.map((t) => {
          const active = pathname === t.href;
          return (
            <Link
              key={t.href}
              href={t.href}
              className={cn(
                "relative -mb-px shrink-0 border-b-2 px-4 py-2.5 text-sm font-medium transition-colors",
                active ? "border-primary text-primary" : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </Link>
          );
        })}
      </div>
      {children}
    </div>
  );
}
