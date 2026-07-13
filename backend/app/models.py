from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import (
    BigInteger as SQLBigInteger,
)
from sqlalchemy import (
    Boolean,
    CheckConstraint,
    DateTime,
    Float,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
)
from sqlalchemy.orm import Mapped, mapped_column

from .config import get_settings
from .database import Base

# PostgreSQL 使用 BIGINT；测试/本地 SQLite 必须退化为 INTEGER 才能自动生成主键。
BigInteger = SQLBigInteger().with_variant(Integer, "sqlite")


def utcnow() -> datetime:
    value = datetime.now(UTC)
    # SQLite 不保留时区信息；本地开发和测试统一使用 naive UTC，生产 PostgreSQL 使用 aware UTC。
    return value.replace(tzinfo=None) if get_settings().database_url.startswith("sqlite") else value


def db_datetime(value: datetime) -> datetime:
    """将客户端时间规范化为当前数据库使用的 UTC 形式。"""
    if value.tzinfo is not None:
        value = value.astimezone(UTC)
    elif not get_settings().database_url.startswith("sqlite"):
        value = value.replace(tzinfo=UTC)
    if get_settings().database_url.startswith("sqlite") and value.tzinfo is not None:
        value = value.replace(tzinfo=None)
    return value


class User(Base):
    __tablename__ = "users"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    email: Mapped[str | None] = mapped_column(String(320), unique=True, index=True)
    password_hash: Mapped[str] = mapped_column(String(255))
    nickname: Mapped[str] = mapped_column(String(40), index=True)
    alias: Mapped[str] = mapped_column(String(40), unique=True)
    campus_identity: Mapped[str] = mapped_column(String(20), default="student")
    role: Mapped[str] = mapped_column(String(20), default="user", index=True)
    status: Mapped[str] = mapped_column(String(20), default="active", index=True)
    credit: Mapped[int] = mapped_column(Integer, default=80)
    xp: Mapped[int] = mapped_column(Integer, default=0)
    avatar_path: Mapped[str | None] = mapped_column(String(500))
    dm_stranger_off: Mapped[bool] = mapped_column(Boolean, default=False)
    hide_online: Mapped[bool] = mapped_column(Boolean, default=False)
    verified_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    deactivated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, onupdate=utcnow)
    __table_args__ = (CheckConstraint("credit >= 0 AND credit <= 100", name="ck_user_credit"),)


class SessionRecord(Base):
    __tablename__ = "sessions"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    token_hash: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    csrf_token: Mapped[str] = mapped_column(String(64))
    ip_address: Mapped[str] = mapped_column(String(64), default="")
    user_agent: Mapped[str] = mapped_column(String(500), default="")
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), index=True)
    absolute_expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True))
    last_seen_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    revoked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class VerificationCode(Base):
    __tablename__ = "verification_codes"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    email: Mapped[str] = mapped_column(String(320), index=True)
    purpose: Mapped[str] = mapped_column(String(30), index=True)
    code_hash: Mapped[str] = mapped_column(String(64))
    ip_address: Mapped[str] = mapped_column(String(64), default="")
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), index=True)
    consumed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class RateLimitEvent(Base):
    __tablename__ = "rate_limit_events"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    action: Mapped[str] = mapped_column(String(40), index=True)
    subject: Mapped[str] = mapped_column(String(320), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, index=True)


class EmailOutbox(Base):
    __tablename__ = "email_outbox"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    to_email: Mapped[str] = mapped_column(String(320), index=True)
    subject: Mapped[str] = mapped_column(String(200))
    body: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    attempts: Mapped[int] = mapped_column(Integer, default=0)
    next_attempt_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    sent_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    last_error: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ContentEntity(Base):
    __tablename__ = "content_entities"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    type: Mapped[str] = mapped_column(String(30), index=True)
    owner_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"), index=True)
    status: Mapped[str] = mapped_column(String(20), default="published", index=True)
    allow_comments: Mapped[bool] = mapped_column(Boolean, default=True)
    search_visible: Mapped[bool] = mapped_column(Boolean, default=True)
    moderation_reason: Mapped[str] = mapped_column(Text, default="")
    revision: Mapped[int] = mapped_column(Integer, default=1)
    deleted_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, index=True)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, onupdate=utcnow)


class ContentRevision(Base):
    __tablename__ = "content_revisions"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    editor_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"), index=True)
    revision: Mapped[int] = mapped_column(Integer)
    title: Mapped[str] = mapped_column(String(160), default="")
    body: Mapped[str] = mapped_column(Text)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("entity_id", "revision", name="uq_content_revision"),)


class Attachment(Base):
    __tablename__ = "attachments"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    owner_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    entity_id: Mapped[int | None] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    path: Mapped[str] = mapped_column(String(500), unique=True)
    thumbnail_path: Mapped[str] = mapped_column(String(500), default="")
    mime_type: Mapped[str] = mapped_column(String(100))
    size_bytes: Mapped[int] = mapped_column(Integer)
    width: Mapped[int] = mapped_column(Integer)
    height: Mapped[int] = mapped_column(Integer)
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Post(Base):
    __tablename__ = "posts"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    board: Mapped[str] = mapped_column(String(30), default="treehole", index=True)
    title: Mapped[str] = mapped_column(String(120), default="")
    body: Mapped[str] = mapped_column(Text)
    identity_mode: Mapped[str] = mapped_column(String(20), default="anonymous")
    expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), index=True)
    views: Mapped[int] = mapped_column(Integer, default=0)
    __table_args__ = (Index("ix_posts_search", "title", "body"),)


class Comment(Base):
    __tablename__ = "comments"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    target_entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    parent_id: Mapped[int | None] = mapped_column(ForeignKey("comments.entity_id", ondelete="CASCADE"), index=True)
    reply_to_user_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    body: Mapped[str] = mapped_column(Text)
    identity_mode: Mapped[str] = mapped_column(String(20), default="nickname")


class Reaction(Base):
    __tablename__ = "reactions"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    type: Mapped[str] = mapped_column(String(20), default="like")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("entity_id", "user_id", "type", name="uq_reaction"),)


class Favorite(Base):
    __tablename__ = "favorites"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("entity_id", "user_id", name="uq_favorite"),)


class Report(Base):
    __tablename__ = "reports"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), index=True)
    reporter_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"), index=True)
    reason: Mapped[str] = mapped_column(String(80))
    detail: Mapped[str] = mapped_column(Text, default="")
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("entity_id", "reporter_id", name="uq_report"),)


class ModerationCase(Base):
    __tablename__ = "moderation_cases"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), unique=True)
    source: Mapped[str] = mapped_column(String(30), default="risk_rule")
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    assignee_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    decision: Mapped[str] = mapped_column(String(30), default="")
    notes: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    decided_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))


class Notification(Base):
    __tablename__ = "notifications"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    type: Mapped[str] = mapped_column(String(30), default="system")
    title: Mapped[str] = mapped_column(String(120))
    body: Mapped[str] = mapped_column(Text)
    link: Mapped[str] = mapped_column(String(500), default="")
    read_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, index=True)


class AuditLog(Base):
    __tablename__ = "audit_logs"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    actor_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), index=True)
    action: Mapped[str] = mapped_column(String(80), index=True)
    target_type: Mapped[str] = mapped_column(String(40))
    target_id: Mapped[str] = mapped_column(String(80))
    reason: Mapped[str] = mapped_column(Text, default="")
    before_json: Mapped[str] = mapped_column(Text, default="")
    after_json: Mapped[str] = mapped_column(Text, default="")
    request_id: Mapped[str] = mapped_column(String(64), default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, index=True)


class Team(Base):
    __tablename__ = "teams"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    owner_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"), index=True)
    game: Mapped[str] = mapped_column(String(60), index=True)
    mode: Mapped[str] = mapped_column(String(80))
    rank_requirement: Mapped[str] = mapped_column(String(80), default="不限")
    capacity: Mapped[int] = mapped_column(Integer, default=5)
    voice_name: Mapped[str] = mapped_column(String(80), default="")
    voice_link: Mapped[str] = mapped_column(String(500), default="")
    notes: Mapped[str] = mapped_column(Text, default="")
    recurrence: Mapped[str] = mapped_column(String(20), default="once")
    reminder_minutes: Mapped[int] = mapped_column(Integer, default=30)
    status: Mapped[str] = mapped_column(String(20), default="active", index=True)
    __table_args__ = (CheckConstraint("capacity >= 2 AND capacity <= 99", name="ck_team_capacity"),)


class TeamRun(Base):
    __tablename__ = "team_runs"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    team_id: Mapped[int] = mapped_column(ForeignKey("teams.entity_id", ondelete="CASCADE"), index=True)
    starts_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), index=True)
    status: Mapped[str] = mapped_column(String(20), default="scheduled", index=True)
    reminder_sent_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("team_id", "starts_at", name="uq_team_run_start"),)


class TeamMembership(Base):
    __tablename__ = "team_memberships"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    team_id: Mapped[int] = mapped_column(ForeignKey("teams.entity_id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    role: Mapped[str] = mapped_column(String(20), default="member")
    status: Mapped[str] = mapped_column(String(20), default="active", index=True)
    joined_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    left_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))


class TeamRunMember(Base):
    __tablename__ = "team_run_members"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    run_id: Mapped[int] = mapped_column(ForeignKey("team_runs.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    status: Mapped[str] = mapped_column(String(20), default="joined")
    checked_in_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    excused_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    credit_awarded: Mapped[bool] = mapped_column(Boolean, default=False)
    __table_args__ = (UniqueConstraint("run_id", "user_id", name="uq_team_run_member"),)


class TeamRating(Base):
    __tablename__ = "team_ratings"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    run_id: Mapped[int] = mapped_column(ForeignKey("team_runs.id", ondelete="CASCADE"), index=True)
    rater_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    target_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    tag: Mapped[str] = mapped_column(String(30))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("run_id", "rater_id", "target_id", "tag", name="uq_team_rating"),)


class Question(Base):
    __tablename__ = "questions"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    title: Mapped[str] = mapped_column(String(160), index=True)
    body: Mapped[str] = mapped_column(Text)
    category: Mapped[str] = mapped_column(String(60), default="其他", index=True)
    tags: Mapped[str] = mapped_column(String(300), default="")
    bounty_xp: Mapped[int] = mapped_column(Integer, default=0)
    bounty_settled: Mapped[bool] = mapped_column(Boolean, default=False)
    accepted_answer_id: Mapped[int | None] = mapped_column(BigInteger)


class Answer(Base):
    __tablename__ = "answers"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    question_id: Mapped[int] = mapped_column(ForeignKey("questions.entity_id", ondelete="CASCADE"), index=True)
    body: Mapped[str] = mapped_column(Text)


class HandbookArticle(Base):
    __tablename__ = "handbook_articles"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    category: Mapped[str] = mapped_column(String(80), index=True)
    title: Mapped[str] = mapped_column(String(160), index=True)
    body: Mapped[str] = mapped_column(Text)
    featured_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    featured_rewarded: Mapped[bool] = mapped_column(Boolean, default=False)


class Course(Base):
    __tablename__ = "courses"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    name: Mapped[str] = mapped_column(String(160), index=True)
    teacher: Mapped[str] = mapped_column(String(100), index=True)
    active: Mapped[bool] = mapped_column(Boolean, default=True)
    __table_args__ = (UniqueConstraint("name", "teacher", name="uq_course_teacher"),)


class CourseOffering(Base):
    __tablename__ = "course_offerings"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    course_id: Mapped[int] = mapped_column(ForeignKey("courses.id", ondelete="CASCADE"), index=True)
    semester: Mapped[str] = mapped_column(String(30), index=True)
    section: Mapped[str] = mapped_column(String(60), default="")
    __table_args__ = (UniqueConstraint("course_id", "semester", "section", name="uq_offering"),)


class CourseReview(Base):
    __tablename__ = "course_reviews"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    offering_id: Mapped[int] = mapped_column(ForeignKey("course_offerings.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    rating: Mapped[int] = mapped_column(Integer)
    tags: Mapped[str] = mapped_column(String(300), default="")
    body: Mapped[str] = mapped_column(Text)
    correction: Mapped[str] = mapped_column(Text, default="")
    __table_args__ = (
        CheckConstraint("rating >= 1 AND rating <= 5", name="ck_review_rating"),
        UniqueConstraint("offering_id", "user_id", name="uq_course_review_user"),
    )


class Listing(Base):
    __tablename__ = "listings"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    category: Mapped[str] = mapped_column(String(60), index=True)
    title: Mapped[str] = mapped_column(String(160), index=True)
    description: Mapped[str] = mapped_column(Text)
    price: Mapped[float] = mapped_column(Float)
    condition: Mapped[str] = mapped_column(String(80))
    location: Mapped[str] = mapped_column(String(120))
    trade_status: Mapped[str] = mapped_column(String(20), default="available", index=True)
    __table_args__ = (CheckConstraint("price >= 0", name="ck_listing_price"),)


class Activity(Base):
    __tablename__ = "activities"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    category: Mapped[str] = mapped_column(String(60), index=True)
    title: Mapped[str] = mapped_column(String(160), index=True)
    body: Mapped[str] = mapped_column(Text, default="")
    location: Mapped[str] = mapped_column(String(160))
    starts_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), index=True)
    ends_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    capacity: Mapped[int | None] = mapped_column(Integer)
    status: Mapped[str] = mapped_column(String(20), default="open", index=True)


class ActivityMember(Base):
    __tablename__ = "activity_members"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    activity_id: Mapped[int] = mapped_column(ForeignKey("activities.entity_id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    status: Mapped[str] = mapped_column(String(20), default="joined")
    joined_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("activity_id", "user_id", name="uq_activity_member"),)


class LostItem(Base):
    __tablename__ = "lost_items"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    kind: Mapped[str] = mapped_column(String(20))
    item_name: Mapped[str] = mapped_column(String(160), index=True)
    description: Mapped[str] = mapped_column(Text, default="")
    location: Mapped[str] = mapped_column(String(160))
    happened_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    status: Mapped[str] = mapped_column(String(20), default="open", index=True)


class LostClaim(Base):
    __tablename__ = "lost_claims"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    item_id: Mapped[int] = mapped_column(ForeignKey("lost_items.entity_id", ondelete="CASCADE"), index=True)
    claimant_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    message: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    decided_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    __table_args__ = (UniqueConstraint("item_id", "claimant_id", name="uq_lost_claim"),)


class ObservePost(Base):
    __tablename__ = "observe_posts"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    title: Mapped[str] = mapped_column(String(160))
    body_masked: Mapped[str] = mapped_column(Text)
    body_raw: Mapped[str] = mapped_column(Text)
    respondent_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), index=True)
    response: Mapped[str] = mapped_column(Text, default="")
    response_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    admin_note: Mapped[str] = mapped_column(Text, default="")


class Penalty(Base):
    __tablename__ = "penalties"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"), index=True)
    public_mask: Mapped[str] = mapped_column(String(60))
    violation_type: Mapped[str] = mapped_column(String(120))
    result: Mapped[str] = mapped_column(Text)
    rule: Mapped[str] = mapped_column(String(160))
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Appeal(Base):
    __tablename__ = "appeals"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    penalty_id: Mapped[int] = mapped_column(ForeignKey("penalties.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    reason: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    admin_note: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("penalty_id", "user_id", name="uq_appeal"),)


class Conversation(Base):
    __tablename__ = "conversations"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    context_type: Mapped[str] = mapped_column(String(30), default="direct")
    context_id: Mapped[int | None] = mapped_column(BigInteger)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ConversationMember(Base):
    __tablename__ = "conversation_members"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    conversation_id: Mapped[int] = mapped_column(ForeignKey("conversations.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    last_read_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
    __table_args__ = (UniqueConstraint("conversation_id", "user_id", name="uq_conversation_member"),)


class Message(Base):
    __tablename__ = "messages"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    conversation_id: Mapped[int] = mapped_column(ForeignKey("conversations.id", ondelete="CASCADE"), index=True)
    body: Mapped[str] = mapped_column(Text)


class Block(Base):
    __tablename__ = "blocks"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    blocked_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("user_id", "blocked_id", name="uq_block"),)


class Announcement(Base):
    __tablename__ = "announcements"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    title: Mapped[str] = mapped_column(String(160))
    body: Mapped[str] = mapped_column(Text)
    level: Mapped[str] = mapped_column(String(20), default="normal")
    audience: Mapped[str] = mapped_column(String(30), default="all")
    published_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, index=True)


class AnnouncementRead(Base):
    __tablename__ = "announcement_reads"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    announcement_id: Mapped[int] = mapped_column(ForeignKey("announcements.id", ondelete="CASCADE"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    read_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    __table_args__ = (UniqueConstraint("announcement_id", "user_id", name="uq_announcement_read"),)


class Feedback(Base):
    __tablename__ = "feedback"
    entity_id: Mapped[int] = mapped_column(ForeignKey("content_entities.id", ondelete="CASCADE"), primary_key=True)
    type: Mapped[str] = mapped_column(String(30), default="suggestion")
    title: Mapped[str] = mapped_column(String(160))
    body: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(20), default="pending")
    admin_note: Mapped[str] = mapped_column(Text, default="")


class Setting(Base):
    __tablename__ = "settings"
    key: Mapped[str] = mapped_column(String(80), primary_key=True)
    value: Mapped[str] = mapped_column(Text)
    updated_by: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, onupdate=utcnow)


class BackupJob(Base):
    __tablename__ = "backup_jobs"
    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    requested_by: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="RESTRICT"))
    status: Mapped[str] = mapped_column(String(20), default="pending", index=True)
    file_path: Mapped[str] = mapped_column(String(500), default="")
    download_token: Mapped[str] = mapped_column(String(100), unique=True)
    error: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True))
