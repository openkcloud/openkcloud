#!/usr/bin/env python3
"""Pre-open Pod watch; create-to-assign timing with explicit create-request stamps.

CRITICAL: never use first watch observation as create-request time.
  t_create_req  = client monotonic just before Pod create API
  t_create_resp = client monotonic when create response+UID known
  t_first_watch = first watch sight (diagnostic only)
  t_assigned    = first watch/event with nodeName for that UID
  W_sched_s     = t_assigned - t_create_req   (primary)
  W_api_s       = t_assigned - t_create_resp  (secondary)
"""
from __future__ import annotations

import argparse
import json
import subprocess
import threading
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def mono() -> float:
    return time.monotonic()


def kubectl_json(args: list[str]) -> Any:
    raw = subprocess.check_output(["kubectl", *args], text=True)
    return json.loads(raw)


class PodAssignWatch:
    def __init__(self, ns: str, out_dir: Path, label_selector: str | None = None):
        self.ns = ns
        self.out = out_dir
        self.out.mkdir(parents=True, exist_ok=True)
        self.label_selector = label_selector
        self.t_work_submit_mono: float | None = None
        self.t_job_apply_mono: float | None = None
        self.lock = threading.Lock()
        self.pods: dict[str, dict[str, Any]] = {}  # uid -> record
        self.by_name: dict[str, str] = {}  # name -> uid (after create resp)
        self.pending_by_name: dict[str, dict[str, Any]] = {}  # name -> pre-UID stamps
        self.watch_meta: dict[str, Any] = {
            "started_utc": None,
            "resource_version": None,
            "reconnects": 0,
            "errors": [],
            "contract": "t_create_req != t_first_watch; W_sched uses t_create_req only",
        }
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def mark_work_submit(self) -> float:
        self.t_work_submit_mono = mono()
        (self.out / "t_work_submit_mono.txt").write_text(f"{self.t_work_submit_mono}\n")
        (self.out / "t_work_submit_utc.txt").write_text(utc_now() + "\n")
        return self.t_work_submit_mono

    def mark_job_apply(self) -> float:
        """Job apply is NOT pod create-request. Record separately only."""
        self.t_job_apply_mono = mono()
        (self.out / "t_job_apply_mono.txt").write_text(f"{self.t_job_apply_mono}\n")
        (self.out / "t_job_apply_utc.txt").write_text(utc_now() + "\n")
        return self.t_job_apply_mono

    def begin_create_request(self, name: str) -> float:
        """Call immediately before kubectl/API create for this pod name."""
        t = mono()
        with self.lock:
            self.pending_by_name[name] = {
                "name": name,
                "t_create_req_mono": t,
                "t_create_req_utc": utc_now(),
            }
        return t

    def complete_create_response(self, name: str, uid: str, create_resp_mono: float | None = None) -> None:
        """Call when create response returned with UID (not watch sight)."""
        t = create_resp_mono if create_resp_mono is not None else mono()
        with self.lock:
            pend = self.pending_by_name.pop(name, {})
            # Watch may have already created the record and consumed pending — merge, do not wipe.
            rec = self.pods.get(uid)
            if rec is None and name in self.by_name:
                rec = self.pods.get(self.by_name[name])
            if rec is None:
                rec = {
                    "uid": uid,
                    "name": name,
                    "t_create_req_mono": None,
                    "t_create_req_utc": None,
                    "t_create_resp_mono": None,
                    "t_create_resp_utc": None,
                    "t_first_watch_mono": None,
                    "t_first_watch_utc": None,
                    "t_assigned_mono": None,
                    "t_assigned_utc": None,
                    "nodeName": None,
                    "phase": None,
                    "schedulerName": None,
                    "W_sched_s": None,
                    "W_api_s": None,
                    "W_work_s": None,
                    "create_source": "client_api",
                }
                self.pods[uid] = rec
            else:
                # ensure uid key (watch may have registered under same uid already)
                self.pods[uid] = rec
            if pend.get("t_create_req_mono") is not None:
                rec["t_create_req_mono"] = pend["t_create_req_mono"]
                rec["t_create_req_utc"] = pend.get("t_create_req_utc")
            rec["uid"] = uid
            rec["name"] = name
            rec["t_create_resp_mono"] = t
            rec["t_create_resp_utc"] = utc_now()
            if rec.get("create_source") == "watch_only_no_client_stamp":
                rec["create_source"] = "client_api_merged_watch"
            elif not rec.get("create_source"):
                rec["create_source"] = "client_api"
            # recompute waits if already assigned
            if rec.get("t_assigned_mono") is not None:
                ta = rec["t_assigned_mono"]
                tc = rec.get("t_create_req_mono")
                tr = rec.get("t_create_resp_mono")
                rec["W_sched_s"] = None if tc is None else ta - tc
                rec["W_api_s"] = None if tr is None else ta - tr
            self.by_name[name] = uid

    def start(self) -> None:
        args = ["get", "pods", "-n", self.ns, "-o", "json"]
        if self.label_selector:
            args.extend(["-l", self.label_selector])
        try:
            lst = kubectl_json(args)
            rv = lst.get("metadata", {}).get("resourceVersion")
        except Exception as e:
            self.watch_meta["errors"].append({"utc": utc_now(), "stage": "list", "err": str(e)})
            rv = None
        self.watch_meta["started_utc"] = utc_now()
        self.watch_meta["resource_version"] = rv
        self._thread = threading.Thread(target=self._run_watch, daemon=True)
        self._thread.start()

    def stop(self, timeout: float = 5.0) -> None:
        self._stop.set()
        if self._thread:
            self._thread.join(timeout=timeout)
        self.flush()

    def _run_watch(self) -> None:
        while not self._stop.is_set():
            cmd = ["kubectl", "get", "pods", "-n", self.ns, "-o", "json", "--watch"]
            if self.label_selector:
                cmd.extend(["-l", self.label_selector])
            try:
                proc = subprocess.Popen(
                    cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1
                )
            except Exception as e:
                self.watch_meta["errors"].append({"utc": utc_now(), "stage": "popen", "err": str(e)})
                self.watch_meta["reconnects"] += 1
                time.sleep(0.5)
                continue
            assert proc.stdout is not None
            buf = ""
            depth = 0
            while not self._stop.is_set():
                ch = proc.stdout.read(1)
                if ch == "":
                    break
                buf += ch
                if ch == "{":
                    depth += 1
                elif ch == "}":
                    depth -= 1
                    if depth == 0 and buf.strip():
                        try:
                            self._handle_obj(json.loads(buf))
                        except json.JSONDecodeError as e:
                            self.watch_meta["errors"].append(
                                {"utc": utc_now(), "stage": "json", "err": str(e)}
                            )
                        buf = ""
            try:
                proc.kill()
            except Exception:
                pass
            if self._stop.is_set():
                break
            self.watch_meta["reconnects"] += 1
            time.sleep(0.3)

    def _handle_obj(self, obj: dict[str, Any]) -> None:
        if obj.get("kind") == "Pod":
            pod = obj
        elif "object" in obj:
            pod = obj["object"]
        else:
            return
        md = pod.get("metadata") or {}
        uid = md.get("uid")
        name = md.get("name")
        if not uid:
            return
        spec = pod.get("spec") or {}
        st = pod.get("status") or {}
        node = spec.get("nodeName")
        phase = st.get("phase")
        now_m = mono()
        now_u = utc_now()
        with self.lock:
            rec = self.pods.get(uid)
            if rec is None:
                # Seen on watch before/without client create stamp — record watch only;
                # do NOT invent t_create_req from first sight.
                rec = {
                    "uid": uid,
                    "name": name,
                    "t_create_req_mono": None,
                    "t_create_req_utc": None,
                    "t_create_resp_mono": None,
                    "t_create_resp_utc": None,
                    "t_first_watch_mono": now_m,
                    "t_first_watch_utc": now_u,
                    "t_assigned_mono": None,
                    "t_assigned_utc": None,
                    "nodeName": None,
                    "phase": phase,
                    "schedulerName": spec.get("schedulerName"),
                    "W_sched_s": None,
                    "W_api_s": None,
                    "W_work_s": None,
                    "create_source": "watch_only_no_client_stamp",
                }
                # attach pending by name if client stamped before UID known
                if name and name in self.pending_by_name:
                    pend = self.pending_by_name.pop(name)
                    rec["t_create_req_mono"] = pend.get("t_create_req_mono")
                    rec["t_create_req_utc"] = pend.get("t_create_req_utc")
                    rec["create_source"] = "client_req_plus_watch_uid"
                self.pods[uid] = rec
                if name:
                    self.by_name[name] = uid
            else:
                if rec.get("t_first_watch_mono") is None:
                    rec["t_first_watch_mono"] = now_m
                    rec["t_first_watch_utc"] = now_u
                rec["name"] = name or rec["name"]
                rec["phase"] = phase
                if spec.get("schedulerName"):
                    rec["schedulerName"] = spec.get("schedulerName")
            if node and rec.get("t_assigned_mono") is None:
                rec["t_assigned_mono"] = now_m
                rec["t_assigned_utc"] = now_u
                rec["nodeName"] = node
                tc = rec.get("t_create_req_mono")
                tr = rec.get("t_create_resp_mono")
                rec["W_sched_s"] = None if tc is None else now_m - tc
                rec["W_api_s"] = None if tr is None else now_m - tr
                tw = self.t_work_submit_mono
                rec["W_work_s"] = None if tw is None else now_m - tw

    def flush(self) -> dict[str, Any]:
        with self.lock:
            assigned = {u: r for u, r in self.pods.items() if r.get("t_assigned_mono") is not None}
            unassigned = {u: r for u, r in self.pods.items() if r.get("t_assigned_mono") is None}
            # only samples with real create_req count for W_sched stats
            sched_samples = [
                r["W_sched_s"]
                for r in assigned.values()
                if r.get("W_sched_s") is not None and r.get("t_create_req_mono") is not None
            ]
            rep = {
                "t_work_submit_mono": self.t_work_submit_mono,
                "t_job_apply_mono": self.t_job_apply_mono,
                "watch_meta": self.watch_meta,
                "n_pods_seen": len(self.pods),
                "n_assigned": len(assigned),
                "n_unassigned": len(unassigned),
                "n_W_sched_samples": len(sched_samples),
                "assigned": assigned,
                "unassigned": unassigned,
                "pending_create_without_uid": dict(self.pending_by_name),
                "note": (
                    "Primary latency W_sched_s = t_assigned - t_create_req. "
                    "t_first_watch must not replace t_create_req. "
                    "Pods without client create stamp are excluded from W_sched aggregates."
                ),
            }
        (self.out / "assign_watch.json").write_text(json.dumps(rep, indent=2) + "\n")
        (self.out / "watch_meta.json").write_text(json.dumps(self.watch_meta, indent=2) + "\n")
        return rep


def create_pod_timed(watch: PodAssignWatch, yaml_path: Path) -> dict[str, Any]:
    """kubectl create -f pod.yaml with create_req/resp stamps. Returns name/uid."""
    # read name from yaml quickly
    import re

    text = yaml_path.read_text()
    m = re.search(r"^\s*name:\s*(\S+)\s*$", text, re.M)
    if not m:
        raise RuntimeError(f"no metadata.name in {yaml_path}")
    name = m.group(1)
    watch.begin_create_request(name)
    # create and capture uid from output
    proc = subprocess.run(
        ["kubectl", "create", "-f", str(yaml_path), "-o", "json"],
        capture_output=True,
        text=True,
    )
    t_resp = mono()
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr or proc.stdout)
    obj = json.loads(proc.stdout)
    uid = obj["metadata"]["uid"]
    watch.complete_create_response(name, uid, create_resp_mono=t_resp)
    return {"name": name, "uid": uid, "t_create_resp_mono": t_resp}


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--ns", default="accel-jobs")
    ap.add_argument("--out", required=True)
    ap.add_argument("--duration-s", type=float, default=0)
    args = ap.parse_args()
    w = PodAssignWatch(args.ns, Path(args.out))
    w.start()
    w.mark_work_submit()
    if args.duration_s > 0:
        time.sleep(args.duration_s)
        w.stop()
        print(json.dumps(w.flush(), indent=2)[:2000])
    else:
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            w.stop()


if __name__ == "__main__":
    main()
