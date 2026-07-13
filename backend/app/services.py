from __future__ import annotations

import json
import re
from datetime import timedelta
from typing import Any

from sqlalchemy import func, select
from sqlalchemy.dialects.postgresql import insert as postgresql_insert
from sqlalchemy.dialects.sqlite import insert as sqlite_insert
from sqlalchemy.orm import Session

from .errors import APIError
from .models import (
    AuditLog,
    ContentEntity,
    ContentRevision,
    EmailOutbox,
    ModerationCase,
    Notification,
    RateLimitEvent,
    Setting,
    User,
    utcnow,
)
from .security import thread_anonymous_name

ABUSE_WORDS = {"傻逼", "去死吧", "全家死", "人肉", "代考", "代写"}
HIGH_RISK_WORDS = {"电子烟", "处方药", "管制刀", "求扩散", "避雷", "学号", "身份证"}
CARE_WORDS = {"自杀", "轻生", "不想活", "自残", "活不下去", "想死"}


def normalize_email(email: str) -> str:
    return email.strip().lower()


def require_campus_email(email: str, domains: set[str]) -> str:
    normalized = normalize_email(email)
    if "@" not in normalized or normalized.rsplit("@", 1)[1] not in domains:
        raise APIError(400, "CAMPUS_EMAIL_REQUIRED", "请使用学校邮箱注册")
    return normalized


def check_rate_limit(db: Session, action: str, subject: str, limit: int, minutes: int) -> None:
    since = utcnow() - timedelta(minutes=minutes)
    count = (
        db.scalar(
            select(func.count(RateLimitEvent.id)).where(
                RateLimitEvent.action == action,
                RateLimitEvent.subject == subject,
                RateLimitEvent.created_at >= since,
            )
        )
        or 0
    )
    if count >= limit:
        raise APIError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
    db.add(RateLimitEvent(action=action, subject=subject))


def enqueue_email(db: Session, email: str, subject: str, body: str) -> None:
    db.add(EmailOutbox(to_email=email, subject=subject, body=body))


def notify(db: Session, user_id: int, title: str, body: str, link: str = "", kind: str = "system") -> None:
    db.add(Notification(user_id=user_id, type=kind, title=title, body=body, link=link))


def audit(
    db: Session,
    actor_id: int | None,
    action: str,
    target_type: str,
    target_id: int | str,
    reason: str = "",
    before: Any = None,
    after: Any = None,
    request_id: str = "",
) -> None:
    db.add(
        AuditLog(
            actor_id=actor_id,
            action=action,
            target_type=target_type,
            target_id=str(target_id),
            reason=reason,
            before_json=json.dumps(before, ensure_ascii=False, default=str) if before is not None else "",
            after_json=json.dumps(after, ensure_ascii=False, default=str) if after is not None else "",
            request_id=request_id,
        )
    )


def _configured_risk_words(db: Session | None) -> tuple[set[str], set[str]]:
    reject_words = set(ABUSE_WORDS)
    review_words = set(HIGH_RISK_WORDS)
    if not db:
        return reject_words, review_words
    row = db.get(Setting, "risk_words")
    if not row or not row.value.strip():
        return reject_words, review_words
    try:
        value = json.loads(row.value)
    except json.JSONDecodeError:
        value = [x.strip() for x in re.split(r"[,，\n]", row.value) if x.strip()]
    if isinstance(value, dict):
        reject_words.update(str(x).strip() for x in value.get("reject", []) if str(x).strip())
        review_words.update(str(x).strip() for x in value.get("review", []) if str(x).strip())
    elif isinstance(value, list):
        review_words.update(str(x).strip() for x in value if str(x).strip())
    return reject_words, review_words


def moderate_text(text: str, force_review: bool = False, db: Session | None = None) -> tuple[str, str, bool]:
    compact = text.lower()
    reject_words, review_words = _configured_risk_words(db)
    rejected = next((word for word in reject_words if word.lower() in compact), None)
    if rejected:
        return "rejected", f"命中禁止内容：{rejected}", any(x in compact for x in CARE_WORDS)
    risky = next((word for word in review_words if word.lower() in compact), None)
    if force_review or risky:
        return "pending", f"需要人工审核：{risky or '风险板块'}", any(x in compact for x in CARE_WORDS)
    return "published", "", any(x in compact for x in CARE_WORDS)


def create_entity(
    db: Session,
    owner_id: int,
    entity_type: str,
    text: str,
    *,
    allow_comments: bool = True,
    search_visible: bool = True,
    force_review: bool = False,
) -> tuple[ContentEntity, bool]:
    status, reason, care = moderate_text(text, force_review, db)
    if status == "rejected":
        raise APIError(400, "CONTENT_REJECTED", reason)
    entity = ContentEntity(
        owner_id=owner_id,
        type=entity_type,
        status=status,
        allow_comments=allow_comments,
        search_visible=search_visible,
        moderation_reason=reason,
    )
    db.add(entity)
    db.flush()
    if status == "pending":
        db.add(ModerationCase(entity_id=entity.id, source="risk_rule"))
    return entity, care


def record_revision(db: Session, entity: ContentEntity, editor_id: int, title: str, body: str) -> None:
    db.add(
        ContentRevision(
            entity_id=entity.id,
            editor_id=editor_id,
            revision=entity.revision,
            title=title,
            body=body,
        )
    )
    entity.revision += 1


def remoderate_entity(db: Session, entity: ContentEntity, text: str, source: str = "edit_risk") -> None:
    status, reason, _ = moderate_text(text, db=db)
    if status == "rejected":
        raise APIError(400, "CONTENT_REJECTED", reason)
    if status != "pending":
        return
    entity.status = "pending"
    entity.moderation_reason = reason
    case = db.scalar(select(ModerationCase).where(ModerationCase.entity_id == entity.id).with_for_update())
    if case:
        case.status = "pending"
        case.source = source
        case.assignee_id = None
        case.decision = ""
        case.decided_at = None
    else:
        db.add(ModerationCase(entity_id=entity.id, source=source))


def insert_unique(db: Session, table, values: dict[str, Any], index_elements: list[str]) -> bool:
    """使用数据库冲突处理实现并发安全的幂等写入。"""
    dialect = db.get_bind().dialect.name
    if dialect == "postgresql":
        statement = postgresql_insert(table).values(**values).on_conflict_do_nothing(index_elements=index_elements)
    elif dialect == "sqlite":
        statement = sqlite_insert(table).values(**values).on_conflict_do_nothing(index_elements=index_elements)
    else:  # 生产只允许 PostgreSQL，本地与测试使用 SQLite。
        raise RuntimeError(f"不支持的数据库方言：{dialect}")
    result = db.execute(statement)
    return bool(result.rowcount)


def author_name(db: Session, entity: ContentEntity, identity_mode: str, thread_id: int | None = None) -> str:
    user = db.get(User, entity.owner_id)
    if not user or user.status == "deleted":
        return "已注销用户"
    if identity_mode == "alias":
        return user.alias
    if identity_mode == "anonymous":
        return thread_anonymous_name(thread_id or entity.id, user.id)
    return user.nickname


def public_entity_or_404(db: Session, entity_id: int) -> ContentEntity:
    entity = db.get(ContentEntity, entity_id)
    if not entity or entity.status != "published":
        raise APIError(404, "CONTENT_NOT_FOUND", "内容不存在")
    return entity


def mask_observe_body(body: str) -> str:
    body = re.sub(r"\b\d{6,18}\b", "▓▓▓▓▓▓", body)
    body = re.sub(r"1[3-9]\d{9}", "1**********", body)
    return body
