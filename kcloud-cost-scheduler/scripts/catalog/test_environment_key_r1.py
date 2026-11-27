#!/usr/bin/env python3
"""R1: environment_key must separate MEASURED configs (updated contract)."""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from catalog.workload_context import build_catalog_lookup_key, build_environment_key, build_profile_key

MODEL = "meta-llama/Llama-3.1-8B-Instruct"
COMMON = dict(
    model=MODEL,
    revision="na",
    runtime="vllm",
    accelerator="gpu",
    sku="rtx-pro-6000",
    node="gpu-node-01",
    precision="auto",
    protocol="sustained_closed_loop_120s",
    input_tokens=149,
    output_tokens=128,
    max_num_seqs="na",
)


def main() -> int:
    pk_a = build_profile_key(MODEL, "vllm", "gpu", "auto", 149, 128, 4096, 30)
    pk_b = build_profile_key(MODEL, "vllm", "gpu", "auto", 149, 128, 4096, 30)
    assert pk_a == pk_b

    k1 = build_environment_key(
        **COMMON, gpu_memory_utilization=0.9, kv_blocks=4096, lsu_quota=178, engine_count=1,
        allocation_mode="k1-lsu-exclusive",
    )
    eqkv = build_environment_key(
        **COMMON, gpu_memory_utilization=0.4, kv_blocks=2048, lsu_quota=89, engine_count=1,
        allocation_mode="eqkv-solo",
    )
    compete = build_environment_key(
        **COMMON, gpu_memory_utilization=0.4, kv_blocks=2048, lsu_quota=89, engine_count=2,
        allocation_mode="eqkv-dual-c15",
    )
    keys = {"k1": k1, "eqkv": eqkv, "compete": compete}
    assert len(set(keys.values())) == 3, keys
    assert build_catalog_lookup_key(k1, 30).endswith(":c30")
    print("PASS R1 environment_key separation")
    for name, key in keys.items():
        print(f"  {name}: {key}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
