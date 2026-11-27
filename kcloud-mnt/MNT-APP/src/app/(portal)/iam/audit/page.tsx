"use client";
import { History } from "lucide-react";
import { api } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

type ActivitiesResponse = { items: { id: string; ts: string; actor: string; action: string; target: string; result: string }[] };

export default function AuditPage() {
  const { data, isLoading } = api.dashboard.activities() as { data: ActivitiesResponse | undefined; isLoading: boolean };
  if (isLoading || !data) return <Skeleton className="h-96" />;
  return (
    <Card>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-44">시각</TableHead>
            <TableHead>행위자</TableHead>
            <TableHead>이벤트</TableHead>
            <TableHead>대상</TableHead>
            <TableHead>결과</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.items.map((a) => (
            <TableRow key={a.id}>
              <TableCell className="font-mono text-xs text-muted-foreground">
                {a.ts.replace("T", " ")}
              </TableCell>
              <TableCell>
                <div className="flex items-center gap-1.5">
                  <History className="size-3.5 text-muted-foreground" />
                  <span className={a.actor.startsWith("system") ? "font-mono text-xs" : ""}>{a.actor}</span>
                </div>
              </TableCell>
              <TableCell>{a.action}</TableCell>
              <TableCell className="font-mono text-xs text-muted-foreground">{a.target}</TableCell>
              <TableCell>
                <Badge variant={a.result === "success" ? "success" : a.result === "warning" ? "warning" : "destructive"}>
                  {a.result === "success" ? "성공" : a.result === "warning" ? "경고" : "실패"}
                </Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
