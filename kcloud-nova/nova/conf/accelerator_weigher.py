# Copyright 2024 OpenStack Foundation
# All Rights Reserved.
#
#    Licensed under the Apache License, Version 2.0 (the "License"); you may
#    not use this file except in compliance with the License. You may obtain
#    a copy of the License at
#
#         http://www.apache.org/licenses/LICENSE-2.0
#
#    Unless required by applicable law or agreed to in writing, software
#    distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
#    WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
#    License for the specific language governing permissions and limitations
#    under the License.

from oslo_config import cfg

accelerator_weigher_group = cfg.OptGroup(
    name="accelerator_weigher",
    title="Accelerator Weigher configuration",
    help="""
Configuration options for the AcceleratorWeigher scheduler weigher.

The AcceleratorWeigher scores compute hosts based on accelerator resource
availability (GPU, FPGA, etc.) by querying the Placement API. It uses a
group-based RC+traits calculation with configurable scoring policies.
""")

accelerator_weigher_opts = [
    cfg.StrOpt(
        "rc_pattern",
        default=r"(?i)^(CUSTOM_)?(FPGA|PGPU|VGPU|QAT|NIC|SSD|AICHIP)$",
        help="""
Regex to identify accelerator resource classes included in scoring.

This pattern is matched against resource class names from Placement to
determine which resources are considered accelerators for weighing purposes.

Possible values:

* A valid Python regular expression string.

Default matches: FPGA, PGPU, VGPU, CUSTOM_QAT, CUSTOM_NIC, CUSTOM_SSD,
CUSTOM_AICHIP (case-insensitive).

Related options:

* ``[filter_scheduler] weight_classes``
"""),
    cfg.StrOpt(
        "policy",
        default="sum-fit",
        choices=["sum-fit", "product-fit"],
        help="""
Scoring policy for accelerator weighing.

This is the default policy. It can be overridden per request via the
flavor extra_spec ``accelerator_weigher:policy``.

Possible values:

* ``sum-fit``: Score is the sum of group slacks across all request groups.
  Hosts with more total free accelerator capacity score higher.
* ``product-fit``: Score is the product of (group_slack + epsilon) across
  groups. Rewards hosts that have balanced capacity across all requested
  resource types.

Related options:

* ``[filter_scheduler] weight_classes``
"""),
    cfg.FloatOpt(
        "accelerator_weight_multiplier",
        default=1.0,
        help="""
Multiplier applied to the final accelerator score.

This option determines how the accelerator weigher score influences host
selection relative to other weighers. A positive value prefers hosts with
more free accelerator resources (spreading). A negative value prefers hosts
with fewer free resources (stacking). Zero disables the weigher.

The value of this configuration option can be overridden per host aggregate
by setting the aggregate metadata key with the same name
(``accelerator_weight_multiplier``).

Possible values:

* An integer or float value, where the value corresponds to the multiplier
  ratio for this weigher.

Related options:

* ``[filter_scheduler] weight_classes``
"""),
    cfg.IntOpt(
        "usage_cache_seconds",
        default=10,
        min=0,
        help="""
TTL in seconds for cached resource provider usage data.

The AcceleratorWeigher fetches usage data from Placement for each child
resource provider. This option controls how long fetched usage data is
reused across scheduling passes before being refreshed.

Set to 0 to disable cross-pass caching (usage is still cached within
a single scheduling pass via the per-pass cache).

Possible values:

* A non-negative integer, where the integer corresponds to cache TTL
  in seconds.
"""),
    cfg.IntOpt(
        "max_concurrent_usage_requests",
        default=8,
        min=1,
        help="""
Maximum number of concurrent HTTP requests for fetching RP usages.

The AcceleratorWeigher fetches usage data for each child resource provider
under a compute host's root RP. This option controls how many of these
requests run in parallel using a thread pool.

Higher values reduce wall-clock time for hosts with many child RPs
(e.g., multiple GPUs/FPGAs) but increase concurrent load on Placement.

Possible values:

* A positive integer.
"""),
    cfg.BoolOpt(
        "trace",
        default=False,
        help="""
Emit verbose DEBUG logs for detailed flow tracing.

When enabled, the AcceleratorWeigher logs detailed information about each
step of the weighing process, including HTTP calls, provider tree traversal,
trait matching, and scoring calculations.

Possible values:

* A boolean value.
"""),
]


def register_opts(conf):
    conf.register_group(accelerator_weigher_group)
    conf.register_opts(accelerator_weigher_opts, group=accelerator_weigher_group)


def list_opts():
    return {
        accelerator_weigher_group: accelerator_weigher_opts,
    }
