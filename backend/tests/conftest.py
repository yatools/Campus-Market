from __future__ import annotations

import os
import shutil
import tempfile
from collections.abc import Generator

import pytest
from fastapi.testclient import TestClient

SQLITE_TEST_PATH = os.path.join(tempfile.gettempdir(), "wutong-wall-tests.db").replace("\\", "/")
TEST_DB = os.getenv("TEST_DATABASE_URL", f"sqlite:///{SQLITE_TEST_PATH}")
os.environ.update(
    {
        "ENVIRONMENT": "test",
        "DATABASE_URL": TEST_DB,
        "SECRET_KEY": "test-secret-key-that-is-long-enough-for-tests",
        "ALLOWED_CAMPUS_EMAIL_DOMAINS": "test.edu.cn",
        "COOKIE_SECURE": "false",
        "AUTO_CREATE_SCHEMA": "true",
        "DOCS_ENABLED": "true",
        "UPLOAD_DIR": os.path.join(tempfile.gettempdir(), "wutong-test-uploads"),
        "BACKUP_DIR": os.path.join(tempfile.gettempdir(), "wutong-test-backups"),
    }
)

from app.database import Base, SessionLocal, engine  # noqa: E402
from app.main import app  # noqa: E402
from app.models import VerificationCode, utcnow  # noqa: E402
from app.security import code_hash  # noqa: E402


@pytest.fixture(autouse=True)
def clean_database() -> Generator[None, None, None]:
    Base.metadata.drop_all(bind=engine)
    Base.metadata.create_all(bind=engine)
    upload_dir = os.environ["UPLOAD_DIR"]
    shutil.rmtree(upload_dir, ignore_errors=True)
    yield
    shutil.rmtree(upload_dir, ignore_errors=True)


@pytest.fixture
def client() -> Generator[TestClient, None, None]:
    with TestClient(app) as test_client:
        yield test_client


def register(client: TestClient, email: str, nickname: str = "测试同学", password: str = "SafePassword123") -> dict:
    code = "123456"
    with SessionLocal() as db:
        db.add(
            VerificationCode(
                email=email,
                purpose="register",
                code_hash=code_hash(email, "register", code),
                expires_at=utcnow().replace(year=utcnow().year + 1),
            )
        )
        db.commit()
    response = client.post(
        "/api/v1/auth/register",
        json={
            "email": email,
            "code": code,
            "password": password,
            "nickname": nickname,
            "agreed_terms_version": "test",
        },
    )
    assert response.status_code == 201, response.text
    return response.json()["user"]


def csrf(client: TestClient) -> dict[str, str]:
    return {"X-CSRF-Token": client.cookies.get("wutong_csrf") or ""}
