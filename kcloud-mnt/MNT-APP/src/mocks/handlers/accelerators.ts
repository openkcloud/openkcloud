import { http, HttpResponse, delay } from "msw";
import { accelerators, nodes, pools, zones, centers } from "../fixtures";

export const acceleratorHandlers = [
  http.get("/api/accelerators", async ({ request }) => {
    await delay(120);
    const url = new URL(request.url);
    const vendor = url.searchParams.get("vendor");
    const kind = url.searchParams.get("kind");
    const status = url.searchParams.get("status");
    const q = url.searchParams.get("q")?.toLowerCase();

    let items = [...accelerators];
    if (vendor && vendor !== "all") items = items.filter((a) => a.vendor === vendor);
    if (kind && kind !== "all") items = items.filter((a) => a.kind === kind);
    if (status && status !== "all") items = items.filter((a) => a.status === status);
    if (q)
      items = items.filter(
        (a) => a.name.toLowerCase().includes(q) || a.model.toLowerCase().includes(q),
      );

    return HttpResponse.json({
      items,
      total: items.length,
      meta: {
        byKind: ["GPU", "NPU", "PIM"].map((k) => ({
          kind: k,
          count: accelerators.filter((a) => a.kind === k).length,
        })),
        byVendor: Array.from(new Set(accelerators.map((a) => a.vendor))).map(
          (v) => ({ vendor: v, count: accelerators.filter((a) => a.vendor === v).length }),
        ),
      },
    });
  }),

  http.get("/api/accelerators/topology", async () => {
    await delay(140);
    return HttpResponse.json({
      centers,
      zones,
      pools,
      nodes: nodes.map((n) => ({
        ...n,
        accelerators: accelerators.filter((a) => a.nodeId === n.id),
      })),
    });
  }),

  http.get("/api/accelerators/:id", async ({ params }) => {
    await delay(80);
    const acc = accelerators.find((a) => a.id === params.id);
    if (!acc) return HttpResponse.json({ error: "not found" }, { status: 404 });
    const node = nodes.find((n) => n.id === acc.nodeId);
    return HttpResponse.json({ ...acc, node });
  }),
];
