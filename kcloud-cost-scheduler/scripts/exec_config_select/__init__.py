"""Trial-only execution-config selector."""

from .selector import (
    POLICY_VERSION,
    BundleProfile,
    SelectionDecision,
    WorkBundleSpec,
    select_execution_config,
)

__all__ = [
    "POLICY_VERSION",
    "BundleProfile",
    "SelectionDecision",
    "WorkBundleSpec",
    "select_execution_config",
]
