"""CORE 워커가 실행하는 DB 기록 작업 — 진행 단계·상세 단계·티켓 상태(포털 DB 는 CORE 만 쓴다).

워크플로(vm_provision·kubernetes_provision 등)가 이름으로 부른다. 재시도돼도 결과가 같도록(멱등) 작성한다.
"""
import json

from sqlalchemy import text
from temporalio import activity
from temporalio.exceptions import ApplicationError

from ..db import SessionLocal
from . import provision_queue


@activity.defn(name="core.init_steps")
def init_steps(ticket_id: str, attempt: int, has_accelerator: bool, has_fip: bool,
               limit: int | None = None) -> list[dict] | None:
    """회차의 단계 5개와 상세 단계를 waiting 으로 등록하고 진행 순서(plan)를 돌려준다 — 이미 등록돼 있으면 plan 만.
    단계·상세 단계와 순서는 공통코드(TKT_PROVISION_STEP_IAAS / TKT_PROVISION_SUBSTEP_IAAS) 기준.
    가속기 없는 스펙은 bind_accelerator = skipped(상세 단계 없음), Floating IP 가 없으면 attach_fip 제외.
    limit 이 있으면 시작 자리 확인 — 진행 중 + 앞서 기다리는 신청이 limit 이상이면 등록하지 않고 None(provision_queue)."""
    with SessionLocal() as db:
        steps = db.execute(text("""SELECT code FROM cmn_code
                                    WHERE group_code = 'TKT_PROVISION_STEP_IAAS' AND is_active
                                    ORDER BY sort_order""")).scalars().all()
        subs = db.execute(text("""SELECT upper_code, code FROM cmn_code
                                   WHERE group_code = 'TKT_PROVISION_SUBSTEP_IAAS' AND is_active
                                   ORDER BY sort_order""")).all()
        registered = db.execute(
            text("SELECT 1 FROM tkt_provision_step WHERE request_id = :id AND attempt = :a LIMIT 1"),
            {"id": ticket_id, "a": attempt},
        ).first() is not None
        if not registered and limit is not None:
            db.execute(text("SELECT pg_advisory_xact_lock(:k)"), {"k": provision_queue.LOCK_KEY})  # 등록(커밋)까지 잡는다
            if not provision_queue.has_room(db, ticket_id, limit):
                db.rollback()
                return None
        plan = []
        for seq, step in enumerate(steps, 1):
            skipped = step == "bind_accelerator" and not has_accelerator
            codes = [] if skipped else [c for up, c in subs if up == step and (has_fip or c != "attach_fip")]
            if not registered:
                step_id = db.execute(
                    text("""INSERT INTO tkt_provision_step (request_id, attempt, seq, step_code, status)
                            VALUES (:id, :a, :seq, :code, :st) RETURNING id"""),
                    {"id": ticket_id, "a": attempt, "seq": seq, "code": step, "st": "skipped" if skipped else "waiting"},
                ).scalar()
                for i, code in enumerate(codes, 1):
                    db.execute(text("INSERT INTO tkt_provision_substep (step_id, seq, substep_code) VALUES (:s, :i, :c)"),
                               {"s": step_id, "i": i, "c": code})
            plan += [{"step": step, "sub": code} for code in codes]
        db.commit()
        return plan


@activity.defn(name="core.init_steps_ainb")
def init_steps_ainb(ticket_id: str, attempt: int) -> list[dict]:
    """AI 워크스페이스 회차의 단계·상세 단계를 waiting 으로 등록하고 진행 순서(plan)를 돌려준다 — 이미 등록돼 있으면 plan 만.
    단계·상세 단계와 순서는 공통코드(TKT_PROVISION_STEP_AINB / TKT_PROVISION_SUBSTEP_AINB) 기준."""
    with SessionLocal() as db:
        steps = db.execute(text("""SELECT code FROM cmn_code
                                    WHERE group_code = 'TKT_PROVISION_STEP_AINB' AND is_active
                                    ORDER BY sort_order""")).scalars().all()
        subs = db.execute(text("""SELECT upper_code, code FROM cmn_code
                                   WHERE group_code = 'TKT_PROVISION_SUBSTEP_AINB' AND is_active
                                   ORDER BY sort_order""")).all()
        registered = db.execute(
            text("SELECT 1 FROM tkt_provision_step WHERE request_id = :id AND attempt = :a LIMIT 1"),
            {"id": ticket_id, "a": attempt},
        ).first() is not None
        plan = []
        for seq, step in enumerate(steps, 1):
            codes = [c for up, c in subs if up == step]
            if not registered:
                step_id = db.execute(
                    text("""INSERT INTO tkt_provision_step (request_id, attempt, seq, step_code, step_group_code, status)
                            VALUES (:id, :a, :seq, :code, 'TKT_PROVISION_STEP_AINB', 'waiting') RETURNING id"""),
                    {"id": ticket_id, "a": attempt, "seq": seq, "code": step},
                ).scalar()
                for i, code in enumerate(codes, 1):
                    db.execute(text("""INSERT INTO tkt_provision_substep (step_id, seq, substep_code, substep_group_code)
                                       VALUES (:s, :i, :c, 'TKT_PROVISION_SUBSTEP_AINB')"""),
                               {"s": step_id, "i": i, "c": code})
            plan += [{"step": step, "sub": code} for code in codes]
        db.commit()
        return plan


@activity.defn(name="core.init_steps_paas")
def init_steps_paas(ticket_id: str, attempt: int) -> list[dict]:
    """클러스터 회차의 단계·상세 단계를 waiting 으로 등록하고 진행 순서(plan)를 돌려준다 — 이미 등록돼 있으면 plan 만.
    단계·상세 단계와 순서는 공통코드(TKT_PROVISION_STEP_PAAS / TKT_PROVISION_SUBSTEP_PAAS, db/paas_cluster_request.sql) 기준."""
    with SessionLocal() as db:
        steps = db.execute(text("""SELECT code FROM cmn_code
                                    WHERE group_code = 'TKT_PROVISION_STEP_PAAS' AND is_active
                                    ORDER BY sort_order""")).scalars().all()
        subs = db.execute(text("""SELECT upper_code, code FROM cmn_code
                                   WHERE group_code = 'TKT_PROVISION_SUBSTEP_PAAS' AND is_active
                                   ORDER BY sort_order""")).all()
        if not steps:
            raise ApplicationError("클러스터 생성 단계 공통코드가 없습니다(db/paas_cluster_request.sql 적용 필요)",
                                   non_retryable=True)
        registered = db.execute(
            text("SELECT 1 FROM tkt_provision_step WHERE request_id = :id AND attempt = :a LIMIT 1"),
            {"id": ticket_id, "a": attempt},
        ).first() is not None
        plan = []
        for seq, step in enumerate(steps, 1):
            codes = [c for up, c in subs if up == step]
            if not registered:
                step_id = db.execute(
                    text("""INSERT INTO tkt_provision_step (request_id, attempt, seq, step_code, step_group_code, status)
                            VALUES (:id, :a, :seq, :code, 'TKT_PROVISION_STEP_PAAS', 'waiting') RETURNING id"""),
                    {"id": ticket_id, "a": attempt, "seq": seq, "code": step},
                ).scalar()
                for i, code in enumerate(codes, 1):
                    db.execute(text("""INSERT INTO tkt_provision_substep (step_id, seq, substep_code, substep_group_code)
                                       VALUES (:s, :i, :c, 'TKT_PROVISION_SUBSTEP_PAAS')"""),
                               {"s": step_id, "i": i, "c": code})
            plan += [{"step": step, "sub": code} for code in codes]
        db.commit()
        return plan


@activity.defn(name="core.grant_cluster_access")
def grant_cluster_access(project_id: str, cluster_id: str) -> None:
    """새 클러스터에 소유 프로젝트 접근 권한(전용 — namespace NULL) 등록 → 그 프로젝트 멤버가 포털에서 클러스터를 쓴다.
    UNIQUE (project_id, cluster_id, namespace) 는 NULL 중복을 막지 못해 NOT EXISTS 로 멱등하게 넣는다."""
    with SessionLocal() as db:
        db.execute(text("""INSERT INTO paas_project_cluster (project_id, cluster_id, namespace)
                           SELECT CAST(:p AS varchar), CAST(:c AS varchar), NULL
                            WHERE NOT EXISTS (SELECT 1 FROM paas_project_cluster
                                               WHERE project_id = :p AND cluster_id = :c AND namespace IS NULL)"""),
                   {"p": project_id, "c": cluster_id})
        db.commit()


def _sets(change: dict, prefix: str, params: dict) -> list[str]:
    """변경 하나 → SET 절. 진행 중이 되면 시작 시각, 완료·실패면 종료 시각(이미 있으면 유지 — 재시도해도 같은 결과).
    건너뜀(skipped)은 시각을 남기지 않는다."""
    sets = []
    status = change.get("status")
    if status:
        params["st"] = status
        sets.append("status = :st")
        if status != "skipped":
            sets.append(f"started_at = coalesce({prefix}started_at, now())")
        if status in ("done", "failed"):
            sets.append(f"ended_at = coalesce({prefix}ended_at, now())")
    if "error" in change:
        params["err"] = change["error"]
        sets.append("error_message = :err")
    if "value" in change:
        params["val"] = None if change["value"] is None else str(change["value"])[:255]
        sets.append("value = :val")
    return sets


@activity.defn(name="core.update_steps")
def update_steps(ticket_id: str, attempt: int, changes: list[dict]) -> None:
    """단계·상세 단계 변경 묶음을 한 트랜잭션으로 기록. 변경 = {"step": 코드 | "sub": 코드, "status"?, "value"?, "error"?}."""
    with SessionLocal() as db:
        for c in changes:
            params = {"id": ticket_id, "a": attempt, "code": c.get("sub") or c["step"]}
            if "sub" in c:
                sets = _sets(c, "ss.", params)
                db.execute(text(f"""UPDATE tkt_provision_substep ss SET {", ".join(sets)}
                                      FROM tkt_provision_step s
                                     WHERE ss.step_id = s.id AND s.request_id = :id AND s.attempt = :a
                                       AND ss.substep_code = :code"""), params)
            else:
                sets = _sets(c, "", params)
                db.execute(text(f"""UPDATE tkt_provision_step SET {", ".join(sets)}
                                     WHERE request_id = :id AND attempt = :a AND step_code = :code"""), params)
        db.commit()


@activity.defn(name="core.finish")
def finish(ticket_id: str, status: str, result: dict | None, error: str | None) -> None:
    """티켓 마감 — completed(결과 식별자) 또는 failed(오류 메시지). 생성 중(provisioning)인 티켓만 바꾼다.
    마감 기록(finished_by · finished_at) — 처리자는 그 회차 프로비저닝을 시작한 관리자, 시각은 지금."""
    with SessionLocal() as db:
        db.execute(
            text("""UPDATE tkt_request SET status = :st, result = CAST(:r AS jsonb), error_message = :e,
                           finished_by = provisioning_by, finished_at = now()
                     WHERE id = :id AND status = 'provisioning'"""),
            {"id": ticket_id, "st": status, "r": json.dumps(result) if result else None, "e": error},
        )
        db.commit()


@activity.defn(name="core.cancel_queued")
def cancel_queued(ticket_id: str, workflow_id: str, prev: dict) -> None:
    """시작 대기 중 취소 — 신청을 프로비저닝을 누르기 전 상태·값으로 되돌린다(이 회차가 아직 생성 중일 때만)."""
    with SessionLocal() as db:
        provision_queue.restore(db, ticket_id, workflow_id, prev)


ALL = [init_steps, init_steps_ainb, init_steps_paas, update_steps, finish, cancel_queued, grant_cluster_access]
