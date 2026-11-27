#!/usr/bin/env python3
"""Q2 scheduling latency: submit → first observed non-empty spec.nodeName (monotonic ms).

A  = schedulerName default-scheduler (or empty → default)
B0 = keti-cost-scheduler while SCHED_CATALOG_MODE=off
B1 = keti-cost-scheduler while SCHED_CATALOG_MODE=shadow

Does not use creationTimestamp/lastTransitionTime for ms math.
Does not kill foreign workloads. Prefer --no-gpu when host GPU is occupied outside k8s.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


def utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def sh(args: list[str], check: bool = True) -> str:
    r = subprocess.run(args, text=True, capture_output=True)
    if check and r.returncode:
        raise RuntimeError(f"{args}: {r.stderr[-2000:]}")
    return r.stdout


def write_json(path: Path, obj: Any) -> None:
    path.write_text(json.dumps(obj, ensure_ascii=False, indent=2), encoding="utf-8")


def pod_manifest(
    name: str,
    ns: str,
    scheduler_name: str,
    *,
    use_gpu: bool,
    experiment_id: str,
    arm: str,
) -> str:
    sched_line = f"  schedulerName: {scheduler_name}\n" if scheduler_name else ""
    if use_gpu:
        rc = "  runtimeClassName: nvidia\n"
        resources = """    resources:
      requests:
        cpu: "100m"
        memory: 256Mi
        nvidia.com/gpu: "1"
      limits:
        cpu: "500m"
        memory: 512Mi
        nvidia.com/gpu: "1"
"""
    else:
        rc = ""
        resources = """    resources:
      requests:
        cpu: "50m"
        memory: 64Mi
      limits:
        cpu: "200m"
        memory: 128Mi
"""
    return f"""apiVersion: v1
kind: Pod
metadata:
  name: {name}
  namespace: {ns}
  labels:
    app: q2-schedule-latency
    experiment.kcloud.io/id: "{experiment_id}"
    experiment.kcloud.io/arm: "{arm}"
spec:
{sched_line}{rc}  restartPolicy: Never
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.9
    imagePullPolicy: IfNotPresent
{resources}"""


def wait_node_name(ns: str, name: str, timeout_s: float) -> tuple[Optional[str], float, dict[str, Any]]:
    """Poll get pod; return (nodeName, submit_to_assign_ms, meta).

    t0 is set by caller immediately before kubectl create. Here we only observe.
    """
    meta: dict[str, Any] = {"polls": [], "watch_note": "kubectl_get_poll_not_watch"}
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        t_poll = time.monotonic()
        try:
            raw = sh(
                [
                    "kubectl",
                    "-n",
                    ns,
                    "get",
                    "pod",
                    name,
                    "-o",
                    "json",
                ],
                check=False,
            )
            if not raw.strip():
                time.sleep(0.05)
                continue
            pod = json.loads(raw)
        except Exception as e:
            meta["polls"].append({"err": str(e), "t": t_poll})
            time.sleep(0.05)
            continue
        node = (pod.get("spec") or {}).get("nodeName") or ""
        phase = (pod.get("status") or {}).get("phase")
        uid = (pod.get("metadata") or {}).get("uid")
        rv = (pod.get("metadata") or {}).get("resourceVersion")
        meta["uid"] = uid
        meta["resourceVersion"] = rv
        meta["phase"] = phase
        meta["polls"].append({"t_mono": t_poll, "nodeName": node or None, "phase": phase})
        if node:
            return node, t_poll, meta
        time.sleep(0.05)
    return None, time.monotonic(), meta


def delete_pod(ns: str, name: str) -> None:
    sh(["kubectl", "-n", ns, "delete", "pod", name, "--wait=true", "--timeout=60s"], check=False)


def get_sched_mode() -> str:
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
    for e in d["spec"]["template"]["spec"]["containers"][0].get("env") or []:
        if e.get("name") == "SCHED_CATALOG_MODE":
            return str(e.get("value") or "")
    return ""


def set_sched_mode(mode: str) -> None:
    # strategic merge patch env value
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
    # verify
    got = get_sched_mode()
    if got != mode:
        raise RuntimeError(f"mode verify failed want={mode} got={got}")


def run_one(
    out_dir: Path,
    arm: str,
    scheduler_name: str,
    *,
    use_gpu: bool,
    ns: str,
    experiment_id: str,
    timeout_s: float,
    idx: int,
) -> dict[str, Any]:
    # RFC 1123 subdomain: lowercase only (arm labels like A/B0 must be lowered).
    name = f"q2-{arm.lower()}-{idx:02d}-{uuid.uuid4().hex[:6]}"
    man = out_dir / f"{name}.yaml"
    man.write_text(
        pod_manifest(name, ns, scheduler_name, use_gpu=use_gpu, experiment_id=experiment_id, arm=arm),
        encoding="utf-8",
    )
    t_submit = time.monotonic()
    t_submit_wall = time.time()
    create = subprocess.run(
        ["kubectl", "create", "-f", str(man)],
        text=True,
        capture_output=True,
    )
    t_create_resp = time.monotonic()
    row: dict[str, Any] = {
        "arm": arm,
        "pod": name,
        "schedulerName": scheduler_name or "(default)",
        "use_gpu": use_gpu,
        "submit_wall_epoch": t_submit_wall,
        "submit_mono": t_submit,
        "create_resp_mono": t_create_resp,
        "create_rc": create.returncode,
        "create_stderr": create.stderr[-500:],
        "utc": utc(),
    }
    if create.returncode != 0:
        row["error"] = "create_failed"
        row["submit_to_assignment_observed_ms"] = None
        return row

    node, t_assign, meta = wait_node_name(ns, name, timeout_s)
    row["assign_mono"] = t_assign
    row["nodeName"] = node
    row["submit_to_assignment_observed_ms"] = round((t_assign - t_submit) * 1000.0, 3)
    row["create_resp_ms"] = round((t_create_resp - t_submit) * 1000.0, 3)
    row["meta"] = meta
    if not node:
        row["error"] = "assign_timeout"
    # always delete this trial pod
    delete_pod(ns, name)
    # confirm gone
    time.sleep(0.2)
    return row


def summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    ok = [r for r in rows if r.get("submit_to_assignment_observed_ms") is not None and not r.get("error")]
    xs = sorted(r["submit_to_assignment_observed_ms"] for r in ok)

    def pct(q: float) -> Optional[float]:
        if not xs:
            return None
        i = min(int(q * len(xs)), len(xs) - 1)
        return xs[i]

    return {
        "n_ok": len(ok),
        "n_total": len(rows),
        "p50_ms": pct(0.50),
        "p95_ms": pct(0.95),
        "p99_ms": pct(0.99),
        "mean_ms": round(sum(xs) / len(xs), 3) if xs else None,
        "min_ms": xs[0] if xs else None,
        "max_ms": xs[-1] if xs else None,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out-root", required=True)
    ap.add_argument("--experiment-id", default="Q2_SCHED_LATENCY_20261026")
    ap.add_argument("--namespace", default="kcp-workload")
    ap.add_argument("--repeats", type=int, default=10)
    ap.add_argument("--timeout-s", type=float, default=60.0)
    ap.add_argument("--no-gpu", action="store_true", help="CPU-only pause pods (safe under foreign GPU load)")
    ap.add_argument("--arms", default="A,B1", help="comma list among A,B0,B1")
    ap.add_argument(
        "--allow-mode-switch",
        action="store_true",
        help="For B0: patch SCHED_CATALOG_MODE off then restore original",
    )
    args = ap.parse_args()

    out = Path(args.out_root)
    out.mkdir(parents=True, exist_ok=True)
    use_gpu = not args.no_gpu
    arms = [a.strip() for a in args.arms.split(",") if a.strip()]

    original_mode = get_sched_mode()
    write_json(
        out / "preflight.json",
        {
            "utc": utc(),
            "original_SCHED_CATALOG_MODE": original_mode,
            "use_gpu": use_gpu,
            "arms": arms,
            "metric": "submit_to_assignment_observed_ms",
            "definition": "monotonic: kubectl create send → first poll with non-empty spec.nodeName",
        },
    )

    all_rows: list[dict[str, Any]] = []
    try:
        for arm in arms:
            if arm == "A":
                sched = "default-scheduler"
                # no mode change
            elif arm == "B0":
                sched = "keti-cost-scheduler"
                if not args.allow_mode_switch:
                    raise SystemExit("B0 requires --allow-mode-switch to set SCHED_CATALOG_MODE=off")
                set_sched_mode("off")
            elif arm == "B1":
                sched = "keti-cost-scheduler"
                if args.allow_mode_switch and get_sched_mode() != "shadow":
                    set_sched_mode("shadow")
            else:
                raise SystemExit(f"unknown arm {arm}")

            mode_now = get_sched_mode()
            arm_dir = out / arm
            arm_dir.mkdir(exist_ok=True)
            rows = []
            for i in range(1, args.repeats + 1):
                print(f"RUN arm={arm} i={i}/{args.repeats} sched={sched} mode={mode_now}", flush=True)
                row = run_one(
                    arm_dir,
                    arm,
                    sched,
                    use_gpu=use_gpu,
                    ns=args.namespace,
                    experiment_id=args.experiment_id,
                    timeout_s=args.timeout_s,
                    idx=i,
                )
                row["SCHED_CATALOG_MODE"] = mode_now
                rows.append(row)
                all_rows.append(row)
                write_json(arm_dir / f"run-{i:02d}.json", row)
            summ = summarize(rows)
            write_json(arm_dir / "summary.json", summ)
            print(json.dumps({"arm": arm, **summ}, ensure_ascii=False), flush=True)
    finally:
        # restore original mode if we switched
        try:
            if args.allow_mode_switch and get_sched_mode() != original_mode:
                print(f"RESTORE SCHED_CATALOG_MODE={original_mode}", flush=True)
                set_sched_mode(original_mode)
        except Exception as e:
            write_json(out / "restore_error.json", {"error": str(e), "wanted": original_mode})
            print(f"RESTORE_FAILED: {e}", flush=True)

    write_json(out / "all_runs.json", all_rows)
    by_arm = {}
    for arm in arms:
        arm_rows = [r for r in all_rows if r.get("arm") == arm]
        by_arm[arm] = summarize(arm_rows)
    write_json(out / "summary_by_arm.json", by_arm)
    write_json(
        out / "postflight.json",
        {"utc": utc(), "SCHED_CATALOG_MODE": get_sched_mode(), "original": original_mode},
    )
    print("Q2_DONE", json.dumps(by_arm, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
