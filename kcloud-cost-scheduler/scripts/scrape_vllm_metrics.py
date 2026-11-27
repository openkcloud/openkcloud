#!/usr/bin/env python3
"""Poll vLLM /metrics until killed; append CSV rows."""
from __future__ import annotations

import csv
import re
import sys
import time
import urllib.request

url, out, interval = sys.argv[1], sys.argv[2], float(sys.argv[3])
pat = re.compile(
    r"^(vllm:num_requests_running|vllm:num_requests_waiting|vllm:kv_cache_usage_perc)\{[^}]*\}\s+([0-9.eE+-]+)"
)
with open(out, "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=["timestamp_unix", "running", "waiting", "kv_cache_pct"])
    w.writeheader()
    f.flush()
    while True:
        row = {"timestamp_unix": time.time(), "running": None, "waiting": None, "kv_cache_pct": None}
        try:
            text = urllib.request.urlopen(url, timeout=2).read().decode()
            for line in text.splitlines():
                m = pat.match(line)
                if not m:
                    continue
                name, val = m.group(1), float(m.group(2))
                if name.endswith("running"):
                    row["running"] = val
                elif name.endswith("waiting"):
                    row["waiting"] = val
                elif name.endswith("kv_cache_usage_perc"):
                    row["kv_cache_pct"] = val * 100.0
        except Exception:
            pass
        w.writerow(row)
        f.flush()
        time.sleep(interval)
