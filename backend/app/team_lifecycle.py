from __future__ import annotations

from datetime import datetime, timedelta

from sqlalchemy import select
from sqlalchemy.orm import Session

from .models import ContentEntity, Team, TeamMembership, TeamRun, TeamRunMember, utcnow


def run_expires_at(team: Team, starts_at: datetime) -> datetime:
    return starts_at + timedelta(minutes=team.post_departure_retention_minutes)


def advance_team_lifecycles(db: Session, team_id: int | None = None, now: datetime | None = None) -> None:
    current = now or utcnow()
    filters = [Team.status == "active"]
    if team_id is not None:
        filters.append(Team.entity_id == team_id)
    teams = db.scalars(select(Team).where(*filters).with_for_update()).all()
    for team in teams:
        runs = db.scalars(
            select(TeamRun)
            .where(TeamRun.team_id == team.entity_id, TeamRun.status == "scheduled")
            .order_by(TeamRun.starts_at)
            .with_for_update()
        ).all()
        expired: list[TeamRun] = []
        for run in runs:
            if run.expires_at is None:
                run.expires_at = run_expires_at(team, run.starts_at)
            if run.expires_at <= current:
                run.status = "completed"
                expired.append(run)

        if not expired:
            continue
        if team.recurrence == "once":
            team.status = "archived"
            entity = db.get(ContentEntity, team.entity_id)
            if entity and entity.status == "published":
                entity.status = "expired"
                entity.search_visible = False
            continue

        remaining = next(
            (
                run.id
                for run in runs
                if run.status == "scheduled" and run.expires_at is not None and run.expires_at > current
            ),
            None,
        )
        if remaining:
            continue
        next_start = max(run.starts_at for run in expired) + timedelta(days=7)
        while run_expires_at(team, next_start) <= current:
            next_start += timedelta(days=7)
        next_run = db.scalar(
            select(TeamRun).where(TeamRun.team_id == team.entity_id, TeamRun.starts_at == next_start)
        )
        if not next_run:
            next_run = TeamRun(
                team_id=team.entity_id,
                starts_at=next_start,
                expires_at=run_expires_at(team, next_start),
            )
            db.add(next_run)
            db.flush()
            members = db.scalars(
                select(TeamMembership).where(
                    TeamMembership.team_id == team.entity_id,
                    TeamMembership.status == "active",
                )
            ).all()
            db.add_all([TeamRunMember(run_id=next_run.id, user_id=member.user_id) for member in members])
    db.flush()
