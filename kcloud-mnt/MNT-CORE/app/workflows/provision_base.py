"""프로비저닝 워크플로 공통 — 진행 기록(상세 단계 plan)·시간 제한·CORE 작업 호출·실패 사유.

VM(vm_provision)·클러스터(kubernetes_provision) 워크플로가 함께 쓴다. 기반 클래스는 워크플로가 self.inp(ticketId·attempt)·
self.plan·self.cur 를 정해 두고 쓴다. 메서드를 옮기기만 했으므로 실행 중인 워크플로의 명령 순서는 그대로다.
이 파일은 Temporal 샌드박스에서 실행되므로 DB·HTTP 등 외부 모듈을 가져오지 않는다.
"""
import asyncio
from datetime import timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ActivityError, ApplicationError, TimeoutError as ActivityTimeout, TimeoutType

CORE_QUEUE = "okc-core"   # CORE 워커: 워크플로 + DB 기록 작업

HEARTBEAT = timedelta(seconds=30)         # 이 시간 동안 신호가 없으면 워커가 죽은 것으로 보고 다른 워커가 이어받음
QUERY_RETRY = RetryPolicy(initial_interval=timedelta(seconds=1), backoff_coefficient=2,
                          maximum_interval=timedelta(seconds=30), maximum_attempts=5)  # 조회: 간격을 늘려 가며 최대 5회
NO_RETRY = RetryPolicy(maximum_attempts=1)  # 만드는 요청 — 다시 보내면 중복 생성 위험
DB_RETRY = RetryPolicy(initial_interval=timedelta(seconds=1), backoff_coefficient=2,
                       maximum_attempts=4)  # CORE DB 기록: 최대 3회 재시도

END = "__end__"  # 모든 상세 단계 완료


def reason(e: BaseException, worker: str | None = None) -> str:
    """작업 실패에서 사람이 읽을 사유만 꺼낸다. 큐에서 작업을 아무도 가져가지 않았으면 어느 워커인지 알려 준다
    (worker 가 없으면 작업 이름 앞부분 — iaas.* → IAAS, paas.* → PAAS)."""
    c = e.cause if isinstance(e, ActivityError) and e.cause else e
    if isinstance(c, ActivityTimeout) and c.type == TimeoutType.SCHEDULE_TO_START:
        w = worker or (e.activity_type.split(".")[0].upper() if isinstance(e, ActivityError) else "")
        return f"{w} 워커가 작업을 가져가지 않습니다 (MNT-{w} 워커 실행·Temporal 연결 확인)"
    return getattr(c, "message", None) or str(c)


class ProvisionSteps:
    """진행 기록·시간 제한·CORE 작업 호출 — 워크플로 클래스가 상속한다(self.inp·self.plan·self.cur 필요)."""

    async def _within(self, limit: timedelta, coro):
        """limit 안에 끝나지 않으면 진행 중인 작업을 취소하고 실패. (asyncio.wait_for 는 취소된 작업의 오류를
        그대로 올려 사유가 'Cancelled' 로 남으므로 직접 판정한다)"""
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

    # ── 진행 기록 ────────────────────────────────────────────────
    def _idx(self, code: str) -> int:
        if code == END:
            return len(self.plan)
        return next((i for i, p in enumerate(self.plan) if p["sub"] == code), self.cur)  # 없는 코드면 그대로

    async def _go(self, code: str, values: dict | None = None):
        """code 상세 단계까지 진행 — 앞선 상세 단계는 완료(값 기록), code 는 진행 중. 단계(큰 단계)도 함께 갱신."""
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

    async def _set_values(self, values: dict):
        changes = [{"sub": k, "value": v} for k, v in values.items() if v]
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
    async def _core(self, name: str, *args):
        return await workflow.execute_activity(name, args=list(args), task_queue=CORE_QUEUE,
                                               start_to_close_timeout=timedelta(seconds=30), retry_policy=DB_RETRY)
