#!/usr/bin/env python3
"""Single NETIO Out1+Out2 cumulative Energy snapshot for interval boundaries."""

from __future__ import annotations

import argparse
import json
import os
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def fetch(url: str, timeout: float) -> dict[str, Any]:
    with urllib.request.urlopen(url, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def snap(url: str, timeout: float, out_ids: set[int]) -> dict[str, Any]:
    host_utc = datetime.now(timezone.utc).isoformat()
    mono = time.monotonic()
    doc = fetch(url, timeout)
    rows = []
    total = 0
    ok = True
    for o in doc.get("Outputs") or doc.get("outputs") or []:
        try:
            oid = int(o.get("ID") or o.get("Id") or -1)
        except (TypeError, ValueError):
            continue
        if oid not in out_ids:
            continue
        e = o.get("Energy")
        if e is None:
            ok = False
            e_i = None
        else:
            e_i = int(e)
            total += e_i
        rows.append(
            {
                "id": oid,
                "energy": e_i,
                "load": o.get("Load"),
                "power": o.get("Power"),
                "state": o.get("State"),
            }
        )
    return {
        "host_utc": host_utc,
        "mono_s": mono,
        "ok": ok and len(rows) == len(out_ids),
        "energy_sum": total if ok and len(rows) == len(out_ids) else None,
        "outputs": rows,
        "agent_time": (doc.get("Agent") or {}).get("Time"),
    }


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--url", default=os.environ.get("NETIO_URL"), required="NETIO_URL" not in os.environ,
                   help="NETIO PDU netio.json URL (or set NETIO_URL)")
    p.add_argument("--timeout-s", type=float, default=3.0)
    p.add_argument("--out-ids", default="1,2")
    p.add_argument("--label", default="snap")
    p.add_argument("--out", required=True)
    args = p.parse_args()
    want = {int(x) for x in args.out_ids.split(",") if x.strip()}
    s = snap(args.url, args.timeout_s, want)
    s["label"] = args.label
    path = Path(args.out)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(s, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(s, indent=2))
    if not s["ok"]:
        raise SystemExit(2)


if __name__ == "__main__":
    main()
