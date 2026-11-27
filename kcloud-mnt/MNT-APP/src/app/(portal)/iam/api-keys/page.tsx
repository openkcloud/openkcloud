"use client";
import { Key, Copy, RefreshCw, Trash2 } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { relativeTime } from "@/lib/utils";

const API_KEYS = [
  { id: "ak-1", name: "kfp-runner-bot", owner: "박의규", scopes: ["kubeflow:*", "paas:workloads:read"], lastUsed: "2026-05-31T13:50:00", createdAt: "2026-04-12", expiresAt: "2027-04-12" },
  { id: "ak-2", name: "ci-cd-pipeline", owner: "윤호영", scopes: ["paas:workloads:write", "iaas:images:read"], lastUsed: "2026-05-31T12:11:00", createdAt: "2025-12-04", expiresAt: "2026-12-04" },
  { id: "ak-3", name: "monitoring-exporter", owner: "system", scopes: ["metrics:read"], lastUsed: "2026-05-31T14:28:00", createdAt: "2025-11-01", expiresAt: "—" },
  { id: "ak-4", name: "data-loader-job", owner: "최현화", scopes: ["iaas:volumes:read", "storage:objects:write"], lastUsed: "2026-05-30T22:14:00", createdAt: "2026-03-22", expiresAt: "2026-09-22" },
];

export default function ApiKeysPage() {
  return (
    <Card>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>이름</TableHead>
            <TableHead>소유자</TableHead>
            <TableHead>스코프</TableHead>
            <TableHead>토큰 (마스킹)</TableHead>
            <TableHead>마지막 사용</TableHead>
            <TableHead>만료</TableHead>
            <TableHead className="w-32" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {API_KEYS.map((k) => (
            <TableRow key={k.id}>
              <TableCell>
                <div className="flex items-center gap-2">
                  <Key className="size-4 text-brand-indigo-600" />
                  <span className="font-medium">{k.name}</span>
                </div>
              </TableCell>
              <TableCell className="text-sm">{k.owner}</TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1">
                  {k.scopes.map((s) => (
                    <Badge key={s} variant="ghost" className="font-mono text-[10px]">
                      {s}
                    </Badge>
                  ))}
                </div>
              </TableCell>
              <TableCell className="font-mono text-xs text-muted-foreground">
                okc_••••••••{k.id.slice(-4)}
              </TableCell>
              <TableCell className="text-xs">{relativeTime(k.lastUsed)}</TableCell>
              <TableCell className="text-xs">{k.expiresAt}</TableCell>
              <TableCell>
                <div className="flex items-center gap-0.5">
                  <Button variant="ghost" size="icon-sm">
                    <Copy className="size-3.5" />
                  </Button>
                  <Button variant="ghost" size="icon-sm">
                    <RefreshCw className="size-3.5" />
                  </Button>
                  <Button variant="ghost" size="icon-sm">
                    <Trash2 className="size-3.5 text-rose-600" />
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
