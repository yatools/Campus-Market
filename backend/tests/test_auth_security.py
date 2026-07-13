from __future__ import annotations

from fastapi.testclient import TestClient
from pydantic import ValidationError

from app.config import Settings

from .conftest import csrf, register


def test_verification_code_is_never_returned(client: TestClient) -> None:
    response = client.post(
        "/api/v1/auth/request-code",
        json={"email": "new@test.edu.cn", "purpose": "register"},
    )
    assert response.status_code == 202
    assert response.json() == {"accepted": True, "resend_after": 60}
    assert "code" not in response.text.lower()


def test_mutation_requires_csrf_when_session_exists(client: TestClient) -> None:
    register(client, "csrf@test.edu.cn")
    response = client.post(
        "/api/v1/posts",
        json={"body": "这是一条没有 CSRF 头的请求", "identity_mode": "nickname"},
    )
    assert response.status_code == 403
    assert response.json()["code"] == "CSRF_INVALID"


def test_logout_revokes_server_session(client: TestClient) -> None:
    register(client, "logout@test.edu.cn")
    assert client.get("/api/v1/me").status_code == 200
    assert client.post("/api/v1/auth/logout", headers=csrf(client)).status_code == 200
    assert client.get("/api/v1/me").status_code == 401


def test_email_log_endpoint_does_not_exist(client: TestClient) -> None:
    assert client.get("/api/v1/emails").status_code == 404


def test_production_rejects_example_placeholders() -> None:
    try:
        Settings(
            _env_file=None,
            environment="production",
            public_origin="https://wall.example.edu.cn",
            secret_key="replace-with-at-least-32-random-characters",
            database_url="postgresql+psycopg://wutong:replace-with-password@db/wutong",
            allowed_campus_email_domains="stu.example.edu.cn",
            smtp_host="smtp.example.com",
            smtp_username="mailer@example.com",
            smtp_password="replace-with-smtp-secret",
            smtp_from="mailer@example.com",
        )
    except ValidationError as exc:
        assert "生产配置缺失" in str(exc)
    else:
        raise AssertionError("示例占位配置不应通过生产校验")
