from __future__ import annotations

import hashlib
import hmac
import secrets
from datetime import timedelta

from argon2 import PasswordHasher
from argon2.exceptions import InvalidHashError, VerifyMismatchError
from fastapi import Request, Response
from sqlalchemy.orm import Session

from .config import get_settings
from .models import SessionRecord, User, utcnow

settings = get_settings()
password_hasher = PasswordHasher(time_cost=3, memory_cost=65536, parallelism=2)


def hash_password(password: str) -> str:
    return password_hasher.hash(password)


def verify_password(password: str, password_hash: str) -> bool:
    try:
        return password_hasher.verify(password_hash, password)
    except (VerifyMismatchError, InvalidHashError):
        return False


def token_hash(value: str) -> str:
    return hmac.new(settings.secret_key.encode(), value.encode(), hashlib.sha256).hexdigest()


def code_hash(email: str, purpose: str, code: str) -> str:
    return token_hash(f"{email.lower()}:{purpose}:{code}")


def new_alias() -> str:
    return f"梧桐#{secrets.randbelow(900000) + 100000}"


def create_session(db: Session, user: User, request: Request) -> tuple[str, SessionRecord]:
    raw_token = secrets.token_urlsafe(48)
    csrf = secrets.token_urlsafe(32)
    now = utcnow()
    absolute = now + timedelta(days=settings.session_absolute_days)
    record = SessionRecord(
        user_id=user.id,
        token_hash=token_hash(raw_token),
        csrf_token=csrf,
        ip_address=request.client.host if request.client else "",
        user_agent=request.headers.get("user-agent", "")[:500],
        expires_at=min(now + timedelta(days=settings.session_sliding_days), absolute),
        absolute_expires_at=absolute,
    )
    db.add(record)
    db.flush()
    return raw_token, record


def rotate_session(record: SessionRecord) -> str:
    """轮换会话与 CSRF 凭据，不改变该会话的最长有效期。"""
    raw_token = secrets.token_urlsafe(48)
    record.token_hash = token_hash(raw_token)
    record.csrf_token = secrets.token_urlsafe(32)
    return raw_token


def set_session_cookies(response: Response, raw_token: str, record: SessionRecord) -> None:
    max_age = max(0, int((record.absolute_expires_at - utcnow()).total_seconds()))
    response.set_cookie(
        settings.session_cookie_name,
        raw_token,
        max_age=max_age,
        httponly=True,
        secure=settings.cookie_secure,
        samesite="lax",
        path="/",
    )
    response.set_cookie(
        settings.csrf_cookie_name,
        record.csrf_token,
        max_age=max_age,
        httponly=False,
        secure=settings.cookie_secure,
        samesite="lax",
        path="/",
    )


def clear_session_cookies(response: Response) -> None:
    response.delete_cookie(settings.session_cookie_name, path="/")
    response.delete_cookie(settings.csrf_cookie_name, path="/")
