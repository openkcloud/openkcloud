#!/usr/bin/env python3
"""Q2 scheduling latency v2 — API client + pre-watch timestamps.

Timestamps (monotonic + wall):
  t_submit_send     — immediately before pods.create
  t_create_resp     — when create API returns
  t_watch_assigned  — first watch event with non-empty spec.nodeName

Also records scheduler pod identity around mode switches, per-trial scheduler
log excerpts, and catalog-api access lines (B1). Does not add serving load.
"""
from __future__ import annotations

import argparse
import json
import os
import random
import re
import subprocess
import threading
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

from kubernetes import client, config, watch
from kubernetes.client.rest import ApiException


def utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def write_json(path: Path, obj: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(obj, ensure_ascii=False, indent=2), encoding="utf-8")


def sh(args: list[str], check: bool = True) -> str:
    r = subprocess.run(args, text=True, capture_output=True)
    if check and r.returncode:
        raise RuntimeError(f"{args}: {r.stderr[-2000:]}")
    return r.stdout


def load_api() -> client.CoreV1Api:
    try:
        config.load_incluster_config()
    except config.ConfigException:
        config.load_kube_config()
    return client.CoreV1Api()


def sched_deploy_env() -> dict[str, str]:
    raw = sh(
        [
            "kubectl",
            "-n",
            "kcp-control-plane",
            "get",
            "deploy",
            "keti-cost-scheduler",
            "-o",
            "json",
        ]
    )
    d = json.loads(raw)
    c0 = d["spec"]["template"]["spec"]["containers"][0]
    env = {e["name"]: e.get("value", "") for e in (c0.get("env") or [])}
    return {
        "image": c0.get("image") or "",
        "uid": d["metadata"]["uid"],
        "resourceVersion": d["metadata"]["resourceVersion"],
        "SCHED_CATALOG_MODE": env.get("SCHED_CATALOG_MODE", ""),
        "SCHED_SHADOW_SCORE_WEIGHT": env.get("SCHED_SHADOW_SCORE_WEIGHT", ""),
        "SCHED_CATALOG_CACHE_TTL_SEC": env.get("SCHED_CATALOG_CACHE_TTL_SEC", ""),
        "SCHED_CATALOG_TIMEOUT_MS": env.get("SCHED_CATALOG_TIMEOUT_MS", ""),
        "env": env,
    }


def sched_pod_snapshot() -> dict[str, Any]:
    raw = sh(
        [
            "kubectl",
            "-n",
            "kcp-control-plane",
            "get",
            "pods",
            "-l",
            "app=keti-cost-scheduler",
            "-o",
            "json",
        ]
    )
    items = json.loads(raw).get("items") or []
    out = []
    for p in items:
        c0 = (p.get("spec") or {}).get("containers") or [{}]
        c0 = c0[0]
        out.append(
            {
                "name": p["metadata"]["name"],
                "uid": p["metadata"]["uid"],
                "resourceVersion": p["metadata"]["resourceVersion"],
                "phase": (p.get("status") or {}).get("phase"),
                "startTime": (p.get("status") or {}).get("startTime"),
                "nodeName": (p.get("spec") or {}).get("nodeName"),
                "image": c0.get("image"),
                "command": c0.get("command"),
                "args": c0.get("args"),
            }
        )
    return {"pods": out, "utc": utc()}


def set_sched_mode(mode: str) -> dict[str, Any]:
    """Patch MODE, wait rollout, return before/after pod snapshots."""
    before = sched_pod_snapshot()
    before_env = sched_deploy_env()
    patch = {
        "spec": {
            "template": {
                "spec": {
                    "containers": [
                        {
                            "name": "keti-cost-scheduler",
                            "env": [{"name": "SCHED_CATALOG_MODE", "value": mode}],
                        }
                    ]
                }
            }
        }
    }
    sh(
        [
            "kubectl",
            "-n",
            "kcp-control-plane",
            "patch",
            "deploy",
            "keti-cost-scheduler",
            "--type=strategic",
            "-p",
            json.dumps(patch),
        ]
    )
    sh(
        [
            "kubectl",
            "-n",
            "kcp-control-plane",
            "rollout",
            "status",
            "deploy/keti-cost-scheduler",
            "--timeout=180s",
        ]
    )
    # settle: old pod gone, new Ready
    deadline = time.time() + 60
    after = sched_pod_snapshot()
    while time.time() < deadline:
        after = sched_pod_snapshot()
        pods = after["pods"]
        if len(pods) == 1 and pods[0].get("phase") == "Running":
            break
        time.sleep(0.5)
    got = sched_deploy_env()
    if got["SCHED_CATALOG_MODE"] != mode:
        raise RuntimeError(f"mode verify failed want={mode} got={got['SCHED_CATALOG_MODE']}")
    time.sleep(2.0)  # brief settle after Ready
    return {
        "wanted_mode": mode,
        "before_pods": before,
        "after_pods": after,
        "before_env_mode": before_env["SCHED_CATALOG_MODE"],
        "after_env": got,
        "utc": utc(),
    }


def make_pod_body(
    name: str,
    ns: str,
    scheduler_name: str,
    *,
    use_gpu: bool,
    experiment_id: str,
    arm: str,
    block: int,
    trial: int,
    warmup: bool,
) -> client.V1Pod:
    labels = {
        "app": "q2-schedule-latency",
        "experiment.kcloud.io/id": experiment_id,
        "experiment.kcloud.io/arm": arm.lower(),
        "experiment.kcloud.io/block": str(block),
        "experiment.kcloud.io/trial": str(trial),
        "experiment.kcloud.io/warmup": "1" if warmup else "0",
    }
    if use_gpu:
        resources = client.V1ResourceRequirements(
            requests={"cpu": "100m", "memory": "256Mi", "nvidia.com/gpu": "1"},
            limits={"cpu": "500m", "memory": "512Mi", "nvidia.com/gpu": "1"},
        )
        runtime_class = "nvidia"
    else:
        resources = client.V1ResourceRequirements(
            requests={"cpu": "50m", "memory": "64Mi"},
            limits={"cpu": "200m", "memory": "128Mi"},
        )
        runtime_class = None
    meta = client.V1ObjectMeta(name=name, namespace=ns, labels=labels)
    container = client.V1Container(
        name="pause",
        image="registry.k8s.io/pause:3.9",
        image_pull_policy="IfNotPresent",
        resources=resources,
    )
    spec = client.V1PodSpec(
        containers=[container],
        restart_policy="Never",
        scheduler_name=scheduler_name or None,
        runtime_class_name=runtime_class,
    )
    return client.V1Pod(api_version="v1", kind="Pod", metadata=meta, spec=spec)


class AssignWatcher:
    """Namespace watch started before create; waits for named pod nodeName."""

    def __init__(self, api: client.CoreV1Api, ns: str, pod_name: str, timeout_s: float):
        self.api = api
        self.ns = ns
        self.pod_name = pod_name
        self.timeout_s = timeout_s
        self.events: list[dict[str, Any]] = []
        self.result: dict[str, Any] = {}
        self._error: Optional[str] = None
        self._done = threading.Event()
        self._ready = threading.Event()
        self._thread: Optional[threading.Thread] = None
        self._resource_version: Optional[str] = None
        self._watch_attempts = 0
        self._watch_errors: list[str] = []
        self._ready_mono: Optional[float] = None

    def start(self) -> None:
        # establish resourceVersion snapshot before create
        lst = self.api.list_namespaced_pod(self.ns, limit=1)
        self._resource_version = lst.metadata.resource_version
        self._thread = threading.Thread(target=self._run, name=f"watch-{self.pod_name}", daemon=True)
        self._thread.start()
        # wait until watch stream is established (or fail), not a fixed sleep
        if not self._ready.wait(timeout=5.0):
            self._watch_errors.append("watch_ready_timeout")

    def _run(self) -> None:
        deadline = time.monotonic() + self.timeout_s
        # one reconnect on transient watch failure before create+assign window ends
        while time.monotonic() < deadline and not self.result:
            self._watch_attempts += 1
            w = watch.Watch()
            try:
                stream = w.stream(
                    self.api.list_namespaced_pod,
                    namespace=self.ns,
                    resource_version=self._resource_version,
                    timeout_seconds=max(1, int(self.timeout_s)),
                )
                self._ready_mono = time.monotonic()
                self._ready.set()
                for ev in stream:
                    now_m = time.monotonic()
                    now_w = time.time()
                    if now_m >= deadline:
                        break
                    et = ev.get("type")
                    obj = ev.get("object")
                    if obj is None:
                        continue
                    # BOOKMARK / ERROR objects may lack metadata.name
                    meta = getattr(obj, "metadata", None)
                    name = getattr(meta, "name", None) if meta is not None else None
                    if name != self.pod_name:
                        continue
                    node = (obj.spec.node_name if obj.spec else None) or ""
                    entry = {
                        "type": et,
                        "mono": now_m,
                        "wall": now_w,
                        "uid": obj.metadata.uid,
                        "resourceVersion": obj.metadata.resource_version,
                        "nodeName": node or None,
                        "phase": (obj.status.phase if obj.status else None),
                    }
                    self.events.append(entry)
                    if node:
                        self.result = {
                            "nodeName": node,
                            "assign_mono": now_m,
                            "assign_wall": now_w,
                            "uid": obj.metadata.uid,
                            "resourceVersion": obj.metadata.resource_version,
                            "watch_event_type": et,
                        }
                        w.stop()
                        self._done.set()
                        return
                if not self.result:
                    self._error = "assign_timeout"
                break
            except Exception as e:
                self._watch_errors.append(str(e))
                self._error = str(e)
                # refresh RV and retry once if time remains
                try:
                    lst = self.api.list_namespaced_pod(self.ns, limit=1)
                    self._resource_version = lst.metadata.resource_version
                except Exception as e2:
                    self._watch_errors.append(f"rv_refresh:{e2}")
                    break
                if self._watch_attempts >= 2:
                    break
                time.sleep(0.05)
            finally:
                self._ready.set()
        self._done.set()

    def wait(self) -> dict[str, Any]:
        self._done.wait(timeout=self.timeout_s + 5)
        if self._thread:
            self._thread.join(timeout=2)
        out = dict(self.result)
        out["watch_events"] = self.events
        out["watch_resource_version_start"] = self._resource_version
        out["watch_ready_mono"] = self._ready_mono
        out["watch_attempts"] = self._watch_attempts
        out["watch_errors"] = self._watch_errors
        if self._error and not out.get("nodeName"):
            out["error"] = self._error
        return out


def logs_since(ns: str, name: str, since_s: int = 120) -> str:
    r = subprocess.run(
        ["kubectl", "-n", ns, "logs", name, f"--since={since_s}s"],
        text=True,
        capture_output=True,
    )
    return r.stdout or r.stderr or ""


def excerpt_for_pod(log_text: str, pod_name: str, max_lines: int = 40) -> list[str]:
    lines = [ln for ln in log_text.splitlines() if pod_name in ln]
    return lines[-max_lines:]


def catalog_access_excerpt(since_s: int = 120) -> list[str]:
    r = subprocess.run(
        [
            "kubectl",
            "-n",
            "kcp-control-plane",
            "logs",
            "deploy/catalog-api",
            f"--since={since_s}s",
        ],
        text=True,
        capture_output=True,
    )
    text = r.stdout or ""
    keep = []
    for ln in text.splitlines():
        if any(x in ln for x in ("/v1/profile", "/v1/env_lookup", "/v1/placement_decision", "GET ", "POST ")):
            if "/health" in ln:
                continue
            keep.append(ln)
    return keep[-80:]


def summarize(rows: list[dict[str, Any]], key: str = "submit_to_assign_watch_ms") -> dict[str, Any]:
    ok = [r for r in rows if r.get(key) is not None and not r.get("error") and not r.get("warmup")]
    xs = sorted(float(r[key]) for r in ok)

    def pct(q: float) -> Optional[float]:
        if not xs:
            return None
        i = min(int(q * (len(xs) - 1)), len(xs) - 1)
        return round(xs[i], 3)

    return {
        "n_ok": len(ok),
        "n_total_measured": len(ok),
        "n_rows_incl_warmup_errors": len(rows),
        "p50_ms": pct(0.50),
        "p95_ms": pct(0.95),
        "p99_ms": pct(0.99),
        "mean_ms": round(sum(xs) / len(xs), 3) if xs else None,
        "min_ms": xs[0] if xs else None,
        "max_ms": xs[-1] if xs else None,
        "metric": key,
    }


def run_trial(
    api: client.CoreV1Api,
    *,
    out_dir: Path,
    ns: str,
    arm: str,
    scheduler_name: str,
    use_gpu: bool,
    experiment_id: str,
    block: int,
    trial: int,
    warmup: bool,
    timeout_s: float,
    mode_now: str,
) -> dict[str, Any]:
    name = f"q2v2-{arm.lower()}-b{block}-t{trial:02d}-{uuid.uuid4().hex[:6]}"
    body = make_pod_body(
        name,
        ns,
        scheduler_name,
        use_gpu=use_gpu,
        experiment_id=experiment_id,
        arm=arm,
        block=block,
        trial=trial,
        warmup=warmup,
    )
    watcher = AssignWatcher(api, ns, name, timeout_s)
    watcher.start()

    t_submit = time.monotonic()
    t_submit_wall = time.time()
    create_err = None
    created = None
    try:
        created = api.create_namespaced_pod(ns, body)
    except ApiException as e:
        create_err = f"{e.status}: {e.body}"
    t_create = time.monotonic()
    t_create_wall = time.time()

    row: dict[str, Any] = {
        "arm": arm,
        "block": block,
        "trial": trial,
        "warmup": warmup,
        "pod": name,
        "schedulerName": scheduler_name or "(default)",
        "SCHED_CATALOG_MODE": mode_now,
        "use_gpu": use_gpu,
        "t_submit_send_mono": t_submit,
        "t_submit_send_wall": t_submit_wall,
        "t_create_resp_mono": t_create,
        "t_create_resp_wall": t_create_wall,
        "create_resp_ms": round((t_create - t_submit) * 1000.0, 3),
        "utc": utc(),
    }
    if create_err:
        row["error"] = "create_failed"
        row["create_error"] = create_err
        watcher.wait()
        return row

    if created is not None:
        row["created_uid"] = created.metadata.uid
        row["created_resourceVersion"] = created.metadata.resource_version

    assign = watcher.wait()
    row["watch"] = assign
    if assign.get("nodeName"):
        row["nodeName"] = assign["nodeName"]
        row["t_watch_assigned_mono"] = assign["assign_mono"]
        row["t_watch_assigned_wall"] = assign["assign_wall"]
        row["submit_to_assign_watch_ms"] = round((assign["assign_mono"] - t_submit) * 1000.0, 3)
        row["create_to_assign_watch_ms"] = round((assign["assign_mono"] - t_create) * 1000.0, 3)
    else:
        row["error"] = assign.get("error") or "assign_timeout"
        row["submit_to_assign_watch_ms"] = None

    # scheduler / catalog log excerpts for this pod
    if scheduler_name == "keti-cost-scheduler":
        snap = sched_pod_snapshot()
        row["scheduler_pod"] = snap["pods"][0] if snap["pods"] else None
        if snap["pods"]:
            slog = logs_since("kcp-control-plane", snap["pods"][0]["name"], since_s=90)
            row["scheduler_log_excerpt"] = excerpt_for_pod(slog, name)
            # parse internal elapsed if present
            el = []
            for ln in row["scheduler_log_excerpt"]:
                m = re.search(r"elapsed=([0-9.]+)(ms|µs|us|s)", ln)
                if m:
                    val = float(m.group(1))
                    unit = m.group(2)
                    if unit == "s":
                        val *= 1000
                    elif unit in ("µs", "us"):
                        val /= 1000
                    el.append(val)
            row["scheduler_log_elapsed_ms"] = el
        if arm.upper() == "B1" or mode_now == "shadow":
            # Snapshot catalog access log lines whose wall timestamp falls near this trial.
            cat = catalog_access_excerpt(since_s=120)
            # Keep lines with HTTP path of interest; correlate by time window around create/assign.
            t0 = t_submit_wall - 0.05
            t1 = (assign.get("assign_wall") or t_create_wall) + 0.5
            near = []
            for ln in cat:
                # catalog-api format lacks iso time in some builds; keep all path hits in since-window
                if any(p in ln for p in ("/v1/profile", "/v1/env_lookup", "/v1/placement_decision")):
                    near.append(ln)
            row["catalog_api_lines"] = near[-20:]
            row["catalog_api_counts"] = {
                "profile": sum(1 for ln in near if "/v1/profile" in ln),
                "env_lookup": sum(1 for ln in near if "/v1/env_lookup" in ln),
                "placement_decision": sum(1 for ln in near if "/v1/placement_decision" in ln),
                "note": "since-window aggregate (not strictly per-pod); cache hits skip GET",
                "trial_wall_window": [t0, t1],
            }
    else:
        # default-scheduler: kube-system static pod logs if available
        r = subprocess.run(
            ["kubectl", "-n", "kube-system", "logs", "kube-scheduler-" + os.environ.get("CONTROL_PLANE_NODE", "control-plane"), f"--since=90s"],
            text=True,
            capture_output=True,
        )
        row["scheduler_log_excerpt"] = excerpt_for_pod(r.stdout or "", name)

    # delete trial pod
    try:
        api.delete_namespaced_pod(name, ns, grace_period_seconds=0)
    except ApiException:
        pass
    # wait gone
    gone_deadline = time.monotonic() + 30
    while time.monotonic() < gone_deadline:
        try:
            api.read_namespaced_pod(name, ns)
            time.sleep(0.1)
        except ApiException as e:
            if e.status == 404:
                break
            time.sleep(0.1)
    write_json(out_dir / f"block{block}_{'wu' if warmup else 'm'}_t{trial:02d}.json", row)
    return row


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out-root", required=True)
    ap.add_argument("--experiment-id", default="Q2_SCHED_LATENCY_V2_20261026")
    ap.add_argument("--namespace", default="kcp-workload")
    ap.add_argument("--warmup", type=int, default=2, help="warmup trials per arm per block (excluded)")
    ap.add_argument("--measured", type=int, default=8, help="measured trials per arm per block")
    ap.add_argument("--blocks", type=int, default=3)
    ap.add_argument("--timeout-s", type=float, default=45.0)
    ap.add_argument("--no-gpu", action="store_true")
    ap.add_argument("--seed", type=int, default=20261026)
    ap.add_argument("--allow-mode-switch", action="store_true")
    args = ap.parse_args()

    out = Path(args.out_root)
    out.mkdir(parents=True, exist_ok=True)
    use_gpu = not args.no_gpu
    rng = random.Random(args.seed)
    api = load_api()

    original = sched_deploy_env()
    write_json(
        out / "preflight.json",
        {
            "utc": utc(),
            "original": original,
            "sched_pods": sched_pod_snapshot(),
            "use_gpu": use_gpu,
            "warmup": args.warmup,
            "measured": args.measured,
            "blocks": args.blocks,
            "seed": args.seed,
            "metric_primary": "submit_to_assign_watch_ms",
            "definition": "mono: pods.create send → first watch event with non-empty spec.nodeName",
            "binder_note": "DefaultBinder V(2) string present in image; process args have no -v=2 so V(2) inactive; use INFO schedule elapsed + watch",
        },
    )

    # arm configs depend on mode; we switch mode when arm needs it
    arm_sched = {
        "A": "default-scheduler",
        "B0": "keti-cost-scheduler",
        "B1": "keti-cost-scheduler",
    }
    arm_mode = {"A": None, "B0": "off", "B1": "shadow"}  # None = leave as-is for A

    all_rows: list[dict[str, Any]] = []
    mode_events: list[dict[str, Any]] = []
    current_mode = original["SCHED_CATALOG_MODE"]

    try:
        for b in range(1, args.blocks + 1):
            order = ["A", "B0", "B1"]
            rng.shuffle(order)
            block_meta = {"block": b, "order": list(order), "utc": utc()}
            write_json(out / f"block{b}_order.json", block_meta)
            print(f"BLOCK {b} order={order}", flush=True)

            for arm in order:
                want_mode = arm_mode[arm]
                if want_mode is not None:
                    if not args.allow_mode_switch:
                        raise SystemExit("B0/B1 require --allow-mode-switch")
                    if current_mode != want_mode:
                        print(f"MODE {current_mode} -> {want_mode} (arm={arm})", flush=True)
                        ev = set_sched_mode(want_mode)
                        mode_events.append(ev)
                        write_json(out / f"mode_switch_{len(mode_events):02d}_{want_mode}.json", ev)
                        current_mode = want_mode

                mode_now = sched_deploy_env()["SCHED_CATALOG_MODE"]
                sched_name = arm_sched[arm]
                arm_dir = out / f"block{b}" / arm
                arm_dir.mkdir(parents=True, exist_ok=True)

                for phase, n, is_wu in (("warmup", args.warmup, True), ("measured", args.measured, False)):
                    for i in range(1, n + 1):
                        print(
                            f"RUN block={b} arm={arm} {phase} i={i}/{n} sched={sched_name} mode={mode_now}",
                            flush=True,
                        )
                        row = run_trial(
                            api,
                            out_dir=arm_dir,
                            ns=args.namespace,
                            arm=arm,
                            scheduler_name=sched_name,
                            use_gpu=use_gpu,
                            experiment_id=args.experiment_id,
                            block=b,
                            trial=i,
                            warmup=is_wu,
                            timeout_s=args.timeout_s,
                            mode_now=mode_now,
                        )
                        all_rows.append(row)
                        print(
                            json.dumps(
                                {
                                    "pod": row["pod"],
                                    "warmup": is_wu,
                                    "ms": row.get("submit_to_assign_watch_ms"),
                                    "create_ms": row.get("create_resp_ms"),
                                    "err": row.get("error"),
                                    "node": row.get("nodeName"),
                                },
                                ensure_ascii=False,
                            ),
                            flush=True,
                        )
    finally:
        if args.allow_mode_switch and current_mode != original["SCHED_CATALOG_MODE"]:
            print(f"RESTORE mode -> {original['SCHED_CATALOG_MODE']}", flush=True)
            try:
                ev = set_sched_mode(original["SCHED_CATALOG_MODE"])
                mode_events.append(ev)
                write_json(out / "mode_restore.json", ev)
            except Exception as e:
                write_json(out / "restore_error.json", {"error": str(e)})
                print(f"RESTORE_FAILED: {e}", flush=True)

    write_json(out / "all_runs.json", all_rows)
    write_json(out / "mode_events.json", mode_events)

    by_arm: dict[str, Any] = {}
    for arm in ("A", "B0", "B1"):
        rows = [r for r in all_rows if r.get("arm") == arm]
        by_arm[arm] = {
            "submit_to_assign_watch": summarize(rows, "submit_to_assign_watch_ms"),
            "create_to_assign_watch": summarize(rows, "create_to_assign_watch_ms"),
            "create_resp": summarize(rows, "create_resp_ms"),
        }
    write_json(out / "summary_by_arm.json", by_arm)

    # per-block measured only
    by_block = {}
    for b in range(1, args.blocks + 1):
        by_block[str(b)] = {}
        for arm in ("A", "B0", "B1"):
            rows = [r for r in all_rows if r.get("arm") == arm and r.get("block") == b]
            by_block[str(b)][arm] = summarize(rows, "submit_to_assign_watch_ms")
    write_json(out / "summary_by_block.json", by_block)

    write_json(
        out / "postflight.json",
        {
            "utc": utc(),
            "env": sched_deploy_env(),
            "sched_pods": sched_pod_snapshot(),
            "leftover_q2": sh(
                [
                    "kubectl",
                    "-n",
                    args.namespace,
                    "get",
                    "pods",
                    "-l",
                    "app=q2-schedule-latency",
                    "--no-headers",
                ],
                check=False,
            ).strip(),
        },
    )
    print("Q2V2_DONE", json.dumps(by_arm, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
