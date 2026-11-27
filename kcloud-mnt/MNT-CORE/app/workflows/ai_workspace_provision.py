"""AI 워크스페이스 프로비저닝 워크플로 — CORE 워커가 실행한다. Kubeflow 작업은 AINB 큐, DB 기록은 CORE 큐의 작업으로 부른다.

AI 워크스페이스 = 프로젝트 1:1 (Kubeflow Profile = 네임스페이스 + 자원 한도). 진행 표시는 VM 과 같은 방식 —
시작할 때 등록한 상세 단계 순서(plan)를 앞에서부터 '진행 중 → 완료'로 기록한다.
실패하면 이번 회차가 만든 워크스페이스를 지운 뒤 티켓을 '실패'로 마감한다 — 재시도는 새 회차로 처음부터.
이 파일은 Temporal 샌드박스에서 실행되므로 DB·HTTP 등 외부 모듈을 가져오지 않는다.
"""
import asyncio
from datetime import timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ActivityError, ApplicationError, TimeoutError as ActivityTimeout, TimeoutType

from .vm_provision import CORE_QUEUE, DB_RETRY, END, HEARTBEAT, QUERY_RETRY

AINB_QUEUE = "okc-ainb"   # AINB 워커: Kubeflow 작업

READY_MAX = timedelta(minutes=3)       # Profile 생성 ~ 네임스페이스 Active·자원 한도 적용 최대
WORKFLOW_MAX = timedelta(minutes=10)   # 워크플로 전체 최대(넘으면 실패로 기록)
CLEANUP_MAX = timedelta(minutes=1)
QUEUE_WAIT_MAX = timedelta(minutes=2)  # AINB 작업이 큐에서 기다리는 최대 — 넘으면 AINB 워커가 없는 것으로 보고 실패


def _reason(e: BaseException) -> str:
    """작업 실패에서 사람이 읽을 사유만 꺼낸다."""
    c = e.cause if isinstance(e, ActivityError) and e.cause else e
    if isinstance(c, ActivityTimeout) and c.type == TimeoutType.SCHEDULE_TO_START:
        return "AINB 워커가 작업을 가져가지 않습니다 (MNT-AINB 워커 실행·Temporal 연결 확인)"
    return getattr(c, "message", None) or str(c)


@workflow.defn(name="AiWorkspaceProvision")
class AiWorkspaceProvision:
    @workflow.run
    async def run(self, inp: dict) -> dict:
        self.inp = inp
        self.cur = -1
        self.plan: list[dict] = []
        self.created = False  # 이번 회차가 Profile 을 만들었는지 — 실패하면 지운다
        try:
            self.plan = await self._core("core.init_steps_ainb", inp["ticketId"], inp["attempt"])
            result = await self._within(WORKFLOW_MAX, self._provision())
        except Exception as e:  # noqa: BLE001 — 어떤 실패든 진행 기록·티켓에 남긴다
            msg = _reason(e)
            try:
                await self._fail_current(msg)
            except Exception:  # noqa: BLE001 — 단계 기록이 실패해도 정리·티켓 마감은 한다
                pass
            msg += await self._cleanup()
            await self._core("core.finish", inp["ticketId"], "failed", None, msg)
            raise ApplicationError(msg, non_retryable=True) from e
        await self._core("core.finish", inp["ticketId"], "completed", result, None)
        return result

    async def _provision(self) -> dict:
        inp = self.inp
        name = inp["workspaceName"]
        limits = (f"vCPU {inp['cpu']} · 메모리 {inp['memoryGi']}Gi · 가속기 {inp['acceleratorCount']}"
                  f" · 저장공간 {inp['storageGi']}Gi")
        await self._go("check_name")
        await self._go("check_quota", {"check_name": name})
        await self._go("create_profile", {"check_quota": limits})

        spec = {k: inp[k] for k in ("projectId", "projectName", "ownerEmail", "cpu", "memoryGi",
                                    "acceleratorCount", "storageGi")}
        cluster = inp.get("clusterId") or ""   # 신청 때 고른 서비스 클러스터 — 비면 AINB 기본 클러스터
        res = await self._ainb("ainb.create_workspace", {"name": name, **spec, "clusterId": cluster},
                               retry=QUERY_RETRY)  # 같은 프로젝트면 멱등
        self.created = self.created or res["created"]
        await self._go("apply_quota", {"create_profile": name})

        deadline = (workflow.now() + READY_MAX).timestamp()
        ready = await self._ainb("ainb.wait_ready", name, deadline, cluster,
                                 timeout=READY_MAX + timedelta(seconds=30), heartbeat=HEARTBEAT)
        await self._go("check_namespace", {"apply_quota": ", ".join(f"{k}={v}" for k, v in ready["quota"].items())})
        await self._go(END, {"check_namespace": "Active"})
        return {"namespace": name, "quota": ready["quota"], "accelerator": ready["accelerator"]}

    async def _cleanup(self) -> str:
        """실패한 회차가 만든 워크스페이스 정리 → 실패 사유에 덧붙일 문구."""
        if not self.created:
            return ""
        try:
            await self._ainb("ainb.delete_workspace", self.inp["workspaceName"], self.inp["projectId"],
                             self.inp.get("clusterId") or "", timeout=CLEANUP_MAX)
        except Exception as e:  # noqa: BLE001 — 정리 실패는 사유에 남긴다
            return f" — 만든 워크스페이스 정리 실패, 직접 정리 필요({self.inp['workspaceName']}): {_reason(e)}"
        return f" — 만든 워크스페이스({self.inp['workspaceName']})는 정리함"

    async def _within(self, limit: timedelta, coro):
        task = asyncio.ensure_future(coro)
        done, _ = await asyncio.wait([task], timeout=limit.total_seconds())
        if not done:
            task.cancel()
            try:
                await task
            except BaseException:  # noqa: BLE001 — 취소된 작업의 오류는 버리고 시간 초과로 기록
                pass
            raise ApplicationError(f"전체 {int(limit.total_seconds() // 60)}분 안에 끝나지 않았습니다")
        return task.result()

    # ── 진행 기록 (VmProvision 과 같은 방식) ────────────────────────
    def _idx(self, code: str) -> int:
        if code == END:
            return len(self.plan)
        return next((i for i, p in enumerate(self.plan) if p["sub"] == code), self.cur)

    async def _go(self, code: str, values: dict | None = None):
        target, values, changes = self._idx(code), values or {}, []
        while self.cur < target:
            if self.cur >= 0:
                p = self.plan[self.cur]
                changes.append({"sub": p["sub"], "status": "done", "value": values.get(p["sub"])})
                if self.cur + 1 == len(self.plan) or self.plan[self.cur + 1]["step"] != p["step"]:
                    changes.append({"step": p["step"], "status": "done"})
            self.cur += 1
            if self.cur < len(self.plan):
                p = self.plan[self.cur]
                if self.cur == 0 or self.plan[self.cur - 1]["step"] != p["step"]:
                    changes.append({"step": p["step"], "status": "running"})
                changes.append({"sub": p["sub"], "status": "running"})
        if changes:
            await self._core("core.update_steps", self.inp["ticketId"], self.inp["attempt"], changes)

    async def _fail_current(self, msg: str):
        if 0 <= self.cur < len(self.plan):
            p = self.plan[self.cur]
            await self._core("core.update_steps", self.inp["ticketId"], self.inp["attempt"], [
                {"sub": p["sub"], "status": "failed", "error": msg},
                {"step": p["step"], "status": "failed", "error": msg},
            ])

    # ── 작업 호출 ────────────────────────────────────────────────
    async def _ainb(self, name: str, *args, retry: RetryPolicy = QUERY_RETRY, timeout: timedelta = timedelta(seconds=60),
                    heartbeat: timedelta | None = None):
        return await workflow.execute_activity(name, args=list(args), task_queue=AINB_QUEUE, start_to_close_timeout=timeout,
                                               schedule_to_start_timeout=QUEUE_WAIT_MAX, heartbeat_timeout=heartbeat,
                                               retry_policy=retry)

    async def _core(self, name: str, *args):
        return await workflow.execute_activity(name, args=list(args), task_queue=CORE_QUEUE,
                                               start_to_close_timeout=timedelta(seconds=30), retry_policy=DB_RETRY)
