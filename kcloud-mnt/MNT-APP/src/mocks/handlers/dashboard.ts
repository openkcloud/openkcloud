import { http, HttpResponse, delay } from "msw";
import {
  accelerators,
  activities,
  alerts,
  clusters,
  makePowerSeries,
  makeUtilSeries,
  nodes,
  pools,
  tenants,
  vms,
  workloads,
} from "../fixtures";

export const dashboardHandlers = [
  http.get("/api/dashboard/summary", async () => {
    await delay(120);
    const totalCpu = pools.reduce((s, p) => s + p.capacity.vcpu, 0);
    const usedCpu = pools.reduce((s, p) => s + p.used.vcpu, 0);
    const totalRam = pools.reduce((s, p) => s + p.capacity.ramGb, 0);
    const usedRam = pools.reduce((s, p) => s + p.used.ramGb, 0);
    const totalDisk = pools.reduce((s, p) => s + p.capacity.diskTb, 0);
    const usedDisk = pools.reduce((s, p) => s + p.used.diskTb, 0);
    return HttpResponse.json({
      kpis: {
        vmCount: vms.length,
        runningVmCount: vms.filter((v) => v.status === "Running").length,
        nodeCount: nodes.length,
        clusterCount: clusters.length,
        podCount: workloads.reduce((s, w) => s + w.ready, 0),
        tenantCount: tenants.length,
        acceleratorTotal: accelerators.length,
        acceleratorActive: accelerators.filter((a) => a.status === "Allocated").length,
        cpuPct: Math.round((usedCpu / totalCpu) * 100),
        ramPct: Math.round((usedRam / totalRam) * 100),
        diskPct: Math.round((usedDisk / totalDisk) * 100),
        avgAccUtil: Math.round(
          accelerators.reduce((s, a) => s + a.utilization, 0) / accelerators.length,
        ),
        powerKw: Math.round(
          nodes.reduce((s, n) => s + n.powerW, 0) / 1000 * 10,
        ) / 10,
        estimatedCostMonth: 18420000, // ₩
        powerSavedPct: 12.4,
      },
      tenantsByUtil: tenants
        .map((t) => ({
          name: t.name,
          vcpuPct: Math.round((t.usedVcpu / t.quotaVcpu) * 100),
          ramPct: Math.round((t.usedRamGb / t.quotaRamGb) * 100),
          accPct: t.quotaAcc ? Math.round((t.usedAcc / t.quotaAcc) * 100) : 0,
        }))
        .sort((a, b) => b.vcpuPct - a.vcpuPct),
      acceleratorBreakdown: ["GPU", "NPU", "PIM"].map((kind) => ({
        kind,
        total: accelerators.filter((a) => a.kind === kind).length,
        allocated: accelerators.filter(
          (a) => a.kind === kind && a.status === "Allocated",
        ).length,
        avgUtil: Math.round(
          accelerators
            .filter((a) => a.kind === kind)
            .reduce((s, a) => s + a.utilization, 0) /
            Math.max(1, accelerators.filter((a) => a.kind === kind).length),
        ),
      })),
      powerSeries: makePowerSeries(),
      utilSeries: makeUtilSeries(),
    });
  }),

  http.get("/api/dashboard/activities", async () => {
    await delay(100);
    return HttpResponse.json({ items: activities });
  }),

  http.get("/api/dashboard/alerts", async () => {
    await delay(100);
    return HttpResponse.json({
      items: alerts,
      counts: {
        critical: alerts.filter((a) => a.severity === "critical" && !a.acknowledged).length,
        warning: alerts.filter((a) => a.severity === "warning" && !a.acknowledged).length,
        info: alerts.filter((a) => a.severity === "info").length,
      },
    });
  }),
];
