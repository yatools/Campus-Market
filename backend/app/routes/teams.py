from __future__ import annotations

from datetime import datetime, timedelta

from fastapi import APIRouter, Depends, Query
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import current_user, optional_user, participating_user
from ..errors import APIError
from ..models import (
    ContentEntity,
    Team,
    TeamMembership,
    TeamRating,
    TeamRun,
    TeamRunMember,
    User,
    db_datetime,
    utcnow,
)
from ..services import audit, create_entity, insert_unique, notify

router = APIRouter(prefix="/teams", tags=["车队"])
RATING_TAGS = {"friendly", "communication", "skill", "punctual"}


class TeamCreate(BaseModel):
    game: str = Field(min_length=1, max_length=60)
    mode: str = Field(min_length=1, max_length=80)
    rank_requirement: str = Field(default="不限", max_length=80)
    capacity: int = Field(default=5, ge=2, le=99)
    starts_at: datetime
    recurrence: str = "once"
    voice_name: str = Field(default="", max_length=80)
    voice_link: str = Field(default="", max_length=500)
    notes: str = Field(default="", max_length=2000)
    reminder_minutes: int = Field(default=30, ge=5, le=1440)

    @field_validator("recurrence")
    @classmethod
    def recurrence_valid(cls, value: str) -> str:
        if value not in {"once", "weekly"}:
            raise ValueError("仅支持单次或每周车队")
        return value


class TeamUpdate(BaseModel):
    mode: str | None = Field(default=None, min_length=1, max_length=80)
    rank_requirement: str | None = Field(default=None, max_length=80)
    capacity: int | None = Field(default=None, ge=2, le=99)
    voice_name: str | None = Field(default=None, max_length=80)
    voice_link: str | None = Field(default=None, max_length=500)
    notes: str | None = Field(default=None, max_length=2000)
    reminder_minutes: int | None = Field(default=None, ge=5, le=1440)


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
    return db.scalar(
        select(TeamRun)
        .where(TeamRun.team_id == team_id, TeamRun.status == "scheduled")
        .order_by(TeamRun.starts_at.asc())
    )


def team_payload(db: Session, team: Team, viewer: User | None = None) -> dict:
    entity = db.get(ContentEntity, team.entity_id)
    owner = db.get(User, team.owner_id)
    members = active_members(db, team.entity_id)
    run = current_run(db, team.entity_id)
    member_users = [db.get(User, x.user_id) for x in members]
    return {
        "id": team.entity_id,
        "game": team.game,
        "mode": team.mode,
        "rank_requirement": team.rank_requirement,
        "capacity": team.capacity,
        "owner": {"id": owner.id, "nickname": owner.nickname} if owner else None,
        "voice_name": team.voice_name,
        "voice_link": team.voice_link if viewer and any(x.user_id == viewer.id for x in members) else "",
        "notes": team.notes,
        "recurrence": team.recurrence,
        "reminder_minutes": team.reminder_minutes,
        "status": team.status,
        "content_status": entity.status if entity else "deleted",
        "next_run": {"id": run.id, "starts_at": run.starts_at, "status": run.status} if run else None,
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
    viewer: User | None = Depends(optional_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [ContentEntity.status == "published", Team.status == "active"]
    if game:
        filters.append(Team.game == game)
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
    if user.credit < 60:
        raise APIError(403, "CREDIT_REQUIRED", "创建车队需要信用分不低于 60")
    starts_at = db_datetime(data.starts_at)
    if starts_at <= utcnow() + timedelta(minutes=10):
        raise APIError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
    entity, _ = create_entity(db, user.id, "team", f"{data.game} {data.mode} {data.notes}")
    team = Team(
        entity_id=entity.id,
        owner_id=user.id,
        game=data.game.strip(),
        mode=data.mode.strip(),
        rank_requirement=data.rank_requirement.strip(),
        capacity=data.capacity,
        voice_name=data.voice_name.strip(),
        voice_link=data.voice_link.strip(),
        notes=data.notes.strip(),
        recurrence=data.recurrence,
        reminder_minutes=data.reminder_minutes,
    )
    db.add(team)
    run = TeamRun(team_id=entity.id, starts_at=starts_at)
    membership = TeamMembership(team_id=entity.id, user_id=user.id, role="owner")
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
    team = db.get(Team, team_id)
    entity = db.get(ContentEntity, team_id)
    privileged = bool(viewer and team and (team.owner_id == viewer.id or viewer.role in {"moderator", "admin"}))
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
    run = TeamRun(team_id=team_id, starts_at=starts_at)
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
    team = db.get(Team, team_id)
    entity = db.get(ContentEntity, team_id)
    if not team or not entity or entity.status != "published":
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
    for key, value in data.model_dump(exclude_none=True).items():
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
    return team_payload(db, team, user)


@router.post("/{team_id}/join")
def join_team(team_id: int, user: User = Depends(participating_user), db: Session = Depends(get_db)) -> dict:
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
        return team_payload(db, team, user)
    if len(active_members(db, team_id)) >= team.capacity:
        raise APIError(409, "TEAM_FULL", "车队已满员")
    membership = TeamMembership(team_id=team_id, user_id=user.id)
    db.add(membership)
    run = current_run(db, team_id)
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
    return team_payload(db, team, user)


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
                penalty = -3
                user.credit = max(0, user.credit + penalty)
            run_member.status = "left"
    membership.status = "left"
    membership.left_at = utcnow()
    notify(db, team.owner_id, "成员已下车", f"{user.nickname} 退出了 {team.game} 车队", f"/teams/{team_id}", "team")
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
        user.credit = min(100, user.credit + 1)
        member.credit_awarded = True
        delta = 1
    else:
        delta = 0
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
