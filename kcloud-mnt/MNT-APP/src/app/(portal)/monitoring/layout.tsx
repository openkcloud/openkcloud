"use client";
import * as React from "react";
import { usePathname } from "next/navigation";
import { toast } from "sonner";
import { Clock } from "lucide-react";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { RefreshControls } from "@/components/iaas/refresh-controls";

const RANGES = ["지난 15분", "지난 1시간", "지난 6시간", "지난 24시간"];

// 이 화면 쿼리들(새로고침·로딩 표시 대상). prefix 매칭이라 뒤 세그먼트는 생략.
const OBS_KEYS = [
  ["obs-overview"],
  ["obs-accel-summary"],
  ["obs-clusters"],
  ["obs-top-hosts"],
  ["obs-error-logs"],
  ["obs-kpi-timeseries"],
  ["obs-nodes"],
  ["obs-util-timeseries"],
  ["obs-power-breakdown"],
  ["obs-power-timeseries"],
  ["obs-alerts-active"],
  ["obs-alert-policies"],
  ["obs-alert-channels"],
];

export default function Layout({ children }: { children: React.ReactNode }) {
  const [range, setRange] = React.useState(1);
  const pathname = usePathname();
  // 로그 화면은 자체 검색/조회기간/실시간(tail) 컨트롤을 쓰므로 전역 조회구간·새로고침 주기는 숨긴다.
  const hideHeaderControls = pathname?.startsWith("/monitoring/logs");
  return (
    <div className="space-y-6">
      <PageHeader
        title="옵저버빌리티"
        description="인프라·가속기 지표와 전력·클러스터 현황 통합 분석"
        actions={
          hideHeaderControls ? undefined : (
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                const next = (range + 1) % RANGES.length;
                setRange(next);
                toast.success(`조회 구간 ${RANGES[next]}`);
              }}
              title="누를 때마다 조회 구간이 바뀐다"
            >
              <Clock className="size-3.5" /> {RANGES[range]}
            </Button>
            <RefreshControls queryKeys={OBS_KEYS} />
          </>
          )
        }
      />
      {children}
    </div>
  );
}
