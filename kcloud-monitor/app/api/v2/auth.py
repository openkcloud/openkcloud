"""토큰 발급과 검증 라우터

보호된 경로의 인증은 app.auth.verify_token_or_api_key 한 곳으로 모여 있음
(JWT Bearer 또는 X-API-Key).
"""
from datetime import timedelta

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

from app.auth import check_password, create_access_token, verify_token
from app.config import Settings
from app.deps import get_settings

router = APIRouter()


class LoginRequest(BaseModel):
    """로그인 요청 본문."""

    username: str = Field(..., description="계정명")
    password: str = Field(..., description="비밀번호")


class LoginResponse(BaseModel):
    """로그인 응답."""

    access_token: str = Field(..., description="JWT. 이후 요청의 Authorization: Bearer 헤더에 넣는 값")
    token_type: str = Field(..., description="토큰 종류. 항상 bearer")
    expires_in: int = Field(..., description="토큰 유효 시간(초)")
    username: str = Field(..., description="발급 대상 계정명")


@router.post("/auth/login", response_model=LoginResponse, summary="로그인(JWT 발급)")
async def login(login_data: LoginRequest, settings: Settings = Depends(get_settings)):
    """아이디와 비밀번호로 JWT 발급

    입력 예시

    * `POST /api/v2/auth/login`
    * `{"username": "admin", "password": "비밀번호"}`

    입력 옵션

    * `username`: 계정명 (필수)
    * `password`: 비밀번호 (필수)

    응답

    * `access_token`: 이후 요청의 `Authorization: Bearer` 헤더에 넣는 값
    * `token_type`: 항상 `bearer`
    * `expires_in`: 토큰 유효 시간(초)
    * `username`: 발급 대상 계정명

    오류

    * 401: 아이디 또는 비밀번호 불일치
    """
    if not check_password(login_data.username, login_data.password, settings):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Incorrect username or password",
            headers={"WWW-Authenticate": "Bearer"},
        )
    expires = timedelta(minutes=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES)
    token = create_access_token(
        data={"sub": login_data.username}, expires_delta=expires, settings=settings
    )
    return LoginResponse(
        access_token=token,
        token_type="bearer",
        expires_in=settings.JWT_ACCESS_TOKEN_EXPIRE_MINUTES * 60,
        username=login_data.username,
    )


@router.get("/auth/verify", summary="토큰 유효성 확인")
async def verify_current_token(username: str = Depends(verify_token)):
    """요청에 담긴 토큰의 유효 여부 확인

    입력 예시

    * `GET /api/v2/auth/verify`
    * 요청 헤더에 `Authorization: Bearer <access_token>` 포함

    응답

    * `valid`: 토큰 유효 여부
    * `username`: 토큰에 담긴 계정명
    * `message`: 확인 결과 문구

    오류

    * 401: 토큰이 없거나 유효하지 않음

    참고

    * 유효할 때만 200
    """
    return {"valid": True, "username": username, "message": "Token is valid"}
