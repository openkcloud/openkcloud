"""Tests for staleness helpers (design_contracts §6)."""

from datetime import datetime, timedelta

from app.models.common.responses import SuccessResponse
from app.utils.staleness import (
    is_stale,
    apply_staleness,
    METRIC_STALE_SECONDS,
    RESOURCE_MAP_STALE_SECONDS,
    STALE_DATA_WARNING,
)

NOW = datetime(2026, 6, 17, 12, 0, 0)


def test_none_observed_at_is_not_stale():
    assert is_stale(None, now=NOW) is False


def test_fresh_metric_within_threshold():
    assert is_stale(NOW - timedelta(seconds=60), now=NOW) is False


def test_metric_stale_after_2min():
    assert is_stale(NOW - timedelta(seconds=METRIC_STALE_SECONDS + 1), now=NOW) is True


def test_resource_map_threshold_is_10min():
    five_min_ago = NOW - timedelta(minutes=5)
    # stale as a metric (>2m) but fresh as resource-map (<10m)
    assert is_stale(five_min_ago, now=NOW) is True
    assert is_stale(five_min_ago, now=NOW, threshold_seconds=RESOURCE_MAP_STALE_SECONDS) is False


def test_future_observed_at_not_stale():
    assert is_stale(NOW + timedelta(seconds=30), now=NOW) is False


def test_apply_staleness_sets_flag_and_warning():
    r = SuccessResponse(message="ok")
    observed = NOW - timedelta(seconds=METRIC_STALE_SECONDS + 1)
    apply_staleness(r, observed, now=NOW)
    assert r.is_stale is True
    assert STALE_DATA_WARNING in r.warnings
    assert r.observed_at == observed


def test_apply_staleness_fresh_no_warning():
    r = SuccessResponse(message="ok")
    apply_staleness(r, NOW - timedelta(seconds=10), now=NOW)
    assert r.is_stale is False
    assert r.warnings == []
