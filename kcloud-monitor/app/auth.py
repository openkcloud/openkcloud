import secrets
from datetime import datetime, timedelta
from typing import Optional
from fastapi import Depends, HTTPException, status, Header
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from jose import JWTError, jwt

from app.config import Settings
from app.deps import get_settings

# Security schemes
security_bearer = HTTPBearer()
security_bearer_optional = HTTPBearer(auto_error=False)

def check_password(username: str, password: str, settings: Settings) -> bool:
    """Compare login credentials in constant time."""
    correct_username = secrets.compare_digest(username, settings.API_AUTH_USERNAME)
    correct_password = secrets.compare_digest(password, settings.API_AUTH_PASSWORD)
    return correct_username and correct_password

def create_access_token(data: dict, expires_delta: timedelta, settings: Settings) -> str:
    """Create JWT access token."""
    to_encode = {**data, "exp": datetime.utcnow() + expires_delta}
    return jwt.encode(to_encode, settings.JWT_SECRET_KEY, algorithm=settings.JWT_ALGORITHM)

def _decode_jwt(token: str, settings: Settings) -> str:
    """Decode a JWT and return its subject (username). Raises 401 on failure."""
    credentials_exception = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Could not validate credentials",
        headers={"WWW-Authenticate": "Bearer"},
    )
    try:
        payload = jwt.decode(token, settings.JWT_SECRET_KEY, algorithms=[settings.JWT_ALGORITHM])
        username: str = payload.get("sub")
        if username is None:
            raise credentials_exception
        return username
    except JWTError:
        raise credentials_exception


def verify_token(
    credentials: HTTPAuthorizationCredentials = Depends(security_bearer),
    settings: Settings = Depends(get_settings)
) -> str:
    """Verify JWT token."""
    return _decode_jwt(credentials.credentials, settings)


def verify_token_or_api_key(
    credentials: Optional[HTTPAuthorizationCredentials] = Depends(security_bearer_optional),
    x_api_key: Optional[str] = Header(
        None,
        alias="X-API-Key",
        description="API 키 (Bearer 토큰 대신 사용, 서버에 API_KEY 가 설정된 경우만)",
    ),
    settings: Settings = Depends(get_settings),
) -> str:
    """Accept a valid API key (X-API-Key) or a JWT bearer token — parallel auth.

    The API key is checked only when ``API_KEY`` is configured; otherwise a JWT
    bearer token is required.
    """
    if settings.API_KEY and x_api_key and secrets.compare_digest(x_api_key, settings.API_KEY):
        return "api_key"
    if credentials is not None:
        return _decode_jwt(credentials.credentials, settings)
    raise HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Missing credentials: provide a Bearer token or X-API-Key",
        headers={"WWW-Authenticate": "Bearer"},
    )
