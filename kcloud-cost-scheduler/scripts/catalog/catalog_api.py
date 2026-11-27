#!/usr/bin/env python3
"""HTTP Catalog API — Scheduler queries this; never opens SQLite directly."""

from __future__ import annotations

import argparse
import json
import sys
import uuid
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlparse

_HERE = Path(__file__).resolve().parent
if str(_HERE) not in sys.path:
    sys.path.insert(0, str(_HERE))
if str(_HERE.parent) not in sys.path:
    sys.path.insert(0, str(_HERE.parent))

try:
    from repository import open_catalog
except ImportError:
    from catalog.repository import open_catalog

try:
    from env_lookup import lookup_environment
except ImportError:
    from catalog.env_lookup import lookup_environment


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


class CatalogHandler(BaseHTTPRequestHandler):
    repo = None  # set in main

    def _json(self, code: int, body: Any) -> None:
        raw = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def _read_json(self) -> dict:
        n = int(self.headers.get("Content-Length", "0"))
        if n <= 0:
            return {}
        return json.loads(self.rfile.read(n).decode("utf-8"))

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        path = parsed.path
        qs = parse_qs(parsed.query)

        if path == "/health":
            self._json(200, {
                "status": "ok",
                "runs_total": self.repo.count_runs(),
                "runs_measured": self.repo.count_runs("measured"),
                "runs_legacy": self.repo.count_runs("legacy_import"),
                "ts": _now(),
            })
            return

        if path == "/v1/profile":
            key = (qs.get("profile_key") or [""])[0]
            if not key:
                self._json(400, {"error": "profile_key required"})
                return
            row = self.repo.get_reference_profile(key)
            if not row:
                self._json(200, {
                    "profile_key": key,
                    "found": False,
                    "confidence": "FALLBACK",
                    "predicted_p99_ms": None,
                    "predicted_duration_s": None,
                    "predicted_net_energy_j": None,
                })
                return
            self._json(200, {
                "found": True,
                "profile_key": row["profile_key"],
                "confidence": row.get("confidence") or "REFERENCE",
                "predicted_p99_ms": row.get("predicted_p99_ms"),
                "predicted_duration_s": row.get("predicted_duration_s"),
                "predicted_net_energy_j": row.get("predicted_net_energy_j"),
                "source": row.get("source"),
                "model": row.get("model"),
                "runtime": row.get("runtime"),
                "accelerator": row.get("accelerator"),
            })
            return

        if path.startswith("/v1/run/"):
            run_uuid = path[len("/v1/run/"):]
            row = self.repo.get_run(run_uuid)
            if not row:
                self._json(404, {"error": "not found"})
                return
            self._json(200, row)
            return

        if path == "/v1/policy":
            policy_key = (qs.get("policy_key") or [""])[0]
            env_key = (qs.get("environment_key") or [""])[0]
            if policy_key:
                row = self.repo.get_policy_bound(policy_key)
            elif env_key:
                row = self.repo.get_policy_by_environment(env_key)
            else:
                self._json(400, {"error": "policy_key or environment_key required"})
                return
            if not row:
                self._json(200, {
                    "found": False,
                    "policy_key": policy_key or None,
                    "environment_key": env_key or None,
                    "concurrency_cap": None,
                    "enforce_enabled": False,
                })
                return
            self._json(200, {
                "found": True,
                "policy_key": row["policy_key"],
                "environment_key": row["environment_key"],
                "concurrency_cap": row["concurrency_cap"],
                "sla_p99_ms": row.get("sla_p99_ms"),
                "source": row.get("source"),
                "confidence": row.get("confidence"),
                "cohort": row.get("cohort"),
                "model": row.get("model"),
                "runtime": row.get("runtime"),
                "protocol": row.get("protocol"),
                "enforce_enabled": bool(row.get("enforce_enabled")),
                "notes": row.get("notes"),
                "evidence_json": row.get("evidence_json"),
                "adopted_at": row.get("adopted_at"),
            })
            return

        if path == "/v1/lookup":
            # Measured runs + adopted policy. Catalog read ≠ scheduler enforce.
            env_key = (qs.get("environment_key") or [""])[0]
            cohort = (qs.get("cohort") or [""])[0]
            conc_raw = (qs.get("concurrency") or [""])[0]
            if not env_key and not cohort:
                self._json(400, {"error": "environment_key or cohort required"})
                return
            conc = int(conc_raw) if conc_raw else None
            policy = self.repo.get_policy_by_environment(env_key) if env_key else None
            use_cohort = cohort or (policy.get("cohort") if policy else None)
            runs = self.repo.list_runs(cohort=use_cohort, concurrency=conc) if use_cohort else []
            profiles = [
                {
                    "concurrency": r.get("concurrency"),
                    "repeat": r.get("repeat_idx"),
                    "latency_p99_ms": r.get("latency_p99_ms"),
                    "sla_pass": bool(r.get("sla_pass")),
                    "run_uuid": r.get("run_uuid"),
                    "run_id": r.get("run_id"),
                }
                for r in runs
            ]
            self._json(200, {
                "found": bool(runs) or bool(policy),
                "environment_key": env_key or None,
                "cohort": use_cohort,
                "measured_runs": profiles,
                "policy": {
                    "found": bool(policy),
                    "concurrency_cap": policy.get("concurrency_cap") if policy else None,
                    "enforce_enabled": bool(policy.get("enforce_enabled")) if policy else False,
                    "source": policy.get("source") if policy else None,
                    "policy_key": policy.get("policy_key") if policy else None,
                    "note": "lookup only; enforce_enabled=false means not a hard scheduling limit",
                },
                "scheduling_enforced": False,
            })
            return

        if path == "/v1/env_lookup":
            # Score path: exact environment_key + concurrency. Never cross-env reuse.
            env_key = (qs.get("environment_key") or [""])[0]
            conc_raw = (qs.get("concurrency") or [""])[0]
            if not env_key:
                self._json(400, {"error": "environment_key required"})
                return
            if not conc_raw:
                self._json(400, {"error": "concurrency required"})
                return
            try:
                conc = int(conc_raw)
            except ValueError:
                self._json(400, {"error": "concurrency must be int"})
                return
            if conc < 1:
                self._json(400, {"error": "concurrency must be >= 1"})
                return
            body = lookup_environment(
                self.repo, environment_key=env_key, concurrency=conc
            )
            # Score-facing alias fields (catalogclient ProfilePrediction shape).
            body["profile_key"] = body.get("catalog_lookup_key")
            self._json(200, body)
            return

        self._json(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path == "/v1/placement_decision":
            body = self._read_json()
            body.setdefault("decision_id", str(uuid.uuid4()))
            body.setdefault("evaluated_at", _now())
            body.setdefault("mode", body.get("mode") or "shadow")
            self.repo.insert_placement_decision(body)
            self._json(201, {"decision_id": body["decision_id"]})
            return

        if parsed.path.startswith("/v1/placement_decision/") and parsed.path.endswith("/outcome"):
            decision_id = parsed.path[len("/v1/placement_decision/"):-len("/outcome")]
            body = self._read_json()
            self.repo.record_decision_outcome(
                decision_id,
                run_uuid=body.get("run_uuid"),
                selected_actual=body.get("selected_actual"),
                regret_json=body.get("regret_json") or {},
            )
            self._json(200, {"decision_id": decision_id})
            return

        if parsed.path == "/v1/backup":
            body = self._read_json()
            dest = Path(body.get("dest") or "/tmp/kcloud-catalog-backup.db")
            out = self.repo.backup(dest)
            self._json(200, {"backup": str(out)})
            return

        self._json(404, {"error": "not found"})

    def log_message(self, fmt: str, *args) -> None:
        print(f"[catalog-api] {self.address_string()} {fmt % args}")


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--db", default="/var/lib/kcloud/catalog/kcloud.db")
    p.add_argument("--host", default="127.0.0.1")
    p.add_argument("--port", type=int, default=8099)
    args = p.parse_args()
    CatalogHandler.repo = open_catalog(Path(args.db))
    server = ThreadingHTTPServer((args.host, args.port), CatalogHandler)
    print(f"catalog API listening on http://{args.host}:{args.port} db={args.db}")
    server.serve_forever()


if __name__ == "__main__":
    main()
