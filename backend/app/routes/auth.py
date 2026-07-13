from __future__ import annotations

import secrets
from datetime import timedelta

from fastapi import APIRouter, Depends, Request, Response
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import desc, select, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from ..config import get_settings
from ..database import get_db
from ..deps import current_user, get_session_record
from ..errors import APIError
from ..models import SessionRecord, User, VerificationCode, utcnow
from ..security import (
    clear_session_cookies,
    code_hash,
    create_session,
    hash_password,
    new_alias,
    set_session_cookies,
    verify_password,
)
from ..services import audit, check_rate_limit, enqueue_email, normalize_email, notify, require_campus_email

router = APIRouter(prefix="/auth", tags=["认证"])
settings = get_settings()


class CodeRequest(BaseModel):
    email: str = Field(min_length=5, max_length=320)
    purpose: str = "register"

    @field_validator("purpose")
    @classmethod
    def validate_purpose(cls, value: str) -> str:
        if value not in {"register", "reset_password", "change_email"}:
            raise ValueError("不支持的验证码用途")
        return value


class RegisterRequest(BaseModel):
    email: str
    code: str = Field(pattern=r"^\d{6}$")
    password: str = Field(min_length=10, max_length=128)
    nickname: str = Field(min_length=2, max_length=20)
    agreed_terms_version: str = Field(min_length=1, max_length=30)


class LoginRequest(BaseModel):
    email: str
    password: str = Field(min_length=1, max_length=128)


class PasswordResetRequest(BaseModel):
    email: str
    code: str = Field(pattern=r"^\d{6}$")
    new_password: str = Field(min_length=10, max_length=128)


def user_payload(user: User) -> dict:
    return {
        "id": user.id,
        "email": user.email,
        "nickname": user.nickname,
        "alias": user.alias,
        "campus_identity": user.campus_identity,
        "role": user.role,
        "status": user.status,
        "credit": user.credit,
        "xp": user.xp,
        "avatar_url": f"/uploads/{user.avatar_path}" if user.avatar_path else None,
        "dm_stranger_off": user.dm_stranger_off,
        "hide_online": user.hide_online,
        "verified_at": user.verified_at,
        "created_at": user.created_at,
    }


def consume_code(db: Session, email: str, purpose: str, code: str) -> VerificationCode:
    row = db.scalar(
        select(VerificationCode)
        .where(
            VerificationCode.email == email,
            VerificationCode.purpose == purpose,
            VerificationCode.consumed_at.is_(None),
        )
        .order_by(desc(VerificationCode.id))
        .with_for_update()
    )
    if (
        not row
        or row.expires_at < utcnow()
        or not secrets.compare_digest(row.code_hash, code_hash(email, purpose, code))
    ):
        raise APIError(400, "INVALID_CODE", "验证码错误或已过期")
    row.consumed_at = utcnow()
    return row


@router.post("/request-code", status_code=202)
def request_code(data: CodeRequest, request: Request, db: Session = Depends(get_db)) -> dict:
    email = require_campus_email(data.email, settings.campus_domains)
    existing = db.scalar(select(User).where(User.email == email))
    if data.purpose == "register" and existing:
        raise APIError(409, "EMAIL_EXISTS", "该邮箱已注册")
    if data.purpose == "reset_password" and not existing:
        # 不泄露账号是否存在，仍返回相同结果但不发信。
        return {"accepted": True, "resend_after": 60}
    ip = request.client.host if request.client else "unknown"
    check_rate_limit(db, "email_code_email", f"{data.purpose}:{email}", 5, 60)
    check_rate_limit(db, "email_code_ip", f"{data.purpose}:{ip}", 200, 60)
    code = f"{secrets.randbelow(1_000_000):06d}"
    db.add(
        VerificationCode(
            email=email,
            purpose=data.purpose,
            code_hash=code_hash(email, data.purpose, code),
            ip_address=ip,
            expires_at=utcnow() + timedelta(minutes=10),
        )
    )
    purpose_text = {"register": "注册", "reset_password": "重置密码", "change_email": "更换邮箱"}[data.purpose]
    enqueue_email(db, email, f"【梧桐墙】{purpose_text}验证码", f"你的验证码是 {code}，10 分钟内有效。请勿转发。")
    return {"accepted": True, "resend_after": 60}


@router.post("/register", status_code=201)
def register(data: RegisterRequest, request: Request, response: Response, db: Session = Depends(get_db)) -> dict:
    email = require_campus_email(data.email, settings.campus_domains)
    check_rate_limit(db, "register", request.client.host if request.client else "unknown", 100, 60)
    consume_code(db, email, "register", data.code)
    if db.scalar(select(User.id).where(User.email == email)):
        raise APIError(409, "EMAIL_EXISTS", "该邮箱已注册")
    user = User(
        email=email,
        password_hash=hash_password(data.password),
        nickname=data.nickname.strip(),
        alias=new_alias(),
        campus_identity="student",
    )
    db.add(user)
    try:
        db.flush()
    except IntegrityError as exc:
        raise APIError(409, "ACCOUNT_CONFLICT", "邮箱或昵称已被使用") from exc
    raw, session = create_session(db, user, request)
    set_session_cookies(response, raw, session)
    notify(db, user.id, "欢迎加入梧桐墙", "校园邮箱已验证。请先阅读社区规范，再开始参与讨论。", "/me")
    audit(db, user.id, "account.register", "user", user.id, after={"terms": data.agreed_terms_version})
    return {"user": user_payload(user), "csrf_token": session.csrf_token}


@router.post("/login")
def login(data: LoginRequest, request: Request, response: Response, db: Session = Depends(get_db)) -> dict:
    email = normalize_email(data.email)
    ip = request.client.host if request.client else "unknown"
    check_rate_limit(db, "login_ip", ip, 300, 15)
    check_rate_limit(db, "login_email", email, 10, 15)
    user = db.scalar(select(User).where(User.email == email))
    if not user or not verify_password(data.password, user.password_hash):
        raise APIError(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
    if user.status in {"disabled", "deleted"}:
        raise APIError(403, "ACCOUNT_DISABLED", "账号已停用")
    raw, session = create_session(db, user, request)
    set_session_cookies(response, raw, session)
    audit(db, user.id, "account.login", "session", session.id)
    return {"user": user_payload(user), "csrf_token": session.csrf_token}


@router.post("/reset-password")
def reset_password(data: PasswordResetRequest, db: Session = Depends(get_db)) -> dict:
    email = require_campus_email(data.email, settings.campus_domains)
    user = db.scalar(select(User).where(User.email == email))
    if not user:
        raise APIError(400, "INVALID_CODE", "验证码错误或已过期")
    consume_code(db, email, "reset_password", data.code)
    user.password_hash = hash_password(data.new_password)
    db.execute(update(SessionRecord).where(SessionRecord.user_id == user.id).values(revoked_at=utcnow()))
    notify(db, user.id, "密码已重置", "所有设备的登录已失效；如非本人操作，请联系管理员。")
    audit(db, user.id, "account.password_reset", "user", user.id)
    return {"ok": True}


@router.post("/logout")
def logout(
    response: Response,
    record: SessionRecord = Depends(get_session_record),
    db: Session = Depends(get_db),
) -> dict:
    record.revoked_at = utcnow()
    clear_session_cookies(response)
    return {"ok": True}


@router.post("/logout-all")
def logout_all(
    response: Response,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    db.execute(
        update(SessionRecord)
        .where(SessionRecord.user_id == user.id, SessionRecord.revoked_at.is_(None))
        .values(revoked_at=utcnow())
    )
    clear_session_cookies(response)
    audit(db, user.id, "account.logout_all", "user", user.id)
    return {"ok": True}
