#!/usr/bin/env python3
"""Pending snapshot: Allocated vs requests vs usage notes + UID-filtered events."""
from __future__ import annotations

import argparse
import json
import subprocess
from datetime import datetime, timezone
from pathlib import Path


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def kubectl_json(args: list[str]):
    return json.loads(subprocess.check_output(["kubectl", *args], text=True))


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ns", default="accel-jobs")
    ap.add_argument("--node", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--scheduler-log-lines", type=int, default=200)
    args = ap.parse_args()
    dest = Path(args.out)
    dest.mkdir(parents=True, exist_ok=True)

    pods = kubectl_json(["get", "pods", "-n", args.ns, "-o", "json"])
    node = kubectl_json(["get", "node", args.node, "-o", "json"])
    try:
        ev = kubectl_json(["get", "events", "-n", args.ns, "--sort-by=.lastTimestamp", "-o", "json"])
    except Exception as e:
        ev = {"items": [], "error": str(e)}

    alloc = node.get("status", {}).get("allocatable", {})
    # describe allocated is not in json easily — use metrics from pods sum requests
    uid_set = set()
    rows = []
    sum_req_mem = 0
    for pod in pods.get("items", []):
        md = pod["metadata"]
        spec = pod.get("spec", {})
        st = pod.get("status", {})
        uid = md.get("uid")
        uid_set.add(uid)
        res = []
        for c in spec.get("containers", []):
            req = (c.get("resources") or {}).get("requests") or {}
            lim = (c.get("resources") or {}).get("limits") or {}
            res.append({"name": c.get("name"), "requests": req, "limits": lim})
            mem = req.get("memory")
            if mem:
                # rough parse Gi/Mi
                if mem.endswith("Gi"):
                    sum_req_mem += int(float(mem[:-2]) * 1024)
                elif mem.endswith("Mi"):
                    sum_req_mem += int(float(mem[:-2]))
        cond = st.get("conditions") or []
        rows.append(
            {
                "name": md.get("name"),
                "uid": uid,
                "phase": st.get("phase"),
                "nodeName": spec.get("nodeName"),
                "schedulerName": spec.get("schedulerName"),
                "resources": res,
                "conditions": [
                    {
                        "type": c.get("type"),
                        "status": c.get("status"),
                        "reason": c.get("reason"),
                        "message": c.get("message"),
                    }
                    for c in cond
                ],
            }
        )

    # UID-filtered events
    filtered = []
    for e in ev.get("items", []):
        invol = e.get("involvedObject") or {}
        if invol.get("uid") in uid_set or invol.get("name") in {r["name"] for r in rows}:
            filtered.append(
                {
                    "type": e.get("type"),
                    "reason": e.get("reason"),
                    "message": e.get("message"),
                    "count": e.get("count"),
                    "lastTimestamp": e.get("lastTimestamp") or e.get("eventTime"),
                    "involvedObject": {
                        "kind": invol.get("kind"),
                        "name": invol.get("name"),
                        "uid": invol.get("uid"),
                    },
                }
            )

    # scheduler pod logs (best-effort)
    sched_logs = {}
    for app in ("keti-cost-scheduler",):
        try:
            raw = subprocess.check_output(
                [
                    "kubectl",
                    "-n",
                    "kcp-control-plane",
                    "logs",
                    f"deploy/{app}",
                    f"--tail={args.scheduler_log_lines}",
                ],
                text=True,
                stderr=subprocess.STDOUT,
            )
            # keep lines mentioning our pod names/uids
            names = {r["name"] for r in rows}
            keep = []
            for line in raw.splitlines():
                if any(n in line for n in names) or any((u or "") in line for u in uid_set):
                    keep.append(line)
            sched_logs[app] = {"matched_lines": keep[-80:], "n_matched": len(keep)}
            (dest / f"scheduler_{app}_tail.txt").write_text(raw)
        except Exception as e:
            sched_logs[app] = {"error": str(e)}

    mem_alloc = alloc.get("memory", "")
    # parse Ki
    alloc_mi = None
    if mem_alloc.endswith("Ki"):
        alloc_mi = int(mem_alloc[:-2]) // 1024

    rep = {
        "utc": utc_now(),
        "note": "Allocated/requests are reservations, not cgroup usage. Pair with memory.peak series.",
        "node_allocatable": {
            k: alloc.get(k)
            for k in (
                "memory",
                "cpu",
                "nvidia.com/gpu",
                "furiosa.ai/rngd",
                "keti.re.kr/lsu",
            )
        },
        "sum_pod_request_memory_Mi_approx_ns": sum_req_mem,
        "allocatable_memory_Mi_approx": alloc_mi,
        "pods": rows,
        "events_uid_or_name_filtered": filtered,
        "scheduler_log_matches": sched_logs,
    }
    (dest / "pending_rich.json").write_text(json.dumps(rep, indent=2) + "\n")
    (dest / "pods.json").write_text(json.dumps(pods, indent=2) + "\n")
    subprocess.check_call(
        ["kubectl", "describe", "node", args.node],
        stdout=open(dest / "node_describe.txt", "w"),
        stderr=subprocess.STDOUT,
    )
    print(json.dumps({"out": str(dest), "n_pods": len(rows), "n_events": len(filtered)}, indent=2))


if __name__ == "__main__":
    main()
