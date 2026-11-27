# -*- coding: utf-8 -*-
"""
Accelerator Weigher (group-based with RC+traits; sum-fit & product-fit policies)
-----------------------------------------------------------------------------------
Scores compute hosts based on accelerator resource availability by querying the
Placement API.  Per RequestGroup calculation using RC+traits: an RP must both
hold the RC inventory AND satisfy the required traits.

Policies:
    sum-fit      : score = sum(group_slacks)
    product-fit  : score = product(group_slack + epsilon)  (epsilon avoids 0)

Tracing:
    Set [accelerator_weigher] trace = true for detailed DEBUG logs.
"""

import re
import threading
import time
import pprint
from concurrent import futures
from typing import Dict, List, Optional, Tuple, Set
from urllib.parse import quote

from oslo_log import log as logging
import nova.conf
from nova import context
from nova.compute import provider_tree
from nova.scheduler import utils
from nova.scheduler import weights
from nova.scheduler.client import report as placement_report

LOG = logging.getLogger(__name__)

CONF = nova.conf.CONF

EPS = 1e-6
UNMET_FLOOR = -1e6
VALID_POLICIES = frozenset({
    "sum-fit", "product-fit",
})

_rc_regex_cache = None
_rc_regex_pattern = None


def _get_rc_regex() -> re.Pattern:
    """Return compiled regex for rc_pattern, cached across calls."""
    global _rc_regex_cache, _rc_regex_pattern
    pattern = CONF.accelerator_weigher.rc_pattern
    if _rc_regex_cache is None or pattern != _rc_regex_pattern:
        _rc_regex_cache = re.compile(pattern)
        _rc_regex_pattern = pattern
    return _rc_regex_cache


# ------------------------------ trace helper ------------------------------

def _trace(fmt: str, *args):
    """Emit debug logs only when [accelerator_weigher] trace=True."""
    if CONF.accelerator_weigher.trace:
        LOG.debug("[ACCEL TRACE] " + fmt, *args)


# ------------------------------ Placement helpers -------------------------

def _get_client() -> placement_report.SchedulerReportClient:
    """Return the global singleton SchedulerReportClient instance."""
    client = placement_report.report_client_singleton()
    _trace("Acquired report client singleton: %r", client)
    return client


def _lookup_root_rp_uuid(
    client: placement_report.SchedulerReportClient,
    host_state,
    stats: Dict,
) -> Optional[str]:
    """Resolve compute RP UUID by hypervisor name using client.get()."""
    name = (getattr(host_state, "hypervisor_hostname", None)
            or getattr(host_state, "host", None))
    _trace("Lookup root RP for host=%r hypervisor_hostname=%r",
           getattr(host_state, "host", None), name)
    if not name:
        _trace("Host has no name; cannot lookup root RP")
        stats["errors"].append("no-hostname")
        return None

    t0 = time.time()
    url = f"/resource_providers?name={quote(name)}"
    _trace("HTTP GET %s", url)
    resp = client.get(url)
    dt = (time.time() - t0) * 1000.0
    _trace("HTTP RESP %s -> %s (%.1f ms)", url,
           getattr(resp, "status_code", None), dt)
    stats["http_calls"] = stats.get("http_calls", 0) + 1
    stats["http_times_ms"] = stats.get("http_times_ms", 0.0) + dt

    if resp.status_code != 200:
        LOG.debug("RP lookup failed for %s: %s %s",
                  name, resp.status_code, getattr(resp, "text", "?"))
        stats["errors"].append(f"rp-lookup-{resp.status_code}")
        return None

    rps = (resp.json() or {}).get("resource_providers", []) or []
    if not rps:
        stats["errors"].append("rp-lookup-empty")
        return None
    root_uuid = rps[0].get("uuid")
    _trace("Root RP uuid for %s -> %s", name, root_uuid)
    return root_uuid


def _get_provider_tree_and_usages(
    client: placement_report.SchedulerReportClient,
    root_uuid: str,
    stats: Dict,
    skip_usages: bool = False,
) -> Tuple[Optional[provider_tree.ProviderTree], Dict[str, Dict[str, float]]]:
    """Get ProviderTree and optionally collect usages from Placement.

    :param skip_usages: If True, return empty usages (caller has cached data).
    :returns: (ProviderTree, usages_dict) where usages_dict maps
              rp_uuid -> {rc: usage}.
    """
    _trace("Get ProviderTree for root=%s", root_uuid)
    ctx = context.get_context()

    t0 = time.time()
    try:
        ptree = client.get_provider_tree_and_ensure_root(ctx, root_uuid)
        stats["tree_call_ms"] = (time.time() - t0) * 1000.0
        _trace("ProviderTree returned (%.1f ms)", stats["tree_call_ms"])
    except Exception as e:
        LOG.debug("Failed to get ProviderTree: %s", e, exc_info=True)
        stats["errors"].append(f"get-tree-{type(e).__name__}")
        return None, {}

    try:
        provider_uuids = ptree.get_provider_uuids_in_tree(root_uuid)
        stats["providers_total"] = len(provider_uuids)
        _trace("Found %d providers in tree", len(provider_uuids))
    except ValueError:
        LOG.debug("Root %s not found in ProviderTree", root_uuid)
        stats["errors"].append("root-not-in-tree")
        return None, {}

    if skip_usages:
        _trace("Skipping usage fetch (caller has cached usages)")
        return ptree, {}

    # Fetch usages concurrently for all child RPs.
    child_uuids = [u for u in provider_uuids if u != root_uuid]
    if not child_uuids:
        return ptree, {}

    max_workers = min(
        CONF.accelerator_weigher.max_concurrent_usage_requests,
        len(child_uuids))
    stats_lock = threading.Lock()

    def _fetch_usage(rp_uuid):
        t0 = time.time()
        url = f"/resource_providers/{rp_uuid}/usages"
        _trace("HTTP GET %s", url)
        resp = client.get(url)
        dt = (time.time() - t0) * 1000.0
        with stats_lock:
            stats["http_calls"] = stats.get("http_calls", 0) + 1
            stats["http_times_ms"] = stats.get("http_times_ms", 0.0) + dt
        if resp.status_code == 200:
            return rp_uuid, (resp.json() or {}).get("usages", {}) or {}
        return rp_uuid, {}

    _trace("Fetching usages for %d child RPs (max_workers=%d)",
           len(child_uuids), max_workers)
    t_usage = time.time()
    usages_dict = {}
    with futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
        for rp_uuid, usages in executor.map(_fetch_usage, child_uuids):
            if usages:
                usages_dict[rp_uuid] = usages
    stats["usage_fetch_ms"] = (time.time() - t_usage) * 1000.0
    _trace("Usage fetch complete: %d RPs in %.1f ms",
           len(usages_dict), stats["usage_fetch_ms"])

    return ptree, usages_dict


# ------------------------------ Request parsing ---------------------------

def _extract_accel_groups(
    weight_properties,
    rc_regex: re.Pattern,
    stats: Dict,
) -> List[Tuple[Dict[str, int], Set[str]]]:
    """Extract accelerator-related request groups from RequestSpec.

    Returns: [(resources{rc: amount}, required_traits_set), ...]
    """
    groups_out: List[Tuple[Dict[str, int], Set[str]]] = []

    groups = getattr(weight_properties, "requested_resources", None) or []
    stats["groups_seen"] = len(groups)
    _trace("Extract groups: found %d request groups", len(groups))

    for idx, g in enumerate(groups):
        resources = getattr(g, "resources", None) or {}
        accel_resources = {}
        for rc, amount in resources.items():
            if rc_regex.match(rc):
                try:
                    accel_resources[rc] = int(amount)
                except (ValueError, TypeError):
                    _trace("Group[%d] RC=%s invalid amount=%r; skipping",
                           idx, rc, amount)
                    stats["invalid_amounts"] = (
                        stats.get("invalid_amounts", 0) + 1)

        req_traits = getattr(g, "required_traits", None) or set()
        if isinstance(req_traits, list):
            req_traits = set(req_traits)

        if accel_resources:
            groups_out.append((accel_resources, req_traits))
            _trace("Group[%d] accel_resources=%s required_traits=%s",
                   idx, accel_resources, sorted(req_traits))
        else:
            _trace("Group[%d] skipped (no accel RC)", idx)
            stats["groups_skipped_no_accel"] = (
                stats.get("groups_skipped_no_accel", 0) + 1)

    stats["groups_accel"] = len(groups_out)
    return groups_out


# ------------------------------ RC+traits computation ---------------------

def _sum_free_for_rc_with_traits(
    ptree: provider_tree.ProviderTree,
    root_uuid: str,
    rc_name: str,
    required_traits: Set[str],
    usages_dict: Dict[str, Dict[str, float]],
    stats: Dict,
    provider_uuids: Optional[List[str]] = None,
) -> float:
    """Sum free units for (RC + required_traits) across child RPs."""
    _trace("Sum free: RC=%s traits=%s", rc_name, sorted(required_traits))
    total_free = 0.0

    if provider_uuids is None:
        try:
            provider_uuids = ptree.get_provider_uuids_in_tree(root_uuid)
        except ValueError:
            _trace("Root %s not found in tree", root_uuid)
            stats["errors"].append("root-not-in-tree")
            return 0.0

    stats["providers_iterated"] = (
        stats.get("providers_iterated", 0) + len(provider_uuids))

    for rp_uuid in provider_uuids:
        if rp_uuid == root_uuid:
            continue

        try:
            prov_data = ptree.data(rp_uuid)
        except ValueError:
            continue

        # Skip RPs without the requested RC
        inv = prov_data.inventory.get(rc_name)
        if not inv:
            stats["rps_skipped_no_rc"] = (
                stats.get("rps_skipped_no_rc", 0) + 1)
            continue

        # Skip RPs missing required traits
        if required_traits and not required_traits.issubset(prov_data.traits):
            stats["rps_skipped_traits"] = (
                stats.get("rps_skipped_traits", 0) + 1)
            continue

        # Compute free = total - reserved - used (floor at 0)
        total = float(inv.get("total", 0))
        reserved = float(inv.get("reserved", 0))
        used = float(usages_dict.get(rp_uuid, {}).get(rc_name, 0))
        free = max(total - reserved - used, 0.0)

        if free > 0:
            total_free += free
            _trace("RP=%s RC=%s free=%.3f (total=%.0f reserved=%.0f "
                   "used=%.0f) running_total=%.3f",
                   rp_uuid, rc_name, free, total, reserved, used, total_free)
            stats["free_contribs"] = stats.get("free_contribs", 0) + 1

    _trace("Total free for RC=%s -> %.3f", rc_name, total_free)
    return total_free


def _group_slack(
    ptree: provider_tree.ProviderTree,
    root_uuid: str,
    accel_resources: Dict[str, int],
    required_traits: Set[str],
    usages_dict: Dict[str, Dict[str, float]],
    stats: Dict,
    provider_uuids: Optional[List[str]] = None,
) -> float:
    """Compute group slack: sum of (free_rc - required_rc) over all RCs."""
    _trace("group_slack: resources=%s traits=%s",
           accel_resources, sorted(required_traits))

    if not accel_resources:
        _trace("No RCs in group -> UNMET_FLOOR")
        return UNMET_FLOOR

    slacks: List[float] = []
    for rc, amount in accel_resources.items():
        free_total = _sum_free_for_rc_with_traits(
            ptree, root_uuid, rc, required_traits, usages_dict,
            stats=stats, provider_uuids=provider_uuids)
        slack = free_total - float(amount)
        _trace("RC=%s required=%d free=%.3f slack=%.3f",
               rc, amount, free_total, slack)
        slacks.append(slack)

    gs = sum(slacks)
    _trace("group_slack -> %.3f", gs)
    return gs


# ------------------------------ Main weigher ------------------------------

class AcceleratorWeigher(weights.BaseHostWeigher):
    """Weigher scoring hosts by accelerator capacity using Placement data."""

    minval = 0

    def __init__(self):
        super().__init__()
        self._pass_cache = {}    # {root_uuid: (ptree, usages_dict)}
        self._usage_cache = {}   # {root_uuid: (timestamp, usages_dict)}

    def weigh_objects(self, weighed_obj_list, weight_properties):
        """Clear per-pass cache before weighing a new set of hosts."""
        self._pass_cache = {}
        return super().weigh_objects(weighed_obj_list, weight_properties)

    def weight_multiplier(self, host_state):
        """Return multiplier, allowing per-aggregate override."""
        return utils.get_weight_multiplier(
            host_state, 'accelerator_weight_multiplier',
            CONF.accelerator_weigher.accelerator_weight_multiplier)

    # -- Private helpers --------------------------------------------------

    def _resolve_root_uuid(self, host_state, client, stats):
        """Return root RP UUID from host_state.uuid or HTTP lookup."""
        root_uuid = getattr(host_state, 'uuid', None)
        if root_uuid:
            _trace("Using host_state.uuid=%s as root RP UUID", root_uuid)
            stats["root_rp_source"] = "host_state.uuid"
        else:
            root_uuid = _lookup_root_rp_uuid(client, host_state, stats=stats)
            stats["root_rp_source"] = "http_lookup"
        return root_uuid

    def _fetch_data(self, root_uuid, client, stats):
        """Fetch ProviderTree + usages with 3-layer cache.

        Cache layers:
          1. Per-pass cache (within a single weigh_objects call)
          2. TTL cache (across weigh_objects calls, usage data only)
          3. Placement API (concurrent ThreadPoolExecutor)

        Returns: (ptree, usages_dict) or (None, {}) on failure.
        """
        # Layer 1: per-pass cache
        if root_uuid in self._pass_cache:
            stats["cache_hit"] = "pass"
            _trace("Pass-cache hit for root_uuid=%s", root_uuid)
            return self._pass_cache[root_uuid]

        # Layer 2: TTL cache (usage data only; tree is always refreshed)
        ttl = CONF.accelerator_weigher.usage_cache_seconds
        cached_entry = self._usage_cache.get(root_uuid)
        if cached_entry and ttl > 0 and (time.time() - cached_entry[0]) < ttl:
            stats["cache_hit"] = "ttl"
            _trace("TTL-cache hit for root_uuid=%s (age=%.1fs, ttl=%ds)",
                   root_uuid, time.time() - cached_entry[0], ttl)
            ptree, _ = _get_provider_tree_and_usages(
                client, root_uuid, stats=stats, skip_usages=True)
            usages_dict = cached_entry[1]
        else:
            # Layer 3: full Placement fetch
            stats["cache_hit"] = False
            ptree, usages_dict = _get_provider_tree_and_usages(
                client, root_uuid, stats=stats)
            if ptree and ttl > 0:
                self._usage_cache[root_uuid] = (time.time(), usages_dict)

        if ptree:
            self._pass_cache[root_uuid] = (ptree, usages_dict)

        return ptree, usages_dict

    def _get_policy(self, weight_properties):
        """Resolve scoring policy: flavor extra_spec override or config default.

        Flavor extra_spec key: ``accelerator_weigher:policy``
        """
        flavor = getattr(weight_properties, 'flavor', None)
        if flavor:
            extra_specs = getattr(flavor, 'extra_specs', None) or {}
            override = extra_specs.get('accelerator_weigher:policy')
            if override and override in VALID_POLICIES:
                _trace("Policy override from flavor extra_spec: %s", override)
                return override
        return CONF.accelerator_weigher.policy

    def _apply_policy(self, group_slacks, policy=None):
        """Compute final score from group slacks using the given policy.

        Supported policies: sum-fit, product-fit.

        Returns: float score, or UNMET_FLOOR if any group is unmet.
        """
        if policy is None:
            policy = CONF.accelerator_weigher.policy

        if any(gs == UNMET_FLOOR for gs in group_slacks):
            return UNMET_FLOOR

        if policy == "sum-fit":
            score = sum(group_slacks)
        elif policy == "product-fit":
            score = 1.0
            for gs in group_slacks:
                score *= (gs + EPS)
        else:
            score = sum(group_slacks)  # fallback

        _trace("policy=%s slacks=%s -> score=%.6f",
               policy, group_slacks, score)
        return float(score)

    def _build_stats(self, stats, host_name, root_uuid, group_slacks,
                     score, t_start):
        """Populate final stats summary and emit trace."""
        stats.update({
            "final_score": score,
            "host": host_name,
            "root_rp": root_uuid,
            "policy": CONF.accelerator_weigher.policy,
            "multiplier": CONF.accelerator_weigher.accelerator_weight_multiplier,
            "groups_slacks": group_slacks,
            "duration_ms": (time.time() - t_start) * 1000.0,
            "rc_pattern": CONF.accelerator_weigher.rc_pattern,
        })
        _trace("STATS SUMMARY:\n%s", pprint.pformat(stats))

    # -- Core weighing ----------------------------------------------------

    def _weigh_object(self, host_state, weight_properties):
        """Score a host based on accelerator capacity and configured policy.

        Pipeline:
          1. Resolve root RP UUID
          2. Extract accelerator request groups
          3. Fetch ProviderTree + usages (with caching)
          4. Compute per-group slack
          5. Apply scoring policy
        """
        stats: Dict = {
            "http_calls": 0,
            "http_times_ms": 0.0,
            "errors": [],
        }
        host_name = getattr(host_state, "host", "?")
        t_start = time.time()

        _trace("==== weigh_object START host=%r ====", host_name)

        # 1. Resolve root RP UUID
        client = _get_client()
        root_uuid = self._resolve_root_uuid(host_state, client, stats)
        if not root_uuid:
            LOG.debug("No root RP for host=%s; returning 0", host_name)
            return 0.0

        # 2. Resolve policy (flavor extra_spec override or config default)
        policy = self._get_policy(weight_properties)

        # 3. Extract accelerator request groups
        rc_regex = _get_rc_regex()
        groups = _extract_accel_groups(weight_properties, rc_regex, stats=stats)
        if not groups:
            LOG.debug("No accelerator request groups; host=%s returns 0",
                      host_name)
            return 0.0

        # 4. Fetch ProviderTree + usages
        ptree, usages_dict = self._fetch_data(root_uuid, client, stats)
        if not ptree:
            LOG.debug("Failed to build ProviderTree for host=%s; returning 0",
                      host_name)
            return 0.0

        # Pre-fetch provider UUIDs once for all groups
        try:
            provider_uuids = ptree.get_provider_uuids_in_tree(root_uuid)
        except ValueError:
            LOG.debug("Root %s not found in ProviderTree", root_uuid)
            return 0.0

        # 5. Compute per-group slacks and apply the scoring policy
        group_slacks = []
        for idx, (accel_resources, req_traits) in enumerate(groups):
            _trace("Processing group[%d] on host=%s", idx, host_name)
            gs = _group_slack(
                ptree, root_uuid, accel_resources, req_traits,
                usages_dict, stats=stats, provider_uuids=provider_uuids)
            group_slacks.append(gs)
            _trace("group[%d] -> slack=%.3f", idx, gs)
        score = self._apply_policy(group_slacks, policy=policy)

        LOG.debug(
            "AcceleratorWeigher host=%s root_rp=%s policy=%s "
            "groups=%d slacks=%s score=%.6f",
            host_name, root_uuid, policy,
            len(group_slacks), group_slacks, score)

        self._build_stats(
            stats, host_name, root_uuid, group_slacks, score, t_start)
        _trace("==== weigh_object END host=%r score=%.6f ====",
               host_name, score)
        return score
