from __future__ import annotations

from collections import Counter
from datetime import date, datetime

from fastapi import APIRouter, Depends, Query
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..credit import apply_credit_rule, require_credit
from ..database import get_db
from ..deps import current_user, moderator_user, optional_user, participating_user
from ..errors import APIError
from ..models import (
    Activity,
    ActivityMember,
    Answer,
    Attachment,
    ContentEntity,
    Course,
    CourseOffering,
    CourseReview,
    Favorite,
    HandbookArticle,
    Listing,
    LostClaim,
    LostItem,
    Question,
    User,
    db_datetime,
    utcnow,
)
from ..services import (
    audit,
    author_name,
    create_entity,
    moderate_text,
    notify,
    record_revision,
    remoderate_entity,
    touch_entity,
)

router = APIRouter(tags=["校园模块"])


def page(items: list, current: int, size: int, total: int) -> dict:
    return {"items": items, "page": current, "page_size": size, "total": total}


def files(db: Session, entity_id: int) -> list[dict]:
    return [
        {"id": x.id, "url": f"/uploads/{x.path}", "thumbnail_url": f"/uploads/{x.thumbnail_path or x.path}"}
        for x in db.scalars(
            select(Attachment).where(Attachment.entity_id == entity_id, Attachment.status == "attached")
        ).all()
    ]


def bind_files(db: Session, user: User, entity_id: int, ids: list[int]) -> None:
    if not ids:
        return
    rows = db.scalars(
        select(Attachment).where(Attachment.id.in_(ids), Attachment.owner_id == user.id).with_for_update()
    ).all()
    if len(rows) != len(set(ids)) or any(x.status != "pending" for x in rows):
        raise APIError(400, "INVALID_ATTACHMENTS", "附件不存在、已使用或不属于当前用户")
    for row in rows:
        row.entity_id = entity_id
        row.status = "attached"
    db.flush()


# ---------------------------------------------------------------- 问答
class QuestionCreate(BaseModel):
    title: str = Field(min_length=4, max_length=160)
    body: str = Field(default="", max_length=10000)
    category: str = Field(default="其他", max_length=60)
    tags: list[str] = Field(default_factory=list, max_length=8)
    bounty_xp: int = Field(default=0, ge=0, le=500)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class QuestionUpdate(BaseModel):
    title: str | None = Field(default=None, min_length=4, max_length=160)
    body: str | None = Field(default=None, max_length=10000)
    category: str | None = Field(default=None, max_length=60)
    tags: list[str] | None = Field(default=None, max_length=8)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class AnswerCreate(BaseModel):
    body: str = Field(min_length=2, max_length=10000)
    attachment_ids: list[int] = Field(default_factory=list, max_length=6)


def answer_payload(db: Session, entity: ContentEntity, answer: Answer, viewer: User | None = None) -> dict:
    return {
        "id": entity.id,
        "body": answer.body,
        "author": author_name(db, entity, "nickname"),
        "mine": bool(viewer and entity.owner_id == viewer.id),
        "status": entity.status,
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
        "attachments": files(db, entity.id),
    }


def question_payload(db: Session, entity: ContentEntity, question: Question, viewer: User | None, detail: bool) -> dict:
    answers = db.execute(
        select(ContentEntity, Answer)
        .join(Answer, Answer.entity_id == ContentEntity.id)
        .where(Answer.question_id == entity.id, ContentEntity.status == "published")
        .order_by(ContentEntity.created_at.asc())
    ).all()
    data = {
        "id": entity.id,
        "title": question.title,
        "body": question.body,
        "category": question.category,
        "tags": [x for x in question.tags.split(",") if x],
        "bounty_xp": question.bounty_xp,
        "accepted_answer_id": question.accepted_answer_id,
        "author": author_name(db, entity, "nickname"),
        "answer_count": len(answers),
        "mine": bool(viewer and entity.owner_id == viewer.id),
        "status": entity.status,
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
        "attachments": files(db, entity.id),
    }
    if detail:
        data["answers"] = [answer_payload(db, ae, answer, viewer) for ae, answer in answers]
    return data


@router.get("/questions")
def list_questions(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=50),
    category: str = Query("", max_length=60),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published"]
    if category:
        filters.append(Question.category == category)
    total = (
        db.scalar(
            select(func.count(Question.entity_id))
            .join(ContentEntity, ContentEntity.id == Question.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, Question)
        .join(Question, Question.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([question_payload(db, e, q, viewer, False) for e, q in rows], page_no, page_size, total)


@router.post("/questions", status_code=201)
def create_question(data: QuestionCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    if data.bounty_xp > user.xp:
        raise APIError(400, "XP_NOT_ENOUGH", "经验余额不足以支付悬赏")
    entity, _ = create_entity(db, user.id, "question", f"{data.title}\n{data.body}")
    question = Question(
        entity_id=entity.id,
        title=data.title.strip(),
        body=data.body.strip(),
        category=data.category.strip(),
        tags=",".join(x.strip() for x in data.tags if x.strip()),
        bounty_xp=data.bounty_xp,
    )
    user.xp -= data.bounty_xp
    db.add(question)
    bind_files(db, user, entity.id, data.attachment_ids)
    return question_payload(db, entity, question, user, True)


@router.patch("/questions/{question_id}")
def update_question(
    question_id: int,
    data: QuestionUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == question_id).with_for_update())
    question = db.get(Question, question_id)
    if not entity or not question:
        raise APIError(404, "QUESTION_NOT_FOUND", "问题不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_OWNER", "只有提问者可以编辑问题")
    if question.accepted_answer_id:
        raise APIError(409, "QUESTION_SETTLED", "已有采纳回答的问题不能再编辑")
    values = data.model_dump(exclude_none=True, exclude={"attachment_ids"})
    if not values:
        return question_payload(db, entity, question, user, True)
    record_revision(db, entity, user.id, question.title, question.body)
    for key, value in values.items():
        if key == "tags":
            question.tags = ",".join(x.strip() for x in value if x.strip())
        else:
            setattr(question, key, value.strip() if isinstance(value, str) else value)
    remoderate_entity(db, entity, f"{question.title}\n{question.body}")
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, entity.id)
    audit(db, user.id, "question.update", "question", entity.id)
    return question_payload(db, entity, question, user, True)


@router.get("/questions/{question_id}")
def get_question(question_id: int, viewer: User | None = Depends(optional_user), db: Session = Depends(get_db)) -> dict:
    entity = db.get(ContentEntity, question_id)
    question = db.get(Question, question_id)
    if not entity or not question or (entity.status != "published" and not (viewer and viewer.id == entity.owner_id)):
        raise APIError(404, "QUESTION_NOT_FOUND", "问题不存在")
    return question_payload(db, entity, question, viewer, True)


@router.post("/questions/{question_id}/answers", status_code=201)
def create_answer(
    question_id: int,
    data: AnswerCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    question = db.get(Question, question_id)
    parent = db.get(ContentEntity, question_id)
    if not question or not parent or parent.status != "published":
        raise APIError(404, "QUESTION_NOT_FOUND", "问题不存在")
    entity, _ = create_entity(db, user.id, "answer", data.body)
    answer = Answer(entity_id=entity.id, question_id=question_id, body=data.body.strip())
    db.add(answer)
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, question_id)
    if parent.owner_id != user.id and entity.status == "published":
        notify(db, parent.owner_id, "问题有了新回答", question.title, f"/questions/{question_id}", "answer")
    return answer_payload(db, entity, answer, user)


@router.post("/answers/{answer_id}/accept")
def accept_answer(answer_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    answer = db.get(Answer, answer_id)
    if not answer:
        raise APIError(404, "ANSWER_NOT_FOUND", "回答不存在")
    question = db.scalar(select(Question).where(Question.entity_id == answer.question_id).with_for_update())
    question_entity = db.get(ContentEntity, answer.question_id)
    answer_entity = db.get(ContentEntity, answer_id)
    if not question or not question_entity or not answer_entity or answer_entity.status != "published":
        raise APIError(404, "QUESTION_NOT_FOUND", "问题不存在")
    if question_entity.owner_id != user.id:
        raise APIError(403, "ASKER_REQUIRED", "只有提问者可以采纳")
    if answer_entity.owner_id == user.id:
        raise APIError(400, "SELF_ACCEPT_NOT_ALLOWED", "不能采纳自己的回答")
    if question.accepted_answer_id:
        if question.accepted_answer_id == answer_id:
            return {"ok": True, "awarded_xp": 0}
        raise APIError(409, "ALREADY_ACCEPTED", "该问题已经采纳过回答")
    question.accepted_answer_id = answer_id
    question.bounty_settled = True
    touch_entity(db, question.entity_id)
    answerer = db.get(User, answer_entity.owner_id)
    reward = 20 + question.bounty_xp
    answerer.xp += reward
    notify(db, answerer.id, "回答被采纳", f"获得 {reward} 经验", f"/questions/{question.entity_id}", "answer")
    return {"ok": True, "awarded_xp": reward}


# ---------------------------------------------------------------- 生存手册
class ArticleCreate(BaseModel):
    category: str = Field(min_length=1, max_length=80)
    title: str = Field(min_length=4, max_length=160)
    body: str = Field(min_length=20, max_length=30000)
    draft: bool = False
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class ArticleUpdate(BaseModel):
    category: str | None = Field(default=None, min_length=1, max_length=80)
    title: str | None = Field(default=None, min_length=4, max_length=160)
    body: str | None = Field(default=None, min_length=20, max_length=30000)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


def article_payload(db: Session, entity: ContentEntity, article: HandbookArticle, viewer: User | None = None) -> dict:
    return {
        "id": entity.id,
        "category": article.category,
        "title": article.title,
        "body": article.body,
        "featured": bool(article.featured_at),
        "author": author_name(db, entity, "nickname"),
        "mine": bool(viewer and viewer.id == entity.owner_id),
        "status": entity.status,
        "favorite_count": db.scalar(select(func.count(Favorite.id)).where(Favorite.entity_id == entity.id)) or 0,
        "attachments": files(db, entity.id),
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
    }


@router.get("/handbook")
def list_handbook(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=50),
    category: str = Query("", max_length=80),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published"]
    if category:
        filters.append(HandbookArticle.category == category)
    total = (
        db.scalar(
            select(func.count(HandbookArticle.entity_id))
            .join(ContentEntity, ContentEntity.id == HandbookArticle.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, HandbookArticle)
        .join(HandbookArticle, HandbookArticle.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(HandbookArticle.featured_at.desc(), ContentEntity.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([article_payload(db, e, a, viewer) for e, a in rows], page_no, page_size, total)


@router.post("/handbook", status_code=201)
def create_article(data: ArticleCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    entity, _ = create_entity(db, user.id, "handbook", f"{data.title}\n{data.body}")
    if data.draft:
        entity.status = "draft"
    article = HandbookArticle(
        entity_id=entity.id,
        category=data.category.strip(),
        title=data.title.strip(),
        body=data.body.strip(),
    )
    db.add(article)
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, entity.id)
    return article_payload(db, entity, article, user)


@router.patch("/handbook/{article_id}")
def update_article(
    article_id: int,
    data: ArticleUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == article_id).with_for_update())
    article = db.get(HandbookArticle, article_id)
    if not entity or not article:
        raise APIError(404, "ARTICLE_NOT_FOUND", "手册文章不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_OWNER", "只有作者可以编辑手册")
    values = data.model_dump(exclude_none=True, exclude={"attachment_ids"})
    if values:
        record_revision(db, entity, user.id, article.title, article.body)
        for key, value in values.items():
            setattr(article, key, value.strip())
        if entity.status != "draft":
            remoderate_entity(db, entity, f"{article.title}\n{article.body}")
    bind_files(db, user, entity.id, data.attachment_ids)
    audit(db, user.id, "handbook.update", "handbook", entity.id)
    touch_entity(db, entity.id)
    return article_payload(db, entity, article, user)


@router.get("/handbook/{article_id}")
def get_article(article_id: int, viewer: User | None = Depends(optional_user), db: Session = Depends(get_db)) -> dict:
    entity = db.get(ContentEntity, article_id)
    article = db.get(HandbookArticle, article_id)
    if not entity or not article or (entity.status != "published" and not (viewer and viewer.id == entity.owner_id)):
        raise APIError(404, "ARTICLE_NOT_FOUND", "文章不存在")
    return article_payload(db, entity, article, viewer)


@router.post("/handbook/{article_id}/publish")
def publish_article(article_id: int, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    entity = db.get(ContentEntity, article_id)
    article = db.get(HandbookArticle, article_id)
    if not entity or not article:
        raise APIError(404, "ARTICLE_NOT_FOUND", "文章不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_OWNER", "只有作者可以发布草稿")
    if entity.status != "draft":
        return article_payload(db, entity, article, user)
    reviewed, reason, _ = moderate_text(f"{article.title}\n{article.body}", db=db)
    if reviewed == "rejected":
        raise APIError(400, "CONTENT_REJECTED", reason)
    entity.status = reviewed
    entity.moderation_reason = reason
    return article_payload(db, entity, article, user)


@router.post("/handbook/{article_id}/feature")
def feature_article(
    article_id: int,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    article = db.get(HandbookArticle, article_id)
    entity = db.get(ContentEntity, article_id)
    if not article or not entity:
        raise APIError(404, "ARTICLE_NOT_FOUND", "文章不存在")
    if not article.featured_at:
        article.featured_at = utcnow()
    if not article.featured_rewarded:
        owner = db.get(User, entity.owner_id)
        owner.xp += 50
        article.featured_rewarded = True
        notify(db, owner.id, "文章被加精", f"《{article.title}》被加精，经验 +50", f"/handbook/{article_id}")
    audit(db, moderator.id, "handbook.feature", "handbook", article_id)
    return {"ok": True}


# ---------------------------------------------------------------- 课程评价
class CourseCreate(BaseModel):
    name: str = Field(min_length=2, max_length=160)
    teacher: str = Field(min_length=1, max_length=100)


class OfferingCreate(BaseModel):
    course_id: int
    semester: str = Field(min_length=2, max_length=30)
    section: str = Field(default="", max_length=60)


class ReviewCreate(BaseModel):
    offering_id: int
    rating: int = Field(ge=1, le=5)
    tags: list[str] = Field(default_factory=list, max_length=8)
    body: str = Field(min_length=5, max_length=5000)
    attachment_ids: list[int] = Field(default_factory=list, max_length=6)


class CorrectionCreate(BaseModel):
    text: str = Field(min_length=2, max_length=3000)


def offering_payload(db: Session, offering: CourseOffering) -> dict:
    course = db.get(Course, offering.course_id)
    rows = db.scalars(
        select(CourseReview)
        .join(ContentEntity, ContentEntity.id == CourseReview.entity_id)
        .where(CourseReview.offering_id == offering.id, ContentEntity.status == "published")
    ).all()
    count = len(rows)
    tag_counts = Counter(tag.strip() for row in rows for tag in row.tags.split(",") if tag.strip())
    return {
        "id": offering.id,
        "course": course.name,
        "teacher": course.teacher,
        "semester": offering.semester,
        "section": offering.section,
        "review_count": count,
        "tags": [tag for tag, _ in tag_counts.most_common(5)],
        "score": round(sum(x.rating for x in rows) / count, 1) if count >= 5 else None,
        "score_hidden_reason": None if count >= 5 else "评价不足 5 条，暂不显示分数",
        "reviews": [
            {
                "id": x.entity_id,
                "rating": x.rating,
                "tags": x.tags.split(",") if x.tags else [],
                "body": x.body,
                "correction": x.correction,
                "attachments": files(db, x.entity_id),
            }
            for x in rows[-10:]
        ],
    }


@router.get("/course-offerings")
def list_offerings(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=100),
    db: Session = Depends(get_db),
) -> dict:
    total = db.scalar(select(func.count(CourseOffering.id))) or 0
    rows = db.scalars(
        select(CourseOffering)
        .order_by(CourseOffering.semester.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([offering_payload(db, x) for x in rows], page_no, page_size, total)


@router.post("/courses", status_code=201)
def create_course(data: CourseCreate, moderator: User = Depends(moderator_user), db: Session = Depends(get_db)) -> dict:
    existing = db.scalar(select(Course).where(Course.name == data.name.strip(), Course.teacher == data.teacher.strip()))
    if existing:
        return {"id": existing.id, "name": existing.name, "teacher": existing.teacher}
    course = Course(name=data.name.strip(), teacher=data.teacher.strip())
    db.add(course)
    db.flush()
    audit(db, moderator.id, "course.create", "course", course.id)
    return {"id": course.id, "name": course.name, "teacher": course.teacher}


@router.post("/course-offerings", status_code=201)
def create_offering(
    data: OfferingCreate,
    moderator: User = Depends(moderator_user),
    db: Session = Depends(get_db),
) -> dict:
    if not db.get(Course, data.course_id):
        raise APIError(404, "COURSE_NOT_FOUND", "课程不存在")
    existing = db.scalar(
        select(CourseOffering).where(
            CourseOffering.course_id == data.course_id,
            CourseOffering.semester == data.semester,
            CourseOffering.section == data.section,
        )
    )
    if existing:
        return offering_payload(db, existing)
    offering = CourseOffering(course_id=data.course_id, semester=data.semester.strip(), section=data.section.strip())
    db.add(offering)
    db.flush()
    audit(db, moderator.id, "course_offering.create", "course_offering", offering.id)
    return offering_payload(db, offering)


@router.post("/course-reviews", status_code=201)
def create_review(data: ReviewCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    require_credit(db, user, "threshold.course_review", "评价课程")
    if not db.scalar(select(CourseOffering).where(CourseOffering.id == data.offering_id).with_for_update()):
        raise APIError(404, "OFFERING_NOT_FOUND", "课程班次不存在")
    if db.scalar(
        select(CourseReview.entity_id).where(
            CourseReview.offering_id == data.offering_id, CourseReview.user_id == user.id
        )
    ):
        raise APIError(409, "REVIEW_EXISTS", "你已经评价过该课程班次")
    entity, _ = create_entity(db, user.id, "course_review", data.body, search_visible=False)
    review = CourseReview(
        entity_id=entity.id,
        offering_id=data.offering_id,
        user_id=user.id,
        rating=data.rating,
        tags=",".join(data.tags),
        body=data.body.strip(),
    )
    db.add(review)
    bind_files(db, user, entity.id, data.attachment_ids)
    return {"id": entity.id, "status": entity.status}


@router.post("/course-reviews/{review_id}/correction")
def correct_review(
    review_id: int,
    data: CorrectionCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    if user.role not in {"moderator", "admin"} and user.campus_identity != "staff":
        raise APIError(403, "STAFF_REQUIRED", "只有教职工或审核员可以提交更正")
    review = db.get(CourseReview, review_id)
    if not review:
        raise APIError(404, "REVIEW_NOT_FOUND", "评价不存在")
    before = review.correction
    review.correction = data.text.strip()
    audit(db, user.id, "course_review.correct", "course_review", review_id, before=before, after=review.correction)
    return {"ok": True}


# ---------------------------------------------------------------- 二手集市（仅线下面交）
class ListingCreate(BaseModel):
    category: str = Field(min_length=1, max_length=60)
    title: str = Field(min_length=3, max_length=160)
    description: str = Field(min_length=5, max_length=10000)
    price: float = Field(ge=0, le=1_000_000)
    condition: str = Field(min_length=1, max_length=80)
    negotiable: bool = True
    purchased_at: date | None = None
    location: str = Field(min_length=2, max_length=120)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class ListingStatusUpdate(BaseModel):
    status: str

    @field_validator("status")
    @classmethod
    def valid_status(cls, value: str) -> str:
        if value not in {"available", "reserved", "sold", "offline"}:
            raise ValueError("商品状态无效")
        return value


class ListingUpdate(BaseModel):
    category: str | None = Field(default=None, min_length=1, max_length=60)
    title: str | None = Field(default=None, min_length=3, max_length=160)
    description: str | None = Field(default=None, min_length=5, max_length=10000)
    price: float | None = Field(default=None, ge=0, le=1_000_000)
    condition: str | None = Field(default=None, min_length=1, max_length=80)
    negotiable: bool | None = None
    purchased_at: date | None = None
    location: str | None = Field(default=None, min_length=2, max_length=120)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


def listing_payload(db: Session, entity: ContentEntity, listing: Listing, viewer: User | None = None) -> dict:
    seller = db.get(User, entity.owner_id)
    completed_sales = db.scalar(
        select(func.count(Listing.entity_id))
        .join(ContentEntity, ContentEntity.id == Listing.entity_id)
        .where(ContentEntity.owner_id == entity.owner_id, Listing.trade_status == "sold")
    ) or 0
    return {
        "id": entity.id,
        "category": listing.category,
        "title": listing.title,
        "description": listing.description,
        "price": listing.price,
        "condition": listing.condition,
        "negotiable": listing.negotiable,
        "purchased_at": listing.purchased_at,
        "location": listing.location,
        "trade_status": listing.trade_status,
        "trade_mode": "offline_only",
        "seller": {
            "id": seller.id,
            "nickname": seller.nickname,
            "credit": seller.credit,
            "verified": bool(seller.verified_at),
            "completed_sales": completed_sales,
        } if seller else None,
        "mine": bool(viewer and entity.owner_id == viewer.id),
        "attachments": files(db, entity.id),
        "favorite_count": db.scalar(select(func.count(Favorite.id)).where(Favorite.entity_id == entity.id)) or 0,
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
    }


@router.get("/listings")
def list_listings(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=50),
    category: str = Query("", max_length=60),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published", Listing.trade_status.in_(["available", "reserved"])]
    if category:
        filters.append(Listing.category == category)
    total = (
        db.scalar(
            select(func.count(Listing.entity_id))
            .join(ContentEntity, ContentEntity.id == Listing.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, Listing)
        .join(Listing, Listing.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([listing_payload(db, e, x, viewer) for e, x in rows], page_no, page_size, total)


@router.post("/listings", status_code=201)
def create_listing(data: ListingCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    require_credit(db, user, "threshold.listing_publish", "发布商品")
    entity, _ = create_entity(db, user.id, "listing", f"{data.title}\n{data.description}")
    listing = Listing(
        entity_id=entity.id,
        category=data.category.strip(),
        title=data.title.strip(),
        description=data.description.strip(),
        price=data.price,
        condition=data.condition.strip(),
        negotiable=data.negotiable,
        purchased_at=data.purchased_at,
        location=data.location.strip(),
    )
    db.add(listing)
    bind_files(db, user, entity.id, data.attachment_ids)
    return listing_payload(db, entity, listing, user)


@router.patch("/listings/{listing_id}/status")
def update_listing_status(
    listing_id: int,
    data: ListingStatusUpdate,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == listing_id).with_for_update())
    listing = db.get(Listing, listing_id)
    if not entity or not listing:
        raise APIError(404, "LISTING_NOT_FOUND", "商品不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "NOT_SELLER", "只有卖家可以修改商品状态")
    listing.trade_status = data.status
    if data.status == "offline":
        entity.status = "hidden"
    audit(db, user.id, "listing.status", "listing", listing_id, after={"status": data.status})
    touch_entity(db, entity.id)
    return listing_payload(db, entity, listing, user)


@router.patch("/listings/{listing_id}")
def update_listing(
    listing_id: int,
    data: ListingUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == listing_id).with_for_update())
    listing = db.get(Listing, listing_id)
    if not entity or not listing:
        raise APIError(404, "LISTING_NOT_FOUND", "商品不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_SELLER", "只有卖家可以修改商品")
    values = data.model_dump(exclude_none=True, exclude={"attachment_ids"})
    if values:
        record_revision(db, entity, user.id, listing.title, listing.description)
    for key, value in values.items():
        setattr(listing, key, value.strip() if isinstance(value, str) else value)
    remoderate_entity(db, entity, f"{listing.title}\n{listing.description}")
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, entity.id)
    audit(db, user.id, "listing.update", "listing", listing_id)
    return listing_payload(db, entity, listing, user)


# ---------------------------------------------------------------- 活动
class ActivityCreate(BaseModel):
    category: str = Field(min_length=1, max_length=60)
    title: str = Field(min_length=4, max_length=160)
    body: str = Field(default="", max_length=10000)
    location: str = Field(min_length=2, max_length=160)
    starts_at: datetime
    ends_at: datetime | None = None
    capacity: int | None = Field(default=None, ge=2, le=10000)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


class ActivityUpdate(BaseModel):
    category: str | None = Field(default=None, min_length=1, max_length=60)
    title: str | None = Field(default=None, min_length=4, max_length=160)
    body: str | None = Field(default=None, max_length=10000)
    location: str | None = Field(default=None, min_length=2, max_length=160)
    starts_at: datetime | None = None
    ends_at: datetime | None = None
    capacity: int | None = Field(default=None, ge=2, le=10000)
    attachment_ids: list[int] = Field(default_factory=list, max_length=9)


def activity_payload(db: Session, entity: ContentEntity, activity: Activity, viewer: User | None = None) -> dict:
    joined = db.scalar(
        select(ActivityMember.id).where(
            ActivityMember.activity_id == entity.id,
            ActivityMember.user_id == (viewer.id if viewer else -1),
            ActivityMember.status == "joined",
        )
    )
    count = (
        db.scalar(
            select(func.count(ActivityMember.id)).where(
                ActivityMember.activity_id == entity.id, ActivityMember.status == "joined"
            )
        )
        or 0
    )
    return {
        "id": entity.id,
        "category": activity.category,
        "title": activity.title,
        "body": activity.body,
        "location": activity.location,
        "starts_at": activity.starts_at,
        "ends_at": activity.ends_at,
        "capacity": activity.capacity,
        "status": activity.status,
        "member_count": count,
        "joined": bool(joined),
        "mine": bool(viewer and viewer.id == entity.owner_id),
        "author": author_name(db, entity, "nickname"),
        "attachments": files(db, entity.id),
        "created_at": entity.created_at,
        "updated_at": entity.updated_at,
    }


@router.get("/activities")
def list_activities(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=100),
    category: str = Query("", max_length=60),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published", Activity.status == "open"]
    if category:
        filters.append(Activity.category == category)
    total = (
        db.scalar(
            select(func.count(Activity.entity_id))
            .join(ContentEntity, ContentEntity.id == Activity.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, Activity)
        .join(Activity, Activity.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(Activity.starts_at.asc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([activity_payload(db, e, a, viewer) for e, a in rows], page_no, page_size, total)


@router.post("/activities", status_code=201)
def create_activity(data: ActivityCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    starts_at = db_datetime(data.starts_at)
    ends_at = db_datetime(data.ends_at) if data.ends_at else None
    if starts_at <= utcnow():
        raise APIError(400, "INVALID_START_TIME", "活动开始时间必须晚于当前时间")
    if ends_at and ends_at <= starts_at:
        raise APIError(400, "INVALID_END_TIME", "活动结束时间必须晚于开始时间")
    entity, _ = create_entity(db, user.id, "activity", f"{data.title}\n{data.body}")
    values = data.model_dump(exclude={"attachment_ids"})
    values["starts_at"] = starts_at
    values["ends_at"] = ends_at
    activity = Activity(entity_id=entity.id, **values)
    db.add(activity)
    db.flush()
    bind_files(db, user, entity.id, data.attachment_ids)
    db.add(ActivityMember(activity_id=entity.id, user_id=user.id))
    return activity_payload(db, entity, activity, user)


@router.patch("/activities/{activity_id}")
def update_activity(
    activity_id: int,
    data: ActivityUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == activity_id).with_for_update())
    activity = db.get(Activity, activity_id)
    if not entity or not activity:
        raise APIError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_OWNER", "只有发起人可以编辑活动")
    if activity.status != "open" or activity.starts_at <= utcnow():
        raise APIError(409, "ACTIVITY_NOT_EDITABLE", "活动已开始或结束，不能再编辑")
    values = data.model_dump(exclude_none=True, exclude={"attachment_ids"})
    if "starts_at" in values:
        values["starts_at"] = db_datetime(values["starts_at"])
    if "ends_at" in values:
        values["ends_at"] = db_datetime(values["ends_at"])
    if not values:
        return activity_payload(db, entity, activity, user)
    starts_at = values.get("starts_at", activity.starts_at)
    ends_at = values.get("ends_at", activity.ends_at)
    if starts_at <= utcnow() or (ends_at and ends_at <= starts_at):
        raise APIError(400, "INVALID_ACTIVITY_TIME", "活动时间无效")
    current_count = (
        db.scalar(
            select(func.count(ActivityMember.id)).where(
                ActivityMember.activity_id == activity_id, ActivityMember.status == "joined"
            )
        )
        or 0
    )
    if values.get("capacity") is not None and values["capacity"] < current_count:
        raise APIError(400, "CAPACITY_TOO_SMALL", "容量不能小于当前报名人数")
    record_revision(db, entity, user.id, activity.title, activity.body)
    for key, value in values.items():
        setattr(activity, key, value.strip() if isinstance(value, str) else value)
    remoderate_entity(db, entity, f"{activity.title}\n{activity.body}")
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, entity.id)
    audit(db, user.id, "activity.update", "activity", entity.id)
    return activity_payload(db, entity, activity, user)


@router.put("/activities/{activity_id}/membership")
def join_activity(activity_id: int, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    activity = db.scalar(select(Activity).where(Activity.entity_id == activity_id).with_for_update())
    entity = db.get(ContentEntity, activity_id)
    if not activity or not entity or entity.status != "published" or activity.status != "open":
        raise APIError(404, "ACTIVITY_NOT_FOUND", "活动不存在或已关闭")
    row = db.scalar(
        select(ActivityMember).where(ActivityMember.activity_id == activity_id, ActivityMember.user_id == user.id)
    )
    if row and row.status == "joined":
        return activity_payload(db, entity, activity, user)
    count = (
        db.scalar(
            select(func.count(ActivityMember.id)).where(
                ActivityMember.activity_id == activity_id, ActivityMember.status == "joined"
            )
        )
        or 0
    )
    if activity.capacity and count >= activity.capacity:
        raise APIError(409, "ACTIVITY_FULL", "活动名额已满")
    if row:
        row.status = "joined"
        row.joined_at = utcnow()
    else:
        db.add(ActivityMember(activity_id=activity_id, user_id=user.id))
    if entity.owner_id != user.id:
        notify(
            db,
            entity.owner_id,
            "有人加入活动",
            f"{user.nickname} 加入了《{activity.title}》",
            f"/activities/{activity_id}",
        )
    touch_entity(db, activity_id)
    return activity_payload(db, entity, activity, user)


@router.delete("/activities/{activity_id}/membership")
def leave_activity(activity_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    entity = db.get(ContentEntity, activity_id)
    if entity and entity.owner_id == user.id:
        raise APIError(400, "OWNER_CANNOT_LEAVE", "发起人需取消活动")
    row = db.scalar(
        select(ActivityMember).where(ActivityMember.activity_id == activity_id, ActivityMember.user_id == user.id)
    )
    if row:
        row.status = "left"
    touch_entity(db, activity_id)
    return {"ok": True}


@router.post("/activities/{activity_id}/cancel")
def cancel_activity(activity_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    entity = db.get(ContentEntity, activity_id)
    activity = db.get(Activity, activity_id)
    if not entity or not activity:
        raise APIError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "NOT_OWNER", "无权取消活动")
    activity.status = "cancelled"
    entity.status = "hidden"
    members = db.scalars(
        select(ActivityMember).where(ActivityMember.activity_id == activity_id, ActivityMember.status == "joined")
    ).all()
    for member in members:
        notify(db, member.user_id, "活动已取消", f"《{activity.title}》已取消", "/activities")
        member.status = "cancelled"
    return {"ok": True}


# ---------------------------------------------------------------- 失物招领
class LostCreate(BaseModel):
    kind: str
    item_name: str = Field(min_length=2, max_length=160)
    description: str = Field(default="", max_length=5000)
    location: str = Field(min_length=2, max_length=160)
    happened_at: datetime | None = None
    attachment_ids: list[int] = Field(default_factory=list, max_length=6)

    @field_validator("kind")
    @classmethod
    def valid_kind(cls, value: str) -> str:
        if value not in {"lost", "found"}:
            raise ValueError("类型必须为 lost 或 found")
        return value


class LostUpdate(BaseModel):
    item_name: str | None = Field(default=None, min_length=2, max_length=160)
    description: str | None = Field(default=None, max_length=5000)
    location: str | None = Field(default=None, min_length=2, max_length=160)
    happened_at: datetime | None = None
    attachment_ids: list[int] = Field(default_factory=list, max_length=6)


class ClaimCreate(BaseModel):
    message: str = Field(min_length=5, max_length=2000)


class ClaimDecision(BaseModel):
    approve: bool


def lost_payload(db: Session, entity: ContentEntity, item: LostItem, viewer: User | None = None) -> dict:
    claims = db.scalar(select(func.count(LostClaim.id)).where(LostClaim.item_id == entity.id)) or 0
    return {
        "id": entity.id,
        "kind": item.kind,
        "item_name": item.item_name,
        "description": item.description,
        "location": item.location,
        "happened_at": item.happened_at,
        "status": item.status,
        "claim_count": claims,
        "mine": bool(viewer and viewer.id == entity.owner_id),
        "author": author_name(db, entity, "nickname"),
        "attachments": files(db, entity.id),
    }


@router.get("/lost-items")
def list_lost_items(
    page_no: int = Query(1, alias="page", ge=1),
    page_size: int = Query(20, ge=1, le=100),
    kind: str = Query("", max_length=20),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published"]
    if kind:
        filters.append(LostItem.kind == kind)
    total = (
        db.scalar(
            select(func.count(LostItem.entity_id))
            .join(ContentEntity, ContentEntity.id == LostItem.entity_id)
            .where(*filters)
        )
        or 0
    )
    rows = db.execute(
        select(ContentEntity, LostItem)
        .join(LostItem, LostItem.entity_id == ContentEntity.id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page_no - 1) * page_size)
        .limit(page_size)
    ).all()
    return page([lost_payload(db, e, item, viewer) for e, item in rows], page_no, page_size, total)


@router.post("/lost-items", status_code=201)
def create_lost_item(data: LostCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    entity, _ = create_entity(db, user.id, "lost_item", f"{data.item_name}\n{data.description}")
    values = data.model_dump(exclude={"attachment_ids"})
    if values["happened_at"]:
        values["happened_at"] = db_datetime(values["happened_at"])
    item = LostItem(entity_id=entity.id, **values)
    db.add(item)
    bind_files(db, user, entity.id, data.attachment_ids)
    return lost_payload(db, entity, item, user)


@router.patch("/lost-items/{item_id}")
def update_lost_item(
    item_id: int,
    data: LostUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.scalar(select(ContentEntity).where(ContentEntity.id == item_id).with_for_update())
    item = db.get(LostItem, item_id)
    if not entity or not item:
        raise APIError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
    if entity.owner_id != user.id:
        raise APIError(403, "NOT_OWNER", "只有发布者可以编辑失物信息")
    if item.status != "open":
        raise APIError(409, "LOST_ITEM_NOT_EDITABLE", "认领流程已结束，不能再编辑")
    values = data.model_dump(exclude_none=True, exclude={"attachment_ids"})
    if "happened_at" in values:
        values["happened_at"] = db_datetime(values["happened_at"])
    if values:
        record_revision(db, entity, user.id, item.item_name, item.description)
        for key, value in values.items():
            setattr(item, key, value.strip() if isinstance(value, str) else value)
        remoderate_entity(db, entity, f"{item.item_name}\n{item.description}")
    bind_files(db, user, entity.id, data.attachment_ids)
    touch_entity(db, entity.id)
    audit(db, user.id, "lost_item.update", "lost_item", entity.id)
    return lost_payload(db, entity, item, user)


@router.post("/lost-items/{item_id}/claims", status_code=201)
def create_claim(
    item_id: int,
    data: ClaimCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, item_id)
    item = db.scalar(select(LostItem).where(LostItem.entity_id == item_id).with_for_update())
    if not entity or not item or entity.status != "published" or item.status != "open":
        raise APIError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在或已结束")
    if entity.owner_id == user.id:
        raise APIError(400, "SELF_CLAIM", "不能认领自己发布的条目")
    existing = db.scalar(select(LostClaim).where(LostClaim.item_id == item_id, LostClaim.claimant_id == user.id))
    if existing:
        return {"id": existing.id, "status": existing.status}
    claim = LostClaim(item_id=item_id, claimant_id=user.id, message=data.message.strip())
    db.add(claim)
    db.flush()
    notify(
        db,
        entity.owner_id,
        "收到认领申请",
        f"{user.nickname} 提交了《{item.item_name}》的认领申请",
        f"/lost-items/{item_id}",
    )
    return {"id": claim.id, "status": claim.status}


@router.get("/lost-items/{item_id}/claims")
def list_claims(
    item_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, item_id)
    item = db.get(LostItem, item_id)
    if not entity or not item:
        raise APIError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "OWNER_REQUIRED", "只有发布者可以查看认领申请")
    rows = db.scalars(select(LostClaim).where(LostClaim.item_id == item_id).order_by(LostClaim.created_at)).all()
    return page(
        [
            {
                "id": row.id,
                "claimant_id": row.claimant_id,
                "claimant": db.get(User, row.claimant_id).nickname,
                "message": row.message,
                "status": row.status,
                "created_at": row.created_at,
            }
            for row in rows
        ],
        1,
        100,
        len(rows),
    )


@router.post("/lost-items/{item_id}/claims/{claim_id}/decision")
def decide_claim(
    item_id: int,
    claim_id: int,
    data: ClaimDecision,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    entity = db.get(ContentEntity, item_id)
    item = db.scalar(select(LostItem).where(LostItem.entity_id == item_id).with_for_update())
    claim = db.get(LostClaim, claim_id)
    if not entity or not item or not claim or claim.item_id != item_id:
        raise APIError(404, "CLAIM_NOT_FOUND", "认领申请不存在")
    if entity.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "OWNER_REQUIRED", "只有发布者可以确认认领")
    if claim.status != "pending":
        return {"id": claim.id, "status": claim.status}
    claim.status = "approved" if data.approve else "rejected"
    claim.decided_at = utcnow()
    if data.approve:
        item.status = "completed"
        db.query(LostClaim).filter(LostClaim.item_id == item_id, LostClaim.id != claim.id).update(
            {LostClaim.status: "rejected"}, synchronize_session=False
        )
        if item.kind == "found":
            finder = db.get(User, entity.owner_id)
            if finder:
                apply_credit_rule(
                    db,
                    finder,
                    "reward.lost_claim",
                    actor_id=user.id,
                    target_type="lost_item",
                    target_id=item_id,
                )
        notify(db, claim.claimant_id, "认领已确认", f"《{item.item_name}》认领流程已完成", f"/lost-items/{item_id}")
    else:
        notify(
            db,
            claim.claimant_id,
            "认领申请未通过",
            f"《{item.item_name}》的发布者未确认该申请",
            f"/lost-items/{item_id}",
        )
    return {"id": claim.id, "status": claim.status}
