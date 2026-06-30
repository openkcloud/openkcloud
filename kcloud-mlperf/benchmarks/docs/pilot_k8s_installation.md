# Pilot Kubernetes Installation — kcloud-mlperf

## Overview

`scripts/install_pilot_k8s.sh` deploys the **kcloud-mlperf benchmark suite** (MLPerf CNN/DM + MMLU-Pro) into a Kubernetes namespace (`kcloud-mlperf` by default).

**Design promise:** the only required manual input is a comma-separated list of node IPs. Everything else — device mode, storage class, PVC access mode, HF token, registry — is auto-detected from the cluster at runtime.

What gets deployed:

| Resource | Name (default) |
|---|---|
| Namespace | `kcloud-mlperf` |
| ServiceAccount + RBAC | `kcloud-mlperf` (least-privilege, own namespace only) |
| HF token Secret | `huggingface-token` |
| Benchmark scripts ConfigMap | `kcloud-mlperf-bench-scripts` |
| Results PVC | `kcloud-mlperf-results` (50 Gi) |
| Smoke Job | `kcloud-mlperf-smoke` |
| Benchmark Job | `kcloud-mlperf-bench` |

All resources are labeled `app.kubernetes.io/managed-by=kcloud-tool` and `app.kubernetes.io/part-of=<release>` so cleanup is safe and scoped.

---

## Prerequisites

- `kubectl` configured and pointing at the target cluster (`~/.kube/config`). Run `kubectl get nodes` to verify.
- Device plugin(s) installed and healthy for your hardware:
  - GPU: NVIDIA GPU Operator advertising `nvidia.com/gpu`
  - NPU (FuriosaAI RNGD): FuriosaAI device plugin advertising `furiosa.ai/rngd`
  - NPU (Rebellions Atom+): Rebellions device plugin advertising `rebellions.ai/ATOM` (**currently parked** — see [Troubleshooting](#troubleshooting))
  - CPU: no plugin required
- An RWX-capable StorageClass (NFS preferred) **or** a fallback RWO class. See [StorageClass auto-detection](#auto-detection-behavior) for the two-default gotcha.
- A Hugging Face token with read access to `meta-llama/Llama-3.1-8B-Instruct` (or your chosen model). See [HF token handling](#hf-token-handling).
- Standard POSIX tools: `bash`, `kubectl`, `envsubst`, `base64`.

---

## One-Command Install

```bash
./scripts/install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13"
```

**What happens:**

1. **Preflight** — verifies `kubectl` connectivity; validates supplied IPs against `kubectl get nodes -o wide` InternalIPs (warns on mismatch, does not abort unless `--force` is absent and an apply is pending).
2. **Auto-detect** — discovers device mode, storage class, PVC access mode, HF token source, and registry prefix.
3. **Render** — substitutes all `${VAR}` placeholders in `deploy/templates/*.yaml` via `envsubst`.
4. **Apply** — `kubectl apply` (declarative; re-running is safe).
5. **Smoke** — runs `kcloud-mlperf-smoke` Job and waits up to `--timeout` seconds for success (default enabled; disable with `--skip-smoke-test`).

Secret values are **never printed**. All log lines that touch HF token data redact with `***`.

---

## Auto-Detection Behavior

Each value is auto-detected at runtime and can be overridden with the corresponding flag.

| What is detected | How it is detected | Default result | Override flag |
|---|---|---|---|
| Namespace | — | `kcloud-mlperf` | `--namespace <ns>` |
| Release name | — | `kcloud-mlperf` | `--release <name>` |
| Device mode | Scans `kubectl get nodes` allocatable resources in priority order: `nvidia.com/gpu` → `furiosa.ai/rngd` → `rebellions.ai/ATOM` → CPU fallback | `gpu` if L40/A40 present | `--device auto\|gpu\|npu-rngd\|npu-atom\|cpu` |
| StorageClass | Prefers any class whose provisioner contains `nfs`; falls back to first class with `is-default-class=true`. Warns when two classes are both default (see gotcha below). | `nfs-client` if present | `--storage-class <sc>` |
| PVC access mode | `ReadWriteMany` if NFS provisioner detected; otherwise `ReadWriteOnce` | `ReadWriteMany` (nfs) / `ReadWriteOnce` (local) | — (derived from SC) |
| HF token source | `HF_TOKEN` env → `HUGGING_FACE_HUB_TOKEN` env → `~/.cache/huggingface/token` file → existing `huggingface-token` secret in cluster | first match in order | `--hf-token-source auto\|env\|file:<path>\|secret:<ns>/<name>` |
| Registry prefix | Scans existing Job images in the cluster; empty = public images | empty (public) | `--registry <reg>` |
| Operator presence | Looks for `kcloud-npu-operator` first; falls back to raw device resource detection | raw device plugin OK | — |

### Two-Default StorageClass Gotcha

The current cluster has **two** classes both annotated as default:

```
local-path (default)   rancher.io/local-path          RWO-only
nfs-client (default)   nfs-subdir-external-provisioner  RWX-capable
```

The installer does **not** rely on the `is-default-class` annotation alone. It selects by provisioner substring (`nfs`). If you have a different NFS provisioner name, pass `--storage-class nfs-client` explicitly.

---

## HF Token Handling

Token resolution order (first match wins):

1. Environment variable `HF_TOKEN`
2. Environment variable `HUGGING_FACE_HUB_TOKEN`
3. File `~/.cache/huggingface/token`
4. Existing in-cluster Secret named `huggingface-token` (copied into the target namespace)

**Security guarantees:**
- The token value is base64-encoded before injection into the Secret manifest.
- All installer log lines that reference the token print `***` in place of the value.
- The rendered Secret manifest is never echoed to stdout.
- `--dry-run` does not create the secret; the token is redacted in the printed plan.

If no source is found the installer exits with an actionable error before touching the cluster.

To supply the token explicitly:

```bash
# Via environment
HF_TOKEN=hf_xxxx ./scripts/install_pilot_k8s.sh --node-ips "..."

# Via explicit file
./scripts/install_pilot_k8s.sh --node-ips "..." --hf-token-source file:/path/to/token

# Reuse an existing in-cluster secret
./scripts/install_pilot_k8s.sh --node-ips "..." --hf-token-source secret:llm-bench/huggingface-token
```

---

## Device Modes and Fallback Semantics

Priority (highest to lowest): **gpu → npu-rngd → npu-atom → cpu**

| Mode | Device resource | Benchmark image | Model |
|---|---|---|---|
| `gpu` | `nvidia.com/gpu: 1` | `vllm/vllm-openai:v0.8.4` | `RedHatAI/Meta-Llama-3.1-8B-Instruct-FP8` |
| `npu-rngd` | *(none — thin client hits external server)* | `python:3.11-slim` | `furiosa-ai/Llama-3.1-8B-Instruct-FP8` |
| `npu-atom` | `rebellions.ai/ATOM: 1` | `python:3.11-slim` | `meta-llama/Llama-3.1-8B-Instruct` |
| `cpu` | *(none)* | `python:3.11-slim` | `meta-llama/Llama-3.1-8B-Instruct` |

**npu-rngd note:** the benchmark Job runs as a thin client and targets an external FuriosaAI LLM server (`http://<node>:8000`). No device resource is requested; the Job lands on a CPU node via anti-affinity.

**npu-atom note:** the Rebellions Atom+ device plugin is currently in `Init:0/1` (parked). Selecting `--device npu-atom` will succeed at install time but Jobs will stay `Pending` until the device plugin becomes healthy. See [Troubleshooting](#troubleshooting).

**cpu note:** no accelerator is required. Throughput will be much lower than GPU/NPU paths.

---

## Smoke Test vs. Full Benchmark

| Flag | Behavior |
|---|---|
| `--bench smoke` (default) | Runs a minimal probe Job: asserts device visibility + HF/model reachability; writes a marker to the results PVC. 1 sample, fast. |
| `--bench full` | Runs the full benchmark (100 samples for CNN/DM; full MMLU-Pro sweep). Takes significant time depending on device. |
| `--smoke-test` | Force-run the smoke Job after install (default when `--bench smoke`). |
| `--skip-smoke-test` | Install without running the smoke Job. |

Default model: `meta-llama/Llama-3.1-8B-Instruct`. Override with `--model <hf-id>`.

Smoke uses `N_SAMPLES=1`, `MAX_TOKENS=128`. Full benchmark uses `N_SAMPLES=100`, `MAX_TOKENS=128`.

---

## All Flags Reference

| Flag | Default | Effect |
|---|---|---|
| `--node-ips "<csv>"` | *required* | Comma-separated cluster node InternalIPs. Validated against `kubectl get nodes`. Required except for `--help`. |
| `--namespace <ns>` | `kcloud-mlperf` | Kubernetes namespace for all resources. |
| `--release <name>` | `kcloud-mlperf` | Release label (`part-of`); also prefixes resource names. |
| `--results-pvc-size <s>` | `50Gi` | Size of the results PVC. |
| `--storage-class <sc>` | auto-detect | Override StorageClass selection. |
| `--device <mode>` | `auto` | `auto\|gpu\|npu-rngd\|npu-atom\|cpu`. Auto selects by resource priority. |
| `--model <hf-id>` | `meta-llama/Llama-3.1-8B-Instruct` | HuggingFace model ID. FP8 serving variant is auto-selected for `gpu`/`npu-rngd`. |
| `--hf-token-source <s>` | `auto` | `auto\|env\|file:<path>\|secret:<ns>/<name>` |
| `--registry <reg>` | auto-detect / empty | Private registry prefix prepended to image names. Empty = use public images. |
| `--bench <mode>` | `smoke` | `smoke\|full`. Selects benchmark depth. |
| `--timeout <secs>` | `600` | Seconds to wait for rollout/job completion. |
| `--dry-run` | off | Render templates + `kubectl apply --dry-run=client`; print plan; **no cluster mutation**. |
| `--validate-only` | off | Read-only preflight checks (connectivity, nodes, device, SC, secret). Exit 0 = cluster ready. **No rendering or apply.** |
| `--smoke-test` | on (with default bench=smoke) | Run smoke Job after install and wait for success. |
| `--skip-smoke-test` | off | Install without running the smoke Job. |
| `--cleanup` | off | Delete all resources labeled `managed-by=kcloud-tool` and `part-of=<release>` in `--namespace`. Requires `--force` to delete a non-empty namespace not owned by this installer. |
| `--force` | off | Allow overwriting/non-idempotent actions. Required by `--cleanup` on foreign namespaces. |
| `-h \| --help` | — | Print usage and exit 0. Never touches the cluster. |

---

## Cleanup

```bash
# Delete all managed resources in the default namespace/release
./scripts/install_pilot_k8s.sh --node-ips "..." --cleanup

# Delete a namespace not originally created by this installer (requires --force)
./scripts/install_pilot_k8s.sh --node-ips "..." --cleanup --force --namespace my-ns
```

`--cleanup` selects resources by label:
```
app.kubernetes.io/managed-by=kcloud-tool
app.kubernetes.io/part-of=<release>
```

Resources **not** carrying these labels are never touched. The namespace itself is only deleted if it is labeled as ours OR `--force` is passed.

---

## Troubleshooting

### Pod stuck in `Pending`

```bash
kubectl describe pod -n kcloud-mlperf <pod-name>
# Look at "Events:" section for scheduling failures
```

Common causes:

- **No matching device resource** — the device plugin is not running or not advertising the resource. Check: `kubectl get nodes -o json | jq '.items[].status.allocatable'`
- **StorageClass mismatch** — PVC cannot be bound. Check: `kubectl get pvc -n kcloud-mlperf` and `kubectl describe pvc -n kcloud-mlperf <name>`
- **Node selector mismatch** — GPU jobs use `nvidia.com/gpu.product` node labels. Check: `kubectl get nodes --show-labels | grep gpu.product`

### Reading pod logs and events

```bash
# Pod logs
kubectl logs -n kcloud-mlperf <pod-name>

# All events in namespace (sorted by time)
kubectl get events -n kcloud-mlperf --sort-by='.lastTimestamp'

# Job status
kubectl get jobs -n kcloud-mlperf
kubectl describe job -n kcloud-mlperf kcloud-mlperf-smoke
```

### Two-Default StorageClass gotcha

If `kubectl get sc` shows two classes both annotated `(default)`, the installer picks by NFS provisioner substring — not by annotation. If your NFS provisioner has an unusual name, pass `--storage-class <sc-name>` explicitly. Otherwise the installer may silently choose `local-path` (RWO), which will block multi-node PVC mounts.

Symptom: PVC bound but pods on a different node from the PVC writer report `Multi-Attach error`.

Fix:
```bash
./scripts/install_pilot_k8s.sh --node-ips "..." --storage-class nfs-client
```

### node4 SSH port is 22 (others are 122)

All nodes use SSH port **122** except `node4` (192.0.2.114, FuriosaAI RNGD), which is on the standard port **22**. This is not a cluster error — it is a known host configuration difference. If your tooling assumes port 122 uniformly, add an SSH config override for that IP.

### Atom+ (Rebellions) device plugin parked

The Rebellions device-plugin DaemonSet is in `Init:0/1` state. The `rebellions.ai/ATOM` resource will not appear in node allocatable until it completes initialization. Selecting `--device npu-atom` installs successfully, but Jobs will stay `Pending`.

Check status:
```bash
kubectl get pods -n rbln-system
kubectl describe ds -n rbln-system   # find the device-plugin daemonset
```

The installer emits a `[warn]` when `npu-atom` mode is selected and the device resource is absent from allocatable.

### HF authentication failures

Symptoms: Job exits with `401 Unauthorized` or `Repository Not Found`.

1. Verify token is valid: `curl -s -H "Authorization: Bearer $HF_TOKEN" https://huggingface.co/api/whoami`
2. Verify model access: token must have accepted the gated model license on huggingface.co.
3. Verify the secret was created: `kubectl get secret -n kcloud-mlperf huggingface-token` (the token value itself is base64-encoded and safe to inspect with `-o yaml`).
4. If the in-cluster secret from another namespace was copied, re-run with `--force` to overwrite if the key was stale.

### Validate cluster readiness without installing

```bash
./scripts/install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13" --validate-only
```

Exit 0 = all preflight checks pass. Non-zero = the error message identifies the blocker.

### Dry-run (render + print, no apply)

```bash
./scripts/install_pilot_k8s.sh --node-ips "192.0.2.11,192.0.2.12,192.0.2.13" --dry-run
```

Prints the rendered manifests and the `kubectl apply --dry-run=client` plan. No cluster state is changed.
