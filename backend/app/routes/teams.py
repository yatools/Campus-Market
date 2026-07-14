from __future__ import annotations

from datetime import datetime, timedelta

from fastapi import APIRouter, Depends, Query, Response
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..credit import apply_credit_rule, require_credit
from ..database import get_db
from ..deps import current_user, optional_user, participating_user
from ..errors import APIError
from ..models import (
    ContentEntity,
    Team,
    TeamGame,
    TeamGameAlias,
    TeamMembership,
    TeamRating,
    TeamRun,
    TeamRunMember,
    User,
    db_datetime,
    utcnow,
)
from ..services import audit, create_entity, insert_unique, notify, touch_entity
from ..team_lifecycle import advance_team_lifecycles, run_expires_at
from .games import ensure_game_catalog, normalize_game

router = APIRouter(prefix="/teams", tags=["车队"])
RATING_TAGS = {"friendly", "communication", "skill", "punctual"}
REMINDER_CHANNELS = {"email", "in_app", "calendar"}


class TeamCreate(BaseModel):
    game_id: int | None = None
    game: str = Field(default="", max_length=60)
    mode: str = Field(min_length=1, max_length=80)
    rank_requirement: str = Field(default="不限", max_length=80)
    capacity: int = Field(default=5, ge=2, le=99)
    starts_at: datetime
    recurrence: str = "once"
    voice_name: str = Field(default="", max_length=80)
    voice_link: str = Field(default="", max_length=500)
    notes: str = Field(default="", max_length=2000)
    newbie_level: str = Field(default="欢迎新手", max_length=80)
    vibe: str = Field(default="", max_length=160)
    reminder_channels: list[str] = Field(default_factory=lambda: ["email", "in_app"], max_length=3)
    reminder_minutes: int = Field(default=30, ge=5, le=1440)
    post_departure_retention_minutes: int = Field(default=120, ge=60, le=480)

    @field_validator("recurrence")
    @classmethod
    def recurrence_valid(cls, value: str) -> str:
        if value not in {"once", "weekly"}:
            raise ValueError("仅支持单次或每周车队")
        return value

    @field_validator("reminder_channels")
    @classmethod
    def channels_valid(cls, values: list[str]) -> list[str]:
        values = list(dict.fromkeys(values))
        if not values or any(value not in REMINDER_CHANNELS for value in values):
            raise ValueError("提醒渠道仅支持邮件、站内和日历")
        return values


class TeamUpdate(BaseModel):
    game_id: int | None = None
    mode: str | None = Field(default=None, min_length=1, max_length=80)
    rank_requirement: str | None = Field(default=None, max_length=80)
    capacity: int | None = Field(default=None, ge=2, le=99)
    voice_name: str | None = Field(default=None, max_length=80)
    voice_link: str | None = Field(default=None, max_length=500)
    notes: str | None = Field(default=None, max_length=2000)
    newbie_level: str | None = Field(default=None, max_length=80)
    vibe: str | None = Field(default=None, max_length=160)
    reminder_channels: list[str] | None = Field(default=None, max_length=3)
    reminder_minutes: int | None = Field(default=None, ge=5, le=1440)

    @field_validator("reminder_channels")
    @classmethod
    def channels_valid(cls, values: list[str] | None) -> list[str] | None:
        if values is None:
            return None
        values = list(dict.fromkeys(values))
        if not values or any(value not in REMINDER_CHANNELS for value in values):
            raise ValueError("提醒渠道仅支持邮件、站内和日历")
        return values


class JoinRequest(BaseModel):
    reminder_channels: list[str] = Field(default_factory=lambda: ["email", "in_app"], max_length=3)

    @field_validator("reminder_channels")
    @classmethod
    def channels_valid(cls, values: list[str]) -> list[str]:
        values = list(dict.fromkeys(values))
        if not values or any(value not in REMINDER_CHANNELS for value in values):
            raise ValueError("提醒渠道仅支持邮件、站内和日历")
        return values


class TransferRequest(BaseModel):
    user_id: int


class RatingRequest(BaseModel):
    target_user_id: int
    tags: list[str] = Field(min_length=1, max_length=4)


class TeamRunCreate(BaseModel):
    starts_at: datetime


class TeamRunUpdate(BaseModel):
    starts_at: datetime | None = None
    status: str | None = None

    @field_validator("status")
    @classmethod
    def status_valid(cls, value: str | None) -> str | None:
        if value is not None and value not in {"scheduled", "cancelled"}:
            raise ValueError("场次状态无效")
        return value


def active_members(db: Session, team_id: int) -> list[TeamMembership]:
    return db.scalars(
        select(TeamMembership).where(TeamMembership.team_id == team_id, TeamMembership.status == "active")
    ).all()


def current_run(db: Session, team_id: int) -> TeamRun | None:
    now = utcnow()
    return db.scalar(
        select(TeamRun)
        .where(
            TeamRun.team_id == team_id,
            TeamRun.status == "scheduled",
            (TeamRun.expires_at.is_(None) | (TeamRun.expires_at > now)),
        )
        .order_by(TeamRun.starts_at.asc())
    )


def team_payload(db: Session, team: Team, viewer: User | None = None) -> dict:
    entity = db.get(ContentEntity, team.entity_id)
    owner = db.get(User, team.owner_id)
    members = active_members(db, team.entity_id)
    run = current_run(db, team.entity_id)
    member_users = [db.get(User, x.user_id) for x in members]
    mine_membership = next((row for row in members if viewer and row.user_id == viewer.id), None)
    rating_tags = {
        tag: int(count)
        for tag, count in db.execute(
            select(TeamRating.tag, func.count(TeamRating.id))
            .where(TeamRating.target_id == team.owner_id)
            .group_by(TeamRating.tag)
        ).all()
    }
    run_counts = {
        status: int(count)
        for status, count in db.execute(
            select(TeamRun.status, func.count(TeamRun.id))
            .join(Team, Team.entity_id == TeamRun.team_id)
            .where(
                Team.owner_id == team.owner_id,
                TeamRun.starts_at < utcnow(),
                TeamRun.status.in_(["completed", "cancelled"]),
            )
            .group_by(TeamRun.status)
        ).all()
    }
    decided_runs = sum(run_counts.values())
    completion_rate = round(run_counts.get("completed", 0) * 100 / decided_runs) if decided_runs else None
    return {
        "id": team.entity_id,
        "game": team.game,
        "game_id": team.game_id,
        "mode": team.mode,
        "rank_requirement": team.rank_requirement,
        "capacity": team.capacity,
        "owner": {
            "id": owner.id,
            "nickname": owner.nickname,
            "credit": owner.credit,
            "verified": bool(owner.verified_at),
        } if owner else None,
        "completion_rate": completion_rate,
        "rating_tags": rating_tags,
        "voice_name": team.voice_name,
        "voice_link": team.voice_link if viewer and any(x.user_id == viewer.id for x in members) else "",
        "notes": team.notes,
        "newbie_level": team.newbie_level,
        "vibe": team.vibe,
        "reminder_channels": [x for x in team.reminder_channels.split(",") if x],
        "my_reminder_channels": [x for x in mine_membership.reminder_channels.split(",") if x] if mine_membership else [],
        "recurrence": team.recurrence,
        "reminder_minutes": team.reminder_minutes,
        "post_departure_retention_minutes": team.post_departure_retention_minutes,
        "status": team.status,
        "content_status": entity.status if entity else "deleted",
        "next_run": {
            "id": run.id,
            "starts_at": run.starts_at,
            "expires_at": run.expires_at or run_expires_at(team, run.starts_at),
            "status": run.status,
        } if run else None,
        "members": [{"id": u.id, "nickname": u.nickname, "credit": u.credit} for u in member_users if u],
        "member_count": len(members),
        "joined": bool(viewer and any(x.user_id == viewer.id for x in members)),
        "mine": bool(viewer and team.owner_id == viewer.id),
        "created_at": entity.created_at if entity else None,
    }


@router.get("")
def list_teams(
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=50),
    game: str = Query("", max_length=60),
    game_id: int | None = Query(None, ge=1),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    advance_team_lifecycles(db)
    filters = [ContentEntity.status == "published", Team.status == "active"]
    if game:
        filters.append(Team.game == game)
    if game_id:
        filters.append(Team.game_id == game_id)
    total = (
        db.scalar(
            select(func.count(Team.entity_id)).join(ContentEntity, ContentEntity.id == Team.entity_id).where(*filters)
        )
        or 0
    )
    rows = db.scalars(
        select(Team)
        .join(ContentEntity, ContentEntity.id == Team.entity_id)
        .where(*filters)
        .order_by(ContentEntity.created_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {"items": [team_payload(db, x, viewer) for x in rows], "page": page, "page_size": page_size, "total": total}


@router.post("", status_code=201)
def create_team(data: TeamCreate, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
    require_credit(db, user, "threshold.team_create", "创建车队")
    ensure_game_catalog(db)
    game = db.get(TeamGame, data.game_id) if data.game_id else None
    if not game and data.game.strip():
        alias = db.scalar(select(TeamGameAlias).where(TeamGameAlias.normalized_alias == normalize_game(data.game)))
        game = db.get(TeamGame, alias.game_id) if alias else None
    if not game or not game.active:
        raise APIError(400, "GAME_NOT_APPROVED", "请选择已审核的游戏，或先提交新游戏")
    starts_at = db_datetime(data.starts_at)
    if starts_at <= utcnow() + timedelta(minutes=10):
        raise APIError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
    entity, _ = create_entity(db, user.id, "team", f"{game.name} {data.mode} {data.notes}")
    team = Team(
        entity_id=entity.id,
        owner_id=user.id,
        game_id=game.id,
        game=game.name,
        mode=data.mode.strip(),
        rank_requirement=data.rank_requirement.strip(),
        capacity=data.capacity,
        voice_name=data.voice_name.strip(),
        voice_link=data.voice_link.strip(),
        notes=data.notes.strip(),
        newbie_level=data.newbie_level.strip(),
        vibe=data.vibe.strip(),
        reminder_channels=",".join(data.reminder_channels),
        recurrence=data.recurrence,
        reminder_minutes=data.reminder_minutes,
        post_departure_retention_minutes=data.post_departure_retention_minutes,
    )
    db.add(team)
    run = TeamRun(
        team_id=entity.id,
        starts_at=starts_at,
        expires_at=starts_at + timedelta(minutes=data.post_departure_retention_minutes),
    )
    membership = TeamMembership(team_id=entity.id, user_id=user.id, role="owner", reminder_channels=",".join(data.reminder_channels))
    db.add_all([run, membership])
    db.flush()
    db.add(TeamRunMember(run_id=run.id, user_id=user.id))
    audit(db, user.id, "team.create", "team", entity.id)
    return team_payload(db, team, user)


def run_payload(db: Session, run: TeamRun, viewer: User | None, include_members: bool = False) -> dict:
    rows = db.scalars(select(TeamRunMember).where(TeamRunMember.run_id == run.id)).all()
    mine = next((row for row in rows if viewer and row.user_id == viewer.id), None)
    payload = {
        "id": run.id,
        "team_id": run.team_id,
        "starts_at": run.starts_at,
        "expires_at": run.expires_at,
        "status": run.status,
        "member_count": len([row for row in rows if row.status not in {"left", "removed"}]),
        "my_status": mine.status if mine else None,
        "checked_in": bool(mine and mine.checked_in_at),
        "excused": bool(mine and mine.excused_at),
        "created_at": run.created_at,
    }
    if include_members:
        payload["members"] = [
            {
                "user_id": row.user_id,
                "nickname": (db.get(User, row.user_id).nickname if db.get(User, row.user_id) else "已注销用户"),
                "status": row.status,
                "checked_in_at": row.checked_in_at,
                "excused_at": row.excused_at,
                "credit_awarded": row.credit_awarded,
            }
            for row in rows
        ]
    return payload


@router.get("/{team_id}/runs")
def list_team_runs(
    team_id: int,
    page: int = Query(1, ge=1),
    page_size: int = Query(20, ge=1, le=100),
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    advance_team_lifecycles(db, team_id)
    team = db.get(Team, team_id)
    entity = db.get(ContentEntity, team_id)
    historical_member = bool(
        viewer
        and db.scalar(
            select(TeamMembership.id).where(
                TeamMembership.team_id == team_id,
                TeamMembership.user_id == viewer.id,
            )
        )
    )
    privileged = bool(
        viewer
        and team
        and (team.owner_id == viewer.id or viewer.role in {"moderator", "admin"} or historical_member)
    )
    if not team or not entity or (entity.status != "published" and not privileged):
        raise APIError(404, "TEAM_NOT_FOUND", "车队不存在")
    total = db.scalar(select(func.count(TeamRun.id)).where(TeamRun.team_id == team_id)) or 0
    rows = db.scalars(
        select(TeamRun)
        .where(TeamRun.team_id == team_id)
        .order_by(TeamRun.starts_at.desc())
        .offset((page - 1) * page_size)
        .limit(page_size)
    ).all()
    return {
        "items": [run_payload(db, row, viewer, privileged) for row in rows],
        "page": page,
        "page_size": page_size,
        "total": total,
    }


@router.post("/{team_id}/runs", status_code=201)
def create_team_run(
    team_id: int,
    data: TeamRunCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.scalar(select(Team).where(Team.entity_id == team_id).with_for_update())
    if not team or team.owner_id != user.id or team.status != "active":
        raise APIError(403, "OWNER_REQUIRED", "只有车头可以新增场次")
    starts_at = db_datetime(data.starts_at)
    if starts_at <= utcnow() + timedelta(minutes=10):
        raise APIError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
    existing = db.scalar(select(TeamRun).where(TeamRun.team_id == team_id, TeamRun.starts_at == starts_at))
    if existing:
        return run_payload(db, existing, user, True)
    run = TeamRun(team_id=team_id, starts_at=starts_at, expires_at=run_expires_at(team, starts_at))
    db.add(run)
    db.flush()
    db.add_all([TeamRunMember(run_id=run.id, user_id=row.user_id) for row in active_members(db, team_id)])
    db.flush()
    audit(db, user.id, "team_run.create", "team_run", run.id, after={"starts_at": starts_at})
    return run_payload(db, run, user, True)


@router.patch("/{team_id}/runs/{run_id}")
def update_team_run(
    team_id: int,
    run_id: int,
    data: TeamRunUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.scalar(select(Team).where(Team.entity_id == team_id).with_for_update())
    run = db.scalar(select(TeamRun).where(TeamRun.id == run_id).with_for_update())
    if not team or team.owner_id != user.id:
        raise APIError(403, "OWNER_REQUIRED", "只有车头可以修改场次")
    if not run or run.team_id != team_id:
        raise APIError(404, "RUN_NOT_FOUND", "发车场次不存在")
    if run.status != "scheduled":
        return run_payload(db, run, user, True)
    before = {"starts_at": run.starts_at, "status": run.status}
    if data.starts_at is not None:
        starts_at = db_datetime(data.starts_at)
        if starts_at <= utcnow() + timedelta(minutes=10):
            raise APIError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
        duplicate = db.scalar(
            select(TeamRun.id).where(
                TeamRun.team_id == team_id,
                TeamRun.starts_at == starts_at,
                TeamRun.id != run_id,
            )
        )
        if duplicate:
            raise APIError(409, "RUN_TIME_CONFLICT", "该时间已有发车场次")
        run.starts_at = starts_at
        run.expires_at = run_expires_at(team, starts_at)
        run.reminder_sent_at = None
    if data.status == "cancelled":
        run.status = "cancelled"
        members = db.scalars(select(TeamRunMember).where(TeamRunMember.run_id == run.id)).all()
        for member in members:
            if member.status not in {"left", "removed"}:
                member.status = "cancelled"
                notify(db, member.user_id, "发车场次已取消", f"{team.game} · {team.mode} 的本次场次已取消", f"/teams/{team_id}", "team")
    audit(
        db,
        user.id,
        "team_run.update",
        "team_run",
        run.id,
        before=before,
        after={"starts_at": run.starts_at, "status": run.status},
    )
    return run_payload(db, run, user, True)


@router.get("/{team_id}/members/history")
def member_history(
    team_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.get(Team, team_id)
    if not team or (team.owner_id != user.id and user.role not in {"moderator", "admin"}):
        raise APIError(403, "OWNER_REQUIRED", "无权查看成员历史")
    rows = db.scalars(
        select(TeamMembership).where(TeamMembership.team_id == team_id).order_by(TeamMembership.joined_at.desc())
    ).all()
    return {
        "items": [
            {
                "id": row.id,
                "user_id": row.user_id,
                "nickname": (db.get(User, row.user_id).nickname if db.get(User, row.user_id) else "已注销用户"),
                "role": row.role,
                "status": row.status,
                "joined_at": row.joined_at,
                "left_at": row.left_at,
            }
            for row in rows
        ],
        "page": 1,
        "page_size": len(rows) or 20,
        "total": len(rows),
    }


@router.get("/{team_id}")
def get_team(team_id: int, viewer: User | None = Depends(optional_user), db: Session = Depends(get_db)) -> dict:
    advance_team_lifecycles(db, team_id)
    team = db.get(Team, team_id)
    entity = db.get(ContentEntity, team_id)
    historical_member = bool(
        viewer
        and db.scalar(
            select(TeamMembership.id).where(
                TeamMembership.team_id == team_id,
                TeamMembership.user_id == viewer.id,
            )
        )
    )
    privileged = bool(
        viewer
        and team
        and (team.owner_id == viewer.id or viewer.role in {"moderator", "admin"} or historical_member)
    )
    if not team or not entity or (entity.status != "published" and not privileged):
        raise APIError(404, "TEAM_NOT_FOUND", "车队不存在")
    return team_payload(db, team, viewer)


@router.patch("/{team_id}")
def update_team(
    team_id: int,
    data: TeamUpdate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.get(Team, team_id)
    if not team:
        raise APIError(404, "TEAM_NOT_FOUND", "车队不存在")
    if team.owner_id != user.id and user.role not in {"moderator", "admin"}:
        raise APIError(403, "OWNER_REQUIRED", "只有车头可以修改车队")
    if data.capacity is not None and data.capacity < len(active_members(db, team_id)):
        raise APIError(400, "CAPACITY_TOO_SMALL", "容量不能小于当前成员数")
    values = data.model_dump(exclude_none=True)
    if "game_id" in values:
        game = db.get(TeamGame, values.pop("game_id"))
        if not game or not game.active:
            raise APIError(404, "TEAM_GAME_NOT_FOUND", "游戏不存在或尚未审核")
        team.game_id = game.id
        team.game = game.name
    if "reminder_channels" in values:
        values["reminder_channels"] = ",".join(values["reminder_channels"])
    for key, value in values.items():
        setattr(team, key, value.strip() if isinstance(value, str) else value)
    for member in active_members(db, team_id):
        if member.user_id != user.id:
            notify(
                db,
                member.user_id,
                "车队信息已更新",
                f"{team.game} · {team.mode} 的车头更新了车队信息",
                f"/teams/{team_id}",
                "team",
            )
    audit(db, user.id, "team.update", "team", team_id)
    touch_entity(db, team_id)
    return team_payload(db, team, user)


@router.post("/{team_id}/join")
def join_team(
    team_id: int,
    data: JoinRequest | None = None,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    advance_team_lifecycles(db, team_id)
    team = db.scalar(select(Team).where(Team.entity_id == team_id).with_for_update())
    entity = db.get(ContentEntity, team_id)
    if not team or not entity or entity.status != "published" or team.status != "active":
        raise APIError(404, "TEAM_NOT_FOUND", "车队不存在或已关闭")
    existing = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == user.id,
            TeamMembership.status == "active",
        )
    )
    if existing:
        if data:
            existing.reminder_channels = ",".join(data.reminder_channels)
        return team_payload(db, team, user)
    if len(active_members(db, team_id)) >= team.capacity:
        raise APIError(409, "TEAM_FULL", "车队已满员")
    run = current_run(db, team_id)
    if not run or run.starts_at <= utcnow():
        raise APIError(409, "TEAM_ALREADY_DEPARTED", "车队已经发车，不能继续上车")
    membership = TeamMembership(
        team_id=team_id,
        user_id=user.id,
        reminder_channels=",".join((data or JoinRequest()).reminder_channels),
    )
    db.add(membership)
    if run:
        run_member = db.scalar(
            select(TeamRunMember).where(TeamRunMember.run_id == run.id, TeamRunMember.user_id == user.id)
        )
        if run_member:
            run_member.status = "joined"
            run_member.excused_at = None
        else:
            db.add(TeamRunMember(run_id=run.id, user_id=user.id))
    notify(
        db, team.owner_id, "有新成员上车", f"{user.nickname} 加入了你的 {team.game} 车队", f"/teams/{team_id}", "team"
    )
    notify(db, user.id, "上车成功", f"你已加入 {team.game} · {team.mode}，请留意发车提醒", f"/teams/{team_id}", "team")
    touch_entity(db, team_id)
    return team_payload(db, team, user)


def ics_escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace(";", "\\;").replace(",", "\\,").replace("\n", "\\n")


@router.get("/{team_id}/calendar.ics")
def team_calendar(
    team_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> Response:
    team = db.get(Team, team_id)
    run = current_run(db, team_id)
    membership = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == user.id,
            TeamMembership.status == "active",
        )
    )
    if not team or not run or not membership:
        raise APIError(404, "TEAM_RUN_NOT_FOUND", "只有当前车队成员可以订阅发车日历")
    start = run.starts_at.strftime("%Y%m%dT%H%M%SZ")
    end = (run.starts_at + timedelta(hours=2)).strftime("%Y%m%dT%H%M%SZ")
    description = f"{team.mode}；段位 {team.rank_requirement}；语音 {team.voice_name or '待通知'}；{team.notes}"
    content = "\r\n".join([
        "BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Wutong Wall//Team Calendar//ZH-CN",
        "CALSCALE:GREGORIAN", "BEGIN:VEVENT", f"UID:team-{team_id}-run-{run.id}@wutong-wall",
        f"DTSTAMP:{utcnow().strftime('%Y%m%dT%H%M%SZ')}", f"DTSTART:{start}", f"DTEND:{end}",
        f"SUMMARY:{ics_escape(team.game + ' · ' + team.mode)}", f"DESCRIPTION:{ics_escape(description)}",
        "END:VEVENT", "END:VCALENDAR", "",
    ])
    return Response(
        content=content,
        media_type="text/calendar; charset=utf-8",
        headers={"Content-Disposition": f'attachment; filename="wutong-team-{team_id}.ics"'},
    )


@router.post("/{team_id}/leave")
def leave_team(team_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    team = db.get(Team, team_id)
    if not team:
        raise APIError(404, "TEAM_NOT_FOUND", "车队不存在")
    if team.owner_id == user.id:
        raise APIError(400, "OWNER_CANNOT_LEAVE", "车头需先转让或取消车队")
    membership = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == user.id,
            TeamMembership.status == "active",
        )
    )
    if not membership:
        return {"ok": True, "credit_delta": 0}
    run = current_run(db, team_id)
    penalty = 0
    if run:
        run_member = db.scalar(
            select(TeamRunMember).where(TeamRunMember.run_id == run.id, TeamRunMember.user_id == user.id)
        )
        if run_member:
            if run.starts_at - timedelta(minutes=30) <= utcnow() < run.starts_at and not run_member.excused_at:
                penalty = apply_credit_rule(
                    db,
                    user,
                    "penalty.team_late_leave",
                    target_type="team_run",
                    target_id=run.id,
                )
            run_member.status = "left"
    membership.status = "left"
    membership.left_at = utcnow()
    notify(db, team.owner_id, "成员已下车", f"{user.nickname} 退出了 {team.game} 车队", f"/teams/{team_id}", "team")
    touch_entity(db, team_id)
    return {"ok": True, "credit_delta": penalty}


@router.post("/{team_id}/runs/{run_id}/excuse")
def excuse(team_id: int, run_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    run = db.get(TeamRun, run_id)
    member = db.scalar(select(TeamRunMember).where(TeamRunMember.run_id == run_id, TeamRunMember.user_id == user.id))
    if not run or run.team_id != team_id or not member or member.status != "joined":
        raise APIError(403, "RUN_MEMBER_REQUIRED", "只有本场成员可以请假")
    if utcnow() >= run.starts_at:
        raise APIError(400, "RUN_STARTED", "发车后不能请假")
    member.excused_at = member.excused_at or utcnow()
    member.status = "excused"
    team = db.get(Team, team_id)
    notify(db, team.owner_id, "成员请假", f"{user.nickname} 已为本次发车请假", f"/teams/{team_id}", "team")
    touch_entity(db, team_id)
    return {"ok": True}


@router.post("/{team_id}/runs/{run_id}/check-in")
def check_in(team_id: int, run_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    run = db.get(TeamRun, run_id)
    if not run or run.team_id != team_id:
        raise APIError(404, "RUN_NOT_FOUND", "发车场次不存在")
    member = db.scalar(
        select(TeamRunMember).where(TeamRunMember.run_id == run_id, TeamRunMember.user_id == user.id).with_for_update()
    )
    if not member or member.status not in {"joined", "checked_in"}:
        raise APIError(403, "RUN_MEMBER_REQUIRED", "只有本场成员可以签到")
    if abs((run.starts_at - utcnow()).total_seconds()) > 1800:
        raise APIError(400, "OUTSIDE_CHECKIN_WINDOW", "仅可在发车前后 30 分钟签到")
    if not member.checked_in_at:
        member.checked_in_at = utcnow()
        member.status = "checked_in"
    if not member.credit_awarded:
        delta = apply_credit_rule(
            db,
            user,
            "reward.team_check_in",
            target_type="team_run",
            target_id=run.id,
        )
        member.credit_awarded = True
    else:
        delta = 0
    touch_entity(db, team_id)
    return {"ok": True, "credit_delta": delta, "checked_in_at": member.checked_in_at}


@router.post("/{team_id}/transfer")
def transfer_team(
    team_id: int,
    data: TransferRequest,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.get(Team, team_id)
    if not team or team.owner_id != user.id:
        raise APIError(403, "OWNER_REQUIRED", "只有车头可以转让车队")
    target = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == data.user_id,
            TeamMembership.status == "active",
        )
    )
    current = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == user.id,
            TeamMembership.status == "active",
        )
    )
    if not target:
        raise APIError(400, "TARGET_NOT_MEMBER", "新车头必须是当前成员")
    team.owner_id = data.user_id
    target.role = "owner"
    if current:
        current.role = "member"
    notify(db, data.user_id, "你已成为车头", f"{team.game} · {team.mode} 已转让给你", f"/teams/{team_id}", "team")
    audit(db, user.id, "team.transfer", "team", team_id, after={"owner_id": data.user_id})
    touch_entity(db, team_id)
    return team_payload(db, team, user)


@router.post("/{team_id}/members/{member_id}/remove")
def remove_member(
    team_id: int,
    member_id: int,
    user: User = Depends(current_user),
    db: Session = Depends(get_db),
) -> dict:
    team = db.get(Team, team_id)
    if not team or (team.owner_id != user.id and user.role not in {"moderator", "admin"}):
        raise APIError(403, "OWNER_REQUIRED", "无权移除成员")
    if member_id == team.owner_id:
        raise APIError(400, "CANNOT_REMOVE_OWNER", "不能移除车头")
    member = db.scalar(
        select(TeamMembership).where(
            TeamMembership.team_id == team_id,
            TeamMembership.user_id == member_id,
            TeamMembership.status == "active",
        )
    )
    if member:
        member.status = "removed"
        member.left_at = utcnow()
    run = current_run(db, team_id)
    if run:
        run_member = db.scalar(
            select(TeamRunMember).where(TeamRunMember.run_id == run.id, TeamRunMember.user_id == member_id)
        )
        if run_member:
            run_member.status = "removed"
    notify(db, member_id, "你已被移出车队", f"你已被移出 {team.game} · {team.mode}", "/teams", "team")
    touch_entity(db, team_id)
    return {"ok": True}


@router.post("/{team_id}/cancel")
def cancel_team(team_id: int, user: User = Depends(current_user), db: Session = Depends(get_db)) -> dict:
    team = db.get(Team, team_id)
    if not team or (team.owner_id != user.id and user.role not in {"moderator", "admin"}):
        raise APIError(403, "OWNER_REQUIRED", "无权取消车队")
    team.status = "cancelled"
    entity = db.get(ContentEntity, team_id)
    entity.status = "hidden"
    run = current_run(db, team_id)
    if run:
        run.status = "cancelled"
    for member in active_members(db, team_id):
        notify(db, member.user_id, "车队已取消", f"{team.game} · {team.mode} 已取消", "/teams", "team")
        member.status = "cancelled"
        member.left_at = utcnow()
    audit(db, user.id, "team.cancel", "team", team_id)
    return {"ok": True}


@router.post("/{team_id}/runs/{run_id}/ratings")
def rate_member(
    team_id: int,
    run_id: int,
    data: RatingRequest,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    invalid = set(data.tags) - RATING_TAGS
    if invalid:
        raise APIError(400, "INVALID_RATING_TAG", "评价标签无效")
    if data.target_user_id == user.id:
        raise APIError(400, "SELF_RATING", "不能评价自己")
    run = db.get(TeamRun, run_id)
    if not run or run.team_id != team_id or utcnow() < run.starts_at:
        raise APIError(400, "RATING_NOT_OPEN", "发车后才能评价")
    members = db.scalars(
        select(TeamRunMember).where(
            TeamRunMember.run_id == run_id,
            TeamRunMember.user_id.in_([user.id, data.target_user_id]),
            TeamRunMember.status.in_(["joined", "checked_in", "excused"]),
        )
    ).all()
    if len(members) != 2:
        raise APIError(403, "SAME_RUN_REQUIRED", "只能评价同场队友")
    for tag in set(data.tags):
        insert_unique(
            db,
            TeamRating.__table__,
            {"run_id": run_id, "rater_id": user.id, "target_id": data.target_user_id, "tag": tag},
            ["run_id", "rater_id", "target_id", "tag"],
        )
    return {"ok": True}
