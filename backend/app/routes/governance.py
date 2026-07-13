from __future__ import annotations

from datetime import timedelta

from fastapi import APIRouter, Depends, Query
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, or_, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import current_user, optional_user, participating_user
from ..errors import APIError
from ..models import (
    Activity,
    ActivityMember,
    Announcement,
    AnnouncementRead,
    Appeal,
    Block,
    ContentEntity,
    Conversation,
    ConversationMember,
    Feedback,
    Listing,
    Message,
    Notification,
    ObservePost,
    Penalty,
    TeamMembership,
    User,
    utcnow,
)
from ..services import audit, check_rate_limit, create_entity, insert_unique, mask_observe_body, notify

router = APIRouter(tags=["治理与通信"])


def page(items: list, page_no: int, page_size: int, total: int) -> dict:
    return {"items": items, "page": page_no, "page_size": page_size, "total": total}


# ---------------------------------------------------------------- 观察台 / 治理
class ObserveCreate(BaseModel):
    title: str = Field(min_length=4, max_length=160)
    body: str = Field(min_length=10, max_length=10000)


class ObserveResponse(BaseModel):
    body: str = Field(min_length=2, max_length=5000)


class AppealCreate(BaseModel):
    reason: str = Field(min_length=10, max_length=5000)


def observe_payload(db: Session, entity: ContentEntity, row: ObservePost, viewer: User | None) -> dict:
    privileged = viewer and viewer.role in {"moderator", "admin"}
    return {
        "id": entity.id,
        "title": row.title,
        "body": row.body_raw if privileged else row.body_masked,
        "status": entity.status,
        "response": row.response,
        "admin_note": row.admin_note,
        "mine": bool(viewer and viewer.id == entity.owner_id),
        "respondent": bool(viewer and viewer.id == row.respondent_id),
        "created_at": entity.created_at,
    }


@router.get("/observe-posts")
def list_observe(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=50),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    visible_filter = ContentEntity.status == "published"
    if viewer:
        if viewer.role in {"moderator", "admin"}:
            visible_filter = ContentEntity.id.is_not(None)
        else:
            visible_filter = or_(
                ContentEntity.status == "published",
                ContentEntity.owner_id == viewer.id,
                ObservePost.respondent_id == viewer.id,
            )
    total = (
        db.scalar(
            select(func.count(ObservePost.entity_id))
            .join(ContentEntity, ContentEntity.id == ObservePost.entity_id)
            .where(visible_filter)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, ObservePost)
        .join(ObservePost, ObservePost.entity_id == ContentEntity.id)
        .where(visible_filter)
        .order_by(ContentEntity.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page(
        [observe_payload(db, e, o, viewer) for e, o in rows],
        page_no,
        page_size,
        total,
    )


@router.post("/observe-posts", status_code=201)
def create_observe(data: ObserveCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    if user.credit < 75:
        raise APIError(403, "CREDIT_REQUIRED", "发布观察帖需要信用分不低于 75")
    entity, _ = create_entity(
        db,
        user.id,
        "observe",
        f"{data.title}\n{data.body}",
        force_review=True,
        search_visible=False,
    )
    row = ObservePost(
        entity_id=entity.id,
        title=data.title.strip(),
        body_masked=mask_observe_body(data.body.strip()),
        body_raw=data.body.strip(),
    )
    db.add(row)
    audit(db, user.id, "observe.create", "observe", entity.id)
    return observe_payload(db, entity, row, user)


@router.post("/observe-posts/{observe_id}/response")
def respond_observe(
    observe_id: int,
    data: ObserveResponse,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.get(ObservePost, observe_id)
    entity = db.get(ContentEntity, observe_id)
    if not row or not entity:
        raise APIError(404, "OBSERVE_NOT_FOUND", "观察帖不存在")
    if row.respondent_id != user.id:
        raise APIError(403, "RESPONDENT_REQUIRED", "只有审核员指定的回应方可以回应")
    row.response = data.body.strip()
    row.response_at = utcnow()
    audit(db, user.id, "observe.respond", "observe", observe_id)
    notify(db, entity.owner_id, "观察帖收到回应", row.title, f"/observe/{observe_id}")
    return {"ok": True, "response": row.response}


@router.get("/penalties")
def penalties(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=100),
    db: Session = Depends(get_db),
) -> dict:
    total = db.scalar(select(func.count(Penalty.id))) or 0
    rows = db.scalars(
        select(Penalty)
        .order_by(Penalty.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page(
        [
            {
                "id": x.id,
                "user": x.public_mask,
                "violation_type": x.violation_type,
                "result": x.result,
                "rule": x.rule,
                "created_at": x.created_at,
            }
            for x in rows
        ],
        page_no,
        page_size,
        total,
    )


@router.post("/penalties/{penalty_id}/appeals", status_code=201)
def appeal_penalty(
    penalty_id: int,
    data: AppealCreate,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    penalty = db.get(Penalty, penalty_id)
    if not penalty:
        raise APIError(404, "PENALTY_NOT_FOUND", "处罚记录不存在")
    if penalty.user_id != user.id:
        raise APIError(403, "PENALTY_OWNER_REQUIRED", "只能申诉自己的处罚记录")
    existing = db.scalar(select(Appeal).where(Appeal.penalty_id == penalty_id, Appeal.user_id == user.id))
    if existing:
        return {"id": existing.id, "status": existing.status}
    row = Appeal(penalty_id=penalty_id, user_id=user.id, reason=data.reason.strip())
    db.add(row)
    db.flush()
    return {"id": row.id, "status": row.status}


# ---------------------------------------------------------------- 私信
class ConversationCreate(BaseModel):
    recipient_id: int
    context_type: str = "direct"
    context_id: int | None = None
    first_message: str = Field(min_length=1, max_length=2000)

    @field_validator("context_type")
    @classmethod
    def valid_context(cls, value: str) -> str:
        if value not in {"direct", "listing", "team", "activity"}:
            raise ValueError("会话上下文无效")
        return value


class MessageCreate(BaseModel):
    body: str = Field(min_length=1, max_length=2000)


def is_blocked(db: Session, a: int, b: int) -> bool:
    return bool(
        db.scalar(
            select(Block.id).where(
                ((Block.user_id == a) & (Block.blocked_id == b)) | ((Block.user_id == b) & (Block.blocked_id == a))
            )
        )
    )


def contextual_contact_allowed(
    db: Session, sender_id: int, recipient_id: int, kind: str, context_id: int | None
) -> bool:
    if kind == "listing" and context_id:
        entity = db.get(ContentEntity, context_id)
        listing = db.get(Listing, context_id)
        return bool(
            entity and listing and entity.owner_id == recipient_id and listing.trade_status in {"available", "reserved"}
        )
    if kind == "team" and context_id:
        count = (
            db.scalar(
                select(func.count(TeamMembership.id)).where(
                    TeamMembership.team_id == context_id,
                    TeamMembership.user_id.in_([sender_id, recipient_id]),
                    TeamMembership.status == "active",
                )
            )
            or 0
        )
        return count == 2
    if kind == "activity" and context_id:
        entity = db.get(ContentEntity, context_id)
        activity = db.get(Activity, context_id)
        participants = set(
            db.scalars(
                select(ActivityMember.user_id).where(
                ActivityMember.activity_id == context_id,
                    ActivityMember.user_id.in_([sender_id, recipient_id]),
                ActivityMember.status == "joined",
                )
            ).all()
        )
        if entity:
            participants.add(entity.owner_id)
        return bool(
            entity
            and activity
            and entity.status == "published"
            and activity.status == "open"
            and {sender_id, recipient_id}.issubset(participants)
        )
    return False


def conversation_payload(db: Session, conversation: Conversation, viewer_id: int) -> dict:
    members = db.scalars(select(ConversationMember).where(ConversationMember.conversation_id == conversation.id)).all()
    other_member = next((x for x in members if x.user_id != viewer_id), None)
    other = db.get(User, other_member.user_id) if other_member else None
    last = db.execute(
        select(ContentEntity, Message)
        .join(Message, Message.entity_id == ContentEntity.id)
        .where(Message.conversation_id == conversation.id, ContentEntity.status == "published")
        .order_by(ContentEntity.created_at.desc())
        .limit(1)
    ).first()
    my_member = next((x for x in members if x.user_id == viewer_id), None)
    unread = (
        db.scalar(
            select(func.count(Message.entity_id))
            .join(ContentEntity, ContentEntity.id == Message.entity_id)
            .where(
                Message.conversation_id == conversation.id,
                ContentEntity.owner_id != viewer_id,
                ContentEntity.status == "published",
                ContentEntity.created_at
                > (my_member.last_read_at if my_member and my_member.last_read_at else conversation.created_at),
            )
        )
        or 0
    )
    return {
        "id": conversation.id,
        "context_type": conversation.context_type,
        "context_id": conversation.context_id,
        "other_user": {"id": other.id, "nickname": other.nickname} if other else None,
        "last_message": last[1].body[:100] if last else "",
        "last_message_at": last[0].created_at if last else conversation.created_at,
        "unread": unread,
    }


def find_pair_conversation(
    db: Session, user_a: int, user_b: int, kind: str, context_id: int | None
) -> Conversation | None:
    candidates = db.scalars(
        select(Conversation).where(Conversation.context_type == kind, Conversation.context_id == context_id)
    ).all()
    for conversation in candidates:
        ids = set(
            db.scalars(
                select(ConversationMember.user_id).where(ConversationMember.conversation_id == conversation.id)
            ).all()
        )
        if ids == {user_a, user_b}:
            return conversation
    return None


def add_message(db: Session, conversation: Conversation, sender: User, body: str) -> Message:
    check_rate_limit(db, "message_send_minute", str(sender.id), 30, 1)
    check_rate_limit(db, "message_send_day", str(sender.id), 300, 24 * 60)
    if sender.credit < 85:
        today = utcnow().replace(hour=0, minute=0, second=0, microsecond=0)
        sent = (
            db.scalar(
                select(func.count(ContentEntity.id)).where(
                    ContentEntity.type == "message",
                    ContentEntity.owner_id == sender.id,
                    ContentEntity.created_at >= today,
                )
            )
            or 0
        )
        is_new = sender.created_at > utcnow() - timedelta(days=7)
        limit = 5 if is_new else 20
        if sent >= limit:
            raise APIError(429, "MESSAGE_LIMIT_REACHED", f"已达到今日私信上限（{limit} 条）")
    entity, _ = create_entity(db, sender.id, "message", body, allow_comments=False, search_visible=False)
    row = Message(entity_id=entity.id, conversation_id=conversation.id, body=body.strip())
    db.add(row)
    return row


@router.get("/conversations")
def list_conversations(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(30, ge=1, le=100),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    ids = db.scalars(select(ConversationMember.conversation_id).where(ConversationMember.user_id == user.id)).all()
    total = len(ids)
    rows = (
        db.scalars(
            select(Conversation)
            .where(Conversation.id.in_(ids))
            .order_by(Conversation.id.desc())
            .offset((page_no - 1) * page_size)
            .limit(page_size)
        ).all()
        if ids
        else []
    )
    return page([conversation_payload(db, x, user.id) for x in rows], page_no, page_size, total)


@router.post("/conversations", status_code=201)
def create_conversation(
    data: ConversationCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    recipient = db.get(User, data.recipient_id)
    if not recipient or recipient.status != "active":
        raise APIError(404, "RECIPIENT_NOT_FOUND", "收件人不存在")
    if recipient.id == user.id:
        raise APIError(400, "SELF_MESSAGE", "不能给自己发私信")
    if is_blocked(db, user.id, recipient.id):
        raise APIError(403, "MESSAGE_BLOCKED", "无法向该用户发送私信")
    context_allowed = contextual_contact_allowed(db, user.id, recipient.id, data.context_type, data.context_id)
    if data.context_type == "direct" and data.context_id is not None:
        raise APIError(400, "INVALID_MESSAGE_CONTEXT", "普通私信不能携带业务上下文")
    if data.context_type != "direct" and not context_allowed:
        raise APIError(403, "INVALID_MESSAGE_CONTEXT", "你们不在该商品、车队或活动上下文中")
    if recipient.dm_stranger_off and not context_allowed:
        raise APIError(403, "STRANGER_MESSAGES_OFF", "对方已关闭陌生人私信")
    conversation = find_pair_conversation(db, user.id, recipient.id, data.context_type, data.context_id)
    if not conversation:
        conversation = Conversation(context_type=data.context_type, context_id=data.context_id)
        db.add(conversation)
        db.flush()
        db.add_all(
            [
                ConversationMember(conversation_id=conversation.id, user_id=user.id, last_read_at=utcnow()),
                ConversationMember(conversation_id=conversation.id, user_id=recipient.id),
            ]
        )
    message = add_message(db, conversation, user, data.first_message)
    notify(db, recipient.id, "收到新私信", f"来自 {user.nickname} 的消息", f"/messages/{conversation.id}", "message")
    return {"conversation": conversation_payload(db, conversation, user.id), "message_id": message.entity_id}


@router.get("/conversations/{conversation_id}/messages")
def list_messages(
    conversation_id: int,
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(50, ge=1, le=200),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    membership = db.scalar(
        select(ConversationMember).where(
            ConversationMember.conversation_id == conversation_id,
            ConversationMember.user_id == user.id,
        )
    )
    if not membership:
        raise APIError(403, "CONVERSATION_MEMBER_REQUIRED", "无权查看该会话")
    total = db.scalar(
        select(func.count(Message.entity_id))
        .join(ContentEntity, ContentEntity.id == Message.entity_id)
        .where(Message.conversation_id == conversation_id, ContentEntity.status == "published")
    ) or 0
    rows = db.execute(
        select(ContentEntity, Message)
        .join(Message, Message.entity_id == ContentEntity.id)
        .where(Message.conversation_id == conversation_id, ContentEntity.status == "published")
        .order_by(ContentEntity.created_at.asc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    membership.last_read_at = utcnow()
    return page(
        [
            {
                "id": entity.id,
                "body": message.body,
                "sender_id": entity.owner_id,
                "mine": entity.owner_id == user.id,
                "created_at": entity.created_at,
            }
            for entity, message in rows
        ],
        page_no,
        page_size,
        total,
    )


@router.post("/conversations/{conversation_id}/messages", status_code=201)
def send_message(
    conversation_id: int,
    data: MessageCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    conversation = db.get(Conversation, conversation_id)
    members = db.scalars(select(ConversationMember).where(ConversationMember.conversation_id == conversation_id)).all()
    if not conversation or user.id not in {x.user_id for x in members}:
        raise APIError(403, "CONVERSATION_MEMBER_REQUIRED", "无权向该会话发送消息")
    recipient_id = next(x.user_id for x in members if x.user_id != user.id)
    if is_blocked(db, user.id, recipient_id):
        raise APIError(403, "MESSAGE_BLOCKED", "无法向该用户发送私信")
    row = add_message(db, conversation, user, data.body)
    notify(db, recipient_id, "收到新私信", f"来自 {user.nickname} 的消息", f"/messages/{conversation_id}", "message")
    return {"id": row.entity_id, "body": row.body, "mine": True, "created_at": utcnow()}


@router.put("/blocks/{blocked_id}")
def block_user(blocked_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    if blocked_id == user.id or not db.get(User, blocked_id):
        raise APIError(400, "INVALID_BLOCK_TARGET", "拉黑对象无效")
    insert_unique(
        db,
        Block.__table__,
        {"user_id": user.id, "blocked_id": blocked_id},
        ["user_id", "blocked_id"],
    )
    return {"active": True}


@router.delete("/blocks/{blocked_id}")
def unblock_user(blocked_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    row = db.scalar(select(Block).where(Block.user_id == user.id, Block.blocked_id == blocked_id))
    if row:
        db.delete(row)
    return {"active": False}


# ---------------------------------------------------------------- 通知 / 公告 / 反馈
class FeedbackCreate(BaseModel):
    type: str = "suggestion"
    title: str = Field(min_length=3, max_length=160)
    body: str = Field(min_length=10, max_length=10000)


@router.get("/notifications")
def list_notifications(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(30, ge=1, le=100),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    total = db.scalar(select(func.count(Notification.id)).where(Notification.user_id == user.id)) or 0
    rows = db.scalars(
        select(Notification)
        .where(Notification.user_id == user.id)
        .order_by(Notification.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page(
        [
            {
                "id": x.id,
                "type": x.type,
                "title": x.title,
                "body": x.body,
                "link": x.link,
                "read_at": x.read_at,
                "created_at": x.created_at,
            }
            for x in rows
        ],
        page_no,
        page_size,
        total,
    )


@router.post("/notifications/{notification_id}/read")
def read_notification(
    notification_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.get(Notification, notification_id)
    if not row or row.user_id != user.id:
        raise APIError(404, "NOTIFICATION_NOT_FOUND", "通知不存在")
    row.read_at = row.read_at or utcnow()
    return {"ok": True}


@router.post("/notifications/read-all")
def read_all_notifications(user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    db.query(Notification).filter(Notification.user_id == user.id, Notification.read_at.is_(None)).update(
        {Notification.read_at: utcnow()}, synchronize_session=False
    )
    return {"ok": True}


@router.get("/announcements")
def list_announcements(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=100),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    audience_filter = Announcement.audience == "all"
    if viewer:
        audience_filter = audience_filter | (Announcement.audience == viewer.campus_identity)
    total = db.scalar(select(func.count(Announcement.id)).where(audience_filter)) or 0
    rows = db.scalars(
        select(Announcement)
        .where(audience_filter)
        .order_by(Announcement.published_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    items = []
    for row in rows:
        read = False
        if viewer:
            read = bool(
                db.scalar(
                    select(AnnouncementRead.id).where(
                        AnnouncementRead.announcement_id == row.id, AnnouncementRead.user_id == viewer.id
                    )
                )
            )
        items.append(
            {
                "id": row.id,
                "title": row.title,
                "body": row.body,
                "level": row.level,
                "audience": row.audience,
                "read": read,
                "read_count": db.scalar(
                    select(func.count(AnnouncementRead.id)).where(AnnouncementRead.announcement_id == row.id)
                )
                or 0,
                "published_at": row.published_at,
            }
        )
    return page(items, page_no, page_size, total)


@router.put("/announcements/{announcement_id}/read")
def read_announcement(
    announcement_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    announcement = db.get(Announcement, announcement_id)
    if not announcement:
        raise APIError(404, "ANNOUNCEMENT_NOT_FOUND", "公告不存在")
    if announcement.audience != "all" and announcement.audience != user.campus_identity:
        raise APIError(404, "ANNOUNCEMENT_NOT_FOUND", "公告不存在")
    insert_unique(
        db,
        AnnouncementRead.__table__,
        {"announcement_id": announcement_id, "user_id": user.id},
        ["announcement_id", "user_id"],
    )
    return {"active": True}


@router.post("/feedback", status_code=201)
def create_feedback(data: FeedbackCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    entity, _ = create_entity(db, user.id, "feedback", f"{data.title}\n{data.body}", search_visible=False)
    row = Feedback(entity_id=entity.id, type=data.type, title=data.title.strip(), body=data.body.strip())
    db.add(row)
    return {"id": entity.id, "status": row.status}
