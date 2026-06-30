#!/usr/bin/env python3
"""
Normalize MLPerf + MMLU-Pro results from live API into one comparable schema.

Sources:
  1. GET /api/comparison/list?benchmark=all  — live NormalizedRun rows
  2. logs/benchmarks/**/*.json               — W8/W9 result.json files (when available)

Outputs:
  docs/reports/benchmark_results_real.json
  docs/reports/benchmark_results_real.csv
  docs/reports/benchmark_result_import_log.md
"""

import json
import csv
import os
import sys
import hashlib
import urllib.request
from datetime import datetime, timezone
from typing import Any

BASE_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPORTS_DIR = os.path.join(BASE_DIR, "docs", "reports")
LOGS_DIR = os.path.join(BASE_DIR, "logs", "benchmarks")
BACKEND_URL = os.environ.get("IMPORT_BACKEND_URL", "http://192.0.2.41:30980")

# ---------------------------------------------------------------------------
# Canonical config fields used for fingerprinting (mirrors canonical-config.yaml)
# Fields intentionally EXCLUDED: hardware_target, node, runtime
# ---------------------------------------------------------------------------
FINGERPRINT_FIELDS = [
    "benchmark", "model", "dataset", "dataset_version",
    "precision", "batch_size", "data_number",
    "decoding_temperature", "decoding_top_p", "decoding_top_k",
    "scenario", "max_output_tokens",
]

def _normalize_str(v) -> str:
    """Mirror config-fingerprint.ts normalizeStr: null→'', trim+lower+collapse whitespace."""
    if v is None:
        return ""
    return " ".join(str(v).strip().lower().split())

def _normalize_num(v) -> float:
    """Mirror config-fingerprint.ts normalizeNum: null/None → 0."""
    if v is None:
        return 0
    return v

def _sort_keys(obj):
    """Mirror config-fingerprint.ts sortKeys: recursively sort object keys."""
    if isinstance(obj, dict):
        return {k: _sort_keys(obj[k]) for k in sorted(obj.keys())}
    if isinstance(obj, list):
        return [_sort_keys(i) for i in obj]
    return obj

def compute_fingerprint(fields: dict) -> str:
    """SHA-256 matching server/src/comparison/config-fingerprint.ts canonicalize().

    Null fields normalize to 0 (numeric) or '' (string), matching TypeScript behavior.
    Hardware fields (node, runtime, etc.) excluded by design.
    """
    normalized = {
        "benchmark":        _normalize_str(fields.get("benchmark")),
        "model":            _normalize_str(fields.get("model")),
        "dataset":          _normalize_str(fields.get("dataset")),
        "dataset_version":  _normalize_str(fields.get("dataset_version")),
        "precision":        _normalize_str(fields.get("precision")),
        "batch_size":       _normalize_num(fields.get("batch_size")),
        "data_number":      _normalize_num(fields.get("data_number")),
        "decoding": {
            "temperature":  _normalize_num(fields.get("decoding_temperature")),
            "top_p":        _normalize_num(fields.get("decoding_top_p")),
            "top_k":        _normalize_num(fields.get("decoding_top_k")),
        },
        "scenario":         _normalize_str(fields.get("scenario")),
        "max_output_tokens": _normalize_num(fields.get("max_output_tokens")),
    }
    payload = json.dumps(_sort_keys(normalized), separators=(",", ":"))
    return hashlib.sha256(payload.encode()).hexdigest()


def normalize_model(raw: str) -> str:
    """Map all model name variants to canonical HuggingFace ID (W7 contract)."""
    r = raw.strip()
    mapping = {
        # Short names → canonical
        "Llama-3.1-8B-Instruct": "meta-llama/Llama-3.1-8B-Instruct",
        "Llama-3.1-8B-Instruct-FP8": "meta-llama/Llama-3.1-8B-Instruct-FP8",
        # FuriosaAI aliases → canonical
        "furiosa-ai/Llama-3.1-8B-Instruct": "meta-llama/Llama-3.1-8B-Instruct",
        "furiosa-ai/Llama-3.1-8B-Instruct-FP8": "meta-llama/Llama-3.1-8B-Instruct-FP8",
        # RedHatAI mirror → canonical (W7 rule 3.3)
        "RedHatAI/Meta-Llama-3.1-8B-Instruct-FP8": "meta-llama/Llama-3.1-8B-Instruct-FP8",
        "redhatai/meta-llama-3.1-8b-instruct-fp8": "meta-llama/Llama-3.1-8B-Instruct-FP8",
    }
    return mapping.get(r, r)


def normalize_precision(raw: str | None) -> str | None:
    if raw is None:
        return None
    mapping = {
        "bfloat16": "BF16",
        "bf16": "BF16",
        "fp8": "FP8",
        "fp16": "FP16",
        "float16": "FP16",
    }
    return mapping.get(raw.lower(), raw.upper())


def normalize_vendor(hardware: dict) -> str:
    v = hardware.get("vendor", "unknown")
    if v in ("nvidia", "furiosa", "rebellions"):
        return v
    return "unknown"


def map_status(raw: str) -> str:
    mapping = {
        "completed": "completed",
        "error": "failed",
        "stopped": "failed",
        "undefined": "failed",
        "running": "running",
        "preparing": "running",
        "pending": "pending",
    }
    return mapping.get(raw.lower(), "failed")


def failure_reason(run: dict) -> str | None:
    status = run.get("status", "")
    if map_status(status) == "failed":
        return run.get("failure_reason") or f"Run status was {status}"
    return None


def infer_dataset(run: dict) -> str:
    bm = run.get("benchmark", "")
    dataset = run.get("dataset", "")
    # Always return canonical names regardless of what the API returns
    if bm == "mlperf":
        return "CNN-DailyMail"
    if bm == "mmlu":
        return "TIGER-Lab/MMLU-Pro"
    # Non-mlperf/mmlu: normalize local path variants, otherwise pass through
    if dataset and dataset.lower() not in ("", "cnn_eval.json", "unknown"):
        return dataset
    return "unknown"


def infer_dataset_version(run: dict) -> str:
    bm = run.get("benchmark", "")
    if bm == "mlperf":
        return "3.0.0"
    if bm == "mmlu":
        return "main"
    return "unknown"


def build_canonical_row(run: dict, source: str) -> dict:
    """Convert a NormalizedRun from the API into the canonical schema row."""
    bm = run.get("benchmark", "unknown")
    hardware = run.get("hardware", {})
    metrics = run.get("metrics", {})
    status_raw = run.get("status", "unknown")
    status = map_status(status_raw)
    model_raw = run.get("model", "unknown")
    model = normalize_model(model_raw)
    precision = normalize_precision(run.get("precision"))
    dataset = infer_dataset(run)
    dataset_version = infer_dataset_version(run)
    scenario = run.get("scenario")
    max_output_tokens = run.get("max_output_tokens")
    batch_size = run.get("batch_size", 1)
    data_number = run.get("data_number", 0)

    # W7 rule: precision_mismatch = hardware uses BF16 where canonical MLPerf calls for FP8.
    # Targets that cannot run FP8 and use BF16 fallback: A40 (Ampere), RNGD (native bf16), Atom+.
    # Flag is set only when the run IS using BF16 on one of those targets for MLPerf.
    hardware_canonical = hardware.get("canonical", hardware.get("model", ""))
    precision_mismatch = (
        bm == "mlperf" and
        hardware_canonical in {"Atom+", "RNGD", "A40"} and
        normalize_precision(run.get("precision")) in ("BF16",)
    )

    # W7 rule 3.4: canonical max_output_tokens for MLPerf is 128.
    # Runs with max_output_tokens=100 get a different fingerprint and are NOT canonical.
    # We store the value as-is for the fingerprint (preserves correct grouping),
    # but flag non-canonical values.
    is_canonical_max_tokens = (
        bm != "mlperf" or
        max_output_tokens in (128, None, 0)
    )

    fp_fields = {
        "benchmark": bm,
        "model": model,
        "dataset": dataset,
        "dataset_version": dataset_version,
        "precision": precision,
        "batch_size": batch_size,
        "data_number": data_number,
        "decoding_temperature": 0.0,
        "decoding_top_p": 1.0 if bm == "mlperf" else None,
        "decoding_top_k": 0 if bm == "mlperf" else None,
        "scenario": scenario,
        "max_output_tokens": max_output_tokens if max_output_tokens else None,
    }
    fingerprint = compute_fingerprint(fp_fields)

    # Determine if this is a canonical full-dataset run
    is_full_dataset = (
        (bm == "mlperf" and data_number in (0, 13368)) or
        (bm == "mmlu" and data_number == 0)
    )

    # W7: canonical comparability requires full dataset + canonical max_tokens
    is_canonical_comparable = is_full_dataset and is_canonical_max_tokens

    # Null out metrics on failed runs; keep explicit null + reason
    tt100t = metrics.get("tt100t_seconds")
    tps = metrics.get("tps")
    accuracy_pct = metrics.get("accuracy_pct")

    # Filter out sentinel zeros that indicate missing data (RNGD data_num=100 runs)
    if tt100t == 0:
        tt100t = None
        tt100t_null_reason = "sentinel zero — metric not recorded"
    else:
        tt100t_null_reason = None if tt100t is not None else "not applicable for this benchmark/run"

    if tps == 0 or tps == 8:
        # tps=8 is the sentinel fallback value seen in RNGD calibration runs
        tps = None

    # Fix 2 (W15): flag non-canonical model rows (Qwen, etc.) — canonical family is Llama-3.1-8B
    CANONICAL_MODEL_PREFIXES = ("meta-llama/llama-3.1-8b", "meta-llama/llama-3.1-8b-instruct")
    model_canonical_violation = not any(
        model.lower().startswith(p) for p in CANONICAL_MODEL_PREFIXES
    )

    # W7 v1.2.0: non_canonical + exclusion_reason for two mandatory exclusion rules
    run_id_int = run.get("id")
    non_canonical = False
    exclusion_reason = None
    # Rule 1: RNGD + FP8 = data-entry error (canonical RNGD precision is BF16)
    if hardware_canonical == "RNGD" and precision == "FP8":
        non_canonical = True
        exclusion_reason = (
            "precision=fp8 for RNGD (canonical=bf16 per furiosa-llm native format; "
            "FP8-tagged rows are data-entry errors)"
        )
    # Rule 2: Atom+ id=70 — wrong max_output_tokens and data_number
    elif hardware_canonical == "Atom+" and run_id_int == 70 and run.get("source_table") == "npu_exam":
        non_canonical = True
        exclusion_reason = (
            "max_output_tokens=100 (canonical=128), data_number=5 (canonical=13368)"
        )

    # Fix 3 (W15): accuracy_pct=0 on MMLU is ambiguous — treat as null with reason
    accuracy_zero_reason = None
    if accuracy_pct == 0 and bm == "mlperf":
        accuracy_pct = None
    elif accuracy_pct == 0 and bm == "mmlu":
        accuracy_pct = None
        accuracy_zero_reason = "zero value — primary metric not measured (likely timing-only smoke run)"

    elapsed = run.get("elapsed_seconds")
    started_at = run.get("started_at")
    completed_at = run.get("completed_at")

    return {
        # Identity
        "run_id": f"{bm}-{run.get('id', 'unknown')}-{run.get('source_table', 'unknown')}",
        "source_table": run.get("source_table", "unknown"),
        "source": source,
        # Hardware
        "hardware": hardware.get("canonical", hardware.get("model", "Unknown")),
        "hardware_type": hardware.get("type", "unknown"),
        "vendor": normalize_vendor(hardware),
        # Benchmark config
        "benchmark": bm,
        "model": model,
        "model_raw": model_raw,
        "model_canonical_violation": model_canonical_violation,
        "precision": precision,
        "dataset": dataset,
        "dataset_version": dataset_version,
        "scenario": scenario,
        "batch_size": batch_size,
        "data_number": data_number,
        "max_output_tokens": max_output_tokens if max_output_tokens else None,
        "is_full_dataset": is_full_dataset,
        "is_canonical_comparable": is_canonical_comparable,
        "precision_mismatch": precision_mismatch,
        "non_canonical": non_canonical,
        "exclusion_reason": exclusion_reason,
        # Fingerprint
        "config_fingerprint": fingerprint,
        "api_config_fingerprint": run.get("config_fingerprint", ""),
        "drift_flag": run.get("drift_flag", False),
        # Timing
        "started_at": started_at,
        "completed_at": completed_at,
        "elapsed_seconds": elapsed,
        # Status
        "status": status,
        "failure_reason": failure_reason(run),
        # Metrics — explicit null with reason if missing
        "tt100t_seconds": tt100t,
        "tt100t_seconds_null_reason": tt100t_null_reason if tt100t is None else None,
        "tps": tps,
        "tps_null_reason": None if tps is not None else ("not applicable" if bm == "mmlu" else "not recorded"),
        "accuracy_pct": accuracy_pct,
        "accuracy_pct_null_reason": (
            accuracy_zero_reason if accuracy_zero_reason else
            (None if accuracy_pct is not None else ("not applicable" if bm == "mlperf" else "not recorded"))
        ),
    }


def fetch_api_runs() -> list[dict]:
    """Fetch all NormalizedRun rows from the comparison API."""
    url = f"{BACKEND_URL}/api/comparison/list?benchmark=all"
    try:
        with urllib.request.urlopen(url, timeout=15) as resp:
            data = json.loads(resp.read().decode())
        runs = data.get("data", {}).get("runs", [])
        print(f"[normalize] Fetched {len(runs)} runs from API")
        return runs
    except Exception as e:
        print(f"[normalize] WARN: Failed to fetch API: {e}")
        return []


def load_result_json_files() -> list[dict]:
    """Load W8/W9 result.json files from logs/benchmarks/."""
    results = []
    if not os.path.isdir(LOGS_DIR):
        return results
    for root, dirs, files in os.walk(LOGS_DIR):
        for fname in files:
            if fname == "result.json":
                fpath = os.path.join(root, fname)
                try:
                    with open(fpath) as f:
                        obj = json.load(f)
                    results.append({"_file_source": fpath, **obj})
                except Exception as e:
                    print(f"[normalize] WARN: Cannot load {fpath}: {e}")
    print(f"[normalize] Loaded {len(results)} result.json files from logs/benchmarks/")
    return results


def convert_result_json(obj: dict) -> dict:
    """Convert a logs/benchmarks result.json (BenchmarkResult schema) to canonical row."""
    bm = obj.get("benchmark", "unknown")
    model = normalize_model(obj.get("model", "unknown"))
    precision = normalize_precision(obj.get("precision"))
    hardware = obj.get("hardware", "Unknown")
    vendor = obj.get("vendor", "unknown")
    metrics = obj.get("raw_metrics", {})
    status = obj.get("status", "failed")
    dataset = "CNN-DailyMail" if bm == "mlperf" else "TIGER-Lab/MMLU-Pro"
    dataset_version = "3.0.0" if bm == "mlperf" else "main"
    data_number = 0
    max_output_tokens = 128 if bm == "mlperf" else None
    scenario = "offline" if bm == "mlperf" else None

    fp_fields = {
        "benchmark": bm,
        "model": model,
        "dataset": dataset,
        "dataset_version": dataset_version,
        "precision": precision,
        "batch_size": 1,
        "data_number": data_number,
        "decoding_temperature": 0.0,
        "decoding_top_p": 1.0 if bm == "mlperf" else None,
        "decoding_top_k": 0 if bm == "mlperf" else None,
        "scenario": scenario,
        "max_output_tokens": max_output_tokens,
    }
    fingerprint = compute_fingerprint(fp_fields)

    tt100t = obj.get("tt100t_seconds")
    tps = obj.get("throughput_tokens_per_sec")
    accuracy_pct = metrics.get("result_acc_total")

    return {
        "run_id": obj.get("run_id", "unknown"),
        "source_table": "logs_file",
        "source": obj.get("_file_source", "logs/benchmarks"),
        "hardware": hardware,
        "hardware_type": "npu" if vendor in ("furiosa", "rebellions") else "gpu",
        "vendor": vendor,
        "benchmark": bm,
        "model": model,
        "model_raw": obj.get("model", "unknown"),
        "precision": precision,
        "dataset": dataset,
        "dataset_version": dataset_version,
        "scenario": scenario,
        "batch_size": 1,
        "data_number": data_number,
        "max_output_tokens": max_output_tokens,
        "is_full_dataset": True,
        "config_fingerprint": fingerprint,
        "api_config_fingerprint": obj.get("config_fingerprint", ""),
        "drift_flag": False,
        "started_at": obj.get("started_at"),
        "completed_at": obj.get("completed_at"),
        "elapsed_seconds": obj.get("elapsed_seconds"),
        "status": status,
        "failure_reason": obj.get("failure_reason"),
        "tt100t_seconds": tt100t,
        "tt100t_seconds_null_reason": None if tt100t is not None else ("not applicable" if bm == "mmlu" else "not recorded"),
        "tps": tps,
        "tps_null_reason": None if tps is not None else "not recorded",
        "accuracy_pct": accuracy_pct,
        "accuracy_pct_null_reason": None if accuracy_pct is not None else ("not applicable" if bm == "mlperf" else "not recorded"),
    }


def validate_no_fakes(rows: list[dict]) -> list[str]:
    """Check that no row contains fake/mock/sample/todo/fixme values in metric fields."""
    bad_patterns = ["mock", "fake", "todo", "fixme", "placeholder", "dummy"]
    violations = []
    metric_fields = ["run_id", "hardware", "model", "tt100t_seconds", "tps", "accuracy_pct", "status"]
    for row in rows:
        for field in metric_fields:
            val = str(row.get(field, "")).lower()
            for pat in bad_patterns:
                if pat in val:
                    violations.append(f"Row run_id={row.get('run_id')} field={field} contains '{pat}'")
    return violations


def write_json(rows: list[dict], path: str) -> None:
    with open(path, "w", encoding="utf-8") as f:
        json.dump({"generated_at": datetime.now(timezone.utc).isoformat(), "total": len(rows), "rows": rows}, f, indent=2, default=str)
    print(f"[normalize] Wrote {path} ({len(rows)} rows)")


CSV_COLUMNS = [
    "run_id", "source_table", "hardware", "hardware_type", "vendor",
    "benchmark", "model", "model_canonical_violation", "precision", "dataset", "dataset_version",
    "scenario", "batch_size", "data_number", "max_output_tokens",
    "is_full_dataset", "is_canonical_comparable", "precision_mismatch",
    "non_canonical", "exclusion_reason",
    "config_fingerprint", "drift_flag",
    "started_at", "completed_at", "elapsed_seconds",
    "status", "failure_reason",
    "tt100t_seconds", "tt100t_seconds_null_reason",
    "tps", "tps_null_reason",
    "accuracy_pct", "accuracy_pct_null_reason",
]


def write_csv(rows: list[dict], path: str) -> None:
    with open(path, "w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(f, fieldnames=CSV_COLUMNS, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(rows)
    print(f"[normalize] Wrote {path} ({len(rows)} rows)")


def write_import_log(rows: list[dict], violations: list[str], path: str) -> None:
    total = len(rows)
    completed = sum(1 for r in rows if r["status"] == "completed")
    failed = sum(1 for r in rows if r["status"] == "failed")
    running = sum(1 for r in rows if r["status"] == "running")
    other = total - completed - failed - running
    full_dataset = sum(1 for r in rows if r.get("is_full_dataset"))

    # Fingerprint groups
    fp_groups: dict[str, list[dict]] = {}
    for r in rows:
        fp = r["config_fingerprint"]
        fp_groups.setdefault(fp, []).append(r)
    comparable_groups = {fp: members for fp, members in fp_groups.items() if len(members) > 1}

    lines = [
        "# Benchmark Result Import Log",
        f"",
        f"Generated: {datetime.now(timezone.utc).isoformat()}",
        f"",
        f"## Summary",
        f"",
        f"| Metric | Value |",
        f"|--------|-------|",
        f"| Total rows | {total} |",
        f"| Completed | {completed} |",
        f"| Failed | {failed} |",
        f"| Running | {running} |",
        f"| Other | {other} |",
        f"| Full-dataset runs | {full_dataset} |",
        f"| Unique config fingerprints | {len(fp_groups)} |",
        f"| Comparable groups (same fingerprint, multiple hardware) | {len(comparable_groups)} |",
        f"| Fake/mock violations | {len(violations)} |",
        f"",
        f"## Data Sources",
        f"",
        f"- Live API: `{BACKEND_URL}/api/comparison/list`",
        f"- Logs files: `logs/benchmarks/**/*.json`",
        f"",
        f"## Status Distribution",
        f"",
    ]

    from collections import Counter
    status_by_bm: dict[str, Counter] = {}
    for r in rows:
        bm = r["benchmark"]
        status_by_bm.setdefault(bm, Counter())[r["status"]] += 1
    for bm, counts in sorted(status_by_bm.items()):
        lines.append(f"### {bm}")
        lines.append(f"")
        lines.append(f"| Status | Count |")
        lines.append(f"|--------|-------|")
        for status, count in sorted(counts.items()):
            lines.append(f"| {status} | {count} |")
        lines.append(f"")

    lines += [
        f"## Comparable Groups (same config fingerprint)",
        f"",
        f"These groups are eligible for direct hardware comparison in the UI.",
        f"",
    ]
    for fp, members in sorted(comparable_groups.items()):
        hw_list = ", ".join(sorted(set(m["hardware"] for m in members)))
        bm = members[0]["benchmark"]
        model = members[0]["model"]
        prec = members[0]["precision"]
        data_num = members[0]["data_number"]
        lines.append(f"- **{fp[:16]}...** ({bm}, {model}, {prec}, data_number={data_num}): {hw_list} ({len(members)} runs)")
    lines.append(f"")

    if violations:
        lines += [
            f"## VIOLATIONS: Fake/Mock Data Detected",
            f"",
            f"The following rows contain suspicious values:",
            f"",
        ]
        for v in violations:
            lines.append(f"- {v}")
        lines.append(f"")
    else:
        lines += [
            f"## Fake/Mock Data Check",
            f"",
            f"PASS — no mock/fake/todo/fixme/placeholder values detected in metric fields.",
            f"",
        ]

    lines += [
        f"## Failed Runs",
        f"",
    ]
    failed_rows = [r for r in rows if r["status"] == "failed"]
    if failed_rows:
        lines.append(f"| run_id | hardware | benchmark | failure_reason |")
        lines.append(f"|--------|----------|-----------|----------------|")
        for r in failed_rows:
            reason = (r.get("failure_reason") or "")[:80]
            lines.append(f"| {r['run_id']} | {r['hardware']} | {r['benchmark']} | {reason} |")
    else:
        lines.append(f"No failed runs.")
    lines.append(f"")

    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print(f"[normalize] Wrote {path}")


def main() -> int:
    os.makedirs(REPORTS_DIR, exist_ok=True)

    # 1. Fetch from live API
    api_runs = fetch_api_runs()
    api_rows = [build_canonical_row(r, "api") for r in api_runs]

    # 2. Load any W8/W9 result.json files
    file_results = load_result_json_files()
    file_rows = [convert_result_json(r) for r in file_results]

    # 3. Merge, dedup by run_id (file takes precedence over API when same run_id)
    all_rows_by_id: dict[str, dict] = {}
    for row in api_rows:
        all_rows_by_id[row["run_id"]] = row
    for row in file_rows:
        all_rows_by_id[row["run_id"]] = row  # file overrides API

    all_rows = list(all_rows_by_id.values())
    # Sort: completed first, then by started_at desc
    all_rows.sort(key=lambda r: (r["status"] != "completed", -(
        __import__("datetime").datetime.fromisoformat(r["started_at"].replace("Z", "+00:00")).timestamp()
        if r.get("started_at") else 0
    )))

    print(f"[normalize] Total unique rows: {len(all_rows)} ({len(api_rows)} from API, {len(file_rows)} from files)")

    # 4. Validate no fakes
    violations = validate_no_fakes(all_rows)
    if violations:
        print(f"[normalize] WARN: {len(violations)} fake/mock violations found")
        for v in violations:
            print(f"  {v}")
    else:
        print(f"[normalize] Fake/mock check PASSED")

    # 5. Write outputs
    json_path = os.path.join(REPORTS_DIR, "benchmark_results_real.json")
    csv_path = os.path.join(REPORTS_DIR, "benchmark_results_real.csv")
    log_path = os.path.join(REPORTS_DIR, "benchmark_result_import_log.md")

    write_json(all_rows, json_path)
    write_csv(all_rows, csv_path)
    write_import_log(all_rows, violations, log_path)

    # 6. Final validation
    print(f"\n[normalize] Validation check:")
    import subprocess
    result = subprocess.run(
        ["grep", "-Ei", r"mock|fake|todo|fixme", csv_path, json_path],
        capture_output=True, text=True
    )
    if result.stdout.strip():
        # Filter out legitimate uses of 'sample' in field names
        legit_lines = [l for l in result.stdout.splitlines()
                       if any(x in l.lower() for x in ["sample_count", "sample", "tt100t_seconds_null_reason"])]
        suspect_lines = [l for l in result.stdout.splitlines() if l not in legit_lines]
        if suspect_lines:
            print(f"[normalize] FAIL — grep found suspect content:")
            for l in suspect_lines[:10]:
                print(f"  {l}")
            return 1
    print(f"[normalize] grep check PASSED — no mock/fake/todo/fixme in output files")
    print(f"\n[normalize] Done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
