"""SQLAlchemy 엔진·세션. DATABASE_URL 하나로 어디서든(로컬/파드) 동작한다."""
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from .config import settings

engine = create_engine(
    settings.database_url,
    pool_pre_ping=True,   # 끊어진 커넥션 자동 감지(파드 재시작·네트워크 단절 대비)
    pool_size=5,
    max_overflow=5,
    # 끊긴 연결을 오래 붙잡지 않게 — 화면 조회 한 번(5초) 안에 감지·재연결(libpq 연결 옵션)
    connect_args={
        "connect_timeout": 2,       # 새 연결 2초(psycopg 최소값, 기본 130초)
        "tcp_user_timeout": 3000,   # 보낸 데이터의 ACK 를 3초 못 받으면 끊는다(리눅스만, 기본은 OS 재전송 한도 약 15분)
        "keepalives_idle": 60,      # 60초 쉬면 탐침(10초 간격 3번) — 쉬는 동안 죽은 연결을 미리 닫고 중간 장비 기록 유지
        "keepalives_interval": 10,
        "keepalives_count": 3,
        # 위 3초에 버려진 트랜잭션이 잠금(신청 번호·시작 자리)을 쥔 채 남지 않게 DB 서버가 60초 뒤 정리
        "options": "-c idle_in_transaction_session_timeout=60000",
    },
)

SessionLocal = sessionmaker(bind=engine, autoflush=False, autocommit=False)


def get_db():
    """FastAPI 의존성 — 요청 단위 세션."""
    db = SessionLocal()
    try:
        yield db
    finally:
        db.close()
