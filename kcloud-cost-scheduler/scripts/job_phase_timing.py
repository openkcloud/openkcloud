#!/usr/bin/env python3
"""Merge per-job time intervals without double-counting overlaps.

Server-level load (or any phase) share must use the union length of intervals,
not the sum of per-job durations. Phases need not partition 100% of wall time.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Iterable, Sequence


Interval = tuple[float, float]


def merge_union(intervals: Sequence[Interval]) -> list[Interval]:
    cleaned: list[Interval] = []
    for a, b in intervals:
        if b <= a:
            continue
        cleaned.append((float(a), float(b)))
    if not cleaned:
        return []
    cleaned.sort()
    out = [cleaned[0]]
    for a, b in cleaned[1:]:
        la, lb = out[-1]
        if a <= lb:
            out[-1] = (la, max(lb, b))
        else:
            out.append((a, b))
    return out


def union_length(intervals: Sequence[Interval]) -> float:
    return sum(b - a for a, b in merge_union(intervals))


def share(union_s: float, wall_s: float) -> float | None:
    if wall_s <= 0:
        return None
    return union_s / wall_s


def intervals_from_jobs(
    jobs: Iterable[dict], phase: str
) -> list[Interval]:
    """Expect each job to carry f'{phase}_start_unix' and f'{phase}_end_unix'."""
    out: list[Interval] = []
    sk, ek = f"{phase}_start_unix", f"{phase}_end_unix"
    for j in jobs:
        if sk in j and ek in j and j[sk] is not None and j[ek] is not None:
            out.append((float(j[sk]), float(j[ek])))
    return out


def summarize(
    jobs: list[dict],
    wall_start_unix: float,
    wall_end_unix: float,
    phases: Sequence[str] = ("load", "warm", "wait", "run"),
) -> dict:
    wall_s = float(wall_end_unix) - float(wall_start_unix)
    phase_stats: dict[str, dict] = {}
    for ph in phases:
        iv = intervals_from_jobs(jobs, ph)
        u = union_length(iv)
        phase_stats[ph] = {
            "n_intervals": len(iv),
            "sum_s": sum(b - a for a, b in iv if b > a),
            "union_s": u,
            "union_share_of_wall": share(u, wall_s),
            "merged_intervals": merge_union(iv),
        }
    return {
        "wall_start_unix": wall_start_unix,
        "wall_end_unix": wall_end_unix,
        "wall_s": wall_s,
        "rule": "server_phase_share = union_length / wall_s (overlap counted once)",
        "phases_need_not_sum_to_100pct": True,
        "phases": phase_stats,
        "jobs": jobs,
    }


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument(
        "--jobs-file",
        required=True,
        help="JSON list of jobs with <phase>_start_unix / <phase>_end_unix",
    )
    p.add_argument("--wall-start-unix", type=float, required=True)
    p.add_argument("--wall-end-unix", type=float, required=True)
    p.add_argument(
        "--phases",
        default="load,warm,wait,run",
        help="comma-separated phase names",
    )
    p.add_argument("--out", default="")
    args = p.parse_args()

    raw = json.loads(Path(args.jobs_file).read_text(encoding="utf-8"))
    if isinstance(raw, dict) and "jobs" in raw:
        jobs = raw["jobs"]
    elif isinstance(raw, list):
        jobs = raw
    else:
        raise SystemExit("jobs-file must be a list or {jobs:[...]}")

    phases = [x.strip() for x in args.phases.split(",") if x.strip()]
    rep = summarize(jobs, args.wall_start_unix, args.wall_end_unix, phases)
    text = json.dumps(rep, indent=2) + "\n"
    if args.out:
        Path(args.out).write_text(text, encoding="utf-8")
    print(text)


if __name__ == "__main__":
    main()
