"""
Authentication API endpoints
Provides login and token management
"""
from datetime import timedelta
from fastapi import APIRouter, Depends, HTTPException, status
from fastapi.security import HTTPBasicCredentials
from pydantic import BaseModel

from app.auth import verify_credentials, verify_token, create_access_token, Token
from app.config import Settings
from app.deps import get_settings

router = APIRouter()

class LoginRequest(BaseModel):
    username: str
    password: str

class LoginResponse(BaseModel):
    access_token: str
    token_type: str
    expires_in: int
    username: str

@router.post("/auth/login", response_model=LoginResponse, summary="🔑 Login and get access token")
async def login(
    login_data: LoginRequest,
    settings: Settings = Depends(get_settings)
):
    """
    This endpoint validates your credentials and returns a JWT token.
    """

    # Verify credentials
    if login_data.username != settings.API_AUTH_USERNAME or login_data.password != settings.API_AUTH_PASSWORD:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Incorrect username or password",
            headers={"WWW-Authenticate": "Bearer"},
        )
    
    # Create access token
    access_token_expires = timedelta(minutes=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES)
    access_token = create_access_token(
        data={"sub": login_data.username}, 
        expires_delta=access_token_expires,
        settings=settings
    )
    
    return LoginResponse(
        access_token=access_token,
        token_type="bearer",
        expires_in=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES * 60,  # in seconds
        username=login_data.username
    )

@router.post("/auth/token", response_model=Token, summary="Login with Basic Auth (Alternative)")
async def login_basic(
    username: str = Depends(verify_credentials),
    settings: Settings = Depends(get_settings)
):
    """
    Alternative login endpoint using HTTP Basic Authentication.
    Returns JWT token for subsequent requests.
    """
    access_token_expires = timedelta(minutes=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES)
    access_token = create_access_token(
        data={"sub": username}, 
        expires_delta=access_token_expires,
        settings=settings
    )
    
    return Token(
        access_token=access_token,
        token_type="bearer",
        expires_in=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES * 60
    )

@router.get("/auth/verify", summary="Verify current token")
async def verify_current_token(username: str = Depends(verify_token)):
    """
    Verify if the current JWT token is valid.
    
    This endpoint requires a valid Bearer token in the Authorization header.
    Use this to check if your token is still valid before making other API calls.
    
    Returns the username associated with the token if valid.
    """
    return {
        "valid": True,
        "username": username,
        "message": "Token is valid"
    }
