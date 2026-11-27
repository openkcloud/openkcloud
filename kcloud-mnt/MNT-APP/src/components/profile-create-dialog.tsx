"use client";
import * as React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { createProfile } from "@/lib/notebook-api";

export function ProfileCreateDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = React.useState("");
  const [ownerEmail, setOwnerEmail] = React.useState("");
  const [cpu, setCpu] = React.useState("8");
  const [memoryGi, setMemoryGi] = React.useState("16");
  const [gpu, setGpu] = React.useState("1");
  const [storageGi, setStorageGi] = React.useState("50");

  const create = useMutation({
    mutationFn: createProfile,
    onSuccess: () => {
      toast.success(`워크스페이스 "${name}" 생성 — 네임스페이스·쿼터 프로비저닝 중`);
      qc.invalidateQueries({ queryKey: ["kf", "profiles"] });
      onOpenChange(false);
      setName("");
    },
    onError: (e: Error) => toast.error(`생성 실패: ${e.message}`),
  });

  const valid =
    /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(name) && ownerEmail.includes("@");

  function submit() {
    create.mutate({
      name,
      ownerEmail,
      cpu: Number(cpu),
      memoryGi: Number(memoryGi),
      acceleratorCount: Number(gpu),
      storageGi: Number(storageGi),
    });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>새 워크스페이스 (관리자)</DialogTitle>
          <DialogDescription>
            프로젝트와 같은 이름으로 만든다. Kubeflow Profile 생성 → Namespace·ResourceQuota·RBAC 자동 프로비저닝
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="pf-name">워크스페이스 이름 (= 프로젝트 이름)</Label>
            <Input
              id="pf-name"
              placeholder="예: ai-research-2 (소문자·숫자·하이픈)"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="pf-owner">소유자 이메일</Label>
            <Input
              id="pf-owner"
              placeholder="owner@okestro.com"
              value={ownerEmail}
              onChange={(e) => setOwnerEmail(e.target.value)}
            />
          </div>
          <div className="grid grid-cols-4 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="pf-cpu">vCPU</Label>
              <Input id="pf-cpu" value={cpu} onChange={(e) => setCpu(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-mem">MEM (Gi)</Label>
              <Input id="pf-mem" value={memoryGi} onChange={(e) => setMemoryGi(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-gpu">GPU</Label>
              <Input id="pf-gpu" value={gpu} onChange={(e) => setGpu(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pf-sto">Storage (Gi)</Label>
              <Input id="pf-sto" value={storageGi} onChange={(e) => setStorageGi(e.target.value)} />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>취소</Button>
          <Button onClick={submit} disabled={!valid || create.isPending}>
            {create.isPending ? "생성 중…" : "생성"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
