#!/usr/bin/env python3
"""CatalogRepository interface + SQLite adapter."""

from __future__ import annotations

import json
import sqlite3
from abc import ABC, abstractmethod
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional


SCHEMA_PATH = Path(__file__).resolve().parent / "schema.sql"


class CatalogRepository(ABC):
    @abstractmethod
    def init_schema(self) -> None: ...

    @abstractmethod
    def upsert_reference_profile(self, row: dict[str, Any]) -> None: ...

    @abstractmethod
    def get_reference_profile(self, profile_key: str) -> Optional[dict[str, Any]]: ...

    @abstractmethod
    def insert_workload_run(self, row: dict[str, Any]) -> None: ...

    @abstractmethod
    def insert_request_samples(self, rows: list[dict[str, Any]]) -> None: ...

    @abstractmethod
    def insert_accelerator_samples(self, rows: list[dict[str, Any]]) -> None: ...

    @abstractmethod
    def upsert_interference(self, row: dict[str, Any]) -> None: ...

    @abstractmethod
    def insert_placement_decision(self, row: dict[str, Any]) -> None: ...

    @abstractmethod
    def get_run(self, run_uuid: str) -> Optional[dict[str, Any]]: ...

    @abstractmethod
    def get_run_by_trace(self, trace_id: str) -> Optional[dict[str, Any]]: ...

    @abstractmethod
    def list_unjoined_decisions(self) -> list[dict[str, Any]]: ...

    @abstractmethod
    def record_decision_outcome(
        self,
        decision_id: str,
        *,
        run_uuid: Optional[str],
        selected_actual: Optional[str],
        regret_json: dict[str, Any],
    ) -> None: ...

    @abstractmethod
    def count_runs(self, source: Optional[str] = None) -> int: ...

    @abstractmethod
    def upsert_policy_bound(self, row: dict[str, Any]) -> None: ...

    @abstractmethod
    def get_policy_bound(self, policy_key: str) -> Optional[dict[str, Any]]: ...

    @abstractmethod
    def get_policy_by_environment(self, environment_key: str) -> Optional[dict[str, Any]]: ...

    @abstractmethod
    def list_runs(
        self,
        *,
        cohort: Optional[str] = None,
        concurrency: Optional[int] = None,
        experiment_id: Optional[str] = None,
        environment_key: Optional[str] = None,
    ) -> list[dict[str, Any]]: ...

    @abstractmethod
    def backup(self, dest: Path) -> Path: ...


class SQLiteCatalogRepository(CatalogRepository):
    def __init__(self, db_path: Path):
        self.db_path = Path(db_path)
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._conn = sqlite3.connect(str(self.db_path), check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        # The API serves reads while ingest writes from another process, so the
        # bootstrap DB needs WAL plus a wait instead of an immediate lock error.
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA busy_timeout=5000")

    def init_schema(self) -> None:
        sql = SCHEMA_PATH.read_text(encoding="utf-8")
        self._conn.executescript(sql)
        # Migrate older DBs that predate environment_key on workload_run.
        cols = {
            r[1]
            for r in self._conn.execute("PRAGMA table_info(workload_run)").fetchall()
        }
        if "environment_key" not in cols:
            self._conn.execute("ALTER TABLE workload_run ADD COLUMN environment_key TEXT")
        if "server_energy_j" not in cols:
            self._conn.execute("ALTER TABLE workload_run ADD COLUMN server_energy_j REAL")
        self._conn.execute(
            "CREATE INDEX IF NOT EXISTS idx_workload_run_env ON workload_run(environment_key)"
        )
        ref_cols = {
            r[1]
            for r in self._conn.execute("PRAGMA table_info(reference_profile)").fetchall()
        }
        if "predicted_server_energy_j" not in ref_cols:
            self._conn.execute(
                "ALTER TABLE reference_profile ADD COLUMN predicted_server_energy_j REAL"
            )
        self._conn.commit()

    def upsert_reference_profile(self, row: dict[str, Any]) -> None:
        cols = [
            "profile_key", "model", "runtime", "accelerator", "precision",
            "input_tokens", "output_tokens", "context_length", "batch", "concurrency",
            "predicted_p99_ms", "predicted_duration_s", "predicted_net_energy_j",
            "predicted_server_energy_j",
            "source", "confidence", "created_at",
        ]
        row = {**row, "created_at": row.get("created_at") or _now()}
        placeholders = ",".join("?" for _ in cols)
        updates = ",".join(f"{c}=excluded.{c}" for c in cols if c != "profile_key")
        self._conn.execute(
            f"INSERT INTO reference_profile ({','.join(cols)}) VALUES ({placeholders}) "
            f"ON CONFLICT(profile_key) DO UPDATE SET {updates}",
            [row.get(c) for c in cols],
        )
        self._conn.commit()

    def get_reference_profile(self, profile_key: str) -> Optional[dict[str, Any]]:
        cur = self._conn.execute(
            "SELECT * FROM reference_profile WHERE profile_key=?", (profile_key,)
        )
        r = cur.fetchone()
        return dict(r) if r else None

    def insert_workload_run(self, row: dict[str, Any]) -> None:
        cols = [
            "run_uuid", "experiment_id", "run_id", "trace_id", "workload_id",
            "cohort", "concurrency", "repeat_idx", "allocation_mode", "runtime", "model",
            "source", "quality_flag", "decision_reason", "routing_policy", "decision_branch",
            "npu_cap", "submitted_at", "scheduled_at", "container_started_at",
            "endpoint_ready_at", "load_started_at", "load_ended_at", "duration_s",
            "latency_p99_ms", "net_energy_j", "server_energy_j", "energy_per_success_j", "sla_pass",
            "requests_total", "requests_success", "error_rate", "git_sha", "result_path",
            "environment_key",
        ]
        try:
            self._conn.execute(
                f"INSERT INTO workload_run ({','.join(cols)}) VALUES ({','.join('?' for _ in cols)})",
                [row.get(c) for c in cols],
            )
            self._conn.commit()
        except sqlite3.IntegrityError as e:
            raise RuntimeError(
                f"workload_run conflict experiment_id={row.get('experiment_id')} run_id={row.get('run_id')}: {e}"
            ) from e

    def insert_request_samples(self, rows: list[dict[str, Any]]) -> None:
        if not rows:
            return
        cols = [
            "request_id", "run_uuid", "trace_id", "request_index", "endpoint",
            "route_device", "ttft_ms", "tpot_ms", "total_ms", "prompt_tokens",
            "completion_tokens", "error",
        ]
        self._conn.executemany(
            f"INSERT INTO request_sample ({','.join(cols)}) VALUES ({','.join('?' for _ in cols)})",
            [[r.get(c) for c in cols] for r in rows],
        )
        self._conn.commit()

    def insert_accelerator_samples(self, rows: list[dict[str, Any]]) -> None:
        if not rows:
            return
        cols = ["run_uuid", "sample_timestamp", "accelerator_uuid", "source", "metric", "value"]
        self._conn.executemany(
            f"INSERT INTO accelerator_sample ({','.join(cols)}) VALUES ({','.join('?' for _ in cols)})",
            [[r.get(c) for c in cols] for r in rows],
        )
        self._conn.commit()

    def upsert_interference(self, row: dict[str, Any]) -> None:
        cols = [
            "run_uuid", "colocated_json", "cnn_images_delta", "cnn_forwards_delta",
            "cnn_throughput_ips", "gpu_util_avg_pct", "npu_util_avg_pct",
        ]
        if isinstance(row.get("colocated_json"), (dict, list)):
            row = {**row, "colocated_json": json.dumps(row["colocated_json"])}
        self._conn.execute(
            f"INSERT INTO interference_context ({','.join(cols)}) VALUES ({','.join('?' for _ in cols)}) "
            f"ON CONFLICT(run_uuid) DO UPDATE SET "
            + ",".join(f"{c}=excluded.{c}" for c in cols if c != "run_uuid"),
            [row.get(c) for c in cols],
        )
        self._conn.commit()

    def insert_placement_decision(self, row: dict[str, Any]) -> None:
        cols = [
            "decision_id", "trace_id", "run_uuid", "mode", "decision_reason",
            "routing_policy", "decision_branch", "evaluated_at", "candidates_json",
            "selected_shadow", "selected_enforced", "selected_actual",
            "predicted_p99_ms", "predicted_duration_s", "predicted_net_energy_j",
            "confidence", "score_components_json", "regret_json", "model_version",
        ]
        for k in ("candidates_json", "score_components_json", "regret_json"):
            if isinstance(row.get(k), (dict, list)):
                row[k] = json.dumps(row[k])
        row = {**row, "evaluated_at": row.get("evaluated_at") or _now()}
        self._conn.execute(
            f"INSERT INTO placement_decision ({','.join(cols)}) VALUES ({','.join('?' for _ in cols)})",
            [row.get(c) for c in cols],
        )
        self._conn.commit()

    def get_run(self, run_uuid: str) -> Optional[dict[str, Any]]:
        cur = self._conn.execute("SELECT * FROM workload_run WHERE run_uuid=?", (run_uuid,))
        r = cur.fetchone()
        return dict(r) if r else None

    def get_run_by_trace(self, trace_id: str) -> Optional[dict[str, Any]]:
        cur = self._conn.execute(
            "SELECT * FROM workload_run WHERE trace_id=? ORDER BY load_ended_at DESC LIMIT 1",
            (trace_id,),
        )
        r = cur.fetchone()
        return dict(r) if r else None

    def list_unjoined_decisions(self) -> list[dict[str, Any]]:
        # An empty JSON object counts as "no outcome recorded yet", otherwise a
        # decision written at ingest time would never be revisited.
        cur = self._conn.execute(
            "SELECT * FROM placement_decision "
            "WHERE regret_json IS NULL OR TRIM(regret_json) IN ('', '{}')"
        )
        return [dict(r) for r in cur.fetchall()]

    def record_decision_outcome(
        self,
        decision_id: str,
        *,
        run_uuid: Optional[str],
        selected_actual: Optional[str],
        regret_json: dict[str, Any],
    ) -> None:
        self._conn.execute(
            "UPDATE placement_decision SET run_uuid=?, selected_actual=?, regret_json=? "
            "WHERE decision_id=?",
            (run_uuid, selected_actual, json.dumps(regret_json, ensure_ascii=False), decision_id),
        )
        self._conn.commit()

    def count_runs(self, source: Optional[str] = None) -> int:
        if source:
            cur = self._conn.execute("SELECT COUNT(*) FROM workload_run WHERE source=?", (source,))
        else:
            cur = self._conn.execute("SELECT COUNT(*) FROM workload_run")
        return int(cur.fetchone()[0])

    def upsert_policy_bound(self, row: dict[str, Any]) -> None:
        cols = [
            "policy_key", "environment_key", "cohort", "model", "runtime", "accelerator",
            "protocol", "concurrency_cap", "sla_p99_ms", "source", "confidence",
            "evidence_json", "notes", "enforce_enabled", "adopted_at", "created_at",
        ]
        row = {**row}
        if isinstance(row.get("evidence_json"), (dict, list)):
            row["evidence_json"] = json.dumps(row["evidence_json"], ensure_ascii=False)
        row.setdefault("created_at", _now())
        row.setdefault("enforce_enabled", 0)
        placeholders = ",".join("?" for _ in cols)
        updates = ",".join(f"{c}=excluded.{c}" for c in cols if c != "policy_key")
        self._conn.execute(
            f"INSERT INTO policy_bound ({','.join(cols)}) VALUES ({placeholders}) "
            f"ON CONFLICT(policy_key) DO UPDATE SET {updates}",
            [row.get(c) for c in cols],
        )
        self._conn.commit()

    def get_policy_bound(self, policy_key: str) -> Optional[dict[str, Any]]:
        cur = self._conn.execute("SELECT * FROM policy_bound WHERE policy_key=?", (policy_key,))
        r = cur.fetchone()
        return dict(r) if r else None

    def get_policy_by_environment(self, environment_key: str) -> Optional[dict[str, Any]]:
        cur = self._conn.execute(
            "SELECT * FROM policy_bound WHERE environment_key=? "
            "ORDER BY adopted_at DESC LIMIT 1",
            (environment_key,),
        )
        r = cur.fetchone()
        return dict(r) if r else None

    def list_runs(
        self,
        *,
        cohort: Optional[str] = None,
        concurrency: Optional[int] = None,
        experiment_id: Optional[str] = None,
        environment_key: Optional[str] = None,
    ) -> list[dict[str, Any]]:
        clauses = []
        args: list[Any] = []
        if cohort is not None:
            clauses.append("cohort=?")
            args.append(cohort)
        if concurrency is not None:
            clauses.append("concurrency=?")
            args.append(concurrency)
        if experiment_id is not None:
            clauses.append("experiment_id=?")
            args.append(experiment_id)
        if environment_key is not None:
            clauses.append("environment_key=?")
            args.append(environment_key)
        where = (" WHERE " + " AND ".join(clauses)) if clauses else ""
        cur = self._conn.execute(
            "SELECT run_uuid, experiment_id, run_id, cohort, concurrency, repeat_idx, "
            "allocation_mode, runtime, model, source, latency_p99_ms, duration_s, "
            "net_energy_j, server_energy_j, sla_pass, requests_total, error_rate, "
            "result_path, environment_key "
            f"FROM workload_run{where} ORDER BY concurrency, repeat_idx",
            args,
        )
        return [dict(r) for r in cur.fetchall()]

    def backup(self, dest: Path) -> Path:
        dest = Path(dest)
        dest.parent.mkdir(parents=True, exist_ok=True)
        with sqlite3.connect(str(dest)) as out:
            self._conn.backup(out)
        return dest

    def close(self) -> None:
        self._conn.close()


def open_catalog(db_path: Path) -> SQLiteCatalogRepository:
    repo = SQLiteCatalogRepository(db_path)
    repo.init_schema()
    return repo


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()
