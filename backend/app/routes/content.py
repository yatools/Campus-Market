from __future__ import annotations

import io
import secrets
from datetime import timedelta
from pathlib import Path

from fastapi import APIRouter, Depends, File, Query, Request, UploadFile
from PIL import Image, ImageOps, UnidentifiedImageError
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import delete, func, literal, or_, select, union_all
from sqlalchemy.orm import Session

from ..config import get_settings
from ..credit import require_credit
from ..database import get_db
from ..deps import current_user, optional_user, participating_user
from ..errors import APIError
from ..models import (
    Activity,
    Attachment,
    Comment,
    ContentEntity,
    ContentRevision,
    Favorite,
    HandbookArticle,
    Listing,
    LostItem,
    ModerationCase,
    Post,
    Question,
    Reaction,
    Report,
    User,
    utcnow,
)
from ..services import (
    audit,
    author_name,
    create_entity,
    insert_unique,
    notify,
    public_entity_or_404,
    record_revision,
    remoderate_entity,
    touch_entity,
)

router = APIRouter(tags=["内容与互动"])
settings = get_settings()
IDENTITY_MODES = {"nickname", "alias", "anonymous"}
Image.MAX_IMAGE_PIXELS = 40_000_000


class PostCreate(BaseModel):
    title: str = Field(default="", max_length=120)
    body: str = Field(min_length=1, max_length=10000)
    identity_mode: str = "anonymous"
    visibility: str = "forever"
    allow_comments: bool = True
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)

    @field_validator("identity_mode")
    @classmethod
    def identity_valid(cls, value: str) -> str:
        if value not in IDENTITY_MODES:
            raise ValueError("身份模式无效")
        return value

    @field_validator("visibility")
    @classmethod
    def visibility_valid(cls, value: str) -> str:
        if value not in {"24h", "7d", "forever"}:
            raise ValueError("可见期无效")
        return value


class PostUpdate(BaseModel):
    title: str | None = Field(default=None, max_length=120)
    body: str | None = Field(default=None, min_length=1, max_length=10000)
    allow_comments: bool | None = None
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class CommentCreate(BaseModel):
    body: str = Field(min_length=1, max_length=3000)
    parent_id: int | None = None
    identity_mode: str = "nickname"
    attachment_ids: list[int] = Field(default_factory=list, max_length=6)

    @field_validator("identity_mode")
    @classmethod
    def identity_valid(cls, value: str) -> str:
        if value not in IDENTITY_MODES:
            raise ValueError("身份模式无效")
        return value


class ReportCreate(BaseModel):
    reason: str = Field(min_length=2, max_length=80)
    detail: str = Field(default="", max_length=2000)


def page_meta(items: list, page: int, page_size: int, total: int) -> dict:
    return {"items": items, "page": page, "page_size": page_size, "total": total}


def counts_for(db: Session, entity_id: int) -> dict:
    return {
        "likes": db.scalar(
            select(func.count(Reaction.id)).where(Reaction.entity_id == entity_id, Reaction.type == "like")
        )
        or 0,
        "favorites": db.scalar(select(func.count(Favorite.id)).where(Favorite.entity_id == entity_id)) or 0,
        "comments": db.scalar(
            select(func.count(Comment.entity_id))
            .join(ContentEntity, ContentEntity.id == Comment.entity_id)
            .where(Comment.target_entity_id == entity_id, ContentEntity.status == "published")
        )
        or 0,
    }


def attachment_payload(db: Session, entity_id: int) -> list[dict]:
    rows = db.scalars(
        select(Attachment).where(Attachment.entity_id == entity_id, Attachment.status == "attached")
    ).all()
    return [
        {
            "id": row.id,
            "url": f"/uploads/{row.path}",
            "thumbnail_url": f"/uploads/{row.thumbnail_path or row.path}",
            "width": row.width,
            "height": row.height,
        }
        for row in rows
    ]


def post_payload(db: Session, entity: ContentEntity, post: Post, viewer: User | None = None) -> dict:
    data = {
        "id": entity.id,
        "type": entity.type,
        "status": entity.status,
        "title": post.title,
        "body": post.body,
        "board": post.board,
        "author": author_name(db, entity, post.identity_mode, entity.id),
        "identity_mode": post.identity_mode,
        "allow_comments": entity.allow_comments,
        "expires_at": post.expires_at,
        "views": post.views,
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
        "mine": bool(viewer and viewer.id == entity.owner_id),
        "attachments": attachment_payload(db, entity.id),
    }
    data.update(counts_for(db, entity.id))
    if viewer:
        data["liked"] = bool(
            db.scalar(
                select(Reaction.id).where(
                    Reaction.entity_id == entity.id, Reaction.user_id == viewer.id, Reaction.type == "like"
                )
            )
        )
        data["favorited"] = bool(
            db.scalar(select(Favorite.id).where(Favorite.entity_id == entity.id, Favorite.user_id == viewer.id))
        )
    return data


def attach_uploads(db: Session, user: User, entity_id: int, attachment_ids: list[int]) -> None:
    if not attachment_ids:
        return
    rows = db.scalars(
        select(Attachment).where(Attachment.id.in_(attachment_ids), Attachment.owner_id == user.id).with_for_update()
    ).all()
    if len(rows) != len(set(attachment_ids)) or any(x.status != "pending" for x in rows):
        raise APIError(400, "INVALID_ATTACHMENTS", "附件不存在、已使用或不属于当前用户")
    for row in rows:
        row.entity_id = entity_id
        row.status = "attached"
    db.flush()


@router.get("/posts")
def list_posts(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=50),
    q: str = Query("", max_length=80),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    now = utcnow()
    filters = [
        ContentEntity.type == "post",
        ContentEntity.status == "published",
        Post.board == "treehole",
        or_(Post.expires_at.is_(None), Post.expires_at > now),
    ]
    if q:
        pattern = f"%{q}%"
        filters.extend(
            [ContentEntity.search_visible.is_(True), or_(Post.title.ilike(pattern), Post.body.ilike(pattern))]
        )
    total = (
        db.scalar(select(func.count(ContentEntity.id)).join(Post, Post.entity_id == ContentEntity.id).where(*filters))
        or 0
    )
    rows = db.execute(
        select(ContentEntity, Post)
        .join(Post, Post.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return page_meta([post_payload(db, entity, post, viewer) for entity, post in rows], page, page_size, total)


@router.post("/posts", status_code=201)
def create_post(data: PostCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    if data.identity_mode == "anonymous":
        require_credit(db, user, "threshold.anonymous_post", "匿名发帖")
    expires = {"24h": utcnow() + timedelta(hours=24), "7d": utcnow() + timedelta(days=7)}.get(data.visibility)
    entity, care = create_entity(
        db,
        user.id,
        "post",
        f"{data.title}\n{data.body}",
        allow_comments=data.allow_comments,
        search_visible=data.identity_mode != "anonymous",
    )
    post = Post(
        entity_id=entity.id,
        board="treehole",
        title=data.title.strip(),
        body=data.body.strip(),
        identity_mode=data.identity_mode,
        expires_at=expires,
    )
    db.add(post)
    attach_uploads(db, user, entity.id, data.attachment_ids)
    audit(db, user.id, "post.create", "content", entity.id)
    payload = post_payload(db, entity, post, user)
    payload["care"] = care
    payload["care_message"] = (
        "如果你正在经历难以承受的时刻，请联系校心理中心或 24 小时心理援助热线 12356。" if care else ""
    )
    return payload


@router.get("/posts/{post_id}")
def get_post(
    post_id: int,
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, post_id)
    post = db.get(Post, post_id)
    if not entity or not post:
        raise APIError(404, "POST_NOT_FOUND", "帖子不存在")
    can_preview = viewer and (viewer.id == entity.owner_id or viewer.role in {"moderator", "admin"})
    if entity.status != "published" and not can_preview:
        raise APIError(404, "POST_NOT_FOUND", "帖子不存在")
    if post.expires_at and post.expires_at <= utcnow() and not can_preview:
        raise APIError(404, "POST_EXPIRED", "帖子已过期")
    post.views += 1
    return post_payload(db, entity, post, viewer)


@router.patch("/posts/{post_id}")
def update_post(
    post_id: int,
    data: PostUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == post_id).with_for_update())
    post = db.get(Post, post_id)
    if not entity or not post:
        raise APIError(404, "POST_NOT_FOUND", "帖子不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "NOT_OWNER", "无权修改该帖子")
    content_changed = data.title is not None or data.body is not None
    if content_changed:
        record_revision(db, entity, user.id, post.title, post.body)
    if data.title is not None:
        post.title = data.title.strip()
    if data.body is not None:
        post.body = data.body.strip()
    if data.allow_comments is not None:
        entity.allow_comments = data.allow_comments
    attach_uploads(db, user, entity.id, data.attachment_ids)
    remoderate_entity(db, entity, f"{post.title}\n{post.body}")
    touch_entity(db, entity.id)
    audit(db, user.id, "post.update", "content", entity.id)
    return post_payload(db, entity, post, user)


@router.delete("/entities/{entity_id}")
def delete_entity(
    entity_id: int,
    request: Request,
    reason: str = Query("", max_length=1000),
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, entity_id)
    if not entity:
        raise APIError(404, "CONTENT_NOT_FOUND", "内容不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "NOT_OWNER", "无权删除该内容")
    if entity.owner_id != user.id and len(reason.strip()) < 2:
        raise APIError(400, "DELETION_REASON_REQUIRED", "审核人员删除内容时必须填写理由")
    before_status = entity.status
    question = db.get(Question, entity_id)
    if question and not question.bounty_settled and not question.accepted_answer_id:
        owner = db.get(User, entity.owner_id)
        owner.xp += question.bounty_xp
        question.bounty_settled = True
    entity.status = "deleted"
    entity.deleted_at = utcnow()
    entity.search_visible = False
    audit(
        db,
        user.id,
        "content.delete",
        entity.type,
        entity.id,
        reason.strip() or "作者自行删除",
        before={"status": before_status},
        after={"status": entity.status, "deleted_at": entity.deleted_at},
        request_id=getattr(request.state, "request_id", ""),
    )
    return {"ok": True}


@router.get("/entities/{entity_id}/revisions")
def list_revisions(
    entity_id: int,
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, entity_id)
    if not entity:
        raise APIError(404, "CONTENT_NOT_FOUND", "内容不存在")
    if not viewer or (viewer.id != entity.owner_id and viewer.role not in {"moderator", "admin"}):
        raise APIError(403, "REVISION_ACCESS_DENIED", "只有作者和审核人员可以查看历史版本")
    total = db.scalar(select(func.count(ContentRevision.id)).where(ContentRevision.entity_id == entity_id)) or 0
    rows = db.scalars(
        select(ContentRevision)
        .where(ContentRevision.entity_id == entity_id)
        .order_by(ContentRevision.revision.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return page_meta(
        [
            {
                "id": row.id,
                "revision": row.revision,
                "title": row.title,
                "body": row.body,
                "editor_id": row.editor_id,
                "created_at": row.created_at,
            }
            for row in rows
        ],
        page,
        page_size,
        total,
    )


def comment_payload(db: Session, entity: ContentEntity, comment: Comment, viewer: User | None) -> dict:
    payload = {
        "id": entity.id,
        "target_entity_id": comment.target_entity_id,
        "parent_id": comment.parent_id,
        "reply_to_user_id": comment.reply_to_user_id,
        "body": comment.body if entity.status == "published" else "该回帖已隐藏",
        "author": author_name(db, entity, comment.identity_mode, comment.target_entity_id),
        "identity_mode": comment.identity_mode,
        "status": entity.status,
        "mine": bool(viewer and entity.owner_id == viewer.id),
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
        "attachments": attachment_payload(db, entity.id),
        "likes": db.scalar(
            select(func.count(Reaction.id)).where(Reaction.entity_id == entity.id, Reaction.type == "like")
        )
        or 0,
    }
    if viewer:
        payload["liked"] = bool(
            db.scalar(
                select(Reaction.id).where(
                    Reaction.entity_id == entity.id, Reaction.user_id == viewer.id, Reaction.type == "like"
                )
            )
        )
    return payload


@router.get("/entities/{target_id}/comments")
def list_comments(
    target_id: int,
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=50),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    public_entity_or_404(db, target_id)
    root_filter = [Comment.target_entity_id == target_id, Comment.parent_id.is_(None)]
    total = db.scalar(select(func.count(Comment.entity_id)).where(*root_filter)) or 0
    roots = db.execute(
        select(ContentEntity, Comment)
        .join(Comment, Comment.entity_id == ContentEntity.id)
        .where(*root_filter)
        .order_by(ContentEntity.created_at.asc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    root_ids = [entity.id for entity, _ in roots]
    reply_rows = []
    if root_ids:
        reply_rows = db.execute(
            select(ContentEntity, Comment)
            .join(Comment, Comment.entity_id == ContentEntity.id)
            .where(Comment.parent_id.in_(root_ids))
            .order_by(ContentEntity.created_at.asc())
        ).all()
    replies_by_root: dict[int, list[dict]] = {root_id: [] for root_id in root_ids}
    for entity, comment in reply_rows:
        replies_by_root.setdefault(comment.parent_id or 0, []).append(comment_payload(db, entity, comment, viewer))
    items = []
    for entity, comment in roots:
        item = comment_payload(db, entity, comment, viewer)
        item["replies"] = replies_by_root.get(entity.id, [])
        items.append(item)
    return page_meta(items, page, page_size, total)


@router.post("/entities/{target_id}/comments", status_code=201)
def create_comment(
    target_id: int,
    data: CommentCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    target = db.get(ContentEntity, target_id)
    if not target or target.status != "published":
        raise APIError(404, "CONTENT_NOT_FOUND", "评论对象不存在")
    if not target.allow_comments:
        raise APIError(403, "COMMENTS_CLOSED", "发布者已关闭回帖")
    if data.identity_mode != "nickname":
        post = db.get(Post, target_id)
        if not post or post.board != "treehole":
            raise APIError(400, "IDENTITY_MODE_NOT_ALLOWED", "该板块回帖需显示昵称")
    parent: Comment | None = None
    reply_to: int | None = None
    if data.parent_id:
        parent = db.get(Comment, data.parent_id)
        parent_entity = db.get(ContentEntity, data.parent_id)
        if (
            not parent
            or not parent_entity
            or parent_entity.status != "published"
            or parent.target_entity_id != target_id
        ):
            raise APIError(404, "PARENT_NOT_FOUND", "被回复的内容不存在")
        reply_to = parent_entity.owner_id
        if parent.parent_id is not None:
            parent = db.get(Comment, parent.parent_id)
        if not parent:
            raise APIError(404, "PARENT_NOT_FOUND", "被回复的内容不存在")
    entity, _ = create_entity(
        db,
        user.id,
        "comment",
        data.body,
        allow_comments=False,
        search_visible=False,
    )
    comment = Comment(
        entity_id=entity.id,
        target_entity_id=target_id,
        parent_id=parent.entity_id if parent else None,
        reply_to_user_id=reply_to,
        body=data.body.strip(),
        identity_mode=data.identity_mode,
    )
    db.add(comment)
    attach_uploads(db, user, entity.id, data.attachment_ids)
    touch_entity(db, target_id)
    recipient = reply_to or target.owner_id
    if recipient != user.id and entity.status == "published":
        notify(
            db,
            recipient,
            "收到新回帖",
            f"{author_name(db, entity, data.identity_mode, target_id)} 回复了你的内容",
            f"/content/{target_id}",
            "reply",
        )
    return comment_payload(db, entity, comment, user)


@router.put("/entities/{entity_id}/reactions/{reaction_type}")
def add_reaction(
    entity_id: int,
    reaction_type: str,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    if reaction_type != "like":
        raise APIError(400, "REACTION_NOT_SUPPORTED", "暂不支持该互动类型")
    public_entity_or_404(db, entity_id)
    insert_unique(
        db,
        Reaction.__table__,
        {"entity_id": entity_id, "user_id": user.id, "type": reaction_type},
        ["entity_id", "user_id", "type"],
    )
    db.flush()
    count = (
        db.scalar(
            select(func.count(Reaction.id)).where(Reaction.entity_id == entity_id, Reaction.type == reaction_type)
        )
        or 0
    )
    return {"active": True, "count": count}


@router.delete("/entities/{entity_id}/reactions/{reaction_type}")
def remove_reaction(
    entity_id: int,
    reaction_type: str,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    db.execute(
        delete(Reaction).where(
            Reaction.entity_id == entity_id, Reaction.user_id == user.id, Reaction.type == reaction_type
        )
    )
    count = (
        db.scalar(
            select(func.count(Reaction.id)).where(Reaction.entity_id == entity_id, Reaction.type == reaction_type)
        )
        or 0
    )
    return {"active": False, "count": count}


@router.put("/entities/{entity_id}/favorite")
def favorite(entity_id: int, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    public_entity_or_404(db, entity_id)
    insert_unique(
        db,
        Favorite.__table__,
        {"entity_id": entity_id, "user_id": user.id},
        ["entity_id", "user_id"],
    )
    return {"active": True}


@router.delete("/entities/{entity_id}/favorite")
def unfavorite(entity_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    db.execute(delete(Favorite).where(Favorite.entity_id == entity_id, Favorite.user_id == user.id))
    return {"active": False}


@router.post("/entities/{entity_id}/reports", status_code=201)
def report(
    entity_id: int,
    data: ReportCreate,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = public_entity_or_404(db, entity_id)
    if entity.owner_id == user.id:
        raise APIError(400, "SELF_REPORT", "不能举报自己的内容")
    report_created = insert_unique(
        db,
        Report.__table__,
        {
            "entity_id": entity_id,
            "reporter_id": user.id,
            "reason": data.reason,
            "detail": data.detail,
            "status": "pending",
        },
        ["entity_id", "reporter_id"],
    )
    insert_unique(
        db,
        ModerationCase.__table__,
        {"entity_id": entity_id, "source": "report", "status": "pending"},
        ["entity_id"],
    )
    case = db.scalar(select(ModerationCase).where(ModerationCase.entity_id == entity_id).with_for_update())
    if report_created and case and case.status != "pending":
        case.status = "pending"
        case.source = "report"
        case.assignee_id = None
        case.decision = ""
        case.decided_at = None
    return {"accepted": True}


@router.post("/uploads/images", status_code=201)
async def upload_image(
    file: UploadFile = File(...),
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    allowed = {"image/jpeg": "JPEG", "image/png": "PNG", "image/webp": "WEBP"}
    if file.content_type not in allowed:
        raise APIError(400, "UNSAFE_IMAGE_TYPE", "仅支持 JPG、PNG 和 WebP 图片")
    raw = await file.read(settings.max_upload_mb * 1024 * 1024 + 1)
    if len(raw) > settings.max_upload_mb * 1024 * 1024:
        raise APIError(413, "IMAGE_TOO_LARGE", f"图片不能超过 {settings.max_upload_mb}MB")
    try:
        image = Image.open(io.BytesIO(raw))
        detected_format = (image.format or "").upper()
        if detected_format != allowed[file.content_type]:
            raise APIError(400, "IMAGE_MIME_MISMATCH", "图片声明类型与实际格式不一致")
        image.verify()
        image = Image.open(io.BytesIO(raw))
        if image.width * image.height > 40_000_000 or max(image.width, image.height) > 16_384:
            raise APIError(400, "IMAGE_DIMENSIONS_TOO_LARGE", "图片像素尺寸过大")
        image = ImageOps.exif_transpose(image).convert("RGB")
    except APIError:
        raise
    except (Image.DecompressionBombError, UnidentifiedImageError, OSError) as exc:
        raise APIError(400, "INVALID_IMAGE", "图片文件无效") from exc
    day = utcnow().strftime("%Y/%m/%d")
    relative_dir = Path(day)
    absolute_dir = settings.upload_dir / relative_dir
    absolute_dir.mkdir(parents=True, exist_ok=True)
    key = secrets.token_hex(20)
    path = relative_dir / f"{key}.webp"
    thumb_path = relative_dir / f"{key}-thumb.webp"
    image.save(settings.upload_dir / path, "WEBP", quality=86, method=6)
    thumb = image.copy()
    thumb.thumbnail((640, 640))
    thumb.save(settings.upload_dir / thumb_path, "WEBP", quality=80, method=6)
    row = Attachment(
        owner_id=user.id,
        path=path.as_posix(),
        thumbnail_path=thumb_path.as_posix(),
        mime_type="image/webp",
        size_bytes=(settings.upload_dir / path).stat().st_size,
        width=image.width,
        height=image.height,
    )
    db.add(row)
    db.flush()
    return {
        "id": row.id,
        "url": f"/uploads/{row.path}",
        "thumbnail_url": f"/uploads/{row.thumbnail_path}",
        "width": row.width,
        "height": row.height,
    }


@router.get("/search")
def search(
    q: str = Query(min_length=2, max_length=80),
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=50),
    db: Session = Depends(get_db),
) -> dict:
    pattern = f"%{q}%"
    common = [ContentEntity.status == "published", ContentEntity.search_visible.is_(True)]
    queries = [
        select(
            ContentEntity.id.label("id"),
            literal("post").label("type"),
            func.coalesce(func.nullif(Post.title, ""), func.substr(Post.body, 1, 40)).label("title"),
            func.substr(Post.body, 1, 120).label("summary"),
            ContentEntity.created_at.label("created_at"),
        )
        .join(Post, Post.entity_id == ContentEntity.id)
        .where(*common, or_(Post.title.ilike(pattern), Post.body.ilike(pattern)))
    ]
    model_specs = [
        (Question, Question.title, Question.body, "question"),
        (HandbookArticle, HandbookArticle.title, HandbookArticle.body, "handbook"),
        (Listing, Listing.title, Listing.description, "listing"),
        (Activity, Activity.title, Activity.body, "activity"),
        (LostItem, LostItem.item_name, LostItem.description, "lost"),
    ]
    for model, title_col, body_col, kind in model_specs:
        queries.append(
            select(
                ContentEntity.id.label("id"),
                literal(kind).label("type"),
                title_col.label("title"),
                func.substr(body_col, 1, 120).label("summary"),
                ContentEntity.created_at.label("created_at"),
            )
            .join(model, model.entity_id == ContentEntity.id)
            .where(*common, or_(title_col.ilike(pattern), body_col.ilike(pattern)))
        )
    combined = union_all(*queries).subquery()
    total = db.scalar(select(func.count()).select_from(combined)) or 0
    rows = db.execute(
        select(combined)
        .order_by(combined.c.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    items = [{"id": row.id, "type": row.type, "title": row.title, "summary": row.summary} for row in rows]
    return page_meta(items, page, page_size, total)


@router.get("/hot")
def hot(db: Session = Depends(get_db)) -> dict:
    cutoff = utcnow() - timedelta(days=14)
    reaction_counts = (
        select(Reaction.entity_id.label("entity_id"), func.count(Reaction.id).label("likes"))
        .where(Reaction.type == "like")
        .group_by(Reaction.entity_id)
        .subquery()
    )
    favorite_counts = (
        select(Favorite.entity_id.label("entity_id"), func.count(Favorite.id).label("favorites"))
        .group_by(Favorite.entity_id)
        .subquery()
    )
    comment_counts = (
        select(Comment.target_entity_id.label("entity_id"), func.count(Comment.entity_id).label("comments"))
        .join(ContentEntity, ContentEntity.id == Comment.entity_id)
        .where(ContentEntity.status == "published")
        .group_by(Comment.target_entity_id)
        .subquery()
    )
    rows = db.execute(
        select(
            ContentEntity.id,
            ContentEntity.type,
            ContentEntity.created_at,
            func.coalesce(reaction_counts.c.likes, 0).label("likes"),
            func.coalesce(favorite_counts.c.favorites, 0).label("favorites"),
            func.coalesce(comment_counts.c.comments, 0).label("comments"),
        )
        .outerjoin(reaction_counts, reaction_counts.c.entity_id == ContentEntity.id)
        .outerjoin(favorite_counts, favorite_counts.c.entity_id == ContentEntity.id)
        .outerjoin(comment_counts, comment_counts.c.entity_id == ContentEntity.id)
        .where(ContentEntity.status == "published", ContentEntity.created_at >= cutoff)
        .order_by(ContentEntity.created_at.desc())
        .limit(200)
    ).all()
    ids = [row.id for row in rows]
    post_titles = {
        row.entity_id: row.title or row.body[:40]
        for row in db.scalars(select(Post).where(Post.entity_id.in_(ids))).all()
    }
    question_titles = {
        row.entity_id: row.title for row in db.scalars(select(Question).where(Question.entity_id.in_(ids))).all()
    }
    listing_titles = {
        row.entity_id: row.title for row in db.scalars(select(Listing).where(Listing.entity_id.in_(ids))).all()
    }
    activity_titles = {
        row.entity_id: row.title for row in db.scalars(select(Activity).where(Activity.entity_id.in_(ids))).all()
    }
    title_maps = {"post": post_titles, "question": question_titles, "listing": listing_titles, "activity": activity_titles}
    ranked: list[dict] = []
    for row in rows:
        counts = {"likes": row.likes, "favorites": row.favorites, "comments": row.comments}
        age_hours = max(0.0, (utcnow() - row.created_at).total_seconds() / 3600)
        score = (row.likes + row.favorites * 3 + row.comments * 2) / (1 + age_hours / 24)
        title = title_maps.get(row.type, {}).get(row.id, row.type)
        ranked.append({"id": row.id, "type": row.type, "title": title, "score": round(score, 2), **counts})
    ranked.sort(key=lambda x: x["score"], reverse=True)
    return page_meta(ranked[:20], 1, 20, min(20, len(ranked)))
