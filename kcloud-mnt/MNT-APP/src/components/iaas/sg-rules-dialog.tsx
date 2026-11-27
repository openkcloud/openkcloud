"use client";
import * as React from "react";
import { Loader2, Plus, X } from "lucide-react";
import { toast } from "sonner";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useSecurityGroup, useAddSgRule, useDeleteSgRule } from "@/lib/iaas-api";
import { useConfirm } from "@/components/ui/confirm";

/** 보안 그룹 규칙 편집 모달 — 네트워크 상세·보안그룹 목록에서 공용. readOnly 면 조회만(사용자 — 규칙 변경은 관리자). */
export function SgRulesDialog({ sgId, onClose, readOnly = false }: {
  sgId: string | undefined; onClose: () => void; readOnly?: boolean;
}) {
  const id = sgId ?? "";
  const { data: sg, isLoading } = useSecurityGroup(sgId);
  const addRule = useAddSgRule(id);
  const delRule = useDeleteSgRule(id);
  const confirm = useConfirm();
  const [direction, setDirection] = React.useState("ingress");
  const [protocol, setProtocol] = React.useState("tcp");
  const [portMin, setPortMin] = React.useState("");
  const [portMax, setPortMax] = React.useState("");
  const [cidr, setCidr] = React.useState("0.0.0.0/0");
  const busy = addRule.isPending || delRule.isPending;

  function submit() {
    const anyProto = protocol === "any";
    addRule.mutate(
      {
        direction,
        ethertype: "IPv4",
        protocol: anyProto ? null : protocol,
        portMin: anyProto || !portMin ? null : Number(portMin),
        portMax: anyProto ? null : (portMax ? Number(portMax) : (portMin ? Number(portMin) : null)),
        remoteCidr: cidr.trim() || null,
      },
      {
        onSuccess: () => { toast.success("규칙 추가됨"); setPortMin(""); setPortMax(""); },
        onError: (e) => toast.error("규칙 추가 실패", { description: String((e as Error).message) }),
      },
    );
  }

  const fmtPort = (r: { protocol: string | null; portMin: number | null; portMax: number | null }) => {
    if (!r.protocol) return "any";
    if (r.portMin == null) return "all";
    return r.portMin === r.portMax ? String(r.portMin) : `${r.portMin}–${r.portMax}`;
  };

  return (
    <Dialog open={!!sgId} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="font-mono">{sg?.name ?? "보안 그룹"} 규칙</DialogTitle>
          <DialogDescription>{sg?.desc || `인바운드/아웃바운드 규칙 ${readOnly ? "조회" : "편집"} (OpenStack Neutron)`}</DialogDescription>
        </DialogHeader>

        <div className="max-h-[45vh] space-y-1.5 overflow-y-auto">
          {isLoading ? (
            <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> 규칙 조회 중…
            </div>
          ) : !sg || sg.rules.length === 0 ? (
            <Card className="p-6 text-center text-sm text-muted-foreground">규칙이 없습니다.</Card>
          ) : (
            sg.rules.map((r) => (
              <div key={r.id} className="flex items-center gap-2 rounded-md border border-border px-3 py-2 text-xs">
                <Badge variant={r.direction === "ingress" ? "success" : "outline"} className="text-[10px]">
                  {r.direction === "ingress" ? "인바운드" : "아웃바운드"}
                </Badge>
                <span className="font-mono">{r.ethertype}</span>
                <span className="font-mono">{r.protocol ?? "any"}</span>
                <span className="font-mono">port {fmtPort(r)}</span>
                <span className="ml-auto font-mono text-muted-foreground">{r.remoteCidr ?? (r.remoteGroup ? "SG" : "any")}</span>
                {!readOnly && (
                  <Button
                    variant="ghost" size="icon-sm" disabled={busy} title="규칙 삭제"
                    onClick={async () => {
                      if (!(await confirm({
                        title: "규칙 삭제", destructive: true, confirmLabel: "삭제",
                        description: `${r.direction === "ingress" ? "인바운드" : "아웃바운드"} ${r.protocol ?? "any"} ${fmtPort(r)} 규칙을 삭제할까요?`,
                      }))) return;
                      delRule.mutate(r.id, { onError: (e) => toast.error("삭제 실패", { description: String((e as Error).message) }) });
                    }}
                  >
                    <X className="size-3.5 text-rose-600" />
                  </Button>
                )}
              </div>
            ))
          )}
        </div>

        {!readOnly && (
          <div className="rounded-md border border-border p-3">
            <div className="mb-2 text-[10px] uppercase text-muted-foreground">규칙 추가</div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
              <Select value={direction} onValueChange={setDirection}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="ingress">인바운드</SelectItem>
                  <SelectItem value="egress">아웃바운드</SelectItem>
                </SelectContent>
              </Select>
              <Select value={protocol} onValueChange={setProtocol}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="tcp">TCP</SelectItem>
                  <SelectItem value="udp">UDP</SelectItem>
                  <SelectItem value="icmp">ICMP</SelectItem>
                  <SelectItem value="any">Any</SelectItem>
                </SelectContent>
              </Select>
              <Input type="number" value={portMin} onChange={(e) => setPortMin(e.target.value)} placeholder="port" disabled={protocol === "any" || protocol === "icmp"} />
              <Input type="number" value={portMax} onChange={(e) => setPortMax(e.target.value)} placeholder="~port(선택)" disabled={protocol === "any" || protocol === "icmp"} />
              <Input value={cidr} onChange={(e) => setCidr(e.target.value)} placeholder="0.0.0.0/0" />
            </div>
            <div className="mt-2 flex justify-end">
              <Button size="sm" onClick={submit} disabled={busy}>
                <Plus className="size-3.5" /> {addRule.isPending ? "추가 중…" : "규칙 추가"}
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
