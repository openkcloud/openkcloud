"""서비스 클러스터 프로비저닝 워크플로 — CORE 워커가 실행한다. Magnum·K8s 작업은 PAAS 큐, 쿼터·망 확인은 IAAS 큐,
DB 기록은 CORE 큐의 작업으로 부른다.

진행 표시: 시작할 때 등록한 상세 단계 순서(plan, 공통코드 TKT_PROVISION_*_PAAS)를 앞에서부터 '진행 중 → 완료'로 기록한다.
실패하면 이번 회차가 만든(또는 생성 요청 응답이 끊겨 만들어졌을 수 있는) 클러스터를 삭제하고 사라질 때까지 기다린 뒤
티켓을 '실패'로 마감한다 — 재시도는 새 회차로 처음부터. 시간 값은 임시값(시험 후 조정).
이 파일은 Temporal 샌드박스에서 실행되므로 DB·HTTP 등 외부 모듈을 가져오지 않는다.
"""
from datetime import datetime, timedelta

from temporalio import workflow
from temporalio.common import RetryPolicy
from temporalio.exceptions import ActivityError, ApplicationError

from .provision_base import END, HEARTBEAT, NO_RETRY, QUERY_RETRY, ProvisionSteps, reason

IAAS_QUEUE = "okc-iaas"   # IAAS 워커: 쿼터·노드 망 확인
PAAS_QUEUE = "okc-paas"   # PAAS 워커: Magnum·K8s 작업

# ── 시간 기준(임시값 — 시험 후 조정) ─────────────────────────────────
POLL_SECONDS = 10                         # 진행 상태 확인 간격
WATCH_MAX = timedelta(seconds=60)         # 상태 확인 작업 1회 최대 점유시간(상태가 바뀌면 바로 돌아옴)
VISIBLE_MAX = timedelta(minutes=2)        # 생성 요청 뒤 GET 이 404·400 인 동안 견디는 시간(2026-10-19 시험: 몇 초)
CREATE_MAX = timedelta(minutes=45)        # 생성 요청 ~ CREATE_COMPLETE 최대(시험: 10~13분)
SETUP_MAX = timedelta(minutes=5)          # K8s API 연결·네임스페이스·접근 권한 최대
WORKFLOW_MAX = timedelta(minutes=60)      # 워크플로 전체 최대(넘으면 실패로 기록)
QUEUE_WAIT_MAX = timedelta(minutes=2)     # PAAS·IAAS 작업이 큐에서 기다리는 최대 — 넘으면 '워커 없음'
CLEANUP_MAX = timedelta(minutes=15)       # 실패 후 클러스터 삭제 완료까지 최대(시험: 76~91초)
RUN_MAX = WORKFLOW_MAX + CLEANUP_MAX + timedelta(minutes=5)  # 실행 제한(안전장치) — 진행 + 정리 + 여유

# ── 조기 실패 판정(45분까지 기다리지 않게) — 2026-10-21 기준, 시험 근거는 judge() ─────────────
NO_VM_MAX = timedelta(minutes=5)          # 노드 VM 이 안 생기면 실패(시험: 요청 31초 뒤 생김)
NO_VM_WARN = timedelta(minutes=2)         # 노드 VM 이 이만큼 안 생기면 '주의' 표시
INIT_MAX = timedelta(minutes=10)          # Control Plane VM 이 뜬 뒤 Worker VM 이 안 생기면 실패(시험: 초기화 2~2.5분)
INIT_WARN = timedelta(minutes=5)          # 그만큼 '주의' 표시 — 성공 때 초기화 시간보다 길게
PROBLEM_MAX = {                           # PAAS 가 본 문제가 이만큼 계속되면 실패
    "vm_error": timedelta(0),             # 노드 VM ERROR(용량·가속기 할당 등) — 바로
    "node_network": timedelta(0),         # 노드 콘솔에 IP·메타데이터 실패 문구 — 바로(문구 자체가 확정)
    "occm": timedelta(minutes=3),         # 클라우드 컨트롤러가 OpenStack API·DNS 에 닿지 못함(2번 이상 재시작)
}
WAITS = ("no_cp_vm", "init", "no_worker_vm")  # seen 의 대기 조건(문제 종류와 따로) — 풀리면 지우고, 다시 생기면 그때부터 센다


def judge(s: dict, now: datetime, seen: dict[str, datetime]) -> tuple[str | None, str | None]:
    """생성 중 조기 실패 판정 → (실패 사유, 주의 문구). 둘 다 없으면 정상. seen = 문제 종류·대기 조건별 처음 본 시각(갱신함,
    워크플로가 no_cp_vm 을 생성 요청 시각으로 넣어 둔다 — VM 이 있다가 사라지면 사라진 때부터).
    근거(시험): ① 노드 VM 이 안 생김 — 2026-10-21 req-261021-004 1회차(플레이버 ID, CAPO 'no flavors were found'),
    ② VM ERROR, ③ 노드가 IP·메타데이터를 못 받음 — 2026-10-19 1차(DHCP 꺼짐), ④ 클라우드 컨트롤러가 OpenStack API·DNS 에
    못 닿음 — 2026-10-20 2~5차(compute2 게이트웨이), ⑤ Control Plane 초기화 뒤 Worker 가 안 생김 — 2026-10-22
    req-261022-007(외부망 LB 주소에 밖에서 못 닿아 Cluster API 가 초기화를 확인 못 함, 44분 뒤 CP VM 이 지워짐).
    성공 때는 Control Plane VM 이 요청 31초 뒤 생기고, 뜬 뒤 2~2.5분이면 Worker VM 이 생긴다."""
    issues = []  # (실패 사유, 주의 문구, 시작 시각, 제한, 주의 시작)
    p = s.get("problem")
    if p:
        issues.append((p["message"], p["message"], seen.setdefault(p["code"], now),
                       PROBLEM_MAX.get(p["code"], PROBLEM_MAX["occm"]), timedelta(0)))
    for code in [c for c in seen if c not in WAITS and (not p or c != p["code"])]:  # 사라진 문제는 다시 생기면 처음부터
        seen.pop(code)
    minutes, init_minutes = int(NO_VM_MAX.total_seconds() // 60), int(INIT_MAX.total_seconds() // 60)
    waits = {  # 대기 조건 → (조건, 실패 사유, 주의 문구, 제한, 주의 시작)
        "no_cp_vm": (s["cpVms"] == 0, f"Control Plane VM 이 {minutes}분 안에 만들어지지 않았습니다 — OpenStack 이 노드 VM 생성을 "
                     "거절했을 수 있습니다(플레이버·이미지·키페어·쿼터 확인)", "Control Plane VM 이 아직 만들어지지 않았습니다",
                     NO_VM_MAX, NO_VM_WARN),
        # Cluster API 는 Control Plane 초기화를 확인한 뒤에 Worker VM 을 만든다
        "init": (s["cpActive"] > 0 and s["workerVms"] == 0 and not s["masterReady"],
                 f"Control Plane VM 이 뜬 뒤 {init_minutes}분 동안 Worker VM 이 만들어지지 않았습니다 — Control Plane 초기화가 "
                 "멈췄거나(노드 초기화·클러스터 API 주소 접근) OpenStack 이 Worker VM 생성을 거절했을 수 있습니다",
                 "Control Plane VM 이 뜬 뒤 Worker VM 이 아직 만들어지지 않았습니다", INIT_MAX, INIT_WARN),
        "no_worker_vm": (s["masterReady"] and s["workerVms"] == 0, f"Worker VM 이 {minutes}분 안에 만들어지지 않았습니다 — "
                         "OpenStack 이 노드 VM 생성을 거절했을 수 있습니다(플레이버·가속기·쿼터 확인)",
                         "Worker VM 이 아직 만들어지지 않았습니다", NO_VM_MAX, NO_VM_WARN),
    }
    for key, (on, fail, warn, limit, warn_at) in waits.items():
        if on:
            issues.append((fail, warn, seen.setdefault(key, now), limit, warn_at))
        else:
            seen.pop(key, None)
    for fail, _, since, limit, _ in issues:
        if now - since >= limit:
            return fail, None
    for _, warn, since, _, warn_at in issues:
        if now - since >= warn_at:
            return None, f"주의: {warn} ({int((now - since).total_seconds() // 60)}분째)"
    return None, None


@workflow.defn(name="KubernetesProvision")
class KubernetesProvision(ProvisionSteps):
    @workflow.run
    async def run(self, inp: dict) -> dict:
        self.inp = inp
        self.cur = -1  # 진행 중인 상세 단계 위치(plan 인덱스)
        self.plan: list[dict] = []
        self.requested = False  # 생성 요청을 보냈다 — 응답이 끊겨도 만들어졌을 수 있어 실패하면 정리한다
        self.cluster_id: str | None = None
        self.granted = False  # 접근 권한까지 등록 — 이후 실패(진행 기록 등)로 다 만든 클러스터를 지우지 않는다
        try:
            self.plan = await self._core("core.init_steps_paas", inp["ticketId"], inp["attempt"])
            result = await self._within(WORKFLOW_MAX, self._provision())
        except Exception as e:  # noqa: BLE001 — 어떤 실패든 진행 기록·티켓에 남긴다
            msg = reason(e)
            try:
                await self._fail_current(msg)
            except Exception:  # noqa: BLE001 — 단계 기록이 실패해도 정리·티켓 마감은 한다
                pass
            note, left = await self._cleanup()
            await self._core("core.finish", inp["ticketId"], "failed", left, msg + note)
            raise ApplicationError(msg + note, non_retryable=True) from e
        await self._core("core.finish", inp["ticketId"], "completed", result, None)
        return result

    async def _provision(self) -> dict:
        inp = self.inp
        # 클러스터 생성 가능 여부 — 신청·프로비저닝 뒤 바뀌었을 수 있어 실행 때 다시 본다(여기서 실패하면 만든 것 없음)
        await self._go("check_name")
        await self._paas("paas.check_name", inp["clusterName"])
        await self._go("check_quota", {"check_name": inp["clusterName"]})
        quota = await self._iaas("iaas.check_quota", inp["projectId"], inp["need"])
        await self._go("check_network", {"check_quota": quota})
        network = await self._iaas("iaas.check_network", inp["networkId"], inp["subnetId"], inp["floatingNetworkId"])
        await self._go("send_request", {"check_network": network})

        # 생성 요청(1회) — 다시 보내면 중복 생성 위험. 응답이 끊기면 정리 때 이름으로 찾아 지운다
        self.requested = True
        spec = {"name": inp["clusterName"], "keypair": inp.get("keypair"), **{k: inp[k] for k in (
            "templateId", "masterCount", "masterFlavorId", "nodeCount", "flavorId", "networkId", "subnetId")}}
        self.cluster_id = await self._paas("paas.create_cluster", inp["projectId"], spec, retry=NO_RETRY,
                                           timeout=timedelta(seconds=90))
        values = {"send_request": self.cluster_id}  # 끝난 상세 단계의 값
        await self._go("prepare_api", values)

        # 생성 대기 — 상세 단계를 실제 순서대로(시험 2026-10-21: API 0:30 · CP VM 4:30 · 초기화 6:35 · Worker VM 10:07 ·
        # 노드 준비 10:37 · 완료 15:41). 앞 단계가 끝날 때 다음 조건이 이미 채워져 있으면 바로 넘어간다(Worker VM 과 노드 준비는
        # 겹쳐 진행). 진행 중 문제가 보이면 그 단계에 '주의'를 남기고, 기준 시간 동안 계속되면 바로 실패한다(judge)
        m, n = inp["masterCount"], inp["nodeCount"]
        stages = [  # (상세 단계, 완료 조건, 완료 값)
            ("prepare_api", lambda s: bool(s["apiAddress"]), lambda s: s["apiAddress"]),
            ("create_cp_vm", lambda s: s["cpActive"] >= m, lambda s: f"{s['cpActive']}/{m}"),
            ("init_control_plane", lambda s: s["workerVms"] > 0 or s["masterReady"], lambda s: None),
            ("create_workers", lambda s: s["workersActive"] >= n, lambda s: f"{s['workersActive']}/{n}"),
            ("create_control_plane", lambda s: s["masterReady"], lambda s: None),  # 노드를 세지 않는다(Control Plane 노드그룹 완료)
            ("finish_cluster", lambda s: s["status"] == "CREATE_COMPLETE", lambda s: None),
        ]
        started, until, last = workflow.now(), workflow.now() + CREATE_MAX, ""
        done, note, seen = 0, "", {"no_cp_vm": started}
        while True:
            left = until - workflow.now()
            if left <= timedelta(0):
                raise ApplicationError(f"{int(CREATE_MAX.total_seconds() // 60)}분 안에 클러스터 생성이 끝나지 않았습니다"
                                       f" (마지막 상태 {last})")
            s = await self._paas("paas.watch_cluster", self.cluster_id, last, min(WATCH_MAX, left).total_seconds(),
                                 POLL_SECONDS, timeout=WATCH_MAX + timedelta(seconds=30), heartbeat=HEARTBEAT)
            last = s["key"]
            if not s["status"]:  # 생성 직후 잠깐 조회되지 않는다
                if workflow.now() - started > VISIBLE_MAX:
                    raise ApplicationError("생성 요청 뒤 클러스터가 조회되지 않습니다")
                continue
            if s["status"].endswith("FAILED"):
                raise ApplicationError(f"클러스터 생성 실패({s['status']}): {s['reason'] or '사유 없음'}")
            moved = False
            while done < len(stages) and stages[done][1](s):
                values[stages[done][0]] = stages[done][2](s)
                done, moved = done + 1, True
            if moved:
                # 로컬(skipSetup)은 설정 단계로 넘어가지 않는다 — _skip_setup 이 건너뜀으로 마감(시작 시각을 남기지 않게)
                if done < len(stages) or not inp.get("skipSetup"):
                    await self._go(stages[done][0] if done < len(stages) else "check_api", values)
                note = ""
            if s["status"] == "CREATE_COMPLETE":
                break
            fail, warn = judge(s, workflow.now(), seen)
            if fail:
                raise ApplicationError(fail)
            if (warn or "") != note and 0 <= self.cur < len(self.plan):
                note = warn or ""
                await self._core("core.update_steps", inp["ticketId"], inp["attempt"],
                                 [{"sub": self.plan[self.cur]["sub"], "value": warn}])
        # 로컬(CLUSTER_SKIP_SETUP) — 입력에 있을 때만. 입력으로 정하므로 이 값 없이 시작한 실행은 경로가 그대로다(재생 안전)
        if inp.get("skipSetup"):
            return await self._skip_setup(values)
        await self._go("check_api", values)
        return await self._within(SETUP_MAX, self._setup())

    async def _skip_setup(self, values: dict) -> dict:
        """로컬 — 포털(PAAS)이 새 클러스터 K8s API 에 닿지 못하므로 K8s API 연결 · 네임스페이스 · 접근 권한 단계를 건너뜀으로
        기록하고 완료한다(네임스페이스·접근 권한은 만들지 않는다 — 결과에 namespace 없음). 앞선 상세 단계는 완료(값 기록)."""
        inp, start, changes = self.inp, self._idx("check_api"), []
        rest = self.plan[max(self.cur, 0):]
        for i, p in enumerate(rest, max(self.cur, 0)):
            changes.append({"sub": p["sub"], "status": "done", "value": values.get(p["sub"])} if i < start
                           else {"sub": p["sub"], "status": "skipped"})
        for step in dict.fromkeys(p["step"] for p in rest):  # 남은 큰 단계 — 상세 단계가 모두 건너뜀이면 건너뜀
            first = next(i for i, p in enumerate(self.plan) if p["step"] == step)
            changes.append({"step": step, "status": "skipped" if first >= start else "done"})
        self.cur = len(self.plan)
        await self._core("core.update_steps", inp["ticketId"], inp["attempt"], changes)
        return {"clusterId": self.cluster_id, "clusterName": inp["clusterName"], "apiAddress": values.get("prepare_api") or ""}

    async def _setup(self) -> dict:
        """K8s API 연결 → 프로젝트 네임스페이스 → 접근 권한(소유 프로젝트, 전용)."""
        inp = self.inp
        try:
            api = await self._paas("paas.check_api", self.cluster_id)
        except ActivityError as e:  # 대개 포털(PAAS)이 클러스터 API 망에 닿지 않음 — 원래 오류는 원인으로 남긴다
            raise ApplicationError("포털이 클러스터 K8s API 에 접근하지 못했습니다.") from e
        await self._go("create_namespace", {"check_api": api})
        namespace = await self._paas("paas.create_namespace", self.cluster_id, inp["namespace"], inp["projectId"],
                                     inp["projectName"])
        await self._go("grant_access", {"create_namespace": namespace})
        await self._core("core.grant_cluster_access", inp["projectId"], self.cluster_id)
        self.granted = True
        await self._go(END, {"grant_access": inp["projectName"]})
        return {"clusterId": self.cluster_id, "clusterName": inp["clusterName"], "namespace": namespace, "apiAddress": api}

    async def _cleanup(self) -> tuple[str, dict | None]:
        """실패한 회차의 클러스터 삭제 → (실패 사유에 덧붙일 문구, 티켓 result). 정리하지 못하면 클러스터 ID 를 남긴다."""
        if not self.requested or self.granted:
            return "", ({"clusterId": self.cluster_id} if self.granted else None)
        deadline = (workflow.now() + CLEANUP_MAX).timestamp()
        try:
            deleted = await self._paas("paas.delete_cluster", self.inp["projectId"], self.inp["clusterName"],
                                       self.cluster_id, deadline, timeout=CLEANUP_MAX + timedelta(seconds=30),
                                       heartbeat=HEARTBEAT)
        except Exception as e:  # noqa: BLE001 — 정리 실패는 사유에 남기고 클러스터 ID 를 결과에 보존
            return (f" — 생성한 클러스터 정리 실패, 확인 필요(clusterId={self.cluster_id or '응답 없음'}): {reason(e)}",
                    {"clusterId": self.cluster_id, "clusterName": self.inp["clusterName"]})
        return (" — 생성한 클러스터는 삭제함" if deleted else ""), None

    # ── 작업 호출 ────────────────────────────────────────────────
    async def _paas(self, name: str, *args, retry: RetryPolicy = QUERY_RETRY, timeout: timedelta = timedelta(seconds=60),
                    heartbeat: timedelta | None = None):
        return await workflow.execute_activity(name, args=list(args), task_queue=PAAS_QUEUE, start_to_close_timeout=timeout,
                                               schedule_to_start_timeout=QUEUE_WAIT_MAX, heartbeat_timeout=heartbeat,
                                               retry_policy=retry)

    async def _iaas(self, name: str, *args):
        return await workflow.execute_activity(name, args=list(args), task_queue=IAAS_QUEUE,
                                               start_to_close_timeout=timedelta(seconds=60),
                                               schedule_to_start_timeout=QUEUE_WAIT_MAX, retry_policy=QUERY_RETRY)
