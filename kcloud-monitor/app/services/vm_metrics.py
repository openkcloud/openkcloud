"""
KCloud Monitor v2 — VM 사용량·전력 귀속 도메인 서비스 (libvirt exporter 기반).

데이터소스: `libvirtd-exporter`(Prometheus job `libvirtd-exporter-prometheus-libvirtd-exporter`).
  - 메트릭 접두사는 `libvirtd_domain_*` (주의: `libvirt_domain_*` 아님 — 중간에 d).
  - 조인 키는 `uuid` 라벨(= nova instance uuid = libvirt domain uuid, 동일 값).

단위(실측 확인):
  - `libvirtd_domain_cpu_time`   : 나노초(ns) 누적 → rate 후 /1e9 = 사용 코어 수.
  - `libvirtd_domain_balloon_*`  : KiB → ×1024 = bytes.
  - `libvirtd_domain_block_*`    : bytes 누적, `device` 라벨로 디스크별 → sum 필요.
  - `libvirtd_domain_net_*`      : bytes 누적, `interface` 라벨로 NIC별 → sum 필요.
  - `libvirtd_domain_domain_state`: libvirt 도메인 상태 코드(1=running).

전력 귀속(P6): 물리서버 총전력(IPMI)을 그 노드 위 VM들의 CPU 사용 비율로 배분한다.
  VM_i 전력 ≈ ipmi(node) × (VM_i CPU rate / Σ 노드 위 모든 VM CPU rate)
  ⚠️ IPMI 총전력엔 가속기 카드·팬·PSU 손실 등 VM이 직접 쓰지 않는 전력도 포함되므로,
     이 값은 "CPU 점유 기준 귀속 근사치"다(warnings=POWER_ATTRIBUTION_CPU_PROPORTIONAL).

각 서비스 함수는 `{"status": str, "data": <dict|None>, "warnings": list[str]}`를 반환한다.
"""
import re
from typing import Optional

from app.services.prometheus import first_finite, prometheus_client

# ── 메트릭 이름 (실측 확정) ────────────────────────────────────────────────
CPU_TIME_METRIC = "libvirtd_domain_cpu_time"        # ns 누적
STATE_METRIC = "libvirtd_domain_domain_state"       # 1=running
BLOCK_READ_METRIC = "libvirtd_domain_block_read_bytes"
BLOCK_WRITE_METRIC = "libvirtd_domain_block_write_bytes"
NET_RX_METRIC = "libvirtd_domain_net_rx_bytes"
NET_TX_METRIC = "libvirtd_domain_net_tx_bytes"

# 메모리(balloon) — 값은 KiB 단위
MEMORY_METRICS = {
    "rss_bytes": "libvirtd_domain_balloon_rss",
    "current_bytes": "libvirtd_domain_balloon_current",
    "available_bytes": "libvirtd_domain_balloon_available",
    "unused_bytes": "libvirtd_domain_balloon_unused",
    "usable_bytes": "libvirtd_domain_balloon_usable",
}

IPMI_POWER_METRIC = "ipmi_dcmi_power_consumption_watts"  # 라벨 node
NOVA_STATUS_METRIC = "openstack_nova_server_status"      # 조인용, 라벨 uuid/hypervisor_hostname

KIB = 1024
NS_PER_SEC = 1_000_000_000.0
DEFAULT_RATE_WINDOW = "5m"

# libvirt 도메인 상태 코드 → 이름 (virDomainState)
DOMAIN_STATE_NAMES = {
    0: "nostate",
    1: "running",
    2: "blocked",
    3: "paused",
    4: "shutdown",
    5: "shutoff",
    6: "crashed",
    7: "pmsuspended",
}

# PromQL 라벨 값 인젝션 방지 — 조인 키/노드명은 nova 출처지만 방어적으로 검증한다.
_LABEL_SAFE = re.compile(r"^[A-Za-z0-9._:\-]+$")


def _label_safe(value: Optional[str]) -> bool:
    return bool(value) and bool(_LABEL_SAFE.match(value))


async def _instant_value(query: str) -> Optional[float]:
    return first_finite(await prometheus_client.instant(query))


# ---------------------------------------------------------------------------
# VM 사용량 메트릭
# ---------------------------------------------------------------------------

async def vm_usage(uuid: str, *, rate_window: str = DEFAULT_RATE_WINDOW) -> dict:
    """단일 VM(uuid)의 CPU/메모리/디스크/네트워크 사용량 (libvirt exporter).

    uuid는 nova에서 해소된 신뢰 값이지만, PromQL 인젝션 방어로 형식을 재검증한다.
    """
    if not _label_safe(uuid):
        return {"status": "partial", "data": None, "warnings": ["INVALID_UUID"]}

    sel = f'{{uuid="{uuid}"}}'
    warnings: list[str] = []

    # CPU — rate(ns/s) → 코어 수
    cpu_rate_ns = await _instant_value(f"rate({CPU_TIME_METRIC}{sel}[{rate_window}])")
    cpu_time_total = await _instant_value(f"{CPU_TIME_METRIC}{sel}")
    cores_used = cpu_rate_ns / NS_PER_SEC if cpu_rate_ns is not None else None

    # libvirt 시계열 자체가 없으면 이 VM은 exporter 커버리지 밖 → NO_DATA
    if cpu_time_total is None:
        warnings.append("NO_DATA_LIBVIRT")

    # 메모리 — KiB → bytes
    memory: dict[str, Optional[float]] = {}
    for field, metric in MEMORY_METRICS.items():
        kib = await _instant_value(f"{metric}{sel}")
        memory[field] = kib * KIB if kib is not None else None
    # 파생: 사용 중 = current − unused
    if memory["current_bytes"] is not None and memory["unused_bytes"] is not None:
        memory["used_bytes"] = max(0.0, memory["current_bytes"] - memory["unused_bytes"])
    else:
        memory["used_bytes"] = None

    # 디스크 — device 라벨별 합
    disk = {
        "read_bytes_total": await _instant_value(f"sum({BLOCK_READ_METRIC}{sel})"),
        "write_bytes_total": await _instant_value(f"sum({BLOCK_WRITE_METRIC}{sel})"),
    }

    # 네트워크 — interface 라벨별 합
    network = {
        "rx_bytes_total": await _instant_value(f"sum({NET_RX_METRIC}{sel})"),
        "tx_bytes_total": await _instant_value(f"sum({NET_TX_METRIC}{sel})"),
    }

    # 상태
    state_code = await _instant_value(f"{STATE_METRIC}{sel}")
    state = DOMAIN_STATE_NAMES.get(int(state_code)) if state_code is not None else None

    data = {
        "state": state,
        "cpu": {"cores_used": cores_used, "cpu_time_ns_total": cpu_time_total},
        "memory": memory,
        "disk": disk,
        "network": network,
    }
    status = "partial" if warnings else "success"
    return {"status": status, "data": data, "warnings": warnings}


# ---------------------------------------------------------------------------
# VM 전력 귀속 (P6)
# ---------------------------------------------------------------------------

async def vm_attributed_power(
    uuid: str,
    host: Optional[str],
    *,
    rate_window: str = DEFAULT_RATE_WINDOW,
) -> dict:
    """물리서버 총전력(IPMI)을 노드 내 VM CPU 점유 비율로 배분한 귀속 전력(근사).

    host는 nova hypervisor_hostname(= IPMI node 라벨 값)이어야 한다.
    """
    warnings: list[str] = ["POWER_ATTRIBUTION_CPU_PROPORTIONAL"]

    if not _label_safe(uuid):
        return {"status": "partial", "data": None, "warnings": ["INVALID_UUID"]}
    if not _label_safe(host):
        warnings.append("NO_HOST")
        data = {
            "attributed_watts": None,
            "server_total_watts": None,
            "cpu_share_pct": None,
            "method": "cpu_proportional",
        }
        return {"status": "partial", "data": data, "warnings": warnings}

    # 물리서버 총전력
    server_total = await _instant_value(f'{IPMI_POWER_METRIC}{{node="{host}"}}')
    if server_total is None:
        warnings.append("NO_DATA_SERVER_POWER")

    # 이 VM의 CPU rate
    vm_rate = await _instant_value(f'rate({CPU_TIME_METRIC}{{uuid="{uuid}"}}[{rate_window}])')

    # 노드 내 모든 VM의 CPU rate 합 (uuid 조인으로 hypervisor_hostname 필터)
    node_total_expr = (
        f"sum(rate({CPU_TIME_METRIC}[{rate_window}]) "
        f"* on(uuid) group_left(hypervisor_hostname) "
        f'{NOVA_STATUS_METRIC}{{hypervisor_hostname="{host}"}})'
    )
    node_total = await _instant_value(node_total_expr)

    cpu_share: Optional[float] = None
    attributed: Optional[float] = None
    if vm_rate is None or node_total is None:
        warnings.append("NO_DATA_LIBVIRT")
    elif node_total <= 0:
        warnings.append("NODE_CPU_TOTAL_ZERO")
    else:
        cpu_share = vm_rate / node_total
        if server_total is not None:
            attributed = server_total * cpu_share

    data = {
        "attributed_watts": attributed,
        "server_total_watts": server_total,
        "cpu_share_pct": cpu_share * 100 if cpu_share is not None else None,
        "method": "cpu_proportional",
    }
    status = "partial" if any(w.startswith("NO_DATA") or w in ("NODE_CPU_TOTAL_ZERO", "NO_HOST")
                              for w in warnings) else "success"
    return {"status": status, "data": data, "warnings": warnings}
