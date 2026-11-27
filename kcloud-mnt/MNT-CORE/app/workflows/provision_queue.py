"""VM 프로비저닝 시작 대기(예약) — 동시에 진행하는 VM 프로비저닝을 PROVISION_MAX 건으로 제한한다.

진행 중 = 생성 중(provisioning)이고 현재 회차의 단계가 등록된 신청(워크플로가 시작 자리를 받음).
대기 중 = 생성 중인데 현재 회차의 단계가 아직 없는 신청 — 워크플로가 자리를 기다린다(먼저 누른 순서대로 시작).
시작 판정(core.init_steps)·예상 대기 시간(프로비저닝 API·진행 조회)·되돌리기(시작 실패·대기 중 취소)가 함께 쓴다.
"""
import heapq
import json

from sqlalchemy import text
from sqlalchemy.orm import Session

from .vm_provision import PROVISION_MAX, QUEUE_WAIT_MAX, RUN_MAX

LOCK_KEY = 47030001   # 시작 판정을 한 번에 하나씩 — pg_advisory_xact_lock 키(다른 곳과 겹치지 않는 상수)
TYPICAL_DEFAULT = 60  # 완료 기록이 없을 때 VM 1건 진행 시간(초)
LEFT_MIN = 10         # 진행 중인 건이 끝나기까지 남은 시간의 최소값(초) — 평소보다 오래 걸리는 중이어도 곧 끝난다고 본다

# 현재 회차(워크플로 ID 의 끝 번호)의 단계 — 워크플로 ID = vm-provision-{신청ID}-{회차}
CURRENT_STEP = "s.request_id = r.id AND r.workflow_id = 'vm-provision-' || r.id || '-' || s.attempt"


def rows(db: Session) -> list[dict]:
    """생성 중인 VM 신청 — 먼저 누른 순서. 워크플로가 이미 끝났어야 할 만큼 지난 것은 뺀다
    (진행 중은 실행 제한 RUN_MAX, 대기 중은 대기 제한 QUEUE_WAIT_MAX + 1분 — 강제 종료 등으로 남은 기록이 자리를 막지 않게)."""
    return [dict(r) for r in db.execute(text(f"""
        SELECT r.id, r.request_spec->>'imageId' AS image_id, st.n IS NOT NULL AS running,
               extract(epoch FROM clock_timestamp() - st.started_at) AS elapsed
          FROM tkt_request r
          LEFT JOIN LATERAL (SELECT count(*) AS n, min(s.started_at) AS started_at
                               FROM tkt_provision_step s WHERE {CURRENT_STEP} HAVING count(*) > 0) st ON true
         WHERE r.type = 'vm' AND r.status = 'provisioning'
           AND r.provisioning_at > clock_timestamp()
                                   - make_interval(secs => CASE WHEN st.n IS NULL THEN :queued ELSE :alive END)
         ORDER BY r.provisioning_at, r.id"""),
        {"queued": QUEUE_WAIT_MAX.total_seconds() + 60, "alive": RUN_MAX.total_seconds()}).mappings()]


def has_room(db: Session, ticket_id: str, limit: int) -> bool:
    """이 신청이 지금 시작해도 되는지 — 진행 중 + 이 신청보다 먼저 기다린 신청 < limit."""
    rs = rows(db)
    queued = [r["id"] for r in rs if not r["running"]]
    ahead = queued.index(ticket_id) if ticket_id in queued else len(queued)
    return sum(r["running"] for r in rs) + ahead < limit


def estimate(left: list[float], ahead: list[float], limit: int) -> float:
    """시작까지 대기(초) — left: 진행 중인 건마다 끝나기까지 남은 시간, ahead: 앞서 기다리는 건의 진행 시간(순서대로).
    자리가 날 때마다 앞선 건부터 하나씩 들어간다고 보고, 이 신청 차례에 자리가 나는 시각."""
    left = sorted(max(x, LEFT_MIN) for x in left)
    slots = left[max(0, len(left) - limit):] + [0.0] * max(0, limit - len(left))  # 넘친 만큼은 먼저 끝나야 자리가 난다
    heapq.heapify(slots)
    for d in ahead:
        heapq.heappush(slots, heapq.heappop(slots) + d)
    return slots[0]


def expected_wait(db: Session, ticket_id: str | None = None) -> int:
    """시작까지 예상 대기(초) — ticket_id 가 없으면 지금 새로 넣을 신청 기준. 0 이면 바로 시작.
    VM 1건 진행 시간 = 같은 이미지의 최근 완료 10건 평균(없으면 최근 완료 20건 평균, 그마저 없으면 TYPICAL_DEFAULT) —
    큰 이미지는 부팅 볼륨 준비가 길어 이미지마다 다르다."""
    done = db.execute(text(f"""
        SELECT r.request_spec->>'imageId' AS image_id, extract(epoch FROM max(s.ended_at) - min(s.started_at)) AS sec
          FROM tkt_request r JOIN tkt_provision_step s ON {CURRENT_STEP}
         WHERE r.type = 'vm' AND r.status = 'completed'
         GROUP BY r.id ORDER BY max(s.ended_at) DESC LIMIT 200""")).all()
    recent = [float(sec) for _, sec in done[:20]]
    default = sum(recent) / len(recent) if recent else TYPICAL_DEFAULT

    def typical(image_id: str | None) -> float:
        xs = [float(sec) for img, sec in done if img == image_id][:10]
        return sum(xs) / len(xs) if xs else default

    rs = rows(db)
    left = [typical(r["image_id"]) - float(r["elapsed"] or 0) for r in rs if r["running"]]
    queued = [r for r in rs if not r["running"]]
    ids = [r["id"] for r in queued]
    ahead = queued[:ids.index(ticket_id)] if ticket_id in ids else queued
    return round(estimate(left, [typical(r["image_id"]) for r in ahead], PROVISION_MAX))


def restore(db: Session, ticket_id: str, workflow_id: str, prev: dict) -> None:
    """생성 중으로 바꾼 신청을 프로비저닝을 누르기 전 상태·값(prev)으로 되돌린다 — 시작 실패·시작 전 취소.
    이 회차(workflow_id)가 아직 생성 중일 때만."""
    db.execute(text("""UPDATE tkt_request
                          SET status = :st, provision_spec = CAST(:spec AS jsonb), provisioning_by = :by,
                              provisioning_at = CAST(:at AS timestamptz), workflow_id = :wf, error_message = :err,
                              result = CAST(:res AS jsonb), finished_by = :fby, finished_at = CAST(:fat AS timestamptz)
                        WHERE id = :id AND status = 'provisioning' AND workflow_id = :cur"""),
               {"id": ticket_id, "cur": workflow_id, "st": prev["status"], "by": prev["provisioningBy"],
                # 마감 기록 — 이 값을 넣기 전에 시작한 대기 회차의 prev 에는 없다
                "fby": prev.get("finishedBy"), "fat": prev.get("finishedAt"),
                "at": prev["provisioningAt"], "wf": prev["workflowId"], "err": prev["errorMessage"],
                "spec": json.dumps(prev["provisionSpec"], ensure_ascii=False) if prev["provisionSpec"] else None,
                "res": json.dumps(prev["result"]) if prev["result"] else None})
    db.commit()
