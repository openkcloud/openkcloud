"""Minimal execution-config selector for an LLM+CNN work bundle.

Does not call live Catalog; callers inject profiles (fixture or lookup results).
Does not mutate cluster state.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass, field
from typing import Any, Mapping, Optional, Sequence

POLICY_VERSION = "exec-config-select-1"

LOCKED_REQUESTS_SHA = (
    "e12a54b63b7d5745e80a78520fea0aa19cb0182a563b6ed2c870fce8c18e88e7"
)

TEMPLATE_SHA = {
    "job-a-llm.yaml.template": (
        "d532a499b2b8bc92b405d80c62e257fd525b8290a17a271b643189f23eac0a4b"
    ),
    "job-a-cnn.yaml.template": (
        "4273433af3eb6e29d9a34b98b0e6fe73d7c788c346b6a6e2fff016c02d72f78b"
    ),
    "job-rngd-llm.yaml.template": (
        "626b0dd22b565ce0ae9a24e507b63e5c18d2fc2cbde0db685ead1f68047030b6"
    ),
    "job-b-cnn.yaml.template": (
        "6875c7de409a005f1732a05ffc8d8ad810ec9757ddcba4bb75875ed4d12ba17e"
    ),
}

CONFIGS: Mapping[str, Mapping[str, Any]] = {
    "A_gpu_serial": {
        "llm_template": "job-a-llm.yaml.template",
        "cnn_template": "job-a-cnn.yaml.template",
        "compatible_with": {
            "workload_kind": "llm_cnn_bundle",
            "N": 600,
            "C": 4,
            "in_tokens": 149,
            "out_tokens": 128,
        },
    },
    "B_rngd_gpu": {
        "llm_template": "job-rngd-llm.yaml.template",
        "cnn_template": "job-b-cnn.yaml.template",
        "compatible_with": {
            "workload_kind": "llm_cnn_bundle",
            "N": 600,
            "C": 4,
            "in_tokens": 149,
            "out_tokens": 128,
        },
    },
}


@dataclass(frozen=True)
class WorkBundleSpec:
    work_id: str
    workload_kind: str = "llm_cnn_bundle"
    N: int = 600
    C: int = 4
    in_tokens: int = 149
    out_tokens: int = 128
    requests_sha256: str = LOCKED_REQUESTS_SHA
    cnn_images: int = 566432
    energy_scope: str = "server_primary_wh"
    # device must stay unset for auto path
    device: Optional[str] = None


@dataclass(frozen=True)
class BundleProfile:
    config_id: str
    predicted_server_wh: float
    sla_pass: bool
    confidence: str  # MEASURED required
    energy_scope: str = "server_primary_wh"
    profile_id: str = ""
    match_type: str = "exact"
    notes: str = ""


@dataclass
class CandidateRecord:
    config_id: str
    eligible: bool
    exclude_reason: Optional[str] = None
    predicted_server_wh: Optional[float] = None
    profile_id: Optional[str] = None
    template_sha: Mapping[str, str] = field(default_factory=dict)


@dataclass
class SelectionDecision:
    policy_version: str
    work_id: str
    decision: str  # selected | hold
    selected_config_id: Optional[str]
    candidates: list[CandidateRecord]
    hold_reason: Optional[str] = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "policy_version": self.policy_version,
            "work_id": self.work_id,
            "decision": self.decision,
            "selected_config_id": self.selected_config_id,
            "hold_reason": self.hold_reason,
            "candidates": [asdict(c) for c in self.candidates],
        }


def _compatible(spec: WorkBundleSpec, config_id: str) -> Optional[str]:
    if spec.device:
        return "input_device_fixed"
    meta = CONFIGS.get(config_id)
    if meta is None:
        return "unknown_config"
    need = meta["compatible_with"]
    if spec.workload_kind != need["workload_kind"]:
        return "workload_kind_mismatch"
    if (
        spec.N != need["N"]
        or spec.C != need["C"]
        or spec.in_tokens != need["in_tokens"]
        or spec.out_tokens != need["out_tokens"]
    ):
        return "token_or_concurrency_mismatch"
    if spec.requests_sha256 != LOCKED_REQUESTS_SHA:
        return "requests_sha_mismatch"
    if spec.energy_scope != "server_primary_wh":
        return "energy_scope_unsupported"
    return None


def _profile_ok(profile: Optional[BundleProfile], config_id: str) -> Optional[str]:
    if profile is None:
        return "profile_not_found"
    if profile.config_id != config_id:
        return "profile_config_mismatch"
    if profile.match_type != "exact":
        return "match_type_not_exact"
    if profile.confidence != "MEASURED":
        return "confidence_not_measured"
    if not profile.sla_pass:
        return "sla_fail"
    if profile.energy_scope != "server_primary_wh":
        return "energy_scope_mismatch"
    if profile.predicted_server_wh is None or profile.predicted_server_wh < 0:
        return "energy_invalid"
    return None


def select_execution_config(
    spec: WorkBundleSpec,
    profiles: Mapping[str, Optional[BundleProfile]],
    config_ids: Optional[Sequence[str]] = None,
) -> SelectionDecision:
    """Choose lowest predicted_server_wh among eligible configs."""
    ids = list(config_ids) if config_ids is not None else list(CONFIGS.keys())
    records: list[CandidateRecord] = []
    eligible: list[tuple[str, float, CandidateRecord]] = []

    for cid in ids:
        tsha = {}
        meta = CONFIGS.get(cid)
        if meta:
            tsha = {
                meta["llm_template"]: TEMPLATE_SHA[meta["llm_template"]],
                meta["cnn_template"]: TEMPLATE_SHA[meta["cnn_template"]],
            }
        reason = _compatible(spec, cid)
        if reason:
            records.append(
                CandidateRecord(
                    config_id=cid,
                    eligible=False,
                    exclude_reason=reason,
                    template_sha=tsha,
                )
            )
            continue
        prof = profiles.get(cid)
        preason = _profile_ok(prof, cid)
        if preason:
            records.append(
                CandidateRecord(
                    config_id=cid,
                    eligible=False,
                    exclude_reason=preason,
                    profile_id=getattr(prof, "profile_id", None) if prof else None,
                    template_sha=tsha,
                )
            )
            continue
        assert prof is not None
        rec = CandidateRecord(
            config_id=cid,
            eligible=True,
            predicted_server_wh=float(prof.predicted_server_wh),
            profile_id=prof.profile_id or None,
            template_sha=tsha,
        )
        records.append(rec)
        eligible.append((cid, float(prof.predicted_server_wh), rec))

    if not eligible:
        return SelectionDecision(
            policy_version=POLICY_VERSION,
            work_id=spec.work_id,
            decision="hold",
            selected_config_id=None,
            candidates=records,
            hold_reason="no_eligible_candidate",
        )

    # min energy; tie → lexicographic config_id
    eligible.sort(key=lambda x: (x[1], x[0]))
    chosen = eligible[0][0]
    return SelectionDecision(
        policy_version=POLICY_VERSION,
        work_id=spec.work_id,
        decision="selected",
        selected_config_id=chosen,
        candidates=records,
        hold_reason=None,
    )
