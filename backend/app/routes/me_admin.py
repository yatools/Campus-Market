from __future__ import annotations

import secrets
from pathlib import Path

from fastapi import APIRouter, Depends, Query, Request, Response
from fastapi.responses import FileResponse
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select, update
from sqlalchemy.orm import Session

from ..anonymous import DEFAULT_ANONYMOUS_NICKNAMES, normalize_nickname_pool
from ..config import get_settings
from ..credit import MAX_CREDIT, apply_credit_rule
from ..database import get_db
from ..deps import admin_user, current_user, moderator_user
from ..errors import APIError
from ..models import (
    Announcement,
    Answer,
    Appeal,
    Attachment,
    AuditLog,
    BackupJob,
    Comment,
    ContentEntity,
    CourseReview,
    EmailOutbox,
    Favorite,
    Feedback,
    HandbookArticle,
    Listing,
    LostItem,
    Message,
    ModerationCase,
    Notification,
    ObservePost,
    Penalty,
    Post,
    Question,
    Report,
    SessionRecord,
    Setting,
    Team,
    User,
    utcnow,
)
from ..security import clear_session_cookies, hash_password, verify_password
from ..services import audit, enqueue_email, moderate_text, notify, require_campus_email
from .auth import consume_code, user_payload

settings = get_settings()
me_router = APIRouter(prefix="/me", tags=["用户后台"])
admin_router = APIRouter(prefix="/admin", tags=["管理后台"])


class ProfileUpdate(BaseModel):
    nickname: str | None = Field(default=None, min_length=2, max_length=20)
    alias: str | None = Field(default=None, min_length=2, max_length=20)
    avatar_attachment_id: int | None = None


class PrivacyUpdate(BaseModel):
    dm_stranger_off: bool | None = None
    hide_online: bool | None = None


class PasswordChange(BaseModel):
    old_password: str = Field(min_length=1, max_length=128)
    new_password: str = Field(min_length=10, max_length=128)


class EmailChange(BaseModel):
    new_email: str
    code: str = Field(pattern=r"^\d{6}$")


class DeactivateRequest(BaseModel):
    password: str
    confirmation: str


def content_title(db: Session, entity: ContentEntity) -> str:
    if row := db.get(Post, entity.id):
        return row.title or row.body[:50]
    if row := db.get(Question, entity.id):
        return row.title
    if row := db.get(HandbookArticle, entity.id):
        return row.title
    if row := db.get(Listing, entity.id):
        return row.title
    if row := db.get(Team, entity.id):
        return f"{row.game} · {row.mode}"
    if row := db.get(CourseReview, entity.id):
        return row.body[:50]
    if row := db.get(Feedback, entity.id):
        return row.title
    if row := db.get(LostItem, entity.id):
        return row.item_name
    if db.get(Comment, entity.id):
        return "回帖"
    if db.get(Answer, entity.id):
        return "回答"
    if db.get(Message, entity.id):
        return "被举报的私信"
    return entity.type


def content_preview(db: Session, entity: ContentEntity) -> str:
    if row := db.get(Post, entity.id):
        return row.body[:2000]
    if row := db.get(Question, entity.id):
        return row.body[:2000]
    if row := db.get(HandbookArticle, entity.id):
        return row.body[:2000]
    if row := db.get(Listing, entity.id):
        return row.description[:2000]
    if row := db.get(ObservePost, entity.id):
        return row.body_raw[:2000]
    if row := db.get(CourseReview, entity.id):
        return row.body[:2000]
    if row := db.get(Feedback, entity.id):
        return row.body[:2000]
    if row := db.get(Comment, entity.id):
        return row.body[:2000]
    if row := db.get(Answer, entity.id):
        return row.body[:2000]
    if row := db.get(Message, entity.id):
        return row.body[:2000]
    if row := db.get(LostItem, entity.id):
        return row.description[:2000]
    return ""


@me_router.get("")
def me(user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    data = user_payload(user)
    data["unread_notifications"] = (
        db.scalar(
            select(func.count(Notification.id)).where(Notification.user_id == user.id, Notification.read_at.is_(None))
        )
        or 0
    )
    data["active_sessions"] = (
        db.scalar(
            select(func.count(SessionRecord.id)).where(
                SessionRecord.user_id == user.id,
                SessionRecord.revoked_at.is_(None),
                SessionRecord.expires_at > utcnow(),
            )
        )
        or 0
    )
    return data


@me_router.patch("/profile")
def update_profile(data: ProfileUpdate, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    if data.nickname is not None:
        user.nickname = data.nickname.strip()
    if data.alias is not None:
        alias = data.alias.strip()
        if len(alias) < 2 or len(alias) > 20 or any(ord(char) < 32 for char in alias):
            raise APIError(400, "ALIAS_INVALID", "固定匿名昵称需为 2–20 个字符，且不能包含换行或控制字符")
        moderation_status, reason, _ = moderate_text(alias, db=db)
        if moderation_status != "published":
            raise APIError(400, "ALIAS_REJECTED", reason or "固定匿名昵称包含不适合公开展示的内容")
        if db.scalar(select(User.id).where(User.alias == alias, User.id != user.id)):
            raise APIError(409, "ALIAS_EXISTS", "这个固定匿名昵称已被使用")
        user.alias = alias
    if data.avatar_attachment_id is not None:
        attachment = db.get(Attachment, data.avatar_attachment_id)
        if not attachment or attachment.owner_id != user.id or attachment.status != "pending":
            raise APIError(400, "INVALID_AVATAR", "头像附件无效")
        attachment.status = "avatar"
        user.avatar_path = attachment.path
    audit(db, user.id, "account.profile_update", "user", user.id)
    return user_payload(user)


@me_router.patch("/privacy")
def update_privacy(data: PrivacyUpdate, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    for key, value in data.model_dump(exclude_none=True).items():
        setattr(user, key, value)
    audit(db, user.id, "account.privacy_update", "user", user.id, after=data.model_dump(exclude_none=True))
    return {"dm_stranger_off": user.dm_stranger_off, "hide_online": user.hide_online}


@me_router.post("/password")
def change_password(data: PasswordChange, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    if not verify_password(data.old_password, user.password_hash):
        raise APIError(400, "OLD_PASSWORD_INVALID", "原密码错误")
    user.password_hash = hash_password(data.new_password)
    db.execute(update(SessionRecord).where(SessionRecord.user_id == user.id).values(revoked_at=utcnow()))
    audit(db, user.id, "account.password_change", "user", user.id)
    return {"ok": True, "login_required": True}


@me_router.post("/email")
def change_email(data: EmailChange, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    email = require_campus_email(data.new_email, settings.campus_domains)
    if db.scalar(select(User.id).where(User.email == email, User.id != user.id)):
        raise APIError(409, "EMAIL_EXISTS", "该邮箱已被使用")
    consume_code(db, email, "change_email", data.code)
    before = user.email
    user.email = email
    user.verified_at = utcnow()
    audit(db, user.id, "account.email_change", "user", user.id, before=before, after=email)
    return {"ok": True, "email": email}


@me_router.get("/sessions")
def sessions(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    total = db.scalar(select(func.count(SessionRecord.id)).where(SessionRecord.user_id == user.id)) or 0
    rows = db.scalars(
        select(SessionRecord)
        .where(SessionRecord.user_id == user.id)
        .order_by(SessionRecord.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": x.id,
                "ip_address": x.ip_address,
                "user_agent": x.user_agent,
                "last_seen_at": x.last_seen_at,
                "expires_at": x.expires_at,
                "revoked": bool(x.revoked_at),
            }
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@me_router.delete("/sessions/{session_id}")
def revoke_session(session_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    row = db.get(SessionRecord, session_id)
    if not row or row.user_id != user.id:
        raise APIError(404, "SESSION_NOT_FOUND", "会话不存在")
    row.revoked_at = row.revoked_at or utcnow()
    return {"ok": True}


@me_router.get("/content")
def my_content(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    type: str = Query("", max_length=30),
    status: str = Query("", max_length=20),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.owner_id == user.id]
    if type:
        filters.append(ContentEntity.type == type)
    if status:
        filters.append(ContentEntity.status == status)
    total = db.scalar(select(func.count(ContentEntity.id)).where(*filters)) or 0
    rows = db.scalars(
        select(ContentEntity)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": x.id,
                "type": x.type,
                "title": content_title(db, x),
                "status": x.status,
                "created_at": x.created_at,
                "updated_at": x.updated_at,
            }
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@me_router.get("/favorites")
def my_favorites(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    type: str = Query("", max_length=30),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [Favorite.user_id == user.id, ContentEntity.status == "published"]
    if type:
        filters.append(ContentEntity.type == type)
    total = (
        db.scalar(
            select(func.count(Favorite.id))
            .join(ContentEntity, ContentEntity.id == Favorite.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(Favorite, ContentEntity)
        .join(ContentEntity, ContentEntity.id == Favorite.entity_id)
        .where(*filters)
        .order_by(Favorite.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    items = [{"id": e.id, "type": e.type, "title": content_title(db, e), "favorited_at": f.created_at} for f, e in rows]
    return {"items": items, "page": page, "page_size": page_size, "total": total}


@me_router.get("/reports")
def my_reports(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    status: str = Query("", max_length=20),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [Report.reporter_id == user.id]
    if status:
        filters.append(Report.status == status)
    total = db.scalar(select(func.count(Report.id)).where(*filters)) or 0
    rows = db.scalars(
        select(Report)
        .where(*filters)
        .order_by(Report.id.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {"id": x.id, "entity_id": x.entity_id, "reason": x.reason, "status": x.status, "created_at": x.created_at}
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@me_router.get("/appeals")
def my_appeals(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    status: str = Query("", max_length=20),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [Appeal.user_id == user.id]
    if status:
        filters.append(Appeal.status == status)
    total = db.scalar(select(func.count(Appeal.id)).where(*filters)) or 0
    rows = db.scalars(
        select(Appeal)
        .where(*filters)
        .order_by(Appeal.id.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": x.id,
                "penalty_id": x.penalty_id,
                "status": x.status,
                "admin_note": x.admin_note,
                "created_at": x.created_at,
            }
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@me_router.post("/deactivate")
def deactivate_account(
    data: DeactivateRequest,
    response: Response,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    if data.confirmation != "注销我的账号":
        raise APIError(400, "CONFIRMATION_REQUIRED", "请输入“注销我的账号”确认")
    if not verify_password(data.password, user.password_hash):
        raise APIError(400, "PASSWORD_INVALID", "密码错误")
    user.status = "disabled"
    user.deactivated_at = utcnow()
    db.execute(update(SessionRecord).where(SessionRecord.user_id == user.id).values(revoked_at=utcnow()))
    clear_session_cookies(response)
    audit(db, user.id, "account.deactivate", "user", user.id)
    return {"ok": True, "anonymize_after_days": 30}


# ---------------------------------------------------------------- 管理后台
class UserAdminUpdate(BaseModel):
    role: str | None = None
    campus_identity: str | None = None
    status: str | None = None
    credit: int | None = Field(default=None, ge=0, le=MAX_CREDIT)
    reason: str = Field(min_length=2, max_length=1000)

    @field_validator("role")
    @classmethod
    def role_valid(cls, value: str | None) -> str | None:
        if value is not None and value not in {"user", "moderator", "admin"}:
            raise ValueError("角色无效")
        return value

    @field_validator("campus_identity")
    @classmethod
    def identity_valid(cls, value: str | None) -> str | None:
        if value is not None and value not in {"student", "alumni", "staff"}:
            raise ValueError("校园身份无效")
        return value

    @field_validator("status")
    @classmethod
    def status_valid(cls, value: str | None) -> str | None:
        if value is not None and value not in {"active", "restricted", "disabled"}:
            raise ValueError("账号状态无效")
        return value


class ModerationDecision(BaseModel):
    decision: str
    note: str = Field(default="", max_length=5000)
    respondent_id: int | None = None

    @field_validator("decision")
    @classmethod
    def decision_valid(cls, value: str) -> str:
        if value not in {"approve", "reject", "hide"}:
            raise ValueError("审核决定无效")
        return value


class PenaltyCreate(BaseModel):
    user_id: int
    violation_type: str = Field(min_length=2, max_length=120)
    result: str = Field(min_length=2, max_length=3000)
    rule: str = Field(min_length=2, max_length=160)
    credit_delta: int = Field(default=0, ge=-MAX_CREDIT, le=0)


class AppealDecision(BaseModel):
    status: str
    note: str = Field(default="", max_length=3000)

    @field_validator("status")
    @classmethod
    def status_valid(cls, value: str) -> str:
        if value not in {"approved", "rejected"}:
            raise ValueError("申诉决定无效")
        return value


class AnnouncementCreate(BaseModel):
    title: str = Field(min_length=2, max_length=160)
    body: str = Field(min_length=2, max_length=10000)
    level: str = "normal"
    audience: str = "all"

    @field_validator("level")
    @classmethod
    def level_valid(cls, value: str) -> str:
        if value not in {"normal", "strong"}:
            raise ValueError("公告级别无效")
        return value

    @field_validator("audience")
    @classmethod
    def audience_valid(cls, value: str) -> str:
        if value not in {"all", "student", "alumni", "staff"}:
            raise ValueError("公告目标人群无效")
        return value


class SettingUpdate(BaseModel):
    value: str = Field(max_length=20000)


class FeedbackDecision(BaseModel):
    status: str
    admin_note: str = Field(default="", max_length=3000)
    reward_xp: int = Field(default=0, ge=0, le=500)

    @field_validator("status")
    @classmethod
    def status_valid(cls, value: str) -> str:
        if value not in {"accepted", "rejected"}:
            raise ValueError("反馈处理状态无效")
        return value


@admin_router.get("/overview")
def admin_overview(moderator: User = Depends(moderator_user), db: Session = Depends(get_db)) -> dict:
    del moderator
    return {
        "users": db.scalar(select(func.count(User.id))) or 0,
        "published_content": db.scalar(select(func.count(ContentEntity.id)).where(ContentEntity.status == "published"))
        or 0,
        "pending_moderation": db.scalar(select(func.count(ModerationCase.id)).where(ModerationCase.status == "pending"))
        or 0,
        "pending_reports": db.scalar(select(func.count(Report.id)).where(Report.status == "pending")) or 0,
        "pending_appeals": db.scalar(select(func.count(Appeal.id)).where(Appeal.status == "pending")) or 0,
        "unread_feedback": db.scalar(select(func.count(Feedback.entity_id)).where(Feedback.status == "pending")) or 0,
        "pending_email": db.scalar(select(func.count(EmailOutbox.id)).where(EmailOutbox.status == "pending")) or 0,
        "failed_email": db.scalar(select(func.count(EmailOutbox.id)).where(EmailOutbox.status == "failed")) or 0,
    }


@admin_router.get("/users")
def admin_users(
    q: str = Query("", max_length=80),
    page: int = Query(1, ge=1),
    page_size: int = Query(30, ge=1, le=100),
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    del moderator
    filters = []
    if q:
        filters.append((User.nickname.ilike(f"%{q}%")) | (User.email.ilike(f"%{q}%")))
    total = db.scalar(select(func.count(User.id)).where(*filters)) or 0
    rows = db.scalars(
        select(User).where(*filters).order_by(User.created_at.desc()).offset((page - 1) * page_size).limit(page_size)
    ).all()
    return {
        "items": [user_payload(x) for x in rows],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@admin_router.patch("/users/{user_id}")
def admin_update_user(
    user_id: int,
    data: UserAdminUpdate,
    request: Request,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    target = db.scalar(select(User).where(User.id == user_id).with_for_update())
    if not target:
        raise APIError(404, "USER_NOT_FOUND", "用户不存在")
    if target.id == admin.id and data.status in {"disabled", "restricted"}:
        raise APIError(400, "SELF_LOCKOUT", "不能限制自己的管理员账号")
    before = user_payload(target)
    for key, value in data.model_dump(exclude_none=True, exclude={"reason"}).items():
        setattr(target, key, value)
    if target.status == "disabled" or target.role != before["role"]:
        db.execute(
            update(SessionRecord)
            .where(SessionRecord.user_id == target.id, SessionRecord.revoked_at.is_(None))
            .values(revoked_at=utcnow())
        )
    audit(
        db,
        admin.id,
        "admin.user_update",
        "user",
        target.id,
        data.reason,
        before=before,
        after=user_payload(target),
        request_id=getattr(request.state, "request_id", ""),
    )
    notify(db, target.id, "账号状态已更新", data.reason, "/me")
    return user_payload(target)


@admin_router.get("/moderation-cases")
def moderation_cases(
    status: str = Query("pending", max_length=20),
    source: str = Query("", max_length=30),
    entity_type: str = Query("", max_length=30),
    page: int = Query(1, ge=1),
    page_size: int = Query(30, ge=1, le=100),
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    del moderator
    filters = [ModerationCase.status == status]
    if source:
        filters.append(ModerationCase.source == source)
    if entity_type:
        filters.append(ContentEntity.type == entity_type)
    total = (
        db.scalar(
            select(func.count(ModerationCase.id))
            .join(ContentEntity, ContentEntity.id == ModerationCase.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ModerationCase, ContentEntity)
        .join(ContentEntity, ContentEntity.id == ModerationCase.entity_id)
        .where(*filters)
        .order_by(ModerationCase.created_at.asc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": case.id,
                "entity_id": entity.id,
                "entity_type": entity.type,
                "title": content_title(db, entity),
                "preview": content_preview(db, entity),
                "source": case.source,
                "status": case.status,
                "notes": case.notes,
                "reports": [
                    {
                        "id": report.id,
                        "reporter_id": report.reporter_id,
                        "reason": report.reason,
                        "detail": report.detail,
                        "created_at": report.created_at,
                    }
                    for report in db.scalars(
                        select(Report).where(Report.entity_id == entity.id, Report.status == "pending")
                    ).all()
                ],
                "created_at": case.created_at,
            }
            for case, entity in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@admin_router.get("/reports")
def report_queue(
    status: str = Query("pending", max_length=20),
    reason: str = Query("", max_length=80),
    page: int = Query(1, ge=1),
    page_size: int = Query(30, ge=1, le=100),
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    del moderator
    filters = [Report.status == status]
    if reason:
        filters.append(Report.reason == reason)
    total = db.scalar(select(func.count(Report.id)).where(*filters)) or 0
    rows = db.execute(
        select(Report, ContentEntity)
        .join(ContentEntity, ContentEntity.id == Report.entity_id)
        .where(*filters)
        .order_by(Report.created_at.asc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": report.id,
                "entity_id": entity.id,
                "entity_type": entity.type,
                "title": content_title(db, entity),
                "preview": content_preview(db, entity),
                "reporter_id": report.reporter_id,
                "reason": report.reason,
                "detail": report.detail,
                "status": report.status,
                "created_at": report.created_at,
            }
            for report, entity in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@admin_router.post("/moderation-cases/{case_id}/decision")
def decide_moderation(
    case_id: int,
    data: ModerationDecision,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    case = db.scalar(select(ModerationCase).where(ModerationCase.id == case_id).with_for_update())
    if not case:
        raise APIError(404, "CASE_NOT_FOUND", "审核案件不存在")
    if case.status != "pending":
        return {"id": case.id, "status": case.status, "decision": case.decision}
    entity = db.get(ContentEntity, case.entity_id)
    before = {"case_status": case.status, "entity_status": entity.status}
    case.status = "resolved"
    case.assignee_id = moderator.id
    case.decision = data.decision
    case.notes = data.note
    case.decided_at = utcnow()
    entity.status = "published" if data.decision == "approve" else "hidden"
    if data.decision != "approve" and (question := db.get(Question, entity.id)):
        if not question.bounty_settled and not question.accepted_answer_id:
            owner = db.get(User, entity.owner_id)
            owner.xp += question.bounty_xp
            question.bounty_settled = True
    if observe := db.get(ObservePost, entity.id):
        if data.respondent_id is not None:
            respondent = db.get(User, data.respondent_id)
            if not respondent or respondent.status != "active":
                raise APIError(404, "RESPONDENT_NOT_FOUND", "指定回应方不存在")
            observe.respondent_id = data.respondent_id
            notify(db, data.respondent_id, "你被指定为观察帖回应方", observe.title, f"/observe/{entity.id}")
        observe.admin_note = data.note
    db.query(Report).filter(Report.entity_id == entity.id, Report.status == "pending").update(
        {Report.status: "resolved"}, synchronize_session=False
    )
    audit(
        db,
        moderator.id,
        "moderation.decide",
        entity.type,
        entity.id,
        data.note,
        before=before,
        after={"case_status": case.status, "entity_status": entity.status, "decision": data.decision},
    )
    notify(db, entity.owner_id, "内容审核结果", f"审核结果：{data.decision}。{data.note}", f"/content/{entity.id}")
    return {"id": case.id, "status": case.status, "decision": case.decision}


@admin_router.post("/penalties", status_code=201)
def create_penalty(
    data: PenaltyCreate,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    target = db.get(User, data.user_id)
    if not target:
        raise APIError(404, "USER_NOT_FOUND", "用户不存在")
    before_credit = target.credit
    target.credit = max(0, min(MAX_CREDIT, target.credit + data.credit_delta))
    row = Penalty(
        user_id=target.id,
        public_mask=f"用户 {target.alias[-4:]}",
        violation_type=data.violation_type,
        result=data.result,
        rule=data.rule,
    )
    db.add(row)
    db.flush()
    audit(
        db,
        moderator.id,
        "penalty.create",
        "penalty",
        row.id,
        data.rule,
        before={"user_id": target.id, "credit": before_credit},
        after={"user_id": target.id, "credit": target.credit, "result": data.result},
    )
    notify(db, target.id, "收到治理处理", data.result, "/governance")
    return {"id": row.id, "credit": target.credit}


@admin_router.get("/appeals")
def list_appeals(
    status: str = Query("", max_length=20),
    page: int = Query(1, ge=1),
    page_size: int = Query(30, ge=1, le=100),
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    del moderator
    filters = [Appeal.status == status] if status else []
    total = db.scalar(select(func.count(Appeal.id)).where(*filters)) or 0
    rows = db.scalars(
        select(Appeal)
        .where(*filters)
        .order_by(Appeal.created_at.asc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": x.id,
                "penalty_id": x.penalty_id,
                "user_id": x.user_id,
                "reason": x.reason,
                "status": x.status,
                "admin_note": x.admin_note,
            }
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@admin_router.post("/appeals/{appeal_id}/decision")
def decide_appeal(
    appeal_id: int,
    data: AppealDecision,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.scalar(select(Appeal).where(Appeal.id == appeal_id).with_for_update())
    if not row:
        raise APIError(404, "APPEAL_NOT_FOUND", "申诉不存在")
    if row.status != "pending":
        return {"id": row.id, "status": row.status}
    before_status = row.status
    row.status = data.status
    row.admin_note = data.note
    audit(
        db,
        moderator.id,
        "appeal.decide",
        "appeal",
        row.id,
        data.note,
        before={"status": before_status},
        after={"status": data.status},
    )
    notify(db, row.user_id, "申诉处理结果", f"{data.status}：{data.note}", "/me")
    return {"id": row.id, "status": row.status}


@admin_router.post("/announcements", status_code=201)
def create_announcement(
    data: AnnouncementCreate,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    row = Announcement(**data.model_dump())
    db.add(row)
    db.flush()
    if data.level == "strong":
        filters = [User.status == "active", User.email.is_not(None)]
        if data.audience != "all":
            filters.append(User.campus_identity == data.audience)
        recipients = db.scalars(select(User).where(*filters)).all()
        for recipient in recipients:
            notify(db, recipient.id, data.title, data.body, "/explore/announcements", "announcement")
            enqueue_email(db, recipient.email, f"【梧桐墙公告】{data.title}", data.body)
    audit(db, moderator.id, "announcement.create", "announcement", row.id)
    return {"id": row.id, **data.model_dump(), "published_at": row.published_at}


@admin_router.get("/settings")
def settings_list(admin: User = Depends(admin_user), db: Session = Depends(get_db)) -> dict:
    del admin
    rows = db.scalars(select(Setting).order_by(Setting.key)).all()
    values = {x.key: x.value for x in rows}
    values.setdefault("anonymous_nickname_pool", "\n".join(DEFAULT_ANONYMOUS_NICKNAMES))
    return values


@admin_router.put("/settings/{key}")
def update_setting(
    key: str,
    data: SettingUpdate,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    allowed = {
        "handbook_categories",
        "risk_words",
        "site_notice",
        "registration_open",
        "anonymous_nickname_pool",
    }
    if key not in allowed:
        raise APIError(400, "SETTING_NOT_ALLOWED", "不支持该设置项")
    value = data.value
    if key == "anonymous_nickname_pool":
        try:
            value = "\n".join(normalize_nickname_pool(value))
        except ValueError as exc:
            raise APIError(400, "ANONYMOUS_POOL_INVALID", str(exc)) from exc
    row = db.get(Setting, key)
    before = row.value if row else ""
    if row:
        row.value = value
        row.updated_by = admin.id
    else:
        row = Setting(key=key, value=value, updated_by=admin.id)
        db.add(row)
    audit(db, admin.id, "setting.update", "setting", key, before=before, after=value)
    return {"key": key, "value": value}


@admin_router.get("/feedback")
def feedback_queue(
    status: str = Query("", max_length=20),
    page: int = Query(1, ge=1),
    page_size: int = Query(30, ge=1, le=100),
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    del moderator
    filters = [Feedback.status == status] if status else []
    total = db.scalar(select(func.count(Feedback.entity_id)).where(*filters)) or 0
    rows = db.execute(
        select(ContentEntity, Feedback)
        .join(Feedback, Feedback.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": e.id,
                "user_id": e.owner_id,
                "type": f.type,
                "title": f.title,
                "body": f.body,
                "status": f.status,
                "admin_note": f.admin_note,
            }
            for e, f in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@admin_router.post("/feedback/{feedback_id}/decision")
def decide_feedback(
    feedback_id: int,
    data: FeedbackDecision,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.scalar(select(Feedback).where(Feedback.entity_id == feedback_id).with_for_update())
    entity = db.get(ContentEntity, feedback_id)
    if not row or not entity:
        raise APIError(404, "FEEDBACK_NOT_FOUND", "反馈不存在")
    if row.status in {"accepted", "rejected"}:
        return {"ok": True, "status": row.status}
    before = {"status": row.status, "admin_note": row.admin_note}
    row.status = data.status
    row.admin_note = data.admin_note
    owner = db.get(User, entity.owner_id)
    if data.status == "accepted":
        owner.xp += data.reward_xp
        apply_credit_rule(
            db,
            owner,
            "reward.feedback_accepted",
            actor_id=moderator.id,
            target_type="feedback",
            target_id=feedback_id,
        )
    notify(db, owner.id, "反馈处理结果", f"{data.status}：{data.admin_note}", "/me")
    audit(
        db,
        moderator.id,
        "feedback.decide",
        "feedback",
        feedback_id,
        data.admin_note,
        before=before,
        after={"status": row.status, "admin_note": row.admin_note},
    )
    return {"ok": True}


@admin_router.post("/backups", status_code=202)
def request_backup(admin: User = Depends(admin_user), db: Session = Depends(get_db)) -> dict:
    row = BackupJob(requested_by=admin.id, download_token=secrets.token_urlsafe(32))
    db.add(row)
    db.flush()
    audit(db, admin.id, "backup.request", "backup", row.id)
    return {"id": row.id, "status": row.status}


@admin_router.get("/backups")
def list_backups(admin: User = Depends(admin_user), db: Session = Depends(get_db)) -> dict:
    del admin
    rows = db.scalars(select(BackupJob).order_by(BackupJob.created_at.desc()).limit(20)).all()
    return {
        "items": [
            {
                "id": x.id,
                "status": x.status,
                "created_at": x.created_at,
                "finished_at": x.finished_at,
                "download_url": f"/api/v1/admin/backups/{x.id}/download?token={x.download_token}"
                if x.status == "ready"
                else None,
                "error": x.error,
            }
            for x in rows
        ],
        "page": 1,
        "page_size": 20,
        "total": len(rows),
    }


@admin_router.get("/backups/{job_id}/download")
def download_backup(
    job_id: int,
    token: str,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
):
    del admin
    row = db.get(BackupJob, job_id)
    if not row or row.status != "ready" or not secrets.compare_digest(row.download_token, token):
        raise APIError(404, "BACKUP_NOT_FOUND", "备份不存在或尚未完成")
    path = Path(row.file_path)
    if not path.is_file() or settings.backup_dir.resolve() not in path.resolve().parents:
        raise APIError(404, "BACKUP_FILE_MISSING", "备份文件不存在")
    return FileResponse(path, filename=path.name, media_type="application/zip")


@admin_router.get("/audit-logs")
def audit_logs(
    page: int = Query(1, ge=1),
    page_size: int = Query(50, ge=1, le=100),
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    del admin
    total = db.scalar(select(func.count(AuditLog.id))) or 0
    rows = db.scalars(
        select(AuditLog).order_by(AuditLog.created_at.desc()).offset((page - 1) * page_size).limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": x.id,
                "actor_id": x.actor_id,
                "action": x.action,
                "target_type": x.target_type,
                "target_id": x.target_id,
                "reason": x.reason,
                "created_at": x.created_at,
            }
            for x in rows
        ],
        "page": page,
        "page_size": page_size,
        "total": total,
    }
