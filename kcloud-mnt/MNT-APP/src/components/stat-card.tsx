import * as React from "react";
import { TrendingUp, TrendingDown } from "lucide-react";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface StatCardProps {
  label: string;
  value: React.ReactNode;
  unit?: string;
  delta?: { value: number; trend: "up" | "down"; label?: string; positive?: "up" | "down" };
  icon?: React.ComponentType<{ className?: string }>;
  accent?: "indigo" | "mint" | "amber" | "violet" | "rose" | "sky";
  footer?: React.ReactNode;
  className?: string;
}

const ACCENT_MAP: Record<NonNullable<StatCardProps["accent"]>, string> = {
  indigo: "from-brand-indigo-500/15 to-brand-indigo-500/0 text-brand-indigo-700",
  mint: "from-brand-mint-500/15 to-brand-mint-500/0 text-brand-mint-700",
  amber: "from-amber-500/15 to-amber-500/0 text-amber-700",
  violet: "from-violet-500/15 to-violet-500/0 text-violet-700",
  rose: "from-rose-500/15 to-rose-500/0 text-rose-700",
  sky: "from-sky-500/15 to-sky-500/0 text-sky-700",
};

export function StatCard({
  label,
  value,
  unit,
  delta,
  icon: Icon,
  accent = "indigo",
  footer,
  className,
}: StatCardProps) {
  const positiveDir = delta?.positive ?? "up";
  const isPositive =
    delta && ((positiveDir === "up" && delta.trend === "up") ||
      (positiveDir === "down" && delta.trend === "down"));

  return (
    <Card className={cn("relative overflow-hidden p-5", className)}>
      <div
        className={cn(
          "pointer-events-none absolute inset-0 bg-gradient-to-br opacity-100",
          ACCENT_MAP[accent],
        )}
      />
      <div className="relative flex items-start justify-between">
        <div className="space-y-2">
          <div className="text-xs font-medium text-muted-foreground">{label}</div>
          <div className="flex items-baseline gap-1">
            <span className="text-2xl font-bold tabular-nums text-foreground">
              {value}
            </span>
            {unit && (
              <span className="text-xs font-medium text-muted-foreground">{unit}</span>
            )}
          </div>
          {delta && (
            <div
              className={cn(
                "inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium",
                isPositive ? "bg-brand-mint-50 text-brand-mint-700" : "bg-rose-50 text-rose-700",
              )}
            >
              {delta.trend === "up" ? (
                <TrendingUp className="size-3" />
              ) : (
                <TrendingDown className="size-3" />
              )}
              {delta.value}%
              {delta.label && (
                <span className="ml-0.5 font-normal opacity-70">{delta.label}</span>
              )}
            </div>
          )}
        </div>
        {Icon && (
          <div
            className={cn(
              "flex size-10 items-center justify-center rounded-lg",
              accent === "indigo" && "bg-brand-indigo-100 text-brand-indigo-700",
              accent === "mint" && "bg-brand-mint-50 text-brand-mint-700",
              accent === "amber" && "bg-amber-50 text-amber-700",
              accent === "violet" && "bg-violet-50 text-violet-700",
              accent === "rose" && "bg-rose-50 text-rose-700",
              accent === "sky" && "bg-sky-50 text-sky-700",
            )}
          >
            <Icon className="size-5" />
          </div>
        )}
      </div>
      {footer && (
        <div className="relative mt-4 border-t border-border pt-3 text-xs text-muted-foreground">
          {footer}
        </div>
      )}
    </Card>
  );
}
