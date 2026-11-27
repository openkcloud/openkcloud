"""VM 프로비저닝 워크플로 — CORE 워커가 실행한다. OpenStack 작업은 IAAS 큐, DB 기록은 CORE 큐의 작업으로 부른다.

진행 표시: 시작할 때 등록한 상세 단계 순서(plan)를 앞에서부터 '진행 중 → 완료'로 기록한다.
실패하면 이번 회차가 만든 자원(VM·부팅 볼륨·FIP)을 정리한 뒤 티켓을 '실패'로 마감한다 — 재시도는 새 회차로 처음부터.
네트워크·스토리지·가속기·기동은 VM 생성 요청 한 번 안에서 Nova 가 처리하므로, 서버 상태(task_state)를 보고
그 지점까지의 상세 단계를 완료로 표시한다(짧게 지나간 상태는 한꺼번에 완료로 보일 수 있다).
진행 기록·시간 제한·CORE 작업 호출은 provision_base 공통(클러스터 워크플로와 함께 쓴다).
이 파일은 Temporal 샌드박스에서 실행되므로 DB·HTTP 등 외부 모듈을 가져오지 않는다.
"""
import asyncio
from datetime import timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ActivityError, ApplicationError

# CORE_QUEUE·HEARTBEAT·재시도·END 는 다른 모듈(ai_workspace_provision·runtime)·시험이 이 모듈에서 가져다 쓴다
from .provision_base import (CORE_QUEUE, DB_RETRY, END, HEARTBEAT, NO_RETRY, QUERY_RETRY,  # noqa: F401
                             ProvisionSteps, reason)

IAAS_QUEUE = "okc-iaas"   # IAAS 워커: OpenStack 작업

# ── 시간·재시도 기준(임시값 — 논의 후 조정) ───────────────────────────
POLL_SECONDS = 1                          # 상태 확인 간격 — 사이에 지나간 단계는 시작·완료가 같은 시각(화면 '1초 미만')
WATCH_MAX = timedelta(seconds=60)         # 상태 확인 작업 1회 최대 점유시간(상태가 바뀌면 바로 돌아옴)
VOLUME_READY_MAX = timedelta(minutes=5)   # 부팅 볼륨 준비 대기 1회 최대
VOLUME_READY_TRIES = 2                    # 부팅 볼륨 준비 대기 횟수 — 1회를 넘겨도 Cinder 는 계속 만들고 있어 한 번 더 기다린다
BUILD_MAX = timedelta(minutes=5)          # 스케줄링 ~ ACTIVE 최대
WORKFLOW_MAX = timedelta(minutes=30)      # 워크플로 전체 최대(넘으면 실패로 기록)
QUEUE_WAIT_MAX = timedelta(minutes=5)     # 큐에서 기다리는 최대 — 시작 자리 대기(예약)·IAAS 작업 대기 모두. 넘으면 실패
CLEANUP_MAX = timedelta(minutes=3)        # 실패 후 자원 정리(VM·볼륨 삭제 완료까지) 최대
RUN_MAX = QUEUE_WAIT_MAX + WORKFLOW_MAX + timedelta(minutes=5)  # 실행 제한(안전장치) — 시작 대기 + 진행 + 정리 여유
# 동시에 진행하는 VM 프로비저닝 수 — VM 하나는 IAAS 작업을 한 번에 하나만 쓰므로 IAAS 워커 슬롯
# (MNT-IAAS MAX_ACTIVITIES × 파드 수)과 같게 둔다. 넘는 신청은 시작 전에 자리를 기다린다(먼저 누른 순서)
PROVISION_MAX = 8
ADMIT_POLL = timedelta(seconds=2)         # 시작 자리가 났는지 다시 확인하는 간격

_NETWORK_SUBS = ("check_network", "create_port", "assign_ip", "apply_secgroup")
_STORAGE_SUBS = ("attach_boot_volume", "check_mount")


def _reason(e: BaseException) -> str:
    """작업 실패에서 사람이 읽을 사유만 꺼낸다."""
    return reason(e, "IAAS")


@workflow.defn(name="VmProvision")
class VmProvision(ProvisionSteps):
    def __init__(self) -> None:
        self.cancel_requested = False  # 시작 대기 중 취소 신호 — run 보다 신호가 먼저 올 수 있어 여기서 만든다

    @workflow.signal(name="cancel_queue")
    def cancel_queue(self) -> None:
        """시작 대기(예약) 취소 — 아직 시작 전이면 신청을 이전 상태로 되돌리고 끝낸다. 시작한 뒤에는 무시."""
        self.cancel_requested = True

    @workflow.run
    async def run(self, inp: dict) -> dict:
        self.inp = inp
        self.cur = -1  # 진행 중인 상세 단계 위치(plan 인덱스)
        self.plan: list[dict] = []
        self.res: dict = {}  # 이번 회차가 만든 자원(볼륨·서버·FIP) — 실패하면 정리하고, 정리 못 한 것만 티켓 result 에 남긴다
        self.volume_creating = False  # 부팅 볼륨 준비 시간 초과 — 아직 만드는 중이라 Cinder 가 지우지 못한다(직접 정리)
        try:
            plan = await self._admit()
            if plan is None:  # 시작 전 취소 — 신청은 이전 상태로 되돌렸다
                return {"cancelled": True}
            self.plan = plan
            result = await self._within(WORKFLOW_MAX, self._provision())
        except Exception as e:  # noqa: BLE001 — 어떤 실패든 진행 기록·티켓에 남긴다
            msg = _reason(e)
            try:
                await self._fail_current(msg)
            except Exception:  # noqa: BLE001 — 단계 기록이 실패해도 정리·티켓 마감은 한다
                pass
            msg += await self._cleanup()
            await self._core("core.finish", inp["ticketId"], "failed", self.res or None, msg)
            raise ApplicationError(msg, non_retryable=True) from e
        await self._core("core.finish", inp["ticketId"], "completed", result, None)
        return result

    async def _admit(self) -> list[dict] | None:
        """시작 자리 대기(예약) — 진행 중 + 앞서 기다리는 신청이 PROVISION_MAX 보다 적으면 단계를 등록(= 시작)하고
        진행 순서(plan)를 돌려준다. 기다리는 중 취소되면 신청을 이전 상태로 되돌리고 None. QUEUE_WAIT_MAX 안에 자리가 안 나면 실패."""
        inp, until = self.inp, workflow.now() + QUEUE_WAIT_MAX
        while not self.cancel_requested:
            plan = await self._core("core.init_steps", inp["ticketId"], inp["attempt"], inp["hasAccelerator"],
                                    bool(inp.get("floatingNetworkId")), PROVISION_MAX)
            if plan is not None:
                return plan
            if workflow.now() >= until:
                raise ApplicationError(f"{int(QUEUE_WAIT_MAX.total_seconds() // 60)}분 안에 시작하지 못했습니다 — 동시에 진행할 수 있는 "
                                       f"VM 프로비저닝({PROVISION_MAX}건)이 모두 진행 중입니다. 다시 시도하세요")
            try:
                await workflow.wait_condition(lambda: self.cancel_requested, timeout=ADMIT_POLL)
            except asyncio.TimeoutError:
                pass
        await self._core("core.cancel_queued", inp["ticketId"], workflow.info().workflow_id, inp["prev"])
        return None

    async def _provision(self) -> dict:
        inp = self.inp
        await self._go("check_flavor")
        flavor_name = await self._iaas("iaas.check_flavor", inp["flavorId"])
        await self._go("check_image", {"check_flavor": flavor_name})
        image_name = await self._iaas("iaas.check_image", inp["imageId"])
        await self._go("set_keypair", {"check_image": image_name})
        await self._go("create_boot_volume", {"set_keypair": inp["keypairLabel"]})

        # 부팅 볼륨 — 이름 {VM이름}-boot-vol, 준비(available)까지 대기. 실패하면 IAAS 작업이 볼륨을 지운다
        self.volume_name = f"{inp['vmName']}-boot-vol"
        volume_id = await self._iaas("iaas.create_boot_volume", inp["projectId"], self.volume_name, inp["volumeGb"],
                                     inp["imageId"], retry=NO_RETRY)
        self.res["volumeId"] = volume_id
        # 준비 대기 — VOLUME_READY_MAX 씩 VOLUME_READY_TRIES 번. 그 안에 준비되면 다음 단계, error 면 IAAS 작업이 지우고 실패.
        # 다 넘기면 실패 — 만드는 중인 볼륨은 Cinder 가 지우지 못하므로 정리하지 않고 직접 정리로 남긴다
        for _ in range(VOLUME_READY_TRIES):
            deadline = (workflow.now() + VOLUME_READY_MAX).timestamp()  # 조회 오류로 재시도돼도 처음 기준을 지킨다
            status = await self._iaas("iaas.wait_volume", inp["projectId"], volume_id, deadline,
                                      timeout=VOLUME_READY_MAX + timedelta(seconds=30), heartbeat=HEARTBEAT)
            if status is None:  # 준비됨
                break
        else:
            self.volume_creating = True
            raise ApplicationError("볼륨 생성 시간이 초과되었습니다. 관리자에게 문의하세요", non_retryable=True)
        await self._go("send_request", {"create_boot_volume": f"{self.volume_name} · {inp['volumeGb']}GB"})

        # VM 생성 요청(1회) — 실패하면 IAAS 작업이 부팅 볼륨을 지운다.
        # 응답이 끊겨도 Nova 가 만들었을 수 있으니 이름을 남겨 둔다(정리 때 이름으로 찾아 삭제)
        self.res["serverName"] = inp["vmName"]
        spec = {k: inp[k] for k in ("flavorId", "imageId", "volumeGb", "networkId", "fixedIp", "securityGroups",
                                    "publicKey")}
        if inp.get("host"):  # 관리자가 고른 노드(노드 지정 권한이 있을 때만) — 예전 입력에는 키가 없다
            spec["host"] = inp["host"]
        if inp.get("keypairName"):  # 넣는 키 이름 — Nova 메타데이터로 남겨 상세에 표시. 예전 입력에는 키가 없다
            spec["keypairName"] = inp["keypairName"]
        server_id = await self._iaas("iaas.create_server", inp["projectId"], {"name": inp["vmName"], **spec}, volume_id,
                                     retry=NO_RETRY, timeout=timedelta(seconds=90))
        self.res["serverId"] = server_id
        del self.res["serverName"]
        await self._go("schedule_host", {"send_request": "202 Accepted"})

        # 상태 확인 — 바뀔 때마다 그 지점까지 진행 기록. ERROR 면 실패, 제한 시간 안에 ACTIVE 가 안 되면 실패
        until = workflow.now() + BUILD_MAX
        last = ""
        while True:
            left = until - workflow.now()
            if left <= timedelta(0):
                raise ApplicationError(f"{int(BUILD_MAX.total_seconds() // 60)}분 안에 ACTIVE 가 되지 않았습니다 (마지막 상태 {last})")
            s = await self._iaas("iaas.watch_server", server_id, last, min(WATCH_MAX, left).total_seconds(),
                                 POLL_SECONDS, timeout=WATCH_MAX + timedelta(seconds=30), heartbeat=HEARTBEAT)
            last = s["key"]
            if s["status"] == "ERROR":
                raise ApplicationError(s.get("fault") or "Nova 빌드 실패(ERROR)")
            if s["status"] == "ACTIVE":
                break
            ts = s.get("taskState")
            target = ("spawn_instance" if ts == "spawning" else "attach_boot_volume" if ts == "block_device_mapping"
                      else "check_network" if s.get("host") or ts == "networking" else "schedule_host")
            await self._go(target, await self._values_until(target, server_id, s))

        # ACTIVE — 남은 기동 단계 완료. 네트워크·스토리지 값은 앞에서 비어 있었을 수 있어 최종 상태로 다시 채운다
        detail = await self._iaas("iaas.server_detail", server_id)
        iface = self._iface(detail)
        fip_target = "attach_fip" if inp.get("floatingNetworkId") else END
        values = await self._values_until(fip_target, server_id, s, detail)
        values |= {"spawn_instance": "BUILD → ACTIVE", "check_active": "ACTIVE"}
        await self._go(fip_target, values)
        await self._set_values(self._server_values(detail))

        result = {**self.res, "portId": iface and iface["portId"], "fixedIp": iface and next(iter(iface["ips"]), None)}  # serverId·volumeId
        if fip_target == "attach_fip":
            if not iface:
                raise ApplicationError("Floating IP 를 연결할 포트를 찾지 못했습니다")
            fip = await self._iaas("iaas.attach_fip", inp["projectId"], inp["floatingNetworkId"], iface["portId"],
                                   retry=NO_RETRY)
            self.res["floatingIpId"] = fip["id"]
            result |= {"floatingIpId": fip["id"], "floatingIp": fip["ip"]}
            await self._go(END, {"attach_fip": fip["ip"]})
        return result

    async def _cleanup(self) -> str:
        """실패한 회차가 만든 자원 정리 → 실패 사유에 덧붙일 문구. 다 정리하면 남은 자원 기록을 비운다."""
        if not self.res:
            return ""
        if self.volume_creating:  # 부팅 볼륨만 만든 단계 — 지울 수 없으니 기다리지 않고 남긴다(결과에 볼륨 ID 기록)
            return f" — 부팅 볼륨 직접 정리 필요(volumeId={self.res['volumeId']})"
        deadline = (workflow.now() + CLEANUP_MAX).timestamp()
        try:
            done = await self._iaas("iaas.cleanup", self.inp["projectId"], self.res, deadline,
                                    timeout=CLEANUP_MAX + timedelta(seconds=30), heartbeat=HEARTBEAT)
        except Exception as e:  # noqa: BLE001 — 정리 실패는 사유에 남기고 자원 식별자를 결과에 보존
            left = ", ".join(f"{k}={v}" for k, v in self.res.items())
            return f" — 생성한 자원 정리 실패, 직접 정리 필요({left}): {_reason(e)}"
        self.res = {}
        return f" — 생성한 자원({'·'.join(done)})은 정리함" if done else ""

    # ── 상세 단계 값 ─────────────────────────────────────────────
    async def _values_until(self, target: str, server_id: str, s: dict, detail: dict | None = None) -> dict:
        """현재 위치부터 target 직전까지 완료 처리할 상세 단계들의 표시 값."""
        passing = {p["sub"] for p in self.plan[max(self.cur, 0):self._idx(target)]}
        values: dict = {"schedule_host": s.get("host") or None}
        if passing & set(_NETWORK_SUBS + _STORAGE_SUBS):
            detail = detail or await self._try_iaas("iaas.server_detail", server_id)
            if detail:
                values |= self._server_values(detail)
        if "allocate_accelerator" in passing:
            values["allocate_accelerator"] = self.inp.get("acceleratorLabel")
        if "bind_arq" in passing:
            states = await self._try_iaas("iaas.arq_states", server_id)
            values["bind_arq"] = ", ".join(dict.fromkeys(states or [])) or None
        return values

    def _iface(self, detail: dict) -> dict | None:
        """신청 네트워크에 붙은 인터페이스(없으면 첫 번째)."""
        ifaces = detail["interfaces"]
        return next((i for i in ifaces if i["netId"] == self.inp["networkId"]), None) or next(iter(ifaces), None)

    def _server_values(self, detail: dict) -> dict:
        """네트워크·스토리지 상세 단계 값 — 포트·IP·보안그룹·볼륨·장치(아직 없으면 None)."""
        iface = self._iface(detail)
        ip = iface and next(iter(iface["ips"]), None)
        vol = next(iter(detail["attachedVolumes"]), None)
        return {
            "check_network": self.inp["networkLabel"],
            "create_port": iface and iface["portId"][:13],  # Horizon 과 같은 줄임(ID 앞 13자리, 예: b6b113d5-154c)
            "assign_ip": ip and (f"{ip} (고정)" if self.inp.get("fixedIp") else ip),
            "apply_secgroup": ", ".join(detail["securityGroups"]) or None,
            "attach_boot_volume": vol and self.volume_name,
            "check_mount": vol and vol["device"],
        }

    # ── 작업 호출 ────────────────────────────────────────────────
    async def _iaas(self, name: str, *args, retry: RetryPolicy = QUERY_RETRY, timeout: timedelta = timedelta(seconds=60),
                    heartbeat: timedelta | None = None):
        return await workflow.execute_activity(name, args=list(args), task_queue=IAAS_QUEUE, start_to_close_timeout=timeout,
                                               schedule_to_start_timeout=QUEUE_WAIT_MAX, heartbeat_timeout=heartbeat,
                                               retry_policy=retry)

    async def _try_iaas(self, name: str, *args):
        """표시용 조회 — 실패해도 진행을 멈추지 않는다(값은 비워 두고 ACTIVE 때 다시 채움)."""
        try:
            return await self._iaas(name, *args)
        except ActivityError:
            return None
