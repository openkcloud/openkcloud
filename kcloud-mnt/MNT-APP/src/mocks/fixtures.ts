/* Mock fixture — openKcloud CMP 시연용 시드 데이터 */
import type {
  Flavor,
  TemplateImage,
  Org,
  ProvNetwork,
  SecurityGroup,
  Accelerator,
  AcceleratorPartition,
  Activity,
  Alert,
  Center,
  Image,
  IaasNetwork,
  KCluster,
  KFExperiment,
  KFInferenceService,
  KFNotebook,
  KFPipeline,
  KeyPair,
  KService,
  KWorkload,
  ManagementCluster,
  Network,
  Node,
  Pool,
  ResourceMapNode,
  ResourceMapRelationship,
  ResourceRequest,
  ServiceCluster,
  Tenant,
  Volume,
  VM,
  VMGpuPassthrough,
  Zone,
} from "@/lib/types";

// Stable pseudo-random
function rand(seed: number) {
  let s = seed;
  return () => {
    s = (s * 9301 + 49297) % 233280;
    return s / 233280;
  };
}
const r = rand(1234);
const pick = <T>(arr: T[]) => arr[Math.floor(r() * arr.length)];
const intIn = (min: number, max: number) =>
  Math.floor(r() * (max - min + 1)) + min;

// ─────────────────────── Providers ───────────────────────
export const centers: Center[] = [
  { id: "ctr-1", name: "대전 본센터", code: "DJ", location: "대전광역시 유성구", createdAt: "2023-04-15" },
  { id: "ctr-2", name: "판교 클라우드센터", code: "PG", location: "경기도 성남시 분당구", createdAt: "2024-09-20" },
];
export const zones: Zone[] = [
  { id: "zone-1", centerId: "ctr-1", name: "DJ-Z1", code: "DJZ1" },
  { id: "zone-2", centerId: "ctr-1", name: "DJ-Z2", code: "DJZ2" },
  { id: "zone-3", centerId: "ctr-2", name: "PG-Z1", code: "PGZ1" },
  { id: "zone-4", centerId: "ctr-2", name: "PG-Z2", code: "PGZ2" },
];
export const networks: Network[] = [
  { id: "net-1", zoneId: "zone-1", name: "DJ-Z1-Mgmt", class: "관리망", cidr: "10.10.0.0/16", vlan: 100 },
  { id: "net-2", zoneId: "zone-1", name: "DJ-Z1-Svc", class: "서비스망", cidr: "172.16.0.0/16", vlan: 200 },
  { id: "net-3", zoneId: "zone-2", name: "DJ-Z2-Data", class: "데이터망", cidr: "192.168.10.0/24", vlan: 300 },
  { id: "net-4", zoneId: "zone-3", name: "PG-Z1-Svc", class: "서비스망", cidr: "172.20.0.0/16", vlan: 210 },
  { id: "net-5", zoneId: "zone-3", name: "PG-Z1-Data", class: "데이터망", cidr: "192.168.20.0/24", vlan: 310 },
  { id: "net-6", zoneId: "zone-4", name: "PG-Z2-Svc", class: "서비스망", cidr: "172.24.0.0/16", vlan: 220 },
];
export const pools: Pool[] = [
  {
    id: "pool-1",
    name: "DJ-OpenStack-Prod",
    type: "OpenStack",
    zoneId: "zone-1",
    endpoint: "https://os-dj-z1.openkcloud.local:5000/v3",
    status: "정상",
    capacity: { vcpu: 512, ramGb: 2048, diskTb: 80 },
    used: { vcpu: 318, ramGb: 1240, diskTb: 47 },
  },
  {
    id: "pool-2",
    name: "DJ-OpenStack-AI",
    type: "OpenStack",
    zoneId: "zone-2",
    endpoint: "https://os-dj-z2.openkcloud.local:5000/v3",
    status: "정상",
    capacity: { vcpu: 384, ramGb: 3072, diskTb: 120 },
    used: { vcpu: 192, ramGb: 1920, diskTb: 64 },
  },
  {
    id: "pool-3",
    name: "PG-OpenStack-Inference",
    type: "OpenStack",
    zoneId: "zone-3",
    endpoint: "https://os-pg-z1.openkcloud.local:5000/v3",
    status: "정상",
    capacity: { vcpu: 256, ramGb: 1024, diskTb: 60 },
    used: { vcpu: 152, ramGb: 612, diskTb: 32 },
  },
  {
    id: "pool-4",
    name: "PG-OpenShift-Edge",
    type: "OpenShift",
    zoneId: "zone-4",
    endpoint: "https://ocp.openkcloud.local:6443",
    status: "정상",
    capacity: { vcpu: 192, ramGb: 768, diskTb: 40 },
    used: { vcpu: 124, ramGb: 480, diskTb: 22 },
  },
];

// ─────────────────────── 조직 · 사용자 (포털 DB: iam_org · iam_user) ───────────────────────
export const orgs: Org[] = [
  { id: "org-etri",    name: "ETRI",       superUserId: "u-001" },
  { id: "org-okestro", name: "오케스트로", superUserId: "u-005" },
  { id: "org-keti",    name: "KETI",       superUserId: "u-003" },
];

// username = iam_user.username = iam_ssh_key.owner
export const portalUsers = [
  { id: "u-001", username: "admin",   name: "최현화", orgId: "org-etri" },
  { id: "u-002", username: "user",    name: "윤호영", orgId: "org-okestro" },
  { id: "u-003", username: "mj.kim",  name: "김민준", orgId: "org-keti" },
  { id: "u-004", username: "js.park", name: "박지수", orgId: "org-etri" },
  { id: "u-005", username: "jh.lee",  name: "이준호", orgId: "org-okestro" },
];

// SSH 키 (포털 DB: iam_ssh_key — owner = iam_user.username, 개인키 미저장)
// 조직별 키 목록 = iam_ssh_key ⋈ iam_user(username → org_id)
export const sshKeys = [
  { id: "sk-1", owner: "user",    name: "hy-yun-laptop",     type: "SSH-ED25519", fingerprint: "SHA256:8s0k+aFzZ2GxjP9NLcv1XoQnEd8H4RmqA5oV6WuZ2t0", createdAt: "2026-03-15" },
  { id: "sk-2", owner: "user",    name: "hy-yun-desktop",    type: "ECDSA",       fingerprint: "SHA256:R7qWsX8yZ9aB0cD1eF2gH3iJ4kL5mN6oP7qR8sT9uVw", createdAt: "2026-05-20" },
  { id: "sk-3", owner: "jh.lee",  name: "okestro-ops-key",   type: "SSH-ED25519", fingerprint: "SHA256:Lq2W8eR4tY6uI0oP3aS5dF7gH9jK1lZ3xC5vB7nM9qE", createdAt: "2026-06-02" },
  { id: "sk-4", owner: "admin",   name: "etri-shared-01",    type: "SSH-RSA",     fingerprint: "SHA256:X9pQmR3vN2s8YkA4dLcE7BgV1oF6WuHtZjK5NqPxRxo", createdAt: "2026-04-01" },
  { id: "sk-5", owner: "js.park", name: "etri-research-key", type: "SSH-ED25519", fingerprint: "SHA256:Hn3J5kL7mN9pQ1rS3tU5vW7xY9zA1bC3dE5fG7hI9jK", createdAt: "2026-07-11" },
  { id: "sk-6", owner: "mj.kim",  name: "keti-edge-key",     type: "SSH-RSA",     fingerprint: "SHA256:Pz4X6cV8bN0mA2sD4fG6hJ8kL0qW2eR4tY6uI8oP0aS", createdAt: "2026-04-18" },
];

// 사용자-프로젝트 할당 (포털 DB: iam_user_project)
export const userProjects: Record<string, string[]> = {
  "u-002": ["proj-llm-serving"],
  "u-003": ["proj-edge-inference"],
  "u-004": ["proj-ai-research"],
  "u-005": ["proj-llm-serving", "proj-platform-ops"],
};

// 프로젝트별 할당 가속기 (CMP DB: project_accelerator_allocations)
export const projectAccelerators: Record<string, { kind: string; label: string; node: string }[]> = {
  "proj-ai-research": [
    { kind: "NPU", label: "Furiosa RNGD × 2", node: "compute1" },
    { kind: "NPU", label: "Rebellions ATOM × 1", node: "compute1" },
  ],
  "proj-llm-serving": [
    { kind: "GPU", label: "NVIDIA A100 × 1", node: "compute5" },
    { kind: "NPU", label: "Furiosa RNGD × 1", node: "compute5" },
  ],
  "proj-edge-inference": [{ kind: "GPU", label: "NVIDIA L40S × 1", node: "compute6" }],
  "proj-platform-ops": [],
};

// ─────────────────────── Tenants ───────────────────────
export const tenants: Tenant[] = [
  {
    id: "proj-ai-research",
    name: "ai-research",
    orgId: "org-etri",
    description: "초거대 AI 모델 연구 워크로드 (LLaMA·Solar 학습/추론)",
    quotaVcpu: 256,
    quotaRamGb: 1024,
    quotaDiskTb: 30,
    quotaAcc: 8,
    usedVcpu: 184,
    usedRamGb: 768,
    usedDiskTb: 18,
    usedAcc: 6,
    members: 14,
    createdAt: "2025-05-20",
  },
  {
    id: "proj-llm-serving",
    name: "llm-serving",
    orgId: "org-okestro",
    description: "초거대 AI 서빙 운영 (KServe + vLLM)",
    quotaVcpu: 192,
    quotaRamGb: 768,
    quotaDiskTb: 20,
    quotaAcc: 6,
    usedVcpu: 128,
    usedRamGb: 512,
    usedDiskTb: 11,
    usedAcc: 4,
    members: 8,
    createdAt: "2025-06-12",
  },
  {
    id: "proj-edge-inference",
    name: "edge-inference",
    orgId: "org-keti",
    description: "엣지 추론 PoC (Furiosa RNGD)",
    quotaVcpu: 96,
    quotaRamGb: 384,
    quotaDiskTb: 8,
    quotaAcc: 4,
    usedVcpu: 38,
    usedRamGb: 142,
    usedDiskTb: 3,
    usedAcc: 2,
    members: 5,
    createdAt: "2025-08-03",
  },
  {
    id: "proj-platform-ops",
    name: "platform-ops",
    orgId: "org-okestro",
    description: "플랫폼 공통 운영 (모니터링/CICD/로깅)",
    quotaVcpu: 128,
    quotaRamGb: 512,
    quotaDiskTb: 15,
    quotaAcc: 0,
    usedVcpu: 86,
    usedRamGb: 320,
    usedDiskTb: 9,
    usedAcc: 0,
    members: 6,
    createdAt: "2025-04-22",
  },
];

// ─────────────────────── Nodes & Accelerators ───────────────────────
export const nodes: Node[] = [];
export const accelerators: Accelerator[] = [];

function makeAcc(
  spec: Partial<Accelerator>,
  defaults: Partial<Accelerator> = {},
): Accelerator {
  const id = spec.id!;
  return {
    id,
    name: spec.name!,
    vendor: spec.vendor!,
    kind: spec.kind!,
    model: spec.model!,
    memoryGb: spec.memoryGb!,
    nodeId: spec.nodeId!,
    poolId: spec.poolId!,
    status: spec.status ?? "Allocated",
    sharing: spec.sharing ?? "전용",
    multiplexing: spec.multiplexing ?? "1:1",
    utilization: spec.utilization ?? intIn(20, 92),
    temperatureC: spec.temperatureC ?? intIn(45, 78),
    powerW: spec.powerW ?? defaults.powerW ?? 0,
    allocatedToWorkload: spec.allocatedToWorkload,
    allocatedToTenant: spec.allocatedToTenant,
    driverVersion: spec.driverVersion ?? defaults.driverVersion ?? "",
    firmwareVersion: spec.firmwareVersion ?? defaults.firmwareVersion ?? "",
    pciAddress: spec.pciAddress,
    capabilities: spec.capabilities ?? defaults.capabilities ?? [],
  };
}

// Node hostname pool
const buildNode = (
  id: string,
  hostname: string,
  poolId: string,
  zoneId: string,
  accIds: string[],
  spec: Partial<Node> = {},
): Node => ({
  id,
  hostname,
  poolId,
  zoneId,
  status: spec.status ?? "Up",
  vcpu: spec.vcpu ?? 96,
  ramGb: spec.ramGb ?? 512,
  diskTb: spec.diskTb ?? 12,
  acceleratorIds: accIds,
  ipAddress: spec.ipAddress ?? `10.10.${intIn(10, 60)}.${intIn(2, 254)}`,
  powerW: spec.powerW ?? intIn(420, 980),
  uptimeDays: spec.uptimeDays ?? intIn(7, 412),
});

// NVIDIA A100 nodes — 2 nodes × 4 GPUs = 8
for (let n = 1; n <= 2; n++) {
  const nodeId = `node-gpu-${n}`;
  const accIds: string[] = [];
  for (let i = 1; i <= 4; i++) {
    const accId = `acc-a100-${n}-${i}`;
    accIds.push(accId);
    accelerators.push(
      makeAcc(
        {
          id: accId,
          name: "NVIDIA A100 80GB",
          vendor: "NVIDIA",
          kind: "GPU",
          model: "A100-SXM4-80GB",
          memoryGb: 80,
          nodeId,
          poolId: n === 1 ? "pool-2" : "pool-3",
          status: r() > 0.2 ? "Allocated" : "Idle",
          sharing: r() > 0.5 ? "멀티플렉싱" : "전용",
          multiplexing: r() > 0.5 ? "1:N" : "1:1",
          driverVersion: "550.127.05",
          firmwareVersion: "92.00.45.00.05",
          powerW: intIn(280, 410),
          pciAddress: `0000:${(40 + i).toString(16)}:00.0`,
          capabilities: ["CUDA 12.4", "MIG", "NVLink", "FP16/BF16", "TF32"],
          allocatedToTenant: pick(["proj-ai-research", "proj-llm-serving"]),
        },
      ),
    );
  }
  nodes.push(
    buildNode(nodeId, `gpu-node-0${n}.openkcloud`, n === 1 ? "pool-2" : "pool-3", n === 1 ? "zone-2" : "zone-3", accIds, {
      vcpu: 128,
      ramGb: 1024,
      diskTb: 30,
    }),
  );
}

// NVIDIA L40S — 1 node × 4 GPUs (PCIe · Ada Lovelace, NVLink/MIG 없음)
{
  const nodeId = "node-l40s-1";
  const accIds: string[] = [];
  for (let i = 1; i <= 4; i++) {
    const accId = `acc-l40s-${i}`;
    accIds.push(accId);
    accelerators.push(
      makeAcc({
        id: accId,
        name: "NVIDIA L40S 48GB",
        vendor: "NVIDIA",
        kind: "GPU",
        model: "L40S-48GB",
        memoryGb: 48,
        nodeId,
        poolId: "pool-2",
        status: r() > 0.25 ? "Allocated" : "Idle",
        sharing: r() > 0.5 ? "멀티플렉싱" : "전용",
        multiplexing: r() > 0.5 ? "1:N" : "1:1",
        driverVersion: "550.127.05",
        firmwareVersion: "95.02.66.00.01",
        powerW: intIn(240, 350),
        pciAddress: `0000:${(0x60 + i).toString(16)}:00.0`,
        capabilities: ["CUDA 12.4", "FP8", "BF16/FP16", "TF32", "Transformer Engine"],
        allocatedToTenant: pick(["proj-ai-research", "proj-llm-serving"]),
      }),
    );
  }
  nodes.push(
    buildNode(nodeId, "gpu-l40s-01.openkcloud", "pool-2", "zone-2", accIds, {
      vcpu: 96,
      ramGb: 768,
      diskTb: 24,
    }),
  );
}

// Furiosa RNGD — 1 node × 4 NPUs
{
  const nodeId = "node-npu-1";
  const accIds: string[] = [];
  for (let i = 1; i <= 4; i++) {
    const accId = `acc-furiosa-${i}`;
    accIds.push(accId);
    accelerators.push(
      makeAcc({
        id: accId,
        name: "Furiosa RNGD",
        vendor: "Furiosa",
        kind: "NPU",
        model: "RNGD-48GB",
        memoryGb: 48,
        nodeId,
        poolId: "pool-2",
        status: r() > 0.3 ? "Allocated" : "Idle",
        sharing: pick(["전용", "시간분할"]),
        multiplexing: "1:1",
        driverVersion: "furiosa-driver 2.1.0",
        firmwareVersion: "fw-rngd-v1.4.2",
        powerW: intIn(80, 145),
        pciAddress: `0000:b${i}:00.0`,
        capabilities: ["INT8", "FP16", "BF16", "Mixture Precision"],
        allocatedToTenant: pick(["proj-edge-inference", "proj-llm-serving"]),
      }),
    );
  }
  nodes.push(buildNode(nodeId, "npu-node-01.openkcloud", "pool-2", "zone-2", accIds));
}

// Rebellions ATOM — 2 nodes × 6 NPUs = 12
for (let n = 1; n <= 2; n++) {
  const nodeId = `node-atom-${n}`;
  const accIds: string[] = [];
  for (let i = 1; i <= 6; i++) {
    const accId = `acc-rbln-${n}-${i}`;
    accIds.push(accId);
    accelerators.push(
      makeAcc({
        id: accId,
        name: "Rebellions ATOM",
        vendor: "Rebellions",
        kind: "NPU",
        model: "ATOM-32GB",
        memoryGb: 32,
        nodeId,
        poolId: "pool-3",
        status: r() > 0.25 ? "Allocated" : "Idle",
        sharing: pick(["전용", "논리분할"]),
        multiplexing: r() > 0.5 ? "N:1" : "1:1",
        driverVersion: "rbln-driver 1.4.0",
        firmwareVersion: "atom-fw-v3.2.1",
        powerW: intIn(45, 78),
        pciAddress: `0000:c${i}:00.0`,
        capabilities: ["INT8", "INT16", "FP16", "Sparsity"],
        allocatedToTenant: pick(["proj-edge-inference", "proj-llm-serving", "proj-ai-research"]),
      }),
    );
  }
  nodes.push(buildNode(nodeId, `atom-node-0${n}.openkcloud`, "pool-3", "zone-3", accIds));
}

// Samsung HBM-PIM — 1 node × 2 PIMs
{
  const nodeId = "node-pim-1";
  const accIds: string[] = [];
  for (let i = 1; i <= 2; i++) {
    const accId = `acc-pim-${i}`;
    accIds.push(accId);
    accelerators.push(
      makeAcc({
        id: accId,
        name: "Samsung HBM-PIM",
        vendor: "Samsung",
        kind: "PIM",
        model: "HBM-PIM Aquabolt-XL",
        memoryGb: 96,
        nodeId,
        poolId: "pool-2",
        status: r() > 0.5 ? "Allocated" : "Idle",
        sharing: "전용",
        multiplexing: "1:1",
        driverVersion: "pim-driver 0.9.2 (beta)",
        firmwareVersion: "pim-fw-v0.7",
        powerW: intIn(40, 65),
        pciAddress: `0000:d${i}:00.0`,
        capabilities: ["In-memory MAC", "GEMV Acceleration", "Beta Release"],
        allocatedToTenant: pick(["proj-ai-research", undefined]),
      }),
    );
  }
  nodes.push(buildNode(nodeId, "pim-node-01.openkcloud", "pool-2", "zone-2", accIds));
}

// 일반 컴퓨트 노드 (가속기 없음) — CPU 전용 VM 배치용
for (let n = 1; n <= 3; n++) {
  nodes.push(
    buildNode(`node-cpu-${n}`, `cpu-node-0${n}.openkcloud`, "pool-1", "zone-1", [], {
      vcpu: 128,
      ramGb: 512,
      diskTb: 20,
    }),
  );
}

// ─────────────────────── VMs ───────────────────────
const vmNames = [
  "llama-trainer-01",
  "llama-trainer-02",
  "solar-inf-gw",
  "vllm-serving-01",
  "vllm-serving-02",
  "vllm-serving-03",
  "data-loader-01",
  "data-loader-02",
  "research-notebook-01",
  "research-notebook-02",
  "research-notebook-03",
  "edge-inf-furiosa-01",
  "edge-inf-furiosa-02",
  "kfp-runner-01",
  "kfp-runner-02",
  "prometheus-01",
  "elastic-data-01",
  "grafana-01",
  "loki-01",
  "tempo-01",
  "rabbitmq-01",
  "minio-01",
  "harbor-01",
  "harbor-replica",
];
// ─────────────────────── KeyPairs (SSH) ───────────────────────
// VM보다 먼저 정의 — VM.keyName이 이 목록을 참조. usedBy는 vms 생성 후 계산.
export const keypairs: KeyPair[] = [
  { id: "kp-001", name: "hy-yun-laptop",  type: "SSH-ED25519", fingerprint: "SHA256:8s0k+aFzZ2GxjP9NLcv1XoQnEd8H4RmqA5oV6WuZ2t0", owner: "hy.yun",    project: "proj-ai-research",   createdAt: "2026-03-15", usedBy: 0 },
  { id: "kp-002", name: "etri-shared-01", type: "SSH-RSA",     fingerprint: "SHA256:X9pQmR3vN2s8YkA4dLcE7BgV1oF6WuHtZjK5NqPxRxo", owner: "hhchoi",    project: "proj-llm-serving",   createdAt: "2026-04-01", usedBy: 0 },
  { id: "kp-003", name: "ci-runner-key",  type: "SSH-ED25519", fingerprint: "SHA256:Z1kL5nM8pQrS2tU3vW4xY5zA6bC7dE8fG9hI0jK1lMn", owner: "system.ci", project: "proj-platform-ops",  createdAt: "2026-02-10", usedBy: 0 },
  { id: "kp-004", name: "hy-yun-desktop", type: "ECDSA",       fingerprint: "SHA256:R7qWsX8yZ9aB0cD1eF2gH3iJ4kL5mN6oP7qR8sT9uVw", owner: "hy.yun",    project: "proj-ai-research",   createdAt: "2026-05-20", usedBy: 0 },
];

export const vms: VM[] = vmNames.map((name, i) => {
  const tenant = pick(tenants);
  const pool = pick(pools);
  const node = pick(nodes.filter((n) => n.poolId === pool.id)) || nodes[0];
  const kp = pick(keypairs); // 키페어 소유자 = VM 소유자 (정합성 보장)
  return {
    id: `vm-${(i + 1).toString().padStart(3, "0")}`,
    name,
    tenantId: tenant.id,
    poolId: pool.id,
    nodeId: node.id,
    flavor: pick(["m1.large", "m1.xlarge", "m2.2xlarge", "g1.4xlarge", "n1.npu-2", "n2.atom-4"]),
    vcpu: pick([8, 16, 32, 64]),
    ramGb: pick([32, 64, 128, 256]),
    diskGb: pick([100, 200, 500, 1000]),
    os: pick(["Ubuntu 22.04", "Ubuntu 24.04", "RHEL 9", "Rocky Linux 9"]),
    status: pick(["Running", "Running", "Running", "Running", "Stopped", "Building"]),
    ipAddresses: [`172.16.${intIn(0, 250)}.${intIn(2, 254)}`],
    // 인터넷망+공인 IP를 신청한 일부 VM만 Floating IP(공인 대역 192.168.0.0/24) 보유
    publicIp: i % 3 === 0 ? `192.168.0.${intIn(10, 250)}` : undefined,
    // 부팅 시 주입된 키페어 — keypairs 목록에서 참조 (단일 소스)
    keyName: kp.name,
    owner: kp.owner,
    acceleratorIds: name.includes("trainer") || name.includes("vllm") || name.includes("furiosa") ? [pick(accelerators).id] : [],
    createdAt: new Date(Date.now() - intIn(2, 200) * 86400000).toISOString(),
    uptimeHours: intIn(4, 2400),
  };
});

// 키페어별 usedBy = 실제 이 키페어로 부팅된 VM 수
keypairs.forEach((k) => {
  k.usedBy = vms.filter((v) => v.keyName === k.name).length;
});

// ─────────────────────── Volumes ───────────────────────
export const volumes: Volume[] = Array.from({ length: 28 }, (_, i) => ({
  id: `vol-${(i + 1).toString().padStart(3, "0")}`,
  name: `volume-${pick(["data", "model", "ckpt", "logs", "boot"])}-${(i + 1).toString().padStart(2, "0")}`,
  tenantId: pick(tenants).id,
  poolId: pick(pools).id,
  sizeGb: pick([50, 100, 200, 500, 1000, 2000]),
  type: pick(["ssd", "nvme", "hdd"]),
  attachedVmId: r() > 0.3 ? pick(vms).id : undefined,
  bootable: r() > 0.7,
  status: pick(["in-use", "in-use", "in-use", "available"]),
}));

// ─────────────────────── IaaS Networks ───────────────────────
export const iaasNetworks: IaasNetwork[] = [
  {
    id: "in-1",
    name: "ai-research-internal",
    tenantId: "proj-ai-research",
    poolId: "pool-2",
    cidr: "192.168.100.0/24",
    type: "tenant",
    subnets: 2,
    ports: 14,
  },
  {
    id: "in-2",
    name: "llm-serving-external",
    tenantId: "proj-llm-serving",
    poolId: "pool-2",
    cidr: "10.20.0.0/24",
    type: "external",
    subnets: 1,
    ports: 8,
  },
  {
    id: "in-3",
    name: "edge-inference-vlan",
    tenantId: "proj-edge-inference",
    poolId: "pool-3",
    cidr: "172.30.10.0/24",
    type: "tenant",
    vlan: 410,
    subnets: 1,
    ports: 5,
  },
  {
    id: "in-4",
    name: "platform-shared",
    tenantId: "proj-platform-ops",
    poolId: "pool-1",
    cidr: "10.50.0.0/22",
    type: "shared",
    subnets: 4,
    ports: 22,
  },
];

// ─────────────────────── Images ───────────────────────
export const images: Image[] = [
  { id: "img-1", name: "ubuntu-22.04-server", os: "Ubuntu 22.04 LTS", arch: "x86_64", sizeMb: 2456, visibility: "public", format: "qcow2", hasAcceleratorRuntime: false, createdAt: "2025-04-20" },
  { id: "img-2", name: "ubuntu-24.04-cuda-12.4", os: "Ubuntu 24.04 + CUDA", arch: "x86_64", sizeMb: 8612, visibility: "public", format: "qcow2", hasAcceleratorRuntime: true, createdAt: "2025-09-12" },
  { id: "img-3", name: "rhel-9-furiosa-sdk", os: "RHEL 9 + Furiosa SDK", arch: "x86_64", sizeMb: 6240, visibility: "shared", format: "qcow2", hasAcceleratorRuntime: true, createdAt: "2025-10-04" },
  { id: "img-4", name: "rocky-9-rebellions-rt", os: "Rocky 9 + Rebellions Runtime", arch: "x86_64", sizeMb: 5980, visibility: "shared", format: "qcow2", hasAcceleratorRuntime: true, createdAt: "2025-11-15" },
  { id: "img-5", name: "openkcloud-kubeflow-base", os: "Ubuntu 24.04 + Kubeflow Notebook", arch: "x86_64", sizeMb: 7820, visibility: "private", format: "qcow2", hasAcceleratorRuntime: true, createdAt: "2026-01-22" },
  { id: "img-6", name: "vllm-serving-runtime", os: "Ubuntu 24.04 + vLLM + Triton", arch: "x86_64", sizeMb: 12480, visibility: "private", format: "qcow2", hasAcceleratorRuntime: true, createdAt: "2026-02-18" },
];

// ─────────────────────── 템플릿 이미지 (자원 신청 전용 · Glance 라이브 조회 대상) ───────────────────────
// cloudInit = 이미지 속성 기준 cloud-init 포함 여부, minDiskGb = Glance min_disk
export const templateImages: TemplateImage[] = [
  { id: "img-1", name: "ubuntu-22.04-server", os: "Ubuntu 22.04 LTS", arch: "x86_64", sizeMb: 2456, visibility: "public", format: "qcow2", hasAcceleratorRuntime: false, cloudInit: true, minDiskGb: 10, createdAt: "2025-04-20" },
  { id: "img-2", name: "ubuntu-24.04-cuda-12.4", os: "Ubuntu 24.04 + CUDA", arch: "x86_64", sizeMb: 8612, visibility: "public", format: "qcow2", hasAcceleratorRuntime: true, cloudInit: true, minDiskGb: 40, createdAt: "2025-09-12" },
  { id: "img-3", name: "rhel-9-furiosa-sdk", os: "RHEL 9 + Furiosa SDK", arch: "x86_64", sizeMb: 6240, visibility: "shared", format: "qcow2", hasAcceleratorRuntime: true, cloudInit: true, minDiskGb: 40, createdAt: "2025-10-04" },
  { id: "img-4", name: "rocky-9-rebellions-rt", os: "Rocky 9 + Rebellions Runtime", arch: "x86_64", sizeMb: 5980, visibility: "shared", format: "qcow2", hasAcceleratorRuntime: true, cloudInit: true, minDiskGb: 40, createdAt: "2025-11-15" },
  { id: "img-5", name: "openkcloud-kubeflow-base", os: "Ubuntu 24.04 + Kubeflow Notebook", arch: "x86_64", sizeMb: 7820, visibility: "private", format: "qcow2", hasAcceleratorRuntime: true, cloudInit: true, minDiskGb: 50, createdAt: "2026-01-22" },
  { id: "img-6", name: "vllm-serving-runtime", os: "Ubuntu 24.04 + vLLM + Triton", arch: "x86_64", sizeMb: 12480, visibility: "private", format: "qcow2", hasAcceleratorRuntime: true, cloudInit: true, minDiskGb: 80, createdAt: "2026-02-18" },
  // cloud-init 미포함 — 키페어 주입 불가, 이미지 기본 계정으로 접속
  { id: "img-7", name: "centos-7-legacy-appliance", os: "CentOS 7 (Appliance)", arch: "x86_64", sizeMb: 1840, visibility: "public", format: "qcow2", hasAcceleratorRuntime: false, cloudInit: false, minDiskGb: 20, createdAt: "2024-11-02" },
  { id: "img-8", name: "windows-server-2022", os: "Windows Server 2022", arch: "x86_64", sizeMb: 10240, visibility: "public", format: "qcow2", hasAcceleratorRuntime: false, cloudInit: false, minDiskGb: 60, createdAt: "2025-03-10" },
];

// ─────────────────────── Flavors (Nova 라이브 조회 대상) ───────────────────────
// diskGb 0 = 루트 디스크 없음 → 신청 시 부팅 볼륨 크기 입력(boot-from-volume)
// 가속기는 extra spec(pci_passthrough:alias / Cyborg)에 내장
export const flavors: Flavor[] = [
  { id: "flv-1",  name: "m1.small",   vcpu: 4,  ramGb: 16,  diskGb: 100,  accel: null },
  { id: "flv-2",  name: "m1.large",   vcpu: 8,  ramGb: 32,  diskGb: 200,  accel: null },
  { id: "flv-3",  name: "m1.xlarge",  vcpu: 16, ramGb: 64,  diskGb: 500,  accel: null },
  { id: "flv-4",  name: "m1.2xlarge", vcpu: 32, ramGb: 128, diskGb: 1000, accel: null },
  { id: "flv-5",  name: "v1.small",   vcpu: 4,  ramGb: 16,  diskGb: 0,    accel: null },
  { id: "flv-6",  name: "v1.large",   vcpu: 8,  ramGb: 32,  diskGb: 0,    accel: null },
  { id: "flv-7",  name: "g1.a100",    vcpu: 32, ramGb: 128, diskGb: 1000, accel: { kind: "GPU", label: "NVIDIA A100 × 1" } },
  { id: "flv-8",  name: "g1.l40s",    vcpu: 16, ramGb: 64,  diskGb: 500,  accel: { kind: "GPU", label: "NVIDIA L40S × 1" } },
  { id: "flv-9",  name: "g1.l40s.v",  vcpu: 16, ramGb: 64,  diskGb: 0,    accel: { kind: "GPU", label: "NVIDIA L40S × 1" } },
  { id: "flv-10", name: "n1.rngd-1",  vcpu: 16, ramGb: 64,  diskGb: 500,  accel: { kind: "NPU", label: "Furiosa RNGD × 1" } },
  { id: "flv-11", name: "n1.rngd-2",  vcpu: 32, ramGb: 128, diskGb: 1000, accel: { kind: "NPU", label: "Furiosa RNGD × 2" } },
  { id: "flv-12", name: "n1.atom-1",  vcpu: 16, ramGb: 64,  diskGb: 500,  accel: { kind: "NPU", label: "Rebellions ATOM × 1" } },
];

// ─────────────────────── 승인용 네트워크 · 보안그룹 (Neutron 라이브 조회 대상) ───────────────────────
// kind: project(프로젝트 소유) · shared(공유) · external(router:external — FIP 풀/직접 연결)
export const provNetworks: ProvNetwork[] = [
  { id: "net-ai-int",   name: "ai-research-net",   kind: "project",  projectId: "proj-ai-research",    cidr: "192.168.100.0/24" },
  { id: "net-llm-int",  name: "llm-serving-net",   kind: "project",  projectId: "proj-llm-serving",    cidr: "192.168.110.0/24" },
  { id: "net-edge-int", name: "edge-inference-net", kind: "project", projectId: "proj-edge-inference", cidr: "172.30.10.0/24" },
  { id: "net-ops-int",  name: "platform-ops-net",  kind: "project",  projectId: "proj-platform-ops",   cidr: "192.168.120.0/24" },
  { id: "net-shared",   name: "platform-shared",   kind: "shared",   projectId: "proj-platform-ops",   cidr: "10.50.0.0/22" },
  { id: "net-ext",      name: "ext-net",           kind: "external", projectId: "admin",               cidr: "203.0.113.0/24" },
  { id: "net-ext-svc",  name: "ext-svc-net",       kind: "external", projectId: "admin",               cidr: "198.51.100.0/24" },
];

export const securityGroups: SecurityGroup[] = [
  { id: "sg-ai-default",   name: "default",   projectId: "proj-ai-research",    description: "프로젝트 기본 — 같은 그룹 내 통신 허용" },
  { id: "sg-ai-ssh",       name: "allow-ssh", projectId: "proj-ai-research",    description: "TCP 22 인바운드 허용" },
  { id: "sg-llm-default",  name: "default",   projectId: "proj-llm-serving",    description: "프로젝트 기본 — 같은 그룹 내 통신 허용" },
  { id: "sg-llm-ssh",      name: "allow-ssh", projectId: "proj-llm-serving",    description: "TCP 22 인바운드 허용" },
  { id: "sg-llm-web",      name: "allow-web", projectId: "proj-llm-serving",    description: "TCP 80·443 인바운드 허용" },
  { id: "sg-edge-default", name: "default",   projectId: "proj-edge-inference", description: "프로젝트 기본 — 같은 그룹 내 통신 허용" },
  { id: "sg-edge-ssh",     name: "allow-ssh", projectId: "proj-edge-inference", description: "TCP 22 인바운드 허용" },
  { id: "sg-ops-default",  name: "default",   projectId: "proj-platform-ops",   description: "프로젝트 기본 — 같은 그룹 내 통신 허용" },
];

// ─────────────────────── K8s Clusters ───────────────────────
export const clusters: KCluster[] = [
  {
    id: "k8s-1",
    name: "openkcloud-ai-prod",
    provider: "OpenStack",
    poolId: "pool-2",
    zoneId: "zone-2",
    version: "1.30.4",
    status: "Ready",
    nodeCount: 12,
    controlPlanes: 3,
    workers: 9,
    cni: "Calico",
    totalCpu: 384,
    totalMemGb: 3072,
    totalAcc: 18,
    usedCpu: 248,
    usedMemGb: 1980,
    usedAcc: 12,
    createdAt: "2025-06-04",
  },
  {
    id: "k8s-2",
    name: "openkcloud-edge-staging",
    provider: "OpenShift",
    poolId: "pool-4",
    zoneId: "zone-4",
    version: "1.29.6",
    status: "Ready",
    nodeCount: 6,
    controlPlanes: 3,
    workers: 3,
    cni: "OVN-Kubernetes",
    totalCpu: 192,
    totalMemGb: 768,
    totalAcc: 6,
    usedCpu: 108,
    usedMemGb: 412,
    usedAcc: 3,
    createdAt: "2025-08-19",
  },
  {
    id: "k8s-3",
    name: "openkcloud-platform-ops",
    provider: "OpenStack",
    poolId: "pool-1",
    zoneId: "zone-1",
    version: "1.30.4",
    status: "Upgrading",
    nodeCount: 5,
    controlPlanes: 3,
    workers: 2,
    cni: "Cilium",
    totalCpu: 96,
    totalMemGb: 384,
    totalAcc: 0,
    usedCpu: 64,
    usedMemGb: 244,
    usedAcc: 0,
    createdAt: "2025-04-22",
  },
];

// ─────────────────────── K8s Workloads ───────────────────────
const wlSpec: Array<Partial<KWorkload>> = [
  { name: "llama-3-70b-trainer", kind: "Job", namespace: "ai-research", replicas: 4, image: "openkcloud/llama-train:0.6.2", cpu: "32", mem: "256Gi", accelerators: 4 },
  { name: "vllm-solar-10b", kind: "Deployment", namespace: "llm-serving", replicas: 3, image: "openkcloud/vllm:0.6.5", cpu: "16", mem: "128Gi", accelerators: 1 },
  { name: "kserve-furiosa-rngd", kind: "Deployment", namespace: "llm-serving", replicas: 2, image: "openkcloud/kserve-furiosa:1.2.0", cpu: "8", mem: "32Gi", accelerators: 1 },
  { name: "kserve-atom-stt", kind: "Deployment", namespace: "edge-inference", replicas: 2, image: "openkcloud/kserve-rbln:0.9.1", cpu: "8", mem: "16Gi", accelerators: 1 },
  { name: "kubeflow-notebook-server", kind: "StatefulSet", namespace: "kubeflow", replicas: 6, image: "kubeflow/codeserver-python:v1.10.0-cuda12.4", cpu: "4", mem: "32Gi", accelerators: 1 },
  { name: "tempo-distributed", kind: "StatefulSet", namespace: "observability", replicas: 3, image: "grafana/tempo:2.4.1", cpu: "2", mem: "16Gi", accelerators: 0 },
  { name: "prometheus-operator", kind: "Deployment", namespace: "monitoring", replicas: 2, image: "prom/prometheus:v2.55.0", cpu: "2", mem: "8Gi", accelerators: 0 },
  { name: "loki-write", kind: "StatefulSet", namespace: "observability", replicas: 3, image: "grafana/loki:3.2.0", cpu: "2", mem: "16Gi", accelerators: 0 },
  { name: "grafana", kind: "Deployment", namespace: "observability", replicas: 1, image: "grafana/grafana:10.4.7", cpu: "1", mem: "4Gi", accelerators: 0 },
  { name: "harbor-core", kind: "Deployment", namespace: "harbor", replicas: 2, image: "goharbor/harbor-core:v2.10.3", cpu: "1", mem: "2Gi", accelerators: 0 },
  { name: "argocd-server", kind: "Deployment", namespace: "argocd", replicas: 2, image: "argoproj/argocd:v2.13.1", cpu: "1", mem: "2Gi", accelerators: 0 },
  { name: "katib-controller", kind: "Deployment", namespace: "kubeflow", replicas: 1, image: "kubeflow/katib-controller:v0.18.0", cpu: "1", mem: "2Gi", accelerators: 0 },
  { name: "minio", kind: "StatefulSet", namespace: "platform-ops", replicas: 4, image: "minio/minio:RELEASE.2025-02-12", cpu: "2", mem: "8Gi", accelerators: 0 },
  { name: "data-prep-cronjob", kind: "CronJob", namespace: "ai-research", replicas: 1, image: "openkcloud/data-prep:2.1", cpu: "8", mem: "32Gi", accelerators: 0 },
  { name: "log-rotator", kind: "DaemonSet", namespace: "platform-ops", replicas: 12, image: "fluent/fluent-bit:3.1.7", cpu: "0.2", mem: "256Mi", accelerators: 0 },
];

export const workloads: KWorkload[] = wlSpec.map((spec, i) => {
  const cluster = pick(clusters);
  const ready = Math.max(0, (spec.replicas ?? 1) - (r() > 0.85 ? 1 : 0));
  return {
    id: `wl-${(i + 1).toString().padStart(3, "0")}`,
    name: spec.name!,
    clusterId: cluster.id,
    namespace: spec.namespace!,
    kind: spec.kind!,
    replicas: spec.replicas ?? 1,
    ready,
    image: spec.image!,
    cpu: spec.cpu!,
    mem: spec.mem!,
    accelerators: spec.accelerators ?? 0,
    status: spec.kind === "CronJob" ? "Succeeded" : ready === spec.replicas ? "Running" : ready > 0 ? "Running" : "Pending",
    createdAt: new Date(Date.now() - intIn(1, 80) * 86400000).toISOString(),
    labels: { app: spec.name!.split("-")[0], team: spec.namespace! },
  };
});

// ─────────────────────── K8s Services ───────────────────────
export const services: KService[] = workloads.slice(0, 12).map((w, i) => ({
  id: `svc-${(i + 1).toString().padStart(3, "0")}`,
  name: `${w.name}-svc`,
  clusterId: w.clusterId,
  namespace: w.namespace,
  type: pick(["ClusterIP", "ClusterIP", "LoadBalancer", "Ingress", "NodePort"]),
  ports: [{ port: pick([80, 443, 8080, 8443, 9090, 3000]), targetPort: 8080, protocol: "TCP" }],
  endpoints: w.ready,
  externalIp: r() > 0.6 ? `203.0.113.${intIn(2, 250)}` : undefined,
}));

// ─────────────────────── Kubeflow ───────────────────────
export const notebooks: KFNotebook[] = [
  { id: "nb-1", name: "research-llama-finetune", ownerId: "u-003", namespace: "ai-research", image: "kubeflow/codeserver-python:v1.10.0-cuda12.4", cpu: "16", memGb: 64, acceleratorKind: "GPU", acceleratorCount: 2, storageGb: 500, status: "Running", url: "/ai-development/notebooks/nb-1", createdAt: "2026-04-12", lastActivity: "2026-05-31" },
  { id: "nb-2", name: "solar-data-prep", ownerId: "u-003", namespace: "ai-research", image: "kubeflow/codeserver-python:v1.10.0", cpu: "8", memGb: 32, acceleratorCount: 0, storageGb: 200, status: "Running", url: "/ai-development/notebooks/nb-2", createdAt: "2026-05-02", lastActivity: "2026-05-30" },
  { id: "nb-3", name: "vllm-benchmark", ownerId: "u-002", namespace: "llm-serving", image: "openkcloud/kubeflow-vllm:0.6.5", cpu: "8", memGb: 64, acceleratorKind: "GPU", acceleratorCount: 1, storageGb: 100, status: "Running", url: "/ai-development/notebooks/nb-3", createdAt: "2026-04-28", lastActivity: "2026-05-29" },
  { id: "nb-4", name: "rebellions-atom-finetune", ownerId: "u-003", namespace: "edge-inference", image: "openkcloud/kubeflow-rbln:1.4.0", cpu: "4", memGb: 16, acceleratorKind: "NPU", acceleratorCount: 1, storageGb: 100, status: "Starting", url: "/ai-development/notebooks/nb-4", createdAt: "2026-05-31", lastActivity: "2026-05-31" },
  { id: "nb-5", name: "rebellions-stt-dev", ownerId: "u-003", namespace: "edge-inference", image: "openkcloud/kubeflow-rbln:1.4.0", cpu: "4", memGb: 16, acceleratorKind: "NPU", acceleratorCount: 1, storageGb: 100, status: "Stopped", url: "/ai-development/notebooks/nb-5", createdAt: "2026-03-15", lastActivity: "2026-05-20" },
  { id: "nb-6", name: "pim-experiments", ownerId: "u-003", namespace: "ai-research", image: "openkcloud/kubeflow-pim:0.7", cpu: "8", memGb: 64, acceleratorKind: "PIM", acceleratorCount: 1, storageGb: 200, status: "Running", url: "/ai-development/notebooks/nb-6", createdAt: "2026-04-30", lastActivity: "2026-05-31" },
  { id: "nb-7", name: "datasets-explore", ownerId: "u-002", namespace: "ai-research", image: "kubeflow/codeserver-python:v1.10.0", cpu: "4", memGb: 16, acceleratorCount: 0, storageGb: 50, status: "Running", url: "/ai-development/notebooks/nb-7", createdAt: "2026-05-10", lastActivity: "2026-05-31" },
];

export const pipelines: KFPipeline[] = [
  { id: "pl-1", name: "llama-3-finetune-pipeline", description: "데이터 전처리 → 분산 학습 → 평가 → KServe 배포", ownerId: "u-003", steps: 6, runs: 14, successRate: 0.92, lastRun: "2026-05-30", acceleratorKind: "GPU", status: "Active" },
  { id: "pl-2", name: "stt-rebellions-pipeline", description: "음성 데이터 정제 → ATOM 양자화 → 추론 검증", ownerId: "u-003", steps: 5, runs: 8, successRate: 0.87, lastRun: "2026-05-28", acceleratorKind: "NPU", status: "Active" },
  { id: "pl-3", name: "vllm-bench-pipeline", description: "vLLM 추론 벤치마크 자동 실행", ownerId: "u-002", steps: 4, runs: 21, successRate: 0.95, lastRun: "2026-05-31", acceleratorKind: "GPU", status: "Active" },
  { id: "pl-4", name: "pim-gemv-experiment", description: "HBM-PIM에서 GEMV 가속 실험", ownerId: "u-003", steps: 3, runs: 6, successRate: 0.5, lastRun: "2026-05-25", acceleratorKind: "PIM", status: "Active" },
];

export const experiments: KFExperiment[] = [
  { id: "ex-1", name: "llama-lr-search", pipelineId: "pl-1", ownerId: "u-003", objective: "maximize eval_accuracy", goal: 0.88, bestMetric: 0.864, trials: 24, status: "Running", createdAt: "2026-05-22" },
  { id: "ex-2", name: "vllm-batch-size-tune", pipelineId: "pl-3", ownerId: "u-002", objective: "minimize p95_latency_ms", goal: 80, bestMetric: 92, trials: 18, status: "Succeeded", createdAt: "2026-05-15" },
  { id: "ex-3", name: "rbln-quant-precision", pipelineId: "pl-2", ownerId: "u-003", objective: "minimize WER", goal: 0.05, bestMetric: 0.058, trials: 12, status: "Running", createdAt: "2026-05-27" },
];

export const inferenceServices: KFInferenceService[] = [
  { id: "is-1", name: "solar-10b-vllm", modelName: "solar-10b-chat", modelVersion: "v1.2", ownerId: "u-002", acceleratorKind: "GPU", replicas: 3, rpsAvg: 124, p95LatencyMs: 980, status: "Ready", endpoint: "https://api.openkcloud.local/v1/solar-10b" },
  { id: "is-2", name: "stt-rngd-prod", modelName: "korean-stt-large", modelVersion: "v3.1", ownerId: "u-002", acceleratorKind: "NPU", replicas: 2, rpsAvg: 56, p95LatencyMs: 420, status: "Ready", endpoint: "https://api.openkcloud.local/v1/stt-rngd" },
  { id: "is-3", name: "stt-atom-edge", modelName: "korean-stt-small", modelVersion: "v2.4", ownerId: "u-003", acceleratorKind: "NPU", replicas: 4, rpsAvg: 88, p95LatencyMs: 220, status: "Ready", endpoint: "https://api.openkcloud.local/v1/stt-atom" },
];

// ─────────────────────── Activities & Alerts ───────────────────────
export const activities: Activity[] = [
  { id: "act-1", ts: "2026-05-31T14:22:00", actor: "윤호영", action: "VM 시작", target: "llama-trainer-02", result: "success" },
  { id: "act-2", ts: "2026-05-31T14:18:00", actor: "박의규", action: "Kubeflow Notebook 생성", target: "furiosa-poc", result: "success" },
  { id: "act-3", ts: "2026-05-31T13:50:00", actor: "최현화", action: "GPU 할당 변경", target: "acc-a100-1-3 → vllm-solar-10b", result: "success" },
  { id: "act-4", ts: "2026-05-31T13:32:00", actor: "system-scheduler", action: "워크로드 재배치", target: "kserve-atom-stt → node-atom-2", result: "success" },
  { id: "act-5", ts: "2026-05-31T12:40:00", actor: "박의규", action: "API Key 발급", target: "kfp-runner-bot", result: "success" },
  { id: "act-6", ts: "2026-05-31T11:18:00", actor: "system-monitor", action: "고온 경보", target: "acc-a100-1-2 (78°C)", result: "warning" },
  { id: "act-7", ts: "2026-05-31T09:24:00", actor: "윤호영", action: "프로젝트 쿼터 변경", target: "proj-ai-research (+2 NPU)", result: "success" },
  { id: "act-8", ts: "2026-05-30T22:10:00", actor: "system-scheduler", action: "야간 자동 스케일 다운", target: "openkcloud-ai-prod (-2 nodes)", result: "success" },
];

export const alerts: Alert[] = [
  { id: "al-1", severity: "warning", ts: "2026-05-31T11:18:00", source: "acc-a100-1-2", message: "GPU 온도 78°C 임계치 근접", acknowledged: false },
  { id: "al-2", severity: "critical", ts: "2026-05-31T08:45:00", source: "pool-3", message: "OpenStack Neutron API 지연 (1.4s) — neutron-server 재시작 권장", acknowledged: false },
  { id: "al-3", severity: "info", ts: "2026-05-31T07:00:00", source: "k8s-3", message: "openkcloud-platform-ops 클러스터 1.30.5 업그레이드 진행 중", acknowledged: true },
  { id: "al-4", severity: "warning", ts: "2026-05-30T19:32:00", source: "proj-llm-serving", message: "테넌트 GPU 쿼터 92% 도달", acknowledged: false },
];

// 24h power & cost time series for dashboard
export function makePowerSeries() {
  const now = new Date();
  return Array.from({ length: 24 }, (_, i) => {
    const t = new Date(now.getTime() - (23 - i) * 3600 * 1000);
    const base = 8400 + Math.sin((i / 24) * Math.PI * 2) * 1800;
    return {
      time: `${t.getHours().toString().padStart(2, "0")}:00`,
      kw: Math.round((base + (r() - 0.5) * 600) * 10) / 10 / 1000,
      cost: Math.round((base + (r() - 0.5) * 600) * 0.18),
    };
  });
}
export function makeUtilSeries() {
  return Array.from({ length: 7 }, (_, i) => {
    const day = new Date(Date.now() - (6 - i) * 86400000);
    return {
      day: `${day.getMonth() + 1}/${day.getDate()}`,
      gpu: 40 + Math.round(r() * 50),
      npu: 30 + Math.round(r() * 40),
      pim: 10 + Math.round(r() * 25),
    };
  });
}

// ═══════════════════════════════════════════════════════════════
// 이노그리드 KCloud Monitor v2 — 6계층 자원 모델 보강 데이터
// ═══════════════════════════════════════════════════════════════

// 1) 관리 클러스터 1개 + 서비스 클러스터 2개
export const managementClusters: ManagementCluster[] = [
  {
    id: "mgmt",
    name: "openkcloud-mgmt",
    type: "management",
    description: "관리 클러스터 — 물리 인프라 + OpenStack 제어면",
    nodeCount: nodes.length,
    gpuCount: accelerators.length,
    totalPowerWatts: nodes.reduce((s, n) => s + n.powerW, 0),
    hasOpenstack: true,
    serviceClusters: ["svc-ai-prod", "svc-edge-staging"],
    status: "healthy",
  },
];

export const serviceClusters: ServiceCluster[] = [
  {
    id: "svc-ai-prod",
    name: "openkcloud-ai-prod",
    type: "service",
    parentCluster: "mgmt",
    openstackProject: "proj-ai-research",
    description: "초거대 AI 학습/추론 서비스 클러스터 (Magnum)",
    k8sVersion: "1.30.4",
    cni: "Calico",
    nodeCount: 8,
    vmIds: [],   // populated below
    gpuCount: 6,
    totalCpu: 256,
    totalMemGb: 2048,
    usedCpu: 184,
    usedMemGb: 1432,
    totalPowerWatts: 1840,
    status: "healthy",
    createdAt: "2025-06-04",
  },
  {
    id: "svc-edge-staging",
    name: "openkcloud-edge-staging",
    type: "service",
    parentCluster: "mgmt",
    openstackProject: "proj-edge-inference",
    description: "엣지 추론 스테이징 (Furiosa/Rebellions NPU)",
    k8sVersion: "1.29.6",
    cni: "Cilium",
    nodeCount: 4,
    vmIds: [],
    gpuCount: 4,
    totalCpu: 128,
    totalMemGb: 512,
    usedCpu: 76,
    usedMemGb: 284,
    totalPowerWatts: 412,
    status: "warning",
    createdAt: "2025-08-19",
  },
];

// 2) VM 중 일부를 서비스 클러스터 노드로 지정 + passthrough 매핑
const vmsAsServiceNodes = vms.slice(0, 12);
serviceClusters[0].vmIds = vmsAsServiceNodes.slice(0, 8).map((v) => v.id);
serviceClusters[1].vmIds = vmsAsServiceNodes.slice(8, 12).map((v) => v.id);

export const vmGpuPassthroughs: VMGpuPassthrough[] = [];
const passthroughCandidates = accelerators.filter((a) => a.status === "Allocated").slice(0, 10);
passthroughCandidates.forEach((acc, i) => {
  const vm = vmsAsServiceNodes[i % vmsAsServiceNodes.length];
  if (!vm) return;
  const svc =
    serviceClusters.find((s) => s.vmIds.includes(vm.id))?.id ?? undefined;
  const pt: VMGpuPassthrough = {
    vmId: vm.id,
    acceleratorId: acc.id,
    hostPciBdf: acc.pciAddress ?? `0000:${(20 + i).toString(16)}:00.0`,
    state: "allocated_to_container",
    serviceCluster: svc,
  };
  vmGpuPassthroughs.push(pt);
  acc.passthroughTo = pt;
  acc.allocationState = "allocated";
  vm.gpuPassthrough = vm.gpuPassthrough ?? [];
  vm.gpuPassthrough.push(pt);
  vm.serviceClusterId = svc;
  vm.openstackProject = vm.tenantId;
  vm.clusterRole = i % 3 === 0 ? "control-plane" : "worker";
  vm.hypervisor = acc.nodeId;
});

// 데모용 — 가속기 2개(NVIDIA A100 ×2)가 패스스루된 VM 하나 명시적으로 구성
{
  const dualVm = vms.find((v) => v.id === "vm-001");
  const dualAccs = ["acc-a100-1-1", "acc-a100-1-2"]
    .map((id) => accelerators.find((a) => a.id === id))
    .filter(Boolean) as Accelerator[];
  if (dualVm && dualAccs.length === 2) {
    const dualAccIds = new Set(dualAccs.map((a) => a.id));
    // 이 VM 또는 대상 가속기에 걸린 기존 패스스루 제거(중복 방지)
    for (let i = vmGpuPassthroughs.length - 1; i >= 0; i--) {
      const pt = vmGpuPassthroughs[i];
      if (pt.vmId === dualVm.id || dualAccIds.has(pt.acceleratorId)) {
        vmGpuPassthroughs.splice(i, 1);
      }
    }
    // 다른 VM의 gpuPassthrough 배열에서도 대상 가속기 참조 제거
    vms.forEach((v) => {
      if (v.id !== dualVm.id && v.gpuPassthrough?.length) {
        v.gpuPassthrough = v.gpuPassthrough.filter((pt) => !dualAccIds.has(pt.acceleratorId));
        v.acceleratorIds = v.acceleratorIds.filter((id) => !dualAccIds.has(id));
      }
    });
    dualVm.acceleratorIds = dualAccs.map((a) => a.id);
    dualVm.gpuPassthrough = [];
    dualVm.nodeId = dualAccs[0].nodeId;
    dualVm.hypervisor = dualAccs[0].nodeId;
    dualVm.openstackProject = dualVm.tenantId;
    dualVm.owner = "hy.yun";        // 사용자(hy.yun) 목록에도 노출
    dualVm.keyName = "hy-yun-laptop";
    dualVm.status = "Running";
    dualAccs.forEach((acc, i) => {
      const pt: VMGpuPassthrough = {
        vmId: dualVm.id,
        acceleratorId: acc.id,
        hostPciBdf: acc.pciAddress ?? `0000:${(0x40 + i).toString(16)}:00.0`,
        state: "allocated_to_container",
      };
      vmGpuPassthroughs.push(pt);
      dualVm.gpuPassthrough!.push(pt);
      acc.status = "Allocated";
      acc.allocationState = "allocated";
      acc.passthroughTo = pt;
    });
  }
}

// Idle accelerators get explicit allocationState
accelerators.forEach((a) => {
  if (!a.allocationState) a.allocationState = a.status === "Allocated" ? "allocated" : "idle";
});

// 3) 파티션 — A100 1대를 MIG 4분할, Rebellions ATOM 1대를 4-slice
export const partitions: AcceleratorPartition[] = [];

const a100ForMig = accelerators.find((a) => a.id === "acc-a100-2-3");
if (a100ForMig) {
  a100ForMig.partitionMode = "mig";
  a100ForMig.sharing = "논리분할";
  ["1g10gb-gi0-ci0", "1g10gb-gi1-ci0", "2g20gb-gi2-ci0", "3g40gb-gi3-ci0"].forEach((suffix, i) => {
    const profile = suffix.split("-")[0].replace("g", "g.");
    const memMb = i === 3 ? 40960 : i === 2 ? 20480 : 10240;
    partitions.push({
      id: `mig-${suffix}`,
      acceleratorId: a100ForMig.id,
      model: "mig",
      profileName: profile,
      gpuInstanceId: i,
      computeInstanceId: 0,
      memoryTotalMb: memMb,
      memoryUsedMb: i === 2 ? 0 : Math.round(memMb * (0.4 + r() * 0.4)),
      utilizationPercent: i === 2 ? 0 : Math.round(35 + r() * 55),
      powerWatts: i === 2 ? 8 : Math.round(28 + r() * 18),
      powerEstimation: "proportional",
      workloadBinding: i === 2 ? undefined : {
        namespace: i === 0 ? "llm-serving" : i === 1 ? "ai-research" : "kubeflow",
        podName: i === 0 ? "vllm-solar-10b-7f9b" : i === 1 ? "data-prep-cronjob-22041" : "kubeflow-notebook-3",
        podUid: `pod-uid-${(i + 1) * 11}`,
        containerName: i === 0 ? "vllm" : i === 1 ? "loader" : "notebook",
        workloadType: i === 0 ? "deployment" : i === 1 ? "cronjob" : "statefulset",
      },
    });
  });
}

const atomForSlice = accelerators.find((a) => a.id === "acc-rbln-1-1");
if (atomForSlice) {
  atomForSlice.partitionMode = "npu-slice";
  atomForSlice.sharing = "논리분할";
  for (let i = 0; i < 4; i++) {
    partitions.push({
      id: `npu-slice-${atomForSlice.id}-s${i}`,
      acceleratorId: atomForSlice.id,
      model: "npu-slice",
      profileName: `atom-1/4`,
      memoryTotalMb: 8192,
      memoryUsedMb: Math.round(2048 + r() * 4096),
      utilizationPercent: Math.round(30 + r() * 50),
      powerWatts: Math.round(15 + r() * 8),
      powerEstimation: "proportional",
      workloadBinding: i < 3 ? {
        namespace: "edge-inference",
        podName: `stt-atom-edge-${(i + 1).toString().padStart(2, "0")}`,
        podUid: `pod-uid-atom-${i + 1}`,
        containerName: "inference",
        workloadType: "deployment",
      } : undefined,
    });
  }
}

// 4) 자원 계보 — Resource Map
export const resourceMapNodes: ResourceMapNode[] = [];
export const resourceMapEdges: ResourceMapRelationship[] = [];

// L0 · 물리 서버
nodes.forEach((n) => {
  resourceMapNodes.push({
    id: `L0:${n.id}`,
    layer: "L0",
    type: "physical-server",
    label: n.hostname,
    meta: { vcpu: n.vcpu, ramGb: n.ramGb, powerW: n.powerW, status: n.status, ip: n.ipAddress },
  });
  // L1 = L0 동일 실체 (mgmt K8s node)
  resourceMapNodes.push({
    id: `L1:${n.id}`,
    layer: "L1",
    type: "mgmt-node",
    label: `${n.hostname} (k8s)`,
    meta: { cluster: "mgmt", nodeUid: `uid-${n.id}` },
  });
  resourceMapEdges.push({ source: `L0:${n.id}`, target: `L1:${n.id}`, kind: "runs_on" });

  // LA · 부착된 가속기
  n.acceleratorIds.forEach((aid) => {
    const acc = accelerators.find((a) => a.id === aid);
    if (!acc) return;
    resourceMapNodes.push({
      id: `LA:${aid}`,
      layer: "LA",
      type: "accelerator",
      label: `${acc.vendor} ${acc.kind} (${aid})`,
      meta: {
        model: acc.model,
        vendor: acc.vendor,
        kind: acc.kind,
        memoryGb: acc.memoryGb,
        utilization: acc.utilization,
        allocationState: acc.allocationState ?? "idle",
      },
    });
    resourceMapEdges.push({ source: `L0:${n.id}`, target: `LA:${aid}`, kind: "attached_to" });
  });
});

// L3 · OpenStack VM (서비스 클러스터 노드인 것만)
vmsAsServiceNodes.forEach((v) => {
  resourceMapNodes.push({
    id: `L3:${v.id}`,
    layer: "L3",
    type: "openstack-vm",
    label: v.name,
    meta: {
      project: v.openstackProject ?? v.tenantId,
      hypervisor: v.hypervisor ?? v.nodeId,
      role: v.clusterRole ?? "worker",
      flavor: v.flavor,
    },
  });
  resourceMapEdges.push({ source: `L0:${v.nodeId}`, target: `L3:${v.id}`, kind: "runs_on" });
  // L4 = L3에 대응되는 서비스 K8s 노드
  resourceMapNodes.push({
    id: `L4:${v.id}`,
    layer: "L4",
    type: "service-node",
    label: `${v.name}.k8s`,
    meta: {
      cluster: v.serviceClusterId ?? "—",
      role: v.clusterRole ?? "worker",
    },
  });
  resourceMapEdges.push({ source: `L3:${v.id}`, target: `L4:${v.id}`, kind: "runs_on" });
});

// LA → L3 passthrough
vmGpuPassthroughs.forEach((pt) => {
  resourceMapEdges.push({
    source: `LA:${pt.acceleratorId}`,
    target: `L3:${pt.vmId}`,
    kind: "passthrough_to",
  });
});

// L5 · Pod / Container — 대표 워크로드 일부
const sampleWorkloads = workloads.filter((w) => w.accelerators > 0).slice(0, 6);
sampleWorkloads.forEach((w) => {
  const pid = `L5:${w.id}`;
  resourceMapNodes.push({
    id: pid,
    layer: "L5",
    type: "container",
    label: w.name,
    meta: {
      namespace: w.namespace,
      kind: w.kind,
      image: w.image,
      accelerators: w.accelerators,
    },
  });
  // schedule onto a service-node (L4)
  const vm = vmsAsServiceNodes.find((v) => v.serviceClusterId === "svc-ai-prod");
  if (vm) {
    resourceMapEdges.push({ source: pid, target: `L4:${vm.id}`, kind: "scheduled_to" });
    // allocate accelerator from VM's passthrough
    const pt = vm.gpuPassthrough?.[0];
    if (pt) {
      resourceMapEdges.push({
        source: `LA:${pt.acceleratorId}`,
        target: pid,
        kind: "allocated_to",
      });
    }
  }
});

// 파티션 노드 추가
partitions.forEach((p) => {
  const pid = `PT:${p.id}`;
  resourceMapNodes.push({
    id: pid,
    layer: "LA",
    type: "partition",
    label: `${p.model.toUpperCase()} ${p.profileName}`,
    meta: {
      model: p.model,
      profile: p.profileName,
      memoryMb: p.memoryTotalMb,
      utilization: p.utilizationPercent,
    },
  });
  resourceMapEdges.push({
    source: `LA:${p.acceleratorId}`,
    target: pid,
    kind: "partitioned_from",
  });
});

// ─────────────────────── Resource Requests ───────────────────────
export const resourceRequests: ResourceRequest[] = [
  // 대기 — GPU · 인터넷망 + 공인 IP 희망 · 키페어
  {
    id: "req-101", type: "vm", status: "pending",
    requestedBy: "u-002", requestedByName: "윤호영", requestedAt: "2026-09-22T10:12:00Z",
    orgId: "org-okestro", orgName: "오케스트로",
    projectId: "proj-llm-serving", projectName: "llm-serving",
    vmName: "vllm-serving-01", networkType: "external", floatingIp: true,
    flavorName: "g1.a100", vcpu: 32, ramGb: 128, diskGb: 1000,
    imageId: "img-6", imageName: "vllm-serving-runtime", cloudInit: true,
    keypairId: "sk-1", keypairName: "hy-yun-laptop",
    acceleratorKind: "GPU", acceleratorCount: 1,
  },
  // 대기 — 디스크 0 플레이버 → 부팅 볼륨 지정 · 내부망 · 키페어 없음
  {
    id: "req-102", type: "vm", status: "pending",
    requestedBy: "u-004", requestedByName: "박지수", requestedAt: "2026-09-22T09:40:00Z",
    orgId: "org-etri", orgName: "ETRI",
    projectId: "proj-ai-research", projectName: "ai-research",
    vmName: "dev-box-04", networkType: "internal", floatingIp: false,
    flavorName: "v1.large", vcpu: 8, ramGb: 32, diskGb: 0, volumeGb: 100,
    imageId: "img-1", imageName: "ubuntu-22.04-server", cloudInit: true,
  },
  // 대기 — cloud-init 없는 이미지 (키페어 미적용)
  {
    id: "req-103", type: "vm", status: "pending",
    requestedBy: "u-003", requestedByName: "김민준", requestedAt: "2026-09-21T16:05:00Z",
    orgId: "org-keti", orgName: "KETI",
    projectId: "proj-edge-inference", projectName: "edge-inference",
    vmName: "legacy-gw-01", networkType: "external", floatingIp: false,
    flavorName: "m1.small", vcpu: 4, ramGb: 16, diskGb: 100,
    imageId: "img-7", imageName: "centos-7-legacy-appliance", cloudInit: false,
  },
  {
    id: "req-002",
    type: "cluster",
    status: "pending",
    requestedBy: "u-002",
    requestedByName: "윤호영",
    requestedAt: "2026-09-16T14:10:00Z",
    orgId: "org-okestro", orgName: "오케스트로",
    projectId: "proj-llm-serving",
    projectName: "llm-serving",
    nodeCount: 3,
    masterCount: 1,
    vcpu: 8,
    ramGb: 32,
    storageGb: 100,
    acceleratorKind: "GPU",
    acceleratorCount: 2,
  },
  // 생성 중 — 목업 핸들러가 단계 진행을 시뮬레이션
  {
    id: "req-006", type: "vm", status: "provisioning",
    requestedBy: "u-005", requestedByName: "이준호", requestedAt: "2026-09-18T08:00:00Z",
    orgId: "org-okestro", orgName: "오케스트로",
    projectId: "proj-llm-serving", projectName: "llm-serving",
    vmName: "llm-worker-01", networkType: "internal", floatingIp: false,
    flavorName: "g1.l40s.v", vcpu: 16, ramGb: 64, diskGb: 0, volumeGb: 200,
    imageId: "img-2", imageName: "ubuntu-24.04-cuda-12.4", cloudInit: true,
    keypairId: "sk-3", keypairName: "okestro-ops-key",
    acceleratorKind: "GPU", acceleratorCount: 1,
    provisionSpec: { networkId: "net-llm-int", networkName: "llm-serving-net", networkKind: "project", securityGroups: ["default", "allow-ssh"] },
    approvedBy: "u-001", approvedByName: "최현화", approvedAt: "2026-09-18T09:00:00Z",
  },
  // 완료 — external 망 직접 연결 (Static IP 직접 입력)
  {
    id: "req-003", type: "vm", status: "completed",
    requestedBy: "u-003", requestedByName: "김민준", requestedAt: "2026-09-15T11:00:00Z",
    orgId: "org-keti", orgName: "KETI",
    projectId: "proj-edge-inference", projectName: "edge-inference",
    vmName: "inference-server-02", networkType: "external", floatingIp: true,
    flavorName: "g1.l40s", vcpu: 16, ramGb: 64, diskGb: 500,
    imageId: "img-2", imageName: "ubuntu-24.04-cuda-12.4", cloudInit: true,
    keypairId: "sk-6", keypairName: "keti-edge-key",
    acceleratorKind: "GPU", acceleratorCount: 1,
    provisionSpec: { networkId: "net-ext-svc", networkName: "ext-svc-net", networkKind: "external", fixedIp: "198.51.100.24", securityGroups: ["default", "allow-ssh"] },
    result: { serverId: "8f1c2a4e-5b7d-4c9e-a1f3-2d6b8e0c4a71", fixedIp: "198.51.100.24" },
    approvedBy: "u-001", approvedByName: "최현화", approvedAt: "2026-09-15T15:30:00Z",
  },
  // 완료 — 내부망 + Floating IP
  {
    id: "req-008", type: "vm", status: "completed",
    requestedBy: "u-004", requestedByName: "박지수", requestedAt: "2026-09-12T13:20:00Z",
    orgId: "org-etri", orgName: "ETRI",
    projectId: "proj-ai-research", projectName: "ai-research",
    vmName: "research-trainer-01", networkType: "external", floatingIp: true,
    flavorName: "n1.rngd-1", vcpu: 16, ramGb: 64, diskGb: 500,
    imageId: "img-3", imageName: "rhel-9-furiosa-sdk", cloudInit: true,
    keypairId: "sk-5", keypairName: "etri-research-key",
    acceleratorKind: "NPU", acceleratorCount: 1,
    provisionSpec: { networkId: "net-ai-int", networkName: "ai-research-net", networkKind: "project", floatingNetworkId: "net-ext", floatingNetworkName: "ext-net", securityGroups: ["default", "allow-ssh"] },
    result: { serverId: "3b9e7d21-0c4f-4a8b-9e6d-5f2a1c7b8e90", fixedIp: "192.168.100.37", floatingIp: "203.0.113.58" },
    approvedBy: "u-001", approvedByName: "최현화", approvedAt: "2026-09-12T15:00:00Z",
  },
  // 실패 — 서버 생성 단계 실패 (재시도 가능)
  {
    id: "req-007", type: "vm", status: "failed",
    requestedBy: "u-002", requestedByName: "윤호영", requestedAt: "2026-09-19T10:30:00Z",
    orgId: "org-okestro", orgName: "오케스트로",
    projectId: "proj-llm-serving", projectName: "llm-serving",
    vmName: "vllm-bench-02", networkType: "external", floatingIp: true,
    flavorName: "g1.a100", vcpu: 32, ramGb: 128, diskGb: 1000,
    imageId: "img-6", imageName: "vllm-serving-runtime", cloudInit: true,
    keypairId: "sk-1", keypairName: "hy-yun-laptop",
    acceleratorKind: "GPU", acceleratorCount: 1,
    provisionSpec: { networkId: "net-llm-int", networkName: "llm-serving-net", networkKind: "project", floatingNetworkId: "net-ext", floatingNetworkName: "ext-net", securityGroups: ["default", "allow-ssh", "allow-web"] },
    errorMessage: "No valid host was found. There are not enough hosts available.",
    approvedBy: "u-001", approvedByName: "최현화", approvedAt: "2026-09-19T11:00:00Z",
  },
  // 반려
  {
    id: "req-004", type: "vm", status: "rejected",
    requestedBy: "u-004", requestedByName: "박지수", requestedAt: "2026-09-14T09:00:00Z",
    orgId: "org-etri", orgName: "ETRI",
    projectId: "proj-ai-research", projectName: "ai-research",
    vmName: "big-trainer-xlarge", networkType: "external", floatingIp: true,
    flavorName: "m1.2xlarge", vcpu: 32, ramGb: 128, diskGb: 1000,
    imageId: "img-1", imageName: "ubuntu-22.04-server", cloudInit: true,
    approvedRejectBy: "u-001", approvedRejectByName: "최현화", approvedRejectAt: "2026-09-14T14:00:00Z",
    approvedRejectReason: "대용량 스펙 사용 목적 불명확 — 사유 보완 후 재신청 바랍니다",
  },
  {
    id: "req-005",
    type: "cluster",
    status: "completed",
    requestedBy: "u-004",
    requestedByName: "박지수",
    requestedAt: "2026-09-10T10:00:00Z",
    orgId: "org-etri", orgName: "ETRI",
    projectId: "proj-ai-research",
    projectName: "ai-research",
    nodeCount: 5,
    masterCount: 1,
    vcpu: 16,
    ramGb: 64,
    storageGb: 200,
    acceleratorKind: "NPU",
    acceleratorCount: 2,
    approvedBy: "u-001",
    approvedByName: "최현화",
    approvedAt: "2026-09-10T16:00:00Z",
    resultId: "svc-ai-prod",
  },
];

// 5) ObservedAt — 모든 데이터의 공통 신선도 메타
export const OBSERVED_AT_BASE = "2026-06-02T10:00:00Z";
export function observedMeta(staleMinutes = 0.5) {
  return {
    observed_at: OBSERVED_AT_BASE,
    is_stale: staleMinutes > 2,
  };
}

