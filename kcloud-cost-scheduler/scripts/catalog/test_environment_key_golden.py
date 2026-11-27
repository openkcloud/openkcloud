#!/usr/bin/env python3
"""Golden: Python build_environment_key matches shared JSON (and thus Go)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
from catalog.workload_context import build_catalog_lookup_key, build_environment_key

GOLDEN_CANDIDATES = [
    Path(__file__).resolve().parent / "testdata" / "environment_keys_golden.json",
    Path(__file__).resolve().parents[2]
    / "cost-based-scheduler"
    / "internal"
    / "workloadcontext"
    / "testdata"
    / "environment_keys_golden.json",
]


def _golden_path() -> Path:
    for p in GOLDEN_CANDIDATES:
        if p.exists():
            return p
    raise FileNotFoundError("environment_keys_golden.json not found in " + str(GOLDEN_CANDIDATES))


def main() -> int:
    data = json.loads(_golden_path().read_text(encoding="utf-8"))
    for case in data["cases"]:
        inp = case["input"]
        got = build_environment_key(
            model=inp["model"],
            revision=inp.get("revision", ""),
            runtime=inp["runtime"],
            accelerator=inp["accelerator"],
            sku=inp.get("sku", ""),
            node=inp.get("node", ""),
            precision=inp.get("precision", "auto"),
            protocol=inp.get("protocol", ""),
            input_tokens=int(inp["input_tokens"]),
            output_tokens=int(inp["output_tokens"]),
            gpu_memory_utilization=inp.get("gpu_memory_utilization"),
            kv_blocks=inp.get("kv_blocks"),
            max_num_seqs=inp.get("max_num_seqs"),
            lsu_quota=inp.get("lsu_quota"),
            engine_count=inp.get("engine_count", 1),
            allocation_mode=inp.get("allocation_mode", ""),
        )
        if got != case["environment_key"]:
            print(f"FAIL {case['id']} env key\n got {got}\nwant {case['environment_key']}")
            return 1
        if case.get("lookup_c30"):
            lk = build_catalog_lookup_key(got, 30)
            if lk != case["lookup_c30"]:
                print(f"FAIL {case['id']} lookup_c30\n got {lk}\nwant {case['lookup_c30']}")
                return 1
        if case.get("lookup_c15"):
            lk = build_catalog_lookup_key(got, 15)
            if lk != case["lookup_c15"]:
                print(f"FAIL {case['id']} lookup_c15\n got {lk}\nwant {case['lookup_c15']}")
                return 1
        print(f"OK {case['id']}")
    print("PASS python golden environment_key")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
