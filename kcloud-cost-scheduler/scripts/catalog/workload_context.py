#!/usr/bin/env python3
"""P1 WorkloadContext — shared contract with Go parser."""

from __future__ import annotations

import hashlib
import json
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from typing import Any, Optional, Union

CONFIDENCE_SPEC = "SPEC"
CONFIDENCE_MEASURED = "MEASURED"
CONFIDENCE_REFERENCE = "REFERENCE"
CONFIDENCE_FALLBACK = "FALLBACK"
CONFIDENCE_LOW = "LOW"

# Match type for Catalog env lookup (Score path).
MATCH_EXACT = "exact"
MATCH_SIMILAR = "similar"
MATCH_NOT_FOUND = "profile_not_found"


Scalar = Union[float, int, str, None]


@dataclass
class WorkloadContext:
    profile_key: str
    workload_id: str
    trace_id: str
    experiment_id: str
    task: str = "serving"
    model: str = ""
    runtime: str = ""
    precision: str = "fp16"
    input_tokens: int = 256
    output_tokens: int = 128
    context_length: int = 4096
    batch: int = 1
    concurrency: int = 1
    slo_p99_ms: float = 0.0
    accelerator: str = "gpu"
    confidence: str = CONFIDENCE_SPEC
    routing_policy: str = ""
    decision_reason: str = "experimental_assignment"
    annotations_hash: str = ""
    created_at: str = ""
    extras: dict[str, Any] = field(default_factory=dict)
    environment_key: str = ""

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


def _axis(v: Scalar, default: str = "na") -> str:
    """Canonical axis string shared with Go (FormatFloat 'g' / strconv)."""
    if v is None or v == "":
        return default
    if isinstance(v, bool):
        return str(v).lower()
    if isinstance(v, float):
        # Match Go strconv.FormatFloat(f, 'g', -1, 64)
        return format(v, "g")
    if isinstance(v, int):
        return str(v)
    return str(v)


def build_profile_key(
    model: str,
    runtime: str,
    accelerator: str,
    precision: str,
    input_tokens: int,
    output_tokens: int,
    context_length: int,
    concurrency: int,
) -> str:
    raw = "|".join(
        [
            model or "unknown",
            runtime or "unknown",
            accelerator or "unknown",
            precision or "unknown",
            str(input_tokens),
            str(output_tokens),
            str(context_length),
            str(concurrency),
        ]
    )
    digest = hashlib.sha1(raw.encode("utf-8")).hexdigest()[:12]
    safe_model = (model or "unknown").replace("/", "_")
    return f"{safe_model}:{runtime}:{accelerator}:c{concurrency}:{digest}"


def build_environment_key(
    *,
    model: str,
    runtime: str,
    accelerator: str,
    sku: str = "",
    revision: str = "",
    node: str = "",
    precision: str = "auto",
    input_tokens: int = 0,
    output_tokens: int = 0,
    protocol: str = "",
    gpu_memory_utilization: Scalar = None,
    kv_blocks: Scalar = None,
    max_num_seqs: Scalar = None,
    lsu_quota: Scalar = None,
    engine_count: Scalar = 1,
    allocation_mode: str = "",
    # Backward-compatible aliases (R1 reingest / older callers)
    max_model_len: Scalar = None,
    pod_count: Scalar = None,
) -> str:
    """Catalog environment key (no concurrency).

    Axes (order fixed — must match Go BuildEnvironmentKey):
      model:revision:runtime:accelerator:sku:node:precision:protocol:
      inN:outN:utilU:kvKV:mnsM:lsuL:enginesE:mode

    Concurrency is attached only via ``build_catalog_lookup_key``.
    """
    kv = kv_blocks if kv_blocks is not None else max_model_len
    engines = engine_count if engine_count is not None else pod_count
    if engines is None or engines == "":
        engines = 1

    parts = [
        model or "unknown",
        _axis(revision),
        runtime or "unknown",
        accelerator or "unknown",
        _axis(sku),
        _axis(node),
        precision or "unknown",
        _axis(protocol),
        f"in{_axis(input_tokens, '0')}",
        f"out{_axis(output_tokens, '0')}",
        f"util{_axis(gpu_memory_utilization)}",
        f"kv{_axis(kv)}",
        f"mns{_axis(max_num_seqs)}",
        f"lsu{_axis(lsu_quota)}",
        f"engines{_axis(engines, '1')}",
        allocation_mode or "na",
    ]
    return ":".join(parts)


def build_catalog_lookup_key(environment_key: str, concurrency: int) -> str:
    """Score/Catalog measured lookup key = environment_key + :cN."""
    if concurrency < 1:
        raise ValueError("concurrency must be >= 1")
    return f"{environment_key}:c{int(concurrency)}"


def parse_workload_context(
    *,
    experiment_id: str,
    workload_id: str,
    trace_id: str,
    model: str,
    runtime: str,
    accelerator: str = "gpu",
    precision: str = "fp16",
    input_tokens: int = 256,
    output_tokens: int = 128,
    context_length: int = 4096,
    concurrency: int = 1,
    slo_p99_ms: float = 0.0,
    routing_policy: str = "",
    decision_reason: str = "experimental_assignment",
    confidence: str = CONFIDENCE_SPEC,
    annotations: Optional[dict[str, str]] = None,
    extras: Optional[dict[str, Any]] = None,
    environment_key: str = "",
) -> WorkloadContext:
    if slo_p99_ms < 0:
        raise ValueError("slo_p99_ms must be >= 0")
    if concurrency < 1:
        raise ValueError("concurrency must be >= 1")
    if input_tokens < 0 or output_tokens < 0:
        raise ValueError("token counts must be >= 0")

    ann = annotations or {}
    if "profile.ai/slo-p99-ms" in ann and ann["profile.ai/slo-p99-ms"]:
        try:
            slo_p99_ms = float(ann["profile.ai/slo-p99-ms"])
        except ValueError as e:
            raise ValueError(f"invalid profile.ai/slo-p99-ms: {ann['profile.ai/slo-p99-ms']}") from e
    if "profile.ai/model" in ann and ann["profile.ai/model"]:
        model = ann["profile.ai/model"]
    if "profile.ai/precision" in ann and ann["profile.ai/precision"]:
        precision = ann["profile.ai/precision"]

    conf = confidence
    if not model:
        conf = CONFIDENCE_FALLBACK
    ann_hash = hashlib.sha1(json.dumps(ann, sort_keys=True).encode()).hexdigest()[:16] if ann else ""

    return WorkloadContext(
        profile_key=build_profile_key(
            model, runtime, accelerator, precision, input_tokens, output_tokens, context_length, concurrency
        ),
        workload_id=workload_id,
        trace_id=trace_id,
        experiment_id=experiment_id,
        model=model,
        runtime=runtime,
        precision=precision,
        input_tokens=input_tokens,
        output_tokens=output_tokens,
        context_length=context_length,
        concurrency=concurrency,
        slo_p99_ms=slo_p99_ms,
        accelerator=accelerator,
        confidence=conf,
        routing_policy=routing_policy,
        decision_reason=decision_reason,
        annotations_hash=ann_hash,
        created_at=datetime.now(timezone.utc).isoformat(),
        extras=extras or {},
        environment_key=environment_key,
    )


def write_workload_context(path, ctx: WorkloadContext) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(ctx.to_dict(), indent=2, ensure_ascii=False), encoding="utf-8")
