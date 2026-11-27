#!/usr/bin/env python3
"""Gate: both (or one) endpoints must report equal num_gpu_blocks."""
from __future__ import annotations

import argparse
import re
import sys
import urllib.request


def blocks(url: str) -> int | None:
    text = urllib.request.urlopen(url.rstrip("/") + "/metrics", timeout=10).read().decode()
    m = re.search(r'num_gpu_blocks="(\d+)"', text)
    return int(m.group(1)) if m else None


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--url", action="append", required=True)
    p.add_argument("--expect", type=int, required=True)
    args = p.parse_args()
    vals = []
    for u in args.url:
        b = blocks(u)
        print(f"{u} num_gpu_blocks={b}")
        if b is None:
            return 2
        vals.append(b)
    if any(v != args.expect for v in vals):
        print(f"FAIL: expected all == {args.expect}, got {vals}", file=sys.stderr)
        return 3
    print(f"GATE PASS: all blocks == {args.expect}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
