"""
Tests for Authentication API (/api/v1/auth)
"""
import pytest
from unittest.mock import patch


class TestLogin:
    """Test /api/v1/auth/login endpoint"""

    def test_login_success(self, client, test_settings):
        """Test successful login with correct credentials"""
        response = client.post(
            "/api/v1/auth/login",
            json={
                "username": test_settings.API_AUTH_USERNAME,
                "password": test_settings.API_AUTH_PASSWORD
            }
        )
        assert response.status_code == 200
        data = response.json()
        assert "access_token" in data
        assert data["token_type"] == "bearer"
        assert data["username"] == test_settings.API_AUTH_USERNAME
        assert "expires_in" in data

    def test_login_invalid_username(self, client):
        """Test login with invalid username"""
        response = client.post(
            "/api/v1/auth/login",
            json={
                "username": "wronguser",
                "password": "testpass"
            }
        )
        assert response.status_code == 401
        assert "Incorrect username or password" in response.json()["detail"]

    def test_login_invalid_password(self, client, test_settings):
        """Test login with invalid password"""
        response = client.post(
            "/api/v1/auth/login",
            json={
                "username": test_settings.API_AUTH_USERNAME,
                "password": "wrongpass"
            }
        )
        assert response.status_code == 401
        assert "Incorrect username or password" in response.json()["detail"]

    def test_login_missing_credentials(self, client):
        """Test login with missing credentials"""
        response = client.post("/api/v1/auth/login", json={})
        assert response.status_code == 422  # Validation error


class TestTokenVerification:
    """Test /api/v1/auth/verify endpoint"""

    def test_verify_valid_token(self, client, auth_headers, test_settings):
        """Test token verification with valid token"""
        response = client.get("/api/v1/auth/verify", headers=auth_headers)
        assert response.status_code == 200
        data = response.json()
        assert data["valid"] is True
        assert data["username"] == test_settings.API_AUTH_USERNAME

    def test_verify_missing_token(self, client):
        """Test token verification without token"""
        response = client.get("/api/v1/auth/verify")
        assert response.status_code == 403  # Forbidden

    def test_verify_invalid_token(self, client):
        """Test token verification with invalid token"""
        response = client.get(
            "/api/v1/auth/verify",
            headers={"Authorization": "Bearer invalid-token"}
        )
        assert response.status_code == 401  # Unauthorized


class TestBasicAuthLogin:
    """Test /api/v1/auth/token endpoint (Basic Auth)"""

    def test_token_with_basic_auth(self, client, test_settings):
        """Test getting token with HTTP Basic Auth"""
        response = client.post(
            "/api/v1/auth/token",
            auth=(test_settings.API_AUTH_USERNAME, test_settings.API_AUTH_PASSWORD)
        )
        assert response.status_code == 200
        data = response.json()
        assert "access_token" in data
        assert data["token_type"] == "bearer"

    def test_token_with_invalid_basic_auth(self, client):
        """Test basic auth with invalid credentials"""
        response = client.post(
            "/api/v1/auth/token",
            auth=("wronguser", "wrongpass")
        )
        assert response.status_code == 401
