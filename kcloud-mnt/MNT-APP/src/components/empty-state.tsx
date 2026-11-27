import * as React from "react";
import { Card } from "@/components/ui/card";

interface EmptyStateProps {
  icon?: React.ComponentType<{ className?: string }>;
  title: string;
  description?: string;
  action?: React.ReactNode;
}

export function EmptyState({ icon: Icon, title, description, action }: EmptyStateProps) {
  return (
    <Card className="flex flex-col items-center justify-center gap-3 p-12 text-center">
      {Icon && (
        <div className="flex size-12 items-center justify-center rounded-full bg-secondary text-muted-foreground">
          <Icon className="size-6" />
        </div>
      )}
      <div className="space-y-1">
        <h3 className="text-base font-semibold">{title}</h3>
        {description && (
          <p className="max-w-md text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {action}
    </Card>
  );
}
