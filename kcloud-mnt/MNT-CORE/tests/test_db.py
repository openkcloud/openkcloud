"""DB 연결 설정(app/db.py connect_args) — 끊긴 연결을 오래 붙잡지 않는다."""
from sqlalchemy import text

from app.db import engine


def test_연결_시간_제한과_keepalive():
    with engine.connect() as conn:
        p = conn.connection.dbapi_connection.info.get_parameters()
        assert (p["connect_timeout"], p["tcp_user_timeout"]) == ("2", "3000")
        assert (p["keepalives_idle"], p["keepalives_interval"], p["keepalives_count"]) == ("60", "10", "3")


def test_버려진_트랜잭션은_DB_서버가_60초_뒤_정리():
    with engine.connect() as conn:
        assert conn.execute(text("SHOW idle_in_transaction_session_timeout")).scalar() == "1min"
