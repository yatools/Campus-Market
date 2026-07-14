from __future__ import annotations

from fastapi import APIRouter, Depends, Query
from pydantic import BaseModel, Field, field_validator
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from ..database import get_db
from ..deps import admin_user, participating_user
from ..errors import APIError
from ..models import GameSubmission, Team, TeamGame, TeamGameAlias, User, utcnow
from ..services import audit, check_rate_limit, notify

router = APIRouter(tags=["游戏目录"])
admin_router = APIRouter(prefix="/admin", tags=["管理后台"])
DEFAULT_GAMES = [
    ("英雄联盟", ["LOL", "League of Legends"]), ("无畏契约", ["瓦", "Valorant"]),
    ("CS2", ["Counter-Strike 2", "CS"]), ("原神", ["Genshin Impact"]),
    ("崩坏：星穹铁道", ["星铁", "星穹铁道"]), ("王者荣耀", ["王者"]),
    ("雀魂", ["Mahjong Soul"]), ("Minecraft", ["MC", "我的世界"]),
    ("饥荒", ["Don't Starve Together", "DST"]), ("DND", ["D&D", "龙与地下城"]),
]


def normalize_game(value: str) -> str:
    return "".join(value.casefold().split())


def ensure_game_catalog(db: Session) -> None:
    if db.scalar(select(func.count(TeamGame.id))):
        return
    for name, aliases in DEFAULT_GAMES:
        game = TeamGame(name=name)
        db.add(game)
        db.flush()
        for alias in [name, *aliases]:
            db.add(TeamGameAlias(game_id=game.id, alias=alias, normalized_alias=normalize_game(alias)))


def game_payload(db: Session, game: TeamGame) -> dict:
    aliases = db.scalars(
        select(TeamGameAlias.alias).where(TeamGameAlias.game_id == game.id).order_by(TeamGameAlias.id)
    ).all()
    return {"id": game.id, "name": game.name, "aliases": aliases, "active": game.active}


class GameSubmissionCreate(BaseModel):
    name: str = Field(min_length=1, max_length=80)
    aliases: list[str] = Field(default_factory=list, max_length=10)

    @field_validator("aliases")
    @classmethod
    def clean_aliases(cls, values: list[str]) -> list[str]:
        result: list[str] = []
        for value in values:
            value = value.strip()
            if value and value not in result:
                if len(value) > 80:
                    raise ValueError("游戏别名不能超过 80 个字符")
                result.append(value)
        return result


class GameSubmissionDecision(BaseModel):
    action: str
    target_game_id: int | None = None
    canonical_name: str = Field(default="", max_length=80)
    admin_note: str = Field(default="", max_length=1000)

    @field_validator("action")
    @classmethod
    def valid_action(cls, value: str) -> str:
        if value not in {"approve_new", "merge", "reject"}:
            raise ValueError("游戏审核动作无效")
        return value


@router.get("/team-games")
def list_team_games(db: Session = Depends(get_db)) -> dict:
    ensure_game_catalog(db)
    games = db.scalars(select(TeamGame).where(TeamGame.active.is_(True)).order_by(TeamGame.id)).all()
    return {"items": [game_payload(db, game) for game in games], "total": len(games)}


@router.post("/game-submissions", status_code=201)
def submit_game(
    data: GameSubmissionCreate,
    user: User = Depends(participating_user),
    db: Session = Depends(get_db),
) -> dict:
    ensure_game_catalog(db)
    check_rate_limit(db, "game_submission", str(user.id), 5, 24 * 60)
    name = data.name.strip()
    keys = {normalize_game(name), *(normalize_game(value) for value in data.aliases)}
    existing = db.scalar(select(TeamGameAlias).where(TeamGameAlias.normalized_alias.in_(keys)))
    if existing:
        game = db.get(TeamGame, existing.game_id)
        raise APIError(409, "GAME_EXISTS", f"该游戏已收录为“{game.name if game else existing.alias}”")
    pending = db.scalar(
        select(GameSubmission).where(
            GameSubmission.submitter_id == user.id,
            GameSubmission.status == "pending",
            GameSubmission.proposed_name == name,
        )
    )
    if pending:
        raise APIError(409, "GAME_SUBMISSION_EXISTS", "相同游戏已在审核中")
    row = GameSubmission(submitter_id=user.id, proposed_name=name, aliases=data.aliases)
    db.add(row)
    db.flush()
    audit(db, user.id, "game_submission.create", "game_submission", row.id)
    return {"id": row.id, "status": row.status, "name": row.proposed_name, "aliases": row.aliases}


@admin_router.get("/game-submissions")
def list_game_submissions(
    status: str = Query("pending", max_length=20),
    page: int = Query(1, ge=1),
    page_size: int = Query(50, ge=1, le=100),
    _: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    filters = [GameSubmission.status == status] if status else []
    total = db.scalar(select(func.count(GameSubmission.id)).where(*filters)) or 0
    rows = db.scalars(
        select(GameSubmission).where(*filters).order_by(GameSubmission.created_at.desc())
        .offset((page - 1) * page_size).limit(page_size)
    ).all()
    return {
        "items": [
            {
                "id": row.id, "submitter_id": row.submitter_id, "name": row.proposed_name,
                "aliases": row.aliases, "status": row.status, "resolved_game_id": row.resolved_game_id,
                "admin_note": row.admin_note, "created_at": row.created_at, "reviewed_at": row.reviewed_at,
            }
            for row in rows
        ],
        "page": page, "page_size": page_size, "total": total,
    }


def bind_alias(db: Session, game: TeamGame, alias: str) -> None:
    key = normalize_game(alias)
    existing = db.scalar(select(TeamGameAlias).where(TeamGameAlias.normalized_alias == key))
    if existing and existing.game_id != game.id:
        other = db.get(TeamGame, existing.game_id)
        raise APIError(409, "GAME_ALIAS_CONFLICT", f"别名“{alias}”已属于“{other.name if other else existing.alias}”")
    if not existing:
        db.add(TeamGameAlias(game_id=game.id, alias=alias.strip(), normalized_alias=key))


@admin_router.post("/game-submissions/{submission_id}/decision")
def decide_game_submission(
    submission_id: int,
    data: GameSubmissionDecision,
    admin: User = Depends(admin_user),
    db: Session = Depends(get_db),
) -> dict:
    row = db.scalar(select(GameSubmission).where(GameSubmission.id == submission_id).with_for_update())
    if not row:
        raise APIError(404, "GAME_SUBMISSION_NOT_FOUND", "游戏提交不存在")
    if row.status != "pending":
        raise APIError(409, "GAME_SUBMISSION_DECIDED", "该提交已经处理")
    game: TeamGame | None = None
    if data.action == "approve_new":
        name = data.canonical_name.strip() or row.proposed_name
        if db.scalar(select(TeamGame.id).where(TeamGame.name == name)):
            raise APIError(409, "GAME_EXISTS", "规范游戏名称已存在，请改为合并")
        game = TeamGame(name=name)
        db.add(game)
        db.flush()
        row.status = "approved"
    elif data.action == "merge":
        game = db.get(TeamGame, data.target_game_id)
        if not game or not game.active:
            raise APIError(404, "TEAM_GAME_NOT_FOUND", "目标游戏不存在")
        row.status = "merged"
    else:
        row.status = "rejected"
    if game:
        all_aliases = [game.name, row.proposed_name, *row.aliases]
        for alias in all_aliases:
            bind_alias(db, game, alias)
        keys = {normalize_game(alias) for alias in all_aliases}
        for team in db.scalars(select(Team)).all():
            if normalize_game(team.game) in keys:
                team.game_id = game.id
                team.game = game.name
        row.resolved_game_id = game.id
    row.reviewer_id = admin.id
    row.admin_note = data.admin_note.strip()
    row.reviewed_at = utcnow()
    notify(
        db, row.submitter_id, "新游戏提交已处理",
        f"“{row.proposed_name}”已{ '收录' if row.status == 'approved' else '合并' if row.status == 'merged' else '驳回' }。{row.admin_note}",
        "/teams", "game_submission",
    )
    audit(db, admin.id, "game_submission.decide", "game_submission", row.id, after={"status": row.status, "game_id": row.resolved_game_id})
    return {"id": row.id, "status": row.status, "game": game_payload(db, game) if game else None}
