import { http, HttpResponse, delay } from "msw";
import {
  centers,
  zones,
  networks,
  pools,
  tenants,
  vms,
  volumes,
  images,
  iaasNetworks,
  flavors,
  templateImages,
  sshKeys,
  provNetworks,
  securityGroups,
} from "../fixtures";

export const iaasHandlers = [
  http.get("/api/iaas/centers", async () => {
    await delay(80);
    return HttpResponse.json({ items: centers });
  }),
  http.get("/api/iaas/zones", async () => {
    await delay(80);
    return HttpResponse.json({ items: zones });
  }),
  http.get("/api/iaas/networks-base", async () => {
    await delay(80);
    return HttpResponse.json({ items: networks });
  }),
  http.get("/api/iaas/pools", async () => {
    await delay(80);
    return HttpResponse.json({ items: pools });
  }),
  http.get("/api/iaas/projects", async () => {
    await delay(100);
    return HttpResponse.json({ items: tenants });
  }),
  http.get("/api/iaas/instances", async ({ request }) => {
    await delay(120);
    const url = new URL(request.url);
    const tenantId = url.searchParams.get("tenantId");
    const status = url.searchParams.get("status");
    let items = [...vms];
    if (tenantId && tenantId !== "all") items = items.filter((v) => v.tenantId === tenantId);
    if (status && status !== "all") items = items.filter((v) => v.status === status);
    return HttpResponse.json({ items, total: items.length });
  }),
  http.get("/api/iaas/volumes", async () => {
    await delay(100);
    return HttpResponse.json({ items: volumes });
  }),
  http.get("/api/iaas/images", async () => {
    await delay(80);
    return HttpResponse.json({ items: images });
  }),
  http.get("/api/iaas/networks", async () => {
    await delay(80);
    return HttpResponse.json({ items: iaasNetworks });
  }),
  // ── 자원 신청용 라이브 조회 (Nova · Neutron) ──
  http.get("/api/iaas/flavors", async () => {
    await delay(100);
    return HttpResponse.json({ items: flavors });
  }),
  // 템플릿 — Glance 이미지 + cloud-init 여부 · min_disk (이미지 메뉴 목업과 분리)
  http.get("/api/iaas/template-images", async () => {
    await delay(100);
    return HttpResponse.json({ items: templateImages });
  }),
  // 키페어 — 신청자 본인 키만 (iam_ssh_key.owner = username)
  http.get("/api/iaas/keypairs", async ({ request }) => {
    await delay(100);
    const username = new URL(request.url).searchParams.get("username");
    return HttpResponse.json({ items: sshKeys.filter((k) => k.owner === username) });
  }),
  // 승인용 — 티켓 프로젝트 기준: 프로젝트 소유 + shared + external
  http.get("/api/iaas/projects/:projectId/networks", async ({ params }) => {
    await delay(120);
    const items = provNetworks.filter((n) => n.kind !== "project" || n.projectId === params.projectId);
    return HttpResponse.json({ items });
  }),
  http.get("/api/iaas/projects/:projectId/security-groups", async ({ params }) => {
    await delay(120);
    return HttpResponse.json({ items: securityGroups.filter((g) => g.projectId === params.projectId) });
  }),
];
