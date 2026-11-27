import { http, HttpResponse, delay } from "msw";
import {
  managementClusters,
  serviceClusters,
  partitions,
  resourceMapNodes,
  resourceMapEdges,
  vmGpuPassthroughs,
  vms,
  accelerators,
  observedMeta,
} from "../fixtures";

const meta = () => observedMeta();

export const resourceMapHandlers = [
  // KCloud Monitor v2 호환 alias
  http.get("/api/v2/clusters", async () => {
    await delay(80);
    return HttpResponse.json({
      ...meta(),
      data: [
        ...managementClusters.map((c) => ({
          name: c.id,
          type: "management",
          description: c.description,
          node_count: c.nodeCount,
          gpu_count: c.gpuCount,
          total_power_watts: c.totalPowerWatts,
          has_openstack: true,
          service_clusters: c.serviceClusters,
          status: c.status,
        })),
        ...serviceClusters.map((c) => ({
          name: c.id,
          type: "service",
          description: c.description,
          parent_cluster: c.parentCluster,
          openstack_project: c.openstackProject,
          node_count: c.nodeCount,
          gpu_count: c.gpuCount,
          total_power_watts: c.totalPowerWatts,
          status: c.status,
        })),
      ],
      pagination: {
        total: managementClusters.length + serviceClusters.length,
        limit: 100,
        offset: 0,
        has_next: false,
      },
    });
  }),

  http.get("/api/clusters/management", async () => {
    await delay(80);
    return HttpResponse.json({ ...meta(), items: managementClusters });
  }),
  http.get("/api/clusters/service", async () => {
    await delay(80);
    return HttpResponse.json({ ...meta(), items: serviceClusters });
  }),

  // Resource map
  http.get("/api/resource-map", async () => {
    await delay(120);
    return HttpResponse.json({
      ...meta(),
      nodes: resourceMapNodes,
      edges: resourceMapEdges,
      stats: {
        L0: resourceMapNodes.filter((n) => n.layer === "L0").length,
        L1: resourceMapNodes.filter((n) => n.layer === "L1").length,
        L3: resourceMapNodes.filter((n) => n.layer === "L3").length,
        L4: resourceMapNodes.filter((n) => n.layer === "L4").length,
        L5: resourceMapNodes.filter((n) => n.layer === "L5").length,
        LA: resourceMapNodes.filter((n) => n.layer === "LA").length,
      },
    });
  }),

  // Partitions
  http.get("/api/partitions", async () => {
    await delay(100);
    return HttpResponse.json({
      ...meta(),
      items: partitions,
      stats: {
        total: partitions.length,
        byModel: ["mig", "mdev", "vgpu", "npu-slice"].map((m) => ({
          model: m,
          count: partitions.filter((p) => p.model === m).length,
        })),
      },
    });
  }),

  // Passthrough mapping
  http.get("/api/passthroughs", async () => {
    await delay(80);
    return HttpResponse.json({ ...meta(), items: vmGpuPassthroughs });
  }),

  // KCloud Monitor v2 — accelerator detail (Innogrid spec format)
  http.get("/api/v2/clusters/mgmt/accelerators/:id", async ({ params }) => {
    await delay(80);
    const acc = accelerators.find((a) => a.id === params.id);
    if (!acc) return HttpResponse.json({ error: "not found" }, { status: 404 });
    return HttpResponse.json({
      ...meta(),
      accelerator_id: acc.id,
      model: acc.name,
      accelerator_type: acc.kind.toLowerCase(),
      vendor: acc.vendor.toLowerCase(),
      node_name: acc.nodeId,
      pci_bdf: acc.pciAddress,
      driver_version: acc.driverVersion,
      utilization_percent: acc.utilization,
      memory_used_mb: Math.round(acc.memoryGb * 1024 * (acc.utilization / 100)),
      memory_total_mb: acc.memoryGb * 1024,
      power_watts: acc.powerW,
      temperature_celsius: acc.temperatureC,
      partition_mode: acc.partitionMode ?? null,
      allocation_state: acc.allocationState ?? "idle",
      passthrough_to: acc.passthroughTo
        ? {
            vm_id: acc.passthroughTo.vmId,
            service_cluster: acc.passthroughTo.serviceCluster,
            host_pci_bdf: acc.passthroughTo.hostPciBdf,
            state: acc.passthroughTo.state,
          }
        : null,
    });
  }),
];
