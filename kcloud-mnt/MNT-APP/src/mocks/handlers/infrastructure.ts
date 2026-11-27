import { http, HttpResponse, delay } from "msw";
import {
  nodes,
  accelerators,
  vms,
  workloads,
  serviceClusters,
  vmGpuPassthroughs,
  volumes,
  observedMeta,
} from "../fixtures";

const meta = () => observedMeta();

export const infrastructureHandlers = [
  // 물리 서버 목록
  http.get("/api/infrastructure/servers", async () => {
    await delay(80);
    const items = nodes.map((n) => {
      const accs = accelerators.filter((a) => a.nodeId === n.id);
      const hostedVms = vms.filter((v) => v.nodeId === n.id);
      return {
        ...n,
        acceleratorCount: accs.length,
        acceleratorKinds: Array.from(new Set(accs.map((a) => a.kind))),
        vmCount: hostedVms.length,
        runningVms: hostedVms.filter((v) => v.status === "Running").length,
        cpuUtil: Math.min(95, 30 + Math.round(hostedVms.length * 8 + accs.length * 5)),
        memUtil: Math.min(95, 40 + Math.round(hostedVms.length * 6 + accs.length * 4)),
        accelUtil: accs.length ? Math.round(accs.reduce((s, a) => s + a.utilization, 0) / accs.length) : 0,
      };
    });
    return HttpResponse.json({ ...meta(), items });
  }),

  // 물리 서버 상세
  http.get("/api/infrastructure/servers/:id", async ({ params }) => {
    await delay(120);
    const node = nodes.find((n) => n.id === params.id);
    if (!node) return HttpResponse.json({ error: "not found" }, { status: 404 });
    const accs = accelerators.filter((a) => a.nodeId === node.id);
    const hostedVms = vms.filter((v) => v.nodeId === node.id);
    return HttpResponse.json({
      ...meta(),
      node,
      accelerators: accs,
      vms: hostedVms.map((v) => ({
        ...v,
        gpuPassthroughCount: vmGpuPassthroughs.filter((pt) => pt.vmId === v.id).length,
      })),
      stats: {
        cpu: {
          total: node.vcpu,
          used: Math.round(node.vcpu * 0.65),
          util: 65,
        },
        memory: {
          total: node.ramGb,
          used: Math.round(node.ramGb * 0.71),
          util: 71,
        },
        disk: {
          total: node.diskTb,
          used: Math.round(node.diskTb * 0.42 * 10) / 10,
          util: 42,
        },
        power: {
          watts: node.powerW,
          breakdown: {
            cpu: Math.round(node.powerW * 0.28),
            dram: Math.round(node.powerW * 0.08),
            gpu: Math.round(node.powerW * 0.58),
            other: Math.round(node.powerW * 0.06),
          },
        },
      },
    });
  }),

  // VM 상세
  http.get("/api/iaas/instances/:id", async ({ params }) => {
    await delay(100);
    const vm = vms.find((v) => v.id === params.id);
    if (!vm) return HttpResponse.json({ error: "not found" }, { status: 404 });
    const node = nodes.find((n) => n.id === vm.nodeId);
    const accs = accelerators.filter((a) => vm.acceleratorIds.includes(a.id));
    const pts = vmGpuPassthroughs.filter((pt) => pt.vmId === vm.id);
    const svc = serviceClusters.find((s) => s.id === vm.serviceClusterId);
    const wls = vm.serviceClusterId
      ? workloads.filter((w) => w.accelerators > 0).slice(0, 4)
      : [];
    // 부팅 시 신청한 추가 스토리지 = 이 VM에 붙은 데이터(비부팅) 볼륨
    const dataVolumes = volumes.filter((vol) => vol.attachedVmId === vm.id && !vol.bootable);
    return HttpResponse.json({
      ...meta(),
      vm,
      host: node,
      accelerators: accs,
      passthroughs: pts,
      serviceCluster: svc,
      workloads: wls,
      dataVolumes,
    });
  }),

  // 가속기 상세
  http.get("/api/accelerators/:id/detail", async ({ params }) => {
    await delay(100);
    const acc = accelerators.find((a) => a.id === params.id);
    if (!acc) return HttpResponse.json({ error: "not found" }, { status: 404 });
    const node = nodes.find((n) => n.id === acc.nodeId);
    const pt = vmGpuPassthroughs.find((p) => p.acceleratorId === acc.id);
    const vm = pt ? vms.find((v) => v.id === pt.vmId) : undefined;
    return HttpResponse.json({
      ...meta(),
      accelerator: acc,
      host: node,
      passthrough: pt,
      vm,
    });
  }),

  // 워크로드 상세
  http.get("/api/paas/workloads/:id", async ({ params }) => {
    await delay(100);
    const wl = workloads.find((w) => w.id === params.id);
    if (!wl) return HttpResponse.json({ error: "not found" }, { status: 404 });
    return HttpResponse.json({ ...meta(), workload: wl });
  }),

  // 토폴로지 (Smartscape)
  http.get("/api/topology", async () => {
    await delay(140);
    return HttpResponse.json({
      ...meta(),
      nodes,
      accelerators,
      vms: vms.filter((v) => v.serviceClusterId),
      workloads: workloads.filter((w) => w.accelerators > 0).slice(0, 8),
      serviceClusters,
      passthroughs: vmGpuPassthroughs,
    });
  }),
];
