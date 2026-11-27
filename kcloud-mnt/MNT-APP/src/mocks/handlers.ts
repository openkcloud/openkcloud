import { http, HttpResponse } from "msw";
import { acceleratorHandlers } from "./handlers/accelerators";
import { iaasHandlers } from "./handlers/iaas";
import { paasHandlers } from "./handlers/paas";
import { kubeflowHandlers } from "./handlers/kubeflow";
import { dashboardHandlers } from "./handlers/dashboard";
import { resourceMapHandlers } from "./handlers/resource-map";
import { infrastructureHandlers } from "./handlers/infrastructure";
import { ticketCoreOverlapHandlers, ticketHandlers, ticketIaasOverlapHandlers } from "./handlers/ticket";

// 게이트웨이(배포) 빌드 = NEXT_PUBLIC_IAAS_API 가 "" → 실 MNT-IAAS 사용.
// 이때 /api/iaas 목 핸들러를 빼야 실연동 요청이 bypass 로 게이트웨이(진짜 백엔드)에 도달한다.
// (로컬 dev 는 IAAS_BASE 가 절대주소(localhost:8001)라 목과 경로가 안 겹쳐 그대로 둔다)
const useRealIaas = process.env.NEXT_PUBLIC_IAAS_API === "";
// /api/core 도 같은 이유 — 게이트웨이 빌드(NEXT_PUBLIC_CORE_API "")에서는 실연동과 겹치는 목업(조직·사용자 프로젝트)을 뺀다
const useRealCore = process.env.NEXT_PUBLIC_CORE_API === "";
// /api/paas 도 같은 이유 — 목 /api/paas/clusters·workloads 가 실 MNT-PAAS 경로와 겹친다
const useRealPaas = process.env.NEXT_PUBLIC_PAAS_API === "";

export const handlers = [
  ...dashboardHandlers,
  ...acceleratorHandlers,
  ...(useRealIaas ? [] : iaasHandlers),
  ...(useRealPaas ? [] : paasHandlers),
  ...kubeflowHandlers,
  ...resourceMapHandlers,
  ...infrastructureHandlers,
  ...ticketHandlers,
  ...(useRealCore ? [] : ticketCoreOverlapHandlers),
  ...(useRealIaas ? [] : ticketIaasOverlapHandlers),
  // catch-all for /api/*
  http.get("/api/health", () => HttpResponse.json({ status: "ok", env: "mock" })),
];
