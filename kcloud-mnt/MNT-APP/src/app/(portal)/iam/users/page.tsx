"use client";
import { UserRound, ShieldCheck } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { MOCK_USERS, roleLabel, type Role } from "@/lib/auth";

const EXTENDED_USERS: Array<{
  id: string;
  username: string;
  displayName: string;
  email: string;
  org: string;
  role: Role;
  projects: string[];
}> = [
  ...Object.values(MOCK_USERS).map((m) => m.user),
  { id: "u-003", username: "park.ek", displayName: "박의규", email: "ek.park@okestro.com", org: "오케스트로(주)", role: "user", projects: ["*"] },
  { id: "u-004", username: "kim.bw", displayName: "김바울", email: "bw.kim@innogrid.com", org: "이노그리드", role: "user", projects: ["*"] },
  { id: "u-005", username: "ahn.jh", displayName: "안재훈", email: "jh.ahn@keti.re.kr", org: "KETI", role: "user", projects: ["*"] },
  { id: "u-006", username: "huh.un", displayName: "허의남", email: "uinam.huh@khu.ac.kr", org: "경희대 산학협력단", role: "user", projects: ["*"] },
  { id: "u-007", username: "ham.jc", displayName: "함재춘", email: "jc.ham@kaci.kr", org: "한국AI클라우드산업협회", role: "admin", projects: ["*"] },
  { id: "u-008", username: "kim.dw", displayName: "김대원", email: "dw.kim@etri.re.kr", org: "ETRI", role: "user", projects: ["*"] },
  { id: "u-009", username: "shim.jw", displayName: "심정우", email: "jw.shim@etri.re.kr", org: "ETRI", role: "user", projects: ["*"] },
];

export default function UsersPage() {
  return (
    <Card>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>사용자</TableHead>
            <TableHead>기관</TableHead>
            <TableHead>역할</TableHead>
            <TableHead>접근 범위</TableHead>
            <TableHead>2FA</TableHead>
            <TableHead>마지막 로그인</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {EXTENDED_USERS.map((u) => (
            <TableRow key={u.id}>
              <TableCell>
                <div className="flex items-center gap-2.5">
                  <Avatar className="size-8">
                    <AvatarFallback className="bg-brand-indigo-100 text-brand-indigo-700">
                      {u.displayName.slice(0, 1)}
                    </AvatarFallback>
                  </Avatar>
                  <div>
                    <div className="font-medium">{u.displayName}</div>
                    <div className="font-mono text-[11px] text-muted-foreground">
                      {u.email}
                    </div>
                  </div>
                </div>
              </TableCell>
              <TableCell className="text-sm">{u.org}</TableCell>
              <TableCell>
                <Badge variant={u.role === "admin" ? "default" : "success"}>
                  <ShieldCheck className="size-3" /> {roleLabel(u.role)}
                </Badge>
              </TableCell>
              <TableCell>
                <Badge variant="ghost" className="font-mono text-[10px]">All projects</Badge>
              </TableCell>
              <TableCell>
                <Badge variant="success" className="gap-1">
                  <span className="size-1.5 rounded-full bg-brand-mint-500" />
                  활성
                </Badge>
              </TableCell>
              <TableCell className="text-xs text-muted-foreground">방금 전</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
