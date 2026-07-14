from __future__ import annotations

from datetime import datetime

from fastapi import APIRouter, Depends, Query
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import optional_user
from ..models import (
    Activity,
    Attachment,
    Comment,
    ContentEntity,
    Course,
    CourseOffering,
    CourseReview,
    Favorite,
    HandbookArticle,
    Listing,
    LostItem,
    ObservePost,
    Post,
    Question,
    Reaction,
    Team,
    User,
    db_datetime,
    utcnow,
)
from ..services import author_name

router = APIRouter(prefix="/feed", tags=["首页动态"])
FEED_TYPES = {"post", "team", "question", "handbook", "course_review", "listing", "activity", "lost_item", "observe"}


def attachments(db: Session, entity_id: int) -> list[dict]:
    rows = db.scalars(
        select(Attachment).where(Attachment.entity_id == entity_id, Attachment.status == "attached")
    ).all()
    return [
        {
            "id": row.id, "url": f"/uploads/{row.path}",
            "thumbnail_url": f"/uploads/{row.thumbnail_path or row.path}",
            "width": row.width, "height": row.height,
        }
        for row in rows
    ]


def metrics(db: Session, entity_id: int) -> dict:
    return {
        "likes": db.scalar(select(func.count(Reaction.id)).where(Reaction.entity_id == entity_id, Reaction.type == "like")) or 0,
        "favorites": db.scalar(select(func.count(Favorite.id)).where(Favorite.entity_id == entity_id)) or 0,
        "comments": db.scalar(
            select(func.count(Comment.entity_id)).join(ContentEntity, ContentEntity.id == Comment.entity_id)
            .where(Comment.target_entity_id == entity_id, ContentEntity.status == "published")
        ) or 0,
    }


def feed_payload(db: Session, entity: ContentEntity, viewer: User | None) -> dict | None:
    base = {
        "id": entity.id, "type": entity.type, "created_at": entity.created_at, "updated_at": entity.updated_at,
        "attachments": attachments(db, entity.id), "meta": {}, "route": "/", "author": "",
    }
    if entity.type == "post":
        row = db.get(Post, entity.id)
        if not row:
            return None
        base.update(title=row.title, body=row.body, author=author_name(db, entity, row.identity_mode, entity.id), route="/treehole")
        base["meta"] = {"identity_mode": row.identity_mode, "expires_at": row.expires_at, "views": row.views}
    elif entity.type == "team":
        row = db.get(Team, entity.id)
        if not row or row.status != "active":
            return None
        base.update(title=f"{row.game} · {row.mode}", body=row.notes, author=(db.get(User, row.owner_id).nickname if db.get(User, row.owner_id) else "已注销用户"), route=f"/teams/{entity.id}")
        base["meta"] = {"game": row.game, "game_id": row.game_id, "capacity": row.capacity, "newbie_level": row.newbie_level, "vibe": row.vibe}
    elif entity.type == "question":
        row = db.get(Question, entity.id)
        if not row:
            return None
        base.update(title=row.title, body=row.body, author=author_name(db, entity, "nickname"), route="/explore/questions")
        base["meta"] = {"category": row.category, "bounty_xp": row.bounty_xp, "accepted": bool(row.accepted_answer_id)}
    elif entity.type == "handbook":
        row = db.get(HandbookArticle, entity.id)
        if not row:
            return None
        base.update(title=row.title, body=row.body, author=author_name(db, entity, "nickname"), route="/explore/handbook")
        base["meta"] = {"category": row.category, "featured": bool(row.featured_at)}
    elif entity.type == "course_review":
        row = db.get(CourseReview, entity.id)
        offering = db.get(CourseOffering, row.offering_id) if row else None
        course = db.get(Course, offering.course_id) if offering else None
        if not row or not offering or not course:
            return None
        base.update(title=f"{course.name} · {course.teacher}", body=row.body, author="匿名课评", route="/explore/courses")
        base["meta"] = {"rating": row.rating, "semester": offering.semester, "tags": row.tags.split(",") if row.tags else []}
    elif entity.type == "listing":
        row = db.get(Listing, entity.id)
        if not row or row.trade_status not in {"available", "reserved"}:
            return None
        base.update(title=row.title, body=row.description, author=author_name(db, entity, "nickname"), route="/explore/listings")
        base["meta"] = {"category": row.category, "price": row.price, "condition": row.condition, "location": row.location, "negotiable": row.negotiable}
    elif entity.type == "activity":
        row = db.get(Activity, entity.id)
        if not row or row.status != "open":
            return None
        base.update(title=row.title, body=row.body, author=author_name(db, entity, "nickname"), route="/explore/activities")
        base["meta"] = {"category": row.category, "location": row.location, "starts_at": row.starts_at, "capacity": row.capacity}
    elif entity.type == "lost_item":
        row = db.get(LostItem, entity.id)
        if not row:
            return None
        base.update(title=row.item_name, body=row.description, author=author_name(db, entity, "nickname"), route="/explore/lost")
        base["meta"] = {"kind": row.kind, "location": row.location, "status": row.status}
    elif entity.type == "observe":
        row = db.get(ObservePost, entity.id)
        if not row:
            return None
        base.update(title=row.title, body=row.body_masked, author="文明观察员", route="/explore/observe")
        base["meta"] = {"responded": bool(row.response)}
    else:
        return None
    base.update(metrics(db, entity.id))
    if viewer:
        base["liked"] = bool(db.scalar(select(Reaction.id).where(Reaction.entity_id == entity.id, Reaction.user_id == viewer.id, Reaction.type == "like")))
        base["favorited"] = bool(db.scalar(select(Favorite.id).where(Favorite.entity_id == entity.id, Favorite.user_id == viewer.id)))
    return base


@router.get("")
def list_feed(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=50),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published", ContentEntity.type.in_(FEED_TYPES)]
    total = db.scalar(select(func.count(ContentEntity.id)).where(*filters)) or 0
    entities = db.scalars(
        select(ContentEntity).where(*filters).order_by(ContentEntity.updated_at.desc(), ContentEntity.id.desc())
        .offset((page - 1) * page_size).limit(page_size)
    ).all()
    items = [item for entity in entities if (item := feed_payload(db, entity, viewer)) is not None]
    return {"items": items, "page": page, "page_size": page_size, "total": total, "watermark": utcnow()}


@router.get("/changes")
def feed_changes(after: datetime, db: Session = Depends(get_db)) -> dict:
    watermark = utcnow()
    after = db_datetime(after)
    count = db.scalar(
        select(func.count(ContentEntity.id)).where(
            ContentEntity.status == "published",
            ContentEntity.type.in_(FEED_TYPES),
            ContentEntity.updated_at > after,
            ContentEntity.updated_at <= watermark,
        )
    ) or 0
    return {"count": count, "watermark": watermark}
