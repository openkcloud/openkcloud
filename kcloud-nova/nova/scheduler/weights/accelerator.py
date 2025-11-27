# -*- coding: utf-8 -*-
"""
Accelerator Weigher (group-based with RC+traits; sum-fit & product-fit policies)
-----------------------------------------------------------------------------------
- Per RequestGroup calculation using Placement data.
- "RC+traits" basis: an RP must both hold the RC inventory AND satisfy traits.
- Policies:
    sum-fit      : sum of per-group sum of RC slacks -> score = sum(sum)
    product-fit  : sum of per-group product of RC slacks -> score = sum(product with epsilon)
- Placement access via Nova's SchedulerReportClient singleton.

Tracing:
- Set [accelerator_weigher] trace = true to emit very detailed DEBUG logs.
- A per-call 'stats' dict is collected and dumped at the end for quick overview.
"""

import re
import time
import pprint
from typing import Dict, List, Optional, Tuple, Set

from oslo_config import cfg
from oslo_log import log as logging
from nova import context
from nova.compute import provider_tree
from nova.scheduler import weights
from nova.scheduler.client import report as placement_report

LOG = logging.getLogger(__name__)

_ACCEL_OPTS = [
    cfg.StrOpt(
        "rc_pattern",
        default=r"(?i)^(CUSTOM_)?(FPGA|PGPU|VGPU|QAT|NIC|SSD|AICHIP)$",
        help=(
            "Regex to identify accelerator RCs included in scoring. "
            "Matches: FPGA, PGPU, VGPU, CUSTOM_QAT, CUSTOM_NIC, CUSTOM_SSD, CUSTOM_AICHIP."
        ),
    ),
    cfg.StrOpt(
        "policy",
        default="sum-fit",
        choices=["sum-fit", "product-fit"],
        help=("Scoring policy: "
              "sum-fit (sum of per-group sum of RC slacks) or "
              "product-fit (sum of per-group product of RC slacks)."),
    ),
    cfg.FloatOpt(
        "accelerator_weight_multiplier",
        default=1.0,
        help="Multiplier applied to the final accelerator score.",
    ),
    cfg.BoolOpt(
        "trace",
        default=False,
        help="Emit verbose DEBUG logs for detailed flow tracing.",
    ),
]

CONF = cfg.CONF
CONF.register_opts(_ACCEL_OPTS, group="accelerator_weigher")

EPS = 1e-6
UNMET_FLOOR = float("-inf")

# ------------------------------ trace helper ------------------------------

def _trace(fmt: str, *args):
    """Emit debug logs only when [accelerator_weigher] trace=True."""
    if CONF.accelerator_weigher.trace:
        LOG.debug("[ACCEL TRACE] " + fmt, *args)


# ------------------------------ Placement client helpers ------------------------------

def _get_client() -> placement_report.SchedulerReportClient:
    """Return the global singleton SchedulerReportClient instance."""
    client = placement_report.report_client_singleton()
    _trace("Acquired report client singleton: %r", client)
    return client




def _lookup_root_rp_uuid(client: placement_report.SchedulerReportClient, host_state, stats: Dict) -> Optional[str]:
    """Resolve compute RP UUID by hypervisor name using client.get()."""
    name = getattr(host_state, "hypervisor_hostname", None) or getattr(host_state, "host", None)
    _trace("Lookup root RP for host=%r hypervisor_hostname=%r", getattr(host_state, "host", None), name)
    if not name:
        _trace("Host has no name; cannot lookup root RP")
        stats["errors"].append("no-hostname")
        return None

    t0 = time.time()
    url = f"/resource_providers?name={name}"
    _trace("HTTP GET %s", url)
    resp = client.get(url)
    dt = (time.time() - t0) * 1000.0
    code = getattr(resp, "status_code", None)
    _trace("HTTP RESP %s -> %s (%.1f ms)", url, code, dt)
    stats["http_calls"] = stats.get("http_calls", 0) + 1
    stats["http_times_ms"] = stats.get("http_times_ms", 0.0) + dt

    if resp.status_code != 200:
        LOG.debug("RP lookup failed for %s: %s %s", name, resp.status_code, getattr(resp, "text", "?"))
        stats["errors"].append(f"rp-lookup-{resp.status_code}")
        return None
    rps = (resp.json() or {}).get("resource_providers", []) or []
    root_uuid = rps[0].get("uuid") if rps else None
    _trace("Root RP uuid for %s -> %s", name, root_uuid)
    if not rps:
        stats["errors"].append("rp-lookup-empty")
    return root_uuid


def _get_provider_tree_and_usages(client: placement_report.SchedulerReportClient, root_uuid: str, stats: Dict) -> Tuple[Optional[provider_tree.ProviderTree], Dict[str, Dict[str, float]]]:
    """Get ProviderTree using get_provider_tree_and_ensure_root() and collect usages.

    Returns:
        Tuple of (ProviderTree, usages_dict) where usages_dict maps rp_uuid -> {rc: usage}
    """
    _trace("Get ProviderTree for root=%s using get_provider_tree_and_ensure_root", root_uuid)

    # Create a minimal context for the API call
    ctx = context.get_context()

    # Get ProviderTree with all inventories and traits populated
    t0 = time.time()
    try:
        ptree = client.get_provider_tree_and_ensure_root(ctx, root_uuid)
        dt = (time.time() - t0) * 1000.0
        stats["tree_call_ms"] = dt
        _trace("get_provider_tree_and_ensure_root returned ProviderTree (%.1f ms)", dt)
    except Exception as e:
        LOG.debug("Failed to get ProviderTree: %s", e, exc_info=True)
        stats["errors"].append(f"get-tree-{type(e).__name__}")
        return None, {}

    # Get provider UUIDs in tree
    try:
        provider_uuids = ptree.get_provider_uuids_in_tree(root_uuid)
        stats["providers_total"] = len(provider_uuids)
        _trace("Found %d providers in tree", len(provider_uuids))
    except ValueError:
        LOG.debug("Root %s not found in ProviderTree", root_uuid)
        stats["errors"].append("root-not-in-tree")
        return None, {}

    # Collect usage information (not included in ProviderTree)
    usages_dict = {}  # rp_uuid -> {rc: usage}
    for rp_uuid in provider_uuids:
        if rp_uuid == root_uuid:
            continue

        t0 = time.time()
        usage_url = f"/resource_providers/{rp_uuid}/usages"
        _trace("HTTP GET %s", usage_url)
        usage_resp = client.get(usage_url)
        dt = (time.time() - t0) * 1000.0
        stats["http_calls"] = stats.get("http_calls", 0) + 1
        stats["http_times_ms"] = stats.get("http_times_ms", 0.0) + dt

        if usage_resp.status_code == 200:
            usages = (usage_resp.json() or {}).get("usages", {}) or {}
            usages_dict[rp_uuid] = usages

    return ptree, usages_dict


def _get_free_for_rc_from_tree(ptree: provider_tree.ProviderTree, rp_uuid: str, rc_name: str, usages_dict: Dict[str, Dict[str, float]], stats: Dict) -> float:
    """Compute free units for RC on RP using ProviderTree data: max(total - reserved - used, 0)."""
    _trace("Compute free for RP=%s RC=%s", rp_uuid, rc_name)

    try:
        prov_data = ptree.data(rp_uuid)
    except ValueError:
        _trace("RP=%s not found in tree", rp_uuid)
        stats["rc_miss"] = stats.get("rc_miss", 0) + 1
        return 0.0

    # Get inventory from ProviderTree
    inventory = prov_data.inventory.get(rc_name)
    if not inventory:
        _trace("RP=%s has no inventory for RC=%s", rp_uuid, rc_name)
        stats["rc_miss"] = stats.get("rc_miss", 0) + 1
        return 0.0

    total = float(inventory.get("total", 0))
    reserved = float(inventory.get("reserved", 0))
    _trace("Inventory RP=%s RC=%s total=%.3f reserved=%.3f", rp_uuid, rc_name, total, reserved)

    # Get usage from usages_dict
    used = float(usages_dict.get(rp_uuid, {}).get(rc_name, 0))
    _trace("Usage RP=%s RC=%s used=%.3f", rp_uuid, rc_name, used)

    free = total - reserved - used
    free = free if free > 0 else 0.0
    _trace("Free RP=%s RC=%s -> %.3f", rp_uuid, rc_name, free)
    if free > 0:
        stats["free_contribs"] = stats.get("free_contribs", 0) + 1
    return free


# ------------------------------ Request parsing ------------------------------

def _extract_accel_groups(weight_properties, rc_regex: re.Pattern, stats: Dict) -> List[Tuple[Dict[str, int], Set[str]]]:
    """Extract accelerator-related request groups from RequestSpec.

    weight_properties is a RequestSpec object with requested_resources field.
    Returns: [(resources{rc:amount}, required_traits_set), ...]
    """
    groups_out: List[Tuple[Dict[str, int], Set[str]]] = []

    # weight_properties is already a RequestSpec object
    groups = getattr(weight_properties, "requested_resources", None) or []
    stats["groups_seen"] = len(groups)
    _trace("Extract groups: found %d request groups", len(groups))

    for idx, g in enumerate(groups):
        # RequestGroup has 'resources' (dict) and 'required_traits' (set) fields
        resources = getattr(g, "resources", None) or {}
        accel_resources = {}
        for rc, amount in resources.items():
            if rc_regex.match(rc):
                try:
                    accel_resources[rc] = int(amount)
                except (ValueError, TypeError):
                    _trace("Group[%d] RC=%s invalid amount=%r; skipping", idx, rc, amount)
                    stats["invalid_amounts"] = stats.get("invalid_amounts", 0) + 1

        # required_traits is already a set in RequestGroup
        req_traits = getattr(g, "required_traits", None) or set()
        if isinstance(req_traits, list):
            req_traits = set(req_traits)

        if accel_resources:
            groups_out.append((accel_resources, req_traits))
            _trace("Group[%d] accel_resources=%s required_traits=%s", idx, accel_resources, sorted(req_traits))
        else:
            _trace("Group[%d] skipped (no accel RC)", idx)
            stats["groups_skipped_no_accel"] = stats.get("groups_skipped_no_accel", 0) + 1

    stats["groups_accel"] = len(groups_out)
    return groups_out


# ------------------------------ RC+traits computations ------------------------------

def _sum_free_for_rc_with_traits(
    ptree: provider_tree.ProviderTree,
    root_uuid: str,
    rc_name: str,
    required_traits: Set[str],
    usages_dict: Dict[str, Dict[str, float]],
    stats: Dict,
) -> float:
    """Sum free units for (RC + required_traits) across child RPs using ProviderTree."""
    _trace("Sum free across tree: RC=%s required_traits=%s", rc_name, sorted(required_traits))
    total_free = 0.0

    # Get all provider UUIDs in tree (excluding root)
    try:
        provider_uuids = ptree.get_provider_uuids_in_tree(root_uuid)
    except ValueError:
        _trace("Root %s not found in tree", root_uuid)
        stats["errors"].append("root-not-in-tree")
        return 0.0

    stats["providers_iterated"] = stats.get("providers_iterated", 0) + len(provider_uuids)
    _trace("Traversing %d providers under root=%s", len(provider_uuids), root_uuid)

    for rp_uuid in provider_uuids:
        if rp_uuid == root_uuid:
            continue

        try:
            prov_data = ptree.data(rp_uuid)
        except ValueError:
            _trace("Skip RP=%s (not found in tree)", rp_uuid)
            continue

        # 1) Check if RC inventory exists
        if rc_name not in prov_data.inventory:
            _trace("Skip RP=%s (no RC=%s)", rp_uuid, rc_name)
            stats["rps_skipped_no_rc"] = stats.get("rps_skipped_no_rc", 0) + 1
            continue

        # 2) Traits must satisfy all required traits
        if required_traits and not required_traits.issubset(prov_data.traits):
            _trace("Skip RP=%s (traits missing) need=%s have=%s", rp_uuid, sorted(required_traits), sorted(prov_data.traits))
            stats["rps_skipped_traits"] = stats.get("rps_skipped_traits", 0) + 1
            continue

        # 3) Free contribution
        free = _get_free_for_rc_from_tree(ptree, rp_uuid, rc_name, usages_dict, stats=stats)
        if free > 0:
            total_free += free
            _trace("Accum free RC=%s RP=%s +%.3f -> total=%.3f", rc_name, rp_uuid, free, total_free)
            stats["free_sum_updates"] = stats.get("free_sum_updates", 0) + 1
        else:
            stats["free_nonpos"] = stats.get("free_nonpos", 0) + 1

    _trace("Total free for RC=%s with traits=%s -> %.3f", rc_name, sorted(required_traits), total_free)
    return total_free


def _group_slack(
    ptree: provider_tree.ProviderTree,
    root_uuid: str,
    accel_resources: Dict[str, int],
    required_traits: Set[str],
    usages_dict: Dict[str, Dict[str, float]],
    stats: Dict,
) -> float:
    """Compute group slack: min over RC ( total_free_rc - required_rc )."""
    _trace("Compute group_slack for resources=%s traits=%s", accel_resources, sorted(required_traits))
    slacks: List[float] = []
    for rc, amount in accel_resources.items():
        free_total = _sum_free_for_rc_with_traits(ptree, root_uuid, rc, required_traits, usages_dict, stats=stats)
        slack = free_total - float(amount)
        _trace("RC=%s required=%.3f free_total=%.3f slack=%.3f", rc, float(amount), free_total, slack)
        slacks.append(slack)
    if not slacks:
        _trace("No RCs in group -> slack=0")
        return UNMET_FLOOR
    gs = sum(slacks)
    _trace("group_slack (sum over RC) -> %.3f", gs)
    return gs


def _group_product(
    ptree: provider_tree.ProviderTree,
    root_uuid: str,
    accel_resources: Dict[str, int],
    required_traits: Set[str],
    usages_dict: Dict[str, Dict[str, float]],
    stats: Dict,
) -> float:
    """Return product of per-RC slacks for a group (0 if any RC unmet)."""
    _trace("Compute group_product for resources=%s traits=%s", accel_resources, sorted(required_traits))
    prod = 1.0
    for rc, amount in accel_resources.items():
        free_total = _sum_free_for_rc_with_traits(ptree, root_uuid, rc, required_traits, usages_dict, stats=stats)
        slack = free_total - float(amount)
        _trace("RC=%s required=%.3f free_total=%.3f slack=%.3f", rc, float(amount), free_total, slack)
        if slack < 0:
            _trace("group_product -> 0 (unmet RC=%s; slack<0)", rc)
            stats["groups_product_unmet"] = stats.get("groups_product_unmet", 0) + 1
            return 0.0
        prod *= (slack + EPS)
    _trace("group_product (eps=%.6g) -> %.6f", EPS, prod)
    return prod


# ------------------------------ Main weigher ------------------------------

class AcceleratorWeigher(weights.BaseHostWeigher):
    """Weigher scoring hosts using group-based RC+traits calculation and chosen policy."""

    def weight_multiplier(self, host_state):
        return CONF.accelerator_weigher.accelerator_weight_multiplier

    def _weigh_object(self, host_state, weight_properties):
        """Score host per policy; any unmet group yields score=0 (no favor)."""
        # Per-call stats collector
        stats: Dict = {
            "http_calls": 0,
            "http_times_ms": 0.0,
            "http_codes": {},
            "notes": [],
            "errors": [],
        }

        host_name = getattr(host_state, "host", "?")
        _trace("==== weigh_object START host=%r policy=%s rc_pattern=%r mult=%.3f trace=%s ====",
               host_name,
               CONF.accelerator_weigher.policy,
               CONF.accelerator_weigher.rc_pattern,
               CONF.accelerator_weigher.accelerator_weight_multiplier,
               CONF.accelerator_weigher.trace)

        t_start = time.time()
        client = _get_client()
        rc_regex = re.compile(CONF.accelerator_weigher.rc_pattern)

        root_uuid = _lookup_root_rp_uuid(client, host_state, stats=stats)
        if not root_uuid:
            LOG.debug("No root RP for host=%s; returning 0", host_name)
            _trace("==== weigh_object END (no root RP) host=%r ====", host_name)
            return 0.0

        groups = _extract_accel_groups(weight_properties, rc_regex, stats=stats)
        if not groups:
            LOG.debug("No accelerator request groups; host=%s returns 0", host_name)
            _trace("==== weigh_object END (no accel groups) host=%r ====", host_name)
            return 0.0

        # Get ProviderTree once and reuse for all groups
        ptree, usages_dict = _get_provider_tree_and_usages(client, root_uuid, stats=stats)
        if not ptree:
            LOG.debug("Failed to build ProviderTree for host=%s; returning 0", host_name)
            _trace("==== weigh_object END (no ProviderTree) host=%r ====", host_name)
            return 0.0

        group_slacks: List[float] = []
        group_products: List[float] = []
        any_unmet = False

        for idx, (accel_resources, req_traits) in enumerate(groups):
            _trace("Processing group[%d] on host=%s ...", idx, host_name)
            gs = _group_slack(ptree, root_uuid, accel_resources, req_traits, usages_dict, stats=stats)
            gp = _group_product(ptree, root_uuid, accel_resources, req_traits, usages_dict, stats=stats)
            group_slacks.append(gs)
            group_products.append(gp)
            _trace("group[%d] -> slack=%.3f product=%.6f", idx, gs, gp)

        # Unmet if any group's slack == UNMET_FLOOR (product may be small but non-zero due to epsilon)
        if any(gs == UNMET_FLOOR for gs in group_slacks):
            LOG.debug(
                "Group unmet; host=%s slacks=%s products=%s -> score=%f",
                getattr(host_state, "host", "?"), group_slacks, group_products
            )
            # Summary stats
            stats["final_score"] = UNMET_FLOOR
            stats["host"] = host_name
            stats["root_rp"] = root_uuid
            stats["policy"] = CONF.accelerator_weigher.policy
            stats["multiplier"] = CONF.accelerator_weigher.accelerator_weight_multiplier
            stats["groups_slacks"] = group_slacks
            stats["groups_products"] = group_products
            stats["duration_ms"] = (time.time() - t_start) * 1000.0
            _trace("STATS SUMMARY:\n%s", pprint.pformat(stats))
            _trace("==== weigh_object END (unmet group -> score=%f) host=%r ====", UNMET_FLOOR, host_name)
            return UNMET_FLOOR

        policy = CONF.accelerator_weigher.policy
        if policy in ("sum-fit"):
            total_slack = sum(gs for gs in group_slacks if gs > 0)
            score = total_slack
            _trace("policy=%s total_slack=%.6f score(before mult)=%.6f", policy, total_slack, score)
        else:  # product-fit
            total_prod = sum(group_products)
            score = total_prod
            _trace("policy=%s total_product=%.6f score(before mult)=%.6f", policy, total_prod, score)

        LOG.debug(
            "AcceleratorWeigher host=%s root_rp=%s policy=%s groups=%d slacks=%s products=%s score=%.6f",
            host_name,
            root_uuid,
            policy,
            len(group_slacks),
            group_slacks,
            group_products,
            float(score),
        )

        # Summarize stats (helps analyze call overhead and skip reasons)
        stats["final_score"] = float(score)
        stats["host"] = host_name
        stats["root_rp"] = root_uuid
        stats["policy"] = policy
        stats["multiplier"] = CONF.accelerator_weigher.accelerator_weight_multiplier
        stats["groups_slacks"] = group_slacks
        stats["groups_products"] = group_products
        stats["duration_ms"] = (time.time() - t_start) * 1000.0
        stats["rc_pattern"] = CONF.accelerator_weigher.rc_pattern

        _trace("STATS SUMMARY:\n%s", pprint.pformat(stats))
        _trace("==== weigh_object END host=%r score=%.6f ====", host_name, float(score))
        return float(score)

