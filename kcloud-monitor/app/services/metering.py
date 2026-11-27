"""
KCloud Metering — 미터링, 단가 정책, 비용 예측.

다섯 부품으로 구성된다.
  1. 단가 정책  : 자원 유형 x 벤더 x 모델 x 계약 형태별 단가와 유효 구간. 덮어쓰지 않고 새 버전을 추가
  2. 미터링 엔진: 매시 가속기 카드별 점유, 사용, 가중 시간과 전력량을 계측해 사용량 레코드(불변)로 적재
  3. 비용 계산  : 사용량 x 그날 유효 단가. 이동 평균, 선형 추세 예측, 예산 판정, 가상 시나리오, 단가 시뮬레이션.
                  순수 함수라 DB, Prometheus 없이 검증
  4. 저장소     : PostgreSQL. 사용량 레코드, 계측 이력, 단가 정책, 예산, 예측 이력
  5. 조회 조립  : 라우터가 쓰는 사용량 집계, 월말 예측, 예산 현황

사용량은 사실이고 비용은 정책의 함수라, 단가가 바뀌어도 사용량 레코드는 다시 계산하지 않는다.
DB 가 없으면 계측과 정책 저장은 꺼지고, 예측만 Prometheus 에서 점유 시간을 바로 세는 임시 경로로 동작한다.
청구 기본 단위는 점유 시간(allocated hours), 과금 월과 일의 경계는 한국 시간(KST).
예측까지가 범위이고, 예측을 근거로 자원을 재배치하는 최적화는 이 모듈 범위 밖이다.
"""
from __future__ import annotations

import asyncio
import calendar
import json
import logging
import math
import re
import statistics
import uuid
from collections import defaultdict
from dataclasses import asdict, dataclass, replace
from datetime import date, datetime, time, timedelta, timezone
from typing import Callable, Optional

from app.config import npu_util, settings
from app.services.cluster_discovery import DEFAULT_CLUSTER_NAME, cluster_discovery
from app.services.prometheus import parse_finite, prometheus_client

logger = logging.getLogger(__name__)

KST = timezone(timedelta(hours=9))
RETENTION_DAYS = 10  # Prometheus 보관 기간. 이보다 앞선 구간은 계측할 수 없어 결측으로 둠


# ---------------------------------------------------------------------------
# 1. 단가 정책
# ---------------------------------------------------------------------------

# 과금 자원 유형. 값은 (단위, 지금 계측 여부, 설명)
RESOURCE_TYPES: dict[str, tuple[str, bool, str]] = {
    "accelerator": ("가속기·시간", True, "GPU, NPU 카드 1장"),
    "cpu": ("vCPU·시간", False, "가상 CPU"),
    "memory": ("GB·시간", False, "메모리"),
    "storage": ("GB·일", False, "블록, 오브젝트 스토리지"),
    "network": ("GB", False, "외부 전송량"),
    "power": ("kWh", False, "전력량. 상용 클라우드 관행이 아닌 전력 효율 분석용 확장 항목"),
}
BILLING_UNITS = {
    "allocated_hours": "점유 시간. 할당돼 있던 시간 (기본, 상용 클라우드 표준)",
    "usage_hours": "사용 시간. 사용률이 0보다 큰 시간 (유휴 미과금)",
    "weighted_hours": "사용률 가중 시간. 사용 시간 x 평균 사용률 (내부 배분용)",
}
CONTRACT_TYPES = {"on_demand": "온디맨드", "reserved": "약정", "spot": "선점형"}
USD_KRW = 1324.6  # 2026-10-23 서울 외환시장 주간 종가 (초기 단가 조회일 환율)


@dataclass(frozen=True)
class PricePolicy:
    id: str
    name: str
    resource_type: str
    vendor: str  # 빈 문자열이면 벤더 무관
    model: str  # 모델 이름에 단어로 들어 있는 글자 (model_matches). 빈 문자열이면 벤더 전체
    unit_price_krw: float  # billing_unit 1단위(가속기는 1장·1시간) 단가
    effective_from: date
    effective_to: Optional[date] = None  # 이 날 전까지 유효. None 이면 계속
    contract_type: str = "on_demand"
    billing_unit: str = "allocated_hours"
    source: str = ""


# 초기 단가 (DB 첫 기동 때 시드, DB 가 없으면 이 값으로 계산). 화면이나 API 로 새 버전을 추가해 바꾼다.
# GPU: 시장 온디맨드 중앙값(getdeploying.com, 2026-10-23) x 조회일 환율.
# NPU: 국내 NPUaaS(KT Cloud ATOM, 가비아 ATOM-Max, 삼성SDS RNGD)가 모두 구독형이고 공개 단가가 없어,
#      제조사가 공개 비교 대상으로 삼은 GPU 의 시장 중앙값을 대입한 임시값 (메모리도 같은 급)
SEED_POLICIES: list[PricePolicy] = [
    PricePolicy("price-l40s-v1", "NVIDIA L40S 온디맨드", "accelerator", "nvidia", "L40S",
                round(1.64 * USD_KRW), date(2026, 1, 1),
                source="시장 온디맨드 중앙값 $1.64/h (getdeploying.com 32곳, 2026-10-23) x 1,324.6원"),
    PricePolicy("price-a30-v1", "NVIDIA A30 온디맨드", "accelerator", "nvidia", "A30",
                round(0.68 * USD_KRW), date(2026, 1, 1),
                source="시장 온디맨드 중앙값 $0.68/h (getdeploying.com 3곳, 2026-10-23) x 1,324.6원"),
    PricePolicy("price-rngd-v1", "Furiosa RNGD 임시", "accelerator", "furiosa", "RNGD",
                round(1.64 * USD_KRW), date(2026, 1, 1),
                source="공개 단가 없음. 제조사 비교 대상 L40S(Hot Chips 2024, 같은 48GB) 시장 중앙값 대입 (임시)"),
    PricePolicy("price-atom-v1", "Rebellions ATOM 임시", "accelerator", "rebellions", "",
                round(0.69 * USD_KRW), date(2026, 1, 1),
                source="공개 단가 없음. 제조사 비교 대상 T4(MLPerf v3.0, 같은 16GB) 시장 중앙값 $0.69/h 대입 (임시)"),
]


def model_matches(fragment: str, model: str) -> bool:
    """모델 이름 조각이 모델 이름에 단어로 들어 있는지. 대소문자 무시, 빈 조각은 모든 모델.

    부분 문자열로 맞추면 A10 이 A100 에, L4 가 L40S 에 붙어 다른 카드의 단가가 조용히 쓰인다.
    그래서 조각 앞뒤가 영문, 숫자가 아닐 때만 맞는 것으로 본다.

    >>> [model_matches(f, m) for f, m in [("L40S", "NVIDIA L40S"), ("A10", "NVIDIA A100-SXM4-80GB"),
    ...  ("A100", "NVIDIA A100-SXM4-80GB"), ("L4", "NVIDIA L40S"), ("CA22", "RBLN-CA22"), ("", "RNGD"), ("rngd", "RNGD")]]
    [True, False, True, False, True, True, True]
    """
    if not fragment:
        return True
    return re.search(rf"(?<![A-Za-z0-9]){re.escape(fragment)}(?![A-Za-z0-9])", model, re.IGNORECASE) is not None


def find_price(resource_type: str, vendor: str, model: str, day: date, policies: list[PricePolicy],
               contract_type: str = "on_demand") -> Optional[PricePolicy]:
    """그날 유효한 단가 정책, 없으면 None. 여러 개가 맞으면 벤더 지정, 긴 모델 지정, 늦은 시작 순으로 우선.

    단가를 덮어쓰지 않고 유효 구간을 나눠 두므로 지난 날의 비용은 그때 단가로 다시 계산된다.
    ponytail: 계약 형태는 on_demand 만 조회. 프로젝트별 계약 지정이 생기면 contract_type 을 넘김

    >>> p = [PricePolicy("a", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1), date(2026, 10, 1)),
    ...      PricePolicy("b", "", "accelerator", "nvidia", "L40S", 80, date(2026, 10, 1)),
    ...      PricePolicy("c", "", "accelerator", "nvidia", "", 50, date(2026, 1, 1)),
    ...      PricePolicy("d", "", "accelerator", "", "", 10, date(2026, 10, 1))]
    >>> [find_price("accelerator", "nvidia", m, d, p).id for m, d in
    ...  [("NVIDIA L40S", date(2026, 9, 30)), ("NVIDIA L40S", date(2026, 10, 1)), ("NVIDIA A30", date(2026, 10, 1))]]
    ['a', 'b', 'c']
    >>> find_price("accelerator", "furiosa", "RNGD", date(2026, 10, 1), p).id
    'd'
    >>> a10 = [PricePolicy("a10", "", "accelerator", "nvidia", "A10", 100, date(2026, 1, 1))]
    >>> find_price("accelerator", "nvidia", "NVIDIA A100-SXM4-80GB", date(2026, 10, 1), a10) is None
    True
    """
    hits = [p for p in policies
            if p.resource_type == resource_type and p.contract_type == contract_type
            and p.vendor in ("", vendor) and model_matches(p.model, model)
            and p.effective_from <= day and (p.effective_to is None or day < p.effective_to)]
    return max(hits, key=lambda p: (p.vendor != "", len(p.model), p.effective_from), default=None)


def price_info(p: PricePolicy) -> dict:
    return {"policy_id": p.id, "name": p.name, "resource_type": p.resource_type, "vendor": p.vendor,
            "model": p.model, "contract_type": p.contract_type, "billing_unit": p.billing_unit,
            "unit_price_krw": p.unit_price_krw, "effective_from": p.effective_from.isoformat(),
            "effective_to": p.effective_to.isoformat() if p.effective_to else None, "source": p.source}


class PolicyConflict(Exception):
    """단가 정책 변경 규칙 위반. args[0] 이 코드."""


def check_new_policy(new: PricePolicy, existing: list[PricePolicy], today: date) -> list[PricePolicy]:
    """새 버전을 추가해도 되는지 검사하고, 끝이 열린 이전 버전 중 새 버전 시작일에 닫아야 할 것을 반환.

    소급 금지: 오늘(KST)까지 쌓인 사용량의 비용이 바뀌지 않게, 시작일은 내일 이후만 허용.
    자동 종료는 새 버전도 끝이 열려 있을 때만. 끝이 있는 버전을 끼워 넣으면 그 뒤 단가가 비므로 겹침으로 거절.

    >>> old = PricePolicy("a", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1))
    >>> new = replace(old, id="b", unit_price_krw=80, effective_from=date(2026, 11, 1))
    >>> [p.id for p in check_new_policy(new, [old], date(2026, 10, 23))]
    ['a']
    >>> try:
    ...     check_new_policy(replace(new, effective_to=date(2026, 12, 1)), [old], date(2026, 10, 23))
    ... except PolicyConflict as e:
    ...     print(e)
    POLICY_OVERLAP
    >>> try:
    ...     check_new_policy(replace(new, effective_from=date(2026, 10, 23)), [old], date(2026, 10, 23))
    ... except PolicyConflict as e:
    ...     print(e)
    POLICY_BACKDATED
    """
    if new.effective_from <= today:
        raise PolicyConflict("POLICY_BACKDATED")
    if new.effective_to is not None and new.effective_to <= new.effective_from:
        raise PolicyConflict("POLICY_INVALID_PERIOD")
    close = []
    for p in existing:
        same = (p.resource_type, p.vendor, p.model.lower(), p.contract_type) == \
               (new.resource_type, new.vendor, new.model.lower(), new.contract_type)
        overlap = p.effective_from < (new.effective_to or date.max) and new.effective_from < (p.effective_to or date.max)
        if not (same and overlap) or p.id == new.id:
            continue
        if p.effective_to is None and new.effective_to is None and p.effective_from < new.effective_from:
            close.append(p)
        else:
            raise PolicyConflict("POLICY_OVERLAP")
    return close


def leaves_gap(new: PricePolicy, existing: list[PricePolicy], replaced_ids: set[str]) -> bool:
    """이전 버전을 자동 종료시킨 버전에 끝을 넣으면 그 뒤로 같은 자원의 단가가 비는지. replaced_ids 는 이 버전이 닫은 버전.

    새 버전 추가는 같은 경우를 겹침으로 거절하므로(check_new_policy) 수정에도 같은 규칙을 둔다.
    끝 날에 시작하는 다음 버전이 있으면 공백이 아니다. 이전 버전이 없는 자원은 끝을 넣어도 된다.

    >>> v1 = PricePolicy("v1", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1), date(2026, 12, 1))
    >>> v2 = PricePolicy("v2", "", "accelerator", "nvidia", "L40S", 80, date(2026, 12, 1), date(2026, 12, 10))
    >>> leaves_gap(v2, [v1], {"v1"}), leaves_gap(replace(v2, effective_to=None), [v1], {"v1"})
    (True, False)
    >>> v3 = replace(v2, id="v3", effective_from=date(2026, 12, 10), effective_to=None)
    >>> leaves_gap(v2, [v1, v3], {"v1"}), leaves_gap(v2, [], set())
    (False, False)
    """
    if new.effective_to is None or not replaced_ids:
        return False
    key = (new.resource_type, new.vendor, new.model.lower(), new.contract_type)
    return not any((p.resource_type, p.vendor, p.model.lower(), p.contract_type) == key
                   and p.effective_from == new.effective_to and p.id != new.id for p in existing)


# ---------------------------------------------------------------------------
# 2. 미터링 엔진
# ---------------------------------------------------------------------------

# 벤더별 카드 지표와 exporter 라벨. 카드 ID 는 클러스터를 넘어 유일한 UUID 를 씀
# (가속기 API 의 Furiosa 카드 ID 는 노드 안에서만 유일한 device 라 서로 다름).
# ponytail: MIG 인스턴스는 부모 카드 1장으로 묶어 계측. 파티션 단위 과금은 파티션 귀속 근거가 생긴 뒤
METER_SOURCES: dict[str, dict] = {
    # MIG 모드 카드는 GPU_UTIL 을 내지 않아 그래픽 엔진 활성 비율(0~1, 인스턴스별)로 대신 계측
    "nvidia": {"util": "DCGM_FI_DEV_GPU_UTIL", "util_fallback": "DCGM_FI_PROF_GR_ENGINE_ACTIVE * 100",
               "power": "DCGM_FI_DEV_POWER_USAGE",
               "id": "UUID", "host": "Hostname", "model": "modelName", "model_default": ""},
    # 사용률은 코어(8개)별 시계열이라 카드 단위로 평균. 모델 라벨이 없어 고정값
    "furiosa": {"util": "furiosa_npu_core_utilization", "power": "furiosa_npu_hw_power",
                "id": "uuid", "host": "hostname", "model": None, "model_default": "RNGD"},
    # 전력 x1000: exporter 가 실측의 1/1000 로 표출 (rbln-stat 대조, 2026-08-24)
    "rebellions": {"util": '{__name__="RBLN_DEVICE_STATUS:UTILIZATION"}',
                   "power": '({__name__="RBLN_DEVICE_STATUS:CARD_POWER"}) * 1000',
                   "id": "uuid", "host": "hostname", "model": "card", "model_default": ""},
}
METER_INTERVAL_SEC = 300
# 정각 뒤 이만큼 기다렸다가 계측. 레코드는 다시 쓰지 않으므로 원격 수집(remote write) 지연까지 넉넉히 둠
SETTLE = timedelta(minutes=10)
CHUNK = timedelta(hours=24)  # 밀린 구간을 한 번에 조회하는 최대 길이


def card_util_expr(vendor: str) -> str:
    """카드 1장당 시계열 1개인 사용률(%) 식. NPU 는 모니터링 API 와 같은 배율(NPU_UTIL_SCALE_*)을 적용.

    >>> card_util_expr("furiosa")
    'avg by (cluster, uuid, hostname) (furiosa_npu_core_utilization)'
    >>> card_util_expr("nvidia").count("avg by (cluster, UUID, Hostname, modelName)")
    2
    """
    src = METER_SOURCES[vendor]
    by = ", ".join(["cluster", src["id"], src["host"]] + ([src["model"]] if src["model"] else []))
    expr = npu_util(f"avg by ({by}) ({src['util']})", vendor)
    if src.get("util_fallback"):
        expr = f"({expr}) or (avg by ({by}) ({src['util_fallback']}))"
    return expr


def meter_queries(vendor: str) -> dict[str, str]:
    """1시간 구간 값을 구하는 식. 구간 안을 1분 간격으로 다시 표본 추출해 존재 비율, 평균, 최대를 셈.

    산식: 점유 = 존재 표본 비율 x 1h, 사용 = 사용률>0 표본 비율 x 1h, 가중 = 평균 사용률/100 x 1h,
    전력(게이지 W) = 평균 W / 1000 x 1h. 결측 분은 0 으로 셈(가중, 전력은 점유 비율만큼만 들어감).
    """
    util = card_util_expr(vendor)
    src = METER_SOURCES[vendor]
    power = f"max by (cluster, {src['id']}) ({src['power']})"
    w = "[1h:1m]"
    return {
        "allocated_hours": f"count_over_time(({util}){w}) / 60",
        "usage_hours": f"count_over_time((({util}) > 0){w}) / 60",
        "weighted_hours": f"sum_over_time(({util}){w}) / 6000",
        "avg_utilization_percent": f"avg_over_time(({util}){w})",
        "peak_utilization_percent": f"max_over_time(({util}){w})",
        "power_kwh": f"sum_over_time(({power}){w}) / 60000",
    }


# 쿠버네티스 없는 단독 VM 클러스터(Magnum 매핑 없음)는 카드 호스트 이름과 같은 이름의 Nova VM 의 프로젝트로 귀속한다.
# openstack-exporter 의 VM 상태(tenant_id)에 프로젝트 정보(id, name)를 붙인 것. 범위 조회 간격(1h)마다 그 시간에 있던 VM
VM_PROJECT_QUERY = (
    "max by (name, project) (last_over_time(openstack_nova_server_status[1h]) * on (tenant_id) group_left(project) "
    'max by (tenant_id, project) (label_replace(label_replace(openstack_identity_project_info, '
    '"tenant_id", "$1", "id", "(.*)"), "project", "$1", "name", "(.*)")))')


def vm_project_map(result: list[dict]) -> dict[str, str]:
    """VM 이름 → 프로젝트 이름. 같은 이름의 VM 이 여러 프로젝트에 있으면 어느 쪽인지 몰라 뺀다.

    >>> s = lambda n, p: {"metric": {"name": n, "project": p}, "values": [[0, "1"]]}
    >>> vm_project_map([s("vm-a", "p1"), s("vm-a", "p1"), s("vm-b", "p1"), s("vm-b", "p2")])
    {'vm-a': 'p1'}
    """
    found: dict[str, set[str]] = defaultdict(set)
    for series in result:
        m = series.get("metric", {})
        if m.get("name") and m.get("project"):
            found[m["name"]].add(m["project"])
    return {name: next(iter(ps)) for name, ps in found.items() if len(ps) == 1}


async def meter_range(start: datetime, end: datetime) -> tuple[list[dict], set[datetime]]:
    """[start, end) 의 1시간 구간마다 카드별 사용량 레코드와, Prometheus 에 데이터가 있던 구간 집합.

    Prometheus 조회 실패는 예외로 올려 그 구간을 다음 주기에 다시 계측하게 한다(빈 결과와 구분).
    수집 대상(up) 표본이 하나도 없던 구간은 사용량 0 이 아니라 결측으로 남기려고 따로 표시한다.
    프로젝트는 클러스터의 Magnum 매핑, 없으면 카드 호스트와 같은 이름의 VM 프로젝트. 둘 다 없으면 null(과금 보류)이고
    경고를 남긴다. 이전 값을 재사용하지 않는다.
    ponytail: 보류된 레코드를 나중에 프로젝트에 다시 귀속하는 작업은 아직 없음
    """
    clusters = await cluster_discovery.get_clusters()
    projects = {c.label_value: c.openstack_project for c in clusters.values()}
    s, e = (start + timedelta(hours=1)).isoformat(), end.isoformat()
    jobs = [(v, f, q) for v in METER_SOURCES for f, q in meter_queries(v).items()]
    jobs.append(("", "vm_projects", VM_PROJECT_QUERY))
    jobs.append(("", "covered", "sum(count_over_time(up[1h]))"))
    results = await asyncio.gather(*(
        prometheus_client.range_query(q, s, e, "1h", raise_errors=True) for _, _, q in jobs))
    covered = {datetime.fromtimestamp(float(ts), timezone.utc) - timedelta(hours=1)
               for series in results[-1] for ts, raw in series.get("values", []) if parse_finite(raw)}
    fields = [(vendor, field, result) for (vendor, field, _), result in zip(jobs[:-2], results)]
    return build_records(fields, projects, vm_project_map(results[-2])), covered


def build_records(fields: list[tuple[str, str, list[dict]]], projects: dict[str, Optional[str]],
                  vm_projects: Optional[dict[str, str]] = None) -> list[dict]:
    """(벤더, 필드, 범위 조회 결과) 목록을 카드 x 1시간 사용량 레코드로 조립. 벤더마다 allocated_hours 가 먼저 와야 한다.

    표본 시각은 구간 끝이라 레코드 ts 는 1시간 앞. 점유는 1.0 을 넘지 않게 자르고, 전력이 없으면 NO_POWER_DATA.
    프로젝트는 클러스터 매핑(projects)이 먼저, 없으면 카드 호스트 이름의 VM 프로젝트(vm_projects).

    >>> def s(v, uuid="GPU-1"):
    ...     return [{"metric": {"cluster": "l40s", "UUID": uuid, "Hostname": "n1", "modelName": "NVIDIA L40S"},
    ...              "values": [[1794366000, v]]}]
    >>> recs = build_records([("nvidia", "allocated_hours", s("1.0167") + s("0.5", "GPU-2")),
    ...                       ("nvidia", "weighted_hours", s("0.25")),
    ...                       ("nvidia", "power_kwh", s("0.1234567", "GPU-2") + s("0.1", "GPU-9"))], {"l40s": "p1"})
    >>> [(r["resource_id"], r["allocated_hours"], r["usage_hours"], r["weighted_hours"], r["power_kwh"],
    ...   r["power_estimation"], r["warnings"]) for r in recs]
    [('GPU-1', 1.0, 0.0, 0.25, None, None, ['NO_POWER_DATA']), ('GPU-2', 0.5, 0.0, None, 0.123457, 'direct', [])]
    >>> recs[0]["ts"].isoformat(), recs[0]["project"]
    ('2026-11-11T02:00:00+00:00', 'p1')
    >>> build_records([("nvidia", "allocated_hours", s("1"))], {})[0]["warnings"]
    ['NO_PROJECT_MAPPING', 'NO_POWER_DATA']
    >>> build_records([("nvidia", "allocated_hours", s("1"))], {"l40s": None}, {"n1": "p2"})[0]["project"]  # 단독 VM
    'p2'
    """
    recs: dict[tuple, dict] = {}
    for vendor, field, result in fields:
        src = METER_SOURCES[vendor]
        for series in result:
            m = series.get("metric", {})
            rid, label = m.get(src["id"]), m.get("cluster", "")
            if not rid:
                continue
            for ts, raw in series.get("values", []):
                val = parse_finite(raw)
                if val is None:
                    continue
                hour = datetime.fromtimestamp(float(ts), timezone.utc) - timedelta(hours=1)
                key = (hour, vendor, rid)
                # ponytail: 한 구간 안에서 호스트, 모델 라벨이 바뀌면 시계열이 둘로 갈려 뒤의 값만 남음.
                #           라벨 변경이 잦아지면 (cluster, id) 로만 묶고 호스트, 모델은 따로 조회
                if field == "allocated_hours":
                    project = projects.get(label) or (vm_projects or {}).get(m.get(src["host"]))
                    recs[key] = {
                        "ts": hour, "resource_type": "accelerator", "resource_id": rid,
                        "cluster": label or DEFAULT_CLUSTER_NAME, "project": project, "vendor": vendor,
                        "model": (m.get(src["model"]) if src["model"] else None) or src["model_default"],
                        "node": m.get(src["host"]), "allocated_hours": round(min(val, 1.0), 4),
                        # 사용률 > 0 인 분이 없으면 그 식은 시계열 자체가 없어서, 카드가 있었던 구간은 0 에서 시작
                        "usage_hours": 0.0, "weighted_hours": None, "avg_utilization_percent": None,
                        "peak_utilization_percent": None, "power_kwh": None, "power_estimation": None,
                        "warnings": [] if project else ["NO_PROJECT_MAPPING"],
                    }
                elif key in recs:  # 사용률 없이 전력만 있는 카드는 점유 근거가 없어 건너뜀
                    recs[key][field] = round(val, 6 if field == "power_kwh" else 4)
                    if field == "power_kwh":
                        recs[key]["power_estimation"] = "direct"  # 카드 드라이버 보고값(실측)
    for r in recs.values():
        if r["power_kwh"] is None:
            r["warnings"].append("NO_POWER_DATA")
    return list(recs.values())


async def meter_pending(now: Optional[datetime] = None) -> int:
    """아직 계측하지 않은 완결 구간을 계측해 적재. 보관 기간 밖으로 밀린 구간은 결측으로 남김. 적재한 레코드 수."""
    now = now or datetime.now(timezone.utc)
    end = (now - SETTLE).replace(minute=0, second=0, microsecond=0)
    earliest = (now - timedelta(days=RETENTION_DAYS)).replace(minute=0, second=0, microsecond=0) + timedelta(hours=1)
    last = await store.last_metered_hour()
    start = earliest if last is None else max(earliest, last + timedelta(hours=1))
    total = 0
    while start < end:
        stop = min(start + CHUNK, end)
        records, covered = await meter_range(start, stop)
        await store.save_metering(start, stop, records, covered)
        total += len(records)
        start = stop
    return total


async def run_loop() -> None:
    """METER_INTERVAL_SEC 마다 밀린 구간을 계측. 실패하면 다음 주기에 같은 구간부터 다시."""
    logger.info("metering loop started (interval=%ds)", METER_INTERVAL_SEC)
    while True:
        try:
            n = await meter_pending()
            if n:
                logger.info("metering: %d records", n)
        except Exception as exc:
            logger.warning("metering failed, retry next round: %s", exc)
        await asyncio.sleep(METER_INTERVAL_SEC)


@dataclass
class UsageRow:
    """구간(일 또는 시간) x 클러스터 x 자원 유형 x 모델 하나의 사용량 합계."""

    day: date  # KST 날짜. 단가는 이 날 기준
    cluster: str
    project: Optional[str]  # OpenStack 프로젝트(과금 단위). 매핑 실패 시 None
    vendor: str
    model: str
    allocated_hours: float
    usage_hours: Optional[float] = None
    weighted_hours: Optional[float] = None
    power_kwh: Optional[float] = None
    resource_type: str = "accelerator"
    bucket: Optional[datetime] = None  # 시간 단위 집계의 구간 시작(KST)


async def fetch_usage(start: datetime, end: datetime) -> tuple[list[UsageRow], list[str]]:
    """DB 가 없을 때의 임시 경로. 1시간 표본 하나 = 그 시각 카드 장수 x 1시간 (점유 시간만).

    프로젝트는 계측 경로와 같은 규칙(클러스터 매핑, 없으면 호스트 이름의 VM 프로젝트)으로 붙인다.
    벤더 하나나 VM 프로젝트 조회가 실패하면 그 부분만 빠지고 UPSTREAM_ERROR 경고를 남긴다.
    """
    clusters = await cluster_discovery.get_clusters()
    projects = {c.label_value: c.openstack_project for c in clusters.values()}
    s, e = start.astimezone(timezone.utc).isoformat(), end.astimezone(timezone.utc).isoformat()
    vendors = list(METER_SOURCES)
    queries = [f"count by (cluster, {METER_SOURCES[v]['host']}"
               f"{', ' + METER_SOURCES[v]['model'] if METER_SOURCES[v]['model'] else ''}) ({card_util_expr(v)})"
               for v in vendors]
    results = await asyncio.gather(*(
        prometheus_client.range_query(q, s, e, "1h", raise_errors=True) for q in queries + [VM_PROJECT_QUERY]),
        return_exceptions=True)
    warnings = ["UPSTREAM_ERROR"] if any(isinstance(r, Exception) for r in results) else []
    vm_projects = {} if isinstance(results[-1], Exception) else vm_project_map(results[-1])
    rows: dict[tuple, UsageRow] = {}
    for vendor, result in zip(vendors, results[:-1]):
        if isinstance(result, Exception):
            continue
        src = METER_SOURCES[vendor]
        for series in result:
            m = series.get("metric", {})
            label = m.get("cluster", "")
            model = (m.get(src["model"]) if src["model"] else None) or src["model_default"]
            project = projects.get(label) or vm_projects.get(m.get(src["host"]))
            for ts, raw in series.get("values", []):
                cards = parse_finite(raw)
                if not cards:
                    continue
                day = datetime.fromtimestamp(float(ts), KST).date()
                key = (day, label, project, vendor, model)
                if key not in rows:
                    rows[key] = UsageRow(day, label or DEFAULT_CLUSTER_NAME, project, vendor, model, 0.0)
                rows[key].allocated_hours += cards
    return list(rows.values()), warnings


# ---------------------------------------------------------------------------
# 3. 비용 계산
# ---------------------------------------------------------------------------

def cost_of(row: UsageRow, policies: list[PricePolicy]) -> tuple[Optional[float], Optional[PricePolicy]]:
    """사용량 x 단가. 정책의 billing_unit 에 맞는 시간을 씀. 단가나 그 시간이 없으면 비용 None."""
    p = find_price(row.resource_type, row.vendor, row.model, row.day, policies)
    if p is None:
        return None, None
    hours = getattr(row, p.billing_unit)
    return (None if hours is None else hours * p.unit_price_krw), p


def _add(a: Optional[float], b: Optional[float]) -> Optional[float]:
    return a if b is None else b if a is None else a + b


def summarize(rows: list[UsageRow], policies: list[PricePolicy], key: Callable[[UsageRow], tuple],
              names: tuple[str, ...]) -> list[dict]:
    """rows 를 key 로 묶어 시간, 전력, 비용 합계. names 는 key 튜플 각 자리의 응답 필드 이름.

    결측은 0 으로 채우지 않는다: 값이 하나도 없으면 null, 단가가 없는 점유 시간은 unpriced_hours 로 따로 셈.

    >>> rows = [UsageRow(date(2026, 10, 1), "l40s", "p1", "nvidia", "L40S", 10, 6, 3, 1.5),
    ...         UsageRow(date(2026, 10, 1), "atom", "p1", "rebellions", "CA22", 4, 4, 2, None)]
    >>> pol = [PricePolicy("a", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1))]
    >>> g = summarize(rows, pol, lambda r: (r.project,), ("project",))[0]
    >>> (g["allocated_hours"], g["idle_hours"], g["utilization_percent"], g["power_kwh"], g["cost_krw"], g["unpriced_hours"])
    (14.0, 4.0, 35.71, 1.5, 1000, 4.0)
    """
    groups: dict[tuple, dict] = {}
    for r in rows:
        k = key(r)
        g = groups.setdefault(k, {"allocated_hours": 0.0, "usage_hours": None, "weighted_hours": None,
                                  "power_kwh": None, "cost_krw": None, "unpriced_hours": 0.0})
        g["allocated_hours"] += r.allocated_hours
        for f in ("usage_hours", "weighted_hours", "power_kwh"):
            g[f] = _add(g[f], getattr(r, f))
        cost, _ = cost_of(r, policies)
        if cost is None:
            g["unpriced_hours"] += r.allocated_hours
        else:
            g["cost_krw"] = _add(g["cost_krw"], cost)
    out = []
    for k, g in groups.items():
        alloc, used, weighted = g["allocated_hours"], g["usage_hours"], g["weighted_hours"]
        out.append({
            **dict(zip(names, k)),
            "allocated_hours": round(alloc, 2),
            "usage_hours": None if used is None else round(used, 2),
            "weighted_hours": None if weighted is None else round(weighted, 2),
            "idle_hours": None if used is None else round(alloc - used, 2),
            "utilization_percent": None if weighted is None or not alloc else round(weighted / alloc * 100, 2),
            "power_kwh": None if g["power_kwh"] is None else round(g["power_kwh"], 3),
            "cost_krw": None if g["cost_krw"] is None else round(g["cost_krw"]),
            "unpriced_hours": round(g["unpriced_hours"], 2),
        })
    return out


def simulate_policy(rows: list[UsageRow], policies: list[PricePolicy], candidate: PricePolicy) -> dict:
    """같은 사용량에 후보 단가를 대입한 비용과 지금 단가 비용을 비교. 저장하지 않음.

    후보는 조회 구간 전체에 적용(유효 구간 무시)하고, 후보가 맞지 않는 사용량은 지금 단가 그대로.

    >>> rows = [UsageRow(date(2026, 10, 1), "l40s", "p1", "nvidia", "NVIDIA L40S", 10, 6, 3)]
    >>> cur = [PricePolicy("a", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1))]
    >>> cand = replace(cur[0], id="sim", unit_price_krw=50, billing_unit="usage_hours")
    >>> r = simulate_policy(rows, cur, cand)
    >>> (r["current_cost_krw"], r["simulated_cost_krw"], r["difference_krw"], r["by_project"][0]["difference_krw"])
    (1000, 300, -700, -700)
    """
    cand = replace(candidate, effective_from=date.min, effective_to=None)
    groups: dict[tuple, list[float]] = defaultdict(lambda: [0.0, 0.0])
    for r in rows:
        now_cost, _ = cost_of(r, policies)
        new_cost, matched = cost_of(r, [cand])
        g = groups[(r.project, r.cluster)]
        g[0] += now_cost or 0.0
        g[1] += (new_cost or 0.0) if matched else (now_cost or 0.0)
    cur_total, new_total = sum(g[0] for g in groups.values()), sum(g[1] for g in groups.values())
    return {
        "current_cost_krw": round(cur_total),
        "simulated_cost_krw": round(new_total),
        "difference_krw": round(new_total - cur_total),
        "difference_percent": round((new_total - cur_total) / cur_total * 100, 2) if cur_total else None,
        "by_project": sorted(
            ({"project": p, "cluster": c, "current_cost_krw": round(a), "simulated_cost_krw": round(b),
              "difference_krw": round(b - a)} for (p, c), (a, b) in groups.items()),
            key=lambda x: x["difference_krw"]),
    }


def forecast_remaining(history: list[float], remaining_days: float, method: str,
                       confidence_percent: float) -> tuple[str, float, Optional[float], Optional[float]]:
    """완결된 일별 비용(오래된 순)으로 남은 기간 비용과 신뢰구간 계산. (실제 쓴 방법, 예측, 하한, 상한)

    moving_average: 일평균 x 남은 일수
    linear_trend  : 최소제곱 직선을 남은 기간 가운데 시점에서 읽은 값 x 남은 일수. 3일 미만이면 이동 평균
    구간 폭은 일별 오차를 서로 독립으로 보고 z x 표준편차 x sqrt(남은 일수 + 남은 일수^2 / 표본 수).
    표본이 1일뿐이면 변동을 알 수 없어 하한, 상한은 None (폭 0 인 구간을 내지 않음).

    >>> forecast_remaining([100, 100, 100], 10, "moving_average", 90)
    ('moving_average', 1000.0, 1000.0, 1000.0)
    >>> forecast_remaining([10, 20, 30], 2, "linear_trend", 90)
    ('linear_trend', 90.0, 90.0, 90.0)
    >>> forecast_remaining([10, 20], 2, "linear_trend", 90)[0]
    'moving_average'
    >>> _, mid, lo, hi = forecast_remaining([90, 110, 100, 100], 10, "moving_average", 90)
    >>> lo < mid == 1000.0 < hi
    True
    >>> forecast_remaining([100], 10, "linear_trend", 90)
    ('moving_average', 1000.0, None, None)
    """
    n = len(history)
    z = statistics.NormalDist().inv_cdf(0.5 + confidence_percent / 200)
    if method == "linear_trend" and n >= 3:
        xs = list(range(n))
        slope, intercept = statistics.linear_regression(xs, history)
        sd = math.sqrt(sum((y - intercept - slope * x) ** 2 for x, y in zip(xs, history)) / (n - 2))
        mid = max(0.0, intercept + slope * (n - 0.5 + remaining_days / 2)) * remaining_days
    else:
        method = "moving_average"
        mid = statistics.fmean(history) * remaining_days
        if n < 2:
            return method, round(mid, 2), None, None
        sd = statistics.stdev(history)
    half = z * sd * math.sqrt(remaining_days + remaining_days ** 2 / n)
    return method, round(mid, 2), round(max(0.0, mid - half), 2), round(mid + half, 2)


def judge_budget(accumulated: Optional[float], predicted: Optional[float], amount: float,
                 alert_percent: float) -> dict:
    """예산 판정. 소진율은 현재 누적, 초과 예상액은 월말 예측 기준이라 두 값을 따로 둔다.

    둘을 섞으면 아직 안 쓴 돈으로 경고가 뜨거나, 초과가 확실한데 경고가 없는 상태가 생긴다.

    >>> judge_budget(50, 120, 100, 80)["status"]
    'forecast_exceeded'
    >>> judge_budget(85, 95, 100, 80)["status"]
    'warning'
    >>> judge_budget(100, 130, 100, 80)["estimated_overrun_krw"]
    30.0
    >>> judge_budget(None, None, 100, 80)["status"]
    'unknown'
    """
    used = None if accumulated is None else round(accumulated / amount * 100, 2)
    forecast = None if predicted is None else round(predicted / amount * 100, 2)
    if accumulated is None:
        status = "unknown"
    elif accumulated >= amount:
        status = "exceeded"
    elif predicted is not None and predicted > amount:
        status = "forecast_exceeded"
    elif used >= alert_percent:
        status = "warning"
    else:
        status = "ok"
    return {
        "amount_krw": amount,
        "alert_percent": alert_percent,
        "utilization_percent": used,
        "forecast_percent": forecast,
        "estimated_overrun_krw": None if predicted is None else round(max(0.0, float(predicted - amount)), 2),
        "status": status,
    }


def observed_from(now: datetime) -> date:
    """Prometheus 에서 사용량을 하루 통째로 볼 수 있는 첫 날(KST). 그 전은 보관 기간 밖이라 결측."""
    return (now - timedelta(days=RETENTION_DAYS)).astimezone(KST).date() + timedelta(days=1)


def prometheus_days(now: datetime) -> set[date]:
    """Prometheus 임시 경로에서 하루 통째로 볼 수 있는 지난 날(KST) 집합."""
    first, today = observed_from(now), now.astimezone(KST).date()
    return {first + timedelta(days=i) for i in range((today - first).days)}


def fetch_range(now: datetime, window_days: int) -> tuple[datetime, datetime]:
    """이번 달 누적과 이동 평균 창을 모두 덮는 조회 구간."""
    today = now.astimezone(KST).date()
    first = min(today.replace(day=1), today - timedelta(days=window_days))
    return datetime.combine(first, time(), KST), now


def build_prediction(rows: list[UsageRow], now: datetime, policies: list[PricePolicy], *,
                     seen_days: Optional[set[date]] = None, project: Optional[str] = None,
                     cluster: Optional[str] = None, method: str = "moving_average", window_days: int = 7,
                     confidence_percent: float = 90.0) -> tuple[dict, list[str]]:
    """이번 달(KST) 누적 비용과 월말 예측. 반환은 (응답 data, 경고 코드).

    seen_days 는 사용량을 하루 통째로 가진 지난 날 집합(없으면 Prometheus 보관 기간 기준). 이번 달 지난 날 중
    여기 없는 날(missing_days)은 일부 기록도 누적에서 빼고 예측에서 일평균으로 채운다(일부와 일평균을 함께
    넣으면 이중 계산). 오늘은 진행 중이라 지금까지 기록을 누적에 넣는다.

    >>> now = datetime(2026, 10, 10, 15, tzinfo=timezone.utc)  # KST 10/11 0시. 10일 지남, 21일 남음
    >>> days = [date(2026, 10, d) for d in range(1, 11)]
    >>> rows = [UsageRow(d, "l40s", "p1", "nvidia", "NVIDIA L40S", 24.0) for d in days]
    >>> pol = [PricePolicy("a", "", "accelerator", "nvidia", "L40S", 100, date(2026, 1, 1))]
    >>> data, w = build_prediction(rows, now, pol, seen_days=set(days))
    >>> (data["current"]["accumulated_cost_krw"], data["predicted"]["month_end_cost_krw"],
    ...  data["current"]["days_remaining"], data["breakdown"][0]["projected_cost_krw"], w)
    (24000, 74400, 21.0, 74400, [])
    >>> data, w = build_prediction(rows, now, pol, seen_days=set(days[3:]))  # 앞 3일은 못 본 날: 일평균으로 채움
    >>> (data["current"]["accumulated_cost_krw"], data["current"]["missing_days"],
    ...  data["predicted"]["month_end_cost_krw"], w)
    (16800, 3, 74400, ['PARTIAL_HISTORY'])
    >>> data, w = build_prediction(rows, now, [], seen_days=set(days))  # 단가 없음: 금액은 null
    >>> (data["current"]["accumulated_cost_krw"], data["predicted"], w)
    (None, None, ['PRICE_NOT_DEFINED'])
    >>> build_prediction(rows, now, pol, seen_days=set(days), project="p2")[1]
    ['NO_DATA']
    >>> build_prediction(rows, now, pol, seen_days={days[-1]}, window_days=1)[0]["predicted"]["confidence_interval"] is None
    True
    """
    local = now.astimezone(KST)
    today = local.date()
    month_start = today.replace(day=1)
    days_in_month = calendar.monthrange(today.year, today.month)[1]
    # 응답에 보이는 값과 계산 값을 같게 하려고 먼저 반올림 (0.01일 = 약 15분)
    elapsed = round((local - datetime.combine(month_start, time(), KST)).total_seconds() / 86400, 2)
    remaining = round(days_in_month - elapsed, 2)
    seen = prometheus_days(now) if seen_days is None else seen_days
    missing = sum((month_start + timedelta(days=i)) not in seen for i in range((today - month_start).days))
    window = [d for d in (today - timedelta(days=k) for k in range(window_days, 0, -1)) if d in seen]

    warnings: list[str] = []
    if project is not None and rows and not any(r.project for r in rows):
        warnings.append("NO_PROJECT_MAPPING")
    scoped = [r for r in rows if (r.day in seen or r.day == today)
              and (project is None or r.project == project) and (cluster is None or r.cluster == cluster)]
    if not scoped:
        warnings.append("NO_DATA")

    items: dict[tuple, dict] = {}
    for r in scoped:
        it = items.setdefault((r.cluster, r.project, r.vendor, r.model), {
            "cluster": r.cluster, "project": r.project, "vendor": r.vendor, "model": r.model,
            "hours": 0.0, "daily": defaultdict(float), "policy": None, "policy_day": None})
        if r.day >= month_start:
            it["hours"] += r.allocated_hours
        cost, policy = cost_of(r, policies)
        if cost is None:
            continue
        if it["policy_day"] is None or r.day > it["policy_day"]:  # 표시 단가는 가장 최근 날 기준
            it["policy"], it["policy_day"] = policy, r.day
        it["daily"][r.day] += cost

    breakdown, daily = [], defaultdict(float)
    for it in items.values():
        priced = it["policy"] is not None
        if not priced and "PRICE_NOT_DEFINED" not in warnings:
            warnings.append("PRICE_NOT_DEFINED")
        for d, v in it["daily"].items():
            daily[d] += v
        hist = [it["daily"].get(d, 0.0) for d in window]
        avg = statistics.fmean(hist) if priced and hist else None
        acc = sum(v for d, v in it["daily"].items() if d >= month_start) if priced else None
        breakdown.append({
            "cluster": it["cluster"], "project": it["project"], "vendor": it["vendor"], "model": it["model"],
            "allocated_hours": round(it["hours"], 2),
            "price": price_info(it["policy"]) if priced else None,
            "accumulated_cost_krw": None if acc is None else round(acc),
            "daily_avg_cost_krw": None if avg is None else round(avg),
            "projected_cost_krw": None if avg is None else round(acc + avg * (remaining + missing)),
        })
    breakdown.sort(key=lambda b: (b["cluster"], b["vendor"], b["model"]))

    priced_any = any(b["price"] for b in breakdown)
    accumulated = sum(v for d, v in daily.items() if d >= month_start) if priced_any else None
    history = [daily.get(d, 0.0) for d in window]
    predicted = None
    used_method = method
    if missing:
        warnings.append("PARTIAL_HISTORY")
    if not window:
        warnings.append("INSUFFICIENT_HISTORY")
    elif priced_any:
        used_method, mid, lo, hi = forecast_remaining(history, remaining, method, confidence_percent)
        if used_method != method:
            warnings.append("TREND_FALLBACK_MOVING_AVERAGE")
        # 못 본 지난 날은 추세 연장이 아니라 일평균으로 채움
        _, fill, f_lo, f_hi = forecast_remaining(history, missing, "moving_average", confidence_percent)
        interval = None if lo is None or f_lo is None else {"low_krw": round(accumulated + f_lo + lo),
                                                             "high_krw": round(accumulated + f_hi + hi)}
        predicted = {"month_end_cost_krw": round(accumulated + fill + mid), "confidence_interval": interval,
                     "confidence_percent": confidence_percent}

    data = {
        "scope": {"project": project, "cluster": cluster},
        "month": month_start.strftime("%Y-%m"),
        "method": used_method,
        "included_resource_types": ["accelerator"],
        "current": {
            "date": today.isoformat(),
            "days_in_month": days_in_month,
            "days_elapsed": elapsed,
            "days_remaining": remaining,
            "missing_days": missing,
            "history_days": len(window),
            "accumulated_cost_krw": None if accumulated is None else round(accumulated),
            "daily_avg_cost_krw": round(statistics.fmean(history)) if priced_any and history else None,
        },
        "predicted": predicted,
        "budget": None,
        "breakdown": breakdown,
    }
    return data, warnings


def what_if(pred: dict, changes: list[dict], day: date, policies: list[PricePolicy]) -> tuple[dict, list[str]]:
    """남은 기간 동안 가속기 구성과 사용 시간을 바꾸면 월말 비용이 얼마가 되는지 계산.

    바꾸는 항목은 vendor 가 같고 모델 이름에 change.model 이 단어로 들어 있는(cluster 를 주면 그 클러스터만) 기존
    항목을 대체한다. 맞는 항목이 없으면 새 자원 추가로 본다. 기준선은 항목별 이동 평균.

    >>> pred = {"current": {"days_remaining": 10, "missing_days": 0, "accumulated_cost_krw": 1000},
    ...         "breakdown": [{"cluster": "l40s", "vendor": "nvidia", "model": "NVIDIA L40S",
    ...                        "daily_avg_cost_krw": 100}]}
    >>> pol = [PricePolicy("p", "", "accelerator", "nvidia", "L40S", 5, date(2026, 1, 1))]
    >>> r, w = what_if(pred, [{"vendor": "nvidia", "model": "L40S", "count": 1, "hours_per_day": 8}],
    ...                date(2026, 10, 23), pol)
    >>> (r["baseline_month_end_cost_krw"], r["scenario_month_end_cost_krw"], r["savings_krw"], w)
    (2000, 1400, 600, [])
    >>> pred["breakdown"] = [{"cluster": "c", "vendor": "nvidia", "model": m, "daily_avg_cost_krw": 100}
    ...                      for m in ("NVIDIA A100", "NVIDIA A10")]
    >>> pol = [PricePolicy("a10", "", "accelerator", "nvidia", "A10", 5, date(2026, 1, 1))]
    >>> what_if(pred, [{"vendor": "nvidia", "model": "A10", "count": 1, "hours_per_day": 1}],
    ...         date(2026, 10, 23), pol)[0]["changes"][0]["replaced_items"]  # A100 은 대체하지 않음
    1
    """
    cur = pred["current"]
    remaining, missing, acc = cur["days_remaining"], cur["missing_days"], cur["accumulated_cost_krw"] or 0
    items = [b for b in pred["breakdown"] if b.get("daily_avg_cost_krw") is not None]
    base_daily = sum(b["daily_avg_cost_krw"] for b in items)
    baseline = acc + base_daily * (remaining + missing)

    warnings: list[str] = []
    replaced: set[int] = set()
    added = 0.0
    out = []
    for ch in changes:
        policy = find_price("accelerator", ch["vendor"], ch["model"], day, policies)
        matched = [] if policy is None else [  # 앞선 변경이 이미 대체한 항목은 다시 세지 않음
            i for i, b in enumerate(items)
            if i not in replaced and b["vendor"] == ch["vendor"] and model_matches(ch["model"], b["model"])
            and (not ch.get("cluster") or b["cluster"] == ch["cluster"])]
        if policy is None:
            if "PRICE_NOT_DEFINED" not in warnings:
                warnings.append("PRICE_NOT_DEFINED")
            daily_cost = None
        else:
            replaced.update(matched)
            daily_cost = ch["count"] * ch["hours_per_day"] * policy.unit_price_krw
            added += daily_cost
        out.append({**ch, "price": price_info(policy) if policy else None,
                    "scenario_daily_cost_krw": None if daily_cost is None else round(daily_cost),
                    "replaced_daily_cost_krw": round(sum(items[i]["daily_avg_cost_krw"] for i in matched)),
                    "replaced_items": len(matched)})
    kept_daily = sum(b["daily_avg_cost_krw"] for i, b in enumerate(items) if i not in replaced)
    scenario = acc + base_daily * missing + (kept_daily + added) * remaining
    return {
        "days_remaining": remaining,
        "baseline_month_end_cost_krw": round(baseline),
        "scenario_month_end_cost_krw": round(scenario),
        "difference_krw": round(scenario - baseline),
        "savings_krw": round(max(0.0, baseline - scenario)),
        "changes": out,
    }, warnings


# ---------------------------------------------------------------------------
# 4. 저장소
# ---------------------------------------------------------------------------

SCHEMA_SQL = """
CREATE TABLE IF NOT EXISTS metering_usage_records (
    ts            TIMESTAMPTZ NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    cluster       TEXT NOT NULL,
    project       TEXT,
    vendor        TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    node          TEXT,
    allocated_hours DOUBLE PRECISION NOT NULL,
    usage_hours   DOUBLE PRECISION,
    weighted_hours DOUBLE PRECISION,
    avg_utilization_percent DOUBLE PRECISION,
    peak_utilization_percent DOUBLE PRECISION,
    power_kwh     DOUBLE PRECISION,
    power_estimation TEXT,
    warnings      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ts, resource_type, resource_id)
);
CREATE INDEX IF NOT EXISTS metering_usage_records_project_idx ON metering_usage_records(project, ts);
CREATE TABLE IF NOT EXISTS metering_runs (
    hour          TIMESTAMPTZ PRIMARY KEY,
    records       INTEGER NOT NULL,
    covered       BOOLEAN NOT NULL,
    finished_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS metering_price_policies (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    vendor        TEXT NOT NULL DEFAULT '',
    model         TEXT NOT NULL DEFAULT '',
    contract_type TEXT NOT NULL DEFAULT 'on_demand',
    billing_unit  TEXT NOT NULL DEFAULT 'allocated_hours',
    unit_price_krw DOUBLE PRECISION NOT NULL CHECK (unit_price_krw >= 0),
    effective_from DATE NOT NULL,
    effective_to  DATE,
    closed_by     TEXT,
    source        TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS metering_budgets (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    project       TEXT,
    cluster       TEXT,
    month         TEXT,
    amount_krw    DOUBLE PRECISION NOT NULL CHECK (amount_krw > 0),
    alert_percent DOUBLE PRECISION NOT NULL DEFAULT 80,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS metering_predictions (
    scope         TEXT NOT NULL,
    month         TEXT NOT NULL,
    method        TEXT NOT NULL,
    as_of         DATE NOT NULL,
    accumulated_cost_krw DOUBLE PRECISION NOT NULL,
    predicted_cost_krw   DOUBLE PRECISION NOT NULL,
    low_krw       DOUBLE PRECISION NOT NULL,
    high_krw      DOUBLE PRECISION NOT NULL,
    confidence_percent DOUBLE PRECISION NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, month, method, as_of)
);
"""

_POLICY_FIELDS = ("id", "name", "resource_type", "vendor", "model", "contract_type", "billing_unit",
                  "unit_price_krw", "effective_from", "effective_to", "source")
_BUDGET_FIELDS = ("name", "project", "cluster", "month", "amount_krw", "alert_percent")
_RECORD_FIELDS = ("ts", "resource_type", "resource_id", "cluster", "project", "vendor", "model", "node",
                  "allocated_hours", "usage_hours", "weighted_hours", "avg_utilization_percent",
                  "peak_utilization_percent", "power_kwh", "power_estimation", "warnings")


def to_policy(row) -> PricePolicy:
    return PricePolicy(**{k: row[k] for k in _POLICY_FIELDS})


def _filters(project: Optional[str], cluster: Optional[str], resource_type: Optional[str],
             start_idx: int) -> tuple[str, list]:
    """선택 필터를 SQL 조건과 인자로. 값은 모두 바인드 인자라 주입 걱정 없음."""
    conds, args = [], []
    for col, val in (("project", project), ("cluster", cluster), ("resource_type", resource_type)):
        if val is not None:
            args.append(val)
            conds.append(f"{col}=${start_idx + len(args) - 1}")
    return "".join(f" AND {c}" for c in conds), args


def scope_key(project: Optional[str], cluster: Optional[str]) -> str:
    return f"project={project or '*'},cluster={cluster or '*'}"


class MeteringStore:
    def __init__(self) -> None:
        self.pool = None

    @property
    def ready(self) -> bool:
        return self.pool is not None

    async def connect(self) -> None:
        import asyncpg  # 선택 의존성. DATABASE_URL 미설정 환경에서는 import 자체를 피함

        pool = await asyncpg.create_pool(settings.DATABASE_URL, min_size=1, max_size=4)
        try:
            async with pool.acquire() as con, con.transaction():
                await con.execute(SCHEMA_SQL)
                # 여러 워커가 함께 떠도 시드가 겹치지 않게 같은 ID 는 건너뜀
                if await con.fetchval("SELECT count(*) FROM metering_price_policies") == 0:
                    for p in SEED_POLICIES:
                        await self._insert_policy(con, p, " ON CONFLICT (id) DO NOTHING")
                    logger.info("price policies seeded: %d", len(SEED_POLICIES))
        except Exception:
            await pool.close()  # 준비가 덜 된 풀을 남기면 ready 가 참이 되어 503 대신 500 이 남
            raise
        self.pool = pool

    async def close(self) -> None:
        if self.pool:
            await self.pool.close()
            self.pool = None

    # ── 사용량 레코드 ──
    async def last_metered_hour(self) -> Optional[datetime]:
        return await self.pool.fetchval("SELECT max(hour) FROM metering_runs")

    async def complete_days(self, start: datetime, end: datetime) -> set[date]:
        """24시간 모두 데이터가 있는 상태로 계측된 KST 날짜."""
        rows = await self.pool.fetch(
            "SELECT (hour AT TIME ZONE 'Asia/Seoul')::date AS d FROM metering_runs "
            "WHERE hour >= $1 AND hour < $2 AND covered GROUP BY 1 HAVING count(*) = 24", start, end)
        return {r["d"] for r in rows}

    async def save_metering(self, start: datetime, end: datetime, records: list[dict],
                            covered: set[datetime]) -> None:
        """레코드는 불변: 같은 구간, 같은 자원이 이미 있으면 그대로 둠. 계측한 구간은 레코드가 없어도 이력에 남기고,
        Prometheus 에 데이터가 없던 구간은 covered=false 로 남겨 사용량 0 이 아닌 결측으로 취급한다."""
        counts: dict[datetime, int] = defaultdict(int)
        for r in records:
            counts[r["ts"]] += 1
        async with self.pool.acquire() as con, con.transaction():
            if records:
                await con.executemany(
                    f"INSERT INTO metering_usage_records ({','.join(_RECORD_FIELDS)}) VALUES "
                    f"({','.join(f'${i}' for i in range(1, len(_RECORD_FIELDS) + 1))}) ON CONFLICT DO NOTHING",
                    [tuple(json.dumps(r[f]) if f == "warnings" else r[f] for f in _RECORD_FIELDS) for r in records])
            hours = [start + timedelta(hours=i) for i in range(int((end - start) / timedelta(hours=1)))]
            await con.executemany(
                "INSERT INTO metering_runs (hour, records, covered) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING",
                [(h, counts.get(h, 0), h in covered) for h in hours])

    async def metered_hours(self, start: datetime, end: datetime) -> int:
        return await self.pool.fetchval(
            "SELECT count(*) FROM metering_runs WHERE hour >= $1 AND hour < $2 AND covered", start, end)

    async def aggregate(self, start: datetime, end: datetime, bucket: str = "day", project: Optional[str] = None,
                        cluster: Optional[str] = None, resource_type: Optional[str] = None) -> list[UsageRow]:
        """KST 일 또는 시간 x 클러스터 x 프로젝트 x 모델별 합계. 단가는 일 단위라 비용 계산 전 단계."""
        unit = {"day": "day", "hour": "hour"}[bucket]
        where, args = _filters(project, cluster, resource_type, 3)
        rows = await self.pool.fetch(
            f"SELECT date_trunc('{unit}', ts AT TIME ZONE 'Asia/Seoul') AS b, cluster, project, resource_type, "
            "vendor, model, sum(allocated_hours) AS a, sum(usage_hours) AS u, sum(weighted_hours) AS w, "
            "sum(power_kwh) AS p FROM metering_usage_records "
            f"WHERE ts >= $1 AND ts < $2{where} GROUP BY 1,2,3,4,5,6 ORDER BY 1", start, end, *args)
        return [UsageRow(day=r["b"].date(), cluster=r["cluster"], project=r["project"], vendor=r["vendor"],
                         model=r["model"], allocated_hours=r["a"], usage_hours=r["u"], weighted_hours=r["w"],
                         power_kwh=r["p"], resource_type=r["resource_type"],
                         bucket=r["b"].replace(tzinfo=KST) if bucket == "hour" else None) for r in rows]

    async def records(self, start: datetime, end: datetime, project: Optional[str], cluster: Optional[str],
                      resource_type: Optional[str], limit: int, offset: int) -> tuple[list[dict], int]:
        where, args = _filters(project, cluster, resource_type, 3)
        base = f"FROM metering_usage_records WHERE ts >= $1 AND ts < $2{where}"
        total = await self.pool.fetchval(f"SELECT count(*) {base}", start, end, *args)
        n = len(args) + 3
        rows = await self.pool.fetch(
            f"SELECT * {base} ORDER BY ts DESC, resource_id LIMIT ${n} OFFSET ${n + 1}",
            start, end, *args, limit, offset)
        out = []
        for r in rows:
            d = dict(r)
            d["warnings"] = json.loads(d["warnings"]) if isinstance(d["warnings"], str) else d["warnings"]
            d["record_id"] = f"meter-{d['ts'].astimezone(KST):%Y%m%d-%H%M}-{d['resource_id']}"
            out.append(d)
        return out, total

    # ── 단가 정책 ──
    @staticmethod
    async def _insert_policy(con, p: PricePolicy, suffix: str = "") -> None:
        d = asdict(p)
        await con.execute(
            f"INSERT INTO metering_price_policies ({','.join(_POLICY_FIELDS)}) VALUES "
            f"({','.join(f'${i}' for i in range(1, len(_POLICY_FIELDS) + 1))}){suffix}",
            *(d[k] for k in _POLICY_FIELDS))

    async def list_policies(self) -> list[PricePolicy]:
        return [to_policy(r) for r in await self.policy_rows()]

    async def policy_rows(self) -> list[dict]:
        return [dict(r) for r in await self.pool.fetch(
            "SELECT * FROM metering_price_policies ORDER BY resource_type, vendor, model, effective_from")]

    async def get_policy_row(self, pid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM metering_price_policies WHERE id=$1", pid)
        return dict(r) if r else None

    async def add_policy_version(self, p: PricePolicy, close: list[PricePolicy]) -> dict:
        async with self.pool.acquire() as con, con.transaction():
            await self._insert_policy(con, p)
            for old in close:  # 누가 닫았는지 남겨, 그 버전을 지울 때 이 버전만 다시 연다
                await con.execute("UPDATE metering_price_policies SET effective_to=$2, closed_by=$3, updated_at=now() "
                                  "WHERE id=$1", old.id, p.effective_from, p.id)
        return await self.get_policy_row(p.id)

    async def update_policy(self, p: PricePolicy) -> dict:
        """종료일을 직접 바꾸면 자동 종료 기록(closed_by)은 지움."""
        await self.pool.execute(
            "UPDATE metering_price_policies SET name=$2, billing_unit=$3, unit_price_krw=$4, "
            "closed_by=CASE WHEN effective_to IS NOT DISTINCT FROM $5 THEN closed_by END, effective_to=$5, "
            "source=$6, updated_at=now() WHERE id=$1",
            p.id, p.name, p.billing_unit, p.unit_price_krw, p.effective_to, p.source)
        return await self.get_policy_row(p.id)

    async def delete_policy(self, p: PricePolicy) -> None:
        """아직 시작 전인 버전 삭제. 이 버전이 자동으로 닫았던 이전 버전만 다시 연다.

        이전 버전의 새 끝은 이 버전의 끝에서 시작하는 후속 버전이 있으면 그 날, 없으면 계속(단가 공백 방지).
        """
        async with self.pool.acquire() as con, con.transaction():
            successor = None if p.effective_to is None else await con.fetchval(
                "SELECT id FROM metering_price_policies WHERE resource_type=$1 AND vendor=$2 AND "
                "lower(model)=lower($3) AND contract_type=$4 AND effective_from=$5 AND id<>$6",
                p.resource_type, p.vendor, p.model, p.contract_type, p.effective_to, p.id)
            await con.execute("DELETE FROM metering_price_policies WHERE id=$1", p.id)
            await con.execute("UPDATE metering_price_policies SET effective_to=$2, closed_by=$3, updated_at=now() "
                              "WHERE closed_by=$1", p.id, p.effective_to if successor else None, successor)

    # ── 예산 ──
    async def list_budgets(self) -> list[dict]:
        return [dict(r) for r in await self.pool.fetch("SELECT * FROM metering_budgets ORDER BY id")]

    async def get_budget(self, bid: str) -> Optional[dict]:
        r = await self.pool.fetchrow("SELECT * FROM metering_budgets WHERE id=$1", bid)
        return dict(r) if r else None

    async def create_budget(self, b: dict) -> dict:
        bid = f"bgt-{uuid.uuid4().hex[:6]}"
        await self.pool.execute(
            "INSERT INTO metering_budgets (id,name,project,cluster,month,amount_krw,alert_percent) "
            "VALUES ($1,$2,$3,$4,$5,$6,$7)", bid, *(b[k] for k in _BUDGET_FIELDS))
        return await self.get_budget(bid)

    async def replace_budget(self, bid: str, b: dict) -> Optional[dict]:
        status = await self.pool.execute(
            "UPDATE metering_budgets SET name=$2,project=$3,cluster=$4,month=$5,amount_krw=$6,alert_percent=$7,"
            "updated_at=now() WHERE id=$1", bid, *(b[k] for k in _BUDGET_FIELDS))
        return await self.get_budget(bid) if status.endswith("1") else None

    async def delete_budget(self, bid: str) -> bool:
        return (await self.pool.execute("DELETE FROM metering_budgets WHERE id=$1", bid)).endswith("1")

    async def budget_for(self, project: Optional[str], cluster: Optional[str], month: str) -> Optional[dict]:
        """범위가 같은 예산. 그 달 지정 예산이 매월 예산(month NULL)보다 우선."""
        r = await self.pool.fetchrow(
            "SELECT * FROM metering_budgets WHERE project IS NOT DISTINCT FROM $1 AND cluster IS NOT DISTINCT FROM $2 "
            "AND (month=$3 OR month IS NULL) ORDER BY month IS NULL, id LIMIT 1", project, cluster, month)
        return dict(r) if r else None

    async def budgets_in(self, month: str) -> list[dict]:
        rows = await self.pool.fetch(
            "SELECT DISTINCT ON (project, cluster) * FROM metering_budgets WHERE month=$1 OR month IS NULL "
            "ORDER BY project, cluster, month IS NULL, id", month)
        return [dict(r) for r in rows]

    # ── 예측 이력 ──
    async def save_prediction(self, scope: str, data: dict) -> None:
        """하루 한 건(마지막 값)만 남김. 월말 실적과 비교해 방법별 오차를 재는 근거."""
        p = data["predicted"]
        await self.pool.execute(
            "INSERT INTO metering_predictions (scope,month,method,as_of,accumulated_cost_krw,predicted_cost_krw,"
            "low_krw,high_krw,confidence_percent) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) "
            "ON CONFLICT (scope,month,method,as_of) DO UPDATE SET accumulated_cost_krw=EXCLUDED.accumulated_cost_krw,"
            "predicted_cost_krw=EXCLUDED.predicted_cost_krw,low_krw=EXCLUDED.low_krw,high_krw=EXCLUDED.high_krw,"
            "confidence_percent=EXCLUDED.confidence_percent,created_at=now()",
            scope, data["month"], data["method"], date.fromisoformat(data["current"]["date"]),
            data["current"]["accumulated_cost_krw"], p["month_end_cost_krw"],
            p["confidence_interval"]["low_krw"], p["confidence_interval"]["high_krw"], p["confidence_percent"])


store = MeteringStore()


# ---------------------------------------------------------------------------
# 5. 조회 조립
# ---------------------------------------------------------------------------

def today_kst() -> date:
    return datetime.now(timezone.utc).astimezone(KST).date()


async def load_policies() -> list[PricePolicy]:
    return await store.list_policies() if store.ready else SEED_POLICIES


async def _prometheus_usage(now: datetime, window_days: int) -> tuple[list[UsageRow], set[date], list[str]]:
    rows, warnings = await fetch_usage(*fetch_range(now, window_days))
    return rows, prometheus_days(now), warnings


async def recent_usage(now: datetime, window_days: int) -> tuple[list[UsageRow], set[date], list[str]]:
    """예측용 일별 사용량, 하루 통째로 계측된 날 집합, 경고.

    계측 이력이 있으면 사용량 레코드, 없으면 Prometheus 임시 경로.
    """
    if not store.ready or await store.last_metered_hour() is None:
        return await _prometheus_usage(now, window_days)
    start, end = fetch_range(now, window_days)
    return await store.aggregate(start, end), await store.complete_days(start, end), []


async def predict_monthly(project: Optional[str], cluster: Optional[str], method: str, window_days: int,
                          confidence_percent: float, save_history: bool = False,
                          now: Optional[datetime] = None) -> tuple[dict, list[str]]:
    """사용량 조회 + 예측 + 예산 판정. save_history 면 예측 이력을 하루 한 건 남김.

    이력은 방법별 오차 비교용이라 기본 조건(7일 창, 90%)으로 부른 월말 예측만 남긴다.
    """
    now = now or datetime.now(timezone.utc)
    warnings: list[str] = []
    try:
        rows, seen, w = await recent_usage(now, window_days)
        policies = await load_policies()
    except Exception as exc:  # DB 장애여도 임시 경로와 초기 단가로 예측은 돌려줌
        logger.warning("metering store error: %s", exc)
        warnings.append("METERING_STORE_UNAVAILABLE")
        (rows, seen, w), policies = await _prometheus_usage(now, window_days), SEED_POLICIES
    data, w2 = build_prediction(rows, now, policies, seen_days=seen, project=project, cluster=cluster,
                                method=method, window_days=window_days, confidence_percent=confidence_percent)
    warnings += [x for x in w + w2 if x not in warnings]
    if store.ready and "METERING_STORE_UNAVAILABLE" not in warnings:
        try:
            budget = await store.budget_for(project, cluster, data["month"])
            if budget:
                predicted = (data["predicted"] or {}).get("month_end_cost_krw")
                data["budget"] = {"budget_id": budget["id"], "name": budget["name"],
                                  **judge_budget(data["current"]["accumulated_cost_krw"], predicted,
                                                 budget["amount_krw"], budget["alert_percent"])}
            # 이력은 구간까지 비교하므로 구간을 내지 못한 날(표본 1일)은 남기지 않음
            if save_history and data["predicted"] and data["predicted"]["confidence_interval"]:
                await store.save_prediction(scope_key(project, cluster), data)
        except Exception as exc:  # 저장소 장애가 예측 응답을 막지 않게 함
            logger.warning("metering store error: %s", exc)
            warnings.append("METERING_STORE_UNAVAILABLE")
    return data, warnings


async def budget_statuses(project: Optional[str], cluster: Optional[str], window_days: int,
                          confidence_percent: float) -> tuple[list[dict], list[str]]:
    """이번 달에 적용되는 예산마다 누적 소진율과 월말 예측 판정. 사용량은 한 번만 조회."""
    now = datetime.now(timezone.utc)
    month = now.astimezone(KST).strftime("%Y-%m")
    budgets = [b for b in await store.budgets_in(month)
               if (project is None or b["project"] == project) and (cluster is None or b["cluster"] == cluster)]
    if not budgets:
        return [], ["NO_BUDGET"]
    rows, seen, warnings = await recent_usage(now, window_days)
    policies = await load_policies()
    out = []
    for b in budgets:
        data, w = build_prediction(rows, now, policies, seen_days=seen, project=b["project"],
                                   cluster=b["cluster"], window_days=window_days,
                                   confidence_percent=confidence_percent)
        warnings += [x for x in w if x not in warnings]
        predicted = (data["predicted"] or {}).get("month_end_cost_krw")
        out.append({"budget_id": b["id"], "name": b["name"], "scope": data["scope"], "month": data["month"],
                    "accumulated_cost_krw": data["current"]["accumulated_cost_krw"],
                    "predicted_month_end_cost_krw": predicted,
                    **judge_budget(data["current"]["accumulated_cost_krw"], predicted,
                                   b["amount_krw"], b["alert_percent"])})
    return out, warnings


def resolve_period(period: str, start: Optional[str], end: Optional[str],
                   now: Optional[datetime] = None) -> tuple[datetime, datetime]:
    """조회 구간. start 가 있으면 period 를 무시하고 start ~ end(없으면 지금), 없으면 period(current_month | last_month).

    날짜만 주면 KST 0시. end 는 그 시각 전까지(미포함).

    >>> now = datetime(2026, 10, 23, 8, tzinfo=timezone.utc)
    >>> [d.isoformat() for d in resolve_period("last_month", None, None, now)]
    ['2026-09-01T00:00:00+09:00', '2026-10-01T00:00:00+09:00']
    >>> s, e = resolve_period("last_month", "2026-10-20", None, now)
    >>> (s.isoformat(), e == now)
    ('2026-10-20T00:00:00+09:00', True)
    """
    now = now or datetime.now(timezone.utc)

    def parse(s: str) -> datetime:
        d = datetime.fromisoformat(s)
        return d if d.tzinfo else d.replace(tzinfo=KST)

    month_start = datetime.combine(now.astimezone(KST).date().replace(day=1), time(), KST)
    if period == "last_month":
        p_start = datetime.combine((month_start - timedelta(days=1)).date().replace(day=1), time(), KST)
        p_end = month_start
    else:
        p_start, p_end = month_start, now
    if start:
        return parse(start), (parse(end) if end else now)
    return p_start, (parse(end) if end else p_end)


async def usage_window(start: datetime, end: datetime) -> tuple[dict, list[str]]:
    """구간 안에서 계측된 시간 수와 기대 시간 수. 덜 계측됐으면 PARTIAL_HISTORY.

    기대 시간은 계측 주기상 이미 끝났어야 할 구간까지만 셈 (방금 끝난 구간은 SETTLE 과 루프 간격 뒤에 계측).
    """
    due = (datetime.now(timezone.utc) - SETTLE - timedelta(seconds=METER_INTERVAL_SEC)).replace(
        minute=0, second=0, microsecond=0)
    expected = max(0, int((min(end, due) - start) / timedelta(hours=1)))
    metered = await store.metered_hours(start, end)
    return ({"start": start.isoformat(), "end": end.isoformat(), "metered_hours": metered,
             "expected_hours": expected}, ["PARTIAL_HISTORY"] if metered < expected else [])
