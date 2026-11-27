#!/usr/bin/env python3
"""Poll container cgroup memory.current / memory.peak for pods of one namespace on this node.

Run on the node. Records paths, UIDs, and host free memory.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def sh(cmd: str) -> str:
    return subprocess.check_output(cmd, shell=True, text=True, stderr=subprocess.DEVNULL)


def host_mem() -> dict:
    out = {}
    for line in Path("/proc/meminfo").read_text().splitlines():
        if ":" not in line:
            continue
        k, v = line.split(":", 1)
        out[k] = v.strip()
    # MemAvailable kB
    def kb(key: str) -> int | None:
        raw = out.get(key, "")
        m = re.match(r"(\d+)", raw)
        return int(m.group(1)) if m else None

    return {
        "MemTotal_kB": kb("MemTotal"),
        "MemAvailable_kB": kb("MemAvailable"),
        "MemFree_kB": kb("MemFree"),
        "utc": utc_now(),
    }


def container_ids_for_pod(ns: str, pod: str) -> list[dict]:
    raw = subprocess.check_output(
        ["kubectl", "-n", ns, "get", "pod", pod, "-o", "json"], text=True
    )
    d = json.loads(raw)
    uid = d["metadata"]["uid"]
    rows = []
    for cs in (d.get("status") or {}).get("containerStatuses") or []:
        cid = (cs.get("containerID") or "").split("//")[-1]
        rows.append(
            {
                "name": cs.get("name"),
                "containerID": cid,
                "ready": cs.get("ready"),
                "pod_uid": uid,
            }
        )
    return rows


def find_cgroup_dir(container_id: str) -> str | None:
    if not container_id:
        return None
    # common containerd paths
    patterns = [
        f"/sys/fs/cgroup/kubepods.slice/*/*/{container_id}*/memory.peak",
        f"/sys/fs/cgroup/kubepods/*/*/{container_id}*/memory.peak",
        f"/sys/fs/cgroup/**/cri-containerd-{container_id}.scope/memory.peak",
    ]
    # use find
    try:
        found = sh(
            f"find /sys/fs/cgroup -type f -name memory.peak 2>/dev/null | grep -F '{container_id}' | head -3"
        ).strip()
    except Exception:
        found = ""
    if not found:
        # fallback: cgroup.procs scan is heavy; try crictl inspect
        try:
            insp = sh(f"crictl inspect {container_id}")
            # look for info.runtimeSpec.linux.cgroupsPath or pid
            j = json.loads(insp)
            pid = j.get("info", {}).get("pid") or j.get("pid")
            if pid:
                cg = Path(f"/proc/{pid}/cgroup").read_text()
                for line in cg.splitlines():
                    # 0::/path for cgroupv2
                    if ":/" in line:
                        path = line.split(":", 2)[-1]
                        cand = Path("/sys/fs/cgroup") / path.lstrip("/")
                        if (cand / "memory.peak").exists():
                            return str(cand)
        except Exception:
            return None
        return None
    peak_path = found.splitlines()[0]
    return str(Path(peak_path).parent)


def read_mem(cg: str) -> dict:
    p = Path(cg)
    def read(name: str) -> int | None:
        f = p / name
        if not f.exists():
            return None
        try:
            return int(f.read_text().strip())
        except Exception:
            return None

    return {
        "cgroup": cg,
        "memory_current": read("memory.current"),
        "memory_peak": read("memory.peak"),
        "memory_max": read("memory.max"),
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ns", default="accel-jobs")
    ap.add_argument("--pods", required=True, help="comma-separated pod names")
    ap.add_argument("--out", required=True)
    ap.add_argument("--interval-s", type=float, default=2.0)
    ap.add_argument("--duration-s", type=float, default=0, help="0=until interrupted")
    args = ap.parse_args()
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    names = [x.strip() for x in args.pods.split(",") if x.strip()]
    t0 = time.time()
    series = []
    meta = {"pods": names, "ns": args.ns, "started_utc": utc_now(), "host_t0": host_mem()}
    (out / "meta_start.json").write_text(json.dumps(meta, indent=2) + "\n")
    try:
        while True:
            row = {"utc": utc_now(), "mono": time.monotonic(), "host": host_mem(), "containers": []}
            for pod in names:
                try:
                    for c in container_ids_for_pod(args.ns, pod):
                        cg = find_cgroup_dir(c["containerID"])
                        mem = read_mem(cg) if cg else {"cgroup": None}
                        row["containers"].append({**c, "pod": pod, **mem})
                except Exception as e:
                    row["containers"].append({"pod": pod, "error": str(e)})
            series.append(row)
            # rolling max peak
            peaks = {}
            for r in series:
                for c in r["containers"]:
                    key = f"{c.get('pod')}/{c.get('name')}"
                    pk = c.get("memory_peak")
                    if pk is not None:
                        peaks[key] = max(peaks.get(key, 0), pk)
            (out / "peaks_rolling.json").write_text(
                json.dumps({"utc": utc_now(), "peaks_bytes": peaks, "host": row["host"]}, indent=2)
                + "\n"
            )
            if args.duration_s > 0 and (time.time() - t0) >= args.duration_s:
                break
            time.sleep(args.interval_s)
    except KeyboardInterrupt:
        pass
    (out / "series.jsonl").write_text("\n".join(json.dumps(x) for x in series) + "\n")
    (out / "meta_end.json").write_text(
        json.dumps({"ended_utc": utc_now(), "host_end": host_mem(), "n_samples": len(series)}, indent=2)
        + "\n"
    )
    print(json.dumps({"n_samples": len(series), "out": str(out)}, indent=2))


if __name__ == "__main__":
    main()
