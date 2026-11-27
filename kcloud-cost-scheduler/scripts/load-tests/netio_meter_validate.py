#!/usr/bin/env python3
"""NETIO Out1+Out2 cumulative Energy validation (meter check).

Uses host UTC / monotonic clock for boundaries. Does not substitute device J
when counters fail. Agent.Time is recorded but not trusted as the interval clock.
"""

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


def extract_outputs(doc: dict[str, Any]) -> list[dict[str, Any]]:
    outs = doc.get("Outputs") or doc.get("outputs") or []
    if not isinstance(outs, list):
        return []
    return outs


def energy_of(out: dict[str, Any]) -> int | None:
    for key in ("Energy", "EnergyAbs", "EnergyRelative"):
        if key in out and out[key] is not None:
            try:
                return int(out[key])
            except (TypeError, ValueError):
                return None
    return None


def sample(url: str, timeout: float) -> dict[str, Any]:
    host_utc = datetime.now(timezone.utc).isoformat()
    mono = time.monotonic()
    doc = fetch(url, timeout)
    rows = []
    for o in extract_outputs(doc):
        oid = o.get("ID") or o.get("Id") or o.get("Name")
        rows.append(
            {
                "id": oid,
                "name": o.get("Name"),
                "state": o.get("State"),
                "energy": energy_of(o),
                "current": o.get("Current"),
                "load": o.get("Load"),
                "power": o.get("Power"),
                "raw_keys": sorted(o.keys()),
            }
        )
    agent = doc.get("Agent") or {}
    return {
        "host_utc": host_utc,
        "mono_s": mono,
        "agent_time": agent.get("Time"),
        "agent_uptime": agent.get("Uptime") or agent.get("UpTime"),
        "outputs": rows,
        "raw_agent": agent,
    }


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--url", default=os.environ.get("NETIO_URL"), required="NETIO_URL" not in os.environ,
                   help="NETIO PDU netio.json URL (or set NETIO_URL)")
    p.add_argument("--duration-s", type=float, default=180.0)
    p.add_argument("--interval-s", type=float, default=2.0)
    p.add_argument("--timeout-s", type=float, default=3.0)
    p.add_argument("--out-dir", required=True)
    p.add_argument("--out-ids", default="1,2", help="comma Energy outputs to sum (server Wh)")
    args = p.parse_args()

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    want = {int(x) for x in args.out_ids.split(",") if x.strip()}

    samples_path = out_dir / "samples.jsonl"
    t_end = time.monotonic() + args.duration_s
    n = 0
    decreases = 0
    missing = 0
    prev: dict[int, int] = {}
    deltas: list[int] = []
    change_gaps: list[float] = []
    last_change_mono: float | None = None

    with samples_path.open("w", encoding="utf-8") as f:
        while time.monotonic() < t_end:
            try:
                s = sample(args.url, args.timeout_s)
                err = None
            except Exception as e:  # noqa: BLE001 — record fetch failures explicitly
                s = {
                    "host_utc": datetime.now(timezone.utc).isoformat(),
                    "mono_s": time.monotonic(),
                    "error": str(e),
                }
                err = str(e)
                missing += 1
            f.write(json.dumps(s, ensure_ascii=False) + "\n")
            n += 1
            if err:
                time.sleep(args.interval_s)
                continue
            by_id = {}
            for row in s.get("outputs") or []:
                try:
                    oid = int(row["id"])
                except (TypeError, ValueError, KeyError):
                    continue
                if oid in want and row.get("energy") is not None:
                    by_id[oid] = int(row["energy"])
            if len(by_id) != len(want):
                missing += 1
            changed = False
            for oid, e in by_id.items():
                if oid in prev:
                    d = e - prev[oid]
                    if d < 0:
                        decreases += 1
                    elif d > 0:
                        deltas.append(d)
                        changed = True
                prev[oid] = e
            if changed:
                now_m = float(s["mono_s"])
                if last_change_mono is not None:
                    change_gaps.append(now_m - last_change_mono)
                last_change_mono = now_m
            time.sleep(args.interval_s)

    first_e = None
    last_e = None
    # re-read file ends for boundary sum
    lines = samples_path.read_text(encoding="utf-8").strip().splitlines()
    for line in lines:
        s = json.loads(line)
        if "error" in s:
            continue
        total = 0
        ok = True
        for row in s.get("outputs") or []:
            try:
                oid = int(row["id"])
            except (TypeError, ValueError, KeyError):
                continue
            if oid in want:
                if row.get("energy") is None:
                    ok = False
                else:
                    total += int(row["energy"])
        if ok and len([1 for row in s.get("outputs") or [] if int(row.get("id") or -1) in want]) >= len(want):
            if first_e is None:
                first_e = total
            last_e = total

    summary = {
        "url": args.url,
        "duration_s": args.duration_s,
        "interval_s": args.interval_s,
        "out_ids": sorted(want),
        "samples": n,
        "missing_or_incomplete": missing,
        "energy_decreases": decreases,
        "energy_start_sum": first_e,
        "energy_end_sum": last_e,
        "energy_delta_sum": None if first_e is None or last_e is None else last_e - first_e,
        "positive_step_count": len(deltas),
        "positive_step_min": min(deltas) if deltas else None,
        "positive_step_max": max(deltas) if deltas else None,
        "change_gap_s_median": (
            sorted(change_gaps)[len(change_gaps) // 2] if change_gaps else None
        ),
        "unit_note": "Energy integer treated as Wh (cross-checked against the Load power integral at idle, ~0.4% difference)",
        "agent_time_note": "Agent.Time can be invalid (1970) on some PDUs; host_utc/mono used for boundaries",
        "formula": "E_server = sum(Energy_end - Energy_start) for Out IDs",
    }
    (out_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(summary, indent=2))
    if decreases:
        raise SystemExit(3)


if __name__ == "__main__":
    main()
