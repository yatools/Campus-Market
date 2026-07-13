from __future__ import annotations

from datetime import timedelta

from fastapi import Depends, Request, Response
from sqlalchemy import select
from sqlalchemy.orm import Session

from .config import get_settings
from .database import get_db
from .errors import APIError
from .models import SessionRecord, User, utcnow
from .security import rotate_session, set_session_cookies, token_hash

settings = get_settings()


def get_session_record(request: Request, response: Response, db: Session = Depends(get_db)) -> SessionRecord:
    raw = request.cookies.get(settings.session_cookie_name, "")
    if not raw:
        raise APIError(401, "AUTH_REQUIRED", "请先登录")
    record = db.scalar(select(SessionRecord).where(SessionRecord.token_hash == token_hash(raw)))
    now = utcnow()
    if not record or record.revoked_at or record.expires_at <= now or record.absolute_expires_at <= now:
        raise APIError(401, "SESSION_EXPIRED", "登录已过期，请重新登录")
    if record.last_seen_at <= now - timedelta(hours=settings.session_rotation_hours):
        record.last_seen_at = now
        record.expires_at = min(now + timedelta(days=settings.session_sliding_days), record.absolute_expires_at)
        raw_token = rotate_session(record)
        set_session_cookies(response, raw_token, record)
    request.state.session = record
    return record


def current_user(
    record: SessionRecord = Depends(get_session_record),
    db: Session = Depends(get_db),
) -> User:
    user = db.get(User, record.user_id)
    if not user or user.status in {"disabled", "deleted"}:
        raise APIError(403, "ACCOUNT_DISABLED", "账号已停用")
    return user


def participating_user(user: User = Depends(current_user)) -> User:
    """允许限权账号管理自身资料和申诉，但禁止继续发布或互动。"""
    if user.status == "restricted":
        raise APIError(403, "ACCOUNT_RESTRICTED", "账号当前处于限权状态，不能发布或互动")
    return user


def optional_user(request: Request, db: Session = Depends(get_db)) -> User | None:
    raw = request.cookies.get(settings.session_cookie_name, "")
    if not raw:
        return None
    record = db.scalar(select(SessionRecord).where(SessionRecord.token_hash == token_hash(raw)))
    now = utcnow()
    if not record or record.revoked_at or record.expires_at <= now or record.absolute_expires_at <= now:
        return None
    user = db.get(User, record.user_id)
    return user if user and user.status not in {"disabled", "deleted"} else None


def moderator_user(user: User = Depends(current_user)) -> User:
    if user.status != "active":
        raise APIError(403, "ACCOUNT_RESTRICTED", "账号当前状态不能执行审核操作")
    if user.role not in {"moderator", "admin"}:
        raise APIError(403, "MODERATOR_REQUIRED", "需要审核员权限")
    return user


def admin_user(user: User = Depends(current_user)) -> User:
    if user.status != "active":
        raise APIError(403, "ACCOUNT_RESTRICTED", "账号当前状态不能执行管理操作")
    if user.role != "admin":
        raise APIError(403, "ADMIN_REQUIRED", "需要管理员权限")
    return user
