#!/usr/bin/env python3
"""Catalog env lookup helpers — exact / similar / not_found (no cross-env reuse)."""

from __future__ import annotations

from statistics import median
from typing import Any, Optional

from catalog.workload_context import (
    CONFIDENCE_FALLBACK,
    CONFIDENCE_LOW,
    CONFIDENCE_MEASURED,
    MATCH_EXACT,
    MATCH_NOT_FOUND,
    MATCH_SIMILAR,
    build_catalog_lookup_key,
)


def _aggregate_runs(runs: list[dict[str, Any]]) -> dict[str, Any]:
    p99s = [float(r["latency_p99_ms"]) for r in runs if r.get("latency_p99_ms") is not None]
    # Device net (historical field). Keep separate from server total.
    energies = [float(r["net_energy_j"]) for r in runs if r.get("net_energy_j") is not None]
    server_energies = [
        float(r["server_energy_j"]) for r in runs if r.get("server_energy_j") is not None
    ]
    durs = [float(r["duration_s"]) for r in runs if r.get("duration_s") is not None]
    sla_vals = [r.get("sla_pass") for r in runs]
    # Unevaluated → sla_pass stays null (never treat as PASS).
    if not sla_vals or any(v is None for v in sla_vals):
        sla_pass: Optional[bool] = None
    else:
        sla_pass = all(bool(v) for v in sla_vals)

    return {
        "predicted_p99_ms": median(p99s) if p99s else None,
        "predicted_net_energy_j": median(energies) if energies else None,
        "predicted_server_energy_j": median(server_energies) if server_energies else None,
        "predicted_duration_s": median(durs) if durs else None,
        "sla_pass": sla_pass,
        "run_count": len(runs),
        "runs": [
            {
                "run_uuid": r.get("run_uuid"),
                "run_id": r.get("run_id"),
                "repeat": r.get("repeat_idx"),
                "latency_p99_ms": r.get("latency_p99_ms"),
                "sla_pass": None if r.get("sla_pass") is None else bool(r.get("sla_pass")),
                "net_energy_j": r.get("net_energy_j"),
                "server_energy_j": r.get("server_energy_j"),
            }
            for r in runs
        ],
    }


def lookup_environment(
    repo: Any,
    *,
    environment_key: str,
    concurrency: int,
) -> dict[str, Any]:
    """Exact env+concurrency only for MEASURED use.

    - exact: same environment_key AND concurrency
    - similar: same environment_key, different concurrency only → LOW, difference recorded;
      NEVER substitutes another util/LSU/KV/engines environment
    - not_found: profile_not_found; sla_pass=null
    """
    lookup_key = build_catalog_lookup_key(environment_key, concurrency)
    base = {
        "environment_key": environment_key,
        "concurrency": concurrency,
        "catalog_lookup_key": lookup_key,
        "scheduling_enforced": False,
    }

    exact = repo.list_runs(environment_key=environment_key, concurrency=concurrency)
    if exact:
        agg = _aggregate_runs(exact)
        return {
            **base,
            **agg,
            "found": True,
            "match_type": MATCH_EXACT,
            "confidence": CONFIDENCE_MEASURED,
            "difference": None,
            "reason": "exact_environment_and_concurrency",
        }

    same_env = repo.list_runs(environment_key=environment_key)
    if same_env:
        other_c = sorted({int(r["concurrency"]) for r in same_env if r.get("concurrency") is not None})
        agg = _aggregate_runs(same_env)
        # Similar must NOT claim SLA PASS for the requested concurrency.
        return {
            **base,
            "found": False,
            "match_type": MATCH_SIMILAR,
            "confidence": CONFIDENCE_LOW,
            "predicted_p99_ms": None,
            "predicted_net_energy_j": None,
            "predicted_server_energy_j": None,
            "predicted_duration_s": None,
            "sla_pass": None,
            "run_count": len(same_env),
            "runs": agg["runs"],
            "difference": {
                "field": "concurrency",
                "requested": concurrency,
                "available": other_c,
                "note": "same environment only; predictions withheld for Score MEASURED path",
            },
            "reason": "same_environment_different_concurrency",
        }

    return {
        **base,
        "found": False,
        "match_type": MATCH_NOT_FOUND,
        "confidence": CONFIDENCE_FALLBACK,
        "predicted_p99_ms": None,
        "predicted_net_energy_j": None,
        "predicted_server_energy_j": None,
        "predicted_duration_s": None,
        "sla_pass": None,
        "run_count": 0,
        "runs": [],
        "difference": None,
        "reason": MATCH_NOT_FOUND,
    }
