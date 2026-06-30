import secrets
from datetime import datetime, timedelta
from typing import Optional
from fastapi import Depends, HTTPException, status, Header
from fastapi.security import HTTPBasic, HTTPBasicCredentials, HTTPBearer, HTTPAuthorizationCredentials
from jose import JWTError, jwt
from pydantic import BaseModel

from app.config import Settings
from app.deps import get_settings

# Security schemes
security_basic = HTTPBasic()
security_bearer = HTTPBearer()
security_bearer_optional = HTTPBearer(auto_error=False)

# JWT Configuration - loaded from settings
def get_jwt_config(settings: Settings = Depends(get_settings)):
    return {
        "secret_key": settings.JWT_SECRET_KEY,
        "algorithm": settings.JWT_ALGORITHM,
        "expire_minutes": settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES
    }

class Token(BaseModel):
    access_token: str
    token_type: str
    expires_in: int

class TokenData(BaseModel):
    username: Optional[str] = None

def verify_credentials(
    credentials: HTTPBasicCredentials = Depends(security_basic), 
    settings: Settings = Depends(get_settings)
) -> str:
    """Verifies basic authentication credentials."""
    correct_username = secrets.compare_digest(credentials.username, settings.API_AUTH_USERNAME)
    correct_password = secrets.compare_digest(credentials.password, settings.API_AUTH_PASSWORD)
    
    if not (correct_username and correct_password):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Incorrect username or password",
            headers={"WWW-Authenticate": "Basic"},
        )
    return credentials.username

def create_access_token(data: dict, expires_delta: Optional[timedelta] = None, settings: Settings = None) -> str:
    """Create JWT access token."""
    if settings is None:
        from app.deps import get_settings
        settings = get_settings()
    
    to_encode = data.copy()
    if expires_delta:
        expire = datetime.utcnow() + expires_delta
    else:
        expire = datetime.utcnow() + timedelta(minutes=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES)
    
    to_encode.update({"exp": expire})
    encoded_jwt = jwt.encode(to_encode, settings.JWT_SECRET_KEY, algorithm=settings.JWT_ALGORITHM)
    return encoded_jwt

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
    x_api_key: Optional[str] = Header(None, alias="X-API-Key"),
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
