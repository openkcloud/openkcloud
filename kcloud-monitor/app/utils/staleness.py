"""
Staleness helpers (design_contracts §6).

A response is "stale" when its underlying data was observed longer ago than a
threshold: 2 minutes for metrics, 10 minutes for resource-map data. Stale data
may still be returned, but ``is_stale`` is set and ``STALE_DATA`` is added to
``warnings[]``.
"""

from datetime import datetime
from typing import Optional

METRIC_STALE_SECONDS = 120        # 2 minutes (design_contracts §6)
RESOURCE_MAP_STALE_SECONDS = 600  # 10 minutes (design_contracts §6)
STALE_DATA_WARNING = "STALE_DATA"


def is_stale(
    observed_at: Optional[datetime],
    *,
    now: Optional[datetime] = None,
    threshold_seconds: int = METRIC_STALE_SECONDS,
) -> bool:
    """Return True if ``observed_at`` is older than ``threshold_seconds``.

    Returns False when ``observed_at`` is None (cannot judge) or in the future
    (clock skew). Times are treated as naive UTC, consistent with the response
    models' ``datetime.utcnow`` defaults.
    """
    if observed_at is None:
        return False
    now = now or datetime.utcnow()
    age_seconds = (now - observed_at).total_seconds()
    return age_seconds > threshold_seconds


def apply_staleness(
    response,
    observed_at: Optional[datetime],
    *,
    now: Optional[datetime] = None,
    threshold_seconds: int = METRIC_STALE_SECONDS,
):
    """Set ``observed_at``/``is_stale`` on a BaseResponse and append the
    ``STALE_DATA`` warning when stale. Mutates and returns ``response``.
    """
    if observed_at is not None and getattr(response, "observed_at", None) is None:
        response.observed_at = observed_at
    if is_stale(observed_at, now=now, threshold_seconds=threshold_seconds):
        response.is_stale = True
        if STALE_DATA_WARNING not in response.warnings:
            response.warnings.append(STALE_DATA_WARNING)
    return response
