# MNT-CORE — openKcloud 포털 코어 백엔드

포털 고유 데이터(IAM·메뉴·자원신청 워크플로우·마켓·감사)를 소유하는 모듈.
**PostgreSQL(openkcloud_portal)의 유일한 소유자** — 다른 MNT 모듈은 DB에 붙지 않는다.
자원의 실시간 상태는 저장하지 않는다(MNT-IAAS/PAAS 가 상위 시스템 라이브 조회).

## 구조
```
app/
├── main.py          FastAPI 엔트리 (/healthz = DB ping 포함)
├── config.py        설정 — 전부 env 주입 (파드: ConfigMap/Secret)
├── db.py            SQLAlchemy 엔진·세션
└── routers/
    └── menus.py     GET /api/core/menus?role=admin|user — 역할별 LNB 트리
db/
├── ddl.sql          스키마 v0.1 (users·roles·user_roles·menus·role_menus)
└── seed.sql         목업 기준 시드 (계정 2·역할 2·메뉴 전체·역할 매핑)
deploy/
└── all.yaml         ConfigMap+Secret+Deployment+Service (이미지 TAG 교체 후 apply)
```

## 로컬 실행
```bash
python3 -m venv .venv && source .venv/bin/activate   # (또는 pip --user)
pip install -r requirements.txt
cp config.env.example config.env                      # DATABASE_URL 수정

# DB 연결(관리 K8s 밖이면 port-forward):
#   kubectl -n openkcloud-mnt port-forward svc/openkcloud-db-rw 5432:5432

# 스키마·시드 적용 (최초 1회):
#   psql "$DATABASE_URL과 동일 DSN" -f db/ddl.sql -f db/seed.sql

uvicorn app.main:app --reload --port 8000
# 확인: curl localhost:8000/healthz
#       curl 'localhost:8000/api/core/menus?role=admin'
```

## 설정 (env — 파드 전환 시 매핑)
| 변수 | 주입 | 설명 |
|---|---|---|
| `DATABASE_URL` | **Secret** | postgresql+psycopg DSN |
| `CORS_ORIGINS` | ConfigMap | 콤마 구분. 게이트웨이 single-origin이면 빈 값 |
| `IAAS/PAAS/OBS/AINB_API_BASE` | ConfigMap | 다른 MNT 모듈 내부 주소 (svc DNS) |

## 게이트웨이 연동
이 모듈은 `/api/core/*` 를 직접 서빙한다 → Istio VirtualService 에서
`prefix /api/core → mnt-core.openkcloud-mnt.svc:80` 매칭만 하면 됨(rewrite 불필요).
