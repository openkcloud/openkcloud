# Adding a Rebellions Atom+ NPU node (node5) to the kcloud-stack cluster

Verified working 2026-06-04 on the consolidation jw cluster
(jw1 master + jw2/jw3 A30 GPU + node4 RNGD + **node5 Rebellions Atom+**).

## The device

- **node5 = `192.0.2.111`**, 2× **Rebellions ATOM (RBLN-CA22)** NPUs (`rbln0`, `rbln1`),
  exposed in-cluster as `rebellions.ai/ATOM` (see `scripts/lib/detect.sh`).
- Verified specs (per Rebellions, for the roofline/perf docs): ~32 TFLOPS FP16,
  256 GB/s GDDR6 (16 GB), 64 MB on-chip SRAM, ~90 W TDP. BF16/FP8 peak is **not
  published** by Rebellions — leave those cells unverified.
- Host tools: `rbln-stat` / `rbln-smi` at `/usr/local/bin`.

## Prerequisite — SDK / driver alignment (the historical blocker)

Atom+ was previously parked because the host `rebel-compiler` (0.9.3) did not
match the kernel driver (3.0). It is fixed when the whole stack is on one line:

```
kernel driver / rebellions-dkms / rebellions-fw : 3.0.0
rbln-sdk / rebel-compiler / optimum-rbln / vllm_rbln : 0.10.3
rbln-container-toolkit : 0.2.1
```

Verify before joining: `ssh kcloud@192.0.2.111 rbln-stat` should list 2×
RBLN-CA22, healthy, idle.

## Join — kubespray adds the node correctly

Add `192.0.2.111` to the kubespray worker inventory and run the normal
`install_kcloud_stack.sh --provision` flow. **Kubespray installs the
`nginx-proxy` static pod (local API LB on `127.0.0.1:6443`) automatically** —
that is essential and is the part a bare `kubeadm join` omits.

> **GOTCHA (if you ever join a worker by hand with plain `kubeadm join`):** it
> does NOT install the `nginx-proxy` static pod. Then kube-proxy + calico on the
> new node try the API at `127.0.0.1:6443`, get "connection refused", never
> program the ClusterIP ipvs rules (no `kube-ipvs0`), calico `install-cni` can't
> reach `10.233.0.1:443` ("no route to host"), and the node stays **NotReady**
> (calico `Init:CrashLoopBackOff`). Fix: copy `/etc/kubernetes/manifests/nginx-proxy.yml`
> and `/etc/nginx/nginx.conf` (upstream = control-plane IP:6443) from an existing
> worker, then `systemctl restart kubelet`. Also: `kubeadm token create
> --print-join-command` prints the endpoint as `127.0.0.1:6443` — rewrite it to
> the control-plane's real IP for the worker.

## Device registration (no upstream scheduler plugin)

There is **no upstream Rebellions k8s device-plugin** that advertises
`rebellions.ai/ATOM` as a schedulable resource, so `install_kcloud_stack.sh
--device npu-atom` intentionally only *verifies* an existing rbln device
resource (`scripts/lib/stages.sh` `_install_operators` → `npu-atom` case). The
platform surfaces Atom+ via:

1. **Node labels** on node5:
   `accelerator-type=npu, npu-vendor=rebellions, npu-model=atomplus, accelerator-count=2`.
2. **The device registry** (`cluster.yaml` → `etri-llm-cluster-config` ConfigMap
   the backend reads). Add a worker entry:

   ```yaml
     - name: node5
       role: worker
       accelerator: { type: npu, vendor: rebellions, model: "Atom+", count: 2 }
       ssh: { host: 192.0.2.111, port: 22 }
       labels:
         accelerator-type: npu
         npu-vendor: rebellions
         npu-model: atomplus
   ```

> **LIMITATION to fix later:** `scripts/lib/stages.sh` `_cluster_yaml_worker_entry()`
> currently emits `accelerator: { type: cpu, vendor: intel, count: 0 }` for EVERY
> worker — it does not classify GPU/NPU by node. Until that helper consumes the
> per-node accelerator facts (now collected, since discovery greps
> `nvidia|furiosa|rebellions|rbln`), the correct accelerator block must be set in
> the device registry by hand (as above). This affects all accelerators, not just
> Atom+.

After the registry is updated, restart the backend so it reloads:
`kubectl rollout restart deploy/etri-llm-backend -n llm-evaluation`. Atom+ then
shows in the web UI Cluster Inventory as "Rebellions Atom+: 2 devices, READY".
