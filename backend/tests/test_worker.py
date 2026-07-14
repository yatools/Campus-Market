from __future__ import annotations

import zipfile
from datetime import timedelta
from types import SimpleNamespace

from sqlalchemy import func, select

from app.database import SessionLocal
from app.models import (
    ContentEntity,
    EmailOutbox,
    Notification,
    Post,
    Team,
    TeamMembership,
    TeamRun,
    TeamRunMember,
    User,
    utcnow,
)
from app.security import hash_password
from app.worker import cleanup, create_backup_archive, process_team_runs


def make_user(db, email: str, nickname: str) -> User:
    user = User(
        email=email,
        password_hash=hash_password("WorkerPassword123"),
        nickname=nickname,
        alias=f"梧桐#{nickname}",
        credit=800,
    )
    db.add(user)
    db.flush()
    return user


def test_worker_sends_team_reminder_and_rolls_weekly_run() -> None:
    with SessionLocal() as db:
        owner = make_user(db, "worker-owner@test.edu.cn", "车头")
        member = make_user(db, "worker-member@test.edu.cn", "队员")
        entity = ContentEntity(owner_id=owner.id, type="team", status="published")
        db.add(entity)
        db.flush()
        team = Team(
            entity_id=entity.id,
            owner_id=owner.id,
            game="LOL",
            mode="固定队",
            recurrence="weekly",
            capacity=5,
            reminder_minutes=30,
        )
        db.add(team)
        run = TeamRun(team_id=entity.id, starts_at=utcnow() + timedelta(minutes=10))
        db.add(run)
        db.flush()
        db.add_all(
            [
                TeamMembership(team_id=entity.id, user_id=owner.id, role="owner"),
                TeamMembership(team_id=entity.id, user_id=member.id),
                TeamRunMember(run_id=run.id, user_id=owner.id),
                TeamRunMember(run_id=run.id, user_id=member.id),
            ]
        )
        db.flush()
        process_team_runs(db)
        db.flush()
        assert run.reminder_sent_at is not None
        assert db.scalar(select(func.count(Notification.id))) == 2
        assert db.scalar(select(func.count(EmailOutbox.id))) == 2

        run.starts_at = utcnow() - timedelta(hours=3)
        run.expires_at = run.starts_at + timedelta(minutes=team.post_departure_retention_minutes)
        process_team_runs(db)
        db.flush()
        assert run.status == "completed"
        next_run = db.scalar(select(TeamRun).where(TeamRun.team_id == entity.id, TeamRun.status == "scheduled"))
        assert next_run is not None
        assert db.scalar(select(func.count(TeamRunMember.id)).where(TeamRunMember.run_id == next_run.id)) == 2


def test_worker_expires_posts_and_anonymizes_deactivated_accounts() -> None:
    with SessionLocal() as db:
        user = make_user(db, "cleanup@test.edu.cn", "待注销")
        user.status = "disabled"
        user.deactivated_at = utcnow() - timedelta(days=31)
        entity = ContentEntity(owner_id=user.id, type="post", status="published")
        db.add(entity)
        db.flush()
        db.add(
            Post(
                entity_id=entity.id,
                board="treehole",
                body="过期内容",
                identity_mode="anonymous",
                expires_at=utcnow() - timedelta(minutes=1),
            )
        )
        db.flush()
        cleanup(db)
        db.flush()
        assert entity.status == "expired"
        assert user.status == "deleted"
        assert user.email is None
        assert user.nickname == "已注销用户"


def test_backup_contains_integrity_manifest() -> None:
    archive = create_backup_archive(SimpleNamespace(id=999))
    try:
        with zipfile.ZipFile(archive) as package:
            assert package.testzip() is None
            names = set(package.namelist())
            assert {"database.dump", "SHA256SUMS"} <= names
            checksums = package.read("SHA256SUMS").decode()
            assert "database.dump" in checksums
    finally:
        archive.unlink(missing_ok=True)
