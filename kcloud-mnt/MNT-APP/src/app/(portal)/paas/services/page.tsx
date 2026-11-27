"use client";
/**
 * 서비스·인그레스 — 선택한 서비스 클러스터의 Service·Ingress 라이브 조회.
 * 쓰기(생성·편집·삭제)는 다음 단계. 레이아웃·폰트는 IaaS 목록 화면 규약과 동일.
 */
import * as React from "react";
import { Search, Lock, Network } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { ClusterScopeBar, useClusterScope } from "@/components/paas/cluster-scope-bar";
import { useK8sServices } from "@/lib/paas-api";

export default function ServicesPage() {
  const { clusterId } = useClusterScope();
  const [namespace, setNamespace] = React.useState("all");
  const [q, setQ] = React.useState("");

  const data = useK8sServices(clusterId, namespace === "all" ? undefined : namespace);
  const services = (data.data?.services ?? []).filter((s) => !q || s.name.includes(q));
  const ingresses = (data.data?.ingresses ?? []).filter((i) => !q || i.name.includes(q));

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <ClusterScopeBar
          queryKeys={[["paas-services"], ["paas-namespaces"]]}
          namespace={namespace}
          onNamespaceChange={setNamespace}
        >
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input placeholder="서비스 검색…" value={q} onChange={(e) => setQ(e.target.value)} className="w-52 pl-9" />
          </div>
          <span className="text-xs text-muted-foreground">총 {services.length + ingresses.length}개</span>
        </ClusterScopeBar>
      </Card>

      {!clusterId ? (
        <Card><EmptyBox text="먼저 서비스 클러스터를 선택하세요." /></Card>
      ) : data.isLoading ? (
        <Skeleton className="h-64" />
      ) : data.isError ? (
        <Card><EmptyBox text="서비스 조회 실패 — 클러스터 API 응답 오류." /></Card>
      ) : (
        <Tabs defaultValue="services">
          <TabsList>
            <TabsTrigger value="services">Service ({services.length})</TabsTrigger>
            <TabsTrigger value="ingresses">Ingress ({ingresses.length})</TabsTrigger>
          </TabsList>

          <TabsContent value="services" className="pt-3">
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>서비스</TableHead>
                    <TableHead>네임스페이스</TableHead>
                    <TableHead>유형</TableHead>
                    <TableHead>Cluster IP</TableHead>
                    <TableHead>External IP</TableHead>
                    <TableHead>포트</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {services.map((s) => (
                    <TableRow key={`${s.namespace}/${s.name}`}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Network className="size-3.5 text-brand-indigo-600" />
                          <span className="max-w-[200px] truncate font-mono text-sm font-semibold">{s.name}</span>
                        </div>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{s.namespace}</TableCell>
                      <TableCell>
                        <Badge variant={s.type === "LoadBalancer" ? "default" : "outline"} className="text-[10px]">{s.type}</Badge>
                      </TableCell>
                      <TableCell className="font-mono text-[11px]">{s.clusterIp || "—"}</TableCell>
                      <TableCell className="font-mono text-[11px]">{s.externalIp || "—"}</TableCell>
                      <TableCell className="font-mono text-[11px] text-muted-foreground">
                        {s.ports.map((p) => `${p.port}→${p.targetPort}/${p.protocol}`).join(", ") || "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                  {services.length === 0 && <EmptyRow colSpan={6} />}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>

          <TabsContent value="ingresses" className="pt-3">
            <Card>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>인그레스</TableHead>
                    <TableHead>네임스페이스</TableHead>
                    <TableHead>클래스</TableHead>
                    <TableHead>호스트</TableHead>
                    <TableHead>TLS</TableHead>
                    <TableHead>규칙 수</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ingresses.map((i) => (
                    <TableRow key={`${i.namespace}/${i.name}`}>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          <Network className="size-3.5 text-brand-indigo-600" />
                          <span className="max-w-[200px] truncate font-mono text-sm font-semibold">{i.name}</span>
                        </div>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{i.namespace}</TableCell>
                      <TableCell className="font-mono text-xs">{i.className || "—"}</TableCell>
                      <TableCell className="max-w-[260px] truncate font-mono text-[11px]">{i.hosts.join(", ") || "*"}</TableCell>
                      <TableCell>
                        {i.tls ? (
                          <Badge variant="success" className="gap-1 text-[10px]"><Lock className="size-3" /> TLS</Badge>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="font-mono text-xs">{i.ruleCount}</TableCell>
                    </TableRow>
                  ))}
                  {ingresses.length === 0 && <EmptyRow colSpan={6} />}
                </TableBody>
              </Table>
            </Card>
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}

function EmptyBox({ text }: { text: string }) {
  return <div className="py-10 text-center text-sm text-muted-foreground">{text}</div>;
}
function EmptyRow({ colSpan }: { colSpan: number }) {
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="py-10 text-center text-sm text-muted-foreground">
        항목이 없습니다.
      </TableCell>
    </TableRow>
  );
}
