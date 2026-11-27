import { http, HttpResponse, delay } from "msw";
import { resourceRequests, orgs, portalUsers, provNetworks, tenants, userProjects, projectAccelerators, flavors, vms } from "../fixtures";
import type { ProvisionSpec, ProvisionStep, ResourceRequest } from "@/lib/types";

// ── 프로비저닝 시뮬레이션 ─────────────────────────────────────────
// 실제: Temporal 워크플로가 Nova 생성 요청 후 서버 task_state(scheduling → networking →
//       block_device_mapping → spawning)와 vm_state 를 폴링해 tkt_provision_step 을 갱신.
// 목업: 승인 시각 기준 경과 시간으로 세부 단계 진행을 계산한다.
const APPROVED_MS = 1500; // 승인 → 워크플로 시작까지
const SUB_MS = 900;       // 세부 단계당 소요

type Sub = { code: string; value: string };
type StepDef = { code: string; subs: Sub[]; skipped?: boolean };

// cmn_code 목업 — 단계(TKT_PROVISION_STEP_IAAS)·상세 단계(TKT_PROVISION_SUBSTEP_IAAS) 이름은 코드 테이블에서 조인
const STEP_TITLE: Record<string, string> = {
  create_server: "가상머신 생성",
  attach_network: "네트워크 연결",
  attach_volume: "스토리지 연결",
  bind_accelerator: "가속기 바인딩",
  wait_active: "부팅 완료",
};
const SUBSTEP_NAME: Record<string, string> = {
  check_flavor: "플레이버 확인",
  check_image: "이미지 확인",
  set_keypair: "키페어 지정",
  send_request: "생성 요청 전송",
  schedule_host: "호스트 스케줄링",
  check_network: "네트워크 확인",
  create_port: "포트 생성",
  assign_ip: "IP 할당",
  apply_secgroup: "보안그룹 적용",
  create_boot_volume: "부팅 볼륨 생성",
  use_local_disk: "로컬 루트 디스크",
  attach_root_disk: "루트 디스크 연결",
  allocate_accelerator: "가속기 할당",
  attach_pci: "PCI 패스스루 연결",
  spawn_instance: "인스턴스 기동",
  check_active: "상태 확인",
  attach_fip: "Floating IP 연결",
};

// 진행 상태: startedAt 부터 fromSub 번째 세부 단계가 진행된다 (재시도는 실패한 큰 단계 처음부터)
const runs: Record<string, { startedAt: number; fromSub: number }> = {
  "req-006": { startedAt: Date.now() - SUB_MS * 5.5, fromSub: 0 },
};

// 실행 회차 (tkt_provision_step.attempt) — 승인 시 1, 재시도마다 +1
const attempts: Record<string, number> = { "req-006": 1, "req-003": 1, "req-008": 1, "req-007": 1 };

function hash(s: string) {
  return [...s].reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7);
}

function hostOf(id: string, cidr: string) {
  return cidr.split("/")[0].replace(/\d+$/, String(10 + (hash(id) % 200)));
}

// 결과값(IP 등)은 티켓별로 결정적 — 진행 중에도 같은 값을 보여준다
function planned(r: ResourceRequest, p: ProvisionSpec) {
  const net = provNetworks.find((n) => n.id === p.networkId);
  const ext = provNetworks.find((n) => n.id === p.floatingNetworkId);
  return {
    fixedIp: r.result?.fixedIp ?? p.fixedIp ?? (net ? hostOf(r.id, net.cidr) : undefined),
    floatingIp: r.result?.floatingIp ?? (ext ? hostOf(r.id + "fip", ext.cidr) : undefined),
  };
}

// 큰 단계 5개는 고정, 세부 단계는 신청·승인 내용에 따라 구성
function buildSteps(r: ResourceRequest): StepDef[] {
  const p = r.provisionSpec;
  if (!p) return [];
  const ip = planned(r, p);
  const h = hash(r.id).toString(16);
  const flavor = flavors.find((f) => f.name === r.flavorName);
  const host = projectAccelerators[r.projectId]?.find((a) => a.label === flavor?.accel?.label)?.node ?? "cpu-node-01";
  const bootVolume = !r.diskGb && !!r.volumeGb;
  // 상세 단계는 조건에 맞는 코드만 등록 (tkt_provision_substep)
  return [
    {
      code: "create_server",
      subs: [
        { code: "check_flavor", value: r.flavorName ?? "—" },
        { code: "check_image", value: r.imageName ?? "—" },
        { code: "set_keypair", value: r.cloudInit === false ? "미적용 (cloud-init 없음)" : r.keypairName ?? "미지정" },
        { code: "send_request", value: "202 Accepted" },
        { code: "schedule_host", value: host },
      ],
    },
    {
      code: "attach_network",
      subs: [
        { code: "check_network", value: `${p.networkName}${p.networkKind === "external" ? " (external)" : ""}` },
        { code: "create_port", value: `port-${hash(r.id + "port").toString(16).slice(-4)}` },
        { code: "assign_ip", value: `${ip.fixedIp ?? "—"}${p.fixedIp ? " (고정)" : ""}` },
        { code: "apply_secgroup", value: p.securityGroups.join(", ") || "default" },
      ],
    },
    {
      code: "attach_volume",
      subs: bootVolume
        ? [
            { code: "create_boot_volume", value: `vol-${h.slice(-4)} · ${r.volumeGb}GB` },
            { code: "attach_root_disk", value: "/dev/vda" },
          ]
        : [
            { code: "use_local_disk", value: `${r.diskGb}GB (플레이버 내장)` },
            { code: "attach_root_disk", value: "/dev/vda" },
          ],
    },
    {
      code: "bind_accelerator",
      skipped: !flavor?.accel,
      subs: flavor?.accel
        ? [
            { code: "allocate_accelerator", value: `${flavor.accel.kind} · ${flavor.accel.label}` },
            { code: "attach_pci", value: "vfio-pci" },
          ]
        : [],
    },
    {
      code: "wait_active",
      subs: [
        { code: "spawn_instance", value: "BUILD → spawning" },
        { code: "check_active", value: "ACTIVE" },
        ...(ip.floatingIp ? [{ code: "attach_fip", value: ip.floatingIp }] : []),
      ],
    },
  ];
}

// 진행 대상 세부 단계 (건너뛴 큰 단계 제외)를 일렬로
function flatSubs(defs: StepDef[]) {
  return defs.flatMap((d, si) => (d.skipped ? [] : d.subs.map((_, i) => ({ si, i }))));
}

// 시드 실패 티켓은 가상머신 생성 · 호스트 스케줄링(schedule_host)에서 실패한 것으로 표시
function failedPos(r: ResourceRequest) {
  const flat = flatSubs(buildSteps(r));
  const k = flat.findIndex((f) => f.si === 0 && f.i === 4);
  return k < 0 ? 0 : k;
}

const iso = (t: number) => new Date(t).toISOString();

// cur = 진행 중인 세부 단계 위치(flat), failAt = 실패 위치, base = 첫 세부 단계 시작 시각
function render(defs: StepDef[], cur: number, base: number, error?: string, failAt?: number): ProvisionStep[] {
  const flat = flatSubs(defs);
  const pos = (si: number, i: number) => flat.findIndex((f) => f.si === si && f.i === i);
  return defs.map((d, si) => {
    if (d.skipped) {
      return { seq: si + 1, code: d.code, message: STEP_TITLE[d.code], status: "skipped", details: [] };
    }
    const first = pos(si, 0);
    const last = pos(si, d.subs.length - 1);
    const subStatus = (k: number) =>
      failAt !== undefined && k === failAt ? "failed"
        : k < cur ? "done"
        : k === cur && failAt === undefined ? "running"
        : "waiting";
    const details = d.subs.map((s, i) => ({ ...s, label: SUBSTEP_NAME[s.code], status: subStatus(pos(si, i)) }));
    const status = details.some((x) => x.status === "failed") ? "failed"
      : details.every((x) => x.status === "done") ? "done"
      : details.some((x) => x.status !== "waiting") ? "running"
      : "waiting";
    const started = status === "waiting" ? undefined : base + first * SUB_MS;
    const ended = status === "done" || status === "failed" ? base + (Math.min(last, failAt ?? last) + 1) * SUB_MS : undefined;
    return {
      seq: si + 1, code: d.code, message: STEP_TITLE[d.code], status, details,
      startedAt: started ? iso(started) : undefined,
      endedAt: ended ? iso(ended) : undefined,
      error: status === "failed" ? error : undefined,
    };
  }) as ProvisionStep[];
}

// 경과 시간에 맞춰 티켓 상태(approved → provisioning → completed) 동기화 후 단계 반환
function sync(r: ResourceRequest): ProvisionStep[] {
  const defs = buildSteps(r);
  const total = flatSubs(defs).length;
  const run = runs[r.id];
  // 시드 완료·실패 티켓은 승인 시각 기준으로 타임라인을 구성
  const seedBase = Date.parse(r.approvedAt ?? r.requestedAt) + APPROVED_MS;
  if (r.status === "completed" && !run) return render(defs, total, seedBase);
  if (r.status === "failed") return render(defs, failedPos(r), seedBase, r.errorMessage, failedPos(r));
  if (!run) return render(defs, -1, seedBase);

  const elapsed = Date.now() - run.startedAt;
  if (elapsed < 0) return render(defs, -1, run.startedAt);
  r.status = "provisioning";
  const cur = run.fromSub + Math.floor(elapsed / SUB_MS);
  const base = run.startedAt - run.fromSub * SUB_MS;
  if (cur >= total) {
    r.status = "completed";
    r.result = { serverId: crypto.randomUUID(), ...planned(r, r.provisionSpec!) };
    delete runs[r.id];
  }
  return render(defs, Math.min(cur, total), base);
}

// VM 이름 중복 — 같은 프로젝트의 기존 VM(실제: Nova 라이브 조회) + 반려되지 않은 VM 티켓
function vmNameTaken(projectId: string, name: string) {
  if (vms.some((v) => v.tenantId === projectId && v.name === name)) return "이미 같은 이름의 가상머신이 있습니다.";
  if (resourceRequests.some((r) => r.type === "vm" && r.projectId === projectId && r.vmName === name && r.status !== "rejected"))
    return "같은 이름으로 신청된 티켓이 있습니다.";
  return null;
}

// Static IP 사용 여부 — 실제: Neutron GET /v2.0/ports?network_id=…&fixed_ips=ip_address=…
// 목업: 게이트웨이(.1) + 다른 티켓이 쓰는(완료 결과 · 진행 중 지정) 고정 IP
function ipInUse(networkId: string, ip: string, exceptId?: string) {
  const net = provNetworks.find((n) => n.id === networkId);
  if (net && ip === net.cidr.split("/")[0].replace(/\d+$/, "1")) return "게이트웨이 주소입니다.";
  const used = resourceRequests.some((r) =>
    r.id !== exceptId && r.provisionSpec?.networkId === networkId &&
    (r.result?.fixedIp === ip || (r.provisionSpec.fixedIp === ip && r.status !== "failed")));
  return used ? "이미 사용 중인 IP입니다." : null;
}

// 화면은 사용자를 username 으로 보낸다 (CORE API 와 동일) — 목업 데이터는 u-00x id 기준
const userIdOf = (username: string | null) => portalUsers.find((u) => u.username === username)?.id;

// 실연동 API 와 경로가 겹치는 목업 — 배포(게이트웨이) 빌드처럼 실제 백엔드를 상대경로로 부를 때는 handlers.ts 에서 뺀다
export const ticketCoreOverlapHandlers = [
  http.get("/api/core/orgs", async () => {
    await delay(80);
    return HttpResponse.json({ items: orgs });
  }),
  // 사용자가 속한 프로젝트 (iam_project_member)
  http.get("/api/core/users/:username/projects", async ({ params }) => {
    await delay(100);
    const ids = userProjects[userIdOf(params.username as string) ?? ""] ?? [];
    const items = tenants
      .filter((t) => ids.includes(t.id))
      .map((t) => ({
        id: t.id,
        name: t.name,
        orgId: t.orgId,
        orgName: orgs.find((o) => o.id === t.orgId)?.name,
        quota: `vCPU ${t.quotaVcpu} · RAM ${t.quotaRamGb} GB · Disk ${t.quotaDiskTb} TB`,
        accelerators: projectAccelerators[t.id] ?? [],
      }));
    return HttpResponse.json({ items });
  }),
];
export const ticketIaasOverlapHandlers = [
  http.get("/api/iaas/networks/:networkId/ip-check", async ({ params, request }) => {
    await delay(150);
    const ip = new URL(request.url).searchParams.get("ip") ?? "";
    const reason = ipInUse(params.networkId as string, ip);
    return HttpResponse.json({ available: !reason, reason });
  }),
];

// 자원 신청(티켓) — VM·서비스 클러스터 공통. 유형은 type("vm" | "cluster")으로 구분한다.
export const ticketHandlers = [
  http.get("/api/ticket/vm-name-check", async ({ request }) => {
    await delay(150);
    const url = new URL(request.url);
    const reason = vmNameTaken(url.searchParams.get("projectId") ?? "", url.searchParams.get("name") ?? "");
    return HttpResponse.json({ available: !reason, reason });
  }),
  http.get("/api/ticket/requests", async ({ request }) => {
    await delay(120);
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const type = url.searchParams.get("type");
    const requestedBy = url.searchParams.get("requestedBy");
    const orgId = url.searchParams.get("orgId");
    resourceRequests.forEach(sync);
    let items = [...resourceRequests];
    if (status && status !== "all") items = items.filter((r) => r.status === status);
    if (type && type !== "all") items = items.filter((r) => r.type === type);
    if (requestedBy) items = items.filter((r) => r.requestedBy === userIdOf(requestedBy));
    if (orgId && orgId !== "all") items = items.filter((r) => r.orgId === orgId);
    return HttpResponse.json({ items, total: items.length });
  }),
  http.post("/api/ticket/requests", async ({ request }) => {
    await delay(300);
    const body = (await request.json()) as Record<string, unknown>;
    // 제출 시 서버에서 이름 중복 재확인
    if (body.type !== "cluster") {
      const reason = vmNameTaken(String(body.projectId), String(body.vmName));
      if (reason) return HttpResponse.json({ message: reason }, { status: 409 });
    }
    // 티켓 조직 = 프로젝트의 조직
    const tenant = tenants.find((t) => t.id === body.projectId);
    const org = orgs.find((o) => o.id === tenant?.orgId);
    const newReq = {
      id: `req-${Date.now()}`,
      type: "vm",
      status: "pending",
      requestedAt: new Date().toISOString(),
      orgId: org?.id,
      orgName: org?.name,
      ...body,
      requestedBy: userIdOf(String(body.requestedBy)) ?? body.requestedBy,
    } as ResourceRequest;
    resourceRequests.unshift(newReq);
    return HttpResponse.json(newReq, { status: 201 });
  }),
  http.patch("/api/ticket/requests/:id", async ({ params, request }) => {
    await delay(400);
    const body = (await request.json()) as {
      action: "approve" | "reject";
      reason?: string;
      provisionSpec?: ProvisionSpec;
      reviewedBy?: string;
      reviewedByName?: string;
    };
    const req = resourceRequests.find((r) => r.id === params.id);
    if (!req) return HttpResponse.json({ message: "not found" }, { status: 404 });
    const now = new Date().toISOString();
    if (body.action === "approve") {
      req.approvedBy = body.reviewedBy;
      req.approvedByName = body.reviewedByName;
      req.approvedAt = now;
      // 승인 시 서버에서 Static IP 충돌 재확인
      const p = body.provisionSpec;
      if (p?.fixedIp) {
        const reason = ipInUse(p.networkId, p.fixedIp, req.id);
        if (reason) return HttpResponse.json({ message: reason }, { status: 409 });
      }
      req.status = "approved";
      req.provisionSpec = body.provisionSpec;
      if (req.type === "vm") {
        runs[req.id] = { startedAt: Date.now() + APPROVED_MS, fromSub: 0 };
        attempts[req.id] = 1;
      }
    } else {
      req.status = "rejected";
      req.approvedRejectBy = body.reviewedBy;
      req.approvedRejectByName = body.reviewedByName;
      req.approvedRejectAt = now;
      req.approvedRejectReason = body.reason;
    }
    return HttpResponse.json({ success: true, id: params.id, action: body.action });
  }),
  http.get("/api/ticket/requests/:id/steps", async ({ params }) => {
    await delay(100);
    const req = resourceRequests.find((r) => r.id === params.id);
    if (!req) return HttpResponse.json({ message: "not found" }, { status: 404 });
    const items = sync(req);
    return HttpResponse.json({ status: req.status, attempt: attempts[req.id] ?? 1, errorMessage: req.errorMessage, result: req.result, items });
  }),
  // 실패 티켓 재시도 — 실패한 큰 단계부터 다시 진행 (목업은 재시도 시 성공)
  http.post("/api/ticket/requests/:id/retry", async ({ params }) => {
    await delay(300);
    const req = resourceRequests.find((r) => r.id === params.id);
    if (!req || req.status !== "failed") return HttpResponse.json({ message: "재시도할 수 없는 상태" }, { status: 409 });
    // Nova 빌드 실패는 서버가 ERROR 로 남으므로 실패한 큰 단계의 처음부터 다시 진행
    const defs = buildSteps(req);
    const flat = flatSubs(defs);
    const failedStep = flat[failedPos(req)]?.si ?? 0;
    runs[req.id] = { startedAt: Date.now(), fromSub: flat.findIndex((f) => f.si === failedStep) };
    attempts[req.id] = (attempts[req.id] ?? 1) + 1;
    req.status = "provisioning";
    req.errorMessage = undefined;
    return HttpResponse.json({ success: true, id: params.id });
  }),
];
