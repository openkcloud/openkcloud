import { http, HttpResponse, delay } from "msw";
import { clusters, services, workloads } from "../fixtures";

export const paasHandlers = [
  http.get("/api/paas/clusters", async () => {
    await delay(120);
    return HttpResponse.json({ items: clusters });
  }),
  http.get("/api/paas/clusters/:id", async ({ params }) => {
    await delay(80);
    const c = clusters.find((c) => c.id === params.id);
    if (!c) return HttpResponse.json({ error: "not found" }, { status: 404 });
    return HttpResponse.json({
      ...c,
      workloads: workloads.filter((w) => w.clusterId === c.id),
      services: services.filter((s) => s.clusterId === c.id),
    });
  }),
  http.get("/api/paas/workloads", async ({ request }) => {
    await delay(120);
    const url = new URL(request.url);
    const clusterId = url.searchParams.get("clusterId");
    const namespace = url.searchParams.get("namespace");
    let items = [...workloads];
    if (clusterId && clusterId !== "all") items = items.filter((w) => w.clusterId === clusterId);
    if (namespace && namespace !== "all") items = items.filter((w) => w.namespace === namespace);
    return HttpResponse.json({ items, total: items.length });
  }),
  http.get("/api/paas/services", async () => {
    await delay(100);
    return HttpResponse.json({ items: services });
  }),
];
