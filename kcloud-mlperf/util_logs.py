from __future__ import annotations

import re
from pathlib import Path
from typing import Dict, List


def parse_summary(summary_path: Path) -> Dict[str, float]:
    metrics: Dict[str, float] = {}
    if not summary_path.exists():
        return metrics
    text = summary_path.read_text(encoding="utf-8", errors="ignore")
    for line in text.splitlines():
        if "=" not in line:
            continue
        key, val = line.split("=", 1)
        key = key.strip()
        val = val.strip()
        try:
            metrics[key] = float(val)
        except Exception:
            # leave non-numeric entries out
            pass
    return metrics


def parse_detail(detail_path: Path) -> List[float]:
    latencies: List[float] = []
    if not detail_path.exists():
        return latencies
    text = detail_path.read_text(encoding="utf-8", errors="ignore")
    pattern = re.compile(r"latency_ms=([0-9]+\.?[0-9]*)")
    for line in text.splitlines():
        m = pattern.search(line)
        if m:
            try:
                latencies.append(float(m.group(1)))
            except Exception:
                pass
    return latencies


