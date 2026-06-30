# Full-Stack Installation — kcloud-tool

## Overview

`scripts/install_kcloud_stack.sh` brings up the **entire ETRI LLM evaluation platform** on a
Kubernetes cluster from a single command. Only required input: the cluster node IP list.

What gets deployed (in stage order):

| Stage | What | Namespace |
|---|---|---|
| preflight | Tooling check, node-IP validation, role resolution, device detection | — |
| provision | Kubespray cluster bootstrap (bare nodes only, `--provision`) | — |
| storage | `nfs-subdir-external-provisioner` Helm chart; StorageClass + RWX PVC | `nfs-provisioner` |
| operators | GPU Operator, FuriosaAI RNGD plugin, Rebellions Atom+ plugin (per device) | `gpu-operator` / `furiosa-system` / `rbln-system` |
| observability | Loki, kube-prometheus-stack, Grafana Alloy | `loki` / `monitoring` |
| webapp | `app-chart` — frontend (`:30001`) + backend (`:30980`) + config | `llm-evaluation` |
| benchmarks | Delegates to `install_pilot_k8s.sh` — MLPerf CNN/DM + MMLU-Pro | `kcloud-mlperf` |
| verify | Cluster health, web UI reachable, backend `/api/devices` JSON, access URLs | — |

All resources are labeled `app.kubernetes.io/managed-by=kcloud-tool` and
`app.kubernetes.io/part-of=kcloud-stack` so cleanup is safe and scoped.

---

## Prerequisites

- `kubectl` configured pointing at the target cluster (`~/.kube/config`). Run `kubectl get nodes`.
- `helm` v3.x.
- `envsubst` (GNU gettext-tools), `jq`, `curl`, `base64`.
- Nodes reachable from the machine running the installer (NodePort access for verification).
- If using `--provision` (bare metal bootstrap): SSH access to all nodes on `--ssh-port` (default 122); `sshpass` installed or `SSHPASS` set; sudo access.
- A Hugging Face token with read access to `meta-llama/Llama-3.1-8B-Instruct`. See [HF Token Handling](#hf-token-handling).
- For private image registries: a valid `imagePullSecret` dockerconfigjson, never logged. See [Image Pull Secret](#image-pull-secret).
- Internet egress from cluster nodes for the FuriosaAI Helm repo (`--device npu-rngd`). Skip cleanly if absent — furiosa stage is best-effort.

---

## One-Command Install

```bash
./scripts/install_kcloud_stack.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
```

**What happens:**

1. **Preflight** — checks tooling (`kubectl`, `helm`, `envsubst`, `jq`); validates supplied IPs against `kubectl get nodes` InternalIPs; resolves roles (control-plane = first IP, NFS server = first IP by default, access IP = first IP by default); detects device mode.
2. **Stage order** — storage → operators → observability → webapp → benchmarks.
3. **Verify** — confirms all key services are up; prints the access URL table.

Secret values (HF token, dockerconfigjson, SSH/sudo password) are **never printed**. All log lines redact with `***`.

---

## Stage-by-Stage Breakdown

### preflight

- Checks that `kubectl`, `helm`, `envsubst`, `jq` are on `PATH`.
- Validates `--node-ips` against cluster node InternalIPs (warn on mismatch, warn unless `--force` absent and apply pending).
- Resolves role assignments: control-plane = ip1, NFS server = `--nfs-server` or ip1, access IP = `--access-ip` or ip1.
- Auto-detects device mode (gpu → npu-rngd → npu-atom → cpu) via `detect_device_mode` from `lib/detect.sh`.
- `--dry-run` offline: degrades to a printed plan with no cluster queries.
- `--validate-only`: exits 0 if all checks pass, non-zero with the blocking issue otherwise.

### provision *(only with `--provision`; skipped if cluster already healthy)*

- Renders a kubespray inventory from `--node-ips` into `deploy/render/inventory/`.
- Runs vendored `kubespray cluster.yml` via SSH (port `--ssh-port`, default 122; sudo via `SSHPASS` env or interactive prompt — **never logged**).
- This is the only stage that needs SSH/sudo. Guard: requires explicit `--provision` flag, never runs automatically.
- After kubespray finishes, merges the generated kubeconfig into `~/.kube/config` and confirms node Ready.

### storage

- Renders `deploy/platform/nfs-values-override.yaml.tmpl` → `deploy/render/nfs-values-override.yaml`
  (`NFS_SERVER`, `NFS_PATH=/nfs-storage`).
- `helm upgrade --install nfs-subdir <chart> -n nfs-provisioner --create-namespace -f <render>`.
- Verifies a default RWX StorageClass exists afterward (picks by NFS provisioner substring, not annotation alone — see [Two-Default StorageClass](#two-default-storageclass-gotcha)).
- **CPU/kind fallback:** if no NFS server is reachable, installs `rancher/local-path` or accepts an existing default StorageClass; emits `[warn]` that RWX-only features (multi-node PVC mounts) degrade to RWO.

### operators

Controlled by `--device` (default `auto`):

| Device mode | Chart / plugin | Namespace | Verification |
|---|---|---|---|
| `gpu` | `gpu-operator-25.10.0` (vendored) | `gpu-operator` | DaemonSet Ready + `nvidia.com/gpu` allocatable |
| `npu-rngd` | FuriosaAI Helm repo (best-effort, needs egress) | `furiosa-system` | `furiosa.ai/rngd` allocatable |
| `npu-atom` | Rebellions Atom+ plugin (vendored) | `rbln-system` | `rebellions.ai/ATOM` allocatable |
| `cpu` | Skipped | — | — |

`--skip-operators` bypasses this entire stage (required for kind testing; see [Kind Testing](#kind-confidence-loop-testing)).

### observability *(skip with `--skip-observability`)*

- `helm upgrade --install loki loki-2.2.1 -n loki --create-namespace`
- `helm upgrade --install kube-prometheus-stack kube-prometheus-stack-79.1.1 -n monitoring --create-namespace`
- `helm upgrade --install alloy alloy-1.4.0 -n monitoring`
- Verification: each release shows `deployed`; key pods (prometheus-server, grafana, loki) are Ready.

### webapp *(skip with `--skip-webapp`)*

- Renders `deploy/platform/app-chart-values-override.yaml.tmpl` → override YAML:
  - `VITE__APP_API_BASE_URL: http://<ACCESS_IP>:30980/api` (frontend→backend URL)
  - Namespace, managed-by/part-of labels.
- Renders `deploy/platform/cluster.yaml.tmpl` → `config/cluster.yaml` (per-node SSH map, ports from `--ssh-port`/`SSH_PORT_NPU`).
- `helm upgrade --install app-chart <PLATFORM_DIR>/app-chart -n <APP_NS> --create-namespace -f values.yaml -f <override>`
- Verification:
  - backend + frontend Deployments rolled out.
  - `curl http://<ACCESS_IP>:30001` → HTTP 200 (frontend).
  - `curl http://<ACCESS_IP>:30980/api/devices` → JSON array (backend).

### benchmarks *(skip with `--skip-benchmarks`)*

Delegates entirely to the existing installer — no duplication:

```bash
scripts/install_pilot_k8s.sh \
  --node-ips "<NODE_IPS>" \
  --namespace <BENCH_NAMESPACE> \
  [--device <mode>] [--dry-run] [--timeout <secs>]
```

This preserves all benchmark-installer behavior including HF token resolution, smoke/full mode, and PVC sizing. The pilot installer validator (16/16 PASS) is unaffected.

### verify

Cluster-wide health report — exits non-zero if any **required** check fails:

| Check | Required | Command |
|---|---|---|
| All nodes Ready | required | `kubectl get nodes` |
| Default StorageClass exists | required | `kubectl get sc` |
| webapp frontend HTTP 200 | required | `curl http://<ACCESS_IP>:30001` |
| backend `/api/devices` JSON | required | `curl http://<ACCESS_IP>:30980/api/devices` |
| Helm releases deployed | required | `helm list -A` |
| Device plugin DaemonSet Ready | warn if hardware present | `kubectl get ds -n <ns>` |
| Observability pods Ready | warn if not skipped | `kubectl get pods -n monitoring` |
| Benchmark namespace exists | warn if not skipped | `kubectl get ns <BENCH_NS>` |

Prints a final table and the access URL block (see [Access URLs](#access-urls)).

---

## Auto-Detect vs. Override Table

Every auto-detected value has a corresponding flag. Supply only the flags you need to change.

| What is detected | How | Default | Override flag |
|---|---|---|---|
| Control-plane node | First IP in `--node-ips` | ip1 | — (positional) |
| NFS server IP | `--nfs-server` or control-plane | ip1 | `--nfs-server <ip>` |
| Browser-facing IP | `--access-ip` or control-plane | ip1 | `--access-ip <ip>` |
| Device mode | Scans allocatable: `nvidia.com/gpu` → `furiosa.ai/rngd` → `rebellions.ai/ATOM` → cpu | `gpu` if L40/A40 present | `--device auto\|gpu\|npu-rngd\|npu-atom\|cpu` |
| StorageClass | NFS provisioner substring → first default-annotated class | `nfs-client` if present | `--storage-class <sc>` (benchmark stage) |
| PVC access mode | RWX if NFS provisioner, else RWO | RWX (NFS) / RWO (local) | — (derived) |
| HF token source | `HF_TOKEN` env → `HUGGING_FACE_HUB_TOKEN` env → `~/.cache/huggingface/token` → in-cluster secret | first match | `--hf-token-source auto\|env\|file:<path>\|secret:<ns>/<name>` (benchmark stage) |
| Registry prefix | Scans existing pod images; empty = public | empty | `--registry <reg>` (benchmark stage) |
| App namespace | — | `llm-evaluation` | `--app-namespace <ns>` |
| Bench namespace | — | `kcloud-mlperf` | `--bench-namespace <ns>` |
| Platform chart dir | — | `/home/kcloud/etri-llm-deployments/app/kubernetes` | `--platform-dir <path>` |
| SSH port (control-plane / GPU) | — | 122 | `--ssh-port <n>` |
| SSH port (NPU nodes) | — | 22 | hardcoded per node role; NPU nodes always use 22 |
| Per-stage rollout timeout | — | 600 s | `--timeout <secs>` |

---

## Existing Cluster vs. `--provision` (Bare Nodes)

### Existing cluster (default)

`kubectl` is already configured. The installer skips provision entirely:

```bash
./scripts/install_kcloud_stack.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
```

Node IPs are validated against `kubectl get nodes` InternalIPs at preflight. A mismatch produces a `[warn]`; with `--force` absent and a live apply pending, the installer aborts with an actionable message.

### Bare-node bootstrap (`--provision`)

Runs kubespray to turn raw Linux nodes into a Kubernetes cluster, then proceeds with the platform stages:

```bash
SSHPASS=<sudo-password> ./scripts/install_kcloud_stack.sh \
  --node-ips "192.0.2.11,192.0.2.12,192.0.2.13" \
  --provision \
  --ssh-port 22
```

**Behavior:**
- If `kubectl get nodes` already reports the target nodes as Ready, `--provision` is auto-skipped (idempotent re-run).
- SSH port defaults to 122 when `--provision` is used; set `--ssh-port 22` for standard-port hosts.
- The sudo password is read from `SSHPASS` env (never logged) or prompted interactively. Do not pass it as a flag.
- Rendered kubespray inventory lives under `deploy/render/inventory/` (gitignored).

---

## Storage and NFS Selection

The installer needs a StorageClass that supports `ReadWriteMany` (RWX) for the results PVC and app data volumes.

**Happy path:** NFS provisioner (`nfs-subdir-external-provisioner`) deployed and StorageClass `nfs-client` created. The installer auto-selects by provisioner substring, not by the `is-default-class` annotation, because two classes can be simultaneously annotated as default (see [Troubleshooting](#two-default-storageclass-gotcha)).

**NFS server IP:** defaults to the control-plane node (ip1). If NFS is on a different node:

```bash
./scripts/install_kcloud_stack.sh \
  --node-ips "192.0.2.11,192.0.2.12,192.0.2.13" \
  --nfs-server 192.0.2.12
```

**NFS path:** default `/nfs-storage`. Rendered into `nfs-values-override.yaml` as `nfs.path`.

**CPU/kind fallback:** if no NFS server is reachable, the installer tries to use `rancher/local-path` or whatever default StorageClass exists. RWX-only features (multi-node mounts) degrade silently with a `[warn]`; the webapp and benchmarks still run with `ReadWriteOnce`.

---

## HF Token Handling

Token resolution order for the benchmark stage (first match wins):

1. `HF_TOKEN` environment variable
2. `HUGGING_FACE_HUB_TOKEN` environment variable
3. File at `~/.cache/huggingface/token`
4. Existing in-cluster Secret `huggingface-token` (copied into target namespace)

**Security guarantees:**
- The raw token value is **never printed**. Log lines show `[REDACTED:token]`.
- `--dry-run` does not create the Secret; the token is omitted from the printed plan.
- The rendered Secret manifest is never echoed to stdout.

```bash
# Via environment (recommended for CI)
HF_TOKEN=hf_xxxx ./scripts/install_kcloud_stack.sh --node-ips "..."

# Via explicit file
./scripts/install_kcloud_stack.sh --node-ips "..." \
  --hf-token-source file:/path/to/token   # passes through to pilot installer

# Reuse existing in-cluster secret
./scripts/install_kcloud_stack.sh --node-ips "..." \
  --hf-token-source secret:llm-evaluation/huggingface-token
```

## Image Pull Secret

For private registries, supply the dockerconfigjson as an environment variable before running:

```bash
export DOCKER_CONFIG_JSON='{"auths":{"registry.example.com":{"auth":"..."}}}' 
./scripts/install_kcloud_stack.sh --node-ips "..."
```

The value is base64-encoded at render time and injected as a Kubernetes Secret. **It is never printed in any log line.** Rendered Secret files under `deploy/render/` are gitignored and mode `0600`.

---

## GPU / NPU / CPU Fallback

Priority for auto-detection: **gpu > npu-rngd > npu-atom > cpu**.

| Mode | Operators installed | Benchmark image | Notes |
|---|---|---|---|
| `gpu` | GPU Operator | `vllm/vllm-openai:v0.8.4` | L40 / A40; FP8 model auto-selected |
| `npu-rngd` | FuriosaAI plugin (best-effort) | `python:3.11-slim` | Thin client; hits external `http://<node>:8000`; no device resource requested |
| `npu-atom` | Rebellions Atom+ plugin | `python:3.11-slim` | Currently parked (`Init:0/1`); Jobs Pending until plugin Ready |
| `cpu` | None | `python:3.11-slim` | kind / test clusters; low throughput |

`--skip-operators` suppresses all operator installs. Required for kind testing (no GPU/NPU hardware).

**npu-rngd note:** the FuriosaAI Helm repo requires outbound internet egress. If egress is blocked, stage 08 (`npu-furiosa`) exits with a `[warn]` and is skipped — no error, remaining stages continue.

---

## Verification and Access URLs

After all stages complete, `stage_verify` prints:

```
════════════════════════════════════════════════════════
 kcloud-stack  VERIFY SUMMARY
════════════════════════════════════════════════════════
 Nodes Ready          ✓  3/3
 StorageClass (RWX)   ✓  nfs-client
 GPU Operator         ✓  Running
 Observability        ✓  loki / prometheus / alloy
 webapp frontend      ✓  HTTP 200
 webapp backend       ✓  /api/devices → JSON array
 benchmarks ns        ✓  kcloud-mlperf

ACCESS URLS
  Frontend :   http://<ACCESS_IP>:30001
  Backend  :   http://<ACCESS_IP>:30980/api
════════════════════════════════════════════════════════
```

Exit code 0 = all required checks pass. Non-zero = the summary table identifies the failing check.

To re-run verification alone without re-installing:

```bash
./scripts/install_kcloud_stack.sh --node-ips "..." --only verify
```

---

## Kind Confidence-Loop Testing

kind provides a lightweight, hardware-free way to prove orchestration logic, Helm idempotency, namespace sequencing, webapp reachability, and cleanup before touching real hardware.

**What kind CAN test:**

- Flag parsing, CLI contract, `--help`, `--dry-run`, `--validate-only`.
- Namespace and StorageClass setup (local-path fallback).
- Helm chart templating and idempotent `helm upgrade --install`.
- webapp frontend + backend up, `/api/devices` responds.
- Cleanup label selection.
- Re-run idempotency (second run = no diff / converged).

**What kind CANNOT test** (real cluster required):

- GPU/NPU operators (no hardware, no device plugins).
- kubespray provisioning (bare-metal SSH bootstrap).
- RWX NFS mounts (no NFS server).
- NodePort firewall / external-IP routing.

### Run the confidence loop

```bash
# Create a kind cluster, run the installer with --skip-operators, tear down; repeat N times
test/run_confidence_loop.sh 3
```

Each iteration: `kind_up.sh` → `e2e_install.sh` → `kind_down.sh`. Results logged to `test/.results/`. Final summary: `N/N PASS` or per-failure detail.

```bash
# Single iteration (keep cluster on failure for debugging)
test/e2e_install.sh --keep

# Tear down manually
test/kind_down.sh
```

`test/kind_up.sh` creates 1 control-plane + 2 worker nodes (mirrors a 3-node real cluster). If no container runtime is available (no docker/nerdctl), the script prints a clear `SKIP` with instructions and exits 3.

---

## All Flags Reference

| Flag | Default | Effect |
|---|---|---|
| `--node-ips "<csv>"` | *required* | Comma-separated cluster node InternalIPs. First = control-plane. Required except for `--help`. |
| `--access-ip <ip>` | ip1 (control-plane) | Browser-facing IP for frontend→backend URL (`VITE__APP_API_BASE_URL`). |
| `--nfs-server <ip>` | ip1 (control-plane) | IP of the NFS server host. |
| `--app-namespace <ns>` | `llm-evaluation` | Namespace for the webapp (app-chart). |
| `--bench-namespace <ns>` | `kcloud-mlperf` | Namespace for the benchmark layer. |
| `--platform-dir <path>` | `/home/kcloud/etri-llm-deployments/app/kubernetes` | Source of vendored charts and stage scripts. |
| `--provision` | off | Run kubespray first (bare nodes). Auto-skipped if cluster already healthy. |
| `--ssh-port <n>` | `122` | SSH port for kubespray and cluster.yaml control-plane/GPU nodes. NPU nodes always use port 22. |
| `--device <mode>` | `auto` | `auto\|gpu\|npu-rngd\|npu-atom\|cpu`. Drives operator install and benchmark device selection. |
| `--skip-observability` | off | Skip loki / prometheus / alloy stages. |
| `--skip-webapp` | off | Skip the app-chart (platform infra + benchmarks still run). |
| `--skip-benchmarks` | off | Skip the benchmark layer. |
| `--skip-operators` | off | Skip all device operators (required for kind / CPU-only testing). |
| `--only <stage>` | — | Run exactly one stage: `preflight\|provision\|storage\|operators\|observability\|webapp\|benchmarks\|verify`. |
| `--timeout <secs>` | `600` | Per-rollout wait timeout. |
| `--dry-run` | off | Render + `helm template` + `kubectl apply --dry-run=client`. No cluster mutation. Works offline. |
| `--validate-only` | off | Read-only preflight: cluster reachable, nodes match, tooling present, charts found, IP substitution resolves. Exit 0 = ready. |
| `--cleanup` | off | Remove all resources labeled `managed-by=kcloud-tool`. Guarded; requires `--force` for non-empty / unlabeled namespaces. |
| `--force` | off | Allow potentially-overwriting / destructive-by-design actions. |
| `-h \| --help` | — | Print usage; exit 0. Never touches the cluster. |

---

## Cleanup

```bash
# Remove everything this installer created
./scripts/install_kcloud_stack.sh --node-ips "..." --cleanup

# Force-delete a namespace not labeled as ours
./scripts/install_kcloud_stack.sh --node-ips "..." --cleanup --force
```

`--cleanup` selects resources by:
```
app.kubernetes.io/managed-by=kcloud-tool
app.kubernetes.io/part-of=kcloud-stack
```

Helm releases are uninstalled by release name. Namespaces labeled `managed-by=kcloud-tool` are deleted. Resources not carrying these labels are never touched. `--force` enables deletion of non-empty or unlabeled namespaces — use with care.

Dry-run cleanup (shows what would be deleted, mutates nothing):

```bash
./scripts/install_kcloud_stack.sh --node-ips "..." --cleanup --dry-run
```

---

## Troubleshooting

### Two-Default StorageClass gotcha

**Symptom:** `kubectl get sc` shows two classes both annotated `(default)`:

```
local-path (default)   rancher.io/local-path                         WaitForFirstConsumer  RWO-only
nfs-client (default)   cluster.local/nfs-subdir-external-provisioner Immediate             RWX-capable
```

The installer selects by NFS provisioner substring — not by annotation. If your NFS provisioner has an unusual name, pass `--storage-class nfs-client` to the benchmark stage explicitly, or ensure the provisioner name contains the substring `nfs`.

Secondary symptom: PVCs bound but pods on a different node report `Multi-Attach error for volume`. Cause: RWO PVC (local-path) used instead of RWX (nfs-client).

Fix:
```bash
./scripts/install_kcloud_stack.sh --node-ips "..." \
  --hf-token-source secret:llm-evaluation/huggingface-token  # forces reuse
# Then in benchmark stage override:
#   --storage-class nfs-client  (passed through to install_pilot_k8s.sh)
```

To remove the extra default annotation from `local-path`:
```bash
kubectl patch storageclass local-path \
  -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}'
```

### NPU node SSH port is 22 (all others are 122)

All nodes use SSH port **122** except NPU nodes (e.g., FuriosaAI RNGD on 192.0.2.114), which use the standard port **22**. The installer encodes this as `SSH_PORT_NPU=22` in `cluster.yaml.tmpl`. If you have a non-standard topology, set the NPU SSH port by editing the rendered `cluster.yaml` under `deploy/render/`.

For kubespray provisioning, `--ssh-port` sets the port for all nodes; if your NPU node is also being provisioned and uses port 22 while others use 122, run kubespray in two passes or set all nodes to a uniform port first.

### FuriosaAI Helm repo requires internet egress

The NPU-RNGD operator stage does `helm repo add furiosa ...` which requires outbound HTTPS to the FuriosaAI Helm repository. If your cluster has no internet egress:

- The stage exits with `[warn]` and is **skipped** — it is best-effort.
- The remaining stages (webapp, benchmarks) continue.
- If the FuriosaAI device plugin is already installed (vendored charts), point `--platform-dir` at a directory containing a local copy of the Furiosa chart, or pre-add the Helm repo from a machine with egress and use `helm pull` to vendor it.

Check if the furiosa plugin is already running (pre-installed):
```bash
kubectl get pods -n furiosa-system
kubectl get nodes -o json | jq '.items[].status.allocatable | with_entries(select(.key | startswith("furiosa")))'
```

### NodePort firewall

NodePorts 30001 (frontend) and 30980 (backend) must be reachable from the browser/client machine. If verification reports `curl: (7) Failed to connect`:

```bash
# On each node (iptables example)
sudo iptables -I INPUT -p tcp --dport 30001 -j ACCEPT
sudo iptables -I INPUT -p tcp --dport 30980 -j ACCEPT

# Or via firewalld
sudo firewall-cmd --permanent --add-port=30001/tcp
sudo firewall-cmd --permanent --add-port=30980/tcp
sudo firewall-cmd --reload
```

If using a cloud provider, add the inbound rules in the security group / network policy for the `--access-ip` node.

### Node not Ready after provision

After kubespray completes, if `kubectl get nodes` still shows `NotReady`:

```bash
# On the affected node
journalctl -u kubelet -n 100 --no-pager

# Common causes:
# 1. CNI plugin not initialised — wait up to 3 minutes after kubespray finishes
# 2. containerd not running: sudo systemctl status containerd
# 3. Swap not disabled: sudo swapoff -a && sudo sed -i '/swap/d' /etc/fstab
# 4. Missing kernel modules: sudo modprobe br_netfilter overlay

# Re-check node status
kubectl get nodes -o wide
kubectl describe node <node-name>  # look at "Conditions:" section
```

If a node stays `NotReady` for more than 5 minutes after kubespray exits 0, re-run with `--provision` (idempotent): kubespray will reconcile the delta.

### Atom+ (Rebellions) device plugin parked

The Rebellions device-plugin DaemonSet is in `Init:0/1` state. `rebellions.ai/ATOM` will not appear in node allocatable until it completes. Jobs targeting `npu-atom` will stay `Pending`.

```bash
kubectl get pods -n rbln-system
kubectl describe ds -n rbln-system
```

The installer emits `[warn]` when `npu-atom` is selected and the resource is absent from allocatable. This is a known state; selecting `--device cpu` or `--device auto` (which falls back past atom) avoids Pending jobs.

### Webapp backend returns empty `/api/devices`

The backend reads its device list from `config/cluster.yaml` (mounted as a ConfigMap). If that file has stale IPs or empty node entries, `/api/devices` returns `[]`.

```bash
# Inspect the mounted config
kubectl exec -n llm-evaluation deploy/llm-evaluation-backend -- cat /app/config/cluster.yaml

# Re-render and re-apply with corrected node IPs
./scripts/install_kcloud_stack.sh --node-ips "..." --only webapp
```

Verify the `VITE__APP_API_BASE_URL` secret in the frontend pod matches the running backend IP:
```bash
kubectl exec -n llm-evaluation deploy/llm-evaluation-frontend -- env | grep API_BASE
```
