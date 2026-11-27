"""자원신청 티켓(VM · AI 워크스페이스 · 서비스 클러스터) — 신청 · 목록 · 이름 중복 확인 · 승인·반려 · 프로비저닝(Temporal 워크플로) · 진행 조회.

티켓 1건 = VM 1대. 신청자·처리자는 토큰(Keycloak)의 포털 사용자 — 요청 본문의 이름은 쓰지 않는다.
OpenStack 값(프로젝트·플레이버·이미지·서버 이름)은 MNT-IAAS 라이브 조회로 확인하고,
신청 시점의 이름·스펙을 request_spec 에 함께 저장한다(표시용 스냅샷).
"""
import http.client
import ipaddress
import json
import math
import re
import urllib.error
import urllib.request
from urllib.parse import quote

from anyio import from_thread
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import text
from sqlalchemy.orm import Session

from ..auth import CurrentUser, current_user, require_admin
from ..config import settings
from ..db import get_db
from ..workflows import provision_queue, runtime
from ..workflows.vm_provision import QUEUE_WAIT_MAX

router = APIRouter(tags=["tkt_request"])

# VM 이름 = 서버 이름 = hostname. Nova hostname 규칙에서 영문은 소문자만 허용
_VM_NAME_RE = re.compile(r"^[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$")
_REQUEST_ROLES = ["owner", "member"]  # 신청 가능한 프로젝트 멤버 역할 (viewer 불가)
_MAX_VOLUME_GB = 500


def _service_get(base: str, module: str, setting: str, what: str, path: str, token: str, timeout: float = 30):
    """다른 백엔드 GET(JSON) — 요청한 사용자의 토큰을 그대로 넘긴다(그 백엔드가 같은 기준으로 권한 판정).
    주소 미설정은 503, 권한 거부(401·403)는 그대로, 그 밖의 연결·응답 실패는 502."""
    label = f"{what}({module})"
    if not base:
        raise HTTPException(503, f"{module} 주소({setting})가 설정되지 않았습니다")
    req = urllib.request.Request(base + path, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace")
        if e.code in (401, 403):
            try:
                detail = json.loads(body).get("detail") or body
            except ValueError:
                detail = body
            raise HTTPException(e.code, detail)
        raise HTTPException(502, f"{label} 실패: {body[:300] or e}")
    except (urllib.error.URLError, http.client.HTTPException, TimeoutError, ValueError) as e:
        raise HTTPException(502, f"{label} 실패: {e}")


def _iaas(path: str, token: str, timeout: float = 30):
    """MNT-IAAS GET(JSON) — OpenStack 라이브 조회."""
    base = settings.iaas_api_base and settings.iaas_api_base.rstrip("/") + "/api/iaas"
    return _service_get(base, "MNT-IAAS", "IAAS_API_BASE", "OpenStack 조회", path, token, timeout)


def _paas(path: str, token: str):
    """MNT-PAAS GET(JSON) — Magnum(클러스터 템플릿·이름) 조회. PAAS 도 모든 API 에 토큰이 필요하다."""
    base = settings.paas_api_base and settings.paas_api_base.rstrip("/") + "/api/paas"
    return _service_get(base, "MNT-PAAS", "PAAS_API_BASE", "클러스터 조회", path, token)


def _project_path(project_id: str) -> str:
    return "/projects/" + quote(project_id, safe="")


def _nova_keypair(name: str, token: str) -> dict | None:
    """OpenStack(Nova) 키페어(이름) — 관리자 신청의 키페어 목록 기준. 없으면 None."""
    return next((k for k in _iaas("/keypairs", token)["items"] if k["name"] == name), None)


def _check_nova_keypair(name: str | None, keypair_id: int | None, user: CurrentUser) -> None:
    """신청 키페어 — 사용자는 본인 SSH 키(keypairId), 관리자는 OpenStack 키페어(novaKeypair, 이름)."""
    if name is None:
        return
    if not user.is_admin:
        raise HTTPException(403, "OpenStack 키페어는 관리자만 지정할 수 있습니다")
    if keypair_id is not None:
        raise HTTPException(400, "키페어는 하나만 지정할 수 있습니다")
    if not _nova_keypair(name, user.token):
        raise HTTPException(400, "OpenStack 키페어를 찾을 수 없습니다")


def _check_requestable(db: Session, project_id: str, user: CurrentUser) -> None:
    """신청할 수 있는 프로젝트인지 — 포털 원장(iam_project)에 등록된 프로젝트의 owner·member.
    조직(org) 할당은 선택(없어도 됨). 관리자는 등록된 모든 프로젝트."""
    if user.is_admin:
        if not db.execute(text("SELECT 1 FROM iam_project WHERE project_id = :p"), {"p": project_id}).first():
            raise HTTPException(403, "신청할 수 없는 프로젝트입니다 (포털에 등록되지 않은 프로젝트)")
        return
    role = db.execute(
        text("""SELECT pm.role FROM iam_project_member pm
                  JOIN iam_project p ON p.project_id = pm.project_id
                 WHERE pm.project_id = :p AND pm.user_id = :u"""),
        {"p": project_id, "u": user.id},
    ).scalar()
    if role not in _REQUEST_ROLES:
        raise HTTPException(403, "신청할 수 없는 프로젝트입니다 (조직이 할당된 프로젝트의 owner·member 만 신청 가능)")


_SERVER_NAME_TAKEN = "이미 같은 이름의 가상머신이 있습니다."
_TICKET_NAME_TAKEN = "같은 이름으로 신청된 티켓이 있습니다."


def _server_name_taken(project_id: str, name: str, token: str) -> bool:
    """같은 프로젝트의 기존 VM(Nova)과 이름이 겹치는지. hostname 은 대소문자 구분이 없으므로 소문자로 비교."""
    names = _iaas(_project_path(project_id) + f"/server-names?name={quote(name, safe='')}", token)["names"]  # Nova 이름 필터
    return name in {n.lower() for n in names}


def _ticket_name_taken(db: Session, project_id: str, name: str) -> bool:
    """같은 프로젝트의 진행 중 VM 티켓(대기·승인·생성 중·재시도 대상 실패)과 이름이 겹치는지.
    완료된 신청은 Nova 조회(_server_name_taken)로 보므로 제외 — VM 을 지웠으면 같은 이름으로 다시 신청할 수 있다."""
    return db.execute(
        text("""SELECT 1 FROM tkt_request
                 WHERE type = 'vm' AND project_id = :p AND status IN ('pending', 'approved', 'provisioning', 'failed')
                   AND request_spec->>'vmName' = :n"""),
        {"p": project_id, "n": name},
    ).first() is not None


_SELECT = """
    SELECT r.id, r.type, r.status, r.project_id, r.request_spec, r.provision_spec, r.result, r.title,
           r.approved_at, r.provisioning_at, r.approved_reject_at, r.approved_reject_reason,
           r.provisioning_reject_at, r.provisioning_reject_reason, r.error_message, r.created_at, r.finished_at,
           u.username, u.name AS requester_name, ro.name AS requester_org_name, o.id AS org_id, o.name AS org_name,
           ap.username AS approved_by, ap.name AS approved_by_name, apo.name AS approved_by_org_name,
           pv.username AS provisioning_by, pv.name AS provisioning_by_name, pvo.name AS provisioning_by_org_name,
           ar.username AS approved_reject_by, ar.name AS approved_reject_by_name, aro.name AS approved_reject_by_org_name,
           pr.username AS provisioning_reject_by, pr.name AS provisioning_reject_by_name, pro.name AS provisioning_reject_by_org_name,
           fn.username AS finished_by, fn.name AS finished_by_name, fno.name AS finished_by_org_name
      FROM tkt_request r
      JOIN iam_user u ON u.id = r.requested_by
      LEFT JOIN iam_org ro ON ro.id = u.org_id  -- 신청자 본인 조직(관리자는 다른 조직 프로젝트에도 신청)
      JOIN iam_project p ON p.project_id = r.project_id
      LEFT JOIN iam_org o ON o.id = p.org_id  -- 프로젝트 조직은 선택(v0.4)
      LEFT JOIN iam_user ap ON ap.id = r.approved_by
      LEFT JOIN iam_user pv ON pv.id = r.provisioning_by
      LEFT JOIN iam_user ar ON ar.id = r.approved_reject_by
      LEFT JOIN iam_user pr ON pr.id = r.provisioning_reject_by
      LEFT JOIN iam_user fn ON fn.id = r.finished_by
      -- 처리자 조직(처리 기록 '이름(조직)') — 신청자 조직과 같은 기준
      LEFT JOIN iam_org apo ON apo.id = ap.org_id
      LEFT JOIN iam_org pvo ON pvo.id = pv.org_id
      LEFT JOIN iam_org aro ON aro.id = ar.org_id
      LEFT JOIN iam_org pro ON pro.id = pr.org_id
      LEFT JOIN iam_org fno ON fno.id = fn.org_id
"""


def _iso(v) -> str | None:
    return v.isoformat() if v else None


def _to_item(r) -> dict:
    """화면 ResourceRequest 형태 — request_spec 필드를 펼쳐서 내려준다."""
    return {
        **r["request_spec"],
        "id": r["id"],  # 신청 ID (req-YYMMDD-NNN)
        "type": r["type"],
        "title": r["title"],  # 신청 제목(별도 컬럼) — request_spec 과 분리
        "status": r["status"],
        "requestedBy": r["username"],
        "requestedByName": r["requester_name"],
        "requesterOrgName": r["requester_org_name"],  # 신청자 조직 — orgId·orgName 은 프로젝트 조직
        "requestedAt": r["created_at"].isoformat(),
        "orgId": str(r["org_id"]) if r["org_id"] is not None else None,
        "orgName": r["org_name"],
        "projectId": r["project_id"],
        "provisionSpec": r["provision_spec"],
        "result": r["result"],
        # 처리 기록 — 승인 단계(승인 / 반려) · 프로비저닝 단계(시작 / 반려) · 마감(완료 / 실패)
        "approvedBy": r["approved_by"],
        "approvedByName": r["approved_by_name"],
        "approvedByOrgName": r["approved_by_org_name"],
        "approvedAt": _iso(r["approved_at"]),
        "provisioningBy": r["provisioning_by"],
        "provisioningByName": r["provisioning_by_name"],
        "provisioningByOrgName": r["provisioning_by_org_name"],
        "provisioningAt": _iso(r["provisioning_at"]),
        "approvedRejectBy": r["approved_reject_by"],
        "approvedRejectByName": r["approved_reject_by_name"],
        "approvedRejectByOrgName": r["approved_reject_by_org_name"],
        "approvedRejectAt": _iso(r["approved_reject_at"]),
        "approvedRejectReason": r["approved_reject_reason"],
        "provisioningRejectBy": r["provisioning_reject_by"],
        "provisioningRejectByName": r["provisioning_reject_by_name"],
        "provisioningRejectByOrgName": r["provisioning_reject_by_org_name"],
        "provisioningRejectAt": _iso(r["provisioning_reject_at"]),
        "provisioningRejectReason": r["provisioning_reject_reason"],
        "finishedBy": r["finished_by"],  # 마감 처리자 = 그 회차 프로비저닝을 시작한 관리자
        "finishedByName": r["finished_by_name"],
        "finishedByOrgName": r["finished_by_org_name"],
        "finishedAt": _iso(r["finished_at"]),
        "errorMessage": r["error_message"],
    }


@router.get("/tickets/projects")
def requestable_projects(user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """신청할 수 있는 프로젝트 — 내가 owner·member 인 포털 등록 프로젝트(관리자는 등록된 전부).
    조직(org) 할당은 선택(LEFT JOIN — 없어도 포함). 이름은 OpenStack 라이브 조회(없거나 비활성은 제외)."""
    if user.is_admin:
        rows = db.execute(
            text("""SELECT p.project_id, 'admin' AS role, o.id AS org_id, o.name AS org_name
                      FROM iam_project p LEFT JOIN iam_org o ON o.id = p.org_id"""),
        ).mappings().all()
    else:
        rows = db.execute(
            text("""SELECT pm.project_id, pm.role, o.id AS org_id, o.name AS org_name
                      FROM iam_project_member pm
                      JOIN iam_user u ON u.id = pm.user_id
                      JOIN iam_project p ON p.project_id = pm.project_id
                      LEFT JOIN iam_org o ON o.id = p.org_id
                     WHERE u.username = :un AND pm.role = ANY(:roles)"""),
            {"un": user.username, "roles": _REQUEST_ROLES},
        ).mappings().all()
    if not rows:
        return {"items": []}
    db.rollback()  # 조회 트랜잭션을 닫고 MNT-IAAS 를 호출한다(호출 동안 커넥션을 잡지 않게)
    # 화면이 한 번에 5초씩 최대 3번 시도하는 조회 — 서버도 5초 넘게 붙잡지 않는다
    live = {p["id"]: p for p in _iaas("/projects", user.token, timeout=5)["items"] if p.get("enabled", True)}
    items = [{"id": r["project_id"], "name": live[r["project_id"]]["name"], "role": r["role"],
              "orgId": str(r["org_id"]) if r["org_id"] is not None else None, "orgName": r["org_name"]}
             for r in rows if r["project_id"] in live]
    return {"items": sorted(items, key=lambda x: x["name"])}


class RegisterProjectBody(BaseModel):
    projectId: str


@router.post("/tickets/projects", status_code=201, dependencies=[Depends(require_admin)])
def register_project(body: RegisterProjectBody, db: Session = Depends(get_db)):
    """프로젝트를 포털 원장(iam_project)에 등록(조직 미지정 = org_id NULL). 프로젝트 생성 직후 호출, 멱등."""
    pid = body.projectId.strip()
    if not pid:
        raise HTTPException(400, "projectId 가 필요합니다")
    db.execute(
        text("INSERT INTO iam_project (project_id, org_id) VALUES (:p, NULL) ON CONFLICT (project_id) DO NOTHING"),
        {"p": pid},
    )
    db.commit()
    return {"projectId": pid}


@router.get("/tickets/vm-name-check")
def vm_name_check(projectId: str, name: str, user: CurrentUser = Depends(current_user),
                  db: Session = Depends(get_db)):
    """VM 이름 사용 가능 여부(같은 프로젝트 기준)."""
    if not _VM_NAME_RE.match(name):
        return {"available": False, "reason": "영문 소문자·숫자·하이픈(-) 2~63자로 입력하세요."}
    # MNT-IAAS 를 먼저 호출 — DB 조회 트랜잭션이 열린 채로 외부 호출을 기다리지 않게
    reason = (_SERVER_NAME_TAKEN if _server_name_taken(projectId, name, user.token)
              else _TICKET_NAME_TAKEN if _ticket_name_taken(db, projectId, name) else None)
    return {"available": reason is None, "reason": reason}


@router.get("/tickets")
def list_tickets(requestedBy: str | None = None, status: str | None = None, type: str | None = None,
                 orgId: int | None = None, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """티켓 목록(최신순). 사용자는 본인 신청만(requestedBy 무시), 관리자는 전체 또는 requestedBy(username) 지정."""
    if not user.is_admin:
        requestedBy = user.username
    rows = db.execute(
        text(_SELECT + """
             WHERE (CAST(:by AS text) IS NULL OR u.username = :by)
               AND (CAST(:st AS text) IS NULL OR r.status = :st)
               AND (CAST(:tp AS text) IS NULL OR r.type = :tp)
               AND (CAST(:org AS bigint) IS NULL OR o.id = :org)
             ORDER BY split_part(r.id, '-', 2) DESC, split_part(r.id, '-', 3)::int DESC"""),  # 신청 ID(접수 순) 최신순
        {"by": requestedBy, "st": status, "tp": type, "org": orgId},
    ).mappings().all()
    items = [_to_item(r) for r in rows]
    return {"items": items, "total": len(items)}


class CreateTicketBody(BaseModel):
    requestedBy: str | None = None  # 옛 화면 호환용 — 쓰지 않음(신청자 = 토큰 사용자)
    projectId: str               # OpenStack 프로젝트 UUID
    title: str | None = None     # 신청 제목(선택) — 별도 컬럼
    vmName: str
    purpose: str = ""            # 용도(선택, 300자)
    networkType: str             # 희망 망: internal(내부망) | external(인터넷망)
    floatingIp: bool = False     # 공인 IP 희망 (인터넷망일 때만)
    flavorId: str
    imageId: str
    volumeGb: int | None = None  # 플레이버 disk=0 일 때 부팅 볼륨 크기
    keypairId: int | None = None  # iam_ssh_key.id (선택)
    novaKeypair: str | None = None  # 관리자만 — OpenStack(Nova) 키페어 이름 (선택)


@router.post("/tickets", status_code=201)
def create_ticket(body: CreateTicketBody, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """VM 자원신청(신청자 = 토큰 사용자). 저장 직전에 서버에서 다시 확인한다 — 멤버·역할, 이름 규칙·중복(DB+Nova),
    플레이버(프로젝트가 쓸 수 있는 것·가속기 device-profile 있음)·이미지(프로젝트에서 사용 가능) 존재, 부팅 볼륨 범위,
    키페어(사용자 = 본인 키, 관리자 = OpenStack 키페어)."""
    user_id = user.id
    _check_requestable(db, body.projectId, user)
    db.rollback()  # 확인용 조회 트랜잭션을 닫는다 — 아래 MNT-IAAS 호출 동안 커넥션을 잡지 않게

    if body.networkType not in ("internal", "external"):
        raise HTTPException(400, "희망 네트워크는 internal 또는 external 이어야 합니다")
    if not _VM_NAME_RE.match(body.vmName):
        raise HTTPException(400, "가상머신 이름은 영문 소문자·숫자·하이픈(-) 2~63자여야 합니다")

    project = next((p for p in _iaas("/projects", user.token)["items"]
                    if p["id"] == body.projectId and p.get("enabled", True)), None)
    if not project:
        raise HTTPException(400, "OpenStack 프로젝트를 찾을 수 없습니다")
    flavor = next((f for f in _iaas(_project_path(body.projectId) + "/flavors", user.token)["items"]
                   if f["id"] == body.flavorId), None)  # public + 이 프로젝트에 허용된 비공개
    if not flavor:
        raise HTTPException(400, "선택한 스펙(플레이버)을 찾을 수 없습니다")
    if flavor.get("deviceProfileMissing"):  # 가속기 device-profile 이 Cyborg 에 없으면 VM 을 만들 수 없다
        raise HTTPException(400, "연결된 디바이스 프로파일이 존재하지 않습니다")
    image = next((i for i in _iaas(_project_path(body.projectId) + "/images", user.token)["items"]
                  if i["id"] == body.imageId), None)
    if not image:
        raise HTTPException(400, "이 프로젝트에서 사용할 수 없는 템플릿(이미지)입니다")

    # 부팅 디스크는 항상 Cinder 부팅 볼륨 — 크기 = 플레이버 디스크(0이면 입력값), 이미지 최소 크기(가상 크기·min_disk) 이상
    volume_gb = None
    low = max(1, image.get("minVolumeGb", image["minDisk"]))  # MNT-IAAS 가 아직 minVolumeGb 를 안 주는 배포 순서여도 동작
    if flavor["diskGb"] == 0:
        if body.volumeGb is None or not low <= body.volumeGb <= _MAX_VOLUME_GB:
            raise HTTPException(400, f"부팅 볼륨 크기는 {low} ~ {_MAX_VOLUME_GB} GB 여야 합니다")
        volume_gb = body.volumeGb
    elif flavor["diskGb"] < low:
        raise HTTPException(400, f"선택한 스펙의 디스크({flavor['diskGb']} GB)가 템플릿에 필요한 최소 크기({low} GB)보다 작습니다")

    if (body.keypairId is not None or body.novaKeypair is not None) and not image["cloudInit"]:
        raise HTTPException(400, "cloud-init 이 없는 템플릿에는 키페어를 지정할 수 없습니다")
    _check_nova_keypair(body.novaKeypair, body.keypairId, user)
    if _server_name_taken(body.projectId, body.vmName, user.token):
        raise HTTPException(409, _SERVER_NAME_TAKEN)

    # 여기부터 저장까지 한 트랜잭션 — 외부 호출 없음. 같은 이름 동시 제출은 잠금으로 직렬화(커밋·롤백 시 해제)
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext(:k))"), {"k": f"tkt-vm:{body.projectId}:{body.vmName}"})
    if _ticket_name_taken(db, body.projectId, body.vmName):
        raise HTTPException(409, _TICKET_NAME_TAKEN)
    keypair = None
    if body.keypairId is not None:
        keypair = db.execute(
            text("SELECT id, name FROM iam_ssh_key WHERE id = :id AND owner = :o"),
            {"id": body.keypairId, "o": user.username},
        ).mappings().first()
        if not keypair:
            raise HTTPException(400, "본인 키페어가 아닙니다")

    # 신청 ID: req-YYMMDD-NNN (KST 날짜별 일련번호 = 그날 최대 번호 + 1). 같은 날 동시 신청은 잠금으로 직렬화
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext('tkt-request-no'))"))
    day = db.execute(text("SELECT to_char(now() AT TIME ZONE 'Asia/Seoul', 'YYMMDD')")).scalar()
    last = db.execute(
        text("SELECT max(split_part(id, '-', 3)::int) FROM tkt_request WHERE id LIKE :p"),
        {"p": f"req-{day}-%"},
    ).scalar()
    ticket_id = f"req-{day}-{(last or 0) + 1:03d}"

    spec = {
        "projectName": project["name"],
        "vmName": body.vmName,
        "purpose": body.purpose.strip()[:300],
        "networkType": body.networkType,
        "floatingIp": body.floatingIp and body.networkType == "external",
        "flavorId": flavor["id"],
        "flavorName": flavor["name"],
        "vcpu": flavor["vcpus"],
        "ramGb": flavor["ramMb"] / 1024,
        "diskGb": flavor["diskGb"],
        "volumeGb": volume_gb,
        "deviceProfile": flavor["accelerator"],
        "accelerators": flavor.get("accelerators") or None,  # 가속기 종류·개수 [{kind: GPU|NPU, count}] — 플레이버 라이브 조회 값
        "imageId": image["id"],
        "imageName": image["name"],
        "cloudInit": image["cloudInit"],
        "keypairId": str(keypair["id"]) if keypair else None,
        "keypairName": keypair["name"] if keypair else body.novaKeypair,
        "novaKeypair": body.novaKeypair,
    }
    db.execute(
        text("""INSERT INTO tkt_request (id, type, project_id, requested_by, title, request_spec)
                VALUES (:id, 'vm', :p, :u, :t, CAST(:s AS jsonb))"""),
        {"id": ticket_id, "p": body.projectId, "u": user_id,
         "t": (body.title or "").strip() or None,
         "s": json.dumps({k: v for k, v in spec.items() if v is not None}, ensure_ascii=False)},
    )
    db.commit()
    row = db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one()
    return _to_item(row)


# ── AI 워크스페이스 신청(type ai-workspace) — AI 워크스페이스 = 프로젝트 1:1 (Kubeflow Profile) ─────────
# 승인·반려는 VM 과 같은 PATCH, 프로비저닝은 아래 provision-ai-workspace(Temporal AiWorkspaceProvision).

_WS_NAME_RE = re.compile(r"^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$")


def _ainb(path: str, token: str | None = None):
    """MNT-AINB GET(JSON). 주소 미설정은 503, 연결·응답 실패는 502.
    token 을 주면 신청자 토큰을 넘긴다 — AINB 가 그 토큰으로 서비스 클러스터 접속 정보(MNT-PAAS kubeconfig)를 받아 둔다."""
    if not settings.ainb_api_base:
        raise HTTPException(503, "MNT-AINB 주소(AINB_API_BASE)가 설정되지 않았습니다")
    url = settings.ainb_api_base.rstrip("/") + "/api/ainb" + path
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"} if token else {})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        raise HTTPException(502, f"AI 노트북(MNT-AINB) 조회 실패: {e.read().decode(errors='replace')[:300] or e}")
    except (urllib.error.URLError, http.client.HTTPException, TimeoutError, ValueError) as e:
        raise HTTPException(502, f"AI 노트북(MNT-AINB) 조회 실패: {e}")


def _workspace_name(project_name: str, project_id: str) -> str:
    """프로젝트 이름 → 워크스페이스(네임스페이스) 이름: 소문자·숫자·하이픈만 63자. 남는 게 없으면 ws-<ID 앞 8자리>."""
    name = re.sub(r"-+", "-", re.sub(r"[^a-z0-9-]", "-", project_name.lower()))[:63].strip("-")
    return name if _WS_NAME_RE.match(name) else f"ws-{project_id.replace('-', '')[:8].lower()}"


class CreateAiWorkspaceBody(BaseModel):
    requestedBy: str | None = None  # 옛 화면 호환용 — 쓰지 않음(신청자 = 토큰 사용자)
    projectId: str               # OpenStack 프로젝트 UUID — 이 프로젝트의 AI 워크스페이스
    clusterId: str | None = None  # 만들 서비스 클러스터(MNT-PAAS 클러스터 ID) — AINB 가 여러 클러스터를 쓸 때(PaaS 모드) 필요
    purpose: str = ""
    cpu: int = 4
    memoryGi: int = 16
    acceleratorCount: int = 0
    storageGi: int = 50


@router.post("/tickets/ai-workspace", status_code=201)
def create_ai_workspace_ticket(body: CreateAiWorkspaceBody, user: CurrentUser = Depends(current_user),
                               db: Session = Depends(get_db)):
    """AI 워크스페이스 신청(신청자 = 토큰 사용자) — 멤버·역할, 프로젝트(라이브), 프로젝트당 1개(처리 중인 신청·기존 워크스페이스),
    워크스페이스 이름 사용 가능(MNT-AINB) 확인."""
    user_id = user.id
    _check_requestable(db, body.projectId, user)
    db.rollback()  # 확인용 조회 트랜잭션을 닫는다 — 아래 MNT-IAAS·AINB 호출 동안 커넥션을 잡지 않게

    for label, v in (("vCPU", body.cpu), ("메모리", body.memoryGi), ("저장공간", body.storageGi)):
        if v < 1:
            raise HTTPException(400, f"{label}는 1 이상이어야 합니다")
    if body.acceleratorCount < 0:
        raise HTTPException(400, "가속기는 0 이상이어야 합니다")
    project = next((p for p in _iaas("/projects", user.token)["items"]
                    if p["id"] == body.projectId and p.get("enabled", True)), None)
    if not project:
        raise HTTPException(400, "OpenStack 프로젝트를 찾을 수 없습니다")
    ws_name = _workspace_name(project["name"], body.projectId)
    cluster_q, cluster_name = "", None
    if body.clusterId:
        # 서비스 클러스터 — 사용자는 이 프로젝트에 연결된 클러스터만(관리자는 어느 클러스터든).
        # 연결에 네임스페이스가 정해져 있으면(공유 클러스터) 그 네임스페이스를 워크스페이스로 쓴다.
        row = db.execute(text("""SELECT namespace FROM paas_project_cluster
                                  WHERE project_id = :p AND cluster_id = :c
                                  ORDER BY namespace NULLS FIRST LIMIT 1"""),
                         {"p": body.projectId, "c": body.clusterId}).first()
        db.rollback()
        if row is None and not user.is_admin:
            raise HTTPException(400, "이 프로젝트에 연결된 서비스 클러스터가 아닙니다")
        if row is not None and row[0]:
            ws_name = row[0]
        try:
            cluster_name = _paas("/clusters/" + quote(body.clusterId, safe=""), user.token).get("name")
        except HTTPException:
            cluster_name = None   # 이름은 표시용 — 접근 가능 여부는 아래 AINB 확인(신청자 토큰)이 판정한다
        cluster_q = f"&clusterId={quote(body.clusterId, safe='')}"
    # 워크스페이스가 이미 있는지는 클러스터(프로젝트 라벨·이름)로 본다 — 완료된 신청이 있어도 워크스페이스를 지웠으면 다시 신청할 수 있다.
    # 신청자 토큰을 넘겨, AINB 가 그 클러스터 접속 정보가 없으면 받아 두게 한다(승인 뒤 생성은 사용자 없이 돈다).
    chk = _ainb(f"/workspace-name-check?name={quote(ws_name, safe='')}&projectId={quote(body.projectId, safe='')}"
                + cluster_q, user.token)
    if not chk["available"]:
        raise HTTPException(409, f"AI 워크스페이스를 신청할 수 없습니다 — {chk['reason']}")

    # 여기부터 저장까지 한 트랜잭션 — 같은 프로젝트 동시 신청은 잠금으로 직렬화
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext(:k))"), {"k": f"tkt-ai-workspace:{body.projectId}"})
    if db.execute(text("""SELECT 1 FROM tkt_request
                           WHERE type = 'ai-workspace' AND project_id = :p
                             AND status IN ('pending', 'approved', 'provisioning', 'failed')"""),
                  {"p": body.projectId}).first():
        raise HTTPException(409, "이 프로젝트에는 처리 중인 AI 워크스페이스 신청이 있습니다 (프로젝트당 1개)")

    db.execute(text("SELECT pg_advisory_xact_lock(hashtext('tkt-request-no'))"))
    day = db.execute(text("SELECT to_char(now() AT TIME ZONE 'Asia/Seoul', 'YYMMDD')")).scalar()
    last = db.execute(
        text("SELECT max(split_part(id, '-', 3)::int) FROM tkt_request WHERE id LIKE :p"),
        {"p": f"req-{day}-%"},
    ).scalar()
    ticket_id = f"req-{day}-{(last or 0) + 1:03d}"
    spec = {
        "projectName": project["name"],
        "workspaceName": ws_name,
        "purpose": body.purpose.strip()[:300],
        "cpu": body.cpu,
        "memoryGi": body.memoryGi,
        "acceleratorCount": body.acceleratorCount,
        "storageGi": body.storageGi,
    }
    if body.clusterId:
        spec.update(clusterId=body.clusterId, clusterName=cluster_name or body.clusterId)
    db.execute(
        text("""INSERT INTO tkt_request (id, type, project_id, requested_by, request_spec)
                VALUES (:id, 'ai-workspace', :p, :u, CAST(:s AS jsonb))"""),
        {"id": ticket_id, "p": body.projectId, "u": user_id, "s": json.dumps(spec, ensure_ascii=False)},
    )
    db.commit()
    row = db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one()
    return _to_item(row)


class ReviewBody(BaseModel):
    action: str                  # approve | reject
    processedBy: str | None = None  # 옛 화면 호환용 — 쓰지 않음(처리자 = 토큰 사용자, admin 역할)
    reason: str | None = None    # 반려 사유 (반려 시 필수)
    expectedStatus: str | None = None  # 화면이 보고 있던 상태 — 다르면 다른 관리자가 먼저 처리한 것(409)


def _admin_id(db: Session, username: str, denied: str) -> int:
    """관리자(admin 역할) 사용자 ID — 아니면 403(denied)."""
    admin_id = db.execute(
        text("""SELECT u.id FROM iam_user u
                  JOIN iam_user_role ur ON ur.user_id = u.id
                  JOIN iam_role r ON r.id = ur.role_id
                 WHERE u.username = :u AND u.status = 'active' AND r.code = 'admin'"""),
        {"u": username},
    ).scalar()
    if admin_id is None:
        raise HTTPException(403, denied)
    return admin_id


# 반려 단계별 기록 컬럼 접두어 — 대기 → 반려 = 승인 단계 반려, 승인·실패 → 반려 = 프로비저닝 단계 반려
# (실패한 회차가 만든 자원은 워크플로가 실패 때 이미 정리했다)
_REJECT_STAGE = {"pending": "approved_reject", "approved": "provisioning_reject", "failed": "provisioning_reject"}


@router.patch("/tickets/{ticket_id}")
def review_ticket(ticket_id: str, body: ReviewBody, user: CurrentUser = Depends(current_user),
                  db: Session = Depends(get_db)):
    """승인·반려(관리자).
    - 승인: 대기(pending) → 승인(approved). 승인 설정(네트워크·보안그룹)은 프로비저닝 단계에서 입력한다.
    - 반려: 대기 → 반려(approved_reject_*), 승인·실패 → 반려(provisioning_reject_*). 사유 필수."""
    if body.action not in ("approve", "reject"):
        raise HTTPException(400, "action 은 approve 또는 reject 여야 합니다")
    admin_id = _admin_id(db, user.username, "승인·반려는 관리자만 할 수 있습니다")
    status = db.execute(text("SELECT status FROM tkt_request WHERE id = :id"), {"id": ticket_id}).scalar()
    if status is None:
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    if body.expectedStatus and body.expectedStatus != status:
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")

    params = {"id": ticket_id, "cur": status, "by": admin_id}
    if body.action == "approve":
        if status != "pending":
            raise HTTPException(409, "대기중인 신청만 승인할 수 있습니다")
        sets = "status = 'approved', approved_by = :by, approved_at = now()"
    else:
        stage = _REJECT_STAGE.get(status)
        if stage is None:
            raise HTTPException(409, "대기·승인·실패 상태의 신청만 반려할 수 있습니다")
        params["reason"] = (body.reason or "").strip()
        if not params["reason"]:
            raise HTTPException(400, "반려 사유를 입력하세요")
        sets = f"status = 'rejected', {stage}_by = :by, {stage}_at = now(), {stage}_reason = :reason"

    # 읽은 상태 그대로일 때만 바꾼다 — 그사이 다른 관리자가 처리했으면 0건 → 409
    res = db.execute(text(f"UPDATE tkt_request SET {sets} WHERE id = :id AND status = :cur"), params)
    if res.rowcount == 0:
        db.rollback()
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    db.commit()
    return _to_item(db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one())


# ── 프로비저닝 — 승인 설정 확인 후 Temporal 워크플로(VmProvision) 시작 ─────────────

class ProvisionSpecBody(BaseModel):
    networkId: str
    floatingNetworkId: str | None = None  # 내부·공유망 + Floating IP 일 때 FIP 풀(external 망)
    fixedIp: str | None = None            # 고정 IP 직접 입력 — 모든 망 (없으면 DHCP 자동 할당)
    securityGroups: list[str] = []        # 보안그룹 이름 (프로젝트 라이브 조회 기준)
    host: str | None = None               # 배치할 컴퓨트 노드(미리보기에서 고름) — 노드 지정 권한이 있을 때만, 없으면 스케줄러
    # 키페어 — 보내지 않으면 신청 값, 보내면 그 OpenStack(Nova) 키페어 이름(관리자가 직접 선택), null = 넣지 않음
    novaKeypair: str | None = None


def _verified_provision_spec(project_id: str, flavor_id: str, spec: ProvisionSpecBody, token: str) -> dict:
    """승인 설정을 라이브 조회로 다시 확인하고, 네트워크 이름·종류 등은 서버 값으로 채운다."""
    nets = {n["id"]: n for n in _iaas(_project_path(project_id) + "/networks", token)["items"]}
    net = nets.get(spec.networkId)
    if not net:
        raise HTTPException(400, "이 프로젝트에서 사용할 수 없는 네트워크입니다")
    out = {"networkId": net["id"], "networkName": net["name"], "networkKind": net["kind"]}
    if spec.fixedIp:  # 직접 입력(고정 IP) — 망 종류와 무관, 없으면 DHCP 자동 할당
        chk = _iaas(f"/networks/{quote(net['id'], safe='')}/ip-check?ip={quote(spec.fixedIp, safe='')}", token)
        if not chk["available"]:
            raise HTTPException(409, f"Static IP {spec.fixedIp}: {chk['reason']}")
        out["fixedIp"] = str(ipaddress.ip_address(spec.fixedIp))  # 정규화해서 저장
    if net["kind"] == "external":
        if spec.floatingNetworkId:
            raise HTTPException(400, "외부(external) 네트워크에는 Floating IP 를 함께 지정할 수 없습니다")
    else:
        if spec.floatingNetworkId:
            fip = nets.get(spec.floatingNetworkId)
            if not fip or fip["kind"] != "external":
                raise HTTPException(400, "Floating IP 풀은 외부(external) 네트워크여야 합니다")
            # VM 을 만든 뒤 FIP 단계에서야 실패하지 않게 — 네트워크가 FIP 풀로 나가는 라우터와 연결돼 있는지 미리 확인
            chk = _iaas(f"/networks/{quote(net['id'], safe='')}/fip-reachable?floatingNetworkId={quote(fip['id'], safe='')}",
                        token)
            if not chk["reachable"]:
                raise HTTPException(400, f"{net['name']} 네트워크는 Floating IP({fip['name']})를 연결할 수 없습니다\n"
                                         "Floating IP 연관 라우터와 연결돼 있지 않습니다.")  # 서브넷별 사유는 IAAS 응답(chk)에
            out |= {"floatingNetworkId": fip["id"], "floatingNetworkName": fip["name"]}
    if not spec.securityGroups:
        raise HTTPException(400, "보안그룹을 하나 이상 선택하세요")
    sg_names = [g["name"] for g in _iaas(_project_path(project_id) + "/security-groups", token)["items"]]
    for name in spec.securityGroups:
        if sg_names.count(name) != 1:
            raise HTTPException(400, f"보안그룹 '{name}'을(를) 프로젝트에서 찾을 수 없거나 같은 이름이 여러 개입니다")
    out["securityGroups"] = list(dict.fromkeys(spec.securityGroups))  # 중복 제거, 순서 유지
    if spec.host:  # 미리보기 뒤 권한·자원이 바뀌었을 수 있다 — 지금도 지정할 수 있고 후보인지
        pv = _iaas(f"/placement-preview?flavorId={quote(flavor_id, safe='')}&projectId={quote(project_id, safe='')}", token)
        if not pv["hostPinning"]:
            raise HTTPException(409, "노드를 지정할 권한이 없습니다 — 화면을 새로 고쳐 다시 시도하세요")
        if not any(n["host"] == spec.host and n["candidate"] for n in pv["nodes"]):
            raise HTTPException(409, f"선택한 노드({spec.host})에는 지금 배치할 수 없습니다 — 미리보기를 새로 고쳐 다시 선택하세요")
        out["host"] = spec.host
    return out


def _check_fixed_ip_unassigned(db: Session, ticket_id: str, spec: dict) -> None:
    """Neutron 포트는 VM 생성 때 생기므로, 같은 Static IP 가 지정된 다른 신청(생성 중·재시도 대상 실패)도 확인.
    같은 IP 동시 지정은 잠금으로 직렬화(트랜잭션 종료 시 해제) — 저장과 같은 트랜잭션 안에서 호출할 것."""
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext(:k))"), {"k": f"tkt-ip:{spec['networkId']}:{spec['fixedIp']}"})
    other = db.execute(
        text("""SELECT id FROM tkt_request
                 WHERE id <> :id AND status IN ('provisioning', 'failed')
                   AND provision_spec->>'networkId' = :net AND provision_spec->>'fixedIp' = :ip"""),
        {"id": ticket_id, "net": spec["networkId"], "ip": spec["fixedIp"]},
    ).scalar()
    if other:
        raise HTTPException(409, f"Static IP {spec['fixedIp']}: 다른 신청({other})에 이미 지정된 IP입니다")


class ProvisionBody(BaseModel):
    processedBy: str | None = None     # 옛 화면 호환용 — 쓰지 않음(처리자 = 토큰 사용자, admin 역할)
    expectedStatus: str | None = None  # 화면이 보고 있던 상태 — 다르면 409
    provisionSpec: ProvisionSpecBody


@router.post("/tickets/{ticket_id}/provision")
def provision_ticket(ticket_id: str, body: ProvisionBody, user: CurrentUser = Depends(current_user),
                     db: Session = Depends(get_db)):
    """프로비저닝 시작(관리자) — 승인(approved)된 VM 신청, 또는 실패(failed)한 신청의 재시도.
    승인 설정(네트워크·IP·보안그룹·키페어)과 VM 이름 중복을 라이브 조회로 다시 확인하고, 생성 중(provisioning)으로 바꾼 뒤
    Temporal 워크플로를 새 회차로 시작한다(실패한 회차의 자원은 워크플로가 실패 때 정리했으므로 처음부터).
    워크플로 시작에 실패하면 이전 상태·값으로 되돌린다."""
    if not settings.temporal_api_base:
        raise HTTPException(503, "Temporal 주소(TEMPORAL_API_BASE)가 설정되지 않았습니다")
    admin_id = _admin_id(db, user.username, "프로비저닝은 관리자만 할 수 있습니다")
    t = db.execute(text("""SELECT status, type, project_id, request_spec, provision_spec, result, error_message,
                                  provisioning_by, provisioning_at, workflow_id, finished_by, finished_at
                             FROM tkt_request WHERE id = :id"""),
                   {"id": ticket_id}).mappings().first()
    if t is None:
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    if body.expectedStatus and body.expectedStatus != t["status"]:
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    if t["type"] != "vm" or t["status"] not in ("approved", "failed"):
        raise HTTPException(409, "승인되었거나 실패한 VM 신청만 프로비저닝(재시도)할 수 있습니다")
    # 동시 진행 자리가 대기 제한 안에 나지 않을 것 같으면 예약하지 않는다(provision_queue) — 예상은 신청에 남겨 예약 모달에 고정 표시
    expected = provision_queue.expected_wait(db)
    if expected >= QUEUE_WAIT_MAX.total_seconds():
        raise HTTPException(409, "워크플로가 모두 동작중 입니다. 다음에 다시 시도해주세요"
                                 f"(예상 대기시간 {int(QUEUE_WAIT_MAX.total_seconds() // 60)}분 이상)")
    rs = t["request_spec"]
    db.rollback()  # MNT-IAAS 확인 동안 커넥션을 잡지 않게

    spec = _verified_provision_spec(t["project_id"], rs["flavorId"], body.provisionSpec, user.token)
    if _server_name_taken(t["project_id"], rs["vmName"], user.token):  # 신청 뒤에 같은 이름 VM 이 생겼을 수 있다
        raise HTTPException(409, _SERVER_NAME_TAKEN)
    # 키페어 — 관리자가 바꿨으면 그 OpenStack 키페어(null = 넣지 않음), 아니면 신청 값(사용자 = 본인 SSH 키, 관리자 = OpenStack 키페어)
    key_changed = "novaKeypair" in body.provisionSpec.model_fields_set
    nova_name = body.provisionSpec.novaKeypair if key_changed else rs.get("novaKeypair")
    if key_changed:
        if nova_name and rs.get("cloudInit") is False:
            raise HTTPException(400, "cloud-init 이 없는 템플릿에는 키페어를 넣을 수 없습니다")
        spec["novaKeypair"] = nova_name  # 바꾼 키페어(재시도·상세 표시) — 없으면 신청 값
    public_key = None
    if nova_name:  # OpenStack 키페어 — 지금의 공개키를 cloud-init 으로 넣는다
        nova_key = _nova_keypair(nova_name, user.token)
        if not nova_key:
            if key_changed:
                raise HTTPException(400, "OpenStack 키페어를 찾을 수 없습니다")
            raise HTTPException(409, f"신청한 키페어({nova_name})가 삭제되어 주입할 수 없습니다")
        public_key = nova_key["publicKey"]

    # 여기부터 저장까지 한 트랜잭션
    if spec.get("fixedIp"):
        _check_fixed_ip_unassigned(db, ticket_id, spec)
    if not key_changed and rs.get("keypairId"):
        public_key = db.execute(text("SELECT public_key FROM iam_ssh_key WHERE id = :id"),
                                {"id": int(rs["keypairId"])}).scalar()
        if public_key is None:
            raise HTTPException(409, f"신청한 키페어({rs.get('keypairName')})가 삭제되어 주입할 수 없습니다")
    # 회차 — 다시 돌리면(예: 실패 후 승인 상태로 되돌려 재실행) 새 회차로 기록하고, 이전 회차 기록은 이력으로 남긴다
    attempt = db.execute(text("SELECT coalesce(max(attempt), 0) + 1 FROM tkt_provision_step WHERE request_id = :id"),
                         {"id": ticket_id}).scalar()
    workflow_id = f"vm-provision-{ticket_id}-{attempt}"
    res = db.execute(
        text("""UPDATE tkt_request
                   SET status = 'provisioning', provision_spec = CAST(:spec AS jsonb), provisioning_by = :by,
                       provisioning_at = now(), workflow_id = :wf, error_message = NULL, result = NULL,
                       finished_by = NULL, finished_at = NULL, queue_expected_sec = :qexp
                 WHERE id = :id AND status = :cur"""),
        {"id": ticket_id, "spec": json.dumps(spec, ensure_ascii=False), "by": admin_id, "wf": workflow_id,
         "cur": t["status"], "qexp": expected},
    )
    if res.rowcount == 0:
        db.rollback()
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    db.commit()

    accel = ", ".join(f"{a['kind']} × {a['count']}" for a in rs.get("accelerators") or [])
    key_name = nova_name if key_changed else rs.get("keypairName")  # 넣는 키 이름(없으면 키를 넣지 않음)
    inp = {
        "ticketId": ticket_id, "attempt": attempt, "projectId": t["project_id"],
        "vmName": rs["vmName"], "flavorId": rs["flavorId"], "imageId": rs["imageId"],
        "volumeGb": rs.get("volumeGb") or rs["diskGb"],  # 부팅 볼륨 = 플레이버 디스크(0이면 신청 입력값)
        "networkId": spec["networkId"],
        "networkLabel": spec["networkName"] + (" (external)" if spec["networkKind"] == "external" else ""),
        "fixedIp": spec.get("fixedIp"), "securityGroups": spec["securityGroups"],
        "floatingNetworkId": spec.get("floatingNetworkId"),
        "host": spec.get("host"),
        "publicKey": public_key,
        "keypairName": key_name,  # Nova 메타데이터로 남겨 가상서버 상세에 표시
        "keypairLabel": key_name or ("미적용 (cloud-init 없음)" if rs.get("cloudInit") is False else "미지정"),
        "hasAccelerator": bool(rs.get("deviceProfile")),
        "acceleratorLabel": accel or rs.get("deviceProfile"),
        # 누르기 전 상태·값 — 시작 실패·시작 전 취소 때 되돌린다(provision_queue.restore)
        "prev": {"status": t["status"], "provisionSpec": t["provision_spec"], "provisioningBy": t["provisioning_by"],
                 "provisioningAt": _iso(t["provisioning_at"]), "workflowId": t["workflow_id"],
                 "errorMessage": t["error_message"], "result": t["result"],
                 "finishedBy": t["finished_by"], "finishedAt": _iso(t["finished_at"])},
    }
    try:
        from_thread.run(runtime.start_vm_provision, workflow_id, inp)  # 이벤트 루프의 Temporal 클라이언트로 시작
    except Exception as e:  # noqa: BLE001 — 시작 실패면 이전 상태(승인·실패)와 값으로 되돌린다
        provision_queue.restore(db, ticket_id, workflow_id, inp["prev"])
        raise HTTPException(503, f"프로비저닝 워크플로를 시작하지 못했습니다: {e}")
    return _to_item(db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one())


@router.post("/tickets/{ticket_id}/provision/cancel", status_code=202)
def cancel_provision_queue(ticket_id: str, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """VM 프로비저닝 시작 대기(예약) 취소(관리자) — 워크플로에 취소 신호를 보낸다. 시작 전이면 워크플로가 신청을
    누르기 전 상태로 되돌리고 끝나고, 그사이 시작했으면 신호는 무시된다(결과는 진행 조회로 확인)."""
    _admin_id(db, user.username, "예약 취소는 관리자만 할 수 있습니다")
    t = db.execute(text("SELECT type, status, workflow_id FROM tkt_request WHERE id = :id"),
                   {"id": ticket_id}).mappings().first()
    if t is None:
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    if t["type"] != "vm" or t["status"] != "provisioning":
        raise HTTPException(409, "시작을 기다리는 VM 신청만 취소할 수 있습니다")
    started = db.execute(text(f"SELECT 1 FROM tkt_request r JOIN tkt_provision_step s ON {provision_queue.CURRENT_STEP} "
                              "WHERE r.id = :id LIMIT 1"), {"id": ticket_id}).first()
    if started:
        raise HTTPException(409, "이미 시작되어 취소할 수 없습니다")
    db.rollback()
    try:
        from_thread.run(runtime.cancel_vm_queue, t["workflow_id"])
    except Exception as e:  # noqa: BLE001
        raise HTTPException(503, f"취소 요청을 보내지 못했습니다: {e}")
    return {"accepted": True}


class ProvisionAiWorkspaceBody(BaseModel):
    processedBy: str                   # 처리자 username (admin 역할)
    expectedStatus: str | None = None  # 화면이 보고 있던 상태 — 다르면 409
    # 승인하며 조정한 자원 한도 (비우면 신청 값)
    cpu: int | None = None
    memoryGi: int | None = None
    acceleratorCount: int | None = None
    storageGi: int | None = None


@router.post("/tickets/{ticket_id}/provision-ai-workspace")
def provision_ai_workspace_ticket(ticket_id: str, body: ProvisionAiWorkspaceBody, db: Session = Depends(get_db)):
    """AI 워크스페이스 생성 시작(관리자) — 승인(approved)된 신청, 또는 실패(failed)한 신청의 재시도.
    자원 한도를 확정해 생성 중(provisioning)으로 바꾼 뒤 Temporal 워크플로(AiWorkspaceProvision)를 새 회차로 시작한다.
    워크플로 시작에 실패하면 이전 상태·값으로 되돌린다."""
    if not settings.temporal_api_base:
        raise HTTPException(503, "Temporal 주소(TEMPORAL_API_BASE)가 설정되지 않았습니다")
    admin_id = _admin_id(db, body.processedBy, "프로비저닝은 관리자만 할 수 있습니다")
    t = db.execute(text("""SELECT r.status, r.type, r.project_id, r.request_spec, r.provision_spec, r.result,
                                  r.error_message, r.provisioning_by, r.provisioning_at, r.workflow_id,
                                  u.username, u.email
                             FROM tkt_request r JOIN iam_user u ON u.id = r.requested_by
                            WHERE r.id = :id"""),
                   {"id": ticket_id}).mappings().first()
    if t is None:
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    if body.expectedStatus and body.expectedStatus != t["status"]:
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    if t["type"] != "ai-workspace" or t["status"] not in ("approved", "failed"):
        raise HTTPException(409, "승인되었거나 실패한 AI 워크스페이스 신청만 생성(재시도)할 수 있습니다")
    rs = t["request_spec"]
    spec = {k: rs[k] if getattr(body, k) is None else getattr(body, k)
            for k in ("cpu", "memoryGi", "acceleratorCount", "storageGi")}
    for label, k in (("vCPU", "cpu"), ("메모리", "memoryGi"), ("저장공간", "storageGi")):
        if spec[k] < 1:
            raise HTTPException(400, f"{label}는 1 이상이어야 합니다")
    if spec["acceleratorCount"] < 0:
        raise HTTPException(400, "가속기는 0 이상이어야 합니다")

    attempt = db.execute(text("SELECT coalesce(max(attempt), 0) + 1 FROM tkt_provision_step WHERE request_id = :id"),
                         {"id": ticket_id}).scalar()
    workflow_id = f"ai-workspace-provision-{ticket_id}-{attempt}"
    res = db.execute(
        text("""UPDATE tkt_request
                   SET status = 'provisioning', provision_spec = CAST(:spec AS jsonb), provisioning_by = :by,
                       provisioning_at = now(), workflow_id = :wf, error_message = NULL, result = NULL,
                       finished_by = NULL, finished_at = NULL
                 WHERE id = :id AND status = :cur"""),
        {"id": ticket_id, "spec": json.dumps(spec), "by": admin_id, "wf": workflow_id, "cur": t["status"]},
    )
    if res.rowcount == 0:
        db.rollback()
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    db.commit()

    inp = {
        "ticketId": ticket_id, "attempt": attempt, "projectId": t["project_id"],
        "projectName": rs.get("projectName"), "workspaceName": rs["workspaceName"],
        "clusterId": rs.get("clusterId"),   # 신청 때 고른 서비스 클러스터(PaaS 모드) — 없으면 AINB 기본 클러스터
        "ownerEmail": t["email"] or f"{t['username']}@openkcloud.local", **spec,
    }
    try:
        from_thread.run(runtime.start_ai_workspace_provision, workflow_id, inp)
    except Exception as e:  # noqa: BLE001 — 시작 실패면 이전 상태(승인·실패)와 값으로 되돌린다
        db.execute(text("""UPDATE tkt_request
                              SET status = :st, provision_spec = CAST(:spec AS jsonb), provisioning_by = :by,
                                  provisioning_at = :at, workflow_id = :wf, error_message = :err,
                                  result = CAST(:res AS jsonb)
                            WHERE id = :id AND status = 'provisioning'"""),
                   {"id": ticket_id, "st": t["status"], "by": t["provisioning_by"], "at": t["provisioning_at"],
                    "wf": t["workflow_id"], "err": t["error_message"],
                    "spec": json.dumps(t["provision_spec"]) if t["provision_spec"] else None,
                    "res": json.dumps(t["result"]) if t["result"] else None})
        db.commit()
        raise HTTPException(503, f"프로비저닝 워크플로를 시작하지 못했습니다: {e}")
    return _to_item(db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one())


# ── 서비스 클러스터 신청(type cluster) — 새 클러스터 + 프로젝트 네임스페이스 + 접근 권한(전용) ─────────
# 승인·반려는 VM 과 같은 PATCH, 프로비저닝은 아래 provision-cluster(Temporal KubernetesProvision).
# 클러스터 이름은 모든 프로젝트에서 유일 — Magnum 클러스터(MNT-PAAS) + 진행 중 신청(대기·승인·생성 중·실패).

_CLUSTER_NAME_RE = re.compile(r"^[a-z]([a-z0-9-]{0,38}[a-z0-9])?$")  # MNT-PAAS 와 같은 규칙
_CLUSTER_NAME_RULE = "클러스터 이름은 영문 소문자로 시작하고 소문자·숫자·하이픈(끝 제외)만, 40자 이내로 입력하세요."
_CLUSTER_TICKET_TAKEN = "같은 이름으로 진행 중인 클러스터 신청이 있습니다."
_CLUSTER_MASTER_COUNTS = (1, 3)
_CLUSTER_MAX_WORKERS = 20  # Worker 0 은 애드온이 배치되지 않아 완성되지 않는다(2026-10-19 시험) — 최소 1


def _cluster_ticket_taken(db: Session, name: str, exclude_id: str | None = None) -> bool:
    return db.execute(
        text("""SELECT 1 FROM tkt_request
                 WHERE type = 'cluster' AND status IN ('pending', 'approved', 'provisioning', 'failed')
                   AND request_spec->>'clusterName' = :n AND (CAST(:x AS text) IS NULL OR id <> :x)"""),
        {"n": name, "x": exclude_id},
    ).first() is not None


def _cluster_name_reason(db: Session, name: str, token: str, exclude_id: str | None = None) -> str | None:
    """클러스터 이름을 쓸 수 없는 사유(없으면 None) — 규칙 → Magnum 클러스터(MNT-PAAS, 모든 프로젝트) → 진행 중 신청.
    MNT-PAAS 를 먼저 호출 — DB 조회 트랜잭션이 열린 채로 외부 호출을 기다리지 않게."""
    if not _CLUSTER_NAME_RE.match(name):
        return _CLUSTER_NAME_RULE
    chk = _paas(f"/cluster-name-check?name={quote(name, safe='')}", token)
    if not chk["available"]:
        return chk["reason"]
    return _CLUSTER_TICKET_TAKEN if _cluster_ticket_taken(db, name, exclude_id) else None


def _namespace_name(project_name: str, project_id: str) -> str:
    """클러스터의 프로젝트 네임스페이스 — AI 워크스페이스 이름 규칙(_workspace_name) + 쿠버네티스 예약 이름(default·kube-*) 제외.
    신청 때 한 번 정해 request_spec.namespace 에 저장하고 다시 계산하지 않는다."""
    name = _workspace_name(project_name, project_id)
    return f"ws-{project_id.replace('-', '')[:8].lower()}" if name == "default" or name.startswith("kube-") else name


def _cluster_flavors(project_id: str, master_id: str, worker_id: str, token: str) -> tuple[dict, dict]:
    """Control Plane·Worker 플레이버 확인(프로젝트가 쓸 수 있는 것) — Control Plane 은 가속기 없음, Worker 는 device-profile 있음."""
    flavors = {f["id"]: f for f in _iaas(_project_path(project_id) + "/flavors", token)["items"]}
    master, worker = flavors.get(master_id), flavors.get(worker_id)
    if not master or not worker:
        raise HTTPException(400, "선택한 스펙(플레이버)을 찾을 수 없습니다")
    if master.get("accelerator"):
        raise HTTPException(400, "Control Plane 에는 가속기 스펙을 쓸 수 없습니다")
    if worker.get("deviceProfileMissing"):
        raise HTTPException(400, "연결된 디바이스 프로파일이 존재하지 않습니다")
    return master, worker


def _check_node_counts(master_count: int, node_count: int) -> None:
    if master_count not in _CLUSTER_MASTER_COUNTS:
        raise HTTPException(400, "Control Plane 수는 1 또는 3 이어야 합니다")
    if not 1 <= node_count <= _CLUSTER_MAX_WORKERS:
        raise HTTPException(400, f"Worker 수는 1 ~ {_CLUSTER_MAX_WORKERS} 이어야 합니다")


def _cluster_template(template_id: str, project_id: str, token: str) -> dict:
    """신청할 수 있는 템플릿 — 숨김이 아니고 공개이거나 신청 프로젝트 소유."""
    t = next((x for x in _paas("/clustertemplates", token)["items"] if x["id"] == template_id), None)
    if not t or t.get("hidden") or not (t.get("public") or t.get("project_id") == project_id):
        raise HTTPException(400, "신청할 수 없는 클러스터 템플릿입니다")
    return t


def _external_network_id(nets: list[dict], ref: str) -> str:
    """템플릿 외부 네트워크(ID 또는 이름 — 예전 포털 템플릿 화면은 이름으로 저장) → 외부망 ID.
    Floating IP 확인은 라우터 게이트웨이의 네트워크 ID 와 비교하므로 ID 가 필요하다. nets = 프로젝트 네트워크 목록(외부망 포함)."""
    ext = [n for n in nets if n["kind"] == "external"]
    if any(n["id"] == ref for n in ext):
        return ref
    named = [n for n in ext if n["name"] == ref]
    if not named:
        raise HTTPException(400, f"클러스터 템플릿의 외부 네트워크({ref})를 찾을 수 없습니다")
    if len(named) > 1:
        raise HTTPException(400, f"클러스터 템플릿의 외부 네트워크({ref})와 이름이 같은 외부망이 여러 개입니다 — 템플릿에서 다시 고르세요")
    return named[0]["id"]


@router.get("/tickets/cluster-name-check")
def cluster_name_check(name: str, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """클러스터 이름 사용 가능 여부(모든 프로젝트 기준)."""
    reason = _cluster_name_reason(db, name, user.token)
    return {"available": reason is None, "reason": reason}


class CreateClusterBody(BaseModel):
    projectId: str
    title: str | None = None     # 신청 제목(선택) — 별도 컬럼
    clusterName: str
    templateId: str
    masterCount: int = 1
    masterFlavorId: str
    nodeCount: int = 1
    flavorId: str                # Worker 플레이버 — 가속기는 이 플레이버의 device-profile (망·서브넷은 관리자가 프로비저닝 때)
    keypairId: int | None = None  # iam_ssh_key.id(본인 키) — 없으면 템플릿 키페어
    novaKeypair: str | None = None  # 관리자만 — OpenStack(Nova) 키페어 이름
    purpose: str = ""


@router.post("/tickets/cluster", status_code=201)
def create_cluster_ticket(body: CreateClusterBody, user: CurrentUser = Depends(current_user),
                          db: Session = Depends(get_db)):
    """서비스 클러스터 신청(신청자 = 토큰 사용자). 저장 직전에 서버에서 다시 확인한다 — 멤버·역할, 노드 수, 프로젝트,
    이름 규칙·중복(Magnum + 진행 중 신청), 템플릿(공개·숨김 아님), 플레이버(프로젝트가 쓸 수 있는 것, Control Plane 가속기 없음),
    키페어(사용자 = 본인 키, 관리자 = OpenStack 키페어)."""
    _check_requestable(db, body.projectId, user)
    db.rollback()  # 확인용 조회 트랜잭션을 닫는다 — 아래 외부 호출 동안 커넥션을 잡지 않게

    _check_node_counts(body.masterCount, body.nodeCount)
    project = next((p for p in _iaas("/projects", user.token)["items"]
                    if p["id"] == body.projectId and p.get("enabled", True)), None)
    if not project:
        raise HTTPException(400, "OpenStack 프로젝트를 찾을 수 없습니다")
    if not _CLUSTER_NAME_RE.match(body.clusterName):
        raise HTTPException(400, _CLUSTER_NAME_RULE)
    chk = _paas(f"/cluster-name-check?name={quote(body.clusterName, safe='')}", user.token)
    if not chk["available"]:
        raise HTTPException(409, chk["reason"])
    template = _cluster_template(body.templateId, body.projectId, user.token)
    master, worker = _cluster_flavors(body.projectId, body.masterFlavorId, body.flavorId, user.token)
    _check_nova_keypair(body.novaKeypair, body.keypairId, user)

    # 여기부터 저장까지 한 트랜잭션 — 외부 호출 없음. 같은 이름 동시 제출은 잠금으로 직렬화(커밋·롤백 시 해제)
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext(:k))"), {"k": f"tkt-cluster:{body.clusterName}"})
    if _cluster_ticket_taken(db, body.clusterName):
        raise HTTPException(409, _CLUSTER_TICKET_TAKEN)
    keypair = None
    if body.keypairId is not None:
        keypair = db.execute(
            text("SELECT id, name FROM iam_ssh_key WHERE id = :id AND owner = :o"),
            {"id": body.keypairId, "o": user.username},
        ).mappings().first()
        if not keypair:
            raise HTTPException(400, "본인 키페어가 아닙니다")
    db.execute(text("SELECT pg_advisory_xact_lock(hashtext('tkt-request-no'))"))
    day = db.execute(text("SELECT to_char(now() AT TIME ZONE 'Asia/Seoul', 'YYMMDD')")).scalar()
    last = db.execute(
        text("SELECT max(split_part(id, '-', 3)::int) FROM tkt_request WHERE id LIKE :p"),
        {"p": f"req-{day}-%"},
    ).scalar()
    ticket_id = f"req-{day}-{(last or 0) + 1:03d}"

    spec = {
        "projectName": project["name"],
        "clusterName": body.clusterName,
        "templateId": template["id"],
        "templateName": template["name"],
        "kubeVersion": template.get("kubeVersion") or None,
        "masterCount": body.masterCount,
        "masterFlavorId": master["id"],
        "masterFlavorName": master["name"],
        "masterVcpu": master["vcpus"],
        "masterRamGb": master["ramMb"] / 1024,
        "nodeCount": body.nodeCount,
        "flavorId": worker["id"],
        "flavorName": worker["name"],
        "vcpu": worker["vcpus"],
        "ramGb": worker["ramMb"] / 1024,
        "deviceProfile": worker["accelerator"],
        "accelerators": worker.get("accelerators") or None,
        "keypairId": str(keypair["id"]) if keypair else None,
        "keypairName": keypair["name"] if keypair else body.novaKeypair,
        "novaKeypair": body.novaKeypair,
        "namespace": _namespace_name(project["name"], body.projectId),
        "purpose": body.purpose.strip()[:300],
    }
    db.execute(
        text("""INSERT INTO tkt_request (id, type, project_id, requested_by, title, request_spec)
                VALUES (:id, 'cluster', :p, :u, :t, CAST(:s AS jsonb))"""),
        {"id": ticket_id, "p": body.projectId, "u": user.id,
         "t": (body.title or "").strip() or None,
         "s": json.dumps({k: v for k, v in spec.items() if v is not None}, ensure_ascii=False)},
    )
    db.commit()
    return _to_item(db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one())


class ProvisionClusterSpec(BaseModel):
    networkId: str
    subnetId: str
    masterCount: int | None = None      # 비우면 신청 값 — 관리자가 조정할 수 있다
    masterFlavorId: str | None = None
    nodeCount: int | None = None
    flavorId: str | None = None
    # 키페어 — 보내지 않으면 신청 값, 보내면 그 OpenStack(Nova) 키페어 이름(관리자가 직접 선택), null = 템플릿 키페어
    novaKeypair: str | None = None


class ProvisionClusterBody(BaseModel):
    expectedStatus: str | None = None  # 화면이 보고 있던 상태 — 다르면 409
    provisionSpec: ProvisionClusterSpec


def _quota_short(quota: dict, need: dict) -> list[str]:
    """쿼터(IAAS get_project_quota 형식)가 모자란 항목 문구 — 쿼터 -1 은 무제한."""
    short = []
    for key, label, unit in (("instances", "VM", "대"), ("vcpu", "vCPU", ""), ("ramGb", "RAM", "GB")):
        q, used = quota[key]["quota"], quota[key]["used"]
        if q >= 0 and used + need[key] > q:
            short.append(f"{label} 필요 {need[key]}{unit} · 남음 {max(q - used, 0)}{unit}")
    return short


@router.post("/tickets/{ticket_id}/provision-cluster")
def provision_cluster_ticket(ticket_id: str, body: ProvisionClusterBody, user: CurrentUser = Depends(current_user),
                             db: Session = Depends(get_db)):
    """클러스터 프로비저닝 시작(관리자) — 승인(approved)된 클러스터 신청, 또는 실패(failed)한 신청의 재시도.
    망·서브넷(프로젝트가 쓸 수 있는 망, DHCP 켜짐, API Floating IP 연결 가능)·플레이버·노드 수·이름·쿼터를 라이브 조회로
    확인하고, 생성 중(provisioning)으로 바꾼 뒤 Temporal 워크플로를 새 회차로 시작한다. 시작에 실패하면 이전 상태·값으로 되돌린다."""
    if not settings.temporal_api_base:
        raise HTTPException(503, "Temporal 주소(TEMPORAL_API_BASE)가 설정되지 않았습니다")
    admin_id = _admin_id(db, user.username, "프로비저닝은 관리자만 할 수 있습니다")
    t = db.execute(text("""SELECT status, type, project_id, request_spec, provision_spec, result, error_message,
                                  provisioning_by, provisioning_at, workflow_id, finished_by, finished_at
                             FROM tkt_request WHERE id = :id"""),
                   {"id": ticket_id}).mappings().first()
    if t is None:
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    if body.expectedStatus and body.expectedStatus != t["status"]:
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    if t["type"] != "cluster" or t["status"] not in ("approved", "failed"):
        raise HTTPException(409, "승인되었거나 실패한 클러스터 신청만 프로비저닝(재시도)할 수 있습니다")
    rs, ps, pid, token = t["request_spec"], body.provisionSpec, t["project_id"], user.token
    db.rollback()  # 외부 확인 동안 커넥션을 잡지 않게

    master_count = ps.masterCount or rs["masterCount"]
    node_count = ps.nodeCount or rs["nodeCount"]
    _check_node_counts(master_count, node_count)
    template = _cluster_template(rs["templateId"], pid, token)
    floating = template.get("external_network_id")
    if not floating:
        raise HTTPException(400, "클러스터 템플릿에 외부 네트워크(API Floating IP 풀)가 없습니다")
    master, worker = _cluster_flavors(pid, ps.masterFlavorId or rs["masterFlavorId"], ps.flavorId or rs["flavorId"], token)

    # 망·서브넷 — 프로젝트가 쓸 수 있는 망(외부망 제외), DHCP 켜진 서브넷, 그 서브넷으로 API 에 Floating IP 를 붙일 수 있어야.
    # 외부망에 노드를 바로 붙이면 API 로드밸런서(OVN) 주소에 밖에서 닿지 못한다(2026-10-22 req-261022-007 — No route to host)
    nets = _iaas(_project_path(pid) + "/networks", token)["items"]
    floating = _external_network_id(nets, floating)
    net = next((n for n in nets if n["id"] == ps.networkId), None)
    if not net:
        raise HTTPException(400, "이 프로젝트에서 사용할 수 없는 네트워크입니다")
    if net["kind"] == "external":
        raise HTTPException(400, "외부망에는 클러스터를 만들 수 없습니다 — 노드를 외부망에 직접 연결하면 클러스터 외부에서 API 주소(OVN 로드밸런서)에 접근할 수 없습니다")
    subnet = next((s for s in _iaas(f"/networks/{quote(net['id'], safe='')}", token)["subnets"] if s["id"] == ps.subnetId),
                  None)
    if not subnet:
        raise HTTPException(400, "서브넷이 고른 네트워크에 속하지 않습니다")
    if not subnet["dhcp"]:
        raise HTTPException(400, "DHCP 가 꺼진 서브넷에는 노드가 주소를 받지 못합니다")
    chk = _iaas(f"/networks/{quote(net['id'], safe='')}/fip-reachable?floatingNetworkId={quote(floating, safe='')}"
                f"&subnetId={quote(subnet['id'], safe='')}", token)
    if not chk["reachable"]:
        raise HTTPException(400, f"클러스터 API 에 Floating IP 를 붙일 수 없는 네트워크입니다\n{chk['reason']}")

    # 쿼터 — 노드 VM 수·vCPU·RAM. 실행 때 워크플로가 다시 본다(그 사이 다른 VM 이 쓸 수 있다)
    need = {"instances": master_count + node_count,
            "vcpu": master_count * master["vcpus"] + node_count * worker["vcpus"],
            "ramGb": math.ceil((master_count * master["ramMb"] + node_count * worker["ramMb"]) / 1024)}
    quota = _iaas(f"/placement-preview?flavorId={quote(worker['id'], safe='')}&projectId={quote(pid, safe='')}", token)["quota"]
    short = _quota_short(quota, need)
    if short:
        raise HTTPException(409, "프로젝트 쿼터가 부족합니다 — " + ", ".join(short))
    reason = _cluster_name_reason(db, rs["clusterName"], token, exclude_id=ticket_id)  # 신청 뒤 같은 이름이 생겼을 수 있다
    if reason:
        raise HTTPException(409, reason)
    # 키페어 — 이름으로 OpenStack(Nova) 키페어를 찾아 그대로 넘긴다(새로 만들지 않음). 관리자가 바꿨으면 그 값, 아니면 신청 값
    # (관리자 신청 = OpenStack 키페어, 사용자 신청 = 포털 SSH 키 이름 — 같은 이름의 OpenStack 키페어가 있어야 한다). 없으면 템플릿 키페어
    changed = "novaKeypair" in ps.model_fields_set
    key_name = ps.novaKeypair if changed else rs.get("keypairName")
    if key_name and not _nova_keypair(key_name, token):
        if changed:
            raise HTTPException(400, "OpenStack 키페어를 찾을 수 없습니다")
        raise HTTPException(409, f"신청한 키페어({key_name})가 OpenStack 에 없어 노드에 넣을 수 없습니다"
                                 " — 프로비저닝 설정에서 다른 키페어를 고르세요")
    # 키페어를 넘기지 않으면 Magnum 이 템플릿 키페어를 넣는다 — 그것도 OpenStack 에 있어야 한다(없으면 생성 요청이 거절됨)
    tpl_key = template.get("keypair_id")
    if not key_name and tpl_key and not _nova_keypair(tpl_key, token):
        raise HTTPException(409, f"템플릿 키페어({tpl_key})가 OpenStack 에 없어 노드에 넣을 수 없습니다"
                                 " — 프로비저닝 설정에서 다른 키페어를 고르세요")

    spec = {"networkId": net["id"], "networkName": net["name"], "subnetId": subnet["id"], "subnetCidr": subnet["cidr"],
            "floatingNetworkId": floating, "masterCount": master_count, "masterFlavorId": master["id"],
            "masterFlavorName": master["name"], "nodeCount": node_count, "flavorId": worker["id"],
            "flavorName": worker["name"],
            "keypairName": key_name or None}  # 넣은 OpenStack 키페어(재시도·상세 표시) — 없으면 템플릿 키페어
    attempt = db.execute(text("SELECT coalesce(max(attempt), 0) + 1 FROM tkt_provision_step WHERE request_id = :id"),
                         {"id": ticket_id}).scalar()
    workflow_id = f"cluster-provision-{ticket_id}-{attempt}"
    res = db.execute(
        text("""UPDATE tkt_request
                   SET status = 'provisioning', provision_spec = CAST(:spec AS jsonb), provisioning_by = :by,
                       provisioning_at = now(), workflow_id = :wf, error_message = NULL, result = NULL,
                       finished_by = NULL, finished_at = NULL
                 WHERE id = :id AND status = :cur"""),
        {"id": ticket_id, "spec": json.dumps(spec, ensure_ascii=False), "by": admin_id, "wf": workflow_id,
         "cur": t["status"]},
    )
    if res.rowcount == 0:
        db.rollback()
        raise HTTPException(409, "다른 관리자가 먼저 처리했습니다. 목록을 새로 고쳐 확인하세요")
    db.commit()

    inp = {"ticketId": ticket_id, "attempt": attempt, "projectId": pid, "projectName": rs["projectName"],
           "clusterName": rs["clusterName"], "templateId": rs["templateId"], "masterCount": master_count,
           "masterFlavorId": master["id"], "nodeCount": node_count, "flavorId": worker["id"],
           "networkId": net["id"], "subnetId": subnet["id"], "floatingNetworkId": floating,
           "keypair": key_name or None, "namespace": rs["namespace"], "need": need,
           "skipSetup": settings.cluster_skip_setup}  # 로컬 — K8s API 연결·네임스페이스·접근 권한 건너뜀
    prev = {"status": t["status"], "provisionSpec": t["provision_spec"], "provisioningBy": t["provisioning_by"],
            "provisioningAt": _iso(t["provisioning_at"]), "workflowId": t["workflow_id"],
            "errorMessage": t["error_message"], "result": t["result"],
            "finishedBy": t["finished_by"], "finishedAt": _iso(t["finished_at"])}
    try:
        from_thread.run(runtime.start_cluster_provision, workflow_id, inp)  # 이벤트 루프의 Temporal 클라이언트로 시작
    except Exception as e:  # noqa: BLE001 — 시작 실패면 이전 상태(승인·실패)와 값으로 되돌린다
        provision_queue.restore(db, ticket_id, workflow_id, prev)
        raise HTTPException(503, f"프로비저닝 워크플로를 시작하지 못했습니다: {e}")
    return _to_item(db.execute(text(_SELECT + " WHERE r.id = :id"), {"id": ticket_id}).mappings().one())


@router.get("/tickets/{ticket_id}/steps")
def ticket_steps(ticket_id: str, user: CurrentUser = Depends(current_user), db: Session = Depends(get_db)):
    """프로비저닝 진행(현재 회차) — 단계 5개와 상세 단계. 이름은 공통코드.
    현재 회차는 티켓의 workflow_id(vm-provision-{신청ID}-{회차})로 정한다 — 재시도 직후 새 회차 단계가 등록되기 전에는
    빈 목록을 돌려준다(가장 큰 회차로 고르면 그 사이 이전 회차 기록이 보인다).
    now = 이 응답을 읽기 시작한 DB 시각 — 단계 시각(DB now())과 같은 시계라 화면이 진행 중 시간을 서버 기준으로 그린다.
    queue = VM 시작 대기(예약) 중이면 {지금까지 기다린 초, 예상 대기 초}(provision_queue) — 자리가 있어 곧 시작하면 None.
    예상 대기는 프로비저닝을 누른 시점에 남긴 값(queue_expected_sec) 그대로 — 그때 자리가 있었는데(0) 그사이 다른 신청이
    먼저 들어갔으면 지금까지 기다린 시간 + 지금 본 예상."""
    t = db.execute(text("""SELECT type, status, result, error_message, workflow_id, requested_by, provisioning_at,
                                  queue_expected_sec, clock_timestamp() AS now
                             FROM tkt_request WHERE id = :id"""),
                   {"id": ticket_id}).mappings().first()
    if t is None or not (user.is_admin or t["requested_by"] == user.id):  # 남의 신청은 없는 것처럼
        raise HTTPException(404, "신청을 찾을 수 없습니다")
    tail = (t["workflow_id"] or "").rsplit("-", 1)[-1]
    attempt = int(tail) if tail.isdigit() else db.execute(
        text("SELECT coalesce(max(attempt), 1) FROM tkt_provision_step WHERE request_id = :id"), {"id": ticket_id},
    ).scalar()
    steps = db.execute(
        text("""SELECT s.id, s.seq, s.step_code, c.name, s.status, s.started_at, s.ended_at, s.error_message
                  FROM tkt_provision_step s
                  JOIN cmn_code c ON c.group_code = s.step_group_code AND c.code = s.step_code
                 WHERE s.request_id = :id AND s.attempt = :a
                 ORDER BY s.seq"""),
        {"id": ticket_id, "a": attempt},
    ).mappings().all()
    subs = db.execute(
        text("""SELECT ss.step_id, ss.substep_code, c.name, ss.value, ss.status, ss.started_at, ss.ended_at
                  FROM tkt_provision_substep ss
                  JOIN cmn_code c ON c.group_code = ss.substep_group_code AND c.code = ss.substep_code
                 WHERE ss.step_id = ANY(:ids)
                 ORDER BY ss.seq"""),
        {"ids": [s["id"] for s in steps]},
    ).mappings().all()
    queue = None
    if (t["type"] == "vm" and t["status"] == "provisioning" and not steps
            and not provision_queue.has_room(db, ticket_id, provision_queue.PROVISION_MAX)):
        waited = round((t["now"] - t["provisioning_at"]).total_seconds())
        queue = {"waitedSec": waited,
                 "expectedSec": t["queue_expected_sec"] or waited + provision_queue.expected_wait(db, ticket_id)}
    return {
        "status": t["status"],
        "now": _iso(t["now"]),
        "attempt": attempt,
        "errorMessage": t["error_message"],
        "result": t["result"],
        "queue": queue,
        "items": [{
            "seq": s["seq"], "code": s["step_code"], "message": s["name"], "status": s["status"],
            "startedAt": _iso(s["started_at"]), "endedAt": _iso(s["ended_at"]), "error": s["error_message"],
            "details": [{"code": d["substep_code"], "label": d["name"], "value": d["value"], "status": d["status"],
                         "startedAt": _iso(d["started_at"]), "endedAt": _iso(d["ended_at"])}
                        for d in subs if d["step_id"] == s["id"]],
        } for s in steps],
    }
